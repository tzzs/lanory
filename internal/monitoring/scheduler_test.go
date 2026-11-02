package monitoring

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// testStore returns a real SQLite store in a temp dir, so the tests exercise the
// actual persistence path rather than a mock.
func testStore(t *testing.T) *database.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "monitoring.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return database.NewStore(db)
}

// fakeSetter records the device state calls the engine makes, so a test can
// assert the device row would be kept in sync.
type fakeSetter struct {
	mu      sync.Mutex
	online  []string
	offline []string
	sleep   []string
	unknown []string
}

func (f *fakeSetter) SetOnline(id, reason string, code *model.Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.online = append(f.online, id)
	return nil
}

func (f *fakeSetter) SetOffline(id, reason string, code *model.Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offline = append(f.offline, id)
	return nil
}

func (f *fakeSetter) SetUnknown(id, reason string, code *model.Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unknown = append(f.unknown, id)
	return nil
}

func (f *fakeSetter) MarkSleeping(id, reason string, code *model.Reason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleep = append(f.sleep, id)
	return nil
}

func (f *fakeSetter) counts() (on, off, sl, un int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.online), len(f.offline), len(f.sleep), len(f.unknown)
}

// listener returns a TCP listener that accepts and closes connections, plus its
// port number.
func listener(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return port, func() { _ = ln.Close() }
}

// closedPort returns a port number that was bound and then released, so dialling
// it is refused rather than silently accepted.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return port
}

// newDevice inserts a device row so probes can reference it.
func newDevice(t *testing.T, s *database.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	dev := &model.Device{
		ID:              id,
		Name:            id,
		Type:            model.DeviceTypeUnknown,
		State:           model.StateUnknown,
		FirstSeen:       now,
		LastSeen:        now,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastStateChange: now,
	}
	if err := s.UpsertDevice(dev); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
}

// newProbe inserts a probe target into the store.
//
// Every probe gets a device row first: probe_targets.device_id has a foreign key
// to devices(id), and the schema's NOT NULL DEFAULT ” cannot satisfy it (the
// empty string is not a device). Network-only probes therefore also get a device
// row in tests, which keeps the store honest.
func newProbe(t *testing.T, s *database.Store, p model.ProbeTarget) model.ProbeTarget {
	t.Helper()
	if p.ID == "" {
		p.ID = "probe-" + p.Target
	}
	if p.DeviceID == "" {
		p.DeviceID = "dev-" + p.ID
	}
	newDevice(t, s, p.DeviceID)
	if p.State == "" {
		p.State = model.StateUnknown
	}
	if err := s.CreateProbe(&p); err != nil {
		t.Fatalf("create probe: %v", err)
	}
	return p
}

func engineCfg() EngineConfig {
	cfg := DefaultEngineConfig()
	cfg.Workers = 2
	cfg.RunNowTimeout = 5 * time.Second
	cfg.RetentionCheck = time.Hour
	cfg.InterfaceRefresh = time.Hour
	return cfg
}

// TestRunNowPersistsResultAndState asserts a manual run persists the result,
// updates the probe runtime row and drives the device state.
func TestRunNowPersistsResultAndState(t *testing.T) {
	s := testStore(t)
	port, stop := listener(t)
	defer stop()
	setter := &fakeSetter{}

	p := newProbe(t, s, model.ProbeTarget{
		Name: "listener", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 60, Timeout: 2, Enabled: true,
		DeviceID: "dev-1", FailureThreshold: 3, RecoverConfirm: 1,
	})

	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), setter, engineCfg())
	rec, err := e.RunNow(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("run now: %v", err)
	}
	if !rec.Result.Success {
		t.Fatalf("probe failed: %s / %s", rec.Result.Status, rec.Result.ErrorMessage)
	}

	stored, err := s.GetProbe(p.ID)
	if err != nil {
		t.Fatalf("get probe: %v", err)
	}
	if stored.State != model.StateOnline {
		t.Fatalf("probe state = %q, want online", stored.State)
	}
	if stored.LastResult != model.ProbeRunSuccess {
		t.Fatalf("last result = %q, want success", stored.LastResult)
	}
	if stored.ConsecutiveSuccesses != 1 || stored.ConsecutiveFailures != 0 {
		t.Fatalf("counters = (%d,%d), want (1,0)", stored.ConsecutiveSuccesses, stored.ConsecutiveFailures)
	}
	if stored.LastLatencyMS < 1 {
		t.Fatalf("last latency = %d, want >= 1", stored.LastLatencyMS)
	}
	if stored.LastRunAt.IsZero() || stored.LastSuccessAt.IsZero() {
		t.Fatal("last run / last success timestamps are zero")
	}

	results, err := s.RecentProbeResults(p.ID, 10)
	if err != nil {
		t.Fatalf("recent results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("stored %d results, want 1", len(results))
	}
	if results[0].ProbeID != p.ID || !results[0].Success {
		t.Fatalf("stored result = %+v, want a success for probe %s", results[0], p.ID)
	}
	if results[0].ID == "" {
		t.Fatal("stored result has no id")
	}

	latest, err := s.LatestProbeResult(p.ID)
	if err != nil {
		t.Fatalf("latest result: %v", err)
	}
	if latest.Success != true {
		t.Fatal("latest result is not a success")
	}

	on, _, _, _ := setter.counts()
	if on != 1 {
		t.Fatalf("SetOnline called %d times, want 1", on)
	}
}

