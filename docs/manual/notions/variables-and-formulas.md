# Variables and formulas

A document holds values, and each value belongs to a variable of its
class. Some a caller sets; the others the engine works out, as a
spreadsheet does. This chapter says how values are checked, how the
exact kinds of number differ from the plain one, what a blank is, and
what happens when a formula cannot be worked out.

## Two kinds of variable

| | An input | A computed variable |
|---|---|---|
| Written with | a `type` | a `formula` |
| Who sets it | a caller, or its `default` when the document is created | nobody: the engine works it out |
| When it changes | when a caller sets it | whenever a variable it reads changes, in the same commit |

## The class

An offer that a salesperson writes for a customer.

```json
{
  "name": "Offer",
  "title": "\"Offer for \" & customer",
  "variables": {
    "customer":    {"type": "text", "required": true},
    "owner":       {"type": "text", "default": "USER()"},
    "kind":        {"type": "text", "values": ["goods", "service"], "default": "\"goods\""},
    "quantity":    {"type": "int", "min": 0, "max": 1000, "default": "1"},
    "unit_price":  {"type": "money", "scale": 2},
    "discount":    {"type": "decimal", "scale": 2, "default": "0"},
    "issued":      {"type": "date", "default": "TODAY()"},
    "valid_days":  {"type": "int", "default": "30"},
    "weight":      {"type": "number"},
    "net":         {"formula": "ROUND(quantity * unit_price * (1 - discount), 2)"},
    "per_piece":   {"formula": "ROUND(net / quantity, 2)"},
    "valid_until": {"formula": "ADDDAYS(issued, NUMBER(valid_days))"},
    "label":       {"formula": "customer & \", \" & quantity & \" pcs\""},
    "heavy":       {"formula": "weight > 100"}
  },
  "states": {"initial": "draft", "list": ["draft", "sent"]},
  "transitions": [
    {"name": "send", "from": "draft", "to": "sent"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "FALSE"
  }
}
```

The first nine variables are inputs, the last five are computed.

## What it does

Krzysztof writes an offer on 16 November 2026.

| Step | Krzysztof | Answer |
|---|---|---|
| 1 | creates an offer for ACME: 3 pieces at 19.99 EUR | allowed |
| 2 | sets the discount to 0.15 | allowed: `net` is 50.97 EUR and `per_piece` 16.99 EUR |
| 3 | sets the quantity to 2000 | refused, `out_of_range`, variable `quantity` |
| 4 | sets the quantity to 1.5 | refused, `bad_value`, variable `quantity` |
| 5 | sets the kind to "rental" | refused, `not_in_values`, variable `kind` |
| 6 | sets the unit price to "19.99", with no currency | refused, `bad_value`, variable `unit_price` |
| 7 | sets the unit price to "19.999 EUR" | refused, `bad_value`, variable `unit_price` |
| 8 | sets `net` | refused, `read_only`, variable `net` |
| 9 | blanks the customer | refused, `required`, variable `customer` |
| 10 | blanks the discount | allowed: `net` is 59.97 EUR again |
| 11 | sets the quantity to 0 | refused, `div_by_zero`, variable `per_piece` |
| 12 | resets the discount | allowed: the discount is 0.00 again |

After step 1 the offer holds this. Krzysztof gave three of the
fourteen values.

<!-- the values after step 1 -->
| Variable | Value | Where it came from |
|---|---|---|
| `customer` | ACME | Krzysztof |
| `quantity` | 3 | Krzysztof |
| `unit_price` | 19.99 EUR | Krzysztof |
| `owner` | krzysztof | its default, `USER()` |
| `kind` | goods | its default |
| `discount` | 0.00 | its default |
| `issued` | 2026-11-16 | its default, `TODAY()` |
| `valid_days` | 30 | its default |
| `weight` | blank | nobody set it, and it has no default |
| `net` | 59.97 EUR | its formula |
| `per_piece` | 19.99 EUR | its formula |
| `valid_until` | 2026-12-16 | its formula |
| `label` | ACME, 3 pcs | its formula |
| `heavy` | FALSE | its formula; a blank weight counts as zero |

## Inputs are checked on the way in

A value that does not fit its variable is refused, and the document
stays as it was. The refusal names the variable.

| The value | Code |
|---|---|
| is not of the type: text for a number, a fraction for an `int`, a day that does not exist | `bad_value` |
| has more decimal places than the `scale` of the variable | `bad_value` |
| is money with no currency | `bad_value` |
| is below `min` or above `max` | `out_of_range` |
| is not one of the `values` | `not_in_values` |
| is blank, for a `required` variable | `required` |
| is given for a computed variable | `read_only` |

