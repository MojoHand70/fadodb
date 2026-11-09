# ADR-0012: Performance budgets and gates

- Status: Accepted; amended by ADR-0030 (the gate measures the minimum over code placements); the gate's scope narrowed on 2026-11-09 (see Gates)
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
  overriding with a written reason in the pull request.

## Performance

This record defines the budgets the others refer to.
