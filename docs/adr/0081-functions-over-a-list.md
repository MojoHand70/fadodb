# ADR-0081: Functions over a list, each with its SQL from the start

- Status: Proposed; amends ADR-0046 (item 4: `AVERAGE`, `MEDIAN` and `COUNTUNIQUE` stand in the column position of a `LOOKUP`). Items 1, 2 and 5 and the set of item 6 are the owner's word of 2026-11-23; the rest is settled by me for him to veto, and marked so. Step 1, step 2 and the first group of step 3 are built: see "What is built".
- Date: 2026-11-23

## Context

A document has lists. Some it holds: the lines of an invoice, a
variable of type `rows`, and its tags, an `array<text>`. One is worked
out when it is read and kept nowhere: a live variable whose formula is
a `QUERY`, the documents of another class (ADR-0076, item 9).

The language names eleven functions for lists: `COUNT`, `SUM`,
`AVERAGE`, `MIN`, `MAX`, `FIRST`, `LAST`, `INDEX`, `CONTAINS`, `JOIN`
and `FILTER`. Its catalogue says "COUNT of a rows value is its number
of rows". None of them worked on a list: the first five took single
values only, the other six were not built, and a formula that named a
list was refused with "composite values are not supported yet".

The owner asked what functions run over a query variable, "Like
COUNT(q), MAX(q,col)", and was told: none. One thing worked, and still
does: `LOOKUP(Invoice, COUNT(id), order = ID())`, with `COUNT`, `SUM`,
`MIN` or `MAX`, in a variable that is kept (ADR-0046).

Two ways to work a figure out of a query were put to him.

| | The function becomes the question | The rows are read, then counted |
|---|---|---|
| `SUM(invoices.net)` | one statement to the table of invoices | every row of the list is read, then added |
| A list of a million rows | the same one statement | a million rows for one number |
| A list read by pages (1,000 rows, 10,000 at most) | the whole list, always | the sum of one page: a wrong number, and no warning |
| Measured, 100 rows through an index among 10,000 | 55 µs | 340 µs to read the rows alone |
| The same among 1,000,000, on disk | 0.2 ms | 0.5 ms to read the rows alone |

The first is the engine's rule since ADR-0020: the VM never reads the
rows of a source, because the predecessor engine did and measured a
round trip per row at 10 to 50 times the cost of one statement.

He then asked for the set to be held against what the databases have
natively. Each native form was run on 2026-11-23 in the two the engine
is built with, SQLite 3.54.1 and DuckDB 2.0.0. Postgres, the store
planned for a server, is from its documentation.

| Function | SQLite | DuckDB | Postgres |
|---|---|---|---|
| `COUNT`, `SUM`, `MIN`, `MAX` | native | native | native |
| how many different values | `COUNT(DISTINCT x)` | the same | the same |
| the same, of the rows that meet a condition | `FILTER (WHERE ...)` | the same | the same |
| whether some row, or every row, meets a condition | `EXISTS` | the same, and `bool_and`, `bool_or` | the same, and `bool_and`, `bool_or` |
| one value by the order of the list | `ORDER BY ... LIMIT 1 OFFSET n` | the same, and `first`, `arg_max` | `ORDER BY ... LIMIT 1 OFFSET n` |
| the values of a column as one text | `string_agg(x, ', ' ORDER BY y)` | the same | the same |
| a shorter, a sorted, a cut list; the different values | `WHERE`, `ORDER BY`, `LIMIT`, `DISTINCT` | the same | the same |
| `AVERAGE` | `AVG` answers a float | `AVG` of a `DECIMAL` answers a `DOUBLE` | exact, at a scale of its own |
| the middle value | not native; `ORDER BY` and `OFFSET` give it | `median` | `percentile_cont(0.5)` |
| standard deviation, variance, a percentile, the most frequent value | not native | native | native |
| running totals and ranks | window functions | the same | the same |

## Decision

1. **One set of functions for every kind of list.** The same name
   works over a list a document holds and over a list that is a
   `QUERY`. The engine chooses how it is worked out.

2. **A function over a list enters the language only together with its
   SQL for every database the engine uses, and with cases that prove
   one answer on all of them.** Today those are SQLite and DuckDB;
   Postgres joins them when it is a store. A function that can be
   worked out only row by row is not taken. The owner's words: "SQL
   from the start".

