// Command stream is the program of the manual's chapter "The stream of
// changes" (docs/manual/the-stream-of-changes.md): an agent asks to be
// told of every invoice that enters review, is told of each as it
// happens, reads it and approves it.
//
//	go run ./examples/stream
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

// An invoice that sales writes and finance approves. Its owner and
// finance may read it.
const invoice = `{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "net":    {"type": "money", "scale": 2},
    "owner":  {"type": "text", "default": "USER()"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "approved"]},
  "transitions": [
    {"name": "submit", "from": "draft", "to": "review"},
    {"name": "approve", "from": "review", "to": "approved"}
  ],
  "access": {
    "read":   "owner = USER() OR INGROUP(\"finance\")",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "delete": "FALSE",
    "transition": {"submit": "owner = USER()", "approve": "INGROUP(\"finance\")"}
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-stream")
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
	db, err := fado.Open(filepath.Join(dir, "acme.fado"), fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()

	olga := fado.As(fado.Principal{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	bob := fado.As(fado.Principal{ID: "bob", Kind: fado.ActorPerson, Groups: []string{"sales"}})
	agent := fado.As(fado.Principal{ID: "ap-agent", Kind: fado.ActorAgent, Groups: []string{"finance"}})
	if _, err := db.DefineClass(ctx, []byte(invoice), olga); err != nil {
		return err
	}

	// The agent asks to be told of every invoice that enters review.
	// The stream begins now: it says what happens from here on.
	changes, err := db.Subscribe(ctx, fado.ChangeFilter{
		Classes:  []string{"Invoice"},
		Kinds:    []string{"transition"},
		ToStates: []string{"review"},
	}, agent)
	if err != nil {
		return err
	}
	defer changes.Close()

	// The agent waits. Next answers when there is an event to tell, and
	// not before: nobody asks again and again.
	done := make(chan error, 1)
	go func() {
		for handled := 0; handled < 2; handled++ {
			ev, err := changes.Next(ctx)
			if err != nil {
				done <- err
				return
			}
			fmt.Fprintf(w, "the agent is told: %s of %s by %s, from %s to %s\n", ev.Name, ev.DocID, ev.ActorID, ev.FromState, ev.ToState)
			// The event says what happened to which document, and
			// carries no value of it. The agent reads the document as
			// it stands now, with the rights it has now, and decides.
			doc, err := db.Get(ctx, ev.Class, ev.DocID, agent)
			if err != nil {
				done <- err
				return
			}
			if _, err := db.Transition(ctx, ev.Class, ev.DocID, "approve", nil, agent); err != nil {
				done <- err
				return
			}
			fmt.Fprintf(w, "the agent reads %s, net %v, and approves it\n", doc.ID, doc.Values["net"])
		}
		done <- nil
	}()

	// Bob writes two invoices and submits each. Writing one is no move
	// into review, so the agent is not told of it.
	for _, number := range []string{"FV/2026/11/0042", "FV/2026/11/0043"} {
		if _, err := db.Create(ctx, "Invoice", fado.Values{"number": number, "net": "1200.00 PLN"}, bob, fado.WithID(number)); err != nil {
			return err
		}
		if _, err := db.Transition(ctx, "Invoice", number, "submit", nil, bob); err != nil {
			return err
		}
	}
	if err := <-done; err != nil {
		return err
	}
	for _, number := range []string{"FV/2026/11/0042", "FV/2026/11/0043"} {
		doc, err := db.Get(ctx, "Invoice", number, bob)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s is %s, by %s\n", doc.ID, doc.State, doc.UpdatedBy)
	}
	return nil
}
