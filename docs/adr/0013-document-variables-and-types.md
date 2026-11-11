# ADR-0013: Document variables and the type system

- Status: Accepted; amended by ADR-0019 (formula keywords are reserved names), ADR-0022 (decimal, int and money), ADR-0023 (date is a calendar day, datetime an instant, enum values), ADR-0031 (named schemas and object values), ADR-0034 (range constraints on numeric variables), ADR-0047 (three more system variables: who created, last changed and moved the document)
- Date: 2026-11-05

## Context

Three evaluators, two storage engines and a browser must agree on what a
value is. Earlier drafts called them "values" and left arrays vague. Real
documents carry lists of records, such as invoice lines, and the storage
shape of those lists decides whether the document stack is queryable and
whether versions stay affordable.

## Decision

- A class declares **variables**. Scalar types: `null`, `bool`, `number`
  (float64, ADR-0004), `text`, `date` (int64 seconds UTC), `ref` (class and
  id), `json` (opaque JSON text). Composite types: `array<scalar>` and
  `rows<schema>`, where a schema is a list of named scalar columns. No
  nesting.
- `json` is the escape hatch for data without a typed shape. The engine
  stores and compares it as text and reads it only through the pure function
  `JSONVALUE(value, path)`, which extracts one scalar. It cannot be indexed,
  cannot appear inside `array` or `rows`, and no formula produces one.
- Three kinds: **input** variables, settable, with an optional `default`
  formula; **computed** variables, read-only, with a `formula` and a `mode`
  (ADR-0015); **system** variables `ID`, `TITLE`, `STATE`, `VERSION`,
  `CREATED_AT`, `UPDATED_AT`, `STATE_ENTERED_AT`, present on every document.
  `TITLE` is computed from the class's `title` formula (ADR-0018) and equals
  `ID` when there is none.
- Values are immutable. Assigning an element of a composite produces a whole
  new value in a new commit.
- Composite variables hold at most 1,000 elements before a definition-time
  warning and 10,000 as a hard cap. Larger data belongs in an explicit table
  (ADR-0016) behind a `ref`.
- Variable names are identifiers, resolved case-insensitively in formulas
  and shown as declared in tables and views. The columns that generated
  tables and views add are reserved for variables and schema columns and
  rejected at definition time: `id`, `title`, `state`, `version`,
  `created_at`, `updated_at`, `state_entered_at`, `valid_from`, `valid_to`,
  `actor_id`, `event_id`, `doc_id`, `idx`, `value`.
- `rows` is also the result type of `QUERY`, so a triggered variable can hold
  a query result and the UI can render it as a grid.

## Consequences

- Every variable maps to a typed column; composites map to child tables
  (ADR-0014). No typed value is stored as JSON; a `json` variable is one
  opaque text column.
- The VM value is a tagged struct with a list and a rows slot; the scalar
  path never allocates.
- Row-wise expressions over `rows`, such as `SUM(lines.qty * lines.price)`,
  compile to a loop around the scalar path and to `unnest` or a join in SQL.
- The element caps are a product rule, documented and enforced, not a hidden
  limit.

## Performance

Budget shared with ADR-0012: a 50-formula auto recalculation in under 50 µs,
with composites of a few hundred elements. The child-table copy per version
is measured in Phase 2 against the commit budget.
