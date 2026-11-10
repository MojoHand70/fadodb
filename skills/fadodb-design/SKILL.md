---
name: fadodb-design
description: Draft, validate and refine a FadoDB document class from a description of a business process. A class is one JSON document with typed variables, Excel-style formulas, a statechart with guards, field rights per state, rules and access as formulas. Use this skill whenever someone wants to model, design, declare or set up a process, workflow, approval flow, form, register, ledger, permit, case or any record that has states in FadoDB, or asks to turn a spreadsheet, a policy, a procedure or a use-case card into a class, even if they never say "class" or "FadoDB". Also use it to review, explain or repair an existing class definition or a diagnostic the engine returned.
metadata:
  hat: designer
  status: stub
---

# Design a FadoDB class

**Status: stub, 2026-11-09.** This page says what the designer does, what
works today and the outline of the procedure. The reference files it
names under `../reference/` are not written yet; until they are, read the
decision records they list.

## The hat

The designer turns a process somebody described into a class the engine
accepts: the fields with their types, the formulas that compute, the
states and the transitions between them, who may move what and when, and
what happens on a change. The engine validates the draft and names the
exact place of every problem, so the designer's loop is draft, validate,
read the diagnostics, fix, preview against live data, define.

## What works today

| Part of a class | Mark |
|---|---|
| variables, types, defaults, ranges, named schemas, objects, attachments, tables and `LOOKUP` | shipped |
| formulas, exact money, calendar days, `IFERROR` | shipped |
| states, transitions, guards, eventless chains, `STATE()` and `ID()` | in progress, Phase 3 |
| field rights per state, per-transition rules | shipped with Phase 3 |
| rules and actions | designed, Phase 4: they compile and are checked, they do not fire |
| the class-level access rule | designed, Phase 5: it compiles, it is not enforced |
| class versions, terminal states, migrations | designed, ADR-0039 |

## Procedure, in outline

1. **Find the shape.** Every process in the catalogue is one of six
   shapes or two together: the approval chain, the lifecycle register,
   the ledger and the period, matching and reconciliation, the case with
   a clock, the schedule of obligations. Name the shape first; the states
   and the guards follow from it.
2. **List the fields and type each one.** Money is exact, with a scale
   and a currency; a date is a calendar day; a value from a fixed list is
   an enum; a repeated line is a `rows` variable with a schema. Mark what
   is required and write defaults as formulas.
3. **Write the formulas you would write in the spreadsheet.** The engine
   computes; the agent never does arithmetic. Division by zero is an
   error unless the formula says what to do with `IFERROR`.
4. **Draw the states, then the transitions, then the guards.** A guard
   says what must hold (`total > 0`) and who may move it
   (`INGROUP("finance")`). A terminal state freezes the document.
5. **Write the rights per state.** Which fields are read-only once
   approved, which group may write a discount.
6. **Write the rules and the access rule**, marked as what they are
   today: checked, not run.
7. **Validate.** Today through `ValidateClass` on the Go API; a
   `fadodb class check FILE` subcommand is planned so this needs no Go.
   Each diagnostic carries a stable code, a JSON pointer to the element,
   an offset inside the formula, a message and often a hint. Fix the
   place it names; do not guess around it.
8. **Preview the draft against live data** with `PreviewDraft` before
   defining it: it runs the draft through the commit pipeline over an
   existing document or as a create and writes nothing.
9. **Define.** A redefinition is a new version that touches no document
   (ADR-0039).

## Why the order matters

An agent that writes the whole class in one pass and reads the
diagnostics once gets a class that compiles. An agent that follows the
shape gets a class a person can read in two minutes, with the states a
process owner recognises. Both matter: the second is why the class is a
document and not code.

## Worked examples

- [`../../examples/classes/invoice.json`](../../examples/classes/invoice.json),
  the Invoice from the README: an approval chain with a `rows` variable,
  a rule and rights per state.
- The six samples the front page of fadodb.org links to, planned.

## References, to be written

`../reference/class-format.md`, `formulas.md`, `functions.md`,
`statechart.md`, `rights-and-rules.md`, `diagnostics.md`. Their sources
are listed in [`../reference/README.md`](../reference/README.md).
