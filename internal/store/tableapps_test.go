package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

// TestTableApps_ATableNamesItsApp follows Acme (ADR-0079): Dan puts the
// table Rates into the app Expenses, as he puts a class there. The app is
// there first; a table names one app or none; the tables of one app are
// listed as its classes are, and are there when the database is opened
// again.
func TestTableApps_ATableNamesItsApp(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	rates := `{"name": "Rates", "app": "Expenses", "columns": {"currency": "text", "day": "date", "rate": "decimal(4)"}, "key": ["currency", "day"]}`
	var diags model.Diagnostics
	if _, err := s.DefineTable(ctx, []byte(rates), asSetup); !errors.As(err, &diags) || len(diags) != 1 || diags[0].Code != model.CodeUnknownApp || diags[0].Path != "/app" || diags[0].Hint == "" {
		t.Fatalf("a table of an app that does not exist: %v", err)
	}
	if got := s.Tables(); len(got) != 0 {
		t.Fatalf("a refused table was stored: %v", got)
	}
	for _, app := range []string{`{"name": "Expenses"}`, `{"name": "Helpdesk"}`} {
		if _, err := s.DefineApp(ctx, []byte(app), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	// The name of the app is read without regard to capitals, as every
	// name is; the definition keeps it as it was written.
	tbl, err := s.DefineTable(ctx, []byte(strings.Replace(rates, `"Expenses"`, `"expenses"`, 1)), asSetup)
	if err != nil || tbl.App != "expenses" {
		t.Fatalf("a table of an app the database has: %+v, %v", tbl, err)
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Codes", "columns": {"code": "text"}, "key": ["code"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	check := func(s *Store, when string) {
		t.Helper()
		if got, err := s.TablesOf("Expenses"); err != nil || !reflect.DeepEqual(got, []string{"Rates"}) {
			t.Fatalf("%s: the tables of Expenses: %v, %v", when, got, err)
		}
		if got, err := s.TablesOf("HELPDESK"); err != nil || len(got) != 0 {
			t.Fatalf("%s: the tables of an app that has none: %v, %v", when, got, err)
		}
		var refusal *Error
		if _, err := s.TablesOf("Travel"); !errors.As(err, &refusal) || refusal.Code != CodeUnknownApp {
			t.Fatalf("%s: the tables of an app that does not exist: %v", when, err)
		}
		if got := s.Tables(); !reflect.DeepEqual(got, []string{"Rates", "Codes"}) {
			t.Fatalf("%s: every table: %v", when, got)
		}
		if stored, err := s.Definition(ctx, DefinitionTable, "Rates", 0); err != nil || !strings.Contains(string(stored.Definition), `"app":"expenses"`) {
			t.Fatalf("%s: the table read back: %+v, %v", when, stored, err)
		}
	}
	check(s, "defined")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	check(again, "opened again")
}

// TestTableApps_ATableInUseMovesToAnotherApp holds item 4 of ADR-0079: a
// table moves to another app by its next version while it holds rows and
// a class reads it, and nothing of its rows or of what reads it changes.
// Any other change of a table in use is refused as before.
func TestTableApps_ATableInUseMovesToAnotherApp(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, app := range []string{`{"name": "Expenses"}`, `{"name": "Treasury"}`} {
		if _, err := s.DefineApp(ctx, []byte(app), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	setupTables(t, s)
	if _, err := s.defineOpen(ctx, []byte(order), asSetup); err != nil {
		t.Fatal(err)
	}
	// A lookup builds the plan and the rows in memory before the move.
	if doc, err := s.Create(ctx, "Order", Values{"currency": "EUR", "net": "100.00"}); err != nil || doc.Values["rate"] != "4.321000" {
		t.Fatalf("before the move: %v, %v", doc, err)
	}
	moved := strings.Replace(ratesTable, `"name": "Rates",`, `"name": "Rates", "app": "Treasury",`, 1)
	tbl, err := s.defineOpenTable(ctx, []byte(moved), asSetup)
	if err != nil || tbl.Version != 2 || tbl.App != "Treasury" {
		t.Fatalf("the move to Treasury: %+v, %v", tbl, err)
	}
	if got, err := s.TablesOf("Treasury"); err != nil || !reflect.DeepEqual(got, []string{"Rates"}) {
		t.Fatalf("the tables of Treasury: %v, %v", got, err)
	}
	if rows, err := s.TableRows(ctx, "Rates", asSetup); err != nil || len(rows) != 2 {
		t.Fatalf("the rows after the move: %v, %v", rows, err)
	}
	// What reads the table reads the rows the database holds now: a row
	// imported after the move is the one a new order looks up.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.5", "fee": "1.00 EUR", "name": "Euro"}}); err != nil {
		t.Fatal(err)
	}
	if doc, err := s.Create(ctx, "Order", Values{"currency": "EUR", "net": "100.00"}); err != nil || doc.Values["rate"] != "4.500000" {
		t.Fatalf("after the move and an import: %v, %v", doc.Values, err)
	}
	// A change of anything else, with or without the app, is refused while
	// the table is in use.
	var diags model.Diagnostics
	changed := strings.Replace(moved, `"name": "text"}`, `"name": "number"}`, 1)
	if _, err := s.defineOpenTable(ctx, []byte(changed), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotSupported {
		t.Fatalf("a column changed while the table is in use: %v", err)
	}
	relabelled := strings.Replace(ratesTable, `"label": "Exchange rates"`, `"app": "Expenses", "label": "Rates of exchange"`, 1)
	if _, err := s.defineOpenTable(ctx, []byte(relabelled), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotSupported {
		t.Fatalf("a move with a new label while the table is in use: %v", err)
	}
	// Into an app the database does not have, it does not move.
	if _, err := s.defineOpenTable(ctx, []byte(strings.Replace(moved, `"Treasury"`, `"Travel"`, 1)), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeUnknownApp {
		t.Fatalf("a move to an app that does not exist: %v", err)
	}
	// And out of every app, back to none.
	if tbl, err := s.defineOpenTable(ctx, []byte(ratesTable), asSetup); err != nil || tbl.Version != 3 || tbl.App != "" {
		t.Fatalf("the move to no app: %+v, %v", tbl, err)
	}
	if got, err := s.TablesOf("Treasury"); err != nil || len(got) != 0 {
		t.Fatalf("the tables of Treasury after the table left: %v, %v", got, err)
	}
	if versions, err := s.Definitions(ctx, DefinitionTable, "Rates"); err != nil || len(versions) != 3 {
		t.Fatalf("who defined each version: %v, %v", versions, err)
	}
}

// TestAddColumns_ATableInUse holds ADR-0080: Dan adds two columns to Rates
// while it holds rows and the class Order reads it. The version is taken,
// the rows it holds are blank in the new columns, the new columns are
// written and read like the others and by SQL, what reads the table goes
// on reading it, and so it is when the database is opened again. Any other
// change of the table in use is still refused.
func TestAddColumns_ATableInUse(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	setupTables(t, s)
	if _, err := s.defineOpen(ctx, []byte(order), asSetup); err != nil {
		t.Fatal(err)
	}
	if doc, err := s.Create(ctx, "Order", Values{"currency": "EUR", "net": "100.00"}); err != nil || doc.Values["rate"] != "4.321000" {
		t.Fatalf("before the columns: %v, %v", doc, err)
	}
	wider := strings.Replace(ratesTable, `"name": "text"}`, `"name": "text", "note": {"type": "text", "label": "Note"}, "card_fee": "money(2)"}`, 1)
	tbl, err := s.defineOpenTable(ctx, []byte(wider), asSetup)
	if err != nil || tbl.Version != 2 || len(tbl.Fields) != 6 {
		t.Fatalf("two columns added to a table in use: %+v, %v", tbl, err)
	}
	rows, err := s.TableRows(ctx, "Rates", asSetup)
	if err != nil || len(rows) != 2 || rows[0]["note"] != nil || rows[0]["card_fee"] != nil || rows[0]["rate"] != "4.321000" {
		t.Fatalf("the rows after the columns were added: %v, %v", rows, err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.5", "fee": "1.00 EUR", "name": "Euro", "note": "NBP", "card_fee": "0.50 EUR"}}); err != nil {
		t.Fatal(err)
	}
	if doc, err := s.Create(ctx, "Order", Values{"currency": "EUR", "net": "100.00"}); err != nil || doc.Values["rate"] != "4.500000" {
		t.Fatalf("a lookup after the columns and an import: %v, %v", doc, err)
	}
	var note, fee, cur string
	if err := s.db.QueryRowContext(ctx, `SELECT note, card_fee, card_fee_currency FROM Rates WHERE code = 'EUR'`).Scan(&note, &fee, &cur); err != nil || note != "NBP" || cur != "EUR" {
		t.Fatalf("the view after the columns were added: %q %q %q, %v", note, fee, cur, err)
	}
	// Anything more than columns added is refused while the table is in
	// use: a new column in the key, a column changed, a new label.
	var diags model.Diagnostics
	for what, def := range map[string]string{
		"a new column in the key": strings.Replace(wider, `"key": ["code"]`, `"key": ["code", "note"]`, 1),
		"a column changed":        strings.Replace(wider, `"rate": "decimal(6)"`, `"rate": "decimal(4)"`, 1),
		"a column added and a new label": strings.Replace(strings.Replace(wider, `"card_fee": "money(2)"}`, `"card_fee": "money(2)", "x": "text"}`, 1),
			`"Exchange rates"`, `"Rates of exchange"`, 1),
	} {
		if _, err := s.defineOpenTable(ctx, []byte(def), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotSupported {
			t.Errorf("%s while the table is in use: %v", what, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, err := again.ImportRows(ctx, "Rates", []Values{{"code": "GBP", "rate": "5.1", "name": "Pound", "note": "ECB"}}); err != nil {
		t.Fatalf("a row with the new column after an open: %v", err)
	}
	if rows, err := again.TableRows(ctx, "Rates", asSetup); err != nil || len(rows) != 3 {
		t.Fatalf("the rows after an open: %v, %v", rows, err)
	}
}

// TestRemove_RowsAndTables holds ADR-0082: rows are removed by their keys,
// all of them or none, as the table's write rule lets; a table is taken
// away with its rows and its words by a designer, unless a class reads it
// or a rule writes it; and a table made again under its name goes on from
// its versions.
func TestRemove_RowsAndTables(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	setupTables(t, s)
	if _, err := s.defineOpen(ctx, []byte(order), asSetup); err != nil {
		t.Fatal(err)
	}
	// Rows by their keys: a key the table lacks removes nothing.
	if n, err := s.DeleteRows(ctx, "Rates", []Values{{"code": "EUR"}, {"code": "XXX"}}); err != nil || n != 1 {
		t.Fatalf("two keys, one of them in the table: %d, %v", n, err)
	}
	if rows, err := s.TableRows(ctx, "Rates", asSetup); err != nil || len(rows) != 1 || rows[0]["code"] != "USD" {
		t.Fatalf("the rows after the delete: %v, %v", rows, err)
	}
	// A key names every column of the table's key and no other; one that
	// does not refuses them all, and nothing is removed.
	var refusal *Error
	if _, err := s.DeleteRows(ctx, "Prices", []Values{{"sku": "B2", "min_qty": 1}, {"sku": "A1"}}); !errors.As(err, &refusal) || refusal.Code != CodeRequired || refusal.Variable != "Prices[1].min_qty" {
		t.Fatalf("a key without all its columns: %v", err)
	}
	if _, err := s.DeleteRows(ctx, "Rates", []Values{{"code": "USD", "rate": "1"}}); !errors.As(err, &refusal) || refusal.Code != CodeUnknownVariable {
		t.Fatalf("a key with a column outside the key: %v", err)
	}
	if rows, _ := s.TableRows(ctx, "Prices", asSetup); len(rows) != 3 {
		t.Fatalf("a refused delete removed rows: %v", rows)
	}
	if n, err := s.DeleteRows(ctx, "Prices", []Values{{"sku": "A1", "min_qty": 10}}); err != nil || n != 1 {
		t.Fatalf("a row of a key of two columns: %d, %v", n, err)
	}
	// The table's write rule is asked, as for an import.
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Kept", "columns": {"k": "text"}, "key": ["k"], "access": {"read": "TRUE", "write": "INGROUP(\"treasury\")"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteRows(ctx, "Kept", []Values{{"k": "a"}}, As(Principal{ID: "ann", Kind: ActorPerson})); !errors.As(err, &refusal) || refusal.Code != CodeForbidden {
		t.Fatalf("somebody the write rule does not let through deleted rows: %v", err)
	}

	// A table a class reads, or a rule of a class writes, is not removed.
	if err := s.RemoveTable(ctx, "rates", asSetup); !errors.As(err, &refusal) || refusal.Code != CodeNotAllowed || !strings.Contains(refusal.Message, "the class Order reads") {
		t.Fatalf("removing a table a class reads: %v", err)
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Fed", "columns": {"k": "text"}, "key": ["k"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	feeds := `{"name": "Feeds", "variables": {"c": {"type": "text"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b"}],
	  "rules": [{"on": "transition", "name": "go", "do": [{"action": "table.insert", "table": "Fed", "row": {"k": "c"}}]}]}`
	if _, err := s.defineOpen(ctx, []byte(feeds), asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTable(ctx, "Fed", asSetup); !errors.As(err, &refusal) || refusal.Code != CodeNotAllowed || !strings.Contains(refusal.Message, "a rule of the class Feeds writes") {
		t.Fatalf("removing a table a rule writes: %v", err)
	}
	// Somebody who is no designer removes no table.
	if err := s.RemoveTable(ctx, "Kept", As(Principal{ID: "ann", Kind: ActorPerson})); !errors.As(err, &refusal) || refusal.Code != CodeForbidden {
		t.Fatalf("somebody who is no designer removed a table: %v", err)
	}
	// Scratch goes, with its rows and its words; made again, it is its next version.
	for _, def := range []string{`{"name": "Scratch", "columns": {"k": "text"}, "key": ["k"]}`, `{"name": "Scratch", "columns": {"k": "text", "v": "text"}, "key": ["k"]}`} {
		if _, err := s.defineOpenTable(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ImportRows(ctx, "Scratch", []Values{{"k": "a", "v": "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineWords(ctx, []byte(`{"table": "Scratch", "language": "pl", "label": "Brudnopis"}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTable(ctx, "SCRATCH", asSetup); err != nil {
		t.Fatalf("removing a table nothing reads: %v", err)
	}
	if _, ok := s.Table("Scratch"); ok || slices.Contains(s.Tables(), "Scratch") {
		t.Fatalf("the table is still there: %v", s.Tables())
	}
	if _, err := s.TableRows(ctx, "Scratch", asSetup); !errors.As(err, &refusal) || refusal.Code != CodeUnknownTable {
		t.Fatalf("the rows of a removed table: %v", err)
	}
	if err := s.RemoveTable(ctx, "Scratch", asSetup); !errors.As(err, &refusal) || refusal.Code != CodeUnknownTable {
		t.Fatalf("removing it again: %v", err)
	}
	if got := s.ListWords(WordsOf{Table: "Scratch"}); len(got) != 0 {
		t.Fatalf("the words of a removed table are still there: %v", got)
	}
	again, err := s.defineOpenTable(ctx, []byte(`{"name": "Scratch", "columns": {"k": "text"}, "key": ["k"]}`), asSetup)
	if err != nil || again.Version != 3 {
		t.Fatalf("the table made again: %+v, %v", again, err)
	}
	if rows, err := s.TableRows(ctx, "Scratch", asSetup); err != nil || len(rows) != 0 {
		t.Fatalf("the table made again holds rows: %v, %v", rows, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if tbl, ok := reopened.Table("Scratch"); !ok || tbl.Version != 3 {
		t.Fatalf("the table made again, after an open: %+v", tbl)
	}
	if versions, err := reopened.Definitions(ctx, DefinitionTable, "Scratch"); err != nil || len(versions) != 3 {
		t.Fatalf("the versions of Scratch: %v, %v", versions, err)
	}
}
