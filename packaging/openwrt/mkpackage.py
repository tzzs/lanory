#!/usr/bin/env python3
"""Build installable OpenWrt packages from a cross-compiled Lanory binary.

Why this exists
---------------
An OpenWrt package is not a compiled artefact. It is an archive of files: the
already-built binary plus a handful of shell scripts, UCI defaults and a YAML
template. Lanory's binary is a fully static Go executable, so the "target
toolchain" that the SDK would supply is not needed to produce an installable
package at all - it is only needed if you want the buildroot to compile the Go
source itself, which is a reproducibility convention rather than a requirement.

This script therefore produces the same two package formats the buildroot would,
using only Python's standard library:

  .ipk  opkg format: three concatenated gzip streams
        (debian-binary, control.tar.gz, data.tar.gz)
  .apk  apk-tools v2 format: a single tar with .PKGINFO followed by the files

Both are unsigned. OpenWrt accepts an unsigned package that is installed
explicitly (`apk add --allow-untrusted <file>` or `opkg install <file>`). For a
package that will be distributed through a feed, sign it with your own key - see
packaging/openwrt/README.md.

Usage:
    python3 packaging/openwrt/mkpackage.py \
        --binary dist/lanory-linux-arm64 \
        --arch aarch64_generic \
        --version 0.1.0 \
        --out dist/

    # both formats, all architectures we cross-compile
    python3 packaging/openwrt/mkpackage.py --binary-dir dist --all --out dist/packages
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import os
import shutil
import stat
import sys
import tarfile
import time

# ---------------------------------------------------------------------------
# Package metadata
# ---------------------------------------------------------------------------

PKG_NAME = "lanory"
PKG_MAINTAINER = "Lanory contributors"
PKG_DESCRIPTION = (
    "Lanory (\u7f51\u7ed8) is a self-hosted home network topology visualisation "
    "and monitoring tool for OpenWrt and iStoreOS. It discovers devices from the "
    "router's own bridge FDB, ARP/NDP neighbours and DHCP leases, draws an "
    "interactive topology, and monitors reachability over time. "
    "Read-only: it never modifies WAN/LAN/DHCP/firewall/DNS/routes."
)
PKG_URL = "https://github.com/lanory/lanory"
PKG_LICENSE = "MIT"

# Runtime dependencies, verified against a real iStoreOS 25.12.5 package database
# rather than guessed. The two package managers use different names for the same
# things, and OpenWrt's iproute2 splits into separate packages:
#
#   opkg (.ipk)            apk (.apk)              what it provides
#   libc                   libc                    the C library
#   iproute2-ip-full       ip-full                 `ip -j link/addr/neigh`
#   bridge                 ip-bridge               `bridge -j fdb show`
#   ubus                   ubus                    `ubus call network.interface dump`
#   libubox                (pulled in by ubus)     blobmsg helpers
#   libjson-c              libjson-c5              JSON parsing
#   ca-bundle              ca-bundle               HTTPS to a vendor API
#
# libubox and libjson-c5 carry an ABI date suffix in their package name
# (libubox20260213), so the apk dependency list does not pin them directly:
# ubus already depends on the right one, and hard-coding a date would make the
# package refuse to install the moment iStoreOS bumps the ABI.
DEPENDS_IPK = "libc,iproute2-ip-full,bridge,ubus,libjson-c,ca-bundle"
DEPENDS_APK = "libc ip-full ip-bridge ubus ca-bundle"

# go GOARCH/GOARM -> OpenWrt package architecture name
ARCH_MAP = {
    ("amd64", ""): "x86_64",
    ("386", ""): "i386_pentium4",
    ("arm64", ""): "aarch64_generic",
    ("arm", "7"): "arm_cortex-a7_neon-vfpv4",
    ("arm", "6"): "arm_cortex-a5_neon-vfpv4",
    ("riscv64", ""): "riscv64_riscv64",
}

HERE = os.path.dirname(os.path.abspath(__file__))
FILES_DIR = os.path.join(HERE, "files")


# ---------------------------------------------------------------------------
# File tree
# ---------------------------------------------------------------------------


def package_tree(binary: str) -> list[tuple[str, int, bytes]]:
    """Return the (path, mode, contents) list that goes into the package.

    Paths are absolute on the target (e.g. /usr/bin/lanory); the archive stores
    them without the leading slash, which is what both package formats expect.
    """
    with open(binary, "rb") as fh:
        binary_data = fh.read()

    def read(name: str) -> bytes:
        with open(os.path.join(FILES_DIR, name), "rb") as fh:
            return fh.read()

    return [
        ("/usr/bin/lanory", 0o755, binary_data),
        ("/etc/init.d/lanory", 0o755, read("lanory.init")),
        ("/etc/config/lanory", 0o600, read("lanory.config")),
        ("/etc/lanory/config.yaml", 0o600, read("config.yaml")),
        ("/etc/uci-defaults/99-lanory", 0o755, read("99-lanory")),
        ("/usr/share/lanory/README.md", 0o644, read("README.deploy.md")),
    ]


# Directories that must exist in the data archive even when empty.
EXTRA_DIRS = ["/var/lib/lanory"]

# Files whose edits must survive an upgrade.
CONFFILES = ["/etc/config/lanory", "/etc/lanory/config.yaml"]


# ---------------------------------------------------------------------------
# Tar helpers
# ---------------------------------------------------------------------------


def add_bytes(tar: tarfile.TarInfo, data: bytes) -> io.BytesIO:
    tar.size = len(data)
    return io.BytesIO(data)


def tar_open(name: str, mode: str) -> tarfile.TarFile:
    return tarfile.open(name, mode, format=tarfile.GNU_FORMAT)


def add_file(tar: tarfile.TarFile, path: str, mode: int, data: bytes | None = None, mtime: float | None = None) -> None:
    """Add one regular file to the archive at the given absolute target path."""
    arcname = path.lstrip("/")
    info = tarfile.TarInfo(arcname)
    info.mode = mode
    info.mtime = int(mtime or time.time())
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    if data is None:
        info.type = tarfile.DIRTYPE
        info.mode = 0o755
        tar.addfile(info)
    else:
        info.size = len(data)
        tar.addfile(info, io.BytesIO(data))


def add_dir(tar: tarfile.TarFile, path: str, mtime: float | None = None) -> None:
    """Add a single directory entry (not its parents)."""
    arcname = path.lstrip("/").rstrip("/")
    if not arcname:
        return
    info = tarfile.TarInfo(arcname)
    info.type = tarfile.DIRTYPE
    info.mode = 0o755
    info.mtime = int(mtime or time.time())
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    tar.addfile(info)


def parent_dirs(path: str) -> list[str]:
    """Every ancestor directory of an absolute path, outermost first."""
    parts = path.strip("/").split("/")[:-1]
    out = []
    for i in range(len(parts)):
        out.append("/" + "/".join(parts[: i + 1]))
    return out


# ---------------------------------------------------------------------------
# .ipk (opkg)
# ---------------------------------------------------------------------------


def control_file(version: str, arch: str) -> str:
    # The Installed-Size field is in KiB and used by opkg for space checks.
    return (
        f"Package: {PKG_NAME}\n"
        f"Version: {version}\n"
        f"Depends: {DEPENDS_IPK}\n"
        f"Provides: {PKG_NAME}\n"
        f"Section: net\n"
        f"Priority: optional\n"
        f"Architecture: {arch}\n"
        f"Installed-Size: {installed_kib()}\n"
        f"Maintainer: {PKG_MAINTAINER}\n"
        f"Source: {PKG_URL}\n"
        f"SourceName: {PKG_NAME}\n"
        f"License: {PKG_LICENSE}\n"
        f"Description: {PKG_DESCRIPTION}\n"
    )


def postinst_script() -> bytes:
    return b"""#!/bin/sh
