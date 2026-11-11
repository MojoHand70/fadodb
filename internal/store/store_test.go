package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// invoice is the test class: inputs of every stored kind with defaults,
// an enum, a range, two objects of the vocabulary, a rows and an array
// variable, auto formulas and a title.
const invoice = `{
  "name": "Invoice",
  "label": "Invoice",
  "title": "\"INV-\" & number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "customer": {"type": "ref", "class": "Customer"},
    "issued":   {"type": "date", "default": "TODAY()"},
    "due":      {"type": "date", "default": "ADDDAYS(issued, 30)"},
    "owner":    {"type": "text", "default": "USER()"},
    "status":   {"type": "text", "values": ["open", "paid"], "default": "\"open\""},
    "discount": {"type": "decimal", "scale": 2, "default": "0", "min": 0, "max": "0.50"},
    "net":      {"type": "money", "scale": 2},
    "priority": {"type": "number"},
    "parts":    {"type": "number"},
    "notes":    {"type": "text"},
    "settings": {"type": "json"},
    "shipping": {"type": "object", "schema": "Address"},
    "contact":  {"type": "object", "schema": "Person"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "number", "price": "decimal(2)"}},
    "tags":     {"type": "array<text>"},
    "total":     {"formula": "ROUND(net * (1 - discount), 2)"},
    "ship_city": {"formula": "shipping.city"},
    "per_part":  {"formula": "IF(ISBLANK(parts), BLANK, priority / parts)"},
    "label":     {"formula": "number & \" for \" & contact.name"}
  },
  "states": {"initial": "draft", "list": ["draft", "approved", "paid"], "final": ["paid"]}
}`

// clock is a test clock that advances one second per reading.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

