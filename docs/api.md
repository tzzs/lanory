# Lanory API Contract (v1)

Base path: `/api/v1`. All responses are JSON. All timestamps are RFC 3339 UTC.

This document is the binding contract between the Go backend and the React
frontend. Both sides are developed in parallel against it; do not change a
response shape without updating this file and the other side.

## Conventions

- Success: `200 OK` / `201 Created` with a JSON body.
- Client error: `4xx` with `{"error": "message", "code": "machine_code", "details": {...}}`.
- Server error: `5xx` with the same envelope.
- List endpoints accept `limit` (default 100, max 500) and `offset`.
- Times are strings such as `"2026-10-19T20:12:46.517Z"`.
- Booleans are real booleans, never `0`/`1` strings.
- Empty lists are `[]`, never `null`.

## Reason codes

Every piece of text that explains *why* something is in its current state is
produced twice: as an English sentence, and as a reason code.

```json
{"code": "probe.unreachable_consecutive", "params": {"failures": "12", "source": "lanory", "protocol": "icmp"}}
```

The English sentence (`state_reason`, `message`, `title`, `detail`) is what a
log file, a bug report and an API consumer outside the web UI read. The reason
code is what a UI renders in the user's language: the code names the template,
the parameters fill it in. A sentence baked into the database cannot be
translated after the fact; a code plus parameters can.

The fields are:

- `Device.state_reason_code`, `ProbeTarget.state_reason_code`
- `NetworkEvent.reason_code`
- `Anomaly.reason_code` (the title) and `Anomaly.detail_reason_code` (the
  explanatory line under it)

Both fields are optional. A row written before reason codes existed, or one
whose text matched no known template, has no code: a client falls back to the
English text. Codes are stable; the sentences behind them are not.

## Health

`GET /api/v1/health` → `{"status":"ok","version":"0.1.0","demo_mode":false,"uptime_seconds":123}`

## Overview

`GET /api/v1/overview` → `Overview`

```json
{
  "counts": {"total": 8, "online": 5, "offline": 1, "unknown": 2, "sleeping": 0, "hidden": 0, "manual": 1},
  "interfaces": [{"interface": {...}, "role": "lan", "status": "link_up", "reason": ""}],
  "networks": [{"id": "net-br-lan", "name": "br-lan", "interface_id": "if-1", "cidr": "192.168.100.0/24", "gateway_ip": "192.168.100.1", "role": "lan", "device_count": 7}],
  "recent_new_devices": [...],
  "recent_offline_devices": [...],
  "recent_events": [...],
  "anomalies": [{"id": "...", "severity": "warning", "title": "...", "detail": "...", "reason_code": {"code": "anomaly.devices_offline", "params": {"count": "2"}}, "detail_reason_code": {"code": "anomaly.devices_offline_detail", "params": {"count": "2"}}, "since": "..."}],
  "last_scan_at": "...",
  "discovery_enabled": true,
  "demo_mode": false,
  "generated_at": "..."
}
```

## Devices

`GET /api/v1/devices?query=&type=&state=&source=&interface=&sort=&order=&online_first=&limit=&offset=`
→ `{"devices":[Device...],"total":n,"limit":n,"offset":n}`

`sort` is one of `name`, `ip`, `type`, `last_seen` (default `last_seen`), and `order` is `asc` or `desc`. With `online_first=true`, online devices form a leading group: `order` reverses the order *within* each group but never puts offline devices above online ones.

`GET /api/v1/devices/{id}` → `DeviceDetail`

```json
{
  "device": {...},
  "addresses": [{"id":"addr-1","device_id":"dev-1","address":"192.0.2.116","family":"ipv4","interface_id":"if-eth2","source":"dhcp","prefix_length":24,"is_primary":true,"first_seen":"...","last_seen":"..."}],
  "identities": [{"id":"idn-1","device_id":"dev-1","type":"mac","value":"b082e2613eb7","source":"dhcp","confidence":0.85,"first_seen":"...","last_seen":"..."}],
  "interfaces": [...],
  "edges": [TopologyEdge...],
  "probes": [ProbeTarget...],
  "last_probe": ProbeResult | null,
  "events": [NetworkEvent...]
}
```

`POST /api/v1/devices` body:

```json
{
  "name": "Optical Modem",
  "type": "modem_ont",
  "ipv4": "192.168.1.2",
  "mac": "aa:bb:cc:dd:ee:ff",
  "parent_device_id": "dev-router",
  "connection_type": "physical_link",
  "description": "provided by ISP"
}
```
→ `201` + `Device`. Devices created through this endpoint are `is_manual: true`
and start in state `unknown` unless probed.

`PATCH /api/v1/devices/{id}` body is a partial device object:
`{"name":"...","type":"router","description":"...","is_locked":true}` → `Device`

`DELETE /api/v1/devices/{id}?mode=hide|delete`
- `mode=hide` (default for discovered devices): sets `is_hidden`, keeps history.
- `mode=delete`: removes the device and its history. Manual devices only.
→ `204`

`POST /api/v1/devices/{id}/merge` body `{"source_device_id":"dev-2"}` → `200` + merged `Device`

## Topology

`GET /api/v1/topology` → `Topology`

```json
{
  "nodes": [{"id":"dev-1","kind":"device","device":{...},"state":"online","state_reason":"","primary_ip":"192.0.2.116","primary_mac":"02:11:22:33:44:b7",
             "addresses":[{"address":"192.0.2.116","family":"ipv4","prefix_length":24,"cidr":"192.0.2.0/24","side":"unknown"}],
             "children":["dev-3"],"unresolved":false}],
  "edges": [TopologyEdge...],
  "layout": [{"id":"lay-1","node_id":"dev-1","x":120.5,"y":340,"pinned":false,"updated_at":"..."}],
  "generated_at": "..."
}
```

Node kinds: `device`, `network`. Network nodes carry `network` and no `device`.

`addresses` lists **every** address of the node, not just `primary_ip`. A router
is the reason: it is one device with an address on each side of it, and a caller
that only reads `primary_ip` cannot tell which side it received.

| Field | Meaning |
| --- | --- |
| `address` | the address as observed |
| `family` | `ipv4` or `ipv6` |
| `prefix_length` | CIDR prefix length, omitted when unknown |
| `cidr` | the network the address belongs to (`192.168.100.0/24`), omitted when the prefix is unknown |
| `interface` | the local interface carrying it; omitted for an address on a remote device |
| `side` | `lan`, `wan`, `dmz`, `virtual`, `loopback` or `unknown` — the role of that interface |
| `is_primary` | true for the one address `primary_ip` was taken from |

Addresses sharing a `cidr` are on the same subnet, so a client can group them
without re-deriving the mask. A node with more than one distinct `cidr` is
multi-homed. A remote device's address has `side: "unknown"`: no interface of
ours carries it, so assigning a side would be a guess. Link-local IPv6 addresses
are not listed.

`GET /api/v1/topology/evidence` → `TopologyEvidence`
`{"edges":[{"edge":TopologyEdge,"evidence":[DiscoveryEvidence...]}], "unmapped_devices":[...]}`

`POST /api/v1/topology/edges` body:

```json
{
  "source_id": "dev-router",
  "target_id": "dev-ap",
  "edge_type": "user_defined",
  "source_interface_id": "if-eth1",
  "label": "eth1"
}
```
→ `201` + `TopologyEdge` with `confidence: 1.0`, `user_confirmed: true`.

`PATCH /api/v1/topology/edges/{id}` body `{"confidence":0.9,"locked":true,"label":"eth2 · 2.5 Gbps","status":"link_up"}` → `TopologyEdge`

`DELETE /api/v1/topology/edges/{id}` → `204`. Locked edges return `409`.

`POST /api/v1/topology/layout` body `{"nodes":[{"node_id":"dev-1","x":10,"y":20,"pinned":true}]}` → `200` `{"saved": 1}`

`POST /api/v1/topology/auto-layout` body `{"algorithm":"tree"}` → `200` `{"layout":[TopologyLayout...]}`

`algorithm` is `tree` (default), `layered` or `force`; anything else is a `400`
with `details.algorithm`. The endpoint recomputes every node position and
persists the result, so the returned layout is the layout a later `GET
/api/v1/topology` reports. Pinned nodes keep their position. When
`topology.preserve_manual_layout` is set (the default) a node the user dragged
keeps its saved position too; setting `topology.auto_layout: false` alongside it
turns the call into a no-op that returns the stored layout unchanged.

