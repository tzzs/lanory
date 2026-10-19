// Package api implements the Lanory HTTP server: REST API, SSE and static
// frontend hosting.
//
// The package is deliberately dependency free apart from the standard library
// and golang.org/x/net. Routing uses the method-and-wildcard patterns added to
// net/http.ServeMux in Go 1.22 ("GET /api/v1/devices/{id}"), which gives the
// same ergonomics as a third party router without the dependency.
//
// Responsibilities:
//
//   - Wire the HTTP surface described in docs/api.md (the binding contract).
//   - Enforce authentication, CSRF, request limits and the SSRF boundary.
//   - Serve the built frontend with a SPA fallback.
//   - Stream realtime events over SSE from internal/events.Bus.
//
// The package does not own the discovery, topology, monitoring or demo engines.
// They are injected through the provider interfaces declared in providers.go;
// until they exist, providers_stub.go supplies store-only implementations so
// the server is complete and honest today.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/events"
)

// Version is the Lanory release reported by /api/v1/health.
//
// It is overridden at build time with -ldflags "-X main.version=...".
var Version = "0.1.0"

// apiPrefix is the base path of every documented route. When
// ServerConfig.BasePath is set, routes are additionally mounted under it so a
// reverse proxy can prefix the whole application.
const apiPrefix = "/api/v1"

// Default limits. They are constants rather than configuration so a request
// cannot talk the server into an unbounded response.
const (
	// DefaultLimit is the page size used when a client does not ask for one.
	DefaultLimit = 100
	// MaxLimit is the largest page a client may ask for.
	MaxLimit = 500
	// maxEventsLimit caps the event listing.
	maxEventsLimit = 500
	// defaultSSEHeartbeat is the comment interval that keeps proxies from
	// dropping an idle SSE connection.
	defaultSSEHeartbeat = 15 * time.Second
	// defaultRequestTimeout bounds a normal API request.
	defaultRequestTimeout = 30 * time.Second
	// defaultShutdownTimeout bounds graceful shutdown.
	defaultShutdownTimeout = 15 * time.Second
	// defaultEventBuffer is the per-connection SSE backlog.
	defaultEventBuffer = 256
)

// ErrServerClosed is returned when a shutdown is already in progress.
var ErrServerClosed = errors.New("api: server already closed")

// App holds everything the HTTP layer needs. It is constructed once with
// explicit dependencies and has no global state.
type App struct {
	cfg *config.Config
	// store is the persistence layer.
	store *database.Store
	// bus carries realtime events to SSE clients.
	bus *events.Bus
	// ssrf guards probe targets.
	ssrf *SSRFGuard

	// topo builds the topology graph.
	topo TopologyProvider
	// discovery runs and reports discovery jobs.
	discovery DiscoveryProvider
	// monitoring runs and reports probes.
	monitoring MonitoringProvider
	// demo loads the sample network.
	demo DemoProvider
	// settings reads and writes effective settings.
	settings SettingsProvider
	// ifaces reports interface status.
	ifaces InterfaceStatusProvider

	// platform describes the host Lanory runs on.
	platform platformInfo

	// assets serves the frontend.
	assets *staticAssets
	// mux is the root handler.
	mux http.Handler
	// server is the running http.Server.
	server *http.Server

	// startedAt is the process start time, used for uptime_seconds.
	startedAt time.Time
	// limiter throttles abusive clients.
	limiter *rateLimiter

	mu      sync.Mutex
	closed  bool
	closeCh chan struct{}
}

// Options configures an App. Every field is optional; the zero value produces
// a server with no engines wired, which still serves health, devices, events,
// topology and the frontend from the store.
type Options struct {
	// Config is the loaded configuration. Required.
	Config *config.Config
	// Store is the persistence layer. Required.
	Store *database.Store
	// Bus carries realtime events. When nil, SSE returns no events (but the
	// endpoint still works and heartbeats).
	Bus *events.Bus
	// Platform describes the host. Optional.
	Platform platformInfo
	// WebDir overrides Config.Server.WebDir for the frontend.
	WebDir string
	// Logger is unused directly (slog.Default is used) but keeps the option
	// set explicit for future structured logging.
	Logger *slog.Logger
}

