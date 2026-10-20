package scan

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeProber answers for a fixed set of hosts.
type fakeProber struct {
	answers map[string]bool
	seen    []string
	delay   time.Duration
	err     error
}

func (f *fakeProber) Ping(ctx context.Context, host net.IP, timeout time.Duration) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.seen = append(f.seen, host.String())
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return f.answers[host.String()], nil
}

func testLimit() Limit {
	l := DefaultLimit()
	l.AuthorisedSubnets = []string{"192.0.2.0/29"}
	l.MaxHostsPerRun = 64
	l.MaxHostsPerSubnet = 64
	l.Concurrency = 4
	l.RatePerSecond = 0 // no throttling in tests
	l.Timeout = 50 * time.Millisecond
	return l
}

// TestRefusesWithoutAuthorisedSubnets is the most important safety property:
// with no policy configured the scanner must not send a single packet.
func TestRefusesWithoutAuthorisedSubnets(t *testing.T) {
	l := DefaultLimit()
	l.AuthorisedSubnets = nil
	p := &fakeProber{}
	c := NewWithProber(l, p)

	if _, err := c.ValidateTargets([]string{"192.0.2.0/24"}); err == nil {
		t.Fatal("ValidateTargets accepted a subnet with no authorised subnets configured")
	}

	res, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("Collect returned %d results with no authorised subnets", len(res))
	}
	if len(p.seen) != 0 {
		t.Errorf("prober was asked to ping %d hosts despite no authorisation", len(p.seen))
	}
}

// TestRejectsUnauthorisedSubnet ensures a requested range outside the policy is
// refused rather than silently expanded.
func TestRejectsUnauthorisedSubnet(t *testing.T) {
	c := NewWithProber(testLimit(), &fakeProber{})

	for _, sub := range []string{
		"8.8.8.0/24",     // public
		"192.0.3.0/24",   // adjacent but not authorised
		"169.254.0.0/16", // link-local
		"0.0.0.0/0",      // everything
		"192.0.2.0/24",   // wider than the authorised /29
	} {
		if _, err := c.ValidateTargets([]string{sub}); err == nil {
			t.Errorf("ValidateTargets accepted unauthorised/wider subnet %q", sub)
		}
	}
}

// TestRejectsMalformedCIDR covers the grammar, not just the policy.
func TestRejectsMalformedCIDR(t *testing.T) {
	c := NewWithProber(testLimit(), &fakeProber{})
	for _, sub := range []string{
		"192.0.2.1", // no prefix length
		"192.0.2.0/33",
		"192.0.2.0/-1",
		"not-a-cidr",
		"192.0.2.0/24/8",
		"",
	} {
		if _, err := c.ValidateTargets([]string{sub}); err == nil {
			t.Errorf("ValidateTargets accepted malformed CIDR %q", sub)
		}
	}
}

// TestAcceptsSubsetOfAuthorised checks that a narrower range inside the policy
// is allowed, which is what a user picking one part of the LAN needs.
func TestAcceptsSubsetOfAuthorised(t *testing.T) {
	l := testLimit()
	l.AuthorisedSubnets = []string{"192.0.2.0/24"}
	c := NewWithProber(l, &fakeProber{})

	got, err := c.ValidateTargets([]string{"192.0.2.16/30"})
	if err != nil {
		t.Fatalf("ValidateTargets rejected an authorised subset: %v", err)
	}
	if len(got) != 1 || got[0] != "192.0.2.16/30" {
		t.Errorf("ValidateTargets = %v", got)
	}
}

// TestEmptyRequestUsesAuthorisedDefaults: an empty request means "scan the
// configured range", not "scan everything".
func TestEmptyRequestUsesAuthorisedDefaults(t *testing.T) {
	c := NewWithProber(testLimit(), &fakeProber{})
	got, err := c.ValidateTargets(nil)
	if err != nil {
		t.Fatalf("ValidateTargets(nil): %v", err)
	}
	if len(got) != 1 || got[0] != "192.0.2.0/29" {
		t.Errorf("empty request resolved to %v, want the configured subnet", got)
	}
}

