// Package config implements the Lanory configuration system.
//
// Configuration is layered:
//
//	built-in defaults  <  config file (YAML)  <  environment variables
//	                                          <  database overrides (Web UI)
//
// The Web UI writes to the database layer (app_settings), which overrides the
// file. This lets a router be tuned from the browser without editing files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	Discovery  DiscoveryConfig  `yaml:"discovery"`
	Collectors CollectorsConfig `yaml:"collectors"`
	Monitoring MonitoringConfig `yaml:"monitoring"`
	Topology   TopologyConfig   `yaml:"topology"`
	Auth       AuthConfig       `yaml:"auth"`
	Logging    LoggingConfig    `yaml:"logging"`
	Demo       DemoConfig       `yaml:"demo"`
}

// ServerConfig controls the HTTP server.
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// BasePath allows mounting the UI behind a reverse proxy prefix.
	BasePath string `yaml:"base_path"`
	// WebDir is the directory holding the built frontend. Empty means the
	// static assets are embedded in the binary.
	WebDir string `yaml:"web_dir"`
	// TrustedProxies lists CIDRs whose X-Forwarded-For headers are honoured.
	TrustedProxies []string `yaml:"trusted_proxies"`
}

// DatabaseConfig controls SQLite.
type DatabaseConfig struct {
	Path string `yaml:"path"`
	// RetentionDays prunes events, probe results and evidence older than this.
	RetentionDays int `yaml:"retention_days"`
}

// DiscoveryConfig controls the discovery engine.
type DiscoveryConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Interval string `yaml:"interval"`
	// AutoDetectSubnets derives scan targets from configured LAN networks.
	AutoDetectSubnets bool `yaml:"auto_detect_subnets"`
	MaxConcurrency    int  `yaml:"max_concurrency"`
	// Subnets lists extra user authorised CIDRs to scan.
	Subnets []string `yaml:"subnets"`
	// ScanTimeout is the per-host timeout for active probes.
	ScanTimeout string `yaml:"scan_timeout"`
	// RateLimit is the maximum number of probes per second.
	RateLimit int `yaml:"rate_limit"`
}

// CollectorsConfig enables or disables individual collectors.
type CollectorsConfig struct {
	OpenWrt bool `yaml:"openwrt"`
	ARP     bool `yaml:"arp"`
	NDP     bool `yaml:"ndp"`
	DHCP    bool `yaml:"dhcp"`
	FDB     bool `yaml:"fdb"`
	LLDP    bool `yaml:"lldp"`
	MDNS    bool `yaml:"mdns"`
	SSDP    bool `yaml:"ssdp"`
	SNMP    bool `yaml:"snmp"`
	Scan    bool `yaml:"scan"`
}

// MonitoringConfig controls the probe engine.
type MonitoringConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Interval         string `yaml:"interval"`
	Timeout          string `yaml:"timeout"`
	FailureThreshold int    `yaml:"failure_threshold"`
	RecoverConfirm   int    `yaml:"recover_confirm"`
	// InterfaceRefresh controls how often interface state is polled.
	InterfaceRefresh string `yaml:"interface_refresh"`
	// AutoProbe creates an ICMP probe for every newly discovered device that has
	// an address. Without it a discovered device stays "unknown" forever, because
	// nothing else creates probes.
	AutoProbe bool `yaml:"auto_probe"`
	// DeviceRefresh controls how often device reachability is polled.
	DeviceRefresh string `yaml:"device_refresh"`
}

// TopologyConfig controls the topology engine.
type TopologyConfig struct {
	AutoLayout           bool `yaml:"auto_layout"`
	PreserveManualLayout bool `yaml:"preserve_manual_layout"`
	PreferConfirmedEdges bool `yaml:"prefer_confirmed_edges"`
	// UnverifiedEdgeThreshold: edges below this confidence are drawn dashed.
	UnverifiedEdgeThreshold float64 `yaml:"unverified_edge_threshold"`
}

