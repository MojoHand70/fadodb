# ADR-0079: A table belongs to an app

- Status: Proposed; amends ADR-0077 (a table names its app, as a class does), ADR-0016 (the table definition gains `app`), ADR-0055 (`list_tables` takes `app`)
- Date: 2026-11-23

## Context

ADR-0077 gave the database the app, a name for the classes that belong
together, and left out "the tables, schemas, actions and inlets that
belong to it". A table named no app. So a face that shows an app's
tables beside its classes could not say which tables those are, and a
designer who made a table for the expenses had nowhere to put it.

The owner, 2026-11-23, of the table editor he asked for that day:
"Table should belong to an App. Not Class." And, on what to build
next: "go ahead with the engine part, table belongs to an app".

The case is Acme. Dan, a designer, builds Expenses: the class Expense
and the table Rates, which the treasury keeps and Expense reads. Rates
was made before there were apps, holds sixty rows, and Expense looks
its rate up in it.

## Decision

1. **A table says which app it belongs to, in its own definition**, as
   a class does (ADR-0077, item 2):

   ```json
   {
     "name": "Rates",
     "app": "Expenses",
     "label": "Exchange rates",
     "columns": {"currency": "text", "day": "date", "rate": "decimal(4)"},
     "key": ["currency", "day"],
     "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
   }
   ```

   `get_table` answers it as it was defined, `app` with the rest.

2. **The app is there first.** A table that names an app the database
   does not have is refused with the finding `unknown_app` at `/app`,
   with the hint to define the app or to leave `app` out.

3. **A table belongs to one app, or to none.** A table without `app`
   is what every table was until now.

4. **A table moves to another app by its next version, and that
   change is taken whether the table has rows or a class reads it.**
   The new definition differs from the one the table has in `app`
   alone: its columns in their order, its key, its indexes, its rules
   and its words are the same. Nothing of its storage or its rows
   changes, and what reads it reads the same rows. Any other change of
   a table that has rows, or that a class reads, is refused as before
   (the manual, chapter Tables), with or without a new app beside it.

5. **`list_tables` takes `app`**, as `list_classes` does: the names of
   the tables of one app, in the order they were defined; an empty
   list for an app that has none; without `app`, every table as
   before. A name that is no app is refused with `unknown_app`, 404
   over HTTP. In Go: `DB.TablesOf`.

6. **Names stay the database's** (ADR-0077, item 7). A table is
   `Rates` in every formula, view and command, whichever app it
   belongs to; a class of one app reads a table of another, as it
   reads one of none. An app decides nothing (ADR-0077, item 9).

## The owner's words

Item 1 is his of 2026-11-23. Items 2, 3, 5 and 6 are what ADR-0077,
which he accepted, says of a class, said of a table.

Item 4 was put to him the same day, after the pull request that built
it was merged: that a move to another app is taken while the table is
in use. Without it, Dan cannot put Rates into Expenses at all: it holds
rows and Expense reads it, so every new version of it is refused. The
other way was that such a move is refused like any change of a table
in use, and Dan defines a copy under another name in the app, moves
Expense over to it, and imports the sixty rows again. His ruling: "My
answer is yes, we can freely move a table between apps. Table name
must be unique on server."

The name was unique on the server already, and stays so whatever the
app (item 6): no two tables have names that differ only in the case
of letters, and a table may not have the name of a class (the manual,
chapter Tables).

## Consequences

- A face lists an app's tables with `list_tables` and `app`, as it
  lists its classes, and a table made from inside an app belongs to
  it.
- The table definition has one more word, and `list_tables` one more
  member. The storage schema is unchanged: the app is in the
  definition the database keeps.
- A database in which a table names an app is not opened by an engine
  before this record: that engine does not know the word, and refuses
  the stored definition.
- ADR-0077, ADR-0016 and ADR-0055 are marked as amended by this
  record.

## What this record leaves out

- **The app System**, which every database is to have, and the system
  tables in it. The owner's words of 2026-11-23: "I think Engine by
  default should have one App called System. This is where system
  tables belong to." A record of its own, with `hidden` on an app,
  his words of 2026-11-22: "This marks an app as technical and app is
  never shown to people only to designers."
- **Changing a table that has rows**, its values checked and the
  formulas that use a column, which the owner asked for the same day:
  a record of its own.
- **A change of a table's words while it is in use** (its label, its
  description, a column's label): refused as any other change, as
  before.
- **The engine's own class Substitution** belongs to no app (ADR-0077,
  item 4). Whether it moves into System is a question for later.
- **Schemas, actions and inlets** of an app.

## Performance

No hot path is touched. Whether a table's app is there is asked once,
when the table is defined. `list_tables` with `app` walks the tables
once. A move to another app writes the definition and nothing else;
the plans of the lookups are made again, as after any version of a
table.
