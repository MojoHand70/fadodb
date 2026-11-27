package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A kept value that follows its table (ADR-0091). Acme's invoices in a
// foreign currency keep the rate of the table Rates, which the treasury
// keeps. Ola enters invoices, Anna is in accounting, Tess is in the
// treasury: she may change the table and has no right over an invoice,
// which only accounting may so much as read.
const followRates = `{"name": "Rates",
 "columns": {"code": "text", "rate": "decimal(4)"},
 "key": ["code"],
 "access": {"read": "INGROUP(\"everyone\")", "write": "INGROUP(\"treasury\")"}}`

const followInvoice = `{"name": "Invoice", "title": "\"Invoice \" & number",
 "variables": {
   "number":   {"type": "text", "required": true},
   "amount":   {"type": "decimal", "scale": 2, "required": true},
   "currency": {"type": "text", "required": true},
   "rate":     {"type": "decimal", "scale": 4, "max": 10,
                "formula": "LOOKUP(Rates, rate, code = THIS.currency)",
                "mode": "triggered", "on": ["enter:entered", "table:Rates"]},
   "in_pln":   {"formula": "ROUND(amount * rate, 2)"}
 },
 "states": {"initial": "entered", "list": ["entered", "checked", "booked"], "final": ["booked"]},
 "transitions": [
   {"name": "check", "from": "entered", "to": "checked"},
   {"name": "book",  "from": "checked", "to": "booked"}
 ],
 "rights": {
   "entered": {"rate": {"write": ["ACCOUNTING"]}},
   "checked": {"rate": {"write": "FALSE"}}
 },
 "access": {
   "read":   "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
   "create": "INGROUP(\"everyone\")",
   "update": "CREATEDBY() = USER() OR INGROUP(\"ACCOUNTING\")",
   "delete": "FALSE",
   "transition": {"check": "INGROUP(\"ACCOUNTING\")", "book": "INGROUP(\"ACCOUNTING\")"}
 }}`

var (
	followOla  = Principal{ID: "ola", Groups: []string{"sales"}}
	followAnna = Principal{ID: "anna", Groups: []string{"ACCOUNTING"}}
	followTess = Principal{ID: "tess", Groups: []string{"treasury"}}
)

