# ADR-0039: Class versions, terminal states and migrations

- Status: Proposed; amends ADR-0008 (what a final state means), ADR-0014 (the class version on every row) and ADR-0018 (what a redefinition may change)
- Date: 2026-11-08

## Context

The architecture page said that adding a variable or a formula happens
in place, that changing a type or removing a variable makes a new class
version with a migration record, and that documents migrate lazily on
first write or eagerly by command. Phase 2 built the store without
deciding any of it: a class with documents could not be redefined at
all, and the acceptance criterion "migration round-trips preserve every
event and every history row" waited.

The decision was put to the owner on 2026-11-08 with a recommendation to
recompute every document eagerly when a formula changes. He refused it:
an invoice issued in October was computed by October's rules, and
rewriting it in November, even as a new version, restates a record
nobody touched. Documents are records, not cells of a spreadsheet whose
formulas must always hold. He added that a document workflow has states
in which a document must never change again, approved or rejected, and
that without that guarantee the system is not credible. The model below
is his; the rules on what a redefinition may do are the engine's
consequence of it.

## Decision

1. **A terminal state freezes a document.** The states listed in
   `states.final` are terminal (ADR-0008 named them final without saying
   what that means). A document in a terminal state keeps its values,
   its computed values and its class version: `Set`, `Delete`, `Preview`,
   recomputation and migration are refused with `terminal`. It may move
   to another terminal state, approved to archived, and never back to a
   live one; the binder refuses at definition time a transition out of a
   terminal state whose target is not terminal. Such a transition writes
   a history row with the same values and the new state, and computes
   nothing (Phase 3). A class may have many terminal states.

2. **A live document is one that is not in a terminal state.** A class
   without states has only live documents.

3. **A redefinition is a new class version and touches no document.**
   `DefineClass` of an existing class stores version N+1 and keeps every
   earlier definition compiled. Every current and history row records
   `class_version`, the version of the class that computed it, and a
   document is read under that version: the variables of its version, its
   values, its formulas' results. The views follow the current version;
   a column that an older version filled and the current one lacks is
   not in the view and stays in the table for the history.

4. **A live document adopts the current version at its next commit.**
   The commit is computed by the current definition: the document's
   values move to the current layout by name, every auto formula and the
   title are recomputed, the new variables are blank, and the row records
   the current version. An edit that changes no value is still that
   adoption when the document was on an older version. A new document is
   created under the current version.

5. **`Migrate` adopts on purpose.** It brings every live document of a
   class that an older version computed to the current version, as
   commits in batches of a thousand per transaction, each a history row
   and an event of kind `migrated` with the caller as actor. It skips
   terminal documents, moves nothing when run again, and is never
   automatic. Nothing is ever rewritten: the acceptance criterion holds
   because a migration appends.

6. **What a redefinition may do**, decided against the documents the
   store holds; a refusal is a diagnostic of `Validate` and `DefineClass`
   with the code `not_allowed` and the element's path:

   | Change | Rule |
   |---|---|
   | Add a variable, input or computed | Allowed. Terminal documents never show it; live ones show it blank until they adopt |
   | Change a formula, a default, a mode, triggers or the title | Allowed. Terminal documents keep their values; live ones recompute when they adopt |
   | Change `required`, `values`, `min`, `max`, `index` or a ref's class | Allowed; applies to values set or computed from the next commit on |
   | Add states, mark a state terminal, change transitions, rules, access, rights or the header | Allowed |
   | Remove a state | Refused while a document is in it |
   | Make a terminal state live again | Refused while a document is in it |
   | Remove a variable | Refused while a current row holds a value for it, a composite while a current version has rows |
   | Change a variable's type, scale or shape | Refused while any row, current or in the history, holds a value for it; the column is dropped and made again when none does |
   | Rename a variable or the class | Not a change the format knows: a rename is a removal and an addition |

   The type rule is stricter than the owner's words, which named terminal
   instances only. Converting or blanking a live value silently is what
   costs credibility, a column cannot hold two types for two versions of
   the same document, and a conversion rule can be added when a case
   needs it. A class whose history is empty is dropped and made again,
   whatever the change.

7. **The engine schema is version 2.** `class_version` is a system column
   of the current and history tables, after `version`, and of the views
   and the filter. A pre-release file of schema 1 is refused and
   recreated; there is no upgrade path before 1.0.

## Consequences

- An agent redefines classes freely and nothing it has recorded changes
  under it; what it cannot do is said at `Validate` with the count of
  documents in the way, before anything is written.
- Every value is explainable: a history row says which version of the
  class computed it, and reads it back under that version.
- A frozen document costs a check of its state at the start of every
  commit; the check is one string comparison per terminal state.
- The store keeps every version of every class compiled, a few kilobytes
  each; a class redefined a thousand times is a thousand compiled
  classes, which is fine and not a target.
- The views show the current version's columns. An analyst who needs an
  older version's removed variable reads the physical history table,
  which keeps the column; the documentation says so where it describes
  the views.
- The architecture page's "Class migrations" section is replaced by this
  record; ADR-0014's row gains `class_version`; ADR-0008's final states
  have their meaning.

## Performance

A redefinition is DDL on the tables and a recompile, no row is touched;
measured at a few milliseconds. Reading a document on an older version
costs a second read of its row under that version's layout, once per
document and only while it stays on the older version. A migration runs
at the batched commit rate of `docs/perf/v0.2.0.md`, 24,000 commits per
second here, so a million live documents adopt a new version in under a
minute, on purpose. The terminal check adds nothing measurable to the
commit campaign.
