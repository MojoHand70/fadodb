# Changelog

All notable changes to FadoDB are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- The store and the Go API, `internal/store` and `fado`: a database file
  opened as ADR-0035 configures it, WAL mode with the checkpointer on its
  own connection and every statement prepared once on one pinned writer,
  `synchronous` and the text limit kept in its metadata. `DefineClass`
  compiles a definition and creates the current, history and child
  tables of ADR-0014 with the views a user sees; `DefineSchema` stores a
  named schema. `Create`, `Set` and `Delete` are commits in the sense of
  ADR-0015, computed first and written last in one transaction: the
  values in the wire shape of ADR-0018 checked against the type, the
  enum list, the range and the text limit, the defaults of the inputs
  not given, the auto variables and the title recalculated over the
  dirty part of the dependency graph, the history row closed and
  written, the child rows carried forward in one statement, the event
  appended with its idempotency key and business date (ADR-0026), and
  the principal recorded as the actor (ADR-0027). A repeated key returns
  the first commit's result; a set that changes nothing commits nothing.
  `Preview` runs the same pipeline and rolls back (ADR-0025), reporting
  the error of every variable whose formula failed. `Get`, `Version`,
  `History` and `Events` read a document, one version, every version
  and the log. Every refusal is an error with a stable code and the
  variable it names. A class with no documents can be redefined; one
  with documents cannot until class migration is decided.
- `formula.ParseExact`, `ExactFromFloat`, `Rescale`, `Render` and `Day`:
  the exact-number and date helpers the store reads and writes values
  with.
- The class model, `internal/model`: a class definition is parsed from
  JSON in written order and compiled with every finding reported at once
  as a list of diagnostics, each a stable code, the JSON pointer of the
  element, the position inside a formula, a message and a fix where there
  is one. It checks names against the reserved columns and keywords,
  types with their scales, enum values, ranges, refs, the properties an
  input and a computed variable may have, binds defaults, formulas, the
  title, guards, rule conditions, access rules and rights with the
  function classes each allows, infers a computed variable's type from
  its formula when none is declared, reads a bare number literal as an
  exact constant for a decimal variable, builds the dependency graph of
  the auto variables and the defaults and refuses a cycle, resolves
  named schemas with the predefined vocabulary of ADR-0032 and user
  schemas that extend them, and lays the variables out as the typed
  columns and child tables of ADR-0014. The statechart, rules, access
  rules and rights are checked for structure and bound now and run in
  their phases.
- Object field paths in formulas (ADR-0031): a variable of type
  `object<Schema>` is read by its leaves, `shipping.city` or
  `venue.address.city`, each a scalar slot of the field's type that
  binds, evaluates and translates to SQL like a scalar variable. The
  conformance suites declare schemas and set leaves by path; the binding
  suite `objects` fixes what is refused for now: a whole object in a
  formula, a path that stops on an object, and rows columns. A `pure`
  binding mode allows pure functions only, for class titles and
  presentation formulas.
- The `bench` package: the end-to-end scenarios behind the budgets of
  ADR-0012, on database/sql so that one scenario runs on every engine. It
  opens Phase 2 with the primary-store benchmark of ADR-0017, commits in
  the shape of the document tables into SQLite and into DuckDB with their
  latency, throughput and whether a second process can read the live
  file, and the point-lookup benchmark of ADR-0020, a keyed lookup on a Go
  map against a prepared statement at five table sizes. The DuckDB target
  lives in the `analytics` module. The results are proposed as ADR-0035,
  the transactional store, and ADR-0036, the in-memory index for `LOOKUP`
  inside commits.
- Exact numbers (ADR-0022): the types `decimal(s)`, `int` and `money(s)`,
  an int64 mantissa at a scale of up to 18 places, with exact `+`, `-` and
  `*`, division at the dividend's scale plus four, the error `overflow`
  where a result does not fit, currencies that must agree or raise
  `currency_mismatch`, and a number constant read exactly when it meets an
  exact operand. New functions `DECIMAL`, `NUMBER`, `MONEY`, `MONEYVALUE`,
  `DECIMALVALUE`, `AMOUNT` and `CURRENCY`; `ROUND` takes a method,
  `half_up`, `half_even`, `down`, `up`, `floor` or `ceiling`; `MIN`, `MAX`,
  `SUM`, `AVERAGE`, `MOD`, `ABS`, `SIGN`, `FLOOR`, `CEILING` and `TRUNC`
  run on either family. A money variable is two columns in SQL, the
  mantissa and the currency.
- A `date` is a calendar day, days since 1970-01-01, and `datetime` is the
  instant the old `date` was (ADR-0023). `DATEVALUE` reads days only; new
  `DATETIMEVALUE`, `DATEOF` and `DATETIME`; the date functions take either
  kind and the two never compare without a conversion; `TEXT` of a
  datetime always shows the clock. Enum values for text variables are
  recorded for Phase 2.
