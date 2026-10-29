package topology

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Options control how the graph is built.
type Options struct {
	// UnverifiedEdgeThreshold: edges below this confidence are marked
	// unverified and drawn dashed.
	UnverifiedEdgeThreshold float64
	// Now is the timestamp used for the build; overridden in tests.
	Now time.Time
}

// DefaultOptions returns the options matching config.Default().
func DefaultOptions() Options {
	return Options{
		UnverifiedEdgeThreshold: 0.6,
		Now:                     time.Now(),
	}
}

// StoreReader is the slice of the store the engine needs.
//
// Declaring it as an interface (rather than taking *database.Store) keeps the
// engine testable and makes the dependency explicit: the engine reads devices,
// their interfaces, their addresses and the edges, and it writes nothing.
type StoreReader interface {
	// ListDevices returns every device, including hidden ones when asked.
	ListDevices(f database.DeviceFilter) ([]model.Device, int, error)
	// ListAllInterfaces returns every interface of every device.
	ListAllInterfaces() ([]model.NetworkInterface, error)
	// ListAddresses returns the addresses of one device.
	ListAddresses(deviceID string) ([]model.DeviceAddress, error)
	// ListActiveAddresses returns the addresses worth displaying: recently
	// observed, primary, or manual.
	ListActiveAddresses(deviceID string, now time.Time) ([]model.DeviceAddress, error)
	// ListEdges returns the topology edges.
	ListEdges(f database.EdgeFilter) ([]model.TopologyEdge, error)
	// ListIdentities returns the identities of one device.
	ListIdentities(deviceID string) ([]model.DeviceIdentity, error)
}

// Builder turns store data into a graph.
type Builder struct {
	store StoreReader
	opts  Options
}

// NewBuilder returns a builder reading through store.
func NewBuilder(store StoreReader) *Builder {
	return &Builder{store: store, opts: DefaultOptions()}
}

// WithOptions replaces the builder options.
func (b *Builder) WithOptions(o Options) *Builder {
	b.opts = o
	return b
}

// Build assembles the topology graph.
//
// The build order matters:
//
//  1. Every non-hidden device becomes a node.
//  2. Each bridge (or interface carrying an IP) becomes a network node.
//  3. Stored edges become graph edges, carrying their user-confirmed and locked
//     flags through untouched.
//  4. Port observations (observed_mac_port edges) decide each device's parent:
//     the bridge port it was seen on, and ultimately the device that owns the
//     port.
//  5. Devices observed on a port with no known intermediate device are grouped
//     under an unresolved-segment node.
//  6. Devices that could not be placed are reported as unmapped.
func (b *Builder) Build() (*Graph, error) {
	now := b.opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	devices, _, err := b.store.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 2000})
	if err != nil {
		return nil, err
	}
	// Interfaces are read through the package's own helper because
	// database.Store.ListAllInterfaces emits malformed SQL (see ifaces.go).
	interfaces, err := b.listInterfaces()
	if err != nil {
		return nil, err
	}
	// Attach each interface's addresses, which network nodes need in order to
	// report a CIDR and a gateway.
	if err := b.attachAddresses(interfaces); err != nil {
		return nil, err
	}
	edges, err := b.store.ListEdges(database.EdgeFilter{IncludeUserDefined: true})
	if err != nil {
		return nil, err
	}

	idx := newNodeIndex()
	ifacesByDevice := groupInterfaces(interfaces)

	// 1. Device nodes.
	for i := range devices {
		d := devices[i]
		// Node cards and the inspector show current addresses, not the full
		// ARP history. Old observations stay in the database for evidence.
		addrs, _ := b.store.ListActiveAddresses(d.ID, now)
		node := deviceNode(d, ifacesByDevice[d.ID], addrs, now)
		idx.add(node)
	}

	// 2. Network nodes: bridges and interfaces that carry an IP address.
	networks := buildNetworks(devices, interfaces)
	for _, n := range networks {
		idx.add(n)
	}

	// 3. Stored edges.
	//
	// The store records the interface a relation leaves from; the physical port
	// and its bridge are resolved here so the placement logic and the UI both
	// have them without re-reading the interface table.
	ifaceByID := map[string]model.NetworkInterface{}
	for _, iface := range interfaces {
		ifaceByID[iface.ID] = iface
	}
	graphEdges := make([]Edge, 0, len(edges))
	for _, e := range edges {
		src := deviceNodeID(e.SourceID)
		dst := deviceNodeID(e.TargetID)
		if idx.get(src) == nil || idx.get(dst) == nil {
			// An edge pointing at a hidden or deleted device cannot be drawn.
			continue
		}
		port, bridge := resolvePort(e, ifaceByID, interfaces)
		graphEdges = append(graphEdges, Edge{
			ID:                e.ID,
			Source:            src,
			Target:            dst,
			SourceInterfaceID: e.SourceInterfaceID,
			TargetInterfaceID: e.TargetInterfaceID,
			Type:              e.EdgeType,
			Status:            e.Status,
			Confidence:        e.Confidence,
			UserConfirmed:     e.UserConfirmed,
			Locked:            e.Locked,
			// An edge at or below the threshold has not earned a solid line.
			// The threshold is inclusive on purpose: mDNS (0.60) sits exactly
			// there and must be drawn dashed.
			Unverified:      e.Unverified || e.Confidence <= b.opts.UnverifiedEdgeThreshold,
			Label:           e.Label,
			Port:            port,
			Bridge:          bridge,
			DiscoverySource: e.DiscoverySource,
			LastObserved:    e.LastObserved,
			EvidenceCount:   e.EvidenceCount,
		})
	}

	// 4-6. Placement.
	assignParents(idx, devices, interfaces, graphEdges)

	graph := &Graph{
		Nodes:       idx.sortedNodes(),
		Edges:       graphEdges,
		GeneratedAt: now,
	}
	SortNodes(graph.Nodes)
	// 7. Fill the Children fields once the tree is final.
	PopulateChildren(graph)
	return graph, nil
}

