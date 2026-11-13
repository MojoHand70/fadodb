// Command first is the program of the manual's first application
// (docs/manual/first-application.md): it defines the Invoice class of
// invoice.json in a new database, walks one invoice through its states
// as two people, and prints what the engine answered at each step.
//
//	go run ./examples/first
//
// Change invoice.json and run it again to see what a change does.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

func main() {
	file := filepath.Join("examples", "first", "invoice.json")
	if len(os.Args) > 1 {
		file = os.Args[1]
	}
	if err := run(os.Stdout, file); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run defines the class in the file and plays the story of the chapter,
// writing one line per step.
func run(w io.Writer, classFile string) error {
	definition, err := os.ReadFile(classFile)
	if err != nil {
		return err
	}
	// A database is one file. This one is made new at every run.
	dir, err := os.MkdirTemp("", "fado-first")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := fado.Open(filepath.Join(dir, "first.fado"), fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()

	// Every call says who makes it. The program's own setup is a designer:
	// it may define classes, and nothing else.
	setup := fado.As(fado.Principal{ID: "setup", Kind: fado.ActorService, System: []string{fado.GroupDesigners}})
	if _, err := db.DefineClass(ctx, definition, setup); err != nil {
		return err
	}

	// The two people of the story. Anna is in the group ACCOUNTING.
	krzysztof := fado.As(fado.Principal{ID: "krzysztof", Kind: fado.ActorPerson})
	anna := fado.As(fado.Principal{ID: "anna", Kind: fado.ActorPerson, Groups: []string{"ACCOUNTING"}})

	// 1. Krzysztof registers an invoice. The engine computes the gross.
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV/2026/11/017", "supplier": "ACME", "net": "1000.00 PLN"}, krzysztof)
	if err != nil {
		return err
	}
	step(w, 1, "krzysztof creates it", "%s, state %s, gross %v", doc.Title, doc.State, doc.Values["gross"])

	// 2. In INAPPROVAL the editor is Krzysztof, so Anna is refused.
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"note": "checked"}, anna)
	step(w, 2, "anna writes a note", "%s", outcome(err))

	// 3. Krzysztof changes the net amount; the gross follows.
	doc, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"net": "1200.00 PLN"}, krzysztof)
	if err != nil {
		return err
	}
	step(w, 3, "krzysztof changes the net", "allowed, gross %v, version %d", doc.Values["gross"], doc.Version)

	// 4. Only Krzysztof may approve.
	_, err = db.Transition(ctx, "Invoice", doc.ID, "approve", nil, anna)
	step(w, 4, "anna approves", "%s", outcome(err))
	doc, err = db.Transition(ctx, "Invoice", doc.ID, "approve", nil, krzysztof)
	if err != nil {
		return err
	}
	step(w, 5, "krzysztof approves", "allowed, state %s", doc.State)

	// 6. In INACCOUNTING the editors are the group ACCOUNTING.
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"note": "one more thing"}, krzysztof)
	step(w, 6, "krzysztof writes a note", "%s", outcome(err))

	// 7. Booking needs an account: the guard of the transition says so.
	_, err = db.Transition(ctx, "Invoice", doc.ID, "book", nil, anna)
	step(w, 7, "anna books it with no account", "%s", outcome(err))
	if _, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"account": "402-01"}, anna); err != nil {
		return err
	}
	doc, err = db.Transition(ctx, "Invoice", doc.ID, "book", nil, anna)
	if err != nil {
		return err
	}
	step(w, 8, "anna sets the account and books", "allowed, state %s", doc.State)

	// 9. BOOKED is a final state: the invoice is frozen.
	_, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"note": "too late"}, anna)
	step(w, 9, "anna writes a note", "%s", outcome(err))

	// The log: every change, who made it and what it did.
	events, err := db.Events(ctx, "Invoice", doc.ID, anna)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "\nThe log of the invoice:")
	for _, e := range events {
		line := fmt.Sprintf("  version %d  %-10s by %s", e.AfterVersion, e.Kind, e.ActorID)
		if e.Kind == "transition" {
			line += fmt.Sprintf(", %s: %s -> %s", e.Name, e.FromState, e.ToState)
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// step writes one line of the story: its number, who does what, and what
// the engine answered.
func step(w io.Writer, n int, what, answer string, args ...any) {
	fmt.Fprintf(w, "%d. %-35s %s\n", n, what+":", fmt.Sprintf(answer, args...))
}

// outcome says in a few words what the engine answered: allowed, or the
// code of its refusal and the place it names.
func outcome(err error) string {
	if err == nil {
		return "allowed"
	}
	var refusal *fado.Error
	if !errors.As(err, &refusal) {
		return err.Error()
	}
	switch {
	case refusal.Variable != "":
		return "refused, " + refusal.Code + ", variable " + refusal.Variable
	case refusal.Path != "":
		return "refused, " + refusal.Code + " at " + refusal.Path
	}
	return "refused, " + refusal.Code
}
