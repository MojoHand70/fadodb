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
