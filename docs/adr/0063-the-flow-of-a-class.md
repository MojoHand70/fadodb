# ADR-0063: The flow of a class in one call

- Status: Proposed
- Date: 2026-11-16

## Context

A chart of a process shows its states and the arrows between them, and
is worth looking at when it also shows where the documents are and how
they move: twelve invoices in review, the oldest since Monday, three
hundred approved this month. An agent that is asked why approvals are
slow needs the same numbers before it says anything.

Today a caller has the parts and not the picture. The definition of a
class has its states and transitions (`get_class`). `count` answers how
many documents match a filter, one state at a time. The views
`<Class>_states` and `<Class>_events` have every stay and every move,
and SQL reads them, but SQL is for an analyst and reads everything
(ADR-0051): a clerk who may read their own invoices cannot be given
it. So a face would send a dozen commands for one chart, and could not
draw the moves at all for most of its users.

The owner ruled on 2026-11-14 that one call which answers the flow of a
class belongs to Phase 6, beside the check of a class from the command
line.

## Decision

1. **One command, `flow`, answers the flow of a class**: its states,
   each with the documents that are in it now, and its transitions,
   each with the moves along it. In Go it is the call `Flow`.

   ```json
   {"class": "Invoice", "since": "2026-11-01", "until": "2026-12-01"}
   ```

   ```json
   {"flow": {
     "class": "Invoice", "class_version": 1,
     "since": 1793491200, "until": 1796083200,
     "created": 4,
     "states": [
       {"name": "draft", "initial": true, "documents": 0,
        "stays": 4, "average_seconds": 27900, "longest_seconds": 100800},
       {"name": "review", "documents": 1, "oldest_since": 1793613600,
        "stays": 2, "average_seconds": 5400, "longest_seconds": 10800},
       {"name": "approved", "final": true, "documents": 1, "oldest_since": 1793624400,
        "stays": 0, "average_seconds": 0, "longest_seconds": 0},
       {"name": "cancelled", "final": true, "documents": 2, "oldest_since": 1793613600,
        "stays": 0, "average_seconds": 0, "longest_seconds": 0}
     ],
     "transitions": [
       {"name": "submit", "from": "draft", "to": "review", "moves": 3},
       {"from": "review", "to": "cancelled", "automatic": true, "moves": 1},
       {"name": "approve", "from": "review", "to": "approved", "moves": 1}
     ]
   }}
   ```

2. **It counts the documents its caller may read, and no other**, by
   the read rule of the class, as `query` and `count` do (ADR-0049).
   Two callers get two flows of one class. A count is information: the
   number of invoices in `disputed` tells somebody who may not read
   them that there are disputes. Somebody who must see every document
   is given a read rule that says so. The chart itself, the states and
   the transitions, is the definition of the class, which every caller
   reads.

3. **Where the documents are is now; the moves and the stays are
   counted in a period.** `since` is its first moment and `until` the
   moment it ends before, each a moment as `at` is written (ADR-0042).
   Left out, the period is since the class began. A period that ends
   before it begins is refused with `bad_value`.

4. **For a state**: `documents`, how many are in it now; `oldest_since`,
   the moment the one that is there longest entered it, left out when
   the state is empty; `stays`, how many stays in it ended in the
   period; `average_seconds` and `longest_seconds`, how long they
   lasted. A stay ends with the move that leaves the state, and its
   length is that move's moment less the moment the state was entered,
   which the version the move left carries (ADR-0047).

5. **For a transition**: `moves`, how many times a document moved along
   it in the period. A transition that leaves several states is listed
   once for each. `automatic` marks one with no name, which the engine
   takes when its guard holds, and `timed` one that fires by time.
   `created` says how many of the documents were created in the period.

6. **A class that was defined again keeps what happened.** A state or a
   transition the class no longer has is listed after the class's own,
   marked `retired`, when documents left the one or moved along the
   other in the period. A state that documents are in cannot be taken
   out of a class, so no document is in a retired state.

7. **A deleted document is in no number**, neither where it was nor how
   it moved, as it is in no answer of `query`.

8. **The command takes `for`, and not `at`.** The flow of an earlier
   moment is not built: the documents in each state are counted now.

9. **The work is two statements, whatever the number of documents**
   (the rule of ADR-0020 for a lookup, kept here): one that groups the
   readable documents by state, and one that groups their created and
   transition events by transition, joined to the version each move
   left.

## Questions for the owner

1. **Whose documents are counted.** Ann, in finance, reads every
   invoice; Bob, in sales, reads his own. Proposed: each sees the flow
   of what they may read, as in item 2. The other way: everybody's
   documents, for anyone who may see the class. One chart for all, and
   Bob learns how many invoices sit in a state he cannot look into.
2. **Over which time.** Olga asks how invoices moved in November.
   Proposed: the documents are counted now, and the moves and stays in
   the period the caller gives; with none, since the class began, as in
   item 3. The other way: a fixed window when none is given, such as
   the last thirty days.
3. **What it says about waiting.** Proposed: for each state, how many
   are there, since when the oldest waits, and for the stays that ended
   their number, their average and the longest, as in item 4. The other
   way: the median as well, which is truer when one invoice waited a
   year, and costs a sort of the stays of each state.

## Consequences

- A face draws a chart of a process with one command, for a user with
  any rights, and an agent reads where documents wait before it
  answers.
- Two people who compare their charts see different numbers when they
  may read different documents. A face says whose view a chart is.
- The call reads every document its caller may read, and every move of
  those documents in the period. It is for a chart and a report, and
  not for something a program asks a thousand times a second.
- A flow of the past, a median, and the moves split by how each came
  about (a command, a guard, a timer) are not in it. SQL answers them
  for an analyst today.

## Performance

No commit is touched, and no table or index is added. Measured on the
development machine, on disk, on a class whose read rule lets sales
read their own invoices and finance every one, with three in four
invoices submitted, half approved and a quarter paid
(`TestFlowCampaign`, the median of forty calls):

| Documents in the class | Who asks | Documents counted | Moves counted | The call takes |
|---|---|---|---|---|
| 100,000 | finance, since the class began | 100,000 | 150,000 | 361 ms |
| 100,000 | one of sales, who reads a hundredth | 1,000 | 1,500 | 15 ms |
| 100,000 | finance, for the last day | 100,000 | 1,500 | 80 ms |
| 1,000,000 | finance, since the class began | 1,000,000 | 1,500,000 | 4.2 s |
| 1,000,000 | one of sales, who reads a hundredth | 10,000 | 15,000 | 161 ms |
| 1,000,000 | finance, for the last day | 1,000,000 | 15,000 | 907 ms |

The cost follows the documents the caller may read: about four
microseconds for each, with its moves. A short period does not make
the call as short as its moves are few, because the events are kept in
the order of their documents and not of their moments.

No budget is set. Two things would make a large class faster, and each
costs every commit, so neither is done before a chart needs it: an
index of the events by moment, for a short period; and counts kept as
documents move, for the whole picture.
