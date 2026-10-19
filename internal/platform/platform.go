// Package platform detects the host Lanory runs on and derives the filesystem
// paths and collector capabilities that follow from it.
//
// Lanory targets OpenWrt / iStoreOS routers, but it is developed on macOS and
// must also run on a generic Linux box. Every capability below is therefore
// reported per platform so the rest of the program can degrade gracefully
// instead of failing: a macOS workstation simply has fewer collectors than an
// OpenWrt router, which is a fact to display, not an error to raise.
//
// Nothing in this package touches the network or executes a command. It only
// reads well known release files and environment variables.
package platform

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Kind is the detected host family.
type Kind string

const (
	// KindOpenWrt is an OpenWrt or iStoreOS router. Full network introspection.
	KindOpenWrt Kind = "openwrt"
	// KindLinux is a generic Linux host. Partial introspection.
	KindLinux Kind = "linux"
	// KindDarwin is macOS, the development platform.
	KindDarwin Kind = "darwin"
	// KindWindows is Windows.
	KindWindows Kind = "windows"
	// KindUnknown is anything else (BSD, Solaris, ...).
	KindUnknown Kind = "unknown"
)

// Default release file locations, in priority order.
const (
	// OpenWrtReleaseFile is the OpenWrt release information file.
	OpenWrtReleaseFile = "/etc/openwrt_release"
	// OSReleaseFile is the freedesktop.org release information file.
	OSReleaseFile = "/etc/os-release"
)

// Default directory names used when no environment override is present.
const (
	appDirName = "lanory"
	// DirConfig is the configuration subdirectory on Linux/OpenWrt.
	DirConfig = "/etc/lanory"
	// DirData is the persistent data directory on Linux/OpenWrt.
	DirData = "/var/lib/lanory"
)

// Environment variables that override the detected directories.
const (
	EnvConfigDir = "LANORY_CONFIG_DIR"
	EnvDataDir   = "LANORY_DATA_DIR"
)

// Paths holds the directories Lanory reads and writes.
type Paths struct {
	// ConfigDir holds config.yaml.
	ConfigDir string
	// DataDir holds the SQLite database and other mutable state.
	DataDir string
	// ConfigFile is the preferred configuration file path.
	ConfigFile string
	// DatabaseFile is the default SQLite database path.
	DatabaseFile string
}

// CollectorCapability reports whether a collector is expected to work here.
//
// Supported is a statement about the platform, not about the configuration: a
// supported collector can still be disabled in config.yaml.
type CollectorCapability struct {
	// Name is the stable collector identifier, e.g. "bridge_fdb".
	Name string
	// Supported reports whether the platform can provide this collector.
	Supported bool
	// Reason explains an unsupported collector in human readable form.
	Reason string `json:"reason,omitempty"`
}

// Platform is the result of host detection.
type Platform struct {
	// Kind is the detected host family.
	Kind Kind
	// OS is runtime.GOOS.
	OS string
	// Arch is runtime.GOARCH.
	Arch string
	// OpenWrtVersion is DISTRIB_RELEASE from /etc/openwrt_release.
	OpenWrtVersion string
	// OpenWrtTarget is DISTRIB_TARGET from /etc/openwrt_release.
	OpenWrtTarget string
	// OpenWrtBoard is DISTRIB_BOARD from /etc/openwrt_release, when present.
	OpenWrtBoard string
	// OSName is PRETTY_NAME from /etc/os-release on generic Linux.
	OSName string
	// Paths are the directories derived from the host family.
	Paths Paths
	// Collectors lists the capability of every known collector.
	Collectors []CollectorCapability
}

// IsOpenWrt reports whether the host is an OpenWrt / iStoreOS router.
func (p Platform) IsOpenWrt() bool { return p.Kind == KindOpenWrt }

// IsRouter reports whether the host is expected to be the network gateway.
func (p Platform) IsRouter() bool { return p.Kind == KindOpenWrt || p.Kind == KindLinux }

// Supports reports whether the named collector is expected to work here.
func (p Platform) Supports(name string) bool {
	for _, c := range p.Collectors {
		if c.Name == name {
			return c.Supported
		}
	}
	return false
}

// CollectorNames returns the names of the collectors expected to work.
func (p Platform) CollectorNames() []string {
	out := make([]string, 0, len(p.Collectors))
	for _, c := range p.Collectors {
		if c.Supported {
			out = append(out, c.Name)
		}
	}
	return out
}

// Describe renders a one line human readable summary.
func (p Platform) Describe() string {
	if p.IsOpenWrt() {
		return fmt.Sprintf("OpenWrt %s (%s, %s/%s)", p.OpenWrtVersion, p.OpenWrtTarget, p.OS, p.Arch)
	}
	if p.OSName != "" {
		return fmt.Sprintf("%s (%s/%s)", p.OSName, p.OS, p.Arch)
	}
	return fmt.Sprintf("%s (%s/%s)", p.Kind, p.OS, p.Arch)
}

// Test seams. They are variables so tests can inject a fake host without
// touching the real filesystem.
var (
	readOpenWrtRelease = func() (map[string]string, error) {
		return readReleaseFile(OpenWrtReleaseFile)
	}
	readOSRelease = func() (map[string]string, error) {
		return readReleaseFile(OSReleaseFile)
	}
	userHomeDir = os.UserHomeDir
	goos        = runtime.GOOS
	goarch      = runtime.GOARCH
)

