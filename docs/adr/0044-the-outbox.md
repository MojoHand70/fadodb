# ADR-0044: The outbox: what a commit queues, the worker and the engine's own actions

- Status: Accepted; amends ADR-0006 (which outcomes of an action are events), ADR-0009 (the row, the order, which failures are retried, exactly once for the engine's own actions, a bound on chains), ADR-0026 (the engine's commands carry keys under a reserved prefix), ADR-0027 (how the worker hands a row to an action), ADR-0043 (a `refresh` that names another document is an outbox action; a `create` with `each` queues a row for each element)
- Date: 2026-11-09

## Context

ADR-0009 decided the shape: an action that is not synchronous is a row in
the `outbox` table, written in the transaction of the event that caused
it, run afterwards by a worker in the same process, keyed by event id and
action index, retried with exponential backoff, moved to dead letter
after a number of attempts, its result an `action_run` or `action_failed`
event. ADR-0027 added the actions an embedder registers. ADR-0043 says
when a commit writes a row and with what arguments. The acceptance
criterion of the phase is that a crash in the middle of a commit never
loses or duplicates an action, and that the throughput is measured and
published.

What is not decided: what the row holds; in which order rows run, which
matters to a receiver that books what it is sent; which failures are
worth a retry; whether every attempt is an event; how the engine's own
actions, which write to the same database, avoid running twice; what
stops two classes whose rules call each other; whose rights an action on
another document runs with; how a caller, and above all an agent with no
person watching, sees what is stuck and moves it; and how a test or a
simulation runs the queue at a moment it chooses.

## Decision

1. **The row.** One row of `outbox` per queued action, written by the
   commit whose rule ran: `event_id`, the event of that commit; `idx`,
   the position of the action among the actions that event queued, from
   0 in the order of ADR-0043; the class, the document id and the version
   the commit made; the class version and the path of the rule; the
   action's name; `args`, the evaluated arguments as JSON in the wire
   shape of ADR-0018; the command's principal, as id, groups and the
   admin flag (item 11); `depth` (item 6); `status`, one of `pending`,
   `done` and `dead`; `attempts`; `due_at`; `last_error`, a code and a
   message; `created_at` and `finished_at`. The key of ADR-0009 is the pair,
   written `<event_id>:<idx>`. A done row is kept: it is the record of
   what was asked, with its arguments.

2. **The worker.** `Open` starts one worker per database. A commit that
   queued a row wakes it, and so does the earliest `due_at`. It runs the
   engine's own actions one at a time, since they take the writer, and
   up to `Workers` actions that leave the database at once. `Close` waits
   for the actions in flight. The options are a group of `Open`:
   `Outbox.Manual`, `Outbox.Workers` (4), `Outbox.MaxAttempts` (10) and
   `Outbox.Backoff` (one second, doubling, so about nine minutes in all).
   `Drain(ctx)` runs every row that is due, in order, on the caller's
   goroutine, and returns how many it ran; with `Manual` the worker does
   not start and `Drain` is the only thing that runs actions. A row is
   due by the database's clock (`Options.Now`), so a test, the
   conformance suite and a copy of a file opened under a chosen clock
   (ADR-0042) run the queue at the moment they choose.

3. **Order.** The rows of one document run one at a time in the order of
   their keys, which is the order their events happened in. A row that
   waits for a retry holds back the later rows of its document; a dead
   row does not. Rows of different documents do not wait for each other.

4. **Three outcomes.** An action ends in one of three ways.

   | Outcome | When | The row | The event |
   |---|---|---|---|
   | It ran | the action returned | `done` | `action_run` |
   | No answer | a registered action returned an error, a call got no reply or a reply that says try again, the store was busy | `attempts` + 1 and a later `due_at`; after `MaxAttempts`, `dead` | `action_failed`, once, when it dies |
   | A refusal | the engine refused the command the action stands for, with any code; an action returned an error it marked permanent; a call was answered with a refusal | `dead` at once | `action_failed` with the code |

   A retry repeats a question that got no answer. A refusal is an
   answer: `wrong_state` or `guard_false` from the document an action
   wanted to move is recorded, not asked again. A process that must wait
   for another document says so with a guard, not with retries. The two
   events belong to the document that caused the action, carry the
   action's name and the row's event as `cause_event_id`, and appear in
   `<Class>_events`. The attempts in between are state of the row, not
   events: the log says what happened, once.

5. **Exactly once for the engine's own actions.** `create`,
   `transition`, `refresh` and the `table.*` actions write to the
   database the outbox is in. The
   worker makes the action's commit, marks the row done and appends
   `action_run` in one transaction. A crash before it leaves the row
   pending and nothing done; a crash after it leaves both. The commit
   carries the command key `outbox:<event_id>:<idx>`, a prefix a caller's
   key may not use (`bad_value`), and the row's event as its
   `cause_event_id`, so every consequence leads back to its cause. An
   action that leaves the database (ADR-0045) runs at least once: a crash
   between its effect and the mark repeats it with the same key, and the
   receiver makes twice once.

