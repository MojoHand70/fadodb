---
name: fadodb-design
description: Draft, validate and refine a FadoDB document class from a description of a business process. A class is one JSON document with typed variables, Excel-style formulas, a statechart with guards, field rights per state, rules and access as formulas. Use this skill whenever someone wants to model, design, declare or set up a process, workflow, approval flow, form, register, ledger, permit, case or any record that has states in FadoDB, or asks to turn a spreadsheet, a policy, a procedure or a use-case card into a class, even if they never say "class" or "FadoDB". Also use it to review, explain or repair an existing class definition or a diagnostic the engine returned.
metadata:
  hat: designer
  status: stub
---

# Design a FadoDB class

**Status: stub, 2026-11-09.** This page says what the designer does, what
works today and the outline of the procedure. For how a class is
written, block by block and word by word, read
[`../../docs/manual/class.md`](../../docs/manual/class.md) first, and
[`../../docs/manual/first-application.md`](../../docs/manual/first-application.md)
for a small class that runs. The other reference files this page names
under `../reference/` are not written yet; until they are, read the
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
| `COUNT`, `SUM`, `MIN` and `MAX` in a `LOOKUP` of a table, as in `LOOKUP(Stock, SUM(qty), sku = THIS.sku)` | shipped with Phase 4 |
| a formula that reads the documents of another class, `LOOKUP(Approval, COUNT(id), purchase = ID() AND state = "granted")` | shipped with Phase 4: in a triggered variable, a default, a guard or a rule, not in an auto formula; the variable declares its `type`; it sees every document and is computed at its trigger, so the other class's rule refreshes this document; put `"index": true` on the variable the condition compares. Two classes that name each other, a purchase and its approvals, are defined in one call with `DefineClasses`; `examples/classes/purchase.json` and `approval.json` are the worked pair |
| formulas, exact money, calendar days, `IFERROR` | shipped |
| states, transitions, guards, eventless chains, `STATE()` and `ID()` | shipped |
| the life of the document in a formula: `CREATEDAT()`, `CREATEDBY()`, `UPDATEDAT()`, `UPDATEDBY()`, `ENTEREDAT()`, `ENTEREDBY()`, `ENTEREDFROM()`, `VERSION()` | shipped with Phase 4: in a default, a triggered variable, a guard, a rule, an access rule and a right, not in an auto formula. "Seven days in this state" is `NOW() >= ADDDAYS(ENTEREDAT(), 7)`; four eyes is `CREATEDBY() <> USER()` as the access rule of the transition. A transition back into the same state is an entry and restarts `ENTEREDAT()` |
| the life of the document by state: `FIRSTENTERED("approved")`, `LASTENTERED`, `FIRSTLEFT`, `LASTLEFT`, each with a `...BY` for who; `ENTRIES("sent")`; `DAYSIN("review")`; `FIRSTEDITOR("draft")`, `LASTEDITOR("draft")` | shipped with Phase 4: where the functions above may stand, and not in a read rule, a read right or a filter, which run for every row (keep the answer in a triggered variable, or query the `_states` view). The state is written as text and checked when the class is defined, or computed, as in `DAYSIN(STATE())`. A transition back into the same state is an entry. An editor created the document in the state or changed it there; who moved it in or out is `LASTENTEREDBY` and `LASTLEFTBY`. "Back to whoever wrote the draft" is `LASTEDITOR("draft")`; "who first sent it to review may not approve it" is `FIRSTENTEREDBY("review") <> USER()`; "after three rounds" is `ENTRIES("review") >= 3` |
| the list of documents that are due, `Due` | shipped with Phase 4: the documents of a class that a commit would move now, or at a moment, each with its state and the state it would move to. A document becomes due when a guard of an eventless transition comes to hold with no commit of its own: the clock passed a date, or a document or a table the guard looks up changed. The engine does not move it by itself yet: the host program touches it, with a refresh. Index the variable a guard compares with the clock, as in `"due": {"type": "date", "index": true}` for `TODAY() > due` |
| a transition that carries its time: `"after": "30d"`, a time in the state as a whole number and `d`, `h`, `m` or `s`, or `"at": "ADDDAYS(due, 1)"`, a date or a datetime the document holds | shipped with Phase 4: it needs neither a name nor a guard, and a guard beside it is still asked. It fires in the first commit that touches the document once its time has come, in the order of the class with the eventless transitions; its event has the origin `timer`. A reminder is a named transition back into its own state with an `after` and a rule on its name; an escalation that reminders must not push back is `"at": "ADDDAYS(FIRSTENTERED(\"waiting\"), 14)"`. With a name a person may take it before its time; give it the access rule `FALSE` to leave it to time alone. An `at` may not read `NOW()`, `TODAY()`, `USER()`, another document or a table, and a blank `at` never comes. Put `"index": true` on the date an `at` names |
| the engine moving a document by itself when its timer comes | shipped with Phase 4: the move is made as the person whose commit set the timer, who moved the document into the state or who last changed the date, so `USER()` in its rules is theirs and its actions run with their rights. A reminder missed while the engine was stopped is made once, not once for every period missed. A guard beside a timer is asked when the time comes; if it does not hold then, the document waits for a commit to touch it, and `Due` lists it once the guard holds |
| field rights per state, per-transition rules | shipped with Phase 3 |
| rules and actions | shipped with Phase 4: rules fire inside the commit with `set`, `reset`, `refresh` and `emit`, a preview lists them, and triggered variables are computed at their triggers; what a rule does beyond its own document runs through the outbox, exactly once and as the person who caused it: `create` (with `each`, one document per element of a list), `transition` and `refresh` of another document, `table.insert`, `table.update`, `table.delete`; a rule may also call an action that has a definition, with typed `args`, whether a Go function of the host program implements it or the engine sends it as an HTTP request, and `http.call` for one request written in the rule; `Actions` lists every action a rule may name and whether this process can run it; `into` writes what an action returned, or the code it failed with, back to the document, and the eventless transitions follow |
| action definitions: `DefineAction`, `ValidateAction`, `Actions`, `PreviewAction`, `TryAction` | shipped with Phase 4: a definition with a `request` needs no code and reads secrets with `SECRET("name")`; it can be defined only where the host program allows outbound calls and names the definition's connection; `docs/actions.md` is the guide and `examples/actions/webhook.json` a generic webhook; definitions for particular services come with real needs, and a format for packages of them is planned |
| the class-level access rules: `"access": {"read": ..., "create": ..., "update": ..., "delete": ...}` | shipped with Phase 5: each is a condition over the document for the caller, such as `owner = USER() OR INGROUP("finance")`. A document its caller may not `read` does not exist for them: `unknown_document` from every call that names it, and absent from a filter, a count and `Due`. `create` is asked over the new document with its defaults and computed values; `update` over the stored document, by a set, a reset and a named transition that has no rule of its own; `delete` over the stored document. A refusal is `forbidden`, with the place of the rule as its path, `/access/update`. A `read` rule uses the document's own variables, the functions of its row, the caller and the clock, and no lookup. Put `"index": true` on the variable a read rule compares with the caller, as in `"owner": {"type": "text", "default": "USER()", "index": true}`: a caller's own documents are then listed through the index, and a group that sees everything, `OR INGROUP("finance")`, costs its members nothing. A rule left out lets every caller through today; a ruling on that is pending (ADR-0049), so write all four **Write all four.** A rule a class leaves out lets nobody through but a content administrator: a class with no `access` block holds documents nobody creates or sees. `ValidateClass` says so with a note for each rule left out, code `rule_left_out` and `note: true`, which refuses nothing; a definition is refused only when `Refuses()` is true. `INGROUP("everyone")` opens a rule to every caller that has an id, and `TRUE` to a call without a principal too |
| class versions, terminal states, migrations | shipped: a redefinition is a new version and touches no document; a document adopts it at its next commit, or all at once with `Migrate`; a terminal state freezes the document |

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
   approved, which group may write a discount, who the editors are in
   each state. The table is state, then variable, then `write` or
   `read`; `*` stands for every state or every variable; a cell is a
   formula or a list of groups. "In review only reviewers edit" is
   `"rights": {"review": {"*": {"write": ["reviewers"]}}}`. The line of
   the document's state is asked before the line of `*`, and within a
   state a named variable before `*`. Rights narrow what `access`
   allows and never widen it.
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
   **Write the access rules**, all four: who sees a document, who
   creates one, who changes it and who deletes it. They are enforced.
   A transition that only some may take gets a rule of its own under
   `access.transition`; one without is open to whoever may `update`.
   What a rule does to another document is done as the person who set
   it off, so that person needs the other class's rules to let them.
   **A table has two rules too**: `"access": {"read": "TRUE", "write":
   "INGROUP(\"treasury\")"}`, each about the table as a whole. A rule's
   `table.update` is refused when whoever set the rule off may not
   write the table. Whether you may define at all is the host
   program's to say: defining is for the system group `designers`,
   which it gives a caller in `Principal.System`. Without it expect
   `forbidden`, naming the group. Every version you define is recorded
   with your id, what you are and for whom you act, and
   `Definitions(ctx, "class", name)` lists them for every caller. A class may speak of a system group
   as of any group, and `INGROUP("everyone")` is true for every caller
   that has an id.
   **Say what agents may do.** A caller is a person, an agent or a
   service, and may act for somebody: `USERKIND()` and `ACTINGFOR()`
   say so. Keep a decision for a person with `USERKIND() = "person"`,
   never with `<> "agent"`, which a caller of unknown kind would pass.
   Let an assistant in with `owner = USER() OR owner = ACTINGFOR()`.
   `USER()` is always the one who acts, the assistant and not the
   person it works for.
   **Say where a substitute is accepted.** The engine checks every
   "for" against a substitution before a formula runs (ADR-0053), so
   `ACTINGFOR()` is safe to write. A step the approver or their
   substitute takes, and never the one who filed the document, is
   `(approver = USER() OR approver = ACTINGFOR()) AND CREATEDBY() <> USER()`.
   A step that stays personal names `USER()` alone. A group does not
   pass to a substitute: `INGROUP("managers")` waits for a manager.
   The class `Substitution` is the engine's; a class of yours may read
   it, and may not take its name (`reserved_name`).
