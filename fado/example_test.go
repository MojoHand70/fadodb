package fado_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The invoice of docs/architecture.md, as far as this build takes it: a
// class with defaults, objects, lines and auto formulas; a document
// created, changed, previewed and read back with its history.
func Example() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	db, err := fado.Open(filepath.Join(tmp, "example.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Invoice",
	  "title": "\"INV-\" & number",
	  "variables": {
	    "number":   {"type": "text", "required": true},
	    "customer": {"type": "ref", "class": "Customer"},
	    "issued":   {"type": "date", "default": "TODAY()"},
	    "due":      {"type": "date", "default": "ADDDAYS(issued, 30)"},
	    "owner":    {"type": "text", "default": "USER()"},
	    "discount": {"type": "decimal", "scale": 2, "default": "0"},
	    "net":      {"type": "money", "scale": 2},
	    "shipping": {"type": "object", "schema": "Address"},
	    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "number", "price": "decimal(2)"}},
	    "total":     {"formula": "ROUND(net * (1 - discount), 2)"},
	    "ship_city": {"formula": "shipping.city"}
	  },
	  "states": {"initial": "draft", "list": ["draft", "approved", "paid"]}
	}`))
	if err != nil {
		panic(err)
	}

	alice := fado.Principal{ID: "alice", Groups: []string{"sales"}}
	values := fado.Values{
		"number":   "2026-0042",
		"customer": "Customer:42",
		"net":      "100.00 EUR",
		"shipping": fado.Values{"line1": "Długa 1", "city": "Gdańsk", "country": "PL"},
		"lines":    []fado.Values{{"sku": "A1", "qty": 2, "price": "9.50"}},
	}
	doc, err := db.Create(ctx, "Invoice", values, fado.As(alice), fado.Key("ksef-2026-0042"), fado.WithID("inv_42"))
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.Title, doc.State, doc.Version, doc.Values["issued"], doc.Values["due"], doc.Values["owner"], doc.Values["total"], doc.Values["ship_city"])

	// The same command with the same key is the same commit, not a second
	// document.
	again, err := db.Create(ctx, "Invoice", values, fado.As(alice), fado.Key("ksef-2026-0042"), fado.WithID("inv_42"))
	fmt.Println("replayed:", err == nil && again.Version == doc.Version)

	// A change recalculates what depends on it and is a new version.
	doc, _ = db.Set(ctx, "Invoice", "inv_42", fado.Values{"discount": "0.10"}, fado.As(alice))
	fmt.Println(doc.Version, doc.Values["total"])

	// A preview computes and writes nothing.
	preview, _ := db.Preview(ctx, "Invoice", "inv_42", fado.Values{"discount": "0.50"}, "", fado.As(alice))
	current, _ := db.Get(ctx, "Invoice", "inv_42")
	fmt.Println("preview:", preview.Document.Values["total"], "stored:", current.Values["total"])

	// A refusal names the variable and a stable code.
	_, err = db.Set(ctx, "Invoice", "inv_42", fado.Values{"total": "1.00 EUR"})
	var e *fado.Error
	if errors.As(err, &e) {
		fmt.Println(e.Code, e.Variable)
	}

	// Every version is in the history, with who committed it.
	history, _ := db.History(ctx, "Invoice", "inv_42")
	for _, h := range history {
		fmt.Println("version", h.Version, h.Values["total"], h.ActorID, h.ValidTo == 0)
	}

	// A filter is a formula over the class's variables; SQL runs over the
	// views.
	open, _ := db.Query(ctx, fado.Filter{Class: "Invoice", Where: "state = \"draft\" AND total > 50.00", Fields: []string{"number", "total"}})
	fmt.Println("filtered:", len(open), open[0].Values["number"], open[0].Values["total"])
	rows, _ := db.SQL(ctx, `SELECT count(*) AS n, sum(total) AS cents FROM Invoice`, nil)
	fmt.Println("sql:", rows.Columns, rows.Rows[0])

	// A definition's problems come back at once, each with a place.
	_, err = db.DefineClass(ctx, []byte(`{"name": "Receipt", "variables": {"amount": {"type": "money", "default": "1"}, "paid": {"type": "bool", "formula": "amount"}}}`))
	var diags fado.Diagnostics
	if errors.As(err, &diags) {
		for _, d := range diags {
			fmt.Println(d.Path, d.Code)
		}
	}
	// Output:
	// INV-2026-0042 draft 1 2026-11-08 2026-12-08 alice 100.00 EUR Gdańsk
	// replayed: true
	// 2 90.00 EUR
	// preview: 50.00 EUR stored: 90.00 EUR
	// read_only total
	// version 1 100.00 EUR alice false
	// version 2 90.00 EUR alice true
	// filtered: 1 2026-0042 90.00 EUR
	// sql: [n cents] [1 9000]
	// /variables/amount/default type_mismatch
	// /variables/paid/formula type_mismatch
}

// Rules run inside the commit (ADR-0043): a transition stamps who took it
// and when, into variables nobody may write by hand, and says so in the
// event log; a preview reports what would run before anything does.
func Example_rules() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	db, err := fado.Open(filepath.Join(tmp, "rules.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Request",
	  "variables": {
	    "amount":      {"type": "money", "scale": 2},
	    "approved_by": {"type": "text"},
	    "approved_at": {"type": "datetime"},
	    "large":       {"formula": "AMOUNT(amount) > 1000"}
	  },
	  "states": {"initial": "open", "list": ["open", "approved"]},
	  "transitions": [{"name": "approve", "from": "open", "to": "approved"}],
	  "rules": [
	    {"on": "transition", "name": "approve",
	     "do": [{"action": "set", "values": {"approved_by": "USER()", "approved_at": "NOW()"}}]},
	    {"on": "enter", "state": "approved", "if": "large",
	     "do": [{"action": "emit", "event": "request.large_approved"}]}
	  ],
	  "rights": {"*": {"approved_by": {"write": "FALSE"}, "approved_at": {"write": "FALSE"}}}
	}`))
	if err != nil {
		panic(err)
	}
	fin := fado.Principal{ID: "fin", Groups: []string{"finance"}}
	doc, err := db.Create(ctx, "Request", fado.Values{"amount": "2500.00 EUR"}, fado.As(fin), fado.WithID("req_1"))
	if err != nil {
		panic(err)
	}

	// Nobody sets the stamp by hand.
	_, err = db.Set(ctx, "Request", doc.ID, fado.Values{"approved_by": "fin"}, fado.As(fin))
	var refusal *fado.Error
	if errors.As(err, &refusal) {
		fmt.Println("by hand:", refusal.Code)
	}

	// The preview names the rules the transition would run.
	preview, _ := db.Preview(ctx, "Request", doc.ID, nil, "approve", fado.As(fin))
	for _, r := range preview.Rules {
		fmt.Println("would run:", r.Path, r.Ran, r.Actions[0].Action)
	}

	doc, err = db.Transition(ctx, "Request", doc.ID, "approve", nil, fado.As(fin))
	if err != nil {
		panic(err)
	}
	fmt.Println(doc.State, "by", doc.Values["approved_by"], "at", doc.Values["approved_at"], "version", doc.Version)

	events, _ := db.Events(ctx, "Request", doc.ID)
	for _, e := range events {
		fmt.Println(e.Kind, e.Name, e.AfterVersion)
	}
	// Output:
	// by hand: forbidden
	// would run: /rules/0 true set
	// would run: /rules/1 true emit
	// approved by fin at 1794301200 version 2
	// created  1
	// transition approve 2
	// custom request.large_approved 2
}

