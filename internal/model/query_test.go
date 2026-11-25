package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// A live variable whose whole formula is a QUERY is kept as the question
// the store asks (ADR-0076, item 9): it has no program, no column, and
// in the bundle it is the server's.
func TestQuery_CompiledAsAQuestion(t *testing.T) {
	invoice, diags := DefineIn([]byte(`{"name": "Invoice", "variables": {
	  "number": {"type": "text"}, "order": {"type": "ref", "class": "Order"}, "net": {"type": "money(2)"}, "customer": {"type": "text"}},
	  "states": {"initial": "draft", "list": ["draft"]}}`), Env{})
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Classes: func(name string) (*Class, bool) { return invoice, strings.EqualFold(name, "invoice") }}
	const order = `{"name": "Order", "variables": {
	  "number": {"type": "text"}, "customer": {"type": "text"},
	  "invoices": {"mode": "live", "formula": "QUERY(invoice, {NUMBER, net, State}, order = REF() AND customer = this.Customer, {-net, number})"},
	  "all": {"type": "rows", "mode": "live", "formula": "QUERY(Invoice, id)"}},
	  "states": {"initial": "open", "list": ["open"]}}`
	cls, diags := DefineIn([]byte(order), env)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	v, _ := cls.Variable("invoices")
	q := v.Query
	if q == nil || v.Formula != nil || !v.Live() || v.Type.String() != "rows" {
		t.Fatalf("invoices = %+v, query %+v; want a live rows variable with a question and no program", v, q)
	}
	// Names are the declared ones, whatever the case they were written in.
	if q.Source != "Invoice" || strings.Join(q.Columns, " ") != "number net state" || strings.Join(q.Order, " ") != "-net number" || strings.Join(q.This, " ") != "customer" || q.Cond == nil {
		t.Errorf("the question is %+v", q)
	}
	if strings.Join(v.Deps, " ") != "customer" {
		t.Errorf("the list reads %v of its document; want customer", v.Deps)
	}
	if all, _ := cls.Variable("all"); all.Query == nil || all.Query.Cond != nil || strings.Join(all.Query.Columns, " ") != "id" || len(all.Query.Order) != 0 {
		t.Errorf("a list of every document: %+v", all.Query)
	}
	// Kept nowhere: no column and no child table.
	for _, c := range cls.Columns {
		if c.Variable == "invoices" || c.Variable == "all" {
			t.Errorf("the list has the column %s", c.Name)
		}
	}
	if len(cls.Children) != 0 {
		t.Errorf("the list has a child table: %+v", cls.Children)
	}
	// The class reads its source, so a new definition of the source is
	// held to what the list names.
	if strings.Join(cls.Reads, " ") != "Invoice" {
		t.Errorf("the class reads %v; want Invoice", cls.Reads)
	}
	// In the bundle the list is the server's.
	text, err := json.Marshal(Compiled(cls))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), `{"variable":"invoices","type":"rows","reads":["customer"],"server":true}`) {
		t.Errorf("the bundle says of the list: %s", text)
	}
	// Alone, as a stored definition is first compiled, the source is not
	// asked about.
	if alone, diags := DefineIn([]byte(order), Env{Lenient: true, Shallow: true}); len(diags) > 0 || alone == nil {
		t.Errorf("the class alone: %v", diags)
	} else if v, _ := alone.Variable("invoices"); v.Query == nil || v.Query.Source != "invoice" {
		t.Errorf("the list of the class alone: %+v", v.Query)
	}
	// And without its source it is refused.
	if _, diags := DefineIn([]byte(order), Env{}); len(diags) == 0 || !strings.Contains(diags.Error(), "unknown source invoice") {
		t.Errorf("without the source: %v", diags)
	}
}

