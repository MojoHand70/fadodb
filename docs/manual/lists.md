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
| the source | `Invoice` | the class whose documents are listed, or a table whose rows are; see [A table as the source](#a-table-as-the-source) |
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
is the same list on every order. "The invoices of order ORD-17" is
`QUERY(Invoice, {number, net}, order = REF("Order", "ORD-17"))`: with
a class and an id, `REF` is the pointer to a named document.

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

## A table as the source

The source of a `QUERY` may be a table. A quote lists the prices of
its band from Acme's price list:

```jsonc
"prices": {"mode": "live", "formula": "QUERY(Prices, {sku, price}, band = THIS.band, {-price})"}
```

| | A list of documents | A list of the rows of a table |
|---|---|---|
| the columns | variables of the class, and its system columns | columns of the table |
| a row | says the `id` of its document | has its columns and no id: a row of a table is no document |
| sorted, when nothing says | by id | by the key of the table |
| who may read a live list | each document by the read rule of its class, each value by its rights | whoever the `read` line of the table lets through, as for `table_rows`; anybody else is refused with `forbidden` |
| a content administrator | passes the rules with a reason | passes the `read` line with a reason, which the log of reads keeps |

The class is noted as a reader of the table: the table is not removed,
and does not lose a column the list names, while the class lists it.
A [list that is kept](#a-list-that-is-kept) may read a table too; it
is asked with the engine's eyes, as a `LOOKUP` is.

**Not built yet:** a figure over a live list of a table, such as
`COUNT(prices)` in a live variable. Keep the list, and work the
figure out of the kept one.

## A list that is kept

A live list is for browsing: it is asked again for every reader, and
shows the invoices as they are now. A decision needs the other kind.
When an order is closed because every invoice of it was paid, the
order should hold the invoices that decision stood on, as they were
then.

A `QUERY` that is the whole formula of a **triggered** variable is a
list that is kept. The variable says its type, `rows`, and its
columns, which are the columns of the `QUERY` with the types they
have in the source:

```jsonc
"settled": {"type": "rows", "schema": {"number": "text", "net": "money(2)", "state": "text"},
            "formula": "QUERY(Invoice, {number, net, state}, order = REF(), {number})",
            "mode": "triggered", "on": ["before:close", "refresh"]}
```

| | A live list | A list that is kept |
|---|---|---|
| asked | whenever somebody reads it | at the triggers the variable lists |
| for | its reader: the documents and the values they may read | everybody: one list, with the engine's eyes |
| held by the document | no | yes, with every version, as lines somebody entered |
| read with | the command `live` | the document, as any value |
| in a guard, a rule, a computed variable | no | yes: `ALL(settled, state = "paid")`, `SUM(settled.net)` |
| who sees it | who may read each document listed | the `read` right of the variable |
| brought again by | nobody: it is never kept | a `refresh`, by whoever the `write` right of the variable lets |

It holds at most 10,000 rows, as every list of a document does, and it
is copied with each version of the document: it is for the lines a
decision stood on. The guide
[Keep the lines a decision stood on](guides/keep-the-lines-a-decision-stood-on.md)
plays an order from its invoices to its closing.

## A large class

"The invoices of this order" must not read every invoice. The engine
keeps an index on every pointer by itself, so the list of one order is
read through the index of `order`, and finds its few invoices among a
million as among a hundred. Nobody has to write `"index": true` on a
pointer; the class above still says it, and that changes nothing.

| The engine keeps an index on | For |
|---|---|
| every pointer, a `ref` | the documents that point at a document: its list |
| the state of every class | the documents that wait in a state, and how many |
| the moment a document was made, and the moment it last changed | the newest documents, and what changed last |
| a text the read rule compares with the caller, `owner = USER()` | everything a caller asks who reads only their own |
| the variable a `LOOKUP` or a list of any class is keyed by: the one its condition compares for equality with a value of its own document, as `number` in `LOOKUP(Purchase, COUNT(id), number = THIS.purchase)` | that lookup and that list. The index is made when the class that asks is defined, and dropped when no definition asks for it any more |
| a variable that says `"index": true` | a search the class itself does not declare, such as invoices by customer |

Each index on a variable or on the state goes on to the moment a
document was made, so the newest fifty of an order, of a state or of a
customer are read off its end and nothing is sorted.

A list may compare more than its pointer, as in
`order = REF() AND owner = "jan"`. The engine asks it through the
pointer the list compares with its document, whatever else the
condition compares.

A question somebody writes by hand, the `where` of a `query`, may
compare two variables that both have an index, say the order of an
invoice and its customer. The store does not know which of the two
finds fewer documents. So the engine looks into each
index first, for at most a thousand documents, and asks through the
one that ends first. The answer is the same either way; what changes
is how long it takes.

**A question somebody writes that no index serves is stopped.** The
`where` of a `query` or a `count` may compare a variable nobody
indexed, and then the store reads the documents of the class one by
one: a moment among a thousand, minutes among a hundred million. Such
a question runs for at most five seconds, and is then refused with
`too_wide`; the hint names the variable an index is needed on:

```text
too_wide: the question was stopped after 5 seconds: no index serves it, so it reads the documents of Invoice one by one
  fix: customer has no index: say "index": true on it in the class Invoice
```

A question an index serves is never stopped, and neither is a list of
a document. The five seconds are the database's, set with
`MaxQuestion` when it is opened; see
[Opening a database](opening-a-database.md). A question over
everything is the analyst's, asked with `sql`.

**The engine says which question no index serves.** When a class is
defined, each `LOOKUP` and each list of it over the documents of a
class is checked: its condition has to compare a variable that has an
index, for equality, with a value. One that does not reads every
document of that class each time, and is noted, with the mend:

```text
  note: reads_all at /variables/from_fv/formula: this LOOKUP reads every document of Invoice each time it is worked out: its condition compares no variable of Invoice for equality with a value of this document
    fix: begin the condition with such a comparison, as order = REF(): the engine keeps an index on every pointer; a total over a whole class has no index to be served by
```

A note refuses nothing, and the class is defined all the same: on a
class of a thousand documents such a question is harmless. A total
over a whole class, `LOOKUP(Invoice, SUM(net), TRUE)`, is noted too,
for it reads everything by its nature.

**A check says what a definition would build.** An index on a class
that already holds many documents takes time to make, and the commits
of the database wait meanwhile: seconds for ten million documents,
minutes for a hundred million. `validate_class` and
`fadodb class check --db` say it before anything is written, as a
note, code `builds_index`, for a class of 100,000 documents or more:

```text
  note: builds_index at /variables/late/formula: taking this definition makes an index on note of Invoice, which holds about 100,000,000 documents: the commits of the database wait while it is built
    fix: choose the hour: an index over ten million documents takes seconds, over a hundred million minutes
```

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
    "average":  {"label": "Mean", "mode": "live", "formula": "ROUND(AVERAGE(invoices.net), 2)"},
    "middle":   {"label": "Middle value", "mode": "live", "formula": "MEDIAN(invoices.net)"},
    "in_states": {"label": "Different states", "mode": "live", "formula": "COUNTUNIQUE(invoices.state)"},
    "first":    {"label": "First invoice", "mode": "live", "formula": "FIRST(invoices.number)"},
    "numbers":  {"label": "Invoices by number", "mode": "live", "formula": "JOIN(invoices.number, \", \")"},
    "has_draft": {"label": "Has a draft", "mode": "live", "formula": "CONTAINS(invoices.state, \"draft\")"},
    "dearest":  {"label": "Dearest invoice", "mode": "live", "formula": "FIRST(SORT(invoices, {-net}).number)"},
    "two_smallest": {"label": "The two smallest", "mode": "live",
                 "formula": "JOIN(TOP(SORT(invoices, {net}), 2).number, \", \")"},
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

The mean, the middle value and the number of different states, for the
same readers:

<!-- more figures -->
| Who reads | Of the order | `average` | `middle` | `in_states` |
|---|---|---|---|---|
| Jan | ORD-17 | 625.00 PLN | 625.000 PLN | 1 |
| Piotr | ORD-17 | 70.00 PLN | 70.000 PLN | 1 |
| Ewa | ORD-17 | 440.00 PLN | 250.000 PLN | 1 |
| Piotr | ORD-18 | blank | blank | 0 |

Jan reads two invoices, so the middle value is the mean of the two.
Ewa reads three, and the middle one of 70.00, 250.00 and 1000.00 is
250.00.

One value picked out of the list, the values of a column as one text,
and whether a value is there:

<!-- picked figures -->
| Who reads | Of the order | `first` | `numbers` | `has_draft` |
|---|---|---|---|---|
| Jan | ORD-17 | FV-1 | FV-1, FV-2 | TRUE |
| Piotr | ORD-17 | FV-3 | FV-3 | TRUE |
| Ewa | ORD-17 | FV-1 | FV-1, FV-2, FV-3 | TRUE |
| Piotr | ORD-18 | blank | blank | FALSE |

"First" is by the order of the list: `invoices` is sorted by number,
so the first is the lowest number its reader may read.

A list may be narrowed, ordered and cut before the function runs over
it, inside the same formula:

<!-- shaped figures -->
| Who reads | Of the order | `dearest` | `two_smallest` |
|---|---|---|---|
| Jan | ORD-17 | FV-2 | FV-1, FV-2 |
| Piotr | ORD-17 | FV-3 | FV-3 |
| Ewa | ORD-17 | FV-2 | FV-3, FV-1 |
| Piotr | ORD-18 | blank | blank |

`dearest` orders the invoices by their net from the largest and takes
the number of the first. `two_smallest` orders them from the smallest,
takes the first two, and joins their numbers.

| Written | Gives |
|---|---|
| `COUNT(invoices)` | how many rows the list has |
| `COUNT(invoices.net)` | how many of them have a value in the column |
| `SUM(invoices.net)`, `MIN(invoices.net)`, `MAX(invoices.net)` | the sum, the smallest and the largest value of a column |
| `AVERAGE(invoices.net)` | the mean of a column |
| `MEDIAN(invoices.net)` | the middle value of a column; of an even number of values, the mean of the two in the middle |
| `COUNTUNIQUE(invoices.state)` | how many different values a column has |
| `COUNTIF(invoices, state = "draft")` | how many rows meet a condition |
| `SUMIF(invoices, state = "draft", net)`, `MINIF`, `MAXIF`, `AVERAGEIF` | the same figure, of the rows that meet a condition |
| `ANY(invoices, state = "draft")` | whether some row meets a condition |
| `ALL(invoices, state = "paid")` | whether every row meets it |
| `FIRST(invoices.number)`, `LAST(invoices.number)` | the value of a column in the first or the last row, by the order of the list |
| `INDEX(invoices.number, 2)` | the value in the row at a place, counted from 1 |
| `JOIN(invoices.number, ", ")` | the values of a column as one text, with the separator between them |
| `CONTAINS(invoices.state, "draft")` | whether some row has the value in the column |
| `FILTER(invoices, state = "draft")` | inside a function: the rows that meet a condition |
| `SORT(invoices, {-net})` | inside a function: the rows in another order, a minus for descending |
| `TOP(invoices, 3)` | inside a function: the first rows, as many as the count says |
| `UNIQUE(invoices.state)` | inside a function: the different values of a column |

- **A column** is written after a dot, and is any stored variable of
  the listed class, or its `state`, `title` or `id`, whether the list
  shows it or not.
- **A condition** is written as in `QUERY`: a bare name is a column of
  the listed document, `THIS.owner` a variable of the order. The state
  of a row is its column `state`.
- **A list with no rows** counts 0. Its sum is 0, or blank for money;
  its smallest and largest, its mean and its middle value are blank;
  it has 0 different values; `ANY` is FALSE and `ALL` TRUE.
- **Blanks are skipped**: a row with no value in the column counts for
  no mean, no middle value and no different value.
- **Money** adds in one currency; invoices in two give the error
  `currency_mismatch`. `COUNTUNIQUE` alone takes them: the same amount
  in two currencies is two values.
- **The mean and the middle value of exact amounts are exact.** The
  mean of amounts with two places has six, as `AVERAGE` of single
  values has. The middle value has three: one more than the column,
  which the mean of two needs. `ROUND` cuts either, as `average` does
  above.
- **One value out of the list** follows the order of the list, the
  one its `QUERY` gives it. `FIRST` and `LAST` of a list with no rows
  are blank. `INDEX` at a place the list does not have, or at a place
  that is no whole number, is the error `out_of_range`: `IFERROR`
  catches it. `JOIN` writes each value as `TEXT` writes it and skips
  the blanks. The place, the separator and the value `CONTAINS` looks
  for are values of the document, the same for every row.
- **A figure may stand inside a formula**, and two side by side:
  `MAX(invoices.net) - MIN(invoices.net)` is how far the invoices are
  apart. One may not stand inside another's arguments, so the place
  of `INDEX` is no `COUNT` of the list: the last row is `LAST`.
- **A figure stands only in a live variable.** Two readers get two
  figures, so a value that is kept, a guard and a rule may not hold
  one. For those, a `LOOKUP` in a triggered variable counts and sums:
  see [Tables](tables.md).

A figure is one question to the invoices, with the id of the order in
its condition, and the answer is one number: no invoice is read to be
counted. An index on the pointer serves it as it serves the list.

- **`FILTER`, `SORT`, `TOP` and `UNIQUE` give a list, and a list
  stands only inside a function over a list**: the answer of the
  formula is always one value. A column of what they give is written
  after them: `SUM(FILTER(invoices, state = "draft").net)`. They are
  written from the inside out in one order, `FILTER` nearest the list:
  `TOP(SORT(FILTER(invoices, state = "draft"), {-net}), 3)`. `UNIQUE`
  goes round a column, and round nothing else:
  `JOIN(UNIQUE(invoices.state), ", ")`.
- **Each part becomes a part of the one question**: `FILTER` its
  condition, `SORT` its order, `TOP` its limit, `UNIQUE` the different
  values. Rows a `SORT` orders alike keep the order of the list, and a
  blank comes before every value. The count of `TOP` is a value of the
  document: a blank one is 0, and one below 0 or no whole number is
  the error `out_of_range`. `UNIQUE` stands inside `COUNT`, `JOIN`,
  `FIRST`, `LAST`, `INDEX` and `CONTAINS`; a sum of different values
  is not built.

**Not built** (ADR-0081, the owner's ruling of 2026-11-24): a list
that `FILTER`, `SORT`, `TOP` or `UNIQUE` gives as a variable of its
own. A second `QUERY` with a condition and an order is such a list
today. The same figures over the lines and the tags a document holds
are below.

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
| a middle value of a text | `bad_formula` |
| `MEDIAN` of single values | `bad_formula` |

## Totals by group

Ewa wants one line for each person in sales: how many invoices they
wrote, and what the invoices add up to. That is a list too. Its
`QUERY` has a **total** among its columns:

```
QUERY(Invoice, {owner, COUNT(id), SUM(net)})
```

When a column is a total, the plain columns say what the lines are
grouped by: here one line for each owner, as in a pivot table. With no
plain column the list has one line, the totals over every invoice.

Acme keeps such lists in a class of their own, Books, of which there
is one document, `acme`:

```json
{
  "name": "Books",
  "title": "name",
  "variables": {
    "name":     {"type": "text", "required": true, "label": "Name"},
    "by_owner": {"label": "Invoiced by person", "mode": "live",
                 "formula": "QUERY(Invoice, {owner, COUNT(id), SUM(net)})"},
    "by_order": {"label": "Drafts by order", "mode": "live",
                 "formula": "QUERY(Invoice, {order, COUNT(id), SUM(net)}, state = \"draft\", {-SUM(net)})"},
    "whole":    {"label": "All invoices", "mode": "live",
                 "formula": "QUERY(Invoice, {COUNT(id), SUM(net), MAX(net)})"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {
    "read": "INGROUP(\"sales\") OR INGROUP(\"finance\")",
    "create": "INGROUP(\"finance\")",
    "update": "FALSE",
    "delete": "FALSE"
  }
}
```

The rest of the `QUERY` is as for any list. The condition says which
invoices are counted: `by_order` counts the drafts. What the list is
sorted by is a group or a total, written as among the columns:
`by_order` has the largest sum first. Without a sort the lines come in
the order of their groups.

| A total | What it is, over the documents of a group |
|---|---|
| `COUNT(id)` | how many there are; `COUNT(net)` counts those that have a net amount |
| `SUM(net)` | their sum |
| `MIN(net)`, `MAX(net)` | the least and the greatest |
| `AVERAGE(net)` | their mean |
| `COUNTUNIQUE(owner)` | how many different values there are |

A total runs over one variable of the source, or one of its system
columns, and has the type the same function has in a `LOOKUP`: a count
is a number, a sum of money is money.

A total counts what its reader may read, as every list does. In the
story Jan wrote FV-1, FV-2 and FV-4, and Piotr FV-3:

<!-- totals -->
| Who reads | The list | Its lines |
|---|---|---|
| Ewa | `by_owner` | jan, 3, 1255.00 PLN; piotr, 1, 70.00 PLN |
| Jan | `by_owner` | jan, 3, 1255.00 PLN |
| Piotr | `by_owner` | piotr, 1, 70.00 PLN |
| Ewa | `by_order` | Order:ORD-17, 3, 1320.00 PLN; Order:ORD-18, 1, 5.00 PLN |
| Jan | `by_order` | Order:ORD-17, 2, 1250.00 PLN; Order:ORD-18, 1, 5.00 PLN |
| Ewa | `whole` | 4, 1325.00 PLN, 1000.00 PLN |
| Piotr | `whole` | 1, 70.00 PLN, 70.00 PLN |

It is read with the command `live`, as any list. Ewa asks:

<!-- the totals command -->
```jsonc
{"class": "Books", "id": "acme", "variable": "by_owner"}
```

<!-- the totals answer -->
```jsonc
{
  "columns": [
    {"name": "owner", "type": "text"},
    {"name": "count_id", "type": "number"},
    {"name": "sum_net", "type": "money(2)"}
  ],
  "rows": [
    {"owner": "jan", "count_id": 3, "sum_net": "1255.00 PLN"},
    {"owner": "piotr", "count_id": 1, "sum_net": "70.00 PLN"}
  ],
  "more": false
}
```

- **A line is no document.** It has no `id`. A page that wants the
  invoices behind a line asks a list of documents, with the group in
  its condition.
- **A total stands under the name of its function and its variable**,
  in lower case with `_` between: `count_id`, `sum_net`.
- **The reader's page is of the lines.** `order` names a group or a
  total, as `"order": ["-sum_net"]`; `limit` and `offset` are of the
  lines, and `more` says that lines follow. `fields` is refused: the
  columns say what the lines are grouped by, so a reader cannot take
  some of them.

<!-- the totals page -->
| Ewa asks `by_owner` with | The lines | More follow |
|---|---|---|
| nothing more | jan, 3, 1255.00 PLN; piotr, 1, 70.00 PLN | no |
| `"order": ["sum_net"]` | piotr, 1, 70.00 PLN; jan, 3, 1255.00 PLN | no |
| `"order": ["-owner"], "limit": 1` | piotr, 1, 70.00 PLN | yes |
| `"limit": 1, "offset": 1` | piotr, 1, 70.00 PLN | no |

What a list of totals names, a group or what a total runs over, is
read in a document only where the state of the document lets the
reader read it; a document where it does not is left out of every
total. A reader who may read one of them in no state is refused, with
`forbidden`: `SUM(margin)` is for finance.

A sum of money in two currencies within one group stops the list with
`currency_mismatch`, as it stops a `LOOKUP`.

Such a list reads every invoice its condition takes, each time it is
read. Where the condition compares no indexed variable, as in
`by_owner` and `whole`, which have none, the engine says so when the
class is defined, with the note `reads_all`. That is harmless on a
small class. On a large one, a program that keeps a copy of the
documents in the analytical engine answers such a list from the copy,
in milliseconds, with the same lines for the same reader
([An analytical engine](opening-a-database.md#an-analytical-engine)).

## Figures over the lines a document holds

A document has lists of its own as well: its lines, a variable of type
`rows`, and its tags, an `array<text>`. The same functions work over
them. Here nothing is asked of another class: the lines are the
document's, so the figure is the same for every reader, and it is kept
with the document as any computed value is.

```json
{
  "name": "Bill",
  "title": "number",
  "variables": {
    "number":  {"type": "text", "label": "Number", "required": true},
    "lines":   {"type": "rows", "label": "Lines",
                "schema": {"what": "text", "qty": "decimal(0)", "price": "money(2)", "delivered": "bool"}},
    "tags":    {"type": "array<text>", "label": "Tags"},
    "count":   {"label": "Lines", "formula": "COUNT(lines)"},
    "net":     {"label": "Net", "formula": "SUM(lines.qty * lines.price)"},
    "dearest": {"label": "Dearest", "formula": "MAX(lines.price)"},
    "waiting": {"label": "Not delivered", "formula": "COUNTIF(lines, NOT delivered)"},
    "labels":  {"label": "Different tags", "formula": "COUNTUNIQUE(tags)"},
    "things":  {"label": "What it is for", "formula": "JOIN(lines.what, \", \")"},
    "urgent":  {"label": "Urgent", "formula": "CONTAINS(tags, \"urgent\")"},
    "dearest_thing": {"label": "Dearest thing", "formula": "FIRST(SORT(lines, {-price}).what)"},
    "to_deliver": {"label": "Still to deliver", "formula": "JOIN(FILTER(lines, NOT delivered).what, \", \")"},
    "top_two": {"label": "The two dearest lines", "formula": "SUM(TOP(SORT(lines, {-price}), 2).price)"}
  },
  "states": {"initial": "draft", "list": ["draft", "sent"]},
  "transitions": [
    {"name": "send", "from": "draft", "to": "sent",
     "guard": "COUNT(lines) > 0 AND ALL(lines, qty > 0)"}
  ],
  "access": {
    "read": "INGROUP(\"finance\")",
    "create": "INGROUP(\"finance\")",
    "update": "INGROUP(\"finance\")",
    "delete": "FALSE"
  }
}
```

Ewa writes the bill B-1 with three lines: 2 of paper at 10.50, 1 of
pens at 0.25, both delivered, and 3 of ink at 100.00, not delivered
yet. Its tags are red, urgent and red.

<!-- held figures -->
| What Ewa does | `count` | `net` | `dearest` | `waiting` | `labels` | `things` | `urgent` |
|---|---|---|---|---|---|---|---|
| writes B-1 with its three lines | 3 | 321.25 PLN | 100.00 PLN | 1 | 2 | paper, pens, ink | TRUE |
| marks the ink as delivered | 3 | 321.25 PLN | 100.00 PLN | 0 | 2 | paper, pens, ink | TRUE |
| takes the ink off the bill | 2 | 21.25 PLN | 10.50 PLN | 0 | 2 | paper, pens | TRUE |
| changes the number alone | 2 | 21.25 PLN | 10.50 PLN | 0 | 2 | paper, pens | TRUE |

- **A figure is worked out again when its list changes**, in the same
  commit, as any computed value follows what it reads. A change of
  something else leaves it.
- **It stands in a computed variable, a triggered one, a guard and a
  rule.** `send` is refused for a bill with no line, and for one with a
  line that has no quantity: the guard counts the lines.
- **An expression of the columns** stands in the one argument, each
  column written with the list before the dot:
  `SUM(lines.qty * lines.price)` multiplies line by line and adds. A
  bare name beside them is a variable of the document, the same for
  every line: `SUM(lines.qty * lines.price * rate)`.
- **In a form with a condition** the names are the row's, as in
  `QUERY`: `COUNTIF(lines, NOT delivered)`,
  `SUMIF(lines, delivered, qty * price)`. A variable of the document
  is written `THIS.name` there.
- **Tags are a list of single values.** `COUNT(tags)` counts the ones
  that have a value, `COUNTUNIQUE(tags)` the different ones. In a
  condition an element is called `value`: `ANY(tags, value = "red")`.
- **One value out of the lines** goes by the order they stand in on
  the bill: `FIRST(lines.what)`, `LAST(lines.what)`,
  `INDEX(lines.what, 2)`, and `JOIN(lines.what, ", ")` for all of them
  as one text. Over tags no column is named: `FIRST(tags)`,
  `CONTAINS(tags, "urgent")`. In a computed variable a place the bill
  does not have would refuse the commit with `out_of_range`; write
  `IFERROR(INDEX(lines.what, 2), "")` where a bill may be shorter.
- **A list with no element** follows the rules above: it counts 0, its
  sum of money is blank, nothing is first and nothing is joined.

The lines narrowed, ordered and cut inside a formula, for the same
steps:

<!-- held shaped -->
| What Ewa does | `dearest_thing` | `to_deliver` | `top_two` |
|---|---|---|---|
| writes B-1 with its three lines | ink | ink | 110.50 PLN |
| marks the ink as delivered | ink | blank | 110.50 PLN |
| takes the ink off the bill | paper | blank | 10.75 PLN |
| changes the number alone | paper | blank | 10.75 PLN |

`FILTER`, `SORT`, `TOP` and `UNIQUE` are written over the lines as
over a list that is a `QUERY`, and stand where the figures stand.
- **The list itself is no value**: `lines` alone in a formula is
  refused, and so is `lines.qty` outside a function over the list.

**Not built yet** (ADR-0081), each said when the class is defined or
the question asked:

<!-- held refused -->
| Written | Refused with |
|---|---|
| a figure over the lines in a read rule | `not_allowed` |
| a figure over the lines in a filter | `not_allowed` |
| a figure over the lines in a title | `bad_formula` |
| the list as a value | `bad_formula` |

Keep the figure in a computed variable, and read or filter that
variable: `count > 1` in a filter works. A page does not work such a
figure out by itself yet: it asks the server, as it does for a
`LOOKUP`.

## What is refused

A list that is wrong is refused when its class is defined, with the
place in the formula.

<!-- refused -->
| Written in a class | The class is refused with |
|---|---|
| a `QUERY` in a variable that is neither live nor triggered | `bad_formula` |
| a `QUERY` in a triggered variable that does not say its columns | `missing` |
| a `QUERY` inside a larger formula | `bad_formula` |
| a source nobody defined | `bad_formula` |
| a column the source has not | `bad_formula` |
| the middle value of each group, `MEDIAN` among the columns | `not_supported` |
| a function that is no total among the columns | `not_allowed` |
| a total over what is worked out, as `SUM(net * 2)` | `not_supported` |
| a total over a column the source has not | `bad_formula` |
| a sum over a text | `bad_formula` |
| the same total twice | `duplicate` |
| a list of totals sorted by what is none of its columns | `not_allowed` |
| a list of totals in a triggered variable, which would keep it | `not_supported` |
| a condition that is no condition | `type_mismatch` |
| a pointer compared with a text | `bad_formula` |
| `REF()` compared with a pointer to another class | `type_mismatch` |
| a `LOOKUP` in the condition | `not_allowed` |
| a live variable of the source as a column | `not_allowed` |
| braces anywhere else | `bad_formula` |
| a `REF` of a class nobody defined | `bad_formula` |

And the source may not lose what a list names: a new definition of
Invoice without `margin` is refused while the order lists it.

## Not built yet

Of the totals by group, which ADR-0090 proposes, each of these is
planned and waits its turn:

- the middle value of each group, `MEDIAN` among the columns;
- a list of totals that a triggered variable keeps;
- a figure over a list of totals, as `COUNT(by_owner)`;
- totals over the rows of a table.
