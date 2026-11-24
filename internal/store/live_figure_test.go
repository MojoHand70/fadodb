package store

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/query"
)

// Acme's order with figures over the list of its invoices (ADR-0081):
// how many there are, what they add up to, and which of them are filed.
// Each figure is a live variable, worked out for whoever reads it.
const figureOrder = `{
 "name": "Order",
 "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "customer": {"type": "text"},
   "owner":    {"type": "text", "default": "USER()"},
   "invoices": {"label": "Invoices", "mode": "live",
                "formula": "QUERY(Invoice, {number, net, state, margin}, order = REF(), {number})"},
   "count":    {"mode": "live", "formula": "COUNT(invoices)"},
   "priced":   {"mode": "live", "formula": "COUNT(invoices.net)"},
   "total":    {"mode": "live", "formula": "SUM(invoices.net)"},
   "largest":  {"mode": "live", "formula": "MAX(invoices.net)"},
   "smallest": {"mode": "live", "formula": "MIN(invoices.net)"},
   "margins":  {"mode": "live", "formula": "SUM(invoices.margin)"},
   "drafts":   {"mode": "live", "formula": "COUNTIF(invoices, state = \"draft\")"},
   "filed":    {"mode": "live", "formula": "SUMIF(invoices, state <> \"draft\", net)"},
   "top":      {"mode": "live", "formula": "MAXIF(invoices, state = \"draft\", net)"},
   "low":      {"mode": "live", "formula": "MINIF(invoices, state = \"draft\", net)"},
   "any":      {"mode": "live", "formula": "ANY(invoices, state = \"review\")"},
   "all":      {"mode": "live", "formula": "ALL(invoices, state = \"draft\")"},
   "mean":     {"mode": "live", "formula": "IF(COUNT(invoices) = 0, BLANK, SUM(invoices.net) / DECIMAL(COUNT(invoices), 0))"},
   "words":    {"mode": "live", "formula": "IF(COUNT(invoices) = 0, \"none\", TEXT(COUNT(invoices)) & \" invoices\")"},
   "mine":     {"mode": "live", "formula": "COUNTIF(invoices, owner = THIS.owner)"},
   "average":  {"mode": "live", "formula": "AVERAGE(invoices.net)"},
   "middle":   {"mode": "live", "formula": "MEDIAN(invoices.net)"},
   "kinds":    {"mode": "live", "formula": "COUNTUNIQUE(invoices.state)"},
   "amounts":  {"mode": "live", "formula": "COUNTUNIQUE(invoices.margin)"},
   "usual":    {"mode": "live", "formula": "AVERAGEIF(invoices, state = \"draft\", net)"},
   "midmargin": {"mode": "live", "formula": "MEDIAN(invoices.margin)"},
   "spread":   {"mode": "live", "formula": "MAX(invoices.net) - MEDIAN(invoices.net)"},
   "bynet":    {"mode": "live", "formula": "QUERY(Invoice, {number, net}, order = REF(), {net})"},
   "bydesc":   {"mode": "live", "formula": "QUERY(Invoice, {number}, order = REF(), {-number})"},
   "first":    {"mode": "live", "formula": "FIRST(invoices.number)"},
   "lastnet":  {"mode": "live", "formula": "LAST(invoices.net)"},
   "second":   {"mode": "live", "formula": "INDEX(invoices.number, 2)"},
   "numbers":  {"mode": "live", "formula": "JOIN(invoices.number, \", \")"},
   "hasfv3":   {"mode": "live", "formula": "CONTAINS(invoices.number, \"FV-3\")"},
   "cheapest": {"mode": "live", "formula": "FIRST(bynet.number)"},
   "dearest":  {"mode": "live", "formula": "LAST(bynet.number)"},
   "bydear":   {"mode": "live", "formula": "JOIN(bynet.number, \" > \")"},
   "newest":   {"mode": "live", "formula": "FIRST(bydesc.number)"},
   "biggest":  {"mode": "live", "formula": "FIRST(SORT(invoices, {-net}).number)"},
   "small2":   {"mode": "live", "formula": "JOIN(TOP(SORT(invoices, {net}), 2).number, \"+\")"},
   "isdraft":  {"mode": "live", "formula": "COUNT(FILTER(invoices, state = \"draft\"))"},
   "states":   {"mode": "live", "formula": "JOIN(UNIQUE(invoices.state), \",\")"},
   "least":    {"mode": "live", "formula": "SUM(TOP(bynet, 1).net)"},
   "latest2":  {"mode": "live", "formula": "JOIN(TOP(bydesc, 2).number, \",\")"}
 },
 "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
 "transitions": [{"name": "close", "from": "open", "to": "closed"}],
 "access": {"read": "INGROUP(\"sales\") OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE"}
}`

