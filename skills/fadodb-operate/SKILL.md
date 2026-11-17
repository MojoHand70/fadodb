---
name: fadodb-operate
description: Run a FadoDB process on behalf of a person or a system. Create documents, set values, move transitions, read, filter and query, always as a principal, with an idempotency key on every commit and a preview before it. Use this skill whenever an agent has to act on records that live in FadoDB, such as submit, approve, pay, close, reopen, update, retry or resume a document, find out what a document allows next, or explain why a transition or a change was refused. Use it even when the request names the business object, the invoice, the permit, the leave request, and never says FadoDB.
metadata:
  hat: operator
  status: written
---

# Operate a FadoDB process

**Status: written, 2026-11-14.** This page says how to work on the
documents of a class that exists: what to read in the manual, one
piece of work command by command with the engine's own answers, and
what goes wrong most often. A test on each push defines the class this
page shows, sends its commands and compares the answers with the ones
below.

## Read first

| Read | For |
|---|---|
| [`docs/manual/commands.md`](../../docs/manual/commands.md) | every command: the JSON it takes and the JSON it answers. Check a command here before you send it |
| [`docs/manual/notions/who-may.md`](../../docs/manual/notions/who-may.md) | what a class asks about its caller, and what a caller who may not see is told |
| [`docs/manual/notions/states-and-transitions.md`](../../docs/manual/notions/states-and-transitions.md) | the two questions before every move, and values given with a move |
| [`docs/manual/notions/history-and-the-log.md`](../../docs/manual/notions/history-and-the-log.md) | versions and events, the key of a command sent twice, previews |
| [`docs/manual/notions/time.md`](../../docs/manual/notions/time.md) | what moves by itself, and reading as of another day |
| [`docs/manual/refusals.md`](../../docs/manual/refusals.md) | every code the engine answers with, and what it means |
| [`docs/manual/naming-the-caller.md`](../../docs/manual/naming-the-caller.md) | what the program that runs you tells the engine about you. You never say who you are |
| [`docs/manual/the-outbox-and-the-worker.md`](../../docs/manual/the-outbox-and-the-worker.md) | for an operator who is an administrator: the queue of actions, and a dead one |

## A piece of work, command by command

The class. An employee files an expense, and the approver it names
approves it, up to 5000.00, or rejects it with a note.

```json
{
  "name": "Expense",
  "title": "\"Expense of \" & employee",
  "variables": {
    "employee": {"type": "text", "default": "USER()"},
    "approver": {"type": "text", "required": true},
    "what":     {"type": "text", "required": true},
    "amount":   {"type": "money", "scale": 2, "required": true},
    "note":     {"type": "text"}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected"], "final": ["approved", "rejected"]},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed",    "guard": "AMOUNT(amount) > 0"},
    {"name": "approve", "from": "filed", "to": "approved", "guard": "AMOUNT(amount) <= 5000"},
    {"name": "reject",  "from": "filed", "to": "rejected", "guard": "NOT ISBLANK(note)"}
  ],
  "access": {
    "read":   "employee = USER() OR approver = USER()",
    "create": "INGROUP(\"everyone\")",
    "update": "employee = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {
      "file":    "employee = USER()",
      "approve": "approver = USER()",
      "reject":  "approver = USER()"
    }
  },
  "rights": {
    "filed": {
      "*":    {"write": "FALSE"},
      "note": {"write": "approver = USER()"}
    }
  }
}
```

The situation. Ola filed three expenses: `E-1`, a taxi for 120.00 PLN,
and `E-2`, a conference for 8000.00 PLN, both with the approver
`claims-agent`; and `E-3`, a lunch, with the approver `marek`. The
program that runs you tells the engine that you are `claims-agent`,
an agent. It is 10:00 on 16 November 2026.

**1. Ask how much waits for you.** One command, and no loop. The
engine counts what you may see.

<!-- command: count -->
```jsonc
{"class": "Expense", "where": "state = \"filed\" AND approver = USER()"}
```

