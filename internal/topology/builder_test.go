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
			wireDevice("d-win", "02:11:22:33:44:b7", "192.0.2.116", "client-desktop", model.DeviceTypeDesktop),
			wireDevice("d-iphone", "06:77:88:99:aa:d2", "192.0.2.151", "iPhone", model.DeviceTypeSmartphone),
			wireDevice("d-mac", "07:88:99:aa:bb:5e", "192.0.2.222", "client-mac", model.DeviceTypeDesktop),
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

	win := nodeByMAC(g, "02:11:22:33:44:b7")
	if win == nil {
		t.Fatal("Windows PC node missing")
	}
	if win.PrimaryIP != "192.0.2.116" {
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
		"06:77:88:99:aa:d2", "07:88:99:aa:bb:5e", "04:66:77:88:99:e3",
		"05:22:33:44:55:2d", "04:11:22:33:44:24", "cc:11:22:33:44:20",
		"b4:11:22:33:44:13", "02:00:00:00:01:01",
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

	// A switch the bridge learned on eth1, and three devices behind it. The
	// Label is the port evidence: without it the switch's location is unknown
	// and it must not be used to explain a fan-out.
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
		Label:           "eth1",
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
			wireDevice("d-win", "02:11:22:33:44:b7", "192.0.2.116", "client-desktop", model.DeviceTypeDesktop),
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
	win := nodeByMAC(g, "02:11:22:33:44:b7")
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

// A router is multi-homed: the same device owns the LAN gateway address and the
// address it was given on the WAN side. Both must reach the node, tagged with the
// interface and side that carry them, and each must report the subnet it lives
// in so a consumer can group them.
func TestMultiHomedRouterAddresses(t *testing.T) {
	const router = "router-1"
	interfaces := routerInterfaces(router)
	for i := range interfaces {
		switch interfaces[i].Name {
		case "br-lan":
			interfaces[i].Addresses = []model.DeviceAddress{{
				Address:      "192.168.100.1",
				Family:       model.AddressFamilyIPv4,
				PrefixLength: 24,
				InterfaceID:  interfaces[i].ID,
			}}
		case "eth0":
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

	n := nodeByID(g, deviceNodeID(router))
	if n == nil {
		t.Fatal("router node missing")
	}

	byAddr := map[string]model.NodeAddress{}
	for _, a := range n.Addresses {
		byAddr[a.Address] = a
	}

	lan, ok := byAddr["192.168.100.1"]
	if !ok {
		t.Fatalf("LAN address missing from %+v", n.Addresses)
	}
	if lan.Side != model.InterfaceRoleLAN {
		t.Errorf("LAN address side = %q, want lan", lan.Side)
	}
	if lan.Interface != "br-lan" {
		t.Errorf("LAN address interface = %q, want br-lan", lan.Interface)
	}
	if lan.CIDR != "192.168.100.0/24" {
		t.Errorf("LAN address cidr = %q, want 192.168.100.0/24", lan.CIDR)
	}

	wan, ok := byAddr["192.168.1.4"]
	if !ok {
		t.Fatalf("WAN address missing from %+v", n.Addresses)
	}
	if wan.Side != model.InterfaceRoleWAN {
		t.Errorf("WAN address side = %q, want wan", wan.Side)
	}
	if wan.Interface != "eth0" {
		t.Errorf("WAN address interface = %q, want eth0", wan.Interface)
	}
	if wan.CIDR != "192.168.1.0/24" {
		t.Errorf("WAN address cidr = %q, want 192.168.1.0/24", wan.CIDR)
	}

	// The two addresses belong to different networks: that is the whole point
	// of the side tag, and it is what makes them groupable.
	if lan.CIDR == wan.CIDR {
		t.Error("the LAN and WAN addresses must report distinct subnets")
	}

	// Exactly one address is the primary, and it matches PrimaryIP.
	var primaries []string
	for _, a := range n.Addresses {
		if a.IsPrimary {
			primaries = append(primaries, a.Address)
		}
	}
	if len(primaries) != 1 || primaries[0] != n.PrimaryIP {
		t.Errorf("primaries = %v, primary ip = %q", primaries, n.PrimaryIP)
	}
}

// Two devices on the same subnet report the same CIDR, so the UI can group them;
// a device on another subnet reports a different one.
func TestAddressesGroupBySubnet(t *testing.T) {
	const router = "router-1"
	store := &fakeStore{
		devices: []model.Device{
			routerDevice(t, router),
			wireDevice("d-a", "02:11:22:33:44:a1", "192.168.100.20", "a", model.DeviceTypeDesktop),
			wireDevice("d-b", "02:11:22:33:44:a2", "192.168.100.21", "b", model.DeviceTypeLaptop),
			wireDevice("d-c", "02:11:22:33:44:a3", "192.168.1.77", "c", model.DeviceTypeIoT),
		},
		interfaces: routerInterfaces(router),
		addresses: map[string][]model.DeviceAddress{
			"d-a": {{Address: "192.168.100.20", Family: model.AddressFamilyIPv4, PrefixLength: 24}},
			"d-b": {{Address: "192.168.100.21", Family: model.AddressFamilyIPv4, PrefixLength: 24}},
			"d-c": {{Address: "192.168.1.77", Family: model.AddressFamilyIPv4, PrefixLength: 24}},
		},
	}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	cidrOfDevice := func(id string) string {
		n := nodeByID(g, deviceNodeID(id))
		if n == nil || len(n.Addresses) == 0 {
			t.Fatalf("no addresses for %s", id)
		}
		return n.Addresses[0].CIDR
	}

	if a, b := cidrOfDevice("d-a"), cidrOfDevice("d-b"); a != b || a != "192.168.100.0/24" {
		t.Errorf("same-subnet devices disagree: %q vs %q", a, b)
	}
	if c := cidrOfDevice("d-c"); c != "192.168.1.0/24" {
		t.Errorf("d-c cidr = %q, want 192.168.1.0/24", c)
	}
	// A remote device address is not tied to a local interface, so it has no
	// side. Claiming one would be a guess.
	if side := nodeByID(g, deviceNodeID("d-a")).Addresses[0].Side; side != model.InterfaceRoleUnknown {
		t.Errorf("remote address side = %q, want unknown", side)
	}
}

// Link-local IPv6 addresses are per-interface plumbing and must not be listed.
func TestLinkLocalAddressesAreSkipped(t *testing.T) {
	const router = "router-1"
	interfaces := routerInterfaces(router)
	for i := range interfaces {
		if interfaces[i].Name == "br-lan" {
			interfaces[i].Addresses = []model.DeviceAddress{
				{Address: "192.168.100.1", Family: model.AddressFamilyIPv4, PrefixLength: 24, InterfaceID: interfaces[i].ID},
				{Address: "fe80::1", Family: model.AddressFamilyIPv6, PrefixLength: 64, InterfaceID: interfaces[i].ID},
			}
		}
	}
	store := &fakeStore{devices: []model.Device{routerDevice(t, router)}, interfaces: interfaces}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, a := range nodeByID(g, deviceNodeID(router)).Addresses {
		if isLinkLocal(a.Address) {
			t.Errorf("link-local address %q must not be listed", a.Address)
		}
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

// TestSwitchOnDifferentPortDoesNotExplain is the regression test for a real
// fabrication bug: the engine used to search the whole graph for "the only
// switch-like device" and attribute one port's fan-out to it, even when that
// switch was plugged into a different port.
//
// A switch known to be on eth2 says nothing about what is behind eth1. Using it
// draws a cable that does not exist, which is exactly the class of invention the
// unresolved segment exists to prevent.
func TestSwitchOnDifferentPortDoesNotExplain(t *testing.T) {
	const router = "router-1"
	now := time.Now()

	devices := []model.Device{routerDevice(t, router)}
	var edges []model.TopologyEdge

	// The switch is on eth2.
	devices = append(devices, model.Device{
		ID: "d-switch", Name: "Switch on eth2", Type: model.DeviceTypeSwitch,
		State: model.StateOnline, PrimaryMAC: "aa:00:00:00:00:01",
	})
	edges = append(edges, model.TopologyEdge{
		ID: "e-switch", SourceID: router, TargetID: "d-switch",
		EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
		Confidence: 0.7, Status: model.EdgeStatusLinkUp, Label: "eth2", LastObserved: now,
	})

	// An unrelated fan-out on eth1.
	var eth1IDs []string
	for i, mac := range []string{"bb:00:00:00:00:01", "bb:00:00:00:00:02", "bb:00:00:00:00:03"} {
		id := "d-eth1-" + mac[len(mac)-2:]
		eth1IDs = append(eth1IDs, id)
		devices = append(devices, wireDevice(id, mac, "192.168.100."+string(rune('6'+i)), id, model.DeviceTypeUnknown))
		edges = append(edges, model.TopologyEdge{
			ID: "e-" + id, SourceID: router, TargetID: id,
			EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
			Confidence: 0.7, Status: model.EdgeStatusLinkUp, Label: "eth1", LastObserved: now,
		})
	}

	store := &fakeStore{devices: devices, interfaces: routerInterfaces(router), edges: edges}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The eth1 devices must hang under a segment, not under the eth2 switch.
	var segments int
	for _, n := range g.Nodes {
		if n.Kind == NodeKindSegment {
			segments++
			if n.Port != "eth1" {
				t.Errorf("segment on unexpected port %q", n.Port)
			}
		}
	}
	if segments != 1 {
		t.Fatalf("expected 1 segment for eth1, got %d", segments)
	}
	for _, id := range eth1IDs {
		n := nodeByID(g, deviceNodeID(id))
		if n == nil {
			t.Fatalf("%s missing", id)
		}
		if n.Parent == deviceNodeID("d-switch") {
			t.Fatalf("%s was attributed to the switch on eth2", id)
		}
		if n.Parent != segmentNodeID("br-lan", "eth1") {
			t.Errorf("%s parent = %q, want the eth1 segment", id, n.Parent)
		}
	}

	// The switch itself stays where its own evidence puts it: eth2.
	if sw := nodeByID(g, deviceNodeID("d-switch")); sw == nil {
		t.Fatal("switch node missing")
	} else if sw.Parent != deviceNodeID(router) {
		t.Errorf("switch parent = %q, want the router", sw.Parent)
	}
}

// TestSwitchWithoutPortEvidenceDoesNotExplain covers a switch that the engine
// knows about but cannot place: no port observation names where it sits. Without
// that evidence it must not explain a fan-out anywhere.
func TestSwitchWithoutPortEvidenceDoesNotExplain(t *testing.T) {
	const router = "router-1"
	now := time.Now()

	devices := []model.Device{
		routerDevice(t, router),
		{
			ID: "d-switch", Name: "Unplaced switch", Type: model.DeviceTypeSwitch,
			State: model.StateUnknown, PrimaryMAC: "aa:00:00:00:00:09", IsManual: true,
		},
	}
	var edges []model.TopologyEdge
	var ids []string
	for i, mac := range []string{"cc:00:00:00:00:01", "cc:00:00:00:00:02"} {
		id := "d-a-" + mac[len(mac)-2:]
		ids = append(ids, id)
		devices = append(devices, wireDevice(id, mac, "192.168.100."+string(rune('7'+i)), id, model.DeviceTypeUnknown))
		edges = append(edges, model.TopologyEdge{
			ID: "e-" + id, SourceID: router, TargetID: id,
			EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
			Confidence: 0.7, Status: model.EdgeStatusLinkUp, Label: "eth1", LastObserved: now,
		})
	}

	store := &fakeStore{devices: devices, interfaces: routerInterfaces(router), edges: edges}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, id := range ids {
		n := nodeByID(g, deviceNodeID(id))
		if n == nil {
			t.Fatalf("%s missing", id)
		}
		if n.Parent == deviceNodeID("d-switch") {
			t.Errorf("%s was attributed to a switch with no port evidence", id)
		}
	}
	var segments int
	for _, n := range g.Nodes {
		if n.Kind == NodeKindSegment {
			segments++
		}
	}
	if segments != 1 {
		t.Errorf("expected 1 segment, got %d", segments)
	}
}

// TestTwoSwitchesOnOnePortDoNotGuess pins the "cannot tell which one" rule: two
// switch-like devices on the same port mean the engine does not know which hosts
// the others, so it must not pick one at random.
func TestTwoSwitchesOnOnePortDoNotGuess(t *testing.T) {
	const router = "router-1"
	now := time.Now()

	devices := []model.Device{routerDevice(t, router)}
	var edges []model.TopologyEdge
	var leaves []string

	for i, mac := range []string{"dd:00:00:00:00:01", "dd:00:00:00:00:02"} {
		id := "d-sw-" + mac[len(mac)-2:]
		devices = append(devices, model.Device{
			ID: id, Name: id, Type: model.DeviceTypeSwitch, State: model.StateOnline,
			PrimaryMAC: mac, IsManual: true,
		})
		edges = append(edges, model.TopologyEdge{
			ID: "e-" + id, SourceID: router, TargetID: id,
			EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
			Confidence: 0.7, Status: model.EdgeStatusLinkUp, Label: "eth1", LastObserved: now,
		})
		_ = i
	}
	for _, mac := range []string{"dd:00:00:00:00:11", "dd:00:00:00:00:12"} {
		id := "d-leaf-" + mac[len(mac)-2:]
		leaves = append(leaves, id)
		devices = append(devices, wireDevice(id, mac, "192.168.100.90", id, model.DeviceTypeUnknown))
		edges = append(edges, model.TopologyEdge{
			ID: "e-" + id, SourceID: router, TargetID: id,
			EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
			Confidence: 0.7, Status: model.EdgeStatusLinkUp, Label: "eth1", LastObserved: now,
		})
	}

	store := &fakeStore{devices: devices, interfaces: routerInterfaces(router), edges: edges}
	g, err := NewBuilder(store).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// With two candidate switches the engine cannot substantiate either claim,
	// so the leaves stay unplaced-behind-a-segment rather than being guessed.
	for _, id := range leaves {
		n := nodeByID(g, deviceNodeID(id))
		if n == nil {
			t.Fatalf("%s missing", id)
		}
		if n.Parent == deviceNodeID("d-sw-01") || n.Parent == deviceNodeID("d-sw-02") {
			t.Errorf("%s was attributed to one of two indistinguishable switches", id)
		}
	}
}
