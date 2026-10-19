package monitoring

import (
	"context"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// target is a small constructor for internet connectivity checks in tests.
func target(id, role, host string, proto model.ProbeProtocol, port int, enabled bool) InternetTarget {
	return InternetTarget{
		ID: id, Name: id, Role: role, Target: host,
		Protocol: proto, Port: port, Enabled: enabled, Source: "lanory",
	}
}

// TestAggregateInternetAllHealthy asserts "all targets reachable" gives healthy.
func TestAggregateInternetAllHealthy(t *testing.T) {
	targets := []InternetTarget{
		target("gw", InternetRoleGateway, "192.168.100.1", model.ProbeICMP, 0, true),
		target("lan", InternetRoleLAN, "192.0.2.116", model.ProbeICMP, 0, true),
		target("dns", InternetRoleDNS, "example.com@192.168.100.1", model.ProbeDNS, 0, true),
		target("ext", InternetRoleExternal, "one.one.one.one", model.ProbeTCP, 443, true),
	}
	for i := range targets {
		targets[i].Reachable = true
		targets[i].LatencyMS = 10
		targets[i].Status = model.ProbeRunSuccess
	}

	v := aggregateInternet(targets, time.Now().UTC())
	if v.Status != InternetHealthy {
		t.Fatalf("verdict = %q, want %q (%s)", v.Status, InternetHealthy, v.Summary)
	}
	if len(v.Targets) != 4 {
		t.Fatalf("target detail = %d entries, want 4", len(v.Targets))
	}
	if len(v.Groups) != 4 {
		t.Fatalf("groups = %d, want 4 (one per role)", len(v.Groups))
	}
	for _, g := range v.Groups {
		if g.Reachable != g.Total {
			t.Fatalf("group %s = %d/%d, want all reachable", g.Role, g.Reachable, g.Total)
		}
	}
	if v.Source != "lanory" {
		t.Fatalf("source = %q, want lanory", v.Source)
	}
}

// TestAggregateInternetOneFailingIsDegraded asserts a single failing target among
// several healthy ones gives degraded, not down and not healthy.
func TestAggregateInternetOneFailingIsDegraded(t *testing.T) {
	targets := []InternetTarget{
		target("gw", InternetRoleGateway, "192.168.100.1", model.ProbeICMP, 0, true),
		target("lan", InternetRoleLAN, "192.0.2.116", model.ProbeICMP, 0, true),
		target("ext-tcp", InternetRoleExternal, "one.one.one.one", model.ProbeTCP, 443, true),
		target("ext-dns", InternetRoleExternal, "one.one.one.one", model.ProbeDNS, 0, true),
	}
	targets[0].Reachable = true
	targets[0].Status = model.ProbeRunSuccess
	targets[1].Reachable = true
	targets[1].Status = model.ProbeRunSuccess
	// Only one external target fails: the internet is degraded, not down.
	targets[2].Reachable = false
	targets[2].Status = model.ProbeRunError
	targets[2].ErrorType = ErrorTypeRefused
	targets[3].Reachable = true
	targets[3].Status = model.ProbeRunSuccess

	v := aggregateInternet(targets, time.Now().UTC())
	if v.Status != InternetDegraded {
		t.Fatalf("verdict = %q, want %q (%s)", v.Status, InternetDegraded, v.Summary)
	}

	// The per-target detail must still show exactly which one failed.
	var failed []string
	for _, ct := range v.Targets {
		if !ct.Reachable {
			failed = append(failed, ct.ID)
		}
	}
	if len(failed) != 1 || failed[0] != "ext-tcp" {
		t.Fatalf("failed targets = %v, want [ext-tcp]", failed)
	}

	// The external group is partially reachable, so the group must show 1/2.
	var ext *InternetGroup
	for i := range v.Groups {
		if v.Groups[i].Role == InternetRoleExternal {
			ext = &v.Groups[i]
		}
	}
	if ext == nil || ext.Reachable != 1 || ext.Total != 2 {
		t.Fatalf("external group = %+v, want 1/2", ext)
	}
}

// TestAggregateInternetAllFailingIsDown asserts every target failing gives down.
func TestAggregateInternetAllFailingIsDown(t *testing.T) {
	targets := []InternetTarget{
		target("gw", InternetRoleGateway, "192.168.100.1", model.ProbeICMP, 0, true),
		target("lan", InternetRoleLAN, "192.0.2.116", model.ProbeICMP, 0, true),
		target("ext", InternetRoleExternal, "one.one.one.one", model.ProbeTCP, 443, true),
	}
	for i := range targets {
		targets[i].Reachable = false
		targets[i].Status = model.ProbeRunTimeout
		targets[i].ErrorType = ErrorTypeTimeout
	}

	v := aggregateInternet(targets, time.Now().UTC())
	if v.Status != InternetDown {
		t.Fatalf("verdict = %q, want %q (%s)", v.Status, InternetDown, v.Summary)
	}
	if len(v.Targets) != 3 {
		t.Fatalf("target detail = %d entries, want 3 (every target reported separately)", len(v.Targets))
	}
}

// TestAggregateInternetGatewayUpExternalDownAssertsDegraded asserts the split
// verdict the product requires: LAN works, internet does not.
func TestAggregateInternetGatewayUpExternalDownAssertsDegraded(t *testing.T) {
	targets := []InternetTarget{
		target("gw", InternetRoleGateway, "192.168.100.1", model.ProbeICMP, 0, true),
		target("ext", InternetRoleExternal, "one.one.one.one", model.ProbeTCP, 443, true),
	}
	targets[0].Reachable = true
	targets[0].Status = model.ProbeRunSuccess
	targets[1].Reachable = false
	targets[1].Status = model.ProbeRunError

	v := aggregateInternet(targets, time.Now().UTC())
	if v.Status != InternetDegraded {
		t.Fatalf("verdict = %q, want %q: LAN up but WAN down is degraded, not down", v.Status, InternetDegraded)
	}
	for _, g := range v.Groups {
		if g.Role == InternetRoleGateway && g.Reachable != 1 {
			t.Fatalf("gateway group = %d/%d, want 1/1", g.Reachable, g.Total)
		}
		if g.Role == InternetRoleExternal && g.Reachable != 0 {
			t.Fatalf("external group = %d/%d, want 0/1", g.Reachable, g.Total)
		}
	}
}

// TestAggregateInternetDisabledTargetsIgnored asserts a disabled check is
// reported but never counted.
func TestAggregateInternetDisabledTargetsIgnored(t *testing.T) {
	targets := []InternetTarget{
		target("gw", InternetRoleGateway, "192.168.100.1", model.ProbeICMP, 0, true),
		target("off", InternetRoleExternal, "unconfigured.example", model.ProbeTCP, 443, false),
	}
	targets[0].Reachable = true
	targets[0].Status = model.ProbeRunSuccess

	v := aggregateInternet(targets, time.Now().UTC())
	if v.Status != InternetHealthy {
		t.Fatalf("verdict = %q, want %q: a disabled check must not change the verdict", v.Status, InternetHealthy)
	}
	if len(v.Targets) != 2 {
		t.Fatalf("target detail = %d entries, want 2 (disabled checks are still listed)", len(v.Targets))
	}
	for _, g := range v.Groups {
		if g.Role == InternetRoleExternal {
			t.Fatalf("external group present, want disabled checks excluded from the rollup")
		}
	}
}

// TestAggregateInternetNoTargetsIsUnknown asserts an empty configuration reports
// unknown rather than healthy.
func TestAggregateInternetNoTargetsIsUnknown(t *testing.T) {
	v := aggregateInternet(nil, time.Now().UTC())
	if v.Status != InternetUnknown {
		t.Fatalf("verdict = %q, want %q", v.Status, InternetUnknown)
	}
}

// TestDefaultInternetTargets are several independent checks, never one website.
func TestDefaultInternetTargets(t *testing.T) {
	targets := DefaultInternetTargets("lanory")
	if len(targets) < 3 {
		t.Fatalf("%d default targets, want at least 3 independent checks", len(targets))
	}
	roles := map[string]int{}
	hosts := map[string]int{}
	for _, ct := range targets {
		roles[ct.Role]++
		hosts[ct.Target]++
		if ct.Source != "lanory" {
			t.Fatalf("target %q source = %q, want lanory", ct.ID, ct.Source)
		}
		if ct.Protocol == "" {
			t.Fatalf("target %q has no protocol", ct.ID)
		}
	}
	for _, want := range []string{InternetRoleGateway, InternetRoleExternal} {
		if roles[want] == 0 {
			t.Fatalf("no default target for role %q", want)
		}
	}
	// A single host must not carry the whole verdict.
	for host, n := range hosts {
		if n == len(targets) {
			t.Fatalf("every default target points at %q, which is exactly the single-website mistake", host)
		}
	}
}

// TestEngineInternetRoundTrip asserts the engine records per-target outcomes and
// exposes a verdict without a scheduler in the loop.
func TestEngineInternetRoundTrip(t *testing.T) {
	port, stop := listener(t)
	defer stop()

	e := NewEngine(nil, nil, NewExecutor(DefaultExecutorConfig()), nil, engineCfg())
	e.SetInternetTargets([]InternetTarget{
		{
			ID: "gw", Name: "Gateway", Role: InternetRoleGateway,
			Target: "127.0.0.1", Protocol: model.ProbeTCP, Port: port,
			Enabled: true, Source: "lanory",
		},
		{
			ID: "ext", Name: "External", Role: InternetRoleExternal,
			Target: "127.0.0.1", Protocol: model.ProbeTCP, Port: closedPort(t),
			Enabled: true, Source: "lanory",
		},
	})

	v := e.CheckInternet(context.Background())
	if v.Status != InternetDegraded {
		t.Fatalf("verdict = %q, want %q (%s)", v.Status, InternetDegraded, v.Summary)
	}
	if len(v.Targets) != 2 {
		t.Fatalf("target detail = %d, want 2", len(v.Targets))
	}
	byID := map[string]InternetTarget{}
	for _, ct := range v.Targets {
		byID[ct.ID] = ct
		if ct.LastCheckedAt.IsZero() {
			t.Fatalf("target %q has no timestamp", ct.ID)
		}
		if ct.Source != "lanory" {
			t.Fatalf("target %q source = %q, want lanory", ct.ID, ct.Source)
		}
	}
	if !byID["gw"].Reachable {
		t.Fatal("the reachable gateway check is reported as unreachable")
	}
	if byID["ext"].Reachable {
		t.Fatal("the closed port check is reported as reachable")
	}
	if byID["ext"].ErrorType != ErrorTypeRefused {
		t.Fatalf("failed check error type = %q, want %q", byID["ext"].ErrorType, ErrorTypeRefused)
	}

	// The verdict is available afterwards without re-running the probes.
	again := e.InternetVerdict()
	if again.Status != InternetDegraded {
		t.Fatalf("cached verdict = %q, want %q", again.Status, InternetDegraded)
	}
}

// TestCheckInternetNoTargets asserts an engine with no checks reports unknown and
// does not panic.
func TestCheckInternetNoTargets(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, engineCfg())
	v := e.CheckInternet(context.Background())
	if v.Status != InternetUnknown {
		t.Fatalf("verdict = %q, want %q", v.Status, InternetUnknown)
	}
	if len(v.Targets) != 0 {
		t.Fatalf("target detail = %d, want 0", len(v.Targets))
	}
}

