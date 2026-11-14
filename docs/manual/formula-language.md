# The formula language

A formula is an expression in the style of a spreadsheet. The same
language is used wherever a class computes or decides something: a
computed variable, a default, a guard, a line of `access`, a right,
the condition and the values of a rule, the time of a transition, a
title. This chapter is the reference for the language itself. Every
example in it is worked out by the engine on each push, and the answer
and its type are compared with what stands here.

The functions are in [The functions](functions.md). The idea of a
variable and a formula is in
[Variables and formulas](notions/variables-and-formulas.md).

## The document of the examples

The examples read the variables of this class. An object names a
schema, which is defined before the class.

```json
{
  "name": "Depot",
  "fields": {"city": "text", "zip": "text"}
}
```

```json
{
  "name": "Sample",
  "variables": {
    "n":      {"type": "number"},
    "zero":   {"type": "number"},
    "empty":  {"type": "number"},
    "word":   {"type": "text"},
    "note":   {"type": "text"},
    "day":    {"type": "date"},
    "moment": {"type": "datetime"},
    "net":    {"type": "money", "scale": 2},
    "rate":   {"type": "decimal", "scale": 2},
    "count":  {"type": "int"},
    "paid":   {"type": "bool"},
    "depot":  {"type": "object", "schema": "Depot"},
    "tags":   {"type": "array<text>"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  }
}
```

The document has these values. `empty` and `note` are not given, so
they are blank.

```jsonc
{
  "n": 10,
  "zero": 0,
  "word": "Żółw",
  "day": "2027-01-31",
  "moment": "2027-01-31 14:30:00",
  "net": "100.00 PLN",
  "rate": "0.23",
  "count": 3,
  "paid": true,
  "depot": {"city": "Gdańsk", "zip": "80-001"},
  "tags": ["new", "urgent"]
}
```

In the tables below an answer is written as the engine gives it: a
text in quotes, TRUE or FALSE, a number, an exact value with all its
places, a day as `2027-01-31`, a moment as `2027-01-31 14:30:00`.
"refused" means the class is not accepted when it is defined, with
that code. "fails" means the class is accepted and the formula cannot
be worked out for these values, with that code.

## Writing a formula

- **Names are written in any case.** `N` is the variable `n`, and
  `Upper` is `UPPER`. Text is not: `"abc"` and `"ABC"` differ.
- **Text stands in double quotes.** A quote inside it is written
  twice.
- **Numbers** are written `12`, `1.5`, `.5` or `1e3`.
- **`TRUE`, `FALSE` and `BLANK`** are words of the language.
- **There is no way to write a day directly.** `DATE(2027, 1, 31)`
  makes one, and `DATEVALUE("2027-01-31")` reads one from text.
- **A variable may have the name of a function.** `count` here is a
  variable, and `COUNT(...)` is still the function.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `N` | 10 | number |
| `Upper(word)` | "ŻÓŁW" | text |
| `1e3` | 1000 | number |
| `.5` | 0.5 | number |
| `"a" & "b"` | "ab" | text |
| `DATE(2027, 1, 31)` | 2027-01-31 | date |
| `count` | 3 | int |

## Operators

From the one that binds tightest to the one that binds loosest:

| Operator | Meaning |
|---|---|
| `%` after a number | a hundredth: `15%` is 0.15 |
| `-` before a value | the negative |
| `^` | to the power of |
| `*`, `/` | times, divided by |
| `+`, `-` | plus, minus |
| `&` | joins two values into a text |
| `=`, `<>`, `<`, `<=`, `>`, `>=` | compares |
| `NOT` | not |
| `AND` | both |
| `OR` | either |

Operators of the same strength work from left to right, except `^`,
which works from right to left. Brackets change the order.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `2 + 3 * 4` | 14 | number |
| `(2 + 3) * 4` | 20 | number |
| `-2 ^ 2` | 4 | number |
| `2 ^ 3 ^ 2` | 512 | number |
| `n * 15%` | 1.5 | number |
| `7 / 2` | 3.5 | number |
| `n > 5 AND paid` | TRUE | bool |
| `NOT paid` | FALSE | bool |
| `paid AND NOT paid OR paid` | TRUE | bool |
| `n <> 10` | FALSE | bool |

## Conditions and IF

`IF` has two forms that mean the same. Only the branch that is taken
is worked out, so a formula can step around a failure.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `IF(n > 5, "big", "small")` | "big" | text |
| `IF n > 5 THEN "big" ELSE "small"` | "big" | text |
| `IF(zero = 0, 0, n / zero)` | 0 | number |
| `OR(n > 50, paid)` | TRUE | bool |
| `IF(n, 1, 2)` | refused: `bad_formula` | |

The last one is refused because a number is not a condition. Write
`IF(n <> 0, 1, 2)`.

## Types do not mix by themselves

