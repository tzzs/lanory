// Package model defines the shared domain entities for Lanory.
//
// This package is the single source of truth for the data model used by the
// database layer, the discovery collectors, the topology engine, the probe
// engine and the HTTP API. Every other package must import these types instead
// of defining its own variants.
//
// The struct tags below are the API contract: field names in JSON responses
// are part of the public REST surface and must not change without bumping the
// API version.
package model

import (
	"strconv"
	"time"
)

// ---------------------------------------------------------------------------
// Enumerations
// ---------------------------------------------------------------------------

// DeviceType classifies a discovered device.
type DeviceType string

const (
	DeviceTypeInternet     DeviceType = "internet"
	DeviceTypeModemONT     DeviceType = "modem_ont"
	DeviceTypeRouter       DeviceType = "router"
	DeviceTypeFirewall     DeviceType = "firewall"
	DeviceTypeSwitch       DeviceType = "switch"
	DeviceTypeWirelessAP   DeviceType = "wireless_ap"
	DeviceTypeMeshNode     DeviceType = "mesh_node"
	DeviceTypeNAS          DeviceType = "nas"
	DeviceTypeServer       DeviceType = "server"
	DeviceTypeDesktop      DeviceType = "desktop"
	DeviceTypeLaptop       DeviceType = "laptop"
	DeviceTypeSmartphone   DeviceType = "smartphone"
	DeviceTypeTablet       DeviceType = "tablet"
	DeviceTypeSmartwatch   DeviceType = "smartwatch"
	DeviceTypeSmartTV      DeviceType = "smart_tv"
	DeviceTypeIoT          DeviceType = "iot"
	DeviceTypePrinter      DeviceType = "printer"
	DeviceTypeCamera       DeviceType = "camera"
	DeviceTypeVM           DeviceType = "vm"
	DeviceTypeContainer    DeviceType = "container"
	DeviceTypeUnknown      DeviceType = "unknown"
	DeviceTypeNetworkFiber DeviceType = "network_fiber"
	DeviceTypeGenericNode  DeviceType = "generic_node"
)

// DeviceTypes lists every device type in a stable display order.
var DeviceTypes = []DeviceType{
	DeviceTypeInternet,
	DeviceTypeModemONT,
	DeviceTypeRouter,
	DeviceTypeFirewall,
	DeviceTypeSwitch,
	DeviceTypeWirelessAP,
	DeviceTypeMeshNode,
	DeviceTypeNAS,
	DeviceTypeServer,
	DeviceTypeDesktop,
	DeviceTypeLaptop,
	DeviceTypeSmartphone,
	DeviceTypeTablet,
	DeviceTypeSmartwatch,
	DeviceTypeSmartTV,
	DeviceTypeIoT,
	DeviceTypePrinter,
	DeviceTypeCamera,
	DeviceTypeVM,
	DeviceTypeContainer,
	DeviceTypeNetworkFiber,
	DeviceTypeGenericNode,
	DeviceTypeUnknown,
}

// IsValidDeviceType reports whether t is a known device type.
func IsValidDeviceType(t DeviceType) bool {
	for _, v := range DeviceTypes {
		if v == t {
			return true
		}
	}
	return false
}

// DeviceState is the unified availability state of a device.
//
// The distinction between Offline and Unknown is deliberate: Offline requires
// several consecutive failed checks, while Unknown means we simply do not have
// enough evidence to say anything. Devices that are never probed (for example
// a manually added fiber node) stay Unknown instead of being reported as
// Offline.
type DeviceState string

const (
	StateOnline   DeviceState = "online"
	StateOffline  DeviceState = "offline"
	StateUnknown  DeviceState = "unknown"
	StateSleeping DeviceState = "sleeping"
)

// InterfaceRole describes the role of a network interface.
type InterfaceRole string

