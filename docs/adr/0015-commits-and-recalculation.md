# ADR-0015: Commits, recalculation modes, defaults, reset and refresh

- Status: Accepted; amended by ADR-0020 (how recomputes and query functions reach their engine), ADR-0025 (preview runs the pipeline without the write)
- Date: 2026-11-05

## Context

A stored computed value must mean something definite. A formula that reads
the clock, the current user, or data outside the document would make the
stored value depend on who looked and when, and a formula that queries an
analytical engine on every edit would blow the commit budget. Some values
must nevertheless come from the clock, the user or analytics, at a moment the
author chooses.

## Decision

- A **commit** is one atomic change to one document: assignments, or one
  transition, or both, with recalculation, guard and rule checks, the history
  row and the events, in one transaction. It increments the version by one.
  An action touching several documents produces one commit per document,
  linked by the cause event.
- **Auto** computed variables recalculate inside every commit that changes
  anything in their dependency set, in topological order, only the dirty part
  of the graph. They may use pure functions and `LOOKUP` against explicit
  tables. They may not use context functions or `QUERY`; the binder rejects
  such formulas at definition time.
- **Triggered** computed variables recalculate only on declared triggers:
  `refresh` (a command, which is what a button sends), `enter:<state>`,
  `exit:<state>`, `transition:<name>`, `table:<name>` and
  `schedule:<spec>`. They may use any function. They store the version and
  the time at which they were computed.
- `table:` and `schedule:` recomputes are **set-wise**: one query per class
  produces the new value for every affected document, applied as system
  commits in batched transactions, never one query per document.
- **Input defaults**: an input may declare a `default` formula, evaluated at
  creation for every input the creator did not supply, in dependency order,
  and again when the `reset` action or command names the variable. Defaults
  may use any function, including `USER()`, `NOW()`, `LOOKUP` and `QUERY`,
  because they run once at a known moment.
- Guards, rules, access rules and UI formulas are never stored and may use
  any function.
- Order inside a commit: assignments or defaults; auto recalculation; the
  requested transition's guard, then eventless transitions bounded to eight;
  triggered variables whose trigger fired; rules, synchronous actions now
  and the rest to the outbox; writes.
- The browser evaluates auto formulas live from the same bytecode for
  preview; the server's result on commit is authoritative.

## Consequences

- Every computed value is explainable: an auto value is a function of the
  document, a triggered value carries its moment, a default carries its
  creation or reset.
- Authors choose latency consciously: auto for the document's own arithmetic,
  triggered for anything that looks outside.
- `reset` and `refresh` exist both as actions in rules and as commands from
  the protocol.

## Performance

Budgets: auto recalculation of a 50-formula document under 50 µs; a commit
under 2 ms at p99; a set-wise recompute of one triggered variable over
100,000 documents under 2 s end to end. The last one is the Phase 8
benchmark that proves the set-wise design.
