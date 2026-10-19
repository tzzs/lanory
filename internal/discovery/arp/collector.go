// Package arp implements the IPv4 neighbour (ARP) collector.
//
// It reads the kernel's IPv4 neighbour table, which is the list of addresses the
// router has recently resolved to a MAC. That gives two things no other source
// provides as cheaply: the MAC behind an IP, and the interface the address was
// reached through.
//
// Two rules matter more than anything else in this package:
//
//   - A FAILED or STALE neighbour is NOT an offline device. The kernel expires
//     and re-resolves entries constantly; a stale entry simply means the address
//     has not been used recently. Marking it offline would flap the whole UI.
//   - An INCOMPLETE or FAILED entry with no MAC is recorded as an address that
//     exists but is not yet identified. It proves traffic towards that address,
//     so it is worth reporting, but it must never become a device on its own.
package arp

import (
	"context"
	"fmt"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

// Command names.
const (
	cmdIP  = "ip"
	cmdArp = "arp"
)

// procArp is the fallback source when neither `ip` nor `arp` is available. It is
// the kernel's own neighbour table, always present on Linux.
const procArp = "/proc/net/arp"

// commandTimeout bounds one command.
const commandTimeout = 3 * time.Second

// Collector reads the IPv4 neighbour table.
type Collector struct {
	*discovery.BaseCollector

	// neighPath overrides the `ip neigh show` output with a fixture file.
	neighPath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns an ARP collector using the live system command.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorARP,
		"IPv4 neighbour table: which MAC answers for which address on which interface",
		false,
		discovery.CapIPv4, discovery.CapMAC,
	)
	return c
}

// NewWithPath builds a collector that reads its neighbour dump from a file. It
// is the seam the fixture tests use.
func NewWithPath(path string) *Collector {
	c := New()
	c.neighPath = path
	return c
}

// Supported reports whether an IPv4 neighbour source exists.
//
// When the collector was built with an explicit path, the path itself decides:
// a lease-style dump that does not exist yet means the source is not present
// on this host, which is a normal state, not an error.
func (c *Collector) Supported() bool {
	if c.neighPath != "" {
		st, err := discovery.StatFile(c.neighPath)
		return err == nil && st.Exists && !st.IsDir
	}
	if discovery.LookPath(cmdIP) || discovery.LookPath(cmdArp) {
		return true
	}
	st, err := discovery.StatFile(procArp)
	return err == nil && st.Exists && !st.IsDir
}

// Collect reads the neighbour table and returns one observation per entry.
//
// Entries without a MAC are still reported: they document that the router is
// trying to reach an address, which is evidence about the network even when the
// address has not been identified yet.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	now := c.now()
	if !c.Supported() {
		return discovery.EmptyResults(), nil
	}
	entries, err := c.readNeighbours(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}

	var out []discovery.Result
	for _, e := range entries {
		// The ARP collector is IPv4 only. An IPv6 address belongs to the ndp
		// collector; reporting it here would double-count the same neighbour.
		if parsers.IPFamily(e.Address) != model.AddressFamilyIPv4 {
			continue
		}
		if e.MAC == "" {
			// An unresolved address: report it so the address is known to
			// exist, but it cannot become a device without a MAC.
			out = append(out, *unresolved(e, now))
			continue
		}
		out = append(out, *neighbour(e, now))
	}
	return out, nil
}

