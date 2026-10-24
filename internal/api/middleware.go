package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Security and plumbing middleware.
//
// Everything here is built on the standard library only. The middlewares are
// deliberately small and composable; the composition lives in Server.routes.

// ---------------------------------------------------------------------------
// Security headers
// ---------------------------------------------------------------------------

// securityHeaders sets the response headers that keep a browser from helping
// an attacker. Lanory serves its own single-page app and a JSON API, so it can
// afford a strict policy.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// The API never returns attacker controllable content types, so
		// nosniff is safe and blocks MIME confusion.
		h.Set("X-Content-Type-Options", "nosniff")
		// Only the explicit embed entry may be framed by this router's own
		// web interface (LuCI uses a different port). Standalone and API
		// responses retain DENY. Never grant arbitrary LAN sites access.
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/embed" || strings.HasPrefix(r.URL.Path, "/embed/") {
			host := (&url.URL{Host: r.Host}).Hostname()
			// Parsing again rejects whitespace, quotes and CSP delimiters in
			// an untrusted Host header. IPv6 source expressions need brackets.
			candidate, err := url.Parse("http://" + r.Host)
			if err == nil && candidate.Host == r.Host && candidate.Hostname() == host &&
				host != "" && !strings.ContainsAny(host, " ;'\"\\\t\r\n") {
				if strings.Contains(host, ":") {
					host = "[" + host + "]"
				}
				h.Del("X-Frame-Options")
				h.Set("Content-Security-Policy", "frame-ancestors http://"+host+":* https://"+host+":*")
			}
		}
		// Do not leak the full URL (which can carry a token in a query) to
		// third parties.
		h.Set("Referrer-Policy", "no-referrer")
		// Least privilege: Lanory needs none of these browser features.
		h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=()")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Request body limit and timeout
// ---------------------------------------------------------------------------

// maxBodyBytes caps every request body. A 1 MB limit is generous for the
// payloads this API accepts (a device, an edge, a handful of layout nodes) and
// keeps a rogue client from exhausting router memory.
const maxBodyBytes = 1 << 20

// limitBody caps the request body size. http.MaxBytesReader already answers
// with 413, but the error is delivered as plain text, so it is translated into
// the JSON envelope by the handler chain.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// requestTimeout bounds the wall clock time of a single request.
//
// SSE is exempt: a stream is a long lived request by definition and cancels it
// would break the realtime channel.
func requestTimeout(d time.Duration, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d <= 0 || (exempt != nil && exempt(r)) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// isStreamRequest reports whether r is the SSE endpoint.
func isStreamRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/v1/events/stream")
}

// ---------------------------------------------------------------------------
// Panic recovery
// ---------------------------------------------------------------------------

// recoverPanic turns a panic in a handler into a JSON 500.
//
// A router must never die because one request misbehaved: discovery keeps
// running and the next request must still be served. The stack is logged, never
// returned to the client.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Default().Error("panic recovered",
					"panic", rec,
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				// The handler may already have written a partial response; in
				// that case the status is already on the wire and this only
				// guarantees a JSON body for the common case.
				fail(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Request logging
// ---------------------------------------------------------------------------

// statusRecorder captures the response status and byte count for logging.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.written += int64(n)
	return n, err
}

// Flush forwards to the wrapped writer so SSE and chunked responses work.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer for http.NewResponseController.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// requestLogger logs one line per request at debug level for health checks and
// info level for everything else, so a monitoring poll does not spam the log.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", rec.written,
			"remote", remoteIP(r),
		}
		level := slog.LevelInfo
		if r.URL.Path == "/api/v1/health" {
			level = slog.LevelDebug
		} else if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Default().Log(r.Context(), level, "http request", attrs...)
	})
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

// csrfHeader is the custom header required on state changing requests.
//
// The browser never sends this header on its own: a cross-site form post or
// fetch cannot set it without a CORS preflight, and Lanory answers no CORS
// preflight. Requiring it makes CSRF structurally impossible for the same
// origin case, and is what web/src/lib/api.ts already sends.
const csrfHeader = "X-Requested-With"

// csrfValue is the value the header must carry.
const csrfValue = "XMLHttpRequest"

const doubleSubmitCookie = "lanory_csrf"

// authConfig is what the auth middleware needs from the configuration.
type authConfig struct {
	// enabled reports whether authentication is active. It is a function for the
	// same reason token is: the OpenWrt init script supplies the token through an
	// environment variable after the handler chain exists, and that must switch
	// authentication on without rebuilding the chain.
	enabled     func() bool
	allowLAN    bool
	trustedNets []netip.Prefix
	// basePath is the configured reverse-proxy prefix, so the API is
	// recognised wherever it is mounted.
	basePath string
	// token returns the current bearer token. It is a function because the
	// token can be generated after the handler chain is built.
	token func() string
}

