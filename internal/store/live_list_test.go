package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/query"
)

// Acme's orders and invoices (ADR-0076): an order lists the invoices
// that point at it. Jan and Piotr are in sales and each reads the
// invoices he wrote; Ewa is in finance and reads every invoice, and the
// margin, which sales may not see.
const listInvoice = `{
 "name": "Invoice",
 "title": "number",
 "variables": {
   "number": {"type": "text", "required": true, "label": "Number"},
   "order":  {"type": "ref", "class": "Order", "label": "Order"},
   "net":    {"type": "money(2)", "label": "Net amount"},
   "margin": {"type": "money(2)", "label": "Margin"},
   "owner":  {"type": "text", "default": "USER()"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
 "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE",
            "transition": {"file": "owner = USER()", "approve": "INGROUP(\"finance\")"}},
 "rights": {"*": {"margin": {"read": ["finance"], "write": ["finance", "sales"]}}}
}`

const listOrder = `{
 "name": "Order",
 "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "customer": {"type": "text"},
   "owner":    {"type": "text", "default": "USER()"},
   "invoices": {"label": "Invoices", "mode": "live",
                "formula": "QUERY(Invoice, {number, net, state, margin}, order = REF(), {number})"}
 },
 "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
 "transitions": [{"name": "close", "from": "open", "to": "closed"}],
 "access": {"read": "INGROUP(\"sales\") OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE"}
}`

func openList(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(listOrder), []byte(listInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"ORD-17", "ORD-18"} {
		if _, err := s.Create(ctx, "Order", Values{"number": o, "customer": "Globex"}, As(liveJan), WithID(o)); err != nil {
			t.Fatal(err)
		}
	}
	for _, inv := range []struct {
		id, order, net string
		who            Principal
	}{{"FV-1", "ORD-17", "1000.00 PLN", liveJan}, {"FV-2", "ORD-17", "250.00 PLN", liveJan}, {"FV-3", "ORD-17", "70.00 PLN", livePiotr}, {"FV-4", "ORD-18", "5.00 PLN", liveJan}} {
		if _, err := s.Create(ctx, "Invoice", Values{"number": inv.id, "order": "Order:" + inv.order, "net": inv.net, "margin": "1.00 PLN"}, As(inv.who), WithID(inv.id)); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

// ids are the ids of the rows of a list, in its order.
func ids(v *LiveValue) string {
	var out []string
	for _, r := range v.Rows {
		out = append(out, r.ID)
	}
	return strings.Join(out, " ")
}

// A live list shows each reader the documents they may read, and of the
// order asked alone.
func TestLiveList_ForTheReader(t *testing.T) {
	s, ctx := openList(t)
	for _, tc := range []struct {
		who  Principal
		want string
	}{{liveJan, "FV-1 FV-2"}, {livePiotr, "FV-3"}, {liveEwa, "FV-1 FV-2 FV-3"}} {
		v, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(tc.who))
		if err != nil {
			t.Fatalf("%s: %v", tc.who.ID, err)
		}
		if !v.List || ids(v) != tc.want || v.More {
			t.Errorf("%s reads %q (list %v, more %v); want %q", tc.who.ID, ids(v), v.List, v.More, tc.want)
		}
	}
	v, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(liveJan))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(v.Rows[0])
	if want := `{"id":"FV-1","number":"FV-1","net":"1000.00 PLN","state":"draft"}`; string(text) != want {
		t.Errorf("Jan's first row is %s; want %s", text, want)
	}
	v, _ = s.Live(ctx, "Order", "ORD-17", "invoices", As(liveEwa))
	text, _ = json.Marshal(v.Rows[0])
	if want := `{"id":"FV-1","number":"FV-1","net":"1000.00 PLN","state":"draft","margin":"1.00 PLN"}`; string(text) != want {
		t.Errorf("Ewa's first row is %s; want %s", text, want)
	}
	cols, _ := json.Marshal(v.Columns)
	if want := `[{"name":"number","type":"text","label":"Number"},{"name":"net","type":"money(2)","label":"Net amount"},{"name":"state","type":"text"},{"name":"margin","type":"money(2)","label":"Margin"}]`; string(cols) != want {
		t.Errorf("the columns are %s; want %s", cols, want)
	}
}

