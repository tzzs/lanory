package parsers

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// fixtureDir resolves the sanitized EASEPi R1 fixture directory relative to
// this test file (internal/discovery/parsers -> ../../..).
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "easepi-r1"))
	if err != nil {
		t.Fatalf("resolve fixtures: %v", err)
	}
	return dir
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := readFile(filepath.Join(fixtureDir(t), name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestParseIPLink(t *testing.T) {
	entries, err := ParseIPLink(fixture(t, "ip-link.txt"))
	if err != nil {
		t.Fatalf("ParseIPLink: %v", err)
	}
	if len(entries) != 8 {
		t.Fatalf("expected 8 interfaces, got %d: %+v", len(entries), entries)
	}

	byName := map[string]LinkEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	// lo: the loopback device reports LOWER_UP (it is always connected to
	// itself) and is classified as loopback. It must be ignored entirely
	// downstream, so its carrier value is irrelevant.
	if lo, ok := byName["lo"]; !ok {
		t.Fatal("lo missing")
	} else if !lo.Flags.Loopback {
		t.Fatalf("lo: loopback=%v carrier=%v", lo.Flags.Loopback, lo.Flags.HasCarrier())
	}

	// eth0: WAN, 100 Mbps, up.
	if e, ok := byName["eth0"]; !ok {
		t.Fatal("eth0 missing")
	} else if e.MAC != "02:00:00:00:00:10" || e.State != "UP" || !e.Flags.HasCarrier() {
		t.Fatalf("eth0: %+v", e)
	}

	// eth3 has NO-CARRIER: this is the port the fixture highlights.
	if e, ok := byName["eth3"]; !ok {
		t.Fatal("eth3 missing")
	} else if e.Flags.HasCarrier() {
		t.Fatalf("eth3 must have no carrier: %+v", e)
	}

	// br-lan is UP with carrier even though eth3 (a member) is down.
	if e, ok := byName["br-lan"]; !ok {
		t.Fatal("br-lan missing")
	} else if !e.Flags.HasCarrier() {
		t.Fatalf("br-lan must have carrier: %+v", e)
	}

	// Virtual ports.
	for _, n := range []string{"docker0", "dummy0"} {
		e, ok := byName[n]
		if !ok {
			t.Fatalf("%s missing", n)
		}
		if e.Flags.HasCarrier() {
			t.Fatalf("%s must have no carrier: %+v", n, e)
		}
	}
}

func TestParseIPLinkRejectsGarbage(t *testing.T) {
	// A line that is not an interface row is skipped, not fatal: the captured
	// fixtures carry a command header.
	entries, err := ParseIPLink([]byte("ip -br link show\n"))
	if err != nil {
		t.Fatalf("header line must be skipped, got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
	if _, err := ParseIPLink(nil); !isErr(err, ErrEmptyInput) {
		t.Fatalf("expected ErrEmptyInput, got %v", err)
	}
}

func TestParseIPNeighIPv4(t *testing.T) {
	entries, err := ParseIPNeigh(fixture(t, "ip-neigh.txt"))
	if err != nil {
		t.Fatalf("ParseIPNeigh: %v", err)
	}
	// 9 real entries; the INCOMPLETE line has no lladdr and the FAILED line
	// has neither lladdr nor a state word that resolves, so both are kept only
	// when they carry usable information.
	byAddr := map[string]NeighborEntry{}
	for _, e := range entries {
		byAddr[e.Address] = e
	}

	// The upstream gateway: REACHABLE on the WAN port.
	gw, ok := byAddr["192.168.1.1"]
	if !ok {
		t.Fatal("upstream gateway 192.168.1.1 missing")
	}
	if gw.MAC != "03:11:22:33:44:55" || gw.Interface != "eth0" || gw.State != model.NeighborReachable {
		t.Fatalf("gateway: %+v", gw)
	}

	// Windows PC is on eth2, not eth1. This is the fixture's key assertion.
	pc, ok := byAddr["192.0.2.116"]
	if !ok {
		t.Fatal("Windows PC address missing")
	}
	if pc.MAC != "02:11:22:33:44:b7" || pc.Interface != "eth2" {
		t.Fatalf("Windows PC must be on eth2: %+v", pc)
	}

	// STALE must be preserved verbatim, not upgraded to reachable.
	stale, ok := byAddr["192.0.2.151"]
	if !ok {
		t.Fatal("stale entry missing")
	}
	if stale.State != model.NeighborStale || stale.MAC != "06:77:88:99:aa:d2" {
		t.Fatalf("stale: %+v", stale)
	}

	// The other states the fixture exercises.
	for _, tc := range []struct {
		addr  string
		state model.NeighborState
	}{
		{"192.0.2.222", model.NeighborReachable},
		{"192.0.2.197", model.NeighborStale},
		{"192.0.2.159", model.NeighborReachable},
		{"192.0.2.228", model.NeighborDelay},
		{"192.0.2.231", model.NeighborProbe},
	} {
		got, ok := byAddr[tc.addr]
		if !ok {
			t.Fatalf("%s missing", tc.addr)
		}
		if got.State != tc.state {
			t.Fatalf("%s: expected %s, got %s", tc.addr, tc.state, got.State)
		}
	}
}

func TestParseIPNeighIPv6(t *testing.T) {
	entries, err := ParseIPNeigh(fixture(t, "ip-neigh6.txt"))
	if err != nil {
		t.Fatalf("ParseIPNeigh: %v", err)
	}
	if len(entries) != 7 {
		t.Fatalf("expected 7 ipv6 neighbours, got %d", len(entries))
	}

	// The same MAC appears with several addresses; the collector must dedupe
	// to one device. Here we only assert the raw parse keeps them separate.
	macCount := map[string]int{}
	for _, e := range entries {
		if e.MAC == "" {
			t.Fatalf("ipv6 neighbour %s has no MAC", e.Address)
		}
		macCount[e.MAC]++
	}
	if macCount["06:77:88:99:aa:d2"] != 2 {
		t.Fatalf("expected iPhone MAC on 2 addresses (link-local + global), got %d",
			macCount["06:77:88:99:aa:d2"])
	}

	// The router flag must be captured.
	var routers int
	for _, e := range entries {
		if e.Router {
			routers++
		}
	}
	if routers != 1 {
		t.Fatalf("expected exactly 1 router neighbour, got %d", routers)
	}

	// b4:11:22:33:44:13 is only ever seen over IPv6: its single address is a
	// link-local one, and the entry is STALE (which must not imply offline).
	only6 := false
	for _, e := range entries {
		if e.MAC == "b4:11:22:33:44:13" {
			only6 = true
			if e.Address != "fe80::1a2b:3c4d:5e6f:7012" {
				t.Fatalf("unexpected address for ipv6-only device: %s", e.Address)
			}
			if e.State != model.NeighborStale {
				t.Fatalf("expected STALE, got %s", e.State)
			}
			if !IsLinkLocalIPv6(e.Address) {
				t.Fatal("expected a link-local address")
			}
		}
	}
	if !only6 {
		t.Fatal("ipv6-only device missing from ip-neigh6.txt")
	}
}

func TestParseBridgeFDB(t *testing.T) {
	entries, err := ParseBridgeFDB(fixture(t, "bridge-fdb.txt"))
	if err != nil {
		t.Fatalf("ParseBridgeFDB: %v", err)
	}
	if len(entries) != 19 {
		t.Fatalf("expected 19 fdb lines, got %d: %+v", len(entries), entries)
	}

	byMAC := map[string]FDBEntry{}
	for _, e := range entries {
		byMAC[e.MAC] = e
	}

	// Learned station entries carry no flags, so they are dynamic.
	pc, ok := byMAC["02:11:22:33:44:b7"]
	if !ok {
		t.Fatal("Windows PC fdb entry missing")
	}
	// The fixture marks it "permanent" (a static ARP on the router); the port
	// is what matters for topology.
	if pc.Port() != "eth2" || pc.Master != "br-lan" {
		t.Fatalf("Windows PC fdb: %+v", pc)
	}
	if pc.EntryType() != model.FDBEntryPermanent {
		t.Fatalf("expected permanent, got %s", pc.EntryType())
	}

	// eth1 carries several learned MACs. Each is its own entry: 7 learned
	// stations, 1 bridge-self (02:00:00:00:00:11) and 3 multicast self entries.
	eth1 := 0
	for _, e := range entries {
		if e.Port() == "eth1" {
			eth1++
		}
	}
	if eth1 != 11 {
		t.Fatalf("expected 11 entries on eth1, got %d", eth1)
	}

	// The bridge self entries: 02:00:00:00:00:11 appears both on br-lan and on
	// eth1. Neither may become a device.
	for _, mac := range []string{
		"02:00:00:00:00:11", "02:00:00:00:00:12", "02:00:00:00:00:13",
		"33:33:00:00:00:01", "01:00:5e:00:00:01", "00:00:00:00:00:00",
	} {
		if _, ok := byMAC[mac]; !ok {
			t.Fatalf("expected %s in the raw fdb parse", mac)
		}
	}

	// Self flag detection.
	self, ok := byMAC["33:33:00:00:00:01"]
	if !ok || !self.Self || !self.Permanent {
		t.Fatalf("multicast self entry: %+v", self)
	}
}

func TestFDBEntryPort(t *testing.T) {
	e := FDBEntry{MAC: "aa:bb:cc:dd:ee:ff", Dev: "eth1", Master: "br-lan"}
	if e.Port() != "eth1" {
		t.Fatalf("port = %q", e.Port())
	}
}

func TestParseLeases(t *testing.T) {
	leases, err := ParseLeases(fixture(t, "dhcp-leases.txt"))
	if err != nil {
		t.Fatalf("ParseLeases: %v", err)
	}
	if len(leases) != 7 {
		t.Fatalf("expected 7 leases, got %d", len(leases))
	}

	byMAC := map[string]LeaseEntry{}
	for _, l := range leases {
		byMAC[l.MAC] = l
	}

	// client-desktop has an explicit client id.
	pc, ok := byMAC["02:11:22:33:44:b7"]
	if !ok {
		t.Fatal("client-desktop lease missing")
	}
	if pc.Address != "192.0.2.116" || pc.Hostname != "client-desktop" {
		t.Fatalf("client-desktop lease: %+v", pc)
	}
	if pc.ClientID != "01:02:11:22:33:44:b7" {
		t.Fatalf("client id: %+v", pc)
	}

	// The '*' wildcard client id must be preserved as "*" so the identity
	// engine can decide whether to fall back to the MAC.
	for _, mac := range []string{
		"06:77:88:99:aa:d2", "07:88:99:aa:bb:5e", "04:66:77:88:99:e3",
		"05:22:33:44:55:2d", "04:11:22:33:44:24", "cc:11:22:33:44:20",
	} {
		l, ok := byMAC[mac]
		if !ok {
			t.Fatalf("lease for %s missing", mac)
		}
		if l.ClientID != "*" {
			t.Fatalf("%s: expected wildcard client id, got %q", mac, l.ClientID)
		}
	}

	// A '*' hostname normalises to empty.
	lamp := byMAC["05:22:33:44:55:2d"]
	if lamp.Hostname != "smart-lamp" {
		t.Fatalf("smart-lamp hostname: %+v", lamp)
	}
	anon := byMAC["04:11:22:33:44:24"]
	if anon.Hostname != "" {
		t.Fatalf("expected empty hostname for '*', got %q", anon.Hostname)
	}
}

func TestParseUCI(t *testing.T) {
	entries, err := ParseUCI(fixture(t, "uci-network.txt"))
	if err != nil {
		t.Fatalf("ParseUCI: %v", err)
	}
	if len(entries) != 40 {
		t.Fatalf("expected 40 uci lines, got %d", len(entries))
	}

	secs := GroupUCI(entries)
	var lan, wan, brlan *UCISection
	for i := range secs {
		switch secs[i].Package + "." + secs[i].Section {
		case "network.lan":
			lan = &secs[i]
		case "network.wan":
			wan = &secs[i]
		case "network.brlan":
			brlan = &secs[i]
		}
	}

	if lan == nil || wan == nil || brlan == nil {
		t.Fatalf("missing uci sections: lan=%v wan=%v brlan=%v", lan != nil, wan != nil, brlan != nil)
	}

	// WAN interface is eth0; LAN interface is br-lan.
	if v, _ := lan.Get("device"); v != "br-lan" {
		t.Fatalf("lan device = %q", v)
	}
	if v, _ := lan.Get("ipaddr"); v != "192.168.100.1" {
		t.Fatalf("lan ipaddr = %q", v)
	}
	if v, _ := wan.Get("device"); v != "eth0" {
		t.Fatalf("wan device = %q", v)
	}

	// br-lan is a bridge with three member ports.
	if brlan.Type != "bridge" {
		t.Fatalf("br-lan type = %q", brlan.Type)
	}
	members := SplitList(brlan.Options["ports"])
	if !reflect.DeepEqual(members, []string{"eth1", "eth2", "eth3"}) {
		t.Fatalf("br-lan members = %v", members)
	}
}

func TestGroupUCIAnonymousSections(t *testing.T) {
	entries, err := ParseUCI([]byte("network.@device[0]=device\nnetwork.@device[0].name='eth0'\n"))
	if err != nil {
		t.Fatalf("ParseUCI: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 flat entries, got %d", len(entries))
	}
	secs := GroupUCI(entries)
	if len(secs) != 1 {
		t.Fatalf("expected 1 section, got %d: %+v", len(secs), secs)
	}
	s := secs[0]
	if !s.IsAnonymous() {
		t.Fatalf("expected an anonymous section: %+v", s)
	}
	if s.Type != "device" {
		t.Fatalf("type = %q", s.Type)
	}
	if s.Name() != "eth0" {
		t.Fatalf("name = %q", s.Name())
	}
	// The type marker must not leak into Options.
	if _, ok := s.Options[OptionType]; ok {
		t.Fatal("type marker leaked into Options")
	}
	if _, ok := s.Options["name"]; !ok {
		t.Fatal("name option missing")
	}
}

func TestNormalizeMAC(t *testing.T) {
	cases := map[string]string{
		"AA:BB:CC:DD:EE:FF": "aa:bb:cc:dd:ee:ff",
		"aa-bb-cc-dd-ee-ff": "aa:bb:cc:dd:ee:ff",
		"aabbccddeeff":      "aa:bb:cc:dd:ee:ff",
		"aa:bb:cc:dd:ee:ff": "aa:bb:cc:dd:ee:ff",
	}
	for in, want := range cases {
		if got := NormalizeMAC(in); got != want {
			t.Errorf("NormalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "nope", "aa:bb", "zz:bb:cc:dd:ee:ff"} {
		if ValidMAC(bad) {
			t.Errorf("ValidMAC(%q) = true", bad)
		}
	}
}

func TestMulticastClassification(t *testing.T) {
	multicast := []string{
		"33:33:00:00:00:01", "33:33:ff:4a:3d:d2", "01:00:5e:00:00:01",
		"01:80:c2:00:00:0e", "ff:ff:ff:ff:ff:ff",
	}
	for _, m := range multicast {
		c := CanonicalMAC(m)
		if !IsMulticastMAC(c) {
			t.Errorf("%s must be multicast", m)
		}
	}
	unicast := []string{
		"02:11:22:33:44:b7", "06:77:88:99:aa:d2", "00:00:00:00:00:00",
		"02:00:00:00:00:11", "b4:11:22:33:44:13",
	}
	for _, m := range unicast {
		if IsMulticastMAC(CanonicalMAC(m)) {
			t.Errorf("%s must not be multicast", m)
		}
	}
	if !IsZeroMAC(CanonicalMAC("00:00:00:00:00:00")) {
		t.Error("zero mac not detected")
	}
	if !IsBroadcastMAC(CanonicalMAC("ff:ff:ff:ff:ff:ff")) {
		t.Error("broadcast mac not detected")
	}
	if IsZeroMAC(CanonicalMAC("00:00:00:00:00:01")) {
		t.Error("non-zero mac reported as zero")
	}
}

func TestVirtualNameHeuristics(t *testing.T) {
	virtual := []string{"lo", "docker0", "dummy0", "gretap0", "erspan0", "veth123", "wg0", "tailscale0", "br-docker"}
	for _, n := range virtual {
		if !LooksVirtualByName(n) {
			t.Errorf("%s should look virtual", n)
		}
		if VirtualReason(n) == "" {
			t.Errorf("%s: no reason", n)
		}
	}
	ignored := []string{"lo", "lo:0"}
	for _, n := range ignored {
		if !IsIgnoredName(n) {
			t.Errorf("%s should be ignored", n)
		}
		if !IsLoopbackName(n) {
			t.Errorf("%s should be loopback", n)
		}
	}
	for _, n := range []string{"eth0", "eth1", "eth2", "eth3", "br-lan"} {
		if LooksVirtualByName(n) {
			t.Errorf("%s must not look virtual", n)
		}
		if IsIgnoredName(n) {
			t.Errorf("%s must not be ignored", n)
		}
	}
}

func TestIsLinkLocalIPv6(t *testing.T) {
	if !IsLinkLocalIPv6("fe80::1a2b:3c4d:5e6f:7012") {
		t.Error("fe80::/10 must be link-local")
	}
	if IsLinkLocalIPv6("2001:db8:100::151") {
		t.Error("2001:db8 must not be link-local")
	}
	if IsLinkLocalIPv6("192.168.100.1") {
		t.Error("ipv4 must not be link-local")
	}
	if !IsUniqueLocalIPv6("fd7c:8f21:9a03::1") {
		t.Error("fd00::/8 must be unique-local")
	}
}

func TestSplitLines(t *testing.T) {
	got := SplitLines([]byte("a\r\n\nb\n  \nc\n"))
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if SplitLines(nil) != nil {
		t.Fatal("nil input must yield nil")
	}
}

func TestTrimQuotesAndSplitList(t *testing.T) {
	if v := TrimQuotes("'eth0'"); v != "eth0" {
		t.Fatalf("got %q", v)
	}
	if v := TrimQuotes(`"br\"x"`); v != `br"x` {
		t.Fatalf("got %q", v)
	}
	if v := TrimQuotes("bare"); v != "bare" {
		t.Fatalf("got %q", v)
	}
	got := SplitList(" eth1 eth2 eth3 ")
	if !reflect.DeepEqual(got, []string{"eth1", "eth2", "eth3"}) {
		t.Fatalf("got %v", got)
	}
	got = SplitList("'eth1' 'eth2'")
	if !reflect.DeepEqual(got, []string{"eth1", "eth2"}) {
		t.Fatalf("got %v", got)
	}
}

func TestIPFamily(t *testing.T) {
	if IPFamily("192.168.100.1") != model.AddressFamilyIPv4 {
		t.Error("ipv4")
	}
	if IPFamily("fe80::1") != model.AddressFamilyIPv6 {
		t.Error("ipv6")
	}
}

func isErr(got, want error) bool {
	if got == want {
		return true
	}
	if got == nil || want == nil {
		return false
	}
	return got.Error() == want.Error()
}
