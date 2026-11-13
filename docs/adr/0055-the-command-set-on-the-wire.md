# ADR-0055: The command set on the wire: every command, its JSON, and one address each

- Status: Proposed, with three questions open for the owner; amends ADR-0011 (the commands are named and listed; over HTTP an address is the name of a command, not a path that names a document; the WebSocket is left to the record of the change stream), ADR-0018 (how a document is read and changed over HTTP), ADR-0033 (the address a file is served at)
- Date: 2026-11-13

## Context

ADR-0011 decided that the engine has one set of JSON commands and that
HTTP, the WebSocket and the C ABI all carry it. It named ten commands
and no JSON. Since then the Go API has grown to 49 methods, and nothing
but a Go program can call any of them: `internal/server` is one comment.
Phase 6 builds the server, so the commands need their names, their JSON
and their answers before the first line of it.

The first caller is an agent with nobody watching. It sends a command,
reads the answer, and decides what to send next. What it needs from the
wire is what it has from the Go API today: a refusal with a stable code,
the place and a fix; a preview of anything that writes; a key that makes
twice once.

Facts this record was written from, each checked in the code on the day:

- **An id is any text.** `WithID` takes what the caller gives. The
  engine accepted `FV/2026/11/0042`, `a b`, `zażółć`, `a?b#c`, `..`,
  `due`, and ids with a line break, a tab and a zero byte in them. An
  invoice number with slashes is an ordinary id in the first country
  this is used in.
- **A reason is a sentence in the caller's language** (ADR-0052, item
  4). An HTTP header cannot be relied on to carry anything but ASCII.
- **The suites are scripts of commands already.** A step of
  `conformance/statechart`, `conformance/rules` and
  `conformance/access` is JSON that says `"do": "transition"`, as whom,
  with which values, and what to expect. They run against the store.
- **The architecture page draws the routes as paths that name the
  document**, such as `POST /v1/docs/{class}/{id}/transitions/{name}`,
  and ADR-0033 fixed one such path for files. Nothing was built on
  either.
- A door for agents that offers tools, as an MCP server does, offers one
  tool for each command, and keeps the ones that read apart from the
  ones that write.

## Decision

1. **A command is a name and one JSON object, and its answer is one
   JSON object.** Nothing else travels: no second argument, no header
   that changes what a command does, no answer that is a bare list or a
   bare number. Ann's agent approves an invoice:

   ```json
   {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve",
    "values": {"note": "matches order PO-118"},
    "key": "approve-FV/2026/11/0042", "business_date": "2026-11-13"}
   ```

   sent as the command `transition`, and is answered

   ```json
   {"document": {"class": "Invoice", "id": "FV/2026/11/0042", "version": 4,
                 "class_version": 1, "state": "approved",
                 "title": "FV/2026/11/0042",
                 "created_at": 1763020800, "updated_at": 1763035200,
                 "state_entered_at": 1763035200,
                 "created_by": "bob", "updated_by": "ap-agent",
                 "state_entered_by": "ap-agent", "state_entered_from": "review",
                 "values": {"customer": "Customer:42", "total": "1230.00 PLN",
                            "note": "matches order PO-118"}}}
   ```

   or, when the rule of the transition says no,

   ```json
   {"error": {"code": "forbidden",
              "message": "ap-agent may not take approve from review",
              "hint": "the rule of approve: INGROUP(\"finance\")",
              "path": "/access/transition/approve"}}
   ```

   The refusal is the one the Go API gives today, word for word.