const (
	InterfaceRoleWAN      InterfaceRole = "wan"
	InterfaceRoleLAN      InterfaceRole = "lan"
	InterfaceRoleDMZ      InterfaceRole = "dmz"
	InterfaceRoleLoopback InterfaceRole = "loopback"
	InterfaceRoleVirtual  InterfaceRole = "virtual"
	InterfaceRoleUnknown  InterfaceRole = "unknown"
)

// InterfaceType describes the kind of network interface.
type InterfaceType string

const (
	InterfaceTypeEthernet InterfaceType = "ethernet"
	InterfaceTypeBridge   InterfaceType = "bridge"
	InterfaceTypeWireless InterfaceType = "wireless"
	InterfaceTypeVLAN     InterfaceType = "vlan"
	InterfaceTypeLoopback InterfaceType = "loopback"
	InterfaceTypeVirtual  InterfaceType = "virtual"
	InterfaceTypeOther    InterfaceType = "other"
)

// AdminState is the administrative (configured) state of an interface.
type AdminState string

const (
	AdminUp      AdminState = "up"
	AdminDown    AdminState = "down"
	AdminUnknown AdminState = "unknown"
)

// OperState is the operational state of an interface, kept separate from
// Carrier on purpose: oper_state reflects the kernel view while carrier
// reflects the physical link.
type OperState string

const (
	OperUp        OperState = "up"
	OperDown      OperState = "down"
	OperUnknown   OperState = "unknown"
	OperNoCarrier OperState = "no-carrier"
)

// EdgeType classifies the semantics of a topology edge.
type EdgeType string

const (
	// EdgeTypePhysicalLink means a cable between two physical ports.
	EdgeTypePhysicalLink EdgeType = "physical_link"
	// EdgeTypeBridgeMembership means the device belongs to the network carried
	// by a bridge. It is a logical relation, not a cable.
	EdgeTypeBridgeMembership EdgeType = "bridge_membership"
	// EdgeTypeObservedMACPort means a MAC address was learned on a specific
	// bridge port.
	EdgeTypeObservedMACPort EdgeType = "observed_mac_port"
	// EdgeTypeWirelessAssoc means the device associates with an access point.
	EdgeTypeWirelessAssoc EdgeType = "wireless_assoc"
	// EdgeTypeLogicalNetwork means both endpoints share a network.
	EdgeTypeLogicalNetwork EdgeType = "logical_network"
	// EdgeTypeUserDefined means a human asserted the relation.
	EdgeTypeUserDefined EdgeType = "user_defined"
)

// EdgeStatus is the runtime state of a topology edge.
type EdgeStatus string

const (
	EdgeStatusLinkUp      EdgeStatus = "link_up"
	EdgeStatusLinkDown    EdgeStatus = "link_down"
	EdgeStatusReachable   EdgeStatus = "reachable"
	EdgeStatusUnreachable EdgeStatus = "unreachable"
	EdgeStatusUnverified  EdgeStatus = "unverified"
)

// DiscoverySource identifies where a piece of evidence came from.
type DiscoverySource string

const (
	SourceOpenWRT   DiscoverySource = "openwrt"
	SourceUCI       DiscoverySource = "uci"
	SourceUBUS      DiscoverySource = "ubus"
	SourceARP       DiscoverySource = "arp"
	SourceNDP       DiscoverySource = "ndp"
	SourceDHCP      DiscoverySource = "dhcp"
	SourceBridgeFDB DiscoverySource = "bridge_fdb"
	SourceLLDP      DiscoverySource = "lldp"
	SourceMDNS      DiscoverySource = "mdns"
	SourceSSDP      DiscoverySource = "ssdp"
	SourceSNMP      DiscoverySource = "snmp"
	SourceXiaomi    DiscoverySource = "xiaomi"
	SourceScan      DiscoverySource = "scan"
	SourceManual    DiscoverySource = "manual"
	SourcePlatform  DiscoverySource = "platform"
	SourceDemo      DiscoverySource = "demo"
)

// IdentityType classifies a device identity record.
type IdentityType string

