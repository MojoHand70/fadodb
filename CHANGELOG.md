# Changelog

All notable changes to FadoDB are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- The manual, under `docs/`: its contents page with every chapter
  planned, and the first three written: your first application, an
  invoice with its own editors in each state, from an empty file to a
  working process; the class definition, block by block and word by
  word; and the glossary. Two more follow them: what FadoDB is, and
  how it works, a commit followed step by step from the call to the
  answer. A test runs every example of a chapter
  against the engine on each push, and another follows every link.
- `examples/first`, the program of the first application:
  `go run ./examples/first`.
- Five guides in the manual, one task each, under `docs/manual/guides`:
  give each state its own editors, make a field read-only after
  approval, four eyes, a reminder every seven days with a deadline,
  and let somebody act for a person. A test defines the class of each
  guide and plays its table of who does what against the engine.

### Security

- A new database is closed to everybody but its owner. `Open` creates
  the file readable and writable by the user that made it and by nobody
  else, where it was readable by every user of the machine; the log
  SQLite keeps beside it follows, and so does the directory of
  attachments of a database that keeps them outside the file. A
  database that exists keeps its permissions: `chmod 600` on the file
  and `chmod -R go-rwx` on its `.blobs` directory close one made by an
  earlier version. On Windows nothing changes: a file takes the access
  list of its directory.

## [0.5.0] - 2026-11-13

### Added

- The four rules of a class are enforced (ADR-0049): `read`,
  `create`, `update` and `delete` under `access`, each a condition over
  the document for the caller. A document its caller may not see is
  answered as one that does not exist, `unknown_document` in the same
  words, by `Get`, `Version`, `Transitions`, `Preview`, `OpenFile` and
  every command that names it; `History` and `Events` answer nothing,
  and `Query`, `Count` and `Due` leave it out, the rule being one more
  predicate of the filter. `TransitionMany` reports it as
  `unknown_document`. The history of a document is read under the rule
  as the document is now, and over its last version once it is
  deleted.
- A caller who sees a document and may not do what they ask is refused
  with `forbidden`, and `Error.Path` names the rule: `/access/create`,
  `/access/update`, `/access/delete`, `/access/transition/<name>`. The
  hint is the rule. `create` is asked over the new document with its
  defaults and its computed values, `update` and `delete` over the
  stored one. `Set` and `Reset` ask `update`; `Refresh`, and a set that
  names no value, ask only to see the document.
- A command whose caller may not see what it left is answered with the
  document's id, version and state and no values, and a repeat of its
  key the same way.
- `PreviewDelete` says whether a delete would be refused and which
  rules it would run, and writes nothing. `Preview` reports a refusal
  of `create` or `update` under its errors and goes on.
- `OpenFile` asks the read rule of the document and the read right of
  the variable that holds the file.
- `conformance/access/` is the suite of the rules: every path, for every
  caller. `BenchmarkTransitionAccess` is in the benchmark gate: a
  transition on a class with rules allocates what it does without them.
- A filter decides the caller's part of a read rule before it writes
  its statement (ADR-0049, item 5). `INGROUP("finance")` has one answer
  for the whole statement, so `owner = USER() OR INGROUP("finance")` is
  nothing at all for a member of finance and `owner = USER()` for
  anybody else, and a statement is kept for each outcome, not for each
  caller. A caller who sees only their own documents lists and counts
  them through the index on the owner, at the cost of the same
  condition written out.
- A text variable compared with the caller, `owner = USER()`, is
  answered from the variable's index in any filter, in a `where` as in
  a read rule. Before, such a filter read every document of the class.
- A principal says what it is and for whom it acts (ADR-0050):
  `Principal.Kind` is `ActorPerson`, `ActorAgent` or
  `ActorService`, or empty when the host program does not know, and
  `Principal.For` is the id of whoever the caller acts for. The engine
  trusts the kind as it trusts the id, and checks the "for" against a
  substitution (ADR-0053, below). Any other kind is refused with
  `bad_value`.
- Two functions answer them to a formula, wherever `USER()` may stand:
  `USERKIND()` and `ACTINGFOR()`, each blank when the host program said
  nothing. `USER()` stays the one who acts. A step kept for people is
  `USERKIND() = "person"`; an assistant sees what its principal owns
  where the rule says `owner = ACTINGFOR()`. They evaluate alike on the
  Go VM, SQLite and DuckDB, with cases in
  `conformance/formula/context.json`.
- Every event records `actor_kind` and `actor_for` beside `actor_id`:
  `Event.ActorKind` and `Event.ActorFor`, the `_events` view, the
  `_states` view as `entered_by_kind`, `entered_for`, `left_by_kind`
  and `left_for`, and the event an action receives, in
  `api/event.schema.json`. An action and a timer run as whoever set
  them off, with what that caller was; `QueuedAction` shows it.
  `conformance/access/actors.json` is the suite.
- Four system groups hold the work that is not about one document
  (ADR-0051, ADR-0052), with fixed names: `designers` define classes,
  schemas, tables and actions, preview a draft and migrate;
  `administrators` operate the outbox, `Drain`, `PreviewAction`,
  `TryAction` and `CheckBlobs`; `analysts` run `SQL`, which reads every
  row and every variable; and `content-administrators` see every
  document and act on it past the rules of its class, never past a
  guard. The host program gives a caller its system groups in
  `Principal.System`, apart from `Principal.Groups`: a group of its own
  that has one of these names is refused with `bad_value`, so that no
  directory group gives such work by its name. Anybody else is refused
  with `forbidden`, and the refusal names the group, as
  `/system/designers`. Nobody is in a system group unless the host
  program says so, and a call without a principal does none of this
  work. The constants are `GroupAdministrators`, `GroupDesigners`,
  `GroupContentAdministrators`, `GroupAnalysts` and `GroupEveryone`.
- `INGROUP` answers a system group as it answers any group, and answers
  `everyone` for every caller that has an id, on the Go VM, SQLite and
  DuckDB. A class opens to every named caller with
  `INGROUP("everyone")`. `conformance/access/groups.json` is the suite,
  with ten cases in `conformance/formula/context.json`.
- `DefineClass`, `DefineClasses`, `DefineSchema`, `DefineTable`,
  `DefineAction`, `Outbox`, `RetryAction`, `Drain`, `PreviewAction`,
  `TryAction`, `CheckBlobs` and `TableRows` take the options of a
  call, and with them `As`.
