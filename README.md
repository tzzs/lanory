<div align="center">

# Lanory · 网绘

**See your home network at a glance.**
**家的网络，一图看清。**

A self-hosted home network topology and monitoring tool for OpenWrt / iStoreOS.
One Go binary, one SQLite file, one interactive map. No cloud, no account, no
Node.js or Python runtime — and read-only towards your router.

[中文文档](README.zh-CN.md) · [Docs](docs/) · [Demo Mode](#demo-mode) · [MIT](LICENSE)

</div>

---

## What it is

Lanory answers a small set of questions about a home network, and only those:

- Which devices are on my network right now?
- Which physical port is each of them behind?
- What is the negotiated speed of that port, and is it up?
- Which device is unreachable, since when, and why?
- What changed in the last hour?

It is **not** a router administration tool. It never touches WAN, LAN, DHCP,
firewall, DNS, routes or VLAN configuration. It reads the state your OpenWrt
router already has and turns it into a picture you can understand.

```
Internet
   │
   ├── Optical modem
   │
   └── Upstream router 192.168.1.1
          │  100 Mbps
       ┌──┴──────────────────────────────────────┐
       │  EASEPi R1  (OpenWrt, br-lan .100.1/24)  │
       ├── eth0  WAN   192.168.1.4   100 Mbps  UP
       ├── eth1  LAN   1000 Mbps  ── Xiaomi Router ── iPhone · Mac Studio · Watch · lamp · ESP32
       ├── eth2  LAN   2500 Mbps  ── Windows PC 192.168.100.116
       └── eth3  LAN   NO-CARRIER
```

## Features

| | Feature | State |
| --- | --- | --- |
| ✅ | Passive discovery from the router's own state: interfaces, bridge FDB, ARP/NDP, DHCP leases, UCI/ubus | implemented |
| ✅ | Device Identity Engine: merges MAC / DHCP client id / hostname / vendor into one device | implemented |
| ✅ | Interactive topology graph with confidence, evidence and dashed unverified edges | implemented |
| ✅ | Manual devices, manual links, locked links, hide / merge devices | implemented |
| ✅ | Reachability monitoring with failure thresholds (offline needs *N* consecutive failures) | implemented |
| ✅ | Probe history, device state history, network event log | implemented |
| ✅ | Realtime updates over SSE | implemented |
| ✅ | OpenWrt package (procd, UCI; x86 / arm64 / armv7 — **not MIPS**) | packaging complete, **unbuilt** |
| ✅ | Docker image (distroless, multi-stage) | config complete, **unbuilt** |
| ✅ | Demo Mode: the whole UI without a router | implemented |
| ✅ | Chinese and English UI | implemented |
| ⚠️ | LLDP, mDNS, SSDP collectors | implemented, opt-in, not exercised against a real multicast network yet |
| ⚠️ | Generic router adapter | implemented (no client enumeration) |
| ⚠️ | Xiaomi router adapter | implemented, capability detection; verified with a fake device only |
| ❌ | TP-Link / ASUS / Huawei / SNMP adapters | reserved in the registry, not implemented |
| ❌ | Active subnet scanning | code path exists, disabled by default, not the recommended mode |
| ❌ | Multi-agent / cross-subnet topology | not implemented |

The honest status of every package is in **[docs/architecture.md](docs/architecture.md#current-status)**.

## Screenshots

Twelve screenshots would be nicer than a table, but this repository does not ship
screenshots yet, and inventing them would be worse than admitting that. What the
UI actually contains:

| Page | What you see |
| --- | --- |
| **Overview** | device counters, interface status with speed and carrier, anomaly cards, recent devices, recent events |
| **Topology** | React Flow graph: Internet → modem → router → ports → devices; edge labels carry port and speed; dashed for unverified; drag to pin a position |
| **Devices** | searchable table with type / state / source / interface filters, detail panel with addresses, identities, edges, probe results and evidence |
| **Monitoring** | per-probe latency and loss, interface link state, history sparklines |
| **Events** | filterable timeline (`device.online`, `link.down`, `speed.changed`, …) |
| **Settings** | collectors on/off, intervals, retention, adapter credentials, Demo Mode toggle |

To see all of it in two minutes, run Demo Mode (below). If you want screenshots
for a project page, take them from a Demo Mode instance — the data there is
clearly labelled as simulated.

## Install

### OpenWrt / iStoreOS (recommended)

```sh
# build the package with the matching OpenWrt SDK, then on the router:
apk add --allow-untrusted /tmp/lanory-0.1.0-r1.apk
/etc/init.d/lanory enable && /etc/init.d/lanory start
```

Full instructions, including the APK build, dependency list and upgrade
semantics: **[packaging/openwrt/README.md](packaging/openwrt/README.md)**.

> The package build is **not** verified in this repository: no OpenWrt SDK was
> available. The Makefile and init script follow upstream conventions and are
> marked as unbuilt until you run them on a Linux host with the right SDK.

### Docker (optional)

```sh
# full layer-2 discovery needs host networking
docker build -f packaging/docker/Dockerfile -t lanory:0.1.0 .
docker run -d --name lanory --network host \
  -v lanory-data:/var/lib/lanory lanory:0.1.0
```

Bridge networking loses ARP/FDB/mDNS visibility — read
[packaging/docker/README.md](packaging/docker/README.md) before choosing it.

### Generic Linux (systemd)

```sh
CGO_ENABLED=0 go build -trimpath -o lanory ./cmd/lanory
sudo install -Dm0755 lanory /usr/bin/lanory
sudo install -Dm0644 packaging/linux/lanory.service /etc/systemd/system/
sudo systemctl enable --now lanory
```

See [packaging/linux/README.md](packaging/linux/README.md).

## Demo Mode

The fastest way to see whether Lanory is useful to you. No router, no root, no
network access required.

```sh
make demo
# or, manually:
LANORY_DEMO_MODE=true LANORY_DATABASE_PATH=/tmp/lanory-demo.db \
  go run ./cmd/lanory
```

Then open <http://127.0.0.1:8787/>.

Demo Mode loads a deterministic sample network derived from
`fixtures/easepi-r1/` — the sanitized reference network in this repository — into
a fresh SQLite database: 13 devices, 5 interfaces (eth0 WAN 100 Mbps, eth1 LAN
1 Gbps, eth2 LAN 2.5 Gbps, eth3 NO-CARRIER, br-lan), topology edges, probe
targets with history, and a timeline of events.

**Everything is labelled as simulated.** Every device, interface, edge and event
carries the `demo` discovery source, and `/api/v1/health` and `/api/v1/overview`
report `demo_mode: true` so the UI can show a banner. The product spec forbids
presenting simulated online status as a real scan result, and Demo Mode is built
to make that mistake impossible. Loading is idempotent: restart as often as you
like, nothing is duplicated.

Details: [docs/demo.md](docs/demo.md).

## Development

Requirements: Go 1.26+, Node 22+ (frontend only).

```sh
make web-dev      # terminal 1: Vite dev server on :5173, proxies /api to :8787
make run          # terminal 2: build the frontend into the binary and run it
make test         # go test ./...
make check        # gofmt + go vet + go test
cd web && npm test
```

Repository layout:

```
cmd/lanory/            entry point
internal/model/        domain model — the single source of truth
internal/database/     SQLite persistence, versioned migrations
internal/discovery/    collectors: openwrt, fdb, arp, ndp, dhcp, lldp, mdns
internal/device/       Device Identity Engine (merge rules, precedence)
internal/topology/     graph construction, confidence, layout
internal/monitoring/   probe engine, state transitions, internet health
internal/adapters/     router adapters: generic, xiaomi (+ capability registry)
internal/api/          REST + SSE + static hosting
internal/demo/         Demo Mode dataset and loader
internal/platform/     OpenWrt vs generic Linux detection
web/                   React 18 + TypeScript + Vite + Tailwind + React Flow
packaging/             openwrt / docker / linux
docs/                  architecture, discovery, topology, api, development, …
fixtures/easepi-r1/    sanitized reference network used by tests and Demo Mode
```

Read [docs/development.md](docs/development.md) before your first change, and
[docs/architecture.md](docs/architecture.md) for the data model.

## Roadmap

**Now (0.1)** — the current tree
Passive discovery, identity engine, topology, monitoring, manual editing, SSE,
OpenWrt packaging, Docker packaging, Demo Mode, bilingual UI.

**Next (0.2)**
Verify the OpenWrt package against a real 25.12 / iStoreOS 25.12.5 target; verify
the Xiaomi adapter against real hardware; LLDP-based switch discovery; per-port
traffic graphs; a second language pass on the event log.

**Later**
TP-Link / ASUS adapters, generic SNMP, multi-agent probing across subnets, WiFi
association from the wireless driver, export / import of the topology.

**Never**
Writing to the router's configuration. Cloud sync. Accounts. Telemetry.

## Design principles

1. **Read-only.** Lanory reads state; it does not configure your router.
2. **Evidence over guesses.** Every link carries its source, its evidence and a
   confidence. Unproven links are dashed, not hidden.
3. **Absence of data is not an error.** A missing collector, an unreachable
   router API, an unsupported capability — none of them break the application.
4. **Local first.** No cloud, no telemetry, no account. One binary, one file.
5. **Honest UI.** Simulated data is labelled. Inferred relations say so.
   "Unknown" is a real state, distinct from "offline".
6. **Small enough for a router.** Pure-Go SQLite, no cgo, batched writes, WAL with
   a modest autocheckpoint.

## Technical limitations

These are properties of the data sources, not bugs to be fixed later. Read
**[docs/discovery.md](docs/discovery.md)** for the full discussion.

- **The FDB does not prove a device is wireless.** It proves which bridge port a
  MAC was learned on. A port with many MACs may lead to an unmanaged switch or an
  access point; Lanory therefore never asserts "these are WiFi clients".
- **A port with several MACs hides its topology.** The switch or AP behind it is
  invisible unless LLDP or a vendor API reports it. Lanory leaves the segment
  unresolved rather than inventing a node.
- **Randomised MAC addresses break long-term tracking.** Modern phones rotate
  their MAC. Lanory records such addresses with reduced confidence and relies on
  hostname / DHCP client id for merging, which is weaker.
- **ARP/NDP neighbour states are not reachability results.** `REACHABLE`,
  `STALE`, `DELAY`, `PROBE`, `FAILED`, `INCOMPLETE` describe the kernel's
  neighbour cache, not whether the device answers right now.
- **A probe from the router does not prove two endpoints can reach each other.**
  It proves the router reached one endpoint. Client-to-client reachability needs a
  probe from a client.
- **Demo Mode data is simulated.** It is derived from a real snapshot but nothing
  in it is live. It is always labelled.
- **MIPS routers are not supported by this version.** The pure-Go SQLite driver
  (`modernc.org/sqlite` → `modernc.org/libc`) has no MIPS syscall layer, so the
  database layer does not compile for `mips`/`mips64`. ARM and x86 targets are
  unaffected. See [packaging/openwrt/README.md](packaging/openwrt/README.md#target-architecture).

## Security

Lanory exposes a detailed map of your home network. It binds to the LAN and
supports bearer token authentication. Adapter credentials are held in memory,
never logged, never sent anywhere but the configured device; Lanory never tries
default passwords and never attempts to bypass authentication.

Read [docs/security.md](docs/security.md). To report a vulnerability, open a
private security advisory rather than a public issue.

## License

[MIT](LICENSE). Copyright (c) 2026 Lanory contributors.

The `fixtures/` directory contains a **sanitized** copy of a real network
snapshot. The original is not in this repository and never will be.