const (
	IdentityMAC         IdentityType = "mac"
	IdentityDHCPClient  IdentityType = "dhcp_client_id"
	IdentityHostname    IdentityType = "hostname"
	IdentityVendor      IdentityType = "vendor"
	IdentityFingerprint IdentityType = "fingerprint"
)

// AddressFamily is the IP address family.
type AddressFamily string

const (
	AddressFamilyIPv4 AddressFamily = "ipv4"
	AddressFamilyIPv6 AddressFamily = "ipv6"
)

// NeighborState is the state of an ARP/NDP neighbour entry.
type NeighborState string

const (
	NeighborReachable  NeighborState = "reachable"
	NeighborStale      NeighborState = "stale"
	NeighborDelay      NeighborState = "delay"
	NeighborProbe      NeighborState = "probe"
	NeighborFailed     NeighborState = "failed"
	NeighborIncomplete NeighborState = "incomplete"
	NeighborPermanent  NeighborState = "permanent"
	NeighborNone       NeighborState = "none"
)

// FDBEntryType classifies a bridge forwarding database entry.
type FDBEntryType string

const (
	FDBEntryDynamic   FDBEntryType = "dynamic"
	FDBEntryStatic    FDBEntryType = "static"
	FDBEntryPermanent FDBEntryType = "permanent"
	FDBEntryLocal     FDBEntryType = "local"
	FDBEntrySelf      FDBEntryType = "self"
)

// Collector names.
const (
	CollectorOpenWRT    = "openwrt"
	CollectorInterfaces = "interfaces"
	CollectorARP        = "arp"
	CollectorNDP        = "ndp"
	CollectorDHCP       = "dhcp"
	CollectorBridgeFDB  = "bridge_fdb"
	CollectorLLDP       = "lldp"
	CollectorMDNS       = "mdns"
	CollectorScan       = "scan"
)

// ProbeProtocol is the protocol used by a probe.
type ProbeProtocol string

const (
	ProbeICMP ProbeProtocol = "icmp"
	ProbeTCP  ProbeProtocol = "tcp"
	ProbeHTTP ProbeProtocol = "http"
	ProbeDNS  ProbeProtocol = "dns"
	ProbeARP  ProbeProtocol = "arp"
)

// ProbeKind distinguishes device probes from network/connectivity probes.
type ProbeKind string

const (
	ProbeKindDevice  ProbeKind = "device"
	ProbeKindNetwork ProbeKind = "network"
)

// ProbeRunStatus is the outcome of a single probe execution.
type ProbeRunStatus string

const (
	ProbeRunSuccess ProbeRunStatus = "success"
	ProbeRunTimeout ProbeRunStatus = "timeout"
	ProbeRunRefused ProbeRunStatus = "refused"
	ProbeRunError   ProbeRunStatus = "error"
)

// EventType enumerates the recorded history events.
type EventType string

const (
	EventDeviceOnline      EventType = "device.online"
	EventDeviceOffline     EventType = "device.offline"
	EventDeviceDiscovered  EventType = "device.discovered"
	EventDeviceRemoved     EventType = "device.removed"
	EventDeviceUpdated     EventType = "device.updated"
	EventDeviceIPChanged   EventType = "device.ip_changed"
	EventDeviceMACChanged  EventType = "device.mac_changed"
	EventLinkUp            EventType = "link.up"
	EventLinkDown          EventType = "link.down"
	EventSpeedChanged      EventType = "speed.changed"
	EventTopologyChanged   EventType = "topology.changed"
	EventProbeCompleted    EventType = "probe.completed"
	EventDiscoveryStarted  EventType = "discovery.started"
	EventDiscoveryFinished EventType = "discovery.completed"
	EventDeviceMerged      EventType = "device.merged"
	EventDeviceHidden      EventType = "device.hidden"
)

// JobStatus is the status of a discovery job.
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

