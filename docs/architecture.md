# FadoDB architecture

Status: design, 2026-11-05. This describes what FadoDB is being built to be.
Nothing below is implemented yet; see [ROADMAP.md](../ROADMAP.md) for what
exists today. Decisions with their reasoning are in [adr/](adr/).

FadoDB is an embeddable active document database. A document class declares
typed named values, Excel-like formulas, a statechart, and rules that fire
actions on state changes. The engine keeps an append-only event log with
snapshots, evaluates formulas on the server and in the browser from one
compiled bytecode, exposes every document class as a queryable table, and
enforces user and group rules written as formulas. Analytics and process
mining run as SQL over the event log, with DuckDB as an optional add-on. The
core is pure Go, ships as one small binary or library, and speaks a protocol
that lets any frontend attach.

## Principles

1. **Fast by construction.** A feature that cannot state its performance budget is not designed yet.
2. **Small.** Every dependency needs a written justification. The core has one.
3. **Deterministic.** Same class, same values, same user → same result on the server, in SQL, and in the browser.
4. **Data, not code.** A document class is a JSON document. Formulas are text. Actions are declarations.
5. **Protocol first.** Anything the reference frontend can do, any frontend can do through the same messages.
6. **Boring storage.** SQLite in WAL mode in the core. Postgres for the enterprise edition. DuckDB for analytics.
7. **The conformance suite is the specification.** If a behaviour is not in the suite, it is not promised.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────────────┐
│ Frontends: reference web renderer · CLI · your app · AI agent (MCP)  │
└───────────────┬───────────────────────┬──────────────────┬───────────┘
                │ HTTP + WebSocket      │ C ABI (JSON)     │ Go API
