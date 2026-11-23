# Lists of other documents

An order shows its invoices. A page shows the expenses that wait for
approval. Each is a **list**: some documents of a class, with some of
their variables as columns.

A list is a [live variable](live-variables.md) whose formula is a
`QUERY`. It is one question to the documents of a class: which of them,
with which columns, in what order. It is asked when somebody reads the
variable, for that reader, and nothing of it is kept.

This chapter is the reference for lists. A test defines what stands
here and plays it on each push.

## The two classes

Acme's invoices point at their order. Jan and Piotr are in sales, and
each reads the invoices he wrote. Ewa is in finance: she reads every
invoice, and its margin, which sales may not see.

```json
{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true, "label": "Number"},
    "order":  {"type": "ref", "class": "Order", "label": "Order", "index": true},
    "net":    {"type": "money", "scale": 2, "label": "Net amount"},
    "margin": {"type": "money", "scale": 2, "label": "Margin"},
    "owner":  {"type": "text", "default": "USER()"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file", "from": "draft", "to": "review", "label": "Send for approval"},
    {"name": "approve", "from": "review", "to": "paid", "label": "Approve"}
  ],
  "access": {
    "read": "owner = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"file": "owner = USER()", "approve": "INGROUP(\"finance\")"}
  },
  "rights": {
    "*": {"margin": {"read": ["finance"], "write": ["finance", "sales"]}}
  }
}
```

The order has the list, `invoices`:

```json
{
  "name": "Order",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true, "label": "Number"},
    "customer": {"type": "text", "label": "Customer"},
    "owner":    {"type": "text", "default": "USER()"},
    "invoices": {"label": "Invoices", "mode": "live",
                 "formula": "QUERY(Invoice, {number, net, state, margin}, order = REF(), {number})"}
  },
  "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
  "transitions": [{"name": "close", "from": "open", "to": "closed"}],
  "access": {
    "read": "INGROUP(\"sales\") OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"close": "owner = USER()"}
  }
}
```

`invoices` reads: of the class Invoice, the number, the net amount, the
state and the margin of every invoice whose `order` is this order,
sorted by number.

## The four parts of a QUERY

| Part | In the example | What it says |
|---|---|---|
| the source | `Invoice` | the class whose documents are listed |
| the columns | `{number, net, state, margin}` | what each row shows: variables of the source, or its system columns, such as `state` and `title` |
| the condition | `order = REF()` | which documents are listed. Left out, every document of the source is |
| the sort | `{number}` | the order of the rows, a leading `-` for descending, as in `{-net, number}`. Left out, the rows are sorted by id |

Several columns are written in braces, and so are several columns to
sort by. One needs no braces: `QUERY(Invoice, number)`.

The class that holds the list is defined after its source: Acme
defines Invoice, then Order.

## What the condition reads

The condition is asked of every document of the source. So a bare
name in it is a variable of the listed document, here an invoice. The
order, the document the list belongs to, is read in two ways of its
own.

| Written in the condition | Is |
|---|---|
| `order`, `net`, `state` | a variable or a system column of the listed document |
| `STATE()`, `ID()` | the state and the id of the listed document |
| `THIS.customer` | the variable `customer` of the document the list belongs to |
| `REF()` | the pointer to the document the list belongs to: what a `ref` compares with |
| `USER()`, `INGROUP("finance")`, `TODAY()` | the reader and the clock |

A list need not point at its document. "The invoices that wait for
approval" is `QUERY(Invoice, {number, net}, state = "review")`, and it
is the same list on every order.

## The story

Order ORD-17 is Globex's. Jan writes the invoices FV-1 and FV-2 on
it, and Piotr FV-3. Jan writes FV-4 on another order, ORD-18.

<!-- who reads what -->
| Who reads `invoices` | Of the order | The rows |
|---|---|---|
| Jan | ORD-17 | FV-1, FV-2 |
| Piotr | ORD-17 | FV-3 |
| Ewa | ORD-17 | FV-1, FV-2, FV-3 |
| Jan | ORD-18 | FV-4 |
| Piotr | ORD-18 | none |

Each reader gets the invoices the read rule of Invoice gives them, as
if they had asked for the invoices themselves. In each row they get
the columns they may read: the margin is left out of what Jan and
Piotr read.

## Reading a list

With the command `live`, as any live variable. In Go it is the call
`Live`, and `LivePage` to say which page. Jan asks for the invoices of
ORD-17:

<!-- the command -->
```jsonc
{"class": "Order", "id": "ORD-17", "variable": "invoices", "limit": 50}
```

<!-- the answer -->
```jsonc
{
  "columns": [
    {"name": "number", "type": "text", "label": "Number"},
    {"name": "net", "type": "money(2)", "label": "Net amount"},
    {"name": "state", "type": "text"},
    {"name": "margin", "type": "money(2)", "label": "Margin"}
  ],
  "rows": [
    {"id": "FV-1", "number": "FV-1", "net": "250.00 PLN", "state": "draft"},
    {"id": "FV-2", "number": "FV-2", "net": "1000.00 PLN", "state": "draft"}
  ],
  "more": false
}
```

| Member of the answer | What it is |
|---|---|
| `columns` | the columns in the order the formula writes them, each with its name, its type and the label the source gives it. A list with no rows still has its headings |
| `rows` | the documents, each with its `id` and its columns. A column the reader may not read is left out of the row |
| `more` | true when rows follow the last one answered |

Every row says the `id` of its document, whether the formula lists it
or not. That is what a click on a row needs: the page reads the
invoice with `get`, by that id.

## The page of a list

The formula says which documents the list has. The reader says which
of them to read, and the engine reads no more than that: the order,
the limit and the offset go into the one question.

| Member of the command | What it says |
|---|---|
| `order` | the columns to sort by, a leading `-` for descending; the order of the formula when left out |
| `limit` | the most rows to answer; 1,000 when left out, 10,000 at most |
| `offset` | how many rows to skip |
| `fields` | the columns to answer; every one when left out |

Ewa reads the invoices of ORD-17:

<!-- the page -->
| Ewa asks with | The rows | More follow |
|---|---|---|
| nothing more | FV-1, FV-2, FV-3 | no |
| `"order": ["-net"]` | FV-2, FV-1, FV-3 | no |
| `"limit": 2` | FV-1, FV-2 | yes |
| `"limit": 2, "offset": 2` | FV-3 | no |
| `"limit": 3` | FV-1, FV-2, FV-3 | no |

## Nothing is kept

A list has no column and no history, and no commit writes it. Reading
the order with `get` answers no `invoices`. Jan writes one more
invoice on ORD-17, and the next reader of the list sees it; the order
itself is still at the version it had.

## A large class

"The invoices of this order" must not read every invoice. That is why
`order` says `"index": true` in the class above: the list of one order
is read through the index of the pointer, and finds its few invoices
among a million as among a hundred.

**Not built yet:** the engine keeping this index by itself for every
pointer. It is decided (ADR-0075). Until then the designer writes
`"index": true`.

## Figures over a list

An order shows how many invoices it has and what they add up to. Each
is a **figure**: one value worked out of a list. A figure is a live
variable too, and its formula names the list:

```json
{
  "name": "Order",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true, "label": "Number"},
    "customer": {"type": "text", "label": "Customer"},
    "owner":    {"type": "text", "default": "USER()"},
    "invoices": {"label": "Invoices", "mode": "live",
                 "formula": "QUERY(Invoice, {number, net, state, margin}, order = REF(), {number})"},
    "count":    {"label": "How many", "mode": "live", "formula": "COUNT(invoices)"},
    "total":    {"label": "Total", "mode": "live", "formula": "SUM(invoices.net)"},
    "largest":  {"label": "Largest", "mode": "live", "formula": "MAX(invoices.net)"},
    "margins":  {"label": "Margins", "mode": "live", "formula": "SUM(invoices.margin)"},
    "by_owner": {"label": "Written by the owner", "mode": "live",
                 "formula": "COUNTIF(invoices, owner = THIS.owner)"},
    "all_paid": {"label": "All paid", "mode": "live", "formula": "ALL(invoices, state = \"paid\")"}
  },
  "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
  "transitions": [{"name": "close", "from": "open", "to": "closed"}],
  "access": {
    "read": "INGROUP(\"sales\") OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"close": "owner = USER()"}
  }
}
```

A figure is read as any live variable, with the command `live`, and
answers one value. It counts what its reader may read of the list, as
the list itself shows it. In the story above FV-1 is 250.00, FV-2
1000.00, FV-3 70.00 and FV-4 5.00, every margin is 10.00, and Jan
wrote both orders:

