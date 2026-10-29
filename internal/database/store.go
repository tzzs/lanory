package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// Store provides all persistence operations. It is the only package allowed to
// execute SQL against the Lanory database.
type Store struct {
	db *DB
}

// NewStore returns a Store backed by db.
func NewStore(db *DB) *Store { return &Store{db: db} }

// DB exposes the underlying database handle for internal helpers.
func (s *Store) DB() *DB { return s.db }

func (s *Store) now() string { return Now().Format(TimeLayout) }

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func scanTime(v sql.NullTime) time.Time { return v.Time }

func parseDBTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		for _, layout := range []string{time.RFC3339Nano, TimeLayout, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed.UTC()
			}
		}
	case nil:
		return time.Time{}
	}
	return time.Time{}
}

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

// deviceColumns lists the device columns, always qualified with the alias "d".
//
// The qualification is not cosmetic: several queries join device_identities,
// device_sources or device_addresses, and an unqualified "id" is ambiguous there
// (both devices and device_identities have one). Every query that uses this
// constant must therefore alias the devices table as "d".
const deviceColumns = `d.id, d.name, d.hostname, d.type, d.manufacturer, d.model, d.description,
	d.is_manual, d.is_locked, d.is_hidden, d.type_source, d.state, d.state_reason, d.last_state_change,
	d.primary_ipv4, d.primary_ipv6, d.primary_mac, d.is_gateway, d.is_self,
	d.first_seen, d.last_seen, d.created_at, d.updated_at`

