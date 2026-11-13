# FadoDB architecture

Status: design, revised 2026-11-06 with the document model (variables,
commits, recalculation, document tables, explicit tables, DuckDB as the
analytical engine, class metadata, layouts, the wire format). Of what is
described below, the formula engine exists as of v0.1.0, with the
exceptions its section lists; the rest is design. See
[ROADMAP.md](../ROADMAP.md) for what exists today and [perf/](perf/) for
the measured numbers. Decisions with their reasoning are in [adr/](adr/).

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
    "owner":    {"type": "text", "default": "USER()"},
    "discount": {"type": "decimal", "scale": 2, "default": "0"},
    "shipping": {"type": "object", "schema": "Address"},
    "contact":  {"type": "object", "schema": "Person"},
    "scan":     {"type": "object", "schema": "File"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "number", "price": "number"}},

    "subtotal":  {"formula": "SUM(lines.qty * lines.price)"},
    "ship_city": {"formula": "shipping.city"},
    "total":     {"formula": "ROUND(subtotal * (1 - discount), 2)"},
    "rate":      {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"},
    "total_eur": {"formula": "ROUND(total * rate, 2)"},
    "overdue":   {"formula": "TODAY() > due AND STATE() <> \"paid\"",
                  "mode": "triggered", "on": ["enter:approved", "refresh"]},
    "backlog":   {"type": "number",
                  "formula": "LOOKUP(Invoice, COUNT(id), customer = THIS.customer AND state <> \"paid\")",
                  "mode": "triggered", "on": ["refresh", "enter:submitted"]}
  },
  "states": {
    "initial": "draft",
    "list":    ["draft", "submitted", "approved", "paid", "cancelled"],
    "final":   ["paid", "cancelled"]
  },
  "transitions": [
    {"name": "submit",  "from": "draft",     "to": "submitted", "guard": "total > 0"},
    {"name": "approve", "from": "submitted", "to": "approved",  "guard": "INGROUP(\"finance\") AND NOT ISBLANK(scan)"},
    {"name": "pay",     "from": "approved",  "to": "paid"},
    {"name": "cancel",  "from": ["draft", "submitted"], "to": "cancelled"}
  ],
  "rules": [
    {"on": "enter", "state": "approved", "if": "total_eur > 10000",
     "do": [{"action": "http.call",
             "args": {"url": "\"https://hooks.example/approved\"",
                      "body": {"id": "ID()", "total": "total_eur"}}}]},
    {"on": "enter", "state": "paid",
     "do": [{"action": "create", "class": "Receipt",
             "values": {"invoice": "ID()", "amount": "total"}}]},
    {"on": "transition", "name": "cancel",
     "do": [{"action": "reset", "variables": ["discount"]}]}
  ],
  "access": {
    "read":   "INGROUP(\"finance\") OR owner = USER()",
    "create": "INGROUP(\"sales\")",
    "update": "STATE() = \"draft\"",
    "transition": {"approve": "INGROUP(\"finance\") AND owner <> USER()"}
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
| `number` | float64 | Excel arithmetic semantics (ADR-0004); for embedders that want a spreadsheet's numbers |
| `decimal(s)`, `int` | int64 mantissa at a scale of 0 to 18 places | Exact arithmetic or the error `overflow`; `int` is `decimal(0)` (ADR-0022) |
| `money(s)` | a decimal with a currency code | Currencies must agree in one operation; stored as two columns, written `"12.30 PLN"` (ADR-0022) |
| `text` | UTF-8 string | A `values` list makes it an enum, refused outside the list at commit (ADR-0023) |
| `date` | int32 days since 1970-01-01 | A calendar day with no clock and no zone (ADR-0023) |
| `datetime` | int64 seconds UTC | An instant; `DATEOF` and `DATETIME` convert (ADR-0023) |
| `ref` | class and id | Typed reference to another document |
| `json` | opaque JSON text | Structured data the engine does not interpret; read through `JSONVALUE` only |
| `array<scalar>` | ordered list of one scalar type | Short lists the document owns, such as tags |
| `object<Schema>` | one record of a named schema, one typed column per field | A person, a place, a file: the engine ships a vocabulary (ADR-0032) and a class author defines more (ADR-0031) |
| `rows<schema>` | ordered rows with named columns, scalars or objects | Embedded tables such as invoice lines; also what `QUERY` returns |

A **schema** is a named list of fields, each a scalar or another object,
never a collection; `object<Schema>` holds one record and `rows<Schema>` a
collection of them. Objects nest, collections do not: no collection sits
inside a schema or inside another collection. An object is stored flat, one
typed column per field named by its path (`shipping__city`), so every field
is indexable, usable in a read rule and visible in history without JSON; the
compiler turns `shipping.city`, `ISBLANK(contact)` and the equality of two
`Person` values into operations on those columns, and no evaluator ever
sees an object value. An object is a value, not a reference: a `Person` on two documents is
two copies, its `id` is any text the embedder understands, and `ref` remains
the pointer to a document. The engine ships `Person`, `Organization`,
`Address`, `GeoPoint`, `Place`, `Period`, `Event`, `Quantity` and `File`,
each with a presentation a layout renders as a chip, a card or coins; a
class author defines more, or extends one (ADR-0031, ADR-0032). Values are
immutable; assigning a field or an element produces a whole new value in a
new commit. Composite variables are meant to hold up to a few hundred
elements. The soft limit is 1,000 elements with a definition-time warning,
the hard cap is 10,000; larger data belongs in an explicit table behind a
`ref`.

A text value, stored or computed, is at most the database's text limit,
65,536 code points by default (ADR-0021). A formula that would produce more
stops with `out_of_range` before building it, on every evaluator; the limit
is chosen when the database is created and travels with it, so that a
commit recomputes as it was committed wherever the database is opened.

A `json` variable is the escape hatch for data without a typed shape yet:
settings or a payload from another system. It is stored as JSON
text, compared as text, and reachable in formulas only through
`JSONVALUE(value, path)`, a pure function that extracts one scalar by a path
such as `"$.city"`. It cannot be indexed, cannot appear inside `array` or
`rows`, and no formula produces one. A `json` variable that grows a
`JSONVALUE` for every field is a sign that it should become typed variables,
an object, a `rows` variable or an explicit table.

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
  `STATE_ENTERED_AT`, `CREATED_BY`, `UPDATED_BY`, `STATE_ENTERED_BY` and
  `STATE_ENTERED_FROM`. `TITLE` comes from the class's `title` formula
  and equals `ID` when the class declares none.

Names are identifiers, resolved case-insensitively in formulas and shown as
declared in tables and views. The columns the generated tables and views add
are reserved and rejected as variable and schema column names at definition
time: `id`, `title`, `state`, `version`, `created_at`, `updated_at`,
`state_entered_at`, `created_by`, `updated_by`, `state_entered_by`,
`state_entered_from`, `valid_from`, `valid_to`, `actor_id`, `event_id`,
`doc_id`, `idx` and `value`; a name may not end in `_currency`, the suffix of
a money variable's currency column. A schema field reserves only the child
table's own columns, `doc_id`, `version`, `idx` and `value`, since fields
appear prefixed by their path; `__` is reserved in every name as the path
separator (ADR-0031).

### Formulas

Excel-like syntax, case-insensitive functions, the operator set
`+ - * / \ ^ % &`, comparisons, `AND OR NOT`, `IF(...)` and
`IF ... THEN ... ELSE`. The function catalog is a JSON file that generates
documentation, editor hints and conformance tests. Functions fall into
classes, and where a formula may use them depends on what happens to its
result:

| Class | Examples | Allowed in |
|---|---|---|
| Pure | `IF IFERROR ROUND SUM LEFT DATE ADDMONTHS DECIMAL MONEY JSONVALUE` | everywhere |
| Context | `USER() INGROUP() NOW() TODAY() STATE() ID()` | defaults, triggered formulas, guards, rules, access rules, layouts; not in auto formulas |
| Lookup | `LOOKUP(source, column, condition)` | everywhere; in auto formulas only against explicit tables |
| Query | `QUERY(source, columns, condition, sort, groupby, limit)` | defaults, triggered formulas, rules, layouts; not in auto formulas |
| Secret | `SECRET("token")` | the request of an action definition, and nowhere else (ADR-0045) |

The rule behind the table: the stored value of an auto variable must be a
function of the document itself, so it may not depend on who is looking,
on the clock, or on data outside the document beyond a keyed lookup.

Among the context functions are those of the document's own life
(ADR-0047). The functions of the row run today: `CREATEDAT()` and
`CREATEDBY()`, `UPDATEDAT()` and `UPDATEDBY()`, `ENTEREDAT()`,
`ENTEREDBY()` and `ENTEREDFROM()` for the state it is in, and
`VERSION()`. So "seven days in this state" is
`NOW() >= ADDDAYS(ENTEREDAT(), 7)` and four eyes is
`CREATEDBY() <> USER()`. The document keeps who created it, last changed
it and moved it into its state, and the state it came from, as the
system variables `created_by`, `updated_by`, `state_entered_by` and
`state_entered_from`, on the document, in its history and in the views.
Every event that enters a state says how the move came about, under
`origin`: `command`, `action`, `guard` or `timer`, and the event of a
creation names the state the document was created in.

The functions of a named state run today too. They take the name of a
state: `FIRSTENTERED`, `LASTENTERED`, `FIRSTLEFT` and `LASTLEFT`, each
with a `...BY` beside it for who; `ENTRIES`, how many times the document
entered the state, and `DAYSIN`, the days it spent there in all; and
`FIRSTEDITOR` and `LASTEDITOR`, who first and last changed it there. A
transition back into the same state is an entry. An editor is who
created the document in the state or changed it there, not who moved it
in or out. A name written as text is checked against the class's states
when the class is defined. They are read from the document's own events
once for a command, with the step the command is making: `ENTRIES` on
the entry of a state counts that entry. A read rule, a read right and a
filter are refused them, since those run for every row of a query; the
`_states` view has the same facts for every document.

Inside `LOOKUP` and `QUERY`, bare names are columns of the source and the
document's own variables are reached as `THIS.name`. Columns may be
aggregates (`COUNT(id)`, `SUM(amount)`) and `groupby` names the grouping
columns. A source is an explicit table or a class such as `Invoice`; the
history of a class as a source waits. The compiler translates the call to
the SQL dialect of the storage that holds the source, binds the condition
to its columns, and rejects anything else at definition time. Formulas
never carry raw SQL. A formula that reads the documents of a class sees
every one of them, whoever caused it to be computed; who may see the
result is decided by the rights on the variable that holds it (ADR-0046).
Today `LOOKUP` reads explicit tables and the documents of a class, with
one column or with `COUNT`, `SUM`, `MIN` or `MAX` over one expression of
the columns; `QUERY` is designed and not built yet.

A class as a source is its documents as they are now: the system columns
`id`, `title`, `state`, `version`, `created_at`, `updated_at`,
`state_entered_at`, `created_by`, `updated_by`, `state_entered_by` and
`state_entered_from`, and every scalar variable, ordered by `id`. Inside
the call `ID()` and `STATE()` are the reading document's own, so
`id <> ID()` leaves it out. Such a formula may stand in a triggered
variable, a default, a guard, a rule or an access rule other than
`read`, and not in an auto formula. It reads what was stored before the
command began, and nothing recomputes it when the other documents
change: the author says when, usually with a rule of the other class
that refreshes this document. A variable computed this way declares its
`type`, so that its documents keep their layout when the other class
changes, and a redefinition of a class may not break a formula of
another class that reads it. The lookup is one statement on the writer,
inside the commit, narrowed through the index of a variable declared
with `"index": true` when the condition compares one for equality:
10.7 µs to count one purchase's approvals among 100,000.

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
3. evaluates the requested transition's guard and moves the state;
4. recalculates **triggered** variables whose trigger fired in this commit
   (`enter`, `exit`, `transition`, `refresh`);
5. runs the rules whose triggers the commit raised, in a fixed order:
   synchronous actions change the document now, the rest go into the
   outbox;
6. writes the current row, the history row, the child rows and the events.

The eventless transitions are then evaluated over the document the rules
left, bounded to eight per command. Each one that fires is a commit of its
own that repeats steps 4 to 6 with its own triggers. A rule's action
raises no trigger, so one command runs at most nine commits and each rule
at most once in each (ADR-0040, ADR-0043). Today steps 4 and 5 run: the
synchronous actions `set`, `reset`, `refresh` and `emit` change the
document in the commit, and the engine's own actions on other documents
and tables and the actions the host program defines go into the outbox,
an action sent as an HTTP request among them.

A triggered variable keeps its value between its triggers and carries the
moment it was computed: the version of the document whose commit computed
it, the time of that commit, and the code of a recomputation that failed
since, which leaves the value as it was and refuses nothing. The moment
is two columns beside the value, `<name>__asof` and `<name>__error`; the
views add the time as `<name>__at`, and a document carries all three
under `computed`. Such a value is consistent as of that moment, and the
document says which.

Triggered variables are also to recalculate on `table:<name>` when the
named table or stack changes, and on `schedule:<spec>`. Those recomputes
are **set-wise**: one query per class computes the new value for every
affected document, and the results are applied as system commits, never
one query per document. They arrive with the set-wise work of Phase 8;
until then a class that names either trigger is refused.

The browser evaluates auto formulas live while a user edits, from the same
bytecode, so the user sees totals change before saving. The server's result
on commit is authoritative; the conformance suite guarantees the two agree.

### Statechart

- Flat named states, one initial, optional final states, which are
  terminal: a document in one is frozen and may only move to another
  terminal state (ADR-0039). Hierarchical states in 1.x.
- A transition has a name, source states, a target, an optional guard formula,
  and optional entry/exit effects. Transitions are the only way state changes.
- **Eventless transitions** have a guard and no name, and are evaluated after
  every value change with a per-commit bound of eight. Each one that fires
  is a commit of its own in the same transaction, so every state visited
  has its history row (ADR-0040).
- Exit and entry snapshots are the history rows before and after the
  transition; the transition event references both.
- A transition is a command with the assignments it carries: one commit,
  one event. A transition that cannot happen is refused before anything
  is written with `unknown_transition`, `wrong_state`, `guard_false`,
  `too_many_transitions` or `terminal`, and the open transitions of a
  document are listed on request (ADR-0040).
- A class without states is a stateless application: it is evaluated
  with a preview and stores no documents (ADR-0040).
- What is due is listed on request (ADR-0047): `Due` answers which
  documents of a class a commit would move if it touched them now, or
  at a moment given with `At`, each with its state and the state it
  would move to. A document becomes due when the guard of an eventless
  transition comes to hold with no commit of its own: the clock passed
  a date, or a document or a table the guard looks up changed; and when
  the time of a timed transition comes, for which the list says since
  when. A guard
  that translates to SQL runs as one filter over the documents of its
  state; a guard that looks another document up or calls a function of
  a state is evaluated document by document. The list moves nothing: a
  commit that touches the document does, and a refresh is one.
- A transition may carry its time (ADR-0047): `"after": "30d"`, a
  length of time in the state as a whole number and `d`, `h`, `m` or
  `s`, or `"at"`, a formula over the document that gives the moment, a
  date, which means the first instant of that day in UTC, or a
  datetime; a blank `at` never comes. It needs neither a name nor a
  guard, and a guard beside it is still asked. Its meaning is that of
  an eventless transition whose guard also says the time has come: it
  fires in the chain of any commit made once its time has come, in the
  order of the class with the eventless ones, and its event has the
  origin `timer`. With a name it raises the rules of that name, and a
  command may take it before its time. A transition back into the same
  state is an entry like any other, so an `after` on it repeats, which
  is how a reminder is written; an escalation that reminders must not
  push back is an `at` from a moment that does not move,
  `ADDDAYS(FIRSTENTERED("waiting"), 14)`. An `at` may not read the
  clock, the user, another document or a table. `Transitions` shows a
  timed transition with `fires_at`, and `Due` lists the documents whose
  timer has come, each with `since`.
- The engine makes that commit itself when the time comes, with the
  wake-up (ADR-0047). A commit that leaves a document in a state queues
  a row of the outbox for each timed transition leaving it, due at its
  moment, and a commit that changes a date an `at` reads queues one for
  the new moment. When a row is due the worker touches the document:
  what fires in the chain is written, and when nothing is to do, because
  the document has left the state or the stay, the moment has moved to
  later or a guard does not hold, the row is done and nothing is
  written. The touch and the mark are one transaction, so a timer fires
  exactly once; and since any commit fires a timer that has come, one
  the worker missed fires at the next commit. A wake-up acts as the
  person whose commit queued it: who moved the document into the state,
  or who last changed the date. It stands outside the order of its
  document's other actions, and is listed with them as the action
  `timer`. A simulation is a copy of the database opened with
  `Options.Now` and a `Drain`.

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
                          actor_id, before_version, after_version, cause_event_id,
                          command_key (unique, the caller's idempotency key),
                          business_date (a calendar day the caller gives)
event_docs                every document an event touched            (event_id, class, doc_id, qualifier)
```

Every variable is a typed column in the current and history tables, an
object one column per field named by its path (`shipping__city`). Every
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
| `Invoice_states` | stay in a state, one for every entry, a return into the same state too: `state`, `entered_at`, `left_at`, `duration`, entry and exit versions, and for the entry and for the leaving who made the move, by which transition and how, with the state come from (ADR-0047) |
| `Invoice_events` | event touching the class |
| `Invoice_lines` | element of the composite variable `lines`, current version |
| `Invoice_lines_history` | element per version |

"As of" queries are a range filter on `Invoice_history`. Process mining reads
`_states` and `_events` and never touches JSON.

### Rules and actions

Triggers: `create`, `values_change(variable...)`, `exit(state)`,
`enter(state)`, `transition(name)`, `delete`, and `schedule` (0.x). Each rule
has an optional condition formula and a list of actions. Which triggers a
commit raises is fixed by what the command did (ADR-0043): `create` then
`enter` of the initial state for a create; `values_change` for a set;
`values_change`, `exit`, `transition`, `enter` for a transition; `exit`
and `enter` for an eventless transition, and `transition` between them
for a timed one that has a name; `delete` for a delete. Rules run
trigger by trigger in that order and in declaration order within one. A
condition that errors is FALSE; an action that fails refuses the whole
command, with the path of the action. A rule on `schedule` is refused
until the phase that runs it.

| Action | Sync or outbox | Notes |
|---|---|---|
| `set` variables on the same document | sync, same transaction | Deterministic, re-triggers recalculation |
| `reset` input variables to their defaults | sync | Re-evaluates the named `default` formulas |
| `refresh` triggered variables | sync | Fires the `refresh` trigger for the named variables |
| `emit` custom event | sync | An event of kind `custom` with the name the rule gives; for integrations and subscriptions |
| `create` document | outbox | Idempotency key = event id + action index |
| `transition` document | outbox | Same key |
| `table.insert / update / delete` | outbox | Explicit tables, see below |
| a defined action, such as `books.post` or `slack.post` | outbox | Its definition is stored with `DefineAction`; a Go function of the host program implements it, or the engine sends its `request` as HTTP (ADR-0045) |
| `http.call` | outbox | One request written in the rule: `url`, `method`, `headers` and `body` under `args`; retried when there is no answer, dead when refused |

Outbox execution results are events (`action_run`, `action_failed`), so the
audit trail covers side effects. `reset` and `refresh` are also commands a
frontend can send directly, which is what a button does.

Today `set`, `reset`, `refresh` and `emit` run inside the commit, and
`Preview` reports every rule a command would run with the evaluated
arguments of its actions. `Reset` and `Refresh` are commands of the Go
API, each a commit with an event of its own kind: a `Reset` is the
caller's write and is checked against the write rights; a `Refresh`
always commits and the eventless transitions are evaluated after it,
which is how a caller makes a document look at the clock.

The outbox runs today with the engine's own actions (ADR-0044):
`create`, `transition`, `refresh` of another document and the `table.*`
actions. The commit evaluates the action's arguments and writes a row in
its own transaction; a worker, or `Drain` when the embedder asks for it,
does what the action stands for, marks the row and appends `action_run`
to the causing document in one transaction, so the action happens exactly
once.
It runs as the person whose command queued it: what that person may not
do is a refusal, the row is dead at once with the code, and the causing
document gets `action_failed`. A failure of the store is tried again
with a doubling wait. `Outbox` lists the rows, `RetryAction` makes a
dead one pending again, and the view `<Class>_actions` shows them to
SQL. A chain of actions stops sixteen deep. A `create` with `each` and
`as` makes one document for each element of a list, each a row of its
own. A `transition` moves the document its `id` names. A `refresh` with
an `id` sends `Refresh` to another document, which then moves by its own
guards: a child wakes its parent without knowing the parent's
transitions. A table action inserts, updates or deletes one row by its
key. What an action names, the class, the transition, the variables, the
table and its columns, is checked when the rule's class is defined, and a
redefinition that would break such a rule is refused until the rule is
changed.

Actions are an extension point (ADR-0045). Beside the engine's own, a
rule may name an action that has a definition: JSON, like a class, with
a name of a namespace and a verb, parameters and returned values by
scalar type. `DefineAction` stores it with its version, `Actions` lists
every action a rule may name, and a rule gives the arguments as formulas
under `args`, typed against the parameters when the class is defined:

```json
{"name": "books.post", "label": "Post to the books",
 "params": {"invoice": {"type": "text", "required": true}, "amount": "money(2)"},
 "returns": {"entry": "text"}}
```

```json
{"on": "enter", "state": "approved",
 "do": [{"action": "books.post", "args": {"invoice": "ID()", "amount": "total"}}]}
```

What implements a definition lives outside the engine, in one of two
places. The first is a Go function the host program gives to `Open`
under the action's name (ADR-0027). The worker calls it after the commit, outside any
transaction, with the key of the queued action, the attempt, the
arguments and the event that fired the rule, the document at that
version included; the JSON of that call is published under `api/`. No
error is `action_run`; an error is tried again with a doubling wait; an
error marked permanent is a refusal and the action is dead at once. The
call happens at least once, each time with the same key, so the
function makes twice once by the key. Up to `Outbox.Workers` such calls
run at once, and none of them holds back a commit or the engine's own
actions of other documents. `PreviewAction` shows the call for given
values and `TryAction` runs an action once outside any document.

The second is no code at all: a definition that carries a `request`,
which the engine sends as HTTP.

```json
{"name": "slack.post",
 "params": {"channel": {"type": "text", "required": true}, "text": {"type": "text", "required": true}},
 "returns": {"ts": "text"},
 "connection": {"name": "slack", "secrets": ["token"]},
 "request": {"method": "POST",
             "url": "\"https://slack.com/api/chat.postMessage\"",
             "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
             "body": {"channel": "channel", "text": "text"}},
 "ok": "JSONVALUE(BODY, \"ok\") = \"TRUE\"",
 "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}
```

The address, the headers and every string of the body are pure formulas
over the parameters, so every attempt sends the same request; `ok` and
`result` are formulas over `STATUS` and `BODY`, the reply. A reply of
2xx for which `ok` is TRUE is the action run; no reply, 408, 429 and
5xx are tried again, no sooner than `Retry-After` asks; anything else is
a refusal. Three things keep this safe when the definition is data that
an agent may have written:

- **The network is the host program's.** A database sends nothing until
  the program opens it with `Options.Outbound`, which package
  `fado/outbound` makes from the hosts that may be called: https only
  unless a host is listed with `http://`, no redirects, no address
  inside the network unless its host is listed by name, ten seconds and
  one megabyte. A program that does not import the package links no
  HTTP client.
- **Secrets are the host program's.** `SECRET("token")` is filled in
  from `Options.Secrets` at the moment of sending, at every attempt, and
  is stored nowhere: not in the definition, the row, the event, a
  preview or an error.
- **A secret goes only to its service.** The host program names the
  hosts of each connection. A request with a connection goes to those
  hosts and to no other; a request without one reads no secret.

A request that gives no body carries the call itself, the same JSON a Go
function receives, with `Idempotency-Key`, `FadoDB-Event` and
`FadoDB-Attempt` as headers and, when the definition names a `sign`
secret, an HMAC signature in `FadoDB-Signature`. That is the whole
protocol of a developer's own action: an HTTPS endpoint in any language
that checks the signature, makes twice once by the key and answers with
JSON. `http.call` is the engine's own declared action, for one request
written in a rule, to the hosts listed for requests without a
connection.

The answer comes back. An action of the outbox may carry `into`, a map
from input variables of the rule's own document to formulas over what
the action returns, with `OK` and `ERROR`:

```json
{"on": "enter", "state": "charging",
 "do": [{"action": "pay.charge", "args": {"amount": "total"},
         "into": {"payment_id": "payment", "pay_error": "ERROR"}}]}
```

When the action is done or dead, the engine writes those values to the
document as a set of its own, in the transaction that marks the action,
with an event caused by the action's outcome. It is the engine's write,
so no write right is consulted. Like any set it raises `values_change`
and is followed by the eventless transitions: a transition guarded by
`NOT ISBLANK(payment_id)` moves the document on from the answer, and one
guarded by `NOT ISBLANK(pay_error)` from the failure. A `create` returns
the `id` of the document it made and a `transition` the `state` and the
`version` it left its document in. A document that is frozen or gone
when the result arrives keeps what it has, and the queued action says
why.

[`docs/actions.md`](actions.md) is the guide to writing a definition,
and `examples/actions/webhook.json` a generic webhook. Definitions for
particular services are not part of the repository: by the owner's
ruling they come with real needs, and the format for publishing several
definitions together waits for the first of them.

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

Today a table is defined in JSON, `{"name": "Rates", "columns": {"code":
"text", "rate": "decimal(6)"}, "key": ["code"]}`, stored as `tbl_Rates`
with the view `Rates`, filled by the Go API's `ImportRows`, and read by
`LOOKUP` in auto formulas and defaults; a rule changes one row with
`table.insert`, `table.update` and `table.delete` (ADR-0044); only the
`sqlite` binding exists; the other bindings come with the analytical
engine, and `QUERY` with the first layout or report that needs a list of
rows (ADR-0046). A lookup
whose condition is one equality of a column with an expression of the
document is answered from an in-memory index while the table has at most
`MaxIndexedRows` rows, and through a prepared statement narrowed by the
table's index above that (ADR-0036). When such an equality is one term
of an `AND`, and for every aggregate, the statement is narrowed by it
and the condition decides among the rows of that key; a condition with
no such equality runs over the table (ADR-0046). An aggregate is
computed by SQLite's own aggregate functions, so nothing crosses to Go
per row, except a `SUM` of plain numbers, whose rows go one by one, in
key order, to the function the Go VM adds with.

### Security

- The principal comes from the embedder on every call: an id, groups, an
  admin flag and its business day (ADR-0027). The engine keeps no user
  tables; an optional package of users, groups and API keys turns a
  credential into a principal for `fadodb serve`.
- Access rules per class: `read`, `create`, `update`, `delete`, and per-transition rules. Tables have `read` and `write` rules.
- A class may declare `rights` per state and variable, a table of ticks:
  a variable whose `write` rule is FALSE in the document's state cannot be
  set, one whose `read` rule is FALSE is absent from the wire document, the
  change stream and query results; the SQL endpoint and the views stay
  row-level (ADR-0028).
- `read` compiles to a SQL predicate appended to every query, view and
  subscription filter. A formula that reads the documents of a class is
  not a caller: it sees every document, and the rights on the variable
  that holds the result decide who sees it. Whoever may define a class
  can therefore read through it, and the host program decides who that is
  (ADR-0046).
- `create`, `update`, `delete` and transition rules run in the VM at commit time.
- Admins bypass rules. Everyone else gets exactly what the rules say.
- Today the Go API enforces the per-transition rules and the rights table
  (ADR-0041): a transition's rule and the write rights are evaluated over
  the stored document before the assignments, every document returned is
  projected for the principal, and a filter compiles the read right of
  the variables it names into its predicate. The class-level `read`,
  `create`, `update` and `delete` rules are enforced too, on the records
  of Phase 5 as proposed (ADR-0049): a document its caller may not see
  is answered as one that does not exist by every call that names it,
  and left out of a filter, a count and the list of what is due, the
  read rule being one more predicate; a caller who sees a document and
  may not do what they ask is refused with `forbidden` and the place of
  the rule. A named transition without a rule of its own asks `update`.
  What the engine does by itself asks nothing. A rule a class leaves
  out lets every caller through, until the owner rules on the question
  ADR-0049 puts to him. Not built yet: the kind of actor (ADR-0050),
  who may define, operate and run SQL, and the rules of a table
  (ADR-0051). The change stream comes with the server in Phase 6. A
  call without a principal is nobody.

### Querying

Three paths, all with the caller's predicates injected:

1. `LOOKUP` and `QUERY` inside formulas, compiled, cached, portable.
2. A JSON filter DSL (`where`, `order`, `limit`, `fields`) for clients.
3. A read-only SQL endpoint over the views above, for analysts and BI tools.
   The SQLite authorizer restricts statements to `SELECT`; in the full build
   the same views exist in DuckDB.

Today the Go API has the second and the third (ADR-0038): `Query` takes a
class, a `where` formula over the class's variables and the system
columns, an `order`, a `limit`, an `offset` and the `fields` to return,
and answers documents in the wire shape; `Count` counts; `SQL` runs one
`SELECT` over the views on a connection whose authorizer refuses the
physical tables, writes, pragmas and attachments, at most 10,000 rows. A
filter means what the same expression means in a formula, and beside that
exact predicate the compiler adds, for each comparison of a column with a
literal, the comparison on the bare column so that SQLite uses the index,
and the same for a date, a datetime or a number compared with an operand
that reads no document, as in `due < TODAY()`;
a repeated filter runs a cached prepared statement, so it pays SQLite's
parse once. The read right of every variable a filter's `where` or `order` names is
compiled into its predicate for the principal, and the documents come
back projected (ADR-0041). The read rule of the class joins the
predicate of every `Query` and `Count` for a caller who is not an
administrator (ADR-0049), written by the same generator from the
formula the Go VM runs for a read by id. The caller's part of the rule
is decided first: an `INGROUP` of a group has one answer for the whole
statement, so `owner = USER() OR INGROUP("finance")` adds nothing for a
member of finance and is `owner = USER()` for anybody else, a comparison
the index on `owner` answers; a statement is kept for each outcome. The
rule offers its comparisons to an index only where the caller's own
condition offers none, since SQLite has no statistics to choose between
two. The SQL endpoint adds no rule
and reads every row for every caller, until ADR-0051 says who may use
it.

### Layouts

Visuals live at class level, never per document. A class has any number of
named **layouts**, each a JSON document that binds components to variables:
`form`, `list`, `card`, `print`, or whatever a frontend needs. `visible`,
`enabled`, `editable`, `style` and templates are formulas evaluated live in
the browser VM; a `rows` variable renders as a grid, an object as a chip or
a card and a collection of objects as coins (ADR-0032). A layout that looks
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
    {"type": "chip",    "variable": "contact"},
    {"type": "field",   "variable": "scan", "editable": "STATE() = \"draft\""},
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

Attachments are `File` values: a variable of type `object<File>` or
`rows<File>` holds the hash, name, media type and size, attaching is a
commit, and the bytes live behind a blob store, in the database file, in a
directory beside it or in the embedder's own store (ADR-0033). Comments are
neither variables nor visuals; they are planned after 1.0 as side objects
keyed by class and document id, with their own events.

---

## Formula engine

Implemented in v0.1.0 (Phase 1): the lexer, the parser, the binder with
static types and the function-class check of the evaluation mode,
bytecode format v1 with its binary and JSON forms, the Go VM, the SQL
generator with the SQLite and DuckDB dialects, the catalog, the
conformance suite with its property tests, and `fadodb formula eval`.
Since then, unreleased: the exact types `decimal`, `int` and `money`
(ADR-0022), `date` as a calendar day beside `datetime` (ADR-0023),
division by zero as an error and `IFERROR` (ADR-0024), constant folding of
literal expressions, and bytecode format v2.
Since Phase 2 started: object field paths, a leaf of an `object<Schema>`
variable read as one scalar slot (ADR-0031), a pure-only binding mode
for titles and presentation formulas, in `internal/model` the class
definition compiled with the dependency graph of its formulas, cycle
detection and the evaluation order, and in `internal/store` the commit
that runs defaults and the auto recalculation over the stored row and
writes the current, history and child rows and the event. Not yet: whole-object operations,
composite values (arrays and rows) and the query functions, which arrive
with the document model in Phase 2; the browser VM (Phase 7); the
Postgres dialect (Phase 10). The pipeline and the value struct
below are the design: today's `Value` holds a kind, a scale, a number
and a text, and an array is a column-wise value beside it that no
operation consumes yet.

### Pipeline

```
source → lexer → parser (AST) → binder (names→slots, types, DAG, cycles, function-class checks)
       → optimizer (constant folding) → bytecode
bytecode → Go VM          (typed tagged stack, zero allocations on the scalar path)
bytecode → SQL generator  (SQLite, DuckDB, Postgres dialects; used by LOOKUP/QUERY,
                           set-wise recompute, access predicates and views)
bytecode → browser VM     (TypeScript, zero dependencies, shipped as one plain JavaScript file)
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
    kind  Kind      // Null, Bool, Number, Decimal, Money, Text, Date, DateTime, Ref, JSON, Array, Rows
    scale int8      // Decimal, Money: digits after the point
    num   float64   // Number, Date (days), DateTime (seconds), Bool (0/1); the mantissa bits of a Decimal or Money
    str   string    // Text, Ref, JSON; the currency of a Money
    list []Value   // Array
    rows *Rows     // Rows: column names, types, and column-major values
}
```

Scalars never allocate. The VM keeps a fixed-size value stack per execution
and a call-free instruction loop. Row-wise expressions over `rows`, such as
`SUM(lines.qty * lines.price)`, compile to a loop over the rows with the
scalar path inside.

Objects never reach the VM: the compiler scalarizes field paths, object
blankness and object equality into operations on leaf columns (ADR-0031),
so the `Value` struct has no object kind.

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
- Schema: `classes`, `class_versions`, `schemas`, `layouts`, per class the
  current, history and child tables, `events`, `event_docs`, `outbox`,
  `users`, `groups`, `memberships`, `api_keys`, `sessions`,
  `tables_registry`, `schema_version`, `blobs` when the blob store is the
  database file, plus explicit tables bound to `sqlite`.
- Indexes declared per variable (`"index": true`), plus `(class, doc_id, ts)`
  on events and `(id, valid_from)` on history.

SQLite is the transactional store by measurement (ADR-0035): the Phase 2
benchmark wrote 100,000 commits into SQLite and into DuckDB, and DuckDB's
cost per statement, not per fsync, put a small commit at 10 ms against
SQLite's 0.06 ms. The store opens the file as above, with
`wal_autocheckpoint` off and a checkpointer on its own connection, so that
a commit never pays a checkpoint; every statement is prepared once on the
pinned writer connection, and a commit is one `BEGIN IMMEDIATE`
transaction. `synchronous`, the text limit, the blob limit and the blob
store's kind are properties of the database, chosen when it is created
and kept in its `metadata` table. Attachments live behind the `BlobStore`
of ADR-0033: the `blobs` table by default, a directory beside the file,
or the embedder's store; `PutBlob` returns the `File` fact a commit sets,
a commit that names a blob the store does not hold is refused, and
`OpenFile` serves a blob through the document that holds it.

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

### Class versions and migrations

A redefinition of a class is a new class version and touches no document
(ADR-0039). Every current and history row records the class version that
computed it, and a document is read under that version. A document in a
terminal state, one listed in `states.final`, is frozen: its values, its
computed values and its class version never change, and it may only move
to another terminal state. A live document adopts the current version at
its next commit, which the current definition computes; `Migrate` does
that for every live document of a class, in batches, on purpose. A
redefinition may add variables, change formulas and constraints, add
states and change behaviour; it may not remove a state documents are in,
make a terminal state live again while documents are in it, remove a
variable that holds values, or change the type of a variable that ever
held one. History and events are never rewritten.

---

## Protocol and embedding

### Go API

```go
out, err := outbound.Allow(outbound.Options{Connections: map[string][]string{"slack": {"slack.com"}}})
db, err := fado.Open("app.fado", fado.Options{
    Actions: map[string]fado.ActionFunc{"books.post": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
        return post(ctx, call.Key, call.Args, call.Event)
    }},
    Outbound: out,
    Secrets:  func(ctx context.Context, connection, name string) (string, error) { return vault.Get(ctx, connection, name) },
})
act, err := db.DefineAction(ctx, actionJSON)
cls, err := db.DefineClass(ctx, classJSON)
err  = db.DefineLayout(ctx, "Invoice", layoutJSON)
p := fado.Principal{ID: "alice", Groups: []string{"finance"}}
doc, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Customer:42"}, fado.As(p), fado.Key("ksef-2026-0042"))
doc, err  = db.Set(ctx, "Invoice", doc.ID, fado.Values{"discount": "0.10"}, fado.As(p))
doc, err  = db.Transition(ctx, "Invoice", doc.ID, "approve", nil, fado.As(p), fado.BusinessDate(day))
open, err := db.Transitions(ctx, "Invoice", doc.ID, fado.As(p))
due, err  := db.Due(ctx, "Invoice", fado.As(p))
doc, err  = db.Refresh(ctx, "Invoice", doc.ID, []string{"backlog"}, fado.As(p))
preview, err := db.Preview(ctx, "Invoice", doc.ID, fado.Values{"discount": "0.20"}, "approve", fado.As(p))
history, err := db.History(ctx, "Invoice", doc.ID, fado.As(p))
rows, err := db.Query(ctx, `SELECT id, total FROM Invoice WHERE state = 'approved'`, fado.As(p))
sub  := db.Subscribe(ctx, fado.Filter{Class: "Invoice"}, fado.As(p))
```

The principal is the embedder's (ADR-0027); every mutating call takes an
optional idempotency key and a business date (ADR-0026); `Preview` runs the
commit pipeline and writes nothing (ADR-0025); a Go function registered
for an action receives the event that fired the rule from the outbox
(ADR-0027, ADR-0045). Today the `fado`
package has `Open`, `DefineClass`, `ValidateClass`, `DefineClasses` and
`ValidateClasses` for classes that name each other, `DefineSchema`,
`DefineTable`, `ImportRows`, `TableRows`, `Create`, `CreateMany`, `Set`,
`Reset`, `Refresh`, `Transition`, `TransitionMany`, `Transitions`,
`Delete`, `Migrate`,
`Drain`, `Outbox`, `RetryAction`, `DefineAction`, `ValidateAction`,
`Actions`, `Action`, `PreviewAction`, `TryAction`, `Get`, `Version`,
`History`, `Events`, `Query`, `Count`, `SQL`, `PutBlob`, `OpenFile`,
`OpenBlob`, `CheckBlobs`, and `Preview` and `PreviewDraft` of a create, a
set or a transition, and `PreviewMany` of a batch of moves; a preview, a listing or a read runs at a moment the
caller chooses with `At`, and a preview reports its moment (ADR-0042).
The principal decides what a call may set and sees
(ADR-0041): a right or a transition rule that is not TRUE for it refuses
with `forbidden`, and every document comes back projected.
`TransitionMany` moves up to 10,000 documents of a class along one
transition in one transaction, a commit each; a document that cannot
move is reported with its refusal and the others move (ADR-0048). Every refusal
is a `fado.Error` with a stable code and the variable it is about, or the
`Diagnostics` of a definition.
Layouts, `QUERY` and subscriptions arrive with their phases.

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
| `POST /v1/docs/{class}/preview`, `POST /v1/classes/validate` | Evaluate without writing: a document, a draft class, one transition; definition-time diagnostics (ADR-0025) |
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
unchanged. Dates are `"YYYY-MM-DD"`, datetimes integer seconds, exact
values text such as `"12.30"` and money `"12.30 PLN"`, refs are
`"Class:id"` text, objects nested JSON objects and `null` when every field
is blank, `json` variables embedded as they are.

```json
{
  "class": "Invoice", "id": "inv_01JC3X5M8K", "version": 7, "state": "approved",
  "title": "INV-2026-0042",
  "created_at": 1762300800, "updated_at": 1762387200, "state_entered_at": 1762387200,
  "created_by": "bob", "updated_by": "alice", "state_entered_by": "alice",
  "state_entered_from": "submitted",
  "values": {
    "number": "2026-0042", "customer": "Customer:42", "owner": "User:7",
    "issued": 1762300800, "due": 1764892800, "currency": "EUR", "discount": 0,
    "shipping": {"line1": "Długa 1", "line2": null, "city": "Gdańsk", "postal_code": "80-831", "region": null, "country": "PL"},
    "contact": {"id": "anna.k", "name": "Anna Kowalska", "email": "anna@acme.example", "phone": null, "avatar": null},
    "scan": {"hash": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", "name": "invoice.pdf", "media_type": "application/pdf", "size": 48213},
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

A budget is a target until the phase that brings its feature measures it
and publishes the result under [perf/](perf/). Phase 1 measured the
arithmetic row and the binary size row: the five-operation formula runs
in 13.7 ns with no allocation, and the core binary is 2.9 MB
([perf/v0.1.0.md](perf/v0.1.0.md)). The other rows wait for their phases.

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
  The gate runs on pull requests that change code, over the benchmarks the
  budgets name (`scripts/benchgate.sh`); every benchmark runs for a release's
  perf page.
- Each release's numbers are published under `docs/perf/`. Today they come
  from the package benchmarks and the property campaigns, run on the
  release commit by hand and reported by `benchstat`.
- The end-to-end suite is the `bench` package, written against
  database/sql so that one scenario runs on every engine. Since the
  start of Phase 2 it holds the primary-store scenario of ADR-0017 and
  the point-lookup scenario of ADR-0020; each module runs a small
  version in CI and the full campaign on request (`docs/perf/README.md`).
  Planned: the remaining scenarios of the budgets table, nightly runs,
  and CPU and allocation profiles archived per release so a regression
  can be diffed.
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
  fado/outbound/         the sender of an action's HTTP requests; only a program that allows outbound calls imports it
  internal/auth/         users, groups, tokens, predicate compilation
  internal/query/        LOOKUP/QUERY compilation, filter DSL, SQL endpoint, authorizer
  internal/server/       http, websocket, command dispatch
  api/                   openapi.json, json schemas for class, layout and wire documents
  analytics/             duckdb module: projection, native tables, attach, mining views
  postgres/              postgres dialect and driver (separate module)
  cabi/                  c-shared build and header (lite build)
  web/vm/                TypeScript VM, zero dependencies, built to one plain JavaScript file
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
a visual class designer; OCEL export; nested
composites (rows inside rows, arrays of arrays); raw SQL inside formulas;
comments (planned as side objects after 1.0); per-document layouts.
