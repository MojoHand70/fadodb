# ADR-0045: Actions are an extension point: definitions, declared HTTP actions, registered Go actions, connections and results

- Status: Accepted; amends ADR-0009 (built-in actions only, external code through `http.call`), ADR-0011 (action definitions join the definitions the command set carries), ADR-0026 (the delivered event is any event, and its schema), ADR-0027 (an action is registered with a definition; `http.call` belongs to the library, behind a switch)
- Date: 2026-11-09

## Context

The owner's direction, given on 2026-11-09: actions are what opens FadoDB
to the world. A document must be able to call an API, deliver a webhook,
post a message to Slack, add a row to a Google spreadsheet. Other
developers must have a door to write their own actions, and a
marketplace of actions comes eventually.

What the records give today: ADR-0009 keeps the built-in actions in the
core and sends everything else through `http.call`; ADR-0027 lets an
embedder register a Go function under a name. With those two, a Slack
message is an `http.call` whose address, headers and token are spelled
out in every rule of every class that posts one. The token would sit in
class definitions, which are stored with every version and shipped to
browsers in the bundle. Nothing checks the arguments before the first
call fails. Nothing lists the actions a rule may use. An action's answer
goes nowhere, so a document can notify the world and never hear from it.
And there is nothing to publish: an integration is a habit of writing
rules, not a thing with a name.

The first caller is an agent. In its designer hat it needs to find out
what a rule can do, to add an action for an API the way it adds a class,
JSON in and diagnostics out, and to try it without moving a document.

## Decision

1. **An action has a definition.** It is JSON, like a class, a schema
   and a table: `name`, `version`, `label`, `description`, `params` and
   `returns`, each a map of names to the scalar types of ADR-0013 with
   `required` and a `description`, an optional `connection`, and what
   implements it. A name has a namespace and a verb, `slack.post`; names
   without a dot, `table.` and `http.` are the engine's.
   `ValidateAction` answers the diagnostics of a draft and `DefineAction`
   stores it in the `actions` registry with its versions. A rule names
   an action and gives its arguments under `args`, apart from the
   properties every action may carry, such as `into`:

   ```json
   {"on": "enter", "state": "approved",
    "do": [{"action": "slack.post",
            "args": {"channel": "\"#finance\"", "text": "\"Approved: \" & number"},
            "into": {"slack_ts": "ts"}}]}
   ```

   An argument is a formula over the document, or, for a `json`
   parameter, a JSON template whose every string is one. When the class
   is defined the binder checks the action, its parameters and their
   types: `unknown_action`, `unknown_parameter`, `missing`,
   `type_mismatch`, each with its path. The synchronous actions and the
   engine's outbox actions keep the arguments ADR-0043 and ADR-0044 give
   them, and are definitions too, in the catalogue of item 9.

2. **Three ways to implement one contract.**

   | Kind | Where the code is | For |
   |---|---|---|
   | Engine | in the engine: `set`, `reset`, `refresh`, `emit`, `create`, `transition`, `table.*` | the document, other documents, tables |
   | Declared | nowhere: the definition holds a `request`, and the engine sends it | any HTTP API, and any service a developer writes in any language |
   | Registered | a Go function the embedder passes with the definition | code in the embedder's process (ADR-0027) |

   No third party's code is loaded into the engine. ADR-0009 left WASM
   plugins to a record after 1.0, and this record leaves them there.

3. **A declared action** is a definition with a `request`:

   ```json
   {"name": "slack.post", "version": 1, "label": "Post a message to Slack",
    "params": {"channel": {"type": "text", "required": true},
               "text": {"type": "text", "required": true}},
    "returns": {"ts": "text"},
    "connection": {"name": "slack", "secrets": ["token"]},
    "request": {"method": "POST",
                "url": "\"https://slack.com/api/chat.postMessage\"",
                "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
                "body": {"channel": "channel", "text": "text"}},
    "ok": "JSONVALUE(BODY, \"ok\") = \"TRUE\"",
    "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}
   ```

   `url`, the `headers` and every string of `body` are formulas over the
   parameters, with pure functions and `SECRET` only, so every attempt
   sends the same request. The worker evaluates them when it sends, from
   the arguments the commit stored. `ok` and `result` are formulas over
   `STATUS`, a number, and `BODY`, the reply as a `json` value; without
   `ok` any 2xx reply is success. The catalog gains `URLENCODE`, a pure
   function, with its conformance cases. `http.call` is the declared
   action the engine defines for a call nobody named: `url`, `method`,
   `headers` and `body` as parameters, no connection.

4. **What a call carries.** Every call has the key of ADR-0044,
   `<event_id>:<idx>`, the attempt number, the action's name, the
   arguments, and the event that fired the rule in the delivered shape
   of ADR-0026, which is no longer the transition's alone: event id,
   kind, class and class version, document id, name, from and to state,
   the version, the document in the wire shape at that version, the
   actor, the business date, the command key and the timestamp. A
   registered action receives it as a Go value. A declared action sends
   `Idempotency-Key`, `FadoDB-Event` and `FadoDB-Attempt` as headers.
   When its `request` has no `body`, the body is the call itself as
   JSON, and when the request names a `sign` secret the worker signs the
   timestamp and the body with HMAC-SHA256 in `FadoDB-Signature`. That
   envelope is the protocol of a developer's own action: a service in
   any language that verifies the signature, treats the key as
   idempotent and replies with a JSON object, published by a definition
   that points at it. The JSON schemas of the event, the call and the
   action definition are published under `api/` and covered by the
   conformance suite.

