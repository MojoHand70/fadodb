# ADR-0104: The Postgres store: one store, and Postgres under it

- Status: Proposed; amends ADR-0035 (item 4)
- Date: 2026-11-28

## Context

Phase 10 is the Postgres store. On 2026-11-28 the owner opened it and
set its order: "log first, then the Postgres store". The seal of the log
is on main (ADR-0103). This record is the plan of the store, and its
first step is built with it: formulas on Postgres.

ADR-0035, item 4, said the store is written "with the dialect of
ADR-0019 and the `Target`-shaped seam the benchmark used, so that
Postgres (Phase 10) is a second implementation and not a rewrite". Read
on main at 615563e:

- **The seam is there for formulas.** `formula.Dialect` spells the SQL
  of a formula for one engine, and SQLite and DuckDB each have one.
- **It is not there for the store.** `internal/store` is 36,084 lines
  in 78 files, without its tests. 45 of the files speak SQL, in about
  490 statements, and every statement is written for SQLite. `Target`
  lives in `bench/` only.

Most of the 490 statements Postgres reads as they are. What differs,
counted:

| What SQLite does | Where | On Postgres |
|---|---|---|
| A parameter is written `?` | every statement | `$1`, `$2` |
| A column takes any kind of value | 44 engine tables and the tables of every class | every column has one type |
| Every row has a number, and the log is read in its order | 43 places | a counter column of the engine's own |
| A name is compared without regard to case (`NOCASE`) | 19 places | an index on the name in small letters |
| `INSERT OR REPLACE`, `INSERT OR IGNORE` | 8 places | `ON CONFLICT` |
| Its own functions over JSON | 16 places | Postgres's own |
| A trigger keeps the history of a table's rows | 6 | the same trigger in PL/pgSQL |
| Its settings and the copier of its log | 10 places | not needed |
| The engine's functions are Go code SQLite calls | 31 functions | SQL functions, as for DuckDB |
| The read-only SQL endpoint is guarded by SQLite's authorizer | 1 place | a Postgres role that may read the views and nothing else |
| A database is one file | opening it, holding it alone, the files beside it | a schema of a Postgres database |

Measured on 2026-11-28 with the benchmark of ADR-0035, the write step
of a commit alone, 100,000 commits over 20,000 documents, on an Intel
Core Ultra 9 275HX: Postgres 17.11 in a container on the same machine,
reached over TCP, where the smallest prepared statement takes 43 µs
there and back; SQLite on the disk in the same hour, with its
checkpoints in the background, as the store runs it.

| | SQLite | Postgres, a trip for each statement | Postgres, a commit in one trip |
|---|---|---|---|
| Commits a second, one transaction each | 8,067 | 1,522 | 6,110 |
| p50 of a commit | 0.078 ms | 0.648 ms | 0.153 ms |
| p99 of a commit | 0.256 ms | 1.17 ms | 0.364 ms |
| Commits a second, 1,000 to a transaction | 35,236 | 1,786 | not measured |
| Commits a second, each on disk before it is confirmed | 381 | 482 | 528 |

The fast rows are `synchronous=NORMAL` on SQLite and
`synchronous_commit=off` on Postgres; the last row is `FULL` and `on`.
On Postgres the cost of a commit is the trips to the server, about
twelve for the twelve rows a commit writes, and a transaction of a
thousand commits does not take it away. Sent together and answered
together, the same statements take a quarter of the time.

## Decision

### His to rule

1. **On Postgres a commit is on disk before it is confirmed, unless
   the operator says otherwise.** It is Postgres's own default, and
   what Postgres is chosen for. The fast way is an option, as `FULL` is
   on SQLite. The other way is fast by default, as on SQLite: a server
   that stops suddenly then loses up to a second of commits. Open.

Item 3 of ADR-0103, one writer for each database on Postgres, is his
and open too. The store below is built on it.

### Mine, for him to strike or change

