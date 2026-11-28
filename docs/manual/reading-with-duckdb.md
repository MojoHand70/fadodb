# Reading a database with DuckDB

An analyst has questions of their own and tools of their own: DuckDB,
and through it Python and R. A FadoDB database is a SQLite file, and
DuckDB reads SQLite files. So an analyst's own program can open a
database and ask it anything, with every function DuckDB has, and
write the answer to a file of its own.

This chapter says how to open a database from DuckDB, what is in it,
how a value looks there, and what to mind while a server has the
database open. A test runs every statement of this chapter with DuckDB
and compares the answers.

> **Whoever opens the file reads all of it.** No rule of a class is
> asked and no right, and nothing is written into
> [the log of reads](the-log-of-reads.md). The file holds every
> document with every version, and the engine's own records beside
> them: the users and their e-mail addresses, their groups, the
> records of who signed in, the keys of programs as their hashes.
> Give the file to somebody as you would give them everything. For
> one question, asked under a name and written down, there is the
> command `sql`, for the group `analysts`: see
> [Views and SQL](views-and-sql.md).

## The class and what happened

The class and the story are those of [Views and SQL](views-and-sql.md),
so that the answers can be laid side by side.

```json
{
  "name": "Invoice",
  "title": "\"Invoice \" & number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "supplier": {"type": "text", "required": true},
    "net":      {"type": "money", "scale": 2, "required": true},
    "vat_rate": {"type": "decimal", "scale": 2, "default": "0.23"},
    "gross":    {"formula": "ROUND(net * (1 + vat_rate), 2)"},
    "issued":   {"type": "date"},
    "lines":    {"type": "rows", "schema": {"what": "text", "cost": "money"}}
  },
  "states": {"initial": "INAPPROVAL", "list": ["INAPPROVAL", "INACCOUNTING", "BOOKED"], "final": ["BOOKED"]},
  "transitions": [
    {"name": "approve", "from": "INAPPROVAL",   "to": "INACCOUNTING"},
    {"name": "book",    "from": "INACCOUNTING", "to": "BOOKED"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "CREATEDBY() = USER()",
    "transition": {
      "approve": "CREATEDBY() = USER()",
      "book":    "INGROUP(\"ACCOUNTING\")"
    }
  }
}
```

Monday 16 November 2026, in UTC. Anna is in the group `ACCOUNTING`.

<!-- what happened -->
| At | Who | Does |
|---|---|---|
| 09:00 | Krzysztof | creates `I-1`: FV/1, ACME, 1000.00 PLN, issued 13 November, with two lines: a taxi for 40.00 PLN and a hotel for 300.00 PLN |
| 09:00 | Krzysztof | creates `I-2`: FV/2, Globex, 500.00 EUR, issued 13 November |
| 09:00 | Krzysztof | creates `I-3`: FV/3, Initech, 70.00 PLN, issued 13 November |
| 11:00 | Krzysztof | approves `I-1` |
| 11:00 | Krzysztof | changes the net amount of `I-2` to 600.00 EUR |
| 12:00 | Anna | books `I-1` |
| 12:00 | Krzysztof | deletes `I-3` |

The database is the file `books.fado`. Marek, an analyst, has DuckDB
and may read the file.

## Opening it

```sql
INSTALL sqlite;
LOAD sqlite;
ATTACH 'books.fado' AS books (TYPE sqlite, READ_ONLY);
```

- **`INSTALL sqlite` fetches DuckDB's extension for SQLite files**,
  once, from DuckDB's own site. It needs the internet that one time.
  `LOAD sqlite` is said in every session.
- **`READ_ONLY` holds DuckDB to reading.** A statement that would
  write is refused by DuckDB itself.
- **The database is then `books`**, and a view of it is
  `books.Invoice`.
