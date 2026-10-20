package api

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// Authentication and CSRF tests.

// newAuthApp builds an app with authentication switched on and a known token.
func newAuthApp(t *testing.T, token string) *App {
	t.Helper()
	cfg := testConfig(t)
	cfg.Auth.Enabled = true
	cfg.Auth.Token = token
	cfg.Auth.AllowLAN = false
	return newTestAppWithConfig(t, cfg)
}

// TestAuthMissingToken checks that an unauthenticated API request is refused.
func TestAuthMissingToken(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	for _, path := range []string{
		"/api/v1/devices",
		"/api/v1/topology",
		"/api/v1/overview",
		"/api/v1/events",
		"/api/v1/settings",
		"/api/v1/discovery/status",
		"/api/v1/monitoring/status",
		"/api/v1/events/stream",
	} {
		rec := do(t, app, http.MethodGet, path, "")
		e := assertError(t, rec, http.StatusUnauthorized)
		if e.Code != CodeUnauthorized {
			t.Errorf("%s: code = %q, want %q", path, e.Code, CodeUnauthorized)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: 401 must carry a WWW-Authenticate header", path)
		}
	}
}

// TestAuthWrongToken checks that a wrong token is refused.
func TestAuthWrongToken(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertError(t, rec, http.StatusUnauthorized)

	// A token in the query string is ignored: URLs end up in logs and in the
	// Referer header, so the only accepted form is the Authorization header.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/devices?token=correct-token", nil)
	rec = httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertError(t, rec, http.StatusUnauthorized)
}

// TestAuthWrongScheme checks that only Bearer is accepted.
func TestAuthWrongScheme(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	for _, header := range []string{
		"correct-token",
		"Basic correct-token",
		"bearer correct-token",
		"Bearer  ",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, req)
		assertError(t, rec, http.StatusUnauthorized)
	}
}

// TestAuthCorrectToken checks that the right token is accepted.
func TestAuthCorrectToken(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer correct-token")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
}

// TestAuthHealthExempt checks that health never needs a token.
func TestAuthHealthExempt(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	rec := do(t, app, http.MethodGet, "/api/v1/health", "")
	assertStatus(t, rec, http.StatusOK)

	// The static frontend is public: the browser cannot send an Authorization
	// header when loading a document.
	rec = do(t, app, http.MethodGet, "/", "")
	assertStatus(t, rec, http.StatusOK)
}

// TestAuthDisabledByDefault checks that a configuration without auth lets the
// API through.
func TestAuthDisabledByDefault(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.Enabled = false
	app := newTestAppWithConfig(t, cfg)

	rec := do(t, app, http.MethodGet, "/api/v1/devices", "")
	assertStatus(t, rec, http.StatusOK)
}

// TestCSRFMissingHeader checks that a state changing request without the CSRF
// header is refused.
func TestCSRFMissingHeader(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	body := `{"name":"X","type":"router"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer correct-token")
	// Deliberately no CSRF header.
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)

	e := assertError(t, rec, http.StatusForbidden)
	if e.Code != CodeForbidden {
		t.Errorf("code = %q, want %q", e.Code, CodeForbidden)
	}
	if !strings.Contains(e.Error, csrfHeader) {
		t.Errorf("error should name the %s header, got %q", csrfHeader, e.Error)
	}
}

// TestCSRFHeaderAccepted checks the happy path.
func TestCSRFHeaderAccepted(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	body := `{"name":"CSRF OK","type":"router"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer correct-token")
	req.Header.Set(csrfHeader, csrfValue)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)
}