// A triggered variable is computed when one of its triggers happens and
// kept in between (ADR-0043): the rate of a quote is locked when the
// quote is sent, and again when a Refresh asks, and the document says at
// which version and when.
func Example_triggered() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	db, err := fado.Open(filepath.Join(tmp, "triggered.fado"), fado.Options{
		Now: func() time.Time { return time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Quote",
	  "variables": {
	    "amount": {"type": "number"},
	    "rate":   {"type": "number"},
	    "locked": {"formula": "rate", "mode": "triggered", "on": ["transition:send", "refresh"]},
	    "price":  {"formula": "amount * locked"}
	  },
	  "states": {"initial": "draft", "list": ["draft", "sent"]},
	  "transitions": [{"name": "send", "from": "draft", "to": "sent"}]
	}`))
	if err != nil {
		panic(err)
	}
	doc, err := db.Create(ctx, "Quote", fado.Values{"amount": 200, "rate": 0.5}, fado.WithID("q_1"))
	if err != nil {
		panic(err)
	}
	fmt.Println("draft:", doc.Values["locked"], len(doc.Computed))

	doc, _ = db.Transition(ctx, "Quote", doc.ID, "send", nil)
	fmt.Println("sent:", doc.Values["locked"], doc.Values["price"], "computed at version", doc.Computed["locked"].Version)

	// The rate moves on; the locked value and its moment stay.
	doc, _ = db.Set(ctx, "Quote", doc.ID, fado.Values{"rate": 0.75})
	fmt.Println("later:", doc.Values["locked"], doc.Values["price"], "version", doc.Version, "computed at version", doc.Computed["locked"].Version)

	// Until somebody asks for it again.
	doc, _ = db.Refresh(ctx, "Quote", doc.ID, []string{"locked"})
	fmt.Println("refreshed:", doc.Values["locked"], doc.Values["price"], "computed at version", doc.Computed["locked"].Version)
	// Output:
	// draft: <nil> 0
	// sent: 0.5 100 computed at version 2
	// later: 0.5 100 version 3 computed at version 2
	// refreshed: 0.75 150 computed at version 4
}

// A rule acts on other documents through the outbox (ADR-0044): a
// purchase asks each of its approvers, and the first approval that is
// granted approves the purchase. The actions run after the commit that
// queued them, exactly once, as the person whose command it was. Here
// the queue is run by hand with Drain; without Outbox.Manual a worker
// runs it.
func Example_approvals() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	db, err := fado.Open(filepath.Join(tmp, "approvals.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	purchase := func(rules string) string {
		return `{
	  "name": "Purchase",
	  "variables": {"approvers": {"type": "array<text>"}, "approved_by": {"type": "text"}},
	  "states": {"initial": "draft", "list": ["draft", "waiting", "approved"]},
	  "transitions": [{"name": "submit", "from": "draft", "to": "waiting"},
	                  {"name": "approve", "from": "waiting", "to": "approved"}]` + rules + `
	}`
	}
	// One approval for each element of the list, the element read as "who".
	const asks = `,
	  "rules": [{"on": "transition", "name": "submit",
	             "do": [{"action": "create", "class": "Approval", "each": "approvers", "as": "who",
	                     "id": "ID() & \"/\" & who", "values": {"purchase": "ID()", "approver": "who"}}]}]`
	// Granting an approval takes the purchase's own transition.
	const approval = `{
	  "name": "Approval",
	  "variables": {"purchase": {"type": "text", "required": true}, "approver": {"type": "text", "required": true}},
	  "states": {"initial": "asked", "list": ["asked", "granted"]},
	  "transitions": [{"name": "grant", "from": "asked", "to": "granted"}],
	  "access": {"transition": {"grant": "approver = USER()"}},
	  "rules": [{"on": "enter", "state": "granted",
	             "do": [{"action": "transition", "class": "Purchase", "id": "purchase", "name": "approve",
	                     "values": {"approved_by": "approver"}}]}]
	}`
	// The two classes name each other, and a rule's target is checked when
	// its class is defined: the purchase comes first without its rule.
	for _, def := range []string{purchase(""), approval, purchase(asks)} {
		if _, err := db.DefineClass(ctx, []byte(def)); err != nil {
			panic(err)
		}
	}

	sam, bob := fado.Principal{ID: "sam"}, fado.Principal{ID: "bob"}
	doc, err := db.Create(ctx, "Purchase", fado.Values{"approvers": []any{"ann", "bob"}}, fado.As(sam), fado.WithID("po_1"))
	if err != nil {
		panic(err)
	}
	if _, err := db.Transition(ctx, "Purchase", doc.ID, "submit", nil, fado.As(sam)); err != nil {
		panic(err)
	}
	asked, _ := db.Drain(ctx)
	fmt.Println("approvals asked:", asked)

	// Sam may not grant Bob's approval; Bob may.
	_, err = db.Transition(ctx, "Approval", "po_1/bob", "grant", nil, fado.As(sam))
	var refusal *fado.Error
	if errors.As(err, &refusal) {
		fmt.Println("sam grants:", refusal.Code)
	}
	if _, err := db.Transition(ctx, "Approval", "po_1/bob", "grant", nil, fado.As(bob)); err != nil {
		panic(err)
	}
	if _, err := db.Drain(ctx); err != nil {
		panic(err)
	}

	doc, _ = db.Get(ctx, "Purchase", "po_1")
	fmt.Println(doc.State, "by", doc.Values["approved_by"])
	events, _ := db.Events(ctx, "Purchase", "po_1")
	for _, e := range events {
		fmt.Println(e.Kind, e.Name, "as", e.ActorID)
	}
	// Output:
	// approvals asked: 2
	// sam grants: forbidden
	// approved by bob
	// created  as sam
	// transition submit as sam
	// action_run create as sam
	// action_run create as sam
	// transition approve as bob
}

// An action of the host program's own (ADR-0045). The definition says
// what the action takes and returns, and is stored in the database like a
// class; a Go function given to Open does the work; a rule names the
// action and gives its arguments as formulas. After the commit the outbox
// calls the function with the arguments and with the event that fired
// the rule, the document included. A misspelt parameter is refused when
// the class is defined, and an agent can list the action and try it
// before any document uses it.
func Example_goAction() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	// The function: here it prints; a real one posts the message.
	notify := func(_ context.Context, call *fado.ActionCall) (fado.Values, error) {
		fmt.Printf("%s to %v: %v\n", call.Action, call.Args["channel"], call.Args["text"])
		if ev := call.Event; ev != nil {
			fmt.Printf("  fired by %s %s of %s, now %s, as %s\n", ev.Kind, ev.Name, ev.Class, ev.Document.State, ev.ActorID)
		}
		return fado.Values{"ts": "1767225600.000100"}, nil
	}
	db, err := fado.Open(filepath.Join(tmp, "actions.fado"), fado.Options{
		Outbox:  fado.OutboxOptions{Manual: true},
		Actions: map[string]fado.ActionFunc{"team.notify": notify},
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.DefineAction(ctx, []byte(`{
	  "name": "team.notify",
	  "label": "Notify the team",
	  "params": {"channel": {"type": "text", "required": true, "description": "Where to post"},
	             "text":    {"type": "text", "required": true}},
	  "returns": {"ts": "text"}
	}`)); err != nil {
		panic(err)
	}
	invoice := func(args string) []byte {
		return []byte(`{
	  "name": "Invoice",
	  "variables": {"number": {"type": "text"}},
	  "states": {"initial": "draft", "list": ["draft", "approved"]},
	  "transitions": [{"name": "approve", "from": "draft", "to": "approved"}],
	  "rules": [{"on": "enter", "state": "approved",
	             "do": [{"action": "team.notify", "args": ` + args + `}]}]
	}`)
	}
	// The arguments are checked against the definition now, not when the
	// first invoice is approved.
	for _, d := range db.ValidateClass(invoice(`{"channel": "\"#finance\"", "txt": "number"}`)) {
		fmt.Println(d.Code, d.Path)
	}
	if _, err := db.DefineClass(ctx, invoice(`{"channel": "\"#finance\"", "text": "\"Approved: \" & number"}`)); err != nil {
		panic(err)
	}

	// The catalogue says what a rule may name, and a trial runs an action
	// outside any document.
	info, _ := db.Action("team.notify")
	fmt.Println(info.Name, "is", info.Kind, "and takes", info.Params[0].Name, "and", info.Params[1].Name)
	trial, err := db.TryAction(ctx, "team.notify", fado.Values{"channel": "#test", "text": "hello"})
	if err != nil {
		panic(err)
	}
	fmt.Println("trial ok:", trial.OK, "ts:", trial.Result["ts"])

	ann := fado.Principal{ID: "ann"}
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "INV-7"}, fado.As(ann))
	if err != nil {
		panic(err)
	}
	if _, err := db.Transition(ctx, "Invoice", doc.ID, "approve", nil, fado.As(ann)); err != nil {
		panic(err)
	}
	fmt.Println("committed; the action is queued")
	if _, err := db.Drain(ctx); err != nil {
		panic(err)
	}
	events, _ := db.Events(ctx, "Invoice", doc.ID)
	for _, e := range events {
		fmt.Println(e.Kind + ":" + e.Name)
	}
	// Output:
	// unknown_parameter /rules/0/do/0/args/txt
	// missing /rules/0/do/0/args
	// team.notify is registered and takes channel and text
	// team.notify to #test: hello
	// trial ok: true ts: 1767225600.000100
	// committed; the action is queued
	// team.notify to #finance: Approved: INV-7
	//   fired by transition approve of Invoice, now approved, as ann
	// created:
	// transition:approve
	// action_run:team.notify
}

// What is due (ADR-0047): an invoice passes its date while nothing
// touches it. Due lists it, now or at a moment ahead, and a refresh, a
// commit that changes nothing, moves it.
func Example_due() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	var days atomic.Int64
	now := func() time.Time { return start.AddDate(0, 0, int(days.Load())) }
	db, err := fado.Open(filepath.Join(tmp, "example.fado"), fado.Options{Now: now})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Invoice",
	  "variables": {"due": {"type": "date", "index": true}},
	  "states": {"initial": "sent", "list": ["sent", "overdue", "paid"], "final": ["paid"]},
	  "transitions": [
	    {"from": "sent", "to": "overdue", "guard": "TODAY() > due"},
	    {"name": "pay", "from": ["sent", "overdue"], "to": "paid"}
	  ]
	}`))
	if err != nil {
		panic(err)
	}
	for id, due := range map[string]string{"INV-1": "2026-11-10", "INV-2": "2026-11-20"} {
		if _, err := db.Create(ctx, "Invoice", fado.Values{"due": due}, fado.WithID(id)); err != nil {
			panic(err)
		}
	}

	due, err := db.Due(ctx, "Invoice")
	if err != nil {
		panic(err)
	}
	fmt.Println("today:", len(due), "due")

	// What will be due in a week, asked today.
	due, err = db.Due(ctx, "Invoice", fado.At(now().AddDate(0, 0, 7)))
	if err != nil {
		panic(err)
	}
	for _, d := range due {
		fmt.Println("in a week:", d.ID, d.State, "->", d.To)
	}

	// Five days pass. The invoice is as it was written: Due finds it, and
	// a refresh moves it.
	days.Store(5)
	due, err = db.Due(ctx, "Invoice")
	if err != nil {
		panic(err)
	}
	for _, d := range due {
		doc, err := db.Refresh(ctx, "Invoice", d.ID, nil)
		if err != nil {
			panic(err)
		}
		fmt.Println(d.ID, "was", d.State, "and is", doc.State)
	}
	due, err = db.Due(ctx, "Invoice")
	if err != nil {
		panic(err)
	}
	fmt.Println("after the touch:", len(due), "due")

	// Output:
	// today: 0 due
	// in a week: INV-1 sent -> overdue
	// INV-1 was sent and is overdue
	// after the touch: 0 due
}

