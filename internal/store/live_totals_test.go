package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

// Acme's books (ADR-0090): Ewa, in finance, wants one line for each
// customer, with the number of invoices and their total. Jan and Piotr
// are in sales and each reads the invoices he wrote; the margin is
// finance's to read.
const totalsInvoice = `{
 "name": "Invoice",
 "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true, "label": "Number"},
   "customer": {"type": "text", "label": "Customer", "index": true},
   "net":      {"type": "money(2)", "label": "Net amount"},
   "margin":   {"type": "money(2)", "label": "Margin"},
   "units":    {"type": "number"},
   "issued":   {"type": "date"},
   "owner":    {"type": "text", "default": "USER()"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
 "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE",
            "transition": {"file": "owner = USER()", "approve": "INGROUP(\"finance\")"}},
 "rights": {"*": {"margin": {"read": ["finance"], "write": ["finance", "sales"]}}}
}`

func totalsBooks(more string) string {
	return `{
 "name": "Books",
 "title": "name",
 "variables": {
   "name":     {"type": "text", "required": true},
   "customer": {"type": "text"},
   "by_customer": {"mode": "live", "formula": "QUERY(Invoice, {customer, COUNT(id), SUM(net)})"},
   "largest":     {"mode": "live", "formula": "QUERY(Invoice, {customer, SUM(net)}, state <> \"paid\", {-SUM(net)})"},
   "whole":       {"mode": "live", "formula": "QUERY(Invoice, {COUNT(id), SUM(net), MIN(net), MAX(net), AVERAGE(units), COUNTUNIQUE(customer), MAX(units)})"},
   "by_state":    {"mode": "live", "formula": "QUERY(Invoice, {state, customer, COUNT(id)}, customer = THIS.customer)"},
   "margins":     {"mode": "live", "formula": "QUERY(Invoice, {customer, SUM(margin)})"}` + more + `
 },
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`
}

func openTotals(t *testing.T) (*Store, context.Context, string) {
	t.Helper()
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(totalsInvoice), []byte(totalsBooks(""))}, asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Books", Values{"name": "Acme", "customer": "Globex"}, As(liveJan), WithID("acme")); err != nil {
		t.Fatal(err)
	}
	for _, inv := range []struct {
		id, customer, net string
		units             float64
		issued            string
		who               Principal
	}{
		{"FV-1", "Globex", "1000.00 PLN", 4, "2026-11-03", liveJan},
		{"FV-2", "Globex", "250.00 PLN", 1, "2026-11-10", liveJan},
		{"FV-3", "Globex", "70.00 PLN", 1, "2026-11-12", livePiotr},
		{"FV-4", "Initech", "5.00 PLN", 2, "2026-11-20", liveJan},
		{"FV-5", "Hooli", "400.00 PLN", 8, "2026-11-21", livePiotr},
	} {
		if _, err := s.Create(ctx, "Invoice", Values{"number": inv.id, "customer": inv.customer, "net": inv.net, "margin": "1.00 PLN", "units": inv.units, "issued": inv.issued},
			As(inv.who), WithID(inv.id)); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx, path
}

// lines are the lines of a list of totals as the wire writes them.
func lines(t *testing.T, v *LiveValue) string {
	t.Helper()
	out := make([]string, len(v.Rows))
	for i, r := range v.Rows {
		text, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(text)
	}
	return strings.Join(out, "\n")
}