var start = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func openStore(t *testing.T, o Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.fado")
	if o.Now == nil {
		o.Now = (&clock{now: start}).Now
	}
	// A test runs the outbox when it says so, with Drain; the tests of
	// the worker open their store themselves.
	o.Outbox.Manual = true
	s, err := Open(path, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func defineInvoice(t *testing.T, s *Store) *model.Class {
	t.Helper()
	cls, err := s.DefineClass(context.Background(), []byte(invoice))
	if err != nil {
		t.Fatal(err)
	}
	return cls
}

var alice = Principal{ID: "alice", Groups: []string{"sales"}, Today: time.Date(2026, 11, 6, 23, 30, 0, 0, time.FixedZone("CET", 3600))}

func sampleValues() Values {
	return Values{
		"number":   "2026-0042",
		"customer": "Customer:42",
		"net":      "100.00 PLN",
		"priority": 3,
		"parts":    4,
		"settings": `{"reminders": true}`,
		"shipping": Values{"line1": "Długa 1", "city": "Gdańsk", "postal_code": "80-831", "country": "PL"},
		"contact":  map[string]any{"id": "anna.k", "name": "Anna Kowalska", "email": "anna@acme.example"},
		"lines":    []any{Values{"sku": "A1", "qty": 2, "price": "9.50"}, Values{"sku": "B2", "qty": 1.5, "price": 4}},
		"tags":     []string{"urgent", "export"},
	}
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return fmt.Sprintf("not a store error: %v", err)
}

func TestOpen(t *testing.T) {
	s, path := openStore(t, Options{})
	if s.Synchronous() != "NORMAL" || s.Limits().MaxText != 65536 {
		t.Errorf("defaults: synchronous %s, max_text %d", s.Synchronous(), s.Limits().MaxText)
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal mode %s %v", mode, err)
	}
	var auto int
	if err := s.writer.QueryRowContext(context.Background(), "PRAGMA wal_autocheckpoint").Scan(&auto); err != nil || auto != 0 {
		t.Errorf("wal_autocheckpoint %d %v: the committing connection never checkpoints (ADR-0035)", auto, err)
	}
	var sync int
	if err := s.writer.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&sync); err != nil || sync != 1 {
		t.Errorf("synchronous %d %v, want 1 (NORMAL)", sync, err)
	}
	s.Close()
	if _, err := Open(path, Options{Synchronous: "FULL"}); code(err) != CodeBadValue {
		t.Errorf("reopening with another synchronous setting: %v", err)
	}
	if _, err := Open(path, Options{MaxText: 1024}); code(err) != CodeBadValue {
		t.Errorf("reopening with another text limit: %v", err)
	}
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Synchronous() != "NORMAL" {
		t.Errorf("reopened synchronous %s", s2.Synchronous())
	}

	full, err := Open(filepath.Join(t.TempDir(), "full.fado"), Options{Synchronous: "full", MaxText: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	if full.Synchronous() != "FULL" || full.Limits().MaxText != 1024 {
		t.Errorf("full: synchronous %s, max_text %d", full.Synchronous(), full.Limits().MaxText)
	}
	if err := full.writer.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&sync); err != nil || sync != 2 {
		t.Errorf("synchronous %d %v, want 2 (FULL)", sync, err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "x.fado"), Options{Synchronous: "OFF"}); code(err) != CodeBadValue {
		t.Errorf("synchronous OFF: %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "x.fado"), Options{MaxText: 10}); code(err) != CodeBadValue {
		t.Errorf("text limit below the floor: %v", err)
	}
}

func TestDefineClass(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	cls := defineInvoice(t, s)
	if cls.Version != 1 || len(s.Classes()) != 1 {
		t.Errorf("version %d, classes %v", cls.Version, s.Classes())
	}
	rows, err := s.db.Query("SELECT type, name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var typ, name string
		rows.Scan(&typ, &name)
		if strings.Contains(name, "Invoice") {
			names = append(names, typ+":"+name)
		}
	}
	rows.Close()
	want := "index:doc_Invoice_history_valid table:doc_Invoice table:doc_Invoice__lines table:doc_Invoice__tags table:doc_Invoice_history " +
		"view:Invoice view:Invoice_actions view:Invoice_events view:Invoice_history view:Invoice_lines view:Invoice_lines_history view:Invoice_states view:Invoice_tags view:Invoice_tags_history"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("objects:\n%s\nwant\n%s", got, want)
	}
	cols, err := s.db.Query("SELECT name, type FROM pragma_table_info('doc_Invoice_history')")
	if err != nil {
		t.Fatal(err)
	}
	var colList []string
	for cols.Next() {
		var n, typ string
		cols.Scan(&n, &typ)
		colList = append(colList, n+" "+typ)
	}
	cols.Close()
	wantCols := "id TEXT title TEXT state TEXT version INTEGER class_version INTEGER created_at INTEGER updated_at INTEGER state_entered_at INTEGER valid_from INTEGER valid_to INTEGER actor_id TEXT event_id TEXT " +
		"number TEXT customer TEXT issued INTEGER due INTEGER owner TEXT status TEXT discount INTEGER net INTEGER net_currency TEXT priority REAL parts REAL notes TEXT settings TEXT " +
		"shipping__line1 TEXT shipping__line2 TEXT shipping__city TEXT shipping__postal_code TEXT shipping__region TEXT shipping__country TEXT " +
		"contact__id TEXT contact__name TEXT contact__email TEXT contact__phone TEXT contact__avatar TEXT total INTEGER total_currency TEXT ship_city TEXT per_part REAL label TEXT " +
		// who made the document and where it came from, after the variables (ADR-0047)
		"created_by TEXT updated_by TEXT state_entered_by TEXT state_entered_from TEXT"
	if got := strings.Join(colList, " "); got != wantCols {
		t.Errorf("history columns:\n%s\nwant\n%s", got, wantCols)
	}

	// The same definition again is a no-op.
	again, err := s.DefineClass(ctx, []byte(invoice))
	if err != nil || again != cls {
		t.Errorf("redefining the same JSON: %v, same %v", err, again == cls)
	}
	// A different definition of an empty class is the next version.
	v2 := strings.Replace(invoice, `"notes":    {"type": "text"},`, `"notes": {"type": "text"}, "memo": {"type": "text"},`, 1)
	cls2, err := s.DefineClass(ctx, []byte(v2))
	if err != nil || cls2.Version != 2 {
		t.Fatalf("redefinition: %v, version %d", err, cls2.Version)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM class_versions WHERE class = 'Invoice'").Scan(&n); err != nil || n != 2 {
		t.Errorf("class_versions %d %v", n, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_table_info('doc_Invoice') WHERE name = 'memo'").Scan(&n); err != nil || n != 1 {
		t.Errorf("memo column %d %v", n, err)
	}
	// A wrong version number is refused; the right one accepted.
	v3 := strings.Replace(v2, `"name": "Invoice",`, `"name": "Invoice", "version": 7,`, 1)
	_, err = s.DefineClass(ctx, []byte(v3))
	var diags model.Diagnostics
	if !errors.As(err, &diags) || !diags.Has(model.CodeBadValue) {
		t.Errorf("version 7 after 2: %v", err)
	}
	if diags := s.Validate([]byte(v3)); !diags.Has(model.CodeBadValue) {
		t.Errorf("Validate agrees: %v", diags)
	}
	v3 = strings.Replace(v2, `"name": "Invoice",`, `"name": "Invoice", "version": 3,`, 1)
	if cls3, err := s.DefineClass(ctx, []byte(v3)); err != nil || cls3.Version != 3 {
		t.Errorf("version 3 after 2: %v", err)
	}
	// With documents, a redefinition is the next version and touches no
	// document (ADR-0039); the versions test has the rest.
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	v4 := strings.Replace(v2, `"memo": {"type": "text"},`, `"memo": {"type": "text"}, "note2": {"type": "text"},`, 1)
	if cls4, err := s.DefineClass(ctx, []byte(v4)); err != nil || cls4.Version != 4 {
		t.Errorf("redefining under documents: %v", err)
	}
	if diags := s.Validate([]byte(v4)); len(diags) > 0 {
		t.Errorf("Validate under documents: %v", diags)
	}
	if again, err := s.DefineClass(ctx, []byte(v4)); err != nil || again.Version != 4 {
		t.Errorf("the same definition under documents is still a no-op: %v", err)
	}
	if got, err := s.Get(ctx, "Invoice", doc.ID); err != nil || got.ClassVersion != 3 || got.Values["note2"] != nil {
		t.Errorf("the document is read under its version: %v %s", err, dump(got))
	}
	// Diagnostics of a bad definition come back as the error.
	_, err = s.DefineClass(ctx, []byte(`{"name": "Bad", "variables": {"x": {"type": "nope"}}}`))
	if !errors.As(err, &diags) || !diags.Has(model.CodeUnknownType) {
		t.Errorf("bad class: %v", err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Bad", "variables": `)); !errors.As(err, &diags) || !diags.Has(model.CodeBadJSON) {
		t.Errorf("bad json: %v", err)
	}
	// A view name another class would need is taken.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Invoice_lines", "variables": {"x": {"type": "text"}}}`)); code(err) != CodeDuplicate {
		t.Errorf("a class named like a view: %v", err)
	}
	// A class in quotes-needing case: a SQL keyword as a name.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Order", "variables": {"group": {"type": "text"}, "select": {"type": "number"}}, "states": {"initial": "open", "list": ["open"]}}`)); err != nil {
		t.Errorf("SQL keywords as names: %v", err)
	}
	if doc, err := s.Create(ctx, "Order", Values{"group": "a", "select": 1}); err != nil || doc.Values["select"] != 1.0 {
		t.Errorf("create an Order: %v %v", err, doc)
	}
}

func TestDefineSchema(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	sch, err := s.DefineSchema(ctx, []byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "text"}}`))
	if err != nil || sch.Version != 1 {
		t.Fatalf("define: %v", err)
	}
	if again, err := s.DefineSchema(ctx, []byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "text"}}`)); err != nil || again != sch {
		t.Errorf("same schema again: %v", err)
	}
	if sch2, err := s.DefineSchema(ctx, []byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "int"}}`)); err != nil || sch2.Version != 2 {
		t.Errorf("unused schema redefined: %v", err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Task", "variables": {"who": {"type": "object", "schema": "Employee"}, "badge": {"formula": "who.badge"}}, "states": {"initial": "open", "list": ["open"]}}`)); err != nil {
		t.Fatal(err)
	}
	_, err = s.DefineSchema(ctx, []byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "text"}}`))
	var diags model.Diagnostics
	if !errors.As(err, &diags) || !diags.Has(model.CodeNotSupported) {
		t.Errorf("schema in use redefined: %v", err)
	}
	if _, err := s.DefineSchema(ctx, []byte(`{"name": "Person", "fields": {"x": "text"}}`)); !errors.As(err, &diags) || !diags.Has(model.CodeReservedName) {
		t.Errorf("vocabulary name: %v", err)
	}
	doc, err := s.Create(ctx, "Task", Values{"who": Values{"name": "Bo", "badge": 7}})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["badge"] != "7" || doc.Values["who"].(map[string]any)["badge"] != "7" {
		t.Errorf("int field: %v", doc.Values)
	}
}

func TestCreate(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineInvoice(t, s)
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID == "" || len(doc.ID) != 26 || doc.Version != 1 || doc.State != "draft" || doc.Title != "INV-2026-0042" || doc.Class != "Invoice" {
		t.Errorf("header: %+v", doc)
	}
	if doc.CreatedAt == 0 || doc.CreatedAt != doc.UpdatedAt || doc.StateEnteredAt != doc.CreatedAt {
		t.Errorf("times: %+v", doc)
	}
	want := Values{
		"number": "2026-0042", "customer": "Customer:42", "issued": "2026-11-08", "due": "2026-12-08", "owner": "alice", "status": "open",
		"discount": "0.00", "net": "100.00 PLN", "priority": 3.0, "parts": 4.0, "notes": nil, "settings": json.RawMessage(`{"reminders": true}`),
		"shipping": map[string]any{"line1": "Długa 1", "line2": nil, "city": "Gdańsk", "postal_code": "80-831", "region": nil, "country": "PL"},
		"contact":  map[string]any{"id": "anna.k", "name": "Anna Kowalska", "email": "anna@acme.example", "phone": nil, "avatar": nil},
		"lines":    []any{map[string]any{"sku": "A1", "qty": 2.0, "price": "9.50"}, map[string]any{"sku": "B2", "qty": 1.5, "price": "4.00"}},
		"tags":     []any{"urgent", "export"},
		"total":    "100.00 PLN", "ship_city": "Gdańsk", "per_part": 0.75, "label": "2026-0042 for Anna Kowalska",
	}
	if !reflect.DeepEqual(doc.Values, want) {
		t.Errorf("values:\n%s\nwant\n%s", dump(doc.Values), dump(want))
	}
	got, err := s.Get(ctx, "Invoice", doc.ID)
	if err != nil || !reflect.DeepEqual(got, doc) {
		t.Errorf("Get: %v\n%s\nwant\n%s", err, dump(got), dump(doc))
	}
	hist, err := s.History(ctx, "Invoice", doc.ID)
	if err != nil || len(hist) != 1 || hist[0].ValidTo != 0 || hist[0].ActorID != "alice" || hist[0].EventID == "" || !reflect.DeepEqual(hist[0].Document, *doc) {
		t.Errorf("history: %v %s", err, dump(hist))
	}
	events, err := s.Events(ctx, "Invoice", doc.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %v %s", err, dump(events))
	}
	ev := events[0]
	if ev.Kind != EventCreated || ev.ID != hist[0].EventID || ev.ActorID != "alice" || ev.BeforeVersion != 0 || ev.AfterVersion != 1 || ev.BusinessDate != "2026-11-06" || ev.TS != doc.CreatedAt {
		t.Errorf("event: %+v", ev)
	}
	// Views see the document with the declared names.
	var city string
	var total int64
	if err := s.db.QueryRow(`SELECT shipping__city, total FROM Invoice WHERE id = ?`, doc.ID).Scan(&city, &total); err != nil || city != "Gdańsk" || total != 10000 {
		t.Errorf("view: %s %d %v", city, total, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM Invoice_lines WHERE doc_id = ?`, doc.ID).Scan(&n); err != nil || n != 2 {
		t.Errorf("lines view: %d %v", n, err)
	}

	// A caller's id, a business date, a blank object, no composites.
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	doc2, err := s.Create(ctx, "Invoice", Values{"number": "2", "net": "10.00 EUR", "shipping": nil}, WithID("inv_2"), BusinessDate(day))
	if err != nil {
		t.Fatal(err)
	}
	if doc2.ID != "inv_2" || doc2.Values["shipping"] != nil || doc2.Values["ship_city"] != nil || doc2.Values["owner"] != nil ||
		!reflect.DeepEqual(doc2.Values["lines"], []any{}) || doc2.Values["label"] != "2 for " || doc2.Values["per_part"] != nil {
		t.Errorf("doc2: %s", dump(doc2.Values))
	}
	if events, _ := s.Events(ctx, "Invoice", "inv_2"); len(events) != 1 || events[0].BusinessDate != "2026-10-31" || events[0].ActorID != "" {
		t.Errorf("doc2 events: %s", dump(events))
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "3"}, WithID("inv_2")); code(err) != CodeDuplicate {
		t.Errorf("duplicate id: %v", err)
	}
	// A document without a business day anywhere takes the clock's UTC day.
	doc3, _ := s.Create(ctx, "Invoice", Values{"number": "3"})
	if events, _ := s.Events(ctx, "Invoice", doc3.ID); events[0].BusinessDate != "2026-11-08" {
		t.Errorf("default business date: %s", events[0].BusinessDate)
	}
	if _, err := s.Create(ctx, "Nope", Values{}); code(err) != CodeUnknownClass {
		t.Errorf("unknown class: %v", err)
	}
}

