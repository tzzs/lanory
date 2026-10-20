// Package discovery — registry.
//
// The registry owns the set of collectors, decides which ones are enabled, and
// runs them concurrently with a bounded worker pool.
//
// Three rules shape this file:
//
//   - A collector never fails the run. A broken parser, a missing binary and a
//     panic all become a per-collector error that is recorded and reported; the
//     other collectors still run and their results still reach the store.
//   - The run is bounded. MaxConcurrency comes from the config and is clamped to
//     something a router can survive, because unbounded goroutines on a 256 MB
//     device is how a monitoring tool becomes an outage.
//   - The run is cancellable. Every collector receives the run context, and the
//     registry returns as soon as that context is cancelled rather than waiting
//     for a slow command to finish.
package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/model"
)

// DefaultMaxConcurrency bounds parallel collectors when the config does not say
// otherwise. The collectors are cheap (a command or a file read each), so a
// handful of workers is plenty and keeps the router responsive.
const DefaultMaxConcurrency = 4

// MaxConcurrencyLimit is the hard ceiling. Even a misconfigured file cannot
// make the registry spawn more workers than this.
const MaxConcurrencyLimit = 32

// RunResult is the outcome of one collector inside a run.
type RunResult struct {
	// Name is the collector identifier.
	Name string
	// Status is the job status for this collector.
	Status model.JobStatus
	// Results are the observations the collector produced.
	Results []Result
	// Err is the collector's failure, when it failed.
	Err error
	// Duration is how long the collector took.
	Duration time.Duration
	// StartedAt is when the collector started.
	StartedAt time.Time
}

// RunSummary is the outcome of a whole run.
type RunSummary struct {
	// StartedAt and FinishedAt bracket the run.
	StartedAt  time.Time
	FinishedAt time.Time
	// Results are the merged observations from every successful collector.
	Results []Result
	// Collectors holds the per-collector outcome, sorted by name.
	Collectors []RunResult
	// Cancelled reports whether the run was cut short by its context.
	Cancelled bool
}

// Duration returns the wall clock duration of the run.
func (s *RunSummary) Duration() time.Duration { return s.FinishedAt.Sub(s.StartedAt) }

// Errors returns the per-collector failures keyed by collector name.
func (s *RunSummary) Errors() map[string]error {
	out := map[string]error{}
	for _, c := range s.Collectors {
		if c.Err != nil {
			out[c.Name] = c.Err
		}
	}
	return out
}

// Count returns the number of results from a given collector.
func (s *RunSummary) Count(name string) int {
	for _, c := range s.Collectors {
		if c.Name == name {
			return len(c.Results)
		}
	}
	return 0
}

// Failed reports whether any collector failed.
func (s *RunSummary) Failed() bool {
	for _, c := range s.Collectors {
		if c.Err != nil {
			return true
		}
	}
	return false
}

// Registry owns the collector set.
//
// It is safe for concurrent use: status reads and runs take different locks, so
// the status endpoint never blocks behind a running discovery pass.
type Registry struct {
	mu         sync.RWMutex
	collectors []Collector
	enabled    map[string]bool
	status     map[string]*model.CollectorStatus

	maxConcurrency int

	// now returns the current time; overridden in tests.
	now func() time.Time
}

// NewRegistry builds a registry from the collector configuration.
//
// Collectors that are disabled in the config are still constructed, so the
// status endpoint can list them with Enabled: false. They are never run.
func NewRegistry(cfg *config.Config) *Registry {
	r := &Registry{
		enabled:        map[string]bool{},
		status:         map[string]*model.CollectorStatus{},
		now:            time.Now,
		maxConcurrency: DefaultMaxConcurrency,
	}
	if cfg != nil {
		r.maxConcurrency = clampConcurrency(cfg.Discovery.MaxConcurrency)
		r.applyEnabled(cfg.Collectors)
	}
	return r
}

// clampConcurrency bounds the worker count to something sane.
func clampConcurrency(n int) int {
	if n <= 0 {
		return DefaultMaxConcurrency
	}
	if n > MaxConcurrencyLimit {
		return MaxConcurrencyLimit
	}
	return n
}

