# Lanory on OpenWrt 25.12 / iStoreOS 25.12.5

## Status of this directory

**The build was not executed in the environment that produced these files.** No
OpenWrt SDK, no cross toolchain and no OpenWrt device were available (the
development host is macOS/arm64). Everything here follows the OpenWrt package
conventions and has been reviewed against the upstream package skeleton, but it
is unbuilt configuration until you run the commands below on a Linux host with
the matching SDK.

What *is* verified, because it was run:

- `go build ./...` and `go test ./...` for the Go code that the package installs;
- cross-compilation of the binary for `linux/amd64`, `linux/arm64`, `linux/arm`
  (v7) and `linux/386` with `CGO_ENABLED=0` (the same build the package performs);
- the init script passes `sh -n` (syntax checked with `dash`/`busybox ash`
  wherever one was available).

What is *not* verified:

- the APK/IPK actually builds under the OpenWrt 25.12 buildroot;
- the package installs and runs on a real iStoreOS 25.12.5 device;
- the LuCI pages render under the LuCI version shipped with those releases.

Treat the version numbers in `Makefile` as the target, not as a test result.

## What gets installed

| Path | Purpose |
| --- | --- |
| `/usr/bin/lanory` | single static Go binary (API + SSE + embedded frontend) |
| `/etc/init.d/lanory` | procd init script: start / stop / restart / status / enable |
| `/etc/config/lanory` | UCI configuration, the file you edit |
| `/etc/lanory/config.yaml` | generated from UCI on every start |
| `/var/lib/lanory/lanory.db` | SQLite database (WAL), preserved on upgrade *and* uninstall |
| `/usr/share/lanory/README.md` | short deployment note for the device |

Optional: `luci-app-lanory` adds a menu entry under **Network → Lanory**.

## Package names

- `lanory` — the service. This is a **new** package: it is not in the official
  `packages` feed, not in `luci`, and not in the iStoreOS feed. You build it from
  this tree.
- `luci-app-lanory` — optional LuCI companion.

## Dependencies

Runtime (declared in the `Makefile`):

| Dependency | Why |
| --- | --- |
| `iproute2-ip-full` | `ip -j link`, `ip -j addr`, `ip -j neigh` (JSON output needs the full variant) |
| `bridge` | `bridge -j fdb show` for the bridge forwarding database |
| `ubus`, `libubox`, `libjson-c` | `ubus call network.interface dump` for interface roles |
| `ca-bundle` | HTTPS to an upstream modem or a vendor management API |
| `libc` | baseline |

Deliberately **not** dependencies: `dnsmasq` (leases are read as a file when
present and the collector degrades to `Supported() == false` otherwise), `curl`,
`node`, `python`, `nftables` (only used opportunistically for a
firewall hint).

## Target architecture

The package declares `PKGARCH:=all` and `GO_PKG`, so the OpenWrt Go
infrastructure cross-compiles the binary with the target toolchain.

- no `CGO` (SQLite is `modernc.org/sqlite`, pure Go), so no target C library
  beyond libc itself;
- **supported:** `aarch64_generic`, `arm_cortex-a7`/`a9`, `x86_64`, `i386_pentium4`,
  `riscv64`, `powerpc64le`, `s390x`;
- **not supported: MIPS and MIPS64.** `modernc.org/libc` — which
  `modernc.org/sqlite` depends on — has no errno/file syscall implementation for
  MIPS, so the database layer fails to build for `mips`, `mipsle`, `mips64` and
  `mips64le`. On a MIPS router the package will not compile. This is a hard
  limitation of the storage backend, verified by cross-compiling:

  ```
  $ GOOS=linux GOARCH=mips CGO_ENABLED=0 go build ./internal/database/
  package github.com/lanory/lanory/internal/database
      imports modernc.org/sqlite
      imports modernc.org/libc
      imports modernc.org/libc/errno: build constraints exclude all Go files
  ```

  A port would need either a cgo build against the target libsqlite3 (which
  OpenWrt packages as `libsqlite3`) or a different storage backend. Neither is
  implemented.

Set `GO_PKG_TAGS:=netgo,osusergo` (already in the Makefile) so name resolution
and user lookup never need glibc.

Many popular iStoreOS / OpenWrt devices are ARM (aarch64 or armv7) or x86 and are
unaffected. **Check your target architecture before building**:

```sh
# on the device
uname -m                 # aarch64 / armv7l / x86_64 / mips
opkg print-architecture  # or: apk --print-arch
```

## Building with the OpenWrt SDK

The SDK must match the target release **and** the target architecture. For an
iStoreOS 25.12.5 device, use the iStoreOS SDK of the same release if one is
published; otherwise use the OpenWrt 25.12 SDK for the same target and verify the
result on the device.

```sh
# 1. Get the SDK for your target (example: x86/64).
wget https://downloads.openwrt.org/releases/25.12/targets/x86/64/\
openwrt-sdk-25.12-x86-64_gcc-*.Linux-x86_64.tar.zst
tar --zstd -xf openwrt-sdk-25.12-*.tar.zst
cd openwrt-sdk-25.12-*/

# 2. Add this package to the SDK feed.
mkdir -p package/lanory
cp -a /path/to/lanory/packaging/openwrt/. package/lanory/
#    The Makefile expects the repository root one level up for PKG_SOURCE.
#    For a local build, point it at your checkout instead of the git URL:
#      PKG_SOURCE_URL:=/path/to/lanory
#      PKG_SOURCE_VERSION:=<commit>

# 3. (optional) add the LuCI companion.
mkdir -p package/luci-app-lanory
cp -a /path/to/lanory/packaging/openwrt/luci-app-lanory/. package/luci-app-lanory/

# 4. Build.
./scripts/feeds update -a && ./scripts/feeds install -a
make defconfig
make package/lanory/compile V=s
make package/luci-app-lanory/compile V=s   # optional

# 5. Collect the artifact.
find bin/ -name 'lanory*.apk' -o -name 'lanory*.ipk'
```

