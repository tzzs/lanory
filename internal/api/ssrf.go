package api

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// This file implements the SSRF boundary between a user of Lanory and the
// network Lanory runs inside.
//
// Lanory can be pointed at arbitrary probe targets (probe.target) and scan
// subnets (POST /discovery/scan). Without this check an authenticated — or, in
// a LAN-only deployment, any LAN — caller could turn the router into an
// internal port scanner: probe every RFC1918 host for a service, reach the
// cloud metadata endpoint 169.254.169.254, or fingerprint the loopback
// services of the router itself.
//
// The rule is allow-list based:
//
//   - A target must be an address of an already-known device, or an address
//     inside an explicitly authorised subnet.
//   - Authorised subnets come from configuration and from the networks Lanory
//     itself discovered — never from the request body.
//   - URL targets must use http or https and must not carry credentials.
//   - IP literals in reserved ranges (loopback, link-local, unspecified,
//     multicast, tunnel/translation) are rejected unless the address is
//     explicitly the local host and AllowLocalhost is configured.
//
// No DNS lookup is performed here: resolving a hostname would itself be a
// network side effect from a validation function. Hostname and URL targets are
// accepted only when the hostname belongs to a device that is already known;
// the concrete probe engine resolves the name and re-checks the resulting
// address at execution time.
//
// This package never builds or executes a shell command; every check is pure
// address arithmetic on net/netip values.

// Target kinds returned by CheckTarget.
const (
	// TargetKindIP is a bare IP literal.
	TargetKindIP = "ip"
	// TargetKindCIDR is a network prefix.
	TargetKindCIDR = "cidr"
	// TargetKindHostname is a DNS name.
	TargetKindHostname = "hostname"
	// TargetKindURL is an absolute http(s) URL.
	TargetKindURL = "url"
)

// minTargetLen and maxTargetLen bound the size of a target string so a caller
// cannot use a huge payload as a resource exhaustion vector.
const (
	minTargetLen = 3
	maxTargetLen = 253
)

// SSRFGuard validates probe targets against the authorised address space.
//
// A zero SSRFGuard rejects everything: it only becomes usable once
// AuthorisedSubnets has been populated from configuration.
type SSRFGuard struct {
	// AuthorisedSubnets lists the CIDRs a user is allowed to probe or scan.
	// Loopback and link-local are stripped when it is built.
	AuthorisedSubnets []netip.Prefix
	// AllowLocalhost permits loopback and link-local addresses that belong to
	// this host, so Lanory can monitor its own HTTP listener. Off by default.
	AllowLocalhost bool
	// knownAddresses holds addresses already recorded for devices.
	knownAddresses map[netip.Addr]struct{}
	// knownHostnames holds hostnames already recorded for devices.
	knownHostnames map[string]struct{}
}

// NewSSRFGuard builds a guard from the configured subnets. Malformed entries
// are skipped rather than failing the whole server, because a typo in the
// configuration should not stop Lanory from booting.
func NewSSRFGuard(subnets []string, allowLocalhost bool) *SSRFGuard {
	g := &SSRFGuard{
		AuthorisedSubnets: make([]netip.Prefix, 0, len(subnets)),
		AllowLocalhost:    allowLocalhost,
		knownAddresses:    map[netip.Addr]struct{}{},
		knownHostnames:    map[string]struct{}{},
	}
	for _, s := range subnets {
		p, err := ParseCIDR(s)
		if err != nil {
			continue
		}
		g.AuthorisedSubnets = append(g.AuthorisedSubnets, p.Masked())
	}
	return g
}

// AddKnownAddress registers an address of an already known device, which is
// always an acceptable target regardless of the subnet rules.
func (g *SSRFGuard) AddKnownAddress(addr netip.Addr) {
	addr = addr.WithZone("").Unmap()
	if !addr.IsValid() {
		return
	}
	if g.knownAddresses == nil {
		g.knownAddresses = map[netip.Addr]struct{}{}
	}
	g.knownAddresses[addr] = struct{}{}
}

// AddKnownAddresses registers many addresses at once.
func (g *SSRFGuard) AddKnownAddresses(addrs []netip.Addr) {
	for _, a := range addrs {
		g.AddKnownAddress(a)
	}
}

