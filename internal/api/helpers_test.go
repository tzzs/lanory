package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanory/lanory/internal/config"
	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/events"
	"github.com/lanory/lanory/internal/model"
)

// Test helpers shared by the api package tests.

// newTestApp builds an App against a fresh temporary database with a fully
// populated sample network.
//
// The fixture data mirrors fixtures/easepi-r1 so the assertions describe the
// reference network instead of tautologies.
func newTestApp(t *testing.T) *App {
	t.Helper()
	return newTestAppWithConfig(t, testConfig(t))
}

// testConfig returns a configuration tuned for tests: auth off by default so
// route tests are not fighting the token, a private subnet authorised for SSRF,
// and a temp database path.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "lanory-test.db")
	cfg.Auth.Enabled = false
	cfg.Discovery.Subnets = []string{"192.0.2.0/24", "192.168.100.0/24"}
	cfg.Demo.Enabled = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate config: %v", err)
	}
	return cfg
}

// newTestAppWithConfig builds an App with an explicit configuration.
func newTestAppWithConfig(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	return newTestAppWithOptions(t, Options{Config: cfg})
}

// newTestAppWithOptions builds an App with explicit options and seeds the
// reference network into its store.
func newTestAppWithOptions(t *testing.T, opts Options) *App {
	t.Helper()
	if opts.Config == nil {
		opts.Config = testConfig(t)
	}

	db, err := database.Open(opts.Config.Database.Path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := database.NewStore(db)
	bus := events.New(256)

	opts.Store = store
	opts.Bus = bus
	app, err := New(opts)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	seedTestNetwork(t, store, bus)
	return app
}

// seedTestNetwork populates the reference network from fixtures/easepi-r1.
//
// The data is derived from the sanitized fixture: the gateway, the br-lan
// bridge, its member ports and the eight devices the discovery engine is
// expected to find. Only what the fixture asserts is asserted here.
func seedTestNetwork(t *testing.T, store *database.Store, bus *events.Bus) {
	t.Helper()
	now := database.Now()

	// The router itself: gateway + self, with br-lan 192.168.100.1/24.
	router := &model.Device{
		ID: "dev-router", Name: "Router", Type: model.DeviceTypeRouter,
		State: model.StateOnline, StateReason: "self",
		PrimaryIPv4: "192.168.100.1", PrimaryMAC: "02:00:00:00:00:10",
		IsGateway: true, IsSelf: true,
		FirstSeen: now, LastSeen: now, CreatedAt: now, UpdatedAt: now, LastStateChange: now,
		DiscoverySources: []model.DiscoverySource{model.SourceOpenWRT},
	}
	if err := store.UpsertDevice(router); err != nil {
		t.Fatalf("seed router: %v", err)
	}
	_ = upsertInterface(store, &model.NetworkInterface{
		ID: "if-br-lan", DeviceID: router.ID, Name: "br-lan",
		Type: model.InterfaceTypeBridge, Role: model.InterfaceRoleLAN,
		AdminState: model.AdminUp, OperState: model.OperUp, Carrier: true,
		SpeedMbps: 1000, MTU: 1500, IsPhysical: false, LastSeen: now,
	})
	_ = upsertInterface(store, &model.NetworkInterface{
		ID: "if-eth1", DeviceID: router.ID, Name: "eth1",
		Type: model.InterfaceTypeEthernet, Role: model.InterfaceRoleLAN,
		AdminState: model.AdminUp, OperState: model.OperUp, Carrier: true,
		SpeedMbps: 1000, MTU: 1500, IsPhysical: true, LastSeen: now,
	})
	// eth3 has no carrier: it must surface as an anomaly on the overview.
	_ = upsertInterface(store, &model.NetworkInterface{
		ID: "if-eth3", DeviceID: router.ID, Name: "eth3",
		Type: model.InterfaceTypeEthernet, Role: model.InterfaceRoleLAN,
		AdminState: model.AdminUp, OperState: model.OperNoCarrier, Carrier: false,
		MTU: 1500, IsPhysical: true, LastSeen: now,
	})

	type device struct {
		id       string
		name     string
		hostname string
		mac      string
		ipv4     string
		typ      model.DeviceType
		state    model.DeviceState
	}
	devices := []device{
		{"dev-win", "Windows PC", "client-desktop", "02:11:22:33:44:b7", "192.0.2.116", model.DeviceTypeDesktop, model.StateOnline},
		{"dev-iphone", "iPhone", "iPhone", "06:77:88:99:aa:d2", "192.0.2.151", model.DeviceTypeSmartphone, model.StateOnline},
		{"dev-mac", "Mac Studio", "client-mac", "07:88:99:aa:bb:5e", "192.0.2.222", model.DeviceTypeDesktop, model.StateOffline},
		{"dev-watch", "Watch", "Watch", "04:66:77:88:99:e3", "192.0.2.197", model.DeviceTypeSmartwatch, model.StateUnknown},
		{"dev-lamp", "Desk Lamp", "smart-lamp", "05:22:33:44:55:2d", "192.0.2.159", model.DeviceTypeIoT, model.StateOnline},
		{"dev-esp32", "ESP32 Sensor", "iot-sensor", "cc:11:22:33:44:20", "192.0.2.231", model.DeviceTypeIoT, model.StateOnline},
		// The unknown-identity device from the fixture: IPv6-only, no hostname.
		// It must still become exactly one device.
		{"dev-ipv6only", "Unknown b4:11:22:33:44:13", "", "b4:11:22:33:44:13", "", model.DeviceTypeUnknown, model.StateUnknown},
		{"dev-modem", "Optical Modem", "", "", "192.168.1.2", model.DeviceTypeModemONT, model.StateUnknown},
	}
	for _, d := range devices {
		dev := &model.Device{
			ID: d.id, Name: d.name, Hostname: d.hostname,
			Type: d.typ, State: d.state,
			StateReason: "seeded from fixtures/easepi-r1",
			PrimaryIPv4: d.ipv4, PrimaryMAC: d.mac,
			FirstSeen: now, LastSeen: now, CreatedAt: now, UpdatedAt: now, LastStateChange: now,
			DiscoverySources: []model.DiscoverySource{model.SourceBridgeFDB, model.SourceDHCP},
		}
		if d.id == "dev-modem" {
			dev.IsManual = true
			dev.TypeSource = model.SourceManual
			dev.StateReason = "not yet probed"
		}
		if err := store.UpsertDevice(dev); err != nil {
			t.Fatalf("seed device %s: %v", d.id, err)
		}
		if d.mac != "" {
			_ = store.UpsertIdentity(&model.DeviceIdentity{
				DeviceID: d.id, Type: model.IdentityMAC, Value: normalizeMACForTest(d.mac),
				Source: model.SourceBridgeFDB, Confidence: 0.7,
				FirstSeen: now, LastSeen: now,
			})
		}
		if d.hostname != "" {
			_ = store.UpsertIdentity(&model.DeviceIdentity{
				DeviceID: d.id, Type: model.IdentityHostname, Value: d.hostname,
				Source: model.SourceDHCP, Confidence: 0.4,
				FirstSeen: now, LastSeen: now,
			})
		}
		if d.ipv4 != "" {
			_ = store.UpsertAddress(&model.DeviceAddress{
				DeviceID: d.id, Address: d.ipv4, Family: model.AddressFamilyIPv4,
				Source: model.SourceDHCP, PrefixLength: 24, IsPrimary: true,
				FirstSeen: now, LastSeen: now,
			})
		}
		if d.id == "dev-ipv6only" {
			// The fixture device has a link-local IPv6 address and no IPv4.
			_ = store.UpsertAddress(&model.DeviceAddress{
				DeviceID: d.id, Address: "fe80::1a2b:3c4d:5e6f:7012", Family: model.AddressFamilyIPv6,
				Source: model.SourceNDP, PrefixLength: 64, IsPrimary: true,
				FirstSeen: now, LastSeen: now,
			})
		}
		_ = store.AddDeviceSource(d.id, model.SourceBridgeFDB)
		_ = store.AddDeviceSource(d.id, model.SourceDHCP)
	}

	// The Windows PC hangs off eth1 in the store; the fixture asserts it is
	// observed on eth2, which the topology engine is responsible for deriving.
	winEdge := &model.TopologyEdge{
		SourceID: router.ID, TargetID: "dev-win", SourceInterfaceID: "if-eth1",
		EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
		Confidence: 0.7, Status: model.EdgeStatusLinkUp,
		EvidenceCount: 1, LastObserved: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertEdge(winEdge); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	// A locked edge: deleting it must be a 409.
	if err := store.UpsertEdge(&model.TopologyEdge{
		SourceID: router.ID, TargetID: "dev-lamp", SourceInterfaceID: "if-eth1",
		EdgeType: model.EdgeTypeObservedMACPort, DiscoverySource: model.SourceBridgeFDB,
		Confidence: 0.7, Locked: true, Status: model.EdgeStatusLinkUp,
		LastObserved: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed locked edge: %v", err)
	}

	// Evidence backing one edge.
	_ = store.SaveEvidence(&model.DiscoveryEvidence{
		Collector: model.CollectorBridgeFDB, SubjectType: "edge", SubjectID: "edge-seeded",
		EvidenceType: "fdb_entry", RawData: `{"mac":"b082e2613eb7","port":"eth2"}`,
		ObservedAt: now,
	})

	// A probe that has run, so monitoring status has real data.
	probe := &model.ProbeTarget{
		Name: "Windows PC", Kind: model.ProbeKindDevice, Target: "192.0.2.116",
		Protocol: model.ProbeICMP, Interval: 60, Timeout: 3, Enabled: true,
		DeviceID: "dev-win", FailureThreshold: 3, RecoverConfirm: 1,
		State: model.StateOnline, StateReason: "reachable",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateProbe(probe); err != nil {
		t.Fatalf("seed probe: %v", err)
	}
	_ = store.SaveProbeResult(&model.ProbeResult{
		ProbeID: probe.ID, Timestamp: now, Success: true, LatencyMS: 4,
		Status: model.ProbeRunSuccess,
	})
	_ = store.SaveProbeResult(&model.ProbeResult{
		ProbeID: probe.ID, Timestamp: now.Add(-time.Minute), Success: false, LatencyMS: 0,
		Status: model.ProbeRunTimeout, ErrorType: "timeout",
	})

	// Events, newest first.
	//
	// Every seeded event carries a real edge ID: network_events.edge_id is
	// NOT NULL with a foreign key to topology_edges(id), so an event that is
	// not about a specific edge cannot be stored at all. See the note in the
	// package documentation about that schema constraint.
	for _, e := range []struct {
		id   string
		typ  model.EventType
		dev  string
		msg  string
		when time.Time
	}{
		{"evt-3", model.EventLinkUp, "dev-win", "eth1 link came up", now},
		{"evt-2", model.EventLinkDown, "dev-win", "eth1 link went down", now.Add(-time.Minute)},
		{"evt-1", model.EventSpeedChanged, "dev-win", "eth1 negotiated 1000 Mbps", now.Add(-2 * time.Minute)},
	} {
		if err := store.SaveEvent(&model.NetworkEvent{
			ID: e.id, EventType: e.typ, DeviceID: e.dev, EdgeID: winEdge.ID,
			Message: e.msg, Timestamp: e.when,
		}); err != nil {
			t.Fatalf("seed event %s: %v", e.id, err)
		}
	}

	bus.PublishDeviceOnline(router, "seeded")
}

// normalizeMACForTest canonicalises a MAC the way the database layer does.
func normalizeMACForTest(mac string) string {
	out := make([]byte, 0, 12)
	for i := 0; i < len(mac); i++ {
		c := mac[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			if c >= 'A' && c <= 'F' {
				c += 'a' - 'A'
			}
			out = append(out, c)
		}
	}
	return string(out)
}

// do issues a request against the app and returns the response.
func do(t *testing.T, app *App, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	// Every mutating request carries the CSRF header, as the frontend does.
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set(csrfHeader, csrfValue)
	}
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, r)
	return rec
}

// getJSON issues a GET and decodes the JSON body into dst.
func getJSON(t *testing.T, app *App, path string, dst any) *httptest.ResponseRecorder {
	t.Helper()
	rec := do(t, app, http.MethodGet, path, "")
	if dst != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
			t.Fatalf("decode %s: %v\nbody: %s", path, err, rec.Body.String())
		}
	}
	return rec
}

// apiError is the decoded error envelope.
type apiError struct {
	Error   string            `json:"error"`
	Code    string            `json:"code"`
	Details map[string]string `json:"details"`
}

// assertStatus checks the status code and fails with the body for context.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d\nbody: %s", rec.Code, want, rec.Body.String())
	}
}

// assertError checks that the response is a well formed error envelope.
func assertError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) apiError {
	t.Helper()
	assertStatus(t, rec, wantStatus)
	var e apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body is not JSON: %v\nbody: %s", err, rec.Body.String())
	}
	if e.Error == "" {
		t.Fatalf("error envelope has no message: %s", rec.Body.String())
	}
	if e.Code == "" {
		t.Fatalf("error envelope has no code: %s", rec.Body.String())
	}
	return e
}

// openTestDB opens a database without registering cleanup, for cases that need
// the handle directly.
func openTestDB(t *testing.T, path string) *database.DB {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newStore is a convenience wrapper.
func newStore(db *database.DB) *database.Store { return database.NewStore(db) }
