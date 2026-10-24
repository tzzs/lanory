package monitoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// Engine is the probe scheduler.
//
// It owns the periodic execution of every enabled probe, the worker pool that
// bounds concurrency (a soft router CPU must never be saturated), the state
// machine that turns outcomes into device states, and the persistence of
// results and events.
//
// Lifecycle: NewEngine -> Start -> ... -> Shutdown. Shutdown is idempotent,
// cancels in-flight probes and waits for them: probes already handed to a
// worker either finish or observe the cancelled context and return promptly, so
// no goroutine outlives the call.
type Engine struct {
	store    Store
	bus      Publisher
	executor Executor
	states   *StateMachine
	cfg      EngineConfig

	mu      sync.Mutex
	jobs    map[string]*probeJob
	running bool
	// lifeCtx is cancelled by Shutdown. It is the parent of the scheduling
	// context and of every RunNow call, so a shutdown also aborts manual runs.
	lifeCtx    context.Context
	lifeCancel context.CancelFunc
	stopLink   func() bool
	wg         sync.WaitGroup

	// sem bounds the number of probes executing at once.
	sem chan struct{}

	// internet holds the latest connectivity checks.
	internet internetState

	// link holds the engine's interface observer. It is created lazily and kept
	// for the life of the engine so transitions are detected across polls.
	link struct {
		mu       sync.Mutex
		observer *LinkObserver
	}
}

// EngineConfig configures the scheduler.
type EngineConfig struct {
	// Monitoring mirrors config.MonitoringConfig.
	Monitoring config.MonitoringConfig
	// Database mirrors config.DatabaseConfig, used for retention.
	Database config.DatabaseConfig
	// Workers is the size of the probe worker pool. Zero or negative means a
	// conservative default.
	Workers int
	// RunNowTimeout bounds a manual "run now" request.
	RunNowTimeout time.Duration
	// RetentionCheck controls how often old results are pruned.
	RetentionCheck time.Duration
	// InterfaceRefresh controls how often interface state is polled.
	InterfaceRefresh time.Duration
	// AutoProbe controls automatic probe provisioning for discovered devices.
	// The zero value disables it; DefaultEngineConfig enables it.
	AutoProbe AutoProbeConfig
}

// DefaultEngineConfig returns the built-in defaults.
func DefaultEngineConfig() EngineConfig {
	def := config.Default()
	return EngineConfig{
		Monitoring:       def.Monitoring,
		Database:         def.Database,
		Workers:          4,
		RunNowTimeout:    10 * time.Second,
		RetentionCheck:   time.Hour,
		InterfaceRefresh: 30 * time.Second,
		AutoProbe:        DefaultAutoProbeConfig(),
	}
}

// Store is the subset of internal/database.Store the engine persists through.
//
// It is declared here so the probe engine stays decoupled from the database
// package and tests can substitute a fake. *database.Store satisfies it as is.
// The one exception is EventFilter: internal/database owns that type, so it is
// re-exported below rather than duplicated (a structural copy would not satisfy
// the interface).
type Store interface {
	ListEnabledProbes() ([]model.ProbeTarget, error)
	ListProbes() ([]model.ProbeTarget, error)
	GetProbe(id string) (*model.ProbeTarget, error)
	UpdateProbeRuntime(p *model.ProbeTarget) error
	SaveProbeResult(r *model.ProbeResult) error
	RecentProbeResults(probeID string, limit int) ([]model.ProbeResult, error)
	LatestProbeResult(probeID string) (*model.ProbeResult, error)
	ListAllInterfaces() ([]model.NetworkInterface, error)
	GetInterfaceByName(deviceID, name string) (*model.NetworkInterface, error)
	UpsertInterface(i *model.NetworkInterface) error
	SaveEvent(e *model.NetworkEvent) error
	ListEvents(f database.EventFilter) ([]model.NetworkEvent, int, error)
	DeleteOldProbeResults(olderThan time.Time) (int64, error)
}

