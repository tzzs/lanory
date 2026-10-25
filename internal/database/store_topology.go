package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/model"
)

// ---------------------------------------------------------------------------
// ID generation
// ---------------------------------------------------------------------------

var (
	idMu      sync.Mutex
	idCounter uint64
)

// newID returns a short, sortable, unique identifier such as "dev-7f3a9c1e2b".
func newID(prefix string) string {
	idMu.Lock()
	idCounter++
	c := idCounter
	idMu.Unlock()
	return fmt.Sprintf("%s-%x%04x", prefix, Now().UnixNano()&0xffffffffffff, c&0xffff)
}

// ---------------------------------------------------------------------------
// Topology edges
// ---------------------------------------------------------------------------

const edgeColumns = `id, source_id, target_id, source_interface_id, target_interface_id,
	edge_type, discovery_source, confidence, user_confirmed, locked, status, label, unverified,
	last_observed, created_at, updated_at`

func scanEdge(sc interface{ Scan(...any) error }) (*model.TopologyEdge, error) {
	e := &model.TopologyEdge{}
	var (
		src           string
		userConfirmed int
		locked        int
		unverified    int
	)
	err := sc.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.SourceInterfaceID, &e.TargetInterfaceID,
		&e.EdgeType, &src, &e.Confidence, &userConfirmed, &locked, &e.Status, &e.Label, &unverified,
		&e.LastObserved, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	e.DiscoverySource = model.DiscoverySource(src)
	e.UserConfirmed = userConfirmed == 1
	e.Locked = locked == 1
	e.Unverified = unverified == 1
	return e, nil
}

// EdgeFilter constrains edge listings.
type EdgeFilter struct {
	DeviceID           string
	Type               model.EdgeType
	OnlyUnverified     bool
	IncludeUserDefined bool
}

