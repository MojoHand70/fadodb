# ADR-0065: A way in for messages from outside

- Status: Proposed
- Date: 2026-11-16

## Context

An action is a way out: when a document moves, the server sends a
request to another system (ADR-0045). This record is about the other
direction: another system sends a request to the server, and a document
is made or moved. The owner ruled on 2026-11-14 that Phase 6 ends with
it, with a record of its own.

What a message from outside can do today:

- **A program written for FadoDB already has a way in.** It sends a
  command, `POST /v1/create` or `POST /v1/transition`, in the engine's
  own JSON, with the key of a user (ADR-0055, ADR-0057). An agent does,
  a script does, and so does a service that lets its user write the
  address, a header and the body by hand.
- **A system that speaks only its own language has none.** A bank that
  tells of a payment, a shop that tells of an order, a repository that
  tells of a merge: each sends a message in its own JSON, to one
  address, proves itself in its own way, most often with a signature
  made from a secret the two sides share, and sends the message again
  when it gets no answer. It cannot carry the key of a user in a
  header, and it cannot be taught the members of `transition`.

Between the two stands, today, a program that somebody has to write,
host and keep alive: it takes the message, checks the signature, and
sends the command. The owner's direction of 2026-11-13 is that people
will not write such programs; they will ask an agent to add a way in,
and nobody should be afraid to let it.

What is already built, and what this record therefore does not have to
invent:

- **A service is a user** with groups of its own, and the rules of the
  classes judge it like anybody (ADR-0057). A user `bank-in` of the
  group `payments` moves an invoice along `pay` and is refused
  everything else.
- **A command sent twice with the same key makes one commit**, and is
  answered the second time as the first (ADR-0055, item 3).
- **A definition is JSON whose strings are formulas**, which the engine
  checks when it is defined and an agent tries before it is used
  (ADR-0045).
- **A secret is the operator's**, kept beside the server and never in
  the database (ADR-0045, item 6; ADR-0059, item 5).
- **The engine's own way out signs what it sends** with a timestamp
  and a secret (ADR-0045, item 4).

So a way in needs three new things only: an address that takes a
message without a key, a check of who sent it, and the words that turn
the message into one command.

## Decision

