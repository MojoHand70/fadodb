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
- `context` sets what the context functions see: `user`, `groups`, `kind`
  and `for`, what the user is and whom it acts for (ADR-0050), `now`,
  and `state` and `id` for the document the formula runs over. The life
  of that document (ADR-0047) is given beside them: `version`,
  `created_at`, `created_by`, `updated_at`, `updated_by`, `entered_at`,
  `entered_by` and `entered_from` for the functions of the row, and
  `events` for the functions of a state. `events` lists the events of
  the document's life in the order they happened, each with a `kind`
  of `created` (in the state `to`), `moved` (from `from` into `to`) or
  `changed`, its moment `at` and who made it, `by`, absent for nobody.
  Every evaluator folds them with the same code, so the cases specify
  the fold too. An empty list is a document nothing was told of, and no
  life at all is no document.
- Exactly one of `expected` and `error`. `expected` is the value in the
  notation of `inputs`; `null` is a blank result. `error` is one of the
  codes in ADR-0019 and ADR-0020.
- `type` pins the static type of the formula. A date result must declare
  `"type": "date"` so that `expected` can be written as text.
- `sources` declares explicit tables a `LOOKUP` may read (ADR-0020): by
  name, `columns` as name to scalar type, `key` as the columns that order
  the table and identify a row, and `rows` as objects in the notation of
  `inputs`, an absent column blank. Inside a `LOOKUP` a bare name is a
  column of the source and `THIS.name` a declared variable. In place of
  the column, `COUNT`, `SUM`, `MIN` or `MAX` stands over one expression
  of the columns (ADR-0046). The `lookup` suite also runs against the
  store, whose tables answer from an index or a prepared statement.
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
  `system`, the system groups it is in (ADR-0052). A step without `as`
  runs as nobody. Defining the suite's classes, draining its queue and
  reading its tables for the checks are the runner's own, as a caller
  in the system groups that work needs. `clock` is the database's
  clock while the suite runs, as `YYYY-MM-DD HH:MM:SS` in UTC, the
  runner's default when absent.
- A step's `do` is `create`, `set`, `transition`, `preview`,
  `transitions`, `visits`, `get`, `query`, `count` or `due`. `values` are the
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
  `enabled`, `code` and, for a timed transition, `fires`, the moment it
  fires in the notation of `at` with its time, absent where the
  transition is not timed or its moment never comes, `history` the
  states of every history row of the
  document so far, `events` the kind of every event so far with a
  transition written `transition:name`, `visits` the rows of the
  `_states` view as `[state, entry_version, exit_version]`, the exit
  `null` while the visit is current, `docs` the labels of the documents a
  query returned in order, `count` the count, `due` what a `due` step
  listed, each document as `[label, state, the state it would move to]`
  in order, with the moment of its timer as a fourth element, in the
  notation of `at` with its time, when a timed transition would move it,
  and `now` the moment a preview reports, in the notation of `at`.
- `due.json` is the suite of the list of what is due (ADR-0047, item
  8): which documents a commit would move at the clock and at a moment,
  that the list moves nothing and a refresh does, the order of two
  guards that hold, a guard that runs as a filter beside one evaluated
  document by document, and a terminal document, which is never due.
- `timed.json` is the suite of the timed transitions (ADR-0047, items
  9, 10, 13 and 14), as far as a commit fires them: a reminder with
  `after` that repeats and raises the rule of its name, an escalation
  with `at` from a moment that does not move, a deadline that is changed
  while the document waits, a document that leaves before its time, a
  guard beside a timer, a blank `at`, the moment each shows in the
  listing and in the list of what is due, and the origin `timer` of
  their events. Nothing in it moves a document by itself: the scripts
  with the worker in them are `rules/timers.json`.

## Access cases: `access/*.json`

The specification of the four rules of a class (ADR-0049): who may see,
create, change and delete a document, asked on every path. The format
is the statechart suites'. `rules.json` takes one class through every
command as five callers: its owner, another clerk, HR, a stranger and
a content administrator, and a call without a principal. A read, a command,
the listing of transitions, a filter, a count and the list of what is
due have to agree on which documents each caller sees; `action` on a
refusal is the place of the rule, such as `/access/update`.