// TestCheckInternetSkipsDisabled asserts disabled checks are not executed.
func TestCheckInternetSkipsDisabled(t *testing.T) {
	e := NewEngine(nil, nil, NewSlowExecutor(50*time.Millisecond), nil, engineCfg())
	e.SetInternetTargets([]InternetTarget{
		{ID: "on", Name: "on", Role: InternetRoleGateway, Target: "127.0.0.1", Protocol: model.ProbeTCP, Enabled: true},
		{ID: "off", Name: "off", Role: InternetRoleExternal, Target: "127.0.0.1", Protocol: model.ProbeTCP, Enabled: false},
	})
	v := e.CheckInternet(context.Background())
	if v.Status != InternetHealthy {
		t.Fatalf("verdict = %q, want %q", v.Status, InternetHealthy)
	}
	for _, ct := range v.Targets {
		if ct.ID == "off" && !ct.LastCheckedAt.IsZero() {
			t.Fatal("a disabled check was executed")
		}
	}
}

// TestCheckInternetConcurrentTargets asserts the checks run concurrently so one
// slow target does not serialise the verdict.
func TestCheckInternetConcurrentTargets(t *testing.T) {
	e := NewEngine(nil, nil, NewSlowExecutor(300*time.Millisecond), nil, engineCfg())
	e.SetInternetTargets([]InternetTarget{
		{ID: "a", Name: "a", Role: InternetRoleGateway, Target: "127.0.0.1", Protocol: model.ProbeTCP, Enabled: true},
		{ID: "b", Name: "b", Role: InternetRoleLAN, Target: "127.0.0.1", Protocol: model.ProbeTCP, Enabled: true},
		{ID: "c", Name: "c", Role: InternetRoleExternal, Target: "127.0.0.1", Protocol: model.ProbeTCP, Enabled: true},
	})
	start := time.Now()
	v := e.CheckInternet(context.Background())
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("three 300ms checks took %s, want them to run concurrently", elapsed)
	}
	if v.Status != InternetHealthy {
		t.Fatalf("verdict = %q, want %q", v.Status, InternetHealthy)
	}
}

