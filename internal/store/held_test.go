package store

import (
	"strings"
	"testing"
)

// An invoice with its lines, and what is worked out of them (ADR-0081,
// step 2): the total, a count, a guard and a figure with a condition.
const heldInvoice = `{
 "name": "Invoice",
 "title": "number",
 "variables": {
   "number":  {"type": "text", "required": true},
   "lines":   {"type": "rows", "schema": {"sku": "text", "qty": "decimal(0)", "price": "money(2)", "done": "bool"}},
   "tags":    {"type": "array<text>"},
   "count":   {"formula": "COUNT(lines)"},
   "total":   {"formula": "SUM(lines.qty * lines.price)"},
   "largest": {"formula": "MAX(lines.price)"},
   "open":    {"formula": "COUNTIF(lines, NOT done)"},
   "labels":  {"formula": "COUNTUNIQUE(tags)"}
 },
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent", "guard": "COUNT(lines) > 0 AND ALL(lines, qty > 0)"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

func TestHeld_FiguresOverLines(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	who := As(Principal{ID: "ewa", Groups: []string{"finance"}})
	if _, err := s.DefineClass(ctx, []byte(heldInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	line := func(sku string, qty int, price string, done bool) map[string]any {
		return map[string]any{"sku": sku, "qty": qty, "price": price, "done": done}
	}
	d, err := s.Create(ctx, "Invoice", Values{"number": "FV-1", "tags": []any{"red", "blue", "red"},
		"lines": []any{line("A", 2, "10.50 PLN", true), line("B", 1, "0.25 PLN", false)}}, who)
	if err != nil {
		t.Fatal(err)
	}
	want := func(d *Document, vals map[string]any) {
		t.Helper()
		for name, v := range vals {
			if d.Values[name] != v {
				t.Errorf("%s = %v (%T); want %v", name, d.Values[name], d.Values[name], v)
			}
		}
	}
	want(d, map[string]any{"count": 2.0, "total": "21.25 PLN", "largest": "10.50 PLN", "open": 1.0, "labels": 2.0})
	// A change of the lines works the figures out again; a change of
	// something else leaves them, with the lines the store has.
	d, err = s.Set(ctx, "Invoice", d.ID, Values{"lines": []any{line("A", 2, "10.50 PLN", true), line("B", 1, "0.25 PLN", true), line("C", 3, "100.00 PLN", true)}}, who)
	if err != nil {
		t.Fatal(err)
	}
	want(d, map[string]any{"count": 3.0, "total": "321.25 PLN", "largest": "100.00 PLN", "open": 0.0})
	d, err = s.Set(ctx, "Invoice", d.ID, Values{"number": "FV-1/A"}, who)
	if err != nil {
		t.Fatal(err)
	}
	want(d, map[string]any{"count": 3.0, "total": "321.25 PLN"})
	// The guard reads the lines the store has: no command gave them.
	if d, err = s.Transition(ctx, "Invoice", d.ID, "send", nil, who); err != nil || d.State != "sent" {
		t.Fatalf("send: %v, %v", d, err)
	}
	// An invoice with no lines counts 0, its total is blank, and the
	// guard holds it.
	e, err := s.Create(ctx, "Invoice", Values{"number": "FV-2"}, who)
	if err != nil {
		t.Fatal(err)
	}
	want(e, map[string]any{"count": 0.0, "total": nil, "labels": 0.0})
	if _, err := s.Transition(ctx, "Invoice", e.ID, "send", nil, who); err == nil || !strings.Contains(err.Error(), "guard") {
		t.Errorf("an invoice with no lines was sent: %v", err)
	}
	// A line with no quantity does not meet the guard.
	if _, err := s.Set(ctx, "Invoice", e.ID, Values{"lines": []any{map[string]any{"sku": "X", "price": "1.00 PLN"}}}, who); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Invoice", e.ID, "send", nil, who); err == nil {
		t.Error("an invoice with a line of no quantity was sent")
	}
}