// AddKnownHostname registers the hostname of an already known device.
func (g *SSRFGuard) AddKnownHostname(hostname string) {
	h := strings.ToLower(strings.TrimSpace(hostname))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return
	}
	if g.knownHostnames == nil {
		g.knownHostnames = map[string]struct{}{}
	}
	g.knownHostnames[h] = struct{}{}
}

// IsKnownAddress reports whether addr belongs to a known device.
func (g *SSRFGuard) IsKnownAddress(addr netip.Addr) bool {
	if !addr.IsValid() || g.knownAddresses == nil {
		return false
	}
	_, ok := g.knownAddresses[addr.WithZone("").Unmap()]
	return ok
}

// Authorised reports whether addr falls inside an authorised subnet.
func (g *SSRFGuard) Authorised(addr netip.Addr) bool {
	addr = addr.WithZone("").Unmap()
	for _, p := range g.AuthorisedSubnets {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// CheckTarget validates a probe target and returns the normalised target plus
// its kind (one of the TargetKind constants).
//
// An error is returned when the target is malformed, uses a forbidden scheme,
// carries credentials, targets a reserved address range, or falls outside the
// authorised address space.
func (g *SSRFGuard) CheckTarget(target string) (normalized string, kind string, err error) {
	t := strings.TrimSpace(target)
	if len(t) < minTargetLen || len(t) > maxTargetLen {
		return "", "", fmt.Errorf("target length must be between %d and %d characters", minTargetLen, maxTargetLen)
	}

	switch {
	case strings.Contains(t, "://"):
		return g.checkURL(t)
	case strings.Contains(t, "/"):
		return g.checkCIDR(t)
	default:
		return g.checkHost(t)
	}
}

// checkHost handles a bare IP literal or a hostname.
func (g *SSRFGuard) checkHost(t string) (string, string, error) {
	if addr, err := ParseIP(t); err == nil {
		if err := g.checkAddress(addr); err != nil {
			return "", "", err
		}
		return addr.String(), TargetKindIP, nil
	}
	host, err := ValidateHostname(t)
	if err != nil {
		return "", "", fmt.Errorf("target must be an IP address, a CIDR or a hostname: %w", err)
	}
	if g.hostnameKnown(host) {
		return host, TargetKindHostname, nil
	}
	return "", "", fmt.Errorf("hostname %q is not a known device; use an IP address inside an authorised subnet", truncate(host, 40))
}

// hostnameKnown reports whether a known device carries this hostname.
func (g *SSRFGuard) hostnameKnown(host string) bool {
	if g.knownHostnames == nil {
		return false
	}
	_, ok := g.knownHostnames[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))]
	return ok
}

// checkCIDR validates a network prefix.
//
// The whole prefix must be covered by the authorised space: a request for
// 192.168.100.0/23 must fail even though its first address is inside
// 192.168.100.0/24, because the other half is not.
func (g *SSRFGuard) checkCIDR(t string) (string, string, error) {
	p, err := ParseCIDR(t)
	if err != nil {
		return "", "", err
	}
	p = p.Masked()
	if err := g.checkAddress(p.Addr()); err != nil {
		return "", "", err
	}
	for _, authorised := range g.AuthorisedSubnets {
		if prefixCovers(authorised, p) {
			return p.String(), TargetKindCIDR, nil
		}
	}
	return "", "", fmt.Errorf("network %s is not entirely inside an authorised subnet", p)
}

// prefixCovers reports whether outer contains every address of inner.
func prefixCovers(outer, inner netip.Prefix) bool {
	if outer.Addr().Is4() != inner.Addr().Is4() {
		return false
	}
	if outer.Bits() > inner.Bits() {
		return false
	}
	return outer.Contains(inner.Addr())
}

// checkURL validates an absolute http(s) URL.
func (g *SSRFGuard) checkURL(t string) (string, string, error) {
	u, err := ParseHTTPURL(t)
	if err != nil {
		return "", "", err
	}
	// Re-extract the host from the normalised URL so credentials cannot hide
	// behind a redirect-looking path.
	host := hostOnly(u)
	if host == "" {
		return "", "", fmt.Errorf("url %q has no host", truncate(t, 60))
	}
	if addr, err := ParseIP(strings.Trim(host, "[]")); err == nil {
		if err := g.checkAddress(addr); err != nil {
			return "", "", err
		}
		return u, TargetKindURL, nil
	}
	h, err := ValidateHostname(host)
	if err != nil {
		return "", "", fmt.Errorf("url host %q is not a valid hostname", truncate(host, 40))
	}
	if !g.hostnameKnown(h) {
		return "", "", fmt.Errorf("url host %q is not a known device; use an IP address inside an authorised subnet", truncate(h, 40))
	}
	return u, TargetKindURL, nil
}

