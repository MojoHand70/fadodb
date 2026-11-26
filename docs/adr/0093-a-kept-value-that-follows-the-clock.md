# ADR-0093: A kept value that follows the clock, and the time zone of a database

- Status: Proposed; follows ADR-0091, which built `table:` and designed this; amends ADR-0015 and ADR-0043 (the trigger `schedule:<when>` is taken and fired), ADR-0044 (a follow stands outside the order of a document's actions), ADR-0047 (item 16: a schedule on a variable no longer waits; a database has a time zone, for its schedules alone), ADR-0091 (decision 10: how the follows of one class run)
- Date: 2026-11-26

## Context

ADR-0091 built the trigger `table:<name>`: a kept value that is
worked out again when the table it looks into changes. It left the
other trigger the records name since ADR-0015, `schedule:<when>`, with
two of the owner's answers and one question standing before it.

Some values move with the day and with nothing else. How many days a
bill is late is such a value. Kept in a triggered variable it has a
history, a list is sorted by it and a rule fires on it; but then it is
the number of the day somebody last refreshed the bill.

The case. Acme, in Warsaw, wants `days_late` of every open bill worked
out each night at three.

The question that stood: every clock of the engine is UTC (ADR-0047,
item 16). "At 03:00" would be four in the morning in Warsaw in winter
and five in summer.

## Decision

1. **A triggered variable may list `schedule:<when>`**, where the
   words say when:

   ```json
   "days_late": {"type": "number",
                 "formula": "MAX(DAYS(TODAY(), due), 0)",
                 "mode": "triggered", "on": ["schedule:daily at 03:00"]}
   ```

   | Written | Fires |
   |---|---|
   | `hourly` | at every full hour |
   | `daily`, `daily at 03:00` | at midnight; every night at three |
   | `weekly`, `weekly on friday at 18:00` | on Monday at midnight; on Friday at six in the evening |
   | `monthly`, `monthly on 15` | on the first; on the 15th, a day from 1 to 28 |
   | `monthly on last day at 23:00` | on the last day of the month |
   | `monthly on 3rd monday at 06:00` | on the `1st`, `2nd`, `3rd`, `4th` or `last` of a day of the week |
   | `every 15m`, `every 4h` | every quarter of an hour; every four hours, counted from midnight |

   A time is hours and minutes of a day of 24 hours. The length after
   `every` is written as a transition's `after` is (ADR-0047, item
   9), in minutes or hours; it divides a day, so that the schedule
   fires at the same times every day, and is five minutes at the
   least. The words are kept in lower case. A variable may list
   several schedules.

