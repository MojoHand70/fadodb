package store

import (
	"context"
	"strings"
	"testing"
)

// Acme's order with a list that is kept (ADR-0078, item 5): the invoices
// of the order as they were when somebody asked to close it. The order
// is closed only when it has invoices and every one of them is paid, and
// the version the closing makes holds the lines that decision stood on.
// invoices is the live list beside it: the same question, asked whenever
// somebody reads it, kept nowhere.
const keptOrder = `{
 "name": "Order",
 "title": "number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "customer": {"type": "text"},
   "owner":    {"type": "text", "default": "USER()"},
   "invoices": {"label": "Invoices", "mode": "live",
                "formula": "QUERY(Invoice, {number, net, state, margin}, order = REF(), {number})"},
   "settled":  {"type": "rows", "schema": {"number": "text", "net": "money(2)", "state": "text"},
                "formula": "QUERY(Invoice, {number, net, state}, order = REF(), {number})",
                "mode": "triggered", "on": ["before:close", "refresh"]},
   "unpaid":   {"formula": "COUNTIF(settled, state <> \"paid\")"},
   "billed":   {"formula": "SUM(settled.net)"}
 },
 "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
 "transitions": [{"name": "close", "from": "open", "to": "closed",
                  "guard": "COUNT(settled) > 0 AND ALL(settled, state = \"paid\")"}],
 "access": {"read": "INGROUP(\"sales\") OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "owner = USER()", "delete": "FALSE"},
 "rights": {"*": {"settled": {"write": ["finance"]}}}
}`

