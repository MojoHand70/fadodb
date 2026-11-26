# ADR-0091: A kept value that follows its table

- Status: Proposed; builds item 6 of ADR-0078 for a table; amends ADR-0015 and ADR-0043 (the trigger `table:<name>` is taken and fired), ADR-0044 (the outbox keeps a second kind of row of the engine's own, beside the wake-up), ADR-0078 (item 6: whose right is asked, which it left to its build)
- Date: 2026-11-26

## Context

A triggered variable keeps its value between its triggers (ADR-0015,
ADR-0043). The rate of an invoice is the rate of the moment accounting
checked it, and stays, though the table of rates moves on. That is
what the owner wanted of it, and it is built: the triggers are the
entry of a state, the exit of one, a transition, `before:` a
transition, and `refresh`.

Two more triggers are named in the records since ADR-0015 and were
refused when a class is defined:

| Trigger | The value is brought again |
|---|---|
| `table:<name>` | when the table it looks into changes |
| `schedule:<when>` | at a time: every night, every hour |

They are for the opposite wish: a value that is kept, so that it has
a history and costs nothing to read, and that still does not go stale
while its document is open.

ADR-0078, item 6, fixed three things about it, because its rule,
"nobody approves a number that changed under their eyes", fixes them:
each document it reaches gets a commit and a version of its own; it
is never brought into a document in a final state; and it is one
statement for all the documents it reaches, never one for each. It
left one thing "to be decided with its build": whose right is asked.

The case. Acme's estimates in a foreign currency keep the rate of the
table Rates. Ola writes estimates, Marek leads sales, and Tess, in
the treasury, keeps the table. She may change it, and may not so much
as read an estimate.

Measured before the build, on 500,000 invoices of which 100,000 held
a rate the table no longer had: one statement of the store finds them
in a fraction of a second, and one `refresh` for each, as a program
had to send them, takes 8 seconds, 80 millionths of a second for
each.

## Decision

1. **A triggered variable may list `table:<name>`**, where the name
   is a table its own formula looks into:

   ```json
   "rate": {"type": "decimal", "scale": 4,
            "formula": "LOOKUP(Rates, rate, code = THIS.currency)",
            "mode": "triggered", "on": ["enter:draft", "table:Rates"]}
   ```

   The name is written in any case of letters. A variable whose
   formula reads two tables may list both.

2. **When the rows of the table change, the variable is worked out
   again in every document of the class that is not in a final
   state, and kept where it comes out different.** A document whose
   value comes out the same is left alone: it gets no version and no
   event. That differs from a `refresh` a person sends, which always
   makes a version, since a person asked for the moment; a table that
   changes one row of a thousand would otherwise make a version of
   every document that reads the other rows.

3. **Nobody's right is asked.** A right is asked where a person
   decides. Tess decides to change the table, and the table's own
   `write` rule is asked for that (ADR-0051, item 7). What follows in
   the documents is what the class says: its designer wrote
   `table:Rates` on the variable. The engine writes it, as it writes
   a computed value that reads the rate, and as a trigger of the
   document's own move writes without asking whether the person who
   made the move may bring the value (ADR-0078, item 3). Tess needs
   no right over an estimate, and has none.

   The other way, which ADR-0078 proposed and this record does not
   take: the right of whoever changed the table. Then an estimate
   would follow only where the class lets the treasury bring its
   rate, which a class gives to the people who work on estimates. A
   change of the table would reach no document, and nothing would say
   so.

4. **A class stops the value in a state with a `write` right of
   `FALSE` on the variable there.**

   ```json
   "rights": {"sent": {"rate": {"write": "FALSE"}}}
   ```

   `FALSE` is nobody, and a table is nobody too: in `sent` the rate
   stands. It is the cell a designer already knows, resolved as any
   right (ADR-0069): the variable, its section, the sections around
   it, `*`, then the same in the `*` state. Every other `write` right
   is about the people who send a `refresh`, and does not stop a
   table: a cell that names accounting says who may bring the rate by
   hand, and the table still brings it.

5. **Each document the table brings gets a commit of its own**, and
   it is a commit like any other:

   - a version, and an event of the kind `refresh` whose name is
     `table:<Name>` and whose actor is whoever changed the table, so
     that the history of a document says which table moved its value
     and whose change it was;
   - the computed values that read it are worked out, the rules of
     the class fire, and a transition whose guard now holds fires;
   - a command that says it saw the version before is refused with
     `stale_version` (ADR-0078, item 2).

6. **The change of the table queues the work, and the worker does
   it.** The transaction that changes the rows writes one row of the
   outbox (ADR-0044) for each class that follows the table: its
   action is `follow`, its `rule` is `table:<Name>`, its class is the
   one to bring, and it names no document. So the work can be neither
   lost nor invented. The worker runs the row afterwards:

   - one statement of the store finds the documents whose kept value
     is not what the formula gives now, in the states where the
     variable follows;
   - they are brought a thousand in a transaction, each with its own
     version and event, and the writer is let go between two
     transactions, so that other people's commits go in between;
   - the row is marked done in the transaction of the last thousand.

   A process that stops in the middle leaves the row pending. The
   next look finds what is left, because the documents already
   brought hold what the formula gives.

7. **What changes a table**: `import_rows`, `delete_rows` that
   removed a row, the actions `table.insert`, `table.update` and
   `table.delete` of a rule, and a new definition of a table that
   holds rows.

8. **A value that cannot be worked out, or that the variable does
   not accept**, leaves the value as it was and keeps the code beside
   it, as on any trigger (ADR-0019): a rate typed as 44.0000 against
   a `max` of 10 leaves 4.2500 and `out_of_range`, in a version of
   its own, and the same failure a second time writes nothing.

## Questions for the owner

All three are answered. They are kept here as they were asked, with
the other way, so that he can turn one round later.

1. **Whose right is asked when a table changes** (item 3).
   **Answered on 2026-11-26: nobody's.** His words: "Tess who changes
   FX rates doesn't necessarily have rights to change invoices."
   - The record's own proposal had been the right of whoever changed
     the table, as an action runs as the person whose command queued
     it. It was mine, and wrong for this: see item 3.
   - With it came one more question, **whether a class can stop the
     value in a state that is not final**, answered the same day with
     **yes**, by the `write` right of `FALSE` (item 4). The other
     way: the value follows in every state that is not final, and a
     class that wants a rate to stand from `sent` on has no following
     rate before.

2. **Who brings the value when a schedule fires.** **Answered on
   2026-11-26: the engine itself**, and the event says so. The other
   way: a named service user with rights like anybody's.

3. **How a schedule is written.** **Answered on 2026-11-26: in
   words**, and more of them than the three first proposed: he named
   "every 3rd Monday of a month", "daily at 3.00 am" and "repeat
   every X hours". The other way: the five fields of a Unix
   timetable.

Questions 2 and 3 are for `schedule:`, which this record does not
build; see "What this record leaves out".

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **The work is a row of the outbox, and no table of its own.** The
   outbox already is where the engine keeps what it still has to do,
   with retries, a bound on chains and a listing for an
   administrator. The engine's own tables do not change, and the
   engine schema stays 15.
2. **A thousand documents in a transaction**, the size a batch of
   moves is told to have (ADR-0048), and a millisecond between two
   transactions in which the writer is left alone. Without that
   moment the worker, which asks for the writer again at once, is
   often there before a commit that waited, and the commit waits for
   a second transaction.
3. **The event has no cause.** A change of a table writes no event,
   so there is none to name; the event's name says the table and its
   actor who changed it.
4. **The document says it was last changed by whoever changed the
   table**, in `updated_by`, as its event does.
5. **What the commit queues runs as whoever changed the table**
   (ADR-0044, item 11). A rule of the class that fires on the new
   value and acts on another document asks that person's rights
   there. A designer who wants such a rule gives those rights to the
   people who keep the table.
6. **A document that cannot take the value keeps what it holds.**
   When a value computed from the new one fails, or a rule of the
   class refuses the commit, that document is taken back and the
   others go on. The row of the outbox says how many could not, and
   why the first could not, in `into_code` and `into_message`. The
   document itself says nothing: not planned.
7. **A change of a table by a rule's action is one action deeper**
   than the action, and a follow deeper than the bound of a chain
   (ADR-0044) is written dead with `too_deep`. A class whose own rule
   writes the table it follows so comes to an end.
8. **Every change of the table queues the row**, one that changes no
   value too. The statement then finds nothing.
9. **The trigger does not fire when a class is defined.** A variable
   that a new version of a class adds or makes follow is brought in
   the documents that exist at the next change of the table, as any
   new triggered variable waits for its trigger.
10. **The follows of one class run one at a time**, in the order they
    were queued. **The worker looks at what else is due after each
    thousand**, so that the actions and the timers of other documents
    do not wait for a large class to be brought. Where a follow stands
    between two thousands is kept in memory: a process that stops
    loses it, and runs the row from the start of the class, which
    finds what is left.
    Replaced by ADR-0093, decision 9: a follow waits behind no other
    row, so that a schedule which waits a month holds back no table;
    two follows of one class may take turns.
11. **The business date of such a commit** is the day the worker
    makes it.
12. **Refused when a class is defined**: `table:` naming a table the
    variable's formula does not look into, with `not_allowed`; and,
    as not built, `table:` naming a class, and `table:` on a list
    that is kept, with `not_supported`.
13. **Where the statement cannot compare**, every document in the
    variable's states is taken, and the commit of each decides by
    working the value out. That is so when the formula reads who
    created the document or another fact of its life, the documents
    of a class or a list the document holds, and when the formula
    gives another type than the variable keeps. The answer is the
    same; it costs a look at every open document.

## What is built

- The trigger `table:<name>` in the class format, with its checks.
- The row of the outbox, queued by every change of a table's rows and
  by a new definition of a table that holds rows.
- The worker's run of such a row, and `Drain`'s.
- `RetryAction` sends a dead row again, as any.

`BenchmarkFollow` is in the benchmark gate.

## Consequences

- A kept value can be trusted while its document is open, without a
  program that knows which documents to refresh.
- It is not instant. A reader who looks a moment after the change may
  still see the old value in some documents. Each document says which
  version brought its value, and when.
- A change of a table that many documents follow makes as many
  versions and events. A hundred thousand documents are a hundred
  thousand rows of history.
- The outbox shows the work: a row for each class, pending while it
  runs, done after, dead with its code when it could not run.
- Documents in a state where the class closes the variable are never
  brought, whoever changes the table. A class that closes it in every
  state has a trigger that does nothing; nothing says so when the
  class is defined. Not planned.

## What this record leaves out

- **`schedule:<when>`**, the same machine with a clock in front. It
  is built since, by ADR-0093, with the database's own time zone for
  the question that stood here. As this record left it: the engine
  itself brings the value
  (question 2), and a schedule is written in words (question 3).
  Proposed for the words, and open: `schedule:hourly`,
  `schedule:daily at 03:00`, `schedule:weekly on friday at 18:00`,
  `schedule:monthly on 3rd monday`, `schedule:monthly on last day`,
  `schedule:every 4h`, the length written as a transition's `after`
  is. One question stands before it: every clock of the engine is UTC
  (ADR-0047, item 16), so "at 03:00" is 4 or 5 in the morning in
  Warsaw.
- **A value that follows the documents of another class**: "the
  order's total follows its invoices". The same idea over a class,
  and the harder one, since every commit of an invoice would reach
  back to its order. Today a rule of the other class sends the
  refresh.
- **A list that is kept** (ADR-0078, item 5) does not follow a table.
- **The analytical engine.** The store finds the documents itself;
  the copy (ADR-0089) is not asked. It could find them over a very
  large class once tables are in the copy.

## Performance

Measured on 2026-11-26 through the engine itself, on disk, three runs
that agree: 500,000 invoices in ten currencies. Tess changes two of
the ten rates, so 100,000 invoices hold a rate the table no longer
has.

| What | Time |
|---|---|
| the change of the table, with the row of the outbox | 0.1 to 0.2 ms |
| the 100,000 invoices brought, in 101 transactions | 4.7 to 4.9 seconds, 48 millionths of a second each |
| a commit of somebody else meanwhile | 45 ms in the middle, 70 ms at the longest |
| a change that leaves every rate as it was: one look over the class | 250 ms |

For scale, in the same database: a batch of a thousand moves takes 39
millionths of a second for each document, and a `refresh` for each of
the 100,000, as a program had to send them before, 80.

The plan's number for this was 100,000 documents under 2 seconds. It
is not met: each document gets a commit of its own, with its history
row and its event, and that is what the time is.

`BenchmarkFollow`, a thousand documents brought by one change of a
table, takes 43 ms beside the 39 of `BenchmarkTransitionMany`.
