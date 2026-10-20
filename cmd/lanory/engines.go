package main

import (
	"context"
	"errors"
	"log/slog"
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
	"github.com/lanory/lanory/internal/discovery/scan"
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

	// Discovery service: owns the job lifecycle (start, progress, cancel) and
	// the periodic schedule. The registry inside it holds the collectors.
	discoverySvc := discovery.NewService(cfg, bus)
	registerCollectors(discoverySvc.Registry(), cfg)

	// Topology: reconcile observations into the graph, then build the API view.
	updateEngine := topology.NewUpdateEngine(store, bus).WithIngestor(ingestorAdapter{identity})
	topoService := topology.NewService(store, cfg)
	discoverySvc.WithReconciler(reconcilerAdapter{engine: updateEngine})

	// Monitoring: probe engine with a conservative worker pool.
	executorCfg := monitoring.DefaultExecutorConfig()
	// A link-local IPv6 target is only reachable with the interface zone, and
	// the raw ICMP socket needs the source address in the IP header. Both are
	// resolved per probe rather than once at startup: at startup the database
	// is still empty, because the interfaces only appear after the first
	// discovery run.
	executorCfg.ICMPZone = func() string { return lanZone(store) }
	executorCfg.ICMPLocalAddr = func() string { return lanAddress(store) }

	monEngine := monitoring.NewEngine(
		store,
		bus,
		monitoring.NewExecutor(executorCfg),
		identity,
		monitoringEngineConfig(cfg),
	)
	if err := monEngine.Start(ctx); err != nil {
		logger.Warn("monitoring engine failed to start", "error", err)
	}

	// Background discovery schedule. Demo mode never scans the host.
	if !cfg.Demo.Enabled && cfg.Discovery.Enabled {
		stop, err := discoverySvc.StartScheduler(ctx)
		if err != nil {
			logger.Warn("discovery scheduler failed to start", "error", err)
		} else {
			_ = stop // the scheduler lives for the lifetime of the process
		}
	}

	// Expose the engines to the API through its narrow provider interfaces.
	app.SetTopology(topologyProvider{svc: topoService})
	app.SetDiscovery(discoveryProvider{svc: discoverySvc})
	app.SetMonitoring(monitoringProvider{engine: monEngine})
	if demoProvider != nil {
		app.SetDemo(demoProvider)
	}

	if interval, err := cfg.DiscoveryInterval(); err == nil {
		logger.Info("engines wired",
			"platform", host.Kind,
			"collectors", len(discoverySvc.Registry().Collectors()),
			"discovery_interval", interval.String(),
			"demo", cfg.Demo.Enabled,
		)
	}
}

