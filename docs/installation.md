# Installation

## System Requirements

| Requirement | Minimum |
|---|---|
| Go | 1.25 or later |
| PostgreSQL target | 14 or later |
| CGo toolchain | Required — `pg_query_go` uses libpg_query (C library) |
| GCC / Clang | Must be on `PATH` for the CGo build |

Because `pg_query_go` links against the real PostgreSQL C parser, a C compiler must be present. Pure-Go cross-compilation is **not** possible; each target platform must be built on that platform or with a compatible CGo cross-compilation toolchain.

## Build from Source

```bash
git clone https://github.com/dullkingsman/dpg
cd dpg
```

### Build for Current Platform

```bash
make build          # produces ./dpg in project root
make install        # installs to $(go env GOPATH)/bin
```

### All Make Targets

| Target | Description |
|---|---|
| `make build` | Compile for the current OS/arch, output `./dpg` |
| `make install` | `go install` to `$GOPATH/bin` |
| `make test` | `go test ./...` |
| `make test-verbose` | `go test ./... -v` |
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
make test            # unit + integration tests (no live database required)
make test-examples   # pipeline examples (compilation, diffing, linting, portability)
```

The full test suite runs without a live PostgreSQL connection. All diffing and compilation tests use in-memory state or fixture files.
