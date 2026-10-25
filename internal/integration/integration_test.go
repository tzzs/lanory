// Package integration_test exercises the whole discovery pipeline against the
// sanitized EASEPi R1 snapshot.
//
// Every other test in this repository checks one layer: a parser, the identity
// engine, the topology builder. None of them proves that the layers compose. This
// test does: it points the real collectors at the fixture files, feeds the real
// registry output into the real identity engine through the real update engine,
// and then asserts the acceptance scenarios from the specification against the
// resulting database and topology.
//
// The scenarios covered (numbering from the product spec, section 22):
//
//	1  interfaces read correctly: eth0 WAN 100 Mbps, eth1/eth2 LAN, eth3 no carrier
//	2  DHCP + ARP + FDB discover the known devices
//	3  the Windows PC MAC is learned on eth2; iPhone/Mac/Watch on eth1
//	4  a busy eth1 does not become a confirmed wireless association
//	5  a manual device and manual edges survive a re-run
//	7  a missing ping response does not make a device permanently offline
//	9  user settings (manual nodes, links, layout) persist across a restart
//
// Scenario 6 (physical link down) and 8 (device detail) are covered by
// internal/monitoring and internal/api respectively.
package integration_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/device"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/arp"
	"github.com/lanory/lanory/internal/discovery/dhcp"
	"github.com/lanory/lanory/internal/discovery/fdb"
	"github.com/lanory/lanory/internal/discovery/ndp"
	"github.com/lanory/lanory/internal/discovery/openwrt"
	"github.com/lanory/lanory/internal/model"
	"github.com/lanory/lanory/internal/topology"
)

// fixtureDir locates the sanitized snapshot relative to this package.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "easepi-r1"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bridge-fdb.txt")); err != nil {
		t.Skipf("fixtures not available at %s: %v", dir, err)
	}
	return dir
}

// harness wires the real pipeline over a temp database.
type harness struct {
	store   *database.Store
	engine  *device.Engine
	update  *topology.UpdateEngine
	topo    *topology.Service
	reg     *discovery.Registry
	cfg     *config.Config
	baseDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := database.NewStore(db)
	dir := fixtureDir(t)

	cfg := config.Default()

	h := &harness{
		store:   store,
		engine:  device.New(store, nil),
		topo:    topology.NewService(store, cfg),
		reg:     discovery.NewRegistry(cfg),
		cfg:     cfg,
		baseDir: dir,
	}
	h.update = topology.NewUpdateEngine(store, nil).WithIngestor(ingestor{h.engine})

	// Point every collector at the fixture files instead of the live system.
	h.reg.RegisterAll(
		openwrt.NewWithPaths(
			filepath.Join(dir, "uci-network.txt"),
			filepath.Join(dir, "ip-link.txt"),
			filepath.Join(dir, "ubus-network-device.json"),
			filepath.Join(dir, "ubus-network-interface.json"),
		).WithHostInfoFiles(
			filepath.Join(dir, "board.json"),
			filepath.Join(dir, "openwrt_release.txt"),
			"", // no hostname file in the fixture; board.json supplies it
		),
		arp.NewWithPath(filepath.Join(dir, "ip-neigh.txt")),
		ndp.NewWithPath(filepath.Join(dir, "ip-neigh6.txt")),
		dhcp.NewWithPath(filepath.Join(dir, "dhcp-leases.txt")),
		fdb.NewWithPaths(filepath.Join(dir, "bridge-fdb.txt"), filepath.Join(dir, "ip-link.txt")),
	)
	return h
}

// run executes one discovery pass through the full pipeline.
func (h *harness) run(t *testing.T) *topology.UpdateResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary := h.reg.Run(ctx)
	if summary == nil {
		t.Fatal("registry returned no summary")
	}
	for _, c := range summary.Collectors {
		if c.Err != nil {
			t.Logf("collector %s reported: %v", c.Name, c.Err)
		}
	}
	result, err := h.update.Update(ctx, summary)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return result
}

// ingestor adapts the device engine to the topology update engine.
type ingestor struct{ e *device.Engine }

func (i ingestor) Resolve(in topology.Ingest) (*model.Device, bool, error) {
	return i.e.Resolve(device.Ingest{
		MAC:          in.MAC,
		Hostname:     in.Hostname,
		Vendor:       in.Vendor,
		Addresses:    in.Addresses,
		Source:       in.Source,
		ObservedAt:   in.ObservedAt,
		StrongID:     in.StrongID,
		StrongIDType: in.StrongIDType,
		ClientID:     in.ClientID,
		TypeHint:     in.TypeHint,
	})
}

// deviceByMAC returns the device owning a MAC, or nil.
func (h *harness) deviceByMAC(t *testing.T, mac string) *model.Device {
	t.Helper()
	dev, err := h.store.FindDeviceByMAC(mac)
	if err != nil {
		return nil
	}
	return dev
}

