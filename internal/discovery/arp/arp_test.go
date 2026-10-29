package arp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/parsers"
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

// TestMergeProcArpMACsFillsMissingLLADDR pins the cross-source fix.
//
// On the reference router the same host appears twice with different detail:
// `ip neigh show` reports "192.168.100.124 dev br-lan FAILED" and omits the
// lladdr, while /proc/net/arp keeps the MAC with flag 0x0. Without merging, the
// device is identified only through its IPv6 addresses, its reachable IPv4
// address is never attached, and the probe targets an unreachable IPv6 address
// instead - which reports a live host as offline.
func TestMergeProcArpMACsFillsMissingLLADDR(t *testing.T) {
	entries := []parsers.NeighborEntry{
		// FAILED: no MAC from `ip neigh`.
		{Address: "192.168.100.124", State: model.NeighborFailed},
		// A complete entry must be left alone.
		{Address: "192.168.100.116", MAC: "b0:82:e2:61:3e:b7", State: model.NeighborReachable},
		// An address /proc/net/arp does not know about stays without a MAC.
		{Address: "192.168.100.222", State: model.NeighborFailed},
	}

	dir := t.TempDir()
	procPath := filepath.Join(dir, "arp")
	procContent := `IP address       HW type     Flags       HW address            Mask     Device
192.168.100.124  0x1         0x0         8e:0a:79:80:f3:6c     *        br-lan
192.168.100.116  0x1         0x2         b0:82:e2:61:3e:b7     *        eth2
`
	if err := os.WriteFile(procPath, []byte(procContent), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &Collector{}
	c.neighPath = procPath
	// Point the merge at the fixture.
	mergeProcArpMACs(entries, procPath)

	// parsers.NormalizeMAC returns the colon-separated display form.
	if entries[0].MAC != "8e:0a:79:80:f3:6c" {
		t.Errorf("MAC was not filled in: %q", entries[0].MAC)
	}
	if entries[1].MAC != "b0:82:e2:61:3e:b7" {
		t.Errorf("an existing MAC was overwritten: %q", entries[1].MAC)
	}
	if entries[2].MAC != "" {
		t.Errorf("a MAC was invented for an unknown address: %q", entries[2].MAC)
	}
}

func TestMergeProcArpMACsIgnoresZeroMAC(t *testing.T) {
	dir := t.TempDir()
	procPath := filepath.Join(dir, "arp")
	writeFile(t, procPath, `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.10    0x1         0x0         00:00:00:00:00:00     *        eth3
`)
	entries := []parsers.NeighborEntry{{Address: "192.168.1.10", Interface: "eth3", State: model.NeighborFailed}}
	mergeProcArpMACs(entries, procPath)
	if entries[0].MAC != "" {
		t.Fatalf("zero MAC was copied: %q", entries[0].MAC)
	}
}

func TestCollectNormalizesZeroMACToUnresolved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neigh")
	writeFile(t, path, "192.168.1.10 dev eth3 lladdr 00:00:00:00:00:00 FAILED\n")
	c := NewWithPath(path)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Neighbor == nil || results[0].Neighbor.MAC != "" {
		t.Fatalf("zero MAC was not normalized: %+v", results)
	}
}