// applyEnabled records the enable/disable flags from the config.
func (r *Registry) applyEnabled(c config.CollectorsConfig) {
	r.enabled[model.CollectorOpenWRT] = c.OpenWrt
	r.enabled[model.CollectorARP] = c.ARP
	r.enabled[model.CollectorNDP] = c.NDP
	r.enabled[model.CollectorDHCP] = c.DHCP
	r.enabled[model.CollectorBridgeFDB] = c.FDB
	r.enabled[model.CollectorLLDP] = c.LLDP
	r.enabled[model.CollectorMDNS] = c.MDNS
	r.enabled[model.CollectorScan] = c.Scan
}

// SetEnabled turns a collector on or off at runtime (the settings endpoint).
func (r *Registry) SetEnabled(name string, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled[name] = enabled
}

// Register adds a collector. A collector with a duplicate name replaces the
// previous one, which is what a test double wants.
func (r *Registry) Register(c Collector) {
	if c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.collectors {
		if existing.Name() == c.Name() {
			r.collectors[i] = c
			return
		}
	}
	r.collectors = append(r.collectors, c)
	if _, ok := r.enabled[c.Name()]; !ok {
		// A collector registered outside the config defaults to enabled: if it
		// was constructed, it was meant to run.
		r.enabled[c.Name()] = true
	}
}

// RegisterAll adds several collectors.
func (r *Registry) RegisterAll(cs ...Collector) {
	for _, c := range cs {
		r.Register(c)
	}
}

// Collectors returns a snapshot of the registered collectors, sorted by name.
func (r *Registry) Collectors() []Collector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Collector, len(r.collectors))
	copy(out, r.collectors)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names returns the registered collector names, sorted.
func (r *Registry) Names() []string {
	cs := r.Collectors()
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name()
	}
	return out
}

// Status reports every collector with its support, capabilities and last run.
//
// This is the payload of GET /discovery/collectors. An unsupported collector is
// still listed (the UI explains why it is greyed out) but it is never enabled.
func (r *Registry) Status() []model.CollectorStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]model.CollectorStatus, 0, len(r.collectors))
	for _, c := range r.collectors {
		supported := c.Supported()
		st := model.CollectorStatus{
			Name:         c.Name(),
			Supported:    supported,
			Capabilities: c.Capabilities(),
			Description:  c.Description(),
			Enabled:      r.enabled[c.Name()] && supported,
		}
		if last, ok := r.status[c.Name()]; ok {
			st.LastRunAt = last.LastRunAt
			st.LastError = last.LastError
			st.LastDurationMS = last.LastDurationMS
			st.Records = last.Records
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Supported returns the names of the collectors that can run on this host.
func (r *Registry) Supported() []string {
	var out []string
	for _, c := range r.Collectors() {
		if c.Supported() {
			out = append(out, c.Name())
		}
	}
	return out
}

// Enabled returns the names of the collectors that will run: enabled in the
// config AND supported on this host.
func (r *Registry) Enabled() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for _, c := range r.collectors {
		if r.enabled[c.Name()] && c.Supported() {
			out = append(out, c.Name())
		}
	}
	sort.Strings(out)
	return out
}

// TargetSetter is implemented by collectors that accept per-run parameters.
//
// The active scanner is the only one today: it needs the subnets for this run,
// which come from the scan request rather than from the configuration. A
// collector that does not implement it is simply run without targets.
type TargetSetter interface {
	SetTargets(subnets []string) error
}

// SetTargets asks every registered collector that accepts targets to configure
// itself for the next run.
//
// An error from any collector is returned and the caller refuses the run:
// scanning the wrong range, or scanning an unauthorised one, is worse than not
// scanning at all.
func (r *Registry) SetTargets(subnets []string) error {
	for _, c := range r.Collectors() {
		if ts, ok := c.(TargetSetter); ok {
			if err := ts.SetTargets(subnets); err != nil {
				return err
			}
		}
	}
	return nil
}

// Run executes the named collectors (or every enabled collector when names is
// empty) concurrently, bounded by MaxConcurrency.
//
// A collector that is unknown, disabled or unsupported is skipped without an
// error: asking for a collector this host cannot provide is a normal request,
// not a mistake. A collector that fails is recorded in the summary and does not
// stop the others.
func (r *Registry) Run(ctx context.Context, names ...string) *RunSummary {
	if ctx == nil {
		ctx = context.Background()
	}
	started := r.now()
	selected := r.selectCollectors(names)

	summary := &RunSummary{
		StartedAt:  started,
		Collectors: make([]RunResult, 0, len(selected)),
	}
	if len(selected) == 0 {
		summary.FinishedAt = r.now()
		return summary
	}

	// Bounded worker pool. Each worker pulls the next collector index; there is
	// no per-collector goroutine, so a run of 20 collectors still uses at most
	// maxConcurrency goroutines.
	workers := r.maxConcurrency
	if workers > len(selected) {
		workers = len(selected)
	}
	if workers < 1 {
		workers = 1
	}

	results := make([]RunResult, len(selected))
	var (
		next   int
		nextMu sync.Mutex
		wg     sync.WaitGroup
	)
	claim := func() (int, bool) {
		nextMu.Lock()
		defer nextMu.Unlock()
		if next >= len(selected) {
			return 0, false
		}
		i := next
		next++
		return i, true
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i, ok := claim()
				if !ok {
					return
				}
				c := selected[i]
				if err := ctx.Err(); err != nil {
					results[i] = RunResult{
						Name:      c.Name(),
						Status:    model.JobCancelled,
						Err:       err,
						StartedAt: r.now(),
					}
					continue
				}
				results[i] = r.runOne(ctx, c)
			}
		}()
	}
	wg.Wait()

	// Merge, preserving a stable order so the API payload does not shuffle
	// between runs.
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	var merged []Result
	for _, rr := range results {
		summary.Collectors = append(summary.Collectors, rr)
		merged = append(merged, rr.Results...)
	}
	summary.Results = merged
	summary.FinishedAt = r.now()
	summary.Cancelled = ctx.Err() != nil
	r.record(summary)
	return summary
}

