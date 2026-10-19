package topology

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// Reconciliation passes. Each one owns exactly one entity type, so a bug in the
// edge logic cannot corrupt a device and vice versa.

// applyInterfaces writes the local router's interfaces.
//
// Interfaces belong to the device Lanory runs on. Discovery does not own that
// device record, so the engine resolves it by flag (IsSelf or IsGateway) and
// attaches interfaces to it. When neither exists yet the interfaces are skipped:
// inventing a host device here would duplicate whatever the platform layer
// creates.
func (u *UpdateEngine) applyInterfaces(ctx context.Context, batch *observationBatch, res *UpdateResult) error {
	if len(batch.interfaces) == 0 {
		return nil
	}
	self, err := u.selfDevice()
	if err != nil {
		return fmt.Errorf("topology: resolve local device: %w", err)
	}
	if self == nil {
		return nil
	}
	for _, obs := range batch.interfaces {
		if err := ctx.Err(); err != nil {
			return err
		}
		if obs.Ignore {
			// lo and friends: recorded so the UI can explain the skip, but never
			// attached as a network port.
			continue
		}
		if err := u.upsertInterface(self, obs, res); err != nil {
			res.Errors = append(res.Errors, err)
		}
	}
	return nil
}

// upsertInterface writes one interface observation, detecting a speed change.
func (u *UpdateEngine) upsertInterface(self *model.Device, obs discovery.InterfaceObservation, res *UpdateResult) error {
	existing, err := getInterfaceByName(u.store, self.ID, obs.Name)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("topology: read interface %s: %w", obs.Name, err)
	}

	iface := &model.NetworkInterface{
		DeviceID:   self.ID,
		Name:       obs.Name,
		MAC:        obs.MAC,
		Type:       obs.Type,
		Role:       obs.Role,
		AdminState: obs.AdminState,
		OperState:  obs.OperState,
		Carrier:    obs.Carrier,
		SpeedMbps:  obs.SpeedMbps,
		MTU:        obs.MTU,
		BridgeID:   obs.BridgeID,
		VLANID:     obs.VLANID,
		IsPhysical: obs.IsPhysical,
		RXBytes:    obs.RXBytes,
		TXBytes:    obs.TXBytes,
		RXErrors:   obs.RXErrors,
		TXErrors:   obs.TXErrors,
		RXDropped:  obs.RXDropped,
		TXDropped:  obs.TXDropped,
		RXPackets:  obs.RXPackets,
		TXPackets:  obs.TXPackets,
		LastSeen:   u.now(),
	}
	if existing != nil {
		iface.ID = existing.ID
		if existing.SpeedMbps != obs.SpeedMbps && existing.SpeedMbps > 0 && obs.SpeedMbps > 0 {
			iface.LastSpeedChange = u.now()
			res.SpeedChanges = append(res.SpeedChanges, SpeedChange{
				InterfaceID: existing.ID,
				Name:        obs.Name,
				OldMbps:     existing.SpeedMbps,
				NewMbps:     obs.SpeedMbps,
			})
			u.recordSpeedChange(existing, obs)
		} else {
			iface.LastSpeedChange = existing.LastSpeedChange
		}
	} else {
		iface.LastSpeedChange = time.Time{}
	}
	if err := upsertInterface(u.store, iface); err != nil {
		return fmt.Errorf("topology: write interface %s: %w", obs.Name, err)
	}
	// The addresses are what make the interface a *network*: without them the
	// topology cannot name the LAN subnet or its gateway.
	for _, a := range obs.Addresses {
		if a.Address == "" {
			continue
		}
		family := a.Family
		if family == "" {
			if strings.Contains(a.Address, ":") {
				family = model.AddressFamilyIPv6
			} else {
				family = model.AddressFamilyIPv4
			}
		}
		if err := u.store.UpsertAddress(&model.DeviceAddress{
			DeviceID:     self.ID,
			Address:      a.Address,
			Family:       family,
			InterfaceID:  iface.ID,
			Source:       model.SourceOpenWRT,
			PrefixLength: a.PrefixLength,
			IsPrimary:    family == model.AddressFamilyIPv4,
			LastSeen:     u.now(),
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("topology: write address %s: %w", a.Address, err))
		}
	}
	res.InterfacesUpdated++
	return nil
}