- A table has two rules (ADR-0051, item 7): `"access": {"read": ...,
  "write": ...}` in its definition, each a condition over the caller
  and the clock, about the table as a whole. `TableRows` asks `read`;
  `ImportRows` asks `write`, and so do the `table.insert`,
  `table.update` and `table.delete` actions of a rule, as the person
  who set the rule off. A formula goes on reading a table with the
  engine's eyes.
- A content administrator's act past a rule is an override, with a
  reason and a mark (ADR-0052, item 4). The rule is asked first: where
  it lets the caller through, the command is an ordinary one. Where
  only the group does, the command needs `Reason("...")` or is refused
  with `reason_required` and the place of the rule, and the event of
  its commit keeps the rules lifted and the reason: `Event.Override`,
  as `/access/read /access/update`, and `Event.Reason`, both columns of
  the `_events` view. A write past a right is one too, as
  `/rights/draft/secret/write`. `Preview.Override` lists the rules a
  command would lift and `TransitionInfo.Override` the rule a
  transition would, so a caller knows before it acts. What the commit
  queued acts on another document for the same reason. A guard, the
  statechart and a required value hold as for everyone, and a read by
  override asks no reason and leaves no trace.
  `conformance/access/override.json` is the suite.
- Every version of a class, a schema, a table and an action records who
  defined it (ADR-0052, item 7; ADR-0051, item 5): the caller's id,
  what it was, a person, an agent or a service, the id it acted for,
  and when. `Definitions(ctx, what, name)` lists the versions of one,
  oldest first, and is open to every caller; `Class.Defined`,
  `Table.Defined` and `ActionInfo.Defined` say it of the current
  version. A definition that is the stored one is no new version and
  changes no record.
- Checking a class notes each access rule it leaves out (ADR-0052, item
  5): `ValidateClass` and `ValidateClasses` answer a finding with the
  code `rule_left_out`, the place of the rule, what it means and what
  to add, and `Note` set. A note refuses nothing: `Diagnostics.Refuses`
  tells whether a definition is refused, `Diagnostics.Err` is nil when
  it is not, and the class a `DefineClass` returns carries its `Notes`.
  `conformance/access/left_out.json` and `left_out_partly.json` are the
  suites.

- Acting for somebody is checked (ADR-0053). A caller that names
  somebody in `Principal.For` is let through only while a substitution
  says so, on every call, a read as a command; otherwise the call is
  refused with `forbidden`, the path `/principal/for`, and a message
  that says what is missing: no substitution, one that begins later,
  one that has ended, or one for other classes. A caller that acts for
  nobody is asked nothing.
- A substitution is a document of the class `Substitution`, which the
  engine brings to every database: `by` may act for `for`, from `from`
  until `until`, on `classes` or on every class. The person gives one
  for themselves and withdraws it at any time; a database
  administrator gives one for somebody who cannot, with a `reason` and
  never to themselves. Nobody passes one on, nothing changes one and
  nothing deletes one. It is `planned`, `active`, `ended` or
  `withdrawn` and moves by itself at its dates, while the check reads
  the dates and waits for no worker. `fado.SubstitutionClass` and the
  names of its states are constants.
- The name `Substitution` is the engine's: a definition of that name is
  refused with `reserved_name`. A content administrator sees every
  substitution and acts past no rule of the class.
- `Options.HostAnswersFor` is for a host program that keeps its own
  substitutions: the engine records the "for" it is told and checks
  nothing, as before.
- `conformance/access/substitution.json` is the suite, and
  `BenchmarkTransitionFor` measures a transition by a substitute: the
  check is one lookup in memory and allocates nothing.
- The Approval sample accepts whoever validly acts for the approver,
  with `approver = USER() OR approver = ACTINGFOR()`.
- `docs/perf/v0.5.0.md` has the measurements of the phase against the
  budgets of ADR-0049 to ADR-0053, with the campaigns of the earlier
  phases run again. `TestAccessCampaign` is the campaign of access,
  and `BenchmarkTransitionOverride` measures a content administrator's
  transition past a rule.

### Changed

- **Breaking for a host that sets `Principal.For`:** the engine now
  checks it. Give the caller a substitution, or open the database with
  `Options.HostAnswersFor` when the host program keeps its own.
- Every database has the class `Substitution`, listed first by
  `Classes`. `fadodb check` counts it.

- A named transition without a rule of its own asks the `update` rule
  of its class, where the class has one. Before, it was open to every
  caller. A transition with a rule of its own is asked that rule alone.
- A rule's action on another document is refused by that document's
  class rules as it is by its rights (ADR-0044, item 11): the row is
  dead with `forbidden`. A `refresh` of another document asks its
  `read` rule. The wake-up of a timer, an eventless transition and the
  result an action writes back ask nothing.
- **`Principal.Admin` is gone**, and with it the one caller who did
  everything (ADR-0052). What it did is split among the four system
  groups above, and a caller that needs all of it is in all four. A
  program's own setup code names itself as a caller in the groups it
  needs; `fadodb check` acts as a database administrator.
- **The engine schema is version 9.** Version 6 added what the actor of
  an event was and for whom it acted, two columns of the event; version
  7 drops the administrator's flag from the outbox row, which keeps the
  system groups of whoever queued it with the other groups; version 8
  adds the rules an override lifted and its reason to the event; version
  9 adds the table of who defined each version. A
  database made by an earlier engine, v0.4.1 included, is refused when
  opened, as before 1.0 there is no upgrade path.
- A class that declared `read`, `create`, `update` or `delete` had them
  checked and not enforced; they are enforced now.
