// Package openwrt implements the OpenWrt / iStoreOS collector.
//
// It is the primary source of interface truth on the router Lanory runs on: it
// reads UCI configuration, `ip -br link`, and the two ubus network dumps, then
// merges them into one view per interface.
//
// Evidence precedence, highest first:
//
//  1. `ubus call network.interface dump` — the logical interfaces, their
//     addresses, routes and the device each one is bound to. This is the only
//     source that knows which device carries an IP.
//  2. `ubus call network.device dump` — the physical/virtual devices with
//     their counters, speed and bridge membership.
//  3. `uci export network` — the configured role of every interface (which is
//     what makes eth0 the WAN port and br-lan the LAN).
//  4. `ip -br link show` — the fallback when ubus is unavailable, plus the
//     authoritative carrier state.
//
// Roles are derived from UCI, never guessed: eth0 is the WAN port because
// network.wan.ifname says so, not because the name starts with "eth".
package openwrt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

// Command names, resolved through exec.LookPath so Supported() stays honest.
const (
	cmdIP   = "ip"
	cmdUCI  = "uci"
	cmdUBUS = "ubus"
)

// commandTimeout bounds one system command. ubus is fast but a busy router can
// stall it, and a wedged collector must never block the discovery loop.
const commandTimeout = 3 * time.Second

// Collector gathers interface, address and link facts from an OpenWrt system.
//
// It is safe to run anywhere: every source degrades to "unsupported" and
// Collect returns an empty result set rather than an error.
type Collector struct {
	*discovery.BaseCollector

	// uciPath, ipLinkPath, ubusDevicePath and ubusInterfacePath override the
	// live commands with fixture files. They are the seam the fixture tests
	// use; empty means run the real command.
	uciPath           string
	ipLinkPath        string
	ubusDevicePath    string
	ubusInterfacePath string

	// boardPath, releasePath and hostnamePath point at the OpenWrt identity
	// files. Empty means the standard locations.
	boardPath    string
	releasePath  string
	hostnamePath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns an OpenWrt collector using the live system commands.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorOpenWRT,
		"OpenWrt/iStoreOS interfaces, addresses, bridge membership and link state",
		false, // support is probed in Supported()
		discovery.CapInterfaces, discovery.CapIPv4, discovery.CapIPv6,
		discovery.CapMAC, discovery.CapLinks,
	)
	return c
}

// NewWithPaths builds a collector that reads its inputs from the given files
// instead of running commands. It is the seam the fixture tests use.
func NewWithPaths(uciPath, ipLinkPath, ubusDevicePath, ubusInterfacePath string) *Collector {
	c := New()
	c.uciPath = uciPath
	c.ipLinkPath = ipLinkPath
	c.ubusDevicePath = ubusDevicePath
	c.ubusInterfacePath = ubusInterfacePath
	return c
}

// WithHostInfoFiles points the collector at the OpenWrt identity files, so the
// router device gets a real name, model and firmware version instead of
// "Unknown 000011".
func (c *Collector) WithHostInfoFiles(boardPath, releasePath, hostnamePath string) *Collector {
	c.boardPath = boardPath
	c.releasePath = releasePath
	c.hostnamePath = hostnamePath
	return c
}

// HostInfo describes the machine Lanory runs on.
//
// These values name the router device in the UI. They are read from local files
// only; nothing is sent anywhere.
type HostInfo struct {
	// Hostname is /proc/sys/kernel/hostname (or uci system.@system[0].hostname).
	Hostname string
	// Model is the human readable board model, e.g. "EASEPi R1".
	Model string
	// Manufacturer is the board vendor, when the board database names one.
	Manufacturer string
	// Firmware is the OpenWrt release description, e.g. "iStoreOS 25.12.5".
	Firmware string
}

