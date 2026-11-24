package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// waitedFor is what a wait answered, as the kind and the document of
// each event, with the name of a transition.
func waitedFor(w *Waited) string {
	var out []string
	for _, ev := range w.Events {
		said := ev.Kind + " " + ev.DocID
		if ev.Name != "" {
			said += " " + ev.Name
		}
		out = append(out, said)
	}
	return strings.Join(out, "; ")
}

// waits asks the store for the changes after a place, and fails the test
// when it refuses.
func waits(t *testing.T, s *Store, f ChangeFilter, w Wait, as Option) *Waited {
	t.Helper()
	got, err := s.WaitForChanges(context.Background(), f, w, as)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// expenses defines the expense and the memo of the tests of the stream.
func expenses(t *testing.T, s *Store) {
	t.Helper()
	for _, def := range []string{expense, openMemo} {
		if _, err := s.DefineClass(context.Background(), []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
}

// A wait answers the events after its place that the caller may hear of,
// and the place to ask from next; given that place it misses nothing and
// tells nothing twice (ADR-0064, items 22 to 25).
func TestWait_ThePlaceTravelsWithTheCaller(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	expenses(t, s)

	// A log with no event: the place is the start of the log, which a
	// first event comes after.
	empty := waits(t, s, ChangeFilter{}, Wait{}, asOla)
	if len(empty.Events) != 0 || empty.After != LogStart || empty.More {
		t.Fatalf("a wait on an empty log answered %+v", empty)
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "first"}, asEve, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	first := waits(t, s, ChangeFilter{After: empty.After}, Wait{}, asOla)
	if got := waitedFor(first); got != "created m-1" || first.After != first.Events[0].ID || first.More {
		t.Fatalf("after the start: %q, %+v", got, first)
	}

	// From now: nothing yet, and the place is where the log stands.
	now := waits(t, s, ChangeFilter{}, Wait{}, asOla)
	if len(now.Events) != 0 || now.After != first.After {
		t.Fatalf("from now: %+v", now)
	}

	// Ola files an expense for Ann and submits it; Eve writes a memo.
	if _, err := s.Create(ctx, "Expense", Values{"amount": 120, "approver": "ann"}, asOla, WithID("e-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Expense", "e-1", "submit", nil, asOla); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "second"}, asEve, WithID("m-2")); err != nil {
		t.Fatal(err)
	}
	ann := waits(t, s, ChangeFilter{After: now.After}, Wait{}, asAnn)
	if got := waitedFor(ann); got != "created e-1; transition e-1 submit; created m-2" || ann.More {
		t.Errorf("Ann is answered %q", got)
	}
	again := waits(t, s, ChangeFilter{After: ann.After}, Wait{}, asAnn)
	if len(again.Events) != 0 || again.After != ann.After {
		t.Errorf("Ann asks again from her place: %+v", again)
	}

	// Eve may read the memos and no expense. Her place moves over what
	// she may not hear of, so that she does not read it again.
	eve := waits(t, s, ChangeFilter{After: now.After, Classes: []string{"Expense"}}, Wait{}, asEve)
	if len(eve.Events) != 0 || eve.After != ann.After {
		t.Errorf("Eve, of the expenses: %+v, place %q, want %q", eve.Events, eve.After, ann.After)
	}

	// The filter is the stream's: transitions into review only.
	review := waits(t, s, ChangeFilter{After: now.After, Kinds: []string{EventTransition}, ToStates: []string{"review"}}, Wait{}, asFin)
	if got := waitedFor(review); got != "transition e-1 submit" {
		t.Errorf("finance, of what enters review: %q", got)
	}
}

// An answer holds at most its limit of events. A full answer stops at
// its last event and says there is more; the next call takes the rest.
func TestWait_AFullAnswer(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	expenses(t, s)
	start := waits(t, s, ChangeFilter{}, Wait{}, asOla)
	for _, id := range []string{"m-1", "m-2", "m-3", "m-4", "m-5"} {
		if _, err := s.Create(ctx, "Memo", Values{"text": id}, asEve, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	place, calls := start.After, 0
	for {
		w := waits(t, s, ChangeFilter{After: place}, Wait{Limit: 2}, asOla)
		calls++
		got = append(got, waitedFor(w))
		if len(w.Events) > 0 && w.After != w.Events[len(w.Events)-1].ID {
			t.Errorf("a full answer's place is not its last event: %+v", w)
		}
		place = w.After
		if !w.More {
			break
		}
	}
	if want := []string{"created m-1; created m-2", "created m-3; created m-4", "created m-5"}; !reflect.DeepEqual(got, want) || calls != 3 {
		t.Errorf("five memos two at a time: %q in %d calls", got, calls)
	}
}

// A wait with nothing to answer waits for the next commit it may hear
// of, up to its seconds, and answers none when they pass.
func TestWait_WaitingForTheNextCommit(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	expenses(t, s)
	start := waits(t, s, ChangeFilter{}, Wait{}, asOla)

	answered := make(chan *Waited, 1)
	began := time.Now()
	go func() {
		w, err := s.WaitForChanges(ctx, ChangeFilter{After: start.After, Classes: []string{"Memo"}}, Wait{Seconds: 20}, asOla)
		if err != nil {
			t.Error(err)
		}
		answered <- w
	}()
	time.Sleep(50 * time.Millisecond)
	// An expense of somebody else's wakes the wait, which hears nothing
	// of it and waits on.
	if _, err := s.Create(ctx, "Expense", Values{"amount": 5}, asEve, WithID("e-1")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	select {
	case w := <-answered:
		t.Fatalf("the wait answered an expense it does not hear of: %+v", w)
	default:
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "now"}, asEve, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-answered:
		if got := waitedFor(w); got != "created m-1" {
			t.Errorf("the wait answered %q", got)
		}
		if took := time.Since(began); took > 10*time.Second {
			t.Errorf("the wait answered after %s", took)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the wait did not answer the memo")
	}

	// Nothing comes: after its second it answers none, from its place.
	began = time.Now()
	quiet := waits(t, s, ChangeFilter{Classes: []string{"Memo"}}, Wait{Seconds: 1}, asOla)
	if took := time.Since(began); len(quiet.Events) != 0 || took < 900*time.Millisecond || took > 5*time.Second {
		t.Errorf("a quiet wait answered %+v after %s", quiet, took)
	}

	// A wait that is stopped answers what it has, with its place.
	stopped, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	w, err := s.WaitForChanges(stopped, ChangeFilter{}, Wait{Seconds: 30}, asOla)
	if err != nil || len(w.Events) != 0 || w.After == "" {
		t.Errorf("a stopped wait: %+v, %v", w, err)
	}
}

// A caller who acts for somebody hears of a class it may act for them on,
// as that person would; the stream itself is still heard as oneself
// (ADR-0064, item 27; ADR-0071, item 31).
func TestWait_ActingForSomebody(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	expenses(t, s)
	start := waits(t, s, ChangeFilter{}, Wait{}, asOla)
	if _, err := s.Create(ctx, "Expense", Values{"amount": 120, "approver": "ann"}, asOla, WithID("e-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "for all"}, asEve, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	// The approver's own read rule names her: Bob, for Ann, reads it only
	// when he may act for her.
	bobForAnn := As(Principal{ID: "bob", Kind: ActorAgent, For: "ann"})
	nobody := waits(t, s, ChangeFilter{After: start.After}, Wait{}, bobForAnn)
	if len(nobody.Events) != 0 {
		t.Errorf("Bob, for Ann, with no substitution: %q", waitedFor(nobody))
	}
	// Ann lets him act for her on expenses alone.
	if _, err := s.Create(ctx, model.SubstitutionClass, Values{"by": "bob", "classes": []string{"Expense"}}, asAnn, WithID("ann-bob")); err != nil {
		t.Fatal(err)
	}
	read := `{"name": "Expense",
 "variables": {"owner": {"type": "text", "default": "USER()"}, "approver": {"type": "text"}, "amount": {"type": "number"}},
 "states": {"initial": "draft", "list": ["draft", "review", "paid"]},
 "transitions": [{"name": "submit", "from": "draft", "to": "review"}, {"name": "pay", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR (NOT ISBLANK(approver) AND (approver = USER() OR approver = ACTINGFOR())) OR INGROUP(\"finance\")", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	if _, err := s.DefineClass(ctx, []byte(read), asSetup); err != nil {
		t.Fatal(err)
	}
	if got := waitedFor(waits(t, s, ChangeFilter{After: start.After}, Wait{}, bobForAnn)); got != "created e-1" {
		t.Errorf("Bob, for Ann, on expenses: %q, want the expense and not the memo", got)
	}
	// The stream is heard as oneself, as before.
	if _, err := s.Subscribe(ctx, ChangeFilter{}, bobForAnn); err == nil || !strings.Contains(err.Error(), "heard as oneself") {
		t.Errorf("a stream for somebody: %v", err)
	}
}

// A content administrator hears past the read rule with a reason, and
// the log of reads names the wait (ADR-0067).
func TestWait_PastTheRule(t *testing.T) {
	s, ids := acme(t)
	ctx := context.Background()
	start := waits(t, s, ChangeFilter{}, Wait{}, As(acmeOlga))
	if _, err := s.Set(ctx, "SalaryReview", ids["jan"], Values{"salary": 2}, As(acmeEwa)); err != nil {
		t.Fatal(err)
	}
	quiet := waits(t, s, ChangeFilter{After: start.After}, Wait{}, As(acmeOlga))
	told := waits(t, s, ChangeFilter{After: start.After}, Wait{}, Option(func(o *options) { As(acmeOlga)(o); why(o) }))
	if len(quiet.Events) != 0 || len(told.Events) != 1 || told.Events[0].DocID != ids["jan"] {
		t.Errorf("without a reason %q, with one %q", waitedFor(quiet), waitedFor(told))
	}
	reads := logOf(t, s, ReadsFilter{Command: "wait_for_changes"})
	if len(reads) != 1 || !reflect.DeepEqual(reads[0].Documents, []string{ids["jan"]}) || reads[0].Reason == "" {
		t.Errorf("the entry of the wait: %s", dump(reads))
	}
}

// What a wait refuses.
func TestWait_WhatIsRefused(t *testing.T) {
	s, _ := openStore(t, Options{})
	expenses(t, s)
	for what, c := range map[string]struct {
		f    ChangeFilter
		w    Wait
		path string
	}{
		"a minus second":       {w: Wait{Seconds: -1}, path: "/seconds"},
		"more than a minute":   {w: Wait{Seconds: MaxWaitSeconds + 1}, path: "/seconds"},
		"a limit too large":    {w: Wait{Limit: MaxWaitLimit + 1}, path: "/limit"},
		"a minus limit":        {w: Wait{Limit: -1}, path: "/limit"},
		"a place that is none": {f: ChangeFilter{After: "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"}},
		"a kind that is none":  {f: ChangeFilter{Kinds: []string{"approved"}}},
	} {
		_, err := s.WaitForChanges(context.Background(), c.f, c.w, asOla)
		e, ok := err.(*Error)
		if !ok || e.Code != CodeBadValue || e.Path != c.path {
			t.Errorf("%s: %v", what, err)
		}
	}
}
