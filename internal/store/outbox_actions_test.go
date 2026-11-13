package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// The classes of the tests of each: a job whose rules make a task for
// each member of its crew when it is created and when it is deleted, and
// one for each of its parts when it turns large by itself.
const (
	taskClass = `{"name": "Task",
 "variables": {"job": {"type": "text"}, "who": {"type": "text"}, "why": {"type": "text"}, "n": {"type": "number"}},
 "states": {"initial": "todo", "list": ["todo"]}}`
	jobClass = `{"name": "Job",
 "variables": {"crew": {"type": "array<text>"}, "parts": {"type": "rows", "schema": {"sku": "text", "qty": "number"}}, "big": {"type": "bool"}%s},
 "states": {"initial": "new", "list": ["new", "large", "done"]},
 "transitions": [{"from": "new", "to": "large", "guard": "big"}, {"name": "finish", "from": ["new", "large"], "to": "done"}],
 "rules": [
   {"on": "create", "do": [{"action": "create", "class": "Task", "each": "crew", "as": "member", "values": {"job": "ID()", "who": "member", "why": "\"made\""}}]},
   {"on": "enter", "state": "large", "do": [{"action": "create", "class": "Task", "each": "parts", "as": "part", "values": {"job": "ID()", "who": "part.sku", "n": "part.qty", "why": "\"large\""}}]},
   {"on": "delete", "do": [{"action": "create", "class": "Task", "each": "crew", "as": "member", "values": {"job": "ID()", "who": "member", "why": "\"gone\""}}]}%s
 ]}`
)

// tasks are the tasks the actions made, each as "why who", sorted.
func tasks(t testing.TB, s *Store) []string {
	t.Helper()
	found, err := s.Query(context.Background(), Filter{Class: "Task"})
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, d := range found {
		out = append(out, d.Values["why"].(string)+" "+d.Values["who"].(string))
	}
	sort.Strings(out)
	return out
}

