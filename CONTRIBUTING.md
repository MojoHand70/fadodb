# Contributing to FadoDB

Thank you for considering a contribution. FadoDB is at an early stage, so the
most useful contributions right now are design review, conformance cases, and
benchmarks. Read [ROADMAP.md](ROADMAP.md) for where the project stands.

## Build and test

Go 1.26 or newer.

```
make tools     # installs staticcheck, govulncheck and benchstat with go install
make build     # bin/fadodb
make test      # race detector on
make gates     # everything CI runs: lint, vuln, depgate, sizegate, test
make benchgate BASE=origin/main   # benchmark comparison against a git ref
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
6. The benchmark gate: more than 5% slower, more than 5% more bytes per
   operation, or any extra allocation per operation against the base branch
   fails the build.

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
  are labelled as planned.

## Pull requests

- Small, focused, one topic per pull request.
- Imperative commit subjects under 72 characters: "Add lexer for text literals".
- Fill the template, including the performance section.
- A maintainer reviews within a week. Early-stage priorities may mean a good
  change waits for its phase in the roadmap.

## License

Contributions are accepted under the MIT license in [LICENSE](LICENSE). There
is no contributor license agreement.
