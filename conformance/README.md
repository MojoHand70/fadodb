# Conformance suite

The suite is the specification of the formula language (ADR-0003). A
behaviour that is not in the suite is not promised. Every target, the Go
VM, the SQL generator on each dialect and the browser VM, runs the same
cases in CI; a frontend author can run them against their own port.

Semantics are fixed in ADR-0004 (numbers), ADR-0005 (blanks), ADR-0013
(types), ADR-0019 (grammar, coercion, comparison, dates, text, composites,
errors), ADR-0020 (LOOKUP and QUERY) and ADR-0021 (size limits). The
function catalog is `internal/formula/catalog.json`.

## Status

The Go VM and the SQL generator's SQLite and DuckDB dialects run every
evaluation case in CI, and all three must match exactly. SUM and AVERAGE
add left to right on every evaluator so that aggregates agree to the
last bit, rather than differing within what ADR-0019 allows them. The
browser VM does not exist yet.
Object field paths (ADR-0031) bind and evaluate on every target: a leaf
is one slot of the field's type. `LOOKUP` against an explicit table
(ADR-0016, ADR-0020, ADR-0037) binds and evaluates on every target: the
`lookup` suite declares its tables with their rows under `sources`, the
Go VM answers them from an in-memory executor and the SQL dialects from
the tables created in the database. Whole objects in a formula, `ISBLANK`
of an object, object equality and computed object variables are not
implemented yet, nor are the other composites, arrays and rows, and
`QUERY`; the binder refuses them, and the binding suite says so.

Known, documented differences of the SQL targets: `VALUE` and `DATEVALUE`
trim spaces only on DuckDB where the VM trims all whitespace; `UPPER` of
"ß" is "ẞ" on DuckDB and "ß" in the VM; an invalid `REGEXREPLACE` pattern
built at run time fails the statement on DuckDB instead of yielding
`bad_text`.

## Evaluation cases: `formula/*.json`

One file per topic:

```json
{
  "suite": "blank",
  "description": "A stored 0 is not blank.",
  "variables": {"Alfa": "number", "Nazwa": "text", "Due": "date"},
  "cases": [
    {"name": "blank reads as 0 in arithmetic",
     "formula": "Alfa + 1", "inputs": {"Alfa": null}, "expected": 1},
    {"formula": "DATE(2024, 2, 30)", "type": "date", "expected": "2024-03-01"},
    {"formula": "1 \\ 0", "error": "div_by_zero"}
  ]
}
```

- `variables` declares the inputs a case may set, by type: `bool`,
  `number`, `int`, `decimal(s)`, `money(s)`, `text`, `date`, `datetime`,
  `ref`, `json`, `array<scalar>`, `rows`, or `object<Name>` for a schema
  the suite declares.
- `schemas` declares object schemas (ADR-0031) by name: a field name to
  its type, where a field may be `object<Name>` of another schema. A
  formula reads a leaf as `shipping.city`; an input sets it under the
  same path, `"shipping.city": "Gdańsk"`.
- `inputs` gives values; an absent or `null` variable is blank. Dates are
  written `YYYY-MM-DD`, datetimes `YYYY-MM-DD HH:MM:SS` in UTC, exact
  values as text such as `"1.250"`, money as `"12.30 PLN"`.
- `context` sets what the context functions see: `user`, `groups`, `now`.
- Exactly one of `expected` and `error`. `expected` is the value in the
  notation of `inputs`; `null` is a blank result. `error` is one of the
  codes in ADR-0019 and ADR-0020.
- `type` pins the static type of the formula. A date result must declare
  `"type": "date"` so that `expected` can be written as text.
- `sources` declares explicit tables a `LOOKUP` may read (ADR-0020): by
  name, `columns` as name to scalar type, `key` as the columns that order
  the table and identify a row, and `rows` as objects in the notation of
  `inputs`, an absent column blank. Inside a `LOOKUP` a bare name is a
  column of the source and `THIS.name` a declared variable.
- `limits` sets the size limits the suite evaluates under, `{"max_text":
  N}` code points (ADR-0021); absent, the defaults. The `limits` suite runs
  at a small limit so that short texts reach the boundary.

Numbers compare exactly on every target.

## Binding cases: `binding/*.json`

Formulas the binder must accept and formulas it must refuse at definition
time by the static typing rules of ADR-0019, in a `mode` of `any` (the
default), `auto` (ADR-0015) or `pure` (the class title of ADR-0018 and
the presentation formulas of ADR-0032). Same shape as the syntax cases,
plus `mode` and, as in the evaluation cases, `schemas` and `sources`
(whose rows may be left out).

## Syntax cases: `syntax/*.json`

Formulas the compiler must accept and formulas it must refuse at definition
time. The reason on a rejected formula is for people; error messages are
not part of the contract.

```json
{
  "suite": "grammar",
  "variables": {"Alfa": "number"},
  "accept": ["Alfa ^ 2", "IF Alfa > 0 THEN 1 ELSE 2"],
  "reject": [{"formula": "Alfa ^", "reason": "missing operand"}]
}
```
