package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The questions a class asks of a table are answered by the copy when
// they read a large table whole (ADR-0094): a sum over it, a row that no
// index finds, a page of it in an order no index holds. The answer is
// the store's. The test asks each question of two openings of one
// database, one with the engine and one without, for several readers,
// and holds the two to the same answer; it does so again after the
// table changed, by an import, by a removal, and by the opening that
// keeps no copy.

const tablesStock = `{
  "name": "Stock",
  "columns": {"sku": "text", "bin": "text", "qty": "number", "price": "decimal(2)", "fresh": "bool", "since": "date", "cost": "money(2)"},
  "key": ["sku"],
  "indexes": [["since"]],
  "access": {"read": "INGROUP(\"warehouse\")", "write": "INGROUP(\"warehouse\")"}
}`

const tablesShelf = `{
  "name": "Shelf",
  "title": "name",
  "variables": {
    "name":  {"type": "text", "required": true},
    "bin":   {"type": "text"},
    "sku":   {"type": "text"},
    "least": {"type": "number"},
    "total":       {"mode": "live", "formula": "LOOKUP(Stock, SUM(qty), TRUE)"},
    "in_bin":      {"mode": "live", "formula": "LOOKUP(Stock, SUM(qty), bin = THIS.bin)"},
    "rows":        {"mode": "live", "formula": "LOOKUP(Stock, COUNT(sku), qty >= THIS.least)"},
    "bins":        {"mode": "live", "formula": "LOOKUP(Stock, COUNTUNIQUE(bin), TRUE)"},
    "dearest":     {"mode": "live", "formula": "LOOKUP(Stock, MAX(price), fresh)"},
    "mean":        {"mode": "live", "formula": "LOOKUP(Stock, AVERAGE(price), NOT fresh)"},
    "middle":      {"mode": "live", "formula": "LOOKUP(Stock, MEDIAN(qty), TRUE)"},
    "worth":       {"mode": "live", "formula": "LOOKUP(Stock, SUM(cost), fresh)"},
    "newest":      {"mode": "live", "formula": "LOOKUP(Stock, since, bin = THIS.bin AND qty >= THIS.least)"},
    "first_fresh": {"mode": "live", "formula": "LOOKUP(Stock, sku, fresh AND qty >= THIS.least)"},
    "by_key":      {"mode": "live", "formula": "LOOKUP(Stock, qty, sku = THIS.sku)"},
    "of_a_day":    {"mode": "live", "formula": "LOOKUP(Stock, COUNT(sku), since = DATE(2026, 2, 14))"},
    "dear_first":  {"mode": "live", "formula": "QUERY(Stock, {sku, bin, qty, price, cost}, qty >= THIS.least, {-price})"},
    "by_bin":      {"mode": "live", "formula": "QUERY(Stock, {sku, bin, fresh, since}, TRUE, {bin, -qty})"},
    "of_the_bin":  {"mode": "live", "formula": "QUERY(Stock, {sku, qty}, bin = THIS.bin)"}
  },
  "states": {"initial": "open", "list": ["open"]},
  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

// tableFigures are the questions of tablesShelf that are one value, and
// tableKeyed those of them that the key of the table or an index of it
// answers: they are the store's.
var (
	tableFigures = []string{"total", "in_bin", "rows", "bins", "dearest", "mean", "middle", "worth", "newest", "first_fresh", "by_key", "of_a_day"}
	tableKeyed   = map[string]bool{"by_key": true, "of_a_day": true}
)

func TestTables_FromTheCopyAreTheStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(time.Minute); return now }
	// A table of more than ten rows is a large one in this database, so
	// that forty rows are asked of the copy as five million would be.
	withCopy, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, MaxIndexedRows: 10, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer withCopy.Close()
	if _, err := withCopy.DefineTable(ctx, []byte(tablesStock), olga); err != nil {
		t.Fatal(err)
	}
	if _, err := withCopy.DefineClass(ctx, []byte(tablesShelf), olga); err != nil {
		t.Fatal(err)
	}
	wanda, zed := person("wanda", "warehouse"), person("zed")
	root := []fado.Option{fado.As(fado.Principal{ID: "root", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}}), fado.Reason("audit of March")}
	// Some rows have no bin, some no price and some no day: a blank
	// sorts before every value, in the store and in the copy.
	row := func(i, qty int) fado.Values {
		v := fado.Values{"sku": fmt.Sprintf("S-%02d", i), "qty": float64(qty), "fresh": i%3 == 0}
		if i%7 != 3 {
			v["bin"] = []string{"north", "south", "east", "west"}[i%4]
		}
		if i%5 != 2 {
			v["price"] = fmt.Sprintf("%d.%02d", (i%9+1)*3, i%100)
			v["cost"] = fmt.Sprintf("%d.50 PLN", i%6+1)
		}
		if i%6 != 1 {
			v["since"] = []string{"2025-12-31", "2026-06-30", "2026-02-14"}[i%3]
		}
		return v
	}
	var rows []fado.Values
	for i := 0; i < 40; i++ {
		rows = append(rows, row(i, (i*7)%13))
	}
	if _, err := withCopy.ImportRows(ctx, "Stock", rows, wanda); err != nil {
		t.Fatal(err)
	}
	for id, values := range map[string]fado.Values{
		"north": {"name": "The north wall", "bin": "north", "sku": "S-08", "least": 4.0},
		"none":  {"name": "A bin nobody has", "bin": "cellar", "sku": "S-99", "least": 1000.0},
		// No bin, no sku and no least: a blank is the bin of the rows
		// that have none, and a key that is blank finds no row by an
		// index.
		"blank": {"name": "No bin and no sku"},
	} {
		if _, err := withCopy.Create(ctx, "Shelf", values, wanda, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := withCopy.FeedCopy(ctx); err != nil {
		t.Fatal(err)
	}
	// A second opening of the same database, with no engine: the store
	// answers there.
	plain, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()

	said := func(v any, err error) string {
		var refusal *fado.Error
		if errors.As(err, &refusal) {
			return "refused: " + refusal.Code
		}
		if err != nil {
			return "failed: " + err.Error()
		}
		text, _ := json.Marshal(v)
		return string(text)
	}
	// sorted says a list is in an order no index of the table holds:
	// its page is found in the copy at once. Another is the store's for
	// a moment and the copy's after it, so only its answer is held.
	lists := map[string]bool{"dear_first": true, "by_bin": true, "of_the_bin": false}
	same := func(when string) {
		t.Helper()
		for name, who := range map[string][]fado.Option{"wanda, of the warehouse": {wanda}, "zed, whom the table does not let through": {zed}, "a content administrator with a reason": root} {
			reads := name != "zed, whom the table does not let through"
			for _, shelf := range []string{"north", "none", "blank"} {
				for _, variable := range tableFigures {
					before := withCopy.CopyState().Answers
					v, err := withCopy.Live(ctx, "Shelf", shelf, variable, who...)
					got, asked := said(v, err), withCopy.CopyState().Answers-before
					v, err = plain.Live(ctx, "Shelf", shelf, variable, who...)
					if want := said(v, err); got != want {
						t.Errorf("%s, %s reads %s of %s:\nthe copy's program  %s\nthe store's program %s", when, name, variable, shelf, got, want)
					}
					switch {
					case !reads || tableKeyed[variable]:
						if asked != 0 && tableKeyed[variable] && shelf != "blank" {
							t.Errorf("%s, %s reads %s of %s: the copy was asked for a row the key finds", when, name, variable, shelf)
						}
					case asked != 1:
						t.Errorf("%s, %s reads %s of %s: the copy answered %d times, want once (%+v)", when, name, variable, shelf, asked, withCopy.CopyState())
					}
				}
				for list, sorted := range lists {
					for _, read := range []fado.LiveRead{{}, {Limit: 3}, {Limit: 3, Offset: 3}, {Order: []string{"-qty"}, Limit: 4}, {Order: []string{"since", "sku"}, Limit: 50}, {Fields: []string{"sku"}, Limit: 5, Offset: 38}} {
						read.Class, read.ID, read.Variable = "Shelf", shelf, list
						before := withCopy.CopyState().Answers
						v, err := withCopy.LivePage(ctx, read, who...)
						got, asked := said(v, err), withCopy.CopyState().Answers-before
						v, err = plain.LivePage(ctx, read, who...)
						if want := said(v, err); got != want {
							t.Errorf("%s, %s reads the list %s of %s with %+v:\nthe copy's program  %s\nthe store's program %s", when, name, list, shelf, read, got, want)
						}
						// A read that orders the list itself decides
						// whether an index holds the order.
						if reads && (sorted && len(read.Order) == 0 || len(read.Order) > 0 && read.Order[0] == "-qty") && asked != 1 {
							t.Errorf("%s, %s reads the list %s of %s with %+v: the copy answered %d times, want once (%+v)", when, name, list, shelf, read, asked, withCopy.CopyState())
						}
					}
				}
			}
		}
	}
	same("as the table was imported")

	// What the answers are, so that two wrong ones that agree are not
	// taken for right.
	total, err := withCopy.Live(ctx, "Shelf", "north", "total", wanda)
	if want := 0.0; err == nil {
		for i := 0; i < 40; i++ {
			want += float64((i * 7) % 13)
		}
		if total.Value != want {
			t.Errorf("the sum over the table: %v, want %v", total.Value, want)
		}
	} else {
		t.Errorf("the sum over the table: %v", err)
	}
	dear, err := withCopy.LivePage(ctx, fado.LiveRead{Class: "Shelf", ID: "north", Variable: "by_bin", Limit: 2}, wanda)
	if err != nil || len(dear.Rows) != 2 || dear.Rows[0].Values["bin"] != nil || !dear.More {
		t.Errorf("the first rows by their bin, the blank ones first: %v, %s", err, said(dear, nil))
	}

	// The table changes: rows written again and rows more, through the
	// opening that keeps the copy. An answer is never older than its
	// question.
	if _, err := withCopy.ImportRows(ctx, "Stock", []fado.Values{row(3, 500), row(8, 77), row(41, 9), row(42, 1)}, wanda); err != nil {
		t.Fatal(err)
	}
	same("after an import")
	if n, err := withCopy.DeleteRows(ctx, "Stock", []fado.Values{{"sku": "S-00"}, {"sku": "S-41"}, {"sku": "S-13"}}, wanda); err != nil || n != 3 {
		t.Fatalf("rows removed: %d, %v", n, err)
	}
	same("after a removal")
	// And through the opening that keeps none: it writes the token of
	// the rows too, and the other finds the table changed.
	if _, err := plain.ImportRows(ctx, "Stock", []fado.Values{row(50, 11), row(8, 3)}, wanda); err != nil {
		t.Fatal(err)
	}
	same("after the program without the copy changed the table")
	// A new definition, with a column more.
	wider := []byte(`{
  "name": "Stock",
  "columns": {"sku": "text", "bin": "text", "qty": "number", "price": "decimal(2)", "fresh": "bool", "since": "date", "cost": "money(2)", "note": "text"},
  "key": ["sku"],
  "indexes": [["since"]],
  "access": {"read": "INGROUP(\"warehouse\")", "write": "INGROUP(\"warehouse\")"}
}`)
	if _, err := withCopy.DefineTable(ctx, wider, olga); err != nil {
		t.Fatal(err)
	}
	if st := withCopy.CopyState(); st.Err != nil {
		t.Errorf("the copy after the table was defined again: %+v", st)
	}
	before := withCopy.CopyState().Answers
	if v, err := withCopy.Live(ctx, "Shelf", "north", "total", wanda); err != nil || withCopy.CopyState().Answers != before+1 {
		t.Errorf("the sum after the table was defined again: %v, %v, %+v", v, err, withCopy.CopyState())
	}
}
