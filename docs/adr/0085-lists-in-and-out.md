# ADR-0085: Lists in and out: the lines of a message, the lines of a request

- Status: Proposed; would amend ADR-0045 (an action takes a list and sends it, and its answer may give one), ADR-0065 (an inlet fills the lines of a document from a list in the message)
- Date: 2026-11-23

## Context

On 2026-11-23 the owner set the direction for every way in and out:
"EVERY client will want to get his data, this way or another." His
case was an order system. commercetools takes the orders, FadoDB works
them, factories make them, Microsoft Dynamics Business Central books
them, 100,000 orders a month. The same evening he answered "2. yes":
the engine first closes five holes that every way in and out runs
into, with the order system as the case, starting with lists in and
out. This record is those first two holes.

An order is a list of lines. Globex's order 1001, placed by Jan Nowak,
has one sofa made in Swarzędz and four chairs made in Kalisz.
commercetools tells of it in its own JSON:

```json
{"id": "msg-77f1", "type": "OrderCreated",
 "order": {"id": "ct-1001", "orderNumber": "1001",
  "customerEmail": "jan.nowak@example.com",
  "lineItems": [
   {"id": "li-1", "variant": {"sku": "SOFA-3"}, "quantity": 1,
    "price": {"value": {"currencyCode": "PLN", "centAmount": 240000, "fractionDigits": 2}}},
   {"id": "li-2", "variant": {"sku": "CHAIR-1"}, "quantity": 4,
    "price": {"value": {"currencyCode": "PLN", "centAmount": 30000, "fractionDigits": 2}}}
  ]}}
```

Business Central wants the same order back with its lines in one
request, `salesOrderLines`, a list.

What the engine does with this today, tried on main at 3208c91 with
`fadodb inlet check` and `fadodb action check`:

- **An inlet reads one line well.** With paths into the first element,
  `order.lineItems.0.variant.sku` and the like, the check makes
  `"first_sku": "SOFA-3"`, `"first_qty": "1"` and
  `"first_price": "2400.00 PLN"`, the price worked out from the cents.
  It cannot read the second line without a second set of formulas,
  and it cannot know how many lines there are.
- **An inlet cannot fill a document's lines.** Given a `rows` variable,
  the check answers: `not_supported at /object/values/lines: a rule
  cannot give lines: no formula gives a rows<sku text, qty int, price
  money(2)> in this build`.
- **An action cannot take a list.** A parameter with a schema is
  refused: `bad_json at /params/lines: unknown field "schema"`. A
  `json` parameter takes a template, but a template is written out
  whole: it has as many lines as its author wrote.
- **An action's answer cannot give a list.** `result` gives one value
  of a scalar type for each name of `returns`.

What exists to build on: a rule's `create` with `each` and `as`
(ADR-0044, item 12) already repeats something once for each line of a
`rows` variable, and reads a line by its columns, `line.sku`. This
record uses the same two words for the same idea, in three more
places.

This record does not touch the formula language. No formula gives a
whole list here, and none reads one: the repeat stands in the
definition, as `each` stands in a rule. So it is independent of the
work on functions over the lines a document holds (ADR-0081, step 2).

## Decision