// A create with each goes over the list as the commit that fires the
// rule has it (ADR-0044, item 12): the list a create was given, the list
// the document holds when an eventless transition fires, and the list as
// it was when the document is deleted, whichever version of the class
// computed the document. A draft is previewed over the stored list.
func TestOutbox_EachOverEveryCommit(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{taskClass, strings.NewReplacer("%s", "").Replace(jobClass)} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.Create(ctx, "Job", Values{"crew": []any{"ann", "bob"}, "parts": []any{Values{"sku": "desk", "qty": 2}}, "big": true})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "large" || job.Version != 2 {
		t.Fatalf("the job: %s", dump(job))
	}
	// Two rows of the create, keyed by its event, and one of the eventless
	// commit, keyed by that commit's.
	events, _ := s.Events(ctx, "Job", job.ID)
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: job.ID}, asSetup)
	if len(rows) != 3 || rows[0].Key != events[0].ID+":0" || rows[1].Key != events[0].ID+":1" || rows[2].Key != events[1].ID+":0" || rows[2].Version != 2 {
		t.Fatalf("the rows of the create and its eventless transition: %s", dump(rows))
	}
	if got := string(rows[2].Args); got != `{"class":"Task","values":{"job":"`+job.ID+`","n":2,"who":"desk","why":"large"}}` {
		t.Errorf("the arguments over an element of a rows variable: %s", got)
	}
	// A delete goes over the list as it was, here the one a set left.
	if _, err := s.Set(ctx, "Job", job.ID, Values{"crew": []any{"cy"}}); err != nil {
		t.Fatal(err)
	}
	// A document an older version of the class computed: its list is read
	// all the same.
	old, err := s.Create(ctx, "Job", Values{"crew": []any{"dee"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(strings.Replace(strings.Replace(jobClass, "%s", `, "extra": {"type": "text"}`, 1), "%s", "", 1)), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{job.ID, old.ID} {
		if err := s.Delete(ctx, "Job", id); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 6 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if got := strings.Join(tasks(t, s), "|"); got != "gone cy|gone dee|large desk|made ann|made bob|made dee" {
		t.Errorf("tasks %s", got)
	}
	// A draft whose rule goes over a stored list is previewed over it, and
	// over the list the preview itself gives.
	keep, _ := s.Create(ctx, "Job", Values{"crew": []any{"eve", "fay"}})
	draft := strings.Replace(strings.Replace(jobClass, "%s", `, "extra": {"type": "text"}`, 1), "%s",
		`, {"on": "transition", "name": "finish", "do": [{"action": "create", "class": "Task", "each": "crew", "as": "member", "values": {"who": "member"}}]}`, 1)
	for _, tc := range []struct {
		values Values
		want   string
	}{{nil, "eve fay"}, {Values{"crew": []any{"gus"}}, "gus"}} {
		pv, err := s.PreviewDraft(ctx, opened([]byte(draft)), keep.ID, tc.values, "finish", asSetup)
		if err != nil {
			t.Fatal(err)
		}
		var who []string
		for _, r := range pv.Rules {
			for _, a := range r.Actions {
				who = append(who, a.Args["values"].(Values)["who"].(string))
			}
		}
		if strings.Join(who, " ") != tc.want {
			t.Errorf("the draft's preview goes over %v, want %s", who, tc.want)
		}
	}
	if pending, _ := s.Outbox(ctx, OutboxFilter{Status: ActionPending}, asSetup); len(pending) != 2 {
		// The two of the job just created; the previews queued nothing.
		t.Errorf("pending after the previews: %s", dump(pending))
	}
}

// A redefinition may not break a rule of another class (ADR-0044, item
// 7): what removes or retypes something such a rule fills, or takes away
// the transition it takes or the variable it refreshes, is refused with
// not_allowed naming the rule, and goes through once that rule is
// changed. A new input the class requires is not refused: the rule
// cannot give it before it exists, and its action is then refused with
// required when it runs.
func TestOutbox_RedefinitionMayNotBreakARule(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	receipt := func(variables, more string) string {
		return `{"name": "Receipt", "variables": {"invoice": {"type": "text", "required": true}, ` + variables + `},
 "states": {"initial": "issued", "list": ["issued", "void"]}, "transitions": [` + more + `]}`
	}
	const (
		amount = `"amount": {"type": "number"}, "seen": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}`
		cancel = `{"name": "cancel", "from": "issued", "to": "void"}`
	)
	invoice := func(rules string) string {
		return `{"name": "Invoice", "variables": {"total": {"type": "number"}, "receipt": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "paid", "void"]},
 "transitions": [{"name": "pay", "from": "open", "to": "paid"}, {"name": "void", "from": "paid", "to": "void"}],
 "rules": [` + rules + `]}`
	}
	const (
		create  = `{"on": "enter", "state": "paid", "do": [{"action": "emit", "event": "invoice.paid"}, {"action": "create", "class": "Receipt", "id": "\"R-\" & ID()", "values": {"invoice": "ID()", "amount": "total"}}]}`
		moves   = `{"on": "enter", "state": "void", "do": [{"action": "transition", "class": "Receipt", "id": "receipt", "name": "cancel"}, {"action": "refresh", "class": "Receipt", "id": "receipt", "variables": ["seen"]}]}`
		noRules = `{"on": "enter", "state": "paid", "do": [{"action": "emit", "event": "invoice.paid"}]}`
	)
	for _, def := range []string{receipt(amount, cancel), invoice(create + ", " + moves)} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, def, path, action string }{
		{"a variable a create fills is removed", receipt(`"seen": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}`, cancel), "/variables/amount", "/rules/0/do/1/values/amount"},
		{"it takes a type the formula does not have", receipt(`"amount": {"type": "text"}, "seen": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}`, cancel), "/variables/amount", "/rules/0/do/1/values/amount"},
		{"it becomes computed", receipt(`"amount": {"formula": "1 + 1"}, "seen": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}`, cancel), "/variables/amount", "/rules/0/do/1/values/amount"},
		{"the transition an action takes is removed", receipt(amount, ""), "/transitions", "/rules/1/do/0/name"},
		{"the variable a refresh names no longer lists refresh", receipt(`"amount": {"type": "number"}, "seen": {"formula": "NOW()", "mode": "triggered", "on": ["enter:void"]}`, cancel), "/variables", "/rules/1/do/1/variables/0"},
	} {
		_, err := s.defineOpen(ctx, []byte(tc.def), asSetup)
		var diags model.Diagnostics
		if !errors.As(err, &diags) {
			t.Errorf("%s: want the redefinition refused, got %v", tc.name, err)
			continue
		}
		found := false
		for _, d := range diags {
			found = found || d.Code == model.CodeNotAllowed && d.Path == tc.path && strings.Contains(d.Message, "the class Invoice") && strings.Contains(d.Message, tc.action)
		}
		if !found {
			t.Errorf("%s: want not_allowed at %s naming %s of Invoice, got:\n%s", tc.name, tc.path, tc.action, diags.Error())
		}
		// Validate says the same and stores nothing.
		if got := s.Validate(opened([]byte(tc.def))); !reflect.DeepEqual(got, diags) {
			t.Errorf("%s: Validate says %v", tc.name, got)
		}
	}
	if cls, _ := s.Class("Receipt"); cls.Version != 1 {
		t.Fatalf("a refused redefinition made version %d", cls.Version)
	}
	// What breaks no rule goes through: a further variable, another
	// transition, and a type the formula still fits.
	if _, err := s.defineOpen(ctx, []byte(receipt(amount+`, "note": {"type": "text"}`, cancel+`, {"name": "reissue", "from": "void", "to": "issued"}`)), asSetup); err != nil {
		t.Fatalf("a redefinition that breaks nothing: %v", err)
	}
	// A new required input is no refusal: the create that does not give it
	// is refused when it runs, and says why.
	if _, err := s.defineOpen(ctx, []byte(receipt(amount+`, "payer": {"type": "text", "required": true}`, cancel)), asSetup); err != nil {
		t.Fatalf("a new required input: %v", err)
	}
	inv, _ := s.Create(ctx, "Invoice", Values{"total": 10})
	if _, err := s.Transition(ctx, "Invoice", inv.ID, "pay", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if rows, _ := s.Outbox(ctx, OutboxFilter{DocID: inv.ID}, asSetup); len(rows) != 1 || rows[0].Status != ActionDead || rows[0].ErrorCode != CodeRequired {
		t.Errorf("the create that lacks the new input: %s", dump(rows))
	}
	// Changing the rule first frees the class.
	if _, err := s.defineOpen(ctx, []byte(invoice(noRules)), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(receipt(`"note": {"type": "text"}`, "")), asSetup); err != nil {
		t.Errorf("the redefinition after the rule was changed: %v", err)
	}

	// A table a rule writes: a column the rule sets may not go, and a new
	// shape that keeps what the rule gives is taken.
	rates := func(columns string) string {
		return `{"name": "Rates", "columns": {"code": "text", ` + columns + `}, "key": ["code"]}`
	}
	if _, err := s.defineOpenTable(ctx, []byte(rates(`"rate": "number"`)), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Quote", "variables": {"code": {"type": "text"}, "rate": {"type": "number"}},
 "states": {"initial": "open", "list": ["open"]},
 "rules": [{"on": "create", "do": [{"action": "table.insert", "table": "Rates", "row": {"code": "code", "rate": "rate"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	_, err := s.defineOpenTable(ctx, []byte(rates(`"price": "number"`)), asSetup)
	var diags model.Diagnostics
	if !errors.As(err, &diags) || len(diags) != 1 || diags[0].Code != model.CodeNotAllowed || diags[0].Path != "/columns/rate" ||
		!strings.Contains(diags[0].Message, "the class Quote") || !strings.Contains(diags[0].Message, "/rules/0/do/0/row/rate") {
		t.Errorf("a table that loses a column a rule sets: %v", err)
	}
	if _, err := s.defineOpenTable(ctx, []byte(rates(`"rate": "number", "note": "text"`)), asSetup); err != nil {
		t.Errorf("a table that keeps what the rule gives: %v", err)
	}
}

// The table actions over a key of two columns and a money column: the
// row is addressed by every column of its key, a money is written with
// its currency, and what the action left is what the next lookup reads.
func TestOutbox_TableActionsKeysAndMoney(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpenTable(ctx, []byte(pricesTable), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Prices", priceRows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Offer",
 "variables": {"sku": {"type": "text"}, "qty": {"type": "number"}, "price": {"type": "money", "scale": 2},
               "listed": {"formula": "LOOKUP(Prices, price, sku = THIS.sku AND min_qty = THIS.qty)"}},
 "states": {"initial": "draft", "list": ["draft", "listed", "repriced", "withdrawn"]},
 "transitions": [{"name": "list", "from": "draft", "to": "listed"}, {"name": "reprice", "from": "listed", "to": "repriced"}, {"name": "withdraw", "from": "repriced", "to": "withdrawn"}],
 "rules": [
   {"on": "transition", "name": "list", "do": [{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": "qty", "price": "price"}}]},
   {"on": "transition", "name": "reprice", "do": [{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "qty"}, "set": {"price": "price * 2"}}]},
   {"on": "transition", "name": "withdraw", "do": [{"action": "table.delete", "table": "Prices", "key": {"min_qty": "qty", "sku": "sku"}}]}
 ]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	offer, err := s.Create(ctx, "Offer", Values{"sku": "A1", "qty": 5, "price": "7.25 PLN"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	// row is the price of A1 from five up as the table has it, or "".
	row := func() string {
		rows, err := s.TableRows(ctx, "Prices")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r["sku"] == "A1" && dump(r["min_qty"]) == "5" {
				return r["price"].(string)
			}
		}
		return ""
	}
	// listed is what a new offer of the same key reads through its lookup.
	listed := func() any {
		doc, err := s.Create(ctx, "Offer", Values{"sku": "A1", "qty": 5})
		if err != nil {
			t.Fatal(err)
		}
		return doc.Values["listed"]
	}
	for _, step := range []struct {
		transition, row string
		listed          any
	}{{"list", "7.25 PLN", "7.25 PLN"}, {"reprice", "14.50 PLN", "14.50 PLN"}, {"withdraw", "", nil}} {
		if _, err := s.Transition(ctx, "Offer", offer.ID, step.transition, nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			t.Fatalf("%s: drain %d, %v", step.transition, n, err)
		}
		if got := row(); got != step.row {
			t.Errorf("after %s the row is %q, want %q", step.transition, got, step.row)
		}
		if got := listed(); !reflect.DeepEqual(got, step.listed) {
			t.Errorf("after %s a lookup reads %v, want %v", step.transition, got, step.listed)
		}
	}
	// The other rows of the table were never touched.
	if rows, _ := s.TableRows(ctx, "Prices"); len(rows) != len(priceRows) {
		t.Errorf("the table has %d rows, want %d", len(rows), len(priceRows))
	}
	// The outcome of each is an event of the offer, and of nothing else.
	events, _ := s.Events(ctx, "Offer", offer.ID)
	var kinds []string
	for _, e := range events {
		if e.Kind == EventActionRun {
			kinds = append(kinds, e.Name)
		}
	}
	if strings.Join(kinds, " ") != "table.insert table.update table.delete" {
		t.Errorf("the outcomes on the offer: %v", kinds)
	}
}

// The pattern the actions on other documents are for, run by the worker
// itself: an invoice asks each of its approvers, a granted approval
// writes a row and wakes the invoice, and the invoice moves by its own
// guard when it looks (ADR-0044, items 12 and 13).
func TestOutbox_AChildWakesItsParent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wake.fado"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.defineOpenTable(ctx, []byte(`{"name": "Granted", "columns": {"invoice": "text", "approver": "text"}, "key": ["invoice", "approver"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	// The bill's rule names the approvals and the approval's rule names
	// the bill, so the bill is defined first without its rule.
	bill := func(rules string) string {
		return `{"name": "Bill",
 "variables": {"number": {"type": "text"}, "approvers": {"type": "array<text>"},
               "granted": {"formula": "LOOKUP(Granted, approver, invoice = THIS.number) <> \"\"", "type": "bool", "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "draft", "list": ["draft", "waiting", "approved"]},
 "transitions": [{"name": "submit", "from": "draft", "to": "waiting"}, {"from": "waiting", "to": "approved", "guard": "granted"}]` + rules + `}`
	}
	for _, def := range []string{
		bill(""),
		`{"name": "Approval", "variables": {"bill": {"type": "text", "required": true}, "approver": {"type": "text", "required": true}},
 "states": {"initial": "asked", "list": ["asked", "granted"]},
 "transitions": [{"name": "grant", "from": "asked", "to": "granted"}],
 "rules": [{"on": "enter", "state": "granted",
            "do": [{"action": "table.insert", "table": "Granted", "row": {"invoice": "bill", "approver": "approver"}},
                   {"action": "refresh", "class": "Bill", "id": "bill", "variables": ["granted"]}]}]}`,
		bill(`, "rules": [{"on": "transition", "name": "submit",
 "do": [{"action": "create", "class": "Approval", "each": "approvers", "as": "who", "id": "ID() & \"/\" & who", "values": {"bill": "ID()", "approver": "who"}}]}]`),
	} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := s.Create(ctx, "Bill", Values{"number": "B-1", "approvers": []any{"ann", "bob"}}, WithID("B-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Bill", doc.ID, "submit", nil); err != nil {
		t.Fatal(err)
	}
	// wait polls until the worker has made cond true.
	wait := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("the worker never got to: %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	wait("two approvals", func() bool { n, _ := s.Count(ctx, Filter{Class: "Approval"}); return n == 2 })
	if got, _ := s.Get(ctx, "Bill", doc.ID); got.State != "waiting" {
		t.Fatalf("the bill before anyone granted: %s", got.State)
	}
	if _, err := s.Transition(ctx, "Approval", "B-1/bob", "grant", nil); err != nil {
		t.Fatal(err)
	}
	wait("the bill approved", func() bool { got, _ := s.Get(ctx, "Bill", doc.ID); return got.State == "approved" })
	if dead, _ := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup); len(dead) != 0 {
		t.Errorf("dead actions: %s", dump(dead))
	}
}

// The acceptance test of ADR-0044, item 10, for every action the engine
// runs itself: one commit queues a table.insert, a transition and a
// refresh of another document and a create for each of two elements; the
// process is stopped before the worker, and inside and after each of the
// five actions; the file is reopened and counted. Each has happened
// exactly once.
func TestOutbox_EveryActionSurvivesACrash(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the test binary eleven times")
	}
	steps := []string{"queued"}
	for i := 0; i < 5; i++ {
		steps = append(steps, "performed:"+strconv.Itoa(i), "committed:"+strconv.Itoa(i))
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.fado")
			cmd := exec.Command(os.Args[0], "-test.run=^TestOutboxCrashActionsHelper$")
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
			if _, err := s.Drain(ctx, asSetup); err != nil {
				t.Fatal(err)
			}
			rows, _ := s.Outbox(ctx, OutboxFilter{}, asSetup)
			var got []string
			for _, r := range rows {
				got = append(got, r.Action+" "+r.Status+" "+strconv.Itoa(r.Attempts))
			}
			if strings.Join(got, "|") != "table.insert done 1|transition done 1|refresh done 1|create done 1|create done 1" {
				t.Errorf("the rows: %v", got)
			}
			if log, _ := s.TableRows(ctx, "Log"); len(log) != 1 {
				t.Errorf("the table has %s, want the one row", dump(log))
			}
			// The hub was created, moved once and refreshed once.
			hub, err := s.Get(ctx, "Hub", "hub")
			if err != nil || hub.State != "busy" || hub.Version != 3 {
				t.Errorf("the hub: %v, %s", err, dump(hub))
			}
			if got := notes(t, s); strings.Join(got, "|") != "a|b" {
				t.Errorf("notes %v, want each once", got)
			}
			works, _ := s.Query(ctx, Filter{Class: "Work"})
			events, _ := s.Events(ctx, "Work", works[0].ID)
			var kinds []string
			for _, e := range events {
				kinds = append(kinds, e.Kind)
			}
			if strings.Join(kinds, " ") != "created transition action_run action_run action_run action_run action_run" {
				t.Errorf("events %v", kinds)
			}
		})
	}
}

// TestOutboxCrashActionsHelper is the process
// TestOutbox_EveryActionSurvivesACrash stops: one commit queues five
// actions, and it exits without closing the store at the step, and at
// the action, the environment names.
func TestOutboxCrashActionsHelper(t *testing.T) {
	path, at := os.Getenv("FADODB_CRASH_DB"), os.Getenv("FADODB_CRASH_AT")
	if path == "" {
		t.Skip("run by TestOutbox_EveryActionSurvivesACrash")
	}
	step, which, _ := strings.Cut(at, ":")
	idx, _ := strconv.ParseInt(which, 10, 64)
	s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.defineOpenTable(ctx, []byte(`{"name": "Log", "columns": {"id": "text", "text": "text"}, "key": ["id"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{noteClass,
		`{"name": "Hub", "variables": {"seen": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "idle", "list": ["idle", "busy"]}, "transitions": [{"name": "start", "from": "idle", "to": "busy"}]}`,
		`{"name": "Work", "variables": {"tags": {"type": "array<text>"}},
 "states": {"initial": "new", "list": ["new", "gone"]}, "transitions": [{"name": "go", "from": "new", "to": "gone"}],
 "rules": [{"on": "transition", "name": "go", "do": [
   {"action": "table.insert", "table": "Log", "row": {"id": "ID()", "text": "\"went\""}},
   {"action": "transition", "class": "Hub", "id": "\"hub\"", "name": "start"},
   {"action": "refresh", "class": "Hub", "id": "\"hub\""},
   {"action": "create", "class": "Note", "each": "tags", "as": "tag", "values": {"about": "ID()", "text": "tag"}}]}]}`} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "Hub", nil, WithID("hub")); err != nil {
		t.Fatal(err)
	}
	work, err := s.Create(ctx, "Work", Values{"tags": []any{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Work", work.ID, "go", nil); err != nil {
		t.Fatal(err)
	}
	if step == "queued" {
		os.Exit(crashExit)
	}
	s.testStep = func(now string, row *outboxRow) *Error {
		if now == step && row.idx == idx {
			os.Exit(crashExit)
		}
		return nil
	}
	_, err = s.Drain(ctx, asSetup)
	t.Fatalf("the helper reached the end of the drain: %v", err)
}
