// Command lanory is the Lanory server.
//
// It is a single self-contained binary that serves the REST API, the SSE
// realtime channel and the built frontend. It never modifies the network
// configuration: Lanory is read-only with respect to WAN, LAN, DHCP, firewall,
// DNS and routes.
//
// Lifecycle:
//
//	parse flags -> load config -> open database -> build the app ->
//	listen -> wait for SIGINT/SIGTERM -> graceful shutdown
//
// The discovery and monitoring engines are wired in when the packages that own
// them are available. Every optional component is guarded by a nil check so the
// binary always starts, even with no engine wired at all.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/lanory/lanory/internal/api"
	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/demo"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/platform"
)

// Build information, overridden with -ldflags "-X main.version=...".
var (
	version = "0.1.0"
	commit  = "none"
)

// main is the process entry point.
func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "lanory: %v\n", err)
		os.Exit(1)
	}
}

// options holds the parsed command line.
type options struct {
	configPath  string
	demo        bool
	webDir      string
	showVersion bool
	printConfig bool
	dbPath      string
	port        int
	logLevel    string
	logPretty   bool
	authToken   string
}

// parseFlags parses argv and returns the options.
func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("lanory", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are reported by the caller

	fs.StringVar(&o.configPath, "config", "", "path to the configuration file")
	fs.BoolVar(&o.demo, "demo", false, "serve the built-in sample network instead of the real system")
	fs.StringVar(&o.webDir, "web-dir", "", "directory holding the built frontend")
	fs.BoolVar(&o.showVersion, "version", false, "print the version and exit")
	fs.BoolVar(&o.printConfig, "print-config", false, "print the effective configuration and exit")
	fs.StringVar(&o.dbPath, "db", "", "path to the SQLite database")
	fs.IntVar(&o.port, "port", 0, "port to listen on (overrides the configuration)")
	fs.StringVar(&o.logLevel, "log-level", "", "log level: debug, info, warn or error")
	fs.BoolVar(&o.logPretty, "log-pretty", true, "human readable console logging")
	fs.StringVar(&o.authToken, "auth-token", "", "bearer token for API authentication")

	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if o.port < 0 || o.port > 65535 {
		return o, fmt.Errorf("invalid --port %d (must be 1-65535)", o.port)
	}
	return o, nil
}

// usageText is printed on a flag error.
const usageText = `lanory — home network topology visualisation and monitoring

Usage:
  lanory [flags]

Flags:
      --config PATH       path to the configuration file
      --db PATH           path to the SQLite database
      --port N            port to listen on (default 8787)
      --web-dir PATH      directory holding the built frontend
      --demo              serve the built-in sample network
      --auth-token TOKEN  bearer token for API authentication
      --log-level LEVEL   debug, info, warn or error
      --log-pretty        human readable console logging
      --print-config      print the effective configuration and exit
      --version           print the version and exit
  -h, --help              show this help
`

// run executes the whole command with injectable writers, so tests can drive
// main without spawning a process.
func run(args []string, stdout, stderr io.Writer) error {
	opts, err := parseFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usageText)
			return nil
		}
		fmt.Fprint(stderr, usageText)
		return err
	}

	if opts.showVersion {
		fmt.Fprintf(stdout, "lanory %s (commit %s, %s/%s, go %s)\n",
			version, commit, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return nil
	}

	cfg, err := loadConfig(opts)
	if err != nil {
		return err
	}

	logger := newLogger(cfg, opts, stderr)
	slog.SetDefault(logger)

	if opts.printConfig {
		return printConfig(stdout, cfg)
	}

	return serve(cfg, opts, logger, stdout)
}

// loadConfig reads the configuration and applies the command line overrides.
//
// Precedence: defaults < file < environment < flags. Flags win because an
// operator who types --port expects it to take effect.
func loadConfig(opts options) (*config.Config, error) {
	var paths []string
	if opts.configPath != "" {
		paths = []string{opts.configPath}
	}
	cfg, err := config.Load(paths...)
	if err != nil {
		return nil, err
	}
	// If an explicit path was requested and the file is missing, that is an
	// error rather than a silent fall-through to the next default location.
	if opts.configPath != "" {
		if _, statErr := os.Stat(opts.configPath); statErr != nil {
			return nil, fmt.Errorf("config: %w", statErr)
		}
	}

	if opts.dbPath != "" {
		cfg.Database.Path = opts.dbPath
	}
	if opts.port != 0 {
		cfg.Server.Port = opts.port
	}
	if opts.webDir != "" {
		cfg.Server.WebDir = opts.webDir
	}
	if opts.demo {
		cfg.Demo.Enabled = true
	}
	if opts.authToken != "" {
		cfg.Auth.Token = opts.authToken
		cfg.Auth.Enabled = true
	}
	if opts.logLevel != "" {
		cfg.Logging.Level = opts.logLevel
	}
	return cfg, nil
}