3. **Over a list that is a `QUERY`, the function is one question to
   the listed class**, asked when the variable is read, for whoever
   reads it. *(Mine.)*
   - The rows are the ones the list has for that reader: the list's
     own condition, the read rule of the listed class (ADR-0049) and
     the read rights of every variable the function reads (ADR-0041).
     A row whose state does not let the reader read the column does
     not count. A column the reader may read in no state gives a
     blank.
   - Whoever may not read the document, or the list of it, gets no
     figure of it.
   - So the function stands only in a live variable (ADR-0076). A
     value that is kept, and a condition a decision reads, may not
     depend on who looks: a guard, a rule and a triggered variable are
     refused when the class is defined. For those the list must be
     kept (ADR-0078), and until it is, `LOOKUP` counts and sums for
     them.
   - It may stand anywhere inside the formula of a live variable:
     `SUM(invoices.net) / DECIMAL(COUNT(invoices), 0)`. Each function
     is one statement. One may not stand inside another's arguments
     (ADR-0020).

4. **How it is written.** *(Mine.)*
   - A column of a list is written with a dot: `MAX(invoices.net)`.
     It is how the language reads a field of an object, and how its
     catalogue already wrote a column of rows. A column is any stored
     variable of the listed class, or one of its system columns,
     whether the list shows it or not.
   - `COUNT(invoices)` counts the rows; `COUNT(invoices.net)` those
     that have a value in the column.
   - A condition is written as in `QUERY`: a bare name is a column of
     the listed class, `THIS.name` a variable of the document the
     formula belongs to.
   - `STATE()`, `ID()` and `REF()`, and the functions of a document's
     life, are not taken inside a condition yet. In a `LOOKUP` they
     are the document's the formula belongs to, in the condition of a
     `QUERY` the listed document's; one meaning is settled before
     either is let in. The state and the id of a row are its columns
     `state` and `id`.

5. **Figures over a query variable are built first**, then the same
   functions over the lists a document holds, then the functions that
   give a list again. The owner's words: "Let's not wait, do it".

6. **The set.** The owner's words: "the list is ok".

   | Function | Gives | In the catalogue before |
   |---|---|---|
   | `COUNT(list)`, `COUNT(list.column)` | how many rows; how many have a value | yes |
   | `SUM`, `AVERAGE`, `MIN`, `MAX` | a figure of one column | yes |
   | `MEDIAN` | the middle value of a column | new |
   | `COUNTUNIQUE` | how many different values | new |
   | `COUNTIF`, `SUMIF`, `AVERAGEIF`, `MINIF`, `MAXIF` | the same, of the rows that meet a condition | new |
   | `ANY`, `ALL` | whether some row, or every row, meets a condition | new |
   | `CONTAINS` | whether a column has a value | yes |
   | `FIRST`, `LAST`, `INDEX` | one value, by the order of the list | yes |
   | `JOIN` | the values of a column as one text | yes |
   | `FILTER` | the rows that meet a condition | yes, with a mask of yes and no in place of a condition |
   | `SORT`, `TOP` | the list in another order; its first rows | new |
   | `UNIQUE` | the different values of a column | new |

   `FILTER` takes a condition, as every other function of the set
   does, and no mask. *(Mine.)*

7. **The rules of a figure are those the same function has over single
   values**, and those a `LOOKUP` has over rows (ADR-0046): blanks are
   skipped; `COUNT` of nothing is 0; `SUM` of nothing is 0, or blank
   for money; `MIN` and `MAX` of nothing are blank; exact values add
   exactly; money adds in one currency, and two currencies are the
   error `currency_mismatch`. Of a list with no rows `ANY` is FALSE
   and `ALL` is TRUE; a row on which the condition is blank does not
   meet it. *(Mine.)*