// Device is a logical network device. IP addresses are deliberately not part
// of the identity: they change over time and are stored in DeviceAddress.
type Device struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Hostname     string     `json:"hostname"`
	Type         DeviceType `json:"type"`
	Manufacturer string     `json:"manufacturer"`
	Model        string     `json:"model"`
	Description  string     `json:"description"`

	// IsManual marks devices created by the user. Manual devices are never
	// re-created or removed by discovery.
	IsManual bool `json:"is_manual"`
	// IsLocked protects a manual device from automatic merges.
	IsLocked bool `json:"is_locked"`
	// IsHidden suppresses a device from normal listings; hidden devices are
	// not re-created by discovery either.
	IsHidden bool `json:"is_hidden"`

	// TypeSource records where the current device type came from so the UI
	// can explain it.
	TypeSource DiscoverySource `json:"type_source,omitempty"`

	State DeviceState `json:"state"`
	// StateReason explains the current state in human readable form.
	//
	// The text is English and is produced by the backend. It is kept for
	// logs, bug reports and API consumers outside the web UI; the web UI
	// renders StateReasonCode through the translation layer instead.
	StateReason string `json:"state_reason,omitempty"`
	// StateReasonCode is the translatable form of StateReason: a stable code
	// plus the parameters the message template needs.
	StateReasonCode *Reason `json:"state_reason_code,omitempty"`
	// LastStateChange is the timestamp of the last state transition.
	LastStateChange time.Time `json:"last_state_change"`

	// PrimaryIPv4 and PrimaryIPv6 are cached primary addresses.
	PrimaryIPv4 string `json:"primary_ipv4,omitempty"`
	PrimaryIPv6 string `json:"primary_ipv6,omitempty"`
	// PrimaryMAC is the cached primary MAC address, if known.
	PrimaryMAC string `json:"primary_mac,omitempty"`

	// DiscoverySources lists every source that has ever observed the device.
	DiscoverySources []DiscoverySource `json:"discovery_sources"`

	// IsGateway marks the device that acts as the default gateway.
	IsGateway bool `json:"is_gateway"`
	// IsSelf marks the device Lanory itself runs on.
	IsSelf bool `json:"is_self"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeviceAddress is an IP address observed for a device.
type DeviceAddress struct {
	ID           string          `json:"id"`
	DeviceID     string          `json:"device_id"`
	Address      string          `json:"address"`
	Family       AddressFamily   `json:"family"`
	InterfaceID  string          `json:"interface_id,omitempty"`
	Source       DiscoverySource `json:"source"`
	PrefixLength int             `json:"prefix_length,omitempty"`
	IsPrimary    bool            `json:"is_primary"`
	FirstSeen    time.Time       `json:"first_seen"`
	LastSeen     time.Time       `json:"last_seen"`
}

// DeviceIdentity is an identity claim about a device, with a confidence value.
type DeviceIdentity struct {
	ID         string          `json:"id"`
	DeviceID   string          `json:"device_id"`
	Type       IdentityType    `json:"type"`
	Value      string          `json:"value"`
	Source     DiscoverySource `json:"source"`
	Confidence float64         `json:"confidence"`
	FirstSeen  time.Time       `json:"first_seen"`
	LastSeen   time.Time       `json:"last_seen"`
}

// NetworkInterface is a network interface of a device (typically the local
// router, but manual devices may have interfaces too).
type NetworkInterface struct {
	ID       string        `json:"id"`
	DeviceID string        `json:"device_id"`
	Name     string        `json:"name"`
	MAC      string        `json:"mac,omitempty"`
	Type     InterfaceType `json:"type"`
	Role     InterfaceRole `json:"role"`

	AdminState AdminState `json:"admin_state"`
	OperState  OperState  `json:"oper_state"`
	Carrier    bool       `json:"carrier"`
	SpeedMbps  int        `json:"speed_mbps"`
	MTU        int        `json:"mtu"`
	BridgeID   string     `json:"bridge_id,omitempty"`
	VLANID     int        `json:"vlan_id,omitempty"`
	IsPhysical bool       `json:"is_physical"`

	RXBytes   uint64 `json:"rx_bytes"`
	TXBytes   uint64 `json:"tx_bytes"`
	RXErrors  uint64 `json:"rx_errors"`
	TXErrors  uint64 `json:"tx_errors"`
	RXPackets uint64 `json:"rx_packets"`
	TXPackets uint64 `json:"tx_packets"`
	RXDropped uint64 `json:"rx_dropped"`
	TXDropped uint64 `json:"tx_dropped"`

	Addresses []DeviceAddress `json:"addresses,omitempty"`

	// LastSpeedChange records the last observed speed change.
	LastSpeedChange time.Time `json:"last_speed_change,omitempty"`
	LastSeen        time.Time `json:"last_seen"`
}

// LinkReasonCode derives the translatable reason code for an interface's
// observed carrier and operstate.
//
// It is the single source of truth for this derivation: both the live link
// observer (internal/monitoring) and the store-backed monitoring provider
// (internal/api) call it, so an interface rendered through either path uses
// the same wording. Keeping it on the model avoids a circular import between
// the two packages.
func LinkReasonCode(carrier bool, oper OperState, speedMbps int) *Reason {
	params := map[string]string{"operstate": string(oper)}
	switch {
	case carrier && speedMbps > 0:
		params["speed_mbps"] = strconv.Itoa(speedMbps)
		return &Reason{Code: ReasonLinkCarrierUp, Params: params}
	case carrier:
		return &Reason{Code: ReasonLinkCarrierUpNoSpeed, Params: params}
	case oper == OperDown || oper == OperNoCarrier:
		return &Reason{Code: ReasonLinkCarrierDown, Params: params}
	case oper == OperUp:
		return &Reason{Code: ReasonLinkOperstateUnknownCarrier, Params: params}
	default:
		return &Reason{Code: ReasonLinkNoInformation, Params: params}
	}
}

// TopologyEdge is a relation between two devices.
type TopologyEdge struct {
	ID                string          `json:"id"`
	SourceID          string          `json:"source_id"`
	TargetID          string          `json:"target_id"`
	SourceInterfaceID string          `json:"source_interface_id,omitempty"`
	TargetInterfaceID string          `json:"target_interface_id,omitempty"`
	EdgeType          EdgeType        `json:"edge_type"`
	DiscoverySource   DiscoverySource `json:"discovery_source"`
	Confidence        float64         `json:"confidence"`
	UserConfirmed     bool            `json:"user_confirmed"`
	Locked            bool            `json:"locked"`
	Status            EdgeStatus      `json:"status"`

	// Label is an optional user provided label for the edge.
	Label string `json:"label,omitempty"`

	// EvidenceCount is the number of evidence records backing the edge.
	EvidenceCount int `json:"evidence_count"`
	// Unverified marks edges that lack enough evidence.
	Unverified bool `json:"unverified"`

	LastObserved time.Time `json:"last_observed"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DiscoveryEvidence stores the raw basis for an inference.
