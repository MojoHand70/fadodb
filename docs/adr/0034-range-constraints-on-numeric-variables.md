# ADR-0034: Range constraints on numeric variables

- Status: Proposed; amends ADR-0013, ADR-0018
- Date: 2026-11-07

## Context

Discussion 15 lists a rating among the things a document should
understand: a number within a range, one to five stars. ADR-0023 made an
enum a constraint on a text variable, `values`, rather than a type, checked
by the store at commit and carried in the bundle so that a layout renders a
choice. A range is the same kind of thing on a number. Without it an author
writes a guard for every bounded number, and a layout has no way to know
that an `int` is five stars or a percentage.

## Decision

- A variable or a schema field of type `number`, `int`, `decimal(s)` or
  `money(s)` may declare `min` and `max`, inclusive, one or both, as
  literals of the variable's type; on `money` they are decimal literals
  compared with the amount, whatever the currency.
- A commit that sets or computes a value outside the range is refused with
  `out_of_range` naming the variable. A blank passes. The check is the
  store's at commit, after recalculation, like the enum check of ADR-0023;
  it applies to input and computed variables alike, so a range on a
  computed variable is an invariant the class promises.
- Formulas see a plain number; the three evaluators are untouched.
- Widening a range is an in-place class change; narrowing is a migration
  under ADR-0014, since stored values may lie outside.
- `min` and `max` travel in the bundle (amends ADR-0018), and the layout
  format gains `rating`, which renders an `int` with both bounds as that
  many marks, and `slider`, which renders any bounded number. The
  reference renderer implements them in Phase 7.
- A rating is therefore not a schema of ADR-0032; it is
  `{"type": "int", "min": 1, "max": 5}`.

## Consequences

- A bounded number is declared where the variable is, and a frontend knows
  how to render it from the bundle alone.
- A commit may now fail on a computed value; the failure names the
  variable and the code, as a recalculation error does under ADR-0019.
- The class JSON schema and the conformance suite's definition cases grow
  by two keys.

## Performance

Two comparisons per set or recomputed variable at commit, inside the 2 ms
p99 of ADR-0006. Nothing on the evaluators.
