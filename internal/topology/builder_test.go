package topology

import (
	"testing"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// routerDevice returns the OpenWrt device the fixtures describe: an eth0 WAN
// port, a br-lan bridge with three member ports, and the router's own IPs.
func routerDevice(t *testing.T, id string) model.Device {
	t.Helper()
	return model.Device{
		ID:               id,
		Name:             "EASEPi R1",
		Type:             model.DeviceTypeRouter,
		State:            model.StateOnline,
		IsSelf:           true,
		IsGateway:        true,
		PrimaryMAC:       "02:00:00:00:00:11",
		PrimaryIPv4:      "192.168.100.1",
		DiscoverySources: []model.DiscoverySource{model.SourceOpenWRT, model.SourceBridgeFDB},
	}
}

func routerInterfaces(routerID string) []model.NetworkInterface {
	bd := func(name, mac string, typ model.InterfaceType, role model.InterfaceRole,
		carrier bool, speed int, oper model.OperState, bridge string, physical bool) model.NetworkInterface {
		return model.NetworkInterface{
			ID:         "if-" + name,
			DeviceID:   routerID,
			Name:       name,
			MAC:        mac,
			Type:       typ,
			Role:       role,
			AdminState: model.AdminUp,
			OperState:  oper,
			Carrier:    carrier,
			SpeedMbps:  speed,
			MTU:        1500,
			BridgeID:   bridge,
			IsPhysical: physical,
		}
	}
	return []model.NetworkInterface{
		bd("eth0", "02:00:00:00:00:10", model.InterfaceTypeEthernet, model.InterfaceRoleWAN, true, 100, model.OperUp, "", true),
		bd("eth1", "02:00:00:00:00:11", model.InterfaceTypeEthernet, model.InterfaceRoleLAN, true, 1000, model.OperUp, "br-lan", true),
		bd("eth2", "02:00:00:00:00:12", model.InterfaceTypeEthernet, model.InterfaceRoleLAN, true, 2500, model.OperUp, "br-lan", true),
		bd("eth3", "02:00:00:00:00:13", model.InterfaceTypeEthernet, model.InterfaceRoleLAN, false, 0, model.OperNoCarrier, "br-lan", true),
		bd("br-lan", "02:00:00:00:00:11", model.InterfaceTypeBridge, model.InterfaceRoleLAN, true, 0, model.OperUp, "", false),
	}
}

// A device observed on a bridge port.
func wireDevice(id, mac, ip, name string, typ model.DeviceType) model.Device {
	return model.Device{
		ID:               id,
		Name:             name,
		Type:             typ,
		State:            model.StateOnline,
		PrimaryMAC:       mac,
		PrimaryIPv4:      ip,
		DiscoverySources: []model.DiscoverySource{model.SourceBridgeFDB, model.SourceARP, model.SourceDHCP},
	}
}

// hopedEdge builds an observed_mac_port edge: the router observed the device's
// MAC on the given bridge port.
func macPortEdge(id, routerID, port, bridge string, conf float64) model.TopologyEdge {
	return model.TopologyEdge{
		ID:                id,
		SourceID:          routerID,
		TargetID:          port + "-device-" + id, // replaced by caller
		SourceInterfaceID: "",
		EdgeType:          model.EdgeTypeObservedMACPort,
		DiscoverySource:   model.SourceBridgeFDB,
		Confidence:        conf,
		Status:            model.EdgeStatusLinkUp,
		Unverified:        conf < 0.7,
		LastObserved:      time.Now(),
	}
}

func nodeByID(g *Graph, id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func nodeByMAC(g *Graph, mac string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].PrimaryMAC == mac && g.Nodes[i].Kind == NodeKindDevice {
			return &g.Nodes[i]
		}
	}
	return nil
}