// openFigures is openList with the order of figureOrder: ORD-17 has
// FV-1 (1000.00) and FV-2 (250.00), which Jan wrote, and FV-3 (70.00),
// which Piotr wrote; ORD-18 has Jan's FV-4 (5.00). Every margin is 1.00.
func openFigures(t *testing.T) (*Store, func(order, variable string, who Principal) any) {
	t.Helper()
	s, ctx := openList(t)
	if _, err := s.DefineClass(ctx, []byte(figureOrder), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, func(order, variable string, who Principal) any {
		t.Helper()
		v, err := s.Live(ctx, "Order", order, variable, As(who))
		if err != nil {
			t.Fatalf("%s of %s for %s: %v", variable, order, who.ID, err)
		}
		if v.List {
			t.Fatalf("%s of %s is a list; want one value", variable, order)
		}
		return v.Value
	}
}

// A figure over a list counts what its reader may read of the list, as
// the list itself shows it (ADR-0081, item 3).
func TestLiveFigure_ForTheReader(t *testing.T) {
	_, read := openFigures(t)
	for _, tc := range []struct {
		order string
		who   Principal
		want  map[string]any
	}{
		{"ORD-17", liveJan, map[string]any{"count": 2.0, "priced": 2.0, "total": "1250.00 PLN", "largest": "1000.00 PLN", "smallest": "250.00 PLN", "mean": "625.000000 PLN", "words": "2 invoices", "mine": 2.0,
			"average": "625.000000 PLN", "middle": "625.000 PLN", "kinds": 1.0, "usual": "625.000000 PLN", "spread": "375.000 PLN"}},
		{"ORD-17", livePiotr, map[string]any{"count": 1.0, "total": "70.00 PLN", "largest": "70.00 PLN", "smallest": "70.00 PLN", "mean": "70.000000 PLN", "words": "1 invoices", "mine": 0.0,
			"average": "70.000000 PLN", "middle": "70.000 PLN", "kinds": 1.0}},
		{"ORD-17", liveEwa, map[string]any{"count": 3.0, "priced": 3.0, "total": "1320.00 PLN", "largest": "1000.00 PLN", "smallest": "70.00 PLN", "mean": "440.000000 PLN", "words": "3 invoices", "mine": 2.0,
			"average": "440.000000 PLN", "middle": "250.000 PLN", "kinds": 1.0, "amounts": 1.0, "spread": "750.000 PLN"}},
		{"ORD-18", liveJan, map[string]any{"count": 1.0, "total": "5.00 PLN"}},
		// A list with no rows: a count is zero, a sum of money, a least
		// and a greatest are blank, and a formula says what it wants. A
		// mean and a middle value of nothing are blank, and nothing has
		// no different values.
		{"ORD-18", livePiotr, map[string]any{"count": 0.0, "priced": 0.0, "total": nil, "largest": nil, "smallest": nil, "mean": nil, "words": "none", "mine": 0.0, "any": false, "all": true,
			"average": nil, "middle": nil, "kinds": 0.0, "usual": nil, "spread": nil}},
	} {
		for name, want := range tc.want {
			if got := read(tc.order, name, tc.who); got != want {
				t.Errorf("%s of %s for %s = %v (%T); want %v", name, tc.order, tc.who.ID, got, got, want)
			}
		}
	}
}

// A column its reader may read in no state gives a blank, and a column
// they may read counts as the list shows it (ADR-0081, item 3).
func TestLiveFigure_AColumnTheReaderMayNotRead(t *testing.T) {
	_, read := openFigures(t)
	if got := read("ORD-17", "margins", liveEwa); got != "3.00 PLN" {
		t.Errorf("the margins for finance = %v; want 3.00 PLN", got)
	}
	if got := read("ORD-17", "midmargin", liveEwa); got != "1.000 PLN" {
		t.Errorf("the middle margin for finance = %v; want 1.000 PLN", got)
	}
	for _, who := range []Principal{liveJan, livePiotr} {
		for _, name := range []string{"margins", "midmargin", "amounts"} {
			if got := read("ORD-17", name, who); got != nil {
				t.Errorf("%s for %s = %v; want blank: sales may not read a margin", name, who.ID, got)
			}
		}
	}
}

// The figures with a condition, and the two that answer yes or no; each
// follows the documents as they are when it is read, and none is kept.
func TestLiveFigure_WithACondition(t *testing.T) {
	s, read := openFigures(t)
	ctx := t.Context()
	for name, want := range map[string]any{"drafts": 2.0, "filed": nil, "top": "1000.00 PLN", "low": "250.00 PLN", "any": false, "all": true, "kinds": 1.0, "usual": "625.000000 PLN"} {
		if got := read("ORD-17", name, liveJan); got != want {
			t.Errorf("%s with every invoice a draft = %v; want %v", name, got, want)
		}
	}
	if _, err := s.Transition(ctx, "Invoice", "FV-1", "file", nil, As(liveJan)); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]any{"drafts": 1.0, "filed": "1000.00 PLN", "top": "250.00 PLN", "low": "250.00 PLN", "any": true, "all": false, "kinds": 2.0, "usual": "250.000000 PLN", "average": "625.000000 PLN"} {
		if got := read("ORD-17", name, liveJan); got != want {
			t.Errorf("%s after FV-1 was filed = %v; want %v", name, got, want)
		}
	}
	// Ewa sees Piotr's draft too.
	if got := read("ORD-17", "drafts", liveEwa); got != 2.0 {
		t.Errorf("the drafts for finance = %v; want 2", got)
	}
	// Kept nowhere: the order is at the version it was made at, and a
	// read of it answers no figure.
	d, err := s.Get(ctx, "Order", "ORD-17", As(liveJan))
	if err != nil || d.Version != 1 {
		t.Fatalf("the order: %+v, %v; want version 1", d, err)
	}
	for _, name := range []string{"count", "total", "drafts", "any"} {
		if has(d, name) {
			t.Errorf("a read of the order answers %s", name)
		}
	}
}