// openKept is the database of the list tests with the order that keeps
// a list: orders ORD-17 and ORD-18, and four invoices in draft, three of
// ORD-17, of which Piotr wrote one that Jan may not read.
func openKept(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(keptOrder), []byte(listInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"ORD-17", "ORD-18"} {
		if _, err := s.Create(ctx, "Order", Values{"number": o, "customer": "Globex"}, As(liveJan), WithID(o)); err != nil {
			t.Fatal(err)
		}
	}
	for _, inv := range []struct {
		id, order, net string
		who            Principal
	}{{"FV-1", "ORD-17", "1000.00 PLN", liveJan}, {"FV-2", "ORD-17", "250.00 PLN", liveJan}, {"FV-3", "ORD-17", "70.00 PLN", livePiotr}, {"FV-4", "ORD-18", "5.00 PLN", liveJan}} {
		if _, err := s.Create(ctx, "Invoice", Values{"number": inv.id, "order": "Order:" + inv.order, "net": inv.net, "margin": "1.00 PLN"}, As(inv.who), WithID(inv.id)); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

// pay moves an invoice to paid: its owner files it, and Ewa approves it.
func pay(t *testing.T, s *Store, id string, owner Principal) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Transition(ctx, "Invoice", id, "file", nil, As(owner)); err != nil {
		t.Fatalf("file %s: %v", id, err)
	}
	if _, err := s.Transition(ctx, "Invoice", id, "approve", nil, As(liveEwa)); err != nil {
		t.Fatalf("approve %s: %v", id, err)
	}
}

// The list is brought when somebody asks to close the order, the guard
// reads it, the refusal says what it was, and the version the closing
// makes keeps the lines the decision stood on.
func TestKeptList_ADecisionStandsOnTheLinesItSaw(t *testing.T) {
	s, ctx := openKept(t)
	d, err := s.Get(ctx, "Order", "ORD-17", As(liveJan))
	if err != nil || flat(d.Values["settled"]) != "[]" || d.Values["unpaid"] != 0.0 || d.Computed != nil {
		t.Fatalf("an order nobody asked to close: %s, %v", flat(d), err)
	}
	// Jan asks to close it. The three invoices are brought, Piotr's among
	// them, which Jan may not read as a document: a kept list is one list
	// for everybody. None is paid, so the guard refuses, and says what it
	// saw.
	_, err = s.Transition(ctx, "Order", "ORD-17", "close", nil, As(liveJan))
	e, _ := err.(*Error)
	const drafts = `[{"net":"1000.00 PLN","number":"FV-1","state":"draft"},{"net":"250.00 PLN","number":"FV-2","state":"draft"},{"net":"70.00 PLN","number":"FV-3","state":"draft"}]`
	if e == nil || e.Code != CodeGuardFalse || e.Brought == nil || flat((*e.Brought)["settled"]) != drafts {
		t.Fatalf("close with nothing paid: %v, brought %s", err, flat(e))
	}
	if d, _ := s.Get(ctx, "Order", "ORD-17", As(liveJan)); d.Version != 1 || flat(d.Values["settled"]) != "[]" {
		t.Errorf("a refused closing wrote: %s", flat(d))
	}
	// The listing brings the list too, and writes nothing.
	open, err := s.Transitions(ctx, "Order", "ORD-17", As(liveJan))
	if err != nil || len(open) != 1 || open[0].Enabled || open[0].Code != CodeGuardFalse {
		t.Errorf("the listing with nothing paid: %s, %v", flat(open), err)
	}
	pay(t, s, "FV-1", liveJan)
	pay(t, s, "FV-2", liveJan)
	if open, _ := s.Transitions(ctx, "Order", "ORD-17", As(liveJan)); len(open) != 1 || open[0].Enabled {
		t.Errorf("the listing with one invoice unpaid: %s", flat(open))
	}
	pay(t, s, "FV-3", livePiotr)
	if open, _ := s.Transitions(ctx, "Order", "ORD-17", As(liveJan)); len(open) != 1 || !open[0].Enabled {
		t.Errorf("the listing with every invoice paid: %s", flat(open))
	}
	// A preview brings it and writes nothing.
	p, err := s.Preview(ctx, "Order", "ORD-17", nil, "close", As(liveJan))
	const paid = `[{"net":"1000.00 PLN","number":"FV-1","state":"paid"},{"net":"250.00 PLN","number":"FV-2","state":"paid"},{"net":"70.00 PLN","number":"FV-3","state":"paid"}]`
	if err != nil || p.Document.State != "closed" || flat(p.Document.Values["settled"]) != paid {
		t.Fatalf("the preview of the closing: %s, %v", flat(p), err)
	}
	d, err = s.Transition(ctx, "Order", "ORD-17", "close", nil, As(liveJan))
	if err != nil || d.State != "closed" || d.Version != 2 || flat(d.Values["settled"]) != paid {
		t.Fatalf("the closing: %s, %v", flat(d), err)
	}
	// What is worked out from the list follows it, and the list says when
	// it was brought.
	if d.Values["unpaid"] != 0.0 || d.Values["billed"] != "1320.00 PLN" || d.Computed["settled"].Version != 2 {
		t.Errorf("after the closing: unpaid %v, billed %v, the list of version %d", d.Values["unpaid"], d.Values["billed"], d.Computed["settled"].Version)
	}
	// The store has it, version by version.
	for version, want := range map[int64]string{1: "[]", 2: paid} {
		h, err := s.Version(ctx, "Order", "ORD-17", version, As(liveJan))
		if err != nil || flat(h.Values["settled"]) != want {
			t.Errorf("version %d holds %s, %v; want %s", version, flat(h.Values["settled"]), err, want)
		}
	}
	// The live list beside it is still asked for its reader: Jan sees
	// his two invoices, and the kept list has the three.
	live, err := s.Live(ctx, "Order", "ORD-17", "invoices", As(liveJan))
	if err != nil || ids(live) != "FV-1 FV-2" {
		t.Errorf("the live list for Jan: %s, %v", ids(live), err)
	}
}

// A list that is kept is brought again by a refresh, by whoever its
// write right lets, and holds what it was brought as until then.
func TestKeptList_BroughtAgain(t *testing.T) {
	s, ctx := openKept(t)
	// The write right of the list is finance's: Jan may not bring it.
	if _, err := s.Refresh(ctx, "Order", "ORD-17", []string{"settled"}, As(liveJan)); code(err) != CodeForbidden {
		t.Fatalf("Jan refreshes the list: %v; want forbidden", err)
	}
	d, err := s.Refresh(ctx, "Order", "ORD-17", []string{"settled"}, As(liveEwa))
	if err != nil || d.Version != 2 || d.Values["unpaid"] != 3.0 || d.Values["billed"] != "1320.00 PLN" || d.Computed["settled"].Version != 2 {
		t.Fatalf("Ewa refreshes the list: %s, %v", flat(d), err)
	}
	// An invoice moves on and the order does not: the list is kept.
	pay(t, s, "FV-1", liveJan)
	if d, _ := s.Get(ctx, "Order", "ORD-17", As(liveEwa)); d.Version != 2 || d.Values["unpaid"] != 3.0 {
		t.Errorf("after an invoice was paid the order holds %s", flat(d))
	}
	d, err = s.Refresh(ctx, "Order", "ORD-17", nil, As(liveEwa))
	if err != nil || d.Version != 3 || d.Values["unpaid"] != 2.0 || d.Computed["settled"].Version != 3 {
		t.Fatalf("Ewa refreshes the order: %s, %v", flat(d), err)
	}
	// A change of something else carries the list forward as it is.
	d, err = s.Set(ctx, "Order", "ORD-17", Values{"customer": "Globex Ltd"}, As(liveJan))
	if err != nil || d.Version != 4 || d.Values["unpaid"] != 2.0 || d.Computed["settled"].Version != 3 || len(d.Values["settled"].([]any)) != 3 {
		t.Fatalf("a change of the customer: %s, %v", flat(d), err)
	}
	// Nobody sets it: not Jan, whom its write right does not let bring
	// it, and not one who owns the order and is in finance too, whom
	// the right lets bring it again and nothing more.
	if _, err := s.Set(ctx, "Order", "ORD-17", Values{"settled": []any{}}, As(liveJan)); code(err) != CodeForbidden {
		t.Errorf("Jan sets the list: %v; want forbidden", err)
	}
	both := Principal{ID: liveJan.ID, Kind: liveJan.Kind, Groups: []string{"sales", "finance"}}
	if _, err := s.Set(ctx, "Order", "ORD-17", Values{"settled": []any{}}, As(both)); code(err) != CodeReadOnly {
		t.Errorf("a caller its write right lets through sets the list: %v; want read_only", err)
	}
	// The other order has its own list.
	d, err = s.Refresh(ctx, "Order", "ORD-18", nil, As(liveEwa))
	if err != nil || flat(d.Values["settled"]) != `[{"net":"5.00 PLN","number":"FV-4","state":"draft"}]` {
		t.Fatalf("the list of the other order: %s, %v", flat(d), err)
	}
}

// A database opened again compiles the list from its stored definition,
// before the class it reads is at hand, and brings it as before.
func TestKeptList_Reopened(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClasses(ctx, [][]byte{[]byte(keptOrder), []byte(listInvoice)}, asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "ORD-1"}, As(liveJan), WithID("ORD-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Invoice", Values{"number": "FV-9", "order": "Order:ORD-1", "net": "9.00 PLN"}, As(liveJan), WithID("FV-9")); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Refresh(ctx, "Order", "ORD-1", nil, As(liveEwa)); err != nil || len(d.Values["settled"].([]any)) != 1 {
		t.Fatalf("before the database is closed: %s, %v", flat(d), err)
	}
	s.Close()
	s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("open again: %v", err)
	}
	defer s.Close()
	d, err := s.Get(ctx, "Order", "ORD-1", As(liveEwa))
	if err != nil || flat(d.Values["settled"]) != `[{"net":"9.00 PLN","number":"FV-9","state":"draft"}]` {
		t.Fatalf("the list after the database was opened again: %s, %v", flat(d), err)
	}
	pay(t, s, "FV-9", liveJan)
	if d, err = s.Transition(ctx, "Order", "ORD-1", "close", nil, As(liveJan)); err != nil || d.State != "closed" {
		t.Fatalf("the closing after the database was opened again: %s, %v", flat(d), err)
	}
}