// AuthConfig controls access to the web UI and API.
//
// Lanory exposes sensitive network information and can trigger scans, so it
// must not be reachable unauthenticated. By default it binds to the LAN only.
type AuthConfig struct {
	// Enabled turns on token authentication. Defaults to true.
	Enabled bool `yaml:"enabled"`
	// Token is the bearer token. When empty and Enabled is true, a random
	// token is generated at startup and printed to the log once.
	Token string `yaml:"token"`
	// TokenFile optionally holds the token on disk (mode 0600).
	TokenFile string `yaml:"token_file"`
	// AllowLAN exempts requests from RFC1918 addresses from authentication.
	AllowLAN bool `yaml:"allow_lan"`
}

// LoggingConfig controls logging.
type LoggingConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
	// Pretty enables human readable (non-JSON) console logging.
	Pretty bool `yaml:"pretty"`
}

// DemoConfig controls demo mode.
type DemoConfig struct {
	// Enabled makes the app serve the built-in sample network instead of
	// touching the real system. The UI must label this as simulated data.
	Enabled bool `yaml:"enabled"`
	// Seed selects which sample dataset to load.
	Seed string `yaml:"seed"`
}

// Default returns the built-in defaults.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host:           "0.0.0.0",
			Port:           8787,
			BasePath:       "",
			WebDir:         "",
			TrustedProxies: []string{},
		},
		Database: DatabaseConfig{
			Path:          "/var/lib/lanory/lanory.db",
			RetentionDays: 30,
		},
		Discovery: DiscoveryConfig{
			Enabled:           true,
			Interval:          "10m",
			AutoDetectSubnets: true,
			MaxConcurrency:    16,
			Subnets:           []string{},
			ScanTimeout:       "500ms",
			RateLimit:         64,
		},
		Collectors: CollectorsConfig{
			OpenWrt: true,
			ARP:     true,
			NDP:     true,
			DHCP:    true,
			FDB:     true,
			LLDP:    false,
			MDNS:    false,
			SSDP:    false,
			SNMP:    false,
			Scan:    false,
		},
		Monitoring: MonitoringConfig{
			Enabled:          true,
			Interval:         "60s",
			Timeout:          "3s",
			FailureThreshold: 3,
			RecoverConfirm:   1,
			InterfaceRefresh: "30s",
			DeviceRefresh:    "60s",
		},
		Topology: TopologyConfig{
			AutoLayout:              true,
			PreserveManualLayout:    true,
			PreferConfirmedEdges:    true,
			UnverifiedEdgeThreshold: 0.6,
		},
		Auth: AuthConfig{
			Enabled:   true,
			Token:     "",
			TokenFile: "",
			AllowLAN:  false,
		},
		Logging: LoggingConfig{
			Level:  "info",
			File:   "",
			Pretty: true,
		},
		Demo: DemoConfig{
			Enabled: false,
			Seed:    "easepi-r1",
		},
	}
}

// DefaultPaths lists the config file locations searched in order.
func DefaultPaths() []string {
	return []string{
		"/etc/lanory/config.yaml",
		"/etc/lanory/lanory.yaml",
		"./config.yaml",
		"./lanory.yaml",
	}
}

