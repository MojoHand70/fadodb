package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
		if diags := db.ValidateClass([]byte(strings.Replace(variant, `"name": "Payment"`, `"name": "Payment2"`, 1))); len(diags) > 0 {
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
	if diags := db.ValidateClass([]byte(strings.Replace(variant, `"name": "Request"`, `"name": "Request2"`, 1))); variant == class || len(diags) > 0 {
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
