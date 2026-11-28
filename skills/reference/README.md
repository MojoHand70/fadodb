# The shared reference set

The five skills share one body of fact: the class format, the formula
language and its function catalogue, the statechart, rights and rules,
commits and previews, querying, and the diagnostic codes. It is written
once here and each SKILL.md points into it, so nothing is said twice and
nothing drifts.

**Status on 2026-11-28: the reference set is the manual**, under
[`../../docs/manual/`](../../docs/manual/), whose examples a test runs
against the engine on each push. The class format, with the
statechart's syntax and the rights and rules, is
[`class.md`](../../docs/manual/class.md). The list of functions, the
list of commands and the list of refusal and diagnostic codes are
[`functions.md`](../../docs/manual/functions.md),
[`commands.md`](../../docs/manual/commands.md) and
[`refusals.md`](../../docs/manual/refusals.md): `make docs` generates
the three from the engine's own tables, and a test fails when they
fall behind. A skill that needs a fact reads the chapter that holds
it; [`../../docs/README.md`](../../docs/README.md) lists them all.

| Fact | Chapter of the manual | Decision records |
|---|---|---|
| the class format | [`class.md`](../../docs/manual/class.md), [`schemas-and-objects.md`](../../docs/manual/schemas-and-objects.md), [`files.md`](../../docs/manual/files.md), [`tables.md`](../../docs/manual/tables.md) | ADR-0013 (variables and types), ADR-0014 (tables), ADR-0018 (metadata and wire format), ADR-0031 and ADR-0032 (schemas, objects, vocabulary), ADR-0033 (attachments), ADR-0034 (ranges), ADR-0039 (versions and terminal states) |
| formulas | [`formula-language.md`](../../docs/manual/formula-language.md) | ADR-0019 (grammar, coercion, comparison, dates, text, errors), ADR-0004 (numbers), ADR-0005 (blanks), ADR-0022 (exact money), ADR-0023 (calendar day), ADR-0024 (division and IFERROR) |
| functions | [`functions.md`](../../docs/manual/functions.md), generated from `internal/formula/catalog.json`, the function catalogue the conformance suite runs | ADR-0074 (the engine hands out its list of functions) |
| the statechart | [`class.md`](../../docs/manual/class.md), [`notions/states-and-transitions.md`](../../docs/manual/notions/states-and-transitions.md) | ADR-0008 (semantics), ADR-0040 (transitions as commits, eventless chains, snapshots, the `_states` view) |
| rights and rules | [`notions/who-may.md`](../../docs/manual/notions/who-may.md), [`notions/rules-and-actions.md`](../../docs/manual/notions/rules-and-actions.md) | ADR-0028 and ADR-0041 (field rights per state, per-transition rules), ADR-0010 (access as predicates), ADR-0009 (actions) |
| commits and previews | [`notions/history-and-the-log.md`](../../docs/manual/notions/history-and-the-log.md), [`commands.md`](../../docs/manual/commands.md) | ADR-0015 (commits and recalculation), ADR-0025 (evaluate without committing), ADR-0026 (idempotency keys, business dates), ADR-0027 (the principal), ADR-0042 (a moment the caller chooses) |
| querying | [`views-and-sql.md`](../../docs/manual/views-and-sql.md), [`tables.md`](../../docs/manual/tables.md), [`lists.md`](../../docs/manual/lists.md) | ADR-0016 and ADR-0020 (LOOKUP and QUERY), ADR-0037 (explicit tables), ADR-0038 (the filter and the read-only SQL endpoint), ADR-0007 (one table per class) |
| diagnostics | [`refusals.md`](../../docs/manual/refusals.md), generated from the `Code` constants of `internal/model/diagnostic.go` | |

The skills lean on these later records as well, each with the chapter
that teaches it:

| Record | What it decides | Chapter |
|---|---|---|
| ADR-0075 | the engine makes its own indexes, and says which question it cannot serve | [`lists.md`](../../docs/manual/lists.md) |
| ADR-0076 | a value worked out when it is read: the mode `live`, the list of other documents, and what a `ref` compares with | [`live-variables.md`](../../docs/manual/live-variables.md), [`lists.md`](../../docs/manual/lists.md) |
| ADR-0078 | a decision stands on what the document holds: the version a command saw, who may bring a value again, and what a condition saw | [`triggered-variables.md`](../../docs/manual/triggered-variables.md), [`guides/act-on-what-you-read.md`](../../docs/manual/guides/act-on-what-you-read.md) |
| ADR-0081 | functions over a list, each with its SQL from the start | [`lists.md`](../../docs/manual/lists.md) |
| ADR-0085 | lists in and out: the lines of a message, the lines of a request | [`a-way-in-for-messages.md`](../../docs/manual/a-way-in-for-messages.md), [`docs/actions.md`](../../docs/actions.md) |
| ADR-0089 | the copy beside the documents: how the analytical engine is fed and asked | [`opening-a-database.md`](../../docs/manual/opening-a-database.md#an-analytical-engine), [`reading-with-duckdb.md`](../../docs/manual/reading-with-duckdb.md) |
| ADR-0090 | a total among the columns of a `QUERY`: one line for each group | [`lists.md`](../../docs/manual/lists.md) |
| ADR-0091 | a kept value that follows its table | [`triggered-variables.md`](../../docs/manual/triggered-variables.md#following-a-table) |
| ADR-0092 | the app System and its tables: what the database holds, read as any table | [`system-tables.md`](../../docs/manual/system-tables.md) |
| ADR-0093 | a kept value that follows the clock, and the time zone of a database | [`triggered-variables.md`](../../docs/manual/triggered-variables.md#following-the-clock) |
| ADR-0094 | the rows of a table stay in the store, and the copy holds them too | [`tables.md`](../../docs/manual/tables.md#a-large-table) |
| ADR-0095 | how the stays of a state spread: the middle stay and the slow tenth | [`guides/see-where-documents-wait.md`](../../docs/manual/guides/see-where-documents-wait.md) |
| ADR-0096 | how many documents were made and finished in each period | [`guides/see-where-documents-wait.md`](../../docs/manual/guides/see-where-documents-wait.md) |
| ADR-0097 | the paths documents took | [`guides/see-where-documents-wait.md`](../../docs/manual/guides/see-where-documents-wait.md) |
| ADR-0098 | comments on a document | [`comments.md`](../../docs/manual/comments.md) |
| ADR-0099 | where a value comes from, and the documents behind a number | [`guides/where-a-value-comes-from.md`](../../docs/manual/guides/where-a-value-comes-from.md), [`tables.md`](../../docs/manual/tables.md#the-history-of-the-rows) |

The decision records live in [`../../docs/adr/`](../../docs/adr/), with
each one's status in its index; the
conformance suite in [`../../conformance/`](../../conformance/) is the
specification of the formula language.
