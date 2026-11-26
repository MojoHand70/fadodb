# The FadoDB manual

This is the manual of FadoDB, for the person who designs a process, the
developer who embeds the engine, and the agent that works with either.
It is being written chapter by chapter. Each chapter below carries a
mark: **written**, or **planned**. A planned chapter names where its
facts are today.

Every example in a written chapter is run by a test on each push, so a
chapter cannot show a class the engine refuses or an answer it does not
give.

## Start here

| Chapter | For | Mark |
|---|---|---|
| [What FadoDB is](manual/what-is-fadodb.md) | everybody: what it is for, what it is not, how it differs from a database and from a workflow tool | **written** |
| [How it works](manual/how-it-works.md) | everybody: a class, a document, a commit and the log, followed once from end to end | **written** |
| [Your first application](manual/first-application.md) | everybody: an invoice that one person approves and accounting books, from an empty file to a working process | **written** |
| [Glossary](manual/glossary.md) | everybody: every word, and whether it is FadoDB's or yours | **written** |

## The notions

One chapter each, in the order a designer meets them. Each explains
the idea with one class and a story of named people, and a test plays
the story and every table of the chapter against the engine. The
[glossary](manual/glossary.md) has one line for each word, and the
[class definition](manual/class.md) the syntax.

| Chapter | What it explains | Mark |
|---|---|---|
| [Classes and documents](manual/notions/classes-and-documents.md) | a kind of thing and one thing; what a document carries; ids; versions; deleting; the versions of a class | **written** |
| [Variables and formulas](manual/notions/variables-and-formulas.md) | inputs, defaults, computed values; exact numbers and plain ones; blanks; dates; what happens when a formula fails | **written** |
| [States and transitions](manual/notions/states-and-transitions.md) | the chart of a class; the two questions before every move; values given with a move; final states; what the log keeps | **written** |
| [Rules and actions](manual/notions/rules-and-actions.md) | what happens by itself; what a rule does inside a commit and what the engine runs after it; an action that is refused | **written** |
| [Who may](manual/notions/who-may.md) | the caller; the questions a class answers; who edits in which state; how a right is found; the four system groups; the content administrator | **written** |
| [History and the log](manual/notions/history-and-the-log.md) | versions and events and how they point at each other; what an event carries; sending a call twice; the business day; previews; reading the record, and who may | **written** |
| [Time](manual/notions/time.md) | how a class speaks of time; a transition with a time; who makes the move; a timer that was missed; looking at another day | **written** |

## Guides: how do I

Short pages, one task each. Each shows a class, a table of what it
does, and why; a test defines the class and plays the table against
the engine on each push.

| Guide | Mark |
|---|---|
| [Give each state its own editors](manual/guides/editors-per-state.md) | **written** |
| [Make a field read-only after approval](manual/guides/read-only-after-approval.md) | **written** |
| [Four eyes: the one who filed it may not approve it](manual/guides/four-eyes.md) | **written** |
| [A reminder every seven days, and a deadline](manual/guides/reminders-and-deadlines.md) | **written** |
| [Let somebody act for a person](manual/guides/acting-for-somebody.md) | **written** |
| Ask several people and go on when all have agreed | planned. It needs a document to move by itself as soon as a condition holds; the owner ruled that such a transition carries a name and a mark, which is not built yet. Today [examples/classes](../examples/classes/README.md), the Purchase and Approval samples, show it in the form the engine has now |
| [Do something outside when a document changes](manual/guides/act-outside.md) | **written** |
| [Look a value up in a reference table](manual/guides/look-a-value-up.md) | **written** |
| [Move many documents at once](manual/guides/move-many-documents.md) | **written** |
| [Act only on the version you read](manual/guides/act-on-what-you-read.md) | **written** |
| [Say who may bring a value again](manual/guides/who-may-bring-a-value-again.md) | **written** |
| [Bring what a condition needs](manual/guides/bring-what-a-condition-needs.md) | **written** |
| [Keep the lines a decision stood on](manual/guides/keep-the-lines-a-decision-stood-on.md) | **written** |
| [See where documents wait](manual/guides/see-where-documents-wait.md) | **written** |
| [Change a class that already has documents](manual/guides/change-a-class.md) | **written** |

