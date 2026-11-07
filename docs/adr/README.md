# Architecture decision records

One file per decision. A record is never edited after acceptance; a change is
a new record that amends or supersedes the old one, and the old one's status
line says so. Copy `0000-template.md` to start.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-license-and-name.md) | License and name | Accepted |
| [0002](0002-pure-go-core-and-dependency-budget.md) | Pure-Go core and dependency budget | Accepted, amended by 0017 |
| [0003](0003-bytecode-as-interchange-format.md) | Bytecode as the interchange format | Accepted, amended by 0029 |
| [0004](0004-number-semantics.md) | Number semantics: float64 with Excel rules | Accepted, amended by 0019, 0022, 0024 |
| [0005](0005-blank-semantics.md) | Blank and null semantics | Accepted |
| [0006](0006-event-log-as-source-of-truth.md) | Event log as the source of truth | Accepted, amended by 0014, 0026 |
| [0007](0007-one-table-per-class.md) | One table per document class | Accepted, amended by 0014 |
| [0008](0008-statechart-semantics.md) | Statechart semantics, SCXML subset | Accepted |
| [0009](0009-action-execution-model.md) | Action execution model: synchronous versus outbox | Accepted, amended by 0027 |
| [0010](0010-security-rules-as-predicates.md) | Security rules compiled to SQL predicates | Accepted, amended by 0027, 0028 |
| [0011](0011-one-json-command-set.md) | One JSON command set over HTTP, WebSocket and C ABI | Accepted, amended by 0025, 0026, 0027 |
| [0012](0012-performance-gates.md) | Performance budgets and gates | Accepted |
| [0013](0013-document-variables-and-types.md) | Document variables and the type system | Accepted, amended by 0019, 0022, 0023 |
| [0014](0014-document-tables.md) | Document tables: current, history, child tables and views | Accepted, amended by 0026 |
| [0015](0015-commits-and-recalculation.md) | Commits, recalculation modes, defaults, reset and refresh | Accepted, amended by 0020, 0025 |
| [0016](0016-explicit-tables-and-query-functions.md) | Explicit tables, storage bindings, LOOKUP and QUERY | Accepted, amended by 0020 |
| [0017](0017-duckdb-as-the-analytical-engine.md) | DuckDB as the analytical engine, full and lite builds | Accepted |
| [0018](0018-class-metadata-layouts-and-wire-format.md) | JSON for definitions and interchange: class metadata, layouts and the wire format | Accepted, amended by 0022, 0023, 0028 |
| [0019](0019-formula-language-semantics.md) | Formula language semantics: grammar, types, coercion, comparison, dates, text, composites and errors | Accepted, amended by 0021, 0022, 0023, 0024 |
| [0020](0020-query-function-execution.md) | LOOKUP and QUERY execution: one engine call per invocation | Accepted |
| [0021](0021-size-limits.md) | Size limits: one text limit per database, enforced before building | Accepted |
| [0022](0022-exact-decimal-int-and-money.md) | Exact numbers: decimal, int and money | Proposed |
| [0023](0023-calendar-day-datetime-and-enum-values.md) | A date is a calendar day, a datetime an instant, an enum a text with a value set | Proposed |
| [0024](0024-division-by-zero-is-an-error-and-iferror.md) | Division by zero is an error, and IFERROR catches one | Proposed |
| [0025](0025-evaluate-without-committing.md) | Evaluate a class or a document without committing | Proposed |
| [0026](0026-idempotency-keys-business-dates-and-the-transition-event.md) | Idempotency keys on commands, a business date on events, and the transition event | Proposed |
| [0027](0027-principal-from-the-embedder-and-registered-executors.md) | The principal comes from the embedder; action executors are registered | Proposed |
| [0028](0028-field-level-rights-per-state.md) | Field-level rights per state | Proposed |
| [0029](0029-browser-vm-as-one-plain-javascript-file.md) | The browser VM ships as one plain JavaScript file | Proposed |
