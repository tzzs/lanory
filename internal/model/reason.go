package model

// ReasonCode identifies why a device is in its current state, or why an
// anomaly was raised.
//
// A reason code is a stable machine-readable identifier, not a sentence. The
// backend produces codes and parameters; the frontend renders them in the
// user's language. That split is what makes the reason translatable: an
// English sentence baked into the database cannot be translated after the
// fact, but a code plus parameters can.
//
// The human readable English text is still produced and stored alongside the
// code (see Reason and StateReason), because it is what a log file, a bug
// report and the API consumer outside the web UI need.
type ReasonCode string

const (
	// ReasonNone is the zero value: no reason was recorded.
	ReasonNone ReasonCode = ""

	// --- probe outcomes ---

	// ReasonProbeReachable: the device answered the probe.
	ReasonProbeReachable ReasonCode = "probe.reachable"
	// ReasonProbeUnreachableConsecutive: the device failed N consecutive
	// probes and showed no sign of life.
	ReasonProbeUnreachableConsecutive ReasonCode = "probe.unreachable_consecutive"
	// ReasonProbeUnreachableSourceDown: the device failed N consecutive
	// probes, but the probe source lost connectivity too, so the failures
	// describe the path rather than the device.
	ReasonProbeUnreachableSourceDown ReasonCode = "probe.unreachable_source_down"
	// ReasonProbeUnreachableStillPresent: the device failed N consecutive
	// probes but is still listed in the neighbour table.
	ReasonProbeUnreachableStillPresent ReasonCode = "probe.unreachable_still_present"
	// ReasonProbeUnreachablePowerCycling: the device failed N consecutive
	// probes but resumed after an earlier gap.
	ReasonProbeUnreachablePowerCycling ReasonCode = "probe.unreachable_power_cycling"
	// ReasonProbeUnreachableDeeperSleep: the device was already classified as
	// sleeping and answered within the window.
	ReasonProbeUnreachableDeeperSleep ReasonCode = "probe.unreachable_deeper_sleep"
	// ReasonProbeIntermittent: the device answers sometimes and not others.
	ReasonProbeIntermittent ReasonCode = "probe.intermittent"
	// ReasonProbeConfirmingRecovery: the device started answering again but
	// has not yet met the recovery threshold.
	ReasonProbeConfirmingRecovery ReasonCode = "probe.confirming_recovery"
	// ReasonProbeBelowThreshold: the device failed some probes, but not
	// enough to change its state.
	ReasonProbeBelowThreshold ReasonCode = "probe.below_threshold"
	// ReasonProbeNeverSucceeded: the device has never answered a probe.
	ReasonProbeNeverSucceeded ReasonCode = "probe.never_succeeded"
	// ReasonProbeNeverProbed: the device has never been probed at all.
	ReasonProbeNeverProbed ReasonCode = "probe.never_probed"

	// --- link and interface outcomes ---

	// ReasonLinkCarrierUp: the interface has carrier and a known speed.
	ReasonLinkCarrierUp ReasonCode = "link.carrier_up"
	// ReasonLinkCarrierUpNoSpeed: the interface has carrier but reports no
	// speed.
	ReasonLinkCarrierUpNoSpeed ReasonCode = "link.carrier_up_no_speed"
	// ReasonLinkCarrierDown: the interface lost carrier.
	ReasonLinkCarrierDown ReasonCode = "link.carrier_down"
	// ReasonLinkOperstateUnknownCarrier: operstate says up but carrier is
	// unknown, which is contradictory.
	ReasonLinkOperstateUnknownCarrier ReasonCode = "link.operstate_up_carrier_unknown"
	// ReasonLinkNoInformation: the interface reported neither carrier nor
	// operstate.
	ReasonLinkNoInformation ReasonCode = "link.no_information"
	// ReasonLinkCarrierChanged: the carrier state changed.
	ReasonLinkCarrierChanged ReasonCode = "link.carrier_changed"
	// ReasonLinkSpeedChanged: the negotiated speed changed.
	ReasonLinkSpeedChanged ReasonCode = "link.speed_changed"

	// --- discovery and identity outcomes ---

	// ReasonAwaitingFirstProbe: a probe was created but has not run yet.
	ReasonAwaitingFirstProbe ReasonCode = "probe.awaiting_first"
	// ReasonAwaitingFirstCheck: the device has not been checked for
	// reachability yet.
	ReasonAwaitingFirstCheck ReasonCode = "device.awaiting_first_check"
	// ReasonNotYetProbed: a manually created device has not been probed.
	ReasonNotYetProbed ReasonCode = "device.not_yet_probed"
	// ReasonUnknownIntermediate: several devices were observed behind one
	// port and the device in front of them is not known.
	ReasonUnknownIntermediate ReasonCode = "topology.unknown_intermediate"

	// --- anomalies ---

	// ReasonAnomalyNoCarrier: an interface has no carrier.
	ReasonAnomalyNoCarrier ReasonCode = "anomaly.no_carrier"
	// ReasonAnomalyDevicesOffline: some devices failed several consecutive
	// reachability checks.
	ReasonAnomalyDevicesOffline ReasonCode = "anomaly.devices_offline"
	// ReasonAnomalyDevicesUnknown: not enough evidence to say whether some
	// devices are reachable.
	ReasonAnomalyDevicesUnknown ReasonCode = "anomaly.devices_unknown"

	// --- anomaly details ---
	//
	// An anomaly carries a title and an explanatory line under it. Both are
	// sentences, so both need a code: a title-only code would leave the
	// detail in English while its title was translated.

	// ReasonAnomalyNoCarrierDetail: why the interface has no carrier.
	ReasonAnomalyNoCarrierDetail ReasonCode = "anomaly.no_carrier_detail"
	// ReasonAnomalyDevicesOfflineDetail: why the devices are counted offline.
	ReasonAnomalyDevicesOfflineDetail ReasonCode = "anomaly.devices_offline_detail"
	// ReasonAnomalyDevicesUnknownDetail: why the devices' state is unknown.
	ReasonAnomalyDevicesUnknownDetail ReasonCode = "anomaly.devices_unknown_detail"
)

// Reason carries a translatable reason: a code plus the parameters needed to
// fill in the message.
//
// Params holds the interpolated values only (a failure count, an interface
// name, a source id). The code itself says which template applies, so the
// frontend can look up "probe.unreachable_consecutive" and fill in
// "{failures} consecutive probes".
type Reason struct {
	// Code is the stable machine-readable identifier.
	Code ReasonCode `json:"code,omitempty"`
	// Params holds the values the template refers to.
	Params map[string]string `json:"params,omitempty"`
}

// ReasonOrEmpty returns r, or the zero Reason when r is nil.
func ReasonOrEmpty(r *Reason) Reason {
	if r == nil {
		return Reason{}
	}
	return *r
}

// ReasonParams returns the parameter map, never nil, so callers can read it
// without a nil check.
func (r Reason) ReasonParams() map[string]string {
	if r.Params == nil {
		return map[string]string{}
	}
	return r.Params
}
