# ADR-0019: Formula language semantics: grammar, types, coercion, comparison, dates, text, composites and errors

- Status: Accepted; amended by ADR-0021, ADR-0022 (exact numbers), ADR-0023 (date and datetime), ADR-0024 (/ by zero, IFERROR), ADR-0031 (field paths, object blankness and equality), ADR-0043 (what an error in an action means), ADR-0047 (the functions of a document's life), ADR-0050 (two context functions, `USERKIND()` and `ACTINGFOR()`)
- Date: 2026-11-06

## Context

ADR-0003 fixes the pipeline, ADR-0004 the numbers, ADR-0005 the blanks and
ADR-0013 the types. The architecture lists what still had to be fixed
before the conformance suite could be written: mixed-type comparisons, text
and number coercion, date arithmetic and month clamping, number formatting
in `TEXT()`, string collation, and array and rows operations. Three
evaluators must agree on each rule, and the SQL target forces every rule to
be expressible in SQLite without extensions and in DuckDB with its native
functions.

The predecessor engine's tests encode behaviour its users accepted:
Excel-style overflow in `DATE()`, month-end clamping in `ADDMONTHS`,
calendar-day counting in `DAYS`, a stored 0 that is not blank. Where it had
a rule, this record keeps it.

Two choices were taken on 2026-11-06 against Excel's behaviour, for reasons
given below: text comparison is case-sensitive, and string literals use
double quotes only.

## Decision

### Grammar

- Function names, keywords and variable names are case-insensitive. Text
  values are not.
- Keywords are `AND OR NOT IF THEN ELSE TRUE FALSE BLANK THIS`. They are
  reserved: a variable, column or table may not be named so. This extends
  the reserved names of ADR-0013.
- A name followed by `(` is a function call; any other name is a variable
  or, inside `LOOKUP` and `QUERY`, a source column. A variable may share a
  name with a function, so a variable called `Year` keeps working.
- Literals: numbers `12`, `1.5`, `.5`, `2e3`; text in double quotes, with
  `""` for a quote inside; `TRUE`, `FALSE`, `BLANK`. There are no date
  literals: dates come from `DATE(y, m, d)` and `DATEVALUE(text)`.
- Operators from highest to lowest precedence: postfix `%`; unary `-`; `^`;
  `* / \`; `+ -`; `&`; `= <> < <= > >=`; `NOT`; `AND`; `OR`. Operators of
  equal precedence associate left, except `^`, which is right-associative
  (ADR-0004): `-x ^ 2` is `(-x) ^ 2` and `2 ^ 3 ^ 2` is `2 ^ 9`.
- `IF(c, a, b)` and `IF c THEN a ELSE b` are the same expression. `AND`,
  `OR` and `NOT` also exist in function form with the same meaning.
- `IF`, `AND` and `OR` evaluate only what they need, left to right. An
  error in a branch that is not taken is not an error.
- `x.col` reads one column of a rows variable as an array. `THIS.name`
  reaches the document's variables inside `LOOKUP` and `QUERY` (ADR-0016).

### Static types

- The binder gives every expression one static type from ADR-0013: `bool`,
  `number`, `text`, `date`, `ref`, `array<scalar>`, `rows<schema>`, or
  `blank` for the `BLANK` literal. Everything marked below as a
  definition-time error is rejected when the class is defined, never at run
  time. A computed variable's formula must have the variable's declared
  type; `blank` fits every type.
- There is no implicit conversion between text and any other type, except
  inside `&`. `VALUE`, `TEXT`, `DATEVALUE` and `DATE` convert explicitly.
- Arithmetic operands are `number`, `bool` (as 1 and 0) or `blank`; the
  result is `number`. No arithmetic operator applies to a date. `ADDDAYS`,
  `ADDMONTHS`, `ADDYEARS` and `DAYS` do that work, so a formula can never
  silently add seconds where the author meant days.
- `&` accepts any scalar operand and renders it by the `TEXT` rule.
- Comparison operands must have the same static type, or one must be
  `blank`. `bool`, `number`, `text`, `date` and `ref` compare; arrays and
  rows do not. `TRUE = 1` is a definition-time error, not FALSE as in Excel.
- A condition, in `IF`, `AND`, `OR`, `NOT`, guards, rules and access rules,
  must be `bool` or `blank`. A number in a condition is a definition-time
  error whose message suggests `<> 0`.

### Blank at run time

Any variable may hold a blank at run time. ADR-0005 applies per type: a
blank operand behaves as the zero of the operation, which is 0 in
arithmetic and numeric comparison, "" in text comparison and `&`, FALSE in a
condition, the epoch in date comparison, and "" in ref comparison.
`ISBLANK` is the only way to tell a blank from that zero.

### Comparison and text

- Text compares byte-wise on its UTF-8 encoding, which orders by Unicode
  code point, and is case-sensitive: `"abc" = "ABC"` is FALSE. That is
  SQLite's BINARY collation, DuckDB's default and Go's string comparison, so
  no evaluator needs a collation and an indexed text lookup keeps its index.
  Case-insensitive matching is written `UPPER(a) = UPPER(b)`.
- `number` and `date` compare as values, `bool` with FALSE before TRUE,
  `ref` as its `Class:id` text (ADR-0018).
- Positions and lengths in text functions count Unicode code points, not
  bytes and not grapheme clusters: `LEN("żółw")` is 4. Positions are 1-based.
- `UPPER` and `LOWER` apply Unicode simple case mapping.

### Numbers to text and back

- `TEXT(number)` renders with 15 significant digits by the C `%.15g` rule:
  trailing zeros removed, exponent form only below 1e-4 and at or above
  1e15, and `-0` as "0". An integral value below 1e15 therefore renders as
  plain digits with no fractional tail, as ADR-0004 requires for keys.
  `TEXT(0.1 + 0.2)` is "0.3" and `TEXT(1 / 3)` is "0.333333333333333". A
  format argument for `TEXT` is a later record.
- `TEXT(date)` is `YYYY-MM-DD` when the time of day is 00:00:00, otherwise
  `YYYY-MM-DD HH:MM:SS`, always UTC. `TEXT(bool)` is "TRUE" or "FALSE".
  `TEXT(ref)` is the `Class:id` form. `TEXT(BLANK)` is "".
- `VALUE(text)` accepts optional surrounding whitespace, an optional sign,
  digits with an optional fraction and exponent, a dot as the decimal point
  and no thousands separator. Empty or whitespace-only text is blank.
  Anything else is the error `bad_text`.
- `DATEVALUE(text)` accepts `YYYY-MM-DD` and `YYYY-MM-DD HH:MM:SS` as UTC.
  Empty text is blank; anything else is `bad_text`.

### Dates

- A date is a count of seconds since 1970-01-01 00:00:00 UTC with no time
  zone and no leap seconds; negative values are dates before 1970. The range
  is year 1 to year 9999 of the proleptic Gregorian calendar; a result
  outside it is the error `out_of_range`.
- `DATE(y, m, d)` truncates fractions and carries overflow as Excel does:
  month 14 is February of the next year, month 0 is December of the year
  before, day 0 is the last day of the month before, and February 30 is the
  first or second of March.
- `ADDMONTHS` and `ADDYEARS` keep the day of month and clamp to the last day
  when the target month is shorter: January 31 plus one month is February 29
  in a leap year and February 28 otherwise. `ADDDAYS` accepts fractions and
  keeps the time of day.
- `DAYS(a, b)` counts calendar days from `b` to `a`: each date is truncated
  to its midnight first, so 01:00 on the 31st minus 23:00 on the 30th is 1.
- `YEAR`, `MONTH`, `DAY` and `QUARTER` ignore the time of day. `TODAY()` is
  the current day's midnight UTC and `NOW()` the current second; both are
  context functions (ADR-0015).

### Arrays and rows

- An `array<scalar>` is a vector and `rows<schema>` is a table whose
  `x.col` is an array. Scalar operators and pure scalar functions lift
  element-wise over arrays: array against array needs equal lengths, else
  the error `length_mismatch`; array against scalar broadcasts the scalar.
  Comparisons lift to `array<bool>`. This is what makes
  `SUM(lines.qty * lines.price)` one expression.
- Blank elements follow the scalar blank rule element-wise.
- `SUM`, `AVERAGE`, `MIN`, `MAX` and `COUNT` reduce an array and skip
  blanks (ADR-0005); `COUNT` of a rows value counts its rows. The VM sums
  with Neumaier compensation, as SQLite does. SQL engines may still differ
  from the VM in the last bits of a floating-point aggregate, so the
  conformance suite compares aggregate results across evaluators to a
  relative 1e-12 and everything else exactly. Comparisons inside the
  language stay exact (ADR-0004).
- `INDEX(a, i)` is 1-based; a position outside the array is the error
  `out_of_range`. `FILTER(a, mask)` keeps the elements where the
  `array<bool>` is TRUE; on rows, `FILTER(lines, lines.qty > 0)` keeps rows.
  The catalog lists the remaining array and rows functions.
- Arrays and rows are never compared whole and never rendered by `TEXT`.
  Their size caps are ADR-0013's.

### Errors

- Evaluation produces a value or stops with an error carrying a code and
  the position of the failing operation. The codes are a closed list:
  `div_by_zero` for `\` and `MOD` by zero; `not_a_number` for a non-finite
  result such as `0 ^ -1`, `SQRT(-1)` or `LN(0)`; `bad_text`;
  `out_of_range` for dates, `INDEX` and negative lengths or positions in
  text functions; `length_mismatch`; `too_many_elements` for the composite
  caps. ADR-0020 adds the codes of the query functions. A new code needs a
  conformance case.
- NaN and infinity never exist as values; the operations that can produce
  them are the ones listed above, and the VM checks their results.
- What an error means to its caller: a commit whose auto recalculation
  errors fails as a whole and reports the variable and the code; a
  triggered recomputation that errors leaves the stored value unchanged and
  records the error with its moment; a guard, rule or access rule that
  errors evaluates as FALSE and the error is logged, so an error can never
  open a transition or grant access.
- The SQL generator emits a value expression and, when the formula contains
  an operation that can fail, an error expression yielding the code or
  NULL, nested in the same `CASE` structure as the value so that untaken
  branches cannot fail. The value expression is NULL where the VM errors.
  The set-wise runner reads both, and the differential test checks the
  value on success and the code on error, so the SQL path never turns an
  error into a silent blank.

### One translation per dialect

Each rule above has one translation per dialect, recorded in the catalog
next to the function so that it can be audited. Where a dialect's built-in
function differs from the rule, the DuckDB dialect rewrites in native SQL
only, so that set-wise recomputes stay vectorised. The SQLite dialect may
instead register the engine's Go implementation as a SQL function, which
`modernc.org/sqlite` supports, and which guarantees agreement with the VM
for functions SQLite lacks, such as Unicode `UPPER` and the `VALUE` grammar.

## Consequences

- Static typing rejects at definition time what Excel reports as `#VALUE!`
  at run time, and the editor can show the error before the class is saved.
- Dates without operators cost a function call where Excel users would
  write `+ 1`; the error message names the function.
- Case-sensitive text is a visible difference from Excel. The function
  reference says so wherever text is compared.
- The lite build's SQL path runs the registered Go functions once per row;
  the DuckDB path never calls into Go.
- Aggregates can differ in the last bits between evaluators; the suite
  says so by construction instead of hiding it with a global tolerance.

## Performance

Budgets from ADR-0012: the five-operation scalar formula under 100 ns with
zero allocations is unaffected, because every rule above is decided at bind
time and the VM loop checks only blanks and the listed failing operations.
Text functions allocate by nature and get their own benchmarks with
`-benchmem` when the catalog lands. The Go functions registered into SQLite
cost a Go call per row and are measured in the Phase 2 query benchmarks.
