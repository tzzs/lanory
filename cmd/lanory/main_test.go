package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Command-level tests.
//
// They drive run() with injected writers and an ephemeral database, so no test
// needs to spawn a process or bind a real port for longer than a request.

// TestVersionFlag checks --version.
func TestVersionFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--version"}, &out, &errOut); err != nil {
		t.Fatalf("run --version: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "lanory ") {
		t.Errorf("output = %q, want it to start with 'lanory '", got)
	}
	if !strings.Contains(got, runtimeGoos()) {
		t.Errorf("output = %q, want it to mention the GOOS", got)
	}
	if !strings.Contains(got, runtimeGoarch()) {
		t.Errorf("output = %q, want it to mention the GOARCH", got)
	}
}

// TestPrintConfig checks --print-config, which must emit valid YAML.
func TestPrintConfig(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--print-config", "--port", "9999"}, &out, &errOut); err != nil {
		t.Fatalf("run --print-config: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "server:") || !strings.Contains(got, "port: 9999") {
		t.Errorf("output does not contain the overridden port:\n%s", got)
	}
	if !strings.Contains(got, "database:") {
		t.Errorf("output does not contain the database section:\n%s", got)
	}
}

// TestUnknownFlag checks that a bad flag is an error, not a panic.
func TestUnknownFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"--nope"}, &out, &errOut)
	if err == nil {
		t.Fatal("an unknown flag must fail")
	}
	if !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("usage must be printed on a flag error:\n%s", errOut.String())
	}
}

// TestUnexpectedArgument checks that a positional argument is refused.
func TestUnexpectedArgument(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"stray"}, &out, &errOut); err == nil {
		t.Fatal("an unexpected positional argument must fail")
	}
}

// TestInvalidPortFlag checks the port range.
func TestInvalidPortFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, p := range []string{"0", "-1", "70000"} {
		if err := run([]string{"--port", p}, &out, &errOut); err == nil {
			t.Errorf("--port %s must be rejected", p)
		}
	}
}

// TestMissingConfigFile checks that an explicit --config path must exist.
func TestMissingConfigFile(t *testing.T) {
	var out, errOut bytes.Buffer
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	err := run([]string{"--config", missing, "--print-config"}, &out, &errOut)
	if err == nil {
		t.Fatal("a missing --config file must be an error")
	}
	if !strings.Contains(err.Error(), "config:") {
		t.Errorf("error = %v, want a config error", err)
	}
}

// TestConfigFileIsLoaded checks that a configuration file is honoured.
func TestConfigFileIsLoaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lanory.yaml")
	content := "server:\n  host: 127.0.0.1\n  port: 9911\n  trusted_proxies:\n    - 127.0.0.0/8\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := run([]string{"--config", path, "--print-config"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "host: 127.0.0.1") {
		t.Errorf("the configured host was not applied:\n%s", got)
	}
	if !strings.Contains(got, "port: 9911") {
		t.Errorf("the configured port was not applied:\n%s", got)
	}
}

// TestFlagsOverrideConfig checks that flags win over the configuration file.
func TestFlagsOverrideConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lanory.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 9911\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := run([]string{"--config", path, "--print-config", "--port", "9922"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "port: 9922") || strings.Contains(got, "port: 9911") {
		t.Errorf("the --port flag must win over the file:\n%s", got)
	}
}

// TestServesAndShutsDown checks the full lifecycle: listen, answer a request,
// shut down on a termination signal.
func TestServesAndShutsDown(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lanory-cmd.db")
	port := freePort(t)

	addr, stop := startServer(t,
		"--db", dbPath,
		"--port", strconv.Itoa(port),
		"--log-level", "error",
	)
	if !strings.Contains(addr, strconv.Itoa(port)) {
		t.Errorf("listen line = %q, want to mention port %d", addr, port)
	}

	base := "http://" + addr
	res, err := http.Get(base + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&health); err != nil {
		res.Body.Close()
		t.Fatalf("decode health: %v", err)
	}
	res.Body.Close()
	if health.Status != "ok" {
		t.Errorf("status = %q, want ok", health.Status)
	}

	// A route that needs a database round trip must also answer. With no token
	// configured, the default config still has auth enabled, so a read of the
	// devices is refused with 401 — which is itself the documented behaviour.
	res, err = http.Get(base + "/api/v1/devices")
	if err != nil {
		t.Fatalf("GET devices: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET devices without a token = %d, want 401 (auth is enabled by default)", res.StatusCode)
	}

	// The database file must have been created on disk.
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database was not created: %v", err)
	}

	// Cancellation must lead to a clean exit, not a hang.
	select {
	case err := <-stop():
		if err != nil {
			t.Errorf("run returned an error on shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the process did not exit within the shutdown deadline")
	}
}

// TestServesWithWebDir checks that an explicit --web-dir is served.
func TestServesWithWebDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>WEB DIR</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "lanory-web.db")

	port := freePort(t)
	addr, _ := startServer(t, "--db", dbPath, "--port", strconv.Itoa(port), "--web-dir", dir, "--log-level", "error")

	res, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer res.Body.Close()
	body := make([]byte, 4096)
	n, _ := res.Body.Read(body)
	if !strings.Contains(string(body[:n]), "WEB DIR") {
		t.Errorf("the web dir was not served: %s", body[:n])
	}
}

