# Lanory in Docker

**Status: unbuilt.** The Dockerfile and compose file are complete and reviewed,
but no Docker daemon was available in the environment that produced them, so the
image has never been built here and no digest is published. Build it yourself and
check the output before relying on it.

Docker is **optional**. The supported production deployment is the native
OpenWrt package (`packaging/openwrt`), because that is the only place where
Lanory sees the real bridge. The container exists for evaluation, development and
for NAS / homelab hosts that are already Docker based.

## The one thing to understand: network mode decides what Lanory can see

| | `network_mode: host` | default bridge network |
| --- | --- | --- |
| Bridge FDB (`bridge fdb show`) | the real bridge, every learned MAC | only the container's own veth |
| ARP / NDP neighbours | the host's neighbour table | the container's docker network only |
| mDNS / SSDP | visible (multicast reaches the host) | not forwarded by the bridge |
| Interface inventory | the host's interfaces, speeds included | the container's veth, one "eth0" |
| Wireless client list | available if the host has a wireless interface | no |
| Port publishing | n/a, binds the host directly | `-p 8787:8787` |
| Recommendation | **use this** on a Linux host | only with explicit `discovery.subnets` |

Lanory does not pretend otherwise: in bridge mode the FDB and neighbour
collectors report `Supported() == false` or return a single veth, and the UI shows
the resulting picture as incomplete rather than inventing devices. That is why
the compose file sets `LANORY_DISCOVERY_AUTO_DETECT_SUBNETS=false` for the bridge
profile: with auto-detection on and nothing visible, Lanory would scan the
docker network, which is not what the user wants.

## Build

```sh
# from the repository root
docker build -f packaging/docker/Dockerfile -t lanory:0.1.0 .

# multi-arch (needs buildx)
docker buildx build -f packaging/docker/Dockerfile \
  --platform linux/amd64,linux/arm64 -t lanory:0.1.0 --push .
```

The build is multi-stage:

1. `node:22-alpine` runs `npm ci && npm run build` → `web/dist`;
2. `golang:1.26-alpine` builds the static binary with the frontend embedded;
3. `gcr.io/distroless/static-debian12:nonroot` is the runtime: no shell, no
   package manager, runs as UID 65532.

The final image contains only the binary, the CA bundle and
`/etc/lanory/config.yaml`. `CGO_ENABLED=0` is required and is set in the
Dockerfile: SQLite is `modernc.org/sqlite`, which is pure Go.

## Run

```sh
# full discovery, Linux host
docker compose -f packaging/docker/docker-compose.yml up -d lanory-hostnet

# reduced discovery behind an explicit subnet list
docker compose -f packaging/docker/docker-compose.yml --profile bridge up -d lanory
```

or without compose:

```sh
docker run -d --name lanory \
  --network host \
  --read-only --tmpfs /tmp:size=16m \
  --cap-drop ALL --cap-add NET_RAW \
  --security-opt no-new-privileges \
  -v lanory-data:/var/lib/lanory \
  -e LANORY_DATABASE_PATH=/var/lib/lanory/lanory.db \
  lanory:0.1.0
```

`CAP_NET_RAW` is needed for ICMP probes. Everything else runs with `cap_drop:
ALL`. The container is `read_only` with `/var/lib/lanory` as the only writable
volume.

## Demo Mode in a container

```sh
docker run --rm -p 8787:8787 \
  -e LANORY_DEMO_MODE=true \
  lanory:0.1.0
```

Demo Mode loads a simulated sample network. The UI badges every device as
simulated and the API reports `demo_mode: true`; it must never be mistaken for a
real scan.

## Environment variables

Every setting in `config.yaml` has a `LANORY_*` override. The most useful ones:

| Variable | Effect |
| --- | --- |
| `LANORY_SERVER_PORT` | HTTP port (default 8787) |
| `LANORY_DATABASE_PATH` | SQLite file; must be on the volume |
| `LANORY_DISCOVERY_ENABLED` | background discovery on/off |
| `LANORY_DISCOVERY_INTERVAL` | e.g. `10m` |
| `LANORY_DISCOVERY_AUTO_DETECT_SUBNETS` | derive scan targets from LAN subnets |
| `LANORY_MONITORING_ENABLED` | probe engine on/off |
| `LANORY_AUTH_ENABLED` / `LANORY_AUTH_TOKEN` | require a bearer token |
| `LANORY_DEMO_MODE` | load the sample network |
| `LANORY_LOG_LEVEL` | `debug` / `info` / `warn` / `error` |

## Health

The distroless image has no shell or curl. The Dockerfile's `HEALTHCHECK` is
commented out until `lanory --health-check` exists, so probe the endpoint from
outside:

```sh
curl -fsS http://127.0.0.1:8787/api/v1/health
# {"status":"ok","version":"0.1.0","demo_mode":false,"uptime_seconds":42}
```

## What is not verified

- that the image builds (no Docker daemon here);
- that `--platform linux/arm64` produces a working arm64 image;
- that host networking is enough for every collector (it is enough for ARP/FDB;
  mDNS depends on how the host handles multicast);
- that the distroless base tag still resolves.

Build it, run it against a known network, and compare the device list with what
`ip neigh` and `bridge fdb show` report on the host before trusting the result.
