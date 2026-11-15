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
| [Change a class that already has documents](manual/guides/change-a-class.md) | **written** |

## Reference

| Chapter | What it lists | Mark |
|---|---|---|
| [The class definition](manual/class.md) | every block of a class and every word in it | **written** |
| [The formula language](manual/formula-language.md) | how a formula is written, the operators, what mixes with what, blanks, text, plain and exact numbers, days and moments, objects, what a failure means | **written**; every example is worked out by the engine on each push |
| [The functions](manual/functions.md) | every function with its arguments and what it answers | **written**, generated from the engine's own catalogue |
| The calls | every call of the Go package: what it takes, what it answers, what it may refuse with | planned; today the documentation of the `fado` package |
| [The commands](manual/commands.md) | every call as a command: the JSON it takes and the JSON it answers | **written**, generated from the engine's own table. A command is run by the Go call `Exec`, or sent over HTTP, with a key to a database that `fadodb serve` serves, or to a program that mounts the door, `fado/httpdoor` |
| [The refusals](manual/refusals.md) | every code and what it means | **written**, generated from the engine's own constants; the fix for each comes with the guides |
| [Tables](manual/tables.md) | reference data: the definition and the types of a column, rows and imports, a formula that reads a table, a rule that writes one, who may, changing a table | **written**; a test plays the chapter's story and every table of it against the engine |
| [Schemas and objects](manual/schemas-and-objects.md) | a value with named fields: defining a schema, the nine schemas the engine brings, an object in a class, what a class can and cannot do with an object today, how an object is stored | **written**; a test plays the chapter's story and every table of it against the engine |
| [Files](manual/files.md) | attaching a file to a document: the facts and the bytes, opening a file under the rules of the document, who may, where the bytes are kept, how large a file may be, checking the files | **written**; a test plays the chapter's story and every table of it against the engine |
| [Triggered variables](manual/triggered-variables.md) | a computed variable worked out only at chosen moments: the four triggers, an invoice that takes its rate when it is checked, the moment a value carries, what happens when working it out fails, refreshing and who may, what such a formula may read | **written**; a test plays the chapter's two stories and every table of it against the engine |
| [Actions](actions.md) | defining an action, calling another system, secrets | **written** |
| [Views and SQL](manual/views-and-sql.md) | the views of a class and their columns, how a value looks in SQL, seven questions with their statements and answers, what a statement may do, who may run one | **written**; a test runs every statement of the chapter and compares the answer |

## Embedding and running it

For the developer whose program holds the engine.

| Chapter | What it covers | Mark |
|---|---|---|
| [Opening a database](manual/opening-a-database.md) | a whole small program; what opening does; what is on disk; every option, and whether it belongs to the database or to the program; closing; what is refused | **written**; a test runs the program and plays every table of the chapter |
| [Naming the caller](manual/naming-the-caller.md) | a whole small program with four callers; every part of a caller, what the engine uses it for and what it checks; a call with no caller; why the program is the only door; the system groups; the business day; the options of a call | **written**; a test runs the program and plays every table of the chapter |
| [Serving a database](manual/serving-a-database.md) | from nothing to a server in four commands: `fadodb init`, `fadodb user add`, `fadodb key new`, `fadodb serve`; where it listens and why a key never travels in the clear by accident; the one program that holds a database; the settings, a file of the operator's; the hosts its actions may call, with a worked case; where the secrets are; actions that need a Go function, and the server of your own that runs them; the line it writes for each request; its health; stopping | **written**; tests make a database, serve it, call it with a key and let it call a service with a secret. The server is being built in parts: the stream of changes and the counts are not here yet |
| [The outbox and the worker](manual/the-outbox-and-the-worker.md) | a whole small program with an action that gets no answer and one that is refused; a row of the queue; how the worker runs it; what happens when an action fails; a dead action and sending it again; running the queue by hand; who may | **written**; a test runs the program and plays every table of the chapter |
| [Running it](manual/running-it.md) | a backup and putting it back, with what is gone and what runs twice; what a copy is, and a copy of a database that does not stop; what a program that died leaves; what `fadodb check` finds and does not find; how large the file gets; how fast the engine is, from [docs/perf](perf/README.md) | **written**; a test plays the chapter's story and every table of it against the engine, the tool and the performance pages |
| Versions | what a release may change before 1.0, and what the [changelog](../CHANGELOG.md) promises | planned |

## For agents

An agent works from the five skills under [`skills/`](../skills/README.md),
one for each role it can take: designing a class, working on
documents, trying what would happen, finding out how a process runs,
and keeping its own work as documents. All five are written. A skill
shows one piece of work with the engine's own answers and says what
goes wrong most often; for the syntax it points here.

## About the project

| Page | What it is |
|---|---|
| [Architecture](architecture.md) | how the engine is built |
| [Decision records](adr/README.md) | why each thing is as it is, one decision at a time |
| [Performance](perf/README.md) | the budgets and the measurements of each release |
| [Roadmap](../ROADMAP.md), [changelog](../CHANGELOG.md) | what is done, what is next, what each release changed |