Every value has a type, and the engine checks the types when the
class is defined. It never turns a text into a number, or a moment
into a day, without being asked.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `n = "10"` | refused: `bad_formula` | |
| `n + "1"` | refused: `bad_formula` | |
| `VALUE("12.5") + 1` | 13.5 | number |
| `n & " pcs"` | "10 pcs" | text |
| `TEXT(n)` | "10" | text |
| `paid = 1` | refused: `bad_formula` | |
| `day + 1` | refused: `bad_formula` | |
| `ADDDAYS(day, 1)` | 2027-02-01 | date |
| `moment > day` | refused: `bad_formula` | |
| `DATEOF(moment) > day` | FALSE | bool |

`&` is the one place where anything is turned into text by itself.
The refusal of each of the others says which function converts.

## Blank

A variable with no value is blank. In a formula a blank counts as the
nothing of its kind: zero in arithmetic and in a comparison with a
number, empty text in `&` and in a comparison with a text. `ISBLANK`
is the only way to tell a blank from that.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `empty + 1` | 1 | number |
| `empty * n` | 0 | number |
| `empty = 0` | TRUE | bool |
| `ISBLANK(empty)` | TRUE | bool |
| `ISBLANK(zero)` | FALSE | bool |
| `word & note` | "Żółw" | text |
| `note = ""` | TRUE | bool |
| `IF(ISBLANK(note), "none", note)` | "none" | text |

## Text

Text is compared exactly, capitals included, and capitals sort before
small letters. Lengths and positions count characters, and the first
character is at position 1.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `"abc" = "ABC"` | FALSE | bool |
| `UPPER("abc") = UPPER("ABC")` | TRUE | bool |
| `"a" < "b"` | TRUE | bool |
| `"B" < "a"` | TRUE | bool |
| `LEN(word)` | 4 | number |
| `LEFT(word, 2)` | "Żó" | text |
| `MID(word, 2, 2)` | "ół" | text |
| `RIGHT(word, 2)` | "łw" | text |
| `FIND("ł", word)` | 3 | number |
| `LOWER(word)` | "żółw" | text |
| `SUBSTITUTE(word, "ół", "o")` | "Żow" | text |
| `TRIM("  a  ")` | "a" | text |

## Turning values into text and back

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `TEXT(0.1 + 0.2)` | "0.3" | text |
| `TEXT(1 / 3)` | "0.333333333333333" | text |
| `TEXT(day)` | "2027-01-31" | text |
| `TEXT(moment)` | "2027-01-31 14:30:00" | text |
| `TEXT(paid)` | "TRUE" | text |
| `TEXT(net)` | "100.00 PLN" | text |
| `VALUE(" 12.5 ")` | 12.5 | number |
| `VALUE("abc")` | fails: `bad_text` | |
| `DATEVALUE("2027-02-01")` | 2027-02-01 | date |
| `DATEVALUE("1/2/2027")` | fails: `bad_text` | |

`TEXT` of a number gives fifteen significant digits, which is why
0.1 + 0.2 reads "0.3".

## Plain numbers

A `number` is a number as a spreadsheet keeps it: fast, and not exact
in its last digits.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `n / 3` | 3.3333333333333335 | number |
| `0.1 + 0.2` | 0.30000000000000004 | number |
| `0.1 + 0.2 = 0.3` | FALSE | bool |
| `ROUND(n / 3, 2)` | 3.33 | number |
| `ROUND(2.5)` | 3 | number |
| `ROUND(-2.5)` | -3 | number |
| `ROUND(2.5, 0, "half_even")` | 2 | number |
| `MOD(7, 2)` | 1 | number |
| `MAX(n, 3)` | 10 | number |
| `SUM(1, 2, 3)` | 6 | number |

## Exact numbers

`int`, `decimal` and `money` keep every digit. A decimal and a money
have a scale, the number of places after the point, and the type of a
result says how many it has.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `count + 1` | 4 | int |
| `count / 2` | 1.5000 | decimal(4) |
| `rate + 0.1` | 0.33 | decimal(2) |
| `rate + 0.1 = 0.33` | TRUE | bool |
| `count * rate` | 0.69 | decimal(2) |
| `net * 2` | 200.00 PLN | money(2) |
| `net * rate` | 23.0000 PLN | money(4) |
| `ROUND(net * (1 + rate), 2)` | 123.00 PLN | money(2) |
| `net / 3` | 33.333333 PLN | money(6) |
| `ROUND(net / 3, 2)` | 33.33 PLN | money(2) |
| `net / net` | 1.000000 | decimal(6) |
| `-net` | -100.00 PLN | money(2) |
| `AMOUNT(net)` | 100.00 | decimal(2) |
| `CURRENCY(net)` | "PLN" | text |

The rules behind the table:

- **Adding keeps the larger scale. Multiplying adds the scales.
  Dividing gives four places more than the value divided.** `ROUND`
  brings a result back.
- **A number written in the formula is read exactly.** `rate + 0.1`
  is exact.
- **Money carries its currency**, and one operation takes one
  currency.
