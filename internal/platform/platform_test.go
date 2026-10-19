package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Platform detection tests.
//
// They exercise detect() directly with synthetic release files, so the
// assertions do not depend on the host the suite runs on, and they restore the
// package level seams afterwards.

// withSeams installs fake release readers and restores the originals.
func withSeams(t *testing.T, openwrt, osRelease map[string]string, goosValue string) {
	t.Helper()
	origOpenWrt, origOS, origGOOS := readOpenWrtRelease, readOSRelease, goos
	t.Cleanup(func() {
		readOpenWrtRelease, readOSRelease, goos = origOpenWrt, origOS, origGOOS
	})
	readOpenWrtRelease = func() (map[string]string, error) { return openwrt, nil }
	readOSRelease = func() (map[string]string, error) { return osRelease, nil }
	goos = goosValue
}

// TestDetectOpenWrt checks the router case, which is the primary target.
func TestDetectOpenWrt(t *testing.T) {
	withSeams(t, map[string]string{
		"DISTRIB_ID":      "OpenWrt",
		"DISTRIB_RELEASE": "25.12.0",
		"DISTRIB_TARGET":  "armsr/armv7",
		"DISTRIB_BOARD":   "easepi-r1",
	}, map[string]string{}, "linux")

	p := Detect()
	if p.Kind != KindOpenWrt {
		t.Fatalf("kind = %q, want openwrt", p.Kind)
	}
	if !p.IsOpenWrt() {
		t.Error("IsOpenWrt must be true")
	}
	if !p.IsRouter() {
		t.Error("OpenWrt must be reported as a router")
	}
	if p.OpenWrtVersion != "25.12.0" {
		t.Errorf("openwrt version = %q", p.OpenWrtVersion)
	}
	if p.OpenWrtTarget != "armsr/armv7" {
		t.Errorf("openwrt target = %q", p.OpenWrtTarget)
	}
	if p.Paths.ConfigDir != DirConfig {
		t.Errorf("config dir = %q, want %q", p.Paths.ConfigDir, DirConfig)
	}
	if p.Paths.DataDir != DirData {
		t.Errorf("data dir = %q, want %q", p.Paths.DataDir, DirData)
	}
	if p.Paths.ConfigFile != filepath.Join(DirConfig, "config.yaml") {
		t.Errorf("config file = %q", p.Paths.ConfigFile)
	}
	if p.Paths.DatabaseFile != filepath.Join(DirData, "lanory.db") {
		t.Errorf("database file = %q", p.Paths.DatabaseFile)
	}
	if !strings.Contains(p.Describe(), "OpenWrt") {
		t.Errorf("Describe = %q, want it to mention OpenWrt", p.Describe())
	}

	// Every collector that needs the router must be supported.
	for _, name := range []string{"openwrt", "interfaces", "arp", "ndp", "dhcp", "bridge_fdb", "scan"} {
		if !p.Supports(name) {
			t.Errorf("collector %q should be supported on OpenWrt", name)
		}
	}
	// Optional packages are not installed by default.
	for _, name := range []string{"lldp", "mdns"} {
		if p.Supports(name) {
			t.Errorf("collector %q is not available by default on OpenWrt", name)
		}
	}
}

// TestDetectIStretchOS checks that a release file without an OpenWrt id is
// still treated as OpenWrt layout.
func TestDetectIStoreOS(t *testing.T) {
	// iStoreOS ships DISTRIB_ID="iStoreOS" but keeps the OpenWrt layout.
	withSeams(t, map[string]string{"DISTRIB_ID": "iStoreOS", "DISTRIB_RELEASE": "25.12.5"}, nil, "linux")

	p := Detect()
	if p.Kind != KindOpenWrt {
		t.Fatalf("kind = %q, want openwrt", p.Kind)
	}
}

// TestDetectGenericLinux checks the generic Linux case.
func TestDetectGenericLinux(t *testing.T) {
	withSeams(t, nil, map[string]string{"PRETTY_NAME": "Debian GNU/Linux 13 (trixie)"}, "linux")

	p := Detect()
	if p.Kind != KindLinux {
		t.Fatalf("kind = %q, want linux", p.Kind)
	}
	if p.IsOpenWrt() {
		t.Error("a generic Linux host is not OpenWrt")
	}
	if !p.IsRouter() {
		t.Error("a generic Linux host is still expected to run as the router")
	}
	if p.OSName != "Debian GNU/Linux 13 (trixie)" {
		t.Errorf("os name = %q", p.OSName)
	}
	if !strings.Contains(p.Describe(), "Debian") {
		t.Errorf("Describe = %q", p.Describe())
	}
	// Interface and neighbour introspection work, DHCP leases usually do not.
	if !p.Supports("interfaces") || !p.Supports("arp") || !p.Supports("bridge_fdb") {
		t.Error("interfaces, arp and bridge_fdb must be supported on Linux")
	}
	if p.Supports("openwrt") {
		t.Error("the openwrt collector is not supported on a generic Linux host")
	}
	if p.Supports("dhcp") {
		t.Error("a generic Linux host has no dnsmasq lease file")
	}
}

// TestDetectDarwin checks the development platform degrades gracefully.
func TestDetectDarwin(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvDataDir, "")
	withSeams(t, nil, map[string]string{"PRETTY_NAME": "macOS"}, "darwin")

	p := Detect()
	if p.Kind != KindDarwin {
		t.Fatalf("kind = %q, want darwin", p.Kind)
	}
	if p.IsRouter() {
		t.Error("macOS must not be reported as a router")
	}
	if !strings.Contains(p.Describe(), "darwin") {
		t.Errorf("Describe = %q, want it to mention darwin", p.Describe())
	}

	// No network collector works, so the UI shows an honest empty state
	// instead of a list of collectors that will all fail.
	names := p.CollectorNames()
	if len(names) != 1 || names[0] != "manual" {
		t.Errorf("collector names = %v, want [manual]", names)
	}
	for _, c := range p.Collectors {
		if c.Supported && c.Name != "manual" {
			t.Errorf("collector %q claims support on macOS", c.Name)
		}
		if !c.Supported && c.Reason == "" {
			t.Errorf("unsupported collector %q must explain why", c.Name)
		}
	}
}

