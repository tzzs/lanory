package demo

import (
	"testing"

	"github.com/lanory/lanory/internal/topology"
)

// The demo router is the multi-homed case: LAN 192.168.100.1 and WAN
// 192.168.1.4 belong to the same device and must both reach the node.
func TestDemoRouterIsMultiHomed(t *testing.T) {
	s := newTestStore(t)
	if _, err := Load(s, Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	g, err := topology.NewBuilder(s).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Kind != topology.NodeKindDevice || n.Device == nil || !n.Device.IsSelf {
			continue
		}
		sides := map[string]string{}
		for _, a := range n.Addresses {
			sides[a.Address] = string(a.Side)
		}
		if len(n.Addresses) < 2 {
			t.Fatalf("expected at least 2 addresses on the router, got %d: %+v", len(n.Addresses), n.Addresses)
		}
		if sides[RouterLANIPv4] != "lan" {
			t.Errorf("LAN address side = %q, want lan", sides[RouterLANIPv4])
		}
		if sides[RouterWANIPv4] != "wan" {
			t.Errorf("WAN address side = %q, want wan", sides[RouterWANIPv4])
		}
		return
	}
	t.Fatal("no self device node in the demo graph")
}