2. **The commands are the calls of the Go API, one for one.** Every call
   of `fado.DB` that reads or changes the database is a command, with
   the call's name in lower case and underscores; nothing is a command
   that is not a call. The marks: R reads, W writes, D destroys. Who may
   send each is what ADR-0049 to ADR-0053 say of the call; this record
   changes none of it.

   Documents:

   | Command | Takes | Answers | |
   |---|---|---|---|
   | `create` | `class`, `values`, `id` when the caller gives one | `document` | W |
   | `create_many` | `class`, `documents`, each `{"values": …}` | `documents` | W |
   | `get` | `class`, `id` | `document` | R |
   | `set` | `class`, `id`, `values` | `document` | W |
   | `reset`, `refresh` | `class`, `id`, `variables` | `document` | W |
   | `transition` | `class`, `id`, `transition`, `values` | `document` | W |
   | `transition_many` | `class`, `transition`, `ids`, `values` | `outcomes`, one for each id: `id` and `document` or `refusal` | W |
   | `delete` | `class`, `id` | nothing, `{}` | D |
   | `transitions` | `class`, `id` | `transitions` | R |
   | `due` | `class` | `due` | R |
   | `history` | `class`, `id` | `versions` | R |
   | `version` | `class`, `id`, `version` | `version` | R |
   | `events` | `class`, `id` | `events` | R |
   | `preview` | `class`, `values`, `transition`, and `id` unless it is a create | `preview` | R |
   | `preview_delete` | `class`, `id` | `preview` | R |
   | `preview_many` | as `transition_many` | `outcomes` | R |
   | `query` | `class`, `where`, `order`, `limit`, `offset`, `fields` | `documents` | R |
   | `count` | `class`, `where` | `count` | R |
   | `sql` | `statement`, `args` | `columns`, `rows` | R |

   Definitions:

   | Command | Takes | Answers | |
   |---|---|---|---|
   | `define_class`, `define_schema`, `define_table`, `define_action` | `definition` | `class`, `schema`, `table` or `action` | W |
   | `define_classes` | `definitions` | `classes` | W |
   | `validate_class`, `validate_action` | `definition` | `diagnostics`, empty when nothing is wrong | R |
   | `validate_classes` | `definitions` | `diagnostics` | R |
   | `get_class`, `get_schema`, `get_table`, `get_action` | `name`, and `version` for an earlier version of a class or an action, which the store keeps | `class`, `schema`, `table` or `action` | R |
   | `list_classes`, `list_schemas`, `list_tables`, `list_actions` | nothing | `classes`, `schemas`, `tables` or `actions` | R |
   | `definitions` | `what`, `name` | `versions`, each with who defined it and when | R |
   | `preview_draft` | `definition`, `id`, `values`, `transition` | `preview` | R |
   | `migrate` | `class` | `migrated`, a count | W |

   Tables, actions and the queue:

   | Command | Takes | Answers | |
   |---|---|---|---|
   | `table_rows` | `table` | `rows` | R |
   | `import_rows` | `table`, `rows`, `replace` | `imported`, a count | W |
   | `preview_action` | `action`, `args` | `preview` | R |
   | `try_action` | `action`, `args` | `outcome` | W |
   | `outbox` | `status`, `class`, `id`, `limit` | `actions` | R |
   | `retry_action` | `action_key` | nothing, `{}` | W |
   | `drain` | nothing | `attempted`, a count | W |
   | `check_blobs` | nothing | `checked`, a count, and `mismatched` | R |

   A definition travels as JSON inside the command, not as text:
   `{"definition": {"name": "Invoice", "variables": …}}`. What a
   `get_class` answers under `class` is the definition as it was given,
   with `version`, `defined` (who, what they were, for whom, when) and
   `notes`, so that a designer reads back what a designer wrote.

   Not commands: `Open` and `Close`, which are the server's; `PutBlob`,
   `OpenFile` and `OpenBlob`, whose bytes travel beside the command set
   (item 9); `Sign`, `VerifySignature` and `Permanent`, which help a Go
   program and touch no database.

3. **Five fields mean the same in every command that takes them.** They
   are the options of the Go API:

   | Field | Go | Commands | Value |
   |---|---|---|---|
   | `key` | `Key` | W, D | text; a repeat with the same key makes no second commit and answers the first one's answer (ADR-0026) |
   | `business_date` | `BusinessDate` | W, D | `"2026-11-13"` |
   | `reason` | `Reason` | W, D | text; why a content administrator acts past a rule (ADR-0052) |
   | `at` | `At` | R | `"2026-11-13"`, `"2026-11-13 09:30:00"` in UTC, or seconds since the epoch (ADR-0042) |
   | `for` | `Principal.For` | all | the id of whoever the caller acts for; the engine checks it against a substitution (ADR-0053) |

   The key of a queued action, which `retry_action` takes, is
   `action_key`, so that `key` means one thing. The business day a host
   program gives with its caller (`Principal.Today`) is not a field: a
   command that means another day than the server's says
   `business_date`.

4. **Who calls is never in the command.** No field names the caller, its
   groups or what it is: a person, an agent or a service. They come with
   the door. Over HTTP the server works them out from the credential the
   request carries, which is the record on callers and keys; in a Go
   program the host gives them, as today. The one thing a caller says
   about itself is `for`, and that one the engine checks.

5. **A field a command does not know is refused**, and so is a field of
   the wrong JSON type. `{"class": "Invoice", "id": "7", "valeus": {…}}`
   sent as `set` is refused with `bad_json` and the path `/valeus`; it
   is not a `set` that changes nothing. An agent that misspells finds
   out at once, and a command sent to an older server that does not know
   a newer field is refused instead of half done.

