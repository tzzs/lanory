#!/usr/bin/env python3
"""Sign an APK v2 package with an RSA key.

Why this exists
---------------
An unsigned package can only be installed with `apk add --allow-untrusted`,
which is fine for a quick test and not something to ask of a user on a router
they rely on. A signed package is verified against a public key the user
installed once, and then installs without the override.

APK v2 signature format
-----------------------
Per apk-v2(5), the package is three concatenated gzip members forming one tar:

    signatures | control (.PKGINFO + scripts) | data

The signature member holds one or more files named:

    .SIGN.<algorithm>.<keyid>

where <algorithm> is RSA, RSA256 or RSA512 and <keyid> is the base name of the
public key file in /etc/apk/keys. The signature is computed over the control
member when .PKGINFO carries a datahash, and over control+data when it does not.

This script therefore:
  1. reads the package and splits it into its gzip members,
  2. verifies that .PKGINFO carries a datahash,
  3. signs the *compressed* control member with the private key,
  4. writes the signature member in front of it.

The key pair is generated with openssl; the public key is what the user copies
onto the router.

Usage:
    python3 packaging/openwrt/signapk.py --key lanory.key --in pkg.apk --out signed.apk
    python3 packaging/openwrt/signapk.py --keygen lanory.key      # create the key pair
"""

from __future__ import annotations

import argparse
import hashlib
import io
import os
import subprocess
import sys
import tarfile
import zlib

# The signature member must come first in the tar, so it is built separately and
# prepended.
SIGN_NAME_TEMPLATE = ".SIGN.RSA256.{keyid}"


def split_gzip_members(data: bytes) -> list[bytes]:
    """Split concatenated gzip streams into their decompressed contents."""
    out: list[bytes] = []
    rest = data
    while rest:
        dec = zlib.decompressobj(16 + zlib.MAX_WBITS)
        chunk = dec.decompress(rest)
        out.append(chunk)
        rest = dec.unused_data
    return out


def gzip_bytes(data: bytes) -> bytes:
    buf = io.BytesIO()
    with gzip_file(buf) as gz:
        gz.write(data)
    return buf.getvalue()


def gzip_file(buf):
    import gzip

    return gzip.GzipFile(fileobj=buf, mode="wb", mtime=0)


def member_offsets(data: bytes) -> list[tuple[int, int]]:
    """Return the (start, length) of every gzip member in the file."""
    out: list[tuple[int, int]] = []
    off = 0
    rest = data
    while rest:
        dec = zlib.decompressobj(16 + zlib.MAX_WBITS)
        dec.decompress(rest)
        consumed = len(rest) - len(dec.unused_data)
        out.append((off, consumed))
        off += consumed
        rest = dec.unused_data
    return out


def tar_entries(data: bytes) -> list[str]:
    """List the names in a tar archive."""
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:
        return tar.getnames()


def read_pkginfo(control: bytes) -> str:
    with tarfile.open(fileobj=io.BytesIO(control)) as tar:
        for member in tar.getmembers():
            if member.name == ".PKGINFO":
                fobj = tar.extractfile(member)
                if fobj is None:
                    break
                return fobj.read().decode()
    return ""


def generate_key(path: str, bits: int = 3072) -> None:
    """Create an RSA key pair with openssl."""
    print(f"generating a {bits}-bit RSA key in {path}", file=sys.stderr)
    subprocess.run(
        [
            "openssl", "genrsa", "-out", path, str(bits),
        ],
        check=True,
    )
    os.chmod(path, 0o600)
    pub = path + ".pub"
    subprocess.run(
        ["openssl", "rsa", "-in", path, "-pubout", "-out", pub],
        check=True,
    )
    print(f"public key written to {pub}", file=sys.stderr)
    # The key file name on the router must match the key id in the signature
    # exactly: apk looks the public key up by file name, and a mismatch is
    # reported as "UNTRUSTED signature" rather than as a missing key.
    keyid = os.path.basename(path)
    if keyid.endswith(".key"):
        keyid = keyid[: -len(".key")]
    print(
        f"copy {path}.pub to the router as /etc/apk/keys/{keyid}\n"
        "(the file name must match exactly; no extension)",
        file=sys.stderr,
    )


