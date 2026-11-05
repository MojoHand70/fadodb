# ADR-0006: Event log as the source of truth

- Status: Accepted
- Date: 2026-11-05

## Context

Requirements that pull in the same direction: snapshots on state exit and
entry, an audit trail, process mining over document lifecycles, and the
ability to rebuild after a class migration. Event sourcing satisfies all four
with one mechanism.

## Decision

- Every change to a document is an append-only event in `events`, with a
  second table `event_docs` linking each event to every document it touched
  and a qualifier such as `created` or `transitioned`.
- A transition event carries both snapshots: `before` is the exit snapshot,
  `after` the entry snapshot.
- The per-class tables hold the current state and are projections; they can
  be rebuilt from the log.
- The log is never rewritten. Class migrations replay; deletions append a
  `deleted` event and remove the projection row.
- Action executions and failures are events too, so the audit trail covers
  side effects.

## Consequences

- Storage grows with history. A retention command can archive events older
  than a threshold to a file; it does not delete them silently.
- `event_docs` costs one extra row per touched document per event, and in
  return multi-document process mining needs no retrofit.
- Time-travel queries become a feature rather than a project.

## Performance

Budget: a transition commits in under 2 ms at p99 including the event row and
both snapshots, on NVMe, single writer. Snapshot size is bounded by document
size; large arrays should be measured in Phase 3.
