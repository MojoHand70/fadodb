package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The engine keeps its own indexes (ADR-0075, item 2). Acme's invoices
// again: an invoice points at its order, a designer asked for an index
// on the customer, and the read rule lets a seller read what he wrote.
const ownInvoice = `{
 "name": "Invoice", "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "order":    {"type": "ref", "class": "Order"},
   "customer": {"type": "text", "index": true},
   "note":     {"type": "text"},
   "owner":    {"type": "text", "default": "USER()"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
 "transitions": [{"name": "file", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE",
            "transition": {"file": "owner = USER()", "approve": "INGROUP(\"finance\")"}}
}`

const ownOrder = `{
 "name": "Order", "title": "number",
 "variables": {"number": {"type": "text", "required": true}},
 "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}
}`

// indexesOf lists the indexes of a table, each with the columns it is
// on, in the order of their names.
func indexesOf(t testing.TB, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT name, sql FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL", table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			t.Fatal(err)
		}
		_, on, _ := strings.Cut(ddl, " ON "+quote(table)+" ")
		out = append(out, strings.TrimPrefix(name, table+"_")+" "+strings.ReplaceAll(on, `"`, ""))
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

const ownIndexes = "created_at (created_at); customer (customer, created_at); order (order, created_at); owner (owner, created_at); state (state, created_at); updated_at (updated_at)"

