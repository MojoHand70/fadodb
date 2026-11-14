# Classes and documents

Everything in FadoDB is one of two things: a class, or a document of a
class. This chapter says what each is, what a document carries, and
what is kept when it changes. It uses the invoice of
[your first application](../first-application.md).

## A kind, and one of the kind

| | A class | A document |
|---|---|---|
| What it is | a kind of thing: Invoice | one thing of that kind: the invoice FV/2026/11/017 |
| How many | one per kind | as many as there are invoices |
| Who writes it | a designer, once | whoever the class lets create one |
| What it says | what every invoice holds, how it moves, who may | the values of this invoice, where it is, what happened to it |

Think of a printed form and the rules of the office that handles it.
The class is the blank form together with those rules. A document is
one form, filled in, with its stamps.

A class decides six things for all its documents, one block each: what
they hold (`variables`), where they can be (`states`), how they move
(`transitions`), what happens by itself (`rules`), who may do what
(`access`) and who may touch which value (`rights`).
[The class definition](../class.md) lists every word of each block.

A document belongs to its class for good. An invoice does not become
an order.

## What a document carries

Krzysztof creates the invoice on 16 November 2026 at 09:00 UTC, giving
its number, its supplier and its net amount. This is the document as
the engine answers it:

<!-- the document -->
```json
{
  "class": "Invoice",
  "id": "01M7HSBBM00Z32VP3R0647SJHY",
  "title": "Invoice FV/2026/11/017",
  "state": "INAPPROVAL",
  "version": 1,
  "class_version": 1,
  "created_at": 1794819600,
  "updated_at": 1794819600,
  "state_entered_at": 1794819600,
  "created_by": "krzysztof",
  "updated_by": "krzysztof",
  "state_entered_by": "krzysztof",
  "values": {
    "account": null,
    "gross": "1230.00 PLN",
    "net": "1000.00 PLN",
    "note": null,
    "number": "FV/2026/11/017",
    "supplier": "ACME",
    "vat_rate": "0.23"
  }
}
```

He gave three values. Everything else the engine filled in.

<!-- what a document carries -->
| Part | What it is | Who sets it |
|---|---|---|
| `class` | the class of the document | the caller, by creating it there |
| `id` | what the document is found by, within its class | the engine, or the caller; see below |
| `title` | a name for people, from the `title` formula of the class; the id when the class has none | the engine |
| `state` | where the document is | the engine, by a transition |
| `version` | how many commits made the document what it is | the engine |
| `class_version` | which version of the class worked this version out | the engine |
| `created_at`, `created_by` | when it was created, and by whom | the engine |
| `updated_at`, `updated_by` | when this version was made, and by whom | the engine |
| `state_entered_at`, `state_entered_by` | when it entered the state it is in, and who moved it there | the engine |
| `state_entered_from` | the state it was in before; absent while it is in the state it was created in | the engine |
| `values` | the variables of the class: the inputs as they were set, the computed ones as the formulas gave them | the caller for inputs, the engine for the rest |
| `computed` | for a variable worked out only at chosen moments: when that was. Absent in a class that has none | the engine |

Three things to know when reading one:

- **Times are seconds since 1970, in UTC.** 1794819600 is 16 November
  2026, 09:00:00.
- **Every variable is there.** One with no value is `null`, which the
  manual calls blank. Here `account` and `note` are blank, `vat_rate`
  came from its default and `gross` from its formula.
- **A variable the caller may not see is not there at all**, as if the
  class did not have it.

## The id

A document is found by its class and its id.

- **The engine makes one** when the caller gives none: 26 letters and
  digits, made so that documents sort in the order they were created.
- **The caller may give its own**, such as the number a document has in
  another system.
- **An id is never used twice in a class**, not even after the document
  was deleted. A second document under the same id is refused with
  `duplicate`. Two classes may each have a document under the same id.

## Nothing is overwritten

Every commit makes the next version of the document and keeps the one
before.

<!-- the versions -->
| Version | What happened | `net` | `gross` | `state` |
|---|---|---|---|---|
| 1 | Krzysztof creates it | 1000.00 PLN | 1230.00 PLN | INAPPROVAL |
| 2 | Krzysztof changes the net amount | 1200.00 PLN | 1476.00 PLN | INAPPROVAL |
| 3 | Krzysztof approves it | 1200.00 PLN | 1476.00 PLN | INACCOUNTING |

The history of the document answers all three, each with the time it
was current from and until, and who made it. One version can be read
by its number: version 1 still says 1000.00 PLN.

A commit that is refused makes no version. When Anna, who may not
write in `INAPPROVAL`, tries to change the net amount, the invoice
stays at the version it had.

## Deleting

The invoice cannot be deleted at all: its class says `"delete":
"FALSE"`, and a caller who tries is refused with `forbidden`. A class
that allows it looks like this:

```json
{
  "name": "Note",
  "variables": {
    "text": {"type": "text"}
  },
  "states": {"initial": "open", "list": ["open", "done"], "final": ["done"]},
  "transitions": [
    {"name": "finish", "from": "open", "to": "done"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "INGROUP(\"everyone\")",
    "delete": "INGROUP(\"everyone\")"
  }
}
```

Krzysztof writes a note, changes its text and deletes it.

<!-- after a delete -->
| Asked after the delete | Answer |
|---|---|
| the note itself | `unknown_document`: it is gone |
| its history | both versions, as they were |
| its events | three: `created`, `set`, `deleted`, each with who and when |
| a new note under the same id | refused, `duplicate` |

So a delete takes the document away and leaves the record of it. A
note that is `done` cannot be deleted: `done` is final, and the answer
is `terminal`.

## A class has versions too

Defining a class again under the same name makes its next version,
when something in it changed. Defined again with nothing changed, it
stays the version it was.

- A new version touches no document. Each document says under
  `class_version` which version made it, and takes the new one at its
  next change.
- Every version of a class is kept, with who defined it and when.
- A document in a final state keeps its version of the class for ever.

The guide [Change a class that already has
documents](../guides/change-a-class.md) plays this through.

## The classes of a database

A database holds as many classes as the application needs, each under
its own name.

- **One class is the engine's.** Every database has `Substitution`,
  which records who may act for whom; see [Let somebody act for a
  person](../guides/acting-for-somebody.md). A class of your own cannot
  take that name: `reserved_name`.
- **A class without a `states` block stores no documents.** Creating
  one is refused with `stateless`. Such a class is a calculator: it is
  given values and answers with what its formulas make of them.
- **There is no call that removes a class** today.

## What is refused

<!-- what is refused -->
| The caller | Code |
|---|---|
| names a class the database does not have | `unknown_class` |
| sets a variable the class does not have | `unknown_variable` |
| creates a document under an id the class has used | `duplicate` |
| asks for a document that is not there, or that they may not see | `unknown_document` |
| deletes where the class does not let them | `forbidden` |
| changes or deletes a document in a final state | `terminal` |
| creates a document of a class without states | `stateless` |

The whole list is in [The refusals](../refusals.md).

Next: [Variables and formulas](variables-and-formulas.md).
