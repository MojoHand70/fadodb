# Conformance suite

The suite is the specification of the formula language (ADR-0003). A
behaviour that is not in the suite is not promised. Every target, the Go
VM, the SQL generator on each dialect and the browser VM, runs the same
cases in CI; a frontend author can run them against their own port.

Semantics are fixed in ADR-0004 (numbers), ADR-0005 (blanks), ADR-0013
(types), ADR-0019 (grammar, coercion, comparison, dates, text, composites,
errors) and ADR-0020 (LOOKUP and QUERY). The function catalog is
`internal/formula/catalog.json`.

## Status

The Go VM, the SQL generator and the browser VM do not exist yet. Today the
evaluation cases are checked to be well formed and to parse; the syntax
cases run against the parser. Evaluation starts with the VM.

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

Numbers compare exactly, except that aggregates over floating-point values
compare to a relative 1e-12 across targets (ADR-0019).

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
