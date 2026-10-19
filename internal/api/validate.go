package api

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file holds every input validation helper used by the HTTP layer.
//
// The helpers live in one place so the rules are applied consistently and can
// be tested exhaustively. Every helper returns a plain error that is rendered
// verbatim in the "error" field of the standard error envelope, so messages
// must be actionable ("mac must look like aa:bb:cc:dd:ee:ff").
//
// Two properties matter more than convenience:
//
//  1. Malformed and ambiguous input is rejected rather than silently repaired.
//     "192.168.001.001" (leading zero octet) and "192.168.1.999" (octet > 255)
//     are ambiguous or out of range and must fail, because two parsers can
//     disagree on their meaning and that disagreement is a security bug.
//  2. Validation is the SSRF boundary. Nothing in this package performs I/O;
//     it only decides whether a value is acceptable.

// ErrInvalid is the sentinel wrapped by every validation error so handlers can
// distinguish a client mistake from an internal failure.
var ErrInvalid = errors.New("invalid input")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrInvalid)
}

// ---------------------------------------------------------------------------
// Addresses
// ---------------------------------------------------------------------------

// ParseIPv4 parses an IPv4 address in dotted quad form.
//
// Leading zeros are rejected: Go's netip rejects them, and RFC 6943 documents
// how differently systems interpret "010.1.1.1" (octal vs decimal).
func ParseIPv4(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "%") {
		return netip.Addr{}, invalid("ipv4 %q must not carry a zone suffix", truncate(s, 40))
	}
	if hasLeadingZeroOctet(s) {
		// Go's netip rejects this too, but with a generic message. Calling it
		// out matters: "192.168.001.001" is ambiguous — some parsers read the
		// octet as octal, some as decimal — and that disagreement is exactly
		// how an SSRF filter gets bypassed.
		return netip.Addr{}, invalid("ipv4 %q has an octet with a leading zero", truncate(s, 40))
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, invalid("ipv4 %q is not a valid address", truncate(s, 40))
	}
	if !addr.Is4() {
		return netip.Addr{}, invalid("ipv4 %q is not an IPv4 address", truncate(s, 40))
	}
	return addr, nil
}

// ParseIPv6 parses an IPv6 address.
//
// Zone suffixes such as "%eth0" are rejected. A zone identifies a local
// interface and is meaningless (and surprising) in an API payload: it lets a
// caller smuggle host-specific context into a stored target.
func ParseIPv6(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "%") {
		// A zone identifies a local interface. It is meaningless in a stored
		// target and lets a caller smuggle host specific context, so it is
		// refused before any further parsing.
		return netip.Addr{}, invalid("ipv6 %q must not carry a zone suffix", truncate(s, 40))
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, invalid("ipv6 %q is not a valid address", truncate(s, 40))
	}
	if !addr.Is6() {
		return netip.Addr{}, invalid("ipv6 %q is not an IPv6 address", truncate(s, 40))
	}
	return addr, nil
}

// ParseIP parses either an IPv4 or an IPv6 address, without a zone.
func ParseIP(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "%") {
		return netip.Addr{}, invalid("IP address %q must not carry a zone suffix", truncate(s, 40))
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, invalid("%q is not a valid IP address", truncate(s, 40))
	}
	return addr, nil
}

// hasLeadingZeroOctet reports whether a dotted quad has an octet written with a
// leading zero ("010.1.1.1"). Only the ASCII digit case is considered; the
// value is rejected before parsing so the message can be specific.
func hasLeadingZeroOctet(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}
	for _, octet := range strings.Split(s, ".") {
		if len(octet) > 1 && octet[0] == '0' {
			return true
		}
	}
	return false
}

// ParseCIDR parses an IPv4 or IPv6 CIDR prefix. The address part may be a host
// address (it is masked), but the prefix length must be in range.
func ParseCIDR(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, invalid("cidr %q is not a valid network", s)
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// MAC
// ---------------------------------------------------------------------------

// NormalizeMAC canonicalises a MAC address to "aabbccddeeff".
//
// Separators (: - .) and case are ignored; anything that does not reduce to
// exactly twelve hex digits is rejected.
func NormalizeMAC(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "", invalid("mac must not be empty")
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r == ':' || r == '-' || r == '.' || r == ' ':
			// Common separators: aa:bb:cc:dd:ee:ff, aa-bb-..., aabb.ccdd.eeff.
			continue
		default:
			return "", invalid("mac %q contains a non-hex character %q", truncate(s, 32), string(r))
		}
	}
	out := b.String()
	if len(out) != 12 {
		return "", invalid("mac %q must have exactly 12 hex digits", s)
	}
	return out, nil
}