- **DuckDB is run by a program of the analyst's own**: DuckDB's
  command line, a notebook, a script. It is never run inside the
  program that has the database open; see
  [While a server has it open](#while-a-server-has-it-open).

## What is in it

DuckDB shows the views the engine keeps for every class, which
[Views and SQL](views-and-sql.md#the-views-of-a-class) describes
column by column:

<!-- the views DuckDB sees -->
| View | It has one row for |
|---|---|
| `Invoice` | each document, as it is now |
| `Invoice_actions` | each action that a commit of a document queued |
| `Invoice_events` | each event of a document |
| `Invoice_history` | each version of each document |
| `Invoice_lines` | each row of `lines`, of each document as it is now |
| `Invoice_lines_history` | each row of `lines`, of each version |
| `Invoice_states` | each stay of a document in a state |

- **A table of reference data has one view**, under its own name, and
  so has each [system table](system-tables.md).
- **DuckDB shows the tables behind the views too**, `doc_Invoice` and
  the like, and the engine's own: `events`, `outbox`, `fado_users` and
  the rest. The command `sql` refuses them; DuckDB, which has the
  file, does not. They are the engine's to lay out, and a release may
  lay them out otherwise. The views are what a class promises: ask
  the views.

## How a value looks, and how DuckDB reads it

The file holds a value as the engine stores it, as
[Views and SQL](views-and-sql.md#how-a-value-looks-in-sql) lists type
by type. DuckDB turns each into a type of its own:

<!-- how DuckDB reads a value -->
| Type | In the file | To read it in DuckDB | It is then |
|---|---|---|---|
| `decimal` | `23` | `vat_rate * 0.01` | `0.23`, exact |
| `money` | `100000`, and `PLN` in `net_currency` | `net * 0.01` | `1000.00`, exact, with its currency beside it |
| `date` | `20770` | `DATE '1970-01-01' + CAST(issued AS INTEGER)` | `2026-11-13`, a date |
| a moment, such as `created_at` | `1794819600` | `to_timestamp(created_at) AT TIME ZONE 'UTC'` | `2026-11-16 09:00:00`, a timestamp |

- **An exact number is a whole number of its smallest unit.** With two
  places multiply by `0.01`, with four by `0.0001`: DuckDB then keeps
  it exact, as a decimal. Dividing by `100` gives a number that is
  not.
- **A true or a false is `1` or `0`**, a blank is `NULL`, and a text
  and a plain number are what they are.

## Five questions

**1. How many invoices are in each state now?** The statement is the
one the command `sql` takes, with `books.` before the view.

<!-- question -->
```sql
SELECT state, count(*) AS invoices FROM books.Invoice GROUP BY state ORDER BY state
```

<!-- answer -->
| `state` | `invoices` |
|---|---|
| BOOKED | 1 |
| INAPPROVAL | 1 |

**2. The invoices as a person reads them**, in DuckDB's own types.

<!-- question -->
```sql
SELECT number, net * 0.01 AS net, net_currency, vat_rate * 0.01 AS vat_rate,
       DATE '1970-01-01' + CAST(issued AS INTEGER) AS issued,
       to_timestamp(created_at) AT TIME ZONE 'UTC' AS created
FROM books.Invoice ORDER BY number
```

<!-- answer -->
| `number` | `net` | `net_currency` | `vat_rate` | `issued` | `created` |
|---|---|---|---|---|---|
| FV/1 | 1000.00 | PLN | 0.23 | 2026-11-13 | 2026-11-16 09:00:00 |
| FV/2 | 600.00 | EUR | 0.23 | 2026-11-13 | 2026-11-16 09:00:00 |

**3. The middle stay in each state.** DuckDB has functions SQLite does
not, a quantile among them.

<!-- question -->
```sql
SELECT state, count(id) AS stays, quantile_disc(duration, 0.5) AS middle_seconds
FROM books.Invoice_states WHERE left_at IS NOT NULL GROUP BY state ORDER BY state
```

<!-- answer -->
| `state` | `stays` | `middle_seconds` |
|---|---|---|
| INACCOUNTING | 1 | 3600 |
| INAPPROVAL | 1 | 7200 |

**4. What do the lines of each invoice add up to?**

<!-- question -->
```sql
SELECT i.number, count(l.idx) AS lines, sum(l.cost) * 0.01 AS cost
FROM books.Invoice i JOIN books.Invoice_lines l ON l.doc_id = i.id
GROUP BY i.number ORDER BY i.number
```

<!-- answer -->
| `number` | `lines` | `cost` |
|---|---|---|
| FV/1 | 2 | 340.00 |

**5. The invoices into a file of the analyst's own**, here Parquet,
which Python, R and every tool for data read.

<!-- question -->
```sql
COPY (SELECT number, net * 0.01 AS net, net_currency FROM books.Invoice)
TO 'invoices.parquet' (FORMAT parquet)
```

<!-- question -->
```sql
SELECT count(*) AS invoices, sum(net) AS net FROM 'invoices.parquet'
```

<!-- answer -->
| `invoices` | `net` |
|---|---|
| 2 | 1600.00 |

The sum adds złoty and euro, which no formula of the engine would:
DuckDB knows nothing of a currency. Group by `net_currency`.

## From Python and from R

Both have a package named `duckdb`, which runs the same statements.

```python
import duckdb

con = duckdb.connect()
con.sql("INSTALL sqlite")
con.sql("LOAD sqlite")
con.sql("ATTACH 'books.fado' AS books (TYPE sqlite, READ_ONLY)")
rows = con.sql("SELECT state, count(*) AS invoices FROM books.Invoice GROUP BY state").fetchall()
```

```r
library(duckdb)

con <- dbConnect(duckdb())
dbExecute(con, "INSTALL sqlite")
dbExecute(con, "LOAD sqlite")
dbExecute(con, "ATTACH 'books.fado' AS books (TYPE sqlite, READ_ONLY)")
rows <- dbGetQuery(con, "SELECT state, count(*) AS invoices FROM books.Invoice GROUP BY state")
```

The test of this chapter runs the statements through DuckDB's
library. It does not run these two programs: they are written from
the documentation of the two packages.

## While a server has it open

A server keeps its database open, and DuckDB may read it at the same
time, from its own program.

<!-- while a server has it open -->
| What | What happens |
|---|---|
| DuckDB reads while the server writes | it reads; each statement sees what was committed when it began |
| the server commits while a statement of DuckDB runs | it commits; a reader holds no writer back |
| a statement of DuckDB runs for minutes | the server cannot fold its latest commits into the database until it ends, and `books.fado-wal` grows meanwhile |
| DuckDB may not write in the directory of the database, and nobody has it open | it is refused, `attempt to write a readonly database` |
| DuckDB reads a database that nobody has open | it reads, and leaves two small files beside it, `books.fado-wal`, empty, and `books.fado-shm` |

- **For work that takes long, read a copy.** Make one as
  [Running it](running-it.md#what-a-copy-is) says, with the server
  running or stopped, and attach the copy.
- **DuckDB needs to write beside the file even to read it.** SQLite
  keeps two small files beside a database that is being read, and
  makes them when they are not there. An analyst who may read the
  file and may not write in its directory reads a copy.
- **The two small files are harmless.** They hold no commit. A
  program that opens the database after clears them when it closes.
- **Never inside the program that has the database open.** DuckDB
  brings its own SQLite, and two SQLites in one program that hold one
  file can lose each other's locks and damage it. A Go program that
  embeds the engine, and wants DuckDB over its documents, gives the
  engine DuckDB instead: see
  [An analytical engine](opening-a-database.md#an-analytical-engine).

## The copy that `fadodb` keeps

A database served by `fadodb` has a second file, `books.fado.duckdb`:
the copy the engine keeps for its own questions
([An analytical engine](opening-a-database.md#an-analytical-engine)).
It is a DuckDB file, and it is not for an analyst:

- **One program opens it**, and while the server runs that is the
  server.
- **It is never the truth.** The engine makes it again when it likes,
  and lays it out as it likes.

Read `books.fado`.

## Not here yet

- **A way for DuckDB to read only what its caller may see.** The file
  is all or nothing.
- **A record of what DuckDB read.** The log of reads holds what goes
  through the engine.
- **The programs for Python and R, run by a test.**
