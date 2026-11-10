---
name: fadodb-own-work
description: Run an agent's own work as FadoDB documents. A task ledger with the attempts as rows and the outcome as a state, an approval inbox where the agent proposes and a person disposes, hand-offs to a human as transitions, retries and timeouts as states, a multi-step operation across services as one saga document. Use this skill whenever an agent needs durable state for what it is doing, must ask a person for approval before it acts, must leave an auditable record of its own actions, resumes work after a restart, or coordinates several steps or services over time, even when FadoDB is not yet in the picture.
metadata:
  hat: own-principal
  status: stub
---

# The agent's own work as documents

**Status: stub, 2026-11-09.** This page says what this hat does, what
works today and the outline of the procedure. The reference files it
names under `../reference/` are not written yet.

## The hat

Here the agent is a principal in its own right. Its tasks, its proposals
and its sagas are documents of classes it designed, and the same engine
that refuses an unbalanced journal refuses the agent approving its own
proposal. What the agents did last night is a query; a restart resumes
from the state the documents hold.

## What works today

| Means | Mark |
|---|---|
| the agent as a principal with an id and groups, `As`; the log records it | shipped |
| a guard that keeps a transition from the agent's group; field rights per state | shipped with Phase 3 |
| idempotent retries, `Key`; the filter to poll for work | shipped |
| rules that call a registered executor back when a state is entered | designed, Phase 4 |
| subscriptions over the WebSocket instead of polling | designed, Phase 6 |

## Procedure, in outline

1. **Be a principal.** Take an id and a group of your own, and make every
   call as that principal. The guards hold you like anyone else, and that
   is the point: the log says what the agent did.
2. **The approval inbox.** One class: proposed, awaiting review, approved
   or declined, executed. The agent creates and submits; the approve
   transition's guard requires a human group and excludes the agent's;
   the fields of the proposal are read-only after submission. The agent
   polls for `state = "approved"`, acts, and moves the document to
   executed with the result.
3. **The task ledger.** One class: queued, running, needs a human, done
   or failed. Inputs are variables, attempts are rows with their moment
   and their error, hand-offs and timeouts are transitions. A restart
   queries for running tasks and resumes.
4. **The saga.** One document per operation across services: each step a
   state, each failure a compensating transition, the log the saga's
   history. With Phase 4 each step's call becomes an outbox action with
   retries.
5. **Retry with the key.** The same key makes the retry the same commit,
   so a lost answer is asked again without a duplicate.
6. **Poll until Phase 4.** A rule that calls the agent back on entering a
   state is designed; until it ships, the filter is the inbox.

## Worked examples, planned

`../../examples/classes/agent-inbox.json`, the approval inbox the front
page of fadodb.org links to, and a task ledger.

## References, to be written

`../reference/statechart.md`, `rights-and-rules.md`,
`commits-and-previews.md`, `querying.md`. Their sources are listed in
[`../reference/README.md`](../reference/README.md).
