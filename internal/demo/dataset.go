package demo

import "time"

// Sample network constants.
//
// Every address below comes from fixtures/easepi-r1 (the sanitized copy of the
// reference network) and belongs to a reserved documentation range:
//
//	IPv4: 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 (RFC 5737)
//	IPv6: 2001:db8::/32 (RFC 3849)
//	MAC : locally administered (second-least-significant bit of the first
//	      octet set), so the addresses cannot collide with real hardware.
const (
	// RouterWANIPv4 is the WAN address of the reference router.
	RouterWANIPv4 = "192.168.1.4"
	// RouterWANPrefix is the WAN prefix length.
	RouterWANPrefix = 24
	// RouterWANGateway is the upstream gateway on the WAN side.
	RouterWANGateway = "192.168.1.1"
	// RouterWANIPv6 is the WAN IPv6 address of the reference router.
	RouterWANIPv6 = "2001:db8:1::4"

	// RouterLANIPv4 is the LAN address of the reference router (the bridge).
	RouterLANIPv4 = "192.168.100.1"
	// RouterLANPrefix is the LAN prefix length.
	RouterLANPrefix = 24
	// RouterLANIPv6ULA is the unique-local LAN address of the bridge.
	RouterLANIPv6ULA = "fd7c:8f21:9a03::1"
	// RouterLANIPv6Global is the global LAN address of the bridge.
	RouterLANIPv6Global = "2001:db8:100::1"

	// RouterMAC is the MAC of the router's first LAN port, which the bridge
	// inherits. It is a locally administered address.
	RouterMAC = "02:00:00:00:00:10"

	// UpstreamName is the display name of the upstream router node.
	UpstreamName = "Upstream Router"
	// UpstreamIPv4 is the address of the upstream router as seen by the local
	// router: the WAN gateway. It sits on a different subnet from the LAN
	// address of the local router, so the two never collide.
	UpstreamIPv4 = RouterWANGateway
	// UpstreamMAC is a locally administered MAC for the upstream node.
	UpstreamMAC = "03:11:22:33:44:55"

	// ModemName is the display name of the ISP optical modem node.
	ModemName = "ISP Optical Modem"
	// ModemIPv4 is the upstream-side address of the optical modem. The fixture
	// does not capture the modem (it is not on the LAN side of the router), so
	// this node is created from the user's description and is flagged manual.
	ModemIPv4 = "192.168.1.2"
	// ModemMAC is a locally administered MAC for the optical modem.
	ModemMAC = "04:22:33:44:55:66"

	// XiaomiName is the display name of the secondary router behind eth1.
	XiaomiName = "Xiaomi Router"
	// XiaomiIPv4 is the management address of the secondary router.
	//
	// The sanitized fixture never observed the Xiaomi router's own address: its
	// clients appear in the FDB on eth1, but the router itself is transparent
	// at layer 2. This address is therefore a documented inference, marked as
	// such in the device description, not a captured observation.
	XiaomiIPv4 = "192.168.100.2"
	// XiaomiMAC is a locally administered MAC for the secondary router.
	XiaomiMAC = "05:33:44:55:66:77"

	// DeviceMACIPv6Only is the MAC of the device that was only ever observed
	// through IPv6 neighbour discovery.
	DeviceMACIPv6Only = "b4:11:22:33:44:13"
	// DeviceIPv6Only is that device's link-local address, exactly as captured
	// in fixtures/easepi-r1/ip-neigh6.txt.
	DeviceIPv6Only = "fe80::1a2b:3c4d:5e6f:7012"
)

// Interface speed and role values of the reference network, from
// fixtures/easepi-r1/expected.yaml.
const (
	// SpeedWANMbps is the negotiated WAN speed.
	SpeedWANMbps = 100
	// SpeedEth1Mbps is the negotiated speed of eth1.
	SpeedEth1Mbps = 1000
	// SpeedEth2Mbps is the negotiated speed of eth2.
	SpeedEth2Mbps = 2500
)

