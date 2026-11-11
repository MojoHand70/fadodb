# ADR-0046: Formulas that read other documents: a class as the source of LOOKUP, with COUNT, SUM, MIN and MAX

- Status: Accepted; amends ADR-0003 (bytecode format v4), ADR-0010 (with whose access a formula reads other documents), ADR-0011 (several classes defined in one call), ADR-0016 (no access predicate on a class a formula reads; `QUERY` and the bindings leave Phase 4), ADR-0020 (a class is answered by the transactional store; the type of an aggregate), ADR-0037 (the query slot carries an aggregate, the key is found inside a conjunction, an aggregate may fail)
- Date: 2026-11-10

## Context

A process often depends on documents other than the one that moves. A
purchase waits for three approvals, each a document with states of its
own. A customer may not order while the unpaid invoices exceed a limit.
A number must not be used twice.

What runs today reaches halfway. `LOOKUP(source, column, condition)`
reads one value of an explicit table (ADR-0037). A rule creates, moves
and refreshes another document through the outbox (ADR-0044), so an
approval can wake its purchase. But the purchase cannot ask how many of
its approvals are granted. ADR-0044, item 13, says the pattern of
parallel approvals is whole only with documents as a lookup source, and
ADR-0043, item 13, left that, `QUERY` and the binding interface to a
record of their own. This is that record.

ADR-0016 gave the shape: a source is a table or a class such as
`Invoice`, a column may be an aggregate, a bare name is a column of the
source and a variable of the document is `THIS.name`. Four things were
open.

- **Whose access.** ADR-0010 says a formula reads other documents with
  the document owner's access. ADR-0016 and ADR-0020 add the caller's
  read predicate. Both cannot hold, and neither is built.
- **How much now.** `QUERY` returns rows. Nothing that exists today
  shows rows that come from other documents: layouts arrive with the
  server and reports with the analytical engine.
- **What a formula sees inside a commit**: of the commits before it, of
  its own document, and when its value goes stale.
- **Which engine answers.** ADR-0020, item 3, answers a class from the
  DuckDB projection in the full build. The projection follows the
  commits, and a guard inside a commit cannot wait for it.

The owner ruled on the first two on 2026-11-10. They are items 1 and 2.

## Decision

1. **One value now, lists later** (the owner's ruling). This step
   builds formulas that bring one value from other documents: a
   variable of one document, a count, a sum, the smallest or the
   largest. `QUERY`, which returns a list of rows, is built with the
   first layout or report that needs one. Item 15 says what is fixed
   for it already.

2. **A formula reads with the engine's eyes** (the owner's ruling). A
   formula that reads the documents of a class sees every one of them,
   and every variable of each, whoever caused the formula to be
   computed. No access predicate is added and no right is asked. Who
   may see the result is decided where the result is stored: by the
   rights on the variable that holds it (ADR-0028). This differs from
   actions on purpose. What a rule does to another document it does as
   the person (ADR-0044, item 11). What a formula reads is the same for
   everybody, so that a stored value and a guard do not depend on who
   pressed the button, unless the condition itself says `USER()`.

3. **A class is a source.** `LOOKUP(Approval, column, condition)` reads
   the documents of the class `Approval` as they are now: one row per
   document, those in a terminal state included, deleted ones not. The
   columns are the system columns `id`, `title`, `state`, `version`,
   `created_at`, `updated_at` and `state_entered_at`, and the scalar
   variables of the class's current version, inputs and computed, with
   their stored values. A document written by an older version of the
   class is blank in the variables that version did not have. The key,
   which decides the first row of ADR-0020, is `id`. A class may read
   itself. The fields of an object, `rows` variables and the history of
   a class are not sources yet.