type DiscoveryEvidence struct {
	ID           string    `json:"id"`
	Collector    string    `json:"collector"`
	SubjectType  string    `json:"subject_type"` // device | interface | edge | address
	SubjectID    string    `json:"subject_id"`
	EvidenceType string    `json:"evidence_type"`
	RawData      string    `json:"raw_data"`
	ObservedAt   time.Time `json:"observed_at"`
}

// ProbeTarget is a monitored target.
type ProbeTarget struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Kind     ProbeKind     `json:"kind"`
	Target   string        `json:"target"` // IP, hostname or CIDR
	Port     int           `json:"port,omitempty"`
	Protocol ProbeProtocol `json:"protocol"`
	SourceID string        `json:"source_id"` // the device the probe runs from
	Interval int           `json:"interval"`  // seconds
	Timeout  int           `json:"timeout"`   // seconds
	Enabled  bool          `json:"enabled"`
	// DeviceID links a device probe to its device.
	DeviceID string `json:"device_id,omitempty"`

	// FailureThreshold is the number of consecutive failures before a device
	// is declared offline.
	FailureThreshold int `json:"failure_threshold"`
	// RecoverConfirm is the number of consecutive successes required to bring
	// a device back online.
	RecoverConfirm int `json:"recover_confirm"`

	State       DeviceState `json:"state"`
	StateReason string      `json:"state_reason,omitempty"`
	// StateReasonCode is the translatable form of StateReason.
	StateReasonCode      *Reason        `json:"state_reason_code,omitempty"`
	ConsecutiveFailures  int            `json:"consecutive_failures"`
	ConsecutiveSuccesses int            `json:"consecutive_successes"`
	LastResult           ProbeRunStatus `json:"last_result,omitempty"`
	LastLatencyMS        int            `json:"last_latency_ms,omitempty"`
	LastError            string         `json:"last_error,omitempty"`
	LastRunAt            time.Time      `json:"last_run_at"`
	LastSuccessAt        time.Time      `json:"last_success_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProbeResult is a single probe execution result.
type ProbeResult struct {
	ID           string         `json:"id"`
	ProbeID      string         `json:"probe_id"`
	Timestamp    time.Time      `json:"timestamp"`
	Success      bool           `json:"success"`
	LatencyMS    int            `json:"latency_ms"`
	Status       ProbeRunStatus `json:"status"`
	ErrorType    string         `json:"error_type,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
}

