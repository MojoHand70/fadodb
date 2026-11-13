package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The classes of the outbox tests: an order whose rules create a note
// when it is placed and another when it ships, and the note.
const (
	noteClass = `{"name": "Note",
 "variables": {"about": {"type": "text", "required": true}, "text": {"type": "text"}, "by": {"type": "text", "default": "USER()"}},
 "states": {"initial": "kept", "list": ["kept"]}}`
	orderClass = `{"name": "Order",
 "variables": {"item": {"type": "text"}, "qty": {"type": "number", "default": "1"}},
 "states": {"initial": "new", "list": ["new", "placed", "shipped"]},
 "transitions": [{"name": "place", "from": "new", "to": "placed"}, {"name": "ship", "from": "placed", "to": "shipped"}],
 "rules": [
   {"on": "transition", "name": "place", "do": [{"action": "create", "class": "Note", "id": "\"placed-\" & ID()", "values": {"about": "ID()", "text": "\"placed \" & item"}}]},
   {"on": "enter", "state": "shipped", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()", "text": "\"shipped\""}}]},
   {"on": "delete", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()", "text": "\"deleted\""}}]}
 ]}`
)

func defineOrders(t testing.TB, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, def := range []string{noteClass, orderClass} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
}

// notes are the texts of the notes the actions made, sorted.
func notes(t testing.TB, s *Store) []string {
	t.Helper()
	found, err := s.Query(context.Background(), Filter{Class: "Note"})
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, d := range found {
		out = append(out, d.Values["text"].(string))
	}
	sort.Strings(out)
	return out
}

// What the commit of an action is: caused by the event that queued it,
// on that event's business day, as its principal, under the key of the
// row, which a caller may not use; and what the listing and the view
// say of the row.
func TestOutbox_TheActionsCommit(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineOrders(t, s)
	ctx := context.Background()
	day := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	order, err := s.Create(ctx, "Order", Values{"item": "desk"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Order", order.ID, "place", nil, As(alice), BusinessDate(day)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Outbox(ctx, OutboxFilter{Class: "order", DocID: order.ID, Status: ActionPending}, asSetup)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the outbox: %v, %s", err, dump(rows))
	}
	q := rows[0]
	events, _ := s.Events(ctx, "Order", order.ID)
	placed := events[1]
	if q.Key != placed.ID+":0" || q.Class != "Order" || q.Version != 2 || q.Rule != "/rules/0/do/0" || q.Action != "create" || q.PrincipalID != "alice" || q.Depth != 0 ||
		string(q.Args) != `{"class":"Note","id":"placed-`+order.ID+`","values":{"about":"`+order.ID+`","text":"placed desk"}}` {
		t.Errorf("the row: %s", dump(q))
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	note, err := s.Get(ctx, "Note", "placed-"+order.ID)
	if err != nil || note.Values["by"] != "alice" || note.Values["text"] != "placed desk" {
		t.Fatalf("the note: %v, %s", err, dump(note))
	}
	made, _ := s.Events(ctx, "Note", note.ID)
	if len(made) != 1 || made[0].Kind != EventCreated || made[0].CauseEventID != placed.ID || made[0].CommandKey != "outbox:"+q.Key ||
		made[0].BusinessDate != "2026-02-28" || made[0].ActorID != "alice" {
		t.Errorf("the event of the action's commit: %s", dump(made))
	}
	events, _ = s.Events(ctx, "Order", order.ID)
	if ran := events[len(events)-1]; ran.Kind != EventActionRun || ran.Name != "create" || ran.CauseEventID != placed.ID || ran.BusinessDate != "2026-02-28" || ran.ActorID != "alice" || ran.Code != "" {
		t.Errorf("the event of the outcome: %s", dump(ran))
	}
	// The view of the class's actions, for the SQL endpoint.
	view, err := s.SQL(ctx, `SELECT "key", doc_id, action, status, attempts, error_code FROM Order_actions`, nil, asSetup)
	if err != nil || !reflect.DeepEqual(view.Rows, [][]any{{q.Key, order.ID, "create", "done", int64(1), nil}}) {
		t.Errorf("Order_actions: %v, %v", err, view)
	}
	// A caller's key may not look like the outbox's.
	if _, err := s.Create(ctx, "Order", Values{}, Key("outbox:"+q.Key)); code(err) != CodeBadValue {
		t.Errorf("a caller's key with the outbox prefix: %v", err)
	}
	if err := s.RetryAction(ctx, "nothing", asSetup); code(err) != CodeUnknownAction {
		t.Errorf("a retry of a key that is none: %v", err)
	}
	if err := s.RetryAction(ctx, placed.ID+":7", asSetup); code(err) != CodeUnknownAction {
		t.Errorf("a retry of a row that is not there: %v", err)
	}
}

// An eventless transition, a delete and a batch queue like any commit:
// the row of an eventless commit is keyed by that commit's event.
func TestOutbox_EveryCommitQueues(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(noteClass), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Parcel", "variables": {"weight": {"type": "number"}},
 "states": {"initial": "new", "list": ["new", "heavy"]},
 "transitions": [{"from": "new", "to": "heavy", "guard": "weight > 10"}],
 "rules": [
   {"on": "create", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()", "text": "\"made\""}}]},
   {"on": "enter", "state": "heavy", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()", "text": "\"heavy\""}}]},
   {"on": "delete", "do": [{"action": "create", "class": "Note", "values": {"about": "ID()", "text": "\"gone\""}}]}
 ]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	docs, err := s.CreateMany(ctx, "Parcel", []Values{{"weight": 20}, {"weight": 1}})
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(ctx, "Parcel", docs[0].ID)
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: docs[0].ID}, asSetup)
	if len(rows) != 2 || rows[0].Key != events[0].ID+":0" || rows[0].Version != 1 || rows[1].Key != events[1].ID+":0" || rows[1].Version != 2 {
		t.Fatalf("the rows of a create with an eventless transition: %s\nevents %s", dump(rows), dump(events))
	}
	if err := s.Delete(ctx, "Parcel", docs[1].ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 4 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if got := notes(t, s); strings.Join(got, " ") != "gone heavy made made" {
		t.Errorf("notes %v", got)
	}
	// The outcome of an action of a deleted document is still its event.
	events, _ = s.Events(ctx, "Parcel", docs[1].ID)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, " ") != "created deleted action_run action_run" {
		t.Errorf("the events of the deleted parcel: %v", kinds)
	}
}

