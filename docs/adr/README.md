# Architecture decision records

One file per decision. A record is never edited after acceptance; a change is
a new record that amends or supersedes the old one, and the old one's status
line says so. Copy `0000-template.md` to start.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-license-and-name.md) | License and name | Accepted |
| [0002](0002-pure-go-core-and-dependency-budget.md) | Pure-Go core and dependency budget | Accepted, amended by 0017 |
| [0003](0003-bytecode-as-interchange-format.md) | Bytecode as the interchange format | Accepted, amended by 0029, 0037 |
| [0004](0004-number-semantics.md) | Number semantics: float64 with Excel rules | Accepted, amended by 0019, 0022, 0024 |
| [0005](0005-blank-semantics.md) | Blank and null semantics | Accepted |
| [0006](0006-event-log-as-source-of-truth.md) | Event log as the source of truth | Accepted, amended by 0014, 0026, 0044 |
| [0007](0007-one-table-per-class.md) | One table per document class | Accepted, amended by 0014 |
| [0008](0008-statechart-semantics.md) | Statechart semantics, SCXML subset | Accepted, amended by 0039, 0040 |
| [0009](0009-action-execution-model.md) | Action execution model: synchronous versus outbox | Accepted, amended by 0027, 0044, 0045 |
| [0010](0010-security-rules-as-predicates.md) | Security rules compiled to SQL predicates | Accepted, amended by 0027, 0028, 0041 |
| [0011](0011-one-json-command-set.md) | One JSON command set over HTTP, WebSocket and C ABI | Accepted, amended by 0025, 0026, 0027, 0033, 0045 |
| [0012](0012-performance-gates.md) | Performance budgets and gates | Accepted, amended by 0030 |
| [0013](0013-document-variables-and-types.md) | Document variables and the type system | Accepted, amended by 0019, 0022, 0023, 0031, 0034 |
| [0014](0014-document-tables.md) | Document tables: current, history, child tables and views | Accepted, amended by 0026, 0031, 0039, 0040, 0043 |
| [0015](0015-commits-and-recalculation.md) | Commits, recalculation modes, defaults, reset and refresh | Accepted, amended by 0020, 0025, 0040, 0043 |
| [0016](0016-explicit-tables-and-query-functions.md) | Explicit tables, storage bindings, LOOKUP and QUERY | Accepted, amended by 0020, 0036, 0037 |
| [0017](0017-duckdb-as-the-analytical-engine.md) | DuckDB as the analytical engine, full and lite builds | Accepted, amended by 0035 |
| [0018](0018-class-metadata-layouts-and-wire-format.md) | JSON for definitions and interchange: class metadata, layouts and the wire format | Accepted, amended by 0022, 0023, 0028, 0031, 0032, 0033, 0034, 0039, 0043 |
| [0019](0019-formula-language-semantics.md) | Formula language semantics: grammar, types, coercion, comparison, dates, text, composites and errors | Accepted, amended by 0021, 0022, 0023, 0024, 0031, 0043 |
| [0020](0020-query-function-execution.md) | LOOKUP and QUERY execution: one engine call per invocation | Accepted, amended by 0036, 0037 |
| [0021](0021-size-limits.md) | Size limits: one text limit per database, enforced before building | Accepted, amended by 0033 |
| [0022](0022-exact-decimal-int-and-money.md) | Exact numbers: decimal, int and money | Accepted |
| [0023](0023-calendar-day-datetime-and-enum-values.md) | A date is a calendar day, a datetime an instant, an enum a text with a value set | Accepted |
| [0024](0024-division-by-zero-is-an-error-and-iferror.md) | Division by zero is an error, and IFERROR catches one | Accepted |
| [0025](0025-evaluate-without-committing.md) | Evaluate a class or a document without committing | Accepted, amended by 0042, 0043 |
| [0026](0026-idempotency-keys-business-dates-and-the-transition-event.md) | Idempotency keys on commands, a business date on events, and the transition event | Accepted, amended by 0040, 0044, 0045 |
| [0027](0027-principal-from-the-embedder-and-registered-executors.md) | The principal comes from the embedder; action executors are registered | Accepted, amended by 0044, 0045 |
| [0028](0028-field-level-rights-per-state.md) | Field-level rights per state | Accepted, amended by 0041 |
| [0029](0029-browser-vm-as-one-plain-javascript-file.md) | The browser VM ships as one plain JavaScript file | Accepted |
| [0030](0030-benchmark-gate-measures-the-minimum-over-placements.md) | The benchmark gate measures the minimum over code placements | Accepted |
| [0031](0031-named-schemas-and-object-values.md) | Named schemas and object values | Accepted |
| [0032](0032-predefined-vocabulary-and-object-presentation.md) | A predefined vocabulary of objects, and how an object is presented | Accepted |
| [0033](0033-attachments-as-file-values-behind-a-blob-store.md) | Attachments are File values; the bytes live behind a blob store | Accepted |
| [0034](0034-range-constraints-on-numeric-variables.md) | Range constraints on numeric variables | Accepted |
| [0035](0035-sqlite-is-the-transactional-store.md) | SQLite is the transactional store, by measurement | Accepted |
| [0036](0036-in-memory-index-for-lookups-in-commits.md) | An in-memory index serves LOOKUP inside commits, by measurement | Accepted |
| [0037](0037-explicit-tables-and-the-compiled-lookup.md) | Explicit tables bound to SQLite and the compiled LOOKUP | Accepted |
| [0038](0038-the-filter-and-the-read-only-sql-endpoint.md) | The filter and the read-only SQL endpoint | Accepted |
| [0039](0039-class-versions-terminal-states-and-migrations.md) | Class versions, terminal states and migrations | Accepted, amended by 0040, 0043 |
| [0040](0040-transitions-as-commits.md) | Transitions as commits: the command, eventless chains and state visits | Accepted, amended by 0043 |
| [0041](0041-field-rights-and-transition-rules-in-the-go-api.md) | Field rights and transition rules in the Go API | Accepted |
| [0042](0042-a-moment-the-caller-chooses.md) | A moment the caller chooses, for previews and reads | Accepted |
| [0043](0043-rules-inside-the-commit.md) | Rules inside the commit: triggers, synchronous actions and triggered variables | Accepted, amended by 0044 |
| [0044](0044-the-outbox.md) | The outbox: what a commit queues, the worker and the engine's own actions | Accepted |
| [0045](0045-actions-are-an-extension-point.md) | Actions are an extension point: definitions, declared HTTP actions, registered Go actions, connections and results | Accepted |
| [0046](0046-formulas-that-read-other-documents.md) | Formulas that read other documents: a class as the source of LOOKUP, with COUNT, SUM, MIN and MAX | Proposed |
