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
| states, transitions, guards, eventless chains, `STATE()` and `ID()` | shipped |
| field rights per state, per-transition rules | shipped with Phase 3 |
| rules and actions | in progress, Phase 4: rules fire inside the commit with `set`, `reset`, `refresh` and `emit`, a preview lists them, and triggered variables are computed at their triggers; what a rule does beyond its own document runs through the outbox, exactly once and as the person who caused it: `create` (with `each`, one document per element of a list), `transition` and `refresh` of another document, `table.insert`, `table.update`, `table.delete`; a rule may also call an action that has a definition, with typed `args`, whether a Go function of the host program implements it or the engine sends it as an HTTP request, and `http.call` for one request written in the rule; `Actions` lists every action a rule may name and whether this process can run it; `into` writes what an action returned, or the code it failed with, back to the document, and the eventless transitions follow |
| action definitions: `DefineAction`, `ValidateAction`, `Actions`, `PreviewAction`, `TryAction` | in progress, Phase 4: a definition with a `request` needs no code and reads secrets with `SECRET("name")`; it can be defined only where the host program allows outbound calls and names the definition's connection; `docs/actions.md` is the guide and `examples/actions/webhook.json` a generic webhook; definitions for particular services come with real needs, and a format for packages of them is planned |
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
6. **Write the rules.** They run. Read `Actions` first: a rule names an
   engine action or one the catalogue lists, and gives a defined action
   its arguments under `args`, a formula for each parameter. To reach
   an API that has no action yet, write a definition with a `request`,
   check it with `ValidateAction`, look at what it would send with
   `PreviewAction`, and run it once with `TryAction` before a document
   depends on it. Never write a token into a definition or a class: a
   request reads it with `SECRET("name")`, and the host program
   supplies it. To go on from the answer, give the action an `into`
   and guard the next transition on the variable it fills; guard
   another on the error variable, so that a failure is a state and not
   a document that waits for ever.
   **Write the access rule**, marked as what it is today: checked, not
   enforced.
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
