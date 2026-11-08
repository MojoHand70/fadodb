# ADR-0037: Explicit tables bound to SQLite and the compiled LOOKUP

- Status: Proposed; amends ADR-0003 (bytecode format v3), ADR-0016 (the table definition) and ADR-0020 (how a LOOKUP condition is compiled and carried)
- Date: 2026-11-08

## Context

ADR-0016 made an explicit table a first-class object with a schema, a
key, indexes and a storage binding, and gave formulas `LOOKUP(source,
column, condition)`. ADR-0020 fixed the run-time shape: one instruction
over a query slot, one engine call per invocation, the first matching row
in the source's key order, and said the slot holds the condition "as SQL
per dialect generated at bind time" with the document's variables bound
as `THIS.` arguments. ADR-0036 measured the in-memory index against the
prepared statement and fixed `MaxIndexedRows`.

Three things were still open when the store arrived. How a table is
written down and stored: ADR-0016 names a DDL subset and an import
command without a format. How the condition travels: bytecode is the
interchange format of ADR-0003 and the browser VM will read it, so a slot
that holds SQL text per dialect would put engine dialects into a format
meant to be engine-neutral. And what the equality operator means for an
index key: ADR-0005 reads a blank as 0 or "" in a comparison, and
ADR-0022 compares exact values by amount, so a hash index must agree with
`=` on every value or give a different answer than the statement.

## Decision

1. **A table is defined in JSON**, like a class or a schema, and stored
   with its versions:

   ```json
   {"name": "Rates", "label": "Exchange rates",
    "columns": {"code": "text", "rate": "decimal(6)", "fee": "money(2)", "since": "date"},
    "key": ["code"], "indexes": [["since"]], "binding": "sqlite"}
   ```

   `columns` are scalar types of ADR-0013 and ADR-0022, money as two
   stored columns, no objects and no composites yet; `key` names the
   columns that identify a row and order the table, which is the order
   of "first row" (ADR-0020); `indexes` lists further column sets;
   `binding` is `sqlite`, the default and the only one this build
   implements, `duckdb` and `external` being refused as `not_supported`
   until Phases 4 and 8. The definition is checked with the diagnostics
   of the class model, stored in `tables_registry`, and the rows live in
   `tbl_<Name>` with the view `<Name>`, which the SQL endpoint and the
   analytical engine read. A table name may not be a class name, a
   generated name or a view's.

2. **Rows arrive through `ImportRows`**, one transaction, each row a map
   of column to value in the wire shape of ADR-0018, inserted when its
   key is new and replaced when its key exists; `Replace` empties the
   table first. A key column may not be blank. An import is not an event:
   a table is reference data, not a document, and the `table.*` actions
   and the `table:` trigger of ADR-0015 arrive with Phases 4 and 8. An
   import drops the table's index; the next lookup rebuilds it. A table a
   class reads, or one with rows, is not redefined in this build, for the
   same reason a class with documents is not: migration is undecided.

3. **The condition is a program of its own.** The binder compiles
   `LOOKUP(source, column, condition)` into one query slot that holds the
   source, the column and its type, the key columns, and the condition as
   a nested program whose slots are the source columns it reads, named by
   column, and the document variables it reads as `THIS.name`, named
   `THIS.<variable>`; the slot lists the enclosing program's slots those
   variables live in. Inside the condition a bare name is a column and a
   variable is `THIS.name`, scalars only for now; a query function does
   not nest inside another's arguments. The bytecode format becomes v3 to
   carry the slot. The SQL per dialect of ADR-0020 is generated from the
   nested program when the executor prepares its statement, not at bind
   time, so that the format stays engine-neutral and a third dialect needs
   no re-definition of every class.

4. **A row on which the condition errors does not match**, on every
   evaluator: the VM skips it, and the generated subquery requires the
   condition's error expression to be NULL. A lookup itself never fails;
   the key expression failing on every row is the same as no row.

5. **The equality shape is recorded.** When the condition is one equality
   of a column with an expression of the document alone, of the column's
   kind, the binder keeps that expression as a second nested program and
   names the column, and the store serves the lookup from a hash index
   while the table has at most `MaxIndexedRows` rows (ADR-0036). The index
   key is the value as `=` sees it: a text, ref or json column by its
   text with a blank as ""; a number, date, datetime or bool column by
   its number with a blank as 0; a decimal column by its amount, so that
   1.5 and 1.50 are one key and a blank is 0. Money columns are not
   indexed, since money compares with a decimal by its amount and with
   money of another currency not at all; the statement path serves them.
   Above the limit, the equality shape runs as a statement narrowed to the
   rows whose column equals the key, which SQLite answers through the
   table's index and which is exact, since a key that is neither blank nor
   the zero of its kind equals no blank row; a blank or zero key runs the
   condition over the table. Every other condition runs the condition over
   the table as a statement.

6. **`MaxIndexedRows` is kept in the metadata** and may be changed when the
   database is opened, since it changes speed and memory and never a
   result (ADR-0036).

7. **The executor is an interface of the VM.** `QueryExecutor.Lookup`
   receives the slot, the values of the `THIS` variables and the formula's
   context; the store implements it over its tables, and an in-memory
   executor in the formula package answers the conformance suite and the
   tests. The `lookup` suite declares its tables with their rows inline
   under `sources` and runs on the Go VM, SQLite and DuckDB with one
   answer; three binding suites fix what is refused.

## Consequences

- An agent defines a table the way it defines a class: JSON in, a list of
  diagnostics out, each with a code and a path; `ImportRows` is the bulk
  load, and `TableRows` reads the table back.
- The format change is the last one a program of the first release needs
  for the query functions: QUERY gets its own slot shape in Phase 4 and a
  reserved opcode exists for it.
- The condition as a program means the browser VM can show what a lookup
  tests without executing it, and that the SQL generator's correctness
  tests cover lookups through the same conformance cases.
- A full-table statement is proportional to the table: 35 µs per thousand
  rows here. The binder knows which shape a formula took, and the index
  column is in the slot; a later change can tell the author.
- ADR-0020's "SQL per dialect generated at bind time" is amended to "from
  the slot's program when the executor prepares"; its other decisions
  stand. ADR-0016's DDL subset is this JSON; its import command is
  `ImportRows`, and the CSV form arrives with the CLI.

## Performance

Measured on 2026-11-08 on the machine of ADR-0035, a two-formula class,
one lookup of a 1,000-row table and one arithmetic formula on the result,
recalculated in memory:

| Path | Time per recalculation | Allocations |
|---|---|---|
| Index, equality shape | 88 ns | 0 |
| Statement, equality shape, narrowed to the key by the table's index | 2.4 µs | 34, 1.4 KB |
| Statement, a condition with AND, the condition over the table | 35 µs | 30, 1.3 KB |

The cached budget of ADR-0016, under 1 µs, is met by the index with ten
times to spare; the cold budget, under 100 µs against an indexed table, is
met by the narrowed statement forty times over. The allocations of the
statement paths are database/sql's parameter and row boxing. The
benchmarks live in the `store` package and the gate watches the first.
