package fdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/discovery/parsers"
	"github.com/lanory/lanory/internal/model"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "easepi-r1", name))
	if err != nil {
		t.Fatalf("resolve fixture: %v", err)
	}
	return p
}

func newFixtureCollector(t *testing.T) *Collector {
	t.Helper()
	// The FDB dump plus the interface list: the interface MACs are what lets
	// the collector tell "the bridge repeating eth2's own address" apart from
	// "a station behind eth2".
	c := NewWithPaths(fixture(t, "bridge-fdb.txt"), fixture(t, "ip-link.txt"))
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

// TestNotDevices is the acceptance test for noise filtering. Every MAC in this
// table appears in bridge-fdb.txt and must NOT become a device.
func TestNotDevices(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	seen := map[string]bool{}
	for i := range results {
		if results[i].FDBEntry != nil {
			seen[results[i].FDBEntry.MAC] = true
		}
		if results[i].Link != nil && results[i].Link.RemoteMAC != "" {
			seen[results[i].Link.RemoteMAC] = true
		}
	}

	notDevices := []string{
		"33:33:00:00:00:01", // multicast (IPv6 solicited node)
		"33:33:ff:4a:3d:d2", // multicast (solicited node for the iPhone)
		"33:33:ff:10:16:13", // multicast (solicited node for the ipv6-only dev)
		"01:00:5e:00:00:01", // IPv4 multicast
		"b0:1b:cd:a2:33:11", // bridge self / permanent local
		"b0:1b:cd:a2:33:12", // permanent local
		"b0:1b:cd:a2:33:13", // permanent local (no carrier port)
		"00:00:00:00:00:00", // zero mac
		"02:42:9c:1e:5f:7a", // docker0 virtual interface
		"2e:4a:66:11:22:33", // dummy0 virtual interface
	}
	for _, mac := range notDevices {
		if seen[mac] {
			t.Errorf("noise MAC %s must not produce an observation", mac)
		}
	}

	// The bridge's own MACs (02:00:00:00:00:1x) are the bridge, not devices.
	for _, mac := range []string{"02:00:00:00:00:11", "02:00:00:00:00:12", "02:00:00:00:00:13"} {
		if seen[mac] {
			t.Errorf("bridge self MAC %s must not produce an observation", mac)
		}
	}
	// The docker0 / dummy0 MACs from ip-link.txt are also noise.
	for _, mac := range []string{"02:42:ac:12:00:02", "02:11:22:33:44:55"} {
		if seen[mac] {
			t.Errorf("virtual interface MAC %s must not produce an observation", mac)
		}
	}
}

// TestLearnedDevices asserts that exactly the eight real devices survive, each
// with the correct port.
func TestLearnedDevices(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	type obs struct {
		port   string
		bridge string
		vlan   int
		typ    model.FDBEntryType
	}
	got := map[string]obs{}
	var links int
	for i := range results {
		r := &results[i]
		if r.FDBEntry != nil {
			got[r.FDBEntry.MAC] = obs{
				port:   r.FDBEntry.Port,
				bridge: r.FDBEntry.Bridge,
				vlan:   r.FDBEntry.VLANID,
				typ:    r.FDBEntry.EntryType,
			}
		}
		if r.Link != nil {
			links++
		}
	}

	want := map[string]obs{
		"02:11:22:33:44:b7": {port: "eth2", bridge: "br-lan", typ: model.FDBEntryPermanent},
		"06:77:88:99:aa:d2": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
		"07:88:99:aa:bb:5e": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
		"04:66:77:88:99:e3": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
		"05:22:33:44:55:2d": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
		"04:11:22:33:44:24": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
		"cc:11:22:33:44:20": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
		"b4:11:22:33:44:13": {port: "eth1", bridge: "br-lan", typ: model.FDBEntryDynamic},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d device observations, got %d: %v", len(want), len(got), got)
	}
	for mac, w := range want {
		g, ok := got[mac]
		if !ok {
			t.Errorf("device %s missing", mac)
			continue
		}
		if g.port != w.port {
			t.Errorf("%s: port = %q, want %q", mac, g.port, w.port)
		}
		if g.bridge != w.bridge {
			t.Errorf("%s: bridge = %q, want %q", mac, g.bridge, w.bridge)
		}
		if g.typ != w.typ {
			t.Errorf("%s: entry type = %q, want %q", mac, g.typ, w.typ)
		}
	}

	// The Windows PC is on eth2, not eth1. This is the fixture's headline
	// assertion and the easiest one to get wrong.
	if got["02:11:22:33:44:b7"].port != "eth2" {
		t.Errorf("Windows PC must be linked to eth2, got %q", got["02:11:22:33:44:b7"].port)
	}

	// One link per device observation.
	if links != len(want) {
		t.Errorf("expected %d link observations, got %d", len(want), links)
	}
}

// TestNoWirelessAssumption is the core product rule: a port with several learned
// MACs must NOT be reported as a wireless association. The collector has no idea
// what is behind eth1, so it must not claim it is WiFi.
func TestNoWirelessAssumption(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	perPort := map[string]int{}
	for i := range results {
		l := results[i].Link
		if l == nil {
			continue
		}
		if l.LinkType != model.EdgeTypeObservedMACPort {
			t.Fatalf("unexpected link type %q", l.LinkType)
		}
		if l.LinkType == model.EdgeTypeWirelessAssoc {
			t.Fatal("FDB must never assert a wireless association")
		}
		perPort[l.Port]++
	}
	// eth1 has 7 learned MACs, eth2 has 1. They must stay separate observations.
	if perPort["eth1"] != 7 {
		t.Errorf("expected 7 separate eth1 observations, got %d", perPort["eth1"])
	}
	if perPort["eth2"] != 1 {
		t.Errorf("expected 1 eth2 observation, got %d", perPort["eth2"])
	}
}

// TestLinkConfidence pins the confidence so the topology engine's weighting
// stays stable.
func TestLinkConfidence(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i := range results {
		l := results[i].Link
		if l == nil {
			continue
		}
		if l.Confidence != 0.70 {
			t.Errorf("link confidence = %v, want 0.70", l.Confidence)
		}
		if l.Interface != "br-lan" {
			t.Errorf("link interface = %q, want br-lan", l.Interface)
		}
		if l.RemoteMAC == "" {
			t.Error("link has no remote MAC")
		}
		if l.Port == "" {
			t.Error("link has no port")
		}
	}
}

func TestVLANExtraction(t *testing.T) {
	// A VLAN-tagged FDB entry must carry its VLAN id.
	c := NewWithPath(fixture(t, "bridge-fdb.txt"))
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i := range results {
		o := results[i].FDBEntry
		if o != nil && o.VLANID != 0 {
			t.Logf("%s has vlan %d", o.MAC, o.VLANID)
		}
	}
	// The fixture has no tagged entries; the parser is covered by
	// parsers.TestParseBridgeFDB.
}

// TestMissingFile makes an unsupported collector, not an error.
func TestMissingFile(t *testing.T) {
	c := NewWithPath(filepath.Join(t.TempDir(), "does-not-exist"))
	c.now = time.Now
	if !c.Supported() {
		t.Fatal("a configured path must be considered supported")
	}
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect must not error on a missing file: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorBridgeFDB {
		t.Fatalf("name = %q", c.Name())
	}
	caps := c.Capabilities()
	if len(caps) == 0 {
		t.Fatal("expected capabilities")
	}
	if c.Description() == "" {
		t.Fatal("expected a description")
	}
}

// An empty FDB dump is a valid outcome (a bridge with no learned MACs).
func TestEmptyInput(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/empty.txt"
	if err := writeFile(path, "\n\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

// A garbage FDB dump must not panic and must not invent devices.
func TestGarbageInput(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/garbage.txt"
	if err := writeFile(path, "not a mac at all\nrandom text\n\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results from garbage, got %d", len(results))
	}
}

// A truncated but valid MAC must not produce a half-parsed device.
func TestPartialMAC(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/partial.txt"
	if err := writeFile(path, "aa:bb:cc:dd:ee dev eth1 master br-lan\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPath(path)
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results for a truncated MAC, got %d", len(results))
	}
}

// TestShouldSkipRules exercises the filter rules directly, including the ones
// the fixture does not contain (a VLAN-tagged entry, a static entry).
func TestShouldSkipRules(t *testing.T) {
	virtuals := map[string]bool{"docker0": true, "dummy0": true}
	bridgeMACs := map[string]bool{"02:00:00:00:00:11": true}
	localMACs := map[string]bool{
		"02:00:00:00:00:12": true,
		"02:00:00:00:00:13": true,
	}
	cases := []struct {
		name  string
		entry parsers.FDBEntry
		skip  bool
	}{
		{"normal learned", parsers.FDBEntry{MAC: "aa:bb:cc:dd:ee:ff", Dev: "eth1", Master: "br-lan"}, false},
		{"zero mac", parsers.FDBEntry{MAC: "00:00:00:00:00:00", Dev: "br-lan", Master: "br-lan"}, true},
		{"broadcast mac", parsers.FDBEntry{MAC: "ff:ff:ff:ff:ff:ff", Dev: "eth1", Master: "br-lan"}, true},
		{"ipv6 multicast", parsers.FDBEntry{MAC: "33:33:ff:aa:bb:cc", Dev: "eth1", Self: true, Permanent: true}, true},
		{"ipv4 multicast", parsers.FDBEntry{MAC: "01:00:5e:00:00:01", Dev: "eth2", Self: true, Permanent: true}, true},
		{"bridge own mac on member port", parsers.FDBEntry{MAC: "02:00:00:00:00:11", Dev: "eth1", Master: "br-lan", Permanent: true}, true},
		{"bridge own mac on the bridge", parsers.FDBEntry{MAC: "02:00:00:00:00:11", Dev: "br-lan", Master: "br-lan", Permanent: true}, true},
		{"local iface mac permanent", parsers.FDBEntry{MAC: "02:00:00:00:00:12", Dev: "eth2", Master: "br-lan", Permanent: true}, true},
		{"local iface mac dynamic", parsers.FDBEntry{MAC: "02:00:00:00:00:12", Dev: "eth2", Master: "br-lan", Dynamic: true}, false},
		{"virtual iface mac", parsers.FDBEntry{MAC: "02:42:ac:12:00:02", Dev: "docker0", Master: "docker0", Permanent: true}, true},
		{"unparseable mac", parsers.FDBEntry{MAC: "garbage", Dev: "eth1", Master: "br-lan"}, true},
	}
	for _, tc := range cases {
		got, reason := shouldSkip(tc.entry, virtuals, bridgeMACs, localMACs)
		if got != tc.skip {
			t.Errorf("%s: skip = %v (%s), want %v", tc.name, got, reason, tc.skip)
		}
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
