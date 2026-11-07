# ADR-0030: The benchmark gate measures the minimum over code placements

- Status: Accepted; amends ADR-0012
- Date: 2026-11-07

## Context

ADR-0012 gates every pull request on benchstat: more than 5% slower on
any benchmark fails the job. The yardstick of the Go VM is a
five-operation formula that runs in about 13 ns.

In the pull request that added exact numbers (ADR-0022) the bench job
failed twice on that yardstick while the loop's float path had the same
or fewer instructions per case than before. A sweep over sixteen
placements of the same test binary, built with a padding function of
growing size ahead of the package's code, showed the loop running at
13.15 ns when its function starts on a 64-byte line and at 15.0 ns when
it starts 32 bytes into one, nothing in between, on an Intel Core Ultra 9
275HX; the runner showed the same two modes with a larger gap. Main's
loop ran within 13.3 to 14.2 ns at every placement, by the luck of its
layout: nine shapes of the new loop each had a slow placement class.
Which class a build lands in is decided by the size of the code before
the function, since Go aligns functions to 32 bytes and pads jumps away
from 32-byte boundaries, so it changes with any edit to the package and
differs between the development machine, the runner and an embedder's
binary. A 10% effect on a 13 ns benchmark is twice the gate's limit; the
gate could not tell placement from cost.

## Decision

The gate measures the cost of the code, not its placement.

- `scripts/benchgate.sh` builds each tree at `BENCH_PLACEMENTS`
  placements (default 5): the tree as it is, then shifted by one 32-byte
  slot or more per placement by a padding function that
  `scripts/layoutpad.sh` writes ahead of every benchmarked package's code
  (a file sorting first in the package, with statements the compiler
  cannot fold, kept by the linker because an init calls it, growing by
  28 bytes per placement so that every step crosses one 32-byte slot and
  five placements always hold at least two of each half of a 64-byte
  line).
  `BENCH_COUNT` rounds (default 5) run per placement, interleaved between
  the trees as before.
- Per benchmark and per tree, the placement with the lowest median time
  supplies the samples benchstat compares. Bytes and allocations per
  operation do not depend on placement and come from the same samples.
  The limits of ADR-0012 stand: 5% time, 5% bytes, no extra allocation,
  significant changes only.
- The job prints the median per placement of every benchmark for both
  trees, so a placement effect is visible in the log as two modes.
- `scripts/layoutsweep.sh` gives a maintainer the spread of one package's
  benchmarks over any number of placements.
- Five rounds per placement, since benchstat's test cannot reach
  significance below four samples a side.

Padding files are named `a_layoutpad.go`, are removed when a run ends
and are ignored by git.

## Consequences

- The bench job takes about five times longer: around eleven minutes
  on the runner instead of two and a half.
- A change that leaves the best placement's cost unchanged but adds a
  slow placement class passes. The gate does not guard an embedder's
  placement, which its own build decides; a maintainer who wants to see
  the spread runs the sweep.
- The written-reason override of ADR-0012 stays for what the gate still
  gets wrong.
- The five-operation yardstick and the budgets of ADR-0012 are
  unchanged; a benchmark under about 20 ns remains the right size for a
  VM instruction loop now that placement is measured out.

## Performance

`BenchmarkVM_Arithmetic5` over sixteen placements: main 13.3 to 14.2 ns;
the exact-numbers loop 13.15 ns in eight placements and 15.0 ns in the
other eight. The gate under this record compares 13.15 ns with 13.3 ns.
