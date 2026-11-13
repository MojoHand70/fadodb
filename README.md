# FadoDB

[![ci](https://github.com/MojoHand70/fadodb/actions/workflows/ci.yml/badge.svg)](https://github.com/MojoHand70/fadodb/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/MojoHand70/fadodb.svg)](https://pkg.go.dev/github.com/MojoHand70/fadodb/fado)
[![Go Report Card](https://goreportcard.com/badge/github.com/MojoHand70/fadodb)](https://goreportcard.com/report/github.com/MojoHand70/fadodb)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

An embeddable **active document database**, written in Go, MIT licensed.
Website: [fadodb.org](https://fadodb.org).

> **Status: Phase 4, rules and actions, is done and released as v0.4.0,
> and the command that moves many documents as v0.4.1;
> Phase 5, security, is in progress.** The Go API stores
> documents of typed classes as commits with recalculation, history and
> an event log,
> with attachments, lookups into reference tables, filters and read-only
> SQL; moves them through their statecharts with guards and eventless
> transitions; enforces field rights and transition rules for the
> principal; previews any change at a moment the caller chooses; runs
> the rules of a class inside the commit, with the actions `set`,
> `reset`, `refresh` and `emit`; and computes triggered variables at
> their triggers, each with the moment it was computed. What a rule
> does beyond its own document runs through the outbox, exactly once
> and as the person who caused it: it creates a document, or one for
> each element of a list, moves or refreshes another document, and
> changes a row of a table. A rule can also call an action that
> somebody else defined: a definition with typed parameters, stored in
> the database, implemented by a Go function of the host program or sent
> by the engine as an HTTP request, to the hosts the host program allows
> and with secrets it supplies, and what the action returns is written
> back to the document, so a process goes on from the answer. A formula
> can read the documents of another class: one value of one document,
> or a count, a sum, the smallest or the largest over those that match,
> which is how a purchase counts its approvals. A formula reads the
> life of its document too: when and by whom it was created, changed
> and moved into and out of each state. A transition may carry its
> time, `"after": "30d"` or `"at"` a date the document holds, and the
> engine moves the document by itself when that time comes, as the
> person whose commit set the timer; `Due` lists the documents that a
> guard on time would move, so the host program can touch them. One
> call, `TransitionMany`, moves many documents, a commit each: those
> that cannot move are reported with their reason, and the others
> move. The access rules of a class are enforced, who may see, create,
> change and delete a document, on the records of Phase 5 as proposed;
> the server comes with its phase. The numbers are in
> [docs/perf/](docs/perf/), the plan in [ROADMAP.md](ROADMAP.md).

## What it is

A document class declares typed variables, Excel-like formulas, a
statechart, and rules that fire actions when the state changes. FadoDB keeps
an append-only event log and a typed history row for every version,
evaluates formulas on the server and in the browser from one compiled
bytecode, exposes every document class as a queryable SQL view, and
enforces user and group access rules that are themselves formulas.
Analytics and process mining are SQL over the event log.

A class is one JSON document. This one defines, stores, computes and
moves through its states today, and its access rules, its rights and
its transition rules are enforced: finance, sales and the customer see
an invoice, and nobody deletes one. Its rule runs: paying an invoice
queues a receipt in the outbox, and the outbox creates it. The `Receipt`
class the rule names is
[examples/classes/receipt.json](examples/classes/receipt.json) and is
defined first. Formulas over `lines` arrive with composites:

```json
{
  "name": "Invoice",
  "title": "\"Invoice for \" & customer",
  "variables": {
    "customer": {"type": "text", "required": true},
    "due":      {"type": "date", "default": "ADDDAYS(TODAY(), 30)"},
    "discount": {"type": "decimal", "scale": 2, "default": "0", "min": 0, "max": "0.50"},
    "net":      {"type": "money", "scale": 2},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money"}},
    "total":    {"formula": "ROUND(net * (1 - discount), 2)"}
  },
  "states": {"initial": "draft", "list": ["draft", "submitted", "approved", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "submit",  "from": "draft",     "to": "submitted", "guard": "total > 0"},
    {"name": "approve", "from": "submitted", "to": "approved",  "guard": "INGROUP(\"finance\")"},
    {"name": "pay",     "from": "approved",  "to": "paid"}
  ],
  "rules": [
    {"on": "enter", "state": "paid",
     "do": [{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "amount": "total"}}]}
  ],
  "access": {"read": "INGROUP(\"finance\") OR INGROUP(\"sales\") OR customer = USER()", "delete": "FALSE"},
  "rights": {"*": {"discount": {"write": ["sales"]}}, "approved": {"discount": {"read": ["finance"]}}}
}
```

## Try the document store

```go
db, err := fado.Open("app.fado", fado.Options{})
_, err = db.DefineClass(ctx, invoiceJSON)

alice := fado.Principal{ID: "alice", Groups: []string{"sales"}}
doc, err := db.Create(ctx, "Invoice", fado.Values{"customer": "ACME", "net": "100.00 PLN"},
	fado.As(alice), fado.Key("order-42"))                    // the key makes a retry the same commit
doc, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"discount": "0.10"}, fado.As(alice))
fmt.Println(doc.Version, doc.Values["total"])             // 2 90.00 PLN
doc, err = db.Transition(ctx, "Invoice", doc.ID, "submit", nil, fado.As(alice))
fmt.Println(doc.Version, doc.State)                       // 3 submitted; approve needs finance

open, err := db.Query(ctx, fado.Filter{Class: "Invoice", Where: "state = \"draft\" AND total > 50.00", Order: []string{"-due"}},
	fado.As(alice))                                          // the invoices alice may see
rows, err := db.SQL(ctx, `SELECT count(*), sum(total) FROM Invoice`, nil)
history, err := db.History(ctx, "Invoice", doc.ID, fado.As(alice)) // every version, who committed it, when
preview, err := db.Preview(ctx, "Invoice", doc.ID, fado.Values{"discount": "0.50"}, "", fado.As(alice))
```

Every change is a commit: the assignments, the recalculation of the
formulas that depend on them, a typed history row and an event, in one
transaction. A transition is a commit too, its guard evaluated over the
recalculated document, and every eventless transition that follows is a
commit of its own in the same transaction; `Transitions` says which
moves are open. The principal decides what a call may set and sees: a
value a right refuses, or a transition its rule refuses, comes back as
`forbidden`, and a variable the principal may not read in the document's
state is absent from what they get back. The four rules of a class say
who may see, create, change and delete a document: a document its
caller may not see is answered as if it did not exist, by a read, a
command, a filter and a count alike, and a caller who sees it and may
not change it gets `forbidden` with the rule. A rule a class leaves out
lets every caller through until the record of Phase 5 on it is
accepted (ADR-0049). Defining, operating the outbox and running SQL,
which reads every row, are for an administrator and for the groups the
host program names when it opens the database; a host program that
names none is asked nothing, as before (ADR-0051). A table may say who
reads and who writes its rows. A
principal may say whether it is a person, an agent or a service, and
for whom it acts: every event records it, and `USERKIND()` and
`ACTINGFOR()` let a class keep a step for a person (ADR-0050). The
rules of a class run inside the commit: a `set` or a
`reset` changes the document before it is written, an `emit` appends a
custom event, an action that fails refuses the command with its place in
the definition, and a preview lists the rules a command would run. A
variable with `"mode": "triggered"` is computed when one of its triggers
happens, the entry or the exit of a state, a transition or a `refresh`,
and kept in between; the document says under `computed` at which version
and when. `Refresh` recomputes such variables on request and lets the
document move if a guard now holds, and `Reset` puts inputs back to
their defaults. An action on something else, such as the `create`
above, is written to the outbox in the commit's transaction and run
afterwards by a worker, exactly once and as the person whose command
caused it; a refusal leaves a dead action with its code, `Outbox` lists
them and `RetryAction` tries one again. Beside `create`, which with
`each` makes one document for each element of a list, a rule can take a
`transition` of another document, `refresh` it so that it moves by its
own guards, and insert, update or delete a row of a table. Other
actions are the host program's: `DefineAction` stores a definition with
a name such as `books.post`, typed parameters and what it returns; the
program gives `Open` a Go function under that name; and a rule calls it
with `"args"`, a formula for each parameter, checked when the class is
defined. The outbox calls the function after the commit with the
arguments and the event that fired the rule, the document included, at
least once and each time with the same key; an error is tried again and
`fado.Permanent` marks a refusal. A definition may instead carry a
`request`: a method, an address, headers and a body, each a formula over
the parameters, with `ok` and `result` to read the reply. The engine
sends it, so an API with a token needs no code: the token is
`SECRET("token")` in the definition and comes from the host program at
the moment of sending, never from the database. A database sends
nothing until its host program opens it with `outbound.Allow` and the
hosts that may be called, and a connection's secret goes only to that
connection's hosts. A request without a body of its own carries the
call, signed, which is what a developer's own service receives, and
`http.call` sends a one-off request written in the rule. An action may
carry `into`, which writes what it returned, or the code it failed
with, to variables of the document when it has ended; the eventless
transitions follow, so the document moves on by its own guards.
`Actions` lists what a rule may name, `PreviewAction` shows what would
be sent and `TryAction` runs one outside any document.
[docs/actions.md](docs/actions.md) is the guide to writing one. A preview, a listing or a read
may be asked at a moment the caller chooses, `fado.At(t)`, and a preview
reports the moment it ran under; a commit runs at the database's clock.
A refusal is a `fado.Error` with a stable code and the variable it
names; a definition's problems come back at once as `Diagnostics`, each
with a code and the place. Files are attached as `File` values behind a
blob store, reference data lives in explicit tables that formulas read
with `LOOKUP`, and a class can be redefined as a new version that
touches no existing document. `fadodb check FILE` verifies a database
file. The full walk-through is `fado/example_test.go`, and
`fado/readme_test.go` runs the class and the calls above.

## Install

Each release has archives for Linux, macOS and Windows on amd64 and arm64,
with a `checksums.txt`, on the
[releases page](https://github.com/MojoHand70/fadodb/releases). Unpack the
one for your platform and put `fadodb` on your path. With Go 1.26 or newer
you can build it instead:

```
go install github.com/MojoHand70/fadodb/cmd/fadodb@latest
```

Every release before 1.0 is a pre-release. `fadodb version` prints the
version, the commit and the build date.

## Try the formula engine

```
$ fadodb formula eval -var qty:int=3 -var price:money='9.99 PLN' 'ROUND(qty * price * (1 - 10%), 2)'
26.97 PLN
$ fadodb formula eval -var a:decimal=0.10 -var b:decimal=0.20 'a + b = 0.30'
TRUE
$ fadodb formula eval -var name:text=invoice 'UPPER(LEFT(name, 3)) & "-" & TEXT(42)'
"INV-42"
$ fadodb formula eval -sql duckdb -var due:date 'DAYS(due, TODAY())'
value  (CAST("due" AS DOUBLE) - floor(CAST($now AS DOUBLE) / CAST(86400 AS DOUBLE)))
error  NULL
params now
```

Numbers come in two families: `number` is a float64 with a spreadsheet's
arithmetic, and `decimal`, `int` and `money` are exact, an int64 mantissa
at a declared scale, with rounding written in the formula and overflow an
error. A `date` is a calendar day and a `datetime` an instant. The same
formula gives the same result on the VM, on SQLite and on DuckDB: the
conformance suite under `conformance/` is the specification, and the
property tests generate formulas to check that the three agree. The
semantics are in [ADR-0019](docs/adr/0019-formula-language-semantics.md),
[ADR-0022](docs/adr/0022-exact-decimal-int-and-money.md),
[ADR-0023](docs/adr/0023-calendar-day-datetime-and-enum-values.md) and
[ADR-0024](docs/adr/0024-division-by-zero-is-an-error-and-iferror.md), the
size limits in [ADR-0021](docs/adr/0021-size-limits.md), and the numbers
in [docs/perf/](docs/perf/).

## Design principles

- **Fast by construction.** Every feature has a performance budget, and CI
  fails on benchmark regressions.
- **Small.** Pure Go core on the standard library plus `modernc.org/sqlite`.
  No cgo in the core. DuckDB analytics and Postgres are separate modules.
- **Deterministic.** Same class, same values, same user: same result on the
  server, in SQL, and in the browser.
- **Records, not cells.** A redefinition of a class never changes a stored
  document; a document in a terminal state never changes at all.
- **Protocol first.** One JSON command set over HTTP, WebSocket, a C ABI and
  the Go API, so any frontend or language can attach.
- **Honest status.** Docs describe what works today.

The reasoning is in [docs/architecture.md](docs/architecture.md) and the
[architecture decision records](docs/adr/).

## Build

Go 1.26 or newer.

```
make tools    # staticcheck, govulncheck, benchstat
make build    # bin/fadodb
make gates    # what CI runs
```

## Layout

```
cmd/fadodb/        command-line tool: formula eval, check; the server later
fado/              public Go API
internal/formula/  lexer, parser, binder, bytecode, VM, SQL generator
internal/model/    document classes, schemas, tables, rules, validation, the diff between class versions
internal/store/    SQLite store: documents, history, events, rules in the commit, the outbox, tables, lookups, filters, blobs, class versions
internal/query/    the filter compiler, with the read rule of a class as a predicate, and the SQL endpoint's authorizer
internal/state/    statechart engine (Phase 3)
fado/outbound/     what a host program imports to let a database send the HTTP requests of actions
api/               JSON schemas of an action definition, of the call an action receives and of the delivered event
internal/auth/     users, groups and API keys for the server (Phase 6)
internal/server/   HTTP and WebSocket (Phase 6)
analytics/         the DuckDB module: conformance and store benchmarks today, the projection in Phase 8
bench/             end-to-end performance scenarios behind the budgets
docs/              architecture, ADRs, dependency budget, benchmarks
scripts/           the gates CI runs
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Design review and conformance cases
are the most useful contributions at this stage.

## License

MIT, see [LICENSE](LICENSE).
