package fado_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The guides of the manual each show a class and a table of what it
// does. These tests take the class out of the guide, define it, and play
// the table, so that a guide cannot promise what the engine does not do.

// guideClass returns the first whole class a guide shows.
func guideClass(t *testing.T, guide string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "docs", "manual", "guides", guide))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(data, -1) {
		var whole struct {
			Name      string          `json:"name"`
			Variables json.RawMessage `json:"variables"`
		}
		if json.Unmarshal(m[1], &whole) == nil && whole.Name != "" && whole.Variables != nil {
			return m[1]
		}
	}
	t.Fatalf("%s shows no class", guide)
	return nil
}

// guideParts returns every whole table, action and class a guide shows,
// each in the order of the page.
func guideParts(t *testing.T, guide string) (tables, actions, classes [][]byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "docs", "manual", "guides", guide))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(data, -1) {
		switch kindOf(m[1]) {
		case "table":
			tables = append(tables, m[1])
		case "action":
			actions = append(actions, m[1])
		case "class":
			classes = append(classes, m[1])
		}
	}
	return tables, actions, classes
}

// guideDB opens a database with the class of the guide defined.
func guideDB(t *testing.T, guide string, o fado.Options) *fado.DB {
	t.Helper()
	o.Outbox.Manual = true
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.DefineClass(context.Background(), guideClass(t, guide), setup); err != nil {
		t.Fatalf("the class of %s is refused: %v", guide, err)
	}
	return db
}

// answer says what the engine answered as a guide's table writes it.
func answer(err error) string {
	if err == nil {
		return "allowed"
	}
	var refusal *fado.Error
	if !errors.As(err, &refusal) {
		return err.Error()
	}
	switch {
	case refusal.Variable != "":
		return refusal.Code + ", variable " + refusal.Variable
	case refusal.Path != "":
		return refusal.Code + " at " + refusal.Path
	}
	return refusal.Code
}

// want fails the test when a step of a guide's table is answered
// otherwise than the guide says.
func want(t *testing.T, step string, err error, answered string) {
	t.Helper()
	if got := answer(err); got != answered {
		t.Errorf("%s: answered %q, and the guide says %q", step, got, answered)
	}
}

func person(id string, groups ...string) fado.Option {
	return fado.As(fado.Principal{ID: id, Kind: fado.ActorPerson, Groups: groups})
}

func TestGuide_EditorsPerState(t *testing.T) {
	db := guideDB(t, "editors-per-state.md", fado.Options{})
	ctx := context.Background()
	ann, rita := person("ann"), person("rita", "reviewers")
	memo, err := db.Create(ctx, "Memo", fado.Values{"subject": "Budget", "body": "first"}, ann)
	if err != nil {
		t.Fatal(err)
	}
	set := func(as fado.Option, values fado.Values) error {
		_, err := db.Set(ctx, "Memo", memo.ID, values, as)
		return err
	}
	move := func(as fado.Option, name string) error {
		_, err := db.Transition(ctx, "Memo", memo.ID, name, nil, as)
		return err
	}
	want(t, "writing: Rita changes the body", set(rita, fado.Values{"body": "hers"}), "forbidden, variable body")
	want(t, "writing: Ann changes the body", set(ann, fado.Values{"body": "second"}), "allowed")
	want(t, "writing: Rita sends it", move(rita, "send"), "forbidden at /access/transition/send")
	want(t, "writing: Ann sends it", move(ann, "send"), "allowed")
	want(t, "review: Ann changes the body", set(ann, fado.Values{"body": "third"}), "forbidden, variable body")
	want(t, "review: Rita changes the body", set(rita, fado.Values{"body": "hers"}), "forbidden, variable body")
	want(t, "review: Rita writes a remark", set(rita, fado.Values{"remarks": "shorter, please"}), "allowed")
	want(t, "review: Rita returns it", move(rita, "return"), "allowed")
	want(t, "writing again: Ann changes the body", set(ann, fado.Values{"body": "shorter"}), "allowed")
}

func TestGuide_ReadOnlyAfterApproval(t *testing.T) {
	db := guideDB(t, "read-only-after-approval.md", fado.Options{})
	ctx := context.Background()
	sam, mia := person("sam", "sales"), person("mia", "managers")
	offer, err := db.Create(ctx, "Offer", fado.Values{"customer": "ACME", "price": "900.00 EUR"}, sam)
	if err != nil {
		t.Fatal(err)
	}
	set := func(as fado.Option, values fado.Values) error {
		_, err := db.Set(ctx, "Offer", offer.ID, values, as)
		return err
	}
	move := func(as fado.Option, name string) error {
		_, err := db.Transition(ctx, "Offer", offer.ID, name, nil, as)
		return err
	}
	want(t, "draft: Sam changes the price", set(sam, fado.Values{"price": "950.00 EUR"}), "allowed")
	want(t, "draft: Mia approves", move(mia, "approve"), "allowed")
	want(t, "approved: Sam changes the price", set(sam, fado.Values{"price": "100.00 EUR"}), "forbidden, variable price")
	want(t, "approved: Sam changes the comment", set(sam, fado.Values{"comment": "call first"}), "allowed")
	want(t, "approved: Mia reopens it", move(mia, "reopen"), "allowed")
	want(t, "draft again: Sam changes the price", set(sam, fado.Values{"price": "940.00 EUR"}), "allowed")
	want(t, "Mia approves again", move(mia, "approve"), "allowed")
	want(t, "Sam sends it", move(sam, "send"), "allowed")
	want(t, "sent: Sam changes the comment", set(sam, fado.Values{"comment": "too late"}), "terminal")
}