2. **At each of those moments the variable is worked out again in
   every document of the class that is not in a final state, and kept
   where it comes out different**, as when a table changes (ADR-0091,
   items 2, 4 and 5): a document whose value comes out the same gets
   no version; a `write` right of `FALSE` on the variable in a state
   makes the value stand there; each document brought gets a commit
   of its own, with the rules of the class, and a command that saw
   the version before is refused with `stale_version`.

   **A commit that brings a document into a state where the variable
   follows works it out too**, as for a table (ADR-0091, item 9,
   added the same day on the owner's yes): a new bill says how many
   days it is late in its first version, and one that comes back
   from a state where the number stood has the number of that day in
   the commit that brings it back. The variable lists the schedule
   alone.

3. **The engine itself brings the value.** Nobody changed anything,
   so the commit has no actor: its event is of the kind `refresh`,
   its name is the schedule, `schedule:daily at 03:00`, and the
   document says nobody changed it last. Nobody's right is asked.

4. **A database has a time zone, and its schedules go by it.** It is
   named as the database of time zones names it, `Europe/Warsaw`. The
   program that opens the database gives it, in `Options.TimeZone`;
   the server reads it from its settings, as
   `"timezone": "Europe/Warsaw"`. The database keeps it, so it is
   given once; a later one replaces it, and the schedules that wait
   are then timed again. A database that was never given one is in
   UTC, as every database was.

   **`NOW()` and `TODAY()` stay in UTC.** The zone is for the moments
   of the schedules and for nothing else. So a schedule that fires
   between midnight and two in the morning in Warsaw still sees the
   day before in `TODAY()`; the manual says to work a value that
   counts days out at three.

5. **Each schedule of a class has a row of the outbox that waits for
   its next moment** (ADR-0044): action `follow`, its `rule` the
   schedule, its `due_at` the moment, no document and no principal.
   The worker runs it as it runs the row of a table (ADR-0091, item
   6), and the transaction that marks it done queues the row of the
   moment after. A class that is defined, and a database that is
   opened, give a schedule its row when it has none.

6. **A moment that was missed is not made up for.** A server that was
   down at three in the night works the values out once when it is
   started, and the next row waits for the next moment from then.

7. **The nights the clocks change.** A time such a night does not
   have, in the hour the clocks skip, fires when that hour would have
   ended. A time it has twice fires the first time.

## Questions for the owner

All three are answered; they are kept as they were asked.

1. **Who brings the value when a schedule fires** (item 3).
   **Answered on 2026-11-26: the engine itself.** The other way: a
   named service user, with rights like anybody's.
2. **How a schedule is written** (item 1). **Answered on 2026-11-26:
   in words**, and more of them than the three first proposed: he
   named "every 3rd Monday of a month", "daily at 3.00 am" and "repeat
   every X hours". The words of item 1 are mine, made for those; the
   other way was the five fields of a Unix timetable.
3. **By which clock a schedule goes** (item 4). **Answered on
   2026-11-26: the database's own time zone.** The other way: each
   schedule names its zone, `schedule:daily at 03:00 Europe/Warsaw`.

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **The zone is set where the program is set up, and by no
   command**: `Options.TimeZone` for a Go program, the member
   `timezone` of the server's settings. `fadodb init` does not take
   it, since it says what is fixed when a database is made, and this
   is not.
2. **The zone of the machine is not taken.** `"Local"` is refused: a
   database moved to another machine would change its nights.
3. **Both programs carry the database of time zones** (`time/tzdata`,
   0.45 MB), so that the image for containers, which has none, knows
   Warsaw. A Go program that embeds the engine uses its machine's, or
   imports it too; a database whose zone the machine does not know is
   not opened, and says why.
4. **`every` takes minutes or hours that divide a day**, and five
   minutes at the least. "Every third day" and "every second week"
   are not written; nor are working days and holidays, for which the
   engine has no calendar.
5. **`monthly on` takes a day from 1 to 28.** The 29th, 30th and 31st
   are not in every month: the end of a month is `last day`.
6. **Told no time, a schedule fires at midnight**; told no day, a
   weekly one on Monday and a monthly one on the first.
7. **Variables of a class that list the same schedule are brought in
   one commit.** One schedule of a class is one row.
8. **A schedule a class no longer has ends with the row that waited**:
   the row runs at its moment, brings nothing, and queues no other.
9. **A row that waits holds nothing back, and waits behind nothing.**
   The outbox ran the rows of one document one after another, and all
   follows of a class name no document: a schedule that waits a month
   would have held back every table the class follows. A follow now
   stands outside that order, as a wake-up does. Two follows of one
   class may so take turns, a thousand documents each; each brings
   what differs at that moment, and the values end as the formulas
   give them. It replaces decision 10 of ADR-0091.
10. **The trigger does not fire when a class is defined**, as for a
    table: a new variable on a schedule is brought at its first
    moment.
11. **Refused when a class is defined**: words the engine does not
    read, with `bad_trigger` and how a schedule is written; and, as
    not built, a schedule on a list that is kept, with
    `not_supported`.

## What is built

- The words of a schedule, and the moment after a moment in a zone.
- The trigger in the class format, with its checks.
- The time zone of a database: `Options.TimeZone`, `DB.TimeZone()`,
  the member `timezone` of the server's settings.
- The rows of the schedules in the outbox, and their run.
- The value worked out by a commit that brings its document into a
  state where it follows.

The stored shape of a database does not change: the zone is a line of
its metadata, and the engine schema stays 15.

## Consequences

- A value that counts days is right every morning, in every open
  document, without a program that refreshes each.
- The queue shows when each schedule runs next.
- A schedule looks at every open document of its class each time. On
  a large class a short `every` costs a look over the class every few
  minutes: 250 ms for 500,000 documents when nothing is to bring
  (ADR-0091, Performance).
- A database that is closed runs no schedule. Whoever embeds the
  engine with the worker switched off runs it with `Drain`.
- The day in a formula and the night of a schedule are on two clocks:
  see item 4.

## What this record leaves out

- **`TODAY()` and `NOW()` by the database's time zone.** It would
  change what every formula of a database means, in the three
  evaluators at once; a record of its own.
- **A zone for one schedule**, the other way of question 3.
- **Working days, holidays, "every second week".**
- **A rule on a schedule** (ADR-0043): a rule that fires at a time
  without a value to bring. Not built.
- **A value that follows the documents of another class**, and a list
  that is kept on a trigger of this kind: as ADR-0091 leaves them.

## Performance

A schedule runs the worker of ADR-0091, and costs what a table that
brings a value costs: 48 millionths of a second for each document
brought, 250 ms for a look over 500,000 documents that brings none.
The commit of a document took no new step as this was first built.
Since the addition to item 2, a create and a transition ask each
triggered variable of the class one thing more, whether it follows in
the state entered. The full gate of that change, on 2026-11-26, moved
no benchmark: a transition takes 56 millionths of a second before and
after, with the same bytes and the same allocations.
