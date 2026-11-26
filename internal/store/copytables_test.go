package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tables in the copy (ADR-0094), against the stand-in: what a feed
// hands over, when, and to whom it leaves a large table. What the copy
// answers is tested in the module analytics, against DuckDB.

const copyStock = `{"name": "Stock",
 "columns": {"sku": "text", "bin": "text", "qty": "number", "price": "decimal(2)", "fresh": "bool"},
 "key": ["sku"],
 "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"warehouse\")"}}`

var copyWanda = As(Principal{ID: "wanda", Groups: []string{"warehouse"}})

// stocked opens a database whose program gave the stand-in for an
// analytical engine, with the table Stock and three rows of it. The
// feed that runs by itself has a period no test reaches.
func stocked(t *testing.T, c *memCopy) (*Store, string, context.Context) {
	t.Helper()
	s, path := openStore(t, Options{Analytics: memEngine{c}, CopyEvery: time.Hour})
	ctx := context.Background()
	<-s.feeder.first
	if _, err := s.DefineTable(ctx, []byte(copyStock), asSetup); err != nil {
		t.Fatal(err)
	}
	stock(t, s, ctx, "A-1 north 5 12.50 true", "A-2 north 7 3.00 false", "B-1 south 2 99.99 true")
	return s, path, ctx
}

// stock imports rows of Stock, each written as its five values.
func stock(t *testing.T, s *Store, ctx context.Context, rows ...string) {
	t.Helper()
	in := make([]Values, len(rows))
	for i, row := range rows {
		f := strings.Fields(row)
		qty, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			t.Fatal(err)
		}
		in[i] = Values{"sku": f[0], "bin": f[1], "qty": qty, "price": f[3], "fresh": f[4] == "true"}
	}
	if _, err := s.ImportRows(ctx, "Stock", in, copyWanda); err != nil {
		t.Fatal(err)
	}
}

// held writes the rows the copy holds of Stock, in the order of their
// keys, as the test writes them.
func (c *memCopy) held(table string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := make([]string, 0, len(c.rows[table]))
	for _, row := range c.rows[table] {
		rows = append(rows, strings.Trim(fmt.Sprint(row), "[]"))
	}
	return strings.Join(rows, "; ")
}

func (c *memCopy) mark(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.marks[name]
}

func (c *memCopy) fed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.feeds
}

// The copy holds the rows of a table as the store keeps them, and is
// made again whenever they change: by an import, by a removal of rows,
// and by nothing else.
func TestCopyTables_HoldTheRowsOfATable(t *testing.T) {
	c := newMemCopy()
	s, _, ctx := stocked(t, c)
	mustFeed(t, s)
	// A decimal is its mantissa, a true or a false is one, and the rows
	// stand under the name of the store's table, in lower case.
	const three = "A-1 north 5 1250 true; A-2 north 7 300 false; B-1 south 2 9999 true"
	if got := c.held("tbl_stock"); got != three {
		t.Fatalf("the copy holds %q", got)
	}
	first := c.mark("table:STOCK")
	if version, token, _ := strings.Cut(first, " "); version != "1" || token == "" || token == copyNoRows {
		t.Fatalf("the mark of the table: %q", first)
	}
	if now := s.feeder.tablesNow()["STOCK"]; !now.current || now.rows != 3 {
		t.Errorf("what the feed found of the table: %+v", now)
	}
	// A feed that finds the table as it was writes nothing.
	feeds := c.fed()
	mustFeed(t, s)
	if c.fed() != feeds {
		t.Errorf("a feed that found nothing new wrote to the copy")
	}
	// A row more, and one changed: the copy of the table is made again,
	// whole.
	stock(t, s, ctx, "A-2 north 9 3.00 false", "C-1 east 1 0.50 false")
	mustFeed(t, s)
	if got := c.held("tbl_stock"); got != "A-1 north 5 1250 true; A-2 north 9 300 false; B-1 south 2 9999 true; C-1 east 1 50 false" {
		t.Errorf("after an import the copy holds %q", got)
	}
	second := c.mark("table:STOCK")
	if second == first || !strings.HasPrefix(second, "1 ") {
		t.Errorf("the mark after an import: %q, and before it %q", second, first)
	}
	// Rows removed.
	if n, err := s.DeleteRows(ctx, "Stock", []Values{{"sku": "A-2"}, {"sku": "C-1"}}, copyWanda); err != nil || n != 2 {
		t.Fatalf("rows removed: %d, %v", n, err)
	}
	mustFeed(t, s)
	if got := c.held("tbl_stock"); got != "A-1 north 5 1250 true; B-1 south 2 9999 true" {
		t.Errorf("after a removal the copy holds %q", got)
	}
	// A removal that removes nothing changes nothing.
	feeds = c.fed()
	if n, err := s.DeleteRows(ctx, "Stock", []Values{{"sku": "Z-9"}}, copyWanda); err != nil || n != 0 {
		t.Fatalf("no row removed: %d, %v", n, err)
	}
	mustFeed(t, s)
	if c.fed() != feeds {
		t.Errorf("a removal of no row made the copy again")
	}
	// The system tables are read where they are, and never copied.
	c.mu.Lock()
	for name := range c.marks {
		if strings.HasPrefix(name, copyMarkTable) && name != "table:STOCK" {
			t.Errorf("the copy has the mark %s", name)
		}
	}
	c.mu.Unlock()
}

