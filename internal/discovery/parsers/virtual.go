package parsers

import (
	"strconv"
	"strings"
)

// VirtualNameHints lists interface name fragments that identify interfaces
// which are not physical network ports. A device learned behind docker0 or
// dummy0 is never a network node, so the topology engine ignores them entirely.
//
// The list deliberately excludes plain "br-" prefixes: an OpenWrt LAN bridge
// (br-lan) is a real forwarding device built from physical ports, so it must
// not be classified as virtual by name alone. Container bridges such as
// "br-docker" and "docker0" are covered by their own hints.
var VirtualNameHints = []string{
	"docker", "dummy", "gretap", "erspan", "tunl", "sit", "teql",
	"veth", "wireguard", "wg", "tailscale", "ifb", "pppoe-wan",
	"6in4", "ip6tnl", "macvlan", "macvtap",
}

// LooksVirtualByName reports whether an interface name suggests a virtual
// device. It is a heuristic only: the caller confirms with the reported
// interface type when the system provides one.
//
// The loopback device is reported as virtual too: it is not a physical port and
// must never become a topology node. Callers that need to skip an interface
// entirely should use IsIgnoredName, which is narrower.
func LooksVirtualByName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if IsLoopbackName(n) {
		return true
	}
	for _, h := range VirtualNameHints {
		if strings.HasPrefix(n, h) || strings.Contains(n, "-"+h) || strings.Contains(n, h+"-") {
			return true
		}
	}
	return false
}

// IsLoopbackName reports whether the name denotes the loopback device.
func IsLoopbackName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "lo" || strings.HasPrefix(n, "lo:") || strings.HasPrefix(n, "loopback")
}

// IsIgnoredName reports whether an interface must be skipped entirely.
func IsIgnoredName(name string) bool {
	return IsLoopbackName(name)
}

// VirtualReason explains why an interface is virtual or ignored, for evidence.
// It returns "" for a name that does not look virtual, so callers can tell a
// real reason from "no opinion".
func VirtualReason(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case n == "lo" || strings.HasPrefix(n, "lo:") || strings.HasPrefix(n, "loopback"):
		return "loopback device"
	case strings.HasPrefix(n, "docker"):
		return "docker bridge"
	case strings.HasPrefix(n, "dummy"):
		return "dummy device"
	case strings.HasPrefix(n, "gretap"), strings.HasPrefix(n, "erspan"):
		return "tunnel overlay"
	case strings.HasPrefix(n, "wg"), strings.HasPrefix(n, "wireguard"):
		return "wireguard tunnel"
	case strings.HasPrefix(n, "tailscale"):
		return "tailscale tunnel"
	case strings.HasPrefix(n, "veth"):
		return "container veth pair"
	case strings.HasPrefix(n, "ifb"):
		return "intermediate functional block"
	case strings.HasPrefix(n, "macvlan"), strings.HasPrefix(n, "macvtap"):
		return "macvlan virtual port"
	case strings.HasPrefix(n, "teql"):
		return "traffic equalizer"
	case strings.HasPrefix(n, "sit"), strings.HasPrefix(n, "tunl"), strings.HasPrefix(n, "ip6tnl"):
		return "tunnel device"
	case strings.HasPrefix(n, "6in4"):
		return "6in4 tunnel"
	case strings.HasPrefix(n, "gre"):
		return "gre tunnel"
	case strings.HasPrefix(n, "br-docker"), strings.HasPrefix(n, "docker"):
		return "container bridge"
	case LooksVirtualByName(n):
		return "virtual interface"
	default:
		return ""
	}
}

// ParseUint parses an unsigned integer, returning 0 on failure. Command output
// on a router is occasionally garbled, and a zero value is safer than aborting
// the whole collector.
func ParseUint(v string) int {
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0
	}
	return int(n)
}

// ParseMACList parses the comma separated MAC list that `bridge fdb show`
// prints when several ports share an entry, keeping only the well formed ones.
func ParseMACList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if ValidMAC(part) {
			out = append(out, NormalizeMAC(part))
		}
	}
	return out
}
