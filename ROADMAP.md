# Roadmap

**Current status: Phase 3, the statechart, done on 2026-11-09 and released as v0.3.0. Phase 4, rules and actions, started on 2026-11-09.**
The Go API (`fado`) opens a database file, defines schemas, classes and
explicit tables, and creates, changes, reads and deletes documents as
commits: assignments and defaults, the auto recalculation, typed history
rows, child rows for composites, an event log with idempotency keys and
business dates, attachments behind a blob store, `LOOKUP` against tables,
a filter over a class and read-only SQL over the views, with previews that
write nothing. `fadodb formula eval` evaluates a formula and `fadodb
check` verifies a file. The rules of a class run inside the commit with
the actions `set`, `reset`, `refresh` and `emit`, and triggered
variables are computed at their triggers; what a rule does to other
documents and to tables runs through the outbox; and a rule can call an
action that has a definition, implemented by a Go function of the host
program or sent by the engine as an HTTP request to the hosts the host
program allows, with its result written back to the document. Formulas
that read other documents, time, the row-level access rules and the
server come with their steps and phases.
Since v0.1.0 the records ADR-0022 to
ADR-0038 fixed what the first embedder needs and what Phase 2 measured:
SQLite as the transactional store and the in-memory index for lookups, by
measurement (ADR-0035, ADR-0036), the compiled `LOOKUP` and explicit
tables (ADR-0037), the filter and the SQL endpoint (ADR-0038), and class
versions with terminal states and migrations (ADR-0039). Phase 3 opened
with its design in three records, accepted on 2026-11-09: ADR-0040,
transitions as commits, with eventless chains, snapshots, the allowed
transitions and the `_states` view; ADR-0041, field rights and
per-transition rules enforced in the Go API; and ADR-0042, a moment the
caller chooses for previews and reads. It closed on 2026-11-09 with the
statechart running in the Go API: `STATE()` and `ID()` (#40), the
transition command with guards, eventless chains as further commits, the
listing of open transitions, the preview of a transition and the
`_states` view (#42), the rights table and the per-transition rules
enforced on every path for the principal (#43), `At(t)` for previews and
reads (#44), and the measurements of `docs/perf/v0.3.0.md` against every
budget of the three records (#45). Phase 4 opened with its design in
three records, accepted on 2026-11-10: ADR-0043, rules inside the
commit, with the synchronous actions and triggered variables; ADR-0044,
the outbox, its worker and the engine's own actions, which run as the
person whose command queued them; and ADR-0045, actions as an
extension point, with declared HTTP actions, Go actions an embedder
registers, connections and results written back to the document.
Formulas that read other documents got their record, ADR-0046,
accepted on 2026-11-10; conditions on time and timers, which the owner
ruled into the phase that day, get theirs before that step. Its first step runs the rules
inside the commit: the triggers of each commit, the synchronous actions
`set`, `reset` and `emit`, and the rules a preview reports (#51). Its
second computes the triggered variables at their triggers, with the
`refresh` action and the moment of each value, under engine schema 3
(#52). Its third makes `Reset` and `Refresh` commands of the Go API
(#53). Its fourth is the outbox, in two parts: first the rows a commit
writes, the worker and `Drain`, retries and dead actions, their events,
the listing and the retry by hand, with `create` as the first action and
the test that stops the process at every step (#54); then the rest of
the engine's own actions, `transition` and `refresh` of another
document, `each` on a `create` and the `table.*` actions, and the
refusal of a redefinition that would break another class's rule (#56).
Its fifth opens the engine to actions others write, in three parts, of
which the first is built: action definitions with typed parameters,
stored with `DefineAction`; Go functions the host program gives to
`Open`, called by the outbox with the arguments and the event that
fired the rule; the catalogue, the preview and the trial of an action;
and the published schemas under `api/` (#57). The second, built too,
sends an action as an HTTP request: a definition with a `request`,
`http.call`, secrets the host supplies at the moment of sending and that
go only to the hosts of their connection, the signed call for a
developer's own service, and `fado/outbound`, without which a database
sends nothing (#58). The third, built too, writes an action's result
back to the document with `into`, so that a process goes on from the
answer, and adds one generic webhook definition and a guide to writing
an action for any REST API. By the owner's ruling of 2026-11-10 the
phase writes no definitions for particular services, which come with
real needs, and the package format waits for the first of them. Its
sixth step, formulas that read other documents, has its design in
ADR-0046, accepted on 2026-11-10, and is not built yet: a class as the
source of `LOOKUP`, with a count, a sum, the smallest and the largest.
By the owner's rulings of that day a formula sees every document, and
`QUERY`, which returns lists, is built with the first layout or report
that needs one; tables stored outside the file come with the analytical
engine in Phase 8. This page is updated when a phase starts and when it ends;
it never describes a phase as done before its acceptance criteria pass.

Legend: ✅ done · 🚧 in progress · ⬜ not started

| Phase | Goal | Status |
|---|---|---|
| 0 | Foundations: module, gates, CI, release pipeline, ADRs, docs | ✅ |
| 1 | Formula engine: lexer, parser, binder, bytecode, Go VM, SQL generator with SQLite and DuckDB dialects, conformance suite | ✅ |
| 2 | Document model and store: primary-store benchmark, variables with defaults, `json` type, enum values, range constraints, named schemas and object values with the predefined vocabulary, attachments as `File` values with a blob store, class metadata and title, current, history and child tables, commits with idempotency keys and business dates, preview and validate without writing, event log, migrations, LOOKUP, queries | ✅ |
| 3 | Statechart: the transition command with guards, eventless chains, exit and entry snapshots and the `_states` view, allowed transitions and the preview of a transition, `STATE()` and `ID()`, per-transition access rules and field-level rights per state, statechart conformance cases | ✅ |
| 4 | Rules and actions: event-condition-action rules run inside the commit, the synchronous actions `set`, `reset`, `refresh` and `emit`, triggered variables with their moment, the transactional outbox with its worker, retries and dead rows, the engine's actions on other documents and tables, actions as an extension point with definitions, declared HTTP actions, Go actions the embedder registers, connections and results written back, the delivered event, formulas that read other documents (`LOOKUP` over a class, with a count, a sum, the smallest and the largest), conditions on time and timers | 🚧 |
| 5 | Security: the principal from the embedder on every call, access rules compiled to SQL predicates, an optional package of users, groups and API keys for the server | ⬜ |
| 6 | Server and CLI: JSON command set over HTTP and WebSocket, documents on the wire, layouts, `QUERY` when the first layout needs a list of rows (otherwise with the reports of Phase 8), OpenAPI, `fadodb serve` | ⬜ |
| 7 | Browser VM and reference frontend: TypeScript VM on the same bytecode shipped as one plain JavaScript file, class bundle, layout renderer with the chip, card, coins and rating components, TypeScript client | ⬜ |
| 8 | Analytical engine: DuckDB projection of document tables, native and attached bindings, set-wise recompute, mining views, full build per OS | ⬜ |
| 9 | Embedding: C ABI, Python client, embedding guides | ⬜ |
| 10 | Postgres backend | ⬜ |
| 11 | Optional AI layer: MCP server, authoring assistant | ⬜ |
| 12 | 1.0 hardening: compatibility promises, documentation site, published benchmarks | ⬜ |

Each phase ends with a tagged release and its benchmarks published under
`docs/perf/`. Phases 8 to 11 are independent of each other and may be
reordered to follow demand.

Details of what each phase contains, and the reasoning behind the design, are
in [docs/architecture.md](docs/architecture.md) and [docs/adr/](docs/adr/).

## Not planned for 1.0

Parallel statechart regions, BPMN, multi-tenancy inside one database file,
replication and clustering, WASM plugin actions, a visual class designer,
OCEL export, nested collections, raw SQL inside formulas.
