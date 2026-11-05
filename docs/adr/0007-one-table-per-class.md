# ADR-0007: One table per document class

- Status: Accepted; amended by ADR-0014 (history table, child tables for composite variables, views)
- Date: 2026-11-05

## Context

Users must query the stack of document instances like a normal table, with
indexes, and access rules must compile to SQL predicates. Two layouts were
considered: a generic documents table with a JSON column, or one typed table
per class.

## Decision

- Each class gets a table `doc_<class>` with one typed column per value,
  computed columns for stored formula results, a `state` column, and system
  columns `id`, `version`, `created_at`, `updated_at`, `state_entered_at`.
- Arrays are stored as JSON text in their column; array functions are
  evaluated by the VM, and SQL translation uses the JSON functions of the
  dialect.
- Values flagged `"index": true` get a B-tree index.
- Class changes issue DDL: adding a value or formula is in place; changing a
  type or removing a value creates a new class version and a migration.

## Consequences

- Queries, filters and read predicates are plain SQL over typed columns, and
  the query planner can use indexes.
- The number of tables grows with the number of classes, which is fine for
  hundreds and should be measured for thousands.
- The read-only SQL endpoint exposes these tables through views, so renaming
  internals never breaks users.

## Performance

Budget: an indexed filter over 1,000,000 documents returns in under 10 ms.
Table creation and migration cost is measured in Phase 2.
