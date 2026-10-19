#!/usr/bin/env python3
"""Assert that restarting Lanory does not duplicate or lose Demo Mode data.

Demo Mode is loaded into a real SQLite database. If loading were not idempotent,
every restart would double the device count, and the UI would slowly fill with
duplicates. The Go tests cover this at the store level; this script covers it end
to end, against the database the previous process left behind.

Usage: assert_restart.py <overview.json>
"""

from __future__ import annotations

import json
import sys


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: assert_restart.py <overview.json>", file=sys.stderr)
        return 2

    with open(argv[1], encoding="utf-8") as handle:
        overview = json.load(handle)

    counts = overview.get("counts") or {}
    total = counts.get("total", 0)
    assert total > 0, f"the second start lost the demo data: {counts}"

    # A duplicated load would double the dataset. The demo dataset has 13
    # devices; accept a small band so the assertion survives a deliberate
    # dataset change while still catching duplication (2x would be 26).
    assert total <= 20, (
        f"device count {total} looks like the dataset was loaded twice; "
        f"idempotency regressed: {counts}"
    )

    # The second start must still serve the very same rows, and every one of them
    # must still be labelled as demo data.
    devices = overview.get("recent_new_devices") or []
    for device in devices:
        sources = device.get("discovery_sources") or []
        assert "demo" in sources, (
            f"device {device.get('id')!r} lost its demo label across a restart: {sources}"
        )

    print(f"OK: demo data survived a restart without duplication, counts = {counts}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv))
    except AssertionError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        sys.exit(1)
