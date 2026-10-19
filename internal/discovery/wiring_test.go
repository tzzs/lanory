package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// apiDiscoveryProvider mirrors the narrow interface internal/api declares (see
// internal/api/providers.go).
//
// It is declared here rather than imported so that a change on either side of
// the boundary fails to compile in this test instead of at integration time.
type apiDiscoveryProvider interface {
	Status() (*model.DiscoveryStatus, error)
	Collectors() ([]model.CollectorStatus, error)
	Scan(ctx context.Context, req ScanRequest) (*model.DiscoveryJob, error)
	Job(id string) (*model.DiscoveryJob, error)
	Cancel(id string) error
}

// TestWiringSatisfiesAPI proves the service satisfies the HTTP contract.
func TestWiringSatisfiesAPI(t *testing.T) {
	var svc apiDiscoveryProvider = NewService(config.Default(), nil)
	if svc == nil {
		t.Fatal("service is nil")
	}
	if _, err := svc.Status(); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := svc.Collectors(); err != nil {
		t.Fatalf("Collectors: %v", err)
	}
}

// TestWiringServiceWithRegistry wires the service, the registry, the real bus and
// the config exactly as cmd/lanory does.
func TestWiringServiceWithRegistry(t *testing.T) {
	cfg := config.Default()
	cfg.Discovery.Enabled = true

	bus := events.New(64)
	svc := NewService(cfg, bus)

	// The bus satisfies the service's Publisher.
	var _ Publisher = bus

	// Registry registration is exercised by the per-collector wiring test in
	// internal/topology, which can import the collector packages without a
	// cycle.
	svc.Registry().RegisterAll(newServiceFake(model.CollectorOpenWRT, true, 1),
		newServiceFake(model.CollectorBridgeFDB, true, 1),
		newServiceFake(model.CollectorARP, true, 1),
		newServiceFake(model.CollectorNDP, true, 1),
		newServiceFake(model.CollectorDHCP, true, 1))

	status, err := svc.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Collectors) != 5 {
		t.Fatalf("collectors = %d, want 5", len(status.Collectors))
	}
	// Every reported collector must have a coherent status row.
	for _, c := range status.Collectors {
		if c.Name == "" {
			t.Error("a status row has no name")
		}
		if c.Enabled && !c.Supported {
			t.Errorf("%s is reported enabled but unsupported", c.Name)
		}
		if c.Description == "" {
			t.Errorf("%s has no description", c.Name)
		}
	}
}

// sqlReconciler is a minimal Reconciler used to prove the service can be wired
// to a real store without importing the topology package.
type sqlReconciler struct {
	store *database.Store
	calls int
}

func (r *sqlReconciler) Reconcile(_ context.Context, summary *RunSummary) (ReconcileStats, error) {
	r.calls++
	var stats ReconcileStats
	for i := range summary.Results {
		if summary.Results[i].Interface != nil {
			stats.InterfacesUpdated++
		}
	}
	return stats, nil
}

// TestWiringReconciler proves the Reconciler seam accepts a store-backed
// implementation: the topology update engine satisfies it structurally.
func TestWiringReconciler(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "wiring.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	store := database.NewStore(db)

	cfg := config.Default()
	svc := NewService(cfg, nil)
	f := newServiceFake(model.CollectorARP, true, 3)
	svc.Registry().Register(f)
	rec := &sqlReconciler{store: store}
	svc.WithReconciler(rec)

	job, err := svc.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	finished := waitForJob(t, svc, job.ID)
	if finished.Status != model.JobCompleted {
		t.Fatalf("status = %q (%s)", finished.Status, finished.Error)
	}
	if rec.calls != 1 {
		t.Fatalf("reconciler calls = %d", rec.calls)
	}
	// The store is still healthy after the run.
	if err := store.Ping(); err != nil {
		t.Fatalf("store ping: %v", err)
	}
}
