package discovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/model"
)

// serviceConfig builds an enabled config with a short interval.
func serviceConfig(enabled bool) *config.Config {
	cfg := config.Default()
	cfg.Discovery.Enabled = enabled
	cfg.Discovery.Interval = "10m"
	cfg.Collectors = config.CollectorsConfig{
		OpenWrt: true, ARP: true, NDP: true, DHCP: true, FDB: true,
	}
	return cfg
}

// newResults builds n generic results so a fake collector has something to
// report.
func newResults(n int) []Result {
	out := make([]Result, 0, n)
	for i := 0; i < n; i++ {
		r := NewResult(ResultGeneric, model.SourceDemo, time.Unix(1771000000, 0).UTC())
		r.Attributes = map[string]string{"i": itoaSmall(i)}
		out = append(out, *r)
	}
	return out
}

func itoaSmall(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// newServiceFake builds a fake collector reporting n results.
func newServiceFake(name string, supported bool, n int) *fakeCollector {
	f := newFake(name, supported)
	f.results = newResults(n)
	return f
}

func newServiceWithFake(t *testing.T, enabled bool) (*Service, *fakeCollector) {
	t.Helper()
	s := NewService(serviceConfig(enabled), nil)
	f := newServiceFake(model.CollectorARP, true, 2)
	s.Registry().Register(f)
	return s, f
}

func TestServiceStatusBeforeAnyRun(t *testing.T) {
	s, _ := newServiceWithFake(t, true)
	st, err := s.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Error("nothing should be running")
	}
	if st.LastJob != nil {
		t.Error("expected no last job")
	}
	if len(st.Collectors) != 1 {
		t.Fatalf("collectors = %d", len(st.Collectors))
	}
	if !st.Enabled || st.Interval != "10m" {
		t.Errorf("config not reflected: enabled=%v interval=%q", st.Enabled, st.Interval)
	}
}

func TestServiceCollectors(t *testing.T) {
	s, _ := newServiceWithFake(t, true)
	cs, err := s.Collectors()
	if err != nil {
		t.Fatalf("Collectors: %v", err)
	}
	if len(cs) != 1 || cs[0].Name != model.CollectorARP {
		t.Fatalf("collectors = %+v", cs)
	}
	if !cs[0].Supported || !cs[0].Enabled {
		t.Errorf("status = %+v", cs[0])
	}
}

func TestServiceScanCompletes(t *testing.T) {
	s, f := newServiceWithFake(t, true)
	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if job.ID == "" {
		t.Fatal("job has no id")
	}
	if job.Trigger != "manual" {
		t.Errorf("trigger = %q", job.Trigger)
	}

	// Wait for the run to finish.
	done := waitForJob(t, s, job.ID)
	if done.Status != model.JobCompleted {
		t.Fatalf("status = %q (%s)", done.Status, done.Error)
	}
	if done.Progress != 100 {
		t.Errorf("progress = %d", done.Progress)
	}
	if done.FinishedAt.IsZero() {
		t.Error("no finish time")
	}
	if len(done.Collectors) != 1 {
		t.Fatalf("collector runs = %d", len(done.Collectors))
	}
	if done.Collectors[0].Records != 2 {
		t.Errorf("records = %d, want 2", done.Collectors[0].Records)
	}
	if f.calls.Load() != 1 {
		t.Errorf("collector ran %d times", f.calls.Load())
	}
}

func waitForJob(t *testing.T, s *Service, id string) *model.DiscoveryJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := s.Job(id)
		if err != nil {
			t.Fatalf("Job: %v", err)
		}
		if j.Status != model.JobRunning {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job never finished")
	return nil
}

// Starting a second run while one is in flight must be refused, not queued.
func TestServiceScanIsSingleFlight(t *testing.T) {
	s, f := newServiceWithFake(t, true)
	f.delay = 200 * time.Millisecond

	first, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if _, err := s.Scan(context.Background(), ScanRequest{}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Scan error = %v, want ErrAlreadyRunning", err)
	}
	waitForJob(t, s, first.ID)

	// Once finished a new run is allowed.
	second, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("third Scan: %v", err)
	}
	waitForJob(t, s, second.ID)
}

