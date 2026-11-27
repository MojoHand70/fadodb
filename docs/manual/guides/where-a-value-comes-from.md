# Where a value comes from

A number on a document is often made of other documents' numbers,
which are made of others in turn, down to a number somebody typed or a
row of a table. One command, `lineage`, answers where a value comes
from, one level at a time: how it is made, when, and what it read,
with the values of the parts. A person clicks down; an agent asks again
for the part it wants to know more about.

## The case

Acme sells in region North. Ann and Piotr are in sales, and each reads
the invoices they wrote. Dan leads sales and reads every invoice. Tess,
in the treasury, keeps the table of rates.

<!-- the table -->
```json
{
  "name": "Rates",
  "columns": {"currency": "text", "rate": "decimal(4)"},
  "key": ["currency"],
  "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}
}
```

The invoice: Ann types the amount and the currency; the rate is the
table's at the moment she files it; `net` is worked out from the two.

<!-- the invoice -->
```json
{
  "name": "Invoice", "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "customer": {"type": "ref", "class": "Customer", "index": true},
    "owner":    {"type": "text", "default": "USER()"},
    "amount":   {"type": "decimal", "scale": 2, "required": true},
    "currency": {"type": "text", "required": true},
    "rate":     {"type": "decimal", "scale": 4, "mode": "triggered", "on": ["transition:file"],
                 "formula": "LOOKUP(Rates, rate, currency = THIS.currency)"},
    "net":      {"type": "decimal", "scale": 2, "formula": "ROUND(amount * rate, 2)"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file",    "from": "draft",  "to": "review"},
    {"name": "approve", "from": "review", "to": "paid"}
  ],
  "access": {
    "read":   "owner = USER() OR INGROUP(\"leads\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER() AND STATE() <> \"paid\"",
    "delete": "FALSE",
    "transition": {"file": "owner = USER()", "approve": "INGROUP(\"leads\")"}
  }
}
```

The customer keeps what its filed invoices add up to, worked out every
night at three:

<!-- the customer -->
```json
{
  "name": "Customer", "title": "name",
  "variables": {
    "name":     {"type": "text", "required": true},
    "region":   {"type": "ref", "class": "Region", "index": true},
    "invoiced": {"type": "decimal", "scale": 2, "mode": "triggered", "on": ["schedule:daily at 03:00", "refresh"],
                 "formula": "LOOKUP(Invoice, SUM(net), customer = REF() AND state <> \"draft\")"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"leads\")", "update": "INGROUP(\"leads\")", "delete": "FALSE"}
}
```

The region keeps what its customers add up to, at four:

<!-- the region -->
```json
{
  "name": "Region", "title": "name",
  "variables": {
    "name":  {"type": "text", "required": true},
    "sales": {"type": "decimal", "scale": 2, "mode": "triggered", "on": ["schedule:daily at 04:00", "refresh"],
              "formula": "LOOKUP(Customer, SUM(invoiced), region = REF())"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"leads\")", "update": "INGROUP(\"leads\")", "delete": "FALSE"}
}
```

A sum of sums is written this way: the middle level, the customer, is a
kept value. A live value is the reader's own and kept nowhere, so no
formula adds it up.

## What happened

All times are in UTC. Here the night's runs are a `refresh`, which
works a kept value out as its schedule does.

<!-- what happened -->
| When | Who | Does |
|---|---|---|
| 2 December, 9:00 | Tess | sets EUR to 4.2500 and PLN to 1.0000 |
| 2 December, 9:00 | Ann | writes INV-17 for Acme, 300.00 EUR, and files it: its rate is 4.2500, its net 1,275.00 |
| 2 December, 9:00 | Piotr | writes INV-18 for Acme, 2,000.00 PLN, and files it |
| 2 December, 9:00 | Ann | writes INV-19 for Borg, 500.00 EUR, and files it |
| 3 December, 3:00 | the night | works out the customers: Acme 3,275.00, Borg 2,125.00 |
| 3 December, 4:00 | the night | works out the region: North 5,400.00 |
| 3 December, 10:00 | Tess | changes EUR to 4.3100 |
| 3 December, 11:00 | Piotr | corrects INV-18 to 2,200.00 |

## North's 5,400.00

At noon Dan asks where North's sales come from:

```jsonc
{"class": "Region", "id": "north", "variable": "sales"}
```

<!-- north -->
```jsonc
{
  "lineage": {
    "class": "Region",
    "id": "north",
    "variable": "sales",
    "version": 2,
    "value": "5400.00",
    "kind": "kept",
    "formula": "LOOKUP(Customer, SUM(invoiced), region = REF())",
    "since": {
      "version": 2,
      "at": 1796270400,
      "by": "dan",
      "by_kind": "person",
      "event": "refresh"
    },
    "lookups": [
      {
        "class": "Customer",
        "aggregate": "SUM",
        "column": "invoiced",
        "value": "5400.00",
        "count": 2,
        "parts": [
          {
            "id": "Acme",
            "version": 2,
            "value": "3275.00"
          },
          {
            "id": "Borg",
            "version": 2,
            "value": "2125.00"
          }
        ],
        "more": false,
        "changed_since": {
          "count": 0,
          "parts": []
        },
        "now": "5400.00"
      }
    ],
    "adds_up": true
  }
}
```