// One line for each customer, for whoever reads it: Ewa counts every
// invoice, Jan and Piotr the ones each may read.
func TestLiveTotals_OneLineForEachGroup(t *testing.T) {
	s, ctx, _ := openTotals(t)
	for _, tc := range []struct {
		who  Principal
		want string
	}{
		{liveEwa, `{"customer":"Globex","count_id":3,"sum_net":"1320.00 PLN"}
{"customer":"Hooli","count_id":1,"sum_net":"400.00 PLN"}
{"customer":"Initech","count_id":1,"sum_net":"5.00 PLN"}`},
		{liveJan, `{"customer":"Globex","count_id":2,"sum_net":"1250.00 PLN"}
{"customer":"Initech","count_id":1,"sum_net":"5.00 PLN"}`},
		{livePiotr, `{"customer":"Globex","count_id":1,"sum_net":"70.00 PLN"}
{"customer":"Hooli","count_id":1,"sum_net":"400.00 PLN"}`},
	} {
		v, err := s.Live(ctx, "Books", "acme", "by_customer", As(tc.who))
		if err != nil {
			t.Fatalf("%s: %v", tc.who.ID, err)
		}
		if got := lines(t, v); !v.List || v.More || got != tc.want {
			t.Errorf("%s reads (list %v, more %v)\n%s\nwant\n%s", tc.who.ID, v.List, v.More, got, tc.want)
		}
	}
	v, err := s.Live(ctx, "Books", "acme", "by_customer", As(liveEwa))
	if err != nil {
		t.Fatal(err)
	}
	cols, _ := json.Marshal(v.Columns)
	if want := `[{"name":"customer","type":"text","label":"Customer"},{"name":"count_id","type":"number"},{"name":"sum_net","type":"money(2)"}]`; string(cols) != want {
		t.Errorf("the columns are %s; want %s", cols, want)
	}
	// A line is no document: it has no id.
	if v.Rows[0].ID != "" {
		t.Errorf("a line has the id %q", v.Rows[0].ID)
	}
	// The list is kept nowhere: one more invoice is in the next reading.
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-6", "customer": "Initech", "net": "20.00 PLN"}, As(liveJan), WithID("FV-6")); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Live(ctx, "Books", "acme", "by_customer", As(liveEwa))
	if got := lines(t, v); !strings.Contains(got, `{"customer":"Initech","count_id":2,"sum_net":"25.00 PLN"}`) {
		t.Errorf("after one more invoice of Initech:\n%s", got)
	}
}

