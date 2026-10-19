// Package ndp implements the IPv6 neighbour table collector.
//
// The IPv6 neighbour table is the IPv6 twin of the ARP table, with one
// significant difference: a single device routinely has several addresses —
// a link-local address derived from its MAC, one or more global addresses, and
// often a privacy (temporary) global address. Each address gets its own
// neighbour entry, but they all carry the SAME MAC.
//
// That is what makes the collector's job easy in one direction and hard in the
// other: it never creates a device, so it cannot accidentally produce one
// device per address. It reports one observation per (address, MAC) pair and
// lets the identity engine collapse them by MAC.
//
// The link-local address is kept, not dropped: for a device that has no global
// address (an IPv6-only device, which the fixture exercises) it is the only
// address there is. It is stored with its interface, so the topology engine can
// still place it on the right port.
//
// The router flag is preserved: an IPv6 neighbour that advertises itself as a
// router is a gateway, not a leaf device.
package ndp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

// Command names.
const (
	cmdIP = "ip"
)

// commandTimeout bounds one `ip -6 neigh show` invocation.
const commandTimeout = 4 * time.Second

// Collector reads the IPv6 neighbour table.
type Collector struct {
	*discovery.BaseCollector

	// neighPath overrides the `ip -6 neigh show` output with a fixture file.
	neighPath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns an NDP collector using the live system command.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorNDP,
		"IPv6 neighbour table (NDP): addresses, MACs and router flags",
		false, // support is probed in Supported()
		discovery.CapIPv6, discovery.CapMAC, discovery.CapLinks,
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

// Supported reports whether the collector can run.
//
// The `ip` binary is necessary but not sufficient: IPv6 may be disabled in the
// kernel. When `ip -6 neigh show` fails with "Family not supported" the
// collector reports unsupported on the next Collect rather than erroring.
func (c *Collector) Supported() bool {
	if c.neighPath != "" {
		st, err := discovery.StatFile(c.neighPath)
		return err == nil && st.Exists && !st.IsDir
	}
	return discovery.LookPath(cmdIP)
}

// Sources reports which input the collector will use.
func (c *Collector) Sources() []string {
	if c.neighPath != "" {
		return []string{"file:" + c.neighPath}
	}
	return []string{"ip -6 neigh show"}
}

// Collect reads the IPv6 neighbour table and returns one observation per entry.
//
// Deduplication happens here at the (MAC, address) level only: the same address
// seen twice in one dump is one observation. Multiple addresses for one MAC stay
// multiple observations, because they are genuinely different addresses and the
// identity engine is the component that decides they belong to one device.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	now := c.now()
	if !c.Supported() {
		return discovery.EmptyResults(), nil
	}

	entries, err := c.readNeigh(ctx)
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}

	seen := map[string]bool{}
	var out []discovery.Result
	for _, e := range entries {
		if !isIPv6(e.Address) {
			continue
		}
		mac := parsers.CanonicalMAC(e.MAC)
		if mac != "" && (parsers.IsZeroMAC(mac) || parsers.IsMulticastMAC(mac)) {
			continue
		}
		key := e.Address + "|" + mac
		if seen[key] {
			continue
		}
		seen[key] = true

		obs := &discovery.NeighborObservation{
			Address:   e.Address,
			Family:    model.AddressFamilyIPv6,
			MAC:       e.MAC,
			Interface: e.Interface,
			State:     e.State,
			Router:    e.Router,
		}
		r := discovery.NewResult(discovery.ResultNeighbor, model.SourceNDP, now)
		r.Neighbor = obs
		r.Attributes = map[string]string{
			"interface":  e.Interface,
			"state":      string(e.State),
			"link_local": boolStr(parsers.IsLinkLocalIPv6(e.Address)),
			"router":     boolStr(e.Router),
		}
		out = append(out, *r)
	}
	return out, nil
}

// readNeigh returns the parsed IPv6 neighbour entries.
//
// When the kernel has no IPv6 support the command exits non-zero with a
// "Family not supported" style message. That is an unsupported condition, so
// the collector returns no entries and marks itself unsupported.
func (c *Collector) readNeigh(ctx context.Context) ([]parsers.NeighborEntry, error) {
	if c.neighPath != "" {
		data, _, err := discovery.ReadFileCapped(c.neighPath, 0)
		if err != nil {
			return nil, fmt.Errorf("ndp: read %s: %w", c.neighPath, err)
		}
		entries, perr := parsers.ParseIPNeigh(data)
		if perr != nil {
			return nil, fmt.Errorf("ndp: parse %s: %w", c.neighPath, perr)
		}
		return entries, nil
	}
	out, err := discovery.RunCommand(ctx, discovery.ExecConfig{Timeout: commandTimeout}, cmdIP, "-6", "neigh", "show")
	if err != nil {
		if isUnsupportedFamily(err) {
			c.SetSupported(false)
			return nil, nil
		}
		return nil, nil //nolint:nilerr // an unavailable source is not an error
	}
	entries, perr := parsers.ParseIPNeigh(out)
	if perr != nil {
		return nil, fmt.Errorf("ndp: parse ip -6 neigh show: %w", perr)
	}
	return entries, nil
}

// isUnsupportedFamily reports whether err means the kernel has no IPv6.
func isUnsupportedFamily(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "Address family not supported")
}

func isIPv6(addr string) bool { return strings.Contains(addr, ":") }

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