// EventFilter re-exports internal/database.EventFilter so callers can query the
// history through the engine without importing the database package.
type EventFilter = database.EventFilter

// Publisher is the subset of internal/events.Bus the engine publishes through.
// *events.Bus satisfies it as is.
type Publisher interface {
	PublishProbeCompleted(p *model.ProbeTarget, r *model.ProbeResult)
	PublishDeviceOnline(d *model.Device, reason string)
	PublishDeviceOffline(d *model.Device, reason string)
	PublishDeviceUpdated(d *model.Device, reason string)
	PublishLinkUpdated(e *model.TopologyEdge, reason string)
	// Subscribe delivers events to the engine. It is part of the interface
	// because the event bus always provides it, and the engine uses it to react
	// to discovery instead of waiting for the next interface-refresh tick:
	// without it a device found by a scan waited up to 30 seconds before its
	// probe was created, so the monitoring page showed it as "unknown" for
	// that long.
	Subscribe() (<-chan events.Event, func())
}

// probeJob tracks one scheduled probe.
type probeJob struct {
	target model.ProbeTarget
	timer  *time.Timer
}

// NewEngine returns a probe engine.
//
// store, bus and setter may each be nil, in which case the corresponding side
// effect is skipped (useful in unit tests and for network-only probes that have
// no device behind them).
func NewEngine(store Store, bus Publisher, executor Executor, setter DeviceStateSetter, cfg EngineConfig) *Engine {
	if executor == nil {
		executor = NewExecutor(DefaultExecutorConfig())
	}
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &Engine{
		store:      store,
		bus:        bus,
		executor:   executor,
		states:     NewStateMachine(setter).WithPresence(NewARPObserver()),
		cfg:        cfg,
		jobs:       map[string]*probeJob{},
		sem:        make(chan struct{}, workerCount(cfg.Workers)),
		internet:   internetState{targets: map[string]*InternetTarget{}},
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
	}
}

// workerCount returns the size of the worker pool.
func workerCount(n int) int {
	if n > 0 {
		return n
	}
	return 4
}

// StateMachine exposes the engine's device state machine, so callers (and the
// API) can explain the current state of a probe.
func (e *Engine) StateMachine() *StateMachine { return e.states }

// Start begins scheduling the enabled probes.
//
// The context passed here controls the whole lifetime of the engine: cancelling
// it is equivalent to calling Shutdown with that context.
func (e *Engine) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("monitoring: engine already running")
	}
	if e.lifeCtx == nil || e.lifeCtx.Err() != nil {
		e.mu.Unlock()
		return fmt.Errorf("monitoring: engine was shut down and cannot be restarted")
	}
	e.running = true
	runCtx, cancel := context.WithCancel(e.lifeCtx)
	// Link the caller's context to the engine lifetime as well, so cancelling
	// either one stops the scheduler.
	stop := context.AfterFunc(ctx, cancel)
	e.stopLink = stop
	e.mu.Unlock()

	if err := e.reload(runCtx); err != nil {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
		stop()
		cancel()
		return err
	}

	// Provision probes for devices discovery already found before the first
	// tick, so a freshly installed service starts monitoring immediately instead
	// of leaving every device "unknown" for a whole interface-refresh interval.
	if _, err := e.EnsureProbes(runCtx, e.cfg.AutoProbe); err == nil {
		_ = e.reload(runCtx)
	}

	e.wg.Add(4)
	go e.retentionLoop(runCtx)
	go e.interfaceLoop(runCtx)
	go e.linkRefreshLoop(runCtx)
	go e.discoveryLoop(runCtx)
	return nil
}