// The formula says which documents; the caller says which page of them,
// in what order and with which columns (ADR-0076, item 7).
func TestLiveList_ThePage(t *testing.T) {
	s, ctx := openList(t)
	page := func(r LiveRead) *LiveValue {
		t.Helper()
		r.Class, r.ID, r.Variable = "Order", "ORD-17", "invoices"
		v, err := s.LivePage(ctx, r, As(liveEwa))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// The order of the formula, then the caller's.
	if v := page(LiveRead{}); ids(v) != "FV-1 FV-2 FV-3" {
		t.Errorf("by the formula's order: %q", ids(v))
	}
	if v := page(LiveRead{Order: []string{"-number"}}); ids(v) != "FV-3 FV-2 FV-1" {
		t.Errorf("by -number: %q", ids(v))
	}
	// A page, and whether rows follow it.
	if v := page(LiveRead{Limit: 2}); ids(v) != "FV-1 FV-2" || !v.More {
		t.Errorf("the first two: %q, more %v; want FV-1 FV-2 and more", ids(v), v.More)
	}
	if v := page(LiveRead{Limit: 2, Offset: 2}); ids(v) != "FV-3" || v.More {
		t.Errorf("the next two: %q, more %v; want FV-3 and no more", ids(v), v.More)
	}
	if v := page(LiveRead{Limit: 3}); ids(v) != "FV-1 FV-2 FV-3" || v.More {
		t.Errorf("exactly three: %q, more %v; want all and no more", ids(v), v.More)
	}
	// The columns asked for, in the order asked.
	v := page(LiveRead{Fields: []string{"STATE", "number"}, Limit: 1})
	text, _ := json.Marshal(v.Rows[0])
	if want := `{"id":"FV-1","state":"draft","number":"FV-1"}`; string(text) != want || len(v.Columns) != 2 || v.Columns[0].Name != "state" {
		t.Errorf("two columns: %s, %v; want %s", text, v.Columns, want)
	}
	for _, tc := range []struct {
		name string
		r    LiveRead
		code string
	}{
		{"a column the list has not", LiveRead{Fields: []string{"owner"}}, CodeUnknownVariable},
		{"an order by what the source has not", LiveRead{Order: []string{"nothing"}}, CodeUnknownVariable},
		{"a limit past the most", LiveRead{Limit: 10001}, CodeBadValue},
		{"a negative offset", LiveRead{Offset: -1}, CodeBadValue},
	} {
		tc.r.Class, tc.r.ID, tc.r.Variable = "Order", "ORD-17", "invoices"
		if _, err := s.LivePage(ctx, tc.r, As(liveEwa)); err == nil || err.(*Error).Code != tc.code {
			t.Errorf("%s: %v; want %s", tc.name, err, tc.code)
		}
	}
	// A page is for a list alone.
	if _, err := s.DefineClass(ctx, []byte(liveInvoice2), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"number": "M-1"}, As(liveJan), WithID("M-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LivePage(ctx, LiveRead{Class: "Memo", ID: "M-1", Variable: "mine", Limit: 5}, As(liveJan)); err == nil || err.(*Error).Code != CodeNotAllowed {
		t.Errorf("a limit on a single value: %v; want %s", err, CodeNotAllowed)
	}
}

// liveInvoice2 is a class with a live variable that is one value.
const liveInvoice2 = `{"name": "Memo", "title": "number",
 "variables": {"number": {"type": "text"}, "owner": {"type": "text", "default": "USER()"},
               "mine": {"type": "bool", "mode": "live", "formula": "owner = USER()"}},
 "states": {"initial": "a", "list": ["a"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`

// Nothing of a list is kept: no read of the order carries it, no commit
// writes it, and what was added a moment ago is in it.
func TestLiveList_KeptNowhere(t *testing.T) {
	s, ctx := openList(t)
	doc, err := s.Get(ctx, "Order", "ORD-17", As(liveJan))
	if err != nil || has(doc, "invoices") {
		t.Fatalf("get = %v, %v; want the order without its list", doc, err)
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-5", "order": "Order:ORD-17", "net": "9.00 PLN"}, As(liveJan), WithID("FV-5")); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(liveJan)); err != nil || ids(v) != "FV-1 FV-2 FV-5" {
		t.Errorf("after one more invoice: %q, %v; want FV-1 FV-2 FV-5", ids(v), err)
	}
	after, err := s.Get(ctx, "Order", "ORD-17", As(liveJan))
	if err != nil || after.Version != doc.Version {
		t.Errorf("the order is at version %d after its list changed; it was at %d", after.Version, doc.Version)
	}
	if _, err := s.Set(ctx, "Order", "ORD-17", Values{"invoices": []any{}}, As(liveJan)); err == nil || err.(*Error).Code != CodeReadOnly {
		t.Errorf("a set of the list: %v; want %s", err, CodeReadOnly)
	}
	// An order nothing points at has a list with its headings and no row.
	v, err := s.Live(ctx, "Order", "ORD-18", "invoices", As(livePiotr))
	if err != nil || !v.List || len(v.Rows) != 0 || len(v.Columns) != 4 || v.More {
		t.Errorf("Piotr's list of ORD-18 = %+v, %v; want the four columns and no row", v, err)
	}
}

// Whoever may read the document reads its list; of the documents listed
// they get the ones they may read. A content administrator reads past
// the rules with a reason, and the log of reads keeps what was shown.
func TestLiveList_WhoMayRead(t *testing.T) {
	s, ctx := openList(t)
	stranger := Principal{ID: "olga", Groups: []string{"design"}}
	if _, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(stranger)); err == nil || err.(*Error).Code != CodeUnknownDocument {
		t.Errorf("somebody who may not read the order: %v; want %s", err, CodeUnknownDocument)
	}
	if _, err := s.Live(ctx, "Order", "ORD-99", "invoices", As(liveJan)); err == nil || err.(*Error).Code != CodeUnknownDocument {
		t.Errorf("an order that is not there: %v; want %s", err, CodeUnknownDocument)
	}
	if _, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(liveRoot)); err == nil || err.(*Error).Code != CodeReasonRequired {
		t.Errorf("a content administrator without a reason: %v; want %s", err, CodeReasonRequired)
	}
	v, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(liveRoot), Reason("audit of November"))
	if err != nil || ids(v) != "FV-1 FV-2 FV-3" {
		t.Fatalf("a content administrator with a reason: %q, %v; want every invoice of the order", ids(v), err)
	}
	if _, has := v.Rows[0].Values["margin"]; !has {
		t.Errorf("a content administrator with a reason does not see the margin: %v", v.Rows[0].Values)
	}
	reads, err := s.Reads(ctx, ReadsFilter{Class: "Invoice"}, As(Principal{ID: "adm", System: []string{GroupAdministrators}}))
	if err != nil || len(reads) != 1 || reads[0].Command != "live" || strings.Join(reads[0].Documents, " ") != "FV-1 FV-2 FV-3" {
		t.Fatalf("the log of reads = %+v, %v; want one entry of live with the three invoices", reads, err)
	}
}

