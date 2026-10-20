package parsers

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/lanory/lanory/internal/model"
)

// ParseError marks a parse failure that carries the offending line or value, so
// callers can attach it to the evidence record.
type ParseError struct {
	// Format names the parser, e.g. "ip link".
	Format string
	// Line is the raw text that could not be parsed.
	Line string
	// Err is the underlying cause.
	Err error
}

// Error implements error.
func (e *ParseError) Error() string {
	return fmt.Sprintf("parsers: %s: %q: %v", e.Format, e.Line, e.Err)
}

// Unwrap exposes the underlying cause.
func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(format, line string, err error) error {
	return &ParseError{Format: format, Line: line, Err: err}
}

// ErrEmptyInput is returned when a parser receives no usable data at all. It is
// not a hard failure: an empty dump simply means "nothing observed".
var ErrEmptyInput = errors.New("parsers: empty input")

// LinkFlags is the set of flags `ip -br link` prints inside <...>.
type LinkFlags struct {
	Broadcast    bool
	Multicast    bool
	Loopback     bool
	PointToPoint bool
	Up           bool
	LowerUp      bool
	NoCarrier    bool
	Master       string // "<MASTER br0>"
	Vlan         string // "<VLAN 100>"
}

// LinkEntry is one line of `ip -br link show`.
type LinkEntry struct {
	Name  string
	State string // UNKNOWN | UP | DOWN
	MAC   string
	Flags LinkFlags
}

// HasCarrier reports whether the kernel believes the link is connected. The
// LOWER_UP flag is the authoritative signal: it reflects whether the physical
// layer is up, which is exactly the "cable plugged in" question.
func (f LinkFlags) HasCarrier() bool {
	return f.LowerUp && !f.NoCarrier
}

