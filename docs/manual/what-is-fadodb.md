# What FadoDB is

FadoDB is a database for business processes. You describe a kind of
document once, an invoice, a permit, a claim, and the engine keeps
every document of that kind: what it holds, what is computed from it,
where it is in its process, who may do what with it, and everything
that was ever done to it.

## The idea

In most systems one business process lives in four places.

| Where | What it knows |
|---|---|
| tables | the data |
| program code | how to compute, and what is allowed |
| a workflow tool | which step comes next |
| logs | what happened, as far as somebody remembered to write it down |

A change to the process means changing all four and keeping them in
step. A question such as "who approved this, and were they allowed
to?" means reading all four.

In FadoDB the four are one thing: a document that holds its data,
computes itself, knows its state and who may move it, and carries its
own record. The rule and the data it guards cannot drift apart,
because they are the same document.

## What you write

You write a **class**: one JSON document that describes a kind of
document. It says:

- **what each document holds**: its variables, with their types;
- **what is computed**: formulas in the style of a spreadsheet;
- **where a document can be**: its states, and the moves between them;
- **what must hold for a move**: guards;
- **what happens by itself**: rules, such as "when an invoice is paid,
  create a receipt";
- **who may** see, create, change and move a document, down to single
  variables in single states.

A class is data, not program code. A person can read it in a few
minutes, and an agent can write one. [The class definition](class.md)
has every word of it, and [your first application](first-application.md)
builds one from nothing.

## What the engine does

For every document of a class, the engine:

- **checks every change against the class**, and refuses what the
  class does not allow, with a code, the exact place and often the
  fix;
- **computes** every formula when a value it reads changes;
- **keeps every version** of the document, and an event for every
  change: what happened, who did it, when, and for whom;
- **runs the rules**, and what they do to other documents or other
  systems exactly once;
- **moves a document by itself** when its time comes: a reminder after
  seven days, an expiry after thirty;
- **answers questions**: one document, a filter over many, the history
  of one, and SQL over all of it for analysis.

It can also **try a change without making it**: a preview answers what
would happen, and whether it would be refused.

## Who it is for

**The person who designs a process.** The process is written down as a
class that the business can read, and it runs as written.

**The developer.** FadoDB is a library: a program opens a database
file and calls the engine. There is no server to run and nothing to
install beside the program. The core is pure Go with one dependency.

**The agent.** An AI agent is a caller like any other, with its own
name in the log. It works well with FadoDB because nothing depends on
a screen: every step can be previewed, every refusal says what was
wrong and where, and a command sent twice is done once. What an agent
may do is what the class gives it, and what it did is in the log, step
by step and for whom. That is the sense of the project's line:
authority to act, with the evidence built in. The controls are the
class; the evidence is the log.

## What it is not

- **Not a general database.** It stores documents of classes. SQL is
  for reading and analysis, not for writing.
- **Not a workflow tool beside your data.** There is no separate
  process to keep in step: the state is in the document.
- **Not an application with screens.** Today it is the engine. Screens
  are built on it, by you or, later, from layouts the engine serves.
- **Not a user directory.** FadoDB keeps no users and no passwords.
  The program that embeds it says on every call who the caller is and
  which groups they are in.

## Where it stands

FadoDB is before version 1.0. This is what works today, in release
v0.5.0, through the Go package `fado`:

| Works today | Comes later |
|---|---|
| classes, variables, formulas, exact money, dates | a server that takes the same commands over the network |
| states, transitions, guards, automatic moves by time | formulas and forms in the browser |
| rules and actions, calls to other systems, exactly once | an analytical engine for large reports |
| who may: access, rights, system groups, substitutions | the engine's own users, groups and keys |
| versions, events, previews, filters, read-only SQL | a server for agents (MCP) |

The [roadmap](../../ROADMAP.md) has the phases, the
[changelog](../../CHANGELOG.md) what each release changed, and
[the performance pages](../perf/README.md) how fast it is: on the
disk of a laptop a change to a document takes about a tenth of a
millisecond.

Next: [how it works](how-it-works.md).
