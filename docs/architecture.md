# Architecture

Lanory is a single Go process that serves an embedded React frontend, a REST API
and an SSE stream, backed by one SQLite file. This document describes the moving
parts, the data model, and — importantly — what is actually implemented today.

- [Shape of the system](#shape-of-the-system)
- [Current status](#current-status)
- [Data flow](#data-flow)
- [Packages](#packages)
- [Data model](#data-model)
- [Concurrency and scheduling](#concurrency-and-scheduling)
- [Storage and flash wear](#storage-and-flash-wear)
- [Invariants](#invariants)
- [What is deliberately absent](#what-is-deliberately-absent)

## Shape of the system

```
                    ┌──────────────────────────────────────────┐
   browser ────────▶│  internal/api  (net/http, Go 1.22 mux)   │
   (SSE)   ◀────────│  REST · SSE · embedded frontend · auth   │
                    └───────────────┬──────────────────────────┘
                                    │ provider interfaces
        ┌───────────────────────────┼──────────────────────────────┐
        │                           │                              │
┌───────▼────────┐        ┌─────────▼────────┐          ┌──────────▼─────────┐
│ internal/      │        │ internal/        │          │ internal/          │
│ discovery      │        │ topology         │          │ monitoring         │
│ collectors     │        │ graph + layout   │          │ probes + state     │
└───────┬────────┘        └─────────┬────────┘          └──────────┬─────────┘
        │  []discovery.Result       │ reads                        │ writes
        ▼                           │                              │
┌───────────────────┐               │                              │
│ internal/device   │               │                              │
│ identity engine   │               │                              │
└─────────┬─────────┘               │                              │
          │ UpsertDevice / ...      │                              │
          └───────────────┬─────────┴──────────────────────────────┘
                          ▼
                 ┌──────────────────┐        ┌──────────────────────┐
                 │ internal/database│◀──────▶│ SQLite (WAL, one file)│
                 │ Store            │        └──────────────────────┘
                 └──────────────────┘
                          ▲
                          │ model.* entities
                 ┌────────┴─────────┐      ┌───────────────────────┐
                 │ internal/events  │      │ internal/adapters     │
                 │ in-process bus   │      │ generic · xiaomi      │
                 └──────────────────┘      └───────────────────────┘
                 ┌──────────────────┐
                 │ internal/demo    │  Demo Mode dataset + loader
                 └──────────────────┘
```

The important structural decision: **`internal/model` is the only place that
defines entities.** The database, the collectors, the topology engine, the API and
the TypeScript frontend all mirror those types. `docs/api.md` is the binding
contract; `web/src/types/api.ts` is its TypeScript half.

## Current status

This table is the honest answer to "does this work?". "Implemented" means the
package has tests that exercise real behaviour derived from
`fixtures/easepi-r1/`. "Verified externally" means it was run against real
hardware or a real build system — which, for packaging, it has not been.

| Area | Package | Status | Evidence |
| --- | --- | --- | --- |
| Domain model | `internal/model` | implemented | compiles, used everywhere |
| SQLite persistence + migrations | `internal/database` | implemented | `database_test.go` |
| Configuration (YAML + env + DB) | `internal/config` | implemented | unit tests |
| Event bus (SSE fan-out) | `internal/events` | implemented | used by API + monitoring |
| Discovery contract | `internal/discovery` | implemented | interface + registry tests |
| OpenWrt interface/UCI/ubus collector | `internal/discovery/openwrt` | implemented | parser tests vs fixtures |
| Bridge FDB collector | `internal/discovery/fdb` | implemented | fixture tests, noise filtering |
| ARP / NDP collectors | `internal/discovery/arp`, `ndp` | implemented | fixture tests |
| DHCP lease collector | `internal/discovery/dhcp` | implemented | fixture tests |
| LLDP collector | `internal/discovery/lldp` | implemented, opt-in | parser tests; no multicast network tested |
| mDNS collector | `internal/discovery/mdns` | implemented, opt-in | parser tests; no multicast network tested |
| Device Identity Engine | `internal/device` | implemented | precedence + merge tests |
| Topology builder | `internal/topology` | implemented | graph tests vs fixtures |
| Layout (tree, pinning) | `internal/topology` | implemented | layout tests |
| Probe engine + state machine | `internal/monitoring` | implemented | scheduler + state tests |
| Internet connectivity | `internal/monitoring` | implemented | aggregation tests |
| REST + SSE API | `internal/api` | implemented | route tests |
| Static frontend embedding | `internal/api` | implemented | SPA fallback tests |
| Frontend | `web/` | implemented | typecheck + unit tests |
| Demo Mode | `internal/demo` | implemented | 18 tests incl. idempotency |
| Generic router adapter | `internal/adapters/generic` | implemented | HTTP test server |
| Xiaomi router adapter | `internal/adapters/xiaomi` | implemented | fake device; **no real hardware** |
| Adapter registry | `internal/adapters/registry` | implemented | isolation tests |
| OpenWrt package | `packaging/openwrt` | **configured, unbuilt** | no SDK available |
| LuCI companion | `packaging/openwrt/luci-app-lanory` | **configured, unbuilt** | no LuCI SDK |
| systemd unit | `packaging/linux` | **configured, unverified** | no Linux host |
| CI | `.github/workflows/ci.yml` | configured, never run on GitHub | — |

Two further honest notes:

- Large parts of this tree were developed by several agents in parallel. The
  `internal/api` package currently works around a defect in
  `internal/database`: four interface queries (`ListInterfaces`,
  `ListAllInterfaces`, `GetInterface`, `GetInterfaceByName`) build their SQL
  without the `SELECT` keyword and fail with `near "id": syntax error`. The API
  layer reads interfaces through the store's `*sql.DB` with a correct query
  instead of editing another package; `internal/demo` does the same behind a
  feature probe. When the store is fixed, both workarounds delegate again. This
  is documented in `internal/api/ifaces.go` and
  `internal/demo/store_compat.go`.
- `network_events.edge_id` and `probe_targets.device_id` are declared
  `NOT NULL DEFAULT ''` with a foreign key. SQLite implements `ON DELETE SET
  NULL` as an `UPDATE ... SET x = NULL`, which the `NOT NULL` constraint rejects,
  so deleting a device that still has events or probes fails. Demo Mode works
  around it by ordering its deletes; a schema fix is the real answer.

## Data flow

### One discovery run

```
scheduler tick
   │
   ├─ for each enabled collector (bounded worker pool)
   │     collector.Collect(ctx) → []discovery.Result
   │     every Result carries: Kind, Source, ObservedAt and one payload
   │
   ├─ for each Result, in order
   │     identity.Resolve(Ingest{...}) → *model.Device, created, error
   │         merge precedence: strong id > MAC > history > hostname > IP
   │         writes: devices, device_identities, device_addresses, device_sources
   │
   ├─ interface results → network_interfaces (+ roles, speeds, counters)
   ├─ link results      → topology_edges (via the topology engine, with confidence)
   ├─ raw evidence      → discovery_evidence (truncated at 4 KiB)
   │
   ├─ bus.PublishDiscoveryCompleted(...)
   └─ bus.PublishTopologyUpdated("discovery")
```

A collector that fails is recorded in the run summary and the run continues. A
collector that is not supported on this host (`Supported() == false`) is skipped
silently — that is the normal state of, say, the DHCP collector on a host without
dnsmasq.

### One probe run

```
probe scheduler (per target, its own interval)
   │
   ├─ executor.Execute(ctx, target) → ProbeRecord{Result, Method, Source}
   │     icmp → tcp → http fallback chain, bounded by timeout
   │
   ├─ state machine: consecutive failures / successes
   │     offline only after failure_threshold consecutive failures
   │     online again only after recover_confirm successes
   │
   ├─ store.SaveProbeResult + store.UpdateProbeRuntime
   ├─ device engine SetOnline / SetOffline / MarkSleeping / SetUnknown
   └─ bus.PublishProbeCompleted + device.* event
```

Network probes (`kind: network`) never move a device's state: "the Internet is
reachable" says nothing about whether the printer is on.

### One API read

Handlers never compute network state on the fly except for the topology, which is
derived from stored rows. The overview endpoint aggregates: device counters,
interface statuses, logical networks, anomalies, recent events. It reads the demo
flag from `app_settings` and sets `Overview.DemoMode`, so a UI that shows
simulated data always has the flag to badge it.

## Packages

| Package | Responsibility | Must not do |
| --- | --- | --- |
| `internal/model` | entities + API response types | contain behaviour beyond validation helpers |
| `internal/database` | schema, migrations, all SQL | know about HTTP or collectors |
| `internal/config` | layered configuration | touch the network |
| `internal/events` | in-process fan-out, bounded buffer | persist events |
| `internal/discovery` | Collector contract, normalized results | mutate the network |
| `internal/discovery/*` | one source each | import each other |
| `internal/device` | identity precedence and merging | decide topology |
| `internal/topology` | graph construction, confidence, layout | probe devices |
| `internal/monitoring` | probes, state machine, internet health | discover devices |
| `internal/adapters` | vendor adapters + capability registry | store credentials on disk |
| `internal/api` | HTTP surface, auth, SSRF guard | contain business rules |
| `internal/demo` | sample dataset + idempotent loader | present itself as real data |
| `internal/platform` | OpenWrt vs generic Linux detection | read network state |

## Data model

```
devices ──┬── device_identities     (mac | dhcp_client_id | hostname | vendor | fingerprint)
          ├── device_addresses      (ipv4 | ipv6, one row per address)
          ├── device_sources        (which collectors ever saw it)
          ├── network_interfaces ──── device_addresses (interface_id)
          ├── topology_edges        (source_id, target_id, both optional interfaces)
          ├── probe_targets ──────── probe_results
          └── network_events

discovery_evidence   (collector, subject_type, subject_id, raw_data)
topology_layout      (node_id → x, y, pinned)
app_settings         (key, value)  ← includes "demo_mode"
```

### Why identity is separate from address

An IP is not an identity: DHCP reassigns it, IPv6 rotates its suffix, and two
devices can swap addresses overnight. `Device.PrimaryIPv4` is a cache for display;
the truth is in `device_addresses`, with `first_seen` / `last_seen`.

The identity engine's precedence (strongest first):

1. strong identity: DHCP client id, LLDP chassis id, vendor serial;
2. MAC address;
3. historical association (a MAC previously linked to this device);
4. hostname;
5. IP address;
6. other fingerprints.

Hostname and IP alone are never sufficient for a merge — both change and both can
be reused. A randomised MAC (locally administered bit set) is recorded with
reduced confidence (×0.6) and is never used as a long-lived identity.

### Why edges carry confidence

An edge means "these two things are related", and there are at least six ways to
know that, with very different reliability:

| Evidence | Base confidence |
| --- | --- |
| User asserted it | 1.00 |
| LLDP neighbour advertisement | 0.95 |
| DHCP server assigned the address to the MAC | 0.85 |
| ARP/NDP exchange observed | 0.80 |
| Bridge FDB learned the MAC on a port | 0.70 |
| mDNS announcement | 0.60 |
| Both endpoints share a subnet | 0.30 |

The topology engine combines evidence and marks edges below
`topology.unverified_edge_threshold` (default 0.6) as `unverified`, which the UI
draws dashed. A locked or user-confirmed edge is never overwritten by an
inference; a conflicting inference is surfaced instead.

## Concurrency and scheduling

- **One SQLite connection pool** with `MaxOpenConns(4)`. Writes are serialised by
  SQLite itself; the pool is small on purpose, because a router has little
  memory and `SQLITE_BUSY` storms are worse than a short queue.
- **Discovery runs on a bounded worker pool** (`discovery.max_concurrency`,
  default 16, hard cap 512). Collectors run concurrently; the *result application*
  is sequential, because the identity engine merges state.
- **Probes run per target**, each on its own interval, with a bounded executor.
  A probe never blocks a discovery run.
- **The event bus never blocks a producer.** Subscribers have a bounded channel;
  a slow subscriber is dropped rather than applying backpressure to the discovery
  loop. Clients reconnect and use `seq` to detect gaps.
- **HTTP handlers have a request timeout** (30 s) and a body size limit. SSE
  connections are exempt from the request timeout and send a heartbeat comment
  every 15 s so proxies do not drop them.

## Storage and flash wear

The target is a router with a small flash device. Design decisions follow from
that:

| Decision | Reason |
| --- | --- |
| `modernc.org/sqlite` (pure Go) | no cgo, so one static binary for ARM/x86/riscv and no libsqlite3 on the device. **Cost:** `modernc.org/libc` has no MIPS syscall layer, so MIPS routers are unsupported. See [deployment](deployment.md#mips-is-not-supported). |
| WAL, `synchronous=NORMAL`, `wal_autocheckpoint=1000` | fewer fsyncs per write; a power loss can lose the last transaction, never corrupt the database |
| batch writes per discovery run (default 10 min) | one transaction per run instead of one per row |
| evidence truncated at 4 KiB | raw `ip`/`bridge` output stays small |
| retention pruning (`retention_days`, default 30) | probe results and events do not grow forever |
| no log file by default | `/var/log` is tmpfs on OpenWrt; a rotating file on flash wears it out |

The database file plus its WAL is the only persistent state. Copying
`/var/lib/lanory/lanory.db*` while the service is stopped is a complete backup.

## Invariants

These are the rules the code is written to preserve. When a change seems to
require breaking one, the design is wrong, not the invariant.

1. **A collector never fails the run.** Errors are recorded and the run goes on.
2. **An unsupported source is not an error.** `Supported() == false` is a normal
   answer.
3. **Discovery is read-only.** No collector executes a command that changes
   network state. The active scan path only sends probes, and it is opt-in.
4. **Absence of evidence is not evidence of absence.** A device that was never
   probed stays `unknown`, not `offline`.
5. **Offline requires repeated failure.** `failure_threshold` consecutive failed
   probes (default 3).
6. **A network probe never changes a device state.**
7. **User intent wins.** A locked edge or a manual device is never overwritten or
   deleted by discovery.
8. **Simulated data is labelled.** Every demo row carries `model.SourceDemo`, and
   the API reports `demo_mode`.
9. **Credentials never leave memory.** No logging, no serialisation, no request to
   anything but the configured target.
10. **No credential guessing.** An adapter without a configured credential makes
    zero authentication attempts.

## What is deliberately absent

- **No Redis, no Postgres, no message queue.** The event bus is in-process; SQLite
  is the queue for everything that must survive a restart.
- **No Node/Python at runtime.** The frontend is built ahead of time and embedded.
- **No cloud, no accounts, no telemetry.** There is no code path that opens an
  outbound connection except a probe you configured, or an adapter you enabled.
- **No writing to the router.** Not even a "convenience" DHCP reservation feature.
- **No fabricated topology.** A switch behind eth1 without LLDP is not a node; it
  is an unresolved segment.

## Measured resource usage

The specification asks for CPU, memory, database growth, scan duration and
concurrency testing, and explicitly forbids inventing low-memory numbers. The
measurements below come from `make bench` and `make resource-test`, which run as
part of the normal test suite (`internal/resourcetest`).

They were taken on a **developer machine (Apple silicon, darwin/arm64)**, not on a
router. They are representative of the algorithmic cost, not of absolute router
performance — the same code on a 1 GHz MIPS-like core will be several times
slower in wall-clock terms while the complexity stays the same.

| Operation | Cost | Why it matters |
| --- | --- | --- |
| `UpsertDevice` (write) | ~80–130 µs/op | the dominant per-tick cost, bound by flash writes |
| `SaveEvent` (history write) | ~135 µs/op | one row per state transition, not per poll |
| `Service.Build()` (topology) | ~1.5 ms/op | the graph the UI polls every 15 s |
| Overview aggregate query | ~0.5 ms/op | stat tiles + device list page |
| SSE publish, 4 subscribers | ~3 µs/op | fan-out is effectively free |

Scale and retention behaviour:

| Property | Result |
| --- | --- |
| Topology build for 250 devices | 5 ms, 200 nodes — comfortably inside a 15 s poll |
| 100 consecutive builds over 200 devices | heap delta **−38 KiB** (no retained graphs) |
| 25 discovery ticks observing the same 3 devices | still 3 rows, not 75 |
| Retention pruning of 200 old + 50 recent events | removes exactly 200 |

The memory test asserts only that the heap does not grow by more than 64 MiB over
100 builds, which is a leak detector rather than a performance target. Publishing
a precise "runs in N MB" figure without measuring on the target hardware would be
dishonest, so none is published.

### What the measurements exposed

Writing these tests found a schema bug rather than confirming the code was fine.
`network_events.device_id` was declared `NOT NULL DEFAULT ''` together with
`ON DELETE SET NULL`. SQLite therefore could not satisfy the foreign key when a
device was deleted: the delete failed and the device's history was lost, which is
exactly when history matters most. Migration `0002_event_foreign_keys.sql`
rebuilds the table with nullable reference columns, and
`TestHistorySurvivesDeviceDeletion` guards the behaviour.