`actors.json` is the suite of what a principal says of itself
(ADR-0050): a principal of a suite may carry `kind` and `for`, and
`actors` on a step checks the actors of the document's events so far,
each written as its id, then its kind and `for` whom where the host
said, as `agent-7 agent for ann`.

A suite of access cases says `"as_written": true`: its classes are
defined as they are written. Every other suite is not about who may,
and its runner writes `TRUE` for each access rule a class or a table
of the suite leaves out, so that the suite runs open to every caller,
a step without `as` too.

`left_out.json` and `left_out_partly.json` are the suites of a rule a
class leaves out (ADR-0052, item 5): a class with no `access` block,
which nobody but a content administrator reaches, and a class that
writes some of its rules, where the refusal names the one that is
missing and a transition without a rule of its own asks `update`.

`override.json` is the suite of the content administrator's override
(ADR-0052, item 4). A step may carry `reason`, the reason it gives for
an act past a rule, and `overrides` on a step checks the marks of the
document's events so far, in order: for an event of a command that got
past a rule the places of the rules, then the reason, as
`/access/read /access/update: the owner left the company`, and `""` for
any other.

`substitution.json` is the suite of acting for somebody (ADR-0053). Its
steps name the class `Substitution`, which the engine brings and no
suite defines, beside the suite's own. A refusal for a "for" that no
substitution covers has the code `forbidden` and `/principal/for` as its
`action`. A suite may say `"host_answers_for": true`: its store is
opened for a host program that keeps its own substitutions, and no
"for" is checked. `actors.json` gives its assistants their substitutions
in its first two steps.

`groups.json` is the suite of the system groups as the formulas of a
class see them (ADR-0052): `INGROUP` of a system group and of
`everyone`, on a read by id, in a command and in a filter; a call
without a principal, which is in no group; a content administrator,
who passes the rules of the class and never a guard; and a designer, a
database administrator and an analyst, who pass no rule by being one.

## Rules cases: `rules/*.json`

The specification of the rules inside the commit (ADR-0043): which
triggers a commit raises and in which order, what a rule sees, the
synchronous actions, that rules do not cascade, what a failing action
does, and what a frozen document allows. The format is the statechart
suites', with these additions:

- `life.json` is the suite of the functions of the row in triggered
  variables, rules and actions (ADR-0047), and `statechart/stays.json`
  the one of entering a state. Three expectations serve them: `life`
  checks what the document keeps of its life, by name; `origins` the
  origin of every event that enters a state, in order; `stays` the rows
  of the `_states` view as state, then who, from where, by which
  transition and how for the entry, then who, by which transition and
  how for the leaving.
- `states.json` is the suite of the functions of a state (ADR-0047,
  item 6) in triggered variables, guards, access rules and rules: what
  the log has and the steps of the command in progress, a transition
  back into the same state, who counts as an editor, and a filter that
  is refused them.
- A suite with `"together": true` defines its `classes` and its `class`
  in one call (ADR-0046, item 12), as classes that name each other must
  be.
- `reads.json` is the suite of formulas that read the documents of a
  class (ADR-0046): parallel approvals, a class that counts over itself,
  what a lookup sees of its own document and of deleted and finished
  ones, and that rights do not narrow what a formula reads.
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
  `queued` the actions for the outbox, each by its `action` name and,
  where given, by the `args` the preview evaluated, in the order they
  would be queued; `errors` what the preview recorded and went
  on from, each by `code` and, where given, by `variable` and by the
  `action` path.

- `computed` are the moments of triggered variables by name, checked on
  the document a step returned or on every document a query returned:
  `version`, the version whose commit computed the value; `error`, the
  code of a recomputation that failed since; and, where given, `at`, the
  time of that commit in the notation of a step's `at`. `null` says the
  variable has no moment. A variable listed under `absent` must have no
  moment either.

- A suite may carry `tables` and `classes`, definitions the store takes
  before `class`, in order: the tables its rules write and the classes
  they name (ADR-0044). A step acts on the class its `class` names
  instead of the suite's own. A `create` may give its document an `id`,
  and any other step may name by `id` a document the script did not
  create: one an action made.
- The step `import` writes `rows` into `table`, and the step `rows`
  reads `table`; its `rows` are the rows expected, in key order.
- The step `drain` runs every action of the outbox that is due, and
  `drained` is how many it attempted. The step `retry` makes the action
  at position `row` among those the document queued pending again. The
  runner opens the store with the worker off, so nothing runs between
  the steps.