// TestConnectivityFeedsSourceReachability asserts the link between the internet
// verdict and the device state machine: when nothing at all is reachable, the
// probe source has lost the network, and device failures must not be read as
// "every device went offline".
func TestConnectivityFeedsSourceReachability(t *testing.T) {
	e := NewEngine(nil, nil, NewSlowExecutor(time.Millisecond), nil, engineCfg())
	e.SetInternetTargets([]InternetTarget{
		{ID: "lan", Name: "lan", Role: InternetRoleLAN, Target: "127.0.0.1", Protocol: model.ProbeTCP, Enabled: true},
	})
	e.executor = NewSlowExecutor(time.Millisecond)
	target := probe("tv", 3, 1)
	e.StateMachine().Apply(target, true)

	// Everything fails: the source is disconnected.
	downExecutor := &alwaysFailExecutor{}
	e.executor = downExecutor
	v := e.CheckInternet(context.Background())
	if v.Status != InternetDown {
		t.Fatalf("verdict = %q, want down", v.Status)
	}
	if e.StateMachine().SourceReachable() {
		t.Fatal("source still reports reachable after every connectivity target failed")
	}

	// Device probes keep failing, but the devices must not all go offline.
	for i := 0; i < 6; i++ {
		tr := e.StateMachine().Apply(target, false)
		if tr.To == model.StateOffline {
			t.Fatalf("device went offline while the probe source was disconnected (failure %d)", i+1)
		}
	}
	if got := e.StateMachine().CurrentState("tv"); got != model.StateSleeping {
		t.Fatalf("state = %q, want sleeping while the source is disconnected", got)
	}

	// Connectivity returns: so does the source reachability.
	e.executor = NewSlowExecutor(time.Millisecond)
	v = e.CheckInternet(context.Background())
	if v.Status == InternetDown {
		t.Fatalf("verdict = %q, want it to recover", v.Status)
	}
	if !e.StateMachine().SourceReachable() {
		t.Fatal("source reachability did not recover")
	}
}

