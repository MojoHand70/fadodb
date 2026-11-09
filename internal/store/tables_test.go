package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

const ratesTable = `{"name": "Rates", "label": "Exchange rates", "columns": {"code": "text", "rate": "decimal(6)", "fee": "money(2)", "name": "text"}, "key": ["code"], "indexes": [["name"]]}`
const pricesTable = `{"name": "Prices", "columns": {"sku": "text", "min_qty": "number", "price": "money(2)"}, "key": ["sku", "min_qty"]}`

// order is a class that reads both tables: two indexed lookups, one
// statement lookup, and a default with a lookup.
const order = `{"name": "Order", "variables": {
  "currency": {"type": "text", "required": true},
  "net":      {"type": "decimal(2)"},
  "sku":      {"type": "text"},
  "qty":      {"type": "number"},
  "rate":     {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"},
  "net_pln":  {"formula": "ROUND(net * rate, 2)"},
  "fee":      {"formula": "LOOKUP(Rates, fee, code = THIS.currency)"},
  "unit_price": {"formula": "LOOKUP(Prices, price, sku = THIS.sku AND min_qty <= THIS.qty)"},
  "label":    {"type": "text", "default": "LOOKUP(Rates, name, code = THIS.currency) & \" order\""}
}, "states": {"initial": "open", "list": ["open"]}}`

var rateRows = []Values{
	{"code": "EUR", "rate": "4.321000", "fee": "1.00 EUR", "name": "Euro"},
	{"code": "USD", "rate": "3.9876", "fee": "0.50 USD", "name": "Dollar"},
}

var priceRows = []Values{
	{"sku": "A1", "min_qty": 10, "price": "8.00 PLN"},
	{"sku": "A1", "min_qty": 1, "price": "9.50 PLN"},
	{"sku": "B2", "min_qty": 1, "price": "4.00 PLN"},
}

