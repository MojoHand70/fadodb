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
evaluation case in CI: the VM and SQLite exactly, DuckDB exactly except
aggregates, which may differ by the relative 1e-12 of ADR-0019 because
DuckDB sums without compensation. The browser VM does not exist yet.
Composite values (arrays and rows) and the query functions are not
implemented yet; the binder refuses them, and the binding suite says so.

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
  `number`, `text`, `date`, `ref`, `json`, `array<scalar>`, `rows`.
- `inputs` gives values; an absent or `null` variable is blank. Dates are
  written `YYYY-MM-DD` or `YYYY-MM-DD HH:MM:SS`, in UTC.
- `context` sets what the context functions see: `user`, `groups`, `now`.
- Exactly one of `expected` and `error`. `expected` is the value in the
  notation of `inputs`; `null` is a blank result. `error` is one of the
  codes in ADR-0019 and ADR-0020.
- `type` pins the static type of the formula. A date result must declare
  `"type": "date"` so that `expected` can be written as text.
- `limits` sets the size limits the suite evaluates under, `{"max_text":
  N}` code points (ADR-0021); absent, the defaults. The `limits` suite runs
  at a small limit so that short texts reach the boundary.

Numbers compare exactly, except that aggregates over floating-point values
compare to a relative 1e-12 across targets (ADR-0019).

## Binding cases: `binding/*.json`

Formulas the binder must accept and formulas it must refuse at definition
time by the static typing rules of ADR-0019, in a `mode` of `any` (the
default) or `auto` (ADR-0015). Same shape as the syntax cases, plus `mode`.

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
