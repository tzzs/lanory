# Development

Everything needed to build, test and change Lanory.

- [Prerequisites](#prerequisites)
- [First run](#first-run)
- [Daily loop](#daily-loop)
- [Tests](#tests)
- [Repository conventions](#repository-conventions)
- [Where to change what](#where-to-change-what)
- [Frontend](#frontend)
- [Fixtures](#fixtures)
- [Adding a collector](#adding-a-collector)
- [Adding a router adapter](#adding-a-router-adapter)
- [Debugging](#debugging)
- [Known defects in the current tree](#known-defects-in-the-current-tree)

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | 1.26+ | everything |
| Node.js | 22+ | the frontend (`web/`), and `make web` |
| npm | 10+ | frontend dependencies |
| make | any | the convenience targets |
| sqlite3 | optional | inspecting the database by hand |
| golangci-lint | optional | not wired into CI yet; `go vet` is |

`CGO_ENABLED=0` is the supported build mode: SQLite is
[`modernc.org/sqlite`](https://modernc.org/sqlite), a pure-Go translation, so a
static binary builds for every router architecture.

## First run

```sh
git clone https://github.com/lanory/lanory.git
cd lanory

# 1. Backend only, with the built-in placeholder page (no Node needed).
go run ./cmd/lanory --config packaging/linux/config.yaml

# 2. Or: the real UI.
cd web && npm ci && npm run build && cd ..
go run ./cmd/lanory

# 3. Or: the whole UI with simulated data, on any OS, without a router.
LANORY_DEMO_MODE=true LANORY_DATABASE_PATH=/tmp/lanory-demo.db go run ./cmd/lanory
```

Then <http://127.0.0.1:8787/>.

## Daily loop

Two terminals:

```sh
# terminal 1 — Vite dev server on :5173, proxies /api to :8787
make web-dev

# terminal 2 — Go server; rebuilds the frontend first with `make run`
go run ./cmd/lanory
```

`app_settings` (written by the Settings page) overrides the config file, so a
setting changed in the UI survives a restart even if the YAML says otherwise. To
reset, delete the row:

```sh
sqlite3 /tmp/lanory.db "DELETE FROM app_settings WHERE key LIKE 'discovery.%';"
```

## Tests

```sh
make test                 # go test ./...
make test-race            # race detector (slower, catches real bugs)
make check                # gofmt + go vet + go test
cd web && npm test        # vitest
cd web && npm run typecheck
```

Conventions:

- **Tests derive expectations from `fixtures/easepi-r1/`**, not from invented
  numbers. If a parser changes, the fixture decides whether it is still right.
- **No tautologies.** A test that asserts a constant equals itself is worse than
  no test: it looks like coverage. `internal/demo` deliberately asserts real
  behaviour (idempotency, duplicate detection, source tagging).
- **Everything network-facing takes an injectable dialer or executor** so tests
  never touch a real network. `internal/adapters/*` tests use `httptest`.
- **A test that needs root, a router or a live network is skipped, not faked.**
  Use `t.Skip` with the reason.

Useful single-package runs:

```sh
go test ./internal/discovery/fdb -run TestParse -v
go test ./internal/topology -count=1 -v
go test ./internal/demo -v
```

## Repository conventions

- **`internal/model` is the contract.** Changing a JSON tag there changes the API;
  update `docs/api.md` and `web/src/types/api.ts` in the same commit.
- **`internal/database` owns all SQL.** No other package writes a query, except
  the documented workaround in `internal/api/ifaces.go` and
  `internal/demo/store_compat.go` (see
  [known defects](#known-defects-in-the-current-tree)).
- **Collectors report, they do not decide.** A collector returns
  `discovery.Result`; merging and topology belong to other packages.
- **Every exported symbol has a doc comment.** "Exported" includes package-level
  functions, types, methods and constants.
- **No placeholder implementations.** An unimplemented endpoint returns `501` with
  a machine readable code; it does not return fake data. `auto-layout` is the
  current example.
- **`gofmt` always.** `gofmt -l` must be empty for the files you touch.
- **No new dependency without a reason.** The current set is `modernc.org/sqlite`,
  `gopkg.in/yaml.v3` and `golang.org/x/net`. Anything else needs a paragraph in the
  PR explaining why the standard library cannot do it.
- **Never commit** `fixtures/private/`, `docs/lanory.md` (the local product brief),
  a database file, or `web/dist`.

## Where to change what

| You want to… | Change |
| --- | --- |
| add a field to a device | `internal/model/model.go`, migration, `web/src/types/api.ts`, `docs/api.md` |
| change how two observations merge | `internal/device/identity.go` |
| change which port a device lands on | `internal/topology/builder.go` |
| change confidence rules | `internal/topology/confidence.go` |
| add an API endpoint | `internal/api/server.go` (route), `internal/api/handlers_*.go`, `docs/api.md` |
| add a metric / probe protocol | `internal/monitoring/probe.go` |
| add a discovery source | `internal/discovery/<name>/`, see below |
| add a vendor | `internal/adapters/<vendor>/`, register in `internal/adapters/registry` |
| change the demo dataset | `internal/demo/dataset.go` + `load.go` |
| change the UI | `web/src/features/<page>/` |
| change packaging | `packaging/` |

## Frontend

```
web/src/
├── app/          App shell, router, AppShell
├── components/   shared UI primitives (ui.tsx, Sidebar, AlertBanner)
├── features/     one directory per page
│   ├── overview/ devices/ topology/ monitoring/ events/ settings/
├── hooks/        useApi (TanStack Query), useRealtime (SSE)
├── i18n/         zh-CN and en dictionaries
├── lib/          api client, queryClient
├── stores/       zustand UI state (selection, theme, language, filters)
└── types/        api.ts — the TypeScript mirror of internal/model
```

Rules:

- **Server state belongs to TanStack Query**, not zustand. zustand holds only what
  must survive a route change (selection, theme, language, filters).
- **The topology page is lazy-loaded** because React Flow is ~100 kB.
- **All user-visible strings go through `i18n`**, in both languages. A missing key
  falls back to the key itself, which is visible in review.
- **Demo Mode must be visible**: if `overview.demo_mode` is true, render the demo
  banner and badge; never present simulated data as real.
- **Polling intervals are conservative** (30–60 s) because the backend may be a
  router. SSE is the primary update path.

```sh
cd web
npm run typecheck    # tsc -b --noEmit
npm test             # vitest
npm run build        # tsc -b && vite build  → web/dist
```

## Fixtures

`fixtures/easepi-r1/` is a **sanitized** copy of a real network snapshot:

- `ip-link.txt`, `ip-neigh.txt`, `ip-neigh6.txt`
- `bridge-fdb.txt`
- `dhcp-leases.txt`
- `uci-network.txt`, `ubus-network-device.json`, `ubus-network-interface.json`
- `expected.yaml` — the acceptance criteria; tests read it rather than hardcoding
  expectations

`fixtures/private/` holds the real snapshot and is git-ignored. **Never read it,
copy from it, or reference it.** If you need a new fixture property, derive it
from the sanitized copy by hand.

To add a fixture file, keep the same de-identification rules: documentation
ranges for addresses, locally administered MACs, invented hostnames.

## Adding a collector

1. Create `internal/discovery/<name>/collector.go` implementing
   `discovery.Collector`:

```go
type Collector struct{ /* … */ }

func (c *Collector) Name() string        { return "my_source" }
func (c *Collector) Supported() bool     { /* is the command/file available? */ }
func (c *Collector) Capabilities() []string { return []string{discovery.CapMAC} }
func (c *Collector) Collect(ctx context.Context) ([]discovery.Result, error)
func (c *Collector) Description() string { /* one line for the UI */ }
```

2. `Supported()` must return `false` (not an error) when the tool is absent.
3. `Collect()` must return `nil, err` only for a real failure; an empty network is
   `[]Result{}, nil`.
4. Register it in `internal/discovery/registry.go` and add a config flag in
   `internal/config/config.go`.
5. Add a `collectors.<name>` entry to `docs/discovery.md` and to the YAML samples.
6. Write parser tests against a new fixture file. Do not assert on invented input.

## Adding a router adapter

1. Create `internal/adapters/<vendor>/<vendor>.go`:

```go
func New(cfg adapters.Config) (adapters.Adapter, error)
func (a *Adapter) Name() string
func (a *Adapter) Manufacturer() string
func (a *Adapter) Capabilities() []adapters.Capability
func (a *Adapter) Probe(ctx context.Context) ([]discovery.Result, error)
func (a *Adapter) Status() adapters.Status
func (a *Adapter) Fallback() string
```

2. **Detect capabilities, never assume them.** Return `Status().Detected` with only
   what the device actually proved with a well-shaped response.
3. **Never guess credentials.** If `cfg.Credential.Configured()` is false, return
   immediately with zero requests. Write the test that asserts the counter is 0.
4. **Never send a secret over plain HTTP** unless the user set `AllowInsecure` or
   the target is loopback.
5. Register in `internal/adapters/registry/registry.go`.
6. Add the vendor to `docs/discovery.md` and the security notes in
   `docs/security.md`.

## Debugging

| Symptom | Where to look |
| --- | --- |
| a device is missing | `bridge fdb show`, `ip neigh`, `/tmp/dhcp.leases` on the router; then the discovery status page |
| a device has the wrong port | the FDB entry and `expected.yaml`; a port with many MACs is not a bug |
| a device flickers online/offline | raise `failure_threshold`; check the probe log |
| the UI shows no data | `/api/v1/health` first, then `/api/v1/overview` |
| SSE never updates | `/api/v1/events/stream` in a browser devtools network tab; a proxy may buffer it |
| the database grows | lower `retention_days`; check `discovery_evidence` |

Handy queries:

```sh
sqlite3 /var/lib/lanory/lanory.db "SELECT id, name, state, primary_ipv4 FROM devices;"
sqlite3 /var/lib/lanory/lanory.db "SELECT source_id, target_id, edge_type, confidence FROM topology_edges;"
sqlite3 /var/lib/lanory/lanory.db "SELECT event_type, message, timestamp FROM network_events ORDER BY timestamp DESC LIMIT 20;"
sqlite3 /var/lib/lanory/lanory.db "SELECT COUNT(*), MIN(timestamp), MAX(timestamp) FROM probe_results;"
sqlite3 /var/lib/lanory/lanory.db "SELECT * FROM app_settings WHERE key = 'demo_mode';"
```

Run the server with `--log-level debug` (or `LANORY_LOG_LEVEL=debug`) to see each
collector's duration and record count.

## Known defects in the current tree

These are real, present in `main`, and owned by packages this document's author
does not modify. They are listed so nobody debugs them twice.

### 1. Interface queries in `internal/database` omit `SELECT`

```go
// internal/database/store.go — current
rows, err := s.db.Query(interfaceColumns+` FROM network_interfaces WHERE …`)
```

`interfaceColumns` is a plain column list, so the statement starts with `id, …`
and SQLite rejects it: `SQL logic error: near "id": syntax error`.

Affected: `Store.ListInterfaces`, `Store.ListAllInterfaces`,
`Store.GetInterface`, `Store.GetInterfaceByName`, and therefore
`Store.UpsertInterface` (its existence check calls `GetInterface`).

Workaround in place:

- `internal/api/ifaces.go` reads interfaces through `store.DB()` with the correct
  statement;
- `internal/demo/store_compat.go` probes the store once and uses the same
  fallback if the store is broken.

Fix: prefix each of the four statements with `"SELECT " +`. Both workarounds then
delegate automatically (`preferStoreInterfaceQueries` flips to true).

### 2. `NOT NULL DEFAULT ''` next to a foreign key

`network_events.edge_id` and `probe_targets.device_id` are declared
`NOT NULL DEFAULT ''` **and** carry a foreign key with `ON DELETE SET NULL`.
SQLite implements the FK action as `UPDATE … SET x = NULL`, which the `NOT NULL`
constraint rejects, so:

- inserting an event with no edge fails with
  `FOREIGN KEY constraint failed`;
- deleting a device that still has probes or events fails the same way.

Demo Mode orders its deletes and always anchors its rows to real devices/edges to
work around it. The real fix is a migration making those columns nullable (or
dropping the FK):
`ALTER TABLE network_events ...` is not supported by SQLite, so it needs a
table rebuild.

### 3. Pre-existing `gofmt` drift

Some baseline files are not `gofmt` clean (alignment in `internal/model`,
`internal/database`, `internal/events`, `internal/device`,
`internal/discovery`, `internal/config`). CI therefore checks formatting only on
files changed in the range. Formatting the whole tree is a single mechanical
commit nobody has made yet.
