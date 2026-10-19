-- 0001_initial.sql
-- Lanory initial schema. See docs/architecture.md for the data model overview.

CREATE TABLE devices (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    hostname          TEXT NOT NULL DEFAULT '',
    type              TEXT NOT NULL DEFAULT 'unknown',
    manufacturer      TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    is_manual         INTEGER NOT NULL DEFAULT 0,
    is_locked         INTEGER NOT NULL DEFAULT 0,
    is_hidden         INTEGER NOT NULL DEFAULT 0,
    type_source       TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'unknown',
    state_reason      TEXT NOT NULL DEFAULT '',
    last_state_change DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    primary_ipv4      TEXT NOT NULL DEFAULT '',
    primary_ipv6      TEXT NOT NULL DEFAULT '',
    primary_mac       TEXT NOT NULL DEFAULT '',
    is_gateway        INTEGER NOT NULL DEFAULT 0,
    is_self           INTEGER NOT NULL DEFAULT 0,
    first_seen        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_devices_state ON devices(state);
CREATE INDEX idx_devices_type ON devices(type);
CREATE INDEX idx_devices_last_seen ON devices(last_seen);
CREATE INDEX idx_devices_hidden ON devices(is_hidden);
CREATE INDEX idx_devices_manual ON devices(is_manual);

-- discovery sources are stored as a comma separated list; the separate table
-- below is the normalised form used by queries.
CREATE TABLE device_sources (
    device_id TEXT NOT NULL,
    source    TEXT NOT NULL,
    first_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (device_id, source),
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE TABLE device_addresses (
    id            TEXT PRIMARY KEY,
    device_id     TEXT NOT NULL,
    address       TEXT NOT NULL,
    family        TEXT NOT NULL,
    interface_id  TEXT NOT NULL DEFAULT '',
    source        TEXT NOT NULL,
    prefix_length INTEGER NOT NULL DEFAULT 0,
    is_primary    INTEGER NOT NULL DEFAULT 0,
    first_seen    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_device_addresses_unique ON device_addresses(device_id, family, address);
CREATE INDEX idx_device_addresses_address ON device_addresses(address);
CREATE INDEX idx_device_addresses_device ON device_addresses(device_id);

CREATE TABLE device_identities (
    id         TEXT PRIMARY KEY,
    device_id  TEXT NOT NULL,
    identity_type TEXT NOT NULL,
    identity_value TEXT NOT NULL,
    source     TEXT NOT NULL,
    confidence REAL NOT NULL DEFAULT 0.5,
    first_seen DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_device_identities_unique ON device_identities(device_id, identity_type, identity_value);
CREATE INDEX idx_device_identities_value ON device_identities(identity_type, identity_value);

CREATE TABLE network_interfaces (
    id             TEXT PRIMARY KEY,
    device_id      TEXT NOT NULL,
    name           TEXT NOT NULL,
    mac            TEXT NOT NULL DEFAULT '',
    type           TEXT NOT NULL DEFAULT 'ethernet',
    role           TEXT NOT NULL DEFAULT 'unknown',
    admin_state    TEXT NOT NULL DEFAULT 'unknown',
    oper_state     TEXT NOT NULL DEFAULT 'unknown',
    carrier        INTEGER NOT NULL DEFAULT 0,
    speed_mbps     INTEGER NOT NULL DEFAULT 0,
    mtu            INTEGER NOT NULL DEFAULT 0,
    bridge_id      TEXT NOT NULL DEFAULT '',
    vlan_id        INTEGER NOT NULL DEFAULT 0,
    is_physical    INTEGER NOT NULL DEFAULT 0,
    rx_bytes       INTEGER NOT NULL DEFAULT 0,
    tx_bytes       INTEGER NOT NULL DEFAULT 0,
    rx_errors      INTEGER NOT NULL DEFAULT 0,
    tx_errors      INTEGER NOT NULL DEFAULT 0,
    rx_packets     INTEGER NOT NULL DEFAULT 0,
    tx_packets     INTEGER NOT NULL DEFAULT 0,
    rx_dropped     INTEGER NOT NULL DEFAULT 0,
    tx_dropped     INTEGER NOT NULL DEFAULT 0,
    last_speed_change DATETIME,
    last_seen      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_network_interfaces_unique ON network_interfaces(device_id, name);
CREATE INDEX idx_network_interfaces_bridge ON network_interfaces(bridge_id);

CREATE TABLE topology_edges (
    id                  TEXT PRIMARY KEY,
    source_id           TEXT NOT NULL,
    target_id           TEXT NOT NULL,
    source_interface_id TEXT NOT NULL DEFAULT '',
    target_interface_id TEXT NOT NULL DEFAULT '',
    edge_type           TEXT NOT NULL,
    discovery_source    TEXT NOT NULL,
    confidence          REAL NOT NULL DEFAULT 0.5,
    user_confirmed      INTEGER NOT NULL DEFAULT 0,
    locked              INTEGER NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'unverified',
    label               TEXT NOT NULL DEFAULT '',
    unverified          INTEGER NOT NULL DEFAULT 0,
    last_observed       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (source_id) REFERENCES devices(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE INDEX idx_topology_edges_source ON topology_edges(source_id);
CREATE INDEX idx_topology_edges_target ON topology_edges(target_id);
CREATE UNIQUE INDEX idx_topology_edges_unique
    ON topology_edges(source_id, target_id, COALESCE(NULLIF(source_interface_id,''),'~'), COALESCE(NULLIF(target_interface_id,''),'~'), edge_type);

CREATE TABLE discovery_evidence (
    id            TEXT PRIMARY KEY,
    collector     TEXT NOT NULL,
    subject_type  TEXT NOT NULL,
    subject_id    TEXT NOT NULL,
    evidence_type TEXT NOT NULL,
    raw_data      TEXT NOT NULL DEFAULT '',
    observed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_discovery_evidence_subject ON discovery_evidence(subject_type, subject_id);
CREATE INDEX idx_discovery_evidence_observed ON discovery_evidence(observed_at);

CREATE TABLE probe_targets (
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
    device_id           TEXT NOT NULL DEFAULT '',
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

CREATE INDEX idx_probe_targets_device ON probe_targets(device_id);
CREATE INDEX idx_probe_targets_enabled ON probe_targets(enabled);

CREATE TABLE probe_results (
    id            TEXT PRIMARY KEY,
    probe_id      TEXT NOT NULL,
    timestamp     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success       INTEGER NOT NULL DEFAULT 0,
    latency_ms    INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'error',
    error_type    TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (probe_id) REFERENCES probe_targets(id) ON DELETE CASCADE
);

CREATE INDEX idx_probe_results_probe_time ON probe_results(probe_id, timestamp);
CREATE INDEX idx_probe_results_time ON probe_results(timestamp);

CREATE TABLE network_events (
    id         TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    device_id  TEXT NOT NULL DEFAULT '',
    edge_id    TEXT NOT NULL DEFAULT '',
    old_value  TEXT NOT NULL DEFAULT '',
    new_value  TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL DEFAULT '',
    evidence   TEXT NOT NULL DEFAULT '',
    timestamp  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE SET NULL,
    FOREIGN KEY (edge_id) REFERENCES topology_edges(id) ON DELETE SET NULL
);

CREATE INDEX idx_network_events_time ON network_events(timestamp);
CREATE INDEX idx_network_events_type ON network_events(event_type);
CREATE INDEX idx_network_events_device ON network_events(device_id);

CREATE TABLE topology_layout (
    id         TEXT PRIMARY KEY,
    node_id    TEXT NOT NULL UNIQUE,
    x          REAL NOT NULL DEFAULT 0,
    y          REAL NOT NULL DEFAULT 0,
    pinned     INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