// checkAddress applies the reserved-range rules to a concrete address.
//
// The rules, in order:
//
//  1. Reject the unspecified address, the broadcast address and multicast.
//  2. Reject loopback and link-local unless this is the local host and
//     AllowLocalhost is configured.
//  3. Reject tunnel / translation ranges (IPv4-mapped, 6to4, Teredo, NAT64),
//     which are packets that can leave the local address space.
//  4. Accept addresses of known devices (a device may legitimately be a
//     link-local peer, e.g. a directly attached modem).
//  5. Otherwise require membership in an authorised subnet.
func (g *SSRFGuard) checkAddress(addr netip.Addr) error {
	addr = addr.WithZone("").Unmap()

	if addr.Is4() {
		b4 := addr.As4()
		switch b4 {
		case [4]byte{0, 0, 0, 0}:
			return fmt.Errorf("the unspecified address 0.0.0.0 is not a valid target")
		case [4]byte{255, 255, 255, 255}:
			return fmt.Errorf("the broadcast address 255.255.255.255 is not a valid target")
		}
	}
	if addr.IsMulticast() {
		return fmt.Errorf("multicast address %s is not a valid target", addr)
	}

	if isReservedLocal(addr) {
		if !(g.AllowLocalhost && g.IsLocalHost(addr)) {
			return fmt.Errorf("address %s is in a reserved local range (loopback or link-local) and is not allowed", addr)
		}
	}

	if tunnelRange(addr) {
		return fmt.Errorf("address %s is in a tunnel or translation range and is not allowed", addr)
	}

	if g.IsKnownAddress(addr) {
		return nil
	}
	if g.Authorised(addr) {
		return nil
	}
	if isReservedLocal(addr) && g.AllowLocalhost && g.IsLocalHost(addr) {
		// The local host is always an acceptable target once the operator has
		// opted in, so an authorised subnet is not required for it.
		return nil
	}
	return fmt.Errorf("address %s is outside the authorised subnets", addr)
}

// isReservedLocal reports whether addr is loopback or link-local.
func isReservedLocal(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsUnspecified()
}

// IsLocalHost reports whether addr addresses the machine Lanory runs on.
//
// Loopback addresses are always the local host. Any other address counts only
// when it is currently bound by one of the host's interfaces, so a caller
// cannot claim an arbitrary internet address is "the local host".
func (g *SSRFGuard) IsLocalHost(addr netip.Addr) bool {
	addr = addr.WithZone("").Unmap()
	if addr.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		p, perr := netip.ParsePrefix(a.String())
		if perr != nil {
			continue
		}
		if p.Addr().WithZone("").Unmap() == addr {
			return true
		}
	}
	return false
}

// tunnelRange reports whether addr is an IPv4-mapped, 6to4, Teredo or NAT64
// address, i.e. a packet that can leave the local network through a tunnel.
func tunnelRange(addr netip.Addr) bool {
	if addr.Is4() {
		return false
	}
	if addr.Is4In6() {
		// An IPv4-mapped address smuggles an IPv4 destination inside IPv6.
		return true
	}
	b := addr.As16()
	// 2002::/16 — 6to4
	if b[0] == 0x20 && b[1] == 0x02 {
		return true
	}
	// 2001:0000::/32 — Teredo
	if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x00 && b[3] == 0x00 {
		return true
	}
	// 64:ff9b::/96 — NAT64
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b {
		return true
	}
	return false
}

// hostOnly returns the host part of a URL, without scheme, port, credentials,
// path or query. It operates on an already parsed and normalised URL string.
func hostOnly(u string) string {
	rest := u
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, "@"); i >= 0 {
		rest = rest[i+1:]
	}
	if strings.HasPrefix(rest, "[") {
		if i := strings.Index(rest, "]"); i >= 0 {
			return rest[:i+1]
		}
		return rest
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		return rest[:i]
	}
	return rest
}
