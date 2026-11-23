# ADR-0084: Changing a table that holds rows

- Status: Proposed; amends ADR-0037 (a table in use is redefined), ADR-0080 (item 6: a version that adds columns and changes anything else is no longer refused whole)
- Date: 2026-11-23

## Context

A table that holds rows, or that a class reads, took a new version only
to gain columns (ADR-0080) or to move to another app (ADR-0079). Any
other change was refused, and the engine's advice was a table under
another name and every row imported again.

The owner, 2026-11-23, of the table editor he asked for: "Changing types
and removing columns should check two things 1. is data present in the
table and there is a way to convert the data and 2. is any formula using
this column." And, the same evening: "go ahead with piece 5".

The case is Acme. Dan keeps the table CostCentres: sixty rows, and the
class Expense looks up the name of a cost centre in it. The column
`budget` was made as text and holds figures; Dan wants it a decimal. The
column `region` nobody uses any more; Dan removes it. Then he tries to
remove `name`, which Expense reads.

## Decision

1. **A version of a table that holds rows, or that a class reads, is
   taken when it passes the owner's two checks** and keeps its key: the
   same columns, of the same types. Its words, its rules, its indexes
   and its app may change in the same version, and columns may be added.

2. **Check 1: every value of a changed column converts to its new
   type.** A value converts as an import of it into the new column would
   take it: exactly, or not at all. Text that is a whole number becomes
   one; a decimal gains places; a figure, a day, a moment or a yes or no
   becomes text; the text `true` or `false` becomes a yes or no; text in
   the form of a day or a moment becomes one; anything becomes JSON. A
   decimal does not lose a place, an amount does not lose its currency,
   a moment does not lose its time of day. A value that does not convert
   refuses the version with `bad_value` at the column, saying how many
   rows do not convert and naming the first by its key, with its value:
   `1 row does not convert to decimal(1): ... the row CC-210 holds "2.55"`.

3. **Check 2: no formula and no rule of a class uses a column it
   changes or removes.** The engine compiles each class that reads the
   table or writes it against the table without those columns: what the
   class then finds missing, it uses. A use refuses the version with
   `not_allowed`, naming the class and the place: `the class Expense uses
   a column this version changes or removes, at /variables/centre_name`.
   The class is changed first. A column that a formula uses keeps its
   type even where the formula would still work with the new one.

4. **The rows are carried over** in the same transaction as the
   version: each as the new version takes it. A removed column takes its
   values with it; a new column is blank in them.

5. **The key does not change** while the table holds rows or a class
   reads it: refused with `not_supported`, and the advice of before
   stands, a table under another name.

## The owner's words

Items 2 and 3 are his two checks of 2026-11-23. Three things are added
for him to veto:

- **That a removed column takes its values without a check.** The
  values are present, and they go. The other way: the engine refuses
  to remove a column that holds values until they are blanked. A face
  says before the removal how many rows hold one, and asks.
- **That a column a formula uses keeps even a type that would still
  work** (item 3). Dan cannot widen `rate` from four places to six
  while Expense reads it. The other way: the engine recompiles the
  class against the new type, and refuses only what breaks. Put to
  him on 2026-11-23, after this was built; his ruling: "no, keep it as
  built". A column a formula or a rule uses keeps its type exactly.
- **That the key does not change** (item 5). The other way: a new key
  is taken where the rows are still told apart by it.

## Consequences

- A designer changes a column's type and removes a column of a table
  people fill, from a face or with `define_table`, without a copy and an
  import; what cannot be done is refused with the row or the class
  named.
- ADR-0080's item 6 no longer holds: a version that adds columns and
  changes anything else is taken when it passes the two checks.
- The manual's chapter Tables says which versions of a table in use are
  taken, and plays them.

## What this record leaves out

- **Renaming a column that holds values**: a column removed and
  another added do not carry its values.
- **A new key** for a table in use (item 5).
- **Converting a value the import does not take** (an amount to a
  figure, dropping its currency; a moment to a day, dropping its time):
  refused, not converted.

## Performance

No hot path is touched. A version of a table in use that changes
columns reads every row once, converts it, and writes it again into the
table made anew, in one transaction; it costs a write of the table. The
two checks compile each class that reads or writes the table twice.
