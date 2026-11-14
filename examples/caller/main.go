// Command caller is the program of the manual's chapter "Naming the
// caller" (docs/manual/naming-the-caller.md): four callers work on one
// invoice, and the program prints what the engine answered each of them
// and what the log kept.
//
//	go run ./examples/caller
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

// An invoice that sales creates and accounting books. Booking is kept
// for a person.
const invoice = `{
  "name": "Invoice",
  "variables": {
    "number":  {"type": "text", "required": true},
    "account": {"type": "text"}
  },
  "states": {"initial": "open", "list": ["open", "booked"], "final": ["booked"]},
  "transitions": [
    {"name": "book", "from": "open", "to": "booked", "guard": "NOT ISBLANK(account)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"accounting\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"accounting\")",
    "delete": "FALSE",
    "transition": {"book": "INGROUP(\"accounting\") AND USERKIND() = \"person\""}
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-caller")
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
	db, err := fado.Open(filepath.Join(dir, "books.fado"), fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	// The program knows its callers: from its own login, a token, a
	// queue. Here they are written down.
	dan := fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}}
	ola := fado.Principal{ID: "ola", Kind: fado.ActorPerson, Groups: []string{"sales"}}
	bot := fado.Principal{ID: "ledger-bot", Kind: fado.ActorAgent, Groups: []string{"accounting"}}
	anna := fado.Principal{ID: "anna", Kind: fado.ActorPerson, Groups: []string{"accounting"}}

	// Defining is work of the system group designers.
	if _, err := db.DefineClass(ctx, []byte(invoice), fado.As(dan)); err != nil {
		return err
	}

	// said prints what the engine answered a call.
	said := func(what string, err error) {
		answer := "allowed"
		if err != nil {
			answer = "refused, " + fado.ErrorCode(err)
		}
		fmt.Fprintf(w, "%-36s %s\n", what+":", answer)
	}

	// Each call says who is calling, with As.
	made, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/031"}, fado.As(ola))
	said("ola creates an invoice", err)
	if err != nil {
		return err
	}
	id := made.ID

	// A call with no As is nobody's.
	_, err = db.Get(ctx, "Invoice", id)
	said("a call that names nobody reads it", err)

	_, err = db.Set(ctx, "Invoice", id, fado.Values{"account": "4010"}, fado.As(bot))
	said("ledger-bot sets the account", err)
	_, err = db.Transition(ctx, "Invoice", id, "book", nil, fado.As(bot))
	said("ledger-bot books it", err)
	_, err = db.Transition(ctx, "Invoice", id, "book", nil, fado.As(anna))
	said("anna books it", err)

	// The log keeps who did each thing, and what they were.
	events, err := db.Events(ctx, "Invoice", id, fado.As(anna))
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "the log:")
	for _, event := range events {
		fmt.Fprintf(w, "  %-10s by %s, %s\n", event.Kind, event.ActorID, event.ActorKind)
	}
	return nil
}
