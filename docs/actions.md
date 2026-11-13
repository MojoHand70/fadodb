# Actions that reach other systems

A document acts on the world through the actions of its rules. Beside the
engine's own actions, which change documents and tables, a rule can call
an action that somebody defined. This page says how to define one for any
system that speaks HTTP, how to use it in a rule, and how to keep its
answer. The design is in
[ADR-0045](adr/0045-actions-are-an-extension-point.md).

**Status on 2026-11-12.** Everything on this page shipped with v0.4.0.
FadoDB carries no ready-made definitions for
particular services: by the owner's ruling they come with real needs,
and until then an integration is a definition you write, which takes
about twenty lines. A format for publishing several definitions together
is planned.

## Three ways to add an action

| You have | You write | Code |
|---|---|---|
| An API that speaks HTTP and takes a token | A definition with a `request` | None |
| A service of your own, in any language | A definition that points at it, and an endpoint that takes the signed call | Your service |
| A Go program that embeds FadoDB | A definition, and a Go function given to `Open` | A Go function |

A definition is JSON, stored in the database with `DefineAction`. It
holds no code and no secret.

## An action for a REST API

Suppose an API creates a ticket at `POST https://api.example.com/v1/tickets`,
takes a bearer token, and answers `{"id": "T-17", "status": "open"}`.

```json
{
  "name": "tickets.create",
  "label": "Open a ticket",
  "params": {
    "subject":  {"type": "text", "required": true},
    "priority": {"type": "text", "description": "low, normal or high"}
  },
  "returns": {"ticket": "text"},
  "connection": {"name": "tickets", "secrets": ["token"]},
  "request": {
    "method": "POST",
    "url": "\"https://api.example.com/v1/tickets\"",
    "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
    "body": {"subject": "subject", "priority": "IF(ISBLANK(priority), \"normal\", priority)"}
  },
  "ok": "STATUS = 201 OR STATUS = 200",
  "result": {"ticket": "JSONVALUE(BODY, \"id\")"}
}
```

- **`name`** is a namespace and a verb. **`params`** are what a rule gives
  the action and **`returns`** what it answers, each with a scalar type.
- **`request`** is what the engine sends. `url`, every header and every
  string of `body` is a formula over the parameters. Only pure functions
  are allowed there, so every attempt sends the same request.
- **`SECRET("token")`** is filled in when the request is sent, from the
  host program. A secret is never stored: not in the definition, the
  queued action, an event, a preview or an error message.
- **`ok`** is a condition over the reply: `STATUS`, a number, and `BODY`,
  the reply as JSON. Without it any 2xx reply is a success.
- **`result`** gives the returned values, each a formula over `STATUS`
  and `BODY`. `JSONVALUE` reads one value at a dotted path as text;
  `VALUE`, `DECIMALVALUE` and `DATEVALUE` convert it.

Other shapes of request use the same parts.

A `GET` says everything in its address, and `URLENCODE` makes a value
safe for it. Here the key travels in the address:

```json
{
  "name": "rates.get",
  "params": {"pair": {"type": "text", "required": true}},
  "returns": {"rate": "decimal(4)"},
  "connection": {"name": "rates", "secrets": ["key"]},
  "request": {
    "method": "GET",
    "url": "\"https://rates.example.com/v1/rate?pair=\" & URLENCODE(pair) & \"&key=\" & URLENCODE(SECRET(\"key\"))"
  },
  "result": {"rate": "DECIMALVALUE(JSONVALUE(BODY, \"rate\"), 4)"}
}
```

A receiver that does not read JSON gets a body that is one text, with
the `Content-Type` it expects:

```json
{
  "name": "sms.send",
  "params": {"to": {"type": "text", "required": true}, "text": {"type": "text", "required": true}},
  "connection": {"name": "sms", "secrets": ["key"]},
  "request": {
    "url": "\"https://sms.example.com/v1/messages\"",
    "headers": {"Authorization": "\"Key \" & SECRET(\"key\")", "Content-Type": "\"application/x-www-form-urlencoded\""},
    "body": "\"to=\" & URLENCODE(to) & \"&text=\" & URLENCODE(text)"
  }
}
```

For HTTP Basic authentication the host program supplies the encoded pair
as one secret, and the header is `"\"Basic \" & SECRET(\"basic\")"`.

### What the engine does with the reply

| The receiver | The action | Code on the queued action |
|---|---|---|
| answers 2xx, and `ok` is TRUE or absent | ran | |
| answers 2xx, and `ok` is not TRUE | is refused: dead at once | `not_ok` |
| answers 408, 429 or 5xx | is asked again, no sooner than `Retry-After` asks | `try_later` |
| answers anything else | is refused: dead at once | `refused` |
| does not answer | is asked again | `no_reply` |

An action that is asked again waits one second, then two, then four, up
to ten attempts, and is dead after the last. A dead action stays in the
outbox with what the receiver said, and `RetryAction` tries it again.
The engine adds `Idempotency-Key` to every request, the same at every
attempt, so a receiver can make twice once.

## What the host program decides

A definition is data, and an agent may have written it. Whether a
process reaches the network, where, and with which secrets is decided by
the program that opens the database.

