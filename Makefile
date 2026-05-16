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
DOCS_VERSION ?= $(shell cat website/VERSION 2>/dev/null || echo dev)
LSP_VERSION  ?= $(shell git describe --tags --match 'lsp-v*' --abbrev=0 2>/dev/null | sed 's/^lsp-//' || echo dev)

# Resolve hugo binary: prefer user-local installs (~/.local/bin) over system-
# wide ones so that `make docs-site` uses the same binary setup.sh installs.
HUGO := $(shell PATH="$(HOME)/.local/bin:$(PATH)" sh -c 'command -v hugo 2>/dev/null || echo hugo')

.PHONY: build build-full install install-full \
        test test-verbose test-integration test-examples test-dpg vet lint \
        test-lsp test-lsp-smoke test-grammar test-lang \
        test-nvim test-vscode test-idea test-helix test-editors \
        test-all \
        dist dist-linux dist-darwin dist-windows \
        clean clean-dist clean-all version release tag changelog \
        docs-cli docs-site docs-serve \
        setup

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
	# ---

	# ---------------------------------------------
	# UNIT TESTS
	# ---------------------------------------------
	go test ./...

test-verbose:
	# ---

	# ---------------------------------------------
	# VERBOSE UNIT TESTS
	# ---------------------------------------------
	go test ./... -v

test-integration:
	# ---

	# ---------------------------------------------
	# INTEGRATION TESTS
	# ---------------------------------------------
	go test -tags integration -count=1 -timeout 5m ./...

test-examples:
	# ---

	# ---------------------------------------------
	# EXAMPLE TESTS
	# ---------------------------------------------
	go test ./examples/... -v

test-dpg: test test-integration

# ── Lang tests ────────────────────────────────────────────────────────────────

# LSP Go unit tests (fast; no binaries required)
test-lsp:
	# ---

	# ---------------------------------------------
	# LSP TESTS
	# ---------------------------------------------
	cd editors/lsp && go test ./internal/...

# LSP end-to-end smoke test (builds dpg-lsp; requires dpg on PATH)
test-lsp-smoke:
	# ---

	# ---------------------------------------------
	# LSP SMOKE TESTS
	# ---------------------------------------------
	cd editors/lsp && go test ./cmd/lsp-smoke/

# Tree-sitter grammar (requires Node.js + tree-sitter-cli)
test-grammar:
	# ---

	# ---------------------------------------------
	# GRAMMAR TESTS
	# ---------------------------------------------
	editors/grammar/scripts/test.sh

test-lang: test-lsp test-lsp-smoke test-grammar

# ── Editor tests ──────────────────────────────────────────────────────────────

# Neovim Lua specs (requires nvim 0.10+ and plenary.nvim)
test-nvim:
	# ---

	# ---------------------------------------------
	# NVIM TESTS
	# ---------------------------------------------
	nvim --headless --noplugin -u editors/nvim/tests/minimal_init.lua \
	  -c "PlenaryBustedDirectory editors/nvim/tests/ {minimal_init='editors/nvim/tests/minimal_init.lua'}" \
	  -c "qa!"

# VS Code extension tests (requires Node.js; downloads Electron on first run)
test-vscode:
	# ---

	# ---------------------------------------------
	# VSCODE TESTS
	# ---------------------------------------------
	cd editors/vscode && npm install && npm run compile && npm test

# JetBrains plugin tests (requires JDK 17+; downloads IntelliJ on first run)
test-idea:
	# ---

	# ---------------------------------------------
	# IDEA TESTS
	# ---------------------------------------------
	cd editors/idea && ./gradlew test

# Helix languages.toml structural validation (requires python3 or taplo)
test-helix:
	# ---

	# ---------------------------------------------
	# HELIX TESTS
	# ---------------------------------------------
	editors/helix/validate.sh

# Run all editor test suites
test-editors: test-nvim test-vscode test-idea test-helix

# ── All Tests ─────────────────────────────────────────────────────────────────

# Run every test suite — no tests spared
test-all: test-dpg test-lang test-editors

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

# ── Tag & Release ─────────────────────────────────────────────────────────────

changelog:
	@bash scripts/changelog.sh "$(or $(PREFIX),v)"

tag:
	@test -n "$(TAG)" || (echo "Usage: make tag TAG=v1.2.3  (also lsp-v1.2.3, docs-v1.2.3)"; exit 1)
	@bash scripts/tag.sh "$(TAG)"

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
	cd $(WEBSITE_DIR) && npm install && \
	HUGO_PARAMS_DOCSVERSION="$(DOCS_VERSION)" \
	HUGO_PARAMS_DPGVERSION="$(VERSION)" \
	HUGO_PARAMS_LSPVERSION="$(LSP_VERSION)" \
	$(HUGO) --minify

docs-serve: docs-cli
	cd $(WEBSITE_DIR) && npm install && \
	HUGO_PARAMS_DOCSVERSION="$(DOCS_VERSION)" \
	HUGO_PARAMS_DPGVERSION="$(VERSION)" \
	HUGO_PARAMS_LSPVERSION="$(LSP_VERSION)" \
	$(HUGO) serve --disableFastRender

# ── Setup ─────────────────────────────────────────────────────────────────────

setup:
	bash scripts/setup.sh

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -f $(BUILD)/$(BINARY)

clean-dist:
	rm -rf $(DIST)

clean-all: clean clean-dist
