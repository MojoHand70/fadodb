package fado_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on the class says a pointer compares with a pointer, and
// that REF makes one (ADR-0076, item 10): its table and each sentence
// under it are played here. Acme has a customer C-17 and two invoices,
// INV-1 of that customer and INV-2 of nobody.
func TestClass_APointerComparesWithAPointer(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "class.md"))
	if err != nil {
		t.Fatal(err)
	}
	written := rows(marked(t, page, "pointers"))
	if len(written) != 2 || written[0][0] != "REF()" || written[1][0] != `REF("Customer", "C-17")` ||
		written[0][2] != "where ID() may" || written[1][2] != "anywhere" {
		t.Fatalf("the chapter's table of pointers is %v; the test plays REF() and REF(\"Customer\", \"C-17\")", written)
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "pointers.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	invoice := func(more string) string {
		return scratch("Invoice", `"number": {"type": "text"}, "customer": {"type": "ref", "class": "Customer"},
		  "itself": {"type": "ref", "class": "Invoice", "default": "REF()"},
		  "named":  {"type": "ref", "class": "Customer", "default": "REF(\"Customer\", number)"},
		  "acme":   {"formula": "customer = REF(\"Customer\", \"C-17\")"}`+more, oneState)
	}
	for _, class := range []string{scratch("Customer", `"name": {"type": "text"}`, oneState), invoice("")} {
		if _, err := db.DefineClass(ctx, []byte(class), setup); err != nil {
			t.Fatal(err)
		}
	}
	jan := person("jan")
	if _, err := db.Create(ctx, "Customer", fado.Values{"name": "Acme"}, jan, fado.WithID("C-17")); err != nil {
		t.Fatal(err)
	}
	one, err := db.Create(ctx, "Invoice", fado.Values{"customer": "Customer:C-17"}, jan, fado.WithID("INV-1"))
	if err != nil {
		t.Fatal(err)
	}
	// REF() is the pointer to the document the formula belongs to, and
	// REF with a class and an id the pointer to that document; a blank
	// id gives a blank pointer.
	if one.Values["itself"] != "Invoice:INV-1" || one.Values["named"] != nil || one.Values["acme"] != true {
		t.Errorf("INV-1 holds itself %v, named %v, acme %v; the chapter says Invoice:INV-1, blank and true", one.Values["itself"], one.Values["named"], one.Values["acme"])
	}
	two, err := db.Create(ctx, "Invoice", fado.Values{"number": "C-17"}, jan, fado.WithID("INV-2"))
	if err != nil {
		t.Fatal(err)
	}
	if two.Values["named"] != "Customer:C-17" || two.Values["acme"] != false {
		t.Errorf("INV-2 holds named %v, acme %v; the chapter says Customer:C-17 and false", two.Values["named"], two.Values["acme"])
	}
	// It is a condition in a filter too, and a pointer never compares
	// with a text.
	found, err := db.Query(ctx, fado.Filter{Class: "Invoice", Where: `customer = REF("Customer", "C-17")`}, jan)
	if err != nil || len(found) != 1 || found[0].ID != "INV-1" {
		t.Errorf("the filter by a pointer finds %v, %v; the chapter says INV-1", found, err)
	}
	var refused *fado.Error
	if _, err := db.Query(ctx, fado.Filter{Class: "Invoice", Where: `customer = "Customer:C-17"`}, jan); !errors.As(err, &refused) || refused.Code != "bad_formula" {
		t.Errorf("a pointer compared with a text: %v; the chapter says it is refused", err)
	}
	if got := finding(db, invoice(`, "x": {"formula": "customer = \"Customer:C-17\""}`)); got != "bad_formula" {
		t.Errorf("a class that compares a pointer with a text: %s; the chapter says it is refused", got)
	}
	// A class that compares pointers to two classes is refused when it
	// is defined, in the chapter's words.
	twoClasses := invoice(`, "x": {"type": "bool", "mode": "live", "formula": "customer = REF()"}`)
	says := ""
	for _, d := range db.ValidateClass([]byte(twoClasses)) {
		if !d.Note {
			says = d.Code + ": " + d.Message
		}
	}
	if want := "type_mismatch: customer points at Customer, and this document is an Invoice"; says != want {
		t.Errorf("customer = REF() written in an Invoice: %q; the chapter says %q", says, want)
	}
	if !strings.Contains(string(page), `"customer points at Customer, and this
document is an Invoice"`) {
		t.Error("the chapter no longer quotes the refusal the test plays")
	}
	// REF() stands where ID() may: not in a formula the engine works
	// out by itself, which reads the document's values and nothing of
	// the caller or the document's place.
	for _, formula := range []string{"TEXT(REF())", "ID()"} {
		if got := finding(db, invoice(`, "x": {"formula": "`+formula+`"}`)); got != "bad_formula" {
			t.Errorf("%s in an auto formula: %s; REF() stands where ID() may", formula, got)
		}
	}
}