// readHostInfo reads the local identity files. Every file is optional: a missing
// one leaves the corresponding field empty rather than failing the collector,
// because Lanory also runs on generic Linux during development.
func (c *Collector) readHostInfo() HostInfo {
	var info HostInfo

	hostnamePath := c.hostnamePath
	if hostnamePath == "" {
		hostnamePath = "/proc/sys/kernel/hostname"
	}
	if data, supported, err := discovery.ReadFileCapped(hostnamePath, 256); err == nil && supported {
		info.Hostname = strings.TrimSpace(string(data))
	}

	boardPath := c.boardPath
	if boardPath == "" {
		boardPath = "/etc/board.json"
	}
	if data, supported, err := discovery.ReadFileCapped(boardPath, 64*1024); err == nil && supported {
		var board struct {
			Model struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Vendor string `json:"vendor_name"`
			} `json:"model"`
			System struct {
				Hostname string `json:"hostname"`
			} `json:"system"`
		}
		if json.Unmarshal(data, &board) == nil {
			if board.Model.Name != "" {
				info.Model = board.Model.Name
			} else if board.Model.ID != "" {
				info.Model = board.Model.ID
			}
			info.Manufacturer = board.Model.Vendor
			if info.Hostname == "" {
				info.Hostname = board.System.Hostname
			}
		}
	}

	releasePath := c.releasePath
	if releasePath == "" {
		releasePath = "/etc/openwrt_release"
	}
	if data, supported, err := discovery.ReadFileCapped(releasePath, 8*1024); err == nil && supported {
		info.Firmware = parseReleaseDescription(string(data))
	}

	return info
}

// parseReleaseDescription extracts a short firmware description from
// /etc/openwrt_release, which is a shell fragment such as:
//
//	DISTRIB_ID='iStoreOS'
//	DISTRIB_RELEASE='25.12.5'
func parseReleaseDescription(raw string) string {
	var id, release string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `'"`)
		switch key {
		case "DISTRIB_ID":
			id = value
		case "DISTRIB_RELEASE":
			release = value
		}
	}
	switch {
	case id != "" && release != "":
		return id + " " + release
	case id != "":
		return id
	default:
		return release
	}
}

// Supported reports whether any input is available. The collector runs when
// ubus, uci or ip is present, or when /etc/config/network exists (an OpenWrt
// marker that works even when the binaries live outside PATH).
func (c *Collector) Supported() bool {
	if c.uciPath != "" || c.ipLinkPath != "" || c.ubusDevicePath != "" || c.ubusInterfacePath != "" {
		return true
	}
	if discovery.LookPath(cmdUBUS) || discovery.LookPath(cmdUCI) || discovery.LookPath(cmdIP) {
		return true
	}
	if fi, err := os.Stat("/etc/config/network"); err == nil && !fi.IsDir() {
		return true
	}
	return false
}

// Sources reports which inputs are available, for the status endpoint.
func (c *Collector) Sources() map[string]bool {
	return map[string]bool{
		"ubus_device":    c.ubusDevicePath != "" || discovery.LookPath(cmdUBUS),
		"ubus_interface": c.ubusInterfacePath != "" || discovery.LookPath(cmdUBUS),
		"uci":            c.uciPath != "" || discovery.LookPath(cmdUCI),
		"ip":             c.ipLinkPath != "" || discovery.LookPath(cmdIP),
	}
}

// Collect reads every available source and returns normalized observations.
//
// An unreadable source is skipped silently; only a structurally broken source
// (unparseable JSON, for example) produces an error. Interface rows are always
// emitted, including the ignored ones, so the UI can show that lo was seen and
// deliberately skipped.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	now := c.now()
	if !c.Supported() {
		return discovery.EmptyResults(), nil
	}

	uci, err := c.readUCI(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}
	links, err := c.readLinks(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}
	devDump, err := c.readUbusDevices(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}
	ifDump, err := c.readUbusInterfaces(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}

	views := c.merge(uci, links, devDump, ifDump, now)

	var out []discovery.Result

	// Emit the host identity first so the topology engine can name the router
	// device instead of rendering it as "Unknown 000011".
	if host := c.hostResult(now); host != nil {
		out = append(out, *host)
	}

	for _, v := range views {
		out = append(out, *c.interfaceResult(v, now))
		if v.Ignore {
			continue
		}
		for _, a := range v.Addresses {
			out = append(out, *c.addressResult(v, a, now))
		}
		// A bridge is a forwarding device: link it to each member port.
		if v.Type == model.InterfaceTypeBridge {
			for _, m := range v.Members {
				out = append(out, *c.membershipResult(v, m, now))
			}
		}
	}
	return out, nil
}

