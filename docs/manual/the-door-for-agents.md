# The door for agents

`fadodb mcp` offers the commands of a server to an agent as tools. An
agent's program, such as Claude Desktop, Claude Code, Cursor or a
framework that runs agents, asks a door which tools it has and calls
them, in the Model Context Protocol (MCP). This is that door.

A tool is a command. Its name is the command's, its arguments are the
command's object and its answer is the command's answer: the same
names and the same JSON that [the commands](commands.md) lists, that a
program sends over HTTP and that
[`fadodb exec`](commands-from-a-terminal.md) sends from a terminal. So
an agent can do what a program can, and a command added to the engine
is a tool with it.

The door is a caller as any other. It holds no database. The agent's
program starts it on its own machine, and the door sends each call to
the server with the key of a user. So what an agent does is judged by
the rules of the classes and written to the log under that user, and
no tool lets an agent say that it is somebody else. The same door
stands at the server itself, at `/mcp`, for an agent that cannot start
a program on a machine ([at the server itself](#at-the-server-itself)).

The story of this chapter: Acme keeps its invoices in a database that
[`fadodb serve` serves](serving-a-database.md), with the class of an
invoice that [Commands from a terminal](commands-from-a-terminal.md)
defines. Bob, in sales, files invoices. Ann, in finance, approves
them, and so does `ap-agent`, an agent that Olga, who keeps the
database, sets up here. A test plays every call of the chapter through
the door against a server, and compares what the agent reads.

## Setting an agent up

An agent is a user, with groups of its own and a key of its own. Olga
makes both:

<!-- olga: the agent -->
```sh
fadodb exec create_user '{"id": "ap-agent", "kind": "agent", "name": "Accounts payable agent", "groups": ["finance"]}'
fadodb exec create_key '{"user": "ap-agent", "label": "invoice matching"}'
```

`create_key` prints the key once, under `key` as `secret`. From then
on every event the agent causes says `ap-agent`, and the rules of a
class can keep a transition for people.

The agent's program is then told to start the door, with the server
and the key. Most programs read it as JSON of this form:

<!-- the agent's program is told -->
```jsonc
{
  "mcpServers": {
    "acme": {
      "command": "fadodb",
      "args": ["mcp"],
      "env": {
        "FADODB_URL": "https://fado.acme.example",
        "FADODB_KEY": "fado_…"
      }
    }
  }
}
```

The door is told the server and the key as `fadodb exec` is:

<!-- what the door is told -->
| What | Flag | Environment | Told nothing |
|---|---|---|---|
| the server | `--url` | `FADODB_URL` | `http://127.0.0.1:8080`, where `fadodb serve` listens when it is told nothing |
| the key | `--key-file`, a file that holds it | `FADODB_KEY` | the door does not start, and says how a key is made |
| a certificate to know the server by | `--ca`, a file in PEM | | the certificates the machine knows |
| that the key may travel in the clear | `--plain-http` | | it may not, beyond this machine |
| only the tools that read | `--read-only` | | every tool |
| only the tools named | `--tools`, as `get,query,transition` | | every tool |
| a folder for files | `--files`, as `/home/bob/invoices` | | no tool for files ([files from a folder](#files-from-a-folder)) |

- **The key is never an argument**, here as at a terminal. It is in
  the environment the agent's program gives the door, or in a file
  that only its owner reads.
- **One door, one database.** An agent that works for Acme and for
  Globex is given two doors, each with its server and its key.
- **A person's assistant is that person.** Ann may give her own key to
  the door of her own assistant. What it does is then done as Ann, and
  the log says Ann.

## At the server itself

`fadodb serve` has the same door at its own address, `/mcp`, for an
agent that cannot start a program: Claude on claude.ai, or an agent
that runs in the cloud. Its tools are the same, and each request
proves who sends it, as one sent to the commands does:

- **Ann connects Claude.** In Claude she adds a connector with the
  address `https://fado.acme.example/mcp`. Claude asks the door, is
  told where passes come from, and sends Ann to the server, where she
  signs in and allows it
  ([connecting a client](signing-in.md#connecting-a-client)). What
  Claude does is then done by `ann~claude.ai`, acting for Ann, with
  her groups.
- **`ap-agent` sends its key**, from a program that lets its user set
  a header. Claude Code reads it as JSON of this form:

<!-- the agent's program is told, at the server -->
```jsonc
{
  "mcpServers": {
    "acme": {
      "type": "http",
      "url": "https://fado.acme.example/mcp",
      "headers": {"Authorization": "Bearer fado_…"}
    }
  }
}
```

The two places differ in what starts the door and who it sends as:

<!-- the two places -->
| | `fadodb mcp` | `/mcp` at the server |
|---|---|---|
| started by | the agent's program, on its own machine | `fadodb serve`, always |
| sends as | the user whose key it was given | whoever each request proves to be, with a pass or a key |
| offers | every tool, or fewer with `--read-only` and `--tools` | every tool; the rights decide what each caller may do |
| carries at once | 16 calls | as many as the server runs commands, 64 unless [its settings](serving-a-database.md#the-settings) say otherwise |
| stops a call | when the agent's program sends a notice | when the agent's program sends a notice, or closes the request |

Over HTTP the door takes one message to a POST and answers it with one
JSON object, never with a stream. It keeps no session, so each request
carries what the door needs: one of the revision 2026-07-28 says its
revision in its params and in the header `MCP-Protocol-Version`, and
names its method in `Mcp-Method` and the tool it calls in `Mcp-Name`;
one of the revisions before says its revision in
`MCP-Protocol-Version`, and one that says none is taken for
2025-03-26. A notice is taken with 202 and no body.

A request is refused before any tool runs when:

<!-- refused at the door for agents -->
| The request | Status |
|---|---|
| proves nobody: no pass or key, one the server does not know, or the session of a browser alone | 401, with `WWW-Authenticate` naming where passes for `/mcp` come from |
| carries a pass for the commands | 401: a pass is for one address |
| comes from a page of another site | 403 |
| is no POST | 405 |
| does not say its body is JSON | 415 |
| is larger than the server reads | 413 |
| has headers that do not say what its body says | 400 |
| names a method the door does not have | 404 in the revision 2026-07-28, and 200 with the error in the revisions before |

Every request is a line of [the server's log](serving-a-database.md#what-it-writes),
with `"door":"mcp"` and the command of the tool it called.

## What the door says of itself

When an agent's program opens the door, the door hands it a text, which
the protocol calls `instructions`, and the program puts it before its
agent: the sign above the door. This is the sign of a door that offers
every tool:

<!-- the sign -->
```text
FadoDB is a database of documents that move through the states of
their class. Each tool is one command of the database at
https://fado.acme.example, sent as the user whose key this door was
given. whoami answers who that user is. list_classes answers which
classes there are, and get_class how one is defined: its variables,
its states, its transitions and who may do what. list_apps answers
which apps there are: an app is a name for the classes and the tables
that belong together, and a class or a table says in its definition
which app it belongs to. list_functions answers every function a formula can call, with its
arguments; read it before you write a formula. Look before you build:
before you define an app, a class, a table or a schema, list what the
database has (list_apps, list_classes, list_tables, list_schemas) and
read each one that may be the same thing; match by meaning, not by
name. Ask the person you work for which one to use, and never keep a
thing twice. preview answers what a
create, a set or a transition would do, and writes nothing. A command
the database refuses is answered with the refusal under error: a code,
a message, the place in the command and a hint. The tools that commit
a document take a key, and one sent again with the same key makes no
second commit: create, set, reset, refresh, transition,
transition_many, delete. No other tool takes one. Decide on what you
read: a document you read has a version, and a change to it says which
version you read, as version (set, reset, refresh, transition, delete).
When the document has moved on since, the command is refused with
stale_version and nothing is written: read the document again and
decide on what it holds now.
```

- **Look before you build** is on the sign because the door is the one
  place every agent passes. An employee is kept in one list. Ann's
  assistant, asked to set up training requests, finds the table
  `People` before it defines a class `Employee` beside it, and asks
  Ann which one to use. The whole procedure is the first step of
  [the designer's skill](../../skills/fadodb-design/SKILL.md#look-before-you-build),
  which an agent reads only when it was given the skills.
- **The five tools that build say it again**, each in its own
  description: `define_app`, `define_class`, `define_classes`,
  `define_table` and `define_schema`. A program may show its agent the
  tools and not the sign.
- **The sign says what the door offers.** A door started with
  `--read-only` says nothing of building or of keys, and a door
  started with `--tools` names only the tools it has.
- **A sign stops nobody.** What an agent may do is decided by the
  groups of its user and the rules of the classes.

## The tools

Every command is a tool, but [two](#what-is-no-tool). A tool says what
the table of the commands says of its command. This is `transition`,
as the door lists it:

<!-- a tool -->
```jsonc
{
  "name": "transition",
  "title": "Transition",
  "description": "Moves a document along a named transition of its class. It answers document: the document after the command.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["class", "id", "transition"],
    "properties": {
      "class": {"type": "string", "description": "the name of the class"},
      "id": {"type": "string", "description": "the id of the document"},
      "transition": {"type": "string", "description": "the name of the transition"},
      "values": {"type": "object", "description": "values to assign in the same commit, by variable"},
      "key": {"type": "string", "description": "the idempotency key: the same command sent again with the same key makes no second commit and is answered as the first was"},
      "business_date": {"type": "string", "description": "the business day of the commit, as \"2026-11-13\"; the server's day when left out"},
      "reason": {"type": "string", "description": "why a content administrator acts or reads past a rule; a change keeps it on its event, a read in the log of reads"},
      "for": {"type": "string", "description": "the id of whoever the caller acts for; let through only while a substitution says so"},
      "version": {"type": "integer", "description": "the version of the document its sender read before deciding; when the document is at another, the command is refused with stale_version and nothing is written"}
    }
  },
  "annotations": {"readOnlyHint": false, "destructiveHint": false, "openWorldHint": false}
}
```

The marks of a tool tell an agent's program what the tool does, so
that it can let a tool that reads run by itself and ask a person
before one that removes:

<!-- the marks -->
| The command | Tools | `readOnlyHint` | `destructiveHint` | `openWorldHint` |
|---|---|---|---|---|
| reads | 58 | true | false | false |
| writes | 35 | false | false | false |
| writes, and runs an action now: `try_action`, `drain` | 2 | false | false | true |
| removes: `delete` | 1 | false | true | false |

An action is a request to a host outside the database, so the two
commands that run one now are the ones marked as reaching out.

## What an agent reads

The answer of a tool is the answer of its command, as text. The agent
asks who it is:

<!-- ap-agent: whoami -->
```jsonc
{"name": "whoami", "arguments": {}}
```

<!-- ap-agent: whoami, the answer -->
```jsonc
{
  "user": {
    "id": "ap-agent",
    "kind": "agent",
    "name": "Accounts payable agent",
    "groups": [
      "finance"
    ],
    "system_groups": [],
    "disabled": false
  }
}
```

It asks which invoices over 1000 wait for approval:

<!-- ap-agent: query -->
```jsonc
{"name": "query", "arguments": {"class": "Invoice", "where": "state = \"review\" AND total > 1000", "order": ["id"], "fields": ["supplier", "total"]}}
```

<!-- ap-agent: query, the answer -->
```jsonc
{
  "documents": [
    {
      "class": "Invoice",
      "id": "FV/2026/11/0042",
      "title": "Ink and Son",
      "state": "review",
      "version": 2,
      "class_version": 1,
      "created_at": 1794733200,
      "updated_at": 1794733200,
      "state_entered_at": 1794733200,
      "created_by": "bob",
      "updated_by": "bob",
      "state_entered_by": "bob",
      "state_entered_from": "draft",
      "values": {
        "supplier": "Ink and Son",
        "total": "1250.50"
      }
    },
    {
      "class": "Invoice",
      "id": "FV/2026/11/0043",
      "title": "Paper Mill",
      "state": "review",
      "version": 2,
      "class_version": 1,
      "created_at": 1794733200,
      "updated_at": 1794733200,
      "state_entered_at": 1794733200,
      "created_by": "bob",
      "updated_by": "bob",
      "state_entered_by": "bob",
      "state_entered_from": "draft",
      "values": {
        "supplier": "Paper Mill",
        "total": "2400.00"
      }
    }
  ]
}
```

And it approves one, with a key of the command:

<!-- ap-agent: transition -->
```jsonc
{"name": "transition", "arguments": {"class": "Invoice", "id": "FV/2026/11/0043", "transition": "approve", "key": "approve-FV/2026/11/0043"}}
```

<!-- ap-agent: transition, the answer -->
```jsonc
{
  "document": {
    "class": "Invoice",
    "id": "FV/2026/11/0043",
    "title": "Paper Mill",
    "state": "approved",
    "version": 3,
    "class_version": 1,
    "created_at": 1794733200,
    "updated_at": 1794733200,
    "state_entered_at": 1794733200,
    "created_by": "bob",
    "updated_by": "ap-agent",
    "state_entered_by": "ap-agent",
    "state_entered_from": "review",
    "values": {
      "scan": null,
      "supplier": "Paper Mill",
      "total": "2400.00"
    }
  }
}
```

- **The key makes a command safe to send again.** An agent that did
  not hear the answer sends the same call with the same `key`. The
  invoice is not approved twice: the second call is answered as the
  first was, and the test of this chapter sends it twice.
- **An agent tries before it writes.** `preview` answers what a
  create, a set or a transition would do, and writes nothing;
  `transitions` lists what a document can do now, and for each whether
  this caller may.

### A refusal

Bob's assistant, with Bob's key, tries to approve an invoice. The
database refuses, and the tool answers the refusal as the database
wrote it, marked as an error:

<!-- bob: transition -->
```jsonc
{"name": "transition", "arguments": {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}}
```

<!-- bob: transition, the refusal -->
```jsonc
{
  "error": {
    "code": "forbidden",
    "message": "bob may not take approve from review",
    "hint": "the rule of approve: INGROUP(\"finance\")",
    "path": "/access/transition/approve"
  }
}
```

An agent reads the `code` to decide, the `path` to find the place, and
the `hint` to mend what it sent. The codes are the ones of
[the refusals](refusals.md).

### No answer

When the server is not reached, or its answer is cut off, the tool
answers in the same form, with a code of the door's own:

<!-- no answer -->
```jsonc
{
  "error": {
    "code": "no_answer",
    "message": "no answer came through: the server at http://127.0.0.1:8080/v1/transition was not reached: dial tcp 127.0.0.1:8080: connect: connection refused",
    "hint": "the command may have been carried out: sent again with the same key, it makes no second commit; sent with no key, read the document before sending it again"
  }
}
```

`no_answer` is no refusal: nobody knows whether the command was
carried out. The hint says what is safe for the command that was
sent:

<!-- after no answer -->
| The command | What the hint says |
|---|---|
| reads, such as `get` or `query` | nothing was changed: ask again |
| commits a document and so takes a `key`: `create`, `set`, `reset`, `refresh`, `transition`, `transition_many`, `delete` | sent again with the same key, it makes no second commit; sent with no key, read the document before sending it again |
| writes anything else, such as `define_class`, `set_user` or `try_action` | it takes no key: read what it would have changed before sending it again |

So an agent gives a `key` to every command that commits a document,
as `ap-agent` does above. The door tells an agent which tools take
one when it is asked what it is.

## Keeping tools from an agent

What an agent may do is decided where it is decided for everybody: by
the groups of its user and the rules of the classes. Whoever starts a
door can also keep tools from the agent altogether, so that it does
not see them:

- `--read-only` offers only the tools that read. Ann gives her
  assistant her own key and this flag, and it can look and count and
  cannot move an invoice.
- `--tools get,query,transitions,transition` offers these tools and no
  other. A short list also costs an agent less to read: the whole list
  is about 79,000 characters.

An agent that calls a tool the door does not offer is told why:

<!-- ann: read only -->
```jsonc
{"name": "transition", "arguments": {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}}
```

<!-- ann: read only, the answer -->
```
Unknown tool: transition. This door was started to offer only the tools that read
```

## Waiting for a change

`ap-agent` handles an invoice once it enters `review`. A stream of
changes stays open, and a tool answers once, so the stream is no
tool: the agent waits with `wait_for_changes`. It names the changes it
wants, where it stopped, and how long it will wait. Its first call
names no place, so it begins now:

<!-- waiting: the call -->
```jsonc
{"name": "wait_for_changes", "arguments": {"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"], "seconds": 30}}
```

While it waits, Bob submits FV/2026/11/0042, and the agent reads:

<!-- waiting: the answer -->
```jsonc
{
  "events": [
    {
      "event_id": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE",
      "class": "Invoice",
      "doc_id": "FV/2026/11/0042",
      "kind": "transition",
      "name": "submit",
      "from_state": "draft",
      "to_state": "review",
      "actor_id": "bob",
      "actor_kind": "person",
      "before_version": 1,
      "after_version": 2,
      "business_date": "2026-11-15",
      "ts": 1794733200,
      "origin": "command"
    }
  ],
  "after": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE",
  "more": false
}
```

It handles the invoice, and asks again from where it stopped:

<!-- waiting: again -->
```jsonc
{"name": "wait_for_changes", "arguments": {"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"], "after": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE", "seconds": 30}}
```

- **It answers what there is, or waits for it.** The events after its
  place that it may hear of are answered at once. When there are none
  yet, it waits for the first, up to `seconds`, from 0 to 60, and
  answers none when they pass. Left out, `seconds` is 0, and the call
  answers at once: an agent started every morning asks what happened
  in the night.
- **`after` is where to ask from next.** Given back, the agent misses
  nothing, also of what came between two calls, and is told nothing
  twice. `start` begins before the first event of the log, and a
  database that has none yet answers `start`.
- **The place moves over what the agent may not read**, so that it does
  not read the log again at each call. It names the last event the call
  read, which says when that event was written and nothing else of it.
- **An answer holds 100 events at most**, or `limit` of them, up to
  1,000. One that is full says `"more": true`, and the next call takes
  the rest at once.
- **Who hears of what** is the stream's rule: the agent hears of an
  event when it may read the document as the event left it
  ([who hears of what](the-stream-of-changes.md#who-hears-of-what)). An
  agent that acts for a person, as Claude does for Ann at the server
  itself, hears of what it may read for her, on the classes it may act
  for her on. A stream is heard as oneself; this is how such an agent
  waits.
- **At the server, a wait holds the place of a stream**, not the slot of
  a command: as many agents wait at once as the server holds streams,
  256 unless its operator says otherwise, and none of them keeps a
  command waiting. When the server stops, a wait answers what it has,
  with its place.

## Files from a folder

Bob files the invoice FV/2026/11/0045 and its scan, and `ap-agent`
reads the scan to check the amounts. The bytes of a file do not pass
through an agent's conversation. The door reads them from a folder on
the disk of whoever starts it and writes them into it, and nowhere
else. Bob names the folder of his assistant's door with `--files`:

<!-- files: bob's program is told -->
```jsonc
{
  "mcpServers": {
    "acme": {
      "command": "fadodb",
      "args": ["mcp", "--files", "/home/bob/invoices"],
      "env": {
        "FADODB_URL": "https://fado.acme.example",
        "FADODB_KEY": "fado_…"
      }
    }
  }
}
```

The door then offers two more tools, `put_file` and `get_file`. The
scan is `/home/bob/invoices/scan-0045.pdf`, and his assistant puts it:

<!-- files: put -->
```jsonc
{"name": "put_file", "arguments": {"path": "scan-0045.pdf"}}
```

It is answered the facts of the file, as
[files](files.md#the-facts) describes them:

<!-- files: put, the answer -->
```jsonc
{
  "file": {
    "hash": "09ac2f156d98803f6a577eaef61f7d4c178d040c6a14967330564523bb95d3e5",
    "name": "scan-0045.pdf",
    "media_type": "application/pdf",
    "size": 53
  }
}
```

The bytes do nothing until a document names them. The assistant gives
the facts to the variable `scan` of the invoice, as it would any
value, and `set` answers the invoice after the change:

<!-- files: set -->
```jsonc
{"name": "set", "arguments": {"class": "Invoice", "id": "FV/2026/11/0045", "values": {"scan": {"hash": "09ac2f156d98803f6a577eaef61f7d4c178d040c6a14967330564523bb95d3e5", "name": "scan-0045.pdf", "media_type": "application/pdf", "size": 53}}}}
```

`ap-agent` runs with a folder of its own, `--files /srv/ap-agent/inbox`.
It reads the invoice with `get`, finds the hash in `scan`, and writes
the file into its folder:

<!-- files: get -->
```jsonc
{"name": "get_file", "arguments": {"class": "Invoice", "id": "FV/2026/11/0045", "hash": "09ac2f156d98803f6a577eaef61f7d4c178d040c6a14967330564523bb95d3e5"}}
```

<!-- files: get, the answer -->
```jsonc
{
  "path": "scan-0045.pdf",
  "file": {
    "hash": "09ac2f156d98803f6a577eaef61f7d4c178d040c6a14967330564523bb95d3e5",
    "name": "scan-0045.pdf",
    "media_type": "application/pdf",
    "size": 53
  }
}
```

The scan is now `/srv/ap-agent/inbox/scan-0045.pdf`, where the agent's
program reads it with its own tools.

- **The folder, and nowhere else.** A path is a path in the folder, as
  `scan-0045.pdf` or `2026/scan-0045.pdf`, and a whole path is taken
  when it lies in the folder. A path that leads out of it, with `..`,
  as a whole path elsewhere, or through a link in the folder that points
  out of it, is refused with `bad_value` at `/path`. The door does not
  start with a folder that is not there.
- **No file is written over.** `get_file` refuses a path where a file
  already is, and says so before it asks the server; the agent gives
  another path. Left out, the path is the name the file was put under.
  It writes into a folder of the folder that is there, and makes none.
- **The bytes are checked.** `get_file` leaves the file in the folder
  only when it came whole and its bytes have the hash it asked for, and
  otherwise leaves nothing.
- **The rules are the document's.** Attaching a file is a change of the
  document, judged as any `set` or `create`. `get_file` opens a file
  through the document that holds it, as `GET /v1/file` does: whoever
  may read the variable that holds it may read it
  ([who may](files.md#who-may)). It takes `version` for a file an
  earlier version held, and `for` and `reason` as a command does.
- **`--read-only` offers `get_file`**, which reads the database, and not
  `put_file`. `--tools` names them as it names commands.
- **At the server itself there are none.** At `/mcp` there is no folder
  of the caller's, and Claude on claude.ai attaches no file. A person
  attaches one at a terminal, with
  [`fadodb file put`](commands-from-a-terminal.md#a-file-on-a-document),
  or a program with `POST /v1/blobs`.

## What is no tool

<!-- no tool -->
| Command | Why |
|---|---|
| `subscribe` | its answer is a stream, and a tool is one question and one answer: an agent waits with wait_for_changes |
| `create_key` | its answer holds the secret of a key, which would stay in the conversation of an agent; a key is made at a terminal |
| `create_password_link` | its answer holds the secret of a link that sets a password, which would stay in the conversation of an agent; a link is made at a terminal and given to the person |
| `create_invitation` | its answer holds the secret of an invitation, which lets a person in and would stay in the conversation of an agent; an invitation is made at a terminal and given to the person |

The bytes of a file are in no tool's arguments or answer: they go
through a folder ([files from a folder](#files-from-a-folder)).

## Trying the door at a terminal

The door reads a message on each line and writes each answer on a line,
so it can be tried by hand. A message says the version of the protocol
it speaks:

<!-- by hand -->
```sh
echo '{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {"name": "count", "arguments": {"class": "Invoice"}, "_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": {}}}}' | fadodb mcp
```

<!-- by hand, the answer -->
```jsonc
{"jsonrpc":"2.0","id":1,"result":{"_meta":{"io.modelcontextprotocol/serverInfo":{"name":"fadodb","title":"FadoDB","version":"dev"}},"content":[{"text":"{\"count\":3}","type":"text"}],"isError":false,"resultType":"complete"}}
```

The text of `content` is the answer of the command, and `isError` says
whether it is a refusal. Nothing is written to standard output but
such answers; what goes wrong with the door itself is said on standard
error.

## The protocol

The door speaks these revisions of the Model Context Protocol:

<!-- the revisions -->
| Revision | How a conversation opens |
|---|---|
| 2026-07-28 | with no greeting: every request says its revision, and the door remembers nothing between two requests |
| 2025-11-25 | with a greeting, `initialize`, that settles the revision |
| 2025-06-18 | with a greeting |
| 2025-03-26 | with a greeting |
| 2024-11-05 | with a greeting |

An agent's program of either kind finds the door speaking its form.

- **The door offers tools, and nothing else of the protocol**: no
  resources and no prompts.
- **The tools are listed on one page**, and the list does not change
  while a door lives.
- **A slow command holds up no other.** The door carries up to 16
  calls at once, and a call that the agent's program stops is stopped:
  the server ends the command and writes `caller_left` in
  [its log](serving-a-database.md#what-it-writes).
- **A message is one line of at most 64 MiB.** A server reads 16 MiB
  of a command unless it was told otherwise, and says so itself.

## How the door ends

The door ends when the agent's program closes its input, after it has
answered what was asked before.

| It ends with | When |
|---|---|
| 0 | the agent's program closed its input, or the door was told to stop |
| 2 | it could not start: no key, an address it may not send a key to, a tool that is none, or a folder for files that is not there |
| 3 | an answer could not be written to the agent's program |