1. **A list in a message becomes the lines of a document.** In the
   `values` of an inlet, a `rows` variable takes a repeat in place of a
   formula: `each`, the path to the list in the message; `as`, the name
   of one element; and `row`, a formula for each column.

   The class, whole. `ct-in` is the service user of commercetools, in
   the group `commerce`; Olga's team is the group `orders`:

   ```json
   {
     "name": "Order",
     "title": "number",
     "variables": {
       "number":   {"type": "text", "required": true},
       "email":    {"type": "text"},
       "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}},
       "bc_id":    {"type": "text"},
       "bc_error": {"type": "text"}
     },
     "states": {"initial": "received", "list": ["received", "booking"]},
     "transitions": [
       {"name": "book", "from": "received", "to": "booking"}
     ],
     "rules": [
       {"on": "enter", "state": "booking",
        "do": [{"action": "bc.sales_order",
                "args": {"customer": "\"WEB-PL\"", "lines": "lines"},
                "into": {"bc_id": "bc_id", "bc_error": "ERROR"}}]}
     ],
     "access": {
       "read":   "INGROUP(\"orders\") OR INGROUP(\"commerce\")",
       "create": "INGROUP(\"commerce\")",
       "update": "FALSE",
       "delete": "FALSE",
       "transition": {"book": "INGROUP(\"orders\")"}
     }
   }
   ```

   The inlet, whole:

   ```json
   {
     "name": "ct.order",
     "label": "commercetools says that an order was placed",
     "as": "ct-in",
     "connection": {"name": "ct", "secrets": ["api_key"]},
     "proof": {"form": "shown", "header": "x-api-key", "secret": "api_key"},
     "when": "JSONVALUE(BODY, \"type\") = \"OrderCreated\"",
     "key": "JSONVALUE(BODY, \"id\")",
     "command": "create",
     "object": {
       "class": "Order",
       "id": "JSONVALUE(BODY, \"order.id\")",
       "values": {
         "number": "JSONVALUE(BODY, \"order.orderNumber\")",
         "email":  "JSONVALUE(BODY, \"order.customerEmail\")",
         "lines": {
           "each": "order.lineItems",
           "as": "item",
           "row": {
             "sku":   "JSONVALUE(item, \"variant.sku\")",
             "qty":   "DECIMAL(VALUE(JSONVALUE(item, \"quantity\")), 0)",
             "price": "MONEY(ROUND(DECIMALVALUE(JSONVALUE(item, \"price.value.centAmount\"), 0) / 100, 2), JSONVALUE(item, \"price.value.currencyCode\"))"
           }
         }
       }
     }
   }
   ```

   The message above makes the order `ct-1001` with two lines: SOFA-3,
   1, 2400.00 PLN, and CHAIR-1, 4, 300.00 PLN. Each formula of `row` is
   the formula the check took today for the first line, with `item` in
   the place of `BODY` and the path.

   - **`each`** is a path into the body, written as `JSONVALUE` takes
     one: `order.lineItems`. It is a path and not a formula, so it can
     be checked when the inlet is defined.
   - **`as`** names the element. Inside `row` the element is a `json`
     value, read with `JSONVALUE`. `BODY`, `HEADERS`, `PARAMS` and `RAW`
     are still there, so a line can take the currency of the whole
     order. The name may not be one of the four.
   - **`row`** gives a formula for each column it fills, typed against
     the column when the inlet is defined. A column it does not name is
     blank. A name that is no column is `unknown_variable`, at its
     place.
   - **A message without the list**, or with an empty one, gives a
     document with no lines. A path that leads to something else than a
     list is `bad_message` at `/object/values/lines/each`. A formula
     that fails on one element is `bad_message` at its column,
     `/object/values/lines/row/qty`, and the words name the element:
     "the element 2 of order.lineItems". The message makes nothing, as
     any refused message does.
   - **At most 10,000 elements**, the cap of a composite (ADR-0013). A
     longer list is `bad_message`, and makes nothing.
   - **The repeat is allowed wherever an inlet gives values**: with
     `create`, `set` and `transition`. With `set` it gives the lines
     whole, as a `set` of a `rows` variable always does.
   - **`try_inlet` and `fadodb inlet check`** show the lines the message
     makes, row by row.

