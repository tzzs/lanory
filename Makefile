# Lanory — build, test and packaging helpers.
#
# Everything targets a single Go binary that serves the API and the built
# frontend. No Node.js or Python runtime is required at deploy time.

BINARY      := lanory
GO          ?= go
VERSION     ?= 0.1.0
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

WEB_DIR     := web
PKG_DIR     := packaging/openwrt

.PHONY: all build test test-race bench lint fmt vet clean web web-dev install uninstall \
        packages apk-sign apk-keygen apk-sdk apk-check package-check check run dev demo

all: build

## build: build the Lanory binary (embeds the frontend when present)
build: web
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/lanory

## test: run the Go test suite
test:
	$(GO) test ./... -count=1

## test-race: run the Go test suite with the race detector
test-race:
	$(GO) test ./... -count=1 -race

## coverage: run tests and print a coverage summary
coverage:
	$(GO) test ./... -count=1 -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

## bench: run the resource benchmarks (writes, reads, topology build, SSE)
bench:
	$(GO) test ./internal/resourcetest/ -bench=. -benchtime=20x -run=^$$ -v

## resource-test: run the scale, memory and retention tests
resource-test:
	$(GO) test ./internal/resourcetest/ -v -run 'Test' 2>&1 | grep -Ev '^(=== RUN|=== PAUSE|=== CONT)'px

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: format Go sources
fmt:
	$(GO) fmt ./...

## check: fmt + vet + test + packaging verification (the pre-commit gate)
check: fmt vet test apk-check

## web: build the frontend into web/dist
web:
	cd $(WEB_DIR) && npm run build

## webapp: stage web/dist into internal/api/webapp for go:embed
webapp: web
	mkdir -p internal/api/webapp
	cp -r $(WEB_DIR)/dist/. internal/api/webapp/
	@echo "frontend staged into internal/api/webapp"

## web-dev: run the frontend dev server (proxies /api to :8787)
web-dev:
	cd $(WEB_DIR) && npm run dev

## run: build everything and run the service
run: web build
	./bin/$(BINARY) --config config.yaml

## dev: run backend and frontend concurrently (requires two terminals or a process manager)
dev:
	@echo "terminal 1: make web-dev"
	@echo "terminal 2: $(GO) run ./cmd/lanory --config config.yaml --web-dir $(WEB_DIR)/dist"

## demo: run the service against the built-in sample network
demo: web build
	./bin/$(BINARY) --demo --config config.yaml

## release: cross-compile the release binaries for the supported targets
release: webapp
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/lanory
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/lanory
	CGO_ENABLED=0 GOOS=linux GOARCH=386 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-386 ./cmd/lanory
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-armv7 ./cmd/lanory
	@echo "release binaries in dist/"

## apk-check: static verification of the OpenWrt packaging (no SDK needed)
apk-check:
	sh $(PKG_DIR)/verify.sh

## package-check: verify the built .ipk/.apk archives are structurally valid
package-check:
	python3 $(PKG_DIR)/verify-packages.py

## packages: build installable .ipk and .apk packages from the release binaries
## (no OpenWrt SDK needed: the binary is already static, the rest is text)
packages: release
	python3 $(PKG_DIR)/mkpackage.py --all --binary-dir dist --version $(VERSION) --out dist/packages

## apk-sign: sign the built packages with a local RSA key
## (LANORY_KEY=/path/to/key.key, defaults to ~/.lanory/lanory.key)
apk-sign:
	@test -n "$(LANORY_KEY)" || { echo "set LANORY_KEY=/path/to/key.key"; exit 1; }
	@test -f "$(LANORY_KEY)" || python3 $(PKG_DIR)/signapk.py --keygen $(LANORY_KEY)
	@for p in dist/packages/*.apk; do \
		case "$$p" in *.signed) continue;; esac; \
		python3 $(PKG_DIR)/signapk.py --key $(LANORY_KEY) --in $$p --out $$p.signed || exit 1; \
	done
	@echo "signed packages are in dist/packages/*.apk.signed"

## apk-keygen: generate a signing key pair
apk-keygen:
	@test -n "$(LANORY_KEY)" || { echo "set LANORY_KEY=/path/to/key.key"; exit 1; }
	python3 $(PKG_DIR)/signapk.py --keygen $(LANORY_KEY)

## apk-sdk: build the package inside the OpenWrt buildroot (needs the SDK)
apk-sdk:
	$(MAKE) -C $(PKG_DIR)

## install: install onto the local OpenWrt target
install:
	$(MAKE) -C $(PKG_DIR) install

## uninstall: remove from the local OpenWrt target, keeping user data
uninstall:
	$(MAKE) -C $(PKG_DIR) uninstall

clean:
	rm -rf bin/ $(WEB_DIR)/dist/ coverage.out