# Runs after the package files are unpacked. Enables and starts the service,
# but only when this is a real install (not an image build).
[ -n "$IPKG_INSTROOT" ] && exit 0

# Seed a default UCI section without touching a user edit.
if ! uci -q get lanory.main >/dev/null 2>&1; then
	uci -q batch <<-EOF
		set lanory.main=lanory
		set lanory.main.enabled='1'
		set lanory.main.listen='0.0.0.0'
		set lanory.main.port='8787'
		set lanory.main.data_dir='/var/lib/lanory'
		set lanory.main.generate_config='1'
		set lanory.main.generate_token='1'
		commit lanory
	EOF
fi

mkdir -p /var/lib/lanory
chmod 750 /var/lib/lanory

/etc/init.d/lanory enable
# uci-defaults would normally run this; do it here as well so a package
# installed with --force-* still gets the runtime directory.
[ -x /etc/uci-defaults/99-lanory ] && /etc/uci-defaults/99-lanory

/etc/init.d/lanory restart >/dev/null 2>&1

echo "Lanory installed. The access token is in /etc/lanory/token:"
echo "  cat /etc/lanory/token"
exit 0
"""


def prerm_script() -> bytes:
    return b"""#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
/etc/init.d/lanory stop >/dev/null 2>&1
/etc/init.d/lanory disable >/dev/null 2>&1
exit 0
"""


def postrm_script() -> bytes:
    return b"""#!/bin/sh