// demoDevice describes one device row of the sample network.
//
// The table is intentionally flat and readable: it is the source of truth for
// the demo dataset and the tests assert against it.
type demoDevice struct {
	// ID is the deterministic device ID.
	ID string
	// Name is the display name.
	Name string
	// Hostname is the reported hostname, if any.
	Hostname string
	// Type is the device type.
	Type string
	// Manufacturer and Model are the vendor fields.
	Manufacturer string
	Model        string
	// MAC is the canonical colon separated MAC, or empty for nodes that have
	// no hardware address (manual nodes such as the modem or the Internet
	// cloud).
	MAC string
	// IPv4 and IPv6 are the addresses of the device, if any.
	IPv4 string
	IPv6 string
	// Iface is the local interface the device was learned on.
	Iface string
	// State is the simulated device state.
	State string
	// StateReason explains the simulated state.
	StateReason string
	// ClientID is the DHCP client identifier, when the fixture has one.
	ClientID string
	// Description is a short human readable note.
	Description string
	// IsGateway marks the default gateway of the LAN.
	IsGateway bool
	// IsSelf marks the router Lanory runs on.
	IsSelf bool
	// IsManual marks a user created node.
	IsManual bool
	// NeighborState is the ARP/NDP state recorded as evidence, if any.
	NeighborState string
}

// demoDevices is the sample network. It reproduces fixtures/easepi-r1:
// eight discovered devices plus the manual nodes needed to draw a complete
// picture (Internet cloud, optical modem, upstream router, the Xiaomi router
// behind eth1).
var demoDevices = []demoDevice{
	{
		ID: "dev-mac-020000000010", Name: "EASEPi R1",
		Type: "router", Manufacturer: "EASEPi", Model: "R1",
		MAC: RouterMAC, IPv4: RouterLANIPv4, IPv6: RouterLANIPv6Global,
		State: "online", StateReason: "Lanory runs on this device",
		IsGateway: true, IsSelf: true,
		Description: "OpenWrt router running Lanory (demo)",
	},
	{
		ID: "dev-mac-020000000020", Name: "Upstream Router",
		Type: "router", Manufacturer: "Unknown", MAC: UpstreamMAC,
		IPv4: UpstreamIPv4, Iface: "eth0", State: "online",
		StateReason:   "answered ARP on eth0",
		Description:   "ISP provided router, upstream of the WAN port (demo)",
		NeighborState: "reachable",
	},
	{
		ID: "dev-manual-modem", Name: ModemName,
		Type: "modem_ont", MAC: ModemMAC, IPv4: ModemIPv4,
		State: "unknown", StateReason: "manually added, never probed",
		IsManual:    true,
		Description: "ISP optical modem (demo, manually added)",
	},
	{
		ID: "dev-mac-053344556677", Name: XiaomiName,
		Type: "router", Manufacturer: "Xiaomi", Model: "unknown",
		MAC: XiaomiMAC, IPv4: XiaomiIPv4, Iface: "eth1", State: "online",
		StateReason:   "answered ARP on eth1",
		Description:   "Secondary router behind eth1; management API not reachable (demo)",
		NeighborState: "reachable",
	},
	{
		ID: "dev-mac-b082e2613eb7", Name: "client-desktop", Hostname: "client-desktop",
		Type: "desktop", MAC: "02:11:22:33:44:b7",
		IPv4: "192.0.2.116", Iface: "eth2", State: "online",
		StateReason:   "answered probe from the router",
		ClientID:      "01:02:11:22:33:44:b7",
		Description:   "Desktop PC wired to eth2 (demo)",
		NeighborState: "reachable",
	},
	{
		ID: "dev-mac-0c6f8b4a3dd2", Name: "iPhone", Hostname: "iPhone",
		Type: "smartphone", MAC: "06:77:88:99:aa:d2",
		IPv4: "192.0.2.151", IPv6: "2001:db8:100::151", Iface: "eth1",
		State: "online", StateReason: "answered probe from the router",
		Description:   "Phone behind the Xiaomi router (demo)",
		NeighborState: "stale",
	},
	{
		ID: "dev-mac-867b1c3f065e", Name: "client-mac", Hostname: "client-mac",
		Type: "desktop", MAC: "07:88:99:aa:bb:5e",
		IPv4: "192.0.2.222", IPv6: "2001:db8:100::222", Iface: "eth1",
		State: "online", StateReason: "answered probe from the router",
		Description:   "Desktop behind the Xiaomi router (demo)",
		NeighborState: "reachable",
	},
	{
		ID: "dev-mac-463f368942e3", Name: "Watch", Hostname: "Watch",
		Type: "smartwatch", MAC: "04:66:77:88:99:e3",
		IPv4: "192.0.2.197", Iface: "eth1", State: "sleeping",
		StateReason:   "no answer for 3 consecutive probes, wrist worn",
		Description:   "Smartwatch, currently asleep (demo)",
		NeighborState: "stale",
	},
	{
		ID: "dev-mac-52e58f5bc52d", Name: "smart-lamp", Hostname: "smart-lamp",
		Type: "iot", MAC: "05:22:33:44:55:2d",
		IPv4: "192.0.2.159", Iface: "eth1", State: "online",
		StateReason:   "answered probe from the router",
		Description:   "Smart lamp (demo)",
		NeighborState: "reachable",
	},
	{
		ID: "dev-mac-0467617f7f24", Name: "Unknown 04:67:61",
		MAC: "04:11:22:33:44:24", IPv4: "192.0.2.228", Iface: "eth1",
		State: "offline", StateReason: "3 consecutive failed probes",
		Description:   "Static IP device missing from the DHCP lease file (demo)",
		NeighborState: "delay",
	},
	{
		ID: "dev-mac-ccb5d1057020", Name: "iot-sensor", Hostname: "iot-sensor",
		Type: "iot", MAC: "cc:11:22:33:44:20",
		IPv4: "192.0.2.231", Iface: "eth1", State: "online",
		StateReason:   "answered probe from the router",
		Description:   "ESP32 sensor (demo)",
		NeighborState: "probe",
	},
	{
		ID: "dev-mac-b4fa48101613", Name: "Unknown b4:fa:48",
		MAC: DeviceMACIPv6Only, IPv6: DeviceIPv6Only, Iface: "eth1",
		State: "unknown", StateReason: "only ever seen in the NDP table",
		Description:   "Observed through IPv6 neighbour discovery only (demo)",
		NeighborState: "stale",
	},
	{
		ID: "dev-mac-0f1122334455", Name: "NAS",
		Type: "nas", MAC: "0f:11:22:33:44:55",
		IPv4: "192.0.2.50", Iface: "eth2", State: "online",
		StateReason: "answered probe from the router",
		Description: "Manually added NAS wired to eth2 (demo)",
		IsManual:    true, NeighborState: "reachable",
	},
}

