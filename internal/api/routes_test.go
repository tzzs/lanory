package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// errorsIs is the matching helper used by the assertions above.
func errorsIs(err, target error) bool { return errors.Is(err, target) }

// Route contract tests: every documented route returns the documented shape
// and status code.

// TestHealthRoute checks GET /api/v1/health.
func TestHealthRoute(t *testing.T) {
	app := newTestApp(t)

	var got model.HealthResponse
	rec := getJSON(t, app, "/api/v1/health", &got)
	assertStatus(t, rec, http.StatusOK)

	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Version == "" {
		t.Error("version must not be empty")
	}
	if got.DemoMode {
		t.Error("demo_mode must be false outside demo mode")
	}
	if got.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds = %d, must not be negative", got.UptimeSeconds)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

// TestHealthIsPublic checks that health is the one unauthenticated route.
func TestHealthIsPublic(t *testing.T) {
	cfg := testConfig(t)
	cfg.Auth.Enabled = true
	cfg.Auth.Token = "secret-token"
	app := newTestAppWithConfig(t, cfg)

	rec := do(t, app, http.MethodGet, "/api/v1/health", "")
	assertStatus(t, rec, http.StatusOK)

	// The same client with no token cannot read devices.
	rec = do(t, app, http.MethodGet, "/api/v1/devices", "")
	assertError(t, rec, http.StatusUnauthorized)
}

// TestOverviewRoute checks GET /api/v1/overview.
func TestOverviewRoute(t *testing.T) {
	app := newTestApp(t)

	var got model.Overview
	rec := getJSON(t, app, "/api/v1/overview", &got)
	assertStatus(t, rec, http.StatusOK)

	// Counts must match the seeded fixture network: the router plus the eight
	// devices from fixtures/easepi-r1.
	if got.Counts.Total != 9 {
		t.Errorf("counts.total = %d, want 9", got.Counts.Total)
	}
	// The router (self) plus Windows PC, iPhone, Desk Lamp and ESP32 Sensor.
	if got.Counts.Online != 5 {
		t.Errorf("counts.online = %d, want 5", got.Counts.Online)
	}
	if got.Counts.Offline != 1 {
		t.Errorf("counts.offline = %d, want 1", got.Counts.Offline)
	}
	if got.Counts.Manual != 1 {
		t.Errorf("counts.manual = %d, want 1 (the optical modem)", got.Counts.Manual)
	}

	// Interfaces must be a JSON array, never null.
	if jsonFieldType(t, rec.Body.Bytes(), "interfaces") != "array" {
		t.Fatal("interfaces is not a JSON array")
	}
	if len(got.Interfaces) == 0 {
		t.Fatal("interfaces must not be empty")
	}
	// eth3 has no carrier, so it must be reported as link_down with a reason.
	var foundNoCarrier bool
	for _, is := range got.Interfaces {
		if is.Interface.Name == "eth3" {
			foundNoCarrier = true
			if is.Status != model.EdgeStatusLinkDown {
				t.Errorf("eth3 status = %q, want link_down", is.Status)
			}
			if is.Reason == "" {
				t.Error("eth3 must carry a reason")
			}
		}
	}
	if !foundNoCarrier {
		t.Error("eth3 is missing from the interface list")
	}

	if len(got.Networks) == 0 {
		t.Error("networks must not be empty")
	} else if got.Networks[0].CIDR != "192.168.100.0/24" {
		t.Errorf("networks[0].cidr = %q, want 192.168.100.0/24", got.Networks[0].CIDR)
	}
	if got.GeneratedAt.IsZero() {
		t.Error("generated_at must be set")
	}
	if got.DiscoveryEnabled != true {
		t.Error("discovery_enabled should follow the configuration")
	}

	// The offline device must appear in the recent offline list.
	var sawOffline bool
	for _, d := range got.RecentOffline {
		if d.ID == "dev-mac" {
			sawOffline = true
		}
	}
	if !sawOffline {
		t.Error("the offline Mac Studio must be listed in recent_offline_devices")
	}

	// No-carrier must surface as an anomaly, because that is the whole point.
	var sawAnomaly bool
	for _, an := range got.Anomalies {
		if an.Severity == "" || an.Title == "" {
			t.Errorf("anomaly %q must have a severity and a title", an.ID)
		}
		if an.ID == "anom-link-if-eth3" {
			sawAnomaly = true
		}
	}
	if !sawAnomaly {
		t.Error("eth3 with no carrier must produce an anomaly")
	}
}

// TestDevicesListRoute checks GET /api/v1/devices and its filters.
func TestDevicesListRoute(t *testing.T) {
	app := newTestApp(t)

	var got model.DeviceList
	rec := getJSON(t, app, "/api/v1/devices", &got)
	assertStatus(t, rec, http.StatusOK)

	if got.Total != 9 {
		t.Errorf("total = %d, want 9", got.Total)
	}
	if len(got.Devices) != 9 {
		t.Errorf("len(devices) = %d, want 9", len(got.Devices))
	}
	if got.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", got.Limit, DefaultLimit)
	}
	// Every device must carry a non-null discovery_sources array.
	for _, d := range got.Devices {
		if raw := jsonFieldOf(t, rec.Body.Bytes(), "devices", d.ID); raw != "" {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &fields); err != nil {
				t.Fatalf("device %s is not a JSON object: %v", d.ID, err)
			}
			src, ok := fields["discovery_sources"]
			if !ok || string(src) == "null" {
				t.Errorf("device %s must have a discovery_sources array", d.ID)
			}
		}
	}
	if jsonFieldType(t, rec.Body.Bytes(), "devices") != "array" {
		t.Error("devices must be a JSON array")
	}

	// Pagination.
	var page model.DeviceList
	getJSON(t, app, "/api/v1/devices?limit=3&offset=0", &page)
	if len(page.Devices) != 3 || page.Limit != 3 {
		t.Errorf("limit=3 returned %d devices, limit=%d", len(page.Devices), page.Limit)
	}
	if page.Total != 9 {
		t.Errorf("total must stay 9 across pages, got %d", page.Total)
	}
	getJSON(t, app, "/api/v1/devices?limit=3&offset=6", &page)
	if len(page.Devices) != 3 {
		t.Errorf("last page should return 3 devices, got %d", len(page.Devices))
	}

	// A limit above the maximum is clamped, not an error.
	getJSON(t, app, "/api/v1/devices?limit=100000", &page)
	if page.Limit != MaxLimit {
		t.Errorf("limit = %d, want clamped to %d", page.Limit, MaxLimit)
	}

	// Filters.
	getJSON(t, app, "/api/v1/devices?state=online", &page)
	for _, d := range page.Devices {
		if d.State != model.StateOnline {
			t.Errorf("state filter leaked %q", d.State)
		}
	}
	if len(page.Devices) != 5 {
		t.Errorf("state=online returned %d, want 5", len(page.Devices))
	}

	getJSON(t, app, "/api/v1/devices?type=smartphone", &page)
	if len(page.Devices) != 1 || page.Devices[0].Type != model.DeviceTypeSmartphone {
		t.Errorf("type=smartphone returned %d devices", len(page.Devices))
	}

	getJSON(t, app, "/api/v1/devices?query=iPhone", &page)
	if len(page.Devices) != 1 || page.Devices[0].ID != "dev-iphone" {
		t.Errorf("query=iPhone returned %d devices", len(page.Devices))
	}

	getJSON(t, app, "/api/v1/devices?source=dhcp", &page)
	if len(page.Devices) == 0 {
		t.Error("source=dhcp returned nothing")
	}

	// sort/order.
	getJSON(t, app, "/api/v1/devices?sort=name&order=asc", &page)
	// The store sorts with SQLite COLLATE NOCASE, which folds ASCII case.
	for i := 1; i < len(page.Devices); i++ {
		if strings.ToLower(page.Devices[i-1].Name) > strings.ToLower(page.Devices[i].Name) {
			t.Errorf("sort=name&order=asc is not sorted: %q before %q",
				page.Devices[i-1].Name, page.Devices[i].Name)
		}
	}
}

