# ADR-0103: The seal of the log: what happened, nobody alters unseen

- Status: Proposed
- Date: 2026-11-28

## Context

On 2026-11-19 the owner said of the log: "Answer is that we have to do
it before the security audit." He agreed the same day that it is the
first step of Phase 10. It is designed for both stores, and built in
SQLite before the Postgres store is written, so that store has it from
its first commit. On 2026-11-28 he opened Phase 10, read the page of
analysis, and said: "go ahead with the log first".

What the engine keeps today, read on main at 7f935c7:

- **The log and the histories are only ever added to** (ADR-0006,
  ADR-0014). One column is the exception: the `valid_to` of a version,
  which the next commit of the document sets.
- **The file is an SQLite file.** Whoever holds it can open it with
  any SQLite tool and change an event, a version or a line, or take one
  out. Nothing in FadoDB shows it. ADR-0035 says so: "access rules do
  not apply to raw file access".

The story the page told. Ann files expense E-1042 for 4,800.00 PLN, a
hotel and a train. On 2 November at 14:05 Jan approves it. A month
later Piotr, who looks after Acme's server, opens the file and makes
Olga the approver. An auditor who reads the log reads what Piotr left.
The tagline says "evidence built in"; today the evidence is only as
good as the trust in whoever holds the file.

Measured for the page, on an Intel Core Ultra 9 275HX, 15 runs each:
SHA-256 of what a commit writes takes 0.22 µs for 512 bytes and 0.97 µs
for 4 KB, and allocates nothing. A seal written by a statement of its
own, one INSERT per commit, cost a transition in memory 5 µs and 7
allocations. So the seal rides in a row the commit writes anyway.

## Decision

### His to rule: the four questions of the page

They are open. Step 1 below is the same for each answer, except 4,
which it builds as proposed.

1. **Two parts.** Part 1 is a seal on every event, in the file: step
   1, built here. Part 2 is the latest seal, signed with a key that is
   not in the file and handed out of it: planned. The other way is
   part 1 alone, which stops anybody but the holder of the file, and
   the holder is the one an auditor asks about.
2. **Where the signed seals go first:** the stream of changes and a
   file the operator names, on every server, the free engine too;
   FadoConsole for the hosted service.
3. **Postgres keeps one writer for each database**, so that its events
   have one order, as on SQLite.
4. **An older database is sealed from when an engine with the seal
   opens it**, and the check says from when (item 10).

### Mine, for him to strike or change: step 1

5. **Every event carries its seal**, in a new column of `events`,
   `seal`. It is a SHA-256 of the seal of the event before it, in the
   order of the log, and of the bytes of what the event wrote, in this
   order:
   - the closing of the version before, its `valid_to`;
   - its version, the history row;
   - its lists, each either with rows of its own or carried forward
     from the version before;
   - its own row;
   - its `event_docs` row.

   The first event of a log is chained to 32 zero bytes. The engine
   schema becomes 17.
6. **How a row is sealed.** A row is its values with the names of
   their columns, in the order of the names. A NULL is left out, so a
   column added to a table later, NULL in the rows before, changes
   nothing. A whole number, and a number without a fraction, are
   sealed as an integer, since SQLite may keep either one either way. A
   version's `valid_to` is sealed with the event that closed it, not
   with the version.
7. **A version that carries its lists forward seals that it did**, not
   their rows. The engine does that for a version it makes by itself,
   an eventless move after a commit. The seal has one bit for each list
   of the class after its 32 bytes, so the check knows which. The
   check then holds those rows to the rows of the version before.
8. **The seal costs a commit no statement.** It rides in the event's
   row. The writer keeps in memory the seal of the last event written,
   of the last one committed and of the last one before each
   savepoint. So a transaction taken back, whole or to a savepoint, a
   preview, or a refused document of a batch, takes its seals back with
   its rows. Every write of the store begins in one place,
   `beginWriter`, and a test holds every savepoint to the seal's own
   functions. Should the writer find an event written at another row
   than the one after its last, which only a change behind its back
   can cause, it seals the event again after the event the row
   follows, if it knows that event, and otherwise after its last, so
   that the check finds the gap.
9. **The check.** `CheckLog` in the Go package, the command
   `check_log` for administrators, and `fadodb check` for a file. It
   reads one snapshot and stops no commit. It walks the log in order
   and works each event's seal out from the rows the file holds and
   the stored seal of the event before. It names, with the document,
   the version and the event:
   - `changed`: an event whose seal does not match;
   - `unsealed`: an event without a seal where the log is sealed;
   - `carried`: a list a version carried forward that holds other rows;
   - `stray`: a version or a row of a list that no event wrote, or a
     row of `event_docs` without its event;
   - `current`: a document whose current row is not its last version,
     on the columns of its class version.

   At most 100 of each kind are named. `fadodb check` ends with 1 when
   there is any.
