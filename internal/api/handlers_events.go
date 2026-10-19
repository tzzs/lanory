package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Handlers for /api/v1/events, the SSE stream and /api/v1/settings.

// handleListEvents serves GET /api/v1/events.
func (a *App) handleListEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pagination(r, DefaultLimit, maxEventsLimit)
	if err != nil {
		badRequest(w, err.Error())
		return
	}

	filter := database.EventFilter{
		DeviceID: strings.TrimSpace(q.Get("device_id")),
		Query:    strings.TrimSpace(q.Get("query")),
		Limit:    limit,
		Offset:   offset,
	}
	if v := strings.TrimSpace(q.Get("type")); v != "" {
		types, err := parseEventTypes(v)
		if err != nil {
			badRequestField(w, "type", err.Error())
			return
		}
		filter.Types = types
	}
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		t, err := ParseTime(v)
		if err != nil {
			badRequestField(w, "since", err.Error())
			return
		}
		filter.Since = t
	}
	if v := strings.TrimSpace(q.Get("until")); v != "" {
		t, err := ParseTime(v)
		if err != nil {
			badRequestField(w, "until", err.Error())
			return
		}
		filter.Until = t
	}

	events, total, err := a.store.ListEvents(filter)
	if err != nil {
		internalError(w, err)
		return
	}
	if events == nil {
		events = []model.NetworkEvent{}
	}
	writeJSON(w, http.StatusOK, model.EventList{
		Events: events,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// parseEventTypes splits a comma separated list of event types.
func parseEventTypes(v string) ([]model.EventType, error) {
	var out []model.EventType
	for _, part := range strings.Split(v, ",") {
		t := model.EventType(strings.TrimSpace(part))
		if t == "" {
			continue
		}
		if !isKnownEventType(t) {
			return nil, invalid("unknown event type %q", t)
		}
		out = append(out, t)
	}
	return out, nil
}

// knownEventTypes is the set of event types the API accepts as a filter.
var knownEventTypes = map[model.EventType]bool{
	model.EventDeviceOnline:      true,
	model.EventDeviceOffline:     true,
	model.EventDeviceDiscovered:  true,
	model.EventDeviceRemoved:     true,
	model.EventDeviceUpdated:     true,
	model.EventDeviceIPChanged:   true,
	model.EventDeviceMACChanged:  true,
	model.EventLinkUp:            true,
	model.EventLinkDown:          true,
	model.EventSpeedChanged:      true,
	model.EventTopologyChanged:   true,
	model.EventProbeCompleted:    true,
	model.EventDiscoveryStarted:  true,
	model.EventDiscoveryFinished: true,
	model.EventDeviceMerged:      true,
	model.EventDeviceHidden:      true,
}

// isKnownEventType reports whether t is a filterable event type.
func isKnownEventType(t model.EventType) bool { return knownEventTypes[t] }

// handleGetEvent serves GET /api/v1/events/{id}.
//
// The store has no single-event lookup, so the row is fetched directly. The
// query is parameterised and reads the same columns as Store.ListEvents, which
// keeps the wire shape identical to the listing endpoint.
func (a *App) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	row := a.store.DB().QueryRow(`SELECT id, event_type, device_id, edge_id, old_value, new_value,
		message, evidence, timestamp FROM network_events WHERE id = ?`, id)

	var e model.NetworkEvent
	var eventType string
	if err := row.Scan(&e.ID, &eventType, &e.DeviceID, &e.EdgeID, &e.OldValue, &e.NewValue,
		&e.Message, &e.Evidence, &e.Timestamp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(w, "event")
			return
		}
		internalError(w, err)
		return
	}
	e.EventType = model.EventType(eventType)
	writeJSON(w, http.StatusOK, e)
}

// ---------------------------------------------------------------------------
// SSE
// ---------------------------------------------------------------------------

// handleEventStream serves GET /api/v1/events/stream.
//
// Protocol notes, all of which are observable behaviour and covered by tests:
//
//   - Content-Type is text/event-stream and the response is flushed after every
//     frame so a proxy cannot buffer the stream into uselessness.
//   - Each frame carries an "id:", an "event:" and a "data:" line. The id is
//     the bus sequence number, which lets a client detect a gap after a
//     reconnect.
//   - A comment line (": heartbeat") is written every 15s. Comments are ignored
//     by EventSource but keep idle connections and intermediaries alive.
//   - Last-Event-ID is honoured: events retained by the bus that are newer than
//     the supplied sequence are replayed before live events, so a client that
//     reconnects does not silently miss what happened while it was away.
//   - The subscription is removed and the goroutine exits as soon as the client
//     disconnects, the request context is cancelled, or the server shuts down.
func (a *App) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if a.bus == nil {
		unavailable(w, "realtime events are not available: no event bus is configured")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		internalError(w, fmt.Errorf("response writer does not support flushing"))
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	// A proxy must not cache or transform a stream.
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	h.Set("Connection", "keep-alive")
	// Disable proxy buffering (nginx and others).
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := a.bus.Subscribe()
	defer cancel()

	// Replay anything the client missed.
	if last := lastEventID(r); last > 0 {
		for _, e := range a.bus.Recent() {
			if e.Seq > last {
				if !writeSSEFrame(w, flusher, toSSEEvent(e)) {
					return
				}
			}
		}
	}

	ticker := time.NewTicker(defaultSSEHeartbeat)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.Done():
			return
		case <-ticker.C:
			if !writeComment(w, flusher, "heartbeat") {
				return
			}
		case e, ok := <-ch:
			if !ok {
				// The bus dropped this subscriber for being too slow.
				return
			}
			if !writeSSEFrame(w, flusher, toSSEEvent(e)) {
				return
			}
		}
	}
}

// lastEventID reads the Last-Event-ID header, then falls back to the
// lastEventId query parameter (some proxies strip the header).
func lastEventID(r *http.Request) uint64 {
	v := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if v == "" {
		v = strings.TrimSpace(r.URL.Query().Get("lastEventId"))
	}
	if v == "" {
		return 0
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// writeComment writes an SSE comment line.
//
// Comments are ignored by EventSource but keep an idle connection, and every
// intermediary that would otherwise time it out, alive. It reports whether the
// write succeeded.
func writeComment(w io.Writer, flusher http.Flusher, text string) bool {
	if _, err := fmt.Fprintf(w, ": %s\n\n", text); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// writeSSEFrame writes one SSE frame. It reports whether the write succeeded;
// a false return means the client is gone and the handler must stop.
func writeSSEFrame(w http.ResponseWriter, flusher http.Flusher, e sseEvent) bool {
	data, err := jsonMarshal(e)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// handleGetSettings serves GET /api/v1/settings.
func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.settings.All()
	if err != nil {
		internalError(w, err)
		return
	}
	if settings == nil {
		settings = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// handlePatchSettings serves PATCH /api/v1/settings.
//
// Only known keys are accepted: an unknown key is a 400, because silently
// storing a setting that nothing reads is worse than refusing it.
func (a *App) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(r, &body); err != nil {
		badRequestFrom(w, err)
		return
	}
	if len(body) == 0 {
		badRequest(w, "request body must contain at least one setting")
		return
	}
	for k, v := range body {
		if !isKnownSetting(k) {
			badRequestField(w, k, "unknown setting key")
			return
		}
		if v == nil {
			badRequestField(w, k, "value must not be null")
			return
		}
	}

	updated, err := a.settings.Patch(body)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	if updated == nil {
		updated = map[string]string{}
	}
	if a.bus != nil {
		for k, v := range body {
			a.bus.PublishSettingsUpdated(k, settingValue(v))
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated})
}
