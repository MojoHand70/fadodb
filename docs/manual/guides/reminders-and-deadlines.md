# A reminder every seven days, and a deadline

A request waits for an answer. Every seven days without one it is
reminded, and on its deadline it expires by itself. Nobody has to
press anything.

## The class

```json
{
  "name": "Request",
  "title": "subject",
  "variables": {
    "subject":   {"type": "text", "required": true},
    "deadline":  {"type": "date", "required": true},
    "reminders": {"type": "number", "default": "0"}
  },
  "states": {"initial": "waiting", "list": ["waiting", "answered", "expired"], "final": ["answered", "expired"]},
  "transitions": [
    {"name": "answer", "from": "waiting", "to": "answered"},
    {"name": "remind", "from": "waiting", "to": "waiting", "after": "7d"},
    {"name": "expire", "from": "waiting", "to": "expired", "at": "deadline"}
  ],
  "rules": [
    {"on": "transition", "name": "remind", "do": [{"action": "set", "values": {"reminders": "reminders + 1"}}]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE",
    "transition": {"answer": "INGROUP(\"everyone\")", "remind": "FALSE", "expire": "FALSE"}
  }
}
```

Two transitions carry a time:

- `remind` has `"after": "7d"`: seven days after the request entered
  `waiting`. It leads back into `waiting`, which counts as a new
  entry, so it comes again seven days later.
- `expire` has `"at": "deadline"`: at the date the request holds.

Both have `FALSE` under `access.transition`, so nobody can take them
by hand. The engine takes them when their time comes.

## What it does

Ann files a request on 1 December with a deadline of 20 December.

| On | What happens | The request |
|---|---|---|
| 1 December | Ann creates it | `waiting`, 0 reminders |
| 8 December | the engine takes `remind` | `waiting`, 1 reminder |
| 15 December | the engine takes `remind` again | `waiting`, 2 reminders |
| 20 December | the engine takes `expire` | `expired` |

Had somebody answered in between, the request would be `answered`, a
final state, and neither timer would come.

## How it works

- **A timed transition is a transition like any other.** It has its
  version and its event in the log, marked as made by a timer.
- **The rule on `remind` is what the reminder does.** Here it counts.
  It could as well call another system to send a message; see
  [Actions](../../actions.md).
- **The engine acts as the person whose change started the wait**:
  here Ann, who created the request. The log shows her name with the
  mark of the timer.
- **A date means the first moment of that day**, in UTC. Use a
  `datetime` variable for an exact moment.
- **It needs the engine's worker**, which runs inside every program
  that opens the database, unless the program switched it off. A timer
  that was missed while no program was running is made as soon as one
  runs again, once.

## Variations

| To | Write |
|---|---|
| expire thirty days after the request was first filed, however often it was reminded | `"at": "ADDDAYS(FIRSTENTERED(\"waiting\"), 30)"` |
| remind after hours or minutes | `"after": "12h"`, `"after": "90m"` |
| let a person also take the transition before its time | give its line under `access.transition` a formula other than `FALSE` |
| see when a timer will come | ask for the transitions of the document; each timed one says when it fires |

See also: [The class definition, Transitions](../class.md#transitions).
