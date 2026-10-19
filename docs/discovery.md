# Discovery

How Lanory learns what is on the network, what each source can and cannot prove,
and where the limits are. This is the most important document in the repository,
because most of Lanory's UI quality comes from being honest here.

- [What discovery is not](#what-discovery-is-not)
- [Sources](#sources)
- [The reference network](#the-reference-network)
- [Noise that must not become a device](#noise-that-must-not-become-a-device)
- [Limits, with the reason](#limits-with-the-reason)
- [Adapters: the vendor API path](#adapters-the-vendor-api-path)
- [Configuration](#configuration)
- [Implementation status](#implementation-status)

## What discovery is not

Discovery does not "scan your network and find everything". There is no such
thing. What Lanory does is read the evidence the router already accumulated, and
be precise about what that evidence proves:

- a MAC learned on a bridge port proves **the bridge saw that MAC behind that
  port**, nothing more;
- an ARP entry proves **the router recently exchanged traffic with that address**;
- a DHCP lease proves **the DHCP server handed out that address to that client**;
- none of these proves the device is online *now*, and none of them proves a
  device is wireless.

Everything below follows from taking that seriously.

## Sources

### Interface inventory — `internal/discovery/openwrt`

Reads `ip -j link`, `ip -j addr`, `ubus call network.interface dump`,
`ubus call network.device status` (with `uci show network` as a fallback) and
`ip -j neigh`/`ip -j -6 neigh`.

Proves: interface existence, admin/oper state, carrier, negotiated speed, MTU,
MAC, counters, addresses, bridge membership.

Does **not** prove: which end device is behind a port — only the FDB does that.

Role assignment (`wan` / `lan` / `dmz` / `loopback` / `virtual` / `unknown`) comes
from UCI/ubus logical interfaces, not from guesswork about names: `eth0` is not
WAN because it is called eth0.

### Bridge forwarding database — `internal/discovery/fdb`

`bridge -j fdb show` (falling back to `bridge fdb show`, `brctl showmacs`).

Proves: **a MAC address was learned on a specific bridge port.**

Does not prove: wireless, the number of devices behind the port, whether the
device is online, or which VLAN a frame belongs to (the FDB reports it, but a
single-MAC-per-VLAN rule means it is not a device property).

This is the only passive source that answers "which port?" — which is why it is
the most important collector and the one with the most noise filtering.

### ARP and NDP — `internal/discovery/arp`, `internal/discovery/ndp`

`ip -j neigh` and `ip -j -6 neigh`.

Proves: the kernel had a neighbour entry for (address, MAC) on an interface, with
a state.

Does not prove: reachability. The states mean:

| State | What it actually means |
| --- | --- |
| `REACHABLE` | the kernel confirmed reachability within `base_reachable_time` (tens of seconds), not now |
| `STALE` | the entry is still usable but has not been confirmed recently |
| `DELAY` | the kernel is waiting before re-confirming |
| `PROBE` | the kernel is actively sending unicast probes right now |
| `FAILED` | resolution failed — the address may still be in use |
| `INCOMPLETE` | no MAC has ever been resolved for this address |
| `PERMANENT` | a static entry |
| `NOARP` | no address resolution is performed |

Lanory stores the state as evidence and **never** turns `REACHABLE` into
"online". Online/offline comes from the probe engine only.

IPv6 link-local addresses are kept as evidence but never promoted to a device's
primary address: they change with the interface MAC and are not routable.

### DHCP leases — `internal/discovery/dhcp`

Reads `/tmp/dhcp.leases` (dnsmasq on OpenWrt), or a configured path.

Proves: the DHCP server assigned this address to this MAC, at this time, with this
hostname and client id.

Does not prove: that the device is currently using the address. A lease survives
a device being switched off for hours. It is corroborating evidence, not a
liveness signal.

A device with a static IP simply does not appear here; that is expected, not an
error. Devices discovered only via FDB/ARP still show up.

### LLDP — `internal/discovery/lldp`

`lldpctl -f json` or `lldpcli show neighbors`.

Proves: a peer *advertised itself* on this port, with a chassis id, port id and
often a system name. This is the strongest passive evidence of a physical link,
and it is the only way to see an unmanaged switch that runs LLDP.

Does not prove: anything when LLDP is not running on the peer — which is the case
for most consumer hardware. Disabled by default, because it needs `lldpd` and
multicast traffic.

### mDNS — `internal/discovery/mdns`

Observes mDNS announcements on the LAN.

Proves: a hostname and often a service type (`_airplay._tcp`, `_ipp._tcp`),
which is good type evidence ("this is an Apple TV", "this is a printer").

Does not prove: the IP→port relation, and it is noisy — many devices announce
several times an hour, and some announce nothing. Disabled by default.

### Vendor adapters — `internal/adapters/*`

See [below](#adapters-the-vendor-api-path). An adapter can prove things no passive
source can, in particular **wireless association**: which SSID and band a client
uses. That requires an authenticated API, which most routers do not have.

## The reference network

Every test and Demo Mode uses the sanitized snapshot in
`fixtures/easepi-r1/`. It reproduces a real EASEPi R1 router:

```
Internet
   │
   ├── optical modem
   │
   └── upstream router 192.168.1.1
          │ eth0, 100 Mbps
       ┌──┴──────────────────────────────────────┐
       │   EASEPi R1   br-lan 192.168.100.1/24    │
       │                                          │
       ├── eth1  1000 Mbps  UP  ── Xiaomi Router ── 7 MACs
       ├── eth2  2500 Mbps  UP  ── Windows PC 192.0.2.116
       └── eth3  NO-CARRIER      (cable unplugged)
```

What the fixture asserts, and what the tests therefore assert:

| Interface | Role | Carrier | Speed | Bridge |
| --- | --- | --- | --- | --- |
| `eth0` | WAN | up | 100 Mbps | — |
| `eth1` | LAN | up | 1000 Mbps | `br-lan` |
| `eth2` | LAN | up | 2500 Mbps | `br-lan` |
| `eth3` | LAN | **no carrier** | 0 | `br-lan` |
| `br-lan` | LAN | up | (2500) | bridge of the three |

FDB entries:

| MAC | Port | Becomes a device? |
| --- | --- | --- |
| `02:11:22:33:44:b7` | `eth2` | yes — Windows PC |
| `06:77:88:99:aa:d2` | `eth1` | yes — iPhone |
| `07:88:99:aa:bb:5e` | `eth1` | yes — Mac Studio |
| `04:66:77:88:99:e3` | `eth1` | yes — Watch |
| `05:22:33:44:55:2d` | `eth1` | yes — desk lamp |
| `04:11:22:33:44:24` | `eth1` | yes — static IP, no DHCP lease |
| `cc:11:22:33:44:20` | `eth1` | yes — ESP32 |
| `b4:11:22:33:44:13` | `eth1` | yes — IPv6-only, identity unknown |

Expected totals: **8 discovered devices**, exactly one unknown-identity device,
and the Windows PC attached to `eth2` — not `eth1`.

## Noise that must not become a device

The FDB is full of entries that are not devices. Creating a device for any of them
gives the user something they have to delete by hand, forever. The filter is
layered, in `internal/discovery/fdb` and `internal/database/mac.go`:

| Pattern | Example | Why it is not a device |
| --- | --- | --- |
| IPv6 multicast (`33:33:*`) | `33:33:00:00:00:01` | group address, never a station |
| IPv4 multicast (`01:00:5e:*`) | `01:00:5e:00:00:01` | group address |
| broadcast | `ff:ff:ff:ff:ff:ff` | not a station |
| all-zero | `00:00:00:00:00:00` | placeholder |
| bridge self / local, permanent | `02:00:00:00:00:11` | the bridge's own address on its own port |
| `self` entries | `33:33:ff:4a:3d:d2` | the bridge's membership record |
| virtual interface | `docker0`, `dummy0`, `gretap0`, `erspan0`, `veth*` | infrastructure, not an end device |
| loopback | `lo` | never a device |

Two additional rules:

- **Locally administered MACs are kept but downgraded.** `02:*` is both "bridge
  self" and "randomised client MAC", so the rule is contextual: permanent/local
  entries on a bridge port are dropped; a dynamic entry with the local bit set is
  kept as a device with reduced identity confidence (`×0.6`).
- **A MAC is a device only once.** Identity matching is by canonical MAC, so an
  address learned on two ports updates one device instead of creating two.

## Limits, with the reason

These are the limitations stated in the product brief, expanded with the reason
each one exists.

### The FDB alone cannot prove a device is on WiFi

The FDB says "this MAC was learned on `eth1`". It does not say whether `eth1`
leads to a cable, an unmanaged switch, or an access point with twelve clients.

Lanory therefore **never emits `EdgeTypeWirelessAssoc` from FDB data**. Wireless
association requires an explicit source (a wireless driver dump, an LLDP
neighbour that says "port 3 is a bridge", or a vendor API that says
`connection: wireless`). In the reference network, seven MACs on `eth1` is
correctly reported as "seven devices behind this port", not "seven WiFi clients".

### A port with several MACs may hide an unmanaged switch or AP

If `eth1` carries a Xiaomi router in bridge mode, everything behind that router
appears on `eth1`. The router itself is invisible at layer 2 unless it speaks
LLDP or exposes a management API.

The honest representation is an **unresolved segment**: the devices are attached
to `eth1`, and the topology draws them as children of the port without inventing
an intermediate node. A user who knows what is there can add the Xiaomi router
manually and re-parent the devices; that relation is then `user_defined` with
confidence 1.0.

Lanory does not guess "there must be a switch here because there are many MACs".

### Randomised MAC addresses make long-term tracking unreliable

iOS, Android and Windows rotate their MAC per SSID (and sometimes hourly). From
the router's point of view the same phone is a new device every day.

Lanory's mitigation, and its limits:

- a locally administered MAC is stored with `confidence × 0.6`;
- merging falls back to DHCP client id and hostname, which change less often;
- hostname-only merging requires corroboration (a shared address), so two devices
  called `iPhone` do not become one.

The unavoidable consequence: a randomised phone may appear as several devices
over months, and Lanory cannot fix that without an identity source the protocol
does not provide.

### ARP/NDP neighbour states are not reachability results

`REACHABLE` means "confirmed within the last ~30 seconds, per kernel timers".
`STALE` means "usable, unconfirmed". `FAILED` means "resolution failed". None of
them is a liveness measurement, and the kernel's timers are not a monitoring
service.

Lanory uses neighbour state as *evidence* (with confidence 0.80 for the
address→MAC relation) and gets liveness exclusively from the probe engine.

### A probe from the router does not prove two endpoints can reach each other

If the router can `ping 192.0.2.116`, it does not follow that the phone can.
Client isolation, AP VLANs and firewall rules between clients all break that
inference while leaving the router's own view intact.

Lanory therefore reports "the router reached this address", not "this device is
reachable from everywhere". Cross-endpoint reachability needs an agent on an
endpoint, which is not implemented.

### Demo Mode data is simulated

The dataset is derived from a real snapshot, and every row is tagged
`model.SourceDemo`. The API reports `demo_mode: true`; the UI shows a banner. It
must never be mistaken for a live scan, and the tests enforce the tagging.

## Adapters: the vendor API path

An adapter is the only way to learn wireless associations. The contract
(`internal/adapters`):

```go
type Adapter interface {
	Name() string
	Manufacturer() string
	Capabilities() []Capability
	Probe(ctx context.Context) ([]discovery.Result, error)
	Status() Status
	Fallback() string
}
```

Rules:

1. **Capabilities are detected, not assumed.** `Capabilities()` is the claim;
   `Status().Detected` is what the device actually proved. A device that answers
   `404` for the wireless endpoint simply does not have the capability, and the
   rest of the application is unaffected.
2. **No data is not an error.** Unreachable → empty result, nil error, and
   `Fallback()` explaining the passive path.
3. **No credential guessing.** An adapter without a configured credential makes
   **zero** HTTP requests. The Xiaomi adapter's test asserts the request counter
   stays at 0.
4. **Secrets stay in memory.** `Credential` redacts itself for every `fmt` verb
   and for `encoding/json`; there is no serialisation path that writes it.

Registration lives in `internal/adapters/registry`, so a new vendor is one file
plus one `Register` call. A vendor whose factory fails cannot break another
vendor: the registry isolates the error.

## Configuration

```yaml
discovery:
  enabled: true
  interval: "10m"          # how often to re-read router state
  auto_detect_subnets: true
  max_concurrency: 16      # bounded worker pool
  subnets: []              # explicit extra CIDRs, for active scanning only
  scan_timeout: "500ms"
  rate_limit: 64           # probes per second, active scan only

collectors:
  openwrt: true            # interfaces, roles, addresses (UCI/ubus)
  arp: true
  ndp: true
  dhcp: true
  fdb: true
  lldp: false              # needs lldpd + multicast
  mdns: false              # noisy
  ssdp: false
  snmp: false
  scan: false              # active probing of subnets
```

Environment overrides use the `LANORY_` prefix, e.g.
`LANORY_DISCOVERY_INTERVAL=30m`. The Web UI writes a third layer into
`app_settings`, which wins over both.

Turning a collector off does not delete what it already discovered: the devices
stay, marked as not re-verified, and the discovery status page shows the collector
as disabled rather than failing.

## Implementation status

| Source | Implemented | Tested | Notes |
| --- | --- | --- | --- |
| openwrt interfaces | yes | fixture tests | `ubus` preferred, `uci` fallback |
| bridge FDB | yes | fixture tests | noise filtering covered |
| ARP | yes | fixture tests | — |
| NDP | yes | fixture tests | IPv6 link-local kept as evidence |
| DHCP leases | yes | fixture tests | missing file → `Supported() == false` |
| LLDP | yes, opt-in | parser tests | no real LLDP network in CI |
| mDNS | yes, opt-in | parser tests | no real multicast network in CI |
| SSDP | not implemented | — | config flag reserved |
| SNMP | not implemented | — | config flag reserved |
| active scan | partial | unit tests | not the recommended mode |
| generic adapter | yes | HTTP test server | claims no client capability |
| Xiaomi adapter | yes | fake device | **not tested against hardware** |
