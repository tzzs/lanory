package database

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestOpenAppliesMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mig.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	version, err := db.Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version < 1 {
		t.Fatalf("expected schema version >= 1, got %d", version)
	}

	// Re-opening must be a no-op (idempotent migrations).
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	v2, _ := db2.Version()
	if v2 != version {
		t.Fatalf("version changed on reopen: %d != %d", v2, version)
	}
}

func TestAllTablesExist(t *testing.T) {
	s := newTestStore(t)
	for _, table := range []string{
		"devices", "device_addresses", "device_identities", "device_sources",
		"network_interfaces", "topology_edges", "discovery_evidence",
		"probe_targets", "probe_results", "network_events",
		"topology_layout", "app_settings",
	} {
		var name string
		err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}
