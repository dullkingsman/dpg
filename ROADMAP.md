# Roadmap

This document describes the planned direction for DPG. It is a living document and will be updated as priorities shift. Items are not binding commitments.

For the full language specification see [rfc/v0.8.0.md](rfc/v0.8.0.md).

---

## v0.1.x — Stability (current)

Focus: correctness and reliability for the object types already supported.

- [ ] CI pipeline (GitHub Actions) — `go test`, `go vet`, `staticcheck` on push and PR
- [ ] Tests for `graph`, `merger`, `compiler`, `introspect`, `project`, `config` packages
- [ ] Resolve the function-body-not-captured code path in the differ — currently emits a SQL comment silently; should error or emit a `MANUAL`-tagged op
- [ ] Bug fixes driven by early adopter feedback

---

## v0.2.0 — Extensibility

Focus: allow third-party tools to integrate with the DPG pipeline without forking.

- [ ] Open `internal/pipeline` — promote the registry and core interfaces to a public, stable API so external packages can register custom differs, emitters, linters, and secret resolvers
- [ ] Documented extension points and an example plugin
- [ ] Stable Go module API (no breaking changes to public packages within the v0.x line)

---

## v0.3.0 — Broader Object Coverage

Focus: close the gaps in PostgreSQL object support per RFC Appendix A.

- [ ] Triggers (basic `CREATE TRIGGER` / `DROP TRIGGER`)
- [ ] Full-text search objects (`TEXT SEARCH CONFIGURATION`, `DICTIONARY`)
- [ ] Foreign Data Wrappers (`SERVER`, `USER MAPPING`, `FOREIGN TABLE`)
- [ ] Replication publications and subscriptions
- [ ] Tablespaces
- [ ] Row-level security policies (`POLICY` inside `{ }` blocks)
- [ ] Partitioning strategies and `--approve-partition-rebuild` flag enforcement

---

## v0.4.0 — Developer Experience

Focus: quality-of-life improvements for day-to-day use.

- [ ] `dpg init` — scaffold a new project interactively
- [ ] `dpg validate` — offline schema validation without diffing (syntax, type resolution, constraint sanity)
- [ ] Watch mode for `dpg plan` — re-run on source file changes
- [ ] JSON / machine-readable output flag for all commands
- [ ] Shell completion (Bash, Zsh, Fish) via Cobra

---

## v1.0.0 — Stable

Milestone criteria for a stable release:

- Public API (post v0.2.0) has been stable for at least two minor releases
- Core object types (tables, views, functions, types, roles, sequences, extensions) are fully covered by the differ and tested
- CI is green on Linux (amd64, arm64) and macOS (arm64)
- No known correctness bugs in plan/apply
- RFC v1.0.0 ratified

---

## Not Planned

- A graphical UI (the CLI is the interface)
- Support for databases other than PostgreSQL
- An ORM or query builder layer
- Automatic schema migration without explicit `dpg apply` approval