// Who may not read the order, the list or the class it lists gets no
// figure of it.
func TestLiveFigure_WhoMayRead(t *testing.T) {
	s, _ := openFigures(t)
	ctx := t.Context()
	nobody := Principal{ID: "olaf", Groups: []string{"warehouse"}}
	if _, err := s.Live(ctx, "Order", "ORD-17", "count", As(nobody)); err == nil || err.(*Error).Code != CodeUnknownDocument {
		t.Errorf("a figure of an order its reader may not see: %v; want %s", err, CodeUnknownDocument)
	}
	// A figure has no page: it is one value.
	if _, err := s.LivePage(ctx, LiveRead{Class: "Order", ID: "ORD-17", Variable: "count", Limit: 5}, As(liveJan)); err == nil || err.(*Error).Code != CodeNotAllowed {
		t.Errorf("a figure asked with a limit: %v; want %s", err, CodeNotAllowed)
	}
	// The read right of the list holds for a figure over it.
	guarded := strings.Replace(figureOrder, `"access":`, `"rights": {"*": {"invoices": {"read": ["finance"]}}}, "access":`, 1)
	if _, err := s.DefineClass(ctx, []byte(guarded), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Live(ctx, "Order", "ORD-17", "count", As(liveJan)); err == nil || err.(*Error).Code != CodeForbidden || err.(*Error).Variable != "invoices" {
		t.Errorf("a figure over a list its reader may not read: %v; want %s on invoices", err, CodeForbidden)
	}
	if v, err := s.Live(ctx, "Order", "ORD-17", "count", As(liveEwa)); err != nil || v.Value != 3.0 {
		t.Errorf("the same for finance: %+v, %v; want 3", v, err)
	}
}

// What a figure may be is checked when its class is defined.
func TestLiveFigure_RefusedWhenDefined(t *testing.T) {
	s, ctx := openList(t)
	for _, tc := range []struct {
		name, variable, code, says string
	}{
		{"a figure that is kept", `"x": {"type": "number", "mode": "triggered", "on": ["refresh"], "formula": "COUNT(invoices)"}`, "bad_formula", "stands only in a live variable"},
		{"a figure in a computed value", `"x": {"type": "number", "formula": "COUNT(invoices)"}`, "bad_formula", "stands only in a live variable"},
		{"a sum of a list and no column", `"x": {"mode": "live", "formula": "SUM(invoices)"}`, "bad_formula", "SUM takes a column of the list"},
		{"a column the listed class has not", `"x": {"mode": "live", "formula": "SUM(invoices.nothing)"}`, "bad_formula", "the list invoices has no column nothing"},
		{"a sum of texts", `"x": {"mode": "live", "formula": "SUM(invoices.number)"}`, "bad_formula", "must be a number, got text"},
		{"a condition that is none", `"x": {"mode": "live", "formula": "COUNTIF(invoices, net)"}`, "bad_formula", "must be a condition"},
		{"a condition over what the listed class has not", `"x": {"mode": "live", "formula": "COUNTIF(invoices, nothing = 1)"}`, "bad_formula", "unknown column nothing of Invoice"},
		{"the state of the document in a condition", `"x": {"mode": "live", "formula": "COUNTIF(invoices, STATE() = \"paid\")"}`, "bad_formula", "STATE() is a function of the document the formula belongs to"},
		{"a function of a life in a condition", `"x": {"mode": "live", "formula": "COUNTIF(invoices, CREATEDBY() = USER())"}`, "bad_formula", "a function of a document's life"},
		{"a figure over what is no list", `"x": {"mode": "live", "formula": "COUNTIF(number, TRUE)"}`, "bad_formula", "the first argument is a list of the document"},
		{"a middle value that is kept", `"x": {"type": "money(3)", "formula": "MEDIAN(invoices.net)"}`, "bad_formula", "stands only in a live variable"},
		{"a mean of a list and no column", `"x": {"mode": "live", "formula": "AVERAGE(invoices)"}`, "bad_formula", "AVERAGE takes a column of the list"},
		{"a middle value of a list and no column", `"x": {"mode": "live", "formula": "MEDIAN(invoices)"}`, "bad_formula", "MEDIAN takes a column of the list"},
		{"a middle value of texts", `"x": {"mode": "live", "formula": "MEDIAN(invoices.number)"}`, "bad_formula", "MEDIAN: argument 1 must be a number or an exact amount, got text"},
		{"a mean of texts", `"x": {"mode": "live", "formula": "AVERAGEIF(invoices, TRUE, number)"}`, "bad_formula", "must be a number, got text"},
		{"a middle value of what is no list", `"x": {"mode": "live", "formula": "MEDIAN(number)"}`, "bad_formula", "MEDIAN: the argument is a list of the document"},
		{"different values of what is no list", `"x": {"mode": "live", "formula": "COUNTUNIQUE(customer)"}`, "bad_formula", "in the column position of a LOOKUP"},
		{"a middle value inside a figure", `"x": {"mode": "live", "formula": "COUNTIF(invoices, net > MEDIAN(invoices.net))"}`, "bad_formula", "inside the arguments of another function over rows"},
		{"a mean inside a figure", `"x": {"mode": "live", "formula": "COUNTIF(invoices, net > AVERAGE(invoices.net))"}`, "bad_formula", "inside another's arguments"},
		{"a figure as the place of INDEX", `"x": {"mode": "live", "formula": "INDEX(invoices.number, COUNT(invoices))"}`, "bad_formula", "inside another's arguments"},
		{"a column as the place of INDEX", `"x": {"mode": "live", "formula": "INDEX(invoices.number, invoices.net)"}`, "bad_formula", "the position is a value of the document"},
		{"a place that is a text", `"x": {"mode": "live", "formula": "INDEX(invoices.number, \"two\")"}`, "bad_formula", "the position must be a number"},
		{"a separator that is a number", `"x": {"mode": "live", "formula": "JOIN(invoices.number, 1)"}`, "bad_formula", "the separator must be a text"},
		{"the first of a list and no column", `"x": {"mode": "live", "formula": "FIRST(invoices)"}`, "bad_formula", "FIRST takes a column of the list"},
		{"a value of another type looked for", `"x": {"mode": "live", "formula": "CONTAINS(invoices.number, 3)"}`, "bad_formula", "number"},
		{"the first of a list that is kept", `"x": {"type": "text", "formula": "FIRST(invoices.number)"}`, "bad_formula", "stands only in a live variable"},
		{"a list that stands by itself", `"x": {"mode": "live", "formula": "FILTER(invoices, state = \"draft\")"}`, "bad_formula", "a list stands only inside a function over a list"},
		{"a column of a list that stands by itself", `"x": {"mode": "live", "formula": "FILTER(invoices, state = \"draft\").net"}`, "bad_formula", "a list stands only inside a function over a list"},
		{"a column SORT has not", `"x": {"mode": "live", "formula": "FIRST(SORT(invoices, {nothing}).number)"}`, "bad_formula", "the list invoices has no column nothing"},
		{"SORT without braces", `"x": {"mode": "live", "formula": "FIRST(SORT(invoices, net).number)"}`, "bad_formula", "the columns stand in braces"},
		{"FILTER outside SORT", `"x": {"mode": "live", "formula": "COUNT(FILTER(SORT(invoices, {net}), state = \"draft\"))"}`, "bad_formula", "stands in the wrong place"},
		{"TOP twice", `"x": {"mode": "live", "formula": "COUNT(TOP(TOP(invoices, 3), 2))"}`, "bad_formula", "stands in the wrong place"},
		{"the sum of the different values", `"x": {"mode": "live", "formula": "SUM(UNIQUE(invoices.net))"}`, "bad_formula", "of the different values of a column is not built"},
		{"a column as the count of TOP", `"x": {"mode": "live", "formula": "COUNT(TOP(invoices, invoices.net))"}`, "bad_formula", "the count is a value of the document"},
		{"a count that is a text", `"x": {"mode": "live", "formula": "COUNT(TOP(invoices, \"two\"))"}`, "bad_formula", "the count must be a number"},
		{"JOIN of a narrowed list and no column", `"x": {"mode": "live", "formula": "JOIN(FILTER(invoices, state = \"draft\"), \", \")"}`, "bad_formula", "JOIN takes a column of the list"},
		{"FILTER of a column", `"x": {"mode": "live", "formula": "MAX(FILTER(invoices.net, state = \"draft\"))"}`, "bad_formula", "take the list itself"},
		{"UNIQUE inside FILTER", `"x": {"mode": "live", "formula": "COUNT(FILTER(UNIQUE(invoices.state), TRUE))"}`, "bad_formula", "UNIQUE gives single values"},
		{"UNIQUE of a list and no column", `"x": {"mode": "live", "formula": "COUNT(UNIQUE(invoices))"}`, "bad_formula", "UNIQUE takes a column of the list"},
		{"a narrowed list that is kept", `"x": {"type": "number", "formula": "COUNT(FILTER(invoices, state = \"draft\"))"}`, "bad_formula", "stands only in a live variable"},
		{"JOIN in a LOOKUP", `"x": {"type": "text", "mode": "triggered", "on": ["refresh"], "formula": "LOOKUP(Invoice, JOIN(number, \", \"), order = ID())"}`, "bad_formula", "cannot stand where the column stands"},
		{"a figure with too little", `"x": {"mode": "live", "formula": "SUMIF(invoices, TRUE)"}`, "bad_formula", "SUMIF takes 3 arguments, got 2"},
		{"a figure inside a figure", `"x": {"mode": "live", "formula": "COUNTIF(invoices, net > SUM(invoices.net))"}`, "bad_formula", "inside another's arguments"},
		{"a lookup beside a figure", `"x": {"mode": "live", "formula": "COUNT(invoices) + LOOKUP(Invoice, COUNT(id), TRUE)"}`, "not_supported", "is not built yet (ADR-0076)"},
		{"another live value", `"y": {"mode": "live", "formula": "COUNT(invoices)"}, "x": {"mode": "live", "formula": "y + 1"}`, "not_allowed", "y is a live variable"},
	} {
		def := `{"name": "Order", "title": "number", "variables": {"number": {"type": "text", "required": true}, "customer": {"type": "text"}, "owner": {"type": "text", "default": "USER()"},
		  "invoices": {"mode": "live", "formula": "QUERY(Invoice, {number, net, state, margin}, order = REF(), {number})"}, ` + tc.variable + `},
		  "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]}, "transitions": [{"name": "close", "from": "open", "to": "closed"}]}`
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the class was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s, %q", tc.name, err, tc.code, tc.says)
		}
	}
}

