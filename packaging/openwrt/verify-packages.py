#!/usr/bin/env python3
"""Verify that the packages produced by mkpackage.py are structurally valid.

This runs without OpenWrt: it re-parses the archives and checks them against the
formats documented in apk-tools (doc/apk-v2.5.scd) and the opkg .ipk layout. It
is what `make package-check` runs, and it is a CI step.

Checked:
  .ipk  starts with the debian-binary marker, has exactly two concatenated gzip
        streams (control, data), the control file has every required field,
        conffiles lists the files that must survive an upgrade, and the install
        scripts are present and executable.
  .apk  is a single gzip stream containing .PKGINFO first, with pkgname/pkgver/
        arch/depend/size, plus the install scripts and the file tree.
  both  the binary inside the package is byte-identical to the release binary,
        file modes are right (config files are not world readable, executables
        are 0755), and every path the init script reads is present.
"""

from __future__ import annotations

import glob
import gzip
import hashlib
import io
import os
import sys
import tarfile
import zlib

FAIL = 0


def fail(msg: str) -> None:
    global FAIL
    print(f"FAIL  {msg}")
    FAIL += 1


def ok(msg: str) -> None:
    print(f"ok    {msg}")


def say(msg: str) -> None:
    print(f"\n== {msg}")


REQUIRED_CONTROL_FIELDS = [
    "Package",
    "Version",
    "Depends",
    "Architecture",
    "Installed-Size",
    "Maintainer",
    "License",
    "Description",
]

REQUIRED_PKGINFO_FIELDS = ["pkgname", "pkgver", "arch", "size", "license"]

# Paths the init script and the service expect to find after install.
REQUIRED_PATHS = [
    "usr/bin/lanory",
    "etc/init.d/lanory",
    "etc/config/lanory",
    "etc/lanory/config.yaml",
    "etc/uci-defaults/99-lanory",
]


def gunzip_members(data: bytes) -> list[bytes]:
    """Split concatenated gzip streams (the .ipk layout)."""
    out = []
    rest = data
    while rest:
        d = zlib.decompressobj(16 + zlib.MAX_WBITS)
        out.append(d.decompress(rest))
        rest = d.unused_data
    return out


def check_ipk(path: str, binary_path: str | None) -> None:
    name = os.path.basename(path)
    data = open(path, "rb").read()

    if not data.startswith(b"2.0\n"):
        fail(f"{name}: does not start with the debian-binary marker")
        return
    ok(f"{name}: debian-binary marker present")

    members = gunzip_members(data[4:])
    if len(members) != 2:
        fail(f"{name}: expected 2 gzip streams (control, data), found {len(members)}")
        return
    ok(f"{name}: two gzip streams (control, data)")

    control = tarfile.open(fileobj=io.BytesIO(members[0]))
    data_tar = tarfile.open(fileobj=io.BytesIO(members[1]))

    ctl = control.extractfile("control").read().decode()
    fields = {}
    for line in ctl.splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            fields[k.strip()] = v.strip()
    missing = [f for f in REQUIRED_CONTROL_FIELDS if f not in fields]
    if missing:
        fail(f"{name}: control is missing {missing}")
    else:
        ok(f"{name}: control has all required fields")

    if fields.get("Package") != "lanory":
        fail(f"{name}: Package is {fields.get('Package')!r}, want 'lanory'")

    # conffiles must list the config files, or user edits vanish on upgrade.
    if "conffiles" not in control.getnames():
        fail(f"{name}: no conffiles; UCI/YAML edits would be lost on upgrade")
    else:
        conf = control.extractfile("conffiles").read().decode().split()
        for want in ("/etc/config/lanory", "/etc/lanory/config.yaml"):
            if want in conf:
                ok(f"{name}: conffiles lists {want}")
            else:
                fail(f"{name}: conffiles does not list {want}")

    for script in ("postinst", "prerm", "postrm"):
        if script not in control.getnames():
            fail(f"{name}: missing {script}")
            continue
        info = control.getmember(script)
        if not info.mode & 0o111:
            fail(f"{name}: {script} is not executable (mode {info.mode:o})")
        else:
            ok(f"{name}: {script} present and executable")

    names = data_tar.getnames()
    for want in REQUIRED_PATHS:
        if want in names:
            ok(f"{name}: ships {want}")
        else:
            fail(f"{name}: does not ship {want}")

    check_modes(name, data_tar)
    if binary_path:
        check_binary(name, data_tar, "usr/bin/lanory", binary_path)


