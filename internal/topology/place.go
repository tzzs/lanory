package topology

import (
	"sort"
	"strings"

	"github.com/lanory/lanory/internal/model"
)

// assignParents decides where every device node hangs in the tree.
//
// The decision ladder, strongest evidence first:
//
//  1. An observed_mac_port edge — the device hangs under the device that owns
//     the bridge port it was learned on. This is the primary placement.
//  2. A stored physical_link edge (LLDP confirmed the neighbour) — the device
//     hangs under its neighbour.
//  3. A user_defined edge — the user's assertion wins over everything computed.
//  4. A logical_network edge — the device hangs under the network node it shares
//     an address with. This is the weakest relation and is marked unverified
//     unless a stronger edge corroborates it.
//
// The critical rule: when several devices are observed behind the SAME bridge
// port and no known intermediate device explains the sharing, they are grouped
// under an unresolved-segment node. The engine does not create a switch or an
// access point to make the tree look tidy — that would be a fabricated device
// the user then has to delete.
//
// Every parent value stored here is a NODE ID ("dev-…", "net-…", "seg-…"), never
// a raw device ID. Mixing the two is the classic way to end up with a tree where
// nothing is a child of anything.
func assignParents(idx *nodeIndex, devices []model.Device, interfaces []model.NetworkInterface, edges []Edge) {
	byID := map[string]*model.Device{}
	for i := range devices {
		byID[devices[i].ID] = &devices[i]
	}

	ifaceByID := map[string]model.NetworkInterface{}
	portsByName := map[string]model.NetworkInterface{}
	// bridges maps a bridge name to the device that owns it.
	bridgeOwner := map[string]string{}
	for _, iface := range interfaces {
		ifaceByID[iface.ID] = iface
		if iface.BridgeID != "" {
			if cp := portsByName[iface.Name]; cp.ID == "" {
				portsByName[iface.Name] = iface
			}
			if _, ok := bridgeOwner[iface.BridgeID]; !ok {
				bridgeOwner[iface.BridgeID] = iface.DeviceID
			}
		}
		if iface.Type == model.InterfaceTypeBridge {
			if _, ok := bridgeOwner[iface.Name]; !ok {
				bridgeOwner[iface.Name] = iface.DeviceID
			}
		}
	}

	// The bridge a port belongs to, or "" when unknown.
	bridgeOf := func(port string) string {
		if iface, ok := portsByName[port]; ok && iface.BridgeID != "" {
			return iface.BridgeID
		}
		return ""
	}

	// Port observations: which device was learned on which (bridge, port).
	type portObs struct {
		bridge string
		port   string
		devID  string
		conf   float64
	}
	var obs []portObs
	seenObs := map[string]bool{}
	for _, e := range edges {
		if e.Type != model.EdgeTypeObservedMACPort {
			continue
		}
		// The relation may be stored in either direction; the device is the
		// endpoint that is a known device and is NOT the bridge owner.
		srcDev := strings.TrimPrefix(e.Source, "dev-")
		dstDev := strings.TrimPrefix(e.Target, "dev-")

		port := e.Port
		if port == "" {
			port = e.Label
		}
		bridge := e.Bridge
		if iface, ok := ifaceByID[e.SourceInterfaceID]; ok {
			if port == "" {
				port = iface.Name
			}
			if bridge == "" && iface.BridgeID != "" {
				bridge = iface.BridgeID
			}
		}
		if bridge == "" && port != "" {
			bridge = bridgeOf(port)
		}

		// Pick the device side: the endpoint that is not the bridge owner.
		devID := ""
		owner := bridgeOwner[bridge]
		switch {
		case srcDev != "" && srcDev != owner:
			devID = srcDev
		case dstDev != "" && dstDev != owner:
			devID = dstDev
		case dstDev != "":
			devID = dstDev
		default:
			devID = srcDev
		}
		if devID == "" {
			continue
		}
		key := devID + "|" + bridge + "|" + port
		if seenObs[key] {
			continue
		}
		seenObs[key] = true
		obs = append(obs, portObs{bridge: bridge, port: port, devID: devID, conf: e.Confidence})
	}

	parent := map[string]string{}   // device ID -> parent node ID
	reason := map[string]string{}   // device ID -> explanation
	unverified := map[string]bool{} // device ID -> placement is weak

	// 1. Port observations place a device under the bridge owner.
	for _, o := range obs {
		owner := bridgeOwner[o.bridge]
		if owner == "" || owner == o.devID {
			continue
		}
		parentNode := deviceNodeID(owner)
		if existing, ok := parent[o.devID]; ok && existing != parentNode {
			// Two ports claim the same device. The first placement stands; the
			// conflict is recorded so the evidence panel can explain it.
			reason[o.devID] += "; conflicting port observations"
			unverified[o.devID] = true
			continue
		}
		parent[o.devID] = parentNode
		if o.port != "" {
			reason[o.devID] = "observed on port " + o.port + " of " + deviceDisplay(byID[owner], owner)
		} else {
			reason[o.devID] = "observed behind " + deviceDisplay(byID[owner], owner)
		}
		unverified[o.devID] = o.conf < ConfidenceFDB
	}

	// 2. Confirmed physical links and user assertions override the computed
	//    placement. A user assertion is never downgraded.
	for _, e := range edges {
		srcDev := strings.TrimPrefix(e.Source, "dev-")
		dstDev := strings.TrimPrefix(e.Target, "dev-")
		if _, ok := byID[srcDev]; !ok {
			continue
		}
		if _, ok := byID[dstDev]; !ok {
			continue
		}
		switch e.Type {
		case model.EdgeTypeUserDefined:
			parent[dstDev] = deviceNodeID(srcDev)
			reason[dstDev] = "user confirmed relation"
			unverified[dstDev] = false
		case model.EdgeTypePhysicalLink:
			// LLDP confirmed adjacency: only place the device when nothing
			// stronger (a port observation) already did.
			if _, ok := parent[dstDev]; !ok {
				parent[dstDev] = deviceNodeID(srcDev)
				reason[dstDev] = "confirmed adjacent device"
				unverified[dstDev] = false
			}
		}
	}

	// 3. Logical network membership, for devices that have no placement yet.
	for _, e := range edges {
		if e.Type != model.EdgeTypeLogicalNetwork {
			continue
		}
		var devID, netName string
		if strings.HasPrefix(e.Source, "dev-") {
			devID = strings.TrimPrefix(e.Source, "dev-")
			netName = strings.TrimPrefix(e.Target, "net-")
		} else {
			devID = strings.TrimPrefix(e.Target, "dev-")
			netName = strings.TrimPrefix(e.Source, "net-")
		}
		if devID == "" || netName == "" {
			continue
		}
		if _, ok := parent[devID]; ok {
			continue
		}
		if idx.get(networkNodeID(netName)) == nil {
			continue
		}
		parent[devID] = networkNodeID(netName)
		reason[devID] = "shares the " + netName + " subnet"
		unverified[devID] = true
	}

	// 4. Apply the placements.
	for devID, parentNode := range parent {
		node := idx.get(deviceNodeID(devID))
		if node == nil || node.Parent != "" {
			continue
		}
		node.Parent = parentNode
		node.Unverified = unverified[devID]
		if node.Reason == "" {
			node.Reason = reason[devID]
		}
	}

	// 5. Group devices that share a port with no known intermediate.
	byPort := map[string][]string{}
	for _, o := range obs {
		if o.port == "" {
			continue
		}
		key := o.bridge + "/" + o.port
		byPort[key] = append(byPort[key], o.devID)
	}
	for _, seg := range groupUnresolved(idx, byPort, byID, bridgeOwner) {
		idx.add(seg)
	}
}

