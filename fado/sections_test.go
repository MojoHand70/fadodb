package fado_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// classIn returns the class of a chapter of the manual by its name,
// with the line endings of a checkout on Windows made plain.
func classIn(t *testing.T, chapter, name string) []byte {
	t.Helper()
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", chapter))
	if err != nil {
		t.Fatal(err)
	}
	page = bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
	for _, class := range notionClasses(t, page) {
		if className(string(class)) == name {
			return class
		}
	}
	t.Fatalf("%s shows no class %s", chapter, name)
	return nil
}

// The chapter on the class plays the record's invoice (ADR-0069, item
// 4): Jan files it, and in review Ewa and Marta set what the table of
// the chapter says, each refusal naming the line that decided.
func TestSections_WhoSetsWhatInReview(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "class.md"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "sections.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	class := classIn(t, "class.md", "Invoice")
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	jan := person("jan")
	inv, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Acme", "number": "FV/7", "net": "100 PLN", "vat": "23 PLN"}, jan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Invoice", inv.ID, "file", nil, jan); err != nil {
		t.Fatal(err)
	}
	people := map[string]fado.Option{
		"Ewa, an approver":  person("ewa", "approvers"),
		"Marta, in finance": person("marta", "finance"),
	}
	values := map[string]any{"net": "120 PLN", "vat": "24 PLN", "street": "Polna 1"}
	table := rows(marked(t, page, "who sets what in review"))
	if len(table) != 4 {
		t.Fatalf("the table has %d rows", len(table))
	}
	for _, row := range table {
		who, variable, decided, answered := row[0], row[1], row[2], row[3]
		_, err := db.Set(ctx, "Invoice", inv.ID, fado.Values{variable: values[variable]}, people[who])
		got := "set"
		var refusal *fado.Error
		if errors.As(err, &refusal) {
			got = "refused, " + refusal.Code
			cell, _, _ := strings.Cut(decided, ":")
			if refusal.Variable != variable || !strings.Contains(refusal.Hint, "from the cell "+cell+":") {
				t.Errorf("%s sets %s: the refusal names %s, and its hint %q; the chapter says %s", who, variable, refusal.Variable, refusal.Hint, cell)
			}
		} else if err != nil {
			got = err.Error()
		}
		if got != answered {
			t.Errorf("%s sets %s: %s, and the chapter says %s", who, variable, got, answered)
		}
	}

	// The definition is answered as it was written, the outline in it.
	stored, err := db.Definition(ctx, fado.DefinitionClass, "Invoice", 0)
	if err != nil || !strings.Contains(string(stored.Definition), `"sections":{"supplier":{"label":"Supplier"`) {
		t.Errorf("the definition answered: %v %s", err, stored.Definition)
	}
	// A new outline is a new version of the class and moves no document.
	marta := people["Marta, in finance"]
	before, err := db.Get(ctx, "Invoice", inv.ID, marta)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(class), `"variables": ["customer", "number"]`, `"variables": ["customer", "number", "notes"]`, 1)
	again, err := db.DefineClass(ctx, []byte(moved), setup)
	if err != nil || again.Version != 2 {
		t.Fatalf("notes moved into the supplier section: %v", err)
	}
	if after, err := db.Get(ctx, "Invoice", inv.ID, marta); err != nil || after.Version != before.Version || after.ClassVersion != before.ClassVersion {
		t.Errorf("the document after a new outline: %v, version %d of %d", err, after.Version, before.Version)
	}
}

// A read right on a section hides each variable inside it, however deep,
// from the document a caller gets and from a filter (ADR-0041): Marta in
// finance does not see where the goods went, and a filter of hers on the
// street is refused, where Lena in logistics finds the invoice.
func TestSections_ReadBySection(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "sections.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	class := strings.Replace(string(classIn(t, "class.md", "Invoice")),
		`"review": {"amounts": {"write": ["finance"]}, "vat": {"write": "FALSE"}}`,
		`"*": {"delivery": {"read": ["logistics"]}, "city": {"read": "TRUE"}}`, 1)
	if _, err := db.DefineClass(ctx, []byte(class), setup); err != nil {
		t.Fatal(err)
	}
	inv, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Acme", "number": "FV/8", "has_delivery": true, "street": "Polna 1", "city": "Kraków"}, person("jan"))
	if err != nil {
		t.Fatal(err)
	}
	marta, lena := person("marta", "finance"), person("lena", "finance", "logistics")
	got, err := db.Get(ctx, "Invoice", inv.ID, marta)
	if err != nil {
		t.Fatal(err)
	}
	_, street := got.Values["street"]
	_, delivery := got.Values["has_delivery"]
	if street || delivery || got.Values["city"] != "Kraków" || got.Values["customer"] != "Acme" {
		t.Errorf("Marta's invoice: %v", got.Values)
	}
	_, err = db.Count(ctx, fado.Filter{Class: "Invoice", Where: `street = "Polna 1"`}, marta)
	var refusal *fado.Error
	if !errors.As(err, &refusal) || refusal.Code != "forbidden" || !strings.Contains(refusal.Message, "street") {
		t.Errorf("Marta filters by the street: %v", err)
	}
	if n, err := db.Count(ctx, fado.Filter{Class: "Invoice", Where: `street = "Polna 1"`}, lena); err != nil || n != 1 {
		t.Errorf("Lena counts the invoices delivered to Polna 1: %d %v", n, err)
	}
}
