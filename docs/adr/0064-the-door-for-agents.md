# ADR-0064: The door for agents

- Status: Accepted; amended by ADR-0071 (an agent signs in at `/mcp` with a pass); amends ADR-0055 (`wait_for_changes` joins the table) and ADR-0058 (`after` takes `start`); the door at the server built on 2026-11-23, items 11 to 21; the tool that waits built on 2026-11-24, items 22 to 30; files from a folder built on 2026-11-24, items 31 to 38
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

## Settled while building the door at the server

Built on 2026-11-23 as the owner's ruling 1 has it, the same tools at
`/mcp`, with the passes of ADR-0071. Settled by me while building, each
the owner's to strike:

11. **One message to a POST, one JSON object to answer it.** The door
    at `/mcp` never answers with a stream, which both forms of the
    protocol allow a door to choose. Nothing it does streams: a tool is
    one question and one answer.
12. **No session.** The door keeps nothing between two requests, as the
    revision of 2026-07-28 has it, and mints no session for the
    revisions before, which lets a door keep none. A greeting is
    answered and kept nowhere; a request of those revisions says its
    revision in the header `MCP-Protocol-Version`, and one that says
    none is taken for 2025-03-26, as the protocol says. A GET, for a
    stream the door does not open, and a DELETE, for a session it does
    not keep, are answered 405.
13. **The headers of the modern form are held to its body.** A request
    of 2026-07-28 names its revision in `MCP-Protocol-Version`, its
    method in `Mcp-Method` and the tool it calls in `Mcp-Name`; one
    that leaves a header out, or says something else than its body, is
    refused 400 with -32020, and a revision the door does not speak
    with -32022, as that revision asks.
14. **A method the door does not have** is answered 404 in the modern
    form, as that revision asks, and 200 with the error in the forms
    before it, to which a 404 says that their session ended and makes
    them greet again.
15. **Every other error of the protocol is answered 200**, an unknown
    tool and arguments that are no object among them, and so is a
    command the database refuses, which is the answer of its tool, as
    at `fadodb mcp`.
16. **Who calls is asked as at `/v1/`**, with a key or a pass in the
    header, and never with the session of a browser, which is for the
    server's own pages. A request that proves nobody is refused 401
    with `WWW-Authenticate: Bearer resource_metadata="…"`, naming
    `/.well-known/oauth-protected-resource/mcp` when the server knows
    the address it is reached at (ADR-0071, items 8 and 34); a server
    that does not know it names none.
17. **A page of another site is refused 403**, as the protocol asks of
    a door at an address: a request whose `Origin` is not the server's
    own address. A request from a program sends no `Origin`.
18. **A tool runs as a command sent to `/v1/` does**: one of the
    server's slots for commands, its time, and its refusals `timeout`
    and `caller_left`. A notice that stops a call stops only a call of
    the same caller, since without a session an id is the caller's
    alone; a closed request stops its call as well.
19. **The log and the counts.** A request to the door is a line of the
    server's log with `"door":"mcp"`; a tool called is logged and
    counted with its command, and any other request under `mcp`. The
    counts gain the status 202, which a notice is answered with.
20. **The server says where agents connect** when it starts, beside
    where people sign in: `agents connect at <address>/mcp`.
21. **Every tool is offered at `/mcp`.** `--read-only` and `--tools`
    are for whoever starts `fadodb mcp`; at the server the rights of
    the classes decide what each caller may do. The three commands
    that are no tool (items 2 and 7) are none here either.

## Settled while building the tool that waits

Built on 2026-11-24 as the owner's ruling 3 has it ("yes, go ahead
with the waiting tool"). Settled by me while building, each the
owner's to strike:

22. **One command, `wait_for_changes`**, of the command set and so a
    tool at both doors, `POST /v1/wait_for_changes`, and the Go call
    `WaitForChanges`. It takes what `subscribe` takes, `classes`,
    `kinds`, `to_states` and `after`, and `seconds` and `limit`. It
    answers `events`, each as the stream tells it, `after` and `more`.
23. **It answers what there is, or waits for it.** It reads the log
    from its place to the end and answers the events the caller may
    hear of; when there are none it waits for the next commit, up to
    `seconds`, from 0 to 60, and answers none when they pass. Left out,
    `seconds` is 0 and the call answers at once, so that an agent
    started on a timer asks what happened since it last looked. Sixty
    is within what the programs of agents give a tool to answer.
24. **`after` is the place to ask from next.** Given back, it misses
    nothing and tells nothing twice, also of what came between two
    calls. It is the last event the call read, which the caller may
    not be able to read: so it says when that event was written and
    nothing else of it, not its class, its document or its kind. The
    other way: the last event the caller heard, which says nothing,
    but makes a caller that hears little read the log again from there
    at each call, a longer stretch each time.
25. **`start` is the place before the first event of the log**, which
    no event's id can be. A wait in a database whose log holds no event
    answers it, so that the first events of a new database are not
    missed between two calls, and `subscribe` takes it too, for a
    stream that begins with the first event.
