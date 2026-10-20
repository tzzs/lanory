// Package scan implements the optional active subnet scanner.
//
// Active scanning is deliberately not part of the default discovery path. The
// product spec requires that Lanory stay passive and cheap on a router, and an
// active sweep of a /24 is the most expensive thing it can be asked to do. This
// collector therefore:
//
//   - is disabled unless discovery.collectors.scan is enabled in the
//     configuration, so it never runs by accident;
//   - only ever touches subnets the operator authorised (the configured
//     discovery.subnets plus explicitly requested ones), and validates every
//     one of them against a strict CIDR grammar before expanding anything;
//   - caps the host count per run, so a /8 typo cannot turn into 16 million
//     probes;
//   - bounds concurrency and applies a per-second rate limit;
//   - aborts promptly on context cancellation;
//   - sends exactly one ICMP echo per host and never performs port scans,
//     banner grabs or anything resembling an attack.
//
// What it can prove: a host answered an echo request from the router running
// Lanory. That is reachability from this vantage point, not a physical
// connection, and it does not identify the device.
package scan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// Limit holds the guard rails for one scan run.
type Limit struct {
	// AuthorisedSubnets are the CIDRs the operator allows scanning. A subnet
	// requested at runtime must be a subset of one of these.
	AuthorisedSubnets []string
	// MaxHostsPerRun caps the total number of addresses probed in one run.
	MaxHostsPerRun int
	// MaxHostsPerSubnet caps the host count of a single subnet.
	MaxHostsPerSubnet int
	// Concurrency bounds in-flight probes.
	Concurrency int
	// RatePerSecond bounds how many probes start per second.
	RatePerSecond int
	// Timeout is the per-host echo timeout.
	Timeout time.Duration
}

// DefaultLimit returns conservative defaults for a low powered router.
func DefaultLimit() Limit {
	return Limit{
		AuthorisedSubnets: nil,
		MaxHostsPerRun:    1024,
		MaxHostsPerSubnet: 512,
		Concurrency:       16,
		RatePerSecond:     64,
		Timeout:           500 * time.Millisecond,
	}
}

// Prober sends one echo request and reports whether the host answered.
type Prober interface {
	// Ping reports whether host answered within timeout.
	Ping(ctx context.Context, host net.IP, timeout time.Duration) (bool, error)
}

// Collector is the active scan collector.
type Collector struct {
	*discovery.BaseCollector

	limit Limit
	probe Prober

	// targets is filled by the scheduler (discovery service) before a run via
	// SetTargets, so the collector itself stays stateless per run.
	mu      sync.Mutex
	targets []string
}

// New returns a scan collector using the default prober. It starts disabled.
func New(limit Limit) *Collector {
	return &Collector{
		BaseCollector: discovery.NewBaseCollector(
			model.CollectorScan,
			"Active ICMP sweep of authorised LAN subnets (opt-in)",
			true,
			discovery.CapIPv4,
			discovery.CapIPv6,
		),
		limit: limit,
		probe: newICMPProber(),
	}
}

// NewWithProber returns a scan collector with an injected prober, for tests.
func NewWithProber(limit Limit, p Prober) *Collector {
	c := New(limit)
	if p != nil {
		c.probe = p
	}
	return c
}

// SetTargets records the CIDRs to scan for the next run.
//
// Validation happens here, before a single packet is sent, so an unauthorised
// or malformed subnet is rejected instead of silently expanding.
func (c *Collector) SetTargets(subnets []string) error {
	cp, err := c.ValidateTargets(subnets)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.targets = cp
	c.mu.Unlock()
	return nil
}

// ValidateTargets checks the requested subnets against the configured policy and
// returns the canonical CIDR list.
//
// An empty request means "auto-detect", which uses the authorised subnets from
// the configuration. Every requested subnet must be contained in an authorised
// one; a request for an arbitrary internet range is refused.
func (c *Collector) ValidateTargets(subnets []string) ([]string, error) {
	if len(subnets) == 0 {
		return append([]string(nil), c.limit.AuthorisedSubnets...), nil
	}
	if len(c.limit.AuthorisedSubnets) == 0 {
		return nil, errors.New("scan: no authorised subnets are configured; refusing to scan")
	}
	allowed := make([]*net.IPNet, 0, len(c.limit.AuthorisedSubnets))
	for _, s := range c.limit.AuthorisedSubnets {
		_, n, err := net.ParseCIDR(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("scan: configured subnet %q is invalid: %w", s, err)
		}
		allowed = append(allowed, n)
	}

	out := make([]string, 0, len(subnets))
	for _, raw := range subnets {
		_, n, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("scan: %q is not a valid CIDR", raw)
		}
		if !containedInAny(n, allowed) {
			return nil, fmt.Errorf("scan: %s is not inside an authorised subnet", n.String())
		}
		if err := c.checkSize(n); err != nil {
			return nil, err
		}
		out = append(out, n.String())
	}
	return out, nil
}

// checkSize refuses a subnet whose host count exceeds the configured cap.
func (c *Collector) checkSize(n *net.IPNet) error {
	ones, bits := n.Mask.Size()
	if ones == 0 && bits == 0 {
		return fmt.Errorf("scan: %s has a non-contiguous mask", n.String())
	}
	hostBits := bits - ones
	// A /31 or /32 has 2 or 1 addresses; treat the host count generously and
	// cap it by MaxHostsPerSubnet rather than trying to be clever about
	// point-to-point links.
	var hosts uint64
	if hostBits >= 63 {
		hosts = 1 << 62
	} else {
		hosts = uint64(1) << hostBits
	}
	if hosts > uint64(c.limit.MaxHostsPerSubnet) {
		return fmt.Errorf("scan: %s contains %d addresses, above the %d per-subnet limit",
			n.String(), hosts, c.limit.MaxHostsPerSubnet)
	}
	return nil
}

