# ADR-0076: A value worked out when it is read: the mode `live`, the list of other documents, and what a `ref` compares with

- Status: Accepted; amends ADR-0015 (a third mode of a computed variable), ADR-0016 and ADR-0020 (`QUERY` is built for a live variable, and how its lists are written), ADR-0019 (the function `REF`), ADR-0028 (a read right on a live variable), ADR-0038 (a `ref` in a filter is answered through its index), ADR-0046 (item 2: a live value reads with the reader's eyes; item 15: `QUERY`), ADR-0055 (one command joins the table), ADR-0070 (a live variable in the bundle); amended by ADR-0078 (the list that is kept)
- Date: 2026-11-20

## Context

On 2026-11-20 the owner asked for what forms call master and detail:
"a document in one process lists documents, which are in another
process", and "I click on a row containing a document and this
documents selected variables show-up in a designated target
container." An order shows its invoices; a click on one shows it at
the side.

What is clicked, and where the details appear, is the face's
(ADR-0069). What the engine owes is the list. Three things stand in
its way today.

- **Every computed value is kept.** A computed variable is worked out
  when a value it reads changes, and a triggered one at the moments its
  class names (ADR-0015, ADR-0043). Both are stored with the document
  and are part of its history. That is right for the rate an invoice
  was checked at. It is wrong for "the invoices of this order now": a
  kept list is old the moment an invoice is added, and one list is kept
  for everybody, though Jan may not read Piotr's invoices.
- **`QUERY` is not built.** It is in the language since ADR-0016 and is
  refused, "not implemented yet". ADR-0046, item 15, left how its
  columns and its sort are written to "the first real use". This is it.
- **A `ref` cannot be asked about.** A class can say that an invoice
  points at an order, `"order": {"type": "ref", "class": "Order"}`,
  and the value is `"Order:ORD-17"`. But a comparison wants two sides
  of one type (ADR-0019), and a formula can write nothing of the type
  `ref`: `order = "Order:ORD-17"` is refused. A `ref` may also point
  at a document that is not there, and nothing answers what points at
  an order.

The owner's direction, the same day: the pointer ties the list to its
document; and "we need a version of QUERY() which doesn't store data
in document", by a new argument or by a new function such as `LIST()`.
I had said the list was no `QUERY`, because a
`QUERY` in a formula is kept. He was right, and for a reason that
ADR-0075 makes plain: a list written in the class is a question the
engine knows before the first document exists, and so has its index
ready, while a filter a face sends is seen first when it runs. An
agent, which has no face, asks for the invoices of an order by name.

This record is one part of a larger question the owner opened on
2026-11-18, when a value on a document is worked out. It answers the
case of a value that is never kept. A list that is kept, and brought
again at moments the class names, stays open.

The case is Acme. Order ORD-17 is for Globex. Jan, in sales, wrote the
invoices FV-1 and FV-2 on it, Piotr, in sales too, FV-3. Ewa is in
finance and reads every invoice. Olga designs the classes.

## Decision

1. **A computed variable has a third mode, `live`.** It is worked out
   when somebody reads it, for that reader, and kept nowhere.

   | | A computed variable | A triggered variable | A live variable |
   |---|---|---|---|
   | Written with | `formula` | `formula`, `"mode": "triggered"`, `on` | `formula`, `"mode": "live"` |
   | Worked out | whenever a value it reads changes | when one of its triggers fires | when it is read |
   | In between | it follows the values it reads | it keeps its value | there is no in between: nothing is kept |
   | Reads other documents with | (it reads none) | the engine's eyes | the reader's eyes |
   | Is part of the document and its history | yes | yes | no |
   | A guard, a rule or a kept value may read it | yes | yes | no |

   The two classes of the case, whole:

   ```json
   {
     "name": "Order",
     "title": "number",
     "variables": {
       "number":   {"type": "text", "required": true, "label": "Number", "list": true},
       "customer": {"type": "text", "label": "Customer", "list": true},
       "owner":    {"type": "text", "default": "USER()"},
       "invoices": {"label": "Invoices", "mode": "live",
                    "formula": "QUERY(Invoice, {number, net, state}, order = REF(), {number})"}
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

   ```json
   {
     "name": "Invoice",
     "title": "number",
     "variables": {
       "number": {"type": "text", "required": true, "label": "Number", "list": true},
       "order":  {"type": "ref", "class": "Order", "label": "Order"},
       "net":    {"type": "money", "scale": 2, "label": "Net amount", "list": true},
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
       "*": {"margin": {"read": ["finance"], "write": ["finance"]}}
     }
   }
   ```

   `invoices` reads: of the class Invoice, the number, the net amount
   and the state of every invoice whose `order` is this document, by
   number.

2. **It reads with the reader's eyes** (amends ADR-0046, item 2, for a
   live variable alone). A kept value is one for everybody, so a
   triggered formula sees every document, and the rights on its
   variable decide who sees the result. A live value is worked out for
   one reader, so it shows that reader what the reader may read: the
   rows the source's read rule gives them, and in each row the
   variables its rights let them see (ADR-0028, ADR-0049). A count or a
   sum in a live formula counts what the reader may read.

   | Who reads `invoices` of ORD-17 | Rows |
   |---|---|
   | Jan | FV-1, FV-2 |
   | Piotr | FV-3 |
   | Ewa | FV-1, FV-2, FV-3 |
   | an agent of finance, which has no face | FV-1, FV-2, FV-3 |

   Had the list a column `margin`, it would be blank in what Jan and
   Piotr read. A content administrator who gives a reason reads past
   the rules, and the log of reads keeps the documents shown, as it
   does for `query` (ADR-0067).

3. **Nothing of it is kept.** A live variable has no column, no row of
   history and no event. It is not in the answer of `get`, `query`,
   `history` or the stream of changes, not in a view and not in an
   export. Nothing recalculates when the documents it reads change;
   they are read again when somebody asks.

4. **Nothing that is kept or decided may read it.** A live variable
   read by a computed or a triggered formula, a default, the title, a
   guard, an access rule, a right, the condition of a rule or the
   argument of an action is refused when the class is defined, with
   `not_allowed` and the hint to make it triggered. A decision stands
   on what the document holds, so that what a guard saw can be read
   afterwards. A live variable reads no other live variable either;
   that may be loosened when a case asks for it.

5. **What a live formula may read**: what a triggered formula may: the
   values the document holds, the caller, the clock, the state, the
   life of the document, tables and other documents. Not its own
   value, for it has none. So a live variable need not be a list:

   ```json
   "waiting_days": {"label": "Days waiting", "mode": "live",
                    "formula": "DAYS(TODAY(), DATEOF(ENTEREDAT()))"}
   ```

   is right every day and written by no commit.

6. **What else it may say.** `label`, `description` and `format`
   (ADR-0068); a place in a section (ADR-0069); a read right in
   `rights` (ADR-0028). Refused with `not_allowed`: `on`, `default`,
   `required`, `values`, `min`, `max`, `index` and `list`, which all
   speak of a kept value.

7. **It is read with a command of its own, `live`.** In Go it is
   `DB.Live`.

   | Member | Type | | What it is |
   |---|---|---|---|
   | `class` | text | needed | the name of the class |
   | `id` | text | needed | the id of the document |
   | `variable` | text | needed | the live variable |
   | `order` | list of texts | | for rows: the columns to sort by, a leading "-" for descending; the formula's own order when left out |
   | `limit` | whole number | | for rows: the most rows to answer; 1,000 when left out, 10,000 at most |
   | `offset` | whole number | | for rows: how many rows to skip |
   | `fields` | list of texts | | for rows: the columns to answer; every one when left out |

   And, of the members that mean the same everywhere: `reason` and
   `for`. It answers `value`, for a single value, or `rows` and
   `more`, which is true when rows follow the last one answered.

   **The formula says which documents; the caller says which page of
   them.** An order of Globex's may have twenty thousand invoices. The
   face asks for fifty, sorted as the person chose, and the engine
   reads fifty: order, limit and offset go into the one statement the
   formula became (ADR-0020), and nothing is fetched to be thrown
   away.

   Who may: whoever may read the document, and the variable by its
   read right. Jan asks for the invoices of ORD-17:

   ```jsonc
   {"class": "Order", "id": "ORD-17", "variable": "invoices", "limit": 50}
   ```

   ```jsonc
   {
     "rows": [
       {"id": "FV-1", "number": "FV-1", "net": "1000.00 PLN", "state": "draft"},
       {"id": "FV-2", "number": "FV-2", "net": "250.00 PLN", "state": "draft"}
     ],
     "more": false
   }
   ```

8. **A row that is one document says which.** When the source of a
   `QUERY` is a class, every row carries the `id` of its document,
   whether the formula lists it or not. That is what a click needs:
   the face reads the invoice with `get`, asks `transitions` for its
   buttons, and shows it where it likes. Details are an ordinary read
   of an ordinary document, by the reader's rights.

9. **`QUERY`, as far as a list needs it.** `QUERY(source, columns,
   condition, sort)` is built for a live variable. The source is a
   class or a table. Inside, a bare name is a column of the source and
   `THIS.name` a variable of the document, as in `LOOKUP` (ADR-0046,
   item 5). The columns are the variables of the source and its system
   columns; the answer has the type `rows`, each column with the type,
   the label and the format the source gives it.

   **Several columns are written in braces**, `{number, net, state}`,
   as a spreadsheet writes a list of constants, and so are several
   columns to sort by, `{-net, number}`, a leading `-` for descending
   as in the commands. One column needs no braces. The owner ruled so
   on 2026-11-22; it was question 1 of this record.

   Not built by this record: a `QUERY` in a triggered variable, whose
   rows are kept; grouping and aggregates among its columns; the
   `limit` argument, which the caller's page replaces for a live list.

10. **`REF` makes something a `ref` compares with** (amends ADR-0019).

    | Written | Is | May stand |
    |---|---|---|
    | `REF()` | the pointer to the document the formula belongs to | where `ID()` may |
    | `REF("Order", "ORD-17")` | the pointer to that document; the class is written as a literal, the id is any text | anywhere |

    The compiler knows the class each side of a comparison points at,
    and refuses a comparison of pointers to two classes when the class
    is defined: `order = REF()` written in a class `Customer` is
    refused with `type_mismatch`, "order points at Order, and this
    document is a Customer". A list of the wrong documents is caught
    before a document exists. A comparison of a `ref` variable with a
    `REF`, in the top-level `AND` of a filter or as the key of a
    `LOOKUP` or a `QUERY`, is answered through the index of the
    variable (ADR-0038, item 3; ADR-0037, item 5), which the engine
    keeps for every `ref` (ADR-0075).

11. **A `ref` is checked when it is set.** The document it names must
    be there, and the caller must be able to read it. Otherwise the
    change is refused with `unknown_document`, the same answer for
    "not there" and "not yours", as `get` gives, so that setting a
    pointer tells nobody what exists. A `ref` already in a document is
    not checked again when other values change. The owner ruled so on
    2026-11-22; it was question 2 of this record.

12. **A class says what points at it.** The answer of `get_class`
    gains `pointed_at_by`, worked out from the current definitions and
    written by nobody:

    ```jsonc
    "pointed_at_by": [{"class": "Invoice", "variable": "order"}]
    ```

    A designer who puts a list on the order's page, and an agent asked
    what belongs to an order, are offered the invoices and type no
    condition.

13. **In the bundle a live variable is the server's** (ADR-0070, item
    7): it carries `"server": true` in place of its formula, and the
    page asks `live`. A page could work out itself one that reads the
    document, the caller and the clock alone; this record does not
    build that.

14. **The stream of changes says nothing of a live value**, for no
    commit changed it (ADR-0058). A page that shows the invoices of
    ORD-17 hears of the events of the class Invoice it may read, and
    asks again.

## Questions for the owner

None is open. Question 1, how `QUERY` writes several columns, is
answered: item 9. Question 2, a `ref` that names a document which is
not there, is answered: item 11. Both are in the rulings below.

## The owner's rulings

Accepted by him on 2026-11-22: "accept all three and start building
QUERY." Nothing of the record was built when he accepted it.

Of 2026-11-20:

- That the pointer ties a list to its document: "Agreed with your
  proposal, the pointer (ID?)." A pointer holds the class and the id
  of the document it names.
- That the list is a `QUERY` in the class which is not kept: "we need
  a version of QUERY() which doesn't store data in document", by a new
  argument or a new function such as `LIST()`.
- To the question "`"mode": "live"` on the variable, so `LOOKUP` gets
  the same choice with no second function; the other way: your
  `LIST()`, which shows the difference in the formula itself but needs
  a twin for every reading function", he answered "yes".

Of 2026-11-22, to the two questions of this record as they were put
to him that day, numbered 2 and 3 after the one of ADR-0075:

- "QUERY's columns: in braces (my proposal), or in a text": **"2.
  yes"**. That is item 9. The case is the formula of `invoices` in the
  class Order: `QUERY(Invoice, {number, net, state}, order = REF(),
  {-net, number})`. Each name is a name to the checker: it is
  coloured, offered while typing, and found at its place when it is
  wrong; it is one new piece of grammar. The way not taken, a text:
  `QUERY(Invoice, "number, net, state", order = REF(), "-net,
  number")`, as the `QUERY` of Google Sheets takes one. Nothing new in
  the grammar; the names would sit inside a text, where an editor
  offers and colours nothing.
- "A pointer to a document that is not there: refused when set (my
  proposal), or taken as today": **"3. yes"**. That is item 11. The
  case: an agent files invoice FV-9 on `Order:ORD-71`, a slip for
  ORD-17, and is refused. A list never shows a document under an order
  that does not exist, and whoever loads documents from another system
  loads the orders before their invoices. The way not taken, as today:
  the pointer is taken, documents may be loaded in any order, and the
  slip is found when somebody misses the invoice.

Settled by me, his to veto: that a live value reads with the reader's
eyes (item 2; told to him with the mode, not put as a question); that
nothing kept or decided may read one (item 4); the command `live` and
its members (item 7); the `id` on every row (item 8); `REF` in place
of a `ref` that compares with a text (item 10: in the chat I had
sketched `order = ID()`, which would compare an order's pointer with
the id of a customer without a word); `pointed_at_by` (item 12).

Settled by me when the list was built, on 2026-11-22, his to veto:

- **The list before its index.** He said that day: "Each instance
  writes its data to a table together with instance id, so getting it
  is a query to the table with ID in where." A list is built as that
  query: it is handed to what answers the command `query` (ADR-0038),
  with the values of the document it is read on as parameters, so the
  read rule, the rights, the page and the log of reads are the ones a
  filter has. The index the engine keeps for every `ref` is ADR-0075's
  and not built; a `ref` that says `"index": true` is served by its
  index today.
- **`REF()` is that parameter.** It is built inside the condition of a
  `QUERY`, where it and `THIS.name` are values handed to the
  statement. The rest of item 10, `REF()` wherever `ID()` may stand
  and `REF("Order", "ORD-17")`, is not built.
- **The answer of `live` says the columns of a list**, beside `rows`
  and `more`: each with its name, its type and the label the source
  gives it, in the order of the formula. Item 9 gives a column those;
  a list with no rows needs them for its headings.
- **In Go the page of a list is `LivePage`**, and `Live` reads the
  first page. The command is one, `live`, as item 7 says.
- **Inside the condition `STATE()` and `ID()` are the listed
  document's**, as they are the row's in a filter (ADR-0040). The
  document the list belongs to is `REF()` and `THIS.name`.

## Consequences

- A list on a document is written once, in the class, and every face
  and every agent reads the same one, each with its own eyes.
- The engine knows the question when the class is defined, so its
  index is there before the first document (ADR-0075), and the list of
  one order is read in under a thousandth of a second among a hundred
  million invoices, as among a hundred thousand.
- A designer has three answers to "when is it worked out": by itself,
  at moments the class names, and when it is read. The third is the
  first value of a document that differs from reader to reader.
- A reader can no longer assume that everything shown on a document
  is in its history. A face should tell a live value apart.
- `QUERY` is in the language in earnest, and the formula language
  gains a list in braces. Three evaluators have to agree on `REF`
  (ADR-0003); `QUERY` is the server's alone, as `LOOKUP` is.
- Setting a `ref` costs one read of a document by its id.

## What this record leaves out

- **The face's part**: the list as a piece of a page, the row that is
  chosen, the container the details appear in. It is FadoDB Workshop's,
  in its own repository; new work for a wish first stated on
  2026-11-20.
- **A list that is kept**: a `QUERY` in a triggered variable, brought
  again at moments the class names. It waits for the owner's ruling on
  when a value brought from outside may be worked out again, opened on
  2026-11-18. Not decided.
- **Grouping, and totals among the columns** of a `QUERY`. Planned
  with the reports of Phase 8, which read everything (ADR-0075, item
  1). A single count or sum is a `LOOKUP` today (ADR-0046).
- **How many rows a live list has**, without reading them. `more` says
  only that there are more. Not planned; a face that shows the number
  asks `count`.
- **What a delete does to the pointers at a document.** Today nothing:
  the pointer stays and names a document that is gone. Not decided.
- **A list of pointers**, `array<ref>`, as the source of a list. Not
  planned.

## Performance

No budget of a commit changes: a live variable is in no commit.

The budget this record adds: **a live list narrowed by a `ref` to at
most 100 documents is read in under 1 ms from a class of 1,000,000**,
on a connection that reads, beside the writer. Measured on 2026-11-20
with what is built today, a `query` narrowed by an indexed text among
10,000,000 invoices: 0.05 to 0.08 ms; among 100,000,000, read from
the disk: 0.3 to 0.9 ms (ADR-0075). A benchmark in the
`store` package proves the budget and joins the gate when the mode is
built.

The check of a `ref` when it is set (item 11) is one read by the
primary key, 2 to 4 µs (ADR-0036), in the commit that sets it.