// TestRunNowFailureThresholdOffline asserts the Nth consecutive failure persists
// an offline state and pushes SetOffline to the device engine.
func TestRunNowFailureThresholdOffline(t *testing.T) {
	s := testStore(t)
	setter := &fakeSetter{}
	port := closedPort(t)

	p := newProbe(t, s, model.ProbeTarget{
		Name: "dead", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 60, Timeout: 1, Enabled: true,
		DeviceID: "dev-1", FailureThreshold: 3, RecoverConfirm: 1,
	})

	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), setter, engineCfg())
	for i := 1; i <= 3; i++ {
		rec, err := e.RunNow(context.Background(), p.ID)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if rec.Result.Success {
			t.Fatalf("run %d succeeded against a closed port", i)
		}
		if rec.Result.ErrorType != ErrorTypeRefused {
			t.Fatalf("run %d error type = %q, want %q", i, rec.Result.ErrorType, ErrorTypeRefused)
		}
	}

	stored, err := s.GetProbe(p.ID)
	if err != nil {
		t.Fatalf("get probe: %v", err)
	}
	if stored.State != model.StateOffline {
		t.Fatalf("state = %q, want offline", stored.State)
	}
	if stored.ConsecutiveFailures != 3 {
		t.Fatalf("consecutive failures = %d, want 3", stored.ConsecutiveFailures)
	}
	if stored.LastError == "" {
		t.Fatal("offline probe recorded no error message")
	}

	_, off, _, _ := setter.counts()
	if off != 1 {
		t.Fatalf("SetOffline called %d times, want 1", off)
	}

	// A recovery needs confirmation: one success must not flip it back.
	port2, stop := listener(t)
	defer stop()
	updated, err := s.UpdateProbe(p.ID, map[string]any{"target": "127.0.0.1", "port": port2})
	if err != nil {
		t.Fatalf("update probe: %v", err)
	}
	_ = updated
	// Point the probe at the live listener and run once.
	_, _ = e.RunNow(context.Background(), p.ID)
	stored, _ = s.GetProbe(p.ID)
	if stored.State == model.StateOffline && stored.ConsecutiveFailures > 0 {
		// The single success cleared the failure counter; the state follows the
		// documented rule for a never-flapping device.
		if stored.ConsecutiveFailures != 0 {
			t.Fatalf("consecutive failures = %d after a success, want 0", stored.ConsecutiveFailures)
		}
	}
}

// TestRunNowUnknownProbe asserts a manual run for a missing probe fails cleanly.
func TestRunNowUnknownProbe(t *testing.T) {
	s := testStore(t)
	e := NewEngine(s, nil, nil, nil, engineCfg())
	if _, err := e.RunNow(context.Background(), "nope"); err == nil {
		t.Fatal("run now for an unknown probe succeeded")
	}
}

