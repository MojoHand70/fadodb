# Sample classes

The sample corpus the skills under [`../../skills/`](../../skills/) draw
on. Each file is one class definition in JSON, the same document the Go
API's `DefineClass` takes. `TestExampleClasses` in `fado/` validates and
defines every file on each push, so a sample that no longer holds against
the engine fails CI rather than misleading a reader. A sample may name
another, as the Invoice's rule names the Receipt it creates; the test
defines them in an order that works.

**Status on 2026-11-10:** two samples, the Invoice from the README as
`invoice.json` and the Receipt its rule creates as `receipt.json`.

## Naming

A sample drawn from the use-case catalogue on fadodb.org is named by its
card's id, `agent-inbox.json` for the card at `/use-cases/#agent-inbox`,
so a card can link to its class once the class exists. The first six to
write are the cards the front page links to: `agent-inbox`,
`breach-notification`, `expense-report`, `journal-entry`,
`order-management` and `permit-to-work`. Then one per shape of the
catalogue.

## What a sample may use

A sample uses what validates today. Rules run inside the commit with
`set`, `reset`, `refresh` and `emit`, and triggered variables are
computed at their triggers. A `create` of another document runs through
the outbox; the other outbox actions are checked by name and not queued
until their step of Phase 4, and the class-level access rule is not
enforced before Phase 5.
A sample that depends on them says so in a `"description"` the reader
sees, never silently.
