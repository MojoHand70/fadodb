---
name: fadodb-own-work
description: Run an agent's own work as FadoDB documents. A task ledger with the attempts as rows and the outcome as a state, an approval inbox where the agent proposes and a person disposes, hand-offs to a human as transitions, retries and timeouts as states, a multi-step operation across services as one saga document. Use this skill whenever an agent needs durable state for what it is doing, must ask a person for approval before it acts, must leave an auditable record of its own actions, resumes work after a restart, or coordinates several steps or services over time, even when FadoDB is not yet in the picture.
metadata:
  hat: own-principal
  status: written
---

# The agent's own work as documents

**Status: written, 2026-11-14.** This page says how an agent keeps
its own work as documents: what to read in the manual, one piece of
work command by command with the engine's own answers, and what goes
wrong most often. A test on each push defines the class this page
shows, plays the piece of work, compares the answers with the ones
below, and holds the log to the table.

## Read first

| Read | For |
|---|---|
| [`docs/manual/notions/who-may.md`](../../docs/manual/notions/who-may.md#a-step-kept-for-a-person) | how a class keeps a step for a person, and what an agent may do beside it |
| [`docs/manual/naming-the-caller.md`](../../docs/manual/naming-the-caller.md) | what the program that runs you tells the engine about you: your id, that you are an agent, and for whom you act |
| [`docs/manual/notions/history-and-the-log.md`](../../docs/manual/notions/history-and-the-log.md#sending-a-call-twice) | the key of a command sent twice, which is how you survive a crash |
| [`docs/manual/notions/time.md`](../../docs/manual/notions/time.md) | a transition with a time: the timeout of a task that stopped |
| [`docs/manual/notions/rules-and-actions.md`](../../docs/manual/notions/rules-and-actions.md) | a rule that writes what you may not: the count of attempts |
| [`docs/manual/guides/acting-for-somebody.md`](../../docs/manual/guides/acting-for-somebody.md) | acting for a person, which needs that person's word |
| [`docs/manual/commands.md`](../../docs/manual/commands.md) | every command |

## A piece of work, command by command

The class. A task is proposed by an agent and decided by a person. The
agent carries it out and writes down the result. The class counts the
attempts, and a task that runs for an hour without an end is set
aside for the person to look at.

```json
{
  "name": "Task",
  "title": "what",
  "variables": {
    "what":     {"type": "text", "required": true},
    "owner":    {"type": "text", "default": "USER()"},
    "reviewer": {"type": "text", "required": true},
    "attempts": {"type": "int", "default": "0"},
    "result":   {"type": "text"},
    "problem":  {"type": "text"}
  },
  "states": {
    "initial": "proposed",
    "list": ["proposed", "approved", "running", "stalled", "failed", "done", "declined"],
    "final": ["done", "declined"]
  },
  "transitions": [
    {"name": "approve", "from": "proposed", "to": "approved"},
    {"name": "decline", "from": "proposed", "to": "declined", "guard": "NOT ISBLANK(problem)"},
    {"name": "start",   "from": "approved", "to": "running"},
    {"name": "finish",  "from": "running",  "to": "done",     "guard": "NOT ISBLANK(result)"},
    {"name": "fail",    "from": "running",  "to": "failed",   "guard": "NOT ISBLANK(problem)"},
    {"name": "retry",   "from": "failed",   "to": "running",  "guard": "attempts < 3"},
    {"name": "stall",   "from": "running",  "to": "stalled",  "after": "1h"},
    {"name": "resume",  "from": "stalled",  "to": "running"}
  ],
  "rules": [
    {"on": "enter", "state": "running",
     "do": [{"action": "set", "values": {"attempts": "attempts + 1"}}]}
  ],
  "access": {
    "read":   "owner = USER() OR reviewer = USER()",
    "create": "USERKIND() = \"agent\"",
    "update": "FALSE",
    "delete": "FALSE",
    "transition": {
      "approve": "reviewer = USER() AND USERKIND() = \"person\"",
      "decline": "reviewer = USER() AND USERKIND() = \"person\"",
      "start":   "owner = USER()",
      "finish":  "owner = USER()",
      "fail":    "owner = USER()",
      "retry":   "owner = USER()",
      "stall":   "FALSE",
      "resume":  "reviewer = USER() AND USERKIND() = \"person\""
    }
  },
  "rights": {
    "proposed": {
      "*":        {"write": "owner = USER()"},
      "owner":    {"write": "FALSE"},
      "attempts": {"write": "FALSE"},
      "result":   {"write": "FALSE"},
      "problem":  {"write": "reviewer = USER()"}
    },
    "*": {
      "*":       {"write": "FALSE"},
      "result":  {"write": "owner = USER()"},
      "problem": {"write": "owner = USER()"}
    }
  }
}
```

What the class keeps from you, on purpose:

- **The decision.** `approve`, `decline` and `resume` are for the
  reviewer, and for a person.
- **The count of attempts.** A rule writes `attempts`; nobody else
  may.
- **The past.** Nothing of a task changes except by a transition.

The program that runs you tells the engine that you are `ops-agent`,
an agent. Marek is the reviewer. It is 09:00 on 16 November 2026.

**1. Propose, with an id and a key of your own.** If the answer is
lost, the same command under the same key is answered again and makes
no second task.

<!-- command: create -->
```jsonc
{
  "class": "Task",
  "id": "T-1",
  "values": {"what": "delete the logs older than 90 days", "reviewer": "marek"},
  "key": "propose-T-1"
}
```

<!-- answer -->
```jsonc
{
  "document": {
    "class": "Task",
    "id": "T-1",
    "title": "delete the logs older than 90 days",
    "state": "proposed",
    "version": 1,
    "class_version": 1,
    "created_at": 1794819600,
    "updated_at": 1794819600,
    "state_entered_at": 1794819600,
    "created_by": "ops-agent",
    "updated_by": "ops-agent",
    "state_entered_by": "ops-agent",
    "values": {
      "attempts": "0",
      "owner": "ops-agent",
      "problem": null,
      "result": null,
      "reviewer": "marek",
      "what": "delete the logs older than 90 days"
    }
  }
}
```

**2. You cannot decide your own proposal.** The refusal says which
line of the class keeps the decision.

<!-- command: transition -->
```jsonc
{"class": "Task", "id": "T-1", "transition": "approve"}
```

<!-- answer -->
```jsonc
{
  "error": {
    "code": "forbidden",
    "message": "ops-agent may not take approve from proposed",
    "hint": "the rule of approve: reviewer = USER() AND USERKIND() = \"person\"",
    "path": "/access/transition/approve"
  }
}
```

At 09:30 Marek approves the task.

**3. Ask what is yours to do.** One query, and `USER()` is you. Ask
again later when nothing is there.

<!-- command: query -->
```jsonc
{"class": "Task", "where": "state = \"approved\" AND owner = USER()", "fields": ["what"]}
```

<!-- answer -->
```jsonc
{
  "documents": [
    {
      "class": "Task",
      "id": "T-1",
      "title": "delete the logs older than 90 days",
      "state": "approved",
      "version": 2,
      "class_version": 1,
      "created_at": 1794819600,
      "updated_at": 1794821400,
      "state_entered_at": 1794821400,
      "created_by": "ops-agent",
      "updated_by": "marek",
      "state_entered_by": "marek",
      "state_entered_from": "proposed",
      "values": {
        "what": "delete the logs older than 90 days"
      }
    }
  ]
}
```

**4. Start, with a key.** The rule counts the attempt: `attempts` is
1, and you did not write it.

<!-- command: transition -->
```jsonc
{"class": "Task", "id": "T-1", "transition": "start", "key": "start-T-1-1"}
```

<!-- answer -->
```jsonc
{
  "document": {
    "class": "Task",
    "id": "T-1",
    "title": "delete the logs older than 90 days",
    "state": "running",
    "version": 3,
    "class_version": 1,
    "created_at": 1794819600,
    "updated_at": 1794823200,
    "state_entered_at": 1794823200,
    "created_by": "ops-agent",
    "updated_by": "ops-agent",
    "state_entered_by": "ops-agent",
    "state_entered_from": "approved",
    "values": {
      "attempts": "1",
      "owner": "ops-agent",
      "problem": null,
      "result": null,
      "reviewer": "marek",
      "what": "delete the logs older than 90 days"
    }
  }
}
```

At 10:00 you start deleting, and then your program stops. At 11:00
the engine takes `stall`: the task has run for an hour. At 12:00 your
program runs again and remembers nothing.

**5. After a restart, ask the documents.** What you were doing is in
their states.

<!-- command: query -->
```jsonc
{"class": "Task", "where": "owner = USER() AND state <> \"done\" AND state <> \"declined\"", "fields": ["what", "attempts"]}
```

<!-- answer -->
```jsonc
{
  "documents": [
    {
      "class": "Task",
      "id": "T-1",
      "title": "delete the logs older than 90 days",
      "state": "stalled",
      "version": 4,
      "class_version": 1,
      "created_at": 1794819600,
      "updated_at": 1794826800,
      "state_entered_at": 1794826800,
      "created_by": "ops-agent",
      "updated_by": "ops-agent",
      "state_entered_by": "ops-agent",
      "state_entered_from": "running",
      "values": {
        "attempts": "1",
        "what": "delete the logs older than 90 days"
      }
    }
  ]
}
```

The task is stalled, and `resume` is Marek's. At 12:00 he looks and
resumes it, and the rule counts a second attempt.

**6. Finish, with the result.** The guard of `finish` asks for one.

<!-- command: transition -->
```jsonc
{
  "class": "Task",
  "id": "T-1",
  "transition": "finish",
  "values": {"result": "412 files deleted, 3.1 GB freed"},
  "key": "finish-T-1"
}
```

<!-- answer -->
```jsonc
{
  "document": {
    "class": "Task",
    "id": "T-1",
    "title": "delete the logs older than 90 days",
    "state": "done",
    "version": 6,
    "class_version": 1,
    "created_at": 1794819600,
    "updated_at": 1794831000,
    "state_entered_at": 1794831000,
    "created_by": "ops-agent",
    "updated_by": "ops-agent",
    "state_entered_by": "ops-agent",
    "state_entered_from": "running",
    "values": {
      "attempts": "2",
      "owner": "ops-agent",
      "problem": null,
      "result": "412 files deleted, 3.1 GB freed",
      "reviewer": "marek",
      "what": "delete the logs older than 90 days"
    }
  }
}
```

The log of the task says who did each step, what they were, and how
the step came about.

<!-- the log of the task -->
| Event | Name | By | What they were | How |
|---|---|---|---|---|
| `created` | | ops-agent | agent | command |
| `transition` | `approve` | marek | person | command |
| `transition` | `start` | ops-agent | agent | command |
| `transition` | `stall` | ops-agent | agent | timer |
| `transition` | `resume` | marek | person | command |
| `transition` | `finish` | ops-agent | agent | command |

The move by the timer carries the id of whoever began the wait, which
is you, and the mark `timer`.

## What goes wrong most often

1. **Deciding your own proposal.** A class that takes your work
   seriously keeps the decision for a person: `USERKIND() = "person"`
   in the line of the transition. You are refused with `forbidden`,
   and that is the class working. Naming yourself as reviewer does not
   help.
2. **Getting round a rule with somebody's name.** You act as
   yourself. To act for a person you say so with `for`, and it passes
   only while that person gave you a substitution; without one it is
   refused with `forbidden` at `/principal/for`. Never ask the program
   to call you by a person's id.
3. **Proposing with no key.** If the answer is lost you cannot tell
   whether the task exists. With an id of your own a second `create`
   is refused with `duplicate`; with the same key it is answered as
   the first time.
4. **Taking the answer to a repeated command for the present.** A
   command repeated under its key is answered as it was the first
   time, however old that is. To know where a task stands, read it.
5. **Keeping what you do in your memory.** After a restart it is
   gone. The state of each task is in its document: ask with a query,
   as step 5 does.
6. **Counting your own attempts.** Let a rule of the class count
   them. You may not write `attempts`: a change of a task is refused
   with `forbidden`.
7. **A task with no timeout.** A program that stops leaves its task
   `running` for ever, unless the class has a transition with a time,
   as `stall` here. Design one into every state in which you work.
8. **Going on after a timeout as if nothing happened.** After `stall`
   your `finish` is refused with `wrong_state`, and the hint names
   what leaves the state. Read the task again; do not send the command
   again.
9. **Trying again for ever.** `retry` has a guard, `attempts < 3`.
   After the third attempt it is refused with `guard_false`, and the
   task stays `failed` for a person to see.
10. **Finishing without saying what was done.** `finish` asks for a
    result, and `fail` for the problem. What you write there is the
    record a person reads later.

## The hat

Here the agent is a principal in its own right. Its tasks, its proposals
and its sagas are documents of classes it designed, and the same engine
that refuses an unbalanced journal refuses the agent approving its own
proposal. What the agents did last night is a query; a restart resumes
from the state the documents hold.

## What works today

| Means | Mark |
|---|---|
| the agent as a principal with an id and groups, `As`; the log records it | shipped |
| the principal says it is an agent, and for whom it acts: `Kind: fado.ActorAgent`, `For: "ann"`; every event records both, and a class reads them with `USERKIND()` and `ACTINGFOR()` | shipped with Phase 5 |
| acting for a person needs a substitution (ADR-0053): the person creates a `Substitution` with `by` your id, for a time and for the classes they choose, and until then every call of yours that names them in `For` is refused with `forbidden` and the path `/principal/for`, a read too. The message says what is missing: none, one that begins later, one that has ended, one for other classes. Ask first with `Preview`; never work around it by dropping `For`, which makes the act your own. You cannot give yourself one, and you cannot pass one on. Under a host program that keeps its own substitutions (`HostAnswersFor`) the engine asks nothing | shipped with Phase 5 |
| a guard that keeps a transition from the agent's group; field rights per state | shipped with Phase 3 |
| what the agent may do is what the formulas of the class give it: a document its caller may not `read` does not exist for it, a refused change is `forbidden` with the place of the rule, and a rule a class leaves out lets nobody through. Ask first with `Preview` and `Transitions` | shipped with Phase 5 |
| an agent that is a content administrator, in the system group `content-administrators`, acts on any document past the rules of its class, never past a guard. Such an act is an override: pass `fado.Reason("...")` or get `reason_required`; the event keeps the rules lifted and the reason, `Event.Override` and `Event.Reason`. `Preview.Override` and `TransitionInfo.Override` say beforehand that an act would be one. Give the true reason: it is the record of why a rule was set aside | shipped with Phase 5 |
| idempotent retries, `Key`; the filter to poll for work | shipped |
| rules that call the agent's program back when a state is entered | shipped with Phase 4: a Go function the host program gives to `Open` in `Options.Actions`, with a definition stored by `DefineAction`, is called through the outbox with the event and the document; an agent in another process is called over HTTP by a definition with a `request` that points at its endpoint, signed, where the host program allows that host |
| the stream of changes instead of polling: one HTTP reply that stays open and says each event the agent may see (ADR-0058) | in progress, Phase 6: nothing is built |

## Procedure, in outline

1. **Be a principal.** Take an id and a group of your own, and make every
   call as that principal. The guards hold you like anyone else, and that
   is the point: the log says what the agent did. Say what you are: the
   kind `agent`, and under `For` the id of the person you work for when
   there is one. The log then tells your steps from a person's, and a
   class can give you the preparation and keep the decision for them.
   Do not borrow a person's id to get past a rule.
2. **The approval inbox.** One class: proposed, awaiting review, approved
   or declined, executed. The agent creates and submits; the approve
   transition's guard requires a human group and excludes the agent's;
   the fields of the proposal are read-only after submission. The agent
   polls for `state = "approved"`, acts, and moves the document to
   executed with the result.
3. **The task ledger.** One class: queued, running, needs a human, done
   or failed. Inputs are variables, attempts are rows with their moment
   and their error, hand-offs and timeouts are transitions. A restart
   queries for running tasks and resumes.
4. **The saga.** One document per operation across services: each step a
   state, each failure a compensating transition, the log the saga's
   history. Each step's call is an outbox action with retries: a Go
   function of the host program, or a request the engine sends.
5. **Retry with the key.** The same key makes the retry the same commit,
   so a lost answer is asked again without a duplicate.
6. **Be called back, or poll.** Where the agent's program embeds the
   engine, a rule on entering a state calls its Go function: the
   function receives the event and the document at that version, makes
   twice once by the call's key, and returns an error to be asked again
   later. An agent outside that process is called over HTTP: a
   definition with a `request` points at its endpoint, the request
   carries the call, signed, and the agent answers 2xx, or 5xx to be
   asked again. Polling with the filter still works where no endpoint
   can be reached.

## Worked examples

The task above is a whole class, and it is both the inbox, in which
you propose and a person decides, and the ledger, in which the
attempts are counted and a restart finds its place. Planned:
`../../examples/classes/agent-inbox.json`, the approval inbox the
front page of fadodb.org links to.

## Where the rest is

The reference this page once planned under `../reference/` is the
manual now: the chapters under "Read first" above. The decision
records behind them are listed in
[`../reference/README.md`](../reference/README.md).
