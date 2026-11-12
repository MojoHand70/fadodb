---
name: fadodb-mine
description: Analyse how a FadoDB process actually runs from its event log, its history and its views. Cycle time per state, where documents wait, rework loops, who approves slowest, what was refused and why, throughput and backlog, as SQL over the tables every class writes anyway. Use this skill whenever someone asks for process mining, bottlenecks, SLAs or cycle times, an audit trail, who did what and when, a report or a dashboard over documents in FadoDB, or wants a finding to feed back into a redrafted class.
metadata:
  hat: analyst
  status: stub
---

# Mine a FadoDB process

**Status: stub, 2026-11-09.** This page says what the analyst does, what
works today and the outline of the procedure. The reference files it
names under `../reference/` are not written yet.

## The hat

The analyst reads. Every class is a SQL view, every version a history
row, every change an event with its principal and its moment, every
state entry and exit a snapshot. Process mining is a query over what the
engine wrote anyway, with no export and no second system, and the finding
goes back to the designer as a redrafted class.

## What works today

| Means | Mark |
|---|---|
| one view per class; the history and child tables | shipped |
| the filter over a class, `Query` and `Count`, with `Where` and `Order` in the formula language | shipped |
| the read-only `SQL` endpoint with parameters | shipped |
| the event log, `Events`; the `_states` view of state entries and exits | shipped with Phase 3 |
| who and how: `created_by`, `updated_by`, `state_entered_by` and `state_entered_from` on every document and version; `origin` on every event that enters a state (`command`, `action`, `guard`); in `_states` one row for every entry, a return into the same state too, with `entered_by`, `entered_from`, `entered_via`, `entered_how`, `left_by`, `left_via`, `left_how` | in `main`, ships with v0.4.0 |
| the event of a creation names the state the document was created in, under `to_state`, as the event of a transition names the one it entered; so the log alone tells every entry | in `main`, ships with v0.4.0 |
| the same facts for one document inside a formula, `FIRSTENTERED("approved")`, `ENTRIES("sent")`, `DAYSIN("review")` and the like | in `main`, ships with v0.4.0: a formula of the class may ask them; a filter may not, so across documents read the `_states` view with `SQL` |
| a picture as of a moment, `At(t)` on reads | shipped |
| DuckDB projection and the mining views | designed, Phase 8 |
| a mining narrative over the views | designed, Phase 11 |

## Procedure, in outline

1. **Know the tables.** The class view holds the current version of each
   document; history holds every version; the event log holds every
   commit with its principal, its moment and its business date; the
   `_states` view holds each entry into and exit from a state, with who
   made each move, by which transition and how: `entered_how` and
   `left_how` say `command` for a step somebody asked for, `action` for
   one a rule of a document asked for, and `guard` for one the document
   took by its own condition, so the share of a process that runs by
   itself is a count.
2. **Ask in SQL, read-only.** Time in a state is the gap between an entry
   and the exit that follows it. Rework is a state entered twice by one
   document. Approver latency is that gap grouped by the principal who
   made the exit. A refusal is not an event: nothing was written, so
   what was refused lives with the operator's log, not here.
3. **Pick the moment.** `At(t)` on a read shows the process as it stood
   then, for a report that must match what a person saw.
4. **Report with the query.** A finding travels with the SQL that
   produced it, so the next run is a rerun.
5. **Hand the finding to the designer**, as a change to the class: a
   guard that is too tight, a state nobody leaves, a right that blocks
   the wrong group.

## References, to be written

`../reference/querying.md`, `statechart.md`,
`commits-and-previews.md`. Their sources are listed in
[`../reference/README.md`](../reference/README.md).
