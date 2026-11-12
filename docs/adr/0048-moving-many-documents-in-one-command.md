# ADR-0048: Moving many documents in one command

- Status: Accepted; amends ADR-0040 (a second command that takes a transition), ADR-0025 (the preview of a batch), ADR-0026 (a key on a batch of moves)
- Date: 2026-11-12

## Context

A document moves from one state to another by `Transition`: one
document a command, one transaction a command (ADR-0040). Documents are
created many at a time by `CreateMany`: one transaction, a commit each,
all or none (ADR-0035). No command moves many.

The measurements of v0.4.0 put a number on the gap
(`docs/perf/v0.4.0.md`). A million documents moved by a command each
take 87 seconds, at 11,539 a second, and each of those commands opens
and closes a transaction of its own. Documents created in batches of
1,000 are written 2.7 times as fast as documents created one by one
(21,269 a second against 7,896), since a batch pays for one transaction.

The owner asked on 2026-11-12 what moving a million documents costs,
his case being a mass move such as the closing of a period, and said
the missing command is to be built. He ruled two things that day:

1. **One refused document does not stop the rest.** Ann books October:
   1,000 invoices take the transition `book`. Invoice 417 has no cost
   centre, so its guard is FALSE. 999 invoices are booked, and 417
   comes back with its reason.
2. **It is built after v0.4.0 is released**, and released by itself on
   his word.

The caller is as often an agent as a person, and nobody may be
watching. So the reply has to say what became of every document, a call
sent twice must not move a document twice, and the call has to be able
to run without writing.

Items 1 to 3 and 6 below follow from the rulings. Items 4, 5 and 7 to
12 were proposed with the record. The owner merged the proposal on
2026-11-12 with none of them changed, and accepted them with the
record.

## Decision

1. **The command.** `TransitionMany(ctx, class, name, ids, values,
   opts...)` moves the documents of one class named by `ids` along the
   transition `name`, in the order given, in one transaction. `values`
   are assignments applied to every document in its commit, and may be
   empty. `As` and `BusinessDate` apply to every document.

2. **Each move is the commit `Transition` makes.** For every document
   the engine checks the transition's access rule and the write rights
   for the principal (ADR-0041), applies the assignments, recalculates,
   evaluates the guard, recomputes the triggered variables and runs the
   rules (ADR-0043), follows the eventless and the timed transitions
   (ADR-0040, ADR-0047), and writes a version, a history row and an
   event with the origin `command`. The actions the commit queues go to
   the outbox (ADR-0044) and the wake-ups of the state it lands in with
   them (ADR-0047). Nothing in the document, its history or the log
   tells a move made in a batch from a move made alone.

   So the batch does what the same `Transition` commands would do one
   after the other, in that order, with three differences: they share
   one transaction, they share one moment, and one that is refused does
   not stop the others.

