# ADR-0099: Where a value comes from, and the documents behind a number

- Status: Proposed; amends ADR-0037 (a table keeps the history of its rows), ADR-0055 (the command `lineage` joins the table; `flow`, `throughput` and `paths` take `documents`), ADR-0063, ADR-0096 and ADR-0097 (each number they give can be opened), ADR-0078 (what it left out as "a history of a table" is taken up)
- Date: 2026-11-26

## Context

On 2026-11-26 the owner wrote: "Let's say a document displays a value,
which is a sum of other documents values, which can also be sums of
other documents values or sums from tables records. I think users,
human or Agents, might be interested in getting to know where those
values come from and / or their lineage." The same night he extended
Phase 8 "into linneage and drill down analytical capabilities".

Two walks, in two directions:

- **Drill down** goes from a number to the documents it counts. The
  flow says that 766,915 invoices moved along `approve` in a week;
  which ones?
- **Lineage** goes from a value to what made it. A region keeps what
  its customers add up to, a customer what its invoices add up to, an
  invoice its net from an amount somebody typed and a rate from a
  table. Where does the region's number come from?

Where they meet, the parts of a kept total are the documents behind
it.

What the engine keeps today, checked on main at ea5560c:

- **Every version of every document**, with its moment, who made it
  and the event that wrote it, in the order the engine wrote them; the
  lists of a document with each version.
- **A kept value says when it was worked out**: the version, and so
  the event, whose commit worked it out (ADR-0043). Its formula read
  the other documents as they were stored before that commit began.
- **The engine knows what each formula reads** inside its own
  document; it hands that to a page today (ADR-0070).
- **A table keeps nothing of what a row was** (ADR-0037). ADR-0078
  listed "a history of a table" as not planned. A table's rows carry a
  token of their last change (ADR-0094), made from the same clock as
  the events, so the engine can tell whether a table changed after a
  value was worked out, and no more.
- **A condition cannot ask about a document's life**: when it moved,
  along what, how long it stayed. So nothing lists the documents
  behind the numbers of `flow`, `throughput` and `paths`, although the
  copy keeps every move (ADR-0089, item 2) and the store every event.
- **ADR-0015 promised** that "every computed value is explainable".
  No command gives the explanation.

The owner was shown the analysis with the case below, the costs
measured, one recommendation and one question: should a table keep
the history of its rows? A as today, B every table, C a table that
asks. He answered "1. B" on 2026-11-26.

## Decision

### Part 1: the documents behind a number

1. **`flow`, `throughput` and `paths` take one more member,
   `documents`**, which names one number of their answer. The command
   then answers the documents behind that number, in place of the
   numbers. In Go the calls are `FlowDocuments`, `ThroughputDocuments`
   and `PathsDocuments`, each with the ask of its command and a
   `DocumentsAsk`.

   ```jsonc
   {"class": "Invoice", "since": "2026-11-02", "documents": {"transition": "submit"}}
   ```

   ```jsonc
   {
     "documents": {
       "count": 3,
       "rows": [
         {"id": "D-1", "state": "cancelled", "at": 1793613600, "version": 2, "by": "dan"},
         {"id": "B-2", "state": "review", "at": 1793613600, "version": 2, "by": "bob"},
         {"id": "B-1", "state": "approved", "at": 1793613600, "version": 2, "by": "bob"}
       ],
       "more": false
     }
   }
   ```

2. **The numbers that can be opened:**

   | Command | `documents` | The documents | In the order |
   |---|---|---|---|
   | `flow` | `{"created": true}` | made in the period | the newest first |
   | `flow` | `{"state": "review"}` | in the state now | the one there longest first |
   | `flow` | `{"stays": "review"}` | each stay in the state that ended in the period | the longest first |
   | `flow` | `{"stays": "review", "at_least": "slow_tenth"}` | of those, the stays that lasted at least the slow tenth; `"middle"` for the middle | the longest first |
   | `flow` | `{"transition": "approve"}` | each move along it in the period; `from` and `to` narrow a transition that leaves several states, and name one that has no name | the newest first |
   | `throughput` | `{"made": "2026-11-02"}` | made in the period of `by` that begins that day | the newest first |
   | `throughput` | `{"finished": "2026-11-02"}` | finished in it | the newest first |
   | `paths` | `{"path": ["draft", "review", "approved"]}` | made in the period, that took the path | the newest first |

3. **`count` is the number the command gives**, counted the same way
   over the same documents: the same period, the same day of the
   database's time zone, the same reader. A stay or a move is listed
   as often as it happened, as the flow counts it.

4. **A page at a time.** `limit`, 50 when left out and 1,000 at the
   most, and `offset`, inside `documents`; `more` says that rows follow.
   Each row is the document's `id`, its title and its state now, and
   what put it into the number: `at`, the moment it moved, entered,
   was made or finished; `seconds`, how long a stay lasted, left out
   for a stay whose beginning the engine does not know; `version`, the
   version the move made; `by`, who made it.

5. **Each caller sees their own**, by the read rule of the class, as
   the numbers are counted. A content administrator who asks past the
   rule gives a reason, and the log of reads keeps the call and the
   documents only the override showed (ADR-0067).

6. **A name nobody has refused.** A state or a transition that the
   class has not and that no document of it ever had is refused with
   `bad_value`, and the hint lists what the class has; one the class
   no longer has, with documents that went through it, is answered.
   Two numbers at once, `at_least` without `stays`, a day that does
   not begin a period of `by`, are refused.

7. **Both programs answer it, and the same**: the copy over the moves
   it keeps, the store over the events and the versions.

