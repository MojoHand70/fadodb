package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// A charge returns the payment's id.
	payCharge = `{"name": "pay.charge", "params": {"amount": "decimal(2)"}, "returns": {"payment": "text", "fee": "decimal(2)"}}`
	// A purchase is charged when it is submitted, and paid when the
	// payment's id has arrived.
	purchaseClass = `{"name": "Purchase",
 "variables": {"amount": {"type": "decimal", "scale": 2}, "payment_id": {"type": "text"}, "fee": {"type": "decimal", "scale": 2}, "failed_with": {"type": "text"}, "receipt": {"type": "text"}%s},
 "states": {"initial": "new", "list": ["new", "charging", "paid", "failed"]},
 "transitions": [{"name": "submit", "from": "new", "to": "charging"},
                 {"from": "charging", "to": "paid", "guard": "NOT ISBLANK(payment_id)"},
                 {"from": "charging", "to": "failed", "guard": "NOT ISBLANK(failed_with)"}],
 "rights": {"*": {"payment_id": {"write": ["nobody"]}, "fee": {"write": ["nobody"]}}},
 "rules": [
   {"on": "enter", "state": "charging", "do": [{"action": "pay.charge", "args": {"amount": "amount"}, "into": %s}]},
   {"on": "enter", "state": "paid", "do": [{"action": "create", "class": "Note", "id": "\"receipt-\" & ID()", "values": {"about": "ID()", "text": "payment_id"}, "into": {"receipt": "id"}}]}
 ]}`
	purchaseInto = `{"payment_id": "payment", "fee": "fee", "failed_with": "ERROR"}`
)

func definePurchases(t testing.TB, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(payCharge), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{noteClass, fmt.Sprintf(purchaseClass, "", purchaseInto)} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
}

func submit(t testing.TB, s *Store, amount string, opts ...Option) string {
	t.Helper()
	ctx := context.Background()
	doc, err := s.Create(ctx, "Purchase", Values{"amount": amount}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Purchase", doc.ID, "submit", nil, opts...); err != nil {
		t.Fatal(err)
	}
	return doc.ID
}