// NetworkEvent is a history record.
type NetworkEvent struct {
	ID        string    `json:"id"`
	EventType EventType `json:"event_type"`
	DeviceID  string    `json:"device_id,omitempty"`
	EdgeID    string    `json:"edge_id,omitempty"`
	OldValue  string    `json:"old_value,omitempty"`
	NewValue  string    `json:"new_value,omitempty"`
	Message   string    `json:"message,omitempty"`
	// ReasonCode is the translatable form of Message: a stable code plus the
	// parameters the message template needs. Empty for events whose message
	// is self-describing (a speed change, for example, whose old and new
	// values carry the meaning).
	ReasonCode *Reason   `json:"reason_code,omitempty"`
	Evidence   string    `json:"evidence,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// TopologyLayout persists a node position.
type TopologyLayout struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	X         float64   `json:"x"`
	Y         float64   `json:"y"`
	Pinned    bool      `json:"pinned"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AppSetting is a single key/value setting row.
type AppSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// Composed / API response types
// ---------------------------------------------------------------------------

// DeviceDetail is the full device payload returned by GET /devices/{id}.
type DeviceDetail struct {
	Device     *Device            `json:"device"`
	Addresses  []DeviceAddress    `json:"addresses"`
	Identities []DeviceIdentity   `json:"identities"`
	Interfaces []NetworkInterface `json:"interfaces"`
	Edges      []TopologyEdge     `json:"edges"`
	Probes     []ProbeTarget      `json:"probes"`
	LastProbe  *ProbeResult       `json:"last_probe"`
	Events     []NetworkEvent     `json:"events"`
}

// Overview is the payload of GET /overview.
type Overview struct {
	Counts           DeviceCounts      `json:"counts"`
	Interfaces       []InterfaceStatus `json:"interfaces"`
	Networks         []NetworkStatus   `json:"networks"`
	RecentNew        []Device          `json:"recent_new_devices"`
	RecentOffline    []Device          `json:"recent_offline_devices"`
	RecentEvents     []NetworkEvent    `json:"recent_events"`
	Anomalies        []Anomaly         `json:"anomalies"`
	LastScanAt       time.Time         `json:"last_scan_at"`
	DiscoveryEnabled bool              `json:"discovery_enabled"`
	DemoMode         bool              `json:"demo_mode"`
	GeneratedAt      time.Time         `json:"generated_at"`
}

// DeviceCounts holds device counters.
type DeviceCounts struct {
	Total    int `json:"total"`
	Online   int `json:"online"`
	Offline  int `json:"offline"`
	Unknown  int `json:"unknown"`
	Sleeping int `json:"sleeping"`
	Hidden   int `json:"hidden"`
	Manual   int `json:"manual"`
}

// InterfaceStatus is the runtime status of a network interface.
type InterfaceStatus struct {
	Interface NetworkInterface `json:"interface"`
	Role      InterfaceRole    `json:"role"`
	Status    EdgeStatus       `json:"status"`
	// Reason is the English fallback; ReasonCode is the translatable form.
	Reason     string  `json:"reason,omitempty"`
	ReasonCode *Reason `json:"reason_code,omitempty"`
}

// NetworkStatus describes a logical network (bridge or interface with an IP).
type NetworkStatus struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	InterfaceID string        `json:"interface_id"`
	CIDR        string        `json:"cidr,omitempty"`
	GatewayIP   string        `json:"gateway_ip,omitempty"`
	Role        InterfaceRole `json:"role"`
	DeviceCount int           `json:"device_count"`
}