func setupTables(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, def := range []string{ratesTable, pricesTable} {
		if _, err := s.DefineTable(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.ImportRows(ctx, "Rates", rateRows); err != nil || n != 2 {
		t.Fatalf("import rates: %d %v", n, err)
	}
	if n, err := s.ImportRows(ctx, "prices", priceRows); err != nil || n != 3 {
		t.Fatalf("import prices: %d %v", n, err)
	}
}

func TestDefineTable(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	setupTables(t, s)
	if got := strings.Join(s.Tables(), ","); got != "Rates,Prices" {
		t.Errorf("tables %s", got)
	}
	tbl, ok := s.Table("rates")
	if !ok || tbl.Version != 1 || tbl.Binding != "sqlite" || len(tbl.Columns) != 5 {
		t.Errorf("table %+v %v", tbl, ok)
	}
	rows, err := s.db.Query("SELECT type, name FROM sqlite_master WHERE name LIKE '%Rates%' ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var typ, name string
		rows.Scan(&typ, &name)
		names = append(names, typ+":"+name)
	}
	rows.Close()
	if got := strings.Join(names, " "); got != "index:sqlite_autoindex_tbl_Rates_1 index:tbl_Rates_name table:tbl_Rates view:Rates" {
		t.Errorf("objects: %s", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM Rates WHERE fee_currency = 'EUR'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("view: %d %v", n, err)
	}
	// The rows come back in key order, in the wire shape.
	got, err := s.TableRows(ctx, "Rates")
	if err != nil {
		t.Fatal(err)
	}
	want := []Values{
		{"code": "EUR", "rate": "4.321000", "fee": "1.00 EUR", "name": "Euro"},
		{"code": "USD", "rate": "3.987600", "fee": "0.50 USD", "name": "Dollar"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows:\n%s\nwant\n%s", dump(got), dump(want))
	}
	// An import replaces a row by its key and adds a new one.
	if n, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "5", "fee": nil}, {"code": "GBP", "rate": "5.5", "name": "Pound"}}); err != nil || n != 2 {
		t.Fatalf("second import: %d %v", n, err)
	}
	got, _ = s.TableRows(ctx, "Rates")
	if len(got) != 3 || got[0]["rate"] != "5.000000" || got[0]["fee"] != nil || got[0]["name"] != nil || got[1]["code"] != "GBP" {
		t.Errorf("after the second import:\n%s", dump(got))
	}
	// Replace empties the table first.
	if _, err := s.ImportRows(ctx, "Rates", rateRows, Replace()); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.TableRows(ctx, "Rates"); len(got) != 2 {
		t.Errorf("after replace: %d rows", len(got))
	}
	// Refusals.
	for _, tc := range []struct {
		rows []Values
		code string
		vari string
	}{
		{[]Values{{"code": "CHF", "nope": 1}}, CodeUnknownVariable, "Rates[0].nope"},
		{[]Values{{"rate": "1"}}, CodeRequired, "Rates[0].code"},
		{[]Values{{"code": "CHF", "rate": "abc"}}, CodeBadValue, "Rates[0].rate"},
		{[]Values{{"code": "CHF"}, {"code": "CHF", "fee": "1.00"}}, CodeBadValue, "Rates[1].fee"},
	} {
		_, err := s.ImportRows(ctx, "Rates", tc.rows)
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.code || e.Variable != tc.vari {
			t.Errorf("import %v: want %s on %s, got %v", tc.rows, tc.code, tc.vari, err)
		}
	}
	if got, _ = s.TableRows(ctx, "Rates"); len(got) != 2 {
		t.Errorf("a refused import wrote rows: %d", len(got))
	}
	if _, err := s.ImportRows(ctx, "Nope", rateRows); code(err) != CodeUnknownTable {
		t.Errorf("unknown table: %v", err)
	}
	// Definitions: the same again is a no-op; a bad one is diagnostics; a
	// name a class or view holds is taken.
	if again, err := s.DefineTable(ctx, []byte(ratesTable)); err != nil || again != tbl {
		t.Errorf("same table again: %v", err)
	}
	_, err = s.DefineTable(ctx, []byte(`{"name": "Bad", "columns": {"a": "text"}}`))
	var diags model.Diagnostics
	if !errors.As(err, &diags) || !diags.Has(model.CodeMissing) {
		t.Errorf("bad table: %v", err)
	}
	defineInvoice(t, s)
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Invoice_lines", "columns": {"a": "text"}, "key": ["a"]}`)); code(err) != CodeDuplicate {
		t.Errorf("a table named like a view: %v", err)
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Invoice", "columns": {"a": "text"}, "key": ["a"]}`)); !errors.As(err, &diags) || !diags.Has(model.CodeDuplicate) {
		t.Errorf("a table named like a class: %v", err)
	}
	// A table with rows is not redefined; an empty one is the next version.
	_, err = s.DefineTable(ctx, []byte(strings.Replace(ratesTable, `"name": "text"`, `"name": "text", "note": "text"`, 1)))
	if !errors.As(err, &diags) || !diags.Has(model.CodeNotSupported) {
		t.Errorf("redefining with rows: %v", err)
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Empty", "columns": {"a": "text"}, "key": ["a"]}`)); err != nil {
		t.Fatal(err)
	}
	if v2, err := s.DefineTable(ctx, []byte(`{"name": "Empty", "columns": {"a": "text", "b": "int"}, "key": ["a"]}`)); err != nil || v2.Version != 2 || len(v2.Columns) != 2 {
		t.Errorf("redefining an empty table: %v %+v", err, v2)
	}
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Empty", "version": 9, "columns": {"a": "text"}, "key": ["a"]}`)); !errors.As(err, &diags) || !diags.Has(model.CodeBadValue) {
		t.Errorf("wrong version: %v", err)
	}
}

