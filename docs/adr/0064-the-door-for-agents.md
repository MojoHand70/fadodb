# ADR-0064: The door for agents

- Status: Accepted
- Date: 2026-11-16

## Context

An agent's program, such as Claude Desktop, Claude Code, Cursor or a
framework that runs agents, does not read a description of HTTP
addresses. It asks a door which tools it has and calls them, in the
Model Context Protocol (MCP). A tool has a name, a description, the
schema of its arguments, and marks that say whether it reads, writes or
removes. Agents are the first caller of FadoDB, and some processes
will have no other, so this door is not an extra.

What is already decided:

- **A door for agents offers one tool for each command, and keeps the
  ones that read apart from the ones that write** (ADR-0055, context).
  The table of the commands was made data so that the tools come from
  it (ADR-0055, item 10).
- **The owner ruled on 2026-11-14 that the door is built in Phase 6**,
  and that the engine knows keys only: a person, an agent and a
  service each call with the key of a user (ADR-0057).
- **The command-line tool is a caller of a server** (ADR-0062): it is
  told the server and the key, and the key is never an argument.
- **How a tool hears of changes was left to this record** (ADR-0058,
  item 12), and the bytes of a file travel beside the commands
  (ADR-0055, item 9).

What the protocol brings:

- **Two ways for a door to stand.** As a program the agent's program
  starts on its own machine and talks to over standard input and
  output; the protocol says such a door takes its credentials from the
  environment. Or at an address, over HTTP; there the protocol's own
  way to sign in is OAuth.
- **Two forms.** Until the revision of 2025-11-25 a conversation
  opened with a greeting, `initialize`, that settled the revision. The
  revision of 2026-07-28 has no greeting: every request says its
  revision, and a door remembers nothing between two requests. The
  programs and libraries in use speak the first by default and the
  second when asked, so a door meets both.
- **What the catalogues of such doors ask**, where a door is listed
  for people to find: no single tool that takes any command; a title
  and a mark of reading or removing on every tool; a name of at most
  64 characters.

The case used below: Acme's database is served at
`https://fado.acme.example`. Olga keeps it. Ann, in finance, approves
invoices, and Bob, in sales, files them. `ap-agent` is an agent, a
user in `finance` with a key of its own.

## Decision

1. **`fadodb mcp` is the door: a program that the agent's program
   starts on its own machine.** It speaks the protocol over standard
   input and output, a message to a line, and sends each call to a
   server as a command with the key of a user, as `fadodb exec` does
   (ADR-0062). It is told the server and the key the same way, and the
   key is never an argument. An agent's program is told:

   ```json
   {"mcpServers": {"acme": {
     "command": "fadodb", "args": ["mcp"],
     "env": {"FADODB_URL": "https://fado.acme.example", "FADODB_KEY": "fado_…"}}}}
   ```

2. **A tool is a command.** Its name is the command's, its arguments
   are the command's object, and its answer is the command's answer,
   as text. The tools are written from the table of the commands, so a
   command added to the engine is a tool with no work in the door. A
   tool says what the table says: what the command does, what it
   answers, and each member of its object with what it is.
   `ap-agent` approves an invoice by calling the tool `transition`
   with

   ```json
   {"class": "Invoice", "id": "FV/2026/11/0043", "transition": "approve",
    "key": "approve-FV/2026/11/0043"}
   ```

   and reads

   ```json
   {"document": {"class": "Invoice", "id": "FV/2026/11/0043", "title": "Paper Mill",
                 "state": "approved", "version": 3, "class_version": 1,
                 "created_at": 1794733200, "updated_at": 1794819600,
                 "state_entered_at": 1794819600,
                 "created_by": "bob", "updated_by": "ap-agent",
                 "state_entered_by": "ap-agent", "state_entered_from": "review",
                 "values": {"scan": null, "supplier": "Paper Mill", "total": "2400.00"}}}
   ```

   The door checks nothing of the arguments itself: the server is the
   one judge, so a tool and a command cannot come to behave apart.

3. **The marks of a tool come from the mark of its command.** A
   command that reads is marked as reading only. `delete` is marked as
   removing. `try_action` and `drain`, which run an action now, are
   marked as reaching outside the database; no other tool is. An
   agent's program uses the marks to let a tool that reads run by
   itself and to ask a person before one that removes.

4. **A refusal is an answer the agent reads.** A command the database
   refuses is answered as the database wrote it, with its code, its
   message, the place in the command and the hint, and marked as an
   error. Bob's assistant, with Bob's key, reads

   ```json
   {"error": {"code": "forbidden",
              "message": "bob may not take approve from review",
              "hint": "the rule of approve: INGROUP(\"finance\")",
              "path": "/access/transition/approve"}}
   ```

   When no answer comes through, because the server was not reached or
   its answer was cut off, the tool answers in the same form with a
   code of the door's own, `no_answer`, and a hint that says what is
   safe for the command that was sent. A command that reads is asked
   again. A command that commits a document takes a `key` (ADR-0055,
   item 3), so an agent sends it again safely. Any other command that
   writes, such as `define_class`, `set_user` or `try_action`, takes
   no key: it may have been carried out, and the agent reads what it
   would have changed before sending it again.

5. **The door is a caller as any other.** It holds no database. What
   an agent does through it is judged by the rules of the classes and
   written to the log under the user whose key the door was given. No
   tool and no argument says who is calling; `for` stays what it is
   everywhere, let through only while a substitution says so
   (ADR-0053). An agent that must do less is a user of its own with
   fewer groups (ADR-0057). A person may give their own key to the
   door of their own assistant, and what it does is then done as that
   person.

