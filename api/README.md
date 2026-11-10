# Published schemas

JSON Schemas (draft 2020-12) of the shapes FadoDB hands to code outside
the engine. Each is a public contract from the release that ships it
(ADR-0026, ADR-0045): a change to one is a new decision record. They are
checked against what the engine does by `TestAPISchemas` in
`internal/store`.

| File | What it describes | Status |
|---|---|---|
| `action.schema.json` | An action definition: the JSON `DefineAction` takes, for an action a Go function implements and for one the engine sends as a request. | In `main`, ships with v0.4.0. |
| `call.schema.json` | The call an action receives: the key, the attempt, the arguments and the event. It is also the body of a request whose definition gives none. | In `main`, ships with v0.4.0. |
| `event.schema.json` | The event that fired the rule, with the document at that version. | In `main`, ships with v0.4.0. |

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
