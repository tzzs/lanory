package topology

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// newTestStore opens a throwaway SQLite database for the update tests.
func newTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "topology.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return database.NewStore(db)
}

// fakeIngestor is a deterministic stand-in for the identity engine. It keys
// devices by MAC so the tests can assert placement without depending on the
// real engine's heuristics.
type fakeIngestor struct {
	store   *database.Store
	devices map[string]*model.Device
	next    int
	// err, when set, makes every Resolve fail.
	err error
	// created counts how many devices were created.
	created int
	// calls records every ingest for assertion.
	calls []Ingest
}

func newFakeIngestor(store *database.Store) *fakeIngestor {
	return &fakeIngestor{store: store, devices: map[string]*model.Device{}}
}

func (f *fakeIngestor) Resolve(ing Ingest) (*model.Device, bool, error) {
	f.calls = append(f.calls, ing)
	if f.err != nil {
		return nil, false, f.err
	}
	mac := database.CanonicalizeMAC(ing.MAC)
	// The fake persists through the store so the update engine's own lookups
	// (edges, evidence) behave exactly as they do with the real engine.
	if d, ok := f.devices[mac]; ok {
		d.LastSeen = ing.ObservedAt
		if ing.Hostname != "" {
			d.Hostname = ing.Hostname
		}
		if err := f.store.UpsertDevice(d); err != nil {
			return nil, false, err
		}
		return d, false, nil
	}
	f.next++
	now := ing.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	d := &model.Device{
		ID:               database.FormatMAC(mac),
		Name:             ing.Hostname,
		Hostname:         ing.Hostname,
		Type:             model.DeviceTypeUnknown,
		State:            model.StateUnknown,
		PrimaryMAC:       database.FormatMAC(mac),
		FirstSeen:        now,
		LastSeen:         now,
		DiscoverySources: []model.DiscoverySource{ing.Source},
	}
	if d.Name == "" {
		d.Name = "device " + d.PrimaryMAC
	}
	f.devices[mac] = d
	f.created++
	if err := f.store.UpsertDevice(d); err != nil {
		return nil, false, err
	}
	return d, true, nil
}

// recordingBus captures published events.
type recordingBus struct {
	topology int
	links    []string
	devices  []string
}

func (b *recordingBus) PublishTopologyUpdated(reason string) { b.topology++ }
func (b *recordingBus) PublishLinkUpdated(e *model.TopologyEdge, reason string) {
	if e != nil {
		b.links = append(b.links, e.ID+":"+reason)
	}
}
func (b *recordingBus) PublishDeviceUpdated(d *model.Device, reason string) {}

// seedRouter creates the self device and its interfaces, which every update
// test needs: the engine attaches observed ports to this device.
func seedRouter(t *testing.T, store *database.Store) *model.Device {
	t.Helper()
	router := &model.Device{
		ID:          "dev-router",
		Name:        "EASEPi R1",
		Type:        model.DeviceTypeRouter,
		State:       model.StateOnline,
		IsSelf:      true,
		IsGateway:   true,
		PrimaryMAC:  "02:00:00:00:00:11",
		PrimaryIPv4: "192.168.100.1",
		FirstSeen:   time.Now().UTC(),
		LastSeen:    time.Now().UTC(),
	}
	if err := store.UpsertDevice(router); err != nil {
		t.Fatalf("seed router: %v", err)
	}
	ports := []struct {
		name    string
		mac     string
		typ     model.InterfaceType
		role    model.InterfaceRole
		carrier bool
		speed   int
		bridge  string
	}{
		{"eth0", "02:00:00:00:00:10", model.InterfaceTypeEthernet, model.InterfaceRoleWAN, true, 100, ""},
		{"eth1", "02:00:00:00:00:11", model.InterfaceTypeEthernet, model.InterfaceRoleLAN, true, 1000, "br-lan"},
		{"eth2", "02:00:00:00:00:12", model.InterfaceTypeEthernet, model.InterfaceRoleLAN, true, 2500, "br-lan"},
		{"eth3", "02:00:00:00:00:13", model.InterfaceTypeEthernet, model.InterfaceRoleLAN, false, 0, "br-lan"},
		{"br-lan", "02:00:00:00:00:11", model.InterfaceTypeBridge, model.InterfaceRoleLAN, true, 0, ""},
	}
	for _, p := range ports {
		iface := &model.NetworkInterface{
			ID:         "if-" + p.name,
			DeviceID:   router.ID,
			Name:       p.name,
			MAC:        p.mac,
			Type:       p.typ,
			Role:       p.role,
			AdminState: model.AdminUp,
			OperState:  model.OperUp,
			Carrier:    p.carrier,
			SpeedMbps:  p.speed,
			MTU:        1500,
			BridgeID:   p.bridge,
			IsPhysical: p.typ == model.InterfaceTypeEthernet,
		}
		if !p.carrier {
			iface.OperState = model.OperNoCarrier
		}
		if err := upsertInterface(store, iface); err != nil {
			t.Fatalf("seed interface %s: %v", p.name, err)
		}
	}
	return router
}