Whether the call can do that work depends on which topology engine the binary was
wired with, and a caller sees one of two things:

| Case | Response |
| --- | --- |
| Full engine (the normal deployment) | `200` with the recomputed `layout` |
| Store-only fallback, no topology engine injected | `501` `{"error":"capability requires a discovery engine that is not wired","code":"not_implemented"}` |

The `501` is a wiring fact, not a missing feature: the fallback provider reads
and writes the store but cannot compute positions, and returning the stored
layout with `200` would report a layout the caller did not ask for. A client
should treat `501` as "ask the user to move the nodes by hand and save with
`POST /api/v1/topology/layout`" rather than as a transient failure; retrying will
not change the answer.

## Discovery

`GET /api/v1/discovery/status` → `DiscoveryStatus`

```json
{
  "running": false,
  "current_job_id": "",
  "last_job": {"id":"job-1","status":"completed","trigger":"manual","started_at":"...","finished_at":"...","progress":100,"collectors":[{"name":"bridge_fdb","status":"completed","duration_ms":12,"records":9,"error":""}],"devices_found":8,"devices_new":0,"interfaces_found":5,"edges_created":7},
  "collectors": [{"name":"openwrt","supported":true,"capabilities":["interfaces","ipv4"],"description":"...","enabled":true}],
  "interval": "10m",
  "enabled": true
}
```

`POST /api/v1/discovery/scan` body `{"collectors":["bridge_fdb","arp"],"subnets":["192.168.100.0/24"]}` → `202` + `DiscoveryJob`
`GET /api/v1/discovery/jobs/{id}` → `DiscoveryJob`
`POST /api/v1/discovery/jobs/{id}/cancel` → `200` `{"cancelled": true}`
`GET /api/v1/discovery/collectors` → `{"collectors":[CollectorStatus...]}`

## Monitoring

`GET /api/v1/monitoring/status` → `MonitoringStatus`

```json
{
  "probes": [{"probe": ProbeTarget, "recent":[ProbeResult...], "loss_pct": 0.0, "avg_latency_ms": 4}],
  "interfaces": [{"interface": {...}, "role": "lan", "status": "link_up", "reason": ""}],
  "generated_at": "..."
}
```

**List fields are always arrays, never `null`.** `recent` is `[]` for a probe
that has never run, and `discovery_sources` is `[]` for a device no collector
has observed yet. A nil Go slice marshals to `null`, which is not what this
contract says and not what a client can iterate: reading `recent.length` on
`null` throws during render, and an uncaught render error unmounts the whole
app. The handlers normalise every list on the way out, and the TypeScript mirror
still declares these fields nullable so a client cannot rely on the
normalisation alone.

`GET /api/v1/monitoring/probes` → `{"probes":[ProbeTarget...]}`
`POST /api/v1/monitoring/probes` body:

```json
{"name":"Router","kind":"network","target":"192.168.100.1","protocol":"icmp","interval":60,"timeout":3}
```
or for a device: `{"name":"iPhone","kind":"device","device_id":"dev-2","protocol":"icmp","interval":60}` → `201` + `ProbeTarget`

`device_id` is **optional**. `probe_targets.device_id` is nullable in the store
(schema 0003), and the column exists to say which device a probe describes, not to
say that it must describe one:

- A probe whose device is deleted is **detached, not deleted**: the foreign key
  is `ON DELETE SET NULL`, so the row survives with no device and `device_id`
  disappears from the JSON. Probe history outlives the device it once described,
  because the address it measured may still be reachable.
- A probe with no device never moves a device's state, and does not appear under
  `probes` in `GET /api/v1/devices/{id}`.
- When a device is merged into another (`POST /api/v1/devices/{id}/merge`), its
  probes move to the surviving device.

`device_id` is omitted from the JSON when it is empty, never sent as `null` or
`""`.

One caveat a caller can observe today: `POST /api/v1/monitoring/probes` with no
`device_id` still **anchors the new probe to the host device** (the device with
`is_self: true`) rather than creating an unattached probe, so the `201` body
typically carries a `device_id` you did not send. Anchoring is what
`internal/api/ifaces.go` has always done, from before the column was made
nullable, and it matches the rule that a probe runs from the host Lanory runs on.
If no host device exists the call fails with `500`. A probe only becomes
unattached by its device being deleted.

