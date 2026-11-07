// Package bench is the end-to-end performance suite: the scenarios behind
// the budgets of ADR-0012, written against database/sql so that the same
// scenario runs on every storage engine under consideration.
//
// Two scenarios open Phase 2, as ADR-0017 and ADR-0020 ask:
//
//   - The primary-store benchmark (RunStore) writes commits in the shape
//     of ADR-0014 and ADR-0026, the current row, the history row, the child
//     rows and the event rows in one transaction, and reports latency and
//     throughput, unbatched and batched. Its result decides the
//     transactional store.
//   - The point-lookup benchmark (RunLookup) measures one keyed lookup
//     against a reference table on a Go map and through a prepared
//     statement, at several table sizes. Its result fixes the size up to
//     which an auto formula's LOOKUP is served from memory.
//
// The SQLite target lives here; the DuckDB target lives in the analytics
// module, which is where cgo is allowed. The tests of each module run a
// small smoke scenario always and the full campaign when FADODB_BENCH is
// set; see the tests for the variables.
package bench