// A rule's action on a table changes its rows too, and so does a new
// definition; a table that is taken away leaves the copy.
func TestCopyTables_ARuleADefinitionAndARemoval(t *testing.T) {
	c := newMemCopy()
	s, _, ctx := stocked(t, c)
	mustFeed(t, s)
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Delivery", "variables": {"sku": {"type": "text"}, "bin": {"type": "text"}},
 "states": {"initial": "in", "list": ["in"]},
 "rules": [{"on": "create", "do": [{"action": "table.insert", "table": "Stock", "row": {"sku": "sku", "bin": "bin"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Delivery", Values{"sku": "D-1", "bin": "west"}, copyWanda); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if got := c.held("tbl_stock"); !strings.HasSuffix(got, "; D-1 west <nil> <nil> <nil>") {
		t.Errorf("after a rule wrote a row the copy holds %q", got)
	}
	// The class goes, so that the table may be defined again and taken
	// away; then a column more.
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Delivery", "variables": {"sku": {"type": "text"}, "bin": {"type": "text"}},
 "states": {"initial": "in", "list": ["in"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	wider := strings.Replace(copyStock, `"fresh": "bool"`, `"fresh": "bool", "note": "text"`, 1)
	if _, err := s.DefineTable(ctx, []byte(wider), asSetup); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if got := c.held("tbl_stock"); !strings.HasPrefix(got, "A-1 north 5 1250 true <nil>; ") {
		t.Errorf("after a new definition the copy holds %q", got)
	}
	if mark := c.mark("table:STOCK"); !strings.HasPrefix(mark, "2 ") {
		t.Errorf("the mark after a new definition: %q", mark)
	}
	if err := s.RemoveTable(ctx, "Stock", asSetup); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	if got, mark := c.held("tbl_stock"), c.mark("table:STOCK"); got != "" || mark != "" {
		t.Errorf("after the table was taken away the copy holds %q under the mark %q", got, mark)
	}
	// Defined again, it is copied again.
	if _, err := s.DefineTable(ctx, []byte(copyStock), asSetup); err != nil {
		t.Fatal(err)
	}
	stock(t, s, ctx, "E-1 north 1 1.00 true")
	mustFeed(t, s)
	if got := c.held("tbl_stock"); got != "E-1 north 1 100 true" {
		t.Errorf("the table defined again: the copy holds %q", got)
	}
}

// A program without an analytical engine writes the token too: the next
// one that keeps the copy finds the table changed. And a table whose
// rows were written before any token was is copied, and marked so.
func TestCopyTables_ChangedByAProgramWithoutTheCopy(t *testing.T) {
	c := newMemCopy()
	s, path, ctx := stocked(t, c)
	mustFeed(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	plain, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	stock(t, plain, ctx, "A-1 north 50 12.50 true")
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Analytics: memEngine{c}, CopyEvery: time.Hour, Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	<-again.feeder.first
	if got := c.held("tbl_stock"); !strings.HasPrefix(got, "A-1 north 50 1250 true; ") {
		t.Errorf("after another program changed the table the copy holds %q", got)
	}
	// A database of before this record: its rows have no token.
	if _, err := again.db.ExecContext(ctx, `DELETE FROM metadata WHERE key LIKE 'rows:%'`); err != nil {
		t.Fatal(err)
	}
	mustFeed(t, again)
	if mark := c.mark("table:STOCK"); mark != "1 "+copyNoRows {
		t.Errorf("the mark of a table without a token: %q", mark)
	}
	if got := c.held("tbl_stock"); !strings.HasPrefix(got, "A-1 north 50 1250 true; ") {
		t.Errorf("a table without a token: the copy holds %q", got)
	}
}

// A large table is not copied by the feed of a question, which wakes
// the feed that runs by itself; that feed leaves the table alone for a
// while after it copied it; and the feed of a store that closes begins
// none.
func TestCopyTables_ALargeTable(t *testing.T) {
	small, rest := copySmallTable, copyTableRest
	copySmallTable = 2
	t.Cleanup(func() { copySmallTable, copyTableRest = small, rest })
	c := newMemCopy()
	s, _, ctx := stocked(t, c)
	mustFeed(t, s)
	f := s.feeder
	const three = "A-1 north 5 1250 true; A-2 north 7 300 false; B-1 south 2 9999 true"
	if got := c.held("tbl_stock"); got != three {
		t.Fatalf("the copy holds %q", got)
	}
	// The feed of a question: the table is left as it was, and is known
	// not to be current. The feed that runs by itself is woken, and
	// waits for this one to end.
	stock(t, s, ctx, "C-1 east 1 0.50 false")
	f.mu.Lock()
	if _, err := s.feedOnce(ctx, f, copyCatchUp, feedAsked); err != nil {
		f.mu.Unlock()
		t.Fatal(err)
	}
	got, now := c.held("tbl_stock"), f.tablesNow()["STOCK"]
	// The rest it would take is over before it begins.
	f.tableTook["STOCK"] = 0
	f.mu.Unlock()
	if got != three || now.current {
		t.Errorf("the feed of a question copied a large table: %q, %+v", got, now)
	}
	if _, ready := s.copyReadyTable(ctx, s.tables["STOCK"]); ready {
		// It may be: the woken feed has copied the table already.
		if !f.tablesNow()["STOCK"].current {
			t.Errorf("the copy answers for a table it does not hold as it is")
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.HasSuffix(c.held("tbl_stock"), "; C-1 east 1 50 false") {
		if time.Now().After(deadline) {
			t.Fatalf("the feed that runs by itself was not woken: the copy holds %q", c.held("tbl_stock"))
		}
		time.Sleep(time.Millisecond)
	}
	// It rests: for as long as the test says the last copy took, times
	// copyTableRest, the feed that runs by itself leaves the table, and
	// a feed somebody asks for does not.
	f.mu.Lock()
	f.tableTook["STOCK"] = time.Hour
	f.mu.Unlock()
	stock(t, s, ctx, "D-1 west 4 1.00 true")
	if err := s.feedAs(ctx, 0, feedSelf); err != nil {
		t.Fatal(err)
	}
	if got := c.held("tbl_stock"); strings.Contains(got, "D-1") || f.tablesNow()["STOCK"].current {
		t.Errorf("a large table was copied again while it rests: %q", got)
	}
	if err := s.feedAs(ctx, 0, feedClosing); err != nil {
		t.Fatal(err)
	}
	if got := c.held("tbl_stock"); strings.Contains(got, "D-1") {
		t.Errorf("the feed of a store that closes began a large table: %q", got)
	}
	mustFeed(t, s)
	if got := c.held("tbl_stock"); !strings.HasSuffix(got, "; D-1 west 4 100 true") || !f.tablesNow()["STOCK"].current {
		t.Errorf("a feed that was asked for: the copy holds %q", got)
	}
}

// A question over a table is the store's when the copy does not answer:
// the stand-in answers no statement, and the answers are right.
func TestCopyTables_TheStoreAnswersWhenTheCopyDoesNot(t *testing.T) {
	moment, indexed := copyStoreFirst, 0
	copyStoreFirst = 0
	t.Cleanup(func() { copyStoreFirst = moment })
	c := newMemCopy()
	s, _, ctx := stocked(t, c)
	indexed, s.maxIndexed = s.maxIndexed, 2
	t.Cleanup(func() { s.maxIndexed = indexed })
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Shelf", "title": "bin",
 "variables": {
   "bin":   {"type": "text"},
   "units": {"mode": "live", "formula": "LOOKUP(Stock, SUM(qty), bin = THIS.bin)"},
   "one":   {"mode": "live", "formula": "LOOKUP(Stock, qty, sku = \"B-1\")"},
   "lines": {"mode": "live", "formula": "QUERY(Stock, {sku, qty}, bin = THIS.bin, {-qty})"}
 },
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	shelf, err := s.Create(ctx, "Shelf", Values{"bin": "north"}, copyWanda)
	if err != nil {
		t.Fatal(err)
	}
	mustFeed(t, s)
	for round := 0; round < 2; round++ {
		units, err := s.Live(ctx, "Shelf", shelf.ID, "units", copyWanda)
		if err != nil || units.Value != 12.0 {
			t.Fatalf("the units of the shelf: %+v, %v", units, err)
		}
		if st := s.CopyState(); st.Answers != 0 || st.AskErr == nil || !strings.Contains(st.AskErr.Error(), "answers no statement") {
			t.Errorf("the copy was not asked for the sum: %+v", st)
		}
		one, err := s.Live(ctx, "Shelf", shelf.ID, "one", copyWanda)
		if err != nil || one.Value != 2.0 {
			t.Fatalf("the row found by its key: %+v, %v", one, err)
		}
		lines, err := s.Live(ctx, "Shelf", shelf.ID, "lines", copyWanda)
		if err != nil || len(lines.Rows) != 2 || lines.Rows[0].Values["sku"] != "A-2" || lines.Rows[1].Values["sku"] != "A-1" {
			t.Fatalf("the lines of the shelf: %+v, %v", lines, err)
		}
	}
}
