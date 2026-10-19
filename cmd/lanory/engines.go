package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/api"
	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/demo"
	"github.com/lanory/lanory/internal/device"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/discovery/arp"
	"github.com/lanory/lanory/internal/discovery/dhcp"
	"github.com/lanory/lanory/internal/discovery/fdb"
	"github.com/lanory/lanory/internal/discovery/lldp"
	"github.com/lanory/lanory/internal/discovery/mdns"
	"github.com/lanory/lanory/internal/discovery/ndp"
	"github.com/lanory/lanory/internal/discovery/openwrt"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
	"github.com/lanory/lanory/internal/monitoring"
	"github.com/lanory/lanory/internal/platform"
	"github.com/lanory/lanory/internal/topology"
)

// newSchedulers wires the discovery, topology and monitoring engines into the
// application and starts the background loops. It is the integration point that
// connects the independently developed packages to the API layer.
//
// Every engine is guarded: if a constructor fails, the failure is logged and the
// service keeps running, because Lanory must never refuse to boot.
func newSchedulers(
	cfg *config.Config,
	host platform.Platform,
	store *database.Store,
	bus *events.Bus,
	app *api.App,
	demoProvider *demo.Provider,
	logger *slog.Logger,
) {
	ctx := context.Background()

	// Device identity engine: shared by discovery ingestion and probe state.
	identity := device.New(store, bus)

	// Discovery registry with the platform-appropriate collectors.
	registry := discovery.NewRegistry(cfg)
	registry.RegisterAll(
		openwrt.New(),
		arp.New(),
		ndp.New(),
		dhcp.New(),
		fdb.New(),
		lldp.New(),
		mdns.New(),
	)

	// Topology: reconcile observations into the graph, then build the API view.
	updateEngine := topology.NewUpdateEngine(store, bus).WithIngestor(ingestorAdapter{identity})
	topoService := topology.NewService(store, cfg)

	// Monitoring: probe engine with a conservative worker pool.
	monEngine := monitoring.NewEngine(
		store,
		bus,
		monitoring.NewExecutor(monitoring.DefaultExecutorConfig()),
		identity,
		monitoringEngineConfig(cfg),
	)
	if err := monEngine.Start(ctx); err != nil {
		logger.Warn("monitoring engine failed to start", "error", err)
	}

	jobs := newJobBook()

	// Background discovery loop. Demo mode never scans the host.
	if !cfg.Demo.Enabled && cfg.Discovery.Enabled {
		startDiscoveryLoop(ctx, cfg, registry, updateEngine, bus, logger)
	}

	// Expose the engines to the API through its narrow provider interfaces.
	app.SetTopology(topologyProvider{svc: topoService})
	app.SetDiscovery(discoveryProvider{reg: registry, update: updateEngine, jobs: jobs})
	app.SetMonitoring(monitoringProvider{engine: monEngine})
	if demoProvider != nil {
		app.SetDemo(demoProvider)
	}

	if interval, err := cfg.DiscoveryInterval(); err == nil {
		logger.Info("engines wired",
			"platform", host.Kind,
			"collectors", len(registry.Collectors()),
			"discovery_interval", interval.String(),
			"demo", cfg.Demo.Enabled,
		)
	}
}