// fdbResult builds an FDB observation for a MAC on a port.
func fdbResult(mac, bridge, port string) discovery.Result {
	r := discovery.NewResult(discovery.ResultFDB, model.SourceBridgeFDB, time.Unix(1771000000, 0).UTC())
	r.FDBEntry = &discovery.FDBObservation{
		MAC:       mac,
		Bridge:    bridge,
		Port:      port,
		EntryType: model.FDBEntryDynamic,
	}
	return *r
}

// neighResult builds an IPv4 neighbour observation.
func neighResult(addr, mac, iface string, state model.NeighborState) discovery.Result {
	r := discovery.NewResult(discovery.ResultNeighbor, model.SourceARP, time.Unix(1771000000, 0).UTC())
	r.Neighbor = &discovery.NeighborObservation{
		Address:   addr,
		Family:    model.AddressFamilyIPv4,
		MAC:       mac,
		Interface: iface,
		State:     state,
	}
	return *r
}

// leaseResult builds a DHCP lease observation.
func leaseResult(mac, addr, hostname string) discovery.Result {
	r := discovery.NewResult(discovery.ResultLease, model.SourceDHCP, time.Unix(1771000000, 0).UTC())
	r.Lease = &discovery.LeaseObservation{
		MAC:      mac,
		Address:  addr,
		Hostname: hostname,
	}
	return *r
}

// summary wraps results in a RunSummary.
func summary(results ...discovery.Result) *discovery.RunSummary {
	now := time.Unix(1771000000, 0).UTC()
	return &discovery.RunSummary{
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		Results:    results,
	}
}

func newUpdateEngine(t *testing.T, store *database.Store) (*UpdateEngine, *fakeIngestor, *recordingBus) {
	t.Helper()
	ing := newFakeIngestor(store)
	bus := &recordingBus{}
	eng := NewUpdateEngine(store, bus).WithIngestor(ing)
	eng.Now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return eng, ing, bus
}

// ---------------------------------------------------------------------------
// device creation and merging
// ---------------------------------------------------------------------------

// A MAC seen by several collectors must become exactly one device.
func TestUpdateCreatesOneDevicePerMAC(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)

	const mac = "b0:82:e2:61:3e:b7"
	res, err := eng.Update(context.Background(), summary(
		neighResult("192.168.100.116", mac, "eth2", model.NeighborReachable),
		fdbResult(mac, "br-lan", "eth2"),
		leaseResult(mac, "192.168.100.116", "WindowsPC"),
	))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ing.created != 1 {
		t.Fatalf("expected exactly 1 device created, got %d", ing.created)
	}
	if res.DevicesCreated != 1 {
		t.Errorf("DevicesCreated = %d", res.DevicesCreated)
	}

	devices, total, err := store.ListDevices(database.DeviceFilter{IncludeHidden: true})
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if total != 2 { // the router plus the new device
		t.Fatalf("expected 2 devices in the store, got %d: %+v", total, devices)
	}
}

