package parsers

import (
	"testing"
)

func TestParseUbusDeviceDump(t *testing.T) {
	dump, err := ParseUbusDeviceDump(fixture(t, "ubus-network-device.json"))
	if err != nil {
		t.Fatalf("ParseUbusDeviceDump: %v", err)
	}
	if len(dump.Devices) != 8 {
		t.Fatalf("expected 8 devices, got %d", len(dump.Devices))
	}

	eth1, ok := dump.Devices["eth1"]
	if !ok {
		t.Fatal("eth1 missing")
	}
	if eth1.Type != "ethernet" || eth1.Speed != 1000 || !eth1.Up || !eth1.Link {
		t.Fatalf("eth1: %+v", eth1)
	}
	if eth1.MAC != "02:00:00:00:00:11" || eth1.MTU != 1500 {
		t.Fatalf("eth1 identity: %+v", eth1)
	}
	if eth1.Stats.RXBytes != 4512345678 || eth1.Stats.TXPackets != 8123455 {
		t.Fatalf("eth1 stats: %+v", eth1.Stats)
	}

	// br-lan reports its three member ports, one of which (eth3) has no
	// carrier. The bridge itself is up.
	br, ok := dump.Devices["br-lan"]
	if !ok {
		t.Fatal("br-lan missing")
	}
	if br.Type != "bridge" || len(br.Members) != 3 {
		t.Fatalf("br-lan: %+v", br)
	}
	if br.Members[0] != "eth1" || br.Members[1] != "eth2" || br.Members[2] != "eth3" {
		t.Fatalf("br-lan members: %v", br.Members)
	}
	// br-lan borrows the MAC of its lowest numbered port, which is why the
	// bridge must never be mistaken for a separate device.
	if br.MAC != "02:00:00:00:00:11" {
		t.Fatalf("br-lan mac: %s", br.MAC)
	}

	// eth3: no link, zero counters.
	eth3, ok := dump.Devices["eth3"]
	if !ok {
		t.Fatal("eth3 missing")
	}
	if eth3.Link || eth3.Speed != 0 {
		t.Fatalf("eth3 must be down: %+v", eth3)
	}

	// Virtual devices are present in the dump and must be recognised.
	if d, ok := dump.Devices["docker0"]; !ok || d.Type != "bridge" {
		t.Fatalf("docker0: %+v", d)
	}
	if d, ok := dump.Devices["dummy0"]; !ok || d.Type != "dummy" {
		t.Fatalf("dummy0: %+v", d)
	}
	if InterfaceTypeFromUbus(dump.Devices["dummy0"].Type) != "virtual" {
		t.Fatal("dummy must map to virtual")
	}
	if InterfaceTypeFromUbus(dump.Devices["lo"].Type) != "loopback" {
		t.Fatal("lo must map to loopback")
	}
}

func TestParseUbusDeviceDumpRejectsGarbage(t *testing.T) {
	if _, err := ParseUbusDeviceDump([]byte("{oops")); err == nil {
		t.Fatal("expected a JSON error")
	}
	if _, err := ParseUbusDeviceDump(nil); err == nil {
		t.Fatal("expected an error for empty input")
	}
}

func TestParseUbusInterfaceDump(t *testing.T) {
	dump, err := ParseUbusInterfaceDump(fixture(t, "ubus-network-interface.json"))
	if err != nil {
		t.Fatalf("ParseUbusInterfaceDump: %v", err)
	}
	if len(dump.Interfaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d", len(dump.Interfaces))
	}

	// The WAN interface carries a different subnet and a default route.
	wan, ok := dump.Interfaces["wan"]
	if !ok {
		t.Fatal("wan missing")
	}
	if wan.Device != "eth0" || wan.L3Device != "eth0" || wan.Proto != "dhcp" {
		t.Fatalf("wan: %+v", wan)
	}
	if len(wan.IPv4) != 1 || wan.IPv4[0].Address != "192.168.1.4" || wan.IPv4[0].Mask != 24 {
		t.Fatalf("wan ipv4: %+v", wan.IPv4)
	}
	if gw := wan.DefaultGateway(); gw != "192.168.1.1" {
		t.Fatalf("wan gateway = %q", gw)
	}
	if got := wan.Subnets(); len(got) != 1 || got[0] != "192.168.1.4/24" {
		t.Fatalf("wan subnets = %v", got)
	}

	// The LAN interface is the bridge, with the fixture's documented CIDR.
	lan, ok := dump.Interfaces["lan"]
	if !ok {
		t.Fatal("lan missing")
	}
	if lan.Device != "br-lan" {
		t.Fatalf("lan device = %q", lan.Device)
	}
	if len(lan.IPv4) != 1 || lan.IPv4[0].Address != "192.168.100.1" || lan.IPv4[0].Mask != 24 {
		t.Fatalf("lan ipv4: %+v", lan.IPv4)
	}
	// The LAN carries one ULA and one global IPv6 address.
	if len(lan.IPv6) != 2 {
		t.Fatalf("lan ipv6: %+v", lan.IPv6)
	}
	if lan.IPv6[0].Address != "fd7c:8f21:9a03::1" {
		t.Fatalf("lan ula = %q", lan.IPv6[0].Address)
	}
	// A LAN interface has no default route.
	if gw := lan.DefaultGateway(); gw != "" {
		t.Fatalf("lan must not have a default gateway, got %q", gw)
	}

	// The loopback interface must be ignorable.
	if lo, ok := dump.Interfaces["loopback"]; !ok || lo.Device != "lo" {
		t.Fatalf("loopback: %+v", lo)
	}
}

func TestParseUbusInterfaceDumpRejectsGarbage(t *testing.T) {
	if _, err := ParseUbusInterfaceDump([]byte("nope")); err == nil {
		t.Fatal("expected a JSON error")
	}
}

func TestCIDR(t *testing.T) {
	if got := CIDR("192.168.100.1", 24); got != "192.168.100.1/24" {
		t.Fatalf("got %q", got)
	}
	if got := CIDR("192.168.100.1", 300); got != "192.168.100.1/32" {
		t.Fatalf("bad mask must fall back to /32, got %q", got)
	}
}
