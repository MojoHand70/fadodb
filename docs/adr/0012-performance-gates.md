# ADR-0012: Performance budgets and gates

- Status: Accepted; amended by ADR-0030 (the gate measures the minimum over code placements); the gate's scope narrowed on 2026-11-09 and again on 2026-11-14, and since 2026-11-15 a pull request is held on GitHub to a short form of the gate (see Gates)
- Date: 2026-11-05

## Context

Performance is the first requirement of the project, and it is lost one
unmeasured change at a time. The only defence that survives contributors and
AI-assisted sessions is a budget per feature and a gate per pull request.

## Decision

Initial budgets, to be validated and revised at the end of Phase 1 and
published with every release under `docs/perf/`:

| Measure | Budget |
|---|---|
| Five-operation arithmetic formula, Go VM | < 100 ns, 0 allocations |
| Full recalculation of a 50-formula document | < 50 µs |
| Transition commit, p99, NVMe, single writer | < 2 ms |
| Document creates per second, batched | ≥ 10,000 |
| Transitions per second, unbatched | ≥ 2,000 |
| Indexed filter over 1,000,000 documents | < 10 ms |
| Directly-follows view over 10,000,000 events, DuckDB build | < 2 s |
| Browser VM recalculation of 200 formulas | < 1 ms |
| Core binary size | ≤ 15 MB |
| Open an existing database | < 50 ms |

Gates, enforced in CI on every pull request:

- Benchmarks with `-benchmem` next to every package; `benchstat` compares the
  pull request against the base branch and fails on more than 5% regression
  in time or bytes per operation, or any extra allocation per operation.
  Since 2026-11-09 the gate runs on the pull requests that change Go code,
  a build file or a script, and over the benchmarks the budgets name, the
  hot paths of the table above and of each phase's records, eleven at the
  time; every benchmark still runs by hand for a release's perf page. The
  gate had grown to half an hour per pull request, docs-only ones included,
  as Phase 3 added its benchmarks.
  Since 2026-11-14 "change Go code" is read exactly: the gate runs when
  the pull request changes a file that goes into the measured programs.
  That is a Go file of a package linked into the benchmarks of the gate's
  packages, a test file of those packages themselves, a file they embed,
  `go.mod`, `go.sum`, the Makefile, a script or the CI workflow.
  `scripts/benchgate.sh --needed` answers it from the go tool's own list
  of packages, and answers yes when in doubt. A test of another package
  or a package the benchmarks do not link cannot move a number of the
  gate, and each had cost the half hour all the same.
  Since 2026-11-15, on the owner's word, the gate has two forms. He
  would not wait half an hour for one check, and the half hour was
  buying nothing: on one pull request the gate failed twice, 25 minutes
  each, on runners with different processors. The first run refused
  three benchmarks at 5.4 to 7.4% and passed a fourth; the second
  passed those three and refused the fourth at 5.3%. The change touched
  none of the four, and all four cost the same on both trees on the
  development machine at twelve placements. A shared runner cannot
  measure 5% of time, with five placements or with one.
  - **On GitHub a pull request is held to the short form**,
    `scripts/benchgate.sh --quick`: one placement and five rounds of
    half a second, ten runs of each benchmark where the full form makes
    fifty. Bytes and allocations per operation do not depend on the
    machine and are held as before: 5% and none. Time is refused only
    when a benchmark is more than half slower than on the base branch,
    which no placement explains (the widest swing between placements
    in those two runs was 16%) and a blunder does.
  - **The full form**, five placements, five rounds and 5% on time, is
    what `make benchgate` runs. Whoever changes a hot path runs it on
    their own machine before the push and quotes its numbers in the
    pull request. No check on GitHub holds anybody to that step.
  - **The budgets are measured for each release** on the development
    machine and published in its perf page, as before.
  What this gives up: a slowdown between 5% and 50% is no longer
  stopped by GitHub before a merge. It is found by the full form on
  the author's machine, or by the measurements of the release.
- Binary size gate against the budget above.
- A perf suite under `bench/` runs the end-to-end scenarios nightly; CPU and
  allocation profiles are archived per release.
- Every ADR and design note has a Performance section naming the budget and
  the benchmark that proves it.

## Consequences

- Some pull requests will be rejected for performance alone. That is the
  point.
- Benchmarks on shared CI runners are noisy; the gate uses six runs and
  benchstat's significance test, and a maintainer can re-run before
  overriding with a written reason in the pull request. Since
  2026-11-15 the runner is no longer asked for what it cannot measure:
  see Gates.

## Performance

This record defines the budgets the others refer to.