## Reference

| Chapter | What it lists | Mark |
|---|---|---|
| [The class definition](manual/class.md) | every block of a class and every word in it; checking a class in a file with `fadodb class check` before it reaches a database | **written**; a test holds the check to the tool |
| [The formula language](manual/formula-language.md) | how a formula is written, the operators, what mixes with what, blanks, text, plain and exact numbers, days and moments, objects, what a failure means | **written**; every example is worked out by the engine on each push |
| [The functions](manual/functions.md) | every function with its arguments and what it answers | **written**, generated from the engine's own catalogue |
| The calls | every call of the Go package: what it takes, what it answers, what it may refuse with | planned; today the documentation of the `fado` package |
| [The commands](manual/commands.md) | every call as a command: the JSON it takes and the JSON it answers | **written**, generated from the engine's own table. A command is run by the Go call `Exec`, or sent over HTTP, with a key to a database that `fadodb serve` serves, or to a program that mounts the door, `fado/httpdoor`; from a terminal, `fadodb exec` sends it. One command, `subscribe`, is answered with a stream. The same list for a program in another language is [api/openapi.json](../api/openapi.json) |
| [The refusals](manual/refusals.md) | every code and what it means | **written**, generated from the engine's own constants; the fix for each comes with the guides |
| [Tables](manual/tables.md) | reference data: the definition and the types of a column, rows and imports, a formula that reads a table, a rule that writes one, who may, changing a table | **written**; a test plays the chapter's story and every table of it against the engine |
| [System tables](manual/system-tables.md) | the app System and its four tables, the apps, the classes, the users and the groups of the database: their columns, reading them in a formula and a list, what is refused | **written**; a test plays the chapter's tables, its story and what it refuses against the engine |
| [Apps](manual/apps.md) | a name for the classes and tables that belong together: defining an app, a class and a table that say which app they belong to, reading what there is, what an app does not do yet | **written**; a test defines the chapter's app, its class and its table against the engine |
| [Schemas and objects](manual/schemas-and-objects.md) | a value with named fields: defining a schema, the nine schemas the engine brings, an object in a class, what a class can and cannot do with an object today, how an object is stored | **written**; a test plays the chapter's story and every table of it against the engine |
| [Files](manual/files.md) | attaching a file to a document: the facts and the bytes, opening a file under the rules of the document, who may, where the bytes are kept, how large a file may be, checking the files | **written**; a test plays the chapter's story and every table of it against the engine |
| [Triggered variables](manual/triggered-variables.md) | a computed variable worked out only at chosen moments: the triggers, an invoice that takes its rate when it is checked, the moment a value carries, what happens when working it out fails, refreshing and who may, an estimate whose rate follows the table of rates until it is sent, a bill whose days late are worked out every night at three with the words of a schedule, what such a formula may read | **written**; a test plays the chapter's stories and every table of it against the engine |
| [Live variables](manual/live-variables.md) | a computed variable worked out when it is read, for the reader, and kept nowhere: the days an invoice has waited, whether the reader wrote it, the command that reads one, who may, what such a variable may not say and what may not read it, what is not built yet | **written**; a test plays the chapter's story and every table of it against the engine |
| [Lists of other documents](manual/lists.md) | a live variable whose formula is a `QUERY`: the invoices of an order, the four parts of a `QUERY`, what its condition reads, who gets which rows, the page of a list, figures over a list (`COUNT`, `SUM`, `MIN`, `MAX`, `AVERAGE`, `MEDIAN`, `COUNTUNIQUE`, with a condition, `ANY`, `ALL`), the same figures over the lines and tags a document holds (`SUM(lines.qty * lines.price)`), one value out of a list (`FIRST`, `LAST`, `INDEX`, `JOIN`, `CONTAINS`), a list narrowed, ordered and cut inside a function (`FILTER`, `SORT`, `TOP`, `UNIQUE`), what is refused, what is not built yet | **written**; a test plays the chapter's story, its command and every table of it against the engine |
| [Actions](actions.md) | defining an action, calling another system, secrets | **written** |
| [Views and SQL](manual/views-and-sql.md) | the views of a class and their columns, how a value looks in SQL, seven questions with their statements and answers, what a statement may do, who may run one | **written**; a test runs every statement of the chapter and compares the answer |

