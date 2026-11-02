package topology

import (
	"sort"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// This file converts the engine's internal graph into the API contract types
// defined in internal/model.
//
// The conversion lives here rather than in the API package for two reasons:
//
//   - The mapping is mechanical but easy to get subtly wrong (children order,
//     unverified flags, network payloads). Keeping it next to the graph means a
//     change to the graph forces a change here.
//   - The API package must not depend on the topology package's internals, so
//     the wiring layer simply calls ToModel.

// ToModel renders the graph as the GET /topology payload.
//
// layout is copied through untouched: persisted positions are user data and must
// survive a rebuild.
func (g *Graph) ToModel(layout []model.TopologyLayout) *model.Topology {
	if g == nil {
		return &model.Topology{
			Nodes:       []model.TopologyNode{},
			Edges:       []model.TopologyEdge{},
			Layout:      []model.TopologyLayout{},
			GeneratedAt: time.Now().UTC(),
		}
	}
	out := &model.Topology{
		Nodes:       make([]model.TopologyNode, 0, len(g.Nodes)),
		Edges:       make([]model.TopologyEdge, 0, len(g.Edges)),
		Layout:      layout,
		GeneratedAt: g.GeneratedAt,
	}
	if out.Layout == nil {
		out.Layout = []model.TopologyLayout{}
	}
	for _, n := range g.Nodes {
		out.Nodes = append(out.Nodes, n.toModel())
	}
	for _, e := range g.Edges {
		out.Edges = append(out.Edges, e.toModel())
	}
	return out
}

// toModel renders one node.
//
// The API contract (docs/api.md, web/src/types/api.ts) admits exactly two node
// kinds: "device" and "network". An unresolved segment is therefore emitted as a
// network node with Unresolved set, not as a third kind. The UI decides how to
// draw it from the Unresolved flag (web/src/features/topology/graph.ts checks it
// before the kind), so a consumer that only understands the documented two kinds
// still renders the graph correctly instead of silently dropping the node.
func (n Node) toModel() model.TopologyNode {
	kind := string(n.Kind)
	unresolved := n.Unresolved || n.Kind == NodeKindSegment
	if unresolved {
		kind = string(NodeKindNetwork)
	}
	out := model.TopologyNode{
		ID:              n.ID,
		Kind:            kind,
		Device:          n.Device,
		Interface:       n.Interface,
		Network:         n.Network,
		State:           n.State,
		StateReason:     n.StateReason,
		StateReasonCode: n.StateReasonCode,
		PrimaryIP:       n.PrimaryIP,
		PrimaryMAC:      n.PrimaryMAC,
		Addresses:       n.Addresses,
		Children:        n.Children,
		Unresolved:      unresolved,
	}
	// A segment node has no device, but the UI still needs a label. The reason
	// already carries one; the state stays unknown on purpose: a segment is not
	// online or offline, it is simply unaccounted for.
	if out.State == "" {
		out.State = model.StateUnknown
	}
	// A network node must carry a network payload, or the UI has nothing to
	// label it with. Segments already set one; this is the safety net.
	if out.Kind == string(NodeKindNetwork) && out.Network == nil {
		out.Network = &model.NetworkStatus{
			ID:          n.ID,
			Name:        n.Reason,
			Role:        model.InterfaceRoleUnknown,
			InterfaceID: "",
		}
	}
	return out
}

// toModel renders one edge.
func (e Edge) toModel() model.TopologyEdge {
	return model.TopologyEdge{
		ID:                e.ID,
		SourceID:          e.Source,
		TargetID:          e.Target,
		SourceInterfaceID: e.SourceInterfaceID,
		TargetInterfaceID: e.TargetInterfaceID,
		EdgeType:          e.Type,
		DiscoverySource:   e.DiscoverySource,
		Confidence:        e.Confidence,
		UserConfirmed:     e.UserConfirmed,
		Locked:            e.Locked,
		Status:            e.Status,
		Label:             e.PortLabel(),
		EvidenceCount:     e.EvidenceCount,
		Unverified:        e.Unverified,
		LastObserved:      e.LastObserved,
	}
}

// PortLabel returns the label the UI should show.
//
// The stored label wins (the user may have renamed the link); otherwise the port
// name is the most useful thing to display, because it is what the user sees on
// the hardware.
func (e Edge) PortLabel() string {
	if e.Label != "" {
		return e.Label
	}
	return e.Port
}

// ToModelLayout converts layouts from the layout engine into the API type.
func ToModelLayout(placements []Placement) []model.TopologyLayout {
	out := make([]model.TopologyLayout, 0, len(placements))
	for _, p := range placements {
		out = append(out, model.TopologyLayout{
			NodeID: p.NodeID,
			X:      p.X,
			Y:      p.Y,
			Pinned: p.Pinned,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// FromModelLayout converts persisted layouts into the layout engine's input.
func FromModelLayout(layouts []model.TopologyLayout) []Placement {
	out := make([]Placement, 0, len(layouts))
	for _, l := range layouts {
		out = append(out, Placement{
			NodeID: l.NodeID,
			X:      l.X,
			Y:      l.Y,
			Pinned: l.Pinned,
		})
	}
	return out
}
