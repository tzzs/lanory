package demo

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// SettingDemoMode is the app_settings key that flags the database as holding
// demo data. The API layer reads it to set Overview.DemoMode and
// HealthResponse.DemoMode; the UI shows the demo banner when it is true.
const SettingDemoMode = "demo_mode"

// SettingDemoSeed is the app_settings key recording which dataset was loaded.
const SettingDemoSeed = "demo_seed"

// SettingDemoLoadedAt is the app_settings key recording when the demo data was
// last (re)loaded.
const SettingDemoLoadedAt = "demo_loaded_at"

// SeedEasePiR1 is the name of the built-in sample dataset, derived from the
// sanitized fixtures in fixtures/easepi-r1.
const SeedEasePiR1 = "easepi-r1"

// DefaultSeed is the dataset used when no seed is given.
const DefaultSeed = SeedEasePiR1

// Options controls how the demo dataset is loaded.
type Options struct {
	// Seed selects the dataset. Empty means DefaultSeed.
	Seed string
	// Reseed drops the existing demo rows before loading again. Without it,
	// Load is idempotent: a second call adds nothing.
	Reseed bool
	// Now overrides the reference time; used by tests. Zero means time.Now().
	Now time.Time
}

// Demo is a loaded demo dataset. It carries the deterministic IDs of the
// created rows so callers (and tests) can inspect the result without guessing.
type Demo struct {
	// Seed is the dataset that was loaded.
	Seed string
	// LoadedAt is when the dataset was loaded.
	LoadedAt time.Time
	// DeviceIDs lists the device IDs in the dataset.
	DeviceIDs []string
	// InterfaceIDs lists the interface IDs in the dataset.
	InterfaceIDs []string
	// EdgeIDs lists the edge IDs in the dataset.
	EdgeIDs []string
	// ProbeIDs lists the probe IDs in the dataset.
	ProbeIDs []string
	// EventCount is the number of history events written.
	EventCount int
	// ProbeResultCount is the number of probe results written.
	ProbeResultCount int
}

// KnownSeeds lists the datasets this package can load.
func KnownSeeds() []string { return []string{SeedEasePiR1} }