- **An exact value and a `number` variable do not mix.** One of them
  has to be converted: `NUMBER(x)` makes an exact value plain, and
  `DECIMAL(x, places)` makes a plain one exact.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `net + MONEY(5, "PLN")` | 105.00 PLN | money(2) |
| `net + MONEY(5, "EUR")` | fails: `currency_mismatch` | |
| `net > MONEY(50, "PLN")` | TRUE | bool |
| `net * net` | refused: `bad_formula` | |
| `count + n` | refused: `bad_formula` | |
| `NUMBER(count) + n` | 13 | number |
| `rate * n` | refused: `bad_formula` | |
| `DECIMAL(n / 3, 2)` | 3.33 | decimal(2) |

## Days and moments

A `date` is a calendar day. A `datetime` is a moment, in UTC. There is
no arithmetic operator on either: functions move them and measure
between them.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `ADDDAYS(day, 1)` | 2027-02-01 | date |
| `ADDMONTHS(day, 1)` | 2027-02-28 | date |
| `ADDYEARS(DATE(2028, 2, 29), 1)` | 2029-02-28 | date |
| `DAYS(DATE(2027, 3, 1), day)` | 29 | number |
| `YEAR(day)` | 2027 | number |
| `MONTH(day)` | 1 | number |
| `DAY(day)` | 31 | number |
| `QUARTER(day)` | 1 | number |
| `day > DATE(2027, 1, 1)` | TRUE | bool |
| `DATE(2027, 14, 1)` | 2028-02-01 | date |
| `DATE(2027, 3, 0)` | 2027-02-28 | date |
| `ADDDAYS(moment, 0.5)` | 2027-02-01 02:30:00 | datetime |
| `DATEOF(moment)` | 2027-01-31 | date |
| `DATE(10000, 1, 1)` | fails: `out_of_range` | |

- `ADDMONTHS` and `ADDYEARS` keep the day of the month and stop at the
  last day of a shorter month.
- `DATE` carries over: month 14 is February of the next year, and day
  0 is the last day of the month before.
- `DAYS(a, b)` counts the calendar days from `b` to `a`.
- A document answers a moment as seconds since 1970; `TEXT` writes it
  as above.

## Objects

A variable of type `object` holds a value with named fields. A formula
reads one field with a dot.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `depot.city` | "Gdańsk" | text |
| `depot.city & " " & depot.zip` | "Gdańsk 80-001" | text |
| `ISBLANK(depot.city)` | FALSE | bool |
| `depot` | refused: `bad_formula` | |

> **Status.** A formula reads the fields of an object, and not the
> object as a whole. It cannot read a list or rows yet either: a
> variable of type `array<...>` or `rows` is stored and answered with
> the document, and a formula that names it is refused.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `COUNT(tags)` | refused: `bad_formula` | |

The functions that work on lists are in the language and are marked
in [The functions](functions.md) where they are not built yet.

## When a formula fails

Some formulas cannot be worked out for some values.

<!-- examples -->
| Formula | Answer | Type |
|---|---|---|
| `n / zero` | fails: `div_by_zero` | |
| `SQRT(-1)` | fails: `not_a_number` | |
| `VALUE("abc")` | fails: `bad_text` | |
| `LEFT(word, -1)` | fails: `out_of_range` | |
| `net + MONEY(5, "EUR")` | fails: `currency_mismatch` | |
| `IFERROR(n / zero, -1)` | -1 | number |
| `IFERROR(VALUE("abc"), 0)` | 0 | number |

`IFERROR(formula, otherwise)` gives the second value when the first
fails. The other codes a formula can fail with are in
[The refusals](refusals.md).

What a failure means depends on where the formula stands:

| Where | A failure |
|---|---|
| a computed variable, a default | refuses the commit, with the variable and the code |
| a value a rule sets, an argument of an action | refuses the commit, with the place of the action |
| a guard, a line of `access`, a right, the `if` of a rule | counts as FALSE |

So a failure never lets anybody through, and never leaves a value in a
document that could not be worked out.

## Where a formula stands

A formula may read different things in different places.

| Place | Reads the document | Reads the clock, the caller and the life of the document |
|---|---|---|
| a computed variable, a title | yes | no |
| a default | yes | yes |
| a guard, a line of `access`, a right | yes | yes |
| the `if` and the values of a rule | yes | yes |
| the `at` of a transition | yes | the life of the document only |

A computed variable stays true until one of its inputs changes, so it
may not read anything that changes by itself. The kinds of function,
and which kind may stand where, are in
[The functions](functions.md#where-a-function-may-stand).

## The same answer everywhere

A formula is worked out in two places today: in the engine, for a
document, and in SQL, when a filter or a rule of `access` is asked of
many documents at once. Both give the same answer, digit for digit.
The suites under [conformance](../../conformance/README.md) are the
specification of the language: every case runs on both, and they must
agree. The browser will be the third place, with Phase 7.
