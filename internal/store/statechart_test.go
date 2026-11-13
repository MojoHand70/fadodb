package store

import (
	"context"
	"strings"
	"testing"
)

// approval is the statechart of the Phase 3 tests: a named transition
// with a guard over a value, one over the principal, one from two states,
// one whose guard errors, an eventless transition out of approved and
// one back to draft, and two terminal states linked by a transition.
const approval = `{"name": "Approval", "variables": {
  "amount": {"type": "number"},
  "note":   {"type": "text"},
  "big":    {"formula": "amount > 1000"},
  "trace":  {"type": "text", "default": "STATE()"}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "booked", "rejected", "archived"], "final": ["rejected", "archived"]},
 "transitions": [
   {"name": "submit",  "from": "draft",  "to": "review",   "guard": "amount > 0"},
   {"name": "approve", "from": "review", "to": "approved", "guard": "INGROUP(\"finance\")"},
   {"name": "reject",  "from": ["draft", "review"], "to": "rejected"},
   {"from": "approved", "to": "booked", "guard": "NOT big"},
   {"from": "review",   "to": "draft",  "guard": "amount <= 0"},
   {"name": "archive", "from": "rejected", "to": "archived"},
   {"name": "fail",    "from": "draft",  "to": "review",   "guard": "1 / amount > 0"}
 ]}`

var finance = Principal{ID: "fin", Groups: []string{"finance"}}

func defineApproval(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DefineClass(context.Background(), []byte(approval), asSetup); err != nil {
		t.Fatal(err)
	}
}

func states(infos []TransitionInfo) string {
	var b strings.Builder
	for i, in := range infos {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(in.Name)
		if in.Enabled {
			b.WriteString("+")
		} else {
			b.WriteString("-" + in.Code)
		}
	}
	return b.String()
}