func TestGuide_FourEyes(t *testing.T) {
	db := guideDB(t, "four-eyes.md", fado.Options{})
	ctx := context.Background()
	fay, fred := person("fay", "finance"), person("fred", "finance")
	pay, err := db.Create(ctx, "Payment", fado.Values{"payee": "ACME", "amount": "500.00 EUR"}, fay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Payment", pay.ID, "file", nil, fay); err != nil {
		t.Fatal(err)
	}
	_, err = db.Transition(ctx, "Payment", pay.ID, "approve", nil, fay)
	want(t, "Fay approves her own payment", err, "forbidden at /access/transition/approve")
	// What she may do, as the listing says it.
	open, err := db.Transitions(ctx, "Payment", pay.ID, fay)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]string{}
	for _, tr := range open {
		listed[tr.Name] = "allowed"
		if !tr.Enabled {
			listed[tr.Name] = "not allowed, " + tr.Code
		}
	}
	if listed["approve"] != "not allowed, forbidden" || listed["reject"] != "allowed" {
		t.Errorf("what Fay may do: %v", listed)
	}
	_, err = db.Transition(ctx, "Payment", pay.ID, "approve", nil, fred)
	want(t, "Fred approves it", err, "allowed")
	events, err := db.Events(ctx, "Payment", pay.ID, fred)
	if err != nil {
		t.Fatal(err)
	}
	var who []string
	for _, e := range events {
		who = append(who, e.Kind+" "+e.Name+" by "+e.ActorID)
	}
	if got := strings.Join(who, "; "); got != "created  by fay; transition file by fay; transition approve by fred" {
		t.Errorf("the log: %s", got)
	}
	// The variations of the guide are conditions the engine accepts.
	class := string(guideClass(t, "four-eyes.md"))
	for _, condition := range []string{
		`LASTEDITOR(\"draft\") <> USER()`,
		`LASTENTEREDBY(\"filed\") <> USER()`,
		`USERKIND() = \"person\" AND CREATEDBY() <> USER()`,
		`CREATEDBY() <> USER() AND (amount <= 10000 OR INGROUP(\"managers\"))`,
	} {
		variant := strings.Replace(class, `INGROUP(\"finance\") AND CREATEDBY() <> USER()`, condition, 1)
		if variant == class {
			t.Fatal("the condition of approve is not where the test looks for it")
		}
		if diags := db.ValidateClass([]byte(strings.Replace(variant, `"name": "Payment"`, `"name": "Payment2"`, 1))); len(withoutDescriptions(diags)) > 0 {
			t.Errorf("the variation %s is refused: %v", condition, diags)
		}
	}
}

func TestGuide_RemindersAndDeadlines(t *testing.T) {
	start := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	var days atomic.Int64
	now := func() time.Time { return start.AddDate(0, 0, int(days.Load())) }
	db := guideDB(t, "reminders-and-deadlines.md", fado.Options{Now: now})
	ctx := context.Background()
	ann := person("ann")
	req, err := db.Create(ctx, "Request", fado.Values{"subject": "Budget for 2027", "deadline": "2026-12-20"}, ann)
	if err != nil {
		t.Fatal(err)
	}
	on := func(day int, state string, reminders float64) {
		t.Helper()
		days.Store(int64(day - 1))
		if _, err := db.Drain(ctx, setup); err != nil {
			t.Fatal(err)
		}
		doc, err := db.Get(ctx, "Request", req.ID, ann)
		if err != nil {
			t.Fatal(err)
		}
		if doc.State != state || doc.Values["reminders"] != reminders {
			t.Errorf("on %d December: %s with %v reminders, and the guide says %s with %v", day, doc.State, doc.Values["reminders"], state, reminders)
		}
	}
	on(1, "waiting", 0)
	on(7, "waiting", 0)
	on(8, "waiting", 1)
	on(15, "waiting", 2)
	on(19, "waiting", 2)
	on(20, "expired", 2)
	// Nobody takes a timed transition by hand here.
	other, err := db.Create(ctx, "Request", fado.Values{"subject": "Another", "deadline": "2027-01-31"}, ann)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Transition(ctx, "Request", other.ID, "expire", nil, ann)
	want(t, "Ann expires a request by hand", err, "forbidden at /access/transition/expire")
	// The log says the timer made the moves, as Ann.
	events, err := db.Events(ctx, "Request", req.ID, ann)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Name != "expire" || last.Origin != "timer" || last.ActorID != "ann" {
		t.Errorf("the event of the expiry: %+v", last)
	}
	// The variation: a deadline counted from the first entry.
	class := string(guideClass(t, "reminders-and-deadlines.md"))
	variant := strings.Replace(class, `"at": "deadline"`, `"at": "ADDDAYS(FIRSTENTERED(\"waiting\"), 30)"`, 1)
	if diags := db.ValidateClass([]byte(strings.Replace(variant, `"name": "Request"`, `"name": "Request2"`, 1))); variant == class || len(withoutDescriptions(diags)) > 0 {
		t.Errorf("the variation with FIRSTENTERED is refused: %v", diags)
	}
}

