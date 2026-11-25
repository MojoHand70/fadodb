# A way in for messages

An action is a way out: when a document moves, the server sends a
request to another system. An inlet is the way in: another system
sends a request to the server, and a document is made or moved.

A program that was written for FadoDB needs no inlet. It sends a
[command](commands.md) with the key of a user. An inlet is for a
system that speaks only its own language: a bank that tells of a
payment, a shop that tells of an order. Such a system sends a message
in its own JSON, to one address, proves itself with a secret it shares
with the receiver, and sends the message again when it gets no answer.

An inlet is a definition, like a class and an action. It says how a
message proves who sent it and which one command it becomes. It is
data and not code: it is stored with a command, the server that is
running takes it at once, and its formulas read the message and
nothing else. Nobody writes or hosts a program.

The story of this chapter: Acme's bank tells Acme when a customer has
paid an invoice. Tomek runs Acme's server, Olga keeps the database,
and Dan designs its classes. A test plays every step against a server
and compares what the tool, the server and the bank's side read.

What is not here yet is listed at the end.

## The case

Acme's invoices, with the transition that only the payments may take:

<!-- the class -->
```json
{
  "name": "Invoice",
  "variables": {
    "customer": {"type": "text", "required": true},
    "total":    {"type": "money", "required": true},
    "paid_on":  {"type": "date"},
    "bank_ref": {"type": "text"}
  },
  "states": {"initial": "sent", "list": ["sent", "paid"]},
  "transitions": [
    {"name": "pay", "from": "sent", "to": "paid"}
  ],
  "access": {
    "read":   "INGROUP(\"finance\") OR INGROUP(\"payments\")",
    "create": "INGROUP(\"finance\")",
    "update": "INGROUP(\"finance\")",
    "delete": "FALSE",
    "transition": {"pay": "INGROUP(\"payments\")"}
  }
}
```

The bank sends a notice of every payment to an address Acme gave it,
signed with a secret the two share:

<!-- the message -->
```
POST https://fado.acme.example/v1/in/bank.paid
Content-Type: application/json
Bank-Signature: t=1794844920,v1=5257a869…

{"id": "ntc_8841", "type": "payment.received", "payment": {"reference": "FV/2026/11/0043", "amount": "2400.00", "currency": "PLN", "booked": "2026-11-16"}}
```

## The inlet

<!-- the inlet -->
```jsonc
{
  "name": "bank.paid",
  "label": "The bank says that an invoice was paid",
  "as": "bank-in",
  "connection": {"name": "bank", "secrets": ["signing_key"]},
  "proof": {"form": "timestamp.body", "header": "Bank-Signature", "secret": "signing_key"},
  "when": "JSONVALUE(BODY, \"type\") = \"payment.received\"",
  "key": "JSONVALUE(BODY, \"id\")",
  "command": "transition",
  "object": {
    "class": "Invoice",
    "id": "JSONVALUE(BODY, \"payment.reference\")",
    "transition": "pay",
    "values": {
      "paid_on": "DATEVALUE(JSONVALUE(BODY, \"payment.booked\"))",
      "bank_ref": "JSONVALUE(BODY, \"id\")"
    }
  }
}
```

<!-- the members of an inlet -->
| Member | What it says | When it is not given |
|---|---|---|
| `name` | who sends and what it says, joined by a dot. It is the end of the address: `/v1/in/bank.paid` | the inlet is refused |
| `label`, `description` | what the inlet is, for people | nothing |
| `as` | the user the command is carried out as: a user of the kind `service` | the inlet is refused |
| `connection` | the connection of the sender, and the secrets of it the proof may use | the inlet is refused |
| `proof` | how a message proves who sent it | the inlet is refused: there is no address the whole world may write to |
| `when` | which messages the inlet acts on, a condition | it acts on every message |
| `key` | what makes two messages the same message, a text | the bytes of the body do |
| `command` | what a message becomes: `create`, `set` or `transition` | the inlet is refused |
| `object` | the object of that command: `class` and `transition` as they are, `id` and every value under `values` a formula over the message | the inlet is refused |

- **One message, one command, one document.** A message makes one
  commit or none.
- **The values are typed when the inlet is defined.** `paid_on` is a
  day, so the text of the message goes through `DATEVALUE`; a text
  where a day goes is refused with `type_mismatch` and its place,
  before any message comes.
- **The class and the transition are names**, written as they are.
  The engine checks them, and every variable, against the class as it
  is when the inlet is defined.