// Running reports whether the engine is started.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// Shutdown stops the scheduler and waits for in-flight probes. It is idempotent
// and safe to call on an engine that was never started.
//
// An engine is single use: after Shutdown it cannot be started again. Create a
// new Engine instead. That keeps the lifetime rules simple and stops a restart
// from racing with the goroutines the previous run is still draining.
func (e *Engine) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	if e.lifeCancel != nil {
		// Cancel the engine lifetime even if Start was never called: an
		// in-flight manual run must still be aborted.
		e.lifeCancel()
		e.lifeCancel = nil
	}
	if !e.running {
		e.mu.Unlock()
		return nil
	}
	e.running = false
	if e.stopLink != nil {
		e.stopLink()
		e.stopLink = nil
	}
	jobs := make([]*probeJob, 0, len(e.jobs))
	for _, j := range e.jobs {
		jobs = append(jobs, j)
	}
	e.jobs = map[string]*probeJob{}
	e.mu.Unlock()

	for _, j := range jobs {
		if j.timer != nil {
			j.timer.Stop()
		}
	}

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reload re-reads the enabled probes from the store and reschedules them. The
// API calls it after a probe is created, updated or deleted.
func (e *Engine) Reload(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	running := e.running
	lifeCtx := e.lifeCtx
	e.mu.Unlock()
	if !running || lifeCtx == nil {
		return nil
	}
	return e.reload(lifeCtx)
}

// reload schedules the enabled probes, cancelling the timers of any probe that
// disappeared or was disabled.
func (e *Engine) reload(ctx context.Context) error {
	if e.store == nil {
		return nil
	}
	probes, err := e.store.ListEnabledProbes()
	if err != nil {
		return fmt.Errorf("monitoring: list probes: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.jobs == nil {
		e.jobs = map[string]*probeJob{}
	}
	keep := make(map[string]struct{}, len(probes))
	for i := range probes {
		p := probes[i]
		keep[p.ID] = struct{}{}
		if existing, ok := e.jobs[p.ID]; ok {
			if existing.target.Interval == p.Interval {
				existing.target = p
				continue
			}
			if existing.timer != nil {
				existing.timer.Stop()
			}
		}
		job := &probeJob{target: p}
		interval := probeInterval(p, e.cfg.Monitoring)
		target := p
		job.timer = time.AfterFunc(interval, func() { e.dispatch(ctx, target) })
		e.jobs[p.ID] = job
	}
	for id, job := range e.jobs {
		if _, ok := keep[id]; ok {
			continue
		}
		if job.timer != nil {
			job.timer.Stop()
		}
		delete(e.jobs, id)
	}
	return nil
}

// dispatch runs one scheduled probe inside the worker pool and then re-arms the
// probe's timer, so a probe keeps running at its own interval for as long as the
// engine is running.
//
// When the pool is saturated the run is skipped rather than queued: the probe
// will be retried on its next tick, and a soft router is never flooded with
// backlogged work.
func (e *Engine) dispatch(ctx context.Context, target model.ProbeTarget) {
	if ctx.Err() != nil {
		return
	}
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return
	default:
		e.scheduleNext(ctx, target)
		return
	}
	defer func() { <-e.sem }()

	e.wg.Add(1)
	defer e.wg.Done()
	_, _, _ = e.runProbe(ctx, target)
	e.scheduleNext(ctx, target)
}

// scheduleNext re-arms the timer of a probe, unless the engine has been shut down
// or the probe has since been removed or rescheduled.
func (e *Engine) scheduleNext(ctx context.Context, target model.ProbeTarget) {
	if ctx.Err() != nil {
		return
	}
	interval := probeInterval(target, e.cfg.Monitoring)
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobs[target.ID]
	if !ok || !e.running {
		return
	}
	job.timer = time.AfterFunc(interval, func() { e.dispatch(ctx, target) })
}

// interfaceLoop polls the interface link state at the configured interval.
//
// The observer is created once and kept for the life of the engine, because
// transition detection compares each sample with the previous one: a fresh
// observer per poll would never see a change.
func (e *Engine) interfaceLoop(ctx context.Context) {
	defer e.wg.Done()

	every := e.cfg.InterfaceRefresh
	if every <= 0 {
		every = 30 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.observeLinks()
			e.refreshPresence()
			e.provisionProbes(ctx)
		}
	}
}