func TestServiceCancel(t *testing.T) {
	s, f := newServiceWithFake(t, true)
	f.delay = 5 * time.Second

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Give the worker a moment to start.
	time.Sleep(20 * time.Millisecond)

	begin := time.Now()
	if err := s.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	done := waitForJob(t, s, job.ID)
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("cancel took %v", elapsed)
	}
	if done.Status != model.JobCancelled {
		t.Errorf("status = %q, want cancelled", done.Status)
	}
}

func TestServiceCancelUnknownJob(t *testing.T) {
	s, _ := newServiceWithFake(t, true)
	if err := s.Cancel("nope"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("error = %v, want ErrJobNotFound", err)
	}
}

func TestServiceJobUnknown(t *testing.T) {
	s, _ := newServiceWithFake(t, true)
	if _, err := s.Job("nope"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("error = %v, want ErrJobNotFound", err)
	}
}

func TestServiceScanSelectsCollectors(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	arp := newServiceFake(model.CollectorARP, true, 1)
	ndp := newServiceFake(model.CollectorNDP, true, 1)
	s.Registry().RegisterAll(arp, ndp)

	job, err := s.Scan(context.Background(), ScanRequest{Collectors: []string{model.CollectorNDP}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitForJob(t, s, job.ID)
	if ndp.calls.Load() != 1 {
		t.Error("the selected collector did not run")
	}
	if arp.calls.Load() != 0 {
		t.Error("an unselected collector ran")
	}
}

// A failing collector must be recorded on the job without failing the run.
func TestServiceScanIsolatesCollectorFailure(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	bad := newServiceFake(model.CollectorARP, true, 0)
	bad.err = errors.New("arp exploded")
	good := newServiceFake(model.CollectorNDP, true, 1)
	s.Registry().RegisterAll(bad, good)

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	done := waitForJob(t, s, job.ID)
	if done.Status != model.JobCompleted {
		t.Errorf("status = %q, want completed: one broken collector must not fail the run", done.Status)
	}
	var badRun *model.CollectorRun
	for i := range done.Collectors {
		if done.Collectors[i].Name == model.CollectorARP {
			badRun = &done.Collectors[i]
		}
	}
	if badRun == nil {
		t.Fatal("the failing collector is missing from the job")
	}
	if badRun.Error == "" {
		t.Error("the failure was not recorded")
	}
	if badRun.Status != model.JobFailed {
		t.Errorf("collector status = %q", badRun.Status)
	}
}

func TestServiceScanFailedWhenEverythingFails(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	bad := newServiceFake(model.CollectorARP, true, 0)
	bad.err = errors.New("boom")
	s.Registry().Register(bad)

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	done := waitForJob(t, s, job.ID)
	if done.Status != model.JobFailed {
		t.Errorf("status = %q, want failed", done.Status)
	}
}

// fakeReconciler records what the service asked it to persist.
type fakeReconciler struct {
	mu      sync.Mutex
	calls   int
	records int
	err     error
	stats   ReconcileStats
}

func (f *fakeReconciler) Reconcile(_ context.Context, summary *RunSummary) (ReconcileStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.records = len(summary.Results)
	return f.stats, f.err
}

func TestServiceReconcilesResults(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 3))
	rec := &fakeReconciler{stats: ReconcileStats{
		DevicesCreated: 2, InterfacesUpdated: 5, EdgesCreated: 3,
	}}
	s.WithReconciler(rec)

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	done := waitForJob(t, s, job.ID)
	rec.mu.Lock()
	calls, records := rec.calls, rec.records
	rec.mu.Unlock()
	if calls != 1 {
		t.Fatalf("reconciler calls = %d", calls)
	}
	if records != 3 {
		t.Errorf("reconciler saw %d results, want 3", records)
	}
	if done.DevicesNew != 2 {
		t.Errorf("devices_new = %d", done.DevicesNew)
	}
	if done.DevicesFound != 2 {
		t.Errorf("devices_found = %d", done.DevicesFound)
	}
	if done.InterfacesFound != 5 {
		t.Errorf("interfaces_found = %d", done.InterfacesFound)
	}
	if done.EdgesCreated != 3 {
		t.Errorf("edges_created = %d", done.EdgesCreated)
	}
}