- `IFERROR(value, fallback)` catches an evaluation error in the value
  (ADR-0024), on the VM through a handler and in SQL through the error
  expression.
- `fadodb formula eval` declares the new types and reads money as
  `"12.30 PLN"`.
- The conformance suites `decimal`, `money`, `datetime`, `iferror` and the
  binding suite `exact`; the property generator produces exact, money,
  date and datetime formulas, and found nine divergences between the VM
  and the SQL dialects before they shipped.
- ADR-0025 to ADR-0029 record what the first embedder needs
  from Phases 2 to 7: preview and validate without writing, idempotency
  keys on commands and a business date on events with a published
  transition event, the principal from the embedder and registered action
  executors, field-level rights per state, and the browser VM as one plain
  JavaScript file.
- ADR-0031 to ADR-0034 record, for Phases 2 and 7: named schemas and
  `object<Schema>` values stored flat as one typed column per field, with
  field paths, object blankness and equality in formulas; a predefined
  vocabulary (`Person`, `Organization`, `Address`, `GeoPoint`, `Place`,
  `Period`, `Event`, `Quantity`, `File`) with a presentation block and the
  `chip`, `card`, `coins` and `list` components; attachments as `File`
  values with the bytes behind a blob store, in the database file, a
  directory beside it or the embedder's own store; and `min` and `max` on
  numeric variables with `rating` and `slider` components.

### Changed

