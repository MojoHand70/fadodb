package store

import (
	"context"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/query"
)

// REF makes a pointer a ref compares with (ADR-0076, item 10): REF() is
// the pointer to the document the formula belongs to, wherever ID() may
// stand, and REF("Order", "ORD-17") the pointer to a named document,
// anywhere. Acme's orders and invoices again: Jan and Piotr are in sales
// and each reads the invoices he wrote, Ewa is in finance and reads all.
const pointOrder = `{
 "name": "Order",
 "title": "number",
 "variables": {
   "number":  {"type": "text", "required": true},
   "owner":   {"type": "text", "default": "USER()"},
   "self":    {"type": "ref", "class": "Order", "default": "REF()"},
   "written": {"type": "text", "formula": "\"this is \" & REF()", "mode": "triggered", "on": ["enter:open"]},
   "billed":  {"type": "number", "formula": "LOOKUP(Invoice, COUNT(id), order = REF())", "mode": "triggered", "on": ["refresh"]},
   "waiting": {"type": "number", "mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), order = REF() AND state <> \"paid\")"},
   "of17":    {"mode": "live", "formula": "QUERY(Invoice, {number}, order = REF(\"Order\", \"ORD-17\"), {number})"}
 },
 "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
 "transitions": [{"name": "close", "from": "open", "to": "closed"}],
 "access": {"read": "INGROUP(\"sales\") OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE"}
}`

const pointInvoice = `{
 "name": "Invoice",
 "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "order":    {"type": "ref", "class": "Order", "index": true},
   "corrects": {"type": "ref", "class": "Invoice", "index": true},
   "owner":    {"type": "text", "default": "USER()"},
   "of17":     {"formula": "order = REF(\"Order\", \"ORD-17\")"},
   "of":       {"formula": "order = REF(\"order\", \"ORD-\" & \"17\")"},
   "itself":   {"type": "bool", "mode": "live", "formula": "corrects = REF()"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
 "transitions": [
   {"name": "file", "from": "draft", "to": "review", "guard": "order <> REF(\"Order\", \"ORD-18\")"},
   {"name": "approve", "from": "review", "to": "paid"}
 ],
 "rules": [{"on": "transition", "name": "approve", "if": "corrects = REF()", "do": [{"action": "set", "values": {"number": "number & \" (corrects itself)\""}}]}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE",
            "transition": {"file": "owner = USER()", "approve": "INGROUP(\"finance\")"}}
}`