- **A rule a class leaves out lets nobody through** (ADR-0052, item 5,
  the owner's ruling). A class with no `access` block holds documents
  that nobody but a content administrator creates, sees, changes or
  deletes; a class that writes `read` and leaves out `update` is seen
  and not changed. The same holds for the `read` and the `write` rule
  of a table. The refusal names the rule that is missing and what to
  add. `"read": "INGROUP(\"everyone\")"` opens a rule to every caller
  that has an id, and `"TRUE"` to a call without a principal as well.
  A class without states is asked `read` and `create` by its previews.
  Every sample writes its rules: an invoice is created and changed by
  sales while it is a draft, approved and paid by finance, seen by
  both and by its customer, and deleted by nobody.

## [0.4.1] - 2026-11-13

### Added

- `TransitionMany` moves many documents by one call (ADR-0048): up to
  10,000 documents of one class, named by their ids, along one
  transition, in the order given and in one transaction. Each move is
  the commit `Transition` makes, with its own guard, rights, rules,
  version, history row and event. A document that cannot move does not
  stop the others: the reply has one `MoveOutcome` for each id, the
  document after its move or the refusal with its code, and a refused
  document is left as it was. The call's own error is for the call:
  `unknown_class`, `unknown_transition`, `too_many_elements`,
  `bad_value` for an id named twice, `not_allowed` for `At`, or a
  failure of the database, after which nothing has moved. `Key` is held
  for each document, as the key, `#` and the id, so the call sent again
  moves no document twice. The values are the same for every document,
  and a guard that counts other documents sees the moves made earlier
  in the same batch.
- `PreviewMany` runs the same batch and rolls it back: what the call
  would answer for every document, now or at a moment given with `At`,
  with nothing written and no action queued.
- Measured on disk at 1,000,000 documents: 27,257 documents a second in
  batches of 1,000, against 12,427 a second by `Transition`, a command
  each; a million documents in 37 seconds where they took 80. The
  budget of ADR-0048 is 15,000. `BenchmarkTransitionMany` is in the
  benchmark gate, and the campaign of commits has two more rows: the
  batch, and single commits made while batches run. The page is
  `docs/perf/v0.4.1.md`.

## [0.4.0] - 2026-11-12

### Added

- A transition may carry its time (ADR-0047, items 9, 13 and 14):
  `"after": "30d"`, a length of time in its state as a whole number and
  `d`, `h`, `m` or `s`, or `"at"`, a formula over the document that
  gives a date or a datetime. It needs neither a name nor a guard, and
  a guard beside the timer is still asked. Such a transition fires in
  the chain of the first commit that touches the document once its time
  has come, in the order of the class with the eventless ones; its
  event has the origin `timer`, and with a name it raises the rules and
  the triggered variables of that name. A transition back into its own
  state with an `after` repeats, which is how a reminder is written. A
  named one may be taken by a command before its time. Refused when the
  class is defined: both on one transition, an `after` that is no
  length of time, an `at` that reads the clock, the user, another
  document or a table, an `at` on a transition into its own state, and
  a timed transition out of a terminal state.
- The engine moves a document by itself when its timer comes
  (ADR-0047, items 10 to 12). A commit that leaves a document in a
  state queues a wake-up, a row of the outbox, for each timed
  transition leaving it, due at its moment; a commit that changes a
  date an `at` reads queues one for the new moment. When a wake-up is
  due the worker touches the document: what fires in the chain is
  written, each transition a version with the origin `timer`, and when
  nothing is to do, because the document has left the state or the
  stay, the moment has moved to later or a guard does not hold, the row
  is done and nothing is written. The touch and the mark are one
  transaction, so a timer fires exactly once, and a timer the worker
  missed fires at the next commit, so a stopped worker loses nothing. A
  wake-up acts as the person whose commit queued it: `USER()` in the
  rules of the move is theirs, and the actions those rules queue run
  with their rights. `Outbox` and `<Class>_actions` list wake-ups as
  the action `timer`, with their moment as `due_at`. `Drain` runs what
  is due by the database's clock, so a copy of a database opened with
  `Options.Now` plays its timers at the moment a simulation chooses.
- `Transitions` shows a timed transition with `fires_at`, the moment
  its time comes, one without a name too. `Due` lists the documents a
  timed transition would move, each with `since`, the moment since
  which it has been due.
- `Due` lists the documents of a class that a commit would move if it
  touched them now, or at the moment given with `At` (ADR-0047, item
  8): each with its state and the state it would move to, the target of
  the first eventless transition leaving its state whose guard is TRUE.
  A document becomes due when a guard comes to hold with no commit of
  its own: the clock passed a date, or a document or a table the guard
  looks up changed. The list is in the order of the class's states and
  then of the ids, at most 10,000 documents in one call, and it writes
  nothing: a commit that touches the document moves it, and a refresh
  is one. A guard that translates to SQL runs as one filter over the
  documents of its state; a guard that looks another document up or
  calls a function of a state is evaluated document by document, as is
  a document an older version of the class computed. A terminal
  document is never due.
- The functions of a state (ADR-0047, item 6). Each takes the name of a
  state: `FIRSTENTERED` and `FIRSTENTEREDBY`, `LASTENTERED` and
  `LASTENTEREDBY`, `FIRSTLEFT` and `FIRSTLEFTBY`, `LASTLEFT` and
  `LASTLEFTBY`, when and by whom the document first and last entered
  the state and left it; `ENTRIES`, how many times it entered it;
  `DAYSIN`, the days it spent there in all, up to now when it is there;
  `FIRSTEDITOR` and `LASTEDITOR`, who first and last created the
  document there or changed it there with a set or a reset; who moved
  it in or out is not an editor. They are context functions, on
  the Go VM, SQLite and DuckDB, for a default, a triggered variable, a
  guard, a rule, a transition rule and a write right. A transition back
  into the same state is an entry. A state written as text is checked
  against the class's states when the class is defined; a computed one,
  as in `DAYSIN(STATE())`, is looked up when the formula runs. They
  read the document's own events once for a command, with the step the
  command is making. A read rule, a read right and a filter are refused
  them: the `_states` view has the same facts for every document.
- The event of a creation names the state the document was created in,
  under `to_state`, as the event of a transition names the one it
  entered.

- The document keeps who, and where from (ADR-0047, items 3 and 4):
  `created_by`, `updated_by`, `state_entered_by` and
  `state_entered_from` join the system variables, on the document, on
  every version in the history, in the views, as columns a `LOOKUP`
  over the class reads, and in the document on the wire. Each is the
  principal of the command, blank when it named none. The names are
  reserved.
- A transition back into the same state is an entry like any other, and
  the `_states` view has a row for every entry. It gains `entered_by`,
  `entered_from`, `entered_via` and `entered_how` for the move that
  began the stay, and `left_by`, `left_via` and `left_how` for the one
  that ended it.
- Every event that enters a state says how the move came about, under
  `origin`: `command` when somebody asked for it, `action` when a rule
  of a document did through the outbox, `guard` when an eventless
  transition fired. `Events`, the event an action receives and the
  `_events` view carry it.
- The functions of the row (ADR-0047, item 5): `CREATEDAT()`,
  `CREATEDBY()`, `UPDATEDAT()`, `UPDATEDBY()`, `ENTEREDAT()`,
  `ENTEREDBY()`, `ENTEREDFROM()` and `VERSION()`, context functions
  like `STATE()` and `ID()`, on the Go VM, SQLite and DuckDB. "Seven
  days in this state" is `NOW() >= ADDDAYS(ENTEREDAT(), 7)`; four eyes
  is `CREATEDBY() <> USER()`. In a rule or a triggered variable of an
  exit or of a transition they tell of the stay that ends, as `STATE()`
  does of the state left.

- `COUNT`, `SUM`, `MIN` and `MAX` in the column position of a `LOOKUP`
  (ADR-0046), over an explicit table: `LOOKUP(Stock, SUM(qty), sku =
  THIS.sku)`. The function runs over one expression of the table's
  columns, for every matching row, and answers what it answers over the
  same values inside one document: blanks skipped, exact values exact
  and of their type, money within one currency. With no matching row
  `COUNT` is 0, `SUM` is 0, or blank for money, and `MIN` and `MAX` are
  blank. Numbers are added one by one in key order; an exact sum fails
  with `overflow` only when the total does not fit. An aggregate fails
  where its function fails, and with the error of the first matching row
  on which its expression fails. `AVERAGE` is refused with the hint to
  divide a `SUM` by a `COUNT`. A class as the source comes with the next
  part of the step.
- `DefineClasses` and `ValidateClasses` (ADR-0046, item 12): several
  class definitions in one call, each checked against the others and
  against what is stored, all stored or none. Classes that name each
  other, an approval whose rule refreshes its purchase and a purchase
  whose formula counts its approvals, could not be defined before,
  since neither could be first. The path of a finding starts with the
  position of its definition, `/1/variables/total`; when a definition is
  wrong by itself only its own findings are reported; a definition that
  is the stored one is left as it is. `DefineClass` behaves as it did.
- `examples/classes/purchase.json` and `approval.json`: parallel
  approvals written out, walked through by `TestExampleApprovals`.
- A formula reads the documents of a class (ADR-0046):
  `LOOKUP(Approval, COUNT(id), purchase = ID() AND state = "granted")`.
  The source is the class's documents as they are now, those in a
  terminal state included and deleted ones not: the system columns `id`,
  `title`, `state`, `version`, `created_at`, `updated_at` and
  `state_entered_at`, and every scalar variable, ordered by `id`. `ID()`
  and `STATE()` inside the call are the reading document's own. The
  formula sees every document, whoever caused it to be computed and
  whatever the rights say; the rights on the variable that holds the
  result decide who sees it. It may stand in a triggered variable, a
  default, a guard, a rule and an access rule other than `read`, and is
  refused in an auto formula. It reads what was stored before the
  command began and is recomputed at its own triggers only, so the other
  class's rule refreshes the reader. It runs as one statement on the
  writer inside the commit, narrowed through the index of an indexed
  variable: 10.7 µs to count the approvals of one purchase among
  100,000, and 80 µs for the whole refresh that counts and commits.
- A variable computed from the documents of a class declares its `type`
  (`missing` at `/variables/<name>/type`, with the type in the hint):
  the layout of its documents must not follow another class. A
  redefinition of a class that would break a formula of another class
  which reads it is refused with `not_allowed`, naming the formula.
- Among the equalities of an `AND`, a lookup takes for its key the one
  on a column that leads an index, wherever it stands in the condition.
- `conformance/rules/reads.json`, `conformance/binding/lookup-class.json`,
  and the formula suite's lookups run against document tables too.
  `BenchmarkLookupClass` joined the benchmark gate.
- A lookup is narrowed by its key when the equality of a column with an
  expression of the document is one term of an `AND`, not only when it
  is the whole condition: `sku = THIS.sku AND min_qty <= THIS.qty` reads
  the rows of that `sku` through the table's index, 2.2 µs where the
  whole table of 1,000 rows took 33 µs.
- `conformance/formula/lookup.json` gained the aggregates and runs on
  the Go VM, SQLite and DuckDB, and now against the store's own tables
  too, with the index and without.

- Rules run inside the commit (ADR-0043). Every commit raises a fixed
  list of triggers: `create` and `enter` of the initial state on a
  create; `values_change` on a set; `values_change`, `exit`,
  `transition`, `enter` on a transition; `exit` and `enter` on each
  eventless transition, which is a commit of its own; `delete` on a
  delete. Rules run trigger by trigger and in declaration order, each
  over the document as computed so far; a condition that errors is
  FALSE. Rules do not cascade inside a commit.
- The synchronous actions `set`, `reset` and `emit`. A `set` evaluates
  every formula before it assigns any and is the engine's write, so no
  write right is consulted; a `reset` re-evaluates defaults and blanks an
  input without one; `emit` appends an event of kind `custom` with the
  name the rule gives, caused by the commit's event. An action that fails
  refuses the whole command; `Error` gained `Path`, the place of the
  action in the definition, such as `/rules/2/do/0`.
- `Preview` and `PreviewDraft` report `Rules`: every rule whose trigger
  the command raised, the commit it belongs to, whether it ran, and its
  actions with their evaluated arguments.
- `conformance/rules/`, suites in the script format of the statechart
  suites, and `BenchmarkRules`, which joins the benchmark gate: the
  rules of a commit do not allocate.

- Triggered variables are computed (ADR-0043). A variable with
  `"mode": "triggered"` recomputes in the commit that raises one of its
  triggers, `enter:`, `exit:` or `transition:`, after the state change
  and before the rules, and when the `refresh` action names it; it keeps
  its value in between. Its moment is stored beside it in two columns,
  `<name>__asof` and `<name>__error`; the views add the time as
  `<name>__at`, and `Document` gained `Computed`, the version, the time
  and the error per variable, hidden with the variable by its read
  right. A recomputation that errors leaves the value as it was, records
  the code and refuses nothing.
- The `refresh` action of a rule, which recomputes the triggered
  variables it names, or every one that lists `refresh`.
- `Reset` and `Refresh` as commands of the Go API (ADR-0043), each one
  commit with a key, a principal, a business date and an event of kind
  `reset` or `refresh`. A `Reset` puts the named inputs back to their
  defaults and is the caller's write, checked against the write rights;
  one that changes nothing commits nothing. A `Refresh` recomputes the
  triggered variables it names, or every one that lists `refresh`, always
  commits, and is followed by the eventless transitions: it is how a
  caller makes a document look at the clock. Both raise `values_change`
  of what changed and are refused on a terminal document.

- The outbox (ADR-0044), with `create` as its first action. A rule's
  `create` of a document of another class is evaluated by the commit and
  written as a row of the `outbox` table in the commit's transaction. A
  worker, started by `Open` unless `Options.Outbox.Manual` is set, or
  `Drain` on the caller's goroutine, makes the new document's commit,
  marks the row done and appends `action_run` to the causing document in
  one transaction: the action happens exactly once, which
  `TestOutbox_SurvivesACrashAtEveryStep` shows by stopping the process
  after the commit, inside the action and after it. The action runs as
  the person whose command queued it, on the business day of the event
  that caused it; what the engine refuses is a dead row with the code
  and an `action_failed` event; a failure of the store is tried again
  with a doubling wait, `Options.Outbox.MaxAttempts` times. A chain of
  actions stops sixteen deep with `too_deep`.
- `Outbox` lists the queued actions, `RetryAction` makes a dead one
  pending again, and every class has the view `<Class>_actions`.
  `Preview` reports what a command would queue with the evaluated
  arguments. `Event` gained `Code`, the code an action died with.
- `examples/classes/receipt.json`, the class the README's Invoice
  creates a document of.

- The rest of the engine's own outbox actions (ADR-0044, items 7, 12
  and 13), each run exactly once and as the person whose command queued
  it:
  - `transition` takes a named transition of the document its `id`
    names, of the rule's own class unless it names another, with
    `values`. The access rule of the transition and the write rights of
    the target apply to the person.
  - `refresh` with an `id` sends the `Refresh` command to another
    document: the triggered variables it names recompute, and the
    document then moves by its own guards. A child wakes its parent
    this way.
  - `create` with `each` and `as` makes one document for each element of
    an array or a rows variable: `"each": "approvers", "as": "approver"`.
    The formulas under `values` and `id` read the element by that name,
    a rows element by field, `line.sku`. Every child is a row of the
    outbox of its own, with its own outcome event.
  - `table.insert`, `table.update` and `table.delete` change one row of
    an explicit table, addressed by its key. An insert of an existing
    key is refused with `duplicate`, an update of a missing one with the
    new code `unknown_row`.
