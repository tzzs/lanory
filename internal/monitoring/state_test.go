package monitoring

import (
	"strings"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

func probe(id string, threshold, recover int) model.ProbeTarget {
	return model.ProbeTarget{
		ID:               id,
		Name:             id,
		Kind:             model.ProbeKindDevice,
		Target:           "192.168.100.10",
		Protocol:         model.ProbeICMP,
		SourceID:         "lanory",
		Interval:         60,
		Timeout:          1,
		Enabled:          true,
		DeviceID:         "dev-1",
		FailureThreshold: threshold,
		RecoverConfirm:   recover,
	}
}

// TestStateNeverProbedStaysUnknown asserts the "never probed" rule.
func TestStateNeverProbedStaysUnknown(t *testing.T) {
	m := NewStateMachine(nil)
	if got := m.CurrentState("nope"); got != model.StateUnknown {
		t.Fatalf("unprobed probe state = %q, want unknown", got)
	}
	if _, ok := m.Snapshot("nope"); ok {
		t.Fatal("unprobed probe must not have a snapshot")
	}
	reason := UnknownReason("lanory")
	if !strings.Contains(reason, "never probed") {
		t.Fatalf("unknown reason = %q, want it to mention never probed", reason)
	}
}

// TestSingleFailureNeverOffline asserts that a single failure never marks a
// device offline, and that failures below the threshold do not either.
//
// The state after a below-threshold failure is "online" (a transient blip on a
// device that was answering) or "unknown" (a device that never answered). What
// matters — and what is asserted here — is that it is never "offline".
func TestSingleFailureNeverOffline(t *testing.T) {
	cases := []struct {
		name      string
		threshold int
		failures  int
		want      model.DeviceState
	}{
		{"threshold1 one failure", 1, 1, model.StateOffline},
		{"threshold2 one failure", 2, 1, model.StateOnline},
		{"threshold3 one failure", 3, 1, model.StateOnline},
		{"threshold3 two failures", 3, 2, model.StateOnline},
		{"threshold5 two failures", 5, 2, model.StateOnline},
		{"threshold3 two failures never answered", 3, 2, model.StateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewStateMachine(nil)
			target := probe("p", tc.threshold, 1)
			answered := !strings.Contains(tc.name, "never answered")
			if answered {
				// Bring the device online first.
				if state, _ := m.Decide(target, true); state != model.StateOnline {
					t.Fatalf("after one success state = %q, want online", state)
				}
				m.Apply(target, true)
			}
			for i := 0; i < tc.failures; i++ {
				m.Apply(target, false)
			}
			if got := m.CurrentState("p"); got != tc.want {
				t.Fatalf("after %d failures (threshold %d) state = %q, want %q",
					tc.failures, tc.threshold, got, tc.want)
			}
			if got := m.CurrentState("p"); got == model.StateOffline && tc.threshold > tc.failures {
				t.Fatalf("device went offline after only %d failures with threshold %d", tc.failures, tc.threshold)
			}
		})
	}
}

// TestNthFailureFlipsOffline asserts that the Nth consecutive failure flips the
// device to offline, and that no earlier failure does.
func TestNthFailureFlipsOffline(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("p", 3, 1)
	m.Apply(target, true)
	for i := 1; i <= 2; i++ {
		m.Apply(target, false)
		if got := m.CurrentState("p"); got != model.StateOnline {
			t.Fatalf("failure %d: state = %q, want online (offline only at failure 3)", i, got)
		}
	}
	m.Apply(target, false)
	if got := m.CurrentState("p"); got != model.StateOffline {
		t.Fatalf("after 3 consecutive failures state = %q, want offline", got)
	}
	snap, ok := m.Snapshot("p")
	if !ok {
		t.Fatal("snapshot missing")
	}
	if snap.DeviceState != model.StateOffline {
		t.Fatalf("snapshot state = %q, want offline", snap.DeviceState)
	}
	if !strings.Contains(snap.Reason, "consecutive") {
		t.Fatalf("offline reason = %q, want it to mention consecutive failures", snap.Reason)
	}
	failures, successes := m.counters("p")
	if failures != 3 || successes != 0 {
		t.Fatalf("counters = (%d,%d), want (3,0)", failures, successes)
	}
}