// The condition reads the document the list is on: a variable of it as
// THIS.name, of any scalar type, beside what a filter may read.
func TestLiveList_ReadsItsDocument(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Part", "title": "name",
		  "variables": {"name": {"type": "text"}, "kind": {"type": "text"}, "price": {"type": "decimal(2)"}, "cost": {"type": "money(2)"},
		                "since": {"type": "date"}, "stock": {"type": "number"}, "sold": {"type": "bool"}},
		  "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b"}],
		  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE", "transition": {"go": "TRUE"}}}`,
		`{"name": "Shelf", "title": "kind",
		  "variables": {"kind": {"type": "text"}, "most": {"type": "decimal(2)"}, "budget": {"type": "money(2)"}, "from": {"type": "date"},
		                "least": {"type": "number"}, "sold": {"type": "bool"},
		    "same":    {"mode": "live", "formula": "QUERY(Part, name, kind = THIS.kind)"},
		    "cheap":   {"mode": "live", "formula": "QUERY(Part, {name, price}, price <= THIS.most, {-price})"},
		    "within":  {"mode": "live", "formula": "QUERY(Part, name, cost <= THIS.budget)"},
		    "newer":   {"mode": "live", "formula": "QUERY(Part, name, since >= THIS.from AND stock > THIS.least)"},
		    "flagged": {"mode": "live", "formula": "QUERY(Part, name, sold = THIS.sold AND kind = THIS.kind)"},
		    "moved":   {"mode": "live", "formula": "QUERY(Part, {name, state}, STATE() = \"b\" OR name = THIS.kind)"},
		    "all":     {"mode": "live", "formula": "QUERY(Part, id)"},
		    "others":  {"mode": "live", "formula": "QUERY(Shelf, kind, kind <> THIS.kind AND ID() <> \"\", kind)"}},
		  "states": {"initial": "a", "list": ["a"]},
		  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`,
	} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []Values{
		{"name": "bolt", "kind": "steel", "price": "1.50", "cost": "1.00 PLN", "since": "2026-01-10", "stock": 5, "sold": true},
		{"name": "nut", "kind": "steel", "price": "0.50", "cost": "0.30 PLN", "since": "2026-03-01", "stock": 50, "sold": false},
		{"name": "ring", "kind": "rubber", "price": "2.00", "cost": "1.90 EUR", "since": "2026-06-01", "stock": 500, "sold": true},
		{"name": "cap", "kind": "", "price": "0.00", "since": "2026-06-01", "stock": 0},
	} {
		if _, err := s.Create(ctx, "Part", p, As(liveJan), WithID(p["name"].(string))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(ctx, "Part", "ring", "go", nil, As(liveJan)); err != nil {
		t.Fatal(err)
	}
	for _, sh := range []Values{
		{"kind": "steel", "most": "1.50", "budget": "1.00 PLN", "from": "2026-02-01", "least": 10, "sold": true},
		{"kind": "", "most": "0.00"},
	} {
		if _, err := s.Create(ctx, "Shelf", sh, As(liveJan), WithID("S-"+sh["kind"].(string))); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ shelf, list, want string }{
		{"S-steel", "same", "bolt nut"},
		{"S-steel", "cheap", "bolt nut cap"},
		{"S-steel", "within", "bolt cap nut"},
		{"S-steel", "newer", "nut ring"},
		{"S-steel", "flagged", "bolt"},
		// STATE() and ID() speak of the listed document, as a bare name does.
		{"S-steel", "moved", "ring"},
		{"S-steel", "all", "bolt cap nut ring"},
		{"S-steel", "others", "S-"},
		// A blank compares as "" and as 0, as in every formula.
		{"S-", "same", "cap"},
		{"S-", "cheap", "cap"},
		{"S-", "within", "cap"},
		{"S-", "others", "S-steel"},
	} {
		v, err := s.Live(ctx, "Shelf", tc.shelf, tc.list, As(liveJan))
		if err != nil {
			t.Errorf("%s of %s: %v", tc.list, tc.shelf, err)
			continue
		}
		if ids(v) != tc.want {
			t.Errorf("%s of %s lists %q; want %q", tc.list, tc.shelf, ids(v), tc.want)
		}
	}
}

// A list that is wrong is refused when its class is defined, at its
// place in the formula.
func TestLiveList_RefusedWhenDefined(t *testing.T) {
	s, ctx := openList(t)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, variable, code, says string
	}{
		{"a class nobody defined", `"l": {"mode": "live", "formula": "QUERY(Nothing, number)"}`, "bad_formula", "unknown source Nothing"},
		{"a table as the source", `"l": {"mode": "live", "formula": "QUERY(rates, code)"}`, "not_supported", "over a table is not built yet"},
		{"a column the source has not", `"l": {"mode": "live", "formula": "QUERY(Invoice, {number, nothing})"}`, "bad_formula", "unknown column nothing of Invoice"},
		{"a column listed twice", `"l": {"mode": "live", "formula": "QUERY(Invoice, {number, NUMBER})"}`, "duplicate", "listed twice"},
		{"a count among the columns", `"l": {"mode": "live", "formula": "QUERY(Invoice, {number, COUNT(net)})"}`, "not_supported", "not built yet"},
		{"a column that is no name", `"l": {"mode": "live", "formula": "QUERY(Invoice, {number, 1 + 2})"}`, "bad_formula", "a column is the name"},
		{"a sort by what the source has not", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, TRUE, {-nothing})"}`, "bad_formula", "cannot sort by nothing"},
		{"a sort by the same twice", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, TRUE, {net, -net})"}`, "duplicate", "sorted by twice"},
		{"a sort that is no name", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, TRUE, {net + 1})"}`, "bad_formula", "what it sorts by is the name"},
		{"a group", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, TRUE, number, number)"}`, "not_supported", "grouping and the limit"},
		{"a condition that is none", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, net)"}`, "type_mismatch", "must be a condition"},
		{"a condition over what the source has not", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, nothing = 1)"}`, "bad_formula", "unknown variable nothing"},
		{"a variable the document has not", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, number = THIS.nothing)"}`, "bad_formula", "the document has no variable nothing"},
		{"a pointer compared with a text", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, order = \"Order:ORD-17\")"}`, "bad_formula", "compares ref with text"},
		{"a lookup in the condition", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, LEN(number) > LOOKUP(rates, rate, code = \"PLN\"))"}`, "not_allowed", "looks nothing up"},
		{"a function of a state in the condition", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, DAYSIN(\"draft\") > 0)"}`, "not_allowed", "a function of a state"},
		{"a live variable of the document", `"m": {"type": "bool", "mode": "live", "formula": "n = USER()"}, "l": {"mode": "live", "formula": "QUERY(Invoice, number, owner = USER() AND THIS.m)"}`, "not_allowed", "m is a live variable"},
		{"a declared type that is no rows", `"l": {"type": "text", "mode": "live", "formula": "QUERY(Invoice, number)"}`, "type_mismatch", "a QUERY answers rows"},
		{"a list that is kept and does not say its columns", `"l": {"mode": "triggered", "on": ["refresh"], "formula": "QUERY(Invoice, number)"}`, "missing", "says its columns"},
		{"a list inside a formula", `"l": {"mode": "live", "formula": "IF(TRUE, QUERY(Invoice, number), BLANK)"}`, "bad_formula", "the whole formula of a live variable"},
		{"a list inside the formula of a triggered variable", `"l": {"type": "number", "mode": "triggered", "on": ["refresh"], "formula": "LEN(n) + QUERY(Invoice, number)"}`, "bad_formula", "the whole formula of a live variable"},
		{"braces where no list stands", `"l": {"mode": "live", "formula": "LEN({n, n})"}`, "bad_formula", "a list in braces stands only"},
		{"the pointer outside a list", `"l": {"mode": "live", "formula": "TEXT(REF())"}`, "bad_formula", "REF() is built for the condition of a QUERY"},
		{"the pointer to another document", `"l": {"mode": "live", "formula": "QUERY(Invoice, number, order = REF(\"Order\", \"ORD-17\"))"}`, "bad_formula", "REF with a class and an id is not built yet"},
	} {
		def := `{"name": "T", "variables": {"n": {"type": "text"}, ` + tc.variable + `}, "states": {"initial": "a", "list": ["a"]}}`
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the class was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s, %q", tc.name, err, tc.code, tc.says)
		}
	}
	// A pointer compares with the pointer to this document only when it
	// points at this class (ADR-0076, item 10).
	_, err := s.DefineClass(ctx, []byte(`{"name": "Customer", "variables": {"n": {"type": "text"},
	  "l": {"mode": "live", "formula": "QUERY(Invoice, number, order = REF())"}}, "states": {"initial": "a", "list": ["a"]}}`), asSetup)
	if err == nil || !strings.Contains(err.Error(), "type_mismatch") || !strings.Contains(err.Error(), "order points at Order, and this document is a Customer") {
		t.Errorf("a list of what points at another class: %v", err)
	}
	// A live variable of the source is kept nowhere: a list cannot show
	// it, sort by it or ask about it, and a LOOKUP cannot read it.
	if _, err := s.DefineClass(ctx, []byte(liveInvoice2), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`QUERY(Memo, mine)`, `QUERY(Memo, number, TRUE, mine)`, `QUERY(Memo, number, mine)`} {
		def := `{"name": "T", "variables": {"n": {"type": "text"}, "l": {"mode": "live", "formula": "` + f + `"}}, "states": {"initial": "a", "list": ["a"]}}`
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err == nil || !strings.Contains(err.Error(), "not_allowed") || !strings.Contains(err.Error(), "mine is a live variable") {
			t.Errorf("%s: %v; want not_allowed, mine is a live variable", f, err)
		}
	}
	def := `{"name": "T", "variables": {"n": {"type": "text"}, "l": {"type": "bool", "mode": "triggered", "on": ["refresh"], "formula": "LOOKUP(Memo, mine, number = THIS.n)"}}, "states": {"initial": "a", "list": ["a"]}}`
	if _, err := s.DefineClass(ctx, []byte(def), asSetup); err == nil || !strings.Contains(err.Error(), "unknown column mine") {
		t.Errorf("a LOOKUP of a live variable: %v; want unknown column mine", err)
	}
}

// The source may not lose what a list of another class names: the
// redefinition is refused, as one that breaks a LOOKUP is.
func TestLiveList_TheSourceKeepsWhatIsListed(t *testing.T) {
	s, ctx := openList(t)
	without := strings.Replace(listInvoice, `"margin": {"type": "money(2)", "label": "Margin"},`, "", 1)
	without = strings.Replace(without, `,
 "rights": {"*": {"margin": {"read": ["finance"], "write": ["finance", "sales"]}}}`, "", 1)
	if _, err := s.DefineClass(ctx, []byte(without), asSetup); err == nil || !strings.Contains(err.Error(), "margin") {
		t.Errorf("an invoice without the margin the order lists: %v; want a refusal that names margin", err)
	}
	// The file opens again with its lists, whichever class is read first.
	path := s.path
	s.Close()
	again, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if v, err := again.Live(ctx, "Order", "ORD-17", "invoices", As(liveEwa)); err != nil || ids(v) != "FV-1 FV-2 FV-3" {
		t.Errorf("after a reopen: %q, %v; want FV-1 FV-2 FV-3", ids(v), err)
	}
}

// A list narrowed by the pointer to its document offers the comparison
// to an index of the pointer, as a filter with the id written in it
// would.
func TestLiveList_SeeksTheIndex(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	indexed := strings.Replace(listInvoice, `"order":  {"type": "ref", "class": "Order", "label": "Order"}`, `"order":  {"type": "ref", "class": "Order", "index": true}`, 1)
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(listOrder), []byte(indexed)}, asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-17"}, As(liveJan), WithID("ORD-17")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"number": "FV", "order": "Order:ORD-17"}, As(liveJan)); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := s.class("Order")
	src, _ := s.class("Invoice")
	cv := c.byName["INVOICES"]
	o := options{principal: liveEwa}
	_, on, _, err := s.liveOn(ctx, c, cv, "ORD-17", o, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.CompileOn(Filter{Class: "Invoice"}, on, src.cls, s.sources(), caller(o, src))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT "id" FROM "Invoice" WHERE `+plan.Where, append(append([]any{}, plan.Args...), s.contextArgs(o)...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var detail string
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		detail += d + "\n"
	}
	if !strings.Contains(detail, "doc_Invoice_order") || strings.Contains(detail, "SCAN") {
		t.Errorf("the invoices of one order are not read through the index on order:\n%s\n%s", detail, plan.Where)
	}
}
