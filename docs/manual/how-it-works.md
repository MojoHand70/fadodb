# How it works

Four things carry everything in FadoDB: a class, a document, a commit
and the log. This chapter follows one change from the call to the
answer, and then says what is kept and how it is read. It uses the
invoice of [your first application](first-application.md).

## A class and its documents

A **class** describes a kind of document: Invoice. A **document** is
one of them: the invoice FV/2026/11/017. The class is written once.
Every document of the class has the variables the class names, is in
one of its states, and is changed only in the ways the class allows.

A document also has a few things the engine keeps for it without
being asked: an id, a title, its state, a version number, and who
created it, who last changed it and who moved it into its state, each
with the time.

## A commit

A **commit** is one change to one document: a create, a set of some
values, a transition, a delete. It is made whole or not at all.

Krzysztof approves the invoice. This is what the engine does, in
order:

1. **Who is calling?** The program that embeds the engine says so on
   the call: Krzysztof, a person, in these groups.
2. **May he see the invoice?** The `read` line of the class is asked.
   If it says no, he is told there is no such document, and nothing
   more.
3. **May he do this?** For a transition its line under `access` is
   asked; for a change of values, `update` and then the `rights` of
   each variable he sets. If one says no, the commit is refused with
   `forbidden` and the line that said so.
4. **The values are applied.** For a new document, the defaults of the
   variables he did not give.
5. **The formulas are worked out again**, those that read what
   changed, in the order they depend on each other.
6. **The guard is asked**, for a transition. If it does not hold, the
   commit is refused with `guard_false`. If it holds, the state
   changes.
7. **The rules run** that this change sets off. What they set on the
   document is part of the same commit.
8. **Automatic transitions are followed.** If the document now meets
   the condition of one, it moves again, each move a version of its
   own.
9. **It is written**: the new version of the document, and an event
   in the log. Everything of this commit, or nothing.
10. **What reaches outside is queued** in the same write: a document
    of another class to create, another system to call. The engine
    runs these afterwards, each exactly once.

He is answered with the invoice as it now is. Had any step refused,
nothing would have been written, and the answer would say which step,
where in the class, and often how to fix it.

A **preview** is the same ten steps with the ninth left out. It
answers what the commit would do.

A call may carry a **key**. A commit sent again with the same key is
not made twice: the caller gets the first answer. An agent or a
program that is not sure its call arrived simply sends it again.

## What is kept

Nothing is overwritten.

| Kept | What it is | Example of a question it answers |
|---|---|---|
| the current version | the document as it is now | what is the gross amount? |
| every earlier version | the document as each commit left it | what was the net amount before it was changed? |
| an event for every commit | what happened, who did it, what they were, for whom they acted, when, from which state to which | who approved this, and when? |

The events of all documents, in order, are the **log**. Nothing in it
is ever changed or removed. An act that went past a rule, by a content
administrator, is in it with its reason.

## Reading

| Call | Answers |
|---|---|
| one document by its id | the document, as the caller may see it |
| a filter | the documents of a class that meet a condition, sorted, a page at a time |
| the history of a document | its versions, oldest first |
| the events of a document | its part of the log |
| the transitions of a document | which moves leave its state, and which of them the caller may take now |
| what is due | the documents that a commit would move now, because time passed |
| SQL | anything, over views the engine keeps for every class; for callers in the group `analysts`, since it reads everything |

Who may see is asked on every one of these but the last. A document
its caller may not see is left out of a filter and a count as if it
did not exist, and a variable they may not see is left out of the
document.

A read can be made **as of a moment**: what would this caller see, or
what would this commit do, on the first of next month.

## Time

A transition can carry a time: thirty days after the document entered
the state, or at a date the document holds. When that time comes, the
engine makes the move by itself, as a commit like any other, with its
event in the log.

## Where it lives

A database is one file. A program opens it and calls the engine in its
own process; the Go package is `fado`. There is one writer at a time,
so commits happen one after another and never see each other half
done, while reads go on beside it.

The program that opens the database is the **host program**. It is the
only one that knows who its users are. On every call it names the
caller: an id, groups, whether it is a person, an agent or a service,
and for whom it acts. The engine trusts the host for who the caller is
and decides everything else itself, from the class.

A server that takes the same commands over the network, so that
several programs and agents share one database, comes with Phase 6.

Next: [the class definition](class.md), or the [glossary](glossary.md).
