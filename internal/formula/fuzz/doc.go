// Package fuzz generates random, well-typed formulas with inputs and runs
// each one on the Go VM and on a SQL dialect, reporting any divergence.
//
// It is the property-based half of "three evaluators, one behaviour"
// (ADR-0003): the conformance suite pins the behaviour somebody asked for,
// the generator searches for the behaviour nobody thought to ask about.
// Every formula it produces binds, so a bind error is a generator bug, and
// every divergence it finds can be pasted into the suite as a case.
//
// The SQLite runner in internal/formula/sqlitefn and the DuckDB runner in
// the analytics module drive it; FADODB_FUZZ_N, FADODB_FUZZ_SEED,
// FADODB_FUZZ_WORKERS and FADODB_FUZZ_MAXTEXT size a run.
package fuzz
