# Roadmap

**Current status: Phase 8, the analytical engine, started on 2026-11-25. Phase 7, the browser VM and the reference frontend, was released as v0.7.0 on 2026-11-24.**
The Go API (`fado`) opens a database file, defines schemas, classes and
explicit tables, and creates, changes, reads and deletes documents as
commits: assignments and defaults, the auto recalculation, typed history
rows, child rows for composites, an event log with idempotency keys and
business dates, attachments behind a blob store, `LOOKUP` against tables,
a filter over a class and read-only SQL over the views, with previews that
write nothing. `fadodb formula eval` evaluates a formula and `fadodb
check` verifies a file. The rules of a class run inside the commit with
the actions `set`, `reset`, `refresh` and `emit`, and triggered
variables are computed at their triggers; what a rule does to other
documents and to tables runs through the outbox; and a rule can call an
action that has a definition, implemented by a Go function of the host
program or sent by the engine as an HTTP request to the hosts the host
program allows, with its result written back to the document; and a
formula can read the documents of another class, one value or a count,
a sum, the smallest or the largest. A formula reads the life of its
document, a transition may carry its time, and the engine moves the
document by itself when that time comes. The access rules of a class
are enforced (ADR-0049), a rule a class leaves out lets nobody
through, four system groups hold the work that is not about one
document, and a content administrator's act past a rule carries a
reason that the event keeps (ADR-0052); a caller that acts for
somebody is let through only while a substitution says so (ADR-0053);
and the server comes with its phase.
Since v0.1.0 the records ADR-0022 to
ADR-0038 fixed what the first embedder needs and what Phase 2 measured:
SQLite as the transactional store and the in-memory index for lookups, by
measurement (ADR-0035, ADR-0036), the compiled `LOOKUP` and explicit
tables (ADR-0037), the filter and the SQL endpoint (ADR-0038), and class
versions with terminal states and migrations (ADR-0039). Phase 3 opened
with its design in three records, accepted on 2026-11-09: ADR-0040,
transitions as commits, with eventless chains, snapshots, the allowed
transitions and the `_states` view; ADR-0041, field rights and
per-transition rules enforced in the Go API; and ADR-0042, a moment the
caller chooses for previews and reads. It closed on 2026-11-09 with the
statechart running in the Go API: `STATE()` and `ID()` (#40), the
transition command with guards, eventless chains as further commits, the
listing of open transitions, the preview of a transition and the
`_states` view (#42), the rights table and the per-transition rules
enforced on every path for the principal (#43), `At(t)` for previews and
reads (#44), and the measurements of `docs/perf/v0.3.0.md` against every
budget of the three records (#45). Phase 4 opened with its design in
three records, accepted on 2026-11-10: ADR-0043, rules inside the
commit, with the synchronous actions and triggered variables; ADR-0044,
the outbox, its worker and the engine's own actions, which run as the
person whose command queued them; and ADR-0045, actions as an
extension point, with declared HTTP actions, Go actions an embedder
registers, connections and results written back to the document.
Formulas that read other documents got their record, ADR-0046,
accepted on 2026-11-10; conditions on time and timers, which the owner
ruled into the phase that day, got theirs, ADR-0047, accepted on
2026-11-11. Its first step runs the rules
inside the commit: the triggers of each commit, the synchronous actions
`set`, `reset` and `emit`, and the rules a preview reports (#51). Its
second computes the triggered variables at their triggers, with the
`refresh` action and the moment of each value, under engine schema 3
(#52). Its third makes `Reset` and `Refresh` commands of the Go API
(#53). Its fourth is the outbox, in two parts: first the rows a commit
writes, the worker and `Drain`, retries and dead actions, their events,
the listing and the retry by hand, with `create` as the first action and
the test that stops the process at every step (#54); then the rest of
the engine's own actions, `transition` and `refresh` of another
document, `each` on a `create` and the `table.*` actions, and the
refusal of a redefinition that would break another class's rule (#56).
Its fifth opens the engine to actions others write, in three parts, of
which the first is built: action definitions with typed parameters,
stored with `DefineAction`; Go functions the host program gives to
`Open`, called by the outbox with the arguments and the event that
fired the rule; the catalogue, the preview and the trial of an action;
and the published schemas under `api/` (#57). The second, built too,
sends an action as an HTTP request: a definition with a `request`,
`http.call`, secrets the host supplies at the moment of sending and that
go only to the hosts of their connection, the signed call for a
developer's own service, and `fado/outbound`, without which a database
sends nothing (#58). The third, built too, writes an action's result
back to the document with `into`, so that a process goes on from the
answer, and adds one generic webhook definition and a guide to writing
an action for any REST API. By the owner's ruling of 2026-11-10 the
phase writes no definitions for particular services, which come with
real needs, and the package format waits for the first of them. Its
sixth step, formulas that read other documents, has its design in
ADR-0046, accepted on 2026-11-10: a class as the source of `LOOKUP`,
with a count, a sum, the smallest and the largest. Its first part is
built: `COUNT`, `SUM`, `MIN` and `MAX` in a `LOOKUP` of a table, with
one answer on the Go VM, SQLite and DuckDB, and a lookup narrowed by an
equality that is one term of an `AND` (#62). Its second is built too:
a class as the source, read with the engine's eyes by a statement
inside the commit and narrowed through the index of an indexed
variable; a variable computed from other documents declares its type;
a redefinition may not break another class's formula; and the pattern
of parallel approvals runs in `conformance/rules/reads.json` (#63). Its
third closes the step: `DefineClasses` defines several classes in one
call, all or none, so that classes which name each other can be defined
at all, and the Purchase and Approval samples under `examples/classes/`
are the pattern written out (#64). Its seventh step, time, has its
design in ADR-0047, accepted on 2026-11-11: a formula reads the life of
its document, when and by whom it was created, changed and moved into
and out of each state; every entry into a state is recorded, a return
into the same state too; `Due` lists the documents that would move now;
and a transition may carry `after` or `at`, so that the engine moves
the document when the time comes. By the owner's ruling of that day a
timer is written on a transition. Its first part is built: the document
keeps who created it, last changed it and moved it into its state, and
where it came from; the log says how each entry came about; the
`_states` view has a row for every entry, with who and how at both
ends; and a formula reads the row with `CREATEDAT()`, `CREATEDBY()`,
`UPDATEDAT()`, `UPDATEDBY()`, `ENTEREDAT()`, `ENTEREDBY()`,
`ENTEREDFROM()` and `VERSION()`, under engine schema 5. Its second part
is built too: the functions of a named state, `FIRSTENTERED("approved")`
and `FIRSTENTEREDBY`, `LASTENTERED`, `FIRSTLEFT` and `LASTLEFT` with
their `...BY`, `ENTRIES`, `DAYSIN`, `FIRSTEDITOR` and `LASTEDITOR`, read
from the document's own events once for a command. Its third part
closes the checking half: `Due` lists the documents of a class that a
commit would move now, or at a moment given with `At`, each with its
state and the state it would move to; a guard that translates to SQL
runs as one filter, and a filter that compares an indexed date with the
clock now uses the index. The firing half has its first part: a
transition carries `after` or `at`, fires in the chain of the first
commit that touches the document once its time has come, with the
origin `timer`, and shows its moment in `Transitions` and in `Due`. Its
second part closes the step: a commit that leaves a document in a state
queues a wake-up in the outbox for each of its timers, and the worker
touches the document when one is due, as the person whose commit queued
it, by the owner's ruling of 2026-11-12; `conformance/rules/timers.json`
is its suite. The phase closed on 2026-11-12 with its eighth step,
the measurements of `docs/perf/v0.4.0.md` against every budget of the
five records (#73). A command that moves many documents in one
transaction follows the release as an addendum to this phase, with its
design in ADR-0048: `TransitionMany` moves up to 10,000 documents of a
class along one transition, a commit each, and `PreviewMany` tries the
batch and writes nothing (#79). It is released as v0.4.1, with its
measurements in `docs/perf/v0.4.1.md`: 27,257 documents a second on
disk against a budget of 15,000.
By the owner's ruling of that day a document that is refused
does not stop the others: they move, and the refused one is reported
with its reason.
By the owner's rulings of that day a formula sees every document, and
`QUERY`, which returns lists, is built with the first layout or report
that needs one; tables stored outside the file come with the analytical
engine in Phase 8. Phase 5 opened on 2026-11-13 with its design in
three records: ADR-0049, who may see, create, change and delete a
document, with the read rule in every filter; ADR-0050, whether the
one who acts is a person, an agent or a service, and for whom; and
ADR-0051, who may define, operate and query, and the rules of a table.
Two more followed that day at the owner's request: ADR-0052, the
system groups that split the administrator in four, with a content
administrator who acts past the rules for a stated reason; and
ADR-0053, a substitution the engine keeps and checks, for a person who
stands in for another and for an assistant. The owner accepted the
five on 2026-11-13 with his rulings: a rule a class leaves out lets
nobody through, work the host gave to nobody is done by nobody, the
one who acts is `USER()` whoever they act for, and the engine checks
for whom.
By the owner's ruling of that day the phase is the engine's part: the
engine's own users, groups and API keys, and the filter on the change
stream, come with the server in Phase 6. Its first step is built: the
four rules of a class, `read`, `create`,
`update` and `delete`, are enforced on every call of the Go API; a
document its caller may not see is answered as one that does not exist
and left out of every filter, count and list of what is due; a refusal
names the rule; a transition without a rule of its own asks `update`;
and `conformance/access/` is the suite. Its second step is built too:
a filter decides
the caller's part of a read rule before it writes its statement, so a
caller who sees only their own documents lists them through the index
on the owner, and a filter with a read rule costs what the same filter
costs without one. Its third step is built on ADR-0050: a
principal says whether it is a person, an agent or a service, and for
whom it acts; `USERKIND()` and `ACTINGFOR()` answer both to a formula;
and every event records them, under engine schema 6. Its fourth step
is built on ADR-0051: defining, operating the queue and running SQL
each ask who the caller is, and a table has a read rule and a write
rule. Its fifth step is the system groups of ADR-0052: the
administrator's flag is gone, and `administrators`, `designers`,
`content-administrators` and `analysts` hold what it did, each its own
part; the host program gives them to a caller apart from its other
groups; a class can ask for them with `INGROUP`, and for `everyone`,
which is every caller that has an id; and work nobody was given is
done by nobody, a call without a principal included.
Its sixth step closes what a class does not say: a rule a class or a
table leaves out lets nobody through but a content administrator, the
refusal names the rule that is missing, checking a class notes each
one without refusing it, and every sample writes its four rules.
Its seventh step is the override: a content administrator's command
that gets past a rule needs a reason, its event keeps the rules lifted
and the reason, a preview and the listing of transitions say so
beforehand, and what the commit queued acts for the same reason.
Its eighth step keeps what a designer does in sight: every version
of a class, a schema, a table and an action records who defined it,
what they were, for whom they acted and when, and one call lists them.
With it the system groups of ADR-0052 are built.
Its ninth step is the substitution of ADR-0053: the engine brings the
class `Substitution` to every database, a document of it says who may
act for whom, from when, until when and on which classes, and every
call that names somebody it acts for is refused unless one says so;
the person gives one, a database administrator when the person
cannot, and a host program that keeps its own says so at `Open`.
The phase closed on 2026-11-13 with its tenth step, the measurements
of `docs/perf/v0.5.0.md`, on disk with a million calls of each kind:
every budget of the five records holds, and so do those of Phases 2, 3
and 4 on the same build. What it leaves for later, each with its own
record when the owner asks: a log of the reads a content administrator
makes past a rule, with the reads of SQL; the engine's own users,
groups and API keys, and the filter on the change stream, which come
with the server in Phase 6.
One record is proposed ahead of the layouts, at the owner's
request of 2026-11-13: ADR-0054, two properties of a layout component,
`mobile` and `desktop`, that say on which screen it shows.
Phase 6 opened on 2026-11-13. Its design is five records, which the
owner accepted on 2026-11-14 with his rulings on their 16 questions:
ADR-0055, every command of the Go API as one JSON object, sent to one
address for each command; ADR-0056, one database for each customer,
and a server that serves one database, which the address alone names;
ADR-0057, users, groups and keys kept in the database, where an
administrator keeps every user's groups and alone makes a key;
ADR-0058, the stream of changes as the log read forward, over a plain
HTTP reply in place of the WebSocket first planned, each entry an
event and nothing said of a document that leaves a subscriber's sight;
and ADR-0059, `fadodb serve`, which serves a database that
`fadodb init` made, runs no action written as a Go function and takes
its settings from the operator. By
his rulings on the scope of the phase and on ADR-0055, a command has
one address and its id
travels in its JSON, a field a command does not know is refused, and an
id holds no control character; the door for agents, an MCP server, is
built in this phase and not in Phase 11; the engine knows keys only,
and passwords and a company's sign-on come with the frontend; layouts
move to Phase 7; a database is closed to everybody but its owner; and
the phase gains a check of a class and of an action from the command
line, one call that answers the flow of a class, and as its last step,
with a record of its own, a way in for messages from outside. The
first step is built: every call of the Go API that reads or changes a
database is also a command, a name and one JSON object answered by one
JSON object, which the Go call `Exec` runs; the three script suites run
through the commands as they run through the Go API. The second step
is built too: the HTTP door, the package `fado/httpdoor`, by which a
program that has opened a database lets it answer the commands as
`POST /v1/<command>`. The program says where it listens and who sends
each request; the door holds a command to its limits, the largest
request, the time one command may run and how many run at once, and
answers with the status of the code. The three suites run a third way,
as requests to a server that listens on the machine. The third step
added the files beside the commands to the door: a file is put at
`POST /v1/blobs`, read through the document that holds it at
`GET /v1/file`, under the rules of the document, and its bytes are read
by their hash alone, by a content administrator, at `GET /v1/blob`.
The fourth step gave a database its own users, groups and keys
(ADR-0057): a program opens it with `Users`, nine commands keep them,
from `create_user` to `whoami`, a key is one line of text that begins
`fado_` and is shown once, the database keeps a hash of it, and every
change to a user or a key is kept. The door asks for a key with
`httpdoor.Keys`; checking one takes 0.1 µs and reads nothing. The
fifth step is `fadodb serve`, built in parts. The first part is in:
`fadodb init` makes a database, `fadodb user add` and `fadodb key new`
make its first administrator on the machine that holds it, and
`fadodb serve` serves it to callers who send a key. Told nothing, it
listens on this machine only, and it refuses to listen beyond it
without a certificate unless it is told that something in front
encrypts. While it serves it holds the database alone: a second
program that opens it is refused and told who holds it. It writes one
line for each request, answers its health, and stops cleanly. The
second part is in too: the operator's settings, a JSON file that lists
the hosts the actions of the database may call and the limits of the
server; the secrets of its connections, read from a directory or the
environment at every request; and the refusal of an action that needs
a Go function, which a program of the company's own runs
(`examples/server`). The third part is in as well: the counts of the
server, for an administrator, at `GET /v1/metrics`, in the form
Prometheus reads. The sixth step is the stream of changes, built in
two parts, both in: the Go call `Subscribe`, which tells a caller of
every event of the log it may read of, in the order of the commits,
with the read rule asked for each; and its address on the server,
`POST /v1/subscribe`, a reply that stays open and says each event as
it is written, which a caller that was away picks up where it stopped.
The seventh step is the description of the commands for other
languages, `api/openapi.json`, written from the engine's table of the
commands. The eighth is the check of a class and of an action from a
file, `fadodb class check` and `fadodb action check`, so that an agent
asks whether the engine would take a definition before it reaches a
database. The ninth is the command-line tool as a caller of a server
(ADR-0062): `fadodb exec` sends any
command to a server with the key of a user and prints the answer;
`--xlsx` writes the documents of a query as an Excel file, in which a
number is a number and a day a day, so that an export is written in no
locale, and `--csv` writes them for a program; `fadodb file put` and
`fadodb file get` put a file on a document and read one. With it an
answer writes `<`, `>` and `&` as themselves. The tenth is the flow of
a class in one call (ADR-0063): the
command `flow` answers the states of a class, each with the documents
that are in it now and how long the stays in it lasted, and its
transitions, each with the moves along it, counted over the documents
the caller may read. The eleventh is the door for agents (ADR-0064):
`fadodb mcp`, a program that an
agent's program starts and speaks the Model Context Protocol to, which
offers each command as a tool and sends each call to a server with the
key of a user; a refusal reaches the agent as the database wrote it.
The twelfth is the image for containers: the repository builds an
image that holds the tool and nothing else, in which `fadodb serve`
runs as a user that is not root, with the database in a volume;
`fadodb health` asks a server whether it answers, which is how Docker
watches one; and a compose file keeps a served database running. A
release publishes the image at `ghcr.io/mojohand70/fadodb`, under its
version and under no other name; v0.6.0 is the first.
The thirteenth is the way in for messages from outside (ADR-0065):
an inlet, a definition that is the
mirror of an action's, says how a message of a system that speaks
only its own language proves who sent it and which one command it
becomes; six commands check, try, define, read, list and remove one;
the server takes a message at `POST /v1/in/<inlet>`, with no key,
once the operator's settings have `inbound`, checks its proof and
carries the command out as the service user the inlet names, once
however often the message comes; the sender is answered a status and
a code, and nothing of the database; and `fadodb inlet check` asks
the engine about an inlet in a file and shows what a message would
become.
The phase closed on 2026-11-17 with the measurements of
`docs/perf/v0.6.0.md`: every budget of its records holds, the server's
as ADR-0066 measures it, beside the Go API called by a request, and so
do those of Phases 2 to 5 on the same build. The owner accepted that
day the records that were built as they proposed, ADR-0062 to
ADR-0065, with his rulings on their questions, and ADR-0066. What the
phase leaves for later, by his rulings of the same day: the log of the
reads a content administrator makes past a rule, with the reads of
SQL, which gets its own record as the first step after the release;
and three things for agents, each its own step: the door at the server
itself, with the signing in of Phase 7, a tool that waits for a
change, and files through the door.
Phase 7 started on 2026-11-17, and the owner ruled its scope that
day. Its first step, the log of reads (ADR-0067), is built: a content
administrator sees past a rule only with a reason, and what they saw,
with every SQL statement, is kept in a log that database
administrators read with the command `reads`. Then, in this order: the
browser VM, which is built: every function a page runs, exact numbers
and money too, passing every case of the conformance suite a page runs,
200 formulas recalculated in about 13 µs of the 1,000 µs budget, and
attached to every release; what a class says to people (ADR-0068),
whose labels, descriptions, columns of The List, formats and words in
other languages are built; the sections of a class
(ADR-0069, the screen being the face's and no layout the engine's),
which are built: the outline of a class, a section inside a section,
and a section's rights for every variable in it, the most specific name
winning; the class bundle, `get_bundle` and `GET /v1/bundle`, which is
built: a class in the person's words and its formulas compiled, proved
in the browser VM against the server's answers; signing in
for people at `fadodb serve` itself (ADR-0071, ADR-0088), of which
invitations and passkeys on the device in use, passwords, the second
step for administrators and designers, sessions and a company's
sign-on and the passes of clients by OAuth, Claude among them as an
agent acting for the person, are built, and the code a phone scans to
add its passkey, then the end of passwords and the second step, are
planned; the pages of a
face, which the server hands out from a folder, `--pages` (ADR-0073),
and which is built: the server has no pages of a face of its own, and
the open face, FadoDB Workshop, is in a repository of its own, where a
person signs in, reads the list of a class and reads, changes, makes
and moves a document of plain values; work on it stopped on
2026-11-21, and a designer made of lists is not built; the app
(ADR-0077), a name for the classes that
belong together, which is built: `define_app`, `get_app`, `list_apps`,
and a class or a table that says which app it belongs to (a table's,
ADR-0079, proposed), while an app's own
groups, and exporting an app, are not built; the door for agents at
the server itself, at `/mcp`, which is built: the same tools, for a
caller that proves who it is with a pass a person gives, as Claude
does, or with a key, in both forms of the protocol, with no session;
and the tool that waits for a change, `wait_for_changes`, which is
built: an agent is answered the events after its place, or waits for
the first up to a minute, and is told where to ask from next, and an
agent that acts for its person, as Claude does, waits too. And files
through the door, which are built: an agent's door started with
`--files FOLDER` offers `put_file`, which puts a file of the folder and
answers its facts for `create` or `set`, and `get_file`, which writes a
file a document holds into the folder, by its hash, over no other
file; nothing outside the folder is read or written, and the door at
the server, which has no folder of the caller's, offers neither.
The phase closed on 2026-11-24 with the measurements of
[docs/perf/v0.7.0.md](docs/perf/v0.7.0.md): every budget of its
records holds, and beside v0.6.0 the same evening a commit is no
slower in memory and 2 to 3% slower on disk. No starter is built into
the program (the owner, 2026-11-24): an app comes with a purchase of
FadoDB, or is installed from the marketplace by exporting it from one
database and importing it into another, which is built after v0.7.0.
What the phase leaves for later, by the owner's word of the same day
("Can we close v0.7.0 now without" his face, and "don't wait, close
v0.7.0 now"): its test, in which an agent with only the skills and the
door is told a business story in plain words and builds the table and
the class, and the people of the story use them in a browser, runs
when the owner's face is finished; signing in with a passkey
(ADR-0088, proposed, its premises ruled the same day), of which the
first of five steps is in the release, comes after it: the second,
invitations and passkeys on the device in use, is built since, and
until passwords go a person may still sign in with a password, and an
administrator or a designer with a code as well; and ADR-0085, a list sent by an action and taken
from its answer, accepted, is built after the release. Built as they
propose and still proposed, each the owner's to accept or strike:
ADR-0079 to ADR-0084 and ADR-0086.
Three records were accepted by the owner on 2026-11-22. Two are of
2026-11-20. ADR-0075, the engine makes the
index of every question a class declares and says which question it
cannot serve, so that a class of a hundred million documents answers
a declared question in under a thousandth of a second: nothing of it is
built yet. ADR-0076, a
value worked out when it is read, `"mode": "live"`, with `QUERY` for
the list of other documents on a document and `REF` for what a `ref`
compares with: built are a live variable that reads its own document,
the caller and the clock, the command `live` that reads it, the check
of a `ref` when it is set, what points at a class in the answer of
`get_class`, and the list: a `QUERY` over the documents of a class as
the whole formula of a live variable, with `THIS.name` and `REF()` in
its condition, read a page at a time for its reader. Not built yet: a
`QUERY` over a table, or with counts or sums (a `QUERY` kept in a
triggered variable is built in Phase 8, and so is a `LOOKUP` in a live variable); `REF` outside the condition
of a `QUERY` and with a class and an id; the engine's own index on
every `ref`, which is ADR-0075's, so a list at scale wants
`"index": true` on its pointer today. ADR-0081, proposed, functions over a
list, each with its SQL from the start: built over a list that is a
`QUERY` are `COUNT`, `SUM`, `MIN`, `MAX`, `AVERAGE`, `MEDIAN` and
`COUNTUNIQUE`, the forms with a condition (`COUNTIF`, `SUMIF`,
`MINIF`, `MAXIF`, `AVERAGEIF`), and `ANY` and `ALL`, each one
statement asked for whoever reads the live variable that holds it;
`AVERAGE`, `MEDIAN` and `COUNTUNIQUE` stand in the column position of
a `LOOKUP` as well. The same functions work over the lines and the
tags a document holds, `SUM(lines.qty * lines.price)`, in a computed
variable, a guard and a rule; not yet in a read rule, a filter, a
title, nor in the page's own evaluator. One value out of a list is
built over both kinds of list: `FIRST`, `LAST`, `INDEX`, `JOIN` and
`CONTAINS`, by the order of the list. A list is narrowed, ordered and
cut inside such a function: `FILTER`, `SORT`, `TOP` and `UNIQUE`, each
a part of the function's one question. With them every function of
the catalogue is built. Decided not now: a list one of them gives as
a variable of its own.
The third is of 2026-11-21: ADR-0078, a decision stands on what the
document holds. A command says which version of a document its sender
saw, the `write` right of a triggered variable
says who may bring its value again, and what a condition needs from
outside the document is brought onto it before the condition is asked.
Nothing of it was built in Phase 7; Phase 8 builds it first, and its
first step is built: the version a command saw.
Five more records proposed while the phase ran are built and wait for
the owner's acceptance, all of them on the tables: ADR-0079, a table
belongs to an app; ADR-0080, a column added to a table in use;
ADR-0082, removing rows and removing a table; ADR-0083, who removed a
table or words, and when; and ADR-0084, changing a table that holds
rows. The owner ruled on 2026-11-23 that what ADR-0075, ADR-0076,
ADR-0078 and ADR-0081 leave unbuilt does not hold back v0.7.0: what of
it is not built when the phase's own steps are done is built first
after the release, as the log of reads was after v0.6.0.
Phase 8 started on 2026-11-25. It builds first what the records
accepted during Phase 7 leave unbuilt, as the owner ruled. ADR-0078
comes in four steps: the version a command saw, which is built
(`version` on `set`, `reset`, `refresh`, `transition` and `delete`,
`versions` on `transition_many`, the refusal `stale_version`, and
`seen_version` on the event; engine schema 14, to which a database of
v0.7.0 is brought when it is opened); who may bring a value again,
which is built too (the `write` right of a triggered variable is asked
of whoever sends the `refresh`); a
trigger `before:` a transition, with a guard that reads the document
alone, which is built as well (a guard that looks in a table is
refused when a class is defined, and the value is brought by a
triggered variable before the guard is asked); and a list that is
kept, which is built: a `QUERY` as the whole formula of a triggered
variable, held with every version of the document. With it the four
steps of ADR-0078 are built; a kept value that follows its source,
its item 6, comes with the analytical engine. Then what ADR-0076 and ADR-0075 leave:
a table as the source of a list, a `LOOKUP` in a live variable, and
`REF()` wherever `ID()` may stand with `REF("Order", "ORD-17")` for a
named document, which are built, as are the way the engine chooses
between two indexes that fit one question, by looking into each, and
the indexes the engine keeps by itself on every pointer, on the state
and on the two moments of a document; and the note the engine gives when a class is defined, on each
question of it that no index serves; counts, sums and grouping in a
`QUERY`, an index for the key of a lookup that is no pointer, and the
stop of a free question that runs too long, which are not. Then the lists in and out of messages and requests, ADR-0085.
After these comes the analytical engine itself: DuckDB beside the
documents, fed from the commits, for the question that reads
everything; the mining views; a value that follows its source or the
clock, worked out for many documents in one statement; tables kept in
DuckDB or in a database of the customer's own; and the program with
DuckDB built for each operating system. Nothing of the analytical
engine is built yet.
This page is updated when a phase starts and when it ends;
it never describes a phase as done before its acceptance criteria pass.

Legend: ✅ done · 🚧 in progress · ⬜ not started

| Phase | Goal | Status |
|---|---|---|
| 0 | Foundations: module, gates, CI, release pipeline, ADRs, docs | ✅ |
| 1 | Formula engine: lexer, parser, binder, bytecode, Go VM, SQL generator with SQLite and DuckDB dialects, conformance suite | ✅ |
| 2 | Document model and store: primary-store benchmark, variables with defaults, `json` type, enum values, range constraints, named schemas and object values with the predefined vocabulary, attachments as `File` values with a blob store, class metadata and title, current, history and child tables, commits with idempotency keys and business dates, preview and validate without writing, event log, migrations, LOOKUP, queries | ✅ |
| 3 | Statechart: the transition command with guards, eventless chains, exit and entry snapshots and the `_states` view, allowed transitions and the preview of a transition, `STATE()` and `ID()`, per-transition access rules and field-level rights per state, statechart conformance cases | ✅ |
| 4 | Rules and actions: event-condition-action rules run inside the commit, the synchronous actions `set`, `reset`, `refresh` and `emit`, triggered variables with their moment, the transactional outbox with its worker, retries and dead rows, the engine's actions on other documents and tables, actions as an extension point with definitions, declared HTTP actions, Go actions the embedder registers, connections and results written back, the delivered event, formulas that read other documents (`LOOKUP` over a class, with a count, a sum, the smallest and the largest), conditions on time and timers | ✅ |
| 5 | Security: the class-level access rules enforced on every path, who may see, create, change and delete a document, with the read rule compiled to a SQL predicate; the kind of actor, a person, an agent or a service, and whom it acts for, on every event, checked against a substitution the database keeps; who may define, operate and query, by four system groups; a content administrator who acts past a rule with a reason; the rules of a table | ✅ |
| 6 | Server and CLI: the JSON command set over HTTP, one address for each command; users, groups and API keys kept in the database, and the authenticator for a host program that has its own; the stream of changes filtered by the read rule; documents on the wire; the door for agents (MCP); the command-line tool with a check of a class and of an action; the flow of a class in one call; a way in for messages from outside; OpenAPI; `fadodb serve` | ✅ |
| 7 | Browser VM and reference frontend: the log of reads first; TypeScript VM on the same bytecode shipped as one plain JavaScript file, what a class says to people and its sections, the class bundle, `QUERY` when the first page needs a list of rows (otherwise with the reports of Phase 8), signing in with a password or a company's sign-on, the pages of a face handed out by `fadodb serve` from a folder (a face, which draws a document from its class, lives apart from the engine; chip, card, coins and rating are the face's), the door for agents at the server itself, a small client file beside the VM | ✅ |
| 8 | Analytical engine: first what ADR-0078, ADR-0076, ADR-0075 and ADR-0085 leave unbuilt after v0.7.0; then the DuckDB projection of document tables, native and attached bindings, set-wise recompute, mining views, full build per OS | 🚧 |
| 9 | Embedding: C ABI, Python client, embedding guides | ⬜ |
| 10 | Postgres backend | ⬜ |
| 11 | Optional AI layer: authoring assistant (the MCP server moved to Phase 6) | ⬜ |
| 12 | 1.0 hardening: compatibility promises, documentation site, published benchmarks | ⬜ |

Each phase ends with a tagged release and its benchmarks published under
`docs/perf/`. Phases 8 to 11 are independent of each other and may be
reordered to follow demand.

Details of what each phase contains, and the reasoning behind the design, are
in [docs/architecture.md](docs/architecture.md) and [docs/adr/](docs/adr/).

## Not planned for 1.0

Parallel statechart regions, BPMN, multi-tenancy inside one database file,
replication and clustering, WASM plugin actions, a visual class designer,
OCEL export, nested collections, raw SQL inside formulas.
