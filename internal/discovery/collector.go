// Package discovery defines the Collector contract shared by every discovery
// source, plus the normalized Result type they all emit.
//
// Design rules that every collector must follow:
//
//   - A collector never fails the whole discovery run. It returns an error that
//     is recorded and the other collectors continue.
//   - A collector only *reports* observations. It must not mutate the network.
//   - When the underlying system command or file is missing, the collector
//     reports Supported() == false instead of erroring.
//   - Every result carries its source, timestamp, evidence and a confidence so
//     the topology engine can reason about it.
//
// The Collector interface and the normalized Result type live in discovery.go.
// This file provides BaseCollector, the embedded implementation every concrete
// collector builds on: it centralises the bookkeeping (name, support detection,
// capability list, timestamping, panic containment) so the concrete collectors
// only contain the code that actually reads a router.
package discovery

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// BaseCollector holds the state shared by every concrete collector. Concrete
// collectors embed it and populate the fields at construction time; the
// embedded methods give them the Name / Supported / Capabilities / Description
// part of the Collector interface for free.
//
// The support flag is stored atomically so a collector can flip it during
// Collect (for example after noticing the ubus socket disappeared) without
// racing the status endpoint.
type BaseCollector struct {
	name         string
	description  string
	capabilities []string

	// supported is the cached answer to Supported(). Concrete collectors that
	// need a live answer override the method; the rest use this flag.
	supported atomic.Bool

	// lastCheck records when the support flag was last probed.
	lastCheck time.Time
}

// NewBaseCollector builds the shared state for a concrete collector.
func NewBaseCollector(name, description string, supported bool, capabilities ...string) *BaseCollector {
	b := &BaseCollector{name: name, description: description, capabilities: capabilities}
	b.supported.Store(supported)
	return b
}

// Name returns the collector identifier.
func (b *BaseCollector) Name() string { return b.name }

// Description returns the human readable explanation.
func (b *BaseCollector) Description() string { return b.description }

// Capabilities returns a copy of the capability list, so callers cannot mutate
// the collector's own slice.
func (b *BaseCollector) Capabilities() []string {
	out := make([]string, len(b.capabilities))
	copy(out, b.capabilities)
	return out
}

// Supported returns whether the collector can run on this host.
func (b *BaseCollector) Supported() bool { return b.supported.Load() }

// SetSupported updates the cached support flag.
func (b *BaseCollector) SetSupported(v bool) { b.supported.Store(v) }

// SetLastCheck records when the support flag was refreshed.
func (b *BaseCollector) SetLastCheck(t time.Time) { b.lastCheck = t }

// LastCheck returns when support was last probed.
func (b *BaseCollector) LastCheck() time.Time { return b.lastCheck }

// Stamp fills in the source and timestamp on a result when the collector left
// them empty. Every result a collector returns must carry both, so the
// topology engine can weigh evidence by recency.
func Stamp(r *Result, source model.DiscoverySource, at time.Time) *Result {
	if r.Source == "" {
		r.Source = source
	}
	if r.ObservedAt.IsZero() {
		if at.IsZero() {
			at = time.Now().UTC()
		}
		r.ObservedAt = at
	}
	return r
}

// NewResult builds a stamped result of the given kind.
func NewResult(kind ResultKind, source model.DiscoverySource, at time.Time) *Result {
	return Stamp(&Result{Kind: kind}, source, at)
}

// EmptyResults returns an empty, non-nil slice. Collectors use it for the
// "nothing observed" case so callers can rely on a nil error with no records.
func EmptyResults() []Result { return []Result{} }

// CollectError reports a collector failure that must not abort the run. It
// wraps err with the collector name so the registry can log which source broke
// without knowing the collector internals.
func CollectError(name string, err error) error {
	if err == nil {
		return nil
	}
	return &CollectorFailure{Collector: name, Err: err}
}

// CollectorFailure carries the name of the failing collector.
type CollectorFailure struct {
	Collector string
	Err       error
}

// Error implements error.
func (f *CollectorFailure) Error() string {
	return fmt.Sprintf("discovery: collector %s: %v", f.Collector, f.Err)
}

// Unwrap exposes the underlying cause.
func (f *CollectorFailure) Unwrap() error { return f.Err }

// PanicError carries a recovered panic value so a bug in one parser surfaces as
// a normal collector error instead of crashing the process.
type PanicError struct {
	Value any
}

// Error implements error.
func (p *PanicError) Error() string { return fmt.Sprintf("panic during collect: %v", p.Value) }

// RunAll runs every supported collector in cs sequentially, returning the
// merged results and a per-collector error map.
//
// A collector that errors, panics, or is unsupported does not stop the others:
// that is the whole point of the collector boundary. Use Registry.Run for the
// concurrent variant.
func RunAll(ctx context.Context, cs []Collector) ([]Result, map[string]error) {
	var (
		out  []Result
		errs = map[string]error{}
	)
	for _, c := range cs {
		if c == nil || !c.Supported() {
			continue
		}
		results, err := SafeCollect(ctx, c)
		if err != nil {
			errs[c.Name()] = err
			continue
		}
		out = append(out, results...)
	}
	return out, errs
}

// SafeCollect runs one collector, converting a panic into an error so a bug in
// a single parser cannot take down the discovery loop.
func SafeCollect(ctx context.Context, c Collector) (results []Result, err error) {
	if c == nil {
		return nil, fmt.Errorf("discovery: nil collector")
	}
	defer func() {
		if r := recover(); r != nil {
			results = nil
			err = CollectError(c.Name(), &PanicError{Value: r})
		}
	}()
	return c.Collect(ctx)
}
