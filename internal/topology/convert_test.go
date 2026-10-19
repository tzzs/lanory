package topology

import (
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

func TestGraphToModel(t *testing.T) {
	now := time.Unix(1771000000, 0).UTC()
	dev := &model.Device{ID: "d1", Name: "WindowsPC", State: model.StateOnline}
	g := &Graph{
		GeneratedAt: now,
		Nodes: []Node{
			{
				ID: "dev-d1", Kind: NodeKindDevice, Device: dev,
				State: model.StateOnline, PrimaryIP: "192.168.100.116",
				PrimaryMAC: "b0:82:e2:61:3e:b7", Children: []string{"dev-d2"},
			},
			{
				ID: "seg-br-lan-eth1", Kind: NodeKindSegment, Unresolved: true,
				Reason: "several devices observed behind eth1", Parent: "dev-router",
			},
			{
				ID: "net-br-lan", Kind: NodeKindNetwork,
				Network: &model.NetworkStatus{ID: "br-lan", Name: "br-lan", CIDR: "192.168.100.1/24"},
			},
		},
		Edges: []Edge{
			{
				ID: "edge-1", Source: "dev-router", Target: "dev-d1",
				Type: model.EdgeTypeObservedMACPort, Confidence: 0.7,
				Status: model.EdgeStatusLinkUp, Port: "eth2",
				DiscoverySource: model.SourceBridgeFDB, LastObserved: now,
			},
			{
				ID: "edge-2", Source: "dev-router", Target: "dev-d1",
				Type: model.EdgeTypeUserDefined, Confidence: 1.0,
				UserConfirmed: true, Locked: true,
				Status: model.EdgeStatusLinkUp, Label: "my link",
			},
		},
	}
	layout := []model.TopologyLayout{{ID: "lay-1", NodeID: "dev-d1", X: 10, Y: 20}}

	got := g.ToModel(layout)
	if got.GeneratedAt != now {
		t.Errorf("generated at = %v", got.GeneratedAt)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("nodes = %d", len(got.Nodes))
	}
	if len(got.Edges) != 2 {
		t.Fatalf("edges = %d", len(got.Edges))
	}
	if len(got.Layout) != 1 {
		t.Fatalf("layout = %d", len(got.Layout))
	}

	// A device node carries its device and its display fields.
	n := got.Nodes[0]
	if n.ID != "dev-d1" || n.Kind != "device" {
		t.Errorf("node = %+v", n)
	}
	if n.Device == nil || n.Device.Name != "WindowsPC" {
		t.Errorf("device payload: %+v", n.Device)
	}
	if n.PrimaryIP != "192.168.100.116" || n.PrimaryMAC != "b0:82:e2:61:3e:b7" {
		t.Errorf("display fields: %+v", n)
	}
	if len(n.Children) != 1 || n.Children[0] != "dev-d2" {
		t.Errorf("children = %v", n.Children)
	}
	if n.Unresolved {
		t.Error("a device node must not be marked unresolved")
	}

	// A segment node is unresolved and has no device, but still has a state.
	seg := got.Nodes[1]
	if !seg.Unresolved {
		t.Error("a segment must be unresolved in the payload")
	}
	if seg.Device != nil {
		t.Error("a segment must not carry a device")
	}
	if seg.State != model.StateUnknown {
		t.Errorf("segment state = %q, want unknown", seg.State)
	}

	// A network node carries its network payload.
	net := got.Nodes[2]
	if net.Kind != "network" || net.Network == nil {
		t.Fatalf("network node: %+v", net)
	}
	if net.Network.CIDR != "192.168.100.1/24" {
		t.Errorf("cidr = %q", net.Network.CIDR)
	}

	// The port observation gets its port as the label, because the edge has no
	// explicit label.
	e1 := got.Edges[0]
	if e1.Label != "eth2" {
		t.Errorf("edge 1 label = %q, want eth2", e1.Label)
	}
	if e1.EdgeType != model.EdgeTypeObservedMACPort {
		t.Errorf("edge 1 type = %q", e1.EdgeType)
	}

	// A user label wins over the port.
	e2 := got.Edges[1]
	if e2.Label != "my link" {
		t.Errorf("edge 2 label = %q", e2.Label)
	}
	if !e2.UserConfirmed || !e2.Locked {
		t.Errorf("user/locked flags lost: %+v", e2)
	}
	if e2.Confidence != 1.0 {
		t.Errorf("confidence = %v", e2.Confidence)
	}
}

func TestNilGraphToModel(t *testing.T) {
	var g *Graph
	got := g.ToModel(nil)
	if got == nil {
		t.Fatal("ToModel must never return nil")
	}
	if got.Nodes == nil || got.Edges == nil || got.Layout == nil {
		t.Fatal("empty lists must be non-nil, never null")
	}
	if len(got.Nodes) != 0 {
		t.Fatalf("nodes = %d", len(got.Nodes))
	}
}

func TestLayoutConversion(t *testing.T) {
	placements := []Placement{
		{NodeID: "b", X: 2, Y: 3},
		{NodeID: "a", X: 1, Y: 1, Pinned: true},
	}
	modelLayout := ToModelLayout(placements)
	if len(modelLayout) != 2 {
		t.Fatalf("got %d layouts", len(modelLayout))
	}
	// Sorted by node id for a stable payload.
	if modelLayout[0].NodeID != "a" || modelLayout[1].NodeID != "b" {
		t.Fatalf("not sorted: %+v", modelLayout)
	}
	if !modelLayout[0].Pinned {
		t.Error("pinned flag lost")
	}

	back := FromModelLayout(modelLayout)
	if len(back) != 2 {
		t.Fatalf("got %d placements", len(back))
	}
	if back[0].NodeID != "a" || back[0].X != 1 || !back[0].Pinned {
		t.Errorf("round trip lost data: %+v", back[0])
	}
}

func TestPortLabel(t *testing.T) {
	if got := (Edge{Port: "eth1"}).PortLabel(); got != "eth1" {
		t.Errorf("got %q", got)
	}
	if got := (Edge{Port: "eth1", Label: "uplink"}).PortLabel(); got != "uplink" {
		t.Errorf("got %q", got)
	}
	if got := (Edge{}).PortLabel(); got != "" {
		t.Errorf("got %q", got)
	}
}