// The listed class may not lose a column a figure names, and a database
// opened again answers the figures as before.
func TestLiveFigure_TheSourceKeepsWhatIsCounted(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := t.Context()
	const invoice = `{"name": "Invoice", "title": "number", "variables": {"number": {"type": "text"}, "order": {"type": "ref", "class": "Order"}, "net": {"type": "money(2)"}, "paid": {"type": "bool"}},
	  "states": {"initial": "draft", "list": ["draft"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`
	const order = `{"name": "Order", "title": "number", "variables": {"number": {"type": "text"},
	  "invoices": {"mode": "live", "formula": "QUERY(Invoice, {number}, order = REF())"},
	  "open": {"mode": "live", "formula": "SUMIF(invoices, NOT(paid), net)"}},
	  "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(order), []byte(invoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	// Neither net nor paid is a column of the list; the figure names
	// both, so Invoice keeps both.
	for _, gone := range []string{`, "net": {"type": "money(2)"}`, `, "paid": {"type": "bool"}`} {
		if _, err := s.DefineClass(ctx, []byte(strings.Replace(invoice, gone, "", 1)), asSetup); err == nil {
			t.Errorf("Invoice was defined anew without %s, which a figure of Order names", gone)
		}
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-1"}, As(liveJan), WithID("ORD-1")); err != nil {
		t.Fatal(err)
	}
	for _, inv := range []Values{{"number": "A", "net": "10.00 PLN", "paid": true}, {"number": "B", "net": "7.50 PLN"}, {"number": "C", "net": "2.50 PLN", "paid": false}} {
		inv["order"] = "Order:ORD-1"
		if _, err := s.Create(ctx, "Invoice", inv, As(liveJan)); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := s.Live(ctx, "Order", "ORD-1", "open", As(liveJan)); err != nil || v.Value != "10.00 PLN" {
		t.Fatalf("what is open of ORD-1: %+v, %v; want 10.00 PLN", v, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if v, err := again.Live(ctx, "Order", "ORD-1", "open", As(liveJan)); err != nil || v.Value != "10.00 PLN" {
		t.Errorf("the same after the database was opened again: %+v, %v", v, err)
	}
}

// A figure is one statement, and it finds the rows of its list as the
// list does: through the index of the pointer (ADR-0081, item 2).
func TestLiveFigure_SeeksTheIndex(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.DefineClasses(ctx, [][]byte{
		[]byte(`{"name": "Order", "title": "number", "variables": {"number": {"type": "text"},
		  "invoices": {"mode": "live", "formula": "QUERY(Invoice, {number}, order = REF())"},
		  "total": {"mode": "live", "formula": "SUM(invoices.net)"}}, "states": {"initial": "open", "list": ["open"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`),
		[]byte(`{"name": "Invoice", "title": "number", "variables": {"number": {"type": "text"}, "order": {"type": "ref", "class": "Order", "index": true}, "net": {"type": "decimal(2)"}},
		  "states": {"initial": "draft", "list": ["draft"]}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`),
	}, asSetup); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"ORD-1", "ORD-2"} {
		if _, err := s.Create(ctx, "Order", Values{"number": o}, As(liveJan), WithID(o)); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 40; i++ {
			if _, err := s.Create(ctx, "Invoice", Values{"number": "FV", "order": "Order:" + o, "net": "1.25"}, As(liveJan)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if v, err := s.Live(ctx, "Order", "ORD-1", "total", As(liveJan)); err != nil || v.Value != "50.00" {
		t.Fatalf("the total of ORD-1: %+v, %v; want 50.00", v, err)
	}
	// The rows of the figure are the rows of the list for the reader:
	// SQLite is asked how it finds them.
	c, _ := s.class("Order")
	src, _ := s.class("Invoice")
	o := options{principal: liveJan}
	_, on, _, err := s.liveOn(ctx, c, c.byName["INVOICES"], "ORD-1", o, nil)
	if err != nil {
		t.Fatal(err)
	}
	read := *on
	read.Reads = []string{"net"}
	plan, err := query.CompileOn(Filter{Class: "Invoice"}, &read, src.cls, s.sources(), caller(o, src))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT SUM("net") FROM (SELECT * FROM "Invoice" WHERE `+plan.Where+`)`, append(append([]any{}, plan.Args...), s.contextArgs(o)...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var how []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		how = append(how, detail)
	}
	if text := strings.Join(how, "; "); !strings.Contains(text, "doc_Invoice_order") || strings.Contains(text, "SCAN") {
		t.Errorf("the figure finds its rows by %q; want the index doc_Invoice_order and no scan", text)
	}
}

// A content administrator reads a figure past the rules only with a
// reason, and the log of reads keeps how many documents only the
// override let them count (ADR-0067, item 3), as for a count.
func TestLiveFigure_PastTheRules(t *testing.T) {
	s, _ := openFigures(t)
	ctx := t.Context()
	if _, err := s.Live(ctx, "Order", "ORD-17", "count", As(liveRoot)); err == nil || err.(*Error).Code != CodeReasonRequired {
		t.Errorf("a content administrator without a reason: %v; want %s", err, CodeReasonRequired)
	}
	v, err := s.Live(ctx, "Order", "ORD-17", "total", As(liveRoot), Reason("audit of November"))
	if err != nil || v.Value != "1320.00 PLN" {
		t.Fatalf("a content administrator with a reason: %+v, %v; want the total of every invoice", v, err)
	}
	// The margin is theirs to read too.
	if v, err := s.Live(ctx, "Order", "ORD-17", "margins", As(liveRoot), Reason("audit of November")); err != nil || v.Value != "3.00 PLN" {
		t.Errorf("the margins for a content administrator with a reason: %+v, %v; want 3.00 PLN", v, err)
	}
	reads, err := s.Reads(ctx, ReadsFilter{Class: "Invoice"}, As(Principal{ID: "adm", System: []string{GroupAdministrators}}))
	if err != nil || len(reads) != 2 {
		t.Fatalf("the log of reads = %+v, %v; want two entries, one for each figure", reads, err)
	}
	for _, r := range reads {
		if r.Command != "live" || r.Reason != "audit of November" || r.Counted == nil || *r.Counted != 3 {
			t.Errorf("the log of reads keeps %+v; want a live read that counted three invoices past the rules", r)
		}
	}
}

// One value out of a list (ADR-0081, step 3): the first and the last row
// by the list's own order, the one at a place, the values joined, and
// whether a value is there, each of the rows the reader may read.
func TestLiveFigure_OneValueOutOfAList(t *testing.T) {
	s, read := openFigures(t)
	for _, tc := range []struct {
		order string
		who   Principal
		want  map[string]any
	}{
		{"ORD-17", liveJan, map[string]any{"first": "FV-1", "lastnet": "250.00 PLN", "second": "FV-2", "numbers": "FV-1, FV-2", "hasfv3": false,
			"cheapest": "FV-2", "dearest": "FV-1", "bydear": "FV-2 > FV-1", "newest": "FV-2",
			"biggest": "FV-1", "small2": "FV-2+FV-1", "isdraft": 2.0, "states": "draft", "least": "250.00 PLN", "latest2": "FV-2,FV-1"}},
		{"ORD-17", livePiotr, map[string]any{"first": "FV-3", "lastnet": "70.00 PLN", "numbers": "FV-3", "hasfv3": true, "dearest": "FV-3", "cheapest": "FV-3", "newest": "FV-3",
			"biggest": "FV-3", "small2": "FV-3", "isdraft": 1.0, "least": "70.00 PLN", "latest2": "FV-3"}},
		// Finance reads all three. The list of the order is by number, one
		// other by the net from the smallest, a third by number from the
		// last: each figure follows the order of its own list, which is
		// none of them the order the invoices were made in.
		{"ORD-17", liveEwa, map[string]any{"first": "FV-1", "lastnet": "70.00 PLN", "second": "FV-2", "numbers": "FV-1, FV-2, FV-3", "hasfv3": true,
			"cheapest": "FV-3", "dearest": "FV-1", "bydear": "FV-3 > FV-2 > FV-1", "newest": "FV-3",
			// a list narrowed, ordered and cut inside the figure; TOP of a
			// list takes its first rows by the list's own order
			"biggest": "FV-1", "small2": "FV-3+FV-2", "isdraft": 3.0, "states": "draft", "least": "70.00 PLN", "latest2": "FV-3,FV-2"}},
		// A list with no rows: nothing is first, nothing is joined.
		{"ORD-18", livePiotr, map[string]any{"first": nil, "lastnet": nil, "numbers": nil, "hasfv3": false, "dearest": nil, "bydear": nil,
			"biggest": nil, "small2": nil, "isdraft": 0.0, "states": nil, "least": nil, "latest2": nil}},
	} {
		for name, want := range tc.want {
			if got := read(tc.order, name, tc.who); got != want {
				t.Errorf("%s of %s for %s = %v (%T); want %v", name, tc.order, tc.who.ID, got, got, want)
			}
		}
	}
	// A place the list does not have for its reader is an error, and no
	// blank: Piotr reads one invoice of the order.
	ctx := t.Context()
	if _, err := s.Live(ctx, "Order", "ORD-17", "second", As(livePiotr)); err == nil || err.(*Error).Code != "out_of_range" {
		t.Errorf("the second of one invoice: %v; want out_of_range", err)
	}
}