// An action that gets no answer is tried again with a doubling wait and
// dies after MaxAttempts with one event; while it waits it holds back the
// later actions of its document and not those of another (ADR-0044,
// items 3 and 4).
func TestOutbox_NoAnswerIsRetried(t *testing.T) {
	now := start
	s, _ := openStore(t, Options{Now: func() time.Time { return now }, Outbox: OutboxOptions{MaxAttempts: 4}})
	defineOrders(t, s)
	ctx := context.Background()
	slow, _ := s.Create(ctx, "Order", Values{"item": "slow"})
	fast, _ := s.Create(ctx, "Order", Values{"item": "fast"})
	for _, id := range []string{slow.ID, fast.ID} {
		if _, err := s.Transition(ctx, "Order", id, "place", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, "Order", id, "ship", nil); err != nil {
			t.Fatal(err)
		}
	}
	busy := true
	s.testStep = func(step string, row *outboxRow) *Error {
		// The slow order's first action, the note of its placing.
		if busy && step == "perform" && strings.Contains(string(row.args), "placed slow") {
			return &Error{Code: CodeStorage, Message: "the store is busy"}
		}
		return nil
	}
	row := func(id string, i int) QueuedAction {
		rows, err := s.Outbox(ctx, OutboxFilter{DocID: id}, asSetup)
		if err != nil || len(rows) != 2 {
			t.Fatalf("the rows of %s: %v, %s", id, err, dump(rows))
		}
		return rows[i]
	}
	// First drain: the slow order's first action fails, its second waits
	// behind it; the fast order's two run.
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 3 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if got := notes(t, s); strings.Join(got, "|") != "placed fast|shipped" {
		t.Errorf("notes %v", got)
	}
	if q := row(slow.ID, 0); q.Status != ActionPending || q.Attempts != 1 || q.DueAt != start.Unix()+1 || q.ErrorCode != CodeStorage {
		t.Errorf("after one attempt: %s", dump(q))
	}
	if q := row(slow.ID, 1); q.Status != ActionPending || q.Attempts != 0 {
		t.Errorf("the action behind it: %s", dump(q))
	}
	// Not due yet: nothing runs.
	if n, _ := s.Drain(ctx, asSetup); n != 0 {
		t.Errorf("a drain before the retry is due attempted %d", n)
	}
	// The wait doubles: 1 s, 2 s, 4 s; the fourth failure is the last.
	for attempt, wait := range []int64{1, 2, 4} {
		now = now.Add(time.Duration(wait) * time.Second)
		// The last attempt kills the action, and a dead action holds nothing
		// back: the one behind it runs in the same drain.
		want := 1
		if attempt == 2 {
			want = 2
		}
		if n, _ := s.Drain(ctx, asSetup); n != want {
			t.Fatalf("attempt %d: drained %d, want %d", attempt+2, n, want)
		}
		q := row(slow.ID, 0)
		if attempt < 2 {
			if q.Status != ActionPending || q.Attempts != attempt+2 || q.DueAt != now.Unix()+2*wait {
				t.Errorf("attempt %d: %s", attempt+2, dump(q))
			}
			continue
		}
		if q.Status != ActionDead || q.Attempts != 4 || q.ErrorCode != CodeStorage || q.FinishedAt != now.Unix() {
			t.Errorf("after the last attempt: %s", dump(q))
		}
	}
	events, _ := s.Events(ctx, "Order", slow.ID)
	var failed []*Event
	for _, e := range events {
		if e.Kind == EventActionFailed {
			failed = append(failed, e)
		}
	}
	if len(failed) != 1 || failed[0].Code != CodeStorage || failed[0].Name != "create" {
		t.Errorf("one event when it dies, and none for the attempts before: %s", dump(events))
	}
	if q := row(slow.ID, 1); q.Status != ActionDone {
		t.Errorf("the action behind the dead one: %s", dump(q))
	}
	// Retried by hand once the store answers, the first runs too.
	busy = false
	if err := s.RetryAction(ctx, row(slow.ID, 0).Key, asSetup); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Drain(ctx, asSetup); n != 1 || row(slow.ID, 0).Status != ActionDone || row(slow.ID, 0).Attempts != 1 {
		t.Errorf("after the retry: drained %d, %s", n, dump(row(slow.ID, 0)))
	}
	if got := notes(t, s); len(got) != 4 {
		t.Errorf("notes %v", got)
	}
}