// A list that is wrong is refused when its class is defined.
func TestKeptList_RefusedWhenDefined(t *testing.T) {
	s, ctx := openList(t)
	const formula = `"formula": "QUERY(Invoice, {number, net}, owner = THIS.n)", "mode": "triggered", "on": ["refresh"]`
	for _, tc := range []struct {
		name, variable, code, says string
	}{
		{"no type", `"l": {` + formula + `}`, "missing", "says its columns"},
		{"a type that is no rows", `"l": {"type": "text", ` + formula + `}`, "missing", "says its columns"},
		{"no schema", `"l": {"type": "rows", ` + formula + `}`, "missing", "a rows variable has a schema"},
		{"a column the schema has not", `"l": {"type": "rows", "schema": {"number": "text"}, ` + formula + `}`, "missing", "the schema of l has no such column"},
		{"a column of another type", `"l": {"type": "rows", "schema": {"number": "text", "net": "number"}, ` + formula + `}`, "type_mismatch", "money(2) in Invoice"},
		{"a column the QUERY does not list", `"l": {"type": "rows", "schema": {"number": "text", "net": "money(2)", "state": "text"}, ` + formula + `}`, "not_allowed", "which its QUERY does not list"},
		{"a column the source has not", `"l": {"type": "rows", "schema": {"number": "text", "nothing": "text"}, "formula": "QUERY(Invoice, {number, nothing})", "mode": "triggered", "on": ["refresh"]}`, "bad_formula", "unknown column nothing of Invoice"},
		{"a schema on a live list", `"l": {"type": "rows", "schema": {"number": "text"}, "mode": "live", "formula": "QUERY(Invoice, number)"}`, "not_allowed", "the columns of a live list"},
		{"a default", `"l": {"type": "rows", "schema": {"number": "text"}, "default": "1", "formula": "QUERY(Invoice, number)", "mode": "triggered", "on": ["refresh"]}`, "not_allowed", "no default"},
		{"the version of the documents listed", `"l": {"type": "rows", "schema": {"number": "text"}, "formula": "QUERY(Invoice, {number, version})", "mode": "triggered", "on": ["refresh"]}`, "not_supported", "cannot hold the column version"},
		{"another formula that would give rows", `"l": {"type": "rows", "schema": {"number": "text"}, "formula": "n", "mode": "triggered", "on": ["refresh"]}`, "not_supported", "the one list a formula gives is a QUERY"},
	} {
		def := `{"name": "T", "variables": {"n": {"type": "text"}, ` + tc.variable + `}, "states": {"initial": "a", "list": ["a"]}}`
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the class was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s, %q", tc.name, err, tc.code, tc.says)
		}
	}
	// The system columns are kept too, with their types.
	def := `{"name": "T", "variables": {"n": {"type": "text"},
  "l": {"type": "rows", "schema": {"id": "text", "state": "text", "class_version": "number", "created_at": "datetime", "net": "money(2)"},
        "formula": "QUERY(Invoice, {id, state, class_version, created_at, net}, TRUE, {-net})", "mode": "triggered", "on": ["enter:a"]}},
 "states": {"initial": "a", "list": ["a"]}}`
	if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
		t.Fatalf("a list of system columns: %v", err)
	}
	// Brought when the document is created, at the entry of its first
	// state: every invoice, the largest first.
	d, err := s.Create(ctx, "T", nil, WithID("t-1"))
	rows, _ := d.Values["l"].([]any)
	if err != nil || len(rows) != 4 || d.Computed["l"].Version != 1 {
		t.Fatalf("a list brought at the entry of the first state: %s, %v", flat(d), err)
	}
	first, _ := rows[0].(map[string]any)
	if first["id"] != "FV-1" || first["state"] != "draft" || first["class_version"] != 1.0 || first["net"] != "1000.00 PLN" || first["created_at"] == nil {
		t.Errorf("the first row: %s", flat(first))
	}
}

