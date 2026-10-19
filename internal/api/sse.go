package api

import (
	"encoding/json"

	"github.com/lanory/lanory/internal/events"
)

// SSE frame helpers.
//
// The HTTP layer only needs the wire shape of an event, so events.Event is
// adapted into sseEvent here. Keeping the adapter in one place means the SSE
// writer has no dependency on the bus internals and can be tested with plain
// values.

// sseEvent is the JSON payload written to a data: frame.
//
// The field names are the API contract (see docs/api.md): type, seq, timestamp
// and payload.
type sseEvent struct {
	Type      string `json:"type"`
	Seq       uint64 `json:"seq"`
	Timestamp string `json:"timestamp"`
	Payload   any    `json:"payload,omitempty"`
}

// toSSEEvent converts a bus event into its SSE representation.
func toSSEEvent(e events.Event) sseEvent {
	return sseEvent{
		Type:      e.Type,
		Seq:       e.Seq,
		Timestamp: e.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		Payload:   e.Payload,
	}
}

// jsonMarshal renders v as compact JSON for a data: frame.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
