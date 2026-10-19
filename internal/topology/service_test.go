package topology

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// newServiceTestStore opens a real SQLite store in a temp dir.
func newServiceTestStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return database.NewStore(db)
}

func TestServiceBuildEmptyStore(t *testing.T) {
	store := newServiceTestStore(t)
	svc := NewService(store, nil)

	topo, err := svc.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if topo == nil {
		t.Fatal("Build returned nil")
	}
	// The API contract requires non-nil lists, never null.
	if topo.Nodes == nil || topo.Edges == nil || topo.Layout == nil {
		t.Fatal("empty lists must be non-nil")
	}
	if len(topo.Nodes) != 0 {
		t.Fatalf("expected no nodes, got %d", len(topo.Nodes))
	}
}

func TestServiceBuildWithData(t *testing.T) {
	store := newServiceTestStore(t)
	router := &model.Device{
		ID: "router-1", Name: "Router", Type: model.DeviceTypeRouter,
		State: model.StateOnline, IsSelf: true, PrimaryMAC: "02:00:00:00:00:11",
	}
	if err := store.UpsertDevice(router); err != nil {
		t.Fatalf("seed router: %v", err)
	}
	pc := &model.Device{
		ID: "pc-1", Name: "WindowsPC", Type: model.DeviceTypeDesktop,
		State: model.StateOnline, PrimaryMAC: "b0:82:e2:61:3e:b7",
	}
	if err := store.UpsertDevice(pc); err != nil {
		t.Fatalf("seed pc: %v", err)
	}
	// NOTE: the interface write goes through UpsertInterface, which currently
	// fails inside internal/database (interfaceColumns is used without its
	// "SELECT " prefix, so GetInterface/ListAllInterfaces raise a SQL syntax
	// error). The service under test does not depend on that path, so the edge
	// is seeded with a plain interface id instead of a stored row; the port
	// label is what the topology engine reads.
	const ifaceID = "if-eth1"
	if err := store.UpsertEdge(&model.TopologyEdge{
		SourceID: router.ID, TargetID: pc.ID,
		SourceInterfaceID: ifaceID,
		EdgeType:          model.EdgeTypeObservedMACPort,
		DiscoverySource:   model.SourceBridgeFDB,
		Confidence:        0.7,
		Status:            model.EdgeStatusLinkUp,
		Label:             "eth1",
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	svc := NewService(store, nil)
	topo, err := svc.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var gotPC *model.TopologyNode
	for i := range topo.Nodes {
		if topo.Nodes[i].ID == "dev-pc-1" {
			gotPC = &topo.Nodes[i]
		}
	}
	if gotPC == nil {
		t.Fatalf("device node missing: %+v", topo.Nodes)
	}
	if gotPC.Kind != "device" {
		t.Errorf("kind = %q", gotPC.Kind)
	}
	if len(topo.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(topo.Edges))
	}
	e := topo.Edges[0]
	if e.SourceID != "dev-router-1" || e.TargetID != "dev-pc-1" {
		t.Errorf("edge endpoints = %s -> %s", e.SourceID, e.TargetID)
	}
	if e.EdgeType != model.EdgeTypeObservedMACPort {
		t.Errorf("edge type = %q", e.EdgeType)
	}
	if e.Label != "eth1" {
		t.Errorf("edge label = %q, want the port", e.Label)
	}
}

func TestServiceEvidence(t *testing.T) {
	store := newServiceTestStore(t)
	router := &model.Device{ID: "router-1", Name: "Router", State: model.StateOnline}
	pc := &model.Device{ID: "pc-1", Name: "PC", State: model.StateOnline}
	lonely := &model.Device{ID: "lonely-1", Name: "Lonely", State: model.StateUnknown}
	for _, d := range []*model.Device{router, pc, lonely} {
		if err := store.UpsertDevice(d); err != nil {
			t.Fatalf("seed device: %v", err)
		}
	}
	edge := &model.TopologyEdge{
		SourceID: router.ID, TargetID: pc.ID,
		EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
		Confidence: 0.7, Status: model.EdgeStatusLinkUp,
	}
	if err := store.UpsertEdge(edge); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	if err := store.SaveEvidence(&model.DiscoveryEvidence{
		Collector: "bridge_fdb", SubjectType: "edge", SubjectID: edge.ID,
		EvidenceType: "fdb_port", RawData: "aa:bb dev eth1",
	}); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}

	svc := NewService(store, nil)
	ev, err := svc.Evidence()
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if len(ev.Edges) != 1 {
		t.Fatalf("expected 1 edge evidence, got %d", len(ev.Edges))
	}
	if len(ev.Edges[0].Evidence) != 1 {
		t.Errorf("edge evidence = %d records", len(ev.Edges[0].Evidence))
	}
	if len(ev.Unmapped) != 1 {
		t.Fatalf("expected 1 unmapped device, got %d", len(ev.Unmapped))
	}
	if ev.Unmapped[0].Device.ID != lonely.ID {
		t.Errorf("unmapped = %q, want the lonely device", ev.Unmapped[0].Device.ID)
	}
}

