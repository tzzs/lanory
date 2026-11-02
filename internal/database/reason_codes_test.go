package database

import (
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// Reason codes survive the database round trip, including the JSON parameter
// map used by the frontend's translation templates.
func TestReasonCodeRoundTrip(t *testing.T) {
	s := newTestStore(t)

	dev := &model.Device{
		ID:          "dev-reason",
		Name:        "Test device",
		State:       model.StateOffline,
		StateReason: "unreachable for 3 consecutive probes from lanory via icmp with no recent success",
		StateReasonCode: &model.Reason{
			Code:   model.ReasonProbeUnreachableConsecutive,
			Params: map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp"},
		},
	}
	if err := s.UpsertDevice(dev); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDevice(dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StateReasonCode == nil || got.StateReasonCode.Code != model.ReasonProbeUnreachableConsecutive {
		t.Fatalf("reason code = %#v", got.StateReasonCode)
	}
	if got.StateReasonCode.Params["failures"] != "3" {
		t.Fatalf("reason params = %#v", got.StateReasonCode.Params)
	}

	event := &model.NetworkEvent{
		EventType:  model.EventDeviceOffline,
		DeviceID:   dev.ID,
		Message:    dev.StateReason,
		ReasonCode: dev.StateReasonCode,
	}
	if err := s.SaveEvent(event); err != nil {
		t.Fatal(err)
	}
	events, _, err := s.ListEvents(EventFilter{DeviceID: dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ReasonCode == nil || events[0].ReasonCode.Code != model.ReasonProbeUnreachableConsecutive {
		t.Fatalf("events = %#v", events)
	}
}

// TestDecodeReasonAcceptsNumbersAndStrings asserts that the decoder copes with
// both shapes the parameter map can take.
//
// The backfill writes its JSON with SQLite's json_object, which types a
// numeric parameter as a JSON number. Go's own encoder writes every parameter
// as a JSON string. A row can therefore hold either, and the frontend expects
// strings in both cases, so the number has to be normalised on the way out.
func TestDecodeReasonAcceptsNumbersAndStrings(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		want  model.ReasonCode
		param string
		value string
	}{
		{
			name:  "numeric parameter",
			raw:   `{"code":"probe.unreachable_consecutive","params":{"failures":12,"source":"lanory","protocol":"icmp"}}`,
			want:  model.ReasonProbeUnreachableConsecutive,
			param: "failures",
			value: "12",
		},
		{
			name:  "string parameter",
			raw:   `{"code":"probe.unreachable_consecutive","params":{"failures":"12","source":"lanory","protocol":"icmp"}}`,
			want:  model.ReasonProbeUnreachableConsecutive,
			param: "failures",
			value: "12",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeReason(tc.raw)
			if got == nil {
				t.Fatal("decodeReason returned nil")
			}
			if got.Code != tc.want {
				t.Fatalf("code = %q, want %q", got.Code, tc.want)
			}
			if got.Params[tc.param] != tc.value {
				t.Fatalf("param %q = %q, want %q", tc.param, got.Params[tc.param], tc.value)
			}
		})
	}

	// A row with no code, or with damaged JSON, has no reason: the reader
	// falls back to the English text.
	for _, raw := range []string{"", "{}", "not json", `{"code":"","params":{}}`} {
		if got := decodeReason(raw); got != nil {
			t.Fatalf("decodeReason(%q) = %#v, want nil", raw, got)
		}
	}
}
