# Deployment

Two deployment targets, in order of how well they fit Lanory:

1. **OpenWrt / iStoreOS on the router** — the supported production path. Lanory
   sits where the bridge is, so it sees the real ports.
2. **A generic Linux host** (systemd) — a Raspberry Pi, an x86 mini PC, a NAS.
   Works for monitoring; layer-2 detail depends on where the host sits.

- [Requirements](#requirements)
- [OpenWrt / iStoreOS](#openwrt--istoreos)
- [Generic Linux](#generic-linux)
- [Configuration reference](#configuration-reference)
- [Reverse proxy](#reverse-proxy)
- [Authentication](#authentication)
- [Backup and restore](#backup-and-restore)
- [Upgrade](#upgrade)
- [Resource usage](#resource-usage)
- [Diagnostics](#diagnostics)
- [What is verified](#what-is-verified)

## Requirements

| | Minimum | Comfortable |
| --- | --- | --- |
| CPU | armv7, i386, riscv64, ppc64le, s390x | arm64 or x86-64 |
| CPU **not supported** | **MIPS / MIPS64** | — |
| RAM | 64 MiB free | 256 MiB free |
| Disk / flash | 30 MiB binary + database | 100 MiB |
| Kernel | Linux with `/proc` and `/sys` | + `bridge` and `iproute2-ip-full` |
| Runtime deps | none (static binary) | — |

Lanory is a single static binary. It needs no Redis, Postgres, Node.js, Python or
libc beyond the kernel ABI. The frontend is embedded.

### MIPS is not supported

The storage layer is `modernc.org/sqlite`, a pure-Go translation of SQLite. It
depends on `modernc.org/libc`, which has no system-call implementation for MIPS.
The database package therefore does not compile for `linux/mips`, `linux/mipsle`,
`linux/mips64` or `linux/mips64le`:

```
$ GOOS=linux GOARCH=mips CGO_ENABLED=0 go build ./internal/database/
package github.com/lanory/lanory/internal/database
	imports modernc.org/sqlite
	imports modernc.org/libc
	imports modernc.org/libc/errno: build constraints exclude all Go files
```

Most OpenWrt devices sold today are ARM (aarch64 / armv7) or x86; those are
supported. A MIPS port needs either a cgo build linked against the target
`libsqlite3` (which OpenWrt ships as a package) or a different storage backend.
Neither exists in this tree.

Check before you install:

```sh
uname -m                 # aarch64 / armv7l / x86_64 → supported
                         # mips / mips64          → not supported
```

## OpenWrt / iStoreOS

The full procedure, including the APK build with the OpenWrt SDK, dependency
declarations and upgrade semantics, is in
**[packaging/openwrt/README.md](../packaging/openwrt/README.md)**.

Short version, on the device:

```sh
apk add --allow-untrusted /tmp/lanory-0.1.0-r1.apk
uci set lanory.main.enabled='1'
uci set lanory.main.listen='0.0.0.0'
uci set lanory.main.port='8787'
uci commit lanory
/etc/init.d/lanory enable
/etc/init.d/lanory start
logread -e lanory | tail -30
```

Then <http://192.168.100.1:8787/>.

Paths:

| Path | Content |
| --- | --- |
| `/usr/bin/lanory` | the binary |
| `/etc/config/lanory` | UCI configuration (the file to edit) |
| `/etc/lanory/config.yaml` | generated from UCI on every start |
| `/etc/lanory/token` | optional auth token, mode 0600, exported as `LANORY_AUTH_TOKEN` |
| `/var/lib/lanory/lanory.db` | SQLite database, preserved on upgrade and uninstall |

> **Status:** the package is complete but was **not built or installed** in the
> environment that produced this repository. Verify the SDK version against your
> OpenWrt 25.12 / iStoreOS 25.12.5 release before deploying.

## Generic Linux

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o lanory ./cmd/lanory
sudo install -Dm0755 lanory /usr/bin/lanory
sudo install -Dm0644 packaging/linux/lanory.service /etc/systemd/system/lanory.service
sudo install -d -o lanory -g lanory -m 0750 /var/lib/lanory
sudo install -d -o root -g lanory -m 0750 /etc/lanory
sudo install -m 0640 -g lanory packaging/linux/config.yaml /etc/lanory/config.yaml
sudo systemctl daemon-reload
sudo systemctl enable --now lanory
journalctl -u lanory -f
```

The unit grants `CAP_NET_RAW` (ICMP) and nothing else. It is sandboxed with
`ProtectSystem=strict` and `ReadWritePaths=/var/lib/lanory`, so moving the database
requires editing the unit.

Details: [packaging/linux/README.md](../packaging/linux/README.md).

## Configuration reference

Layers, lowest to highest priority:

```
built-in defaults  <  config file (YAML)  <  environment (LANORY_*)  <  app_settings (Web UI)
```

The Web UI writes to the database, which is why a change made in the browser
survives a restart even if the YAML still has the old value.

### Server

| YAML | Env | Default | Notes |
| --- | --- | --- | --- |
| `server.host` | `LANORY_SERVER_HOST` | `0.0.0.0` | use `127.0.0.1` behind a proxy |
| `server.port` | `LANORY_SERVER_PORT` | `8787` | |
| `server.base_path` | `LANORY_SERVER_BASE_PATH` | `""` | e.g. `/lanory` |
| `server.web_dir` | `LANORY_SERVER_WEB_DIR` | `""` | empty = embedded frontend |

### Database

| YAML | Env | Default |
| --- | --- | --- |
| `database.path` | `LANORY_DATABASE_PATH` | `/var/lib/lanory/lanory.db` |
| `database.retention_days` | — | `30` |

### Discovery

| YAML | Env | Default | Notes |
| --- | --- | --- | --- |
| `discovery.enabled` | `LANORY_DISCOVERY_ENABLED` | `true` | |
| `discovery.interval` | `LANORY_DISCOVERY_INTERVAL` | `10m` | Go duration |
| `discovery.auto_detect_subnets` | `LANORY_DISCOVERY_AUTO_DETECT_SUBNETS` | `true` | |
| `discovery.max_concurrency` | `LANORY_DISCOVERY_MAX_CONCURRENCY` | `16` | 1–512 |
| `discovery.subnets` | — | `[]` | explicit extra CIDRs |
| `discovery.scan_timeout` | — | `500ms` | per host, active scan |
| `discovery.rate_limit` | — | `64` | probes/second |

### Collectors

`collectors.openwrt`, `.arp`, `.ndp`, `.dhcp`, `.fdb` default to `true`;
`.lldp`, `.mdns`, `.ssdp`, `.snmp`, `.scan` default to `false`.

### Monitoring

| YAML | Env | Default |
| --- | --- | --- |
| `monitoring.enabled` | `LANORY_MONITORING_ENABLED` | `true` |
| `monitoring.interval` | `LANORY_MONITORING_INTERVAL` | `60s` |
| `monitoring.timeout` | — | `3s` |
| `monitoring.failure_threshold` | — | `3` |
| `monitoring.recover_confirm` | — | `1` |
| `monitoring.interface_refresh` | — | `30s` |

### Auth and logging

| YAML | Env | Default |
| --- | --- | --- |
| `auth.enabled` | `LANORY_AUTH_ENABLED` | `false` |
| `auth.token` | `LANORY_AUTH_TOKEN` | `""` |
| `auth.token_file` | — | `""` |
| `auth.allow_lan` | — | `true` (config default) |
| `logging.level` | `LANORY_LOG_LEVEL` | `info` |
| `logging.file` | `LANORY_LOG_FILE` | `""` (log to stderr / journal / syslog) |

### Demo

| YAML | Env | Default |
| --- | --- | --- |
| `demo.enabled` | `LANORY_DEMO_MODE` | `false` |
| `demo.seed` | — | `easepi-r1` |

A complete annotated file: `packaging/linux/config.yaml`.

## Reverse proxy

Lanory serves the SPA and the API from one origin, so a proxy only needs to
forward everything:

```nginx
location /lanory/ {
    proxy_pass         http://127.0.0.1:8787/;
    proxy_http_version 1.1;
    proxy_set_header   Host              $host;
    proxy_set_header   X-Real-IP         $remote_addr;
    proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header   X-Forwarded-Proto $scheme;

    # SSE: no buffering, no timeout, no compression
    proxy_buffering    off;
    proxy_cache        off;
    proxy_read_timeout 3600s;
    gzip               off;
}
```

Set `server.base_path: "/lanory"` so generated URLs and redirects match. If the
proxy is not on loopback, add its address to `server.trusted_proxies` — otherwise
`X-Forwarded-For` is ignored (which is the safe default).

The one thing that breaks SSE behind a proxy is buffering: if live updates never
arrive but a page reload works, the proxy is buffering `text/event-stream`.

## Authentication

Three honest options:

1. **LAN only (default).** No token, no login. Fine when the router is only
   reachable from inside; dangerous if the port is forwarded or the LAN is
   untrusted (guest WiFi sharing the same subnet).
2. **Bearer token.** `LANORY_AUTH_TOKEN=<long random value>` or a token file with
   mode 0600. Every API request needs `Authorization: Bearer <token>`; the UI
   stores it in `localStorage`. `/api/v1/health` stays public so a monitor can
   check liveness without the secret.
3. **Reverse proxy auth.** Put the UI behind an authenticated proxy (Authelia,
   Traefik forward-auth, nginx basic auth) and bind Lanory to loopback.

Generate a token:

```sh
head -c 32 /dev/urandom | base64
```

Do not reuse the router's admin password. Lanory needs a secret of its own.

## Backup and restore

The database plus its WAL is the entire state:

```sh
# stop first so the WAL is checkpointed
/etc/init.d/lanory stop          # or: systemctl stop lanory
tar czf lanory-backup-$(date +%F).tgz -C /var/lib lanory/
/etc/init.d/lanory start
```

On SQLite with WAL, copying `lanory.db` alone while the service runs can miss
committed transactions. Stop the service, or copy all three files
(`lanory.db`, `-wal`, `-shm`).

Restore:

```sh
/etc/init.d/lanory stop
rm -f /var/lib/lanory/lanory.db*
tar xzf lanory-backup-2026-10-19.tgz -C /var/lib
chown -R lanory:lanory /var/lib/lanory   # generic Linux only
/etc/init.d/lanory start
```

An alternative is a SQL dump, which is portable across schema versions:

```sh
sqlite3 /var/lib/lanory/lanory.db ".backup /tmp/lanory.backup.db"
```

## Upgrade

- **OpenWrt:** install the new APK/IPK over the old one. `/var/lib/lanory` is
  preserved; migrations run at startup.
- **Generic Linux:** replace the binary, then `systemctl restart lanory`.

Migrations are versioned and forward-only (`internal/database/migrations`). There
is no downgrade path: keep the backup you made before upgrading.

## Resource usage

Observed on the development machine with the Demo Mode dataset (13 devices, 14
edges, 53 probe results), not measured on a router:

- binary: ~14 MiB stripped (`-ldflags "-s -w"`), arm64/mips smaller;
- RSS: grows with the dataset; tens of MiB for a 30-device home network;
- CPU: idle is effectively zero between runs; a discovery run is a few hundred
  milliseconds of parsing;
- writes: one transaction per discovery run, one per probe result, WAL checkpoint
  every 1000 pages.

If the router feels slower, raise `discovery.interval` to `30m` and
`monitoring.interval` to `300s` before suspecting a bug.

## Diagnostics

```sh
curl -fsS http://127.0.0.1:8787/api/v1/health
curl -fsS http://127.0.0.1:8787/api/v1/discovery/status | jq .
curl -fsS http://127.0.0.1:8787/api/v1/discovery/collectors | jq .
```

`/discovery/collectors` is the first thing to look at when a device is missing: it
shows, per collector, whether it is supported on this host, whether it is enabled,
when it last ran, how long it took, how many records it produced and the last
error.

Logs: `logread -e lanory` on OpenWrt, `journalctl -u lanory` on systemd.

## What is verified

| Statement | Verified? |
| --- | --- |
| Go code builds and its tests pass | **yes** — run `make check` |
| the binary cross-compiles for amd64/arm64/armv7/386 | **yes** — `make release`, verified with `file` (static, correct ELF arch, frontend embedded) |
| the binary runs on the target architecture | **no** — no Linux host or router in the build environment |
| the OpenWrt APK builds | **no** — no OpenWrt SDK available |
| Demo Mode works end to end | **yes** — `internal/demo` tests + CI integration job |
| the OpenWrt package builds | **no** — no SDK available |
| the package installs on iStoreOS 25.12.5 | **no** |
| the systemd unit passes `systemd-analyze verify` | **no** |
| resource usage on a router | **no** — desktop estimate only |
| LLDP / mDNS against a real network | **no** — parser tests only |
