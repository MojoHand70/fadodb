package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A principal says what it is and for whom it acts (ADR-0050), and the
// engine writes both on every event. The class below gives each caller a
// step: an assistant drafts for Ann, Ann files, a service books.
const tripClass = `{"name": "Trip",
 "variables": {"owner": {"type": "text", "default": "IF(ACTINGFOR() <> \"\", ACTINGFOR(), USER())"}, "cost": {"type": "number"}},
 "states": {"initial": "draft", "list": ["draft", "filed", "booked", "late"]},
 "transitions": [
   {"name": "file", "from": "draft", "to": "filed"},
   {"name": "book", "from": "filed", "to": "booked"},
   {"from": "booked", "to": "late", "after": "1h"}
 ],
 "rules": [{"on": "transition", "name": "file", "do": [{"action": "create", "class": "Ticket", "values": {"trip": "ID()"}}]}]}`

var (
	annPerson = Principal{ID: "ann", Kind: ActorPerson}
	assistant = Principal{ID: "agent-7", Kind: ActorAgent, For: "ann"}
	loader    = Principal{ID: "loader", Kind: ActorService}
)

func TestActor_OnEveryEvent(t *testing.T) {
	now := &clock{now: start}
	s, _ := openStore(t, Options{Now: now.Now})
	ctx := context.Background()
	for _, def := range []string{
		`{"name": "Ticket", "variables": {"trip": {"type": "text"}}, "states": {"initial": "issued", "list": ["issued"]}}`,
		tripClass,
	} {
		if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	gives(t, s, "agent-7", "ann")
	doc, err := s.Create(ctx, "Trip", Values{"cost": 120}, As(assistant))
	if err != nil || doc.Values["owner"] != "ann" || doc.CreatedBy != "agent-7" {
		t.Fatalf("an assistant drafts for ann: %v %s", err, dump(doc))
	}
	if _, err := s.Set(ctx, "Trip", doc.ID, Values{"cost": 130}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Trip", doc.ID, "file", nil, As(annPerson)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Trip", doc.ID, "book", nil, As(loader)); err != nil {
		t.Fatal(err)
	}
	// The outbox row carries what the caller was, and the listing shows it.
	queued, err := s.Outbox(ctx, OutboxFilter{Class: "Trip", DocID: doc.ID}, asSetup)
	if err != nil || len(queued) != 2 {
		t.Fatalf("the rows the trip queued: %v %s", err, dump(queued))
	}
	if q := queued[0]; q.Action != "create" || q.PrincipalID != "ann" || q.PrincipalKind != ActorPerson || q.PrincipalFor != "" {
		t.Errorf("the row of the create ann caused: %s", dump(q))
	}
	if q := queued[1]; q.Action != ActionTimer || q.PrincipalID != "loader" || q.PrincipalKind != ActorService {
		t.Errorf("the wake-up the service's commit queued: %s", dump(q))
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	// The timer comes: the move is made as the service whose commit set it.
	now.mu.Lock()
	now.now = now.now.Add(2 * time.Hour)
	now.mu.Unlock()
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, "Trip", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		got = append(got, fmt.Sprintf("%s:%s by %q %q for %q", e.Kind, e.Name, e.ActorID, e.ActorKind, e.ActorFor))
	}
	want := []string{
		`created: by "agent-7" "agent" for "ann"`,
		`set: by "" "" for ""`,
		`transition:file by "ann" "person" for ""`,
		`transition:book by "loader" "service" for ""`,
		// The outcome of the action is the event of whoever caused it.
		`action_run:create by "ann" "person" for ""`,
		`transition: by "loader" "service" for ""`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the events of the trip:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// What the rule made is made as ann, a person.
	tickets, err := s.Query(ctx, Filter{Class: "Ticket"})
	if err != nil || len(tickets) != 1 {
		t.Fatalf("tickets: %v %d", err, len(tickets))
	}
	tev, _ := s.Events(ctx, "Ticket", tickets[0].ID)
	if len(tev) != 1 || tev[0].ActorID != "ann" || tev[0].ActorKind != ActorPerson || tev[0].Origin != OriginAction {
		t.Errorf("the event of the ticket the rule made: %s", dump(tev))
	}
	// One statement over a view answers which steps an agent took and for
	// whom, and the view of stays says what moved the document each time.
	rows, err := s.SQL(ctx, `SELECT kind, actor_id, actor_for FROM Trip_events WHERE actor_kind = 'agent'`, nil, asSetup)
	if err != nil || len(rows.Rows) != 1 || fmt.Sprint(rows.Rows[0]) != "[created agent-7 ann]" {
		t.Errorf("the steps an agent took: %v %v", err, rows)
	}
	rows, err = s.SQL(ctx, `SELECT state, entered_by, entered_by_kind, entered_for, left_by, left_by_kind FROM Trip_states ORDER BY entry_version`, nil, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	stays := fmt.Sprint(rows.Rows)
	if stays != "[[draft agent-7 agent ann ann person] [filed ann person <nil> loader service] [booked loader service <nil> loader service] [late loader service <nil> <nil> <nil>]]" {
		t.Errorf("the stays of the trip: %s", stays)
	}
}

// A kind that is none of the three is refused, by a command and by a
// read alike, and nothing is written.
func TestActor_UnknownKind(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Memo", Values{"t": "x"}, As(annPerson))
	if err != nil {
		t.Fatal(err)
	}
	robot := As(Principal{ID: "r2", Kind: "robot"})
	for what, call := range map[string]func() error{
		"Create":         func() error { _, err := s.Create(ctx, "Memo", nil, robot); return err },
		"CreateMany":     func() error { _, err := s.CreateMany(ctx, "Memo", []Values{{}}, robot); return err },
		"Set":            func() error { _, err := s.Set(ctx, "Memo", doc.ID, Values{"t": "y"}, robot); return err },
		"Transition":     func() error { _, err := s.Transition(ctx, "Memo", doc.ID, "go", nil, robot); return err },
		"TransitionMany": func() error { _, err := s.TransitionMany(ctx, "Memo", "go", []string{}, nil, robot); return err },
		"Delete":         func() error { return s.Delete(ctx, "Memo", doc.ID, robot) },
		"Preview":        func() error { _, err := s.Preview(ctx, "Memo", doc.ID, nil, "", robot); return err },
		"Get":            func() error { _, err := s.Get(ctx, "Memo", doc.ID, robot); return err },
		"Version":        func() error { _, err := s.Version(ctx, "Memo", doc.ID, 1, robot); return err },
		"History":        func() error { _, err := s.History(ctx, "Memo", doc.ID, robot); return err },
		"Events":         func() error { _, err := s.Events(ctx, "Memo", doc.ID, robot); return err },
		"Transitions":    func() error { _, err := s.Transitions(ctx, "Memo", doc.ID, robot); return err },
		"Query":          func() error { _, err := s.Query(ctx, Filter{Class: "Memo"}, robot); return err },
		"Count":          func() error { _, err := s.Count(ctx, Filter{Class: "Memo"}, robot); return err },
		"Due":            func() error { _, err := s.Due(ctx, "Memo", robot); return err },
		"OpenFile":       func() error { _, _, err := s.OpenFile(ctx, "Memo", doc.ID, "sha256:x", robot); return err },
	} {
		err := call()
		if code(err) != CodeBadValue || !strings.Contains(err.Error(), `"robot"`) {
			t.Errorf("%s as a robot: %v", what, err)
		}
	}
	if ev, _ := s.Events(ctx, "Memo", doc.ID); len(ev) != 1 || ev[0].ActorKind != ActorPerson {
		t.Errorf("the events after the refusals: %s", dump(ev))
	}
}

// The event an action receives says what its actor was and for whom it
// acted (ADR-0050, item 4), as the published schema has it.
func TestActor_TheDeliveredEvent(t *testing.T) {
	got := map[string]*DeliveredEvent{}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"audit.note": func(_ context.Context, call *ActionCall) (Values, error) {
		got[call.Event.DocID] = call.Event
		return nil, nil
	}}})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "audit.note"}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]},
 "rules": [{"on": "enter", "state": "a", "do": [{"action": "audit.note"}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	gives(t, s, "agent-7", "ann")
	first, err := s.Create(ctx, "Memo", Values{"t": "x"}, As(assistant))
	if err != nil {
		t.Fatal(err)
	}
	// Somebody else's command comes between the commit and its action.
	second, err := s.Create(ctx, "Memo", Values{"t": "y"}, As(annPerson))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	if e := got[first.ID]; e == nil || e.ActorID != "agent-7" || e.ActorKind != ActorAgent || e.ActorFor != "ann" {
		t.Errorf("the event the first action received: %s", dump(e))
	}
	if e := got[second.ID]; e == nil || e.ActorID != "ann" || e.ActorKind != ActorPerson || e.ActorFor != "" {
		t.Errorf("the event the second action received: %s", dump(e))
	}
	// The outcome of each action is an event of whoever caused it, with
	// what they were, whatever command the writer ran last.
	for id, want := range map[string]string{first.ID: "agent-7 agent ann", second.ID: "ann person "} {
		events, err := s.Events(ctx, "Memo", id)
		if err != nil || len(events) != 2 || events[1].Kind != EventActionRun {
			t.Fatalf("the events of a memo: %v %s", err, dump(events))
		}
		if e := events[1]; e.ActorID+" "+e.ActorKind+" "+e.ActorFor != want {
			t.Errorf("the outcome of the action: %s, want %s", dump(e), want)
		}
	}
}
