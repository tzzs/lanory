# Lanory on OpenWrt 25.12 / iStoreOS 25.12.5

## Status of this directory

**The build was not executed in the environment that produced these files.** No
OpenWrt SDK, no cross toolchain and no OpenWrt device were available (the
development host is macOS/arm64). Everything here follows the OpenWrt package
conventions and has been reviewed against the upstream package skeleton, but it
is unbuilt configuration until you run the commands below on a Linux host with
the matching SDK.

What *is* verified, because it was run:

- **`make packages` produces 8 installable packages** (`.ipk` and `.apk` for
  x86_64, aarch64, armv7 and i386) with no SDK present, and
  `make package-check` re-parses every one of them against the documented
  formats, including a SHA-256 comparison of the packaged binary against the
  release binary;

- `go build ./...` and `go test ./...` for the Go code that the package installs;
- cross-compilation of the binary for `linux/amd64`, `linux/arm64`, `linux/arm`
  (v7) and `linux/386` with `CGO_ENABLED=0` (the same build the package performs),
  confirmed with `file` to be static and of the right ELF class;
- the init script parses under `sh -n` and passes `shellcheck --shell=sh`;
- `packaging/openwrt/verify.sh` (`make apk-check`) passes. It checks 40+
  invariants that do not need the SDK: init script structure, that the option
  names in the UCI file match the ones the init script reads, that every path the
  Makefile installs is the path the init script uses, that no `PKGARCH:=all` is
  declared for a compiled binary, that the conffiles are complete, the LuCI
  companion metadata, and that the token handling is coherent (see below);
- the token path end to end: with `LANORY_AUTH_TOKEN` set, an unauthenticated
  request returns 401 and the token returns 200, while `/api/v1/health` stays
  exempt.

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

Optional: `luci-app-lanory` adds a menu entry under **Services → Lanory**.

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

## Two ways to build a package

There are two routes, and for Lanory the first one is much simpler.

### A. Build the package directly — no SDK (recommended)

An OpenWrt package is not a compiled artefact. It is an archive of the
already-built binary plus a few shell scripts, UCI defaults and a YAML template.
Lanory's binary is a **fully static** Go executable, so nothing in the package
needs the target toolchain; the SDK is only needed if you want the buildroot to
compile the Go source itself.

```sh
# cross-compile every architecture we support, then package each one
make release     # dist/lanory-linux-{amd64,arm64,armv7,386}
make packages    # dist/packages/*.ipk and *.apk

# or one architecture only
python3 packaging/openwrt/mkpackage.py \
  --binary dist/lanory-linux-arm64 --version 0.1.0 --out dist/packages
```

`mkpackage.py` detects the ELF machine type and names the package with the right
OpenWrt architecture (`x86_64`, `aarch64_generic`, `arm_cortex-a7_neon-vfpv4`,
`i386_pentium4`). It emits both formats. The service package filenames are:

| Architecture | APK (OpenWrt 25.x / iStoreOS default) | IPK (older/compatibility path) |
| --- | --- | --- |
| x86_64 | `x86_64-lanory-0.1.0.apk` | `lanory_0.1.0_x86_64.ipk` |
| aarch64 | `aarch64_generic-lanory-0.1.0.apk` | `lanory_0.1.0_aarch64_generic.ipk` |
| armv7 | `arm_cortex-a7_neon-vfpv4-lanory-0.1.0.apk` | `lanory_0.1.0_arm_cortex-a7_neon-vfpv4.ipk` |
| i386 | `i386_pentium4-lanory-0.1.0.apk` | `lanory_0.1.0_i386_pentium4.ipk` |

The `.apk` files use one gzip stream containing `.PKGINFO` plus files and install
with `apk add --allow-untrusted`. The `.ipk` files use
`debian-binary` + `control.tar.gz` + `data.tar.gz` and install with `opkg
install`; keep this as the older OpenWrt/compatibility path. Both are unsigned,
which OpenWrt accepts for an explicitly installed local file. Sign them with
your own key if you intend to serve them from a feed.

`make package-check` re-parses the produced archives and verifies them against
the documented formats: the debian-binary marker, the two gzip streams, every
required control/.PKGINFO field, the conffiles list, the install scripts, file
modes, that `/var/lib/lanory` exists, and that the binary inside is byte-identical
to the release binary and of the architecture the package claims.

### B. Build inside the OpenWrt SDK

Use this when you want a buildroot-produced, feed-style package: reproducible from
source, signed by the buildroot, with the SDK's own Go toolchain compiling the
module.

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

`cp -a` copies the whole directory, `root/` included, so the package ships
`/usr/share/rpcd/acl.d/luci-app-lanory.json` (the ACL group the menu node
declares) and `/usr/share/luci/menu.d/luci-app-lanory.json`. Without the ACL
file the `luci-app-lanory` access group does not exist, `depends.acl` is
unsatisfied, and the menu entry is dropped from the tree - see the LuCI
companion's `root/` directory.

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
`apk-tools`) instead of `opkg`. For OpenWrt 25.x / iStoreOS, use the APK path
first. Route A produces both, while Route B produces whichever format the SDK is
configured for.

```sh
# APK: OpenWrt 25.x / iStoreOS default path
scp dist/packages/aarch64_generic-lanory-0.1.0.apk root@192.168.100.1:/tmp/
ssh root@192.168.100.1 'apk add --allow-untrusted /tmp/aarch64_generic-lanory-0.1.0.apk'

# IPK: older OpenWrt releases or compatibility path
scp dist/packages/lanory_0.1.0_aarch64_generic.ipk root@192.168.100.1:/tmp/
ssh root@192.168.100.1 'opkg install /tmp/lanory_0.1.0_aarch64_generic.ipk'
```