// discoveryLoop provisions probes as soon as discovery reports new devices.
//
// It subscribes to the event bus rather than polling, so a device found by a
// scan starts being monitored within a second instead of at the next
// interface-refresh tick. The subscription is optional: a bus that cannot be
// subscribed to simply leaves the periodic path in charge.
func (e *Engine) discoveryLoop(ctx context.Context) {
	defer e.wg.Done()

	if e.bus == nil {
		// No event bus: the periodic provisioning still runs.
		<-ctx.Done()
		return
	}
	ch, cancel := e.bus.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// A topology change means devices may have been added.
			if ev.Type != "topology.updated" && ev.Type != "device.updated" {
				continue
			}
			e.provisionProbes(ctx)
		}
	}
}

// refreshPresence re-reads the neighbour table.
//
// The table is the cross-check that tells "the device is gone" from "the device
// does not answer this probe method", so it has to be current when the state
// machine decides.
func (e *Engine) refreshPresence() {
	observer, ok := e.states.presence.(*ARPObserver)
	if !ok || observer == nil {
		return
	}
	if _, err := observer.Refresh(context.Background()); err != nil {
		// The neighbour table is not available everywhere; the state machine
		// then simply has no cross-check and falls back to the probe outcome.
		return
	}
}

// provisionProbes creates probes for devices that do not have one yet, then
// reloads the schedule so they start running.
//
// Without this a discovered device stays in the "unknown" state forever: the
// probe engine only runs probes that already exist, and nothing in the discovery
// path was creating them. That made the monitoring page empty on a fresh
// install even though seventeen devices had been found.
func (e *Engine) provisionProbes(ctx context.Context) {
	created, err := e.EnsureProbes(ctx, e.cfg.AutoProbe)
	if err != nil || created == 0 {
		return
	}
	// New rows must be picked up by the scheduler.
	_ = e.reload(ctx)
}

// linkRefreshLoop performs the first interface observation shortly after Start so
// the API has link state to return immediately.
func (e *Engine) linkRefreshLoop(ctx context.Context) {
	defer e.wg.Done()
	select {
	case <-ctx.Done():
		return
	default:
	}
	e.observeLinks()
}

// observeLinks runs one interface observation through the engine's observer.
func (e *Engine) observeLinks() {
	e.link.mu.Lock()
	if e.link.observer == nil {
		e.link.observer = NewLinkObserver(e.store, e.bus)
	}
	observer := e.link.observer
	e.link.mu.Unlock()
	_, _ = observer.Observe()
}

// LinkStatus returns the current link health of every interface, computing a
// transition-aware observation on demand. Used by GET /monitoring/status.
func (e *Engine) LinkStatus() ([]LinkStatus, error) {
	e.link.mu.Lock()
	if e.link.observer == nil {
		e.link.observer = NewLinkObserver(e.store, e.bus)
	}
	observer := e.link.observer
	e.link.mu.Unlock()
	return observer.Observe()
}

// retentionLoop prunes probe results beyond the configured retention window.
func (e *Engine) retentionLoop(ctx context.Context) {
	defer e.wg.Done()
	every := e.cfg.RetentionCheck
	if every <= 0 {
		every = time.Hour
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = e.Prune(ctx)
		}
	}
}

// Prune deletes probe results older than the retention window. Retention is best
// effort: a failure is returned to the caller but never stops monitoring.
func (e *Engine) Prune(ctx context.Context) error {
	if e.store == nil {
		return nil
	}
	days := e.cfg.Database.RetentionDays
	if days <= 0 {
		return nil
	}
	if _, err := e.store.DeleteOldProbeResults(time.Now().UTC().AddDate(0, 0, -days)); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("monitoring: prune probe results: %w", err)
	}
	return nil
}