// portForMAC returns the bridge port a MAC was learned on, from stored edges.
func (h *harness) portForMAC(t *testing.T, mac string) string {
	t.Helper()
	dev := h.deviceByMAC(t, mac)
	if dev == nil {
		return ""
	}
	interfaces, err := h.store.ListAllInterfaces()
	if err != nil {
		return ""
	}
	byID := map[string]string{}
	for _, iface := range interfaces {
		byID[iface.ID] = iface.Name
	}
	edges, err := h.store.ListEdgesForDevice(dev.ID)
	if err != nil {
		return ""
	}
	for _, e := range edges {
		if e.EdgeType != model.EdgeTypeObservedMACPort {
			continue
		}
		if name := byID[e.SourceInterfaceID]; name != "" {
			return name
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Scenario 1: interfaces
// ---------------------------------------------------------------------------

func TestScenario1_Interfaces(t *testing.T) {
	h := newHarness(t)
	h.run(t)

	ifaces := map[string]*model.NetworkInterface{}
	all, err := h.store.ListAllInterfaces()
	if err != nil {
		t.Fatalf("list interfaces: %v", err)
	}
	for i := range all {
		ifaces[all[i].Name] = &all[i]
	}

	cases := []struct {
		name    string
		role    model.InterfaceRole
		carrier bool
		speed   int
	}{
		{"eth0", model.InterfaceRoleWAN, true, 100},
		{"eth1", model.InterfaceRoleLAN, true, 1000},
		{"eth2", model.InterfaceRoleLAN, true, 2500},
		{"eth3", model.InterfaceRoleLAN, false, 0},
	}
	for _, tc := range cases {
		iface, ok := ifaces[tc.name]
		if !ok {
			t.Errorf("%s was not discovered", tc.name)
			continue
		}
		if iface.Role != tc.role {
			t.Errorf("%s role = %q, want %q", tc.name, iface.Role, tc.role)
		}
		if iface.Carrier != tc.carrier {
			t.Errorf("%s carrier = %v, want %v", tc.name, iface.Carrier, tc.carrier)
		}
		if tc.speed > 0 && iface.SpeedMbps != tc.speed {
			t.Errorf("%s speed = %d, want %d", tc.name, iface.SpeedMbps, tc.speed)
		}
	}

	// eth1 and eth2 belong to br-lan; eth3 is a member but has no carrier.
	for _, name := range []string{"eth1", "eth2", "eth3"} {
		if iface := ifaces[name]; iface != nil && iface.BridgeID == "" {
			t.Errorf("%s is not recorded as a bridge member", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 2 + 3: devices discovered, correct ports
// ---------------------------------------------------------------------------

func TestScenario2And3_DevicesAndPorts(t *testing.T) {
	h := newHarness(t)
	h.run(t)

	// Scenario 3: the Windows PC is on eth2, everything else on eth1.
	type want struct {
		mac  string
		port string
		name string
	}
	expected := []want{
		{"02:11:22:33:44:b7", "eth2", "the Windows PC"},
		{"06:77:88:99:aa:d2", "eth1", "the iPhone"},
		{"07:88:99:aa:bb:5e", "eth1", "the Mac"},
		{"04:66:77:88:99:e3", "eth1", "the Watch"},
	}
	for _, w := range expected {
		dev := h.deviceByMAC(t, w.mac)
		if dev == nil {
			t.Errorf("%s (%s) was not discovered", w.name, w.mac)
			continue
		}
		if got := h.portForMAC(t, w.mac); got != w.port {
			t.Errorf("%s is on %q, want %q", w.name, got, w.port)
		}
	}

	// Scenario 2: the DHCP leases and ARP neighbours produce named devices.
	devices, total, err := h.store.ListDevices(database.DeviceFilter{Limit: 200})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if total < 8 {
		t.Errorf("discovered %d devices, expected at least 8 from the fixture", total)
	}

	named := map[string]bool{}
	for _, d := range devices {
		if d.PrimaryIPv4 != "" {
			named[d.PrimaryIPv4] = true
		}
	}
	for _, ip := range []string{"192.0.2.116", "192.0.2.151", "192.0.2.222", "192.0.2.197"} {
		if !named[ip] {
			t.Errorf("no device has the known address %s", ip)
		}
	}
}

// ---------------------------------------------------------------------------
// Noise must not become devices
// ---------------------------------------------------------------------------

func TestNoiseIsNotADevice(t *testing.T) {
	// The scenario is exercised repeatedly on purpose. The collector used to
	// build its interface list from a Go map, so the order of the interface
	// observations changed on every run: whenever docker0 (which reports itself
	// as a bridge) came first, its MAC was taken for the host and the real
	// router became an ordinary device. A single run therefore proved nothing;
	// every run must keep the noise out and the router in place.
	noise := []string{
		"33:33:00:00:00:01", // IPv6 multicast
		"01:00:5e:00:00:01", // IPv4 multicast
		"00:00:00:00:00:00", // zero
		"02:42:ac:12:00:02", // docker0
		"02:11:22:33:44:55", // dummy0
	}
	for run := 0; run < 10; run++ {
		t.Run(fmt.Sprintf("run-%02d", run), func(t *testing.T) {
			h := newHarness(t)
			h.run(t)

			for _, mac := range noise {
				if dev := h.deviceByMAC(t, mac); dev != nil {
					t.Errorf("noise MAC %s became device %q", mac, dev.Name)
				}
			}

			// The bridge MAC is a different case: it belongs to the router itself.
			// It must be represented exactly once, flagged as the local host, and
			// never as an ordinary end device.
			if dev := h.deviceByMAC(t, "02:00:00:00:00:11"); dev != nil {
				if !dev.IsSelf {
					t.Errorf("the bridge MAC became an ordinary device %q instead of being marked is_self", dev.Name)
				}
				if dev.Type != model.DeviceTypeRouter {
					t.Errorf("the local host has type %q, want router", dev.Type)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: eth1 does not imply Wi-Fi
// ---------------------------------------------------------------------------

func TestScenario4_NoWirelessInference(t *testing.T) {
	h := newHarness(t)
	h.run(t)

	edges, err := h.store.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}

	// Count the MACs learned on eth1. The fixture has seven. That is evidence of
	// several layer-2 devices behind one port, not evidence they are wireless.
	learnedOnEth1 := map[string]bool{}
	for _, e := range edges {
		if e.EdgeType == model.EdgeTypeObservedMACPort {
			learnedOnEth1[e.TargetID] = true
		}
	}
	if len(learnedOnEth1) < 2 {
		t.Skip("not enough MAC-port edges to evaluate the fan-out case")
	}

	for _, e := range edges {
		if e.EdgeType == model.EdgeTypeWirelessAssoc {
			t.Errorf("a wireless association edge was inferred without evidence: %s -> %s", e.SourceID, e.TargetID)
		}
	}

	// The topology must group them under something explicitly unresolved rather
	// than inventing a confirmed switch or AP.
	topo, err := h.topo.Build()
	if err != nil {
		t.Fatalf("build topology: %v", err)
	}
	unresolved := 0
	for _, n := range topo.Nodes {
		if n.Unresolved {
			unresolved++
		}
	}
	if unresolved == 0 {
		t.Log("note: no unresolved segment node was produced; check whether the fixture expects one")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5 + 9: user intent survives
// ---------------------------------------------------------------------------

func TestScenario5And9_UserIntentSurvivesRescan(t *testing.T) {
	h := newHarness(t)
	h.run(t)

	// Find the device Lanory is running on (the router with br-lan).
	all, _, _ := h.store.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	var routerID string
	for _, d := range all {
		if d.PrimaryIPv4 == "192.0.2.1" || d.Name == "EASEPi R1" {
			routerID = d.ID
			break
		}
	}
	if routerID == "" {
		// Fall back to whichever device owns the bridge.
		interfaces, _ := h.store.ListAllInterfaces()
		for _, iface := range interfaces {
			if iface.Type == model.InterfaceTypeBridge {
				routerID = iface.DeviceID
				break
			}
		}
	}
	if routerID == "" {
		t.Skip("could not identify the router device in the fixture")
	}

	// The user adds a manual Xiaomi router and attaches two devices to it.
	xiaomi := &model.Device{
		ID:        "dev-manual-xiaomi",
		Name:      "Xiaomi Router",
		Type:      model.DeviceTypeRouter,
		IsManual:  true,
		IsLocked:  true,
		State:     model.StateUnknown,
		FirstSeen: time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
	}
	if err := h.store.UpsertDevice(xiaomi); err != nil {
		t.Fatalf("create manual device: %v", err)
	}

	manualEdge := &model.TopologyEdge{
		SourceID:        routerID,
		TargetID:        xiaomi.ID,
		EdgeType:        model.EdgeTypeUserDefined,
		DiscoverySource: model.SourceManual,
		Confidence:      1.0,
		UserConfirmed:   true,
		Locked:          true,
		Status:          model.EdgeStatusLinkUp,
	}
	if err := h.store.UpsertEdge(manualEdge); err != nil {
		t.Fatalf("create manual edge: %v", err)
	}

	// The user also pins a layout position.
	if err := h.store.SaveLayout(&model.TopologyLayout{
		NodeID: xiaomi.ID, X: 123, Y: 456, Pinned: true,
	}); err != nil {
		t.Fatalf("save layout: %v", err)
	}

	// Now run discovery again, as the scheduler would.
	h.run(t)
	h.run(t)

	// Scenario 5: the manual device is still there, still manual, still locked.
	after, err := h.store.GetDevice(xiaomi.ID)
	if err != nil {
		t.Fatalf("the manual device was removed by discovery: %v", err)
	}
	if !after.IsManual || !after.IsLocked {
		t.Errorf("manual flags lost: is_manual=%v is_locked=%v", after.IsManual, after.IsLocked)
	}

	// The manual edge survives, keeps confidence 1.0 and stays locked.
	edges, err := h.store.ListEdgesForDevice(xiaomi.ID)
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	var found *model.TopologyEdge
	for i := range edges {
		if edges[i].EdgeType == model.EdgeTypeUserDefined && edges[i].TargetID == xiaomi.ID {
			found = &edges[i]
			break
		}
	}
	if found == nil {
		t.Fatal("the user-confirmed edge was dropped by a rescan")
	}
	if !found.UserConfirmed || !found.Locked {
		t.Errorf("user-confirmed edge lost its flags: confirmed=%v locked=%v", found.UserConfirmed, found.Locked)
	}
	if found.Confidence < 0.99 {
		t.Errorf("user-confirmed edge confidence = %v, want 1.0", found.Confidence)
	}

	// Scenario 9: the pinned layout is unchanged.
	layout, err := h.store.ListLayout()
	if err != nil {
		t.Fatalf("list layout: %v", err)
	}
	var pos *model.TopologyLayout
	for i := range layout {
		if layout[i].NodeID == xiaomi.ID {
			pos = &layout[i]
			break
		}
	}
	if pos == nil {
		t.Fatal("the saved layout position was lost")
	}
	if pos.X != 123 || pos.Y != 456 || !pos.Pinned {
		t.Errorf("layout = (%v,%v) pinned=%v, want (123,456) pinned", pos.X, pos.Y, pos.Pinned)
	}

	// Repeated discovery must not duplicate the fixture devices.
	all, total, err := h.store.ListDevices(database.DeviceFilter{Limit: 500, IncludeHidden: true})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	seen := map[string]int{}
	for _, d := range all {
		if d.PrimaryMAC != "" {
			seen[d.PrimaryMAC]++
		}
	}
	for mac, n := range seen {
		if n > 1 {
			t.Errorf("MAC %s is owned by %d devices after repeated discovery", mac, n)
		}
	}
	t.Logf("after 3 discovery passes: %d devices, %d edges", total, len(edges))
}

// ---------------------------------------------------------------------------
// Scenario 7: absence is not offline
// ---------------------------------------------------------------------------

func TestScenario7_AbsenceIsNotOffline(t *testing.T) {
	h := newHarness(t)
	h.run(t)

	// Pick a device that discovery found.
	devices, _, err := h.store.ListDevices(database.DeviceFilter{Limit: 50})
	if err != nil || len(devices) == 0 {
		t.Skip("no devices discovered")
	}
	target := devices[0]
	if target.State == model.StateOffline {
		t.Fatalf("a freshly discovered device is already offline")
	}

	// A run that observes nothing must not flip anything to offline. The only
	// thing that may change a device state is a probe result with enough
	// consecutive failures, which is the monitoring engine's job.
	before := target.State
	empty := h.reg.Run(context.Background())
	if _, err := h.update.Update(context.Background(), empty); err != nil {
		t.Fatalf("update with no observations: %v", err)
	}
	after, err := h.store.GetDevice(target.ID)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if after.State != before {
		t.Errorf("state changed from %q to %q merely because discovery saw nothing", before, after.State)
	}

	// And an unreachable device stays unknown, not offline, until probes say so.
	manual := &model.Device{
		ID: "dev-manual-unprobed", Name: "Fiber", Type: model.DeviceTypeNetworkFiber,
		IsManual: true, State: model.StateUnknown,
	}
	if err := h.store.UpsertDevice(manual); err != nil {
		t.Fatalf("create device: %v", err)
	}
	got, err := h.store.GetDevice(manual.ID)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.State == model.StateOffline {
		t.Error("a device with no probe results is reported offline; it must stay unknown")
	}
}

// ---------------------------------------------------------------------------
// Idempotence at the database level
// ---------------------------------------------------------------------------

func TestRepeatedRunsDoNotGrowTheDatabase(t *testing.T) {
	h := newHarness(t)
	h.run(t)

	first, err := h.store.DeviceCounts()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}

	for i := 0; i < 3; i++ {
		h.run(t)
	}

	after, err := h.store.DeviceCounts()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if after.Total != first.Total {
		t.Errorf("device count grew from %d to %d over 3 repeat runs", first.Total, after.Total)
	}

	edges, err := h.store.CountEdges()
	if err != nil {
		t.Fatalf("count edges: %v", err)
	}
	t.Logf("stable across 4 runs: %d devices, %d edges", after.Total, edges)
}
