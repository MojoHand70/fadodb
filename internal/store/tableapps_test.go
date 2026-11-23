package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	// A column a class uses keeps its type while the table is in use; a new
	// label with the move is taken, its rows carried over (ADR-0084).
	var diags model.Diagnostics
	changed := strings.Replace(moved, `"name": "text"}`, `"name": "number"}`, 1)
	if _, err := s.defineOpenTable(ctx, []byte(changed), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotAllowed {
		t.Fatalf("a column Order uses, changed while the table is in use: %v", err)
	}
	relabelled := strings.Replace(ratesTable, `"label": "Exchange rates"`, `"app": "Expenses", "label": "Rates of exchange"`, 1)
	if tbl, err := s.defineOpenTable(ctx, []byte(relabelled), asSetup); err != nil || tbl.Version != 3 {
		t.Fatalf("a move with a new label while the table is in use: %+v, %v", tbl, err)
	}
	if rows, err := s.TableRows(ctx, "Rates", asSetup); err != nil || len(rows) != 2 {
		t.Fatalf("the rows after the new label: %v, %v", rows, err)
	}
	// Into an app the database does not have, it does not move.
	if _, err := s.defineOpenTable(ctx, []byte(strings.Replace(moved, `"Treasury"`, `"Travel"`, 1)), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeUnknownApp {
		t.Fatalf("a move to an app that does not exist: %v", err)
	}
	// And out of every app, back to none.
	if tbl, err := s.defineOpenTable(ctx, []byte(ratesTable), asSetup); err != nil || tbl.Version != 4 || tbl.App != "" {
		t.Fatalf("the move to no app: %+v, %v", tbl, err)
	}
	if got, err := s.TablesOf("Treasury"); err != nil || len(got) != 0 {
		t.Fatalf("the tables of Treasury after the table left: %v, %v", got, err)
	}
	if versions, err := s.Definitions(ctx, DefinitionTable, "Rates"); err != nil || len(versions) != 4 {
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
	// While the table is in use its key stays, and a column Order uses
	// keeps its type (ADR-0084); a column added with a new label is taken.
	var diags model.Diagnostics
	for what, c := range map[string]struct{ def, code string }{
		"a new column in the key":      {strings.Replace(wider, `"key": ["code"]`, `"key": ["code", "note"]`, 1), model.CodeNotSupported},
		"a column Order uses, changed": {strings.Replace(wider, `"rate": "decimal(6)"`, `"rate": "decimal(4)"`, 1), model.CodeNotAllowed},
	} {
		if _, err := s.defineOpenTable(ctx, []byte(c.def), asSetup); !errors.As(err, &diags) || diags[0].Code != c.code {
			t.Errorf("%s while the table is in use: %v", what, err)
		}
	}
	labelled := strings.Replace(strings.Replace(wider, `"card_fee": "money(2)"}`, `"card_fee": "money(2)", "x": "text"}`, 1), `"Exchange rates"`, `"Rates of exchange"`, 1)
	if _, err := s.defineOpenTable(ctx, []byte(labelled), asSetup); err != nil {
		t.Errorf("a column added and a new label while the table is in use: %v", err)
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

// TestChange_ATableInUse holds ADR-0084, the owner's two checks of a table
// that holds rows: a value of a changed column converts to its new type
// or the version is refused, naming the row; a column a formula or a rule
// of a class uses keeps its type and stays, or the version is refused,
// naming the class. What passes both is taken, its rows carried over.
func TestChange_ATableInUse(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	setupTables(t, s)
	if _, err := s.defineOpen(ctx, []byte(order), asSetup); err != nil {
		t.Fatal(err)
	}
	codes := func(cols string) string {
		return `{"name": "Codes", "columns": {"code": "text", ` + cols + `}, "key": ["code"], "access": {"read": "TRUE", "write": "TRUE"}}`
	}
	if _, err := s.DefineTable(ctx, []byte(codes(`"n": "text", "d": "decimal(2)", "flag": "text", "day": "date"`)), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Codes", []Values{{"code": "a", "n": "7", "d": "1.50", "flag": "true", "day": "2026-11-13"}, {"code": "b", "n": "12", "d": "2.55", "flag": "false"}}); err != nil {
		t.Fatal(err)
	}
	var diags model.Diagnostics
	// Text that is a whole number becomes one; a decimal gains places; text of
	// true and false becomes a yes or no; a column goes.
	tbl, err := s.DefineTable(ctx, []byte(codes(`"n": "int", "d": "decimal(4)", "flag": "bool"`)), asSetup)
	if err != nil || tbl.Version != 2 {
		t.Fatalf("a version that changes three columns and removes one: %+v, %v", tbl, err)
	}
	rows, err := s.TableRows(ctx, "Codes", asSetup)
	if err != nil || len(rows) != 2 || rows[0]["n"] != "7" || rows[0]["d"] != "1.5000" || rows[0]["flag"] != true || rows[1]["flag"] != false || rows[1]["d"] != "2.5500" {
		t.Fatalf("the rows carried over: %v, %v", rows, err)
	}
	if _, ok := rows[0]["day"]; ok {
		t.Fatalf("a removed column is still in the rows: %v", rows[0])
	}
	// A value that would change or be lost refuses the version: one place
	// cannot hold 2.55, and "x" is no figure.
	if _, err := s.DefineTable(ctx, []byte(codes(`"n": "int", "d": "decimal(1)", "flag": "bool"`)), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeBadValue || diags[0].Path != "/columns/d" || !strings.Contains(diags[0].Message, `the row b holds "2.5500"`) {
		t.Fatalf("fewer places than a value has: %v", err)
	}
	if _, err := s.ImportRows(ctx, "Codes", []Values{{"code": "c", "n": "1", "d": "1", "flag": true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineTable(ctx, []byte(codes(`"n": "bool", "d": "decimal(4)", "flag": "bool"`)), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeBadValue || !strings.HasPrefix(diags[0].Message, "3 rows do not convert") {
		t.Fatalf("whole numbers to a yes or no: %v", err)
	}
	// The key stays, the same columns of the same types.
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Codes", "columns": {"code": "int", "n": "int", "d": "decimal(4)", "flag": "bool"}, "key": ["code"], "access": {"read": "TRUE", "write": "TRUE"}}`), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotSupported || diags[0].Path != "/key" {
		t.Fatalf("a key of another type: %v", err)
	}
	// Everything as text: a figure as its digits, a yes or no as true or false.
	if _, err := s.DefineTable(ctx, []byte(codes(`"n": "text", "d": "text", "flag": "text"`)), asSetup); err != nil {
		t.Fatalf("every column to text: %v", err)
	}
	if rows, _ := s.TableRows(ctx, "Codes", asSetup); rows[0]["d"] != "1.5000" || rows[0]["flag"] != "true" || rows[2]["n"] != "1" {
		t.Fatalf("the rows as text: %v", rows)
	}

	// Rates: Order reads its rate, its fee and its name. Those keep their
	// type and stay; a column nothing uses changes, with the table read.
	for what, def := range map[string]string{
		"the name, which Order's default reads, removed": strings.Replace(ratesTable, `, "name": "text"}, "key": ["code"], "indexes": [["name"]]}`, `}, "key": ["code"]}`, 1),
		"the rate, which Order's formula reads, retyped": strings.Replace(ratesTable, `"rate": "decimal(6)"`, `"rate": "decimal(8)"`, 1),
	} {
		if _, err := s.defineOpenTable(ctx, []byte(def), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotAllowed || !strings.Contains(diags[0].Message, "the class Order uses a column") {
			t.Errorf("%s: %v", what, err)
		}
	}
	noted := strings.Replace(ratesTable, `"name": "text"}`, `"name": "text", "note": "text"}`, 1)
	if _, err := s.defineOpenTable(ctx, []byte(noted), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.321", "fee": "1.00 EUR", "name": "Euro", "note": "2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpenTable(ctx, []byte(strings.Replace(noted, `"note": "text"`, `"note": "int"`, 1)), asSetup); err != nil {
		t.Fatalf("a column nothing uses, retyped while Order reads the table: %v", err)
	}
	if doc, err := s.Create(ctx, "Order", Values{"currency": "EUR", "net": "100.00"}); err != nil || doc.Values["rate"] != "4.321000" || doc.Values["label"] != "Euro order" {
		t.Fatalf("Order after the version: %v, %v", doc, err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if rows, err := again.TableRows(ctx, "Codes", asSetup); err != nil || len(rows) != 3 || rows[1]["d"] != "2.5500" {
		t.Fatalf("the rows after an open: %v, %v", rows, err)
	}
}

// TestRemove_WhoRemovedIt follows Acme (ADR-0083): Dan the designer
// removes the table Scratch, which has Polish words, and Dee, an agent
// working for the lead, removes the German words of Rates. The log of
// definitions says, on the version each had, who removed it, what they
// were, for whom they acted and when; a removal that is refused records
// nothing; and the record is in the file, so another process reads the
// same. The engine schema stays 13: the record is a row of a table every
// such database has.
func TestRemove_WhoRemovedIt(t *testing.T) {
	now := &clock{now: start}
	s, path := openStore(t, Options{Now: now.Now})
	ctx := context.Background()
	dan := Principal{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}}
	dee := Principal{ID: "dee", Kind: ActorAgent, For: "lead", System: []string{GroupDesigners}}
	gives(t, s, "dee", "lead")
	for _, def := range []string{`{"name": "Scratch", "columns": {"k": "text"}, "key": ["k"]}`, `{"name": "Scratch", "columns": {"k": "text", "v": "text"}, "key": ["k"]}`,
		`{"name": "Rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"]}`} {
		if _, err := s.defineOpenTable(ctx, []byte(def), As(dan)); err != nil {
			t.Fatal(err)
		}
	}
	for _, def := range []string{`{"table": "Scratch", "language": "pl", "label": "Brudnopis"}`, `{"table": "Rates", "language": "de", "label": "Kurse"}`,
		`{"table": "Rates", "language": "pl", "label": "Kursy"}`} {
		if _, err := s.DefineWords(ctx, []byte(def), As(dan)); err != nil {
			t.Fatal(err)
		}
	}
	// A removal that is refused records nothing: Ann is no designer.
	var refusal *Error
	if err := s.RemoveTable(ctx, "Scratch", As(Principal{ID: "ann", Kind: ActorPerson})); !errors.As(err, &refusal) || refusal.Code != CodeForbidden {
		t.Fatalf("somebody who is no designer removed a table: %v", err)
	}
	now.mu.Lock()
	now.now = now.now.Add(time.Hour)
	now.mu.Unlock()
	if err := s.RemoveTable(ctx, "scratch", As(dan)); err != nil {
		t.Fatal(err)
	}
	now.mu.Lock()
	now.now = now.now.Add(time.Hour)
	now.mu.Unlock()
	if err := s.RemoveWords(ctx, WordsOf{Table: "Rates", Language: "de"}, As(dee)); err != nil {
		t.Fatal(err)
	}
	// Dan reads the log: a designer is answered who removed a thing.
	removed := func(s *Store, what, name string) string {
		t.Helper()
		defs, err := s.Definitions(ctx, what, name, As(dan))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range defs {
			line := fmt.Sprintf("v%d by %s", d.Version, d.By)
			if d.Removed != nil {
				line += strings.TrimSpace(fmt.Sprintf(", removed by %s %s for %s at +%dh", d.Removed.By, d.Removed.Kind, d.Removed.For, hours(d.Removed.At)))
			}
			out = append(out, line)
		}
		return strings.Join(out, "; ")
	}
	check := func(s *Store, scratch string) {
		t.Helper()
		for key, want := range map[string]string{
			// The table at the version it had, and its words with it, by
			// the same person at the same moment.
			"table Scratch":          scratch,
			"words table/Scratch/pl": "v1 by dan, removed by dan person for  at +1h",
			// Words removed on their own, by an agent for the lead.
			"words table/Rates/de": "v1 by dan, removed by dee agent for lead at +2h",
			// What was not removed says nothing of a removal.
			"table Rates":          "v1 by dan",
			"words table/Rates/pl": "v1 by dan",
		} {
			what, name, _ := strings.Cut(key, " ")
			if got := removed(s, what, name); got != want {
				t.Errorf("the log of %s: %s, want %s", key, got, want)
			}
		}
		// A removal is no kind of definition a caller names.
		if _, err := s.Definitions(ctx, DefinitionTable+removedSuffix, "Scratch", As(dan)); code(err) != CodeBadValue {
			t.Errorf("the removals asked as a kind of definition: %v", err)
		}
		// This database keeps no users, so the engine cannot tell whom Dee
		// works for: an agent is not answered who removed a thing.
		if defs, err := s.Definitions(ctx, DefinitionTable, "Scratch", As(dee)); err != nil || len(defs) < 2 || defs[1].Removed != nil {
			t.Errorf("an agent in a database that keeps no users: %+v, %v", defs, err)
		}
	}
	check(s, "v1 by dan; v2 by dan, removed by dan person for  at +1h")
	// Made again, the table is its next version and nobody removed it; the
	// version that was removed still says who did.
	if again, err := s.defineOpenTable(ctx, []byte(`{"name": "Scratch", "columns": {"k": "text"}, "key": ["k"]}`), As(dan)); err != nil || again.Version != 3 {
		t.Fatalf("the table made again: %+v, %v", again, err)
	}
	made := "v1 by dan; v2 by dan, removed by dan person for  at +1h; v3 by dan"
	check(s, made)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: now.Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if tbl, ok := again.Table("Scratch"); !ok || tbl.Version != 3 || tbl.Defined.By != "dan" {
		t.Fatalf("the table made again, after an open: %+v", tbl)
	}
	if got := again.ListWords(WordsOf{Table: "Rates"}); len(got) != 1 || got[0].Language != "pl" {
		t.Fatalf("the words of Rates after an open: %+v", got)
	}
	check(again, made)
	// Removed twice, the table records each removal on its own version.
	if err := again.RemoveTable(ctx, "Scratch", As(dee)); err != nil {
		t.Fatal(err)
	}
	if got := removed(again, DefinitionTable, "Scratch"); got != "v1 by dan; v2 by dan, removed by dan person for  at +1h; v3 by dan, removed by dee agent for lead at +2h" {
		t.Errorf("the log of Scratch removed twice: %s", got)
	}
}

// TestRemove_WhoSeesARemoval holds the owner's ruling of 2026-11-23 on
// who reads who removed a table (ADR-0083): "administrators and designers
// only, Agents if work on Administrator behalf". Acme keeps its users:
// Olga is a database administrator, Dan a designer, Ann neither, and Ewa
// an administrator who has left. Dan removes the table Scratch. Who may
// not see the removal gets the versions without it, and no refusal.
func TestRemove_WhoSeesARemoval(t *testing.T) {
	s, _ := openStore(t, Options{Users: true})
	ctx := context.Background()
	for _, u := range []User{{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}}, {ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}},
		{ID: "ann", Kind: ActorPerson}, {ID: "ewa", Kind: ActorPerson, System: []string{GroupAdministrators}}} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	dan := Principal{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Scratch", "columns": {"k": "text"}, "key": ["k"]}`), As(dan)); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTable(ctx, "Scratch", As(dan)); err != nil {
		t.Fatal(err)
	}
	// The agent bot works for Olga, for Dan and for Ewa, each of whom said
	// so; bot2 says it works for Olga, and she never said so.
	for _, acts := range []string{"olga", "dan", "ewa"} {
		gives(t, s, "bot", acts)
	}
	disabled := true
	if _, err := s.SetUser(ctx, "ewa", UserEdit{Disabled: &disabled}, machine); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		opts []Option
		sees bool
	}{
		"Dan, a designer":                        {[]Option{As(dan)}, true},
		"Olga, a database administrator":         {[]Option{As(Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}})}, true},
		"a service in administrators":            {[]Option{As(Principal{ID: "nightly", Kind: ActorService, System: []string{GroupAdministrators}})}, true},
		"a designer whose kind is not given":     {[]Option{As(Principal{ID: "setup", System: []string{GroupDesigners}})}, true},
		"an agent working for Olga":              {[]Option{As(Principal{ID: "bot", Kind: ActorAgent, For: "olga"})}, true},
		"Ann, who is neither":                    {[]Option{As(Principal{ID: "ann", Kind: ActorPerson})}, false},
		"an analyst":                             {[]Option{As(Principal{ID: "ana", Kind: ActorPerson, System: []string{GroupAnalysts}})}, false},
		"an agent working for Dan, a designer":   {[]Option{As(Principal{ID: "bot", Kind: ActorAgent, For: "dan"})}, false},
		"an agent working for nobody":            {[]Option{As(Principal{ID: "bot", Kind: ActorAgent})}, false},
		"ap-agent, itself in administrators":     {[]Option{As(Principal{ID: "ap-agent", Kind: ActorAgent, System: []string{GroupAdministrators, GroupDesigners}})}, false},
		"an agent working for Ewa, who has left": {[]Option{As(Principal{ID: "bot", Kind: ActorAgent, For: "ewa"})}, false},
		"an agent Olga never let act for her":    {[]Option{As(Principal{ID: "bot2", Kind: ActorAgent, For: "olga"})}, false},
		"a call that names nobody":               {nil, false},
	} {
		defs, err := s.Definitions(ctx, DefinitionTable, "Scratch", c.opts...)
		if err != nil || len(defs) != 1 || defs[0].By != "dan" {
			t.Errorf("%s reads the versions of Scratch: %+v, %v", name, defs, err)
			continue
		}
		if got := defs[0].Removed != nil; got != c.sees {
			t.Errorf("%s is answered who removed Scratch: %v, want %v", name, got, c.sees)
		} else if got && (defs[0].Removed.By != "dan" || defs[0].Removed.Kind != ActorPerson) {
			t.Errorf("%s reads that %+v removed Scratch", name, defs[0].Removed)
		}
	}
}
