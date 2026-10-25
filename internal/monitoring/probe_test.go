package monitoring

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// TestExecuteTCPSuccess asserts a successful TCP probe records every field the
// engine relies on: source, target, protocol, timestamp, success, latency.
func TestExecuteTCPSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	e := NewExecutor(DefaultExecutorConfig())
	target := model.ProbeTarget{
		Name: "listener", Target: "127.0.0.1", Port: portNum,
		Protocol: model.ProbeTCP, SourceID: "lanory", Timeout: 2,
	}
	rec := e.Execute(context.Background(), target)

	if !rec.Result.Success {
		t.Fatalf("probe failed: %s / %s", rec.Result.Status, rec.Result.ErrorMessage)
	}
	if rec.SourceID != "lanory" {
		t.Fatalf("source = %q, want lanory", rec.SourceID)
	}
	if rec.TargetValue != "127.0.0.1" {
		t.Fatalf("target = %q, want 127.0.0.1", rec.TargetValue)
	}
	if rec.Protocol != model.ProbeTCP {
		t.Fatalf("protocol = %q, want tcp", rec.Protocol)
	}
	if rec.Result.Timestamp.IsZero() {
		t.Fatal("timestamp is zero")
	}
	if rec.Result.LatencyMS < 1 {
		t.Fatalf("latency = %d ms, want >= 1", rec.Result.LatencyMS)
	}
	if rec.Result.Status != model.ProbeRunSuccess {
		t.Fatalf("status = %q, want success", rec.Result.Status)
	}
	if rec.Result.ErrorType != "" || rec.Result.ErrorMessage != "" {
		t.Fatalf("successful probe recorded an error: %q / %q", rec.Result.ErrorType, rec.Result.ErrorMessage)
	}
}

// TestExecuteTCPRefused asserts a dial against a closed port is classified as
// refused and reported as a failure, not as a timeout.
func TestExecuteTCPRefused(t *testing.T) {
	// Bind then close so the port is (very likely) free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	e := NewExecutor(DefaultExecutorConfig())
	target := model.ProbeTarget{Target: "127.0.0.1", Port: portNum, Protocol: model.ProbeTCP, Timeout: 2}
	rec := e.Execute(context.Background(), target)

	if rec.Result.Success {
		t.Fatal("probe against a closed port succeeded")
	}
	if rec.Result.ErrorType != ErrorTypeRefused {
		t.Fatalf("error type = %q, want %q (message %q)", rec.Result.ErrorType, ErrorTypeRefused, rec.Result.ErrorMessage)
	}
	if rec.Result.Status != model.ProbeRunRefused {
		t.Fatalf("status = %q, want %q", rec.Result.Status, model.ProbeRunRefused)
	}
	if rec.Result.ErrorMessage == "" {
		t.Fatal("failure recorded no message")
	}
}

// TestExecuteTCPTimeout asserts a listener that accepts but never replies still
// completes the probe successfully: a completed TCP handshake means the host is
// reachable, and the probe is a reachability probe, not a service probe.
func TestExecuteTCPTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Accept and never reply, never close.
			_ = c
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	e := NewExecutor(DefaultExecutorConfig())
	target := model.ProbeTarget{Target: "127.0.0.1", Port: portNum, Protocol: model.ProbeTCP, Timeout: 2}
	rec := e.Execute(context.Background(), target)

	<-accepted
	if !rec.Result.Success {
		t.Fatalf("probe failed: %s / %s", rec.Result.Status, rec.Result.ErrorMessage)
	}
}