6. **An answer names what it holds.** A document is under `document`,
   a list under its plural, a count under its name, as in the tables of
   item 2; a later version can add a member beside them. The document
   itself is the shape of ADR-0018 with what the Go API's `Document` has
   gained since: `class_version`, who created it, changed it and moved
   it into its state, the state it came from, and `computed`. A preview
   holds its document under `document` already, so an agent reads
   `document.state` the same way from a command and from its preview.

7. **A refusal is the Go API's error as JSON, under `error`**: `code`,
   `message`, and where they apply `hint`, `path`, `variable` and `pos`.
   The codes are the ones of `internal/store/errors.go`; a caller
   branches on the code and on nothing else. A definition that is
   refused answers `bad_definition` with every finding beside it, so
   that an agent fixes them all in one pass:

   ```json
   {"error": {"code": "bad_definition", "message": "2 findings refuse the definition"},
    "diagnostics": [
      {"code": "unknown_variable", "path": "/variables/total/formula", "pos": 4,
       "message": "…", "hint": "…"},
      {"code": "bad_formula", "path": "/transitions/1/guard", "pos": 0, "message": "…"}]}
   ```

   The wire adds five codes of its own, for what cannot happen to a Go
   program:

   | Code | When |
   |---|---|
   | `bad_json` | the request is not JSON, is not an object, or breaks item 5; the code a definition already uses |
   | `unknown_command` | no command has that name |
   | `unauthenticated` | the request carries no credential, or one the server does not know |
   | `too_large` | the request is larger than the server reads: 16 MiB unless the server is told otherwise |
   | `internal` | something failed that is no refusal; the words go to the server's log and not to the caller |

   A command that ran and has something to report is not a refusal: a
   preview that lists errors, and a `transition_many` in which some
   documents were refused, answer as commands that ran.

8. **Over HTTP a command is `POST /v1/<command>`**, the object as the
   body, `Content-Type: application/json`, and the answer as the body of
   the reply:

   ```
   POST /v1/transition
   Authorization: Bearer <the key of ap-agent>
   Content-Type: application/json

   {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}
   ```

   A command that reads is a POST too. The status is worked out from
   the code by this table, for the proxies and the tools in between; a
   caller reads the code:

   | Status | Codes |
   |---|---|
   | 200 | the command ran |
   | 400 | `bad_json` |
   | 401 | `unauthenticated` |
   | 403 | `forbidden`, `reason_required` |
   | 404 | `unknown_command`, `unknown_class`, `unknown_document`, `unknown_table`, `unknown_action`, `unknown_blob`, `unknown_row` |
   | 409 | `key_reused`, `duplicate`, `wrong_state`, `terminal`, `guard_false` |
   | 413 | `too_large`, `blob_too_large` |
   | 422 | every other refusal |
   | 500 | `internal`, `storage` |
   | 503 | `closed` |

   Why the address is the command and not a path that names the
   document:

   - An id is any text. `FV/2026/11/0042` in a path is four segments
     unless it is escaped, and an escaped slash is rewritten or refused
     by servers and proxies that stand in front. In a JSON body it is a
     string.
   - A path that ends in a word and a path that ends in an id cannot be
     told apart: `/v1/docs/Invoice/due` is the list of what is due, or
     the document whose id is `due`.
   - `DELETE` has no body, so its `reason` would go in a header, which
     cannot carry it, or in the address.
   - Each of these is a rule the HTTP door would have and no other door
     would. With the address as the command, HTTP, a tool for an agent,
     the C ABI and a line of the command-line tool all send the same
     object, and one table generates the description of all of them.

   What is given up: a document has no address a browser can open and
   cache. A command that reads may gain a `GET` form when a frontend
   needs the browser's cache, which the class bundle of Phase 7 will;
   that is that phase's to decide.

9. **Files travel beside the commands**, as ADR-0033 decided, at
   addresses that take the id as a parameter and not as a path:

   | Address | Does |
   |---|---|
   | `POST /v1/blobs?name=invoice.pdf` | takes the bytes as the body with their `Content-Type`; answers `{"file": …}`, the `File` fact to commit |
   | `GET /v1/file?class=Invoice&id=FV%2F2026%2F11%2F0042&hash=9f86…` | streams the file through the document that holds it, the hash as its ETag; `version=` for an earlier version |
   | `GET /v1/blob?hash=9f86…` | streams a blob by its hash alone, for a database administrator |

   An escaped slash in a parameter is safe where one in a path is not.
   This replaces the path ADR-0033 drew.