// The action's commit, the mark and the event are one transaction: an
// action that fails after its commit and before its mark leaves nothing
// behind, and runs once when it is tried again (ADR-0044, item 5).
func TestOutbox_ExactlyOnce(t *testing.T) {
	now := start
	s, _ := openStore(t, Options{Now: func() time.Time { return now }})
	defineOrders(t, s)
	ctx := context.Background()
	order, _ := s.Create(ctx, "Order", Values{"item": "desk"})
	if _, err := s.Transition(ctx, "Order", order.ID, "place", nil); err != nil {
		t.Fatal(err)
	}
	fail := true
	s.testStep = func(step string, row *outboxRow) *Error {
		if fail && step == "performed" {
			return &Error{Code: CodeStorage, Message: "lost between the effect and the mark"}
		}
		return nil
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if got := notes(t, s); len(got) != 0 {
		t.Fatalf("the failed action left a note: %v", got)
	}
	fail = false
	now = now.Add(time.Second)
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("the second drain: %d, %v", n, err)
	}
	if got := notes(t, s); len(got) != 1 {
		t.Errorf("notes %v, want one", got)
	}
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: order.ID}, asSetup)
	if rows[0].Status != ActionDone || rows[0].Attempts != 2 {
		t.Errorf("the row: %s", dump(rows[0]))
	}
}

