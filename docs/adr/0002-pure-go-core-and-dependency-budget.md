# ADR-0002: Pure-Go core and dependency budget

- Status: Accepted
- Date: 2026-11-05

## Context

FadoDB must embed into other programs, cross-compile for every platform Go
supports, and ship as one small binary. Two facts shape the choice:

- DuckDB's Go bindings carry a static library of about 82 MB per platform and
  require cgo. Linking it into the core would multiply binary size and
  complicate every build.
- `modernc.org/sqlite` is a pure-Go SQLite. It costs some throughput against
  the cgo driver, measured in Phase 2, but removes the C toolchain from the
  core entirely.

Every extra module also widens the supply-chain surface and the maintenance
load of a small team.

## Decision

- The core module `github.com/MojoHand70/fadodb` depends on the Go standard
  library and `modernc.org/sqlite` only.
- Anything that needs cgo or a database driver lives in a separate Go module
  under the same repository: `analytics/` for DuckDB, `postgres/` for pgx.
- Every direct dependency, in any module, must be listed in
  `docs/dependencies.md` with a license and a justification. The dependency
  gate in CI enforces this for the core.
- Developer tools are installed with `go install` and never appear in
  `go.mod`.

## Consequences

- Release builds use `CGO_ENABLED=0` and cross-compile from one machine.
- Some conveniences are written by hand: a Prometheus text exporter, a small
  router, JSON schema validation for class definitions. Each is a few hundred
  lines and under our control.
- If the pure-Go SQLite misses the Phase 2 budgets, the fallback is a build
  tag selecting the cgo driver for people who accept a C toolchain, not a
  change to the core default.

## Performance

Budget: core binary at or under 15 MB, enforced by `scripts/sizegate.sh`.
Store throughput budgets are in ADR-0012 and are measured against the pure-Go
driver.