// startDiscoveryLoop runs discovery periodically in the background.
func startDiscoveryLoop(
	ctx context.Context,
	cfg *config.Config,
	reg *discovery.Registry,
	update *topology.UpdateEngine,
	bus *events.Bus,
	logger *slog.Logger,
) {
	interval, err := cfg.DiscoveryInterval()
	if err != nil || interval <= 0 {
		interval = 10 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				summary := reg.Run(ctx)
				if result, uerr := update.Update(ctx, summary); uerr != nil {
					logger.Warn("discovery update failed", "error", uerr)
				} else if result != nil {
					logger.Debug("discovery run applied",
						"observations", result.Observations,
						"devices_created", result.DevicesCreated,
						"edges_created", result.EdgesCreated,
					)
				}
				bus.PublishTopologyUpdated("discovery run applied")
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Adapters to the API provider interfaces
// ---------------------------------------------------------------------------

type topologyProvider struct{ svc *topology.Service }

func (p topologyProvider) Build() (*model.Topology, error) { return p.svc.Build() }

func (p topologyProvider) Evidence() (*model.TopologyEvidence, error) { return p.svc.Evidence() }

func (p topologyProvider) AutoLayout(algorithm string) ([]model.TopologyLayout, error) {
	return p.svc.AutoLayout(algorithm)
}

type discoveryProvider struct {
	reg    *discovery.Registry
	update *topology.UpdateEngine
	jobs   *jobBook
}

func (p discoveryProvider) Status() (*model.DiscoveryStatus, error) {
	return p.jobs.status(p.reg), nil
}

func (p discoveryProvider) Collectors() ([]model.CollectorStatus, error) {
	return p.reg.Status(), nil
}

func (p discoveryProvider) Scan(ctx context.Context, req api.ScanRequest) (*model.DiscoveryJob, error) {
	summary := p.reg.Run(ctx, req.Collectors...)
	result, err := p.update.Update(ctx, summary)
	if err != nil {
		return nil, err
	}
	job := jobFromSummary(summary, result, "manual")
	p.jobs.put(job)
	return job, nil
}

func (p discoveryProvider) Job(id string) (*model.DiscoveryJob, error) {
	job := p.jobs.get(id)
	if job == nil {
		return nil, errors.New("discovery: job not found")
	}
	return job, nil
}

func (p discoveryProvider) Cancel(id string) error {
	p.jobs.cancel(id)
	return nil
}

type monitoringProvider struct{ engine *monitoring.Engine }

func (p monitoringProvider) Status() (*model.MonitoringStatus, error) {
	status, err := p.engine.Status(20)
	if err != nil {
		return nil, err
	}
	return &status, nil
}

func (p monitoringProvider) Run(ctx context.Context, probeID string) (*model.ProbeResult, error) {
	rec, err := p.engine.RunNow(ctx, probeID)
	if err != nil {
		return nil, err
	}
	result := rec.Result
	return &result, nil
}

// ingestorAdapter bridges the device.Engine to the topology update engine's
// DeviceIngestor interface, converting the mirrored ingest struct.
type ingestorAdapter struct{ e *device.Engine }

func (a ingestorAdapter) Resolve(ing topology.Ingest) (*model.Device, bool, error) {
	return a.e.Resolve(device.Ingest{
		MAC:          ing.MAC,
		Hostname:     ing.Hostname,
		Vendor:       ing.Vendor,
		Addresses:    ing.Addresses,
		Source:       ing.Source,
		ObservedAt:   ing.ObservedAt,
		StrongID:     ing.StrongID,
		StrongIDType: ing.StrongIDType,
		ClientID:     ing.ClientID,
		TypeHint:     ing.TypeHint,
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func monitoringEngineConfig(cfg *config.Config) monitoring.EngineConfig {
	timeout, err := cfg.MonitoringTimeout()
	if err != nil || timeout <= 0 {
		timeout = 3 * time.Second
	}
	return monitoring.EngineConfig{
		Monitoring:       cfg.Monitoring,
		Database:         cfg.Database,
		Workers:          cfg.Discovery.MaxConcurrency,
		RunNowTimeout:    timeout,
		InterfaceRefresh: interfaceRefresh(cfg),
	}
}

// jobFromSummary renders a discovery run as an API DiscoveryJob.
func jobFromSummary(summary *discovery.RunSummary, result *topology.UpdateResult, trigger string) *model.DiscoveryJob {
	job := &model.DiscoveryJob{
		ID:         "job-" + time.Now().UTC().Format("20060102T150405.000000000"),
		Status:     model.JobCompleted,
		Trigger:    trigger,
		StartedAt:  summary.StartedAt,
		FinishedAt: summary.FinishedAt,
		Progress:   100,
		Collectors: make([]model.CollectorRun, 0, len(summary.Collectors)),
	}
	if summary.Cancelled {
		job.Status = model.JobCancelled
	}
	for _, rc := range summary.Collectors {
		job.Collectors = append(job.Collectors, model.CollectorRun{
			Name:       rc.Name,
			Status:     rc.Status,
			DurationMS: int(rc.Duration.Milliseconds()),
			Records:    len(rc.Results),
			Error:      errString(rc.Err),
		})
	}
	if result != nil {
		job.DevicesFound = result.DevicesCreated + result.DevicesUpdated
		job.DevicesNew = result.DevicesCreated
		job.InterfacesFound = result.InterfacesUpdated
		job.EdgesCreated = result.EdgesCreated + result.EdgesUpdated
	}
	return job
}

// interfaceRefresh returns how often the link observer polls /sys/class/net.
func interfaceRefresh(cfg *config.Config) time.Duration {
	if d, err := cfg.InterfaceRefreshInterval(); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// jobBook keeps recent discovery jobs in memory for the status and job
// endpoints. Bounded, because Lanory runs on a router with limited RAM.
type jobBook struct {
	mu   sync.Mutex
	jobs []*model.DiscoveryJob
}

func newJobBook() *jobBook {
	return &jobBook{jobs: make([]*model.DiscoveryJob, 0, 16)}
}

func (b *jobBook) put(job *model.DiscoveryJob) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.jobs = append(b.jobs, job)
	if len(b.jobs) > 16 {
		b.jobs = b.jobs[len(b.jobs)-16:]
	}
}

func (b *jobBook) get(id string) *model.DiscoveryJob {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.jobs) - 1; i >= 0; i-- {
		if b.jobs[i].ID == id {
			return b.jobs[i]
		}
	}
	return nil
}

func (b *jobBook) cancel(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, job := range b.jobs {
		if job.ID == id && job.Status == model.JobRunning {
			job.Status = model.JobCancelled
			return
		}
	}
}

func (b *jobBook) latest() *model.DiscoveryJob {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.jobs) == 0 {
		return nil
	}
	return b.jobs[len(b.jobs)-1]
}

func (b *jobBook) status(reg *discovery.Registry) *model.DiscoveryStatus {
	return &model.DiscoveryStatus{
		LastJob:    b.latest(),
		Collectors: reg.Status(),
		Interval:   "10m",
		Enabled:    true,
	}
}
