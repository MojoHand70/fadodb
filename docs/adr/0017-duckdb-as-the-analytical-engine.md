# ADR-0017: DuckDB as the analytical engine, full and lite builds

- Status: Accepted; amends ADR-0002
- Date: 2026-11-05

## Context

ADR-0002 keeps DuckDB out of the core library and treated it as an optional
analytics add-on. That understates its role: many formulas will depend on
analytical queries over tables and over the document stack, and those run
on DuckDB. The product needs DuckDB inside; the library must stay pure Go
for embedding, cross-compiling and the C ABI.

## Decision

- DuckDB is FadoDB's analytical engine. `QUERY` over stacks, set-wise
  recomputes of triggered variables, analytics and process mining run on it.
- Two builds from one code base. `fadodb`, the reference product, compiles
  DuckDB in, binds explicit tables to `duckdb` by default, attaches external
  databases through DuckDB, and keeps a projection of every document table
  fed append-only from the history and event tables. `fadodb-lite` is pure
  Go: the analytical engine interface is implemented on SQLite with a
  documented subset and lower limits. The library and the C ABI are the lite
  build. The 15 MB size gate applies to the lite build.
- The transactional store is SQLite in both builds, as an expectation to be
  measured, not a fact: the first Phase 2 benchmark writes 100,000 commits
  into SQLite and into DuckDB and reports latency and throughput. The
  storage interface keeps the decision cheap to flip, and the result is
  recorded in a follow-up ADR.
- The projection lags the transactional store by its feed delay. Triggered
  variables record the version they were computed at; the documentation
  states that analytical values are consistent as of that moment.
- Any DuckDB extension the full build relies on is installed at build time
  and verified without network access in CI, since the client bundles only
  ICU, JSON, Parquet and Autocomplete.
- The full build is built natively per operating system; windows/arm64 ships
  the lite build only.

## Consequences

- ADR-0002's decision stands: the core module has no cgo dependency. Its
  "optional add-on" framing is replaced by this record.
- The release pipeline grows a per-OS matrix for the full build; the lite
  build keeps the single cross-compiling job.
- Users of the lite build get the same formula language with slower and
  narrower `QUERY`, stated in the capability table of the docs.

## Performance

Budgets: `mining_dfg` over 10,000,000 state visits under 2 s in the full
build; set-wise recompute over 100,000 documents under 2 s; the primary-store
benchmark decides latency and throughput of commits for both candidates.