// No observations means no reconciliation: an empty run must not touch the store.
func TestServiceSkipsReconcileOnEmptyRun(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 0))
	rec := &fakeReconciler{}
	s.WithReconciler(rec)

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitForJob(t, s, job.ID)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.calls != 0 {
		t.Errorf("reconciler ran %d times on an empty result set", rec.calls)
	}
}

func TestServiceReconcileFailureIsRecorded(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 2))
	s.WithReconciler(&fakeReconciler{err: errors.New("store is full")})

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	done := waitForJob(t, s, job.ID)
	if done.Error == "" {
		t.Error("the reconciliation failure was not recorded on the job")
	}
}

// fakePublisher records the SSE events the service emits.
type fakePublisher struct {
	mu     sync.Mutex
	events []string
}

func (f *fakePublisher) PublishDiscoveryStarted(jobID, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "started")
}

func (f *fakePublisher) PublishDiscoveryCompleted(jobID, status string, progress int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "completed:"+status)
}

func (f *fakePublisher) PublishTopologyUpdated(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "topology")
}

func TestServicePublishesEvents(t *testing.T) {
	bus := &fakePublisher{}
	s := NewService(serviceConfig(true), bus)
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 1))
	s.WithReconciler(&fakeReconciler{})

	job, err := s.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	waitForJob(t, s, job.ID)

	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.events) < 2 {
		t.Fatalf("events = %v", bus.events)
	}
	if bus.events[0] != "started" {
		t.Errorf("first event = %q", bus.events[0])
	}
	var sawCompleted bool
	for _, e := range bus.events {
		if e == "completed:completed" {
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Errorf("no completion event: %v", bus.events)
	}
}

// TestServiceScanReturnsPreRunSnapshot checks the snapshot Scan hands back.
//
// The returned snapshot is the contract of POST /api/v1/discovery/scan: a 202
// carrying the job as it was when the request was answered, which is why a
// caller may read status and progress off it. The run goroutine mutates the same
// record, so the snapshot has to be taken before that goroutine can touch it.
// This test hammers the window between "the run has been started" and "the
// snapshot was copied": a fast collector finishes in microseconds, so an
// unsynchronised copy is caught both by the assertion (the snapshot would report
// progress 100 and status completed) and by `-race` (the copy reads a field the
// run goroutine is writing).
func TestServiceScanReturnsPreRunSnapshot(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	// No delay: the run races the return path as hard as it can.
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 3))

	for i := 0; i < 200; i++ {
		// The job status flips to completed slightly before the run goroutine
		// clears the single-flight flag, so wait for the service to be idle
		// rather than for the job alone.
		waitForIdle(t, s)
		job, err := s.Scan(context.Background(), ScanRequest{})
		if err != nil {
			t.Fatalf("Scan %d: %v", i, err)
		}
		if job.Status != model.JobRunning {
			t.Fatalf("iteration %d: returned job status = %q, want %q: the snapshot must be the state at return, not a later one",
				i, job.Status, model.JobRunning)
		}
		if job.Progress != 0 {
			t.Fatalf("iteration %d: returned job progress = %d, want 0", i, job.Progress)
		}
		if !job.FinishedAt.IsZero() {
			t.Fatalf("iteration %d: returned job already has a finish time %v", i, job.FinishedAt)
		}
		if len(job.Collectors) != 0 {
			t.Fatalf("iteration %d: returned job already lists %d collector runs", i, len(job.Collectors))
		}
		waitForJob(t, s, job.ID)
	}
}

// waitForIdle blocks until no run is in flight.
func waitForIdle(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Status()
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if !st.Running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the service never became idle")
}

