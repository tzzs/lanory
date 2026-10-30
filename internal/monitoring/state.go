package monitoring

import (
	"fmt"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// The state machine below implements model.DeviceState with rules that are
// deliberately asymmetric, because a single probe is weak evidence in both
// directions on a wireless network:
//
//   - failures need corroboration: FailureThreshold CONSECUTIVE failures before
//     a device is declared offline. One failure — or any number of failures
//     below the threshold — never marks a device offline.
//   - successes need corroboration too: RecoverConfirm consecutive successes
//     before a device leaves a degraded state.
//
// Sleeping heuristic (documented and tested)
//
// A device that answers intermittently — one success in three probes, the
// classic phone or watch power saving pattern — must settle on "sleeping", never
// flip between online and offline, and never be declared gone. The engine
// therefore keeps a bounded window of the most recent outcomes and applies:
//
//  1. trailing failures >= FailureThreshold
//     => OFFLINE, unless one of the escape hatches applies, all of which give
//     SLEEPING because they are evidence the device is alive:
//     a. the probe source itself lost connectivity: the failures then describe
//     the path, not the device;
//     b. the window shows the device resuming after a run of failures, which is
//     direct evidence of power cycling;
//     c. the device is already classified as sleeping and answered within the
//     window: a longer gap is consistent with a deeper sleep.
//  2. the window is oscillating
//     => SLEEPING. "Oscillating" means the window contains a success that
//     arrived after at least one failure: the device answers sometimes and not
//     other times, which is precisely what power saving looks like over a long
//     enough window. A device that has never failed and never stopped answering
//     is not oscillating.
//  3. no trailing failures
//     => ONLINE once RecoverConfirm successes have accumulated (or immediately
//     when the device has never failed), otherwise SLEEPING while the recovery
//     is being confirmed.
//  4. failures below the threshold that are not oscillating
//     => ONLINE (a transient blip) when the device has answered before, and
//     UNKNOWN when it never has.
//
// A device that has never been probed stays UNKNOWN with an explanatory reason.
//
// Two consequences are worth stating because they are deliberate:
//
//   - A device that flaps — one success in three, the phone/watch pattern — is
//     never marked offline, no matter how long the current silent run is,
//     because the window keeps showing that it comes back.
//   - A device that answers once and then goes permanently silent for
//     FailureThreshold probes IS marked offline: permanent silence is absence,
//     and calling it sleeping forever would hide a genuinely dead device.

// stateHistorySize is the number of recent outcomes retained per probe to
// detect intermittent (sleeping) devices. It is fixed and small so the engine
// keeps a constant memory footprint on a router.
const stateHistorySize = 12

// DeviceStateSetter is the subset of the Device Identity Engine the probe engine
// needs to keep the device row and the SSE stream in sync.
//
// It is declared as a narrow interface on purpose: internal/device imports
// internal/database, so importing it here would couple the probe engine to the
// whole identity engine. *device.Engine satisfies it as is, so wiring stays
// trivial while tests can supply a fake.
type DeviceStateSetter interface {
	// SetOnline marks the device online.
	SetOnline(id, reason string, code *model.Reason) error
	// SetOffline marks the device offline.
	SetOffline(id, reason string, code *model.Reason) error
	// SetUnknown marks the device state as unknown.
	SetUnknown(id, reason string, code *model.Reason) error
	// MarkSleeping records that the device shows power saving behaviour.
	MarkSleeping(id, reason string, code *model.Reason) error
}

// StateTransition records a state change with the evidence behind it, so the UI
// can always answer "why is this device sleeping?".
type StateTransition struct {
	// ProbeID is the probe that produced the transition.
	ProbeID string
	// DeviceID is the device the probe is attached to, when any.
	DeviceID string
	// From and To are the device states before and after.
	From model.DeviceState
	To   model.DeviceState
	// Reason explains the transition in human readable form.
	Reason string
	// ReasonCode is the translatable form of Reason: a stable code plus the
	// parameters the message template needs.
	ReasonCode *model.Reason
	// Method is the detection method, e.g. "icmp" or "icmp->tcp".
	Method string
	// Source is the probe source that produced the evidence.
	Source string
	// At is when the transition was decided.
	At time.Time
}

// StateSnapshot is the tracked state of one probe.
type StateSnapshot struct {
	DeviceState model.DeviceState `json:"device_state"`
	Reason      string            `json:"reason,omitempty"`
	// History lists the most recent outcomes, oldest first.
	History []bool `json:"history,omitempty"`
}

// StateMachine applies probe outcomes to the device state model.
//
// It is safe for concurrent use; each probe owns its own state entry. Decide
// reads the machine's bookkeeping but never mutates it, so "what would happen"
// questions are side effect free.
type StateMachine struct {
	setter DeviceStateSetter
	states map[string]*probeState

	// sourceReachable reports whether the probe source still has a path to the
	// network. When it is false, a failed device probe is not evidence that the
	// device is gone — it is evidence that the source cannot see the network.
	sourceReachable bool

	// presence is the optional cross-check of whether a device is still on the
	// segment. The neighbour table is the kernel's own view: an entry means the
	// host answered an ARP request recently, whatever it does about echo
	// requests. It is what separates "the device is gone" from "the device does
	// not answer ping", which a Windows machine or a sleeping phone otherwise
	// gets reported as.
	presence PresenceChecker
}

// PresenceChecker reports whether a device is still present on the segment.
type PresenceChecker interface {
	// Seen reports whether the neighbour table has a recent entry for the
	// address.
	Seen(ip string) bool
}

// probeState is the per-probe bookkeeping the state machine needs.
type probeState struct {
	// history holds the last stateHistorySize outcomes, oldest first.
	history []bool
	// deviceState is the state currently applied to the device.
	deviceState model.DeviceState
	// lastReason is the reason of the last applied transition.
	lastReason string
}

// NewStateMachine returns a state machine that reports device transitions
// through setter. setter may be nil, in which case transitions are computed but
// not pushed anywhere (useful for unit tests and for network-only probes).
func NewStateMachine(setter DeviceStateSetter) *StateMachine {
	return &StateMachine{
		setter:          setter,
		states:          map[string]*probeState{},
		sourceReachable: true,
	}
}

// Setter returns the device state setter this machine pushes transitions
// through, or nil when none was configured.
func (m *StateMachine) Setter() DeviceStateSetter { return m.setter }

// WithPresence attaches the neighbour-table cross-check.
//
// Without it a device that is present but silent to the probe method is
// reported as offline, which is wrong for a Windows machine, a sleeping phone
// or a printer with no ICMP stack.
func (m *StateMachine) WithPresence(p PresenceChecker) *StateMachine {
	m.presence = p
	return m
}

// SetSourceReachable records whether the probe source still has a path to the
// network. When it is false the machine stops declaring devices offline: the
// failures describe the path, not the devices.
func (m *StateMachine) SetSourceReachable(reachable bool) { m.sourceReachable = reachable }

// SourceReachable reports the current source connectivity assumption.
func (m *StateMachine) SourceReachable() bool { return m.sourceReachable }

// CurrentState returns the state the machine currently tracks for probeID. A
// probe that was never evaluated is StateUnknown.
func (m *StateMachine) CurrentState(probeID string) model.DeviceState {
	if st, ok := m.states[probeID]; ok {
		return st.deviceState
	}
	return model.StateUnknown
}

// Snapshot returns a copy of the tracked state of probeID. The second return
// value is false when the probe has never been evaluated.
func (m *StateMachine) Snapshot(probeID string) (StateSnapshot, bool) {
	st, ok := m.states[probeID]
	if !ok {
		return StateSnapshot{}, false
	}
	history := make([]bool, len(st.history))
	copy(history, st.history)
	return StateSnapshot{
		DeviceState: st.deviceState,
		Reason:      st.lastReason,
		History:     history,
	}, true
}

// Decide computes the device state that the outcome success implies for target.
//
// Decide is pure: it never mutates the machine, so callers can ask "what would
// happen" without side effects. Use Apply to record an outcome.
func (m *StateMachine) Decide(target model.ProbeTarget, success bool) (model.DeviceState, string) {
	history := appendOutcome(m.rawHistory(target.ID), success)
	state, r := m.decideHistory(target, history)
	return state, r.Text()
}

// Apply records the outcome and returns the resulting transition.
func (m *StateMachine) Apply(target model.ProbeTarget, success bool) StateTransition {
	from := m.CurrentState(target.ID)
	st := m.stateFor(target.ID)
	st.history = appendOutcome(st.history, success)
	to, r := m.decideHistory(target, st.history)
	st.deviceState = to
	st.lastReason = r.text
	return StateTransition{
		ProbeID:    target.ID,
		DeviceID:   target.DeviceID,
		From:       from,
		To:         to,
		Reason:     r.text,
		ReasonCode: r.Reason(),
		Method:     string(target.Protocol),
		Source:     sourceLabel(target),
		At:         time.Now().UTC(),
	}
}

// stateFor returns (creating if needed) the bookkeeping of a probe.
func (m *StateMachine) stateFor(probeID string) *probeState {
	st, ok := m.states[probeID]
	if !ok {
		st = &probeState{deviceState: model.StateUnknown}
		m.states[probeID] = st
	}
	return st
}

// rawHistory returns the stored history of a probe without copying it.
func (m *StateMachine) rawHistory(probeID string) []bool {
	if st, ok := m.states[probeID]; ok {
		return st.history
	}
	return nil
}

// decideHistory implements the documented rules over an outcome window.
func (m *StateMachine) decideHistory(target model.ProbeTarget, history []bool) (model.DeviceState, probeReason) {
	threshold := positiveOr(target.FailureThreshold, 1)
	recover := positiveOr(target.RecoverConfirm, 1)
	src := sourceLabel(target)
	proto := string(target.Protocol)

	failures := trailingFailures(history)
	successes := trailingSuccesses(history)

	// Rule 1: enough consecutive failures.
	if failures >= threshold {
		if !m.sourceReachable {
			return model.StateSleeping, newReason(
				model.ReasonProbeUnreachableSourceDown,
				fmt.Sprintf(
					"unreachable for %d consecutive probes from %s via %s, but the probe source itself lost connectivity: this says nothing about the device",
					failures, src, proto),
				withParams(commonParams(src, proto), map[string]string{
					"failures": itoa(failures),
				}))
		}
		// The device is in the neighbour table, so it is on the segment and
		// answered an ARP request recently. It is not gone; it just does not
		// answer this probe method.
		if m.presence != nil && target.Target != "" && m.presence.Seen(target.Target) {
			return model.StateSleeping, newReason(
				model.ReasonProbeUnreachableStillPresent,
				fmt.Sprintf(
					"unreachable for %d consecutive probes from %s via %s, but the neighbour table still lists %s: the device is on the segment and does not answer this probe method",
					failures, src, proto, target.Target),
				withParams(commonParams(src, proto), map[string]string{
					"failures": itoa(failures),
					"address":  target.Target,
				}))
		}
		if oscillating(history) {
			// The device resumed after an earlier run of failures: power cycling.
			return model.StateSleeping, newReason(
				model.ReasonProbeUnreachablePowerCycling,
				fmt.Sprintf(
					"unreachable for %d consecutive probes from %s via %s, but the device resumed after an earlier gap of %d failures: power saving, not absence",
					failures, src, proto, threshold),
				withParams(commonParams(src, proto), map[string]string{
					"failures": itoa(failures),
					"gap":      itoa(threshold),
				}))
		}
		if m.CurrentState(target.ID) == model.StateSleeping && hasSuccess(history) {
			// Already classified as power saving and still alive within the window.
			return model.StateSleeping, newReason(
				model.ReasonProbeUnreachableDeeperSleep,
				fmt.Sprintf(
					"unreachable for %d consecutive probes from %s via %s; the device was already sleeping and answered %s, so this is a deeper sleep rather than absence",
					failures, src, proto, lastSuccessAge(history)),
				withParams(commonParams(src, proto), map[string]string{
					"failures":     itoa(failures),
					"last_success": lastSuccessAge(history),
				}))
		}
		return model.StateOffline, newReason(
			model.ReasonProbeUnreachableConsecutive,
			fmt.Sprintf(
				"unreachable for %d consecutive probes from %s via %s with no recent success",
				failures, src, proto),
			withParams(commonParams(src, proto), map[string]string{
				"failures": itoa(failures),
			}))
	}

	// Rule 2: oscillating pattern.
	if failures > 0 && oscillating(history) {
		return model.StateSleeping, newReason(
			model.ReasonProbeIntermittent,
			fmt.Sprintf(
				"intermittent reachability from %s via %s: %d of the last %d probes failed",
				src, proto, countFailures(history), len(history)),
			withParams(commonParams(src, proto), map[string]string{
				"failed": itoa(countFailures(history)),
				"total":  itoa(len(history)),
			}))
	}

	// Rule 3: answering right now.
	if failures == 0 {
		if successes >= recover || !hasFailure(history) {
			return model.StateOnline, newReason(
				model.ReasonProbeReachable,
				fmt.Sprintf("reachable from %s via %s", src, proto),
				commonParams(src, proto))
		}
		return model.StateSleeping, newReason(
			model.ReasonProbeConfirmingRecovery,
			fmt.Sprintf(
				"confirming recovery from %s via %s: %d of %d required successes", src, proto, successes, recover),
			withParams(commonParams(src, proto), map[string]string{
				"successes": itoa(successes),
				"required":  itoa(recover),
			}))
	}

	// Rule 4: failures below the threshold that are not oscillating.
	if hasSuccess(history) {
		return model.StateOnline, newReason(
			model.ReasonProbeBelowThreshold,
			fmt.Sprintf(
				"%d consecutive failure(s) below the threshold of %d from %s via %s: not enough evidence to change state",
				failures, threshold, src, proto),
			withParams(commonParams(src, proto), map[string]string{
				"failures":  itoa(failures),
				"threshold": itoa(threshold),
			}))
	}
	return model.StateUnknown, newReason(
		model.ReasonProbeNeverSucceeded,
		fmt.Sprintf(
			"no successful probe yet from %s via %s (%d failure(s), threshold %d)",
			src, proto, failures, threshold),
		withParams(commonParams(src, proto), map[string]string{
			"failures":  itoa(failures),
			"threshold": itoa(threshold),
		}))
}

// counters returns the consecutive failure and success counters of a probe.
//
// A probe that was never evaluated reports (0, 0). The counters describe the
// trailing run of outcomes, which is exactly what the probe_targets row stores.
func (m *StateMachine) counters(probeID string) (failures, successes int) {
	st, ok := m.states[probeID]
	if !ok {
		return 0, 0
	}
	return trailingFailures(st.history), trailingSuccesses(st.history)
}

// UnknownReason is the reason recorded for a device that has never been probed.
func UnknownReason(source string) string {
	return fmt.Sprintf("never probed from %s: no evidence either way",
		sourceLabel(model.ProbeTarget{SourceID: source}))
}

// --- outcome window helpers -------------------------------------------------

// appendOutcome appends an outcome and keeps the window bounded.
func appendOutcome(history []bool, success bool) []bool {
	out := make([]bool, 0, len(history)+1)
	out = append(out, history...)
	out = append(out, success)
	if len(out) > stateHistorySize {
		out = out[len(out)-stateHistorySize:]
	}
	return out
}

// trailingFailures counts the failures at the end of the window.
func trailingFailures(history []bool) int {
	n := 0
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] {
			break
		}
		n++
	}
	return n
}