// TestDetectWindows checks the Windows case.
func TestDetectWindows(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvDataDir, "")
	withSeams(t, nil, nil, "windows")

	p := Detect()
	if p.Kind != KindWindows {
		t.Fatalf("kind = %q, want windows", p.Kind)
	}
	if p.Supports("interfaces") {
		t.Error("interface introspection is Linux-only")
	}
}

// TestDetectUnknownOS checks the fallback for an unrecognised GOOS.
func TestDetectUnknownOS(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvDataDir, "")
	withSeams(t, nil, nil, "freebsd")

	p := Detect()
	if p.Kind != KindUnknown {
		t.Fatalf("kind = %q, want unknown", p.Kind)
	}
}

// TestDetectNoReleaseFiles checks that detection never fails.
func TestDetectNoReleaseFiles(t *testing.T) {
	withSeams(t, nil, nil, "linux")
	p := Detect()
	if p.Kind != KindLinux {
		t.Errorf("kind = %q, want linux", p.Kind)
	}
	if p.OSName != "" {
		t.Errorf("os name = %q, want empty when /etc/os-release is absent", p.OSName)
	}
}

// TestPathOverrides checks that the environment overrides win.
func TestPathOverrides(t *testing.T) {
	withSeams(t, map[string]string{"DISTRIB_ID": "OpenWrt"}, nil, "linux")

	dir := t.TempDir()
	t.Setenv(EnvConfigDir, filepath.Join(dir, "etc"))
	t.Setenv(EnvDataDir, filepath.Join(dir, "var"))

	p := Detect()
	if p.Paths.ConfigDir != filepath.Join(dir, "etc") {
		t.Errorf("config dir = %q, want the override", p.Paths.ConfigDir)
	}
	if p.Paths.DataDir != filepath.Join(dir, "var") {
		t.Errorf("data dir = %q, want the override", p.Paths.DataDir)
	}
	if p.Paths.DatabaseFile != filepath.Join(dir, "var", "lanory.db") {
		t.Errorf("database file = %q", p.Paths.DatabaseFile)
	}
}

// TestReadReleaseFile checks the shell-style release file parser.
func TestReadReleaseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "os-release")
	content := `# a comment
PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
VERSION_ID="13"

NAME='Debian GNU/Linux"
BROKEN LINE WITHOUT EQUALS
=
EMPTY=
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := readReleaseFile(path)
	if err != nil {
		t.Fatalf("readReleaseFile: %v", err)
	}
	if got["PRETTY_NAME"] != "Debian GNU/Linux 13 (trixie)" {
		t.Errorf("PRETTY_NAME = %q", got["PRETTY_NAME"])
	}
	if got["NAME"] != "Debian GNU/Linux" {
		t.Errorf("NAME = %q (single quotes must be stripped)", got["NAME"])
	}
	if got["VERSION_ID"] != "13" {
		t.Errorf("VERSION_ID = %q", got["VERSION_ID"])
	}
	if _, ok := got[""]; ok {
		t.Error("an empty key must be skipped")
	}
	if got["EMPTY"] != "" {
		t.Errorf("EMPTY = %q, want empty", got["EMPTY"])
	}
}

// TestReadReleaseFileMissing checks that a missing file is an error.
func TestReadReleaseFileMissing(t *testing.T) {
	if _, err := readReleaseFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("reading a missing release file must fail")
	}
}

// TestDetectRealHost checks Detect() against the machine running the tests,
// which must produce a usable platform without panicking.
func TestDetectRealHost(t *testing.T) {
	p := Detect()
	if p.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", p.OS, runtime.GOOS)
	}
	if p.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", p.Arch, runtime.GOARCH)
	}
	switch p.Kind {
	case KindOpenWrt, KindLinux, KindDarwin, KindWindows, KindUnknown:
	default:
		t.Fatalf("kind = %q is not a known kind", p.Kind)
	}
	// Paths must always be absolute, whatever the host.
	for _, dir := range []string{p.Paths.ConfigDir, p.Paths.DataDir} {
		if !filepath.IsAbs(dir) {
			t.Errorf("directory %q must be absolute", dir)
		}
	}
	if len(p.Collectors) == 0 {
		t.Error("every platform must report a collector list, even if all unsupported")
	}
}

// TestCollectorNamesAreStable guards against renaming a collector, which would
// break the API surface and the configuration keys.
func TestCollectorNamesAreStable(t *testing.T) {
	want := []string{
		"openwrt", "interfaces", "arp", "ndp", "dhcp",
		"bridge_fdb", "lldp", "mdns", "ssdp", "scan",
	}
	for _, kind := range []Kind{KindOpenWrt, KindLinux, KindDarwin} {
		t.Setenv(EnvConfigDir, "")
		t.Setenv(EnvDataDir, "")
		goosValue := "linux"
		if kind == KindDarwin {
			goosValue = "darwin"
		}
		openwrt := map[string]string{"DISTRIB_ID": "OpenWrt"}
		if kind != KindOpenWrt {
			openwrt = nil
		}
		withSeams(t, openwrt, nil, goosValue)

		got := make(map[string]bool)
		for _, c := range Detect().Collectors {
			got[c.Name] = true
		}
		for _, name := range want {
			if !got[name] {
				t.Errorf("%s: collector %q is missing from the capability list", kind, name)
			}
		}
	}
}
