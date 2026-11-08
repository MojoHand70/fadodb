package fado_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// TestReadmeClass keeps the README honest: the class it shows defines,
// stores and computes as the text says.
func TestReadmeClass(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```json\n(.*?)\n```").FindSubmatch(readme)
	if m == nil {
		t.Fatal("no json block in the README")
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "readme.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineClass(ctx, m[1]); err != nil {
		t.Fatalf("the README class does not define: %v", err)
	}
	alice := fado.Principal{ID: "alice", Groups: []string{"sales"}}
	doc, err := db.Create(ctx, "Invoice", fado.Values{"customer": "ACME", "net": "100.00 PLN"}, fado.As(alice), fado.Key("order-42"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err = db.Set(ctx, "Invoice", doc.ID, fado.Values{"discount": "0.10"}, fado.As(alice))
	if err != nil || doc.Version != 2 || doc.Values["total"] != "90.00 PLN" || doc.Title != "Invoice for ACME" || doc.State != "draft" {
		t.Errorf("the README's walk-through: %v %+v", err, doc)
	}
	open, err := db.Query(ctx, fado.Filter{Class: "Invoice", Where: "state = \"draft\" AND total > 50.00", Order: []string{"-due"}})
	if err != nil || len(open) != 1 {
		t.Errorf("the README's filter: %v %d", err, len(open))
	}
	rows, err := db.SQL(ctx, `SELECT count(*), sum(total) FROM Invoice`, nil)
	if err != nil || rows.Rows[0][0] != int64(1) || rows.Rows[0][1] != int64(9000) {
		t.Errorf("the README's SQL: %v %v", err, rows)
	}
	if _, err := db.Preview(ctx, "Invoice", doc.ID, fado.Values{"discount": "0.50"}, "", fado.As(alice)); err != nil {
		t.Errorf("the README's preview: %v", err)
	}
	if history, err := db.History(ctx, "Invoice", doc.ID); err != nil || len(history) != 2 {
		t.Errorf("the README's history: %v", err)
	}
}