## What a formula of an inlet reads

<!-- what a formula reads -->
| Name | What it is |
|---|---|
| `BODY` | the body, as a `json` value: a JSON body as it is, the fields of a form as an object of texts; blank when the body is neither |
| `HEADERS` | the headers of the message, as an object, their names in small letters |
| `PARAMS` | the parameters that follow `?` in the address, as an object |
| `RAW` | the body as text |

They are read with [the functions](functions.md) the language has,
`JSONVALUE` first: `JSONVALUE(BODY, "payment.reference")`,
`JSONVALUE(HEADERS, "x-shop")`.

- **A formula of an inlet reads the message and nothing else**: no
  document, no table, no clock and no secret. `TODAY()`, `USER()`,
  `LOOKUP` and `SECRET` are refused when the inlet is defined.
- **The header that carries the proof is not among `HEADERS`**, so
  that no formula copies a secret into a document.

## A list in a message

An order comes with its lines. commercetools says that Globex's order
1001 was placed, and the message holds a list, one element for each
line. A `rows` variable takes a repeat in place of a formula, and the
list becomes the lines of the document.

The class. `ct-in` is the service user of commercetools, in the group
`commerce`; Olga's team is the group `orders`:

<!-- a list: the class -->
```json
{
  "name": "Order",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "email":  {"type": "text"},
    "lines":  {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}}
  },
  "states": {"initial": "received", "list": ["received", "booking"]},
  "transitions": [{"name": "book", "from": "received", "to": "booking"}],
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

<!-- a list: the inlet -->
```jsonc
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

The message:

<!-- a list: the message -->
```jsonc
{"id": "evt-7781", "type": "OrderCreated",
 "order": {"id": "ct-1001", "orderNumber": "1001", "customerEmail": "buyer@globex.example",
  "lineItems": [
   {"variant": {"sku": "SOFA-3"},  "quantity": 1, "price": {"value": {"centAmount": 240000, "currencyCode": "PLN"}}},
   {"variant": {"sku": "CHAIR-1"}, "quantity": 4, "price": {"value": {"centAmount": 30000,  "currencyCode": "PLN"}}}
  ]}}
```

It makes the order `ct-1001` with two lines:

<!-- a list: the lines -->
| sku | qty | price |
|---|---|---|
| SOFA-3 | 1 | 2400.00 PLN |
| CHAIR-1 | 4 | 300.00 PLN |

<!-- a list: the parts of a repeat -->
| Part | What it says |
|---|---|
| `each` | where the list is in the body: a path, written as `JSONVALUE` takes one. It is a path and no formula, so it is checked when the inlet is defined |
| `as` | the name of one element. Inside `row` the element is a `json` value, read with `JSONVALUE`. It may not be `BODY`, `HEADERS`, `PARAMS` or `RAW` |
| `row` | a formula for each column it fills, typed against the column when the inlet is defined. A column it does not name is blank |

- **`BODY`, `HEADERS`, `PARAMS` and `RAW` are still there inside
  `row`**, so a line can take the currency of the whole order.
- **A message without the list, or with an empty one,** makes a
  document with no lines.
- **A repeat stands wherever an inlet gives values**: with `create`,
  `set` and `transition`. With `set` it gives the lines whole, as a
  `set` of a `rows` variable always does.
- **At most 10,000 elements**, as many as a document holds lines.

<!-- a list: what is refused -->
| The message | Is refused with | At |
|---|---|---|
| has something at the path that is no list | `bad_message` | `/object/values/lines/each` |
| has an element a formula fails on, such as a quantity that is no number | `bad_message`, and the words name the element: "the element 2 of order.lineItems" | the column, `/object/values/lines/row/qty` |
| has more than 10,000 elements | `bad_message` | `/object/values/lines/each` |

A refused message makes nothing, as any refused message does.

## How a message proves who sent it

The `proof` names a header of the message, a secret of the connection
and one of three forms:

<!-- the forms of a proof -->
| Form | What the sender puts into the header | Also takes |
|---|---|---|
| `shown` | the secret itself | `prefix`, what stands before it, such as `Bearer ` |
| `body` | the signature of the body: HMAC-SHA256 with the secret | `prefix`, such as `sha256=`, and `encoding`, `hex` or `base64`; `hex` when left out |
| `timestamp.body` | `t=<timestamp>,v1=<signature>`: the time in seconds since 1970, and the signature, in hex, of the timestamp, a dot and the body | nothing |

