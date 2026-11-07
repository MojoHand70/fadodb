# ADR-0024: Division by zero is an error, and IFERROR catches one

- Status: Accepted; amends ADR-0004, ADR-0019
- Date: 2026-11-06

## Context

ADR-0004 kept the sfmt engine's rule that `/` by zero yields 0 and added
`\` as the operator whose division by zero is an error. The rule hides a
defect: a unit price computed from a zero quantity becomes 0, is stored,
summed and reported, and nobody sees that a formula divided by nothing.
Excel reports it. The first embedder calls a silent zero in bookkeeping a
defect of the first order, and the engine's own exact types (ADR-0022)
have no zero to hide behind.

ADR-0019 makes a commit whose auto recalculation errors fail as a whole.
With `/` an error, a draft invoice whose quantity is still blank cannot be
saved while `total / qty` is unguarded. The language has `IF` to guard, and
nothing to catch: an author who wants "the value, or 0 when it cannot be
computed" writes the condition twice.

## Decision

- `/` by zero is the error `div_by_zero` for `number` and for the exact
  types alike, a blank divisor included. `\` is the same operator, kept so
  that formulas from the predecessor engine parse; the catalog says so.
- `IFERROR(value, fallback)` evaluates the value and, when the evaluation
  stops with an error, evaluates the fallback instead. Only errors inside
  the value are caught; an error in the fallback, or outside, propagates.
  A blank is a value, not an error. The fallback has the value's type or is
  blank, and two exact alternatives join at the larger scale, as the
  branches of `IF` do.
- The VM runs the value under a handler that restores the stack; the SQL
  generator selects the fallback where the value's error expression is
  set and swallows the value's own errors, in the same structure that
  keeps an untaken branch from failing. `IFERROR` may nest sixteen deep.
- The error model of ADR-0019 stands: a commit whose auto recalculation
  errors fails as a whole, a triggered recomputation that errors keeps the
  stored value, a guard or rule that errors is FALSE. An author who wants
  a draft to save with an incomplete ratio writes the guard, with `IF` or
  with `IFERROR`, and the formula says so.

## Consequences

- A division that was silently 0 now stops the commit and names the
  variable. Formulas written against v0.1.0 that relied on the zero need
  `IF(qty = 0, BLANK, total / qty)` or `IFERROR(total / qty, BLANK)`.
  Nothing stores such a formula yet.
- The error codes of ADR-0019 gain `overflow`, `inexact` and
  `currency_mismatch` from ADR-0022; `IFERROR` catches any of the closed
  list.
- `IFERROR` is one more place where SQL has to agree with the VM on which
  errors belong to which branch. The `iferror` suite and the property
  campaign cover it, and the campaign found the one place the join
  rescale was caught where the VM did not catch it.

## Performance

A formula without `IFERROR` pays nothing: the handler stack lives in the VM
and is touched only by `try` and `end.try`. The float budget of ADR-0012
stands. In SQL an `IFERROR` repeats the value's error expression once, as a
condition, which the dialects already do for the branches of `IF`.
