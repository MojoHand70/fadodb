# ADR-0005: Blank and null semantics

- Status: Accepted
- Date: 2026-11-05

## Context

Documents have optional values. Spreadsheets treat an empty cell as 0 in
arithmetic and as "" in text, while SQL treats NULL as poison that propagates.
Three evaluators must agree, so the rule has to be written once.

## Decision

- There is one blank value, `BLANK`, which is also the JSON `null`.
- In arithmetic and comparisons a blank operand behaves as 0; in text
  concatenation as the empty string; as a condition as false.
- `ISBLANK(x)` is true only for a blank, never for a stored 0 or "".
- Aggregates (`SUM`, `AVERAGE`, `MIN`, `MAX`, `COUNT`) skip blanks.
  `AVERAGE` of only blanks is blank, not an error.
- A formula may return `BLANK` explicitly, and the result is stored as null.
- Arrays may contain blanks; array functions follow the aggregate rule.
- SQL translation wraps numeric and text reads in `COALESCE` where the rule
  above requires a default, and leaves NULL where the result is blank.

## Consequences

- Behaviour matches spreadsheets where users expect it and is documented
  where spreadsheets are inconsistent.
- The SQL generator has one place that applies `COALESCE`, which keeps the
  translation auditable.

## Performance

`COALESCE` on indexed columns prevents index use in some planners; read rules
and filters should compare against non-null columns where possible. Measured
in the Phase 2 query benchmarks.