10. **One piece of Go turns a command into a call, and every door uses
    it.** It takes the caller, the name and the object, and returns the
    answer or the refusal. The table of item 2 is data in it: for each
    command its name, its fields, its answer, its mark and the Go call.
    From that table come the HTTP addresses, the OpenAPI description
    under `api/`, the help of the command-line tool, and the tools of a
    door for agents if the owner rules one into the phase. A test walks
    the methods of `fado.DB` and fails when one is neither in the table
    nor in the short list of item 2 that are not commands, so a call
    added to the Go API cannot be forgotten on the wire.

    The HTTP door is a package a host program can import, as
    `fado/outbound` is, and it takes the function that turns a request
    into a caller (ADR-0027). `fadodb serve` is that package with the
    engine's own keys.

11. **The suites run through every door and must answer the same.** A
    step of `conformance/statechart`, `conformance/rules` and
    `conformance/access` is run three ways: by the Go API, as today; as
    a command through item 10; and as a request to a server listening
    on the machine. One refusal code that differs, or one value, fails
    the build. This is the acceptance of the phase in the plan, and it
    makes the suites the specification of the command set, as they are
    of the statechart.

12. **`/v1` may grow and may not change.** A command, a field of a
    command or a member of an answer may be added. None is removed,
    renamed or given another meaning without a record, and after 1.0
    not at all inside `/v1` (ADR-0011). Item 5 is what makes adding
    safe to see: a caller that uses a new field against an old server
    is told.

13. **What this record leaves to the others of the phase.**

    - How a caller proves who it is, and the engine's own users, groups
      and keys.
    - The stream of changes, and with it whether a WebSocket is needed
      at all.
    - What `fadodb serve` is told when it starts: the address it
      listens on, the hosts its actions may call and where their
      secrets come from, and that an action implemented by a Go
      function has no function under it.
    - Layouts and the class bundle.

    And what it leaves out: several commands in one request; a command
    that changes a document only if it is still at a version the caller
    saw; pages of a long history or event list beyond what the Go API
    gives today. Each is a field or a command that item 12 lets in
    later.

## Found while writing this record

- **An id may hold a line break.** Nothing refuses one, or a tab, or a
  zero byte, which ends a text in C. Such an id cannot be typed, breaks
  a log line and a CSV export, and looks like another id on a screen.
  Proposed, question 3: an id may be any text but a control character.
  Slashes, spaces and letters of any alphabet stay.
- **A schema cannot be read back.** The Go API has `Class`, `Table` and
  `Action` and no `Schema`. `get_schema` and `list_schemas` are in the
  table of item 2, and the Go API gains the two calls with it.
- **A class is read back compiled.** `Class` answers the engine's own
  form. The store keeps the JSON of every version; `get_class` answers
  that, and the Go API gains a call that does.

## Questions for the owner

1. **One address for each command (item 8), or paths that name the
   document, as the architecture page drew them?** Proposed: one
   address for each command. The same invoice both ways:

   ```
   POST /v1/transition
   {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}

   POST /v1/docs/Invoice/FV%2F2026%2F11%2F0042/transitions/approve
   {}
   ```

2. **Is a field a command does not know refused (item 5), or passed
   over?** Proposed: refused.
3. **May an id hold a control character, such as a line break?**
   Proposed: no; every other character stays allowed.

## Consequences

- An agent learns one rule for the whole engine: send the command's
  name and one object, read one object back, branch on `error.code`.
  The same rule holds for a tool, a script with `curl`, a client in any
  language and, in Phase 9, the C ABI.
- The architecture page's table of routes is replaced by the table of
  commands when this record is accepted, and the sentence of ADR-0033
  on its two addresses by item 9.
- A proxy in front of the server can let a command through or stop it
  by its address, `/v1/sql` for one. It cannot tell one class from
  another by the address; the rules of the class do that.
- Reading is a POST, so nothing between the caller and the server
  caches an answer. For documents that change under rules this is the
  safe default.
- The Go API gains three small calls (item 2 and the findings), so that
  nothing is on the wire that a Go program cannot do.
- Every call added to the Go API from now on needs its row in the
  table, or the build fails.
- The clients of Phases 7 and 9 are generated from one table and have
  one method for each command.

## Performance

The budget of ADR-0011 stands: the server adds under 200 µs at p99 to a
transition over the Go API, measured on one machine over a connection
that is kept open, with the campaign of the phase's last step. The
document is written by the hand-written encoder of ADR-0018, which a
test holds to the bytes `encoding/json` writes for the same document.
Turning a command into a call is measured by itself, without a network,
by `BenchmarkExecTransition` beside `BenchmarkTransition`, and joins the
benchmark gate; its budget is set from its first measurement, in the
pull request that builds it, and recorded here when the record is
accepted.