### Part 2: lineage

8. **A command, `lineage`**, in Go the call `Lineage`. It answers one
   value of one document and what it is made of, one level: the caller
   asks again for a part.

   ```jsonc
   {"class": "Customer", "id": "Acme", "variable": "invoiced"}
   ```

   It says the value; its kind (typed, computed, kept, live); its
   formula; for a kept value when, by which trigger, in which version
   and by whose commit it was worked out; for a typed value who typed
   it, of what kind and for whom, when and in which version; and what
   it read, with the values of the parts. `version` asks for the value
   of an earlier version, so that a walk down stays in the past.

9. **Rebuilt when asked.** No commit writes anything more. A kept
   value's parts are the documents as they were just before the commit
   that worked it out, in the order the engine wrote them.

10. **It says whether it adds up**, which parts changed since, by whom,
    and what the value would be now.

11. **The reader's rights hold.** Parts the reader may not read are
    counted and summed, never shown.

12. **A table's part** is the row as it was, from the history of Part
    3; for a table that keeps none, the row now and whether the table
    changed since.

### Part 3: the history of a table's rows

13. **Every table keeps the history of its rows**: the row as it was,
    when it changed and who changed it, by which import or rule. The
    owner's answer, "1. B".

14. **A table may say `"history": false`**: one brought in whole that
    no decision reads.

15. **Only what changed is kept.** A row that is brought again with the
    same values is not written; a replace keeps the rows it changed or
    took out, not the whole table.

## Decisions of mine, each the owner's to strike

Settled on 2026-11-26.

1. **The name `lineage`**, the owner's word.
2. **One level a call**: the caller asks again for a part, and an
   answer stays short however deep the values go.
3. **Parts a reader may not read are counted and summed, never
   shown.** The total, which the reader may read, already tells the
   sum; the answer adds only how many.
4. **Drill down is one more member on the command that gave the
   number**, not a command of its own, as `live` answers a value or a
   page of a list. The period, the reader and the class are those of
   the number.
5. **The orders of item 2**, and fifty rows a page.
6. **A stay with no known beginning** is listed with no `seconds`, and
   left out of `at_least`, as the spread leaves it out.
7. **Rebuilt when asked, nothing written by commits**, for lineage
   over documents.

## The steps

Each is one pull request.

1. This record, and Part 1.
2. Part 2, over documents.
3. Part 3, and the table's part of Part 2.

## What is built

- Part 1: `documents` on `flow`, `throughput` and `paths`; the calls
  `FlowDocuments`, `ThroughputDocuments` and `PathsDocuments`, with
  `DocumentsAsk`, `Documents` and `DocumentRow`; their statements for
  the store and for the copy.

## Consequences

- An agent that reads a number of a process can name the documents
  behind it in the next call, and act on them.
- A chart of a process can open each of its numbers.
- The layout of a database changes with Part 3: a table of history
  beside each table.

## What this record leaves out

- **Dashboards**, the line after "lineage and drill down" in the
  owner's list.
- **The documents behind a line of totals by group** (ADR-0090): the
  same `QUERY` without its totals, with the line's values in its
  condition, answers it today.
- **A value brought from outside**, an answer of an action written back
  (ADR-0044): lineage leads to the action's row and stops there.
- **The paths of documents that were deleted**, as ADR-0097.

## Performance

Measured on 2026-11-26 on a copy of the database of 3,400,000 invoices
(ten million versions, ten million moves), on disk, on the owner's
computer.

Part 1, through the engine as this record builds it: five askings of
the program with the copy and three of the one without, the middle
one, three runs that agree. Each list held exactly as many documents
as the engine's number.

| The documents behind | `fadodb`, from the copy | `fadodb-core`, from the store |
|---|---|---|
| 766,915 moves along `approve` in a week | 70 to 80 ms | 2.9 to 3.0 seconds |
| the 114,802 stays of the week that lasted at least the slow tenth, the spread worked out first | 74 to 77 ms | 7.0 to 7.2 seconds |
| 108,215 invoices finished on a day of Warsaw | 37 to 42 ms | 2.4 to 2.5 seconds |
| 166,600 invoices that took one path | 0.53 to 0.56 seconds | 6.3 to 6.5 seconds |

Parts 2 and 3 are not built. Their figures are from statements of
mine written beside the engine, and each is measured again through
the engine as it is built.

Lineage, the engine at 373839a; a customer with 1,700 invoices and
one with 1,700,000, three runs of five askings, which agree:

| One level of the answer | 1,700, copy | 1,700, store | 1,700,000, copy | 1,700,000, store |
|---|---|---|---|---|
| the parts as they were: how many, their sum | 41 to 42 ms | 0.76 seconds | 143 to 146 ms | 3.9 seconds |
| the fifty largest, as they were | 49 to 51 ms | 0.76 seconds | 168 to 172 ms | 3.9 seconds |
| which changed since | 1 ms | 3.2 seconds | 1 ms | 3.2 seconds |

Rebuilt as they were, both customers' parts added up to their kept
totals exactly.

A history of a table's rows, a stand-in in which SQLite keeps the row
as it was; ten rates and a million prices, three runs:

| What | As before | With the history |
|---|---|---|
| one rate changed | 10 millionths of a second | 13 millionths |
| a million prices brought again, 10,000 changed | 2.0 to 2.3 seconds | 2.4 to 2.6 seconds |
| the same as a replace, keeping every row taken out | 1.9 to 2.0 seconds | 4.2 to 4.4 seconds |
