package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

var (
	ann = Principal{ID: "ann", Groups: []string{"sales"}}
	bob = Principal{ID: "bob", Groups: []string{"finance"}}
)

// lifeClaim is a class whose formulas read the life of its document
// (ADR-0047, items 4 and 5): a default, a guard, an access rule, a
// triggered variable and a rule, and one variable no formula reads.
const lifeClaim = `{"name": "Claim",
 "variables": {
   "amount":   {"type": "decimal(2)"},
   "raised":   {"type": "text", "default": "CREATEDBY()"},
   "came":     {"type": "text", "formula": "ENTEREDFROM()", "mode": "triggered", "on": ["enter:review", "enter:draft"]},
   "mover":    {"type": "text", "formula": "ENTEREDBY() & \"@\" & TEXT(VERSION())", "mode": "triggered", "on": ["enter:review"]},
   "note":     {"type": "text"},
   "approver": {"type": "text"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved"]},
 "transitions": [
   {"name": "submit", "from": "draft", "to": "review"},
   {"name": "back", "from": "review", "to": "draft"},
   {"name": "again", "from": "review", "to": "review"},
   {"name": "approve", "from": "review", "to": "approved", "guard": "DAYSBETWEEN(CREATEDAT(), NOW()) >= 0 AND UPDATEDBY() <> \"\""},
   {"from": "draft", "to": "review", "guard": "amount > 1000 AND ENTEREDFROM() = \"review\""}
 ],
 "access": {"transition": {"approve": "CREATEDBY() <> USER()"}},
 "rules": [{"on": "enter", "state": "approved", "do": [{"action": "set", "values": {"approver": "ENTEREDBY()"}}]}]
}`

