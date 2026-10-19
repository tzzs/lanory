package discovery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/model"
)

// fakeCollector is a controllable collector for the registry tests.
type fakeCollector struct {
	*BaseCollector
	name        string
	results     []Result
	err         error
	panics      bool
	delay       time.Duration
	supported   bool
	calls       atomic.Int32
	concurrent  atomic.Int32
	maxSeen     atomic.Int32
	observedCtx atomic.Value
}

func newFake(name string, supported bool) *fakeCollector {
	return &fakeCollector{
		BaseCollector: NewBaseCollector(name, "test collector", supported, CapMAC),
		name:          name,
		supported:     supported,
	}
}

func (f *fakeCollector) Name() string { return f.name }

func (f *fakeCollector) Supported() bool { return f.supported }

func (f *fakeCollector) Collect(ctx context.Context) ([]Result, error) {
	f.calls.Add(1)
	n := f.concurrent.Add(1)
	defer f.concurrent.Add(-1)
	for {
		max := f.maxSeen.Load()
		if n <= max || f.maxSeen.CompareAndSwap(max, n) {
			break
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.panics {
		panic("boom")
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

func testConfig(maxConcurrency int) *config.Config {
	cfg := config.Default()
	cfg.Discovery.MaxConcurrency = maxConcurrency
	cfg.Collectors = config.CollectorsConfig{
		OpenWrt: true, ARP: true, NDP: true, DHCP: true, FDB: true, LLDP: true, MDNS: true,
	}
	return cfg
}

func TestRegistryRegistersAndSorts(t *testing.T) {
	r := NewRegistry(testConfig(4))
	r.RegisterAll(newFake("zeta", true), newFake("alpha", true), newFake("mike", true))
	names := r.Names()
	want := []string{"alpha", "mike", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("names = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

func TestRegistryRegisterReplacesDuplicate(t *testing.T) {
	r := NewRegistry(testConfig(4))
	first := newFake("dup", true)
	second := newFake("dup", true)
	r.Register(first)
	r.Register(second)
	if len(r.Collectors()) != 1 {
		t.Fatalf("expected 1 collector, got %d", len(r.Collectors()))
	}
	r.Run(context.Background())
	if second.calls.Load() != 1 || first.calls.Load() != 0 {
		t.Fatalf("replacement was not used: first=%d second=%d", first.calls.Load(), second.calls.Load())
	}
}

func TestRegistrySkipsUnsupported(t *testing.T) {
	r := NewRegistry(testConfig(4))
	supported := newFake("supported", true)
	unsupported := newFake("unsupported", false)
	r.RegisterAll(supported, unsupported)

	summary := r.Run(context.Background())
	if summary.Count("supported") != 0 {
		// The fake returns no results; the call counter is what matters.
	}
	if supported.calls.Load() != 1 {
		t.Errorf("supported collector was not run")
	}
	if unsupported.calls.Load() != 0 {
		t.Errorf("unsupported collector must not run")
	}
	if len(summary.Collectors) != 1 {
		t.Fatalf("expected 1 collector in the summary, got %d", len(summary.Collectors))
	}
}

func TestRegistrySkipsDisabled(t *testing.T) {
	cfg := testConfig(4)
	cfg.Collectors.ARP = false
	r := NewRegistry(cfg)
	arp := newFake(model.CollectorARP, true)
	r.Register(arp)

	r.Run(context.Background())
	if arp.calls.Load() != 0 {
		t.Fatal("a disabled collector must not run")
	}

	// An explicit request for a disabled collector DOES run it: the caller
	// asked for it by name.
	r.Run(context.Background(), model.CollectorARP)
	if arp.calls.Load() != 1 {
		t.Fatal("an explicitly requested collector must run even when disabled")
	}
}

func TestRegistryOneFailureDoesNotAbortOthers(t *testing.T) {
	r := NewRegistry(testConfig(4))
	broken := newFake("broken", true)
	broken.err = errors.New("kaboom")
	panicky := newFake("panicky", true)
	panicky.panics = true
	good := newFake("good", true)
	good.results = []Result{*NewResult(ResultGeneric, model.SourceARP, time.Now())}

	r.RegisterAll(broken, panicky, good)
	summary := r.Run(context.Background())

	if len(summary.Collectors) != 3 {
		t.Fatalf("expected 3 collector runs, got %d", len(summary.Collectors))
	}
	if !summary.Failed() {
		t.Fatal("expected the summary to report failures")
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected the healthy collector's result to survive, got %d", len(summary.Results))
	}
	if summary.Count("good") != 1 {
		t.Fatalf("good collector results = %d", summary.Count("good"))
	}

	errs := summary.Errors()
	if errs["broken"] == nil {
		t.Error("broken collector error missing")
	}
	if errs["panicky"] == nil {
		t.Error("panic must surface as an error")
	}
	if errs["good"] != nil {
		t.Error("good collector must not have an error")
	}

	// The panic must be contained, not propagated.
	for _, c := range summary.Collectors {
		if c.Name == "panicky" && c.Status != model.JobFailed {
			t.Errorf("panicky status = %q, want failed", c.Status)
		}
	}
}

func TestRegistryBoundedConcurrency(t *testing.T) {
	cfg := testConfig(4)
	cfg.Discovery.MaxConcurrency = 2
	r := NewRegistry(cfg)

	var fakes []*fakeCollector
	for i := 0; i < 10; i++ {
		f := newFake(string(rune('a'+i)), true)
		f.delay = 10 * time.Millisecond
		fakes = append(fakes, f)
		r.Register(f)
	}

	summary := r.Run(context.Background())
	if len(summary.Collectors) != 10 {
		t.Fatalf("expected 10 collector runs, got %d", len(summary.Collectors))
	}
	var maxSeen int32
	for _, f := range fakes {
		if v := f.maxSeen.Load(); v > maxSeen {
			maxSeen = v
		}
	}
	if maxSeen > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", maxSeen)
	}
	if maxSeen < 2 {
		t.Logf("note: observed concurrency %d (scheduling may serialise)", maxSeen)
	}
}

func TestRegistryCancellation(t *testing.T) {
	r := NewRegistry(testConfig(4))
	slow := newFake("slow", true)
	slow.delay = 5 * time.Second
	r.Register(slow)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	summary := r.Run(ctx)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s; the run must abort promptly", elapsed)
	}
	if !summary.Cancelled {
		t.Fatal("expected the summary to be marked cancelled")
	}
	if summary.Collectors[0].Status != model.JobCancelled {
		t.Fatalf("status = %q, want cancelled", summary.Collectors[0].Status)
	}
}

func TestRegistryEmptySelection(t *testing.T) {
	r := NewRegistry(testConfig(4))
	summary := r.Run(context.Background())
	if summary == nil {
		t.Fatal("nil summary")
	}
	if len(summary.Results) != 0 || len(summary.Collectors) != 0 {
		t.Fatalf("expected an empty run, got %+v", summary)
	}
	if summary.Failed() {
		t.Fatal("an empty run must not be marked failed")
	}
}

func TestRegistryStatusReportsLastRun(t *testing.T) {
	r := NewRegistry(testConfig(4))
	good := newFake("good", true)
	good.results = []Result{
		*NewResult(ResultGeneric, model.SourceARP, time.Now()),
		*NewResult(ResultGeneric, model.SourceARP, time.Now()),
	}
	bad := newFake("bad", true)
	bad.err = errors.New("nope")
	r.RegisterAll(good, bad)

	r.Run(context.Background())

	status := r.Status()
	if len(status) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(status))
	}
	byName := map[string]model.CollectorStatus{}
	for _, s := range status {
		byName[s.Name] = s
	}

	if byName["good"].Records != 2 {
		t.Errorf("good records = %d, want 2", byName["good"].Records)
	}
	if byName["good"].LastRunAt.IsZero() {
		t.Error("good LastRunAt not recorded")
	}
	if byName["good"].LastError != "" {
		t.Errorf("good LastError = %q", byName["good"].LastError)
	}
	if byName["bad"].LastError == "" {
		t.Error("bad LastError not recorded")
	}
	if !byName["good"].Supported || !byName["good"].Enabled {
		t.Errorf("good status flags: %+v", byName["good"])
	}
}

// A collector whose Supported() is false must never be reported as enabled,
// even when the config says it is.
func TestRegistryStatusUnsupportedNotEnabled(t *testing.T) {
	r := NewRegistry(testConfig(4))
	r.Register(newFake("absent", false))
	status := r.Status()
	if len(status) != 1 {
		t.Fatalf("expected 1 status, got %d", len(status))
	}
	if status[0].Enabled {
		t.Fatal("an unsupported collector must not be reported as enabled")
	}
	if status[0].Supported {
		t.Fatal("Supported must be false")
	}
}

func TestClampConcurrency(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want int
	}{
		{0, DefaultMaxConcurrency},
		{-5, DefaultMaxConcurrency},
		{1, 1},
		{8, 8},
		{1000, MaxConcurrencyLimit},
	} {
		if got := clampConcurrency(tc.in); got != tc.want {
			t.Errorf("clampConcurrency(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRegistryEnabledList(t *testing.T) {
	cfg := testConfig(4)
	cfg.Collectors.FDB = false
	r := NewRegistry(cfg)
	r.RegisterAll(
		newFake(model.CollectorOpenWRT, true),
		newFake(model.CollectorARP, true),
		newFake(model.CollectorBridgeFDB, true),
		newFake(model.CollectorDHCP, false),
	)
	got := r.Enabled()
	want := []string{model.CollectorARP, model.CollectorOpenWRT}
	if len(got) != len(want) {
		t.Fatalf("Enabled() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Enabled() = %v, want %v", got, want)
		}
	}
}

func TestRunAllHelper(t *testing.T) {
	a := newFake("a", true)
	a.results = []Result{*NewResult(ResultGeneric, model.SourceARP, time.Now())}
	b := newFake("b", true)
	b.err = errors.New("bad")

	results, errs := RunAll(context.Background(), []Collector{a, b, newFake("c", false)})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(errs) != 1 || errs["b"] == nil {
		t.Fatalf("expected only b to fail, got %v", errs)
	}
}
