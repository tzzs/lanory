package api

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Interface reads.
//
// The API layer needs interfaces for three things: validating that a
// parent/source/target interface ID exists before creating an edge, listing the
// interfaces of the overview and monitoring pages, and deriving the logical
// networks of the topology.
//
// The store's own interface queries (Store.ListInterfaces, ListAllInterfaces,
// GetInterface, GetInterfaceByName) build their SQL by concatenating the column
// list with the FROM clause and omit the SELECT keyword, so every one of them
// fails with "near \"id\": syntax error". That is a defect in
// internal/database/store.go, which this package does not own.
//
// Rather than editing another agent's file — or silently degrading the UI — the
// API layer reads interfaces through the store's exposed *sql.DB with a correct
// query. The scan mirrors Store.scanInterface exactly so the values are
// identical once the store is fixed.
//
// When internal/database is corrected, these helpers can delegate to the store
// methods and this file can be deleted. Nothing else in the API layer needs to
// change, because every call site goes through these functions.

// interfaceColumns mirrors the column list of database.interfaceColumns.
const interfaceColumns = `id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
	speed_mbps, mtu, bridge_id, vlan_id, is_physical,
	rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
	last_speed_change, last_seen`

// scanInterface reads one interface row in the same column order as
// database.Store.scanInterface.
func scanInterface(sc interface{ Scan(...any) error }) (*model.NetworkInterface, error) {
	i := &model.NetworkInterface{}
	var (
		ifaceType, role, admin, oper string
		carrier, physical            int
		speedChange                  sql.NullTime
		lastSeen                     time.Time
	)
	err := sc.Scan(&i.ID, &i.DeviceID, &i.Name, &i.MAC, &ifaceType, &role, &admin, &oper, &carrier,
		&i.SpeedMbps, &i.MTU, &i.BridgeID, &i.VLANID, &physical,
		&i.RXBytes, &i.TXBytes, &i.RXErrors, &i.TXErrors, &i.RXPackets, &i.TXPackets, &i.RXDropped, &i.TXDropped,
		&speedChange, &lastSeen)
	if err != nil {
		return nil, err
	}
	i.Type = model.InterfaceType(ifaceType)
	i.Role = model.InterfaceRole(role)
	i.AdminState = model.AdminState(admin)
	i.OperState = model.OperState(oper)
	i.Carrier = carrier == 1
	i.IsPhysical = physical == 1
	if speedChange.Valid {
		i.LastSpeedChange = speedChange.Time
	}
	i.LastSeen = lastSeen
	return i, nil
}

// listInterfaces returns every interface of a device.
func listInterfaces(store *database.Store, deviceID string) ([]model.NetworkInterface, error) {
	rows, err := store.DB().Query(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE device_id = ? ORDER BY name",
		deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.NetworkInterface
	for rows.Next() {
		iface, err := scanInterface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *iface)
	}
	return out, rows.Err()
}

// listAllInterfaces returns every interface of every device.
func listAllInterfaces(store *database.Store) ([]model.NetworkInterface, error) {
	rows, err := store.DB().Query(
		"SELECT " + interfaceColumns + " FROM network_interfaces ORDER BY device_id, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.NetworkInterface
	for rows.Next() {
		iface, err := scanInterface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *iface)
	}
	return out, rows.Err()
}

