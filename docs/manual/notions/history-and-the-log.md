# History and the log

FadoDB keeps two records of everything that happens to a document,
and neither is ever changed. The history says what the document looked
like after each change. The log says what happened, who did it and
when. This chapter says what each holds, how they fit together, and
how they are read. It uses the invoice of
[your first application](../first-application.md).

## Two records

| | The history | The log |
|---|---|---|
| Holds | versions: the whole document as each commit left it | events: one for each thing that happened |
| Answers | what was the net amount before it was changed? | who approved this, and when? |
| One entry per | commit that changed the document | commit, and each thing a rule or the engine did because of it |
| Says | every value, the state, from when until when it was current | the kind of event, who, what they were, for whom, when, which transition, from which state to which |

They point at each other. A version names the event that made it, and
an event names the version before it and the version after it.

## What it does

Monday 16 November 2026, in UTC.

| At | Who | Does | Version | Event |
|---|---|---|---|---|
| 09:00 | Krzysztof | creates the invoice, net 1000.00 PLN | 1 | `created` |
| 10:00 | Krzysztof | changes the net amount to 1200.00 PLN; his program sends the key `net-fix-1` with the call | 2 | `set` |
| 10:01 | his program | sends the same call again with the same key, not sure the first arrived | still 2 | none |
| 10:02 | Anna | tries to write a note, and is refused | still 2 | none |
| 11:00 | Krzysztof | approves | 3 | `transition` |
| 12:00 | Anna | gives the account and books, for the business day 31 October | 4 | `transition` |

## The history

<!-- the history -->
| Version | Current from | Until | Made by | `state` | `net` |
|---|---|---|---|---|---|
| 1 | 09:00 | 10:00 | krzysztof | INAPPROVAL | 1000.00 PLN |
| 2 | 10:00 | 11:00 | krzysztof | INAPPROVAL | 1200.00 PLN |
| 3 | 11:00 | 12:00 | krzysztof | INACCOUNTING | 1200.00 PLN |
| 4 | 12:00 | | anna | BOOKED | 1200.00 PLN |

Each row is the whole document, as described in
[Classes and documents](classes-and-documents.md), with the time it
was current from and until. The last one has no "until": it is the
document as it is now.

## The log

<!-- the log -->
| Event | Name | From | To | By | At | Business day | Key |
|---|---|---|---|---|---|---|---|
| `created` | | | INAPPROVAL | krzysztof | 09:00 | 2026-11-16 | |
| `set` | | | | krzysztof | 10:00 | 2026-11-16 | net-fix-1 |
| `transition` | `approve` | INAPPROVAL | INACCOUNTING | krzysztof | 11:00 | 2026-11-16 | |
| `transition` | `book` | INACCOUNTING | BOOKED | anna | 12:00 | 2026-10-31 | |

Four things happened, so there are four events. The call that was
sent twice is there once, and Anna's refused note not at all: **a
refusal writes nothing**.

## What an event carries

<!-- what an event carries -->
| Part | What it is |
|---|---|
| `event_id` | the id of the event; ids sort in the order things happened |
| `class`, `doc_id` | the document it is about |
| `kind` | what happened; the kinds are below |
| `name` | the transition that was taken, the action that ran, or the name of an event of your own |
| `from_state`, `to_state` | for a move, the state left and the state entered; for a create, the first state |
| `actor_id`, `actor_kind`, `actor_for` | who did it, whether that was a person, an agent or a service, and for whom they acted |
| `before_version`, `after_version` | the version of the document before the event and after it |
| `ts` | when, in seconds since 1970, UTC |
| `business_date` | the business day the event belongs to |
| `command_key` | the key the call carried, when it carried one |
| `cause_event_id` | the event that caused this one, for what a rule or a queued action did |
| `origin` | on an event that enters a state, how the move came about: `command`, `action`, `guard` or `timer` |
| `override`, `reason` | for an act past a rule: which lines, and the reason given |
| `code` | on `action_failed`: the code the action was refused with |

## The kinds of event

<!-- the kinds of event -->
| Kind | Written when |
|---|---|
| `created` | a document is created |
| `set` | values are changed |
| `reset` | variables are put back to their defaults by a call |
| `refresh` | variables that are worked out at chosen moments are worked out again by a call |
| `transition` | a transition is taken, by a caller or by the engine |
| `deleted` | a document is deleted |
| `migrated` | a document is moved to a new version of its class on purpose |
| `custom` | a rule emits an event of your own |
| `action_run` | an action the document queued was carried out |
| `action_failed` | an action the document queued was refused, or gave up |

## Sending a call twice

A program or an agent is not always sure that its call arrived. It may
give the call a **key**, any text it chooses, and send the call again
with the same key.

- **The same call under the same key is made once.** The second time
  the caller gets the first answer, and nothing is written. That is
  step 10:01.
- **A different call under a key already used is refused** with
  `key_reused`. A key names one call.
- **A change that changes nothing makes no version.** Setting the net
  amount to the value it already has is answered with the document as
  it is.

## The business day

Every event has two times. `ts` is the moment the engine wrote it, and
nobody chooses it. `business_date` is the day the event belongs to for
the business. It is the day of the clock, or the business day the host
program names for the caller, unless the call says otherwise, as Anna
does when she books on 16 November for October.

## Looking before

A **preview** runs a call through everything a commit does and writes
nothing. It answers with the document as it would be, the states it
would pass through, the transitions that would then be open, the rules
it would set off, and the moment it was worked out for. A call that
would be refused is answered with that refusal.

For Anna in `INACCOUNTING`: a preview of `book` with no account is
answered with `guard_false`, and a preview of `book` with the account
shows the invoice in `BOOKED` at version 4, while the invoice stays
where it is.

## Reading it

| To know | Ask for |
|---|---|
| the document as it is | the document, by class and id |
| the document as one commit left it | one version, by its number |
| everything it ever was | its history |
| everything that happened to it | its events |
| what can happen next | its transitions |
| the same across all documents, for a report | SQL, as a caller in the system group `analysts` |

For SQL the engine keeps views for each class, named after it:
`Invoice` for the documents as they are, `Invoice_history` for the
versions, `Invoice_events` for the log and `Invoice_states` for every
stay of a document in a state. How long did the invoice wait in each
state?

```sql
select state, entered_by, left_by, duration from Invoice_states order by entered_at
```

<!-- the stays -->
| `state` | `entered_by` | `left_by` | `duration`, in seconds |
|---|---|---|---|
| INAPPROVAL | krzysztof | krzysztof | 7200 |
| INACCOUNTING | krzysztof | anna | 3600 |
| BOOKED | anna | | |

A chapter on the views and SQL is planned. A formula can read the same
life of its own document, with `ENTEREDAT()`, `DAYSIN("INAPPROVAL")`
and the others in [The functions](../functions.md).

## Who may read the record

The history and the events of a document are read under the rules of
its class, as the document is now.

- **A caller who may not see the document gets nothing**: an empty
  history, no events, and `unknown_document` for a version.
- **A value a caller may not see is left out of every version**, as it
  is left out of the document.
- **A deleted document keeps its record.** Its history and its events
  are still answered; see
  [Classes and documents](classes-and-documents.md).

## What is never done

- No call changes or removes a version or an event.
- A delete adds an event; it removes nothing from the record.
- A read is not written down.

## What is refused

<!-- what is refused -->
| The caller | Code |
|---|---|
| uses a key again for a different call | `key_reused` |
| asks for a version the document does not have | `unknown_document` |

Next: [Time](time.md).
