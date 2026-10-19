package topology

import (
	"math"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

func TestBaseConfidenceValues(t *testing.T) {
	// The base values are part of the product contract: they are what the UI
	// explains to the user. Pin them so a refactor cannot quietly rebalance the
	// evidence model.
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"LLDP", ConfidenceLLDP, 0.95},
		{"DHCP", ConfidenceDHCP, 0.85},
		{"Neighbor", ConfidenceNeighbor, 0.80},
		{"FDB", ConfidenceFDB, 0.70},
		{"MDNS", ConfidenceMDNS, 0.60},
		{"Subnet", ConfidenceSubnet, 0.30},
		{"Manual", ConfidenceManual, 1.0},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}

	// The ordering is meaningful: a stronger evidence class must never be
	// worth less than a weaker one.
	order := []float64{
		ConfidenceManual, ConfidenceLLDP, ConfidenceDHCP, ConfidenceNeighbor,
		ConfidenceFDB, ConfidenceMDNS, ConfidenceSubnet,
	}
	for i := 1; i < len(order); i++ {
		if order[i] > order[i-1] {
			t.Fatalf("evidence weights are out of order at index %d: %v > %v", i, order[i], order[i-1])
		}
	}
}

func TestCombineEmpty(t *testing.T) {
	// No evidence is 0, not a default. An edge with nothing behind it must look
	// like it.
	if got := Combine(nil); got != 0 {
		t.Fatalf("Combine(nil) = %v, want 0", got)
	}
	if got := Combine([]Evidence{}); got != 0 {
		t.Fatalf("Combine([]) = %v, want 0", got)
	}
}

func TestCombineSingleSource(t *testing.T) {
	for _, tc := range []struct {
		src  model.DiscoverySource
		want float64
	}{
		{model.SourceLLDP, ConfidenceLLDP},
		{model.SourceDHCP, ConfidenceDHCP},
		{model.SourceARP, ConfidenceNeighbor},
		{model.SourceBridgeFDB, ConfidenceFDB},
		{model.SourceMDNS, ConfidenceMDNS},
	} {
		got := Combine([]Evidence{{Source: tc.src, Confidence: tc.want}})
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("single %s = %v, want %v", tc.src, got, tc.want)
		}
	}
}

// FDB alone must be weaker than FDB plus DHCP. This is the headline assertion
// for multi-source merging.
func TestCombineFDBLessThanFDBPlusDHCP(t *testing.T) {
	fdbOnly := Combine([]Evidence{{Source: model.SourceBridgeFDB, Confidence: ConfidenceFDB}})
	both := Combine([]Evidence{
		{Source: model.SourceBridgeFDB, Confidence: ConfidenceFDB},
		{Source: model.SourceDHCP, Confidence: ConfidenceDHCP},
	})
	if !(fdbOnly < both) {
		t.Fatalf("FDB only (%v) must be weaker than FDB+DHCP (%v)", fdbOnly, both)
	}
	// Noisy-OR: 1 - (1-0.7)(1-0.85) = 0.955.
	want := 0.955
	if math.Abs(both-want) > 1e-9 {
		t.Fatalf("FDB+DHCP = %v, want %v", both, want)
	}
}

// Combining more independent sources must never lower the result.
func TestCombineIsMonotonic(t *testing.T) {
	var evidence []Evidence
	prev := 0.0
	for _, src := range []model.DiscoverySource{
		model.SourceMDNS, model.SourceBridgeFDB, model.SourceARP,
		model.SourceDHCP, model.SourceLLDP,
	} {
		evidence = append(evidence, Evidence{Source: src, Confidence: BoostForSource(src)})
		got := Combine(evidence)
		if got < prev {
			t.Fatalf("confidence dropped from %v to %v after adding %s", prev, got, src)
		}
		if got > 1 {
			t.Fatalf("confidence exceeded 1: %v", got)
		}
		prev = got
	}
}

// The same source twice is one piece of evidence, not two.
func TestCombineIgnoresDuplicateSource(t *testing.T) {
	once := Combine([]Evidence{{Source: model.SourceBridgeFDB, Confidence: ConfidenceFDB}})
	twice := Combine([]Evidence{
		{Source: model.SourceBridgeFDB, Confidence: ConfidenceFDB},
		{Source: model.SourceBridgeFDB, Confidence: ConfidenceFDB},
	})
	if math.Abs(once-twice) > 1e-9 {
		t.Fatalf("duplicate FDB evidence changed the result: %v vs %v", once, twice)
	}
}

