// Package dhcp implements the DHCP lease collector.
//
// It reads the dnsmasq lease file and reports every lease. A lease is the
// strongest routine identity evidence Lanory has: the DHCP server itself
// asserted "this MAC owns this address", and most clients also send a hostname
// and sometimes a client identifier.
//
// What a lease is NOT:
//
//   - It is not a liveness signal. A lease can be expired but still held by a
//     device that is asleep; a device can be online without ever having asked
//     for a lease (static IP). Neither case makes a device offline.
//   - It is not a topology signal. A lease says nothing about which port the
//     device sits behind. That comes from the bridge FDB.
//
// The lease file path is configurable because OpenWrt distributions differ
// (/tmp/dhcp.leases on stock OpenWrt, /tmp/dnsmasq.leases on some builds, and
// something else again under iStoreOS). A missing file makes the collector
// unsupported, which is a normal state on a router that runs no DHCP server.
package dhcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

// DefaultLeasePath is the stock OpenWrt dnsmasq lease file.
const DefaultLeasePath = "/tmp/dhcp.leases"

// AlternateLeasePath is used by some OpenWrt builds and by iStoreOS.
const AlternateLeasePath = "/tmp/dnsmasq.leases"

// Collector reads the DHCP lease file.
type Collector struct {
	*discovery.BaseCollector

	// LeasePath is the lease file to read. Empty falls back to the defaults.
	LeasePath string

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// Compile-time proof that the collector satisfies the shared contract.
var _ discovery.Collector = (*Collector)(nil)

// New returns a DHCP collector reading the default lease path.
func New() *Collector {
	c := &Collector{now: time.Now}
	c.BaseCollector = discovery.NewBaseCollector(
		model.CollectorDHCP,
		"DHCP leases: MAC to IP mappings assigned by the local server",
		false, // support is probed in Supported()
		discovery.CapIPv4, discovery.CapMAC, discovery.CapHostname,
	)
	c.refreshSupported()
	return c
}

// NewWithPath builds a collector reading a specific lease file. It is the seam
// the fixture tests use.
func NewWithPath(path string) *Collector {
	c := New()
	c.LeasePath = path
	c.refreshSupported()
	return c
}

// SetLeasePath changes the lease file and re-probes support.
func (c *Collector) SetLeasePath(path string) {
	c.LeasePath = path
	c.refreshSupported()
}

// Supported reports whether a readable lease file exists. A router with no DHCP
// server (a pure AP, or one that delegates DHCP upstream) has no lease file,
// which is a supported-false condition rather than an error.
func (c *Collector) Supported() bool {
	return c.BaseCollector.Supported()
}

// refreshSupported probes the lease file once and caches the answer.
func (c *Collector) refreshSupported() {
	c.SetSupported(c.leaseFileExists())
	c.SetLastCheck(time.Now())
}

func (c *Collector) leaseFileExists() bool {
	for _, p := range c.candidatePaths() {
		st, err := discovery.StatFile(p)
		if err == nil && st.Exists && !st.IsDir {
			return true
		}
	}
	return false
}

// candidatePaths lists the lease files to look for, in order.
func (c *Collector) candidatePaths() []string {
	if c.LeasePath != "" {
		return []string{c.LeasePath}
	}
	return []string{DefaultLeasePath, AlternateLeasePath}
}

// Sources reports which lease file the collector will read.
func (c *Collector) Sources() []string {
	return c.candidatePaths()
}

// Collect reads the lease file and returns one observation per lease.
//
// Expired leases are still reported: an expired lease means the DHCP server has
// not heard from the device recently, which is a weaker signal than an ARP
// failure but is not a proof of absence. The expiry is carried on the
// observation so the topology engine can weigh it.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := c.now()
	if !c.Supported() {
		// Re-probe: the lease file may have appeared since the last run.
		c.refreshSupported()
		if !c.Supported() {
			return discovery.EmptyResults(), nil
		}
	}

	leases, err := c.readLeases()
	if err != nil {
		return nil, discovery.CollectError(c.Name(), err)
	}

	var out []discovery.Result
	for _, l := range leases {
		mac := parsers.CanonicalMAC(l.MAC)
		if mac == "" || parsers.IsZeroMAC(mac) || parsers.IsMulticastMAC(mac) {
			continue
		}
		clientID := l.ClientID
		if clientID == "*" || clientID == "" {
			// dnsmasq writes "*" when the client sent no client identifier.
			// It is not an identity, so it is not reported as one.
			clientID = ""
		}
		obs := &discovery.LeaseObservation{
			MAC:       parsers.NormalizeMAC(l.MAC),
			Address:   l.Address,
			Hostname:  l.Hostname,
			ClientID:  clientID,
			ExpiresAt: time.Unix(l.ExpiresAt, 0).UTC(),
		}
		r := discovery.NewResult(discovery.ResultLease, model.SourceDHCP, now)
		r.Lease = obs
		attrs := map[string]string{
			"address": l.Address,
			// Always explicit, so a consumer can distinguish "lease still
			// valid" from "no expiry information at all".
			"expired": boolStr(!obs.ExpiresAt.After(now)),
		}
		if l.Hostname != "" {
			attrs["hostname"] = l.Hostname
		}
		r.Attributes = attrs
		out = append(out, *r)
	}
	return out, nil
}

// readLeases parses the lease file. A missing file yields no leases and no
// error; an unreadable one is a real error.
func (c *Collector) readLeases() ([]parsers.LeaseEntry, error) {
	var (
		data     []byte
		ok       bool
		err      error
		usedPath string
	)
	for _, p := range c.candidatePaths() {
		data, ok, err = discovery.ReadFileCapped(p, 0)
		if err != nil {
			return nil, fmt.Errorf("dhcp: read %s: %w", p, err)
		}
		if ok {
			usedPath = p
			break
		}
	}
	if !ok {
		return nil, nil
	}
	leases, perr := parsers.ParseLeases(data)
	if perr != nil {
		if errors.Is(perr, parsers.ErrEmptyInput) {
			// An empty lease file means the DHCP server has handed out no
			// leases yet, which is a valid state, not a parse failure.
			return nil, nil
		}
		return nil, fmt.Errorf("dhcp: parse %s: %w", usedPath, perr)
	}
	return leases, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