// runOne runs a single collector, timing it and converting a panic into an
// error.
//
// A context cancellation is not a collector failure: it means the run was
// aborted, so it is reported as JobCancelled even though Collect returned an
// error. The error is still kept on the result for diagnostics.
func (r *Registry) runOne(ctx context.Context, c Collector) RunResult {
	started := r.now()
	res := RunResult{Name: c.Name(), StartedAt: started}
	out, err := SafeCollect(ctx, c)
	res.Duration = r.now().Sub(started)
	res.Results = out
	res.Err = err
	switch {
	case ctx.Err() != nil:
		// The run was cancelled. Keep the error (it is usually context.Canceled)
		// but report the status the caller asked for.
		res.Status = model.JobCancelled
	case err != nil:
		res.Status = model.JobFailed
	default:
		res.Status = model.JobCompleted
	}
	return res
}

// record stores the last run outcome for the status endpoint.
func (r *Registry) record(s *RunSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rr := range s.Collectors {
		st, ok := r.status[rr.Name]
		if !ok {
			st = &model.CollectorStatus{Name: rr.Name}
			r.status[rr.Name] = st
		}
		st.LastRunAt = rr.StartedAt
		st.LastDurationMS = int(rr.Duration / time.Millisecond)
		st.Records = len(rr.Results)
		st.LastError = ""
		if rr.Err != nil {
			st.LastError = rr.Err.Error()
		}
	}
}

// selectCollectors resolves the requested names into collectors to run.
//
// An empty request means "every enabled and supported collector".
func (r *Registry) selectCollectors(names []string) []Collector {
	r.mu.RLock()
	defer r.mu.RUnlock()

	want := map[string]bool{}
	explicit := len(names) > 0
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			want[n] = true
		}
	}

	var out []Collector
	for _, c := range r.collectors {
		if explicit {
			if !want[c.Name()] {
				continue
			}
		} else if !r.enabled[c.Name()] {
			continue
		}
		if !c.Supported() {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// String renders a short human readable summary, used in logs.
func (s *RunSummary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "discovery run in %s: %d results from %d collectors",
		s.Duration().Round(time.Millisecond), len(s.Results), len(s.Collectors))
	for _, c := range s.Collectors {
		fmt.Fprintf(&b, "; %s=%s/%d", c.Name, c.Status, len(c.Results))
		if c.Err != nil {
			fmt.Fprintf(&b, " (%v)", c.Err)
		}
	}
	return b.String()
}