// The condition says which documents are counted, and the list is
// sorted by a group or a total, the formula's way or the caller's; the
// caller's page is of the lines.
func TestLiveTotals_ConditionOrderAndPage(t *testing.T) {
	s, ctx, _ := openTotals(t)
	page := func(variable string, r LiveRead) *LiveValue {
		t.Helper()
		r.Class, r.ID, r.Variable = "Books", "acme", variable
		v, err := s.LivePage(ctx, r, As(liveEwa))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// FV-1 is paid, and no longer among the ones the list counts.
	if _, err := s.Transition(ctx, "Invoice", "FV-1", "file", nil, As(liveJan)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Invoice", "FV-1", "approve", nil, As(liveEwa)); err != nil {
		t.Fatal(err)
	}
	want := `{"customer":"Hooli","sum_net":"400.00 PLN"}
{"customer":"Globex","sum_net":"320.00 PLN"}
{"customer":"Initech","sum_net":"5.00 PLN"}`
	if got := lines(t, page("largest", LiveRead{})); got != want {
		t.Errorf("the largest first, of what is not paid:\n%s\nwant\n%s", got, want)
	}
	// The caller's order, by a total and by a group.
	if got := lines(t, page("largest", LiveRead{Order: []string{"sum_net"}})); !strings.HasPrefix(got, `{"customer":"Initech"`) {
		t.Errorf("by sum_net, the least first:\n%s", got)
	}
	if got := lines(t, page("by_customer", LiveRead{Order: []string{"-customer"}})); !strings.HasPrefix(got, `{"customer":"Initech"`) {
		t.Errorf("by -customer:\n%s", got)
	}
	if got := lines(t, page("by_customer", LiveRead{Order: []string{"-count_id", "-customer"}})); !strings.HasPrefix(got, `{"customer":"Globex","count_id":3`) || !strings.Contains(got, "Initech\",\"count_id\":1,\"sum_net\":\"5.00 PLN\"}\n{\"customer\":\"Hooli\"") {
		t.Errorf("by -count_id, then -customer:\n%s", got)
	}
	// A page of the lines, and whether lines follow it.
	v := page("by_customer", LiveRead{Limit: 2})
	if got := lines(t, v); !v.More || len(v.Rows) != 2 || !strings.HasPrefix(got, `{"customer":"Globex"`) {
		t.Errorf("the first two lines (more %v):\n%s", v.More, got)
	}
	v = page("by_customer", LiveRead{Limit: 2, Offset: 2})
	if got := lines(t, v); v.More || got != `{"customer":"Initech","count_id":1,"sum_net":"5.00 PLN"}` {
		t.Errorf("the lines after the first two (more %v):\n%s", v.More, got)
	}
	// The condition reads the document the list is on.
	want = `{"state":"draft","customer":"Globex","count_id":2}
{"state":"paid","customer":"Globex","count_id":1}`
	if got := lines(t, page("by_state", LiveRead{})); got != want {
		t.Errorf("the invoices of the customer of the books, by state:\n%s\nwant\n%s", got, want)
	}
	// What the caller may not ask of a list of totals.
	for name, r := range map[string]LiveRead{
		"a column it does not have": {Order: []string{"net"}},
		"a column twice":            {Order: []string{"customer", "-customer"}},
		"some of its columns":       {Fields: []string{"customer"}},
		"a page too long":           {Limit: 20000},
	} {
		r.Class, r.ID, r.Variable = "Books", "acme", "by_customer"
		if _, err := s.LivePage(ctx, r, As(liveEwa)); err == nil {
			t.Errorf("%s is answered", name)
		}
	}
}

// With no plain column the answer is one line, the totals over every
// document the reader may read; over no document it is one line too.
func TestLiveTotals_OneLineOfTheWhole(t *testing.T) {
	s, ctx, _ := openTotals(t)
	v, err := s.Live(ctx, "Books", "acme", "whole", As(liveEwa))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count_id":5,"sum_net":"1725.00 PLN","min_net":"5.00 PLN","max_net":"1000.00 PLN","average_units":3.2,"countunique_customer":3,"max_units":8}`
	if got := lines(t, v); got != want {
		t.Errorf("the whole:\n%s\nwant\n%s", got, want)
	}
	stranger := Principal{ID: "olga", Groups: []string{"design"}}
	v, err = s.Live(ctx, "Books", "acme", "whole", As(stranger))
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(t, v); got != `{"count_id":0,"sum_net":null,"min_net":null,"max_net":null,"average_units":null,"countunique_customer":0,"max_units":null}` {
		t.Errorf("the whole for somebody who reads no invoice:\n%s", got)
	}
	// A list by group has no line then.
	if v, err = s.Live(ctx, "Books", "acme", "by_customer", As(stranger)); err != nil || len(v.Rows) != 0 || len(v.Columns) != 3 {
		t.Errorf("by customer for somebody who reads no invoice: %+v, %v", v, err)
	}
}

// A document with no customer is counted in a line of its own, which
// stands first; a total counts what the reader may read, so the margin
// is finance's to add up; and money in two currencies does not add.
func TestLiveTotals_BlanksRightsAndCurrencies(t *testing.T) {
	s, ctx, _ := openTotals(t)
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-7", "net": "3.00 PLN"}, As(liveJan), WithID("FV-7")); err != nil {
		t.Fatal(err)
	}
	v, err := s.Live(ctx, "Books", "acme", "by_customer", As(liveEwa))
	if err != nil {
		t.Fatal(err)
	}
	if got := lines(t, v); !strings.HasPrefix(got, `{"customer":null,"count_id":1,"sum_net":"3.00 PLN"}`) {
		t.Errorf("the invoice with no customer:\n%s", got)
	}
	if v, err = s.Live(ctx, "Books", "acme", "margins", As(liveEwa)); err != nil || !strings.Contains(lines(t, v), `{"customer":"Globex","sum_margin":"3.00 PLN"}`) {
		t.Errorf("Ewa's margins: %v\n%s", err, lines(t, v))
	}
	if _, err = s.Live(ctx, "Books", "acme", "margins", As(liveJan)); err == nil || err.(*Error).Code != CodeForbidden {
		t.Errorf("Jan, who may not read a margin, adds them up: %v", err)
	}
	// A content administrator passes the rules with a reason, and the
	// log of reads says how many documents only that let them count.
	// Without a reason they are a reader like any other, whom the rule
	// of Invoice shows nothing.
	if v, err = s.Live(ctx, "Books", "acme", "by_customer", As(liveRoot)); err != nil || len(v.Rows) != 0 {
		t.Errorf("a content administrator without a reason: %v, %+v", err, v)
	}
	if v, err = s.Live(ctx, "Books", "acme", "by_customer", As(liveRoot), Reason("audit of November")); err != nil || len(v.Rows) != 4 {
		t.Fatalf("a content administrator with a reason: %v, %+v", err, v)
	}
	reads, err := s.Reads(ctx, ReadsFilter{Class: "Invoice"}, As(Principal{ID: "adm", System: []string{GroupAdministrators}}))
	if err != nil || len(reads) != 1 || reads[0].Command != "live" || reads[0].Counted == nil || *reads[0].Counted != 6 {
		t.Errorf("the log of reads = %+v, %v; want one entry of live with six documents counted past the rules", reads, err)
	}
	// Money in two currencies within one group.
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-8", "customer": "Hooli", "net": "10.00 EUR"}, As(liveJan), WithID("FV-8")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Live(ctx, "Books", "acme", "by_customer", As(liveEwa)); err == nil || err.(*Error).Code != "currency_mismatch" {
		t.Errorf("a sum of zloty and euro: %v", err)
	}
	// Piotr reads Hooli's zloty alone, and his sum stands.
	if v, err = s.Live(ctx, "Books", "acme", "by_customer", As(livePiotr)); err != nil || !strings.Contains(lines(t, v), `{"customer":"Hooli","count_id":1,"sum_net":"400.00 PLN"}`) {
		t.Errorf("Piotr's totals: %v", err)
	}
}