- Three conformance suites under `conformance/rules/`: `each`, `others`
  and `tables`. `TestOutbox_EveryActionSurvivesACrash` stops the process
  inside and after each of the five actions one commit queued and counts
  that each happened once.

- Actions are an extension point (ADR-0045, items 1, 4, 5 and 9): an
  action a rule names is the engine's own or has a definition, and the
  host program implements a definition with a Go function.
  - `DefineAction` stores an action definition, JSON like a class: a
    name of a namespace and a verb, such as `books.post`, with `params`
    and `returns` by scalar type, `required` and a `description`.
    `ValidateAction` reports what it would refuse. A redefinition is the
    next version of the action.
  - `Options.Actions` gives `Open` the Go functions of the process, each
    under the name of its action. A function receives a
    `fado.ActionCall`: the key of the queued action, the same at every
    attempt; the attempt; the arguments by parameter; and the event that
    fired the rule, with the document at that version.
  - A rule calls a defined action with `"args"`, a formula for each
    parameter, or for a `json` parameter a template with a formula in
    the place of every string. The commit evaluates them and queues a
    row; the worker calls the function outside any transaction, at
    least once. No error is `action_run`; an error is tried again with
    the usual wait; an error wrapped with `fado.Permanent` is a refusal,
    dead at once. A function may call the database.
  - `Actions` lists every action a rule may name, the engine's own and
    the defined ones, with their parameters; `Action` returns one.
    `PreviewAction` shows the call an action would receive for given
    values and calls nothing; `TryAction` runs an action once outside
    any document and answers what it returned or how it failed.
  - `api/`, with the JSON schemas of an action definition, of a call
    and of the delivered event, held to the engine by `TestAPISchemas`.
  - The conformance suite `conformance/rules/calls.json`, and
    `BenchmarkOutboxRegistered`.
  - New codes: `unknown_parameter`, `not_registered`, `action_error` and
    `bad_result` for the store; `unknown_action` and `unknown_parameter`
    for a definition.