func TestServiceAutoLayout(t *testing.T) {
	store := newServiceTestStore(t)
	router := &model.Device{ID: "r", Name: "R", State: model.StateOnline}
	pc := &model.Device{ID: "p", Name: "P", State: model.StateOnline}
	for _, d := range []*model.Device{router, pc} {
		if err := store.UpsertDevice(d); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := store.UpsertEdge(&model.TopologyEdge{
		SourceID: "r", TargetID: "p",
		EdgeType: model.EdgeTypeUserDefined, DiscoverySource: model.SourceManual,
		Confidence: 1.0, UserConfirmed: true, Status: model.EdgeStatusLinkUp,
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	cfg := config.Default()
	cfg.Topology.AutoLayout = true
	svc := NewService(store, cfg)

	layout, err := svc.AutoLayout("tree")
	if err != nil {
		t.Fatalf("AutoLayout: %v", err)
	}
	if len(layout) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(layout))
	}
	// The positions must be persisted.
	stored, err := store.ListLayout()
	if err != nil {
		t.Fatalf("list layout: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected 2 persisted positions, got %d", len(stored))
	}
	// The root must be above its child.
	y := map[string]float64{}
	for _, l := range layout {
		y[l.NodeID] = l.Y
	}
	if y["dev-r"] >= y["dev-p"] {
		t.Errorf("root y=%v must be above child y=%v", y["dev-r"], y["dev-p"])
	}
}

func TestServiceAutoLayoutPreservesPinned(t *testing.T) {
	store := newServiceTestStore(t)
	if err := store.UpsertDevice(&model.Device{ID: "r", Name: "R", State: model.StateOnline}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.SaveLayout(&model.TopologyLayout{
		NodeID: "dev-r", X: 111, Y: 222, Pinned: true,
	}); err != nil {
		t.Fatalf("seed layout: %v", err)
	}

	cfg := config.Default()
	cfg.Topology.AutoLayout = true
	svc := NewService(store, cfg)
	layout, err := svc.AutoLayout("tree")
	if err != nil {
		t.Fatalf("AutoLayout: %v", err)
	}
	for _, l := range layout {
		if l.NodeID == "dev-r" {
			if l.X != 111 || l.Y != 222 {
				t.Fatalf("a pinned node moved to (%v,%v)", l.X, l.Y)
			}
			return
		}
	}
	t.Fatal("pinned node missing from the layout")
}

// With auto layout disabled and manual positions preserved, the stored layout is
// returned untouched: rearranging a hand-arranged network would be a bug.
func TestServiceAutoLayoutDisabledReturnsStored(t *testing.T) {
	store := newServiceTestStore(t)
	if err := store.UpsertDevice(&model.Device{ID: "r", Name: "R", State: model.StateOnline}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.SaveLayout(&model.TopologyLayout{
		NodeID: "dev-r", X: 7, Y: 9,
	}); err != nil {
		t.Fatalf("seed layout: %v", err)
	}
	cfg := config.Default()
	cfg.Topology.AutoLayout = false
	cfg.Topology.PreserveManualLayout = true

	svc := NewService(store, cfg)
	layout, err := svc.AutoLayout("tree")
	if err != nil {
		t.Fatalf("AutoLayout: %v", err)
	}
	if len(layout) != 1 || layout[0].X != 7 || layout[0].Y != 9 {
		t.Fatalf("layout = %+v, want the stored position", layout)
	}
}

func TestServiceNilStore(t *testing.T) {
	svc := NewService(nil, nil)
	if _, err := svc.Build(); err == nil {
		t.Error("Build must fail without a store")
	}
	if _, err := svc.Evidence(); err == nil {
		t.Error("Evidence must fail without a store")
	}
	if _, err := svc.AutoLayout("tree"); err == nil {
		t.Error("AutoLayout must fail without a store")
	}
}

func TestServiceUsesConfigThreshold(t *testing.T) {
	store := newServiceTestStore(t)
	cfg := config.Default()
	cfg.Topology.UnverifiedEdgeThreshold = 0.9
	svc := NewService(store, cfg)
	if svc.builder().opts.UnverifiedEdgeThreshold != 0.9 {
		t.Errorf("threshold = %v, want 0.9", svc.builder().opts.UnverifiedEdgeThreshold)
	}

	// A zero threshold falls back to the documented default rather than
	// marking everything verified.
	cfg2 := config.Default()
	cfg2.Topology.UnverifiedEdgeThreshold = 0
	svc2 := NewService(store, cfg2)
	if svc2.builder().opts.UnverifiedEdgeThreshold != UnverifiedThreshold {
		t.Errorf("threshold = %v, want the default", svc2.builder().opts.UnverifiedEdgeThreshold)
	}
	_ = time.Now
}