4. **An aggregate may stand where the column stands**, over a class or
   a table: `COUNT(x)`, `SUM(x)`, `MIN(x)` and `MAX(x)`, where `x` is
   an expression over the source's columns, written as in the
   condition. The answer is what the same function gives over the same
   values inside one document (ADR-0019, ADR-0022). Blanks are skipped.
   `COUNT(id)` is the number of matching documents. Exact values add
   exactly and keep their type, and money adds within one currency.
   With no matching row `COUNT` is 0, `SUM` is 0, or blank for money,
   and `MIN` and `MAX` are blank. ADR-0020 said an aggregate is a
   `number`; it has the type its function gives instead. `AVERAGE`
   waits: `SUM` and `COUNT` give it, and its exact rounding would have
   to be written for three evaluators.

5. **Inside the call the names mean what they meant** (ADR-0016,
   ADR-0037). A bare name is a column of the source. `THIS.name` is a
   variable of the document the formula belongs to. A context function
   speaks of that document too: `ID()` is its id and `STATE()` its
   state, while `id` and `state` are the source's. So `id <> ID()`
   leaves the document itself out of a count over its own class. A
   query function still does not nest inside another.

6. **Where it may be written.** Wherever a `LOOKUP` of a table may be
   written today, except in an auto formula. That is a triggered
   variable, a default, a guard, an access rule other than `read`, a
   right, the condition of a rule and the argument of an action. The
   stored value of an auto variable must be a function of its own
   document (ADR-0015), so a class as the source there is refused when
   the class is defined, with the hint to make the variable triggered.

7. **What it sees.** The lookup runs inside the commit that evaluates
   it, in that commit's transaction. It sees every commit made before,
   the earlier commits of its own chain included (ADR-0040). Of the
   document being computed it sees the row stored before this commit,
   and no row while the document is being created. It reads stored
   values and computes nobody's formulas, so two classes that read each
   other cannot loop. A preview reads the documents as they are stored
   now: `At(t)` moves the clock and nothing else (ADR-0042).

8. **Nothing recomputes by itself.** A value read from other documents
   is true of the moment its trigger fired, and ADR-0043 stores that
   moment beside it. A later change to those documents reaches the
   reader when the reader commits again, and the author says when: the
   rule of the approval refreshes the purchase (ADR-0044, item 13). A
   guard with a lookup is looked at when its own document commits, not
   when the others change. A trigger that fires on a change of another
   class is not in this step.

9. **One statement per evaluation, on the transactional store.** A
   class is answered by SQLite on the writer, inside a commit and in a
   preview, in every build. ADR-0020, item 3, gave a class to the
   DuckDB projection, which follows the commits and so cannot answer a
   guard. Nothing crosses to the engine per row (ADR-0020, item 2). The
   in-memory index of ADR-0036 stays with tables, since documents
   change with every commit.

10. **The key is found inside an `AND`.** ADR-0037, item 5, recorded
    the key only when the whole condition was one equality. Now the
    first term of a top-level `AND` that compares a column for equality
    with an expression of the document alone is the key, and the
    statement is narrowed by it, under the same rule for blank and zero
    keys. `purchase = ID() AND state = "granted"` is therefore answered
    through the index of `purchase` when that variable says
    `"index": true`. Without an index the statement reads every
    document of the class. That is allowed, and its cost is measured
    (Performance).

11. **A new version of a class may not break its readers.** A formula
    is compiled against the current version of the class it reads. A
    redefinition that removes or retypes a variable which a formula of
    another class reads is refused with `not_allowed`, naming that
    formula, as ADR-0044, item 7, refuses one that breaks a rule. The
    reader is changed first.

12. **Classes that name each other are defined in one call.** The
    approval's rule names the purchase and the purchase's formula names
    the approval, so neither can be defined first. `DefineClasses`
    takes several definitions, checks each against the others and
    against the stored ones, and stores all or none, each as
    `DefineClass` would. `ValidateClasses` answers the diagnostics of
    the set and stores nothing.

13. **Bytecode format v4.** The query slot of ADR-0037 gains the
    aggregate and the expression it runs over, a nested program like
    the condition. Classes are compiled from their JSON when a database
    opens, so no file changes and the engine schema stays 4. The
    browser VM still receives the last computed value and executes no
    lookup (ADR-0020, item 7).