// demoInterfaces describes the local interfaces of the router.
type demoInterface struct {
	// ID is the deterministic interface ID.
	ID string
	// Name is the interface name.
	Name string
	// MAC is the hardware address.
	MAC string
	// Type is the interface type.
	Type string
	// Role is the interface role.
	Role string
	// OperState is the operational state.
	OperState string
	// Carrier reports the physical link state.
	Carrier bool
	// SpeedMbps is the negotiated speed.
	SpeedMbps int
	// BridgeID is the bridge the port belongs to, if any.
	BridgeID string
	// MTU is the interface MTU.
	MTU int
	// IsPhysical reports whether the interface is a real port.
	IsPhysical bool
	// RXBytes/TXBytes and the packet/error counters are simulated traffic.
	RXBytes uint64
	TXBytes uint64
	// Addr are the addresses carried by the interface.
	Addr []string
}

// demoInterfaces reproduces fixtures/easepi-r1/expected.yaml exactly.
var demoInterfaces = []demoInterface{
	{
		ID: "iface-demo-eth0", Name: "eth0", MAC: "02:00:00:00:00:10",
		Type: "ethernet", Role: "wan", OperState: "up", Carrier: true,
		SpeedMbps: SpeedWANMbps, MTU: 1500, IsPhysical: true,
		RXBytes: 987654321, TXBytes: 123456789,
		Addr: []string{RouterWANIPv4 + "/24", RouterWANIPv6 + "/64"},
	},
	{
		ID: "iface-demo-eth1", Name: "eth1", MAC: "02:00:00:00:00:11",
		Type: "ethernet", Role: "lan", OperState: "up", Carrier: true,
		SpeedMbps: SpeedEth1Mbps, MTU: 1500, IsPhysical: true,
		BridgeID: "iface-demo-br-lan",
		RXBytes:  4512345678, TXBytes: 9876543210,
	},
	{
		ID: "iface-demo-eth2", Name: "eth2", MAC: "02:00:00:00:00:12",
		Type: "ethernet", Role: "lan", OperState: "up", Carrier: true,
		SpeedMbps: SpeedEth2Mbps, MTU: 1500, IsPhysical: true,
		BridgeID: "iface-demo-br-lan",
		RXBytes:  12345678901, TXBytes: 23456789012,
	},
	{
		ID: "iface-demo-eth3", Name: "eth3", MAC: "02:00:00:00:00:13",
		Type: "ethernet", Role: "lan", OperState: "no-carrier", Carrier: false,
		SpeedMbps: 0, MTU: 1500, IsPhysical: true,
		BridgeID: "iface-demo-br-lan",
	},
	{
		ID: "iface-demo-br-lan", Name: "br-lan", MAC: "02:00:00:00:00:11",
		Type: "bridge", Role: "lan", OperState: "up", Carrier: true,
		SpeedMbps: SpeedEth2Mbps, MTU: 1500, IsPhysical: false,
		RXBytes: 16858134779, TXBytes: 33344352322,
		Addr: []string{RouterLANIPv4 + "/24", RouterLANIPv6ULA + "/64", RouterLANIPv6Global + "/64"},
	},
}

