BINARY  := dpg
MODULE  := github.com/dullkingsman/dpg
CMD     := ./cmd/dpg
BUILD   := build
DIST    := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X '$(MODULE)/internal/version.Version=$(VERSION)' \
           -X '$(MODULE)/internal/version.Commit=$(COMMIT)'   \
           -X '$(MODULE)/internal/version.Date=$(DATE)'

WEBSITE_DIR := website

# Resolve hugo binary: prefer user-local installs (~/.local/bin) over system-
# wide ones so that `make docs-site` uses the same binary setup.sh installs.
HUGO := $(shell PATH="$(HOME)/.local/bin:$(PATH)" sh -c 'command -v hugo 2>/dev/null || echo hugo')

.PHONY: build build-full install install-full \
        test test-verbose test-integration test-examples vet lint \
        dist dist-linux dist-darwin dist-windows \
        clean clean-dist clean-all version release \
        docs-cli docs-site docs-serve

# ── Build ─────────────────────────────────────────────────────────────────────
# Fast development build — documentation is NOT embedded.
# dpg docs will print an error in binaries built this way.

build:
	go build -ldflags "$(LDFLAGS)" -o $(BUILD)/$(BINARY) $(CMD)

install:
	go install -ldflags "$(LDFLAGS)" $(CMD)

# Full build — builds the Hugo documentation site first, then embeds it.
# Requires Hugo (extended), Node.js, and npm to be on PATH.

build-full: docs-site
	go build -tags embeddata -ldflags "$(LDFLAGS)" -o $(BUILD)/$(BINARY) $(CMD)

install-full: docs-site
	go install -tags embeddata -ldflags "$(LDFLAGS)" $(CMD)

# ── Test ──────────────────────────────────────────────────────────────────────

test:
	go test ./...

test-verbose:
	go test ./... -v

test-integration:
	go test -tags integration -count=1 -timeout 5m ./...

test-examples:
	go test ./examples/... -v

# ── Quality ───────────────────────────────────────────────────────────────────

vet:
	go vet ./...

lint:
	staticcheck ./...

# ── Distribution ──────────────────────────────────────────────────────────────
# All dist targets embed the documentation site; run docs-site first.

dist: docs-site dist-linux dist-darwin dist-windows

dist-linux:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 \
		go build -tags embeddata -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 $(CMD)
	@# linux/arm64 requires a native ARM64 host (CGo via pg_query_go prevents cross-compilation).
	@# Build it on an arm64 machine or let the release CI handle it (ubuntu-24.04-arm runner).
	@if [ "$$(uname -m)" = "aarch64" ] || [ "$$(uname -m)" = "arm64" ]; then \
		GOOS=linux GOARCH=arm64 \
			go build -tags embeddata -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 $(CMD); \
	else \
		echo "  skipping linux/arm64 (not on ARM64 host; CI builds it natively)"; \
	fi

dist-darwin:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=amd64 \
		go build -tags embeddata -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 $(CMD)
	GOOS=darwin GOARCH=arm64 \
		go build -tags embeddata -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 $(CMD)

dist-windows:
	@mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 \
		go build -tags embeddata -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe $(CMD)

# ── Version ───────────────────────────────────────────────────────────────────

version:
	@echo "Version: $(VERSION)"
	@echo "Commit:  $(COMMIT)"
	@echo "Date:    $(DATE)"

# ── Release ───────────────────────────────────────────────────────────────────

release: dist
	@for f in $(DIST)/$(BINARY)-*; do \
		tar czf $$f.tar.gz -C $(DIST) $$(basename $$f) && \
		echo "archived $$f.tar.gz"; \
	done

# ── Docs ──────────────────────────────────────────────────────────────────────

docs-cli:
	@mkdir -p $(WEBSITE_DIR)/content/docs/cli
	go run ./tools/gendocs --output $(WEBSITE_DIR)/content/docs/cli

docs-site: docs-cli
	cd $(WEBSITE_DIR) && npm install && $(HUGO) --minify

docs-serve: docs-cli
	cd $(WEBSITE_DIR) && npm install && $(HUGO) serve --disableFastRender

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -f $(BUILD)/$(BINARY)

clean-dist:
	rm -rf $(DIST)

clean-all: clean clean-dist