// New builds an App from explicit dependencies.
//
// No engine is started here: New only wires the HTTP surface. Call Run or
// Serve to start listening.
func New(opts Options) (*App, error) {
	if opts.Config == nil {
		return nil, errors.New("api: config is required")
	}
	if opts.Store == nil {
		return nil, errors.New("api: store is required")
	}

	a := &App{
		cfg:       opts.Config,
		store:     opts.Store,
		bus:       opts.Bus,
		platform:  opts.Platform,
		startedAt: time.Now(),
		limiter:   newRateLimiter(240, time.Minute),
		closeCh:   make(chan struct{}),
	}

	// Authorised subnets: configuration first, then the networks Lanory itself
	// knows about. Discovered networks are derived from stored addresses, never
	// from the request.
	subnets := append([]string{}, opts.Config.Discovery.Subnets...)
	subnets = append(subnets, discoveredSubnets(opts.Store)...)
	a.ssrf = NewSSRFGuard(subnets, false)
	a.refreshKnownTargets()

	webDir := opts.WebDir
	if webDir == "" {
		webDir = opts.Config.Server.WebDir
	}
	a.assets = newStaticAssets(webDir)

	a.topo = newStoreTopology(opts.Store)
	a.ifaces = &storeMonitoring{store: opts.Store, platform: opts.Platform}
	a.discovery = newStoreDiscovery(opts.Store, opts.Platform)
	a.monitoring = newStoreMonitoring(opts.Store, opts.Platform)
	a.demo = newStoreDemo(opts.Config.Demo.Enabled, opts.Config.Demo.Seed)
	a.settings = newStoreSettings(opts.Store, settingsFromConfig(opts.Config))

	a.mux = a.routes()
	return a, nil
}

// netipPrefix renders addr/prefixBits as a CIDR string.
func netipPrefix(addr netip.Addr, prefixBits int) string {
	return netip.PrefixFrom(addr.Unmap(), prefixBits).String()
}

// discoveredSubnets derives the /24 networks of every stored device address.
//
// Only RFC1918 and CGNAT space is accepted: a device that happens to hold a
// public address must not make that address range probeable.
func discoveredSubnets(store *database.Store) []string {
	devices, _, err := store.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range devices {
		for _, raw := range []string{d.PrimaryIPv4, d.PrimaryIPv6} {
			if raw == "" {
				continue
			}
			addr, err := ParseIP(raw)
			if err != nil {
				continue
			}
			addr = addr.Unmap()
			if !isPrivateAddr(addr) {
				continue
			}
			bits := 24
			if addr.Is6() {
				bits = 64
			}
			p := netipPrefix(addr, bits)
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// refreshKnownTargets registers the addresses and hostnames of known devices
// with the SSRF guard.
func (a *App) refreshKnownTargets() {
	devices, _, err := a.store.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		return
	}
	for _, d := range devices {
		if d.Hostname != "" {
			a.ssrf.AddKnownHostname(d.Hostname)
		}
		if d.Name != "" {
			a.ssrf.AddKnownHostname(d.Name)
		}
		for _, raw := range []string{d.PrimaryIPv4, d.PrimaryIPv6} {
			if raw == "" {
				continue
			}
			if addr, err := ParseIP(raw); err == nil {
				a.ssrf.AddKnownAddress(addr)
			}
		}
		addrs, err := a.store.ListAddresses(d.ID)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if a2, err := ParseIP(addr.Address); err == nil {
				a.ssrf.AddKnownAddress(a2)
			}
		}
	}
}

// SetTopology injects the real topology engine, replacing the store-only stub.
func (a *App) SetTopology(p TopologyProvider) {
	if p != nil {
		a.topo = p
	}
}

// SetDiscovery injects the real discovery engine.
func (a *App) SetDiscovery(p DiscoveryProvider) {
	if p != nil {
		a.discovery = p
	}
}

// SetMonitoring injects the real monitoring engine.
func (a *App) SetMonitoring(p MonitoringProvider) {
	if p != nil {
		a.monitoring = p
	}
}

// SetDemo injects the real demo loader.
func (a *App) SetDemo(p DemoProvider) {
	if p != nil {
		a.demo = p
	}
}

// SetSettings injects a settings provider backed by the real configuration.
func (a *App) SetSettings(p SettingsProvider) {
	if p != nil {
		a.settings = p
	}
}

// SetPlatform records the detected platform for the discovery status.
func (a *App) SetPlatform(p platformInfo) {
	if p != nil {
		a.platform = p
	}
}

// AuthToken returns the current bearer token.
//
// The token is read on every request rather than captured at build time,
// because the startup path may generate one after the handler chain exists
// (when authentication is enabled but no token was configured).
func (a *App) AuthToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Auth.Token
}

