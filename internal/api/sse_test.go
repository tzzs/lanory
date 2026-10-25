// Tests for the SSE wire format.
//
// The frames written here are parsed by web/src/hooks/useRealtime.ts, which
// splits the stream on blank lines, strips one leading space from each value and
// joins several `data:` lines with "\n". Those are the SSE rules; the assertions
// below pin the bytes this server emits so the two sides cannot drift apart.
//
// Every assertion is on exact output rather than a substring: "contains an id
// line" would still pass on a frame the client cannot reassemble.

package api

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// sseRecorder is a response writer that captures bytes and counts flushes.
//
// writeSSEFrame takes an http.ResponseWriter plus a http.Flusher, so a test that
// wants to inspect the bytes needs both on one value.
type sseRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *sseRecorder) Flush() { r.flushes++ }

// TestSSEFrameExactBytes pins the byte layout of a normal event frame.
//
// Three lines — id, event, data — followed by the blank line that terminates the
// frame. The data line carries compact JSON with no trailing newline of its own,
// which is what the client's JSON.parse expects.
func TestSSEFrameExactBytes(t *testing.T) {
	rec := newSSERecorder()
	ev := sseEvent{
		Type:      "device.online",
		Seq:       42,
		Timestamp: "2026-10-19T20:12:46.517Z",
		Payload:   map[string]string{"reason": "probe"},
	}
	if !writeSSEFrame(rec, rec, ev) {
		t.Fatal("writeSSEFrame reported a write failure")
	}

	want := "id: 42\n" +
		"event: device.online\n" +
		`data: {"type":"device.online","seq":42,"timestamp":"2026-10-19T20:12:46.517Z","payload":{"reason":"probe"}}` + "\n" +
		"\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("frame bytes mismatch\n got: %q\nwant: %q", got, want)
	}
	if rec.flushes != 1 {
		t.Errorf("flushes = %d, want 1: a frame must be flushed so a proxy cannot buffer it", rec.flushes)
	}
}

// TestSSECommentFrameExactBytes pins the heartbeat frame.
//
// A comment line is the only frame an idle stream sends; it starts with a colon
// and is terminated by a blank line. The client drops it.
func TestSSECommentFrameExactBytes(t *testing.T) {
	rec := newSSERecorder()
	if !writeComment(rec, rec, "heartbeat") {
		t.Fatal("writeComment reported a write failure")
	}
	if got, want := rec.Body.String(), ": heartbeat\n\n"; got != want {
		t.Fatalf("comment frame = %q, want %q", got, want)
	}
	if rec.flushes != 1 {
		t.Errorf("flushes = %d, want 1", rec.flushes)
	}
}

