-- 0003_probe_device_nullable.sql
--
-- The same contradiction that 0002 fixed in network_events is still present in
-- probe_targets:
--
--     device_id TEXT NOT NULL DEFAULT ''
--     FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE SET NULL
--
-- SQLite implements ON DELETE SET NULL by writing NULL into the child column,
-- which NOT NULL rejects. Deleting a device that still has probes therefore
-- fails with "NOT NULL constraint failed: probe_targets.device_id".
--
-- The NOT NULL DEFAULT '' also rejects the ordinary case of a probe that is not
-- a device probe at all. A gateway, upstream or DNS reachability check belongs
-- to no single device, and the foreign key rejects the '' the store wrote for
-- it. Such a probe could not be created.
--
-- NULL is the correct representation of "this probe is not attached to a
-- device", just as it is for network_events. Probe history is valuable because
-- it survives the device it once described. Readers keep seeing '' because the
-- store normalises NULL back to an empty string.
--
-- SQLite cannot alter a column's nullability in place, so the table is rebuilt.
-- The copy is done inside the migration transaction, and the copy uses
-- NULLIF(device_id, '') so existing unattached probes (stored as '') become
-- NULL while attached probes keep their device.

CREATE TABLE probe_targets_new (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    kind                TEXT NOT NULL DEFAULT 'device',
    target              TEXT NOT NULL,
    port                INTEGER NOT NULL DEFAULT 0,
    protocol            TEXT NOT NULL DEFAULT 'icmp',
    source_id           TEXT NOT NULL DEFAULT '',
    interval            INTEGER NOT NULL DEFAULT 60,
    timeout             INTEGER NOT NULL DEFAULT 3,
    enabled             INTEGER NOT NULL DEFAULT 1,
    device_id           TEXT,
    failure_threshold   INTEGER NOT NULL DEFAULT 3,
    recover_confirm     INTEGER NOT NULL DEFAULT 1,
    state               TEXT NOT NULL DEFAULT 'unknown',
    state_reason        TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    last_result         TEXT NOT NULL DEFAULT '',
    last_latency_ms     INTEGER NOT NULL DEFAULT 0,
    last_error          TEXT NOT NULL DEFAULT '',
    last_run_at         DATETIME,
    last_success_at     DATETIME,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE SET NULL
);

INSERT INTO probe_targets_new (
    id, name, kind, target, port, protocol, source_id, interval, timeout, enabled,
    device_id, failure_threshold, recover_confirm, state, state_reason,
    consecutive_failures, consecutive_successes, last_result, last_latency_ms, last_error,
    last_run_at, last_success_at, created_at, updated_at
)
SELECT id,
       name,
       kind,
       target,
       port,
       protocol,
       source_id,
       interval,
       timeout,
       enabled,
       NULLIF(device_id, ''),
       failure_threshold,
       recover_confirm,
       state,
       state_reason,
       consecutive_failures,
       consecutive_successes,
       last_result,
       last_latency_ms,
       last_error,
       last_run_at,
       last_success_at,
       created_at,
       updated_at
FROM probe_targets;

-- probe_results.probe_id references probe_targets(id) ON DELETE CASCADE. With
-- foreign keys enabled, DROP TABLE probe_targets performs an implicit DELETE of
-- its rows and that cascade would erase the entire probe history. The history is
-- saved first and restored after the replacement table has taken the old name.
CREATE TEMP TABLE probe_results_backup AS
SELECT id, probe_id, timestamp, success, latency_ms, status, error_type, error_message
FROM probe_results;

DROP TABLE probe_targets;
ALTER TABLE probe_targets_new RENAME TO probe_targets;

INSERT INTO probe_results (id, probe_id, timestamp, success, latency_ms, status, error_type, error_message)
SELECT id, probe_id, timestamp, success, latency_ms, status, error_type, error_message
FROM probe_results_backup;

DROP TABLE probe_results_backup;

CREATE INDEX idx_probe_targets_device ON probe_targets(device_id);
CREATE INDEX idx_probe_targets_enabled ON probe_targets(enabled);