// SetAuthToken installs the bearer token after construction.
//
// It exists for the startup path where no token was configured and one is
// generated: rebuilding the whole application for that would restart nothing
// but waste the work. The next request sees the new token.
func (a *App) SetAuthToken(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Auth.Token = token
	// Authentication without a token is not authentication, so it stays off
	// until a token exists.
	a.cfg.Auth.Enabled = a.cfg.Auth.Enabled || token != ""
}

// FrontendSource reports where the static frontend comes from: "embedded",
// "disk" or "placeholder".
func (a *App) FrontendSource() string { return a.assets.Source() }

// Bus exposes the event bus so callers can publish.
func (a *App) Bus() *events.Bus { return a.bus }

// Store exposes the persistence layer.
func (a *App) Store() *database.Store { return a.store }

// Config exposes the loaded configuration.
func (a *App) Config() *config.Config { return a.cfg }

// Handler returns the root http.Handler.
func (a *App) Handler() http.Handler { return a.mux }

// ---------------------------------------------------------------------------
// Routing
// ---------------------------------------------------------------------------

// routes builds the root handler.
//
// Middleware order (outermost first):
//
//	recoverPanic   — a panic in any handler becomes a JSON 500
//	securityHeaders— nosniff, frame options, referrer policy
//	requestLogger  — one structured log line per request
//	requestTimeout — bounds every request except SSE
//	limitBody      — 1 MB cap
//	rate limiter   — per-IP fixed window
//	authenticate   — bearer token (health exempt)
//	csrfProtect    — custom header on state changing methods
//
// The API and the static assets are two sibling muxes so a request for
// /api/v1/... never reaches the filesystem handler, and an unknown API path
// still returns a JSON 404 rather than the SPA shell.
func (a *App) routes() http.Handler {
	apiMux := http.NewServeMux()
	a.registerAPIRoutes(apiMux)

	var apiHandler http.Handler = apiMux
	// Translate the plain-text 404/405 that ServeMux writes into the JSON
	// envelope, so every error response has the documented shape.
	apiHandler = jsonNotFound(apiHandler)

	root := http.NewServeMux()
	base := strings.TrimSuffix(a.cfg.Server.BasePath, "/")
	if base != "" {
		// Mount the API under the configured reverse-proxy prefix.
		root.Handle(base+"/api/v1", apiHandler)
		root.Handle(base+"/api/v1/", apiHandler)
		root.Handle(base, a.assets.Handler())
		root.Handle(base+"/", http.StripPrefix(base, a.assets.Handler()))
	} else {
		root.Handle("/api/v1", apiHandler)
		root.Handle("/api/v1/", apiHandler)
		root.Handle("/", a.assets.Handler())
	}

	var h http.Handler = root
	h = recoverPanic(h)
	h = securityHeaders(h)
	h = requestLogger(h)
	h = requestTimeout(defaultRequestTimeout, isStreamRequest)(h)
	h = limitBody(h)
	h = a.limiter.middleware(h)

	auth := authConfig{
		enabled:     a.cfg.Auth.Enabled,
		allowLAN:    a.cfg.Auth.AllowLAN,
		trustedNets: trustedPrefixes(a.cfg.Server.TrustedProxies),
		basePath:    strings.TrimSuffix(a.cfg.Server.BasePath, "/"),
		token:       a.AuthToken,
	}
	h = auth.middleware(h)
	h = csrfProtect(h)
	return h
}

