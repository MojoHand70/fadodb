# ADR-0042: A moment the caller chooses, for previews and reads

- Status: Accepted; amends ADR-0025 (a preview may run at a chosen moment and reports it)
- Date: 2026-11-09

## Context

Every clock the engine has is the database's: `Options.Now`, given at
open and set by tests, stamps the commits and feeds `NOW()` and `TODAY()`
in every formula; the business date of ADR-0026 is the principal's day or
UTC today. An agent that runs a process wants to ask what would happen on
another day: whether the eventless transition that reads `TODAY() > due`
would fire on the 15th, which transitions would be open then, what a
default that reads `NOW()` would produce. ADR-0025 made the preview the
dry run of a commit and said the moment of a preview is part of the
answer; it gave no way to choose the moment. The point was raised on
2026-11-07 and deferred to the phase that puts transitions in a preview.

What forces the decision now: `Preview` with a transition and
`Transitions` arrive with Phase 3 (ADR-0040), and both evaluate guards
that may read the clock.

## Decision

1. **`At(t)`** is an option of every call that reads or previews and
   writes nothing: `Preview`, `PreviewDraft`, `Transitions`, `Query` and
   `Count`, and the reads `Get`, `Version` and `History` for the rights
   they evaluate. In such a call `NOW()` is the instant `t` and `TODAY()`
   its UTC day, in every formula the call evaluates: defaults, guards,
   eventless guards, access rules, rights, and the triggered formulas of
   Phase 4. A zero `t` is the database's clock. `At` touches no business
   date: the business day is the principal's fact (ADR-0026, ADR-0027),
   not the clock.

2. **A commit runs at the database's clock.** `At` on `Create`,
   `CreateMany`, `Set`, `Transition`, `Delete`, `Migrate` or `ImportRows`
   is refused with `not_allowed` and the hint that a change is previewed
   at a moment, or made in a copy of the database opened with
   `Options.Now` set. The timestamps a commit writes, `ts`, `created_at`,
   `updated_at`, `state_entered_at`, `valid_from` and `valid_to`, stay
   system time, as ADR-0026 says.

3. **The preview reports its moment.** `Preview` carries `Now`, the
   seconds its formulas saw, chosen or not, so that the answer says which
   clock it was computed under (ADR-0025).

4. **`Options.Now` stays the clock of the database**, for tests and for
   an embedder that owns time, such as one that replays a scenario into a
   copy of the file. It is a property of the open database, not of a call,
   and its documentation says so instead of "tests set it".

## Consequences

- An agent plans with the engine's own rules at any moment it names, in
  one call, writing nothing: the listing says which transitions would be
  open on the 15th, the preview what the document would look like and
  where it would land. A scenario of several steps, where the second
  builds on the first, is a copy of the file opened with `Options.Now`
  set, which SQLite makes a file copy; the engine offers nothing more for
  it in 1.0.
- A commit computed under a chosen clock would store values whose moment
  no row records; refusing it keeps every stored value explainable by the
  database's clock and the history row's time, as ADR-0015 promises.
- Reads take `At` for the sake of the rights, which may read `TODAY()`; a
  read that evaluates no formula ignores it.
- One more option in the API and one field on the preview, no new type.

## Performance

Nothing measurable: `At` sets the `Now` of the VM's context, which every
call fills already. The preview row of the commit campaign runs with and
without `At` to show that the difference is nothing.