// What is refused when the class is defined (ADR-0090, item 7), and
// what the engine notes of a list of totals.
func TestLiveTotals_Defined(t *testing.T) {
	s, ctx, _ := openTotals(t)
	for formula, want := range map[string]string{
		`QUERY(Invoice, {customer, MEDIAN(net)})`:                 model.CodeNotSupported,
		`QUERY(Invoice, {customer, UPPER(number)})`:               model.CodeNotAllowed,
		`QUERY(Invoice, {customer, SUM(units * 2)})`:              model.CodeNotSupported,
		`QUERY(Invoice, {customer, SUM(number)})`:                 model.CodeBadFormula,
		`QUERY(Invoice, {customer, MAX(issued)})`:                 model.CodeBadFormula,
		`QUERY(Invoice, {customer, SUM(nothing)})`:                model.CodeBadFormula,
		`QUERY(Invoice, {customer, SUM(net, units)})`:             model.CodeBadFormula,
		`QUERY(Invoice, {customer, SUM(net), SUM(net)})`:          model.CodeDuplicate,
		`QUERY(Invoice, {customer, SUM(net)}, TRUE, {-units})`:    model.CodeNotAllowed,
		`QUERY(Invoice, {customer, SUM(net)}, TRUE, {MAX(net)})`:  model.CodeNotAllowed,
		`QUERY(Invoice, {customer, SUM(net)}, TRUE, {-SUM(net)})`: "",
		`QUERY(Invoice, {customer}, TRUE, {SUM(net)})`:            model.CodeNotAllowed,
	} {
		def := totalsBooks(`, "probe": {"mode": "live", "formula": "` + strings.ReplaceAll(formula, `"`, `\"`) + `"}`)
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		diags, _ := err.(model.Diagnostics)
		switch {
		case want == "" && err != nil:
			t.Errorf("%s is refused: %v", formula, err)
		case want != "" && (len(diags) == 0 || diags[0].Code != want || diags[0].Path != "/variables/probe/formula"):
			t.Errorf("%s: %v; want %s at the formula", formula, err, want)
		}
	}
	// A list of totals that is kept, and a figure over one, are not
	// built.
	kept := totalsBooks(`, "kept": {"type": "rows", "mode": "triggered", "on": ["create"], "schema": {"customer": {"type": "text"}, "sum_net": {"type": "money(2)"}},
	  "formula": "QUERY(Invoice, {customer, SUM(net)})"}`)
	if _, err := s.DefineClass(ctx, []byte(kept), asSetup); err == nil || !err.(model.Diagnostics).Has(model.CodeNotSupported) {
		t.Errorf("a list of totals that is kept: %v", err)
	}
	figure := totalsBooks(`, "customers": {"mode": "live", "formula": "COUNT(by_customer)"}`)
	if _, err := s.DefineClass(ctx, []byte(figure), asSetup); err == nil {
		t.Error("a figure over a list of totals is accepted")
	}
	// The engine notes that such a list reads every document, unless its
	// condition compares an indexed variable.
	res, err := s.DefineClass(ctx, []byte(totalsBooks(`, "note": {"type": "text"}`)), asSetup)
	if err != nil {
		t.Fatal(err)
	}
	noted := map[string]bool{}
	for _, d := range res.Notes {
		if d.Code == model.CodeReadsAll {
			noted[d.Path] = true
		}
	}
	if !noted["/variables/by_customer/formula"] || !noted["/variables/largest/formula"] || noted["/variables/by_state/formula"] {
		t.Errorf("the lists noted as reading everything: %v", noted)
	}
}