10. **An older database.** When an engine with the seal opens a file
    of schema 13 to 16, its events have no seal. The metadata
    `sealed_after` keeps the row of the last of them, and every event
    after it must carry a seal. The check says "sealed from" the
    moment of the first sealed event, and how many events before it are
    not sealed.
11. **Every database the store's tests make is checked as it closes.**
    So a write the seal missed, or a value the check reads back
    otherwise than the writer sealed it, fails the tests. A planted
    fault, the closing of a version left out of the seal, failed every
    test that changes a document.

### Not in this step

Each is a piece of its own:

- **Part 2**, the signed seals, after his answers to 1 and 2.
- **What happened in the engine's other tables**: the definitions and
  their versions; the rows of tables and their histories; users,
  groups and keys; sign-ins; the log of reads; the text of comments.
  Each is kept in its own table, which this step does not seal. The
  event of a comment is sealed, and its text in `fado_comments` is not.
  Planned as step 2.
- **The Postgres store** (question 3).
- **Files**: `fadodb check` already checks each by its own hash
  (ADR-0033).

## Consequences

- **Who the seal stops.** Somebody who changes, removes or adds a row
  of the log with an SQLite tool and does not work every seal after it
  out again: a careless edit, a restore that mixed two files, a tool
  that "repairs" a table. Piotr, who changes Jan's approval, is
  named, with 14:05 and the approval.
- **Who it does not stop, until part 2.** Whoever holds the file and
  works out every seal after a change, or takes the last events away,
  is not found by the check. The manual says so where it shows the
  check.
- **The events of an older database before the seal** stay as they
  were, unsealed. Before 1.0 no file has to be carried over, so this is
  a courtesy, not a promise.
- **A seal is 32 bytes, and 33 for a class with up to eight lists**: a
  million events add some 33 MB.
- **The column `seal` shows in the file**, to whoever reads it with
  DuckDB or SQLite. The `_events` views do not show it.
- **The check reads about 48 µs per event**: 4.8 seconds for 100,000
  events on disk, three runs alike. That is about 80 minutes for 100
  million. It is a check of a copy, not of every commit.

## Performance

The seal costs a transition about 1.3 µs, under 3%, and no allocation;
every benchmark of the gate keeps its allocations, or makes one fewer.
The seal adds one value to the event's row, and the SQLite driver keeps
a list of the texts and blobs it binds, which grows past 16 for the
event of a transition: one allocation of 256 bytes. The same change
boxes the name of a transition once, as the name of a state already
is, which saves one allocation per event.

`make benchgate` against main at aa1a5ee, five placements of five rounds
each, on an Intel Core Ultra 9 275HX; "no difference" is benchstat's,
for a change it does not find significant:

| Benchmark | Time, main | Time, the seal | Allocations, main → the seal | Bytes, main → the seal |
|---|---|---|---|---|
| Transition | 49.80 µs | 51.12 µs (+2.65%) | 155 → 155 | 8.711 KiB → 8.945 KiB (+2.69%) |
| TransitionAccess | 50.73 µs | 52.15 µs (+2.79%) | 163 → 163 | 8.945 KiB → 9.180 KiB (+2.62%) |
| ExecTransition | 51.39 µs | 52.74 µs (+2.63%) | 165 → 165 | 9.851 KiB → 10.090 KiB (+2.43%) |
| DoorTransition | 59.59 µs | 60.94 µs (+2.26%) | 216 → 216 | 12.84 KiB → 13.07 KiB (+1.77%) |
| TransitionKeyed | 51.66 µs | 52.43 µs (+1.50%) | 205 → 204 | 10.62 KiB → 10.60 KiB (−0.13%) |
| TransitionBefore | 60.09 µs | 61.33 µs (+2.07%) | 176 → 176 | 9.836 KiB → 10.070 KiB (+2.38%) |
| TransitionMany, 1,000 moves | 33.40 ms | 34.42 ms (+3.06%) | 146.7k → 146.7k | 8.268 MiB → 8.497 MiB (+2.77%) |
| Outbox | 200.6 µs | 205.1 µs (no difference) | 455 → 454 | 23.86 KiB → 24.08 KiB (+0.92%) |
| Timer | 139.0 µs | 142.5 µs (+2.50%) | 390 → 389 | 17.37 KiB → 17.36 KiB (−0.08%) |
| Receive | 66.85 µs | 67.87 µs (no difference) | 262 → 261 | 21.40 KiB → 21.37 KiB (−0.10%) |
| ReceiveLines | 523.5 µs | 521.6 µs (no difference) | 9,079 → 9,079 | 517.6 KiB → 517.6 KiB |
| Follow, 1,000 documents | 26.14 ms | 26.96 ms (+3.13%) | 165.4k → 164.4k | 9.315 MiB → 9.300 MiB (−0.16%) |

The gate's limits, 5% on time and bytes and none on allocations, hold
for every benchmark it runs. The check reads about 48 µs per event, as
said above.
