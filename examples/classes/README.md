# Sample classes

The sample corpus the skills under [`../../skills/`](../../skills/) draw
on. Each file is one class definition in JSON, the same document the Go
API's `DefineClass` takes. `TestExampleClasses` in `fado/` validates and
defines every file on each push, so a sample that no longer holds against
the engine fails CI rather than misleading a reader. A sample may name
another, as the Invoice's rule names the Receipt it creates; the test
defines them in an order that works. Samples that name each other, so
that neither could be first, are defined together with `DefineClasses`,
which takes several definitions in one call.

**Status on 2026-11-11:** four samples. The Invoice from the README is
`invoice.json` and the Receipt its rule creates `receipt.json`.
`purchase.json` and `approval.json` are parallel approvals: submitting a
purchase creates one approval for each approver, each approver answers
their own, and the purchase counts them with a `LOOKUP` over the
Approval class and moves by its own guards. The two name each other.
`TestExampleApprovals` walks them through.

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
computed at their triggers. What a rule does beyond its own document
runs through the outbox: `create`, also with `each` for one document
per element of a list, `transition` and `refresh` of another document,
and `table.insert`, `table.update` and `table.delete`. A rule may also
call an action that has a definition, and `http.call`, which sends one
request. A sample cannot bring a definition, a Go function or the host
program's leave to send, so no sample here names a defined action, and
one that names `http.call` says that it runs only where the host
program allows the host. The class-level access rules are enforced, and
a rule a class leaves out lets nobody through, so every sample writes
all four: who may see, create, change and delete its documents. A
sample is seen and changed only by the callers its rules let through.
`INGROUP("everyone")` is every caller that has an id.
A sample that depends on them says so in a `"description"` the reader
sees, never silently.
