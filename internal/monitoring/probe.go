package monitoring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lanory/lanory/internal/model"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ErrorType classifies why a probe failed. The values are persisted in
// model.ProbeResult.ErrorType, so they are part of the API contract and must
// stay stable strings.
const (
	// ErrorTypeTimeout means no answer arrived before the deadline.
	ErrorTypeTimeout = "timeout"
	// ErrorTypeRefused means the peer actively refused the connection
	// (TCP RST, ICMP administratively prohibited).
	ErrorTypeRefused = "refused"
	// ErrorTypeUnreachable means the network path could not be used (no route,
	// network unreachable, ICMP destination unreachable).
	ErrorTypeUnreachable = "unreachable"
	// ErrorTypeDNS means the target hostname could not be resolved.
	ErrorTypeDNS = "dns"
	// ErrorTypeUnknown is the fallback for anything not recognised.
	ErrorTypeUnknown = "unknown"
)

// DefaultSourceID is the probe source used when a ProbeTarget has none set. It
// identifies the host Lanory runs on (the OpenWrt router), not a remote
// terminal: reachability results are always "reachable from Lanory".
const DefaultSourceID = "lanory"

// DefaultICMPListenAddr is where the unprivileged ICMP datagram socket binds.
const DefaultICMPListenAddr = "0.0.0.0"

// icmpEchoPayload is the echo request body. A stable marker makes it possible to
// recognise our own replies in a busy network.
var icmpEchoPayload = []byte("lanory-probe")

// ProbeRecord is one fully described probe execution.
//
// model.ProbeResult deliberately holds only the per-run outcome, so the fields
// the task requires around every execution (source, target, protocol) live
// here. The scheduler returns a ProbeRecord and persists its Result.
type ProbeRecord struct {
	// Result is the model.ProbeResult to persist.
	Result model.ProbeResult
	// SourceID is the probe source that produced this record.
	SourceID string `json:"source_id"`
	// TargetValue is the probed target (IP, hostname or CIDR).
	TargetValue string `json:"target"`
	// Protocol is the protocol that was executed. It can differ from the
	// configured protocol when an ICMP probe degraded to TCP.
	Protocol model.ProbeProtocol `json:"protocol"`
}

// Executor performs a single probe execution against a target.
//
// Implementations must be safe for concurrent use and must not keep per-probe
// state. Everything a probe needs comes from the model.ProbeTarget it is handed.
type Executor interface {
	// Execute runs the probe described by target.
	Execute(ctx context.Context, target model.ProbeTarget) ProbeRecord
	// Protocol reports whether protocol can be executed on this host at all.
	Protocol(protocol model.ProbeProtocol) bool
	// Close releases the resources held by the executor.
	Close() error
}

// ExecutorConfig tunes probe execution.
type ExecutorConfig struct {
	// DefaultTimeout is used when a probe target carries no timeout of its own.
	DefaultTimeout time.Duration
	// DefaultPort is used by tcp probes without a port.
	DefaultPort int
	// FallbackToTCP makes an icmp probe degrade to a TCP connect against the
	// same target when the ICMP datagram socket cannot be opened (unprivileged
	// process, or a kernel with net.ipv4.ping_group_range locked down). The
	// degradation is recorded in the record: a fallback probe never claims a
	// real ICMP exchange happened.
	FallbackToTCP bool
	// ICMPPayloadSize is the echo request body size in bytes.
	ICMPPayloadSize int
	// ICMPListenAddr is the local address the ICMP datagram socket binds to.
	ICMPListenAddr string
	// HTTPMaxRedirects caps redirect following for http probes.
	HTTPMaxRedirects int
	// HTTPMaxBodyBytes caps how much of a response body is read.
	HTTPMaxBodyBytes int64
	// DNSServer optionally forces dns probes to a specific server, in
	// "host" or "host:port" form. Empty means the system resolver.
	DNSServer string
	// SourceID labels the records produced by this executor.
	SourceID string
}

// DefaultExecutorConfig returns the built-in defaults.
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		DefaultTimeout:   3 * time.Second,
		DefaultPort:      80,
		FallbackToTCP:    true,
		ICMPPayloadSize:  32,
		ICMPListenAddr:   DefaultICMPListenAddr,
		HTTPMaxRedirects: 3,
		HTTPMaxBodyBytes: 64 * 1024,
		SourceID:         DefaultSourceID,
	}
}

// ProbeExecutor is the default Executor: real ICMP, TCP, HTTP and DNS probes
// executed from this host.
type ProbeExecutor struct {
	cfg ExecutorConfig

	// icmpMu serialises ICMP echo exchanges. The datagram socket is shared, so
	// two concurrent echo requests could otherwise swallow each other's
	// replies. Serialising also keeps a soft router from being flooded with
	// parallel echo traffic, which is the intended behaviour here.
	icmpMu sync.Mutex
	sock   icmpSocket
}