2. **One store.** `internal/store` is the store on Postgres too; it is
   not written a second time. What differs is asked of a small part,
   the database under the store: the type of a column, the number of a
   row, insert-or-replace, a name without case, JSON, a trigger, how a
   write begins and how a commit is sent. SQLite answers as it does
   today, to the letter, and the benchmark gate holds it to today's
   allocations and time. Planned.
3. **The spelling of a parameter is changed in one place.** The module
   `postgres/` reaches Postgres through a driver of its own over pgx.
   It turns `?` and `@name` into `$1`, `$2` in the order they first
   stand, binds a name used twice once, and leaves out a named value
   the statement does not use. A statement's text is the same for both
   stores wherever the SQL is. Built here.
4. **Formulas on Postgres are the third dialect of the SQL generator**
   (ADR-0003, ADR-0019), `formula.Postgres`. Built here; the next
   section says how.
5. **Postgres 17 or later.** It is the first that maps letters to
   capitals the same on every machine without a library of the system
   (`pg_c_utf8`). The module is tested on 17, 18 and 19 on GitHub.
6. **A FadoDB database is one schema of a Postgres database.** The
   address names the database and the schema. One customer is one
   schema or one database, as the operator likes (ADR-0056). Planned.
7. **A commit goes to Postgres in one trip**, by the measurement above.
   Planned.
8. **Every test of the store runs on both stores**, on GitHub too, and
   so do the suites of `conformance/`. Planned.
9. **The driver is pgx, in the module `postgres/`** (ADR-0002). The
   core module stays the standard library and SQLite, and no program
   links pgx yet.

### Formulas on Postgres

10. **The types.** A number is `float8`, and so are the days of a
    date and the seconds of a datetime, as in DuckDB. A bool is
    `boolean`. An exact value is its `bigint` mantissa; exact
    arithmetic runs in `numeric`, which no operation on 64-bit
    operands overflows, and the result is brought back to `bigint`,
    NULL where it does not fit.
11. **No statement fails where the VM gives an error code.** Postgres
    fails a statement where SQLite and DuckDB give an infinity, a NULL
    or a NaN: a float that leaves its range in either direction, a
    division by zero, a root or a logarithm of what has none, a text
    that is no number, no date or no JSON. Every such operation is
    written through a function that cannot fail. Its value is an
    infinity, a NaN or NULL, and the error column carries the code, as
    in the other dialects. `+`, `-`, `*` and `/` of numbers are
    `fado_add`, `fado_mul` and `fado_div`: the operator itself where
    the operands are far from the ends of the range, and an exact
    answer by scaling with a power of two where they are not.
12. **The functions are SQL functions of one expression** wherever
    that can be, which the planner writes into the statement that
    calls them. They are named as the other dialects name theirs,
    `fado_<name>` and `fado_<name>_err`, and `Setup` gives the
    statements that make them in the schema of the database. Two are
    PL/pgSQL, because they must catch what Postgres raises: the one
    that replaces by a pattern, and the one that reads JSON.
13. **A guard beside a risky reading is not enough.** The planner
    reads a constant as it plans, before any guard is asked, and from
    Postgres 18 on it plans a function's statement with its arguments
    as constants. Postgres 17 passed every test with JSON read behind
    a guard; 18 and 19 failed the statement. So JSON is read by a
    function that catches the failure. The tests on 18 and 19 are what
    found it.
14. **Text is ordered by its code points**, as the VM orders it,
    whatever the database was created with: a comparison and a sort
    say `COLLATE "C"`, and a table's text columns are declared so, so
    that an index serves them. The case of a letter is changed by
    `pg_c_utf8`, Unicode's simple rule, which Go follows. The suite
    runs on a database created with `en_US.utf8` too.
15. **`TEXT` of a number** is C's `%.15g`, as everywhere: Postgres's
    own printing with `extra_float_digits` at 0, set on the function.