// attachAddresses fills in the Addresses field of every interface.
//
// database.Store.ListAddresses works (it is only the interface queries that are
// malformed), so the addresses come from the store directly.
func (b *Builder) attachAddresses(interfaces []model.NetworkInterface) error {
	byDevice := map[string][]model.DeviceAddress{}
	for i := range interfaces {
		ifaces := &interfaces[i]
		if ifaces.DeviceID == "" {
			continue
		}
		addrs, ok := byDevice[ifaces.DeviceID]
		if !ok {
			var err error
			addrs, err = b.store.ListAddresses(ifaces.DeviceID)
			if err != nil {
				return fmt.Errorf("topology: list addresses for %s: %w", ifaces.DeviceID, err)
			}
			byDevice[ifaces.DeviceID] = addrs
		}
		for _, a := range addrs {
			if a.InterfaceID != "" && a.InterfaceID != ifaces.ID {
				continue
			}
			ifaces.Addresses = append(ifaces.Addresses, a)
		}
	}
	return nil
}

// listInterfaces returns every interface of every device.
//
// It prefers the store method and falls back to the package's own query when the
// store one fails, so the builder keeps working whichever way
// internal/database/store.go evolves.
func (b *Builder) listInterfaces() ([]model.NetworkInterface, error) {
	if store, ok := b.store.(*database.Store); ok {
		return listAllInterfaces(store)
	}
	return b.store.ListAllInterfaces()
}

// ---------------------------------------------------------------------------
// node construction
// ---------------------------------------------------------------------------

func deviceNode(d model.Device, ifaces []model.NetworkInterface, addrs []model.DeviceAddress, now time.Time) Node {
	node := Node{
		ID:          deviceNodeID(d.ID),
		Kind:        NodeKindDevice,
		Device:      &d,
		State:       d.State,
		StateReason: d.StateReason,
		PrimaryMAC:  d.PrimaryMAC,
	}
	if node.PrimaryMAC == "" {
		// Fall back to the first interface MAC.
		for _, iface := range ifaces {
			if iface.MAC != "" {
				node.PrimaryMAC = iface.MAC
				break
			}
		}
	}
	// Primary IP: the device's own cached value, else the first address.
	node.PrimaryIP = d.PrimaryIPv4
	if node.PrimaryIP == "" {
		for _, a := range addrs {
			if a.Family == model.AddressFamilyIPv4 {
				node.PrimaryIP = a.Address
				break
			}
		}
	}
	if node.PrimaryIP == "" {
		for _, a := range addrs {
			if a.Family == model.AddressFamilyIPv6 && !isLinkLocal(a.Address) {
				node.PrimaryIP = a.Address
				break
			}
		}
	}
	// Every address, tagged with its interface and the side that interface
	// belongs to. A router is the case this exists for: 192.168.100.1 (br-lan,
	// lan) and 192.168.1.4 (eth0, wan) are the same device, and a caller that
	// only sees PrimaryIP cannot tell the two apart.
	node.Addresses = collectAddresses(d, ifaces, addrs, node.PrimaryIP)
	node.Reason = describePlacement(d, ifaces, addrs)
	_ = now
	return node
}

