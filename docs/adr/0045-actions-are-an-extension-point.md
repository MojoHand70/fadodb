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

## Details settled while building

The first step built items 1, 4 and 9, and of items 2 and 5 the Go
function, on 2026-11-10, and settled these points.

- **Where a function comes from.** `Options.Actions` maps an action's
  name to its Go function and is given at every `Open`, so the worker
  has every function before it runs its first row; the definition comes
  through `DefineAction`. The process that defines a definition without
  a `request` must have its function, else the definition is refused
  with `not_supported`. A process that opens the file without the
  function lists the action as not available, takes classes that name
  it, and gets no answer for its rows, with the code `not_registered`
  and the usual retries (ADR-0027).
- **Names.** Identifiers joined by dots, a namespace before a verb, at
  most 100 characters, compared without regard to case like every name.
  A name the engine keeps is `reserved_name`.
- **Types.** A parameter and a returned value take any scalar type of
  ADR-0013, `ref` included, written as the type alone or as an object
  with `type`, `required` and `description`. `required` on a returned
  value is `not_allowed`.
- **Arguments.** A string is a formula, typed against its parameter. A
  `json` parameter takes a formula of type json or a template, nested at
  most 16 deep. The row and the call hold the arguments as an object by
  parameter in the wire shape; the function receives every parameter,
  one not given as null.
- **A required argument that is blank** is the action's refusal, not the
  command's, as ADR-0044 settled for a blank id: the row is queued and
  is dead with `required`, and the function is not called.
- **The arguments are read against the definition as it is when the
  action runs.** A redefinition may not take away or retype a parameter
  a rule gives; it is refused with `not_allowed` naming the rule, as for
  a class or a table. It may add a required parameter, since the rule
  cannot give it before it exists: until the rule is changed its actions
  are dead with `required`. A row that holds an argument the action has
  lost is dead with `unknown_parameter`.
- **What a function's error is.** The row records `action_error` with
  the error's words; an error of the store that the function passes on
  keeps its code; a panic is an error. The context of a call ends when
  the database closes, and a call cut short that way counts no attempt.
- **What a function returns** is read against `returns` by `TryAction`,
  which reports `bad_result`. The worker does not read it yet: nothing
  uses a result before `into`.
- **The JSON of a call** has `key`, `attempt`, `action`, `args` and
  `event`. The event has `event_id`, `kind`, `class`, `class_version`,
  `doc_id`, `name`, `from_state`, `to_state`, `version`, `document`,
  `actor_id`, `business_date`, `command_key` and `ts`. Its kind is one
  of `created`, `set`, `reset`, `refresh`, `transition` and `deleted`.
  The event of an eventless transition carries the key of the command
  that caused it. For a delete, `version` and `document` are the last
  version there was. The schemas are `api/action.schema.json`,
  `api/call.schema.json` and `api/event.schema.json`.
- **The worker** runs the engine's own actions one at a time and every
  other action on a goroutine of its own, up to `Workers` at once. A
  call in flight holds one worker and the later actions of its document,
  and nothing else. `Drain` runs calls one at a time on the caller's
  goroutine, and beside a running worker it leaves alone a document
  whose action is in flight.
- **A trial and a preview** take the arguments as values. The call of a
  trial has no event and a key that starts with `try:`. `TryAction`
  answers `ok`, the `result`, and for a failure the `code`, the
  `message` and whether the outbox would ask again. An engine action is
  not tried alone (`not_allowed`).
- **The catalogue** lists an engine action with the properties it is
  written with, each of the type `name`, `names`, `formula` or
  `formulas`.
- **Until the later steps of the phase.** Any action without a
  definition is `unknown_action`. Until the second step a definition
  with `request`, `connection`, `ok` or `result` was `not_supported`,
  and `http.call` in a rule was accepted and not run; until the third,
  `into` on an action was `not_supported`.
- **The registry** is the tables `actions` and `action_versions`, names
  no class may take. The engine schema stays at 3: tables are added and
  none is changed.

The second step built items 3, 6 and 7, and of items 4 and 5 the
request, on 2026-11-10, with the owner's ruling below, and settled these
points.