// authenticate enforces the bearer token on API routes.
//
// /api/v1/health is always public: a load balancer or a watchdog must be able
// to check liveness without credentials.
//
// When AllowLAN is set, requests from a private address are exempt so a router
// on a trusted LAN can be used without typing a token. The exemption only ever
// widens to RFC1918 and CGNAT space; anything else still needs the token.
func (a authConfig) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if a.token != nil {
			token = a.token()
		}
		enabled := false
		if a.enabled != nil {
			enabled = a.enabled()
		}
		if !enabled || token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if a.allowLAN && a.isTrusted(r) {
			next.ServeHTTP(w, r)
			return
		}
		// The static frontend is public by design: a browser cannot attach an
		// Authorization header when it loads a document, and the login page
		// must be reachable before the user has typed a token.
		if !isAPIPath(r.URL.Path, a.basePath) {
			next.ServeHTTP(w, r)
			return
		}
		if !bearerOK(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="lanory"`)
			unauthorized(w, "missing or invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isTrusted reports whether the request comes from an exempt address.
func (a authConfig) isTrusted(r *http.Request) bool {
	ip := remoteIP(r)
	if ip == nil {
		return false
	}
	addr, err := netip.ParseAddr(ip.String())
	if err != nil {
		return false
	}
	if addr.IsLoopback() {
		// A local browser on the router itself is as trusted as the LAN.
		return true
	}
	if len(a.trustedNets) == 0 {
		return isPrivateAddr(addr)
	}
	for _, p := range a.trustedNets {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// isPrivateAddress reports whether ip is in RFC1918, CGNAT, loopback or
// link-local space.
func isPrivateAddress(ip *net.IP) bool {
	if ip == nil {
		return false
	}
	addr, err := netip.ParseAddr(ip.String())
	if err != nil {
		return false
	}
	return isPrivateAddr(addr)
}

// isPrivateAddr is the netip spelling of isPrivateAddress.
func isPrivateAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}
	if addr.Is4() {
		b := addr.As4()
		switch {
		case b[0] == 10:
			return true
		case b[0] == 172 && b[1] >= 16 && b[1] <= 31:
			return true
		case b[0] == 192 && b[1] == 168:
			return true
		case b[0] == 100 && b[1] >= 64 && b[1] <= 127:
			// CGNAT 100.64.0.0/10
			return true
		}
	}
	return false
}

// bearerOK compares the Authorization header against the configured token.
//
// The comparison is constant time so a token cannot be recovered by timing the
// endpoint. Only the exact "Bearer <token>" form is accepted; query string
// tokens are refused because URLs end up in logs and in the Referer header.
func bearerOK(r *http.Request, token string) bool {
	h := r.Header.Get("Authorization")
	if h == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimSpace(h[len(prefix):])
	if got == "" {
		return false
	}
	return subtleEqual(got, token)
}

// subtleEqual compares two strings in constant time.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// csrfProtect requires proof that the request came from Lanory's own UI on
// every state changing request.
//
// Two accepted forms:
//
//  1. The X-Requested-With header (what the frontend sends). A cross-site
//     attacker cannot set a custom header without a CORS preflight, and Lanory
//     never answers one.
//  2. A double-submit cookie: the request carries a random cookie value and
//     echoes it in the X-CSRF-Token header, which a cross-site attacker cannot
//     read because of the same-origin policy.
//
// GET, HEAD, OPTIONS and TRACE are safe methods and are exempt.
func csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get(csrfHeader) == csrfValue {
			next.ServeHTTP(w, r)
			return
		}
		if cookie, err := r.Cookie(doubleSubmitCookie); err == nil && cookie.Value != "" {
			if subtleEqual(cookie.Value, r.Header.Get("X-CSRF-Token")) {
				next.ServeHTTP(w, r)
				return
			}
		}
		forbidden(w, "missing "+csrfHeader+" header")
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// remoteIP extracts the client IP, honouring X-Forwarded-For only when the
// immediate peer is a trusted proxy.
//
// Honouring the header unconditionally would let any client claim to come from
// 127.0.0.1 and defeat the LAN exemption and the rate limiter.
func remoteIP(r *http.Request) *net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return nil
	}
	if isTrustedProxy(ip) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// The right-most entry is the one appended by our own proxy.
			candidate := net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
			if candidate != nil {
				return &candidate
			}
		}
	}
	return &ip
}

// trustedProxies holds the CIDRs whose X-Forwarded-For headers are honoured.
//
// It is set once from config.ServerConfig.TrustedProxies during startup, before
// the server accepts a request, and is read-only afterwards, so no mutex is
// needed.
var trustedProxies []netip.Prefix

// SetTrustedProxies installs the CIDRs whose X-Forwarded-For headers are
// honoured. It is called once during startup, before any request is served.
func SetTrustedProxies(cidrs []string) {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		if p, err := ParseCIDR(c); err == nil {
			out = append(out, p.Masked())
		}
	}
	trustedProxies = out
}

// isTrustedProxy reports whether ip is a configured trusted proxy.
func isTrustedProxy(ip net.IP) bool {
	addr, err := netip.ParseAddr(ip.String())
	if err != nil {
		return false
	}
	if len(trustedProxies) == 0 {
		return false
	}
	for _, p := range trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// isPublicPath reports whether a path is served without authentication.
//
// Only the health endpoint is public among the API routes: a watchdog, a
// reverse proxy or an OpenWrt procd health check must probe liveness without a
// token. Everything else, including the SSE stream, requires auth.
//
// The static frontend is deliberately not listed here. Its files are served by
// a separate handler that the API middleware never sees, so the browser can
// load the login page and the assets without credentials.
func isPublicPath(path string) bool {
	return isHealthPath(path)
}

// isHealthPath reports whether path is the unauthenticated health endpoint.
func isHealthPath(path string) bool {
	return path == "/api/v1/health" || path == "/health"
}

// isAPIPath reports whether path belongs to the versioned REST API.
//
// It is the boundary between "authenticated" and "public": the API and only the
// API carries network information and can trigger side effects. Everything else
// (the frontend, its assets, and client-side routes) is public because a
// browser cannot attach credentials to a document request.
func isAPIPath(path, basePath string) bool {
	if strings.HasPrefix(path, apiPrefix) {
		return true
	}
	// A configured reverse-proxy prefix wraps the API too.
	return basePath != "" && strings.HasPrefix(path, basePath+apiPrefix)
}

// ---------------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------------

// rateLimiter is a fixed-window per-IP limiter.
//
// It is intentionally coarse: its job is to stop a runaway client or a script
// from hammering a router, not to be a fair scheduler. A fixed window can allow
// a short burst at a window boundary; that is acceptable here and keeps the
// memory footprint constant (one entry per client, no queue).
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string]*rateBucket
	limit  int
	window time.Duration
	now    func() time.Time
}

type rateBucket struct {
	count   int
	resetAt time.Time
}

// newRateLimiter returns a limiter allowing limit requests per window per IP.
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if limit <= 0 {
		limit = 120
	}
	if window <= 0 {
		window = time.Minute
	}
	return &rateLimiter{
		hits:   map[string]*rateBucket{},
		limit:  limit,
		window: window,
		now:    time.Now,
	}
}

// allow reports whether a request from key is within the limit, recording it
// when it is.
func (rl *rateLimiter) allow(key string) bool {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.hits[key]
	if !ok || now.After(b.resetAt) {
		rl.hits[key] = &rateBucket{count: 1, resetAt: now.Add(rl.window)}
		rl.gcLocked(now)
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// gcLocked drops expired buckets so the map does not grow without bound.
func (rl *rateLimiter) gcLocked(now time.Time) {
	if len(rl.hits) < 1024 {
		return
	}
	for k, b := range rl.hits {
		if now.After(b.resetAt) {
			delete(rl.hits, k)
		}
	}
}

// middleware wraps next with the limit. Requests over the limit get 429.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := remoteIP(r)
		key := "anon"
		if ip != nil {
			key = ip.String()
		}
		if !rl.allow(key) {
			w.Header().Set("Retry-After", "60")
			fail(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Token generation
// ---------------------------------------------------------------------------

// GenerateToken returns a cryptographically random bearer token.
//
// It is used at startup when authentication is enabled but no token is
// configured: a default install must not be reachable with a guessable token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// errBodyTooLarge marks a body larger than maxBodyBytes.
//
// http.MaxBytesReader produces an *http.MaxBytesError, which is checked with
// errors.As. The sentinel exists so the check reads clearly at the call site.
var errBodyTooLarge = errors.New("request body too large")

// isBodyTooLarge reports whether err comes from http.MaxBytesReader.
func isBodyTooLarge(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errBodyTooLarge) {
		return true
	}
	var too *http.MaxBytesError
	return errors.As(err, &too)
}