5. **Replies and outcomes** (ADR-0044, item 4). A declared action ran
   when the reply is 2xx and `ok` is TRUE. No reply, a timeout, 408, 429
   and 5xx are no answer, retried, a `Retry-After` header moving the
   next attempt later. Every other reply, and a 2xx whose `ok` is not
   TRUE, is a refusal. A registered action ran when it returns no error;
   an error is no answer, and an error wrapped with `fado.Permanent` a
   refusal.

6. **Connections and secrets.** A definition names the connection it
   needs and the secrets of it. The embedder supplies them:
   `Options.Secrets` is a function from a connection and a secret's name
   to its value, asked at every attempt, so a provider that renews an
   OAuth token serves Google as it serves a fixed Slack token. The
   engine stores no secret: none in a definition, an argument, an outbox
   row, an event, a preview, an error or a log. `SECRET` is refused at
   definition time everywhere but in a `request`. A provider that fails
   is no answer.

7. **Outbound calls are off until the host allows them.** A class
   definition is data, and an agent may have written it; whether this
   process may reach the network is the host's decision. Without
   `Options.Outbound` a declared action cannot be defined
   (`not_supported`) and a row that needs one dies as one whose Go
   action is not registered does (ADR-0027). With it: a list of hosts
   that may be called, by name or by suffix; https, unless a host is
   listed with http; an address that resolves to a loopback, private or
   link-local range is refused unless its host is listed; redirects are
   not followed; ten seconds for a call and 1 MB of a reply, both
   options. The HTTP client is a package of the core module that only a
   program allowing outbound calls links, with the standard library and
   nothing else (ADR-0002).

8. **Results come back to the document.** A rule's action may carry
   `into`: a map from input variables to formulas over the action's
   `returns`, with `OK`, a bool, and `ERROR`, the code as text. When the
   row is done or dead, the worker applies `into` as a commit on the
   document that fired the rule, in the transaction that marks the row:
   an event of kind `set` caused by the action's event, the engine's
   write, typed at definition time. Like any `Set` it raises
   `values_change` and is followed by the eventless transitions, so a
   process goes on from the answer: a transition guarded by `NOT
   ISBLANK(payment_id)`, a rule on `values_change` of an error variable.
   `create` returns `id`; `transition` returns `state` and `version`.
   A `create` with `each` carries no `into` (ADR-0044, item 12).
   When the document is frozen or gone, or refuses the values, the
   action's outcome stands and the row records why the result was not
   stored. The depth bound of ADR-0044 counts these commits.

9. **The catalogue and the trial.** `Actions()` lists every action a
   rule may name in this database and this process, of the three kinds,
   with its parameters, their types and descriptions, what it returns
   and the connection it needs. `PreviewAction(ctx, name, args)` answers
   what would be sent, the request or the call, with secrets masked, and
   sends nothing. `TryAction(ctx, name, args)` runs the action once
   outside any document, queues nothing, writes no event and answers the
   result or the refusal.

10. **A package** is a JSON document: a publisher's namespace, a
    version and a list of action definitions. Installing it is
    `DefineAction` for each. It holds no code and no secret, which is
    what makes it safe to publish, to review and to install, and
    readable by the agent that will use it. The repository gains
    `actions/` with three packages as the proof of the format,
    `webhook` (the envelope of item 4 to an address of the connection,
    signed), `slack` and `sheets`, run in CI against a local server that
    replays recorded replies and not against the live services. A
    registry of packages, their signing and their review are not
    designed here and not scheduled; the format is the part that must
    not change under them.

## Consequences

- An integration is a named thing with typed parameters. A rule that
  misspells a parameter is refused when the class is defined, not when
  the first invoice is approved.
- Most integrations need no code: an API with a token is a definition of
  twenty lines, which an agent can write, validate, preview and try in
  one sitting. What a template cannot say, a flow of several calls or a
  reply that needs parsing beyond `JSONVALUE`, is a service behind the
  envelope or a Go function.
- A developer's door has one protocol and no SDK: an HTTPS endpoint, a
  signature, a key, a JSON reply. The same definition format publishes
  an action for someone else's API and for one's own service.
- A document hears back. `into` turns an action from a notification into
  a step of the process, at the price of one more commit per answered
  action and of chains the depth bound must stop.
- Secrets are the host's. A database file, an export or a bundle never
  holds one, and a definition can be published as it is. A host without
  a secret provider can run only actions that need none.
- The envelope sends the whole document at that version to the address
  the class's author chose. No principal's read rights apply to it: an
  author who must hide a variable from a receiver writes a `body`.
- A database whose rules call the network does nothing outbound in a
  process that has not allowed it. The rows die and say so, as ADR-0027
  words it for a missing Go action.
- The definition format, the envelope and the delivered event are public
  contracts from the release that ships them, as ADR-0026 says of the
  event; a change is a new record.
- `fadodb serve` (Phase 6) reads its allowed hosts and its secrets from
  its configuration and carries `DefineAction` in the command set; the
  MCP server (Phase 11) lists the catalogue to agents.

## Performance

Budgets. A definition is compiled once, like a class. The commit pays
the arguments and the outbox row (ADR-0044) and nothing for the kind of
action. Beside the network, a declared action costs the worker its
templates, the JSON of the request and the reply, and the mark: under
100 µs per call, measured against a server in the same process by
`BenchmarkDeclaredAction` and published in `docs/perf/v0.4.0.md` with
the outbox figures. A binary that does not allow outbound calls links no
HTTP client; the size gate of ADR-0012 measures the `fadodb` binary,
which links one when the server arrives.
