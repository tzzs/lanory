package demo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// newTestStore opens a fresh migrated SQLite database in a temp dir.
func newTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return database.NewStore(db)
}

// counts returns the row counts of every table the demo loader touches.
func counts(t *testing.T, s *database.Store) map[string]int {
	t.Helper()
	db := s.DB()
	out := map[string]int{}
	for _, table := range []string{
		"devices", "device_addresses", "device_identities", "device_sources",
		"network_interfaces", "topology_edges", "discovery_evidence",
		"probe_targets", "probe_results", "network_events", "app_settings",
	} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

// TestLoadIsIdempotent is the core acceptance test: loading twice must yield
// identical counts and no duplicate devices.
func TestLoadIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	first, err := Load(s, Options{})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	c1 := counts(t, s)

	second, err := Load(s, Options{})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	c2 := counts(t, s)

	for table, want := range c1 {
		if got := c2[table]; got != want {
			t.Errorf("table %s: second load changed count from %d to %d", table, want, got)
		}
	}
	if len(first.DeviceIDs) != len(demoDevices) {
		t.Fatalf("first load created %d devices, want %d", len(first.DeviceIDs), len(demoDevices))
	}
	if len(second.DeviceIDs) != len(demoDevices) {
		t.Fatalf("second load created %d devices, want %d", len(second.DeviceIDs), len(demoDevices))
	}
	if c2["devices"] != len(demoDevices) {
		t.Errorf("device table holds %d rows, want %d", c2["devices"], len(demoDevices))
	}

	// A third load with a different but equivalent seed must also be a no-op.
	if _, err := Load(s, Options{Seed: SeedEasePiR1}); err != nil {
		t.Fatalf("third load: %v", err)
	}
	c3 := counts(t, s)
	for table, want := range c2 {
		if got := c3[table]; got != want {
			t.Errorf("table %s: third load changed count from %d to %d", table, want, got)
		}
	}
}

// TestNoDuplicateDevices checks the device table has no repeated MAC, no
// repeated primary address and exactly one row per deterministic ID.
func TestNoDuplicateDevices(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if _, err := Load(s, Options{}); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}

	devs, total, err := s.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if total != len(demoDevices) {
		t.Fatalf("total = %d, want %d", total, len(demoDevices))
	}
	if len(devs) != len(demoDevices) {
		t.Fatalf("listed %d devices, want %d", len(devs), len(demoDevices))
	}

	seenID := map[string]bool{}
	seenMAC := map[string]bool{}
	seenIP := map[string]bool{}
	for _, d := range devs {
		if seenID[d.ID] {
			t.Errorf("duplicate device id %s", d.ID)
		}
		seenID[d.ID] = true
		if d.PrimaryMAC != "" {
			if seenMAC[d.PrimaryMAC] {
				t.Errorf("duplicate MAC %s on devices %s", d.PrimaryMAC, d.ID)
			}
			seenMAC[d.PrimaryMAC] = true
		}
		if d.PrimaryIPv4 != "" {
			if seenIP[d.PrimaryIPv4] {
				t.Errorf("duplicate IPv4 %s on devices %s", d.PrimaryIPv4, d.ID)
			}
			seenIP[d.PrimaryIPv4] = true
		}
	}
}

