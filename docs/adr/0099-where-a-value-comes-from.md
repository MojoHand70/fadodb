# ADR-0099: Where a value comes from, and the documents behind a number

- Status: Proposed; amends ADR-0037 (a table keeps the history of its rows), ADR-0055 (the commands `lineage` and `row_history` join the table; `flow`, `throughput` and `paths` take `documents`), ADR-0063, ADR-0096 and ADR-0097 (each number they give can be opened), ADR-0067 (the history of the rows says who changed a row; the log of reads still keeps a write past a rule), ADR-0078 (what it left out as "a history of a table" is taken up), ADR-0094 (the token of a table's rows is the id of the change that wrote them)
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

## Settled while building the second step

Decisions of mine, on 2026-11-27, each the owner's to strike.

8. **Whether it adds up is the formula's own answer.** The kept value's
   formula is worked out again by the engine, on the version that
   worked it out, with each lookup answered from the documents as they
   were; `adds_up` compares that with the value, and `rebuilt` says what
   it gives when the two differ. Adding up the parts by hand would hold
   only for a formula that is one sum.
9. **`now` and `changed_since` belong to each lookup**, for a formula
   may hold several; the value as a whole is what `adds_up` speaks of.
10. **The documents as they were are read among those that can have
    been read**: a document that matched the lookup then matches it
    now, or has changed since. The store finds those by its indexes and
    reads their earlier versions by their key, so a customer's 1,700
    invoices are read, not the class.
11. **A typed or a computed value's moment** is the version since which
    it is what it is, with that version's event.
12. **The reader's rule is asked with the reader's own values**, while
    a kept value's lookup is worked out with those of the commit that
    worked it out: its clock, its author, the document's state and id.
13. **A figure over a list of the document**, `SUM(lines.net)`, is
    refused for now with `not_allowed`: the list is read with `live`.
14. **The store answers it in both programs.** The copy is not asked
    yet.
15. **Lineage narrows by the index of a lookup's key column**: "the
    invoices of this customer" are found by the index on `customer`
    when the customer is not blank, and the events after the moment by
    the events' own key. A lookup worked out by itself, as a live value
    is, does not do so yet: its condition hides the column from SQLite,
    which then reads the class (seen on 2026-11-26, put to the owner,
    not mended in this record).

## Settled while building the third step

Decisions of mine, on 2026-11-27, each the owner's to strike.

16. **SQLite writes the history**, by two triggers on each table: a row
    whose values change is kept as it was, and so is a row removed. So
    every way of writing a row keeps it, an import, a delete, a
    replace, a rule's action, and any way added later. A change is
    written first in its transaction, and the triggers read it. The
    engine binds nothing more to a statement than it did, so a value
    that follows a table costs what it did (BenchmarkFollow: the same
    allocations).
17. **Who made a change is kept once a change**, not once a row, in
    `fado_table_changes`: its number, its kind, who, of what kind and
    for whom. Its id is an event id, so it is ordered with the commits
    of documents, and the token of the rows (ADR-0094) is it. The last
    change before a moment is found by halves, since the numbers and
    the ids rise together under the one writer.
18. **Each row says which change gave it its values**, `__since`, the
    change's number, in a column no name of the database's own can
    have. A table that keeps no history keeps it too, so a row
    unchanged since a moment is known to be as it was then. A row
    brought with the values it has is not written at all.
19. **A replace removes only what it did not bring.** The table is no
    longer emptied first: the rows go into a list of their own, the
    table takes what changed from it in one statement, and the rows it
    did not bring are removed after, each kept as it was.
20. **A delete that removes nothing is no change**, and leaves none.
21. **A version of a table carries the history with the rows**, by the
    rules of ADR-0084, the past as the present: a column added is
    blank in the past, a column whose type changes converts in the
    past too, and a value of the past that does not convert refuses
    the version. `"history": false` drops the history kept; leaving it
    out again keeps it from then. The moment a history began is kept,
    and lineage reads the past only after it.
22. **Engine schema 16.** A database of schema 15 is brought to it
    when it is opened: every table gains `__since` and its history, which
    begins at that opening. The rows it holds have no moment ("before
    the history"). An engine of schema 15 no longer opens the file: its
    writes would be kept with no moment.
23. **Removing a table removes the history of its rows**; who made
    its changes stays.
24. **A reading command, `row_history`**, in Go the call `RowHistory`:
    the history of one row by its key, the row now first and then as it
    was before each change, a page at a time, by the table's read rule.
    It answers the owner's "who changed this rate, and when?".
25. **A system table keeps no history** (ADR-0092): its rows are the
    engine's records. `row_history` refuses it.
26. **In lineage, a row of a table says `since` and `until`**: the
    change that gave it the values read, and the one after. Where the
    table kept no history at the moment, the rows are as they are now,
    and `rows_now` says so.
27. **The store answers it.** The copy in the analytical engine does
    not hold the history, and SQL does not read it, yet.

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
- Part 2, over documents: the command `lineage` and the call
  `Lineage`, with `LineageAsk` and what it answers; `PartsSQL` in the
  compiler, beside `AggregateSQL`, for the parts of a lookup; the
  manual's guide "Where a value comes from".
- Part 3: the history of the rows of every table, `"history": false`
  in the definition of a table, engine schema 16; the command
  `row_history` and the call `RowHistory`, with `RowHistoryAsk`,
  `RowHistory` and `RowVersion`; lineage of a value read from a table,
  as the rows were; the manual's section "The history of the rows" in
  the chapter on tables.

## Consequences

- An agent that reads a number of a process can name the documents
  behind it in the next call, and act on them.
- A chart of a process can open each of its numbers.
- The layout of a database changes with Part 3: a table of history
  beside each table, the column `__since` in each, the table
  `fado_table_changes`, engine schema 16.
- The file grows by the rows a change changes, and by nothing where no
  row changes.

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

Part 2, through the engine as this record builds it, from the store in
both programs; three askings, three runs that agree. Each rebuilt the
kept total exactly.

| `lineage` of a customer's kept total | `fadodb` and `fadodb-core` |
|---|---|
| 1,700 invoices, none changed since | 60 to 61 ms |
| 1,700,000 invoices, three changed since | 21.5 to 21.6 seconds |

Part 3, through the engine as this record builds it: its own
`ImportRows`, on disk, beside the engine of Part 2, three runs that
agree. A table of ten rates and one of a million prices.

| What | Before Part 3 | With the history |
|---|---|---|
| one rate changed (200 times, the middle of five rounds) | 12.5 to 13.2 millionths of a second | 28.8 to 29.9 millionths |
| a million prices brought first | 1.95 to 2.03 seconds | 2.42 to 2.46 seconds |
| a million prices brought again, 10,000 changed | 1.81 to 1.89 seconds | 2.02 to 2.08 seconds |
| the same as a replace | 1.59 to 1.64 seconds | 2.17 to 2.25 seconds |
| the file after three of each | 45.5 MB | 49.5 MB, 60,000 rows of history |
| a value that follows a table (BenchmarkFollow) | 171,389 allocations | 171,389 allocations |

- **One rate changed costs twice what it did**: the commit writes the
  change with who made it, the row as it was and its index, beside the
  row and the token of the rows. A value that follows the table costs
  milliseconds for its documents beside it.
- **A row kept stores the number of its change**, a few bytes, where
  an id would be 26: 4 MB more for a million rows.
- **A replace** brings its rows into a list of its own and writes what
  changed in one statement, in key order, then removes what it did not
  bring: a third more than emptying the table first, where the
  stand-in below took more than twice as long.

The figures below came first, from statements of mine written beside
the engine, before Part 3 was built.

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
