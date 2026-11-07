// Package analytics is FadoDB's DuckDB module (ADR-0017): a separate Go
// module so that cgo and the DuckDB library never reach the core. Today it
// holds the conformance runner for the DuckDB dialect of the SQL generator
// (ADR-0020) and the DuckDB target of the Phase 2 store benchmarks
// (ADR-0017); the projection of document tables, the duckdb and external
// bindings, set-wise recomputes and the mining views arrive in Phase 8.
//
// Build it with a C compiler. The DuckDB Go client links a prebuilt
// library by default; the duckdb_use_lib build tag links a system
// libduckdb instead (ADR-0002).
package analytics