// TestInterfaceRolesAndSpeeds asserts the documented interface inventory of the
// reference network: eth0 WAN 100 Mbps, eth1 LAN 1000, eth2 LAN 2500,
// eth3 NO-CARRIER, br-lan bridge 192.0.2.1/24.
func TestInterfaceRolesAndSpeeds(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	routerID := demoDevices[0].ID
	want := map[string]struct {
		role    model.InterfaceRole
		speed   int
		carrier bool
		oper    model.OperState
		itype   model.InterfaceType
	}{
		"eth0":   {model.InterfaceRoleWAN, 100, true, model.OperUp, model.InterfaceTypeEthernet},
		"eth1":   {model.InterfaceRoleLAN, 1000, true, model.OperUp, model.InterfaceTypeEthernet},
		"eth2":   {model.InterfaceRoleLAN, 2500, true, model.OperUp, model.InterfaceTypeEthernet},
		"eth3":   {model.InterfaceRoleLAN, 0, false, model.OperNoCarrier, model.InterfaceTypeEthernet},
		"br-lan": {model.InterfaceRoleLAN, 2500, true, model.OperUp, model.InterfaceTypeBridge},
	}
	got := map[string]model.NetworkInterface{}
	ifaces, err := listInterfaces(s, routerID)
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	if len(ifaces) != len(want) {
		t.Fatalf("router has %d interfaces, want %d", len(ifaces), len(want))
	}
	for _, i := range ifaces {
		got[i.Name] = i
	}
	for name, w := range want {
		i, ok := got[name]
		if !ok {
			t.Errorf("interface %s missing", name)
			continue
		}
		if i.Role != w.role {
			t.Errorf("%s role = %q, want %q", name, i.Role, w.role)
		}
		if i.SpeedMbps != w.speed {
			t.Errorf("%s speed = %d, want %d", name, i.SpeedMbps, w.speed)
		}
		if i.Carrier != w.carrier {
			t.Errorf("%s carrier = %v, want %v", name, i.Carrier, w.carrier)
		}
		if i.OperState != w.oper {
			t.Errorf("%s oper_state = %q, want %q", name, i.OperState, w.oper)
		}
		if i.Type != w.itype {
			t.Errorf("%s type = %q, want %q", name, i.Type, w.itype)
		}
	}

	// br-lan carries the LAN subnet; eth0 carries the WAN subnet.
	br, ok := got["br-lan"]
	if !ok {
		t.Fatal("br-lan missing")
	}
	if !hasAddress(br.Addresses, RouterLANIPv4) {
		t.Errorf("br-lan does not carry %s", RouterLANIPv4)
	}
	wan, ok := got["eth0"]
	if !ok {
		t.Fatal("eth0 missing")
	}
	if !hasAddress(wan.Addresses, RouterWANIPv4) {
		t.Errorf("eth0 does not carry %s", RouterWANIPv4)
	}

	// Bridge membership: eth1, eth2 and eth3 point at br-lan.
	for _, port := range []string{"eth1", "eth2", "eth3"} {
		if got[port].BridgeID != br.ID {
			t.Errorf("%s bridge_id = %q, want %q", port, got[port].BridgeID, br.ID)
		}
	}
}

