---
name: fadodb-simulate
description: Simulate a FadoDB process before it runs for real. Copy a class into a scratch database, replay a sequence of commits and transitions, inspect what the formulas, guards and eventless transitions do at chosen moments, compare two versions of a class, and keep only the one that worked. Use this skill whenever someone asks what would happen if, wants to test a draft class or a policy change against realistic data, rehearse a month-end, an approval chain, a rollout or a deadline, or check scheduled transitions ahead of time, whether or not they use the word simulate.
metadata:
  hat: simulator
  status: written
---

# Simulate a FadoDB process

**Status: written, 2026-11-14.** This page says how to answer "what
would happen if" without changing anything: what to read in the
manual, one piece of work command by command with the engine's own
answers, and what goes wrong most often. A test on each push defines
the class this page shows, sends its commands, compares the answers
with the ones below, and checks that nothing was written.

## Read first

| Read | For |
|---|---|
| [`docs/manual/commands.md`](../../docs/manual/commands.md) | the commands that write nothing: `preview`, `preview_draft`, `preview_many`, `preview_delete`, `transitions`, `due`, and the member `at` |
| [`docs/manual/notions/history-and-the-log.md`](../../docs/manual/notions/history-and-the-log.md#looking-before) | what a preview is, and what it answers |
| [`docs/manual/notions/time.md`](../../docs/manual/notions/time.md#looking-at-another-day) | looking at another day: what `at` changes, and what it does not |
| [`docs/manual/guides/change-a-class.md`](../../docs/manual/guides/change-a-class.md) | what a changed class does to the documents that exist |
| [`docs/manual/opening-a-database.md`](../../docs/manual/opening-a-database.md#the-clock) | for the program that runs you: a copy of the file with a clock of its own, when a scenario needs real changes |

## A piece of work, command by command

The class. An employee files a claim, and the approver it names
approves it, up to 5000.00, or rejects it with a note. A claim nobody
decides in fourteen days expires.

```json
{
  "name": "Claim",
  "title": "\"Claim of \" & employee",
  "variables": {
    "employee": {"type": "text", "default": "USER()"},
    "approver": {"type": "text", "required": true},
    "what":     {"type": "text", "required": true},
    "amount":   {"type": "money", "scale": 2, "required": true},
    "note":     {"type": "text"}
  },
  "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"]},
  "transitions": [
    {"name": "file",    "from": "draft", "to": "filed",    "guard": "AMOUNT(amount) > 0"},
    {"name": "approve", "from": "filed", "to": "approved", "guard": "AMOUNT(amount) <= 5000"},
    {"name": "reject",  "from": "filed", "to": "rejected", "guard": "NOT ISBLANK(note)"},
    {"name": "expire",  "from": "filed", "to": "expired",  "after": "14d"}
  ],
  "access": {
    "read":   "employee = USER() OR approver = USER()",
    "create": "INGROUP(\"everyone\")",
    "update": "employee = USER() AND STATE() = \"draft\"",
    "delete": "FALSE",
    "transition": {
      "file":    "employee = USER()",
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

The situation. On Monday 16 November 2026 at 09:00 Ola filed three
claims with the approver `claims-agent`: `C-1`, a taxi for 120.00 PLN;
`C-2`, a conference for 8000.00 PLN; `C-3`, a hotel for 300.00 PLN. It
is 10:00. The program that runs you tells the engine that you are
`claims-agent`, an agent, and a designer.

Two questions came in. Finance asks what would happen if an approver
could approve up to 10,000. Ola asks what becomes of her claims if
nobody looks at them this month.

**1. What does the rule do today?** A preview runs the whole command
and writes nothing.

<!-- command: preview -->
```jsonc
{"class": "Claim", "id": "C-2", "transition": "approve"}
```

<!-- answer -->
```jsonc
{
  "error": {
    "code": "guard_false",
    "message": "the guard of approve is not TRUE for the document in filed",
    "hint": "the guard: AMOUNT(amount) <= 5000"
  }
}
```

**2. What would the changed rule do?** `preview_draft` takes a whole
class that is not stored, and tries it on a document of the stored
class of the same name. Below, one line differs from the class above:
the guard of `approve` says 10000. Nothing is defined.

<!-- command: preview_draft -->
```jsonc
{
  "id": "C-2",
  "transition": "approve",
  "definition": {
    "name": "Claim",
    "title": "\"Claim of \" & employee",
    "variables": {
      "employee": {"type": "text", "default": "USER()"},
      "approver": {"type": "text", "required": true},
      "what":     {"type": "text", "required": true},
      "amount":   {"type": "money", "scale": 2, "required": true},
      "note":     {"type": "text"}
    },
    "states": {"initial": "draft", "list": ["draft", "filed", "approved", "rejected", "expired"], "final": ["approved", "rejected", "expired"]},
    "transitions": [
      {"name": "file",    "from": "draft", "to": "filed",    "guard": "AMOUNT(amount) > 0"},
      {"name": "approve", "from": "filed", "to": "approved", "guard": "AMOUNT(amount) <= 10000"},
      {"name": "reject",  "from": "filed", "to": "rejected", "guard": "NOT ISBLANK(note)"},
      {"name": "expire",  "from": "filed", "to": "expired",  "after": "14d"}
    ],
    "access": {
      "read":   "employee = USER() OR approver = USER()",
      "create": "INGROUP(\"everyone\")",
      "update": "employee = USER() AND STATE() = \"draft\"",
      "delete": "FALSE",
      "transition": {
        "file":    "employee = USER()",
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
}
```

<!-- answer -->
```jsonc
{
  "preview": {
    "document": {
      "class": "Claim",
      "id": "C-2",
      "title": "Claim of ola",
      "state": "approved",
      "version": 3,
      "class_version": 2,
      "created_at": 1794819600,
      "updated_at": 1794823200,
      "state_entered_at": 1794823200,
      "created_by": "ola",
      "updated_by": "claims-agent",
      "state_entered_by": "claims-agent",
      "state_entered_from": "filed",
      "values": {
        "amount": "8000.00 PLN",
        "approver": "claims-agent",
        "employee": "ola",
        "note": null,
        "what": "conference"
      }
    },
    "path": [
      "filed",
      "approved"
    ],
    "now": 1794823200
  }
}
```

**3. And for all of them at once?** `preview_many` answers for each
id: the document as it would be, or the refusal.

<!-- command: preview_many -->
```jsonc
{"class": "Claim", "transition": "approve", "ids": ["C-1", "C-2"]}
```

<!-- answer -->
```jsonc
{
  "outcomes": [
    {
      "id": "C-1",
      "document": {
        "class": "Claim",
        "id": "C-1",
        "title": "Claim of ola",
        "state": "approved",
        "version": 3,
        "class_version": 1,
        "created_at": 1794819600,
        "updated_at": 1794823200,
        "state_entered_at": 1794823200,
        "created_by": "ola",
        "updated_by": "claims-agent",
        "state_entered_by": "claims-agent",
        "state_entered_from": "filed",
        "values": {
          "amount": "120.00 PLN",
          "approver": "claims-agent",
          "employee": "ola",
          "note": null,
          "what": "taxi"
        }
      }
    },
    {
      "id": "C-2",
      "refusal": {
        "code": "guard_false",
        "message": "the guard of approve is not TRUE for the document in filed",
        "hint": "the guard: AMOUNT(amount) <= 5000"
      }
    }
  ]
}
```

**4. What will move by itself by the end of the month?** `due` lists
what a commit would move, and with `at` what it would move at that
moment. `since` is when the time of each comes: 09:00 on 30 November.

<!-- command: due -->
```jsonc
{"class": "Claim", "at": "2026-12-01"}
```

<!-- answer -->
```jsonc
{
  "due": [
    {
      "id": "C-1",
      "state": "filed",
      "to": "expired",
      "since": 1796029200
    },
    {
      "id": "C-2",
      "state": "filed",
      "to": "expired",
      "since": 1796029200
    },
    {
      "id": "C-3",
      "state": "filed",
      "to": "expired",
      "since": 1796029200
    }
  ]
}
```

**5. What would become of one of them?** A preview with no values and
no transition, at a moment, shows what the document would do by
itself by then.

<!-- command: preview -->
```jsonc
{"class": "Claim", "id": "C-1", "at": "2026-12-01"}
```

<!-- answer -->
```jsonc
{
  "preview": {
    "document": {
      "class": "Claim",
      "id": "C-1",
      "title": "Claim of ola",
      "state": "expired",
      "version": 3,
      "class_version": 1,
      "created_at": 1794819600,
      "updated_at": 1794823200,
      "state_entered_at": 1794823200,
      "created_by": "ola",
      "updated_by": "claims-agent",
      "state_entered_by": "claims-agent",
      "state_entered_from": "filed",
      "values": {
        "amount": "120.00 PLN",
        "approver": "claims-agent",
        "employee": "ola",
        "note": null,
        "what": "taxi"
      }
    },
    "path": [
      "filed",
      "expired"
    ],
    "now": 1796083200
  }
}
```

**6. Was anything changed?** No. Three claims are filed, as before.

<!-- command: count -->
```jsonc
{"class": "Claim", "where": "state = \"filed\""}
```

<!-- answer -->
```jsonc
{
  "count": 3
}
```

**The answers.** With the limit at 10,000 the conference could be
approved. Left alone, all three claims expire on 30 November at
09:00. Report both with the commands beside them, and say that
nothing was changed.

## What goes wrong most often

1. **Changing the real database to see what happens.** A preview
   writes nothing; `create`, `set` and `transition` do, and cannot be
   taken back. Ask with the commands of this page.
2. **Building one preview on another.** Each preview starts from the
   document as it is stored. A second preview does not see what the
   first one would have done. A scenario whose steps build on each
   other needs real changes, in a copy of the file that the program
   which runs you opens for it.
3. **Asking at a later moment and forgetting the timers.** A preview
   of a transition at a later moment answers for the document as it is
   stored now. It does not first take what will have moved by itself
   by then: on 1 December `approve` of `C-1` still previews as
   approved, though the claim expires on 30 November. Ask `due` for
   that moment first, as step 4 does.
4. **Changing something as of another moment.** A command that writes
   takes no moment: with `at` it is refused with `not_allowed`.
5. **Trying a draft without being a designer.** `preview_draft` reads
   through a class that nobody has reviewed, so it is for the system
   group `designers`. Anybody else is refused with `forbidden`.
6. **A draft under another name.** A draft is tried on a document of
   the stored class with the same name. Under another name the
   document is not found: `unknown_class`. A draft under a new name
   can only be tried on a create.
7. **A draft the engine refuses.** A draft is checked as a class is.
   One with a mistake is answered with `bad_definition`, and the
   findings say where.
8. **Reading no error as "all would move".** `preview_many` gives no
   error when a document would not move. Read the outcome of each id.
9. **Expecting `due` to list what waits for a person.** `due` lists
   what a commit would move: a transition whose time has come, a guard
   on time that holds. A claim that waits to be approved is not due.
10. **Asking what somebody else may do.** A command runs as the
    caller the program names, and a preview answers for that caller.
    "Would Ola be allowed?" is asked as Ola.

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
| a batch of moves tried without writing: `PreviewMany` answers what `TransitionMany` would, document by document, now or with `At(t)`; a guard that counts other documents sees the moves made earlier in the same batch | shipped in v0.4.1 (ADR-0048) |
| a preview at a chosen moment: `At(t)` on `Preview`, `PreviewDraft`, `Transitions`, `Query`, `Get` and `History` | shipped |
| a caller that acts for somebody, at a chosen moment: a preview or a read with `At(t)` is asked the substitution as of `t`, so a substitution planned for next week can be tried today (ADR-0053). At a past moment it is asked of the substitutions that stand now | shipped with Phase 5 |
| what will be due at a chosen moment: `Due` with `At(t)` lists the documents a commit would move then, by a guard on time or by a timed transition | shipped with Phase 4 |
| what the timers of one document would do by a chosen moment: `Preview` of nothing with `At(t)` shows the chain a commit at that moment would fire, a reminder once, and `Transitions` the moment each timer fires | shipped with Phase 4 |
| a commit at a chosen moment | not designed: a commit runs at the database's clock |
| timers that fire by themselves in a scratch database, under a clock the simulation moves: open the copy with `Options.Now` and `Outbox.Manual`, move the clock, and `Drain` runs every timer that has come, as the worker would | shipped with Phase 4 |
| a rule or a variable on a schedule | designed: it recomputes many documents at once and waits for the analytical engine, Phase 8 |
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

## Where the rest is

The reference this page once planned under `../reference/` is the
manual now: the chapters under "Read first" above. The decision
records behind them are listed in
[`../reference/README.md`](../reference/README.md).
