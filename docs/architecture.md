# FadoDB architecture

Status: design, revised 2026-11-06 with the document model (variables,
commits, recalculation, document tables, explicit tables, DuckDB as the
analytical engine, class metadata, layouts, the wire format). Nothing below
is implemented yet; see
[ROADMAP.md](../ROADMAP.md) for what exists today. Decisions with their
reasoning are in [adr/](adr/).

FadoDB is an embeddable active document database. A document class declares
typed variables, Excel-like formulas, a statechart, and rules that fire
actions on state changes. Every change is a versioned commit; the history of
every document is a typed table, not a log of blobs. Formulas run on the
server and in the browser from one compiled bytecode. Every document class is
a queryable table, explicit tables plug into any storage, and user and group
rules are themselves formulas. DuckDB is the analytical engine: query
functions in formulas, analytics over the document stack and process mining
run on it. The core library is pure Go and talks to the analytical engine
through an interface, so a lite build exists for embedding anywhere.

---

## Principles

1. **Fast by construction.** A feature that cannot state its performance budget is not designed yet.
2. **Small.** Every dependency needs a written justification. The core library has one.
3. **Deterministic.** Same class, same document, same user: same result on the server, in SQL, and in the browser.
4. **Data, not code.** A document class is a JSON document. Formulas are text. Actions are declarations.
5. **Protocol first.** Anything the reference frontend can do, any frontend can do through the same messages.
6. **Boring storage.** SQLite for transactions. DuckDB for analytics. Postgres for the enterprise edition.
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
│  model     classes, variables, defaults, validation, migrations      │
│  formula   lexer · parser · binder · bytecode · Go VM · SQL dialects │
│  state     statechart: guards, entry/exit, eventless transitions     │
│  rules     ECA rules, built-in actions, outbox worker                │
│  auth      users, groups, access rules compiled to predicates        │
│  query     LOOKUP/QUERY compilation, filter DSL, read-only SQL       │
├──────────────────────────────────────────────────────────────────────┤
│ Transactional store: SQLite (core) │ Postgres (enterprise)           │
│   current · history · child tables · events · outbox · tables       │
├──────────────────────────────────────────────────────────────────────┤
│ Analytical engine: DuckDB (full build) │ SQLite fallback (lite)     │
│   projection of document tables · native tables · attached DBs      │
└──────────────────────────────────────────────────────────────────────┘
```

Two builds from one code base: `fadodb`, the reference product with DuckDB
compiled in, and `fadodb-lite`, pure Go for embedding and the C ABI, where
the analytical engine is implemented on SQLite with documented limits. The
browser VM is a TypeScript port of the Go VM executing the same bytecode.

---

## Core concepts

### Document class

A class is one JSON document.

```json
{
  "name": "Invoice",
  "version": 1,
  "label": "Invoice",
  "plural": "Invoices",
  "description": "A bill issued to a customer.",
  "icon": "receipt",
  "title": "\"INV-\" & number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "customer": {"type": "ref", "class": "Customer", "required": true},
    "issued":   {"type": "date", "default": "TODAY()"},
    "due":      {"type": "date", "default": "ADDDAYS(issued, 30)"},
    "owner":    {"type": "ref", "class": "User", "default": "USER().id"},
    "currency": {"type": "text", "default": "\"EUR\""},
    "discount": {"type": "number", "default": "0"},
    "shipping": {"type": "json"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "number", "price": "number"}},

    "subtotal":  {"formula": "SUM(lines.qty * lines.price)"},
    "ship_city": {"formula": "JSONVALUE(shipping, \"$.city\")"},
    "total":     {"formula": "ROUND(subtotal * (1 - discount), 2)"},
    "rate":      {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"},
    "total_eur": {"formula": "ROUND(total * rate, 2)"},
    "overdue":   {"formula": "TODAY() > due AND STATE() <> \"paid\"",
                  "mode": "triggered", "on": ["schedule:daily", "enter:approved"]},
    "backlog":   {"formula": "LOOKUP(Invoice, COUNT(id), customer = THIS.customer AND state <> \"paid\")",
                  "mode": "triggered", "on": ["refresh", "enter:submitted"]}
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
    {"on": "enter", "state": "approved", "if": "total_eur > 10000",
     "do": [{"action": "http.call", "url": "\"https://hooks.example/approved\"",
             "body": {"id": "ID()", "total": "total_eur"}}]},
    {"on": "enter", "state": "paid",
     "do": [{"action": "create", "class": "Receipt",
             "values": {"invoice": "ID()", "amount": "total"}}]},
    {"on": "transition", "name": "cancel",
     "do": [{"action": "reset", "variables": ["discount"]}]}
  ],
  "access": {
    "read":   "INGROUP(\"finance\") OR owner = USER().id",
    "create": "INGROUP(\"sales\")",
    "update": "STATE() = \"draft\"",
    "transition": {"approve": "INGROUP(\"finance\") AND owner <> USER().id"}
  }
}
```

Every string in `default`, `formula`, `guard`, `if`, `access`, `title`,
action arguments and layout bindings is a formula. Literal text inside a
formula is quoted. Classes are versioned; a change to variables creates a
new version and a migration (see Class migrations).

The header holds what is not a variable: `label` and `plural` for display,
`description`, `icon` and `color` for frontends, and `title`, a formula over
pure functions and the document's own variables that gives every document
its display name. The title is computed like an auto variable and exposed as
the system variable `TITLE`, so a list sorts by it and a `ref` renders it
with one join.

### JSON: definitions and interchange, not storage

JSON is FadoDB's format for definitions and for interchange. Class
definitions, layouts, the function catalog and the bytecode debug form are
JSON documents and are stored as such. Every command, every document on the
wire and the bundle a frontend fetches are JSON. What is not JSON is the
stored document: its values are typed columns in the current and history
tables (see Document tables), because a blob cannot be a queryable table and
cannot be projected into the analytical engine. The one exception is the
opaque `json` variable type, which stores a JSON value the engine never
interprets.

### Variables

A class declares **variables**. Each has a name and a type.

| Type | Representation | Notes |
|---|---|---|
| `null` | blank | Excel BLANK semantics, defined once in ADR-0005 |
| `bool` | true/false | Comparisons yield bool, not 1/0 |
| `number` | float64 | Excel arithmetic semantics; a `decimal` type is a post-1.0 ADR |
| `text` | UTF-8 string | |
| `date` | int64 seconds UTC | One representation; `date` and `datetime` differ in display only |
| `ref` | class and id | Typed reference to another document |
| `json` | opaque JSON text | Structured data the engine does not interpret; read through `JSONVALUE` only |
| `array<scalar>` | ordered list of one scalar type | Short lists the document owns, such as tags |
| `rows<schema>` | ordered rows with named scalar columns | Embedded tables such as invoice lines; also what `QUERY` returns |

No nesting: a `rows` column is a scalar, an array holds scalars. Values are
immutable; assigning an element produces a whole new value in a new commit.
Composite variables are meant to hold up to a few hundred elements. The soft
limit is 1,000 elements with a definition-time warning, the hard cap is
10,000; larger data belongs in an explicit table behind a `ref`.

A text value, stored or computed, is at most the database's text limit,
65,536 code points by default (ADR-0021). A formula that would produce more
stops with `out_of_range` before building it, on every evaluator; the limit
is chosen when the database is created and travels with it, so that a
commit recomputes as it was committed wherever the database is opened.

A `json` variable is the escape hatch for data without a typed shape yet: an
address block, settings, a payload from another system. It is stored as JSON
text, compared as text, and reachable in formulas only through
`JSONVALUE(value, path)`, a pure function that extracts one scalar by a path
such as `"$.city"`. It cannot be indexed, cannot appear inside `array` or
`rows`, and no formula produces one. A `json` variable that grows a
`JSONVALUE` for every field is a sign that it should become typed variables,
a `rows` variable or an explicit table.

Variables come in three kinds:

- **Input** variables are set by users or actions. An input may declare a
  `default` formula, evaluated when a document is created for every input the
  creator did not supply, in dependency order, and again when the `reset`
  action names the variable. A default may use any function, including
  `USER()`, `NOW()`, `LOOKUP` and `QUERY`, because it runs once at a known
  moment.
- **Computed** variables carry a `formula` and are read-only. Their `mode` is
  `auto` (the default) or `triggered`; see Commits and recalculation.
- **System** variables exist on every document and cannot be declared:
  `ID`, `TITLE`, `STATE`, `VERSION`, `CREATED_AT`, `UPDATED_AT`,
  `STATE_ENTERED_AT`. `TITLE` comes from the class's `title` formula and
  equals `ID` when the class declares none.

Names are identifiers, resolved case-insensitively in formulas and shown as
declared in tables and views. The columns the generated tables and views add
are reserved and rejected as variable and schema column names at definition
time: `id`, `title`, `state`, `version`, `created_at`, `updated_at`,
`state_entered_at`, `valid_from`, `valid_to`, `actor_id`, `event_id`,
`doc_id`, `idx` and `value`.

### Formulas

Excel-like syntax, case-insensitive functions, the operator set
`+ - * / \ ^ % &`, comparisons, `AND OR NOT`, `IF(...)` and
`IF ... THEN ... ELSE`. The function catalog is a JSON file that generates
documentation, editor hints and conformance tests. Functions fall into four
classes, and where a formula may use them depends on what happens to its
result:

| Class | Examples | Allowed in |
|---|---|---|
| Pure | `IF ROUND SUM LEFT DATE ADDMONTHS JSONVALUE` | everywhere |
| Context | `USER() INGROUP() NOW() TODAY() STATE() ID()` | defaults, triggered formulas, guards, rules, access rules, layouts; not in auto formulas |
| Lookup | `LOOKUP(source, column, condition)` | everywhere; in auto formulas only against explicit tables |
| Query | `QUERY(source, columns, condition, sort, groupby, limit)` | defaults, triggered formulas, rules, layouts; not in auto formulas |

The rule behind the table: the stored value of an auto variable must be a
function of the document itself, so it may not depend on who is looking,
on the clock, or on data outside the document beyond a keyed lookup.

Inside `LOOKUP` and `QUERY`, bare names are columns of the source and the
document's own variables are reached as `THIS.name`. Columns may be
aggregates (`COUNT(id)`, `SUM(amount)`) and `groupby` names the grouping
columns. A source is an explicit table or a document stack view such as
`Invoice` or `Invoice_history`. The compiler translates the call to the SQL
dialect of the storage that holds the source, binds the condition to its
columns, injects the caller's access predicate, and rejects anything else at
definition time. Formulas never carry raw SQL.

Formulas within a class form a dependency graph. The binder rejects cycles at
definition time and precomputes the evaluation order.

### Commits and recalculation

A **commit** is one atomic change to one document: a set of variable
assignments, or one transition, or both, applied together with the
recalculation of dependent formulas, the guard and rule checks, the new
history row and the event rows, in one database transaction. A commit
increments the document's version by exactly one. Commits come from a user
edit, a transition request, an action, or a system recompute; an action that
touches several documents produces one commit per document, linked by the
cause event. Batch loads may put many commits in one transaction for
throughput.

Inside a commit the engine:

1. applies the assignments, or evaluates defaults for a new document;
2. recalculates **auto** variables whose dependencies changed, in topological
   order, only the dirty part of the graph;
3. evaluates the requested transition's guard, then eventless transitions
   until none fires, bounded to eight per commit;
4. recalculates **triggered** variables whose trigger fired in this commit
   (`enter`, `exit`, `transition`, `refresh`);
5. runs rules: synchronous actions now, the rest into the outbox;
6. writes the current row, the history row, the child rows and the events.

Triggered variables also recalculate on `table:<name>` when the named table or
stack changes, and on `schedule:<spec>`. Those recomputes are **set-wise**:
one query per class computes the new value for every affected document, and
the results are applied as system commits, never one query per document. A
triggered variable stores the version and time at which it was computed, and
documentation states plainly that such values are consistent as of that
moment.

The browser evaluates auto formulas live while a user edits, from the same
bytecode, so the user sees totals change before saving. The server's result
on commit is authoritative; the conformance suite guarantees the two agree.

### Statechart

- Flat named states, one initial, optional final states. Hierarchical states in 1.x.
- A transition has a name, source states, a target, an optional guard formula,
  and optional entry/exit effects. Transitions are the only way state changes.
- **Eventless transitions** have a guard and no name, and are evaluated after
  every value change with a per-commit bound of eight.
- Exit and entry snapshots are the history rows before and after the
  transition; the transition event references both.
- Timers (`after: "7d"`) are a 0.x feature implemented through the outbox scheduler.

### Document tables

Each class is stored as typed tables generated from its definition, and the
history of every document is a table, not a blob:

```
doc_Invoice               one row per document, latest version      key: id
doc_Invoice_history       one row per committed version              key: (id, version)
                          + state, valid_from, valid_to, actor_id, event_id
