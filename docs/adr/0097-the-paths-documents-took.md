# ADR-0097: The paths documents took

- Status: Proposed; amends ADR-0055 (one command, `paths`, joins the table), ADR-0089 (a move in the copy says which version it made; the third of what it calls the mining views is built)
- Date: 2026-11-26

## Context

The flow of a class (ADR-0063) says how many documents moved along
each transition. It does not say which ways whole documents went: how
many invoices were filed, checked and approved, and how many were
sent back first. The plan of the first days names that among its
mining views, as the different paths with how often each was taken,
and ADR-0089 lists it as not built.

The owner's answer of 2026-11-26 to the question of ADR-0095 was "yes
- all three". How the stays spread is ADR-0095 and the documents made
and finished in each period ADR-0096. This record is the third.

## Decision

1. **A new command, `paths`**, in Go the call `Paths`. It answers, for
   a class, the paths its documents took through the states, the most
   taken first:

   ```jsonc
   {"class": "Invoice"}
   ```

   ```jsonc
   {
     "paths": {
       "class": "Invoice",
       "class_version": 1,
       "documents": 4,
       "different": 4,
       "paths": [
         {"states": ["draft", "cancelled"], "documents": 1, "finished": true},
         {"states": ["draft", "review"], "documents": 1},
         {"states": ["draft", "review", "approved"], "documents": 1, "finished": true},
         {"states": ["draft", "review", "cancelled"], "documents": 1, "finished": true}
       ],
       "others": 0
     }
   }
   ```

2. **A path is the states a document came into, in their order.** The
   first is the state it was made in, and a state stands there as
   often as it was entered: an invoice that was sent back and came
   again went draft, review, draft, review, approved.

3. **The documents counted are those made in the period**, from
   `since` and before `until`, and each is counted with its whole
   path, up to now. A document that is still open is counted with its
   path so far, and `finished` says which paths end in a final state.

4. **The most taken path comes first**, and paths taken as often stand
   in the order of their states. `limit` says how many are listed, 20
   when left out and 1,000 at the most; `documents` says how many
   documents were counted, `different` how many different paths they
   took, and `others` how many took a path that is not listed.

5. **Each caller sees their own.** The documents counted are those the
   caller may read, by the read rule of the class, as in the flow. A
   deleted document is in no path. A content administrator who asks
   past the rule gives a reason, and the log of reads keeps the call.

6. **The moves of one moment are told apart.** A chain of transitions
   that one commit makes, an invoice submitted and cancelled by the
   engine at once, happens within one second. Each move makes a
   version of its document, and the versions say the order. The store
   reads it from the events. **In the copy a move now says which
   version it made**: the table of the moves gains a column, and a
   copy made before is given its moves again, once, as ADR-0089 does
   when what a copy holds changes.

7. **Both programs answer it, and the same.** The store joins the
   states of each document's moves in the order of the versions and
   counts the documents by what comes out; the copy does the same over
   the moves it keeps.

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **A command of its own**, named `paths`, as the page that put the
   question said.
2. **A path is told by its states, not by its transitions.** Two ways
   from review to cancelled, by a person and by the engine, are one
   path.
3. **The documents made in the period, with their whole path.** The
   other way was the moves made in the period, which would cut a path
   at both ends of it.
4. **Documents that are still open are counted**, with their path so
   far, and are told from the finished by `finished`.
5. **Twenty paths when nothing is said**, and how many documents took
   another, so that an answer is short and still adds up.
6. **The copy is given the version of each move** and not an order
   worked out from the moments, which two moves of one second do not
   give.

## What is built

- The command `paths` and the call `Paths`, with `PathsAsk`, `Paths`
  and `Path`.
- Its statement for the store and for the copy.
- The version of each move in the copy, and the moves of an earlier
  copy made again.

## Consequences

- An agent or a chart sees the usual way through a process and the
  ways round it, with how many documents went each.
- A class in which documents circle has many paths, one for each
  number of rounds. `different` says how many, and `limit` keeps the
  answer short.
- A copy made by an earlier build of this phase reads its events again
  when a program of this record first opens it: no release had the
  copy, so that is a matter for those who ran such a build.

## What this record leaves out

- **How long each path took.**
- **The paths told apart by their transitions.**
- **A path cut to the period**, or to its first or last steps.
- **The paths of documents that were deleted.**

## Performance

Measured on 2026-11-26 through the engine, on disk: 3,400,000
invoices that made ten million moves, on three paths. Each was asked
five times of the program with the copy and three of the one without,
and the middle one is given; three runs with the copy, which agree.

| The paths | `fadodb`, from the copy | `fadodb-core`, from the store |
|---|---|---|
| of every invoice | 0.22 to 0.27 seconds | 9 seconds |

A copy of that database made by an earlier build was given its ten
million moves again, with their versions, in 1 minute 18 seconds, at
the first opening; the openings after it took 50 ms.