// TestDeviceDetailRoute checks GET /api/v1/devices/{id}.
func TestDeviceDetailRoute(t *testing.T) {
	app := newTestApp(t)

	var got model.DeviceDetail
	rec := getJSON(t, app, "/api/v1/devices/dev-win", &got)
	assertStatus(t, rec, http.StatusOK)

	if got.Device == nil || got.Device.ID != "dev-win" {
		t.Fatal("device must be present")
	}
	if got.Device.PrimaryIPv4 != "192.0.2.116" {
		t.Errorf("primary_ipv4 = %q, want 192.0.2.116", got.Device.PrimaryIPv4)
	}
	if got.Device.PrimaryMAC != "02:11:22:33:44:b7" {
		t.Errorf("primary_mac = %q, want 02:11:22:33:44:b7", got.Device.PrimaryMAC)
	}
	if len(got.Addresses) == 0 {
		t.Error("addresses must not be empty")
	}
	if len(got.Identities) == 0 {
		t.Error("identities must not be empty")
	}
	if len(got.Probes) == 0 {
		t.Error("probes must not be empty")
	}
	if got.LastProbe == nil {
		t.Error("last_probe must be present")
	} else if got.LastProbe.Status != model.ProbeRunSuccess {
		t.Errorf("last_probe.status = %q, want success", got.LastProbe.Status)
	}

	// The empty-list contract: every slice field is [] not null.
	for _, field := range []string{"addresses", "identities", "interfaces", "edges", "probes", "events"} {
		if raw := jsonFieldType(t, rec.Body.Bytes(), field); raw != "array" {
			t.Errorf("%s is %s, want array", field, raw)
		}
	}

	// A device with nothing attached still yields arrays, not nulls.
	rec = getJSON(t, app, "/api/v1/devices/dev-ipv6only", &got)
	assertStatus(t, rec, http.StatusOK)
	for _, field := range []string{"addresses", "identities", "interfaces", "edges", "probes", "events"} {
		if raw := jsonFieldType(t, rec.Body.Bytes(), field); raw != "array" {
			t.Errorf("%s is %s, want array", field, raw)
		}
	}

	// Unknown device.
	rec = getJSON(t, app, "/api/v1/devices/dev-nope", nil)
	assertError(t, rec, http.StatusNotFound)
}