How a value of each type is written is in
[The class definition](../class.md#variables). Two things there matter
here: money is written as text with its currency, `"19.99 EUR"`, and a
date as `"2026-11-16"`.

## Exact numbers and plain numbers

FadoDB has two families of number, and they behave differently.

| | `int`, `decimal`, `money` | `number` |
|---|---|---|
| What it is | exact: every digit is kept | a number as a spreadsheet keeps it |
| For | amounts, rates, counts: anything that is booked or compared | measurements: a weight, a distance, a ratio |
| 0.1 + 0.2 = 0.3 | TRUE | FALSE: the sum is 0.30000000000000004 |

- **Money carries its currency.** Adding 10.00 PLN and 5.55 EUR is
  refused with `currency_mismatch`. Money times money is refused when
  the class is defined.
- **An exact result keeps all its places.** Multiplying adds the places
  of both sides: 10.00 PLN times 1.10 is 11.0000 PLN. Dividing gives
  four places more than the value divided: 10.00 PLN by 3 is
  3.333333 PLN. `ROUND` brings a result back to the places you want,
  which is why `net` and `per_piece` use it.
- **An exact value is not taken where a plain number is expected.**
  `ADDDAYS(issued, valid_days)` is refused when the class is defined,
  because `valid_days` is an `int`. `NUMBER(valid_days)` converts it,
  as `valid_until` does.

## Blank

A variable with no value is blank. In a document it is written `null`.

- **In a formula a blank counts as the nothing of its kind**: zero in
  arithmetic, empty text in `&`, FALSE in a condition. That is why
  `heavy` is FALSE for an offer with no weight, and why `net` in step
  10 is worked out as if the discount were zero.
- **`ISBLANK(x)` is the only way to tell a blank from a zero.**
- **A default does not come back.** Blanking the discount in step 10
  leaves it blank. Resetting a variable, as in step 12, gives it its
  default again.

## Defaults

A `default` is a formula, worked out once, when the document is
created and the caller gave no value. It may read who is calling and
what day it is, `USER()` and `TODAY()` here, and other variables of
the document.

A default is for new documents. A variable added to a class later is
blank on the documents that exist; see [Change a
class](../guides/change-a-class.md).

## Computed variables

- **They follow their inputs within the same commit.** In step 2 the
  discount changes, so `net` is worked out again, and then `per_piece`,
  which reads `net`. The engine finds the order itself.
- **Nobody sets one.** Step 8 is refused whoever asks.
- **They read the document, and nothing else.** A computed variable
  may not read the clock or the caller: `TODAY()`, `NOW()` and `USER()`
  are refused in one when the class is defined. Its value has to be the
  same for everybody and on every day until an input changes. What
  depends on the day or the caller goes into a default, a guard or a
  line of `access`.
- **They may not read each other in a circle.** That is refused with
  `cycle` when the class is defined.

A computed variable needs no `type`: it follows from the formula. When
one is given, it has to agree with the formula.

## Dates

A `date` is a calendar day, with no time of day and no time zone. A
`datetime` is a moment, in UTC.

- There is no `+` on a date. `ADDDAYS`, `ADDMONTHS` and `ADDYEARS` move
  one, and `DAYS(a, b)` counts the days from `b` to `a`.
- `ADDMONTHS` keeps the day of the month and stops at the last day of
  a shorter month: one month after 31 January 2027 is 28 February
  2027.

## Text

- `&` joins values into a text, as in `label`.
- Text is compared exactly, capitals included: `"abc" = "ABC"` is
  FALSE. Write `UPPER(a) = UPPER(b)` to compare without them.

## When a formula cannot be worked out

Some formulas fail for some values: a division by zero, a text that is
not a number.

- **In a computed variable the whole commit is refused.** Step 11 sets
  the quantity to 0, `per_piece` would divide by it, and the answer is
  `div_by_zero` with the variable `per_piece`. Nothing is written, so a
  document never holds a value that could not be worked out.
- **`IFERROR(formula, otherwise)` catches it** where a failure is
  expected: `IFERROR(ROUND(net / quantity, 2), BLANK)` leaves
  `per_piece` blank for an offer of nothing.
- **In a guard or a line of `access` a failure counts as FALSE.** A
  formula that fails never opens a transition and never lets anybody
  through.

## What is checked when the class is defined

The engine reads every formula before it accepts the class, so most
mistakes never reach a document.

<!-- checked when the class is defined -->
| The formula | Code |
|---|---|
| names a variable the class does not have | `bad_formula` |
| adds a number to a date | `bad_formula` |
| uses a number where a condition is needed | `bad_formula` |
| multiplies money by money | `bad_formula` |
| gives an `int` where a plain number is expected | `bad_formula` |
| reads the clock or the caller in a computed variable | `bad_formula` |
| is of another type than the variable says | `type_mismatch` |
| reads a variable that reads it back | `cycle` |

A variable may also not take a name the engine keeps for itself, such
as `state` or `id`: `reserved_name`.

Each finding names the place in the class, such as
`/variables/per_piece/formula`, and the engine reports all of them at
once.

## More

- The language itself, with every operator and what mixes with what:
  [The formula language](../formula-language.md).
- Every function: [The functions](../functions.md).
- Reading a value from a reference table or from other documents:
  [Look a value up](../guides/look-a-value-up.md).
- A variable worked out only at chosen moments, `"mode": "triggered"`:
  named in [The class definition](../class.md#variables); its own
  chapter is to come.

Next: [States and transitions](states-and-transitions.md).
