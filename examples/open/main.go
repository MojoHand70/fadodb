// Command open is the program of the manual's chapter "Opening a
// database" (docs/manual/opening-a-database.md): it makes a database
// file, writes a note into it, closes it, opens it again and reads the
// note back.
//
//	go run ./examples/open
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MojoHand70/fadodb/fado"
)

// The class of the notes this program keeps.
const note = `{
  "name": "Note",
  "variables": {"text": {"type": "text", "required": true}},
  "states": {"initial": "kept", "list": ["kept"]},
  "access": {
    "read":   "INGROUP(\"everyone\")",
    "create": "INGROUP(\"everyone\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE"
  }
}`

func main() {
	dir, err := os.MkdirTemp("", "fadodb-open")
	if err == nil {
		defer os.RemoveAll(dir)
		err = run(os.Stdout, dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run keeps a note in a database under dir and reads it back after the
// database was closed and opened again.
func run(w io.Writer, dir string) error {
	ctx := context.Background()
	path := filepath.Join(dir, "notes.fado")

	// Open makes the file when it is not there.
	db, err := fado.Open(path, fado.Options{})
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "a new database has the classes", db.Classes())

	// Every call says who is calling. The program knows its callers; the
	// engine does not.
	dan := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	ola := fado.As(fado.Principal{ID: "ola", Kind: fado.ActorPerson})

	if _, err := db.DefineClass(ctx, []byte(note), dan); err != nil {
		return err
	}
	made, err := db.Create(ctx, "Note", fado.Values{"text": "buy milk"}, ola)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "ola wrote %q, version %d\n", made.Values["text"], made.Version)

	// Close finishes what is in progress and frees the file.
	if err := db.Close(); err != nil {
		return err
	}

	// The class and the note are in the file, and a second Open finds
	// them.
	db, err = fado.Open(path, fado.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Fprintln(w, "opened again, it has the classes", db.Classes())
	again, err := db.Get(ctx, "Note", made.ID, ola)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "ola reads %q, version %d\n", again.Values["text"], again.Version)
	return nil
}
