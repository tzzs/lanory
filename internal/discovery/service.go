package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/model"
)

// Service is the concrete discovery engine the HTTP layer consumes.
//
// It owns the registry, the job lifecycle and the single-flight guarantee: only
// one discovery run may be in flight at a time. That is not a performance
// optimisation, it is a correctness requirement — two concurrent
// `bridge fdb show` passes on a router produce interleaved observations and a
// job history the user cannot reason about.
//
// The wiring layer does:
//
//	svc := discovery.NewService(store, cfg, bus)
//	svc.Registry().RegisterAll(openwrt.New(), arp.New(), ...)
//	app.SetDiscovery(svc)
type Service struct {
	registry *Registry
	cfg      *config.Config
	bus      Publisher

	// reconciler turns a finished run into store writes. When nil the service
	// still runs collectors and reports their results, it just does not persist
	// them: useful for a dry run and for tests.
	reconciler Reconciler
	// scheduler triggers periodic runs.
	scheduler Scheduler

	mu      sync.Mutex
	jobs    map[string]*model.DiscoveryJob
	order   []string
	current string
	running bool
	cancel  context.CancelFunc
	lastJob string

	// Now is the clock; overridden in tests.
	Now func() time.Time
}

// Publisher is the slice of the event bus the service needs.
type Publisher interface {
	PublishDiscoveryStarted(jobID, message string)
	PublishDiscoveryCompleted(jobID, status string, progress int)
	PublishTopologyUpdated(reason string)
}

// Reconciler persists the results of a finished run.
//
// Declared as an interface so the service does not import internal/topology
// (which imports the discovery result types), and so tests can assert what was
// persisted without a database.
type Reconciler interface {
	Reconcile(ctx context.Context, summary *RunSummary) (ReconcileStats, error)
}

// ReconcileStats is the subset of the topology engine's update counters the
// service copies onto the job record.
type ReconcileStats struct {
	DevicesCreated    int
	DevicesUpdated    int
	InterfacesUpdated int
	EdgesCreated      int
	EdgesUpdated      int
}

// Scheduler triggers periodic discovery runs.
type Scheduler interface {
	// Start begins the periodic loop and returns a stop function.
	Start(ctx context.Context, interval time.Duration, trigger func()) (stop func())
}

// maxRetainedJobs bounds the job history. A router has flash for a database, not
// for an unbounded list of every scan since boot.
const maxRetainedJobs = 50

// NewService returns a discovery service backed by cfg.
func NewService(cfg *config.Config, bus Publisher) *Service {
	var cc config.CollectorsConfig
	if cfg != nil {
		cc = cfg.Collectors
	} else {
		cfg = config.Default()
		cc = cfg.Collectors
	}
	s := &Service{
		registry: NewRegistry(cfg),
		cfg:      cfg,
		bus:      bus,
		jobs:     map[string]*model.DiscoveryJob{},
		Now:      func() time.Time { return time.Now().UTC() },
	}
	_ = cc
	return s
}

// Registry exposes the collector registry so the wiring layer can register the
// collectors it detected for this platform.
func (s *Service) Registry() *Registry { return s.registry }

// WithReconciler injects the component that persists a run.
func (s *Service) WithReconciler(r Reconciler) *Service {
	s.reconciler = r
	return s
}