// TestSSEDataNeverContainsRawNewline covers the multi-line `data:` question.
//
// SSE splits a `data:` field on newlines and the client rejoins the fragments
// with "\n", so a single data line carrying an embedded newline would be split
// by the reader and then rejoined by the client — harmless only if the split
// points happen to line up. The guarantee this writer provides is stronger and
// simpler: the payload is compact JSON, in which every newline is escaped as the
// two characters `\` `n` and therefore never appears literally. One data line
// per frame, no continuation to reassemble.
//
// The test pins that: a payload that *would* be multi-line if it were pretty
// printed still goes out on exactly one data line, and the client's
// join-then-parse yields the original value back.
func TestSSEDataNeverContainsRawNewline(t *testing.T) {
	// A json.RawMessage is copied verbatim (compacted) rather than re-encoded,
	// so it is the payload most likely to smuggle a newline into the frame.
	ev := sseEvent{
		Type: "topology.updated",
		Seq:  8,
		Payload: json.RawMessage(`{
  "reason": "discovery",
  "note": "two\nlines"
}`),
	}

	rec := newSSERecorder()
	if !writeSSEFrame(rec, rec, ev) {
		t.Fatal("writeSSEFrame failed on a payload containing newlines")
	}
	got := rec.Body.String()

	// Exactly one terminator, and nothing after it: one frame, not two.
	if strings.Count(got, "\n\n") != 1 || !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("frame is not terminated by exactly one blank line: %q", got)
	}
	body := strings.TrimSuffix(got, "\n\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		t.Fatalf("frame has %d lines, want 3 (id, event, data):\n%q", len(lines), got)
	}
	for i, prefix := range []string{"id: ", "event: ", "data: "} {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Fatalf("line %d = %q, want the %q field", i, lines[i], strings.TrimSpace(prefix))
		}
	}

	// The single data line must be valid JSON on its own, and must round-trip.
	data := strings.TrimPrefix(lines[2], "data: ")
	var decoded struct {
		Type    string `json:"type"`
		Seq     uint64 `json:"seq"`
		Payload struct {
			Reason string `json:"reason"`
			Note   string `json:"note"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(data), &decoded); err != nil {
		t.Fatalf("the data line is not JSON on its own: %v\n%s", err, data)
	}
	if decoded.Type != "topology.updated" || decoded.Seq != 8 {
		t.Errorf("envelope = %q seq %d", decoded.Type, decoded.Seq)
	}
	if decoded.Payload.Reason != "discovery" || decoded.Payload.Note != "two\nlines" {
		t.Errorf("payload did not survive the frame: %+v", decoded.Payload)
	}
}

// TestSSEFrameRejectsUnencodablePayload checks that a payload which cannot be
// marshalled writes nothing at all.
//
// A half-written frame would desynchronise the client: it would see a data line
// without its terminator, hold it until the next frame, and then dispatch the
// two concatenated. Nothing must reach the wire, and the failure must be
// reported so the caller stops.
func TestSSEFrameRejectsUnencodablePayload(t *testing.T) {
	rec := newSSERecorder()
	// A raw newline inside a JSON string literal is not valid JSON, so
	// json.Marshal refuses the whole payload.
	bad := sseEvent{
		Type:    "topology.updated",
		Seq:     9,
		Payload: json.RawMessage("{\"reason\":\"line one\nline two\"}"),
	}
	if writeSSEFrame(rec, rec, bad) {
		t.Fatalf("writeSSEFrame accepted an unencodable payload and wrote %q", rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("a rejected payload must not write a partial frame, got %q", rec.Body.String())
	}
	if rec.flushes != 0 {
		t.Errorf("flushes = %d, want 0 for a frame that was never written", rec.flushes)
	}
}

// TestSSELastEventIDReplayWritesOnlyNewerEvents is the contract this stream is
// supposed to honour: a reconnect carries the last sequence the client
// dispatched, and only what came after it is written.
//
// It is skipped because the server does not currently do that. See
// TestSSELastEventIDReplayRecordsCurrentDuplicate below for the observed
// behaviour, and .sdd/briefs/task-12-report.md for the defect. The assertion is
// left in place so the fix is verified by un-skipping one line, not by writing a
// new test.
func TestSSELastEventIDReplayWritesOnlyNewerEvents(t *testing.T) {
	t.Skip("known defect: the bus replays its whole buffer into the subscriber " +
		"channel before the handler applies the Last-Event-ID filter, so events at " +
		"or below the client's last sequence are re-sent (see task-12-report.md)")

	app := newTestApp(t)
	// Seeding published seq 1; two more make the retained buffer 1,2,3 so
	// "seq > 2" has exactly one answer.
	app.Bus().Publish(events.TypeTopologyUpdated, map[string]string{"reason": "two"})
	app.Bus().Publish(events.TypeTopologyUpdated, map[string]string{"reason": "three"})

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	id, event, data := openStreamWithLastEventID(t, srv.URL, "2")
	if id != "3" {
		t.Fatalf("first frame id = %q, want 3: an event the client already saw must not be replayed (data: %s)", id, data)
	}
	if event != events.TypeTopologyUpdated {
		t.Errorf("replayed event = %q, want %q", event, events.TypeTopologyUpdated)
	}
}

// TestSSELastEventIDReplayRecordsCurrentDuplicate pins what the stream actually
// sends today, byte for byte, so the defect cannot change silently and cannot be
// mistaken for a working replay.
//
// Given a retained buffer of 1,2,3 and Last-Event-ID: 2, the server writes the
// handler's own filtered replay first (seq 3), then the bus's unfiltered buffer
// replay (seq 1, 2, 3). The client therefore receives a duplicate of the event it
// just acknowledged, in the wrong order: 3, 1, 2, 3. The browser hook dispatches
// every frame, so this is visible as repeated device.updated / topology.updated
// handling after a reconnect.
//
// When the defect is fixed, this test must fail and be deleted together with the
// skip on TestSSELastEventIDReplayWritesOnlyNewerEvents.
func TestSSELastEventIDReplayRecordsCurrentDuplicate(t *testing.T) {
	app := newTestApp(t)
	app.Bus().Publish(events.TypeTopologyUpdated, map[string]string{"reason": "two"})
	app.Bus().Publish(events.TypeTopologyUpdated, map[string]string{"reason": "three"})

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	reader, body := openStream(t, srv.URL, "2")
	defer body.Close()

	var ids []string
	for i := 0; i < 4; i++ {
		id, _, _ := readFrameWithin(t, reader, 5*time.Second)
		ids = append(ids, id)
	}
	// seq 3 (the handler's filtered replay), then the bus's whole buffer
	// (1, 2, 3). A correct implementation writes only "3".
	if got, want := strings.Join(ids, ","), "3,1,2,3"; got != want {
		t.Fatalf("frame ids = %s, want %s", got, want)
	}
	if ids[len(ids)-1] != "3" {
		t.Fatalf("expected the trailing duplicate of seq 3, got %v", ids)
	}
}

// openStreamWithLastEventID connects, replays from the given sequence and
// returns the first frame.
func openStreamWithLastEventID(t *testing.T, baseURL, lastID string) (id, event, data string) {
	t.Helper()
	reader, body := openStream(t, baseURL, lastID)
	defer body.Close()
	return readFrameWithin(t, reader, 5*time.Second)
}

// openStream opens the SSE stream with a Last-Event-ID header.
func openStream(t *testing.T, baseURL, lastID string) (*bufio.Reader, io.ReadCloser) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("stream status = %d, want 200", res.StatusCode)
	}
	return bufio.NewReader(res.Body), res.Body
}

// TestSSEStreamRefusesQueryStringToken checks the authentication contract the
// browser client depends on.
//
// EventSource cannot set an Authorization header, so a token in the query string
// is the usual workaround; Lanory refuses it because URLs end up in logs and in
// the Referer header. The stream is therefore fetch-only: a caller that still
// passes ?token= gets a 401 instead of a stream, which is exactly what
// docs/api.md and docs/security.md tell a client to expect.
func TestSSEStreamRefusesQueryStringToken(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.Enabled = true
	cfg.Auth.Token = "correct-token"
	app := newTestAppWithConfig(t, cfg)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/v1/events/stream?token=correct-token")
	if err != nil {
		t.Fatalf("GET stream with a query token: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a token in the query string\nbody: %s", res.StatusCode, body)
	}
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Error("a refused stream must not be served as text/event-stream")
	}
	var envelope apiError
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("401 body is not the error envelope: %v\n%s", err, body)
	}
	if envelope.Code != CodeUnauthorized {
		t.Errorf("code = %q, want %q", envelope.Code, CodeUnauthorized)
	}

	// The same request with the header is served, so the refusal above is about
	// the transport of the token, not about the route being closed.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer correct-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream with a bearer token: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a bearer token", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	id, event, _ := readFrameWithin(t, bufio.NewReader(res.Body), 5*time.Second)
	if id == "" || event == "" {
		t.Errorf("first frame = id %q event %q, want both set", id, event)
	}
}

// TestSSEStreamFrameShapeMatchesDocumentation checks the documented envelope:
// type, seq, timestamp and payload on every frame.
func TestSSEStreamFrameShapeMatchesDocumentation(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/v1/events/stream")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer res.Body.Close()

	_, _, data := readFrameWithin(t, bufio.NewReader(res.Body), 5*time.Second)
	var payload struct {
		Type      string          `json:"type"`
		Seq       uint64          `json:"seq"`
		Timestamp string          `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("data is not JSON: %v\n%s", err, data)
	}
	if payload.Type != string(model.EventDeviceOnline) {
		t.Errorf("type = %q, want %q", payload.Type, model.EventDeviceOnline)
	}
	if payload.Seq == 0 {
		t.Error("seq must be non-zero so a client can detect a gap")
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", payload.Timestamp); err != nil {
		t.Errorf("timestamp = %q, want the documented millisecond RFC 3339 form: %v", payload.Timestamp, err)
	}
	if len(payload.Payload) == 0 {
		t.Error("payload must be present")
	}
}

// readFrameWithin reads one frame, failing rather than blocking forever.
func readFrameWithin(t *testing.T, r *bufio.Reader, timeout time.Duration) (id, event, data string) {
	t.Helper()
	type frame struct{ id, event, data string }
	done := make(chan frame, 1)
	go func() {
		i, e, d := readFrame(t, r)
		done <- frame{i, e, d}
	}()
	select {
	case f := <-done:
		if f.id == "" && f.event == "" && f.data == "" {
			t.Fatal("no frame arrived")
		}
		return f.id, f.event, f.data
	case <-time.After(timeout):
		t.Fatal("timed out waiting for an SSE frame")
		return "", "", ""
	}
}
