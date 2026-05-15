---
title: "Installation"
description: "Install dpg from a pre-built binary or build from source. Also covers dpg-lsp for editor support."
weight: 1
---

## System Requirements

| Requirement | Minimum |
|---|---|
| PostgreSQL target | 14 or later |
| OS | Linux (amd64/arm64), macOS (amd64/arm64), Windows (amd64) |

To build from source, a C compiler (GCC or Clang) and Go 1.25+ are also required — see [Build from Source](#build-from-source) below.

---

## Install from Binary

### One-line install (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/dullkingsman/dpg/master/scripts/install.sh | bash
```

This downloads the correct pre-built binary for your platform and installs it to `/usr/local/bin` (if `sudo` is available) or `~/.local/bin` (otherwise).

To also install the language server for editor support in one step:

```bash
curl -fsSL https://raw.githubusercontent.com/dullkingsman/dpg/master/scripts/install.sh | bash -s -- --with-lsp
```

### Install script options

```bash
# Install a specific version
bash <(curl -fsSL .../install.sh) --version v0.8.0

# Override install directory
bash <(curl -fsSL .../install.sh) --install-dir ~/.bin

# Preview what would be installed (no changes made)
bash <(curl -fsSL .../install.sh) --check

# Install dpg + dpg-lsp in one step (requires Go on PATH)
bash <(curl -fsSL .../install.sh) --with-lsp
```

### Manual download

Download the binary directly from the [Releases page](https://github.com/dullkingsman/dpg/releases):

| Platform | Archive |
|---|---|
| Linux amd64 | `dpg-linux-amd64.tar.gz` |
| Linux arm64 | `dpg-linux-arm64.tar.gz` |
| macOS Intel | `dpg-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `dpg-darwin-arm64.tar.gz` |
| Windows amd64 | `dpg-windows-amd64.exe.tar.gz` |

Each archive contains a single binary. Extract it, rename it to `dpg` (or `dpg.exe` on Windows), and place it somewhere on your `PATH`.

### Install via `go install`

If you have Go 1.25+ and a C compiler installed:

```bash
go install github.com/dullkingsman/dpg/cmd/dpg@latest
```

---

## Install the Language Server (dpg-lsp)

`dpg-lsp` powers editor features: diagnostics, hover documentation, go-to-definition, and completions. It is a separate binary and requires Go on `PATH`:

```bash
go install github.com/dullkingsman/dpg-lsp/cmd/dpg-lsp@latest
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`. Editor setup is covered in [Editor Integration](./editor-integration).

---

## Build from Source

### Additional requirements

| Requirement | Minimum |
|---|---|
| Go | 1.25 or later |
| CGo toolchain | Required — `pg_query_go` uses libpg_query (C library) |
| GCC / Clang | Must be on `PATH` for the CGo build |

Because `pg_query_go` links against the real PostgreSQL C parser, a C compiler must be present. Pure-Go cross-compilation is **not** possible; each target platform must be built on that platform or with a compatible CGo cross-compilation toolchain.

```bash
git clone https://github.com/dullkingsman/dpg
cd dpg
```

### Build for the current platform

```bash
make build          # produces build/dpg
make install        # installs to $(go env GOPATH)/bin
```

### All make targets

| Target | Description |
|---|---|
| `make build` | Compile for the current OS/arch, output `build/dpg` |
| `make install` | `go install` to `$GOPATH/bin` |
| `make test` | `go test ./...` — unit tests only, no Docker required |
| `make test-verbose` | `go test ./... -v` |
| `make test-integration` | `go test -tags integration -count=1 -timeout 5m ./...` — requires Docker |
| `make test-examples` | `go test ./examples/... -v` — runs runnable pipeline examples |
| `make vet` | `go vet ./...` |
| `make lint` | `staticcheck ./...` (requires `staticcheck` on PATH) |
| `make dist` | Cross-compile for all supported platforms into `dist/` |
| `make dist-linux` | Build linux/amd64 and linux/arm64 |
| `make dist-darwin` | Build darwin/amd64 and darwin/arm64 |
| `make dist-windows` | Build windows/amd64 |
| `make clean` | Remove `./dpg` |
| `make clean-dist` | Remove `dist/` |
| `make clean-all` | Remove both |
| `make version` | Print embedded VERSION, COMMIT, DATE |
| `make release` | Build dist + create compressed archives |

### Version information

The binary embeds version metadata at build time:

```bash
# Build with an explicit version tag
make build VERSION=v0.8.0

# The binary reports version info:
dpg --version
# dpg version v0.8.0 (commit: a3f7c91, built: 2026-04-27T00:00:00Z)
```

If built without `VERSION`, the value defaults to `git describe --tags --always --dirty`, or `dev` if git is unavailable.

### Cross-compilation

Because of the CGo requirement, cross-compilation requires a C cross-compiler. The recommended approach is `zig cc`, which provides hermetic cross-compilation:

```bash
# Install zig (https://ziglang.org/download/)
make dist-linux      # uses zig cc for linux/arm64 if not on that arch
make dist-darwin     # requires macOS SDK (only works on macOS hosts)
```

---

## Verifying the Install

```bash
dpg --help
dpg --version
```

Expected output:

```
dpg — Declarative PG schema compiler and migration tool

Usage:
  dpg [command]

Available Commands:
  plan         Diff desired state vs snapshot and print the SQL migration
  apply        Execute the planned migration and update the snapshot
  verify       Check the live database for drift against the snapshot
  dump         Introspect a live database and produce initial .dpg source files
  diff         Diff two DPG source directories and print the SQL migration
  portability  Report PostgreSQL-specific constructs in use
```

## Running Tests

```bash
make test            # unit tests (no live database required)
make test-examples   # pipeline examples (compilation, diffing, linting, portability)
```

Integration tests use [testcontainers-go](https://testcontainers.com) to spin up a real PostgreSQL container. They require Docker to be running:

```bash
make test-integration
# equivalent to: go test -tags integration -count=1 -timeout 5m ./...
```