func (s *Store) scanDevice(sc interface{ Scan(...any) error }) (*model.Device, error) {
	d := &model.Device{}
	var typeSource, stateReason string
	err := sc.Scan(
		&d.ID, &d.Name, &d.Hostname, &d.Type, &d.Manufacturer, &d.Model, &d.Description,
		&d.IsManual, &d.IsLocked, &d.IsHidden, &typeSource, &d.State, &stateReason, &d.LastStateChange,
		&d.PrimaryIPv4, &d.PrimaryIPv6, &d.PrimaryMAC, &d.IsGateway, &d.IsSelf,
		&d.FirstSeen, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	d.TypeSource = model.DiscoverySource(typeSource)
	d.StateReason = stateReason
	return d, nil
}

// DeviceFilter constrains device listings.
type DeviceFilter struct {
	Query         string
	Types         []model.DeviceType
	States        []model.DeviceState
	Source        string
	Interface     string
	NewerThan     time.Time
	IncludeHidden bool
	Sort          string // name | ip | last_seen | type
	Order         string // asc | desc
	// OnlineFirst keeps online devices in a leading group regardless of Sort
	// and Order. It is opt-in because the topology builder and the monitoring
	// scheduler also call ListDevices and rely on the unfiltered order.
	OnlineFirst bool
	Limit       int
	Offset      int
}

// ListDevices returns devices matching the filter.
func (s *Store) ListDevices(f DeviceFilter) ([]model.Device, int, error) {
	var (
		where []string
		args  []any
	)
	if !f.IncludeHidden {
		where = append(where, "d.is_hidden = 0")
	}
	if f.Query != "" {
		where = append(where, "(d.name LIKE ? OR d.hostname LIKE ? OR d.primary_ipv4 LIKE ? OR d.primary_mac LIKE ? OR d.manufacturer LIKE ? OR d.model LIKE ?)")
		like := "%" + escapeLike(f.Query) + "%"
		args = append(args, like, like, like, like, like, like)
	}
	if len(f.Types) > 0 {
		ph, in := placeholders(len(f.Types))
		where = append(where, "d.type IN ("+ph+")")
		for _, t := range f.Types {
			args = append(args, string(t))
		}
		_ = in
	}
	if len(f.States) > 0 {
		ph, _ := placeholders(len(f.States))
		where = append(where, "d.state IN ("+ph+")")
		for _, st := range f.States {
			args = append(args, string(st))
		}
	}
	if !f.NewerThan.IsZero() {
		where = append(where, "d.first_seen >= ?")
		args = append(args, f.NewerThan.UTC().Format(TimeLayout))
	}
	if f.Interface != "" {
		where = append(where, `EXISTS (SELECT 1 FROM discovery_evidence e
			JOIN network_interfaces i ON i.id = e.subject_id
			WHERE e.subject_type = 'interface_port' AND e.collector = 'bridge_fdb'
			  AND i.name = ? AND e.subject_id IN (
				SELECT ia.interface_id FROM device_addresses ia WHERE ia.device_id = d.id
			  UNION SELECT pe.source_interface_id FROM topology_edges pe
				WHERE pe.target_id = d.id OR pe.source_id = d.id))`)
		args = append(args, f.Interface)
	}
	if f.Source != "" {
		where = append(where, "EXISTS (SELECT 1 FROM device_sources ds WHERE ds.device_id = d.id AND ds.source = ?)")
		args = append(args, f.Source)
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	order := "d.last_seen DESC"
	switch f.Sort {
	case "name":
		if f.Order == "desc" {
			order = "d.name COLLATE NOCASE DESC"
		} else {
			order = "d.name COLLATE NOCASE ASC"
		}
	case "ip":
		order = "d.primary_ipv4 ASC"
	case "type":
		order = "d.type ASC, d.name COLLATE NOCASE ASC"
	case "last_seen":
		if f.Order == "asc" {
			order = "d.last_seen ASC"
		} else {
			order = "d.last_seen DESC"
		}
	}
	// The state group is a prefix, so Order reverses the order within each
	// group but never lifts offline devices above online ones.
	if f.OnlineFirst {
		order = "CASE WHEN d.state = 'online' THEN 0 ELSE 1 END ASC, " + order
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM devices d"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	q := "SELECT " + deviceColumns + " FROM devices d" + clause + " ORDER BY " + order + " LIMIT ? OFFSET ?"
	rows, err := s.db.Query(q, append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []model.Device
	for rows.Next() {
		d, err := s.scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range out {
		srcs, err := s.deviceSources(out[i].ID)
		if err != nil {
			return nil, 0, err
		}
		out[i].DiscoverySources = srcs
	}
	return out, total, nil
}

func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
}

func placeholders(n int) (string, int) {
	if n <= 0 {
		return "", 0
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ","), n
}

// GetDevice returns a single device by ID.
func (s *Store) GetDevice(id string) (*model.Device, error) {
	row := s.db.QueryRow("SELECT "+deviceColumns+" FROM devices d WHERE d.id = ?", id)
	d, err := s.scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	srcs, err := s.deviceSources(d.ID)
	if err != nil {
		return nil, err
	}
	d.DiscoverySources = srcs
	return d, nil
}

// GetDeviceDetail returns the full device payload.
func (s *Store) GetDeviceDetail(id string) (*model.DeviceDetail, error) {
	d, err := s.GetDevice(id)
	if err != nil {
		return nil, err
	}
	detail := &model.DeviceDetail{Device: d}
	if detail.Addresses, err = s.ListActiveAddresses(id, Now()); err != nil {
		return nil, err
	}
	if detail.Identities, err = s.ListIdentities(id); err != nil {
		return nil, err
	}
	if detail.Interfaces, err = s.ListInterfaces(id); err != nil {
		return nil, err
	}
	if detail.Edges, err = s.ListEdgesForDevice(id); err != nil {
		return nil, err
	}
	if detail.Probes, err = s.ListProbesByDevice(id); err != nil {
		return nil, err
	}
	if len(detail.Probes) > 0 {
		if res, err := s.LatestProbeResult(detail.Probes[0].ID); err == nil && res != nil {
			detail.LastProbe = res
		}
	}
	if detail.Events, err = s.ListEventsByDevice(id, 20); err != nil {
		return nil, err
	}
	return detail, nil
}

func (s *Store) deviceSources(id string) ([]model.DiscoverySource, error) {
	rows, err := s.db.Query("SELECT source FROM device_sources WHERE device_id = ? ORDER BY source", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DiscoverySource
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		out = append(out, model.DiscoverySource(src))
	}
	return out, rows.Err()
}

// UpsertDevice inserts or updates a device. When the device exists, only
// non-destructible fields are refreshed; user owned fields (name, type) are
// preserved unless explicitly overridden by the caller.
func (s *Store) UpsertDevice(d *model.Device) error {
	now := s.now()
	if d.ID == "" {
		return errors.New("store: device id required")
	}
	existing, err := s.GetDevice(d.ID)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	if err != nil {
		return err
	}
	if existing == nil {
		if d.FirstSeen.IsZero() {
			d.FirstSeen = Now()
		}
		if d.CreatedAt.IsZero() {
			d.CreatedAt = Now()
		}
		d.UpdatedAt = Now()
		if d.Type == "" {
			d.Type = model.DeviceTypeUnknown
		}
		if d.State == "" {
			d.State = model.StateUnknown
		}
		if d.LastStateChange.IsZero() {
			d.LastStateChange = Now()
		}
		_, err = s.db.Exec(`INSERT INTO devices (
			id, name, hostname, type, manufacturer, model, description,
			is_manual, is_locked, is_hidden, type_source, state, state_reason, last_state_change,
			primary_ipv4, primary_ipv6, primary_mac, is_gateway, is_self,
			first_seen, last_seen, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, d.Name, d.Hostname, string(d.Type), d.Manufacturer, d.Model, d.Description,
			boolInt(d.IsManual), boolInt(d.IsLocked), boolInt(d.IsHidden), string(d.TypeSource),
			string(d.State), d.StateReason, d.LastStateChange.UTC().Format(TimeLayout),
			d.PrimaryIPv4, d.PrimaryIPv6, d.PrimaryMAC, boolInt(d.IsGateway), boolInt(d.IsSelf),
			d.FirstSeen.UTC().Format(TimeLayout), d.LastSeen.UTC().Format(TimeLayout),
			d.CreatedAt.UTC().Format(TimeLayout), d.UpdatedAt.UTC().Format(TimeLayout),
		)
		return err
	}

	// Merge: keep user supplied fields when the device is manual/locked.
	name := existing.Name
	if !existing.IsLocked || d.Name != existing.Name && d.Name != "" {
		if d.Name != "" {
			name = d.Name
		}
	}
	devType := existing.Type
	if !existing.IsLocked && d.Type != "" && d.Type != model.DeviceTypeUnknown {
		devType = d.Type
	}
	if d.Type != "" && d.Type != model.DeviceTypeUnknown {
		devType = d.Type
	}
	hostname := existing.Hostname
	if d.Hostname != "" {
		hostname = d.Hostname
	}
	manufacturer := existing.Manufacturer
	if d.Manufacturer != "" {
		manufacturer = d.Manufacturer
	}
	modelName := existing.Model
	if d.Model != "" {
		modelName = d.Model
	}
	desc := existing.Description
	if d.Description != "" {
		desc = d.Description
	}
	typeSource := existing.TypeSource
	if d.TypeSource != "" {
		typeSource = d.TypeSource
	}

	state := existing.State
	stateReason := existing.StateReason
	lastStateChange := existing.LastStateChange
	if d.State != "" && d.State != existing.State {
		state = d.State
		stateReason = d.StateReason
		lastStateChange = Now()
	} else if d.StateReason != "" {
		stateReason = d.StateReason
	}

	firstSeen := existing.FirstSeen
	if d.FirstSeen.Before(existing.FirstSeen) {
		firstSeen = d.FirstSeen
	}
	lastSeen := existing.LastSeen
	if d.LastSeen.After(existing.LastSeen) {
		lastSeen = d.LastSeen
	}

	_, err = s.db.Exec(`UPDATE devices SET
		name = ?, hostname = ?, type = ?, manufacturer = ?, model = ?, description = ?,
		type_source = ?, state = ?, state_reason = ?, last_state_change = ?,
		primary_ipv4 = ?, primary_ipv6 = ?, primary_mac = ?,
		is_gateway = ?, is_self = ?, is_locked = ?, is_hidden = ?,
		first_seen = ?, last_seen = ?, updated_at = ?
		WHERE id = ?`,
		name, hostname, string(devType), manufacturer, modelName, desc,
		string(typeSource), string(state), stateReason, lastStateChange.UTC().Format(TimeLayout),
		d.PrimaryIPv4, d.PrimaryIPv6, d.PrimaryMAC,
		boolInt(d.IsGateway || existing.IsGateway), boolInt(d.IsSelf || existing.IsSelf),
		boolInt(d.IsLocked || existing.IsLocked), boolInt(d.IsHidden || existing.IsHidden),
		firstSeen.UTC().Format(TimeLayout), lastSeen.UTC().Format(TimeLayout), now,
		d.ID,
	)

	// Refresh the returned device from the database so callers see merged state.
	updated, gerr := s.GetDevice(d.ID)
	if gerr == nil {
		*d = *updated
	}
	return err
}

// UpdateDevice applies a partial update to a device.
func (s *Store) UpdateDevice(id string, patch map[string]any) (*model.Device, error) {
	if len(patch) == 0 {
		return s.GetDevice(id)
	}
	allowed := map[string]string{
		"name": "name", "hostname": "hostname", "type": "type",
		"manufacturer": "manufacturer", "model": "model", "description": "description",
		"is_manual": "is_manual", "is_locked": "is_locked", "is_hidden": "is_hidden",
		"type_source": "type_source", "state": "state", "state_reason": "state_reason",
	}
	var (
		sets []string
		args []any
	)
	for k, v := range patch {
		col, ok := allowed[k]
		if !ok {
			continue
		}
		if b, ok := v.(bool); ok {
			sets = append(sets, col+" = ?")
			args = append(args, boolInt(b))
			continue
		}
		sets = append(sets, col+" = ?")
		args = append(args, fmt.Sprintf("%v", v))
	}
	if len(sets) == 0 {
		return s.GetDevice(id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, s.now())
	args = append(args, id)

	q := "UPDATE devices SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if _, ok := patch["state"]; ok {
		if _, err := s.db.Exec("UPDATE devices SET last_state_change = ? WHERE id = ?", s.now(), id); err != nil {
			return nil, err
		}
	}
	return s.GetDevice(id)
}

// SetDeviceState updates a device state and records the reason.
func (s *Store) SetDeviceState(id string, state model.DeviceState, reason string) error {
	_, err := s.db.Exec(`UPDATE devices SET state = ?, state_reason = ?, last_state_change = ?, updated_at = ? WHERE id = ?`,
		string(state), reason, s.now(), s.now(), id)
	return err
}

// HideDevice marks a device hidden so discovery does not resurrect it.
func (s *Store) HideDevice(id string) error {
	_, err := s.db.Exec(`UPDATE devices SET is_hidden = 1, updated_at = ? WHERE id = ?`, s.now(), id)
	return err
}

// DeleteDevice removes a device and all dependent rows. Manual devices must go
// through this path explicitly; discovered devices should be hidden instead.
func (s *Store) DeleteDevice(id string) error {
	res, err := s.db.Exec(`DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MergeDevices merges src into dst: identities, addresses and edges move to dst
// and src is removed.
func (s *Store) MergeDevices(dstID, srcID string) error {
	if dstID == srcID {
		return errors.New("store: cannot merge a device into itself")
	}
	dst, err := s.GetDevice(dstID)
	if err != nil {
		return err
	}
	src, err := s.GetDevice(srcID)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Move identities, skipping duplicates.
	if _, err := tx.Exec(`UPDATE OR IGNORE device_identities SET device_id = ? WHERE device_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM device_identities WHERE device_id = ?`, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE OR IGNORE device_addresses SET device_id = ? WHERE device_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM device_addresses WHERE device_id = ?`, srcID); err != nil {
		return err
	}
	// Move interfaces (names may collide; drop the losers).
	if _, err := tx.Exec(`UPDATE OR IGNORE network_interfaces SET device_id = ? WHERE device_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM network_interfaces WHERE device_id = ?`, srcID); err != nil {
		return err
	}
	// Redirect edges, dropping self edges and duplicates.
	if _, err := tx.Exec(`UPDATE topology_edges SET source_id = ? WHERE source_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE topology_edges SET target_id = ? WHERE target_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM topology_edges WHERE source_id = target_id`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE probe_targets SET device_id = ? WHERE device_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE network_events SET device_id = ? WHERE device_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE OR IGNORE device_sources SET device_id = ? WHERE device_id = ?`, dstID, srcID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM device_sources WHERE device_id = ?`, srcID); err != nil {
		return err
	}

	// Preserve the richer metadata on dst.
	newName := dst.Name
	if src.Name != "" && (dst.Name == "" || (!dst.IsManual && src.IsManual)) {
		newName = src.Name
	}
	newType := dst.Type
	if src.Type != model.DeviceTypeUnknown && (dst.Type == model.DeviceTypeUnknown || src.TypeSource == model.SourceManual) {
		newType = src.Type
	}
	newHost := dst.Hostname
	if src.Hostname != "" && dst.Hostname == "" {
		newHost = src.Hostname
	}
	newManu := dst.Manufacturer
	if src.Manufacturer != "" && dst.Manufacturer == "" {
		newManu = src.Manufacturer
	}
	newModel := dst.Model
	if src.Model != "" && dst.Model == "" {
		newModel = src.Model
	}
	firstSeen := dst.FirstSeen
	if src.FirstSeen.Before(firstSeen) {
		firstSeen = src.FirstSeen
	}
	lastSeen := dst.LastSeen
	if src.LastSeen.After(lastSeen) {
		lastSeen = src.LastSeen
	}

	if _, err := tx.Exec(`UPDATE devices SET name = ?, hostname = ?, type = ?, manufacturer = ?, model = ?,
		is_manual = ?, is_locked = ?, first_seen = ?, last_seen = ?, updated_at = ? WHERE id = ?`,
		newName, newHost, string(newType), newManu, newModel,
		boolInt(dst.IsManual || src.IsManual), boolInt(dst.IsLocked || src.IsLocked),
		firstSeen.UTC().Format(TimeLayout), lastSeen.UTC().Format(TimeLayout), s.now(), dstID); err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM devices WHERE id = ?`, srcID); err != nil {
		return err
	}

	return tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Addresses
// ---------------------------------------------------------------------------

// ListAddresses returns every address ever observed for a device.
//
// This low-level method is intentionally history-preserving: discovery,
// topology and monitoring use it to find an address that may have gone quiet.
// User-facing detail endpoints should use ListActiveAddresses instead.
func (s *Store) ListAddresses(deviceID string) ([]model.DeviceAddress, error) {
	return s.listAddresses(deviceID, ``)
}

// ListActiveAddresses returns addresses observed recently, plus primary and
// manually managed addresses. Discovery data is historical by design, but a
// device detail view should not present a year-old ARP observation as current.
// A 24-hour window is long enough to span the router's normal collection cycle.
func (s *Store) ListActiveAddresses(deviceID string, now time.Time) ([]model.DeviceAddress, error) {
	cutoff := now.Add(-24 * time.Hour).UTC().Format(TimeLayout)
	return s.listAddresses(deviceID, cutoff)
}

func (s *Store) listAddresses(deviceID, cutoff string) ([]model.DeviceAddress, error) {
	query := `SELECT id, device_id, address, family, interface_id, source, prefix_length, is_primary, first_seen, last_seen
		FROM device_addresses WHERE device_id = ?`
	args := []any{deviceID}
	if cutoff != "" {
		query += ` AND (is_primary = 1 OR source = 'manual' OR last_seen >= ?)`
		args = append(args, cutoff)
	}
	query += ` ORDER BY family, address`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeviceAddress
	for rows.Next() {
		var a model.DeviceAddress
		var src string
		var prim int
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.Address, &a.Family, &a.InterfaceID, &src, &a.PrefixLength, &prim, &a.FirstSeen, &a.LastSeen); err != nil {
			return nil, err
		}
		a.Source = model.DiscoverySource(src)
		a.IsPrimary = prim == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListInterfaces returns all interfaces of a device including their addresses.
func (s *Store) ListInterfaces(deviceID string) ([]model.NetworkInterface, error) {
	rows, err := s.db.Query(interfaceColumns+` FROM network_interfaces WHERE device_id = ? ORDER BY name`, deviceID)
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
		addrs, err := s.db.Query(`SELECT id, device_id, address, family, interface_id, source, prefix_length, is_primary, first_seen, last_seen
			FROM device_addresses WHERE interface_id = ? ORDER BY family, address`, iface.ID)
		if err != nil {
			return nil, err
		}
		for addrs.Next() {
			var a model.DeviceAddress
			var src string
			var prim int
			if err := addrs.Scan(&a.ID, &a.DeviceID, &a.Address, &a.Family, &a.InterfaceID, &src, &a.PrefixLength, &prim, &a.FirstSeen, &a.LastSeen); err != nil {
				addrs.Close()
				return nil, err
			}
			a.Source = model.DiscoverySource(src)
			a.IsPrimary = prim == 1
			iface.Addresses = append(iface.Addresses, a)
		}
		addrs.Close()
		if err := addrs.Err(); err != nil {
			return nil, err
		}
		out = append(out, *iface)
	}
	return out, rows.Err()
}

// ListAllInterfaces returns every interface of every device.
func (s *Store) ListAllInterfaces() ([]model.NetworkInterface, error) {
	rows, err := s.db.Query(interfaceColumns + ` FROM network_interfaces ORDER BY device_id, name`)
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

// UpsertAddress inserts or refreshes an address observation.
func (s *Store) UpsertAddress(a *model.DeviceAddress) error {
	if a.ID == "" {
		a.ID = newID("addr")
	}
	if a.FirstSeen.IsZero() {
		a.FirstSeen = Now()
	}
	if a.LastSeen.IsZero() {
		a.LastSeen = Now()
	}
	_, err := s.db.Exec(`INSERT INTO device_addresses
		(id, device_id, address, family, interface_id, source, prefix_length, is_primary, first_seen, last_seen)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id, family, address) DO UPDATE SET
			interface_id = CASE WHEN excluded.interface_id != '' THEN excluded.interface_id ELSE device_addresses.interface_id END,
			source = excluded.source,
			prefix_length = CASE WHEN excluded.prefix_length > 0 THEN excluded.prefix_length ELSE device_addresses.prefix_length END,
			is_primary = excluded.is_primary,
			last_seen = excluded.last_seen`,
		a.ID, a.DeviceID, a.Address, string(a.Family), a.InterfaceID, string(a.Source),
		a.PrefixLength, boolInt(a.IsPrimary), a.FirstSeen.UTC().Format(TimeLayout), a.LastSeen.UTC().Format(TimeLayout))
	return err
}

// FindDeviceByAddress returns the device that owns an address.
func (s *Store) FindDeviceByAddress(family model.AddressFamily, address string) (*model.Device, error) {
	row := s.db.QueryRow(`SELECT `+deviceColumns+` FROM devices d
		WHERE d.id IN (SELECT device_id FROM device_addresses WHERE family = ? AND address = ?) LIMIT 1`,
		string(family), address)
	d, err := s.scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// ---------------------------------------------------------------------------
// Identities
// ---------------------------------------------------------------------------

// ListIdentities returns all identities of a device.
func (s *Store) ListIdentities(deviceID string) ([]model.DeviceIdentity, error) {
	rows, err := s.db.Query(`SELECT id, device_id, identity_type, identity_value, source, confidence, first_seen, last_seen
		FROM device_identities WHERE device_id = ? ORDER BY identity_type, identity_value`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DeviceIdentity
	for rows.Next() {
		var i model.DeviceIdentity
		var src string
		if err := rows.Scan(&i.ID, &i.DeviceID, &i.Type, &i.Value, &src, &i.Confidence, &i.FirstSeen, &i.LastSeen); err != nil {
			return nil, err
		}
		i.Source = model.DiscoverySource(src)
		out = append(out, i)
	}
	return out, rows.Err()
}

// UpsertIdentity inserts or refreshes an identity, keeping the highest
// confidence observed.
func (s *Store) UpsertIdentity(i *model.DeviceIdentity) error {
	now := s.now()
	if i.ID == "" {
		i.ID = newID("idn")
	}
	if i.FirstSeen.IsZero() {
		i.FirstSeen = Now()
	}
	if i.LastSeen.IsZero() {
		i.LastSeen = Now()
	}
	_, err := s.db.Exec(`INSERT INTO device_identities
		(id, device_id, identity_type, identity_value, source, confidence, first_seen, last_seen)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id, identity_type, identity_value) DO UPDATE SET
			confidence = MAX(device_identities.confidence, excluded.confidence),
			last_seen = excluded.last_seen`,
		i.ID, i.DeviceID, string(i.Type), i.Value, string(i.Source), i.Confidence,
		i.FirstSeen.UTC().Format(TimeLayout), i.LastSeen.UTC().Format(TimeLayout))
	_ = now
	return err
}

// FindDevicesByIdentity returns all devices carrying a given identity.
func (s *Store) FindDevicesByIdentity(t model.IdentityType, value string) ([]model.Device, error) {
	rows, err := s.db.Query(`SELECT DISTINCT `+deviceColumns+` FROM devices d
		JOIN device_identities i ON i.device_id = d.id
		WHERE i.identity_type = ? AND i.identity_value = ?`, string(t), value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Device
	for rows.Next() {
		d, err := s.scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// FindDeviceByMAC returns the device owning a MAC address.
func (s *Store) FindDeviceByMAC(mac string) (*model.Device, error) {
	devs, err := s.FindDevicesByIdentity(model.IdentityMAC, normalizeMAC(mac))
	if err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		return nil, ErrNotFound
	}
	return &devs[0], nil
}

// AddDeviceSource records that a source observed a device.
func (s *Store) AddDeviceSource(deviceID string, src model.DiscoverySource) error {
	_, err := s.db.Exec(`INSERT INTO device_sources (device_id, source, first_seen, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(device_id, source) DO UPDATE SET last_seen = excluded.last_seen`,
		deviceID, string(src), s.now(), s.now())
	return err
}

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

const interfaceColumns = `SELECT id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
	speed_mbps, mtu, bridge_id, vlan_id, is_physical,
	rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
	last_speed_change, last_seen`

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
	i.LastSpeedChange = scanTime(speedChange)
	i.LastSeen = lastSeen
	return i, nil
}

// UpsertInterface inserts or updates an interface.
func (s *Store) UpsertInterface(i *model.NetworkInterface) error {
	if i.ID == "" {
		i.ID = newID("iface")
	}
	if i.LastSeen.IsZero() {
		i.LastSeen = Now()
	}
	existing, err := s.GetInterface(i.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if existing != nil {
		if _, err := s.db.Exec(`UPDATE network_interfaces SET
			mac = ?, type = ?, role = ?, admin_state = ?, oper_state = ?, carrier = ?,
			speed_mbps = ?, mtu = ?, bridge_id = ?, vlan_id = ?, is_physical = ?,
			rx_bytes = ?, tx_bytes = ?, rx_errors = ?, tx_errors = ?,
			rx_packets = ?, tx_packets = ?, rx_dropped = ?, tx_dropped = ?,
			last_speed_change = ?, last_seen = ?
			WHERE id = ?`,
			i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolInt(i.Carrier),
			i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolInt(i.IsPhysical),
			i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors, i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
			nullTime(i.LastSpeedChange), i.LastSeen.UTC().Format(TimeLayout), i.ID); err != nil {
			return err
		}
		return nil
	}
	_, err = s.db.Exec(`INSERT INTO network_interfaces (
		id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
		speed_mbps, mtu, bridge_id, vlan_id, is_physical,
		rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
		last_speed_change, last_seen
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.DeviceID, i.Name, i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolInt(i.Carrier),
		i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolInt(i.IsPhysical),
		i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors, i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
		nullTime(i.LastSpeedChange), i.LastSeen.UTC().Format(TimeLayout))
	return err
}

// GetInterface returns an interface by ID.
func (s *Store) GetInterface(id string) (*model.NetworkInterface, error) {
	row := s.db.QueryRow(interfaceColumns+` FROM network_interfaces WHERE id = ?`, id)
	iface, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return iface, err
}

// GetInterfaceByName returns an interface by device and name.
func (s *Store) GetInterfaceByName(deviceID, name string) (*model.NetworkInterface, error) {
	row := s.db.QueryRow(interfaceColumns+` FROM network_interfaces WHERE device_id = ? AND name = ?`, deviceID, name)
	iface, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return iface, err
}

// ---------------------------------------------------------------------------
// Evidence
// ---------------------------------------------------------------------------

// SaveEvidence persists a discovery evidence record. Raw data is truncated to
// keep the database small on flash storage.
func (s *Store) SaveEvidence(e *model.DiscoveryEvidence) error {
	if e.ID == "" {
		e.ID = newID("ev")
	}
	if e.ObservedAt.IsZero() {
		e.ObservedAt = Now()
	}
	if len(e.RawData) > MaxRawDataBytes {
		e.RawData = e.RawData[:MaxRawDataBytes] + "…[truncated]"
	}
	_, err := s.db.Exec(`INSERT INTO discovery_evidence (id, collector, subject_type, subject_id, evidence_type, raw_data, observed_at)
		VALUES (?,?,?,?,?,?,?)`,
		e.ID, e.Collector, e.SubjectType, e.SubjectID, e.EvidenceType, e.RawData, e.ObservedAt.UTC().Format(TimeLayout))
	return err
}

// MaxRawDataBytes caps the size of a stored raw evidence payload.
const MaxRawDataBytes = 4096

// ListEvidence returns evidence for a subject.
func (s *Store) ListEvidence(subjectType, subjectID string) ([]model.DiscoveryEvidence, error) {
	rows, err := s.db.Query(`SELECT id, collector, subject_type, subject_id, evidence_type, raw_data, observed_at
		FROM discovery_evidence WHERE subject_type = ? AND subject_id = ? ORDER BY observed_at DESC LIMIT 100`, subjectType, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DiscoveryEvidence
	for rows.Next() {
		var e model.DiscoveryEvidence
		if err := rows.Scan(&e.ID, &e.Collector, &e.SubjectType, &e.SubjectID, &e.EvidenceType, &e.RawData, &e.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEvidenceForDevice returns all evidence attached to a device and to the
// edges it participates in.
func (s *Store) ListEvidenceForDevice(deviceID string) ([]model.DiscoveryEvidence, error) {
	rows, err := s.db.Query(`SELECT id, collector, subject_type, subject_id, evidence_type, raw_data, observed_at
		FROM discovery_evidence
		WHERE (subject_type = 'device' AND subject_id = ?)
		   OR (subject_type = 'edge' AND subject_id IN (SELECT id FROM topology_edges WHERE source_id = ? OR target_id = ?))
		   OR (subject_type = 'interface_port' AND subject_id IN (SELECT id FROM network_interfaces WHERE device_id = ?))
		ORDER BY observed_at DESC LIMIT 200`, deviceID, deviceID, deviceID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DiscoveryEvidence
	for rows.Next() {
		var e model.DiscoveryEvidence
		if err := rows.Scan(&e.ID, &e.Collector, &e.SubjectType, &e.SubjectID, &e.EvidenceType, &e.RawData, &e.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteOldEvidence removes evidence older than the retention window.
func (s *Store) DeleteOldEvidence(olderThan time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM discovery_evidence WHERE observed_at < ?`, olderThan.UTC().Format(TimeLayout))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------------

// Ping records a liveness check; used by tests and health endpoints.
func (s *Store) Ping() error { return s.db.Ping() }

// JSONString marshals v, returning "null" on error. Used by API handlers.
func JSONString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