┌───────────────▼───────────────────────▼──────────────────▼───────────┐
│ Protocol layer: one JSON command set, OpenAPI described              │
├──────────────────────────────────────────────────────────────────────┤
│ Engine                                                               │
│  model     class definitions, typed values, validation, migrations   │
│  formula   lexer · parser · binder · bytecode · Go VM · SQL gen      │
│  state     statechart: guards, entry/exit, eventless transitions     │
│  rules     ECA rules, built-in actions, outbox worker                │
│  auth      users, groups, tokens, rule compilation to predicates     │
│  query     filter DSL, read-only SQL with injected predicates        │
├──────────────────────────────────────────────────────────────────────┤
│ Store: SQLite (core) │ Postgres (enterprise)        Event log        │
├──────────────────────────────────────────────────────────────────────┤
│ Analytics (optional build): DuckDB attached to the store, mining SQL │
└──────────────────────────────────────────────────────────────────────┘
```

The browser VM is a TypeScript port of the Go VM executing the same bytecode.
The server ships a **class bundle** (definition, bytecode, dependency graph, UI
schema) and a **document view** (values, state, allowed transitions,
permissions); the frontend renders and sends back three kinds of messages:
set values, request transition, query.

---

## Core concepts

### Document class

A class is one JSON document. Example:

```json
{
  "name": "Invoice",
  "version": 1,
  "values": {
    "customer": {"type": "text", "required": true},
    "lines":    {"type": "array", "of": "number"},
    "discount": {"type": "number", "default": 0},
    "due":      {"type": "date"},
    "approver": {"type": "ref", "class": "User"}
  },
  "formulas": {
    "subtotal": "SUM(lines)",
    "total":    "ROUND(subtotal * (1 - discount), 2)",
    "overdue":  "IF(ISBLANK(due), FALSE, TODAY() > due AND STATE() <> \"paid\")"
  },
  "states": {
    "initial": "draft",
    "list":    ["draft", "submitted", "approved", "paid", "cancelled"],
    "final":   ["paid", "cancelled"]
  },
  "transitions": [
    {"name": "submit",  "from": "draft",     "to": "submitted", "guard": "total > 0"},
    {"name": "approve", "from": "submitted", "to": "approved",  "guard": "INGROUP(\"finance\")"},
    {"name": "pay",     "from": "approved",  "to": "paid"},
    {"name": "cancel",  "from": ["draft", "submitted"], "to": "cancelled"}
  ],
  "rules": [
    {"on": "enter", "state": "approved", "if": "total > 10000",
     "do": [{"action": "http.call", "url": "\"https://hooks.example/approved\"",
             "body": {"id": "ID()", "total": "total"}}]},
    {"on": "enter", "state": "paid",
     "do": [{"action": "create", "class": "Receipt",
             "values": {"invoice": "ID()", "amount": "total"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"finance\") OR customer = USER().name",
    "create": "INGROUP(\"sales\")",
    "update": "STATE() = \"draft\"",
    "transition": {"approve": "INGROUP(\"finance\") AND approver <> USER().id"}
  }
}
```

Every string in `guard`, `if`, `access`, action arguments and UI bindings is a
formula. Literal text inside a formula is quoted. Classes are versioned; a
change to values or formulas creates a new version and a migration (see Class migrations below).

### Value types

| Type | Representation | Notes |
|---|---|---|
| `null` | blank | Excel BLANK semantics, defined once in ADR-0005 |
| `bool` | true/false | Comparisons yield bool, not 1/0 |
| `number` | float64 | Excel arithmetic semantics. A `decimal` type is a post-1.0 ADR |
| `text` | UTF-8 string | |
| `date` | int64 seconds UTC | One representation; `date` and `datetime` differ in display only |
| `ref` | `class:id` | Typed reference to another document |
| `array<T>` | list of one scalar type | Scalar or array, as required; no nested arrays in 1.0 |

Records and nested documents are expressed as references, not as nested values.

### Formulas

Excel-like syntax, case-insensitive functions, the operator set from sfmt
(`+ - * / \ ^ % &`, comparisons, `AND OR NOT`, `IF(...)` and `IF ... THEN ... ELSE`).
The function catalog is a JSON file that generates documentation, editor hints,
and conformance tests. Functions fall into three evaluation classes:

| Class | Examples | Where it runs |
|---|---|---|
| Pure | `IF ROUND SUM LEFT DATE ADDMONTHS` | Go VM, SQL, browser VM |
| Context | `USER() INGROUP() STATE() ID() NOW() TODAY()` | All three, with a context record passed in |
| Data | `LOOKUP(class, id, field)`, `COUNTDOCS(class, filter)`, `TABLE(name, key, col)` | Server only: VM through a host interface, SQL as subqueries. The browser receives the last computed value. |

Formulas within a class form a DAG. The binder rejects cycles at definition
time and precomputes the evaluation order, so a value change recomputes only
its dependents.

### Statechart

- Flat named states, one initial, optional final states. Hierarchical states in 1.x.
- A transition has a name, source states, a target, an optional guard formula,
  and optional entry/exit effects. Transitions are the only way state changes.
- **Eventless transitions** (guard only, no name) are evaluated after every
  value change, with a per-commit bound (default 8) to stop loops.
- Exit and entry snapshots are taken automatically and stored on the
  transition event (`before`, `after`).
- Timers (`after: "7d"`) are a 0.x feature implemented through the outbox scheduler.

### Event log

Two tables carry all history:

```
events(id, ts, class, doc_id, kind, name, from_state, to_state,
       actor_id, before_json, after_json, cause_event_id)
event_docs(event_id, class, doc_id, qualifier)   -- every document an event touched
```

Kinds: `created`, `values_set`, `transition`, `action_run`, `action_failed`,
`deleted`. `event_docs` links an event to every document it touched with a
qualifier such as `created` or `transitioned`. That one table is what makes
multi-document process mining possible later, and it is cheap only if added now.

### Rules and actions

Triggers: `create`, `values_change(value...)`, `exit(state)`, `enter(state)`,
`transition(name)`, `delete`, and `schedule` (0.x). Each rule has an optional
condition formula and a list of actions.

| Action | Sync or outbox | Notes |
|---|---|---|
| `set` values on the same document | sync, same transaction | Deterministic, re-triggers recalculation |
| `create` document | outbox | Idempotency key = event id + action index |
| `transition` document | outbox | Same key |
| `table.insert / update / delete` | outbox | Admin tables, see Admin tables |
| `http.call` | outbox | Retries with backoff, dead letter after N |
| `emit` custom event | sync | For integrations and subscriptions |

Outbox execution results are events themselves (`action_run`, `action_failed`),
so the audit trail is complete and process mining sees actions too.

### Admin tables

Administrators create plain tables through a DDL subset (`CREATE TABLE`,
`ALTER TABLE ADD COLUMN`, indexes). Formulas read them with data functions and
actions write them. In SQLite they live in the same file; in the enterprise
edition they live in Postgres. The SQL generator targets both dialects.

### Security

- Principals: users, groups, group membership, API keys, sessions. Roles: `admin`, `user`.
- Access rules per class: `read`, `create`, `update`, `delete`, and per-transition rules.
- `read` compiles to a SQL predicate appended to every query and to every
  subscription filter, so the change stream can never leak a row the query would hide.
- `create`, `update`, `delete` and transition rules run in the VM at commit time.
- Admins bypass rules. Everyone else gets exactly what the rules say.

### Querying the document stack

Each class is a table `doc_<class>` with typed columns, `state`, and system
columns (`id`, `version`, `created_at`, `updated_at`, `state_entered_at`).
Two query paths:

1. A JSON filter DSL (`where`, `order`, `limit`, `fields`) for clients.
2. A read-only SQL endpoint over an allowlisted set of views. The SQLite
   authorizer restricts statements to `SELECT`, and the engine rewrites the
   query to inject the caller's read predicate.

### Visualisation

A UI schema is a second JSON document bound to a class. Components bind to
values; `visible`, `enabled`, `style` and templates are formulas evaluated live
in the browser VM. The reference renderer is one implementation of the schema;
Flutter, terminal, or spreadsheet frontends are others.

```json
{
  "class": "Invoice",
  "layout": [
    {"type": "field",   "value": "customer"},
    {"type": "list",    "value": "lines"},
    {"type": "text",    "template": "Total: {total}", "visible": "total > 0"},
    {"type": "badge",   "template": "OVERDUE", "visible": "overdue", "style": "\"danger\""},
    {"type": "actions", "transitions": ["submit", "approve", "pay"]}
  ]
}
```

---

## Formula engine

### Pipeline

```
source → lexer → parser (AST) → binder (names→slots, types, DAG, cycles)
       → optimizer (constant folding) → bytecode
bytecode → Go VM          (typed tagged stack, zero allocations on the scalar path)
bytecode → SQL generator  (SQLite, Postgres, DuckDB dialects)
bytecode → browser VM     (TypeScript, zero dependencies)
```

### Bytecode

Versioned, compact, serializable to binary and to a JSON debug form.

```
header     magic "FADO", format version u16, flags u16
constants  numbers f64[], texts utf8[]
slots      referenced values: index → name, type
code       [op u8][operand i32] ...
meta       result type, dependencies, evaluation class, context and data usage
```

The bytecode format is a public contract from 0.4 on, because the browser VM
and the C ABI consume it.

### Value representation in the Go VM

```go
type Value struct {
    kind Kind      // Null, Bool, Number, Text, Date, Ref, Array
    num  float64   // Number, Date (seconds), Bool (0/1)
    str  string    // Text, Ref
    arr  []Value   // Array
}
```

Scalars never allocate. The VM keeps a fixed-size value stack per execution,
like sfmt's float stack, and a call-free instruction loop.

### Semantics to fix in ADRs before coding

Blank propagation; division by zero (sfmt: `/` gives 0, `\` errors); mixed-type
comparisons; text/number coercion rules; date arithmetic and month clamping;
number formatting in `TEXT()`; rounding mode; string collation.

### Conformance suite

A directory of JSON cases `{formula, inputs, context, expected}` executed by
all three targets. Property-based tests generate random formulas and assert
VM = SQL = browser VM. The suite is published; a frontend author can run it
against their own VM port.

---

## Storage

### SQLite core

- WAL mode, `synchronous=NORMAL`, one writer goroutine with batching, prepared statements, read connections pooled.
- Schema tables: `classes`, `class_versions`, `doc_<class>` (one per class), `events`, `event_docs`, `outbox`, `users`, `groups`, `memberships`, `api_keys`, `sessions`, `tables_registry`, `schema_version`.
- Indexes declared per value in the class definition (`"index": true`).

### Postgres (enterprise)

Same engine, a second dialect in the SQL generator, driver `pgx` through
`database/sql`, kept in a separate Go module so the core stays pure. Row-level
security is still FadoDB's predicate injection, not Postgres RLS, so behaviour
is identical across backends.

### Class migrations

Adding a value or formula: in place. Changing a type or removing a value:
a new class version with a migration record; documents migrate lazily on
first write or eagerly by command. The event log is never rewritten.

### Analytics (optional build)

DuckDB attaches the SQLite file read-only, or Postgres through its scanner.
Mining views live here. The analytics build is a second binary
(`fadodb-analytics`) or a build tag, never part of the core.

---

## Protocol and embedding

### Go API

```go
db, err := fado.Open("app.fado", fado.Options{})
err  = db.DefineClass(ctx, classJSON)
id, err := db.Create(ctx, "Invoice", fado.Values{"customer": "ACME"}, fado.As(user))
err  = db.Set(ctx, "Invoice", id, fado.Values{"discount": 0.1}, fado.As(user))
err  = db.Transition(ctx, "Invoice", id, "approve", fado.As(user))
rows, err := db.Query(ctx, `SELECT id, total FROM Invoice WHERE state = 'approved'`, fado.As(user))
sub  := db.Subscribe(ctx, fado.Filter{Class: "Invoice"}, fado.As(user))
```

### JSON command set

One command set serves HTTP, WebSocket and the C ABI. HTTP routes are a thin
mapping:

| Route | Purpose |
|---|---|
| `POST /v1/classes`, `GET /v1/classes/{name}`, `GET /v1/classes/{name}/bundle` | Define classes; fetch the bundle for frontends |
| `POST /v1/docs/{class}`, `GET /v1/docs/{class}/{id}`, `PATCH .../values`, `DELETE` | Documents |
| `POST /v1/docs/{class}/{id}/transitions/{name}` | Transition |
| `GET /v1/docs/{class}/{id}/events` | History |
| `POST /v1/query`, `POST /v1/sql` | Filter DSL, read-only SQL |
| `GET /v1/subscribe` (WebSocket) | Change stream filtered by the caller's read predicate |
| `/v1/auth/*`, `/v1/users`, `/v1/groups`, `/v1/tables` | Administration |
| `GET /v1/health`, `GET /v1/metrics` | Operations; Prometheus text format written by hand, no client library |

The OpenAPI document is generated from the Go route table and checked into `api/`.

### C ABI

`go build -buildmode=c-shared` producing `libfadodb` with three functions:
`fado_open`, `fado_exec(json) → json`, `fado_close`. The payload is the same
JSON command set, so every language binding is a thin wrapper and the HTTP and
in-process paths share client code.

### Clients

TypeScript (used by the reference renderer) and Python, both generated from
OpenAPI, both zero-dependency. Other languages come from the community through
the OpenAPI file and the C ABI.

---

## Analytics and process mining

All mining is SQL over `events` and `event_docs`, parameterised by class. Views
shipped with the core (SQLite) and the analytics build (DuckDB):

| View | Answers |
|---|---|
| `mining_dfg` | Directly-follows graph: state pairs, counts, median and p90 duration |
| `mining_variants` | Distinct state paths per document with frequency |
| `mining_time_in_state` | Time spent per state, per document and aggregated |
| `mining_bottlenecks` | States ranked by p90 waiting time |
| `mining_throughput` | Documents entering and leaving per period |
| `mining_actions` | Action success, failure and retry counts per rule |

Directly-follows in one query:

```sql
WITH steps AS (
  SELECT doc_id, to_state AS state, ts,
         LEAD(to_state) OVER (PARTITION BY doc_id ORDER BY ts) AS next_state,
         LEAD(ts)       OVER (PARTITION BY doc_id ORDER BY ts) AS next_ts
  FROM events WHERE class = ? AND kind = 'transition'
)
SELECT state, next_state, COUNT(*) AS n,
       quantile_cont(next_ts - ts, 0.5) AS median_seconds
FROM steps WHERE next_state IS NOT NULL
GROUP BY 1, 2 ORDER BY n DESC;
```

An OCEL 2.0 export command may be added after 1.0 for users of PM4Py or Celonis.
It is an export, not a dependency.

---

## Performance program

### Initial budgets

Validated and revised at the end of Phase 1; until then they are targets, not promises.

| Measure | Budget |
|---|---|
| Simple arithmetic formula, 5 operations, Go VM | < 100 ns, 0 allocations |
| Full recalculation of a 50-formula document | < 50 µs |
| Transition commit, p99 (guard, recalc, event row, snapshots), NVMe | < 2 ms |
| Document creates per second, batched | ≥ 10,000 |
| Transitions per second, unbatched, single writer | ≥ 2,000 |
| Indexed filter over 1,000,000 documents | < 10 ms |
| `mining_dfg` over 10,000,000 events, DuckDB build | < 2 s |
| Browser VM recalculation of 200 formulas | < 1 ms |
| Core binary size | ≤ 15 MB |
| Open an existing database | < 50 ms |

### Tooling and gates

- Go benchmarks next to every package; `-benchmem` always on.
- CI compares against the `main` baseline with `benchstat` and fails on more than
  5% regression in time or any regression in allocations per operation.
- A perf suite under `bench/` generates datasets and runs the end-to-end
  scenarios in the budgets table nightly; results are committed to `docs/perf/` per release.
- CPU and allocation profiles are archived per release so a regression can be diffed.
- Hot-path rules in `CONTRIBUTING.md`: no allocations in the VM loop, no
  interface boxing of values, no reflection, prepared statements only, batched writes.
- Actions are asynchronous by default so an external call can never consume the transition budget.
- Every design note and ADR has a **Performance** section with the budget and the benchmark that proves it.

---

## Repository layout

```
fadodb/
  cmd/fadodb/            single binary: serve, class, doc, query, bench, export
  fado/                  public Go API (small surface, see Go API)
  internal/formula/      lexer, parser, binder, bytecode, vm, sqlgen, catalog
  internal/model/        class definitions, values, validation, migrations
  internal/store/        sqlite store, events, outbox, schema
  internal/state/        statechart engine
  internal/rules/        ECA rules, built-in actions, outbox worker
  internal/auth/         users, groups, tokens, predicate compilation
  internal/query/        filter DSL, SQL endpoint, authorizer
  internal/server/       http, websocket, command dispatch
  api/                   openapi.json, json schemas for class and ui documents
  analytics/             duckdb build: attach, mining views (separate module)
  postgres/              postgres dialect and driver (separate module)
  cabi/                  c-shared build and header
  web/vm/                TypeScript VM, zero dependencies
  web/renderer/          reference frontend
  clients/ts/  clients/py/
  conformance/           formula and statechart cases run by all targets
  bench/                 perf suite, dataset generators, baselines
  docs/                  this plan, adr/, perf/, dependencies.md, function reference
```

Separate Go modules for `analytics/` and `postgres/` keep cgo and drivers out
of the core `go.mod`.

---

## Out of scope for 1.0

Parallel statechart regions; BPMN; multi-tenancy inside one database file
(run one file per tenant); replication and clustering; WASM plugin actions;
a visual class designer; a decimal number type; OCEL export; nested arrays.

---

