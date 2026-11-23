# Live variables

A computed variable is kept: its value is stored with the document,
and every version of the document has it. Some values should not be
kept. "How many days has this invoice waited" is wrong the day after
it was stored. "Did I write this" has a different answer for each
reader.

A **live variable** is a computed variable that is worked out when
somebody reads it, for that reader, and kept nowhere.

This chapter is the reference for live variables. A test defines what
stands here and plays it on each push.

## Three kinds of computed variable

| | A computed variable | A triggered variable | A live variable |
|---|---|---|---|
| Written with | `formula` | `formula`, `"mode": "triggered"`, `on` | `formula`, `"mode": "live"` |
| Worked out | whenever a value it reads changes | when one of its triggers fires | when it is read |
| In between | it follows the values it reads | it keeps its value | there is no in between: nothing is kept |
| Whose answer it is | everybody's | everybody's | the reader's, at that moment |
| Is part of the document and its history | yes | yes | no |
| A guard, a rule or a kept value may read it | yes | yes | no |

The first two are in [The class definition](class.md) and in
[Triggered variables](triggered-variables.md).

## The class

Acme's invoices. Jan and Piotr are in sales, and each reads the
invoices he wrote. Ewa is in finance and reads every invoice.

```json
{
  "name": "Invoice",
  "label": "Invoice",
  "description": "What Acme asks a customer to pay.",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true, "label": "Number",
               "description": "The number on the invoice."},
    "owner":  {"type": "text", "default": "USER()", "label": "Owner",
               "description": "Who wrote it."},
    "net":    {"type": "money", "scale": 2, "label": "Net amount",
               "description": "The amount before VAT."},
    "waiting_days": {"label": "Days waiting", "mode": "live",
                     "formula": "DAYS(TODAY(), DATEOF(ENTEREDAT()))",
                     "description": "How many days it has been in its state."},
    "mine":   {"type": "bool", "label": "Mine", "mode": "live",
               "formula": "owner = USER()",
               "description": "Whether the reader wrote it."},
    "shout":  {"formula": "UPPER(number)", "label": "Number in capitals",
               "description": "The number, in capitals."}
  },
  "states": {
    "initial": "review", "list": ["review", "paid"], "final": ["paid"],
    "descriptions": {"review": "Finance looks at it.", "paid": "The customer paid."}
  },
  "transitions": [
    {"name": "pay", "from": "review", "to": "paid", "label": "Record payment",
     "description": "Finance records that the customer paid."}
  ],
  "access": {
    "read": "owner = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"pay": "INGROUP(\"finance\")"}
  },
  "rights": {
    "review": {"mine": {"read": ["sales"]}}
  }
}
```

`waiting_days` and `mine` are live. `shout` is an ordinary computed
variable, kept with the document.

| Word | Meaning |
|---|---|
| `formula` | what the variable is worked out by |
| `mode` | `live` |
| `type` | the type of the value. It may be left out, and is then the formula's |
| `label`, `description`, `format` | what the variable says to people, as for any variable |

## What a live formula may read

The values its document holds, the caller, the clock, the state, and
what the life of the document says. So it may ask who is reading
(`USER()`, `INGROUP()`), what day it is (`TODAY()`), and since when the
document has been in its state (`ENTEREDAT()`).

A live variable may also be a list of other documents, "the invoices
of this order": its whole formula is then a `QUERY`. That has a
chapter of its own, [Lists of other documents](lists.md).

**Not built yet:** a live variable that looks one value up, with
`LOOKUP`. It is decided (ADR-0076). Today the class is refused with
`not_supported`.

## The story

Jan writes invoice FV-17 on 1 December 2026.

<!-- the story -->
| On | Who asks | For | The answer |
|---|---|---|---|
| 1 December | Jan | `waiting_days` | 0 |
| 1 December | Jan | `mine` | true |
| 1 December | Ewa | `waiting_days` | 0 |
| 10 December | Ewa | `waiting_days` | 9 |
| 10 December | Jan | `waiting_days` | 9 |

Nobody changed the invoice between 1 and 10 December. It is still at
version 1, and its history has one line. The answer changed because
the day changed.

Reading the invoice itself, with `get` or `query`, answers the kept
values and no live one:

<!-- what the document carries -->
| Variable | In the document |
|---|---|
| `number` | yes |
| `owner` | yes |
| `net` | yes |
| `shout` | yes |
| `waiting_days` | no |
| `mine` | no |

## Reading one

A live value has a command of its own, `live`. In Go it is the call
`Live`.

<!-- the command -->
```jsonc
{"class": "Invoice", "id": "FV-17", "variable": "waiting_days"}
```

It answers the value, in the shape its type has on the wire:

<!-- the answer -->
```jsonc
{"value": 9}
```

## Who may read it

Whoever may read the document, and the variable by its read right in
the document's state. In the class above `mine` is for sales while
the invoice is in review.

<!-- who may not -->
| Who asks | For | The answer |
|---|---|---|
| Piotr, who may not read Jan's invoice | `waiting_days` | `unknown_document` |
| Ewa, who is not in sales | `mine` | `forbidden` |
| Jan | `shout`, which is kept | `not_allowed` |
| Jan | `nothing`, which the class has not | `unknown_variable` |
| Jan, of an invoice that is not there | `waiting_days` | `unknown_document` |

A document the caller may not read is, to them, not there: the answer
is the same as for an invoice that does not exist.

## What a live variable may not say

It is kept nowhere, so what speaks of a kept value has no place on
it.

<!-- what it may not say -->
| Written on a live variable | The class is refused with |
|---|---|
| `on` | `not_allowed` |
| `default` | `not_allowed` |
| `required` | `not_allowed` |
| `values` | `not_allowed` |
| `min` | `not_allowed` |
| `max` | `not_allowed` |
| `index` | `not_allowed` |
| `list` | `not_allowed` |

## What may not read it

Nothing that is kept or decided. A decision stands on what the
document holds, so that what a guard saw can be read afterwards. A
value that is kept nowhere cannot be read afterwards.

<!-- what may not read it -->
| A live variable read by | The class is refused with |
|---|---|
| a computed variable | `not_allowed` |
| a triggered variable | `not_allowed` |
| a default | `not_allowed` |
| the title | `not_allowed` |
| a guard | `not_allowed` |
| an access rule | `not_allowed` |
| another live variable | `not_allowed` |
| the filter of `query` | `not_allowed` |
| a `set`, which would give it a value | `read_only` |

To decide on such a value, keep it: make the variable triggered, and
name the moment it is worked out.

## When the class changes

A class may turn a kept variable into a live one, or a live one into a
kept one, with a new version of its definition. A document is what the
version that last computed it made of it: one made before the change
still carries the value that was kept then, until a commit brings it to
the current version.