def check_luci_apk(path: str) -> None:
    """Check the LuCI companion package.

    It is a pure-Lua package, so the checks are the format ones that apk-tools
    enforces, plus the presence of the LuCI sources the menu entry needs.
    """
    name = os.path.basename(path)
    raw = gzip.open(path, "rb").read()
    ok(f"{name}: single gzip stream")

    tar = tarfile.open(fileobj=io.BytesIO(raw))
    names = tar.getnames()

    if not names or names[0] != ".PKGINFO":
        fail(f"{name}: .PKGINFO must be the first entry, found {names[0] if names else 'nothing'}")
        return
    ok(f"{name}: .PKGINFO is the first entry")

    info = tar.extractfile(".PKGINFO").read().decode()
    fields: dict[str, list[str]] = {}
    for line in info.splitlines():
        if " = " in line:
            k, v = line.split(" = ", 1)
            fields.setdefault(k.strip(), []).append(v.strip())

    for required in ["pkgname", "pkgver", "arch", "size"]:
        if required not in fields:
            fail(f"{name}: .PKGINFO is missing {required}")
    if fields.get("pkgname", [None])[0] != "luci-app-lanory":
        fail(f"{name}: pkgname is {fields.get('pkgname', ['?'])[0]}, want luci-app-lanory")
    else:
        ok(f"{name}: pkgname is luci-app-lanory")

    # arch must be "noarch", not "all": apk rejects an arch-less dependency on a
    # package with an architecture when the value is "all".
    if fields.get("arch", [None])[0] != "noarch":
        fail(f"{name}: arch is {fields.get('arch', ['?'])[0]}, want noarch")
    else:
        ok(f"{name}: arch is noarch")

    if "datahash" not in fields:
        fail(f"{name}: .PKGINFO has no datahash; apk-tools rejects the package")
    else:
        ok(f"{name}: datahash is present")

    for script in (".post-install", ".pre-deinstall"):
        if script not in names:
            fail(f"{name}: missing {script}")
        else:
            info_m = tar.getmember(script)
            if not info_m.mode & 0o111:
                fail(f"{name}: {script} is not executable")
            else:
                ok(f"{name}: {script} present and executable")

    required_files = [
        "usr/lib/lua/luci/controller/lanory.lua",
        "usr/lib/lua/luci/model/cbi/lanory.lua",
        "usr/lib/lua/luci/view/lanory/overview.htm",
        "usr/lib/lua/luci/view/lanory/devices.htm",
    ]
    for required in required_files:
        if required in names:
            ok(f"{name}: ships {required}")
        else:
            fail(f"{name}: does not ship {required}")

    # A file whose directory was never registered is rejected with "no dirent in
    # archive". A directory may be recorded with or without a trailing slash
    # (Python's tarfile writes it without), so compare both forms.
    dirs = {n.rstrip("/") for n in names if tar.getmember(n).isdir()}
    missing = []
    for required in required_files:
        parent = required.rsplit("/", 1)[0]
        if parent not in dirs:
            missing.append(parent)
    if missing:
        for d in missing:
            fail(f"{name}: directory entry {d} is missing; apk rejects with 'no dirent in archive'")
    else:
        ok(f"{name}: every file has its directory entry")


def check_apk(path: str, binary_path: str | None) -> None:
    name = os.path.basename(path)
    if "luci-app-lanory" in name:
        check_luci_apk(path)
        return
    try:
        raw = gzip.open(path, "rb").read()
    except OSError as exc:
        fail(f"{name}: not a gzip stream: {exc}")
        return
    ok(f"{name}: single gzip stream")

    tar = tarfile.open(fileobj=io.BytesIO(raw))
    names = tar.getnames()

    if not names or names[0] != ".PKGINFO":
        fail(f"{name}: .PKGINFO must be the first entry, found {names[0] if names else 'nothing'}")
        return
    ok(f"{name}: .PKGINFO is the first entry")

    info = tar.extractfile(".PKGINFO").read().decode()
    fields: dict[str, list[str]] = {}
    for line in info.splitlines():
        if " = " in line:
            k, v = line.split(" = ", 1)
            fields.setdefault(k.strip(), []).append(v.strip())
    missing = [f for f in REQUIRED_PKGINFO_FIELDS if f not in fields]
    if missing:
        fail(f"{name}: .PKGINFO is missing {missing}")
    else:
        ok(f"{name}: .PKGINFO has all required fields")

    if fields.get("depend"):
        ok(f"{name}: declares {len(fields['depend'])} dependencies")
    else:
        fail(f"{name}: declares no dependencies; the collectors need iproute2/bridge/ubus")

    for script in (".post-install", ".pre-deinstall", ".post-deinstall"):
        if script not in names:
            fail(f"{name}: missing {script}")
        else:
            info_m = tar.getmember(script)
            if not info_m.mode & 0o111:
                fail(f"{name}: {script} is not executable")
            else:
                ok(f"{name}: {script} present and executable")

    for want in REQUIRED_PATHS:
        if want in names:
            ok(f"{name}: ships {want}")
        else:
            fail(f"{name}: does not ship {want}")

    check_modes(name, tar)
    if binary_path:
        check_binary(name, tar, "usr/bin/lanory", binary_path)


