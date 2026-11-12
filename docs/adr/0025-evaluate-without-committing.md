# ADR-0025: Evaluate a class or a document without committing

- Status: Accepted; amends ADR-0011, ADR-0015; amended by ADR-0042 (a preview may run at a chosen moment), ADR-0043 (what a preview reports of the rules), ADR-0048 (the preview of a batch of moves)
- Date: 2026-11-06

## Context

ADR-0015 defines a commit as one atomic change with recalculation, guard
and rule checks, history and events. ADR-0011 lists the commands, and every
one that evaluates a document also writes it. The only way to see what a
change would do is to make it. `fadodb formula eval` evaluates one formula
with given variables; it knows nothing of a document, a statechart or a
rule.

The first embedder designs classes for companies and wants a designer to run
a draft class on the company's own documents and see the values, the
transitions that would open and the rules that would fire, writing nothing.
Its agent wants to dry-run one transition before asking a person to approve
it. The browser preview of ADR-0015 covers auto formulas only; triggered
variables, guards, rules and data functions need the server and the data.

Definition-time diagnostics today come from defining the class, which stores
it. A designer who wants to know whether a draft is valid, which layouts it
breaks and what migration it needs has to define it to find out.

## Decision

- A `preview` command takes a class name, optionally a document id, optional
  assignments and optionally one transition. It runs the whole commit
  pipeline of ADR-0015 inside a transaction that is always rolled back:
  assignments or defaults, auto recalculation, the guard, eventless
  transitions, triggered variables whose trigger fired, rules and
  synchronous actions, outbox rows. Nothing is written: no history row, no
  event, no outbox row.
- `preview` returns the would-be document in the wire shape of ADR-0018, the
  error per variable where recalculation failed, the transitions enabled
  from the resulting state with each guard's outcome, the rules that would
  fire and the actions they would queue.
- `preview` may name a draft class definition in the request body instead of
  a stored class. The draft is compiled for the request and discarded, and a
  designer evaluates it on existing documents of the stored class.
- A `validate` command takes a class definition and returns the
  definition-time diagnostics, the layouts the change would break and the
  migration a stored class would need. It stores nothing.
- Phase 2 builds the commit as compute first, write last, in one function
  with a write step that the preview replaces by a rollback. Synchronous
  actions and outbox inserts stay inside the transaction so that the
  rollback covers them.
- A preview runs as the caller's principal. Access rules apply as on a read,
  context functions see the caller and the clock, and a preview never
  reveals a document the caller could not read.
- `fadodb formula eval` remains the one-formula tool.

## Consequences

- Preview and commit are one code path. There is no second evaluator to
  drift from the first, and a preview that disagrees with the commit it
  precedes is a bug, not a limitation.
- Writing the commit as compute first, write last is a constraint on Phase 2
  from the start. It is the shape the rollback needs and also the shape that
  keeps the write step small.
- Set-wise recomputes on `table:` and `schedule:` triggers are not previewed;
  they are system commits over many documents and belong to Phase 8.
- A preview with a draft class pays a compile per request. A draft that
  adds an input without a default sees it blank; a draft that removes a
  variable the layouts bind to is reported by `validate`, not refused by
  `preview`.
- A preview takes the single writer of the store for its duration, because
  synchronous actions and outbox inserts need a write transaction even when
  it rolls back. Many previews compete with commits; the benchmark records
  it and the documentation says so.
- A preview is not reproducible when a formula reads the clock or runs a
  query. That is the same for a commit, and the moment of the preview is
  part of the answer.
- The error list of ADR-0019 gains nothing. A preview reports the codes a
  commit would report, per variable, instead of failing as a whole.

## Performance

Budget: a preview costs a commit without the fsync. The budget is the commit
budget of ADR-0015, under 2 ms at p99, minus the write. The Phase 2
benchmark pairs a preview and a commit on the same document and the same
assignments, so that the difference is the write and nothing else. A draft
compile is measured separately with the class definition benchmark.
