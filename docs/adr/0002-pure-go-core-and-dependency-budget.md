# ADR-0002: Pure-Go core and dependency budget

- Status: Accepted
- Date: 2026-11-05
- Revised: 2026-11-05, to correct the DuckDB Go client facts after checking the
  DuckDB 2.0 client documentation and measuring the current client. The
  decision is unchanged.

## Context

FadoDB must embed into other programs, cross-compile for every platform Go
supports, and ship as one small binary. Two facts shape the choice.

The DuckDB Go client, `github.com/duckdb/duckdb-go/v2`, is written against a
C++ engine. Per its documentation (https://duckdb.org/docs/2.0/clients/go/overview):

- It uses cgo and needs a C compiler. By default it statically links a
  prebuilt DuckDB library, fetched as a `go.duckdb.org/libduckdb/<os>-<arch>`
  module, so no separate DuckDB installation is needed. Prebuilt libraries
  exist for macOS amd64 and arm64, Linux amd64 and arm64, and Windows amd64.
- The build tag `duckdb_use_lib` links dynamically against a system
  `libduckdb` instead, which keeps the Go binary small but requires the
  shared library to be installed where the program runs. The build tag
  `duckdb_use_static_lib` links a custom-built static library.
- The client version encodes the bundled engine version: v2.20000.0 bundles
  DuckDB 2.0.0.
- The prebuilt library bundles the ICU, JSON, Parquet and Autocomplete
  extensions. Other extensions, including `sqlite`, are installed from the
  extension repository on first use, which needs network access or a
  pre-populated extension directory.

Measured on 2026-11-05 with v2.20000.0 on linux/amd64: a minimal Go program
that opens an in-memory DuckDB and runs one query, built with `-s -w`, is
75 MB. Linking that into the core would multiply its size by about fifty and
put a C toolchain into every build.

`modernc.org/sqlite` is a pure-Go SQLite. It costs some throughput against
the cgo driver, measured in Phase 2, but removes the C toolchain from the
core entirely.

Every extra module also widens the supply-chain surface and the maintenance
load of a small team.

## Decision

- The core module `github.com/MojoHand70/fadodb` depends on the Go standard
  library and `modernc.org/sqlite` only.
- Anything that needs cgo or a database driver lives in a separate Go module
  under the same repository: `analytics/` for DuckDB, `postgres/` for pgx.
- The analytics module defaults to the client's static prebuilt library, so
  it is self-contained, and offers a `duckdb_use_lib` build for deployments
  that prefer a shared system library. It must work without network access,
  so it documents how to pre-install the `sqlite` extension and verifies that
  in CI.
- Every direct dependency, in any module, must be listed in
  `docs/dependencies.md` with a license and a justification. The dependency
  gate in CI enforces this for the core.
- Developer tools are installed with `go install` and never appear in
  `go.mod`.

## Consequences

- Release builds of the core use `CGO_ENABLED=0` and cross-compile from one
  machine. The analytics module is built per platform with a C compiler.
- Some conveniences are written by hand: a Prometheus text exporter, a small
  router, JSON schema validation for class definitions. Each is a few hundred
  lines and under our control.
- If the pure-Go SQLite misses the Phase 2 budgets, the fallback is a build
  tag selecting the cgo driver for people who accept a C toolchain, not a
  change to the core default.

## Performance

Budget: core binary at or under 15 MB, enforced by `scripts/sizegate.sh`.
Store throughput budgets are in ADR-0012 and are measured against the pure-Go
driver. The analytics binary has no size budget; its cost is the 75 MB above
and is reported with each release.