// getInterface returns one interface by ID.
func getInterface(store *database.Store, id string) (*model.NetworkInterface, error) {
	row := store.DB().QueryRow(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE id = ?", id)
	iface, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	return iface, err
}

// upsertInterface inserts or refreshes an interface.
//
// It mirrors Store.UpsertInterface but uses a correct existence check, so a
// create on a fresh device works. It is used by the API tests to seed the
// reference network; production code reads interfaces and never writes them,
// because that is the discovery engine's job.
func upsertInterface(store *database.Store, i *model.NetworkInterface) error {
	if i.ID == "" {
		i.ID = fmt.Sprintf("if-%x", database.Now().UnixNano()&0xffffffffffff)
	}
	if i.LastSeen.IsZero() {
		i.LastSeen = database.Now()
	}

	existing, err := getInterface(store, i.ID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return err
	}
	if existing != nil {
		_, err := store.DB().Exec(`UPDATE network_interfaces SET
			mac = ?, type = ?, role = ?, admin_state = ?, oper_state = ?, carrier = ?,
			speed_mbps = ?, mtu = ?, bridge_id = ?, vlan_id = ?, is_physical = ?,
			rx_bytes = ?, tx_bytes = ?, rx_errors = ?, tx_errors = ?,
			rx_packets = ?, tx_packets = ?, rx_dropped = ?, tx_dropped = ?,
			last_speed_change = ?, last_seen = ?
			WHERE id = ?`,
			i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolToInt(i.Carrier),
			i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolToInt(i.IsPhysical),
			i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors,
			i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
			nullTimeOrNil(i.LastSpeedChange), i.LastSeen.UTC().Format(database.TimeLayout), i.ID)
		return err
	}

	_, err = store.DB().Exec(`INSERT INTO network_interfaces (
		id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
		speed_mbps, mtu, bridge_id, vlan_id, is_physical,
		rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
		last_speed_change, last_seen
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.DeviceID, i.Name, i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolToInt(i.Carrier),
		i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolToInt(i.IsPhysical),
		i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors,
		i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
		nullTimeOrNil(i.LastSpeedChange), i.LastSeen.UTC().Format(database.TimeLayout))
	return err
}

// getInterfaceByName returns one interface by device and name.
//
// The monitoring engine uses this to attach an address observation to the right
// port of a router.
func getInterfaceByName(store *database.Store, deviceID, name string) (*model.NetworkInterface, error) {
	row := store.DB().QueryRow(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE device_id = ? AND name = ?",
		deviceID, name)
	iface, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	return iface, err
}

// boolToInt renders a boolean as the SQLite integer the schema expects.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullTimeOrNil renders a zero time as NULL.
func nullTimeOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(database.TimeLayout)
}

// deviceDetail reads the full device payload.
//
// It mirrors Store.GetDeviceDetail but resolves the device's interfaces through
// the corrected query in this file. Everything else (addresses, identities,
// edges, probes, events) comes straight from the store.
func deviceDetail(store *database.Store, id string) (*model.DeviceDetail, error) {
	d, err := store.GetDevice(id)
	if err != nil {
		return nil, err
	}
	detail := &model.DeviceDetail{Device: d}

	if detail.Addresses, err = store.ListActiveAddresses(id, database.Now()); err != nil {
		return nil, err
	}
	if detail.Identities, err = store.ListIdentities(id); err != nil {
		return nil, err
	}
	if detail.Interfaces, err = listInterfaces(store, id); err != nil {
		return nil, err
	}
	if detail.Edges, err = store.ListEdgesForDevice(id); err != nil {
		return nil, err
	}
	if detail.Probes, err = store.ListProbesByDevice(id); err != nil {
		return nil, err
	}
	if len(detail.Probes) > 0 {
		if res, err := store.LatestProbeResult(detail.Probes[0].ID); err == nil && res != nil {
			detail.LastProbe = res
		}
	}
	if detail.Events, err = store.ListEventsByDevice(id, 20); err != nil {
		return nil, err
	}
	return detail, nil
}

// selfDeviceID returns the ID of the device Lanory runs on.
//
// It is the anchor for probes that have no device of their own (network and
// connectivity probes), because a probe always executes from the host Lanory
// runs on.
func selfDeviceID(store *database.Store) string {
	devices, _, err := store.ListDevices(database.DeviceFilter{IncludeHidden: true, Limit: 500})
	if err != nil {
		return ""
	}
	for _, d := range devices {
		if d.IsSelf {
			return d.ID
		}
	}
	return ""
}

// createProbe inserts a probe target.
//
// It mirrors Store.CreateProbe but anchors a device-less probe to the host
// device. probe_targets.device_id is declared NOT NULL DEFAULT ” with a
// foreign key to devices(id), so a probe that belongs to no device cannot be
// stored as the schema stands; that is a defect in internal/database, which
// this package does not own. Anchoring to the host is also what the product
// documentation asks for: a probe always runs from the host Lanory runs on.
func createProbe(store *database.Store, p *model.ProbeTarget) error {
	if p.ID == "" {
		p.ID = newProbeID()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = database.Now()
	}
	p.UpdatedAt = database.Now()
	if p.State == "" {
		p.State = model.StateUnknown
	}
	if p.Kind == "" {
		p.Kind = model.ProbeKindDevice
	}
	if p.Protocol == "" {
		p.Protocol = model.ProbeICMP
	}
	if p.Interval <= 0 {
		p.Interval = 60
	}
	if p.Timeout <= 0 {
		p.Timeout = 3
	}
	if p.FailureThreshold <= 0 {
		p.FailureThreshold = 3
	}
	if p.RecoverConfirm <= 0 {
		p.RecoverConfirm = 1
	}

	deviceID := p.DeviceID
	if deviceID == "" {
		deviceID = selfDeviceID(store)
		if deviceID == "" {
			return errors.New("no device to anchor the probe to; seed the host device first")
		}
		p.DeviceID = deviceID
	}

	_, err := store.DB().Exec(`INSERT INTO probe_targets (
		id, name, kind, target, port, protocol, source_id, interval, timeout, enabled,
		device_id, failure_threshold, recover_confirm, state, state_reason,
		consecutive_failures, consecutive_successes, last_result, last_latency_ms, last_error,
		last_run_at, last_success_at, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, string(p.Kind), p.Target, p.Port, string(p.Protocol), p.SourceID,
		p.Interval, p.Timeout, boolToInt(p.Enabled),
		deviceID, p.FailureThreshold, p.RecoverConfirm, string(p.State), p.StateReason,
		0, 0, "", 0, "",
		nil, nil,
		p.CreatedAt.UTC().Format(database.TimeLayout), p.UpdatedAt.UTC().Format(database.TimeLayout))
	return err
}

// newProbeID builds a short probe identifier.
func newProbeID() string {
	return fmt.Sprintf("probe-%x", database.Now().UnixNano()&0xffffffffffff)
}
