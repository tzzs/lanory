package database

import (
	"strings"

	"github.com/lanory/lanory/internal/model"
)

// normalizeMAC lowercases a MAC address and strips separators, producing the
// canonical form "aabbccddeeff" used for identity matching.
func normalizeMAC(mac string) string {
	mac = strings.ToLower(strings.TrimSpace(mac))
	var b strings.Builder
	for _, r := range mac {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatMAC renders a canonical MAC as colon separated lowercase groups.
func FormatMAC(canonical string) string {
	if len(canonical) != 12 {
		return strings.ToLower(canonical)
	}
	parts := make([]string, 6)
	for i := 0; i < 6; i++ {
		parts[i] = canonical[i*2 : i*2+2]
	}
	return strings.Join(parts, ":")
}

// IsMulticastMAC reports whether the least significant bit of the first octet
// is set, which indicates a group (multicast) address rather than a station.
func IsMulticastMAC(canonical string) bool {
	if len(canonical) < 2 {
		return false
	}
	n := hexVal(canonical[0])<<4 | hexVal(canonical[1])
	return n&0x01 == 1
}

// IsBroadcastMAC reports whether the MAC is the broadcast address.
func IsBroadcastMAC(canonical string) bool {
	return canonical == "ffffffffffff"
}

// IsZeroMAC reports whether the MAC is all zeros.
func IsZeroMAC(canonical string) bool {
	return canonical == "000000000000"
}

// IsLocallyAdministeredMAC reports whether the locally administered bit is set.
// Randomised MAC addresses (used by modern phones and laptops for privacy) set
// this bit, which is a strong hint that the MAC cannot be used as a stable
// long-term identity.
func IsLocallyAdministeredMAC(canonical string) bool {
	if len(canonical) < 2 {
		return false
	}
	n := hexVal(canonical[0])<<4 | hexVal(canonical[1])
	return n&0x02 == 2
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

// CanonicalizeMAC converts any common MAC spelling to the canonical form.
func CanonicalizeMAC(mac string) string {
	return normalizeMAC(mac)
}

// ValidMAC reports whether the string is a well formed MAC address.
func ValidMAC(mac string) bool {
	c := normalizeMAC(mac)
	return len(c) == 12
}

// ValidMACList reports whether the string is a well formed comma separated MAC
// list as produced by `bridge fdb show`.
func ValidMACList(v string) bool {
	for _, part := range strings.Split(v, ",") {
		if !ValidMAC(part) {
			return false
		}
	}
	return true
}

// IdentityConfidence returns the confidence a MAC identity deserves given the
// source and whether the address looks randomised.
func IdentityConfidence(src model.DiscoverySource, randomised bool) float64 {
	base := 0.5
	switch src {
	case model.SourceDHCP:
		base = 0.85
	case model.SourceARP, model.SourceNDP:
		base = 0.8
	case model.SourceBridgeFDB:
		base = 0.7
	case model.SourceLLDP:
		base = 0.95
	case model.SourceMDNS:
		base = 0.6
	case model.SourceManual:
		base = 1.0
	}
	if randomised {
		base *= 0.6
	}
	if base > 1 {
		base = 1
	}
	return base
}
