// Package fdb implements the bridge forwarding database collector.
//
// This is the single most important discovery source for Lanory: it is the only
// one that answers "which physical port is this device behind?". Everything the
// UI draws between a router port and an end device comes from here.
//
// What the collector must get right:
//
//   - Filter noise. The bridge FDB contains multicast addresses, the bridge's
//     own MACs on its ports, zero MACs and the MACs of virtual interfaces. None
//     of them are devices. A device that appears because of a parsing mistake
//     is a device the user has to delete by hand, forever.
//   - Never collapse. One port with five learned MACs is five observations, not
//     one "WiFi" assumption. The collector has no idea whether the port feeds a
//     switch, an access point or five devices behind a hub, so it reports what
//     it saw and lets the topology engine decide.
//   - Report the bridge self entries as such. A bridge's own MAC learned on one
//     of its ports is the bridge, not a device behind the port.
//
// The FDB alone never proves a device is a wireless client: a port can carry a
// switch, an AP or a wired PC. Wireless association requires an explicit source
// (a wireless driver dump or an LLDP/SNMP neighbour), which is why this
// collector never emits EdgeTypeWirelessAssoc.
package fdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

// Command names.
const (
	cmdBridge = "bridge"
	cmdIP     = "ip"
)

// commandTimeout bounds one `bridge fdb show` invocation. A bridge with a very
// large FDB (thousands of MACs on a busy network) takes a while to print.
const commandTimeout = 4 * time.Second

// sysfsPath is the fallback location when the `bridge` tool is missing. Reading
// /sys/class/net/<dev>/brforward directly avoids depending on iproute2.
const sysfsPath = "/sys/class/net"

// Collector reads the bridge forwarding database of every bridge on the host.
type Collector struct {
	*discovery.BaseCollector

	// fdbPath overrides the `bridge fdb show` output with a fixture file. It is
	// the seam the fixture tests use.
	fdbPath string

	// ipLinkPath overrides the `ip -br link show` output used to learn the MAC
	// addresses of the host's own interfaces. Empty means run the real command
	// or fall back to sysfs.
	ipLinkPath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns a bridge FDB collector using the live system command.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorBridgeFDB,
		"Bridge forwarding database: which MAC was learned on which bridge port",
		false, // support is probed in Supported()
		discovery.CapMAC, discovery.CapLinks,
	)
	return c
}

// NewWithPath builds a collector that reads its FDB dump from a file. It is the
// seam the fixture tests use.
func NewWithPath(path string) *Collector {
	c := New()
	c.fdbPath = path
	return c
}

// NewWithPaths builds a collector that reads its FDB dump from fdbPath and its
// local interface list from ipLinkPath. Supplying both makes the collector
// independent of the host it runs on, which is what the fixture tests need.
func NewWithPaths(fdbPath, ipLinkPath string) *Collector {
	c := New()
	c.fdbPath = fdbPath
	c.ipLinkPath = ipLinkPath
	return c
}

// Supported reports whether the collector can run. The `bridge` command is the
// primary source; `ip` is an acceptable substitute (`ip -br fdb show`), and the
// sysfs bridge directory is a last resort.
func (c *Collector) Supported() bool {
	if c.fdbPath != "" {
		return true
	}
	if discovery.LookPath(cmdBridge) || discovery.LookPath(cmdIP) {
		return true
	}
	// Last resort: sysfs. It only counts when at least one real bridge exists.
	bridges, err := listBridges()
	if err != nil {
		return false
	}
	for _, b := range bridges {
		if isBridgeDevice(b) {
			return true
		}
	}
	return false
}

// Sources reports which input the collector will use.
func (c *Collector) Sources() []string {
	switch {
	case c.fdbPath != "":
		return []string{"file:" + c.fdbPath}
	case discovery.LookPath(cmdBridge):
		return []string{"bridge fdb show"}
	case discovery.LookPath(cmdIP):
		return []string{"ip -br fdb show"}
	}
	return nil
}

