# ADR-0094: The rows of a table stay in the store, and the copy holds them too

- Status: Proposed; amends ADR-0016 (the storage bindings `duckdb` and `external` are not built: every table's rows live in the store), ADR-0017 (the program with the analytical engine does not bind tables to DuckDB, and attaches no other database), ADR-0036 (a lookup of a live formula that is keyed by an indexed column is narrowed to its key, as the lookup of a commit is), ADR-0089 (the copy holds the rows of the tables too, and answers the questions a class asks of them); amended by ADR-0099 (the token of a table's rows is the id of the change that last wrote them, written before the rows)
- Date: 2026-11-26

## Context

A table is reference data that formulas look into: rates, a price
list, stock. `LOOKUP` takes one value from it and `QUERY` a list. Its
rows live in the database file, in the store.

ADR-0016 names two more places the rows of a table could live, and
ADR-0017 makes the first of them the place a table has by default in
the program with the analytical engine:

| Written in a table | The rows would live |
|---|---|
| `"binding": "duckdb"` | in DuckDB, and nowhere else |
| `"binding": "external"` | in another database: anything DuckDB can attach |

Neither is built. Both are refused when a table is defined. They are
the last goal of Phase 8 that had no analysis, and they were written
before anything was measured.

Measured on 2026-11-26, on disk, three runs that agree: a table of
stock with 5,000,000 rows, a key `sku`, a `bin` with no index, a
quantity and a price.

| What | In the store | The same rows in DuckDB |
|---|---|---|
| bringing the 5,000,000 rows in | 12 seconds | 1.7 seconds |
| the size on disk | 0.27 GB | 0.04 GB |
| a commit that looks one row up by its key | 0.06 ms, the whole commit | 1.1 ms for the lookup alone |
| how many different bins | 360 ms | 1.7 ms |
| a page of the rows sorted by price | 395 ms | 6 ms |

- **The store is the right place for looking one row up.** A commit
  that takes a price from the table is done in 60 millionths of a
  second. DuckDB needs about twenty times the whole commit for that
  one lookup: it is quick over everything, and slow for one row.
- **DuckDB is the right place for the questions that read the whole
  table.**
- **Neither place is right for both.** It is the finding ADR-0089 made
  for the documents, and the answer is the same: the copy.

Two more things weigh against rows that live in DuckDB alone. A rule
writes a table inside its own transaction (ADR-0044), and rows in
DuckDB could not be written together with a commit of the store. And
`fadodb-core`, which has no DuckDB, could not read such a table at
all.

## Decision

1. **The rows of every table live in the store.** No table's rows live
   in DuckDB alone. `"binding": "duckdb"` is refused, as it was, with
   `not_supported`, and the refusal says that nothing needs writing:
   the program with the analytical engine keeps a copy by itself.

2. **The engine reads no table from another database.**
   `"binding": "external"` is refused, as it was. With it a commit
   would wait for a database that is not ours and fail when it is
   down, the password of that database would sit with the engine, and
   DuckDB's connector for it is not in the library the program is
   built with. The rows of such a table are brought in from outside,
   with `import_rows`, on a schedule; the values that follow the table
   are then brought by themselves (ADR-0091). The program that does it
   is FadoConnect (ADR-0085), and it is not part of Phase 8.

3. **The program with the analytical engine keeps the rows of every
   table in the copy** (ADR-0089), beside the documents. Nothing is
   written for it, and nobody chooses which tables. The system tables
   (ADR-0092) are not copied: their rows are the engine's own records,
   and are read where they are.

4. **A table has no log of its changes, so the store keeps a token
   for each.** Every transaction that changes the rows of a table
   writes the token anew, in that transaction: an import, a removal of
   rows, a rule's `table.insert`, `table.update` or `table.delete`,
   and a new definition of a table that holds rows. The copy keeps the
   token its rows were read under, with the version of the table's
   definition. Where the two differ, the copy of that table is made
   again, whole. `fadodb-core` writes the token too: the next program
   to open the database may keep a copy.

5. **A small table is copied by whichever feed finds it changed; a
   large one by the feed that runs by itself.** Small is up to 10,000
   rows: that is a moment, and a question that finds such a table
   changed copies it and is answered by the copy. A larger table takes
   seconds. No question waits for that: the question wakes the feed
   that runs by itself, and the store answers until the copy is made.