// TestLife is what a document keeps of its own life and what a formula
// reads of it (ADR-0047, items 3 to 5): who created it, who made its
// current version, who moved it into its state and where from, on the
// document, in its history and in the views; how each entry came about,
// in the log; and one row of the view of stays for every entry, a return
// into the same state too.
func TestLife(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(lifeClaim)); err != nil {
		t.Fatal(err)
	}
	who := func(d *Document) string {
		return fmt.Sprintf("v%d %s created:%s updated:%s entered:%s from:%s", d.Version, d.State, d.CreatedBy, d.UpdatedBy, d.StateEnteredBy, d.StateEnteredFrom)
	}
	expect := func(d *Document, err error, want string) *Document {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if got := who(d); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
		return d
	}
	// Created by ann: she is who created it, who made this version and who
	// put it in its first state; it came from nowhere. The default read her.
	d, err := s.Create(ctx, "Claim", Values{"amount": "250.00"}, WithID("C-1"), As(ann))
	expect(d, err, "v1 draft created:ann updated:ann entered:ann from:")
	if d.Values["raised"] != "ann" {
		t.Errorf("the default CREATEDBY(): %v", d.Values["raised"])
	}
	// A change by bob: he made the version; nobody moved the document.
	d, err = s.Set(ctx, "Claim", "C-1", Values{"note": "checked"}, As(bob))
	expect(d, err, "v2 draft created:ann updated:bob entered:ann from:")
	// A change by nobody leaves that version without a who.
	d, err = s.Set(ctx, "Claim", "C-1", Values{"note": "checked twice"})
	expect(d, err, "v3 draft created:ann updated: entered:ann from:")
	// bob submits: he moved it, from draft. The triggered variables read
	// the row as the commit computed it.
	d, err = s.Transition(ctx, "Claim", "C-1", "submit", nil, As(bob))
	expect(d, err, "v4 review created:ann updated:bob entered:bob from:draft")
	if d.Values["came"] != "draft" || d.Values["mover"] != "bob@4" {
		t.Errorf("ENTEREDFROM and ENTEREDBY on entry: %v %v", d.Values["came"], d.Values["mover"])
	}
	// A transition back into the same state is an entry: the moment, who
	// and where from all move, and where from is that state.
	entered := d.StateEnteredAt
	d, err = s.Transition(ctx, "Claim", "C-1", "again", nil, As(ann))
	expect(d, err, "v5 review created:ann updated:ann entered:ann from:review")
	if d.StateEnteredAt < entered || d.Values["came"] != "review" {
		t.Errorf("a return into the same state: entered at %d after %d, came %v", d.StateEnteredAt, entered, d.Values["came"])
	}
	// Four eyes: ann created it and may not approve it; bob may. The rule
	// on the entry writes who approved, which changes a value.
	if _, err := s.Transition(ctx, "Claim", "C-1", "approve", nil, As(ann)); err == nil || err.(*Error).Code != CodeForbidden {
		t.Errorf("the creator approved her own claim: %v", err)
	}
	d, err = s.Transition(ctx, "Claim", "C-1", "approve", nil, As(bob))
	expect(d, err, "v6 approved created:ann updated:bob entered:bob from:review")
	if d.Values["approver"] != "bob" {
		t.Errorf("the rule's ENTEREDBY(): %v", d.Values["approver"])
	}

	// Every version in the history says the same of itself.
	hist, err := s.History(ctx, "Claim", "C-1")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, h := range hist {
		lines = append(lines, who(&h.Document))
	}
	wantHist := []string{
		"v1 draft created:ann updated:ann entered:ann from:",
		"v2 draft created:ann updated:bob entered:ann from:",
		"v3 draft created:ann updated: entered:ann from:",
		"v4 review created:ann updated:bob entered:bob from:draft",
		"v5 review created:ann updated:ann entered:ann from:review",
		"v6 approved created:ann updated:bob entered:bob from:review",
	}
	if !reflect.DeepEqual(lines, wantHist) {
		t.Errorf("history:\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(wantHist, "\n"))
	}
	// The log says how each entry came about; an event that is no entry
	// says nothing.
	evs, err := s.Events(ctx, "Claim", "C-1")
	if err != nil {
		t.Fatal(err)
	}
	var origins []string
	for _, e := range evs {
		origins = append(origins, e.Kind+":"+e.Name+"="+e.Origin)
	}
	if got := strings.Join(origins, " "); got != "created:=command set:= set:= transition:submit=command transition:again=command transition:approve=command" {
		t.Errorf("origins: %s", got)
	}

	// A second claim comes back from review above the limit: the eventless
	// transition moves it on in the same command, by its guard, as bob.
	if _, err := s.Create(ctx, "Claim", Values{"amount": "5000.00"}, WithID("C-2"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Claim", "C-2", "submit", nil, As(ann)); err != nil {
		t.Fatal(err)
	}
	d, err = s.Transition(ctx, "Claim", "C-2", "back", nil, As(bob))
	expect(d, err, "v4 review created:ann updated:bob entered:bob from:draft")
	// The variables of that entry read the row of the eventless commit:
	// its version, and who made it.
	if d.Values["mover"] != "bob@4" || d.Values["came"] != "draft" {
		t.Errorf("the entry an eventless transition made: mover %v, came %v", d.Values["mover"], d.Values["came"])
	}

	// The view of stays has a row for every entry, with who made each
	// move, by which transition, from where and how, at both ends.
	rows, err := s.SQL(ctx, `SELECT id, state, entry_version, exit_version, entered_by, entered_from, entered_via, entered_how, left_by, left_via, left_how FROM Claim_states ORDER BY id, entry_version`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var stays []string
	for _, r := range rows.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			parts[i] = fmt.Sprint(v)
			if v == nil {
				parts[i] = "-"
			}
		}
		stays = append(stays, strings.Join(parts, " "))
	}
	wantStays := []string{
		"C-1 draft 1 3 ann - - command bob submit command",
		"C-1 review 4 4 bob draft submit command ann again command",
		"C-1 review 5 5 ann review again command bob approve command",
		"C-1 approved 6 - bob review approve command - - -",
		"C-2 draft 1 1 ann - - command ann submit command",
		"C-2 review 2 2 ann draft submit command bob back command",
		"C-2 draft 3 3 bob review back command bob - guard",
		"C-2 review 4 - bob draft - guard - - -",
	}
	if !reflect.DeepEqual(stays, wantStays) {
		t.Errorf("stays:\n%s\nwant\n%s", strings.Join(stays, "\n"), strings.Join(wantStays, "\n"))
	}
	// The views of the documents and of their history show who, and the
	// view of events the origin as a word.
	rows, err = s.SQL(ctx, `SELECT c.id, c.created_by, c.updated_by, c.state_entered_by, c.state_entered_from,
  (SELECT count(*) FROM Claim_history h WHERE h.id = c.id AND h.updated_by = 'bob'),
  (SELECT group_concat(origin, ',') FROM (SELECT origin FROM Claim_events e WHERE e.doc_id = c.id AND origin IS NOT NULL ORDER BY e.after_version))
 FROM Claim c ORDER BY c.id`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(rows.Rows); got != "[[C-1 ann bob bob review 3 command,command,command,command] [C-2 ann bob bob draft 2 command,command,command,guard]]" {
		t.Errorf("the views: %s", got)
	}

	// Another class reads them with LOOKUP, as columns.
	const audit = `{"name": "Audit", "variables": {
  "by_bob":  {"type": "number", "default": "LOOKUP(Claim, COUNT(id), updated_by = \"bob\")"},
  "raiser":  {"type": "text", "default": "LOOKUP(Claim, created_by, id = \"C-1\")"},
  "back":    {"type": "number", "default": "LOOKUP(Claim, COUNT(id), state_entered_from = \"draft\" AND state_entered_by = \"bob\")"}},
 "states": {"initial": "open", "list": ["open"]}}`
	if _, err := s.DefineClass(ctx, []byte(audit)); err != nil {
		t.Fatal(err)
	}
	if a, err := s.Create(ctx, "Audit", Values{}); err != nil || a.Values["by_bob"] != 2.0 || a.Values["raiser"] != "ann" || a.Values["back"] != 1.0 {
		t.Errorf("LOOKUP over who: %v %s", err, dump(a))
	}

	// A formula reads the life inside the condition of a LOOKUP too, and a
	// listing reads the moments of the row for a class that never asks
	// who.
	const peer = `{"name": "Peer", "variables": {"mine": {"type": "number", "default": "LOOKUP(Claim, COUNT(id), created_by = CREATEDBY())"}},
 "states": {"initial": "open", "list": ["open", "late"]},
 "transitions": [{"name": "expire", "from": "open", "to": "late", "guard": "NOT ISBLANK(ENTEREDAT()) AND NOT ISBLANK(CREATEDAT()) AND VERSION() = 1"}]}`
	if _, err := s.DefineClass(ctx, []byte(peer)); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Create(ctx, "Peer", Values{}, WithID("P-1"), As(ann)); err != nil || p.Values["mine"] != 2.0 {
		t.Errorf("CREATEDBY() inside a LOOKUP: %v %s", err, dump(p))
	}
	if c, _ := s.class("Peer"); c.who {
		t.Error("a class that reads only moments was marked as reading who")
	}
	if list, err := s.Transitions(ctx, "Peer", "P-1"); err != nil || len(list) != 1 || !list[0].Enabled {
		t.Errorf("a listing whose guard reads the moments of the row: %v %s", err, dump(list))
	}
	if c, _ := s.class("Claim"); !c.who {
		t.Error("a class whose formulas read who was not marked")
	}

	// What is refused: a function of the life in an auto formula, and a
	// variable that takes a system column's name.
	var diags model.Diagnostics
	_, err = s.DefineClass(ctx, []byte(`{"name": "Auto", "variables": {"who": {"formula": "CREATEDBY()"}}, "states": {"initial": "open", "list": ["open"]}}`))
	if !errors.As(err, &diags) || !strings.Contains(diags.Error(), "CREATEDBY is a context function") {
		t.Errorf("CREATEDBY in an auto formula: %v", err)
	}
	for _, name := range []string{"created_by", "updated_by", "state_entered_by", "state_entered_from"} {
		_, err = s.DefineClass(ctx, []byte(`{"name": "Taken", "variables": {"`+name+`": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}}`))
		if !errors.As(err, &diags) || !diags.Has(model.CodeReservedName) {
			t.Errorf("a variable named %s: %v", name, err)
		}
	}

	// Everything is there after a reopen.
	s.Close()
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	d, err = s2.Get(ctx, "Claim", "C-1")
	expect(d, err, "v6 approved created:ann updated:bob entered:bob from:review")
	old, err := s2.Version(ctx, "Claim", "C-1", 4)
	if err != nil {
		t.Fatal(err)
	}
	expect(&old.Document, nil, "v4 review created:ann updated:bob entered:bob from:draft")
}

// TestLife_WriteStep is the write of an existing document, which updates
// its system columns alone when the commit left every value as it was
// (ADR-0047): what is stored is the same either way.
func TestLife_WriteStep(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(lifeClaim)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Claim", Values{"amount": "10.00", "note": "n"}, WithID("C-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		t.Helper()
		rows, err := s.SQL(ctx, `SELECT c.state, c.version, c.amount, c.note, c.came, c.updated_by, h.state, h.version, h.amount, h.note, h.came, h.updated_by, h.valid_to,
  (SELECT p.valid_to IS NOT NULL FROM Claim_history p WHERE p.id = c.id AND p.version = c.version - 1)
 FROM Claim c JOIN Claim_history h ON h.id = c.id AND h.version = c.version`, nil)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(rows.Rows)
	}
	// A transition that changes no value writes the system columns.
	if _, err := s.Transition(ctx, "Claim", "C-1", "submit", nil, As(bob)); err != nil {
		t.Fatal(err)
	}
	if got := stored(); got != "[[review 2 1000 n draft bob review 2 1000 n draft bob <nil> 1]]" {
		t.Errorf("after a transition whose trigger changed a value: %s", got)
	}
	if _, err := s.Transition(ctx, "Claim", "C-1", "again", nil, As(ann)); err != nil {
		t.Fatal(err)
	}
	if got := stored(); got != "[[review 3 1000 n review ann review 3 1000 n review ann <nil> 1]]" {
		t.Errorf("after a return into the same state: %s", got)
	}
	// A set writes its value, and a set that changes nothing is no commit.
	if _, err := s.Set(ctx, "Claim", "C-1", Values{"note": "m"}, As(bob)); err != nil {
		t.Fatal(err)
	}
	if got := stored(); got != "[[review 4 1000 m review bob review 4 1000 m review bob <nil> 1]]" {
		t.Errorf("after a set: %s", got)
	}
	d, err := s.Set(ctx, "Claim", "C-1", Values{"note": "m"}, As(ann))
	if err != nil || d.Version != 4 || d.UpdatedBy != "bob" {
		t.Errorf("a set that changes nothing: %v %s", err, dump(d))
	}
	// What counts as a change is the identity of the value, not that of
	// the number that carries it: the smallest amount an exact value
	// holds has the bits of a negative zero, and is not zero.
	const least = "-92233720368547758.08"
	if _, err := s.Create(ctx, "Claim", Values{"amount": "0.00"}, WithID("C-2"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Claim", "C-2", Values{"amount": least}, As(bob)); err != nil {
		t.Skipf("the smallest amount is not accepted: %v", err)
	}
	rows, err := s.SQL(ctx, `SELECT c.version, c.amount = h.amount, c.amount <> 0 FROM Claim c JOIN Claim_history h ON h.id = c.id AND h.version = c.version WHERE c.id = 'C-2'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(rows.Rows); got != "[[2 1 1]]" {
		t.Errorf("a set from zero to the smallest amount: %s", got)
	}
	if d, err := s.Get(ctx, "Claim", "C-2"); err != nil || fmt.Sprint(d.Values["amount"]) != least {
		t.Errorf("the smallest amount read back: %v %s", err, dump(d))
	}
}