[ -n "$IPKG_INSTROOT" ] && exit 0
# /var/lib/lanory is intentionally preserved: an uninstall must not destroy the
# user's history. Remove it by hand to purge.
if [ -d /var/lib/lanory ]; then
	echo "Lanory data kept in /var/lib/lanory (remove it manually to purge)."
fi
exit 0
"""


def conffiles_content() -> bytes:
    return ("\n".join(CONFFILES) + "\n").encode()


def installed_kib() -> int:
    """Size of the installed tree in KiB, for the Installed-Size field."""
    total = 0
    for path, _mode, data in package_tree_meta():
        total += len(data)
    return max(1, total // 1024)


_META_CACHE: list[tuple[str, int, bytes]] | None = None


def package_tree_meta() -> list[tuple[str, int, bytes]]:
    """File list with contents replaced by a placeholder of the right length.

    Used for the size calculation before the real binary is read, so building a
    package does not need to load 13 MB into memory twice.
    """
    global _META_CACHE
    if _META_CACHE is not None:
        return _META_CACHE
    out = []
    sizes = {
        "/usr/bin/lanory": _BINARY_SIZE,
        "/etc/init.d/lanory": os.path.getsize(os.path.join(FILES_DIR, "lanory.init")),
        "/etc/config/lanory": os.path.getsize(os.path.join(FILES_DIR, "lanory.config")),
        "/etc/lanory/config.yaml": os.path.getsize(os.path.join(FILES_DIR, "config.yaml")),
        "/etc/uci-defaults/99-lanory": os.path.getsize(os.path.join(FILES_DIR, "99-lanory")),
        "/usr/share/lanory/README.md": os.path.getsize(os.path.join(FILES_DIR, "README.deploy.md")),
    }
    for path, size in sizes.items():
        out.append((path, 0o755, b"\0" * size))
    _META_CACHE = out
    return out


_BINARY_SIZE = 0


def gzip_bytes(data: bytes) -> bytes:
    buf = io.BytesIO()
    # mtime=0 keeps the output byte-identical across runs, which makes the
    # package hash reproducible.
    with gzip.GzipFile(fileobj=buf, mode="wb", mtime=0) as gz:
        gz.write(data)
    return buf.getvalue()


def build_ipk(binary: str, version: str, arch: str, out_dir: str) -> str:
    files = package_tree(binary) + [(d, 0o755, None) for d in EXTRA_DIRS]  # type: ignore[list-item]

    # --- control.tar.gz ---
    cbuf = io.BytesIO()
    with tarfile.open(fileobj=cbuf, mode="w", format=tarfile.GNU_FORMAT) as tar:
        add_file(tar, "/control", 0o644, control_file(version, arch).encode())
        add_file(tar, "/conffiles", 0o644, conffiles_content())
        add_file(tar, "/postinst", 0o755, postinst_script())
        add_file(tar, "/prerm", 0o755, prerm_script())
        add_file(tar, "/postrm", 0o755, postrm_script())
    control_gz = gzip_bytes(cbuf.getvalue())

    # --- data.tar.gz ---
    dbuf = io.BytesIO()
    with tarfile.open(fileobj=dbuf, mode="w", format=tarfile.GNU_FORMAT) as tar:
        seen: set[str] = set()
        for path, mode, data in files:
            for parent in parent_dirs(path):
                if parent not in seen:
                    add_dir(tar, parent)
                    seen.add(parent)
            if data is None:
                if path not in seen:
                    add_dir(tar, path)
                    seen.add(path)
            else:
                add_file(tar, path, mode, data)
    data_gz = gzip_bytes(dbuf.getvalue())

    # --- concatenation ---
    name = f"{PKG_NAME}_{version}_{arch}.ipk"
    dest = os.path.join(out_dir, name)
    with open(dest, "wb") as out:
        out.write(b"2.0\n")  # debian-binary
        out.write(control_gz)
        out.write(data_gz)
    return dest


# ---------------------------------------------------------------------------
# .apk (apk-tools v2)
#
# A v2 apk is three concatenated gzip streams forming one tar:
#   signatures (optional) | control (.PKGINFO + scripts) | data
#
# Two details make the difference between "a tarball" and "an apk that apk-tools
# actually accepts", and both are easy to get wrong:
#
#  1. .PKGINFO must carry `datahash = <sha256>`, the digest of the *compressed
#     data gzip stream* exactly as it appears in the file. Without it apk-tools
#     cannot verify the data section and rejects the package outright.
#  2. every regular file in the data section needs an `APK-TOOLS.checksum.SHA1`
#     pax extended header. apk-tools uses these to detect modification and, on
#     install, to decide whether a file it already has is up to date.
#
# apk-tools 3.x still reads v2 packages (src/extract_v2.c), which is why this
# format is used rather than the binary adb/v3 layout: v2 is a documented tar
# layout that a plain Python implementation can produce correctly, whereas v3 is
# a bespoke binary object graph whose encoder would have to be byte-compatible
# with apk's C structs.
# ---------------------------------------------------------------------------


def pkginfo(version: str, arch: str, datahash: str, size: int) -> str:
    """The .PKGINFO metadata file inside a v2 apk."""
    lines = [
        "# Generated by packaging/openwrt/mkpackage.py",
        f"pkgname = {PKG_NAME}",
        f"pkgver = {version}",
        f"pkgdesc = {PKG_DESCRIPTION}",
        f"url = {PKG_URL}",
        f"builddate = {int(time.time())}",
        f"packager = {PKG_MAINTAINER}",
        f"size = {size}",
        f"arch = {arch}",
        f"origin = {PKG_NAME}",
        f"license = {PKG_LICENSE}",
        # The digest of the data stream as it appears in this file. apk-tools
        # refuses a v2 package without it.
        f"datahash = {datahash}",
    ]
    for dep in DEPENDS_APK.split():
        lines.append(f"depend = {dep}")
    return "\n".join(lines) + "\n"


def installed_bytes() -> int:
    return sum(len(data) for _p, _m, data in package_tree_meta())


def _pax_checksum_record(data: bytes) -> tuple[str, str]:
    """The pax record name/value apk-tools looks for."""
    return ("APK-TOOLS.checksum.SHA1", hashlib.sha1(data).hexdigest())


def _tar_add_with_pax(tar: tarfile.TarFile, path: str, mode: int, data: bytes, mtime=None) -> None:
    """Add a regular file with the APK-TOOLS.checksum pax header apk expects."""
    name, value = _pax_checksum_record(data)
    info = tarfile.TarInfo(path.lstrip("/"))
    info.mode = mode
    info.size = len(data)
    info.mtime = int(mtime or time.time())
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    # tarfile writes any pax_headers as an extended header before the entry.
    info.pax_headers = {name: value}
    tar.addfile(info, io.BytesIO(data))


def _ustar_is_acceptable(tar_bytes: bytes) -> bool:
    """apk's tar parser requires the magic to be exactly "ustar\\0".

    Python's PAX_FORMAT writes the GNU flavour "ustar  \\0" (magic + version),
    which apk rejects with -APKE_V2PKG_FORMAT. Post-processing the magic is far
    simpler and safer than reimplementing a tar writer: byte 257 must be
    'u','s','t','a','r','\\0' and the version bytes zeroed.
    """
    return tar_bytes[257:263] == b"ustar\x00"


def _normalise_ustar_magic(tar_bytes: bytes) -> bytes:
    """Rewrite every GNU tar header magic to the ustar\0 form apk requires."""
    out = bytearray(tar_bytes)
    off = 0
    while off + 512 <= len(out):
        header = out[off : off + 512]
        if header[:1] == b"\x00":
            # First zero block of the two-block end marker.
            break
        if bytes(header[257:262]) == b"ustar":
            out[off + 257 : off + 265] = b"ustar\x00" + b"00"
        size_field = bytes(out[off + 124 : off + 136]).rstrip(b"\x00 ")
        try:
            size = int(size_field, 8) if size_field else 0
        except ValueError:
            size = 0
        off += 512 + ((size + 511) // 512) * 512
    return bytes(out)


def _tar_entry(name: str, data: bytes, mode: int, is_dir: bool = False) -> tuple[tarfile.TarInfo, io.BytesIO | None]:
    """A single tar entry.

    apk decides whether an entry is a directory by testing S_ISDIR on the mode
    from the tar header, not by the typeflag alone. A directory entry whose mode
    lacks S_IFDIR is therefore treated as a *file* and then rejected with
    "no dirent in archive", because no directory was registered for it. Setting
    S_IFDIR explicitly is what makes apk accept the tree.
    """
    info = tarfile.TarInfo(name)
    info.mtime = 0  # deterministic output
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    if is_dir:
        info.type = tarfile.DIRTYPE
        info.mode = stat.S_IFDIR | (mode or 0o755)
        return info, None
    info.mode = stat.S_IFREG | mode
    info.size = len(data)
    info.pax_headers = {"APK-TOOLS.checksum.SHA1": hashlib.sha1(data).hexdigest()}
    return info, io.BytesIO(data)


def _tar_bytes(entries: list[tuple[tarfile.TarInfo, io.BytesIO | None]], terminate: bool) -> bytes:
    """Serialise entries into tar bytes.

    `terminate` controls whether the two-block end-of-archive marker is written.
    A v2 apk is ONE logical tar split across gzip members, so only the very last
    member may carry the marker; putting it after the control section makes
    apk-tools stop early and report "unexpected end of file".
    """
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.PAX_FORMAT) as tar:
        for info, fobj in entries:
            tar.addfile(info, fobj)
    raw = buf.getvalue()
    raw = _normalise_ustar_magic(raw)
    if not terminate:
        # Strip tarfile's trailing zero blocks so the next member continues the
        # same archive.
        raw = raw.rstrip(b"\x00")
        raw += b"\x00" * _padding_to_block(len(raw))
    return raw


def _padding_to_block(n: int) -> int:
    return (512 - (n % 512)) % 512


def build_apk(binary: str, version: str, arch: str, out_dir: str) -> str:
    """Build a v2 apk: ONE tar, split into a control member and a data member.

    Layout, per apk-v2(5):
      gzip member 1: .PKGINFO + install scripts        (control)
      gzip member 2: the package files                 (data, ends the archive)

    .PKGINFO must carry `datahash`, the SHA-256 of the *compressed* data member
    exactly as written, so the control member has to be built after the data
    member is known.
    """
    files = package_tree(binary)

    # --- data member content -------------------------------------------------
    data_entries: list[tuple[tarfile.TarInfo, io.BytesIO | None]] = []
    seen: set[str] = set()
    for path, mode, data in files:
        for parent in parent_dirs(path):
            key = parent + "/"
            if key in seen:
                continue
            seen.add(key)
            info, fobj = _tar_entry(key.lstrip("/"), None, 0o755, is_dir=True)
            data_entries.append((info, fobj))
        info, fobj = _tar_entry(path.lstrip("/"), data, mode)
        data_entries.append((info, fobj))
    for d in EXTRA_DIRS:
        for parent in parent_dirs(d) + [d]:
            key = parent + "/"
            if key in seen:
                continue
            seen.add(key)
            info, fobj = _tar_entry(key.lstrip("/"), None, 0o755, is_dir=True)
            data_entries.append((info, fobj))

    # The archive ends here, so this is the only member with the end marker.
    data_raw = _tar_bytes(data_entries, terminate=True)
    data_gz = gzip_bytes(data_raw)
    datahash = hashlib.sha256(data_gz).hexdigest()

    # --- control member content ---------------------------------------------
    control_entries: list[tuple[tarfile.TarInfo, io.BytesIO | None]] = []
    info_k, _ = _tar_entry(".PKGINFO", pkginfo(version, arch, datahash, installed_bytes()).encode(), 0o644)
    control_entries.append((info_k, io.BytesIO(
        pkginfo(version, arch, datahash, installed_bytes()).encode())))
    for script_name, script_body in (
        (".post-install", postinst_script()),
        (".pre-deinstall", prerm_script()),
        (".post-deinstall", postrm_script()),
    ):
        info, fobj = _tar_entry(script_name, script_body, 0o755)
        control_entries.append((info, fobj))

    control_raw = _tar_bytes(control_entries, terminate=False)
    control_gz = gzip_bytes(control_raw)

    name = f"{PKG_NAME}-{version}.apk"
    dest = os.path.join(out_dir, f"{arch}-{name}")
    with open(dest, "wb") as out:
        out.write(control_gz)
        out.write(data_gz)
    return dest


# ---------------------------------------------------------------------------
# LuCI companion package
# ---------------------------------------------------------------------------

# The LuCI integration is pure Lua, so it needs no compiler and no SDK: the
# package is an archive of the same shape as the service package.
LUCI_ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "luci-app-lanory")

# Where each source file goes on the router.
LUCI_LAYOUT = [
    ("luasrc/controller/lanory.lua", "usr/lib/lua/luci/controller/lanory.lua", 0o644),
    ("luasrc/model/cbi/lanory.lua", "usr/lib/lua/luci/model/cbi/lanory.lua", 0o644),
    ("luasrc/view/lanory/overview.htm", "usr/lib/lua/luci/view/lanory/overview.htm", 0o644),
    ("luasrc/view/lanory/devices.htm", "usr/lib/lua/luci/view/lanory/devices.htm", 0o644),
]


def build_luci_apk(version: str, out_dir: str) -> str:
    """Build the luci-app-lanory package.

    The payload is the LuCI sources, and the install script adds the menu entry
    by clearing the LuCI index cache. The service package is a dependency, but
    the dependency is expressed in the package metadata rather than forced here,
    so the LuCI package can also be built and inspected on its own.
    """
    entries = []
    for src, dst, mode in LUCI_LAYOUT:
        path = os.path.join(LUCI_ROOT, src)
        if not os.path.exists(path):
            raise SystemExit(f"missing LuCI source: {path}")
        with open(path, "rb") as fh:
            entries.append((dst, mode, fh.read()))

    # The install script: the menu entry only appears once LuCI has rebuilt its
    # index, and the index is cached in /tmp.
    postinst = b"""#!/bin/sh
