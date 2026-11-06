# Architecture decision records

One file per decision. A record is never edited after acceptance; a change is
a new record that amends or supersedes the old one, and the old one's status
line says so. Copy `0000-template.md` to start.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-license-and-name.md) | License and name | Accepted |
| [0002](0002-pure-go-core-and-dependency-budget.md) | Pure-Go core and dependency budget | Accepted, amended by 0017 |
| [0003](0003-bytecode-as-interchange-format.md) | Bytecode as the interchange format | Accepted |
| [0004](0004-number-semantics.md) | Number semantics: float64 with Excel rules | Accepted |
| [0005](0005-blank-semantics.md) | Blank and null semantics | Accepted |
| [0006](0006-event-log-as-source-of-truth.md) | Event log as the source of truth | Accepted, amended by 0014 |
| [0007](0007-one-table-per-class.md) | One table per document class | Accepted, amended by 0014 |
| [0008](0008-statechart-semantics.md) | Statechart semantics, SCXML subset | Accepted |
| [0009](0009-action-execution-model.md) | Action execution model: synchronous versus outbox | Accepted |
| [0010](0010-security-rules-as-predicates.md) | Security rules compiled to SQL predicates | Accepted |
| [0011](0011-one-json-command-set.md) | One JSON command set over HTTP, WebSocket and C ABI | Accepted |
| [0012](0012-performance-gates.md) | Performance budgets and gates | Accepted |
| [0013](0013-document-variables-and-types.md) | Document variables and the type system | Accepted |
| [0014](0014-document-tables.md) | Document tables: current, history, child tables and views | Accepted |
| [0015](0015-commits-and-recalculation.md) | Commits, recalculation modes, defaults, reset and refresh | Accepted |
| [0016](0016-explicit-tables-and-query-functions.md) | Explicit tables, storage bindings, LOOKUP and QUERY | Accepted |
| [0017](0017-duckdb-as-the-analytical-engine.md) | DuckDB as the analytical engine, full and lite builds | Accepted |
| [0018](0018-class-metadata-layouts-and-wire-format.md) | JSON for definitions and interchange: class metadata, layouts and the wire format | Accepted |