// readNeighbours returns the parsed neighbour entries.
func (c *Collector) readNeighbours(ctx context.Context) ([]parsers.NeighborEntry, error) {
	if c.neighPath != "" {
		data, ok, err := discovery.ReadFileCapped(c.neighPath, 0)
		if err != nil {
			return nil, fmt.Errorf("arp: read %s: %w", c.neighPath, err)
		}
		if !ok || len(data) == 0 {
			return nil, nil //nolint:nilerr // missing source, not a failure
		}
		entries, perr := parsers.ParseIPNeigh(data)
		if perr != nil {
			return nil, fmt.Errorf("arp: parse %s: %w", c.neighPath, perr)
		}
		return entries, nil
	}

	// `ip neigh show` is the richest source: it reports the interface and the
	// neighbour state, which /proc/net/arp cannot.
	if discovery.LookPath(cmdIP) {
		out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmdIP, "neigh", "show")
		if err == nil && len(out) > 0 {
			entries, perr := parsers.ParseIPNeigh(out)
			if perr != nil {
				return nil, fmt.Errorf("arp: parse ip neigh: %w", perr)
			}
			return entries, nil
		}
	}
	// The legacy `arp -n` output has the shape "<ip> ... <mac> ... <iface>".
	if discovery.LookPath(cmdArp) {
		out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmdArp, "-n")
		if err == nil && len(out) > 0 {
			return parseArpTable(out), nil
		}
	}
	// Last resort: /proc/net/arp, "<ip> <hexmac> <flags> <iface>".
	if data, ok, err := discovery.ReadFileCapped(procArp, 0); err == nil && ok && len(data) > 0 {
		return parseProcArp(data), nil
	}
	return nil, nil
}

// parseArpTable parses the legacy `arp -n` output.
func parseArpTable(data []byte) []parsers.NeighborEntry {
	var out []parsers.NeighborEntry
	for _, line := range discovery.Lines(data) {
		fields := parsers.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// Skip the header line.
		if fields[0] == "Address" {
			continue
		}
		macIdx := -1
		for i, f := range fields {
			if parsers.ValidMAC(f) {
				macIdx = i
				break
			}
		}
		if macIdx < 0 {
			continue
		}
		out = append(out, parsers.NeighborEntry{
			Address:   fields[0],
			MAC:       parsers.NormalizeMAC(fields[macIdx]),
			Interface: lastField(fields),
		})
	}
	return out
}

// parseProcArp parses /proc/net/arp: "<ip> <hexmac> <flags> <iface>".
func parseProcArp(data []byte) []parsers.NeighborEntry {
	var out []parsers.NeighborEntry
	for i, line := range discovery.Lines(data) {
		if i == 0 {
			continue // header
		}
		fields := parsers.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mac := fields[1]
		if !parsers.ValidMAC(mac) {
			continue
		}
		e := parsers.NeighborEntry{
			Address:   fields[0],
			MAC:       parsers.NormalizeMAC(mac),
			Interface: fields[3],
		}
		// Flag 0x2 = ATF_COM, the entry is complete.
		if fields[2] == "0x2" {
			e.State = model.NeighborReachable
		} else {
			e.State = model.NeighborStale
		}
		out = append(out, e)
	}
	return out
}

func lastField(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// neighbour builds the neighbour observation for an entry with a MAC.
func neighbour(e parsers.NeighborEntry, now time.Time) *discovery.Result {
	r := discovery.NewResult(discovery.ResultNeighbor, model.SourceARP, now)
	r.Neighbor = &discovery.NeighborObservation{
		Address:   e.Address,
		Family:    model.AddressFamilyIPv4,
		MAC:       e.MAC,
		Interface: e.Interface,
		State:     e.State,
		Router:    e.Router,
	}
	return r
}

// unresolved builds an address-only observation for an entry with no MAC.
//
// The MAC is deliberately empty: the identity engine refuses to create a device
// from an address alone, so this documents the address without inventing a
// device for it.
func unresolved(e parsers.NeighborEntry, now time.Time) *discovery.Result {
	r := discovery.NewResult(discovery.ResultNeighbor, model.SourceARP, now)
	r.Neighbor = &discovery.NeighborObservation{
		Address:   e.Address,
		Family:    model.AddressFamilyIPv4,
		Interface: e.Interface,
		State:     e.State,
	}
	return r
}

// Source reports which input the collector will use.
func (c *Collector) Sources() []string {
	switch {
	case c.neighPath != "":
		return []string{"file:" + c.neighPath}
	case discovery.LookPath(cmdIP):
		return []string{"ip neigh show"}
	case discovery.LookPath(cmdArp):
		return []string{"arp -n"}
	}
	return []string{procArp}
}
