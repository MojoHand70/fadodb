# Architecture decision records

One file per decision. A record is never edited after acceptance; a change is
a new record that supersedes the old one. Copy `0000-template.md` to start.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-license-and-name.md) | License and name | Accepted |
| [0002](0002-pure-go-core-and-dependency-budget.md) | Pure-Go core and dependency budget | Accepted |
| [0003](0003-bytecode-as-interchange-format.md) | Bytecode as the interchange format | Accepted |
| [0004](0004-number-semantics.md) | Number semantics: float64 with Excel rules | Accepted |
| [0005](0005-blank-semantics.md) | Blank and null semantics | Accepted |
| [0006](0006-event-log-as-source-of-truth.md) | Event log as the source of truth | Accepted |
| [0007](0007-one-table-per-class.md) | One table per document class | Accepted |
| [0008](0008-statechart-semantics.md) | Statechart semantics, SCXML subset | Accepted |
| [0009](0009-action-execution-model.md) | Action execution model: synchronous versus outbox | Accepted |
| [0010](0010-security-rules-as-predicates.md) | Security rules compiled to SQL predicates | Accepted |
| [0011](0011-one-json-command-set.md) | One JSON command set over HTTP, WebSocket and C ABI | Accepted |
| [0012](0012-performance-gates.md) | Performance budgets and gates | Accepted |