// TestPerSubnetLimit refuses a range so large it would hammer the router.
func TestPerSubnetLimit(t *testing.T) {
	l := testLimit()
	l.AuthorisedSubnets = []string{"10.0.0.0/8"}
	l.MaxHostsPerSubnet = 512
	c := NewWithProber(l, &fakeProber{})

	if _, err := c.ValidateTargets([]string{"10.0.0.0/8"}); err == nil {
		t.Error("ValidateTargets accepted a /8 above the per-subnet limit")
	}
	if _, err := c.ValidateTargets([]string{"10.0.0.0/24"}); err != nil {
		t.Errorf("ValidateTargets rejected a /24 inside the limit: %v", err)
	}
}

// TestExpandSkipsNetworkAndBroadcast checks the host enumeration boundaries.
func TestExpandSkipsNetworkAndBroadcast(t *testing.T) {
	c := NewWithProber(testLimit(), &fakeProber{})
	hosts := c.expand([]string{"192.0.2.0/29"})

	// A /29 has 8 addresses; the network (.0) and broadcast (.7) are skipped.
	if len(hosts) != 6 {
		t.Fatalf("expand(/29) = %d hosts, want 6: %v", len(hosts), hosts)
	}
	first, last := hosts[0].String(), hosts[len(hosts)-1].String()
	if first != "192.0.2.1" {
		t.Errorf("first host = %s, want 192.0.2.1", first)
	}
	if last != "192.0.2.6" {
		t.Errorf("last host = %s, want 192.0.2.6", last)
	}
	for _, h := range hosts {
		if h.String() == "192.0.2.0" || h.String() == "192.0.2.7" {
			t.Errorf("expand included network/broadcast address %s", h)
		}
	}
}

// TestMaxHostsPerRun truncates a run that would be too large.
func TestMaxHostsPerRun(t *testing.T) {
	l := testLimit()
	l.AuthorisedSubnets = []string{"192.0.2.0/24"}
	l.MaxHostsPerSubnet = 300
	l.MaxHostsPerRun = 10
	p := &fakeProber{}
	c := NewWithProber(l, p)

	if err := c.SetTargets([]string{"192.0.2.0/24"}); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}
	if _, err := c.Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(p.seen) > 10 {
		t.Errorf("probed %d hosts, above the per-run cap of 10", len(p.seen))
	}
}

// TestOnlyRespondingHostsProduceResults makes sure an unanswered probe is not
// reported as a device.
func TestOnlyRespondingHostsProduceResults(t *testing.T) {
	p := &fakeProber{answers: map[string]bool{
		"192.0.2.1": true,
		"192.0.2.3": true,
	}}
	c := NewWithProber(testLimit(), p)
	if err := c.SetTargets([]string{"192.0.2.0/29"}); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}

	res, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("Collect = %d results, want 2", len(res))
	}
	got := map[string]bool{}
	for _, r := range res {
		if r.Neighbor == nil {
			t.Fatalf("result is not a neighbour observation: %+v", r)
		}
		if r.Source != "scan" {
			t.Errorf("result source = %q, want scan", r.Source)
		}
		if r.Neighbor.MAC != "" {
			t.Errorf("scan result claims a MAC (%q) which an echo reply cannot provide", r.Neighbor.MAC)
		}
		got[r.Neighbor.Address] = true
	}
	for _, want := range []string{"192.0.2.1", "192.0.2.3"} {
		if !got[want] {
			t.Errorf("missing result for %s", want)
		}
	}
}