func TestCreate_Refusals(t *testing.T) {
	s, _ := openStore(t, Options{MaxText: 64})
	ctx := context.Background()
	defineInvoice(t, s)
	cases := []struct {
		name   string
		values Values
		code   string
		vari   string
	}{
		{"required", Values{}, CodeRequired, "number"},
		{"required blank", Values{"number": nil}, CodeRequired, "number"},
		{"computed", Values{"number": "1", "total": "5.00 PLN"}, CodeReadOnly, "total"},
		{"unknown", Values{"number": "1", "nope": 1}, CodeUnknownVariable, "nope"},
		{"twice", Values{"number": "1", "Number": "2"}, CodeDuplicate, "number"},
		{"enum", Values{"number": "1", "status": "lost"}, CodeNotInValues, "status"},
		{"below min", Values{"number": "1", "discount": "-0.10"}, CodeOutOfRange, "discount"},
		{"above max", Values{"number": "1", "discount": "0.51"}, CodeOutOfRange, "discount"},
		{"inexact", Values{"number": "1", "discount": 0.125}, CodeBadValue, "discount"},
		{"not text", Values{"number": 1}, CodeBadValue, "number"},
		{"long text", Values{"number": strings.Repeat("x", 65)}, CodeOutOfRange, "number"},
		{"money without currency", Values{"number": "1", "net": "10.00"}, CodeBadValue, "net"},
		{"wrong ref class", Values{"number": "1", "customer": "Supplier:1"}, CodeBadValue, "customer"},
		{"bare ref", Values{"number": "1", "customer": "42"}, CodeBadValue, "customer"},
		{"bad date", Values{"number": "1", "issued": "yesterday"}, CodeBadValue, "issued"},
		{"bad json", Values{"number": "1", "settings": "{oops"}, CodeBadValue, "settings"},
		{"object not a map", Values{"number": "1", "shipping": "Gdańsk"}, CodeBadValue, "shipping"},
		{"unknown field", Values{"number": "1", "shipping": Values{"town": "Gdańsk"}}, CodeUnknownVariable, "shipping.town"},
		{"rows not a list", Values{"number": "1", "lines": Values{"sku": "A"}}, CodeBadValue, "lines"},
		{"bad row field", Values{"number": "1", "lines": []any{Values{"sku": "A", "qty": "two"}}}, CodeBadValue, "lines[0].qty"},
		{"array element", Values{"number": "1", "tags": []any{"a", 2}}, CodeBadValue, "tags[1]"},
		{"division by zero", Values{"number": "1", "priority": 1, "parts": 0}, "div_by_zero", "per_part"},
	}
	for _, tc := range cases {
		_, err := s.Create(ctx, "Invoice", tc.values)
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.code || e.Variable != tc.vari {
			t.Errorf("%s: want %s on %s, got %v", tc.name, tc.code, tc.vari, err)
		}
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM events").Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused commit writes nothing: %d events", n)
	}
	// Too many elements.
	many := make([]any, MaxElements+1)
	for i := range many {
		many[i] = "t"
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "1", "tags": many}); code(err) != CodeTooManyElements {
		t.Errorf("too many elements: %v", err)
	}
}

