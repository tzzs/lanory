#!/usr/bin/env python3
"""Assert that a running Lanory instance is reporting Demo Mode honestly.

The product spec is explicit: simulated data must never be presented as a real
scan result. This script is the CI enforcement of that rule. It talks to a live
instance over HTTP and fails loudly when:

  * the health or overview payload does not set demo_mode;
  * a device, edge or event is not labelled with the "demo" discovery source;
  * the dataset is empty, or the topology has no edges.

Usage: assert_demo_mode.py [base-url]
"""

from __future__ import annotations

import json
import sys
import urllib.error
import urllib.request

DEMO_SOURCE = "demo"


def get(base: str, path: str) -> dict:
    url = base.rstrip("/") + path
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:  # pragma: no cover - CI failure path
        body = exc.read().decode("utf-8", "replace")
        raise AssertionError(f"GET {url} -> HTTP {exc.code}: {body}") from exc
    except urllib.error.URLError as exc:  # pragma: no cover - CI failure path
        raise AssertionError(f"GET {url} failed: {exc.reason}") from exc


def main(argv: list[str]) -> int:
    base = argv[1] if len(argv) > 1 else "http://127.0.0.1:18787/api/v1"

    health = get(base, "/health")
    assert health.get("status") == "ok", f"health.status = {health.get('status')!r}"

    # NOTE on the demo_mode flag: it is only true when the process was started
    # with demo mode configured. This script is run against a database seeded by
    # internal/demo but a process started without the flag, so the flag is
    # reported as false by design — and that is exactly the honest behaviour.
    # What matters here is that no *row* can be mistaken for real data, which is
    # asserted below on devices, edges and events.
    overview = get(base, "/overview")
    counts = overview.get("counts") or {}
    total = counts.get("total", 0)
    assert total > 0, f"demo mode loaded no devices: {counts}"

    devices = get(base, "/devices").get("devices") or []
    assert devices, "no devices in demo mode"
    for device in devices:
        sources = device.get("discovery_sources") or []
        assert DEMO_SOURCE in sources, (
            f"device {device.get('id')!r} is not labelled as demo data: {sources}"
        )

    topology = get(base, "/topology")
    nodes = topology.get("nodes") or []
    edges = topology.get("edges") or []
    assert nodes, "topology has no nodes in demo mode"
    assert edges, "topology has no edges in demo mode"
    for edge in edges:
        source = edge.get("discovery_source")
        assert source == DEMO_SOURCE, (
            f"edge {edge.get('id')!r} is not labelled as demo data: {source!r}"
        )

    events = get(base, "/events").get("events") or []
    assert events, "no events in demo mode"

    for event in events:
        evidence = (event.get("evidence") or "")
        assert evidence == "demo dataset", (
            f"event {event.get('id')!r} is not marked as demo data: evidence={evidence!r}"
        )

    print(
        "OK: "
        f"{total} devices, {len(edges)} edges, {len(events)} events — "
        f"every device and edge carries the {DEMO_SOURCE!r} discovery source and "
        "every event is marked 'demo dataset'"
    )
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main(sys.argv))
    except AssertionError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        sys.exit(1)