// hostResult reports the identity of the machine Lanory runs on.
//
// It is a generic result carrying the router's hostname, model and firmware, so
// the device that owns the switch ports can be displayed with a real name.
func (c *Collector) hostResult(now time.Time) *discovery.Result {
	info := c.readHostInfo()
	if info.Hostname == "" && info.Model == "" && info.Firmware == "" {
		return nil
	}
	r := discovery.NewResult(discovery.ResultGeneric, model.SourceOpenWRT, now)
	r.Attributes = map[string]string{
		"kind":         "host_identity",
		"hostname":     info.Hostname,
		"model":        info.Model,
		"manufacturer": info.Manufacturer,
		"firmware":     info.Firmware,
	}
	return r
}

// ---------------------------------------------------------------------------
// source readers
// ---------------------------------------------------------------------------

// readUCI returns the grouped UCI network sections.
func (c *Collector) readUCI(ctx context.Context) ([]parsers.UCISection, error) {
	// `uci show network` prints the flat "network.lan.ifname='br-lan'" form that
	// parsers.ParseUCI expects. `uci export network` prints a completely
	// different, multi-line config-file syntax ("config interface 'lan'" plus
	// "	option ifname 'br-lan'"), which ParseUCI cannot read: on a real router
	// that mismatch produced ErrEmptyInput and silently disabled the entire
	// interface and edge pipeline.
	data, err := c.readInput(ctx, c.uciPath, cmdUCI, "show", "network")
	if err != nil || len(data) == 0 {
		return nil, nil //nolint:nilerr // an unavailable source is not an error
	}
	entries, perr := parsers.ParseUCI(data)
	if perr != nil {
		return nil, fmt.Errorf("openwrt: parse uci export: %w", perr)
	}
	return parsers.GroupUCI(entries), nil
}

// readLinks returns the `ip -br link show` entries.
func (c *Collector) readLinks(ctx context.Context) ([]parsers.LinkEntry, error) {
	data, err := c.readInput(ctx, c.ipLinkPath, cmdIP, "-br", "link", "show")
	if err != nil || len(data) == 0 {
		return nil, nil //nolint:nilerr // an unavailable source is not an error
	}
	entries, perr := parsers.ParseIPLink(data)
	if perr != nil {
		return nil, fmt.Errorf("openwrt: parse ip link: %w", perr)
	}
	return entries, nil
}

// readUbusDevices returns the parsed network.device dump.
func (c *Collector) readUbusDevices(ctx context.Context) (*parsers.UbusDeviceDump, error) {
	data, err := c.readInput(ctx, c.ubusDevicePath, cmdUBUS, "call", "network.device", "dump")
	if err != nil || len(data) == 0 {
		return nil, nil //nolint:nilerr // an unavailable source is not an error
	}
	dump, perr := parsers.ParseUbusDeviceDump(data)
	if perr != nil {
		return nil, perr
	}
	return dump, nil
}

// readUbusInterfaces returns the parsed network.interface dump.
func (c *Collector) readUbusInterfaces(ctx context.Context) (*parsers.UbusInterfaceDump, error) {
	data, err := c.readInput(ctx, c.ubusInterfacePath, cmdUBUS, "call", "network.interface", "dump")
	if err != nil || len(data) == 0 {
		return nil, nil //nolint:nilerr // an unavailable source is not an error
	}
	dump, perr := parsers.ParseUbusInterfaceDump(data)
	if perr != nil {
		return nil, perr
	}
	return dump, nil
}

