// Package analytics is FadoDB's DuckDB module (ADR-0017): a separate Go
// module so that cgo and the DuckDB library never reach the core. It has
// the analytical engine a program gives the database it opens, Engine,
// which keeps a copy of the documents in a DuckDB file that the database
// feeds itself (ADR-0089, step 1); the conformance runner for the DuckDB
// dialect of the SQL generator (ADR-0020); and the DuckDB target of the
// Phase 2 store benchmarks (ADR-0017). The questions answered from the
// copy, the duckdb and external bindings and the full build arrive with
// the later steps of ADR-0089.
//
// Build it with a C compiler. The DuckDB Go client links a prebuilt
// library by default; the duckdb_use_lib build tag links a system
// libduckdb instead (ADR-0002).
package analytics