// Collect reads the FDB of every bridge and returns one observation per
// learned MAC plus one link observation per (port, MAC) pair.
//
// The collector never fails on noise: an unreadable source is skipped and a
// line that does not parse is dropped. Only a structurally broken input (an
// unreadable file) produces an error.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	now := c.now()
	if !c.Supported() {
		return discovery.EmptyResults(), nil
	}

	entries, err := c.readFDB(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}

	// Virtual interfaces must be known so their MACs can be filtered out.
	virtuals := c.virtualInterfaces(entries)
	// The bridge's own MACs: a MAC seen both on a bridge device and on one of
	// its member ports belongs to the bridge, not to a station.
	bridgeMACs := bridgeOwnMACs(entries)
	// The MAC addresses of the local interfaces themselves. A permanent FDB
	// entry carrying one of these is the interface's own address (the
	// "02:00:00:00:00:12 dev eth2 master br-lan permanent" rows), not a
	// station behind the port.
	localMACs := c.localInterfaceMACs()

	var out []discovery.Result
	for _, e := range entries {
		// Resolve the bridge: the "master" field when present, else the device
		// itself when it is a bridge.
		bridge := e.Master
		if bridge == "" {
			if isBridgeDevice(e.Dev) {
				bridge = e.Dev
			}
		}

		obs := c.observation(e, bridge, virtuals, bridgeMACs, localMACs)
		if obs == nil {
			continue
		}
		r := discovery.NewResult(discovery.ResultFDB, model.SourceBridgeFDB, now)
		r.FDBEntry = obs
		out = append(out, *r)

		// A link observation from the port to the MAC. This is the raw material
		// the topology engine turns into an edge. Ports with several learned
		// MACs produce several links — never one collapsed assumption.
		link := c.link(e, bridge, virtuals, bridgeMACs, localMACs)
		if link == nil {
			continue
		}
		r = discovery.NewResult(discovery.ResultLink, model.SourceBridgeFDB, now)
		r.Link = link
		out = append(out, *r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// input
// ---------------------------------------------------------------------------

// readFDB returns the raw FDB entries.
func (c *Collector) readFDB(ctx context.Context) ([]parsers.FDBEntry, error) {
	if c.fdbPath != "" {
		data, ok, err := discovery.ReadFileCapped(c.fdbPath, 0)
		if err != nil {
			return nil, fmt.Errorf("fdb: read %s: %w", c.fdbPath, err)
		}
		if !ok || len(data) == 0 {
			// A missing or empty file is a valid "no data" outcome: a router
			// with no bridges, or one that has not written the dump yet.
			return nil, nil //nolint:nilerr // missing source, not a failure
		}
		entries, perr := parsers.ParseBridgeFDB(data)
		if perr != nil {
			if errors.Is(perr, parsers.ErrEmptyInput) {
				// An empty dump is a valid "no bridges, nothing learned"
				// outcome, not a parse failure.
				return nil, nil
			}
			return nil, fmt.Errorf("fdb: parse %s: %w", c.fdbPath, perr)
		}
		return entries, nil
	}

	// Prefer `bridge fdb show`; it prints the full detail including the
	// "master" and "self" markers that `ip fdb show` abbreviates.
	if discovery.LookPath(cmdBridge) {
		out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmdBridge, "fdb", "show")
		if err == nil && len(out) > 0 {
			entries, perr := parsers.ParseBridgeFDB(out)
			if perr == nil {
				return entries, nil
			}
			return nil, fmt.Errorf("fdb: parse bridge fdb show: %w", perr)
		}
	}
	if discovery.LookPath(cmdIP) {
		out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmdIP, "-br", "fdb", "show")
		if err == nil && len(out) > 0 {
			entries, perr := parsers.ParseBridgeFDB(out)
			if perr == nil {
				return entries, nil
			}
			return nil, fmt.Errorf("fdb: parse ip fdb show: %w", perr)
		}
	}
	// No command available: fall back to sysfs.
	return readSysfs()
}

// ---------------------------------------------------------------------------
// filtering
// ---------------------------------------------------------------------------

// virtualInterfaces collects the set of interface names that are virtual, so
// their MACs can be filtered out of the FDB.
func (c *Collector) virtualInterfaces(entries []parsers.FDBEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		// A MAC permanently bound to a virtual device is that device's own
		// address: docker0, dummy0 and friends.
		if e.Dev != "" && parsers.LooksVirtualByName(e.Dev) && !parsers.IsLoopbackName(e.Dev) {
			out[e.Dev] = true
		}
	}
	// Add any virtual bridge we can enumerate from the system.
	for _, b := range listBridgesMust() {
		if parsers.LooksVirtualByName(b) {
			out[b] = true
		}
	}
	return out
}