// trustedPrefixes parses the configured trusted proxy CIDRs.
func trustedPrefixes(cidrs []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		if p, err := ParseCIDR(c); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// jsonNotFound rewrites the plain-text 404/405 that net/http writes into the
// JSON error envelope, without touching successful responses.
func jsonNotFound(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusCapture{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusNotFound || rec.status == http.StatusMethodNotAllowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rec.status)
			msg := "not found"
			if rec.status == http.StatusMethodNotAllowed {
				msg = "method not allowed"
			}
			_, _ = w.Write([]byte(`{"error":"` + msg + `","code":"` + codeForStatus(rec.status) + `"}`))
		}
	})
}

// statusCapture suppresses a body and status that the wrapped handler wrote
// when it was a 404/405, so the JSON envelope can be emitted instead.
type statusCapture struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusCapture) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote, s.status = true, code
	if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
		// Hold the status; the wrapper writes the JSON body.
		return
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Write(b []byte) (int, error) {
	if !s.wrote {
		s.wrote, s.status = true, http.StatusOK
	}
	if s.status == http.StatusNotFound || s.status == http.StatusMethodNotAllowed {
		return len(b), nil
	}
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the wrapped writer so SSE responses are not buffered.
func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer for http.NewResponseController.
func (s *statusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// registerAPIRoutes declares every documented route.
//
// The method-and-pattern syntax is what makes 405 possible: registering only
// the documented methods on a path makes net/http answer anything else with
// 405 plus an Allow header.
func (a *App) registerAPIRoutes(mux *http.ServeMux) {
	// Health is the only unauthenticated route.
	mux.HandleFunc("GET /api/v1/health", a.handleHealth)

	// Overview.
	mux.HandleFunc("GET /api/v1/overview", a.handleOverview)

	// Devices.
	mux.HandleFunc("GET /api/v1/devices", a.handleListDevices)
	mux.HandleFunc("POST /api/v1/devices", a.handleCreateDevice)
	mux.HandleFunc("GET /api/v1/devices/{id}", a.handleGetDevice)
	mux.HandleFunc("PATCH /api/v1/devices/{id}", a.handlePatchDevice)
	mux.HandleFunc("DELETE /api/v1/devices/{id}", a.handleDeleteDevice)
	mux.HandleFunc("POST /api/v1/devices/{id}/merge", a.handleMergeDevice)

	// Topology.
	mux.HandleFunc("GET /api/v1/topology", a.handleTopology)
	mux.HandleFunc("GET /api/v1/topology/evidence", a.handleTopologyEvidence)
	mux.HandleFunc("POST /api/v1/topology/edges", a.handleCreateEdge)
	mux.HandleFunc("GET /api/v1/topology/edges/{id}", a.handleGetEdge)
	mux.HandleFunc("PATCH /api/v1/topology/edges/{id}", a.handlePatchEdge)
	mux.HandleFunc("DELETE /api/v1/topology/edges/{id}", a.handleDeleteEdge)
	mux.HandleFunc("POST /api/v1/topology/layout", a.handleSaveLayout)
	mux.HandleFunc("POST /api/v1/topology/auto-layout", a.handleAutoLayout)

	// Discovery.
	mux.HandleFunc("GET /api/v1/discovery/status", a.handleDiscoveryStatus)
	mux.HandleFunc("POST /api/v1/discovery/scan", a.handleStartScan)
	mux.HandleFunc("GET /api/v1/discovery/jobs/{id}", a.handleDiscoveryJob)
	mux.HandleFunc("POST /api/v1/discovery/jobs/{id}/cancel", a.handleCancelDiscoveryJob)
	mux.HandleFunc("GET /api/v1/discovery/collectors", a.handleDiscoveryCollectors)

	// Monitoring.
	mux.HandleFunc("GET /api/v1/monitoring/status", a.handleMonitoringStatus)
	mux.HandleFunc("GET /api/v1/monitoring/probes", a.handleListProbes)
	mux.HandleFunc("POST /api/v1/monitoring/probes", a.handleCreateProbe)
	mux.HandleFunc("GET /api/v1/monitoring/probes/{id}", a.handleGetProbe)
	mux.HandleFunc("PATCH /api/v1/monitoring/probes/{id}", a.handlePatchProbe)
	mux.HandleFunc("DELETE /api/v1/monitoring/probes/{id}", a.handleDeleteProbe)
	mux.HandleFunc("POST /api/v1/monitoring/probes/{id}/run", a.handleRunProbe)
	mux.HandleFunc("GET /api/v1/monitoring/history", a.handleMonitoringHistory)

	// Events.
	mux.HandleFunc("GET /api/v1/events", a.handleListEvents)
	mux.HandleFunc("GET /api/v1/events/stream", a.handleEventStream)
	mux.HandleFunc("GET /api/v1/events/{id}", a.handleGetEvent)

	// Settings.
	mux.HandleFunc("GET /api/v1/settings", a.handleGetSettings)
	mux.HandleFunc("PATCH /api/v1/settings", a.handlePatchSettings)
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// ListenAndServe starts the HTTP server and blocks until it stops.
//
// It is a thin wrapper around http.Server.ListenAndServe so callers can use
// App exactly like an http.Server.
func (a *App) ListenAndServe() error {
	srv := &http.Server{
		Addr:              a.cfg.Addr(),
		Handler:           a.mux,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE streams are long lived. Idle protection comes
		// from ReadHeaderTimeout and the request context instead.
		IdleTimeout: 120 * time.Second,
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrServerClosed
	}
	a.server = srv
	a.mu.Unlock()

	slog.Default().Info("http server listening",
		"addr", srv.Addr,
		"frontend", a.assets.Source(),
		"auth", a.cfg.Auth.Enabled,
		"demo_mode", a.cfg.Demo.Enabled,
	)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown drains in-flight requests within a deadline.
//
// SSE connections are cancelled through the request context, so a shutdown
// does not hang waiting for a client that never disconnects.
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	srv := a.server
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	close(a.closeCh)
	a.mu.Unlock()

	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Done returns a channel that is closed once Shutdown has been called.
func (a *App) Done() <-chan struct{} { return a.closeCh }

// ---------------------------------------------------------------------------
// Query helpers
// ---------------------------------------------------------------------------

// parseIntStrict parses a plain integer with no sign or padding tricks.
func parseIntStrict(v string) (int, error) {
	if v == "" {
		return 0, fmt.Errorf("empty value")
	}
	n := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("%q is not a non-negative integer", v)
		}
		n = n*10 + int(c-'0')
		if n > 1<<31 {
			return 0, fmt.Errorf("%q is too large", v)
		}
	}
	return n, nil
}

// pagination reads limit/offset from the query string.
//
// Invalid values produce a 400 with a message naming the offending parameter;
// out-of-range values are clamped rather than rejected, so a client that asks
// for limit=100000 still gets a usable page.
func pagination(r *http.Request, defaultLimit, maxLimit int) (limit, offset int, err error) {
	q := r.URL.Query()
	limit = defaultLimit
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, perr := parseIntParam(v, "limit")
		if perr != nil {
			return 0, 0, perr
		}
		limit = n
	}
	offset = 0
	if v := strings.TrimSpace(q.Get("offset")); v != "" {
		n, perr := parseIntParam(v, "offset")
		if perr != nil {
			return 0, 0, perr
		}
		offset = n
	}
	if limit < 1 {
		return 0, 0, fmt.Errorf("%w: limit must be >= 1", ErrInvalid)
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		return 0, 0, fmt.Errorf("%w: offset must be >= 0", ErrInvalid)
	}
	return limit, offset, nil
}

// parseIntParam parses a bounded non-negative integer query parameter.
func parseIntParam(v, name string) (int, error) {
	n, err := parseIntStrict(v)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrInvalid, name)
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: %s must not be negative", ErrInvalid, name)
	}
	return n, nil
}