// Anomaly is a network level problem surfaced on the overview page.
type Anomaly struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // info | warning | critical
	// Title and Detail are the English text produced by the backend. The web
	// UI renders ReasonCode and DetailReasonCode through the translation layer
	// instead.
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// ReasonCode is the translatable form of Title: a stable code plus the
	// parameters the templates need.
	ReasonCode *Reason `json:"reason_code,omitempty"`
	// DetailReasonCode is the translatable form of Detail. The two lines are
	// separate sentences, so they need separate codes: one code cannot carry
	// two different messages.
	DetailReasonCode *Reason   `json:"detail_reason_code,omitempty"`
	Since            time.Time `json:"since"`
}

// Topology is the payload of GET /topology.
type Topology struct {
	Nodes       []TopologyNode   `json:"nodes"`
	Edges       []TopologyEdge   `json:"edges"`
	Layout      []TopologyLayout `json:"layout"`
	GeneratedAt time.Time        `json:"generated_at"`
}

// TopologyNode is a node in the topology graph.
type TopologyNode struct {
	ID              string            `json:"id"`
	Kind            string            `json:"kind"` // device | network
	Device          *Device           `json:"device,omitempty"`
	Interface       *NetworkInterface `json:"interface,omitempty"`
	Network         *NetworkStatus    `json:"network,omitempty"`
	State           DeviceState       `json:"state"`
	StateReason     string            `json:"state_reason,omitempty"`
	StateReasonCode *Reason           `json:"state_reason_code,omitempty"`
	PrimaryIP       string            `json:"primary_ip,omitempty"`
	PrimaryMAC      string            `json:"primary_mac,omitempty"`
	Children        []string          `json:"children,omitempty"`
	// Addresses lists every address observed for the node, one entry per
	// address. A router is the canonical multi-homed case: 192.168.100.1 on the
	// LAN side and 192.168.1.4 on the WAN side are the same device, and a
	// consumer that only receives PrimaryIP cannot tell which side it is
	// looking at.
	Addresses []NodeAddress `json:"addresses,omitempty"`
	// Unresolved marks a synthetic segment node that groups devices without a
	// known intermediate.
	Unresolved bool `json:"unresolved,omitempty"`
}

// NodeAddress is one address of a topology node, tagged with the interface and
// the logical side it belongs to.
//
// Side is what makes a multi-homed node legible: a router's LAN address and its
// WAN address live in different subnets and mean different things, so the UI can
// group addresses by Side instead of printing an undifferentiated list.
type NodeAddress struct {
	Address string `json:"address"`
	// Family is ipv4 or ipv6.
	Family AddressFamily `json:"family"`
	// PrefixLength is the CIDR prefix length, 0 when unknown.
	PrefixLength int `json:"prefix_length,omitempty"`
	// CIDR is the network this address belongs to (network address/prefix), so
	// addresses on the same subnet share a value and can be grouped by it.
	CIDR string `json:"cidr,omitempty"`
	// Interface is the local interface carrying the address, when it is a local
	// address. Empty for addresses observed on a remote device.
	Interface string `json:"interface,omitempty"`
	// Side is lan, wan or unknown: the role of the interface the address was
	// observed on. A remote device address has no interface, so its side is
	// unknown.
	Side InterfaceRole `json:"side"`
	// IsPrimary marks the address the node's PrimaryIP was taken from.
	IsPrimary bool `json:"is_primary,omitempty"`
}