// openFollow opens a database with the table, at 4.2500 for the euro and
// 3.9000 for the dollar, and the class given.
func openFollow(t testing.TB, class string) (*Store, context.Context) {
	t.Helper()
	path := t.TempDir() + "/test.fado"
	s, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(followRates), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.2500"}, {"code": "USD", "rate": "3.9000"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(class), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

// invoiced makes an invoice as Ola and moves it as Anna to the state.
func invoiced(t testing.TB, s *Store, ctx context.Context, number, amount, currency, state string) *Document {
	t.Helper()
	doc, err := s.Create(ctx, "Invoice", Values{"number": number, "amount": amount, "currency": currency}, As(followOla))
	if err != nil {
		t.Fatal(err)
	}
	for _, move := range []struct{ name, to string }{{"check", "checked"}, {"book", "booked"}} {
		if doc.State == state {
			break
		}
		if doc, err = s.Transition(ctx, "Invoice", doc.ID, move.name, nil, As(followAnna)); err != nil {
			t.Fatal(err)
		}
	}
	return doc
}

// drained runs the outbox and returns how many rows it attempted.
func drained(t testing.TB, s *Store, ctx context.Context) int {
	t.Helper()
	n, err := s.Drain(ctx, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// follows are the follow rows of the outbox, in the order they were queued.
func follows(t testing.TB, s *Store, ctx context.Context) []QueuedAction {
	t.Helper()
	rows, err := s.Outbox(ctx, OutboxFilter{}, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	var out []QueuedAction
	for _, r := range rows {
		if r.Action == ActionFollow {
			out = append(out, r)
		}
	}
	return out
}

func TestFollow_ATableBringsTheValueOfEveryOpenDocument(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	booked := invoiced(t, s, ctx, "FV-15", "100.00", "EUR", "booked")
	entered := invoiced(t, s, ctx, "FV-16", "100.00", "EUR", "entered")
	checked := invoiced(t, s, ctx, "FV-17", "100.00", "EUR", "checked")
	dollar := invoiced(t, s, ctx, "FV-18", "100.00", "USD", "entered")
	if entered.Values["rate"] != "4.2500" || entered.Values["in_pln"] != "425.00" {
		t.Fatalf("the invoice as entered: %v", entered.Values)
	}
	if n := drained(t, s, ctx); n != 0 {
		t.Fatalf("nothing was queued yet, and the outbox ran %d rows", n)
	}

	// Tess changes the rate of the euro. She may not read an invoice.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "Invoice", entered.ID, As(followTess)); code(err) != CodeUnknownDocument {
		t.Fatalf("Tess reads an invoice: %v", err)
	}
	// Until the worker has run, the invoice holds what it held.
	if doc, _ := s.Get(ctx, "Invoice", entered.ID, As(followAnna)); doc.Values["rate"] != "4.2500" || doc.Version != entered.Version {
		t.Fatalf("before the worker ran: %v, version %d", doc.Values, doc.Version)
	}
	queued := follows(t, s, ctx)
	if len(queued) != 1 || queued[0].Status != ActionPending || queued[0].Class != "Invoice" || queued[0].DocID != "" ||
		queued[0].Rule != "table:Rates" || string(queued[0].Args) != `{"table":"Rates"}` || queued[0].PrincipalID != "tess" || queued[0].Depth != 0 {
		t.Fatalf("the row the change of the table queued: %+v", queued)
	}
	if n := drained(t, s, ctx); n != 1 {
		t.Fatalf("the outbox ran %d rows, want the follow", n)
	}

	// The invoice that is entered follows; what reads the rate follows it.
	doc, err := s.Get(ctx, "Invoice", entered.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["rate"] != "4.3100" || doc.Values["in_pln"] != "431.00" || doc.Version != entered.Version+1 || doc.State != "entered" {
		t.Errorf("the entered invoice after the change: %v, version %d", doc.Values, doc.Version)
	}
	events, err := s.Events(ctx, "Invoice", entered.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Kind != EventRefresh || last.Name != "table:Rates" || last.ActorID != "tess" || last.AfterVersion != doc.Version || last.CauseEventID != "" {
		t.Errorf("the event of the invoice the table brought: %+v", last)
	}
	// The class closes the rate in checked, a finished invoice is frozen,
	// and the dollar did not move: each is as it was, with no version.
	for _, was := range []*Document{booked, checked, dollar} {
		doc, err := s.Get(ctx, "Invoice", was.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Version != was.Version || doc.Values["rate"] != was.Values["rate"] || doc.Values["in_pln"] != was.Values["in_pln"] {
			t.Errorf("%s in %s changed: %v, version %d, was %v, version %d", was.Values["number"], was.State, doc.Values, doc.Version, was.Values, was.Version)
		}
	}
	done := follows(t, s, ctx)
	if len(done) != 1 || done[0].Status != ActionDone || done[0].Attempts != 1 || done[0].OutcomeEvent != "" || done[0].IntoCode != "" {
		t.Errorf("the row after the worker ran: %+v", done)
	}

	// Anna read the invoice before the change, and her command says so:
	// it is refused, since the invoice is at another version than the one
	// she saw (ADR-0078, item 2).
	if _, err := s.Transition(ctx, "Invoice", entered.ID, "check", nil, As(followAnna), Saw(entered.Version)); code(err) != CodeStaleVersion {
		t.Errorf("a command that saw the version before the table brought the rate: %v", err)
	}
	// A change that leaves every rate as it was brings nothing.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	if again, _ := s.Get(ctx, "Invoice", entered.ID, As(followAnna)); again.Version != doc.Version {
		t.Errorf("a change that changes nothing made version %d of the invoice", again.Version)
	}
}

// A row that is removed leaves the invoices that read it with no rate,
// and a rate typed past what the variable accepts leaves the value and
// its code, as a refresh does.
func TestFollow_ARemovedRowAndAValueTheVariableRefuses(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	euro := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	dollar := invoiced(t, s, ctx, "FV-2", "100.00", "USD", "entered")

	// 44.0000 is past the max of the variable: the invoice keeps 4.2500
	// and says why, in a version of its own.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "44.0000"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	doc, err := s.Get(ctx, "Invoice", euro.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["rate"] != "4.2500" || doc.Version != euro.Version+1 {
		t.Fatalf("a rate past the max: %v, version %d", doc.Values, doc.Version)
	}
	if m := doc.Computed["rate"]; m.Error != CodeOutOfRange || m.Version != euro.Version {
		t.Errorf("the moment of the rate after a value it refuses: %+v", m)
	}
	// Another change of the table finds the same refusal, and writes
	// nothing more.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "USD", "rate": "3.9500"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	if again, _ := s.Get(ctx, "Invoice", euro.ID, As(followAnna)); again.Version != doc.Version {
		t.Errorf("the same refusal made version %d", again.Version)
	}
	if d, _ := s.Get(ctx, "Invoice", dollar.ID, As(followAnna)); d.Values["rate"] != "3.9500" || d.Values["in_pln"] != "395.00" {
		t.Errorf("the dollar invoice: %v", d.Values)
	}

	// The euro's row is removed: the invoice has no rate.
	if n, err := s.DeleteRows(ctx, "Rates", []Values{{"code": "EUR"}}, As(followTess)); err != nil || n != 1 {
		t.Fatalf("removed %d rows: %v", n, err)
	}
	drained(t, s, ctx)
	// A blank counts as nothing in a product, as everywhere.
	if d, _ := s.Get(ctx, "Invoice", euro.ID, As(followAnna)); d.Values["rate"] != nil || d.Values["in_pln"] != "0.00" {
		t.Errorf("the euro invoice after its row was removed: %v", d.Values)
	}
	// Removing a row that is not there changes no table, and queues
	// nothing.
	before := len(follows(t, s, ctx))
	if n, err := s.DeleteRows(ctx, "Rates", []Values{{"code": "CHF"}}, As(followTess)); err != nil || n != 0 {
		t.Fatalf("removed %d rows: %v", n, err)
	}
	if after := len(follows(t, s, ctx)); after != before {
		t.Errorf("a removal of nothing queued %d rows", after-before)
	}
}

// More documents than one transaction takes: each transaction is one of
// its own, and the row is done with the last. A transaction takes a
// thousand; the test's store takes fifty.
func TestFollow_AThousandAtATime(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	const many = 50
	s.followEach = many
	const n = 2*many + 30
	docs := make([]Values, n)
	for i := range docs {
		cur := "EUR"
		if i%5 == 0 {
			cur = "USD"
		}
		docs[i] = Values{"number": fmt.Sprintf("FV-%d", i), "amount": "10.00", "currency": cur}
	}
	made, err := s.CreateMany(ctx, "Invoice", docs, As(followOla))
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	s.testStep = func(step string, row *outboxRow) *Error {
		if row.action == ActionFollow && step == "committed" {
			steps = append(steps, step)
		}
		return nil
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if got := drained(t, s, ctx); got != 1 {
		t.Fatalf("the outbox ran %d rows", got)
	}
	s.testStep = nil
	// 104 invoices are in euro: two transactions of fifty and one of the
	// four that are left.
	if len(steps) != 3 {
		t.Errorf("the follow took %d transactions, want 3", len(steps))
	}
	var euro, moved int
	for i, d := range made {
		doc, err := s.Get(ctx, "Invoice", d.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if i%5 == 0 {
			if doc.Version != 1 {
				t.Fatalf("a dollar invoice got version %d", doc.Version)
			}
			continue
		}
		euro++
		if doc.Values["rate"] == "4.3100" && doc.Values["in_pln"] == "43.10" && doc.Version == 2 {
			moved++
		}
	}
	if moved != euro {
		t.Errorf("%d of %d euro invoices follow", moved, euro)
	}
}

// A failure of the store in the middle leaves the row for another
// attempt: the documents of the transactions before it stay brought,
// those of the failed one are as they were, and the next attempt finds
// what is left.
func TestFollow_AFailureIsTriedAgain(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	const many = 50
	s.followEach = many
	docs := make([]Values, many+10)
	for i := range docs {
		docs[i] = Values{"number": fmt.Sprintf("FV-%d", i), "amount": "10.00", "currency": "EUR"}
	}
	made, err := s.CreateMany(ctx, "Invoice", docs, As(followOla))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	// The second transaction, of the ten that are left, fails once.
	var steps []string
	failed := false
	s.testStep = func(step string, row *outboxRow) *Error {
		if row.action != ActionFollow {
			return nil
		}
		steps = append(steps, step)
		if step == "performed" && len(steps) > 4 && !failed {
			failed = true
			return errf(CodeStorage, "", "the disk is full")
		}
		return nil
	}
	// The clock of the test moves a second at every reading, so the wait
	// before the second attempt passes inside the drain.
	drained(t, s, ctx)
	s.testStep = nil
	want := "perform performed marked committed" + // the first fifty
		" perform performed" + // the ten, which fail
		" perform performed marked committed" // the ten again
	if got := strings.Join(steps, " "); got != want {
		t.Errorf("the steps of the follow:\n got %s\nwant %s", got, want)
	}
	rows := follows(t, s, ctx)
	if len(rows) != 1 || rows[0].Status != ActionDone || rows[0].Attempts != 2 || rows[0].ErrorCode != "" {
		t.Fatalf("the row after the second attempt: %+v", rows)
	}
	for _, d := range made {
		doc, err := s.Get(ctx, "Invoice", d.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Values["rate"] != "4.3100" || doc.Version != 2 {
			t.Fatalf("%v: rate %v, version %d", doc.Values["number"], doc.Values["rate"], doc.Version)
		}
	}
}

// A row that waits when the database is closed is run when it is opened
// again.
func TestFollow_AcrossAReopening(t *testing.T) {
	path := t.TempDir() + "/test.fado"
	c := &clock{now: start}
	open := func() *Store {
		s, err := Open(path, Options{Now: c.Now, Outbox: OutboxOptions{Manual: true}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	ctx := context.Background()
	s := open()
	if _, err := s.DefineTable(ctx, []byte(followRates), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.2500"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(followInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	doc := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	defer s.Close()
	cls, ok := s.Class("Invoice")
	if !ok || len(cls.Follows("rates")) != 1 {
		t.Fatalf("the class as it was loaded follows %v", cls.Follows("rates"))
	}
	drained(t, s, ctx)
	if got, _ := s.Get(ctx, "Invoice", doc.ID, As(followAnna)); got == nil || got.Values["rate"] != "4.3100" {
		t.Errorf("the invoice after the database was opened again: %+v", got)
	}
}

// The worker runs the row by itself, a moment after the change.
func TestFollow_TheWorkerRunsIt(t *testing.T) {
	path := t.TempDir() + "/test.fado"
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineTable(ctx, []byte(followRates), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.2500"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(followInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	doc := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := s.Get(ctx, "Invoice", doc.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if got.Values["rate"] == "4.3100" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker did not bring the rate: %v", got.Values)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Two variables follow one table, and the class closes one of them in a
// state: a document there gets one version, with the other alone.
func TestFollow_OneOfTwoVariablesStands(t *testing.T) {
	class := strings.Replace(followInvoice, `"in_pln":`, `"rate_now": {"type": "decimal", "scale": 4,
                "formula": "LOOKUP(Rates, rate, code = THIS.currency)",
                "mode": "triggered", "on": ["enter:entered", "table:Rates"]},
   "in_pln":`, 1)
	s, ctx := openFollow(t, class)
	checked := invoiced(t, s, ctx, "FV-17", "100.00", "EUR", "checked")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	doc, err := s.Get(ctx, "Invoice", checked.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["rate"] != "4.2500" || doc.Values["rate_now"] != "4.3100" || doc.Values["in_pln"] != "425.00" || doc.Version != checked.Version+1 {
		t.Errorf("the checked invoice: %v, version %d", doc.Values, doc.Version)
	}
	if m := doc.Computed["rate"]; m.Version != 1 {
		t.Errorf("the moment of the rate that stands: %+v", m)
	}
	if m := doc.Computed["rate_now"]; m.Version != doc.Version {
		t.Errorf("the moment of the rate that follows: %+v", m)
	}
}

// A formula that one statement over the class cannot say, here because
// it reads who created the document, takes every open document, and the
// commit of each decides: the answer is the same, and a document whose
// value did not move still gets no version.
func TestFollow_AFormulaTheStoreCannotCompare(t *testing.T) {
	s, _ := openFollow(t, followInvoice)
	c, _ := s.class("Invoice")
	if text, _ := s.followSQL(c, "table:Rates"); !strings.Contains(text, "IS NOT") || !strings.Contains(text, `"state" IN ('entered')`) {
		t.Fatalf("the statement that finds the invoices to bring: %s", text)
	}
	s.Close()

	s, ctx := openFollow(t, strings.Replace(followInvoice, `code = THIS.currency`, `code = THIS.currency & LEFT(CREATEDBY(), 0)`, 1))
	c, _ = s.class("Invoice")
	if text, _ := s.followSQL(c, "table:Rates"); strings.Contains(text, "IS NOT") || !strings.Contains(text, `"state" IN ('entered')`) {
		t.Fatalf("the statement for a formula that reads the life: %s", text)
	}
	euro := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	dollar := invoiced(t, s, ctx, "FV-2", "100.00", "USD", "entered")
	checked := invoiced(t, s, ctx, "FV-3", "100.00", "EUR", "checked")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	for _, tc := range []struct {
		was     *Document
		rate    string
		version int64
	}{{euro, "4.3100", euro.Version + 1}, {dollar, "3.9000", dollar.Version}, {checked, "4.2500", checked.Version}} {
		doc, err := s.Get(ctx, "Invoice", tc.was.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Values["rate"] != tc.rate || doc.Version != tc.version {
			t.Errorf("%v: rate %v, version %d; want %s, version %d", doc.Values["number"], doc.Values["rate"], doc.Version, tc.rate, tc.version)
		}
	}
}

// A rule writes the table: the values that follow it are brought one
// action deeper, as the person whose command set the rule off. And a
// class whose own rule writes the table it follows ends at the bound of
// a chain, with a row that says so.
func TestFollow_ARuleChangesTheTable(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Correction",
 "variables": {"code": {"type": "text"}, "rate": {"type": "decimal", "scale": 4}},
 "states": {"initial": "open", "list": ["open", "sent"]},
 "transitions": [{"name": "send", "from": "open", "to": "sent"}],
 "rules": [{"on": "transition", "name": "send", "do": [{"action": "table.update", "table": "Rates", "key": {"code": "code"}, "set": {"rate": "rate"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	inv := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	fix, err := s.Create(ctx, "Correction", Values{"code": "EUR", "rate": "4.4000"}, As(followTess))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Correction", fix.ID, "send", nil, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	if doc, _ := s.Get(ctx, "Invoice", inv.ID, As(followAnna)); doc.Values["rate"] != "4.4000" {
		t.Errorf("the invoice after a rule changed the table: %v", doc.Values)
	}
	rows := follows(t, s, ctx)
	if len(rows) != 1 || rows[0].Status != ActionDone || rows[0].Depth != 1 || rows[0].PrincipalID != "tess" {
		t.Errorf("the follow a rule's action queued: %+v", rows)
	}

	// A class that moves the table it follows, every time it is brought.
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Drift",
 "variables": {"currency": {"type": "text"},
   "rate": {"type": "decimal", "scale": 4, "formula": "LOOKUP(Rates, rate, code = THIS.currency)", "mode": "triggered", "on": ["enter:open", "table:Rates"]}},
 "states": {"initial": "open", "list": ["open"]},
 "rules": [{"on": "values_change", "variables": ["rate"], "do": [{"action": "table.update", "table": "Rates", "key": {"code": "currency"}, "set": {"rate": "rate + 0.0001"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Drift", Values{"currency": "USD"}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "USD", "rate": "3.9500"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	// The chain ends: the step past the bound is written dead, be it the
	// rule's action or the follow it would have queued.
	rows, err = s.Outbox(ctx, OutboxFilter{Class: "Drift"}, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	var brought, dead int
	for _, r := range rows {
		switch {
		case r.Action == ActionFollow && r.Status == ActionDone:
			brought++
		case r.Status == ActionDead && r.ErrorCode == CodeTooDeep:
			dead++
		case r.Status != ActionDone:
			t.Errorf("a row of the chain is left: %+v", r)
		}
	}
	if dead != 1 || brought == 0 || brought > MaxActionDepth {
		t.Errorf("a class that moves its own table: %d follows done, %d rows dead as too deep", brought, dead)
	}
}

// A class is changed under its documents: the invoice the table brings
// adopts the definition of today with that commit, as with any other.
func TestFollow_ADocumentOfAnEarlierDefinition(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	inv := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	later := strings.Replace(followInvoice, `"in_pln":`, `"note": {"type": "text"}, "in_pln":`, 1)
	if _, err := s.DefineClass(ctx, []byte(later), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	doc, err := s.Get(ctx, "Invoice", inv.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["rate"] != "4.3100" || doc.ClassVersion != 2 || doc.Version != inv.Version+1 {
		t.Errorf("the invoice of the earlier definition: %v, version %d, class version %d", doc.Values, doc.Version, doc.ClassVersion)
	}

	// A definition that no longer follows the table, with a row waiting:
	// the row is done, and nothing is brought.
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.5000"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(later, `, "table:Rates"`, ``, 1)), asSetup); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	if again, _ := s.Get(ctx, "Invoice", inv.ID, As(followAnna)); again.Values["rate"] != "4.3100" || again.Version != doc.Version {
		t.Errorf("an invoice of a class that follows no more: %v, version %d", again.Values, again.Version)
	}
	for _, r := range follows(t, s, ctx) {
		if r.Status != ActionDone {
			t.Errorf("a follow of a class that follows no more: %+v", r)
		}
	}
}

// A table whose definition is changed while it holds rows is looked at
// again: the invoices whose rate still is what the table gives are left
// alone.
func TestFollow_ATableIsDefinedAgain(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	inv := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	wider := strings.Replace(followRates, `"rate": "decimal(4)"`, `"rate": "decimal(4)", "name": "text"`, 1)
	if _, err := s.DefineTable(ctx, []byte(wider), asSetup); err != nil {
		t.Fatal(err)
	}
	rows := follows(t, s, ctx)
	if len(rows) != 1 || rows[0].Status != ActionPending || rows[0].PrincipalID != setup.ID {
		t.Fatalf("the row a new definition of the table queued: %+v", rows)
	}
	drained(t, s, ctx)
	if rows = follows(t, s, ctx); rows[0].Status != ActionDone {
		t.Errorf("the row after the worker ran: %+v", rows[0])
	}
	if doc, _ := s.Get(ctx, "Invoice", inv.ID, As(followAnna)); doc.Version != inv.Version || doc.Values["rate"] != "4.2500" {
		t.Errorf("the invoice after the table was defined again: %v, version %d", doc.Values, doc.Version)
	}
}

// The commit of a document the table brings is a commit like any other:
// the rules of the class fire on the new value, and a transition whose
// guard now holds fires.
func TestFollow_TheRulesAndTheGuardsOfTheClass(t *testing.T) {
	class := strings.NewReplacer(
		`"in_pln":`, `"note": {"type": "text"}, "in_pln":`,
		`["entered", "checked", "booked"]`, `["entered", "review", "checked", "booked"]`,
		`{"name": "check", "from": "entered", "to": "checked"},`, `{"name": "check", "from": "entered", "to": "checked"},
   {"from": "entered", "to": "review", "guard": "in_pln > 430"},`,
		`"rights": {`, `"rules": [{"on": "values_change", "variables": ["rate"], "do": [{"action": "set", "values": {"note": "\"the rate moved to \" & TEXT(rate)"}}]}],
 "rights": {`,
	).Replace(followInvoice)
	s, ctx := openFollow(t, class)
	inv := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	doc, err := s.Get(ctx, "Invoice", inv.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != "review" || doc.Values["note"] != "the rate moved to 4.3100" || doc.Version != inv.Version+2 {
		t.Errorf("the invoice after the table brought its rate: in %s, %v, version %d", doc.State, doc.Values, doc.Version)
	}
	events, err := s.Events(ctx, "Invoice", inv.ID, As(followAnna))
	if err != nil {
		t.Fatal(err)
	}
	brought, moved := events[len(events)-2], events[len(events)-1]
	if brought.Kind != EventRefresh || brought.Name != "table:Rates" || moved.Kind != EventTransition || moved.ToState != "review" || moved.CauseEventID != brought.ID || moved.ActorID != "tess" {
		t.Errorf("the events of the invoice: %+v, then %+v", brought, moved)
	}
}

// A document that cannot take the value keeps what it holds, the others
// are brought, and the row says how many could not and why. A row that
// is dead is sent again by an administrator, as any.
func TestFollow_ADocumentThatCannotTakeTheValue(t *testing.T) {
	class := strings.Replace(followInvoice, `"in_pln":`, `"share": {"formula": "amount / (amount * rate - 431)"}, "in_pln":`, 1)
	s, ctx := openFollow(t, class)
	stuck := invoiced(t, s, ctx, "FV-1", "100.00", "EUR", "entered")
	free := invoiced(t, s, ctx, "FV-2", "50.00", "EUR", "entered")
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	if doc, _ := s.Get(ctx, "Invoice", stuck.ID, As(followAnna)); doc.Values["rate"] != "4.2500" || doc.Version != stuck.Version {
		t.Errorf("the invoice whose share cannot be worked out: %v, version %d", doc.Values, doc.Version)
	}
	if doc, _ := s.Get(ctx, "Invoice", free.ID, As(followAnna)); doc.Values["rate"] != "4.3100" || doc.Version != free.Version+1 {
		t.Errorf("the other invoice: %v, version %d", doc.Values, doc.Version)
	}
	rows := follows(t, s, ctx)
	if len(rows) != 1 || rows[0].Status != ActionDone || rows[0].IntoCode != "div_by_zero" ||
		!strings.HasPrefix(rows[0].IntoMessage, "1 of 2 documents kept their value; the first: Invoice "+stuck.ID+": ") {
		t.Fatalf("the row of a follow one document refused: %+v", rows)
	}

	// The worker cannot run a row: it is dead with the code, and has no
	// event. Sent again, it runs.
	s.testStep = func(step string, row *outboxRow) *Error {
		if row.action == ActionFollow && step == "perform" {
			return errf(CodeNotAllowed, "", "not now")
		}
		return nil
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.4000"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	s.testStep = nil
	rows = follows(t, s, ctx)
	dead := rows[len(rows)-1]
	if dead.Status != ActionDead || dead.ErrorCode != CodeNotAllowed || dead.Attempts != 1 || dead.OutcomeEvent != "" {
		t.Fatalf("the row the worker could not run: %+v", dead)
	}
	if doc, _ := s.Get(ctx, "Invoice", free.ID, As(followAnna)); doc.Values["rate"] != "4.3100" {
		t.Errorf("an invoice a dead follow was to bring: %v", doc.Values)
	}
	if err := s.RetryAction(ctx, dead.Key, asSetup); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	rows = follows(t, s, ctx)
	if again := rows[len(rows)-1]; again.Status != ActionDone || again.ErrorCode != "" {
		t.Errorf("the row after it was sent again: %+v", again)
	}
	for _, d := range []*Document{stuck, free} {
		if doc, _ := s.Get(ctx, "Invoice", d.ID, As(followAnna)); doc.Values["rate"] != "4.4000" {
			t.Errorf("%v after the row was sent again: %v", d.Values["number"], doc.Values)
		}
	}
}

// A document is brought with no savepoint of its own until one is
// refused: then the transaction goes back whole and is brought again,
// a savepoint for each document, and so is every later transaction of
// the follow. A document brought before the refusal is brought once.
// A transaction takes a thousand; the test's store takes two.
func TestFollow_ARefusalTakesTheTransactionBackAndTheRestIsCareful(t *testing.T) {
	class := strings.Replace(followInvoice, `"in_pln":`, `"share": {"formula": "amount / (amount * rate - 431)"}, "in_pln":`, 1)
	s, ctx := openFollow(t, class)
	s.followEach = 2
	first := invoiced(t, s, ctx, "FV-1", "50.00", "EUR", "entered")
	stuck := invoiced(t, s, ctx, "FV-2", "100.00", "EUR", "entered")
	rest := []*Document{
		invoiced(t, s, ctx, "FV-3", "50.00", "EUR", "entered"),
		invoiced(t, s, ctx, "FV-4", "50.00", "EUR", "entered"),
		invoiced(t, s, ctx, "FV-5", "50.00", "EUR", "entered"),
	}
	var steps []string
	s.testStep = func(step string, row *outboxRow) *Error {
		if row.action == ActionFollow && (step == "perform" || step == "committed") {
			steps = append(steps, step)
		}
		return nil
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	drained(t, s, ctx)
	s.testStep = nil
	// Three transactions committed, each asked once, the one taken back
	// and brought again among them.
	if want := []string{"perform", "committed", "perform", "committed", "perform", "committed"}; strings.Join(steps, " ") != strings.Join(want, " ") {
		t.Errorf("the steps of the follow: %v, want %v", steps, want)
	}
	if doc, _ := s.Get(ctx, "Invoice", stuck.ID, As(followAnna)); doc.Values["rate"] != "4.2500" || doc.Version != stuck.Version {
		t.Errorf("the invoice whose share cannot be worked out: %v, version %d", doc.Values, doc.Version)
	}
	for _, d := range append([]*Document{first}, rest...) {
		doc, err := s.Get(ctx, "Invoice", d.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Values["rate"] != "4.3100" || doc.Version != d.Version+1 {
			t.Errorf("%v: %v, version %d after %d", d.Values["number"], doc.Values, doc.Version, d.Version)
		}
		events, err := s.Events(ctx, "Invoice", d.ID, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		brought := 0
		for _, e := range events {
			if e.Kind == EventRefresh && e.Name == "table:Rates" {
				brought++
			}
		}
		if brought != 1 {
			t.Errorf("%v has %d events of the follow, want 1", d.Values["number"], brought)
		}
	}
	rows := follows(t, s, ctx)
	if len(rows) != 1 || rows[0].Status != ActionDone || rows[0].IntoCode != "div_by_zero" ||
		!strings.HasPrefix(rows[0].IntoMessage, "1 of 5 documents kept their value; the first: Invoice "+stuck.ID+": ") {
		t.Fatalf("the row of the follow: %+v", rows)
	}
}

// The worker brings one transaction of documents and looks at what else
// is due before the next: the row stays pending in between, and the run
// goes on from where it stood. A transaction takes a thousand; the
// test's store takes fifty.
func TestFollow_TheWorkerLooksAtTheRestBetweenTwoThousands(t *testing.T) {
	s, ctx := openFollow(t, followInvoice)
	const many = 50
	s.followEach = many
	docs := make([]Values, 2*many+30)
	for i := range docs {
		docs[i] = Values{"number": fmt.Sprintf("FV-%d", i), "amount": "10.00", "currency": "EUR"}
	}
	if _, err := s.CreateMany(ctx, "Invoice", docs, As(followOla)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
		t.Fatal(err)
	}
	brought := func() (n int) {
		t.Helper()
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM "Invoice" WHERE "version" = 2`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i, want := range []struct {
		brought int
		status  string
	}{{many, ActionPending}, {2 * many, ActionPending}, {len(docs), ActionDone}} {
		due, err := s.dueRows(ctx, s.stmts.dueEngine, s.now().Unix())
		if err != nil || len(due) != 1 || due[0].action != ActionFollow {
			t.Fatalf("look %d: what is due for the worker: %+v, %v", i+1, due, err)
		}
		if ran, err := s.runAction(ctx, due[0], true); err != nil || !ran {
			t.Fatalf("look %d: the worker ran the row: %v, %v", i+1, ran, err)
		}
		if got, row := brought(), follows(t, s, ctx)[0]; got != want.brought || row.Status != want.status {
			t.Errorf("after look %d: %d invoices brought and the row is %s; want %d and %s", i+1, got, row.Status, want.brought, want.status)
		}
	}
	if len(s.followRuns) != 0 {
		t.Errorf("the place of a follow that is done is kept: %v", s.followRuns)
	}
}

// A value that follows its table is brought when its document comes
// into a state in which it follows (ADR-0091, item 9): when the document
// is made, and when it comes back from a state in which the value
// stood. The class lists the table alone.
func TestFollow_ItIsBroughtOnComingIntoAStateWhereItFollows(t *testing.T) {
	back := strings.NewReplacer(
		`"on": ["enter:entered", "table:Rates"]`, `"on": ["table:Rates"]`,
		`["entered", "checked", "booked"]`, `["entered", "checked", "fast", "booked"]`,
		`{"name": "book",  "from": "checked", "to": "booked"}`, `{"name": "book",  "from": ["checked", "fast"], "to": "booked"},
   {"name": "return", "from": "checked", "to": "entered"},
   {"name": "skip", "from": "entered", "to": "fast"}`,
		`"check": "INGROUP(\"ACCOUNTING\")",`, `"check": "INGROUP(\"ACCOUNTING\")", "return": "INGROUP(\"ACCOUNTING\")", "skip": "INGROUP(\"ACCOUNTING\")",`,
	).Replace(followInvoice)
	s, ctx := openFollow(t, back)
	rate := func(code, value string) {
		t.Helper()
		if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": code, "rate": value}}, As(followTess)); err != nil {
			t.Fatal(err)
		}
		drained(t, s, ctx)
	}
	move := func(id, name string) *Document {
		t.Helper()
		doc, err := s.Transition(ctx, "Invoice", id, name, nil, As(followAnna))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	// A new invoice has its rate from the first moment, and says when it
	// was brought.
	doc, err := s.Create(ctx, "Invoice", Values{"number": "FV-17", "amount": "100.00", "currency": "EUR"}, As(followOla))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["rate"] != "4.2500" || doc.Values["in_pln"] != "425.00" || doc.Computed["rate"].Version != 1 {
		t.Fatalf("the new invoice: %v, %+v", doc.Values, doc.Computed)
	}
	// Checked, it stands, whatever the table does.
	doc = move(doc.ID, "check")
	rate("EUR", "4.3100")
	if got, _ := s.Get(ctx, "Invoice", doc.ID, As(followAnna)); got.Values["rate"] != "4.2500" || got.Version != doc.Version {
		t.Fatalf("the checked invoice after the table changed: %v, version %d", got.Values, got.Version)
	}
	// Returned, it has the rate of the table at once, in the commit of
	// the return, and follows again.
	doc = move(doc.ID, "return")
	if doc.State != "entered" || doc.Values["rate"] != "4.3100" || doc.Values["in_pln"] != "431.00" || doc.Computed["rate"].Version != doc.Version {
		t.Fatalf("the returned invoice: in %s, %v, %+v", doc.State, doc.Values, doc.Computed)
	}
	rate("EUR", "4.4000")
	if got, _ := s.Get(ctx, "Invoice", doc.ID, As(followAnna)); got.Values["rate"] != "4.4000" {
		t.Errorf("the returned invoice after the table changed again: %v", got.Values)
	}
	// Checked again, it stands again, with the rate it was checked with.
	doc = move(doc.ID, "check")
	rate("EUR", "4.5000")
	if got, _ := s.Get(ctx, "Invoice", doc.ID, As(followAnna)); got.Values["rate"] != "4.4000" || got.Version != doc.Version {
		t.Errorf("the invoice checked again: %v, version %d", got.Values, got.Version)
	}
	// A way round the state that makes the value stand: the class closes
	// the rate in checked alone, so on the other path it follows on.
	round, err := s.Create(ctx, "Invoice", Values{"number": "FV-18", "amount": "100.00", "currency": "EUR"}, As(followOla))
	if err != nil {
		t.Fatal(err)
	}
	round = move(round.ID, "skip")
	rate("EUR", "4.6000")
	if got, _ := s.Get(ctx, "Invoice", round.ID, As(followAnna)); got.State != "fast" || got.Values["rate"] != "4.6000" {
		t.Errorf("the invoice that went round the check: in %s, %v", got.State, got.Values)
	}
	s.Close()

	// The class that names where the value follows, and closes it
	// everywhere else: on every path out of entered it stands.
	only := strings.Replace(back, `"rights": {
   "entered": {"rate": {"write": ["ACCOUNTING"]}},
   "checked": {"rate": {"write": "FALSE"}}
 }`, `"rights": {
   "*":       {"rate": {"write": "FALSE"}},
   "entered": {"rate": {"write": ["ACCOUNTING"]}}
 }`, 1)
	if only == back {
		t.Fatal("the rights of the class were not replaced")
	}
	s, ctx = openFollow(t, only)
	for _, path := range []string{"check", "skip"} {
		doc, err := s.Create(ctx, "Invoice", Values{"number": "FV-" + path, "amount": "100.00", "currency": "EUR"}, As(followOla))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Values["rate"] != "4.2500" {
			t.Fatalf("a new invoice of the class that names where the rate follows: %v", doc.Values)
		}
		if doc, err = s.Transition(ctx, "Invoice", doc.ID, path, nil, As(followAnna)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.3100"}}, As(followTess)); err != nil {
			t.Fatal(err)
		}
		drained(t, s, ctx)
		if got, _ := s.Get(ctx, "Invoice", doc.ID, As(followAnna)); got.Values["rate"] != "4.2500" || got.Version != doc.Version {
			t.Errorf("the invoice that left entered by %s: in %s, %v, version %d", path, got.State, got.Values, got.Version)
		}
		if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": "4.2500"}}, As(followTess)); err != nil {
			t.Fatal(err)
		}
		drained(t, s, ctx)
	}
}