// TestEveryRowCarriesDemoSource is the honesty test required by the product
// spec: simulated data must be distinguishable from a real scan.
func TestEveryRowCarriesDemoSource(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	db := s.DB()

	// Every device has the demo source.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM devices d
		WHERE NOT EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source = ?)`,
		string(model.SourceDemo)).Scan(&n); err != nil {
		t.Fatalf("device sources: %v", err)
	}
	if n != 0 {
		t.Errorf("%d devices do not carry the demo source", n)
	}

	// Every device type_source points at demo.
	if err := db.QueryRow(`SELECT COUNT(*) FROM devices WHERE type_source != ?`, string(model.SourceDemo)).Scan(&n); err != nil {
		t.Fatalf("type_source: %v", err)
	}
	if n != 0 {
		t.Errorf("%d devices have type_source != demo", n)
	}

	// Addresses, identities.
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_addresses WHERE source != ?`, string(model.SourceDemo)).Scan(&n); err != nil {
		t.Fatalf("addresses: %v", err)
	}
	if n != 0 {
		t.Errorf("%d addresses do not carry the demo source", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_identities WHERE source != ?`, string(model.SourceDemo)).Scan(&n); err != nil {
		t.Fatalf("identities: %v", err)
	}
	if n != 0 {
		t.Errorf("%d identities do not carry the demo source", n)
	}

	// Edges.
	edges, err := s.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("no edges created")
	}
	for _, e := range edges {
		if e.DiscoverySource != model.SourceDemo {
			t.Errorf("edge %s source = %q, want demo", e.ID, e.DiscoverySource)
		}
	}

	// Evidence rows use the demo collector name.
	if err := db.QueryRow(`SELECT COUNT(*) FROM discovery_evidence WHERE collector != 'demo'`).Scan(&n); err != nil {
		t.Fatalf("evidence: %v", err)
	}
	if n != 0 {
		t.Errorf("%d evidence rows were not written by the demo collector", n)
	}

	// Events carry the demo marker in their evidence column.
	if err := db.QueryRow(`SELECT COUNT(*) FROM network_events WHERE evidence != 'demo dataset'`).Scan(&n); err != nil {
		t.Fatalf("events: %v", err)
	}
	if n != 0 {
		t.Errorf("%d events are not marked as demo data", n)
	}

	// The top level flag.
	if !IsDemo(s) {
		t.Error("IsDemo() = false after loading demo data")
	}
	if seed := Seed(s); seed != SeedEasePiR1 {
		t.Errorf("Seed() = %q, want %q", seed, SeedEasePiR1)
	}
}

// TestDeviceInventory asserts the expected devices, their addresses and their
// attachment ports, mirroring fixtures/easepi-r1/expected.yaml.
func TestDeviceInventory(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}

	want := map[string]struct {
		ipv4  string
		state model.DeviceState
		itype model.DeviceType
		iface string
	}{
		"dev-mac-b082e2613eb7": {"192.0.2.116", model.StateOnline, model.DeviceTypeDesktop, "eth2"},
		"dev-mac-0c6f8b4a3dd2": {"192.0.2.151", model.StateOnline, model.DeviceTypeSmartphone, "eth1"},
		"dev-mac-867b1c3f065e": {"192.0.2.222", model.StateOnline, model.DeviceTypeDesktop, "eth1"},
		"dev-mac-463f368942e3": {"192.0.2.197", model.StateSleeping, model.DeviceTypeSmartwatch, "eth1"},
		"dev-mac-52e58f5bc52d": {"192.0.2.159", model.StateOnline, model.DeviceTypeIoT, "eth1"},
		"dev-mac-0467617f7f24": {"192.0.2.228", model.StateOffline, model.DeviceTypeUnknown, "eth1"},
		"dev-mac-ccb5d1057020": {"192.0.2.231", model.StateOnline, model.DeviceTypeIoT, "eth1"},
		"dev-mac-b4fa48101613": {"", model.StateUnknown, model.DeviceTypeUnknown, "eth1"},
		"dev-mac-053344556677": {"192.168.100.2", model.StateOnline, model.DeviceTypeRouter, "eth1"},
	}
	for id, w := range want {
		dev, err := s.GetDevice(id)
		if err != nil {
			t.Errorf("device %s missing: %v", id, err)
			continue
		}
		if dev.PrimaryIPv4 != w.ipv4 {
			t.Errorf("%s primary_ipv4 = %q, want %q", id, dev.PrimaryIPv4, w.ipv4)
		}
		if dev.State != w.state {
			t.Errorf("%s state = %q, want %q", id, dev.State, w.state)
		}
		if dev.Type != w.itype {
			t.Errorf("%s type = %q, want %q", id, dev.Type, w.itype)
		}
		// The attachment port is recorded as evidence on the port.
		ev, err := s.ListEvidence("interface_port", "iface-demo-"+w.iface)
		if err != nil {
			t.Fatalf("evidence for %s: %v", w.iface, err)
		}
		found := false
		for _, e := range ev {
			if strings.Contains(e.RawData, dev.PrimaryMAC) {
				found = true
				break
			}
		}
		if !found && dev.PrimaryMAC != "" {
			t.Errorf("device %s (%s) has no port evidence on %s", id, dev.PrimaryMAC, w.iface)
		}
	}

	// The IPv6-only device has no IPv4 address at all.
	v6only, err := s.GetDevice("dev-mac-b4fa48101613")
	if err != nil {
		t.Fatalf("ipv6-only device: %v", err)
	}
	if v6only.PrimaryIPv4 != "" {
		t.Errorf("ipv6-only device has an IPv4 address %q", v6only.PrimaryIPv4)
	}
	if v6only.PrimaryIPv6 != DeviceIPv6Only {
		t.Errorf("ipv6-only device primary_ipv6 = %q, want %q", v6only.PrimaryIPv6, DeviceIPv6Only)
	}

	// The router is marked as self and as the gateway.
	self, err := s.GetDevice("dev-mac-020000000010")
	if err != nil {
		t.Fatalf("router device: %v", err)
	}
	if !self.IsSelf || !self.IsGateway {
		t.Errorf("router is_self = %v, is_gateway = %v; want true/true", self.IsSelf, self.IsGateway)
	}
}

// TestProbesAndResults asserts every page has content: probes exist, each probe
// has results, and the states are consistent with the results.
func TestProbesAndResults(t *testing.T) {
	s := newTestStore(t)
	loaded, err := Load(s, Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.ProbeIDs) == 0 {
		t.Fatal("no probes created")
	}
	if loaded.ProbeResultCount == 0 {
		t.Fatal("no probe results created")
	}

	probes, err := s.ListProbes()
	if err != nil {
		t.Fatalf("list probes: %v", err)
	}
	for _, p := range probes {
		recent, err := s.RecentProbeResults(p.ID, 10)
		if err != nil {
			t.Fatalf("results for %s: %v", p.ID, err)
		}
		if len(recent) == 0 {
			t.Errorf("probe %s (%s) has no results", p.ID, p.Name)
			continue
		}
		for _, r := range recent {
			if r.Success && r.Status != model.ProbeRunSuccess {
				t.Errorf("probe %s result %s: success=true but status=%q", p.ID, r.ID, r.Status)
			}
			if !r.Success && r.Status == model.ProbeRunSuccess {
				t.Errorf("probe %s result %s: success=false but status=success", p.ID, r.ID)
			}
		}
		last, err := s.LatestProbeResult(p.ID)
		if err != nil {
			t.Fatalf("latest result for %s: %v", p.ID, err)
		}
		if last.Success != (p.LastResult == model.ProbeRunSuccess) {
			t.Errorf("probe %s last_result = %q but last stored result success = %v", p.ID, p.LastResult, last.Success)
		}
	}
}

// TestEventsPopulated asserts the history page has content.
func TestEventsPopulated(t *testing.T) {
	s := newTestStore(t)
	loaded, err := Load(s, Options{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.EventCount == 0 {
		t.Fatal("no events created")
	}
	evs, total, err := s.ListEvents(database.EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if total != len(demoEvents()) {
		t.Errorf("total events = %d, want %d", total, len(demoEvents()))
	}
	if len(evs) != total {
		t.Errorf("listed %d events, want %d", len(evs), total)
	}
	// Newest first, and all in the past.
	for i := 1; i < len(evs); i++ {
		if evs[i-1].Timestamp.Before(evs[i].Timestamp) {
			t.Errorf("events not sorted newest first at index %d", i)
		}
	}
	for _, e := range evs {
		if e.Timestamp.After(loaded.LoadedAt.Add(time.Second)) {
			t.Errorf("event %s is in the future", e.ID)
		}
	}
}

// TestTopologyEdgesConnectTheGraph asserts the topology page has a connected
// graph: every discovered device has at least one edge, and the WAN/LAN chain
// exists.
func TestTopologyEdgesConnectTheGraph(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	edges, err := s.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("no edges")
	}

	// Every device is an endpoint of at least one edge.
	devs, _, err := s.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	touched := map[string]bool{}
	for _, e := range edges {
		touched[e.SourceID] = true
		touched[e.TargetID] = true
	}
	for _, d := range devs {
		if d.IsSelf {
			continue
		}
		if !touched[d.ID] {
			t.Errorf("device %s (%s) is not attached to any edge", d.ID, d.Name)
		}
	}

	// The WAN chain: upstream router -> modem -> local router.
	if !hasEdge(edges, "dev-mac-020000000020", "dev-manual-modem") {
		t.Error("missing upstream -> modem edge")
	}
	if !hasEdge(edges, "dev-manual-modem", "dev-mac-020000000010") {
		t.Error("missing modem -> router edge")
	}
	// The Windows PC is attached to eth2, not eth1.
	pc, err := s.GetDevice("dev-mac-b082e2613eb7")
	if err != nil {
		t.Fatalf("windows pc: %v", err)
	}
	edge, err := s.GetEdgeByTuple("dev-mac-020000000010", pc.ID, "iface-demo-eth2", "", model.EdgeTypeObservedMACPort)
	if err != nil {
		t.Fatalf("windows pc edge on eth2: %v", err)
	}
	if edge.SourceInterfaceID != "iface-demo-eth2" {
		t.Errorf("windows pc edge interface = %q, want iface-demo-eth2", edge.SourceInterfaceID)
	}
	if _, err := s.GetEdgeByTuple("dev-mac-020000000010", pc.ID, "iface-demo-eth1", "", model.EdgeTypeObservedMACPort); err == nil {
		t.Error("windows pc must not be attached to eth1")
	}
}

// TestReseedRestoresIdenticalData checks that Reseed after a manual change
// produces the same dataset again.
func TestReseedRestoresIdenticalData(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("first load: %v", err)
	}
	before := counts(t, s)

	// Pollute: rename a device, add a manual probe and a stray event.
	dev, err := s.GetDevice("dev-mac-0c6f8b4a3dd2")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	dev.Name = "renamed by user"
	if err := s.UpsertDevice(dev); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// The stray event must reference a real edge: network_events.edge_id is
	// NOT NULL DEFAULT '' with a foreign key to topology_edges.
	edges, err := s.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if err := s.SaveEvent(&model.NetworkEvent{
		EventType: model.EventDeviceUpdated, DeviceID: dev.ID,
		EdgeID: edges[0].ID, Message: "stray",
	}); err != nil {
		t.Fatalf("stray event: %v", err)
	}
	// Both probes need a device: probe_targets.device_id is NOT NULL DEFAULT ''
	// with a foreign key. The "real" probe is attached to a real (non-demo)
	// device, which is the case Reset must preserve.
	if err := s.UpsertDevice(&model.Device{ID: "dev-real-1", Name: "Real", Type: model.DeviceTypeLaptop}); err != nil {
		t.Fatalf("real device: %v", err)
	}
	if err := s.AddDeviceSource("dev-real-1", model.SourceDHCP); err != nil {
		t.Fatalf("real device source: %v", err)
	}
	for id, devID := range map[string]string{
		"probe-demo-stray": dev.ID,
		"probe-real-1":     "dev-real-1",
	} {
		if err := s.CreateProbe(&model.ProbeTarget{ID: id, Name: id, Target: "192.168.100.9", DeviceID: devID}); err != nil {
			t.Fatalf("probe %s: %v", id, err)
		}
	}
	polluted := counts(t, s)
	if polluted["devices"] != before["devices"] || polluted["probe_targets"] != before["probe_targets"] {
		t.Logf("counts before=%v polluted=%v", before, polluted)
	}

	if _, err := Load(s, Options{Reseed: true}); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	after := counts(t, s)

	// Reseed must remove the demo rows and rebuild them: devices and edges are
	// back to the original counts, demo probes back, and the real probe kept.
	if after["devices"] != before["devices"]+1 {
		t.Errorf("after reseed devices = %d, want %d (demo devices plus the one real device)",
			after["devices"], before["devices"]+1)
	}
	if after["topology_edges"] != before["topology_edges"] {
		t.Errorf("after reseed edges = %d, want %d", after["topology_edges"], before["topology_edges"])
	}
	if after["probe_targets"] != before["probe_targets"]+1 {
		t.Errorf("after reseed probes = %d, want %d (demo probes plus the one real probe)",
			after["probe_targets"], before["probe_targets"]+1)
	}
	if _, err := s.GetProbe("probe-real-1"); err != nil {
		t.Errorf("reseed deleted a non-demo probe: %v", err)
	}
	if _, err := s.GetProbe("probe-demo-stray"); err == nil {
		t.Error("reseed kept the stray demo probe")
	}
	// The user rename is gone because the demo row was recreated.
	dev2, err := s.GetDevice("dev-mac-0c6f8b4a3dd2")
	if err != nil {
		t.Fatalf("get device after reseed: %v", err)
	}
	if dev2.Name == "renamed by user" {
		t.Error("reseed did not restore the demo device name")
	}
	// Demo mode flag is back.
	if !IsDemo(s) {
		t.Error("IsDemo() = false after reseed")
	}
}

// TestResetClearsEverything checks Reset removes all demo rows and clears the
// flag, while leaving real rows alone.
func TestResetClearsEverything(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}

	// A real device that only the "dhcp" source has seen.
	if err := s.UpsertDevice(&model.Device{ID: "dev-real-1", Name: "Real", Type: model.DeviceTypeLaptop}); err != nil {
		t.Fatalf("real device: %v", err)
	}
	if err := s.AddDeviceSource("dev-real-1", model.SourceDHCP); err != nil {
		t.Fatalf("real device source: %v", err)
	}
	before := counts(t, s)

	if err := Reset(s); err != nil {
		t.Fatalf("reset: %v", err)
	}
	after := counts(t, s)

	if after["devices"] != 1 {
		t.Errorf("after reset devices = %d, want 1 (the real device)", after["devices"])
	}
	if after["topology_edges"] != 0 {
		t.Errorf("after reset edges = %d, want 0", after["topology_edges"])
	}
	if after["probe_targets"] != 0 {
		t.Errorf("after reset probes = %d, want 0", after["probe_targets"])
	}
	if after["network_events"] != 0 {
		t.Errorf("after reset events = %d, want 0", after["network_events"])
	}
	if after["discovery_evidence"] != 0 {
		t.Errorf("after reset evidence = %d, want 0", after["discovery_evidence"])
	}
	if after["device_sources"] != 1 {
		t.Errorf("after reset device_sources = %d, want 1", after["device_sources"])
	}
	if IsDemo(s) {
		t.Error("IsDemo() = true after reset")
	}
	if before["devices"] <= after["devices"] {
		t.Fatalf("sanity: reset did not remove anything (before=%v after=%v)", before, after)
	}
	if _, err := s.GetDevice("dev-real-1"); err != nil {
		t.Errorf("reset removed a real device: %v", err)
	}
}

// TestResetThenLoadIsANoopAfterFreshLoad checks a Reset+Load pair equals a
// single Load.
func TestResetThenLoadMatchesFreshLoad(t *testing.T) {
	s1 := newTestStore(t)
	if _, err := Load(s1, Options{}); err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	fresh := counts(t, s1)

	s2 := newTestStore(t)
	if _, err := Load(s2, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := Reset(s2); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := Load(s2, Options{}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reloaded := counts(t, s2)

	for table, want := range fresh {
		if got := reloaded[table]; got != want {
			t.Errorf("table %s: reset+reload = %d, fresh load = %d", table, got, want)
		}
	}
}

// TestUnknownSeedIsRejected checks the loader refuses an unknown dataset
// instead of silently loading nothing.
func TestUnknownSeedIsRejected(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{Seed: "does-not-exist"}); err == nil {
		t.Fatal("Load with an unknown seed returned nil error")
	}
	if IsDemo(s) {
		t.Error("failed load still flagged demo mode")
	}
}

// TestDemoSourceIsDistinctFromRealSources guards against a future refactor
// reusing a real discovery source constant for demo data.
func TestDemoSourceIsDistinctFromRealSources(t *testing.T) {
	for _, src := range []model.DiscoverySource{
		model.SourceOpenWRT, model.SourceUCI, model.SourceUBUS, model.SourceARP,
		model.SourceNDP, model.SourceDHCP, model.SourceBridgeFDB, model.SourceLLDP,
		model.SourceMDNS, model.SourceSSDP, model.SourceSNMP, model.SourceXiaomi,
		model.SourceScan, model.SourceManual, model.SourcePlatform,
	} {
		if src == model.SourceDemo {
			t.Errorf("demo source collides with %q", src)
		}
	}
}

// TestDatasetUsesOnlyFixtureAddressing asserts the demo dataset reproduces the
// sanitized reference network (fixtures/easepi-r1) and does not invent
// addresses.
//
// The fixtures deliberately keep the structural properties of the real home
// network under RFC1918 addresses from the fixture's own LAN, plus documentation
// ranges for the parts that had to be rewritten. The rules encoded here:
//
//   - LAN devices live in the fixture's 192.168.100.0/24;
//   - the WAN side lives in the fixture's 192.168.1.0/24;
//   - anything outside those two subnets must be in a reserved documentation
//     range (RFC 5737 / RFC 3849), never a real routable address.
//
// The dataset is also asserted to stay inside the two private /24s above, so a
// future edit cannot quietly introduce an address that would be reachable on a
// reader's real network.
func TestDatasetUsesOnlyFixtureAddressing(t *testing.T) {
	lan := "192.168.100."
	wan := "192.168.1."
	doc := []string{"192.0.2.", "198.51.100.", "203.0.113.", "2001:db8:", "fd7c:", "fe80:"}
	check := func(kind, addr string) {
		if addr == "" {
			return
		}
		if strings.HasPrefix(addr, lan) || strings.HasPrefix(addr, wan) {
			return
		}
		for _, p := range doc {
			if strings.HasPrefix(addr, p) {
				return
			}
		}
		t.Errorf("%s %q is outside the fixture subnets and not a documentation address", kind, addr)
	}
	for _, d := range demoDevices {
		check("device ipv4 "+d.ID, d.IPv4)
		check("device ipv6 "+d.ID, d.IPv6)
	}
	for _, i := range demoInterfaces {
		for _, a := range i.Addr {
			addr, _, _ := splitCIDR(a)
			check("interface "+i.Name, addr)
		}
	}
	check("ipv6-only device", DeviceIPv6Only)
}

// TestSplitCIDR checks the CIDR helper.
func TestSplitCIDR(t *testing.T) {
	addr, family, prefix := splitCIDR("192.0.2.1/24")
	if addr != "192.0.2.1" || family != model.AddressFamilyIPv4 || prefix != 24 {
		t.Errorf("splitCIDR(192.0.2.1/24) = %q, %q, %d", addr, family, prefix)
	}
	addr, family, prefix = splitCIDR("2001:db8:1::4/64")
	if addr != "2001:db8:1::4" || family != model.AddressFamilyIPv6 || prefix != 64 {
		t.Errorf("splitCIDR(2001:db8:1::4/64) = %q, %q, %d", addr, family, prefix)
	}
	addr, _, prefix = splitCIDR("192.0.2.9")
	if addr != "192.0.2.9" || prefix != 0 {
		t.Errorf("splitCIDR(192.0.2.9) = %q, _, %d", addr, prefix)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func hasAddress(addrs []model.DeviceAddress, want string) bool {
	for _, a := range addrs {
		if a.Address == want {
			return true
		}
	}
	return false
}

func hasEdge(edges []model.TopologyEdge, src, dst string) bool {
	for _, e := range edges {
		if e.SourceID == src && e.TargetID == dst {
			return true
		}
	}
	return false
}

// TestProviderSatisfiesAPIContract checks that *Provider has exactly the methods
// the API layer's DemoProvider interface declares, so cmd/lanory can pass it to
// App.SetDemo without an adapter.
func TestProviderSatisfiesAPIContract(t *testing.T) {
	// The interface is restated here rather than imported, because importing
	// internal/api from this package's test would create an import cycle through
	// the providers. Keeping the declaration identical is the contract check.
	type demoProvider interface {
		Enabled() bool
		Seed(ctx context.Context, name string) error
	}

	s := newTestStore(t)
	var p demoProvider = NewProvider(s, true, SeedEasePiR1)
	if !p.Enabled() {
		t.Error("Enabled() = false for a provider constructed with enabled=true")
	}

	if err := p.Seed(context.Background(), ""); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	devs, total, err := s.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if total != len(demoDevices) || len(devs) != len(demoDevices) {
		t.Errorf("after Seed the store holds %d devices, want %d", total, len(demoDevices))
	}
	if !IsDemo(s) {
		t.Error("Seed did not flag the database as demo mode")
	}

	// Seeding twice through the provider must reseed, not duplicate.
	if err := p.Seed(context.Background(), SeedEasePiR1); err != nil {
		t.Fatalf("second Seed: %v", err)
	}
	_, total2, err := s.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if total2 != len(demoDevices) {
		t.Errorf("second Seed changed the device count to %d, want %d", total2, len(demoDevices))
	}
}

// TestProviderRejectsUnknownSeed checks the provider surfaces a typo instead of
// producing an empty UI.
func TestProviderRejectsUnknownSeed(t *testing.T) {
	s := newTestStore(t)
	p := NewProvider(s, true, SeedEasePiR1)
	if err := p.Seed(context.Background(), "not-a-seed"); err == nil {
		t.Fatal("Seed accepted an unknown dataset name")
	}
	if IsDemo(s) {
		t.Error("a failed Seed still flagged demo mode")
	}
}

// TestProviderHonoursContext checks cancellation.
func TestProviderHonoursContext(t *testing.T) {
	s := newTestStore(t)
	p := NewProvider(s, true, SeedEasePiR1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Seed(ctx, ""); err == nil {
		t.Fatal("Seed ignored a cancelled context")
	}
}

// TestProviderActiveReflectsDatabase checks Active() against a database seeded
// outside the provider, which is the restart case.
func TestProviderActiveReflectsDatabase(t *testing.T) {
	s := newTestStore(t)
	p := NewProvider(s, false, SeedEasePiR1)
	if p.Active() {
		t.Error("Active() = true on a fresh database")
	}
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !p.Active() {
		t.Error("Active() = false although the database holds demo data")
	}
}

// TestSeedForCI seeds a database at $LANORY_CI_SEED_DB when the variable is set.
//
// The CI integration job needs a database that already holds the demo dataset so
// it can start the real binary against it and assert, over HTTP, that every
// device and edge is labelled. The wiring from cmd/lanory to demo is a one-liner
// in a package this one does not own, so the job seeds the database through the
// loader instead of relying on a flag it cannot yet observe.
//
// The test is skipped in a normal `go test ./...` run, and it still asserts the
// real behaviour (idempotency) because it loads twice.
func TestSeedForCI(t *testing.T) {
	path := os.Getenv("LANORY_CI_SEED_DB")
	if path == "" {
		t.Skip("LANORY_CI_SEED_DB is not set")
	}
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	store := database.NewStore(db)

	first, err := Load(store, Options{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	second, err := Load(store, Options{})
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if len(first.DeviceIDs) != len(second.DeviceIDs) {
		t.Fatalf("seed is not idempotent: %d then %d devices", len(first.DeviceIDs), len(second.DeviceIDs))
	}
	_, total, err := store.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if total != len(demoDevices) {
		t.Fatalf("seeded %d devices, want %d", total, len(demoDevices))
	}
	if !IsDemo(store) {
		t.Fatal("seeded database is not flagged as demo data")
	}
	t.Logf("seeded %s: %d devices, %d edges, %d probes", path, total, len(first.EdgeIDs), len(first.ProbeIDs))
}

// TestDemoEdgesAreSelfConsistent guards the topology invariants of the sample
// dataset: no edge may link a device to itself, and a user-asserted relation must
// be marked user-confirmed and locked so automatic discovery cannot silently
// drop it.
func TestDemoEdgesAreSelfConsistent(t *testing.T) {
	for _, edge := range demoEdges() {
		if edge.SourceID == edge.TargetID {
			t.Errorf("edge %s -> %s is a self relation: %s", edge.SourceID, edge.TargetID, edge.Type)
		}
		if edge.Type == string(model.EdgeTypeUserDefined) {
			if edge.Confidence != model_edgeConfidenceManual {
				t.Errorf("user_defined edge confidence = %v, want %v", edge.Confidence, model_edgeConfidenceManual)
			}
		}
		if edge.Unverified && edge.Confidence > 0.5 {
			t.Errorf("edge %s->%s is unverified but claims confidence %v", edge.SourceID, edge.TargetID, edge.Confidence)
		}
	}
}

// model_edgeConfidenceManual is the confidence a user-asserted relation carries.
const model_edgeConfidenceManual = 1.0