// containedInAny reports whether n is fully inside one of the authorised nets.
func containedInAny(n *net.IPNet, allowed []*net.IPNet) bool {
	onesN, bitsN := n.Mask.Size()
	for _, a := range allowed {
		onesA, bitsA := a.Mask.Size()
		if bitsN != bitsA {
			continue // different address families never nest
		}
		if onesN < onesA {
			continue // n is wider than a, so it cannot be inside it
		}
		if a.Contains(n.IP) {
			return true
		}
	}
	return false
}

// Collect performs the sweep.
//
// It returns one Neighbor observation per responding host, carrying the address
// only. No MAC is knowable from an echo reply, so the identity engine will keep
// the device at low confidence until ARP or DHCP corroborates it — which is the
// honest outcome.
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error) {
	c.mu.Lock()
	targets := append([]string(nil), c.targets...)
	c.mu.Unlock()

	// With no explicit targets, scan the authorised configuration subnets.
	if len(targets) == 0 {
		targets = append([]string(nil), c.limit.AuthorisedSubnets...)
	}
	if len(targets) == 0 {
		return nil, nil
	}

	hosts := c.expand(targets)
	if len(hosts) == 0 {
		return nil, nil
	}
	if len(hosts) > c.limit.MaxHostsPerRun {
		hosts = hosts[:c.limit.MaxHostsPerRun]
	}

	results := c.sweep(ctx, hosts)
	return results, ctx.Err()
}

// expand walks the target CIDRs and returns the individual host addresses.
//
// Only usable unicast addresses are returned: the network and broadcast
// addresses of an IPv4 subnet are skipped, and IPv6 link-local scopes are
// ignored because a sweep only makes sense on a routed LAN prefix.
func (c *Collector) expand(targets []string) []net.IP {
	var out []net.IP
	seen := map[string]bool{}
	for _, raw := range targets {
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			continue
		}
		ones, bits := n.Mask.Size()
		if bits == 0 {
			continue
		}
		hostBits := bits - ones
		if hostBits > 20 {
			// Guard against accidental /8 expansion even if the size check was
			// bypassed by a caller that set the limit directly.
			continue
		}
		isV4 := n.IP.To4() != nil
		total := 1 << hostBits
		for i := 0; i < total; i++ {
			ip := addToIP(n.IP, i)
			if isV4 {
				// Skip network and broadcast addresses on IPv4.
				if i == 0 || i == total-1 {
					continue
				}
			}
			if ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			key := ip.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ip)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return bytesCompare(out[i], out[j]) < 0
	})
	return out
}

// sweep probes every host with bounded concurrency and a rate limit.
func (c *Collector) sweep(ctx context.Context, hosts []net.IP) []discovery.Result {
	workers := c.limit.Concurrency
	if workers <= 0 {
		workers = 8
	}
	if workers > len(hosts) {
		workers = len(hosts)
	}
	timeout := c.limit.Timeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	rate := c.limit.RatePerSecond
	var tick <-chan time.Time
	if rate > 0 {
		interval := time.Second / time.Duration(rate)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	type answer struct {
		ip net.IP
		ok bool
		at time.Time
	}

	jobs := make(chan net.IP)
	answers := make(chan answer, workers)
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				if tick != nil {
					select {
					case <-ctx.Done():
						return
					case <-tick:
					}
				}
				if ctx.Err() != nil {
					return
				}
				start := time.Now()
				ok, _ := c.probe.Ping(ctx, ip, timeout)
				answers <- answer{ip: ip, ok: ok, at: start}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, ip := range hosts {
			select {
			case <-ctx.Done():
				return
			case jobs <- ip:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(answers)
	}()

	var results []discovery.Result
	for a := range answers {
		if !a.ok {
			continue
		}
		ob := discovery.NeighborObservation{
			Address: a.ip.String(),
			Family:  familyOf(a.ip),
			// No MAC is knowable from an echo reply.
			State: model.NeighborReachable,
		}
		r := discovery.NewResult(discovery.ResultNeighbor, model.SourceScan, a.at)
		r.Neighbor = &ob
		r.Attributes = map[string]string{
			"method": "icmp_echo",
			"note":   "reachable from the Lanory host; not a physical link",
		}
		results = append(results, *r)
	}
	return results
}

func familyOf(ip net.IP) model.AddressFamily {
	if ip.To4() != nil {
		return model.AddressFamilyIPv4
	}
	return model.AddressFamilyIPv6
}

// addToIP returns base + offset without allocating a big integer.
func addToIP(base net.IP, offset int) net.IP {
	if v4 := base.To4(); v4 != nil {
		out := make(net.IP, 4)
		copy(out, v4)
		carry := offset
		for i := 3; i >= 0 && carry > 0; i-- {
			sum := int(out[i]) + carry
			out[i] = byte(sum & 0xff)
			carry = sum >> 8
		}
		return out
	}
	out := make(net.IP, 16)
	copy(out, base.To16())
	carry := offset
	for i := 15; i >= 0 && carry > 0; i-- {
		sum := int(out[i]) + carry
		out[i] = byte(sum & 0xff)
		carry = sum >> 8
	}
	return out
}

func bytesCompare(a, b net.IP) int {
	ab, bb := a.To16(), b.To16()
	if ab == nil || bb == nil {
		return strings.Compare(a.String(), b.String())
	}
	for i := 0; i < len(ab); i++ {
		if ab[i] != bb[i] {
			return int(ab[i]) - int(bb[i])
		}
	}
	return 0
}