// newLogger builds the structured logger.
//
// JSON is used when the log goes to a file or to syslog, because it is
// machine readable; pretty output is the console default.
func newLogger(cfg *config.Config, opts options, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(cfg.Logging.Level)) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	// Deterministic output: no source locations, no timestamps because the
	// OpenWrt log daemon adds them.
	handlerOpts := &slog.HandlerOptions{Level: level}
	pretty := cfg.Logging.Pretty && opts.logPretty && cfg.Logging.File == ""
	var out io.Writer = w
	if cfg.Logging.File != "" {
		f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			// Fall back to the console rather than refusing to start.
			fmt.Fprintf(w, "lanory: cannot open log file %s: %v\n", cfg.Logging.File, err)
		} else {
			out = f
			pretty = false
		}
	}

	var h slog.Handler
	if pretty {
		h = slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	} else {
		h = slog.NewJSONHandler(out, handlerOpts)
	}
	return slog.New(h)
}

// printConfig writes the effective configuration as YAML.
func printConfig(w io.Writer, cfg *config.Config) error {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("lanory-print-config-%d.yaml", os.Getpid()))
	defer os.Remove(tmp)
	if err := cfg.Write(tmp); err != nil {
		return err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// serve builds the application and runs it until a termination signal arrives.
func serve(cfg *config.Config, opts options, logger *slog.Logger, stdout io.Writer) error {
	logger.Info("lanory starting",
		"version", version,
		"commit", commit,
		"platform", platform.Detect().Describe(),
	)

	host := platform.Detect()
	logger.Info("host detected",
		"kind", host.Kind,
		"config_dir", host.Paths.ConfigDir,
		"data_dir", host.Paths.DataDir,
		"collectors", host.CollectorNames(),
	)

	db, err := database.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	store := database.NewStore(db)

	if v, err := db.Version(); err == nil {
		logger.Info("database ready", "path", db.Path(), "schema_version", v)
	}

	bus := events.New(512)
	api.Version = version

	app, err := api.New(api.Options{
		Config:   cfg,
		Store:    store,
		Bus:      bus,
		Platform: hostAdapter{host},
		WebDir:   cfg.Server.WebDir,
	})
	if err != nil {
		return fmt.Errorf("build application: %w", err)
	}

	// Demo mode: load the sample network before wiring the schedulers so the UI
	// is immediately populated. The sample data is clearly flagged as simulated
	// both in the database (SourceDemo) and in the UI (demo banner).
	demoProvider := demo.NewProvider(store, cfg.Demo.Enabled, cfg.Demo.Seed)
	app.SetDemo(demoProvider)
	if cfg.Demo.Enabled {
		if err := demoProvider.Seed(context.Background(), cfg.Demo.Seed); err != nil {
			logger.Warn("could not seed demo dataset", "error", err)
		}
	}

	// Optional engines. They start only when wired, so the binary works with
	// none of them present.
	startEngines(cfg, host, store, bus, app, demoProvider, logger)

	if cfg.Auth.Enabled && cfg.Auth.Token == "" {
		// A default install must not be reachable with a guessable token, so a
		// random one is generated and printed exactly once.
		token, err := api.GenerateToken()
		if err != nil {
			return fmt.Errorf("generate auth token: %w", err)
		}
		if err := persistToken(cfg, token, logger); err != nil {
			logger.Warn("could not persist the generated token to the token file", "error", err)
		}
		logger.Warn("authentication is enabled but no token was configured",
			"token", token,
			"hint", "set auth.token in the configuration or use --auth-token to make it permanent",
		)
		cfg.Auth.Token = token
		app.SetAuthToken(token)
	}

	// The listen address is printed to the configured output so an operator
	// (and a procd script) can find it.
	fmt.Fprintf(stdout, "lanory %s listening on %s (frontend: %s)\n",
		version, cfg.Addr(), app.FrontendSource())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// Drain the listen error so the goroutine does not leak.
	<-errCh
	logger.Info("lanory stopped")
	return nil
}

// persistToken stores a generated token in the configured token file with
// owner-only permissions, so a restart does not invalidate the browser session.
func persistToken(cfg *config.Config, token string, logger *slog.Logger) error {
	if cfg.Auth.TokenFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Auth.TokenFile), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.Auth.TokenFile, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	logger.Info("token written", "path", cfg.Auth.TokenFile)
	return nil
}

// startEngines wires the discovery and monitoring schedulers.
//
// The engines are developed in parallel with this command, so each is guarded
// by a nil check: when a constructor is unavailable the scheduler simply does
// not start and the log says so, instead of the binary failing to boot.
func startEngines(cfg *config.Config, host platform.Platform, store *database.Store, bus *events.Bus, app *api.App, demoProvider *demo.Provider, logger *slog.Logger) {
	newSchedulers(cfg, host, store, bus, app, demoProvider, logger)
}

// hostAdapter presents platform.Platform to the API layer through the narrow
// interface it declares, so internal/api does not import internal/platform.
type hostAdapter struct {
	p platform.Platform
}

func (a hostAdapter) IsOpenWrt() bool           { return a.p.IsOpenWrt() }
func (a hostAdapter) Supports(name string) bool { return a.p.Supports(name) }
func (a hostAdapter) Describe() string          { return a.p.Describe() }