// ListEdges returns topology edges matching the filter.
func (s *Store) ListEdges(f EdgeFilter) ([]model.TopologyEdge, error) {
	var (
		where []string
		args  []any
	)
	if f.DeviceID != "" {
		where = append(where, "(source_id = ? OR target_id = ?)")
		args = append(args, f.DeviceID, f.DeviceID)
	}
	if f.Type != "" {
		where = append(where, "edge_type = ?")
		args = append(args, string(f.Type))
	}
	if f.OnlyUnverified {
		where = append(where, "unverified = 1")
	}
	if !f.IncludeUserDefined {
		// keep everything; user defined edges are part of the topology
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.Query("SELECT "+edgeColumns+" FROM topology_edges"+clause+" ORDER BY created_at", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TopologyEdge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ListEdgesForDevice returns all edges touching a device.
func (s *Store) ListEdgesForDevice(deviceID string) ([]model.TopologyEdge, error) {
	return s.ListEdges(EdgeFilter{DeviceID: deviceID, IncludeUserDefined: true})
}

// GetEdge returns a single edge.
func (s *Store) GetEdge(id string) (*model.TopologyEdge, error) {
	row := s.db.QueryRow("SELECT "+edgeColumns+" FROM topology_edges WHERE id = ?", id)
	e, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// UpsertEdge inserts or updates an edge. An edge is identified by the tuple
// (source, target, source interface, target interface, edge type) so repeated
// discoveries do not create duplicates.
func (s *Store) UpsertEdge(e *model.TopologyEdge) error {
	if e.ID == "" {
		e.ID = newID("edge")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = Now()
	}
	e.UpdatedAt = Now()
	if e.LastObserved.IsZero() {
		e.LastObserved = Now()
	}
	if e.Status == "" {
		e.Status = model.EdgeStatusUnverified
	}
	_, err := s.db.Exec(`INSERT INTO topology_edges (
		id, source_id, target_id, source_interface_id, target_interface_id,
		edge_type, discovery_source, confidence, user_confirmed, locked, status, label, unverified,
		last_observed, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(source_id, target_id,
		COALESCE(NULLIF(source_interface_id,''),'~'),
		COALESCE(NULLIF(target_interface_id,''),'~'), edge_type) DO UPDATE SET
		confidence = MAX(topology_edges.confidence, excluded.confidence),
		discovery_source = CASE WHEN topology_edges.user_confirmed = 1 THEN topology_edges.discovery_source ELSE excluded.discovery_source END,
		user_confirmed = MAX(topology_edges.user_confirmed, excluded.user_confirmed),
		locked = MAX(topology_edges.locked, excluded.locked),
		status = CASE WHEN topology_edges.locked = 1 THEN topology_edges.status ELSE excluded.status END,
		unverified = MIN(topology_edges.unverified, excluded.unverified),
		label = CASE WHEN excluded.label != '' THEN excluded.label ELSE topology_edges.label END,
		last_observed = excluded.last_observed,
		updated_at = excluded.updated_at`,
		e.ID, e.SourceID, e.TargetID, e.SourceInterfaceID, e.TargetInterfaceID,
		string(e.EdgeType), string(e.DiscoverySource), e.Confidence, boolInt(e.UserConfirmed), boolInt(e.Locked),
		string(e.Status), e.Label, boolInt(e.Unverified),
		e.LastObserved.UTC().Format(TimeLayout), e.CreatedAt.UTC().Format(TimeLayout), e.UpdatedAt.UTC().Format(TimeLayout))
	if err != nil {
		return err
	}
	// Refresh ID for caller.
	updated, gerr := s.GetEdgeByTuple(e.SourceID, e.TargetID, e.SourceInterfaceID, e.TargetInterfaceID, e.EdgeType)
	if gerr == nil {
		e.ID = updated.ID
	}
	return nil
}

// GetEdgeByTuple finds an edge by its identity tuple.
func (s *Store) GetEdgeByTuple(src, dst, srcIface, dstIface string, edgeType model.EdgeType) (*model.TopologyEdge, error) {
	row := s.db.QueryRow(`SELECT `+edgeColumns+` FROM topology_edges
		WHERE source_id = ? AND target_id = ?
		  AND COALESCE(NULLIF(source_interface_id,''),'~') = COALESCE(NULLIF(?,''),'~')
		  AND COALESCE(NULLIF(target_interface_id,''),'~') = COALESCE(NULLIF(?,''),'~')
		  AND edge_type = ?`,
		src, dst, srcIface, dstIface, string(edgeType))
	e, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// UpdateEdge applies a partial update to an edge.
func (s *Store) UpdateEdge(id string, patch map[string]any) (*model.TopologyEdge, error) {
	if len(patch) == 0 {
		return s.GetEdge(id)
	}
	allowed := map[string]string{
		"source_interface_id": "source_interface_id",
		"target_interface_id": "target_interface_id",
		"edge_type":           "edge_type",
		"confidence":          "confidence",
		"user_confirmed":      "user_confirmed",
		"locked":              "locked",
		"status":              "status",
		"label":               "label",
		"unverified":          "unverified",
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
		return s.GetEdge(id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, s.now(), id)
	res, err := s.db.Exec("UPDATE topology_edges SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetEdge(id)
}

// DeleteEdge removes an edge. Locked edges cannot be deleted through this path.
func (s *Store) DeleteEdge(id string) error {
	edge, err := s.GetEdge(id)
	if err != nil {
		return err
	}
	if edge.Locked {
		return errors.New("store: edge is locked and cannot be deleted")
	}
	res, err := s.db.Exec(`DELETE FROM topology_edges WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountEdges returns the number of edges.
func (s *Store) CountEdges() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM topology_edges`).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// SaveLayout persists a node position.
func (s *Store) SaveLayout(l *model.TopologyLayout) error {
	if l.ID == "" {
		l.ID = newID("lay")
	}
	l.UpdatedAt = Now()
	_, err := s.db.Exec(`INSERT INTO topology_layout (id, node_id, x, y, pinned, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET
			x = excluded.x, y = excluded.y, pinned = excluded.pinned, updated_at = excluded.updated_at`,
		l.ID, l.NodeID, l.X, l.Y, boolInt(l.Pinned), l.UpdatedAt.UTC().Format(TimeLayout))
	return err
}

// ListLayout returns all persisted node positions.
func (s *Store) ListLayout() ([]model.TopologyLayout, error) {
	rows, err := s.db.Query(`SELECT id, node_id, x, y, pinned, updated_at FROM topology_layout`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.TopologyLayout
	for rows.Next() {
		var l model.TopologyLayout
		var pinned int
		if err := rows.Scan(&l.ID, &l.NodeID, &l.X, &l.Y, &pinned, &l.UpdatedAt); err != nil {
			return nil, err
		}
		l.Pinned = pinned == 1
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteLayout removes a persisted node position.
func (s *Store) DeleteLayout(nodeID string) error {
	_, err := s.db.Exec(`DELETE FROM topology_layout WHERE node_id = ?`, nodeID)
	return err
}

// ---------------------------------------------------------------------------
// Probes
// ---------------------------------------------------------------------------

const probeColumns = `id, name, kind, target, port, protocol, source_id, interval, timeout, enabled,
	device_id, failure_threshold, recover_confirm, state, state_reason,
	consecutive_failures, consecutive_successes, last_result, last_latency_ms, last_error,
	last_run_at, last_success_at, created_at, updated_at`

func scanProbe(sc interface{ Scan(...any) error }) (*model.ProbeTarget, error) {
	p := &model.ProbeTarget{}
	var (
		kind, protocol, state, lastResult string
		enabled                           int
		deviceID                          sql.NullString
		lastRun, lastSuccess              sql.NullTime
	)
	err := sc.Scan(&p.ID, &p.Name, &kind, &p.Target, &p.Port, &protocol, &p.SourceID, &p.Interval, &p.Timeout, &enabled,
		&deviceID, &p.FailureThreshold, &p.RecoverConfirm, &state, &p.StateReason,
		&p.ConsecutiveFailures, &p.ConsecutiveSuccesses, &lastResult, &p.LastLatencyMS, &p.LastError,
		&lastRun, &lastSuccess, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	// device_id is NULL for a probe that is not attached to a device; the model
	// keeps a plain string so readers see '' in both representations.
	p.DeviceID = deviceID.String
	p.Kind = model.ProbeKind(kind)
	p.Protocol = model.ProbeProtocol(protocol)
	p.State = model.DeviceState(state)
	p.Enabled = enabled == 1
	p.LastResult = model.ProbeRunStatus(lastResult)
	p.LastRunAt = scanTime(lastRun)
	p.LastSuccessAt = scanTime(lastSuccess)
	return p, nil
}

// CreateProbe inserts a new probe target.
func (s *Store) CreateProbe(p *model.ProbeTarget) error {
	if p.ID == "" {
		p.ID = newID("probe")
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = Now()
	}
	p.UpdatedAt = Now()
	if p.State == "" {
		p.State = model.StateUnknown
	}
	_, err := s.db.Exec(`INSERT INTO probe_targets (
		id, name, kind, target, port, protocol, source_id, interval, timeout, enabled,
		device_id, failure_threshold, recover_confirm, state, state_reason,
		consecutive_failures, consecutive_successes, last_result, last_latency_ms, last_error,
		last_run_at, last_success_at, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, string(p.Kind), p.Target, p.Port, string(p.Protocol), p.SourceID, p.Interval, p.Timeout, boolInt(p.Enabled),
		nullString(p.DeviceID), p.FailureThreshold, p.RecoverConfirm, string(p.State), p.StateReason,
		p.ConsecutiveFailures, p.ConsecutiveSuccesses, string(p.LastResult), p.LastLatencyMS, p.LastError,
		nullTime(p.LastRunAt), nullTime(p.LastSuccessAt),
		p.CreatedAt.UTC().Format(TimeLayout), p.UpdatedAt.UTC().Format(TimeLayout))
	return err
}

// UpdateProbe applies a partial update to a probe target.
func (s *Store) UpdateProbe(id string, patch map[string]any) (*model.ProbeTarget, error) {
	if len(patch) == 0 {
		return s.GetProbe(id)
	}
	allowed := map[string]string{
		"name": "name", "target": "target", "port": "port", "protocol": "protocol",
		"source_id": "source_id", "interval": "interval", "timeout": "timeout", "enabled": "enabled",
		"kind": "kind", "failure_threshold": "failure_threshold", "recover_confirm": "recover_confirm",
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
		return s.GetProbe(id)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, s.now(), id)
	res, err := s.db.Exec("UPDATE probe_targets SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetProbe(id)
}

// UpdateProbeRuntime stores the outcome of a probe run on the target row.
func (s *Store) UpdateProbeRuntime(p *model.ProbeTarget) error {
	_, err := s.db.Exec(`UPDATE probe_targets SET
		state = ?, state_reason = ?, consecutive_failures = ?, consecutive_successes = ?,
		last_result = ?, last_latency_ms = ?, last_error = ?, last_run_at = ?, last_success_at = ?, updated_at = ?
		WHERE id = ?`,
		string(p.State), p.StateReason, p.ConsecutiveFailures, p.ConsecutiveSuccesses,
		string(p.LastResult), p.LastLatencyMS, p.LastError,
		nullTime(p.LastRunAt), nullTime(p.LastSuccessAt), s.now(), p.ID)
	return err
}

// GetProbe returns a probe by ID.
func (s *Store) GetProbe(id string) (*model.ProbeTarget, error) {
	row := s.db.QueryRow("SELECT "+probeColumns+" FROM probe_targets WHERE id = ?", id)
	p, err := scanProbe(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListProbes returns all probes.
func (s *Store) ListProbes() ([]model.ProbeTarget, error) {
	rows, err := s.db.Query("SELECT " + probeColumns + " FROM probe_targets ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeTarget
	for rows.Next() {
		p, err := scanProbe(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListEnabledProbes returns probes that should run.
func (s *Store) ListEnabledProbes() ([]model.ProbeTarget, error) {
	rows, err := s.db.Query("SELECT " + probeColumns + " FROM probe_targets WHERE enabled = 1 ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeTarget
	for rows.Next() {
		p, err := scanProbe(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListProbesByDevice returns probes attached to a device.
func (s *Store) ListProbesByDevice(deviceID string) ([]model.ProbeTarget, error) {
	rows, err := s.db.Query("SELECT "+probeColumns+" FROM probe_targets WHERE device_id = ? ORDER BY name", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeTarget
	for rows.Next() {
		p, err := scanProbe(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// DeleteProbe removes a probe and its results.
func (s *Store) DeleteProbe(id string) error {
	res, err := s.db.Exec(`DELETE FROM probe_targets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveProbeResult records a probe result.
func (s *Store) SaveProbeResult(r *model.ProbeResult) error {
	if r.ID == "" {
		r.ID = newID("res")
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = Now()
	}
	_, err := s.db.Exec(`INSERT INTO probe_results (id, probe_id, timestamp, success, latency_ms, status, error_type, error_message)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.ID, r.ProbeID, r.Timestamp.UTC().Format(TimeLayout), boolInt(r.Success), r.LatencyMS,
		string(r.Status), r.ErrorType, r.ErrorMessage)
	return err
}

// LatestProbeResult returns the most recent result for a probe.
func (s *Store) LatestProbeResult(probeID string) (*model.ProbeResult, error) {
	row := s.db.QueryRow(`SELECT id, probe_id, timestamp, success, latency_ms, status, error_type, error_message
		FROM probe_results WHERE probe_id = ? ORDER BY timestamp DESC LIMIT 1`, probeID)
	r := &model.ProbeResult{}
	var status string
	var success int
	err := row.Scan(&r.ID, &r.ProbeID, &r.Timestamp, &success, &r.LatencyMS, &status, &r.ErrorType, &r.ErrorMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Success = success == 1
	r.Status = model.ProbeRunStatus(status)
	return r, nil
}

// RecentProbeResults returns the most recent results for a probe.
func (s *Store) RecentProbeResults(probeID string, limit int) ([]model.ProbeResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, probe_id, timestamp, success, latency_ms, status, error_type, error_message
		FROM probe_results WHERE probe_id = ? ORDER BY timestamp DESC LIMIT ?`, probeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeResult
	for rows.Next() {
		var r model.ProbeResult
		var status string
		var success int
		if err := rows.Scan(&r.ID, &r.ProbeID, &r.Timestamp, &success, &r.LatencyMS, &status, &r.ErrorType, &r.ErrorMessage); err != nil {
			return nil, err
		}
		r.Success = success == 1
		r.Status = model.ProbeRunStatus(status)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProbeHistory returns aggregated recent probe history.
func (s *Store) ProbeHistory(since time.Time, limit int) ([]model.ProbeResult, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id, probe_id, timestamp, success, latency_ms, status, error_type, error_message
		FROM probe_results WHERE timestamp >= ? ORDER BY timestamp DESC LIMIT ?`,
		since.UTC().Format(TimeLayout), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProbeResult
	for rows.Next() {
		var r model.ProbeResult
		var status string
		var success int
		if err := rows.Scan(&r.ID, &r.ProbeID, &r.Timestamp, &success, &r.LatencyMS, &status, &r.ErrorType, &r.ErrorMessage); err != nil {
			return nil, err
		}
		r.Success = success == 1
		r.Status = model.ProbeRunStatus(status)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteOldProbeResults prunes probe history beyond the retention window.
func (s *Store) DeleteOldProbeResults(olderThan time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM probe_results WHERE timestamp < ?`, olderThan.UTC().Format(TimeLayout))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// SaveEvent records a network event.
func (s *Store) SaveEvent(e *model.NetworkEvent) error {
	if e.ID == "" {
		e.ID = newID("evt")
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = Now()
	}
	// device_id and edge_id are nullable so history survives the deletion of the
	// row it refers to. An empty string means "no reference".
	_, err := s.db.Exec(`INSERT INTO network_events
		(id, event_type, device_id, edge_id, old_value, new_value, message, evidence, timestamp)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		e.ID, string(e.EventType), nullString(e.DeviceID), nullString(e.EdgeID),
		e.OldValue, e.NewValue, e.Message, e.Evidence,
		e.Timestamp.UTC().Format(TimeLayout))
	return err
}

// EventFilter constrains event listings.
type EventFilter struct {
	Types    []model.EventType
	DeviceID string
	Since    time.Time
	Until    time.Time
	Query    string
	Limit    int
	Offset   int
}

// ListEvents returns events matching the filter, newest first.
func (s *Store) ListEvents(f EventFilter) ([]model.NetworkEvent, int, error) {
	var (
		where []string
		args  []any
	)
	if len(f.Types) > 0 {
		ph, _ := placeholders(len(f.Types))
		where = append(where, "event_type IN ("+ph+")")
		for _, t := range f.Types {
			args = append(args, string(t))
		}
	}
	if f.DeviceID != "" {
		where = append(where, "device_id = ?")
		args = append(args, f.DeviceID)
	}
	if !f.Since.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, f.Since.UTC().Format(TimeLayout))
	}
	if !f.Until.IsZero() {
		where = append(where, "timestamp <= ?")
		args = append(args, f.Until.UTC().Format(TimeLayout))
	}
	if f.Query != "" {
		where = append(where, "(message LIKE ? OR new_value LIKE ? OR old_value LIKE ?)")
		like := "%" + escapeLike(f.Query) + "%"
		args = append(args, like, like, like)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM network_events"+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`SELECT id, event_type, device_id, edge_id, old_value, new_value, message, evidence, timestamp
		FROM network_events`+clause+" ORDER BY timestamp DESC LIMIT ? OFFSET ?",
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []model.NetworkEvent
	for rows.Next() {
		var e model.NetworkEvent
		var et string
		var deviceID, edgeID sql.NullString
		if err := rows.Scan(&e.ID, &et, &deviceID, &edgeID, &e.OldValue, &e.NewValue, &e.Message, &e.Evidence, &e.Timestamp); err != nil {
			return nil, 0, err
		}
		e.DeviceID = deviceID.String
		e.EdgeID = edgeID.String
		e.EventType = model.EventType(et)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// ListEventsByDevice returns the most recent events for a device.
func (s *Store) ListEventsByDevice(deviceID string, limit int) ([]model.NetworkEvent, error) {
	ev, _, err := s.ListEvents(EventFilter{DeviceID: deviceID, Limit: limit})
	return ev, err
}

// RecentEvents returns the newest events across all devices.
func (s *Store) RecentEvents(limit int) ([]model.NetworkEvent, error) {
	ev, _, err := s.ListEvents(EventFilter{Limit: limit})
	return ev, err
}

// DeleteOldEvents prunes events beyond the retention window.
func (s *Store) DeleteOldEvents(olderThan time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM network_events WHERE timestamp < ?`, olderThan.UTC().Format(TimeLayout))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// GetSetting returns a setting value.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting stores a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, s.now())
	return err
}

// ListSettings returns all settings.
func (s *Store) ListSettings() ([]model.AppSetting, error) {
	rows, err := s.db.Query(`SELECT key, value, updated_at FROM app_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AppSetting
	for rows.Next() {
		var s2 model.AppSetting
		if err := rows.Scan(&s2.Key, &s2.Value, &s2.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s2)
	}
	return out, rows.Err()
}

// DeleteSetting removes a setting.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM app_settings WHERE key = ?`, key)
	return err
}

// ---------------------------------------------------------------------------
// Aggregates used by the overview endpoint
// ---------------------------------------------------------------------------

// DeviceCounts returns aggregate device counters.
func (s *Store) DeviceCounts() (model.DeviceCounts, error) {
	var c model.DeviceCounts
	row := s.db.QueryRow(`SELECT
		COUNT(*) AS total,
		COALESCE(SUM(CASE WHEN state = 'online' THEN 1 ELSE 0 END),0) AS online,
		COALESCE(SUM(CASE WHEN state = 'offline' THEN 1 ELSE 0 END),0) AS offline,
		COALESCE(SUM(CASE WHEN state = 'unknown' THEN 1 ELSE 0 END),0) AS unknown,
		COALESCE(SUM(CASE WHEN state = 'sleeping' THEN 1 ELSE 0 END),0) AS sleeping,
		COALESCE(SUM(CASE WHEN is_hidden = 1 THEN 1 ELSE 0 END),0) AS hidden,
		COALESCE(SUM(CASE WHEN is_manual = 1 THEN 1 ELSE 0 END),0) AS manual
		FROM devices`)
	err := row.Scan(&c.Total, &c.Online, &c.Offline, &c.Unknown, &c.Sleeping, &c.Hidden, &c.Manual)
	return c, err
}

// CountDevicesByType returns device counts grouped by type.
func (s *Store) CountDevicesByType() (map[model.DeviceType]int, error) {
	rows, err := s.db.Query(`SELECT type, COUNT(*) FROM devices WHERE is_hidden = 0 GROUP BY type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[model.DeviceType]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[model.DeviceType(t)] = n
	}
	return out, rows.Err()
}
