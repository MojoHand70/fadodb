# Time

A process waits: for an answer, for a deadline, for seven days to
pass. This chapter says how a class speaks of time, how the engine
moves a document when its time comes, and how to look at a database
as it will be on another day.

## Three ways a class speaks of time

| A class can | Written as | Where |
|---|---|---|
| read the clock | `TODAY()`, `NOW()` | in a default, a guard, a line of `access`, a rule |
| read the life of the document | `ENTEREDAT()`, `DAYSIN("sent")`, `FIRSTENTERED("sent")` and the others | the same places |
| give a transition its time | `after`, `at` | on the transition |

A computed variable reads neither the clock nor the life of the
document. Its value must stay true until an input changes, and a value
that reads the clock would be out of date a moment later; see
[Variables and formulas](variables-and-formulas.md).

## The class

An offer to a customer. It is valid for thirty days, the customer is
reminded every seven, and the day after its last day it expires.

```json
{
  "name": "Offer",
  "title": "\"Offer for \" & customer",
  "variables": {
    "customer":    {"type": "text", "required": true},
    "valid_until": {"type": "date", "default": "ADDDAYS(TODAY(), 30)"},
    "reminders":   {"type": "int", "default": "0"}
  },
  "states": {"initial": "sent", "list": ["sent", "accepted", "expired"], "final": ["accepted", "expired"]},
  "transitions": [
    {"name": "accept", "from": "sent", "to": "accepted", "guard": "TODAY() <= valid_until"},
    {"name": "remind", "from": "sent", "to": "sent",     "after": "7d"},
    {"name": "expire", "from": "sent", "to": "expired",  "at": "ADDDAYS(valid_until, 1)"}
  ],
  "rules": [
    {"on": "transition", "name": "remind", "do": [{"action": "set", "values": {"reminders": "reminders + 1"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE",
    "transition": {"accept": "INGROUP(\"everyone\")", "remind": "FALSE", "expire": "FALSE"}
  }
}
```

Time is in it three times: the default of `valid_until` reads the
day, the guard of `accept` reads the day, and `remind` and `expire`
carry a time of their own.

## What it does

Krzysztof sends the offer on 1 December 2026 at 09:00 UTC. Ewa is the
customer.

| On | What happens | The offer |
|---|---|---|
| 1 December | Krzysztof creates it | `sent`, valid until 31 December, 0 reminders |
| 8 December | the engine takes `remind` | `sent`, 1 reminder |
| 15 December | no program is running | unchanged |
| 20 December | a program opens the database; the engine takes the `remind` it missed, once | `sent`, 2 reminders |
| 20 December | Krzysztof shortens it: valid until 22 December | `sent`; `expire` now comes on 23 December |
| 23 December | Ewa accepts | refused, `guard_false`: its last day has passed |
| 23 December | the engine takes `expire` | `expired` |

## A transition with a time

- **`after` is a length of time in the state**: a whole number and
  `d`, `h`, `m` or `s`, as `"7d"` or `"36h"`. It counts from the
  moment the document entered the state. `remind` leads back into
  `sent`, which is a new entry, so it comes again seven days later.
- **`at` is a moment the document holds**: a formula over its own
  values that gives a date or a datetime. A date means the first
  moment of that day in UTC, so `expire` comes at midnight after the
  last valid day. When a value it reads changes, the moment moves with
  it, as on 20 December.
- **The engine takes it when its time comes**, as a commit like any
  other: a version, an event, the rules of the transition. A guard
  beside the time is still asked.
- **It acts as the person whose commit began the wait.** The log says
  Krzysztof, with the mark that a timer made the move.
- **Nobody is asked whether they may.** The move is the document's
  own. `FALSE` under `access.transition` only keeps people from taking
  it by hand before its time. With another formula there, a person may
  take it early.

<!-- the log -->
| Event | Name | From | To | By | How |
|---|---|---|---|---|---|
| `created` | | | `sent` | krzysztof | `command` |
| `transition` | `remind` | `sent` | `sent` | krzysztof | `timer` |
| `transition` | `remind` | `sent` | `sent` | krzysztof | `timer` |
| `set` | | | | krzysztof | |
| `transition` | `expire` | `sent` | `expired` | krzysztof | `timer` |

## Who makes the move

The engine's worker runs inside every program that opens the database,
unless the program switched it off. When a document enters a state,
the commit writes down when each timed transition that leaves it is
due, and the worker wakes at that moment.

- **A timer that was missed is made late, once.** No program ran on
  15 December. On 20 December the reminder of the 15th is made, one
  time, and the next one is due seven days after that.
- **Nothing is lost while no program runs.** What is due is written in
  the database, not kept in memory.
- **A timer whose document has moved on does nothing.** If Ewa accepts
  on 5 December, the offer is final and neither timer comes.

## When the next one comes

The list of the open transitions of a document says for each timed
one when it fires. For the offer on 1 December:

<!-- what is open on 1 December -->
| Transition | Leads to | For Ewa | Fires |
|---|---|---|---|
| `accept` | `accepted` | yes | |
| `remind` | `sent` | no: `forbidden` | 8 December 2026, 09:00 |
| `expire` | `expired` | no: `forbidden` | 1 January 2027, 00:00 |

## Looking at another day

Every call that reads or previews can be asked **as of a moment**.
`NOW()` and `TODAY()` are then that moment in every formula the call
works out, and nothing is written.

<!-- as of a moment -->
| Asked on 1 December | As of | Answer |
|---|---|---|
| which offers would move by themselves? | now | none |
| which offers would move by themselves? | 10 December | this one: a reminder has been due since 8 December |
| may Ewa accept? | 20 December | yes |
| may Ewa accept? | 2 January | no: `guard_false` |

This is how a screen shows "expires in nine days", how an agent plans
its week, and how a designer tries what a class will do next month.

A change is never made as of another moment. A commit runs at the
clock of the database, and a call that tries is refused with
`not_allowed`. To rehearse a month, open a copy of the file with a
clock of your own.

## A guard that reads the clock

The guard of `accept` reads `TODAY()`. It is asked when somebody takes
the transition, so it is always right at that moment. Nothing happens
by itself when the day passes: on 1 January nobody is told that the
offer can no longer be accepted. What must happen by itself is a
transition with a time, here `expire`.

## What a day is

A day is 24 hours of UTC, and a date is a calendar day in UTC. The
engine knows no time zones and no working days today. A deadline in
local time is a `datetime` that your program works out.

## What is checked when the class is defined

<!-- checked when the class is defined -->
| The transition | Code |
|---|---|
| has both `after` and `at` | `not_allowed` |
| has a time and leaves a final state | `not_allowed` |
| has an `at` and leads into its own state | `not_allowed` |
| has an `at` that reads the clock or the caller | `not_allowed` |
| has an `at` that is not a date or a datetime | `type_mismatch` |
| has an `after` that is not a length of time | `bad_value` |

## Not here yet

- A rule that runs by time without moving the document. Today a
  reminder is a transition back into the same state, as above.
- Working days and time zones.
- A move as soon as a condition holds, with no time involved, under a
  name. The owner ruled on 2026-11-13 that every transition has a
  name and an automatic one carries a mark; that is not built yet.

See also: the guide [A reminder every seven days, and a
deadline](../guides/reminders-and-deadlines.md).
