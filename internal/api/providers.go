package api

import (
	"context"

	"github.com/lanory/lanory/internal/model"
)

// This file declares the narrow interfaces the HTTP layer needs from the
// engines that other packages own.
//
// The API package must not import internal/discovery, internal/topology,
// internal/monitoring or internal/demo: those packages are developed in
// parallel and change often. Instead every capability the handlers need is
// expressed as an interface here, and the concrete implementation is injected
// through App.
//
// Until the engines are wired, internal/api/providers_stub.go implements these
// interfaces directly from the database, so the server is fully functional
// end to end today. The stub is a real implementation (it derives everything
// from stored data); it is not a fake that invents results.

// TopologyProvider builds the topology graph served by GET /topology.
//
// TODO(engine): internal/topology will supply the concrete type. Its
// *topology.Builder takes a store reader and options and returns a *Graph; a
// small adapter (Build -> *model.Topology via Graph.ToModel) will satisfy this
// interface.
type TopologyProvider interface {
	// Build returns the current topology graph.
	Build() (*model.Topology, error)
	// Evidence returns the edges with their supporting evidence.
	Evidence() (*model.TopologyEvidence, error)
	// AutoLayout recomputes node positions with the named algorithm.
	AutoLayout(algorithm string) ([]model.TopologyLayout, error)
}

// DiscoveryProvider runs discovery collectors.
//
// TODO(engine): internal/discovery will supply the concrete type. Its
// *discovery.Registry already reports CollectorStatus and lists names and
// capabilities; the job scheduling around it (Scan/Job/Cancel) is the part
// still to be adapted to this interface.
type DiscoveryProvider interface {
	// Status returns the current discovery state.
	Status() (*model.DiscoveryStatus, error)
	// Collectors lists the registered collectors and whether they work here.
	Collectors() ([]model.CollectorStatus, error)
	// Scan starts a discovery job and returns it.
	Scan(ctx context.Context, req ScanRequest) (*model.DiscoveryJob, error)
	// Job returns a discovery job by ID.
	Job(id string) (*model.DiscoveryJob, error)
	// Cancel stops a running job.
	Cancel(id string) error
}

// ScanRequest is the body of POST /discovery/scan.
type ScanRequest struct {
	// Collectors limits the run to the named collectors. Empty means all.
	Collectors []string `json:"collectors,omitempty"`
	// Subnets limits the run to the given CIDRs. Empty means auto-detected.
	Subnets []string `json:"subnets,omitempty"`
}

// MonitoringProvider executes probes.
//
// TODO(engine): internal/monitoring will supply the concrete type:
// *monitoring.Engine, constructed with the store, a bus publisher and an
// executor. Engine.Status already returns *model.MonitoringStatus; Engine.RunNow
// returns a ProbeRecord that an adapter can render as *model.ProbeResult.
type MonitoringProvider interface {
	// Status returns the probes with their recent results and the interfaces.
	Status() (*model.MonitoringStatus, error)
	// Run executes one probe immediately and returns its result.
	Run(ctx context.Context, probeID string) (*model.ProbeResult, error)
}

// DemoProvider loads the built-in sample network.
//
// TODO(engine): internal/demo.Provider (Enabled, Seed) is the intended
// implementation; it is not imported here so the API package stays independent
// of the demo package's lifecycle.
type DemoProvider interface {
	// Enabled reports whether demo mode is active.
	Enabled() bool
	// Seed loads the named sample dataset.
	Seed(ctx context.Context, name string) error
}

// SettingsProvider reads and writes effective settings, layering the database
// overrides on top of the configuration file.
//
// TODO(engine): internal/config should own effective settings once it learns
// about app_settings overrides; until then storeSettings in providers_stub.go
// keeps the configuration defaults together with the database overrides.
type SettingsProvider interface {
	// All returns the effective settings as a flat key/value map.
	All() (map[string]string, error)
	// Patch applies a partial update and returns the resulting map.
	Patch(patch map[string]any) (map[string]string, error)
}

// InterfaceStatusProvider derives the runtime status of network interfaces.
//
// TODO(engine): internal/monitoring.LinkObserver reports live interface status;
// the store-only implementation in providers_stub.go reports the last observed
// state, which is correct but is not a fresh measurement.
type InterfaceStatusProvider interface {
	// InterfaceStatus returns the status of every interface, newest first.
	InterfaceStatus() ([]model.InterfaceStatus, error)
}