6. **Whoever starts a door may keep tools from its agent.**
   `--read-only` offers only the tools that read, and `--tools` only
   the ones named. This does not replace the rules: it keeps a tool
   out of the agent's sight, and a list of four tools costs an agent
   less to read than the whole list. An agent that calls a tool the
   door does not offer is told why.

7. **Two commands are no tool.** `subscribe`, because its answer is a
   stream and a tool is one question and one answer (question 3).
   `create_key`, because its answer holds the secret of a key
   (question 2). The files are not in the door (question 4).

8. **The door speaks both forms of the protocol**: the revision of
   2026-07-28, and the revisions that open with a greeting, from
   2025-11-25 back to 2024-11-05. A conversation is in the form it
   opens in. The door offers tools and nothing else of the protocol,
   no resources and no prompts; it lists its tools on one page; it
   carries several calls at once, so that a slow command holds up no
   other, and stops a call the agent's program stops.

9. **The door says of itself what an agent needs to begin**: that each
   tool is a command sent as the user of the key, that `whoami`
   answers who that is, that `list_classes` and `get_class` answer
   what there is, that `preview` writes nothing, how a refusal reads,
   and which tools take a key. This text is the sign above the door:
   an agent's program puts it before its agent.

   **The sign tells an agent to look before it builds** (the owner's
   word, 2026-11-16): before it defines a class, a table or a schema
   it lists what the database has and reads each one that may be the
   same thing, it asks the person it works for which one to use, and
   it never keeps a thing twice. The procedure is the first step of
   the designer's skill, but a skill is read only by an agent that was
   given it, and the door is the one place every agent passes. Ann's
   assistant, with a designer's key and no skill, would otherwise
   define a class `Employee` beside the table `People`. The four
   tools that build, `define_class`, `define_classes`, `define_table`
   and `define_schema`, say it again in their descriptions, since a
   program may show its agent the tools and not the sign. A door that
   offers none of them does not say it. It is a sign and no lock: no
   rule of the engine refuses a second list, which is the matter of
   ADR-0060.

10. **One door, one database** (ADR-0056). An agent that works for
    Acme and for Globex is given two doors, each with its server and
    its key.

## The owner's rulings

All of 2026-11-17, each as the record proposed. The door stays as it
is built. Three things the record proposed beyond it are wanted and
come after v0.6.0, each as its own step: the door at the server
itself, the tool that waits for a change, and files from a named
folder. He accepted the record the same day.

1. **Where the door stands: ruled.** Ann wants her assistant to work Acme's
   invoices from her laptop. `ap-agent` runs in the cloud, with nobody
   at a keyboard and no machine to start a program on.
   Proposed: both places, in two steps. First `fadodb mcp`, as in item
   1, which is built. Then the same tools at the server itself, at
   `/mcp`, with the key of a user in the header a command carries.
   The other way: `fadodb mcp` only, for now. The protocol's own way
   to sign in at an address is OAuth, which comes with the frontend
   and not in this phase. A key in a header works with the programs
   that let their user set a header, and not with the ones that only
   sign in through OAuth.
   On 2026-11-17 the owner ruled: both places, as proposed, and the
   door at the server is its own step after v0.6.0, built with the
   signing in of Phase 7.

2. **A key through the door: ruled.** Olga, an administrator, tells her
   assistant to make a key for `ap-agent`. The answer of `create_key`
   holds the secret, which would then stay in the conversation, in its
   transcript on Olga's disk, and with whoever runs the model.
   Proposed: the door does not offer `create_key`; a key is made at a
   terminal, where it goes into a file. Every other command on users
   and keys is a tool, and the rights decide, as everywhere.
   The other way: it is offered as any other command.
   On 2026-11-17 the owner ruled: as built, the door does not offer
   it.

3. **Waiting for a change: ruled.** `ap-agent` wants to be told when an
   invoice enters `review`.
   Proposed: a tool that waits. It takes what `subscribe` takes and a
   number of seconds, and answers the events that came in that time
   and the place to ask from next. It is its own step: the stream does
   not say today where the log stands when it is opened, and a tool
   that asks twice must miss nothing between.
   The other way: none. An agent asks `query` when it is started, and
   a program that hears the stream starts it.
   On 2026-11-17 the owner ruled: the tool that waits is wanted, as
   its own step after v0.6.0.

4. **Files: ruled.** Bob's assistant attaches `scan-0045.pdf` to an invoice.
   The bytes cannot sensibly pass through the agent's conversation, so
   the door would read the file from Bob's disk itself; an agent that
   was misled could then send any file Bob can read.
   Proposed: the files are in the door only when whoever starts it
   names a folder, `fadodb mcp --files ~/invoices`. The door reads
   from that folder and writes into it, and nowhere else. Its own
   step.
   The other way: no files through the door in this phase.
   On 2026-11-17 the owner ruled: files from a named folder are
   wanted, as their own step after v0.6.0.

## Consequences

- An agent is set up in two commands and a few lines of its program's
  settings, and can then do what a program can, under rules it cannot
  talk its way past.
- The tools cannot fall behind the engine, and a tool cannot behave
  apart from its command.
- The whole list of the tools is about 47,000 characters, which an
  agent's program reads once. `--tools` shortens it.
- An agent hears of no change by itself until question 3 is ruled and
  built, and puts no file on a document until question 4 is.
- The door listed in a catalogue for people to find, and the door with
  the skills as one thing to install, are steps after this one, each
  on the owner's word.

## Performance

No commit is touched: the door is a caller. Measured on the
development machine, against a server on the same machine: 2,000 calls
of `get` through one door took 0.25 s, with 16 carried at once. The
door starts, lists its tools and ends in under 10 ms. The command-line
tool grew by 65 kB, to 14.56 MB of its budget of 15.
