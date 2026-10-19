package generic

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

// newAdapter builds an adapter whose HTTP traffic is redirected to a test
// server instead of the configured address, so the tests never touch a network.
func newAdapter(t *testing.T, cfg adapters.Config, srv *httptest.Server) *Adapter {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	ga := a.(*Adapter)
	if srv != nil {
		addr := strings.TrimPrefix(srv.URL, "http://")
		ga.dialer = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}
		ga.http_.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			DisableKeepAlives: true,
		}
	}
	return ga
}

// TestProbeReturnsEmptyWithoutError is the acceptance test for the generic
// adapter: with nothing listening, it must return zero results and a nil error.
func TestProbeReturnsEmptyWithoutError(t *testing.T) {
	// A port nothing listens on. Grab a free one and close it immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	a := newAdapter(t, adapters.Config{
		ID:      "generic:test",
		Vendor:  "generic",
		Address: "127.0.0.1",
		Port:    port,
		Timeout: 200 * time.Millisecond,
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
	if !strings.Contains(st.Reason, "fall") {
		t.Errorf("Status().Reason does not mention the fallback: %q", st.Reason)
	}
	if st.Error != "" {
		t.Errorf("Status().Error should stay empty for an unreachable device, got %q", st.Error)
	}
	if st.Detected.Has(adapters.CapClients) {
		t.Error("the generic adapter reported a client capability it cannot have")
	}
}

// TestProbeReportsReachableEndpoint checks that a device which answers is
// reported as a node, with the fallback note still visible.
func TestProbeReportsReachableEndpoint(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// A login page: the device exists but offers no API.
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>login</body></html>"))
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID:      "generic:test",
		Vendor:  "generic",
		Address: "127.0.0.1",
		Port:    1, // overridden by the dialer
		Timeout: time.Second,
	}, srv)

	results, err := a.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Probe returned no results for a reachable device")
	}
	if results[0].Kind != "generic" {
		t.Errorf("result kind = %q, want generic", results[0].Kind)
	}
	if results[0].Attributes["adapter"] != Name {
		t.Errorf("adapter attribute = %q", results[0].Attributes["adapter"])
	}
	st := a.Status()
	if !st.Reachable {
		t.Error("Status().Reachable = false for a reachable device")
	}
	if st.Requests == 0 {
		t.Error("Status().Requests = 0 for a reachable device")
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Error("the test server was never hit")
	}
	// The generic adapter cannot prove capabilities even when reachable.
	if len(st.Detected) != 0 {
		t.Errorf("generic adapter detected capabilities it cannot prove: %v", st.Detected)
	}
}

// TestNoCredentialMeansNoCredentialAttempt checks the security rule: without a
// configured credential the adapter sends no Authorization header at all.
func TestNoCredentialMeansNoCredentialAttempt(t *testing.T) {
	var sawAuth atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth.Store(true)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "generic:test", Vendor: "generic", Address: "127.0.0.1", Timeout: time.Second,
	}, srv)

	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if sawAuth.Load() {
		t.Error("the adapter sent an Authorization header without a configured credential")
	}
}

// TestConfiguredCredentialIsSentOnlyAsBasicAuth checks that a configured
// credential is used, and that the value is the configured one (never a guess).
func TestConfiguredCredentialIsSentOnlyAsBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newAdapter(t, adapters.Config{
		ID: "generic:test", Vendor: "generic", Address: "127.0.0.1", Timeout: time.Second,
		Credential: adapters.NewCredential(adapters.CredentialBasic, "operator", "correct-horse"),
	}, srv)

	if _, err := a.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if gotUser != "operator" || gotPass != "correct-horse" {
		t.Errorf("server saw credentials %q/%q", gotUser, gotPass)
	}
}

// TestMetadata is a cheap check that the adapter declares itself honestly.
func TestMetadata(t *testing.T) {
	a := newAdapter(t, adapters.Config{ID: "g", Vendor: "generic", Address: "127.0.0.1"}, nil)
	if a.Name() != Name {
		t.Errorf("Name() = %q", a.Name())
	}
	if a.Manufacturer() != "Generic" {
		t.Errorf("Manufacturer() = %q", a.Manufacturer())
	}
	caps := a.Capabilities()
	for _, c := range caps {
		if c == adapters.CapWirelessClients {
			t.Error("the generic adapter claims wireless clients, which it cannot provide")
		}
	}
	if len(caps) == 0 {
		t.Error("the generic adapter claims no capability at all")
	}
	if !strings.Contains(a.Fallback(), "FDB") {
		t.Errorf("Fallback() = %q, expected the passive collector path", a.Fallback())
	}
}

// TestEmptyAddressIsAnError checks that a configuration mistake is reported,
// because the user must fix it, unlike an unreachable device.
func TestEmptyAddressIsAnError(t *testing.T) {
	if _, err := New(adapters.Config{Vendor: "generic"}); err == nil {
		t.Fatal("New accepted an empty management address")
	}
}