func TestBuildDeviceNodes(t *testing.T) {
	const router = "router-1"
	store := &fakeStore{
		devices: []model.Device{
			routerDevice(t, router),
			wireDevice("d-win", "b0:82:e2:61:3e:b7", "192.168.100.116", "WindowsPC", model.DeviceTypeDesktop),
			wireDevice("d-iphone", "0c:6f:8b:4a:3d:d2", "192.168.100.151", "iPhone", model.DeviceTypeSmartphone),
			wireDevice("d-mac", "86:7b:1c:3f:06:5e", "192.168.100.222", "Mac-Studio", model.DeviceTypeDesktop),
		},
		interfaces: routerInterfaces(router),
		addresses:  map[string][]model.DeviceAddress{},
	}

	g, err := NewBuilder(store).WithOptions(Options{UnverifiedEdgeThreshold: 0.6}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Four devices plus one network node (br-lan).
	var devices, networks int
	for _, n := range g.Nodes {
		switch n.Kind {
		case NodeKindDevice:
			devices++
		case NodeKindNetwork:
			networks++
		}
	}
	if devices != 4 {
		t.Fatalf("expected 4 device nodes, got %d", devices)
	}
	// br-lan and eth0 (which carries the WAN address) become network nodes.
	if networks < 1 {
		t.Fatalf("expected at least 1 network node, got %d", networks)
	}

	if n := nodeByID(g, deviceNodeID(router)); n == nil {
		t.Fatal("router node missing")
	} else if n.Device == nil || !n.Device.IsSelf {
		t.Fatal("router must be flagged is_self")
	}

	win := nodeByMAC(g, "b0:82:e2:61:3e:b7")
	if win == nil {
		t.Fatal("Windows PC node missing")
	}
	if win.PrimaryIP != "192.168.100.116" {
		t.Errorf("primary ip = %q", win.PrimaryIP)
	}
	if win.State != model.StateOnline {
		t.Errorf("state = %q", win.State)
	}
}

// Hidden devices must not appear.
func TestBuildExcludesHiddenDevices(t *testing.T) {
	const router = "router-1"
	store := &fakeStore{
		devices: []model.Device{
			routerDevice(t, router),
			func() model.Device {
				d := wireDevice("d-hidden", "aa:aa:aa:aa:aa:aa", "192.168.100.99", "Hidden", model.DeviceTypeIoT)
				d.IsHidden = true
				return d
			}(),
		},
		interfaces: routerInterfaces(router),
	}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if nodeByMAC(g, "aa:aa:aa:aa:aa:aa") != nil {
		t.Error("a hidden device must not become a node")
	}
}

// The core rule: a device observed on a port with no known intermediate goes
// under an unresolved segment, NOT under an invented switch.
func TestUnresolvedSegmentNotInventedSwitch(t *testing.T) {
	const router = "router-1"
	now := time.Now()

	// Eight devices all learned on eth1. Nothing among them is a switch.
	devices := []model.Device{routerDevice(t, router)}
	macs := []string{
		"0c:6f:8b:4a:3d:d2", "86:7b:1c:3f:06:5e", "46:3f:36:89:42:e3",
		"52:e5:8f:5b:c5:2d", "04:67:61:77:7f:24", "cc:b5:d1:05:70:20",
		"b4:fa:48:10:16:13", "02:00:00:00:01:01",
	}
	var edges []model.TopologyEdge
	for i, mac := range macs {
		id := "d-" + mac
		devices = append(devices, wireDevice(id, mac, "192.168.100."+string(rune('2'+i)), "dev-"+mac, model.DeviceTypeUnknown))
		edges = append(edges, model.TopologyEdge{
			ID:              "e-" + mac,
			SourceID:        router,
			TargetID:        id,
			EdgeType:        model.EdgeTypeObservedMACPort,
			DiscoverySource: model.SourceBridgeFDB,
			Confidence:      0.7,
			Status:          model.EdgeStatusLinkUp,
			Label:           "eth1",
			LastObserved:    now,
		})
	}

	store := &fakeStore{
		devices:    devices,
		interfaces: routerInterfaces(router),
		edges:      edges,
	}
	g, err := NewBuilder(store).WithOptions(Options{UnverifiedEdgeThreshold: 0.6}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Exactly one segment node, named after the port.
	var segments []Node
	for _, n := range g.Nodes {
		if n.Kind == NodeKindSegment {
			segments = append(segments, n)
		}
	}
	if len(segments) != 1 {
		t.Fatalf("expected exactly 1 unresolved segment, got %d: %+v", len(segments), segments)
	}
	if !segments[0].Unverified {
		t.Error("a segment must always be marked unverified")
	}
	if segments[0].StateReason == "" {
		t.Error("a segment must explain itself")
	}
	if nodeByID(g, segmentNodeID("br-lan", "eth1")) == nil {
		t.Fatalf("expected the segment id %s", segmentNodeID("br-lan", "eth1"))
	}

	// Every device hangs under the segment.
	for _, mac := range macs {
		n := nodeByMAC(g, mac)
		if n == nil {
			t.Fatalf("device %s missing", mac)
		}
		if n.Parent != segmentNodeID("br-lan", "eth1") {
			t.Errorf("%s: parent = %q, want the segment", mac, n.Parent)
		}
		if !n.Unverified {
			t.Errorf("%s: must be marked unverified behind a segment", mac)
		}
	}

	// And there must be no switch or AP node invented to host them.
	for _, n := range g.Nodes {
		if n.Device == nil {
			continue
		}
		switch n.Device.Type {
		case model.DeviceTypeSwitch, model.DeviceTypeWirelessAP, model.DeviceTypeMeshNode:
			if n.Device.ID != "dev-router-1" {
				t.Fatal("the engine invented a switch or access point")
			}
		}
	}
}

// When a known switch is among the devices on the port, the fan-out is attributed
// to it instead of a segment.
func TestKnownSwitchExplainsFanOut(t *testing.T) {
	const router = "router-1"
	now := time.Now()

	devices := []model.Device{routerDevice(t, router)}
	var edges []model.TopologyEdge

	// A switch, seen on eth1, and three devices behind it.
	sw := model.Device{
		ID:         "d-switch",
		Name:       "Living room switch",
		Type:       model.DeviceTypeSwitch,
		State:      model.StateOnline,
		PrimaryMAC: "aa:bb:cc:00:00:01",
		IsManual:   true,
	}
	devices = append(devices, sw)
	edges = append(edges, model.TopologyEdge{
		ID:              "e-switch",
		SourceID:        router,
		TargetID:        "d-switch",
		EdgeType:        model.EdgeTypeObservedMACPort,
		DiscoverySource: model.SourceBridgeFDB,
		Confidence:      0.7,
		Status:          model.EdgeStatusLinkUp,
		LastObserved:    now,
	})
	for i, mac := range []string{"aa:bb:cc:00:00:11", "aa:bb:cc:00:00:12", "aa:bb:cc:00:00:13"} {
		id := "d-" + mac
		devices = append(devices, wireDevice(id, mac, "192.168.100."+string(rune('5'+i)), "dev-"+mac, model.DeviceTypeIoT))
		edges = append(edges, model.TopologyEdge{
			ID:              "e-" + mac,
			SourceID:        router,
			TargetID:        id,
			EdgeType:        model.EdgeTypeObservedMACPort,
			DiscoverySource: model.SourceBridgeFDB,
			Confidence:      0.7,
			Status:          model.EdgeStatusLinkUp,
			Label:           "eth1",
			LastObserved:    now,
		})
	}

	store := &fakeStore{devices: devices, interfaces: routerInterfaces(router), edges: edges}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, n := range g.Nodes {
		if n.Kind == NodeKindSegment {
			t.Fatalf("no segment should exist when the switch is known: %+v", n)
		}
	}
}

// A single device behind a port is attached directly to the port owner: no
// segment is created for it.
func TestSingleDeviceNoSegment(t *testing.T) {
	const router = "router-1"
	now := time.Now()

	store := &fakeStore{
		devices: []model.Device{
			routerDevice(t, router),
			wireDevice("d-win", "b0:82:e2:61:3e:b7", "192.168.100.116", "WindowsPC", model.DeviceTypeDesktop),
		},
		interfaces: routerInterfaces(router),
		edges: []model.TopologyEdge{{
			ID:              "e-win",
			SourceID:        router,
			TargetID:        "d-win",
			EdgeType:        model.EdgeTypeObservedMACPort,
			DiscoverySource: model.SourceBridgeFDB,
			Confidence:      0.7,
			Status:          model.EdgeStatusLinkUp,
			Label:           "eth2",
			LastObserved:    now,
		}},
	}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Kind == NodeKindSegment {
			t.Fatalf("no segment for a single device: %+v", n)
		}
	}
	win := nodeByMAC(g, "b0:82:e2:61:3e:b7")
	if win == nil {
		t.Fatal("Windows PC missing")
	}
	if win.Parent != deviceNodeID(router) {
		t.Errorf("parent = %q, want the router", win.Parent)
	}
	if win.Unverified {
		t.Error("a 0.70 confidence observation is at the threshold and should be verified")
	}
}

// A low-confidence observation must be marked unverified so it is drawn dashed.
func TestLowConfidenceIsUnverified(t *testing.T) {
	const router = "router-1"
	store := &fakeStore{
		devices: []model.Device{
			routerDevice(t, router),
			wireDevice("d-x", "aa:bb:cc:dd:ee:ff", "192.168.100.55", "mystery", model.DeviceTypeUnknown),
		},
		interfaces: routerInterfaces(router),
		edges: []model.TopologyEdge{{
			ID:              "e-x",
			SourceID:        router,
			TargetID:        "d-x",
			EdgeType:        model.EdgeTypeObservedMACPort,
			DiscoverySource: model.SourceMDNS,
			Confidence:      0.3,
			Status:          model.EdgeStatusUnverified,
			Label:           "eth1",
			LastObserved:    time.Now(),
		}},
	}
	g, err := NewBuilder(store).WithOptions(Options{UnverifiedEdgeThreshold: 0.6}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n := nodeByMAC(g, "aa:bb:cc:dd:ee:ff")
	if n == nil {
		t.Fatal("device node missing")
	}
	if !n.Unverified {
		t.Error("a 0.30 confidence observation must be unverified")
	}
	// The edge must be unverified too.
	var found bool
	for _, e := range g.Edges {
		if e.Type == model.EdgeTypeObservedMACPort {
			found = true
			if !e.Unverified {
				t.Error("the edge must be marked unverified")
			}
		}
	}
	if !found {
		t.Error("expected an observed_mac_port edge")
	}
}

// A user-confirmed edge wins over the computed placement and is never
// downgraded.
func TestUserConfirmedEdgeWins(t *testing.T) {
	const router = "router-1"
	store := &fakeStore{
		devices: []model.Device{
			routerDevice(t, router),
			wireDevice("d-a", "aa:aa:aa:aa:aa:aa", "192.168.100.10", "A", model.DeviceTypeUnknown),
			wireDevice("d-b", "bb:bb:bb:bb:bb:bb", "192.168.100.11", "B", model.DeviceTypeUnknown),
		},
		interfaces: routerInterfaces(router),
		edges: []model.TopologyEdge{{
			ID:              "e-user",
			SourceID:        "d-a",
			TargetID:        "d-b",
			EdgeType:        model.EdgeTypeUserDefined,
			DiscoverySource: model.SourceManual,
			Confidence:      1.0,
			UserConfirmed:   true,
			Status:          model.EdgeStatusLinkUp,
			LastObserved:    time.Now(),
		}},
	}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b := nodeByMAC(g, "bb:bb:bb:bb:bb:bb")
	if b == nil {
		t.Fatal("device B missing")
	}
	if b.Parent != deviceNodeID("d-a") {
		t.Errorf("B parent = %q, want the user-confirmed parent", b.Parent)
	}
	if b.Unverified {
		t.Error("a user-confirmed relation is never unverified")
	}
	if b.Reason == "" {
		t.Error("the reason must say the user confirmed it")
	}
}

// An edge that points at a hidden or vanished device must not be drawn.
func TestEdgesToMissingDevicesAreDropped(t *testing.T) {
	const router = "router-1"
	store := &fakeStore{
		devices:    []model.Device{routerDevice(t, router)},
		interfaces: routerInterfaces(router),
		edges: []model.TopologyEdge{{
			ID:           "e-gone",
			SourceID:     router,
			TargetID:     "dev-does-not-exist",
			EdgeType:     model.EdgeTypeObservedMACPort,
			Confidence:   0.9,
			Status:       model.EdgeStatusLinkUp,
			LastObserved: time.Now(),
		}},
	}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Edges) != 0 {
		t.Fatalf("expected the dangling edge to be dropped, got %d", len(g.Edges))
	}
}

func TestEmptyStore(t *testing.T) {
	g, err := NewBuilder(&fakeStore{}).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Fatalf("expected an empty graph, got %d nodes %d edges", len(g.Nodes), len(g.Edges))
	}
}