// What the commit of a result is (ADR-0045, item 8): a set of the
// engine's own on the document that caused the action, caused by the
// event of the action's outcome, on the business day of the event that
// fired the rule, under the principal whose command it was and under no
// write right; the eventless transitions follow it, and what its rules
// queue is one deeper in the chain.
func TestInto_TheCommitOfAResult(t *testing.T) {
	rec := &recorder{do: func(_ context.Context, call *ActionCall) (Values, error) {
		return Values{"payment": "pay_" + call.Args["amount"].(string), "fee": 0.25}, nil
	}}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"pay.charge": rec.fn}})
	definePurchases(t, s)
	ctx := context.Background()
	day := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	doc, err := s.Create(ctx, "Purchase", Values{"amount": "40"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	// Alice may not write the payment's id herself.
	if _, err := s.Set(ctx, "Purchase", doc.ID, Values{"payment_id": "forged"}, As(alice)); code(err) != CodeForbidden {
		t.Fatalf("a person writes the payment's id: %v", err)
	}
	if _, err := s.Transition(ctx, "Purchase", doc.ID, "submit", nil, As(alice), BusinessDate(day), Key("submit-1")); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 2 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	got, _ := s.Get(ctx, "Purchase", doc.ID)
	if got.State != "paid" || got.Version != 5 || got.Values["payment_id"] != "pay_40.00" || got.Values["fee"] != "0.25" || got.Values["failed_with"] != nil || got.Values["receipt"] != "receipt-"+doc.ID {
		t.Fatalf("the purchase: %s", dump(got))
	}
	events, _ := s.Events(ctx, "Purchase", doc.ID)
	var line []string
	byID := map[string]*Event{}
	for _, e := range events {
		byID[e.ID] = e
		line = append(line, e.Kind+":"+e.Name)
	}
	// created, submitted, charged, the result set, moved by its own guard,
	// the note made, its id set.
	if got := strings.Join(line, " "); got != "created: transition:submit action_run:pay.charge set: transition: action_run:create set:" {
		t.Fatalf("the events: %s", got)
	}
	submitted, ran, set, hop, made, last := events[1], events[2], events[3], events[4], events[5], events[6]
	if ran.CauseEventID != submitted.ID || set.CauseEventID != ran.ID || hop.CauseEventID != set.ID {
		t.Errorf("each event names its cause: the action %s, the set %s, the move %s", ran.CauseEventID, set.CauseEventID, hop.CauseEventID)
	}
	// The note's action was queued by the move the result caused, so its
	// own result is caused by its own outcome.
	if made.CauseEventID != hop.ID || last.CauseEventID != made.ID {
		t.Errorf("the second chain: the action %s, the set %s", made.CauseEventID, last.CauseEventID)
	}
	if set.ActorID != "alice" || set.BusinessDate != "2026-02-28" || set.CommandKey != outboxKeyPrefix+"into:"+ran.ID || set.BeforeVersion != 2 || set.AfterVersion != 3 || hop.AfterVersion != 4 {
		t.Errorf("the set of the result: %s", dump(set))
	}
	// What the write queued is one deeper than the action, and as the same
	// person.
	rows := mustRows(t, s, doc.ID)
	if len(rows) != 2 || rows[0].Depth != 0 || rows[1].Depth != 1 || rows[1].PrincipalID != "alice" || rows[1].Version != 4 || rows[0].IntoCode != "" || rows[1].IntoCode != "" {
		t.Errorf("the rows: %s", dump(rows))
	}
	// The view shows why a result was not stored, beside the row.
	out, err := s.SQL(ctx, `SELECT "action", "status", "into_code" FROM "Purchase_actions" ORDER BY "key"`, nil, asSetup)
	if err != nil || len(out.Rows) != 2 || out.Rows[0][2] != nil {
		t.Errorf("the view of the actions: %v, %s", err, dump(out))
	}
}

// The mark of the action, the event of its outcome and the result written
// back are one transaction (ADR-0045, item 8): what fails before their
// commit leaves none of the three, and the next attempt leaves each once.
func TestInto_OneTransactionWithTheMark(t *testing.T) {
	now := start
	rec := &recorder{do: func(context.Context, *ActionCall) (Values, error) { return Values{"payment": "pay_1"}, nil }}
	s, _ := openStore(t, Options{Now: func() time.Time { return now }, Actions: map[string]ActionFunc{"pay.charge": rec.fn}})
	definePurchases(t, s)
	ctx := context.Background()
	id := submit(t, s, "10")
	fail := true
	s.testStep = func(step string, _ *outboxRow) *Error {
		if fail && step == "marked" {
			return &Error{Code: CodeStorage, Message: "the disk is full"}
		}
		return nil
	}
	state := func() string {
		t.Helper()
		doc, err := s.Get(ctx, "Purchase", id)
		if err != nil {
			t.Fatal(err)
		}
		events, _ := s.Events(ctx, "Purchase", id)
		rows := mustRows(t, s, id)
		return fmt.Sprintf("%s v%d payment %v, %d events, %d rows, first %s after %d", doc.State, doc.Version, doc.Values["payment_id"], len(events), len(rows), rows[0].Status, rows[0].Attempts)
	}
	// The call was made; its mark and its result were not stored.
	if _, err := s.Drain(ctx, asSetup); code(err) != CodeStorage {
		t.Fatalf("a drain whose commit fails: %v", err)
	}
	if got := state(); got != "charging v2 payment <nil>, 2 events, 1 rows, first pending after 0" || len(rec.take()) != 1 {
		t.Errorf("after the failure: %s", got)
	}
	// The same for the engine's own action behind it, once the first is
	// through: the note, its mark and its id on the purchase.
	fail = false
	s.testStep = func(step string, row *outboxRow) *Error {
		if row.action == "create" && step == "marked" && !fail {
			fail = true
			return &Error{Code: CodeStorage, Message: "the disk is full"}
		}
		return nil
	}
	if _, err := s.Drain(ctx, asSetup); code(err) != CodeStorage {
		t.Fatalf("a drain whose second commit fails: %v", err)
	}
	if got := state(); got != "paid v4 payment pay_1, 5 events, 2 rows, first done after 1" || len(notes(t, s)) != 0 {
		t.Errorf("after the second failure: %s, notes %v", got, notes(t, s))
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	doc, _ := s.Get(ctx, "Purchase", id)
	if doc.Version != 5 || doc.Values["receipt"] != "receipt-"+id || len(notes(t, s)) != 1 || len(rec.take()) != 1 {
		t.Errorf("in the end each happened once: %s", dump(doc))
	}
}

// A result is written by the rule as it was when the action was queued,
// to the document as its class is now: a variable the class has since
// lost refuses the result, and the row says so.
func TestInto_TheRuleThatQueuedIt(t *testing.T) {
	rec := &recorder{do: func(context.Context, *ActionCall) (Values, error) {
		return Values{"payment": "pay_1", "fee": "0.10"}, nil
	}}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"pay.charge": rec.fn}})
	definePurchases(t, s)
	ctx := context.Background()
	id := submit(t, s, "10")
	// The class moves on while the action waits: the result now goes to
	// another variable, and the fee is computed.
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(fmt.Sprintf(purchaseClass, `, "ref": {"type": "text"}`, `{"ref": "payment", "failed_with": "ERROR"}`),
		`"fee": {"type": "decimal", "scale": 2}`, `"fee": {"formula": "ROUND(amount / 100, 2)"}`, 1)), asSetup); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	// The rule of version 1 wrote payment_id, fee and failed_with; fee is
	// computed now, so the document refuses the set, whole.
	row := mustRows(t, s, id)[0]
	if row.Status != ActionDone || row.IntoCode != CodeReadOnly || !strings.Contains(row.IntoMessage, "fee") {
		t.Errorf("a result for a variable that is computed now: %s", dump(row))
	}
	doc, _ := s.Get(ctx, "Purchase", id)
	if doc.State != "charging" || doc.Values["payment_id"] != nil || doc.Values["ref"] != nil {
		t.Errorf("a refused result changed the document: %s", dump(doc))
	}
	// A purchase submitted now is written by the rule of now.
	next := submit(t, s, "20")
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	if doc, _ := s.Get(ctx, "Purchase", next); doc.Values["ref"] != "pay_1" || doc.Values["payment_id"] != nil || doc.ClassVersion != 2 || mustRows(t, s, next)[0].IntoCode != "" {
		t.Errorf("the result under the rule of now: %s", dump(doc))
	}
}