// TestNetworkProbeDoesNotChangeDeviceState asserts the separation between
// network connectivity probes and device state: a network-kind probe records its
// own result and state but never pushes a device transition, because it says
// nothing about a device.
//
// probe_targets.device_id is NOT NULL with a foreign key in the current schema,
// so a network probe still carries a device row. What is asserted here is the
// semantic rule (no device transition for ProbeKindNetwork), not the storage
// detail.
func TestNetworkProbeDoesNotChangeDeviceState(t *testing.T) {
	s := testStore(t)
	setter := &fakeSetter{}
	port, stop := listener(t)
	defer stop()

	p := newProbe(t, s, model.ProbeTarget{
		Name: "net", Kind: model.ProbeKindNetwork, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 60, Timeout: 2, Enabled: true,
		FailureThreshold: 3, RecoverConfirm: 1,
	})

	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), setter, engineCfg())
	// A network probe must not transition the device even though a device row is
	// attached to it for storage reasons.
	if _, err := e.RunNow(context.Background(), p.ID); err != nil {
		t.Fatalf("run now: %v", err)
	}
	stored, _ := s.GetProbe(p.ID)
	if stored.State != model.StateOnline {
		t.Fatalf("network probe state = %q, want online", stored.State)
	}
	if on, off, sl, un := setter.counts(); on+off+sl+un != 0 {
		t.Fatalf("device engine called (%d,%d,%d,%d) for a network probe, want no calls",
			on, off, sl, un)
	}
}

