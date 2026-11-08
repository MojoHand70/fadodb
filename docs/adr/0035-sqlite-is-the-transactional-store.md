# ADR-0035: SQLite is the transactional store, by measurement

- Status: Accepted; amends ADR-0017, confirms ADR-0033's default blob store
- Date: 2026-11-07

## Context

ADR-0017 made DuckDB the analytical engine and left the transactional
store as an expectation: SQLite, "to be measured, not a fact", with the
first Phase 2 benchmark writing 100,000 commits into SQLite and into
DuckDB and a follow-up record carrying the result. The storage interface
was to keep the decision cheap to flip. One criterion beside speed was
added before the run: whether a second process can read the database file
while FadoDB writes it. An analyst who attaches a FadoDB file from DuckDB,
Python or R while the engine runs, a backup that copies a live file, and
the Phase 8 page on reading a FadoDB file from DuckDB's SQLite scanner all
depend on it.

The benchmark is the `bench` package. One commit writes what ADR-0014 and
ADR-0026 say a commit writes: the current row of a class with twenty-one
variables and a title, scalars of every stored kind, a money variable as
its two columns, two objects flattened to prefixed columns, upserted; the
previous history row closed and the new one inserted; five child rows of a
`rows` variable; one event row with its command key and business date; one
`event_docs` row. Every statement is prepared once on one pinned
connection, which is the store's single writer. Three runs per engine:
100,000 commits over 20,000 documents one transaction per commit, the
same batched 1,000 commits per transaction, and 100,000 creates batched.
SQLite ran as the architecture page configures it, WAL mode with
`synchronous=NORMAL` and SQLite's automatic checkpoint; again with the
checkpoint moved to a second connection; and again with
`synchronous=FULL`, where every commit is fsynced. DuckDB ran with the
Go client's defaults on a database file, and again on one thread. After each run the
tables are checked: one current row per document, one history row, one
event and one `event_docs` row per commit, five child rows per version,
one open history row per document, and the current version equal to the
last history version. The second-process question is a test that holds
the file open and writing while the test binary re-runs itself to open the
same file read-only and count the documents.

Measured on 2026-11-07 on an Intel Core Ultra 9 275HX, 24 threads, 31 GB,
under WSL2 with kernel 6.18, on the virtual disk that holds the home
directory, ext4 on a VHD on NVMe, where an fsync of 4 KB costs 4.0 ms at
the median and 5.7 ms at p99; Go 1.26.5, `modernc.org/sqlite` v1.61.1 and
DuckDB 2.0.0 through `duckdb-go` v2. A first run from `/tmp`, a tmpfs in
memory on this machine where the same fsync costs under a microsecond,
was discarded; the campaign refuses the temporary directory by default
for that reason.

One transaction per commit, 100,000 commits over 20,000 documents:

| Target | Throughput | p50 | p90 | p99 | max | File after |
|---|---|---|---|---|---|---|
| SQLite, WAL, synchronous NORMAL | 3,436 commits/s | 0.061 ms | 0.085 ms | 10.15 ms | 18.5 ms | 108 MB |
| SQLite, NORMAL, checkpoints in the background | 9,955 commits/s | 0.071 ms | 0.100 ms | 0.192 ms | 537 ms | 1,907 MB, log not yet truncated |
| SQLite, WAL, synchronous FULL | 256 commits/s | 4.07 ms | 4.48 ms | 9.26 ms | 35.0 ms | 108 MB |
| DuckDB, client defaults | 94 commits/s | 10.6 ms | 12.7 ms | 14.1 ms | 180 ms | 105 MB |
| DuckDB, one thread | 108 commits/s | 9.4 ms | 10.8 ms | 12.6 ms | 175 ms | 105 MB |

Batched, 1,000 commits per transaction; the percentiles are per
transaction:

| Target | Work | Throughput | p50 | p99 | max | File after |
|---|---|---|---|---|---|---|
| SQLite, NORMAL | 100,000 commits over 20,000 documents | 23,825 commits/s | 39.0 ms | 62.6 ms | 62.6 ms | 110 MB |
| SQLite, NORMAL | 100,000 creates | 27,952 commits/s | 32.7 ms | 49.4 ms | 49.4 ms | 129 MB |
| SQLite, NORMAL, background checkpoints | 100,000 commits over 20,000 documents | 30,727 commits/s | 33.6 ms | 38.4 ms | 38.4 ms | 304 MB |
| SQLite, NORMAL, background checkpoints | 100,000 creates | 36,873 commits/s | 27.1 ms | 30.3 ms | 30.3 ms | 250 MB |
| SQLite, FULL | 100,000 commits over 20,000 documents | 22,284 commits/s | 45.0 ms | 56.2 ms | 56.2 ms | 110 MB |
| SQLite, FULL | 100,000 creates | 26,367 commits/s | 38.0 ms | 48.6 ms | 48.6 ms | 129 MB |
| DuckDB, client defaults | 100,000 commits over 20,000 documents | 166 commits/s | 6,078 ms | 6,547 ms | 6,547 ms | 93 MB |
| DuckDB, client defaults | 100,000 creates | 174 commits/s | 5,652 ms | 6,352 ms | 6,352 ms | 89 MB |
| DuckDB, one thread | 108 commits/s | 9.4 ms | 10.8 ms | 12.6 ms | 175 ms | 105 MB |_BATCHED

Reopening a finished database and reading one row by key took 0.2 to
0.5 ms on SQLite and 15 to 19 ms on DuckDB.

Two things the SQLite rows show. With SQLite's automatic checkpoint,
the connection that crosses 1,000 log pages copies the log into the
database file and fsyncs it inside its own commit, which is the 10 ms at
p99 of the first row: every two hundredth commit pays the checkpoint.
Checkpointing from a second connection every 100 ms
(`wal_checkpoint(PASSIVE)`) takes that out of the commit, 0.19 ms at
p99 and three times the throughput; but a passive checkpoint never
resets a log that a writer keeps appending to, so the variant truncates
the log (`wal_checkpoint(TRUNCATE)`) once it passes 64 MB, and that
truncation holds the writer for the copy, the 537 ms maximum, and under
this saturating writer the log still reached 1.9 GB between truncations.
A writer that commits ten thousand times a second without a pause is the
benchmark, not a workload; the policy and its thresholds are measured
again with the real commit in Phase 2. With `synchronous=FULL` every
commit fsyncs the log, 4 ms on this disk, and the one-writer ceiling is
250 commits per second here; batching is unaffected because a batch is
one fsync.

The DuckDB rows are not about fsync either: a batch of 1,000 commits
takes four to six seconds, which is 4 to 6 ms per commit with one fsync
per batch, so the cost is the statements; one thread instead of
twenty-four gains a sixth, so it is not the pool's coordination either. The commit shape has two updates, the
upsert of the current row and the closing of the previous history row,
which DuckDB executes as a delete and an insert on an indexed table; an
insert-only shape was not measured, but the simplest statement of all,
the point lookup of ADR-0036, costs about 225 µs through the client, so
a dozen statements per commit could not come under 3 ms whatever their
shape. DuckDB's documentation describes an engine built for analytical
work in large transactions, and the measurement agrees with it.

| Question | SQLite | DuckDB |
|---|---|---|
| A second process reads the file while this one writes | Yes, in WAL mode: the reader counted the documents while the writer committed | No: `IO Error: Could not set lock on file`, the writer's process holds the lock |

The budgets of ADR-0012 and ADR-0015 for this path, against the SQLite
rows: a commit under 2 ms at p99 on NVMe, met with background
checkpoints and missed with the automatic one on this disk; 2,000 or
more commits per second unbatched on one writer, met at NORMAL and
missed at FULL, where the disk's fsync is the ceiling; 10,000 or more
document creates per second batched, met three times over; opening a
database under 50 ms, met by two orders of magnitude. These are the
write step alone, without the compute that Phase 2 adds in front of it.

## Decision