// The worker runs what commits queue without being asked, and Close
// waits for it.
func TestOutbox_Worker(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "worker.fado"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defineOrders(t, s)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 20; i++ {
		order, err := s.Create(ctx, "Order", Values{"item": "desk"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Transition(ctx, "Order", order.ID, "place", nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, order.ID)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err := s.Outbox(ctx, OutboxFilter{Status: ActionPending}, asSetup)
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
	if got := notes(t, s); len(got) != len(ids) {
		t.Errorf("%d notes for %d orders", len(got), len(ids))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("a second close: %v", err)
	}
}

// Classes whose rules name each other load whatever the order they were
// defined in (ADR-0044): the second pass of the load resolves them.
func TestOutbox_ClassesThatNameEachOtherLoad(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	ping := `{"name": "Ping", "variables": {"n": {"type": "number", "default": "0"}}, "states": {"initial": "on", "list": ["on"]}%s}`
	pong := `{"name": "Pong", "variables": {"n": {"type": "number", "default": "0"}}, "states": {"initial": "on", "list": ["on"]},
 "rules": [{"on": "create", "if": "n < 3", "do": [{"action": "create", "class": "Ping", "values": {"n": "n + 1"}}]}]}`
	for _, def := range []string{strings.Replace(ping, "%s", "", 1), pong,
		strings.Replace(ping, "%s", `, "rules": [{"on": "create", "if": "n < 3", "do": [{"action": "create", "class": "Pong", "values": {"n": "n + 1"}}]}]`, 1)} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("the reopen: %v", err)
	}
	defer s2.Close()
	if _, err := s2.Create(ctx, "Ping", Values{}); err != nil {
		t.Fatal(err)
	}
	if n, err := s2.Drain(ctx, asSetup); err != nil || n != 3 {
		t.Errorf("drain after the reopen: %d, %v", n, err)
	}
	pings, _ := s2.Count(ctx, Filter{Class: "Ping"})
	pongs, _ := s2.Count(ctx, Filter{Class: "Pong"})
	if pings != 2 || pongs != 2 {
		t.Errorf("%d pings and %d pongs, want 2 and 2", pings, pongs)
	}
}

// The acceptance test of ADR-0044, item 10: the process is stopped after
// the commit and before the worker, inside an action, and after the
// action's transaction; the file is reopened and counted. Every action
// has happened exactly once. The process that is stopped is this test
// binary, run again as TestOutboxCrashHelper.
func TestOutbox_SurvivesACrashAtEveryStep(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the test binary three times")
	}
	for _, step := range []string{"queued", "performed", "committed"} {
		t.Run(step, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.fado")
			cmd := exec.Command(os.Args[0], "-test.run=^TestOutboxCrashHelper$")
			cmd.Env = append(os.Environ(), "FADODB_CRASH_DB="+path, "FADODB_CRASH_AT="+step)
			out, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != crashExit {
				t.Fatalf("the helper did not stop at %s: %v\n%s", step, err, out)
			}
			s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
			if err != nil {
				t.Fatalf("the reopen: %v", err)
			}
			defer s.Close()
			ctx := context.Background()
			n, err := s.Drain(ctx, asSetup)
			if err != nil {
				t.Fatal(err)
			}
			// Stopped after the transaction of the first action, one is left.
			if want := map[string]int{"queued": 2, "performed": 2, "committed": 1}[step]; n != want {
				t.Errorf("the drain after the reopen attempted %d actions, want %d", n, want)
			}
			if got := notes(t, s); strings.Join(got, "|") != "placed desk|shipped" {
				t.Errorf("notes %v, want each once", got)
			}
			rows, _ := s.Outbox(ctx, OutboxFilter{}, asSetup)
			if len(rows) != 2 || rows[0].Status != ActionDone || rows[1].Status != ActionDone {
				t.Errorf("the rows: %s", dump(rows))
			}
			orders, _ := s.Query(ctx, Filter{Class: "Order"})
			events, _ := s.Events(ctx, "Order", orders[0].ID)
			var kinds []string
			for _, e := range events {
				kinds = append(kinds, e.Kind)
			}
			if strings.Join(kinds, " ") != "created transition transition action_run action_run" {
				t.Errorf("events %v", kinds)
			}
		})
	}
}