// A timed transition carries its moment: an after, counted from the
// entry into the state, or an at, a date the document holds. It fires in
// the first commit that touches the document once its time has come.
// Transitions shows the moment, and Due lists the documents whose time
// has come.
func Example_timed() {
	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	var days atomic.Int64
	now := func() time.Time { return start.AddDate(0, 0, int(days.Load())) }
	db, err := fado.Open(filepath.Join(tmp, "example.fado"), fado.Options{Now: now})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	_, err = db.DefineClass(ctx, []byte(`{
	  "name": "Request",
	  "variables": {"deadline": {"type": "date"}, "reminders": {"type": "number", "default": "0"}},
	  "states": {"initial": "waiting", "list": ["waiting", "expired", "closed"], "final": ["expired", "closed"]},
	  "transitions": [
	    {"name": "remind", "from": "waiting", "to": "waiting", "after": "3d"},
	    {"from": "waiting", "to": "expired", "at": "deadline"},
	    {"name": "close", "from": "waiting", "to": "closed"}
	  ],
	  "rules": [{"on": "transition", "name": "remind", "do": [{"action": "set", "values": {"reminders": "reminders + 1"}}]}]
	}`))
	if err != nil {
		panic(err)
	}
	if _, err := db.Create(ctx, "Request", fado.Values{"deadline": "2026-11-10"}, fado.WithID("R-1")); err != nil {
		panic(err)
	}

	stamp := func(secs int64) string { return time.Unix(secs, 0).UTC().Format("Mon 2 Jan 15:04") }
	open, err := db.Transitions(ctx, "Request", "R-1")
	if err != nil {
		panic(err)
	}
	for _, t := range open {
		if t.FiresAt != 0 {
			fmt.Printf("to %s on %s\n", t.To, stamp(t.FiresAt))
		}
	}

	// Three days pass. The reminder is due, and a touch fires it.
	days.Store(3)
	due, err := db.Due(ctx, "Request")
	if err != nil {
		panic(err)
	}
	for _, d := range due {
		doc, err := db.Refresh(ctx, "Request", d.ID, nil)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s was due since %s: %v reminder, still %s\n", d.ID, stamp(d.Since), doc.Values["reminders"], doc.State)
	}

	// On the day of the deadline the next touch moves it on.
	days.Store(8)
	doc, err := db.Refresh(ctx, "Request", "R-1", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("on the 10th:", doc.State)

	// Output:
	// to waiting on Thu 5 Nov 09:00
	// to expired on Tue 10 Nov 00:00
	// R-1 was due since Thu 5 Nov 09:00: 1 reminder, still waiting
	// on the 10th: expired
}