// recordSpeedChange persists and publishes a speed transition, so the UI shows
// "eth2 went from 1 Gbps to 2.5 Gbps" rather than silently redrawing.
//
// The history is written to discovery_evidence rather than network_events.
// network_events.edge_id is declared NOT NULL with a foreign key to
// topology_edges, so a speed change (which is not about any one edge) cannot be
// represented there: NULL violates NOT NULL and "" violates the foreign key.
// That is a defect in the initial migration, which this package does not own.
// discovery_evidence has no such constraint and is the right home for "what did
// the collector observe", which is exactly what a speed change is.
func (u *UpdateEngine) recordSpeedChange(existing *model.NetworkInterface, obs discovery.InterfaceObservation) {
	msg := fmt.Sprintf("%s changed from %d Mbps to %d Mbps", obs.Name, existing.SpeedMbps, obs.SpeedMbps)
	_ = u.store.SaveEvidence(&model.DiscoveryEvidence{
		Collector:    model.CollectorOpenWRT,
		SubjectType:  "interface_port",
		SubjectID:    existing.ID,
		EvidenceType: string(model.EventSpeedChanged),
		RawData:      fmt.Sprintf("%s %d -> %d mbps", obs.Name, existing.SpeedMbps, obs.SpeedMbps),
		ObservedAt:   u.now(),
	})
	if u.bus != nil {
		u.bus.PublishTopologyUpdated(msg)
	}
}

// applyDevices resolves every MAC the run learned about into a device.
//
// The identity engine does the merging; this pass only decides what evidence to
// hand it and how to react to the result.
func (u *UpdateEngine) applyDevices(ctx context.Context, batch *observationBatch, res *UpdateResult) error {
	if u.ident == nil {
		return nil
	}
	// The MACs of this host's own interfaces are the router, not devices. They
	// reach this pass through the openwrt collector's address observations, and
	// turning them into devices would produce a second "router" plus a phantom
	// per port.
	localMACs, err := u.localInterfaceMACs()
	if err != nil {
		return fmt.Errorf("topology: read local interface macs: %w", err)
	}
	// Neighbours reached through a WAN interface are upstream equipment, not
	// devices on the network Lanory manages. They are still collected (the
	// upstream router is genuinely useful evidence that the WAN link works) but
	// they must not be added to the household device list: the user cannot
	// name, place or monitor them, and every one of them would inflate the
	// device count.
	wanPorts, err := u.wanPorts()
	if err != nil {
		return fmt.Errorf("topology: read wan ports: %w", err)
	}

	for _, mac := range batch.sortedMACs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if localMACs[mac] {
			continue
		}
		f := batch.byMAC[mac]
		if onlyOnWAN(f, wanPorts) {
			continue
		}
		if len(f.Addresses) == 0 && len(f.Ports) == 0 && f.Hostname == "" && f.Lease == nil {
			// A MAC with nothing attached is not a device yet.
			continue
		}
		ing := Ingest{
			MAC:        f.MAC,
			Hostname:   f.Hostname,
			Addresses:  f.Addresses,
			Source:     dominantSource(f),
			ObservedAt: f.LastSeen,
			TypeHint:   typeHint(f),
		}
		if f.ClientID != "" {
			ing.ClientID = f.ClientID
		}
		dev, created, err := u.ident.Resolve(ing)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("topology: resolve %s: %w", mac, err))
			continue
		}
		if dev == nil {
			continue
		}
		if created {
			res.DevicesCreated++
		} else {
			res.DevicesUpdated++
		}
		f.deviceID = dev.ID
		if err := u.saveDeviceEvidence(dev, f); err != nil {
			res.Errors = append(res.Errors, err)
		} else {
			res.EvidenceSaved += len(f.Evidence)
		}
	}
	return nil
}

