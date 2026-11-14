# Look a value up in a reference table

A quote is made in a foreign currency, and its value in złoty follows
from the exchange rate. The rates are reference data: a list kept
beside the documents, which one team maintains and every quote reads.

## The table and the class

A **table** is a list of rows with a key. It is defined once, like a
class:

```json
{
  "name": "Rates",
  "columns": {"code": "text", "rate": "decimal(4)"},
  "key": ["code"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}
```

The class reads it with `LOOKUP`:

```json
{
  "name": "Quote",
  "title": "\"Quote in \" & currency",
  "variables": {
    "currency": {"type": "text", "required": true},
    "amount":   {"type": "decimal", "scale": 2, "required": true},
    "rate":     {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"},
    "in_pln":   {"formula": "ROUND(amount * rate, 2)"}
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

`LOOKUP(Rates, rate, code = THIS.currency)` reads: from the table
`Rates`, the column `rate`, of the row whose `code` equals this
document's `currency`. Inside the condition a bare name is a column of
the table, and `THIS.` reaches the document.

## What it does

Tess is in the group `treasury`. The table has EUR at 4.3000 and USD
at 3.9000.

| Who | Does | Answer |
|---|---|---|
| Ann | writes a rate into the table | refused, `forbidden` at `/access/write` |
| Ann | creates a quote of 100.00 in EUR | `rate` 4.3000, `in_pln` 430.00 |
| Tess | changes the EUR rate to 4.5000 | allowed |
| Ann | reads her quote | still `rate` 4.3000, `in_pln` 430.00 |
| Ann | changes the amount to 200.00 | still `rate` 4.3000, `in_pln` 860.00 |
| Ann | changes the currency to USD | `rate` 3.9000, `in_pln` 780.00 |

## How it works

- **The value looked up is kept with the document.** A quote keeps
  the rate it was made with. That is usually what a business wants: a
  quote does not change because the market moved.
- **It is looked up again when a value the lookup reads changes**,
  here the currency, and not when another value changes, nor when the
  table does.
- **A lookup that finds nothing is blank**, and a blank counts as zero
  in arithmetic: a quote in a currency the table does not have gets
  `in_pln` 0.00. Guard the next transition with `NOT ISBLANK(rate)`
  where that matters.
- **A table has its own two lines of who may**: `read` and `write`,
  each about the table as a whole.
- **A row written with a key the table has replaces that row.**

## Variations

| To | Write |
|---|---|
| count, add up, or take the smallest or largest of the rows that match | `LOOKUP(Stock, SUM(qty), sku = THIS.sku)`, and `COUNT`, `MIN`, `MAX` the same way |
| read the documents of another class in the place of a table | `LOOKUP(Approval, COUNT(id), purchase = ID() AND state = "granted")`, in a triggered variable, a default, a guard or a rule |
| take the newest rate at a moment you choose | make the variable triggered, with `"mode": "triggered"` and `"on": ["refresh"]`, and refresh the document |

See also: [The functions](../functions.md), and the Purchase sample
under [examples/classes](../../../examples/classes/README.md) for a
triggered variable that counts other documents.