```go
out, err := outbound.Allow(outbound.Options{
    Hosts: []string{"hooks.partner.example"},
    Connections: map[string][]string{
        "tickets": {"api.example.com"},
        "rates":   {"rates.example.com"},
    },
})
db, err := fado.Open(path, fado.Options{
    Outbound: out,
    Secrets: func(ctx context.Context, connection, name string) (string, error) {
        return vault.Get(ctx, connection, name)
    },
})
```

- **Nothing is sent** by a database opened without `Outbound`. A
  definition with a `request` cannot be defined there, and an action that
  needs one waits and then dies with `outbound_off`.
- **A secret goes only to its service.** A request of a definition that
  names a connection goes to the hosts of that connection and to no
  other. A request without a connection reads no secret and goes to
  `Hosts`.
- **Only https**, on port 443, unless an entry says `http://` or names a
  port. No redirect is followed. A host that only a suffix entry admits,
  such as `.example.com`, is refused when it resolves to an address
  inside the network; a host inside is listed by its own name.
- **`Secrets` is asked at every attempt**, so a token that is renewed is
  served like one that is fixed.

## Using an action in a rule, and keeping its answer

A rule names the action and gives each parameter a formula over the
document. `into` says which variables take the answer.

```json
{
  "name": "Complaint",
  "variables": {
    "subject":      {"type": "text", "required": true},
    "urgent":       {"type": "bool"},
    "ticket":       {"type": "text"},
    "ticket_error": {"type": "text"}
  },
  "states": {"initial": "received", "list": ["received", "filing", "filed", "stuck"]},
  "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"everyone\")", "update": "CREATEDBY() = USER()", "delete": "FALSE"},
  "transitions": [
    {"name": "file", "from": "received", "to": "filing"},
    {"from": "filing", "to": "filed", "guard": "NOT ISBLANK(ticket)"},
    {"from": "filing", "to": "stuck", "guard": "NOT ISBLANK(ticket_error)"}
  ],
  "rules": [
    {"on": "enter", "state": "filing",
     "do": [{"action": "tickets.create",
             "args": {"subject": "subject", "priority": "IF(urgent, \"high\", \"normal\")"},
             "into": {"ticket": "ticket", "ticket_error": "ERROR"}}]}
  ]
}
```

- The arguments are checked against the definition when the class is
  defined: a misspelt parameter or a formula of the wrong type is refused
  then, not when the first complaint is filed.
- The commit that enters `filing` queues the action. The outbox sends it
  afterwards.
- When the action has ended, done or dead, the engine writes the `into`
  values to the document as a set of its own. `into` reads the returned
  values, `OK`, whether the action ran, and `ERROR`, the code it failed
  with.
- After that set the document's own conditions are looked at, as after
  any change: here it moves to `filed` when a ticket came back and to
  `stuck` when the action failed. The process goes on from the answer.

The write is the engine's, so the rights that keep people from setting
`ticket` do not stand in its way. A document that is in a terminal state
or deleted when the answer arrives keeps what it has; the action's
outcome stands and the queued action says why the result was not stored,
under `into_code`. A rule that could never store its result, one on
`delete` or on the entry of a terminal state, is refused when the class
is defined.

The engine's own actions return something too: a `create` the `id` of
the document it made, a `transition` the `state` and the `version` it
left the other document in.

## Try it before a document depends on it

| Call | What it does |
|---|---|
| `ValidateAction` | Says what `DefineAction` would refuse, and stores nothing |
| `Actions` | Lists every action a rule may name, with its parameters and whether this process can run it |
| `PreviewAction` | Shows the request for given values, with every secret masked, and sends nothing |
| `TryAction` | Sends the request once, outside any document, and answers the result or the failure |

## A webhook

[`examples/actions/webhook.json`](../examples/actions/webhook.json) is a
definition that sends the event to one receiver. It has no `body`, so the
request carries the call itself: the arguments, and the event that fired
the rule with the document at that version. The address and the signing
key are secrets of its connection, so the same definition serves any
receiver. [`api/README.md`](../api/README.md) has the receiver's side:
the headers, the signature and what to answer.

A service that takes one webhook and passes it on to other applications
is a receiver like any other: its address is the connection's `url`
secret. FadoDB has not been tried against any particular one.

## A request written in the rule

`http.call` needs no definition. The rule gives the address, the method,
the headers and the body; it returns `status` and `body`. It has no
connection, so it reads no secret and goes only to `Hosts`.

```json
{"action": "http.call",
 "args": {"url": "\"https://hooks.partner.example/complaints\"",
          "body": {"subject": "subject"}},
 "into": {"ticket_error": "ERROR"}}
```

## A Go function

A program that embeds FadoDB can implement a definition in Go. The
definition has no `request`, and the function is given to `Open` under
the action's name.

```go
db, err := fado.Open(path, fado.Options{
    Actions: map[string]fado.ActionFunc{
        "books.post": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
            entry, err := books.Post(ctx, call.Key, call.Args, call.Event.Document)
            return fado.Values{"entry": entry}, err
        },
    },
})
```

An error is asked again later; an error wrapped with `fado.Permanent` is
a refusal. The call may arrive twice after a crash, each time with the
same `call.Key`.
