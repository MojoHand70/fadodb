# Glossary

The words of FadoDB, one line each. Words in `code` are written
exactly so in a class or a formula.

## The main things

| Word | Meaning |
|---|---|
| class | one kind of document, such as an invoice. Written once, as one JSON document; see [The class definition](class.md) |
| document | one thing of a class: one invoice. It has an id, a state, values and a history |
| variable | one named value that every document of a class holds, such as `amount` |
| input | a variable that a caller sets, or that takes its default |
| computed variable | a variable with a `formula`; the engine works it out and nobody sets it |
| formula | an expression in the style of a spreadsheet, such as `ROUND(net * (1 + vat_rate), 2)` |
| state | where a document is in its process, such as `INAPPROVAL` |
| transition | a named move from one state to another, such as `approve` |
| guard | a condition on the document that must hold for a transition |
| automatic transition | a transition the engine takes by itself, by time or by a condition; also called silent |
| final state | a state in which a document is finished and frozen |
| rule | "when this happens, do that": it runs by itself when a document is created, changed or moved |
| action | one thing a rule does: set a value, create another document, call another system |
| table | a list of reference data, such as exchange rates, that formulas look values up in |

## Who may

| Word | Meaning |
|---|---|
| caller | whoever makes a call: a person, an agent or a service |
| principal | the caller as the engine is told of it: an id, its groups, what it is, and for whom it acts |
| host program | the program that embeds FadoDB. It says who the caller is on every call; FadoDB keeps no users |
| group | a named set of callers, as the host program knows them, such as `ACCOUNTING` |
| system group | one of four groups that hold work which is not about one document: `administrators`, `designers`, `content-administrators`, `analysts` |
| `everyone` | every caller that has an id |
| `access` | the block of a class that says who may see, create, change, delete and move a document |
| `rights` | the block of a class that says who may write or see single variables, state by state |
| editor | not a word of the engine: whoever `access` and `rights` let change a document in its state |
| content administrator | a caller who may act on any document past its `access` and `rights`, with a reason that the log keeps |
| substitution | a document that says one caller may act for another, from when and until when |

## What the engine keeps

| Word | Meaning |
|---|---|
| commit | one change to one document, made whole or not at all: a create, a set, a transition, a delete |
| version | the document as one commit left it. Every version is kept |
| history | all the versions of a document, oldest first |
| event | the record of one commit: what happened, who did it, when, and for a move from which state to which |
| log | all the events, in order. Nothing in it is ever changed |
| preview | a commit tried and not made: what it would do, and whether it would be refused |
| refusal | the engine's answer to a call it does not carry out. It has a code, a place and often a fix |
| outbox | where the actions that reach outside a commit wait until the engine runs them |

## The codes of a refusal you meet first

| Code | Meaning |
|---|---|
| `forbidden` | the caller may not do this. The refusal names the line of `access` or the variable |
| `guard_false` | the document does not meet the guard of the transition |
| `unknown_document` | there is no such document, or the caller may not see it |
| `required` | a required variable has no value |
| `terminal` | the document is in a final state and cannot change |
| `wrong_state` | the transition does not leave the state the document is in |

## Whose word is it

In a class, the words on the left of a colon are FadoDB's: `variables`,
`states`, `transitions`, `rules`, `access`, `rights`, and the words
inside them such as `type`, `from`, `guard`, `read`, `write`. So are
the functions, which are written in capitals with brackets, such as
`USER()`, and `TRUE`, `FALSE` and `*`.

The names are yours: of the class, of each variable, state and
transition, and of the users and groups, which come from your own
program.