// The bridge interface becomes a network node carrying its subnet.
func TestNetworkNodeForBridge(t *testing.T) {
	const router = "router-1"
	interfaces := routerInterfaces(router)
	// Attach the LAN address to br-lan.
	for i := range interfaces {
		if interfaces[i].Name == "br-lan" {
			interfaces[i].Addresses = []model.DeviceAddress{{
				Address:      "192.168.100.1",
				Family:       model.AddressFamilyIPv4,
				PrefixLength: 24,
				InterfaceID:  interfaces[i].ID,
			}}
		}
		if interfaces[i].Name == "eth0" {
			interfaces[i].Addresses = []model.DeviceAddress{{
				Address:      "192.168.1.4",
				Family:       model.AddressFamilyIPv4,
				PrefixLength: 24,
				InterfaceID:  interfaces[i].ID,
			}}
		}
	}
	store := &fakeStore{
		devices:    []model.Device{routerDevice(t, router)},
		interfaces: interfaces,
	}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	net := nodeByID(g, networkNodeID("br-lan"))
	if net == nil {
		t.Fatalf("expected a br-lan network node, got %v", g.Nodes)
	}
	if net.Kind != NodeKindNetwork {
		t.Fatalf("br-lan node kind = %q", net.Kind)
	}
	if net.Network == nil {
		t.Fatal("the network payload is missing")
	}
	if net.Network.CIDR != "192.168.100.1/24" {
		t.Errorf("cidr = %q", net.Network.CIDR)
	}
	if net.Network.GatewayIP != "192.168.100.1" {
		t.Errorf("gateway = %q", net.Network.GatewayIP)
	}
}

