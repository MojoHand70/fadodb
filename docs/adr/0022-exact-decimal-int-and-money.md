# ADR-0022: Exact numbers: decimal, int and money

- Status: Proposed; amends ADR-0004, ADR-0013, ADR-0018, ADR-0019
- Date: 2026-11-06

## Context

ADR-0004 made every number a float64 and deferred a decimal type past 1.0,
for the sfmt engine's float-only VM and for the native double in every
target. That record was written in the Phase 0 planning session and never
put to the maintainer as a choice. An invoice total computed in float64
will not be taken seriously by anyone who keeps books: 0.10 + 0.20 is not
0.30, a sum of a million cents drifts, and a silent rounding somewhere in
a chain of formulas is a defect of the first order in that market. The
first embedder keeps money as integer minor units and refuses floats
anywhere, and it is the market FadoDB is built for.

A type is a public contract: it is a column of the history table
(ADR-0014), a field of the wire shape (ADR-0018) and a slot type of the
bytecode (ADR-0003). The cheapest moment to add one is before the first
document is stored, which is now, before Phase 2.

Arbitrary-precision decimals cost an allocation per value and ten times
the VM budget. A fixed-point int64 costs nothing the float path pays: it
fits the value struct, the stack stays an array, and SQLite, DuckDB and
Postgres all hold it as an integer.

## Decision

- Three exact types join the scalar kinds of ADR-0013: `decimal(s)`, an
  int64 mantissa at a scale `s` of 0 to 18 digits after the point; `int`,
  which is `decimal(0)`; and `money(s)`, a decimal with a currency code,
  three upper-case letters. `decimal` alone is `decimal(2)` and `money`
  alone is `money(2)`. `number` stays float64 under ADR-0004 for the
  embedders that want a spreadsheet's arithmetic.
- The two number families never mix implicitly. A `number` variable in an
  exact expression, or an exact variable in `^`, `SQRT`, `POWER`, `EXP`,
  `LN` or `LOG`, is a definition-time error. `DECIMAL(x, places[, method])`
  makes a number exact and `NUMBER(x)` does the reverse.
- A number constant that meets an exact operand is read exactly from its
  spelling: `price * 1.23` reads 1.23 as `decimal(2)`, `23%` as 0.23 and
  `(1 - 10%)` as 0.90. Constants fold at definition time; a division or a
  power of constants is a number, not an exact constant. A constant that
  needs more than 18 digits after the point, or more than 18 digits in all,
  is refused.
- Arithmetic is exact or stops with the error `overflow`. `+` and `-` land
  on the larger scale; `*` on the sum of the scales, which may not exceed
  18; `%` adds two places; `/` lands on the dividend's scale plus four, at
  most 18, rounding half away from zero, the rule MySQL uses. Every
  intermediate must fit an int64: the operands brought to the result's
  scale, the product, the dividend scaled for the division. A bool reads as
  1 or 0 and a blank as 0 of the result's scale.
- Money: money plus or minus money or a decimal is money; money times a
  decimal is money; money divided by a decimal is money and by money a
  decimal; money times money and a decimal divided by money are
  definition-time errors. The money operands of one operation, comparison
  or aggregate share a currency or the result is the error
  `currency_mismatch`. A blank money operand defers to the other's currency;
  a money result with no currency, every money operand having been blank,
  is blank. Money compares with a decimal by its amount.
- Rounding: `ROUND(x, places[, method])`, `FLOOR`, `CEILING`, `TRUNC` and
  `DECIMAL` take the places as a whole number written in the formula, since
  they decide the result's scale at definition time, and a method written
  in the formula as one of `half_up` (the default, a half away from zero),
  `half_even`, `down`, `up`, `floor`, `ceiling`. Negative places round to
  tens and hundreds and give an int. `ROUND` on a number takes the same
  methods.
- Functions: `MIN`, `MAX`, `SUM`, `AVERAGE`, `MOD`, `ABS` and `SIGN` run on
  either family, all arguments of one family, exact results at the largest
  scale, `AVERAGE` four places further. `MONEY(amount, currency)`,
  `MONEYVALUE(text, places)`, `DECIMALVALUE(text, places)`, `AMOUNT(money)`
  and `CURRENCY(money)` build and take apart. `TEXT` renders every digit of
  the scale, `0.30` not `0.3`, and money as the amount, a space and the
  code: `12.30 PLN`, which `MONEYVALUE` reads back. A text with more places
  than asked is the error `inexact`.
- Storage and interchange: a `decimal` column is its mantissa as an
  integer, a `money` variable two columns, the mantissa and the currency
  under the name with the suffix `_currency`. On the wire an exact value is
  text, `"12.30"`, and money `"12.30 PLN"`. The browser VM carries them as
  `BigInt` (ADR-0029). A value stored into a variable must fit its scale; a
  formula whose result scale exceeds the variable's is refused at
  definition time unless it rounds.
- The bytecode gains an exact constant pool and seven opcodes, and the
  format version becomes 2. The SQL dialects compute in the engine's
  integer type, HUGEINT on DuckDB with narrowing checks, INTEGER on SQLite
  with its overflow-to-REAL detected; division, rounding, parsing and
  rendering go through registered functions and macros that take the VM's
  steps one for one.

## Consequences

- An invoice line is `ROUND(qty * price * (1 - discount%), 2)` and the
  total is exact. A third of 1.00 cannot be stored into a two-place money
  without saying how to round it, which is the property the market asks
  for.
- Authors meet two kinds of number and the binder tells them which one they
  have. The hints name `DECIMAL`, `NUMBER` and `AMOUNT`. The conformance
  suite gains the `decimal`, `money` and `exact` suites, and the property
  campaign generates exact and money formulas; the SQL dialects have to
  mirror every alignment and every overflow check, which the campaign
  proved by finding where they did not.
- `number` and the predecessor engine's rules stand unchanged for the
  embedders that want them. The division-by-zero rule of ADR-0004 changes
  under ADR-0024 for both families.
- Eighteen digits is the range: about 92 quadrillion at two places, nine
  billion at nine. Products of two four-place values use eight; a chain of
  products needs a rounding step, which the binder asks for. Currency
  minor-unit tables are not the engine's; a class declares the scale a
  variable keeps, two for most currencies, zero for a yen variable.
- Comparisons align like subtraction, so comparing a seventeen-digit
  integer with a sixteen-place decimal is an overflow, on every evaluator
  alike.

## Performance

The value struct stays four words: the mantissa lives in the bits of the
number field, the scale in the padding beside the kind, so the float path
is untouched and the five-operation float formula keeps its 100 ns and
zero allocations; the exact instructions run out of the instruction loop
so that the float path's registers stay in it. Exact `+`, `-` and `*` are
integer operations with an overflow check, exact `/` a 64-bit division.
Measured on the development machine on 2026-11-07 beside the 13.6 ns of
the float formula: the five-operation exact formula `a * b + c / d - e`
on `decimal(2)` runs in 35 ns and the invoice line
`ROUND(qty * price * (1 - discount%), 2)` on `money(2)` in 38 ns, both
with zero allocations; the budget of ADR-0012 is 100 ns. The binder pays
about 7% more per formula for the constant folding and the typing rules,
a definition-time cost. The SQL dialects pay a registered function call
per exact division and rounding on SQLite, which the Phase 2 query
benchmarks measure, and inline HUGEINT arithmetic on DuckDB.