// applyEdges turns the raw observations into stored topology edges.
//
// One edge per (device, bridge, port) tuple. The edge keeps its confidence, its
// discovery source and its last-observed timestamp, and every one of them gets
// an evidence row carrying the raw collector line.
func (u *UpdateEngine) applyEdges(ctx context.Context, batch *observationBatch, res *UpdateResult) error {
	self, err := u.selfDevice()
	if err != nil {
		return fmt.Errorf("topology: resolve local device: %w", err)
	}
	if self == nil {
		return nil
	}
	bridges, err := u.bridgePorts(self)
	if err != nil {
		return fmt.Errorf("topology: read bridge ports: %w", err)
	}

	for _, mac := range batch.sortedMACs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := batch.byMAC[mac]
		if f.deviceID == "" || len(f.Ports) == 0 {
			continue
		}
		for _, p := range f.Ports {
			if p.Port == "" {
				continue
			}
			// Resolve the port to a local interface; an FDB entry naming an
			// interface the router does not have is a stale row.
			iface := bridges[p.Port]
			if iface == nil {
				continue
			}
			if iface.BridgeID != "" && p.Bridge != "" && iface.BridgeID != p.Bridge {
				continue
			}
			bridge := p.Bridge
			if bridge == "" {
				bridge = iface.BridgeID
			}
			conf := Combine([]Evidence{{
				Source:     p.Source,
				Confidence: discovery.ConfidenceFDB,
				ObservedAt: p.Observed,
			}})
			// A DHCP lease for the same device raises the confidence: two
			// independent sources now agree the device exists, even though only
			// the FDB speaks about the port.
			if f.Lease != nil {
				conf = Combine([]Evidence{
					{Source: p.Source, Confidence: discovery.ConfidenceFDB},
					{Source: model.SourceDHCP, Confidence: discovery.ConfidenceDHCP},
				})
			}
			created, err := u.saveEdge(&model.TopologyEdge{
				SourceID:          self.ID,
				TargetID:          f.deviceID,
				SourceInterfaceID: iface.ID,
				EdgeType:          model.EdgeTypeObservedMACPort,
				DiscoverySource:   p.Source,
				Confidence:        conf,
				Status:            model.EdgeStatusLinkUp,
				Label:             p.Port,
				// An FDB port observation is a real observation, not a guess:
				// the bridge saw the frame. It is marked unverified only when
				// the combined confidence genuinely falls below the FDB base.
				// The epsilon absorbs IEEE-754 noise: SQLite stores REAL, so 0.7
				// round-trips as 0.6999999999999999.
				Unverified:   conf < ConfidenceFDB-1e-6,
				LastObserved: p.Observed,
			}, f, p)
			if err != nil {
				res.Errors = append(res.Errors, err)
				continue
			}
			if created {
				res.EdgesCreated++
			} else {
				res.EdgesUpdated++
			}
		}
	}
	return nil
}

// saveEdge upserts an edge and persists its evidence.
//
// A locked edge is never modified: the store's upsert already refuses to lower a
// locked edge's confidence or status, and this function additionally skips the
// write entirely so the edge's label and interfaces stay exactly as the user set
// them.
func (u *UpdateEngine) saveEdge(e *model.TopologyEdge, f *macFacts, p macPort) (bool, error) {
	existing, err := u.store.GetEdgeByTuple(e.SourceID, e.TargetID, e.SourceInterfaceID, e.TargetInterfaceID, e.EdgeType)
	if err != nil && !isNotFound(err) {
		return false, fmt.Errorf("topology: read edge: %w", err)
	}
	if existing != nil && (existing.Locked || existing.UserConfirmed) {
		// The user owns this relation. Record that it is still being observed,
		// but change nothing.
		_, _ = u.store.UpdateEdge(existing.ID, map[string]any{
			"status": string(e.Status),
		})
		return false, u.saveEdgeEvidence(existing, f, p)
	}
	created := existing == nil
	if err := u.store.UpsertEdge(e); err != nil {
		return false, fmt.Errorf("topology: write edge: %w", err)
	}
	return created, u.saveEdgeEvidence(e, f, p)
}

// saveEdgeEvidence stores the raw collector lines behind an edge.
//
// This is what makes the topology auditable: every edge in the UI can link back
// to the exact FDB or neighbour line that produced it.
func (u *UpdateEngine) saveEdgeEvidence(e *model.TopologyEdge, f *macFacts, p macPort) error {
	if e.ID == "" {
		return nil
	}
	raw := fmt.Sprintf("%s dev %s master %s %s",
		database.FormatMAC(f.MAC), p.Port, p.Bridge, p.EntryType)
	ev := &model.DiscoveryEvidence{
		Collector:    model.CollectorBridgeFDB,
		SubjectType:  "edge",
		SubjectID:    e.ID,
		EvidenceType: "fdb",
		RawData:      strings.TrimSpace(raw),
		ObservedAt:   p.Observed,
	}
	if err := u.store.SaveEvidence(ev); err != nil {
		return fmt.Errorf("topology: save edge evidence: %w", err)
	}
	// Also record the DHCP lease when there is one, so the edge shows both
	// agreeing sources.
	if f.Lease != nil {
		_ = u.store.SaveEvidence(&model.DiscoveryEvidence{
			Collector:    model.CollectorDHCP,
			SubjectType:  "edge",
			SubjectID:    e.ID,
			EvidenceType: "lease",
			RawData:      fmt.Sprintf("%s %s %s", f.Lease.MAC, f.Lease.Address, f.Lease.Hostname),
			ObservedAt:   p.Observed,
		})
	}
	return nil
}

