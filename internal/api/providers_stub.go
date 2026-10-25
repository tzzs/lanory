package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// This file implements the provider interfaces from the database alone. It is
// the fallback for when no engine is injected: the internal/api tests, and any
// build that does not call newSchedulers. Production injects the real engines
// through the App setters in cmd/lanory/engines.go.
//
// It derives every value it returns from rows that were already persisted:
//
//   - no invented online/offline state: a device keeps whatever state the last
//     writer recorded;
//   - no invented edges: only edges stored in topology_edges are returned;
//   - no invented evidence: only rows in discovery_evidence are returned;
//   - no invented collectors: the list reflects what the detected platform
//     supports.
//
// Two capabilities have no engine behind them, so the store-only
// implementations here serve production as well: effective settings
// (storeSettings) and interface status. Operations the store alone cannot
// perform — starting a scan, running a probe, computing a layout — return
// errProviderUnwired, which the HTTP layer turns into 501.

// errProviderUnwired is returned by operations that need a real engine.
//
// The HTTP layer translates it into 501 Not Implemented, which is honest: the
// route exists, the capability behind it does not.
var errProviderUnwired = errors.New("capability requires a discovery engine that is not wired")

// storeTopology is the store-only TopologyProvider.
type storeTopology struct {
	store *database.Store
}

// newStoreTopology returns a TopologyProvider backed by the store.
func newStoreTopology(store *database.Store) TopologyProvider {
	return &storeTopology{store: store}
}

// Build assembles a topology from the devices, edges and layout in the store.
//
// Nodes are devices plus one synthetic node per logical network. Children are
// derived from stored edges only: an edge source is the parent of its target.
// Nothing is inferred, so the result is exactly what has been recorded.
func (s *storeTopology) Build() (*model.Topology, error) {
	devices, _, err := s.store.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 500})
	if err != nil {
		return nil, err
	}
	edges, err := s.store.ListEdges(database.EdgeFilter{IncludeUserDefined: true})
	if err != nil {
		return nil, err
	}
	layout, err := s.store.ListLayout()
	if err != nil {
		return nil, err
	}

	byID := make(map[string]*model.Device, len(devices))
	for i := range devices {
		byID[devices[i].ID] = &devices[i]
	}

	children := map[string][]string{}
	seen := map[string]map[string]bool{}
	for _, e := range edges {
		if seen[e.SourceID] == nil {
			seen[e.SourceID] = map[string]bool{}
		}
		if seen[e.SourceID][e.TargetID] {
			continue
		}
		seen[e.SourceID][e.TargetID] = true
		children[e.SourceID] = append(children[e.SourceID], e.TargetID)
	}

	nodes := make([]model.TopologyNode, 0, len(devices))
	for i := range devices {
		d := devices[i]
		node := model.TopologyNode{
			ID:          d.ID,
			Kind:        "device",
			Device:      &d,
			State:       d.State,
			StateReason: d.StateReason,
			PrimaryIP:   d.PrimaryIPv4,
			PrimaryMAC:  d.PrimaryMAC,
			Children:    children[d.ID],
		}
		nodes = append(nodes, node)
	}

	// Network nodes: one per interface that carries an address. A network node
	// groups the devices whose addresses share its prefix.
	for _, n := range networkNodes(devices) {
		nodes = append(nodes, n)
	}

	if nodes == nil {
		nodes = []model.TopologyNode{}
	}
	if edges == nil {
		edges = []model.TopologyEdge{}
	}
	if layout == nil {
		layout = []model.TopologyLayout{}
	}

	return &model.Topology{
		Nodes:       nodes,
		Edges:       edges,
		Layout:      layout,
		GeneratedAt: database.Now(),
	}, nil
}

// networkNodes derives one synthetic node per logical network from the stored
// interface addresses.
//
// This is derived, not invented: a node exists only when a device with a stored
// IPv4 address exists, and its position in the address space comes from that
// address.
func networkNodes(devices []model.Device) []model.TopologyNode {
	var out []model.TopologyNode
	for i := range devices {
		d := &devices[i]
		if d.IsHidden || d.PrimaryIPv4 == "" {
			continue
		}
		addr, err := ParseIPv4(d.PrimaryIPv4)
		if err != nil {
			continue
		}
		status := model.NetworkStatus{
			ID:        "net-" + strings.ReplaceAll(addr.String(), ".", "-"),
			Name:      networkName(d),
			CIDR:      cidr24(addr.As4()),
			GatewayIP: addr.String(),
			Role:      model.InterfaceRoleLAN,
		}
		out = append(out, model.TopologyNode{
			ID:      status.ID,
			Kind:    "network",
			Network: &status,
			State:   model.StateUnknown,
		})
	}
	return out
}