// Running the same discovery twice must not duplicate devices or edges.
func TestUpdateIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)

	const mac = "0c:6f:8b:4a:3d:d2"
	run := func() *UpdateResult {
		res, err := eng.Update(context.Background(), summary(
			neighResult("192.168.100.151", mac, "eth1", model.NeighborStale),
			fdbResult(mac, "br-lan", "eth1"),
			leaseResult(mac, "192.168.100.151", "iPhone"),
		))
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		return res
	}

	first := run()
	if first.DevicesCreated != 1 {
		t.Fatalf("first run created %d devices", first.DevicesCreated)
	}
	if first.EdgesCreated != 1 {
		t.Fatalf("first run created %d edges", first.EdgesCreated)
	}

	second := run()
	if second.DevicesCreated != 0 {
		t.Errorf("second run created %d devices", second.DevicesCreated)
	}
	if second.EdgesCreated != 0 {
		t.Errorf("second run created %d edges", second.EdgesCreated)
	}
	if ing.created != 1 {
		t.Errorf("the ingestor created %d devices across both runs", ing.created)
	}

	edges, err := store.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge after two runs, got %d", len(edges))
	}
}

// Two devices with different MACs stay two devices.
func TestUpdateDistinctMACsStayDistinct(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)

	_, err := eng.Update(context.Background(), summary(
		neighResult("192.168.100.151", "0c:6f:8b:4a:3d:d2", "eth1", model.NeighborStale),
		neighResult("192.168.100.222", "86:7b:1c:3f:06:5e", "eth1", model.NeighborReachable),
		fdbResult("0c:6f:8b:4a:3d:d2", "br-lan", "eth1"),
		fdbResult("86:7b:1c:3f:06:5e", "br-lan", "eth1"),
	))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ing.created != 2 {
		t.Fatalf("expected 2 devices, got %d", ing.created)
	}
}

// ---------------------------------------------------------------------------
// edges and evidence
// ---------------------------------------------------------------------------

