package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// recorder is the function of a test's actions: it records every call
// and answers what the test says, or runs and returns nothing.
type recorder struct {
	mu    sync.Mutex
	calls []*ActionCall
	do    func(ctx context.Context, call *ActionCall) (Values, error)
}

func (r *recorder) fn(ctx context.Context, call *ActionCall) (Values, error) {
	r.mu.Lock()
	r.calls = append(r.calls, call)
	do := r.do
	r.mu.Unlock()
	if do == nil {
		return nil, nil
	}
	return do(ctx, call)
}

// take returns the calls recorded since the last take.
func (r *recorder) take() []*ActionCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.calls
	r.calls = nil
	return out
}

const (
	booksPost = `{"name": "books.post", "label": "Post to the books",
 "params": {"order": {"type": "text", "required": true}, "amount": "decimal(2)"},
 "returns": {"entry": "text", "posted_at": "datetime"}}`
	auditPing = `{"name": "audit.ping", "params": {"note": "text"}}`
	// An order is placed, turns big by itself when it is large, and is
	// booked by the books.
	shopOrder = `{"name": "Order",
 "variables": {"item": {"type": "text"}, "qty": {"type": "number", "default": "1"}, "price": {"type": "decimal", "scale": 2}%s},
 "states": {"initial": "new", "list": ["new", "placed", "big", "booked"]},
 "transitions": [{"name": "place", "from": "new", "to": "placed"}, {"from": "placed", "to": "big", "guard": "qty > 10"},
                 {"name": "book", "from": ["placed", "big"], "to": "booked"}],
 "rules": [
   {"on": "enter", "state": "placed", "do": [{"action": "books.post", "args": {"order": "ID()", "amount": "price"}}]},
   {"on": "enter", "state": "big", "do": [{"action": "audit.ping", "args": {"note": "\"big: \" & item"}}]}
 ]}`
)

func defineShop(t testing.TB, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, def := range []string{booksPost, auditPing} {
		if _, err := s.DefineAction(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(shopOrder, ""))); err != nil {
		t.Fatal(err)
	}
}

// place creates an order and places it.
func place(t testing.TB, s *Store, values Values, opts ...Option) string {
	t.Helper()
	ctx := context.Background()
	order, err := s.Create(ctx, "Order", values, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Order", order.ID, "place", nil, opts...); err != nil {
		t.Fatal(err)
	}
	return order.ID
}

// The registry of action definitions (ADR-0045, item 1) and the
// catalogue (item 9): a definition is stored with its version and is the
// contract of a Go function the process was opened with.
func TestActions_Registry(t *testing.T) {
	rec := &recorder{}
	funcs := map[string]ActionFunc{"books.post": rec.fn, "Audit.Ping": rec.fn}
	s, path := openStore(t, Options{Actions: funcs})
	ctx := context.Background()

	if diags := s.ValidateAction([]byte(booksPost)); len(diags) > 0 {
		t.Fatalf("a draft that would be taken: %v", diags)
	}
	if _, ok := s.Action("books.post"); ok {
		t.Fatal("a validation stores nothing")
	}
	info, err := s.DefineAction(ctx, []byte(booksPost))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "books.post" || info.Kind != ActionRegistered || info.Version != 1 || !info.Available || info.Sync || info.Label != "Post to the books" ||
		len(info.Params) != 2 || info.Params[0] != (ActionParam{Name: "order", Type: "text", Required: true}) || info.Params[1].Type != "decimal(2)" ||
		len(info.Returns) != 2 || info.Returns[1] != (ActionParam{Name: "posted_at", Type: "datetime"}) {
		t.Errorf("the definition: %s", dump(info))
	}
	// The same definition again is a no-op, however it is spaced.
	again, err := s.DefineAction(ctx, []byte(strings.ReplaceAll(booksPost, "\n", "\n\n  ")))
	if err != nil || again.Version != 1 {
		t.Errorf("the same definition again: %v, %s", err, dump(again))
	}
	// The function is found whatever the case of the name it was given
	// under.
	if ping, err := s.DefineAction(ctx, []byte(auditPing)); err != nil || !ping.Available {
		t.Errorf("a function given under another case: %v, %s", err, dump(ping))
	}

	// The catalogue: the engine's own actions first, then the defined
	// ones in the order they were defined.
	var names []string
	for _, a := range s.Actions() {
		names = append(names, a.Name)
		if model.EngineAction(a.Name) != (a.Kind == ActionEngine) || !a.Available || a.Kind == ActionEngine && (a.Version != 0 || a.Description == "") {
			t.Errorf("the entry of %s: %s", a.Name, dump(a))
		}
	}
	if got := strings.Join(names, " "); got != "set reset refresh emit create transition table.insert table.update table.delete books.post audit.ping" {
		t.Errorf("the catalogue: %s", got)
	}
	if set, ok := s.Action("set"); !ok || !set.Sync || set.Params[0].Type != "formulas" {
		t.Errorf("an engine action: %s", dump(set))
	}
	if create, _ := s.Action("create"); create.Sync || len(create.Params) != 5 {
		t.Errorf("an engine action of the outbox: %s", dump(create))
	}
	if _, ok := s.Action("slack.post"); ok {
		t.Error("an action nobody defined is in the catalogue")
	}
	if a, ok := s.Action("BOOKS.POST"); !ok || a.Name != "books.post" {
		t.Error("a defined action is found whatever its case")
	}

	// A redefinition is the next version.
	wider := strings.Replace(booksPost, `"amount": "decimal(2)"`, `"amount": "decimal(2)", "memo": "text"`, 1)
	if info, err = s.DefineAction(ctx, []byte(wider)); err != nil || info.Version != 2 || len(info.Params) != 3 {
		t.Fatalf("a redefinition: %v, %s", err, dump(info))
	}
	for name, tc := range map[string]struct{ def, code, path string }{
		"a version that is not the next":     {strings.Replace(booksPost, `"label"`, `"version": 5, "label"`, 1), model.CodeBadValue, "/version"},
		"a name that differs in case":        {strings.Replace(booksPost, "books.post", "Books.Post", 1), model.CodeDuplicate, "/name"},
		"a definition nothing implements":    {`{"name": "slack.post", "params": {"text": "text"}}`, model.CodeNotSupported, "/name"},
		"a definition that does not compile": {`{"name": "books.post", "params": {"order": "string"}}`, model.CodeUnknownType, "/params/order"},
		"what is not JSON":                   {`{"name": "books.post",`, model.CodeBadJSON, ""},
	} {
		_, err := s.DefineAction(ctx, []byte(tc.def))
		diags, _ := err.(model.Diagnostics)
		found := false
		for _, d := range diags {
			found = found || d.Code == tc.code && d.Path == tc.path
		}
		if !found {
			t.Errorf("%s: want %s at %q, got %v", name, tc.code, tc.path, err)
		}
		if tc.code != model.CodeBadJSON && len(s.ValidateAction([]byte(tc.def))) == 0 {
			t.Errorf("%s: a validation says what a definition would", name)
		}
	}
	if next, err := s.DefineAction(ctx, []byte(strings.Replace(wider, `"label"`, `"version": 3, "label"`, 1))); err != nil || next.Version != 3 {
		t.Errorf("a redefinition that names the next version: %v, %s", err, dump(next))
	}
	// The registry is the database's own: no class takes its name.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Actions", "variables": {"a": {"type": "text"}}}`)); err == nil || !err.(model.Diagnostics).Has(model.CodeReservedName) {
		t.Errorf("a class named after the registry: %v", err)
	}

	// The definitions travel with the file; the functions do not. A
	// process opened without them lists the actions and cannot run or
	// redefine them.
	s.Close()
	bare, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := bare.Action("books.post"); !ok || a.Available || a.Version != 3 || len(a.Params) != 3 {
		t.Errorf("a definition in a process without its function: %s", dump(a))
	}
	if _, err := bare.DefineAction(ctx, []byte(booksPost)); err == nil || !err.(model.Diagnostics).Has(model.CodeNotSupported) {
		t.Errorf("a redefinition in a process without the function: %v", err)
	}
	if _, err := bare.TryAction(ctx, "books.post", Values{"order": "o"}); code(err) != CodeNotRegistered {
		t.Errorf("a trial in a process without the function: %v", err)
	}
	if pv, err := bare.PreviewAction(ctx, "books.post", Values{"order": "o"}); err != nil || pv.Call.Args["order"] != "o" {
		t.Errorf("a preview needs no function: %v, %s", err, dump(pv))
	}
	bare.Close()
	full, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Actions: funcs})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	if a, _ := full.Action("books.post"); !a.Available || a.Version != 3 {
		t.Errorf("the definition, reopened with its function: %s", dump(a))
	}
	if a, err := full.DefineAction(ctx, []byte(strings.Replace(wider, `"label"`, `"version": 3, "label"`, 1))); err != nil || a.Version != 3 {
		t.Errorf("the stored definition again after a reopen: %v, %s", err, dump(a))
	}
}

