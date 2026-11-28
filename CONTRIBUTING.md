# Contributing to FadoDB

Thank you for considering a contribution. FadoDB is at an early stage, so the
most useful contributions right now are design review, conformance cases, and
benchmarks. Read [ROADMAP.md](ROADMAP.md) for where the project stands.

## Build and test

Go 1.26 or newer.

```
make tools     # installs staticcheck, govulncheck and benchstat with go install
make build     # bin/fadodb-core, in pure Go
make full      # bin/fadodb, the same with the analytical engine (needs a C compiler)
make test      # race detector on
make gates     # everything CI runs: lint, vuln, depgate, sizegate, filegate, test
make benchgate BASE=origin/main   # the full benchmark comparison against a git ref: run it before you push a change to a hot path
scripts/layoutsweep.sh -n 8 'VM_'  # spread of the VM benchmarks over code placements
```

## The gates

Every pull request must pass:

1. `gofmt`, `go vet`, `staticcheck`.
2. `govulncheck`.
3. Tests with the race detector on Linux and macOS, plain tests on Windows.
4. The dependency gate: a direct module in `go.mod` that is not listed in
   [docs/dependencies.md](docs/dependencies.md) fails the build.
5. The size gate: a release-style core binary over the budget fails the build.
6. The file gate: a tracked file over 1 MB fails the build. The repository
   holds source, documents and test data; build `fadodb-core` with
   `make build` and `fadodb` with `make full`, which write to `bin/`,
   and never commit what a build makes.
7. The benchmark gate, in its short form (`scripts/benchgate.sh --quick`):
   any extra allocation per operation, more than 5% more bytes per
   operation, or a benchmark more than half slower against the base branch
   fails the build. It runs when the pull request changes a file that goes
   into the measured programs (`scripts/benchgate.sh --needed origin/main`
   says whether yours does) and is skipped otherwise.

A shared runner cannot measure 5% of time, so the fine comparison is
yours: when you change a hot path, run `make benchgate BASE=origin/main`
on your own machine before you push. It builds both trees at five code
placements and fails on more than 5% slower. Quote its numbers in the
pull request.

## Rules that are not negotiable

- **No new dependency without an entry in `docs/dependencies.md`.** The core
  module uses the standard library plus `modernc.org/sqlite`. Anything needing
  cgo or a driver goes in a separate Go module.
- **Hot paths do not allocate.** The VM loop, recalculation and the transition
  commit path use fixed-size buffers, no interface boxing of values, no
  reflection, prepared statements only.
- **Formula changes come with conformance cases.** The conformance suite is the
  specification. A behaviour that is not in the suite is not promised.
- **Design decisions are ADRs.** Anything that changes a contract or a budget
  gets a file under `docs/adr/`. Use `docs/adr/0000-template.md`.
- **Honest status.** Documentation describes what works today. Planned features
  are labelled as planned. The skills under `skills/` carry the same marks.

## Pull requests

- Small, focused, one topic per pull request.
- Imperative commit subjects under 72 characters: "Add lexer for text literals".
- Fill the template, including the performance section.
- A change to what works today also moves the marks in `skills/`, the pages
  agents load; a stale mark there makes an agent act on it.
- A maintainer reviews within a week. Early-stage priorities may mean a good
  change waits for its phase in the roadmap.

## License

Contributions are accepted under the Apache License 2.0 in [LICENSE](LICENSE),
as its section 5 says. There is no contributor license agreement.
