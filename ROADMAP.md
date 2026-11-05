# Roadmap

**Current status: Phase 0, project scaffold. There is nothing to use yet.**
`fadodb version` is the only working command. This page is updated when a
phase starts and when it ends; it never describes a phase as done before its
acceptance criteria pass.

Legend: ✅ done · 🚧 in progress · ⬜ not started

| Phase | Goal | Status |
|---|---|---|
| 0 | Foundations: module, gates, CI, release pipeline, ADRs, docs | 🚧 |
| 1 | Formula engine: lexer, parser, binder, bytecode, Go VM, SQL generator, conformance suite | ⬜ |
| 2 | Document model and SQLite store: classes, typed values, one table per class, event log, migrations, queries | ⬜ |
| 3 | Statechart: states, transitions, guards, eventless transitions, exit and entry snapshots | ⬜ |
| 4 | Rules and actions: event-condition-action rules, built-in actions, transactional outbox, admin tables | ⬜ |
| 5 | Security: users, groups, API keys, access rules compiled to SQL predicates | ⬜ |
| 6 | Server and CLI: JSON command set over HTTP and WebSocket, OpenAPI, `fadodb serve` | ⬜ |
| 7 | Browser VM and reference frontend: TypeScript VM on the same bytecode, UI schema, TypeScript client | ⬜ |
| 8 | Analytics and process mining: SQL views over the event log, optional DuckDB build | ⬜ |
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
a decimal number type, OCEL export, nested arrays.