<!-- figures -->
| Who reads | Of the order | `count` | `total` | `largest` | `margins` | `by_owner` | `all_paid` |
|---|---|---|---|---|---|---|---|
| Jan | ORD-17 | 2 | 1250.00 PLN | 1000.00 PLN | blank | 2 | FALSE |
| Piotr | ORD-17 | 1 | 70.00 PLN | 70.00 PLN | blank | 0 | FALSE |
| Ewa | ORD-17 | 3 | 1320.00 PLN | 1000.00 PLN | 30.00 PLN | 2 | FALSE |
| Piotr | ORD-18 | 0 | blank | blank | blank | 0 | TRUE |

Sales may not read a margin, so the sum of the margins is blank for
Jan and for Piotr.

| Written | Gives |
|---|---|
| `COUNT(invoices)` | how many rows the list has |
| `COUNT(invoices.net)` | how many of them have a value in the column |
| `SUM(invoices.net)`, `MIN(invoices.net)`, `MAX(invoices.net)` | the sum, the smallest and the largest value of a column |
| `COUNTIF(invoices, state = "draft")` | how many rows meet a condition |
| `SUMIF(invoices, state = "draft", net)`, `MINIF`, `MAXIF` | the same figure, of the rows that meet a condition |
| `ANY(invoices, state = "draft")` | whether some row meets a condition |
| `ALL(invoices, state = "paid")` | whether every row meets it |

- **A column** is written after a dot, and is any stored variable of
  the listed class, or its `state`, `title` or `id`, whether the list
  shows it or not.
- **A condition** is written as in `QUERY`: a bare name is a column of
  the listed document, `THIS.owner` a variable of the order. The state
  of a row is its column `state`.
- **A list with no rows** counts 0. Its sum is 0, or blank for money;
  its smallest and largest are blank; `ANY` is FALSE and `ALL` TRUE.
- **Money** adds in one currency; invoices in two give the error
  `currency_mismatch`.
- **A figure may stand inside a formula**, and two side by side:
  `SUM(invoices.net) / DECIMAL(COUNT(invoices), 0)` is the mean. One
  may not stand inside another's arguments.
- **A figure stands only in a live variable.** Two readers get two
  figures, so a value that is kept, a guard and a rule may not hold
  one. For those, a `LOOKUP` in a triggered variable counts and sums:
  see [Tables](tables.md).

A figure is one question to the invoices, with the id of the order in
its condition, and the answer is one number: no invoice is read to be
counted. An index on the pointer serves it as it serves the list.

**Not built yet** (ADR-0081): `AVERAGE`, `MEDIAN` and `COUNTUNIQUE`
over a list; the same functions over the lines and the tags a document
holds; and the functions that give a list again, `FILTER`, `SORT`,
`TOP` and `UNIQUE`.

What is refused when the class is defined:

<!-- figures refused -->
| Written in a class | The class is refused with |
|---|---|
| a figure in a variable that is not live | `bad_formula` |
| a sum of a list and no column | `bad_formula` |
| a column the listed class has not | `bad_formula` |
| a sum of a text | `bad_formula` |
| a condition that is no condition | `bad_formula` |
| `STATE()` in the condition of a figure | `bad_formula` |
| a figure inside a figure | `bad_formula` |
| `AVERAGE` of a column of a list | `bad_formula` |

## What is refused

A list that is wrong is refused when its class is defined, with the
place in the formula.

<!-- refused -->
| Written in a class | The class is refused with |
|---|---|
| a `QUERY` in a variable that is not live | `bad_formula` |
| a `QUERY` inside a larger formula | `bad_formula` |
| a source nobody defined | `bad_formula` |
| a table as the source | `not_supported` |
| a column the source has not | `bad_formula` |
| a count among the columns | `not_supported` |
| a condition that is no condition | `type_mismatch` |
| a pointer compared with a text | `bad_formula` |
| `REF()` compared with a pointer to another class | `type_mismatch` |
| a `LOOKUP` in the condition | `not_allowed` |
| a live variable of the source as a column | `not_allowed` |
| braces anywhere else | `bad_formula` |
| `REF()` anywhere else | `bad_formula` |

And the source may not lose what a list names: a new definition of
Invoice without `margin` is refused while the order lists it.

## Not built yet

Each of these is decided (ADR-0076) and waits its turn.

- A table as the source of a `QUERY`. A `LOOKUP` reads a table today.
- A count or a sum among the columns, and grouping: a table of
  totals. One figure of a list is built: see "Figures over a list".
- A list that is kept: a `QUERY` in a triggered variable.
- `REF()` outside the condition of a `QUERY`, and `REF("Order",
  "ORD-17")`, the pointer to a named document.
- A single value looked up in a live variable, with `LOOKUP`.