[ -n "$IPKG_INSTROOT" ] || {
	# The LuCI menu index is cached; drop it so the entry appears.
	rm -f /tmp/luci-indexcache* 2>/dev/null
	rm -rf /tmp/luci-modulecache/* 2>/dev/null
	[ -x /etc/init.d/uhttpd ] && /etc/init.d/uhttpd reload >/dev/null 2>&1
}
exit 0
"""
    prerm = b"""#!/bin/sh
[ -n "$IPKG_INSTROOT" ] || {
	rm -f /tmp/luci-indexcache* 2>/dev/null
	rm -rf /tmp/luci-modulecache/* 2>/dev/null
}
exit 0
"""
    conffiles = b"/etc/config/lanory\n"

    # The metadata file is .PKGINFO, not the opkg "control".
    #
    # apk-tools reads a v2 package by looking for a member literally named
    # ".PKGINFO" (src/extract_v2.c). A package that carries the opkg-style
    # "control" member is read as having no metadata at all, and apk refuses it
    # with "file format not supported (in this applet)" - which is exactly what
    # happened to the first attempt at this package. The .ipk format, which does
    # use "control", is a different format for a different package manager.
    pkginfo = (
        f"# Generated by packaging/openwrt/mkpackage.py\n"
        f"pkgname = luci-app-lanory\n"
        f"pkgver = {version}\n"
        f"pkgdesc = LuCI menu and status pages for Lanory.\n"
        f"url = https://github.com/lanory/lanory\n"
        f"builddate = {int(time.time())}\n"
        f"packager = Lanory contributors\n"
        f"size = {sum(len(d) for _dst, _mode, d in entries)}\n"
        f"arch = noarch\n"
        f"origin = luci-app-lanory\n"
        f"license = MIT\n"
        f"depend = luci-base\n"
        f"depend = lanory\n"
    )

    # Build the control and data members.
    #
    # The same rules as the service package apply, because apk-tools reads every
    # package the same way:
    #   - the tar magic must be "ustar\0" + "00", not the GNU flavour Python
    #     writes by default (a mismatch is rejected with "file format not
    #     supported");
    #   - every file carries an APK-TOOLS.checksum.SHA1 pax record;
    #   - the control member has no end-of-archive marker, only the data member
    #     ends the archive.
    control_entries = [
        _entry(".PKGINFO", 0o644, pkginfo.encode()),
        _entry(".post-install", 0o755, postinst),
        _entry(".pre-deinstall", 0o755, prerm),
    ]
    # Directory entries are required: apk classifies an entry by testing
    # S_ISDIR on the mode from the tar header, and rejects a file whose directory
    # was never registered with "no dirent in archive".
    data_entries = []
    seen_dirs = set()
    for dst, mode, data in entries:
        parts = dst.split("/")[:-1]
        for i in range(len(parts)):
            d = "/".join(parts[: i + 1]) + "/"
            if d not in seen_dirs:
                seen_dirs.add(d)
                data_entries.append(_dir_entry(d, 0o755))
        data_entries.append(_entry(dst, mode, data))

    # The data member is built first: .PKGINFO must carry the digest of the
    # compressed data member, so the control member cannot be written before it.
    data_raw = _normalise_ustar_magic(_tar_bytes(data_entries, terminate=True))
    data_gz = gzip_bytes(data_raw)

    pkginfo += f"datahash = {hashlib.sha256(data_gz).hexdigest()}\n"
    control_entries[0] = _entry(".PKGINFO", 0o644, pkginfo.encode())
    control_raw = _normalise_ustar_magic(_tar_bytes(control_entries, terminate=False))
    control_gz = gzip_bytes(control_raw)

    name = f"luci-app-lanory-{version}.apk"
    dest = os.path.join(out_dir, name)
    with open(dest, "wb") as out:
        out.write(control_gz)
        out.write(data_gz)
    return dest


def _dir_entry(name, mode):
    """A directory entry.

    The S_IFDIR bit matters: apk decides whether an entry is a directory by
    testing the mode from the tar header, not by the typeflag.
    """
    info = tarfile.TarInfo(name)
    info.type = tarfile.DIRTYPE
    info.mode = stat.S_IFDIR | mode
    info.mtime = 0
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    return info, None


def _entry(name, mode, data):
    """One tar entry with the APK-TOOLS.checksum pax record apk requires."""
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mode = mode
    info.mtime = 0
    info.uid = 0
    info.gid = 0
    info.uname = "root"
    info.gname = "root"
    info.pax_headers = {"APK-TOOLS.checksum.SHA1": hashlib.sha1(data).hexdigest()}
    return info, io.BytesIO(data)


# ---------------------------------------------------------------------------
# Architecture detection
# ---------------------------------------------------------------------------


def detect_arch(binary: str) -> str:
    """Map an ELF binary's machine/class to an OpenWrt architecture name."""
    with open(binary, "rb") as fh:
        header = fh.read(20)
    if header[:4] != b"\x7fELF":
        raise SystemExit(f"{binary}: not an ELF file")
    ei_class = header[4]  # 1 = 32-bit, 2 = 64-bit
    # e_machine is a 16-bit little-endian field at offset 18
    machine = int.from_bytes(header[18:20], "little")
    # EM_386=3, EM_ARM=40, EM_X86_64=62, EM_AARCH64=183, EM_RISCV=243
    if machine == 3:
        return ARCH_MAP[("386", "")]
    if machine == 62:
        return ARCH_MAP[("amd64", "")]
    if machine == 183:
        return ARCH_MAP[("arm64", "")]
    if machine == 40:
        # 32-bit ARM: we cannot read GOARM from the binary, so assume v7, which
        # is what the release build targets.
        return ARCH_MAP[("arm", "7")]
    if machine == 243:
        return ARCH_MAP[("riscv64", "")]
    raise SystemExit(f"{binary}: unsupported ELF machine {machine} (class {ei_class})")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--binary", help="path to a cross-compiled lanory binary")
    ap.add_argument("--binary-dir", help="directory of lanory-linux-* binaries")
    ap.add_argument("--all", action="store_true", help="package every lanory-linux-* binary in --binary-dir")
    ap.add_argument("--arch", help="override the detected OpenWrt architecture name")
    ap.add_argument("--version", default="0.1.0", help="package version")
    ap.add_argument("--out", default="dist/packages", help="output directory")
    ap.add_argument("--format", choices=["ipk", "apk", "both"], default="both")
    ap.add_argument("--luci", action="store_true", help="also build the LuCI companion package")
    ap.add_argument("--no-service", action="store_true", help="skip the service package (use with --luci)")
    args = ap.parse_args(argv)

    os.makedirs(args.out, exist_ok=True)

    binaries: list[str] = []
    if args.luci and args.no_service:
        pass  # only the LuCI package is wanted
    elif args.all:
        if not args.binary_dir:
            ap.error("--all requires --binary-dir")
        for name in sorted(os.listdir(args.binary_dir)):
            if name.startswith("lanory-linux-"):
                binaries.append(os.path.join(args.binary_dir, name))
    elif args.binary:
        binaries = [args.binary]
    elif not args.luci:
        ap.error("provide --binary or --all --binary-dir")

    if not binaries and not args.luci:
        print("no binaries found", file=sys.stderr)
        return 1

    global _BINARY_SIZE
    built = []
    for binary in binaries:
        arch = args.arch or detect_arch(binary)
        _BINARY_SIZE = os.path.getsize(binary)
        _META_CACHE_INVALIDATE()

        if args.format in ("ipk", "both"):
            built.append(build_ipk(binary, args.version, arch, args.out))
        if args.format in ("apk", "both"):
            built.append(build_apk(binary, args.version, arch, args.out))

    if args.luci:
        built.append(build_luci_apk(args.version, args.out))

    for path in built:
        size = os.path.getsize(path)
        print(f"{path}  ({size / (1024 * 1024):.1f} MiB)")
    return 0


def _META_CACHE_INVALIDATE() -> None:
    global _META_CACHE
    _META_CACHE = None


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