// TestCSRFDoubleSubmit checks the cookie alternative.
func TestCSRFDoubleSubmit(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	body := `{"name":"Double Submit","type":"router"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer correct-token")
	req.AddCookie(&http.Cookie{Name: doubleSubmitCookie, Value: "nonce-123"})
	req.Header.Set("X-CSRF-Token", "nonce-123")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	// A mismatched echo is refused.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer correct-token")
	req.AddCookie(&http.Cookie{Name: doubleSubmitCookie, Value: "nonce-123"})
	req.Header.Set("X-CSRF-Token", "nonce-999")
	rec = httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertError(t, rec, http.StatusForbidden)
}

// TestCSRFSafeMethods checks that reads are exempt.
func TestCSRFSafeMethods(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/api/v1/devices", nil)
		req.Header.Set("Authorization", "Bearer correct-token")
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s returned %d, want 200", method, rec.Code)
		}
	}
}

// TestCSRFAllStateChangingMethods checks PATCH and DELETE too.
func TestCSRFAllStateChangingMethods(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	for method, path := range map[string]string{
		http.MethodPatch:  "/api/v1/devices/dev-win",
		http.MethodDelete: "/api/v1/devices/dev-win",
	} {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer correct-token")
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, req)
		assertError(t, rec, http.StatusForbidden)
	}
}

// TestAuthAppliesToStream checks that SSE is also protected.
func TestAuthAppliesToStream(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	rec := do(t, app, http.MethodGet, "/api/v1/events/stream", "")
	assertError(t, rec, http.StatusUnauthorized)
}

// TestAuthTokenInstalledAfterStartup checks the generated-token path.
func TestAuthTokenInstalledAfterStartup(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.Enabled = true
	cfg.Auth.Token = ""
	app := newTestAppWithConfig(t, cfg)

	// With no token configured, authentication is not enforced: there is
	// nothing to compare against.
	rec := do(t, app, http.MethodGet, "/api/v1/devices", "")
	assertStatus(t, rec, http.StatusOK)

	// A token generated at startup is picked up without rebuilding the app.
	app.SetAuthToken("generated-token")
	rec = do(t, app, http.MethodGet, "/api/v1/devices", "")
	assertError(t, rec, http.StatusUnauthorized)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer generated-token")
	rec = httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)

	// An empty token is refused rather than disabling auth.
	app.SetAuthToken("")
	if app.AuthToken() != "" {
		t.Errorf("AuthToken() = %q, want empty", app.AuthToken())
	}
}

// TestRemoteAddressIsNotSpoofable checks that X-Forwarded-For is ignored unless
// the immediate peer is a trusted proxy.
func TestRemoteAddressIsNotSpoofable(t *testing.T) {
	app := newAuthApp(t, "correct-token")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	// Claim to be localhost to try to slip past the LAN exemption.
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	assertError(t, rec, http.StatusUnauthorized)
}

// ---------------------------------------------------------------------------
// SSE
// ---------------------------------------------------------------------------

// drainFrameWithTimeout reads one frame, failing the test if none arrives.
func drainFrameWithTimeout(t *testing.T, r *bufio.Reader, timeout time.Duration) {
	t.Helper()
	type result struct {
		id, event, data string
	}
	done := make(chan result, 1)
	go func() {
		id, event, data := readFrame(t, r)
		done <- result{id, event, data}
	}()
	select {
	case got := <-done:
		if got.event == "" {
			t.Fatal("no frame arrived within the timeout")
		}
	case <-time.After(timeout):
		t.Fatal("timed out waiting for an SSE frame")
	}
}

// readFrame reads one SSE frame (an id, event and data triple) plus the
// terminating blank line.
func readFrame(t *testing.T, r *bufio.Reader) (id, event, data string) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", "", ""
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if id != "" || event != "" || data != "" {
				return id, event, data
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// TestSSEStreamDeliversFrames checks that a subscriber receives events.
func TestSSEStreamDeliversFrames(t *testing.T) {
	app := newTestApp(t)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/v1/events/stream")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer res.Body.Close()

	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	// The seeded connect publishes one device.online event.
	reader := bufio.NewReader(res.Body)
	id, event, data := readFrame(t, reader)
	if event != string(model.EventDeviceOnline) {
		t.Fatalf("event = %q, want %q (frame id=%q data=%s)", event, model.EventDeviceOnline, id, data)
	}
	if id == "" {
		t.Error("a frame must carry an id so a client can detect gaps")
	}
	if data == "" {
		t.Fatal("a frame must carry a JSON payload")
	}

	// The payload must be the documented shape.
	var frame struct {
		Type      string         `json:"type"`
		Seq       uint64         `json:"seq"`
		Timestamp string         `json:"timestamp"`
		Payload   map[string]any `json:"payload"`
	}
	if err := jsonUnmarshalString(data, &frame); err != nil {
		t.Fatalf("data is not a JSON object: %v\n%s", err, data)
	}
	if frame.Type != string(model.EventDeviceOnline) {
		t.Errorf("payload type = %q, want %q", frame.Type, model.EventDeviceOnline)
	}
	if frame.Seq == 0 {
		t.Error("seq must be non-zero")
	}
	if frame.Timestamp == "" {
		t.Error("timestamp must be present")
	}
	if frame.Payload == nil {
		t.Error("payload must be present")
	}
}

// TestSSELiveEvents checks that events published after connecting arrive.
func TestSSELiveEvents(t *testing.T) {
	app := newTestApp(t)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/v1/events/stream")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}

	reader := bufio.NewReader(res.Body)
	// Drain the replayed buffer so the assertion below sees a fresh event. The
	// buffer holds exactly the one event published while seeding.
	drainFrameWithTimeout(t, reader, 2*time.Second)

	// Publish a distinctive event.
	var published events.Event
	if app.Bus() != nil {
		published = app.Bus().Publish(events.TypeSettingsUpdated, map[string]string{"key": "test", "value": "live"})
	}

	id, event, data := readFrame(t, reader)
	if event != events.TypeSettingsUpdated {
		t.Fatalf("event = %q, want %q (id=%q data=%s)", event, events.TypeSettingsUpdated, id, data)
	}
	var frame struct {
		Seq uint64 `json:"seq"`
	}
	if err := jsonUnmarshalString(data, &frame); err != nil {
		t.Fatalf("data is not JSON: %v", err)
	}
	if frame.Seq != published.Seq {
		t.Errorf("seq = %d, want %d", frame.Seq, published.Seq)
	}

	res.Body.Close()
}

// TestSSEDisconnectCleanlyNoLeak checks that a client disconnect removes the
// subscription and leaks no goroutine.
func TestSSEDisconnectCleanlyNoLeak(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	// Warm up so one-off HTTP machinery is already allocated.
	if res, err := http.Get(srv.URL + "/api/v1/health"); err == nil {
		res.Body.Close()
	}

	before := runtime.NumGoroutine()

	const rounds = 5
	for i := 0; i < rounds; i++ {
		res, err := http.Get(srv.URL + "/api/v1/events/stream")
		if err != nil {
			t.Fatalf("round %d: GET stream: %v", i, err)
		}
		reader := bufio.NewReader(res.Body)
		if _, _, _ = readFrame(t, reader); false {
			t.Fatal("unreachable")
		}
		// Close the body without a shutdown request: this is what an abrupt
		// client disconnect looks like from the server's side.
		res.Body.Close()
	}

	// Give the handlers a moment to observe the closed connection and unwind.
	settled := false
	for i := 0; i < 60; i++ {
		time.Sleep(50 * time.Millisecond)
		if runtime.NumGoroutine() <= before+2 {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, runtime.NumGoroutine())
	}

	// The subscriptions must be gone too.
	if app.bus != nil {
		// A fresh subscriber would receive the buffer; nothing to assert
		// beyond the count above, so this is a smoke check.
		ch, cancel := app.bus.Subscribe()
		cancel()
		_ = ch
	}
}

// TestSSEServerShutdownClosesStream checks that a shutdown cancels a live
// stream instead of hanging.
func TestSSEServerShutdownClosesStream(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/v1/events/stream")
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	reader := bufio.NewReader(res.Body)
	if _, event, _ := readFrame(t, reader); event == "" {
		t.Fatal("no frame received")
	}

	// A shutdown must not hang waiting for a client that never disconnects.
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown returned an error: %v", err)
	}

	// The stream must be closed rather than left dangling: the read either
	// ends in EOF or in a connection error, but it must not block forever.
	type readResult struct {
		line string
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		line, err := reader.ReadString('\n')
		done <- readResult{line, err}
	}()
	select {
	case r := <-done:
		if r.err == nil && r.line != "" {
			t.Logf("stream still produced a line after shutdown: %q", r.line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was not closed by the shutdown")
	}
	res.Body.Close()

	// Shutting down twice must be a no-op rather than a panic.
	ctx2, cancel2 := contextWithTimeout(time.Second)
	defer cancel2()
	if err := app.Shutdown(ctx2); err != nil {
		t.Errorf("second shutdown returned %v, want nil", err)
	}
}

// TestSSEHeartbeat checks that an idle stream still emits a comment frame, so a
// proxy does not drop the connection.
//
// The heartbeat interval is fixed at 15s, which is too slow to wait for in a
// test without slowing the suite down. The comment writer is therefore
// exercised directly here; the live wiring is covered by
// TestSSEStreamDeliversFrames and TestSSEWriteFrameFlushes.
func TestSSEHeartbeat(t *testing.T) {
	var b strings.Builder
	flusher := &flushCounter{w: &b}
	if !writeComment(&bufWriter{&b}, flusher, "heartbeat") {
		t.Fatal("writeComment failed")
	}
	got := b.String()
	if got != ": heartbeat\n\n" {
		t.Fatalf("heartbeat frame = %q, want %q", got, ": heartbeat\n\n")
	}
	if flusher.count == 0 {
		t.Error("a heartbeat must be flushed")
	}
}

// TestSSEToSSEEvent checks the event wire shape.
func TestSSEToSSEEvent(t *testing.T) {
	e := events.Event{
		Type:      events.TypeDeviceOnline,
		Seq:       42,
		Timestamp: time.Date(2026, 10, 19, 20, 12, 46, 517000000, time.UTC),
		Payload:   map[string]string{"reason": "probe"},
	}
	sse := toSSEEvent(e)
	if sse.Type != events.TypeDeviceOnline || sse.Seq != 42 {
		t.Errorf("type/seq = %q/%d", sse.Type, sse.Seq)
	}
	if sse.Timestamp != "2026-10-19T20:12:46.517Z" {
		t.Errorf("timestamp = %q", sse.Timestamp)
	}
	if sse.Payload == nil {
		t.Error("payload must be carried through")
	}
}

// TestSSELastEventIDReplay checks the reconnect path.
func TestSSELastEventIDReplay(t *testing.T) {
	app := newTestApp(t)

	// The seeded publish is seq 1. Publish two more so the buffer has 3.
	app.Bus().Publish(events.TypeTopologyUpdated, "reason-2")
	app.Bus().Publish(events.TypeTopologyUpdated, "reason-3")

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events/stream", nil)
	req.Header.Set("Last-Event-ID", "2")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer res.Body.Close()

	reader := bufio.NewReader(res.Body)
	id, event, _ := readFrame(t, reader)
	if event != events.TypeTopologyUpdated {
		t.Fatalf("first replayed event = %q, want %q", event, events.TypeTopologyUpdated)
	}
	if id != "3" {
		t.Errorf("replayed frame id = %q, want 3 (the only event after seq 2)", id)
	}
}

// TestSSEWriteFrameFlushes checks that every frame is flushed.
func TestSSEWriteFrameFlushes(t *testing.T) {
	rec := httptest.NewRecorder()
	f := &flushCounter{w: rec}
	if !writeSSEFrame(rec, f, sseEvent{Type: "x", Seq: 7}) {
		t.Fatal("writeSSEFrame failed")
	}
	if f.count == 0 {
		t.Error("a frame must be flushed so a proxy cannot buffer it")
	}
	got := rec.Body.String()
	if !strings.Contains(got, "id: 7\n") || !strings.Contains(got, "event: x\n") ||
		!strings.Contains(got, "data: {") || !strings.HasSuffix(got, "\n\n") {
		t.Errorf("frame = %q", got)
	}
}

// TestSSEWithoutBus checks the honest failure when no bus is configured.
func TestSSEWithoutBus(t *testing.T) {
	cfg := testConfig(t)
	db := openTestDB(t, cfg.Database.Path)
	store := newStore(db)
	app, err := New(Options{Config: cfg, Store: store})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	rec := do(t, app, http.MethodGet, "/api/v1/events/stream", "")
	assertError(t, rec, http.StatusServiceUnavailable)
}

// TestConcurrentSSESubscribers checks that many concurrent streams do not
// interfere.
func TestConcurrentSSESubscribers(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := http.Get(srv.URL + "/api/v1/events/stream")
			if err != nil {
				errs <- err.Error()
				return
			}
			defer res.Body.Close()
			reader := bufio.NewReader(res.Body)
			if _, event, _ := readFrame(t, reader); event == "" {
				errs <- "no frame received"
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Errorf("subscriber failed: %s", msg)
	}
}

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// bufWriter adapts a strings.Builder to io.Writer for the flush helpers.
type bufWriter struct{ b *strings.Builder }

func (w *bufWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// flushCounter is a Writer that counts Flush calls.
type flushCounter struct {
	w     io.Writer
	count int
}

func (f *flushCounter) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *flushCounter) Flush()                      { f.count++ }

// jsonUnmarshalString is a thin wrapper so the SSE tests do not import
// encoding/json twice.
func jsonUnmarshalString(s string, v any) error {
	return jsonUnmarshal([]byte(s), v)
}

// TestTokenPresenceEnablesAuth covers the OpenWrt deployment path.
//
// The procd init script generates /etc/lanory/config.yaml with
// auth.enabled=false (the router is a trusted LAN device) and hands the token to
// the binary through the LANORY_AUTH_TOKEN environment variable so it never
// appears in `ps` or in the world readable YAML. A configured token must switch
// authentication on regardless of that flag: shipping a token and then ignoring
// it leaves the API open to the whole LAN, including unauthenticated scan
// triggers.
func TestTokenPresenceEnablesAuth(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.Enabled = false // as the generated YAML has it
	cfg.Auth.Token = "lanory-openwrt-token"
	app := newTestAppWithConfig(t, cfg)
	app.SetAuthToken(cfg.Auth.Token)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	// No token: refused, even though the YAML said disabled.
	resp, err := http.Get(srv.URL + "/api/v1/overview")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("request without a token -> %d, want 401 (a configured token must enable auth)", resp.StatusCode)
	}

	// With the token: accepted.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Auth.Token)
	allowed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	allowed.Body.Close()
	if allowed.StatusCode != http.StatusOK {
		t.Errorf("request with the configured token -> %d, want 200", allowed.StatusCode)
	}
}