- **`kind` is `kept`**: a value worked out at a moment and kept.
  `since` is that moment: the refresh of 3 December at 4:00, which
  made version 2.
- **The lookup read two customers**, as they were just before that
  commit: Acme at its version 2 and Borg at its version 2, the largest
  first.
- **`adds_up`**: the formula, worked out again over those parts, gives
  5,400.00. No customer has changed since.

## One level down: Acme

Dan asks about the larger part:

```jsonc
{"class": "Customer", "id": "Acme", "variable": "invoiced"}
```

<!-- acme -->
```jsonc
{
  "lineage": {
    "class": "Customer",
    "id": "Acme",
    "variable": "invoiced",
    "version": 2,
    "value": "3275.00",
    "kind": "kept",
    "formula": "LOOKUP(Invoice, SUM(net), customer = REF() AND state <> \"draft\")",
    "since": {
      "version": 2,
      "at": 1796266800,
      "by": "dan",
      "by_kind": "person",
      "event": "refresh"
    },
    "lookups": [
      {
        "class": "Invoice",
        "aggregate": "SUM",
        "column": "net",
        "value": "3275.00",
        "count": 2,
        "parts": [
          {
            "id": "INV-18",
            "version": 2,
            "value": "2000.00"
          },
          {
            "id": "INV-17",
            "version": 2,
            "value": "1275.00"
          }
        ],
        "more": false,
        "changed_since": {
          "count": 1,
          "parts": [
            {
              "id": "INV-18",
              "version": 3,
              "at": 1796295600,
              "by": "piotr"
            }
          ]
        },
        "now": "3475.00"
      }
    ],
    "adds_up": true
  }
}
```

- **The two invoices as they were at 3:00**: INV-18 at 2,000.00, its
  version 2.
- **`changed_since`** names what has changed after: Piotr's
  correction of INV-18 at 11:00, its version 3. **`now`** is what the
  lookup gives now, 3,475.00: the kept value is 200.00 behind, and
  North with it, until the night.

## Two levels down: the invoice

```jsonc
{"class": "Invoice", "id": "INV-17", "variable": "net"}
```

<!-- the net -->
```jsonc
{
  "lineage": {
    "class": "Invoice",
    "id": "INV-17",
    "variable": "net",
    "version": 2,
    "value": "1275.00",
    "kind": "computed",
    "formula": "ROUND(amount * rate, 2)",
    "since": {
      "version": 2,
      "at": 1796202000,
      "by": "ann",
      "by_kind": "person",
      "event": "transition",
      "name": "file"
    },
    "variables": [
      {
        "variable": "amount",
        "value": "300.00"
      },
      {
        "variable": "rate",
        "value": "4.2500"
      }
    ],
    "adds_up": true
  }
}
```

A computed value reads its own document. Its `since` is the version
since which it is what it is: the one Ann's filing made. And the rate:

```jsonc
{"class": "Invoice", "id": "INV-17", "variable": "rate"}
```

<!-- the rate -->
```jsonc
{
  "lineage": {
    "class": "Invoice",
    "id": "INV-17",
    "variable": "rate",
    "version": 2,
    "value": "4.2500",
    "kind": "kept",
    "formula": "LOOKUP(Rates, rate, currency = THIS.currency)",
    "since": {
      "version": 2,
      "at": 1796202000,
      "by": "ann",
      "by_kind": "person",
      "event": "transition",
      "name": "file"
    },
    "variables": [
      {
        "variable": "currency",
        "value": "EUR"
      }
    ],
    "lookups": [
      {
        "table": "Rates",
        "column": "rate",
        "value": "4.2500",
        "count": 1,
        "parts": [
          {
            "key": {
              "currency": "EUR"
            },
            "value": "4.2500",
            "since": {
              "at": 1796202000,
              "by": "tess",
              "by_kind": "person",
              "event": "import"
            },
            "until": {
              "at": 1796292000,
              "by": "tess",
              "by_kind": "person",
              "event": "import"
            }
          }
        ],
        "more": false,
        "table_changed_since": true
      }
    ],
    "adds_up": true
  }
}
```

The invoice kept the rate it read, 4.2500. The table has changed since
(`table_changed_since`): its row is 4.3100 now. A table keeps the
history of its rows, so the answer reads the row as it was when Ann
filed: **the row EUR held 4.2500 from 2 December at 9:00, put there by
Tess's import (`since`), until her import of 3 December at 10:00
(`until`)**. Worked out again over that row, the formula gives the rate
the invoice kept: it adds up.