// Load populates store with the sample network.
//
// The operation is idempotent: rows with a natural key are written through the
// Store upserts with deterministic IDs, and append-only tables (probe results,
// events, evidence) are only written when the subject has no demo rows yet.
// Loading twice therefore yields exactly the same row counts.
//
// When opts.Reseed is true the previously loaded demo rows are removed first,
// which is how the UI reseeds a polluted database.
//
// Every row written is tagged model.SourceDemo and the "demo_mode" setting is
// set to "1" so the API can report demo mode honestly.
func Load(store *database.Store, opts Options) (*Demo, error) {
	if store == nil {
		return nil, errors.New("demo: nil store")
	}
	seed := strings.TrimSpace(opts.Seed)
	if seed == "" {
		seed = DefaultSeed
	}
	if seed != SeedEasePiR1 {
		return nil, fmt.Errorf("demo: unknown seed %q (known: %s)", seed, strings.Join(KnownSeeds(), ", "))
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if opts.Reseed {
		if err := Reset(store); err != nil {
			return nil, err
		}
	}

	d := &Demo{Seed: seed, LoadedAt: now}

	routerID := demoDevices[0].ID

	// --- devices ----------------------------------------------------------
	ifaceByName := map[string]string{}
	for _, di := range demoInterfaces {
		ifaceByName[di.Name] = di.ID
	}
	for _, dd := range demoDevices {
		dev := &model.Device{
			ID:              dd.ID,
			Name:            dd.Name,
			Hostname:        dd.Hostname,
			Type:            model.DeviceType(dd.Type),
			Manufacturer:    dd.Manufacturer,
			Model:           dd.Model,
			Description:     dd.Description,
			IsManual:        dd.IsManual,
			IsGateway:       dd.IsGateway,
			IsSelf:          dd.IsSelf,
			TypeSource:      model.SourceDemo,
			State:           model.DeviceState(dd.State),
			StateReason:     dd.StateReason,
			LastStateChange: baseTime,
			PrimaryMAC:      dd.MAC,
			PrimaryIPv4:     dd.IPv4,
			PrimaryIPv6:     dd.IPv6,
			FirstSeen:       baseTime,
			LastSeen:        now,
			CreatedAt:       baseTime,
			UpdatedAt:       now,
		}
		if err := store.UpsertDevice(dev); err != nil {
			return nil, fmt.Errorf("demo: device %s: %w", dd.ID, err)
		}
		if err := store.AddDeviceSource(dd.ID, model.SourceDemo); err != nil {
			return nil, fmt.Errorf("demo: device source %s: %w", dd.ID, err)
		}
		d.DeviceIDs = append(d.DeviceIDs, dd.ID)

		if dd.MAC != "" {
			if err := store.UpsertIdentity(&model.DeviceIdentity{
				DeviceID:   dd.ID,
				Type:       model.IdentityMAC,
				Value:      database.CanonicalizeMAC(dd.MAC),
				Source:     model.SourceDemo,
				Confidence: 0.9,
				LastSeen:   now,
			}); err != nil {
				return nil, fmt.Errorf("demo: mac identity %s: %w", dd.ID, err)
			}
		}
		if dd.ClientID != "" {
			if err := store.UpsertIdentity(&model.DeviceIdentity{
				DeviceID:   dd.ID,
				Type:       model.IdentityDHCPClient,
				Value:      dd.ClientID,
				Source:     model.SourceDemo,
				Confidence: 0.95,
				LastSeen:   now,
			}); err != nil {
				return nil, fmt.Errorf("demo: client id %s: %w", dd.ID, err)
			}
		}
		if dd.Hostname != "" {
			if err := store.UpsertIdentity(&model.DeviceIdentity{
				DeviceID:   dd.ID,
				Type:       model.IdentityHostname,
				Value:      dd.Hostname,
				Source:     model.SourceDemo,
				Confidence: 0.4,
				LastSeen:   now,
			}); err != nil {
				return nil, fmt.Errorf("demo: hostname %s: %w", dd.ID, err)
			}
		}
		if dd.Manufacturer != "" {
			if err := store.UpsertIdentity(&model.DeviceIdentity{
				DeviceID:   dd.ID,
				Type:       model.IdentityVendor,
				Value:      dd.Manufacturer,
				Source:     model.SourceDemo,
				Confidence: 0.5,
				LastSeen:   now,
			}); err != nil {
				return nil, fmt.Errorf("demo: vendor %s: %w", dd.ID, err)
			}
		}

		// Addresses. Interface ownership is only recorded for the router
		// itself; for remote devices the "interface" is a port of the router,
		// which the store models as an evidence-backed port observation rather
		// than as an address owner, so the field stays empty.
		if dd.IPv4 != "" {
			if err := store.UpsertAddress(&model.DeviceAddress{
				DeviceID:     dd.ID,
				Address:      dd.IPv4,
				Family:       model.AddressFamilyIPv4,
				Source:       model.SourceDemo,
				IsPrimary:    true,
				PrefixLength: RouterLANPrefix,
				LastSeen:     now,
			}); err != nil {
				return nil, fmt.Errorf("demo: address %s: %w", dd.IPv4, err)
			}
		}
		if dd.IPv6 != "" {
			if err := store.UpsertAddress(&model.DeviceAddress{
				DeviceID:     dd.ID,
				Address:      dd.IPv6,
				Family:       model.AddressFamilyIPv6,
				Source:       model.SourceDemo,
				IsPrimary:    dd.IPv4 == "",
				PrefixLength: 64,
				LastSeen:     now,
			}); err != nil {
				return nil, fmt.Errorf("demo: address %s: %w", dd.IPv6, err)
			}
		}

		// Evidence: the observation that produced this device.
		evidence := []struct{ kind, raw string }{
			{"dhcp_lease", demoEvidenceFor(dd)},
		}
		if dd.NeighborState != "" {
			evidence = append(evidence, struct{ kind, raw string }{
				"neighbour", fmt.Sprintf("%s %s dev %s lladdr %s %s",
					dd.IPv4, dd.Iface, dd.Iface, dd.MAC, strings.ToUpper(dd.NeighborState)),
			})
		}
		if dd.Iface != "" {
			evidence = append(evidence, struct{ kind, raw string }{
				"fdb_entry", fmt.Sprintf("%s dev %s master br-lan", dd.MAC, dd.Iface),
			})
		}
		for _, ev := range evidence {
			if hasEvidence(store, "device", dd.ID, ev.kind) {
				continue
			}
			if err := store.SaveEvidence(&model.DiscoveryEvidence{
				Collector:    "demo",
				SubjectType:  "device",
				SubjectID:    dd.ID,
				EvidenceType: ev.kind,
				RawData:      ev.raw,
				ObservedAt:   now,
			}); err != nil {
				return nil, fmt.Errorf("demo: evidence %s: %w", dd.ID, err)
			}
		}

	}

	// --- interfaces of the local router -----------------------------------
	ifaceByID := map[string]*model.NetworkInterface{}
	for _, di := range demoInterfaces {
		iface := &model.NetworkInterface{
			ID:         di.ID,
			DeviceID:   routerID,
			Name:       di.Name,
			MAC:        di.MAC,
			Type:       model.InterfaceType(di.Type),
			Role:       model.InterfaceRole(di.Role),
			AdminState: model.AdminUp,
			OperState:  model.OperState(di.OperState),
			Carrier:    di.Carrier,
			SpeedMbps:  di.SpeedMbps,
			MTU:        di.MTU,
			BridgeID:   di.BridgeID,
			IsPhysical: di.IsPhysical,
			RXBytes:    di.RXBytes,
			TXBytes:    di.TXBytes,
			LastSeen:   now,
			RXPackets:  di.RXBytes / 1200,
			TXPackets:  di.TXBytes / 1200,
		}
		if di.SpeedMbps > 0 {
			iface.LastSpeedChange = now.Add(-90 * time.Minute)
		}
		if err := upsertInterface(store, iface); err != nil {
			return nil, fmt.Errorf("demo: interface %s: %w", di.Name, err)
		}
		ifaceByID[di.ID] = iface
		d.InterfaceIDs = append(d.InterfaceIDs, di.ID)
	}

	// Interface addresses (attached to the interface, owned by the router).
	for _, di := range demoInterfaces {
		if len(di.Addr) == 0 {
			continue
		}
		for _, a := range di.Addr {
			addr, family, prefix := splitCIDR(a)
			if err := store.UpsertAddress(&model.DeviceAddress{
				DeviceID:     routerID,
				Address:      addr,
				Family:       family,
				InterfaceID:  di.ID,
				Source:       model.SourceDemo,
				PrefixLength: prefix,
				IsPrimary:    addr == RouterLANIPv4,
				LastSeen:     now,
			}); err != nil {
				return nil, fmt.Errorf("demo: interface address %s: %w", addr, err)
			}
		}
	}

	// --- interfaces of the local router -----------------------------------

	// --- port evidence ----------------------------------------------------
	// Written after the interfaces exist so the device filter by interface has
	// something to match: a MAC learned on a bridge port is evidence about the
	// port, not about the interface that owns the bridge.
	for _, dd := range demoDevices {
		if dd.Iface == "" || dd.MAC == "" {
			continue
		}
		ifaceID, ok := ifaceByName[dd.Iface]
		if !ok {
			continue
		}
		// The evidence type carries the MAC so several devices learned on the
		// same port do not collide: discovery_evidence has no natural key, and
		// the store caps a subject at 100 rows.
		evidenceType := "fdb_entry:" + dd.MAC
		if hasEvidence(store, "interface_port", ifaceID, evidenceType) {
			continue
		}
		if err := store.SaveEvidence(&model.DiscoveryEvidence{
			Collector:    "demo",
			SubjectType:  "interface_port",
			SubjectID:    ifaceID,
			EvidenceType: evidenceType,
			RawData:      fmt.Sprintf("%s dev %s master br-lan", dd.MAC, dd.Iface),
			ObservedAt:   now,
		}); err != nil {
			return nil, fmt.Errorf("demo: port evidence %s: %w", dd.Iface, err)
		}
	}

	// --- topology edges ---------------------------------------------------
	for _, de := range demoEdges() {
		edge := &model.TopologyEdge{
			SourceID:          de.SourceID,
			TargetID:          de.TargetID,
			SourceInterfaceID: de.SourceIface,
			TargetInterfaceID: de.TargetIface,
			EdgeType:          model.EdgeType(de.Type),
			DiscoverySource:   model.SourceDemo,
			Confidence:        de.Confidence,
			Status:            model.EdgeStatus(de.Status),
			Label:             de.Label,
			Unverified:        de.Unverified,
			UserConfirmed:     de.Type == string(model.EdgeTypeUserDefined),
			Locked:            de.Type == string(model.EdgeTypeUserDefined),
			EvidenceCount:     1,
			LastObserved:      now,
			CreatedAt:         baseTime,
		}
		if err := store.UpsertEdge(edge); err != nil {
			return nil, fmt.Errorf("demo: edge %s->%s: %w", de.SourceID, de.TargetID, err)
		}
		d.EdgeIDs = append(d.EdgeIDs, edge.ID)
		if hasEvidence(store, "edge", edge.ID, "inference") {
			continue
		}
		if err := store.SaveEvidence(&model.DiscoveryEvidence{
			Collector:    "demo",
			SubjectType:  "edge",
			SubjectID:    edge.ID,
			EvidenceType: "inference",
			RawData:      de.Evidence,
			ObservedAt:   now,
		}); err != nil {
			return nil, fmt.Errorf("demo: edge evidence %s: %w", edge.ID, err)
		}
	}

	// --- probes and results ----------------------------------------------
	for _, dp := range demoProbes() {
		probe := &model.ProbeTarget{
			ID:                   dp.ID,
			Name:                 dp.Name,
			Kind:                 model.ProbeKind(dp.Kind),
			Target:               dp.Target,
			Protocol:             model.ProbeProtocol(dp.Protocol),
			SourceID:             routerID,
			Interval:             dp.Interval,
			Timeout:              dp.Timeout,
			Enabled:              true,
			DeviceID:             dp.DeviceID,
			FailureThreshold:     3,
			RecoverConfirm:       1,
			State:                model.DeviceState(dp.State),
			StateReason:          dp.StateReason,
			ConsecutiveFailures:  0,
			ConsecutiveSuccesses: 0,
			CreatedAt:            baseTime,
			UpdatedAt:            now,
		}
		// The stored results are the source of truth for last_result, so the
		// summary row can never contradict the history.
		last := dp.Results[len(dp.Results)-1]
		probe.LastResult = model.ProbeRunStatus(last.Status)
		probe.LastLatencyMS = last.LatencyMS
		probe.LastError = errorTypeFor(last.Status)
		probe.LastRunAt = now.Add(-30 * time.Second)
		if last.Status == "success" {
			probe.ConsecutiveSuccesses = 4
			probe.LastSuccessAt = probe.LastRunAt
		} else {
			probe.ConsecutiveFailures = 3
		}
		_ = dp.State
		if _, err := store.GetProbe(probe.ID); err != nil {
			if err := store.CreateProbe(probe); err != nil {
				return nil, fmt.Errorf("demo: probe %s: %w", dp.ID, err)
			}
		} else if err := store.UpdateProbeRuntime(probe); err != nil {
			return nil, fmt.Errorf("demo: probe runtime %s: %w", dp.ID, err)
		}
		d.ProbeIDs = append(d.ProbeIDs, probe.ID)

		// Probe results are append-only, so only write them when the probe has
		// none yet. That is what makes a second Load a no-op.
		existing, err := store.RecentProbeResults(probe.ID, 1)
		if err != nil {
			return nil, fmt.Errorf("demo: probe results %s: %w", dp.ID, err)
		}
		if len(existing) > 0 {
			continue
		}
		oldest := now.Add(-time.Duration(len(dp.Results)*dp.Interval) * time.Second)
		for i, r := range dp.Results {
			ts := oldest.Add(time.Duration(i*dp.Interval) * time.Second)
			if err := store.SaveProbeResult(&model.ProbeResult{
				ProbeID:   probe.ID,
				Timestamp: ts,
				Success:   r.Status == "success",
				LatencyMS: r.LatencyMS,
				Status:    model.ProbeRunStatus(r.Status),
				ErrorType: errorTypeFor(r.Status),
			}); err != nil {
				return nil, fmt.Errorf("demo: probe result %s: %w", dp.ID, err)
			}
			d.ProbeResultCount++
		}
	}

	// --- history events ---------------------------------------------------
	for _, de := range demoEvents() {
		n, _, err := store.ListEvents(database.EventFilter{
			DeviceID: de.DeviceID,
			Types:    []model.EventType{model.EventType(de.Type)},
			Limit:    1,
		})
		if err != nil {
			return nil, fmt.Errorf("demo: events %s: %w", de.DeviceID, err)
		}
		if len(n) > 0 {
			continue
		}
		// network_events.edge_id is NOT NULL DEFAULT '' with a foreign key, so
		// an event must reference an existing edge. Resolve the pair to any
		// edge between the two devices, in either direction.
		edgeID, err := findEdgeID(store, de.EdgeRef[0], de.EdgeRef[1])
		if err != nil {
			return nil, fmt.Errorf("demo: event %s: %w", de.DeviceID, err)
		}
		if err := store.SaveEvent(&model.NetworkEvent{
			EventType: model.EventType(de.Type),
			DeviceID:  de.DeviceID,
			EdgeID:    edgeID,
			OldValue:  de.OldValue,
			NewValue:  de.NewValue,
			Message:   de.Message,
			Evidence:  "demo dataset",
			Timestamp: now.Add(-time.Duration(de.MinutesAgo) * time.Minute),
		}); err != nil {
			return nil, fmt.Errorf("demo: event %s: %w", de.DeviceID, err)
		}
		d.EventCount++
	}

	// --- settings ---------------------------------------------------------
	if err := store.SetSetting(SettingDemoMode, "1"); err != nil {
		return nil, fmt.Errorf("demo: setting %s: %w", SettingDemoMode, err)
	}
	if err := store.SetSetting(SettingDemoSeed, seed); err != nil {
		return nil, fmt.Errorf("demo: setting %s: %w", SettingDemoSeed, err)
	}
	if err := store.SetSetting(SettingDemoLoadedAt, now.Format(database.TimeLayout)); err != nil {
		return nil, fmt.Errorf("demo: setting %s: %w", SettingDemoLoadedAt, err)
	}
	return d, nil
}

// Reset removes every demo row from the database and clears the demo settings.
//
// It deletes only rows tagged with the demo source: devices whose only source
// is "demo" (plus their cascade children), edges with discovery_source "demo",
// probes whose ID carries the demo prefix, evidence written by the "demo"
// collector, and events carrying the demo marker.
func Reset(store *database.Store) error {
	if store == nil {
		return errors.New("demo: nil store")
	}
	db := store.DB()

	// Probe results first (they reference probes), then the probes.
	//
	// A probe is demo data when its ID carries the demo prefix, or when it is
	// attached to a device that only demo discovery ever saw: probe_targets
	// .device_id is NOT NULL with an ON DELETE SET NULL foreign key, and SQLite
	// performs the SET NULL as an UPDATE that the NOT NULL constraint rejects,
	// so such a probe would otherwise block deleting its device.
	// A probe counts as demo data when its ID carries the demo prefix. Probes a
	// user attached to a demo device are left alone: deleting them would be
	// surprising, and the FK problem is handled by re-pointing them at the demo
	// router below instead.
	const demoProbeSelector = `id LIKE 'probe-demo-%'`
	if _, err := db.Exec(`DELETE FROM probe_results WHERE probe_id IN (SELECT id FROM probe_targets WHERE ` + demoProbeSelector + `)`); err != nil {
		return fmt.Errorf("demo: reset probe results: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM probe_targets WHERE ` + demoProbeSelector); err != nil {
		return fmt.Errorf("demo: reset probes: %w", err)
	}
	// Evidence written by the demo collector.
	if _, err := db.Exec(`DELETE FROM discovery_evidence WHERE collector = 'demo'`); err != nil {
		return fmt.Errorf("demo: reset evidence: %w", err)
	}
	// Events must go before the edges they reference.
	//
	// network_events.edge_id is declared NOT NULL DEFAULT '' with a foreign key
	// ON DELETE SET NULL. SQLite implements the FK action as an UPDATE to NULL,
	// which the NOT NULL constraint rejects, so deleting an edge while an event
	// still points at it fails with "NOT NULL constraint failed:
	// network_events.edge_id". Deleting the referencing events first is the only
	// order that works without rewriting the schema.
	if _, err := db.Exec(`DELETE FROM network_events WHERE evidence = 'demo dataset'`); err != nil {
		return fmt.Errorf("demo: reset events: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM network_events WHERE edge_id IN
		(SELECT id FROM topology_edges WHERE discovery_source = ?)`, string(model.SourceDemo)); err != nil {
		return fmt.Errorf("demo: reset events for demo edges: %w", err)
	}
	// Demo edges. Edges are deleted explicitly because they are not covered by
	// the ON DELETE CASCADE of devices.
	if _, err := db.Exec(`DELETE FROM topology_edges WHERE discovery_source = ?`, string(model.SourceDemo)); err != nil {
		return fmt.Errorf("demo: reset edges: %w", err)
	}
	// Probes a user attached to a demo-only device must go before the device
	// disappears. probe_targets.device_id is NOT NULL with an ON DELETE SET
	// NULL foreign key, and SQLite implements SET NULL as an UPDATE, which the
	// NOT NULL constraint rejects. There is nowhere honest to re-point them: the
	// device they watched was demonstration data, so the probe is stale demo
	// data too and is removed (its results cascade with it).
	if _, err := db.Exec(`DELETE FROM probe_results WHERE probe_id IN (
		SELECT p.id FROM probe_targets p
		JOIN devices d ON d.id = p.device_id
		WHERE p.id NOT LIKE 'probe-demo-%'
		  AND EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source = 'demo')
		  AND NOT EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source != 'demo'))`); err != nil {
		return fmt.Errorf("demo: reset user probes on demo devices: %w", err)
	}
	if _, err := db.Exec(`DELETE FROM probe_targets WHERE id NOT LIKE 'probe-demo-%' AND device_id IN (
		SELECT d.id FROM devices d
		WHERE EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source = 'demo')
		  AND NOT EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source != 'demo'))`); err != nil {
		return fmt.Errorf("demo: reset user probes on demo devices: %w", err)
	}
	// Devices whose only source is demo (cascades to addresses, identities,
	// interfaces and sources).
	if _, err := db.Exec(`DELETE FROM devices WHERE id IN (
		SELECT d.id FROM devices d
		WHERE EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source = ?)
		  AND NOT EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source != ?))`,
		string(model.SourceDemo), string(model.SourceDemo)); err != nil {
		return fmt.Errorf("demo: reset devices: %w", err)
	}
	// A device that survived the delete above keeps its demo source row; drop
	// it so the device stops being reported as demo data.
	if _, err := db.Exec(`DELETE FROM device_sources WHERE source = ?`, string(model.SourceDemo)); err != nil {
		return fmt.Errorf("demo: reset device sources: %w", err)
	}
	// Demo interface addresses are attached to the router device, which is not
	// deleted (it is the local router); drop the demo address rows only when
	// they belong to a demo-only device, which the cascade already handled.
	_ = db

	if err := store.DeleteSetting(SettingDemoMode); err != nil && !errors.Is(err, database.ErrNotFound) {
		return fmt.Errorf("demo: reset setting: %w", err)
	}
	if err := store.DeleteSetting(SettingDemoSeed); err != nil && !errors.Is(err, database.ErrNotFound) {
		return fmt.Errorf("demo: reset setting: %w", err)
	}
	if err := store.DeleteSetting(SettingDemoLoadedAt); err != nil && !errors.Is(err, database.ErrNotFound) {
		return fmt.Errorf("demo: reset setting: %w", err)
	}
	return nil
}

// IsDemo reports whether the database currently holds demo data.
func IsDemo(store *database.Store) bool {
	if store == nil {
		return false
	}
	v, err := store.GetSetting(SettingDemoMode)
	if err != nil {
		return false
	}
	return v == "1" || strings.EqualFold(v, "true")
}

// Seed returns the demo seed recorded in the database, if any.
func Seed(store *database.Store) string {
	if store == nil {
		return ""
	}
	v, err := store.GetSetting(SettingDemoSeed)
	if err != nil {
		return ""
	}
	return v
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// hasEvidence reports whether the demo collector already wrote evidence of the
// given type for a subject. Evidence is append-only, so this is what keeps a
// second Load from duplicating rows.
func hasEvidence(store *database.Store, subjectType, subjectID, evidenceType string) bool {
	ev, err := store.ListEvidence(subjectType, subjectID)
	if err != nil {
		return false
	}
	for _, e := range ev {
		if e.Collector == "demo" && e.EvidenceType == evidenceType {
			return true
		}
	}
	return false
}

// findEdgeID returns the ID of any demo edge between a and b.
func findEdgeID(store *database.Store, a, b string) (string, error) {
	edges, err := store.ListEdges(database.EdgeFilter{})
	if err != nil {
		return "", err
	}
	for _, e := range edges {
		if (e.SourceID == a && e.TargetID == b) || (e.SourceID == b && e.TargetID == a) {
			return e.ID, nil
		}
	}
	return "", fmt.Errorf("no edge %s <-> %s", a, b)
}

// splitCIDR splits "192.0.2.1/24" into address, family and prefix length.
func splitCIDR(cidr string) (addr string, family model.AddressFamily, prefix int) {
	parts := strings.SplitN(cidr, "/", 2)
	addr = parts[0]
	if strings.Contains(addr, ":") {
		family = model.AddressFamilyIPv6
	} else {
		family = model.AddressFamilyIPv4
	}
	if len(parts) == 2 {
		for _, c := range parts[1] {
			if c < '0' || c > '9' {
				break
			}
			prefix = prefix*10 + int(c-'0')
		}
	}
	return addr, family, prefix
}

// demoEvidenceFor renders the raw DHCP lease line for a device.
func demoEvidenceFor(dd demoDevice) string {
	if dd.IPv4 == "" {
		return fmt.Sprintf("observed %s on %s (no IPv4 address)", dd.MAC, dd.Iface)
	}
	client := dd.ClientID
	if client == "" {
		client = "*"
	}
	hostname := dd.Hostname
	if hostname == "" {
		hostname = "*"
	}
	return fmt.Sprintf("%d %s %s %s %s", baseTime.Unix(), dd.MAC, dd.IPv4, hostname, client)
}

// errorTypeFor maps a probe status to the stored error type.
func errorTypeFor(status string) string {
	switch status {
	case "success":
		return ""
	case "timeout":
		return "i/o timeout"
	case "refused":
		return "connection refused"
	default:
		return status
	}
}