func TestGuide_ActingForSomebody(t *testing.T) {
	db := guideDB(t, "acting-for-somebody.md", fado.Options{})
	ctx := context.Background()
	ann, bob, carol := person("ann"), person("bob"), person("carol")
	bobForAnn := fado.As(fado.Principal{ID: "bob", Kind: fado.ActorPerson, For: "ann"})
	hers, err := db.Create(ctx, "Expense", fado.Values{"approver": "ann", "amount": "300.00 PLN"}, carol)
	if err != nil {
		t.Fatal(err)
	}
	his, err := db.Create(ctx, "Expense", fado.Values{"approver": "ann", "amount": "900.00 PLN"}, bob)
	if err != nil {
		t.Fatal(err)
	}
	approve := func(id string) error {
		_, err := db.Transition(ctx, "Expense", id, "approve", nil, bobForAnn)
		return err
	}
	want(t, "Bob, for Ann, approves Carol's expense", approve(hers.ID), "forbidden at /principal/for")
	sub, err := db.Create(ctx, fado.SubstitutionClass, fado.Values{"by": "bob", "until": "2026-12-15 00:00:00"}, ann)
	if err != nil || sub.State != fado.SubstitutionActive {
		t.Fatalf("Ann creates a substitution: %v %+v", err, sub)
	}
	want(t, "Bob, for Ann, approves Carol's expense", approve(hers.ID), "allowed")
	want(t, "Bob, for Ann, approves his own expense", approve(his.ID), "forbidden at /access/transition/approve")
	sub, err = db.Transition(ctx, fado.SubstitutionClass, sub.ID, fado.SubstitutionWithdraw, nil, ann)
	if err != nil || sub.State != fado.SubstitutionWithdrawn {
		t.Fatalf("Ann withdraws it: %v", err)
	}
	_, err = db.Get(ctx, "Expense", hers.ID, bobForAnn)
	want(t, "Bob, for Ann, reads Carol's expense", err, "forbidden at /principal/for")
	events, err := db.Events(ctx, "Expense", hers.ID, carol)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Name != "approve" || last.ActorID != "bob" || last.ActorFor != "ann" {
		t.Errorf("the log of Carol's expense: %+v", last)
	}
	// The assistant: an agent, for Ann, on expenses only and with no end.
	if _, err := db.Create(ctx, fado.SubstitutionClass, fado.Values{"by": "agent-7", "classes": []string{"Expense"}}, ann); err != nil {
		t.Fatal(err)
	}
	agent := fado.As(fado.Principal{ID: "agent-7", Kind: fado.ActorAgent, For: "ann"})
	filed, err := db.Create(ctx, "Expense", fado.Values{"approver": "mia", "amount": "40.00 PLN"}, agent)
	if err != nil || filed.Values["owner"] != "ann" || filed.CreatedBy != "agent-7" {
		t.Fatalf("the assistant files for Ann: %v %+v", err, filed)
	}
	made, err := db.Events(ctx, "Expense", filed.ID, ann)
	if err != nil || len(made) != 1 || made[0].ActorKind != fado.ActorAgent || made[0].ActorFor != "ann" {
		t.Errorf("the log of what the assistant filed: %v %+v", err, made)
	}
}

