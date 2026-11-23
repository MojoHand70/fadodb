# ADR-0080: A column added to a table in use

- Status: Proposed; amends ADR-0037 (a table with rows, or one a class reads, is redefined when the new version only adds columns)
- Date: 2026-11-23

## Context

Until now a table took a changed definition only while it held no rows
and no class read it (ADR-0037, the manual, chapter Tables). So a table
that people had begun to fill could not gain a column, however
harmless: the engine's advice was to define the new shape under another
name and import every row again.

The owner, 2026-11-23, with a table of four rows in front of him: "I
want to add a column. Adding a column should not be a problem."

The case is Acme. Dan keeps the table Rates: sixty rows, and the class
Expense looks its rate up in it. The treasury wants to note where each
rate came from, so Dan adds the column `source`.

## Decision

1. **A version that adds columns and changes nothing else is taken
   whatever the table holds and whoever reads it.** Every column the
   table has stays as it is written; the key, the indexes, the rules,
   the binding and the words stay the same. The app may change in the
   same version (ADR-0079, item 4).

2. **The rows the table holds are blank in each new column.** An
   import that follows writes it as it writes any column.

3. **A new column is not part of the key.** The rows there would have
   no value in it, and a key column is never blank. Such a version is
   refused like any other change of a table in use.

4. **The new columns may stand anywhere among the others**; the old
   ones keep their order among themselves.

5. **What reads the table goes on reading it.** A lookup reads what the
   table holds after the version; the view that SQL reads has the new
   columns. Nothing else of the table is touched: its rows stay where
   they are, and the version is a few statements in one transaction.

6. **Any other change of a table in use is refused as before**, with
   the words that say what can change: columns added.

## The owner's words

Item 1 is his of 2026-11-23. Items 2 and 5 follow from it.

Item 3 was put to him the same day, after the pull request that built
it was merged: may a new column of a table that holds rows be part of
its key? The other way would have needed a value for every row the
table holds, given with the version: a default, which a column of a
table does not have today. His answer: "no". A new column of a table
in use is never in its key.

One is added for him to veto:

- **Item 6**, that a version which adds columns and also changes
  anything else (a label, a column's type) is refused whole. The other
  way: the columns are added and the rest refused. One version is one
  change of the definition, so it is taken whole or not at all.

## Consequences

- A designer adds a column to a table people fill, from a face or with
  `define_table`, without a copy and an import.
- The manual's chapter Tables says which changes of a table in use are
  taken.

## What this record leaves out

- **Changing or removing a column of a table that has rows**, with the
  two checks the owner asked for on 2026-11-23: whether each value
  converts, and whether a formula uses the column. A record of its own.
- **A change of a table's words while it is in use** (its label, its
  description, a column's label).
- **A new index on a table in use.**

## Performance

No hot path is touched. Whether a version only adds columns is asked
once, when the table is defined. A column is added with
`ALTER TABLE ... ADD COLUMN`, which in SQLite writes no row, so it costs
the same for sixty rows as for a million; the view is made again, and
the lookup plans with it, as after any version of a table.