- **What sends.** `Options.Outbound` is an interface of two calls, one
  that says whether a request may go to an address and one that sends
  it. Package `fado/outbound` makes the one the record describes, from a
  list of hosts and, for each connection, its hosts. The store builds
  the request and reads the reply and never touches the network: a
  program that does not import `fado/outbound` links no HTTP client,
  which the dependency gate checks on the `fadodb` binary. `Options.Secrets`
  is a function of the connection and the secret's name.
- **A host entry** is a name, `.suffix` for every host under it,
  `name:port`, or `http://name` for a host that may be called without
  TLS. Without a port an entry admits the port of its scheme and no
  other. An entry by the host's own name wins over one by suffix. A host
  that only a suffix admits is refused when the address it connects to
  is loopback, private, link-local, shared or multicast; one listed by
  its own name may be inside the network. The address is judged when the
  connection is made, not at a lookup before it. No proxy of the
  environment is used.
- **What is checked when.** A definition with a request needs a process
  with `Options.Outbound` (`not_supported`), a connection that process
  names (`unknown_connection`), and `Options.Secrets` if the request
  reads a secret (`not_supported`). An address that reads no parameter
  and no secret is checked against the hosts when the action is defined
  (`not_allowed`); any other when the request is sent, where a host that
  is not allowed is a refusal, `host_not_allowed`, and nothing leaves.
  What a process lacks to send a row's request, the leave to send, the
  connection or a secret, is no answer, as a missing Go function is.
  A definition has a request or a Go function of its name, not both.
- **The request.** `method` is GET, POST, PUT, PATCH or DELETE, POST
  when absent. `url` and every header are formulas that give a text; a
  header whose formula gives a blank is not sent. `body` is a template
  like a json argument; a body that is one formula giving a text is sent
  as that text, for a receiver that does not read JSON, and the
  definition gives its `Content-Type`. A GET and a DELETE carry no body
  and may not have one, so the call of item 4 is the body of a POST, a
  PUT and a PATCH that give none. The engine adds `Content-Type` and
  `User-Agent: FadoDB` unless the definition gives them, and a
  definition may not give the engine's own headers, `Host` or
  `Content-Length`.
- **`SECRET("name")`** is a function class of its own in the catalogue.
  The name is a text literal and one the connection declares, so the
  secrets a request reads are known when it is defined. It compiles to
  the read of a slot that the store fills when it sends: a secret is no
  constant of a program. A secret the host does not supply, or supplies
  empty, is no answer, `secret_unavailable`.
- **The signature** is `FadoDB-Signature: t=<seconds>,v1=<hex>`, the
  HMAC-SHA256 of the timestamp, a dot and the body, keyed with the
  `sign` secret, at the database's clock. `fado.VerifySignature` is the
  receiver's check in Go.
- **Replies.** `STATUS` is a number and `BODY` the reply as json, blank
  when the reply is not JSON. The row records `no_reply` for no reply,
  `try_later` for 408, 429 and 5xx, `refused` for any other status that
  is no success, `not_ok` for a 2xx whose `ok` is not TRUE, and
  `reply_too_large`. A `Retry-After` moves the next attempt later, never
  sooner, and by a day at most. `ok` and `result` read the reply and
  nothing else: no parameter, no secret.
- **What a failure says.** The message quotes the first 200 characters
  of the reply on one line, since that is where a receiver says what was
  wrong. Every secret the request read, and every text built from one,
  is masked in it first; a failure to reach a receiver names the host
  and never the rest of the address.
- **`http.call`** is defined in every database and listed in the
  catalogue as a declared action. A rule gives it `url`, `method`,
  `headers` and `body` under `args`; it returns `status` and `body`. It
  has no connection: it goes to the open list of hosts and carries no
  secret. In a process that sends nothing, a class that names it is
  taken, and its rows get no answer, `outbound_off`, with the usual
  retries.
- **A preview** shows the request with `***` in the place of every
  secret and of the signature, asks the host for no secret, and is
  refused where the request would be.
- **`URLENCODE`** keeps letters, digits and `- . _ ~` and writes every
  other byte as `%XX` in upper case; its result counts against the text
  limit.

