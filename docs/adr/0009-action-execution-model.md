# ADR-0009: Action execution model: synchronous versus outbox

- Status: Accepted; amended by ADR-0027 (executors the embedder registers)
- Date: 2026-11-05

## Context

Rules fire actions on state changes: set a value, create another document,
transition another document, write an admin table, call a URL. Some of these
are deterministic and local; others can fail, take time, or must not run
twice. Running everything inside the commit would make the transition budget
depend on the network.

## Decision

- Synchronous actions, run inside the committing transaction: `set` on the
  same document, and `emit` of a custom event. They are deterministic and
  re-trigger recalculation.
- Every other action is written as a row in the `outbox` table in the same
  transaction as the event, and executed afterwards by a worker in the same
  process.
- Each outbox row carries an idempotency key of event id and action index.
  Workers retry with exponential backoff and move a row to dead-letter after a
  configurable number of attempts.
- Results are recorded as `action_run` or `action_failed` events.
- Built-in actions only in the core. External code runs through `http.call`.
  WASM plugins may be added after 1.0 by a new ADR.

## Consequences

- A crash between the commit and the action never loses or duplicates work,
  because the outbox row and the event are in one transaction and the key
  makes execution idempotent.
- Effects on other documents are eventually consistent, which the
  documentation states plainly.
- The transition budget excludes external effects by construction.

## Performance

Budget: outbox throughput is measured in Phase 4 and published; the outbox
insert adds one row per action to the transition's transaction.
