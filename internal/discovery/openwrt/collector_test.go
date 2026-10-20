package openwrt

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
	c := NewWithPaths(
		fixture(t, "uci-network.txt"),
		fixture(t, "ip-link.txt"),
		fixture(t, "ubus-network-device.json"),
		fixture(t, "ubus-network-interface.json"),
	)
	c.now = func() time.Time { return time.Unix(1771000000, 0).UTC() }
	return c
}

func TestSupportedWithFixtures(t *testing.T) {
	c := newFixtureCollector(t)
	if !c.Supported() {
		t.Fatal("fixture collector must be supported")
	}
	// Every source is present.
	src := c.Sources()
	for k, v := range src {
		if !v {
			t.Errorf("source %s should be available", k)
		}
	}
}

func TestNameAndCapabilities(t *testing.T) {
	c := New()
	if c.Name() != model.CollectorOpenWRT {
		t.Fatalf("name = %q", c.Name())
	}
	caps := c.Capabilities()
	if len(caps) == 0 {
		t.Fatal("expected capabilities")
	}
	// Mutating the returned slice must not affect the collector.
	caps[0] = "mutated"
	if c.Capabilities()[0] == "mutated" {
		t.Fatal("Capabilities must return a copy")
	}
	if c.Description() == "" {
		t.Fatal("expected a description")
	}
}

func TestCollectInterfaces(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	byName := map[string]*struct {
		Type     model.InterfaceType
		Role     model.InterfaceRole
		Carrier  bool
		Speed    int
		Oper     model.OperState
		BridgeID string
		Ignore   bool
		Virtual  bool
	}{}
	var ifaceCount, addrCount, linkCount int
	for i := range results {
		r := &results[i]
		switch r.Kind {
		case "interface":
			ifaceCount++
			o := r.Interface
			byName[o.Name] = &struct {
				Type     model.InterfaceType
				Role     model.InterfaceRole
				Carrier  bool
				Speed    int
				Oper     model.OperState
				BridgeID string
				Ignore   bool
				Virtual  bool
			}{o.Type, o.Role, o.Carrier, o.SpeedMbps, o.OperState, o.BridgeID, o.Ignore, o.Virtual}
		case "address":
			addrCount++
		case "link":
			linkCount++
		}
	}

	// 8 interfaces are reported by the ubus device dump.
	if ifaceCount != 8 {
		t.Fatalf("expected 8 interface observations, got %d", ifaceCount)
	}
	// WAN: 1 v4 + 1 v6; LAN: 1 v4 + 2 v6. The loopback address is not emitted
	// as a standalone observation because lo is ignored entirely.
	if addrCount != 5 {
		t.Fatalf("expected 5 address observations, got %d", addrCount)
	}
	// br-lan has 3 members.
	if linkCount != 3 {
		t.Fatalf("expected 3 bridge membership links, got %d", linkCount)
	}

	// --- the fixture's documented interface expectations ---
	for _, tc := range []struct {
		name    string
		role    model.InterfaceRole
		typ     model.InterfaceType
		carrier bool
		speed   int
		oper    model.OperState
		bridge  string
	}{
		{"eth0", model.InterfaceRoleWAN, model.InterfaceTypeEthernet, true, 100, model.OperUp, ""},
		{"eth1", model.InterfaceRoleLAN, model.InterfaceTypeEthernet, true, 1000, model.OperUp, "br-lan"},
		{"eth2", model.InterfaceRoleLAN, model.InterfaceTypeEthernet, true, 2500, model.OperUp, "br-lan"},
		{"eth3", model.InterfaceRoleLAN, model.InterfaceTypeEthernet, false, 0, model.OperNoCarrier, "br-lan"},
		{"br-lan", model.InterfaceRoleLAN, model.InterfaceTypeBridge, true, 0, model.OperUp, ""},
	} {
		got, ok := byName[tc.name]
		if !ok {
			t.Fatalf("%s missing from interface observations", tc.name)
		}
		if got.Role != tc.role {
			t.Errorf("%s: role = %q, want %q", tc.name, got.Role, tc.role)
		}
		if got.Type != tc.typ {
			t.Errorf("%s: type = %q, want %q", tc.name, got.Type, tc.typ)
		}
		if got.Carrier != tc.carrier {
			t.Errorf("%s: carrier = %v, want %v", tc.name, got.Carrier, tc.carrier)
		}
		if got.Speed != tc.speed {
			t.Errorf("%s: speed = %d, want %d", tc.name, got.Speed, tc.speed)
		}
		if got.Oper != tc.oper {
			t.Errorf("%s: oper = %q, want %q", tc.name, got.Oper, tc.oper)
		}
		if got.BridgeID != tc.bridge {
			t.Errorf("%s: bridge = %q, want %q", tc.name, got.BridgeID, tc.bridge)
		}
	}

	// --- virtual / ignored interfaces ---
	for _, tc := range []struct {
		name   string
		ignore bool
	}{
		{"lo", true},
		{"docker0", true},
		{"dummy0", true},
	} {
		got, ok := byName[tc.name]
		if !ok {
			t.Fatalf("%s missing", tc.name)
		}
		if got.Ignore != tc.ignore {
			t.Errorf("%s: ignore = %v, want %v", tc.name, got.Ignore, tc.ignore)
		}
		if !got.Virtual {
			t.Errorf("%s: must be marked virtual", tc.name)
		}
	}

	// The loopback interface must not be a LAN interface.
	if byName["lo"].Role != model.InterfaceRoleLoopback {
		t.Errorf("lo: role = %q", byName["lo"].Role)
	}
	if byName["docker0"].Role != model.InterfaceRoleVirtual {
		t.Errorf("docker0: role = %q", byName["docker0"].Role)
	}
}

