// Package topology builds the network graph the UI renders.
//
// The topology engine is the component that turns discovered facts into a
// picture. Its whole job is deciding what the user is allowed to believe, so it
// is deliberately conservative:
//
//   - A device observed on a bridge port is placed under that port, not under a
//     hypothetical switch. When several devices share one port and no known
//     intermediate device explains the sharing, they are grouped under an
//     unresolved-segment node instead of an invented switch or AP.
//   - A logical network membership (the device is on the LAN subnet) is a
//     separate, weaker relation from a physical port observation. Both are
//     representable and the UI draws them differently.
//   - Anything without enough evidence is marked unverified, so it is drawn
//     dashed. The engine never invents a node to make the graph look complete.
//
// The engine reads only through the store interface, so it is testable without
// a database and cannot accidentally mutate anything.
package topology

import (
	"sort"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// NodeKind classifies a topology node.
type NodeKind string

const (
	// NodeKindDevice is a real device discovered or created by the user.
	NodeKindDevice NodeKind = "device"
	// NodeKindNetwork is a logical network carried by a bridge or an interface
	// with an IP address. It is not a physical box.
	NodeKindNetwork NodeKind = "network"
	// NodeKindSegment is the "unresolved network segment" catch-all: several
	// devices observed behind the same port with no known intermediate. The
	// engine refuses to invent a switch or an access point here.
	NodeKindSegment NodeKind = "segment"
)

// Node is one vertex of the topology graph.
//
// A node is either a device, a network or an unresolved segment. Interfaces and
// ports are attributes of the owning device, not nodes of their own: a router's
// eight ports are eight attributes, not eight boxes.
type Node struct {
	// ID is stable across rebuilds: "dev-<device id>", "net-<name>",
	// "seg-<id>".
	ID string
	// Kind classifies the node.
	Kind NodeKind
	// Device is set for a device node.
	Device *model.Device
	// Network is set for a network node.
	Network *model.NetworkStatus
	// Interface is set for a network node backed by an interface.
	Interface *model.NetworkInterface
	// State and StateReason mirror the device state for rendering.
	State       model.DeviceState
	StateReason string
	// PrimaryIP and PrimaryMAC are the cached display values.
	PrimaryIP  string
	PrimaryMAC string
	// Addresses lists every address observed for this node, tagged with the
	// interface and side that carries it. A router is the canonical
	// multi-homed case: the same box owns 192.168.100.1 on the LAN side and
	// 192.168.1.4 on the WAN side. Keeping the whole list (rather than only
	// PrimaryIP) is what lets the UI group a node's addresses by subnet and
	// explain which side of the box each one belongs to.
	Addresses []model.NodeAddress
	// Children lists the nodes grouped under this one, top to bottom.
	Children []string
	// Parent is the node this one hangs under. Empty means a root. The
	// relationship is derived evidence, never a stored fact: the engine
	// recomputes it on every build and never persists it.
	Parent string
	// Port is the bridge port a device node was observed on, when known.
	Port string
	// Bridge is the bridge carrying the device, when known.
	Bridge string
	// Unresolved marks a synthetic segment node that groups devices without a
	// known intermediate device. The UI renders it as a dashed group, never as
	// a device.
	Unresolved bool
	// Unverified marks a node whose placement rests on a single low-confidence
	// observation.
	Unverified bool
	// Reason explains the placement in human readable form, for the evidence
	// panel.
	Reason string

	// Evidence counts how many observations back this node.
	Evidence int
}

// Edge is one relation between two nodes.
//
// The distinction between the edge types is the product's core promise, so it
// is preserved here rather than flattened into "connected":
//
//   - observed_mac_port: the bridge learned this MAC on this port.
//   - physical_link: LLDP confirmed an adjacent device.
//   - bridge_membership: the port belongs to the bridge.
//   - logical_network: the device has an address inside the network's subnet.
//   - user_defined: a human asserted it.
type Edge struct {
	// ID is the persisted edge id, when the edge came from the store.
	ID string
	// Source and Target are node IDs.
	Source string
	Target string
	// SourceInterfaceID and TargetInterfaceID identify the interfaces the
	// relation leaves and enters, when the store recorded them. For an
	// observed_mac_port edge the source interface is the bridge port.
	SourceInterfaceID string
	TargetInterfaceID string
	// Type is the relation.
	Type model.EdgeType
	// Status is the edge's runtime state.
	Status model.EdgeStatus
	// Confidence is 0..1.
	Confidence float64
	// UserConfirmed and Locked come straight from the store: a locked edge is
	// never downgraded by a later discovery pass.
	UserConfirmed bool
	Locked        bool
	// Unverified marks an edge below the display threshold.
	Unverified bool
	// Label is the user provided label, when any.
	Label string
	// Port, Bridge and SpeedMbps describe the physical side, when known. For
	// an observed_mac_port edge Port is the bridge port the MAC was learned on
	// and Bridge is the bridge carrying it.
	Port      string
	Bridge    string
	SpeedMbps int
	// DiscoverySource records which collector supplied the relation.
	DiscoverySource model.DiscoverySource
	// LastObserved is when the relation was last seen.
	LastObserved time.Time
	// EvidenceCount is the number of records backing the edge.
	EvidenceCount int
}

// Graph is the built topology.
type Graph struct {
	Nodes []Node
	Edges []Edge
	// GeneratedAt is when the graph was built.
	GeneratedAt time.Time
	// Unmapped lists devices the engine could not place anywhere. They are
	// still returned so the evidence panel can explain why.
	Unmapped []Node
}

// nodeIndex indexes nodes by ID for O(1) lookups during the build.
type nodeIndex struct {
	nodes map[string]*Node
	order []string
}

func newNodeIndex() *nodeIndex {
	return &nodeIndex{nodes: map[string]*Node{}}
}

func (n *nodeIndex) get(id string) *Node { return n.nodes[id] }

func (n *nodeIndex) add(node Node) {
	n.nodes[node.ID] = &node
	n.order = append(n.order, node.ID)
}

// sortedNodes returns the nodes in insertion order, which is stable enough for
// rendering and deterministic for tests.
func (n *nodeIndex) sortedNodes() []Node {
	out := make([]Node, 0, len(n.order))
	for _, id := range n.order {
		out = append(out, *n.nodes[id])
	}
	return out
}

// deviceNodeID builds the node ID for a device node.
func deviceNodeID(deviceID string) string { return "dev-" + deviceID }

// networkNodeID builds the node ID for a network node.
func networkNodeID(name string) string { return "net-" + name }

// segmentNodeID builds the node ID for an unresolved-segment node.
func segmentNodeID(bridge, port string) string { return "seg-" + bridge + "-" + port }

// selfNodeID builds the node ID for the router Lanory runs on.
func selfNodeID(deviceID string) string { return "dev-" + deviceID }

// SortNodes orders nodes for a stable payload: networks first, then the self
// device, then everything else by ID.
func SortNodes(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		rank := func(k NodeKind) int {
			switch k {
			case NodeKindNetwork:
				return 0
			case NodeKindSegment:
				return 2
			default:
				return 1
			}
		}
		if ra, rb := rank(a.Kind), rank(b.Kind); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})
}

// Placement is a persisted node position.
//
// It mirrors model.TopologyLayout minus the storage fields, so the layout engine
// and the API can exchange positions without importing the database.
type Placement struct {
	// NodeID is the node the position belongs to.
	NodeID string
	// X and Y are the coordinates in the UI's coordinate space.
	X float64
	Y float64
	// Pinned marks a position the user explicitly locked.
	Pinned bool
}