2. **A list goes out in a request.** A parameter of an action may be
   of the type `rows`, with a schema, written as a `rows` variable of a
   class is. The body of the request repeats a part once for each row.

   The action, whole:

   ```json
   {
     "name": "bc.sales_order",
     "label": "Create a sales order in Business Central",
     "params": {
       "customer": {"type": "text", "required": true},
       "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}, "required": true}
     },
     "returns": {"bc_id": "text"},
     "connection": {"name": "bc", "secrets": ["token"]},
     "request": {
       "method": "POST",
       "url": "\"https://api.businesscentral.dynamics.com/v2.0/globex/production/api/v2.0/companies(4f8a7c21-0d2e-4b8a-9c3e-1a2b3c4d5e6f)/salesOrders\"",
       "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
       "body": {
         "customerNumber": "customer",
         "salesOrderLines": {
           "each": "lines",
           "as": "line",
           "item": {
             "lineType": "\"Item\"",
             "lineObjectNumber": "line.sku",
             "quantity": "VALUE(TEXT(line.qty))",
             "unitPrice": "VALUE(TEXT(AMOUNT(line.price)))"
           }
         }
       }
     },
     "ok": "STATUS = 201",
     "result": {"bc_id": "JSONVALUE(BODY, \"id\")"}
   }
   ```

   When Olga moves `ct-1001` along `book`, the server sends:

   ```json
   {"customerNumber": "WEB-PL",
    "salesOrderLines": [
     {"lineType": "Item", "lineObjectNumber": "SOFA-3", "quantity": 1, "unitPrice": 2400},
     {"lineType": "Item", "lineObjectNumber": "CHAIR-1", "quantity": 4, "unitPrice": 300}
    ]}
   ```

   - **A `rows` parameter** has a schema of columns of scalar types.
     A rule gives it the name of a `rows` variable of its class:
     `"lines": "lines"`. When the class is defined the engine checks
     that the variable has every column the parameter has, of the same
     type; columns the parameter does not name are not sent.
     Anything else is `type_mismatch` at the argument.
   - **A repeat in the body** is an object with `each`, which names a
     `rows` parameter; `as`, the name of one row; and `item`, the
     template of one element, which may be an object, a list, a formula
     or a value, and may itself hold a repeat. Inside `item` a column is
     read as `line.sku`. The repeat becomes a JSON list with one element
     for each row, in the order of the rows. No rows give `[]`. A repeat
     may stand wherever a template may: in the body, and in a `json`
     parameter's template.
   - **What a value becomes in JSON** is as today: a number is a JSON
     number, and an exact value is its text, `"2400.00"`.
     `VALUE(TEXT(...))` makes a JSON number of it, as the example does
     for Business Central.
   - **The rows are read when the commit queues the action**, and kept
     with the queued action, like every argument (ADR-0044). A line
     changed after the commit does not change what is sent, at the first
     attempt or the tenth.
   - **`PreviewAction` shows the whole body**, every line, with the
     secrets masked.

3. **A list in an answer becomes the lines of a document.** A name of
   `returns` may be of the type `rows`, with a schema. Its `result` is
   a repeat over the answer, written as in item 1: `each`, a path into
   `BODY`; `as`; and `row`. `into` writes it to a `rows` variable of the
   class with the same columns, whole.

   A rule that pulls the lines of a sales order back from Business
   Central, after its warehouse changed one:

   ```json
   {
     "name": "bc.sales_order_lines",
     "label": "Read the lines of a sales order in Business Central",
     "params": {"bc_id": {"type": "text", "required": true}},
     "returns": {"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}},
     "connection": {"name": "bc", "secrets": ["token"]},
     "request": {
       "method": "GET",
       "url": "\"https://api.businesscentral.dynamics.com/v2.0/globex/production/api/v2.0/companies(4f8a7c21-0d2e-4b8a-9c3e-1a2b3c4d5e6f)/salesOrders(\" & bc_id & \")/salesOrderLines\"",
       "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"}
     },
     "result": {
       "lines": {
         "each": "value",
         "as": "l",
         "row": {
           "sku": "JSONVALUE(l, \"lineObjectNumber\")",
           "qty": "DECIMAL(VALUE(JSONVALUE(l, \"quantity\")), 0)"
         }
       }
     }
   }
   ```

   This is the pull of a list: one request, one answer, the lines into
   the document, and the document's conditions looked at afterwards, as
   after any `into` (ADR-0045). A list spread over several pages of
   answers is not read here.

4. **Where the lines of a request come from.** From a `rows`
   variable: lines a document holds. How lines that are documents of
   their own reach a request, Globex's OrderLine documents each with
   its own states, this record does not say. The owner deferred it on
   2026-11-23 with the relation of parent and child documents, which he
   wants as a concept of the engine, designed first (question 2).

5. **What changes.** The definitions of an inlet and of an action
   (`api/action.schema.json`, and the definition of an inlet in
   `api/openapi.json`), their checks and their trials, the manual's two
   chapters and `docs/actions.md`, and the skills that teach them. No
   command is added or taken away. The layout of a database does not
   change: a definition is kept as its JSON, and a queued action keeps
   its arguments as JSON already.

