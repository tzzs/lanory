# Topology

How the graph is built, how confidence is derived, what the UI is allowed to
claim, and how user corrections survive.

- [Two graphs, one picture](#two-graphs-one-picture)
- [From evidence to edges](#from-evidence-to-edges)
- [Confidence](#confidence)
- [Unresolved segments](#unresolved-segments)
- [Multi-homed nodes and subnets](#multi-homed-nodes-and-subnets)
- [Offline devices in the graph](#offline-devices-in-the-graph)
- [User intent](#user-intent)
- [Layout](#layout)
- [What the graph does not claim](#what-the-graph-does-not-claim)
- [Implementation status](#implementation-status)

## Two graphs, one picture

Lanory keeps two representations and never mixes them up:

1. **Stored edges** (`topology_edges`) — durable relations: "device X is behind
   port Y of the router". Each row carries its evidence, confidence, status and
   whether the user confirmed or locked it.
2. **Rendered nodes** (`TopologyNode`) — what the UI draws. A node is either a
   device or a logical network (`br-lan`, `eth0` WAN). Network nodes carry an
   interface and a `NetworkStatus` (CIDR, gateway, device count) and no device.

The builder (`internal/topology`) derives the rendered graph from stored rows
every time it is requested. It writes nothing. Layout positions are separate
(`topology_layout`) and are applied at the end, so a re-scan that changes the
graph does not scramble positions the user arranged.

## From evidence to edges

Discovery produces `discovery.Result` values. The builder turns them into edges
with this logic:

| Observation | Edge type | Meaning |
| --- | --- | --- |
| UCI/ubus says `eth1` is a port of `br-lan` | `bridge_membership` | logical: the port belongs to the bridge |
| FDB learned MAC `m` on port `eth1`, and `m` belongs to device D | `observed_mac_port` | D was seen behind `eth1` |
| ARP/NDP shows `m` on interface `br-lan` | *evidence only* | updates the address→device relation, does not create a port edge |
| DHCP assigned address `a` to `m` | *evidence only* | identity + address, not topology |
| LLDP neighbour `c` on port `eth1` | `physical_link` | a peer advertised itself on the port |
| Both endpoints share a subnet and nothing better is known | `logical_network` | weak: same LAN, unknown hop |
| A vendor adapter reports `wireless: true` | `wireless_assoc` | the AP itself says the client is associated |
| The user drew the connection | `user_defined` | confidence 1.0, confirmed |

Every edge stores `discovery_source`, `confidence`, `evidence_count`,
`last_observed` and `unverified`, plus an optional `label` (usually the port and
speed: `eth2 · 2.5 Gbps`).

An edge is identified by `(source_id, target_id, source_interface_id,
target_interface_id, edge_type)`. Re-observing the same relation updates it
instead of creating a duplicate; the unique index enforces that in SQLite.

### Why a `logical_network` edge is the right answer for a hidden router

In the reference network, seven devices are behind `eth1`, which leads to an
Xiaomi router in bridge mode. Layer 2 cannot see the Xiaomi router. The options
were:

- invent a `switch` node → **rejected**: never fabricate a node;
- attach all seven devices directly to `eth1` with `observed_mac_port` at 0.70 →
  correct but misleading about the hop count;
- attach them with `logical_network` at 0.30 and mark them unverified → **chosen**:
  the topology says "these share a LAN with that port", which is exactly what is
  known, and the UI draws it dashed so the user knows it is incomplete.

If the user adds the Xiaomi router manually and re-parents the devices, that
relation becomes `user_defined` at 1.0 and is never overwritten.

## Confidence

Confidence is not decoration: it decides whether an edge is drawn solid, and it is
explained in the UI.

| Basis | Base | Source of the value |
| --- | --- | --- |
| User asserted | 1.00 | `discovery.ConfidenceManual` |
| LLDP neighbour | 0.95 | `discovery.ConfidenceLLDP` |
| DHCP assignment | 0.85 | `discovery.ConfidenceDHCP` |
| ARP/NDP exchange | 0.80 | `discovery.ConfidenceNeighbor` |
| Bridge FDB | 0.70 | `discovery.ConfidenceFDB` |
| mDNS | 0.60 | `discovery.ConfidenceMDNS` |
| Shared subnet | 0.30 | `discovery.ConfidenceSubnet` |

Combining rules:

- multiple independent observations of the same relation raise confidence, but
  never beyond the strongest single source plus a small increment;
- a contradiction lowers it and sets `unverified`;
- below `topology.unverified_edge_threshold` (default 0.6) the edge is
  `unverified` and the UI draws it dashed;
- bridge membership is high confidence (0.95) because it is configuration, not
  inference.

The threshold is deliberately above the subnet value (0.30): a "same subnet"
guess must never look like an established link.

## Unresolved segments

When a port has devices behind it and no intermediate node is known, the graph
uses an unresolved segment rather than inventing hardware:

```
[ router ] ──eth1──▶ (unresolved segment) ──▶ iPhone, Mac Studio, Watch, lamp, ESP32
```

The segment node has `unresolved: true`. The UI labels it "unresolved" and offers
"add the device that is really here", which creates a manual node and lets the
user re-parent. Nothing is asserted on the user's behalf.

## Multi-homed nodes and subnets

A router is not one address. The same box owns `192.168.100.1` on its LAN bridge
and `192.168.1.4` on its WAN port, and those are two different networks on two
different sides of it. `primary_ip` cannot express that, so every topology node
carries the full list:

```json
"addresses": [
  {"address":"192.168.100.1","family":"ipv4","prefix_length":24,"cidr":"192.168.100.0/24","interface":"br-lan","side":"lan","is_primary":true},
  {"address":"192.168.1.4","family":"ipv4","prefix_length":24,"cidr":"192.168.1.0/24","interface":"eth0","side":"wan"}
]
```

Three properties make this usable rather than merely verbose:

| Field | Meaning |
| --- | --- |
| `side` | the role of the interface carrying the address: `lan`, `wan`, `dmz`, `virtual`, `loopback`, or `unknown` |
| `cidr` | the network the address belongs to, masked from address + prefix |
| `interface` | the local interface, empty for an address observed on a remote device |

**Subnet is the classification key.** Addresses that share a `cidr` are on the
same network, so the UI groups them instead of printing a flat list. A node with
addresses in more than one subnet is *multi-homed* and is labelled as such on its
card (`Multi-subnet`), with the detail panel showing one row per
`side · cidr`. A remote device keeps `side: "unknown"`: it has an address, but no
interface of ours carries it, so claiming a side would be a guess.

Only the router is expected to be multi-homed today. The engine does not special
case it: any device whose stored addresses span subnets gets the same treatment,
which is what makes a second router, a DMZ host or a device that moved between
subnets fall out for free.

Link-local IPv6 addresses are excluded. They are per-interface plumbing, not
network membership, and including them would make every node look multi-homed.

## Offline devices in the graph

The topology view hides offline devices by default. The map is meant to answer
"what is on my network now", and a wall of red boxes for sleeping phones and
powered-off consoles buries that answer.

Hiding is deliberately not a plain filter:

- **An offline node with a visible descendant is kept.** Dropping a router
  because its probe failed would orphan every device behind it and silently
  claim those devices have no upstream.
- **Network nodes are never dropped.** A network is not online or offline.
- **Edges follow their endpoints.** An edge is drawn only when both ends are
  visible, so no line points at a node that is not on the canvas.
- **The count is always visible.** The toolbar toggle carries the number of
  hidden devices and a "show" action, so nothing disappears without a way back.

Offline is a *monitoring* verdict (`N` consecutive failed probes), not an absence
of evidence. A device with no probe result is `unknown`, not `offline`, and
`unknown` devices stay visible: they are the ones the user has not yet
classified, which is exactly when the map is most useful.

## User intent

Three mechanisms protect what the user asserted:

| Mechanism | Behaviour |
| --- | --- |
| `user_defined` edge | confidence 1.0, `user_confirmed: true` |
| `locked` edge | discovery updates `last_observed` but never changes type, endpoints or status; `DELETE` returns `409` until unlocked |
| manual device (`is_manual`) | never deleted or hidden by discovery, never merged away automatically |

When discovery contradicts a locked edge, Lanory keeps the user's edge and
records the observation as evidence, so the detail panel can say "the FDB no
longer sees this MAC on `eth1`" without breaking the picture.

If the user confirms a relation and the FDB temporarily disagrees (a device asleep,
a switch flushed its table), the edge stays, and only its `last_observed` ages.

## Layout

Three layout modes:

1. **Layered / tree (default)** — Internet at the top, then modem / upstream
   router, then the local router, then its ports, then devices. It matches how
   people describe a home network.
2. **Manual** — dragging a node pins it (`topology_layout.pinned = true`).
3. **Auto-layout on demand** — recomputes positions for unpinned nodes only.

Persistence rules (`topology.preserve_manual_layout`, default true):

- a stored position is applied by node ID; a new device gets an auto position;
- a node that disappears keeps its row (a device can come back);
- re-scanning never re-arranges pinned nodes;
- "re-layout" (a user action) is the only thing that discards manual positions.

Stored layout is deliberately not versioned: positions are UI state, not domain
data. If the graph structure changes enough that a position looks wrong, the user
rearranges; nothing is lost that matters.

## What the graph does not claim

- **It is not a packet path.** An edge means "these are related", not "traffic
  flows this way".
- **It is not complete.** A device that never appeared in the FDB, never took a
  DHCP lease and never answered ARP is invisible. A powered-off device appears
  from history with an old `last_seen`.
- **It does not show VLANs as separate graphs.** VLAN id is an attribute of an
  interface, and the FDB reports per-VLAN entries; the graph is one LAN view with
  VLAN annotations, not a per-VLAN breakout.
- **It does not infer wireless.** Only an explicit source can.
- **It does not compute "best path" or latency between two devices.** That needs
  a probe from an endpoint, which Lanory does not do.

## Implementation status

| Piece | Status | Test evidence |
| --- | --- | --- |
| edge construction from FDB | implemented | `topology/builder_test.go` vs fixtures |
| bridge membership edges | implemented | fixture tests |
| wireless edges | implemented, needs an explicit source | adapter tests only |
| confidence combination | implemented | `confidence_test.go` |
| unverified threshold | implemented | builder tests |
| unresolved segment | implemented | builder tests |
| locked / confirmed edges | implemented | update tests |
| manual device, manual edge | implemented | `internal/api` route tests |
| merge / hide device | implemented | route tests |
| multi-homed node addresses | implemented | `builder_test.go` (`TestMultiHomedRouterAddresses`) |
| subnet grouping | implemented | `builder_test.go`, `web/src/lib/devices.test.ts` |
| hide offline in the graph | implemented (UI) | `web/src/features/topology/graph.test.ts` |
| tree layout | implemented | `layout_test.go` |
| persisted positions, pinning | implemented | layout tests |
| auto-layout endpoint | **not implemented** (`501`) | route test asserts the 501 |
| VLAN breakout view | not implemented | — |
| cross-subnet topology | not implemented | — |