// A chain that feeds itself through its results stops at the bound
// (ADR-0044, item 6): the commit of a result is one deeper than its
// action, so a rule that calls again whenever the answer changes ends
// with a dead row and a record of where.
func TestInto_AChainOfResultsStops(t *testing.T) {
	rec := &recorder{do: func(_ context.Context, call *ActionCall) (Values, error) {
		return Values{"next": call.Args["n"].(float64) + 1}, nil
	}}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"count.next": rec.fn}})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "count.next", "params": {"n": "number"}, "returns": {"next": "number"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Counter", "variables": {"n": {"type": "number", "default": "0"}, "stopped": {"type": "text"}},
 "states": {"initial": "on", "list": ["on"]},
 "rules": [{"on": "values_change", "variables": ["n"], "do": [{"action": "count.next", "args": {"n": "n"}, "into": {"n": "next", "stopped": "ERROR"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Counter", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Counter", doc.ID, Values{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	rows := mustRows(t, s, doc.ID)
	last := rows[len(rows)-1]
	// Depths 0 to 16 ran; the one that would be 17 deep was written dead.
	if len(rows) != MaxActionDepth+2 || len(rec.take()) != MaxActionDepth+1 || last.Status != ActionDead || last.ErrorCode != CodeTooDeep || last.Depth != MaxActionDepth+1 || last.Attempts != 0 {
		t.Errorf("%d rows, the last %s", len(rows), dump(last))
	}
	// The row that was too deep was never run, so it wrote nothing back.
	if got, _ := s.Get(ctx, "Counter", doc.ID); got.Values["n"] != float64(MaxActionDepth+2) || got.Values["stopped"] != nil {
		t.Errorf("the counter: %s", dump(got))
	}
}

// The worker writes results back by itself: a purchase is paid without
// anybody draining.
func TestInto_Worker(t *testing.T) {
	rec := &recorder{do: func(context.Context, *ActionCall) (Values, error) { return Values{"payment": "pay_1"}, nil }}
	s, err := Open(filepath.Join(t.TempDir(), "into.fado"), Options{Actions: map[string]ActionFunc{"pay.charge": rec.fn}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	definePurchases(t, s)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 10; i++ {
		ids = append(ids, submit(t, s, "10"))
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, id := range ids {
		for {
			doc, err := s.Get(ctx, "Purchase", id)
			if err != nil {
				t.Fatal(err)
			}
			if doc.State == "paid" && doc.Values["receipt"] != nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the worker left the purchase at %s", dump(doc))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if got := notes(t, s); len(got) != len(ids) {
		t.Errorf("%d notes for %d purchases", len(got), len(ids))
	}
}

// A dead action that is retried by hand writes its result again when it
// ends again: the failure first, then the answer, each a set of its own.
func TestInto_ARetriedActionWritesAgain(t *testing.T) {
	declined := true
	rec := &recorder{do: func(context.Context, *ActionCall) (Values, error) {
		if declined {
			return nil, Permanent(fmt.Errorf("the card was declined"))
		}
		return Values{"payment": "pay_2", "fee": "0.30"}, nil
	}}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"pay.charge": rec.fn}})
	definePurchases(t, s)
	ctx := context.Background()
	id := submit(t, s, "10")
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	doc, _ := s.Get(ctx, "Purchase", id)
	if doc.State != "failed" || doc.Values["failed_with"] != CodeActionError || doc.Values["payment_id"] != nil {
		t.Fatalf("after the refusal: %s", dump(doc))
	}
	declined = false
	if err := s.RetryAction(ctx, mustRows(t, s, id)[0].Key, asSetup); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	// The answer replaces the failure: the error is blank again.
	doc, _ = s.Get(ctx, "Purchase", id)
	row := mustRows(t, s, id)[0]
	if doc.Values["payment_id"] != "pay_2" || doc.Values["fee"] != "0.30" || doc.Values["failed_with"] != nil || row.Status != ActionDone || row.IntoCode != "" {
		t.Errorf("after the retry: %s, the row %s", dump(doc), dump(row))
	}
	events, _ := s.Events(ctx, "Purchase", id)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, " "); got != "created transition action_failed set transition action_run set" {
		t.Errorf("the events: %s", got)
	}
}

// A database that is closed and opened again still writes results back:
// what a class's rules do with an answer is read from the stored class.
func TestInto_AfterAReopen(t *testing.T) {
	rec := &recorder{do: func(context.Context, *ActionCall) (Values, error) { return Values{"payment": "pay_9"}, nil }}
	funcs := map[string]ActionFunc{"pay.charge": rec.fn}
	s, path := openStore(t, Options{Actions: funcs})
	definePurchases(t, s)
	id := submit(t, s, "10")
	s.Close()
	again, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Actions: funcs})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	ctx := context.Background()
	if n, err := again.Drain(ctx, asSetup); err != nil || n != 2 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if doc, _ := again.Get(ctx, "Purchase", id); doc.State != "paid" || doc.Values["payment_id"] != "pay_9" || doc.Values["receipt"] != "receipt-"+id {
		t.Errorf("the purchase after a reopen: %s", dump(doc))
	}
}