func openRef(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(pointOrder), []byte(pointInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"ORD-17", "ORD-18"} {
		if _, err := s.Create(ctx, "Order", Values{"number": o}, As(liveJan), WithID(o)); err != nil {
			t.Fatal(err)
		}
	}
	for _, inv := range []struct {
		id, order string
		who       Principal
	}{{"FV-1", "ORD-17", liveJan}, {"FV-2", "ORD-17", liveJan}, {"FV-3", "ORD-17", livePiotr}, {"FV-4", "ORD-18", liveJan}} {
		if _, err := s.Create(ctx, "Invoice", Values{"number": inv.id, "order": "Order:" + inv.order}, As(inv.who), WithID(inv.id)); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

// REF() in a default and in a triggered variable is the pointer to the
// document, and it is written as Class:id. An auto formula takes it no
// more than it takes ID().
func TestRef_TheDocumentItself(t *testing.T) {
	s, ctx := openRef(t)
	for id, want := range map[string]string{"ORD-17": "Order:ORD-17", "ORD-18": "Order:ORD-18"} {
		d, err := s.Get(ctx, "Order", id, As(liveJan))
		if err != nil {
			t.Fatal(err)
		}
		if d.Values["self"] != want || d.Values["written"] != "this is "+want {
			t.Errorf("%s: self %v, written %v; want %s", id, d.Values["self"], d.Values["written"], want)
		}
	}
}

// A ref compares with a pointer written out, in a computed variable and
// in a guard; the class is written as its definition writes it, however
// the formula spells it.
func TestRef_ANamedDocument(t *testing.T) {
	s, ctx := openRef(t)
	for id, want := range map[string]bool{"FV-1": true, "FV-3": true, "FV-4": false} {
		d, err := s.Get(ctx, "Invoice", id, As(liveEwa))
		if err != nil {
			t.Fatal(err)
		}
		if d.Values["of17"] != want || d.Values["of"] != want {
			t.Errorf("%s: of17 %v, of %v; want %v for both", id, d.Values["of17"], d.Values["of"], want)
		}
	}
	// The guard lets an invoice of every order but ORD-18 be filed.
	if _, err := s.Transition(ctx, "Invoice", "FV-4", "file", nil, As(liveJan)); code(err) != CodeGuardFalse {
		t.Errorf("filing an invoice of ORD-18: %v; want guard_false", err)
	}
	if d, err := s.Transition(ctx, "Invoice", "FV-1", "file", nil, As(liveJan)); err != nil || d.State != "review" {
		t.Errorf("filing an invoice of ORD-17: %v, %v", d, err)
	}
}

// REF() in the condition of a LOOKUP is the document the formula
// belongs to: a triggered variable counts with the engine's eyes, a live
// one with its reader's.
func TestRef_TheKeyOfALookup(t *testing.T) {
	s, ctx := openRef(t)
	for id, want := range map[string]float64{"ORD-17": 3, "ORD-18": 1} {
		d, err := s.Refresh(ctx, "Order", id, nil, As(liveJan))
		if err != nil {
			t.Fatal(err)
		}
		if d.Values["billed"] != want {
			t.Errorf("%s: billed %v; want %v", id, d.Values["billed"], want)
		}
	}
	for _, tc := range []struct {
		who  Principal
		want float64
	}{{liveJan, 2}, {livePiotr, 1}, {liveEwa, 3}} {
		v, err := s.Live(ctx, "Order", "ORD-17", "waiting", As(tc.who))
		if err != nil || v.Value != tc.want {
			t.Errorf("%s reads the invoices that wait on ORD-17: %v, %v; want %v", tc.who.ID, v, err, tc.want)
		}
	}
	// A pointer written out names the same document on every order.
	v, err := s.Live(ctx, "Order", "ORD-18", "of17", As(liveEwa))
	if err != nil || ids(v) != "FV-1 FV-2 FV-3" {
		t.Errorf("the invoices of ORD-17, read on ORD-18: %v, %v", v, err)
	}
}

// REF() in a rule and in a filter is the document of the row, as ID()
// is its id.
func TestRef_InARuleAndAFilter(t *testing.T) {
	s, ctx := openRef(t)
	listed := func(where string) string {
		t.Helper()
		docs, err := s.Query(ctx, Filter{Class: "Invoice", Where: where, Order: []string{"id"}}, As(liveEwa))
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		var out []string
		for _, d := range docs {
			out = append(out, d.ID)
		}
		return strings.Join(out, " ")
	}
	if got := listed(`order = REF("Order", "ORD-17")`); got != "FV-1 FV-2 FV-3" {
		t.Errorf("the invoices of ORD-17: %q", got)
	}
	if got := listed(`order <> REF("order", "ORD-17")`); got != "FV-4" {
		t.Errorf("the invoices of another order: %q", got)
	}
	if got := listed(`corrects = REF()`); got != "" {
		t.Errorf("the invoices that correct themselves, before there is one: %q", got)
	}
	// FV-2 corrects itself and FV-1 corrects FV-2.
	for id, corrects := range map[string]string{"FV-2": "Invoice:FV-2", "FV-1": "Invoice:FV-2"} {
		if _, err := s.Set(ctx, "Invoice", id, Values{"corrects": corrects}, As(liveJan)); err != nil {
			t.Fatal(err)
		}
	}
	if got := listed(`corrects = REF()`); got != "FV-2" {
		t.Errorf("the invoices that correct themselves: %q", got)
	}
	if got := listed(`corrects = REF("Invoice", "FV-2") AND corrects <> REF()`); got != "FV-1" {
		t.Errorf("the invoices that correct FV-2 and are not FV-2: %q", got)
	}
	for id, want := range map[string]bool{"FV-1": false, "FV-2": true, "FV-3": false} {
		if v, err := s.Live(ctx, "Invoice", id, "itself", As(liveEwa)); err != nil || v.Value != want {
			t.Errorf("%s corrects itself: %v, %v; want %v", id, v, err, want)
		}
	}
	// The rule fires for the invoice that corrects itself alone.
	for _, id := range []string{"FV-1", "FV-2"} {
		if _, err := s.Transition(ctx, "Invoice", id, "file", nil, As(liveJan)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, "Invoice", id, "approve", nil, As(liveEwa)); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{"FV-1": "FV-1", "FV-2": "FV-2 (corrects itself)"} {
		if d, _ := s.Get(ctx, "Invoice", id, As(liveEwa)); d.Values["number"] != want {
			t.Errorf("%s is numbered %v after the rule; want %s", id, d.Values["number"], want)
		}
	}
	// A filter is held to the rule of two classes as a class is.
	for where, says := range map[string]string{
		`order = REF()`:                    "order points at Order, and this document is an Invoice",
		`order = REF("Invoice", "FV-1")`:   "order points at Order and the REF at Invoice",
		`order = corrects`:                 "order points at Order and corrects at Invoice",
		`order = REF("Nothing", "x")`:      "order points at Order and the REF at Nothing",
		`order = "Order:ORD-17"`:           "compares ref with text",
		`order = REF("Order")`:             "REF takes no argument",
		`order = REF(number, "ORD-17")`:    "the class is written as a text",
		`order = REF("Order", 17)`:         "argument 2 must be",
		`REF("Order", "ORD-17") = REF()`:   "this document is an Invoice",
		`REF("Order", number) = corrects`:  "pointers to two classes",
		`corrects = REF("invoice", order)`: "argument 2 must be",
	} {
		_, err := s.Query(ctx, Filter{Class: "Invoice", Where: where}, As(liveEwa))
		if code(err) != CodeBadFormula || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v; want bad_formula that says %q", where, err, says)
		}
	}
}

// A comparison of a ref with a REF written out is answered through the
// index of the ref (ADR-0038, item 3).
func TestRef_AnsweredThroughTheIndex(t *testing.T) {
	s, ctx := openRef(t)
	for i := 0; i < 50; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"number": "FV", "order": "Order:ORD-18"}, As(liveJan)); err != nil {
			t.Fatal(err)
		}
	}
	src, _ := s.class("Invoice")
	o := options{principal: liveEwa}
	for _, where := range []string{`order = REF("Order", "ORD-17")`, `REF("Order", "ORD-17") = order AND number <> "x"`} {
		plan, err := query.CompileOn(Filter{Class: "Invoice", Where: where}, nil, src.cls, s.sources(), caller(o, src))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT "id" FROM "Invoice" WHERE `+plan.Where, append(append([]any{}, plan.Args...), s.contextArgs(o)...)...)
		if err != nil {
			t.Fatal(err)
		}
		var detail string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			detail += d + "\n"
		}
		rows.Close()
		if !strings.Contains(detail, "doc_Invoice_order") || strings.Contains(detail, "SCAN") {
			t.Errorf("%s is not read through the index on order:\n%s\n%s", where, detail, plan.Where)
		}
	}
}

// What is refused when a class is defined: a comparison of pointers to
// two classes, and a REF that is not written as the function is.
func TestRef_RefusedWhenDefined(t *testing.T) {
	s, ctx := openRef(t)
	for _, tc := range []struct {
		name, variable, code, says string
	}{
		{"REF() compared with a pointer to another class", `"x": {"type": "bool", "mode": "live", "formula": "order = REF()"}`, "type_mismatch", "order points at Order, and this document is a T"},
		{"the same, the other way round", `"x": {"type": "bool", "mode": "live", "formula": "REF() = order"}`, "type_mismatch", "order points at Order, and this document is a T"},
		{"a pointer written out, to another class", `"x": {"formula": "order <> REF(\"Invoice\", n)"}`, "type_mismatch", "order points at Order and the REF at Invoice"},
		{"two variables that point at two classes", `"x": {"formula": "order = invoice"}`, "type_mismatch", "order points at Order and invoice at Invoice"},
		{"in the condition of a lookup", `"x": {"type": "number", "mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), order = REF())"}`, "type_mismatch", "order points at Order, and this document is a T"},
		{"a column and a variable of the document", `"x": {"type": "number", "mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), corrects = THIS.order)"}`, "type_mismatch", "corrects points at Invoice and THIS.order at Order"},
		{"in the condition of a list", `"x": {"mode": "live", "formula": "QUERY(Invoice, number, order = REF())"}`, "type_mismatch", "order points at Order, and this document is a T"},
		{"a list of a named document of another class", `"x": {"mode": "live", "formula": "QUERY(Invoice, number, order = REF(\"Invoice\", \"FV-1\"))"}`, "type_mismatch", "order points at Order and the REF at Invoice"},
		{"a class nobody defined", `"x": {"formula": "REF(\"Nothing\", n)"}`, "bad_formula", "unknown class Nothing"},
		{"a class that is worked out", `"x": {"formula": "REF(n, \"ORD-17\")"}`, "bad_formula", "the class is written as a text"},
		{"one argument", `"x": {"formula": "REF(\"Order\")"}`, "bad_formula", "REF takes no argument, for the pointer to this document, or a class and an id"},
		{"an id that is no text", `"x": {"formula": "REF(\"Order\", 17)"}`, "bad_formula", "argument 2 must be"},
		{"a pointer compared with a text", `"x": {"formula": "order = \"Order:ORD-17\""}`, "bad_formula", "REF(\"Order\", \"ORD-17\") is the pointer"},
		{"REF() in an auto formula, as ID()", `"x": {"formula": "TEXT(REF())"}`, "bad_formula", "may not be used in an auto formula"},
	} {
		def := `{"name": "T", "variables": {"n": {"type": "text"}, "order": {"type": "ref", "class": "Order"}, "invoice": {"type": "ref", "class": "Invoice"}, ` + tc.variable + `}, "states": {"initial": "a", "list": ["a"]}}`
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the class was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s that says %q", tc.name, err, tc.code, tc.says)
		}
	}
	// What the rule lets through: a pointer whose class is one of two,
	// and a list of a named document of the right class.
	def := `{"name": "T", "variables": {"n": {"type": "text"}, "order": {"type": "ref", "class": "Order"}, "invoice": {"type": "ref", "class": "Invoice"},
	  "either": {"formula": "IF(n = \"\", order, invoice) = REF(\"Invoice\", n)"},
	  "named":  {"mode": "live", "formula": "QUERY(Invoice, number, order = REF(\"Order\", \"ORD-17\"))"},
	  "mine":   {"type": "ref", "class": "T"}, "same": {"type": "bool", "mode": "live", "formula": "mine = REF()"}},
	  "states": {"initial": "a", "list": ["a"]}}`
	if _, err := s.DefineClass(ctx, opened([]byte(def)), asSetup); err != nil {
		t.Errorf("a class that compares pointers to one class: %v", err)
	}
}

// A class that a database already holds with a comparison of pointers
// to two classes still runs as it was written: the comparison holds for
// two blank pointers and for no others, as it always did.
func TestRef_AStoredComparisonOfTwoClassesStillRuns(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(pointOrder), []byte(pointInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	earlier := `{"name": "Claim", "variables": {"n": {"type": "text"}, "order": {"type": "ref", "class": "Order"}, "invoice": {"type": "ref", "class": "Invoice"},
	  "odd": {"formula": "order = invoice"}},
	  "states": {"initial": "a", "list": ["a", "b"]},
	  "transitions": [{"name": "go", "from": "a", "to": "b", "guard": "order = invoice"}]}`
	if _, err := s.DefineClass(ctx, opened([]byte(earlier)), asSetup); err == nil || !strings.Contains(err.Error(), "type_mismatch") {
		t.Fatalf("a new class that compares pointers to two classes: %v; want it refused", err)
	}
	s = storedBefore(t, s, path, Options{}, earlier, `order = invoice`, `ISBLANK(order)`)
	d, err := s.Create(ctx, "Claim", Values{"n": "c"}, As(liveJan), WithID("C-1"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Values["odd"] != true {
		t.Errorf("the comparison of a stored class answers %v for two blank pointers; want true", d.Values["odd"])
	}
	if d, err := s.Transition(ctx, "Claim", "C-1", "go", nil, As(liveJan)); err != nil || d.State != "b" {
		t.Errorf("the guard of a stored class: %v, %v", d, err)
	}
}
