---
name: fadodb-operate
description: Run a FadoDB process on behalf of a person or a system. Create documents, set values, move transitions, read, filter and query, always as a principal, with an idempotency key on every commit and a preview before it. Use this skill whenever an agent has to act on records that live in FadoDB, such as submit, approve, pay, close, reopen, update, retry or resume a document, find out what a document allows next, or explain why a transition or a change was refused. Use it even when the request names the business object, the invoice, the permit, the leave request, and never says FadoDB.
metadata:
  hat: operator
  status: stub
---

# Operate a FadoDB process

**Status: stub, 2026-11-09.** This page says what the operator does, what
works today and the outline of the procedure. The reference files it
names under `../reference/` are not written yet.

## The hat

The operator acts on documents of a class that already exists, on behalf
of the person or the system it serves. It asks what is allowed, previews,
commits with a key, and reads a refusal as an answer rather than an
obstacle. The guards hold whoever is asking, the agent included.

## What works today

| Surface | Mark |
|---|---|
| the Go API: `Create`, `CreateMany`, `Set`, `Reset`, `Refresh`, `Transition`, `Transitions`, `Drain`, `Outbox`, `RetryAction`, `Get`, `Delete`, `Query`, `Count`, `SQL`, `History`, `Events`, `Preview` | shipped |
| many documents moved by one call: `TransitionMany` takes up to 10,000 ids of one class and one transition, moves each that can move in one transaction, and answers for every id with the document or the refusal and its code. No error does not mean that all moved: read the outcomes. Send 1,000 a call. `Key` makes the call safe to send again | shipped after v0.4.0 (ADR-0048) |
| the principal on every call, `As`; the idempotency key, `Key`; a moment for reads and previews, `At` | shipped |
| `Due`, the documents of a class that a commit would move now, or at a moment with `At`; `since` says since when, for a document a timed transition would move | shipped with Phase 4 |
| a timed transition, `after` or `at`: it fires in the first commit that touches the document once its time has come, and `Transitions` shows the moment as `fires_at` | shipped with Phase 4 |
| the engine moving a document by itself when its timer comes: `Outbox` and `<Class>_actions` list each timer waiting as the action `timer` with its moment in `due_at`; one that died says why, and `RetryAction` asks again | shipped with Phase 4 |
| the JSON command set over HTTP and WebSocket | designed, Phase 6 |
| the MCP server | designed, Phase 11 |

Until Phase 6 an agent reaches documents through a Go program that
embeds the engine, or through the embedder's own interface.

## Procedure, in outline

1. **Know who you act for.** Every call carries a principal with an id
   and groups (`As`). The event log records it; a rule can keep a
   transition for a human.
2. **Ask what is allowed.** `Transitions` lists each transition from the
   current state with whether it is enabled and, when it is not, a stable
   code such as `guard_false`.
3. **Preview before you commit.** `Preview` with the values and the
   transition runs the whole pipeline and writes nothing: the computed
   values, the path through eventless transitions, the moment its
   formulas saw.
4. **Commit with a key.** `Key("order-42")` makes a retry the same
   commit, so a loop that lost the answer can ask again. Give the
   commit its business date when the event has one (ADR-0026).
5. **Read the refusal.** A rejection names a stable code, an exact
   location and often a fix. A guard that is false is an answer, not a
   transient error; do not retry it, report it.
6. **Read sets with a filter or SQL, not a loop.** `Query` over a class
   and the read-only `SQL` endpoint answer in one call what a loop over
   `Get` answers in a thousand.
7. **Read history when asked what happened.** `History` is every version
   with who committed it and when; `Events` is the log.
8. **Choose the moment when it matters.** `At(t)` on a read or a preview
   makes `NOW()` and `TODAY()` that moment in every formula the call
   evaluates. A commit always runs at the database's clock.
9. **Move what is due.** A timed transition moves by itself: the
   engine touches the document when its time comes, and `Transitions`
   shows that moment as `fires_at`. A guard such as `TODAY() > due` is
   different: it comes to hold while nothing touches the document, and
   the document stays where it is until a commit touches it. `Due`
   lists those documents of a class, each with its state and the state
   it would move to. Touch each with `Refresh` and the commit moves it.
   One call lists at most 10,000, so ask again until the list is empty.
   With `At(t)` it answers what will be due at that moment. A document
   whose timer has come and that `Due` still lists, with `since`, is
   one the engine could not move: look for a dead `timer` row in
   `Outbox`.
10. **Move many with one call.** `TransitionMany` takes the ids of up to
    10,000 documents of one class and one transition, and moves each
    that can move, in one transaction, a commit each. A document that
    cannot move does not stop the others: it comes back with its
    refusal. So the call may return no error and still have moved
    nothing; read the outcome of every id. Send about 1,000 a call,
    since a batch holds the single writer for its length. Give the call
    a `Key`, and a call sent again moves no document twice.
    `PreviewMany` answers the same and writes nothing. The values are
    the same for every document; documents that need their own values
    take `Transition` each.

## References, to be written

`../reference/commits-and-previews.md`, `statechart.md`,
`rights-and-rules.md`, `querying.md`, `diagnostics.md`. Their sources are
listed in [`../reference/README.md`](../reference/README.md).