// Load reads the configuration from the first existing file in paths (or
// DefaultPaths when paths is empty), then applies environment overrides.
func Load(paths ...string) (*Config, error) {
	cfg := Default()

	search := paths
	if len(search) == 0 {
		search = DefaultPaths()
	}
	loaded := false
	for _, p := range search {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("config: read %s: %w", p, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", p, err)
		}
		loaded = true
		break
	}
	_ = loaded

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// envPrefix is the prefix for all Lanory environment overrides.
const envPrefix = "LANORY_"

// applyEnv overrides configuration values from LANORY_* environment variables.
func applyEnv(cfg *Config) error {
	env := os.Getenv

	cfg.Server.Host = str(env(envPrefix+"SERVER_HOST"), cfg.Server.Host)
	if p := env(envPrefix + "SERVER_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("config: LANORY_SERVER_PORT: %w", err)
		}
		cfg.Server.Port = n
	}
	cfg.Server.WebDir = str(env(envPrefix+"SERVER_WEB_DIR"), cfg.Server.WebDir)
	cfg.Server.BasePath = str(env(envPrefix+"SERVER_BASE_PATH"), cfg.Server.BasePath)

	cfg.Database.Path = str(env(envPrefix+"DATABASE_PATH"), cfg.Database.Path)

	cfg.Discovery.Enabled = boolean(env(envPrefix+"DISCOVERY_ENABLED"), cfg.Discovery.Enabled)
	cfg.Discovery.Interval = str(env(envPrefix+"DISCOVERY_INTERVAL"), cfg.Discovery.Interval)
	cfg.Discovery.AutoDetectSubnets = boolean(env(envPrefix+"DISCOVERY_AUTO_DETECT_SUBNETS"), cfg.Discovery.AutoDetectSubnets)
	cfg.Discovery.MaxConcurrency = integer(env(envPrefix+"DISCOVERY_MAX_CONCURRENCY"), cfg.Discovery.MaxConcurrency)

	cfg.Monitoring.Enabled = boolean(env(envPrefix+"MONITORING_ENABLED"), cfg.Monitoring.Enabled)
	cfg.Monitoring.Interval = str(env(envPrefix+"MONITORING_INTERVAL"), cfg.Monitoring.Interval)

	cfg.Logging.Level = str(env(envPrefix+"LOG_LEVEL"), cfg.Logging.Level)
	cfg.Logging.File = str(env(envPrefix+"LOG_FILE"), cfg.Logging.File)

	cfg.Demo.Enabled = boolean(env(envPrefix+"DEMO_MODE"), cfg.Demo.Enabled)
	cfg.Auth.Enabled = boolean(env(envPrefix+"AUTH_ENABLED"), cfg.Auth.Enabled)
	cfg.Auth.Token = str(env(envPrefix+"AUTH_TOKEN"), cfg.Auth.Token)

	return nil
}

func str(v, def string) string {
	if v == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func boolean(v string, def bool) bool {
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(v)))
	if err != nil {
		return def
	}
	return b
}

func integer(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// Validate checks the configuration for obvious mistakes.
func (c *Config) Validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("config: invalid server port %d", c.Server.Port)
	}
	if c.Server.Host == "" {
		return fmt.Errorf("config: server host must not be empty")
	}
	if c.Database.Path == "" {
		return fmt.Errorf("config: database path must not be empty")
	}
	if _, err := c.DiscoveryInterval(); err != nil {
		return err
	}
	if _, err := c.MonitoringInterval(); err != nil {
		return err
	}
	if c.Discovery.MaxConcurrency < 1 {
		return fmt.Errorf("config: discovery.max_concurrency must be >= 1")
	}
	if c.Discovery.MaxConcurrency > 512 {
		return fmt.Errorf("config: discovery.max_concurrency must be <= 512")
	}
	if c.Monitoring.FailureThreshold < 1 {
		return fmt.Errorf("config: monitoring.failure_threshold must be >= 1")
	}
	return nil
}

// Addr returns the listen address.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port) }

// DiscoveryInterval returns the discovery interval as a duration.
func (c *Config) DiscoveryInterval() (time.Duration, error) {
	return parseDuration(c.Discovery.Interval, 10*time.Minute)
}

// MonitoringInterval returns the device probe interval.
func (c *Config) MonitoringInterval() (time.Duration, error) {
	return parseDuration(c.Monitoring.Interval, 60*time.Second)
}

// MonitoringTimeout returns the per-probe timeout.
func (c *Config) MonitoringTimeout() (time.Duration, error) {
	return parseDuration(c.Monitoring.Timeout, 3*time.Second)
}

// InterfaceRefreshInterval returns the interface polling interval.
func (c *Config) InterfaceRefreshInterval() (time.Duration, error) {
	return parseDuration(c.Monitoring.InterfaceRefresh, 30*time.Second)
}

// ScanTimeoutDuration returns the active scan per-host timeout.
func (c *Config) ScanTimeoutDuration() (time.Duration, error) {
	return parseDuration(c.Discovery.ScanTimeout, 500*time.Millisecond)
}

func parseDuration(v string, def time.Duration) (time.Duration, error) {
	if strings.TrimSpace(v) == "" {
		return def, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("config: invalid duration %q: %w", v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("config: duration %q must be positive", v)
	}
	return d, nil
}

// Write persists the configuration as YAML to path.
func (c *Config) Write(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
