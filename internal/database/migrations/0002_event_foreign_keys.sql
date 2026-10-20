-- 0002_event_foreign_keys.sql
--
-- Two problems in the original network_events definition:
--
--  1. `device_id TEXT NOT NULL DEFAULT ''` combined with
--     `ON DELETE SET NULL` is contradictory. SQLite tries to write NULL when the
--     referenced device is deleted, the NOT NULL constraint rejects it, and the
--     delete fails. Deleting a device therefore silently lost or blocked its
--     history instead of preserving the event with a dangling reference.
--
--  2. The same applied to edge_id and topology_edges.
--
-- The fix is to allow NULL, which is the correct representation of "this event
-- happened, but the device it referred to no longer exists". History is valuable
-- precisely because it survives the thing it describes. Readers already treat an
-- empty string and NULL alike, so they are normalised back to '' in the store.
--
-- SQLite cannot alter a column's nullability in place, so the table is rebuilt.
-- The copy is done inside the migration transaction; foreign keys are deferred
-- automatically because the migration runner executes this inside a transaction.

CREATE TABLE network_events_new (
    id         TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    device_id  TEXT,
    edge_id    TEXT,
    old_value  TEXT NOT NULL DEFAULT '',
    new_value  TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL DEFAULT '',
    evidence   TEXT NOT NULL DEFAULT '',
    timestamp  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE SET NULL,
    FOREIGN KEY (edge_id) REFERENCES topology_edges(id) ON DELETE SET NULL
);

INSERT INTO network_events_new (id, event_type, device_id, edge_id, old_value, new_value, message, evidence, timestamp)
SELECT id,
       event_type,
       NULLIF(device_id, ''),
       NULLIF(edge_id, ''),
       old_value,
       new_value,
       message,
       evidence,
       timestamp
FROM network_events;

DROP TABLE network_events;
ALTER TABLE network_events_new RENAME TO network_events;

CREATE INDEX idx_network_events_time ON network_events(timestamp);
CREATE INDEX idx_network_events_type ON network_events(event_type);
CREATE INDEX idx_network_events_device ON network_events(device_id);