// deviceDisplay returns a human readable name for the evidence panel.
func deviceDisplay(d *model.Device, fallback string) string {
	if d != nil && d.Name != "" {
		return d.Name
	}
	return fallback
}

// groupUnresolved finds the ports where several devices were observed and no
// known intermediate device accounts for them, and creates a segment node for
// each.
//
// "Known intermediate" means a device the engine has a record of and whose type
// explains a fan-out: a switch, an access point, a mesh node or a router. When
// one of the devices on the port is switch-like, the others hang under it and
// no segment is created. Everything else becomes a segment, which the UI labels
// "Unresolved network segment" instead of inventing hardware.
func groupUnresolved(
	idx *nodeIndex,
	byPort map[string][]string,
	byID map[string]*model.Device,
	bridgeOwner map[string]string,
) []Node {
	var out []Node

	keys := make([]string, 0, len(byPort))
	for k := range byPort {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		devIDs := byPort[key]
		if len(devIDs) < 2 {
			continue
		}
		// A switch-like device explains the fan-out only when it is itself
		// known to sit on this port (it is one of devIDs) or when it is the
		// only switch-like device among the devices placed behind the bridge
		// owner. The second case covers a switch whose own FDB entry carried
		// no port name, which happens when the switch's MAC was learned on the
		// bridge rather than on a member port.
		var known []string
		for _, id := range devIDs {
			if d := byID[id]; d != nil && isSwitchLike(d) {
				known = append(known, id)
			}
		}
		if len(known) == 0 {
			known = switchLikeBehindBridge(byID, bridgeOwner, key, devIDs)
		}
		if len(known) > 0 {
			// A known switch or AP explains the fan-out.
			for _, id := range devIDs {
				if id == known[0] {
					continue
				}
				node := idx.get(deviceNodeID(id))
				if node != nil {
					node.Parent = deviceNodeID(known[0])
					node.Reason = "behind the known " + string(byID[known[0]].Type) + " " + deviceDisplay(byID[known[0]], known[0])
					node.Unverified = false
				}
			}
			continue
		}

		bridge, port := splitPortKey(key)
		if port == "" {
			continue
		}
		segID := segmentNodeID(bridge, port)
		if idx.get(segID) != nil {
			continue
		}
		owner := bridgeOwner[bridge]
		if owner == "" {
			continue
		}

		seg := Node{
			ID:          segID,
			Kind:        NodeKindSegment,
			Parent:      deviceNodeID(owner),
			State:       model.StateUnknown,
			StateReason: "unknown intermediate device",
			Reason:      "several devices observed behind " + port + "; the intermediate device is not known",
			Unverified:  true,
			Unresolved:  true,
			Bridge:      bridge,
			Port:        port,
		}
		seg.Network = &model.NetworkStatus{
			ID:   segID,
			Name: "Unresolved segment on " + port,
			Role: model.InterfaceRoleUnknown,
		}

		for _, id := range devIDs {
			node := idx.get(deviceNodeID(id))
			if node == nil {
				continue
			}
			node.Parent = segID
			node.Reason = "observed behind " + port + " through a device the engine did not identify"
			node.Unverified = true
			node.Bridge = bridge
			node.Port = port
		}
		out = append(out, seg)
	}
	return out
}