func TestSet(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineInvoice(t, s)
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	bob := Principal{ID: "bob"}
	doc2, err := s.Set(ctx, "Invoice", doc.ID, Values{"discount": "0.10"}, As(bob))
	if err != nil {
		t.Fatal(err)
	}
	if doc2.Version != 2 || doc2.Values["discount"] != "0.10" || doc2.Values["total"] != "90.00 PLN" || doc2.UpdatedAt <= doc.UpdatedAt || doc2.CreatedAt != doc.CreatedAt {
		t.Errorf("after the set: %+v", doc2)
	}
	// Everything else is carried forward, the child rows included.
	for _, k := range []string{"lines", "tags", "shipping", "contact", "label", "ship_city", "issued"} {
		if !reflect.DeepEqual(doc2.Values[k], doc.Values[k]) {
			t.Errorf("%s changed: %v -> %v", k, doc.Values[k], doc2.Values[k])
		}
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM doc_Invoice__lines WHERE doc_id = ? AND version = 2", doc.ID).Scan(&n); err != nil || n != 2 {
		t.Errorf("lines of version 2: %d %v", n, err)
	}
	hist, _ := s.History(ctx, "Invoice", doc.ID)
	if len(hist) != 2 || hist[0].ValidTo != hist[1].ValidFrom || hist[0].ValidTo == 0 || hist[1].ValidTo != 0 || hist[1].ActorID != "bob" || hist[0].Values["total"] != "100.00 PLN" {
		t.Errorf("history: %s", dump(hist))
	}
	events, _ := s.Events(ctx, "Invoice", doc.ID)
	if len(events) != 2 || events[1].Kind != EventSet || events[1].BeforeVersion != 1 || events[1].AfterVersion != 2 || events[1].ActorID != "bob" || events[1].BusinessDate != "2026-11-08" {
		t.Errorf("events: %s", dump(events))
	}
	if v1, err := s.Version(ctx, "Invoice", doc.ID, 1); err != nil || v1.Values["total"] != "100.00 PLN" || len(v1.Values["lines"].([]any)) != 2 {
		t.Errorf("version 1: %v %s", err, dump(v1))
	}
	if _, err := s.Version(ctx, "Invoice", doc.ID, 9); code(err) != CodeUnknownDocument {
		t.Errorf("version 9: %v", err)
	}

	// A set that changes nothing is no commit.
	same, err := s.Set(ctx, "Invoice", doc.ID, Values{"discount": "0.10", "lines": doc.Values["lines"], "shipping": doc.Values["shipping"]})
	if err != nil || same.Version != 2 {
		t.Errorf("no-op set: %v version %d", err, same.Version)
	}
	if events, _ := s.Events(ctx, "Invoice", doc.ID); len(events) != 2 {
		t.Errorf("a no-op set wrote an event")
	}

	// An object is replaced whole; rows are replaced; an unrelated set
	// recomputes only what depends on it.
	doc3, err := s.Set(ctx, "Invoice", doc.ID, Values{"shipping": Values{"city": "Sopot"}, "lines": []any{Values{"sku": "C3", "qty": 1, "price": "1.00"}}, "tags": nil})
	if err != nil {
		t.Fatal(err)
	}
	if doc3.Version != 3 || doc3.Values["ship_city"] != "Sopot" || doc3.Values["shipping"].(map[string]any)["country"] != nil ||
		len(doc3.Values["lines"].([]any)) != 1 || !reflect.DeepEqual(doc3.Values["tags"], []any{}) || doc3.Values["total"] != "90.00 PLN" {
		t.Errorf("doc3: %s", dump(doc3.Values))
	}
	// A blank for a required input, a computed variable, the unknown.
	for _, tc := range []struct {
		values Values
		code   string
	}{
		{Values{"number": nil}, CodeRequired},
		{Values{"total": "1.00 PLN"}, CodeReadOnly},
		{Values{"nope": 1}, CodeUnknownVariable},
		{Values{"status": "lost"}, CodeNotInValues},
		{Values{"parts": 0}, "div_by_zero"},
	} {
		if _, err := s.Set(ctx, "Invoice", doc.ID, tc.values); code(err) != tc.code {
			t.Errorf("set %v: want %s, got %v", tc.values, tc.code, err)
		}
	}
	if _, err := s.Set(ctx, "Invoice", "nope", Values{"notes": "x"}); code(err) != CodeUnknownDocument {
		t.Errorf("unknown document: %v", err)
	}
	if got, _ := s.Get(ctx, "Invoice", doc.ID); got.Version != 3 {
		t.Errorf("refused sets left version %d", got.Version)
	}
}