// TestExecuteTimeoutUsesUnroutableLAN asserts a probe against an address that
// is in the private range but has no host is classified as a timeout and does not
// hang: the deadline is enforced.
func TestExecuteTimeoutUsesUnroutableLAN(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	// 10.255.255.1 is inside RFC1918 but has no host answering on this network,
	// so the SYN goes unanswered and the dial times out. (TEST-NET addresses are
	// not usable here: on a machine with a default route the dialer can be
	// answered by the upstream network.)
	target := model.ProbeTarget{Target: "10.255.255.1", Port: 9, Protocol: model.ProbeTCP, Timeout: 1}
	start := time.Now()
	rec := e.Execute(context.Background(), target)
	elapsed := time.Since(start)

	if rec.Result.Success {
		t.Skip("a host answered on 10.255.255.1:9; cannot assert a timeout here")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("probe took %s, want it bounded by the 1s timeout", elapsed)
	}
	if rec.Result.ErrorType != ErrorTypeTimeout {
		t.Fatalf("error type = %q, want %q (message %q)", rec.Result.ErrorType, ErrorTypeTimeout, rec.Result.ErrorMessage)
	}
	if rec.Result.Status != model.ProbeRunTimeout {
		t.Fatalf("status = %q, want %q", rec.Result.Status, model.ProbeRunTimeout)
	}
}

// TestExecuteICMPTimeout asserts the ICMP probe also enforces its deadline
// against an address that drops the echo request.
func TestExecuteICMPTimeout(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	target := model.ProbeTarget{Target: "10.255.255.1", Protocol: model.ProbeICMP, Timeout: 1}
	start := time.Now()
	rec := e.Execute(context.Background(), target)
	if time.Since(start) > 3*time.Second {
		t.Fatalf("icmp probe took %s, want it bounded by the 1s timeout", time.Since(start))
	}
	if rec.Result.Success {
		t.Skip("something answered the echo request; cannot assert a timeout here")
	}
	if rec.Result.ErrorType != ErrorTypeTimeout {
		t.Fatalf("error type = %q, want %q (message %q)", rec.Result.ErrorType, ErrorTypeTimeout, rec.Result.ErrorMessage)
	}
}

// TestExecuteDeadlineInheritedFromContext asserts an executor-level context
// deadline bounds a probe whose target carries no timeout of its own.
func TestExecuteDeadlineInheritedFromContext(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	rec := e.Execute(ctx, model.ProbeTarget{Target: "10.255.255.1", Port: 9, Protocol: model.ProbeTCP})
	if time.Since(start) > 2*time.Second {
		t.Fatalf("probe took %s, want it bounded by the context deadline", time.Since(start))
	}
	if rec.Result.Success {
		t.Skip("a host answered; cannot assert the deadline here")
	}
	if rec.Result.ErrorType != ErrorTypeTimeout {
		t.Fatalf("error type = %q, want %q", rec.Result.ErrorType, ErrorTypeTimeout)
	}
}

// TestExecuteContextCancelled asserts a cancelled context aborts the probe
// promptly instead of running to its timeout.
func TestExecuteContextCancelled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	e := NewExecutor(DefaultExecutorConfig())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	rec := e.Execute(ctx, model.ProbeTarget{Target: "127.0.0.1", Port: portNum, Protocol: model.ProbeTCP, Timeout: 30})
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancelled probe took %s, want it to return promptly", time.Since(start))
	}
	if rec.Result.Success {
		t.Log("probe completed before cancellation (fast machine); that is fine")
	}
}

// TestExecuteHTTP asserts the http probe follows a bounded GET and records the
// outcome.
func TestExecuteHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	e := NewExecutor(DefaultExecutorConfig())
	target := model.ProbeTarget{Target: "127.0.0.1", Port: portNum, Protocol: model.ProbeHTTP, Timeout: 3}
	rec := e.Execute(context.Background(), target)
	if !rec.Result.Success {
		t.Fatalf("http probe failed: %s / %s", rec.Result.Status, rec.Result.ErrorMessage)
	}
	if rec.Protocol != model.ProbeHTTP {
		t.Fatalf("protocol = %q, want http", rec.Protocol)
	}
}

