# Development Guide

Everything needed to build, test, and contribute to DPG.

---

## Prerequisites

| Tool | Minimum version | Required for | Notes |
|------|----------------|--------------|-------|
| Go | 1.25.6 | Building | Must match `go.mod`; download from go.dev |
| GCC or Clang | any | Building | CGo required by `pg_query_go` (libpg_query C parser) |
| Git | 2.x | Everything | |
| Docker | 20.x | Integration tests | Daemon must be running; pulls `postgres:16-alpine` |
| Hugo extended | 0.147.0 | Docs site | Must be the **extended** variant for Sass/SCSS |
| Node.js | 20+ | Docs site | PostCSS dependency for Docsy theme |
| npm | 10+ | Docs site | Ships with Node.js |
| staticcheck | latest | `make lint` | `go install honnef.co/go/tools/cmd/staticcheck@latest` |
| Zig | 0.14.0 | Cross-compile | Optional; only needed for `make dist-linux` (linux/arm64) |

> **Why CGo?** `pg_query_go` wraps libpg_query — the real PostgreSQL C parser extracted into a standalone library. DPG uses it so that every `.dpg` file is parsed by the exact same grammar as PostgreSQL itself. This rules out a pure-Go cross-compile; each target platform must be built natively or with a CGo cross-compiler (Zig).

---

## Quick Setup

The setup script installs every mandatory tool and verifies the build. Safe to re-run.

```bash
git clone https://github.com/dullkingsman/dpg
cd dpg
bash scripts/setup.sh
```

### Script flags

```bash
bash scripts/setup.sh --check      # check versions, install nothing
bash scripts/setup.sh --no-docs    # skip Hugo + Node (schema tooling only)
bash scripts/setup.sh --no-zig     # skip Zig (local dev, no cross-compilation)
```

---

## Manual Setup

### Linux — Ubuntu / Debian

```bash
# System packages
sudo apt-get update
sudo apt-get install -y git gcc build-essential curl

# Go (replace 1.25.6 with the version in go.mod if it changes)
curl -fsSL https://go.dev/dl/go1.25.6.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH="/usr/local/go/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc

# Docker
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
# Log out and back in, then: docker run hello-world

# Hugo extended
HUGO_VER=0.147.0
curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VER}/hugo_extended_${HUGO_VER}_linux-amd64.tar.gz" \
  | sudo tar -C /usr/local/bin -xz hugo

# Node.js 20
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs

# staticcheck
go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Linux — Fedora / RHEL

```bash
sudo dnf install -y git gcc gcc-c++ make curl

# Go
curl -fsSL https://go.dev/dl/go1.25.6.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH="/usr/local/go/bin:$PATH"' >> ~/.bashrc && source ~/.bashrc

# Docker
sudo dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo
sudo dnf install -y docker-ce docker-ce-cli containerd.io
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"

# Hugo extended (same as Ubuntu)
HUGO_VER=0.147.0
curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VER}/hugo_extended_${HUGO_VER}_linux-amd64.tar.gz" \
  | sudo tar -C /usr/local/bin -xz hugo

# Node.js 20
sudo dnf module install -y nodejs:20/common

go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Linux — Arch

```bash
sudo pacman -S --needed git gcc base-devel docker nodejs npm

# Go (AUR has latest, or download directly)
curl -fsSL https://go.dev/dl/go1.25.6.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
echo 'export PATH="/usr/local/go/bin:$PATH"' >> ~/.bashrc && source ~/.bashrc

sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"

HUGO_VER=0.147.0
curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VER}/hugo_extended_${HUGO_VER}_linux-amd64.tar.gz" \
  | sudo tar -C /usr/local/bin -xz hugo

go install honnef.co/go/tools/cmd/staticcheck@latest
```

### macOS

