# ADR-0010: Security rules compiled to SQL predicates

- Status: Accepted; amended by ADR-0027 (the principal comes from the embedder), ADR-0028 (field-level rights per state), ADR-0041 (the per-transition rules and rights are enforced in the Go API from Phase 3), ADR-0046 (a formula reads other documents with the engine's eyes, not the owner's)
- Date: 2026-11-05

## Context

Access rules are formulas that can read the current user and groups. Checking
them per row in Go after a query would be slow and would leak through
subscriptions and aggregates. Grist and Postgres row-level security show that
rules evaluated inside the query are both faster and safer.

## Decision

- Each class has `read`, `create`, `update`, `delete` and per-transition rules,
  all formulas.
- The `read` rule is compiled to a SQL predicate and appended to every query,
  view, aggregate and subscription filter for that class. In 1.0 a read rule
  may use pure and context functions over the row's own columns only; data
  functions in a read rule are rejected at class definition time.
- `create`, `update`, `delete` and transition rules are evaluated in the VM at
  commit time with the full document available.
- Administrators bypass rules. Every other principal gets exactly what the
  rules say; a class without rules is readable by nobody but administrators.
- The change stream applies the same predicate, so a subscriber never receives
  a row the query would hide.

## Consequences

- One rule, one behaviour, in queries and in subscriptions.
- Rule authors must express read visibility in terms of the row, which is the
  same constraint row-level security imposes elsewhere.
- Formula calculations cannot be used to read across rules, because data
  functions in formulas run with the document owner's access, not the
  caller's; this is documented and tested.

## Performance

Budget: predicate injection adds under 5% to the indexed-filter budget of
ADR-0007. Predicates should reference indexed or plain columns; the compiler
warns when a rule cannot use an index.