// icmpSocket is the lazily created unprivileged ICMP datagram socket. A
// container or a hardened kernel may refuse it, so the error is cached too.
type icmpSocket struct {
	conn *icmp.PacketConn
	err  error
}

// NewExecutor returns an Executor using cfg, filling zero fields from
// DefaultExecutorConfig.
func NewExecutor(cfg ExecutorConfig) *ProbeExecutor {
	def := DefaultExecutorConfig()
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = def.DefaultTimeout
	}
	if cfg.DefaultPort <= 0 {
		cfg.DefaultPort = def.DefaultPort
	}
	if cfg.ICMPPayloadSize <= 0 {
		cfg.ICMPPayloadSize = def.ICMPPayloadSize
	}
	if cfg.ICMPListenAddr == "" {
		cfg.ICMPListenAddr = def.ICMPListenAddr
	}
	if cfg.HTTPMaxRedirects <= 0 {
		cfg.HTTPMaxRedirects = def.HTTPMaxRedirects
	}
	if cfg.HTTPMaxBodyBytes <= 0 {
		cfg.HTTPMaxBodyBytes = def.HTTPMaxBodyBytes
	}
	if cfg.SourceID == "" {
		cfg.SourceID = def.SourceID
	}
	return &ProbeExecutor{cfg: cfg}
}

// Protocol reports whether protocol can be executed on this host.
//
// ICMP reports false when the unprivileged datagram socket cannot be opened and
// FallbackToTCP is disabled; a TCP probe must then be configured explicitly
// instead of pretending ICMP works.
func (e *ProbeExecutor) Protocol(protocol model.ProbeProtocol) bool {
	switch protocol {
	case model.ProbeICMP:
		if _, err := e.icmpConn(); err != nil && !e.cfg.FallbackToTCP {
			return false
		}
		return true
	case model.ProbeTCP, model.ProbeHTTP, model.ProbeDNS:
		return true
	default:
		return false
	}
}

// Close releases the cached ICMP socket, if any.
func (e *ProbeExecutor) Close() error {
	e.icmpMu.Lock()
	defer e.icmpMu.Unlock()
	if e.sock.conn == nil {
		return nil
	}
	conn := e.sock.conn
	e.sock.conn = nil
	return conn.Close()
}

// icmpConn returns the shared unprivileged ICMP socket, opening it on first use.
func (e *ProbeExecutor) icmpConn() (*icmp.PacketConn, error) {
	e.icmpMu.Lock()
	defer e.icmpMu.Unlock()
	if e.sock.conn != nil || e.sock.err != nil {
		return e.sock.conn, e.sock.err
	}
	conn, err := icmp.ListenPacket("udp4", e.cfg.ICMPListenAddr)
	if err != nil {
		e.sock.err = fmt.Errorf("icmp datagram socket unavailable: %w", err)
		return nil, e.sock.err
	}
	e.sock.conn = conn
	return conn, nil
}

// Execute runs one probe.
//
// The record always captures the probe source, target, protocol, timestamp,
// success, latency and status; on failure it also captures the classified error
// type and message. ID and ProbeID of the result are left empty for the caller.
func (e *ProbeExecutor) Execute(ctx context.Context, target model.ProbeTarget) ProbeRecord {
	rec := ProbeRecord{
		SourceID:    sourceOf(e.cfg.SourceID, target.SourceID),
		TargetValue: target.Target,
		Protocol:    target.Protocol,
		Result: model.ProbeResult{
			Timestamp: time.Now().UTC(),
			Status:    model.ProbeRunError,
		},
	}
	if strings.TrimSpace(target.Target) == "" {
		rec.Result.ErrorType = ErrorTypeUnknown
		rec.Result.ErrorMessage = "probe target is empty"
		return rec
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeoutFor(target))
		defer cancel()
	}

	var (
		ok       bool
		err      error
		fallback string
		executed model.ProbeProtocol
	)
	switch target.Protocol {
	case model.ProbeICMP:
		ok, err, fallback, executed = e.probeICMP(ctx, target)
	case model.ProbeTCP:
		ok, err, executed = e.probeTCP(ctx, target)
	case model.ProbeHTTP:
		ok, err, executed = e.probeHTTP(ctx, target)
	case model.ProbeDNS:
		ok, err, executed = e.probeDNS(ctx, target)
	default:
		err = fmt.Errorf("unsupported probe protocol %q", string(target.Protocol))
		executed = target.Protocol
	}
	if executed != "" {
		rec.Protocol = executed
	}
	rec.Result.Success = ok
	// A sub-millisecond round trip must not be reported as 0 ms latency, which
	// the UI would render as "instant" and which would break the loss and
	// latency statistics.
	rec.Result.LatencyMS = elapsedMS(rec.Result.Timestamp)
	if rec.Result.LatencyMS < 1 {
		rec.Result.LatencyMS = 1
	}
	if ok {
		rec.Result.Status = model.ProbeRunSuccess
	} else {
		if err == nil {
			err = errors.New("probe failed without an error")
		}
		rec.Result.ErrorType = ClassifyError(err)
		rec.Result.ErrorMessage = err.Error()
		switch rec.Result.ErrorType {
		case ErrorTypeTimeout:
			rec.Result.Status = model.ProbeRunTimeout
		case ErrorTypeRefused:
			rec.Result.Status = model.ProbeRunRefused
		default:
			rec.Result.Status = model.ProbeRunError
		}
	}
	if fallback != "" {
		if ok {
			// A successful fallback is not an ICMP success. Record how the
			// answer was obtained so the UI can label it truthfully.
			rec.Result.ErrorType = fallback
		} else {
			rec.Result.ErrorMessage = fallback + ": " + rec.Result.ErrorMessage
		}
	}
	return rec
}