8. **The mean, the middle value and the different values.** *(Mine.)*
   - `AVERAGE` over rows is what `AVERAGE` gives the same values
     inside one document (ADR-0022): a number for numbers; for exact
     values the exact sum divided by how many there are, at four
     places more than the column, by the language's own rule of
     division. Of nothing it is blank.
   - `MEDIAN` is the middle value of the values in their order, and
     of an even number of them the mean of the two in the middle. For
     exact values it is exact at one place more than the column, which
     that mean needs and which is enough for it, so nothing is
     rounded: of 0.01 and 0.02 it is 0.015. Money is in one currency.
     Of nothing it is blank.
   - `COUNTUNIQUE` counts the values that differ as `=` compares them,
     of any type that is one value. Money in two currencies is no
     error here: the same amount in two currencies is two values.
   - `MEDIAN` and `COUNTUNIQUE` are functions of rows alone. Over
     single values, `MEDIAN(a, b, c)`, they are refused: nobody asked
     for that, and each would need its form in three evaluators.
   - What stands over a list stands in the column position of a
     `LOOKUP` too, since both are the same aggregate over rows:
     `LOOKUP(Rates, AVERAGE(rate), currency = THIS.currency)`. ADR-0046
     left `AVERAGE` out because its exact division was not written for
     every executor; now it is.

9. **Over a list the document holds, the function is worked out where
   the document is.** *(Mine.)*
   - A list is a variable of type `rows`, with the scalar fields of a
     row as its columns, or an `array`, with the one column `value`.
   - The figure reads its own document and nothing else, so it is the
     same for every reader and is kept as any computed value is: it
     stands in a computed variable, a triggered one, a default, a
     guard, a rule and a live variable. The formula depends on the
     list, and is worked out again in the commit that changes it.
   - The engine folds the elements it has in memory with the commit:
     the ones the command assigns, and for a list the command leaves
     alone the rows the store has. No statement is asked for a list
     the command gave. Item 2 holds all the same: each function has
     its SQL over the table the store keeps the list in, and the cases
     prove one answer on the fold and on both databases.
   - The one argument may be an expression of the columns of one list,
     as ADR-0019 wrote it: `SUM(lines.qty * lines.price)`. A column is
     `list.column` there, and a bare name a variable of the document,
     the same for every row. In a form with a condition the names are
     the row's, as in item 4, and its last argument may be an
     expression of them: `SUMIF(lines, delivered, qty * price)`.
   - It compiles to a fold, an instruction of its own beside the
     lookup: a fold reads no other document, so none of the rules for
     a formula that does apply to it. The instruction is added at the
     end of the list, and a program with a fold is not encoded yet: it
     is the server's, and a page asks the server for it.
   - Not taken yet, each refused in words when the class is defined
     or the question asked: a fold in a read rule, a read right, a
     filter and the condition of a `QUERY`, which run as a predicate
     over every row; in a title; in the page's own evaluator.