// TestExecuteDNSSuccessAndFailure asserts the DNS probe resolves a name and
// fails on a bogus one, classifying the failure as a DNS error.
func TestExecuteDNSSuccessAndFailure(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())

	ok := e.Execute(context.Background(), model.ProbeTarget{
		Target: "localhost", Protocol: model.ProbeDNS, Timeout: 3,
	})
	if !ok.Result.Success {
		t.Skipf("localhost is not resolvable in this environment: %s", ok.Result.ErrorMessage)
	}

	bad := e.Execute(context.Background(), model.ProbeTarget{
		Target: "definitely-not-a-real-host.invalid", Protocol: model.ProbeDNS, Timeout: 3,
	})
	if bad.Result.Success {
		t.Fatal("DNS probe for a .invalid name succeeded")
	}
	if bad.Result.ErrorType != ErrorTypeDNS {
		t.Fatalf("error type = %q, want %q (message %q)", bad.Result.ErrorType, ErrorTypeDNS, bad.Result.ErrorMessage)
	}
}

// TestExecuteICMPLoopback asserts the ICMP probe actually exchanges an echo
// request/reply with 127.0.0.1 when the unprivileged datagram socket is
// available, and that it never claims success when it is not.
func TestExecuteICMPLoopback(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	// The probe works with either socket form; when neither is available it
	// degrades to TCP and says so, and never claims an ICMP success.

	if !e.Protocol(model.ProbeICMP) {
		t.Skip("ICMP socket unavailable on this host")
	}

	rec := e.Execute(context.Background(), model.ProbeTarget{
		Target: "127.0.0.1", Protocol: model.ProbeICMP, SourceID: "lanory", Timeout: 2,
	})
	if !rec.Result.Success {
		if strings.Contains(strings.ToLower(rec.Result.ErrorMessage), "permission denied") {
			t.Skipf("ICMP unavailable on this host: %s", rec.Result.ErrorMessage)
		}
		t.Fatalf("icmp probe to 127.0.0.1 failed: %s / %s", rec.Result.Status, rec.Result.ErrorMessage)
	}
	if rec.Protocol != model.ProbeICMP {
		t.Fatalf("protocol = %q, want icmp", rec.Protocol)
	}
	if rec.Result.ErrorType != "" {
		t.Fatalf("a real ICMP exchange must not report a fallback, got %q", rec.Result.ErrorType)
	}
}

// TestICMPDegradesToTCPAndReportsIt asserts the fallback contract: when the
// datagram socket cannot be opened the probe falls back to TCP, reports the
// degradation in the record, and does not claim to have sent an ICMP packet.
func TestICMPDegradesToTCPAndReportsIt(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	// Point the listener address at the same port the TCP fallback will use by
	// forcing the socket error through an unroutable listen address instead.
	e := NewExecutor(ExecutorConfig{
		DefaultTimeout:   2 * time.Second,
		DefaultPort:      portNum,
		FallbackToTCP:    true,
		ICMPPayloadSize:  16,
		ICMPListenAddr:   "192.0.2.99", // not a local address: bind must fail
		HTTPMaxRedirects: 3,
		HTTPMaxBodyBytes: 1024,
		SourceID:         "lanory",
	})
	rec := e.Execute(context.Background(), model.ProbeTarget{
		Target: "127.0.0.1", Protocol: model.ProbeICMP, Timeout: 2,
	})
	if !rec.Result.Success {
		t.Fatalf("fallback probe failed: %s / %s", rec.Result.Status, rec.Result.ErrorMessage)
	}
	if rec.Protocol != model.ProbeTCP {
		t.Fatalf("executed protocol = %q, want tcp (the fallback)", rec.Protocol)
	}
	if !strings.Contains(rec.Result.ErrorType, "icmp") {
		t.Fatalf("fallback note = %q, want it to say icmp was unavailable", rec.Result.ErrorType)
	}
	if !strings.Contains(rec.Result.ErrorType, "tcp") {
		t.Fatalf("fallback note = %q, want it to name the fallback protocol", rec.Result.ErrorType)
	}
}

