# Published schemas

JSON Schemas (draft 2020-12) of the shapes FadoDB hands to code outside
the engine. Each is a public contract from the release that ships it
(ADR-0026, ADR-0045): a change to one is a new decision record. They are
checked against what the engine does by `TestAPISchemas` in
`internal/store`.

| File | What it describes | Status |
|---|---|---|
| `action.schema.json` | An action definition: the JSON `DefineAction` takes. | In `main`, ships with v0.4.0. Describes a definition a Go function implements; the properties of an action sent as an HTTP request are planned. |
| `call.schema.json` | The call an action receives: the key, the attempt, the arguments and the event. | In `main`, ships with v0.4.0. |
| `event.schema.json` | The event that fired the rule, with the document at that version. | In `main`, ships with v0.4.0. |

A Go function registered in `Options.Actions` receives the call as a
`fado.ActionCall`, whose JSON encoding is `call.schema.json`.
