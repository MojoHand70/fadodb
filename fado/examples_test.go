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
// The files define in name order, so a sample that another one refers
// to sorts first.
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
	for _, f := range files {
		def, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		if diags := db.ValidateClass(def); len(diags) != 0 {
			t.Errorf("%s does not validate:\n%v", name, diags)
			continue
		}
		if _, err := db.DefineClass(ctx, def); err != nil {
			t.Errorf("%s does not define: %v", name, err)
		}
	}
}