doc_Invoice__lines        one row per element per version            key: (doc_id, version, idx)
                          + the row schema's columns, or `value` for arrays
events                    one row per thing that happened            key: event_id
                          class, doc_id, kind, name, from_state, to_state,
                          actor_id, before_version, after_version, cause_event_id
event_docs                every document an event touched            (event_id, class, doc_id, qualifier)
```

Every variable is a typed column in the current and history tables. Every
composite variable has a child table, copied per version, which is why
composite variables are kept small. A transition event points at the exit
snapshot as `before_version` and the entry snapshot as `after_version`, so
nothing is stored twice. The event log is the source of truth for what
happened and why; the history table is the source of truth for what the
document looked like. Both are append-only and never rewritten; deletions
append a `deleted` event and remove the current row.

What users see are views, never the physical names:

| View | One row per |
|---|---|
| `Invoice` | document, current values |
| `Invoice_history` | committed version, with `valid_from` and `valid_to` |
| `Invoice_states` | state visit: `state`, `entered_at`, `left_at`, `duration`, entry and exit versions |
| `Invoice_events` | event touching the class |
| `Invoice_lines` | element of the composite variable `lines`, current version |
| `Invoice_lines_history` | element per version |

"As of" queries are a range filter on `Invoice_history`. Process mining reads
`_states` and `_events` and never touches JSON.

### Rules and actions

Triggers: `create`, `values_change(variable...)`, `exit(state)`,
`enter(state)`, `transition(name)`, `delete`, and `schedule` (0.x). Each rule
has an optional condition formula and a list of actions.

| Action | Sync or outbox | Notes |
|---|---|---|
| `set` variables on the same document | sync, same transaction | Deterministic, re-triggers recalculation |
| `reset` input variables to their defaults | sync | Re-evaluates the named `default` formulas |
| `refresh` triggered variables | sync | Fires the `refresh` trigger for the named variables |
| `emit` custom event | sync | For integrations and subscriptions |
| `create` document | outbox | Idempotency key = event id + action index |
| `transition` document | outbox | Same key |
| `table.insert / update / delete` | outbox | Explicit tables, see below |
| `http.call` | outbox | Retries with backoff, dead letter after N |

Outbox execution results are events (`action_run`, `action_failed`), so the
audit trail covers side effects. `reset` and `refresh` are also commands a
frontend can send directly, which is what a button does.

### Explicit tables

A table is a first-class named object with a schema in the same type system,
indexes, permissions, and one attribute document tables do not have: a
**storage binding**.

| Binding | Where rows live | Default in |
|---|---|---|
| `sqlite` | the transactional store | lite build |
| `duckdb` | DuckDB's native columnar storage | full build |
| `external` | a connector: anything DuckDB can attach (Postgres, MySQL, SQLite files) in the full build; a `database/sql` driver in the lite build | |

Behind every binding is the same Go interface: dialect, query, exec, schema,
capabilities. Implementing it is how a third party hooks up their own
database. Administrators create tables through a DDL subset, load them with
the bulk import command (CSV and Parquet through DuckDB), and change them
through the `table.*` actions. Formulas read them through `LOOKUP` and
`QUERY`; the compiler uses the dialect of the table's binding.

In the full build, DuckDB is where everything meets: native tables, attached
external databases, and a projection of every document table fed from the
history and event tables. That projection is what lets `QUERY` join the
document stack with analytical data in one engine. Joins across bindings are
only available there; the lite build answers `LOOKUP` and `QUERY` from
SQLite with a documented subset of capabilities.

### Security

- Principals: users, groups, group membership, API keys, sessions. Roles: `admin`, `user`.
- Access rules per class: `read`, `create`, `update`, `delete`, and per-transition rules. Tables have `read` and `write` rules.
- `read` compiles to a SQL predicate appended to every query, view,
  subscription filter, and to every `LOOKUP` and `QUERY` whose source is a
  stack, so a formula can never see a row its author could not.
- `create`, `update`, `delete` and transition rules run in the VM at commit time.
- Admins bypass rules. Everyone else gets exactly what the rules say.

### Querying

Three paths, all with the caller's predicates injected:

1. `LOOKUP` and `QUERY` inside formulas, compiled, cached, portable.
2. A JSON filter DSL (`where`, `order`, `limit`, `fields`) for clients.
3. A read-only SQL endpoint over the views above, for analysts and BI tools.
   The SQLite authorizer restricts statements to `SELECT`; in the full build
   the same views exist in DuckDB.

### Layouts

Visuals live at class level, never per document. A class has any number of
named **layouts**, each a JSON document that binds components to variables:
`form`, `list`, `card`, `print`, or whatever a frontend needs. `visible`,
`enabled`, `editable`, `style` and templates are formulas evaluated live in
the browser VM, and a `rows` variable renders as a grid. A layout that looks
different per document does so through those formulas, not through stored
per-document presentation.

```json
{
  "class": "Invoice",
  "name": "form",
  "components": [
    {"type": "field",   "variable": "number"},
    {"type": "field",   "variable": "customer"},
    {"type": "grid",    "variable": "lines", "editable": "STATE() = \"draft\""},
    {"type": "text",    "template": "Total: {total} {currency}", "visible": "total > 0"},
    {"type": "badge",   "template": "OVERDUE", "visible": "overdue", "style": "\"danger\""},
    {"type": "button",  "label": "Refresh backlog", "command": {"refresh": ["backlog"]}},
    {"type": "actions", "transitions": ["submit", "approve", "pay"]}
  ]
}
```

Layouts are stored in their own table and versioned independently of the
class, so a layout change never bumps the class version or migrates
documents. Saving a layout validates every binding and formula against the
class; removing a variable that a layout binds to is rejected until the
layout changes. The reference renderer is one implementation of the layout
format; Flutter, terminal or spreadsheet frontends are others, and all of
them fetch the same bundle (see Documents on the wire).

Attachments and comments are neither variables nor visuals. They are planned
after 1.0 as side objects keyed by class and document id, with their own
events; nothing in the core model depends on them.

---

## Formula engine

### Pipeline

```
source → lexer → parser (AST) → binder (names→slots, types, DAG, cycles, function-class checks)
       → optimizer (constant folding) → bytecode