If the SDK refuses to build the Go package, check in this order:

1. `golang/host` is available in the SDK (`make menuconfig` → Languages → Go).
2. `CONFIG_GOLANG_EXTERNAL_BOOTSTRAP_ROOT` is not set to a version that cannot
   build the module's Go directive (the module asks for Go 1.26; an SDK on an
   older Go will fail with "go.mod requires go >= ..."). Raise the SDK's Go or
   lower the module directive — the second is a source change, not a packaging
   one.
3. The `GO_PKG_BUILD_PKG` path matches `cmd/lanory` in this repository.

## Signing the package

An unsigned package can only be installed with `apk add --allow-untrusted`,
which is fine for a quick test and not something to ask of a user on a router
they depend on. A signed package is verified against a public key the user
installs once, and then installs without the override.

```sh
# 1. generate a key pair (once)
make apk-keygen LANORY_KEY=~/.lanory/lanory.key

# 2. copy the public key to the router
scp ~/.lanory/lanory.key.pub root@192.168.100.1:/etc/apk/keys/lanory
#   the file name must match the key id exactly, with no extension

# 3. sign the packages
make apk-sign LANORY_KEY=~/.lanory/lanory.key
scp dist/packages/aarch64_generic-lanory-0.1.0.apk.signed root@192.168.100.1:/tmp/

# 4. install; no --allow-untrusted needed
ssh root@192.168.100.1 'apk add /tmp/aarch64_generic-lanory-0.1.0.apk.signed'
```

The private key stays on your workstation. Never put it on the router.

## Access control on first start

An installation must not be reachable unauthenticated: the API can trigger scans
and exposes every MAC, IP and hostname on the network. The init script therefore
generates a random 64-hex-character token in `/etc/lanory/token` (mode 0600) on
first start and logs it exactly once:

```
logread -e lanory | grep token
```

Paste it into the UI when prompted. The token is passed to the daemon through the
`LANORY_AUTH_TOKEN` environment variable, so it never appears in `ps`, in
`/etc/config/lanory`, or in the world readable generated YAML.

To opt out (for example when a reverse proxy authenticates requests itself):

```sh
uci set lanory.main.generate_token='0'
uci commit lanory
/etc/init.d/lanory restart
```

With no token present the service trusts private LAN addresses
(`auth.allow_lan` is set accordingly in the generated YAML) and still refuses
public source addresses.

## Installing on the device

```sh
# pick the file matching `uname -m` on the router:
#   aarch64 -> *aarch64_generic*   armv7l -> *arm_cortex-a7_neon-vfpv4*
#   x86_64  -> *x86_64*            i686   -> *i386_pentium4*
ls dist/packages/

# OpenWrt 25.x / iStoreOS: copy and install the APK (or upgrade)
scp dist/packages/aarch64_generic-lanory-0.1.0.apk root@192.168.100.1:/tmp/
ssh root@192.168.100.1 'apk add --allow-untrusted /tmp/aarch64_generic-lanory-0.1.0.apk'

# Older OpenWrt / compatibility path: use the matching IPK with opkg instead
# scp dist/packages/lanory_0.1.0_aarch64_generic.ipk root@192.168.100.1:/tmp/
# ssh root@192.168.100.1 'opkg install /tmp/lanory_0.1.0_aarch64_generic.ipk'

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
| UI loads but every request is 401 | you need the token: `logread -e lanory \| grep token` |
| forgot the token | `cat /etc/lanory/token`, or delete it and restart to generate a new one |
| service starts, UI does not load | `/etc/init.d/lanory status`; `netstat -ltnp \| grep 8787`; the listen address in UCI |
| "no devices" on a fresh install | `bridge fdb show` has entries; `ip neigh` is not empty; the FDB collector needs the `bridge` package |
| empty interface list | `ubus call network.interface dump` works; `iproute2-ip-full` is installed, not the trimmed `ip-tiny` |
| DHCP hostnames missing | `/tmp/dhcp.leases` exists and is readable |
| memory grows | lower `discovery.max_concurrency`, raise the intervals |
| 401/403 from a vendor adapter | the adapter stopped after one rejected attempt. Lanory does not retry with another password |
| no Lanory entry under Services | the ACL group is missing: `ls /usr/share/rpcd/acl.d/luci-app-lanory.json`; then clear `/tmp/luci-indexcache*` and reload uhttpd |
| LuCI logs "no Lua runtime installed" | `apk info -e luci-lua-runtime`; the controller needs it and the package declares it |
| LuCI page renders but the API calls fail | `apk info -e curl`; the controller and both templates shell out to `curl` on 127.0.0.1 |

## Unverified claims

To be explicit: this README describes the intended behaviour. The following
statements are design intent, not observed results in this repository's
environment:

- that the package **installs** on OpenWrt 25.12 / iStoreOS 25.12.5. The archives
  are structurally verified against the documented formats and contain the right
  files with the right modes, but no package manager has ever consumed them here,
  because there is no OpenWrt device in this environment. This is the single most
  important thing to check first on your hardware;
- that the SDK buildroot compiles the Go module (`route B`);
- that procd restarts the binary correctly after a crash;
- that the LuCI page renders on those LuCI versions;
- that the `luci-app-lanory` entry appears in the menu on your target. The
  ACL group, the menu node and the dependency set are all in the package and
  checked by `make package-check`, but no LuCI has rendered them here;
- that the dependency set is sufficient on a minimal image (`CONFIG_ALL=n`);
- that `luci.mk` resolves at the assumed path in your SDK tree; the Makefile
  notes the alternative `$(TOPDIR)/feeds/luci/luci.mk`.

Verify each of them on your target before shipping. `make apk-check` is the
boundary: it proves the packaging is internally consistent, not that it builds.
