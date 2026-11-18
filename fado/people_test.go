package fado_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// withoutDescriptions drops the notes of a missing description (ADR-0068,
// item 3). The chapters and guides of the manual each teach one thing,
// and their classes describe what that thing needs; the samples under
// examples/classes describe everything, and are checked with every note.
func withoutDescriptions(ds fado.Diagnostics) fado.Diagnostics {
	var out fado.Diagnostics
	for _, d := range ds {
		if d.Code != "no_description" {
			out = append(out, d)
		}
	}
	return out
}

// What a class says to people is part of its definition and nothing of a
// document (ADR-0068, item 7): a label changed is a new version of the
// class and moves no document; the definition is answered as it was
// written, a column's long form and all; and a value is on the wire as it
// always was, never in a person's format.
func TestPeople_PartOfTheDefinition(t *testing.T) {
	db, err := fado.Open(filepath.Join(t.TempDir(), "people.fado"), fado.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	class := `{"name": "Invoice",
	  "variables": {
	    "number": {"type": "text", "label": "Number", "list": true, "description": "The supplier's number."},
	    "net":    {"type": "money", "scale": 2, "label": "Net amount", "format": "#,##0.00", "description": "Before VAT."},
	    "lines":  {"type": "rows", "description": "One product to a line.", "schema": {"qty": {"type": "int", "label": "Quantity"}, "sku": "text"}}
	  },
	  "states": {"initial": "draft", "list": ["draft"], "descriptions": {"draft": "Being written."}},
	  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`
	cls, err := db.DefineClass(ctx, []byte(class), setup)
	if err != nil || cls.Version != 1 || len(cls.Notes) != 0 {
		t.Fatalf("the described class: %v, version %d, notes %v", err, cls.Version, cls.Notes)
	}
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "7", "net": "12345.5 PLN"}, setup)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(doc.Values["net"]); got != "12345.50 PLN" {
		t.Errorf("net is %s on the wire, as it always was; the format is a page's", got)
	}
	stored, err := db.Definition(ctx, fado.DefinitionClass, "Invoice", 0)
	if err != nil || !strings.Contains(string(stored.Definition), `"qty":{"type":"int","label":"Quantity"},"sku":"text"`) {
		t.Errorf("the definition answered: %v %s", err, stored.Definition)
	}
	again, err := db.DefineClass(ctx, []byte(strings.Replace(class, `"label": "Net amount"`, `"label": "Net"`, 1)), setup)
	if err != nil || again.Version != 2 {
		t.Fatalf("a label changed: %v, version %d", err, again.Version)
	}
	if after, err := db.Get(ctx, "Invoice", doc.ID, setup); err != nil || after.Version != doc.Version || after.ClassVersion != doc.ClassVersion {
		t.Errorf("the document after a new label: %v, version %d of %d, class version %d of %d", err, after.Version, doc.Version, after.ClassVersion, doc.ClassVersion)
	}
}