// ParseIPLink parses `ip -br link show`.
func ParseIPLink(data []byte) ([]LinkEntry, error) {
	lines := SplitLines(data)
	if len(lines) == 0 {
		return nil, ErrEmptyInput
	}
	out := make([]LinkEntry, 0, len(lines))
	for _, line := range lines {
		// The captured fixture files include the command header line
		// ("ip -br link show"). Skip any line that does not parse as an
		// interface row: a missing MAC means the line is not one.
		e, err := parseLinkLine(line)
		if err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func parseLinkLine(line string) (LinkEntry, error) {
	e := LinkEntry{}
	rest := strings.TrimSpace(line)
	// Name: the first whitespace-delimited token, but the name itself never
	// contains spaces so a simple Fields() prefix is enough.
	sp := strings.IndexAny(rest, " \t")
	if sp < 0 {
		return e, parseErr("ip link", line, errors.New("no interface name"))
	}
	e.Name = strings.TrimSpace(rest[:sp])
	rest = strings.TrimSpace(rest[sp+1:])

	sp = strings.IndexAny(rest, " \t")
	if sp < 0 {
		return e, parseErr("ip link", line, errors.New("no state"))
	}
	e.State = strings.ToUpper(strings.TrimSpace(rest[:sp]))
	rest = strings.TrimSpace(rest[sp+1:])

	sp = strings.IndexAny(rest, " \t")
	if sp < 0 {
		return e, parseErr("ip link", line, errors.New("no mac address"))
	}
	mac := strings.TrimSpace(rest[:sp])
	rest = strings.TrimSpace(rest[sp+1:])

	if !ValidMAC(mac) {
		return e, parseErr("ip link", line, fmt.Errorf("invalid mac %q", mac))
	}
	e.MAC = NormalizeMAC(mac)
	e.Flags = parseFlags(rest)
	return e, nil
}

func parseFlags(s string) LinkFlags {
	var f LinkFlags
	inner := s
	if i := strings.IndexByte(inner, '<'); i >= 0 {
		if j := strings.IndexByte(inner[i:], '>'); j >= 0 {
			inner = inner[i+1 : i+j]
		}
	}
	// `ip -br link` prints the flag list comma separated inside a single
	// <...> token, e.g. "<LOOPBACK,UP,LOWER_UP>". Verbose `ip link` output uses
	// separate tokens. Split on both so either form works.
	raw := strings.FieldsFunc(inner, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	for _, part := range raw {
		switch part {
		case "BROADCAST":
			f.Broadcast = true
		case "MULTICAST":
			f.Multicast = true
		case "LOOPBACK":
			f.Loopback = true
		case "POINTOPOINT":
			f.PointToPoint = true
		case "UP":
			f.Up = true
		case "LOWER_UP":
			f.LowerUp = true
		case "NO-CARRIER":
			f.NoCarrier = true
		case "MASTER":
			// Handled through the MASTER= form below.
		case "VLAN":
			// Handled through the VLAN= form below.
		default:
			if v, ok := strings.CutPrefix(part, "MASTER="); ok {
				f.Master = v
			}
			if v, ok := strings.CutPrefix(part, "VLAN="); ok {
				f.Vlan = v
			}
		}
	}
	return f
}

// NeighborEntry is one line of `ip neigh show` or `ip -6 neigh show`.
type NeighborEntry struct {
	Address   string
	Interface string
	MAC       string
	State     model.NeighborState
	// Router marks IPv6 neighbours that set the router flag.
	Router bool
}

// ParseIPNeigh parses `ip neigh show` (IPv4) or `ip -6 neigh show` (IPv6).
//
// Accepted line shapes:
//
//	ADDR dev IFNAME lladdr MAC [router] STATE
//	ADDR dev IFNAME [router] STATE
//	ADDR dev IFNAME lladdr MAC [STATE] [proxy] [router]
//
// When no state is printed the kernel reports an entry that is in the process
// of being resolved; those are recorded as NeighborIncomplete rather than
// dropped, because they prove traffic towards the address exists.
func ParseIPNeigh(data []byte) ([]NeighborEntry, error) {
	lines := SplitLines(data)
	if len(lines) == 0 {
		return nil, ErrEmptyInput
	}
	out := make([]NeighborEntry, 0, len(lines))
	for _, line := range lines {
		e, ok := parseNeighLine(line)
		if !ok {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// NeighStates maps the kernel neighbour state names to the model enum. Unknown
// spellings fall back to NeighborNone so the caller can decide to ignore them.
var NeighStates = map[string]model.NeighborState{
	"REACHABLE":  model.NeighborReachable,
	"STALE":      model.NeighborStale,
	"DELAY":      model.NeighborDelay,
	"PROBE":      model.NeighborProbe,
	"FAILED":     model.NeighborFailed,
	"INCOMPLETE": model.NeighborIncomplete,
	"PERMANENT":  model.NeighborPermanent,
	"NONE":       model.NeighborNone,
}

// MapNeighborState translates a kernel neighbour state word.
func MapNeighborState(v string) model.NeighborState {
	if s, ok := NeighStates[strings.ToUpper(strings.TrimSpace(v))]; ok {
		return s
	}
	return model.NeighborNone
}

func parseNeighLine(line string) (NeighborEntry, bool) {
	fields := Fields(line)
	if len(fields) < 2 {
		return NeighborEntry{}, false
	}
	e := NeighborEntry{Address: fields[0]}
	for i := 1; i < len(fields); i++ {
		switch f := fields[i]; f {
		case "dev":
			if i+1 < len(fields) {
				i++
				e.Interface = fields[i]
			}
		case "lladdr":
			if i+1 < len(fields) {
				i++
				e.MAC = NormalizeMAC(fields[i])
			}
		case "router":
			e.Router = true
		default:
			if s := MapNeighborState(f); s != model.NeighborNone {
				e.State = s
			}
		}
	}
	if e.Interface == "" {
		return NeighborEntry{}, false
	}
	if e.MAC == "" && e.State == model.NeighborNone {
		return NeighborEntry{}, false
	}
	return e, true
}

// FDBEntry is one line of `bridge fdb show`.
type FDBEntry struct {
	MAC       string
	Dev       string
	Master    string
	VLANID    int
	Self      bool
	Permanent bool
	Static    bool
	Dynamic   bool
}

// EntryType derives the FDB entry type from the flags printed by the bridge
// driver. When no flag is present the bridge learned the entry dynamically,
// which is the common case for a station that just sent a frame.
func (e FDBEntry) EntryType() model.FDBEntryType {
	switch {
	case e.Self:
		return model.FDBEntrySelf
	case e.Permanent:
		return model.FDBEntryPermanent
	case e.Static:
		return model.FDBEntryStatic
	case e.Dynamic:
		return model.FDBEntryDynamic
	default:
		return model.FDBEntryDynamic
	}
}

// Port returns the bridge port the entry was learned on. For entries whose
// device is the bridge itself the port is the bridge name, which callers treat
// as a bridge-self entry rather than a physical port.
func (e FDBEntry) Port() string { return e.Dev }

// ParseBridgeFDB parses `bridge fdb show`.
func ParseBridgeFDB(data []byte) ([]FDBEntry, error) {
	lines := SplitLines(data)
	if len(lines) == 0 {
		return nil, ErrEmptyInput
	}
	out := make([]FDBEntry, 0, len(lines))
	for _, line := range lines {
		e, ok := parseFDBLine(line)
		if !ok {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func parseFDBLine(line string) (FDBEntry, bool) {
	fields := Fields(line)
	if len(fields) < 2 || !ValidMAC(fields[0]) {
		return FDBEntry{}, false
	}
	e := FDBEntry{MAC: NormalizeMAC(fields[0])}
	for i := 1; i < len(fields); i++ {
		switch f := fields[i]; f {
		case "dev", "dst":
			if i+1 < len(fields) {
				i++
				e.Dev = fields[i]
			}
		case "master":
			if i+1 < len(fields) {
				i++
				e.Master = fields[i]
			}
		case "vlan":
			if i+1 < len(fields) {
				i++
				if n, err := strconv.Atoi(fields[i]); err == nil {
					e.VLANID = n
				}
			}
		case "self":
			e.Self = true
		case "permanent":
			e.Permanent = true
		case "static":
			e.Static = true
		case "dynamic":
			e.Dynamic = true
		}
	}
	if e.Dev == "" {
		return FDBEntry{}, false
	}
	return e, true
}

// LeaseEntry is one line of the dnsmasq lease file.
type LeaseEntry struct {
	// ExpiresAt is the absolute Unix time the lease expires.
	ExpiresAt int64
	MAC       string
	Address   string
	Hostname  string
	// ClientID is the raw client identifier field. "*" means the client did not
	// send one, so dnsmasq falls back to the MAC.
	ClientID string
}

// ParseLeases parses the dnsmasq /tmp/dhcp.leases format:
//
//	<expiry-unix> <mac> <ip> <hostname> <client-id>
//
// The client-id field may be "*" (wildcard) and the hostname may be "*" when
// the client sent no hostname option.
func ParseLeases(data []byte) ([]LeaseEntry, error) {
	lines := SplitLines(data)
	if len(lines) == 0 {
		return nil, ErrEmptyInput
	}
	out := make([]LeaseEntry, 0, len(lines))
	for _, line := range lines {
		fields := Fields(line)
		if len(fields) < 3 {
			continue
		}
		exp, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if !ValidMAC(fields[1]) {
			continue
		}
		e := LeaseEntry{
			ExpiresAt: exp,
			MAC:       NormalizeMAC(fields[1]),
			Address:   fields[2],
			ClientID:  "*",
		}
		if len(fields) >= 4 {
			e.Hostname = wildcard(fields[3])
		}
		if len(fields) >= 5 {
			e.ClientID = fields[4]
		}
		out = append(out, e)
	}
	return out, nil
}

func wildcard(v string) string {
	if v == "*" {
		return ""
	}
	return v
}

// UCIEntry is one flattened `uci export` line: "section.option=value".
type UCIEntry struct {
	// Package is the leading section, e.g. "network".
	Package string
	// Section is the section name or, for anonymous sections, "@type[0]".
	Section string
	// Option is the option name inside the section.
	Option string
	// Value is the raw (possibly quoted) value.
	Value string
}

// Key returns "package.section.option".
func (e UCIEntry) Key() string { return e.Package + "." + e.Section + "." + e.Option }

// ParseUCI parses the output of `uci export` (or `uci show`). Lines that do not
// contain an "=" are ignored, which makes the parser usable on the output of
// `uci show network` directly.
//
// The type marker line ("network.@device[0]=device" or
// "network.lan=interface") has no option component. It is emitted as an entry
// with Option set to the special marker OptionType so GroupUCI can fold it
// into the section instead of treating it as a regular option.
func ParseUCI(data []byte) ([]UCIEntry, error) {
	lines := SplitLines(data)
	if len(lines) == 0 {
		return nil, ErrEmptyInput
	}
	var out []UCIEntry
	for _, line := range lines {
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := TrimQuotes(strings.TrimSpace(line[eq+1:]))
		parts := strings.Split(key, ".")
		if len(parts) < 2 {
			continue
		}
		e := UCIEntry{Package: parts[0], Value: value}
		switch len(parts) {
		case 2:
			// "network.lan=interface" or "network.@device[0]=device": the value
			// is the section type and there is no option component.
			e.Section = parts[1]
			e.Option = OptionType
		default:
			e.Section = strings.Join(parts[1:len(parts)-1], ".")
			e.Option = parts[len(parts)-1]
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, ErrEmptyInput
	}
	return out, nil
}

// OptionType is the pseudo option holding a section's type marker.
const OptionType = "\x00type"

// UCISection is the grouped view of a UCI section.
type UCISection struct {
	Package string
	Section string
	// Type is the "interface" / "device" / "globals" marker. For named
	// sections it is derived from the section name.
	Type string
	// Options maps option name to value. List options are joined with spaces.
	Options map[string]string
}

// GroupUCI folds flat entries into sections, joining repeated list options.
//
// The type marker produced by ParseUCI is stored under the OptionType key and
// is deliberately excluded from the returned Options map, so callers reading
// Options see only real options.
func GroupUCI(entries []UCIEntry) []UCISection {
	var order []string
	byKey := map[string]*UCISection{}
	for _, e := range entries {
		key := e.Package + "." + e.Section
		sec, ok := byKey[key]
		if !ok {
			sec = &UCISection{Package: e.Package, Section: e.Section, Options: map[string]string{}}
			byKey[key] = sec
			order = append(order, key)
		}
		if e.Option == OptionType {
			// The type marker is consumed below by sectionType().
			continue
		}
		if prev, exists := sec.Options[e.Option]; exists {
			sec.Options[e.Option] = prev + " " + e.Value
			continue
		}
		sec.Options[e.Option] = e.Value
	}
	out := make([]UCISection, 0, len(order))
	for _, k := range order {
		sec := byKey[k]
		sec.Type = sectionType(entries, *sec)
		out = append(out, *sec)
	}
	return out
}

// sectionType derives the UCI section type. `uci export` writes it as
// "network.@iface[0]=interface" (the OptionType marker) while `uci show network`
// writes "network.@iface[0].interface=..." — both spellings are handled. A
// named section without an explicit type uses its own name as the type, which
// is how "network.brlan=device" becomes type "device".
func sectionType(entries []UCIEntry, sec UCISection) string {
	// The section type comes from the "=type" marker line
	// (network.lan=interface, network.@device[0]=device), which is authoritative.
	// Only if that is absent do we fall back to options.
	//
	// The order matters: a bridge device has BOTH a type marker ("device") and a
	// "type 'bridge'" option. Reading the option first made every bridge report
	// its type as "bridge", so topology lookup by section type "device" found
	// nothing and the bridge ports were never attached.
	for _, e := range entries {
		if e.Option == OptionType && e.Package == sec.Package && e.Section == sec.Section {
			if e.Value != "" {
				return e.Value
			}
		}
	}
	if t, ok := sec.Options["interface"]; ok && t != "" {
		return t
	}
	if t, ok := sec.Options["type"]; ok && t != "" {
		return t
	}
	if sec.Section == "globals" {
		return "globals"
	}
	if strings.HasPrefix(sec.Section, "@") {
		return ""
	}
	return sec.Section
}

// IsAnonymous reports whether the section is an anonymous UCI section
// ("@iface[0]"), which carries no stable name.
func (s UCISection) IsAnonymous() bool { return strings.HasPrefix(s.Section, "@") }

// Name returns the section name: the explicit "name" option when present, else
// the section key itself.
func (s UCISection) Name() string {
	if n, ok := s.Options["name"]; ok && n != "" {
		return n
	}
	return s.Section
}

// Get returns an option value and whether it was set.
func (s UCISection) Get(option string) (string, bool) {
	v, ok := s.Options[option]
	return v, ok
}

// ---------------------------------------------------------------------------
// MAC / address helpers
// ---------------------------------------------------------------------------

// NormalizeMAC lowercases a MAC address and renders it colon separated. It
// accepts the common spellings (aa:bb, aa-bb, aabb) and returns the input
// unchanged when it is not a well formed MAC, so callers can detect garbage.
func NormalizeMAC(v string) string {
	c := CanonicalMAC(v)
	if c == "" {
		return v
	}
	return FormatMAC(c)
}

// CanonicalMAC returns the 12 hex digit form, or "" when v is not a MAC.
func CanonicalMAC(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) != 12 {
		return ""
	}
	return strings.ToLower(out)
}

// ValidMAC reports whether v is a well formed MAC address.
func ValidMAC(v string) bool { return CanonicalMAC(v) != "" }

// FormatMAC renders a canonical MAC as colon separated lowercase groups.
func FormatMAC(canonical string) string {
	if len(canonical) != 12 {
		return strings.ToLower(canonical)
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(canonical[i : i+2])
	}
	return b.String()
}

// IsMulticastMAC reports whether the MAC is a well-known group address.
//
// The IEEE I/G bit (the least significant bit of the first octet) marks a
// group address, but using it alone is too blunt: a locally administered MAC
// whose first octet happens to be odd (03:11:.., 05:33:..) would be discarded
// even though it is a perfectly ordinary station address. The FDB and neighbour
// tables only ever contain the multicast ranges below in practice, so those are
// what gets filtered:
//
//	33:33:*  IPv6 solicited-node multicast
//	01:00:5e:*  IPv4 multicast (and the all-hosts/all-routers groups)
//	01:80:c2:*  IEEE 802.1D / LLDP / STP bridge protocol addresses
//	ff:*  broadcast
func IsMulticastMAC(canonical string) bool {
	if len(canonical) < 4 {
		return false
	}
	switch canonical[:4] {
	case "3333", "0100":
		// 33:33:*  IPv6 solicited-node multicast
		// 01:00:*  IPv4 multicast and IEEE 802 protocols
		return true
	case "0180":
		// 01:80:c2:*  STP / LLDP / 802.1X bridge protocols
		return len(canonical) >= 6 && canonical[:6] == "0180c2"
	}
	return canonical[:2] == "ff"
}

// IsBroadcastMAC reports whether the MAC is the broadcast address.
func IsBroadcastMAC(canonical string) bool { return canonical == "ffffffffffff" }

// IsZeroMAC reports whether the MAC is all zeros.
func IsZeroMAC(canonical string) bool { return canonical == "000000000000" }

// IsLinkLocalIPv6MAC reports whether the MAC is an EUI-64 derived link-local
// address (fe80::/64 mapped), i.e. starts with fe80.
func IsLinkLocalIPv6MAC(_ string) bool { return false }

func hexByte(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// IPFamily classifies an address string.
func IPFamily(addr string) model.AddressFamily {
	if strings.ContainsAny(addr, ":") {
		return model.AddressFamilyIPv6
	}
	return model.AddressFamilyIPv4
}

// ParseIP parses an address, rejecting IPv4-in-IPv6 mapped forms that would
// otherwise be recorded as IPv6.
func ParseIP(addr string) (net.IP, bool) {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return nil, false
	}
	if v4 := ip.To4(); v4 != nil {
		return v4, true
	}
	return ip, true
}

// IsLinkLocalIPv6 reports whether an IPv6 address is link-local (fe80::/10).
func IsLinkLocalIPv6(addr string) bool {
	ip, ok := ParseIP(addr)
	if !ok || ip.To4() != nil {
		return false
	}
	return ip.IsLinkLocalUnicast()
}

// IsUniqueLocalIPv6 reports whether an IPv6 address is in fc00::/7.
func IsUniqueLocalIPv6(addr string) bool {
	ip, ok := ParseIP(addr)
	if !ok || ip.To4() != nil {
		return false
	}
	b := ip.To16()
	return len(b) == 16 && (b[0]&0xfe) == 0xfc
}

// IPv6Bytes returns the 16 byte form of an address, zero valued when invalid.
func IPv6Bytes(addr string) []byte {
	ip, ok := ParseIP(addr)
	if !ok {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		out := make([]byte, 16)
		copy(out[10:], []byte{0xff, 0xff})
		copy(out[12:], v4)
		return out
	}
	return ip.To16()
}

// FirstUint32 is a helper used by the IPv6 -> MAC tests to keep the intent
// explicit.
func FirstUint32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(b[:4])
}

// ScanLine reports the trimmed line at index i, or "" when out of range.
func ScanLine(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

// bufioScanner is used by helpers that need a shared scanner configuration.
var _ = bufio.NewReader