// pathID extracts and validates a path parameter.
func pathID(r *http.Request, name string) (string, error) {
	v := strings.TrimSpace(r.PathValue(name))
	if v == "" {
		return "", fmt.Errorf("%w: missing %s", ErrInvalid, name)
	}
	if len(v) > 128 {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalid, name)
	}
	return v, nil
}

// badRequestFrom converts a validation error into a 400 response.
func badRequestFrom(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, errBodyTooLarge) {
		fail(w, http.StatusRequestEntityTooLarge, "request body exceeds the 1 MB limit")
		return
	}
	badRequest(w, err.Error())
}

// unwired reports a capability that needs an engine that is not wired (501).
func unwired(w http.ResponseWriter, err error) {
	if errors.Is(err, errProviderUnwired) {
		fail(w, http.StatusNotImplemented, err.Error())
		return
	}
	internalError(w, err)
}

// settingsFromConfig flattens the configuration into the setting keys the UI
// can display and edit.
func settingsFromConfig(cfg *config.Config) map[string]string {
	return map[string]string{
		"server.host":                     cfg.Server.Host,
		"server.port":                     fmt.Sprintf("%d", cfg.Server.Port),
		"server.web_dir":                  cfg.Server.WebDir,
		"database.path":                   cfg.Database.Path,
		"database.retention_days":         fmt.Sprintf("%d", cfg.Database.RetentionDays),
		"discovery.enabled":               boolString(cfg.Discovery.Enabled),
		"discovery.interval":              cfg.Discovery.Interval,
		"discovery.auto_detect_subnets":   boolString(cfg.Discovery.AutoDetectSubnets),
		"discovery.max_concurrency":       fmt.Sprintf("%d", cfg.Discovery.MaxConcurrency),
		"discovery.rate_limit":            fmt.Sprintf("%d", cfg.Discovery.RateLimit),
		"monitoring.enabled":              boolString(cfg.Monitoring.Enabled),
		"monitoring.interval":             cfg.Monitoring.Interval,
		"monitoring.timeout":              cfg.Monitoring.Timeout,
		"monitoring.failure_threshold":    fmt.Sprintf("%d", cfg.Monitoring.FailureThreshold),
		"monitoring.recover_confirm":      fmt.Sprintf("%d", cfg.Monitoring.RecoverConfirm),
		"topology.auto_layout":            boolString(cfg.Topology.AutoLayout),
		"topology.preserve_manual_layout": boolString(cfg.Topology.PreserveManualLayout),
		"auth.enabled":                    boolString(cfg.Auth.Enabled),
		"auth.allow_lan":                  boolString(cfg.Auth.AllowLAN),
		"logging.level":                   cfg.Logging.Level,
		"demo.enabled":                    boolString(cfg.Demo.Enabled),
		"demo.seed":                       cfg.Demo.Seed,
	}
}

// knownSettings is the allow-list of writable setting keys.
var knownSettings = map[string]bool{
	"discovery.enabled":               true,
	"discovery.interval":              true,
	"discovery.auto_detect_subnets":   true,
	"discovery.rate_limit":            true,
	"monitoring.enabled":              true,
	"monitoring.interval":             true,
	"monitoring.timeout":              true,
	"monitoring.failure_threshold":    true,
	"monitoring.recover_confirm":      true,
	"topology.auto_layout":            true,
	"topology.preserve_manual_layout": true,
	"auth.allow_lan":                  true,
	"logging.level":                   true,
	"database.retention_days":         true,
	"demo.enabled":                    true,
}

// boolString renders a boolean as the string stored in settings.
func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// demoMode reports whether the server serves the sample network.
func (a *App) demoMode() bool {
	return a.cfg.Demo.Enabled
}
