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
| `spread` | a state | how those stays spread, when the command asks for it; see [How the stays spread](#how-the-stays-spread) |
| `middle_seconds`, `slow_tenth_seconds` | the spread of a state | the middle stay: half of the stays lasted no longer. The slow tenth: nine stays in ten lasted no longer |
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

## How the stays spread

An average is moved by one document that lay forgotten. Three invoices
left `draft` after an hour and B-3 after 28: the average, 7 hours and
45 minutes, is a time no invoice took. Asked with `"spread": true`,
the flow says of each state the middle stay and the slow tenth as
well:

```jsonc
{"class": "Invoice", "spread": true}
```

<!-- the draft state with its spread -->
```jsonc
{
  "name": "draft",
  "initial": true,
  "documents": 0,
  "stays": 4,
  "average_seconds": 27900,
  "longest_seconds": 100800,
  "spread": {
    "middle_seconds": 3600,
    "slow_tenth_seconds": 100800
  }
}
```

<!-- how the stays spread -->
| Who asks | State | Stays | The average | The middle stay | The slow tenth |
|---|---|---|---|---|---|
| Ann | `draft` | 4 | 27900 | 3600 | 100800 |
| Ann | `review` | 2 | 5400 | 0 | 10800 |
| Bob | `draft` | 3 | 36000 | 3600 | 100800 |
| Bob | `review` | 1 | 10800 | 10800 | 10800 |

- **The middle stay says what usually happens**: half of the stays
  lasted no longer. An invoice usually leaves `draft` within the hour.
- **The slow tenth says how long the slowest wait**: nine stays in ten
  lasted no longer. It is where a delay shows that the middle hides.
- **Each is the length of one of the stays.** The stays that ended in
  the period are put in the order of their lengths. Of `n` stays the
  middle one stands at place `(n+1)/2` and the slow tenth at place
  `(9n+9)/10`, both rounded down: of Ann's four stays in `draft`, the
  second and the fourth.
- **A state no stay ended in** says zero for both.
- **It is asked for, and not given unasked**, since it reads every
  stay of the period and puts them in order. Among ten million moves,
  a program with the analytical engine answers the flow in 12 ms and
  the flow with its spread in 80 to 87 ms; a program without it takes
  18 seconds and 40.

## Whether the pile grows

The flow says where documents are and how they moved in a period as a
whole. Another command, `throughput`, says how many were made and how
many finished in each day, week or month of it:

```jsonc
{"class": "Invoice", "by": "day"}
```

<!-- the throughput Ann sees -->
```jsonc
{
  "throughput": {
    "class": "Invoice",
    "class_version": 1,
    "by": "day",
    "time_zone": "UTC",
    "created": 4,
    "finished": 3,
    "periods": [
      {
        "from": "2026-11-02",
        "created": 4,
        "finished": 2
      },
      {
        "from": "2026-11-03",
        "created": 0,
        "finished": 1
      }
    ]
  }
}
```

On Monday four invoices were made and two finished: D-1, which the
engine cancelled, and B-1, which Ann approved. On Tuesday Bob
cancelled B-3. One is still open, B-2.

<!-- what a throughput says -->
| Member | Of | Says |
|---|---|---|
| `by` | the throughput | the length of a period: `day`, `week` or `month` |
| `time_zone` | the throughput | the time zone by whose clock a day begins |
| `created`, `finished` | the throughput | the sums over the periods |
| `periods` | the throughput | the periods, in their order |
| `from` | a period | its first day |
| `created` | a period | how many documents were made in it |
| `finished` | a period | how many documents came into a final state in it |

<!-- made and finished -->
| Who asks | The object | The periods: first day, made / finished |
|---|---|---|
| Ann | `{"class": "Invoice"}` | 2026-11-02 4/2, 2026-11-03 0/1 |
| Bob | `{"class": "Invoice"}` | 2026-11-02 3/1, 2026-11-03 0/1 |
| Ann | `{"class": "Invoice", "by": "week"}` | 2026-11-02 4/3 |
| Ann | `{"class": "Invoice", "by": "month"}` | 2026-11-01 4/3 |
| Ann | `{"class": "Invoice", "since": "2026-11-03"}` | 2026-11-03 0/1 |

- **A document is made when it is created, and finished when it comes
  into a final state**: `approved` or `cancelled` here.
- **A day begins at midnight by the clock of the database's time
  zone**, which the server's settings give as `timezone`, and which is
  UTC when none was given. A week begins on its Monday and a month on
  its first. `by` is `day` when left out.
- **Every period from the first to the last is there**, also one in
  which nothing was made and nothing finished: a chart of them has no
  gaps. Before the first and after the last there is none.
- **`since` and `until` say what is counted**, as for the flow.
- **Each caller sees their own**, as in the flow, and a deleted
  document is in no number.
- **Made more than finished, period after period, is a pile that
  grows.** What was made less what finished, added up from the first
  period, is how many are open.
- **More than 10,000 periods are refused**, with `too_many_rows`: ask
  by week or by month, or over a shorter time.
- **Among ten million moves** a program with the analytical engine
  answers it in 6 to 7 ms, and a program without it in 9 seconds.

## Which way documents went

A third command, `paths`, says which ways the documents went through
the states, the most taken first:

```jsonc
{"class": "Invoice"}
```

<!-- the paths Ann sees -->
```jsonc
{
  "paths": {
    "class": "Invoice",
    "class_version": 1,
    "documents": 4,
    "different": 4,
    "paths": [
      {
        "states": [
          "draft",
          "cancelled"
        ],
        "documents": 1,
        "finished": true
      },
      {
        "states": [
          "draft",
          "review"
        ],
        "documents": 1
      },
      {
        "states": [
          "draft",
          "review",
          "approved"
        ],
        "documents": 1,
        "finished": true
      },
      {
        "states": [
          "draft",
          "review",
          "cancelled"
        ],
        "documents": 1,
        "finished": true
      }
    ],
    "others": 0
  }
}
```

Four invoices, and each went its own way. B-2 is still in review: its
path is not finished, and is its path so far.

<!-- what the paths say -->
| Member | Of | Says |
|---|---|---|
| `documents` | the paths | how many documents were counted |
| `different` | the paths | how many different paths they took |
| `paths` | the paths | the most taken of them, at most as many as `limit` says |
| `others` | the paths | how many documents took a path that is not listed |
| `states` | a path | the states in the order a document came into them: the first is the state it was made in, and a state stands there as often as it was entered |
| `documents` | a path | how many documents took the path |
| `finished` | a path | the path ends in a final state; left out for the path so far of documents that are still open |

<!-- the paths each sees -->
| Who asks | The object | Documents | Others | The paths |
|---|---|---|---|---|
| Ann | `{"class": "Invoice"}` | 4 | 0 | draft, cancelled: 1; draft, review: 1; draft, review, approved: 1; draft, review, cancelled: 1 |
| Bob | `{"class": "Invoice"}` | 3 | 0 | draft, cancelled: 1; draft, review: 1; draft, review, approved: 1 |
| Ann | `{"class": "Invoice", "limit": 1}` | 4 | 3 | draft, cancelled: 1 |
| Ann | `{"class": "Invoice", "since": "2026-11-03"}` | 0 | 0 | |

- **The documents counted are those made in the period**, from
  `since` and before `until`, that the caller may read. Each is
  counted with its whole path, up to now: also the moves it made
  after the period.
- **A document that is sent back and comes again has a longer
  path**: draft, review, draft, review, approved.
- **D-1 went draft, review, cancelled**, though the engine cancelled
  it in the same moment it was submitted: the moves of one moment are
  told apart.
- **The most taken path comes first**, and paths taken as often stand
  in the order of their states.
- **`limit` says how many paths are listed**, 20 when left out and
  1,000 at the most; `others` says how many documents took one of the
  rest.
- **A deleted document is in no path.**
- **Among 3,400,000 invoices that made ten million moves** a program
  with the analytical engine answers the paths in a quarter of a
  second, and a program without it in 9 seconds.

## Asking

In a Go program:

```go
flow, err := db.Flow(ctx, "Invoice", fado.FlowPeriod{}, fado.As(ann))
```

`fado.FlowPeriod{Spread: true}` asks for how the stays spread.

```go
weeks, err := db.Throughput(ctx, "Invoice", fado.ThroughputAsk{By: fado.ThroughputWeek}, fado.As(ann))
paths, err := db.Paths(ctx, "Invoice", fado.PathsAsk{Limit: 10}, fado.As(ann))
```

As commands, over HTTP or through `Exec`, they are `flow`,
`throughput` and `paths` ([the commands](../commands.md#flow)). From a
terminal:

```sh
fadodb exec flow '{"class": "Invoice", "since": "2026-11-03"}'
fadodb exec throughput '{"class": "Invoice", "by": "week"}'
fadodb exec paths '{"class": "Invoice", "limit": 10}'
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
- **How long each path took**, and the paths told apart by the
  transitions and not by the states alone.