<!-- answer -->
```jsonc
{"count": 2}
```

**2. Ask what a document allows.** `enabled` says whether you may take
the transition now, as the document stands, and `code` why not.

<!-- command: transitions -->
```jsonc
{"class": "Expense", "id": "E-2"}
```

<!-- answer -->
```jsonc
{
  "transitions": [
    {"name": "approve", "to": "approved", "enabled": false, "code": "guard_false"},
    {"name": "reject", "to": "rejected", "enabled": false, "code": "guard_false"}
  ]
}
```

**3. Look before you move.** A preview runs the whole command and
writes nothing. One that would be refused answers the refusal, with
the guard in the hint: 8000.00 is above 5000.

<!-- command: preview -->
```jsonc
{"class": "Expense", "id": "E-2", "transition": "approve"}
```

<!-- answer -->
```jsonc
{
  "error": {
    "code": "guard_false",
    "message": "the guard of approve is not TRUE for the document in filed",
    "hint": "the guard: AMOUNT(amount) <= 5000"
  }
}
```

**4. Move, with the values the move needs and a key.** `reject` was
blocked in step 2 only because the note was blank. Values given with a
transition are written first, and the guard is asked then. The key
names this one command.

<!-- command: transition -->
```jsonc
{
  "class": "Expense",
  "id": "E-2",
  "transition": "reject",
  "values": {"note": "above the limit of 5000.00 PLN"},
  "key": "reject-E-2"
}
```

<!-- answer -->
```jsonc
{
  "document": {
    "class": "Expense",
    "id": "E-2",
    "title": "Expense of ola",
    "state": "rejected",
    "version": 3,
    "class_version": 1,
    "created_at": 1794819600,
    "updated_at": 1794823200,
    "state_entered_at": 1794823200,
    "created_by": "ola",
    "updated_by": "claims-agent",
    "state_entered_by": "claims-agent",
    "state_entered_from": "filed",
    "values": {
      "amount": "8000.00 PLN",
      "approver": "claims-agent",
      "employee": "ola",
      "note": "above the limit of 5000.00 PLN",
      "what": "conference in Lisbon"
    }
  }
}
```

**5. Not sure the answer arrived? Send the same command again.** With
the same key the engine answers as it did the first time, word for
word, and writes nothing: the expense is still at version 3.

**6. Read a refusal as an answer.** `E-3` names another approver, so
it is not yours to see. The engine says what it says of an id that was
never used.

<!-- command: get -->
```jsonc
{"class": "Expense", "id": "E-3"}
```

<!-- answer -->
```jsonc
{
  "error": {
    "code": "unknown_document",
    "message": "no document E-3 of class Expense"
  }
}
```

**7. Asked what happened? Read the log.** Each event says who did it,
what they were, and the key the command carried.

<!-- command: events -->
```jsonc
{"class": "Expense", "id": "E-2"}
```

<!-- answer -->
```jsonc
{
  "events": [
    {
      "event_id": "01M7HSBBM0EMY6AY0QD2WXM4MB",
      "class": "Expense",
      "doc_id": "E-2",
      "kind": "created",
      "to_state": "draft",
      "actor_id": "ola",
      "actor_kind": "person",
      "after_version": 1,
      "business_date": "2026-11-16",
      "ts": 1794819600,
      "origin": "command"
    },
    {
      "event_id": "01M7HSBBM0EMY6AY0QD2WXM4ME",
      "class": "Expense",
      "doc_id": "E-2",
      "kind": "transition",
      "name": "file",
      "from_state": "draft",
      "to_state": "filed",
      "actor_id": "ola",
      "actor_kind": "person",
      "before_version": 1,
      "after_version": 2,
      "business_date": "2026-11-16",
      "ts": 1794819600,
      "origin": "command"
    },
    {
      "event_id": "01M7HWS7803G93DXF5JAY1EYM1",
      "class": "Expense",
      "doc_id": "E-2",
      "kind": "transition",
      "name": "reject",
      "from_state": "filed",
      "to_state": "rejected",
      "actor_id": "claims-agent",
      "actor_kind": "agent",
      "before_version": 2,
      "after_version": 3,
      "command_key": "reject-E-2",
      "business_date": "2026-11-16",
      "ts": 1794823200,
      "origin": "command"
    }
  ]
}
```

