# The description of the commands, and the published schemas

## `openapi.json`: the commands, for other languages

Every command of a database, as one OpenAPI 3.1 document: its address,
`POST /v1/<command>`, the members of its object with their types, the
members of its answer, and how a refusal looks. The three addresses of
the files are in it, and the health and the counts that `fadodb serve`
answers. A client in another language is generated from it with the
tool of that language.

- **It is written from the engine's own table of the commands**
  (ADR-0055, item 10) and is never edited by hand: `make docs` writes
  it, and a test fails when it is not what the engine has.
- **A test holds it to a door**: every address is answered, a member
  the document does not list is refused, and a command needs what the
  document says it needs.
- **Three fields are FadoDB's own.** `x-fado-mark` says whether a
  command reads, writes or destroys; `x-fado-call` names the Go call
  it is; `x-fado-stream` marks `subscribe`, whose answer is a reply
  that stays open ([the stream of changes](../docs/manual/the-stream-of-changes.md)).
- **Its version is 1**, the `v1` of the addresses.
- **An answer is described by the names of its members**, and not yet
  by their types: a generated client reads an answer as plain JSON.
  [The commands](../docs/manual/commands.md) says the same in words.

## Published schemas

JSON Schemas (draft 2020-12) of the shapes FadoDB hands to code outside
the engine. Each is a public contract from the release that ships it
(ADR-0026, ADR-0045): a change to one is a new decision record. They are
checked against what the engine does by `TestAPISchemas` in
`internal/store`.

| File | What it describes | Status |
|---|---|---|
| `action.schema.json` | An action definition: the JSON `DefineAction` takes, for an action a Go function implements and for one the engine sends as a request. | Shipped with v0.4.0. |
| `call.schema.json` | The call an action receives: the key, the attempt, the arguments and the event. It is also the body of a request whose definition gives none. | Shipped with v0.4.0. |
| `event.schema.json` | The event that fired the rule, with the document at that version. | Shipped with v0.4.0; what the actor was and for whom it acted since v0.5.0. |

A Go function registered in `Options.Actions` receives the call as a
`fado.ActionCall`, whose JSON encoding is `call.schema.json`.

## A developer's own service

A service in any language is an action when a definition points at it.
The engine sends it a POST whose body is the call, with these headers:

| Header | What it carries |
|---|---|
| `Idempotency-Key` | The key of the queued action, the same at every attempt. A request may arrive twice; the service makes twice once by this key. |
| `FadoDB-Event` | The id of the event that fired the rule. Absent in a trial. |
| `FadoDB-Attempt` | The count of the attempts, from 1. |
| `FadoDB-Signature` | `t=<seconds since the epoch>,v1=<signature>`, when the definition names a `sign` secret. |

The signature is the HMAC-SHA256, in lower-case hex, of the timestamp, a
dot and the body exactly as received, keyed with the secret the service
and the host program share. The service computes it, compares in
constant time and refuses a timestamp far from its own clock;
`fado.VerifySignature` does this in Go. It answers 2xx with a JSON
object when it took the call, 408, 429 or 5xx to be asked again, with
`Retry-After` if it knows when, and any other status to refuse for good.
