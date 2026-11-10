# Conformance suite

The suite is the specification of the formula language (ADR-0003). A
behaviour that is not in the suite is not promised. Every target, the Go
VM, the SQL generator on each dialect and the browser VM, runs the same
cases in CI; a frontend author can run them against their own port.

Semantics are fixed in ADR-0004 (numbers), ADR-0005 (blanks), ADR-0013
(types), ADR-0019 (grammar, coercion, comparison, dates, text, composites,
errors), ADR-0020 (LOOKUP and QUERY) and ADR-0021 (size limits). The
function catalog is `internal/formula/catalog.json`.

## Status

The Go VM and the SQL generator's SQLite and DuckDB dialects run every
evaluation case in CI, and all three must match exactly. SUM and AVERAGE
add left to right on every evaluator so that aggregates agree to the
last bit, rather than differing within what ADR-0019 allows them. The
browser VM does not exist yet.
Object field paths (ADR-0031) bind and evaluate on every target: a leaf
is one slot of the field's type. `LOOKUP` against an explicit table
(ADR-0016, ADR-0020, ADR-0037) binds and evaluates on every target: the
`lookup` suite declares its tables with their rows under `sources`, the
Go VM answers them from an in-memory executor and the SQL dialects from
the tables created in the database. Whole objects in a formula, `ISBLANK`
of an object, object equality and computed object variables are not
implemented yet, nor are the other composites, arrays and rows, and
`QUERY`; the binder refuses them, and the binding suite says so.

Known, documented differences of the SQL targets: `VALUE` and `DATEVALUE`
trim spaces only on DuckDB where the VM trims all whitespace; `UPPER` of
"ß" is "ẞ" on DuckDB and "ß" in the VM; an invalid `REGEXREPLACE` pattern
built at run time fails the statement on DuckDB instead of yielding
`bad_text`.

## Evaluation cases: `formula/*.json`

One file per topic:

```json
{
  "suite": "blank",
  "description": "A stored 0 is not blank.",
  "variables": {"Alfa": "number", "Nazwa": "text", "Due": "date"},
  "cases": [
    {"name": "blank reads as 0 in arithmetic",
     "formula": "Alfa + 1", "inputs": {"Alfa": null}, "expected": 1},
    {"formula": "DATE(2024, 2, 30)", "type": "date", "expected": "2024-03-01"},
    {"formula": "1 \\ 0", "error": "div_by_zero"}
  ]
}
```

- `variables` declares the inputs a case may set, by type: `bool`,
  `number`, `int`, `decimal(s)`, `money(s)`, `text`, `date`, `datetime`,
  `ref`, `json`, `array<scalar>`, `rows`, or `object<Name>` for a schema
  the suite declares.
- `schemas` declares object schemas (ADR-0031) by name: a field name to
  its type, where a field may be `object<Name>` of another schema. A
  formula reads a leaf as `shipping.city`; an input sets it under the
  same path, `"shipping.city": "Gdańsk"`.
- `inputs` gives values; an absent or `null` variable is blank. Dates are
  written `YYYY-MM-DD`, datetimes `YYYY-MM-DD HH:MM:SS` in UTC, exact
  values as text such as `"1.250"`, money as `"12.30 PLN"`.
- `context` sets what the context functions see: `user`, `groups`, `now`,
  and `state` and `id` for the document the formula runs over.
- Exactly one of `expected` and `error`. `expected` is the value in the
  notation of `inputs`; `null` is a blank result. `error` is one of the
  codes in ADR-0019 and ADR-0020.
- `type` pins the static type of the formula. A date result must declare
  `"type": "date"` so that `expected` can be written as text.
- `sources` declares explicit tables a `LOOKUP` may read (ADR-0020): by
  name, `columns` as name to scalar type, `key` as the columns that order
  the table and identify a row, and `rows` as objects in the notation of
  `inputs`, an absent column blank. Inside a `LOOKUP` a bare name is a
  column of the source and `THIS.name` a declared variable.
- `limits` sets the size limits the suite evaluates under, `{"max_text":
  N}` code points (ADR-0021); absent, the defaults. The `limits` suite runs
  at a small limit so that short texts reach the boundary.

Numbers compare exactly on every target.

## Statechart cases: `statechart/*.json`

The specification of the statechart's behaviour (ADR-0040), and of the
rights and transition rules enforced around it (ADR-0041, the `rights`
suite): a class, a script of commands with their principals, and the
expected outcome of each. The store runs every suite in CI; the server
and the browser VM run them with their phases.

```json
{
  "suite": "approval",
  "class": {"name": "Approval", "variables": {"amount": {"type": "number"}},
            "states": {"initial": "draft", "list": ["draft", "review"]},
            "transitions": [{"name": "submit", "from": "draft", "to": "review", "guard": "amount > 0"}]},
  "principals": {"alice": {"id": "alice", "groups": ["sales"]}},
  "script": [
    {"do": "create", "doc": "inv", "as": "alice", "values": {"amount": 5}, "expect": {"state": "draft", "version": 1}},
    {"do": "transitions", "as": "alice", "expect": {"transitions": [{"name": "submit", "to": "review", "enabled": true}]}},
    {"do": "transition", "transition": "submit", "as": "alice", "expect": {"state": "review", "version": 2, "events": ["created", "transition:submit"]}},
    {"do": "transition", "transition": "submit", "as": "alice", "expect": {"code": "wrong_state"}}
  ]
}
```

