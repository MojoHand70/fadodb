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

// A live variable folds the lines the store has, and a filter reads the
// computed figure as any stored value.
func TestHeld_LiveAndFilter(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	who := As(Principal{ID: "ewa", Groups: []string{"finance"}})
	live := strings.Replace(heldInvoice, `"labels":`, `"shown": {"mode": "live", "formula": "COUNT(lines) + COUNT(tags)"}, "labels":`, 1)
	if _, err := s.DefineClass(ctx, []byte(live), asSetup); err != nil {
		t.Fatal(err)
	}
	for i, n := range []int{1, 3} {
		lines := make([]any, n)
		for j := range lines {
			lines[j] = map[string]any{"sku": "A", "qty": 1, "price": "2.00 PLN", "done": true}
		}
		if _, err := s.Create(ctx, "Invoice", Values{"number": []string{"FV-1", "FV-2"}[i], "lines": lines, "tags": []any{"x"}}, WithID([]string{"one", "three"}[i]), who); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := s.Live(ctx, "Invoice", "three", "shown", who); err != nil || v.Value != 4.0 {
		t.Errorf("the live figure over the lines: %+v, %v; want 4", v, err)
	}
	docs, err := s.Query(ctx, Filter{Class: "Invoice", Where: "count > 1"}, who)
	if err != nil || len(docs) != 1 || docs[0].ID != "three" {
		t.Errorf("a filter on the computed figure: %+v, %v", docs, err)
	}
	// A filter does not fold the lines itself yet.
	if _, err := s.Query(ctx, Filter{Class: "Invoice", Where: "COUNT(lines) > 1"}, who); err == nil || !strings.Contains(err.Error(), "keep the figure in a computed variable") {
		t.Errorf("a filter that folds the lines: %v", err)
	}
}

// Where a figure over the document's lines may not stand yet is said
// when the class is defined.
func TestHeld_RefusedWhenDefined(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	for _, tc := range []struct{ name, old, new, says string }{
		{"a read rule", `"read": "TRUE"`, `"read": "COUNT(lines) > 0"`, "a read rule may not work a figure out of the lines"},
		{"a column the lines have not", `"COUNT(lines)"`, `"SUM(lines.nothing)"`, "the list lines has no column nothing"},
		{"a sum of a text", `"COUNT(lines)"`, `"SUM(lines.sku)"`, "must be a number, got text"},
		{"the whole list as a value", `"COUNT(lines)"`, `"lines"`, "composite values are not supported yet"},
		{"two lists in one function", `"SUM(lines.qty * lines.price)"`, `"SUM(lines.qty * other.qty)"`, "one function runs over one list"},
		{"a bare column in the words of the document", `"SUM(lines.qty * lines.price)"`, `"SUM(lines.qty * price)"`, "the column of the list is written lines.price"},
		{"a figure inside a figure", `"COUNTIF(lines, NOT done)"`, `"COUNTIF(lines, qty > COUNT(lines))"`, "inside another's arguments"},
	} {
		def := strings.Replace(heldInvoice, tc.old, tc.new, 1)
		def = strings.Replace(def, `"tags":    {"type": "array<text>"},`, `"tags": {"type": "array<text>"}, "other": {"type": "rows", "schema": {"qty": "decimal(0)"}},`, 1)
		if def == heldInvoice {
			t.Fatalf("%s: the test changes nothing", tc.name)
		}
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v; want %q", tc.name, err, tc.says)
		}
	}
}

// A figure added to a class whose documents have lines already is
// worked out of the lines the store has, when the document is next
// written under the new definition.
func TestHeld_AddedToAClassInUse(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := t.Context()
	who := As(Principal{ID: "ewa", Groups: []string{"finance"}})
	plain := strings.NewReplacer(
		`"count":   {"formula": "COUNT(lines)"},`, ``,
		`"total":   {"formula": "SUM(lines.qty * lines.price)"},`, ``,
		`"largest": {"formula": "MAX(lines.price)"},`, ``,
		`"open":    {"formula": "COUNTIF(lines, NOT done)"},`, ``,
		`"labels":  {"formula": "COUNTUNIQUE(tags)"}`, `"note": {"type": "text"}`,
		`"guard": "COUNT(lines) > 0 AND ALL(lines, qty > 0)"`, `"guard": "TRUE"`).Replace(heldInvoice)
	if strings.Contains(plain, "COUNT(") {
		t.Fatal("the test takes the figures out of the invoice")
	}
	if _, err := s.DefineClass(ctx, []byte(plain), asSetup); err != nil {
		t.Fatal(err)
	}
	lines := []any{map[string]any{"sku": "A", "qty": 2, "price": "10.50 PLN", "done": true}, map[string]any{"sku": "B", "qty": 1, "price": "0.25 PLN", "done": false}}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-1", "lines": lines, "tags": []any{"red"}}, WithID("one"), who); err != nil {
		t.Fatal(err)
	}
	with := strings.Replace(heldInvoice, `"labels":`, `"note": {"type": "text"}, "labels":`, 1)
	if _, err := s.DefineClass(ctx, []byte(with), asSetup); err != nil {
		t.Fatalf("the figures added to the invoice: %v", err)
	}
	d, err := s.Set(ctx, "Invoice", "one", Values{"note": "checked"}, who)
	if err != nil {
		t.Fatal(err)
	}
	if d.Values["total"] != "21.25 PLN" || d.Values["count"] != 2.0 || d.Values["open"] != 1.0 || d.Values["labels"] != 1.0 {
		t.Errorf("the figures of a document written before they were defined: %s", dump(d))
	}
	// A database opened again answers the same, and the guard holds.
	s.Close()
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if d, err := s2.Transition(ctx, "Invoice", "one", "send", nil, who); err != nil || d.State != "sent" || d.Values["total"] != "21.25 PLN" {
		t.Errorf("sent after the database was opened again: %v, %v", d, err)
	}
}