The third step built item 8, on 2026-11-10, and with the owner's
rulings below a sample definition and a guide in the place of the
packages of item 10. It settled these points.

- **Which actions carry `into`.** Every action of the outbox: one that
  has a definition, `http.call`, and the engine's own. A `create`
  returns `id`, a `transition` returns `state` and `version`, the state
  after the eventless transitions that followed; a `refresh` of another
  document and the table actions return nothing, and their `into` reads
  `OK` and `ERROR` alone. `into` on a synchronous action and beside
  `each` is `not_allowed`.
- **What `into` reads.** The returned values by name, `OK` and `ERROR`,
  with pure functions, and nothing of the document. A returned value may
  therefore not be named `OK` or `ERROR` (`reserved_name`). The targets
  are scalar input variables of the rule's class, typed when the class
  is defined.
- **`OK` and `ERROR`.** `OK` is TRUE when the action ran. `ERROR` is the
  code the action died with, and blank when it ran. When it ran and what
  it returned does not fit its definition, the returned values are
  blank, `OK` is TRUE, `ERROR` is `bad_result`, and the row says so.
- **When the result is written.** When the row is done or dead, never
  between attempts. A row written dead as too deep was never run and
  writes nothing back. A dead row that is retried by hand writes again
  when it ends again.
- **The commit of a result** is a set under the class as it is now, with
  the formulas of the rule as it was when the action was queued. Its
  event names the event of the action's outcome as its cause, carries
  the business date of the event that fired the rule, the principal
  whose command it was and the key `outbox:into:<outcome event>`. No
  write right is consulted. What its rules queue is one deeper in the
  chain than the action. A result that changes nothing commits nothing.
- **A result that cannot be stored** leaves the action's outcome as it
  stands: the document is in a terminal state (`terminal`), is gone
  (`unknown_document`), or refuses a value, with the code of that
  refusal. Nothing of the result is stored then, not the values that
  would have fitted. The row records the code and the message in two
  new columns, `into_code` and `into_message`, which `Outbox` and the
  view `<Class>_actions` show. **The engine schema is therefore 4.**
- **A rule that could never store its result** is refused when its class
  is defined (`not_allowed`): one on `delete`, on the entry of a
  terminal state, or on a transition into one. The exit of a state and a
  change of values say nothing of where the document ends, so they are
  taken, and the document answers when the result arrives.
- **A preview** lists the variables a queued action's result will go to;
  it runs nothing, so it has no result.
- **A preview of a request whose address is a secret** shows the address
  masked and does not judge it against the hosts.

## Rulings

**No definitions for particular services, for now.** Item 10 promised
three sample packages, `webhook`, `slack` and `sheets`. Asked on
2026-11-10 how the last two should be verified, the owner ruled that
they are not to be written: what the phase prepares is the extension
point and the generic ways in, a webhook and a call to any REST API, and
a definition for a particular service comes when a real user needs it.
He also named the services that take one webhook and pass it on to many
applications as a route to look at; the generic webhook is how one is
reached, and none has been tried. The repository gains
`examples/actions/webhook.json`, one definition whose address and
signing key are the connection's secrets, and `docs/actions.md`, which
says how to write a definition for any API.

**The package format waits for the first real integration.** Item 10
describes a package as a publisher's namespace, a version and a list of
definitions, installed by one call. Asked on 2026-11-10 whether to build
it now with the webhook as its only content, the owner ruled that it
waits: a real package will show what the format must carry, such as the
hosts each connection needs. Until then one definition is one JSON
document, which `DefineAction` takes. Item 10 is not built in this
phase.

**A secret goes only to the hosts of its connection.** The draft of the
second step put this to the owner on 2026-11-10: as items 6 and 7 read,
the hosts a process may call are one list, so a definition, which is
data and may be an agent's, could send the secret of one service to
another allowed host. He ruled that a secret is tied to its service. The
host program names the hosts of every connection; a request of a
definition that names a connection goes to those hosts and to no other;
and a request without a connection reads no secret and goes to the
hosts listed for such requests. This narrows item 7 and is how
`fado/outbound` is built.

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
