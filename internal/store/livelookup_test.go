package store

import (
	"context"
	"testing"
)

// A live value that looks something up (ADR-0076, item 5): the reader's
// eyes. The quote says the price of its article from the price list, a
// table, and how many invoices its customer has and what they add up
// to, from the documents of a class.
const lookupQuote = `{
 "name": "Offer",
 "variables": {
   "sku":      {"type": "text"},
   "customer": {"type": "text"},
   "price":    {"type": "money(2)", "mode": "live", "formula": "LOOKUP(Prices, price, sku = THIS.sku)"},
   "articles": {"type": "number", "mode": "live", "formula": "LOOKUP(Prices, COUNT(sku), band = \"gold\")"},
   "invoices": {"type": "number", "mode": "live", "formula": "LOOKUP(Invoice, COUNT(id), owner = THIS.customer)"},
   "invoiced": {"type": "money(2)", "mode": "live", "formula": "LOOKUP(Invoice, SUM(net), owner = THIS.customer)"},
   "first":    {"type": "text", "mode": "live", "formula": "LOOKUP(Invoice, number, owner = THIS.customer)"},
   "margins":  {"type": "money(2)", "mode": "live", "formula": "LOOKUP(Invoice, SUM(margin), owner = THIS.customer)"},
   "both":     {"type": "text", "mode": "live", "formula": "TEXT(LOOKUP(Invoice, COUNT(id), owner = THIS.customer)) & \" of \" & TEXT(LOOKUP(Invoice, COUNT(id), TRUE))"}
 },
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

func openLookup(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, ctx := openList(t) // the orders and the four invoices of the list tests
	if _, err := s.DefineTable(ctx, []byte(tablePrices), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Prices", []Values{
		{"sku": "A-1", "band": "gold", "price": "10.00 PLN"},
		{"sku": "A-2", "band": "gold", "price": "25.50 PLN"},
		{"sku": "B-1", "band": "silver", "price": "3.00 PLN"},
	}, As(tablePia)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(lookupQuote), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Offer", Values{"sku": "A-2", "customer": "jan"}, As(liveJan), WithID("O-1")); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// live reads one live value of the offer for a caller.
func liveOf(t *testing.T, s *Store, variable string, opts ...Option) (any, error) {
	t.Helper()
	v, err := s.Live(context.Background(), "Offer", "O-1", variable, opts...)
	if err != nil {
		return nil, err
	}
	return v.Value, nil
}

// A lookup in a table, for a reader the table's read line lets through.
func TestLiveLookup_ATable(t *testing.T) {
	s, ctx := openLookup(t)
	// Jan is in sales, whom the price list lets read.
	if v, err := liveOf(t, s, "price", As(liveJan)); err != nil || v != "25.50 PLN" {
		t.Errorf("the price of the article: %v, %v", v, err)
	}
	if v, err := liveOf(t, s, "articles", As(liveJan)); err != nil || v != 2.0 {
		t.Errorf("the articles of the band: %v, %v", v, err)
	}
	// Nothing is kept: a new price is the next reader's.
	if _, err := s.ImportRows(ctx, "Prices", []Values{{"sku": "A-2", "band": "gold", "price": "30.00 PLN"}}, As(tablePia)); err != nil {
		t.Fatal(err)
	}
	if v, _ := liveOf(t, s, "price", As(liveJan)); v != "30.00 PLN" {
		t.Errorf("the price after it changed: %v", v)
	}
	// Ewa is in finance, whom the price list does not let read: the
	// value is refused, as the rows of the table would be.
	if _, err := liveOf(t, s, "price", As(liveEwa)); code(err) != CodeForbidden {
		t.Errorf("Ewa reads the price: %v; want forbidden", err)
	}
	// A content administrator passes the line with a reason, which the
	// log of reads keeps.
	if _, err := liveOf(t, s, "price", As(tableOlga)); code(err) != CodeReasonRequired {
		t.Errorf("Olga reads the price and gives no reason: %v; want reason_required", err)
	}
	if v, err := liveOf(t, s, "price", As(tableOlga), Reason("a complaint about a quote")); err != nil || v != "30.00 PLN" {
		t.Errorf("Olga reads the price with a reason: %v, %v", v, err)
	}
	reads, err := s.Reads(ctx, ReadsFilter{Table: "Prices"}, As(Principal{ID: "root", System: []string{GroupAdministrators}}))
	if err != nil || len(reads) != 1 || reads[0].Command != "live" || reads[0].Reason != "a complaint about a quote" {
		t.Errorf("the log of reads: %s, %v", flat(reads), err)
	}
}

// A lookup over the documents of a class sees the documents its reader
// may read, and the values the reader may read in them.
func TestLiveLookup_AClass(t *testing.T) {
	s, _ := openLookup(t)
	// Jan wrote three of the four invoices, and reads those: two of
	// ORD-17 and one of ORD-18, 1,255.00 PLN together.
	for variable, want := range map[string]any{"invoices": 3.0, "invoiced": "1255.00 PLN", "first": "FV-1", "both": "3 of 3"} {
		if v, err := liveOf(t, s, variable, As(liveJan)); err != nil || v != want {
			t.Errorf("Jan reads %s: %v, %v; want %v", variable, v, err, want)
		}
	}
	// Ewa is in finance and reads every invoice: the same condition
	// finds the same three of Jan's, among four.
	for variable, want := range map[string]any{"invoices": 3.0, "both": "3 of 4", "margins": "3.00 PLN"} {
		if v, err := liveOf(t, s, variable, As(liveEwa)); err != nil || v != want {
			t.Errorf("Ewa reads %s: %v, %v; want %v", variable, v, err, want)
		}
	}
	// Piotr reads the one invoice he wrote, which is none of Jan's.
	for variable, want := range map[string]any{"invoices": 0.0, "first": nil, "both": "0 of 1"} {
		if v, err := liveOf(t, s, variable, As(livePiotr)); err != nil || v != want {
			t.Errorf("Piotr reads %s: %v, %v; want %v", variable, v, err, want)
		}
	}
	// The margin is finance's to read: for Jan the sum over it is blank.
	if v, err := liveOf(t, s, "margins", As(liveJan)); err != nil || v != nil {
		t.Errorf("Jan reads the sum of the margins: %v, %v; want blank", v, err)
	}
}
