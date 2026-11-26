package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The trigger table:<name> (ADR-0091): a triggered variable follows a
// table its own formula looks into, and a write right of FALSE in a
// state is how a class says the value stands there.
const followRatesTable = `{"name": "Rates", "columns": {"code": "text", "rate": "decimal(4)"}, "key": ["code"]}`

func followInvoice(rate, more string) string {
	return `{"name": "Invoice",
 "variables": {
   "currency": {"type": "text"},
   "customer": {"type": "text"},
   "rate": ` + rate + more + `
 },
 "states": {"initial": "entered", "list": ["entered", "checked", "booked"], "final": ["booked"]},
 "transitions": [
   {"name": "check", "from": "entered", "to": "checked"},
   {"name": "book", "from": "checked", "to": "booked"}
 ],
 "rights": {
   "entered": {"rate": {"write": ["ACCOUNTING"]}},
   "checked": {"rate": {"write": " false "}},
   "*":       {"customer": {"write": "INGROUP(\"sales\")"}}
 }}`
}

func TestDefine_AVariableFollowsATable(t *testing.T) {
	tbl, diags := DefineTable([]byte(followRatesTable))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	order, diags := Define([]byte(`{"name": "Order", "variables": {"total": {"type": "number"}}, "states": {"initial": "open", "list": ["open"]}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Sources: formula.NewSourceSet(tbl.Source()), Classes: func(name string) (*Class, bool) {
		return order, strings.EqualFold(name, "Order")
	}}
	const lookup = `{"type": "decimal", "scale": 4, "formula": "LOOKUP(Rates, rate, code = THIS.currency)", "mode": "triggered", "on": `

	// The table is named in any case of letters, and kept as the table
	// writes it.
	cls, diags := DefineIn([]byte(followInvoice(lookup+`["enter:entered", "table:rates"]}`, "")), env)
	if len(diags) > 0 {
		t.Fatalf("a variable that follows its table: %v", diags)
	}
	rate, _ := cls.Variable("rate")
	if !rate.On("table", "Rates") || !rate.On("enter", "entered") || len(rate.Triggers) != 2 {
		t.Errorf("the triggers of rate: %+v", rate.Triggers)
	}
	if got := cls.Follows("RATES"); len(got) != 1 || got[0] != rate {
		t.Errorf("the variables that follow Rates: %v", got)
	}
	if got := cls.Follows("Limits"); len(got) != 0 {
		t.Errorf("the variables that follow a table the class does not read: %v", got)
	}
	// FALSE is nobody, however it is typed; a rule that names people, and
	// no rule, are not.
	for _, tc := range []struct {
		state, variable string
		nobody          bool
	}{{"checked", "rate", true}, {"entered", "rate", false}, {"booked", "rate", false}, {"entered", "customer", false}} {
		if got := cls.WriteRule(tc.state, tc.variable).Nobody(); got != tc.nobody {
			t.Errorf("the write right of %s in %s is nobody's: %v, want %v", tc.variable, tc.state, got, tc.nobody)
		}
	}

	for _, tc := range []struct{ name, def, code, hint string }{
		{"a table without a name", followInvoice(lookup+`["table:"]}`, ""), CodeBadTrigger, ""},
		{"a table the formula does not look into", followInvoice(lookup+`["table:Limits"]}`, ""), CodeNotAllowed, "LOOKUP"},
		{"a formula that looks nowhere", followInvoice(`{"formula": "currency", "mode": "triggered", "on": ["table:Rates"]}`, ""), CodeNotAllowed, "LOOKUP"},
		{"the documents of a class",
			followInvoice(`{"formula": "LOOKUP(Order, SUM(total), TRUE)", "mode": "triggered", "on": ["table:Order"]}`, ""), CodeNotSupported, "refresh"},
		{"a list that is kept",
			followInvoice(`{"type": "rows", "schema": {"code": "text", "rate": "decimal(4)"}, "formula": "QUERY(Rates, {code, rate})", "mode": "triggered", "on": ["table:Rates"]}`, ""), CodeNotSupported, "refresh"},
		{"a schedule", followInvoice(lookup+`["schedule:daily"]}`, ""), CodeNotSupported, "refresh"},
	} {
		_, diags := DefineIn([]byte(tc.def), env)
		found := false
		for _, d := range diags {
			if d.Code == tc.code && d.Path == "/variables/rate/on/0" && strings.Contains(d.Hint, tc.hint) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s at /variables/rate/on/0 with %q in its hint, got: %v", tc.name, tc.code, tc.hint, diags)
		}
	}
}
