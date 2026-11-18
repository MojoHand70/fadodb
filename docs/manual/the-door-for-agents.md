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
no tool lets an agent say that it is somebody else.

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

- **The key is never an argument**, here as at a terminal. It is in
  the environment the agent's program gives the door, or in a file
  that only its owner reads.
- **One door, one database.** An agent that works for Acme and for
  Globex is given two doors, each with its server and its key.
- **A person's assistant is that person.** Ann may give her own key to
  the door of her own assistant. What it does is then done as Ann, and
  the log says Ann.

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
its states, its transitions and who may do what. Look before you
build: before you define a class, a table or a schema, list what the
database has (list_classes, list_tables, list_schemas) and read each
one that may be the same thing; match by meaning, not by name. Ask the
person you work for which one to use, and never keep a thing twice.
preview answers what a create, a set or a transition would do, and
writes nothing. A command the database refuses is answered with the
refusal under error: a code, a message, the place in the command and a
hint. The tools that commit a document take a key, and one sent again
with the same key makes no second commit: create, set, reset, refresh,
transition, transition_many, delete. No other tool takes one.
```

- **Look before you build** is on the sign because the door is the one
  place every agent passes. An employee is kept in one list. Ann's
  assistant, asked to set up training requests, finds the table
  `People` before it defines a class `Employee` beside it, and asks
  Ann which one to use. The whole procedure is the first step of
  [the designer's skill](../../skills/fadodb-design/SKILL.md#look-before-you-build),
  which an agent reads only when it was given the skills.
- **The four tools that build say it again**, each in its own
  description: `define_class`, `define_classes`, `define_table` and
  `define_schema`. A program may show its agent the tools and not the
  sign.
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
      "for": {"type": "string", "description": "the id of whoever the caller acts for; let through only while a substitution says so"}
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
| reads | 47 | true | false | false |
| writes | 27 | false | false | false |
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
  is about 62,000 characters.

An agent that calls a tool the door does not offer is told why:

<!-- ann: read only -->
```jsonc
{"name": "transition", "arguments": {"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}}
```

<!-- ann: read only, the answer -->
```
Unknown tool: transition. This door was started to offer only the tools that read
```

## What is no tool

<!-- no tool -->
| Command | Why |
|---|---|
| `subscribe` | its answer is a stream, and a tool is one question and one answer |
| `create_key` | its answer holds the secret of a key, which would stay in the conversation of an agent; a key is made at a terminal |
| `create_password_link` | its answer holds the secret of a link that sets a password, which would stay in the conversation of an agent; a link is made at a terminal and given to the person |

The files are not in the door either: the bytes of a file do not pass
through an agent's conversation. From a terminal, `fadodb file` puts a
file on a document and reads one.

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
| 2 | it could not start: no key, an address it may not send a key to, or a tool that is none |
| 3 | an answer could not be written to the agent's program |

## Not here yet

- **The door at the server itself**, for an agent that cannot start a
  program on its machine.
- **A tool that waits for a change**, in place of the stream.
- **Files.**

Each is wanted and is its own step after v0.6.0, by the owner's
rulings of 2026-11-17
([ADR-0064](../adr/0064-the-door-for-agents.md)). The door at the
server comes with the signing in of the frontend.