// TestAuthTokenFlag checks that --auth-token turns authentication on.
func TestAuthTokenFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lanory-auth.db")
	port := freePort(t)
	addr, _ := startServer(t, "--db", dbPath, "--port", strconv.Itoa(port), "--auth-token", "flag-token")

	// Without the token.
	res, err := http.Get("http://" + addr + "/api/v1/devices")
	if err != nil {
		t.Fatalf("GET devices: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without a token = %d, want 401", res.StatusCode)
	}

	// With the token from the flag.
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/v1/devices", nil)
	req.Header.Set("Authorization", "Bearer flag-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET devices with token: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status with a token = %d, want 200", res.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// goosReporter and goarchReporter let the version test assert the real values
// that run() prints, without depending on where the suite runs.
var (
	goosReporter   = func() string { return runtime.GOOS }
	goarchReporter = func() string { return runtime.GOARCH }
)

// runtimeGoos and runtimeGoarch are the thin wrappers used by the assertions.
func runtimeGoos() string   { return goosReporter() }
func runtimeGoarch() string { return goarchReporter() }

// waitForListenLine waits until the process prints its listen address.
// freePort returns a port the kernel says is currently unused.
//
// The server tests used fixed ports (9933-9936). When Go runs packages in
// parallel, or a previous test's process has not finished shutting down yet, the
// bind fails and the test flakes; that was happening roughly one run in three.
// Asking the kernel for an ephemeral port removes the collision entirely. There
// is still a window between closing this listener and the server binding it, but
// it is tiny and the kernel will not hand the same port to two concurrent
// callers.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate a free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("release the probe listener: %v", err)
	}
	return port
}

// syncBuffer is a bytes.Buffer safe for the writer goroutine and the test
// goroutine to touch concurrently.
//
// The server under test writes to it from its own goroutine while the test polls
// it for the listen line and reads it again at the end. bytes.Buffer is not
// safe for that, so the original tests had a genuine data race that `go test
// -race` flags.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForAccept blocks until the server at addr accepts a TCP connection.
//
// The listen line is printed before the listener is created, so a request sent
// immediately after reading it can be refused. Polling until the socket answers
// is the reliable way to know the server is actually up.
func waitForAccept(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the server at %s never accepted a connection", addr)
}

func waitForListenLine(t *testing.T, out *syncBuffer, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		line := out.String()
		if i := strings.Index(line, "listening on "); i >= 0 {
			rest := line[i+len("listening on "):]
			if j := strings.IndexAny(rest, " \n"); j >= 0 {
				return rest[:j]
			}
			return rest
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

// startServer launches runWithContext in the background and returns the address
// it is listening on plus a stop function.
//
// The tests used to send SIGTERM to their own process to trigger a shutdown.
// That only worked while a signal handler happened to be installed: once the
// server had exited, the deferred second stop hit the default disposition and
// killed the whole test binary ("signal: terminated"). A cancellable context is
// the same shutdown path without any process-level signal.
func startServer(t *testing.T, args ...string) (addr string, stop func() <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut syncBuffer
	runErr := make(chan error, 1)
	go func() {
		runErr <- runWithContext(ctx, args, &out, &errOut)
	}()

	addr = waitForListenLine(t, &out, 10*time.Second)
	if addr == "" {
		cancel()
		t.Fatalf("the listen line never appeared: out=%q err=%q", out.String(), errOut.String())
	}
	// The server binds 0.0.0.0. Connecting to 0.0.0.0 as a destination works on
	// some kernels and is refused on others, which made these tests flaky, so
	// the loopback address is used instead.
	if host, port, err := net.SplitHostPort(addr); err == nil && (host == "0.0.0.0" || host == "::" || host == "") {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	// The listen line is printed just before ListenAndServe is called, so the
	// socket may not be accepting connections yet. Wait until it answers.
	waitForAccept(t, addr)

	// The result channel can be consumed exactly once, so remember it: the test
	// may wait on it and the cleanup must not then block on the same channel.
	var (
		once sync.Once
		mu   sync.Mutex
		res  error
		got  bool
	)
	wait := func(timeout time.Duration) (error, bool) {
		mu.Lock()
		if got {
			err := res
			mu.Unlock()
			return err, true
		}
		mu.Unlock()
		select {
		case err := <-runErr:
			mu.Lock()
			res, got = err, true
			mu.Unlock()
			return err, true
		case <-time.After(timeout):
			return nil, false
		}
	}

	stop = func() <-chan error {
		once.Do(func() { cancel() })
		done := make(chan error, 1)
		go func() {
			err, _ := wait(10 * time.Second)
			done <- err
		}()
		return done
	}

	t.Cleanup(func() {
		stop()
		if _, ok := wait(10 * time.Second); !ok {
			t.Log("warning: the server did not stop within 10s")
		}
	})
	return addr, stop
}