def check_modes(name: str, tar: tarfile.TarFile) -> None:
    """Config files must not be world readable; executables must be 0755."""
    try:
        cfg = tar.getmember("etc/lanory/config.yaml")
    except KeyError:
        return
    if cfg.mode & 0o007:
        fail(f"{name}: etc/lanory/config.yaml is world accessible (mode {cfg.mode:o})")
    else:
        ok(f"{name}: config.yaml is not world readable")

    try:
        init = tar.getmember("etc/init.d/lanory")
    except KeyError:
        return
    if not init.mode & 0o111:
        fail(f"{name}: init script is not executable")
    else:
        ok(f"{name}: init script is executable")

    try:
        varlib = tar.getmember("var/lib/lanory")
    except KeyError:
        fail(f"{name}: /var/lib/lanory directory entry missing")
    else:
        if not varlib.isdir():
            fail(f"{name}: /var/lib/lanory is not a directory entry")
        else:
            ok(f"{name}: /var/lib/lanory exists as a directory")


def check_binary(name: str, tar: tarfile.TarFile, member: str, binary_path: str) -> None:
    """The packaged binary must be byte-identical to the release binary."""
    try:
        packaged = tar.extractfile(member).read()
    except KeyError:
        fail(f"{name}: {member} missing")
        return
    source = open(binary_path, "rb").read()
    if hashlib.sha256(packaged).hexdigest() != hashlib.sha256(source).hexdigest():
        fail(f"{name}: {member} differs from {binary_path}")
    else:
        ok(f"{name}: {member} is identical to the release binary")

    # Confirm it is the architecture the package claims.
    if packaged[:4] != b"\x7fELF":
        fail(f"{name}: {member} is not an ELF binary")
        return
    machine = int.from_bytes(packaged[18:20], "little")
    arch = tar.extractfile(".PKGINFO").read().decode() if any(
        m.name == ".PKGINFO" for m in tar.getmembers()
    ) else ""
    expect = {
        3: ("i386", "x86"),
        62: ("x86_64", "amd64"),
        183: ("aarch64", "arm64"),
        40: ("arm", ""),
    }.get(machine)
    if expect is None:
        fail(f"{name}: unknown ELF machine {machine}")
        return
    if arch and not any(token in arch for token in expect if token):
        fail(f"{name}: binary machine looks like {expect[0]} but .PKGINFO says {arch.splitlines()}")
    else:
        ok(f"{name}: binary architecture matches the package")


def main() -> int:
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    pkg_dir = os.path.join(root, "dist", "packages")
    dist_dir = os.path.join(root, "dist")

    if not os.path.isdir(pkg_dir):
        print(f"skip  {pkg_dir} does not exist; run `make packages` first")
        return 0

    ipks = sorted(glob.glob(os.path.join(pkg_dir, "*.ipk")))
    apks = sorted(glob.glob(os.path.join(pkg_dir, "*.apk")))
    if not ipks and not apks:
        print(f"skip  no packages in {pkg_dir}")
        return 0

    def binary_for(path: str) -> str | None:
        """Match a package to the release binary it was built from."""
        base = os.path.basename(path)
        for suffix, goarch in (("x86_64", "amd64"), ("i386", "386"),
                               ("aarch64", "arm64"), ("arm_cortex-a7", "armv7")):
            if suffix in base:
                candidate = os.path.join(dist_dir, f"lanory-linux-{goarch}")
                return candidate if os.path.exists(candidate) else None
        return None

    if ipks:
        say(".ipk packages")
        for path in ipks:
            check_ipk(path, binary_for(path))
    if apks:
        say(".apk packages")
        for path in apks:
            check_apk(path, binary_for(path))

    print()
    if FAIL == 0:
        print(f"all package checks passed ({len(ipks)} ipk, {len(apks)} apk)")
        return 0
    print(f"{FAIL} package check(s) failed")
    return 1


if __name__ == "__main__":
    sys.exit(main())
