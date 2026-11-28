# ADR-0096: How many documents were made and finished in each period

- Status: Proposed; amends ADR-0055 (one command, `throughput`, joins the table), ADR-0089 (the second of what it calls the mining views is built), ADR-0095 (its question to the owner is answered); amended by ADR-0099 (each number of `throughput` opens to the documents behind it, with `documents`)
- Date: 2026-11-26

## Context

The flow of a class (ADR-0063) says where documents are and how they
moved in a period as a whole. It does not say whether the pile grows:
whether more invoices are made in a week than are finished. The plan
of the first days names that among its mining views, as the documents
entering and leaving in each period, and ADR-0089 lists it as not
built.

ADR-0095 put the question to the owner, which of the questions about
how documents moved the engine answers in this phase. **Answered on
2026-11-26: "yes - all three."** The first, how the stays spread, is
ADR-0095. This record is the second. The third, the paths documents
took, has a record of its own.

A period of this kind is a day, a week or a month, and a day begins
at midnight somewhere. Since ADR-0093 a database has a time zone.

## Decision

1. **A new command, `throughput`**, in Go the call `Throughput`. It
   answers, for a class, how many documents were made and how many
   finished in each period:

   ```jsonc
   {"class": "Invoice", "by": "day"}
   ```

   ```jsonc
   {
     "throughput": {
       "class": "Invoice",
       "class_version": 1,
       "by": "day",
       "time_zone": "UTC",
       "created": 4,
       "finished": 3,
       "periods": [
         {"from": "2026-11-02", "created": 4, "finished": 2},
         {"from": "2026-11-03", "created": 0, "finished": 1}
       ]
     }
   }
   ```

2. **A document is made when it is created, and finished when it
   comes into a final state.** A class without states is refused, as
   for the flow: nothing of it is finished.

3. **A period is a day, a week or a month**, said by `by`, a day when
   left out. **A day begins at midnight by the clock of the database's
   time zone** (ADR-0093), a week on its Monday, a month on its first.
   `from` is the first day of the period, written as a day, and the
   answer names the zone.

4. **Every period from the first to the last is in the answer**, also
   one in which nothing was made and nothing finished. Before the
   first period that holds a document and after the last there is
   none.

5. **`since` and `until` say what is counted**, as for the flow: from
   the first moment, and before the last.

6. **Each caller sees their own.** The documents counted are those the
   caller may read, by the read rule of the class, as in the flow. A
   deleted document is in no number. A content administrator who asks
   past the rule gives a reason, and the log of reads keeps the call.

7. **Both programs answer it, and the same.** The store counts the
   events of the class, the copy the moves it keeps ready (ADR-0089,
   item 2), each by the quarter of an hour. The engine then puts the
   quarters into the days, weeks and months of the database's zone
   itself: neither SQLite nor the statement written for the copy knows
   the zones and the nights their clocks change. Every time zone in
   use stands a whole number of quarters from UTC, so a quarter lies
   in one day.

8. **At most 10,000 periods are answered.** More is refused with
   `too_many_rows`: it is asked by a longer period, or over a shorter
   time.

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **A command of its own, and not a word of `flow`.** The page that
   put the question said so. A chart of made and finished is another
   picture than the flow, and whoever asks for one does not pay for
   the other.
2. **The name `throughput`**, the plan's word, and `created` and
   `finished` in the answer, `created` as the flow has it.
3. **A week begins on a Monday**, as a weekly schedule fires on a
   Monday when told no day (ADR-0093).
4. **The first day of a period is written as a day**, `2026-11-02`,
   and not as a moment: a day of the database's zone is no moment
   that every reader would read alike.
5. **The empty periods are listed**, so that a chart has no gaps, and
   only between the first and the last that hold something: a period
   asked for from the year 2000 does not answer nine thousand empty
   days.
6. **Finished is told as one number.** It is not told apart by the
   final state, approved or cancelled; the flow says how many moved
   along each transition.

## What is built

- The command `throughput` and the call `Throughput`, with
  `ThroughputAsk`, `Throughput` and `ThroughputPeriod`.
- Its statement for the store and for the copy.

## Consequences

- An agent or a chart sees whether more is made than finished, by day,
  week or month, with one command.
- What was made less what finished, added up from the first period,
  is how many are open: for the documents that exist now.
- A document that was finished and then deleted is in no period, as
  it is in no flow.

## What this record leaves out

- **The paths documents took.** Built since, by ADR-0097.
- **Finished by final state**, and the moves along each transition by
  period.
- **A zone of the caller's choosing.** The periods go by the
  database's.
- **A year, a quarter of a year, an hour.**

## Performance

Measured on 2026-11-26 through the engine, on disk: 3,400,000
invoices that made ten million moves in 36 days. Each was asked five
times of the program with the copy and three of the one without, and
the middle one is given; three runs with the copy, which agree.

| The throughput | `fadodb`, from the copy | `fadodb-core`, from the store |
|---|---|---|
| by day, by week and by month | 6 to 7 ms | 9 seconds |