func TestDepthAndChildren(t *testing.T) {
	g := &Graph{Nodes: []Node{
		{ID: "a", Kind: NodeKindDevice},
		{ID: "b", Kind: NodeKindDevice, Parent: "a"},
		{ID: "c", Kind: NodeKindDevice, Parent: "b"},
		{ID: "d", Kind: NodeKindDevice, Parent: "b"},
	}}
	PopulateChildren(g)

	if got := len(nodeByID(g, "a").Children); got != 1 {
		t.Errorf("a children = %d, want 1", got)
	}
	if got := len(nodeByID(g, "b").Children); got != 2 {
		t.Errorf("b children = %d, want 2", got)
	}
	if got := Depth(g, "c"); got != 2 {
		t.Errorf("depth(c) = %d, want 2", got)
	}
	if got := Depth(g, "a"); got != 0 {
		t.Errorf("depth(a) = %d, want 0", got)
	}
	if got := Depth(g, "missing"); got != 0 {
		t.Errorf("depth(missing) = %d, want 0", got)
	}
	// A cycle must not hang.
	cyclic := &Graph{Nodes: []Node{{ID: "a", Parent: "b"}, {ID: "b", Parent: "a"}}}
	if got := Depth(cyclic, "a"); got != -1 {
		t.Errorf("depth(cycle) = %d, want -1", got)
	}
}

