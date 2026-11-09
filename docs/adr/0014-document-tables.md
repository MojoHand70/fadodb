# ADR-0014: Document tables: current, history, child tables and views

- Status: Accepted; amends ADR-0006 and ADR-0007; amended by ADR-0026 (command keys and business dates), ADR-0031 (objects flatten to prefixed columns), ADR-0039 (class_version on every row), ADR-0040 (the `_states` view)
- Date: 2026-11-05

## Context

ADR-0006 stored exit and entry snapshots as JSON on transition events.
ADR-0007 gave each class one typed table. Users need the document stack as a
queryable table with every variable a typed column, a row identifiable by
document, state and time, and process mining that never parses JSON. JSON
snapshots on events fail all three.

## Decision

Per class, generated from the definition:

- `doc_<Class>`: one row per document, the latest version. Key `id`.
- `doc_<Class>_history`: one row per committed version. Key `(id, version)`,
  with `state`, `valid_from`, `valid_to`, `actor_id`, `event_id` and every
  variable as a typed column.
- `doc_<Class>__<variable>`: one child table per composite variable, one row
  per element per version. Key `(doc_id, version, idx)`, with the schema's
  columns or a single `value`.
- `events` and `event_docs` are shared across classes. An event carries
  `before_version` and `after_version` instead of snapshots.

Every commit writes the current row, one history row, the child rows for the
new version, and its events in one transaction. Nothing is stored twice, and
nothing is rewritten; deletion appends an event and removes the current row.

Users see views, never physical names: `<Class>`, `<Class>_history`,
`<Class>_states` (one row per state visit with entered, left and duration),
`<Class>_events`, and `<Class>_<variable>` with `_history` for composites.

## Consequences

- ADR-0006 stands with one change: snapshots are typed history rows that
  events reference. The event log remains the record of what happened and
  why; the history table is the record of what the document looked like.
- ADR-0007 stands and gains child tables and views.
- "As of" queries are a range filter on history; time travel is a feature,
  not a project.
- Composite variables are copied per version, which is why ADR-0013 caps
  their size. A retention command archives old versions to a file; it never
  deletes silently.

## Performance

Budget: a commit including the history row and child rows completes in under
2 ms at p99 on NVMe. History indexed on `(id, valid_from)`; events on
`(class, doc_id, ts)`. Measured in Phase 2 and Phase 3.
