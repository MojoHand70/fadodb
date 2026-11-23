# ADR-0082: Removing rows and removing a table

- Status: Proposed; amends ADR-0037 (a table can be removed, and its rows one by one), ADR-0055 (two commands join the table)
- Date: 2026-11-23

## Context

Until now nothing took a row or a table away. An import replaced a row
whole, or emptied the table and filled it again; a rule of a class
removed one row; no call removed a table. So rows typed by mistake
stayed, and so did tables made to try something.

The owner, 2026-11-23, with test rows and the tables "Test" and "Test2"
on his demo, asked what comes next and chose: "Removing rows and
tables. I think we will need a context menu here too." The context menu
is the face's; the engine gives it the two calls.

The case is Acme. Dan typed two rows of Rates with the wrong day, and
made a table Scratch to try an import. Expense reads Rates.

## Decision

1. **`delete_rows` removes the rows of a table that have the keys
   given**, in one transaction:

   ```json
   {"table": "Rates", "keys": [{"currency": "EUR", "day": "2026-11-31"}, {"currency": "USD", "day": "2026-11-31"}]}
   ```

   It answers how many rows it removed. Each key is a value for every
   column of the table's key, and for no other; a key that is not whole,
   or names another column, refuses the call and nothing is removed. A
   key the table does not have removes nothing, as a rule's
   `table.delete` of a row that is not there. It asks the table's
   `write` line, as an import does, and a content administrator's call
   past it is written in the log of reads, as an import past it is. The
   rows in memory are dropped, as after an import. The command is for
   whoever may write the table; in Go, `DB.DeleteRows`.

2. **`remove_table` takes a table away** with its rows, its storage, its
   view for SQL and its words. It is for the system group `designers`,
   as defining a table is. It is refused with `not_allowed`, naming the
   class, while a formula of a class reads the table or a rule of a
   class writes it: the class is changed first. In Go,
   `DB.RemoveTable`.

3. **The versions of a removed table stay** in the log of definitions.
   A table defined again under its name takes the next version after
   them, and starts empty; its name is free again for a class too.

4. **Nothing records who removed it.** The log of definitions has a line
   for each version and none for a removal, as for words taken away
   with `remove_words`; and the rows of a table have no log, as an
   import has none. The storage of the engine is not changed by this
   record, so a database of today's schema opens as it is.

## The owner's words

The two calls are his of 2026-11-23. Items 3 and 4 are added for him
to veto:

- **Item 3**, that a removed table's name is free again and its
  versions go on. The other way: the name stays taken for good.
- **Item 4**, that nothing records who removed a table. The other way:
  a line for each removal, in the log of definitions or a log of its
  own; that changes the engine's storage, and a database made before
  it is made again.

## Consequences

- A face removes rows and tables, from a context menu or any other
  way, through the two calls.
- An agent removes a row it wrote by mistake with the same call a
  person's face uses.
- The manual's chapter Tables says how rows and tables are removed and
  who may, and plays it.

## What this record leaves out

- **A record of who removed a table** (item 4).
- **Removing a class, a schema, an action, an app.** Each a record of
  its own, with what becomes of what refers to it.
- **Removing rows by a condition** ("every rate before 2026"): the keys
  are named one by one.

## Performance

No hot path is touched. A row is removed by its key, through the
table's primary key; a table is removed with its storage in one
transaction.