// networkName names a network node after its gateway device.
func networkName(d *model.Device) string {
	if d.Name != "" {
		return d.Name
	}
	return d.ID
}

// cidr24 returns the /24 containing addr.
func cidr24(addr [4]byte) string {
	return fmt.Sprintf("%d.%d.%d.0/24", addr[0], addr[1], addr[2])
}

// Evidence returns stored edges with their stored evidence.
//
// Unmapped devices are devices with no edge at all. No evidence is fabricated:
// a device with no evidence rows appears with an empty list, which the UI
// renders as "unverified".
func (s *storeTopology) Evidence() (*model.TopologyEvidence, error) {
	edges, err := s.store.ListEdges(database.EdgeFilter{IncludeUserDefined: true})
	if err != nil {
		return nil, err
	}
	devices, _, err := s.store.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 500})
	if err != nil {
		return nil, err
	}

	connected := map[string]bool{}
	out := make([]model.EdgeEvidence, 0, len(edges))
	for _, e := range edges {
		connected[e.SourceID] = true
		connected[e.TargetID] = true
		ev, err := s.store.ListEvidence("edge", e.ID)
		if err != nil {
			return nil, err
		}
		if ev == nil {
			ev = []model.DiscoveryEvidence{}
		}
		out = append(out, model.EdgeEvidence{Edge: e, Evidence: ev})
	}

	unmapped := make([]model.DeviceEvidence, 0)
	for i := range devices {
		d := devices[i]
		if connected[d.ID] {
			continue
		}
		ev, err := s.store.ListEvidence("device", d.ID)
		if err != nil {
			return nil, err
		}
		addrs, err := s.store.ListAddresses(d.ID)
		if err != nil {
			return nil, err
		}
		if ev == nil {
			ev = []model.DiscoveryEvidence{}
		}
		if addrs == nil {
			addrs = []model.DeviceAddress{}
		}
		unmapped = append(unmapped, model.DeviceEvidence{Device: d, Evidence: ev, Addresses: addrs})
	}

	return &model.TopologyEvidence{Edges: out, Unmapped: unmapped}, nil
}

// AutoLayout returns the persisted layout.
//
// Computing a real layout is the topology engine's job; until it is wired this
// method reports that the capability is unavailable instead of inventing
// positions that would then be persisted as if the user had placed them.
func (s *storeTopology) AutoLayout(string) ([]model.TopologyLayout, error) {
	return nil, errProviderUnwired
}

// storeDiscovery is the store-only DiscoveryProvider.
//
// It reports what the detected platform supports and what the store contains,
// but cannot start a scan: that needs the collectors.
type storeDiscovery struct {
	store    *database.Store
	platform platformInfo
}

// platformInfo is the slice of internal/platform the API needs. Declaring it
// here avoids importing the platform package from the handler files, and lets
// tests substitute a fake.
type platformInfo interface {
	IsOpenWrt() bool
	Supports(name string) bool
	Describe() string
}

// newStoreDiscovery returns a DiscoveryProvider backed by the store.
func newStoreDiscovery(store *database.Store, p platformInfo) DiscoveryProvider {
	return &storeDiscovery{store: store, platform: p}
}

// Status reports the discovery state.
//
// Running is false and CurrentJobID is empty because nothing can run yet.
// LastJob is nil: the store keeps discovery jobs in memory in the real engine,
// and inventing a completed job would be a lie. Interval and Enabled come from
// the configuration.
func (s *storeDiscovery) Status() (*model.DiscoveryStatus, error) {
	collectors, err := s.Collectors()
	if err != nil {
		return nil, err
	}
	return &model.DiscoveryStatus{
		Running:    false,
		LastJob:    nil,
		Collectors: collectors,
	}, nil
}

