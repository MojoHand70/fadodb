package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
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
	if c, _ := s.class("Peer"); !c.who {
		t.Error("a class that asks who inside a LOOKUP was not marked as reading who")
	}
	if list, err := s.Transitions(ctx, "Peer", "P-1"); err != nil || len(list) != 1 || !list[0].Enabled {
		t.Errorf("a listing whose guard reads the moments of the row: %v %s", err, dump(list))
	}
	const late = `{"name": "Late", "variables": {"note": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "late"]},
 "transitions": [{"name": "expire", "from": "open", "to": "late", "guard": "NOT ISBLANK(ENTEREDAT()) AND NOT ISBLANK(CREATEDAT()) AND VERSION() = 1"}]}`
	if _, err := s.DefineClass(ctx, []byte(late)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Late", Values{}, WithID("L-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.class("Late"); c.who {
		t.Error("a class that reads only moments was marked as reading who")
	}
	if list, err := s.Transitions(ctx, "Late", "L-1"); err != nil || len(list) != 1 || !list[0].Enabled {
		t.Errorf("a listing whose guard reads the moments of a row read without who: %v %s", err, dump(list))
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

// lifeCase is a class whose formulas read what the document's life says
// of its states (ADR-0047, item 6): an access rule, a guard, triggered
// variables, a rule, and an eventless transition.
const lifeCase = `{"name": "Case",
 "variables": {
   "note":    {"type": "text"},
   "rounds":  {"type": "number", "formula": "ENTRIES(\"review\")", "mode": "triggered", "on": ["enter:review"]},
   "editors": {"type": "text", "formula": "FIRSTEDITOR(\"draft\") & \"/\" & LASTEDITOR(\"draft\")", "mode": "triggered", "on": ["exit:draft", "refresh"]},
   "told":    {"type": "text", "formula": "TEXT(ENTRIES(\"draft\")) & \" \" & TEXT(ENTRIES(\"review\")) & \" \" & FIRSTENTEREDBY(\"review\") & \" \" & LASTLEFTBY(\"draft\") & \" \" & TEXT(DAYSIN(\"draft\"))", "mode": "triggered", "on": ["refresh"]},
   "closed":  {"type": "text"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "closed"]},
 "transitions": [
   {"name": "send", "from": "draft", "to": "review", "guard": "ENTRIES(\"draft\") >= 1"},
   {"name": "back", "from": "review", "to": "draft"},
   {"from": "review", "to": "closed", "guard": "ENTRIES(\"review\") >= 2"}
 ],
 "access": {"transition": {"back": "LASTENTEREDBY(\"review\") <> USER()"}},
 "rules": [{"on": "enter", "state": "closed", "do": [{"action": "set", "values": {"closed": "LASTLEFTBY(\"review\") & \" \" & TEXT(ENTRIES(\"closed\"))"}}]}]
}`

// TestLifeStates is what the functions of a state read in a commit
// (ADR-0047, item 6): the events of the document from the log, once for
// a command, and the steps the command makes itself.
func TestLifeStates(t *testing.T) {
	cl := &clock{now: start}
	s, path := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(lifeCase)); err != nil {
		t.Fatal(err)
	}
	c, _ := s.class("Case")
	if !c.states || !c.life {
		t.Fatalf("the class does not know its formulas read the states: states %v, life %v", c.states, c.life)
	}
	reads := func(want int, what string) {
		t.Helper()
		if s.lifeReads != want {
			t.Errorf("%s: %d reads of the document's events, want %d", what, s.lifeReads, want)
		}
		s.lifeReads = 0
	}
	val := func(d *Document, err error, name string) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(d.Values[name])
	}
	// A document being created has nothing in the log, and nothing is read.
	if _, err := s.Create(ctx, "Case", Values{"note": "a"}, WithID("K-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	reads(0, "a create")
	// A set by nobody, a reset by bob: bob is the last editor of the draft.
	if _, err := s.Set(ctx, "Case", "K-1", Values{"note": "b"}); err != nil {
		t.Fatal(err)
	}
	reads(0, "a set that evaluates none of the functions")
	if _, err := s.Reset(ctx, "Case", "K-1", []string{"note"}, As(bob)); err != nil {
		t.Fatal(err)
	}
	// One command, a guard, two triggered variables and the listing of
	// the result: one read.
	cy := Principal{ID: "cy"}
	d, err := s.Transition(ctx, "Case", "K-1", "send", Values{"note": "c"}, As(cy))
	if got := val(d, err, "editors") + " " + val(d, err, "rounds"); got != "ann/bob 1" {
		t.Errorf("after send: %s, want ann/bob 1: who sends it, with values or without, is no editor of the draft", got)
	}
	reads(1, "a transition")
	// Who last moved it into review may not move it back.
	if _, err := s.Transition(ctx, "Case", "K-1", "back", nil, As(cy)); err == nil || !strings.Contains(err.Error(), "forbidden") && !strings.Contains(fmt.Sprintf("%#v", err), CodeForbidden) {
		t.Errorf("back by who sent it: %v", err)
	}
	reads(1, "a refused transition")
	if _, err := s.Transition(ctx, "Case", "K-1", "back", nil, As(ann)); err != nil {
		t.Fatal(err)
	}
	reads(1, "a transition whose access rule asks")
	list, err := s.Transitions(ctx, "Case", "K-1", As(ann))
	if err != nil || len(list) != 1 || !list[0].Enabled {
		t.Errorf("the listing: %v %+v", err, list)
	}
	reads(1, "a listing")
	// A preview is told the step it would make and leaves nothing: the
	// second entry into review closes the case in the same command.
	pv, err := s.Preview(ctx, "Case", "K-1", nil, "send", As(bob))
	if err != nil || pv.Document.State != "closed" || fmt.Sprintf("%v %v", pv.Document.Values["rounds"], pv.Document.Values["closed"]) != "2 bob 1" {
		t.Errorf("the preview of the second send: %v %s", err, dump(pv))
	}
	reads(1, "a preview")
	d, err = s.Transition(ctx, "Case", "K-1", "send", nil, As(bob))
	if err != nil || d.State != "closed" || d.Version != 7 || fmt.Sprintf("%v %v", d.Values["rounds"], d.Values["closed"]) != "2 bob 1" {
		t.Errorf("the second send: %v %s", err, dump(d))
	}
	reads(1, "a transition with an eventless one after it")

	// The log alone tells the same after a reopen, and a migration is no
	// change of the document: its maker is no editor.
	s.Close()
	s2, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Now: cl.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Create(ctx, "Case", Values{"note": "x"}, WithID("K-2"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.DefineClass(ctx, []byte(strings.Replace(lifeCase, `"closed":  {"type": "text"}`, `"closed":  {"type": "text"}, "more": {"type": "text"}`, 1))); err != nil {
		t.Fatal(err)
	}
	if n, err := s2.Migrate(ctx, "Case", As(bob)); err != nil || n == 0 {
		t.Fatalf("migrate: %d %v", n, err)
	}
	d, err = s2.Refresh(ctx, "Case", "K-2", []string{"editors"}, As(Principal{ID: "dee"}))
	if got := val(d, err, "editors"); got != "ann/ann" {
		t.Errorf("the editors after a migration by bob and a refresh by dee: %s, want ann/ann: neither changed the document", got)
	}
	if _, err := s2.Reset(ctx, "Case", "K-2", []string{"note"}, As(bob)); err != nil {
		t.Fatal(err)
	}
	d, err = s2.Refresh(ctx, "Case", "K-2", []string{"editors"}, As(Principal{ID: "dee"}))
	if got := val(d, err, "editors"); got != "ann/bob" {
		t.Errorf("the editors after a reset by bob: %s, want ann/bob", got)
	}
	d, err = s2.Refresh(ctx, "Case", "K-1", []string{"told"}, As(ann))
	if got := val(d, err, "told"); !strings.HasPrefix(got, "2 2 cy bob ") {
		t.Errorf("the life of K-1 read from the log after a reopen: %q", got)
	}
	// The event of a creation names the state the document entered.
	evs, err := s2.Events(ctx, "Case", "K-2")
	if err != nil || len(evs) == 0 || evs[0].Kind != EventCreated || evs[0].ToState != "draft" || evs[0].FromState != "" {
		t.Errorf("the event of the creation: %v %+v", err, evs)
	}
}

// TestLifeStates_Defined is what a class is refused when it is defined
// (ADR-0047, items 6 and 7): a state written as text that is none of
// its states, and a function of a state where a formula runs for every
// row of a query.
func TestLifeStates_Defined(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	base := `{"name": "Memo", "variables": {"note": {"type": "text"}%s},
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent"%s}]%s}`
	for _, tc := range []struct{ name, vars, trans, rest, path, code, say string }{
		{"a guard that names no state of the class", "", `, "guard": "ENTRIES(\"sent out\") > 0"`, "", "/transitions/0/guard", "", `no state "sent out"`},
		{"a state in another case", "", `, "guard": "ISBLANK(FIRSTENTERED(\"Draft\"))"`, "", "/transitions/0/guard", "", `no state "Draft"`},
		{"a triggered variable that names none", `, "n": {"type": "number", "formula": "DAYSIN(\"gone\")", "mode": "triggered", "on": ["refresh"]}`, "", "", "/variables/n/formula", "", `no state "gone"`},
		{"an auto variable", `, "n": {"type": "number", "formula": "ENTRIES(\"draft\")"}`, "", "", "/variables/n/formula", "", "ENTRIES"},
		{"a read rule", "", "", `, "access": {"read": "FIRSTENTEREDBY(\"sent\") = USER()"}`, "/access/read", model.CodeNotAllowed, "a triggered variable"},
		{"a read right", "", "", `, "rights": {"*": {"note": {"read": "ENTRIES(\"sent\") = 0"}}}`, "/rights/*/note/read", model.CodeNotAllowed, "a triggered variable"},
	} {
		_, err := s.DefineClass(ctx, []byte(fmt.Sprintf(base, tc.vars, tc.trans, tc.rest)))
		var diags model.Diagnostics
		if !errors.As(err, &diags) || len(diags) != 1 || diags[0].Path != tc.path || tc.code != "" && diags[0].Code != tc.code || !strings.Contains(diags[0].Message+" "+diags[0].Hint, tc.say) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// A write right, a transition rule and a rule's condition may ask.
	ok := fmt.Sprintf(base, "", "", `, "access": {"transition": {"send": "FIRSTEDITOR(\"draft\") = USER()"}},
 "rights": {"sent": {"note": {"write": "ENTRIES(\"sent\") = 1"}}},
 "rules": [{"on": "enter", "state": "sent", "if": "ENTRIES(\"sent\") = 1", "do": [{"action": "emit", "event": "first"}]}]`)
	if _, err := s.DefineClass(ctx, []byte(ok)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"note": "a"}, WithID("M-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Memo", "M-1", "send", nil, As(bob)); err == nil {
		t.Error("send by one who did not write the draft: allowed")
	}
	if _, err := s.Transition(ctx, "Memo", "M-1", "send", nil, As(ann)); err != nil {
		t.Errorf("send by who wrote the draft: %v", err)
	}
	if _, err := s.Set(ctx, "Memo", "M-1", Values{"note": "b"}, As(ann)); err != nil {
		t.Errorf("the write right that asks the life: %v", err)
	}
	// A filter is refused them, with where to look instead.
	if _, err := s.Query(ctx, Filter{Class: "Memo", Where: `ENTRIES("sent") > 0`}); err == nil || !strings.Contains(err.Error(), "Memo_states") {
		t.Errorf("a filter with a function of a state: %v", err)
	}

	// A class whose formulas ask nothing keeps no steps and reads nothing.
	if _, err := s.DefineClass(ctx, []byte(lifeClaim)); err != nil {
		t.Fatal(err)
	}
	// A draft of that class that begins to ask is told from the log of
	// the stored document, with the step of the preview.
	for _, who := range []string{"ann", "bob"} {
		if _, err := s.Create(ctx, "Claim", Values{"amount": "5.00"}, WithID("C-"+who), As(Principal{ID: who})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transition(ctx, "Claim", "C-ann", "submit", nil, As(bob)); err != nil {
		t.Fatal(err)
	}
	draft := strings.Replace(lifeClaim, `"note":     {"type": "text"},`,
		`"note":     {"type": "text"}, "seen": {"type": "text", "formula": "FIRSTEDITOR(\"draft\") & \" \" & FIRSTLEFTBY(\"draft\") & \" \" & TEXT(ENTRIES(\"review\"))", "mode": "triggered", "on": ["enter:review"]},`, 1)
	if draft == lifeClaim {
		t.Fatal("the draft is the class")
	}
	pv, err := s.PreviewDraft(ctx, []byte(draft), "C-ann", nil, "again", As(ann))
	if err != nil || fmt.Sprint(pv.Document.Values["seen"]) != "ann bob 2" {
		t.Errorf("a draft that asks the life of a stored document: %v %s", err, dump(pv))
	}
	c, _ := s.class("Claim")
	if c.states {
		t.Error("a class without a function of a state says it has one")
	}
	row := &docRow{}
	row.step(c, formula.LifeEvent{Kind: formula.LifeCreated, To: "draft"})
	if row.pending != nil {
		t.Error("a class that asks nothing keeps the steps of its commits")
	}
}

// TestLife_AskedInsideALookup: a formula that asks who created the
// document from inside the condition of a LOOKUP asks it all the same,
// so the listing of open transitions reads who did, and answers what the
// commit answers.
func TestLife_AskedInsideALookup(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	def := `{"name": "Thing", "variables": {"note": {"type": "text"}},
 "states": {"initial": "a", "list": ["a", "b"]},
 "transitions": [{"name": "go", "from": "a", "to": "b", "guard": "LOOKUP(Thing, COUNT(id), created_by = CREATEDBY()) >= 1"}]}`
	if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Thing", Values{"note": "x"}, WithID("T-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.class("Thing"); !c.who {
		t.Error("the class does not know a formula of its asks who created the document")
	}
	list, err := s.Transitions(ctx, "Thing", "T-1", As(bob))
	if err != nil || len(list) != 1 || !list[0].Enabled {
		t.Errorf("the listing: %v %+v", err, list)
	}
	if _, err := s.Transition(ctx, "Thing", "T-1", "go", nil, As(bob)); err != nil {
		t.Errorf("the transition: %v", err)
	}
}

// TestLifeStates_Corners are the corners of reading a life from the log
// (ADR-0047, item 6): a class without states, a row told as of its own
// version, a clock that went back, a document an earlier version of the
// class computed, and a rule that creates a document for each element
// of a list.
func TestLifeStates_Corners(t *testing.T) {
	now := start
	s, _ := openStore(t, Options{Now: func() time.Time { return now }})
	ctx := context.Background()

	// A class without states has no state to name, and one that is
	// computed finds nothing.
	_, err := s.DefineClass(ctx, []byte(`{"name": "Plain", "variables": {"n": {"type": "number", "default": "ENTRIES(\"draft\")"}}}`))
	var diags model.Diagnostics
	if !errors.As(err, &diags) || len(diags) != 1 || !strings.Contains(diags[0].Message, "the class has no states") {
		t.Errorf("a state named in a class without states: %v", err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Plain", "variables": {"note": {"type": "text"}, "n": {"type": "number", "default": "ENTRIES(note)"}}}`)); err != nil {
		t.Fatal(err)
	}
	if pv, err := s.Preview(ctx, "Plain", "", Values{"note": "draft"}, ""); err != nil || pv.Document.Values["n"] != 0.0 {
		t.Errorf("a computed state in a class without states: %v %s", err, dump(pv))
	}

	// The clock goes back between two commits: the versions say the
	// order of the events, not their times.
	if _, err := s.DefineClass(ctx, []byte(lifeCase)); err != nil {
		t.Fatal(err)
	}
	dee := Principal{ID: "dee"}
	step := func(d time.Duration, do func() (*Document, error)) *Document {
		t.Helper()
		now = now.Add(d)
		doc, err := do()
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	step(0, func() (*Document, error) { return s.Create(ctx, "Case", Values{"note": "a"}, WithID("K-1"), As(ann)) })
	step(time.Hour, func() (*Document, error) { return s.Set(ctx, "Case", "K-1", Values{"note": "b"}, As(bob)) })
	step(-2*time.Hour, func() (*Document, error) { return s.Set(ctx, "Case", "K-1", Values{"note": "c"}, As(dee)) })
	d := step(time.Minute, func() (*Document, error) { return s.Transition(ctx, "Case", "K-1", "send", nil, As(ann)) })
	if d.Values["editors"] != "ann/dee" {
		t.Errorf("the editors when the clock went back: %v, want ann/dee", d.Values["editors"])
	}
	step(time.Hour, func() (*Document, error) { return s.Transition(ctx, "Case", "K-1", "back", nil, As(bob)) })
	step(-3*time.Hour, func() (*Document, error) { return s.Set(ctx, "Case", "K-1", Values{"note": "d"}, As(bob)) })
	d = step(time.Minute, func() (*Document, error) { return s.Refresh(ctx, "Case", "K-1", []string{"told"}, As(ann)) })
	if got := fmt.Sprint(d.Values["told"]); !strings.HasPrefix(got, "2 1 ann ann ") {
		t.Errorf("the life when the clock went back: %q", got)
	}

	// A row is told the life up to its own version, whatever the log has
	// since.
	c, _ := s.class("Case")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, "K-1"), false)
	if err != nil {
		t.Fatal(err)
	}
	row.logged = 3
	if err := s.tellLife(c, row); err != nil {
		t.Fatal(err)
	}
	if st := row.life.States; len(st) != 1 || st[0].State != "draft" || st[0].Entries != 1 || !st[0].In || st[0].LastEditor != "dee" {
		t.Errorf("the life as of version 3: %+v", st)
	}

	// A document that an earlier version of the class computed adopts
	// the new one with its next command, and is told from the log all
	// the same.
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(lifeCase, `"closed":  {"type": "text"}`, `"closed":  {"type": "text"}, "more": {"type": "text"}`, 1))); err != nil {
		t.Fatal(err)
	}
	d = step(time.Minute, func() (*Document, error) { return s.Transition(ctx, "Case", "K-1", "send", nil, As(dee)) })
	if d.State != "closed" || d.Values["rounds"] != 2.0 || d.Values["editors"] != "ann/bob" {
		t.Errorf("a document of an earlier version of the class: %s", dump(d))
	}

	// A rule that creates a document for each element of a list reads the
	// life of the document it runs for.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Ask", "variables": {"job": {"type": "text"}, "whom": {"type": "text"}, "round": {"type": "number"}, "first": {"type": "text"}},
 "states": {"initial": "open", "list": ["open"]}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Job", "variables": {"people": {"type": "array<text>"}},
 "states": {"initial": "draft", "list": ["draft", "asked"]},
 "transitions": [{"name": "ask", "from": "draft", "to": "asked"}, {"name": "again", "from": "asked", "to": "asked"}],
 "rules": [{"on": "enter", "state": "asked", "do": [{"action": "create", "class": "Ask", "each": "people", "as": "who", "id": "ID() & \"-\" & who & \"-\" & TEXT(ENTRIES(\"asked\"))",
   "values": {"job": "ID()", "whom": "who", "round": "ENTRIES(\"asked\")", "first": "FIRSTENTEREDBY(\"asked\")"}}]},
  {"on": "exit", "state": "draft", "do": [{"action": "create", "class": "Ask", "each": "people", "as": "who", "id": "ID() & \"-out-\" & who", "values": {"whom": "ENTEREDBY()"}}]}]}`)); err != nil {
		t.Fatal(err)
	}
	step(time.Minute, func() (*Document, error) {
		return s.Create(ctx, "Job", Values{"people": []any{"x", "y"}}, WithID("J-1"), As(ann))
	})
	step(time.Minute, func() (*Document, error) { return s.Transition(ctx, "Job", "J-1", "ask", nil, As(bob)) })
	step(time.Minute, func() (*Document, error) { return s.Transition(ctx, "Job", "J-1", "again", nil, As(dee)) })
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	ask, err := s.Get(ctx, "Ask", "J-1-y-2")
	if err != nil || ask.Values["round"] != 2.0 || ask.Values["first"] != "bob" || ask.Values["whom"] != "y" {
		t.Errorf("a document created for each element, by a rule that reads the life: %v %s", err, dump(ask))
	}
	// On the way out such a rule reads the stay that ends, as every rule
	// of an exit does: who began the draft, not who ends it.
	if out, err := s.Get(ctx, "Ask", "J-1-out-x"); err != nil || out.Values["whom"] != "ann" {
		t.Errorf("a document created for each element by a rule of an exit: %v %s", err, dump(out))
	}
}

// TestLifeStates_TheCommitItself: what a function of a state sees of the
// command in progress (ADR-0047, item 6). A set and a reset tell of
// themselves, so their maker is the last editor in their own rules; a
// LOOKUP whose condition asks the life of the reading document is given
// it; and the steps of two rows cloned from one do not run into each
// other.
func TestLifeStates_TheCommitItself(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	def := `{"name": "Note", "variables": {"text": {"type": "text"}, "last": {"type": "text"}, "peers": {"type": "number", "formula": "LOOKUP(Note, COUNT(id), created_by = FIRSTENTEREDBY(\"sent\"))", "mode": "triggered", "on": ["enter:sent"]}},
 "states": {"initial": "draft", "list": ["draft", "sent"]},
 "transitions": [{"name": "send", "from": "draft", "to": "sent"}],
 "rules": [{"on": "values_change", "variables": ["text"], "do": [{"action": "set", "values": {"last": "LASTEDITOR(STATE()) & \" of \" & TEXT(ENTRIES(\"draft\"))"}}]}]}`
	if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
		t.Fatal(err)
	}
	cy := Principal{ID: "cy"}
	for id, p := range map[string]Principal{"N-1": ann, "N-2": bob, "N-3": bob} {
		if _, err := s.Create(ctx, "Note", Values{"text": "a"}, WithID(id), As(p)); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.Set(ctx, "Note", "N-1", Values{"text": "b"}, As(bob))
	if err != nil || d.Values["last"] != "bob of 1" {
		t.Errorf("a set in its own rule: %v %s", err, dump(d))
	}
	d, err = s.Reset(ctx, "Note", "N-1", []string{"text"}, As(cy))
	if err != nil || d.Values["last"] != "cy of 1" {
		t.Errorf("a reset in its own rule: %v %s", err, dump(d))
	}
	// The statement of the lookup is bound the states of the document
	// that reads: bob sends it, and bob created two notes.
	d, err = s.Transition(ctx, "Note", "N-1", "send", nil, As(bob))
	if err != nil || d.Values["peers"] != 2.0 {
		t.Errorf("a LOOKUP whose condition asks who first sent the document: %v %s", err, dump(d))
	}

	// Two rows cloned from one keep their own steps.
	c, _ := s.class("Note")
	ev := func(to string) formula.LifeEvent {
		return formula.LifeEvent{Kind: formula.LifeMoved, From: "draft", To: to, At: 1}
	}
	base := &docRow{}
	for _, to := range []string{"a", "b", "c"} {
		base.step(c, ev(to))
	}
	one, two := base.clone(), base.clone()
	one.step(c, ev("one"))
	two.step(c, ev("two"))
	if len(base.pending) != 3 || len(one.pending) != 4 || len(two.pending) != 4 || one.pending[3].To != "one" || two.pending[3].To != "two" {
		t.Errorf("the steps of two clones: %+v and %+v, of %+v", one.pending, two.pending, base.pending)
	}
}