func TestLookup(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	setupTables(t, s)
	if _, err := s.DefineClass(ctx, []byte(order)); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Order", Values{"currency": "EUR", "net": "100.00", "sku": "A1", "qty": 12})
	if err != nil {
		t.Fatal(err)
	}
	want := Values{"currency": "EUR", "net": "100.00", "sku": "A1", "qty": 12.0, "rate": "4.321000", "net_pln": "432.10", "fee": "1.00 EUR", "unit_price": "9.50 PLN", "label": "Euro order"}
	if !reflect.DeepEqual(doc.Values, want) {
		t.Errorf("created:\n%s\nwant\n%s", dump(doc.Values), dump(want))
	}
	// The indexed lookups built the cache; the statement lookup prepared
	// its statement.
	rates := s.tables["RATES"]
	if rates.cache == nil || len(rates.cache.rows) != 2 || len(rates.cache.byCol["CODE"]) != 2 {
		t.Errorf("rates cache %+v", rates.cache)
	}
	if prices := s.tables["PRICES"]; prices.cache != nil {
		t.Errorf("the statement path built a cache")
	}
	statements := 0
	for _, p := range s.plans {
		if p.stmt != nil {
			statements++
		}
	}
	if statements != 1 {
		t.Errorf("%d prepared lookup statements, want 1", statements)
	}
	// A change of the key recomputes what depends on it; the default
	// does not run again.
	doc2, err := s.Set(ctx, "Order", doc.ID, Values{"currency": "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if doc2.Values["rate"] != "3.987600" || doc2.Values["net_pln"] != "398.76" || doc2.Values["fee"] != "0.50 USD" || doc2.Values["label"] != "Euro order" {
		t.Errorf("after the set: %s", dump(doc2.Values))
	}
	// No match is blank, and arithmetic on it reads 0.
	doc3, err := s.Set(ctx, "Order", doc.ID, Values{"currency": "CHF", "sku": "Z9"})
	if err != nil {
		t.Fatal(err)
	}
	if doc3.Values["rate"] != nil || doc3.Values["net_pln"] != "0.00" || doc3.Values["fee"] != nil || doc3.Values["unit_price"] != nil {
		t.Errorf("no match: %s", dump(doc3.Values))
	}
	// An import drops the index; the next commit that recomputes the
	// lookup sees the new rows.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "5.000000", "fee": "2.00 EUR", "name": "Euro"}}); err != nil {
		t.Fatal(err)
	}
	if rates.cache != nil {
		t.Error("the import kept the cache")
	}
	doc4, err := s.Set(ctx, "Order", doc.ID, Values{"currency": "EUR", "sku": "A1"})
	if err != nil {
		t.Fatal(err)
	}
	if doc4.Values["rate"] != "5.000000" || doc4.Values["net_pln"] != "500.00" || doc4.Values["fee"] != "2.00 EUR" || doc4.Values["unit_price"] != "9.50 PLN" {
		t.Errorf("after the import: %s", dump(doc4.Values))
	}
	// A preview reads the tables too and writes nothing.
	p, err := s.Preview(ctx, "Order", doc.ID, Values{"currency": "USD", "qty": 1}, "")
	if err != nil || p.Document.Values["rate"] != "3.987600" || p.Document.Values["unit_price"] != "9.50 PLN" {
		t.Errorf("preview: %v %s", err, dump(p))
	}
	// A class may not read a table the store lacks, and a table a class
	// reads is not redefined.
	_, err = s.DefineClass(ctx, []byte(`{"name": "Quote", "variables": {"c": {"type": "text"}, "r": {"formula": "LOOKUP(Fx, rate, code = THIS.c)"}}, "states": {"initial": "open", "list": ["open"]}}`))
	var diags model.Diagnostics
	if !errors.As(err, &diags) || !diags.Has(model.CodeBadFormula) || !strings.Contains(diags.Error(), "unknown source Fx") {
		t.Errorf("unknown table in a class: %v", err)
	}
	_, err = s.DefineTable(ctx, []byte(strings.Replace(ratesTable, `"name": "text"`, `"name": "text", "note": "text"`, 1)))
	if !errors.As(err, &diags) || !diags.Has(model.CodeNotSupported) || !strings.Contains(diags.Error(), "the class Order reads the table") {
		t.Errorf("redefining a table in use: %v", err)
	}
	// Everything comes back after a reopen, tables before classes.
	path := s.Path()
	s.Close()
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := strings.Join(s2.Tables(), ","); got != "Rates,Prices" {
		t.Errorf("tables after reopen: %s", got)
	}
	doc5, err := s2.Set(ctx, "Order", doc.ID, Values{"currency": "USD"})
	if err != nil || doc5.Values["rate"] != "3.987600" || doc5.Version != 5 {
		t.Errorf("after reopen: %v %s", err, dump(doc5))
	}
}