// Open refuses functions under names no definition could have.
func TestActions_OpenChecksTheFunctions(t *testing.T) {
	fn := func(context.Context, *ActionCall) (Values, error) { return nil, nil }
	for name, funcs := range map[string]map[string]ActionFunc{
		"an engine action's name":      {"create": fn},
		"a name without a namespace":   {"post": fn},
		"the table namespace":          {"table.upsert": fn},
		"the http namespace":           {"http.call": fn},
		"a name that is no identifier": {"books post.now": fn},
		"no function":                  {"books.post": nil},
		"a name twice":                 {"books.post": fn, "Books.Post": fn},
	} {
		s, err := Open(filepath.Join(t.TempDir(), "bad.fado"), Options{Actions: funcs, Outbox: OutboxOptions{Manual: true}})
		if code(err) != CodeBadValue {
			t.Errorf("%s: want bad_value, got %v", name, err)
		}
		if s != nil {
			s.Close()
		}
	}
}

// What a call carries (ADR-0045, item 4): the key of the row, the same at
// every attempt; the arguments; and the event that fired the rule with
// the document at the version that commit made, whatever has happened to
// the document since. A function may call the database: the books book
// the order, under a key of their own.
func TestActions_TheCall(t *testing.T) {
	books, audit := &recorder{}, &recorder{}
	var s *Store
	s, _ = openStore(t, Options{Actions: map[string]ActionFunc{"books.post": books.fn, "audit.ping": audit.fn}})
	defineShop(t, s)
	ctx := context.Background()
	books.do = func(ctx context.Context, call *ActionCall) (Values, error) {
		// Twice is once: the key of the call makes the key of the command.
		_, err := s.Transition(ctx, "Order", call.Event.DocID, "book", nil, Key("booked:"+call.Key), As(Principal{ID: "books"}))
		return Values{"entry": "E-" + call.Args["order"].(string)}, err
	}
	day := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	order, err := s.Create(ctx, "Order", Values{"item": "desk", "qty": 20, "price": "99.5"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Order", order.ID, "place", nil, As(alice), Key("place-1"), BusinessDate(day)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Outbox(ctx, OutboxFilter{DocID: order.ID})
	if err != nil || len(rows) != 2 || rows[0].Action != "books.post" || rows[1].Action != "audit.ping" || rows[0].Version != 2 || rows[1].Version != 3 {
		t.Fatalf("the rows: %v, %s", err, dump(rows))
	}
	if string(rows[0].Args) != `{"amount":"99.50","order":"`+order.ID+`"}` || string(rows[1].Args) != `{"note":"big: desk"}` {
		t.Errorf("the arguments as the commit stored them: %s, %s", rows[0].Args, rows[1].Args)
	}
	if n, err := s.Drain(ctx); err != nil || n != 2 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	// The order has moved on since: placed, big by itself, booked by the
	// books.
	now, _ := s.Get(ctx, "Order", order.ID)
	if now.State != "booked" || now.Version != 4 {
		t.Fatalf("the order: %s", dump(now))
	}
	calls := books.take()
	if len(calls) != 1 {
		t.Fatalf("the books were called %d times", len(calls))
	}
	call := calls[0]
	if call.Key != rows[0].Key || call.Attempt != 1 || call.Action != "books.post" || call.Args["order"] != order.ID || call.Args["amount"] != "99.50" || len(call.Args) != 2 {
		t.Errorf("the call: %s", dump(call))
	}
	ev := call.Event
	if ev == nil || ev.ID+":0" != call.Key || ev.Kind != EventTransition || ev.Class != "Order" || ev.ClassVersion != 1 || ev.DocID != order.ID ||
		ev.Name != "place" || ev.FromState != "new" || ev.ToState != "placed" || ev.Version != 2 || ev.ActorID != "alice" ||
		ev.BusinessDate != "2026-02-28" || ev.CommandKey != "place-1" || ev.TS == 0 {
		t.Errorf("the event: %s", dump(ev))
	}
	// The document is the one that commit made, not the one of now.
	if d := ev.Document; d == nil || d.State != "placed" || d.Version != 2 || d.ID != order.ID || d.Values["item"] != "desk" || d.Values["price"] != "99.50" || d.ClassVersion != 1 {
		t.Errorf("the document of the event: %s", dump(ev.Document))
	}
	// The rule of the eventless transition was fired by that transition's
	// event, which has no name and no key of its own: the key is the
	// command's.
	pings := audit.take()
	if len(pings) != 1 {
		t.Fatalf("the audit was called %d times", len(pings))
	}
	hop := pings[0].Event
	if pings[0].Key != rows[1].Key || hop.Kind != EventTransition || hop.Name != "" || hop.FromState != "placed" || hop.ToState != "big" || hop.Version != 3 ||
		hop.CommandKey != "place-1" || hop.Document.State != "big" || hop.ID == ev.ID {
		t.Errorf("the event of the eventless transition: %s", dump(hop))
	}
	// What the function did is a command like any other: its own actor,
	// its own key.
	events, _ := s.Events(ctx, "Order", order.ID)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind+":"+e.Name+":"+e.ActorID)
		if e.Name == "book" && e.CommandKey != "booked:"+call.Key {
			t.Errorf("the key of the books' command: %s", e.CommandKey)
		}
	}
	if got := strings.Join(kinds, " "); got != "created::alice transition:place:alice transition::alice transition:book:books action_run:books.post:alice action_run:audit.ping:alice" {
		t.Errorf("the events: %s", got)
	}
	for _, r := range mustRows(t, s, order.ID) {
		if r.Status != ActionDone || r.Attempts != 1 || r.PrincipalID != "alice" {
			t.Errorf("the row after the call: %s", dump(r))
		}
	}

	// A later version of the class fires the rule of a later document, and
	// the event says so.
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(shopOrder, `, "memo": {"type": "text"}`))); err != nil {
		t.Fatal(err)
	}
	books.do = nil
	next := place(t, s, Values{"item": "lamp"})
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := books.take(); len(calls) != 1 || calls[0].Event.ClassVersion != 2 || calls[0].Event.Document.ClassVersion != 2 || calls[0].Event.DocID != next ||
		calls[0].Event.ActorID != "" || calls[0].Event.CommandKey != "" || calls[0].Args["amount"] != nil {
		t.Errorf("the call of a document of the next version: %s", dump(calls))
	}
}

