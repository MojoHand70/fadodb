package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