- Division by zero is the error `div_by_zero` for `/` as it was for `\`,
  which is now the same operator (ADR-0024). A formula that relied on the
  silent zero guards the divisor with `IF` or `IFERROR`.
- Bytecode format v2: an exact constant pool and the opcodes `const.dec`,
  `dec.neg`, `dec.percent`, `dec.add`, `dec.sub`, `dec.mul`, `dec.div`,
  `dec.scale`, `try` and `end.try`. A constant expression of literals folds
  at definition time.

### Fixed

- `fadodb version` built with `go install .../cmd/fadodb@v0.1.0` reports
  the module version instead of `dev`.

## [0.1.0] - 2026-11-06

Phase 1, the formula engine: the language of ADR-0019, compiled once and
evaluated three ways, on the Go VM, on SQLite and on DuckDB, with the
conformance suite as the specification and property-based campaigns
checking that the three agree. `fadodb formula eval` runs it from the
command line. Documents, storage and the server are not there yet.

### Added

- `fadodb formula eval`: evaluates a formula on the Go VM with variables,
  a user, groups and a clock given as flags, and prints the result in the
  notation of the conformance suite; `-ast`, `-bytecode` and
  `-sql sqlite|duckdb` show instead what the compiler makes of it.
- The release's benchmarks under `docs/perf/v0.1.0.md`: the VM against
  the budgets of ADR-0012, the compiler, and the throughput and the result
  of the acceptance campaigns of one million generated formulas on each
  SQL target.
- Size limits (ADR-0021). Every text a formula produces is at most the
  database's text limit, 65,536 code points by default, and every builder,
  `&`, `CONCAT`, `REPEAT`, `PAD`, `SUBSTITUTE`, `REPLACE` and `REGEXREPLACE`,
  decides `out_of_range` before it builds, on the VM, SQLite and DuckDB
  alike. A count past the end of a text now reads to the end instead of
  failing above a billion. The limit is a property of the database,
  carried by `formula.Limits`, read by the VM and passed to the dialects;
  the `limits` conformance suite runs at 64 code points and the property
  campaigns at 1,024 (`FADODB_FUZZ_MAXTEXT`).
- Property-based testing of the engine. `internal/formula/fuzz` generates
  random well-typed formulas with inputs and context and runs each on the
  Go VM and on a SQL dialect; the SQLite and DuckDB property tests run
  2,000 formulas in CI and a campaign of any size locally (`FADODB_FUZZ_N`,
  `FADODB_FUZZ_SEED`, `FADODB_FUZZ_WORKERS`). The campaigns found, and the
  branch fixes, with conformance cases for each: a blank-typed program did
  not survive encode and decode; `NOT(a) <= b` parsed as `NOT (a <= b)`,
  and `NOT(` is now the function form; `/`, `\` and `MOD` could overflow
  to infinity without `not_a_number`; `POWER` with a large integer
  exponent was hundreds of ulps off the C math libraries and is computed
  in double-double arithmetic; `LOG` was inexact at the powers of ten and
  wrong for a base below 1 on both dialects; `LN` and `LOG` of a subnormal
  number were the logarithm of the smallest normal number instead; and
  `ROUND`, `FLOOR`, `CEILING` and `TRUNC` beyond 22 digits scaled by a
  power of ten that differed by an ulp between Go and C. They now scale by
  the correctly rounded power on every evaluator and leave a value alone
  once its scaled form is already an integer. On SQLite, `^`, `POWER`, `LN`
  and `LOG` run through the engine's own functions, since SQLite's are the
  platform's and differ in the last bit between Linux, macOS and Windows,
  and so does the rendering of a number as text, since SQLite's `printf`
  rounds the fifteenth digit its own way; and the VM rounds every product
  explicitly, so that arm64's fused multiply-add cannot change a result.
  The million-formula acceptance campaigns found four more: `SUM` and
  `AVERAGE` now add left to right in plain arithmetic on every evaluator,
  since the compensated sum of the VM and SQLite differed from DuckDB's
  inline additions by far more than the tolerance when large terms cancel;
  a JSON array index in a `JSONVALUE` path is a JSON pointer index, digits
  without a leading zero or a sign, as DuckDB reads it; `DATE` on DuckDB
  carries months and days by civil-calendar arithmetic instead of clamped
  timestamp intervals, which could cancel into a date in range where the
  real one was not; and the property runner compares two noisy numbers
  glued into one text by trying every split.
- DuckDB dialect of the SQL generator and the `analytics/` module. The
  dialect computes in DOUBLE with every literal cast, builds dates as naive
  timestamps so the session time zone never leaks in, and ships its engine
  functions as `CREATE MACRO` statements, inlined SQL rather than Go
  callbacks, under the same `fado_<name>` names as SQLite's registered
  functions. The module is a separate Go module with cgo and holds only the
  DuckDB conformance runner; CI builds it on Ubuntu, and `make analytics`
  runs it locally. All evaluation cases now agree across the VM, SQLite and
  DuckDB, aggregates to the relative 1e-12 of ADR-0019.
- `REGEXREPLACE` takes its replacement literally, with no backreferences,
  so the three targets agree; the catalog and the suite say so.
- SQL generator with the SQLite dialect. A program becomes a value
  expression and an error expression over the columns holding its
  variables, with the short-circuit rules of ADR-0019 kept in both. The
  dialect uses native SQL where SQLite agrees with the VM and calls the
  engine's own Go functions, registered by `internal/formula/sqlitefn`,
  where it does not. The differential test runs every evaluation case on
  SQLite through `modernc.org/sqlite`, now a dependency of the core module.
- Binder, bytecode v1 and the Go VM. The binder gives every expression a
  static type by ADR-0019 and rejects type errors, unknown names and
  context functions in auto mode at definition time, with a column in every
  message. Bytecode v1 (`FADO`, versioned, binary and JSON debug forms)
  reserves the `lookup` and `query` opcodes of ADR-0020. The VM executes
  the scalar language with zero allocations on the scalar path and the
  closed list of error codes. 59 of the 67 catalog functions run; the
  composite and query functions are refused as not implemented yet.
- The conformance runner executes every evaluation case on the VM, and a
  binding suite pins the static typing rules and the auto-mode restriction.
- Formula lexer and parser for the grammar of ADR-0019, with positions in
  every error, and the function catalog (`internal/formula/catalog.json`,
  67 functions with their evaluation class, arguments and summary) that the
  parser checks arities against.
- The conformance suite under `conformance/`: 579 evaluation cases in seven
  suites (blank, logic, math, numeric, arithmetic, text, date) and a syntax
  suite of 220 accept and reject cases, with the loader that validates them.
  Until the VM lands, evaluation cases are checked to parse.
- ADR-0019: formula language semantics: grammar, static types, coercion,
  comparison, dates, text, composites and errors.
- ADR-0020: `LOOKUP` and `QUERY` execution: one engine call per invocation,
  point lookups off the analytical engine, set-wise recomputes as one
  statement per class, DuckDB dialect tested from Phase 1.

### Changed

- The benchmark gate warms both trees up and interleaves the measured
  rounds, so a shared runner's drift lands on the base and the change
  alike instead of on whichever tree ran first.
- ROADMAP and README mark Phase 1 as in progress.
- `modernc.org/sqlite` is scheduled for Phase 1 instead of Phase 2, for the
  SQL conformance runner.

## [0.0.1] - 2026-11-05

Project scaffold. Nothing to run beyond `fadodb version`.

### Added

- Go module, `cmd/fadodb` with `version` and `help`, package layout with
  documented placeholders.
- Quality gates: gofmt, vet, staticcheck, govulncheck, race tests on three
  platforms, dependency budget gate, binary size gate, benchmark regression gate.
- Release pipeline with GoReleaser: binaries for Linux, macOS and Windows on
  amd64 and arm64, with checksums.
- Public architecture overview and twelve architecture decision records.
- Contributing guide, code of conduct, security policy, issue and pull request
  templates, roadmap.

[Unreleased]: https://github.com/MojoHand70/fadodb/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.1.0
[0.0.1]: https://github.com/MojoHand70/fadodb/releases/tag/v0.0.1