// TestTopologyRoutes checks GET /api/v1/topology and /topology/evidence.
func TestTopologyRoutes(t *testing.T) {
	app := newTestApp(t)

	var topo model.Topology
	rec := getJSON(t, app, "/api/v1/topology", &topo)
	assertStatus(t, rec, http.StatusOK)

	if len(topo.Nodes) == 0 {
		t.Fatal("nodes must not be empty")
	}
	// Every device is a node, and the two seeded edges are present.
	if len(topo.Edges) != 2 {
		t.Errorf("len(edges) = %d, want 2 (the two seeded edges)", len(topo.Edges))
	}
	for _, n := range topo.Nodes {
		if n.Kind != "device" && n.Kind != "network" {
			t.Errorf("node %s has kind %q, want device or network", n.ID, n.Kind)
		}
		if n.Kind == "device" && n.Device == nil {
			t.Errorf("device node %s has no device payload", n.ID)
		}
		if n.Kind == "network" && n.Network == nil {
			t.Errorf("network node %s has no network payload", n.ID)
		}
	}
	if topo.GeneratedAt.IsZero() {
		t.Error("generated_at must be set")
	}
	for _, field := range []string{"nodes", "edges", "layout"} {
		if raw := jsonFieldType(t, rec.Body.Bytes(), field); raw != "array" {
			t.Errorf("%s is %s, want array", field, raw)
		}
	}

	// The Windows PC node must be a device node with its primary IP.
	var win model.TopologyNode
	for _, n := range topo.Nodes {
		if n.ID == "dev-win" {
			win = n
		}
	}
	if win.ID == "" {
		t.Fatal("the Windows PC node is missing")
	}
	if win.PrimaryIP != "192.0.2.116" {
		t.Errorf("primary_ip = %q, want 192.0.2.116", win.PrimaryIP)
	}
	if win.State != model.StateOnline {
		t.Errorf("state = %q, want online", win.State)
	}

	var ev model.TopologyEvidence
	rec = getJSON(t, app, "/api/v1/topology/evidence", &ev)
	assertStatus(t, rec, http.StatusOK)
	if len(ev.Edges) == 0 {
		t.Error("edges must not be empty")
	}
	if ev.Unmapped == nil {
		t.Error("unmapped_devices must be an array, not null")
	}
	for _, field := range []string{"edges", "unmapped_devices"} {
		if raw := jsonFieldType(t, rec.Body.Bytes(), field); raw != "array" {
			t.Errorf("%s is %s, want array", field, raw)
		}
	}
}

// TestEdgeLifecycle checks create/patch/delete of a topology edge, including
// the 409 on a locked edge and the 204 on delete.
func TestEdgeLifecycle(t *testing.T) {
	app := newTestApp(t)

	// Create.
	body := `{"source_id":"dev-router","target_id":"dev-iphone","edge_type":"user_defined","source_interface_id":"if-eth1","label":"eth1"}`
	rec := do(t, app, http.MethodPost, "/api/v1/topology/edges", body)
	assertStatus(t, rec, http.StatusCreated)

	var created model.TopologyEdge
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode edge: %v", err)
	}
	if created.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0 for a user asserted edge", created.Confidence)
	}
	if !created.UserConfirmed {
		t.Error("a user created edge must be user_confirmed")
	}
	if created.ID == "" {
		t.Fatal("created edge must have an id")
	}

	// A duplicate is a conflict, not a silent upsert.
	rec = do(t, app, http.MethodPost, "/api/v1/topology/edges", body)
	assertError(t, rec, http.StatusConflict)

	// Patch.
	rec = do(t, app, http.MethodPatch, "/api/v1/topology/edges/"+created.ID,
		`{"confidence":0.9,"locked":true,"label":"eth1 · 2.5 Gbps","status":"link_up"}`)
	assertStatus(t, rec, http.StatusOK)
	var patched model.TopologyEdge
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched edge: %v", err)
	}
	if patched.Confidence != 0.9 || !patched.Locked || patched.Label != "eth1 · 2.5 Gbps" {
		t.Errorf("patch did not apply: %+v", patched)
	}
	if patched.Status != model.EdgeStatusLinkUp {
		t.Errorf("status = %q, want link_up", patched.Status)
	}

	// A locked edge cannot be deleted.
	rec = do(t, app, http.MethodDelete, "/api/v1/topology/edges/"+created.ID, "")
	assertError(t, rec, http.StatusConflict)

	// Unlock, then delete.
	rec = do(t, app, http.MethodPatch, "/api/v1/topology/edges/"+created.ID, `{"locked":false}`)
	assertStatus(t, rec, http.StatusOK)
	rec = do(t, app, http.MethodDelete, "/api/v1/topology/edges/"+created.ID, "")
	assertStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Errorf("204 must have an empty body, got %q", rec.Body.String())
	}

	// Deleting it again is a 404.
	rec = do(t, app, http.MethodDelete, "/api/v1/topology/edges/"+created.ID, "")
	assertError(t, rec, http.StatusNotFound)
}