- `outbox` are the actions the document queued, in order, each by
  `action`, `status` and `code`, the last error, and where given by
  `attempts`, `depth`, `as`, the principal it runs as, `args`, and
  `due`, the moment it may next run, in the notation of `at` with its
  time: for a wake-up, the action `timer`, the moment of its timer.
  `found` are the documents a query returned, in order, each by `state`
  and a subset of `values`.
- In `events`, the outcome of an action is written `action_run:name` or
  `action_failed:name`.

- A suite may carry `actions`, action definitions the store takes before
  the classes (ADR-0045). The runner implements each with a function
  that records every call it receives and then does what `functions`
  says for the action's name: `fails` is how many attempts get no answer
  first, `refuses` the words of a refusal, and `returns` what it returns
  when it runs. An action `functions` does not name runs and returns
  nothing.
- `calls` are the calls those functions received during the step, in
  order, each by the properties given, to any depth: `key`, `attempt`,
  `action`, `args` and `event`, and of the event and of its `document`
  any part, in the published shape of `api/call.schema.json`. A property
  expected as `null` may be absent. `"calls": []` says nothing was
  called.
- The step `wait` moves the database's clock on by `seconds`, so that an
  action waiting for its next attempt, or a wake-up waiting for its
  moment, falls due.
- `timers.json` is the suite of the wake-up (ADR-0047, items 10 to 12
  and 17), with scripts that move the clock and drain: a transition
  after a time, at a date the document holds, a reminder that repeats,
  a date that is changed while the document waits, a document that
  leaves before its time, a worker that was stopped, and a commit that
  touches the document first; the wake-ups each commit queues, with
  their moments and who they act as; and that a wake-up with nothing to
  do writes nothing.
- The step `try` runs the action `action` once with the values under
  `args`, and `outcome` is what the trial answered, by the properties
  given; a refusal of the trial itself is a `code`. The step
  `preview_action` asks what the action would be called with, and `call`
  is that call. The step `actions` reads the catalogue: `catalogue` are
  entries of it, each found by its `name` and compared by the properties
  given.

- A suite with `outbound` lets its store send the requests of declared
  actions (ADR-0045, item 7), to a network the runner stands in for:
  `hosts` are the hosts a request without a connection may go to and
  `connections` the hosts of each connection. Without it the store sends
  nothing. `secrets` are the secrets the runner supplies, by connection
  and name. `replies` are what the network answers: each has a `status`,
  200 when absent, a `body` in JSON or a `text`, a `retry_after` in
  seconds, or `no_reply` with the words of a failure to reach the
  receiver; a reply answers the next request to its `url`, or the next
  request at all when it has none, and a request no reply is left for is
  answered 200 with no body.
- `requests` are the requests the store sent during the step, in order,
  each by the properties given: `method`, `url`, `connection`, `headers`
  as an object by name, and `body`, the JSON sent, or `text` for a body
  that is not JSON. A property expected as `null` must be absent.
  `request` is the request a `preview_action` would send, in the same
  shape, with every secret masked.
- Of an action in `outbox`, `into_code` is why its result was not
  written back to the document (ADR-0045, item 8), checked when given:
  `""` says the result was stored, or that the action has no `into`. Of
  an action in a preview's `queued`, `into` are the variables its result
  will be written to, checked when given.

The store runs every suite in CI.

## Binding cases: `binding/*.json`

Formulas the binder must accept and formulas it must refuse at definition
time by the static typing rules of ADR-0019, in a `mode` of `any` (the
default), `auto` (ADR-0015) or `pure` (the class title of ADR-0018 and
the presentation formulas of ADR-0032). Same shape as the syntax cases,
plus `mode` and, as in the evaluation cases, `schemas` and `sources`
(whose rows may be left out). A suite with `secrets`, a list of names,
binds its formulas as the request of an action definition binds them
(ADR-0045): `SECRET("name")` reads one of those secrets. Without
`secrets`, as everywhere else, `SECRET` is refused. A source with
`"class": true` stands for the documents of a class (ADR-0046), which
an auto formula may not read, and `indexed` names its columns that lead
an index. A suite with `states`, a list of names, binds its formulas as
those of a class with these states (ADR-0047): a function of a state
that names a state as text must name one of them, and an empty list is
a class without states. Without `states` no name is checked.

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