6. **The questions of a class over a table that the copy answers**,
   by itself:

   | The question | The copy answers it |
   |---|---|
   | a total, or the first row, that a `LOOKUP` in a live variable asks of a table: `LOOKUP(Stock, SUM(price), bin = THIS.bin)` | at once when the table is a large one and no index of it finds the rows; otherwise when the store has not answered in a moment |
   | a page of a live list over a table: `QUERY(Stock, {sku, price}, qty > 0, {-price})` | at once when the table is a large one and no index holds the order; otherwise when the store has not found the page in a moment |

   A large table, here, is one with more rows than the store keeps in
   memory, `MaxIndexedRows`, 100,000 unless the database says
   otherwise (ADR-0036). The moment is the one of ADR-0089, five
   milliseconds.

7. **An answer is never older than its question** (ADR-0089, item 5).
   Before a question over a table is answered, the tokens are
   compared: the copy answers only when it holds the rows as the
   database has them at that moment. Otherwise the store answers.

8. **The rows of a page are read from the store.** The copy says
   which rows the question takes, in its order, by their keys; the
   store hands over those rows by the key of the table, still under
   the question's own condition. A row changed since the copy looked
   is never shown where it no longer belongs.

9. **Who may read a table is asked as ever**, before either engine is
   asked: the one read line of the table (ADR-0051, item 7). A table
   has no rule for each row, so the statement the copy runs carries
   none.

10. **What stays with the store:** everything inside a commit, so the
    lookup of a computed or a triggered variable; a row found by its
    key; and the command `table_rows`.

11. **A lookup of a live formula that is keyed by an indexed column
    finds its rows through the index.** A `LOOKUP` whose condition
    compares the key of the table, or a column that leads an index of
    it, with a value of the document was narrowed to that key inside
    a commit only (ADR-0036). Read in a live variable, it read the
    table up to the row: 67 to 70 ms for a row a third of the way
    into 5,000,000. It is now narrowed in both, in both programs, and
    takes 0.02 ms.

12. **A table over the limit of the rows kept in memory stays known
    as such while rows are only added or changed.** After every
    change of a table the store forgot what it knew of its size, and
    the next lookup of a commit read the table up to the limit,
    100,000 rows, to find again that it holds more: about 70 ms, once
    after each change, so in every commit that follows a rule's
    `table.update` on a large table. An import that adds or replaces
    rows and a rule's insert or update cannot make a table smaller, so
    the store keeps what it knew. After a removal of rows, and after
    an import that empties the table first, it looks again. Both
    programs.

## Questions for the owner

Both are answered. They are kept as they were asked, with the other
way.

1. **Where the rows of a large table live** (items 1 and 3).
   **Answered on 2026-11-26:** "Your proposal is fine." The proposal:
   in the store, with a copy in DuckDB that the engine keeps. The
   other way: in DuckDB alone when the table says
   `"binding": "duckdb"`, which gives the smallest file and the
   fastest import, and takes a millisecond more in every commit that
   looks a price up, a table only `fadodb` reads, and one no rule can
   write.

2. **How a table from a customer's own database gets in** (item 2).
   **Answered on 2026-11-26:** "External client data sources we leave
   for implementation together with FadoConnect. Not in this phase."
   The other way: the engine reads that database itself at each
   lookup.

## Decisions of mine, each the owner's to strike

Settled while building, on 2026-11-26.

1. **The token is a line of the database's metadata**, `rows:` and
   the name of the table. The stored shape of a database does not
   change, and the engine schema stays 15.
2. **A table whose rows no transaction of this record has changed has
   no token**, and is copied under the mark of none. So a database of
   an earlier release needs nothing done to it.
3. **In the copy the rows stand under the name the store's own table
   has, `tbl_` and the name of the table, in lower case.** The class
   `Invoice` has the tables `Invoice`, `Invoice_versions` and
   `Invoice_moves` there, and a table could be named as one of them.
   DuckDB tells names apart by their letters alone, and a table may be
   defined again in another case.
4. **A large table rests after it was copied**: the feed that runs by
   itself leaves it alone for ten times as long as its copy took. A
   table that changes all the time would otherwise keep the engine
   copying it. A feed somebody asks for, `FeedCopy`, copies it at
   once. The feed of a database that closes, which has five seconds,
   begins no large table.
5. **While a large table is copied, the store answers every
   question**, as it does during a long load of documents (ADR-0089,
   item 5): one feed runs at a time, and no question waits for it.
6. **A table that is taken away leaves the copy** at the next feed:
   its rows and its mark.
