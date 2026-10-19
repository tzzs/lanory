# syntax=docker/dockerfile:1
#
# Lanory container image.
#
# Docker is OPTIONAL. Lanory is designed for OpenWrt / iStoreOS, where it runs as
# a native package and sees the real layer 2 network. The container is for
# evaluation, development and for NAS / homelab hosts, not a prerequisite for
# the router deployment.
#
# Two stages:
#   1. frontend  — builds the React app into web/dist
#   2. build     — builds the Go binary with the frontend embedded
# The runtime stage is distroless static: no shell, no package manager, no libc
# requirement beyond what the Go binary brings. The database needs a writable
# volume at /var/lib/lanory; that path is created in the runtime stage.
#
# The image was NOT built in the environment that produced this file (no Docker
# daemon was available). See README.md in this directory.

# ---------------------------------------------------------------------------
# Stage 1: frontend
# ---------------------------------------------------------------------------
FROM node:22-alpine AS frontend

WORKDIR /src/web

# Copy only the manifests first so a source edit does not bust the npm cache.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
# The production build writes web/dist. `tsc -b` is part of `npm run build`, so
# a type error fails the image build instead of shipping broken JavaScript.
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: Go build
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS build

# git is needed for `-X main.commit`; ca-certificates is copied to the runtime.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
# Overlay the built frontend so //go:embed sees it. The repository ignores
# web/dist, so this copy is the only way the embed finds files in the container
# build.
COPY --from=frontend /src/web/dist ./web/dist

ARG VERSION=0.1.0
ARG TARGETOS=linux
ARG TARGETARCH=amd64

# CGO_ENABLED=0 keeps the binary static: modernc.org/sqlite is pure Go, so no
# libsqlite3 is needed at runtime and distroless static is enough.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/lanory ./cmd/lanory

# ---------------------------------------------------------------------------
# Stage 3: runtime
# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

# The binary runs as the distroless nonroot user (65532:65532).
WORKDIR /var/lib/lanory

COPY --from=build /out/lanory /usr/local/bin/lanory
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
# Default configuration. Override by mounting your own at the same path or by
# setting LANORY_* environment variables (they win over the file).
COPY --chown=65532:65532 packaging/docker/config.yaml /etc/lanory/config.yaml

# The data directory must be writable by the nonroot user. A named volume
# inherits the image's directory ownership on first use.
VOLUME ["/var/lib/lanory"]

EXPOSE 8787

# Health check.
#
# Distroless has no shell and no curl, so the only viable check is the binary
# itself. Lanory exposes GET /api/v1/health, and the CLI is expected to support a
# one-shot `--health-check` flag that queries it and exits non-zero on failure.
# That flag belongs to cmd/lanory, which is not part of this packaging change, so
# the HEALTHCHECK below is COMMENTED OUT rather than shipped broken. Enable it
# once `lanory --health-check` exists:
#
# HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
#   CMD ["/usr/local/bin/lanory", "--health-check"]
#
# Until then, an orchestrator can probe the HTTP endpoint itself:
#   GET http://<container>:8787/api/v1/health  ->  200 {"status":"ok",...}

ENTRYPOINT ["/usr/local/bin/lanory"]
CMD ["--config", "/etc/lanory/config.yaml"]