// TestLookup_StatementPathAgrees runs the same class with the index
// turned off by a tiny MaxIndexedRows, so every lookup is a statement,
// and expects the same documents.
func TestLookup_StatementPathAgrees(t *testing.T) {
	indexed, _ := openStore(t, Options{})
	statements, _ := openStore(t, Options{MaxIndexedRows: 1})
	ctx := context.Background()
	for _, s := range []*Store{indexed, statements} {
		setupTables(t, s)
		if _, err := s.DefineClass(ctx, []byte(order)); err != nil {
			t.Fatal(err)
		}
	}
	if statements.MaxIndexedRows() != 1 {
		t.Errorf("MaxIndexedRows %d", statements.MaxIndexedRows())
	}
	inputs := []Values{
		{"currency": "EUR", "net": "100.00", "sku": "A1", "qty": 12},
		{"currency": "USD", "net": "7.50", "sku": "B2", "qty": 1},
		{"currency": "CHF", "net": "1.00", "sku": "A1", "qty": 0},
		{"currency": "eur", "net": "1.00"},
		{"currency": "USD", "sku": "A1", "qty": 10},
	}
	for i, in := range inputs {
		a, err := indexed.Create(ctx, "Order", in, WithID("o"+itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		b, err := statements.Create(ctx, "Order", in, WithID("o"+itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.Values, b.Values) {
			t.Errorf("input %d: index\n%s\nstatement\n%s", i, dump(a.Values), dump(b.Values))
		}
	}
	if rates := statements.tables["RATES"]; !rates.tooBig || rates.cache != nil {
		t.Errorf("the small limit did not turn the index off: %+v", rates)
	}
	if rates := indexed.tables["RATES"]; rates.cache == nil {
		t.Error("the index was not built")
	}
	// The limit is the database's and may change on open.
	path := statements.Path()
	statements.Close()
	s3, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s3.MaxIndexedRows() != 1 {
		t.Errorf("the limit was not kept: %d", s3.MaxIndexedRows())
	}
	s3.Close()
	s4, err := Open(path, Options{MaxIndexedRows: 500})
	if err != nil {
		t.Fatal(err)
	}
	defer s4.Close()
	if s4.MaxIndexedRows() != 500 {
		t.Errorf("the limit did not change: %d", s4.MaxIndexedRows())
	}
}

func TestCanonKey(t *testing.T) {
	dec := formulaDecimal(2)
	cases := []struct {
		a, b  string
		equal bool
	}{
		{"1.50", "1.5", true}, {"0", "0.00", true}, {"1.50", "1.51", false}, {"-0.10", "-0.1", true},
	}
	for _, tc := range cases {
		a := canonKey(mustExact(t, tc.a), dec)
		b := canonKey(mustExact(t, tc.b), dec)
		if (a == b) != tc.equal {
			t.Errorf("%s vs %s: equal %v", tc.a, tc.b, a == b)
		}
	}
	if canonKey(blankValue(), dec) != canonKey(mustExact(t, "0.00"), dec) {
		t.Error("a blank decimal key is 0")
	}
	if canonKey(blankValue(), formulaText()) != canonKey(textValue(""), formulaText()) {
		t.Error("a blank text key is the empty text")
	}
	if canonKey(blankValue(), formulaNumber()) != canonKey(numberValue(0), formulaNumber()) {
		t.Error("a blank number key is 0")
	}
	if canonKey(numberValue(1), formulaNumber()) == canonKey(numberValue(2), formulaNumber()) {
		t.Error("distinct numbers collide")
	}
}

func TestOpenRejectsBadPath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "dir", "x.fado"), Options{}); err == nil {
		t.Error("a missing directory opened")
	}
}

// Small constructors for the canonical key test, kept out of the way of
// the formula package's own names.
func formulaDecimal(scale int) formula.Type { return formula.DecimalType(scale) }
func formulaText() formula.Type             { return formula.Scalar(formula.KindText) }
func formulaNumber() formula.Type           { return formula.Scalar(formula.KindNumber) }
func blankValue() formula.Value             { return formula.Blank() }
func textValue(s string) formula.Value      { return formula.Text(s) }
func numberValue(f float64) formula.Value   { return formula.Number(f) }

// mustExact parses a decimal at the scale it is written with.
func mustExact(t *testing.T, s string) formula.Value {
	t.Helper()
	scale := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		scale = len(s) - i - 1
	}
	v, err := formula.ParseExact(s, formula.DecimalType(scale))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
