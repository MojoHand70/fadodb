# ADR-0016: Explicit tables, storage bindings, LOOKUP and QUERY

- Status: Accepted; amended by ADR-0020 (run-time execution of LOOKUP and QUERY)
- Date: 2026-11-05

## Context

Formulas need reference data and analytical results: rates, price lists,
aggregates over other documents. Raw SQL inside formulas would be impossible
to secure, cache or port across SQLite, DuckDB and Postgres. Users also want
to bring their own database, which makes the table's storage the natural
extensibility point.

## Decision

- An explicit **table** is a first-class named object: a schema in the
  variable type system, indexes, `read` and `write` access rules, and a
  **storage binding**: `sqlite` (default in the lite build), `duckdb`
  (default in the full build), or `external` (a connector: anything DuckDB
  can attach in the full build, a `database/sql` driver in the lite build).
- Behind every binding is one Go interface: dialect, query, exec, schema,
  capabilities. Implementing it is how a third party hooks up their database.
- Formulas reach tables and document stacks through two compiled functions:
  `LOOKUP(source, column, condition)` returning a scalar, and
  `QUERY(source, columns, condition, sort, groupby, limit)` returning `rows`.
  A source is an explicit table or a stack view such as `Invoice` or
  `Invoice_history`. Columns may be aggregates. Inside both functions bare
  names are the source's columns and the document's variables are
  `THIS.name`.
- The compiler translates a call to the dialect of the source's binding,
  binds the condition to the source's columns, injects the caller's access
  predicate when the source is a stack, caches results per argument set for
  triggered variables, and rejects at definition time anything it cannot
  compile. Formulas never carry raw SQL.
- `LOOKUP` in auto formulas is served from an in-memory index for small
  tables and a prepared statement otherwise; a change to the table is a
  `table:` trigger for the documents that depend on it.
- Tables are created through a DDL subset, loaded through a bulk import
  command, and changed through the `table.*` actions.
- The read-only SQL endpoint remains a separate surface for analysts and BI
  tools, over the same views and predicates.

## Consequences

- Joins across bindings exist only where one engine sees everything, which
  is DuckDB in the full build (ADR-0017). The lite build answers from SQLite
  with a documented subset of capabilities.
- Portability and security are properties of the compiler, not of each
  author's SQL.
- `THIS.` is a deliberate choice: query conditions mostly talk about the
  source, as in SQL. The conformance suite fixes the scoping rule.

## Performance

Budgets: cached `LOOKUP` under 1 µs, cold under 100 µs against an indexed
table; `QUERY` cost is the engine's, reported per build in `docs/perf/`.
