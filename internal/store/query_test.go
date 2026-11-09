package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// seedInvoices creates a handful of invoices with varied values and
// deletes one, for the query tests. It returns the ids in creation order.
func seedInvoices(t *testing.T, s *Store) []string {
	t.Helper()
	ctx := context.Background()
	defineInvoice(t, s)
	rows := []Values{
		{"number": "A-1", "priority": 1, "status": "open", "net": "10.00 PLN", "notes": "first", "shipping": Values{"city": "Gdańsk"}, "lines": []any{Values{"sku": "X", "qty": 1, "price": "1.00"}}, "tags": []any{"a"}},
		{"number": "A-2", "priority": 3, "status": "paid", "net": "20.00 PLN", "discount": "0.10", "shipping": Values{"city": "Sopot"}},
		{"number": "B-1", "priority": 5, "status": "open", "net": "30.00 PLN", "parts": 2, "tags": []any{"b", "c"}},
		{"number": "B-2", "status": "open", "notes": "blank priority"},
		{"number": "C-1", "priority": 2, "status": "paid", "net": "5.00 EUR"},
	}
	var ids []string
	for i, r := range rows {
		p := alice
		if i >= 3 {
			p = Principal{ID: "bob"}
		}
		doc, err := s.Create(ctx, "Invoice", r, As(p))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	if err := s.Delete(ctx, "Invoice", ids[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Invoice", ids[1], Values{"notes": "second version"}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func numbers(docs []*Document) string {
	var out []string
	for _, d := range docs {
		out = append(out, d.Values["number"].(string))
	}
	return strings.Join(out, " ")
}

func TestQuery(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	ids := seedInvoices(t, s)
	cases := []struct {
		name string
		f    Filter
		want string
	}{
		{"everything, by id", Filter{Class: "Invoice"}, "A-1 A-2 B-1 B-2"},
		{"a number above", Filter{Class: "Invoice", Where: "priority > 2"}, "A-2 B-1"},
		{"a number below reads a blank as 0", Filter{Class: "Invoice", Where: "priority < 2"}, "A-1 B-2"},
		{"equal to zero reads a blank", Filter{Class: "Invoice", Where: "priority = 0"}, "B-2"},
		{"text equality", Filter{Class: "Invoice", Where: "status = \"paid\""}, "A-2"},
		{"an object leaf", Filter{Class: "Invoice", Where: "shipping.city = \"Sopot\""}, "A-2"},
		{"a blank leaf reads as the empty text", Filter{Class: "Invoice", Where: "shipping.city = \"\""}, "B-1 B-2"},
		{"not equal", Filter{Class: "Invoice", Where: "number <> \"A-1\""}, "A-2 B-1 B-2"},
		{"a function", Filter{Class: "Invoice", Where: "ISBLANK(notes)"}, "B-1"},
		{"a decimal", Filter{Class: "Invoice", Where: "discount >= 0.10"}, "A-2"},
		{"money by amount", Filter{Class: "Invoice", Where: "total > 15.00"}, "A-2 B-1"},
		{"the literal on the left", Filter{Class: "Invoice", Where: "2 < priority"}, "A-2 B-1"},
		{"a row whose formula errors does not match", Filter{Class: "Invoice", Where: "1 / priority > 0"}, "A-1 A-2 B-1"},
		{"AND of comparisons", Filter{Class: "Invoice", Where: "priority >= 1 AND status = \"open\""}, "A-1 B-1"},
		{"OR", Filter{Class: "Invoice", Where: "priority = 1 OR number = \"B-2\""}, "A-1 B-2"},
		{"a system column", Filter{Class: "Invoice", Where: "state = \"draft\" AND version > 1"}, "A-2"},
		{"by id", Filter{Class: "Invoice", Where: "id = \"" + ids[2] + "\""}, "B-1"},
		{"STATE() is the state column", Filter{Class: "Invoice", Where: "STATE() = \"draft\" AND version > 1"}, "A-2"},
		{"ID() is the id column", Filter{Class: "Invoice", Where: "ID() = \"" + ids[2] + "\""}, "B-1"},
		{"a function of the state", Filter{Class: "Invoice", Where: "LEN(STATE()) = 5 AND ISBLANK(notes)"}, "B-1"},
		{"the principal", Filter{Class: "Invoice", Where: "owner = USER()"}, "A-1 A-2 B-1"},
		{"ordered descending", Filter{Class: "Invoice", Order: []string{"-priority"}}, "B-1 A-2 A-1 B-2"},
		{"blanks first ascending", Filter{Class: "Invoice", Order: []string{"priority", "number"}}, "B-2 A-1 A-2 B-1"},
		{"by a leaf", Filter{Class: "Invoice", Order: []string{"-shipping.city", "number"}}, "A-2 A-1 B-1 B-2"},
		{"limit and offset", Filter{Class: "Invoice", Order: []string{"number"}, Limit: 2, Offset: 1}, "A-2 B-1"},
		{"nothing", Filter{Class: "Invoice", Where: "FALSE"}, ""},
	}
	for _, tc := range cases {
		docs, err := s.Query(ctx, tc.f, As(alice))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got := numbers(docs); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	// Fields: only the ones asked for, composites loaded when asked.
	docs, err := s.Query(ctx, Filter{Class: "Invoice", Where: "number = \"A-1\"", Fields: []string{"number", "lines", "Number"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || len(docs[0].Values) != 2 || len(docs[0].Values["lines"].([]any)) != 1 || docs[0].Title != "INV-A-1" {
		t.Errorf("fields: %s", dump(docs))
	}
	// Without fields, every variable comes back, composites included.
	docs, _ = s.Query(ctx, Filter{Class: "Invoice", Where: "number = \"B-1\""})
	if len(docs) != 1 || len(docs[0].Values["tags"].([]any)) != 2 || docs[0].Values["total"] != "30.00 PLN" {
		t.Errorf("all fields: %s", dump(docs))
	}
	// Counts.
	for where, want := range map[string]int64{"": 4, "priority > 2": 2, "FALSE": 0} {
		if n, err := s.Count(ctx, Filter{Class: "Invoice", Where: where}); err != nil || n != want {
			t.Errorf("count %q: %d %v, want %d", where, n, err, want)
		}
	}
	// Refusals.
	setupTables(t, s)
	for _, tc := range []struct {
		f    Filter
		code string
		vari string
	}{
		{Filter{Class: "Nope"}, CodeUnknownClass, ""},
		{Filter{Class: "Invoice", Where: "priority >"}, CodeBadFormula, "where"},
		{Filter{Class: "Invoice", Where: "nope = 1"}, CodeBadFormula, "where"},
		{Filter{Class: "Invoice", Where: "priority + 1"}, CodeBadFormula, "where"},
		{Filter{Class: "Invoice", Where: "LOOKUP(Rates, code, TRUE) = number"}, CodeNotAllowed, "where"},
		{Filter{Class: "Invoice", Order: []string{"nope"}}, CodeUnknownVariable, "order/0"},
		{Filter{Class: "Invoice", Order: []string{"lines"}}, CodeUnknownVariable, "order/0"},
		{Filter{Class: "Invoice", Order: []string{"number", "-number"}}, CodeBadValue, "order/1"},
		{Filter{Class: "Invoice", Limit: 10001}, CodeBadValue, "limit"},
		{Filter{Class: "Invoice", Offset: -1}, CodeBadValue, "offset"},
		{Filter{Class: "Invoice", Fields: []string{"nope"}}, CodeUnknownVariable, "fields/0"},
	} {
		_, err := s.Query(ctx, tc.f)
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.code || e.Variable != tc.vari {
			t.Errorf("%+v: want %s on %q, got %v", tc.f, tc.code, tc.vari, err)
		}
	}
	_, err = s.Query(ctx, Filter{Class: "Invoice", Where: "priority >"})
	var e *Error
	if !errors.As(err, &e) || e.Pos != 10 {
		t.Errorf("a parse error carries its offset: %+v", e)
	}
}

func TestSQL(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	seedInvoices(t, s)
	setupTables(t, s)
	rows, err := s.SQL(ctx, `SELECT number, total, total_currency FROM Invoice WHERE priority > ? ORDER BY number`, []any{2})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rows.Columns, ",") != "number,total,total_currency" || len(rows.Rows) != 2 || rows.Rows[0][0] != "A-2" || rows.Rows[0][1] != int64(1800) || rows.Rows[0][2] != "PLN" {
		t.Errorf("rows: %s", dump(rows))
	}
	if rows, err := s.SQL(ctx, `SELECT count(*) AS n FROM Invoice_lines`, nil); err != nil || rows.Rows[0][0] != int64(1) {
		t.Errorf("lines view: %v %s", err, dump(rows))
	}
	if rows, err := s.SQL(ctx, `SELECT h.number, h.version FROM Invoice_history h WHERE h.valid_to IS NOT NULL ORDER BY h.number`, nil); err != nil || len(rows.Rows) != 2 {
		t.Errorf("history view: %v %s", err, dump(rows))
	}
	if rows, err := s.SQL(ctx, `SELECT kind, count(*) FROM Invoice_events GROUP BY kind ORDER BY kind`, nil); err != nil || len(rows.Rows) != 3 {
		t.Errorf("events view: %v %s", err, dump(rows))
	}
	if rows, err := s.SQL(ctx, `SELECT code, rate FROM Rates ORDER BY code`, nil); err != nil || len(rows.Rows) != 2 {
		t.Errorf("table view: %v %s", err, dump(rows))
	}
	if rows, err := s.SQL(ctx, `SELECT upper('x') AS u, 1 + 1 AS two`, nil); err != nil || rows.Rows[0][0] != "X" || rows.Rows[0][1] != int64(2) {
		t.Errorf("functions: %v %s", err, dump(rows))
	}
	// Reading no column, count(*) or SELECT 1, is allowed over a view and
	// reveals nothing over the table behind it.
	if rows, err := s.SQL(ctx, `SELECT count(*), (SELECT 1 FROM Invoice LIMIT 1) FROM Invoice`, nil); err != nil || rows.Rows[0][0] != int64(4) {
		t.Errorf("count over a view: %v %s", err, dump(rows))
	}
	for _, text := range []string{
		`SELECT * FROM doc_Invoice`,
		`SELECT * FROM doc_Invoice_history`,
		`SELECT * FROM tbl_Rates`,
		`SELECT * FROM events`,
		`SELECT * FROM classes`,
		`SELECT count(*) FROM classes`,
		`SELECT count(*) FROM events`,
		`SELECT * FROM sqlite_master`,
		`SELECT count(*) FROM sqlite_master`,
		`DELETE FROM doc_Invoice`,
		`INSERT INTO Rates (code) VALUES ('X')`,
		`UPDATE tbl_Rates SET rate = 1`,
		`CREATE TABLE t (a)`,
		`DROP VIEW Invoice`,
		`PRAGMA journal_mode`,
		`ATTACH DATABASE ':memory:' AS m`,
		`BEGIN`,
	} {
		_, err := s.SQL(ctx, text, nil)
		if code(err) != CodeNotAllowed {
			t.Errorf("%s: want not_allowed, got %v", text, err)
		}
	}
	if _, err := s.SQL(ctx, `SELECT FROM`, nil); code(err) != CodeBadValue {
		t.Errorf("a syntax error: %v", err)
	}
	if _, err := s.SQL(ctx, `WITH RECURSIVE s(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM s WHERE i < 10001) SELECT i FROM s`, nil); code(err) != CodeTooManyRows {
		t.Errorf("too many rows: %v", err)
	}
	// The endpoint's connection is its own: reads and commits still work.
	docs, err := s.Query(ctx, Filter{Class: "Invoice"})
	if err != nil || len(docs) != 4 {
		t.Errorf("after SQL: %v %d", err, len(docs))
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "D-1"}); err != nil {
		t.Errorf("a commit after SQL: %v", err)
	}
	if rows, err := s.SQL(ctx, `SELECT count(*) FROM Invoice`, nil); err != nil || rows.Rows[0][0] != int64(5) {
		t.Errorf("the endpoint sees the commit: %v %s", err, dump(rows))
	}
}