// switchLikeBehindBridge finds a switch-like device that is known to sit behind
// the same bridge, used when the switch's own FDB entry did not carry a port.
//
// It deliberately returns at most one candidate: two known switches behind one
// bridge do not tell us which one hosts the other devices, so the engine falls
// back to an unresolved segment rather than guessing.
func switchLikeBehindBridge(byID map[string]*model.Device, bridgeOwner map[string]string, key string, devIDs []string) []string {
	bridge, _ := splitPortKey(key)
	owner := bridgeOwner[bridge]
	onPort := map[string]bool{}
	for _, id := range devIDs {
		onPort[id] = true
	}
	var candidates []string
	for id, d := range byID {
		if id == owner || onPort[id] || !isSwitchLike(d) {
			continue
		}
		candidates = append(candidates, id)
	}
	if len(candidates) == 1 {
		return candidates
	}
	return nil
}

// isSwitchLike reports whether a device plausibly explains a fan-out on one
// port: a switch, an access point, a mesh node, a router or a firewall.
func isSwitchLike(d *model.Device) bool {
	switch d.Type {
	case model.DeviceTypeSwitch, model.DeviceTypeWirelessAP,
		model.DeviceTypeMeshNode, model.DeviceTypeRouter,
		model.DeviceTypeFirewall:
		return true
	}
	return false
}

func splitPortKey(key string) (bridge, port string) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// ChildrenOf returns the direct children of a node ID.
func ChildrenOf(graph *Graph, nodeID string) []Node {
	var out []Node
	for _, n := range graph.Nodes {
		if n.Parent == nodeID {
			out = append(out, n)
		}
	}
	return out
}

// Depth returns the distance from the root, or -1 for an unknown node.
func Depth(graph *Graph, nodeID string) int {
	depth := 0
	current := nodeID
	seen := map[string]bool{}
	for {
		if seen[current] {
			return -1 // cycle guard
		}
		seen[current] = true
		node := findNode(graph, current)
		if node == nil || node.Parent == "" {
			return depth
		}
		current = node.Parent
		depth++
		if depth > 64 {
			return -1
		}
	}
}

func findNode(graph *Graph, id string) *Node {
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == id {
			return &graph.Nodes[i]
		}
	}
	return nil
}

// PopulateChildren fills the Children field of every node from the Parent
// links. It is called after every build so the payload always carries a usable
// tree, even for nodes the layout could not place.
func PopulateChildren(graph *Graph) {
	children := map[string][]string{}
	for _, n := range graph.Nodes {
		if n.Parent == "" || n.ID == n.Parent {
			continue
		}
		children[n.Parent] = append(children[n.Parent], n.ID)
	}
	for id := range children {
		sort.Strings(children[id])
	}
	for i := range graph.Nodes {
		graph.Nodes[i].Children = children[graph.Nodes[i].ID]
	}
}