// shouldSkip reports whether an FDB entry is noise that must not become a
// device. Every rule here exists because the real bridge FDB contains such
// entries on a stock OpenWrt install.
//
// bridgeMACs holds the MACs the bridge itself owns and localMACs the MACs of
// the host's own interfaces; both must be excluded, because a MAC that appears
// on a bridge device or on one of its member ports is not a remote station.
func shouldSkip(e parsers.FDBEntry, virtuals, bridgeMACs, localMACs map[string]bool) (bool, string) {
	mac := parsers.CanonicalMAC(e.MAC)
	// The caller supplied maps may use either the colon separated or the
	// canonical spelling; look up both so neither form silently misses.
	bridge := bridgeMACs[mac] || bridgeMACs[parsers.NormalizeMAC(e.MAC)]
	local := localMACs[mac] || localMACs[parsers.NormalizeMAC(e.MAC)]
	switch {
	case mac == "":
		return true, "unparseable mac"
	case parsers.IsZeroMAC(mac):
		return true, "zero mac"
	case parsers.IsBroadcastMAC(mac):
		return true, "broadcast mac"
	case parsers.IsMulticastMAC(mac):
		// 33:33:* (IPv6 solicited node), 01:00:5e:* (IPv4 multicast),
		// 01:80:c2:* (bridging protocols). None of them are devices.
		return true, "multicast mac"
	case e.Self:
		// The bridge's own address on one of its ports.
		return true, "bridge self entry"
	case bridge:
		// The bridge's own MAC, learned on a member port. This is the
		// "02:00:00:00:00:11 dev eth1 master br-lan permanent" row: the bridge
		// forwarding its own frames, not a device behind eth1.
		return true, "bridge own mac"
	case local && e.EntryType() != model.FDBEntryDynamic:
		// A permanent/static entry carrying the MAC of a local interface is
		// that interface's own address: "02:00:00:00:00:12 dev eth2 master
		// br-lan permanent". Only dynamically learned entries are stations.
		return true, "local interface mac"
	case virtuals[e.Dev]:
		return true, "mac of a virtual interface"
	}
	return false, ""
}

// observation builds the FDB observation, or nil when the entry is noise.
func (c *Collector) observation(e parsers.FDBEntry, bridge string, virtuals, bridgeMACs, localMACs map[string]bool) *discovery.FDBObservation {
	if skip, _ := shouldSkip(e, virtuals, bridgeMACs, localMACs); skip {
		return nil
	}
	return &discovery.FDBObservation{
		MAC:       parsers.NormalizeMAC(e.MAC),
		Bridge:    bridge,
		Port:      e.Port(),
		VLANID:    e.VLANID,
		EntryType: e.EntryType(),
		Self:      e.Self,
	}
}

// link builds the link observation, or nil when the entry is noise or carries
// no usable port.
//
// The link is deliberately typed EdgeTypeObservedMACPort: it records that the
// bridge learned the MAC on that port, nothing more. It is NOT a wireless
// association, and it is NOT a claim that the device is directly attached —
// there may be a switch in between that the FDB cannot see.
func (c *Collector) link(e parsers.FDBEntry, bridge string, virtuals, bridgeMACs, localMACs map[string]bool) *discovery.LinkObservation {
	if skip, _ := shouldSkip(e, virtuals, bridgeMACs, localMACs); skip {
		return nil
	}
	port := e.Port()
	if port == "" {
		return nil
	}
	return &discovery.LinkObservation{
		RemoteMAC:  parsers.NormalizeMAC(e.MAC),
		Interface:  bridge,
		Port:       port,
		LinkType:   model.EdgeTypeObservedMACPort,
		Confidence: discovery.ConfidenceFDB,
		Extra: map[string]string{
			"entry_type": string(e.EntryType()),
			"port":       port,
			"vlan":       itoa(e.VLANID),
		},
	}
}

func itoa(v int) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d", v)
}

// bridgeOwnMACs derives the set of MAC addresses the bridges themselves own.
//
// A bridge repeats its own MAC on each of its member ports (the
// "02:00:00:00:00:11 dev eth1 master br-lan permanent" rows). Detecting the
// repetition is more reliable than pattern-matching the address, because a
// router may use a real burned-in MAC for its bridge.
func bridgeOwnMACs(entries []parsers.FDBEntry) map[string]bool {
	// A MAC that appears on a device which is itself a bridge.
	onBridge := map[string]bool{}
	for _, e := range entries {
		if e.Dev == "" || e.Master == "" {
			continue
		}
		if e.Dev == e.Master {
			onBridge[parsers.CanonicalMAC(e.MAC)] = true
		}
	}
	// A MAC that appears on more than one distinct device is also the bridge's
	// own address (it is present on br-lan and on each member port).
	devices := map[string]map[string]bool{}
	for _, e := range entries {
		if e.Dev == "" {
			continue
		}
		mac := parsers.CanonicalMAC(e.MAC)
		if mac == "" {
			continue
		}
		if devices[mac] == nil {
			devices[mac] = map[string]bool{}
		}
		devices[mac][e.Dev] = true
	}
	out := map[string]bool{}
	for mac := range onBridge {
		out[mac] = true
	}
	for mac, devs := range devices {
		if len(devs) > 1 {
			out[mac] = true
		}
	}
	return out
}

