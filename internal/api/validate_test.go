package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

// Input validation tests.
//
// The point of these tests is not that a helper exists, but that malformed and
// ambiguous input is rejected with an actionable message. Every rejected case
// asserts both the failure and a fragment of the message, because "invalid
// input" alone tells an API client nothing.

// TestParseIPv4 covers IPv4 validation.
func TestParseIPv4(t *testing.T) {
	valid := []string{"192.168.100.1", "0.0.0.0", "255.255.255.255", "10.0.0.1", " 192.168.1.4 "}
	for _, in := range valid {
		addr, err := ParseIPv4(in)
		if err != nil {
			t.Errorf("ParseIPv4(%q) rejected a valid address: %v", in, err)
			continue
		}
		if !addr.Is4() {
			t.Errorf("ParseIPv4(%q) returned a non-IPv4 address", in)
		}
	}

	invalid := []struct {
		in      string
		wantMsg string
	}{
		{"192.168.1.999", "not a valid address"},
		{"192.168.001.001", "leading zero"},
		{"010.1.1.1", "leading zero"},
		{"192.168.1", "not a valid address"},
		{"192.168.1.1.1", "not a valid address"},
		{"::1", "not an IPv4 address"},
		{"2001:db8::1", "not an IPv4 address"},
		// A zone is refused outright, whichever family the address is.
		{"::1%eth0", "zone suffix"},
		{"", "not a valid address"},
		{"not-an-ip", "not a valid address"},
		{"192.168.1.1/24", "not a valid address"},
	}
	for _, tc := range invalid {
		_, err := ParseIPv4(tc.in)
		if err == nil {
			t.Errorf("ParseIPv4(%q) accepted an invalid address", tc.in)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseIPv4(%q) error is not ErrInvalid: %v", tc.in, err)
		}
		if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("ParseIPv4(%q) error = %q, want it to mention %q", tc.in, err, tc.wantMsg)
		}
	}
}

// TestParseIPv6 covers IPv6 validation.
func TestParseIPv6(t *testing.T) {
	valid := []string{"::1", "fe80::1", "2001:db8::1", "fd00::1"}
	for _, in := range valid {
		if _, err := ParseIPv6(in); err != nil {
			t.Errorf("ParseIPv6(%q) rejected a valid address: %v", in, err)
		}
	}

	invalid := []struct {
		in      string
		wantMsg string
	}{
		// A zone identifies a local interface and must never be stored.
		{"::1%eth0", "zone suffix"},
		{"fe80::1%eth0", "zone suffix"},
		{"192.168.1.1", "not an IPv6 address"},
		{"2001:db8::zzzz", "not a valid address"},
		{"2001:db8::1::2", "not a valid address"},
		{"", "not a valid address"},
	}
	for _, tc := range invalid {
		_, err := ParseIPv6(tc.in)
		if err == nil {
			t.Errorf("ParseIPv6(%q) accepted an invalid address", tc.in)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("ParseIPv6(%q) error = %q, want it to mention %q", tc.in, err, tc.wantMsg)
		}
	}
}

// TestParseCIDR covers CIDR validation.
func TestParseCIDR(t *testing.T) {
	valid := map[string]string{
		"192.168.100.0/24": "192.168.100.0/24",
		"10.0.0.0/8":       "10.0.0.0/8",
		"0.0.0.0/0":        "0.0.0.0/0",
		"fd00::/64":        "fd00::/64",
	}
	for in, want := range valid {
		p, err := ParseCIDR(in)
		if err != nil {
			t.Errorf("ParseCIDR(%q) rejected a valid network: %v", in, err)
			continue
		}
		if p.String() != want {
			t.Errorf("ParseCIDR(%q) = %s, want %s", in, p, want)
		}
	}

	invalid := []struct {
		in      string
		wantMsg string
	}{
		{"192.168.1.0/33", "not a valid network"},
		{"192.168.1.0/-1", "not a valid network"},
		{"192.168.1.0/", "not a valid network"},
		{"192.168.1.0", "not a valid network"},
		{"not-a-network/24", "not a valid network"},
		{"", "not a valid network"},
	}
	for _, tc := range invalid {
		if _, err := ParseCIDR(tc.in); err == nil {
			t.Errorf("ParseCIDR(%q) accepted an invalid network", tc.in)
		} else if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("ParseCIDR(%q) error = %q, want %q", tc.in, err, tc.wantMsg)
		}
	}
}