For `kind: "device"` the address is derived from the device's stored
`primary_ipv4`/`primary_ipv6` when `target` is omitted; a device with no known
address is a `400` with `details.target`.

`POST /api/v1/monitoring/probes/{id}/run` → `200` + `ProbeResult`
`GET /api/v1/monitoring/history?device_id=&probe_id=&since=&limit=` → `{"results":[ProbeResult...]}`

## Events

`GET /api/v1/events?type=&device_id=&since=&until=&query=&limit=&offset=` → `EventList`
`GET /api/v1/events/{id}` → `NetworkEvent`

## Settings

`GET /api/v1/settings` → `{"settings": {key: value, ...}}`
`PATCH /api/v1/settings` body `{"discovery.enabled": true, "monitoring.interval": "60s"}` → `200` `{"settings": {...}}`

## Realtime (SSE)

`GET /api/v1/events/stream` — `text/event-stream`, one JSON object per `data:` frame.

```json
{"type":"device.online","seq":42,"timestamp":"...","payload":{"device":{...},"reason":"..."}}
```

Event types: `device.updated`, `device.online`, `device.offline`,
`topology.updated`, `link.updated`, `probe.completed`,
`discovery.started`, `discovery.completed`, `settings.updated`.

### Authentication: the header only

The stream is protected like every other route. The token goes in the
`Authorization: Bearer <token>` header and **nowhere else**: a token in the query
string is refused with `401`, because URLs end up in proxy access logs and in the
`Referer` header.

That rules out `EventSource`, which cannot set request headers. A client must
open the stream with `fetch` and read `response.body` as a stream — the browser
hook in `web/src/hooks/useRealtime.ts` is the reference implementation:

```js
const res = await fetch('/api/v1/events/stream', {
  headers: { Accept: 'text/event-stream', Authorization: `Bearer ${token}` },
  cache: 'no-store',
})
const reader = res.body.getReader()
```

A `401` means the token was missing or wrong and no stream was opened; the client
should surface that instead of retrying in a tight loop.

### Frame format

Each event is three lines terminated by a blank line:

```
id: 42
event: device.online
data: {"type":"device.online","seq":42,"timestamp":"2026-10-19T20:12:46.517Z","payload":{...}}

```

`id` is the bus sequence number and matches `seq` inside the payload. `event` is
the event type, so a client can dispatch by name without parsing first. The
`data:` payload is compact JSON on a single line: `encoding/json` escapes
newlines inside strings, so a frame never contains a raw newline inside a data
value and there is nothing to reassemble.

An idle stream writes a comment frame every 15 seconds:

```
: heartbeat

```

Comment lines start with `:` and carry no event; they exist to keep the
connection and every proxy in front of it alive. A parser must ignore them.

The response also sets `Cache-Control: no-store`, `X-Accel-Buffering: no` and
`Connection: keep-alive`, and every frame is flushed immediately, so a buffering
proxy cannot turn the stream into a single delayed response.

### Reconnect and replay

On reconnect a client sends the last sequence it saw:

```
Last-Event-ID: 42
```

(the `lastEventId` query parameter is accepted as a fallback for proxies that
strip the header, but it is not an authentication channel). The bus retains a
bounded window of recent events — 512 in the shipped binary — and replays what it
still has so a short disconnect does not silently lose state changes.

Two things a client must do regardless:

- **Deduplicate by `seq`.** The replay is a best-effort catch-up over a bounded
  buffer, not a guaranteed exactly-once delivery. Today the stream also replays
  the whole retained buffer into the new subscription, so a reconnect can re-send
  events at or below the `Last-Event-ID` you sent. Treat any `seq` you have
  already handled as a no-op. This is a known defect, recorded in
  `.sdd/briefs/task-12-report.md`.
- **Detect gaps.** A `seq` that jumps by more than one means the buffer dropped
  events: the client missed them and should refetch the state it cares about
  (`GET /api/v1/devices`, `/api/v1/topology`) rather than assume it is current.

Events are in-memory only. They are not persisted and not replayed across a
restart; the `seq` counter starts again at 1, so a client that reconnects to a
restarted server must discard its stored `seq`.