26. **An answer holds 100 events, or `limit`, up to 1,000.** A full
    answer stops at its last event and says `"more": true`; the next
    call takes the rest at once. `more` says the same of a call that
    was stopped before it read the log to its end.
27. **Who hears of what is the stream's rule** (ADR-0058, item 4, and
    ADR-0067 for a content administrator, whose reads past the rule
    the log of reads names under `wait_for_changes`). A caller who acts
    for somebody may wait, unlike the stream, which is heard as
    oneself: it hears of an event when it may act for that person on
    the event's class now, as a command on that class would be let
    through, and may read the document so. This is how Claude, which
    acts for its person at `/mcp`, waits (ADR-0071, item 31). The
    caller is judged as it is when the call begins.
28. **At the server a wait holds the place of a stream**, not the slot
    of a command, at `/v1/` and at `/mcp`: as many wait at once as the
    server holds streams, 256 unless its settings say otherwise
    (`limits.streams`), and none of them keeps a command waiting. A
    wait that finds no place is refused `busy`. It runs for the
    longest wait and then the time of a command, and it is counted in
    `fadodb_streams` while it waits.
29. **A wait that is stopped answers what it has**, with its place:
    when its time is up, when its caller's request ends, and when the
    server stops and ends its streams.
30. **Nothing is kept between two calls**: no session, no subscription
    on the server. The place travels with the caller.

## Settled while building files from a folder

Built on 2026-11-24 as the owner's ruling 4 has it, after the fix of a
test ("go, test fix first"). Settled by me while building, each the
owner's to strike:

31. **Two tools, each one transfer.** `put_file` reads a file of the
    folder, puts its bytes and answers the facts of the file; the
    document is then written with `create` or `set`, so that the rules,
    the version and the answer are the command's, and the bytes and the
    document stay [two steps](../manual/files.md#two-steps). The other
    way: `put_file` names a document and sets its variable, as
    `fadodb file put --class … --variable …` does, two calls in one
    tool. The two tools are the door's and no commands, as the bytes
    are no command (ADR-0055, item 9): the table does not list them,
    and the list of a door given no folder is as it was.
32. **`get_file` opens a file by its hash**, through the document that
    holds it, with `version`, `for` and `reason`, as `GET /v1/file`
    does. The agent reads the hash in the document with `get`. The
    other way: by the variable, with a `get` inside the tool, as
    `fadodb file get --variable` does.
33. **The folder is held as a root of the operating system's files**
    (Go's `os.Root`). A path in it is taken, and a whole path that lies
    in it; `..`, a whole path elsewhere and a link in the folder that
    points out of it are refused with `bad_value` at `/path`. The door
    checks a path before the root is asked, and the root refuses what
    gets past. The folder is there when the door starts, or the door
    does not start.
34. **No file is written over, and no folder is made.** `get_file`
    refuses a path where a file is, before it asks the server, and
    writes into a folder of the folder only when it is there.
35. **The bytes are checked.** `get_file` hashes what it writes and
    leaves the file only when it came whole with the hash asked for;
    otherwise it takes it away, and answers `storage` when the bytes
    differ, or that no answer came through when they broke off.
36. **Left out, the path is the name the file was put under**, its last
    part only, since a name is the words of whoever put the file.
37. **`--read-only` offers `get_file`**, which reads the database, and
    not `put_file`; `--tools` names either. Neither is marked as one
    that only reads, since each changes the folder or the database, and
    neither as one that destroys.
38. **At `/mcp` there is no tool for files.** The server has no folder
    of the caller's, and the bytes would pass through the conversation,
    which ruling 4 set aside. A person puts a file there with
    `fadodb file put`, a program with `POST /v1/blobs`. The other way:
    a tool whose arguments carry the bytes as base64, which costs an
    agent its context and puts the bytes in its transcript.

## Consequences

- An agent is set up in two commands and a few lines of its program's
  settings, and can then do what a program can, under rules it cannot
  talk its way past.
- The tools cannot fall behind the engine, and a tool cannot behave
  apart from its command.
- The whole list of the tools is about 47,000 characters, which an
  agent's program reads once. `--tools` shortens it.
- An agent hears of a change with `wait_for_changes`, and puts files on
  documents and reads them through a folder whoever starts its door
  names.
- An agent that cannot start a program, Claude on claude.ai among them,
  reaches a server at `/mcp`, with a pass a person gives it or a key.
- The door listed in a catalogue for people to find, and the door with
  the skills as one thing to install, are steps after this one, each
  on the owner's word.

## Performance

No commit is touched: the door is a caller. Measured on the
development machine, against a server on the same machine: 2,000 calls
of `get` through one door took 0.25 s, with 16 carried at once. The
door starts, lists its tools and ends in under 10 ms. The command-line
tool grew by 65 kB, to 14.56 MB of its budget of 15.