// WithScheduler injects the periodic trigger.
func (s *Service) WithScheduler(sch Scheduler) *Service {
	s.scheduler = sch
	return s
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// Status returns the current discovery state for GET /discovery/status.
func (s *Service) Status() (*model.DiscoveryStatus, error) {
	s.mu.Lock()
	running := s.running
	current := s.current
	var last *model.DiscoveryJob
	if s.lastJob != "" {
		if j, ok := s.jobs[s.lastJob]; ok {
			cp := *j
			cp.Collectors = append([]model.CollectorRun{}, j.Collectors...)
			last = &cp
		}
	}
	s.mu.Unlock()

	enabled := true
	interval := ""
	if s.cfg != nil {
		enabled = s.cfg.Discovery.Enabled
		interval = s.cfg.Discovery.Interval
	}

	return &model.DiscoveryStatus{
		Running:      running,
		CurrentJobID: current,
		LastJob:      last,
		Collectors:   s.registry.Status(),
		Interval:     interval,
		Enabled:      enabled,
	}, nil
}

// Collectors lists the registered collectors and their status.
func (s *Service) Collectors() ([]model.CollectorStatus, error) {
	return s.registry.Status(), nil
}

// ScanRequest selects what a run should do.
type ScanRequest struct {
	// Collectors limits the run to the named collectors. Empty means all.
	Collectors []string `json:"collectors,omitempty"`
	// Subnets limits the run to the given CIDRs. Empty means auto-detected.
	Subnets []string `json:"subnets,omitempty"`
	// Trigger is "manual", "scheduled" or "startup".
	Trigger string `json:"-"`
}

// ErrAlreadyRunning is returned when a scan is requested while one is in flight.
var ErrAlreadyRunning = errors.New("discovery: a scan is already running")

// ErrJobNotFound is returned for an unknown job id.
var ErrJobNotFound = errors.New("discovery: job not found")

// Scan starts a discovery run and returns its job record.
//
// The run happens in the background so the HTTP request returns immediately; the
// job id is what the UI polls (or what the SSE stream reports). Starting a second
// run while one is in flight is refused rather than queued: a queue of scans on a
// router is a way to hammer the same bridge forever.
func (s *Service) Scan(ctx context.Context, req ScanRequest) (*model.DiscoveryJob, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	trigger := req.Trigger
	if trigger == "" {
		trigger = "manual"
	}
	job := &model.DiscoveryJob{
		ID:         s.newJobID(),
		Status:     model.JobRunning,
		Trigger:    trigger,
		StartedAt:  s.now(),
		Progress:   0,
		Collectors: []model.CollectorRun{},
	}
	// The run must outlive the HTTP request that started it, so it gets its own
	// cancellable context rather than the request's.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.running = true
	s.current = job.ID
	s.cancel = cancel
	s.jobs[job.ID] = job
	s.order = append(s.order, job.ID)
	s.lastJob = job.ID
	s.pruneLocked()
	// The snapshot returned to the caller is taken here, under the lock and
	// before the run goroutine exists. Once `go s.run` is called that goroutine
	// starts writing status, progress, finish time and the collector list on the
	// very same record, so copying afterwards would be an unsynchronised read of
	// fields another goroutine is mutating. The caller gets the state the scan
	// was accepted in, which is what POST /discovery/scan documents.
	cp := *job
	cp.Collectors = append([]model.CollectorRun{}, job.Collectors...)
	s.mu.Unlock()

	if s.bus != nil {
		s.bus.PublishDiscoveryStarted(job.ID, "discovery started")
	}
	if req.Trigger != "manual" {
		req.Trigger = trigger
	}

	go s.run(runCtx, job.ID, req)

	return &cp, nil
}

// run executes the collectors and then the reconciliation.
func (s *Service) run(ctx context.Context, jobID string, req ScanRequest) {
	defer func() {
		s.mu.Lock()
		if s.current == jobID {
			s.running = false
			s.current = ""
			s.cancel = nil
		}
		s.mu.Unlock()
	}()

	// Apply the per-run subnet request before anything is probed. A rejected
	// subnet fails the job instead of silently scanning the wrong range.
	if len(req.Subnets) > 0 {
		if err := s.registry.SetTargets(req.Subnets); err != nil {
			s.mu.Lock()
			if j := s.jobs[jobID]; j != nil {
				j.Status = model.JobFailed
				j.Error = err.Error()
				j.FinishedAt = s.now()
				j.Progress = 100
			}
			s.mu.Unlock()
			if s.bus != nil {
				s.bus.PublishDiscoveryCompleted(jobID, string(model.JobFailed), 100)
			}
			return
		}
	}

	summary := s.registry.Run(ctx, req.Collectors...)
	finished := s.now()

	s.mu.Lock()
	job := s.jobs[jobID]
	if job == nil {
		s.mu.Unlock()
		return
	}
	job.FinishedAt = finished
	job.Progress = 100
	job.Collectors = make([]model.CollectorRun, 0, len(summary.Collectors))
	for _, c := range summary.Collectors {
		job.Collectors = append(job.Collectors, model.CollectorRun{
			Name:       c.Name,
			Status:     c.Status,
			DurationMS: int(c.Duration / time.Millisecond),
			Records:    len(c.Results),
			Error:      errString(c.Err),
		})
	}
	job.InterfacesFound = countKind(summary.Results, ResultInterface)
	s.mu.Unlock()

	if s.reconciler != nil && len(summary.Results) > 0 {
		stats, err := s.reconciler.Reconcile(ctx, summary)
		if err != nil {
			s.mu.Lock()
			if j := s.jobs[jobID]; j != nil {
				j.Error = err.Error()
			}
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			if j := s.jobs[jobID]; j != nil {
				j.DevicesFound = stats.DevicesCreated + stats.DevicesUpdated
				j.DevicesNew = stats.DevicesCreated
				j.InterfacesFound = stats.InterfacesUpdated
				j.EdgesCreated = stats.EdgesCreated
			}
			s.mu.Unlock()
			if s.bus != nil {
				s.bus.PublishTopologyUpdated("discovery")
			}
		}
	}

	status := model.JobCompleted
	if summary.Cancelled {
		status = model.JobCancelled
	} else if summary.Failed() && len(summary.Results) == 0 {
		status = model.JobFailed
	}
	s.mu.Lock()
	if j := s.jobs[jobID]; j != nil {
		j.Status = status
	}
	s.mu.Unlock()

	if s.bus != nil {
		s.bus.PublishDiscoveryCompleted(jobID, string(status), 100)
	}
}

// Job returns a discovery job by ID.
func (s *Service) Job(id string) (*model.DiscoveryJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	cp := *j
	cp.Collectors = append([]model.CollectorRun{}, j.Collectors...)
	return &cp, nil
}

// Cancel stops the running job.
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != id || s.cancel == nil {
		if j, ok := s.jobs[id]; ok && j.Status == model.JobRunning {
			return fmt.Errorf("discovery: job %s is not the running job", id)
		}
		return ErrJobNotFound
	}
	s.cancel()
	if j := s.jobs[id]; j != nil {
		j.Status = model.JobCancelled
	}
	return nil
}

