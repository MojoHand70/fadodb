# See where documents wait

A process has places where documents pile up. One command answers the
whole picture of a class: how many documents are in each state now, how
long they stay there, and how many moved along each transition. It is
what a chart of a process draws, and what an agent reads before it
says where the delay is.

## The class

```json
{
  "name": "Invoice",
  "variables": {
    "owner": {"type": "text", "default": "USER()"},
    "total": {"type": "decimal", "scale": 2}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "approved", "cancelled"], "final": ["approved", "cancelled"]},
  "transitions": [
    {"name": "submit",  "from": "draft",  "to": "review"},
    {"from": "review", "to": "cancelled", "guard": "total = 0"},
    {"name": "approve", "from": "review", "to": "approved"},
    {"name": "cancel",  "from": ["draft", "review"], "to": "cancelled"}
  ],
  "access": {
    "read":   "owner = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "INGROUP(\"sales\")",
    "transition": {"approve": "INGROUP(\"finance\")"}
  }
}
```

Somebody in sales reads their own invoices, and finance reads every
one. The second transition has no name: the engine takes it by itself
when an invoice of nothing enters review.

## What happened

Bob and Dan are in sales, Ann is in finance. All times are in UTC.

<!-- what happened -->
| When | Who | Does |
|---|---|---|
| Monday 2 November, 9:00 | Bob | files B-1, B-2 and B-3, each of 100.00 |
| Monday, 9:00 | Dan | files D-1, of 0.00 |
| Monday, 10:00 | Bob | submits B-1 and B-2 |
| Monday, 10:00 | Dan | submits D-1, and the engine cancels it at once |
| Monday, 13:00 | Ann | approves B-1 |
| Tuesday, 13:00 | Bob | cancels B-3 |

## The command and its answer

On Tuesday afternoon Ann asks for the flow of the class. The command is
`flow`, and its object names the class:

```jsonc
{"class": "Invoice"}
```

<!-- the flow Ann sees -->
```jsonc
{
  "flow": {
    "class": "Invoice",
    "class_version": 1,
    "created": 4,
    "states": [
      {
        "name": "draft",
        "initial": true,
        "documents": 0,
        "stays": 4,
        "average_seconds": 27900,
        "longest_seconds": 100800
      },
      {
        "name": "review",
        "documents": 1,
        "oldest_since": 1793613600,
        "stays": 2,
        "average_seconds": 5400,
        "longest_seconds": 10800
      },
      {
        "name": "approved",
        "final": true,
        "documents": 1,
        "oldest_since": 1793624400,
        "stays": 0,
        "average_seconds": 0,
        "longest_seconds": 0
      },
      {
        "name": "cancelled",
        "final": true,
        "documents": 2,
        "oldest_since": 1793613600,
        "stays": 0,
        "average_seconds": 0,
        "longest_seconds": 0
      }
    ],
    "transitions": [
      {
        "name": "submit",
        "from": "draft",
        "to": "review",
        "moves": 3
      },
      {
        "from": "review",
        "to": "cancelled",
        "automatic": true,
        "moves": 1
      },
      {
        "name": "approve",
        "from": "review",
        "to": "approved",
        "moves": 1
      },
      {
        "name": "cancel",
        "from": "draft",
        "to": "cancelled",
        "moves": 1
      },
      {
        "name": "cancel",
        "from": "review",
        "to": "cancelled",
        "moves": 0
      }
    ]
  }
}
```

## What a number says

<!-- what a number says -->
| Member | Of | Says |
|---|---|---|
| `created` | the flow | how many documents were created in the period |
| `documents` | a state | how many documents are in the state now |
| `oldest_since` | a state | the moment the document that is there longest entered it, in seconds; left out when the state is empty |
| `stays` | a state | how many stays in the state ended in the period: a document that left it and came back is counted twice |
| `average_seconds`, `longest_seconds` | a state | how long those stays lasted |
| `moves` | a transition | how many times a document moved along it in the period |
| `initial`, `final` | a state | the state a document begins in; a state it does not leave |
| `automatic` | a transition | it has no name: the engine takes it when its guard holds |
| `timed` | a transition | it fires by time, with `after` or `at` |
| `retired` | a state, a transition | the class no longer has it, and documents left it or moved along it in the period |

A transition that leaves several states is listed once for each, as
`cancel` is above.

So Ann reads where invoices wait:

- **In draft.** Four stays ended, and they lasted 7 hours and 45
  minutes on average, 28 hours the longest: B-3, which Bob cancelled a
  day after he filed it.
- **In review.** One invoice is there now, B-2, since Monday at 10:00.
  The two stays that ended lasted an hour and a half on average: three
  hours for B-1, and no time at all for D-1.

## Each caller sees their own

The flow counts the documents its caller may read, by the read rule of
the class, as `query` and `count` do. Dan's invoice is in no number of
Bob's, neither where it is nor how it moved, and Eve, who is in no
group, reads the chart and no document.

<!-- who sees what -->
| Who asks | `created` | In `review` now | In `cancelled` now | Moves along `submit` |
|---|---|---|---|---|
| Ann | 4 | 1 | 2 | 3 |
| Bob | 3 | 1 | 1 | 2 |
| Dan | 1 | 0 | 1 | 1 |
| Eve | 0 | 0 | 0 | 0 |

## A period

`since` and `until` say the period the moves and the stays are counted
in: from the first moment, and before the last. Each is a day, a day
and a time in UTC, or seconds. Left out, the period is since the class
began. Where the documents are is always now.

<!-- a period -->
| The object, as Ann sends it | `created` | Moves along `submit` | Moves along `cancel` from `draft` | In `review` now |
|---|---|---|---|---|
| `{"class": "Invoice"}` | 4 | 3 | 1 | 1 |
| `{"class": "Invoice", "until": "2026-11-03"}` | 4 | 3 | 0 | 1 |
| `{"class": "Invoice", "since": "2026-11-03"}` | 0 | 0 | 1 | 1 |
| `{"class": "Invoice", "since": "2026-11-02 10:00:00", "until": "2026-11-02 13:00:00"}` | 0 | 3 | 0 | 1 |

## Asking

In a Go program:

```go
flow, err := db.Flow(ctx, "Invoice", fado.FlowPeriod{}, fado.As(ann))
```

As a command, over HTTP or through `Exec`, it is `flow`
([the commands](../commands.md#flow)). From a terminal:

```sh
fadodb exec flow '{"class": "Invoice", "since": "2026-11-03"}'
```

## What it does not count

- **A deleted document** is in no number, neither where it was nor how
  it moved.
- **A document the caller may not read.** Somebody who must see every
  document of a class is given a read rule that says so, or asks as an
  analyst in SQL ([Views and SQL](../views-and-sql.md)), which reads
  everything and answers any question about the stays.

## Not here yet

- **The picture at a moment in the past.** The documents in each state
  are counted now.
- **The median of the stays.** One invoice that waited a year moves an
  average far; the longest stay is there to show it.