### APK versus IPK

OpenWrt 24.10 and later default to `apk` (Alpine's package format, via
`apk-tools`) instead of `opkg`. The format is selected by the SDK, not by this
package: `make package/lanory/compile` produces whichever the SDK is configured
for. If your SDK still emits `.ipk`, install with `opkg`; if it emits `.apk`, use
`apk`. Both are listed below.

```sh
# APK (OpenWrt 24.10+, 25.12, recent iStoreOS)
apk add --allow-untrusted /tmp/lanory-0.1.0-r1.apk

# IPK (older releases)
opkg install /tmp/lanory_0.1.0-r1_*.ipk
```

If the SDK refuses to build the Go package, check in this order:

1. `golang/host` is available in the SDK (`make menuconfig` → Languages → Go).
2. `CONFIG_GOLANG_EXTERNAL_BOOTSTRAP_ROOT` is not set to a version that cannot
   build the module's Go directive (the module asks for Go 1.26; an SDK on an
   older Go will fail with "go.mod requires go >= ..."). Raise the SDK's Go or
   lower the module directive — the second is a source change, not a packaging
   one.
3. The `GO_PKG_BUILD_PKG` path matches `cmd/lanory` in this repository.

## Installing on the device

```sh
# copy the artifact over
scp bin/targets/*/packages/lanory-*.apk root@192.168.100.1:/tmp/

# install (or upgrade; /var/lib/lanory is preserved)
ssh root@192.168.100.1 'apk add --allow-untrusted /tmp/lanory-*.apk'

# edit the UCI config
ssh root@192.168.100.1 'vi /etc/config/lanory'

# start, enable, check
ssh root@192.168.100.1 '/etc/init.d/lanory enable && /etc/init.d/lanory start'
ssh root@192.168.100.1 '/etc/init.d/lanory status'
ssh root@192.168.100.1 'logread -e lanory | tail -40'

# open the UI
open http://192.168.100.1:8787/
```

`/etc/init.d/lanory` supports `start`, `stop`, `restart`, `reload`, `enable`,
`disable` and `status`. `status` checks both the procd instance and the HTTP
health endpoint.

## Upgrade and uninstall semantics

| Action | `/etc/config/lanory` | `/etc/lanory/*` | `/var/lib/lanory` |
| --- | --- | --- | --- |
| upgrade | preserved (`conffiles`) | preserved | **preserved** |
| `apk del lanory` | removed | removed | **preserved** |
| purge | removed | removed | removed by hand |

Data is never deleted by the package manager. To purge:

```sh
/etc/init.d/lanory stop
/etc/init.d/lanory disable
apk del lanory            # or: opkg remove lanory
rm -rf /var/lib/lanory    # this is the explicit purge
```

A backup of the database is a plain file copy (stop the service first so the WAL
is checkpointed):

```sh
/etc/init.d/lanory stop
cp /var/lib/lanory/lanory.db* /tmp/lanory-backup/
/etc/init.d/lanory start
```

## Logging

The init script lets procd capture stdout/stderr, so the service logs to the
system log:

```sh
logread -e lanory        # follow with: logread -f -e lanory
```

There is no log file by default. `/var/log` is tmpfs on OpenWrt and writing a
rotating log to flash shortens the life of the device.

## Flash wear

SQLite runs in WAL mode with a 1000-page autocheckpoint and `synchronous=NORMAL`.
Discovery writes are batched per run (default every 10 minutes), probe results
are written once per probe interval, and retention pruning runs on the same
schedule. If you want to reduce writes further:

- raise `discovery.interval` to `30m`;
- set `monitoring.interval` to `300s`;
- lower `database.retention_days` to `7`.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| service starts, UI does not load | `/etc/init.d/lanory status`; `netstat -ltnp \| grep 8787`; the listen address in UCI |
| "no devices" on a fresh install | `bridge fdb show` has entries; `ip neigh` is not empty; the FDB collector needs the `bridge` package |
| empty interface list | `ubus call network.interface dump` works; `iproute2-ip-full` is installed, not the trimmed `ip-tiny` |
| DHCP hostnames missing | `/tmp/dhcp.leases` exists and is readable |
| memory grows | lower `discovery.max_concurrency`, raise the intervals |
| 401/403 from a vendor adapter | the adapter stopped after one rejected attempt. Lanory does not retry with another password |

## Unverified claims

To be explicit: this README describes the intended behaviour. The following
statements are design intent, not observed results in this repository's
environment:

- that the package compiles under OpenWrt 25.12 / iStoreOS 25.12.5;
- that procd restarts the binary correctly after a crash;
- that the LuCI page renders on those LuCI versions;
- that the dependency set is sufficient on a minimal image (`CONFIG_ALL=n`).

Verify each of them on your target before shipping.