// A single weak source must never produce a high confidence.
//
// "High" here means it must not read as confirmed, and it must not cross the
// unverified threshold as if it were a real observation. mDNS sits exactly on
// the threshold, which is the intended boundary: it is a real observation, but a
// weak one, so an mDNS-only edge is drawn dashed.
func TestCombineWeakSourceNeverHigh(t *testing.T) {
	got := Combine([]Evidence{{Source: model.SourceMDNS, Confidence: BoostForSource(model.SourceMDNS)}})
	if got > UnverifiedThreshold {
		t.Errorf("a single mDNS observation reached %v, above the threshold %v", got, UnverifiedThreshold)
	}
	if Classify(got) == "confirmed" {
		t.Errorf("a single mDNS observation was classified as confirmed")
	}

	// Subnet inference is the weakest of all and stays well below the threshold.
	subnet := Combine([]Evidence{{Source: model.SourceOpenWRT, Confidence: ConfidenceSubnet}})
	if subnet >= UnverifiedThreshold {
		t.Errorf("subnet inference reached %v", subnet)
	}
	if Classify(subnet) != "inferred" {
		t.Errorf("subnet inference classified as %q, want inferred", Classify(subnet))
	}

	// One weak source plus one strong source is still not confirmed unless the
	// strong source alone would be, which is the point of the noisy-OR.
	fdbPlusMDNS := Combine([]Evidence{
		{Source: model.SourceBridgeFDB, Confidence: ConfidenceFDB},
		{Source: model.SourceMDNS, Confidence: ConfidenceMDNS},
	})
	if fdbPlusMDNS > ConfidenceLLDP {
		t.Errorf("FDB+mDNS reached %v, above LLDP", fdbPlusMDNS)
	}
	if fdbPlusMDNS <= ConfidenceFDB {
		t.Errorf("FDB+mDNS (%v) did not improve on FDB alone", fdbPlusMDNS)
	}
}

// A manual assertion is 1.0 and is never diluted by other evidence.
func TestCombineManualIsOne(t *testing.T) {
	got := Combine([]Evidence{{Source: model.SourceManual, Confidence: ConfidenceManual}})
	if got != ConfidenceManual {
		t.Fatalf("manual = %v, want 1.0", got)
	}
	// Adding weaker evidence must not lower it.
	mixed := Combine([]Evidence{
		{Source: model.SourceManual, Confidence: ConfidenceManual},
		{Source: model.SourceMDNS, Confidence: ConfidenceMDNS},
	})
	if mixed != ConfidenceManual {
		t.Fatalf("manual + mdns = %v, want 1.0", mixed)
	}
	// A manual assertion expressed at a lower confidence still wins, because
	// the source itself is authoritative.
	weakManual := Combine([]Evidence{{Source: model.SourceManual, Confidence: 0.5}})
	if weakManual <= 0.9 {
		t.Fatalf("manual source = %v, expected a high value", weakManual)
	}
}

func TestCombineClampsInput(t *testing.T) {
	got := Combine([]Evidence{{Source: model.SourceARP, Confidence: 5}})
	if got > 1 {
		t.Fatalf("confidence = %v, want <= 1", got)
	}
	got = Combine([]Evidence{{Source: model.SourceARP, Confidence: -1}})
	if got != 0 {
		t.Fatalf("negative confidence = %v, want 0", got)
	}
}

func TestBoostForSource(t *testing.T) {
	for _, tc := range []struct {
		src  model.DiscoverySource
		want float64
	}{
		{model.SourceLLDP, ConfidenceLLDP},
		{model.SourceDHCP, ConfidenceDHCP},
		{model.SourceARP, ConfidenceNeighbor},
		{model.SourceNDP, ConfidenceNeighbor},
		{model.SourceBridgeFDB, ConfidenceFDB},
		{model.SourceMDNS, ConfidenceMDNS},
		{model.SourceManual, ConfidenceManual},
		{model.SourceOpenWRT, ConfidenceSubnet},
		{model.DiscoverySource("nonsense"), ConfidenceSubnet},
	} {
		if got := BoostForSource(tc.src); got != tc.want {
			t.Errorf("BoostForSource(%s) = %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		conf float64
		want string
	}{
		{1.0, "confirmed"},
		{0.95, "confirmed"},
		{0.8, "observed"},
		{0.7, "observed"},
		{0.3, "inferred"},
		{0.1, "inferred"},
		{0, "unknown"},
	} {
		if got := Classify(tc.conf); got != tc.want {
			t.Errorf("Classify(%v) = %q, want %q", tc.conf, got, tc.want)
		}
	}
}