// Every inferred edge must have a discovery_evidence row carrying the raw
// collector line.
func TestUpdateSavesEdgeEvidence(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	const mac = "b0:82:e2:61:3e:b7"
	if _, err := eng.Update(context.Background(), summary(
		fdbResult(mac, "br-lan", "eth2"),
	)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	edges, err := store.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	edge := edges[0]
	if edge.EdgeType != model.EdgeTypeObservedMACPort {
		t.Errorf("edge type = %q", edge.EdgeType)
	}
	if edge.Label != "eth2" {
		t.Errorf("edge label = %q, want eth2", edge.Label)
	}
	if edge.Confidence <= 0 || edge.Confidence > 1 {
		t.Errorf("edge confidence = %v", edge.Confidence)
	}

	ev, err := store.ListEvidence("edge", edge.ID)
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(ev) == 0 {
		t.Fatal("an inferred edge must carry evidence")
	}
	found := false
	for _, e := range ev {
		if e.Collector == model.CollectorBridgeFDB {
			found = true
			if e.RawData == "" {
				t.Error("evidence has no raw data")
			}
			if !contains(e.RawData, mac) {
				t.Errorf("evidence raw data does not mention the MAC: %q", e.RawData)
			}
		}
	}
	if !found {
		t.Fatalf("no bridge_fdb evidence found: %+v", ev)
	}
}

// FDB alone must yield a lower-confidence edge than FDB plus a DHCP lease.
func TestUpdateConfidenceGrowsWithSources(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	const fdbOnly = "aa:aa:aa:aa:aa:01"
	const both = "aa:aa:aa:aa:aa:02"

	if _, err := eng.Update(context.Background(), summary(
		fdbResult(fdbOnly, "br-lan", "eth1"),
		fdbResult(both, "br-lan", "eth1"),
		leaseResult(both, "192.168.100.50", "multi-source"),
	)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	edges, err := store.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	byLabel := map[string]model.TopologyEdge{}
	for _, e := range edges {
		if e.Label != "" {
			byLabel[e.Label] = e
		}
	}
	// Both edges are on eth1, so compare by target MAC instead.
	var fdbConf, bothConf float64
	for _, e := range edges {
		dev, err := store.GetDevice(e.TargetID)
		if err != nil {
			continue
		}
		switch dev.PrimaryMAC {
		case database.FormatMAC(database.CanonicalizeMAC(fdbOnly)):
			fdbConf = e.Confidence
		case database.FormatMAC(database.CanonicalizeMAC(both)):
			bothConf = e.Confidence
		}
	}
	if fdbConf == 0 || bothConf == 0 {
		t.Fatalf("did not find both edges: fdb=%v both=%v (%+v)", fdbConf, bothConf, edges)
	}
	if !(bothConf > fdbConf) {
		t.Fatalf("FDB+DHCP edge (%v) must outrank the FDB-only edge (%v)", bothConf, fdbConf)
	}
}

// A user-confirmed edge must survive a re-run untouched.
func TestUpdateKeepsUserConfirmedEdge(t *testing.T) {
	store := newTestStore(t)
	router := seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	// Seed a manual device and a user-defined edge to it.
	manual := &model.Device{
		ID:        "dev-manual-ap",
		Name:      "Bedroom AP",
		Type:      model.DeviceTypeWirelessAP,
		State:     model.StateUnknown,
		IsManual:  true,
		FirstSeen: time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
	}
	if err := store.UpsertDevice(manual); err != nil {
		t.Fatalf("seed manual device: %v", err)
	}
	edge := &model.TopologyEdge{
		SourceID:          router.ID,
		TargetID:          manual.ID,
		SourceInterfaceID: "if-eth1",
		EdgeType:          model.EdgeTypeUserDefined,
		DiscoverySource:   model.SourceManual,
		Confidence:        ConfidenceManual,
		UserConfirmed:     true,
		Locked:            true,
		Status:            model.EdgeStatusLinkUp,
		Label:             "bedroom",
	}
	if err := store.UpsertEdge(edge); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	// Run a discovery that does not mention the AP at all.
	if _, err := eng.Update(context.Background(), summary(
		neighResult("192.168.100.151", "0c:6f:8b:4a:3d:d2", "eth1", model.NeighborStale),
	)); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.GetEdge(edge.ID)
	if err != nil {
		t.Fatalf("user edge disappeared: %v", err)
	}
	if !got.UserConfirmed || !got.Locked {
		t.Error("user confirmation flags were lost")
	}
	if got.Confidence != ConfidenceManual {
		t.Errorf("confidence = %v, want 1.0", got.Confidence)
	}
	if got.Label != "bedroom" {
		t.Errorf("label = %q, want bedroom", got.Label)
	}

	// The manual device must still exist.
	if _, err := store.GetDevice(manual.ID); err != nil {
		t.Fatalf("manual device disappeared: %v", err)
	}
}

// A device that disappears from discovery must keep its edges, marked
// unverified, never deleted.
func TestUpdateMarksDisappearedDeviceUnverified(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	const mac = "b0:82:e2:61:3e:b7"
	// First run: the device is present.
	if _, err := eng.Update(context.Background(), summary(
		fdbResult(mac, "br-lan", "eth2"),
		neighResult("192.168.100.116", mac, "eth2", model.NeighborReachable),
	)); err != nil {
		t.Fatalf("first update: %v", err)
	}
	edges, err := store.ListEdges(database.EdgeFilter{})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	edgeID := edges[0].ID
	deviceID := edges[0].TargetID

	// Second run: the device is gone. The engine is rolled forward in time so
	// the stale check fires.
	eng.Now = func() time.Time { return time.Unix(1771009999, 0).UTC() }
	res, err := eng.Update(context.Background(), summary(
		neighResult("192.168.100.222", "86:7b:1c:3f:06:5e", "eth1", model.NeighborReachable),
	))
	if err != nil {
		t.Fatalf("second update: %v", err)
	}
	if res.EdgesUnverified == 0 {
		t.Error("expected the disappeared device's edge to be marked unverified")
	}

	got, err := store.GetEdge(edgeID)
	if err != nil {
		t.Fatalf("edge was deleted: %v", err)
	}
	if !got.Unverified {
		t.Error("edge is not marked unverified")
	}
	if got.Status != model.EdgeStatusUnverified {
		t.Errorf("status = %q, want unverified", got.Status)
	}

	// And the device itself must still exist.
	if _, err := store.GetDevice(deviceID); err != nil {
		t.Fatalf("device was deleted: %v", err)
	}
}

// An edge whose device returns must become verified again.
func TestUpdateReVerifiesReturningDevice(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	const mac = "b0:82:e2:61:3e:b7"
	if _, err := eng.Update(context.Background(), summary(fdbResult(mac, "br-lan", "eth2"))); err != nil {
		t.Fatalf("first: %v", err)
	}
	edges, _ := store.ListEdges(database.EdgeFilter{})
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	edgeID := edges[0].ID

	// Second run without the device: marked unverified.
	eng.Now = func() time.Time { return time.Unix(1771005000, 0).UTC() }
	if _, err := eng.Update(context.Background(), summary()); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, _ := store.GetEdge(edgeID)
	if !got.Unverified {
		t.Fatal("edge should be unverified after the device vanished")
	}

	// Third run with the device back: verified again.
	eng.Now = func() time.Time { return time.Unix(1771010000, 0).UTC() }
	if _, err := eng.Update(context.Background(), summary(fdbResult(mac, "br-lan", "eth2"))); err != nil {
		t.Fatalf("third: %v", err)
	}
	got, _ = store.GetEdge(edgeID)
	if got.Unverified {
		t.Error("edge should be verified again once the device is seen")
	}
}

// The FDB relation must follow the port when a device moves.
func TestUpdateEdgeFollowsPortChange(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	const mac = "b0:82:e2:61:3e:b7"
	if _, err := eng.Update(context.Background(), summary(fdbResult(mac, "br-lan", "eth1"))); err != nil {
		t.Fatalf("first: %v", err)
	}
	first, _ := store.ListEdges(database.EdgeFilter{})
	if len(first) != 1 || first[0].Label != "eth1" {
		t.Fatalf("expected an eth1 edge, got %+v", first)
	}

	// The device moves to eth2.
	eng.Now = func() time.Time { return time.Unix(1771009999, 0).UTC() }
	if _, err := eng.Update(context.Background(), summary(fdbResult(mac, "br-lan", "eth2"))); err != nil {
		t.Fatalf("second: %v", err)
	}
	edges, _ := store.ListEdges(database.EdgeFilter{})
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges (old + new port), got %d: %+v", len(edges), edges)
	}
	var eth1, eth2 *model.TopologyEdge
	for i := range edges {
		switch edges[i].Label {
		case "eth1":
			eth1 = &edges[i]
		case "eth2":
			eth2 = &edges[i]
		}
	}
	if eth1 == nil || eth2 == nil {
		t.Fatalf("missing a port edge: %+v", edges)
	}
	// The old edge is kept but marked unverified; it is never silently
	// deleted, because that would erase the history of the move.
	if !eth1.Unverified {
		t.Error("the old port edge must be marked unverified")
	}
	if eth2.Unverified {
		t.Error("the new port edge must not be marked unverified")
	}
}

// ---------------------------------------------------------------------------
// interfaces and speed changes
// ---------------------------------------------------------------------------

// A speed change must be detected and recorded.
func TestUpdateDetectsSpeedChange(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, bus := newUpdateEngine(t, store)

	// eth2 was seeded at 2500; report it at 1000.
	r := discovery.NewResult(discovery.ResultInterface, model.SourceOpenWRT, time.Unix(1771000000, 0).UTC())
	r.Interface = &discovery.InterfaceObservation{
		Name:       "eth2",
		MAC:        "02:00:00:00:00:12",
		Type:       model.InterfaceTypeEthernet,
		Role:       model.InterfaceRoleLAN,
		AdminState: model.AdminUp,
		OperState:  model.OperUp,
		Carrier:    true,
		SpeedMbps:  1000,
		MTU:        1500,
		BridgeID:   "br-lan",
		IsPhysical: true,
	}
	res, err := eng.Update(context.Background(), summary(*r))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(res.SpeedChanges) != 1 {
		t.Fatalf("expected 1 speed change, got %+v", res.SpeedChanges)
	}
	sc := res.SpeedChanges[0]
	if sc.Name != "eth2" || sc.OldMbps != 2500 || sc.NewMbps != 1000 {
		t.Errorf("speed change = %+v", sc)
	}

	// The interface must actually carry the new speed.
	iface, err := getInterfaceByName(store, "dev-router", "eth2")
	if err != nil {
		t.Fatalf("get interface: %v", err)
	}
	if iface.SpeedMbps != 1000 {
		t.Errorf("stored speed = %d, want 1000", iface.SpeedMbps)
	}
	if iface.LastSpeedChange.IsZero() {
		t.Error("LastSpeedChange was not recorded")
	}

	// The transition must be preserved as evidence, so the UI can show the
	// history even though network_events cannot represent an edge-less event.
	ev, err := store.ListEvidence("interface_port", "if-eth2")
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	found := false
	for _, e := range ev {
		if e.EvidenceType == string(model.EventSpeedChanged) {
			found = true
			if !contains(e.RawData, "2500") || !contains(e.RawData, "1000") {
				t.Errorf("speed evidence does not record both values: %q", e.RawData)
			}
		}
	}
	if !found {
		t.Errorf("no speed.changed evidence recorded: %+v", ev)
	}
	if bus.topology == 0 {
		t.Error("no topology.updated event published")
	}
}

// A carrier loss must not be reported as a device going offline.
func TestUpdateCarrierLossIsNotOffline(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)
	const mac = "b0:82:e2:61:3e:b7"
	if _, err := eng.Update(context.Background(), summary(fdbResult(mac, "br-lan", "eth2"))); err != nil {
		t.Fatalf("first: %v", err)
	}

	// eth3 loses carrier (it already has none, so use eth2 for contrast).
	r := discovery.NewResult(discovery.ResultInterface, model.SourceOpenWRT, time.Unix(1771005000, 0).UTC())
	r.Interface = &discovery.InterfaceObservation{
		Name:       "eth2",
		Type:       model.InterfaceTypeEthernet,
		Role:       model.InterfaceRoleLAN,
		AdminState: model.AdminUp,
		OperState:  model.OperNoCarrier,
		Carrier:    false,
		SpeedMbps:  0,
		MTU:        1500,
		BridgeID:   "br-lan",
		IsPhysical: true,
	}
	if _, err := eng.Update(context.Background(), summary(*r)); err != nil {
		t.Fatalf("second: %v", err)
	}

	device, err := store.GetDevice("b0:82:e2:61:3e:b7")
	if err != nil {
		t.Fatalf("device missing: %v", err)
	}
	if device.State == model.StateOffline {
		t.Fatal("a carrier loss must never mark a device offline")
	}
}

