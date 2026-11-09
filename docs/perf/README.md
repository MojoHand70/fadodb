# Benchmark results

Published per tagged release, with the budgets from
[ADR-0012](../adr/0012-performance-gates.md).

- [v0.1.0](v0.1.0.md): the formula engine, Phase 1.
- [v0.2.0](v0.2.0.md): the document model and store, Phase 2.
- [v0.3.0](v0.3.0.md): the statechart, the rights and the chosen moment,
  Phase 3.

The end-to-end suite is the `bench` package: scenarios written against
database/sql so that the same one runs on every storage engine. Each
module runs a small scenario in CI and the full campaign on request:

```
FADODB_BENCH=store,lookup go test -count=1 -v -run TestCampaign ./bench/
cd analytics && FADODB_BENCH=store,lookup CGO_ENABLED=1 go test -count=1 -v -run TestCampaign ./...
```

The store's own campaign runs the real commit, compute and write, on the
test classes of the store package, with the statechart rows of ADR-0040
(a transition, a transition with values, a chain of three and the listing
of ten candidates) and the preview at a chosen moment of ADR-0042, and
the filter over a loaded database, with and without a read right on the
filtered variable (ADR-0041):

```
FADODB_BENCH_DIR=/path/on/disk FADODB_BENCH_COMMITS=100000 go test -count=1 -v -run TestCommitCampaign ./internal/store/
FADODB_BENCH_DIR=/path/on/disk FADODB_BENCH_DOCS=1000000 go test -run '^$' -bench 'Filter|Commit' -benchmem ./internal/store/
```

`FADODB_BENCH_COMMITS` sizes the store campaign (default 100,000),
`FADODB_BENCH_ITERS` the lookups per table size (default 50,000),
`FADODB_BENCH_TARGET` selects the targets whose name contains it (for
one variant, such as `"background checkpoints"` or `"1 thread"`) and
`FADODB_BENCH_DIR` the directory for the database files, by default the
package directory. Point it at the disk the numbers are meant for, never
at `/tmp`: on many Linux machines that is a tmpfs in memory, where an
fsync is free and every store row is wrong by an order of magnitude. The
two campaigns that opened Phase 2 are recorded in
[ADR-0035](../adr/0035-sqlite-is-the-transactional-store.md) and
[ADR-0036](../adr/0036-in-memory-index-for-lookups-in-commits.md); the
first release of Phase 2 publishes them here.
