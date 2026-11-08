# ADR-0020: LOOKUP and QUERY execution: one engine call per invocation

- Status: Accepted; amends ADR-0015 and ADR-0016; amended by ADR-0036 (the index threshold), ADR-0037 (the condition as a program of its own, LOOKUP in this build)
- Date: 2026-11-06

## Context

ADR-0016 compiles `LOOKUP` and `QUERY` to the SQL dialect of the source's
binding but does not say how the VM and the engine meet at run time. That
is where the predecessor engine lost its performance: it measured a Go
round trip per row at 10 to 50 times the cost of one SQL statement over the
same rows. The cost was the boundary, a cgo transition, a planner run and
result marshalling for every row, not Go iteration, which runs at
nanoseconds per element.

The same boundary has a fixed cost per statement on the analytical engine,
in the order of tens to hundreds of microseconds. That is nothing for a
query returning a thousand rows and everything for a point lookup inside a
commit budgeted at 2 ms, with the lookup itself at 100 µs cold (ADR-0016).

## Decision

1. **The VM never reads source rows.** A `LOOKUP` or `QUERY` call compiles
   to one instruction, `OpLookup` or `OpQuery`, whose operand indexes a
   query slot of the program. A slot holds the source, the column list,
   condition, sort, group-by and limit as SQL per dialect generated at bind
   time, and the document variables bound as `THIS.` arguments at run time.
   Bytecode v1 reserves the two opcodes and the slot table from the first
   release.
2. **One engine call per invocation.** The VM hands the slot and the bound
   arguments to a `QueryExecutor` interface with two methods: `Lookup`
   returns a scalar, `Query` returns rows. An implementation runs one
   prepared statement and copies the result column-wise into the `Rows`
   value in a single pass. Nothing crosses the Go-to-engine boundary per
   row, in either direction.
3. **The source's binding chooses the engine** (ADR-0016, ADR-0017). A table
   bound to `sqlite` is answered by the transactional store's prepared
   statement; `duckdb` and `external` by DuckDB in the full build; a stack
   view by the DuckDB projection in the full build and by the SQLite views
   in the lite build.
4. **Point lookups in commits stay off the analytical engine.** `LOOKUP` in
   an auto formula, which ADR-0015 allows only against explicit tables, is
   served from an in-memory index in Go while the table is below a size
   threshold and from a SQLite prepared statement above it, as ADR-0016
   says, and never from DuckDB inside a commit. The threshold and the choice
   are confirmed by a point-lookup benchmark at the start of Phase 2, Go map
   against SQLite against DuckDB, and recorded in a follow-up record.
5. **Set-wise work is one statement per class.** The `table:` and
   `schedule:` recomputes of ADR-0015 translate the whole formula, with its
   `LOOKUP` calls as correlated subqueries or joins and its `QUERY` calls as
   subqueries keyed by document id, into one statement per class, and apply
   the results as batched system commits. The SQL generator exists for this
   as much as for views and predicates.
6. **Semantics.** `LOOKUP` with no matching row is BLANK. With several
   matching rows it returns the first in the source's key order, as
   `VLOOKUP` returns the first match, implemented as `ORDER BY key LIMIT 1`
   so that every engine agrees. `QUERY` returns rows in the requested sort,
   or key order when none is given; `limit` defaults to 1,000 and may not
   exceed the 10,000 hard cap of ADR-0013; result columns carry the source's
   types and aggregates are `number`. Conditions, columns and sort use pure
   functions, source columns and `THIS.` references, blanks follow ADR-0005
   through `COALESCE`, and a query function may not nest inside another's
   arguments in 1.0; the binder rejects it. The caller's access predicate is
   injected when the source is a stack (ADR-0016). Definition-time errors
   are `unknown_source` and `unknown_column`; the run-time error
   `query_failed` carries the engine's message.
7. **Conformance from Phase 1.** The suite holds `LOOKUP` and `QUERY` cases
   with their source data inline. Phase 1 runs them against an in-memory
   executor that implements the semantics above, Phase 2 against SQLite and
   Phase 8 against DuckDB. The browser VM does not execute them and
   receives the last computed value (ADR-0003).
8. **DuckDB is tested from Phase 1.** The `analytics/` module is created in
   Phase 1 holding only the conformance runner for the DuckDB dialect, built
   in a cgo job on Linux in CI; its product code arrives in Phase 8. The
   SQLite runner uses `modernc.org/sqlite` from Phase 1, one phase earlier
   than the dependency table planned, within the same budget.

## Consequences

- The hot path of a commit stays in Go, and the analytical engine sees only
  set-shaped work, which is what it is good at.
- `LOOKUP` and `QUERY` behave the same on every binding, so a class moves
  between the lite and the full build without changing a formula; the full
  build is faster and can join across bindings, not different.
- A third-party binding implements `QueryExecutor` and a dialect; the
  contract is small.
- One statement per class for recomputes makes the SQL generator
  correctness-critical from Phase 1, which the differential tests cover.

## Performance

Budgets from ADR-0016: cached `LOOKUP` under 1 µs and cold under 100 µs
against an indexed table. `QUERY` costs one engine round trip plus the
engine's own time, reported per build in `docs/perf/`. Set-wise recompute of
one triggered variable over 100,000 documents under 2 s (ADR-0015).
Benchmarks live next to each `QueryExecutor` implementation, and the Phase 2
point-lookup comparison decides the index threshold.
