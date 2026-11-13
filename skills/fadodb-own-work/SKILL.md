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
| the principal says it is an agent, and for whom it acts: `Kind: fado.ActorAgent`, `For: "ann"`; every event records both, and a class reads them with `USERKIND()` and `ACTINGFOR()` | shipped with Phase 5 |
| acting for a person needs a substitution (ADR-0053): the person creates a `Substitution` with `by` your id, for a time and for the classes they choose, and until then every call of yours that names them in `For` is refused with `forbidden` and the path `/principal/for`, a read too. The message says what is missing: none, one that begins later, one that has ended, one for other classes. Ask first with `Preview`; never work around it by dropping `For`, which makes the act your own. You cannot give yourself one, and you cannot pass one on. Under a host program that keeps its own substitutions (`HostAnswersFor`) the engine asks nothing | shipped with Phase 5 |
| a guard that keeps a transition from the agent's group; field rights per state | shipped with Phase 3 |
| what the agent may do is what the formulas of the class give it: a document its caller may not `read` does not exist for it, a refused change is `forbidden` with the place of the rule, and a rule a class leaves out lets nobody through. Ask first with `Preview` and `Transitions` | shipped with Phase 5 |
| an agent that is a content administrator, in the system group `content-administrators`, acts on any document past the rules of its class, never past a guard. Such an act is an override: pass `fado.Reason("...")` or get `reason_required`; the event keeps the rules lifted and the reason, `Event.Override` and `Event.Reason`. `Preview.Override` and `TransitionInfo.Override` say beforehand that an act would be one. Give the true reason: it is the record of why a rule was set aside | shipped with Phase 5 |
| idempotent retries, `Key`; the filter to poll for work | shipped |
| rules that call the agent's program back when a state is entered | shipped with Phase 4: a Go function the host program gives to `Open` in `Options.Actions`, with a definition stored by `DefineAction`, is called through the outbox with the event and the document; an agent in another process is called over HTTP by a definition with a `request` that points at its endpoint, signed, where the host program allows that host |
| subscriptions over the WebSocket instead of polling | designed, Phase 6 |

## Procedure, in outline

1. **Be a principal.** Take an id and a group of your own, and make every
   call as that principal. The guards hold you like anyone else, and that
   is the point: the log says what the agent did. Say what you are: the
   kind `agent`, and under `For` the id of the person you work for when
   there is one. The log then tells your steps from a person's, and a
   class can give you the preparation and keep the decision for them.
   Do not borrow a person's id to get past a rule.
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
   history. Each step's call is an outbox action with retries: a Go
   function of the host program, or a request the engine sends.
5. **Retry with the key.** The same key makes the retry the same commit,
   so a lost answer is asked again without a duplicate.
6. **Be called back, or poll.** Where the agent's program embeds the
   engine, a rule on entering a state calls its Go function: the
   function receives the event and the document at that version, makes
   twice once by the call's key, and returns an error to be asked again
   later. An agent outside that process is called over HTTP: a
   definition with a `request` points at its endpoint, the request
   carries the call, signed, and the agent answers 2xx, or 5xx to be
   asked again. Polling with the filter still works where no endpoint
   can be reached.

## Worked examples, planned

`../../examples/classes/agent-inbox.json`, the approval inbox the front
page of fadodb.org links to, and a task ledger.

## References, to be written

`../reference/statechart.md`, `rights-and-rules.md`,
`commits-and-previews.md`, `querying.md`. Their sources are listed in
[`../reference/README.md`](../reference/README.md).