func visits(t *testing.T, s *Store, class, id string) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT state, entry_version, exit_version, duration FROM "`+class+`_states" WHERE id = ? ORDER BY entry_version`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var state string
		var entry int64
		var exit, duration *int64
		if err := rows.Scan(&state, &entry, &exit, &duration); err != nil {
			t.Fatal(err)
		}
		p := state + ":" + itoa(int(entry))
		if exit != nil {
			p += "-" + itoa(int(*exit))
		}
		if duration != nil {
			p += "/" + itoa(int(*duration))
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

func TestTransition(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	doc, err := s.Create(ctx, "Approval", Values{"note": "new"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != "draft" || doc.Version != 1 || doc.Values["trace"] != "draft" {
		t.Fatalf("created: %s", dump(doc))
	}
	// The listing from draft: submit needs an amount, reject is open, fail errors.
	infos, err := s.Transitions(ctx, "Approval", doc.ID, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if got := states(infos); got != "submit-guard_false reject+ fail-guard_false" || !strings.Contains(infos[2].Message, "div_by_zero") || infos[0].To != "review" {
		t.Errorf("listing from draft: %s %s", got, dump(infos))
	}
	// Refusals before anything is written.
	for name, tc := range map[string]struct {
		transition string
		values     Values
		code       string
		hint       string
	}{
		"unknown":     {"fly", nil, CodeUnknownTransition, "submit, approve, reject, archive, fail"},
		"wrong state": {"approve", nil, CodeWrongState, "leaving draft: submit, reject, fail"},
		"guard false": {"submit", nil, CodeGuardFalse, "the guard: amount > 0"},
		"guard error": {"fail", nil, CodeGuardFalse, "the guard: 1 / amount > 0"},
	} {
		_, err := s.Transition(ctx, "Approval", doc.ID, tc.transition, tc.values, As(alice))
		e, _ := err.(*Error)
		if e == nil || e.Code != tc.code || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %v", name, err)
		}
		if name == "guard error" && !strings.Contains(e.Message, "div_by_zero at offset") {
			t.Errorf("guard error message: %s", e.Message)
		}
	}
	if got, _ := s.Get(ctx, "Approval", doc.ID); got.Version != 1 {
		t.Fatalf("a refusal wrote a version: %s", dump(got))
	}
	// Assignments and the transition are one commit and one event.
	doc, err = s.Transition(ctx, "Approval", doc.ID, "submit", Values{"amount": 500}, As(alice), Key("submit-1"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != "review" || doc.Version != 2 || doc.Values["amount"] != 500.0 || doc.Values["trace"] != "draft" {
		t.Errorf("submitted: %s", dump(doc))
	}
	events, _ := s.Events(ctx, "Approval", doc.ID)
	if len(events) != 2 || events[1].Kind != EventTransition || events[1].Name != "submit" || events[1].FromState != "draft" || events[1].ToState != "review" ||
		events[1].BeforeVersion != 1 || events[1].AfterVersion != 2 || events[1].CommandKey != "submit-1" || events[1].ActorID != "alice" {
		t.Errorf("events after submit: %s", dump(events))
	}
	// The guard over the principal.
	if _, err := s.Transition(ctx, "Approval", doc.ID, "approve", nil, As(alice)); code(err) != CodeGuardFalse {
		t.Errorf("approve as sales: %v", err)
	}
	infos, _ = s.Transitions(ctx, "Approval", doc.ID, As(finance))
	if got := states(infos); got != "approve+ reject+" {
		t.Errorf("listing from review for finance: %s", got)
	}
	// The eventless transition fires after the named one: two commits,
	// two versions, the second caused by the first.
	doc, err = s.Transition(ctx, "Approval", doc.ID, "approve", nil, As(finance), Key("approve-1"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != "booked" || doc.Version != 4 {
		t.Errorf("approved and booked: %s", dump(doc))
	}
	events, _ = s.Events(ctx, "Approval", doc.ID)
	if len(events) != 4 || events[2].Name != "approve" || events[2].AfterVersion != 3 || events[3].Kind != EventTransition || events[3].Name != "" ||
		events[3].FromState != "approved" || events[3].ToState != "booked" || events[3].BeforeVersion != 3 || events[3].AfterVersion != 4 ||
		events[3].CauseEventID != events[2].ID || events[3].CommandKey != "" || events[3].ActorID != "fin" {
		t.Errorf("events of the chain: %s", dump(events))
	}
	history, _ := s.History(ctx, "Approval", doc.ID)
	if len(history) != 4 || history[2].State != "approved" || history[3].State != "booked" || history[2].Values["amount"] != history[3].Values["amount"] {
		t.Errorf("history of the chain: %s", dump(history))
	}
	// The replay of the keyed command returns the last version it made.
	again, err := s.Transition(ctx, "Approval", doc.ID, "approve", nil, As(finance), Key("approve-1"))
	if err != nil || again.Version != 4 || again.State != "booked" {
		t.Errorf("replayed approve: %v %s", err, dump(again))
	}
	if _, err := s.Transition(ctx, "Approval", doc.ID, "reject", nil, As(finance), Key("approve-1")); code(err) != CodeKeyReused {
		t.Errorf("key with another transition: %v", err)
	}
	// The _states view: one row per visit, the chain's zero-length visit included.
	if got := visits(t, s, "Approval", doc.ID); !strings.HasPrefix(got, "draft:1-1/") || !strings.Contains(got, " review:2-2/") || !strings.Contains(got, " approved:3-3/0 booked:4") || strings.HasSuffix(got, "/0 booked:4-") {
		t.Errorf("state visits: %s", got)
	}
	// Nothing leaves booked.
	if infos, _ := s.Transitions(ctx, "Approval", doc.ID); len(infos) != 0 {
		t.Errorf("listing from booked: %s", dump(infos))
	}
	if _, err := s.Transition(ctx, "Approval", doc.ID, "submit", nil); code(err) != CodeWrongState {
		t.Errorf("submit from booked: %v", err)
	}
}

func TestTransition_SetFiresEventless(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	doc, _ := s.Create(ctx, "Approval", Values{"amount": 10}, As(alice))
	doc, err := s.Transition(ctx, "Approval", doc.ID, "submit", nil, As(alice))
	if err != nil || doc.State != "review" {
		t.Fatalf("submit: %v %s", err, dump(doc))
	}
	// A set that makes the eventless guard hold moves the document back.
	doc, err = s.Set(ctx, "Approval", doc.ID, Values{"amount": 0}, As(alice))
	if err != nil || doc.State != "draft" || doc.Version != 4 {
		t.Fatalf("set in review: %v %s", err, dump(doc))
	}
	events, _ := s.Events(ctx, "Approval", doc.ID)
	if len(events) != 4 || events[2].Kind != EventSet || events[3].Kind != EventTransition || events[3].CauseEventID != events[2].ID || events[3].ToState != "draft" {
		t.Errorf("events: %s", dump(events))
	}
	// A set that changes nothing commits nothing and moves nothing.
	same, err := s.Set(ctx, "Approval", doc.ID, Values{"amount": 0}, As(alice))
	if err != nil || same.Version != 4 {
		t.Errorf("no-op set: %v %s", err, dump(same))
	}
	// A big amount: approve, and the eventless transition does not fire.
	s.Transition(ctx, "Approval", doc.ID, "submit", Values{"amount": 5000}, As(alice))
	doc, err = s.Transition(ctx, "Approval", doc.ID, "approve", nil, As(finance))
	if err != nil || doc.State != "approved" || doc.Version != 6 {
		t.Errorf("approve a big amount: %v %s", err, dump(doc))
	}
}

func TestTransition_Terminal(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	doc, _ := s.Create(ctx, "Approval", Values{"amount": 10}, As(alice))
	doc, err := s.Transition(ctx, "Approval", doc.ID, "reject", Values{"note": "no"}, As(alice))
	if err != nil || doc.State != "rejected" || doc.Values["note"] != "no" {
		t.Fatalf("reject: %v %s", err, dump(doc))
	}
	if _, err := s.Set(ctx, "Approval", doc.ID, Values{"note": "x"}); code(err) != CodeTerminal {
		t.Errorf("set on a terminal document: %v", err)
	}
	if _, err := s.Transition(ctx, "Approval", doc.ID, "archive", Values{"note": "x"}); code(err) != CodeTerminal {
		t.Errorf("archive with values: %v", err)
	}
	infos, _ := s.Transitions(ctx, "Approval", doc.ID)
	if states(infos) != "archive+" {
		t.Errorf("listing from rejected: %s", dump(infos))
	}
	moved, err := s.Transition(ctx, "Approval", doc.ID, "archive", nil, As(alice))
	if err != nil || moved.State != "archived" || moved.Version != 3 || moved.Values["note"] != "no" {
		t.Errorf("archive: %v %s", err, dump(moved))
	}
	if _, err := s.Transition(ctx, "Approval", doc.ID, "archive", nil); code(err) != CodeWrongState {
		t.Errorf("archive twice: %v", err)
	}
}

func TestTransition_Preview(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	doc, _ := s.Create(ctx, "Approval", Values{"amount": 10}, As(alice))
	s.Transition(ctx, "Approval", doc.ID, "submit", nil, As(alice))
	p, err := s.Preview(ctx, "Approval", doc.ID, Values{"note": "ok"}, "approve", As(finance))
	if err != nil {
		t.Fatal(err)
	}
	if p.Document.State != "booked" || p.Document.Version != 4 || p.Document.Values["note"] != "ok" || strings.Join(p.Path, ">") != "review>approved>booked" || len(p.Transitions) != 0 {
		t.Errorf("previewed approve: %s", dump(p))
	}
	if got, _ := s.Get(ctx, "Approval", doc.ID); got.Version != 2 || got.State != "review" {
		t.Errorf("the preview changed the document: %s", dump(got))
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM events").Scan(&n)
	if n != 2 {
		t.Errorf("a preview wrote events: %d", n)
	}
	// A preview of a set shows the path and the open transitions.
	p, err = s.Preview(ctx, "Approval", doc.ID, Values{"amount": 0}, "", As(finance))
	if err != nil || strings.Join(p.Path, ">") != "review>draft" || states(p.Transitions) != "submit-guard_false reject+ fail-guard_false" {
		t.Errorf("previewed set: %v %s", err, dump(p))
	}
	// A preview of a create.
	p, err = s.Preview(ctx, "Approval", "", Values{"amount": 1}, "", As(alice))
	if err != nil || strings.Join(p.Path, ">") != "draft" || states(p.Transitions) != "submit+ reject+ fail+" {
		t.Errorf("previewed create: %v %s", err, dump(p))
	}
	// A refused transition is refused in the preview too.
	if _, err := s.Preview(ctx, "Approval", doc.ID, nil, "approve", As(alice)); code(err) != CodeGuardFalse {
		t.Errorf("previewed approve as sales: %v", err)
	}
	if _, err := s.Preview(ctx, "Approval", "", nil, "approve", As(alice)); code(err) != CodeBadValue {
		t.Errorf("previewed transition without a document: %v", err)
	}
	// A draft previews a transition under its own guards.
	draft := strings.Replace(approval, `"guard": "INGROUP(\"finance\")"`, `"guard": "TRUE"`, 1)
	p, err = s.PreviewDraft(ctx, []byte(draft), doc.ID, nil, "approve", designing(alice))
	if err != nil || p.Document.State != "booked" || strings.Join(p.Path, ">") != "review>approved>booked" {
		t.Errorf("draft preview of a transition: %v %s", err, dump(p))
	}
}

func TestTransition_ChainBound(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const pingpong = `{"name": "Pong", "variables": {"n": {"type": "number"}},
	  "states": {"initial": "a", "list": ["a", "b", "c"]},
	  "transitions": [
	    {"from": "a", "to": "b", "guard": "n > 0"},
	    {"from": "b", "to": "a", "guard": "n > 1"},
	    {"name": "stop", "from": ["a", "b"], "to": "c"}
	  ]}`
	if _, err := s.DefineClass(ctx, []byte(pingpong), asSetup); err != nil {
		t.Fatal(err)
	}
	// One hop at creation.
	doc, err := s.Create(ctx, "Pong", Values{"n": 1})
	if err != nil || doc.State != "b" || doc.Version != 2 {
		t.Fatalf("create with a hop: %v %s", err, dump(doc))
	}
	// A set that makes the guards hold in turn runs past the bound and
	// writes nothing.
	_, err = s.Set(ctx, "Pong", doc.ID, Values{"n": 2})
	e, _ := err.(*Error)
	if e == nil || e.Code != CodeTooManyTransitions || !strings.Contains(e.Hint, "b > a > b > a") {
		t.Fatalf("past the bound: %v", err)
	}
	if got, _ := s.Get(ctx, "Pong", doc.ID); got.Version != 2 || got.Values["n"] != 1.0 {
		t.Errorf("the refused command wrote: %s", dump(got))
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM Pong_history`).Scan(&n)
	if n != 2 {
		t.Errorf("%d history rows after the refusal", n)
	}
	if _, err := s.Preview(ctx, "Pong", doc.ID, Values{"n": 2}, ""); code(err) != CodeTooManyTransitions {
		t.Errorf("preview past the bound: %v", err)
	}
	// A batch creates with hops too, all or none.
	docs, err := s.CreateMany(ctx, "Pong", []Values{{"n": 1}, {"n": 0}})
	if err != nil || docs[0].State != "b" || docs[0].Version != 2 || docs[1].State != "a" || docs[1].Version != 1 {
		t.Errorf("batch with hops: %v %s", err, dump(docs))
	}
	if _, err := s.CreateMany(ctx, "Pong", []Values{{"n": 1}, {"n": 2}}); code(err) != CodeTooManyTransitions {
		t.Errorf("batch past the bound: %v", err)
	}
}

