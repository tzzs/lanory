# Demo Mode

Demo Mode loads a deterministic sample network so the entire UI can be previewed
without an OpenWrt router, without root, and without touching the network.

It exists because of a product requirement: a user must be able to judge whether
Lanory is worth installing *before* installing it — and because the same dataset
is what the automated tests assert against.

- [Run it](#run-it)
- [How it is honest](#how-it-is-honest)
- [The dataset](#the-dataset)
- [What gets written](#what-gets-written)
- [Idempotency](#idempotency)
- [Reseeding and purging](#reseeding-and-purging)
- [API surface](#api-surface)
- [Implementation](#implementation)
- [Limits](#limits)

## Run it

```sh
# Makefile target (builds the frontend, then runs the binary in demo mode)
make demo

# or directly
LANORY_DEMO_MODE=true LANORY_DATABASE_PATH=/tmp/lanory-demo.db go run ./cmd/lanory
```

Then open <http://127.0.0.1:8787/>.

In a YAML configuration:

```yaml
demo:
  enabled: true
  seed: "easepi-r1"
```

The database path matters: Demo Mode writes real rows into a real SQLite file. Use
a temporary path (`/tmp/lanory-demo.db`) so a demo instance never shares a file
with a real deployment.

## How it is honest

The product spec is explicit: *"不要将模拟在线状态冒充真实扫描结果"* — do not pass
simulated online status off as a real scan result. Three mechanisms enforce it.

### 1. Every row carries the demo source

| Table | Field | Value |
| --- | --- | --- |
| `devices` | `type_source` | `demo` |
| `device_sources` | `source` | `demo` (for every device) |
| `device_addresses` | `source` | `demo` |
| `device_identities` | `source` | `demo` |
| `topology_edges` | `discovery_source` | `demo` |
| `discovery_evidence` | `collector` | `demo` (or `inference` type on an edge) |
| `network_events` | `evidence` | `demo dataset` |

The UI sums discovery sources per device, so a demo device is visibly demo.

### 2. A top-level flag

`app_settings.demo_mode = "1"`, which the API reads and exposes as:

- `GET /api/v1/health` → `{"demo_mode": true}`
- `GET /api/v1/overview` → `{"demo_mode": true}`

The frontend shows a banner and a badge whenever that flag is set, including on
individual device cards.

### 3. CI enforces it end to end

The `integration` job builds the real binary with the real frontend, starts it in
Demo Mode and runs `scripts/ci/assert_demo_mode.py`, which fails unless:

- `health.demo_mode` and `overview.demo_mode` are `true`;
- every device in `/devices` lists `demo` in `discovery_sources`;
- every edge in `/topology` has `discovery_source == "demo"`;
- the device count is non-zero and the topology has edges and events.

## The dataset

Derived from `fixtures/easepi-r1/`, the sanitized copy of a real EASEPi R1
network. Addresses come from the fixture's own `192.168.100.0/24` (LAN) and
`192.168.1.0/24` (WAN) plus reserved documentation ranges; MACs are locally
administered.

### Devices (13)

| Device | Type | Address | Port | State |
| --- | --- | --- | --- | --- |
| EASEPi R1 | router | 192.168.100.1 | — | online (self, gateway) |
| Upstream Router | router | 192.168.1.1 | eth0 | online |
| ISP Optical Modem | modem_ont | 192.168.1.2 | — | unknown (manual) |
| Xiaomi Router | router | 192.168.100.2 | eth1 | online |
| client-desktop | desktop | 192.0.2.116 | eth2 | online |
| iPhone | smartphone | 192.0.2.151 | eth1 | online |
| client-mac | desktop | 192.0.2.222 | eth1 | online |
| Watch | smartwatch | 192.0.2.197 | eth1 | sleeping |
| smart-lamp | iot | 192.0.2.159 | eth1 | online |
| Unknown 192.0.2.228 | unknown | 192.0.2.228 | eth1 | offline |
| iot-sensor | iot | 192.0.2.231 | eth1 | online |
| Unknown b4:fa:48 | unknown | link-local IPv6 only | eth1 | unknown |
| NAS | nas | 192.0.2.50 | eth2 | online (manual) |

Three states are represented on purpose, because they exercise different UI:

- **online** — a recent successful probe;
- **offline** — three consecutive failures (a static-IP device with no DHCP lease);
- **sleeping** — a smartwatch that stopped answering but is not "broken";
- **unknown** — a device only ever seen in the NDP table, never probed.

### Interfaces (5)

| Interface | Role | Type | Carrier | Speed |
| --- | --- | --- | --- | --- |
| `eth0` | wan | ethernet | up | 100 Mbps |
| `eth1` | lan | ethernet | up | 1000 Mbps |
| `eth2` | lan | ethernet | up | 2500 Mbps |
| `eth3` | lan | ethernet | **no carrier** | 0 |
| `br-lan` | lan | bridge | up | — (192.168.100.1/24) |

Exact match with `fixtures/easepi-r1/expected.yaml`; a test asserts it.

### Edges (14)

```
Internet ─ upstream router ─ optical modem ─ [eth0] EASEPi R1
                                                   │
                              ┌────────────────────┼────────────────────┐
                            [eth1]              [eth2]              [eth3]
                              │                    │                   │
                        Xiaomi Router          client-desktop            (empty)
                              │                    │
        ┌──────────┬──────────┼───────┐            NAS (user-defined, 1.0)
      iPhone   client-mac   Watch  lamp/ESP32/unknown
```

- `observed_mac_port` at 0.70 for devices the FDB saw on a port;
- `user_defined` at 1.00 for the NAS the user attached;
- `logical_network` at 0.30, **unverified**, for the devices behind the Xiaomi
  router — because layer 2 cannot prove that hop;
- `bridge_membership` at 0.95 for `eth1`/`eth2` in `br-lan`;
- one deliberately unverified edge (upstream → modem) with 0.60 confidence, so the
  dashed-line rendering is visible.

### Probes and history

- 3 network probes: gateway, upstream, Internet;
- 1 device probe per addressed device;
- 4 recent results per probe for stable devices, 4 including failures for the
  offline/sleeping ones, 1 failing result for the never-probed one;
- 9 events spanning 25 minutes to 3 days ago: `device.offline`, `link.up`,
  `speed.changed` (1000 → 2500), `device.discovered`, `device.ip_changed`.

The dataset is a pure function of constants in `internal/demo/dataset.go`; repeated
calls return equal values (`TestDemoDatasetIsDeterministic`).

## What gets written

`demo.Load(store, opts)` writes, in this order (the order matters because of
foreign keys):

1. devices → device sources, MAC / DHCP-client-id / hostname / vendor identities,
   addresses, per-device evidence;
2. interfaces of the router + their addresses;
3. port evidence (`fdb_entry:<mac>` per port) so the interface filter works;
4. topology edges + per-edge inference evidence;
5. probe targets + results;
6. events (each anchored to a real edge — see the FK note below);
7. `app_settings`: `demo_mode`, `demo_seed`, `demo_loaded_at`.

### Why events carry an edge

`network_events.edge_id` is `NOT NULL DEFAULT ''` **and** has a foreign key to
`topology_edges`, so a row with an empty `edge_id` is rejected by SQLite. Every
demo event therefore references a real demo edge, which is also the honest thing
to record: the event says what changed on that relation.

## Idempotency

Loading twice must not duplicate anything. This is a P0 requirement and is tested
from several angles.

| Table | Strategy |
| --- | --- |
| `devices`, `network_interfaces`, `device_addresses`, `device_identities`, `topology_edges` | deterministic IDs + store upserts (natural keys) |
| `discovery_evidence` | append-only; written only when no demo row of that type exists for the subject |
| `probe_targets` | deterministic IDs; created if missing, runtime fields refreshed |
| `probe_results` | append-only; written only when the probe has no results yet |
| `network_events` | append-only; written only when no demo event of that type exists for the device |
| `app_settings` | upsert |

Asserted by:

- `TestLoadIsIdempotent` — load 1, 2 and 3 produce identical counts for all 11
  tables;
- `TestNoDuplicateDevices` — no repeated ID, MAC or primary IPv4 after three loads;
- `TestResetThenLoadMatchesFreshLoad` — reset + load equals a fresh load.

## Reseeding and purging

```go
// reload the dataset from scratch (drops demo rows first)
demo.Load(store, demo.Options{Reseed: true})

// remove demo data entirely, keeping real rows
demo.Reset(store)

// is this database a demo database?
demo.IsDemo(store)   // reads app_settings.demo_mode
```

`Reset` deletes, in FK-safe order:

1. demo probe results and demo probes;
2. demo evidence;
3. events of demo edges, then events marked as demo data;
4. demo edges;
5. user probes attached to demo-only devices;
6. devices whose **only** source is `demo` (cascades to addresses, identities,
   interfaces, sources);
7. leftover `demo` source rows;
8. the three `demo_*` settings.

Real devices, probes attached to real devices, and real events are left alone —
`TestResetClearsEverything` asserts that a device discovered by `dhcp` survives.

There is no REST endpoint for reseeding in this tree; the CLI flag
`--demo --demo-reseed` is the intended surface and lives in `cmd/lanory`, which is
owned by a different package. Until it exists, Demo Mode reseeds only by starting
against a fresh database path.

## API surface

```go
package demo

// Options controls how the demo dataset is loaded.
type Options struct {
    Seed   string    // "" → DefaultSeed ("easepi-r1")
    Reseed bool      // drop demo rows first
    Now    time.Time // override the reference time (tests)
}

// Demo describes a loaded dataset.
type Demo struct {
    Seed             string
    LoadedAt         time.Time
    DeviceIDs        []string
    InterfaceIDs     []string
    EdgeIDs          []string
    ProbeIDs         []string
    EventCount       int
    ProbeResultCount int
}

func Load(store *database.Store, opts Options) (*Demo, error)
func Reset(store *database.Store) error
func IsDemo(store *database.Store) bool
func Seed(store *database.Store) string
func KnownSeeds() []string          // ["easepi-r1"]
```

Errors are reserved for real problems: a nil store, an unknown seed, or a store
that rejects a row. An empty result is never an error.

## Implementation

| File | Contents |
| --- | --- |
| `internal/demo/doc.go` | package documentation and the honesty rules |
| `internal/demo/dataset.go` | constants, devices, interfaces, edges, probes, events |
| `internal/demo/load.go` | `Load`, `Reset`, `IsDemo`, `Seed`, settings |
| `internal/demo/store_compat.go` | interface reads, including the workaround for a store defect |
| `internal/demo/demo_test.go` | 15 behaviour tests |
| `internal/demo/fixtures_test.go` | 3 tests tying the dataset to `fixtures/easepi-r1` |

### The store workaround

`internal/database` builds four interface queries without the `SELECT` keyword, so
`Store.GetInterface` and friends fail with `near "id": syntax error`. That file
belongs to another package, so `internal/demo` goes through `store.DB()` with the
correct statement, behind a one-time probe
(`preferStoreInterfaceQueries`). If the store is fixed, the probe succeeds and the
helpers delegate. This is documented in `docs/development.md`.

## Limits

- **One seed today.** `KnownSeeds()` returns `["easepi-r1"]`. An unknown seed is an
  error, not a silent no-op.
- **Not a simulator.** Demo Mode does not run the discovery engine, the probe
  engine or the state machine. It writes finished rows. A demo device does not
  transition on its own.
- **No live traffic.** Latency values, counters and history are constants. The UI
  charts look plausible; they are not measurements.
- **Relative times.** Events and probe results are placed relative to the load
  time (25 minutes ago, 3 days ago …) so the history page always looks fresh, while
  `first_seen` values are fixed so row counts stay stable across loads.
- **Not a test fixture replacement.** The discovery tests use
  `fixtures/easepi-r1/` directly; Demo Mode consumes the same facts but through its
  own constants.
