package topology

import (
	"math"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// Base confidence by evidence class.
//
// These are the starting points, not the final answers. An edge's confidence is
// the combination of every independent source that observed it, which is what
// Combine is for. The values encode how much a single observation of that class
// is worth on its own:
//
//   - LLDP is an explicit advertisement from the peer. Nothing beats it.
//   - DHCP is the local server's own assertion: "this MAC owns this address".
//   - Neighbour (ARP/NDP) is an exchange that actually happened.
//   - FDB is the bridge having learned the MAC on a port, which is strong but
//     says nothing about what is beyond the port.
//   - mDNS is a voluntary self-announcement: easy to spoof, easy to be absent.
//   - Subnet membership is an inference from addresses alone and is the weakest
//     thing Lanory will report.
const (
	// ConfidenceLLDP: the peer announced itself.
	ConfidenceLLDP = 0.95
	// ConfidenceDHCP: the DHCP server assigned the address to the MAC.
	ConfidenceDHCP = 0.85
	// ConfidenceNeighbor: an active ARP/NDP exchange.
	ConfidenceNeighbor = 0.80
	// ConfidenceFDB: the bridge learned the MAC on this port.
	ConfidenceFDB = 0.70
	// ConfidenceMDNS: an mDNS announcement.
	ConfidenceMDNS = 0.60
	// ConfidenceSubnet: both endpoints share a subnet.
	ConfidenceSubnet = 0.30
	// ConfidenceManual: the user asserted the relation.
	ConfidenceManual = 1.0
)

// UnverifiedThreshold is the confidence below which an edge is drawn dashed.
// It mirrors the config default; the engine uses the configured value when the
// Builder was given one.
const UnverifiedThreshold = 0.6

// Evidence is one independent observation backing an edge.
//
// Independence matters: two FDB entries from the same bridge dump are one
// observation, not two. The Source field is what makes that check possible, so
// callers must set it honestly.
type Evidence struct {
	// Source is the collector that produced the observation.
	Source model.DiscoverySource
	// Confidence is that source's confidence in the relation.
	Confidence float64
	// ObservedAt is when the observation was made.
	ObservedAt time.Time
	// Description explains the evidence, e.g. "eth2 learned b0:82:... at 100M".
	Description string
}

// Combine merges independent evidence into one confidence.
//
// The rules, in order:
//
//  1. No evidence yields 0, not a default. An edge with nothing behind it must
//     look like it.
//  2. Manual evidence is 1.0 and wins outright. A user-confirmed relation is
//     never re-derived.
//  3. Otherwise the confidences combine as a noisy-OR: 1 - Π(1 - cᵢ). Independent
//     sources reinforce each other, so FDB alone (0.70) plus DHCP (0.85) gives
//     0.955, while a duplicate of the same source does not move the number at
//     all.
//  4. The result never exceeds 1.0, and never reaches it without either a manual
//     confirmation or genuinely overwhelming corroboration.
//
// The combination is deliberately monotone: adding evidence can only raise the
// value, so a re-run that sees the same facts reproduces the same number and the
// UI does not flicker.
func Combine(evidence []Evidence) float64 {
	if len(evidence) == 0 {
		return 0
	}

	// A manual assertion settles it. The source is authoritative, so the stated
	// confidence does not have to reach 1.0 for the relation to be treated as
	// user-owned: a manual edge is by definition something a person asserted.
	for _, e := range evidence {
		if e.Source == model.SourceManual {
			return ConfidenceManual
		}
	}

	// One entry per source: the strongest observation each source made. Two
	// FDB rows about the same port are not two pieces of evidence.
	best := map[model.DiscoverySource]float64{}
	for _, e := range evidence {
		c := clamp01(e.Confidence)
		if c <= 0 {
			// A source that explicitly disclaims the relation is ignored
			// rather than treated as weak support.
			continue
		}
		if c > best[e.Source] {
			best[e.Source] = c
		}
	}
	if len(best) == 0 {
		return 0
	}

	// Noisy-OR: at least one source is right.
	survival := 1.0
	for _, c := range best {
		survival *= 1.0 - c
	}
	combined := 1.0 - survival
	if combined > 1 {
		combined = 1
	}
	// Snap away the floating point noise that 1-(1-c) introduces for a single
	// source (0.7 becomes 0.6999999999999999), so an exact base confidence stays
	// exactly at its declared value and comparisons against thresholds do not
	// flip on a rounding error.
	return snap(combined)
}

// snap rounds to six decimals, which is finer than any meaningful difference in
// confidence and coarse enough to remove IEEE-754 noise.
func snap(v float64) float64 {
	const scale = 1e6
	return math.Round(v*scale) / scale
}

// BoostForSource returns the base confidence a source's observation carries.
// It is the single place that maps a collector to a number, so the weights
// cannot drift between the build path and the update path.
func BoostForSource(src model.DiscoverySource) float64 {
	switch src {
	case model.SourceLLDP:
		return ConfidenceLLDP
	case model.SourceDHCP:
		return ConfidenceDHCP
	case model.SourceARP, model.SourceNDP:
		return ConfidenceNeighbor
	case model.SourceBridgeFDB:
		return ConfidenceFDB
	case model.SourceMDNS:
		return ConfidenceMDNS
	case model.SourceManual:
		return ConfidenceManual
	default:
		return ConfidenceSubnet
	}
}

// clamp01 keeps a confidence inside the valid range.
func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// Classify labels a confidence for the evidence panel.
//
// The buckets are for humans: "confirmed" for anything a person asserted or an
// overwhelming number of sources support, "observed" for a normal single-source
// fact, and "inferred" for the subnet-only guesses.
func Classify(confidence float64) string {
	switch {
	case confidence >= ConfidenceManual:
		return "confirmed"
	case confidence >= ConfidenceLLDP:
		return "confirmed"
	case confidence >= ConfidenceMDNS:
		// FDB, DHCP, neighbour and mDNS all mean "we observed this".
		return "observed"
	case confidence > 0:
		// Anything weaker than a real observation is an inference, including
		// subnet membership and low-confidence mDNS.
		return "inferred"
	}
	return "unknown"
}