## Embedding and running it

For the developer whose program holds the engine.

| Chapter | What it covers | Mark |
|---|---|---|
| [Opening a database](manual/opening-a-database.md) | a whole small program; what opening does; what is on disk; every option, and whether it belongs to the database or to the program; closing; what is refused | **written**; a test runs the program and plays every table of the chapter |
| [Naming the caller](manual/naming-the-caller.md) | a whole small program with four callers; every part of a caller, what the engine uses it for and what it checks; a call with no caller; why the program is the only door; the system groups; the business day; the options of a call | **written**; a test runs the program and plays every table of the chapter |
| [Serving a database](manual/serving-a-database.md) | from nothing to a server in four commands: `fadodb init`, `fadodb user add`, `fadodb key new`, `fadodb serve`; where it listens and why a key never travels in the clear by accident; the one program that holds a database; the settings, a file of the operator's; the hosts its actions may call, with a worked case; where the secrets are; actions that need a Go function, and the server of your own that runs them; the line it writes for each request; its health; its counts for whoever watches it; stopping | **written**; tests make a database, serve it, call it with a key, let it call a service with a secret and read its counts |
| [In a container](manual/in-a-container.md) | the two images, of `fadodb` with the analytical engine and of `fadodb-core`, which holds the tool and nothing else; from nothing to a server in five commands of Docker; the image as a caller and as the door for agents; a compose file that keeps a served database running; reaching it from other machines; the settings and the secrets; a folder of the machine in place of a volume; the one container that holds a database; stopping; the health check | **written**; a test plays the commands of the chapter against the image, on GitHub with every push. The image is pulled from `ghcr.io/mojohand70/fadodb` under the version of a release |
| [Signing in](manual/signing-in.md) | people at `fadodb serve`: the first passkey through an invitation the machine makes; an invitation, and signing in with a passkey; a person's passkeys, and who removes them; a password, until passwords go, and the second step an administrator or a designer sets up; the account page; how long a session holds; a command sent from a page of the server with its session; who may make an invitation or a link, list and remove passkeys, list and end sessions and read the log of sign-ins; a company's sign-on, Microsoft, Google or another provider of OpenID Connect, and how a first sign-on finds the person an administrator added; connecting a client by OAuth: Claude, known by its document, as an agent acting for the person, and a face an administrator adds; the settings `url`, `sessions`, `grants` and `signon`; what is refused | **written**; tests play the chapter against a server, a provider and a client's document they run, and a device that makes and uses passkeys in a browser's place: the invitation, the passkey and its refusals, a person's passkeys, the link, the second step and its codes, a session's command, a company sign-on, a client allowed, renewed and ended, and every refusal of the tables. Not built: the door for agents at `/mcp`, which takes Claude's pass |
| [A way in for messages](manual/a-way-in-for-messages.md) | an inlet, for a system that speaks only its own language: a definition that says how a message proves who sent it and which one command it becomes; what its formulas read of a message; the three forms of a proof; from the operator's setting to the bank's notice, step by step; why a message sent twice is one commit; what a sender is answered, and what it is never told; the lines and the counts of the server; who may check, try, define and remove an inlet; what a server takes on | **written**; a test plays every step against a server: the check from the command line, the signed notice, each answer of the table, the lines and the counts |
| [Commands from a terminal](manual/commands-from-a-terminal.md) | `fadodb exec` and `fadodb file`, for a person, a script and an agent with a shell: a command sent to a server with the key of a user; how the tool is told the server and the key, and why the key is never an argument; a class defined from the file it was checked in; a command, its answer and a refusal; the object from a file or from standard input; an export as an Excel file, written in no locale, and as CSV for a program; the stream, an event to a line; a file put on a document and read back; the help of every command; how the tool ends | **written**; a test types every line of the chapter against a server, compares what the tool prints and opens the Excel file it writes |
| [The door for agents](manual/the-door-for-agents.md) | `fadodb mcp`, for an agent's program that speaks the Model Context Protocol: each command as a tool, sent to a server with the key of a user; setting an agent up with a user and a key of its own; the same door at the server itself, at `/mcp`, with a pass a person gives or a key, and what it refuses; a tool as the door lists it, and the marks that say whether it reads, writes or removes; what an agent reads, a refusal and no answer; keeping tools from an agent; waiting for a change, with `wait_for_changes`; files from a folder, with `put_file` and `get_file`; the three commands that are no tool; the door tried by hand; the revisions of the protocol it speaks | **written**; a test makes every call of the chapter through the door against a server and compares what the agent reads, plays every refusal of the door at the server, has an agent wait while an invoice is submitted, and puts a scan from one folder and writes it into another, refusing every way out of a folder |
| [The stream of changes](manual/the-stream-of-changes.md) | being told of every event one may read of, without asking again and again: a whole small program with an agent that waits for invoices; what a stream is asked for and what an event says; who hears of what; the stream over HTTP, `POST /v1/subscribe`; coming back after a break without losing an event; how a stream ends; what is refused; what a server takes on; what it costs | **written**; a test runs the program, and another plays every table and every reply of the chapter against a server |
| [The bundle](manual/the-bundle.md) | for the developer of a page: a class in the person's words and its formulas compiled, in one call; asking for it, and the browser's cache; what it answers; whose words; the formulas and what each is given; what a page greys out, and why the server still decides; what only the server works out; what it never carries; what is refused | **written**; a test plays every table of the chapter against the engine, and the browser VM works out every answer of two stories from the bundle alone, each the server's |
| [The outbox and the worker](manual/the-outbox-and-the-worker.md) | a whole small program with an action that gets no answer and one that is refused; a row of the queue; how the worker runs it; what happens when an action fails; a dead action and sending it again; running the queue by hand; who may | **written**; a test runs the program and plays every table of the chapter |
| [The log of reads](manual/the-log-of-reads.md) | what a content administrator sees past a rule and every SQL statement: the reason asked, an entry for each call, what is and is not recorded, no trace and no answer, the command `reads` and who may send it, what a face asks of its person, what is refused | **written**; a test plays the chapter's story and every table of it against the engine |
| [Running it](manual/running-it.md) | a backup and putting it back, with what is gone and what runs twice; what a copy is, and a copy of a database that does not stop; what a program that died leaves; what `fadodb check` finds and does not find; how large the file gets; how fast the engine is, from [docs/perf](perf/README.md) | **written**; a test plays the chapter's story and every table of it against the engine, the tool and the performance pages |
| Versions | what a release may change before 1.0, and what the [changelog](../CHANGELOG.md) promises | planned |

## For agents

An agent works from the six skills under [`skills/`](../skills/README.md),
one for each role it can take: designing a class, working on
documents, trying what would happen, finding out how a process runs,
keeping its own work as documents, and building what people see of a
server. All six are written. A skill
shows one piece of work with the engine's own answers and says what
goes wrong most often; for the syntax it points here.

## About the project

| Page | What it is |
|---|---|
| [Architecture](architecture.md) | how the engine is built |
| [Decision records](adr/README.md) | why each thing is as it is, one decision at a time |
| [Performance](perf/README.md) | the budgets and the measurements of each release |
| [Roadmap](../ROADMAP.md), [changelog](../CHANGELOG.md) | what is done, what is next, what each release changed |