// registerCollectors registers every collector available on this host. A
// collector that is not supported here reports itself unsupported, so the
// registry stays honest about what it can do on this platform.
//
// The active scanner is only registered when the operator enabled it and
// authorised at least one subnet. Scanning is never on by default, and a
// scanner with no authorised subnet would refuse every request anyway.
func registerCollectors(reg *discovery.Registry, cfg *config.Config) {
	reg.RegisterAll(
		openwrt.New(),
		arp.New(),
		ndp.New(),
		dhcp.New(),
		fdb.New(),
		lldp.New(),
		mdns.New(),
	)
	if cfg != nil && cfg.Collectors.Scan && len(cfg.Discovery.Subnets) > 0 {
		limit := scan.DefaultLimit()
		limit.AuthorisedSubnets = cfg.Discovery.Subnets
		limit.MaxHostsPerRun = 1024
		limit.MaxHostsPerSubnet = 512
		limit.Concurrency = cfg.Discovery.MaxConcurrency
		limit.RatePerSecond = cfg.Discovery.RateLimit
		if d, err := cfg.ScanTimeoutDuration(); err == nil {
			limit.Timeout = d
		}
		reg.Register(scan.New(limit))
	}
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

type discoveryProvider struct{ svc *discovery.Service }

func (p discoveryProvider) Status() (*model.DiscoveryStatus, error) { return p.svc.Status() }

func (p discoveryProvider) Collectors() ([]model.CollectorStatus, error) {
	return p.svc.Collectors()
}

func (p discoveryProvider) Scan(ctx context.Context, req api.ScanRequest) (*model.DiscoveryJob, error) {
	return p.svc.Scan(ctx, discovery.ScanRequest{
		Collectors: req.Collectors,
		Subnets:    req.Subnets,
		Trigger:    "manual",
	})
}

func (p discoveryProvider) Job(id string) (*model.DiscoveryJob, error) { return p.svc.Job(id) }

func (p discoveryProvider) Cancel(id string) error { return p.svc.Cancel(id) }

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

// reconcilerAdapter adapts the topology update engine to the discovery
// service's Reconciler interface, converting the counter structs.
type reconcilerAdapter struct{ engine *topology.UpdateEngine }

func (a reconcilerAdapter) Reconcile(ctx context.Context, summary *discovery.RunSummary) (discovery.ReconcileStats, error) {
	result, err := a.engine.Update(ctx, summary)
	if err != nil {
		return discovery.ReconcileStats{}, err
	}
	if result == nil {
		return discovery.ReconcileStats{}, nil
	}
	return discovery.ReconcileStats{
		DevicesCreated:    result.DevicesCreated,
		DevicesUpdated:    result.DevicesUpdated,
		InterfacesUpdated: result.InterfacesUpdated,
		EdgesCreated:      result.EdgesCreated,
		EdgesUpdated:      result.EdgesUpdated,
	}, nil
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
	interval, ierr := cfg.MonitoringInterval()
	if ierr != nil || interval <= 0 {
		interval = 60 * time.Second
	}

	auto := monitoring.DefaultAutoProbeConfig()
	auto.Enabled = cfg.Monitoring.AutoProbe
	auto.Interval = interval
	auto.Timeout = timeout
	if cfg.Monitoring.FailureThreshold > 0 {
		auto.FailureThreshold = cfg.Monitoring.FailureThreshold
	}
	if cfg.Monitoring.RecoverConfirm > 0 {
		auto.RecoverConfirm = cfg.Monitoring.RecoverConfirm
	}

	return monitoring.EngineConfig{
		Monitoring:       cfg.Monitoring,
		Database:         cfg.Database,
		Workers:          cfg.Discovery.MaxConcurrency,
		RunNowTimeout:    timeout,
		InterfaceRefresh: interfaceRefresh(cfg),
		AutoProbe:        auto,
	}
}

// lanAddress returns the IPv4 address of the LAN interface.
//
// It is the source address used for ICMP probes: sending from the router's WAN
// address would make a peer answer a different host than the one the user is
// looking at.
func lanAddress(store *database.Store) string {
	ifaces, err := store.ListAllInterfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Role != model.InterfaceRoleLAN {
			continue
		}
		for _, addr := range iface.Addresses {
			if addr.Family == model.AddressFamilyIPv4 && addr.Address != "" {
				return addr.Address
			}
		}
	}
	return ""
}

// lanZone returns the name of the interface that carries the LAN, used as the
// zone for link-local IPv6 probes.
//
// A link-local address (fe80::/10) is not routable: it is only meaningful
// together with the interface it was seen on. The LAN interface is preferred,
// and the first bridge is the fallback.
func lanZone(store *database.Store) string {
	ifaces, err := store.ListAllInterfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Role == model.InterfaceRoleLAN && iface.Type == model.InterfaceTypeEthernet {
			return iface.Name
		}
	}
	for _, iface := range ifaces {
		if iface.Type == model.InterfaceTypeBridge {
			return iface.Name
		}
	}
	return ""
}

// interfaceRefresh returns how often the link observer polls the interface state.
func interfaceRefresh(cfg *config.Config) time.Duration {
	if d, err := cfg.InterfaceRefreshInterval(); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// errString renders an error for a job record.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// errNotFound is the shared "no such record" sentinel for the adapters.
var errNotFound = errors.New("not found")