1. **SQLite is the transactional store**, for the lite and the full build
   alike. The current, history and child tables, `events`, `event_docs`,
   the outbox, the class and schema tables and explicit tables bound to
   `sqlite` live in one SQLite file. ADR-0017's open measurement is
   closed by this record; its other decisions stand.
2. **DuckDB never holds the record of a document.** It remains the
   analytical engine of ADR-0017: the projection fed from the history and
   event tables, native explicit tables, attached databases, set-wise
   recomputes and mining. The measurement is the reason: a transaction
   that writes a dozen small rows costs DuckDB 10.6 ms at the median
   against SQLite's 0.06 ms, and batching does not help it, since the
   cost is per statement and not per fsync; DuckDB's file cannot be read
   by a second process while FadoDB holds it; and DuckDB's own
   documentation describes the engine as built for analytical work in
   large transactions, not for many small ones.
3. **WAL mode with `synchronous=NORMAL` is the default**, as the
   architecture page has it, and `synchronous=FULL` is an option of the
   database recorded in its metadata, for an embedder that wants every
   commit on disk before it is acknowledged. The difference is the
   disk's fsync, 4 ms per commit on this machine, which caps one
   unbatched writer at about 250 commits per second here; a batch pays
   it once. In NORMAL mode a commit survives a crash of the process; the
   last commits before a power failure may be lost, and the
   documentation says so.
4. **The storage interface stays.** The store is written against
   database/sql with the dialect of ADR-0019 and the `Target`-shaped
   seam the benchmark used, so that Postgres (Phase 10) is a second
   implementation and not a rewrite. No second implementation is planned
   for the lite or the full build.
5. **The default blob store is `sqlite`** (ADR-0033): one file holds the
   documents and their attachments. ADR-0033 had made `dir` the default
   should DuckDB become the primary store; it does not.
6. **The commit path is written as the benchmark ran it**: one pinned
   writer connection, every statement prepared once, `BEGIN IMMEDIATE`
   per commit, batched transactions for bulk loads and set-wise system
   commits. The history row is closed by one primary-key update of
   `valid_to`, as ADR-0014's columns imply; the benchmark includes that
   update.
7. **Checkpoints run off the commit path.** The store turns
   `wal_autocheckpoint` off and a checkpointer on its own connection runs
   `wal_checkpoint(PASSIVE)` on a timer and `wal_checkpoint(TRUNCATE)`
   when the log passes a size limit, 64 MB by default, so that a commit
   never pays a checkpoint's fsync and the log cannot grow without bound.
   The timer, the limit and the pause a truncation costs are measured
   with the Phase 2 commit benchmark and may change; the principle, that
   the committing connection never checkpoints, is the decision.

## Consequences

- Nothing in the architecture page changes but one sentence: the
  expectation becomes a measured fact. The architecture page's schema
  list, the Phase 8 page on attaching a FadoDB file from DuckDB through
  the SQLite scanner, and ADR-0033's one-file deployment all stand.
- The pure-Go driver is the whole core's I/O. Its cost per statement,
  measured here at a few microseconds, is what the commit budget is spent
  on; the `bench` package stays in the tree so that a driver upgrade or a
  schema change is measured against these rows.
- A FadoDB file is readable by any SQLite client while the engine runs,
  which is a feature and a responsibility: access rules do not apply to
  raw file access, and the documentation says so where it says how.
- An embedder that wants analytical speed over the document stack gets it
  from the Phase 8 projection, not from the store, and that projection
  lags the store by its feed delay, as ADR-0017 already says.
- Batched loads are the way to the create budget: one transaction per
  commit reaches 3,400 commits per second with the automatic checkpoint
  and 10,000 with the background one on this machine, batching 24,000 to
  37,000.

## Performance

The rows above are the first measurement of the commit, create and open
budgets of ADR-0012; the write step alone, without compute, since the
model does not exist yet. The Phase 2 commit benchmark adds the compute
and must stay under 2 ms at p99, and the first Phase 2 release publishes
both under `docs/perf/`. The second-process test runs in CI on every
operating system for SQLite, so that the property this record relies on
is checked rather than assumed; the DuckDB test records the outcome
without asserting it, since it is DuckDB's process model and not ours.