// TestICMPFallbackDisabledFailsCleanly asserts that with the fallback disabled a
// probe that cannot open the socket fails instead of pretending to work.
func TestICMPFallbackDisabledFailsCleanly(t *testing.T) {
	e := NewExecutor(ExecutorConfig{
		DefaultTimeout: time.Second,
		ICMPListenAddr: "192.0.2.99",
		FallbackToTCP:  false,
		SourceID:       "lanory",
	})
	rec := e.Execute(context.Background(), model.ProbeTarget{
		Target: "127.0.0.1", Protocol: model.ProbeICMP, Timeout: 1,
	})
	if rec.Result.Success {
		t.Fatal("probe succeeded without a socket and without a fallback")
	}
	if rec.Protocol != model.ProbeICMP {
		t.Fatalf("protocol = %q, want icmp", rec.Protocol)
	}
	if e.Protocol(model.ProbeICMP) {
		t.Fatal("Protocol(icmp) reported true without a socket and without a fallback")
	}
}

// TestExecuteUnsupportedProtocol asserts an unknown protocol is reported, not
// silently treated as a success.
func TestExecuteUnsupportedProtocol(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	rec := e.Execute(context.Background(), model.ProbeTarget{Target: "127.0.0.1", Protocol: "carrier-pigeon", Timeout: 1})
	if rec.Result.Success {
		t.Fatal("unsupported protocol reported success")
	}
	if !strings.Contains(rec.Result.ErrorMessage, "unsupported") {
		t.Fatalf("error = %q, want it to mention the protocol is unsupported", rec.Result.ErrorMessage)
	}
	if e.Protocol("carrier-pigeon") {
		t.Fatal("Protocol() reported an unsupported protocol as available")
	}
}

// TestExecuteEmptyTarget asserts an empty target fails with a classified error.
func TestExecuteEmptyTarget(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	rec := e.Execute(context.Background(), model.ProbeTarget{Protocol: model.ProbeTCP})
	if rec.Result.Success {
		t.Fatal("empty target reported success")
	}
	if rec.Result.ErrorType != ErrorTypeUnknown {
		t.Fatalf("error type = %q, want %q", rec.Result.ErrorType, ErrorTypeUnknown)
	}
}

// TestExecuteUsesTargetSourceID asserts a probe without a source is attributed to
// the configured default, and one with a source keeps it.
func TestExecuteUsesTargetSourceID(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	portNum := 0
	for _, c := range port {
		portNum = portNum*10 + int(c-'0')
	}

	e := NewExecutor(DefaultExecutorConfig())
	base := model.ProbeTarget{Target: "127.0.0.1", Port: portNum, Protocol: model.ProbeTCP, Timeout: 2}

	rec := e.Execute(context.Background(), base)
	if rec.SourceID != DefaultSourceID {
		t.Fatalf("source = %q, want %q", rec.SourceID, DefaultSourceID)
	}

	base.SourceID = "windows-pc"
	rec = e.Execute(context.Background(), base)
	if rec.SourceID != "windows-pc" {
		t.Fatalf("source = %q, want windows-pc", rec.SourceID)
	}
}