// Every class has an index on the state and on its two moments; a
// pointer has one without a word of the designer's, and so has the text
// the read rule compares with the caller. Each goes on to the moment a
// document was made.
func TestIndexes_WhatAClassHas(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(ownOrder), []byte(ownInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	if got := indexesOf(t, s.db, "doc_Invoice"); got != ownIndexes {
		t.Errorf("the indexes of Invoice:\n %s\nwant\n %s", got, ownIndexes)
	}
	if got, want := indexesOf(t, s.db, "doc_Order"), "created_at (created_at); state (state, created_at); updated_at (updated_at)"; got != want {
		t.Errorf("the indexes of Order:\n %s\nwant\n %s", got, want)
	}
	c, _ := s.class("Invoice")
	var keyed []string
	for _, v := range c.cls.Variables {
		if c.cls.Keyed(v) {
			keyed = append(keyed, v.Name)
		}
	}
	if got := strings.Join(keyed, " "); got != "order customer owner" {
		t.Errorf("the class keeps an index on %q; want order, customer and owner", got)
	}
	if got := strings.Join(c.cls.Source().Indexed, " "); got != "id order customer owner" {
		t.Errorf("a lookup may take %q for its key; want the id and the three", got)
	}
}

// The indexes follow the definition: a pointer a new version adds gets
// one, and the one a designer no longer asks for is dropped.
func TestIndexes_FollowTheDefinition(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(ownOrder), []byte(ownInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	next := strings.NewReplacer(`"customer": {"type": "text", "index": true}`, `"customer": {"type": "text"}, "second": {"type": "ref", "class": "Order"}`).Replace(ownInvoice)
	if _, err := s.DefineClass(ctx, []byte(next), asSetup); err != nil {
		t.Fatal(err)
	}
	want := "created_at (created_at); order (order, created_at); owner (owner, created_at); second (second, created_at); state (state, created_at); updated_at (updated_at)"
	if got := indexesOf(t, s.db, "doc_Invoice"); got != want {
		t.Errorf("the indexes of the second version of Invoice:\n %s\nwant\n %s", got, want)
	}
	// A read rule that no longer compares the owner with the caller
	// leaves no reason for the index on the owner.
	third := strings.NewReplacer(`"read": "owner = USER() OR INGROUP(\"finance\")"`, `"read": "INGROUP(\"sales\") OR INGROUP(\"finance\")"`).Replace(next)
	if _, err := s.DefineClass(ctx, []byte(third), asSetup); err != nil {
		t.Fatal(err)
	}
	want = "created_at (created_at); order (order, created_at); second (second, created_at); state (state, created_at); updated_at (updated_at)"
	if got := indexesOf(t, s.db, "doc_Invoice"); got != want {
		t.Errorf("the indexes of the third version of Invoice:\n %s\nwant\n %s", got, want)
	}
}

// A database an earlier engine made is brought over when it is opened:
// the indexes it has not are made, the ones laid out the earlier way are
// made again, and its documents stay as they were.
func TestIndexes_ADatabaseOfAnEarlierSchema(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(ownOrder), []byte(ownInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-1"}, As(liveJan), WithID("ORD-1")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := s.Create(ctx, "Invoice", Values{"number": fmt.Sprintf("FV-%d", i), "order": "Order:ORD-1", "customer": "Globex"}, As(liveJan)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// The file as v0.7.0 left it: schema 14, one index, on the customer
	// alone.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`DROP INDEX "doc_Invoice_state"`, `DROP INDEX "doc_Invoice_created_at"`, `DROP INDEX "doc_Invoice_updated_at"`,
		`DROP INDEX "doc_Invoice_order"`, `DROP INDEX "doc_Invoice_owner"`, `DROP INDEX "doc_Invoice_customer"`,
		`DROP INDEX "doc_Order_state"`, `DROP INDEX "doc_Order_created_at"`, `DROP INDEX "doc_Order_updated_at"`,
		`CREATE INDEX "doc_Invoice_customer" ON "doc_Invoice" ("customer")`,
		`UPDATE schema_version SET version = 14`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	raw.Close()
	again, err := Open(filepath.Clean(path), Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("open a database of schema 14: %v", err)
	}
	defer again.Close()
	if got := indexesOf(t, again.db, "doc_Invoice"); got != ownIndexes {
		t.Errorf("the indexes of Invoice after the database was brought over:\n %s\nwant\n %s", got, ownIndexes)
	}
	var version int
	if err := again.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil || version != engineSchema {
		t.Errorf("the schema of the database is %d, %v; want %d", version, err, engineSchema)
	}
	if n, err := again.Count(ctx, Filter{Class: "Invoice", Where: `order = REF("Order", "ORD-1")`}, As(liveJan)); err != nil || n != 20 {
		t.Errorf("the invoices of the order after the database was brought over: %d, %v", n, err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	// A second opening finds them all, and makes none.
	raw, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var before int
	raw.QueryRow("SELECT max(rootpage) FROM sqlite_master").Scan(&before)
	third, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	third.Close()
	var after int
	raw.QueryRow("SELECT max(rootpage) FROM sqlite_master").Scan(&after)
	if before != after {
		t.Errorf("a second opening made an index again: the last table or index began at page %d and now at %d", before, after)
	}
}

// The test the record sets before any index of its own is added (item
// 10): with every index in place, the drafts of one order are asked
// through the pointer, though the index on the state fits them too, and
// the few invoices that wait are asked through the state, though the
// index on the customer fits.
func TestIndexes_NoneMakesAnotherQuestionSlow(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(ownOrder), []byte(ownInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	for o := 0; o < 30; o++ {
		id := fmt.Sprintf("ORD-%d", o)
		if _, err := s.Create(ctx, "Order", Values{"number": id}, As(liveJan), WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	batch := make([]Values, 0, 2400)
	for k := 0; k < 2400; k++ {
		customer := "Globex"
		if (k/30)%2 == 1 {
			customer = fmt.Sprintf("C-%d", k%10)
		}
		batch = append(batch, Values{"number": fmt.Sprintf("FV-%04d", k), "order": fmt.Sprintf("Order:ORD-%d", k%30), "customer": customer})
	}
	made, err := s.CreateMany(ctx, "Invoice", batch, As(liveJan))
	if err != nil {
		t.Fatal(err)
	}
	// Nine in ten are paid, one in ten stays a draft, and five of
	// Globex's wait in review.
	var filed, approved []string
	for k, d := range made {
		if (k/30)%10 == 3 {
			continue // a draft: eight of the eighty of each order
		}
		filed = append(filed, d.ID)
		if k%480 != 0 {
			approved = append(approved, d.ID)
		}
	}
	for _, move := range []struct {
		name string
		ids  []string
		who  Principal
	}{{"file", filed, liveJan}, {"approve", approved, liveEwa}} {
		for len(move.ids) > 0 {
			n := min(1000, len(move.ids))
			outcomes, err := s.TransitionMany(ctx, "Invoice", move.name, move.ids[:n], nil, As(move.who))
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range outcomes {
				if o.Refusal != nil {
					t.Fatalf("%s: %v", move.name, o.Refusal)
				}
			}
			move.ids = move.ids[n:]
		}
	}
	for _, tc := range []struct {
		name, where, column string
		found               int64
	}{
		{"the drafts of one order", `order = REF("Order", "ORD-13") AND state = "draft"`, "order", 8},
		{"the same, the state first", `STATE() = "draft" AND order = REF("Order", "ORD-13")`, "order", 8},
		{"Globex's invoices in review", `customer = "Globex" AND state = "review"`, "state", 5},
		{"the paid invoices of a small customer", `state = "paid" AND customer = "C-7"`, "customer", 96},
	} {
		column, how := taken(t, s, tc.where, liveEwa)
		if column != tc.column || !strings.Contains(how, "doc_Invoice_"+tc.column) || strings.Contains(how, "SCAN") {
			t.Errorf("%s: asked through %q; want the index on %s\n%s", tc.name, column, tc.column, how)
		}
		if n, err := s.Count(ctx, Filter{Class: "Invoice", Where: tc.where}, As(liveEwa)); err != nil || n != tc.found {
			t.Errorf("%s: counts %d, %v; want %d", tc.name, n, err, tc.found)
		}
	}
	// The newest of a state are read off the end of its index: nothing
	// is sorted.
	c, _ := s.class("Invoice")
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT "id" FROM `+quote(c.table)+` WHERE "state" = 'draft' ORDER BY "created_at" DESC LIMIT 50`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var how string
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		how += d + "\n"
	}
	if !strings.Contains(how, "doc_Invoice_state") || strings.Contains(how, "TEMP B-TREE") {
		t.Errorf("the newest drafts are not read off the index on the state:\n%s", how)
	}
}