// Collectors lists the collectors the detected platform supports.
//
// Capabilities and the supported flag come from platform detection; nothing is
// guessed. LastRunAt is zero because no collector has run yet.
func (s *storeDiscovery) Collectors() ([]model.CollectorStatus, error) {
	names := []string{
		model.CollectorOpenWRT, model.CollectorInterfaces, model.CollectorARP, model.CollectorNDP,
		model.CollectorDHCP, model.CollectorBridgeFDB, model.CollectorLLDP,
		model.CollectorMDNS, model.CollectorScan,
	}
	out := make([]model.CollectorStatus, 0, len(names))
	for _, name := range names {
		supported := true
		if s.platform != nil {
			supported = s.platform.Supports(name)
		}
		out = append(out, model.CollectorStatus{
			Name:         name,
			Supported:    supported,
			Capabilities: capabilitiesFor(name),
			Description:  descriptionFor(name),
			Enabled:      supported,
		})
	}
	return out, nil
}

// capabilitiesFor lists what a collector can provide.
func capabilitiesFor(name string) []string {
	switch name {
	case model.CollectorOpenWRT:
		return []string{"interfaces", "ipv4", "ipv6", "mac", "hostname"}
	case model.CollectorInterfaces:
		return []string{"interfaces"}
	case model.CollectorARP:
		return []string{"ipv4", "mac"}
	case model.CollectorNDP:
		return []string{"ipv6", "mac"}
	case model.CollectorDHCP:
		return []string{"ipv4", "mac", "hostname"}
	case model.CollectorBridgeFDB:
		return []string{"mac"}
	case model.CollectorLLDP:
		return []string{"links", "vendor"}
	case model.CollectorMDNS:
		return []string{"hostname", "vendor"}
	case model.CollectorScan:
		return []string{"ipv4", "hostname"}
	default:
		return []string{}
	}
}

// descriptionFor explains a collector in one line.
func descriptionFor(name string) string {
	switch name {
	case model.CollectorOpenWRT:
		return "reads ubus/UCI network state on OpenWrt"
	case model.CollectorInterfaces:
		return "reads interface state, addresses and counters"
	case model.CollectorARP:
		return "reads the IPv4 neighbour table"
	case model.CollectorNDP:
		return "reads the IPv6 neighbour table"
	case model.CollectorDHCP:
		return "reads DHCP leases"
	case model.CollectorBridgeFDB:
		return "reads the bridge forwarding database to map MACs to ports"
	case model.CollectorLLDP:
		return "reads LLDP neighbour advertisements"
	case model.CollectorMDNS:
		return "listens for mDNS announcements"
	case model.CollectorScan:
		return "actively probes the authorised subnets"
	default:
		return ""
	}
}

// Scan is not available: it needs the collectors.
func (s *storeDiscovery) Scan(context.Context, ScanRequest) (*model.DiscoveryJob, error) {
	return nil, errProviderUnwired
}

// Job is not available: jobs live in the engine.
func (s *storeDiscovery) Job(string) (*model.DiscoveryJob, error) {
	return nil, errProviderUnwired
}

// Cancel is not available: nothing can be running.
func (s *storeDiscovery) Cancel(string) error {
	return errProviderUnwired
}

// storeMonitoring is the store-only MonitoringProvider.
type storeMonitoring struct {
	store    *database.Store
	platform platformInfo
}

// newStoreMonitoring returns a MonitoringProvider backed by the store.
func newStoreMonitoring(store *database.Store, p platformInfo) MonitoringProvider {
	return &storeMonitoring{store: store, platform: p}
}

// Status returns the probes with their stored results and the interfaces with
// their last observed state.
//
// Loss and latency are computed from stored results; when a probe has never run
// they are zero, which the UI renders as "no data" rather than "0% loss".
func (s *storeMonitoring) Status() (*model.MonitoringStatus, error) {
	probes, err := s.store.ListProbes()
	if err != nil {
		return nil, err
	}
	interfaces, err := s.InterfaceStatus()
	if err != nil {
		return nil, err
	}

	out := make([]model.ProbeStatus, 0, len(probes))
	for _, p := range probes {
		recent, err := s.store.RecentProbeResults(p.ID, 20)
		if err != nil {
			return nil, err
		}
		if recent == nil {
			recent = []model.ProbeResult{}
		}
		var (
			failures int
			latency  int
			latencyN int
		)
		for _, r := range recent {
			if !r.Success {
				failures++
			}
			if r.LatencyMS > 0 {
				latency += r.LatencyMS
				latencyN++
			}
		}
		avg := 0
		if latencyN > 0 {
			avg = latency / latencyN
		}
		loss := 0.0
		if len(recent) > 0 {
			loss = float64(failures) / float64(len(recent)) * 100
		}
		out = append(out, model.ProbeStatus{
			Probe:        p,
			Recent:       recent,
			LossPct:      loss,
			AvgLatencyMS: avg,
		})
	}

	return &model.MonitoringStatus{
		Probes:      out,
		Interfaces:  interfaces,
		GeneratedAt: database.Now(),
	}, nil
}