// TestLayoutRoutes checks POST /topology/layout and /topology/auto-layout.
func TestLayoutRoutes(t *testing.T) {
	app := newTestApp(t)

	rec := do(t, app, http.MethodPost, "/api/v1/topology/layout",
		`{"nodes":[{"node_id":"dev-win","x":120.5,"y":340,"pinned":true}]}`)
	assertStatus(t, rec, http.StatusOK)

	var saved struct {
		Saved int `json:"saved"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode layout response: %v", err)
	}
	if saved.Saved != 1 {
		t.Errorf("saved = %d, want 1", saved.Saved)
	}

	// The persisted position must come back through the topology.
	var topo model.Topology
	getJSON(t, app, "/api/v1/topology", &topo)
	var found bool
	for _, l := range topo.Layout {
		if l.NodeID == "dev-win" && l.X == 120.5 && l.Y == 340 && l.Pinned {
			found = true
		}
	}
	if !found {
		t.Error("the saved layout was not persisted")
	}

	// Auto-layout is the topology engine's job: 501 until it is wired.
	rec = do(t, app, http.MethodPost, "/api/v1/topology/auto-layout", `{"algorithm":"tree"}`)
	assertError(t, rec, http.StatusNotImplemented)
}

// TestDiscoveryRoutes checks the discovery endpoints.
func TestDiscoveryRoutes(t *testing.T) {
	app := newTestApp(t)

	var status model.DiscoveryStatus
	rec := getJSON(t, app, "/api/v1/discovery/status", &status)
	assertStatus(t, rec, http.StatusOK)
	if status.Enabled != true {
		t.Error("discovery_enabled should follow the configuration")
	}
	if status.Interval == "" {
		t.Error("interval must not be empty")
	}
	if len(status.Collectors) == 0 {
		t.Error("collectors must not be empty")
	}
	if status.Running {
		t.Error("nothing can be running without an engine")
	}
	for _, field := range []string{"collectors"} {
		if raw := jsonFieldType(t, rec.Body.Bytes(), field); raw != "array" {
			t.Errorf("%s is %s, want array", field, raw)
		}
	}

	var collectors struct {
		Collectors []model.CollectorStatus `json:"collectors"`
	}
	rec = getJSON(t, app, "/api/v1/discovery/collectors", &collectors)
	assertStatus(t, rec, http.StatusOK)
	if len(collectors.Collectors) == 0 {
		t.Fatal("collectors must not be empty")
	}
	for _, c := range collectors.Collectors {
		if c.Name == "" {
			t.Error("collector must have a name")
		}
		if c.Capabilities == nil {
			t.Errorf("collector %s must list capabilities as an array", c.Name)
		}
	}

	// Scanning needs the engine.
	rec = do(t, app, http.MethodPost, "/api/v1/discovery/scan", `{"collectors":["arp"],"subnets":["192.168.100.0/24"]}`)
	assertError(t, rec, http.StatusNotImplemented)

	// A scan of a subnet the user is not allowed to reach is a 400, not a 501:
	// the SSRF check happens before the capability check.
	rec = do(t, app, http.MethodPost, "/api/v1/discovery/scan", `{"subnets":["10.0.0.0/8"]}`)
	assertError(t, rec, http.StatusBadRequest)

	rec = do(t, app, http.MethodGet, "/api/v1/discovery/jobs/job-1", "")
	assertError(t, rec, http.StatusNotImplemented)

	rec = do(t, app, http.MethodPost, "/api/v1/discovery/jobs/job-1/cancel", "")
	assertError(t, rec, http.StatusNotImplemented)
}

// TestMonitoringRoutes checks the monitoring endpoints.
func TestMonitoringRoutes(t *testing.T) {
	app := newTestApp(t)

	var status model.MonitoringStatus
	rec := getJSON(t, app, "/api/v1/monitoring/status", &status)
	assertStatus(t, rec, http.StatusOK)

	if len(status.Probes) != 1 {
		t.Fatalf("len(probes) = %d, want 1", len(status.Probes))
	}
	p := status.Probes[0]
	if p.Probe.Target != "192.0.2.116" {
		t.Errorf("probe target = %q, want 192.0.2.116", p.Probe.Target)
	}
	if len(p.Recent) != 2 {
		t.Errorf("len(recent) = %d, want 2", len(p.Recent))
	}
	// One success and one timeout out of two runs: 50% loss, 4ms average.
	if p.LossPct != 50 {
		t.Errorf("loss_pct = %v, want 50", p.LossPct)
	}
	if p.AvgLatencyMS != 4 {
		t.Errorf("avg_latency_ms = %d, want 4", p.AvgLatencyMS)
	}
	if len(status.Interfaces) == 0 {
		t.Error("interfaces must not be empty")
	}

	var probes struct {
		Probes []model.ProbeTarget `json:"probes"`
	}
	rec = getJSON(t, app, "/api/v1/monitoring/probes", &probes)
	assertStatus(t, rec, http.StatusOK)
	if len(probes.Probes) != 1 {
		t.Errorf("len(probes) = %d, want 1", len(probes.Probes))
	}

	// Creating a probe with an allowed target works and normalises it.
	rec = do(t, app, http.MethodPost, "/api/v1/monitoring/probes",
		`{"name":"Mac Studio","kind":"device","device_id":"dev-mac","protocol":"icmp","interval":60}`)
	assertStatus(t, rec, http.StatusCreated)
	var probe model.ProbeTarget
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	if probe.Target != "192.0.2.222" {
		t.Errorf("probe target = %q, want the device address 192.0.2.222", probe.Target)
	}
	if probe.DeviceID != "dev-mac" {
		t.Errorf("device_id = %q, want dev-mac", probe.DeviceID)
	}

	// A tcp probe without a port is a 400.
	rec = do(t, app, http.MethodPost, "/api/v1/monitoring/probes",
		`{"name":"HTTP","kind":"network","target":"192.168.100.1","protocol":"tcp"}`)
	assertError(t, rec, http.StatusBadRequest)

	// Running a probe needs the engine.
	rec = do(t, app, http.MethodPost, "/api/v1/monitoring/probes/"+probe.ID+"/run", "")
	assertError(t, rec, http.StatusNotImplemented)

	// History.
	var history struct {
		Results []model.ProbeResult `json:"results"`
	}
	rec = getJSON(t, app, "/api/v1/monitoring/history?device_id=dev-win", &history)
	assertStatus(t, rec, http.StatusOK)
	if len(history.Results) != 2 {
		t.Errorf("len(results) = %d, want 2", len(history.Results))
	}
	if raw := jsonFieldType(t, rec.Body.Bytes(), "results"); raw != "array" {
		t.Errorf("results is %s, want array", raw)
	}
}

// TestEventRoutes checks GET /api/v1/events and /events/{id}.
func TestEventRoutes(t *testing.T) {
	app := newTestApp(t)

	var list model.EventList
	rec := getJSON(t, app, "/api/v1/events", &list)
	assertStatus(t, rec, http.StatusOK)

	if list.Total != 3 {
		t.Errorf("total = %d, want 3", list.Total)
	}
	if len(list.Events) != 3 {
		t.Errorf("len(events) = %d, want 3", len(list.Events))
	}
	// Newest first.
	for i := 1; i < len(list.Events); i++ {
		if list.Events[i-1].Timestamp.Before(list.Events[i].Timestamp) {
			t.Error("events must be ordered newest first")
		}
	}
	if list.Limit != DefaultLimit {
		t.Errorf("limit = %d, want %d", list.Limit, DefaultLimit)
	}

	// Filters.
	getJSON(t, app, "/api/v1/events?type=link.up", &list)
	if len(list.Events) != 1 || list.Events[0].EventType != model.EventLinkUp {
		t.Errorf("type filter returned %d events", len(list.Events))
	}
	getJSON(t, app, "/api/v1/events?device_id=dev-win", &list)
	if len(list.Events) != 3 {
		t.Errorf("device_id filter returned %d events, want 3", len(list.Events))
	}
	getJSON(t, app, "/api/v1/events?device_id=dev-mac", &list)
	if len(list.Events) != 0 {
		t.Errorf("device_id filter for an unrelated device returned %d events", len(list.Events))
	}
	getJSON(t, app, "/api/v1/events?query="+url.QueryEscape("1000 Mbps"), &list)
	if len(list.Events) != 1 {
		t.Errorf("query filter returned %d events, want 1", len(list.Events))
	}
	// since/until. The three seeded events are at now, -1m and -2m, so a
	// 90 second window keeps the two newest.
	sinceArg := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	getJSON(t, app, "/api/v1/events?since="+url.QueryEscape(sinceArg), &list)
	if len(list.Events) != 2 {
		t.Errorf("since filter returned %d events, want 2", len(list.Events))
	}

	// Single event.
	var ev model.NetworkEvent
	rec = getJSON(t, app, "/api/v1/events/evt-1", &ev)
	assertStatus(t, rec, http.StatusOK)
	if ev.EventType != model.EventSpeedChanged {
		t.Errorf("event_type = %q, want speed.changed", ev.EventType)
	}
	if ev.EdgeID == "" {
		t.Error("edge_id must be present")
	}

	rec = getJSON(t, app, "/api/v1/events/evt-nope", nil)
	assertError(t, rec, http.StatusNotFound)
}

// TestSettingsRoutes checks GET and PATCH /api/v1/settings.
func TestSettingsRoutes(t *testing.T) {
	app := newTestApp(t)

	var got struct {
		Settings map[string]string `json:"settings"`
	}
	rec := getJSON(t, app, "/api/v1/settings", &got)
	assertStatus(t, rec, http.StatusOK)
	if got.Settings["discovery.interval"] != "10m" {
		t.Errorf("discovery.interval = %q, want 10m", got.Settings["discovery.interval"])
	}

	// Patching a known key works.
	rec = do(t, app, http.MethodPatch, "/api/v1/settings", `{"discovery.enabled":false}`)
	assertStatus(t, rec, http.StatusOK)
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got.Settings["discovery.enabled"] != "false" {
		t.Errorf("discovery.enabled = %q, want false", got.Settings["discovery.enabled"])
	}

	// An unknown key is refused, not silently stored.
	rec = do(t, app, http.MethodPatch, "/api/v1/settings", `{"nonsense.key":"x"}`)
	assertError(t, rec, http.StatusBadRequest)

	// An empty body is refused.
	rec = do(t, app, http.MethodPatch, "/api/v1/settings", `{}`)
	assertError(t, rec, http.StatusBadRequest)
}

// TestDeviceLifecycle checks create/patch/merge/delete of devices.
func TestDeviceLifecycle(t *testing.T) {
	app := newTestApp(t)

	// Create a manual device.
	rec := do(t, app, http.MethodPost, "/api/v1/devices", `{
		"name":"Optical Modem",
		"type":"modem_ont",
		"ipv4":"192.168.1.2",
		"mac":"aa:bb:cc:dd:ee:ff",
		"parent_device_id":"dev-router",
		"connection_type":"physical_link",
		"description":"provided by ISP"
	}`)
	assertStatus(t, rec, http.StatusCreated)

	var created model.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	if !created.IsManual {
		t.Error("a device created through the API must be manual")
	}
	if created.State != model.StateUnknown {
		t.Errorf("state = %q, want unknown until probed", created.State)
	}
	if created.PrimaryIPv4 != "192.168.1.2" {
		t.Errorf("primary_ipv4 = %q, want 192.168.1.2", created.PrimaryIPv4)
	}
	if created.PrimaryMAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("primary_mac = %q, want aa:bb:cc:dd:ee:ff", created.PrimaryMAC)
	}

	// The parent relation must have become an edge.
	var topo model.Topology
	getJSON(t, app, "/api/v1/topology", &topo)
	var sawEdge bool
	for _, e := range topo.Edges {
		if e.SourceID == "dev-router" && e.TargetID == created.ID && e.Confidence == 1.0 {
			sawEdge = true
		}
	}
	if !sawEdge {
		t.Error("the parent relation must be stored as a user confirmed edge")
	}

	// Patch.
	rec = do(t, app, http.MethodPatch, "/api/v1/devices/"+created.ID,
		`{"name":"ISP Modem","is_locked":true}`)
	assertStatus(t, rec, http.StatusOK)
	var patched model.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	if patched.Name != "ISP Modem" || !patched.IsLocked {
		t.Errorf("patch did not apply: %+v", patched)
	}

	// A discovered device cannot be deleted outright.
	rec = do(t, app, http.MethodDelete, "/api/v1/devices/dev-win?mode=delete", "")
	assertError(t, rec, http.StatusConflict)

	// ...but it can be hidden.
	rec = do(t, app, http.MethodDelete, "/api/v1/devices/dev-win?mode=hide", "")
	assertStatus(t, rec, http.StatusNoContent)
	var list model.DeviceList
	getJSON(t, app, "/api/v1/devices", &list)
	for _, d := range list.Devices {
		if d.ID == "dev-win" {
			t.Error("a hidden device must not appear in the default listing")
		}
	}
	// The history is kept: the device is still fetchable by id.
	rec = do(t, app, http.MethodGet, "/api/v1/devices/dev-win", "")
	assertStatus(t, rec, http.StatusOK)

	// Merge: fold a second device into the manual one.
	rec = do(t, app, http.MethodPost, "/api/v1/devices", `{"name":"Modem Duplicate","type":"modem_ont"}`)
	assertStatus(t, rec, http.StatusCreated)
	var dup model.Device
	if err := json.Unmarshal(rec.Body.Bytes(), &dup); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	rec = do(t, app, http.MethodPost, "/api/v1/devices/"+created.ID+"/merge",
		`{"source_device_id":"`+dup.ID+`"}`)
	assertStatus(t, rec, http.StatusOK)
	rec = do(t, app, http.MethodGet, "/api/v1/devices/"+dup.ID, "")
	assertError(t, rec, http.StatusNotFound)

	// A manual device can be deleted.
	rec = do(t, app, http.MethodDelete, "/api/v1/devices/"+created.ID+"?mode=delete", "")
	assertStatus(t, rec, http.StatusNoContent)

	// Bad input on create.
	rec = do(t, app, http.MethodPost, "/api/v1/devices", `{"name":"","type":"router"}`)
	assertError(t, rec, http.StatusBadRequest)
	rec = do(t, app, http.MethodPost, "/api/v1/devices", `{"name":"X","type":"not_a_type"}`)
	assertError(t, rec, http.StatusBadRequest)
	rec = do(t, app, http.MethodPost, "/api/v1/devices", `{"name":"X","ipv4":"192.168.1.999"}`)
	assertError(t, rec, http.StatusBadRequest)
	rec = do(t, app, http.MethodPost, "/api/v1/devices", `{"name":"X","mac":"zz:bb:cc:dd:ee:ff"}`)
	assertError(t, rec, http.StatusBadRequest)
}

// TestNotFoundAndMethodNotAllowed checks the routing behaviour.
func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	app := newTestApp(t)

	// Unknown API path.
	rec := do(t, app, http.MethodGet, "/api/v1/nope", "")
	e := assertError(t, rec, http.StatusNotFound)
	if e.Code != CodeNotFound {
		t.Errorf("code = %q, want %q", e.Code, CodeNotFound)
	}
	rec = do(t, app, http.MethodGet, "/api/v1/devices/dev-1/nested", "")
	assertError(t, rec, http.StatusNotFound)

	// Wrong method on a known path.
	rec = do(t, app, http.MethodPut, "/api/v1/devices/dev-win", "")
	e = assertError(t, rec, http.StatusMethodNotAllowed)
	if e.Code != CodeMethodNotAllowed {
		t.Errorf("code = %q, want %q", e.Code, CodeMethodNotAllowed)
	}
	if allow := rec.Header().Get("Allow"); allow == "" {
		t.Error("a 405 must carry an Allow header")
	}
	rec = do(t, app, http.MethodDelete, "/api/v1/devices", "")
	assertError(t, rec, http.StatusMethodNotAllowed)
	rec = do(t, app, http.MethodPost, "/api/v1/health", "")
	assertError(t, rec, http.StatusMethodNotAllowed)

	// A missing id is a 404, not a 500.
	rec = do(t, app, http.MethodGet, "/api/v1/devices/", "")
	assertError(t, rec, http.StatusNotFound)
}

// TestPlaceholderPage checks that the server starts and serves a page when no
// frontend was built.
func TestPlaceholderPage(t *testing.T) {
	app := newTestApp(t)

	rec := do(t, app, http.MethodGet, "/", "")
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "Lanory") {
		t.Errorf("placeholder page does not mention Lanory: %s", rec.Body.String())
	}

	// A client-side route falls back to the same page.
	rec = do(t, app, http.MethodGet, "/topology", "")
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Lanory") {
		t.Error("SPA fallback must serve the entry page for client routes")
	}

	// Security headers are set on every response.
	rec = do(t, app, http.MethodGet, "/api/v1/health", "")
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestStaticAssetsFromDisk checks that an explicit web dir is served with a
// real SPA fallback and correct cache headers.
func TestStaticAssetsFromDisk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/index.html", "<html><body>REAL APP</body></html>")
	writeFile(t, dir+"/assets/app.js", "console.log('app')")
	writeFile(t, dir+"/vite.svg", "<svg/>")

	// The asset source is chosen at construction time, which is what a real
	// deployment wants: assets do not change while the process runs.
	app := newTestAppWithOptions(t, Options{WebDir: dir})

	rec := do(t, app, http.MethodGet, "/", "")
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "REAL APP") {
		t.Errorf("index.html was not served: %s", rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache, no-store, must-revalidate" {
		t.Errorf("index.html cache-control = %q", cc)
	}

	// A fingerprinted asset is cached immutably.
	rec = do(t, app, http.MethodGet, "/assets/app.js", "")
	assertStatus(t, rec, http.StatusOK)
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset cache-control = %q, want immutable", cc)
	}

	// A client-side route falls back to index.html.
	rec = do(t, app, http.MethodGet, "/devices/dev-1", "")
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "REAL APP") {
		t.Error("SPA fallback did not serve index.html")
	}

	// A missing asset is a real 404, not an HTML page.
	rec = do(t, app, http.MethodGet, "/assets/missing.js", "")
	assertError(t, rec, http.StatusNotFound)

	// Path traversal never reaches the filesystem. Go's ServeMux canonicalises
	// the path first (a 301 to the cleaned form), and the asset handler refuses
	// anything that would still escape the root.
	rec = do(t, app, http.MethodGet, "/../etc/passwd", "")
	if rec.Code != http.StatusMovedPermanently && rec.Code != http.StatusTemporaryRedirect &&
		rec.Code != http.StatusNotFound {
		t.Errorf("path traversal returned %d, want a redirect or 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("path traversal leaked /etc/passwd")
	}
	// Encoded traversal is refused outright.
	rec = do(t, app, http.MethodGet, "/%2e%2e/etc/passwd", "")
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMovedPermanently &&
		rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("encoded traversal returned %d, want a redirect or 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("encoded traversal leaked /etc/passwd")
	}

	// Only GET and HEAD are allowed.
	rec = do(t, app, http.MethodPost, "/", "")
	assertError(t, rec, http.StatusMethodNotAllowed)
}

// TestEmbeddedFrontendFallback checks the release build path.
//
// The embedded filesystem ships a README but no index.html until the frontend is
// built, so newStaticAssets("") must fall back to the placeholder rather than
// failing or serving a broken page.
func TestEmbeddedFrontendFallback(t *testing.T) {
	assets := newStaticAssets("")
	if assets.Source() != "placeholder" {
		t.Skipf("a real frontend is embedded in this build (%s)", assets.Source())
	}
	if assets.HasFrontend() {
		t.Error("a directory with no index.html must not report a frontend")
	}

	// And the placeholder must be served, so the service still starts.
	app := newTestApp(t)
	rec := do(t, app, http.MethodGet, "/", "")
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Lanory") {
		t.Errorf("the placeholder page does not mention Lanory: %s", rec.Body.String())
	}
}

// TestBodySizeLimit checks that an oversized body is rejected with 413.
func TestBodySizeLimit(t *testing.T) {
	app := newTestApp(t)

	// 1 MB of padding plus a small name: far beyond maxBodyBytes.
	big := `{"name":"` + strings.Repeat("a", maxBodyBytes+1024) + `"}`
	rec := do(t, app, http.MethodPost, "/api/v1/devices", big)
	assertStatus(t, rec, http.StatusRequestEntityTooLarge)
}

// TestPanicRecovery checks that a panicking handler becomes a JSON 500.
func TestPanicRecovery(t *testing.T) {
	app := newTestApp(t)
	// Wrap a panicking handler in the same middleware chain.
	h := recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	assertError(t, rec, http.StatusInternalServerError)
	_ = app
}

// TestUnknownFieldsRejected checks that a typo in a body is not silently
// ignored.
func TestUnknownFieldsRejected(t *testing.T) {
	app := newTestApp(t)

	rec := do(t, app, http.MethodPost, "/api/v1/devices",
		`{"name":"X","type":"router","device_id":"typo"}`)
	e := assertError(t, rec, http.StatusBadRequest)
	if !strings.Contains(e.Error, "device_id") {
		t.Errorf("error should name the unknown field, got %q", e.Error)
	}
}

// TestInterfaceReadsWorkAroundTheStore checks that interface data is readable
// and writable.
//
// The store's own interface queries omit the SELECT keyword, so every one of
// them fails. internal/api/ifaces.go carries a correct query; this test pins
// that behaviour so a regression is caught immediately.
func TestInterfaceReadsWorkAroundTheStore(t *testing.T) {
	app := newTestApp(t)
	store := app.Store()

	// The seeded router has br-lan, eth1 and eth3.
	ifaces, err := listInterfaces(store, "dev-router")
	if err != nil {
		t.Fatalf("listInterfaces: %v", err)
	}
	names := map[string]bool{}
	for _, i := range ifaces {
		names[i.Name] = true
		if i.DeviceID != "dev-router" {
			t.Errorf("interface %s belongs to %s", i.ID, i.DeviceID)
		}
	}
	for _, want := range []string{"br-lan", "eth1", "eth3"} {
		if !names[want] {
			t.Errorf("interface %s was not read back", want)
		}
	}

	// eth3 has no carrier, so it must come back with carrier=false.
	for _, i := range ifaces {
		if i.Name == "eth3" {
			if i.Carrier {
				t.Error("eth3 must have carrier=false")
			}
			if i.OperState != model.OperNoCarrier {
				t.Errorf("eth3 oper_state = %q, want no-carrier", i.OperState)
			}
		}
	}

	all, err := listAllInterfaces(store)
	if err != nil {
		t.Fatalf("listAllInterfaces: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("listAllInterfaces returned %d, want 3", len(all))
	}

	one, err := getInterface(store, "if-eth1")
	if err != nil {
		t.Fatalf("getInterface: %v", err)
	}
	if one.Name != "eth1" || one.SpeedMbps != 1000 || !one.IsPhysical {
		t.Errorf("getInterface returned %+v", one)
	}

	byName, err := getInterfaceByName(store, "dev-router", "eth1")
	if err != nil {
		t.Fatalf("getInterfaceByName: %v", err)
	}
	if byName.ID != "if-eth1" {
		t.Errorf("getInterfaceByName returned id %q, want if-eth1", byName.ID)
	}

	// A missing interface is a not-found, not a syntax error.
	if _, err := getInterface(store, "if-missing"); !errorsIs(err, database.ErrNotFound) {
		t.Errorf("getInterface(missing) = %v, want ErrNotFound", err)
	}
	if _, err := getInterfaceByName(store, "dev-router", "eth99"); !errorsIs(err, database.ErrNotFound) {
		t.Errorf("getInterfaceByName(missing) = %v, want ErrNotFound", err)
	}

	// A device with no interfaces yields an empty list, not an error.
	none, err := listInterfaces(store, "dev-win")
	if err != nil {
		t.Fatalf("listInterfaces for a device with none: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected no interfaces, got %d", len(none))
	}

	// The overview and monitoring endpoints both rely on this read path.
	rec := do(t, app, http.MethodGet, "/api/v1/overview", "")
	assertStatus(t, rec, http.StatusOK)
	rec = do(t, app, http.MethodGet, "/api/v1/monitoring/status", "")
	assertStatus(t, rec, http.StatusOK)
}

// TestHealthUptimeGrows checks that uptime_seconds is a real measurement.
func TestHealthUptimeGrows(t *testing.T) {
	app := newTestApp(t)
	var first model.HealthResponse
	getJSON(t, app, "/api/v1/health", &first)
	time.Sleep(1100 * time.Millisecond)
	var second model.HealthResponse
	getJSON(t, app, "/api/v1/health", &second)
	if second.UptimeSeconds <= first.UptimeSeconds {
		t.Errorf("uptime did not grow: %d then %d", first.UptimeSeconds, second.UptimeSeconds)
	}
}