## The owner's answers

Each question was put to him in the chat on 2026-11-23, with a case,
what the record proposed and the other way. His words are given as he
wrote them.

1. **Where the lines of a message go.** Globex's order 1001 arrives
   with two lines.
   Proposed: into the lines (`rows`) of the one Order the message
   makes. If the process wants a document for each line, so that each
   has its own states and a factory sees only its own, the Order's rule
   makes them with `create` and `each`, which works today; tried on
   2026-11-23.
   The other way: the inlet itself makes the Order and one OrderLine
   for each element, several documents from one message, all in one
   commit or none. One step fewer, and the lines are not held twice. It
   costs an inlet that is no longer one command, with an id and a key
   for every document it makes.
   The owner answered "1. yes": as proposed.

2. **Where a request's lines come from when they are documents:
   deferred.**
   Globex books order 1001 after the chairs were cancelled: Business
   Central should get the sofa alone.
   Proposed: this record sends the lines a document holds. Lines as
   documents are sent once the kept list of ADR-0078 is built, with
   nothing added here. Until then a booking sends the lines as they
   were ordered.
   The other way: the argument of a `rows` parameter may be a `QUERY`,
   asked when the rule fires, with the eyes of whoever made the commit,
   and kept with the queued action. It sends the lines as they stand
   now, without waiting. It is a second way to read other documents
   inside a commit beside LOOKUP, which ADR-0078 set out to keep to
   one.
   The owner took neither. "We have to do it completely different. One
   of the worst and most difficult things in my previous system was to
   synchronise master document states and children documents states. I
   think we need to treat in FadoDB parent-child document relation as
   a first class concept, design it first and implement second." He
   named what he sees in it: children take the states of their parent
   and may add their own; moving the parent sometimes moves the
   children in the same state; moving all the children sometimes moves
   the parent; and sometimes the parent may not move unless some or all
   children are in a state. "Each child document can have an action
   'book' and depending on its state book to different accounts. It is
   a big subject and I think we should defer it for now." So this
   record sends only the lines a document holds (item 4), and the
   question waits for that concept's own design and record.

3. **A list in an answer, here or later.** A rule asks Business Central
   for the lines of a sales order.
   Proposed: in this record (item 3), with the same words as item 1.
   It is the pull of a list, which every way in needs.
   The other way: its own record later, with lists spread over pages of
   answers and fetched on a schedule.
   The owner answered "yes": as proposed.

## Consequences

- An order with its lines comes in from a shop in one message, and goes
  out to an accounting system in one request, with no program written
  and hosted between them.
- `each` and `as` mean one thing in four places: a rule that creates
  documents, an inlet, a request, an answer.
- An agent writes and checks the repeat as it writes any definition,
  with `fadodb inlet check` and `fadodb action check`, before a
  document depends on it.
- The two definitions grow, and so do their checks.
- Not served yet: lists inside lines; lists spread over pages; lines
  that are documents of their own, which wait for the relation of
  parent and child documents (deferred by the owner).

## What this record leaves out

The author's list. None of it was put to the owner; each item is not
built and not discussed.

- **A list inside a line**, such as the discounts of each line of an
  order. `each` inside `row` would read it into a second `rows`
  variable, which the class cannot nest today.
- **A list spread over pages of answers**, and a list fetched on a
  schedule. Question 3 says where they could go.
- **A list into a table**, such as a price list sent by a supplier,
  through `import_rows`.
- **Choosing or ordering elements on the way**, such as only the lines
  of one factory. The rule that takes the lines can do it, once
  functions over lines are built (ADR-0081).
- **Files and formats other than JSON**, such as CSV and XML. They are
  FadoConnect's, the second program the owner named on 2026-11-23.

## Performance

A repeat reads the message once and hands each element to its formulas
as a small JSON value of its own, not the whole message again. The
budget before the command, for a message with 100 lines of three
columns, about 20 KiB: under 1 ms. Today a message of 2 KiB with three
formulas takes 27 µs before its command (ADR-0065). The budget for
building a request of 100 lines of four values: under 1 ms. Two
benchmarks join the benchmark gate when this is built, one for each.
Nothing changes on the path of a command or an action that has no list.