// ---------------------------------------------------------------------------
// failure handling
// ---------------------------------------------------------------------------

// A failing identity engine must not abort the whole reconciliation.
func TestUpdateSurvivesIngestorFailure(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, ing, _ := newUpdateEngine(t, store)
	ing.err = errors.New("identity engine down")

	res, err := eng.Update(context.Background(), summary(
		neighResult("192.168.100.151", "0c:6f:8b:4a:3d:d2", "eth1", model.NeighborStale),
	))
	if err != nil {
		t.Fatalf("Update must not fail the whole run: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Fatal("expected the per-observation error to be recorded")
	}
}

// An empty run is a no-op, not an error.
func TestUpdateEmptySummary(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	res, err := eng.Update(context.Background(), summary())
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.Observations != 0 {
		t.Errorf("observations = %d", res.Observations)
	}
	if res.DevicesCreated != 0 || res.EdgesCreated != 0 {
		t.Errorf("empty run created things: %+v", res)
	}
}

// A cancelled context must abort promptly.
func TestUpdateHonoursCancellation(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Update(ctx, summary(
		neighResult("192.168.100.151", "0c:6f:8b:4a:3d:d2", "eth1", model.NeighborStale),
	)); err == nil {
		t.Fatal("expected a cancellation error")
	}
}

// An FDB entry naming a port the router does not have must not create an edge.
func TestUpdateIgnoresUnknownPort(t *testing.T) {
	store := newTestStore(t)
	seedRouter(t, store)
	eng, _, _ := newUpdateEngine(t, store)

	if _, err := eng.Update(context.Background(), summary(
		fdbResult("aa:bb:cc:dd:ee:ff", "br-lan", "eth99"),
	)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	edges, _ := store.ListEdges(database.EdgeFilter{})
	if len(edges) != 0 {
		t.Fatalf("expected no edge for an unknown port, got %+v", edges)
	}
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