- Actions the engine sends as HTTP requests (ADR-0045, items 3 to 7).
  - A definition may carry a `request`: `method`, `url`, `headers` and
    `body`, each a pure formula over the parameters, or a template for
    the body. `ok` is a condition over the reply and `result` gives the
    returned values, over `STATUS` and `BODY`. `DefineAction` stores it
    like any definition, and a rule calls it with `args`.
  - `http.call`, the engine's own declared action, for a request written
    in the rule: `url`, `method`, `headers` and `body` under `args`.
  - `Options.Outbound` is what sends. Package `fado/outbound` makes it:
    `outbound.Allow` takes the hosts a database may call and, for every
    connection a definition may name, that connection's hosts. A
    database opened without it sends nothing. Only https, unless a host
    is listed with `http://`; no redirect is followed; an address inside
    the network is refused unless its host is listed by name; ten
    seconds for a call and one megabyte of a reply.
  - `Options.Secrets` supplies the secrets of a connection when a
    request is sent. A definition reads one with `SECRET("name")`, a new
    function that binds only in a request. No secret is stored: not in a
    definition, a row, an event, a preview or an error. A connection's
    secret goes only to the hosts of that connection.
  - A request without a body of its own carries the call as JSON, the
    arguments and the event that fired the rule, with `Idempotency-Key`,
    `FadoDB-Event` and `FadoDB-Attempt` as headers. With `sign` it also
    carries `FadoDB-Signature`, which `fado.VerifySignature` checks.
  - No reply, 408, 429 and 5xx are tried again, no sooner than
    `Retry-After` asks; any other reply that is no success, and a 2xx
    for which `ok` is not TRUE, is a refusal.
  - `PreviewAction` shows the request with secrets masked; `TryAction`
    sends it for real.
  - `URLENCODE(text)`, a pure function, on the Go VM, SQLite and DuckDB.
  - The conformance suites `requests` and `outbound-off` under
    `conformance/rules/`, `conformance/binding/secret.json`,
    `BenchmarkOutboxDeclared` and `BenchmarkDeclaredAction`.
  - New codes: `outbound_off`, `unknown_connection`, `host_not_allowed`,
    `secret_unavailable`, `no_reply`, `try_later`, `refused`, `not_ok`
    and `reply_too_large` for the store; `unknown_secret` and
    `unknown_connection` for a definition.