// RunNow executes a probe immediately. It is the implementation behind
// POST /monitoring/probes/{id}/run and returns the record of the run.
//
// A manual run works whether or not the engine is started. When the engine is
// running the run goes through the same bounded worker pool, so a burst of
// manual runs cannot saturate the router either.
func (e *Engine) RunNow(ctx context.Context, probeID string) (ProbeRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := e.cfg.RunNowTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	e.mu.Lock()
	lifeCtx := e.lifeCtx
	e.mu.Unlock()
	if lifeCtx == nil {
		return ProbeRecord{}, fmt.Errorf("monitoring: engine is shut down")
	}
	// Derive from the engine lifetime so Shutdown aborts a manual run too.
	parent, cancelParent := context.WithCancel(lifeCtx)
	defer cancelParent()
	stop := context.AfterFunc(ctx, cancelParent)
	defer stop()

	runCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	target, err := e.probeTarget(probeID)
	if err != nil {
		return ProbeRecord{}, err
	}

	select {
	case e.sem <- struct{}{}:
	case <-runCtx.Done():
		return ProbeRecord{}, runCtx.Err()
	}
	defer func() { <-e.sem }()

	rec, _, err := e.runProbe(runCtx, *target)
	return rec, err
}

// probeTarget loads a probe target from the store.
func (e *Engine) probeTarget(probeID string) (*model.ProbeTarget, error) {
	if e.store == nil {
		return nil, fmt.Errorf("monitoring: no store configured")
	}
	target, err := e.store.GetProbe(probeID)
	if err != nil {
		return nil, fmt.Errorf("monitoring: get probe %s: %w", probeID, err)
	}
	return target, nil
}

// runProbe executes one probe, persists everything it produced and returns the
// record.
func (e *Engine) runProbe(ctx context.Context, target model.ProbeTarget) (ProbeRecord, model.ProbeTarget, error) {
	if target.Protocol != model.ProbeICMP && !e.executor.Protocol(target.Protocol) {
		return ProbeRecord{}, target, fmt.Errorf("monitoring: protocol %s is not available on this host", string(target.Protocol))
	}

	rec := e.executor.Execute(ctx, target)
	rec.Result.ProbeID = target.ID
	e.finish(target, rec)
	return rec, target, nil
}

// finish persists the result, updates the probe runtime row, applies the state
// transition and publishes the resulting events.
func (e *Engine) finish(target model.ProbeTarget, rec ProbeRecord) {
	if e.store != nil {
		result := rec.Result
		if err := e.store.SaveProbeResult(&result); err == nil {
			rec.Result.ID = result.ID
		}
	}

	transition := e.states.Apply(target, rec.Result.Success)
	failures, successes := e.states.counters(target.ID)

	target.State = transition.To
	target.StateReason = transition.Reason
	target.ConsecutiveFailures = failures
	target.ConsecutiveSuccesses = successes
	target.LastResult = rec.Result.Status
	target.LastLatencyMS = rec.Result.LatencyMS
	target.LastError = rec.Result.ErrorMessage
	target.LastRunAt = rec.Result.Timestamp
	if rec.Result.Success {
		target.LastSuccessAt = rec.Result.Timestamp
	}
	if e.store != nil {
		_ = e.store.UpdateProbeRuntime(&target)
	}

	e.applyTransition(target, rec, transition)

	if e.bus != nil {
		e.bus.PublishProbeCompleted(&target, &rec.Result)
	}
}