func TestDelete(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineInvoice(t, s)
	doc, _ := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	if err := s.Delete(ctx, "Invoice", doc.ID, As(alice)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "Invoice", doc.ID); code(err) != CodeUnknownDocument {
		t.Errorf("after delete: %v", err)
	}
	if err := s.Delete(ctx, "Invoice", doc.ID); code(err) != CodeUnknownDocument {
		t.Errorf("second delete: %v", err)
	}
	hist, _ := s.History(ctx, "Invoice", doc.ID)
	if len(hist) != 1 || hist[0].ValidTo == 0 {
		t.Errorf("history after delete: %s", dump(hist))
	}
	events, _ := s.Events(ctx, "Invoice", doc.ID)
	if len(events) != 2 || events[1].Kind != EventDeleted || events[1].BeforeVersion != 1 || events[1].AfterVersion != 0 {
		t.Errorf("events after delete: %s", dump(events))
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "x"}, WithID(doc.ID)); code(err) != CodeDuplicate {
		t.Errorf("an id that existed is not reused: %v", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM Invoice").Scan(&n); err != nil || n != 0 {
		t.Errorf("current rows %d %v", n, err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM Invoice_events WHERE qualifier = 'deleted'").Scan(&n); err != nil || n != 1 {
		t.Errorf("events view %d %v", n, err)
	}
}

func TestKey(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineInvoice(t, s)
	doc, err := s.Create(ctx, "Invoice", sampleValues(), As(alice), Key("create-42"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Create(ctx, "Invoice", sampleValues(), As(alice), Key("create-42"))
	if err != nil || !reflect.DeepEqual(again, doc) {
		t.Errorf("replayed create: %v\n%s\nwant\n%s", err, dump(again), dump(doc))
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "other"}, Key("create-42")); code(err) != CodeKeyReused {
		t.Errorf("key with another body: %v", err)
	}
	if _, err := s.Set(ctx, "Invoice", doc.ID, Values{"notes": "x"}, Key("create-42")); code(err) != CodeKeyReused {
		t.Errorf("key with another operation: %v", err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM Invoice").Scan(&n)
	if n != 1 {
		t.Errorf("%d documents after the replays", n)
	}
	// A replayed set returns the version it made, not the latest.
	v2, _ := s.Set(ctx, "Invoice", doc.ID, Values{"notes": "first"}, Key("set-1"))
	v3, _ := s.Set(ctx, "Invoice", doc.ID, Values{"notes": "second"}, Key("set-2"))
	replay, err := s.Set(ctx, "Invoice", doc.ID, Values{"notes": "first"}, Key("set-1"))
	if err != nil || replay.Version != 2 || !reflect.DeepEqual(replay, v2) || v3.Version != 3 {
		t.Errorf("replayed set: %v version %d", err, replay.Version)
	}
	// A replayed delete.
	if err := s.Delete(ctx, "Invoice", doc.ID, Key("del-1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "Invoice", doc.ID, Key("del-1")); err != nil {
		t.Errorf("replayed delete: %v", err)
	}
	if events, _ := s.Events(ctx, "Invoice", doc.ID); len(events) != 4 || events[0].CommandKey != "create-42" || events[3].CommandKey != "del-1" {
		t.Errorf("events: %s", dump(events))
	}
}

func TestPreview(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineInvoice(t, s)
	p, err := s.Preview(ctx, "Invoice", "", Values{"number": "p1", "net": "50.00 PLN", "parts": 0, "priority": 1}, "", As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if p.Document.Version != 1 || p.Document.Values["total"] != "50.00 PLN" || p.Document.Values["owner"] != "alice" || p.Document.Values["per_part"] != nil {
		t.Errorf("previewed create: %s", dump(p.Document))
	}
	if len(p.Errors) != 1 || p.Errors[0].Code != "div_by_zero" || p.Errors[0].Variable != "per_part" {
		t.Errorf("preview errors: %s", dump(p.Errors))
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM Invoice_history").Scan(&n)
	if n != 0 {
		t.Errorf("a preview wrote %d history rows", n)
	}
	s.db.QueryRow("SELECT count(*) FROM events").Scan(&n)
	if n != 0 {
		t.Errorf("a preview wrote %d events", n)
	}
	doc, _ := s.Create(ctx, "Invoice", sampleValues())
	p, err = s.Preview(ctx, "Invoice", doc.ID, Values{"discount": "0.25"}, "")
	if err != nil || p.Document.Version != 2 || p.Document.Values["total"] != "75.00 PLN" || len(p.Errors) != 0 {
		t.Errorf("previewed set: %v %s", err, dump(p))
	}
	if got, _ := s.Get(ctx, "Invoice", doc.ID); got.Version != 1 || got.Values["total"] != "100.00 PLN" {
		t.Errorf("the preview changed the document: %s", dump(got))
	}
	// A preview of a refused assignment is refused the same way.
	if _, err := s.Preview(ctx, "Invoice", doc.ID, Values{"status": "lost"}, ""); code(err) != CodeNotInValues {
		t.Errorf("preview of a bad value: %v", err)
	}
	if _, err := s.Preview(ctx, "Invoice", doc.ID, Values{}, "approve"); code(err) != CodeUnknownTransition {
		t.Errorf("preview of a transition the class lacks: %v", err)
	}
	// Missing required inputs are reported, not refused.
	p, err = s.Preview(ctx, "Invoice", "", Values{}, "")
	if err != nil || len(p.Errors) != 1 || p.Errors[0].Code != CodeRequired {
		t.Errorf("preview without the required input: %v %s", err, dump(p))
	}
}

func TestReopen(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	s.DefineSchema(ctx, []byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "text"}}`))
	defineInvoice(t, s)
	s.DefineClass(ctx, []byte(`{"name": "Task", "variables": {"who": {"type": "object", "schema": "Employee"}}, "states": {"initial": "open", "list": ["open"]}}`))
	doc, _ := s.Create(ctx, "Invoice", sampleValues(), As(alice))
	task, _ := s.Create(ctx, "Task", Values{"who": Values{"name": "Bo", "badge": "7"}})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	if _, err := s.Create(ctx, "Invoice", Values{}); code(err) != CodeClosed {
		t.Errorf("after close: %v", err)
	}
	s2, err := Open(path, Options{Now: (&clock{now: start.Add(time.Hour)}).Now})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := strings.Join(s2.Classes(), ","); got != "Invoice,Task" {
		t.Errorf("classes %s", got)
	}
	if sch, ok := s2.Schemas().Lookup("Employee"); !ok || sch.Version != 1 {
		t.Errorf("schema after reopen: %v %v", sch, ok)
	}
	got, err := s2.Get(ctx, "Invoice", doc.ID)
	if err != nil || !reflect.DeepEqual(got, doc) {
		t.Errorf("after reopen: %v\n%s", err, dump(got))
	}
	if got, err := s2.Get(ctx, "Task", task.ID); err != nil || !reflect.DeepEqual(got, task) {
		t.Errorf("task after reopen: %v\n%s", err, dump(got))
	}
	if doc2, err := s2.Set(ctx, "Invoice", doc.ID, Values{"discount": "0.50"}); err != nil || doc2.Values["total"] != "50.00 PLN" || doc2.Version != 2 {
		t.Errorf("set after reopen: %v %s", err, dump(doc2))
	}
	// The second process question of ADR-0035: another handle reads the
	// file while this one holds it.
	s3, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if got, err := s3.Get(ctx, "Invoice", doc.ID); err != nil || got.Version != 2 {
		t.Errorf("second handle: %v %s", err, dump(got))
	}
}

func TestConcurrentCommits(t *testing.T) {
	s, _ := openStore(t, Options{Now: time.Now})
	ctx := context.Background()
	defineInvoice(t, s)
	doc, _ := s.Create(ctx, "Invoice", sampleValues())
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				if _, err := s.Create(ctx, "Invoice", Values{"number": fmt.Sprintf("c%d-%d", i, j)}); err != nil {
					errs <- err
				}
				if _, err := s.Set(ctx, "Invoice", doc.ID, Values{"notes": fmt.Sprintf("%d-%d", i, j)}); err != nil {
					errs <- err
				}
				if _, err := s.Get(ctx, "Invoice", doc.ID); err != nil {
					errs <- err
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, _ := s.Get(ctx, "Invoice", doc.ID)
	hist, _ := s.History(ctx, "Invoice", doc.ID)
	if got.Version != 33 || len(hist) != 33 {
		t.Errorf("version %d, %d history rows, want 33", got.Version, len(hist))
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM Invoice").Scan(&n)
	if n != 33 {
		t.Errorf("%d documents, want 33", n)
	}
	s.db.QueryRow("SELECT count(*) FROM Invoice_history WHERE valid_to IS NULL").Scan(&n)
	if n != 33 {
		t.Errorf("%d open history rows, want 33", n)
	}
}

func TestIDs(t *testing.T) {
	var g idGen
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	a, b := g.next(now), g.next(now)
	if len(a) != 26 || len(b) != 26 || a >= b || a[:10] != b[:10] {
		t.Errorf("same millisecond: %s %s", a, b)
	}
	c := g.next(now.Add(time.Millisecond))
	if c <= b || c[:10] == b[:10] {
		t.Errorf("next millisecond: %s after %s", c, b)
	}
	for _, r := range a + b + c {
		if !strings.ContainsRune(crockford, r) {
			t.Errorf("not Crockford base32: %q", r)
		}
	}
}

func dump(x any) string {
	b, _ := json.MarshalIndent(x, "", "  ")
	return string(b)
}
