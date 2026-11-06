# ADR-0021: Size limits: one text limit per database, enforced before building

- Status: Accepted
- Date: 2026-11-06

## Context

A formula can build text: `&`, `CONCAT`, `REPEAT`, `PAD`, `SUBSTITUTE`,
`REPLACE` and `REGEXREPLACE` all return something longer than their inputs.
The first implementation of ADR-0019 capped a result at 1,048,576 code
points, but only in `REPEAT` and `PAD`, and on the SQL dialects only after
the result had been built. A property campaign on 2026-11-06 found the
consequence: `SUBSTITUTE(REPEAT("a", 100000), "a", REPEAT("b", 10000))` is a
1 GB text on the Go VM, on SQLite and on DuckDB, in well under a second, and
a `REPEAT` of a `REPEAT` built hundreds of megabytes on DuckDB before its
length check ran. The campaign reached 30 GB and took the development
machine down. Nothing distinguished these formulas from one a document
author could write.

Three facts shape the decision. The number itself was never promised; it
lived in the code. DuckDB folds a constant expression at bind time, so a
`CASE` around a builder does not keep the builder from running on constant
arguments; only a bounded argument keeps the result bounded. And the
analytical engine evaluates a column 2,048 rows at a time per thread, so a
per-value limit of 1,048,576 code points means up to 8 GB in flight per
step, which no setting on the engine could make safe.

## Decision

1. **Every text an evaluation produces is at most `MaxText` code points.**
   A function whose result can be longer than its inputs works the length
   out first and stops with `out_of_range` instead of building it: `&` and
   `CONCAT` add the lengths of their parts, `REPEAT` multiplies, `PAD` reads
   the width, `REPLACE` reads the position and the length clamped to the
   text, `SUBSTITUTE` counts the occurrences, and `REGEXREPLACE` finds the
   matches before it replaces them. Functions that only shrink or recase
   their input need no check. Literals are bounded by the formula that
   holds them.
2. **Counts clamp.** A count, length or width past the end of the text
   reads to the end: `LEFT("abc", 1e10)` is "abc". A negative count, or a
   position below 1, is `out_of_range` as before. The separate ceiling of
   a billion on counts is gone; the text limit is the only number.
3. **The limit belongs to the database.** It is chosen when the database is
   created, the default is 65,536 code points, and the same number bounds a
   stored text value, so that whatever a document can hold can flow through
   a formula. It is kept with the database's metadata (Phase 2) and may be
   raised, never lowered, since lowering would invalidate stored values. A
   server may refuse to open a database whose limit exceeds what it is
   configured to host, but never evaluates under a different one: a commit
   must recompute as it was committed, a database must give the same
   answers wherever it is opened, and the conformance suite states the
   semantics against a number, not a process. The floor is 64 code points,
   so that every rendering of a number or a date fits.
4. **The SQL dialects bound what they build, then detect.** A builder's SQL
   keeps its value bounded by clamping the argument that makes it grow, and
   reports `out_of_range` from a length computed without the result; a
   concatenation is cut one past the limit so that a chain stays bounded.
   The dialects receive the limit as the last argument of every builder,
   an integer literal in the statement, since a registered function and a
   macro have no VM to read it from.
5. **Resource budgets stay with the server.** Memory, threads and timeouts
   decide whether a recompute runs, never what it returns. A bounded value
   times a million rows is still unbounded, so the analytical path needs a
   memory ceiling of its own (Phase 8), and the project's own campaigns run
   under one.

The conformance suite carries the rule: the `limits` suite runs at
`max_text` 64 on every evaluator, and the `text` suite pins the default. The
property campaigns run at 1,024 by default (`FADODB_FUZZ_MAXTEXT`), so that
short inputs reach the boundary often and never build a long text.

## Consequences

- A formula cannot ask for more than a few hundred kilobytes on any
  evaluator, whatever its inputs; a document author cannot take a server
  down with a text function.
- `LEFT`, `RIGHT`, `MID` and `REPLACE` accept any non-negative count, as
  Excel does.
- Every text builder costs one length computation before it runs. On DuckDB
  `SUBSTITUTE` runs one bounded deletion to count and `REGEXREPLACE` two
  bounded replacements; the limit is one more argument in the generated
  SQL.
- JSON values and composites have no limit of their own yet; the json type's
  storage (Phase 2) and the element caps of ADR-0013 are where they belong.
  The text rule of ADR-0019 is amended; the list of error codes does not
  change.

## Performance

The VM's scalar path still allocates nothing on arithmetic. `&` now counts
the code points of both operands before it joins them: `BenchmarkVM_Text`
(`UPPER`, `LEFT`, two concatenations and `TEXT`) went from 107.7 ns/op
to 109.5 ns/op with the same 5 allocations and 32 bytes, since the count
runs only when the byte length already exceeds the limit, and `ROUND` got
faster, 40.8 to 35.4 ns/op in `BenchmarkVM_IfRoundCompare`, since its
power of ten is a table lookup now. The per-vector cost that
sets the default, at four bytes per code point:

| Limit in code points | Worst case per value | Per 2,048-row vector |
|---|---|---|
| 1,048,576 | 4 MB | 8 GB |
| 65,536 | 256 KB | 512 MB |