const crashExit = 7

// TestOutboxCrashHelper is the process TestOutbox_SurvivesACrashAtEveryStep
// stops: it queues two actions and exits without closing the store at the
// step the environment names.
func TestOutboxCrashHelper(t *testing.T) {
	path, step := os.Getenv("FADODB_CRASH_DB"), os.Getenv("FADODB_CRASH_AT")
	if path == "" {
		t.Skip("run by TestOutbox_SurvivesACrashAtEveryStep")
	}
	s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	defineOrders(t, s)
	ctx := context.Background()
	order, err := s.Create(ctx, "Order", Values{"item": "desk"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"place", "ship"} {
		if _, err := s.Transition(ctx, "Order", order.ID, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if step == "queued" {
		os.Exit(crashExit)
	}
	s.testStep = func(at string, row *outboxRow) *Error {
		if at == step {
			os.Exit(crashExit)
		}
		return nil
	}
	_, err = s.Drain(ctx, asSetup)
	t.Fatalf("the helper reached the end of the drain: %v", err)
}

// A chain of actions stops at its bound with a record of where: the
// action past the bound is written dead with too_deep and the document
// that asked for it gets the event (ADR-0044, item 6).
func TestOutbox_TooDeep(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Echo", "variables": {"n": {"type": "number", "default": "0"}}, "states": {"initial": "made", "list": ["made"]},
 "rules": [{"on": "create", "do": [{"action": "create", "class": "Echo", "id": "\"echo-\" & TEXT(n + 1)", "values": {"n": "n + 1"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Echo", Values{}, WithID("echo-0"), As(alice)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != MaxActionDepth+1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	last := "echo-" + itoa(MaxActionDepth+1)
	rows, err := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the dead rows: %v, %s", err, dump(rows))
	}
	if q := rows[0]; q.DocID != last || q.Depth != MaxActionDepth+1 || q.ErrorCode != CodeTooDeep || q.Attempts != 0 || q.FinishedAt == 0 || q.PrincipalID != "alice" {
		t.Errorf("the row past the bound: %s", dump(q))
	}
	events, _ := s.Events(ctx, "Echo", last)
	if len(events) != 2 || events[1].Kind != EventActionFailed || events[1].Code != CodeTooDeep || events[1].CauseEventID != events[0].ID || rows[0].OutcomeEvent != events[1].ID {
		t.Errorf("the events of the last document: %s", dump(events))
	}
	// Every document of the chain was made as the person who started it.
	first, _ := s.Events(ctx, "Echo", "echo-1")
	if first[0].ActorID != "alice" {
		t.Errorf("the actor of the first action's commit: %q", first[0].ActorID)
	}
}

// A preview reports what a command would queue, with the arguments as
// the commit would evaluate them, and writes no row (ADR-0044, item 9).
func TestOutbox_Preview(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineOrders(t, s)
	ctx := context.Background()
	order, _ := s.Create(ctx, "Order", Values{"item": "desk"})
	pv, err := s.Preview(ctx, "Order", order.ID, Values{"item": "lamp"}, "place")
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Rules) != 1 || len(pv.Rules[0].Actions) != 1 {
		t.Fatalf("the rules of the preview: %s", dump(pv.Rules))
	}
	a := pv.Rules[0].Actions[0]
	want := Values{"class": "Note", "id": "placed-" + order.ID, "values": Values{"about": order.ID, "text": "placed lamp"}}
	if a.Action != "create" || !a.Queued || !reflect.DeepEqual(a.Args, want) {
		t.Errorf("the queued action of the preview: %s", dump(a))
	}
	if rows, _ := s.Outbox(ctx, OutboxFilter{}, asSetup); len(rows) != 0 {
		t.Errorf("the preview queued: %s", dump(rows))
	}
}
