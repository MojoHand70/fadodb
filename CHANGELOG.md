# Changelog

All notable changes to FadoDB are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

Phase 1, the formula engine, started on 2026-11-06. Still nothing to run
beyond `fadodb version`.

### Added

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