- `class` is the class definition the suite runs against; `principals`
  names the principals steps run as, each with `id`, `groups` and
  `admin`. A step without `as` runs as nobody. `clock` is the database's
  clock while the suite runs, as `YYYY-MM-DD HH:MM:SS` in UTC, the
  runner's default when absent.
- A step's `do` is `create`, `set`, `transition`, `preview`,
  `transitions`, `visits`, `get`, `query` or `count`. `values` are the
  assignments, `transition` the name of the transition a `transition` or
  `preview` step takes, `key` the command's idempotency key, `at` the
  moment the step runs at (ADR-0042) as `YYYY-MM-DD` or `YYYY-MM-DD
  HH:MM:SS` in UTC; `where`, `order` and `fields` are the filter of a
  `query` or a `count`. On a
  `create`, `doc` labels the document made; on another step it names the
  document acted on, the last one created when absent.
- `expect` is checked after the step. `code` is the refusal expected,
  and nothing else is then checked. Otherwise `state` and `version` are
  the document's after the step, `values` a subset of its values in the
  notation of the evaluation cases, `absent` and `present` variables that
  must be missing from or found in the values of the document, or of
  every document a query returned, `path` the states a preview visited
  with the state before first, `transitions` the listing by name, target,
  `enabled` and `code`, `history` the states of every history row of the
  document so far, `events` the kind of every event so far with a
  transition written `transition:name`, `visits` the rows of the
  `_states` view as `[state, entry_version, exit_version]`, the exit
  `null` while the visit is current, `docs` the labels of the documents a
  query returned in order, `count` the count, and `now` the moment a
  preview reports, in the notation of `at`.

## Rules cases: `rules/*.json`

The specification of the rules inside the commit (ADR-0043): which
triggers a commit raises and in which order, what a rule sees, the
synchronous actions, that rules do not cascade, what a failing action
does, and what a frozen document allows. The format is the statechart
suites', with these additions:

- A step's `do` may be `delete`, `reset` or `refresh`; `variables` are
  the variables a reset or a refresh names. Their events are written
  `reset` and `refresh`.
- In `events`, a custom event is written `custom:name`, after the event
  of the commit whose rule emitted it.
- Beside `code`, `action` is the path in the definition of the rule's
  action that refused the command, such as `/rules/2/do/0`.
- Of a `preview` step: `rules` are the paths of the rules that ran, in
  the order they ran across the command's commits; `skipped` the paths of
  the rules whose trigger was raised and whose condition was not TRUE;
  `queued` the actions for the outbox, each by its `action` name, in the
  order they would be queued; `errors` what the preview recorded and went
  on from, each by `code` and, where given, by `variable` and by the
  `action` path.

- `computed` are the moments of triggered variables by name, checked on
  the document a step returned or on every document a query returned:
  `version`, the version whose commit computed the value; `error`, the
  code of a recomputation that failed since; and, where given, `at`, the
  time of that commit in the notation of a step's `at`. `null` says the
  variable has no moment. A variable listed under `absent` must have no
  moment either.

- A suite may carry `classes`, definitions the store takes before
  `class`, in order: the classes its rules name (ADR-0044). A `query` or
  a `count` step reads the class its `class` names instead of the
  suite's own.
- The step `drain` runs every action of the outbox that is due, and
  `drained` is how many it attempted. The step `retry` makes the action
  at position `row` among those the document queued pending again. The
  runner opens the store with the worker off, so nothing runs between
  the steps.
- `outbox` are the actions the document queued, in order, each by
  `action`, `status` and `code`, the last error, and where given by
  `attempts`, `depth`, `as`, the principal it runs as, and `args`.
  `found` are the documents a query returned, in order, each by `state`
  and a subset of `values`.
- In `events`, the outcome of an action is written `action_run:name` or
  `action_failed:name`.

The store runs every suite in CI.

## Binding cases: `binding/*.json`

Formulas the binder must accept and formulas it must refuse at definition
time by the static typing rules of ADR-0019, in a `mode` of `any` (the
default), `auto` (ADR-0015) or `pure` (the class title of ADR-0018 and
the presentation formulas of ADR-0032). Same shape as the syntax cases,
plus `mode` and, as in the evaluation cases, `schemas` and `sources`
(whose rows may be left out).

## Syntax cases: `syntax/*.json`

Formulas the compiler must accept and formulas it must refuse at definition
time. The reason on a rejected formula is for people; error messages are
not part of the contract.

```json
{
  "suite": "grammar",
  "variables": {"Alfa": "number"},
  "accept": ["Alfa ^ 2", "IF Alfa > 0 THEN 1 ELSE 2"],
  "reject": [{"formula": "Alfa ^", "reason": "missing operand"}]
}
```
