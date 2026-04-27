BINARY  := dpg
MODULE  := github.com/dullkingsman/dpg
CMD     := ./cmd/dpg
DIST    := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X '$(MODULE)/internal/version.Version=$(VERSION)' \
           -X '$(MODULE)/internal/version.Commit=$(COMMIT)'   \
           -X '$(MODULE)/internal/version.Date=$(DATE)'

.PHONY: build install test test-verbose test-examples vet lint \
        dist dist-linux dist-darwin dist-windows \
        clean clean-dist clean-all version release

# ── Build ─────────────────────────────────────────────────────────────────────

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

install:
	go install -ldflags "$(LDFLAGS)" $(CMD)

# ── Test ──────────────────────────────────────────────────────────────────────

test:
	go test ./...

test-verbose:
	go test ./... -v

test-examples:
	go test ./examples/... -v

# ── Quality ───────────────────────────────────────────────────────────────────

vet:
	go vet ./...

lint:
	staticcheck ./...

# ── Distribution ──────────────────────────────────────────────────────────────

dist: dist-linux dist-darwin dist-windows

dist-linux:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 $(CMD)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 \
		CC="zig cc -target aarch64-linux-musl" \
		go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 $(CMD)

dist-darwin:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-amd64 $(CMD)
	GOOS=darwin GOARCH=arm64 \
		go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-darwin-arm64 $(CMD)

dist-windows:
	@mkdir -p $(DIST)
	GOOS=windows GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-windows-amd64.exe $(CMD)

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

# ── Clean ─────────────────────────────────────────────────────────────────────

clean:
	rm -f $(BINARY)

clean-dist:
	rm -rf $(DIST)

clean-all: clean clean-dist