func TestCollectAddresses(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	type addr struct {
		iface string
		mac   string
		plen  int
	}
	want := map[string]addr{
		"192.168.1.4":       {"eth0", "02:00:00:00:00:10", 24},
		"2001:db8:1::4":     {"eth0", "02:00:00:00:00:10", 64},
		"192.168.100.1":     {"br-lan", "02:00:00:00:00:11", 24},
		"fd7c:8f21:9a03::1": {"br-lan", "02:00:00:00:00:11", 64},
		"2001:db8:100::1":   {"br-lan", "02:00:00:00:00:11", 64},
	}
	seen := map[string]bool{}
	for i := range results {
		r := &results[i]
		if r.Kind != "address" || r.Address == nil {
			continue
		}
		exp, ok := want[r.Address.Address]
		if !ok {
			continue
		}
		seen[r.Address.Address] = true
		if r.Address.Interface != exp.iface {
			t.Errorf("%s: interface = %q, want %q", r.Address.Address, r.Address.Interface, exp.iface)
		}
		if r.Address.MAC != exp.mac {
			t.Errorf("%s: mac = %q, want %q", r.Address.Address, r.Address.MAC, exp.mac)
		}
		if r.Address.PrefixLength != exp.plen {
			t.Errorf("%s: prefix = %d, want %d", r.Address.Address, r.Address.PrefixLength, exp.plen)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("saw %d of %d expected addresses: %v", len(seen), len(want), seen)
	}
}

func TestCollectBridgeMembership(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var members []string
	for i := range results {
		r := &results[i]
		if r.Kind != "link" || r.Link == nil {
			continue
		}
		if r.Link.LinkType != model.EdgeTypeBridgeMembership {
			t.Fatalf("unexpected link type %q", r.Link.LinkType)
		}
		if r.Link.Interface != "br-lan" {
			t.Fatalf("link interface = %q", r.Link.Interface)
		}
		members = append(members, r.Link.Port)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %v", members)
	}
	want := map[string]bool{"eth1": true, "eth2": true, "eth3": true}
	for _, m := range members {
		if !want[m] {
			t.Errorf("unexpected member %q", m)
		}
		delete(want, m)
	}
	if len(want) != 0 {
		t.Errorf("missing members: %v", want)
	}
}

func TestCollectEveryResultIsStamped(t *testing.T) {
	c := newFixtureCollector(t)
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for i, r := range results {
		if r.Source == "" {
			t.Errorf("result %d has no source", i)
		}
		if r.ObservedAt.IsZero() {
			t.Errorf("result %d has no timestamp", i)
		}
	}
}

// A collector with no available source must be unsupported and return nothing
// rather than an error.
func TestCollectWithoutSources(t *testing.T) {
	c := New()
	c.now = time.Now
	if c.Supported() {
		// On a development machine `ip` may exist; skip rather than assert.
		t.Skip("host exposes ip/uci/ubus; cannot test the unsupported path")
	}
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

// A single broken source must fail the collector, but a *missing* source must
// not: this is the difference between "cannot parse ubus" and "no ubus here".
func TestCollectWithBrokenUbus(t *testing.T) {
	dir := t.TempDir()
	broken := dir + "/broken.json"
	if err := writeFile(broken, "{not json"); err != nil {
		t.Fatalf("write: %v", err)
	}
	c := NewWithPaths(
		fixture(t, "uci-network.txt"),
		fixture(t, "ip-link.txt"),
		broken,
		fixture(t, "ubus-network-interface.json"),
	)
	c.now = time.Now
	if _, err := c.Collect(context.Background()); err == nil {
		t.Fatal("expected an error for unparseable ubus JSON")
	}
}

// Only the ubus sources: no UCI, no ip link. Roles then stay unknown, which is
// the honest answer when the configuration is unavailable.
func TestCollectUbusOnly(t *testing.T) {
	c := NewWithPaths("", "", fixture(t, "ubus-network-device.json"), fixture(t, "ubus-network-interface.json"))
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var sawEth0, sawBrLAN bool
	for i := range results {
		o := results[i].Interface
		if o == nil {
			continue
		}
		switch o.Name {
		case "eth0":
			sawEth0 = true
			// The ubus interface dump names the logical interface "wan", so
			// eth0 still gets the WAN role.
			if o.Role != model.InterfaceRoleWAN {
				t.Errorf("eth0 role = %q", o.Role)
			}
		case "br-lan":
			sawBrLAN = true
			if o.Role != model.InterfaceRoleLAN {
				t.Errorf("br-lan role = %q", o.Role)
			}
		}
	}
	if !sawEth0 || !sawBrLAN {
		t.Fatalf("missing interfaces: eth0=%v br-lan=%v", sawEth0, sawBrLAN)
	}
}

// Only ip link: the fallback path. Carrier and type still work, roles do not.
//
// Note the honest limitation: without ubus or UCI the kernel's DOWN state for a
// port with no cable cannot be distinguished from an administratively down
// port, so eth3 is reported as "down" here. The full merge (which has the ubus
// device dump) reports it as "no-carrier", which is what expected.yaml asserts.
func TestCollectIPLinkOnly(t *testing.T) {
	c := NewWithPaths("", fixture(t, "ip-link.txt"), "", "")
	c.now = time.Now
	results, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	byName := map[string]model.OperState{}
	carrier := map[string]bool{}
	for i := range results {
		o := results[i].Interface
		if o == nil {
			continue
		}
		byName[o.Name] = o.OperState
		carrier[o.Name] = o.Carrier
	}
	if byName["eth3"] != model.OperDown || carrier["eth3"] {
		t.Fatalf("eth3: oper=%q carrier=%v", byName["eth3"], carrier["eth3"])
	}
	if byName["eth0"] != model.OperUp || !carrier["eth0"] {
		t.Fatalf("eth0: oper=%q carrier=%v", byName["eth0"], carrier["eth0"])
	}
	// br-lan borrows eth1's MAC in the fixture; it must still be a bridge.
	if byName["br-lan"] == "" {
		t.Fatal("br-lan missing")
	}
	if _, ok := byName["eth1"]; !ok {
		t.Fatal("eth1 missing")
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// TestUCIShowFormatIsParsed guards the command/parser contract.
//
// readUCI runs `uci show network`, which prints the flat
// "network.lan.ifname='br-lan'" form. An earlier version ran `uci export
// network`, which prints the multi-line config-file syntax instead; ParseUCI
// returned ErrEmptyInput on it, so on a real router the whole interface stage
// failed and no topology edges were ever created. This test pins the flat form
// so the two cannot drift apart again.
func TestUCIShowFormatIsParsed(t *testing.T) {
	// Exactly what `uci show network` prints.
	showOutput := `network.loopback=interface
network.loopback.device='lo'
network.loopback.proto='static'
network.@device[0]=device
network.@device[0].name='br-lan'
network.@device[0].type='bridge'
network.@device[0].ports='eth1' 'eth2' 'eth0'
network.@device[1]=device
network.@device[1].name='eth1'
network.@device[1].macaddr='16:8d:88:77:35:77'
network.lan=interface
network.lan.device='br-lan'
network.lan.proto='static'
network.lan.ipaddr='192.168.100.1/24'
network.wan=interface
network.wan.device='eth0'
network.wan.proto='dhcp'
`
	entries, err := parsers.ParseUCI([]byte(showOutput))
	if err != nil {
		t.Fatalf("ParseUCI rejected `uci show network` output: %v", err)
	}
	sections := parsers.GroupUCI(entries)
	if len(sections) == 0 {
		t.Fatal("no sections parsed from `uci show network` output")
	}

	// The bridge device must expose its ports, which is what drives bridge
	// membership and the LAN port list.
	var sawBridge bool
	for _, s := range sections {
		name, _ := s.Get("name")
		if s.Type == "device" && name == "br-lan" {
			sawBridge = true
			if ports, _ := s.Get("ports"); ports == "" {
				t.Error("br-lan device has no ports; the flat form must keep them")
			}
		}
	}
	if !sawBridge {
		t.Error("br-lan device not found in parsed sections")
	}
}

// TestUCIExportFormatIsNotSilentlyAccepted documents the trap.
//
// `uci export network` uses a different grammar. If readUCI ever runs it again,
// this test makes the failure obvious instead of silently producing zero
// interfaces on a router.
func TestUCIExportFormatIsNotSilentlyAccepted(t *testing.T) {
	exportOutput := `package network

config interface 'loopback'
	option device 'lo'
	option proto 'static'

config device
	option name 'br-lan'
	option type 'bridge'
	list ports 'eth1'
`
	entries, err := parsers.ParseUCI([]byte(exportOutput))
	if err != nil {
		// Rejecting it outright is fine and is what the parser does today.
		return
	}
	// If it is accepted, it must not pretend to have found a usable bridge: the
	// flat "network.brlan.name" keys do not exist in this grammar.
	for _, e := range entries {
		if e.Option == "name" && e.Value == "br-lan" {
			t.Fatalf("ParseUCI accepted `uci export` output as if it were `uci show` output; " +
				"readUCI must run `uci show network`")
		}
	}
}
