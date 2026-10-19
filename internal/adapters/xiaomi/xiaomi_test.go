package xiaomi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/adapters"
)

// newAdapter builds an adapter whose HTTP traffic goes to a test server.
func newAdapter(t *testing.T, cfg adapters.Config, srv *httptest.Server) *Adapter {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	xa := a.(*Adapter)
	if srv != nil {
		addr := strings.TrimPrefix(srv.URL, "http://")
		xa.dialer = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}
		xa.client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			DisableKeepAlives: true,
		}
	}
	return xa
}

// TestZeroRequestsWithoutCredentials is the single most important security test
// in this package. The product spec forbids trying default or common passwords,
// so an unconfigured adapter must not touch the device at all: not one request,
// not one TCP connection.
func TestZeroRequestsWithoutCredentials(t *testing.T) {
	var hits int32
	var connections int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		atomic.AddInt32(&connections, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"mode":"router"}`))
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID:      "xiaomi:test",
		Vendor:  "xiaomi",
		Address: "127.0.0.1",
		Port:    80,
		Timeout: time.Second,
		// No credential.
	}, srv)

	results, err := a.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe without credentials returned an error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Probe without credentials returned %d results, want 0", len(results))
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("Probe without credentials made %d HTTP requests, want 0", got)
	}
	if got := atomic.LoadInt32(&connections); got != 0 {
		t.Fatalf("Probe without credentials opened %d connections, want 0", got)
	}

	st := a.Status()
	if st.Requests != 0 {
		t.Errorf("Status().Requests = %d, want 0", st.Requests)
	}
	if st.Authenticated {
		t.Error("Status().Authenticated = true without a credential")
	}
	if !strings.Contains(st.Reason, "no credentials") {
		t.Errorf("Status().Reason does not say why nothing happened: %q", st.Reason)
	}
	if !strings.Contains(st.Reason, "FDB") {
		t.Errorf("Status().Reason does not report the fallback path: %q", st.Reason)
	}
}

// TestEmptyCredentialAlsoMakesZeroRequests covers the case where the user
// configured a username but left the password empty.
func TestEmptyCredentialAlsoMakesZeroRequests(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "127.0.0.1", Timeout: time.Second,
		Credential: adapters.NewCredential(adapters.CredentialBasic, "admin", ""),
	}, srv)

	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("an empty password caused %d requests; credential guessing is forbidden", got)
	}
}

// TestRefusesPlainHTTPWithSecret checks that a secret is never put on the wire
// in clear text to a non-loopback address.
func TestRefusesPlainHTTPWithSecret(t *testing.T) {
	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "192.168.100.2", Port: 8080,
		Timeout:    time.Second,
		Credential: adapters.NewCredential(adapters.CredentialToken, "", "token"),
	}, nil)

	results, err := a.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Probe returned %d results, want 0", len(results))
	}
	st := a.Status()
	if st.Requests != 0 {
		t.Errorf("the adapter made %d requests while refusing plain HTTP", st.Requests)
	}
	if !strings.Contains(st.Reason, "plain HTTP") {
		t.Errorf("Status().Reason does not explain the refusal: %q", st.Reason)
	}

	// Allowing insecure transport explicitly lifts the refusal (the request then
	// fails because nothing is listening, but it is attempted).
	a2 := newAdapter(t, adapters.Config{
		ID: "xiaomi:test2", Vendor: "xiaomi", Address: "127.0.0.1", Port: 1,
		Timeout: 200 * time.Millisecond, AllowInsecure: true,
		Credential: adapters.NewCredential(adapters.CredentialToken, "", "token"),
	}, nil)
	_, _ = a2.Probe(context.Background())
	if a2.Status().Requests == 0 {
		t.Error("AllowInsecure did not permit a plain HTTP attempt")
	}
}

// TestCapabilitiesAreDetectedNotAssumed is the core capability test: an endpoint
// that answers with an unexpected shape must not grant the capability.
func TestCapabilitiesAreDetectedNotAssumed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every endpoint answers 200 with an HTML page: a captive portal or a
		// login form. That proves nothing.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>please log in</body></html>"))
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "127.0.0.1", Timeout: time.Second,
		Credential: adapters.NewCredential(adapters.CredentialBasic, "admin", "secret"),
	}, srv)

	results, err := a.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	st := a.Status()
	if !st.Reachable {
		t.Error("Status().Reachable = false although the server answered")
	}
	if st.Authenticated {
		t.Error("Status().Authenticated = true for an HTML login page")
	}
	if len(st.Detected) != 0 {
		t.Errorf("detected capabilities from HTML pages: %v", st.Detected)
	}
	if len(results) != 0 {
		t.Errorf("Probe returned %d results from an HTML login page", len(results))
	}
	if !strings.Contains(st.Reason, "fall") {
		t.Errorf("Status().Reason does not mention the fallback: %q", st.Reason)
	}
}

// TestDetectsOnlyWhatTheDeviceExposes checks that a partial API yields exactly
// the matching capabilities, and that the wired/wireless distinction survives.
func TestDetectsOnlyWhatTheDeviceExposes(t *testing.T) {
	handler := http.NewServeMux()
	// Only the status and the wireless client list exist on this device.
	handler.HandleFunc("/cgi-bin/luci/api/xqsystem/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"router","hardware":"R4","romversion":"1.0.1"}`))
	})
	handler.HandleFunc("/cgi-bin/luci/api/xqnetwork/wifi_connect_devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"list":[
			{"mac":"06:77:88:99:aa:d2","name":"iPhone","wireless":true,"ssid":"home","band":"5","signal":-58},
			{"mac":"02:11:22:33:44:b7","name":"client-desktop","wireless":false}
		]}`))
	})
	// Everything else 404s.
	srv := httptest.NewServer(handler)
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "127.0.0.1", Timeout: time.Second,
		Credential: adapters.NewCredential(adapters.CredentialBasic, "admin", "secret"),
	}, srv)

	results, err := a.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	st := a.Status()
	if !st.Authenticated {
		t.Error("Status().Authenticated = false although an endpoint accepted the credential")
	}

	want := map[adapters.Capability]bool{
		adapters.CapOperatingMode:   true,
		adapters.CapWirelessClients: true,
		adapters.CapConnectionType:  true,
		// The client list entries carry ssid / band / signal, so those
		// capabilities are proven by the same response. The dedicated
		// wifi_detail_all endpoint is NOT served, and firmware is never
		// reported, so neither is detected.
		adapters.CapSSID:     true,
		adapters.CapBand:     true,
		adapters.CapSignal:   true,
		adapters.CapUplink:   false,
		adapters.CapFirmware: false,
	}
	for cap, expected := range want {
		if got := st.Detected.Has(cap); got != expected {
			t.Errorf("Detected.Has(%s) = %v, want %v (detected: %v)", cap, got, expected, st.Detected)
		}
	}
	// Capabilities() is a claim, not a detection: the adapter still claims the
	// endpoints it knows how to read even when this device does not serve them.
	if !capabilityIn(a.Capabilities(), adapters.CapUplink) {
		t.Error("Capabilities() should still claim uplink, since it is a claim not a detection")
	}
	if st.Detected.Has(adapters.CapUplink) {
		t.Error("Detected must not contain a capability this device did not prove")
	}

	if len(results) != 2 {
		t.Fatalf("Probe returned %d results, want 2 (one per client)", len(results))
	}
	var wireless, wired int
	for _, r := range results {
		if r.Source != "xiaomi" {
			t.Errorf("result source = %q, want xiaomi", r.Source)
		}
		switch r.Attributes["connection"] {
		case "wireless":
			wireless++
			if r.Attributes["ssid"] != "home" {
				t.Errorf("wireless result has ssid %q", r.Attributes["ssid"])
			}
			if r.Link == nil || r.Link.LinkType != "wireless_assoc" {
				t.Errorf("wireless result link type = %v, want wireless_assoc", r.Link)
			}
		case "wired":
			wired++
			if r.Link == nil || r.Link.LinkType != "logical_network" {
				t.Errorf("wired result link type = %v, want logical_network", r.Link)
			}
		}
	}
	if wireless != 1 || wired != 1 {
		t.Errorf("got %d wireless and %d wired results, want 1 and 1", wireless, wired)
	}
}

// TestCredentialRejectionStopsImmediately checks that a 401 ends the probe, with
// no retry: retrying with another credential is guessing.
func TestCredentialRejectionStopsImmediately(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "127.0.0.1", Timeout: time.Second,
		Credential: adapters.NewCredential(adapters.CredentialBasic, "admin", "wrong"),
	}, srv)

	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("the adapter made %d requests after a 401, want exactly 1", got)
	}
	st := a.Status()
	if st.Authenticated {
		t.Error("Status().Authenticated = true after a 401")
	}
	if !strings.Contains(st.Reason, "rejected") {
		t.Errorf("Status().Reason does not report the rejection: %q", st.Reason)
	}
	if !strings.Contains(st.Reason, "did not retry") {
		t.Errorf("Status().Reason does not state that no retry happened: %q", st.Reason)
	}
}

// TestUnreachableDeviceIsNotAnError checks the degradation rule.
func TestUnreachableDeviceIsNotAnError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "127.0.0.1", Port: port,
		Timeout:       200 * time.Millisecond,
		AllowInsecure: true,
		Credential:    adapters.NewCredential(adapters.CredentialBasic, "admin", "secret"),
	}, nil)

	results, err := a.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe returned an error for an unreachable device: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Probe returned %d results for an unreachable device", len(results))
	}
	st := a.Status()
	if st.Reachable {
		t.Error("Status().Reachable = true for an unreachable device")
	}
	if !strings.Contains(st.Reason, "FDB") {
		t.Errorf("Status().Reason does not report the fallback path: %q", st.Reason)
	}
	if st.Error != "" {
		t.Errorf("an unreachable device must not be reported as an error, got %q", st.Error)
	}
}

// TestCredentialNeverAppearsInStatusOrErrors checks that the status a UI shows
// cannot contain the secret.
func TestCredentialNeverAppearsInStatusOrErrors(t *testing.T) {
	const secret = "top-secret-token-42"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "xiaomi:test", Vendor: "xiaomi", Address: "127.0.0.1", Timeout: time.Second,
		Credential: adapters.NewCredential(adapters.CredentialBasic, "admin", secret),
	}, srv)

	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	st := a.Status()
	rendered := st.Reason + " " + st.Error + " " + strings.Join(st.Detected.Strings(), ",")
	if strings.Contains(rendered, secret) {
		t.Errorf("the status contains the credential: %q", rendered)
	}
}

// TestMetadata checks the declared adapter identity.
func TestMetadata(t *testing.T) {
	a, err := New(adapters.Config{ID: "x", Vendor: "xiaomi", Address: "192.168.100.2"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if a.Name() != Name {
		t.Errorf("Name() = %q", a.Name())
	}
	if a.Manufacturer() != "Xiaomi" {
		t.Errorf("Manufacturer() = %q", a.Manufacturer())
	}
	caps := a.Capabilities()
	// The adapter must claim the client capabilities it knows how to read...
	for _, want := range []adapters.Capability{
		adapters.CapWirelessClients, adapters.CapWiredClients, adapters.CapSSID,
		adapters.CapSignal, adapters.CapConnectionType, adapters.CapOperatingMode,
	} {
		if !capabilityIn(caps, want) {
			t.Errorf("Capabilities() is missing %s", want)
		}
	}
	if !strings.Contains(a.Fallback(), "FDB") {
		t.Errorf("Fallback() = %q", a.Fallback())
	}
	// The default port is 80, and the adapter refuses to use it with a secret.
	if a.(*Adapter).cfg.Port != 80 {
		t.Errorf("default port = %d, want 80", a.(*Adapter).cfg.Port)
	}
}

// TestResponseParsing checks the low level parsers against shapes a real device
// may return.
func TestResponseParsing(t *testing.T) {
	if _, ok := capabilitiesFromResponse(adapters.CapWirelessClients, []byte(`{"list":[]}`)); !ok {
		t.Error("an empty client list still proves the endpoint exists")
	}
	if _, ok := capabilitiesFromResponse(adapters.CapWirelessClients, []byte(`not json`)); ok {
		t.Error("a non JSON body proved a capability")
	}
	if _, ok := capabilitiesFromResponse(adapters.CapWirelessClients, []byte(`{}`)); ok {
		t.Error("an empty document proved a capability")
	}
	if _, ok := capabilitiesFromResponse(adapters.CapWirelessClients, []byte(`{"error":"nope"}`)); ok {
		t.Error("an error document proved a capability")
	}
	// An explicit wireless flag is what unlocks the connection type capability.
	caps, ok := capabilitiesFromResponse(adapters.CapWirelessClients,
		[]byte(`{"list":[{"mac":"aa:bb:cc:dd:ee:ff","wireless":true}]}`))
	if !ok {
		t.Fatal("a shaped client list did not prove the capability")
	}
	for _, want := range []adapters.Capability{adapters.CapWirelessClients, adapters.CapConnectionType, adapters.CapSignal} {
		if !caps.Has(want) {
			t.Errorf("a list with a wireless flag did not prove %s", want)
		}
	}
	// A list without the flag only proves the endpoint exists.
	caps2, ok := capabilitiesFromResponse(adapters.CapWirelessClients,
		[]byte(`{"list":[{"mac":"aa:bb:cc:dd:ee:ff"}]}`))
	if !ok {
		t.Fatal("a shaped client list did not prove the capability")
	}
	if caps2.Has(adapters.CapSignal) {
		t.Error("a list without a wireless flag proved the signal capability")
	}
}

func capabilityIn(set []adapters.Capability, want adapters.Capability) bool {
	for _, c := range set {
		if c == want {
			return true
		}
	}
	return false
}