7. **Validate.** Today through `ValidateClass` on the Go API, and
   `ValidateClasses` for classes that name each other, whose findings
   start with the position of the definition, `/1/variables/total`; a
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

- [`../../examples/first/invoice.json`](../../examples/first/invoice.json),
  the invoice of the manual's first application: two states with their
  own editors, a guard, and a program that walks it through,
  `go run ./examples/first`.
- [`../../examples/classes/invoice.json`](../../examples/classes/invoice.json),
  the Invoice from the README: an approval chain with a `rows` variable,
  a rule and rights per state.
- The guides under [`../../docs/manual/guides/`](../../docs/manual/guides/),
  one task each with a class that a test runs: editors per state, a
  field read-only after approval, four eyes, reminders and a deadline,
  acting for somebody. Take the pattern from the guide before writing
  your own.
- The six samples the front page of fadodb.org links to, planned.

## References, to be written

The class format is written: [`../../docs/manual/class.md`](../../docs/manual/class.md).
So are the list of every function,
[`../../docs/manual/functions.md`](../../docs/manual/functions.md), which
marks the ones this engine does not have yet, and the list of every
refusal and diagnostic code,
[`../../docs/manual/refusals.md`](../../docs/manual/refusals.md); both are
generated from the engine. Still to be written: `formulas.md` and
`statechart.md`. Their sources are listed in
[`../reference/README.md`](../reference/README.md), and the plan of the
whole manual in [`../../docs/README.md`](../../docs/README.md).
