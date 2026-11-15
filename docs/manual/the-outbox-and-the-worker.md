# The outbox and the worker

Some things a rule does reach outside its document: it creates another
document, writes a row of a table, sends a mail, calls another system.
The engine does none of them inside the commit. It writes them down in
the same write as the commit, in a queue called the **outbox**, and a
**worker** runs them afterwards. This chapter is for the developer who
runs a database: what a row of the queue holds, how the worker runs
it, what happens when an action fails, and what to do with one that is
dead. A test runs the program of this chapter and plays every table of
it on each push.

The designer's view is in [Rules and actions](notions/rules-and-actions.md),
and how an action is defined in [Actions](../actions.md).

## The program

An order is sent, and two actions follow it: a mail, and an entry in
the books. The mail server gives no answer twice. The books refuse,
until an administrator sends the action again. The program runs the
queue by hand under a clock of its own, so that every step shows. It
is `examples/outbox`; run it with `go run ./examples/outbox`.

<!-- the program -->
```go
// Command outbox is the program of the manual's chapter "The outbox and
// the worker" (docs/manual/the-outbox-and-the-worker.md): an order is
// sent, and two actions follow it. The mail server gives no answer
// twice, and the books refuse until an administrator sends the action
// again. The program runs the queue by hand, under a clock of its own,
// and prints the queue after each step.
//
//	go run ./examples/outbox
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The two actions, each the contract of a Go function of this program.
const (
	mailSend  = `{"name": "mail.send", "params": {"to": "text"}}`
	booksPost = `{"name": "books.post", "params": {"amount": "number"}, "returns": {"entry": "text"}}`
)

// An order. Sending it queues a mail and an entry in the books, and the
// number of the entry is written back into the order.
const order = `{
  "name": "Order",
  "variables": {
    "customer": {"type": "text", "required": true},
    "amount":   {"type": "number", "required": true},
    "entry":    {"type": "text"}
  },
  "states": {"initial": "new", "list": ["new", "sent"]},
  "transitions": [
    {"name": "send", "from": "new", "to": "sent"}
  ],
  "rules": [
    {"on": "transition", "name": "send", "do": [
      {"action": "mail.send",  "args": {"to": "customer"}},
      {"action": "books.post", "args": {"amount": "amount"}, "into": {"entry": "entry"}}
    ]}
  ],
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE"
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-outbox")
	if err == nil {
		defer os.RemoveAll(dir)
		err = run(os.Stdout, dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run plays the story of the chapter in a database under dir.
func run(w io.Writer, dir string) error {
	ctx := context.Background()
	clock := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

	// The world outside: a mail server that gives no answer twice, and
	// books whose period is closed.
	mails, periodOpen := 0, false
	actions := map[string]fado.ActionFunc{
		"mail.send": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
			if mails++; mails < 3 {
				// An error is no answer: the engine asks again later.
				return nil, errors.New("the mail server gives no answer")
			}
			return nil, nil
		},
		"books.post": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
			if !periodOpen {
				// A permanent error is a refusal: asking again would not help.
				return nil, fado.Permanent(errors.New("the period is closed"))
			}
			return fado.Values{"entry": "E-2026-0117"}, nil
		},
	}

	// Manual keeps the worker from starting, so that the program runs
	// the queue itself, with Drain. A program in service leaves it out.
	db, err := fado.Open(filepath.Join(dir, "orders.fado"), fado.Options{
		Actions: actions,
		Now:     func() time.Time { return clock },
		Outbox:  fado.OutboxOptions{Manual: true, MaxAttempts: 5, Backoff: time.Minute},
	})
	if err != nil {
		return err
	}
	defer db.Close()

	dan := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	adam := fado.As(fado.Principal{ID: "adam", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}})
	ola := fado.As(fado.Principal{ID: "ola", Kind: fado.ActorPerson})

	for _, definition := range []string{mailSend, booksPost} {
		if _, err := db.DefineAction(ctx, []byte(definition), dan); err != nil {
			return err
		}
	}
	if _, err := db.DefineClass(ctx, []byte(order), dan); err != nil {
		return err
	}
	made, err := db.Create(ctx, "Order", fado.Values{"customer": "ewa@example.com", "amount": 1200}, ola)
	if err != nil {
		return err
	}

	// queue prints the rows of the queue, as an administrator sees them.
	queue := func() error {
		rows, err := db.Outbox(ctx, fado.OutboxFilter{DocID: made.ID}, adam)
		for _, row := range rows {
			line := fmt.Sprintf("         %-11s %-8s attempts %d", row.Action, row.Status, row.Attempts)
			if row.Status == "pending" {
				line += ", due " + time.Unix(row.DueAt, 0).UTC().Format("15:04")
			}
			if row.ErrorMessage != "" {
				line += ", " + row.ErrorCode + ": " + row.ErrorMessage
			}
			fmt.Fprintln(w, line)
		}
		return err
	}
	// drain runs what is due at the moment of the clock.
	drain := func() error {
		ran, err := db.Drain(ctx, adam)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s  the queue runs: %d\n", clock.Format("15:04"), ran)
		return queue()
	}

	if _, err := db.Transition(ctx, "Order", made.ID, "send", nil, ola); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s  ola sends the order\n", clock.Format("15:04"))
	if err := queue(); err != nil {
		return err
	}
	// The mail gets no answer at 09:00 and at 09:01, and goes out at
	// 09:03. The entry in the books waits for it, and is then refused.
	for _, wait := range []time.Duration{0, time.Minute, 2 * time.Minute} {
		clock = clock.Add(wait)
		if err := drain(); err != nil {
			return err
		}
	}

	// An administrator finds the dead action, has the period opened, and
	// sends the action again.
	dead, err := db.Outbox(ctx, fado.OutboxFilter{Status: "dead"}, adam)
	if err != nil {
		return err
	}
	periodOpen = true
	for _, row := range dead {
		if err := db.RetryAction(ctx, row.Key, adam); err != nil {
			return err
		}
		fmt.Fprintf(w, "       adam sends %s again\n", row.Action)
	}
	if err := drain(); err != nil {
		return err
	}

	after, err := db.Get(ctx, "Order", made.ID, ola)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "the order has the entry", after.Values["entry"])
	events, err := db.Events(ctx, "Order", made.ID, ola)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "the log:")
	for _, event := range events {
		line := "  " + event.Kind
		if event.Name != "" {
			line += " " + event.Name
		}
		if event.Code != "" {
			line += ", " + event.Code
		}
		fmt.Fprintln(w, line)
	}
	return nil
}
```

It prints:

<!-- output of examples/outbox -->
```text
09:00  ola sends the order
         mail.send   pending  attempts 0, due 09:00
         books.post  pending  attempts 0, due 09:00
