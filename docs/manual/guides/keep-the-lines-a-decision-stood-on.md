# Keep the lines a decision stood on

An order may be closed when it has invoices and every one of them is
paid. The decision reads other documents, the invoices. Afterwards
the order should show what it was decided on: the invoices as they
were when it was closed, whatever happens to them later.

A list that is kept does that. It is a triggered variable whose whole
formula is a `QUERY`: the engine asks the question at a moment the
class names and keeps the answer with the document, as lines somebody
entered.

## The two classes

```json
{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "order":  {"type": "ref", "class": "Order", "index": true},
    "net":    {"type": "money(2)"},
    "owner":  {"type": "text", "default": "USER()"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file",    "from": "draft",  "to": "review"},
    {"name": "approve", "from": "review", "to": "paid"}
  ],
  "access": {
    "read":   "owner = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"file": "owner = USER()", "approve": "INGROUP(\"finance\")"}
  }
}
```

```json
{
  "name": "Order",
  "title": "number",
  "variables": {
    "number":  {"type": "text", "required": true},
    "owner":   {"type": "text", "default": "USER()"},
    "settled": {"type": "rows", "schema": {"number": "text", "net": "money(2)", "state": "text"},
                "formula": "QUERY(Invoice, {number, net, state}, order = REF(), {number})",
                "mode": "triggered", "on": ["before:close", "refresh"]},
    "unpaid":  {"formula": "COUNTIF(settled, state <> \"paid\")"},
    "billed":  {"formula": "SUM(settled.net)"}
  },
  "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
  "transitions": [
    {"name": "close", "from": "open", "to": "closed",
     "guard": "COUNT(settled) > 0 AND ALL(settled, state = \"paid\")"}
  ],
  "access": {
    "read":   "INGROUP(\"sales\") OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE"
  },
  "rights": {"*": {"settled": {"write": ["finance"]}}}
}
```

`settled` is the list. Its `schema` says its columns, which are the
columns of the `QUERY` with the types they have in Invoice. It is
brought `before:close`, when somebody asks to close the order and
before the guard is asked, and by a `refresh`. The guard and the two
computed variables read it as they would read lines somebody entered.

## What it does

Jan and Piotr are in sales, Ewa is in finance. Order ORD-17 is Jan's.
It has three invoices in draft: FV-1 of 1,000.00 PLN and FV-2 of
250.00 PLN, which Jan wrote, and FV-3 of 70.00 PLN, which Piotr wrote
and Jan may not read.

| | Who | Does | Answer |
|---|---|---|---|
| 1 | Jan | reads ORD-17 | version 1, `settled` empty |
| 2 | Jan | closes ORD-17 | refused, `guard_false`; brought: the three invoices, in draft |
| 3 | Jan | refreshes `settled` | refused, `forbidden`, variable `settled` |
| 4 | Ewa | refreshes `settled` | version 2: three lines, `unpaid` 3, `billed` 1,320.00 PLN |
| 5 | Jan, Piotr, Ewa | file and approve the three invoices | each invoice is paid |
| 6 | Jan | reads ORD-17 | version 2 still: three lines in draft, `unpaid` 3 |
| 7 | Jan | closes ORD-17 | version 3, closed: three lines, each paid, `unpaid` 0 |

- **Step 2 writes nothing**, and says what the guard saw. FV-3 is
  among the lines, though Jan may not read that invoice: a list that
  is kept is one list for everybody.
- **Step 6: the list is kept.** The invoices moved on and the order
  did not. It says, under `computed`, that `settled` is of version 2.
- **Step 7 brings the list again**, before the guard, and version 3
  keeps the lines the closing stood on.

## How it works

- **The engine asks the question, with its own eyes**: every invoice
  of the order, whoever asked to close it. Who sees the list is the
  `read` right of `settled`, as for any variable.
- **It is kept with every version** of the order, in a table of its
  own beside the order's, as entered lines are. The history shows what
  the list was at each version.
- **What reads entered lines reads this list**: `COUNT`, `SUM`, `ALL`,
  `COUNTIF` and the others, in a guard, a rule and a computed
  variable.
- **Nobody sets it.** A caller who gives it a value is refused with
  `read_only`. It is brought again by a `refresh`, by whoever its
  `write` right lets: here finance.
- **It holds at most 10,000 rows.** A longer answer is not kept: a
  refresh leaves the list as it was and says `too_many_elements`
  under `computed`, and a closing that needs it is refused.
- **The schema is checked when the class is defined**: every column
  of the `QUERY` is in it with the type it has in the source, and it
  has no other.

## A list to browse, and a list to decide on

| The designer wants | Writes |
|---|---|
| the invoices of the order as they are now, for whoever reads | `"mode": "live"`: asked at every read, for its reader, kept nowhere; see [Lists of other documents](../lists.md) |
| the invoices a decision stood on | `"mode": "triggered"`, with `before:` the transition that decides |
| the invoices as they were at a state change | `"mode": "triggered"`, with `enter:` or `exit:` the state |

See also: [Bring what a condition needs](bring-what-a-condition-needs.md),
[Lists of other documents](../lists.md#a-list-that-is-kept),
[Triggered variables](../triggered-variables.md).