// readInput returns the bytes of one input: the override file when the
// collector was built with a path, otherwise the command output. A missing file
// or a missing binary yields (nil, nil): the source is simply unavailable.
func (c *Collector) readInput(ctx context.Context, path, cmd string, args ...string) ([]byte, error) {
	if path != "" {
		data, _, err := discovery.ReadFileCapped(path, 0)
		if err != nil {
			return nil, fmt.Errorf("fdb: read %s: %w", path, err)
		}
		return data, nil
	}
	if !discovery.LookPath(cmd) {
		return nil, nil
	}
	out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmd, args...)
	if err != nil {
		return nil, nil //nolint:nilerr // degraded source, not a failure
	}
	return out, nil
}

// localInterfaceMACs returns the MAC addresses of the host's own interfaces.
//
// The bridge repeats the MAC of each member port in its FDB as a permanent
// entry, so a MAC owned by a local interface is never a remote device. Two
// sources are consulted: sysfs (always present on Linux, needs no binary) and
// `ip -br link` (works where sysfs is not mounted). When neither is readable
// the set is empty and only the bridge-derived rules apply.
func (c *Collector) localInterfaceMACs() map[string]bool {
	out := map[string]bool{}
	if c.ipLinkPath == "" && !discovery.LookPath(cmdIP) {
		if names, err := readDirNames(sysfsPath); err == nil {
			for _, n := range names {
				data, ok, err := discovery.ReadFileCapped(sysfsPath+"/"+n+"/address", 64)
				if err != nil || !ok {
					continue
				}
				if mac := parsers.CanonicalMAC(strings.TrimSpace(string(data))); mac != "" {
					out[mac] = true
				}
			}
		}
		return out
	}
	data, err := c.readInput(context.Background(), c.ipLinkPath, cmdIP, "-br", "link", "show")
	if err != nil || len(data) == 0 {
		// Fall back to sysfs when `ip` produced nothing.
		if names, derr := readDirNames(sysfsPath); derr == nil {
			for _, n := range names {
				b, ok, ferr := discovery.ReadFileCapped(sysfsPath+"/"+n+"/address", 64)
				if ferr != nil || !ok {
					continue
				}
				if mac := parsers.CanonicalMAC(strings.TrimSpace(string(b))); mac != "" {
					out[mac] = true
				}
			}
		}
		return out
	}
	if entries, perr := parsers.ParseIPLink(data); perr == nil {
		for _, l := range entries {
			if mac := parsers.CanonicalMAC(l.MAC); mac != "" {
				out[mac] = true
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// sysfs fallback
// ---------------------------------------------------------------------------

// listBridges returns the bridge devices visible in sysfs.
func listBridges() ([]string, error) {
	return readDirNames(sysfsPath)
}

// listBridgesMust is the best-effort variant used by Supported().
func listBridgesMust() []string {
	names, _ := listBridges()
	return names
}

// isBridgeDevice reports whether name looks like a bridge in sysfs.
func isBridgeDevice(name string) bool {
	if name == "" {
		return false
	}
	_, err := statPath(sysfsPath + "/" + name + "/bridge")
	return err == nil
}

// readSysfs reads every bridge's forwarding table from sysfs. The format is
// "<port>\t<mac>\t<local|static|dynamic>" per line, without the "master" or
// "self" markers the `bridge` tool prints, so the derived bridge name comes
// from the directory.
func readSysfs() ([]parsers.FDBEntry, error) {
	bridges, err := listBridges()
	if err != nil {
		return nil, nil //nolint:nilerr // no bridges is a normal outcome
	}
	var out []parsers.FDBEntry
	for _, b := range bridges {
		if !isBridgeDevice(b) {
			continue
		}
		data, ok, err := discovery.ReadFileCapped(sysfsPath+"/"+b+"/brforward", 0)
		if err != nil || !ok {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			e := parsers.FDBEntry{
				Dev:    fields[0],
				Master: b,
				MAC:    parsers.NormalizeMAC(fields[1]),
			}
			if len(fields) >= 3 {
				switch fields[2] {
				case "permanent":
					e.Permanent = true
				case "static":
					e.Static = true
				case "local":
					e.Self = true
				}
			}
			out = append(out, e)
		}
	}
	return out, nil
}