// TestOfflineRequiresNoRecentSuccess asserts that reaching the threshold is not
// enough on its own when the device was already classified as power saving.
func TestOfflineRequiresNoRecentSuccess(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("p", 3, 1)
	// Never answered: the threshold is reached and the device is genuinely gone.
	for i := 0; i < 3; i++ {
		m.Apply(target, false)
	}
	if got := m.CurrentState("p"); got != model.StateOffline {
		t.Fatalf("never-answering device after 3 failures = %q, want offline", got)
	}

	// A device that flaps (answers, stops, answers, stops) is power saving: it
	// is never offline, however long the current gap runs.
	m2 := NewStateMachine(nil)
	for _, ok := range []bool{true, false, true, false, false, false, false} {
		m2.Apply(target, ok)
		if got := m2.CurrentState("p"); got == model.StateOffline {
			t.Fatalf("flapping device went offline, want sleeping")
		}
	}
	if got := m2.CurrentState("p"); got != model.StateSleeping {
		t.Fatalf("flapping device = %q, want sleeping", got)
	}

	// But a device that answered once and then went permanently silent is
	// offline: permanent silence is absence, intermittent answering is sleeping.
	m3 := NewStateMachine(nil)
	m3.Apply(target, true)
	for i := 0; i < 3; i++ {
		m3.Apply(target, false)
	}
	if got := m3.CurrentState("p"); got != model.StateOffline {
		t.Fatalf("device that answered once then went silent = %q, want offline", got)
	}
}

// TestRecoveryNeedsConfirmation asserts that a single success after failures
// does not immediately flip the device back online unless recover_confirm is 1.
func TestRecoveryNeedsConfirmation(t *testing.T) {
	for _, recover := range []int{1, 2, 3} {
		t.Run("recover="+string(rune('0'+recover)), func(t *testing.T) {
			m := NewStateMachine(nil)
			target := probe("p", 2, recover)
			m.Apply(target, true)
			m.Apply(target, false)
			if got := m.CurrentState("p"); got != model.StateOnline {
				t.Fatalf("state after 1 failure = %q, want online", got)
			}
			// Two more failures reach the threshold: the device never answered
			// again in this window, so it is offline.
			m.Apply(target, false)
			m.Apply(target, false)
			if got := m.CurrentState("p"); got != model.StateOffline {
				t.Fatalf("state after threshold = %q, want offline", got)
			}

			// A single success: never enough unless recover_confirm == 1.
			m.Apply(target, true)
			if recover == 1 {
				if got := m.CurrentState("p"); got != model.StateOnline {
					t.Fatalf("recover_confirm=1: state after one success = %q, want online", got)
				}
				return
			}
			if got := m.CurrentState("p"); got == model.StateOnline {
				t.Fatalf("recover_confirm=%d: state flipped online after one success", recover)
			}

			// recover-1 more successes to confirm.
			for i := 1; i < recover; i++ {
				m.Apply(target, true)
			}
			if got := m.CurrentState("p"); got != model.StateOnline {
				t.Fatalf("recover_confirm=%d: state after %d successes = %q, want online",
					recover, recover, got)
			}
		})
	}
}

