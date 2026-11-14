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
the story and every table of the chapter against the engine. Until a
planned chapter is written the [glossary](manual/glossary.md) has one
line for its words and the [class definition](manual/class.md) the
syntax.

| Chapter | What it explains | Mark |
|---|---|---|
| [Classes and documents](manual/notions/classes-and-documents.md) | a kind of thing and one thing; what a document carries; ids; versions; deleting; the versions of a class | **written** |
| [Variables and formulas](manual/notions/variables-and-formulas.md) | inputs, defaults, computed values; exact numbers and plain ones; blanks; dates; what happens when a formula fails | **written** |
| [States and transitions](manual/notions/states-and-transitions.md) | the chart of a class; the two questions before every move; values given with a move; final states; what the log keeps | **written** |
| [Rules and actions](manual/notions/rules-and-actions.md) | what happens by itself; what a rule does inside a commit and what the engine runs after it; an action that is refused | **written** |
| [Who may](manual/notions/who-may.md) | the caller; the questions a class answers; who edits in which state; how a right is found; the four system groups; the content administrator | **written** |
| History and the log | versions, events, previews, and reading what happened | planned |
| Time | deadlines, reminders, timed transitions, and choosing a moment to look at | planned |

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
| The formula language | values, operators, blanks, errors, text, dates, exact numbers | planned; today [ADR-0019](adr/0019-formula-language-semantics.md) and the suites under [conformance](../conformance/README.md) |
| [The functions](manual/functions.md) | every function with its arguments and what it answers | **written**, generated from the engine's own catalogue |
| The calls | every call of the Go package: what it takes, what it answers, what it may refuse with | planned; today the documentation of the `fado` package |
| [The refusals](manual/refusals.md) | every code and what it means | **written**, generated from the engine's own constants; the fix for each comes with the guides |
| Tables | reference data: defining, importing, looking up | planned; today [ADR-0037](adr/0037-explicit-tables-and-the-compiled-lookup.md) |
| [Actions](actions.md) | defining an action, calling another system, secrets | **written** |
| Views and SQL | the views the engine makes for each class, and reading them with SQL | planned; today [architecture.md](architecture.md) |

## Embedding and running it

For the developer whose program holds the engine. Planned.

| Chapter | What it covers |
|---|---|
| Opening a database | the file, the options, what belongs to the database and what to the program |
| Naming the caller | the principal on every call; groups; the system groups; a host that keeps its own substitutions |
| The outbox and the worker | what runs after a commit, retries, and what to do with an action that died |
| Running it | backup, the size of the file, what is fast and what is slow, in [docs/perf](perf/README.md) |
| Versions | what a release may change before 1.0, and what the [changelog](../CHANGELOG.md) promises |

## For agents

An agent works from the five skills under [`skills/`](../skills/README.md),
one for each role it can take. A skill says in which order to work; for
the syntax it points here.

## About the project

| Page | What it is |
|---|---|
| [Architecture](architecture.md) | how the engine is built |
| [Decision records](adr/README.md) | why each thing is as it is, one decision at a time |
| [Performance](perf/README.md) | the budgets and the measurements of each release |
| [Roadmap](../ROADMAP.md), [changelog](../CHANGELOG.md) | what is done, what is next, what each release changed |