// elapsedMS reports the milliseconds since started, at least 1.
func elapsedMS(started time.Time) int {
	ms := int(time.Since(started).Milliseconds())
	if ms < 1 {
		ms = 1
	}
	return ms
}

// sourceOf prefers the per-target source over the executor default.
func sourceOf(def, target string) string {
	if strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	return def
}

// timeoutFor returns the per-probe timeout.
func (e *ProbeExecutor) timeoutFor(target model.ProbeTarget) time.Duration {
	if target.Timeout > 0 {
		return time.Duration(target.Timeout) * time.Second
	}
	return e.cfg.DefaultTimeout
}

// probeICMP sends one echo request on an unprivileged ICMP datagram socket.
//
// OpenWrt services usually run as an unprivileged user, so a raw socket is
// deliberately not required. When the socket cannot be opened and FallbackToTCP
// is set, the probe degrades to a TCP connect against the same target and
// reports the degradation; it never claims that an ICMP packet was sent.
func (e *ProbeExecutor) probeICMP(ctx context.Context, target model.ProbeTarget) (bool, error, string, model.ProbeProtocol) {
	ip, err := net.ResolveIPAddr("ip4", target.Target)
	if err != nil {
		return false, err, "", model.ProbeICMP
	}
	if ip.IP.To4() == nil {
		return false, errors.New("target is not an IPv4 address"), "", model.ProbeICMP
	}

	conn, sockErr := e.icmpConn()
	if sockErr != nil {
		if !e.cfg.FallbackToTCP {
			return false, sockErr, "", model.ProbeICMP
		}
		port := target.Port
		if port <= 0 {
			port = e.cfg.DefaultPort
		}
		ok, dialErr, _ := e.probeTCP(ctx, model.ProbeTarget{
			Target:   ip.IP.String(),
			Port:     port,
			Protocol: model.ProbeTCP,
			Timeout:  target.Timeout,
		})
		note := fmt.Sprintf("icmp unavailable (%s); fell back to tcp/%d", sockErr.Error(), port)
		return ok, dialErr, note, model.ProbeTCP
	}

	e.icmpMu.Lock()
	defer e.icmpMu.Unlock()

	id := int(time.Now().UnixNano() & 0xffff)
	if id == 0 {
		id = 1
	}
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{ID: id, Seq: 1, Data: icmpEchoBody(e.cfg.ICMPPayloadSize)},
	}
	req, err := msg.Marshal(nil)
	if err != nil {
		return false, err, "", model.ProbeICMP
	}

	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(e.timeoutFor(target))
	}
	// The socket is shared: always set an explicit deadline and always clear it
	// so a slow peer cannot poison the next probe.
	_ = conn.SetDeadline(deadline)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	if _, err := conn.WriteTo(req, &net.UDPAddr{IP: ip.IP}); err != nil {
		return false, err, "", model.ProbeICMP
	}

	buf := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return false, err, "", model.ProbeICMP
		}
		reply, perr := icmp.ParseMessage(1, buf[:n])
		if perr != nil {
			continue // not a message we understand, keep waiting
		}
		echo, isEcho := reply.Body.(*icmp.Echo)
		if !isEcho || echo.ID != id || echo.Seq != 1 {
			continue // a reply to somebody else's probe
		}
		if !peerIs(peer, ip.IP) {
			continue
		}
		switch reply.Type {
		case ipv4.ICMPTypeEchoReply:
			return true, nil, "", model.ProbeICMP
		case ipv4.ICMPTypeDestinationUnreachable:
			return false, errors.New("icmp destination unreachable"), "", model.ProbeICMP
		default:
			continue // time exceeded, redirect, ... keep waiting
		}
	}
}