// readInput returns the bytes of one input: the override file when the
// collector was built with paths, otherwise the command output. A missing file
// or a missing binary yields (nil, nil): the source is simply unavailable.
func (c *Collector) readInput(ctx context.Context, path, cmd string, args ...string) ([]byte, error) {
	if path != "" {
		data, _, err := discovery.ReadFileCapped(path, 0)
		if err != nil {
			return nil, fmt.Errorf("openwrt: read %s: %w", path, err)
		}
		return data, nil
	}
	if !discovery.LookPath(cmd) {
		return nil, nil
	}
	out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmd, args...)
	if err != nil {
		// A non-zero exit (busybox `ip` on an unsupported family, ubus not
		// running) is a normal "no data" outcome, not a collector failure.
		return nil, nil //nolint:nilerr // degraded source, not a failure
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// merge
// ---------------------------------------------------------------------------

// ifaceView is the merged view of one interface across all sources.
type ifaceView struct {
	Name       string
	MAC        string
	Type       model.InterfaceType
	Role       model.InterfaceRole
	AdminState model.AdminState
	OperState  model.OperState
	Carrier    bool
	SpeedMbps  int
	MTU        int
	BridgeID   string
	VLANID     int
	IsPhysical bool
	Virtual    bool
	Ignore     bool
	Members    []string

	Addresses []parsers.UbusInterfaceAddress

	RXBytes, TXBytes     uint64
	RXErrors, TXErrors   uint64
	RXDropped, TXDropped uint64
	RXPackets, TXPackets uint64

	// Source records which input supplied the row, for evidence.
	Source string
}

// merge folds the four sources into one view per interface.
//
// The ubus device dump provides the base set of devices; anything known only to
// UCI or `ip link` is added afterwards so a device netifd has not brought up
// still shows up.
func (c *Collector) merge(
	uci []parsers.UCISection,
	links []parsers.LinkEntry,
	devDump *parsers.UbusDeviceDump,
	ifDump *parsers.UbusInterfaceDump,
	now time.Time,
) []ifaceView {
	byName := map[string]*ifaceView{}
	var order []string

	get := func(name string) *ifaceView {
		if v, ok := byName[name]; ok {
			return v
		}
		v := &ifaceView{
			Name:       name,
			Type:       model.InterfaceTypeOther,
			Role:       model.InterfaceRoleUnknown,
			AdminState: model.AdminUnknown,
			OperState:  model.OperUnknown,
		}
		byName[name] = v
		order = append(order, name)
		return v
	}

	// 1. ubus device dump: the base layer.
	if devDump != nil {
		for name, d := range devDump.Devices {
			v := get(name)
			v.Source = "ubus:network.device"
			v.MAC = d.MAC
			v.MTU = d.MTU
			v.SpeedMbps = d.Speed
			v.Members = d.Members
			v.Carrier = d.Link
			v.AdminState = adminState(d.Up)
			v.OperState = operState(d.Up, d.Link)
			v.Type = ifaceType(d.Type, name)
			v.IsPhysical = d.Type == "ethernet" && !parsers.LooksVirtualByName(name)
			v.Virtual = isVirtual(name, d.Type)
			v.Ignore = parsers.IsIgnoredName(name)
			v.RXBytes, v.TXBytes = d.Stats.RXBytes, d.Stats.TXBytes
			v.RXPackets, v.TXPackets = d.Stats.RXPackets, d.Stats.TXPackets
			v.RXErrors, v.TXErrors = d.Stats.RXErrors, d.Stats.TXErrors
			v.RXDropped, v.TXDropped = d.Stats.RXDropped, d.Stats.TXDropped
		}
	}

	// 2. ip -br link: fills in interfaces ubus does not know about.
	//
	// ip -br link is the *kernel* view, so it is authoritative for the MAC and
	// the admin state, but it is NOT authoritative for the operational state of
	// a port ubus already reported on: netifd can hold a device
	// administratively down while the cable is still in. When the ubus device
	// dump answered, keep its verdict and only let the kernel *downgrade* an
	// "up" verdict to no-carrier when it explicitly says so.
	for _, l := range links {
		v := get(l.Name)
		if v.Source == "" {
			v.Source = "ip:link"
		}
		if v.MAC == "" {
			v.MAC = l.MAC
		}
		if v.Source != "ubus:network.device" {
			v.Carrier = l.Flags.HasCarrier()
			v.AdminState = adminState(l.Flags.Up)
			v.OperState = operStateFromLink(l, v.AdminState)
		} else {
			// ubus answered: keep its state, but honour an explicit kernel
			// NO-CARRIER flag, which is the one thing ubus cannot know.
			if l.Flags.NoCarrier {
				v.Carrier = false
				if v.OperState == model.OperUp {
					v.OperState = model.OperNoCarrier
				}
			}
			if v.AdminState == model.AdminUnknown {
				v.AdminState = adminState(l.Flags.Up)
			}
		}
		if v.Type == model.InterfaceTypeOther {
			v.Type = ifaceTypeFromLink(l)
		}
		if v.Type == model.InterfaceTypeEthernet && !v.IsPhysical {
			v.IsPhysical = !parsers.LooksVirtualByName(l.Name)
		}
		v.Virtual = v.Virtual || parsers.LooksVirtualByName(l.Name)
		if l.Flags.Loopback {
			v.Ignore = true
			v.Type = model.InterfaceTypeLoopback
			v.Role = model.InterfaceRoleLoopback
		}
	}

	// 3. UCI: roles, bridge membership, VLAN ids.
	applyUCI(uci, byName)

	// 4. ubus interface dump: addresses and the device each logical interface
	//    is bound to. This is what makes an interface "carry a subnet".
	if ifDump != nil {
		for name, i := range ifDump.Interfaces {
			dev := i.L3Device
			if dev == "" {
				dev = i.Device
			}
			if dev == "" {
				continue
			}
			v := get(dev)
			v.Addresses = append(v.Addresses, i.IPv4...)
			v.Addresses = append(v.Addresses, i.IPv6...)
			// The role of a bound device is the role of its logical
			// interface: this is how eth0 becomes the WAN port.
			if role := roleFromInterfaceName(name); role != model.InterfaceRoleUnknown {
				if v.Role == model.InterfaceRoleUnknown {
					v.Role = role
				}
			}
		}
	}

	// Final pass: bridge membership, virtual flags and physicality.
	//
	// A bridge member must carry the name of the bridge it belongs to: that is
	// what makes "eth1 is a port of br-lan" expressible. The bridge itself does
	// NOT carry a BridgeID — it is the bridge, not a member of one — and it
	// never reports a negotiated speed.
	for _, name := range order {
		v := byName[name]
		if v.Virtual {
			v.Ignore = true
			if v.Role == model.InterfaceRoleUnknown || v.Role == model.InterfaceRoleLAN {
				v.Role = model.InterfaceRoleVirtual
			}
			continue
		}
		if v.Type == model.InterfaceTypeBridge {
			v.BridgeID = ""
			v.SpeedMbps = 0
			for _, m := range v.Members {
				mv := byName[m]
				if mv == nil {
					continue
				}
				mv.BridgeID = v.Name
				mv.IsPhysical = true
				if mv.Type == model.InterfaceTypeOther {
					mv.Type = model.InterfaceTypeEthernet
				}
				if mv.Role == model.InterfaceRoleUnknown {
					mv.Role = v.Role
				}
			}
			continue
		}
		if v.BridgeID != "" {
			// A member of a bridge is a physical port.
			v.IsPhysical = true
			if v.Type == model.InterfaceTypeOther {
				v.Type = model.InterfaceTypeEthernet
			}
		}
	}

	out := make([]ifaceView, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	_ = now
	return out
}

// applyUCI folds the UCI sections into the interface views.
func applyUCI(secs []parsers.UCISection, byName map[string]*ifaceView) {
	// Pass 1: device sections. A "device" section names a bridge or a plain
	// port and lists its members.
	for _, s := range secs {
		switch s.Type {
		case "device", "bridge":
			name, _ := s.Get("name")
			if name == "" {
				continue
			}
			v := byName[name]
			if v == nil {
				continue
			}
			if t, ok := s.Get("type"); ok {
				switch t {
				case "bridge":
					v.Type = model.InterfaceTypeBridge
					v.BridgeID = name
				case "vlan":
					v.Type = model.InterfaceTypeVLAN
					if n := parsers.ParseUint(s.Options["vid"]); n > 0 {
						v.VLANID = n
					}
				}
			}
			if ports, ok := s.Options["ports"]; ok {
				if members := parsers.SplitList(ports); len(members) > 0 {
					v.Members = members
				}
			}
			if m, ok := s.Options["macaddr"]; ok && parsers.ValidMAC(m) {
				v.MAC = parsers.NormalizeMAC(m)
			}
		}
	}

	// Pass 1b: bridge membership is symmetric. A member port records which
	// bridge carries it, so the topology engine can draw "eth1 is a port of
	// br-lan" without re-deriving it from the bridge's own port list.
	for _, s := range secs {
		switch s.Type {
		case "device", "bridge":
			name, _ := s.Get("name")
			if name == "" {
				continue
			}
			bridge := byName[name]
			if bridge == nil || bridge.Type != model.InterfaceTypeBridge {
				continue
			}
			for _, m := range bridge.Members {
				if mv := byName[m]; mv != nil && mv.BridgeID == "" {
					mv.BridgeID = name
				}
			}
		}
	}

	// Pass 2: interface sections. An "interface" section binds a role to a
	// device, which is what tells us which port is WAN and which is LAN.
	for _, s := range secs {
		if s.Type != "interface" {
			continue
		}
		role := roleFromInterfaceName(s.Name())
		if role == model.InterfaceRoleUnknown {
			continue
		}
		dev := s.Options["device"]
		if dev == "" {
			dev = s.Options["ifname"]
		}
		for _, name := range splitDeviceList(dev) {
			v := byName[name]
			if v == nil {
				continue
			}
			if v.Role == model.InterfaceRoleUnknown {
				v.Role = role
			}
		}
	}

	// Pass 3: a bridge takes the role of the logical interface bound to it, and
	// propagates it to its member ports. Without this, br-lan and eth1..3 would
	// all stay "unknown".
	bridgeRoles := map[string]model.InterfaceRole{}
	for _, s := range secs {
		if s.Type != "interface" {
			continue
		}
		dev := s.Options["device"]
		if dev == "" {
			continue
		}
		if v := byName[dev]; v != nil && v.Type == model.InterfaceTypeBridge {
			if role := roleFromInterfaceName(s.Name()); role != model.InterfaceRoleUnknown {
				bridgeRoles[dev] = role
			}
		}
	}
	for bridge, role := range bridgeRoles {
		v := byName[bridge]
		if v == nil {
			continue
		}
		v.Role = role
		for _, m := range v.Members {
			if mv := byName[m]; mv != nil && mv.Role == model.InterfaceRoleUnknown {
				mv.Role = role
			}
		}
	}
}

// splitDeviceList splits a UCI ifname/device value. OpenWrt accepts both a
// single name and a space or comma separated list.
func splitDeviceList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// roleFromInterfaceName maps an OpenWrt logical interface name to a role. These
// are the conventions OpenWrt itself uses (network.lan, network.wan), so the
// mapping is configuration-derived rather than a guess about hardware.
func roleFromInterfaceName(name string) model.InterfaceRole {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "loopback":
		return model.InterfaceRoleLoopback
	case n == "lan":
		return model.InterfaceRoleLAN
	case n == "wan", n == "wan6":
		return model.InterfaceRoleWAN
	case n == "dmz":
		return model.InterfaceRoleDMZ
	case strings.HasPrefix(n, "lan"):
		return model.InterfaceRoleLAN
	case strings.HasPrefix(n, "wan"):
		return model.InterfaceRoleWAN
	}
	return model.InterfaceRoleUnknown
}

// ---------------------------------------------------------------------------
// type / state helpers
// ---------------------------------------------------------------------------

func ifaceType(ubusType, name string) model.InterfaceType {
	switch model.InterfaceType(parsers.InterfaceTypeFromUbus(ubusType)) {
	case model.InterfaceTypeBridge:
		return model.InterfaceTypeBridge
	case model.InterfaceTypeLoopback:
		return model.InterfaceTypeLoopback
	case model.InterfaceTypeVLAN:
		return model.InterfaceTypeVLAN
	case model.InterfaceTypeVirtual:
		return model.InterfaceTypeVirtual
	case model.InterfaceTypeWireless:
		return model.InterfaceTypeWireless
	}
	return ifaceTypeFromName(name)
}

// ifaceTypeFromLink derives the type from `ip -br link` flags.
func ifaceTypeFromLink(l parsers.LinkEntry) model.InterfaceType {
	switch {
	case l.Flags.Loopback:
		return model.InterfaceTypeLoopback
	case l.Flags.Master != "":
		return model.InterfaceTypeVLAN
	}
	return ifaceTypeFromName(l.Name)
}

// ifaceTypeFromName derives the type from the interface name. Only the OpenWrt
// conventions are recognised; anything else is "other" rather than guessed as
// ethernet.
func ifaceTypeFromName(name string) model.InterfaceType {
	n := strings.ToLower(name)
	switch {
	case parsers.IsLoopbackName(n):
		return model.InterfaceTypeLoopback
	case strings.HasPrefix(n, "br-"):
		return model.InterfaceTypeBridge
	case strings.HasPrefix(n, "eth"), strings.HasPrefix(n, "en"),
		strings.HasPrefix(n, "lan"), strings.HasPrefix(n, "wan"),
		strings.HasPrefix(n, "port"):
		return model.InterfaceTypeEthernet
	case strings.HasPrefix(n, "wlan"):
		return model.InterfaceTypeWireless
	case strings.Contains(n, "."):
		return model.InterfaceTypeVLAN
	case parsers.LooksVirtualByName(n):
		return model.InterfaceTypeVirtual
	}
	return model.InterfaceTypeOther
}

// isVirtual reports whether an interface is a virtual (non physical port)
// device. A bridge is NOT virtual: it is a forwarding device built from
// physical ports, and hiding it would hide the whole LAN.
func isVirtual(name, ubusType string) bool {
	if parsers.IsLoopbackName(name) {
		return true
	}
	switch strings.ToLower(ubusType) {
	case "dummy", "tunnel", "vxlan", "gre", "sit", "veth":
		return true
	}
	if strings.EqualFold(ubusType, "bridge") {
		// Container bridges are virtual; an OpenWrt LAN bridge is not.
		return parsers.LooksVirtualByName(name)
	}
	return parsers.LooksVirtualByName(name)
}

func adminState(up bool) model.AdminState {
	if up {
		return model.AdminUp
	}
	return model.AdminDown
}

func operState(up, carrier bool) model.OperState {
	if !up {
		return model.OperDown
	}
	if !carrier {
		return model.OperNoCarrier
	}
	return model.OperUp
}

// operStateFromLink derives the operational state from `ip -br link` flags.
//
// The nuance: a bridge member with no cable prints DOWN with the UP flag unset,
// which looks like an administratively down interface. When the flags carry no
// LOWER_UP marker the port is either down or unplugged, and the kernel does not
// distinguish the two. Reporting it as no-carrier lets the UI show "cable
// unplugged" instead of "disabled", which matches what a user sees on the
// hardware. A NO-CARRIER flag, when present, is the explicit signal.
func operStateFromLink(l parsers.LinkEntry, admin model.AdminState) model.OperState {
	switch {
	case l.Flags.HasCarrier():
		return model.OperUp
	case l.Flags.NoCarrier:
		return model.OperNoCarrier
	case l.Flags.Loopback:
		// The loopback device is always connected to itself.
		return model.OperUp
	case l.State == "DOWN":
		// The kernel prints DOWN for an up-but-unplugged port. Only report
		// "down" when the admin state is genuinely down as well.
		if admin == model.AdminDown {
			return model.OperDown
		}
		return model.OperNoCarrier
	}
	return model.OperUnknown
}

// ---------------------------------------------------------------------------
// result builders
// ---------------------------------------------------------------------------

func (c *Collector) interfaceResult(v ifaceView, now time.Time) *discovery.Result {
	r := discovery.NewResult(discovery.ResultInterface, model.SourceOpenWRT, now)
	r.Interface = &discovery.InterfaceObservation{
		Name:       v.Name,
		MAC:        v.MAC,
		Type:       v.Type,
		Role:       v.Role,
		AdminState: v.AdminState,
		OperState:  v.OperState,
		Carrier:    v.Carrier,
		SpeedMbps:  v.SpeedMbps,
		MTU:        v.MTU,
		BridgeID:   v.BridgeID,
		VLANID:     v.VLANID,
		IsPhysical: v.IsPhysical,
		Virtual:    v.Virtual,
		Ignore:     v.Ignore,
		RXBytes:    v.RXBytes,
		TXBytes:    v.TXBytes,
		RXErrors:   v.RXErrors,
		TXErrors:   v.TXErrors,
		RXDropped:  v.RXDropped,
		TXDropped:  v.TXDropped,
		RXPackets:  v.RXPackets,
		TXPackets:  v.TXPackets,
	}
	for _, a := range v.Addresses {
		family := model.AddressFamilyIPv4
		if strings.Contains(a.Address, ":") {
			family = model.AddressFamilyIPv6
		}
		r.Interface.Addresses = append(r.Interface.Addresses, discovery.AddressObservation{
			Address:      a.Address,
			Family:       family,
			PrefixLength: a.Mask,
			MAC:          v.MAC,
			Interface:    v.Name,
		})
	}
	return r
}

func (c *Collector) addressResult(v ifaceView, a parsers.UbusInterfaceAddress, now time.Time) *discovery.Result {
	family := model.AddressFamilyIPv4
	if strings.Contains(a.Address, ":") {
		family = model.AddressFamilyIPv6
	}
	r := discovery.NewResult(discovery.ResultAddress, model.SourceUBUS, now)
	r.Address = &discovery.AddressObservation{
		Address:      a.Address,
		Family:       family,
		PrefixLength: a.Mask,
		MAC:          v.MAC,
		Interface:    v.Name,
	}
	return r
}

// membershipResult links a bridge to one of its member ports. It is a logical
// relation, not a cable: the bridge carries the port's traffic.
func (c *Collector) membershipResult(bridge ifaceView, member string, now time.Time) *discovery.Result {
	r := discovery.NewResult(discovery.ResultLink, model.SourceOpenWRT, now)
	r.Link = &discovery.LinkObservation{
		LocalMAC:   bridge.MAC,
		Interface:  bridge.Name,
		Port:       member,
		LinkType:   model.EdgeTypeBridgeMembership,
		Confidence: discovery.ConfidenceSubnet + 0.4, // 0.70: UCI is authoritative config
		Extra: map[string]string{
			"member": member,
			"bridge": bridge.Name,
			"kind":   "bridge_membership",
		},
	}
	return r
}