// TestIntermittentSettlesSleeping asserts the core sleeping rule: a device that
// answers one probe in three must settle on sleeping, never flip to offline and
// never oscillate between online and offline.
func TestIntermittentSettlesSleeping(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("phone", 3, 1)

	// Pattern: success, failure, failure, repeated. The device never reaches
	// three consecutive failures, so it can never be declared offline.
	pattern := []bool{true, false, false, true, false, false, true, false, false, true, false, false}
	seen := map[model.DeviceState]int{}
	for _, ok := range pattern {
		m.Apply(target, ok)
		seen[m.CurrentState("phone")]++
	}
	if seen[model.StateOffline] != 0 {
		t.Fatalf("intermittent device was marked offline %d times, want 0", seen[model.StateOffline])
	}
	if got := m.CurrentState("phone"); got != model.StateSleeping {
		t.Fatalf("intermittent device settled on %q, want sleeping", got)
	}

	snap, _ := m.Snapshot("phone")
	if !strings.Contains(snap.Reason, "intermittent") {
		t.Fatalf("sleeping reason = %q, want it to explain the heuristic", snap.Reason)
	}
	if len(snap.History) != stateHistorySize {
		t.Fatalf("history length = %d, want %d", len(snap.History), stateHistorySize)
	}
}

// TestIntermittentWithThresholdReachedIsSleeping asserts that a flapping device
// stays sleeping even when the failure threshold is exceeded. This is the
// phone/watch power saving case.
func TestIntermittentWithThresholdReachedIsSleeping(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("watch", 3, 1)

	// Flap twice so the device is classified as sleeping.
	for _, ok := range []bool{true, false, true, false} {
		m.Apply(target, ok)
	}
	if got := m.CurrentState("watch"); got != model.StateSleeping {
		t.Fatalf("flapping device = %q, want sleeping", got)
	}
	// Now a run of failures longer than the threshold.
	for i := 0; i < 5; i++ {
		m.Apply(target, false)
	}
	if got := m.CurrentState("watch"); got != model.StateSleeping {
		t.Fatalf("flapping device past threshold = %q, want sleeping", got)
	}
	snap, _ := m.Snapshot("watch")
	if !strings.Contains(snap.Reason, "power saving") {
		t.Fatalf("reason = %q, want it to mention power saving", snap.Reason)
	}

	// One success moves it back to online, proving it was never declared gone.
	m.Apply(target, true)
	if got := m.CurrentState("watch"); got != model.StateOnline {
		t.Fatalf("state after recovery = %q, want online", got)
	}
}

// TestResumedAfterGapIsOffline asserts the counterpart rule: permanent silence
// is absence. A device that answered once and then never again is offline, and
// coming back after that is a fresh online event rather than a resumed sleep.
func TestResumedAfterGapIsOffline(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("tv", 3, 1)
	m.Apply(target, true)
	for i := 0; i < 3; i++ {
		m.Apply(target, false)
	}
	if got := m.CurrentState("tv"); got != model.StateOffline {
		t.Fatalf("state after gap = %q, want offline", got)
	}
	// It comes back.
	m.Apply(target, true)
	if got := m.CurrentState("tv"); got != model.StateOnline {
		t.Fatalf("state after resume = %q, want online", got)
	}
	// And goes quiet again past the threshold: it has now flapped, so sleeping.
	for i := 0; i < 3; i++ {
		m.Apply(target, false)
	}
	if got := m.CurrentState("tv"); got != model.StateSleeping {
		t.Fatalf("state after second gap = %q, want sleeping", got)
	}
}

// TestSourceUnreachableKeepsDevicesUp asserts the network disconnect rule: when
// the probe source itself cannot reach the network, device failures describe the
// path and not the devices, so devices stay online-or-sleeping.
func TestSourceUnreachableKeepsDevicesUp(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("tv", 3, 1)
	m.Apply(target, true)
	m.SetSourceReachable(false)
	for i := 0; i < 6; i++ {
		m.Apply(target, false)
		if got := m.CurrentState("tv"); got == model.StateOffline {
			t.Fatalf("device declared offline on failure %d while the source was disconnected", i+1)
		}
	}
	if got := m.CurrentState("tv"); got != model.StateSleeping {
		t.Fatalf("state = %q, want sleeping while the source is disconnected", got)
	}
	snap, _ := m.Snapshot("tv")
	if !strings.Contains(snap.Reason, "probe source") {
		t.Fatalf("reason = %q, want it to blame the probe source", snap.Reason)
	}

	// The source comes back and the device answers: online again.
	m.SetSourceReachable(true)
	m.Apply(target, true)
	if got := m.CurrentState("tv"); got != model.StateOnline {
		t.Fatalf("state after reconnect = %q, want online", got)
	}
}

