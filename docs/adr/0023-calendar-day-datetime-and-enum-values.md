# ADR-0023: A date is a calendar day, a datetime an instant, an enum a text with a value set

- Status: Proposed; amends ADR-0013, ADR-0018, ADR-0019
- Date: 2026-11-06

## Context

ADR-0013 made `date` an int64 count of seconds in UTC, one representation
for a day and an instant, differing in display only. Business documents
carry days: an invoice is issued on a day, a deadline is a day, a payment
is due on a day. A day stored as a midnight in UTC becomes the day before
in Warsaw at half past midnight, and every embedder that keeps books
has met that defect. The first embedder's rules are effective from a day
and its periods are chosen by a day; it refuses to compute with instants
where a day is meant.

Status and kind fields take one of a few values. ADR-0013 stores them as
text and nothing refuses a value outside the set, so every client checks
and none agrees.

## Decision

- `date` is a calendar day: a count of days since 1970-01-01, year 1 to
  year 9999, with no clock and no zone. `datetime` is an instant: seconds
  since 1970-01-01 00:00:00 UTC, the type `date` was. The system variables
  `CREATED_AT`, `UPDATED_AT` and `STATE_ENTERED_AT` are datetimes; the
  business date of ADR-0026 is a date.
- `DATE(y, m, d)`, `DATEVALUE(text)` and `TODAY()` give dates; `DATEVALUE`
  reads `YYYY-MM-DD` only. `DATETIMEVALUE(text)` reads `YYYY-MM-DD HH:MM:SS`
  or `YYYY-MM-DD` as UTC and `NOW()` gives the instant. `DATEOF(datetime)`
  is the instant's UTC day and `DATETIME(date)` the day's midnight UTC; a
  date and a datetime never compare or combine without one of them.
- `YEAR`, `MONTH`, `DAY` and `QUARTER` take either. `DAYS`, `DAYSBETWEEN`,
  `MONTHSBETWEEN` and `YEARSBETWEEN` take two of one kind. `ADDDAYS`,
  `ADDMONTHS` and `ADDYEARS` keep their argument's kind: whole days on a
  date, fractions moving the clock on a datetime.
- `TEXT(date)` is `YYYY-MM-DD` and `TEXT(datetime)` is always
  `YYYY-MM-DD HH:MM:SS`; the midnight special case of ADR-0019 goes. On the
  wire a date is `"YYYY-MM-DD"` and a datetime integer seconds, as before.
  A date column is an integer of days; the analytical projection exposes it
  as a DATE.
- An enum is not a type. A `text` variable may declare `values`, a list of
  texts; a commit that sets a value outside the list is refused, the list
  travels in the bundle so that a layout renders a choice, and `STATE` is
  such a variable whose values are the class's states. Adding a value is an
  in-place class change; removing one is a migration under ADR-0014.
  Formulas see the variable as text.

## Consequences

- A browser in Warsaw and a server in UTC agree on an invoice's day,
  because the day never passes through a zone. An instant still does, and
  `DATEOF` says in the formula that a UTC day is meant.
- The conformance suite splits: the `date` suite holds the calendar cases
  of the predecessor engine and the `datetime` suite the ones with a clock.
  The binder refuses a date beside a datetime with a hint, which is the
  predecessor engine's silent seconds arithmetic made visible.
- The rename is a break for a formula written against v0.1.0 that put a
  clock into a `date`; nothing stores such a formula yet.
- Enum values need no evaluator support, so the three evaluators stay as
  they are. The check is the store's at commit time.

## Performance

A day is an integer like the seconds were; the VM, the SQL dialects and the
encoder treat it alike and no budget moves. `DATEOF` and `DATETIME` are a
division and a multiplication. The enum check is a membership test per set
variable at commit, inside the 2 ms commit budget of ADR-0006.