func TestSortNodes(t *testing.T) {
	nodes := []Node{
		{ID: "dev-2", Kind: NodeKindDevice},
		{ID: "seg-x", Kind: NodeKindSegment},
		{ID: "net-y", Kind: NodeKindNetwork},
		{ID: "dev-1", Kind: NodeKindDevice},
	}
	SortNodes(nodes)
	want := []string{"net-y", "dev-1", "dev-2", "seg-x"}
	for i, w := range want {
		if nodes[i].ID != w {
			t.Fatalf("order = %v, want %v", []string{nodes[0].ID, nodes[1].ID, nodes[2].ID, nodes[3].ID}, want)
		}
	}
}

func TestComputeLevels(t *testing.T) {
	g := &Graph{Nodes: []Node{
		{ID: "a"},
		{ID: "b", Parent: "a"},
		{ID: "c", Parent: "b"},
	}}
	levels := computeLevels(g)
	if levels["a"] != 0 {
		t.Errorf("a level = %d", levels["a"])
	}
	if levels["b"] != 1 {
		t.Errorf("b level = %d", levels["b"])
	}
	if levels["c"] != 2 {
		t.Errorf("c level = %d", levels["c"])
	}
}

func TestDefaultOptions(t *testing.T) {
	o := DefaultOptions()
	if o.UnverifiedEdgeThreshold != 0.6 {
		t.Errorf("threshold = %v", o.UnverifiedEdgeThreshold)
	}
}
