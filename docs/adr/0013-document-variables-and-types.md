# ADR-0013: Document variables and the type system

- Status: Accepted
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
  id). Composite types: `array<scalar>` and `rows<schema>`, where a schema is
  a list of named scalar columns. No nesting.
- Three kinds: **input** variables, settable, with an optional `default`
  formula; **computed** variables, read-only, with a `formula` and a `mode`
  (ADR-0015); **system** variables `ID`, `STATE`, `VERSION`, `CREATED_AT`,
  `UPDATED_AT`, `STATE_ENTERED_AT`, present on every document.
- Values are immutable. Assigning an element of a composite produces a whole
  new value in a new commit.
- Composite variables hold at most 1,000 elements before a definition-time
  warning and 10,000 as a hard cap. Larger data belongs in an explicit table
  (ADR-0016) behind a `ref`.
- Variable names are identifiers, resolved case-insensitively in formulas
  and shown as declared in tables and views.
- `rows` is also the result type of `QUERY`, so a triggered variable can hold
  a query result and the UI can render it as a grid.

## Consequences

- Every variable maps to a typed column; composites map to child tables
  (ADR-0014). Nothing is stored as JSON.
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