// TopologyEvidence is the payload of GET /topology/evidence.
type TopologyEvidence struct {
	Edges    []EdgeEvidence   `json:"edges"`
	Unmapped []DeviceEvidence `json:"unmapped_devices"`
}

// EdgeEvidence bundles an edge with its supporting evidence.
type EdgeEvidence struct {
	Edge     TopologyEdge        `json:"edge"`
	Evidence []DiscoveryEvidence `json:"evidence"`
}

// DeviceEvidence bundles a device with its evidence and address observations.
type DeviceEvidence struct {
	Device    Device              `json:"device"`
	Evidence  []DiscoveryEvidence `json:"evidence"`
	Addresses []DeviceAddress     `json:"addresses"`
}

// DiscoveryStatus is the payload of GET /discovery/status.
type DiscoveryStatus struct {
	Running      bool              `json:"running"`
	CurrentJobID string            `json:"current_job_id,omitempty"`
	LastJob      *DiscoveryJob     `json:"last_job"`
	Collectors   []CollectorStatus `json:"collectors"`
	Interval     string            `json:"interval"`
	Enabled      bool              `json:"enabled"`
}

// CollectorStatus describes a registered collector.
type CollectorStatus struct {
	Name           string    `json:"name"`
	Supported      bool      `json:"supported"`
	Capabilities   []string  `json:"capabilities"`
	Description    string    `json:"description,omitempty"`
	Enabled        bool      `json:"enabled"`
	LastRunAt      time.Time `json:"last_run_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastDurationMS int       `json:"last_duration_ms,omitempty"`
	Records        int       `json:"records,omitempty"`
}

// DiscoveryJob tracks one discovery run.
type DiscoveryJob struct {
	ID              string         `json:"id"`
	Status          JobStatus      `json:"status"`
	Trigger         string         `json:"trigger"` // manual | scheduled | startup
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at,omitempty"`
	Progress        int            `json:"progress"` // 0..100
	Collectors      []CollectorRun `json:"collectors"`
	Error           string         `json:"error,omitempty"`
	DevicesFound    int            `json:"devices_found"`
	DevicesNew      int            `json:"devices_new"`
	InterfacesFound int            `json:"interfaces_found"`
	EdgesCreated    int            `json:"edges_created"`
}

// CollectorRun records one collector execution inside a job.
type CollectorRun struct {
	Name       string    `json:"name"`
	Status     JobStatus `json:"status"`
	DurationMS int       `json:"duration_ms"`
	Records    int       `json:"records"`
	Error      string    `json:"error,omitempty"`
}

// MonitoringStatus is the payload of GET /monitoring/status.
type MonitoringStatus struct {
	Probes      []ProbeStatus     `json:"probes"`
	Interfaces  []InterfaceStatus `json:"interfaces"`
	GeneratedAt time.Time         `json:"generated_at"`
}

// ProbeStatus bundles a probe with its latest results.
type ProbeStatus struct {
	Probe        ProbeTarget   `json:"probe"`
	Recent       []ProbeResult `json:"recent"`
	LossPct      float64       `json:"loss_pct"`
	AvgLatencyMS int           `json:"avg_latency_ms"`
}

// EventList is the payload of GET /events.
type EventList struct {
	Events []NetworkEvent `json:"events"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// DeviceList is the payload of GET /devices.
type DeviceList struct {
	Devices []Device `json:"devices"`
	Total   int      `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

// ErrorResponse is the standard error envelope.
type ErrorResponse struct {
	Error   string            `json:"error"`
	Code    string            `json:"code,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// HealthResponse is the payload of GET /api/v1/health.
type HealthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	DemoMode      bool   `json:"demo_mode"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}