An `event_id` is made by the engine and differs at every run.

## What goes wrong most often

1. **Sending a refusal again.** `guard_false`, `forbidden`,
   `wrong_state` and `terminal` are answers. The same command gets the
   same answer until the document or the caller changes. Report the
   code and the hint; send again only a command that got no answer at
   all.
2. **A change with no key.** When the answer is lost you cannot tell
   whether the change was made, and a second send makes it twice. Give
   every command that changes something a `key` of its own.
3. **One key for two commands.** A key names one command. A different
   command under a key that was used is refused with `key_reused`.
4. **Reading `enabled: false` as final.** `transitions` asks the
   guard of the document as it stands. A guard may hold once the
   command brings its values, as `reject` does above: preview with the
   values.
5. **Taking `unknown_document` for "there is no such document".** A
   document you may not see is answered the same way. Say "not found,
   or not mine to see".
6. **A loop over `get`.** `query` and `count` answer in one command,
   and leave out what you may not see. In a filter `USER()` is you.
7. **Changing a finished document.** A document in a final state
   keeps its values: a `set` is refused with `terminal`.
8. **Believing a batch.** `transition_many` answers for each id, with
   the document or its refusal. No error does not mean all moved.
9. **Saying who you are in the command.** The caller comes from the
   program that sends the command, never from the command. The one
   thing a command says is `for`, whom you act for, and that passes
   only while a substitution says so.
10. **Guessing a member.** A member the command does not take is
    refused with `bad_json`, and the hint lists the members it takes.
11. **Waiting for a date to move a document.** A transition with a
    time moves by itself. A guard that reads the clock does not: the
    document stays until a command touches it. `due` lists such
    documents and `refresh` touches one.

## The hat

The operator acts on documents of a class that already exists, on behalf
of the person or the system it serves. It asks what is allowed, previews,
commits with a key, and reads a refusal as an answer rather than an
obstacle. The guards hold whoever is asking, the agent included.

## What works today