func TestStateless(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const calc = `{"name": "Calc", "variables": {"a": {"type": "number"}, "b": {"formula": "a * 2"}}}`
	if _, err := s.DefineClass(ctx, []byte(calc), asSetup); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create(ctx, "Calc", Values{"a": 1})
	e, _ := err.(*Error)
	if e == nil || e.Code != CodeStateless || !strings.Contains(e.Hint, "states block") {
		t.Errorf("create on a stateless class: %v", err)
	}
	if _, err := s.CreateMany(ctx, "Calc", []Values{{"a": 1}}); code(err) != CodeStateless {
		t.Errorf("batch on a stateless class: %v", err)
	}
	p, err := s.Preview(ctx, "Calc", "", Values{"a": 21}, "")
	if err != nil || p.Document.Values["b"] != 42.0 || p.Path != nil || p.Transitions != nil || p.Document.State != "" {
		t.Errorf("preview of a stateless class: %v %s", err, dump(p))
	}
	if _, err := s.Transition(ctx, "Calc", "x", "go", nil); code(err) != CodeUnknownDocument {
		t.Errorf("transition on a stateless class: %v", err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name = 'Calc_states'").Scan(&n)
	if n != 0 {
		t.Error("a stateless class has a _states view")
	}
	// A class with documents keeps its states block.
	defineApproval(t, s)
	if _, err := s.Create(ctx, "Approval", Values{"amount": 1}); err != nil {
		t.Fatal(err)
	}
	_, err = s.DefineClass(ctx, []byte(`{"name": "Approval", "variables": {"amount": {"type": "number"}, "note": {"type": "text"}, "big": {"formula": "amount > 1000"}, "trace": {"type": "text"}}}`), asSetup)
	if err == nil || !strings.Contains(err.Error(), "not_allowed") {
		t.Errorf("removing the states block of a class with documents: %v", err)
	}
}

func TestMigrateMovesNothing(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	doc, _ := s.Create(ctx, "Approval", Values{"amount": 5000}, As(alice))
	s.Transition(ctx, "Approval", doc.ID, "submit", nil, As(alice))
	doc, err := s.Transition(ctx, "Approval", doc.ID, "approve", nil, As(finance))
	if err != nil || doc.State != "approved" {
		t.Fatalf("approve: %v %s", err, dump(doc))
	}
	// The redefinition lets every approved document book; the migration
	// adopts it and moves nothing, the next commit moves.
	if _, err := s.DefineClass(ctx, []byte(strings.Replace(approval, `"guard": "NOT big"`, `"guard": "TRUE"`, 1)), asSetup); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Migrate(ctx, "Approval", designing(alice)); err != nil || n != 1 {
		t.Fatalf("migrate: %d %v", n, err)
	}
	got, _ := s.Get(ctx, "Approval", doc.ID)
	if got.State != "approved" || got.Version != 4 || got.ClassVersion != 2 {
		t.Errorf("after the migration: %s", dump(got))
	}
	got, err = s.Set(ctx, "Approval", doc.ID, Values{"note": "now"}, As(alice))
	if err != nil || got.State != "booked" || got.Version != 6 {
		t.Errorf("the next commit moves: %v %s", err, dump(got))
	}
}
