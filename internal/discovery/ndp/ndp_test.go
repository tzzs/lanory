package ndp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "easepi-r1", name))
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}
	return p
}

func newFixtureCollector(t *testing.T) *Collector {
	t.Helper()
	c := NewWithPath(fixture(t, "ip-neigh6.txt"))
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorNDP {
		t.Fatalf("name = %q", c.Name())
	}
	if c.Description() == "" {
		t.Fatal("expected a description")
	}
	caps := c.Capabilities()
	want := map[string]bool{discoveryCapIPv6(): true, discoveryCapMAC(): true}
	for _, c := range caps {
		delete(want, c)
	}
	if len(want) != 0 {
		t.Errorf("missing capabilities: %v", want)
	}
}

func TestCollectNeighbours(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 7 {
		t.Fatalf("expected 7 neighbour observations, got %d", len(results))
	}
	for i := range results {
		r := &results[i]
		if r.Kind != "neighbor" {
			t.Fatalf("unexpected kind %q", r.Kind)
		}
		if r.Neighbor == nil {
			t.Fatal("neighbour payload missing")
		}
		if r.Source != model.SourceNDP {
			t.Errorf("source = %q", r.Source)
		}
		if r.ObservedAt.IsZero() {
			t.Error("missing timestamp")
		}
		if r.Neighbor.Family != model.AddressFamilyIPv6 {
			t.Errorf("family = %q", r.Neighbor.Family)
		}
		if r.Neighbor.MAC == "" {
			t.Errorf("neighbour %s has no mac", r.Neighbor.Address)
		}
	}
}

// The same MAC seen with several addresses must produce several observations
// and exactly ONE device downstream. The collector's half of that contract is:
// one observation per (address, MAC), never one device per address and never a
// collapsed single observation that loses the addresses.
func TestSameMACMultipleAddresses(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	byMAC := map[string][]string{}
	for i := range results {
		n := results[i].Neighbor
		byMAC[n.MAC] = append(byMAC[n.MAC], n.Address)
	}
	// The iPhone has a link-local and a global address.
	iphone := byMAC["06:77:88:99:aa:d2"]
	if len(iphone) != 2 {
		t.Fatalf("expected 2 addresses for the iPhone MAC, got %v", iphone)
	}
	hasLinkLocal, hasGlobal := false, false
	for _, a := range iphone {
		if a == "fe80::6f8b:4aff:fe4a:3dd2" {
			hasLinkLocal = true
		}
		if a == "2001:db8:100::151" {
			hasGlobal = true
		}
	}
	if !hasLinkLocal || !hasGlobal {
		t.Fatalf("expected a link-local and a global address, got %v", iphone)
	}

	// The Mac Studio likewise.
	if got := len(byMAC["07:88:99:aa:bb:5e"]); got != 2 {
		t.Fatalf("expected 2 addresses for the Mac Studio MAC, got %d", got)
	}
	// Every other MAC has exactly one address.
	for mac, addrs := range byMAC {
		if mac == "06:77:88:99:aa:d2" || mac == "07:88:99:aa:bb:5e" {
			continue
		}
		if len(addrs) != 1 {
			t.Errorf("%s: expected 1 address, got %v", mac, addrs)
		}
	}
}

// The IPv6-only device must survive with its single link-local address.
func TestIPv6OnlyDevice(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var found bool
	for i := range results {
		n := results[i].Neighbor
		if n.MAC != "b4:11:22:33:44:13" {
			continue
		}
		found = true
		if n.Address != "fe80::1a2b:3c4d:5e6f:7012" {
			t.Errorf("address = %q", n.Address)
		}
		if !isLinkLocal(n.Address) {
			t.Error("expected a link-local address")
		}
		// STALE must not be upgraded to reachable, and must not mean offline.
		if n.State != model.NeighborStale {
			t.Errorf("state = %q, want stale", n.State)
		}
		if results[i].Attributes["link_local"] != "true" {
			t.Errorf("link_local attribute = %q", results[i].Attributes["link_local"])
		}
	}
	if !found {
		t.Fatal("the IPv6-only device is missing")
	}
}

// The router flag must be preserved: it identifies the upstream gateway.
func TestRouterFlag(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var routers int
	for i := range results {
		if results[i].Neighbor.Router {
			routers++
			if results[i].Neighbor.MAC != "06:77:88:99:aa:d2" {
				t.Errorf("unexpected router MAC %q", results[i].Neighbor.MAC)
			}
			if results[i].Attributes["router"] != "true" {
				t.Errorf("router attribute = %q", results[i].Attributes["router"])
			}
		}
	}
	if routers != 1 {
		t.Fatalf("expected exactly 1 router neighbour, got %d", routers)
	}
}

// A STALE entry must never be interpreted as "offline" by the collector: it
// reports the state verbatim and nothing else.
func TestStaleIsNotOffline(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i := range results {
		n := results[i].Neighbor
		if n.State != model.NeighborStale {
			continue
		}
		// There is no "offline" concept in the result at all, which is the
		// point: absence of liveness evidence is not a liveness verdict.
		for k, v := range results[i].Attributes {
			if k == "offline" || v == "offline" {
				t.Fatalf("stale entry %s emitted an offline signal", n.Address)
			}
		}
	}
}

// IPv4 addresses in the input must be dropped: they belong to the arp collector.
func TestDropsIPv4(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neigh6")
	content := "192.0.2.151 dev eth1 lladdr 06:77:88:99:aa:d2 REACHABLE\n" +
		"fe80::1 dev eth1 lladdr 06:77:88:99:aa:d2 REACHABLE\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 ipv6 neighbour, got %d", len(results))
	}
	if results[0].Neighbor.Address != "fe80::1" {
		t.Fatalf("unexpected address %q", results[0].Neighbor.Address)
	}
}

func TestDeduplicatesIdenticalEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neigh6")
	content := "fe80::1 dev eth1 lladdr 06:77:88:99:aa:d2 REACHABLE\n" +
		"fe80::1 dev eth1 lladdr 06:77:88:99:aa:d2 REACHABLE\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 observation after dedupe, got %d", len(results))
	}
}

func TestGarbageInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neigh6")
	if err := os.WriteFile(path, []byte("garbage\n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func isLinkLocal(addr string) bool {
	return len(addr) >= 4 && addr[:4] == "fe80"
}

func discoveryCapIPv6() string { return "ipv6" }
func discoveryCapMAC() string  { return "mac" }
