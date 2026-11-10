# ADR-0043: Rules inside the commit: triggers, synchronous actions and triggered variables

- Status: Accepted; amends ADR-0014 and ADR-0018 (the moment of a triggered value, in the tables and on the wire), ADR-0015 (the order inside a commit, the order of triggered variables, `Reset` and `Refresh` as commands with their events), ADR-0019 (what an error in an action means), ADR-0025 (what a preview reports of the rules), ADR-0039 (rules and a frozen document), ADR-0040 (a hop's commit runs its own triggers and rules); amended by ADR-0044 (a `refresh` that names another document is an outbox action; a `create` with `each` queues a row for each element)
- Date: 2026-11-09

## Context

A class already carries its rules: the format reads `on`, `state`, `name`,
`variables`, `if` and `do`, the binder checks their structure and binds
the conditions, and nothing runs them. A triggered variable is declared,
bound and stored as a column that is never computed. ADR-0009 split the
actions into the synchronous ones, which change the document itself
inside the commit, and the ones that go to the outbox. ADR-0015 placed
both in the commit: triggered variables whose trigger fired, then the
rules, then the writes. ADR-0040 then made one command several commits,
one per state change, and ADR-0039 froze a document in a terminal state.

What no record says, and Phase 4 cannot build without: which commit
raises which trigger, now that a command makes up to nine; the order
rules run in; over which document a condition and an argument are
evaluated; whether a value a rule sets fires other rules; what an action
that fails does to the commit; what a rule may do to a frozen document;
where a triggered value keeps the moment ADR-0015 says it stores; what
`reset` and `refresh` write when they arrive as commands; and what a
preview shows of all this.

The caller this is written for is an agent that runs a process with no
person watching. It needs to read from the class alone which rules a
command will fire, to dry-run the command and see them, and to know that
the engine cannot loop.

This record covers what happens inside the commit. The outbox is
ADR-0044 and the actions that leave the database are ADR-0045.

## Decision

1. **The triggers of a commit.** Every commit raises a fixed list of
   triggers, decided by what the command did before any rule runs:

   | Commit | Triggers, in this order |
   |---|---|
   | `Create`, and each document of `CreateMany` | `create`, `enter` of the initial state |
   | `Set`, `Reset`, `Refresh` | `values_change` of every variable that changed |
   | `Transition` | `values_change`, `exit` of the state left, `transition` of the name, `enter` of the target |
   | an eventless transition that fires | `exit`, `enter` |
   | `Delete` | `delete` |
   | `Migrate` | none: a migration moves no document through its process (ADR-0039) |

   A variable changed when its value, input or computed, differs between
   the row before and the document as the commit computed it, before any
   rule ran. A transition whose target is its source raises `exit` and
   `enter` like any other. The same list fires the triggers of the
   triggered variables: `enter:`, `exit:` and `transition:`.

2. **Order.** Rules run trigger by trigger in the order of the table and,
   within one trigger, in the order the class declares them. A rule runs
   when it has no `if` or its `if` is TRUE; a condition that errors is
   FALSE (ADR-0019) and the rule does not run.

3. **What a rule sees.** A condition and the arguments of an action are
   evaluated over the document as the commit has computed it so far: after
   the assignments, the recalculation, the state change, the triggered
   variables and the synchronous actions of the rules before it. `STATE()`
   is the state left in an `exit` and a `transition` rule and the state
   the document is in everywhere else; `USER()` and `INGROUP()` are the
   command's principal; `NOW()` is the commit's clock. A `delete` rule
   sees the document as it was. Every function class is allowed, as
   ADR-0015 says of rules.

4. **The synchronous actions** change the commit's own document before it
   is written: the same version, the same history row, the same event.

   | Action | Written as | Does |
   |---|---|---|
   | `set` | `{"action": "set", "values": {"approved_at": "NOW()"}}` | assigns input variables; every formula of one `set` is evaluated before any is assigned |
   | `reset` | `{"action": "reset", "variables": ["discount"]}` | re-evaluates the defaults of the named inputs in dependency order; an input without a default becomes blank |
   | `refresh` | `{"action": "refresh", "variables": ["rate"]}` | recomputes the named triggered variables, each of which lists `refresh` among its triggers |
   | `emit` | `{"action": "emit", "event": "invoice.flagged"}` | appends an event of kind `custom` with that name |

   A `refresh` that carries an `id` names another document and is an
   outbox action, not one of these (ADR-0044, item 13).

   After the synchronous actions of a rule, the auto variables that depend
   on what changed recalculate. The values a rule assigns are checked like
   any others: type and scale, the enum list, the range, `required`. They
   are the engine's writes, not the caller's, so the write rights are not
   consulted (ADR-0028, ADR-0041). A custom event carries no payload: its
   `after_version` is the commit's version and its `cause_event_id` the
   commit's event, so a reader takes the document at that version, as for
   a transition (ADR-0014). The event's name is a name, not a formula, so
   that a subscription and a mining query can match it.

5. **Rules do not cascade inside a commit.** A synchronous action raises
   no trigger: a value a rule sets does not fire a `values_change` rule,
   in that commit or in the hops after it. What follows a commit belongs
   to the
   statechart: the eventless transitions are evaluated over the document
   the rules left, and each one that fires is a commit of its own
   (ADR-0040) with its own triggers, triggered variables and rules. One
   command therefore runs at most nine commits and each rule at most once
   in each, and the bound of eight is the only loop bound the engine
   needs. The alternative, letting a rule's `set` fire `values_change`
   rules under a second bound, was rejected: the order of effects would
   depend on a loop detector, and an agent could no longer read from the
   class which rules a command fires.

6. **An action that fails refuses the command.** An argument that errors,
   a value the variable refuses, a `reset` that leaves a required input
   blank: the whole command is refused and nothing is written, with the
   error's own code, the variable when there is one, and the path of the
   action in the definition, such as `/rules/2/do/0`. This holds for the
   arguments of an outbox action too, which the commit evaluates (item 7).
   A preview records the error and goes on. ADR-0019 makes a condition
   that errors FALSE; an action is not a condition. It is part of the
   commit, as an auto formula is, and a commit does everything its class
   says or nothing.

7. **The other actions are queued by the commit.** For every action of a
   rule that ran that is not synchronous, the commit evaluates the
   arguments and writes one outbox row in its own transaction (ADR-0044),
   or one for each element when a `create` carries `each` (ADR-0044,
   item 12). The action therefore carries the values of the moment that
   caused it, not of the moment a worker reaches it.

8. **A frozen document computes nothing.** A move between terminal states
   raises its `exit`, `transition` and `enter` triggers; the conditions
   are evaluated over the frozen values, and `emit` and the outbox actions
   run. `set`, `reset` and `refresh` cannot, and a triggered variable
   cannot recompute. So a trigger that can fire on a move out of a
   terminal state is refused at definition time with `not_allowed`, on a
   rule that carries one of those three actions and on a triggered
   variable: `exit` of a terminal state, a `transition` with a terminal
   source, `enter` of a state that a terminal state leads to. The hint
   says to hang it on the transition that arrives from a live state. A
   rule on `delete` may not carry the three either. A class without
   states stores no documents (ADR-0040), so `rules` in such a class are
   refused as well.

9. **Triggered variables.** A triggered variable recomputes in the commit
   that raises one of its triggers, after the state change and before the
   rules, and when the `refresh` action or command names it. The ones a
   commit recomputes run one at a time in declaration order, each over
   the document as it stands, and after each the auto variables that
   depend on it recalculate. `STATE()` is as for rules. A recomputation
   that errors leaves the value as it was and never refuses the command
   (ADR-0019).

   The moment ADR-0015 promises is two system columns per triggered
   variable in the current and the history table: `<name>__asof`, the
   version of the document whose commit last computed the value, and
   `<name>__error`, the code of a failed recomputation since then, null
   when the last one succeeded. `__` is reserved in every name
   (ADR-0031), so no variable collides. The time is the `valid_from` of
   the history row at that version. A document on the wire and in the Go
   API carries them under `computed`, a map from the variable to
   `version`, `at` and `error`, hidden with the variable by its read
   right. A triggered variable that never computed is blank with no
   moment. The engine schema becomes version 3; a file of version 2 is
   refused and recreated, as ADR-0039 did.

   `table:` and `schedule:` on a variable and `schedule` on a rule are
   refused with `not_supported` until the phase that runs them, as the
   bindings a build lacks are (ADR-0037). Until now they were accepted
   and never fired.

10. **`Reset` and `Refresh` are commands** (ADR-0015, ADR-0026):
    `Reset(ctx, class, id, variables, opts...)` and `Refresh(ctx, class,
    id, variables, opts...)`, each one commit with `Key`, `As` and
    `BusinessDate`, an event of kind `reset` or `refresh`, the triggers of
    item 1, and the eventless transitions after it. A `Reset` is the
    caller's write: it names at least one variable, and the write right
    of each is checked (ADR-0041). A `Refresh` with no variables
    recomputes every variable that lists `refresh`; a variable that does
    not list it is refused with `not_allowed`. Both are refused on a
    terminal document. A `Reset` that changes nothing commits nothing; a
    `Refresh` always commits, since the moment of the value is what it
    changes.

11. **The preview reports the rules** (ADR-0025). `Preview` and
    `PreviewDraft` gain `Rules`: for every rule whose trigger was raised,
    in the order it would run, the commit it belongs to, its path in the
    definition, whether it ran and the error code when its condition
    errored, and for each action its name, whether it is synchronous or
    queued, and its evaluated arguments. Nothing is queued and no action
    runs.

12. **The conformance suite** gains `conformance/rules/*.json` in the
    script format of the statechart suite, with the expectations `rules`,
    the rules that ran by path, `queued`, the actions queued with their
    arguments, and `computed`, the moments of the triggered variables.
    The store runs it in CI, the server over the command set in Phase 6.

13. **What waits.** `QUERY`, documents of a class as the source of a
    `LOOKUP`, and the binding interface: a record of their own before
    that step of the phase. Timers: ADR-0044 puts the question.

## Details settled while building

The first step of the phase built items 1 to 8 and 11, without the
triggered variables, on 2026-11-10. It settled these points, which the
items leave open.

- **The auto variables recalculate after each synchronous action** that
  changed something, not once after the last action of the rule
  (item 4). The document a rule leaves is the same. A later action of
  the same rule reads a consistent document, and a formula that fails is
  the failure of the action that caused it.
- **A `set` assigns scalar inputs.** An object, an array and a rows
  variable are refused with `not_supported`, since no formula gives one
  in this build. A `reset` takes them: an object becomes blank in every
  field and a composite empty.
- **An event name** is identifiers joined by single dots, at most 100
  characters, such as `invoice.flagged`. Its row of `event_docs` has the
  qualifier `emitted`.
- **A custom event of a `delete` rule** names the last version as its
  `before_version` and has no `after_version`, as the delete's own event
  does: a delete makes no version.
- **`values_change` across a class version.** A document computed by an
  older version adopts the current one with its next commit (ADR-0039).
  A variable changed when it differs from the row as it was, and one the
  older version did not have was blank.
- **The path of a failed action** is the field `path` of the refusal. A
  formula that fails in the recalculation after an action carries the
  action's path and names its own variable.
- **The preview's report** is `rules`: each with `commit`, 0 for the
  command's own and n for its n-th eventless transition, `path`, `ran`,
  `code` and `actions`; each action with `action`, `queued` and `args`
  in the shape the action is written in.
- **Until the next steps.** The `refresh` action is refused with
  `not_supported` until triggered variables are computed. An action of
  the outbox is checked by its name, named by a preview as queued, and
  not queued.

## Consequences

- Which rules a command fires is a property of the class and the
  command, readable without running anything and confirmed by `Preview`.
  The price is that a rule cannot react to what another rule set in the
  same commit; the author puts both effects under one trigger, or lets an
  eventless transition carry the second.
- A rule whose action cannot succeed blocks every command that fires it,
  as a broken auto formula blocks every commit. The designer sees it in a
  preview before any document does, and wraps what may fail in `IFERROR`.
- A rule whose condition errors does not run and leaves no trace in a
  commit. The preview shows it; a designer tests a class with previews.
- ADR-0040 said a hop's history row has the same values and the new
  state. It has the values its own triggered variables and rules left,
  which are the same when the class hangs nothing on that state.
- A triggered value says when it was computed in the row that holds it,
  so a read at any version answers "as of when" with no second query.
  Two columns per triggered variable is the cost, and a schema version.
- `enter` of a state that a terminal state leads to cannot carry a `set`,
  even when a live state leads there too. The rule goes on the
  transitions from the live states. The rule that nothing computes on a
  frozen document stays without exception.
- A class that names `schedule:` or `table:` stops validating until the
  engine runs them. The architecture page's example changes with the
  step that builds this record.

## Performance

Budgets. A commit of a class with rules none of whose triggers it raised
evaluates nothing and allocates nothing: rules and triggered variables
are indexed by trigger when the class is compiled, and
`BenchmarkTransition` on a class with ten such rules stays within the
gate's 5% of the same class without them. A rule that runs costs its
condition and its arguments, tens of nanoseconds each for arithmetic. A
transition that fires three rules, one `set` and one triggered variable
stays within the 2 ms p99 of ADR-0006. The commit campaign gains the
rows transition with a rule and a `set`, transition with a triggered
variable, and `Refresh`; `BenchmarkRules` measures the compute alone and
joins the gate. Published as `docs/perf/v0.4.0.md`.