// Run is not available: executing a probe needs the probe engine.
func (s *storeMonitoring) Run(context.Context, string) (*model.ProbeResult, error) {
	return nil, errProviderUnwired
}

// InterfaceStatus reports every stored interface with a derived status.
//
// Status is derived from the last observed carrier and oper_state, and is
// labelled "unverified" whenever the counters have never been refreshed, so the
// UI cannot present a guess as a measurement.
func (s *storeMonitoring) InterfaceStatus() ([]model.InterfaceStatus, error) {
	ifaces, err := listAllInterfaces(s.store)
	if err != nil {
		return nil, err
	}
	out := make([]model.InterfaceStatus, 0, len(ifaces))
	for _, i := range ifaces {
		status := model.EdgeStatusUnverified
		reason := "no reachability data has been collected yet"
		switch {
		case !i.Carrier && i.OperState == model.OperNoCarrier:
			status = model.EdgeStatusLinkDown
			reason = "no carrier"
		case i.Carrier && i.OperState == model.OperUp:
			status = model.EdgeStatusLinkUp
			reason = "carrier up"
		}
		out = append(out, model.InterfaceStatus{
			Interface: i,
			Role:      i.Role,
			Status:    status,
			Reason:    reason,
		})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Interface.Name < out[b].Interface.Name })
	return out, nil
}

// storeSettings implements SettingsProvider on top of app_settings.
type storeSettings struct {
	store *database.Store
	base  map[string]string
}

// newStoreSettings returns a SettingsProvider seeded from the configuration.
func newStoreSettings(store *database.Store, base map[string]string) SettingsProvider {
	// Copy the seed so later configuration changes cannot mutate it.
	seed := make(map[string]string, len(base))
	for k, v := range base {
		seed[k] = v
	}
	return &storeSettings{store: store, base: seed}
}

// All returns the effective settings: configuration defaults overlaid with the
// database overrides, which win.
func (s *storeSettings) All() (map[string]string, error) {
	out := make(map[string]string, len(s.base)+16)
	for k, v := range s.base {
		out[k] = v
	}
	rows, err := s.store.ListSettings()
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// Patch applies a partial update. Only known keys are accepted so a typo cannot
// silently create a setting that nothing reads.
func (s *storeSettings) Patch(patch map[string]any) (map[string]string, error) {
	if len(patch) == 0 {
		return s.All()
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !isKnownSetting(k) {
			return nil, fmt.Errorf("%w: unknown setting %q", ErrInvalid, k)
		}
		if err := s.store.SetSetting(k, settingValue(patch[k])); err != nil {
			return nil, err
		}
	}
	return s.All()
}

// isKnownSetting reports whether key is a setting Lanory understands.
func isKnownSetting(key string) bool {
	_, ok := knownSettings[key]
	return ok
}

// settingValue renders a setting value as the string the store keeps.
func settingValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		// JSON numbers decode as float64; render integers without a fraction.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// storeDemo is the store-only DemoProvider: it reports the configured demo mode
// but cannot load a dataset, because that is the demo package's job.
type storeDemo struct {
	enabled bool
	seed    string
}

// newStoreDemo returns a DemoProvider reflecting the configuration.
func newStoreDemo(enabled bool, seed string) DemoProvider {
	return &storeDemo{enabled: enabled, seed: seed}
}

// Enabled reports whether demo mode is configured.
func (d *storeDemo) Enabled() bool { return d.enabled }

// Seed is not available until internal/demo is wired.
func (d *storeDemo) Seed(context.Context, string) error { return errProviderUnwired }

// timeNow is the time source used by the store-only providers.
var timeNow = time.Now