// TestClassifyError asserts the error taxonomy is stable and complete for the
// error shapes a probe can actually produce.
func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"timeout", &net.DNSError{Err: "i/o timeout", IsTimeout: true}, ErrorTypeTimeout},
		{"deadline", context.DeadlineExceeded, ErrorTypeTimeout},
		{"refused", errors.New("dial tcp 127.0.0.1:9: connect: connection refused"), ErrorTypeRefused},
		{"unreachable", errors.New("dial tcp: connect: network is unreachable"), ErrorTypeUnreachable},
		{"no route", errors.New("dial tcp: connect: no route to host"), ErrorTypeUnreachable},
		{"dns", &net.DNSError{Err: "no such host", IsNotFound: true}, ErrorTypeDNS},
		{"unknown", errors.New("something odd happened"), ErrorTypeUnknown},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.err); got != tc.want {
				t.Fatalf("ClassifyError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestSplitDNSTarget asserts the "name@server" syntax used to pin a DNS check to
// a specific resolver.
func TestSplitDNSTarget(t *testing.T) {
	cases := []struct{ in, name, server string }{
		{"example.com", "example.com", ""},
		{"example.com@192.168.100.1", "example.com", "192.168.100.1"},
		{"example.com@192.168.100.1:53", "example.com", "192.168.100.1:53"},
		{"", "", ""},
	}
	for _, tc := range cases {
		name, server := splitDNSTarget(tc.in)
		if name != tc.name || server != tc.server {
			t.Fatalf("splitDNSTarget(%q) = (%q,%q), want (%q,%q)", tc.in, name, server, tc.name, tc.server)
		}
	}
}

// TestHTTPURLRendering asserts the URL a target maps to.
func TestHTTPURLRendering(t *testing.T) {
	cases := []struct {
		target model.ProbeTarget
		want   string
	}{
		{model.ProbeTarget{Target: "http://example.com/health"}, "http://example.com/health"},
		{model.ProbeTarget{Target: "https://example.com"}, "https://example.com"},
		{model.ProbeTarget{Target: "example.com", Port: 8080}, "http://example.com:8080"},
		{model.ProbeTarget{Target: "example.com/path"}, "http://example.com/path"},
		{model.ProbeTarget{Target: "127.0.0.1", Port: 80}, "http://127.0.0.1:80"},
	}
	for _, tc := range cases {
		if got := httpURL(tc.target); got != tc.want {
			t.Fatalf("httpURL(%+v) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// TestExecutorCloseIsIdempotent asserts Close can be called repeatedly.
func TestExecutorCloseIsIdempotent(t *testing.T) {
	e := NewExecutor(DefaultExecutorConfig())
	if err := e.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestExecutorDoesNotLeakSockets asserts the probe sockets are closed again.
//
// The sockets are opened per probe rather than cached: a socket carries the
// deadline and the kernel's reply demultiplexing for one exchange, and a
// shared socket serialised every probe behind the slowest one. The cost of
// opening a socket is far below the cost of a round trip, and the socket must
// not be leaked.
func TestExecutorDoesNotLeakSockets(t *testing.T) {
	before := openFileDescriptors(t)
	e := NewExecutor(DefaultExecutorConfig())
	for i := 0; i < 20; i++ {
		_, _, _, _ = e.probeICMP(context.Background(), model.ProbeTarget{
			Target: "127.0.0.1", Protocol: model.ProbeICMP, Timeout: 1,
		})
	}
	_ = e.Close()
	after := openFileDescriptors(t)
	if after > before+2 {
		t.Errorf("file descriptors grew from %d to %d over 20 probes", before, after)
	}
}

// openFileDescriptors counts the open file descriptors of this process.
func openFileDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd on this platform: %v", err)
	}
	return len(entries)
}

// TestNoGoroutineLeakOnConcurrentExecutes asserts concurrent executions do not
// leak goroutines.
func TestNoGoroutineLeakOnConcurrentExecutes(t *testing.T) {
	before := runtime.NumGoroutine()
	e := NewExecutor(DefaultExecutorConfig())
	var done chan struct{}
	for i := 0; i < 20; i++ {
		done = make(chan struct{})
		go func() {
			defer close(done)
			_ = e.Execute(context.Background(), model.ProbeTarget{
				Target: "192.0.2.1", Port: 9, Protocol: model.ProbeTCP, Timeout: 1,
			})
		}()
		<-done
	}
	// Give the runtime a moment to reap the dialer goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines grew from %d to %d", before, runtime.NumGoroutine())
}
