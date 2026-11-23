# ADR-0083: Who removed a table or words, and when

- Status: Proposed; amends ADR-0082 (item 4: the log of definitions records who removed a table), ADR-0068 (item 14: words taken away record who took them away), ADR-0052 (item 7: a removal is in sight, as a definition is), ADR-0055 (the answer of `definitions` says who removed a version)
- Date: 2026-11-23

## Context

ADR-0082 gave `remove_table`. Its item 4 said that nothing records who
removed a table, as nothing records who took words away with
`remove_words`, and put the other way to the owner: a line for each
removal, in the log of definitions or a log of its own. It said that
either would change the engine's storage, so that a database made
before it would be made again.

The owner, 2026-11-23, asked whether the engine should record who
removed a table and when: "Yes, it should".

The case is Acme. Dan the designer made the table Scratch to try an
import, changed it once, gave it Polish words, and removes it. Dee, an
agent working for the lead, takes away the German words of Rates.

One condition holds the design: no database may need to be made again.
The engine schema is 13. A database of another schema is refused, and
before 1.0 there is no upgrade path, so a new engine schema would make
every database be made again, the owner's demo with his test data
among them.

## Decision

1. **A removal is a line of the log of definitions.** The log is the
   table `definitions`, one line for each version of a class, a
   schema, a table, an action, an inlet, an app and words: what it is,
   its name, the version, and who defined it and when (ADR-0052,
   item 7). A removal is one more line of the same five parts: what
   was removed with `removed` after it, `table removed` or `words
   removed`; its name; the version it had; and who removed it and
   when. It is written in the transaction that removes, so a removal
   is never without its line, nor a line without its removal.

2. **What is recorded** is what is recorded of a definition: the id
   of the caller, what it was, a person, an agent or a service, the id
   it acted for (ADR-0050), and the moment in seconds. `remove_table`
   writes a line for the table and one for each of its words in
   another language that was still in use, all with the same caller
   and the same moment. `remove_words` writes a line for the words it
   takes away. A removal that is refused writes nothing.

3. **`definitions` shows it on the version that was removed.** Each
   version is answered as before, with who defined it and when; the
   version a table or words had when they were removed also has
   `removed`, with `by`, `kind`, `for` and `at`:

   ```json
   {"what": "table", "name": "Scratch", "version": 2, "by": "dan", "kind": "person", "at": 1794996000,
    "removed": {"by": "dan", "kind": "person", "at": 1794999600}}
   ```

   A version nobody removed has no `removed`. Scratch defined again is
   its version 3, with no `removed`; removed again, version 3 has its
   own. Every caller may read it, as every caller may read who defined
   a version (ADR-0051, item 4). In Go, `Definition.Removed`. A
   removal is no kind of definition a caller names: `definitions` with
   `"what": "table removed"` is refused with `bad_value`, as before.

4. **The engine schema stays 13**, and every database opens as it is:

   - The table `definitions` is in every database of schema 13 (it
     came with schema 9), with the columns a removal needs. A removal
     adds lines to it: no table, no column, no name.
   - Every reader of the log asks for one kind of definition by its
     word, or passes over a kind it does not know. An engine made
     before this record, opening a file this one wrote, finds every
     version as before and none of the removals; this engine, opening
     a file of yesterday, finds no removal, since none was recorded.
     A file goes either way.
   - Item 4 of ADR-0082 was wrong on this point: a line in a table
     that is there already is no change of the storage.

5. **Why earlier records raised the schema for a new table, and this
   one does not need to.** Records disagree. ADR-0045 added the tables
   of the actions and said "the engine schema stays at 3: tables are
   added and none is changed"; ADR-0068, item 16, added the tables of
   the words under schema 12, "so a database of this schema opens with
   this engine and gains them"; ADR-0071, item 15, kept the e-mails in
   a table of their own for the same reason. ADR-0065, ADR-0067 and
   ADR-0077 raised the schema for a new table: ADR-0077 raised it to
   13 for the table `apps`, "as at every such step before 1.0", and
   gives no other reason. What a new table of the engine's own can
   break in a file that is already there is a class or a table of that
   file with the same name: SQLite keeps tables and views under one
   set of names, and `CREATE TABLE IF NOT EXISTS`, which the engine
   runs at every open, passes over a view of that name without a word.
   A raised schema keeps such a file out. This record adds no table,
   so the question does not arise.

## The owner's words

"Yes, it should", 2026-11-23, to whether the engine should record who
removed a table and when. What follows is added for him to veto, each
with its case, the proposal, and the other way:

- **Where it is kept.** Dan removes Scratch: the line is in the log
  of definitions, beside the versions of Scratch. The other way: a log
  of its own, a new table of the engine's. That too opens a file of
  schema 13, since every open makes the engine's tables that are
  missing; but it takes a name no class and no table may have
  afterwards, and a file whose class or table has that name already
  would not open, or would have to be made again.
- **Where it is read.** Ann asks `definitions` of Scratch and finds
  version 2 removed by Dan. The other way: a command of its own that
  lists every removal of the database, "what was removed this week",
  which answers without the name of what was removed. Today the
  caller must know the name.
- **The words go with the table, each with its line.** The Polish
  words of Scratch say, on their own version, that Dan removed them at
  the moment he removed the table. The other way: only the table's
  line; that its words went with it follows from ADR-0082, item 2.
- **Words taken away on their own are recorded too.** Dee takes away
  the German words of Rates: `definitions` of `table/Rates/de` says
  removed by `dee`, an agent, for the lead. The other way: only tables,
  as his words name, and words stay as ADR-0068 left them.
- **Who may read it.** Ann, who is no designer, reads who removed
  Scratch, as she reads who defined it. The other way: only designers
  and database administrators read who removed a thing, and then who
  defined it should follow the same line.

## Consequences

- A face shows, on the history of a table, who removed it and when,
  and an agent asks before it makes a table of a name that was used.
- An agent that removes a table or words is named in the log as a
  person is, with whom it acted for.
- `remove_words` now runs in a transaction of its own, since it
  writes two things.
- The manual's chapter Tables shows the answer of `definitions` for
  a removed table and plays it; the chapter on the class says that
  words taken away say who took them away.

## What this record leaves out

- **A list of every removal** (the second point for his veto).
- **Who removed an inlet.** `remove_inlet` closes an inlet's address
  and records nobody, as tables and words did before this record. The
  same line, `inlet removed`, would fit; it was not asked.
- **Who removed rows.** `delete_rows` writes no line: the rows of a
  table have no log, as an import has none (ADR-0082, item 4). A
  content administrator's delete past the table's rule is in the log
  of reads (ADR-0067).
- **Who removed a class, a schema, an action, an app.** None can be
  removed yet (ADR-0082).

## Performance

No hot path is touched. A removal writes one more line for each thing
it removes, in the transaction that removes it. `definitions` reads
the versions with one join of the log to itself, on its primary key.
