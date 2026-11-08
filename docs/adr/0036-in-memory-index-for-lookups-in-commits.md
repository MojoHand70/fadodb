# ADR-0036: An in-memory index serves LOOKUP inside commits, by measurement

- Status: Accepted; amends ADR-0016 and ADR-0020
- Date: 2026-11-07

## Context

ADR-0015 lets an auto formula call `LOOKUP` against an explicit table,
and ADR-0016 says such a lookup is "served from an in-memory index for
small tables and a prepared statement otherwise". ADR-0020 kept point
lookups inside commits off the analytical engine and asked for a
benchmark at the start of Phase 2, a Go map against SQLite against
DuckDB, to confirm the choice and fix the threshold between the index and
the statement.

The benchmark is `RunLookup` in the `bench` package. A table `rates(code
TEXT PRIMARY KEY, rate REAL)` is filled with 100 to 1,000,000 rows by the
engine's own bulk statement, read once into a `map[string]formula.Value`
as the in-memory index would hold it, and then looked up 50,000 times by
pseudo-random key, once on the map and once through a prepared `SELECT
rate FROM rates WHERE code = ?` on a pinned connection. The heap the map
takes is measured by the runtime around the load. The keys are formatted
before the timed loops, so that both loops measure the lookup alone.

Measured on 2026-11-07 on the machine of ADR-0035:

| Engine | Rows | Go map | Map heap | Prepared statement |
|---|---|---|---|---|
| SQLite | 100 | 7 ns | under 0.1 MB | 2.2 µs |
| SQLite | 1,000 | 5 ns | 0.1 MB | 2.4 µs |
| SQLite | 10,000 | 8 ns | 1.1 MB | 2.5 µs |
| SQLite | 100,000 | 15 ns | 8.9 MB | 3.5 µs |
| SQLite | 1,000,000 | 23 ns | 133.5 MB | 4.2 µs |
| DuckDB | 100 | 6 ns | under 0.1 MB | 221.9 µs |
| DuckDB | 1,000 | 5 ns | 0.1 MB | 222.2 µs |
| DuckDB | 10,000 | 8 ns | 1.1 MB | 220.3 µs |
| DuckDB | 100,000 | 15 ns | 9.0 MB | 222.7 µs |
| DuckDB | 1,000,000 | 26 ns | 133.5 MB | 243.4 µs |

The map rows are the same Go map whichever engine filled the table; the
heap is 90 to 135 bytes per row for an eight-byte key and a number,
and the lookup slows from 5 to 23 ns as the map outgrows the caches. A
prepared statement on the SQLite store costs 2.2 to 4.2 µs through the
pure-Go driver, a hundred to a thousand times the map and well under the
100 µs cold budget; it grows little with the table because the primary
key is a B-tree. On DuckDB the same statement costs 220 to 243 µs at
every size: the fixed cost of a statement through the client, which the
table's size barely touches.

The budgets of ADR-0016: a cached `LOOKUP` under 1 µs, a cold one under
100 µs against an indexed table; and of ADR-0012: a 50-formula auto
recalculation under 50 µs, a commit under 2 ms at p99.

## Decision

1. **A `LOOKUP` in an auto formula is served from an in-memory index**
   while the source table has at most `MaxIndexedRows` rows, 100,000 by
   default. The index is built from the table on first use, column-wise
   as ADR-0020 reads results, one hash index per table and equality
   column, and is dropped when the table changes through a `table.*`
   action or an import; the next commit rebuilds it. A lookup costs tens
   of nanoseconds and the table costs about 90 bytes per row of heap at
   this shape, so 100,000 rows of a one-column table are 9 MB, and the
   engine never holds more than that per table without being asked.
2. **Above the limit, and for a condition that is not an equality on
   one column**, the lookup runs as a prepared statement on the SQLite
   store's connection, at 2 to 4 µs on this machine, under the 100 µs
   cold budget by a wide margin. A formula whose `LOOKUP` conditions
   compare with `<`, use `AND` or a function on the source column takes
   this path at any size; the binder reports which path a formula takes,
   so that an author who needs the fast one knows to write an equality.
3. **`MaxIndexedRows` is a database limit** like `MaxText` and `MaxBlob`
   (ADR-0021, ADR-0033): chosen at creation, kept in the metadata, and
   changeable in either direction, since it changes speed and memory and
   never a result.
4. **DuckDB never serves a lookup inside a commit.** One prepared
   statement costs about 225 µs through the Go client, the fixed cost
   ADR-0020 anticipated, which is a tenth of the commit budget per
   lookup; a formula with ten lookups would spend the budget on them. The
   full build answers `LOOKUP` in auto formulas from the SQLite store or
   the index exactly as the lite build does, and `QUERY` and triggered
   `LOOKUP` on DuckDB as ADR-0020 says.

## Consequences

- The cached budget of ADR-0016, under 1 µs, is met by two orders of
  magnitude with the index and missed by the statement; a 50-formula
  recalculation with a lookup in every formula stays under its 50 µs
  budget only on the index. That is why the index exists: not because a
  statement is slow, but because a recalculation makes many lookups.
- Memory is the price. A hundred-row currency table costs nothing; a
  hundred-thousand-row price list costs about 9 MB per equality column
  indexed, which a server operator can see and bound.
- The table change that drops an index is the `table:` trigger of
  ADR-0015 as well, so one notification serves both: recompute the
  dependents, rebuild the index on the next lookup.
- A third-party binding (`external`) is never indexed in memory and never
  used by an auto formula, as ADR-0015 already restricts auto formulas to
  explicit tables bound to the store. That line now has a measured
  reason.
- The threshold is a product rule like the element caps of ADR-0013 and
  is documented with them.

## Performance

Budgets confirmed: cached under 1 µs by the index, cold under 100 µs by
the statement. The index build is one table scan, measured with the table
import in Phase 2; the rebuild after a `table.*` action is the same scan
and runs outside the commit that caused it, on the next lookup. The
benchmark stays in the `bench` package and is rerun when the driver or
the index shape changes.