7. **The two bindings stay words the engine knows and refuses**, with
   `not_supported`, and an unknown one stays `bad_value`.

## What is built

- The token of a table's rows, written by every change of them, in
  both programs.
- The rows of the tables in the copy, made again when the token or
  the definition differs, by the rules of items 4 and 5.
- A `LOOKUP` of a live variable over a table, and a page of a live
  list over a table, answered by the copy by the rules of item 6.
- The lookup of a live formula narrowed to its key (item 11), and
  the size of a large table remembered across a change (item 12).
  Both were found by measuring this record's build on five million
  rows. The first is a fault of this phase's own work, which no
  release had; the second is in every release since v0.2.0.

The module `analytics` did not change: the copy is given tables and
rows as it is for the documents.

## Consequences

- A class asks a large table for a total, a count or a sorted page
  and has the answer in milliseconds, with nothing written for it.
- Looking one row up stays what it was, and a rule writes a table as
  before.
- The rows are kept twice: 0.27 GB in the store and 0.04 GB in the
  copy for the table measured, where DuckDB alone would keep 0.04.
- Bringing rows in is no faster: about 400,000 a second.
- **A sum of plain numbers is no faster in the copy.** The engine adds
  numbers that are not exact, the type `number`, in the order of the
  key, so that every evaluator gives the same last digit (ADR-0046);
  DuckDB sorts the rows for that. A sum of prices or of money, which
  are exact, a count, a smallest and a largest are what the copy is
  quick at.
- A large table that changes all the time is the store's: its copy is
  made again no more often than item 4 of my decisions lets it, and
  between two copies the store answers.
- **A program older than this record writes no token.** When such a
  program changes a table of a database whose copy was already made,
  the copy of that table stays as it was until the table next changes
  under a program of this record, or until the copy's file is removed
  and made again. Nothing says so. It concerns a database that is
  opened by an earlier release after a later one.

## What this record leaves out

- **Totals by group over a table**: one line for each bin, with its
  sum. The store does not answer that either: a total among the
  columns of a `QUERY` is built over a class alone (ADR-0090). A new
  thing, not a faster one.
- **A change of one row copied as one row.** The engine that changes
  the table is the one that feeds the copy, so it could hand over the
  rows it changed. Not built: the table is copied whole.
- **A large table copied beside the feed**, into a table of its own
  that takes the place of the old one in a moment, so that the store
  does not answer every question meanwhile.
- **A list over a table whose condition names an indexed column**
  still reads the table in the store; in the program with the engine
  the copy answers it when the moment has passed.
- **A table too large for the store at all.** Hundreds of millions of
  rows are for a database of the customer's own.

## Performance

Measured on 2026-11-26 through the engine itself, on disk: the table
of the context, 5,000,000 rows. Each question was asked five times and
the middle one is given; three runs of each program, the same hour,
which agree.

| The question | `fadodb-core` | `fadodb` |
|---|---|---|
| a sum of prices over the whole table, in a live `LOOKUP` | 320 to 345 ms | 2.3 to 2.5 ms |
| how many rows are over a quantity | 220 to 250 ms | 1.8 to 2.7 ms |
| the dearest price of one bin, which has no index | 200 to 235 ms | 2.0 to 2.8 ms |
| how many different bins | 345 to 380 ms | 2.4 to 3.0 ms |
| a page of a live list: the fifty dearest rows | 395 to 435 ms | 7 to 10 ms |
| a page of a live list: the fifty largest of one bin | 245 to 265 ms | 6 to 7 ms |
| a sum of plain numbers over the whole table | 2.7 to 2.9 s | 2.6 to 2.9 s |
| a sum of plain numbers over one bin, 100,000 rows | 250 to 280 ms | 55 to 61 ms |
| one row by its key, in a live `LOOKUP` | 0.02 ms | 0.02 ms, from the store |
| a commit that looks one row up by its key | 0.06 ms | 0.06 ms |

What the copy costs:

| What | Measured |
|---|---|
| bringing the 5,000,000 rows in, beside the copy | 12.3 to 13.0 s; 12.3 to 13.2 s without it |
| the copy of the table made, and made again | 4.1 to 4.5 s |
| the copy's file | 0.10 to 0.12 GB, beside 0.27 GB of the database |
| the question after one row changed | 315 to 360 ms, from the store |
| until the copy answers again after that change | 32 to 35 s: the table had been copied a quarter of a minute before, and rested |

The token is one more line written in a transaction that changes a
table: the gate's benchmarks change no table, and none of them moved.
