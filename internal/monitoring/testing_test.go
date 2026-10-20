package monitoring

import (
	"context"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// slowExecutor is a test Executor that takes delay to answer. It is used to
// assert the worker pool bounds concurrency and that shutdown cancels in-flight
// probes without needing a real network black hole.
type slowExecutor struct {
	delay   time.Duration
	success bool
}

// NewSlowExecutor returns an Executor whose every execution takes delay.
func NewSlowExecutor(delay time.Duration) *slowExecutor {
	return &slowExecutor{delay: delay, success: true}
}

func (s *slowExecutor) Execute(ctx context.Context, target model.ProbeTarget) ProbeRecord {
	rec := ProbeRecord{
		SourceID:    sourceOf(DefaultSourceID, target.SourceID),
		TargetValue: target.Target,
		Protocol:    target.Protocol,
		Result: model.ProbeResult{
			Timestamp: time.Now().UTC(),
			Success:   s.success,
			Status:    model.ProbeRunSuccess,
		},
	}
	select {
	case <-ctx.Done():
		rec.Result.Success = false
		rec.Result.Status = model.ProbeRunError
		rec.Result.ErrorType = ErrorTypeTimeout
		rec.Result.ErrorMessage = ctx.Err().Error()
	case <-time.After(s.delay):
	}
	rec.Result.LatencyMS = elapsedMS(rec.Result.Timestamp)
	return rec
}

func (s *slowExecutor) Protocol(p model.ProbeProtocol) bool {
	switch p {
	case model.ProbeICMP, model.ProbeTCP, model.ProbeHTTP, model.ProbeDNS:
		return true
	default:
		return false
	}
}

func (s *slowExecutor) Close() error { return nil }

// newSlowProbeTarget inserts a probe target that the slow executor will answer.
func newSlowProbeTarget(t interface {
	Helper()
	Fatalf(format string, args ...any)
}, s *database.Store) model.ProbeTarget {
	t.Helper()
	if err := s.UpsertDevice(&model.Device{
		ID:              "dev-slow",
		Name:            "slow device",
		Type:            model.DeviceTypeUnknown,
		State:           model.StateUnknown,
		FirstSeen:       time.Now().UTC(),
		LastSeen:        time.Now().UTC(),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		LastStateChange: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
	p := model.ProbeTarget{
		ID:               "probe-slow",
		Name:             "slow",
		Kind:             model.ProbeKindDevice,
		Target:           "127.0.0.1",
		Port:             1,
		Protocol:         model.ProbeTCP,
		Interval:         60,
		Timeout:          1,
		Enabled:          true,
		FailureThreshold: 3,
		RecoverConfirm:   1,
		State:            model.StateUnknown,
		DeviceID:         "dev-slow",
	}
	if err := s.CreateProbe(&p); err != nil {
		t.Fatalf("create slow probe: %v", err)
	}
	return p
}

// runtimeNumGoroutine is a thin indirection over runtime.NumGoroutine so the
// leak assertions read clearly at the call site.
func runtimeNumGoroutine() int { return runtime.NumGoroutine() }

// recordingPublisher counts the frames published on the bus, so a test can
// assert the SSE side effect without a real event bus.
type recordingPublisher struct {
	mu      sync.Mutex
	probes  int
	links   int
	online  int
	offline int
	updated int
}

// Subscribe returns an event channel. The recording publisher never emits, so
// the channel stays empty until it is closed; the tests that use it do not
// depend on the subscription.
func (r *recordingPublisher) Subscribe() (<-chan events.Event, func()) {
	ch := make(chan events.Event)
	return ch, func() { close(ch) }
}

func (r *recordingPublisher) PublishProbeCompleted(p *model.ProbeTarget, res *model.ProbeResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes++
}

func (r *recordingPublisher) PublishDeviceOnline(d *model.Device, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.online++
}

func (r *recordingPublisher) PublishDeviceOffline(d *model.Device, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.offline++
}

func (r *recordingPublisher) PublishDeviceUpdated(d *model.Device, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updated++
}

func (r *recordingPublisher) PublishLinkUpdated(e *model.TopologyEdge, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.links++
}

// startListener returns a live TCP listener, its port and a stop function.
func startListener(t *testing.T) (net.Listener, int, func()) {
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
	return ln, port, func() { _ = ln.Close() }
}

// fakeLinkStore is a minimal Store used to exercise the link observer's
// attribution and persistence logic without the SQLite store.
//
// It exists because database.Store cannot read or write network_interfaces in
// the current revision (its statements omit the SELECT keyword). The observer must
// be correct regardless, so its logic is tested against this stub and against
// the real store wherever the real store works.
type fakeLinkStore struct {
	Store
	mu         sync.Mutex
	interfaces []model.NetworkInterface
	events     []model.NetworkEvent
}

func newFakeLinkStore(ifaces ...model.NetworkInterface) *fakeLinkStore {
	return &fakeLinkStore{interfaces: ifaces}
}

func (f *fakeLinkStore) ListAllInterfaces() ([]model.NetworkInterface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.NetworkInterface, len(f.interfaces))
	copy(out, f.interfaces)
	return out, nil
}

func (f *fakeLinkStore) GetInterfaceByName(deviceID, name string) (*model.NetworkInterface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.interfaces {
		if f.interfaces[i].DeviceID == deviceID && f.interfaces[i].Name == name {
			out := f.interfaces[i]
			return &out, nil
		}
	}
	return nil, database.ErrNotFound
}

func (f *fakeLinkStore) SaveEvent(e *model.NetworkEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, *e)
	return nil
}

func (f *fakeLinkStore) savedEvents() []model.NetworkEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.NetworkEvent, len(f.events))
	copy(out, f.events)
	return out
}