1. **A way in is a definition, and the server runs it.** It is JSON,
   kept in the database with its versions beside the actions, and it is
   the mirror of a declared action: that one says what request the
   server sends, this one says what the server does with a request it
   is sent. This record calls the definition an *inlet*. Nobody writes
   or hosts a program.

   Acme's bank tells Acme when a customer has paid. The class, whole:

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

   The message the bank sends, to the address Acme gave it:

   ```
   POST https://fado.acme.example/v1/in/bank.paid
   Content-Type: application/json
   Bank-Signature: t=1794844920,v1=5257a869…

   {"id": "ntc_8841", "type": "payment.received",
    "payment": {"reference": "FV/2026/11/0043", "amount": "2400.00",
                "currency": "PLN", "booked": "2026-11-16"}}
   ```

   And the inlet, whole:

   ```json
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

   The server checks the signature, and then does what `bank-in` would
   do with a key of its own: the command `transition` on the invoice
   `FV/2026/11/0043`, along `pay`, writing the day and the bank's
   number, with the key `in:bank.paid:ntc_8841`. The invoice is paid,
   its history says by `bank-in`, and the bank is answered 200. This
   much was tried against the server as it is today, the command sent
   by hand in place of the inlet: `bank-in` takes `pay` with the two
   values, the same command again makes no second commit, and a
   `create` by `bank-in` is refused with `forbidden`.

2. **The address** is `POST /v1/in/<name>`, on the server that serves
   the database. A message carries no key of a user. It is the one
   address of the server, beside its health, that answers a caller who
   has none.

3. **A message proves who sent it, or nothing is done.** The `proof`
   of an inlet names a secret of its connection and one of the forms
   the engine knows:

   | Form | What the sender does | Who sends this way |
   |---|---|---|
   | `shown` | puts the secret itself into the header | a sender that cannot sign |
   | `body` | signs the body with the secret, HMAC-SHA256, and puts the signature into the header; `prefix` names what stands before it, such as `sha256=`, and `encoding` is `hex` or `base64` | the common form of the services that sign |
   | `timestamp.body` | signs a timestamp, a dot and the body, and puts `t=<timestamp>,v1=<signature>` into the header | FadoDB's own way out (ADR-0045, item 4), and the services whose form it follows |

   - The engine compares in constant time. A message whose proof does
     not hold is answered `unauthenticated`, and nothing of it is read.
   - With `timestamp.body` a message older than five minutes is
     refused, so one that was overheard is not sent again later.
   - **An inlet without a proof is refused when it is defined.** There
     is no address that the whole world may write to.
   - A form the engine does not know is a new row of the table, added
     when a real sender needs it, as the owner ruled for the ways out
     on 2026-11-10: the generic first, the particular when somebody
     needs it.
   - A database that sends with `webhook.send` and one that takes with
     an inlet of the form `timestamp.body` understand each other with
     no more than a shared secret.

4. **What an inlet reads of a message.** Its formulas see four values
   and nothing else:

   | Name | What it is |
   |---|---|
   | `BODY` | the body, as a `json` value: a JSON body as it is, a form's fields as an object of texts |
   | `HEADERS` | the headers, as an object, their names in small letters |
   | `PARAMS` | the parameters that follow `?` in the address, as an object |
   | `RAW` | the body as text |

   They are read with the functions the language has, `JSONVALUE`
   first. A formula of an inlet is pure: it reads no document, no
   table and no clock, and `SECRET` is not allowed in it. The secret is
   named in `proof` and used by the engine alone.

5. **What a message becomes: one command.** `command` is `create`,
   `set` or `transition`, and `object` is that command's object
   (ADR-0055), in which the class and the name of a transition are
   written as they are and every other text is a formula over the
   message. When the inlet is defined the engine checks it as it checks
   a rule that names an action: the class, the variables and their
   types, the transition, each mistake with its code and its place.
   One message makes one commit, or none.

   - **`when`** says which messages the inlet acts on. A sender often
     sends every kind of notice to one address. A message for which
     `when` is not TRUE is answered 200 and nothing is done, since a
     sender that is answered with a failure sends again and, after
     enough of them, stops sending at all.
   - **`key`** says what makes two messages the same message. The
     command carries `in:<name>:<key>` as its idempotency key, so a
     message that is sent twice makes one document, or one move, and
     is answered the second time as the first. Without `key` the key
     is the SHA-256 of the body: the same bytes are the same message.

6. **An inlet acts as a user, and an administrator says which.** `as`
   names a user of the kind `service`. The command is carried out as
   that user: the rules of the class judge it, and the event names it.
   So the worst an inlet can do is what its user may do: a service
   with narrow rights.

   Because an inlet says whom the server takes a stranger for,
   **defining one is an administrator's command**, as making a key is
   (ADR-0057, item 5). A designer, or an agent with a designer's
   rights, writes the definition, has the engine check it and tries it
   (item 9), and an administrator defines it. Question 3.

7. **What the sender is answered.** The status says what happened, and
   the body says no more than the code. A sender is outside the
   database: it is shown no document, no words of a rule and no hint.

   | What happened | Status | Body |
   |---|---|---|
   | the command was carried out, now or by an earlier message with the same key | 200 | `{"ok": true}` |
   | `when` was not TRUE | 200 | `{"ok": true, "ignored": true}` |
   | the proof does not hold | 401 | `{"error": {"code": "unauthenticated"}}` |
   | no inlet of that name, or the server takes no messages | 404 | `{"error": {"code": "unknown_inlet"}}` |
   | the message is not what the inlet reads: no JSON, a value it cannot turn into a day | 422 | `{"error": {"code": "bad_message"}}` |
   | the database refused the command | the status of its code (ADR-0055, item 8) | `{"error": {"code": "guard_false"}}` |
   | too large, or too many at once | 413, 503 | `too_large`, `busy` |

   The whole refusal, with its words, its hint and its place, is in the
   line the server writes (item 10), for Acme and not for the bank.
   **Nothing of a message is kept** beyond what its command writes: a
   message the database refused is the sender's to send again.
   Question 4.

8. **The server takes no message until the operator says so.**
   ADR-0059 made the server safe when it is told nothing, and its
   actions call no host until the settings list one. The same holds
   inward: without `inbound` in the settings every address under
   `/v1/in/` answers 404, and an inlet cannot be defined
   (`not_supported`), so an agent learns it at once.

   ```json
   {
     "inbound": {
       "message_bytes": 1048576,
       "messages": 16
     }
   }
   ```

   | Member | What it says | When it is not given |
   |---|---|---|
   | `inbound` | the server takes messages at the inlets of its database | it takes none |
   | `inbound.message_bytes` | the largest message, in bytes | 1048576, which is 1 MiB |
   | `inbound.messages` | how many messages are worked at once | 16 |

   A message has places of its own, apart from the 64 of the commands,
   so that a stranger who floods an inlet does not keep Ann from her
   invoices. The signing secret is a secret of the connection, found
   where the secrets of the actions are (ADR-0059, item 5): the file
   `bank/signing_key` in the directory of secrets, or
   `FADODB_SECRET_BANK__SIGNING_KEY`.

9. **The commands, and the check from the command line.**

   | Command | What it does | Who |
   |---|---|---|
   | `validate_inlet` | answers what is wrong with a definition, storing nothing | a designer |
   | `try_inlet` | takes a definition, or the name of one, and a message; answers whether `when` holds, the key, and the command with its object as it would be sent; sends nothing | a designer |
   | `define_inlet` | stores a definition as a new version | an administrator |
   | `remove_inlet` | closes the address; the versions stay | an administrator |
   | `get_inlet`, `list_inlets` | read one, list them, each with its address | a designer, an administrator |

   `fadodb inlet check FILE --message FILE` asks the engine about a
   definition in a file, as `fadodb class check` and
   `fadodb action check` do, and shows the command a message would
   become. An agent loops on it until it passes. The six commands are
   tools of the door for agents with no further work (ADR-0064).

10. **What the server writes and counts.** A message has a line like
    a command's (ADR-0059, item 9), with `in/bank.paid` for the
    command, the user the inlet acts as, the status and the code, and
    for a refusal of the database its words. It is counted in the
    counts under the same name. A name that is no inlet is counted as
    `other`.

11. **In a program of one's own.** The engine gains one call,
    `Receive`, which takes the name of an inlet and a message and
    answers what item 7 lists; `fado/httpdoor` mounts the address when
    it is told to. A host program that has its own way of knowing a
    sender skips nothing: the proof is the inlet's.

12. **What this record leaves out.**

    - **A file in a message**: a message with an attachment, as a mail
      gateway sends. An inlet reads JSON and forms.
    - **Finding a document by a value.** The id of the document is in
      the message. A sender that knows only its own number needs a
      look-up, which reads past the rules of the user, and is a record
      of its own.
    - **A reply shaped for the sender.** Some services first send a
      question and expect its text back before they trust an address.
      They cannot be joined to an inlet until a reply can be shaped.
    - **Several commands from one message**, and a message for a
      table.
    - **Limits for each sender**, such as messages a minute.

## Questions for the owner

1. **Where a way in lives.** Acme's bank can tell Acme of a payment.
   Dan, who designs Acme's classes, asks his agent to join the two.
   Proposed: the agent writes an inlet, a definition of twenty lines,
   and the server runs it. Nothing is hosted beside the server.
   The other way: no inlet in the engine. The agent writes a small
   program that takes the bank's message, checks the signature and
   sends `transition` with the key of `bank-in`; the repository ships
   one as an example, and a guide. It costs every company a second
   program to run, to secure and to keep alive for each sender, and it
   costs the engine nothing: no address that answers without a key.

2. **The word.** This record says *inlet*: `define_inlet`,
   `/v1/in/bank.paid`, "the inlet `bank.paid`".
   The other way: *channel*, which also means a place in Slack that
   an action posts to; or *webhook*, which most services call the
   message they send, and which is already the name of the way out,
   `webhook.send`.

3. **Who lets a stranger in.** The inlet `bank.paid` acts as
   `bank-in`. If Dan, a designer, could define it, he could define one
   that acts as any service of the database, with a connection whose
   secret he knows, and so do what that service may.
   Proposed: an administrator defines an inlet. Dan and his agent
   write it, check it and try it; Olga defines it.
   The other way: a designer defines it, and it stays closed until an
   administrator opens that version of it. Two more commands, and the
   same two people each time it changes.

4. **A message the database refuses.** The bank says that
   `FV/2026/11/0044` was paid, and that invoice is not in `sent`:
   `guard_false`.
   Proposed: the bank is answered 409 and nothing is kept. The line of
   the server says why. The bank sends the message again, as it does
   for any failure, and Acme reads the bank's own list of what it
   could not deliver.
   The other way: the server keeps every message it could not act on,
   in the database, for an administrator to read and to run again,
   as the outbox keeps a dead action. It is a second queue, and it
   holds what strangers sent: who may read it has to be ruled with it.

5. **What an inlet may do.** Proposed: one of `create`, `set` and
   `transition`, on one document.
   The other way: `create` only, so that a message can add to the
   database and change nothing in it, and a rule of the class does the
   rest; or every command that commits a document, `delete` among them.

## Settled while building the engine's part

The owner took the idea on 2026-11-16, once it was plain that an inlet
is data and no code ("Then we are fine"). Questions 2 to 5 are his
still; each is built as proposed and is small to change. Built first:
the engine's part, which is the definition, its six commands and the
Go call `Receive`. The address on the server, the setting `inbound`
and `fadodb inlet check` are the next step. Until then `fadodb serve`
takes no inlet: `define_inlet` answers `not_supported` there, as item
8 says of a server that takes no messages.

- **The program says that it takes messages**, with `Options.Inbound`,
  as it says with `Options.Outbound` that it sends requests. Without
  it an inlet cannot be defined and `Receive` answers `unknown_inlet`
  for every name.
- **An inlet needs a database that keeps its users** (`Options.Users`):
  it acts as one of them. When it is defined the user must be there,
  of the kind `service` and not disabled: `unknown_user` or
  `not_allowed`, at `/as`. The user is looked up again for every
  message, as it is then: an administrator who disables `bank-in`
  closes every inlet that acts as it, and the next message is answered
  `unknown_inlet`.
- **`get_inlet`, `list_inlets` and `validate_inlet` are open to every
  caller with a key**, as reading and checking a class and an action
  are (ADR-0051, item 4). Item 9 said a designer and an administrator.
  An inlet holds the name of a secret and never the secret.
- **The header that carries the proof is not among `HEADERS`.** With
  the form `shown` it holds the secret itself, and a formula that
  copied it would put the secret into a document.
- **The key of a message** is `in:<inlet>:<key>`, the key at most 200
  characters. **No caller's key may start with `in:`**, as none may
  start with `outbox:`: otherwise any user could use up the key of a
  message before the message came, and the message would be refused.
  The same number with other content is refused as a caller's reused
  key is, with `key_reused`.
- **What `Receive` answers of a proof that does not hold** is one
  error, `ErrNotProven`, whatever is wrong, as `Authenticate` answers
  one for every key it does not let through. A door answers it
  `unauthenticated`. A secret the program cannot supply is
  `secret_unavailable`, which names the secret and never holds it.
- **A body** is read as JSON when it is JSON, and as the fields of a
  form when the message says so. Anything else gives the formulas a
  blank `BODY`, and `RAW` still has the text.
- **A blank** where a message must give something is `bad_message`
  with the place: the key, when the inlet names one, and the id, for a
  `set` and a `transition`. A `create` that names no id is given one.
- **`remove_inlet` keeps the versions** and who defined them, and
  defining the inlet again is its next version. Who removed it is in
  the line the server writes for the command, and nowhere in the
  database.
- **A class that changes under an inlet** does not refuse the change
  and does not stop the database from opening. What the inlet says of
  the class is found when the next message comes, and refused then
  with the engine's own code, such as `unknown_variable`.
- **`try_inlet` answers the command and its object** and runs nothing.
  Whoever wants to see what the command would do sends the object to
  `preview`.
- **The layout of a database is 11**: the two tables of the inlets,
  whose names no class may take.

## Consequences

- A system that speaks only its own language is joined to a database
  by a definition that an agent writes and the engine checks, and by
  one secret the operator keeps.
- The server has an address that answers a caller without a key. It
  is off until the operator turns it on, it does nothing without a
  proof, it does at most what one service user may, and what it
  answers says nothing of the database.
- A way in and a way out are two definitions of the same kind, and
  two databases can be joined by them.
- The layout of a database changes: two tables for the inlets and
  their versions (the engine schema goes from 10 to 11).
- The command set grows by six commands, and the settings by one
  member.
- A sender that needs its reply shaped, sends files, or knows only
  its own numbers is not served yet.

## Performance

A message is one command and what stands before it: reading at most
1 MiB, one HMAC over it, and a few formulas over a value of JSON. The
budget proposed for the part before the command is 100 µs for a
message of 2 KiB, measured when it is built. The command is the
command's budget (ADR-0055). Nothing of this is on the path of a
command that comes with a key.
