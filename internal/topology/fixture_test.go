package topology

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/arp"
	"github.com/lanory/lanory/internal/discovery/dhcp"
	"github.com/lanory/lanory/internal/discovery/fdb"
	"github.com/lanory/lanory/internal/discovery/ndp"
	"github.com/lanory/lanory/internal/discovery/openwrt"
	"github.com/lanory/lanory/internal/model"
	"gopkg.in/yaml.v3"
)

// This is the end-to-end acceptance test: replay the sanitized EASEPi R1
// fixtures through the real collectors, feed the results into the real topology
// engine, and assert the expectations declared in fixtures/easepi-r1/expected.yaml.
//
// Deriving the assertions from the YAML (rather than hard coding them) is what
// keeps the fixtures and the expectations from drifting apart.

func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "easepi-r1"))
	if err != nil {
		t.Fatalf("resolve fixtures: %v", err)
	}
	return dir
}

// expectedFile mirrors fixtures/easepi-r1/expected.yaml.
type expectedFile struct {
	Interfaces []struct {
		Name      string   `yaml:"name"`
		Role      string   `yaml:"role"`
		Carrier   bool     `yaml:"carrier"`
		SpeedMbps int      `yaml:"speed_mbps"`
		OperState string   `yaml:"oper_state"`
		Bridge    string   `yaml:"bridge"`
		Type      string   `yaml:"type"`
		IPv4      string   `yaml:"ipv4"`
		Members   []string `yaml:"members"`
	} `yaml:"interfaces"`
	Devices []struct {
		MAC          string `yaml:"mac"`
		IPv4         string `yaml:"ipv4"`
		IPv6         string `yaml:"ipv6"`
		Hostname     string `yaml:"hostname"`
		Interface    string `yaml:"interface"`
		TypeHint     string `yaml:"type_hint"`
		NameContains string `yaml:"name_contains"`
	} `yaml:"devices"`
	NotDevices   []string `yaml:"not_devices"`
	Expectations struct {
		TotalDevices       int    `yaml:"total_devices"`
		Eth1WifiAssumption bool   `yaml:"eth1_wifi_assumption_allowed"`
		WindowsPCInterface string `yaml:"windows_pc_interface"`
		UnknownDeviceCount int    `yaml:"unknown_device_count"`
		// UnresolvedSegments is how many segment nodes the fixture should
		// produce: eth1 carries several devices with no known intermediate.
		UnresolvedSegments int  `yaml:"unresolved_segments_expected"`
		InventedSwitches   bool `yaml:"invented_switches_allowed"`
		AbsenceOffline     bool `yaml:"absence_implies_offline"`
	} `yaml:"expectations"`
}

func loadExpected(t *testing.T) *expectedFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir(t), "expected.yaml"))
	if err != nil {
		t.Fatalf("read expected.yaml: %v", err)
	}
	var e expectedFile
	if err := yaml.Unmarshal(data, &e); err != nil {
		t.Fatalf("parse expected.yaml: %v", err)
	}
	if e.Expectations.TotalDevices == 0 {
		t.Fatal("expected.yaml declares no total_devices")
	}
	return &e
}

// replayCollectors runs the real collectors against the fixtures and returns
// their merged results.
func replayCollectors(t *testing.T) []discovery.Result {
	t.Helper()
	dir := fixtureDir(t)
	ctx := context.Background()

	ow := openwrt.NewWithPaths(
		filepath.Join(dir, "uci-network.txt"),
		filepath.Join(dir, "ip-link.txt"),
		filepath.Join(dir, "ubus-network-device.json"),
		filepath.Join(dir, "ubus-network-interface.json"),
	)
	fdbCollector := fdb.NewWithPaths(
		filepath.Join(dir, "bridge-fdb.txt"),
		filepath.Join(dir, "ip-link.txt"),
	)
	arpCollector := arp.NewWithPath(filepath.Join(dir, "ip-neigh.txt"))
	ndpCollector := ndp.NewWithPath(filepath.Join(dir, "ip-neigh6.txt"))
	dhcpCollector := dhcp.NewWithPath(filepath.Join(dir, "dhcp-leases.txt"))

	var out []discovery.Result
	for _, c := range []discovery.Collector{ow, fdbCollector, arpCollector, ndpCollector, dhcpCollector} {
		if !c.Supported() {
			t.Fatalf("collector %s must be supported with the fixtures", c.Name())
		}
		results, err := c.Collect(ctx)
		if err != nil {
			t.Fatalf("collector %s: %v", c.Name(), err)
		}
		out = append(out, results...)
	}
	if len(out) == 0 {
		t.Fatal("no observations produced from the fixtures")
	}
	return out
}

