# ADR-0040: Transitions as commits: the command, eventless chains and state visits

- Status: Accepted; amends ADR-0008 (what a chain of eventless transitions writes), ADR-0014 (the `_states` view defined), ADR-0015 (one command may make several commits), ADR-0026 (the key of a command that made a chain), ADR-0039 (a class without states has no documents, not only live ones); amended by ADR-0043 (a hop's commit runs its own triggers and rules), ADR-0047 (a transition into the same state is an entry, and the `_states` view says who), ADR-0048 (a second command takes a transition, for many documents)
- Date: 2026-11-08

## Context

ADR-0008 fixed the statechart's semantics: flat named states, one initial
state, named transitions with source states, a target and a guard,
eventless transitions evaluated after every value change until none fires
with a bound of eight per commit, and no way to change the state but a
transition. ADR-0015 placed the guard and the eventless transitions inside
the commit, after the recalculation and before the writes. ADR-0006 and
ADR-0014 made the exit and entry snapshots the history rows the transition
event references as `before_version` and `after_version`. ADR-0026 said
what the transition event carries, and ADR-0039 made the final states
terminal. Phase 2 built the store with `state` and `state_entered_at` on
every row, the `events` columns of a transition, the compiled transitions
and guards with their definition-time checks, and a `Preview` that refuses
a transition.

What none of the records says, and Phase 3 cannot build without: the
shape of the transition command and its refusals; what a chain of
eventless transitions writes, since one commit is one version (ADR-0015)
and a chain of three passes through two states that would have no row of
their own; which commits evaluate eventless transitions; whether a class without
states may hold documents at all, since ADR-0039 allows adding states to
a class and says such a class has only live documents; how a caller
learns which transitions it may take; the definition of the
`<Class>_states` view that ADR-0014 names and nothing defines; and the
context functions `STATE()` and `ID()`, in the architecture's function
table since the start and in no catalog.

The first embedder drives transitions from its own rules and asks a
person only to approve; the project's purpose is agents that run
processes with no person watching. Such a caller needs a transition that
is idempotent, a dry run that tells it which transitions would open, and
a refusal that names a stable code and the fix.

## Decision

1. **The command.** `Transition(ctx, class, id, name, values, opts...)`
   moves a document along the named transition, with the assignments in
   `values` applied in the same commit; `values` may be empty. `Key`, `As`
   and `BusinessDate` apply as for `Set`. The order inside the commit is
   that of ADR-0015, with the checks of ADR-0041 first: the transition's
   access rule and the write rights over the stored document, the
   assignments, the auto recalculation, the guard, the eventless
   transitions, the writes. The guard sees the document after the
   assignments and the recalculation, with `STATE()` the state the
   transition leaves. `Preview` with a transition runs the same pipeline
   and writes nothing (ADR-0025).

2. **Refusals.** A transition that cannot happen is refused before
   anything is written, with a code from this list, a message naming the
   document's state and the transition, and a hint with the fix. A
   refusal is a response, not an event.

   | Code | When | The hint carries |
   |---|---|---|
   | `unknown_transition` | the class has no named transition of that name, or no statechart | the names of the class's transitions |
   | `wrong_state` | the transition exists and the document's state is not among its sources | the transitions that leave the document's state |
   | `guard_false` | the guard did not evaluate TRUE; a guard that errors is FALSE (ADR-0019), and the message carries the error's code and offset | the guard's text |
   | `forbidden` | the transition's access rule (ADR-0010) is not TRUE for the principal, or a value is set that the rights refuse (ADR-0028, ADR-0041) | the rule, or the variable |
   | `too_many_transitions` | more than eight eventless transitions fired after the command | the path of states taken |
   | `terminal` | values are given with a transition out of a terminal state (ADR-0039) | that a terminal document only moves |

   An eventless transition cannot be requested by name; it has none.

3. **One version per state change.** The assignments and the named
   transition are one commit: version N+1, one history row, and one event
   of kind `transition` with `name`, `from_state`, `to_state`,
   `before_version` N and `after_version` N+1, in place of the `set` event
   the assignments alone would have written. Each eventless transition
   that then fires is a further commit on the same document in the same
   transaction: a version of its own, a history row with the same values
   and the new state, as ADR-0039 words it for a move between terminal
   states, and a `transition` event with no `name` whose `cause_event_id`
   is the command's event. The exit snapshot of every transition is thus
   the row its guard saw and the entry snapshot the row it made; nothing
   between them is invented. The bound of eight of ADR-0008 counts the
   eventless commits of one command; past it the whole transaction rolls
   back with `too_many_transitions`. A command with a key (ADR-0026) stores
   the key on its first event, and a repeat returns the document at the
   last version the command made. The alternative, one version for the
   whole chain with the intermediate states as events only, was rejected:
   the second hop's `before_version` would name a row in a state the hop
   did not leave, and the `_states` view would have to reconstruct from
   events what the history table is for.

4. **Which commits evaluate eventless transitions.** `Create`,
   `CreateMany`, `Set` and `Transition`, after their recalculation, from
   the state the commit left the document in, in definition order, until
   none fires. A commit that changes nothing commits nothing and evaluates
   nothing, as today. `Delete` does not. `Migrate` does not: a migration
   adopts a definition and moves no document through its process
   (ADR-0039, "that and nothing else"); the document's next commit does.

5. **A class without states stores no documents.** A class that declares
   no `states` is a stateless application: its variables, defaults and
   formulas are evaluated with `Preview` and `PreviewDraft`, and in the
   browser from Phase 7, and no instance of it is ever stored. `Create`
   and `CreateMany` on such a class are refused with `stateless`, whose
   hint says to add a `states` block. Every stored document is therefore
   created into its class's initial state by the `created` event, and a
   document never gains a statechart later: a redefinition that adds
   `states` finds no documents, and one that removes the block is refused
   while documents exist, as ADR-0039 refuses removing a state with
   documents in it. ADR-0039 item 2 said a class without states has only
   live documents; it has none.

6. **The allowed transitions.** `Transitions(ctx, class, id, opts...)`
   lists, for the named transitions whose sources include the document's
   state, in definition order: the name, the target, whether the principal
   may take it now, and when not the code, `guard_false` with the guard's
   error code when it errored, or `forbidden`. It evaluates the guards and
   the access rules over the stored document for the principal at that
   moment; it is a read and writes nothing. Eventless transitions are not
   listed. An administrator bypasses the access rules and never the
   guards. `Preview` returns the same listing from the resulting state
   under `Transitions`, and under `Path` the states the command visited,
   the state before first (ADR-0025).

7. **The `_states` view.** Every class with a statechart has the view
   `<Class>_states`, one row per visit of a document to a state: `id`,
   `state`, `entered_at`, `left_at` (null while current), `duration` in
   seconds (null while current), `entry_version`, the version whose commit
   entered the state, which is the entry snapshot, and `exit_version`, the
   last version in the state, which is the exit snapshot the leaving
   transition's guard saw (null while current). A visit begins at a
   history row whose state differs from the previous version's. The view
   is defined over the history table with window functions, so a visit of
   zero duration inside a chain is one row like any other.

8. **`STATE()` and `ID()`** join the catalog as context functions
   returning text: the document's state, blank in the preview of a
   stateless application, and its id. The VM reads them from its `Context`; the SQL generator
   binds them to the row's `state` and `id` columns where a statement runs
   over document rows, and to parameters where it evaluates one formula,
   as for `USER()`. The binder treats them as the other context functions:
   not in auto formulas or the title (ADR-0015, ADR-0018). The `context`
   conformance suite gains their cases.

9. **The conformance suite** gains `conformance/statechart/*.json`: a
   class, a script of commands, each with its principal, and the expected
   outcome of each: the state and version after, the states visited, the
   code of a refusal, the transitions listed as enabled or not. The store
   runs them in CI; the server (Phase 6) runs them over the command set
   and the browser VM (Phase 7) runs the listing from the bundle. The suite
   is the specification of the statechart's behaviour; the format is in
   `conformance/README.md`.

10. **What waits.** Triggered variables on `enter:`, `exit:` and
    `transition:`, rules and actions on a transition, timed transitions,
    and the transition event delivered to executors with its JSON schema
    under `api/`: Phase 4 (ADR-0009, ADR-0026, ADR-0027). Hierarchical
    states: a 1.x record (ADR-0008).

## Consequences

- An agent moves a document with one call, retries it with the same key,
  dry-runs it with `Preview`, and reads from a refusal which of six things
  stands in the way and what to do about it. A refused transition never
  shows in the log, so the log stays the record of what happened to
  documents; a caller that wants its attempts recorded records them.
- A chain of k eventless transitions costs k+1 history rows and events.
  Composite variables are copied per version (ADR-0014), so a class with
  large composites and long chains pays for it; chains are short by
  design, the bound is eight, and the campaign measures a chain of three.
- A `transition` commit with values writes one event, not two. A reader
  that wants the values changed compares the two history rows the event
  names.
- `Transitions` and `Preview` are what a frontend greys buttons out with
  and what an agent plans with; the server's answer on `Transition` is the
  one that counts, as for everything else.
- The `_states` view makes the process-mining promise of ADR-0014
  checkable: durations and paths are a query over it, with no JSON. It is
  a view over the history table and costs nothing until read; the mining
  budget belongs to Phase 8.
- `STATE()` in a filter's `where` (ADR-0038) is the `state` column; the
  two spell the same predicate.
- A class without states is a calculator, not a store. Entering the first
  state is always the explicit act of creation, recorded as the `created`
  event, never a side effect of a later edit. Tests and examples that
  store documents of a class without states gain a `states` block; the
  formula engine, the previews and the draft previews are unchanged, and
  a pre-release database that holds such documents keeps them readable
  and accepts no more.

## Performance

Budgets, from ADR-0006 and ADR-0012: a transition commit under 2 ms at
p99 on NVMe, single writer, event row and both snapshots included; at
least 2,000 transitions per second unbatched. New here: a chain of three
eventless transitions within three times a plain commit's write; the
listing of ten candidate transitions, guards evaluated, under 10 µs plus
whatever lookups the guards make; the guard loop allocates nothing. The
Phase 2 campaign of `docs/perf/v0.2.0.md` gains the rows transition,
transition with values, chain of three and listing, and
`BenchmarkTransition` measures the compute alone; the results are
published as `docs/perf/v0.3.0.md`.