14. **An aggregate can fail.** A row on which the condition stops with
    an error still does not match (ADR-0037, item 4). An aggregate
    fails where its function fails inside one document: a sum over two
    currencies is `currency_mismatch`, a sum too large is `overflow`,
    and an error of the expression on a matching row is that error. The
    formula then fails like any other, and `IFERROR` catches it.
    ADR-0037 said a lookup never fails. A lookup of one column still
    does not.

15. **What waits.**

    - `QUERY`. Fixed already: items 2, 3, 5 and 7 to 9 hold for it, it
      returns `rows` within the limits of ADR-0020, item 6, and it is
      not written in an auto formula. How its several columns, its sort
      and its grouping are written is settled by the record that builds
      it: the language has no list, and the first real use should shape
      the answer.
    - The history of a class, `rows` variables and the fields of an
      object as sources. `AVERAGE`. A trigger on another class's
      change. Recomputing many readers in one statement (ADR-0020,
      item 5), which belongs to the analytical engine.
    - The binding interface, which lets a table be stored outside the
      database file. ADR-0037 expected the `duckdb` and `external`
      bindings in Phases 4 and 8. Nothing in Phase 4 needs a second
      binding, and Phase 8 builds the engine they stand on. They move
      there whole.

16. **The conformance suite.** `conformance/formula/lookup.json` gains
    the aggregates, with their rows inline, on the Go VM, SQLite and
    DuckDB with one answer. The binding suites fix what is refused.
    `conformance/rules/` gains scripts over several classes: the
    approvals below, a class that counts over itself, and a chain whose
    second commit reads what the first wrote.

## Details settled while building

The first part of the step built items 4, 10, 13 and 14 over explicit
tables, on 2026-11-10, and settled these points. A class as the source,
items 3, 6 to 9 and 11, and `DefineClasses`, item 12, follow.

- **An exact sum fails only when the total does not fit**, whatever the
  order of the rows. Inside one document `SUM(a, b, c)` adds from the
  left and fails at the first step that overflows; rows have no order a
  sum should depend on. The Go VM adds the mantissas as two halves and
  joins them, SQLite adds the same two halves with its own `SUM`, and
  DuckDB adds in 128 bits.
- **Numbers are added one by one in key order**, from zero, as
  `SUM(a, b, c)` adds them: a sum of floating-point numbers depends on
  the order, so the order is fixed. SQLite's own `SUM` compensates as
  it adds and DuckDB's adds in no fixed order, so SQLite hands the rows
  in key order to a Go function the dialect registers, and DuckDB folds
  an ordered list. It is the one aggregate that crosses to Go per row:
  10 µs for ten rows, where an exact sum takes 3.7 µs and a count
  2.5 µs. Amounts that are added belong in `decimal` or `money`.
- **The errors, in order.** The error of the first matching row, in key
  order, on which the aggregated expression fails; then
  `currency_mismatch`; then `overflow`, or `not_a_number` for a sum of
  numbers that leaves the range. `COUNT` of money in two currencies is
  no error, as inside one document.
- **Where an aggregate may be written.** Over a table, wherever a
  `LOOKUP` of a table may be, an auto formula included. The expression
  it runs over is bound like the condition: bare names are columns,
  `THIS.name` the document, and a context function only where the
  formula's mode allows one. It takes exactly one expression.
- **`AVERAGE` and every other function are refused** in the column
  position when the class is defined, `AVERAGE` with the hint to divide
  a `SUM` by a `COUNT`.
- **A money column is never a key.** Money equals a decimal by its
  amount and money of another currency not at all, which ADR-0037,
  item 5, already said; the binder recorded the key all the same, and
  the in-memory index answered by the amount alone. Running the formula
  suite's lookups against the store found it. The binder now records no
  key for a money column.
