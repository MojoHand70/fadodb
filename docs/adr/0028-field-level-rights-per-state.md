# ADR-0028: Field-level rights per state

- Status: Proposed; amends ADR-0010, ADR-0018
- Date: 2026-11-06

## Context

ADR-0010 gives a class `read`, `create`, `update`, `delete` and
per-transition rules. They decide whether a principal sees or changes a
document, and they stop at the row. Which variables of a visible document a
principal sees, and which ones they may set, has no home. Layouts carry
`visible` and `editable` formulas, but ADR-0018 makes them advisory: a
client that ignores the layout, or asks the filter DSL, sees everything the
read rule allows.

A document shows different fields in different states. A requester fills in
an amount and a reason while the request is a draft, and after approval sees
the decision but not the reviewer's notes. A margin is visible to finance in
every state and to nobody else. The first embedder's designer wants this as
one table of ticks per class: who sees and who edits which variable in which
state. Its test is plain: a person's request never sees a variable their
rules hide, however the administrator defined the class.

## Decision

- A class may declare `rights`: for each state, or `*` for every state, and
  each variable, or `*`, a `read` rule and a `write` rule. A rule is a
  formula with the functions of an access rule under ADR-0015, or a list of
  group names, which means `INGROUP` of any of them. A named state or
  variable takes precedence over `*`.
- This table is the designer's table of ticks. It is stored in the class
  JSON, and the formulas stay the stored truth.
- Write enforcement: a command that sets a variable whose write rule is
  FALSE in the document's current state is refused with the error
  `forbidden` naming the variable. The check runs in the VM at commit time,
  after the class-level and per-transition rules of ADR-0010.
- Read enforcement: a variable whose read rule is FALSE for the caller in
  the document's state is absent from the wire document, from the change
  stream and from the results of `QUERY`, `LOOKUP` and the filter DSL. A
  filter or a sort on a hidden variable is refused.
- The SQL endpoint and the generated views stay row-level, as ADR-0010
  compiles them. This is documented: they are for administrators and
  analysts, not for a person's request.
- Formulas inside the class compute over every variable regardless of
  rights. Rights govern what a principal sees and may set, not what the
  engine computes.
- Administrators bypass. The bundle of ADR-0018 carries the rights table so
  that a frontend can show and grey out fields without a round trip. The
  layout's `visible` and `editable` formulas remain advisory on top.

## Consequences

- Access has two layers with two mechanisms: rows in SQL, fields in the VM
  and the wire encoder. The row layer is the one that reaches SQL and the
  views; the field layer is the one a person's request sees. A host that
  gives a person SQL access gives them every field of every visible row,
  and the documentation says so.
- Rights do not propagate through formulas. A visible computed variable may
  reveal a hidden input by arithmetic, and the designer hides the derived
  variable too. The engine does not infer this.
- A class without `rights` behaves as before, and a variable the table does
  not name is governed by the class rules alone. The table is additive.
- The change stream projects per subscriber, because two subscribers with
  the same row predicate may see different fields. The projection runs on
  the encoder, once per subscriber per event.
- A refused filter on a hidden variable tells the caller that the variable
  exists. The alternative, treating it as unknown, would make the same
  query fail differently for different people; the refusal is the honest
  answer.
- The bundle grows by the rights table and its compiled formulas. A frontend
  evaluates them in the browser VM to grey out fields; the server's answer
  on commit and on read is the one that counts.
- A `write` rule that errors evaluates as FALSE, as every access rule does
  under ADR-0019, so an error never opens a field.

## Performance

Budget: the write check is one formula per set variable at commit, inside
the 2 ms commit budget of ADR-0006. The read filter is a projection step on
the wire encoder, under 5 µs per document for a 50-variable class, measured
in Phase 3 with the encoder benchmark of ADR-0018. A rights table given as
group lists compiles to the same `INGROUP` test as a formula and costs no
more.
