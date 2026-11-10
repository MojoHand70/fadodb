# CLAUDE.md

Guidance for AI-assisted development sessions in this repository.

## What FadoDB is

An embeddable active document database in pure Go. Document classes declare
typed variables, Excel-like formulas, a statechart, and rules that fire actions on
state changes. One compiler produces bytecode for the Go VM, SQL for the
stores, and the browser VM. Every change is a commit with a typed history
row; DuckDB is the analytical engine behind an interface, so the core stays
pure Go. Read
`docs/architecture.md` and `docs/adr/` before changing anything.

## Commands

```
make tools      # install staticcheck, govulncheck, benchstat
make build      # bin/fadodb
make test       # go test -race ./...
make gates      # lint, vuln, depgate, sizegate, test  (what CI runs on the core)
make analytics  # the DuckDB module: separate go.mod, needs cgo and a C compiler
make benchgate BASE=origin/main
go test -count=1 -run TestName ./internal/formula/
```

## Invariants

1. **No new dependency** without an entry in `docs/dependencies.md`. The core
   module is the standard library plus `modernc.org/sqlite`. Nothing with cgo
   in the core; separate Go modules for `analytics/` and `postgres/`.
2. **Hot paths do not allocate.** VM loop, recalculation, transition commit:
   fixed buffers, no interface boxing of values, no reflection, prepared
   statements. Add a benchmark with `-benchmem` for any hot-path change and
   quote before/after numbers in the pull request.
3. **Three evaluators, one behaviour.** Any formula semantics change needs
   conformance cases under `conformance/` and must pass on the Go VM, the SQL
   generator and the browser VM. The suite is the specification.
4. **Decisions are ADRs.** A change to a contract (class format, bytecode,
   storage schema, command set) or to a budget needs an ADR under `docs/adr/`.
5. **Honest status.** README, ROADMAP, docs and `skills/` describe what works
   today. Mark planned behaviour as planned. A change to what works moves
   the marks in `skills/` in the same pull request. Never mark a phase done
   before its acceptance criteria pass.
6. **`private/` is personal and gitignored.** Never stage, commit, reference
   or copy its contents into tracked files.

## Style

- Standard Go: gofmt, vet, staticcheck clean. Package docs on every package.
- Errors are values with context; no panics outside programmer-error guards.
- Imperative commit subjects under 72 characters.
- Update `CHANGELOG.md` under *Unreleased* for user-visible changes.
