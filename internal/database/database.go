// Package database implements the SQLite persistence layer for Lanory.
//
// Design constraints that shape this package:
//
//   - The database runs on OpenWrt flash storage, so writes are batched and
//     WAL journaling is enabled with a modest autocheckpoint.
//   - Schema changes go through versioned migrations. Never edit an existing
//     migration in place; append a new one.
//   - All statements are parameterised. No SQL is built by string
//     concatenation with user input.
package database

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no cgo
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps *sql.DB with Lanory specific helpers.
type DB struct {
	*sql.DB

	path string
	mu   sync.Mutex // serialises migrations
}

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("database: record not found")

// Open opens (creating if needed) the SQLite database at path and applies all
// pending migrations.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("database: empty path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("database: create dir %s: %w", dir, err)
		}
	}

	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=temp_store(MEMORY)" +
		"&_pragma=cache_size(-4000)" +
		"&_pragma=wal_autocheckpoint(1000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", path, err)
	}
	// Single writer connection keeps write ordering deterministic and avoids
	// SQLITE_BUSY storms on low powered routers.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: ping %s: %w", path, err)
	}

	d := &DB{DB: db, path: path}
	if err := d.Migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: migrate: %w", err)
	}
	return d, nil
}

// Path returns the database file path.
func (d *DB) Path() string { return d.path }

// Migrate applies all pending migrations inside a transaction.
func (d *DB) Migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var applied int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(e.Name(), "%d_", &version); err != nil {
			return fmt.Errorf("parse migration name %q: %w", e.Name(), err)
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", e.Name(), err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, version, e.Name()); err != nil {
			return err
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// Version returns the current schema version.
func (d *DB) Version() (int, error) {
	var v int
	err := d.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// Stats returns lightweight database statistics for the settings page.
type Stats struct {
	Path          string `json:"path"`
	SizeBytes     int64  `json:"size_bytes"`
	PageCount     int64  `json:"page_count"`
	SchemaVersion int    `json:"schema_version"`
	Counts        map[string]int `json:"counts"`
	LastVacuumAt  time.Time `json:"last_vacuum_at,omitempty"`
}

// Stats collects database file size, page count and row counts.
func (d *DB) Stats() (*Stats, error) {
	st := &Stats{Path: d.path, Counts: map[string]int{}}
	if fi, err := os.Stat(d.path); err == nil {
		st.SizeBytes = fi.Size()
	}
	if err := d.QueryRow(`PRAGMA page_count`).Scan(&st.PageCount); err != nil {
		return nil, err
	}
	v, err := d.Version()
	if err != nil {
		return nil, err
	}
	st.SchemaVersion = v

	for _, table := range []string{
		"devices", "device_addresses", "device_identities", "network_interfaces",
		"topology_edges", "discovery_evidence", "probe_targets", "probe_results",
		"network_events", "topology_layout", "app_settings",
	} {
		var n int
		if err := d.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n); err != nil {
			// Table may legitimately not exist yet in older schemas.
			continue
		}
		st.Counts[table] = n
	}
	return st, nil
}

// Vacuum reclaims free pages. Intended for manual invocation only.
func (d *DB) Vacuum() error {
	_, err := d.Exec(`VACUUM`)
	return err
}

// Close closes the database.
func (d *DB) Close() error {
	if d.DB == nil {
		return nil
	}
	// Checkpoint the WAL so the main database file is self contained.
	_, _ = d.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return d.DB.Close()
}

// Now returns the current UTC time used for all persisted timestamps.
func Now() time.Time { return time.Now().UTC() }

// TimeLayout is the canonical timestamp format for the API.
const TimeLayout = time.RFC3339Nano