```bash
# Homebrew (installs Xcode CLT which provides clang)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Core tools
brew install go git node@20
brew link --overwrite node@20

# Docker Desktop (provides the Docker daemon)
brew install --cask docker
# Open Docker.app from Applications to start the daemon.

# Hugo extended
HUGO_VER=0.147.0
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/arm64/arm64/')
curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VER}/hugo_extended_${HUGO_VER}_darwin-${ARCH}.tar.gz" \
  | sudo tar -C /usr/local/bin -xz hugo

go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Windows

Windows is a supported build target but the development environment is Linux/macOS. To develop on Windows:

1. Install [WSL2](https://learn.microsoft.com/en-us/windows/wsl/install) with Ubuntu, then follow the Linux instructions inside WSL.
2. Install Docker Desktop for Windows (WSL2 backend).
3. Alternatively, build release binaries using the CI pipeline.

---

## Repository Layout

```
dpg/
├── cmd/dpg/              # CLI entry point and all subcommands
├── internal/
│   ├── ast/              # Shared AST node definitions
│   ├── blockparser/      # Part-2 { } block parser
│   ├── compiler/         # Pipeline orchestration
│   ├── config/           # dpg.toml parsing
│   ├── diff/             # Desired IR vs snapshot → DiffOps
│   ├── emit/             # DiffOps → Migration SQL text
│   ├── executor/         # Execute migration against live PG (pgx)
│   ├── format/           # .dpg formatter
│   ├── graph/            # Topological sort + circular FK resolution
│   ├── introspect/       # Live catalog introspection
│   ├── ir/               # IR types and builder
│   ├── linter/           # Built-in lint rules
│   ├── merger/           # Multi-file merge (RFC §2.7)
│   ├── pgparser/         # pg_query_go wrapper
│   ├── pipeline/         # Interfaces, registry, shared types
│   ├── portability/      # PG-specific construct analysis
│   ├── project/          # Project discovery (dpg.toml walk)
│   ├── scanner/          # Source file tokenizer
│   ├── secrets/          # env:/link: secret resolution
│   ├── snapshot/         # Snapshot JSON store
│   ├── testpg/           # PostgreSQL container for integration tests
│   ├── ui/               # Terminal output helpers
│   └── version/          # Embedded version metadata
├── pkg/dpg/              # Stable public API (plugin authors import this)
├── tools/gendocs/        # Standalone CLI doc generator (cobra/doc)
├── internal/docssite/    # Embedded Hugo site server
├── website/              # Hugo/Docsy source for the documentation site
│   ├── config/_default/  # Hugo configuration
│   ├── content/          # Markdown pages
│   │   ├── docs/         # Getting started, reference, CLI, extending
│   │   └── rfc/          # RFC DPG-001
│   ├── layouts/          # Custom Hugo templates
│   └── static/           # Static assets (CSS, etc.)
├── docs/                 # Source documentation (mirrored into website)
├── rfc/                  # RFC source (mirrored into website/content/rfc)
├── examples/             # Runnable examples and plugin tests
├── scripts/              # Development tooling
├── Makefile              # All build/test/docs targets
├── go.mod / go.sum       # Module definition
└── CLAUDE.md             # AI assistant instructions
```

---

## Build Targets

```bash
make build          # Fast dev build → build/dpg  (docs NOT embedded)
make build-full     # Full build     → build/dpg  (docs embedded; runs Hugo first)
make install        # go install to $GOPATH/bin   (docs not embedded)
make install-full   # go install with embedded docs
```

### Distribution

```bash
make dist           # All platforms with embedded docs → dist/
make dist-linux     # linux/amd64 + linux/arm64 (arm64 needs Zig)
make dist-darwin    # darwin/amd64 + darwin/arm64
make dist-windows   # windows/amd64
make release        # make dist + tar/zip archives
```

All `dist` targets run `make docs-site` first, then compile with `-tags embeddata`.

### About `make build` vs `make build-full`

`make build` is designed for daily development. It skips Hugo entirely so the iteration cycle is `edit → go build → test`. The resulting binary returns an error on `dpg docs`.

`make build-full` is the release path. It runs the full Hugo pipeline (`docs-cli` → `npm install` → `hugo --minify`) and then compiles with `-tags embeddata`, embedding `internal/docssite/public/` into the binary. This requires Hugo extended, Node.js, and npm.

---

## Testing

### Unit tests

```bash
make test           # go test ./...
make test-verbose   # go test ./... -v
make vet            # go vet ./...
make lint           # staticcheck ./...
```

Unit tests have no external dependencies. They run on every push via CI.

### Integration tests

```bash
make test-integration
# equivalent: go test -tags integration -count=1 -timeout 5m ./...
```

Integration tests require Docker. They spin up a `postgres:16-alpine` container via testcontainers-go and exercise the full compile → plan → apply → introspect → zero-drift roundtrip.

The container is started once per test binary via `testpg.Start(t)` in `internal/testpg/testpg.go`. `t.Cleanup` stops and removes it automatically.

```bash
# Verify Docker is running before attempting integration tests:
docker info
docker pull postgres:16-alpine   # optional: pre-pull to speed up first run
```

### Plugin / example tests

```bash
make test-examples
# equivalent: go test ./examples/... -v
```

The `examples/plugin/` package demonstrates how to register a custom linter against `pkg/dpg`. These tests have no external dependencies and run entirely in-process.

---

## Documentation

### Serving locally

```bash
make docs-serve
# Opens http://localhost:1313 with live reload.
# Requires: Hugo extended 0.147.0, Node.js 20, npm.
```

The CLI reference pages (`website/content/docs/cli/*.md`) are generated automatically by `make docs-serve` before Hugo starts.

### Building a static copy

```bash
make docs-site
# Output: internal/docssite/public/
# This is also what make build-full embeds into the binary.
```

### Keeping CLI docs in sync

The generated CLI docs in `website/content/docs/cli/` are produced by `tools/gendocs/main.go`, which mirrors the command tree from `cmd/dpg` without importing pipeline stages. When you add or change a flag, also update its mirror in `tools/gendocs/main.go`.

```bash
make docs-cli       # regenerate only the CLI markdown files
```

---

## Code Organisation Principles

**Offline-first.** `plan` and `diff` never open a database connection. All database access is isolated to `executor` (apply), `introspect` (verify/dump), and `testpg` (integration tests). The compile → diff → emit path has no network I/O.

**Pipeline + registry.** Every processing stage is an interface in `internal/pipeline`. Concrete implementations register themselves into `pipeline.Default` via `init()`. The CLI resolves implementations from the registry at startup, never by importing concrete packages directly.

**Public API surface.** `pkg/dpg` re-exports selected types and functions via type aliases. Plugin authors import only `pkg/dpg`. Never import `internal/` from outside the module.

**Build tags for integration.** Files that import `testcontainers-go` carry `//go:build integration`. This keeps `go test ./...` fast and dependency-free.

---

## Common Workflows

### Add a new CLI flag

1. Add the flag in `cmd/dpg/<command>.go`
2. Mirror it in `tools/gendocs/main.go` (same `Use`, `Short`, `Long`, flags)
3. Run `make docs-cli` to regenerate the markdown

### Add a new lint rule

1. Implement in `internal/linter/` (satisfies `pipeline.Linter` or extend `BuiltinLinter`)
2. Add a unit test
3. Document in `docs/reference/linter.md` and `website/content/docs/reference/linter.md`

### Add a new IR object type

1. Define the struct in `internal/ir/`
2. Register a `KindXxx` constant in `internal/pipeline/types.go`
3. Add IR builder logic in `internal/ir/`
4. Add differ logic in `internal/diff/`
5. Add emitter logic in `internal/emit/`
6. Export via type alias in `pkg/dpg/dpg.go`
7. Add integration test in `examples/` or `internal/` with `//go:build integration`
8. Document in `docs/reference/objects.md`

---

## Troubleshooting

### `cgo: C compiler "gcc" not found`

```bash
# Ubuntu/Debian
sudo apt-get install -y gcc build-essential

# macOS
xcode-select --install
```

### `go: cannot find main module`

Run all `make` and `go` commands from the repository root (where `go.mod` lives), not from a subdirectory.

### `hugo: command not found` or `hugo: not extended`

The standard Hugo binary does not include the Sass/SCSS compiler. Download the **extended** variant explicitly:

```bash
HUGO_VER=0.147.0
ARCH=$(uname -m | sed 's/x86_64/amd64/')
curl -fsSL "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VER}/hugo_extended_${HUGO_VER}_linux-${ARCH}.tar.gz" \
  | sudo tar -C /usr/local/bin -xz hugo
hugo version   # must say "extended"
```

### Integration tests time out or fail to pull the Docker image

```bash
docker info                        # confirm daemon is running
docker pull postgres:16-alpine     # pre-pull the test image
make test-integration
```

If inside a corporate proxy, configure Docker's proxy settings and ensure `registry-1.docker.io` is reachable.

### `dpg docs` returns "documentation is not embedded in this build"

The dev build (`make build`) intentionally does not embed docs. Use:

```bash
make build-full   # embeds docs, requires Hugo + Node
# or use a release binary from GitHub releases
```

### `zig cc: command not found` during `make dist-linux`

Zig is only required for `linux/arm64` cross-compilation. Either install it or build natively on an ARM64 machine. `linux/amd64` does not use Zig.

```bash
ZIG_VER=0.14.0
curl -fsSL "https://ziglang.org/download/${ZIG_VER}/zig-linux-x86_64-${ZIG_VER}.tar.xz" \
  | sudo tar -xJ -C /usr/local/bin --strip-components=1 "zig-linux-x86_64-${ZIG_VER}/zig"
```

---

## Version Metadata

The binary embeds three values at link time:

```bash
make build VERSION=v0.8.0   # explicit version tag
dpg --version
# dpg version v0.8.0 (commit: a3f7c91, built: 2026-05-08T17:00:00Z)
```

Without `VERSION`, it defaults to `git describe --tags --always --dirty` or `dev` if git is unavailable. The three values are injected via `-ldflags` into `internal/version/`.
