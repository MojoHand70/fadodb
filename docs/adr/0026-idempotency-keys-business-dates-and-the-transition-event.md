# ADR-0026: Idempotency keys on commands, a business date on events, and the transition event

- Status: Accepted; amends ADR-0006, ADR-0011, ADR-0014
- Date: 2026-11-06

## Context

ADR-0009 gave every outbox row an idempotency key so that an action runs
once however often the worker retries. Commands have no such key. A caller
whose request times out cannot know whether its command committed, and a
retry may create a second document or move one twice. An embedder that
drives FadoDB from its own rules, such as one that creates a document for
every invoice it receives, needs twice to be once.

Events carry `ts`, the system time of the commit. An embedder that keeps
books needs the day of the fact, which chooses the accounting period, and
that day is not the time of the commit: a document entered on Monday may
describe Friday. The engine has no business to interpret that day, but it
has to carry it.

ADR-0009 says an action can call a URL on a transition and ADR-0006 says
the transition is an event; neither says what the delivered event contains.
An embedder that stores the event as a self-contained fact needs the
document at that version, who moved it, on what day, and what caused it,
without a second request to the engine.

## Decision

- Every mutating command, which is `create`, `set`, `transition`, `refresh`,
  `reset` and `delete`, takes an optional `key`: text up to 128 characters,
  chosen by the caller. The key is stored on the event row as `command_key`
  with a unique index per database.
- A repeat of a command with a key already stored makes no commit. It
  returns the first commit's result: the document at that version and the
  event id. A key reused with a different command body is the error
  `key_reused`.
- Keys are kept as long as the events are kept, and a retention archive
  carries them. The outbox keys of ADR-0009 stay as they are.
- Every event carries `business_date`, a calendar day of the `date` type of
  ADR-0023. A command may give it. The default is the principal's `Today`
  that the embedder supplies under ADR-0027, and UTC today when there is
  none.
- The engine gives `business_date` no semantics beyond storage, an index and
  exposure. It appears on the `_events` and `_states` views, in the mining
  views and on the transition event. `valid_from` and `valid_to` stay system
  time. FadoDB is not a bitemporal store, and an as-of read is by system
  time.
- The transition event, as delivered to an action, which is `http.call` or
  a registered executor under ADR-0027, and on the change stream, carries:
  the event id, the class name and class version, the document id, the from
  state, the to state, the version after the transition, the document in the
  wire shape of ADR-0018 at that version, the actor as the principal id, the
  business date, the command key of the command that caused it when there
  was one, and the timestamp.
- The JSON schema of the transition event is published under `api/` and
  covered by the conformance suite.

## Consequences

- Clients get one retry rule: send again with the same key until a result
  comes back. A retried transition on a document that has moved on since
  still returns the original result, because the key names the first
  commit, not the document's present state.
- Keys are unique per database, not per class or per document. A caller
  that mixes sources chooses a prefix.
- Detecting `key_reused` needs the command body or a hash of it beside the
  key. One more column on the event row; the body itself is not stored.
- A retention archive moves keys out of the live index. A repeat of a key
  older than the retention threshold commits again, so the threshold must
  exceed the longest retry window of any caller. The retention command
  documents this.
- `business_date` is a column the engine never reads. An embedder that needs
  an as-of read by business day filters the `_history` view on it; the
  engine promises nothing about gaps or order in that column.
- The transition event is a public contract from the release that ships it.
  A change to its fields is a new record, as for the wire shape.
- The wire shape of ADR-0018 is embedded in the event unchanged, so an
  embedder that already decodes documents decodes events with the same code.

## Performance

Budget: one text column and one unique index on `events` for the key, one
`date` column and one index for the business date. A keyed commit does one
lookup on the unique index before it computes anything, and the whole stays
within the 2 ms commit budget of ADR-0006. The Phase 2 commit benchmark
runs keyed and unkeyed commits side by side.