// collectAddresses builds the tagged address list of a node.
//
// Addresses come from two places and both are needed:
//
//   - device_addresses rows (what the collectors observed for this device),
//   - the addresses attached to the device's own interfaces (what the local
//     interface configuration says).
//
// For a remote device only the first exists. For the router the two overlap and
// the interface copy wins, because only it knows which interface — and therefore
// which side (lan/wan) — carries the address.
//
// Link-local IPv6 addresses are skipped: they are per-interface plumbing, not a
// network membership, and listing them would drown the meaningful addresses.
func collectAddresses(d model.Device, ifaces []model.NetworkInterface, addrs []model.DeviceAddress, primary string) []model.NodeAddress {
	ifaceByID := make(map[string]model.NetworkInterface, len(ifaces))
	for _, iface := range ifaces {
		ifaceByID[iface.ID] = iface
	}

	out := make([]model.NodeAddress, 0, len(addrs)+len(ifaces))
	seen := map[string]int{} // address -> index in out

	add := func(a model.DeviceAddress, iface *model.NetworkInterface) {
		if a.Address == "" || isLinkLocal(a.Address) {
			return
		}
		side := model.InterfaceRoleUnknown
		ifaceName := ""
		if iface != nil {
			ifaceName = iface.Name
			if iface.Role != "" {
				side = iface.Role
			}
		}
		entry := model.NodeAddress{
			Address:      a.Address,
			Family:       a.Family,
			PrefixLength: a.PrefixLength,
			CIDR:         subnetOf(a.Address, a.PrefixLength),
			Interface:    ifaceName,
			Side:         side,
			IsPrimary:    a.Address == primary,
		}
		if entry.Family == "" {
			if strings.Contains(a.Address, ":") {
				entry.Family = model.AddressFamilyIPv6
			} else {
				entry.Family = model.AddressFamilyIPv4
			}
		}
		if i, ok := seen[entry.Address]; ok {
			// An interface-backed entry is strictly more informative than a bare
			// device_addresses row: keep it, but do not lose a primary flag.
			if entry.Interface != "" {
				out[i].Interface = entry.Interface
				out[i].Side = entry.Side
				if entry.PrefixLength > 0 {
					out[i].PrefixLength = entry.PrefixLength
					out[i].CIDR = entry.CIDR
				}
			}
			out[i].IsPrimary = out[i].IsPrimary || entry.IsPrimary
			return
		}
		seen[entry.Address] = len(out)
		out = append(out, entry)
	}

	// Device-level addresses first: they cover remote devices.
	for _, a := range addrs {
		var iface *model.NetworkInterface
		if a.InterfaceID != "" {
			if found, ok := ifaceByID[a.InterfaceID]; ok {
				iface = &found
			}
		}
		add(a, iface)
	}
	// Interface addresses second: they add the side a bare address row lacks.
	for _, iface := range ifaces {
		for _, a := range iface.Addresses {
			add(a, &iface)
		}
	}

	return out
}

// subnetOf renders the network an address belongs to, or "" when the prefix is
// unknown. Addresses sharing a subnet therefore share this value, which is what
// lets the UI group a multi-homed node's addresses by network.
func subnetOf(address string, prefix int) string {
	if prefix <= 0 || prefix > 128 {
		return ""
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return ""
	}
	prefixLen := prefix
	if addr.Is4() && prefixLen > 32 {
		// A v4-mapped prefix wider than 32 bits is nonsense for a v4 address.
		return ""
	}
	return netip.PrefixFrom(addr, prefixLen).Masked().String()
}

// describePlacement renders a human readable explanation of how the device was
// observed, for the evidence panel.
func describePlacement(d model.Device, ifaces []model.NetworkInterface, addrs []model.DeviceAddress) string {
	var parts []string
	if len(d.DiscoverySources) > 0 {
		srcs := make([]string, 0, len(d.DiscoverySources))
		for _, s := range d.DiscoverySources {
			srcs = append(srcs, string(s))
		}
		parts = append(parts, "observed by "+strings.Join(srcs, ", "))
	}
	if d.PrimaryMAC != "" {
		parts = append(parts, "mac "+d.PrimaryMAC)
	}
	if d.PrimaryIPv4 != "" {
		parts = append(parts, "ipv4 "+d.PrimaryIPv4)
	} else if d.PrimaryIPv6 != "" {
		parts = append(parts, "ipv6 "+d.PrimaryIPv6)
	}
	if len(ifaces) > 0 {
		parts = append(parts, "local interfaces "+strings.Join(interfaceNames(ifaces), ", "))
	}
	if len(parts) == 0 {
		return "manually created device"
	}
	return strings.Join(parts, "; ")
}

func interfaceNames(ifaces []model.NetworkInterface) []string {
	out := make([]string, 0, len(ifaces))
	for _, i := range ifaces {
		out = append(out, i.Name)
	}
	return out
}