A table whose definition says `"history": false` keeps none of it. Its
rows are then read as they are now, and when the table has changed
since, the answer says so with `"rows_now": true`; each row still says,
in `since`, which change gave it the values it has.

The amount is what Ann typed:

<!-- the amount -->
```jsonc
{
  "lineage": {
    "class": "Invoice",
    "id": "INV-17",
    "variable": "amount",
    "version": 2,
    "value": "300.00",
    "kind": "typed",
    "since": {
      "version": 1,
      "at": 1796202000,
      "by": "ann",
      "by_kind": "person",
      "event": "created"
    }
  }
}
```

## Each reader sees their own

Piotr reads only the invoices he wrote. He sees Acme's total, which is
everyone's to read, and asks the same:

<!-- acme for piotr -->
| Who asks | Parts listed | `hidden` |
|---|---|---|
| Dan | INV-18, INV-17 | |
| Piotr | INV-18 | 1, together 1275.00 |

- **A part the reader may not read is counted, never shown.** For a sum
  and a count, `hidden` says what those parts come to as well: the
  total already tells it. For the other aggregates it says how many.
- **A live value is the reader's, now**, and so are its parts.
- **A content administrator** who asks past the rules gives a reason,
  and the log of reads names the parts only the override showed.

## In the past

`version` asks about a value as it was, so that a walk down stays in
the past: INV-18's net at version 2, the one the night read, is
2,000.00.

<!-- in the past -->
| The object | `value` | `since` |
|---|---|---|
| `{"class": "Invoice", "id": "INV-18", "variable": "net", "version": 2}` | 2000.00 | version 2 |
| `{"class": "Invoice", "id": "INV-18", "variable": "net"}` | 2200.00 | version 3 |

## What an answer says

<!-- what lineage says -->
| Member | Of | Says |
|---|---|---|
| `class`, `id`, `variable` | the answer | the value asked about |
| `version` | the answer | the version of the document the value is of |
| `value` | the answer | the value |
| `kind` | the answer | `typed`, `computed`, `kept` or `live` |
| `formula` | the answer | the formula as the class writes it |
| `since` | the answer | the moment the value came: for a kept value the commit that worked it out, for a typed or computed one the version since which it is what it is |
| `variables` | the answer | the values of the document itself the formula read, at that moment |
| `lookups` | the answer | what each lookup of the formula read |
| `adds_up`, `rebuilt` | the answer | the formula worked out again over what it read gives the value; what it gives when it does not |
| `version`, `at`, `by`, `by_kind`, `for`, `event`, `name` | the moment | the version, when, who, of what kind and for whom, and the event: `created`, `set`, `transition` with its name, `refresh`; for a row of a table, a change of the table's rows, with no version: `import`, `replace`, `delete`, `define`, or a rule's `table.insert`, `table.update`, `table.delete` |
| `variable`, `value`, `hidden` | a value it read | its name, its value, and that the reader may not read it |
| `class`, `table`, `list` | a lookup | what it read |
| `aggregate`, `column` | a lookup | `SUM`, `COUNT` and the rest, and what it adds up or takes |
| `value`, `count` | a lookup | what it gave, and over how many |
| `parts`, `more` | a lookup | the page of the parts the reader may read, the largest first, and that more follow |
| `hidden` | a lookup | how many parts the reader may not read, and for a sum or a count what they come to |
| `changed_since`, `now` | a lookup | for a kept value: the parts with a version after it was worked out, and what the lookup gives now |
| `table_changed_since` | a lookup | the table's rows changed after the value was worked out |
| `rows_now` | a lookup | the table kept no history of its rows at the moment and has changed since: the rows are as they are now |
| `id`, `key`, `version`, `value` | a part | the document at the version read, or the row by its key, and the value taken of it |
| `since`, `until` | a part | for a row of a table: the change that gave it the values read, and the one that changed or removed it after; `since` is left out for a row older than the history |
| `count`, `parts` | what changed | how many, and the page of them |
| `id`, `version`, `at`, `by` | a change | the document, its version now, when and by whom it last changed |

## Asking

In a Go program:

```go
l, err := db.Lineage(ctx, "Customer", "Acme", "invoiced", fado.LineageAsk{}, fado.As(dan))
```

`fado.LineageAsk{Version: 2, Limit: 20}` asks about version 2, twenty
parts a page. As a command it is `lineage`
([the commands](../commands.md#lineage)); from a terminal:

```sh
fadodb exec lineage '{"class": "Customer", "id": "Acme", "variable": "invoiced"}'
```

## Not here yet

- **The parts of a figure over a list** of the document, as
  `SUM(lines.net)`: read the list with `live`.
- **The copy in the analytical engine**: lineage is answered by the
  store in both programs. Among 3,400,000 invoices it answers a total
  of one customer's 1,700 invoices in 60 ms, and one of 1,700,000 in 22
  seconds.
