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
// internal/api/providers_stub.go supplies a store-only fallback for every
// interface here, used when no engine is injected — the internal/api tests,
// and any build that does not call newSchedulers. The stub is a real
// implementation (it derives everything from stored data); it is not a fake
// that invents results.

// TopologyProvider builds the topology graph served by GET /topology.
//
// In production cmd/lanory/engines.go injects topologyProvider, an adapter
// over *topology.Service. Without an injected engine the store-only fallback
// in providers_stub.go is used.
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
// In production cmd/lanory/engines.go injects discoveryProvider, an adapter
// over *discovery.Service. Without an injected engine the store-only fallback
// in providers_stub.go is used.
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
// In production cmd/lanory/engines.go injects monitoringProvider, an adapter
// over *monitoring.Engine. Without an injected engine the store-only fallback
// in providers_stub.go is used.
type MonitoringProvider interface {
	// Status returns the probes with their recent results and the interfaces.
	Status() (*model.MonitoringStatus, error)
	// Run executes one probe immediately and returns its result.
	Run(ctx context.Context, probeID string) (*model.ProbeResult, error)
}

// DemoProvider loads the built-in sample network.
//
// In production cmd/lanory/engines.go injects *demo.Provider directly, so the
// API package stays independent of the demo package's lifecycle.
type DemoProvider interface {
	// Enabled reports whether demo mode is active.
	Enabled() bool
	// Seed loads the named sample dataset.
	Seed(ctx context.Context, name string) error
}

// SettingsProvider reads and writes effective settings, layering the database
// overrides on top of the configuration file.
//
// No engine implements this yet: internal/config does not read app_settings,
// so the defaults-plus-overrides layering lives in storeSettings in
// providers_stub.go, in production as well as in tests. internal/config should
// own effective settings once it learns about app_settings overrides.
type SettingsProvider interface {
	// All returns the effective settings as a flat key/value map.
	All() (map[string]string, error)
	// Patch applies a partial update and returns the resulting map.
	Patch(patch map[string]any) (map[string]string, error)
}

// InterfaceStatusProvider derives the runtime status of network interfaces.
//
// internal/monitoring.LinkObserver can report live interface status, but the
// implementation in use is still the store-only one in providers_stub.go,
// which reports the last observed state — correct, but not a fresh
// measurement.
type InterfaceStatusProvider interface {
	// InterfaceStatus returns the status of every interface, newest first.
	InterfaceStatus() ([]model.InterfaceStatus, error)
}
