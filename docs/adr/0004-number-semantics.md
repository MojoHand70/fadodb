# ADR-0004: Number semantics: float64 with Excel rules

- Status: Accepted; amended by ADR-0019 (TEXT rendering rule), ADR-0022 (exact decimal, int and money beside number), ADR-0024 (/ by zero is an error)
- Date: 2026-11-05

## Context

The formula language is Excel-like because that is what the intended users
already know. Excel computes in IEEE 754 doubles. A decimal type would be
friendlier for money but costs roughly an order of magnitude in the VM and
has no native equivalent in SQLite or in JavaScript.

## Decision

- The `number` type is a float64 in every target.
- Arithmetic follows the rules the sfmt engine established and its users
  accepted: `/` by zero yields 0, `\` by zero is an error, `^` is
  right-associative and `-x ^ 2` is `(-x)^2` as in Excel, `5%` is 0.05,
  `ROUND` rounds half away from zero, `MOD` takes the sign of the divisor.
- Comparisons are exact; the language does not add a tolerance.
- `TEXT()` renders integral values without a fractional tail so they can serve
  as keys.
- A `decimal` type is deferred to a post-1.0 ADR, to be added as a new type
  rather than a change to `number`.

## Consequences

- Money is stored as a number and rounded by formula where it matters, as in
  spreadsheets. Documentation says so plainly.
- SQL translation uses `DOUBLE` in DuckDB, `REAL` in SQLite and
  `double precision` in Postgres, with the same division-by-zero rewrite.
- The conformance suite holds every rule above as a case.

## Performance

float64 is a native type in Go, SQLite, Postgres, DuckDB and JavaScript, so
the VM stack can be a fixed array of values with no boxing.