// alwaysFailExecutor is a test Executor whose every probe fails.
type alwaysFailExecutor struct{}

func (a *alwaysFailExecutor) Execute(ctx context.Context, target model.ProbeTarget) ProbeRecord {
	return ProbeRecord{
		SourceID:    sourceOf(DefaultSourceID, target.SourceID),
		TargetValue: target.Target,
		Protocol:    target.Protocol,
		Result: model.ProbeResult{
			Timestamp:    time.Now().UTC(),
			Success:      false,
			Status:       model.ProbeRunTimeout,
			ErrorType:    ErrorTypeTimeout,
			ErrorMessage: "simulated network outage",
			LatencyMS:    1,
		},
	}
}

func (a *alwaysFailExecutor) Protocol(p model.ProbeProtocol) bool { return true }
func (a *alwaysFailExecutor) Close() error                        { return nil }

// TestInternetTargetsSorted asserts the exposed list order is stable, so the API
// does not shuffle the UI between calls.
func TestInternetTargetsSorted(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, engineCfg())
	e.SetInternetTargets([]InternetTarget{
		{ID: "c", Target: "c", Protocol: model.ProbeTCP, Enabled: true},
		{ID: "a", Target: "a", Protocol: model.ProbeTCP, Enabled: true},
		{ID: "b", Target: "b", Protocol: model.ProbeTCP, Enabled: true},
	})
	out := e.InternetTargets()
	if len(out) != 3 {
		t.Fatalf("%d targets, want 3", len(out))
	}
	for i, want := range []string{"a", "b", "c"} {
		if out[i].ID != want {
			t.Fatalf("target %d = %q, want %q", i, out[i].ID, want)
		}
	}
}
