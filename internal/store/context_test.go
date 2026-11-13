package store

import (
	"context"
	"testing"
)

// ticket has defaults that read the document's own state and id
// (ADR-0040): STATE() and ID() are context functions, allowed in a
// default and refused in an auto formula.
const ticket = `{
  "name": "Ticket",
  "variables": {
    "subject":   {"type": "text", "required": true},
    "ref":       {"type": "text", "default": "\"T-\" & ID()"},
    "opened_in": {"type": "text", "default": "STATE()"},
    "owner":     {"type": "text", "default": "USER()"}
  },
  "states": {"initial": "new", "list": ["new", "closed"], "final": ["closed"]}
}`

// calculator is a class without states: a stateless application that is
// previewed and never stored (ADR-0040).
const calculator = `{
  "name": "Calculator",
  "variables": {
    "amount": {"type": "number"},
    "where":  {"type": "text", "default": "STATE()"},
    "which":  {"type": "text", "default": "ID()"},
    "double": {"formula": "amount * 2"}
  }
}`

func TestStateAndIDInFormulas(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(ticket), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Ticket", Values{"subject": "printer"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["ref"] != "T-"+doc.ID || doc.Values["opened_in"] != "new" || doc.Values["owner"] != "alice" {
		t.Errorf("defaults: %s", dump(doc.Values))
	}
	got, err := s.Get(ctx, "Ticket", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Values["ref"] != "T-"+doc.ID || got.Values["opened_in"] != "new" {
		t.Errorf("stored: %s", dump(got.Values))
	}
	// A second document gets its own id and the same initial state.
	other, err := s.Create(ctx, "Ticket", Values{"subject": "coffee"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if other.Values["ref"] == doc.Values["ref"] || other.Values["ref"] != "T-"+other.ID {
		t.Errorf("second document: %s", dump(other.Values))
	}
	// In a filter the functions are the row's columns.
	for where, want := range map[string]int{
		`STATE() = "new"`:                           2,
		`ID() = "` + doc.ID + `"`:                   1,
		`ID() = "` + doc.ID + `" AND state = "new"`: 1,
		`"T-" & ID() = ref`:                         2,
		`STATE() = "closed"`:                        0,
	} {
		docs, err := s.Query(ctx, Filter{Class: "Ticket", Where: where})
		if err != nil {
			t.Errorf("%s: %v", where, err)
			continue
		}
		if len(docs) != want {
			t.Errorf("%s: %d documents, want %d", where, len(docs), want)
		}
	}
	// A preview over a stored document sees its state and id.
	pv, err := s.PreviewDraft(ctx, opened([]byte(ticket)), doc.ID, Values{"subject": "scanner"}, "", designing(alice))
	if err != nil {
		t.Fatal(err)
	}
	if pv.Document.Values["opened_in"] != "new" || pv.Document.Values["ref"] != "T-"+doc.ID {
		t.Errorf("draft over a document: %s", dump(pv.Document.Values))
	}
	// The preview of a stateless application has an id and no state.
	pv, err = s.PreviewDraft(ctx, opened([]byte(calculator)), "", Values{"amount": 21}, "", designing(alice))
	if err != nil {
		t.Fatal(err)
	}
	if _, has := pv.Document.Values["where"]; has && pv.Document.Values["where"] != nil {
		t.Errorf("stateless STATE(): %s", dump(pv.Document.Values))
	}
	if pv.Document.Values["which"] != pv.Document.ID || pv.Document.ID == "" || pv.Document.Values["double"] != float64(42) {
		t.Errorf("stateless ID(): id %q, values %s", pv.Document.ID, dump(pv.Document.Values))
	}
	// An auto formula may not read them.
	_, err = s.defineOpen(ctx, []byte(`{"name": "Bad", "variables": {"a": {"type": "text"}, "b": {"formula": "STATE() & a"}},
	  "states": {"initial": "new", "list": ["new"]}}`), asSetup)

	if err == nil {
		t.Fatal("STATE() in an auto formula was accepted")
	}
}
