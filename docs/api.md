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
  "anomalies": [{"id": "...", "severity": "warning", "title": "...", "detail": "...", "since": "..."}],
  "last_scan_at": "...",
  "discovery_enabled": true,
  "demo_mode": false,
  "generated_at": "..."
}
```

## Devices

`GET /api/v1/devices?query=&type=&state=&source=&interface=&sort=&order=&limit=&offset=`
→ `{"devices":[Device...],"total":n,"limit":n,"offset":n}`

`GET /api/v1/devices/{id}` → `DeviceDetail`

```json
{
  "device": {...},
  "addresses": [{"id":"addr-1","device_id":"dev-1","address":"192.168.100.116","family":"ipv4","interface_id":"if-eth2","source":"dhcp","prefix_length":24,"is_primary":true,"first_seen":"...","last_seen":"..."}],
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
  "nodes": [{"id":"dev-1","kind":"device","device":{...},"state":"online","state_reason":"","primary_ip":"192.168.100.116","primary_mac":"b0:82:e2:61:3e:b7","children":["dev-3"],"unresolved":false}],
  "edges": [TopologyEdge...],
  "layout": [{"id":"lay-1","node_id":"dev-1","x":120.5,"y":340,"pinned":false,"updated_at":"..."}],
  "generated_at": "..."
}
```

Node kinds: `device`, `network`. Network nodes carry `network` and no `device`.

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

`GET /api/v1/monitoring/probes` → `{"probes":[ProbeTarget...]}`
`POST /api/v1/monitoring/probes` body:

```json
{"name":"Router","kind":"network","target":"192.168.100.1","protocol":"icmp","interval":60,"timeout":3}
```
or for a device: `{"name":"iPhone","kind":"device","device_id":"dev-2","protocol":"icmp","interval":60}` → `201` + `ProbeTarget`

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

Clients must reconnect with backoff and use `seq` to detect gaps.