- The result of an action comes back to the document (ADR-0045, item
  8). An action of the outbox may carry `into`, a map from input
  variables of the rule's own document to formulas over what the action
  returns, with `OK` and `ERROR`. When the action is done or dead the
  engine writes the values as a set of its own, in the transaction that
  marks the action; the eventless transitions follow it, so a process
  goes on from the answer and from a failure. A `create` returns `id`
  and a `transition` returns `state` and `version`. A document that is
  frozen or gone, or that refuses a value, keeps what it has, and
  `QueuedAction` says why under `IntoCode` and `IntoMessage`. A preview
  lists the variables a result will go to.
- `examples/actions/webhook.json`, a generic webhook definition whose
  address and signing key are the connection's secrets, and
  `docs/actions.md`, a guide to writing an action for any REST API. Both
  are run against the engine by the tests of `fado/outbound`.
- The conformance suite `conformance/rules/into.json`.
- `docs/perf/v0.4.0.md`, the Phase 4 measurements against the budgets of
  ADR-0043 to ADR-0047, with the rows of Phases 2 and 3 run again; and
  `TestRulesCampaign` in the store package, the campaign of rules and
  actions that measured them on disk: a transition that fires rules, a
  refresh, the outbox queued and drained, a count over the documents of
  another class with and without an index, a formula that reads the
  life of a document with a long history, and timers.

### Changed

- A filter that compares a date, a datetime or a number variable with
  an operand that reads no document, as in `due < TODAY()` or
  `seen >= ADDDAYS(NOW(), -7)`, now lets SQLite seek the variable's
  index, as a comparison with a literal already did. The answer is the
  same; a `Count` over 100,000 documents and the list of what is due
  read the hundred that match and not every row.
- The engine schema is 5, for the four system columns and the origin of
  an event. A database made before does not open: before 1.0 there is
  no upgrade path, and no database exists yet.
- The write step of a commit costs less, which pays for the four
  columns: an existing document is updated in place, a commit that left
  every value as it was writes its system columns alone, and every
  value is boxed once for the current row, the history row and the
  event. A transition allocates 159 times where it allocated 168.
- The write step boxes the name of a state once and keeps it. A
  transition allocates 155 times where it allocated 159.

- The bytecode format is version 4: a query slot may carry an aggregate
  and the expression it runs over, and says when its key is one term of
  a conjunction (ADR-0046). Classes are compiled from their JSON when a
  database opens, so no file changes.

- **The engine schema is version 4**, for the two columns of the outbox
  that say why a result was not stored. A file made by an earlier
  pre-release engine is refused and recreated.
- A returned value of an action definition may not be named `OK` or
  `ERROR`, which `into` reads beside the returned values.
- `http.call` is a call like any other and takes its arguments under
  `args`. Before it was accepted in any shape and not run; the old
  shape, with `url` beside `action`, is refused with `bad_json`. In a
  process that sends nothing its row gets no answer, `outbound_off`.
- The dependency gate checks that the `fadodb` binary links no HTTP
  client. The placeholder package `internal/rules` is gone; the sender
  is `fado/outbound`.
- A rule that names an action nobody defined is refused with
  `unknown_action`, a parameter the action lacks with
  `unknown_parameter`, a required one left out with `missing` and a
  formula of another type with `type_mismatch`, all when the class is
  defined. Before, any action name was accepted and not run.
- A redefinition of an action that would break a rule of a class is
  refused with `not_allowed` naming that rule, as a redefinition of a
  class or of a table is.
- The worker runs the engine's own actions one at a time and up to
  `Options.Outbox.Workers` actions that leave the database at once, and
  a call that takes long holds back neither a commit nor the engine's
  actions of other documents. `Drain` still runs every action one at a
  time on the caller's goroutine.
- `actions` and `action_versions` are names of the database's own tables
  and no class may take them.
- A `create` is checked when the rule's class is defined: the class it
  names must exist and have states, its values must be scalar inputs of
  that class with formulas of their types, and an input the class
  requires without a default must be given. A class that names another
  is defined after it. The same holds for what the other actions name:
  the class and the transition of a `transition`, the class and the
  variables of a `refresh` of another document, the table and the
  columns of a table action.
- A redefinition of a class or of a table that would break a rule of
  another class is refused with `not_allowed` naming that rule: a
  variable or a column the rule fills is removed or takes a type the
  formula does not have, the transition it takes is removed, a variable
  it refreshes no longer lists `refresh`. The fix is to change that rule
  first. A new required input is not refused; the create that does not
  give it is refused with `required` when it runs.
- A command key may not start with `outbox:`, the prefix of the commits
  the outbox makes.
- The engine schema went to version 3 for the moment columns, before it
  went to 4; there is no upgrade path before 1.0.
- A triggered variable's `table:` and `schedule:` triggers are refused
  with `not_supported` until the phase that runs them; before, they were
  accepted and never fired. A trigger that can fire on a move out of a
  terminal state is refused, since a frozen document computes nothing.
- A class definition is checked for what its rules do, where before only
  their structure was. Refused now: `rules` in a class without states; a
  `set`, `reset` or `refresh` on a trigger that can fire over a frozen
  document or on `delete`; a rule on `schedule`, with `not_supported`
  until it is built; a misspelt property of a synchronous action. An
  `emit` names its event under `event`.

### Fixed

- The outbox worker could leave a row that had fallen due waiting for a
  minute. It read the clock once to look at what is due and again to
  ask when the next row falls due; a row whose second began between the
  two readings was due for neither, and the worker waited out its bound.
  It now asks with one reading. It also wakes when the second of the
  next row begins and not up to a second later, and waits a second, not
  a minute, when the store fails while it asks what falls due next
  (ADR-0044).