// TestNormalizeMAC covers MAC validation.
func TestNormalizeMAC(t *testing.T) {
	// Every common spelling of the Windows PC's MAC from the fixture must
	// canonicalise to the same value.
	const want = "b082e2613eb7"
	for _, in := range []string{
		"b0:82:e2:61:3e:b7",
		"B0:82:E2:61:3E:B7",
		"b0-82-e2-61-3e-b7",
		"b082.e261.3eb7",
		"b082e2613eb7",
		" b0:82:e2:61:3e:b7 ",
	} {
		got, err := NormalizeMAC(in)
		if err != nil {
			t.Errorf("NormalizeMAC(%q) rejected a valid MAC: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}

	invalid := []struct {
		in      string
		wantMsg string
	}{
		{"", "must not be empty"},
		{"zz:bb:cc:dd:ee:ff", "non-hex"},
		{"aa:bb:cc:dd:ee", "12 hex digits"},
		{"aa:bb:cc:dd:ee:ff:00", "12 hex digits"},
		{"aa:bb:cc:dd:ee:gg", "non-hex"},
		{"aa:bb:cc:dd:ee:ff/24", "non-hex"},
		{"hello world", "non-hex"},
	}
	for _, tc := range invalid {
		if _, err := NormalizeMAC(tc.in); err == nil {
			t.Errorf("NormalizeMAC(%q) accepted an invalid MAC", tc.in)
		} else if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("NormalizeMAC(%q) error = %q, want %q", tc.in, err, tc.wantMsg)
		}
	}
}

// TestFormatMAC checks the display form round trip.
func TestFormatMAC(t *testing.T) {
	if got := FormatMAC("b082e2613eb7"); got != "b0:82:e2:61:3e:b7" {
		t.Errorf("FormatMAC = %q", got)
	}
	// A value that is not canonical is returned unchanged rather than mangled.
	if got := FormatMAC("short"); got != "short" {
		t.Errorf("FormatMAC(short) = %q", got)
	}
}

// TestValidateHostname covers hostname validation.
func TestValidateHostname(t *testing.T) {
	valid := map[string]string{
		"router":                "router",
		"my-router":             "my-router",
		"router.lan":            "router.lan",
		"Router.LAN":            "router.lan",
		"a.b.c.d":               "a.b.c.d",
		"esp32-sensor":          "esp32-sensor",
		"WindowsPC":             "windowspc",
		"host-1.example.com.":   "host-1.example.com",
		strings.Repeat("a", 63): strings.Repeat("a", 63),
	}
	for in, want := range valid {
		got, err := ValidateHostname(in)
		if err != nil {
			t.Errorf("ValidateHostname(%q) rejected a valid hostname: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidateHostname(%q) = %q, want %q", in, got, want)
		}
	}

	invalid := []struct {
		in      string
		wantMsg string
	}{
		{"", "must not be empty"},
		{"-router", "hyphen"},
		{"router-", "hyphen"},
		{"router..lan", "empty label"},
		{"router lan", "illegal character"},
		{"router:80", "illegal characters"},
		{"http://router", "illegal characters"},
		{"router/path", "illegal characters"},
		{"router%eth0", "illegal characters"},
		{"router@host", "illegal characters"},
		{strings.Repeat("a", 64), "longer than 63"},
		{strings.Repeat("a.", 130) + "a", "longer than"},
	}
	for _, tc := range invalid {
		if _, err := ValidateHostname(tc.in); err == nil {
			t.Errorf("ValidateHostname(%q) accepted an invalid hostname", tc.in)
		} else if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("ValidateHostname(%q) error = %q, want %q", tc.in, err, tc.wantMsg)
		}
	}
}

// TestParseHTTPURL covers URL validation.
func TestParseHTTPURL(t *testing.T) {
	for _, in := range []string{
		"http://192.168.100.1/",
		"https://router.lan/status",
		"http://192.168.100.1:8080/admin",
	} {
		if _, err := ParseHTTPURL(in); err != nil {
			t.Errorf("ParseHTTPURL(%q) rejected a valid URL: %v", in, err)
		}
	}

	invalid := []struct {
		in      string
		wantMsg string
	}{
		{"file:///etc/passwd", "scheme"},
		{"gopher://192.168.1.1/", "scheme"},
		{"ftp://192.168.1.1/", "scheme"},
		// url.Parse leaves the scheme empty for "javascript:...", so this is
		// reported as an unknown scheme rather than a malformed URL.
		{"javascript:alert(1)", "scheme"},
		{"http://user:pass@192.168.1.1/", "credentials"},
		{"http://user@192.168.1.1/", "credentials"},
		{"", "must not be empty"},
		{"http://", "no host"},
		// A bare host has no scheme and is not a URL.
		{"not a url", "scheme"},
		{"192.168.1.1", "scheme"},
	}
	for _, tc := range invalid {
		if _, err := ParseHTTPURL(tc.in); err == nil {
			t.Errorf("ParseHTTPURL(%q) accepted an invalid URL", tc.in)
		} else if !strings.Contains(err.Error(), tc.wantMsg) {
			t.Errorf("ParseHTTPURL(%q) error = %q, want %q", tc.in, err, tc.wantMsg)
		}
	}
}

// TestValidatePort covers port range checking.
func TestValidatePort(t *testing.T) {
	for _, p := range []int{1, 22, 80, 443, 65535} {
		if err := ValidatePort(p); err != nil {
			t.Errorf("ValidatePort(%d) rejected a valid port: %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536, 99999} {
		if err := ValidatePort(p); err == nil {
			t.Errorf("ValidatePort(%d) accepted an invalid port", p)
		}
	}
}

// TestParsePort covers string port parsing.
func TestParsePort(t *testing.T) {
	if p, err := ParsePort("8080"); err != nil || p != 8080 {
		t.Errorf("ParsePort(8080) = %d, %v", p, err)
	}
	for _, in := range []string{"", "abc", "0", "65536", "-1", "80.5"} {
		if _, err := ParsePort(in); err == nil {
			t.Errorf("ParsePort(%q) accepted an invalid port", in)
		}
	}
}

// TestParseInt checks bounded integer parsing.
func TestParseInt(t *testing.T) {
	if n, err := ParseInt("42", 0, 100); err != nil || n != 42 {
		t.Errorf("ParseInt(42) = %d, %v", n, err)
	}
	for _, tc := range []struct {
		in       string
		min, max int
	}{
		{"", 0, 10},
		{"-1", 0, 10},
		{"11", 0, 10},
		{"abc", 0, 10},
		{"1.5", 0, 10},
	} {
		if _, err := ParseInt(tc.in, tc.min, tc.max); err == nil {
			t.Errorf("ParseInt(%q, %d, %d) accepted an invalid value", tc.in, tc.min, tc.max)
		}
	}
}

// TestParseTime checks RFC 3339 parsing.
func TestParseTime(t *testing.T) {
	if _, err := ParseTime("2026-10-19T20:12:46Z"); err != nil {
		t.Errorf("ParseTime rejected a valid timestamp: %v", err)
	}
	for _, in := range []string{"", "yesterday", "2026-10-19", "2026-10-19 20:12:46"} {
		if _, err := ParseTime(in); err == nil {
			t.Errorf("ParseTime(%q) accepted an invalid timestamp", in)
		}
	}
}

// TestPaginationRejectsBadInput checks the query parameter validation.
func TestPaginationRejectsBadInput(t *testing.T) {
	app := newTestApp(t)

	cases := map[string]string{
		"/api/v1/devices?limit=abc":       "limit",
		"/api/v1/devices?limit=-1":        "limit",
		"/api/v1/devices?limit=0":         "limit",
		"/api/v1/devices?offset=abc":      "offset",
		"/api/v1/devices?offset=-5":       "offset",
		"/api/v1/devices?sort=nonsense":   "sort",
		"/api/v1/devices?order=sideways":  "order",
		"/api/v1/devices?type=notatype":   "type",
		"/api/v1/devices?state=notastate": "state",
		"/api/v1/devices?new_since=bad":   "new_since",
	}
	for path, wantField := range cases {
		rec := do(t, app, http.MethodGet, path, "")
		e := assertError(t, rec, http.StatusBadRequest)
		if _, ok := e.Details[wantField]; !ok && !strings.Contains(e.Error, wantField) {
			t.Errorf("%s: error %q does not name %q", path, e.Error, wantField)
		}
	}
}

// TestPaginationLimits checks clamping and defaults.
func TestPaginationLimits(t *testing.T) {
	app := newTestApp(t)

	var list struct {
		Limit int `json:"limit"`
	}
	getJSON(t, app, "/api/v1/devices", &list)
	if list.Limit != DefaultLimit {
		t.Errorf("default limit = %d, want %d", list.Limit, DefaultLimit)
	}
	getJSON(t, app, "/api/v1/devices?limit=100000", &list)
	if list.Limit != MaxLimit {
		t.Errorf("limit = %d, want clamped to %d", list.Limit, MaxLimit)
	}
	getJSON(t, app, "/api/v1/devices?limit=1", &list)
	if list.Limit != 1 {
		t.Errorf("limit = %d, want 1", list.Limit)
	}
}

// TestSSRFGuardAuthorisedSubnets checks the basic allow-list behaviour.
func TestSSRFGuardAuthorisedSubnets(t *testing.T) {
	g := NewSSRFGuard([]string{"192.168.100.0/24"}, false)

	allowed := []string{
		"192.168.100.1",
		"192.168.100.116",
		"192.168.100.0/24",
	}
	for _, target := range allowed {
		if _, _, err := g.CheckTarget(target); err != nil {
			t.Errorf("CheckTarget(%q) rejected an authorised target: %v", target, err)
		}
	}

	rejected := []string{
		"192.168.101.1",
		"10.0.0.1",
		"8.8.8.8",
		"192.168.100.0/23",
	}
	for _, target := range rejected {
		if _, _, err := g.CheckTarget(target); err == nil {
			t.Errorf("CheckTarget(%q) accepted a target outside the authorised subnets", target)
		}
	}
}

// TestSSRFGuardRejectsMetadataAndLoopback is the security acceptance test: the
// classic SSRF targets must all be refused.
func TestSSRFGuardRejectsMetadataAndLoopback(t *testing.T) {
	g := NewSSRFGuard([]string{"192.168.100.0/24"}, false)

	cases := map[string]string{
		// Cloud metadata endpoint.
		"169.254.169.254": "reserved local range",
		// Link-local, where a metadata service usually lives.
		"169.254.1.1": "reserved local range",
		// Loopback: the router's own services.
		"127.0.0.1":  "reserved local range",
		"127.0.0.53": "reserved local range",
		// Unspecified.
		"0.0.0.0": "unspecified",
		// Broadcast.
		"255.255.255.255": "broadcast",
		// IPv6 loopback and link-local.
		"::1":     "reserved local range",
		"fe80::1": "reserved local range",
		// Multicast.
		"224.0.0.1": "multicast",
		"ff02::1":   "multicast",
		// IPv4-mapped IPv6: an IPv4 destination smuggled inside IPv6.
		"::ffff:127.0.0.1": "reserved local range",
		// Tunnel ranges.
		"2002:c0a8:0101::1": "tunnel",
		"64:ff9b::a00:1":    "tunnel",
	}
	for target, wantMsg := range cases {
		_, _, err := g.CheckTarget(target)
		if err == nil {
			t.Errorf("CheckTarget(%q) was accepted but must be rejected", target)
			continue
		}
		if !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("CheckTarget(%q) error = %q, want it to mention %q", target, err, wantMsg)
		}
	}
}

// TestSSRFGuardRejectsBadSchemes checks scheme and credential rejection.
func TestSSRFGuardRejectsBadSchemes(t *testing.T) {
	g := NewSSRFGuard([]string{"192.168.100.0/24"}, true)
	g.AddKnownHostname("router.lan")

	cases := map[string]string{
		"file:///etc/passwd":                 "scheme",
		"gopher://192.168.100.1:70/_":        "scheme",
		"ftp://192.168.100.1/":               "scheme",
		"http://admin:secret@192.168.100.1/": "credentials",
		"http://user@192.168.100.1/":         "credentials",
	}
	for target, wantMsg := range cases {
		_, _, err := g.CheckTarget(target)
		if err == nil {
			t.Errorf("CheckTarget(%q) was accepted but must be rejected", target)
			continue
		}
		if !strings.Contains(err.Error(), wantMsg) {
			t.Errorf("CheckTarget(%q) error = %q, want it to mention %q", target, err, wantMsg)
		}
	}
}

// TestSSRFGuardPortRange checks that a port above 65535 is refused at the API
// layer.
func TestSSRFGuardPortRange(t *testing.T) {
	app := newTestApp(t)

	// A port above 65535 is a JSON number; the handler must reject it.
	rec := do(t, app, http.MethodPost, "/api/v1/monitoring/probes",
		`{"name":"Bad","kind":"network","target":"192.168.100.1","protocol":"tcp","port":70000}`)
	e := assertError(t, rec, http.StatusBadRequest)
	if _, ok := e.Details["port"]; !ok {
		t.Errorf("error does not name the port field: %+v", e)
	}
}

// TestSSRFGuardKnownDeviceAllowed checks that a device address outside the
// configured subnets is still acceptable, because it is already known.
func TestSSRFGuardKnownDeviceAllowed(t *testing.T) {
	g := NewSSRFGuard([]string{"192.168.100.0/24"}, false)
	// The upstream router on the WAN side is 192.168.1.1 in the fixture.
	if _, _, err := g.CheckTarget("192.168.1.1"); err == nil {
		t.Error("an address outside the authorised subnets must be rejected before it is known")
	}
	addr := netip.MustParseAddr("192.168.1.1")
	g.AddKnownAddress(addr)
	if _, _, err := g.CheckTarget("192.168.1.1"); err != nil {
		t.Errorf("a known device address must be accepted: %v", err)
	}
}

// TestSSRFGuardHostnameRequiresKnownDevice checks that a bare hostname is not
// a way around the address allow-list.
func TestSSRFGuardHostnameRequiresKnownDevice(t *testing.T) {
	g := NewSSRFGuard([]string{"192.168.100.0/24"}, false)

	if _, _, err := g.CheckTarget("evil.example.com"); err == nil {
		t.Error("an unknown hostname must be rejected: resolving it would be an SSRF vector")
	}
	g.AddKnownHostname("router.lan")
	if _, _, err := g.CheckTarget("router.lan"); err != nil {
		t.Errorf("a known device hostname must be accepted: %v", err)
	}
	// Case and a trailing dot must not defeat the check.
	if _, _, err := g.CheckTarget("ROUTER.LAN."); err != nil {
		t.Errorf("hostname matching must be case insensitive: %v", err)
	}
}

// TestSSRFGuardLocalhostOptIn checks that AllowLocalhost only widens to the
// local host, never to arbitrary reserved addresses.
func TestSSRFGuardLocalhostOptIn(t *testing.T) {
	g := NewSSRFGuard(nil, true)

	if _, _, err := g.CheckTarget("127.0.0.1"); err != nil {
		t.Errorf("AllowLocalhost must permit loopback: %v", err)
	}
	if _, _, err := g.CheckTarget("::1"); err != nil {
		t.Errorf("AllowLocalhost must permit IPv6 loopback: %v", err)
	}
	// 169.254.169.254 is link-local but is not the local host, so it stays out
	// even with AllowLocalhost: this is the metadata endpoint.
	if _, _, err := g.CheckTarget("169.254.169.254"); err == nil {
		t.Error("AllowLocalhost must not permit the metadata endpoint")
	}
}

// TestSSRFGuardEmptyRejectsEverything checks the safe default.
func TestSSRFGuardEmptyRejectsEverything(t *testing.T) {
	g := NewSSRFGuard(nil, false)
	if _, _, err := g.CheckTarget("192.168.100.1"); err == nil {
		t.Error("a guard with no authorised subnets must reject every target")
	}
}

// TestSSRFGuardLengthLimits checks the target size bounds.
func TestSSRFGuardLengthLimits(t *testing.T) {
	g := NewSSRFGuard([]string{"192.168.100.0/24"}, false)
	if _, _, err := g.CheckTarget("a"); err == nil {
		t.Error("a one character target must be rejected")
	}
	if _, _, err := g.CheckTarget(strings.Repeat("a", 300)); err == nil {
		t.Error("an oversized target must be rejected")
	}
}

// TestProbeTargetSSRFAtAPILevel checks the guard is actually wired into the
// probe endpoint, not just available as a helper.
func TestProbeTargetSSRFAtAPILevel(t *testing.T) {
	app := newTestApp(t)

	targets := []string{
		"169.254.169.254",
		"127.0.0.1",
		"0.0.0.0",
		"::1",
		"file:///etc/passwd",
		"gopher://192.168.100.1:70/_",
		"http://admin:pass@192.168.100.1/",
		"10.0.0.1",
		"8.8.8.8",
	}
	for _, target := range targets {
		body := `{"name":"SSRF","kind":"network","target":"` + target + `","protocol":"icmp"}`
		rec := do(t, app, http.MethodPost, "/api/v1/monitoring/probes", body)
		e := assertError(t, rec, http.StatusBadRequest)
		if _, ok := e.Details["target"]; !ok {
			t.Errorf("target %q: error does not name the target field: %+v", target, e)
		}
	}

	// A legitimately authorised target still works.
	rec := do(t, app, http.MethodPost, "/api/v1/monitoring/probes",
		`{"name":"Router","kind":"network","target":"192.168.100.1","protocol":"icmp"}`)
	assertStatus(t, rec, http.StatusCreated)
}

// TestScanSubnetsSSRF checks that a scan cannot be pointed outside the
// authorised space.
func TestScanSubnetsSSRF(t *testing.T) {
	app := newTestApp(t)

	for _, subnet := range []string{"0.0.0.0/0", "10.0.0.0/8", "8.8.8.0/24", "169.254.0.0/16"} {
		body := `{"subnets":["` + subnet + `"]}`
		rec := do(t, app, http.MethodPost, "/api/v1/discovery/scan", body)
		assertError(t, rec, http.StatusBadRequest)
	}
}