func mustRows(t testing.TB, s *Store, id string) []QueuedAction {
	t.Helper()
	rows, err := s.Outbox(context.Background(), OutboxFilter{DocID: id})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The outcomes of a call (ADR-0044, item 4; ADR-0045, item 5): no error
// is done; an error is no answer, tried again with the same key and a
// counted attempt until MaxAttempts; an error marked permanent is a
// refusal, dead at once; a panic is an error. A row that waits holds back
// the later rows of its document and no other's.
func TestActions_Outcomes(t *testing.T) {
	now := start
	rec := &recorder{}
	s, _ := openStore(t, Options{Now: func() time.Time { return now }, Outbox: OutboxOptions{MaxAttempts: 3},
		Actions: map[string]ActionFunc{"books.post": rec.fn, "audit.ping": rec.fn}})
	defineShop(t, s)
	ctx := context.Background()
	drain := func(want int) []*ActionCall {
		t.Helper()
		if n, err := s.Drain(ctx); err != nil || n != want {
			t.Fatalf("drain: %d, %v, want %d", n, err, want)
		}
		return rec.take()
	}
	outcome := func(id string, i int) string {
		t.Helper()
		r := mustRows(t, s, id)[i]
		return fmt.Sprintf("%s %s %d %s", r.Action, r.Status, r.Attempts, r.ErrorCode)
	}

	// No answer, twice; the big order's second action waits behind the
	// first, and the small order's action does not.
	rec.do = func(_ context.Context, call *ActionCall) (Values, error) {
		if call.Action == "books.post" && call.Args["amount"] == "1.00" {
			return nil, errors.New("the books are busy")
		}
		return nil, nil
	}
	slow := place(t, s, Values{"item": "slow", "qty": 20, "price": "1.00"})
	fast := place(t, s, Values{"item": "fast", "price": "2.00"})
	first := drain(2)
	if outcome(slow, 0) != "books.post pending 1 action_error" || outcome(slow, 1) != "audit.ping pending 0 " || outcome(fast, 0) != "books.post done 1 " {
		t.Errorf("after the first drain: %s | %s | %s", outcome(slow, 0), outcome(slow, 1), outcome(fast, 0))
	}
	if r := mustRows(t, s, slow)[0]; r.ErrorMessage != "the books are busy" || r.DueAt != start.Unix()+1 {
		t.Errorf("the row that waits: %s", dump(r))
	}
	if got := drain(0); len(got) != 0 {
		t.Errorf("a drain before the retry is due called %s", dump(got))
	}
	now = now.Add(time.Second)
	second := drain(1)
	var slowKey string
	for _, c := range first {
		if c.Args["amount"] == "1.00" {
			slowKey = c.Key
		}
	}
	if len(second) != 1 || second[0].Key != slowKey || second[0].Attempt != 2 || second[0].Event.Version != 2 {
		t.Errorf("the second attempt has the key of the first: %s", dump(second))
	}
	// The third attempt is the last: the action dies with one event, and
	// the action behind it runs in the same drain.
	now = now.Add(2 * time.Second)
	if third := drain(2); len(third) != 2 || third[0].Attempt != 3 || third[0].Key != slowKey || third[1].Action != "audit.ping" {
		t.Errorf("the last attempt: %s", dump(third))
	}
	if outcome(slow, 0) != "books.post dead 3 action_error" || outcome(slow, 1) != "audit.ping done 1 " {
		t.Errorf("after the last attempt: %s | %s", outcome(slow, 0), outcome(slow, 1))
	}
	events, _ := s.Events(ctx, "Order", slow)
	var failed []*Event
	for _, e := range events {
		if e.Kind == EventActionFailed {
			failed = append(failed, e)
		}
	}
	if len(failed) != 1 || failed[0].Name != "books.post" || failed[0].Code != CodeActionError {
		t.Errorf("one event when it dies and none for the attempts before: %s", dump(events))
	}

	// A refusal is dead at the first attempt. An error of the store that a
	// function passes on keeps its code, refusal or not.
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a refusal":                       {Permanent(errors.New("the period is closed")), "books.post dead 1 action_error"},
		"a refusal that wraps":            {fmt.Errorf("posting: %w", Permanent(errors.New("closed"))), "books.post dead 1 action_error"},
		"a refusal with the store's code": {Permanent(&Error{Code: CodeWrongState, Message: "the invoice is not open"}), "books.post dead 1 wrong_state"},
		"no answer with the store's code": {&Error{Code: CodeStorage, Message: "the disk is full"}, "books.post pending 1 storage"},
		"a panic":                         {nil, "books.post pending 1 action_error"},
	} {
		rec.do = func(context.Context, *ActionCall) (Values, error) {
			if tc.err == nil {
				panic("nil map")
			}
			return nil, tc.err
		}
		id := place(t, s, Values{"item": name})
		drain(1)
		if got := outcome(id, 0); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
		if r := mustRows(t, s, id)[0]; name == "a panic" && !strings.Contains(r.ErrorMessage, "panicked: nil map") {
			t.Errorf("the message of a panic: %s", r.ErrorMessage)
		}
	}
	if Permanent(nil) != nil {
		t.Error("no error is no refusal")
	}

	// A caller that gives up while the action runs leaves the row as it
	// was: no attempt is counted.
	cctx, cancel := context.WithCancel(ctx)
	rec.do = func(ctx context.Context, _ *ActionCall) (Values, error) {
		cancel()
		return nil, ctx.Err()
	}
	gone := place(t, s, Values{"item": "gone"})
	rec.take()
	if _, err := s.Drain(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a drain whose caller gave up: %v", err)
	}
	if got := outcome(gone, 0); got != "books.post pending 0 " {
		t.Errorf("the row of a call the caller gave up on: %s", got)
	}
}

