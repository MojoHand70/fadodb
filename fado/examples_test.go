package fado_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// TestExampleClasses keeps examples/classes honest: every sample class
// the skills draw on validates and defines against the engine as it is,
// so a change that breaks a sample fails here before a reader meets it.
// A sample may name another, as the Invoice's rule names the Receipt it
// creates: the test defines what it can and comes back to the rest, so
// the order of the files does not matter.
func TestExampleClasses(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "examples", "classes", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no sample classes under examples/classes")
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "examples.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for len(files) > 0 {
		var later []string
		for _, f := range files {
			def, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if diags := db.ValidateClass(def); len(diags) != 0 {
				later = append(later, f)
				continue
			}
			if _, err := db.DefineClass(ctx, def); err != nil {
				t.Errorf("%s does not define: %v", filepath.Base(f), err)
			}
		}
		if len(later) == len(files) {
			// Nothing more defines: these are the samples that do not hold.
			for _, f := range later {
				def, _ := os.ReadFile(f)
				t.Errorf("%s does not validate:\n%v", filepath.Base(f), db.ValidateClass(def))
			}
			return
		}
		files = later
	}
}
