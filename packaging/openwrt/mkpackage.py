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

# Runtime dependencies. These are the tools and libraries the collectors and the
# init script actually use; see packaging/openwrt/README.md for the reasoning.
DEPENDS_IPK = "libc,iproute2-ip-full,bridge,ubus,libubox,libjson-c,ca-bundle"
DEPENDS_APK = "so:libc.so.6 iproute2-ip-full bridge ubus libubox libjson-c ca-bundle"

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
# ---------------------------------------------------------------------------


def pkginfo(version: str, arch: str) -> str:
    """The .PKGINFO metadata file inside a v2 apk."""
    lines = [
        "# Generated by packaging/openwrt/mkpackage.py",
        f"pkgname = {PKG_NAME}",
        f"pkgver = {version}",
        f"pkgdesc = {PKG_DESCRIPTION}",
        f"url = {PKG_URL}",
        f"builddate = {int(time.time())}",
        f"packager = {PKG_MAINTAINER}",
        f"size = {installed_bytes()}",
        f"arch = {arch}",
        f"origin = {PKG_NAME}",
        f"license = {PKG_LICENSE}",
    ]
    for dep in DEPENDS_APK.split():
        lines.append(f"depend = {dep}")
    return "\n".join(lines) + "\n"


def installed_bytes() -> int:
    return sum(len(data) for _p, _m, data in package_tree_meta())


def build_apk(binary: str, version: str, arch: str, out_dir: str) -> str:
    files = package_tree(binary)

    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.GNU_FORMAT) as tar:
        add_file(tar, "/.PKGINFO", 0o644, pkginfo(version, arch).encode())
        # Install scripts are not part of the v2 format's data section; apk uses
        # the same .pre-install / .post-install convention inside the archive.
        add_file(tar, "/.post-install", 0o755, postinst_script())
        add_file(tar, "/.pre-deinstall", 0o755, prerm_script())
        add_file(tar, "/.post-deinstall", 0o755, postrm_script())

        seen: set[str] = set()
        for path, mode, data in files:
            for parent in parent_dirs(path):
                if parent not in seen:
                    add_dir(tar, parent)
                    seen.add(parent)
            add_file(tar, path, mode, data)
        for d in EXTRA_DIRS:
            for parent in parent_dirs(d) + [d]:
                if parent not in seen:
                    add_dir(tar, parent)
                    seen.add(parent)

    name = f"{PKG_NAME}-{version}.apk"
    dest = os.path.join(out_dir, f"{arch}-{name}")
    with open(dest, "wb") as out:
        out.write(gzip_bytes(buf.getvalue()))
    return dest


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
    args = ap.parse_args(argv)

    os.makedirs(args.out, exist_ok=True)

    binaries: list[str] = []
    if args.all:
        if not args.binary_dir:
            ap.error("--all requires --binary-dir")
        for name in sorted(os.listdir(args.binary_dir)):
            if name.startswith("lanory-linux-"):
                binaries.append(os.path.join(args.binary_dir, name))
    elif args.binary:
        binaries = [args.binary]
    else:
        ap.error("provide --binary or --all --binary-dir")

    if not binaries:
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

    for path in built:
        size = os.path.getsize(path)
        print(f"{path}  ({size / (1024 * 1024):.1f} MiB)")
    return 0


def _META_CACHE_INVALIDATE() -> None:
    global _META_CACHE
    _META_CACHE = None


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
