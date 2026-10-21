// Package parsers turns the raw output of OpenWrt system commands and the
// procfs/sysfs style files they read into the typed structures the discovery
// collectors emit.
//
// Every parser in this package is pure: it takes a byte slice and returns a
// value plus an error. Nothing here touches the system, so the parsers can be
// tested against the sanitized fixtures in fixtures/easepi-r1/ without a router.
//
// The parsers are deliberately tolerant. A router running an older iproute2 or
// a trimmed down busybox build produces slightly different output than a full
// Linux box, so a line that does not match the expected shape is skipped rather
// than failing the whole dump. The error return is reserved for input that is
// structurally unusable (for example truncated JSON).
package parsers

import "strings"

// SplitLines splits raw output into lines, dropping empty lines and trailing
// carriage returns (fixtures captured from a router may use either line ending).
func SplitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	raw := strings.Split(string(data), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// Fields splits a line on runs of whitespace. It never returns nil, so callers
// can index safely after checking the length.
func Fields(line string) []string {
	return strings.Fields(line)
}

// TrimQuotes removes the single or double quotes `uci export` wraps around
// values, and unescapes the escaped quotes inside them.
func TrimQuotes(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"') {
			inner := v[1 : len(v)-1]
			if v[0] == '\'' {
				inner = strings.ReplaceAll(inner, `\'`, "'")
			} else {
				inner = strings.ReplaceAll(inner, `\"`, `"`)
			}
			return inner
		}
	}
	return v
}

// SplitList splits a UCI list value such as "ports= eth1 eth2 eth3" into its
// elements. UCI lists are whitespace separated and each element may be quoted.
//
// A single quoted group covering the whole value is unwrapped first; otherwise
// the value is split on whitespace and each element is unquoted individually,
// so "'eth1' 'eth2'" yields two elements rather than one mangled string.
func SplitList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	// Do NOT strip an outer quote pair here.
	//
	// UCI writes a list as 'eth1' 'eth2' 'eth0', which starts and ends with a
	// single quote and therefore looks like one quoted string. Trimming the
	// outer pair first turned it into eth1' 'eth2' 'eth0, and the per-field
	// trim below then produced "eth1'" and "'eth0" - names that match no real
	// interface, so bridge membership silently disappeared on a real router.
	// Splitting first and trimming each field is correct for both forms.
	fields := strings.Fields(v)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = TrimQuotes(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// isSingleQuotedGroup reports whether v is exactly one quoted string, i.e. the
// closing quote is the last character and there is no whitespace outside the
// quotes.
func isSingleQuotedGroup(v string) bool {
	if len(v) < 2 {
		return false
	}
	q := v[0]
	if q != '\'' && q != '"' {
		return false
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '\\' {
			i++
			continue
		}
		if v[i] == q {
			// Everything after the closing quote must be blank.
			return strings.TrimSpace(v[i+1:]) == ""
		}
		if v[i] == ' ' || v[i] == '\t' {
			return false
		}
	}
	return false
}

// StripVPEPrefix removes the "@ifN" suffix `ip -br link` appends to the peer of
// a veth pair, returning the local interface name.
func StripVPEPrefix(name string) string {
	if i := strings.IndexByte(name, '@'); i > 0 {
		return name[:i]
	}
	return name
}
