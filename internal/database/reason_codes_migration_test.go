package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// openSchemaVersion6 builds a database that has migrations 0001 to 0006
// applied, so that Open has to upgrade it with 0007. It applies the embedded
// migration files directly and records them in schema_migrations, exactly as
// (*DB).Migrate does for a fresh database.
//
// Building the old schema by hand is the only way to test a backfill honestly:
// a store that already ran the migration cannot be migrated again, and
// inserting legacy rows after the fact exercises the fallback path rather
// than the backfill.
func openSchemaVersion6(t *testing.T, path string) *sql.DB {
	t.Helper()

	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	t.Cleanup(func() { raw.Close() })

	if _, err := raw.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, name := range []string{
		"0001_initial.sql",
		"0002_event_foreign_keys.sql",
		"0003_probe_device_nullable.sql",
		"0004_cleanup_zero_mac_phantoms.sql",
		"0005_cleanup_zero_mac_addresses.sql",
		"0006_repair_primary_addresses.sql",
	} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			t.Fatalf("parse migration name %q: %v", name, err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, version, name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
	return raw
}

// detection is the suffix the monitoring scheduler appends to a transition
// reason: it names the probe method that produced the verdict.
const detection = " (detected by icmp probe from lanory)"

// legacyReasonFixture describes one reason template: the text that is stored
// in each table and the code and parameters the backfill has to recover.
//
// The three tables carry different text. A device's state_reason and an
// event's message both carry the detection suffix, because the scheduler
// builds one string and uses it for both. A probe row keeps the bare reason.
type legacyReasonFixture struct {
	name string

	deviceReason string // devices.state_reason
	deviceCode   model.ReasonCode
	deviceParams map[string]string

	probeReason string // probe_targets.state_reason
	probeCode   model.ReasonCode
	probeParams map[string]string

	eventMessage string // network_events.message
	eventCode    model.ReasonCode
	eventParams  map[string]string
}

// legacyReasonFixtures is the set of reason texts the system writes.
//
// Every template appears with a single digit and a multi digit parameter
// somewhere in the set, because the extraction must not depend on the width of
// a number.
var legacyReasonFixtures = []legacyReasonFixture{
	{
		name:         "reachable",
		deviceReason: "reachable from lanory via icmp" + detection,
		deviceCode:   model.ReasonProbeReachable,
		deviceParams: map[string]string{"source": "lanory", "protocol": "icmp"},
		probeReason:  "reachable from lanory via icmp",
		probeCode:    model.ReasonProbeReachable,
		probeParams:  map[string]string{"source": "lanory", "protocol": "icmp"},
		eventMessage: "reachable from lanory via icmp" + detection,
		eventCode:    model.ReasonProbeReachable,
		eventParams:  map[string]string{"source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "unreachable consecutive",
		deviceReason: "unreachable for 12 consecutive probes from lanory via icmp with no recent success" + detection,
		deviceCode:   model.ReasonProbeUnreachableConsecutive,
		deviceParams: map[string]string{"failures": "12", "source": "lanory", "protocol": "icmp"},
		probeReason:  "unreachable for 12 consecutive probes from lanory via icmp with no recent success",
		probeCode:    model.ReasonProbeUnreachableConsecutive,
		probeParams:  map[string]string{"failures": "12", "source": "lanory", "protocol": "icmp"},
		eventMessage: "unreachable for 12 consecutive probes from lanory via icmp with no recent success" + detection,
		eventCode:    model.ReasonProbeUnreachableConsecutive,
		eventParams:  map[string]string{"failures": "12", "source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "unreachable source down",
		deviceReason: "unreachable for 3 consecutive probes from lanory via icmp, but the probe source itself lost connectivity: this says nothing about the device" + detection,
		deviceCode:   model.ReasonProbeUnreachableSourceDown,
		deviceParams: map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp"},
		probeReason:  "unreachable for 3 consecutive probes from lanory via icmp, but the probe source itself lost connectivity: this says nothing about the device",
		probeCode:    model.ReasonProbeUnreachableSourceDown,
		probeParams:  map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp"},
		eventMessage: "unreachable for 3 consecutive probes from lanory via icmp, but the probe source itself lost connectivity: this says nothing about the device" + detection,
		eventCode:    model.ReasonProbeUnreachableSourceDown,
		eventParams:  map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "unreachable still present",
		deviceReason: "unreachable for 3 consecutive probes from lanory via icmp, but the neighbour table still lists 192.168.100.116: the device is on the segment and does not answer this probe method" + detection,
		deviceCode:   model.ReasonProbeUnreachableStillPresent,
		deviceParams: map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp", "address": "192.168.100.116"},
		probeReason:  "unreachable for 3 consecutive probes from lanory via icmp, but the neighbour table still lists 192.168.100.116: the device is on the segment and does not answer this probe method",
		probeCode:    model.ReasonProbeUnreachableStillPresent,
		probeParams:  map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp", "address": "192.168.100.116"},
		eventMessage: "unreachable for 3 consecutive probes from lanory via icmp, but the neighbour table still lists 192.168.100.116: the device is on the segment and does not answer this probe method" + detection,
		eventCode:    model.ReasonProbeUnreachableStillPresent,
		eventParams:  map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp", "address": "192.168.100.116"},
	},
	{
		name:         "unreachable power cycling",
		deviceReason: "unreachable for 3 consecutive probes from lanory via icmp, but the device resumed after an earlier gap of 5 failures: power saving, not absence" + detection,
		deviceCode:   model.ReasonProbeUnreachablePowerCycling,
		deviceParams: map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp", "gap": "5"},
		probeReason:  "unreachable for 3 consecutive probes from lanory via icmp, but the device resumed after an earlier gap of 5 failures: power saving, not absence",
		probeCode:    model.ReasonProbeUnreachablePowerCycling,
		probeParams:  map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp", "gap": "5"},
		eventMessage: "unreachable for 3 consecutive probes from lanory via icmp, but the device resumed after an earlier gap of 5 failures: power saving, not absence" + detection,
		eventCode:    model.ReasonProbeUnreachablePowerCycling,
		eventParams:  map[string]string{"failures": "3", "source": "lanory", "protocol": "icmp", "gap": "5"},
	},
	{
		name:         "unreachable deeper sleep",
		deviceReason: "unreachable for 6 consecutive probes from lanory via icmp; the device was already sleeping and answered 12 minutes ago, so this is a deeper sleep rather than absence" + detection,
		deviceCode:   model.ReasonProbeUnreachableDeeperSleep,
		deviceParams: map[string]string{"failures": "6", "source": "lanory", "protocol": "icmp", "last_success": "12 minutes ago"},
		probeReason:  "unreachable for 6 consecutive probes from lanory via icmp; the device was already sleeping and answered 12 minutes ago, so this is a deeper sleep rather than absence",
		probeCode:    model.ReasonProbeUnreachableDeeperSleep,
		probeParams:  map[string]string{"failures": "6", "source": "lanory", "protocol": "icmp", "last_success": "12 minutes ago"},
		eventMessage: "unreachable for 6 consecutive probes from lanory via icmp; the device was already sleeping and answered 12 minutes ago, so this is a deeper sleep rather than absence" + detection,
		eventCode:    model.ReasonProbeUnreachableDeeperSleep,
		eventParams:  map[string]string{"failures": "6", "source": "lanory", "protocol": "icmp", "last_success": "12 minutes ago"},
	},
	{
		name:         "intermittent",
		deviceReason: "intermittent reachability from lanory via icmp: 9 of the last 12 probes failed" + detection,
		deviceCode:   model.ReasonProbeIntermittent,
		deviceParams: map[string]string{"failed": "9", "total": "12", "source": "lanory", "protocol": "icmp"},
		probeReason:  "intermittent reachability from lanory via icmp: 9 of the last 12 probes failed",
		probeCode:    model.ReasonProbeIntermittent,
		probeParams:  map[string]string{"failed": "9", "total": "12", "source": "lanory", "protocol": "icmp"},
		eventMessage: "intermittent reachability from lanory via icmp: 9 of the last 12 probes failed" + detection,
		eventCode:    model.ReasonProbeIntermittent,
		eventParams:  map[string]string{"failed": "9", "total": "12", "source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "confirming recovery",
		deviceReason: "confirming recovery from lanory via icmp: 1 of 2 required successes" + detection,
		deviceCode:   model.ReasonProbeConfirmingRecovery,
		deviceParams: map[string]string{"successes": "1", "required": "2", "source": "lanory", "protocol": "icmp"},
		probeReason:  "confirming recovery from lanory via icmp: 1 of 2 required successes",
		probeCode:    model.ReasonProbeConfirmingRecovery,
		probeParams:  map[string]string{"successes": "1", "required": "2", "source": "lanory", "protocol": "icmp"},
		eventMessage: "confirming recovery from lanory via icmp: 1 of 2 required successes" + detection,
		eventCode:    model.ReasonProbeConfirmingRecovery,
		eventParams:  map[string]string{"successes": "1", "required": "2", "source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "below threshold",
		deviceReason: "2 consecutive failure(s) below the threshold of 3 from lanory via icmp: not enough evidence to change state" + detection,
		deviceCode:   model.ReasonProbeBelowThreshold,
		deviceParams: map[string]string{"failures": "2", "threshold": "3", "source": "lanory", "protocol": "icmp"},
		probeReason:  "2 consecutive failure(s) below the threshold of 3 from lanory via icmp: not enough evidence to change state",
		probeCode:    model.ReasonProbeBelowThreshold,
		probeParams:  map[string]string{"failures": "2", "threshold": "3", "source": "lanory", "protocol": "icmp"},
		eventMessage: "2 consecutive failure(s) below the threshold of 3 from lanory via icmp: not enough evidence to change state" + detection,
		eventCode:    model.ReasonProbeBelowThreshold,
		eventParams:  map[string]string{"failures": "2", "threshold": "3", "source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "never succeeded",
		deviceReason: "no successful probe yet from lanory via icmp (3 failure(s), threshold 3)" + detection,
		deviceCode:   model.ReasonProbeNeverSucceeded,
		deviceParams: map[string]string{"failures": "3", "threshold": "3", "source": "lanory", "protocol": "icmp"},
		probeReason:  "no successful probe yet from lanory via icmp (3 failure(s), threshold 3)",
		probeCode:    model.ReasonProbeNeverSucceeded,
		probeParams:  map[string]string{"failures": "3", "threshold": "3", "source": "lanory", "protocol": "icmp"},
		eventMessage: "no successful probe yet from lanory via icmp (3 failure(s), threshold 3)" + detection,
		eventCode:    model.ReasonProbeNeverSucceeded,
		eventParams:  map[string]string{"failures": "3", "threshold": "3", "source": "lanory", "protocol": "icmp"},
	},
	{
		name:         "never probed",
		deviceReason: "never probed from lanory: no evidence either way",
		deviceCode:   model.ReasonProbeNeverProbed,
		deviceParams: map[string]string{"source": "lanory"},
	},
	{
		name:         "device awaiting first check",
		deviceReason: "waiting for first reachability check",
		deviceCode:   model.ReasonAwaitingFirstCheck,
		deviceParams: map[string]string{},
	},
	{
		name:         "device not yet probed",
		deviceReason: "not yet probed",
		deviceCode:   model.ReasonNotYetProbed,
		deviceParams: map[string]string{},
	},
	{
		name:        "probe awaiting first",
		probeReason: "awaiting first probe",
		probeCode:   model.ReasonAwaitingFirstProbe,
		probeParams: map[string]string{},
	},
}

// TestReasonCodeMigrationBackfillsLegacyTemplates asserts that migration 0007
// recovers the code and the parameters of every reason template the system
// writes, from the text alone.
//
// The database is built at the pre-0007 schema, filled with the legacy text,
// and then upgraded through the real migration runner, so this exercises the
// backfill rather than the fallback.
func TestReasonCodeMigrationBackfillsLegacyTemplates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	raw := openSchemaVersion6(t, dbPath)

	for i, fixture := range legacyReasonFixtures {
		id := fmt.Sprintf("dev-legacy-%d", i)

		// The device always exists: a probe belongs to one, and the probe
		// backfill falls back to the device's code.
		deviceReason := fixture.deviceReason
		if deviceReason == "" {
			deviceReason = "manually added, never probed"
		}
		if _, err := raw.Exec(
			`INSERT INTO devices (id, name, state, state_reason) VALUES (?, ?, 'offline', ?)`,
			id, "Legacy "+fixture.name, deviceReason,
		); err != nil {
			t.Fatalf("insert legacy device %s: %v", fixture.name, err)
		}

		// The probe row belongs to the device, so the fallback that copies the
		// device's code can be exercised too.
		probeID := fmt.Sprintf("probe-legacy-%d", i)
		if fixture.probeReason != "" {
			if _, err := raw.Exec(
				`INSERT INTO probe_targets (id, name, target, device_id, state, state_reason)
				 VALUES (?, ?, '192.0.2.10', ?, 'offline', ?)`,
				probeID, "Legacy probe "+fixture.name, id, fixture.probeReason,
			); err != nil {
				t.Fatalf("insert legacy probe %s: %v", fixture.name, err)
			}
		}

		if fixture.eventMessage != "" {
			if _, err := raw.Exec(
				`INSERT INTO network_events (id, event_type, device_id, message) VALUES (?, 'device.updated', ?, ?)`,
				fmt.Sprintf("evt-legacy-%d", i), id, fixture.eventMessage,
			); err != nil {
				t.Fatalf("insert legacy event %s: %v", fixture.name, err)
			}
		}
	}

	// A reason the backfill does not know: it must keep its text and stay
	// uncoded, so the frontend falls back to the original English.
	const unknownReason = "the operator paused the probe from the web interface"
	if _, err := raw.Exec(
		`INSERT INTO devices (id, name, state, state_reason) VALUES ('dev-legacy-unknown', 'Unknown reason', 'unknown', ?)`,
		unknownReason,
	); err != nil {
		t.Fatalf("insert legacy device with unknown reason: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	// Open applies migration 0007, which backfills the rows.
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open and migrate legacy database: %v", err)
	}
	defer db.Close()

	version, err := db.Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version < 7 {
		t.Fatalf("schema version = %d, want the migration to have run", version)
	}

	s := NewStore(db)
	for i, fixture := range legacyReasonFixtures {
		id := fmt.Sprintf("dev-legacy-%d", i)

		if fixture.deviceReason != "" {
			dev, err := s.GetDevice(id)
			if err != nil {
				t.Fatalf("get legacy device %s: %v", fixture.name, err)
			}
			assertReason(t, "device "+fixture.name, dev.StateReasonCode, fixture.deviceCode, fixture.deviceParams)
			// The English text is never replaced: it is what the logs and the
			// fallback render.
			if dev.StateReason != fixture.deviceReason {
				t.Fatalf("device %s reason text = %q, want it unchanged", fixture.name, dev.StateReason)
			}
		}

		if fixture.probeReason != "" {
			probe, err := s.GetProbe(fmt.Sprintf("probe-legacy-%d", i))
			if err != nil {
				t.Fatalf("get legacy probe %s: %v", fixture.name, err)
			}
			assertReason(t, "probe "+fixture.name, probe.StateReasonCode, fixture.probeCode, fixture.probeParams)
		}

		if fixture.eventMessage == "" {
			continue
		}
		events, _, err := s.ListEvents(EventFilter{DeviceID: id})
		if err != nil {
			t.Fatalf("list legacy events %s: %v", fixture.name, err)
		}
		if len(events) != 1 {
			t.Fatalf("device %s has %d events, want 1", fixture.name, len(events))
		}
		assertReason(t, "event "+fixture.name, events[0].ReasonCode, fixture.eventCode, fixture.eventParams)
		if events[0].Message != fixture.eventMessage {
			t.Fatalf("event %s message = %q, want it unchanged", fixture.name, events[0].Message)
		}
	}

	// The unknown reason keeps no code, so the frontend renders the original
	// text rather than a wrong translation.
	unknown, err := s.GetDevice("dev-legacy-unknown")
	if err != nil {
		t.Fatalf("get device with unknown reason: %v", err)
	}
	if unknown.StateReasonCode != nil {
		t.Fatalf("unknown reason got code %#v, want none", unknown.StateReasonCode)
	}
	if unknown.StateReason != unknownReason {
		t.Fatalf("unknown reason text = %q, want it unchanged", unknown.StateReason)
	}
}

// TestReasonCodeMigrationBackfillsLinkEvents asserts that the link observer's
// event messages are recognised too.
//
// The parameters matter: a backfilled row has to carry the same interface
// name and the same old and new values as a row the running code writes, or
// the two would render differently in the UI.
func TestReasonCodeMigrationBackfillsLinkEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-links.db")
	raw := openSchemaVersion6(t, dbPath)

	links := []struct {
		message string
		code    model.ReasonCode
		params  map[string]string
	}{
		{
			message: "eth2 carrier up -> down",
			code:    model.ReasonLinkCarrierChanged,
			params:  map[string]string{"interface": "eth2", "old": "up", "new": "down"},
		},
		{
			message: "eth2 carrier down -> up",
			code:    model.ReasonLinkCarrierChanged,
			params:  map[string]string{"interface": "eth2", "old": "down", "new": "up"},
		},
		{
			message: "br-lan negotiated speed 1000 Mbps -> 10 Mbps",
			code:    model.ReasonLinkSpeedChanged,
			params:  map[string]string{"interface": "br-lan", "old_mbps": "1000", "new_mbps": "10"},
		},
	}
	for i, link := range links {
		if _, err := raw.Exec(
			`INSERT INTO network_events (id, event_type, message) VALUES (?, 'link.down', ?)`,
			fmt.Sprintf("evt-link-%d", i), link.message,
		); err != nil {
			t.Fatalf("insert legacy link event: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open and migrate legacy database: %v", err)
	}
	defer db.Close()

	events, _, err := NewStore(db).ListEvents(EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != len(links) {
		t.Fatalf("got %d events, want %d", len(events), len(links))
	}
	for _, event := range events {
		want := links[0]
		for _, link := range links {
			if link.message == event.Message {
				want = link
			}
		}
		assertReason(t, "link event "+event.Message, event.ReasonCode, want.code, want.params)
	}
}

// assertReason checks a decoded reason against the expected code and
// parameters.
func assertReason(t *testing.T, what string, got *model.Reason, want model.ReasonCode, params map[string]string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s has no reason code, want %q", what, want)
	}
	if got.Code != want {
		t.Fatalf("%s code = %q, want %q", what, got.Code, want)
	}
	for key, value := range params {
		if got.Params[key] != value {
			t.Fatalf("%s param %q = %q, want %q (all params: %#v)", what, key, got.Params[key], value, got.Params)
		}
	}
}