def sign(private_key: str, control_member: bytes) -> bytes:
    """Sign the compressed control member and return the signature bytes."""
    # The signature covers the compressed control member exactly as it appears
    # in the file, so the member is signed byte for byte.
    proc = subprocess.run(
        ["openssl", "dgst", "-sha256", "-sign", private_key],
        input=control_member,
        capture_output=True,
        check=True,
    )
    return proc.stdout


def build_signature_member(signature: bytes, keyid: str) -> bytes:
    """Build the tar member holding the signature.

    The member must not carry the tar end-of-archive marker: the three gzip
    members are read as one continuous tar, and an end marker in the middle makes
    the parser stop before the control and data sections. Only the last member
    ends the archive.
    """
    name = SIGN_NAME_TEMPLATE.format(keyid=keyid)
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w", format=tarfile.GNU_FORMAT) as tar:
        info = tarfile.TarInfo(name)
        info.size = len(signature)
        info.mode = 0o644
        info.mtime = 0
        info.uid = 0
        info.gid = 0
        info.uname = "root"
        info.gname = "root"
        tar.addfile(info, io.BytesIO(signature))
    raw = buf.getvalue()
    # Strip tarfile's trailing zero blocks: the archive continues in the next
    # gzip member.
    raw = raw.rstrip(b"\x00")
    raw += b"\x00" * ((512 - (len(raw) % 512)) % 512)
    return gzip_bytes(raw)


def sign_package(in_path: str, out_path: str, key_path: str, keyid: str) -> None:
    with open(in_path, "rb") as fh:
        data = fh.read()

    members = split_gzip_members(data)
    if len(members) < 2:
        raise SystemExit(f"{in_path}: expected a control and a data member, found {len(members)}")

    control = members[0]
    pkginfo = read_pkginfo(control)
    if "datahash" not in pkginfo:
        raise SystemExit(
            f"{in_path}: .PKGINFO has no datahash; the signature would have to cover "
            "the data member too, which this tool does not implement"
        )

    offsets = member_offsets(data)
    control_member = data[offsets[0][0] : offsets[0][0] + offsets[0][1]]

    signature = sign(key_path, control_member)
    sig_member = build_signature_member(signature, keyid)

    with open(out_path, "wb") as out:
        out.write(sig_member)
        out.write(data)

    print(f"signed {in_path} -> {out_path}", file=sys.stderr)
    print(f"  key id      : {keyid}", file=sys.stderr)
    print(f"  signature   : {len(signature)} bytes (RSA/SHA-256)", file=sys.stderr)
    print(
        f"  install the public key on the router as /etc/apk/keys/{keyid}",
        file=sys.stderr,
    )


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--key", help="path to the RSA private key")
    ap.add_argument("--keygen", metavar="PATH", help="generate a key pair at PATH and exit")
    ap.add_argument("--in", dest="in_path", help="package to sign")
    ap.add_argument("--out", dest="out_path", help="signed package to write")
    ap.add_argument("--keyid", help="key identifier used in the signature name")
    ap.add_argument("--bits", type=int, default=3072, help="RSA key size for --keygen")
    args = ap.parse_args()

    if args.keygen:
        generate_key(args.keygen, args.bits)
        return 0

    if not args.key or not args.in_path or not args.out_path:
        ap.error("--key, --in and --out are required")

    keyid = args.keyid or os.path.basename(args.key)
    if keyid.endswith(".key"):
        keyid = keyid[: -len(".key")]

    sign_package(args.in_path, args.out_path, args.key, keyid)
    return 0


if __name__ == "__main__":
    sys.exit(main())