// trailingSuccesses counts the successes at the end of the window.
func trailingSuccesses(history []bool) int {
	n := 0
	for i := len(history) - 1; i >= 0; i-- {
		if !history[i] {
			break
		}
		n++
	}
	return n
}

func countFailures(history []bool) int {
	n := 0
	for _, ok := range history {
		if !ok {
			n++
		}
	}
	return n
}

func hasFailure(history []bool) bool { return countFailures(history) > 0 }

func hasSuccess(history []bool) bool {
	for _, ok := range history {
		if ok {
			return true
		}
	}
	return false
}

// oscillating reports whether the window contains a success that arrived after a
// failure, i.e. the device stops and starts answering. That is the signature of
// power saving.
func oscillating(history []bool) bool {
	seenFailure := false
	for _, ok := range history {
		if !ok {
			seenFailure = true
			continue
		}
		if seenFailure {
			return true
		}
	}
	return false
}

// lastSuccessAge renders how far back the last success in history was.
func lastSuccessAge(history []bool) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] {
			back := len(history) - 1 - i
			if back == 0 {
				return "on the previous probe"
			}
			return fmt.Sprintf("%d probe(s) ago", back)
		}
	}
	return "never"
}

// sourceLabel renders the probe source for human readable reasons.
func sourceLabel(target model.ProbeTarget) string {
	if target.SourceID != "" {
		return target.SourceID
	}
	return DefaultSourceID
}

func positiveOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
