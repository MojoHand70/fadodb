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
