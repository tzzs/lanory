// Tests for probe targets and the probe_targets.device_id foreign key.

package database

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lanory/lanory/internal/model"
)

// openSchemaVersion2 builds a database that has only migrations 0001 and 0002
// applied, so that Open has to upgrade it with 0003. It applies the embedded
// migration files directly and records them in schema_migrations, exactly as
// (*DB).Migrate does for a fresh database.
func openSchemaVersion2(t *testing.T, path string) *sql.DB {
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
	for _, name := range []string{"0001_initial.sql", "0002_event_foreign_keys.sql"} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			t.Fatalf("parse migration name %s: %v", name, err)
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

// TestProbeWithoutDevice is the normal case for a monitoring tool: a gateway,
// upstream or Internet reachability check that is not a device probe. It must
// be storable, and the store must read it back as an empty DeviceID.
func TestProbeWithoutDevice(t *testing.T) {
	s := newTestStore(t)

	p := &model.ProbeTarget{
		Name:     "Internet reachability",
		Kind:     model.ProbeKindNetwork,
		Target:   "203.0.113.1",
		Protocol: model.ProbeICMP,
		Interval: 60,
		Timeout:  3,
		Enabled:  true,
	}
	if err := s.CreateProbe(p); err != nil {
		t.Fatalf("create probe without a device: %v", err)
	}

	got, err := s.GetProbe(p.ID)
	if err != nil {
		t.Fatalf("get probe: %v", err)
	}
	if got.DeviceID != "" {
		t.Fatalf("device id = %q, want empty", got.DeviceID)
	}

	// An unattached probe must be stored as NULL. The old schema forced ''
	// through NOT NULL DEFAULT '', which is what broke the foreign key.
	var nulls int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM probe_targets WHERE id = ? AND device_id IS NULL`, p.ID,
	).Scan(&nulls); err != nil {
		t.Fatalf("check stored device id: %v", err)
	}
	if nulls != 1 {
		t.Errorf("device_id of an unattached probe is not NULL in the database")
	}
}

// TestDeleteDeviceWithProbe covers the other half of the contradiction: the
// foreign key asks for ON DELETE SET NULL, but NOT NULL rejected the NULL and
// the device delete failed. The probe must survive with no device, because
// probe history outlives the device it once described.
func TestDeleteDeviceWithProbe(t *testing.T) {
	s := newTestStore(t)

	dev := &model.Device{ID: "dev-probe-owner", Name: "Probe owner"}
	if err := s.UpsertDevice(dev); err != nil {
		t.Fatalf("upsert device: %v", err)
	}
	p := &model.ProbeTarget{
		Name:     "Owner reachable",
		Kind:     model.ProbeKindDevice,
		Target:   "192.0.2.10",
		Protocol: model.ProbeICMP,
		Interval: 60,
		Timeout:  3,
		Enabled:  true,
		DeviceID: dev.ID,
	}
	if err := s.CreateProbe(p); err != nil {
		t.Fatalf("create probe: %v", err)
	}
	attached, err := s.GetProbe(p.ID)
	if err != nil {
		t.Fatalf("get attached probe: %v", err)
	}
	if attached.DeviceID != dev.ID {
		t.Fatalf("attached probe device id = %q, want %q", attached.DeviceID, dev.ID)
	}
	if err := s.SaveProbeResult(&model.ProbeResult{
		ID: "result-owner", ProbeID: p.ID, Success: true, Status: model.ProbeRunSuccess,
	}); err != nil {
		t.Fatalf("save probe result: %v", err)
	}

	if err := s.DeleteDevice(dev.ID); err != nil {
		t.Fatalf("delete device with an attached probe: %v", err)
	}

	survived, err := s.GetProbe(p.ID)
	if err != nil {
		t.Fatalf("probe did not survive the device deletion: %v", err)
	}
	if survived.DeviceID != "" {
		t.Fatalf("probe device id after device deletion = %q, want empty", survived.DeviceID)
	}
	if _, err := s.LatestProbeResult(p.ID); err != nil {
		t.Fatalf("probe result did not survive the device deletion: %v", err)
	}
}

// TestMigrationPreservesProbes upgrades a database that already has 0001 and
// 0002 applied. The rows of probe_targets, and the probe_results that reference
// them, must survive the rebuild with their content intact.
func TestMigrationPreservesProbes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	raw := openSchemaVersion2(t, dbPath)

	if _, err := raw.Exec(`INSERT INTO devices (id, name) VALUES ('dev-legacy', 'Legacy device')`); err != nil {
		t.Fatalf("insert legacy device: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO probe_targets (id, name, target, device_id)
		VALUES ('probe-legacy', 'Legacy probe', '192.0.2.20', 'dev-legacy')`); err != nil {
		t.Fatalf("insert legacy probe: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO probe_results (id, probe_id, success, status)
		VALUES ('result-legacy', 'probe-legacy', 1, 'success')`); err != nil {
		t.Fatalf("insert legacy probe result: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open and migrate legacy database: %v", err)
	}

	version, err := db.Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version < 3 {
		t.Errorf("schema version = %d, want at least 3", version)
	}

	s := NewStore(db)
	probe, err := s.GetProbe("probe-legacy")
	if err != nil {
		t.Fatalf("probe did not survive the migration: %v", err)
	}
	if probe.DeviceID != "dev-legacy" {
		t.Errorf("probe device id after migration = %q, want %q", probe.DeviceID, "dev-legacy")
	}

	// probe_results.probe_id cascades from probe_targets, so dropping the old
	// parent table deletes the child rows unless the migration preserves them.
	result, err := s.LatestProbeResult("probe-legacy")
	if err != nil {
		t.Fatalf("probe result did not survive the migration: %v", err)
	}
	if result.ID != "result-legacy" || !result.Success {
		t.Errorf("probe result after migration = %+v, want result-legacy/success", result)
	}

	// After the upgrade the probe may be detached, which the old schema refused.
	if _, err := s.DB().Exec(`UPDATE probe_targets SET device_id = NULL WHERE id = 'probe-legacy'`); err != nil {
		t.Fatalf("detach probe after migration: %v", err)
	}
	detached, err := s.GetProbe("probe-legacy")
	if err != nil {
		t.Fatalf("get detached probe: %v", err)
	}
	if detached.DeviceID != "" {
		t.Errorf("detached probe device id = %q, want empty", detached.DeviceID)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	// Re-opening must not run the migration a second time or change anything.
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer db2.Close()
	v2, err := db2.Version()
	if err != nil {
		t.Fatalf("version after reopen: %v", err)
	}
	if v2 != version {
		t.Errorf("schema version changed on reopen: %d != %d", v2, version)
	}
	reopened, err := NewStore(db2).GetProbe("probe-legacy")
	if err != nil {
		t.Fatalf("get probe after reopen: %v", err)
	}
	if reopened.DeviceID != "" {
		t.Errorf("probe device id after reopen = %q, want empty", reopened.DeviceID)
	}
}