// FormatMAC renders a canonical MAC as colon separated lowercase groups.
func FormatMAC(canonical string) string {
	if len(canonical) != 12 {
		return canonical
	}
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = canonical[i*2 : i*2+2]
	}
	return strings.Join(parts, ":")
}

// ---------------------------------------------------------------------------
// Hostnames
// ---------------------------------------------------------------------------

// maxHostnameLen is the DNS name limit (RFC 1035).
const maxHostnameLen = 253

// ValidateHostname reports whether s is a syntactically valid DNS hostname.
//
// A hostname label is 1-63 characters of letters, digits or hyphen, and may not
// start or end with a hyphen. Labels are joined by dots. A trailing dot
// (absolute form) is accepted and stripped. Anything containing a colon,
// percent sign or slash is rejected here; those belong to IPs and URLs.
func ValidateHostname(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("hostname must not be empty")
	}
	if len(s) > maxHostnameLen {
		return "", invalid("hostname %q is longer than %d bytes", truncate(s, 40), maxHostnameLen)
	}
	// Reject characters that would let a hostname smuggle in a URL or an IP.
	if strings.ContainsAny(s, ":/@?#%\\ \t") {
		return "", invalid("hostname %q contains illegal characters", truncate(s, 40))
	}
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return "", invalid("hostname must not be empty")
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return "", invalid("hostname %q has an empty label", truncate(s, 40))
		}
		if len(label) > 63 {
			return "", invalid("hostname label %q is longer than 63 characters", truncate(label, 20))
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", invalid("hostname label %q must not start or end with a hyphen", label)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			default:
				return "", invalid("hostname %q contains illegal character %q", truncate(s, 40), string(rune(c)))
			}
		}
	}
	return strings.ToLower(s), nil
}

// IsHostname reports whether s is a valid hostname.
func IsHostname(s string) bool {
	_, err := ValidateHostname(s)
	return err == nil
}

// IsIPLiteral reports whether s parses as a bare IP address (any family).
func IsIPLiteral(s string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	return err == nil && a.Zone() == ""
}

// ---------------------------------------------------------------------------
// URLs
// ---------------------------------------------------------------------------

// ParseHTTPURL parses an absolute http(s) URL and rejects everything else.
//
// Rejected: non-http(s) schemes (file:, gopher:, ftp:, ...), embedded
// credentials ("http://user:pass@host/"), and a missing host. Ports are
// checked separately by ValidatePort.
func ParseHTTPURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid("url must not be empty")
	}
	// Parse requires a scheme; "1.2.3.4" would otherwise be read as a path.
	u, err := url.Parse(s)
	if err != nil {
		return "", invalid("url %q is not a valid URL", truncate(s, 60))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", invalid("url scheme %q is not allowed (only http and https)", u.Scheme)
	}
	if !strings.Contains(s, "://") {
		// A bare host is not a URL: url.Parse reads a scheme-less string as a
		// path, which would silently accept anything.
		return "", invalid("url %q must be absolute and start with http:// or https://", truncate(s, 60))
	}
	if u.User != nil {
		return "", invalid("url must not contain credentials (%q)", truncate(s, 60))
	}
	if u.Hostname() == "" {
		return "", invalid("url %q has no host", truncate(s, 60))
	}
	return u.String(), nil
}

// ValidatePort checks a TCP/UDP port number.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return invalid("port %d is out of range (1-65535)", port)
	}
	return nil
}

// ParsePort parses a port from a string, rejecting empty, non-numeric and out
// of range values.
func ParsePort(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, invalid("port must not be empty")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, invalid("port %q is not a number", s)
	}
	if err := ValidatePort(n); err != nil {
		return 0, err
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Small primitives
// ---------------------------------------------------------------------------

// ParseInt parses a bounded integer query parameter.
func ParseInt(s string, min, max int) (int, error) {
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, invalid("%q is not an integer", s)
	}
	if n < min || n > max {
		return 0, invalid("%d is out of range (%d-%d)", n, min, max)
	}
	return n, nil
}

// ParseFloat parses a float clamped to [0,1] inclusive.
func ParseFloat01(s string) (float64, error) {
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, invalid("%q is not a number", s)
	}
	if f < 0 || f > 1 {
		return 0, invalid("%v is out of range (0-1)", f)
	}
	return f, nil
}

// ParseTime parses an RFC 3339 timestamp.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, invalid("%q is not an RFC 3339 timestamp", truncate(s, 40))
	}
	return t, nil
}

// truncate shortens a value for safe inclusion in an error message.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
