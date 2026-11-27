// Command golang is the story of the chapter "In a program of another
// language", in Go, with no library of C: a Go program holds the engine
// itself (docs/manual/other-languages.md). Run it in a folder with
// invoice.json and nothing else:
//
//	go run github.com/MojoHand70/fadodb/examples/languages/golang
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MojoHand70/fadodb/fado"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	invoice, err := os.ReadFile("invoice.json")
	if err != nil {
		return err
	}
	db, err := fado.Open("books.fado", fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	dan := fado.Principal{ID: "dan", Kind: "person", System: []string{"designers"}}
	ann := fado.Principal{ID: "ann", Kind: "person", Groups: []string{"sales"}}
	bob := fado.Principal{ID: "bob", Kind: "person", Groups: []string{"accounting"}}

	// The same commands as from any other language, with db.Exec; a Go
	// program may call db.DefineClass, db.Create and the rest as well.
	if _, err := db.Exec(ctx, dan, "define_class", []byte(`{"definition": `+string(invoice)+`}`)); err != nil {
		return err
	}
	fmt.Println("dan defines the class Invoice")

	var made struct {
		Document struct{ ID, State string } `json:"document"`
	}
	answer, err := db.Exec(ctx, ann, "create", []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042"}}`))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(answer, &made); err != nil {
		return err
	}
	fmt.Printf("ann makes %s: %s\n", made.Document.ID, made.Document.State)

	book := []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"}`)
	if _, err := db.Exec(ctx, bob, "transition", book); err != nil {
		fmt.Printf("bob books it: refused, %s\n", fado.ErrorCode(err))
	}

	if _, err := db.Exec(ctx, bob, "set", []byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}}`)); err != nil {
		return err
	}
	fmt.Println("bob gives the account 400-01")

	answer, err = db.Exec(ctx, bob, "transition", book)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(answer, &made); err != nil {
		return err
	}
	fmt.Printf("bob books it: %s\n", made.Document.State)
	return nil
}
