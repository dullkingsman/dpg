# Installation

## System Requirements

| Requirement | Minimum |
|---|---|
| PostgreSQL target | 14 or later |
| OS | Linux (amd64/arm64), macOS (amd64/arm64), Windows (amd64) |

To build from source, a C compiler (GCC or Clang) and Go 1.25+ are also required — see [Build from Source](#build-from-source) for details.

---

## Install from Binary

The quickest way to install dpg is via the install script, which downloads the correct pre-built binary for your platform.

### One-line install (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/dullkingsman/dpg/master/scripts/install.sh | bash
```

This installs `dpg` to `/usr/local/bin` (if `sudo` is available) or `~/.local/bin` (otherwise).

To also install the language server in one step:

```bash
curl -fsSL https://raw.githubusercontent.com/dullkingsman/dpg/master/scripts/install.sh | bash -s -- --with-lsp
```

### Install script options

```bash
# Install a specific version
bash scripts/install.sh --version v0.8.0

# Override install directory
bash scripts/install.sh --install-dir ~/.bin

# Preview what would be installed (no changes made)
bash scripts/install.sh --check

# Install dpg + dpg-lsp in one step (requires Go on PATH)
bash scripts/install.sh --with-lsp
```

### Manual download

Download the binary directly from the [Releases page](https://github.com/dullkingsman/dpg/releases) and put it on your `PATH`:

| Platform | Archive |
|---|---|
| Linux amd64 | `dpg-linux-amd64.tar.gz` |
| Linux arm64 | `dpg-linux-arm64.tar.gz` |
| macOS Intel | `dpg-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `dpg-darwin-arm64.tar.gz` |
| Windows amd64 | `dpg-windows-amd64.exe.tar.gz` |

Each archive contains a single binary. Extract it and rename it to `dpg` (or `dpg.exe` on Windows), then place it somewhere on your `PATH`.

### Install via `go install`

If you have Go 1.25+ and a C compiler installed:

```bash
go install github.com/dullkingsman/dpg/cmd/dpg@latest
```

---

## Install the Language Server (dpg-lsp)

`dpg-lsp` provides diagnostics, hover documentation, go-to-definition, and completions in editors. It is a separate binary that requires Go on `PATH`:

```bash
go install github.com/dullkingsman/dpg-lsp/cmd/dpg-lsp@latest
```

After installing, make sure `$(go env GOPATH)/bin` is on your `PATH`. Editor setup is covered in [Editor Integration](./editor-integration.md).

---

## Build from Source

### System Requirements (source build)

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

### Build for Current Platform

```bash
make build          # produces build/dpg
make install        # installs to $(go env GOPATH)/bin
```

### All Make Targets

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

### Version Information

The binary embeds version metadata at build time:

```bash
# Build with an explicit version tag
make build VERSION=v0.8.0

# The binary reports version info:
dpg --version
# dpg version v0.8.0 (commit: a3f7c91, built: 2026-04-27T00:00:00Z)
```

If built without `VERSION`, the value defaults to the output of `git describe --tags --always --dirty`, or `dev` if git is unavailable.

### Cross-Compilation

Because of the CGo requirement, cross-compilation requires a C cross-compiler. The recommended approach is `zig cc`, which provides hermetic cross-compilation:

```bash
# Install zig (https://ziglang.org/download/)
# Then use the provided Makefile targets:
make dist-linux      # uses zig cc for linux/arm64 if not on that arch
make dist-darwin     # requires macOS SDK (only works on macOS hosts)
```

For CI/CD release pipelines, consider using [goreleaser](https://goreleaser.com) with the Docker CGo cross-compilation approach (see `goreleaser` documentation for details).

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

Unit tests run without any external dependencies. Integration tests cover the full compile → plan → apply → introspect → zero-drift roundtrip against a live PostgreSQL 16 instance.
