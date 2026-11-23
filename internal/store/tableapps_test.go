package store

import (
	"context"
	"errors"
	"reflect"
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