// Detect inspects the running host and returns its Platform.
//
// Detect never fails: an unreadable or missing release file simply yields a
// less capable platform.
func Detect() Platform {
	openwrt, _ := readOpenWrtRelease()
	osRelease, _ := readOSRelease()
	return detect(openwrt, osRelease)
}

// detect is the pure core of Detect, split out so tests can feed it synthetic
// release files.
func detect(openwrt, osRelease map[string]string) Platform {
	p := Platform{
		OS:   goos,
		Arch: goarch,
	}
	p.Kind = kindFrom(openwrt, osRelease)

	if id := openwrt["DISTRIB_ID"]; id != "" {
		p.OpenWrtVersion = openwrt["DISTRIB_RELEASE"]
		p.OpenWrtTarget = openwrt["DISTRIB_TARGET"]
		p.OpenWrtBoard = openwrt["DISTRIB_BOARD"]
	}
	if p.OSName == "" {
		p.OSName = osRelease["PRETTY_NAME"]
	}

	p.Paths = paths(p.Kind)
	p.Collectors = capabilities(p.Kind)
	return p
}

// kindFrom maps the detected release files onto a Kind.
func kindFrom(openwrt, osRelease map[string]string) Kind {
	if id := strings.ToLower(openwrt["DISTRIB_ID"]); strings.Contains(id, "openwrt") {
		return KindOpenWrt
	}
	// iStoreOS ships a release file with DISTRIB_ID="iStoreOS" but keeps the
	// OpenWrt layout, so treat any OpenWrt release file as OpenWrt.
	if len(openwrt) > 0 {
		return KindOpenWrt
	}
	switch goos {
	case "linux":
		return KindLinux
	case "darwin":
		return KindDarwin
	case "windows":
		return KindWindows
	default:
		return KindUnknown
	}
}

// paths derives the configuration and data directories for a host family.
func paths(k Kind) Paths {
	var configDir, dataDir string
	switch k {
	case KindOpenWrt, KindLinux:
		configDir, dataDir = DirConfig, DirData
	case KindDarwin:
		configDir, dataDir = filepath.Join(homeOrDot(), ".config", appDirName),
			filepath.Join(homeOrDot(), ".local", "share", appDirName)
	case KindWindows:
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(homeOrDot(), "AppData", "Roaming")
		}
		configDir = filepath.Join(base, appDirName)
		dataDir = filepath.Join(os.Getenv("LOCALAPPDATA"), appDirName)
		if os.Getenv("LOCALAPPDATA") == "" {
			dataDir = filepath.Join(homeOrDot(), "AppData", "Local", appDirName)
		}
	default:
		configDir, dataDir = filepath.Join(homeOrDot(), "."+appDirName),
			filepath.Join(homeOrDot(), "."+appDirName, "data")
	}

	// Explicit overrides always win so a developer can run the real binary
	// against a scratch directory.
	if v := os.Getenv(EnvConfigDir); v != "" {
		configDir = v
	}
	if v := os.Getenv(EnvDataDir); v != "" {
		dataDir = v
	}

	return Paths{
		ConfigDir:    configDir,
		DataDir:      dataDir,
		ConfigFile:   filepath.Join(configDir, "config.yaml"),
		DatabaseFile: filepath.Join(dataDir, "lanory.db"),
	}
}

// homeOrDot returns the user home directory, or "." when it cannot be
// determined (containers, service accounts).
func homeOrDot() string {
	if h, err := userHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// capabilities reports which collectors are expected to work on a host family.
//
// The list is deliberately conservative: claiming a collector works when it
// does not would surface as a confusing empty result in the UI.
func capabilities(k Kind) []CollectorCapability {
	type entry struct {
		name string
		ok   bool
		why  string
	}
	var entries []entry

	switch k {
	case KindOpenWrt:
		entries = []entry{
			{"openwrt", true, ""},
			{"interfaces", true, ""},
			{"arp", true, ""},
			{"ndp", true, ""},
			{"dhcp", true, ""},
			{"bridge_fdb", true, ""},
			{"lldp", false, "optional package lldpd is not installed by default"},
			{"mdns", false, "optional package avahi-daemon is not installed by default"},
			{"ssdp", false, "optional package is not installed by default"},
			{"scan", true, ""},
		}
	case KindLinux:
		entries = []entry{
			{"openwrt", false, "not an OpenWrt host"},
			{"interfaces", true, ""},
			{"arp", true, ""},
			{"ndp", true, ""},
			{"dhcp", false, "no dnsmasq/odhcpd lease file found"},
			{"bridge_fdb", true, ""},
			{"lldp", false, "requires lldpd"},
			{"mdns", false, "requires avahi-daemon"},
			{"ssdp", false, "requires a UPnP listener"},
			{"scan", true, ""},
		}
	case KindDarwin, KindWindows, KindUnknown:
		why := "requires Linux or OpenWrt network introspection"
		entries = []entry{
			{"openwrt", false, "not an OpenWrt host"},
			{"interfaces", false, why},
			{"arp", false, why},
			{"ndp", false, why},
			{"dhcp", false, why},
			{"bridge_fdb", false, why},
			{"lldp", false, why},
			{"mdns", false, why},
			{"ssdp", false, why},
			{"scan", false, why},
			{"manual", true, ""},
		}
	}

	out := make([]CollectorCapability, 0, len(entries))
	for _, e := range entries {
		out = append(out, CollectorCapability{Name: e.name, Supported: e.ok, Reason: e.why})
	}
	return out
}

// readReleaseFile parses a shell style KEY="value" release file.
func readReleaseFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			out[key] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