10. **One value out of a list: `FIRST`, `LAST`, `INDEX`, `JOIN`,
    `CONTAINS`.** *(Mine; the owner's word for step 3 is below.)*
    - They are what ADR-0019 and the catalogue said of them over an
      array, over a list of the document: `FIRST` and `LAST` the value
      of a column in the first and the last row, blank for a list with
      no rows; `INDEX` the one at a place counted from 1, and the error
      `out_of_range` for a place that is no whole number or that the
      list does not have; `JOIN` the values written as `TEXT` writes
      them, blanks skipped, the separator between them, blank when
      nothing is left; `CONTAINS` whether some row has the value,
      compared as `=` compares.
    - "First" is by the order of the list: for a list the document
      holds, the order its elements stand in; for a list that is a
      `QUERY`, the order its `QUERY` gives it, and the id after it, so
      that two rows the list orders alike stand the same way at every
      read.
    - The place, the separator and the value looked for are values of
      the document, the same for every row. No column may stand there,
      and no figure: one function is one statement (ADR-0020).
    - Each is an aggregate of the slot, as the figures are, with its
      fold and its SQL. They are aggregates of a list alone: in the
      column position of a `LOOKUP` they are refused, since the slot of
      a `LOOKUP` is part of an encoded program, which the further
      argument is not.
    - `CONTAINS` is `ANY` with a condition the binder writes: the
      column equals the value. So it needs nothing of its own in any
      executor.

## The owner's words

2026-11-23, in this order:

- "What functions can I run over this query variable? Like COUNT(q),
  MAX(q,col),...?"
- "I would add a comprehensive set of such functions."
- "Can you reconcile those functions against what we have native in
  database? Both in transactions and in analytical DuckDB. I think,
  eventually in the future we might want to optimise querying our
  stractures by sql push-down."
- On the recommendation, the set and the rule of item 2: "The file
  looks good. Let's not wait, do it, the list is ok, SQL from the
  start."

2026-11-24, shown step 3 as two groups, one value out of a list and a
list out of a list, with the proposal to build the first and to bring
one page on the second before building it: "go ahead with step 3".

## What is built

Step 1, the figures over a list that is a `QUERY`: `COUNT`, `SUM`,
`MIN`, `MAX`, `AVERAGE`, `MEDIAN` and `COUNTUNIQUE`, the forms with a
condition (`COUNTIF`, `SUMIF`, `MINIF`, `MAXIF`, `AVERAGEIF`), and
`ANY` and `ALL`.

- Each compiles to a query slot, as the aggregate in the column
  position of a `LOOKUP` does, and the store answers it with the
  statement `formula.AggregateSQL` writes for that aggregate. `COUNT`,
  `SUM`, `MIN` and `MAX` have their SQL for SQLite and for DuckDB
  since ADR-0046. `AVERAGE`, `MEDIAN` and `COUNTUNIQUE` got theirs
  with this record, and the lookup suite proves one answer for all
  seven on the Go fold, on SQLite, on DuckDB and on the store. That is
  how these fourteen functions meet item 2.
- How the three are asked, the same text in both dialects but for the
  casts:
  - `AVERAGE` of numbers is the engine's own sum, added in the order
    of the key, divided by `COUNT`. Of exact values it is the exact
    sum and the count given to the function that divides an exact
    amount as the language does, which both databases already have
    from the engine. No database's own `AVG` is used: in SQLite and in
    DuckDB it answers a float.
  - `MEDIAN` numbers the rows that have a value in the order of the
    value, with `ROW_NUMBER() OVER (ORDER BY ...)`, takes the one or
    the two in the middle and adds their halves; for exact values it
    adds them and multiplies by five, at one place more. DuckDB's own
    `median` is not used: it answers a float for an even number of
    exact values.
  - `COUNTUNIQUE` is `COUNT(DISTINCT ...)`, of the mantissa for an
    exact value and of the amount with its currency for money.
- The rows of the statement are the ones the filter compiler gives
  for the list and its reader, so nothing about rights is written a
  second time, and an index on the pointer serves a figure as it
  serves the list.
- The document is read on the writer's turn; the figures are asked
  beside the writer.

Step 2, the same figures over the lines and the tags a document holds
(item 9): every function of step 1 over a `rows` or an `array`
variable.

- The binder gives a list the document holds as the source of the
  slot, and the slot is one of the program's folds. The store's
  evaluator answers a fold with the elements of the document it runs
  the program over, through the fold the in-memory executor of the
  suites uses.
- `conformance/formula/held.json` proves one answer on the Go fold, on
  SQLite and on DuckDB, where the list is a table with the place of
  each row.

Step 3, its first group (item 10): `FIRST`, `LAST`, `INDEX`, `JOIN`
and `CONTAINS` over both kinds of list.

- How each is asked, the same text in both dialects but for one piece:
  `FIRST` and `LAST` read one row, the rows ordered as the list and
  cut to one (`ORDER BY ... LIMIT 1`, the order turned round for the
  last). `INDEX` numbers the rows in that order and reads the one
  whose number is the place; no row there is the error. `JOIN` is
  `string_agg` with its `ORDER BY` in SQLite. DuckDB's `string_agg`
  takes a constant for its separator and nothing else, so there the
  texts are gathered into a list and folded with the separator
  between each two (`list_reduce`). `CONTAINS` is a count of the rows
  on which the column equals the value.
- Forty more cases in `conformance/formula/held.json` prove one answer
  on the Go fold, on SQLite and on DuckDB.
- Over a list that is a `QUERY` the store gives the statement the
  list's own order.

Not built yet, each decided above:

- A fold in the places item 9 leaves out, and in the browser VM.
- The functions that give a list again, `FILTER`, `SORT`, `TOP` and
  `UNIQUE` (step 3, its second group). Where such a list may stand is
  not decided: only inside another function, or also as a variable of
  its own.

## Consequences

- An order shows how many invoices it has and what they add up to,
  and a page a figure beside its list, with no row read to be
  counted.
- Two readers get two figures. That is the list's own rule, and it is
  why a figure is live and no guard may read one.
- The catalogue gains nine functions with this step, and a function
  that is in the catalogue and has no SQL yet stays refused.
- The mean of amounts with two places has six, and the middle value
  three. A class that wants two writes `ROUND(AVERAGE(invoices.net), 2)`.
- An encoded program may name three more aggregates. The format does
  not change: an engine older than this one refuses such a program as
  one it does not know, and reads every program it read before.
- `FILTER` changes its second argument before it is built.
- A live value's formula now runs on a reader's evaluator when it has
  a figure, and under the writer's lock when it has none, as before.

## What this record leaves out

- What the databases have and the set has not: running totals and
  ranks, standard deviation and variance, a percentile, the most
  frequent value, and the row that has the largest value. Each can be
  pushed down; none is decided.
- A table of totals by a column. That is grouping inside `QUERY`
  (ADR-0076, item 9).
- A figure over other documents in a guard: it needs a kept list
  (ADR-0078).
- Sending a question to DuckDB when it is over everything, and the
  reading rights there: the analytical engine, Phase 8. Item 2 keeps
  it open, since every function has its DuckDB form already.
- A `LOOKUP` in a live variable (ADR-0076, item 5): still refused.

## Performance

The budget this record adds: **a figure over a list narrowed by an
indexed `ref` to at most 100 documents is read in under 0.5 ms from a
class of 1,000,000**, the whole call of the command `live`.
`BenchmarkLiveFigure` in the `store` package proves it and is in the
gate, where it runs among 10,000 invoices.

Measured on 2026-11-23, the hundred invoices of one order, a different
order each time, three rounds each:

| | Among 10,000 | Among 1,000,000, on disk | Allocations |
|---|---|---|---|
| reading the hundred rows (`BenchmarkLiveList`) | 0.34 ms | 0.52 to 0.55 ms | 4,319 |
| `SUM(invoices.net)` (`BenchmarkLiveFigure`) | 0.055 ms | 0.20 to 0.21 ms | 294 |
| `COUNT(invoices)` (`BenchmarkLiveFigureCount`) | 0.044 ms | 0.17 to 0.19 ms | 272 |
| `SUMIF(invoices, state = "draft", net)` (`BenchmarkLiveFigureIf`) | 0.057 ms | 0.22 to 0.27 ms | 300 |
| `AVERAGE(invoices.net)` (`BenchmarkLiveFigureAverage`) | 0.057 ms | 0.19 to 0.21 ms | 308 |
| `COUNTUNIQUE(invoices.net)` (`BenchmarkLiveFigureUnique`) | 0.052 ms | 0.18 to 0.19 ms | 276 |
| `MEDIAN(invoices.net)` (`BenchmarkLiveFigureMedian`) | 0.15 ms | 0.30 ms | 288 |

A figure allocates the same whatever the list holds: no row comes
into memory. Among a million invoices most of its time is the disk:
the hundred invoices of an order lie apart in the file, and each is
visited for its amount. The question of the list is compiled anew at
every read; keeping it compiled is left for later.

The last three rows were measured with their functions, the same day,
with `SUM` beside them as the control: 0.054 ms and 0.18 to 0.19 ms.
The middle value costs about three times a sum among 10,000: the
hundred amounts are put in order inside the statement, with a window
function. Its first form read the rows three times, once for the
value, once for its error and once for the statement around them, and
took 0.26 ms; this one reads them once.

No budget of a commit changes: a figure over a list that is a `QUERY`
is in no commit.

A figure over a list the document holds is in the commit that changes
the list. Measured on 2026-11-23, a commit that changes the twenty
lines of a bill, three rounds each: 0.13 to 0.14 ms and 987
allocations with a count and a total of the lines
(`BenchmarkHeldFigure`), 0.13 to 0.14 ms and 969 without
(`BenchmarkHeldFigureNone`). A class with no fold pays one comparison
for each formula it runs, and no allocation: the map of the lists a
command assigns is the store's own, one for every commit, since the
writer's evaluator now holds it, and a map made per commit would have
been one allocation more in each.