// icmpEchoBody builds the echo payload of size bytes.
func icmpEchoBody(size int) []byte {
	body := make([]byte, size)
	copy(body, icmpEchoPayload)
	return body
}

// peerIs reports whether the reply came from the address we probed.
func peerIs(peer net.Addr, want net.IP) bool {
	switch a := peer.(type) {
	case *net.UDPAddr:
		return a.IP.Equal(want)
	case *net.IPAddr:
		return a.IP.Equal(want)
	default:
		return true
	}
}

// probeTCP opens a TCP connection to target:port.
func (e *ProbeExecutor) probeTCP(ctx context.Context, target model.ProbeTarget) (bool, error, model.ProbeProtocol) {
	port := target.Port
	if port <= 0 {
		port = e.cfg.DefaultPort
	}
	addr := net.JoinHostPort(strings.TrimSpace(target.Target), strconv.Itoa(port))
	dialer := net.Dialer{Timeout: e.timeoutFor(target)}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, err, model.ProbeTCP
	}
	_ = conn.Close()
	return true, nil, model.ProbeTCP
}

// probeHTTP performs a bounded GET: a redirect cap, a body size cap and the
// probe timeout. Any HTTP answer (including 4xx/5xx) counts as reachable; only
// transport failures are probe failures.
func (e *ProbeExecutor) probeHTTP(ctx context.Context, target model.ProbeTarget) (bool, error, model.ProbeProtocol) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL(target), nil)
	if err != nil {
		return false, err, model.ProbeHTTP
	}
	req.Header.Set("User-Agent", "lanory-probe/1.0")

	client := &http.Client{
		Timeout: e.timeoutFor(target),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= e.cfg.HTTPMaxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err, model.ProbeHTTP
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, e.cfg.HTTPMaxBodyBytes))
		_ = resp.Body.Close()
	}()
	return true, nil, model.ProbeHTTP
}

// httpURL renders the absolute URL of an http probe. A target without a scheme
// is treated as http://host[:port][/path].
func httpURL(target model.ProbeTarget) string {
	t := strings.TrimSpace(target.Target)
	if strings.Contains(t, "://") {
		return t
	}
	if target.Port > 0 && !strings.ContainsAny(t, "/:") {
		return "http://" + net.JoinHostPort(t, strconv.Itoa(target.Port))
	}
	return "http://" + t
}

// probeDNS resolves the target hostname.
//
// The target may be "name" (system resolver, or the configured DNSServer) or
// "name@server" to pin the lookup to one server, which is how a user defines a
// dedicated DNS check for a specific resolver such as 192.168.100.1.
func (e *ProbeExecutor) probeDNS(ctx context.Context, target model.ProbeTarget) (bool, error, model.ProbeProtocol) {
	name, server := splitDNSTarget(target.Target)
	if name == "" {
		return false, errors.New("dns probe target is empty"), model.ProbeDNS
	}
	if server == "" {
		server = e.cfg.DNSServer
	}
	resolver := &net.Resolver{}
	if server != "" {
		host, port, err := net.SplitHostPort(server)
		if err != nil {
			host, port = server, "53"
		}
		if host == "" || port == "" {
			return false, fmt.Errorf("invalid dns server %q", server), model.ProbeDNS
		}
		resolver.PreferGo = true
		resolver.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(host, port))
		}
	}
	addrs, err := resolver.LookupHost(ctx, name)
	if err != nil {
		return false, err, model.ProbeDNS
	}
	if len(addrs) == 0 {
		return false, fmt.Errorf("no address records for %s", name), model.ProbeDNS
	}
	return true, nil, model.ProbeDNS
}

// splitDNSTarget splits "name@server" into its parts.
func splitDNSTarget(target string) (name, server string) {
	if i := strings.LastIndex(target, "@"); i >= 0 {
		return strings.TrimSpace(target[:i]), strings.TrimSpace(target[i+1:])
	}
	return strings.TrimSpace(target), ""
}

// ClassifyError maps a probe error onto one of the stable ErrorType values.
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isTimeout(err) {
		return ErrorTypeTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrorTypeDNS
	}
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused") {
		return ErrorTypeRefused
	}
	if isUnreachable(err) {
		return ErrorTypeUnreachable
	}
	if strings.Contains(err.Error(), "i/o timeout") {
		return ErrorTypeTimeout
	}
	return ErrorTypeUnknown
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

func isUnreachable(err error) bool {
	for _, target := range []error{
		syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.EACCES, syscall.EHOSTDOWN,
		syscall.ENETDOWN, syscall.EPERM,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"unreachable", "no route to host", "network is down", "permission denied",
		"administratively prohibited", "host is down",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
