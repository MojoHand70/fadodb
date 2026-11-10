---
name: fadodb-simulate
description: Simulate a FadoDB process before it runs for real. Copy a class into a scratch database, replay a sequence of commits and transitions, inspect what the formulas, guards and eventless transitions do at chosen moments, compare two versions of a class, and keep only the one that worked. Use this skill whenever someone asks what would happen if, wants to test a draft class or a policy change against realistic data, rehearse a month-end, an approval chain, a rollout or a deadline, or check scheduled transitions ahead of time, whether or not they use the word simulate.
metadata:
  hat: simulator
  status: stub
---

# Simulate a FadoDB process

**Status: stub, 2026-11-09.** This page says what the simulator does, what
works today and the outline of the procedure. The reference files it
names under `../reference/` are not written yet.

## The hat

The simulator answers "what would happen" without touching the real
database. A FadoDB database is one file and opening one is cheap, so an
agent runs one sandbox per experiment, replays a scenario, queries the
result and discards the file or adopts the class.

## What works today

| Means | Mark |
|---|---|
| a scratch database per experiment: `Open` on a temporary path | shipped |
| seeding: `DefineClass`, `DefineTable`, `ImportRows`, `CreateMany` | shipped |
| a preview at a chosen moment: `At(t)` on `Preview`, `PreviewDraft`, `Transitions`, `Query`, `Get` and `History` | shipped |
| a commit at a chosen moment | not designed: a commit runs at the database's clock |
| scheduled rules that fire with time | designed, Phase 4 |
| replaying an event log into a database | not designed |

## Procedure, in outline

1. **One file per experiment.** Open a database on a scratch path. Never
   simulate in the file the process runs in.
2. **Define and seed.** Define the class under test, or two variants
   under two names, with the tables they look up and enough documents to
   make the question real.
3. **Replay the scenario as commits**, as the operator would, with
   principals and keys. Where the scenario needs time to pass, ask with a
   preview at that moment: `At` makes `NOW()` and `TODAY()` that instant
   in every default, guard and eventless guard, and the preview reports
   the path the document would take. A commit itself always runs at the
   database's clock, so a scenario that must commit on the 15th is read
   as a preview on the 15th, not written.
4. **Query the result and the log.** The filter, the read-only SQL
   endpoint and `Events` say what happened; compare it with what the
   process owner expects.
5. **Discard or adopt.** Delete the scratch file, or define the class
   that worked in the real database.

## References, to be written

`../reference/commits-and-previews.md`, `statechart.md`,
`querying.md`. Their sources are listed in
[`../reference/README.md`](../reference/README.md).