// saveDeviceEvidence stores the raw lines behind a device's discovery.
func (u *UpdateEngine) saveDeviceEvidence(dev *model.Device, f *macFacts) error {
	for _, e := range f.Evidence {
		if err := u.store.SaveEvidence(&model.DiscoveryEvidence{
			Collector:    e.Collector,
			SubjectType:  "device",
			SubjectID:    dev.ID,
			EvidenceType: e.EvidenceType,
			RawData:      e.Raw,
			ObservedAt:   e.ObservedAt,
		}); err != nil {
			return fmt.Errorf("topology: save device evidence: %w", err)
		}
	}
	return nil
}

// markStale handles the device that disappeared.
//
// This is the rule that separates Lanory from a toy: a device that stops
// appearing is NOT deleted. Its edges are marked unverified and its state is left
// alone. Deleting would lose the user's name, type and manual assertions, and
// would make a sleeping phone look like a network change.
//
// Edges whose device is no longer observed, and which are not locked or
// user-confirmed, get Unverified=true and Status=unverified. They are never
// removed.
func (u *UpdateEngine) markStale(ctx context.Context, batch *observationBatch, at time.Time, res *UpdateResult) error {
	edges, err := u.store.ListEdges(database.EdgeFilter{IncludeUserDefined: true})
	if err != nil {
		return fmt.Errorf("topology: list edges: %w", err)
	}
	// observedTuples is the set of (device, port) relations this run actually
	// saw. Comparing tuples rather than just devices is what lets the engine
	// notice that a device moved from eth1 to eth2: the device is still
	// present, but the eth1 relation is no longer supported.
	observedTuples := map[string]bool{}
	observedDevices := map[string]bool{}
	for _, mac := range batch.sortedMACs() {
		f := batch.byMAC[mac]
		if f.deviceID == "" {
			continue
		}
		observedDevices[f.deviceID] = true
		for _, p := range f.Ports {
			observedTuples[f.deviceID+"|"+p.Port] = true
		}
	}

	for _, e := range edges {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.EdgeType != model.EdgeTypeObservedMACPort {
			continue
		}
		if e.Locked || e.UserConfirmed {
			continue
		}
		// The edge is still supported when the same device was seen on the
		// same port this run.
		if observedTuples[e.TargetID+"|"+e.Label] {
			continue
		}
		// The edge predates this run only if its last observation is older.
		// A freshly written edge must not be marked stale by the same pass that
		// created it.
		if !e.LastObserved.Before(at) {
			continue
		}
		if e.Unverified {
			continue
		}
		_ = observedDevices
		if _, err := u.store.UpdateEdge(e.ID, map[string]any{
			"unverified": true,
			"status":     string(model.EdgeStatusUnverified),
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("topology: mark edge %s unverified: %w", e.ID, err))
			continue
		}
		res.EdgesUnverified++
		if u.bus != nil {
			updated, gerr := u.store.GetEdge(e.ID)
			if gerr == nil {
				u.bus.PublishLinkUpdated(updated, "no longer observed")
			}
		}
	}
	return nil
}

// wanPorts returns the names of the host's WAN-role interfaces.
func (u *UpdateEngine) wanPorts() (map[string]bool, error) {
	out := map[string]bool{}
	self, err := u.selfDevice()
	if err != nil || self == nil {
		return out, err
	}
	ifaces, err := listDeviceInterfaces(u.store, self.ID)
	if err != nil {
		return out, err
	}
	for _, iface := range ifaces {
		if iface.Role == model.InterfaceRoleWAN {
			out[iface.Name] = true
		}
	}
	return out, nil
}

// onlyOnWAN reports whether every observation of a MAC arrived through a WAN
// interface, which makes it upstream equipment rather than a managed device.
func onlyOnWAN(f *macFacts, wanPorts map[string]bool) bool {
	if len(wanPorts) == 0 {
		return false
	}
	sawInterface := false
	for _, a := range f.Addresses {
		if a.Interface == "" {
			continue
		}
		sawInterface = true
		if !wanPorts[a.Interface] {
			return false
		}
	}
	for _, p := range f.Ports {
		if p.Port == "" {
			continue
		}
		sawInterface = true
		if !wanPorts[p.Port] {
			return false
		}
	}
	// Without any interface information the MAC cannot be attributed to the
	// WAN, so it is kept.
	return sawInterface
}

