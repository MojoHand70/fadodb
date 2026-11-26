package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// A LOOKUP of a live formula that is keyed by the key of a table, or by
// a column that leads an index of it, finds its rows through the index,
// as the lookup of a commit does (ADR-0036, ADR-0094): the store does
// not read the table for it. A blank key, which a blank row equals too,
// and a column no index leads, are answered as before.
func TestLiveLookup_ByAKeyIsFoundThroughTheIndex(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Stock",
 "columns": {"sku": "text", "bin": "text", "qty": "number", "price": "decimal(2)", "fresh": "bool"},
 "key": ["sku"], "indexes": [["bin"], ["price"]],
 "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"warehouse\")"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Stock", []Values{
		{"sku": "A-1", "bin": "north", "qty": 5.0, "price": "12.50", "fresh": true},
		{"sku": "A-2", "bin": "north", "qty": 7.0, "price": "3.00", "fresh": false},
		{"sku": "B-1", "bin": "south", "qty": 2.0, "price": "12.50", "fresh": true},
		{"sku": "C-1", "qty": 9.0, "fresh": true},
		{"sku": "C-2", "qty": 1.0, "price": "0.00"},
	}, copyWanda); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Shelf", "title": "name",
 "variables": {
   "name":  {"type": "text"},
   "bin":   {"type": "text"},
   "sku":   {"type": "text"},
   "price": {"type": "decimal", "scale": 2},
   "one":       {"mode": "live", "formula": "LOOKUP(Stock, qty, sku = THIS.sku)"},
   "units":     {"mode": "live", "formula": "LOOKUP(Stock, SUM(qty), bin = THIS.bin)"},
   "a_fresh":   {"mode": "live", "formula": "LOOKUP(Stock, sku, bin = THIS.bin AND fresh)"},
   "at_price":  {"mode": "live", "formula": "LOOKUP(Stock, COUNT(sku), price = THIS.price)"},
   "by_amount": {"mode": "live", "formula": "LOOKUP(Stock, sku, qty = 9)"}
 },
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for id, values := range map[string]Values{
		"north": {"name": "The north wall", "bin": "north", "sku": "A-2", "price": "12.50"},
		"blank": {"name": "No bin, no sku, no price"},
		"zero":  {"name": "A price of nothing", "bin": "cellar", "sku": "Z-9", "price": "0.00"},
	} {
		if _, err := s.Create(ctx, "Shelf", values, copyWanda, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		shelf, variable string
		want            any
	}{
		{"north", "one", 7.0}, {"north", "units", 12.0}, {"north", "a_fresh", "A-1"}, {"north", "at_price", 2.0}, {"north", "by_amount", "C-1"},
		// A blank bin is the bin of the rows that have none; a blank
		// price equals a price of nothing, and the other way round.
		{"blank", "one", nil}, {"blank", "units", 10.0}, {"blank", "a_fresh", "C-1"}, {"blank", "at_price", 2.0},
		{"zero", "one", nil}, {"zero", "units", 0.0}, {"zero", "a_fresh", nil}, {"zero", "at_price", 2.0},
	} {
		got, err := s.Live(ctx, "Shelf", c.shelf, c.variable, copyWanda)
		if err != nil || got.Value != c.want {
			t.Errorf("%s of the shelf %s: %v, %v; want %v", c.variable, c.shelf, got, err, c.want)
		}
	}
	// How SQLite runs the lookups that were narrowed to their key: each
	// through an index, none by reading the table.
	s.filters.mu.Lock()
	var narrowed []string
	for text := range s.filters.stmts {
		if strings.Contains(text, ":fak") {
			narrowed = append(narrowed, text)
		}
	}
	s.filters.mu.Unlock()
	// The key, the bin with a column of the first row and with a total,
	// and the price: four statements. The lookup by an amount, which no
	// index leads, is not among them.
	if len(narrowed) != 4 {
		t.Fatalf("%d statements were narrowed to a key, want 4: %q", len(narrowed), narrowed)
	}
	for _, text := range narrowed {
		args := []any{sql.Named("fak", "north"), sql.Named("fa0", "north")}
		steps, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+text, args...)
		if err != nil {
			t.Fatalf("how SQLite runs %s: %v", text, err)
		}
		how := ""
		for steps.Next() {
			var id, parent, unused int
			var detail string
			if err := steps.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			how += detail + "; "
		}
		steps.Close()
		if !strings.Contains(how, "SEARCH tbl_Stock USING") || strings.Contains(how, "SCAN tbl_Stock") {
			t.Errorf("a lookup narrowed to its key runs as: %s\n%s", how, text)
		}
	}
}

// A table that holds more rows than the store keeps in memory is still
// over the limit after rows were added or changed, so the lookup of the
// next commit does not read it up to the limit again to find that out
// (ADR-0036, ADR-0094). After rows may have gone, it looks again.
func TestTable_OverTheLimitStaysSoUntilRowsGo(t *testing.T) {
	s, _ := openStore(t, Options{MaxIndexedRows: 3})
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(copyStock), asSetup); err != nil {
		t.Fatal(err)
	}
	stock(t, s, ctx, "A-1 north 5 12.50 true", "A-2 north 7 3.00 false", "B-1 south 2 99.99 true", "C-1 east 1 0.50 false")
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Line", "variables": {"sku": {"type": "text"}, "bin": {"type": "text"},
   "price": {"formula": "LOOKUP(Stock, price, sku = THIS.sku)"}},
 "states": {"initial": "in", "list": ["in"]},
 "rules": [{"on": "create", "if": "bin <> \"\"", "do": [{"action": "table.update", "table": "Stock", "key": {"sku": "sku"}, "set": {"bin": "bin"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	tbl := s.tables["STOCK"]
	line := func(sku, bin, want string) {
		t.Helper()
		doc, err := s.Create(ctx, "Line", Values{"sku": sku, "bin": bin}, copyWanda)
		if err != nil || doc.Values["price"] != want {
			t.Fatalf("a line of %s: %v, %v; want the price %s", sku, doc, err, want)
		}
		if _, err := s.Drain(ctx, asSetup); err != nil {
			t.Fatal(err)
		}
	}
	line("B-1", "", "99.99")
	if !tbl.tooBig || tbl.cache != nil {
		t.Fatalf("four rows against a limit of three: over the limit %v, in memory %v", tbl.tooBig, tbl.cache != nil)
	}
	// Rows added, a row written again, and a rule's update: none can
	// have made the table smaller.
	stock(t, s, ctx, "D-1 west 4 1.00 true", "A-1 north 5 13.00 true")
	if !tbl.tooBig {
		t.Errorf("after an import the store has forgotten that the table is over the limit")
	}
	line("A-1", "attic", "13.00")
	if !tbl.tooBig {
		t.Errorf("after a rule's update the store has forgotten that the table is over the limit")
	}
	// Rows removed: it looks again, and the table now fits.
	if n, err := s.DeleteRows(ctx, "Stock", []Values{{"sku": "D-1"}, {"sku": "C-1"}}, copyWanda); err != nil || n != 2 {
		t.Fatalf("rows removed: %d, %v", n, err)
	}
	if tbl.tooBig {
		t.Errorf("after a removal the store still takes the table to be over the limit")
	}
	line("A-2", "", "3.00")
	if tbl.tooBig || tbl.cache == nil {
		t.Errorf("three rows against a limit of three: over the limit %v, in memory %v", tbl.tooBig, tbl.cache != nil)
	}
}
