package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on the outbox and the worker is for the developer who
// runs a database. Its program is examples/outbox, which has a test of
// its own. TestTheOutboxAndTheWorker holds the tables of the chapter
// and what it says in passing.

func TestTheOutboxAndTheWorker(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "the-outbox-and-the-worker.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	clock := start
	path := filepath.Join(t.TempDir(), "orders.fado")
	// The functions of the program: one that works, one that never
	// gets an answer, and one that is refused.
	functions := map[string]fado.ActionFunc{
		"job.works":  func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil },
		"job.silent": func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, errors.New("no answer") },
		"job.refused": func(context.Context, *fado.ActionCall) (fado.Values, error) {
			return nil, fado.Permanent(errors.New("refused"))
		},
		"job.leaves": func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil },
	}
	options := fado.Options{Actions: functions, Now: func() time.Time { return clock },
		Outbox: fado.OutboxOptions{Manual: true, MaxAttempts: 3, Backoff: time.Minute}}
	db, err := fado.Open(path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	adam := fado.As(fado.Principal{ID: "adam", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}})
	ola := person("ola")
	for name := range functions {
		if _, err := db.DefineAction(ctx, []byte(`{"name":"`+name+`"}`), setup); err != nil {
			t.Fatal(err)
		}
	}
	// An order whose transitions each queue the actions named after
	// them, in the order given: "works_silent" queues job.works and then
	// job.silent.
	var transitions, rules []string
	for _, name := range []string{"works", "silent", "refused", "leaves", "silent_works"} {
		var actions []string
		for _, action := range strings.Split(name, "_") {
			actions = append(actions, `{"action":"job.`+action+`"}`)
		}
		transitions = append(transitions, `{"name":"`+name+`","from":"new","to":"new"}`)
		rules = append(rules, `{"on":"transition","name":"`+name+`","do":[`+strings.Join(actions, ",")+`]}`)
	}
	transitions = append(transitions, `{"name":"hold","from":"new","to":"held"}`, `{"name":"release","from":"held","to":"new","after":"2h"}`)
	order := `{"name":"Order","variables":{"customer":{"type":"text"}},"states":{"initial":"new","list":["new","held"]},` +
		`"transitions":[` + strings.Join(transitions, ",") + `],"rules":[` + strings.Join(rules, ",") + `],` +
		`"access":{"read":"CREATEDBY() = USER()","create":"INGROUP(\"everyone\")","update":"CREATEDBY() = USER()","delete":"FALSE"}}`
	if _, err := db.DefineClass(ctx, []byte(order), setup); err != nil {
		t.Fatal(err)
	}
	// take makes a new order and takes a transition of it; the rows of
	// the queue of that order are what the transition queued.
	take := func(transition string) string {
		t.Helper()
		doc, err := db.Create(ctx, "Order", fado.Values{"customer": "ewa"}, ola)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Transition(ctx, "Order", doc.ID, transition, nil, ola); err != nil {
			t.Fatal(err)
		}
		return doc.ID
	}
	queue := func(id string) []fado.QueuedAction {
		t.Helper()
		found, err := db.Outbox(ctx, fado.OutboxFilter{DocID: id}, adam)
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	drain := func() int {
		t.Helper()
		ran, err := db.Drain(ctx, adam)
		if err != nil {
			t.Fatal(err)
		}
		return ran
	}
	lastEvent := func(id string) *fado.Event {
		t.Helper()
		events, err := db.Events(ctx, "Order", id, ola)
		if err != nil || len(events) == 0 {
			t.Fatal(err)
		}
		return events[len(events)-1]
	}

	// A row of the queue: the table names every part and no other.
	var listed, has []string
	for _, row := range rows(marked(t, page, "a row of the queue")) {
		listed = append(listed, strings.Split(row[0], ", ")...)
	}
	kind := reflect.TypeOf(fado.QueuedAction{})
	for i := 0; i < kind.NumField(); i++ {
		tag, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
		has = append(has, tag)
	}
	sort.Strings(listed)
	sort.Strings(has)
	if !reflect.DeepEqual(listed, has) {
		t.Errorf("the chapter lists the parts %v, and a row has %v", listed, has)
	}

	// When an action fails.
	is := func(row []string, got string) {
		t.Helper()
		if !strings.HasPrefix(row[1], got) {
			t.Errorf("a function that %s: the row %s, and the chapter says it %s", row[0], got, row[1])
		}
	}
	checks(t, page, "when an action fails", map[string]func(row []string){
		"returns no error": func(row []string) {
			id := take("works")
			if before := queue(id); len(before) != 1 || before[0].Status != "pending" || before[0].Attempts != 0 || before[0].DueAt != clock.Unix() || before[0].PrincipalID != "ola" || before[0].Depth != 0 || before[0].Rule != "/rules/0/do/0" {
				t.Errorf("a row as the commit queues it: %+v", before)
			}
			if ran := drain(); ran != 1 {
				t.Errorf("the queue ran %d actions", ran)
			}
			after := queue(id)[0]
			is(row, "is "+after.Status+", with the event "+lastEvent(id).Kind)
			if after.OutcomeEvent != lastEvent(id).ID || after.FinishedAt == 0 || lastEvent(id).CauseEventID == "" {
				t.Errorf("a done row and its event: %+v", after)
			}
			if drain() != 0 {
				t.Error("a done action ran again")
			}
		},
		"returns an error": func(row []string) {
			id := take("silent")
			var story []string
			for _, wait := range []time.Duration{0, time.Minute, 2 * time.Minute} {
				clock = clock.Add(wait)
				if ran := drain(); ran != 1 {
					t.Errorf("the queue ran %d actions", ran)
				}
				// It is not due before its time.
				if ran := drain(); ran != 0 {
					t.Errorf("an action that is not due ran: %d", ran)
				}
				r := queue(id)[0]
				story = append(story, r.Status+" "+r.ErrorCode+" in "+time.Unix(r.DueAt, 0).Sub(clock).String())
			}
			// Three attempts: each wait twice the one before, then dead.
			if got := strings.Join(story, "; "); got != "pending action_error in 1m0s; pending action_error in 2m0s; dead action_error in 0s" {
				t.Errorf("an action that gets no answer, attempt by attempt: %s", got)
			}
			if !strings.HasPrefix(row[1], "stays pending and is tried again later; after the last attempt it is dead") {
				t.Errorf("the chapter says such a row %s", row[1])
			}
			if e := lastEvent(id); e.Kind != "action_failed" || e.Code != "action_error" {
				t.Errorf("the event of an action that is out of attempts: %s %s", e.Kind, e.Code)
			}
		},
		"returns an error wrapped with fado.Permanent": func(row []string) {
			id := take("refused")
			drain()
			r := queue(id)[0]
			got := "is " + r.Status
			if r.Attempts == 1 {
				got += " at once"
			}
			is(row, got+", with the event "+lastEvent(id).Kind)
		},
		"is not in this program": func(row []string) {
			id := take("leaves")
			db.Close()
			without := map[string]fado.ActionFunc{}
			for name, function := range functions {
				if name != "job.leaves" {
					without[name] = function
				}
			}
			reopened := options
			reopened.Actions = without
			if db, err = fado.Open(path, reopened); err != nil {
				t.Fatal(err)
			}
			if ran := drain(); ran != 1 {
				t.Errorf("the queue ran %d actions", ran)
			}
			r := queue(id)[0]
			is(row, "stays "+r.Status+" and is tried again later, with the code "+r.ErrorCode)
			if r.DueAt <= clock.Unix() {
				t.Errorf("an action whose function is missing is due at once again: %+v", r)
			}
			// Each action says whether this program can run it.
			for _, action := range db.Actions() {
				if strings.HasPrefix(action.Name, "job.") && action.Available == (action.Name == "job.leaves") {
					t.Errorf("the action %s says available %v", action.Name, action.Available)
				}
			}
		},
	})

	// The actions of one document run in the order they were queued: a
	// later one waits while an earlier one is tried again.
	id := take("silent_works")
	if ran := drain(); ran != 1 {
		t.Errorf("the queue ran %d actions of a document whose first action got no answer", ran)
	}
	if q := queue(id); q[0].Attempts != 1 || q[1].Attempts != 0 || q[1].Status != "pending" {
		t.Errorf("a later action did not wait: %+v", q)
	}

	// A dead action, and the box on the code of a failure.
	dead := take("refused")
	drain()
	failed := lastEvent(dead)
	if failed.Kind != "action_failed" || failed.Code != "action_error" {
		t.Fatalf("the event of a dead action: %s %s", failed.Kind, failed.Code)
	}
	byStatus, err := db.Outbox(ctx, fado.OutboxFilter{Status: "dead", Class: "Order", DocID: dead}, adam)
	if err != nil || len(byStatus) != 1 {
		t.Fatalf("the queue listed by the status dead: %v, %v", byStatus, err)
	}
	key := byStatus[0].Key
	// The cause is mended: the function no longer refuses.
	db.Close()
	mended := options
	mended.Actions = map[string]fado.ActionFunc{}
	for name, function := range functions {
		mended.Actions[name] = function
	}
	mended.Actions["job.refused"] = functions["job.works"]
	if db, err = fado.Open(path, mended); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	if err := db.RetryAction(ctx, key, adam); err != nil {
		t.Fatal(err)
	}
	if r := queue(dead)[0]; r.Status != "pending" || r.Attempts != 0 || r.DueAt > clock.Unix() {
		t.Errorf("a dead action that was sent again: %+v", r)
	}
	for ran := drain(); ran > 0; ran = drain() {
		clock = clock.Add(10 * time.Minute)
	}
	if r := queue(dead)[0]; r.Status != "done" {
		t.Errorf("the action that was sent again: %+v", r)
	}
	// The code of the first failure is no longer answered with its
	// event. When the engine keeps the code with the event, this fails
	// and the box in the chapter goes.
	events, err := db.Events(ctx, "Order", dead, ola)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.ID == failed.ID && e.Code != "" {
			t.Errorf("the event of the first failure keeps its code after the action was sent again: %s", e.Code)
		}
	}

	// Time is in the queue too.
	held := take("hold")
	if q := queue(held); len(q) != 1 || q[0].Action != "timer" || !strings.HasPrefix(q[0].Rule, "/transitions/") || q[0].DueAt != clock.Add(2*time.Hour).Unix() {
		t.Errorf("the row of a transition with a time: %+v", q)
	}

	// Looking at the queue.
	checks(t, page, "looking at the queue", map[string]func(row []string){
		"the rows, by status, by class or by document": func([]string) {
			all, err := db.Outbox(ctx, fado.OutboxFilter{}, adam)
			if err != nil {
				t.Fatal(err)
			}
			pending, _ := db.Outbox(ctx, fado.OutboxFilter{Status: "pending"}, adam)
			ofClass, _ := db.Outbox(ctx, fado.OutboxFilter{Class: "Order"}, adam)
			two, _ := db.Outbox(ctx, fado.OutboxFilter{Limit: 2}, adam)
			if len(pending) == 0 || len(pending) >= len(all) || len(ofClass) != len(all) || len(two) != 2 {
				t.Errorf("the queue has %d rows, %d pending, %d of the class, %d under a limit of two", len(all), len(pending), len(ofClass), len(two))
			}
		},
		"the same in SQL, for a report": func(row []string) {
			if !strings.Contains(row[1], "as Order_actions") {
				t.Errorf("the chapter names the view %q", row[1])
			}
			got, err := db.SQL(ctx, "select action, status from Order_actions where doc_id = ?", []any{held}, setup)
			if err != nil || len(got.Rows) != 1 || got.Rows[0][0] != "timer" || got.Rows[0][1] != "pending" {
				t.Errorf("the view of the queue: %v, %v", got, err)
			}
		},
		"what became of an action, on its document": func([]string) {
			var kinds []string
			for _, e := range events {
				kinds = append(kinds, e.Kind)
			}
			if got := strings.Join(kinds, " "); got != "created transition action_failed action_run" {
				t.Errorf("the events of a document whose action died and was sent again: %s", got)
			}
		},
		"whether this program can run an action": func(row []string) {
			if !strings.Contains(row[1], "available") {
				t.Errorf("the chapter says %q", row[1])
			}
			// Held above, where a function is missing.
		},
	})

	// The options of the worker: the table has every one and no other.
	var shown, fields []string
	for _, row := range rows(marked(t, page, "the options of the worker")) {
		shown = append(shown, row[0])
	}
	kind = reflect.TypeOf(fado.OutboxOptions{})
	for i := 0; i < kind.NumField(); i++ {
		fields = append(fields, kind.Field(i).Name)
	}
	sort.Strings(shown)
	sort.Strings(fields)
	if !reflect.DeepEqual(shown, fields) {
		t.Errorf("the chapter lists the options %v, and the package has %v", shown, fields)
	}

	// What holds when none is given: ten attempts, the first wait one
	// second.
	plainClock := start
	plain, err := fado.Open(filepath.Join(t.TempDir(), "plain.fado"), fado.Options{Now: func() time.Time { return plainClock },
		Actions: map[string]fado.ActionFunc{"job.silent": functions["job.silent"]}, Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err := plain.DefineAction(ctx, []byte(`{"name":"job.silent"}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.DefineClass(ctx, []byte(scratch("Job", `"n":{"type":"number"}`, oneState+`,"rules":[{"on":"create","do":[{"action":"job.silent"}]}]`)), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Create(ctx, "Job", fado.Values{"n": 1}, ola); err != nil {
		t.Fatal(err)
	}
	given := map[string]string{}
	for _, row := range rows(marked(t, page, "the options of the worker")) {
		given[row[0]] = row[2]
	}
	firstWait := ""
	for i := 0; i < 20; i++ {
		if _, err := plain.Drain(ctx, adam); err != nil {
			t.Fatal(err)
		}
		q, err := plain.Outbox(ctx, fado.OutboxFilter{}, adam)
		if err != nil || len(q) != 1 {
			t.Fatal(err)
		}
		if i == 0 {
			firstWait = time.Unix(q[0].DueAt, 0).Sub(plainClock).String()
		}
		if q[0].Status == "dead" {
			if got := fmt.Sprint(q[0].Attempts); got != given["MaxAttempts"] {
				t.Errorf("with no MaxAttempts an action is dead after %s attempts, and the chapter says %s", got, given["MaxAttempts"])
			}
			break
		}
		plainClock = plainClock.Add(time.Hour)
	}
	if firstWait != "1s" || given["Backoff"] != "1 second" {
		t.Errorf("with no Backoff the first wait is %s, and the chapter says %s", firstWait, given["Backoff"])
	}

	// Who may: an administrator sees the arguments of every action, and
	// by this group alone no document.
	if _, err := db.Get(ctx, "Order", held, adam); fado.ErrorCode(err) != "unknown_document" {
		t.Errorf("an administrator reads a document: %v", err)
	}
	administrator := fado.Principal{ID: "adam", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}}
	exec := func(name, object string) map[string]json.RawMessage {
		t.Helper()
		got, err := db.Exec(ctx, administrator, name, []byte(object))
		if err != nil {
			t.Fatalf("the command %s: %v", name, err)
		}
		var answer map[string]json.RawMessage
		if err := json.Unmarshal(got, &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}
	again := take("refused")
	commands := map[string]func(row []string){
		"list the queue": func(row []string) {
			if row[1] != "Outbox" || !strings.Contains(string(exec(row[2], `{"status":"pending","id":"`+again+`"}`)["actions"]), `"job.refused"`) {
				t.Errorf("the command %s does not list the queue", row[2])
			}
		},
		"run what is due, now": func(row []string) {
			if row[1] != "Drain" || string(exec(row[2], `{}`)["attempted"]) != "1" {
				t.Errorf("the command %s does not run what is due", row[2])
			}
		},
		"send a dead action again": func(row []string) {
			// The function was mended above, so the action is done; one
			// that is dead is made by a class whose action is refused.
			q := queue(again)
			if row[1] != "RetryAction" || len(q) != 1 {
				t.Fatalf("the queue of the order: %+v", q)
			}
			if q[0].Status != "done" {
				t.Fatalf("the action is %s", q[0].Status)
			}
			if _, err := db.Exec(ctx, administrator, row[2], []byte(`{"action_key":"`+q[0].Key+`"}`)); fado.ErrorCode(err) != "not_allowed" {
				t.Errorf("the command %s on a done action: %v", row[2], err)
			}
			if _, err := db.Exec(ctx, administrator, row[2], []byte(`{"action_key":"nothing:0"}`)); fado.ErrorCode(err) != "unknown_action" {
				t.Errorf("the command %s on a key the queue does not have: %v", row[2], err)
			}
		},
	}
	// The table is played in the order of the page: listing, sending
	// again, running.
	order2 := []string{"list the queue", "run what is due, now", "send a dead action again"}
	table := map[string][]string{}
	for _, row := range rows(marked(t, page, "the commands")) {
		table[row[0]] = row
	}
	if len(table) != len(commands) {
		t.Errorf("the chapter lists %d commands, and the test plays %d", len(table), len(commands))
	}
	for _, what := range order2 {
		row, ok := table[what]
		if !ok {
			t.Errorf("the chapter has no command to %s", what)
			continue
		}
		commands[what](row)
	}

	// What is refused.
	pendingKey := queue(take("works"))[0].Key
	refused := map[string][]error{
		"lists the queue, sends an action again or runs the queue, and is no administrator": {
			func() error { _, err := db.Outbox(ctx, fado.OutboxFilter{}, ola); return err }(),
			db.RetryAction(ctx, key, ola),
			func() error { _, err := db.Drain(ctx, ola); return err }(),
			func() error {
				_, err := db.Drain(ctx, setup, fado.As(fado.Principal{ID: "d", System: []string{fado.GroupDesigners}}))
				return err
			}(),
		},
		"sends again an action that is pending or done":             {db.RetryAction(ctx, pendingKey, adam), db.RetryAction(ctx, key, adam)},
		"sends again an action under a key the queue does not have": {db.RetryAction(ctx, "nothing:0", adam)},
		"defines an action with no request, and the program has no function under its name": {
			func() error { _, err := db.DefineAction(ctx, []byte(`{"name":"job.nowhere"}`), setup); return err }(),
		},
	}
	listedRows := map[string]bool{}
	for _, row := range rows(marked(t, page, "what is refused")) {
		listedRows[row[0]] = true
		if len(refused[row[0]]) == 0 {
			t.Errorf("the chapter says a caller who %q is refused, and the test has no such call", row[0])
		}
		for _, err := range refused[row[0]] {
			if got := codeOf(err); got != row[1] {
				t.Errorf("a caller who %s: the engine answers %s, and the chapter says %s", row[0], got, row[1])
			}
			var r *fado.Error
			if row[2] != "" && (!errors.As(err, &r) || r.Path != row[2]) {
				t.Errorf("a caller who %s: the refusal names %v, and the chapter says %s", row[0], err, row[2])
			}
		}
	}
	for what := range refused {
		if !listedRows[what] {
			t.Errorf("the test has a caller who %q, and the chapter has no such row", what)
		}
	}

	// A chain has an end: an echo that makes an echo, sixteen deep.
	echo := `{"name":"Echo","variables":{"n":{"type":"number"}},"states":{"initial":"a","list":["a"]},` +
		`"rules":[{"on":"create","do":[{"action":"create","class":"Echo","values":{"n":"n + 1"}}]}],` +
		`"access":{"read":"TRUE","create":"TRUE","update":"TRUE","delete":"TRUE"}}`
	if _, err := db.DefineClass(ctx, []byte(echo), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Create(ctx, "Echo", fado.Values{"n": 0}, ola); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && drain() > 0; i++ {
	}
	chain, err := db.Outbox(ctx, fado.OutboxFilter{Class: "Echo"}, adam)
	if err != nil || len(chain) == 0 {
		t.Fatal(err)
	}
	last := chain[len(chain)-1]
	for _, r := range chain {
		if r.Depth > last.Depth {
			last = r
		}
	}
	if last.Status != "dead" || last.ErrorCode != "too_deep" || last.Depth != 17 {
		t.Errorf("the end of a chain: depth %d, %s, %s; the chapter says sixteen deep and then too_deep", last.Depth, last.Status, last.ErrorCode)
	}
	// The caller's echo, and one for each action of depth 0 to 16.
	if n, err := db.Count(ctx, fado.Filter{Class: "Echo"}, ola); err != nil || n != 18 {
		t.Errorf("the chain made %d documents, %v", n, err)
	}

	// The worker starts when the database is opened, unless the program
	// keeps it from starting.
	ran := make(chan struct{})
	live, err := fado.Open(filepath.Join(t.TempDir(), "live.fado"), fado.Options{Actions: map[string]fado.ActionFunc{
		"job.works": func(context.Context, *fado.ActionCall) (fado.Values, error) { close(ran); return nil, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if _, err := live.DefineAction(ctx, []byte(`{"name":"job.works"}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := live.DefineClass(ctx, []byte(scratch("Job", `"n":{"type":"number"}`, oneState+`,"rules":[{"on":"create","do":[{"action":"job.works"}]}]`)), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := live.Create(ctx, "Job", fado.Values{"n": 1}, ola); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Error("the worker did not run an action by itself")
	}
}
