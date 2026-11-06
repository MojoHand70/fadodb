# Changelog

All notable changes to FadoDB are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

Phase 1, the formula engine, started on 2026-11-06. Still nothing to run
beyond `fadodb version`.

### Added

- SQL generator with the SQLite dialect. A program becomes a value
  expression and an error expression over the columns holding its
  variables, with the short-circuit rules of ADR-0019 kept in both. The
  dialect uses native SQL where SQLite agrees with the VM and calls the
  engine's own Go functions, registered by `internal/formula/sqlitefn`,
  where it does not. The differential test runs every evaluation case on
  SQLite through `modernc.org/sqlite`, now a dependency of the core module.
- Binder, bytecode v1 and the Go VM. The binder gives every expression a
  static type by ADR-0019 and rejects type errors, unknown names and
  context functions in auto mode at definition time, with a column in every
  message. Bytecode v1 (`FADO`, versioned, binary and JSON debug forms)
  reserves the `lookup` and `query` opcodes of ADR-0020. The VM executes
  the scalar language with zero allocations on the scalar path and the
  closed list of error codes. 59 of the 67 catalog functions run; the
  composite and query functions are refused as not implemented yet.
- The conformance runner executes every evaluation case on the VM, and a
  binding suite pins the static typing rules and the auto-mode restriction.
- Formula lexer and parser for the grammar of ADR-0019, with positions in
  every error, and the function catalog (`internal/formula/catalog.json`,
  67 functions with their evaluation class, arguments and summary) that the
  parser checks arities against.
- The conformance suite under `conformance/`: 579 evaluation cases in seven
  suites (blank, logic, math, numeric, arithmetic, text, date) and a syntax
  suite of 220 accept and reject cases, with the loader that validates them.
  Until the VM lands, evaluation cases are checked to parse.
- ADR-0019: formula language semantics: grammar, static types, coercion,
  comparison, dates, text, composites and errors.
- ADR-0020: `LOOKUP` and `QUERY` execution: one engine call per invocation,
  point lookups off the analytical engine, set-wise recomputes as one
  statement per class, DuckDB dialect tested from Phase 1.

### Changed

- ROADMAP and README mark Phase 1 as in progress.
- `modernc.org/sqlite` is scheduled for Phase 1 instead of Phase 2, for the
  SQL conformance runner.

## [0.0.1] - 2026-11-05

Project scaffold. Nothing to run beyond `fadodb version`.

### Added

- Go module, `cmd/fadodb` with `version` and `help`, package layout with
  documented placeholders.
- Quality gates: gofmt, vet, staticcheck, govulncheck, race tests on three
  platforms, dependency budget gate, binary size gate, benchmark regression gate.
- Release pipeline with GoReleaser: binaries for Linux, macOS and Windows on
  amd64 and arm64, with checksums.
- Public architecture overview and twelve architecture decision records.
- Contributing guide, code of conduct, security policy, issue and pull request
  templates, roadmap.

[Unreleased]: https://github.com/MojoHand70/fadodb/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/MojoHand70/fadodb/releases/tag/v0.0.1