6. **A bound on chains.** A row queued by a caller's command has depth 0.
   A row queued by a commit the worker made while running a row of depth
   d has depth d + 1. A row deeper than 16 is written dead with
   `too_deep` and its `action_failed` event. Two classes whose rules
   move each other's documents therefore stop, with a record of where.

7. **The engine's own outbox actions.**

   | Action | Arguments | Does |
   |---|---|---|
   | `create` | `class`, `values`, optionally `id`, optionally `each` with `as` (item 12) | creates a document, or one for each element of a list |
   | `transition` | `class` (the rule's own by default), `id`, `name`, optionally `values` | takes a named transition on a document |
   | `refresh` | `class` (the rule's own by default), `id`, optionally `variables` | sends `Refresh` to another document (item 13) |
   | `table.insert` | `table`, `row` | inserts one row; an existing key is the refusal `duplicate` |
   | `table.update` | `table`, `key`, `set` | changes the row with that key; none is `unknown_row` |
   | `table.delete` | `table`, `key` | deletes the row with that key |

   `class`, `table` and `name` are names; `id` and everything under
   `values`, `row`, `key` and `set` are formulas over the document that
   fired the rule. The class, the transition, the table, the variables
   and the columns are checked when the rule's class is defined, and the
   formulas are typed against what they fill. A redefinition of the
   target that removes or retypes something a rule of another class
   fills is refused with `not_allowed` naming that rule, as ADR-0039
   refuses what the stored documents stand in the way of. A table action
   addresses one row by its key; a change of many rows is `ImportRows`.
   It drops the table's index (ADR-0036) and is no document event: its
   `action_run` is the record.

8. **Operating the queue.** `Outbox(ctx, filter)` lists rows by status,
   class and document, each with its key, action, arguments, attempts,
   next attempt and last error. `RetryAction(ctx, key)` makes a dead row
   pending again with its attempts at zero; the outcome it then reaches
   is a further event. The view `<Class>_actions` shows the rows the
   documents of a class queued, for the SQL endpoint. A dead row is never
   retried by itself and never deleted.

9. **The preview** lists what a command would queue, with the evaluated
   arguments (ADR-0043), writes no row and runs nothing.

10. **The acceptance test** stops the process at each step, after the
    commit and before the worker, inside an action, and between an
    action's effect and its mark, reopens the file and counts. Every
    engine action has happened exactly once. Every action that left the
    database reached a test receiver at least once, each time with the
    same key.

11. **An action runs as the person whose command queued it.** The owner
    ruled this on 2026-11-10; the draft of this record had recommended
    the other answer. The worker runs `create`, `transition`, `refresh`
    and the `table.*` actions as the principal on the row: the id, the
    groups and the admin flag the command had when its commit queued the
    row. The engine keeps no users (ADR-0027), so the groups are those of
    that moment, not of the moment the action runs. The field rights and
    the transition rules of the target (ADR-0041) apply as if the person
    had sent the command, and so will the access rules of Phase 5: a
    rule never does what the person who set it off may not do. What they
    may not do is a refusal (item 4): the row is dead at once with
    `forbidden` and the variable or the transition it names. An
    administrator's command queues actions with an administrator's
    bypass. A command without a principal queues actions that run as
    nobody (ADR-0041, item 6). A chain keeps its principal: the worker
    makes the action's commit as the row's principal, so the rows that
    commit queues carry the same one.

    The rule is about the commands the worker sends. What a rule sets on
    its own document inside the commit (ADR-0043, item 4) and the result
    an action writes back (ADR-0045, item 8) stay the engine's writes,
    as the value of a formula is. A table has no rights of its own
    today, so a table action is refused only for what the table refuses.

    Rejected: the class acts, with the person's id on the events and an
    administrator's bypass. A class could then say "nobody creates a
    receipt by hand; paying an invoice makes one", and a rule could do
    what nobody who sets it off may.

12. **`each` on `create`**, ruled in on 2026-11-10. One child per element
    of a list:

    ```json
    {"action": "create", "class": "Approval", "each": "approvers", "as": "approver",
     "values": {"invoice": "ID()", "approver": "approver"}}
    ```

    `each` names an `array` or a `rows` variable of the rule's class;
    any other variable is `type_mismatch`. `as`, required with it, names
    the element for the formulas under `values` and `id`; the name of a
    variable of the class is `duplicate`. An element of an array is a
    value of the array's type. An element of a rows variable is an
    object of its schema, read by field (`line.sku`, ADR-0031). The
    commit evaluates the arguments once for each element, in the order
    of the list, and writes one row for each with the next `idx` of
    item 1. Every child therefore has its own key, its own attempts and
    its own `action_run` or `action_failed`.
    A blank or empty list queues nothing. The cap on a composite, 10,000
    elements (ADR-0013), is the bound. An argument that errors for one
    element refuses the command and names the element's position
    (ADR-0043, item 6). `into` beside `each` is refused at definition
    time with `not_allowed`: many results have no one variable to go to.
    The preview lists every row.

13. **`refresh` of another document**, ruled in on 2026-11-10. A
    `refresh` that carries an `id` is an outbox action:

    ```json
    {"action": "refresh", "class": "Invoice", "id": "invoice", "variables": ["approvals"]}
    ```

    The worker sends the `Refresh` command of ADR-0043, item 10, to that
    document: the named triggered variables recompute, or every one that
    lists `refresh` when none is named; the commit is always made; and
    the eventless transitions follow it. A child that has finished wakes
    its parent this way without knowing the parent's transitions, and
    the parent moves by its own guards. It runs exactly once (item 5),
    as the person (item 11), under the depth bound (item 6), and its
    class and variables are checked when the rule's class is defined
    (item 7). `Refresh` asks no write right, so until the access rules
    of Phase 5 anybody's child may wake its parent. A target in a
    terminal state refuses with `terminal`, and the row is dead with it:
    a child that reports to a closed parent is recorded, not retried.
    Without an `id`, `refresh` stays the synchronous action of ADR-0043
    on the rule's own document, and a `class` without an `id` is
    `missing`.

    The count a parent's guard reads, a `LOOKUP` over its children,
    needs documents as a lookup source. That is in the record on
    `QUERY`, and the pattern of parallel approvals is whole only with
    it.

## Details settled while building

The first step of the outbox built items 1 to 6 and 8 to 11 with `create`
as the first action, on 2026-11-10, and settled these points.

- **The code of an `action_failed` event** is read from the row of the
  outbox, which names the event of its outcome in a column
  `outcome_event`; the `events` table gains no column. `Events` returns
  it as `code`, and `<Class>_actions` shows it beside the row. The event
  is linked to the document that caused the action with the qualifier
  `caused`.
- **The business date of an action's commit** is that of the event that
  queued it, and so is the business date of the outcome's event: a
  consequence belongs to the day of its cause, whenever the worker
  reaches it.
- **The engine's "no answer"** is a failure of the store. Every code
  with which the engine refuses a command is an answer.
- **The row's `rule`** is the path of the action, `/rules/2/do/0`. A row
  that is retried keeps its last error until the next attempt replaces
  it.
- **The wait before attempt n** is `Backoff` doubled n − 2 times,
  counted on the database's clock in whole seconds. A drain counts a row
  it attempted whether the row then ran, waits or died.
- **What is checked when the rule's class is defined.** The class a
  `create` names exists and has states; each value is a scalar input of
  it and its formula has the variable's type; an input the class
  requires and has no default for is given. A class may create
  documents of itself. A stored definition is not checked again when the
  file opens, since a class may name one defined after it: what a later
  redefinition broke is found when the action runs.

The second step built the rest of items 7, 12 and 13, on 2026-11-10:
`transition`, `refresh` of another document, `each` on a `create`, the
`table.*` actions, and the refusal of a redefinition that breaks another
class's rule. It settled these points.

- **A `table.delete` of a key the table lacks** deletes nothing and is
  done. Item 7 names a refusal for an insert and for an update and none
  for a delete: the row is not there, which is what was asked.
- **A blank id or key is the target's refusal, not the command's.** A
  `transition` or a `refresh` whose `id` gives no value is queued and
  dies with `unknown_document`; a table action whose key column is blank
  dies with `required`. Item 6 of ADR-0043 refuses a command for an
  argument that errors, and a blank is a value. A rule that should do
  nothing for a blank says so with its `if`.
- **Which redefinition breaks a rule.** One after which the rule's class
  would no longer be accepted: a variable or a column the rule fills is
  gone, became computed, or takes a type the rule's formula does not
  have; the transition it takes is gone; a variable it refreshes is no
  longer triggered or no longer lists `refresh`; the class lost its
  states; the key of the table changed. A change of type the formula
  still fits, a wider scale for instance, breaks nothing and is taken.
  The diagnostic sits at the changed element of the new definition and
  names the class and the path of the action.
- **A new input the target requires is not refused.** Item 7 says the
  fix is to change the rule first, and a rule cannot give a variable
  that does not exist yet. The redefinition is taken, and a `create`
  that does not give the input is dead with `required` when it runs,
  until its rule is changed.
- **The list an `each` goes over** is the one the commit leaves: the
  list the command gave, else the one the document held, and empty after
  a `reset` of it earlier in the same command. A rule on `delete` goes
  over the list as it was. An element whose argument errors is named in
  the message by its position from zero, `approvers[2]`, and a preview
  lists that element without arguments.
- **`as` is a name like a variable's** and is refused alone, without
  `each`, with `missing`. `into` beside `each` will be refused with the
  step that brings `into` (ADR-0045); today no action has that property.
- **A table action** names every column of the key under `key`, or
  under `row` for an insert, and nothing but key columns under `key`. An
  update changes no key column (`not_allowed`): the row is deleted and
  another inserted. Only a table the database file holds can be written
  by a rule, which today is every table.

## Rulings

The draft of this record put three questions to the owner. He ruled on
all three on 2026-11-10. An action on another document runs as the
person, which is item 11; the draft had recommended that the class act.
Both additions for parallel approvals are in, which are items 12 and 13.
And time is in this phase, in the two dimensions he named.

**Time.** The draft asked whether timers belong to this phase. The
owner's answer is that time has two dimensions here:

1. *Conditions on time*: whether a document could, or should, move from
   one state to another.
2. *Timers and a scheduler*: firing a transition at a given time, with
   its retries.

Ruled: the phase builds both, the first before the second, and the
second as its last step before the measurements. Neither is designed
here. A record of their own, put to the owner before that step, says
how each is written and what it does. What follows is where they start
from.

What the accepted records give the first, for one document. A guard is
a formula and may read `TODAY()`, `NOW()` and any date the document
holds. `Transitions`, at the database's clock or at a moment given with
`At` (ADR-0042), says whether the document could move. An eventless
transition with such a guard, `TODAY() > due`, says that it should, and
a preview that changes nothing shows at the same moment where it would
land. The move itself is made when a commit touches the document
(ADR-0040, item 4): at the next `Set` or `Refresh`, not when the time
comes. Two things are missing. The moment a document entered its state
is a column a filter reads (ADR-0038); ADR-0013 lists it among the
system variables, and no formula reads it yet, so "seven days in this
state" cannot be written as a guard. And nothing lists the documents of
a class whose eventless transition would fire now, so a caller cannot
ask what is due without asking document by document.

What they give the second: nothing fires by itself. The mechanism it
needs is the `due_at` of item 1: a row queued when a document enters a
state, which sends the move when it is due, is retried like any row, and
is cancelled without an event when the document has left the state. A
timer needs its moment stated, as `"after": "7d"` or as a date the
document holds, because the engine cannot solve a guard for the time at
which it turns true. It would run by the database's clock, so a
simulation is a copy of the file opened with `Options.Now` and a
`Drain`, and commits need no clock of their own (ADR-0042 stands). Under
item 11 its row would carry the principal of the commit that queued it.
`schedule` on a rule and `schedule:` on a variable recompute many
documents at once and stay with the set-wise work of Phase 8 either way.

## Consequences

- A document's effects reach a receiver in the order they happened, and
  the receiver can detect a gap by the document's version. The price is
  that one row waiting for a retry delays the later rows of its
  document, never those of another.
- The engine's own actions need no idempotent receiver: the database is
  its own witness. Effects on other documents remain eventually
  consistent, as ADR-0009 says; they are no longer at least once.
- A refused action is dead at the first attempt and says why. An author
  who used retries as a way to wait finds a dead row and writes a guard.
- An agent operates the queue with a listing, one command and a view,
  and a dead row waits for it indefinitely. Nothing alerts anyone: the
  host watches `<Class>_actions` or the `action_failed` events.
- The queue runs under the database's clock, so the previews of ADR-0042
  and a copy of the file cover what happens later as well as what
  happens now.
- What a rule does to other documents is bounded by the rights of
  whoever sets it off. The person is not known when a class is defined,
  so a missing right is found at run time, as a dead row with
  `forbidden` the first time such a person makes the move. The author
  grants the target's rights to every group that may cause the action,
  and a class cannot keep for its rules what it denies to people.
- One commit queues as many rows as an `each` has elements, and each
  child is created, retried and reported by itself.
- A redefinition can be refused because of another class's rule. The
  diagnostic names the rule, and the fix is to change that rule first.
- The outbox grows with the events. The retention command of ADR-0006
  archives done rows with the events they belong to.

## Performance

Budgets. Queuing an action is one insert in the commit's transaction: a
transition that queues one action stays within the 2 ms p99 of ADR-0006,
and the campaign gains that row. ADR-0009 asked for the throughput to be
measured and published without naming a figure; this record names it:
at least 2,000 actions per second for an engine action and for a registered
action that does nothing, the figure ADR-0012 gives transitions, each
measured with `Drain` over 100,000 rows on disk. The worker finds the
next due row through an index on `(status, due_at)` and never scans the
done rows. `BenchmarkOutbox` measures the insert and the run and joins
the gate. A `create` with `each` pays one insert for each element; a
transition that queues ten children through it is measured and
published, not budgeted. Published as `docs/perf/v0.4.0.md`.