// applyTransition pushes a device state change to the device engine, the event
// store and the SSE bus.
//
// The device engine already publishes device.online / device.offline and its own
// device.updated frame, so this method only adds the history record and the
// sleeping / unknown transitions that engine does not publish.
func (e *Engine) applyTransition(target model.ProbeTarget, rec ProbeRecord, tr StateTransition) {
	if target.Kind == model.ProbeKindNetwork {
		// A network connectivity probe says nothing about a device, so it must
		// never move a device between online and offline. Its own state lives on
		// the probe row, which finish() has already updated.
		return
	}
	if target.DeviceID == "" {
		// No device behind the probe: nothing to keep in sync.
		return
	}
	if tr.From == tr.To {
		return
	}

	reason := fmt.Sprintf("%s (detected by %s probe from %s)", tr.Reason, tr.Method, tr.Source)
	if setter := e.states.Setter(); setter != nil {
		switch tr.To {
		case model.StateOnline:
			_ = setter.SetOnline(target.DeviceID, reason)
		case model.StateOffline:
			_ = setter.SetOffline(target.DeviceID, reason)
		case model.StateSleeping:
			_ = setter.MarkSleeping(target.DeviceID, reason)
		default:
			_ = setter.SetUnknown(target.DeviceID, reason)
		}
	}

	// device.online / device.offline are recorded by the device engine itself;
	// sleeping and unknown transitions have no event type of their own, so they
	// are recorded here with the device.updated envelope to keep the history
	// complete and explainable.
	var eventType model.EventType
	switch tr.To {
	case model.StateOnline, model.StateOffline:
		return
	default:
		eventType = model.EventDeviceUpdated
	}
	if e.store != nil {
		_ = e.store.SaveEvent(&model.NetworkEvent{
			EventType: eventType,
			DeviceID:  target.DeviceID,
			OldValue:  string(tr.From),
			NewValue:  string(tr.To),
			Message:   reason,
			Evidence: fmt.Sprintf("probe=%s protocol=%s latency=%dms source=%s",
				target.ID, string(rec.Protocol), rec.Result.LatencyMS, tr.Source),
			Timestamp: rec.Result.Timestamp,
		})
	}
}

// Status returns the monitoring status payload: every probe with its recent
// results and loss statistics, plus the current interface health.
func (e *Engine) Status(recentLimit int) (model.MonitoringStatus, error) {
	out := model.MonitoringStatus{GeneratedAt: time.Now().UTC()}
	if e.store == nil {
		return out, nil
	}
	probes, err := e.store.ListProbes()
	if err != nil {
		return out, fmt.Errorf("monitoring: list probes: %w", err)
	}
	for _, p := range probes {
		// Recent is never nil. It is serialised straight to JSON, where a nil
		// slice becomes null; a probe that has never run has no results, and a
		// client that reads recent.length would throw on null and take the
		// whole page down.
		ps := model.ProbeStatus{Probe: p, Recent: []model.ProbeResult{}}
		if recent, err := e.store.RecentProbeResults(p.ID, recentLimit); err == nil {
			if recent != nil {
				ps.Recent = recent
			}
			ps.LossPct, ps.AvgLatencyMS = lossAndLatency(recent)
		}
		out.Probes = append(out.Probes, ps)
	}

	statuses, err := e.LinkStatus()
	if err != nil {
		return out, nil
	}
	for _, s := range statuses {
		out.Interfaces = append(out.Interfaces, model.InterfaceStatus{
			Interface: s.Interface,
			Role:      s.Interface.Role,
			Status:    s.Status,
			Reason:    s.Reason,
		})
	}
	return out, nil
}

// lossAndLatency computes the packet loss percentage and average latency of a
// result window. Results are expected oldest-first; any order works because the
// computation is order independent.
func lossAndLatency(results []model.ProbeResult) (float64, int) {
	if len(results) == 0 {
		return 0, 0
	}
	var (
		failed   int
		totalMS  int
		answered int
	)
	for _, r := range results {
		if r.Success {
			answered++
			totalMS += r.LatencyMS
		} else {
			failed++
		}
	}
	loss := float64(failed) / float64(len(results)) * 100
	avg := 0
	if answered > 0 {
		avg = totalMS / answered
	}
	return loss, avg
}

// probeInterval returns the scheduling interval of a probe.
func probeInterval(p model.ProbeTarget, cfg config.MonitoringConfig) time.Duration {
	if p.Interval > 0 {
		return time.Duration(p.Interval) * time.Second
	}
	if d, err := time.ParseDuration(cfg.Interval); err == nil && d > 0 {
		return d
	}
	return 60 * time.Second
}
