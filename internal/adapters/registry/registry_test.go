package registry

import (
	"context"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/adapters"
)

// TestEveryShippedAdapterBuilds checks that each registered vendor can actually
// be instantiated, so a typo in a factory name cannot ship.
func TestEveryShippedAdapterBuilds(t *testing.T) {
	r := New()
	vendors := r.Vendors()
	if len(vendors) != 2 {
		t.Fatalf("Vendors() = %v, want generic and xiaomi", vendors)
	}
	for _, vendor := range vendors {
		a, err := r.Build(adapters.Config{
			ID:      vendor + ":test",
			Vendor:  vendor,
			Address: "127.0.0.1",
		})
		if err != nil {
			t.Errorf("Build(%s): %v", vendor, err)
			continue
		}
		if a.Name() != vendor {
			t.Errorf("adapter for %s reports Name() = %q", vendor, a.Name())
		}
		if a.Manufacturer() == "" {
			t.Errorf("adapter for %s reports an empty manufacturer", vendor)
		}
		if len(a.Capabilities()) == 0 {
			t.Errorf("adapter for %s claims no capability", vendor)
		}
	}
}

// TestUnconfiguredAdaptersAreSafe is the end-to-end degradation test: building
// and probing every shipped adapter against an address that cannot answer must
// produce zero results and a nil error, and must never guess a credential.
//
// The per-adapter request rule differs on purpose:
//
//   - the Xiaomi adapter needs a credential to do anything, so it makes ZERO
//     requests without one. Trying an anonymous or default-password request
//     would be credential guessing, which the spec forbids;
//   - the Generic adapter's whole job is to find out whether a management
//     endpoint exists, and it does that with unauthenticated requests that carry
//     no Authorization header, so it is allowed to make them.
func TestUnconfiguredAdaptersAreSafe(t *testing.T) {
	r := New()
	for _, vendor := range r.Vendors() {
		a, err := r.Build(adapters.Config{
			ID:      vendor + ":test",
			Vendor:  vendor,
			Address: "192.0.2.1", // TEST-NET-1: never routed
			Timeout: 150 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("Build(%s): %v", vendor, err)
		}
		results, err := a.Probe(context.Background())
		if err != nil {
			t.Errorf("%s: Probe returned an error: %v", vendor, err)
		}
		if len(results) != 0 {
			t.Errorf("%s: Probe returned %d results with no credential", vendor, len(results))
		}
		st := a.Status()
		if st.Authenticated {
			t.Errorf("%s: Status().Authenticated = true with no credential", vendor)
		}
		if vendor == "xiaomi" && st.Requests != 0 {
			t.Errorf("xiaomi: Probe made %d requests with no credential, want 0", st.Requests)
		}
		if st.Error != "" {
			t.Errorf("%s: an unreachable device must not produce an error, got %q", vendor, st.Error)
		}
	}
}

// TestVendorIsolation checks that a broken config for one vendor leaves the
// other usable.
func TestVendorIsolation(t *testing.T) {
	r := New()
	if _, err := r.Build(adapters.Config{Vendor: "xiaomi"}); err == nil {
		t.Fatal("Build without an address succeeded")
	}
	a, err := r.Build(adapters.Config{ID: "g", Vendor: "generic", Address: "192.0.2.1"})
	if err != nil {
		t.Fatalf("generic adapter broke after a xiaomi config error: %v", err)
	}
	if a.Name() != "generic" {
		t.Errorf("Name() = %q", a.Name())
	}
}
