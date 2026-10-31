package model

import "testing"

// TestLinkStatusForMatchesCarrierFirstSemantics pins the status rule shared by
// the live observer and the store-backed provider. Carrier is the physical
// signal, so an interface with carrier remains link_up even if operstate is
// contradictory.
func TestLinkStatusForMatchesCarrierFirstSemantics(t *testing.T) {
	cases := []struct {
		name    string
		carrier bool
		oper    OperState
		speed   int
		want    EdgeStatus
	}{
		{name: "carrier up with operstate down", carrier: true, oper: OperDown, want: EdgeStatusLinkUp},
		{name: "carrier up with unknown operstate", carrier: true, oper: OperUnknown, want: EdgeStatusLinkUp},
		{name: "carrier down", carrier: false, oper: OperDown, want: EdgeStatusLinkDown},
		{name: "no carrier", carrier: false, oper: OperNoCarrier, want: EdgeStatusLinkDown},
		{name: "operstate up without carrier", carrier: false, oper: OperUp, want: EdgeStatusUnverified},
		{name: "no information", carrier: false, oper: OperUnknown, want: EdgeStatusUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := LinkStatusFor(tc.carrier, tc.oper, tc.speed)
			if got != tc.want {
				t.Fatalf("LinkStatusFor(%v, %q, %d) = %q, want %q (%s)", tc.carrier, tc.oper, tc.speed, got, tc.want, reason)
			}
			if reason == "" {
				t.Fatal("LinkStatusFor returned an empty reason")
			}
		})
	}
}