- The listing of open transitions read a document without who created
  it, changed it and moved it when the class asked for that only inside
  the condition of a `LOOKUP`, as in
  `LOOKUP(Claim, COUNT(id), created_by = CREATEDBY())`: the function
  answered blank there, and the listing could call a transition closed
  that the commit allowed. It reads them now.
- A rule of an exit that creates a document for each element of a list
  read `ENTEREDAT()`, `ENTEREDBY()` and `ENTEREDFROM()` of the stay that
  begins, where every other rule of an exit reads the stay that ends,
  when no other formula of the class called a function of the
  document's life. It reads the stay that ends.

- An `Open` that refuses a file, because a stored class does not
  compile for one, left the file open: the classes and tables loaded
  before the failure kept their prepared statements. On Windows the file
  could then not be removed or replaced until the process ended. `Open`
  releases them now.
- A `LOOKUP` of a table by a money column, `price = THIS.amount`,
  answered from the in-memory index by the amount alone, so money of
  another currency found a row. ADR-0037 keeps money columns out of the
  index; the binder now records no key for one, and the statement
  compares the currencies. Found by running the formula suite's lookups
  against the store.

## [0.3.0] - 2026-11-09

### Added

- `skills/`, five agent skills in the SKILL.md format, one per hat an
  agent wears with FadoDB: `fadodb-design`, `fadodb-operate`,
  `fadodb-simulate`, `fadodb-mine` and `fadodb-own-work`. Stubs: each
  says what works today with the project's marks and outlines its
  procedure; the shared reference set under `skills/reference/` is to be
  generated from the catalogue and the decision records.
- `examples/classes/`, the sample classes the skills draw on, starting
  with the README's Invoice; `TestExampleClasses` validates and defines
  every sample so the corpus cannot drift from the engine.

### Added

- `docs/perf/v0.3.0.md`, the Phase 3 measurements against the budgets of
  ADR-0040, ADR-0041 and ADR-0042; the commit campaign gained the rows a
  transition, a transition with values, a transition starting a chain of
  three and the listing of ten candidates, and `BenchmarkTransitions10`
  measures that listing alone.
- `At(t)`, a moment the caller chooses for a call that reads or previews
  and writes nothing (ADR-0042): on `Preview`, `PreviewDraft`,
  `Transitions`, `Query`, `Count`, `Get`, `Version` and `History`, `NOW()`
  is the instant and `TODAY()` its UTC day in every formula the call
  evaluates, defaults, guards, eventless guards, access rules and rights
  alike. A preview reports the moment its formulas saw as `Now`, chosen
  or not. A zero moment is the database's clock. A commit runs at the
  database's clock: `At` on `Create`, `CreateMany`, `Set`, `Transition`,
  `Delete`, `Migrate` or `ImportRows` is refused with `not_allowed`, and
  the business date is untouched. `conformance/statechart/clock.json`
  is the specification; a suite may declare its `clock` and a step its
  `at`.
- The rights table and the per-transition rules are enforced in the Go
  API (ADR-0041). A value for a variable whose `write` right is not TRUE
  for the principal in the document's state is refused with `forbidden`
  naming the variable, the first in declaration order on a commit and
  every one under a preview's errors; the right is evaluated over the
  stored document before the assignments, in the state the command
  arrives in, and over a new document after its defaults. A transition
  whose access rule is not TRUE is refused with `forbidden` before its
  guard, and `Transitions` and a preview list it so. Every document the
  API returns, from `Get`, `Version`, `History`, `Query`, `Preview` and
  the commits, is projected for the principal: a variable whose `read`
  right is not TRUE in the row's state is absent from `Values`, a history
  row under the state and the values it had, the rights of the current
  class version applying by name to every row. A filter compiles the read
  right of each variable its `where` or `order` names into its predicate,
  so a row whose state hides the variable neither matches nor sorts, and
  is refused with `forbidden` only when no state lets the principal read
  it; `fields` are projected as a document is. A read right may use pure
  and context functions over the document's own variables only, refused
  at definition with `not_allowed` otherwise; a write right keeps every
  function. An administrator bypasses the rules and the rights and never
  a guard; a call without a principal is nobody. A class without rights
  and transition rules behaves as before. `conformance/statechart/rights.json`
  is the specification; the runner gained `get`, `query` and `count`
  steps with `absent`, `present`, `docs` and `count` expectations.
- The statechart runs (ADR-0040). `Transition(ctx, class, id, name,
  values, opts...)` moves a document along a named transition with the
  assignments applied in the same commit: one version, one history row
  and one event of kind `transition` with the name, the states and the
  versions before and after. The guard sees the recalculated document
  with `STATE()` the state it leaves. Every eventless transition that
  then fires is a further commit in the same transaction, a version and
  a history row of its own with the same values and the new state, and a
  `transition` event without a name caused by the command's event; more
  than eight refuse the command with `too_many_transitions` and write
  nothing. Eventless transitions are evaluated after `Create`,
  `CreateMany`, `Set` and `Transition`, not after `Migrate`. A refusal
  names its code and the fix: `unknown_transition`, `wrong_state`,
  `guard_false` (an erroring guard is FALSE, the message carries its
  code and offset), `too_many_transitions`, and `terminal` for values
  given with a move out of a terminal state. A keyed command that made a
  chain replays to the last version it made.
- `Transitions(ctx, class, id, opts...)` lists the named transitions
  leaving the document's state with whether the principal may take each
  one now, and the code when not. `Preview` and `PreviewDraft` take a
  transition, and a preview of a class with states reports `Path`, the
  states the command visited, and `Transitions`, the listing from the
  resulting state.
- The `<Class>_states` view (ADR-0014, ADR-0040): one row per visit of a
  document to a state, with `entered_at`, `left_at`, `duration`,
  `entry_version` and `exit_version`, over the history table.
- `conformance/statechart/*.json`: scripted suites the store runs in CI,
  the specification of the statechart's behaviour; the format is in
  `conformance/README.md`.

