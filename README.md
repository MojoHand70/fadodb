# FadoDB

[![ci](https://github.com/MojoHand70/fadodb/actions/workflows/ci.yml/badge.svg)](https://github.com/MojoHand70/fadodb/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/MojoHand70/fadodb.svg)](https://pkg.go.dev/github.com/MojoHand70/fadodb)
[![Go Report Card](https://goreportcard.com/badge/github.com/MojoHand70/fadodb)](https://goreportcard.com/report/github.com/MojoHand70/fadodb)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

An embeddable **active document database**, written in Go, MIT licensed.

> **Status: Phase 0, scaffold.** Nothing works yet beyond `fadodb version`.
> The design is public and reviewable; the code is not. See [ROADMAP.md](ROADMAP.md).

## What it will be

A document class declares typed named values, Excel-like formulas, a
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
  "values": {
    "customer": {"type": "text", "required": true},
    "lines":    {"type": "array", "of": "number"},
    "discount": {"type": "number", "default": 0}
  },
  "formulas": {
    "subtotal": "SUM(lines)",
    "total":    "ROUND(subtotal * (1 - discount), 2)"
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
  "access": {"read": "INGROUP(\"finance\") OR customer = USER().name"}
}
```

Planned Go API, not implemented:

```go
db, _ := fado.Open("app.fado", fado.Options{})
db.DefineClass(ctx, invoiceJSON)
id, _ := db.Create(ctx, "Invoice", fado.Values{"customer": "ACME", "lines": []float64{100, 250}}, fado.As(user))
db.Transition(ctx, "Invoice", id, "submit", fado.As(user))
rows, _ := db.Query(ctx, `SELECT id, total FROM Invoice WHERE state = 'submitted'`, fado.As(user))
```

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
internal/model/    document classes, typed values, validation, migrations
internal/store/    SQLite store, event log, outbox
internal/state/    statechart engine
internal/rules/    event-condition-action rules and built-in actions
internal/auth/     users, groups, access rules as predicates
internal/query/    filter DSL and read-only SQL
internal/server/   HTTP and WebSocket
docs/              architecture, ADRs, dependency budget, benchmarks
scripts/           the gates CI runs
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Design review and conformance cases
are the most useful contributions at this stage.

## License

MIT, see [LICENSE](LICENSE).