// StartScheduler begins the periodic discovery loop.
//
// The first run happens immediately: a router that just rebooted, or a service
// that was just restarted after an upgrade, has no topology at all until a
// scan completes. Waiting a full interval would leave the UI empty and the
// monitor with nothing to probe.
//
// It returns a stop function the wiring layer calls on shutdown. When discovery
// is disabled in the configuration nothing is scheduled, which is what
// discovery.enabled=false is supposed to mean.
func (s *Service) StartScheduler(ctx context.Context) (func(), error) {
	if s.cfg == nil || !s.cfg.Discovery.Enabled {
		return func() {}, nil
	}
	if s.scheduler == nil {
		s.scheduler = &tickerScheduler{}
	}
	interval, err := s.cfg.DiscoveryInterval()
	if err != nil {
		return nil, err
	}
	// The default scheduler scans once at startup, so a restarted service has
	// data without waiting for the first tick.
	if ts, ok := s.scheduler.(*tickerScheduler); ok {
		return ts.StartWithImmediate(ctx, interval, func() {
			_, _ = s.Scan(ctx, ScanRequest{Trigger: "scheduled"})
		}, func() {
			_, _ = s.Scan(ctx, ScanRequest{Trigger: "startup"})
		}), nil
	}
	return s.scheduler.Start(ctx, interval, func() {
		_, _ = s.Scan(ctx, ScanRequest{Trigger: "scheduled"})
	}), nil
}

// tickerScheduler is the default periodic trigger.
type tickerScheduler struct{}

// Start runs trigger on every tick until ctx is cancelled or stop is called.
func (t *tickerScheduler) Start(ctx context.Context, interval time.Duration, trigger func()) func() {
	return t.StartWithImmediate(ctx, interval, trigger, nil)
}

// StartWithImmediate runs trigger on every tick, and once immediately before
// the first tick when onStart is given.
//
// The immediate run matters on a router: after a reboot or an upgrade the
// service has no topology at all, and waiting a full interval for the first
// tick leaves the UI empty and the monitor with nothing to probe.
func (t *tickerScheduler) StartWithImmediate(ctx context.Context, interval time.Duration, trigger func(), onStart func()) func() {
	if interval <= 0 {
		return func() {}
	}
	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	go func() {
		defer ticker.Stop()
		if onStart != nil {
			onStart()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				trigger()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// newJobID returns a sortable job identifier.
func (s *Service) newJobID() string {
	return fmt.Sprintf("job-%x", s.now().UnixNano())
}

// pruneLocked drops the oldest jobs beyond the retention limit.
func (s *Service) pruneLocked() {
	for len(s.order) > maxRetainedJobs {
		oldest := s.order[0]
		s.order = s.order[1:]
		if oldest == s.current {
			// Never drop the running job.
			s.order = append(s.order, oldest)
			continue
		}
		delete(s.jobs, oldest)
		if s.lastJob == oldest {
			s.lastJob = ""
		}
	}
}

// Jobs returns the retained job history, newest first.
func (s *Service) Jobs() []model.DiscoveryJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.DiscoveryJob, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		if j, ok := s.jobs[s.order[i]]; ok {
			cp := *j
			cp.Collectors = append([]model.CollectorRun{}, j.Collectors...)
			out = append(out, cp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// EnabledCollectors returns the names the service would actually run.
func (s *Service) EnabledCollectors() []string { return s.registry.Enabled() }

func countKind(results []Result, kind ResultKind) int {
	n := 0
	for i := range results {
		if results[i].Kind == kind {
			n++
		}
	}
	return n
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