// demoEdge describes one topology edge of the sample network.
type demoEdge struct {
	// SourceID and TargetID are the device IDs.
	SourceID string
	TargetID string
	// SourceIface and TargetIface are the interface IDs involved.
	SourceIface string
	TargetIface string
	// Type is the edge type.
	Type string
	// Confidence is the simulated confidence.
	Confidence float64
	// Status is the simulated edge status.
	Status string
	// Label is the human readable label shown on the edge.
	Label string
	// Evidence is the raw evidence text stored for the edge.
	Evidence string
	// Unverified marks edges that lack enough evidence.
	Unverified bool
}

// demoEdges builds the sample topology: Internet -> modem -> upstream router
// -> eth0 -> LAN bridge -> eth1/eth2 -> devices.
func demoEdges() []demoEdge {
	wan := "iface-demo-eth0"
	e1 := "iface-demo-eth1"
	e2 := "iface-demo-eth2"
	router := "dev-mac-020000000010"
	return []demoEdge{
		{
			SourceID: "dev-mac-020000000020", TargetID: "dev-manual-modem",
			Type: "physical_link", Confidence: 0.5, Status: "unverified",
			Unverified: true,
			Evidence:   "user described the physical cabling; no protocol confirms it",
		},
		{
			SourceID: "dev-manual-modem", TargetID: router,
			SourceIface: wan, Type: "physical_link",
			Confidence: 0.7, Status: "link_up",
			Evidence: "eth0 is up at 100 Mbps and has an address on the upstream subnet",
		},
		{
			SourceID: router, TargetID: "dev-mac-053344556677",
			SourceIface: e1, Type: "observed_mac_port",
			Confidence: 0.7, Status: "link_up",
			Evidence: "MAC learned on bridge port eth1; management API not reachable",
		},
		{
			SourceID: router, TargetID: "dev-mac-b082e2613eb7",
			SourceIface: e2, Type: "observed_mac_port",
			Confidence: 0.7, Status: "link_up",
			Evidence: "MAC learned on bridge port eth2 (2.5 Gbps)",
		},
		{
			SourceID: router, TargetID: "dev-mac-0f1122334455",
			SourceIface: e2, Type: "user_defined",
			Confidence: 1.0, Status: "link_up",
			Evidence: "user asserted the relation",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-0c6f8b4a3dd2",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-867b1c3f065e",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-463f368942e3",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-52e58f5bc52d",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-0467617f7f24",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-ccb5d1057020",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		{
			SourceID: "dev-mac-053344556677", TargetID: "dev-mac-b4fa48101613",
			Type: "logical_network", Confidence: 0.3, Status: "unverified",
			Unverified: true,
			Evidence:   "both share the LAN subnet; no protocol confirms the hop",
		},
		// eth1 and eth2 are member ports of br-lan. That fact belongs to the
		// interface rows (role + bridge_id) rather than to a device-to-device
		// edge, so it is not duplicated here as a self relation.
	}
}

// demoProbe describes one probe target of the sample network.
type demoProbe struct {
	// ID is the deterministic probe ID.
	ID string
	// Name is the display name.
	Name string
	// Kind is the probe kind: device or network.
	Kind string
	// DeviceID links the probe to a device. Network probes are anchored to the
	// router that runs them.
	DeviceID string
	// Target is the probe target address.
	Target string
	// Port is used for TCP/HTTP probes.
	Port int
	// Protocol is the probe protocol.
	Protocol string
	// Interval and Timeout are in seconds.
	Interval int
	Timeout  int
	// State is the simulated probe state.
	State string
	// StateReason explains the simulated state.
	StateReason string
	// Results are the simulated recent results, oldest first.
	Results []demoProbeResult
}

// demoProbeResult is one simulated probe result.
type demoProbeResult struct {
	// LatencyMS is the simulated latency.
	LatencyMS int
	// Status is the result status.
	Status string
}

// demoProbes returns the probe targets of the sample network: one device probe
// per device that has an address, plus three network probes (gateway, upstream
// gateway, Internet DNS reachability).
func demoProbes() []demoProbe {
	dev := func(id, name, target string, results []demoProbeResult, state, reason string) demoProbe {
		return demoProbe{
			ID: "probe-demo-" + id, Name: name, DeviceID: id, Target: target,
			Protocol: "icmp", Interval: 60, Timeout: 3,
			State: state, StateReason: reason, Results: results,
		}
	}
	// Network probes are not about a single device: they answer "is the LAN,
	// the uplink or the Internet reachable". probe_targets.device_id is
	// NOT NULL with a foreign key, so the row still needs an anchor; the router
	// that runs the probe is the honest one, and the monitoring engine never
	// moves a device state from a network probe (see applyTransition).
	net := func(id, name, target string, results []demoProbeResult, state, reason string) demoProbe {
		return demoProbe{
			ID: "probe-demo-" + id, Name: name, Kind: "network", DeviceID: demoDevices[0].ID,
			Target: target, Protocol: "icmp", Interval: 60, Timeout: 3,
			State: state, StateReason: reason, Results: results,
		}
	}
	ok := demoProbeResult{LatencyMS: 3, Status: "success"}
	okSlow := demoProbeResult{LatencyMS: 42, Status: "success"}
	bad := demoProbeResult{LatencyMS: 0, Status: "timeout"}

	// The three network probes are not attached to a device: they answer
	// "is the LAN / the uplink / the Internet reachable", not "is this device
	// up", and the product spec keeps those concepts separate.
	probes := []demoProbe{
		net("gateway", "Gateway reachability", RouterLANIPv4,
			[]demoProbeResult{ok, ok, okSlow, ok}, "online", "probe succeeded"),
		net("upstream", "Upstream router reachability", UpstreamIPv4,
			[]demoProbeResult{okSlow, ok, ok, ok}, "online", "probe succeeded"),
		net("internet", "Internet reachability", "203.0.113.1",
			[]demoProbeResult{ok, okSlow, ok, bad}, "online",
			"probe succeeded; last attempt timed out"),
	}
	for _, d := range demoDevices {
		if d.IPv4 == "" {
			continue
		}
		var (
			results []demoProbeResult
			state   = d.State
			reason  = d.StateReason
		)
		switch d.State {
		case "online":
			results = []demoProbeResult{okSlow, ok, ok, ok}
		case "sleeping":
			results = []demoProbeResult{ok, bad, bad, bad}
		case "offline":
			results = []demoProbeResult{ok, bad, bad, bad}
		default:
			// Unknown state: the device has an address but has never answered a
			// probe, so all attempts fail.
			results = []demoProbeResult{bad}
		}
		if d.ID == "dev-mac-020000000010" {
			// The router itself is never probed from itself; keep the device
			// list free of a self-probe.
			continue
		}
		probes = append(probes, dev(d.ID, d.Name, d.IPv4, results, state, reason))
	}
	return probes
}

// demoEvent describes one history event of the sample network.
type demoEvent struct {
	// Type is the event type.
	Type string
	// DeviceID is the device the event belongs to.
	DeviceID string
	// EdgeRef names the demo edge the event concerns, as a pair of device IDs.
	//
	// network_events.edge_id is NOT NULL DEFAULT '' but carries a foreign key
	// to topology_edges, so a row with an empty edge_id is rejected by SQLite.
	// Every demo event therefore references a real edge, which is also the
	// honest thing to record: the event says what changed on that relation.
	EdgeRef [2]string
	// OldValue and NewValue carry the transition.
	OldValue string
	NewValue string
	// Message is the human readable description.
	Message string
	// MinutesAgo is how long ago the event happened, relative to now.
	MinutesAgo int
}

// demoEvents returns the simulated event history, newest first.
func demoEvents() []demoEvent {
	return []demoEvent{
		{
			Type: "device.offline", DeviceID: "dev-mac-0467617f7f24",
			EdgeRef:  [2]string{"dev-mac-053344556677", "dev-mac-0467617f7f24"},
			OldValue: "online", NewValue: "offline", MinutesAgo: 25,
			Message: "3 consecutive failed probes",
		},
		{
			Type: "device.offline", DeviceID: "dev-mac-463f368942e3",
			EdgeRef:  [2]string{"dev-mac-053344556677", "dev-mac-463f368942e3"},
			OldValue: "online", NewValue: "sleeping", MinutesAgo: 70,
			Message: "no answer for 3 consecutive probes; watch is wrist worn",
		},
		{
			Type: "link.up", DeviceID: "dev-mac-b082e2613eb7",
			EdgeRef:    [2]string{"dev-mac-020000000010", "dev-mac-b082e2613eb7"},
			MinutesAgo: 180,
			Message:    "eth2 link up at 2500 Mbps",
		},
		{
			Type: "speed.changed", DeviceID: "dev-mac-b082e2613eb7",
			EdgeRef:  [2]string{"dev-mac-020000000010", "dev-mac-b082e2613eb7"},
			OldValue: "1000", NewValue: "2500", MinutesAgo: 180,
			Message: "eth2 negotiated 2500 Mbps",
		},
		{
			Type: "device.discovered", DeviceID: "dev-mac-b4fa48101613",
			EdgeRef:    [2]string{"dev-mac-053344556677", "dev-mac-b4fa48101613"},
			MinutesAgo: 240,
			Message:    "first seen in the IPv6 neighbour table on eth1",
		},
		{
			Type: "device.discovered", DeviceID: "dev-mac-ccb5d1057020",
			EdgeRef:    [2]string{"dev-mac-053344556677", "dev-mac-ccb5d1057020"},
			MinutesAgo: 320,
			Message:    "first seen in the DHCP lease table",
		},
		{
			Type: "device.discovered", DeviceID: "dev-mac-0f1122334455",
			EdgeRef:    [2]string{"dev-mac-020000000010", "dev-mac-0f1122334455"},
			MinutesAgo: 1440,
			Message:    "manually added by the user",
		},
		{
			Type: "device.ip_changed", DeviceID: "dev-mac-0c6f8b4a3dd2",
			EdgeRef:  [2]string{"dev-mac-053344556677", "dev-mac-0c6f8b4a3dd2"},
			OldValue: "192.168.100.150", NewValue: "192.0.2.151", MinutesAgo: 2880,
			Message: "DHCP lease renewed with a new address",
		},
		{
			Type: "device.discovered", DeviceID: "dev-mac-020000000020",
			EdgeRef:    [2]string{"dev-mac-020000000020", "dev-manual-modem"},
			MinutesAgo: 4320,
			Message:    "first seen as the default gateway on eth0",
		},
	}
}

// baseTime is the fixed reference time used for the deterministic parts of the
// demo dataset (first_seen/last_seen of devices and interfaces). Relative times
// (events, probe results) are computed from the wall clock so the demo always
// looks fresh; the deterministic parts keep the row counts stable.
var baseTime = time.Date(2026, 2, 13, 16, 26, 40, 0, time.UTC)