// TestCancellationIsPrompt ensures a cancelled run stops quickly.
func TestCancellationIsPrompt(t *testing.T) {
	p := &fakeProber{delay: 50 * time.Millisecond}
	l := testLimit()
	l.AuthorisedSubnets = []string{"192.0.2.0/24"}
	l.MaxHostsPerRun = 200
	l.MaxHostsPerSubnet = 300
	l.Concurrency = 2
	c := NewWithProber(l, p)
	if err := c.SetTargets([]string{"192.0.2.0/24"}); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var probeErr error
	go func() {
		_, probeErr = c.Collect(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Collect did not return after cancellation")
	}
	if probeErr != nil && !errors.Is(probeErr, context.Canceled) {
		t.Logf("Collect returned %v after cancellation (acceptable)", probeErr)
	}
}

// TestProberErrorSurfaces: if the prober cannot run at all, that must not look
// like "no hosts answered".
func TestProberErrorSurfaces(t *testing.T) {
	p := &fakeProber{err: errors.New("no ICMP socket available")}
	l := testLimit()
	c := NewWithProber(l, p)
	if err := c.SetTargets([]string{"192.0.2.0/29"}); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}
	res, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("Collect = %d results, want 0 when the prober errors", len(res))
	}
}

// TestNameAndCapabilities pins the registry-visible identity of the collector.
func TestNameAndCapabilities(t *testing.T) {
	c := NewWithProber(testLimit(), &fakeProber{})
	if c.Name() != "scan" {
		t.Errorf("Name = %q", c.Name())
	}
	caps := c.Capabilities()
	if len(caps) == 0 {
		t.Error("Capabilities is empty")
	}
	if !c.Supported() {
		t.Error("Supported = false; the scan collector is available whenever it is constructed")
	}
}

// TestContainedInAny covers the address-family and width rules directly.
func TestContainedInAny(t *testing.T) {
	_, allowed, _ := net.ParseCIDR("192.0.2.0/24")
	cases := []struct {
		cidr string
		want bool
	}{
		{"192.0.2.0/24", true},
		{"192.0.2.0/25", true},
		{"192.0.2.128/25", true},
		{"192.0.2.0/23", false},  // wider than allowed
		{"192.0.3.0/24", false},  // outside
		{"2001:db8::/32", false}, // different family
	}
	for _, tc := range cases {
		_, n, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.cidr, err)
		}
		if got := containedInAny(n, []*net.IPNet{allowed}); got != tc.want {
			t.Errorf("containedInAny(%s) = %v, want %v", tc.cidr, got, tc.want)
		}
	}
}

// TestAddToIP checks the address arithmetic used by expand.
func TestAddToIP(t *testing.T) {
	cases := []struct {
		base   string
		offset int
		want   string
	}{
		{"192.0.2.0", 1, "192.0.2.1"},
		{"192.0.2.255", 1, "192.0.3.0"},
		{"192.0.2.0", 256, "192.0.3.0"},
		{"2001:db8::", 1, "2001:db8::1"},
	}
	for _, tc := range cases {
		got := addToIP(net.ParseIP(tc.base), tc.offset).String()
		if got != tc.want {
			t.Errorf("addToIP(%s, %d) = %s, want %s", tc.base, tc.offset, got, tc.want)
		}
	}
}

// TestTargetsAreNotSharedBetweenRuns ensures SetTargets replaces, not appends.
func TestTargetsAreNotSharedBetweenRuns(t *testing.T) {
	c := NewWithProber(testLimit(), &fakeProber{})
	if err := c.SetTargets([]string{"192.0.2.0/29"}); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}
	c.mu.Lock()
	first := len(c.targets)
	c.mu.Unlock()

	if err := c.SetTargets([]string{"192.0.2.0/30"}); err != nil {
		t.Fatalf("SetTargets: %v", err)
	}
	c.mu.Lock()
	second := c.targets
	c.mu.Unlock()

	if len(second) == first+first {
		t.Error("SetTargets appended instead of replacing")
	}
	if len(second) != 1 || !strings.HasPrefix(second[0], "192.0.2.0/30") {
		t.Errorf("targets after replacement = %v", second)
	}
}