// TestHistoryIsBounded asserts the history window is bounded so the engine keeps
// a constant memory footprint on a router.
func TestHistoryIsBounded(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("p", 100, 1)
	for i := 0; i < stateHistorySize*3; i++ {
		m.Apply(target, true)
	}
	snap, _ := m.Snapshot("p")
	if len(snap.History) != stateHistorySize {
		t.Fatalf("history length = %d, want %d", len(snap.History), stateHistorySize)
	}
}

// TestUnknownWhenNeverSucceeded asserts a probe that has never succeeded stays
// unknown rather than offline or online.
func TestUnknownWhenNeverSucceeded(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("new", 3, 1)
	for i := 0; i < 2; i++ {
		m.Apply(target, false)
	}
	if got := m.CurrentState("new"); got != model.StateUnknown {
		t.Fatalf("state = %q, want unknown for a device that never answered", got)
	}
	m.Apply(target, false)
	if got := m.CurrentState("new"); got != model.StateOffline {
		t.Fatalf("state after threshold of never-answering device = %q, want offline", got)
	}
}

// TestTransitionRecordsEvidence asserts every transition carries the reason, the
// detection method and the probe source.
func TestTransitionRecordsEvidence(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("p", 1, 1)
	tr := m.Apply(target, true)
	if tr.Method != string(model.ProbeICMP) {
		t.Fatalf("method = %q, want icmp", tr.Method)
	}
	if tr.Source != "lanory" {
		t.Fatalf("source = %q, want lanory", tr.Source)
	}
	if tr.From != model.StateUnknown || tr.To != model.StateOnline {
		t.Fatalf("transition = %q -> %q, want unknown -> online", tr.From, tr.To)
	}
	if !strings.Contains(tr.Reason, "lanory") {
		t.Fatalf("reason = %q, want it to name the probe source", tr.Reason)
	}
	if tr.At.IsZero() {
		t.Fatal("transition timestamp is zero")
	}
	if tr.ProbeID != "p" || tr.DeviceID != "dev-1" {
		t.Fatalf("transition ids = (%q,%q), want (p,dev-1)", tr.ProbeID, tr.DeviceID)
	}
}

// TestDecideIsPure asserts Decide does not mutate the machine, so callers can
// ask "what would happen" without side effects.
func TestDecideIsPure(t *testing.T) {
	m := NewStateMachine(nil)
	target := probe("p", 2, 1)
	m.Apply(target, true)
	before, _ := m.Snapshot("p")

	for i := 0; i < 5; i++ {
		_, _ = m.Decide(target, false)
	}
	after, _ := m.Snapshot("p")
	if len(before.History) != len(after.History) {
		t.Fatalf("Decide mutated history: %d -> %d", len(before.History), len(after.History))
	}
	if before.DeviceState != after.DeviceState {
		t.Fatalf("Decide mutated state: %q -> %q", before.DeviceState, after.DeviceState)
	}
}

// TestSourceLabelFallsBack asserts a probe without an explicit source is
// attributed to the Lanory host.
func TestSourceLabelFallsBack(t *testing.T) {
	target := probe("p", 1, 1)
	target.SourceID = ""
	if got := sourceLabel(target); got != DefaultSourceID {
		t.Fatalf("source label = %q, want %q", got, DefaultSourceID)
	}
	target.SourceID = "windows-pc"
	if got := sourceLabel(target); got != "windows-pc" {
		t.Fatalf("source label = %q, want windows-pc", got)
	}
}
