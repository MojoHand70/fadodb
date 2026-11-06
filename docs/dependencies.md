# Dependency budget

The dependency gate (`scripts/depgate.sh`) fails the build when `go.mod` has a
direct requirement that is not listed in the table below. Adding a row needs a
justification and a reviewer who agrees with it. See
[ADR-0002](adr/0002-pure-go-core-and-dependency-budget.md).

## Core module (`github.com/MojoHand70/fadodb`)

| Module | License | Status | Justification |
|---|---|---|---|
| Go standard library | BSD-3 | in use | Everything the core needs for HTTP, JSON, crypto and testing. |
| `modernc.org/sqlite` | BSD-3 | approved, not yet added | Pure-Go SQLite. Keeps the core free of cgo so it cross-compiles and embeds anywhere. Added in Phase 1 for the SQL conformance runner (ADR-0020). |

## Separate modules

These live in their own Go modules so their weight and cgo never reach the core.

| Module | Used by | License | Why separate |
|---|---|---|---|
| `github.com/duckdb/duckdb-go/v2` with `go.duckdb.org/libduckdb/<os>-<arch>` | `analytics/` | MIT | Needs cgo and a C compiler; a minimal binary linking DuckDB 2.0.0 is 75 MB. Prebuilt libraries for five platforms; `duckdb_use_lib` links a system library instead. |
| `github.com/jackc/pgx/v5` | `postgres/` | MIT | Driver and its tree are only needed by the enterprise backend. |

## Developer tools

Installed with `go install`, never added to `go.mod`.

| Tool | Purpose |
|---|---|
| `honnef.co/go/tools/cmd/staticcheck` | Lint |
| `golang.org/x/vuln/cmd/govulncheck` | Vulnerability scan |
| `golang.org/x/perf/cmd/benchstat` | Benchmark comparison in the benchmark gate |
| `github.com/goreleaser/goreleaser/v2` | Release builds, run by the release workflow |
