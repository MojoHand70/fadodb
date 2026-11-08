# FadoDB

[![ci](https://github.com/MojoHand70/fadodb/actions/workflows/ci.yml/badge.svg)](https://github.com/MojoHand70/fadodb/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/MojoHand70/fadodb.svg)](https://pkg.go.dev/github.com/MojoHand70/fadodb)
[![Go Report Card](https://goreportcard.com/badge/github.com/MojoHand70/fadodb)](https://goreportcard.com/report/github.com/MojoHand70/fadodb)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

An embeddable **active document database**, written in Go, MIT licensed.

> **Status: Phase 1, the formula engine, is done and released as v0.1.0;
> Phase 2, the document model and store, is in progress.**
> `fadodb formula eval` evaluates a formula on the Go VM and shows the SQL the
> engine generates for SQLite and DuckDB. The Go API (`fado`) opens a
> database file, defines classes and schemas, and creates, changes, reads
> and deletes documents as commits with recalculation, typed history and an
> event log; the statechart, rules, access rules, queries and the server
> come in later phases. See [ROADMAP.md](ROADMAP.md).

## What it will be

A document class declares typed variables, Excel-like formulas, a
statechart, and rules that fire actions when the state changes. FadoDB keeps
an append-only event log with a snapshot on every transition, evaluates
formulas on the server and in the browser from one compiled bytecode, exposes
every document class as a queryable SQL table, and enforces user and group
access rules that are themselves formulas. Analytics and process mining are
SQL over the event log.

A class is one JSON document:

```json
{
  "name": "Invoice",
  "title": "\"Invoice for \" & customer",
  "variables": {
    "customer": {"type": "text", "required": true},
    "due":      {"type": "date", "default": "ADDDAYS(TODAY(), 30)"},
    "discount": {"type": "decimal", "scale": 2, "default": "0"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money"}},
    "subtotal": {"formula": "SUM(lines.qty * lines.price)"},
    "total":    {"formula": "ROUND(subtotal * (1 - discount), 2)"}
  },
  "states": {"initial": "draft", "list": ["draft", "submitted", "approved", "paid"]},
  "transitions": [
    {"name": "submit",  "from": "draft",     "to": "submitted", "guard": "total > 0"},
    {"name": "approve", "from": "submitted", "to": "approved",  "guard": "INGROUP(\"finance\")"},
    {"name": "pay",     "from": "approved",  "to": "paid"}
  ],
  "rules": [
    {"on": "enter", "state": "paid",
     "do": [{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "amount": "total"}}]}
  ],
  "access": {"read": "INGROUP(\"finance\") OR customer = USER()"}
}
```

Planned Go API, not implemented:

```go
db, _ := fado.Open("app.fado", fado.Options{})
db.DefineClass(ctx, invoiceJSON)
p := fado.Principal{ID: "alice", Groups: []string{"sales"}}
id, _ := db.Create(ctx, "Invoice", fado.Values{"customer": "ACME"}, fado.As(p), fado.Key("order-42"))
db.Transition(ctx, "Invoice", id, "submit", fado.As(p))
rows, _ := db.Query(ctx, `SELECT id, total FROM Invoice WHERE state = 'submitted'`, fado.As(p))
```

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
cmd/fadodb/        command-line tool and server entry point
fado/              public Go API (planned)
internal/formula/  lexer, parser, binder, bytecode, VM, SQL generator
internal/model/    document classes, typed variables, defaults, validation, migrations
internal/store/    SQLite store, event log, outbox
internal/state/    statechart engine
internal/rules/    event-condition-action rules and built-in actions
internal/auth/     users, groups, access rules as predicates
internal/query/    filter DSL and read-only SQL
internal/server/   HTTP and WebSocket
bench/             end-to-end performance scenarios behind the budgets
docs/              architecture, ADRs, dependency budget, benchmarks
scripts/           the gates CI runs
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Design review and conformance cases
are the most useful contributions at this stage.

## License

MIT, see [LICENSE](LICENSE).