// A figure over a list (ADR-0081) is a live variable with a program:
// each function over the list is one query slot that names the list and
// the class it lists, and the class is noted as a reader of that class.
// In the bundle the variable is the server's.
func TestFigure_CompiledAsASlot(t *testing.T) {
	invoice, diags := DefineIn([]byte(`{"name": "Invoice", "variables": {
	  "number": {"type": "text"}, "order": {"type": "ref", "class": "Order"}, "net": {"type": "money(2)"}, "qty": {"type": "number"}},
	  "states": {"initial": "draft", "list": ["draft", "paid"]}, "transitions": [{"name": "pay", "from": "draft", "to": "paid"}]}`), Env{})
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Classes: func(name string) (*Class, bool) { return invoice, strings.EqualFold(name, "invoice") }}
	order := func(more string) string {
		return `{"name": "Order", "variables": {"number": {"type": "text"}, "floor": {"type": "number"},
	  "invoices": {"mode": "live", "formula": "QUERY(Invoice, {number, net}, order = REF())"}` + more + `},
	  "states": {"initial": "open", "list": ["open"]}}`
	}
	cls, diags := DefineIn([]byte(order(`,
	  "count": {"mode": "live", "formula": "count(INVOICES)"},
	  "total": {"mode": "live", "formula": "SUM(invoices.net)"},
	  "big":   {"mode": "live", "formula": "MAXIF(invoices, qty > THIS.floor, qty)"},
	  "all":   {"mode": "live", "formula": "ALL(invoices, state = \"paid\")"},
	  "mean":  {"type": "number", "mode": "live", "formula": "SUM(invoices.qty) / COUNT(invoices.qty)"}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	for _, tc := range []struct {
		name, typ, deps string
		slots           int
		agg             string
	}{
		{"count", "number", "", 1, "COUNT"},
		{"total", "money(2)", "", 1, "SUM"},
		{"big", "number", "floor", 1, "MAX"},
		{"all", "bool", "", 2, "COUNT"},
		{"mean", "number", "", 2, "SUM"},
	} {
		v, _ := cls.Variable(tc.name)
		if v.Formula == nil || v.Query != nil || !v.Live() || v.Type.String() != tc.typ || strings.Join(v.Deps, " ") != tc.deps || len(v.Formula.Queries) != tc.slots {
			t.Errorf("%s = %+v; want a live %s with a program of %d slots that reads %q", tc.name, v, tc.typ, tc.slots, tc.deps)
			continue
		}
		// The slot names the list as the class declares it, and the class
		// the list lists.
		if q := v.Formula.Queries[0]; q.List != "invoices" || q.Source != "Invoice" || q.Agg != tc.agg || q.Value == nil || q.Cond == nil {
			t.Errorf("%s asks %+v; want %s over the list invoices of Invoice", tc.name, q, tc.agg)
		}
	}
	if strings.Join(cls.Reads, " ") != "Invoice" {
		t.Errorf("the class reads %v; want Invoice", cls.Reads)
	}
	// Kept nowhere, and the server's in the bundle.
	for _, c := range cls.Columns {
		if c.Variable == "count" || c.Variable == "total" {
			t.Errorf("the figure has the column %s", c.Name)
		}
	}
	text, err := json.Marshal(Compiled(cls))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), `{"variable":"total","type":"money(2)","reads":[],"server":true}`) {
		t.Errorf("the bundle says of the figure: %s", text)
	}
	// Outside a live variable a figure is refused. A LOOKUP beside a
	// figure in a live variable is taken (ADR-0076, item 5).
	for formula, says := range map[string]string{
		`"type": "number", "formula": "COUNT(invoices)"`:                                              "stands only in a live variable",
		`"type": "number", "mode": "triggered", "on": ["refresh"], "formula": "COUNT(invoices)"`:      "stands only in a live variable",
		`"mode": "live", "formula": "MEDIAN(number)"`:                                                 "MEDIAN: the argument is a list of the document",
		`"type": "number", "formula": "COUNTUNIQUE(invoices.state)"`:                                  "stands only in a live variable",
		`"mode": "live", "formula": "COUNTIF(invoices, ID() = \"x\")"`:                                "ID() is a function of the document the formula belongs to",
	} {
		_, diags := DefineIn([]byte(order(`, "x": {`+formula+`}`)), env)
		if len(diags) == 0 || !strings.Contains(diags.Error(), says) {
			t.Errorf("%s: %v; want %q", formula, diags, says)
		}
	}
	// A class looked at alone, before the class it lists is known, is
	// left with the figure unbound and no finding.
	shallow, diags := DefineIn([]byte(order(`, "count": {"mode": "live", "formula": "COUNT(invoices)"}`)), Env{Shallow: true, Lenient: true})
	if len(diags) > 0 || shallow == nil {
		t.Errorf("the order alone: %v", diags)
	}
}