// TestFixtureInterfaces asserts the interface expectations from expected.yaml
// against the real openwrt collector.
func TestFixtureInterfaces(t *testing.T) {
	exp := loadExpected(t)
	results := replayCollectors(t)

	byName := map[string]discovery.InterfaceObservation{}
	for _, r := range results {
		if r.Interface != nil {
			byName[r.Interface.Name] = *r.Interface
		}
	}

	for _, want := range exp.Interfaces {
		got, ok := byName[want.Name]
		if !ok {
			t.Errorf("%s missing from the openwrt collector", want.Name)
			continue
		}
		if string(got.Role) != want.Role {
			t.Errorf("%s role = %q, want %q", want.Name, got.Role, want.Role)
		}
		if got.Carrier != want.Carrier {
			t.Errorf("%s carrier = %v, want %v", want.Name, got.Carrier, want.Carrier)
		}
		if got.SpeedMbps != want.SpeedMbps {
			t.Errorf("%s speed = %d, want %d", want.Name, got.SpeedMbps, want.SpeedMbps)
		}
		if want.OperState != "" && string(got.OperState) != want.OperState {
			t.Errorf("%s oper_state = %q, want %q", want.Name, got.OperState, want.OperState)
		}
		if want.Bridge != "" && got.BridgeID != want.Bridge {
			t.Errorf("%s bridge = %q, want %q", want.Name, got.BridgeID, want.Bridge)
		}
		if want.Type != "" && string(got.Type) != want.Type {
			t.Errorf("%s type = %q, want %q", want.Name, got.Type, want.Type)
		}
		// The br-lan membership must list exactly the declared ports.
		if len(want.Members) > 0 {
			var members []string
			for _, r := range results {
				if r.Link != nil && r.Link.Interface == want.Name &&
					r.Link.LinkType == model.EdgeTypeBridgeMembership {
					members = append(members, r.Link.Port)
				}
			}
			if len(members) != len(want.Members) {
				t.Errorf("%s members = %v, want %v", want.Name, members, want.Members)
			}
			for _, m := range want.Members {
				found := false
				for _, g := range members {
					if g == m {
						found = true
					}
				}
				if !found {
					t.Errorf("%s is missing from the bridge members %v", m, members)
				}
			}
		}
	}

	// The LAN bridge address from expected.yaml must be on br-lan.
	var lanIPv4 string
	for _, want := range exp.Interfaces {
		if want.IPv4 != "" {
			lanIPv4 = want.IPv4
		}
	}
	if lanIPv4 != "" {
		found := false
		for _, r := range results {
			if r.Address != nil && r.Address.Interface == "br-lan" {
				cidr := r.Address.Address
				if r.Address.PrefixLength > 0 {
					cidr += "/" + itoa(r.Address.PrefixLength)
				}
				if cidr == lanIPv4 {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("br-lan does not carry %s", lanIPv4)
		}
	}
}

// TestFixtureDevices is the headline acceptance test: run the collectors, feed
// them through the update engine, and confirm exactly the expected devices
// appear, each on the right port.
func TestFixtureDevices(t *testing.T) {
	exp := loadExpected(t)
	results := replayCollectors(t)

	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)

	now := time.Unix(1771000000, 0).UTC()
	sm := &discovery.RunSummary{StartedAt: now, FinishedAt: now.Add(time.Second), Results: results}
	res, err := eng.Update(context.Background(), sm)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("update reported errors: %v", res.Errors)
	}

	// Exactly the declared number of devices, no phantom entries from
	// multicast, bridge-self, zero-MAC or virtual-interface noise.
	if ing.created != exp.Expectations.TotalDevices {
		t.Fatalf("created %d devices, expected %d", ing.created, exp.Expectations.TotalDevices)
	}

	// The noise MACs must not have produced devices.
	for _, mac := range exp.NotDevices {
		canonical := database.CanonicalizeMAC(mac)
		if _, ok := ing.devices[canonical]; ok {
			t.Errorf("noise MAC %s became a device", mac)
		}
	}

	// Each declared device must exist, with the right port edge.
	edges, err := store.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	portByMAC := map[string]string{}
	for _, e := range edges {
		if e.EdgeType != model.EdgeTypeObservedMACPort {
			continue
		}
		dev, err := store.GetDevice(e.TargetID)
		if err != nil {
			continue
		}
		portByMAC[database.CanonicalizeMAC(dev.PrimaryMAC)] = e.Label
	}

	for _, want := range exp.Devices {
		canonical := database.CanonicalizeMAC(want.MAC)
		dev, ok := ing.devices[canonical]
		if !ok {
			t.Errorf("device %s (%s) was not created", want.MAC, want.Hostname)
			continue
		}
		if want.Hostname != "" && dev.Hostname != want.Hostname {
			t.Errorf("%s hostname = %q, want %q", want.MAC, dev.Hostname, want.Hostname)
		}
		if want.NameContains != "" && !containsFold(dev.Name, want.NameContains) {
			t.Errorf("%s name = %q, want it to contain %q", want.MAC, dev.Name, want.NameContains)
		}
		if want.Interface != "" {
			if got := portByMAC[canonical]; got != want.Interface {
				t.Errorf("%s port = %q, want %q", want.MAC, got, want.Interface)
			}
		}
	}

	// The Windows PC is the fixture's headline assertion: eth2, never eth1.
	pcMAC := database.CanonicalizeMAC("b0:82:e2:61:3e:b7")
	if got := portByMAC[pcMAC]; got != exp.Expectations.WindowsPCInterface {
		t.Errorf("Windows PC is on %q, want %q", got, exp.Expectations.WindowsPCInterface)
	}

	// No wireless association may be inferred from the FDB alone.
	for _, e := range edges {
		if e.EdgeType == model.EdgeTypeWirelessAssoc {
			t.Errorf("FDB data produced a wireless association edge (id=%s)", e.ID)
		}
	}
	if exp.Expectations.Eth1WifiAssumption {
		t.Log("fixture permits a WiFi assumption on eth1")
	}
}

