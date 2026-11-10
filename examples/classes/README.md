# Sample classes

The sample corpus the skills under [`../../skills/`](../../skills/) draw
on. Each file is one class definition in JSON, the same document the Go
API's `DefineClass` takes. `TestExampleClasses` in `fado/` validates and
defines every file on each push, so a sample that no longer holds against
the engine fails CI rather than misleading a reader. The files define in
name order; a sample another one refers to sorts first.

**Status on 2026-11-09:** one sample, the Invoice from the README, as
`invoice.json`.

## Naming

A sample drawn from the use-case catalogue on fadodb.org is named by its
card's id, `agent-inbox.json` for the card at `/use-cases/#agent-inbox`,
so a card can link to its class once the class exists. The first six to
write are the cards the front page links to: `agent-inbox`,
`breach-notification`, `expense-report`, `journal-entry`,
`order-management` and `permit-to-work`. Then one per shape of the
catalogue.

## What a sample may use

A sample uses what validates today. Rules and access rules compile and
are checked, so a sample may declare them, but rules do not fire before
Phase 4 and the class-level access rule is not enforced before Phase 5.
A sample that depends on them says so in a `"description"` the reader
sees, never silently.