- `STATE()` and `ID()` (ADR-0040), the first of Phase 3: context
  functions returning the state and the id of the document a formula
  runs over, blank for a class without states and when there is no
  document. The VM reads them from its context; in a filter's `where`
  they are the row's `state` and `id` columns, the index term included,
  so `STATE() = "draft"` and `state = "draft"` spell the same predicate;
  elsewhere the SQL binds them as parameters, as `USER()`. Like the
  other context functions they are refused in auto formulas and the
  title. The `context` conformance suite has their cases, and `fadodb
  formula eval` takes `-state` and `-id`.
- `PreviewDraft` (ADR-0025): a class definition compiled for one call
  and run through the commit pipeline over an existing document of the
  stored class, or as a create, writing and storing nothing, so that a
  designer tries a draft on real documents, terminal ones included.

### Changed

- `NOW()` and `TODAY()` in a filter's `where` read the database's clock,
  `Options.Now`, as every other formula does; they read the wall clock
  before.
- A preview of a set that changes nothing no longer stops short: it
  reports `Path`, the states a commit at that moment would visit, and the
  transitions open from the resulting state, with the version as it is,
  so that a preview at a chosen moment answers whether an eventless
  transition would fire.
- `Query` and `Count` keep their prepared statements, at most 64 by SQL
  text, the least recently used one making room, dropped when a class is
  redefined: a repeated filter pays SQLite's parse once, which was half
  of an indexed filter's time, and the read right compiled into a filter
  (ADR-0041) stays within its budget.
- A class without states stores no documents (ADR-0040): it is a
  stateless application, evaluated with `Preview` and `PreviewDraft`.
  `Create` and `CreateMany` on such a class are refused with
  `stateless`. Every stored document is created into its class's
  initial state, and a class with documents keeps its states block.

### Fixed

- A filter whose `where` calls a function the engine implements in Go,
  `UPPER`, `TEXT`, `ROUND` and the others behind `fado_` functions,
  failed with `no such function`: the store never registered them with
  the SQLite driver. `Open` registers them once per process.

## [0.2.0] - 2026-11-08

### Added

- Class versions, terminal states and migrations (ADR-0039 proposed). A
  redefinition is a new class version and touches no document: every
  current and history row records the class version that computed it,
  and a document is read under that version. A document in a terminal
  state, one of `states.final`, is frozen: set, delete, preview and
  migration are refused with `terminal`, and a transition out of it must
  lead to a terminal state. A live document adopts the current version
  at its next commit; `Migrate` adopts every live document of a class in
  batches, as `migrated` events. `DefineClass` and `Validate` refuse,
  with the count of documents in the way, removing a state documents are
  in, thawing a terminal state with documents in it, removing a variable
  that holds values and retyping one that ever did; everything else is
  allowed, and the tables are altered in place. The engine schema is
  version 2; a pre-release file of schema 1 is refused.
- `CreateMany`, the bulk load path of ADR-0035: documents of one class in
  one transaction, one commit each with its history row and event, all
  or none; a batch takes no key and no id. The Phase 2 commit campaign,
  `TestCommitCampaign` in the store package, measures the commit with
  its compute at p99, keyed beside unkeyed commits (ADR-0026), a preview
  beside its commit (ADR-0025), batched creates and commits with lookups,
  on disk with `FADODB_BENCH_DIR`; `BenchmarkRecalculate50` measures the
  full recalculation of a fifty-formula document. The results are
  published under `docs/perf/`.
- Attachments (ADR-0033): a `BlobStore` interface with the `sqlite`
  store, a table in the database file and the default, the `dir` store,
  a directory beside it, and the embedder's own given on open; the kind
  and `MaxBlob`, 32 MiB by default and raisable, in the metadata.
  `PutBlob` hashes a stream while it writes, stops at the limit before
  anything is stored, sniffs the media type when none is given and
  returns the `File` fact to commit. A commit that sets a `File` whose
  hash the store does not hold, or whose size differs, is refused with
  `unknown_blob` naming the variable or the row. `OpenFile` serves a blob
  through a document that holds its hash, at the current version or one
  `AtVersion` names; an administrator may `OpenBlob` by hash. `CheckBlobs`
  recomputes every held blob's hash, and `fadodb check FILE` reports the
  mismatches.
- The filter and the read-only SQL endpoint in the Go API (ADR-0038
  proposed). `Query` takes a class, a `where` formula over the class's
  variables and the system columns, an `order`, a `limit`, an `offset`
  and the `fields` to return, and answers documents in the wire shape
  with their composites loaded in one statement per child table; `Count`
  counts. The formula is bound and translated by the engine, so a filter
  means what a formula means, a row on which it errors does not match,
  and beside the exact predicate every comparison of a column with a
  literal is added on the bare column so that SQLite uses the index.
  `SQL` runs one `SELECT` over the views on a connection whose authorizer
  refuses the physical tables, the schema, writes, pragmas, attachments
  and transactions, with positional parameters and at most 10,000 rows.
  Refusals carry a stable code, the element of the filter and the offset
  inside `where`.
- Explicit tables and `LOOKUP` (ADR-0016, ADR-0020, ADR-0036, ADR-0037
  proposed). A table is defined in JSON with its columns, key and
  indexes, stored as `tbl_<Name>` with the view `<Name>` and filled by
  `ImportRows`, which replaces rows by key or the whole table. The binder
  compiles `LOOKUP(source, column, condition)` into one query slot: the
  condition is a program of its own over the source's columns and the
  document's `THIS.name` variables, a row on which it errors does not
  match, and an equality of a column with an expression of the document
  alone is recorded as the key the index serves. The VM hands the slot to
  a `QueryExecutor`; the store answers from an in-memory index while the
  table has at most `MaxIndexedRows` rows, a database limit kept in the
  metadata, and from a prepared statement narrowed by the table's index
  above that; the SQL generator writes a correlated subquery. The
  conformance suite `lookup` declares its tables inline and runs on the
  VM, SQLite and DuckDB; three binding suites fix what is refused. The
  bytecode format is v3: the query slot carries the condition.
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

[Unreleased]: https://github.com/MojoHand70/fadodb/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.5.0
[0.4.1]: https://github.com/MojoHand70/fadodb/releases/tag/v0.4.1
[0.4.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.4.0
[0.3.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.3.0
[0.2.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.2.0
[0.1.0]: https://github.com/MojoHand70/fadodb/releases/tag/v0.1.0
[0.0.1]: https://github.com/MojoHand70/fadodb/releases/tag/v0.0.1