16. **A parameter is written `@name`**, and where the statement gives
    it no type, with its type. The groups of the user are bound as an
    array of text, the states of a document's life as `jsonb`.
17. **Six optional interfaces in the generator.** A dialect implements
    one where its engine needs what the others do not: the spelling
    of arithmetic on numbers, the ordering of text, the type of a
    parameter, the type of a blank beside a value in `IF` and
    `IFERROR`, the value of the one row of `FIRST`, `LAST` and `INDEX`
    (Postgres has no `MIN` of a boolean), and what a table declares
    for a column. SQLite and DuckDB implement none.
18. **Known differences from the VM**, listed in
    `conformance/README.md`. The first three are each held by a test
    that fails when the difference goes away; the last two are let by
    where the tests compare with Go:
    - `REGEXREPLACE` runs Postgres's patterns, not RE2. The longest
      alternative wins (`a|ab` on "ab" takes "ab"); `(?P<n>…)`, `\pL`
      and `\x{41}` are refused; back references and look-ahead are
      taken; `.` matches a new line.
    - `PROPER` treats as a letter what Unicode calls alphabetic, which
      is more than Go's letters: "Ⅳa" stays "Ⅳa", and is "ⅣA" in the
      VM.
    - `JSONVALUE` refuses JSON that writes the character of number
      zero, which no text of Postgres holds, and a number with an
      exponent past what `numeric` holds.
    - A product or a quotient that lies within one part in 2^53 above
      the half of the smallest number is zero, and the smallest number
      in the VM.
    - `LN`, `LOG` and `^` of a number smaller than 2.2e-308: Go's
      logarithm is wrong there (`math.Log(5e-324)` gives -709.09 for
      -744.44), and Postgres is right.
19. **A fault of the VM, found on the way and not corrected here.**
    `ROUND` of an exact value that drops 19 digits, as a value of 18
    places rounded to tens: 6.3 gives 0 in the VM and on SQLite, and
    10 on Postgres and on DuckDB. 10 is right. It is on main since
    exact numbers were built, and DuckDB has differed from the VM
    there all along.

What holds it: the 1,691 evaluation cases of `conformance/formula`, on
Postgres 17, 18 and 19 on GitHub; 2,000 generated formulas on each, and
campaigns of 100,000 by hand; 126,046 pairs of numbers through `+`,
`-`, `*` and `/` against Go, bit for bit; 22,209 through the power and
the exponent; 38,950 formulas over hostile texts and numbers. In none
did a statement fail.

## Consequences

- **A feature of the store is built once**, and tested on both stores
  the day it is built. The cost is the discipline of the seam: a
  statement that only SQLite reads fails a test on Postgres.
- **Phase 10 is larger than the three weeks the plan gave it.** The
  steps after this one: the seam in the store; a database that opens
  on Postgres, with classes, documents, commits, history, the seal and
  one writer; the rest of the store; the programs; the measurements of
  each store.
- **Postgres is slower than SQLite for a commit and always will be**:
  SQLite is in the program, and Postgres is a trip away. In one trip
  it is twice SQLite's time, inside every budget of ADR-0012.
- **Nothing changes for SQLite**, for the programs, or for a database
  that exists.

## Performance

No path the store runs for SQLite changes. The hooks this step adds to
the SQL generator are asked when a formula is translated, never when
it runs.

Measured for this step:

| | Before | After |
|---|---|---|
| The SQL generated for SQLite and for DuckDB, all 1,691 cases, 3,382 statements | | the same, byte for byte |
| Allocations to generate SQLite's SQL for all the cases | 35,517 | 35,517 |
| `fadodb-core` | 18,227,362 bytes | 18,235,554 bytes |

The speed of formulas on Postgres is not measured in this step. A
deeply nested expression may not be written into the statement by the
planner, and then runs as calls of SQL functions. It is measured with
the store, where a filter and a rule run for real, and published for
each store.