// buildNetworks derives the logical network nodes.
//
// Only an interface that carries an address without being a bridge becomes a
// node. A bridge is the device's own LAN side: br-lan on OpenWrt is not a
// second box, it is the router. Its ports and the devices behind them already
// hang off the router node, so rendering it separately produced a card that
// belonged to nothing in the tree and fell into the "other networks" pile.
func buildNetworks(devices []model.Device, interfaces []model.NetworkInterface) []Node {
	byDevice := groupInterfaces(interfaces)
	var out []Node
	seen := map[string]bool{}

	for _, d := range devices {
		if d.IsHidden {
			continue
		}
		for _, iface := range byDevice[d.ID] {
			if iface.Type == model.InterfaceTypeBridge || !hasIP(iface) {
				continue
			}
			if seen[iface.BridgeID+iface.Name] {
				continue
			}
			seen[iface.BridgeID+iface.Name] = true
			out = append(out, networkNode(d, iface))
		}
	}
	return out
}

func hasIP(iface model.NetworkInterface) bool {
	for _, a := range iface.Addresses {
		if a.Address != "" {
			return true
		}
	}
	return false
}

func networkNode(owner model.Device, iface model.NetworkInterface) Node {
	name := iface.Name
	if iface.BridgeID != "" {
		name = iface.BridgeID
	}
	status := model.NetworkStatus{
		ID:          name,
		Name:        name,
		InterfaceID: iface.ID,
		Role:        iface.Role,
	}
	if cidr := cidrOf(iface); cidr != "" {
		status.CIDR = cidr
	}
	// The network's own address is the gateway for everything on it.
	if len(iface.Addresses) > 0 {
		status.GatewayIP = firstAddress(iface.Addresses)
	}

	return Node{
		ID:        networkNodeID(name),
		Kind:      NodeKindNetwork,
		Device:    &owner,
		Interface: &iface,
		Network:   &status,
		State:     networkState(iface),
		Addresses: collectAddresses(owner, []model.NetworkInterface{iface}, nil, status.GatewayIP),
		Reason:    "network carried by " + iface.Name + " of " + owner.Name,
	}
}

// networkState reports the reachability of a logical network.
//
// A network is a subnet carried by an interface, not a box, so it never reports
// "offline" — that would be a device-shaped lie. It is online while the
// interface that carries it has a carrier, and unknown otherwise (an
// administratively down interface, or one whose cable is unplugged).
func networkState(iface model.NetworkInterface) model.DeviceState {
	if iface.AdminState == model.AdminDown {
		return model.StateUnknown
	}
	if iface.Carrier {
		return model.StateOnline
	}
	return model.StateUnknown
}

func cidrOf(iface model.NetworkInterface) string {
	for _, a := range iface.Addresses {
		if a.Family == model.AddressFamilyIPv4 && a.PrefixLength > 0 {
			return a.Address + "/" + strconv.Itoa(a.PrefixLength)
		}
	}
	return ""
}

func firstAddress(addrs []model.DeviceAddress) string {
	for _, a := range addrs {
		if a.Family == model.AddressFamilyIPv4 {
			return a.Address
		}
	}
	if len(addrs) > 0 {
		return addrs[0].Address
	}
	return ""
}

func groupInterfaces(interfaces []model.NetworkInterface) map[string][]model.NetworkInterface {
	out := map[string][]model.NetworkInterface{}
	for _, iface := range interfaces {
		out[iface.DeviceID] = append(out[iface.DeviceID], iface)
	}
	return out
}

// isLinkLocal reports whether an IPv6 address is link-local.
func isLinkLocal(addr string) bool { return strings.HasPrefix(addr, "fe80:") }

// resolvePort determines the physical port and bridge an observed_mac_port edge
// refers to.
//
// Two conventions reach the engine:
//
//   - The ingest layer stores the bridge port interface id in
//     SourceInterfaceID, so the port is the interface name and the bridge is
//     that interface's BridgeID.
//   - A hand-written or legacy edge carries the port name in the label.
//
// Both are handled, and the result is empty when the relation is not a port
// observation at all.
func resolvePort(e model.TopologyEdge, ifaceByID map[string]model.NetworkInterface, interfaces []model.NetworkInterface) (port, bridge string) {
	if e.EdgeType != model.EdgeTypeObservedMACPort {
		return "", ""
	}
	if iface, ok := ifaceByID[e.SourceInterfaceID]; ok {
		port = iface.Name
		bridge = iface.BridgeID
		if bridge == "" && iface.Type == model.InterfaceTypeBridge {
			bridge = iface.Name
		}
	}
	if port == "" {
		port = e.Label
	}
	if bridge == "" && port != "" {
		for _, iface := range interfaces {
			if iface.Name == port && iface.BridgeID != "" {
				bridge = iface.BridgeID
				break
			}
		}
	}
	return port, bridge
}