09:00  the queue runs: 1
         mail.send   pending  attempts 1, due 09:01, action_error: the mail server gives no answer
         books.post  pending  attempts 0, due 09:00
09:01  the queue runs: 1
         mail.send   pending  attempts 2, due 09:03, action_error: the mail server gives no answer
         books.post  pending  attempts 0, due 09:00
09:03  the queue runs: 2
         mail.send   done     attempts 3
         books.post  dead     attempts 1, action_error: the period is closed
       adam sends books.post again
09:03  the queue runs: 1
         mail.send   done     attempts 3
         books.post  done     attempts 1
the order has the entry E-2026-0117
the log:
  created
  transition send
  action_run mail.send
  action_failed books.post
  action_run books.post
  set
```

## A row of the queue

Each action a commit queued is one row. A row is never removed: a done
row is the record of what was asked, with its arguments.

<!-- a row of the queue -->
| Part | What it is |
|---|---|
| `key` | the name of the row: the event of the commit that queued it, and its place among the actions of that commit. It is the same at every attempt |
| `class`, `doc_id`, `version` | the document whose commit queued the action, and the version that commit made |
| `rule` | where the action stands in the class, such as `/rules/0/do/1` |
| `action` | the action: `create`, `table.insert`, `mail.send`, or `timer` for a transition with a time |
| `args` | the arguments, as the commit worked them out |
| `principal_id`, `principal_kind`, `principal_for` | whom the action runs as: the caller of the commit that queued it |
| `depth` | how far the action is from a caller: 0 when a caller's commit queued it, one more for each action in between |
| `status` | `pending`, `done` or `dead` |
| `attempts` | how often it was tried |
| `due_at` | when the next attempt may run |
| `error_code`, `error_message` | the last failure |
| `created_at`, `finished_at` | when it was queued, and when it was done or dead |
| `outcome_event` | the event `action_run` or `action_failed` that the row ended with |
| `into_code`, `into_message` | why what the action answered could not be written back into the document, when it could not |

## How the worker runs the queue

- **The worker starts when the database is opened**, and stops when it
  is closed. A close waits for an action that is running.
- **A row is due at once.** The worker takes it as soon as the commit
  is written.
- **It runs as the caller of the commit that queued it**, under the
  rules of whatever it touches.
- **The actions of one document run in the order they were queued.** A
  later one waits while an earlier one is tried again: the entry in
  the books is not asked for until the mail is out, at 09:03.
- **The engine's own actions run exactly once**: `create`,
  `transition`, `refresh` and the table actions are commits of the
  same database.
- **A call that leaves the database may arrive twice** after a crash,
  each time with the same key. Let the other side use the key, as
  `call.Key` in a Go function, to do the thing once.
- **What is pending when the program stops stays in the file**, and
  runs when a program opens the database again.

## When an action fails

<!-- when an action fails -->
| The Go function of an action | The row |
|---|---|
| returns no error | is `done`, with the event `action_run` |
| returns an error | stays `pending` and is tried again later; after the last attempt it is `dead` |
| returns an error wrapped with `fado.Permanent` | is `dead` at once, with the event `action_failed` |
| is not in this program | stays `pending` and is tried again later, with the code `not_registered` |

- **No answer is asked again, a refusal is not.** An error means the
  other side could not be reached, and a later attempt may do better.
  `fado.Permanent` means asking again would not help.
- **Each wait is twice the one before.** The first is the `Backoff` of
  the options, one second when none is given: with ten attempts that
  is about nine minutes in all.
- **A refusal of the engine is dead at once too**: a `create` that the
  caller may not make, a `table.insert` of a key the table has. The
  row keeps the code, such as `forbidden` or `duplicate`.
- **A chain has an end.** What an action does may queue actions in
  turn. The engine follows such a chain sixteen deep, and writes the
  next one dead with `too_deep`.
- **The commit that queued the action stands**, whatever becomes of
  the action.

For an action that calls another system over HTTP, which replies count
as no answer and which as a refusal is in
[Actions](../actions.md#what-the-engine-does-with-the-reply).

## A dead action

A dead action is never tried again by itself, and never removed. It
is where an administrator looks.

- **Find it**: list the queue by the status `dead`. The document has
  the event `action_failed` in its log.
- **Mend the cause**: open the period, give the caller the right, add
  the function to the program.
- **Send it again**: the row is `pending` once more, due at once, and
  its attempts start again.

Only a dead action is sent again. A pending one runs by itself, and a
done one has run.

> **The code of a failure is read from the row, and not kept with the
> event.** The event `action_failed` answers its code while the row
> is dead. Once the action is sent again, the event of the first
> failure is still in the log, and answers no code: in the log above,
> `action_failed books.post` stands without one. Until that is
> changed, a program that needs the reason of every failure for good
> writes it down itself when it finds the dead row.

## Running the queue by hand

With `Manual` the worker does not start, and nothing runs until the
program says so, with `Drain`. Drain runs what is due at the moment of
the database's clock, on the calling goroutine, one action at a time,
and answers how many it attempted. It is how a test, a simulation and
a rehearsal in a copy of the file run the queue at moments they
choose, as the program above does.

## Time is in the queue too

A transition with a time is a row of the queue: its action is `timer`,
its `rule` the place of the transition in the class, and its `due_at`
the moment it fires. So the queue shows what will move by itself, and
when; see [Time](notions/time.md).

## Looking at the queue

<!-- looking at the queue -->
| To see | Use |
|---|---|
| the rows, by status, by class or by document | the call `Outbox`, or the command `outbox` |
| the same in SQL, for a report | the view named after the class with `_actions` after it, as `Order_actions` |
| what became of an action, on its document | the events `action_run` and `action_failed` of the document |
| whether this program can run an action | the call `Actions`: each action says whether it is `available` here |

## The options of the worker

They are the program's, given under `Outbox` when it opens the
database; see [Opening a database](opening-a-database.md).

<!-- the options of the worker -->
| Option | What it sets | When none is given |
|---|---|---|
| `Manual` | the worker does not start; the program runs the queue with `Drain` | the worker starts |
| `Workers` | how many actions that leave the database run at once | 4 |
| `MaxAttempts` | how often an action that gets no answer is tried before it is dead | 10 |
| `Backoff` | the wait before the second attempt; each further wait is twice the one before | 1 second |

## Who may

Listing the queue, sending a dead action again and running the queue
by hand are work of the system group `administrators`. An
administrator sees the arguments of every action, and by this group
alone no document.

<!-- the commands -->
| To | Call | Command |
|---|---|---|
| list the queue | `Outbox` | `outbox` |
| send a dead action again | `RetryAction` | `retry_action` |
| run what is due, now | `Drain` | `drain` |

## What is refused

<!-- what is refused -->
| The caller | Code | The refusal names |
|---|---|---|
| lists the queue, sends an action again or runs the queue, and is no administrator | `forbidden` | `/system/administrators` |
| sends again an action that is pending or done | `not_allowed` | |
| sends again an action under a key the queue does not have | `unknown_action` | |
| defines an action with no request, and the program has no function under its name | `not_registered` | |

## Not here yet

- The code of a failure kept with its event.
- Taking a pending action off the queue.
- Removing rows that are done.
- A word to the program when an action dies. Today the program looks.
