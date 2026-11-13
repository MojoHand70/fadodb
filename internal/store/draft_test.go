package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

func TestPreviewDraft(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(ticketV1), asSetup); err != nil {
		t.Fatal(err)
	}
	setupTables(t, s)
	a, err := s.Create(ctx, "Ticket", Values{"title_text": "A", "hours": 2, "tags": []any{"x"}}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	// The draft adds a variable, changes the formula and looks a table up.
	draft := strings.Replace(ticketV2, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate * (1 - discount), 2)"},`,
		`"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate * (1 - discount), 2)"}, "fx": {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"}, "currency": {"type": "text"},`, 1)
	plans := len(s.plans)
	p, err := s.PreviewDraft(ctx, []byte(draft), a.ID, Values{"discount": "0.25", "currency": "EUR"}, "", designing(alice))
	if err != nil {
		t.Fatal(err)
	}
	d := p.Document
	if d.ClassVersion != 2 || d.Version != 2 || d.Values["cost"] != "150.00" || d.Values["fx"] != "4.321000" || d.Values["discount"] != "0.25" || len(d.Values["tags"].([]any)) != 1 || len(p.Errors) != 0 {
		t.Errorf("draft preview: %s", dump(p))
	}
	if len(s.plans) != plans {
		t.Errorf("the draft left %d lookup plans behind", len(s.plans)-plans)
	}
	// Nothing was written or stored.
	if got, _ := s.Get(ctx, "Ticket", a.ID); got.Version != 1 || got.ClassVersion != 1 || got.Values["cost"] != "200.00" {
		t.Errorf("the preview changed the document: %s", dump(got))
	}
	if cls, _ := s.Class("Ticket"); cls.Version != 1 {
		t.Errorf("the draft was stored as version %d", cls.Version)
	}
	// A create under a draft of a new class.
	p, err = s.PreviewDraft(ctx, []byte(`{"name": "Quote", "variables": {"net": {"type": "decimal(2)"}, "vat": {"formula": "ROUND(net * 0.23, 2)"}}}`), "", Values{"net": "100.00"}, "", asSetup)
	if err != nil || p.Document.Values["vat"] != "23.00" || p.Document.Version != 1 {
		t.Errorf("draft of a new class: %v %s", err, dump(p))
	}
	if _, ok := s.Class("Quote"); ok {
		t.Error("the draft class was stored")
	}
	// Errors per variable, as in a preview.
	p, err = s.PreviewDraft(ctx, []byte(strings.Replace(ticketV1, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate, 2)"},`, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate, 2)"}, "per": {"formula": "rate / DECIMAL(hours - 2, 0)"},`, 1)), a.ID, nil, "", asSetup)
	if err != nil || len(p.Errors) != 1 || p.Errors[0].Code != "div_by_zero" || p.Errors[0].Variable != "per" {
		t.Errorf("draft with a failing formula: %v %s", err, dump(p))
	}
	// Refusals.
	_, err = s.PreviewDraft(ctx, []byte(`{"name": "Ticket", "variables": {"x": {"type": "nope"}}}`), a.ID, nil, "", asSetup)
	var diags model.Diagnostics
	if !errors.As(err, &diags) || !diags.Has(model.CodeUnknownType) {
		t.Errorf("a draft that does not compile: %v", err)
	}
	if _, err := s.PreviewDraft(ctx, []byte(`{"name": "Nope", "variables": {"x": {"type": "text"}}}`), a.ID, nil, "", asSetup); code(err) != CodeUnknownClass {
		t.Errorf("a draft of an unknown class with a document: %v", err)
	}
	if _, err := s.PreviewDraft(ctx, []byte(ticketV2), "missing", nil, "", asSetup); code(err) != CodeUnknownDocument {
		t.Errorf("an unknown document: %v", err)
	}
	if _, err := s.PreviewDraft(ctx, []byte(ticketV2), a.ID, nil, "approve", asSetup); code(err) != CodeUnknownTransition {
		t.Errorf("a transition: %v", err)
	}
	// A terminal document may be looked at under a draft, since nothing
	// changes; a plain preview of it is refused.
	if _, err := s.DefineClass(ctx, []byte(frozen), asSetup); err != nil {
		t.Fatal(err)
	}
	f, _ := s.Create(ctx, "Frozen", Values{"n": 21})
	if _, err := s.Preview(ctx, "Frozen", f.ID, Values{"n": 1}, ""); code(err) != CodeTerminal {
		t.Errorf("plain preview of a terminal document: %v", err)
	}
	p, err = s.PreviewDraft(ctx, []byte(strings.Replace(frozen, `"twice": {"formula": "n * 2"}`, `"twice": {"formula": "n * 3"}`, 1)), f.ID, nil, "", asSetup)
	if err != nil || p.Document.Values["twice"] != 63.0 {
		t.Errorf("draft preview of a terminal document: %v %s", err, dump(p))
	}
	if got, _ := s.Get(ctx, "Frozen", f.ID); got.Values["twice"] != 42.0 || got.Version != 1 {
		t.Errorf("the terminal document changed: %s", dump(got))
	}
}