// A database opened by a process that was not given the function of an
// action gets no answer for its rows, with the usual retries (ADR-0027):
// the process that has the function runs them.
func TestActions_NotRegistered(t *testing.T) {
	now := start
	clock := func() time.Time { return now }
	rec := &recorder{}
	funcs := map[string]ActionFunc{"books.post": rec.fn, "audit.ping": rec.fn}
	s, path := openStore(t, Options{Now: clock, Actions: funcs})
	defineShop(t, s)
	ctx := context.Background()
	id := place(t, s, Values{"item": "desk", "price": "5.00"})
	s.Close()

	bare, err := Open(path, Options{Now: clock, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	// The class loads, and its rule queues: the definition is in the file.
	other := place(t, bare, Values{"item": "lamp"})
	if n, err := bare.Drain(ctx); err != nil || n != 2 {
		t.Fatalf("drain without the function: %d, %v", n, err)
	}
	for _, doc := range []string{id, other} {
		if r := mustRows(t, bare, doc)[0]; r.Status != ActionPending || r.Attempts != 1 || r.ErrorCode != CodeNotRegistered || !strings.Contains(r.ErrorMessage, "books.post") {
			t.Errorf("a row whose function this process lacks: %s", dump(r))
		}
	}
	bare.Close()

	now = now.Add(time.Second)
	full, err := Open(path, Options{Now: clock, Outbox: OutboxOptions{Manual: true}, Actions: funcs})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	if n, err := full.Drain(ctx); err != nil || n != 2 {
		t.Fatalf("drain with the function: %d, %v", n, err)
	}
	calls := rec.take()
	if len(calls) != 2 || calls[0].Attempt != 2 || calls[1].Attempt != 2 || calls[0].Args["amount"] != "5.00" && calls[1].Args["amount"] != "5.00" {
		t.Errorf("the calls, once the function is there: %s", dump(calls))
	}
	if r := mustRows(t, full, id)[0]; r.Status != ActionDone || r.Attempts != 2 {
		t.Errorf("the row: %s", dump(r))
	}
}

// A redefinition of an action may not break a rule that calls it
// (ADR-0044, item 7, for ADR-0045): a parameter the rule gives is gone or
// takes another type. What a rule does not use is free to change, and a
// parameter the new version requires is taken: the rule cannot give it
// before it exists, and its action is refused with required until it
// does.
func TestActions_RedefinitionMayNotBreakARule(t *testing.T) {
	rec := &recorder{}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"books.post": rec.fn, "audit.ping": rec.fn}})
	defineShop(t, s)
	ctx := context.Background()
	for name, tc := range map[string]struct{ def, path, says string }{
		"a parameter a rule gives is gone": {`{"name": "books.post", "params": {"order": {"type": "text", "required": true}}}`,
			"/params/amount", "it would break a rule of the class Order, at /rules/0/do/0/args/amount"},
		"a parameter a rule gives takes another type": {`{"name": "books.post", "params": {"order": {"type": "text", "required": true}, "amount": "text"}}`,
			"/params/amount", "the parameter amount is text but the formula is decimal(2)"},
		"an exact parameter at a coarser scale": {`{"name": "books.post", "params": {"order": {"type": "text", "required": true}, "amount": "decimal(1)"}}`,
			"/params/amount", "at /rules/0/do/0/args/amount"},
	} {
		_, err := s.DefineAction(ctx, []byte(tc.def))
		diags, _ := err.(model.Diagnostics)
		if len(diags) != 1 || diags[0].Code != model.CodeNotAllowed || diags[0].Path != tc.path || !strings.Contains(diags[0].Message, tc.says) || !strings.Contains(diags[0].Hint, "change that rule first") {
			t.Errorf("%s: %v", name, err)
		}
		if diags := s.ValidateAction([]byte(tc.def)); !diags.Has(model.CodeNotAllowed) {
			t.Errorf("%s: a validation says so too: %v", name, diags)
		}
	}
	if a, _ := s.Action("books.post"); a.Version != 1 {
		t.Fatalf("a refused redefinition changed the action: %s", dump(a))
	}
	// A wider scale, another description, other returned values and a new
	// optional parameter break nothing.
	if a, err := s.DefineAction(ctx, []byte(`{"name": "books.post", "description": "Books an order.",
 "params": {"order": {"type": "text", "required": true}, "amount": "decimal(4)", "memo": "text"}, "returns": {"entry": "int"}}`)); err != nil || a.Version != 2 {
		t.Fatalf("a redefinition that breaks nothing: %v", err)
	}
	// The action of an order placed before sees the definition of now.
	id := place(t, s, Values{"item": "desk", "price": "7.5"})
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := rec.take(); len(calls) != 1 || calls[0].Args["amount"] != "7.5000" || len(calls[0].Args) != 3 {
		t.Errorf("the arguments under the new definition: %s", dump(calls))
	}
	// A parameter the new version requires: taken, and the rule's action
	// is refused until the rule gives it.
	waiting := place(t, s, Values{"item": "lamp"})
	if a, err := s.DefineAction(ctx, []byte(`{"name": "books.post",
 "params": {"order": {"type": "text", "required": true}, "amount": "decimal(4)", "memo": "text", "period": {"type": "text", "required": true}}}`)); err != nil || a.Version != 3 {
		t.Fatalf("a redefinition that requires more: %v", err)
	}
	if n, err := s.Drain(ctx); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if r := mustRows(t, s, waiting)[0]; r.Status != ActionDead || r.ErrorCode != CodeRequired || !strings.Contains(r.ErrorMessage, "period") || len(rec.take()) != 0 {
		t.Errorf("a row that lacks what the action now requires: %s", dump(r))
	}
	_ = id
	// The class cannot be defined again as it is: its rule gives no
	// period. With it, it can.
	fresh := strings.Replace(fmt.Sprintf(shopOrder, `, "memo": {"type": "text"}`), `"amount": "price"`, `"amount": "price", "period": "\"2026-03\""`, 1)
	stale := fmt.Sprintf(shopOrder, `, "memo": {"type": "text"}`)
	if _, err := s.DefineClass(ctx, []byte(stale)); err == nil || !err.(model.Diagnostics).Has(model.CodeMissing) {
		t.Errorf("a class whose rule leaves a required parameter out: %v", err)
	}
	if _, err := s.DefineClass(ctx, []byte(fresh)); err != nil {
		t.Fatal(err)
	}
	place(t, s, Values{"item": "chair"})
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := rec.take(); len(calls) != 1 || calls[0].Args["period"] != "2026-03" {
		t.Errorf("the call once the rule gives the parameter: %s", dump(calls))
	}
	// A row queued for a parameter the action has since lost is refused.
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(fresh, `"amount": "price", `, "", 1))); err != nil {
		t.Fatal(err)
	}
	old := place(t, s, Values{"item": "old"})
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET args = json_set(args, '$.amount', '1.0000') WHERE doc_id = ?`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineAction(ctx, []byte(`{"name": "books.post", "params": {"order": {"type": "text", "required": true}, "period": {"type": "text", "required": true}}}`)); err != nil {
		t.Fatalf("a parameter no rule gives any more is free to go: %v", err)
	}
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if r := mustRows(t, s, old)[0]; r.Status != ActionDead || r.ErrorCode != CodeUnknownParameter {
		t.Errorf("a row with an argument the action lost: %s", dump(r))
	}
}

// A trial and a preview of an action (ADR-0045, item 9): values in, the
// call and the result out, nothing queued and no event.
func TestActions_TryAndPreview(t *testing.T) {
	rec := &recorder{}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"books.post": rec.fn, "ship.label": rec.fn}})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(booksPost)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineAction(ctx, []byte(`{"name": "ship.label",
 "params": {"to": "ref", "weight": "number", "at": "datetime", "price": "money(2)", "parcel": "json", "fragile": "bool", "day": "date"}}`)); err != nil {
		t.Fatal(err)
	}
	// Every scalar type arrives in the shape a document's values have on
	// the way out.
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	out, err := s.TryAction(ctx, "ship.label", Values{"to": "Customer:42", "weight": 3, "at": at, "price": "12.5 PLN", "parcel": map[string]any{"w": []any{1, "x"}}, "fragile": true, "day": at})
	if err != nil || !out.OK || out.Result != nil || out.Code != "" {
		t.Fatalf("a trial: %v, %s", err, dump(out))
	}
	calls := rec.take()
	if len(calls) != 1 {
		t.Fatalf("the trial called %d times", len(calls))
	}
	call := calls[0]
	if !strings.HasPrefix(call.Key, "try:") || len(call.Key) != 30 || call.Attempt != 1 || call.Event != nil {
		t.Errorf("the call of a trial: %s", dump(call))
	}
	want := `{"at":1772366400,"day":"2026-03-01","fragile":true,"parcel":{"w":[1,"x"]},"price":"12.50 PLN","to":"Customer:42","weight":3}`
	if got, _ := json.Marshal(call.Args); string(got) != want {
		t.Errorf("the arguments: %s, want %s", got, want)
	}
	if _, isRaw := call.Args["parcel"].(json.RawMessage); !isRaw {
		t.Errorf("a json argument arrives as its JSON: %T", call.Args["parcel"])
	}
	// A json value given as its text is the same value.
	if _, err := s.TryAction(ctx, "ship.label", Values{"parcel": `{"w": 1}`}); err != nil || string(rec.take()[0].Args["parcel"].(json.RawMessage)) != `{"w": 1}` {
		t.Errorf("a json argument given as text: %v", err)
	}
	// Two trials have two keys.
	s.TryAction(ctx, "ship.label", nil)
	s.TryAction(ctx, "ship.label", nil)
	if two := rec.take(); len(two) != 2 || two[0].Key == two[1].Key {
		t.Errorf("the keys of two trials: %s", dump(two))
	}

	// What the function returns is read against what the definition
	// declares.
	for name, tc := range map[string]struct {
		returns Values
		want    string
	}{
		"what it declares":        {Values{"entry": "E-1", "posted_at": at}, `{"action":"books.post","ok":true,"result":{"entry":"E-1","posted_at":1772366400}}`},
		"in another case":         {Values{"ENTRY": "E-1"}, `{"action":"books.post","ok":true,"result":{"entry":"E-1","posted_at":null}}`},
		"nothing":                 {nil, `{"action":"books.post","ok":true,"result":{"entry":null,"posted_at":null}}`},
		"a value of another type": {Values{"entry": 7}, `{"action":"books.post","ok":true,"code":"bad_result","message":"what the action returned as entry is no text: want text, got 7 (int)"}`},
	} {
		rec.do = func(context.Context, *ActionCall) (Values, error) { return tc.returns, nil }
		out, err := s.TryAction(ctx, "books.post", Values{"order": "o-1"})
		if got, _ := json.Marshal(out); err != nil || string(got) != tc.want {
			t.Errorf("%s: %v, %s, want %s", name, err, got, tc.want)
		}
	}
	rec.do = func(context.Context, *ActionCall) (Values, error) { return Values{"receipt": "R-1"}, nil }
	if out, _ := s.TryAction(ctx, "books.post", Values{"order": "o-1"}); !out.OK || out.Code != CodeBadResult || !strings.Contains(out.Message, `"receipt"`) {
		t.Errorf("a value the definition does not declare: %s", dump(out))
	}
	// How it failed, and whether the outbox would ask again.
	rec.do = func(context.Context, *ActionCall) (Values, error) { return nil, errors.New("busy") }
	if out, _ := s.TryAction(ctx, "books.post", Values{"order": "o-1"}); out.OK || out.Code != CodeActionError || out.Message != "busy" || !out.Retry {
		t.Errorf("a trial without an answer: %s", dump(out))
	}
	rec.do = func(context.Context, *ActionCall) (Values, error) {
		return nil, Permanent(&Error{Code: CodeWrongState, Message: "booked already"})
	}
	if out, _ := s.TryAction(ctx, "books.post", Values{"order": "o-1"}); out.OK || out.Code != CodeWrongState || out.Retry {
		t.Errorf("a trial that was refused: %s", dump(out))
	}
	rec.take()

	// The refusals of the trial itself; nothing is called.
	for name, tc := range map[string]struct {
		action string
		args   Values
		code   string
		about  string
	}{
		"an unknown action":         {"slack.post", nil, CodeUnknownAction, ""},
		"an engine action":          {"table.insert", nil, CodeNotAllowed, ""},
		"an unknown parameter":      {"books.post", Values{"order": "o", "memo": "x"}, CodeUnknownParameter, "memo"},
		"a required one left out":   {"books.post", Values{"amount": "1.00"}, CodeRequired, "order"},
		"a required one blank":      {"books.post", Values{"order": nil}, CodeRequired, "order"},
		"a value of the wrong type": {"books.post", Values{"order": "o", "amount": "much"}, CodeBadValue, "amount"},
		"a parameter given twice":   {"books.post", Values{"order": "o", "ORDER": "p"}, CodeBadValue, "order"},
		"a ref that is none":        {"ship.label", Values{"to": "nobody"}, CodeBadValue, "to"},
	} {
		for _, try := range []bool{true, false} {
			var err error
			if try {
				_, err = s.TryAction(ctx, tc.action, tc.args)
			} else {
				_, err = s.PreviewAction(ctx, tc.action, tc.args)
			}
			if se, _ := err.(*Error); se == nil || se.Code != tc.code || se.Variable != tc.about {
				t.Errorf("%s (trial %v): want %s about %q, got %v", name, try, tc.code, tc.about, err)
			}
		}
	}
	// A preview shows the call and calls nothing.
	pv, err := s.PreviewAction(ctx, "BOOKS.post", Values{"Order": "o-9", "amount": 3})
	if err != nil || pv.Action != "books.post" || pv.Kind != ActionRegistered || pv.Call.Attempt != 1 || pv.Call.Key != "" || pv.Call.Event != nil ||
		pv.Call.Args["order"] != "o-9" || pv.Call.Args["amount"] != "3.00" {
		t.Errorf("a preview: %v, %s", err, dump(pv))
	}
	if calls := rec.take(); len(calls) != 0 {
		t.Errorf("refused trials and previews called %s", dump(calls))
	}
	var events, queued int
	s.db.QueryRow(`SELECT (SELECT count(*) FROM events), (SELECT count(*) FROM outbox)`).Scan(&events, &queued)
	if events != 0 || queued != 0 {
		t.Errorf("trials wrote %d events and %d rows", events, queued)
	}
}

// The worker runs up to Workers actions that leave the database at once,
// and a commit does not wait for them (ADR-0027, ADR-0044 item 2); a
// drain by hand runs them one at a time.
func TestActions_Workers(t *testing.T) {
	const workers, orders = 3, 9
	var mu sync.Mutex
	inFlight, peak, done := 0, 0, 0
	gate := make(chan struct{})
	held := make(chan struct{}, orders)
	var s *Store
	fn := func(ctx context.Context, call *ActionCall) (Values, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		held <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		mu.Lock()
		inFlight--
		done++
		mu.Unlock()
		return nil, nil
	}
	s, err := Open(filepath.Join(t.TempDir(), "workers.fado"), Options{Outbox: OutboxOptions{Workers: workers}, Actions: map[string]ActionFunc{"books.post": fn, "audit.ping": fn}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defineShop(t, s)
	ctx := context.Background()
	for _, def := range []string{noteClass, `{"name": "Memo", "variables": {"text": {"type": "text"}},
 "states": {"initial": "new", "list": ["new", "filed"]}, "transitions": [{"name": "file", "from": "new", "to": "filed"}],
 "rules": [{"on": "enter", "state": "filed", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()", "text": "\"filed\""}}]}]}`} {
		if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < orders; i++ {
		place(t, s, Values{"item": fmt.Sprint("order ", i)})
	}
	// The worker holds as many calls as it has workers.
	for i := 0; i < workers; i++ {
		select {
		case <-held:
		case <-time.After(10 * time.Second):
			t.Fatalf("%d calls in flight, want %d", i, workers)
		}
	}
	// While they are in flight, and every worker is busy, a commit goes
	// through, and so does the engine's own action it queues: a memo
	// whose rule creates a note.
	finished := make(chan error, 1)
	go func() {
		memo, err := s.Create(ctx, "Memo", nil)
		if err == nil {
			_, err = s.Transition(ctx, "Memo", memo.ID, "file", nil)
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a commit waited for the actions in flight")
	}
	for deadline := time.Now().Add(10 * time.Second); len(notes(t, s)) != 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the engine's own action waited for the calls in flight")
		}
	}
	mu.Lock()
	if inFlight != workers {
		t.Errorf("%d calls in flight while the note was made, want %d", inFlight, workers)
	}
	mu.Unlock()
	close(gate)
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := s.Outbox(ctx, OutboxFilter{Status: ActionPending})
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker left %d actions pending", len(pending))
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != workers || done != orders {
		t.Errorf("at most %d calls were in flight and %d ran, want %d and %d", peak, done, workers, orders)
	}
}

func TestActions_DrainRunsOneAtATime(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	fn := func(context.Context, *ActionCall) (Values, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil, nil
	}
	s, _ := openStore(t, Options{Outbox: OutboxOptions{Workers: 8}, Actions: map[string]ActionFunc{"books.post": fn, "audit.ping": fn}})
	defineShop(t, s)
	for i := 0; i < 6; i++ {
		place(t, s, Values{"item": "x"})
	}
	if n, err := s.Drain(context.Background()); err != nil || n != 6 || peak != 1 {
		t.Errorf("a drain by hand: %d actions, %v, %d at once", n, err, peak)
	}
}

// Close waits for a call in flight, and tells it to stop through its
// context.
func TestActions_CloseWaitsForACall(t *testing.T) {
	entered, left := make(chan struct{}), make(chan struct{})
	fn := func(ctx context.Context, _ *ActionCall) (Values, error) {
		close(entered)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		close(left)
		return nil, ctx.Err()
	}
	path := filepath.Join(t.TempDir(), "close.fado")
	funcs := map[string]ActionFunc{"books.post": fn, "audit.ping": fn}
	s, err := Open(path, Options{Actions: funcs})
	if err != nil {
		t.Fatal(err)
	}
	defineShop(t, s)
	id := place(t, s, Values{"item": "desk"})
	<-entered
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-left:
	default:
		t.Error("Close returned while the call was in flight")
	}
	// The call that was cut short counted no attempt.
	again, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Actions: map[string]ActionFunc{"books.post": (&recorder{}).fn, "audit.ping": (&recorder{}).fn}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if r := mustRows(t, again, id)[0]; r.Status != ActionPending || r.Attempts != 0 {
		t.Errorf("the row of the call Close cut short: %s", dump(r))
	}
}

// An action that leaves the database happens at least once (ADR-0044,
// items 5 and 10): the process is stopped after the commit and before the
// worker, between the action's effect and its mark, and after the mark;
// the file is reopened and the receiver's log counted. The call reached
// the receiver every time with the same key, twice only when the mark was
// lost.
func TestActions_AtLeastOnceAcrossACrash(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the test binary three times")
	}
	for step, want := range map[string]int{"queued": 1, "performed": 2, "committed": 1} {
		t.Run(step, func(t *testing.T) {
			dir := t.TempDir()
			path, log := filepath.Join(dir, "crash.fado"), filepath.Join(dir, "received.log")
			cmd := exec.Command(os.Args[0], "-test.run=^TestActionsCrashHelper$")
			cmd.Env = append(os.Environ(), "FADODB_CRASH_DB="+path, "FADODB_CRASH_AT="+step, "FADODB_CRASH_LOG="+log)
			out, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != crashExit {
				t.Fatalf("the helper did not stop at %s: %v\n%s", step, err, out)
			}
			s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Actions: map[string]ActionFunc{"books.post": receiver(log), "audit.ping": receiver(log)}})
			if err != nil {
				t.Fatalf("the reopen: %v", err)
			}
			defer s.Close()
			ctx := context.Background()
			if _, err := s.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			rows, _ := s.Outbox(ctx, OutboxFilter{})
			if len(rows) != 1 || rows[0].Status != ActionDone || rows[0].Attempts != 1 {
				t.Fatalf("the row: %s", dump(rows))
			}
			raw, _ := os.ReadFile(log)
			lines := strings.Fields(string(raw))
			if len(lines) != want {
				t.Fatalf("the receiver was called %d times, want %d: %q", len(lines), want, lines)
			}
			for _, line := range lines {
				if line != rows[0].Key {
					t.Errorf("the receiver saw the key %s, want %s every time", line, rows[0].Key)
				}
			}
			events, _ := s.Events(ctx, "Order", rows[0].DocID)
			if n := len(events); n != 3 || events[2].Kind != EventActionRun {
				t.Errorf("the events: %s", dump(events))
			}
		})
	}
}

// receiver is an action that leaves the process: it appends the key of
// every call to a file, synced, as a receiver that books what it is sent
// would.
func receiver(log string) ActionFunc {
	return func(_ context.Context, call *ActionCall) (Values, error) {
		f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err := f.WriteString(call.Key + "\n"); err != nil {
			return nil, err
		}
		return nil, f.Sync()
	}
}

// TestActionsCrashHelper is the process
// TestActions_AtLeastOnceAcrossACrash stops: it queues one call and exits
// without closing the store at the step the environment names.
func TestActionsCrashHelper(t *testing.T) {
	path, step, log := os.Getenv("FADODB_CRASH_DB"), os.Getenv("FADODB_CRASH_AT"), os.Getenv("FADODB_CRASH_LOG")
	if path == "" || log == "" {
		t.Skip("run by TestActions_AtLeastOnceAcrossACrash")
	}
	s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Synchronous: "FULL", Actions: map[string]ActionFunc{"books.post": receiver(log), "audit.ping": receiver(log)}})
	if err != nil {
		t.Fatal(err)
	}
	defineShop(t, s)
	place(t, s, Values{"item": "desk"})
	if step == "queued" {
		os.Exit(crashExit)
	}
	s.testStep = func(now string, _ *outboxRow) *Error {
		if now == step {
			os.Exit(crashExit)
		}
		return nil
	}
	_, err = s.Drain(context.Background())
	t.Fatalf("the helper reached the end of the drain: %v", err)
}

// The worker asks again by itself when a call that got no answer falls
// due, with the same key and the next attempt. Nothing wakes it but the
// time: the test runs on the real clock and takes the second the first
// wait lasts.
func TestActions_WorkerAsksAgainWhenDue(t *testing.T) {
	if testing.Short() {
		t.Skip("waits a second for the retry")
	}
	rec := &recorder{do: func(_ context.Context, call *ActionCall) (Values, error) {
		if call.Attempt < 2 {
			return nil, errors.New("not yet")
		}
		return nil, nil
	}}
	s, err := Open(filepath.Join(t.TempDir(), "again.fado"), Options{Actions: map[string]ActionFunc{"books.post": rec.fn, "audit.ping": rec.fn}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defineShop(t, s)
	id := place(t, s, Values{"item": "desk"})
	deadline := time.Now().Add(10 * time.Second)
	for mustRows(t, s, id)[0].Status != ActionDone {
		if time.Now().After(deadline) {
			t.Fatalf("the worker left the row at %s", dump(mustRows(t, s, id)[0]))
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := rec.take()
	if r := mustRows(t, s, id)[0]; r.Attempts != 2 || len(calls) != 2 || calls[0].Key != calls[1].Key || calls[1].Attempt != 2 {
		t.Errorf("the row %s after the calls %s", dump(r), dump(calls))
	}
}

// A drain by hand beside a running worker leaves alone the document
// whose action the worker has in flight: the call is not made twice.
func TestActions_DrainLeavesACallInFlightAlone(t *testing.T) {
	entered, gate := make(chan struct{}, 4), make(chan struct{})
	rec := &recorder{do: func(ctx context.Context, _ *ActionCall) (Values, error) {
		entered <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return nil, nil
	}}
	s, err := Open(filepath.Join(t.TempDir(), "flight.fado"), Options{Actions: map[string]ActionFunc{"books.post": rec.fn, "audit.ping": rec.fn}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defineShop(t, s)
	id := place(t, s, Values{"item": "desk", "qty": 20})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not call")
	}
	// The row is pending and due, and its document's second action waits
	// behind it: a drain finds nothing it may run, and returns.
	drained := make(chan int, 1)
	go func() {
		n, _ := s.Drain(context.Background())
		drained <- n
	}()
	select {
	case n := <-drained:
		if n != 0 {
			t.Errorf("a drain beside the worker attempted %d actions", n)
		}
	case <-entered:
		t.Error("a drain beside the worker made the call the worker has in flight")
	case <-time.After(10 * time.Second):
		t.Error("a drain beside the worker did not return")
	}
	close(gate)
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows := mustRows(t, s, id)
		if rows[0].Status == ActionDone && rows[1].Status == ActionDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker left %s", dump(rows))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if calls := rec.take(); len(calls) != 2 || calls[0].Action != "books.post" || calls[1].Action != "audit.ping" {
		t.Errorf("each action of the document was called once, in order: %s", dump(calls))
	}
}