- **The slot says when its key is a part.** `KeyPart` is set when the
  equality is one term of a conjunction. The index of first rows
  answers only a key that is the whole condition; a partial key and
  every aggregate need all the rows of the key and run as the statement
  narrowed by it. An `OR`, or an equality with a money column, leaves no
  key, and the statement reads the table.
- **The store answers the formula suite.** `conformance/formula/lookup.json`
  runs against the store's own tables as well, once with the index and
  once without, beside the Go VM, the SQL generator on SQLite and DuckDB.

## Consequences

- **Parallel approvals are whole.** Each approval is a document that
  somebody grants or refuses. Granting one refreshes the purchase, the
  purchase counts, and its own guard moves it:

  ```json
  {"name": "Approval",
   "variables": {"purchase": {"type": "text", "required": true, "index": true}},
   "states": {"initial": "asked", "list": ["asked", "granted", "refused"]},
   "transitions": [{"name": "grant", "from": "asked", "to": "granted"},
                   {"name": "refuse", "from": "asked", "to": "refused"}],
   "rules": [{"on": "enter", "state": "granted",
              "do": [{"action": "refresh", "class": "Purchase", "id": "purchase"}]}]}
  ```

  ```json
  {"name": "Purchase",
   "variables": {
     "granted": {"formula": "LOOKUP(Approval, COUNT(id), purchase = ID() AND state = \"granted\")",
                 "mode": "triggered", "on": ["refresh"]}},
   "states": {"initial": "waiting", "list": ["waiting", "approved"]},
   "transitions": [{"from": "waiting", "to": "approved", "guard": "granted >= 3"}]}
  ```

  The same form says that a number is free,
  `LOOKUP(Invoice, COUNT(id), number = THIS.number AND id <> ID()) = 0`,
  and what a customer owes,
  `LOOKUP(Invoice, SUM(total), customer = THIS.customer AND state = "sent")`.

- **Defining a class becomes a privileged act.** By item 2 a formula
  can read any document, and its author decides through the rights of
  one variable who sees the answer. Whoever may define a class, or
  preview a draft of one (ADR-0025), can therefore learn a count, a sum
  or one value of any class, including a variable the rights table
  hides from them. The engine does not ask today who defines a class:
  the host program decides which callers reach `DefineClass` and
  `PreviewDraft`, and from this record on that decision guards every
  document. The documentation says so. Phase 5, which brings the access
  rules, has to say who may define and preview classes, and may let a
  class name the classes allowed to read it.

- **A value can be stale, and says so.** A count is the count at its
  moment. A purchase whose approval was granted while the outbox was
  stopped shows the old count until the refresh runs. `Refresh` by hand
  recomputes it.

- **A formula can be slow.** A count that no index narrows reads every
  document of its class inside the commit, each time it is evaluated.
  Nothing refuses it. The fix is `"index": true` on the variable the
  condition compares.

- **No cache.** ADR-0016 planned a cache of results per argument set
  for triggered variables. It is not built: every commit of the source
  class would empty it, and every evaluation is one statement.

- **The roadmap moves on acceptance.** The row of Phase 4 loses
  "storage bindings, QUERY" and gains this step. The bindings are
  already in the row of Phase 8. `QUERY` goes to whichever of Phase 6,
  with the layouts, and Phase 8, with the reports, needs it first.

## Performance

- A commit that evaluates no lookup over a class does what it did: the
  benchmarks of the gate keep their allocations.
- A lookup or an aggregate over a class of 100,000 documents, narrowed
  by an indexed variable to at most 100 of them, runs under 100 µs, the
  cold budget of ADR-0016. A benchmark in the `store` package proves
  it and joins the gate.
- A refresh that recomputes one such count and moves the document by
  its guard stays inside the 2 ms of a transition commit (ADR-0006,
  ADR-0012).
- A statement that no index narrows costs in proportion to the class.
  ADR-0037 measured 35 µs per thousand rows of a table. The last step
  of the phase measures it over documents, and `docs/perf/v0.4.0.md`
  reports it.
