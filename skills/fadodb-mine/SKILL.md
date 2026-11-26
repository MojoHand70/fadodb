---
name: fadodb-mine
description: Analyse how a FadoDB process actually runs from its event log, its history and its views. Cycle time per state, where documents wait, rework loops, who decides slowest, how much runs by itself, which steps an agent took, throughput and backlog, as SQL over the views every class has anyway. Use this skill whenever someone asks for process mining, bottlenecks, SLAs or cycle times, an audit trail, who did what and when, a report or a dashboard over documents in FadoDB, or wants a finding to feed back into a redrafted class.
metadata:
  hat: analyst
  status: written
---

# Mine a FadoDB process

**Status: written, 2026-11-14.** This page says how to find out how a
process really runs: what to read in the manual, one piece of work
statement by statement with the engine's own answers, and what goes
wrong most often. A test on each push defines the class this page
shows, plays what the page says happened, runs its statements and
compares the answers with the ones below.

## Read first

| Read | For |
|---|---|
| [`docs/manual/views-and-sql.md`](../../docs/manual/views-and-sql.md) | the reference for this hat: the views of a class and their columns, how a value looks in SQL, what a statement may do. Read it whole before you write a statement |
| [`docs/manual/notions/history-and-the-log.md`](../../docs/manual/notions/history-and-the-log.md) | versions and events, what an event carries, and that a refusal writes nothing |
| [`docs/manual/notions/states-and-transitions.md`](../../docs/manual/notions/states-and-transitions.md) | what a stay in a state is, and how a document moves |
| [`docs/manual/notions/time.md`](../../docs/manual/notions/time.md) | transitions that the engine takes by itself, which the log marks `timer` |
| [`docs/manual/commands.md`](../../docs/manual/commands.md#sql) | the command `sql`: the statement, and the values for its `?` |

## A piece of work, statement by statement

The class. Somebody asks for a purchase and files the request. The
approver it names approves it, rejects it with a note, or returns it
with a note to be filed again. A request nobody decides in five days
expires.

```json
{
  "name": "Request",
  "title": "\"Request for \" & what",
  "variables": {
    "what":     {"type": "text", "required": true},
    "amount":   {"type": "money", "scale": 2, "required": true},
    "approver": {"type": "text", "required": true},
    "note":     {"type": "text"}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"]},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed"},
    {"name": "return",  "from": "filed", "to": "draft",    "guard": "NOT ISBLANK(note)"},
    {"name": "approve", "from": "filed", "to": "approved"},
    {"name": "reject",  "from": "filed", "to": "rejected", "guard": "NOT ISBLANK(note)"},
    {"name": "expire",  "from": "filed", "to": "expired",  "after": "5d"}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {
      "file":    "CREATEDBY() = USER()",
      "return":  "approver = USER()",
      "approve": "approver = USER()",
      "reject":  "approver = USER()",
      "expire":  "FALSE"
    }
  },
  "rights": {
    "filed": {
      "*":    {"write": "FALSE"},
      "note": {"write": "approver = USER()"}
    }
  }
}
```

What happened, in the week of 2 November 2026. Marek and Iza are
approvers. `intake-agent` is an agent that files requests it gets by
mail.

<!-- what happened -->
| Day | At | Who | Does |
|---|---|---|---|
| Mon 2 | 09:00 | ola | files `R-1`, a laptop, 6000.00 PLN, for marek |
| Mon 2 | 09:00 | ola | files `R-2`, a monitor, 1500.00 PLN, for iza |
| Mon 2 | 10:00 | piotr | files `R-3`, a trip to Lisbon, 8000.00 PLN, for iza |
| Mon 2 | 13:00 | marek | approves `R-1` |
| Tue 3 | 09:00 | iza | returns `R-2`: "which monitor?" |
| Tue 3 | 09:00 | intake-agent | files `R-4`, paper, 200.00 PLN, for marek |
| Tue 3 | 10:00 | ola | says which monitor, and files `R-2` again |
| Tue 3 | 11:00 | marek | approves `R-4` |
| Wed 4 | 09:00 | piotr | files `R-5`, a chair, 900.00 PLN, for iza |
| Wed 4 | 10:00 | iza | rejects `R-3`: "no budget this year" |
| Thu 5 | 09:00 | ola | files `R-6`, a desk, 1200.00 PLN, for marek |
| Thu 5 | 10:00 | iza | approves `R-2` |
| Mon 9 | 09:00 | the engine | takes `expire` of `R-5` |

The program that runs you tells the engine that you are in the system
group `analysts`. It is 12:00 on Monday 9 November. Every question
below is one command `sql`.

**1. Where do the requests stand now?** The view of the documents as
they are, and no other, answers "now".

<!-- command: sql -->
```jsonc
{"statement": "select state, count(*) as requests from Request group by state order by state"}
```

<!-- answer -->
```jsonc
{
  "columns": ["state", "requests"],
  "rows": [["approved", 3], ["expired", 1], ["filed", 1], ["rejected", 1]]
}
```

**2. How long does a request stay in each state?** The view of stays
has one row for each stay, with its length in seconds. Stays that have
not ended are left out.

<!-- command: sql -->
```jsonc
{"statement": "select state, count(id) as stays, round(avg(duration) / 3600.0, 1) as hours from Request_states where left_at is not null group by state order by state"}
```

<!-- answer -->
```jsonc
{
  "columns": ["state", "stays", "hours"],
  "rows": [["draft", 7, 0.1], ["filed", 6, 41]]
}
```

**3. Who decides slowest?** `left_by` is who made the move out of the
state, and `left_how` tells a person's move from the engine's.

<!-- command: sql -->
```jsonc
{"statement": "select left_by as approver, count(id) as decisions, round(avg(duration) / 3600.0, 1) as hours from Request_states where state = 'filed' and left_how = 'command' group by left_by order by hours desc"}
```

<!-- answer -->
```jsonc
{
  "columns": ["approver", "decisions", "hours"],
  "rows": [["iza", 3, 40], ["marek", 2, 3]]
}
```

**4. What came back for rework?** A request that was filed more than
once.

<!-- command: sql -->
```jsonc
{"statement": "select id, count(id) as times_filed from Request_states where state = 'filed' group by id having count(id) > 1"}
```

<!-- answer -->
```jsonc
{
  "columns": ["id", "times_filed"],
  "rows": [["R-2", 2]]
}
```

**5. How much runs by itself?** Every move says how it came about: a
caller's command, a rule's action, a guard, or a timer.

<!-- command: sql -->
```jsonc
{"statement": "select entered_how as how, count(id) as moves from Request_states where entered_from is not null group by entered_how order by entered_how"}
```

<!-- answer -->
```jsonc
{
  "columns": ["how", "moves"],
  "rows": [["command", 12], ["timer", 1]]
}
```

**6. Which steps did an agent take?** Every event says what its
caller was.

<!-- command: sql -->
```jsonc
{"statement": "select doc_id, kind, name from Request_events where actor_kind = 'agent' order by ts, kind"}
```

<!-- answer -->
```jsonc
{
  "columns": ["doc_id", "kind", "name"],
  "rows": [["R-4", "created", null], ["R-4", "transition", "file"]]
}
```

**7. What has waited longest, and for how long?** A stay that has not
ended has no length yet. Give the statement the moment of the asking
as a value, here 12:00 on 9 November, and it works the wait out.

<!-- command: sql -->
```jsonc
{
  "statement": "select id, (? - entered_at) / 3600 as hours_waiting from Request_states where state = 'filed' and left_at is null order by entered_at",
  "args": [1794225600]
}
```

<!-- answer -->
```jsonc
{
  "columns": ["id", "hours_waiting"],
  "rows": [["R-6", 99]]
}
```

**The finding, and where it goes.** A request waits 41 hours in
`filed` on average; with Iza 40 hours, with Marek 3. One of six came
back for rework, and one expired with nobody deciding. That goes to
the designer as a change of the class, with the statements beside it:
a reminder to the approver after two days, or a second approver. The
next run of the same statements says whether it helped.

## What goes wrong most often

1. **Reading an amount as it is stored.** 6000.00 PLN is 600000 in
   SQL, with PLN in `amount_currency`. Divide by 100 for two decimal
   places, and never add up two currencies.
2. **Reading a day as a number.** A date is days since 1970 and a
   moment is seconds: `date(business_date * 86400, 'unixepoch')`,
   `datetime(ts, 'unixepoch')`.
3. **Asking the stays what waits now.** A stay that has not ended
   belongs to a document that is there, or to one that was deleted:
   the last stay of a deleted document has no end. Count what waits in
   the view of the documents, as question 1 does.
4. **Averaging without the stays that have not ended.** `duration` is
   blank while a document is in the state, so the longest waits are
   the ones an average leaves out. Ask for them apart, as question 7
   does.
5. **`count(*)` over the stays.** It is refused with `not_allowed`, a
   fault of the engine. Count a column: `count(id)`.
6. **Looking for refusals in the log.** A refusal writes nothing. The
   log has what happened, and not what was tried. What an action was
   refused with is there: the events `action_failed`.
7. **Reading the versions as the present.** The view with `_history`
   has every version of every document. "Now" is the view named after
   the class.
8. **Asking for the rows.** A statement that answers more than 10,000
   rows is refused whole with `too_many_rows`. Let SQL group and
   count, and ask for the result.
9. **A class that was defined a moment ago.** SQL does not read a
   class that was defined after the program ran its first statement,
   until the database is opened again: its columns are refused with
   `not_allowed`. It is a fault of the engine.
10. **Taking a name for more than it is.** `entered_by` and `actor_id`
    are who the program that holds the engine said was calling, and
    `actor_kind` says `agent` only where that program said so.
11. **Handing on what you read.** You read every document, past every
    rule of its class. The person who asked may not. Answer with
    counts and sums, and name a document or a value only to somebody
    who may see it.

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
| the read-only `SQL` endpoint with parameters | shipped. It reads every row and every variable, so it is for the system group `analysts`, which the host program gives a caller in `Principal.System`, and for nobody else; a filter, `Query`, is what every caller has. Every statement is kept in the log of reads (ADR-0067): its text, its arguments, and how many rows it returned or the code it failed with, never the rows; a reason given with `fado.Reason` (the member `reason`) is kept beside it. Database administrators read that log; it is not a view | shipped; the log of statements with v0.7.0 |
| the flow of a class in one command, `flow` (in Go `Flow`): the documents in each state now with the moment the oldest entered it, the stays that ended in a period with their average and their longest, and the moves along each transition, counted over the documents the caller may read; `since` and `until` name the period | shipped (ADR-0063). Every caller has it, an analyst or not: start here, and go to `SQL` for what it does not answer, such as the paths documents took or the moves of one person. `docs/manual/guides/see-where-documents-wait.md` |
| the event log, `Events`; the `_states` view of state entries and exits | shipped with Phase 3 |
| whether the sender of a change had read what it changed: `seen_version` on every event and in the `_events` view, the version the command named, null when it named none (ADR-0078). `SELECT actor_id, count(*) FROM Invoice_events WHERE kind = 'transition' AND name = 'approve' AND seen_version IS NULL GROUP BY 1` is who approves without saying what they read. Null for everything before a caller began to send it, and for what the engine does by itself | shipped after v0.7.0 |
| who and how: `created_by`, `updated_by`, `state_entered_by` and `state_entered_from` on every document and version; `origin` on every event that enters a state (`command`, `action`, `guard`, `timer`); in `_states` one row for every entry, a return into the same state too, with `entered_by`, `entered_from`, `entered_via`, `entered_how`, `left_by`, `left_via`, `left_how` | shipped with Phase 4 |
| what each actor was and for whom it acted: `actor_kind` (`person`, `agent`, `service`) and `actor_for` on every event and in the `_events` view; `entered_by_kind`, `entered_for`, `left_by_kind` and `left_for` in `_states` | shipped with Phase 5: `SELECT name, actor_id, actor_for FROM Invoice_events WHERE actor_kind = 'agent'` is which steps an agent took and for whom. Blank where the host program did not say, and for everything before it did |
| who allowed an act for somebody: every `actor_for` on an event was checked against a substitution when the command was given (ADR-0053), unless the host program keeps its own. The substitutions are documents: the view `Substitution` has `by`, `for`, `from`, `until`, `state` and `created_by`, who gave it, and `Substitution_events` says who withdrew one and when. The one that let a step happen is found from the two ids and the time of the event; an event does not name it | shipped with Phase 5 |
| the event of a creation names the state the document was created in, under `to_state`, as the event of a transition names the one it entered; so the log alone tells every entry | shipped with Phase 4 |
| the same facts for one document inside a formula, `FIRSTENTERED("approved")`, `ENTRIES("sent")`, `DAYSIN("review")` and the like | shipped with Phase 4: a formula of the class may ask them; a filter may not, so across documents read the `_states` view with `SQL` |
| a picture as of a moment, `At(t)` on reads | shipped |
| how the stays of each state spread, where an average misleads: `flow` with `"spread": true` (in Go `FlowPeriod{Spread: true}`) adds to each state `spread`, with `middle_seconds`, the stay half of the stays did not outlast, and `slow_tenth_seconds`, the stay nine in ten did not outlast | shipped after v0.7.0 (ADR-0095, proposed). Read the slow tenth to find where documents wait: a state whose middle stay is an hour and whose slow tenth is three days has a tail the average hides. Each is the length of one real stay, the one at place `(n+1)/2` and at place `(9n+9)/10` of `n` stays in the order of their lengths, so both programs give the same number. Ask for it only when it is wanted: it orders every stay of the period, 80 to 87 ms among ten million moves in `fadodb` where the flow alone takes 12 |
| DuckDB projection and the mining views | the copy of the documents in DuckDB is kept and fed since after v0.7.0, by a program built with the module `analytics` (ADR-0089, step 1); `flow` is answered from the copy there (step 2), the same answer in 19 ms among ten million moves where the store takes 18 seconds, since the copy keeps the moves of the documents ready; so are `count`, and the total a `LOOKUP` in a live variable or a figure over a list asks for, when no index narrows them (step 3, its first part), in milliseconds where the store reads the class; a page of a list is found in the copy when the store has not found it in five milliseconds, or would read and sort the class for it (step 3, its second part); the program `fadodb` has the engine and serves a database with its copy; `fadodb-core`, the same program in pure Go, is built without it and answers by the store (`fadodb version` says "with the analytical engine"); the mining views are designed, Phase 8 |
| a mining narrative over the views | designed, Phase 11 |

## Procedure, in outline

1. **Know the tables.** The class view holds the current version of each
   document; history holds every version; the event log holds every
   commit with its principal, its moment and its business date; the
   `_states` view holds each entry into and exit from a state, with who
   made each move, by which transition and how: `entered_how` and
   `left_how` say `command` for a step somebody asked for, `action` for
   one a rule of a document asked for, `guard` for one the document
   took by its own condition and `timer` for one a timed transition
   made when its time had come, so the share of a process that runs by
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

## Where the rest is

The reference this page once planned under `../reference/` is the
manual now: the chapters under "Read first" above. The decision
records behind them are listed in
[`../reference/README.md`](../reference/README.md).
