package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Acme's price list and a quote that lists the prices of its band
// (ADR-0076, item 9): a QUERY whose source is a table. prices is asked
// whenever somebody reads it; agreed is kept with the quote when it is
// sent, and the quote adds it up.
const tablePrices = `{
 "name": "Prices",
 "columns": {"sku": {"type": "text", "label": "Article"}, "band": "text", "price": {"type": "money(2)", "label": "Price"}},
 "key": ["sku"],
 "access": {"read": "INGROUP(\"sales\")", "write": "INGROUP(\"pricing\")"}
}`

const tableQuote = `{
 "name": "Quote",
 "variables": {
   "customer": {"type": "text"},
   "band":     {"type": "text"},
   "prices":   {"mode": "live", "formula": "QUERY(Prices, {sku, price}, band = THIS.band, {-price})"},
   "agreed":   {"type": "rows", "schema": {"sku": "text", "price": "money(2)"},
                "formula": "QUERY(Prices, {sku, price}, band = THIS.band)",
                "mode": "triggered", "on": ["before:send", "refresh"]},
   "dearest":  {"formula": "MAX(agreed.price)"}
 },
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent", "guard": "COUNT(agreed) > 0"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

var (
	tableSam  = Principal{ID: "sam", Kind: ActorPerson, Groups: []string{"sales"}}
	tablePia  = Principal{ID: "pia", Kind: ActorPerson, Groups: []string{"pricing"}}
	tableGus  = Principal{ID: "gus", Kind: ActorPerson}
	tableOlga = Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupContentAdministrators}}
)

func openTableList(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(tablePrices), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Prices", []Values{
		{"sku": "A-1", "band": "gold", "price": "10.00 PLN"},
		{"sku": "A-2", "band": "gold", "price": "25.50 PLN"},
		{"sku": "A-3", "band": "gold", "price": "7.25 PLN"},
		{"sku": "B-1", "band": "silver", "price": "3.00 PLN"},
	}, As(tablePia)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(tableQuote), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Quote", Values{"customer": "Globex", "band": "gold"}, As(tableSam), WithID("Q-1")); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// A live list over a table: the rows the condition takes, in the order
// asked, a page at a time; the rows are no documents and carry no id.
func TestTableList_Live(t *testing.T) {
	s, ctx := openTableList(t)
	v, err := s.Live(ctx, "Quote", "Q-1", "prices", As(tableSam))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := json.Marshal(v)
	const want = `{"columns":[{"name":"sku","type":"text","label":"Article"},{"name":"price","type":"money(2)","label":"Price"}],` +
		`"rows":[{"sku":"A-2","price":"25.50 PLN"},{"sku":"A-1","price":"10.00 PLN"},{"sku":"A-3","price":"7.25 PLN"}],"more":false}`
	if string(text) != want {
		t.Errorf("the list of prices is\n%s\nwant\n%s", text, want)
	}
	// The reader says the page, the order and the columns.
	page, err := s.LivePage(ctx, LiveRead{Class: "Quote", ID: "Q-1", Variable: "prices", Order: []string{"sku"}, Limit: 2, Fields: []string{"SKU"}}, As(tableSam))
	if text, _ := json.Marshal(page); err != nil || string(text) != `{"columns":[{"name":"sku","type":"text","label":"Article"}],"rows":[{"sku":"A-1"},{"sku":"A-2"}],"more":true}` {
		t.Errorf("the first page by article: %s, %v", text, err)
	}
	page, err = s.LivePage(ctx, LiveRead{Class: "Quote", ID: "Q-1", Variable: "prices", Order: []string{"sku"}, Limit: 2, Offset: 2}, As(tableSam))
	if text, _ := json.Marshal(page.Rows); err != nil || string(text) != `[{"sku":"A-3","price":"7.25 PLN"}]` || page.More {
		t.Errorf("the second page by article: %s, %v", text, err)
	}
	// Nothing is kept: a new price is there for the next reader.
	if _, err := s.ImportRows(ctx, "Prices", []Values{{"sku": "A-4", "band": "gold", "price": "99.00 PLN"}}, As(tablePia)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Live(ctx, "Quote", "Q-1", "prices", As(tableSam)); len(v.Rows) != 4 || v.Rows[0].Values["sku"] != "A-4" {
		t.Errorf("after a new price the list is %s", flat(v.Rows))
	}
	// The condition reads the quote: another band, another list.
	if _, err := s.Set(ctx, "Quote", "Q-1", Values{"band": "silver"}, As(tableSam)); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Live(ctx, "Quote", "Q-1", "prices", As(tableSam)); len(v.Rows) != 1 || v.Rows[0].Values["sku"] != "B-1" {
		t.Errorf("the list of another band is %s", flat(v.Rows))
	}
}

// A live list is read with its reader's eyes: the read line of the
// table is asked of whoever reads the list, as table_rows asks it.
func TestTableList_WhoMayRead(t *testing.T) {
	s, ctx := openTableList(t)
	// Gus may read the quote and not the table.
	if _, err := s.Live(ctx, "Quote", "Q-1", "prices", As(tableGus)); code(err) != CodeForbidden {
		t.Errorf("Gus reads the list of prices: %v; want forbidden", err)
	}
	// A content administrator passes the line with a reason, and the log
	// of reads keeps it.
	if _, err := s.Live(ctx, "Quote", "Q-1", "prices", As(tableOlga)); code(err) != CodeReasonRequired {
		t.Errorf("Olga reads the list and gives no reason: %v; want reason_required", err)
	}
	v, err := s.Live(ctx, "Quote", "Q-1", "prices", As(tableOlga), Reason("a customer asked what they were quoted"))
	if err != nil || len(v.Rows) != 3 {
		t.Fatalf("Olga reads the list with a reason: %s, %v", flat(v), err)
	}
	reads, err := s.Reads(ctx, ReadsFilter{Table: "Prices"}, As(Principal{ID: "root", System: []string{GroupAdministrators}}))
	if err != nil || len(reads) != 1 || reads[0].Command != "live" || reads[0].Reason != "a customer asked what they were quoted" {
		t.Errorf("the log of reads: %s, %v", flat(reads), err)
	}
}

// A list over a table that is kept: brought before the quote is sent,
// with the engine's eyes, and held by the version the sending makes.
func TestTableList_Kept(t *testing.T) {
	s, ctx := openTableList(t)
	// Gus may not read the table, and sends the quote all the same: the
	// list is brought for the document, one list for everybody.
	d, err := s.Transition(ctx, "Quote", "Q-1", "send", nil, As(tableGus))
	if err != nil || d.State != "sent" || d.Values["dearest"] != "25.50 PLN" ||
		flat(d.Values["agreed"]) != `[{"price":"10.00 PLN","sku":"A-1"},{"price":"25.50 PLN","sku":"A-2"},{"price":"7.25 PLN","sku":"A-3"}]` {
		t.Fatalf("the quote sent: %s, %v", flat(d), err)
	}
	// A price changes afterwards: the quote holds what was agreed.
	if _, err := s.ImportRows(ctx, "Prices", []Values{{"sku": "A-2", "band": "gold", "price": "30.00 PLN"}}, As(tablePia)); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Get(ctx, "Quote", "Q-1", As(tableSam)); d.Values["dearest"] != "25.50 PLN" {
		t.Errorf("after the price changed the quote holds %s", flat(d.Values))
	}
	// A refresh brings it again.
	if d, err := s.Refresh(ctx, "Quote", "Q-1", nil, As(tableSam)); err != nil || d.Values["dearest"] != "30.00 PLN" {
		t.Errorf("a refresh of the quote: %s, %v", flat(d), err)
	}
	// A quote of a band with no prices is not sent.
	if _, err := s.Create(ctx, "Quote", Values{"band": "bronze"}, As(tableSam), WithID("Q-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Quote", "Q-2", "send", nil, As(tableSam)); code(err) != CodeGuardFalse {
		t.Errorf("a quote with no prices: %v; want guard_false", err)
	}
}

// What is refused when the class is defined, and the table a list reads
// is not taken away under it.
func TestTableList_Refused(t *testing.T) {
	s, ctx := openTableList(t)
	for _, tc := range []struct{ name, variable, code, says string }{
		{"a column the table has not", `"l": {"mode": "live", "formula": "QUERY(Prices, {sku, nothing})"}`, "bad_formula", "unknown column nothing of the table Prices"},
		{"a column listed twice", `"l": {"mode": "live", "formula": "QUERY(Prices, {sku, SKU})"}`, "duplicate", "listed twice"},
		{"a sort by what the table has not", `"l": {"mode": "live", "formula": "QUERY(Prices, sku, TRUE, {-nothing})"}`, "bad_formula", "cannot sort by nothing"},
		{"a condition over what the table has not", `"l": {"mode": "live", "formula": "QUERY(Prices, sku, nothing = 1)"}`, "bad_formula", "nothing"},
		{"a condition that is none", `"l": {"mode": "live", "formula": "QUERY(Prices, sku, band)"}`, "type_mismatch", "must be a condition"},
		{"a lookup in the condition", `"l": {"mode": "live", "formula": "QUERY(Prices, sku, band = LOOKUP(Prices, band, sku = \"A-1\"))"}`, "not_allowed", "looks nothing up"},
		{"a system column of documents", `"l": {"mode": "live", "formula": "QUERY(Prices, {sku, state})"}`, "bad_formula", "unknown column state of the table Prices"},
		{"a kept list whose schema differs", `"l": {"type": "rows", "schema": {"sku": "text", "price": "number"}, "formula": "QUERY(Prices, {sku, price})", "mode": "triggered", "on": ["refresh"]}`, "type_mismatch", "money(2) in Prices"},
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
	if err := s.RemoveTable(ctx, "Prices", asSetup); err == nil {
		t.Error("the table was removed while a class lists it")
	}
	// Nor does the table lose a column the list names.
	without := strings.Replace(tablePrices, `, "price": {"type": "money(2)", "label": "Price"}`, "", 1)
	if without == tablePrices {
		t.Fatal("the table of the test has no price to take away")
	}
	if _, err := s.DefineTable(ctx, []byte(without), asSetup); err == nil || !strings.Contains(err.Error(), "the class Quote uses a column") {
		t.Errorf("a table that would lose a column a class lists: %v", err)
	}
}