func TestGuide_LookAValueUp(t *testing.T) {
	tables, _, classes := guideParts(t, "look-a-value-up.md")
	if len(tables) != 1 || len(classes) != 1 {
		t.Fatalf("the guide shows %d tables and %d classes", len(tables), len(classes))
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineTable(ctx, tables[0], setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, classes[0], setup); err != nil {
		t.Fatal(err)
	}
	ann, tess := person("ann"), person("tess", "treasury")
	if _, err := db.ImportRows(ctx, "Rates", []fado.Values{{"code": "EUR", "rate": "4.3000"}, {"code": "USD", "rate": "3.9000"}}, tess); err != nil {
		t.Fatal(err)
	}
	_, err = db.ImportRows(ctx, "Rates", []fado.Values{{"code": "EUR", "rate": "9.0000"}}, ann)
	want(t, "Ann writes a rate into the table", err, "forbidden at /access/write")
	quote, err := db.Create(ctx, "Quote", fado.Values{"currency": "EUR", "amount": "100.00"}, ann)
	if err != nil {
		t.Fatal(err)
	}
	is := func(step, rate, pln string) {
		t.Helper()
		if quote.Values["rate"] != rate || quote.Values["in_pln"] != pln {
			t.Errorf("%s: rate %v and in_pln %v, and the guide says %s and %s", step, quote.Values["rate"], quote.Values["in_pln"], rate, pln)
		}
	}
	is("Ann creates a quote of 100.00 in EUR", "4.3000", "430.00")
	if _, err := db.ImportRows(ctx, "Rates", []fado.Values{{"code": "EUR", "rate": "4.5000"}}, tess); err != nil {
		t.Fatal(err)
	}
	if rows, err := db.TableRows(ctx, "Rates", ann); err != nil || len(rows) != 2 {
		t.Fatalf("a row written with a key the table has replaces that row: %v %v", rows, err)
	}
	if quote, err = db.Get(ctx, "Quote", quote.ID, ann); err != nil {
		t.Fatal(err)
	}
	is("Ann reads her quote", "4.3000", "430.00")
	if quote, err = db.Set(ctx, "Quote", quote.ID, fado.Values{"amount": "200.00"}, ann); err != nil {
		t.Fatal(err)
	}
	is("Ann changes the amount", "4.3000", "860.00")
	if quote, err = db.Set(ctx, "Quote", quote.ID, fado.Values{"currency": "USD"}, ann); err != nil {
		t.Fatal(err)
	}
	is("Ann changes the currency", "3.9000", "780.00")
	// A currency the table does not have: blank, and zero in arithmetic.
	other, err := db.Create(ctx, "Quote", fado.Values{"currency": "GBP", "amount": "10.00"}, ann)
	if err != nil || other.Values["rate"] != nil || other.Values["in_pln"] != "0.00" {
		t.Errorf("a lookup that finds nothing: %v %v", err, other.Values)
	}
}

func TestGuide_MoveManyDocuments(t *testing.T) {
	db := guideDB(t, "move-many-documents.md", fado.Options{})
	ctx := context.Background()
	acc := person("anna", "accounting")
	ids := []string{"INV-416", "INV-417", "INV-418"}
	for _, id := range ids {
		values := fado.Values{"cost_centre": "sales"}
		if id == "INV-417" {
			values = nil
		}
		if _, err := db.Create(ctx, "Invoice", values, acc, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	said := func(out []fado.MoveOutcome) string {
		var parts []string
		for _, m := range out {
			if m.Moved() {
				parts = append(parts, m.ID+" "+m.Document.State)
			} else {
				parts = append(parts, m.ID+" refused "+m.Refusal.Code)
			}
		}
		return strings.Join(parts, "; ")
	}
	const outcome = "INV-416 booked; INV-417 refused guard_false; INV-418 booked"
	would, err := db.PreviewMany(ctx, "Invoice", "book", ids, nil, acc)
	if err != nil || said(would) != outcome {
		t.Errorf("the preview: %v %s", err, said(would))
	}
	if doc, _ := db.Get(ctx, "Invoice", "INV-416", acc); doc.State != "approved" {
		t.Errorf("the preview wrote: %s", doc.State)
	}
	moved, err := db.TransitionMany(ctx, "Invoice", "book", ids, nil, acc, fado.Key("booking-2026-11"))
	if err != nil || said(moved) != outcome {
		t.Errorf("the call: %v %s", err, said(moved))
	}
	if doc, _ := db.Get(ctx, "Invoice", "INV-417", acc); doc.State != "approved" {
		t.Errorf("the one refused is %s", doc.State)
	}
	again, err := db.TransitionMany(ctx, "Invoice", "book", ids, nil, acc, fado.Key("booking-2026-11"))
	if err != nil || said(again) != outcome {
		t.Errorf("the same call sent again: %v %s", err, said(again))
	}
	if events, _ := db.Events(ctx, "Invoice", "INV-416", acc); len(events) != 2 {
		t.Errorf("INV-416 has %d events after the call was sent twice, want its creation and one move", len(events))
	}
	if _, err := db.Set(ctx, "Invoice", "INV-417", fado.Values{"cost_centre": "sales"}, acc); err != nil {
		t.Fatal(err)
	}
	rest, err := db.TransitionMany(ctx, "Invoice", "book", []string{"INV-417"}, nil, acc)
	if err != nil || said(rest) != "INV-417 booked" {
		t.Errorf("a new call with only that id: %v %s", err, said(rest))
	}
}

func TestGuide_SeeWhereDocumentsWait(t *testing.T) {
	const guide = "see-where-documents-wait.md"
	monday := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	var hours atomic.Int64
	db := guideDB(t, guide, fado.Options{Now: func() time.Time { return monday.Add(time.Duration(hours.Load()) * time.Hour) }})
	ctx := context.Background()
	bob, dan, ann, eve := person("bob", "sales"), person("dan", "sales"), person("ann", "finance"), person("eve")
	must := func(_ *fado.Document, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// What happened, as the guide's table tells it.
	for _, id := range []string{"B-1", "B-2", "B-3"} {
		must(db.Create(ctx, "Invoice", fado.Values{"total": "100.00"}, bob, fado.WithID(id)))
	}
	must(db.Create(ctx, "Invoice", fado.Values{"total": "0.00"}, dan, fado.WithID("D-1")))
	hours.Store(1)
	must(db.Transition(ctx, "Invoice", "B-1", "submit", nil, bob))
	must(db.Transition(ctx, "Invoice", "B-2", "submit", nil, bob))
	must(db.Transition(ctx, "Invoice", "D-1", "submit", nil, dan))
	if doc, _ := db.Get(ctx, "Invoice", "D-1", dan); doc == nil || doc.State != "cancelled" {
		t.Errorf("an invoice of nothing that enters review is %v", doc)
	}
	hours.Store(4)
	must(db.Transition(ctx, "Invoice", "B-1", "approve", nil, ann))
	hours.Store(28)
	must(db.Transition(ctx, "Invoice", "B-3", "cancel", nil, bob))
	hours.Store(29)

	text, err := os.ReadFile(filepath.Join("..", "docs", "manual", "guides", guide))
	if err != nil {
		t.Fatal(err)
	}
	page := strings.ReplaceAll(string(text), "\r\n", "\n")
	block := func(mark string) string {
		t.Helper()
		_, after, found := strings.Cut(page, "<!-- "+mark+" -->\n")
		if !found {
			t.Fatalf("the guide has nothing marked %q", mark)
		}
		part, _, _ := strings.Cut(after, "\n\n")
		return part
	}
	rows := func(mark string) [][]string {
		t.Helper()
		var out [][]string
		for i, line := range strings.Split(block(mark), "\n") {
			if i < 2 {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			for j := range cells {
				cells[j] = strings.Trim(strings.TrimSpace(cells[j]), "`")
			}
			out = append(out, cells)
		}
		return out
	}
	// asked sends the command as a caller and answers the flow.
	asked := func(as fado.Principal, object string) (*fado.Flow, string) {
		t.Helper()
		answer, err := db.Exec(ctx, as, "flow", []byte(object))
		if err != nil {
			t.Fatalf("flow %s: %v", object, err)
		}
		var got struct{ Flow *fado.Flow }
		if err := json.Unmarshal(answer, &got); err != nil {
			t.Fatal(err)
		}
		var shown bytes.Buffer
		if err := json.Indent(&shown, answer, "", "  "); err != nil {
			t.Fatal(err)
		}
		return got.Flow, shown.String()
	}
	who := map[string]fado.Principal{
		"Ann": {ID: "ann", Kind: fado.ActorPerson, Groups: []string{"finance"}},
		"Bob": {ID: "bob", Kind: fado.ActorPerson, Groups: []string{"sales"}},
		"Dan": {ID: "dan", Kind: fado.ActorPerson, Groups: []string{"sales"}},
		"Eve": {ID: "eve", Kind: fado.ActorPerson},
	}
	_ = eve

	// The answer the guide shows is the answer Ann gets.
	_, shown := asked(who["Ann"], `{"class": "Invoice"}`)
	if want := strings.TrimSuffix(strings.TrimPrefix(block("the flow Ann sees"), "```jsonc\n"), "\n```"); shown != want {
		t.Errorf("the flow Ann sees is\n%s\nand the guide shows\n%s", shown, want)
	}
	// The Go call answers the same.
	direct, err := db.Flow(ctx, "Invoice", fado.FlowPeriod{}, ann)
	if viaCommand, _ := asked(who["Ann"], `{"class": "Invoice"}`); err != nil || !reflect.DeepEqual(direct, viaCommand) {
		t.Errorf("the Go call answers %+v (%v), and the command %+v", direct, err, viaCommand)
	}
	// What the guide reads from it.
	if draft, review := direct.States[0], direct.States[1]; draft.Stays != 4 || draft.AverageSeconds != 7*3600+45*60 || draft.LongestSeconds != 28*3600 ||
		review.Documents != 1 || review.OldestSince != monday.Add(time.Hour).Unix() || review.Stays != 2 || review.AverageSeconds != 90*60 {
		t.Errorf("the guide reads draft and review otherwise than the flow says: %+v %+v", draft, review)
	}
	// Every member of the flow is in the guide's table of what a number
	// says, and the table names no other.
	named := map[string]bool{}
	for _, row := range rows("what a number says") {
		for _, member := range strings.Split(row[0], ", ") {
			named[strings.Trim(member, "`")] = true
		}
	}
	has := map[string]bool{}
	for _, of := range []any{fado.Flow{}, fado.FlowState{}, fado.FlowTransition{}} {
		kind := reflect.TypeOf(of)
		for i := 0; i < kind.NumField(); i++ {
			member, _, _ := strings.Cut(kind.Field(i).Tag.Get("json"), ",")
			switch member {
			case "class", "class_version", "since", "until", "states", "transitions", "name", "from", "to":
				continue // what the flow is of, and the names
			}
			if !named[member] {
				t.Errorf("the guide does not say what %s says", member)
			}
			has[member] = true
		}
	}
	for member := range named {
		if !has[member] {
			t.Errorf("the guide explains %s, which the flow has not", member)
		}
	}

	number := func(text string) int64 {
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			t.Fatalf("the guide has %q where a number goes", text)
		}
		return n
	}
	in := func(f *fado.Flow, state string) int64 {
		for _, st := range f.States {
			if st.Name == state {
				return st.Documents
			}
		}
		t.Fatalf("the flow has no state %s", state)
		return 0
	}
	along := func(f *fado.Flow, name, from string) int64 {
		for _, tr := range f.Transitions {
			if tr.Name == name && tr.From == from {
				return tr.Moves
			}
		}
		t.Fatalf("the flow has no transition %s from %s", name, from)
		return 0
	}
	// Each caller sees their own.
	for _, row := range rows("who sees what") {
		p, known := who[row[0]]
		if !known {
			t.Fatalf("the guide has a caller %q", row[0])
		}
		f, _ := asked(p, `{"class": "Invoice"}`)
		if f.Created != number(row[1]) || in(f, "review") != number(row[2]) || in(f, "cancelled") != number(row[3]) || along(f, "submit", "draft") != number(row[4]) {
			t.Errorf("%s sees created %d, %d in review, %d cancelled, %d moves along submit, and the guide says %v",
				row[0], f.Created, in(f, "review"), in(f, "cancelled"), along(f, "submit", "draft"), row[1:])
		}
	}
	if len(rows("who sees what")) != len(who) {
		t.Error("the guide's table of who sees what lacks a caller")
	}
	// A period.
	for _, row := range rows("a period") {
		f, _ := asked(who["Ann"], row[0])
		if f.Created != number(row[1]) || along(f, "submit", "draft") != number(row[2]) || along(f, "cancel", "draft") != number(row[3]) || in(f, "review") != number(row[4]) {
			t.Errorf("%s: created %d, %d along submit, %d along cancel, %d in review, and the guide says %v",
				row[0], f.Created, along(f, "submit", "draft"), along(f, "cancel", "draft"), in(f, "review"), row[1:])
		}
	}
	// A deleted document is in no number.
	if err := db.Delete(ctx, "Invoice", "B-2", bob); err != nil {
		t.Fatal(err)
	}
	if f, _ := asked(who["Ann"], `{"class": "Invoice"}`); f.Created != 3 || in(f, "review") != 0 || along(f, "submit", "draft") != 2 {
		t.Errorf("after a delete Ann sees created %d, %d in review, %d along submit", f.Created, in(f, "review"), along(f, "submit", "draft"))
	}
}

func TestGuide_ChangeAClass(t *testing.T) {
	_, _, classes := guideParts(t, "change-a-class.md")
	if len(classes) != 2 {
		t.Fatalf("the guide shows %d classes, want the class before and after", len(classes))
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	ann := person("ann")
	if cls, err := db.DefineClass(ctx, classes[0], setup); err != nil || cls.Version != 1 {
		t.Fatalf("the class in use: %v", err)
	}
	make := func(subject string) string {
		t.Helper()
		doc, err := db.Create(ctx, "Ticket", fado.Values{"subject": subject}, ann)
		if err != nil {
			t.Fatal(err)
		}
		return doc.ID
	}
	a, b, c := make("A"), make("B"), make("C")
	if _, err := db.Transition(ctx, "Ticket", c, "close", nil, ann); err != nil {
		t.Fatal(err)
	}
	if cls, err := db.DefineClass(ctx, classes[1], setup); err != nil || cls.Version != 2 {
		t.Fatalf("the class with a priority: %v", err)
	}
	is := func(step, id string, version int, has bool) *fado.Document {
		t.Helper()
		doc, err := db.Get(ctx, "Ticket", id, ann)
		if err != nil {
			t.Fatal(err)
		}
		_, got := doc.Values["priority"]
		if doc.ClassVersion != version || got != has {
			t.Errorf("%s: of version %d, with a priority %v; the guide says version %d, %v", step, doc.ClassVersion, got, version, has)
		}
		return doc
	}
	is("A, untouched", a, 1, false)
	if _, err := db.Set(ctx, "Ticket", a, fado.Values{"subject": "A, reworded"}, ann); err != nil {
		t.Fatal(err)
	}
	if doc := is("A, after a change", a, 2, true); doc.Values["priority"] != nil || doc.Values["urgent"] != false {
		t.Errorf("A after a change: %v", doc.Values)
	}
	if n, err := db.Migrate(ctx, "Ticket", setup); err != nil || n != 1 {
		t.Errorf("migrating moved %d tickets: %v; B alone was left to move", n, err)
	}
	is("B, after the migration", b, 2, true)
	is("C, closed", c, 1, false)
	fresh, err := db.Create(ctx, "Ticket", fado.Values{"subject": "D"}, ann)
	if err != nil || fresh.ClassVersion != 2 || fresh.Values["priority"] != "low" {
		t.Errorf("a new ticket: %v %+v", err, fresh)
	}
	if defs, err := db.Definitions(ctx, "class", "Ticket"); err != nil || len(defs) != 2 {
		t.Errorf("the versions recorded: %v %d", err, len(defs))
	}
	// Going back to the first definition would remove a variable that
	// tickets now hold a value for.
	_, err = db.DefineClass(ctx, classes[0], setup)
	if err == nil || !strings.Contains(err.Error(), "not_allowed") || !strings.Contains(err.Error(), "hold a value for urgent") {
		t.Errorf("removing a variable with values: %v", err)
	}
}

func TestGuide_ActOutside(t *testing.T) {
	_, actions, classes := guideParts(t, "act-outside.md")
	if len(actions) != 1 || len(classes) != 1 {
		t.Fatalf("the guide shows %d actions and %d classes", len(actions), len(classes))
	}
	var calls []string
	notify := func(_ context.Context, call *fado.ActionCall) (fado.Values, error) {
		calls = append(calls, call.Args["channel"].(string)+": "+call.Args["text"].(string))
		return nil, nil
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), fado.Options{
		Outbox:  fado.OutboxOptions{Manual: true},
		Actions: map[string]fado.ActionFunc{"team.notify": notify},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineAction(ctx, actions[0], setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, classes[0], setup); err != nil {
		t.Fatal(err)
	}
	ann := person("ann")
	inv, err := db.Create(ctx, "Invoice", fado.Values{"number": "FV-17"}, ann)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Invoice", inv.ID, "approve", nil, ann); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Errorf("the function was called before the worker ran: %v", calls)
	}
	if _, err := db.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "#finance: Approved: FV-17" {
		t.Errorf("after the worker ran: %v", calls)
	}
	if _, err := db.Drain(ctx, setup); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Errorf("the worker ran again and the function was called %d times", len(calls))
	}
	events, err := db.Events(ctx, "Invoice", inv.ID, ann)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, ", "); got != "created, transition, action_run" {
		t.Errorf("the log: %s", got)
	}
}

// The guide on the version a command saw (ADR-0078, item 2): the table
// of the guide played step by step, the command and the refusal it
// shows sent and compared, and the batch of its last section.
func TestGuide_ActOnWhatYouRead(t *testing.T) {
	const guide = "act-on-what-you-read.md"
	tables, _, classes := guideParts(t, guide)
	if len(tables) != 1 || len(classes) != 1 {
		t.Fatalf("the guide shows %d tables and %d classes; want one of each", len(tables), len(classes))
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.DefineTable(ctx, tables[0], setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ImportRows(ctx, "Limits", []fado.Values{{"grade": "B", "limit": "10000.00"}}, person("tess", "treasury")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, classes[0], setup); err != nil {
		t.Fatal(err)
	}
	ola, tomek := person("ola"), person("tomek", "managers")
	file := func(id string) {
		t.Helper()
		if _, err := db.Create(ctx, "Purchase", fado.Values{"number": id, "amount": "9000.00", "grade": "B"}, ola, fado.WithID(id)); err != nil {
			t.Fatal(err)
		}
		if d, err := db.Transition(ctx, "Purchase", id, "file", nil, ola); err != nil || d.Version != 2 {
			t.Fatalf("Ola files %s: %v, %v", id, d, err)
		}
	}
	file("P-7")
	read := func(what string, version int64, amount string) {
		t.Helper()
		d, err := db.Get(ctx, "Purchase", "P-7", tomek)
		if err != nil || d.Version != version || d.Values["amount"] != amount || d.Values["limit"] != "10000.00" {
			t.Fatalf("%s: %v, %v; the guide says version %d, %s against 10000.00", what, d, err, version, amount)
		}
	}
	read("Tomek reads P-7", 2, "9000.00")
	if d, err := db.Set(ctx, "Purchase", "P-7", fado.Values{"amount": "9900.00"}, ola); err != nil || d.Version != 3 {
		t.Fatalf("Ola corrects the amount: %v, %v", d, err)
	}

	// The command the guide shows, and the answer it shows.
	data, err := os.ReadFile(filepath.Join("..", "docs", "manual", "guides", guide))
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```jsonc?\r?\n(.*?)\r?\n```").FindAllSubmatch(data, -1)
	var command, refusal, batch []byte
	for i, m := range blocks {
		switch {
		case bytes.Contains(m[1], []byte(`"version": 2`)) && i+1 < len(blocks):
			command, refusal = m[1], blocks[i+1][1]
		case bytes.Contains(m[1], []byte(`"versions"`)):
			batch = m[1]
		}
	}
	if command == nil || batch == nil {
		t.Fatal("the guide shows no command with a version, or no batch with versions")
	}
	tomekIs := fado.Principal{ID: "tomek", Kind: fado.ActorPerson, Groups: []string{"managers"}}
	got, err := db.Exec(ctx, tomekIs, "transition", command)
	want(t, "Tomek approves, and says he read version 2", err, "stale_version")
	var shown, answered any
	if json.Unmarshal(refusal, &shown) != nil || json.Unmarshal(got, &answered) != nil || !reflect.DeepEqual(shown, answered) {
		t.Errorf("the refusal is\n%s\nand the guide shows\n%s", got, refusal)
	}
	read("Tomek reads P-7 again", 3, "9900.00")
	approve := func() (*fado.Document, error) {
		return db.Transition(ctx, "Purchase", "P-7", "approve", nil, tomek, fado.Saw(3), fado.Key("approve-P-7"))
	}
	if d, err := approve(); err != nil || d.Version != 4 || d.State != "approved" {
		t.Fatalf("Tomek approves, and says he read version 3: %v, %v", d, err)
	}
	if d, err := approve(); err != nil || d.Version != 4 {
		t.Fatalf("the same approval sent again with its key: %v, %v", d, err)
	}
	events, err := db.Events(ctx, "Purchase", "P-7", tomek)
	if err != nil {
		t.Fatal(err)
	}
	var log []string
	for _, e := range events {
		log = append(log, strings.TrimSpace(e.Kind+" "+e.Name)+" saw "+strconv.FormatInt(e.SeenVersion, 10))
	}
	if got := strings.Join(log, "; "); got != "created saw 0; transition file saw 0; set saw 0; transition approve saw 3" {
		t.Errorf("the log: %s", got)
	}
	// A caller who may not read the document learns nothing from the
	// version: the guide's class lets everyone read, so the proof of that
	// line is the store's own test.

	// Many documents at once: the batch the guide shows.
	for _, id := range []string{"P-1", "P-2", "P-3"} {
		file(id)
	}
	if _, err := db.Set(ctx, "Purchase", "P-2", fado.Values{"amount": "9100.00"}, ola); err != nil {
		t.Fatal(err)
	}
	said := func(out []byte) string {
		var answer struct {
			Outcomes []struct {
				ID       string `json:"id"`
				Document *struct {
					State string `json:"state"`
				} `json:"document"`
				Refusal *struct {
					Code string `json:"code"`
				} `json:"refusal"`
			} `json:"outcomes"`
		}
		if err := json.Unmarshal(out, &answer); err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, o := range answer.Outcomes {
			if o.Refusal != nil {
				parts = append(parts, o.ID+" refused "+o.Refusal.Code)
			} else {
				parts = append(parts, o.ID+" "+o.Document.State)
			}
		}
		return strings.Join(parts, "; ")
	}
	const outcome = "P-1 approved; P-2 refused stale_version; P-3 approved"
	for _, name := range []string{"preview_many", "transition_many"} {
		out, err := db.Exec(ctx, tomekIs, name, batch)
		if err != nil || said(out) != outcome {
			t.Errorf("%s: %s, %v; the guide says %s", name, said(out), err, outcome)
		}
	}
	// In Go the same batch names 0 for none.
	file("P-4")
	moved, err := db.TransitionMany(ctx, "Purchase", "approve", []string{"P-2", "P-4"}, nil, tomek, fado.SawEach([]int64{3, 0}))
	if err != nil || !moved[0].Moved() || !moved[1].Moved() {
		t.Errorf("the batch in Go: %v, %v", moved, err)
	}
}

// The guide on who may bring a value again (ADR-0078, item 3): the
// table of the guide played, and the refusal it shows compared.
func TestGuide_WhoMayBringAValueAgain(t *testing.T) {
	const guide = "who-may-bring-a-value-again.md"
	tables, _, classes := guideParts(t, guide)
	if len(tables) != 1 || len(classes) != 1 {
		t.Fatalf("the guide shows %d tables and %d classes; want one of each", len(tables), len(classes))
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	tess := person("tess", "treasury")
	limit := func(amount string) {
		t.Helper()
		if _, err := db.ImportRows(ctx, "Limits", []fado.Values{{"grade": "B", "limit": amount}}, tess); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DefineTable(ctx, tables[0], setup); err != nil {
		t.Fatal(err)
	}
	limit("10000.00")
	if _, err := db.DefineClass(ctx, classes[0], setup); err != nil {
		t.Fatal(err)
	}
	ola, tomek := person("ola"), person("tomek", "managers")
	if _, err := db.Create(ctx, "Purchase", fado.Values{"number": "P-7", "amount": "9000.00", "grade": "B"}, ola, fado.WithID("P-7")); err != nil {
		t.Fatal(err)
	}
	// Ola is no manager, and the move brings the limit all the same.
	if d, err := db.Transition(ctx, "Purchase", "P-7", "file", nil, ola); err != nil || d.Values["limit"] != "10000.00" {
		t.Fatalf("Ola files the purchase: %v, %v", d, err)
	}
	limit("5000.00")

	piotrIs := fado.Principal{ID: "piotr", Kind: fado.ActorPerson}
	got, err := db.Exec(ctx, piotrIs, "refresh", []byte(`{"class": "Purchase", "id": "P-7", "variables": ["limit"]}`))
	want(t, "Piotr refreshes limit", err, "forbidden, variable limit")
	data, err2 := os.ReadFile(filepath.Join("..", "docs", "manual", "guides", guide))
	if err2 != nil {
		t.Fatal(err2)
	}
	var refusal []byte
	for _, m := range regexp.MustCompile("(?s)```json\r?\n(.*?)\r?\n```").FindAllSubmatch(data, -1) {
		if bytes.Contains(m[1], []byte(`"error"`)) {
			refusal = m[1]
		}
	}
	var shown, answered any
	if refusal == nil || json.Unmarshal(refusal, &shown) != nil || json.Unmarshal(got, &answered) != nil || !reflect.DeepEqual(shown, answered) {
		t.Errorf("the refusal is\n%s\nand the guide shows\n%s", got, refusal)
	}

	piotr := fado.As(piotrIs)
	d, err := db.Refresh(ctx, "Purchase", "P-7", nil, piotr)
	want(t, "Piotr refreshes the purchase, naming no variable", err, "allowed")
	if err == nil && (d.Values["limit"] != "10000.00" || d.Computed["checked"].Version != d.Version || d.Computed["limit"].Version == d.Version) {
		t.Errorf("after Piotr's refresh the limit is %v, worked out at version %d of %d; the guide says checked is brought and the limit stays", d.Values["limit"], d.Computed["limit"].Version, d.Version)
	}
	d, err = db.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, tomek)
	want(t, "Tomek refreshes limit", err, "allowed")
	if err == nil && d.Values["limit"] != "5000.00" {
		t.Errorf("after Tomek's refresh the limit is %v; the guide says 5000.00", d.Values["limit"])
	}
	if _, err := db.Transition(ctx, "Purchase", "P-7", "return", nil, tomek); err != nil {
		t.Fatal(err)
	}
	_, err = db.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, tomek)
	want(t, "Tomek refreshes limit of a draft", err, "forbidden, variable limit")
}