| Surface | Mark |
|---|---|
| the Go API: `Create`, `CreateMany`, `Set`, `Reset`, `Refresh`, `Transition`, `Transitions`, `Drain`, `Outbox`, `RetryAction`, `Get`, `Delete`, `Query`, `Count`, `SQL`, `History`, `Events`, `Preview` | shipped |
| many documents moved by one call: `TransitionMany` takes up to 10,000 ids of one class and one transition, moves each that can move in one transaction, and answers for every id with the document or the refusal and its code. No error does not mean that all moved: read the outcomes. Send 1,000 a call. `Key` makes the call safe to send again | shipped in v0.4.1 (ADR-0048) |
| the principal on every call, `As`; the idempotency key, `Key`; a moment for reads and previews, `At` | shipped |
| who may operate: `Outbox`, `RetryAction`, `Drain`, `PreviewAction`, `TryAction` and `CheckBlobs` are for the system group `administrators`, which the host program gives a caller in `Principal.System`; anybody else, a call without a principal too, gets `forbidden` naming the group | shipped with Phase 5: pass `As` on these calls. A database administrator sees no document by being one |
| substitutions (ADR-0053): who may act for whom is a document of the class `Substitution`, in every database. The person gives one, `Create` with `by` and optionally `from`, `until`, `classes` and `reason`, and withdraws it with the transition `withdraw`. A database administrator gives one for somebody who cannot, with `for` and a `reason`, never to themselves, and may withdraw any. Nothing changes one: withdraw it and give another. It is `planned`, `active`, `ended` or `withdrawn`; the check reads its dates, so a worker that is behind delays nothing. `Query` on the class lists them: the person sees those for them, the holder those they hold, a database administrator all. A content administrator acts past no rule of this class | shipped with Phase 5 |
| `Due`, the documents of a class that a commit would move now, or at a moment with `At`; `since` says since when, for a document a timed transition would move | shipped with Phase 4 |
| a timed transition, `after` or `at`: it fires in the first commit that touches the document once its time has come, and `Transitions` shows the moment as `fires_at` | shipped with Phase 4 |
| the engine moving a document by itself when its timer comes: `Outbox` and `<Class>_actions` list each timer waiting as the action `timer` with its moment in `due_at`; one that died says why, and `RetryAction` asks again | shipped with Phase 4 |
| the JSON command set (ADR-0055): every call above as a name and one JSON object, answered by one JSON object, `{"document": …}` or `{"error": {"code": …}}`; a member the command does not take is refused with `bad_json` and its place. `docs/manual/commands.md` lists every command | shipped as the Go call `Exec`, which a host program gives an agent |
| the same commands over HTTP, each sent to its own address, `POST /v1/<command>`, with `Content-Type: application/json`; the status follows the code, and the code is what to act on | shipped as the Go package `fado/httpdoor`: a host program mounts the door and says who sends each request, or opens the database with its own users (ADR-0057), and a request then carries a key: `Authorization: Bearer fado_…`. With a key, `whoami` answers who the database takes the caller for. `fadodb serve` is the server that does this for a database: `docs/manual/serving-a-database.md`. On a server an action is the engine's own or a request to a host its operator listed; a definition with no request is refused with `not_registered`, and one to a host or a connection the operator did not list with `not_allowed` or `unknown_connection`: ask the operator, no command changes the settings. An administrator reads the counts of a server at `GET /v1/metrics`, the actions that are due, waiting or dead among them |
| the same commands from a shell: `fadodb exec COMMAND 'OBJECT'` sends one to a server with the key in `FADODB_KEY` and the server in `FADODB_URL`, and prints the answer; it ends with 0 when the command was carried out, 1 when the server refused it (the refusal is on standard error), 2 when the line could not be read and 3 when no answer came; `fadodb exec help COMMAND` lists the members of a command; `fadodb exec query '…' --xlsx --out FILE` writes an export a person opens in a spreadsheet, and `--csv` one a program reads; `fadodb file put FILE --class … --id … --variable …` puts a file on a document and `fadodb file get … --out FILE` reads one | shipped: `docs/manual/commands-from-a-terminal.md` |
| where the documents of a class are and how they move, in one command: `flow`, with `since` and `until` for a period; it counts what the caller may read | shipped: `docs/manual/guides/see-where-documents-wait.md` |
| the same commands as tools, for an agent whose program speaks the Model Context Protocol: the program starts `fadodb mcp` with the server in `FADODB_URL` and the key in `FADODB_KEY`; a tool has the name of its command, takes its object as arguments and answers its answer as text; a refusal is the answer of the tool, marked as an error, with the code to act on; `no_answer` says that no answer came through and the command may have been carried out, so a command that writes carries a `key`; `subscribe` and `create_key` are no tool | shipped: `docs/manual/the-door-for-agents.md` (ADR-0064) |
| the door at the server itself, a tool that waits for a change, files through the door | not built: questions of ADR-0064 |
| a message from a system that speaks only its own language (a bank, a shop): an inlet, a definition an administrator stores with `define_inlet`, says how the message proves who sent it and which one command, `create`, `set` or `transition`, it becomes, carried out as a service user; `validate_inlet` checks one and `try_inlet` answers the command a message would become, doing nothing; `list_inlets` and `get_inlet` read them | shipped (ADR-0065): `docs/manual/a-way-in-for-messages.md`. The server takes the message at `POST /v1/in/<inlet>`, with no key, only when its operator's settings have `inbound`; otherwise `define_inlet` answers `not_supported`: ask the operator. `fadodb inlet check FILE --class FILE --message FILE` checks an inlet in a file and shows the command a sample message would become, with no server |
| a server in a container: the image is the tool, so `docker run … fadodb exec whoami` and `docker run -i … fadodb mcp` are a caller and the door for agents on a machine that has Docker and no `fadodb`; `fadodb health` asks a server whether it answers, with no key | shipped: `docs/manual/in-a-container.md`. The image is built from a checkout; no release has published one yet |