- **A message whose proof does not hold is answered
  `unauthenticated`**, whatever is wrong with it, and nothing of it is
  read.
- **With `timestamp.body` a message is taken for five minutes** after
  it was signed, so one that was overheard is not sent again later.
- **The engine compares in a time that says nothing** of where the two
  differ.
- **`timestamp.body` is how FadoDB signs what it sends** with the
  action `webhook.send` ([Actions](../actions.md)): a database that
  sends and one that takes understand each other with one shared
  secret.

The secret is Tomek's, kept beside the server and never in the
database: the file `bank/signing_key` in
[the directory of secrets](serving-a-database.md#the-secrets), or the
variable `FADODB_SECRET_BANK__SIGNING_KEY`.

## From nothing to a message

**Tomek turns the way in on.** Told nothing, a server takes no message
from outside. He adds one member to
[the settings](serving-a-database.md#the-settings), puts the secret
the bank gave him into the directory of secrets, and starts the server
again:

<!-- the settings -->
```jsonc
{
  "inbound": {}
}
```

**Olga makes the user the inlet acts as**, a service with the one
group the transition asks for:

<!-- olga: the user -->
```sh
fadodb exec create_user '{"id": "bank-in", "kind": "service", "groups": ["payments"]}'
```

**Dan checks the inlet**, in a file, with the class and a notice of
the bank he was sent as a sample. The check stores nothing and needs
no server:

<!-- dan: the check -->
```sh
fadodb inlet check bank-paid.json --class invoice.json --message notice.json
```

<!-- dan: the check, the answer -->
```
bank-paid.json: the engine takes it
the message notice.json: as bank-in, it becomes the command transition:
{
  "class": "Invoice",
  "id": "FV/2026/11/0043",
  "key": "in:bank.paid:ntc_8841",
  "transition": "pay",
  "values": {
    "bank_ref": "ntc_8841",
    "paid_on": "2026-11-16"
  }
}
```

`notice.json` holds the message as the command `try_inlet` takes it:

<!-- the sample -->
```jsonc
{"body": {"id": "ntc_8841", "type": "payment.received", "payment": {"reference": "FV/2026/11/0043", "amount": "2400.00", "currency": "PLN", "booked": "2026-11-16"}}}
```

**Olga defines it.** An inlet says whom the database takes a stranger
for, so defining one is an administrator's:

<!-- olga: the inlet -->
```sh
fadodb exec define_inlet --from definition=bank-paid.json
```

**The bank sends its notice**, and is answered:

<!-- the answer -->
```jsonc
{"ok":true}
```

The invoice is paid, by `bank-in`, with the day and the bank's number:

<!-- the invoice -->
```jsonc
{"state": "paid", "version": 2, "updated_by": "bank-in", "values": {"bank_ref": "ntc_8841", "customer": "Globex", "paid_on": "2026-11-16", "total": "2400.00 PLN"}}
```

## Twice is once

A sender that gets no answer sends its message again, and some send
every message twice to be sure. The inlet gives the command the key
`in:bank.paid:ntc_8841`, made of its own name and what its `key` reads
from the message. A command with a key that was used makes no second
commit ([the commands](commands.md)): the bank's second notice is
answered as the first was, and the invoice is at version 2 still.

- **The number of a message names one message.** The same number with
  another invoice in it is refused, with `key_reused`.
- **No caller's key may start with `in:`.** Otherwise a user of the
  database could use up the key of a notice before it came.

## What the sender is answered

The status says what happened, and the body says no more than a code.
A sender is outside the database: it is shown no document, no word of
a rule and no hint.

<!-- what the sender is answered -->
| What happened | Status | Body |
|---|---|---|
| the command was carried out, now or by an earlier message with the same key | 200 | `{"ok":true}` |
| the inlet does not act on the message: its `when` is not TRUE | 200 | `{"ok":true,"ignored":true}` |
| the proof does not hold | 401 | `{"error":{"code":"unauthenticated"}}` |
| no inlet of that name, or the server takes no messages | 404 | `{"error":{"code":"unknown_inlet"}}` |
| the message is not what the inlet reads | 422 | `{"error":{"code":"bad_message"}}` |
| the database refused the command: the invoice is paid already | 409 | `{"error":{"code":"wrong_state"}}` |
| the message is larger than the server reads | 413 | `{"error":{"code":"too_large"}}` |

- **A message the inlet does not act on is answered 200**, since a
  sender that is answered with a failure sends again and, after enough
  of them, stops sending.
- **Nothing of a message is kept** beyond what its command writes. A
  message the database refused is the sender's to send again.
- **The words of a refusal are Tomek's to read**, in the line the
  server writes.

## What the server writes and counts

A message has a line like a command's, under `in/` and the name of the
inlet, with the user the inlet acts as. For a message the database
refused, the line holds the words the sender was not given:

<!-- the lines -->
```jsonc
{"time":"2026-11-16T17:40:25.598Z","command":"in/bank.paid","user":"bank-in","status":200,"ms":0.490}
{"time":"2026-11-16T17:40:25.716Z","command":"in/bank.paid","user":"bank-in","status":200,"ms":0.095,"ignored":true}
{"time":"2026-11-16T17:40:25.677Z","command":"in/bank.paid","user":"","status":401,"code":"unauthenticated","ms":0.065}
{"time":"2026-11-16T17:40:25.754Z","command":"in/bank.paid","user":"bank-in","status":409,"code":"wrong_state","ms":0.264,"error":"pay leaves sent; the document is in paid [wrong_state]; no transition leaves paid"}
```

- **Nobody is named for a message without a proof**: it has not shown
  who sent it.
- **A line never holds the body of a message**, a header or a secret.

[The counts](serving-a-database.md#its-counts) have the messages of an
inlet under the same name, by status:

<!-- the counts -->
```
fadodb_requests_total{command="in/bank.paid",status="200"} 3
fadodb_requests_total{command="in/bank.paid",status="401"} 1
fadodb_requests_total{command="in/bank.paid",status="409"} 1
```

A message for a name that is no inlet is counted as `other`, so that a
stranger cannot make the page grow by sending names.

When it starts, the server says which inlets it takes messages at, and
names each one it cannot act on as it was started: an inlet whose
secret is in neither place, or whose user is disabled.

<!-- what the server says -->
```
fadodb serve: it takes messages from outside at /v1/in/<inlet>, for bank.paid
```

## Who may

<!-- who may -->
| Command | What it does | Who |
|---|---|---|
| `validate_inlet` | answers what is wrong with a definition, storing nothing | every caller |
| `try_inlet` | answers what an inlet would do with a message, and does nothing | a designer |
| `define_inlet` | stores a definition as a new version | an administrator |
| `remove_inlet` | closes the address; the versions stay | an administrator |
| `get_inlet`, `list_inlets` | read one, list them, each with its address | every caller |

- **An inlet does what its user may do, and no more.** The rules of
  the class judge `bank-in` as they judge anybody: it takes `pay`,
  and a message that would make an invoice is refused with
  `forbidden`.
- **The user is looked up for every message.** Olga disables
  `bank-in`, and every inlet that acts as it is closed: the next
  message is answered `unknown_inlet`.
- **Every version of an inlet is kept**, with who defined it:
  `definitions` lists them.

## What the server takes on

<!-- the members of inbound -->
| Member | What it says | When it is not given |
|---|---|---|
| `inbound` | the server takes messages from outside at the inlets of its database | it takes none: every address under `/v1/in/` answers `unknown_inlet`, and an inlet cannot be defined |
| `inbound.message_bytes` | the largest message, in bytes | 1048576, which is 1 MiB |
| `inbound.messages` | how many messages are worked at once | 16 |

- **A message has a place of its own**, apart from the 64 of the
  commands, so that a stranger who floods an inlet keeps no caller
  with a key waiting. A message beyond the places is answered `busy`
  at once, and its sender sends again.
- **A message is given as long as a command** to run, and is stopped
  the same way.
- **The body of a message has ten seconds to arrive.** A sender that
  stops in the middle of one is answered `bad_message` and gives its
  place back.

## In a program of your own

A program that opens the database itself says that it takes messages
when it opens it, with `Options.Inbound`, and hands a message to
`Receive`; one that mounts the door,
[`fado/httpdoor`](commands.md#over-http), sets `Inbound` in the door's
options and has the address. [Opening a database](opening-a-database.md)
lists the options.

## Not here yet

- **A file in a message**, as a mail gateway sends. An inlet reads
  JSON and forms.
- **Finding a document by a value.** The id of the document is in the
  message. A sender that knows only its own number is not served.
- **A reply shaped for the sender.** Some services first send a
  question and expect its text back before they trust an address; they
  cannot be joined to an inlet yet.
- **Several commands from one message**, and a message for a table.
- **A list of the messages the database refused.** The sender has it,
  and the lines of the server.
- **Limits for each sender**, such as messages a minute.