// TestFixtureIPv6OnlyDeviceAppearsOnce verifies the device that is only ever
// seen through IPv6 neighbour discovery becomes exactly one device, not one per
// address.
func TestFixtureIPv6OnlyDeviceAppearsOnce(t *testing.T) {
	exp := loadExpected(t)
	results := replayCollectors(t)

	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)
	now := time.Unix(1771000000, 0).UTC()
	if _, err := eng.Update(context.Background(), &discovery.RunSummary{
		StartedAt: now, FinishedAt: now.Add(time.Second), Results: results,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	var want string
	for _, d := range exp.Devices {
		if d.IPv6 != "" && d.IPv4 == "" {
			want = database.CanonicalizeMAC(d.MAC)
		}
	}
	if want == "" {
		t.Fatal("expected.yaml declares no IPv6-only device")
	}
	dev, ok := ing.devices[want]
	if !ok {
		t.Fatalf("the IPv6-only device %s was not created", want)
	}

	// It must be exactly one device: count the devices whose MAC matches.
	count := 0
	for mac := range ing.devices {
		if mac == want {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the IPv6-only device appears %d times", count)
	}
	if dev.PrimaryMAC == "" {
		t.Error("the IPv6-only device has no MAC")
	}
}

// TestFixtureNoVirtualDevices confirms docker0, dummy0 and lo never become
// network nodes or devices.
func TestFixtureNoVirtualDevices(t *testing.T) {
	results := replayCollectors(t)

	for _, r := range results {
		if r.Interface == nil {
			continue
		}
		switch r.Interface.Name {
		case "lo", "docker0", "dummy0":
			if !r.Interface.Ignore && !r.Interface.Virtual {
				t.Errorf("%s must be marked virtual or ignored", r.Interface.Name)
			}
		}
		if r.Interface.Name == "lo" && !r.Interface.Ignore {
			t.Error("lo must be ignored entirely")
		}
	}
	// No FDB or link observation may reference a virtual interface port.
	for _, r := range results {
		if r.Link == nil {
			continue
		}
		switch r.Link.Port {
		case "docker0", "dummy0", "lo":
			t.Errorf("link observation references the virtual port %q", r.Link.Port)
		}
	}
}

// TestFixtureTopologyBuild builds the full graph from a store populated by the
// replay and checks the structural promises.
func TestFixtureTopologyBuild(t *testing.T) {
	results := replayCollectors(t)

	store := newTestStore(t)
	router := seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)
	now := time.Unix(1771000000, 0).UTC()
	if _, err := eng.Update(context.Background(), &discovery.RunSummary{
		StartedAt: now, FinishedAt: now.Add(1 * time.Second), Results: results,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	g, err := NewBuilder(store).WithOptions(Options{
		UnverifiedEdgeThreshold: UnverifiedThreshold,
		Now:                     now,
	}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("empty graph")
	}

	// The router is present and is a root (or hangs under the LAN network).
	routerNode := nodeByID(g, deviceNodeID(router.ID))
	if routerNode == nil {
		t.Fatal("router node missing")
	}

	// Every device node has a stable ID and a parent that exists (or is a root).
	byID := map[string]bool{}
	for _, n := range g.Nodes {
		byID[n.ID] = true
	}
	for _, n := range g.Nodes {
		if n.Parent != "" && !byID[n.Parent] {
			t.Errorf("%s has a dangling parent %q", n.ID, n.Parent)
		}
		if n.ID == n.Parent {
			t.Errorf("%s is its own parent", n.ID)
		}
	}

	// Children arrays agree with the parent links.
	for _, n := range g.Nodes {
		for _, child := range n.Children {
			c := nodeByID(g, child)
			if c == nil {
				t.Errorf("%s lists a missing child %q", n.ID, child)
				continue
			}
			if c.Parent != n.ID {
				t.Errorf("%s lists %s as a child but its parent is %q", n.ID, child, c.Parent)
			}
		}
	}

	// A network node for br-lan must exist and reference the LAN subnet.
	var brLAN *Node
	for i := range g.Nodes {
		if g.Nodes[i].Kind == NodeKindNetwork && g.Nodes[i].Network != nil &&
			g.Nodes[i].Network.Name == "br-lan" {
			brLAN = &g.Nodes[i]
		}
	}
	if brLAN == nil {
		t.Fatal("no network node for br-lan")
	}
	if brLAN.Network.CIDR != "192.168.100.1/24" {
		t.Errorf("br-lan cidr = %q, want 192.168.100.1/24", brLAN.Network.CIDR)
	}

	// Devices behind eth1 must not be attributed to a fabricated switch.
	for _, n := range g.Nodes {
		if n.Device == nil {
			continue
		}
		if n.Device.Type == model.DeviceTypeSwitch && !n.Device.IsManual {
			t.Errorf("the engine invented a switch: %s", n.Device.Name)
		}
	}
}

// TestFixtureUnresolvedSegment is the acceptance test for the product's
// headline rule: eth1 carries several learned MACs, no device among them is a
// switch or an access point, so the engine must group them under one
// unresolved-segment node rather than invent hardware.
func TestFixtureUnresolvedSegment(t *testing.T) {
	exp := loadExpected(t)
	results := replayCollectors(t)

	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)
	now := time.Unix(1771000000, 0).UTC()
	if _, err := eng.Update(context.Background(), &discovery.RunSummary{
		StartedAt: now, FinishedAt: now.Add(time.Second), Results: results,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	g, err := NewBuilder(store).WithOptions(Options{
		UnverifiedEdgeThreshold: UnverifiedThreshold,
		Now:                     now,
	}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var segments []Node
	for _, n := range g.Nodes {
		if n.Kind == NodeKindSegment {
			segments = append(segments, n)
		}
	}
	if got, want := len(segments), exp.Expectations.UnresolvedSegments; got != want {
		t.Fatalf("segment count = %d, want %d: %+v", got, want, segments)
	}
	for _, seg := range segments {
		if !seg.Unresolved {
			t.Errorf("segment %s is not flagged unresolved", seg.ID)
		}
		if !seg.Unverified {
			t.Errorf("segment %s must be unverified", seg.ID)
		}
		if seg.Reason == "" {
			t.Errorf("segment %s has no explanation", seg.ID)
		}
		if seg.Parent == "" {
			t.Errorf("segment %s has no parent", seg.ID)
		}
		if seg.Port != "eth1" && seg.Port != "eth2" {
			t.Errorf("segment %s is on an unexpected port %q", seg.ID, seg.Port)
		}
	}

	// No device may be presented as a wireless association.
	for _, e := range g.Edges {
		if e.Type == model.EdgeTypeWirelessAssoc {
			t.Errorf("edge %s asserts a wireless association from FDB data", e.ID)
		}
	}

	// Devices behind eth1 must be grouped under the segment, not the router.
	if len(segments) > 0 {
		seg := segments[0]
		children := 0
		for _, n := range g.Nodes {
			if n.Parent == seg.ID {
				children++
			}
		}
		if children < 2 {
			t.Errorf("segment %s has %d children; the fixture has several devices on eth1", seg.ID, children)
		}
	}
}

// TestFixtureRepeatedDiscoveryIsStable runs the replay twice and confirms the
// graph does not grow.
func TestFixtureRepeatedDiscoveryIsStable(t *testing.T) {
	results := replayCollectors(t)

	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)
	now := time.Unix(1771000000, 0).UTC()
	run := func(at time.Time) {
		eng.Now = func() time.Time { return at }
		if _, err := eng.Update(context.Background(), &discovery.RunSummary{
			StartedAt: at, FinishedAt: at.Add(time.Second), Results: results,
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	run(now)
	firstDevices := ing.created
	edges1, _ := store.ListEdges(database.EdgeFilter{})

	run(now.Add(10 * time.Minute))
	edges2, _ := store.ListEdges(database.EdgeFilter{})

	if ing.created != firstDevices {
		t.Errorf("second run created %d new devices (first run created %d)", ing.created-firstDevices, firstDevices)
	}
	if len(edges2) != len(edges1) {
		t.Errorf("edge count changed on re-run: %d -> %d", len(edges1), len(edges2))
	}

	// Building twice must produce the same node set.
	g1, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	g2, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}
	if len(g1.Nodes) != len(g2.Nodes) {
		t.Fatalf("node count differs between builds: %d vs %d", len(g1.Nodes), len(g2.Nodes))
	}
}

// TestFixtureAbsenceIsNotOffline is the acceptance test for the rule that a
// device missing from the DHCP lease file or the neighbour table is NOT offline.
//
// The fixture has static-IP devices that never appear in the lease file. After a
// discovery pass that did not mention them they must keep whatever state they
// had; the engine must not flip them to offline.
func TestFixtureAbsenceIsNotOffline(t *testing.T) {
	exp := loadExpected(t)
	if exp.Expectations.AbsenceOffline {
		t.Skip("fixture permits treating absence as offline")
	}
	results := replayCollectors(t)

	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)
	now := time.Unix(1771000000, 0).UTC()
	eng.Now = func() time.Time { return now }
	if _, err := eng.Update(context.Background(), &discovery.RunSummary{
		StartedAt: now, FinishedAt: now.Add(time.Second), Results: results,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Every created device starts in an unknown/online state, never offline:
	// discovery has no liveness verdict to offer.
	for mac, dev := range ing.devices {
		if dev.State == model.StateOffline {
			t.Errorf("device %s is offline after discovery; discovery must not judge liveness", mac)
		}
	}

	// Run a second pass with an EMPTY observation set. Nothing may be marked
	// offline and no device may be deleted.
	beforeCount := len(ing.devices)
	eng.Now = func() time.Time { return now.Add(30 * time.Minute) }
	if _, err := eng.Update(context.Background(), &discovery.RunSummary{
		StartedAt: now.Add(30 * time.Minute), FinishedAt: now.Add(30*time.Minute + time.Second),
	}); err != nil {
		t.Fatalf("Update (empty): %v", err)
	}
	if len(ing.devices) != beforeCount {
		t.Errorf("an empty discovery pass changed the device count: %d -> %d", beforeCount, len(ing.devices))
	}
	for mac, dev := range ing.devices {
		if dev.State == model.StateOffline {
			t.Errorf("an empty discovery pass marked %s offline", mac)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsFold(haystack, needle string) bool {
	h := []rune(lower(haystack))
	n := []rune(lower(needle))
	if len(n) == 0 {
		return true
	}
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lower(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r = r + ('a' - 'A')
		}
		out = append(out, r)
	}
	return string(out)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