An agent reaches documents over HTTP, with a key, where a database is
served by `fadodb serve`: it sends each command as
`POST /v1/<command>` with `Authorization: Bearer <its key>`, and
starts with `whoami`. An agent with a shell sends the same commands
with `fadodb exec`, such as `fadodb exec whoami`, and reads the exit
code: 1 is a refusal to read and act on, 3 is no answer, after which
a command with a `key` is safely sent again. An agent whose program
speaks the Model Context Protocol finds the same commands as tools,
once the program starts `fadodb mcp`: the tool `whoami`, the tool
`transition`, each with the object of its command as arguments. A
refusal is then the answer of the tool, marked as an error; read its
`code`. `no_answer` is the door's own code for no answer, after which
a command with a `key` is safely sent again. Where a Go program embeds the engine, that
program passes the agent's commands to `Exec` as they are, or mounts
the HTTP door (`fado/httpdoor`). A command refused with `busy` did not run, and
one refused with `timeout` wrote nothing if it is one commit. Either
can be sent again, and with a `key` sending twice is safe.

## Procedure, in outline

1. **Know who you act for.** Every call carries a principal with an id
   and groups (`As`). The event log records it; a rule can keep a
   transition for a human.
2. **Ask what is allowed.** `Transitions` lists each transition from the
   current state with whether it is enabled and, when it is not, a stable
   code such as `guard_false`.
3. **Preview before you commit.** `Preview` with the values and the
   transition runs the whole pipeline and writes nothing: the computed
   values, the path through eventless transitions, the moment its
   formulas saw.
4. **Commit with a key.** `Key("order-42")` makes a retry the same
   commit, so a loop that lost the answer can ask again. Give the
   commit its business date when the event has one (ADR-0026).
5. **Read the refusal.** A rejection names a stable code, an exact
   location and often a fix. A guard that is false is an answer, not a
   transient error; do not retry it, report it.
6. **Read sets with a filter or SQL, not a loop.** `Query` over a class
   and the read-only `SQL` endpoint answer in one call what a loop over
   `Get` answers in a thousand.
7. **Read history when asked what happened.** `History` is every version
   with who committed it and when; `Events` is the log.
8. **Choose the moment when it matters.** `At(t)` on a read or a preview
   makes `NOW()` and `TODAY()` that moment in every formula the call
   evaluates. A commit always runs at the database's clock.
9. **Move what is due.** A timed transition moves by itself: the
   engine touches the document when its time comes, and `Transitions`
   shows that moment as `fires_at`. A guard such as `TODAY() > due` is
   different: it comes to hold while nothing touches the document, and
   the document stays where it is until a commit touches it. `Due`
   lists those documents of a class, each with its state and the state
   it would move to. Touch each with `Refresh` and the commit moves it.
   One call lists at most 10,000, so ask again until the list is empty.
   With `At(t)` it answers what will be due at that moment. A document
   whose timer has come and that `Due` still lists, with `since`, is
   one the engine could not move: look for a dead `timer` row in
   `Outbox`.
10. **Move many with one call.** `TransitionMany` takes the ids of up to
    10,000 documents of one class and one transition, and moves each
    that can move, in one transaction, a commit each. A document that
    cannot move does not stop the others: it comes back with its
    refusal. So the call may return no error and still have moved
    nothing; read the outcome of every id. Send about 1,000 a call,
    since a batch holds the single writer for its length. Give the call
    a `Key`, and a call sent again moves no document twice.
    `PreviewMany` answers the same and writes nothing. The values are
    the same for every document; documents that need their own values
    take `Transition` each.

## Where the rest is

The reference this page once planned under `../reference/` is the
manual now: the chapters under "Read first" above. The decision
records behind them are listed in
[`../reference/README.md`](../reference/README.md).
