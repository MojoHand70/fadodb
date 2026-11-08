# Roadmap

**Current status: Phase 1, the formula engine, done on 2026-11-06 and released as v0.1.0. Phase 2, the document model and store, started on 2026-11-07.**
The Go API (`fado`) opens a database file, defines schemas, classes and
explicit tables, and creates, changes, reads and deletes documents as
commits: assignments and defaults, the auto recalculation, typed history
rows, child rows for composites, an event log with idempotency keys and
business dates, attachments behind a blob store, `LOOKUP` against tables,
a filter over a class and read-only SQL over the views, with previews that
write nothing. `fadodb formula eval` evaluates a formula and `fadodb
check` verifies a file. The statechart, rules, access rules, `QUERY` and
the server come with their phases. Since v0.1.0 the records ADR-0022 to
ADR-0038 fixed what the first embedder needs and what Phase 2 measured:
SQLite as the transactional store and the in-memory index for lookups, by
measurement (ADR-0035, ADR-0036), the compiled `LOOKUP` and explicit
tables (ADR-0037), the filter and the SQL endpoint (ADR-0038), and class
versions with terminal states and migrations (ADR-0039). Open in Phase 2:
the release. This page is updated when a phase starts and
when it ends; it never describes a phase as done before its acceptance
criteria pass.

Legend: ✅ done · 🚧 in progress · ⬜ not started

| Phase | Goal | Status |
|---|---|---|
| 0 | Foundations: module, gates, CI, release pipeline, ADRs, docs | ✅ |
| 1 | Formula engine: lexer, parser, binder, bytecode, Go VM, SQL generator with SQLite and DuckDB dialects, conformance suite | ✅ |
| 2 | Document model and store: primary-store benchmark, variables with defaults, `json` type, enum values, range constraints, named schemas and object values with the predefined vocabulary, attachments as `File` values with a blob store, class metadata and title, current, history and child tables, commits with idempotency keys and business dates, preview and validate without writing, event log, migrations, LOOKUP, queries | 🚧 |
| 3 | Statechart: states, transitions, guards, eventless transitions, exit and entry snapshots, field-level rights per state | ⬜ |
| 4 | Rules and actions: event-condition-action rules, built-in actions incl. reset and refresh, transactional outbox, executors the embedder registers, the transition event, explicit tables with storage bindings, QUERY | ⬜ |
| 5 | Security: the principal from the embedder on every call, access rules compiled to SQL predicates, an optional package of users, groups and API keys for the server | ⬜ |
| 6 | Server and CLI: JSON command set over HTTP and WebSocket, documents on the wire, layouts, OpenAPI, `fadodb serve` | ⬜ |
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