func TestServiceJobHistoryIsPruned(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 0))

	for i := 0; i < maxRetainedJobs+3; i++ {
		job, err := s.Scan(context.Background(), ScanRequest{})
		if err != nil {
			t.Fatalf("Scan %d: %v", i, err)
		}
		waitForJob(t, s, job.ID)
	}
	jobs := s.Jobs()
	if len(jobs) > maxRetainedJobs {
		t.Fatalf("retained %d jobs, want at most %d", len(jobs), maxRetainedJobs)
	}
	// Newest first.
	if len(jobs) > 1 && jobs[0].StartedAt.Before(jobs[1].StartedAt) {
		t.Error("job history is not newest-first")
	}
}

// fakeScheduler records the interval it was asked to use.
type fakeScheduler struct {
	mu       sync.Mutex
	interval time.Duration
	started  bool
}

func (f *fakeScheduler) Start(ctx context.Context, interval time.Duration, trigger func()) func() {
	f.mu.Lock()
	f.interval = interval
	f.started = true
	f.mu.Unlock()
	return func() {}
}

func TestServiceSchedulerStarts(t *testing.T) {
	s := NewService(serviceConfig(true), nil)
	s.Registry().Register(newServiceFake(model.CollectorARP, true, 0))
	sch := &fakeScheduler{}
	s.WithScheduler(sch)

	stop, err := s.StartScheduler(context.Background())
	if err != nil {
		t.Fatalf("StartScheduler: %v", err)
	}
	if stop == nil {
		t.Fatal("expected a stop function")
	}
	sch.mu.Lock()
	defer sch.mu.Unlock()
	if !sch.started {
		t.Fatal("scheduler was not started")
	}
	if sch.interval != 10*time.Minute {
		t.Errorf("interval = %v, want 10m", sch.interval)
	}
}

// With discovery disabled nothing is scheduled: that is what the flag means.
func TestServiceSchedulerDisabled(t *testing.T) {
	s := NewService(serviceConfig(false), nil)
	sch := &fakeScheduler{}
	s.WithScheduler(sch)

	if _, err := s.StartScheduler(context.Background()); err != nil {
		t.Fatalf("StartScheduler: %v", err)
	}
	sch.mu.Lock()
	defer sch.mu.Unlock()
	if sch.started {
		t.Fatal("the scheduler must not start when discovery is disabled")
	}
}

// A router that just restarted has no topology. The scheduler must scan once
// at startup instead of leaving the UI empty until the first tick.
func TestTickerSchedulerRunsImmediatelyOnStart(t *testing.T) {
	sch := &tickerScheduler{}
	started := make(chan struct{}, 1)
	stop := sch.StartWithImmediate(context.Background(), time.Hour, func() {}, func() {
		started <- struct{}{}
	})
	defer stop()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the startup scan never ran")
	}
}

// The immediate run must not replace the periodic one.
func TestTickerSchedulerImmediateRunDoesNotSuppressTicks(t *testing.T) {
	sch := &tickerScheduler{}
	var mu sync.Mutex
	var ticks int
	stop := sch.StartWithImmediate(context.Background(), 10*time.Millisecond, func() {
		mu.Lock()
		ticks++
		mu.Unlock()
	}, func() {})
	defer stop()

	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if ticks == 0 {
		t.Fatal("the periodic trigger never fired")
	}
}

func TestTickerSchedulerStops(t *testing.T) {
	sch := &tickerScheduler{}
	var mu sync.Mutex
	var ticks int
	stop := sch.Start(context.Background(), 10*time.Millisecond, func() {
		mu.Lock()
		ticks++
		mu.Unlock()
	})
	time.Sleep(60 * time.Millisecond)
	stop()
	mu.Lock()
	after := ticks
	mu.Unlock()
	if after == 0 {
		t.Fatal("the scheduler never fired")
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if ticks > after+1 {
		t.Errorf("the scheduler kept firing after stop: %d -> %d", after, ticks)
	}
}

func TestServiceDefaultsWithoutConfig(t *testing.T) {
	s := NewService(nil, nil)
	st, err := s.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	// config.Default() has discovery enabled with a 10m interval.
	if !st.Enabled {
		t.Error("expected the default to be enabled")
	}
	if st.Interval != "10m" {
		t.Errorf("interval = %q", st.Interval)
	}
}
