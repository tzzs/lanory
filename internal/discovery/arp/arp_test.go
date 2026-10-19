package arp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/discovery"
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
	c := NewWithPath(fixture(t, "ip-neigh.txt"))
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorARP {
		t.Fatalf("name = %q", c.Name())
	}
	if !contains(c.Capabilities(), discovery.CapIPv4) {
		t.Error("expected the ipv4 capability")
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestCollectIPv4Neighbours(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	byAddr := map[string]*model.NeighborState{}
	macByAddr := map[string]string{}
	ifaceByAddr := map[string]string{}
	for i := range results {
		r := &results[i]
		if r.Kind != "neighbor" || r.Neighbor == nil {
			t.Fatalf("unexpected result kind %q", r.Kind)
		}
		if r.Neighbor.Family != model.AddressFamilyIPv4 {
			t.Fatalf("family = %q, want ipv4", r.Neighbor.Family)
		}
		st := r.Neighbor.State
		byAddr[r.Neighbor.Address] = &st
		macByAddr[r.Neighbor.Address] = r.Neighbor.MAC
		ifaceByAddr[r.Neighbor.Address] = r.Neighbor.Interface
	}

	// The upstream router is a neighbour on the WAN port.
	if macByAddr["192.168.1.1"] != "03:11:22:33:44:55" {
		t.Errorf("gateway mac = %q", macByAddr["192.168.1.1"])
	}
	if ifaceByAddr["192.168.1.1"] != "eth0" {
		t.Errorf("gateway interface = %q", ifaceByAddr["192.168.1.1"])
	}

	// Windows PC is on eth2, not eth1: the fixture's key assertion.
	if ifaceByAddr["192.0.2.116"] != "eth2" {
		t.Errorf("windows pc interface = %q, want eth2", ifaceByAddr["192.0.2.116"])
	}
	if macByAddr["192.0.2.116"] != "02:11:22:33:44:b7" {
		t.Errorf("windows pc mac = %q", macByAddr["192.0.2.116"])
	}

	// Every state in the fixture is preserved verbatim. STALE and FAILED must
	// NOT be normalised to "reachable" or to "offline".
	for _, tc := range []struct {
		addr  string
		state model.NeighborState
	}{
		{"192.168.1.1", model.NeighborReachable},
		{"192.0.2.116", model.NeighborReachable},
		{"192.0.2.151", model.NeighborStale},
		{"192.0.2.222", model.NeighborReachable},
		{"192.0.2.197", model.NeighborStale},
		{"192.0.2.159", model.NeighborReachable},
		{"192.0.2.228", model.NeighborDelay},
		{"192.0.2.231", model.NeighborProbe},
	} {
		got, ok := byAddr[tc.addr]
		if !ok {
			t.Fatalf("%s missing", tc.addr)
		}
		if *got != tc.state {
			t.Errorf("%s: state = %q, want %q", tc.addr, *got, tc.state)
		}
	}
}

// An entry with no MAC must still be reported (it proves the address is being
// probed) but it must never look like a resolved device.
func TestCollectUnresolvedEntries(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var incomplete, failed *discovery.NeighborObservation
	for i := range results {
		n := results[i].Neighbor
		if n == nil {
			continue
		}
		switch n.Address {
		case "192.0.2.253":
			incomplete = n
		case "192.168.100.4":
			failed = n
		}
	}
	if incomplete == nil {
		t.Fatal("INCOMPLETE entry missing")
	}
	if incomplete.MAC != "" {
		t.Errorf("INCOMPLETE must carry no mac, got %q", incomplete.MAC)
	}
	if failed == nil {
		t.Fatal("FAILED entry missing")
	}
	if failed.MAC != "" {
		t.Errorf("FAILED must carry no mac, got %q", failed.MAC)
	}
}

// An IPv6 address in the input must be dropped: it belongs to the ndp collector.
func TestCollectDropsIPv6(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neigh")
	writeFile(t, path, "fe80::1 dev eth1 lladdr 06:77:88:99:aa:d2 REACHABLE\n192.0.2.151 dev eth1 lladdr 06:77:88:99:aa:d2 REACHABLE\n")
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 ipv4 neighbour, got %d", len(results))
	}
	if results[0].Neighbor.Address != "192.0.2.151" {
		t.Fatalf("unexpected neighbour %q", results[0].Neighbor.Address)
	}
	if results[0].Neighbor.Family != model.AddressFamilyIPv4 {
		t.Fatalf("family = %q", results[0].Neighbor.Family)
	}
}

// Every result must carry a source and a timestamp.
func TestCollectStampsResults(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i, r := range results {
		if r.Source != model.SourceARP {
			t.Errorf("result %d: source = %q", i, r.Source)
		}
		if r.ObservedAt.IsZero() {
			t.Errorf("result %d: no timestamp", i)
		}
	}
}

// A missing file makes the collector unsupported and yields no results.
func TestCollectMissingFile(t *testing.T) {
	c := NewWithPath(filepath.Join(t.TempDir(), "absent"))
	if c.Supported() {
		t.Fatal("a missing neighbour dump must be unsupported")
	}
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