bytecode → Go VM          (typed tagged stack, zero allocations on the scalar path)
bytecode → SQL generator  (SQLite, DuckDB, Postgres dialects; used by LOOKUP/QUERY,
                           set-wise recompute, access predicates and views)
bytecode → browser VM     (TypeScript, zero dependencies)
```

### Bytecode

Versioned, compact, serializable to binary and to a JSON debug form.

```
header     magic "FADO", format version u16, flags u16
constants  numbers f64[], texts utf8[]
slots      referenced variables: index → name, type
code       [op u8][operand i32] ...
meta       result type, dependencies, function classes used, mode constraints
```

The bytecode format is a public contract from release 0.4 on, because the
browser VM and the C ABI consume it.

### Value representation in the Go VM

```go
type Value struct {
    kind Kind      // Null, Bool, Number, Text, Date, Ref, Array, Rows
    num  float64   // Number, Date (seconds), Bool (0/1)
    str  string    // Text, Ref
    list []Value   // Array
    rows *Rows     // Rows: column names, types, and column-major values
}
```

Scalars never allocate. The VM keeps a fixed-size value stack per execution
and a call-free instruction loop. Row-wise expressions over `rows`, such as
`SUM(lines.qty * lines.price)`, compile to a loop over the rows with the
scalar path inside.

### Semantics

The rules every evaluator follows are fixed in records before any code:
numbers in ADR-0004, blanks in ADR-0005, types in ADR-0013, grammar,
coercion, comparison, dates, text, composites and errors in ADR-0019, and
how `LOOKUP` and `QUERY` reach their engine in ADR-0020. In short: every
expression has a static type resolved at definition time, there is no
implicit conversion to or from text, text compares case-sensitively, dates
change only through functions, errors are a closed list of codes, and a
query function costs one engine call and never a round trip per row.

### Conformance suite

A directory of JSON cases `{formula, inputs, context, expected}` executed by
all three targets. Property-based tests generate random formulas and assert
VM = SQL = browser VM. The suite is published; a frontend author can run it
against their own VM port.

---

## Storage

### Transactional store: SQLite

- WAL mode, `synchronous=NORMAL`, one writer goroutine with batching,
  prepared statements, read connections pooled.
- Schema: `classes`, `class_versions`, `layouts`, per class the current,
  history and child tables, `events`, `event_docs`, `outbox`, `users`,
  `groups`, `memberships`, `api_keys`, `sessions`, `tables_registry`,
  `schema_version`, plus explicit tables bound to `sqlite`.
- Indexes declared per variable (`"index": true`), plus `(class, doc_id, ts)`
  on events and `(id, valid_from)` on history.

The choice of SQLite as the primary store is an expectation, not a measured
fact. The first Phase 2 benchmark writes 100,000 commits into SQLite and into
DuckDB and reports latency and throughput; the storage interface keeps the
decision cheap to flip. See ADR-0017.

### Analytical engine: DuckDB

In the full build DuckDB holds native explicit tables, attaches external
databases, and keeps a projection of every document table fed from the
history and event tables, append-only. `QUERY` over a stack, set-wise
recomputes, analytics and process mining run there. The projection lags the
transactional store by its feed delay; triggered variables record the version
they were computed at, and the documentation says so. The DuckDB Go client
bundles only the ICU, JSON, Parquet and Autocomplete extensions, so any
extension the full build relies on is installed at build time and verified
offline in CI. See ADR-0002 for the measured cost of the client.

### Postgres (enterprise)

Same engine, a third dialect, driver `pgx` through `database/sql`, kept in a
separate Go module. Row-level security is still FadoDB's predicate injection,
not Postgres RLS, so behaviour is identical across backends.

### Class migrations

Adding a variable or formula: in place. Changing a type or removing a
variable: a new class version with a migration record; documents migrate
lazily on first write or eagerly by command. History and events are never
rewritten.

---

## Protocol and embedding

### Go API

```go
db, err := fado.Open("app.fado", fado.Options{})
err  = db.DefineClass(ctx, classJSON)
err  = db.DefineLayout(ctx, "Invoice", layoutJSON)
id, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Customer:42"}, fado.As(user))
err  = db.Set(ctx, "Invoice", id, fado.Values{"discount": 0.1}, fado.As(user))
err  = db.Transition(ctx, "Invoice", id, "approve", fado.As(user))
err  = db.Refresh(ctx, "Invoice", id, []string{"backlog"}, fado.As(user))
rows, err := db.Query(ctx, `SELECT id, total FROM Invoice WHERE state = 'approved'`, fado.As(user))
sub  := db.Subscribe(ctx, fado.Filter{Class: "Invoice"}, fado.As(user))
```

### JSON command set

One command set serves HTTP, WebSocket and the C ABI. HTTP routes are a thin
mapping:

| Route | Purpose |
|---|---|
| `POST /v1/classes`, `GET /v1/classes/{name}`, `GET /v1/classes/{name}/bundle` | Define classes; fetch the bundle for frontends |
| `PUT /v1/classes/{name}/layouts/{layout}`, `GET .../layouts`, `GET .../layouts/{layout}` | Layouts, versioned independently of the class |
| `POST /v1/docs/{class}`, `GET /v1/docs/{class}/{id}`, `PATCH .../values`, `DELETE` | Documents |
| `POST /v1/docs/{class}/{id}/transitions/{name}` | Transition |
| `POST /v1/docs/{class}/{id}/refresh`, `POST .../reset` | Trigger recalculation of named variables; reset inputs to defaults |
| `GET /v1/docs/{class}/{id}/history`, `GET .../events` | Versions and log |
| `POST /v1/query`, `POST /v1/sql` | Filter DSL, read-only SQL |
| `GET /v1/subscribe` (WebSocket) | Change stream filtered by the caller's read predicate |
| `/v1/tables/*` | Explicit tables: schema, binding, rows, import |
| `/v1/auth/*`, `/v1/users`, `/v1/groups` | Administration |
| `GET /v1/health`, `GET /v1/metrics` | Operations; Prometheus text format written by hand |

The OpenAPI document is generated from the Go route table and checked into `api/`.

### Documents on the wire

A document is one JSON object: system variables at the top level, declared
variables under `values`, composites inline. Reads, the change stream,
create (`values` only) and `PATCH .../values` (the variables to change)
share this shape. A blank is `null`; a variable absent from a patch is
unchanged. Dates are integer seconds, refs are `"Class:id"` text, `json`
variables are embedded as they are.

```json
{
  "class": "Invoice", "id": "inv_01JC3X5M8K", "version": 7, "state": "approved",
  "title": "INV-2026-0042",
  "created_at": 1762300800, "updated_at": 1762387200, "state_entered_at": 1762387200,
  "values": {
    "number": "2026-0042", "customer": "Customer:42", "owner": "User:7",
    "issued": 1762300800, "due": 1764892800, "currency": "EUR", "discount": 0,
    "shipping": {"street": "Długa 1", "city": "Gdańsk"},
    "lines": [{"sku": "A1", "qty": 2, "price": 9.5}],
    "subtotal": 19, "ship_city": "Gdańsk", "total": 19, "rate": 1, "total_eur": 19,
    "overdue": false, "backlog": 3
  }
}
```

The bundle at `GET /v1/classes/{name}/bundle` is the class definition, its
layouts, the compiled bytecode of every formula including layout bindings,
and the slice of the function catalog they use. A frontend needs nothing
else to render a document and evaluate it live. The bundle is cached by an
ETag derived from the class and layout versions. Encoding documents is on
the read path, so the encoder is hand-written and reflection-free.

### C ABI

`go build -buildmode=c-shared` producing `libfadodb` with three functions:
`fado_open`, `fado_exec(json) → json`, `fado_close`. The payload is the same
JSON command set, so every language binding is a thin wrapper and the HTTP and
in-process paths share client code. The C ABI is built from the lite build.

### Clients

TypeScript (used by the reference renderer) and Python, both generated from
OpenAPI, both zero-dependency. Other languages come from the community through
the OpenAPI file and the C ABI.

---

## Analytics and process mining

All mining is SQL over the `_states` and `_events` views, parameterised by
class. Views shipped with both builds:

| View | Answers |
|---|---|
| `mining_dfg` | Directly-follows graph: state pairs, counts, median and p90 duration |
| `mining_variants` | Distinct state paths per document with frequency |
| `mining_time_in_state` | Time spent per state, per document and aggregated |
| `mining_bottlenecks` | States ranked by p90 waiting time |
| `mining_throughput` | Documents entering and leaving per period |
| `mining_actions` | Action success, failure and retry counts per rule |

Directly-follows over the states view:

```sql
WITH steps AS (
  SELECT doc_id, state, entered_at,
         LEAD(state)      OVER (PARTITION BY doc_id ORDER BY entered_at) AS next_state,
         LEAD(entered_at) OVER (PARTITION BY doc_id ORDER BY entered_at) AS next_entered
  FROM Invoice_states
)
SELECT state, next_state, COUNT(*) AS n,
       quantile_cont(next_entered - entered_at, 0.5) AS median_seconds
FROM steps WHERE next_state IS NOT NULL
GROUP BY 1, 2 ORDER BY n DESC;
```

The lite build runs the same views on SQLite, computing percentiles in Go.
An OCEL 2.0 export command may be added after 1.0 for users of PM4Py or
Celonis. It is an export, not a dependency.

---

## Performance program

### Initial budgets

Validated and revised at the end of Phase 1; until then they are targets, not promises.

| Measure | Budget |
|---|---|
| Simple arithmetic formula, 5 operations, Go VM | < 100 ns, 0 allocations |
| Full auto recalculation of a 50-formula document | < 50 µs |
| `LOOKUP` against an indexed explicit table, cached | < 1 µs; cold < 100 µs |
| Commit, p99 (assignments, recalc, history row, child rows, events), NVMe | < 2 ms |
| Document creates per second, batched | ≥ 10,000 |
| Commits per second, unbatched, single writer | ≥ 2,000 |
| Set-wise recompute of one triggered variable over 100,000 documents | < 2 s end to end |
| Indexed filter over 1,000,000 documents | < 10 ms |
| `mining_dfg` over 10,000,000 state visits, full build | < 2 s |
| Browser VM recalculation of 200 formulas | < 1 ms |
| Lite binary and library size | ≤ 15 MB |
| Open an existing database | < 50 ms |
| Encode a 50-variable document to JSON | < 5 µs |

### Tooling and gates

- Go benchmarks next to every package; `-benchmem` always on.
- CI compares against the `main` baseline with `benchstat` and fails on more than
  5% regression in time or bytes per operation, or any extra allocation per operation.
- A perf suite under `bench/` generates datasets and runs the end-to-end
  scenarios in the budgets table nightly; results are committed to `docs/perf/` per release.
- CPU and allocation profiles are archived per release so a regression can be diffed.
- Hot-path rules in `CONTRIBUTING.md`: no allocations in the VM loop, no
  interface boxing of values, no reflection, prepared statements only, batched writes.
- Actions are asynchronous by default so an external call can never consume the commit budget.
- Every design note and ADR has a **Performance** section with the budget and the benchmark that proves it.

---

## Repository layout

```
fadodb/
  cmd/fadodb/            single binary: serve, class, doc, table, query, bench, export
  fado/                  public Go API (small surface, see Go API)
  internal/formula/      lexer, parser, binder, bytecode, vm, sqlgen (dialects), catalog
  internal/model/        class definitions, layouts, variables, defaults, validation, migrations
  internal/store/        sqlite store, history, child tables, events, outbox, schema
  internal/state/        statechart engine
  internal/rules/        ECA rules, built-in actions, outbox worker
  internal/auth/         users, groups, tokens, predicate compilation
  internal/query/        LOOKUP/QUERY compilation, filter DSL, SQL endpoint, authorizer
  internal/server/       http, websocket, command dispatch
  api/                   openapi.json, json schemas for class, layout and wire documents
  analytics/             duckdb module: projection, native tables, attach, mining views
  postgres/              postgres dialect and driver (separate module)
  cabi/                  c-shared build and header (lite build)
  web/vm/                TypeScript VM, zero dependencies
  web/renderer/          reference frontend
  clients/ts/  clients/py/
  conformance/           formula and statechart cases run by all targets
  bench/                 perf suite, dataset generators, baselines
  docs/                  this document, adr/, perf/, dependencies.md, function reference
```

Separate Go modules for `analytics/` and `postgres/` keep cgo and drivers out
of the core `go.mod`.

---

## Out of scope for 1.0

Parallel statechart regions; BPMN; multi-tenancy inside one database file
(run one file per tenant); replication and clustering; WASM plugin actions;
a visual class designer; a decimal number type; OCEL export; nested
composites (rows inside rows, arrays of arrays); raw SQL inside formulas;
attachments and comments (planned as side objects after 1.0); per-document
layouts.
