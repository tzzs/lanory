# Security

Lanory produces a detailed, continuously updated map of a home network. That map
is sensitive: it lists every device, its address, its vendor, when it is used and
when it disappears. Treat a Lanory instance as you would treat a router's admin
page.

- [Threat model](#threat-model)
- [What Lanory exposes](#what-lanory-exposes)
- [Authentication and CSRF](#authentication-and-csrf)
- [Credential handling](#credential-handling)
- [What Lanory refuses to do](#what-lanory-refuses-to-do)
- [Outbound requests and SSRF](#outbound-requests-and-ssrf)
- [Data at rest](#data-at-rest)
- [Filesystem and process hardening](#filesystem-and-process-hardening)
- [Demo Mode](#demo-mode)
- [Reporting a vulnerability](#reporting-a-vulnerability)
- [Known gaps](#known-gaps)

## Threat model

In scope:

| Attacker | What they want | Mitigation |
| --- | --- | --- |
| Someone on the same LAN without an account | full device map, device history, trigger scans | token auth (optional, recommended), LAN-only bind |
| Guest WiFi sharing the LAN subnet | same as above | separate the guest subnet, or enable a token |
| A malicious web page open in a browser on the LAN | cross-site requests to Lanory | CSRF header requirement, no wildcard CORS |
| Someone who can read the flash | credentials, history | no secrets at rest beyond the token file; DB is not encrypted |
| A malicious device on the LAN | spoofed mDNS/FDB entries, junk devices | all inference is evidence-based with confidence; nothing is executed from network input |
| A compromised vendor router | credential theft, SSRF pivot | adapter targets are fixed by configuration; responses are treated as untrusted JSON |

Out of scope:

- an attacker with root on the router (they already have everything);
- a compromised LAN switch or AP;
- physical access to an unlocked device;
- side-channel attacks.

## What Lanory exposes

| Endpoint | Content |
| --- | --- |
| `/api/v1/overview` | counts, interface speeds, anomalies |
| `/api/v1/devices` | every device: name, hostname, MAC, IPs, vendor, type, state, first/last seen |
| `/api/v1/devices/{id}` | the above plus identities, edges, probe history, raw evidence |
| `/api/v1/topology` | the full graph |
| `/api/v1/topology/evidence` | **raw `ip`/`bridge`/`dhcp` output** for every edge |
| `/api/v1/events` | history of who came online, who went offline, when |
| `/api/v1/monitoring/history` | latency and loss per device over time |
| `/api/v1/settings` | configuration, including adapter targets (not secrets) |
| `/api/v1/events/stream` | the same data live |

The raw evidence endpoint is the most revealing: it can contain MAC addresses,
hostnames and DHCP client identifiers. It exists because the product's core claim
is explainability — but it is another reason not to expose the UI beyond the LAN.

## Authentication and CSRF

### Token

`auth.enabled: true` requires `Authorization: Bearer <token>` on every endpoint
except `/api/v1/health` (so a monitor can check liveness) and the SSE stream, which
accepts the token as a query parameter because `EventSource` cannot set headers.

Generate a strong token:

```sh
head -c 32 /dev/urandom | base64
```

Store it in a file with mode 0600 rather than in the YAML, so a configuration
backup does not leak it:

```sh
install -m 0600 /dev/null /etc/lanory/token
head -c 32 /dev/urandom | base64 > /etc/lanory/token
```

The OpenWrt init script exports it as `LANORY_AUTH_TOKEN` so it never appears in
`ps` arguments.

If `auth.enabled` is true and no token is configured, Lanory generates one at
startup and logs it **once**. That is a convenience for a first run, not a
configuration to keep.

### LAN exemption

`auth.allow_lan: true` exempts requests from RFC1918 source addresses. This is
convenient and it is exactly as strong as your LAN: if guest devices share the
subnet, they are exempt too. Prefer a token when the LAN is not fully trusted.

### CSRF

Every mutating request (`POST`, `PATCH`, `DELETE`) must carry
`X-Requested-With: XMLHttpRequest`. Browsers do not add that header to a
cross-origin form post or a simple `fetch`, and a cross-origin request that sets a
custom header triggers a preflight that Lanory does not answer permissively. The
combination of the required header and no wildcard CORS means a malicious page
cannot drive the API, even with a token in `localStorage`.

### Transport

Lanory serves plain HTTP. On a LAN that is normal; across the Internet it is not
acceptable. Put it behind a TLS-terminating reverse proxy, or reach it over a VPN.
Do not port-forward the UI.

## Credential handling

Adapter credentials (a Xiaomi router's token, a generic router's basic-auth pair)
are the only secrets Lanory holds besides its own auth token.

Rules, each enforced by a test in `internal/adapters`:

1. **Memory only.** `adapters.Credential` has unexported fields and no
   serialisation method that emits the secret. The credential store is a plain
   `map` in the process; nothing writes it to the database or to disk.
2. **Redacted everywhere.** `String`, `GoString`, `Format`, `MarshalJSON` and
   `Redacted` all print `<redacted>`. `%v`, `%+v`, `%#v`, `%q`, `%s` and
   `json.Marshal` are all covered by `TestCredentialRedaction`.
3. **One accessor.** `Secret()` is the only way to read it, and the adapters call
   it at the exact point of building an authentication header.
4. **Only to the target.** A credential is sent only to the configured
   `address:port`. There is no redirect following (`http.ErrUseLastResponse`), so a
   device cannot bounce the request — and the header — to another host.
5. **Never over plain HTTP** to a non-loopback address unless
   `allow_insecure: true` is set explicitly. The Xiaomi adapter refuses with a
   reason instead of silently downgrading.
6. **Never logged.** Errors from the HTTP client are sanitised before they reach a
   log line, so a URL in the error text cannot carry a token.

## What Lanory refuses to do

These are product requirements, implemented and tested:

- **Never try default or common passwords.** There is no password list in the
  source. An adapter with no configured credential makes **zero** HTTP requests
  (`TestZeroRequestsWithoutCredentials` asserts the counter stays at 0).
- **Never retry a rejected credential.** A `401`/`403` stops the probe immediately
  with an explanatory status; retrying with a different password would be guessing
  (`TestCredentialRejectionStopsImmediately`).
- **Never attempt to bypass authentication.** No forced-browsing, no path
  traversal, no known CVE exploitation, no protocol downgrade.
- **Never modify the router.** No writes to `/etc/config/*`, no `uci set`, no
  `nft`/`iptables` mutation, no DHCP reservation, no route change.
- **Never scan without authorisation.** Active scanning is disabled by default; it
  only targets subnets listed in `discovery.subnets` or derived from configured
  LAN interfaces, and each target is validated against the SSRF guard.
- **Never phone home.** There is no telemetry endpoint, no update check, no
  analytics. The only outbound connections are probes you configured and adapters
  you enabled.

## Outbound requests and SSRF

Three features make Lanory open an outbound connection:

1. **Device probes** — to an address that belongs to a discovered device.
2. **Network probes** — to a user-configured target.
3. **Router adapters** — to a user-configured management address.

The SSRF guard (`internal/api/ssrf.go`) validates every target:

- a network probe target must be inside a subnet the user authorised
  (`discovery.subnets` or a detected LAN interface), or be a public address when
  the intent is an Internet check;
- loopback, link-local, multicast and unspecified addresses are rejected unless
  the target is explicitly a LAN device;
- a device probe derives its target from the stored device address, which is
  already inside the discovered network;
- hostnames are resolved once and the resolved address is checked, so a DNS
  record cannot be used to pivot to an internal service.

The goal is that a user who can create a probe cannot use Lanory to reach a
service they could not reach themselves.

## Data at rest

| Data | Where | Protection |
| --- | --- | --- |
| devices, history, evidence | SQLite file | filesystem permissions (0750 dir, 0600-ish file as created) |
| auth token | `/etc/lanory/token` | mode 0600, owned by the service account |
| adapter credentials | process memory | none needed: never written |
| configuration | YAML | 0644 by default; **do not put secrets in it** |

The database is **not encrypted**. On a router, flash is physically accessible to
anyone with the device. If that matters to you, put the data directory on an
encrypted volume, or accept that a device inventory is not a secret worth
encrypting a router's flash for.

Retention limits exposure over time: `database.retention_days` prunes events,
probe results and raw evidence (default 30 days). The device list itself is kept
indefinitely, because deleting a device the user renamed would be worse.

## Filesystem and process hardening

**systemd** (`packaging/linux/lanory.service`):

```ini
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/lanory
PrivateTmp=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_NETLINK AF_UNIX
SystemCallFilter=@system-service
CapabilityBoundingSet=CAP_NET_RAW CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_RAW
MemoryMax=256M
```

`CAP_NET_ADMIN` is deliberately **not** granted: Lanory must not be able to change
an interface.

**OpenWrt**: procd runs the binary as root because that is how procd works. The
mitigation is that the binary contains no code path that writes network
configuration; the package has no `setcap` step and no `sudo`. If you want
unprivileged operation, run it under `runit`/`s6` as a dedicated user with
`CAP_NET_RAW`.

**Docker**: `read_only: true`, `cap_drop: ALL`, `cap_add: NET_RAW`,
`no-new-privileges`, nonroot distroless base, one writable volume.

## Demo Mode

Demo Mode is a security-relevant feature precisely because it is the easiest way
to mislead a user. The rule is simple and enforced:

**Every row Demo Mode writes carries `model.SourceDemo`**, and
`/api/v1/health` plus `/api/v1/overview` report `demo_mode: true`. The UI must
show a banner and badge devices when that flag is set. The CI integration job runs
the binary in Demo Mode and asserts the flag and the source on every device and
edge (`scripts/ci/assert_demo_mode.py`).

Demo data also never contains an address from a real network: it is derived from
`fixtures/easepi-r1/`, the sanitized snapshot. `fixtures/private/` is git-ignored
and must never be read, copied or referenced.

## Reporting a vulnerability

Please do not open a public issue for a security problem. Use GitHub's private
security advisory for the repository, or contact the maintainer listed in the
commit history. Include:

- the version or commit,
- the deployment (OpenWrt / Linux / Docker),
- whether authentication was enabled,
- a reproduction that does not contain your real MAC or IP addresses.

Expect an acknowledgement within a week. This is a volunteer project; there is no
bug bounty.

## Known gaps

Being explicit about what is missing is part of the security posture:

| Gap | Impact | Workaround |
| --- | --- | --- |
| no rate limiting on the API | a LAN client can hammer the service | reverse proxy rate limit |
| no account system, one shared token | no per-user audit | reverse proxy with per-user auth |
| no login page; the token lives in `localStorage` | XSS in the UI would expose it | there is no user-generated HTML; the token is only used same-origin |
| no encryption at rest | physical access reads the inventory | encrypted volume |
| SSE token in the query string | may appear in proxy access logs | use `Authorization` where the client supports it, or a proxy that does not log query strings |
| no audit log of who changed what | a shared token cannot attribute changes | reverse proxy logs |
| Demo Mode can be enabled on a real deployment | a user could confuse simulated with real data | the flag is always reported; the UI banner is mandatory |
| no automatic security updates | a router may run an old build | subscribe to releases; the package has no updater by design |