// A list of totals is a definition: it answers after the database was
// closed and opened, and a source that lost a column of it stops the
// reading of the list and nothing else.
func TestLiveTotals_AcrossAReopening(t *testing.T) {
	s, ctx, path := openTotals(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{Now: (&clock{now: start}).Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	v, err := s.Live(ctx, "Books", "acme", "by_customer", As(liveEwa))
	if err != nil || !strings.HasPrefix(lines(t, v), `{"customer":"Globex","count_id":3,"sum_net":"1320.00 PLN"}`) {
		t.Fatalf("after a reopening: %v", err)
	}
	// The source cannot lose what a list of totals names: the class of
	// the list is noted as a reader of it.
	without := strings.Replace(totalsInvoice, `"customer": {"type": "text", "label": "Customer", "index": true},`, "", 1)
	if _, err := s.DefineClass(ctx, []byte(without), asSetup); err == nil || !strings.Contains(err.Error(), "it would break a formula of the class Books, at /variables/by_customer/formula") {
		t.Fatalf("the source loses the column a list of totals groups by: %v", err)
	}
	// Once the books have no such list, and no invoice a customer, it
	// can. An earlier version of the books still names the column, and
	// the database opens with it.
	bare := `{"name": "Books", "title": "name", "variables": {"name": {"type": "text", "required": true}, "customer": {"type": "text"}},
	  "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`
	if _, err := s.DefineClass(ctx, []byte(bare), asSetup); err != nil {
		t.Fatal(err)
	}
	for id, who := range map[string]Principal{"FV-1": liveJan, "FV-2": liveJan, "FV-3": livePiotr, "FV-4": liveJan, "FV-5": livePiotr} {
		if _, err := s.Set(ctx, "Invoice", id, Values{"customer": nil}, As(who)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(without), asSetup); err != nil {
		t.Fatalf("the source loses a column no list names any more: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path, Options{Now: (&clock{now: start}).Now}); err != nil {
		t.Fatalf("the database does not open after the source of an earlier list of totals lost a column: %v", err)
	}
	if _, err := s.Get(ctx, "Books", "acme", As(liveEwa)); err != nil {
		t.Errorf("the books after the reopening: %v", err)
	}
	s.Close()
}