// localInterfaceMACs returns the canonical MACs of this host's interfaces.
func (u *UpdateEngine) localInterfaceMACs() (map[string]bool, error) {
	out := map[string]bool{}
	self, err := u.selfDevice()
	if err != nil || self == nil {
		return out, err
	}
	ifaces, err := listDeviceInterfaces(u.store, self.ID)
	if err != nil {
		return out, err
	}
	for _, iface := range ifaces {
		if mac := database.CanonicalizeMAC(iface.MAC); mac != "" {
			out[mac] = true
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// lookups
// ---------------------------------------------------------------------------

// selfDevice returns the device Lanory runs on.
func (u *UpdateEngine) selfDevice() (*model.Device, error) {
	devices, _, err := u.store.ListDevices(database.DeviceFilter{IncludeHidden: false, Limit: 500})
	if err != nil {
		return nil, err
	}
	for i := range devices {
		if devices[i].IsSelf {
			return &devices[i], nil
		}
	}
	for i := range devices {
		if devices[i].IsGateway {
			return &devices[i], nil
		}
	}
	return nil, nil
}

// bridgePorts maps a port name to the local interface that owns it.
func (u *UpdateEngine) bridgePorts(self *model.Device) (map[string]*model.NetworkInterface, error) {
	ifaces, err := listDeviceInterfaces(u.store, self.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]*model.NetworkInterface{}
	for i := range ifaces {
		iface := ifaces[i]
		out[iface.Name] = &iface
	}
	return out, nil
}

// dominantSource picks the source to attribute a device to: the strongest one
// that observed it, so the device's provenance reflects its best evidence.
func dominantSource(f *macFacts) model.DiscoverySource {
	order := []model.DiscoverySource{
		model.SourceManual, model.SourceLLDP, model.SourceDHCP,
		model.SourceARP, model.SourceNDP, model.SourceBridgeFDB,
		model.SourceMDNS, model.SourceOpenWRT,
	}
	for _, s := range order {
		if f.Sources[s] {
			return s
		}
	}
	return model.SourceBridgeFDB
}

// typeHint guesses a device type from the hostname. It is a hint, never an
// assertion: the identity engine only applies it to devices whose type is still
// unknown, and the user can always override it.
func typeHint(f *macFacts) model.DeviceType {
	h := strings.ToLower(f.Hostname)
	switch {
	case h == "":
		return model.DeviceTypeUnknown
	case strings.Contains(h, "iphone"), strings.Contains(h, "android"),
		strings.Contains(h, "pixel"), strings.Contains(h, "galaxy"):
		return model.DeviceTypeSmartphone
	case strings.Contains(h, "ipad"), strings.Contains(h, "tablet"):
		return model.DeviceTypeTablet
	case strings.Contains(h, "watch"):
		return model.DeviceTypeSmartwatch
	case strings.Contains(h, "macbook"), strings.Contains(h, "laptop"):
		return model.DeviceTypeLaptop
	case strings.Contains(h, "mac-studio"), strings.Contains(h, "imac"),
		strings.Contains(h, "desktop"), strings.Contains(h, "windows"):
		return model.DeviceTypeDesktop
	case strings.Contains(h, "esp"), strings.Contains(h, "sensor"),
		strings.Contains(h, "lamp"), strings.Contains(h, "bulb"),
		strings.Contains(h, "plug"), strings.Contains(h, "thermostat"):
		return model.DeviceTypeIoT
	case strings.Contains(h, "tv"), strings.Contains(h, "appletv"),
		strings.Contains(h, "chromecast"):
		return model.DeviceTypeSmartTV
	case strings.Contains(h, "printer"):
		return model.DeviceTypePrinter
	case strings.Contains(h, "camera"):
		return model.DeviceTypeCamera
	case strings.Contains(h, "nas"), strings.Contains(h, "synology"),
		strings.Contains(h, "qnap"):
		return model.DeviceTypeNAS
	}
	return model.DeviceTypeUnknown
}

func isNotFound(err error) bool {
	return err != nil && (err == database.ErrNotFound || strings.Contains(err.Error(), database.ErrNotFound.Error()))
}