// A list longer than a list may be is not kept: the refresh leaves the
// list as it was and says why, and a closing that needs it is refused.
func TestKeptList_TooLong(t *testing.T) {
	if testing.Short() {
		t.Skip("makes 10,001 documents")
	}
	s, ctx := openKept(t)
	batch := make([]Values, 0, 1000)
	for i := 0; i < MaxElements-2; i++ {
		batch = append(batch, Values{"number": "X-" + itoa(i), "order": "Order:ORD-17", "net": "1.00 PLN"})
		if len(batch) == cap(batch) || i == MaxElements-3 {
			if _, err := s.CreateMany(ctx, "Invoice", batch, As(liveJan)); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	// 10,001 invoices of ORD-17 now: three from the start and 9,998 more.
	d, err := s.Refresh(ctx, "Order", "ORD-17", nil, As(liveEwa))
	if err != nil || d.Computed["settled"].Error != CodeTooManyElements || flat(d.Values["settled"]) != "[]" {
		t.Fatalf("a refresh of a list too long: %v, %s, %v", d.Computed["settled"], flat(d.Values["settled"])[:2], err)
	}
	_, err = s.Transition(ctx, "Order", "ORD-17", "close", nil, As(liveJan))
	if e, _ := err.(*Error); e == nil || e.Code != CodeTooManyElements || e.Variable != "settled" {
		t.Fatalf("a closing over a list too long: %v", err)
	}
}

// A move the document makes by itself brings the list too, when the
// variable lists the entry of the state; the read right of the variable
// says who sees it; and the class it lists may not lose a column it
// keeps.
func TestKeptList_ByItselfWhoSeesItAndItsSource(t *testing.T) {
	s, ctx := openList(t)
	const audit = `{"name": "Audit",
 "variables": {
   "order":  {"type": "text"},
   "ready":  {"type": "bool"},
   "found":  {"type": "rows", "schema": {"number": "text", "margin": "money(2)"},
              "formula": "QUERY(Invoice, {number, margin}, TEXT(order) = \"Order:\" & THIS.order, {-number})",
              "mode": "triggered", "on": ["enter:done"]},
   "count":  {"formula": "COUNT(found)"}
 },
 "states": {"initial": "open", "list": ["open", "done"]},
 "transitions": [{"from": "open", "to": "done", "guard": "ready"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"},
 "rights": {"*": {"found": {"read": ["finance"]}}}}`
	if _, err := s.DefineClass(ctx, []byte(audit), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Audit", Values{"order": "ORD-17"}, As(liveJan), WithID("A-1")); err != nil {
		t.Fatal(err)
	}
	// Jan marks it ready, and the audit moves by itself: version 2 is
	// his change, version 3 the move, which brings the list.
	d, err := s.Set(ctx, "Audit", "A-1", Values{"ready": true}, As(liveJan))
	if err != nil || d.State != "done" || d.Version != 3 || d.Values["count"] != 3.0 {
		t.Fatalf("the audit after it moved by itself: %s, %v", flat(d), err)
	}
	// Jan is in sales: the read right of the list is finance's, and he
	// is shown neither the list nor when it was brought.
	if _, has := d.Values["found"]; has || len(d.Computed) != 0 {
		t.Errorf("Jan is shown the list: %s, %s", flat(d.Values["found"]), flat(d.Computed))
	}
	e, err := s.Get(ctx, "Audit", "A-1", As(liveEwa))
	if err != nil || e.Computed["found"].Version != 3 || flat(e.Values["found"]) != `[{"margin":"1.00 PLN","number":"FV-3"},{"margin":"1.00 PLN","number":"FV-2"},{"margin":"1.00 PLN","number":"FV-1"}]` {
		t.Fatalf("Ewa reads the list: %s, %v", flat(e.Values["found"]), err)
	}
	// The history has the list at the version of the move, and none
	// before it.
	for version, rows := range map[int64]int{2: 0, 3: 3} {
		h, err := s.Version(ctx, "Audit", "A-1", version, As(liveEwa))
		if got, _ := h.Values["found"].([]any); err != nil || len(got) != rows {
			t.Errorf("version %d holds %d rows of the list, %v; want %d", version, len(got), err, rows)
		}
	}
	// The class the list reads may not lose a column the list keeps.
	without := strings.Replace(listInvoice, `   "margin": {"type": "money(2)", "label": "Margin"},`+"\n", "", 1)
	without = strings.Replace(without, `,
 "rights": {"*": {"margin": {"read": ["finance"], "write": ["finance", "sales"]}}}`, "", 1)
	if without == listInvoice {
		t.Fatal("the invoice of the test has no margin to take away")
	}
	if _, err := s.DefineClass(ctx, []byte(without), asSetup); err == nil {
		t.Error("an invoice without the margin was taken while a list keeps it")
	}
}