3. **A refused document does not stop the rest** (the owner's ruling).
   A document that cannot move is refused with the code `Transition`
   would give it: `unknown_document`, `wrong_state`, `guard_false`,
   `forbidden`, `terminal`, `too_many_transitions`, a code of a value
   that does not fit, or the code of a formula that failed. The engine
   then goes on to the next document. A refused document is left as it
   was: the engine marks the transaction before each document (a
   savepoint) and returns to the mark when the document is refused, so
   nothing of it stays written. A refusal is a response, not an event
   (ADR-0040, item 2): the log says nothing of invoice 417.

4. **The reply.** One entry for each id, in the order given: the id,
   and either the document as it is after its move or the refusal with
   its code, message, hint and variable. A call that returns no error
   may have moved every document, some or none; the reply says which.
   The names of the Go types settle in the build.

5. **What refuses the whole call.** The call returns an error and moves
   nothing when the call itself is wrong or the database fails:

   | Code | When |
   |---|---|
   | `unknown_class` | no class of that name |
   | `unknown_transition` | the class has no named transition of that name: one answer in place of the same answer for every document |
   | `too_many_elements` | more ids than the bound of item 10 |
   | `bad_value` | an id is given twice; the message names the id and both positions |
   | `not_allowed` | `At` is given: a command that writes takes no chosen moment (ADR-0042) |
   | `storage`, or the context's error | the database refused a statement, or the caller's context ended; the transaction is rolled back |

   So after a call that returns no error, exactly the documents the
   reply reports as moved have moved. After a call that returns an
   error, none has.

6. **One transaction.** Readers see none of the moves until the batch
   commits, and then all of them. The batch syncs once. The actions of
   all its commits are queued in that transaction, and the worker is
   woken once, after it. Each action then runs in a transaction of its
   own, as always (ADR-0044).

7. **One moment.** Every commit of a batch carries the moment the batch
   began, as the commits of `CreateMany` do. `NOW()` is the same in the
   formulas of every document, and the events stand in the log in the
   order of `ids`.

8. **A document sees the ones moved before it.** A guard or a formula
   that reads other documents (ADR-0046) sees the moves made earlier in
   the same batch, as it would after single commands. A guard that lets
   at most 100 invoices of a supplier be booked refuses the 101st of
   the batch. Where guards count, the order of `ids` matters, and the
   caller chooses it. A rule of one document changes another document
   only through the outbox, so after the batch.

9. **The key.** `CreateMany` takes no key, since a key names one command
   (ADR-0026) and nothing tells its commits apart before the documents
   have ids. A move has the document's id. `Key(k)` on a batch gives
   the commit of each document the key `k` with the document's id
   appended; the stored form settles in the build. Sent again with the
   same key, the batch moves no document a second time. A document the
   first call moved is answered with the document as that commit left
   it. A document the first call refused is tried again, as a refused
   single command is, since a refusal is not recorded. The same key
   with another transition or other values is `key_reused` for that
   document. An agent that lost the reply sends the same call again.

   Without a key, a batch sent twice is safe wherever the transition
   leaves the state, since the second call finds the documents in
   another state and refuses them with `wrong_state`. It is not safe
   for a transition that returns to its own state, such as a reminder,
   whose rules would fire twice. The key is for those.

10. **The bound.** A call takes at most 10,000 ids, the cap ADR-0013
    sets for a list and the page of `Due` and of a filter. The database
    has one writer, and a batch holds it for its whole length: other
    commits wait, and readers go on reading the state before the batch.
    The size to use is 1,000, as `Migrate` and the campaigns use. A
    million documents are a thousand calls.

11. **Trying it first.** `PreviewMany` takes the same arguments, runs
    the same batch and rolls the transaction back: the reply the call
    would give, and nothing written, no action queued, nothing woken.
    It takes `At` as `Preview` does (ADR-0042). It writes inside its
    transaction before taking everything back, so that item 8 holds for
    it too; previewing each document alone would not show the 101st
    invoice refused. It holds the writer as long as the call it tries.
    Ann sees that 417 has no cost centre, gives it one, and books all
    1,000.

12. **What this record leaves out.** Each of these waits for a case
    that needs it, and for a record or an amendment of its own:

    - Values that differ from document to document, such as the payment
      date and reference of each invoice on a bank statement. Here the
      values are the same for all; documents with their own values take
      `Transition` each.
    - A filter in place of the ids. The caller lists the documents with
      a filter and names them, so that the reply answers for each
      document the caller meant, and not for whatever the filter
      matches at that moment.
    - A form that is all or none.
    - `Set`, `Refresh` and `Delete` for many documents.
    - The engine's own moves when one timer comes for many documents,
      which the worker makes one transaction each (ADR-0047).

## Consequences

- Closing a period is a call for each thousand documents, and its reply
  is complete: an agent needs no second question to learn what moved.
- The caller has to read the reply. No error does not mean that
  everything moved.
- `CreateMany` stays all or none and `TransitionMany` is not. A load
  that fails in the middle is repeated whole; a mass move that meets
  one document that is not ready should not make the other 999 wait
  for it.
- The evidence is unchanged: every moved document has its own version,
  history row and event, with the person who moved it. With a key, the
  events of a batch carry it, and the log tells which moves one call
  made; without one, the principal and the moment do.
- One batch holds the writer for its length. A commit of another caller
  that arrives during a batch waits for it to end.
- A transition whose rule queues an action leaves one action a document
  for the worker. After a million such moves the outbox holds a million
  actions, about three minutes of the worker's time at the rate
  measured for v0.4.0.
- The command set of the Go API gains two commands. Nothing changes in
  the class format, the bytecode or the engine's schema.
- It belongs to Phase 4 as an addendum of 2026-11-12, and is released
  by itself on the owner's word.

## Performance

Budgets, on the reference machine, on disk, for a plain transition in
batches of 1,000:

- at least 15,000 documents a second, so a million documents in under
  70 seconds, where a command each takes 87;
- a refused document costs no more than a moved one;
- the savepoint of item 3 is measured by itself and published.

What to expect is more than the budget: the batched create is 2.7 times
the single one, and a move writes about what a create writes, so about
25,000 a second. The budget is set under the expectation, as the
budgets before it were set under what was later measured. The build
adds `BenchmarkTransitionMany` to the benchmark gate, a row for the
batch to the campaign of commits, and the rate of other writers while
batches run; the results are published with the release.
