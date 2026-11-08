# ADR-0038: The filter and the read-only SQL endpoint

- Status: Accepted; amends ADR-0011 (the shape of the query command) and ADR-0016 (the SQL endpoint's rules)
- Date: 2026-11-08

## Context

The architecture page gives clients three ways to read documents: the
query functions inside formulas, "a JSON filter DSL (`where`, `order`,
`limit`, `fields`)", and "a read-only SQL endpoint over the views, for
analysts and BI tools", with the SQLite authorizer restricting statements
to `SELECT`. ADR-0011 names the commands `query` and `sql`; ADR-0012 sets
the budget, an indexed filter over 1,000,000 documents under 10 ms;
ADR-0010 and ADR-0028 say what access rules and rights will do to both in
Phase 5. No record says what the filter is made of.

Two things decided the shape. The project already has a language for
conditions over a document's variables, with a binder that types them and
a generator that writes them as SQL with the blank rules of ADR-0005; a
second condition language in JSON would be a second thing to specify,
test on three evaluators and teach. And the generated SQL is exact at the
cost of indexes: a comparison wraps the column in `COALESCE` so that a
blank reads as 0 or "", and SQLite cannot use an index through it. The
first filter measurement confirmed both: a formula filter was right on
every case the formula suite covers, and an equality on an indexed
column read every row.

## Decision

1. **A filter is a formula.** `Query` takes a class, a `where` formula
   over the class's variables and the system columns `id`, `title`,
   `state`, `version`, `created_at`, `updated_at` and `state_entered_at`,
   an `order` of variables or system columns with a leading `-` for
   descending, a `limit` of at most 10,000 and 1,000 by default
   (ADR-0013, ADR-0020), an `offset`, and the `fields` to return. It
   answers documents in the wire shape of ADR-0018, in the order given
   and by `id` within it, with their composites loaded in one statement
   per child table; `fields` keeps the named variables and loads only the
   composites among them. `Count` counts what `where` matches.

   ```json
   {"class": "Invoice", "where": "state = \"approved\" AND total > 1000.00 AND owner = USER()",
    "order": ["-due", "number"], "limit": 100, "fields": ["number", "total", "lines"]}
   ```

2. **The filter means what the formula means.** The where is bound by the
   binder with pure and context functions, the context being the caller's
   principal and the clock; a lookup or a query in it is refused as
   `not_allowed`, as in a read rule (ADR-0010). It is translated by the
   SQL generator, so blanks, comparisons, text and dates follow ADR-0019,
   and a row on which the formula stops with an error does not match, as
   a row does not match a LOOKUP condition that errors (ADR-0037). A
   refusal carries a stable code, the element of the filter and, for the
   formula, the byte offset: `bad_formula`, `not_allowed`,
   `unknown_variable`, `bad_value`.

3. **The exact predicate is kept, and an index-friendly one is added.**
   For every comparison in the top-level AND of the where between a
   stored scalar column and a literal, with `=`, `<`, `<=`, `>` or `>=`,
   the compiler adds the comparison on the bare column, bound as a
   parameter in the column's stored form, so that SQLite answers it from
   the column's index. The addition is exact: when the zero a blank reads
   as, 0 or "", does not satisfy the comparison, no blank row matched
   anyway; when it does, `OR column IS NULL` keeps the blank rows. Money
   columns, json columns, literals that do not fit a decimal column's
   scale, `<>`, OR and anything that is not a bare column against a
   literal are left to the exact predicate alone.

4. **Order** is by the stored column, so blanks sort first ascending and
   last descending, as SQLite orders NULL. A composite cannot order.

5. **The SQL endpoint runs one `SELECT` over the views.** It runs on a
   connection of its own with `query_only` set and an authorizer that
   allows `SELECT`, reads of the views and of what a view reads for the
   caller, and functions other than the ones that reach the file system,
   and refuses reads of the physical tables' columns and of the schema,
   writes, schema changes, pragmas, attachments and transactions, as
   `not_allowed`. A statement that reads no column of a table, `count(*)`
   or `SELECT 1` over a view, is allowed over the tables behind the views
   and refused over the engine's own tables. Positional parameters bind
   arguments. At most 10,000 rows come back; more is `too_many_rows`, and
   the caller adds a `LIMIT`. Values are as the views store them: a
   mantissa for an exact column, days for a date, seconds for a datetime,
   which is what the SQLite scanner of Phase 8 reads too.

6. **Access rules and rights are not applied yet.** A filter and a SQL
   statement see every row and every column; the principal is taken and
   kept for Phase 5, when the read predicate joins the filter's WHERE and
   the rights govern `fields`, and the SQL endpoint stays row-level as
   ADR-0028 says.

## Consequences

- An agent asks for documents in the language it writes formulas in,
  with the same errors at the same offsets; there is nothing new to learn
  and nothing new to keep in step across evaluators.
- The index-friendly addition is a compiler rule, not a promise about
  SQLite's planner: a filter that does not fit it runs the exact predicate
  over the table, and the `count` row below shows what that costs.
- The SQL endpoint hides the physical names and the schema, so the views
  are the surface, as ADR-0014 intends; a count through a physical name is
  not refused, since it reveals no more than the view's count.
- Every `Query` compiles its where and prepares its statement; a cache of
  plans per class and where text is the obvious next step and is not
  needed to meet the budget.
- ADR-0011's `query` command carries this filter; `sql` carries the
  statement and its parameters. ADR-0016's "read-only SQL endpoint over the
  same views and predicates" is this endpoint, without the predicates
  until Phase 5.

## Performance

Measured on 2026-11-08 on the machine of ADR-0035. A class with an indexed
text variable, a number variable and a text variable, documents created
one commit each, then filtered; each row is the median of twenty runs and
includes compiling the filter, running it and building the documents.

| Filter | 100,000 documents | 1,000,000 documents |
|---|---|---|
| `number = "T-0500000"`, indexed, one document | 88 µs | 54 µs |
| `number >= "T-…" AND number < "T-…"`, indexed range, ten documents | 59 µs | 56 µs |
| `priority = 3 AND owner = "u7"`, plain columns, first ten | 0.62 ms | 0.53 ms |
| `Count` of `priority = 3`, a plain column, the whole table | 19.8 ms | 57.9 ms |

The budget of ADR-0012, an indexed filter over 1,000,000 documents under
10 ms, is the first two rows, met two hundred times over. The last row is
a full scan by nature and grows with the table; an index on the column,
`"index": true`, is the author's answer. The million documents were
loaded one commit each in 77 s, 13,000 commits per second, before any
batched load path. The benchmark is `BenchmarkFilter` in the `store`
package and runs on disk with `FADODB_BENCH_DIR` and `FADODB_BENCH_DOCS`.
