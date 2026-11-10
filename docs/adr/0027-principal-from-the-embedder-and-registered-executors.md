# ADR-0027: The principal comes from the embedder; action executors are registered

- Status: Accepted; amends ADR-0009, ADR-0010, ADR-0011; amended by ADR-0044 (how the worker hands a row to an action), ADR-0045 (an action is registered with a definition; `http.call` belongs to the library, behind a switch)
- Date: 2026-11-06

## Context

The architecture planned users, groups, memberships, API keys and sessions
inside the engine, built in Phase 5, with the read predicate of ADR-0010
joining a membership table. The Go API already takes the acting user on
every call, and the formula engine's `Context` in `internal/formula/vm.go`
already takes the user id and the groups from the caller. The membership
table was the only place the engine would decide for itself who belongs
where.

The first embedder keeps its own people, groups, mandates and substitutions,
embeds the library and runs every person's request as that person, never as
the administrator. A membership table inside the engine would duplicate the
embedder's and drift from it. Other embedders have a directory, a session
store or an identity provider; none of them wants a second one.

ADR-0009 lets external code run only through `http.call`. An embedder in the
same process would have to open an HTTP endpoint to receive its own
events.

## Decision

- The Go API takes a `Principal` on every call: `ID` as text, `Groups` as a
  list of text, `Admin` as a bool and `Today` as an optional calendar day,
  the embedder's business day. `USER()` is the id. `INGROUP(g)` tests the
  list. The admin flag is the bypass of ADR-0010. `Today` is the default
  business date of ADR-0026. This generalises the formula `Context` to the
  whole API.
- The read predicate of ADR-0010 compiles the principal's groups as a
  literal list. No membership table is joined.
- FadoDB's own users, groups, memberships, API keys and sessions, the Phase 5
  work, become one optional package that turns a credential into a
  `Principal` for `fadodb serve`. The engine opens and runs with no auth
  tables. The server takes an authenticator the host supplies: a function
  from a request to a `Principal`.
- The architecture examples that reference a `User` class through `ref`
  change to text ids. A `User` class exists only when the embedder defines
  one.
- An embedder registers an executor by name in the Go API. An executor is a
  function that receives the outbox row, which is the event id, the action
  index, the transition event of ADR-0026 and the action's evaluated
  arguments, and returns an error or nil.
- A rule's action may name a registered executor. A class whose action names
  an executor that is not registered is rejected at definition time.
- The outbox worker of ADR-0009 hands the row to the executor, retries with
  backoff, dead-letters after N attempts and records `action_run` or
  `action_failed` events, as for the built-in actions. Executors must be
  idempotent on the key of event id and action index.
- `http.call` is the executor that ships with the server build.

## Consequences

- The engine never knows who exists. It trusts the `Principal` it is given,
  and a wrong one is the embedder's bug. The server build verifies
  credentials through the authenticator, and the optional package is one
  such authenticator.
- Phase 5 shrinks to the optional package, the authenticator interface and
  the `/v1/auth/*`, `/v1/users` and `/v1/groups` routes, which belong to the
  package.
- A compiled read predicate is specific to a class and a group set, so the
  statement cache keys on both. A principal with many groups produces a
  long literal list; the cache bounds the cost per group set, not per
  request.
- The `owner` of the architecture examples becomes a `text` input with the
  default `USER()`. A `ref` to people is still possible where the embedder
  defines a class for them.
- An executor runs in the embedder's process, on the outbox worker. A slow
  executor delays the queue and never the commit. Its failures are reported
  as events, as for `http.call`, and are the embedder's to fix.
- A database defined by one process and opened by another that did not
  register the executor fails each affected row with `action_failed` naming
  the executor, with the usual retries. Definition-time rejection covers
  the process that defines; the event covers the one that does not.
- The embedder owns substitution and delegation. A person acting for another
  is a `Principal` the embedder constructs; the engine records the id it is
  given as the actor.

## Performance

Budget: no change to the commit budget of ADR-0006. The executor call is on
the outbox worker, outside the transaction, and is measured with the outbox
throughput of ADR-0009 in Phase 4. The literal group list stays within the
5% that ADR-0010 allows for predicate injection, measured in Phase 5.
