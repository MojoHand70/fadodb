# ADR-0008: Statechart semantics, SCXML subset

- Status: Accepted; amended by ADR-0039 (a final state freezes the document)
- Date: 2026-11-05

## Context

Document lifecycles need states, guarded transitions, and effects on entry and
exit. Inventing semantics invites ambiguity; the W3C SCXML specification and
the statechart tradition already define them.

## Decision

- 1.0 implements a flat subset of SCXML: named states, one initial state,
  optional final states, named transitions with source states, a target, and
  an optional guard formula.
- Entry and exit effects are rules bound to `enter(state)` and `exit(state)`.
- Eventless transitions, which have a guard and no name, are evaluated after
  every value change, in definition order, until none fires, with a bound of
  eight per commit. Exceeding the bound fails the commit with a clear error.
- A transition is the only way state changes. There is no direct write to
  `state`.
- Hierarchical states are planned for a 1.x release as a new ADR. Parallel
  regions are not planned.
- Timed transitions (`after`) are scheduled through the action outbox and
  arrive as named transitions.

## Consequences

- Guards use the same formula language and bytecode as everything else, so
  they are evaluated identically on the server and shown as enabled or
  disabled in the browser.
- Authors who know XState or SCXML find familiar rules.
- Loops through eventless transitions are caught at commit rather than
  silently running.

## Performance

Transition evaluation is a loop over the candidate transitions of the current
state with compiled guards. Budget shared with ADR-0006: under 2 ms per
transition at p99.