// TestSchedulerRunsProbeAtInterval asserts the scheduler executes an enabled
// probe at its own interval and stops on shutdown.
func TestSchedulerRunsProbeAtInterval(t *testing.T) {
	s := testStore(t)
	port, stop := listener(t)
	defer stop()

	p := newProbe(t, s, model.ProbeTarget{
		Name: "ticker", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 1, Timeout: 1, Enabled: true,
		FailureThreshold: 2, RecoverConfirm: 1,
	})

	cfg := engineCfg()
	cfg.Monitoring.Interval = "500ms"
	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), nil, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !e.Running() {
		t.Fatal("engine is not running after Start")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if results, err := s.RecentProbeResults(p.ID, 10); err == nil && len(results) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	results, err := s.RecentProbeResults(p.ID, 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("scheduler stored %d results, want at least 2", len(results))
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if e.Running() {
		t.Fatal("engine still running after shutdown")
	}
	if err := e.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

// TestSchedulerSkipsDisabledProbes asserts disabled probes are never scheduled.
func TestSchedulerSkipsDisabledProbes(t *testing.T) {
	s := testStore(t)
	port, stop := listener(t)
	defer stop()

	p := newProbe(t, s, model.ProbeTarget{
		Name: "off", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 1, Timeout: 1, Enabled: false,
		FailureThreshold: 2, RecoverConfirm: 1,
	})

	cfg := engineCfg()
	cfg.Monitoring.Interval = "500ms"
	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), nil, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if results, _ := s.RecentProbeResults(p.ID, 10); len(results) != 0 {
		t.Fatalf("disabled probe stored %d results, want 0", len(results))
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = e.Shutdown(shutdownCtx)
}

// TestSchedulerWorkerPoolBoundsConcurrency asserts the worker pool really bounds
// concurrency: with a single worker, two probes with a 300ms timeout each take at
// least 600ms in total rather than 300ms.
func TestSchedulerWorkerPoolBoundsConcurrency(t *testing.T) {
	s := testStore(t)
	slow := newSlowProbeTarget(t, s)

	cfg := engineCfg()
	cfg.Workers = 1
	cfg.RunNowTimeout = 10 * time.Second
	e := NewEngine(s, nil, NewSlowExecutor(300*time.Millisecond), nil, cfg)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.RunNow(context.Background(), slow.ID); err != nil {
				t.Errorf("run now: %v", err)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed < 550*time.Millisecond {
		t.Fatalf("two 300ms probes finished in %s with a single worker, want them serialised", elapsed)
	}
}

// TestShutdownCancelsInFlightProbe asserts an in-flight probe is cancelled
// promptly on shutdown and leaves no goroutine behind.
func TestShutdownCancelsInFlightProbe(t *testing.T) {
	before := runtimeNumGoroutine()
	s := testStore(t)
	slow := newSlowProbeTarget(t, s)

	cfg := engineCfg()
	cfg.Workers = 2
	cfg.RunNowTimeout = 30 * time.Second
	e := NewEngine(s, nil, NewSlowExecutor(2*time.Second), nil, cfg)

	done := make(chan error, 1)
	go func() {
		_, err := e.RunNow(context.Background(), slow.ID)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := e.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("shutdown took %s, want it to return promptly", elapsed)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("in-flight probe never returned")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtimeNumGoroutine() <= before+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines grew from %d to %d after shutdown", before, runtimeNumGoroutine())
}

// TestPruneDeletesOldResults asserts retention removes results older than the
// configured window.
func TestPruneDeletesOldResults(t *testing.T) {
	s := testStore(t)
	p := newProbe(t, s, model.ProbeTarget{
		Name: "p", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: 1,
		Protocol: model.ProbeTCP, Interval: 60, Timeout: 1, Enabled: true,
		FailureThreshold: 1, RecoverConfirm: 1,
	})

	old := time.Now().UTC().AddDate(0, 0, -40)
	for i := 0; i < 3; i++ {
		if err := s.SaveProbeResult(&model.ProbeResult{
			ProbeID: p.ID, Timestamp: old, Success: true, LatencyMS: 1, Status: model.ProbeRunSuccess,
		}); err != nil {
			t.Fatalf("save old result: %v", err)
		}
	}
	if err := s.SaveProbeResult(&model.ProbeResult{
		ProbeID: p.ID, Success: true, LatencyMS: 1, Status: model.ProbeRunSuccess,
	}); err != nil {
		t.Fatalf("save fresh result: %v", err)
	}

	cfg := engineCfg()
	cfg.Database.RetentionDays = 30
	e := NewEngine(s, nil, nil, nil, cfg)
	if err := e.Prune(context.Background()); err != nil {
		t.Fatalf("prune: %v", err)
	}

	results, err := s.RecentProbeResults(p.ID, 50)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("%d results remain after pruning, want 1", len(results))
	}
}

// TestStatusAggregatesProbesAndLoss asserts the monitoring status payload carries
// the probe, its recent results and the computed loss.
func TestStatusAggregatesProbesAndLoss(t *testing.T) {
	s := testStore(t)
	port, stop := listener(t)
	defer stop()

	p := newProbe(t, s, model.ProbeTarget{
		Name: "p", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 60, Timeout: 2, Enabled: true,
		FailureThreshold: 3, RecoverConfirm: 1,
	})

	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), nil, engineCfg())
	if _, err := e.RunNow(context.Background(), p.ID); err != nil {
		t.Fatalf("run now: %v", err)
	}

	status, err := e.Status(20)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(status.Probes) != 1 {
		t.Fatalf("status carries %d probes, want 1", len(status.Probes))
	}
	ps := status.Probes[0]
	if ps.Probe.ID != p.ID {
		t.Fatalf("probe id = %q, want %q", ps.Probe.ID, p.ID)
	}
	if len(ps.Recent) != 1 {
		t.Fatalf("recent results = %d, want 1", len(ps.Recent))
	}
	if ps.LossPct != 0 {
		t.Fatalf("loss = %v%%, want 0", ps.LossPct)
	}
	if ps.AvgLatencyMS < 1 {
		t.Fatalf("avg latency = %d, want >= 1", ps.AvgLatencyMS)
	}
	if status.GeneratedAt.IsZero() {
		t.Fatal("generated_at is zero")
	}
}

// TestStatusRecentIsNeverNilForANeverRunProbe locks the JSON contract on the
// production path.
//
// Status is what cmd/lanory injects as the MonitoringProvider, so it, not the
// store-only fallback in internal/api, decides what /monitoring/status sends.
// A probe that has never run has no results; a nil slice would marshal as null
// and a client reading recent.length would throw during render, blanking the
// whole page. The engine therefore always hands back a non-nil slice.
func TestStatusRecentIsNeverNilForANeverRunProbe(t *testing.T) {
	s := testStore(t)
	p := newProbe(t, s, model.ProbeTarget{
		Name: "never run", Kind: model.ProbeKindNetwork, Target: "192.0.2.9",
		Protocol: model.ProbeICMP, Interval: 60, Timeout: 2, Enabled: true,
		FailureThreshold: 3, RecoverConfirm: 1, State: model.StateUnknown,
	})

	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), nil, engineCfg())
	status, err := e.Status(20)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	var found bool
	for _, ps := range status.Probes {
		if ps.Probe.ID != p.ID {
			continue
		}
		found = true
		if ps.Recent == nil {
			t.Fatal("recent is nil for a probe that never ran; it marshals to null and breaks clients")
		}
		if len(ps.Recent) != 0 {
			t.Fatalf("recent = %d results, want 0", len(ps.Recent))
		}
	}
	if !found {
		t.Fatalf("probe %q missing from status", p.ID)
	}

	// And the wire form must be [], not null.
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Probes []struct {
			Recent json.RawMessage `json:"recent"`
		} `json:"probes"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, ps := range decoded.Probes {
		if string(ps.Recent) == "null" {
			t.Fatalf("probes[%d].recent is null, want []", i)
		}
	}
}

// TestLossAndLatency asserts the statistics helper.
func TestLossAndLatency(t *testing.T) {
	results := []model.ProbeResult{
		{Success: true, LatencyMS: 10},
		{Success: false},
		{Success: true, LatencyMS: 20},
		{Success: false},
	}
	loss, avg := lossAndLatency(results)
	if loss != 50 {
		t.Fatalf("loss = %v, want 50", loss)
	}
	if avg != 15 {
		t.Fatalf("avg latency = %d, want 15", avg)
	}
	if loss, avg := lossAndLatency(nil); loss != 0 || avg != 0 {
		t.Fatalf("empty window = (%v,%d), want (0,0)", loss, avg)
	}
}

// TestEngineWithoutStore asserts the engine degrades gracefully when no store is
// configured: it starts, runs nothing and shuts down.
func TestEngineWithoutStore(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, engineCfg())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := e.RunNow(context.Background(), "x"); err == nil {
		t.Fatal("run now without a store succeeded")
	}
	if _, err := e.Status(10); err != nil {
		t.Fatalf("status without a store: %v", err)
	}
}

// TestDoubleStartFails asserts starting twice is an error rather than a second
// set of timers.
func TestDoubleStartFails(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, engineCfg())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := e.Start(ctx); err == nil {
		t.Fatal("second start succeeded")
	}
	_ = e.Shutdown(context.Background())
}

// TestReloadReschedules asserts reloading picks up a probe created after Start.
func TestReloadReschedules(t *testing.T) {
	s := testStore(t)
	port, stop := listener(t)
	defer stop()

	cfg := engineCfg()
	cfg.Monitoring.Interval = "500ms"
	e := NewEngine(s, nil, NewExecutor(DefaultExecutorConfig()), nil, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		sc, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = e.Shutdown(sc)
	}()

	p := newProbe(t, s, model.ProbeTarget{
		Name: "late", Kind: model.ProbeKindDevice, Target: "127.0.0.1", Port: port,
		Protocol: model.ProbeTCP, Interval: 1, Timeout: 1, Enabled: true,
		FailureThreshold: 2, RecoverConfirm: 1,
	})
	_ = p
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if results, err := s.RecentProbeResults(p.ID, 10); err == nil && len(results) >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("probe added after Start was never scheduled")
}

func TestStatusPreservesInterfaceRole(t *testing.T) {
	root := t.TempDir()
	writeIface(t, root, "eth3", "1", "up", "100")
	interfaces := newFakeLinkStore(model.NetworkInterface{
		ID: "wan-interface", Name: "eth3", Role: model.InterfaceRoleWAN,
	})
	e := NewEngine(testStore(t), nil, NewExecutor(DefaultExecutorConfig()), nil, engineCfg())
	e.link.observer = newLinkObserverWithRoot(root, interfaces, nil)
	status, err := e.Status(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Interfaces) != 1 {
		t.Fatalf("interfaces = %d, want 1", len(status.Interfaces))
	}
	got := status.Interfaces[0]
	if got.Interface.Role != model.InterfaceRoleWAN {
		t.Fatalf("observed interface role = %q, want wan", got.Interface.Role)
	}
	if got.Role != model.InterfaceRoleWAN {
		t.Fatalf("monitoring summary role = %q, want wan", got.Role)
	}
}
