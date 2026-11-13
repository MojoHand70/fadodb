package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The wake-up (ADR-0047, items 10 to 12): the rows a commit queues for
// the timers of the stay it leaves a document in, and what the engine
// does when one is due.

const wakeLog = `{"name": "Log", "variables": {"of": {"type": "text"}, "by": {"type": "text"}}, "states": {"initial": "kept", "list": ["kept"]}}`

// wakeTicket has a reminder with a guard beside it, a deadline the
// document holds, a transition back into open that a person takes, and
// a rule that makes a Log of every entry into open.
const wakeTicket = `{"name": "Ticket", "variables": {
  "note": {"type": "text"}, "limit": {"type": "date"}, "pings": {"type": "number", "default": "0"}, "by": {"type": "text"}, "quiet": {"type": "bool"}},
 "states": {"initial": "open", "list": ["open", "late", "shut"], "final": ["shut"]},
 "transitions": [
   {"name": "ping", "from": "open", "to": "open", "after": "1h", "guard": "NOT quiet"},
   {"from": "open", "to": "late", "at": "limit"},
   {"name": "shut", "from": ["open", "late"], "to": "shut"},
   {"name": "bump", "from": "open", "to": "open"}],
 "rules": [
   {"on": "transition", "name": "ping", "do": [{"action": "set", "values": {"pings": "pings + 1", "by": "USER()"}}]},
   {"on": "enter", "state": "open", "do": [{"action": "create", "class": "Log", "values": {"of": "ID()", "by": "USER()"}}]}]}`

func openWake(t *testing.T) (*Store, *still) {
	t.Helper()
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	for _, def := range []string{wakeLog, wakeTicket} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	return s, cl
}

func (c *still) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// wakes lists the wake-ups a document queued, each as its transition's
// path, its status, who it acts as and when it is due, in seconds after
// base.
func wakes(t *testing.T, s *Store, class, id string, base time.Time) []string {
	t.Helper()
	rows, err := s.Outbox(context.Background(), OutboxFilter{Class: class, DocID: id}, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		if r.Action == ActionTimer {
			out = append(out, fmt.Sprintf("%s %s %s +%d", r.Rule, r.Status, r.PrincipalID, r.DueAt-base.Unix()))
		}
	}
	return out
}

func drain(t *testing.T, s *Store, want int) {
	t.Helper()
	n, err := s.Drain(context.Background(), asSetup)
	if err != nil || n != want {
		t.Fatalf("the drain attempted %d rows, %v; want %d", n, err, want)
	}
}

func kinds(t *testing.T, s *Store, class, id string) string {
	t.Helper()
	events, err := s.Events(context.Background(), class, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		k := e.Kind
		if e.Name != "" {
			k += ":" + e.Name
		}
		out = append(out, k)
	}
	return strings.Join(out, " ")
}

// What a commit queues: a wake-up for each timed transition leaving the
// state the document is left in whose moment is still to come, after the
// actions of the same commit, as the person who made it.
func TestWake_Rows(t *testing.T) {
	s, cl := openWake(t)
	ctx := context.Background()
	hour := int64(3600)
	limit := dueDay.AddDate(0, 0, 3).Format("2006-01-02")
	at := dueDay.AddDate(0, 0, 3).Truncate(24*time.Hour).Unix() - dueDay.Unix()
	same := func(what, id string, want ...string) {
		t.Helper()
		if want == nil {
			want = []string{}
		}
		if got := wakes(t, s, "Ticket", id, dueDay); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the wake-ups are\n  %q\nwant\n  %q", what, got, want)
		}
	}
	// A preview queues nothing.
	if _, err := s.Preview(ctx, "Ticket", "", Values{"limit": limit}, "", As(ann)); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Outbox(ctx, OutboxFilter{}, asSetup); len(rows) != 0 {
		t.Fatalf("a preview queued %s", dump(rows))
	}
	// No deadline: the at is blank and never comes; the reminder waits.
	doc, err := s.Create(ctx, "Ticket", nil, WithID("T-1"), As(ann))
	if err != nil {
		t.Fatal(err)
	}
	same("a create", "T-1", fmt.Sprintf("/transitions/0 pending ann +%d", hour))
	// The row: after the action the same commit queued, keyed by the
	// commit's event, with the version it made and what it waits for.
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "T-1"}, asSetup)
	events, _ := s.Events(ctx, "Ticket", "T-1")
	if len(rows) != 2 || rows[0].Action != "create" || rows[0].Key != events[0].ID+":0" || rows[1].Key != events[0].ID+":1" ||
		rows[1].Version != doc.Version || rows[1].Depth != 0 || rows[1].Attempts != 0 || rows[1].CreatedAt != dueDay.Unix() ||
		string(rows[1].Args) != `{"state":"open","to":"open","name":"ping"}` {
		t.Errorf("the rows of the create: %s", dump(rows))
	}
	// A guard beside the timer that does not hold now is asked when the
	// time comes: the wake-up is queued all the same.
	if _, err := s.Create(ctx, "Ticket", Values{"quiet": true, "limit": limit}, WithID("T-2"), As(bob)); err != nil {
		t.Fatal(err)
	}
	same("a quiet ticket with a deadline", "T-2", fmt.Sprintf("/transitions/0 pending bob +%d", hour), fmt.Sprintf("/transitions/1 pending bob +%d", at))
	// A deadline that has passed fires in the create's own chain: the
	// document is left in late, which no timer leaves.
	if doc, err := s.Create(ctx, "Ticket", Values{"limit": day(-1)}, WithID("T-3"), As(ann)); err != nil || doc.State != "late" {
		t.Fatalf("a ticket past its deadline: %+v %v", doc, err)
	}
	same("a ticket created late", "T-3")
	// A set that changes nothing, and one that changes what no at reads,
	// queue nothing.
	for _, vals := range []Values{{"note": ""}, {"note": "called"}, {"pings": 0}} {
		if _, err := s.Set(ctx, "Ticket", "T-1", vals, As(bob)); err != nil {
			t.Fatal(err)
		}
	}
	same("sets beside the timers", "T-1", fmt.Sprintf("/transitions/0 pending ann +%d", hour))
	// A date where there was none, as bob: his commit queues the wake-up.
	cl.add(10 * time.Minute)
	if _, err := s.Set(ctx, "Ticket", "T-1", Values{"limit": limit}, As(bob)); err != nil {
		t.Fatal(err)
	}
	same("a deadline given", "T-1", fmt.Sprintf("/transitions/0 pending ann +%d", hour), fmt.Sprintf("/transitions/1 pending bob +%d", at))
	// The same date again changes nothing; another date is another row;
	// the reminder, which counts from the entry, is never queued again.
	if _, err := s.Set(ctx, "Ticket", "T-1", Values{"limit": limit}, As(ann)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Ticket", "T-1", Values{"limit": day(5)}, As(ann)); err != nil {
		t.Fatal(err)
	}
	same("a deadline moved", "T-1", fmt.Sprintf("/transitions/0 pending ann +%d", hour), fmt.Sprintf("/transitions/1 pending bob +%d", at),
		fmt.Sprintf("/transitions/1 pending ann +%d", at+2*86400))
	// A refresh and a reset of another variable queue nothing either.
	if _, err := s.Refresh(ctx, "Ticket", "T-1", nil, As(bob)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reset(ctx, "Ticket", "T-1", []string{"note"}, As(bob)); err != nil {
		t.Fatal(err)
	}
	if got := wakes(t, s, "Ticket", "T-1", dueDay); len(got) != 3 {
		t.Errorf("a refresh or a reset queued a wake-up: %q", got)
	}
	// A date set into the past fires in the commit that sets it, and a
	// reset of the date leaves a blank, which never comes: no row.
	if doc, err := s.Set(ctx, "Ticket", "T-2", Values{"limit": day(-2)}, As(ann)); err != nil || doc.State != "late" {
		t.Fatalf("a deadline set into the past: %+v %v", doc, err)
	}
	if _, err := s.Reset(ctx, "Ticket", "T-1", []string{"limit"}, As(bob)); err != nil {
		t.Fatal(err)
	}
	if got := wakes(t, s, "Ticket", "T-2", dueDay); len(got) != 2 {
		t.Errorf("a date set into the past queued a wake-up: %q", got)
	}
	if got := wakes(t, s, "Ticket", "T-1", dueDay); len(got) != 3 {
		t.Errorf("a date reset queued a wake-up: %q", got)
	}
	// A move into a terminal state queues nothing.
	if _, err := s.Transition(ctx, "Ticket", "T-1", "shut", nil, As(ann)); err != nil {
		t.Fatal(err)
	}
	if got := wakes(t, s, "Ticket", "T-1", dueDay); len(got) != 3 {
		t.Errorf("a terminal state queued a wake-up: %q", got)
	}
	// A moment that has come while the guard beside it does not hold
	// gets no wake-up: the commit's own chain asked, and no moment is
	// left to wait for. The commit that makes the guard hold moves it.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Held", "variables": {"limit": {"type": "date"}, "ready": {"type": "bool"}},
	  "states": {"initial": "open", "list": ["open", "late"]},
	  "transitions": [{"from": "open", "to": "late", "at": "limit", "guard": "ready"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if doc, err := s.Create(ctx, "Held", Values{"limit": day(-1)}, WithID("H-1"), As(ann)); err != nil || doc.State != "open" {
		t.Fatalf("a held document: %+v %v", doc, err)
	}
	if _, err := s.Set(ctx, "Held", "H-1", Values{"limit": day(-2)}, As(ann)); err != nil {
		t.Fatal(err)
	}
	if got := wakes(t, s, "Held", "H-1", dueDay); len(got) != 0 {
		t.Errorf("a moment that has come got a wake-up: %q", got)
	}
	if doc, err := s.Set(ctx, "Held", "H-1", Values{"ready": true}, As(bob)); err != nil || doc.State != "late" {
		t.Errorf("the guard comes to hold: %+v %v", doc, err)
	}
	// The row keeps the whole principal: the id, the groups and the
	// system groups, as every row of the outbox does.
	night := Principal{ID: "nora", Groups: []string{"ops", "night"}, System: []string{GroupContentAdministrators}}
	if _, err := s.Create(ctx, "Ticket", nil, WithID("T-9"), As(night)); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"T-9": `nora {"groups":["ops","night"],"system":["content-administrators"]}`, "T-2": `bob ["finance"]`} {
		var who, groups string
		if err := s.db.QueryRowContext(ctx, `SELECT principal_id, principal_groups FROM outbox WHERE doc_id = ? AND action = ? ORDER BY event_id, idx LIMIT 1`, id, ActionTimer).Scan(&who, &groups); err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%s %s", who, groups); got != want {
			t.Errorf("the principal of the wake-up of %s: %s, want %s", id, got, want)
		}
	}
}

// The move a wake-up makes, as evidence: the event of the transition
// names who the engine acted as, the event that queued the wake-up as
// its cause, and the row's key; the row names the event as its outcome;
// no other event is written. What the move queues belongs to it.
func TestWake_Evidence(t *testing.T) {
	s, cl := openWake(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "Ticket", Values{"limit": day(1)}, WithID("T-1"), As(ann), BusinessDate(dueDay.AddDate(0, 0, -40))); err != nil {
		t.Fatal(err)
	}
	// The create's Log is made; nothing else is due.
	drain(t, s, 1)
	before := kinds(t, s, "Ticket", "T-1")
	// Thirty hours on, past midnight, both timers have come: one touch
	// fires the reminder and, from the stay it begins, the deadline.
	cl.add(30 * time.Hour)
	// The reminder's row runs and moves the ticket on; the Log of the
	// entry it made on the way follows; the deadline's row finds the
	// ticket in another state.
	drain(t, s, 3)
	doc, err := s.Get(ctx, "Ticket", "T-1")
	if err != nil || doc.State != "late" || doc.Version != 3 || doc.Values["pings"] != float64(1) || doc.Values["by"] != "ann" ||
		doc.UpdatedBy != "ann" || doc.StateEnteredBy != "ann" || doc.StateEnteredAt != cl.Now().Unix() {
		t.Fatalf("after the wake-up: %+v %v", doc, err)
	}
	if got := kinds(t, s, "Ticket", "T-1"); got != before+" transition:ping transition action_run:create" {
		t.Fatalf("the events: %s", got)
	}
	events, _ := s.Events(ctx, "Ticket", "T-1")
	created, ping, late := events[0], events[len(events)-3], events[len(events)-2]
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "T-1"}, asSetup)
	var row, other QueuedAction
	for _, r := range rows {
		switch r.Rule {
		case "/transitions/0":
			row = r
		case "/transitions/1":
			other = r
		}
	}
	today := cl.Now().Format("2006-01-02")
	if ping.Origin != OriginTimer || ping.ActorID != "ann" || ping.CauseEventID != created.ID || ping.CommandKey != outboxKeyPrefix+row.Key ||
		ping.BeforeVersion != 1 || ping.AfterVersion != 2 || ping.FromState != "open" || ping.ToState != "open" || ping.BusinessDate != today {
		t.Errorf("the event of the reminder: %+v", ping)
	}
	// The event that carries the key carries the hash of its command, as
	// every keyed event does (ADR-0026).
	var hash, none *string
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT command_hash FROM events WHERE event_id = ?), (SELECT command_hash FROM events WHERE event_id = ?)`, ping.ID, late.ID).Scan(&hash, &none); err != nil || hash == nil || len(*hash) != 64 || none != nil {
		t.Errorf("the hashes of the two events: %v %v %v", hash, none, err)
	}
	// The second transition of the chain is caused by the first, as in
	// any commit, and has no key of its own.
	if late.Origin != OriginTimer || late.Name != "" || late.ActorID != "ann" || late.CauseEventID != ping.ID || late.CommandKey != "" || late.AfterVersion != 3 || late.BusinessDate != today {
		t.Errorf("the event of the deadline: %+v", late)
	}
	if row.Status != ActionDone || row.Attempts != 1 || row.OutcomeEvent != ping.ID || row.FinishedAt != cl.Now().Unix() || row.ErrorCode != "" {
		t.Errorf("the row that fired: %s", dump(row))
	}
	// The row that found nothing to do is done and names no event.
	if other.Status != ActionDone || other.Attempts != 1 || other.OutcomeEvent != "" || other.FinishedAt != cl.Now().Unix() {
		t.Errorf("the row that did nothing: %s", dump(other))
	}
	// The Log the reminder's entry asked for was queued by the move, one
	// action deep, and made as ann; the first Log was the create's.
	logs, err := s.Query(ctx, Filter{Class: "Log", Order: []string{"created_at"}})
	if err != nil || len(logs) != 2 || logs[1].Values["by"] != "ann" || logs[1].CreatedBy != "ann" {
		t.Fatalf("the logs: %s %v", dump(logs), err)
	}
	for _, r := range rows {
		if r.Action == "create" && r.Key == ping.ID+":0" && (r.Depth != 1 || r.PrincipalID != "ann" || r.Status != ActionDone) {
			t.Errorf("the action the move queued: %s", dump(r))
		}
	}
	// The stays say the same: who, by which transition, and how.
	got, err := readStays(s, "Ticket", "T-1")
	if err != nil {
		t.Fatal(err)
	}
	want := `[["open","ann",null,null,"command","ann","ping","timer"],["open","ann","open","ping","timer","ann",null,"timer"],["late","ann","open",null,"timer",null,null,null]]`
	if !sameWire(got, []byte(want)) {
		t.Errorf("the stays: %s", dump(got))
	}
}

// A wake-up belongs to one stay. One whose stay has ended does nothing,
// though the document is in the same state again, and though the stay
// after it began in the same second.
func TestWake_ItsStay(t *testing.T) {
	s, cl := openWake(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "Ticket", nil, WithID("T-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	// Bob reminds by hand in the second the ticket was created in: a new
	// stay in the same state, entered at the same second.
	if _, err := s.Transition(ctx, "Ticket", "T-1", "ping", nil, As(bob)); err != nil {
		t.Fatal(err)
	}
	// And he gives a date in that second too: a wake-up queued by a set,
	// in the stay that is current.
	if _, err := s.Set(ctx, "Ticket", "T-1", Values{"limit": day(1)}, As(bob)); err != nil {
		t.Fatal(err)
	}
	drain(t, s, 2) // the two Logs
	cl.add(time.Hour)
	// Both reminders are due at once. Ann's, of the stay that ended,
	// does nothing; bob's, of the stay that is, fires as bob.
	drain(t, s, 3)
	doc, _ := s.Get(ctx, "Ticket", "T-1")
	if doc.Values["pings"] != float64(2) || doc.Values["by"] != "bob" || doc.Version != 4 {
		t.Fatalf("after the hour: %+v", doc)
	}
	if got := kinds(t, s, "Ticket", "T-1"); strings.Count(got, "transition:ping") != 2 {
		t.Errorf("the events: %s", got)
	}
	// The date's wake-up was queued in the stay that has ended since:
	// on the day it does nothing, and the one of the stay that is fires.
	if got := wakes(t, s, "Ticket", "T-1", dueDay); len(got) != 5 {
		t.Fatalf("the wake-ups: %q", got)
	}
	cl.add(11 * time.Hour)
	n, err := s.Drain(ctx, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Get(ctx, "Ticket", "T-1")
	events, _ := s.Events(ctx, "Ticket", "T-1")
	moves := 0
	for _, e := range events {
		if e.Kind == EventTransition && e.ToState == "late" {
			moves++
		}
	}
	if doc.State != "late" || moves != 1 || n < 2 {
		t.Errorf("on the day: %+v after %d rows, %d moves to late", doc, n, moves)
	}
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "T-1"}, asSetup)
	for _, r := range rows {
		if r.Status != ActionDone {
			t.Errorf("a row is left: %s", dump(r))
		}
	}
	// A stay that ended some minutes before, and whose wake-up is due
	// at the same moment as the one of the stay that is: ann creates a
	// ticket with a deadline, bob bumps it ten minutes later. On the day
	// ann's wake-up does nothing, and the move is bob's.
	if _, err := s.Create(ctx, "Ticket", Values{"quiet": true, "limit": day(3)}, WithID("T-4"), As(ann)); err != nil {
		t.Fatal(err)
	}
	cl.add(10 * time.Minute)
	if _, err := s.Transition(ctx, "Ticket", "T-4", "bump", nil, As(bob)); err != nil {
		t.Fatal(err)
	}
	cl.days(3)
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Get(ctx, "Ticket", "T-4")
	events, _ = s.Events(ctx, "Ticket", "T-4")
	var bump, moved *Event
	for _, e := range events {
		switch {
		case e.Name == "bump":
			bump = e
		case e.ToState == "late":
			moved = e
		}
	}
	if doc.State != "late" || doc.StateEnteredBy != "bob" || moved == nil || moved.ActorID != "bob" || moved.CauseEventID != bump.ID {
		t.Fatalf("the ticket bob bumped: %+v, moved by %+v", doc, moved)
	}
	rows, _ = s.Outbox(ctx, OutboxFilter{DocID: "T-4"}, asSetup)
	fired := 0
	for _, r := range rows {
		if r.Action != ActionTimer {
			continue
		}
		if r.OutcomeEvent != "" {
			fired++
			if r.PrincipalID != "bob" || r.OutcomeEvent != moved.ID || moved.CommandKey != outboxKeyPrefix+r.Key {
				t.Errorf("the wake-up that moved the ticket: %s", dump(r))
			}
		}
		if r.Status != ActionDone {
			t.Errorf("a wake-up is left: %s", dump(r))
		}
	}
	if fired != 1 {
		t.Errorf("%d wake-ups moved the ticket", fired)
	}
	// A document that is gone: its wake-ups do nothing, and say nothing.
	if _, err := s.Create(ctx, "Ticket", Values{"quiet": true}, WithID("T-2"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "Ticket", "T-2", As(ann)); err != nil {
		t.Fatal(err)
	}
	cl.add(2 * time.Hour)
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Outbox(ctx, OutboxFilter{DocID: "T-2"}, asSetup)
	for _, r := range rows {
		if r.Status != ActionDone || r.ErrorCode != "" {
			t.Errorf("a row of the deleted ticket: %s", dump(r))
		}
	}
	if got := kinds(t, s, "Ticket", "T-2"); got != "created deleted action_run:create" {
		t.Errorf("the events of the deleted ticket: %s", got)
	}
}

// A reminder repeats for as long as it is left to: every wake-up stands
// at depth 0, so time never runs a chain of actions into its bound, and
// what each move queues is one action deep.
func TestWake_Repeats(t *testing.T) {
	s, cl := openWake(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, "Ticket", nil, WithID("T-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	// A change of the ticket in the stay is no move: the wake-up of the
	// stay still fires.
	if _, err := s.Set(ctx, "Ticket", "T-1", Values{"note": "seen"}, As(bob)); err != nil {
		t.Fatal(err)
	}
	drain(t, s, 1)
	const times = 2*MaxActionDepth + 3
	for i := 0; i < times; i++ {
		cl.add(time.Hour)
		drain(t, s, 2) // the reminder, and the Log of the entry it makes
	}
	doc, _ := s.Get(ctx, "Ticket", "T-1")
	if doc.Values["pings"] != float64(times) || doc.Version != int64(times)+2 || doc.Values["by"] != "ann" {
		t.Fatalf("after %d hours: %+v", times, doc)
	}
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "T-1", Limit: 10000}, asSetup)
	for _, r := range rows {
		want := 0
		if r.Action == "create" && r.Version > 1 {
			want = 1
		}
		if r.Depth != want || r.Status == ActionDead {
			t.Fatalf("the row %s", dump(r))
		}
	}
	// The wake-up of the stay that is was queued by the last reminder:
	// it is keyed by that move's event, after the action the move queued.
	events, _ := s.Events(ctx, "Ticket", "T-1")
	var last *Event
	for _, e := range events {
		if e.Name == "ping" {
			last = e
		}
	}
	if next := rows[len(rows)-1]; next.Action != ActionTimer || next.Status != ActionPending || next.Key != last.ID+":1" || next.Version != doc.Version || rows[len(rows)-2].Key != last.ID+":0" {
		t.Errorf("the wake-up of the last stay: %s after the event %s", dump(next), last.ID)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Log"}); n != times+1 {
		t.Errorf("%d logs", n)
	}
}

// A wake-up whose move is refused is dead, like any action the engine
// refuses: the row says why, the document gets the event, and nothing
// has moved. It is still due, and a retry asks again.
func TestWake_Dead(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	def := `{"name": "Fuse", "variables": {"n": {"type": "number", "default": "0"}, "d": {"type": "number", "default": "0"}},
	  "states": {"initial": "set", "list": ["set", "blown"]},
	  "transitions": [{"name": "blow", "from": "set", "to": "blown", "after": "1m"}],
	  "rules": [{"on": "transition", "name": "blow", "do": [{"action": "set", "values": {"n": "10 / d"}}]}]}`
	if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Fuse", nil, WithID("F-1"), As(ann), BusinessDate(dueDay.AddDate(0, 0, -40))); err != nil {
		t.Fatal(err)
	}
	cl.add(13 * time.Hour)
	drain(t, s, 1)
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "F-1"}, asSetup)
	if len(rows) != 1 || rows[0].Status != ActionDead || rows[0].Attempts != 1 || rows[0].ErrorCode != "div_by_zero" || rows[0].OutcomeEvent == "" {
		t.Fatalf("the row: %s", dump(rows))
	}
	doc, _ := s.Get(ctx, "Fuse", "F-1")
	if doc.State != "set" || doc.Version != 1 {
		t.Errorf("the document moved: %+v", doc)
	}
	events, _ := s.Events(ctx, "Fuse", "F-1")
	last := events[len(events)-1]
	if len(events) != 2 || last.Kind != EventActionFailed || last.Name != ActionTimer || last.Code != "div_by_zero" || last.ID != rows[0].OutcomeEvent || last.ActorID != "ann" || last.CauseEventID != events[0].ID ||
		// The event of the failure is of the day the timer came, not of
		// the day the fuse was set.
		last.BusinessDate != cl.Now().Format("2006-01-02") || events[0].BusinessDate == last.BusinessDate {
		t.Errorf("the events: %s", dump(events))
	}
	// It is still due: the host program sees it, and may touch it.
	if due, err := s.Due(ctx, "Fuse"); err != nil || len(due) != 1 || due[0].To != "blown" {
		t.Errorf("due: %+v %v", due, err)
	}
	// A commit that touches the fuse fires the timer in its chain, and
	// is refused for the same reason until what refused the move is
	// mended; the set that mends it makes the move.
	if _, err := s.Set(ctx, "Fuse", "F-1", Values{"n": 3}, As(bob)); code(err) != "div_by_zero" {
		t.Fatalf("a set of the fuse as it is: %v", err)
	}
	if _, err := s.Set(ctx, "Fuse", "F-1", Values{"d": 5}, As(bob)); err != nil {
		t.Fatalf("the mending set: %v", err)
	}
	doc, _ = s.Get(ctx, "Fuse", "F-1")
	if doc.State != "blown" || doc.Values["n"] != float64(2) {
		t.Fatalf("after the mending set, whose chain fires the timer: %+v", doc)
	}
	// A retry of the dead row asks again: the fuse has moved on, and
	// the row is done with nothing more to do.
	if err := s.RetryAction(ctx, rows[0].Key, asSetup); err != nil {
		t.Fatal(err)
	}
	drain(t, s, 1)
	rows, _ = s.Outbox(ctx, OutboxFilter{DocID: "F-1"}, asSetup)
	if rows[0].Status != ActionDone || rows[0].ErrorCode != "" || rows[0].OutcomeEvent != "" {
		t.Errorf("the row after the retry: %s", dump(rows[0]))
	}
}

// A wake-up stands outside the order of its document's actions. One that
// waits for its moment holds no action back, and an action that waits
// for another attempt holds no wake-up back.
func TestWake_OrderOfTheDocument(t *testing.T) {
	cl := &still{now: dueDay}
	rec := &recorder{do: func(_ context.Context, call *ActionCall) (Values, error) {
		if call.Attempt < 2 {
			return nil, errors.New("not yet")
		}
		return nil, nil
	}}
	s, _ := openStore(t, Options{Now: cl.Now, Actions: map[string]ActionFunc{"audit.ping": rec.fn}})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(auditPing), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Job", "variables": {"note": {"type": "text"}, "runs": {"type": "number", "default": "0"}},
	  "states": {"initial": "idle", "list": ["idle"]},
	  "transitions": [{"name": "run", "from": "idle", "to": "idle", "after": "1m"}],
	  "rules": [
	    {"on": "transition", "name": "run", "do": [{"action": "set", "values": {"runs": "runs + 1"}}]},
	    {"on": "values_change", "variables": ["note"], "do": [{"action": "audit.ping", "args": {"note": "note"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Job", nil, WithID("J-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	// The wake-up waits a minute. An action queued behind it runs now.
	if _, err := s.Set(ctx, "Job", "J-1", Values{"note": "a"}, As(ann)); err != nil {
		t.Fatal(err)
	}
	drain(t, s, 1)
	if calls := rec.take(); len(calls) != 1 || calls[0].Attempt != 1 {
		t.Fatalf("the action behind a waiting wake-up: %s", dump(calls))
	}
	// The action got no answer and waits for its second attempt, a second
	// from now by the still clock: it is pending, before the wake-up of
	// the next stay in the order of the keys. Move the clock to the
	// wake-up's minute but keep the action waiting: make it fail anew.
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "J-1"}, asSetup)
	if len(rows) != 2 || rows[1].Action != "audit.ping" || rows[1].Status != ActionPending || rows[1].Attempts != 1 {
		t.Fatalf("the rows: %s", dump(rows))
	}
	rec.mu.Lock()
	rec.do = func(context.Context, *ActionCall) (Values, error) { return nil, errors.New("still not") }
	rec.mu.Unlock()
	cl.add(time.Minute)
	// Both are due: the action is asked again and waits again, with a
	// longer wait; the wake-up fires all the same.
	drain(t, s, 2)
	doc, _ := s.Get(ctx, "Job", "J-1")
	if doc.Values["runs"] != float64(1) {
		t.Fatalf("the wake-up waited behind the action: %+v", doc)
	}
	rows, _ = s.Outbox(ctx, OutboxFilter{DocID: "J-1"}, asSetup)
	if rows[1].Status != ActionPending || rows[1].Attempts != 2 || rows[0].Status != ActionDone {
		t.Errorf("the rows: %s", dump(rows))
	}
	// And the wake-up of the new stay, queued after the waiting action,
	// fires a minute on while the action still waits.
	cl.add(time.Minute)
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Get(ctx, "Job", "J-1")
	if doc.Values["runs"] != float64(2) {
		t.Errorf("the second wake-up waited behind the action: %+v", doc)
	}
}

// A class that is defined again. A wake-up queued under the version
// before fires under the one that is, and the transition it fires adopts
// the document. A class that gains a timer queues its wake-up when a
// commit next touches a document, and Migrate touches them all.
func TestWake_ClassVersions(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	def := `{"name": "Lead", "variables": {"n": {"type": "number"}%s},
	  "states": {"initial": "open", "list": ["open", "stale", "cold"]},
	  "transitions": [{"name": "drop", "from": "open", "to": "cold"}%s]}`
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, "", "")), asSetup); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.Create(ctx, "Lead", Values{"n": i}, WithID(fmt.Sprintf("L-%d", i)), As(ann)); err != nil {
			t.Fatal(err)
		}
	}
	if rows, _ := s.Outbox(ctx, OutboxFilter{}, asSetup); len(rows) != 0 {
		t.Fatalf("a class without a timer queued %s", dump(rows))
	}
	// The second version says a lead goes stale after two days, and
	// computes a label.
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, `, "label": {"formula": "\"lead \" & TEXT(n)"}`, `, {"from": "open", "to": "stale", "after": "2d"}`)), asSetup); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.Outbox(ctx, OutboxFilter{}, asSetup); len(rows) != 0 {
		t.Fatalf("the definition queued %s", dump(rows))
	}
	// A commit that touches a lead adopts it and queues its wake-up, as
	// who made the commit; the stay counts from its entry all the same.
	cl.days(1)
	if _, err := s.Set(ctx, "Lead", "L-0", Values{"n": 10}, As(bob)); err != nil {
		t.Fatal(err)
	}
	if got := wakes(t, s, "Lead", "L-0", dueDay); !reflect.DeepEqual(got, []string{fmt.Sprintf("/transitions/1 pending bob +%d", 2*86400)}) {
		t.Errorf("the adopted lead: %q", got)
	}
	// A second commit of the same lead queues nothing more.
	if _, err := s.Set(ctx, "Lead", "L-0", Values{"n": 11}, As(bob)); err != nil {
		t.Fatal(err)
	}
	if got := wakes(t, s, "Lead", "L-0", dueDay); len(got) != 1 {
		t.Errorf("the lead touched twice: %q", got)
	}
	// Migrate brings the others up, and their wake-ups with them.
	if _, err := s.Migrate(ctx, "Lead", designing(ann)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 4; i++ {
		id := fmt.Sprintf("L-%d", i)
		if got := wakes(t, s, "Lead", id, dueDay); !reflect.DeepEqual(got, []string{fmt.Sprintf("/transitions/1 pending ann +%d", 2*86400)}) {
			t.Errorf("the migrated lead %s: %q", id, got)
		}
	}
	// A third version, defined while the wake-ups wait: a variable more.
	// The wake-up of a lead no commit has touched since fires under it:
	// the move adopts the lead, in the one version it writes.
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, `, "label": {"formula": "\"lead \" & TEXT(n)"}, "twice": {"formula": "n * 2"}`, `, {"from": "open", "to": "stale", "after": "2d"}`)), asSetup); err != nil {
		t.Fatal(err)
	}
	cl.days(1)
	drain(t, s, 4)
	doc, err := s.Get(ctx, "Lead", "L-3")
	if err != nil || doc.State != "stale" || doc.ClassVersion != 3 || doc.Version != 3 || doc.Values["twice"] != float64(6) || doc.Values["label"] != "lead 3" {
		t.Fatalf("the lead the wake-up adopted: %+v %v", doc, err)
	}
	if got := kinds(t, s, "Lead", "L-3"); got != "created migrated transition" {
		t.Errorf("its events: %s", got)
	}
	// A fourth version drops the timer. A wake-up queued for it finds
	// nothing that fires, and does nothing.
	if _, err := s.Create(ctx, "Lead", Values{"n": 7}, WithID("L-7"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, `, "label": {"formula": "\"lead \" & TEXT(n)"}, "twice": {"formula": "n * 2"}`, "")), asSetup); err != nil {
		t.Fatal(err)
	}
	cl.days(3)
	drain(t, s, 1)
	doc, _ = s.Get(ctx, "Lead", "L-7")
	rows, _ := s.Outbox(ctx, OutboxFilter{DocID: "L-7"}, asSetup)
	if doc.State != "open" || doc.Version != 1 || doc.ClassVersion != 3 || len(rows) != 1 || rows[0].Status != ActionDone || rows[0].OutcomeEvent != "" {
		t.Errorf("the lead whose timer was dropped: %+v, %s", doc, dump(rows))
	}
}

// The worker moves a document by itself when its time comes, on the
// real clock: nothing touches the document but the engine.
func TestWake_TheWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("waits a second for the timer")
	}
	s, err := Open(filepath.Join(t.TempDir(), "wake.fado"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Egg", "variables": {"n": {"type": "number"}},
	  "states": {"initial": "raw", "list": ["raw", "done"]},
	  "transitions": [{"from": "raw", "to": "done", "after": "1s"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Egg", nil, As(ann))
	if err != nil {
		t.Fatal(err)
	}
	due := time.Unix(doc.StateEnteredAt+1, 0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := s.Get(ctx, "Egg", doc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == "done" {
			// The move is made in the second the timer names, and soon
			// after that second began.
			if late := time.Since(due); got.StateEnteredAt != due.Unix() || got.StateEnteredBy != "ann" || late > 900*time.Millisecond {
				t.Errorf("the egg was done at %d, %s after its time; due at %d", got.StateEnteredAt, late, due.Unix())
			}
			break
		}
		if time.Now().After(deadline) {
			rows, _ := s.Outbox(ctx, OutboxFilter{DocID: doc.ID}, asSetup)
			t.Fatalf("the worker left the egg raw: %s", dump(rows))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := kinds(t, s, "Egg", doc.ID); got != "created transition" {
		t.Errorf("the events: %s", got)
	}
}

// A commit that queues a wake-up leaves the worker asleep when the
// moment is later than the second its sleep ends at, and wakes it when
// the moment is earlier; a commit that queues an action always wakes it.
func TestWake_TellsTheWorkerWhenItMatters(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tell.fado"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, def := range []string{wakeLog,
		`{"name": "Slow", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"from": "a", "to": "b", "after": "1d"}]}`,
		`{"name": "Fast", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"from": "a", "to": "b", "after": "30s"}]}`,
		`{"name": "Loud", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a"]},
		  "rules": [{"on": "create", "do": [{"action": "create", "class": "Log", "values": {"of": "ID()"}}]}]}`,
		// A far timer before a near one, in the order of the class.
		`{"name": "Both", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]},
		  "transitions": [{"from": "a", "to": "b", "after": "1d"}, {"from": "a", "to": "b", "after": "20s"}]}`,
		`{"name": "Many", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"from": "a", "to": "b", "after": "12s"}]}`,
		`{"name": "Old", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b"}]}`,
	} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	// settled waits for the worker to sleep, and returns its looks so far.
	settled := func() int64 {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for last := int64(-1); ; {
			n := s.looks.Load()
			if n == last && s.sleepUntil.Load() != 0 {
				return n
			}
			if last = n; time.Now().After(deadline) {
				t.Fatal("the worker does not come to rest")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	before := settled()
	if until := s.sleepUntil.Load() - time.Now().Unix(); until < 55 || until > 61 {
		t.Fatalf("with nothing queued the worker sleeps %d seconds", until)
	}
	// Timers a day away: the worker sleeps on, and finds them when its
	// minute ends.
	for i := 0; i < 20; i++ {
		if _, err := s.Create(ctx, "Slow", Values{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
	if n := settled(); n != before {
		t.Errorf("twenty commits with a timer a day away made the worker look %d times", n-before)
	}
	// A timer that comes before the worker's minute ends: it is told, and
	// sleeps until that timer.
	if _, err := s.Create(ctx, "Fast", nil); err != nil {
		t.Fatal(err)
	}
	after := settled()
	if until := s.sleepUntil.Load() - time.Now().Unix(); after == before || until < 25 || until > 30 {
		t.Errorf("a timer thirty seconds away: %d looks more, and the worker sleeps %d seconds", after-before, until)
	}
	// A later one than that leaves it asleep again.
	if _, err := s.Create(ctx, "Slow", nil); err != nil {
		t.Fatal(err)
	}
	if n := settled(); n != after {
		t.Errorf("a timer after the one the worker waits for made it look %d times", n-after)
	}
	// A commit that queues two wake-ups tells the worker of the earlier,
	// whichever comes first in the class.
	sleeps := func(what string, most int64) {
		t.Helper()
		n := settled()
		if until := s.sleepUntil.Load() - time.Now().Unix(); n == after || until > most || until < most-5 {
			t.Errorf("%s: %d looks more, and the worker sleeps %d seconds; want at most %d", what, n-after, until, most)
		}
		after = n
	}
	if _, err := s.Create(ctx, "Both", nil); err != nil {
		t.Fatal(err)
	}
	sleeps("two timers in one commit", 20)
	// A batch tells it too.
	if _, err := s.CreateMany(ctx, "Many", []Values{{"n": 1}, {"n": 2}, {"n": 3}}); err != nil {
		t.Fatal(err)
	}
	sleeps("a batch of documents with a timer", 12)
	// And a migration: the class gains a timer, and Migrate queues the
	// wake-ups of the documents it adopts.
	if _, err := s.Create(ctx, "Old", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Old", "variables": {"n": {"type": "number"}}, "states": {"initial": "a", "list": ["a", "b"]},
	  "transitions": [{"name": "go", "from": "a", "to": "b"}, {"from": "a", "to": "b", "after": "6s"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if n := settled(); n != after {
		t.Errorf("a definition made the worker look %d times", n-after)
	}
	if _, err := s.Migrate(ctx, "Old", asSetup); err != nil {
		t.Fatal(err)
	}
	sleeps("a migration into a class with a timer", 6)
	// An action is due at once.
	if _, err := s.Create(ctx, "Loud", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := s.Count(ctx, Filter{Class: "Log"}); n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the action was not run")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The process is stopped while a wake-up runs, at every step, and the
// file is reopened: the timer has fired exactly once (ADR-0044, item 10,
// for the row of ADR-0047). The process that is stopped is this test
// binary, run again as TestWakeCrashHelper.
func TestWake_SurvivesACrashAtEveryStep(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the test binary four times")
	}
	for _, step := range []string{"queued", "performed", "marked", "committed"} {
		t.Run(step, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "crash.fado")
			cmd := exec.Command(os.Args[0], "-test.run=^TestWakeCrashHelper$")
			cmd.Env = append(os.Environ(), "FADODB_CRASH_DB="+path, "FADODB_CRASH_AT="+step)
			out, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != crashExit {
				t.Fatalf("the helper did not stop at %s: %v\n%s", step, err, out)
			}
			// The file is opened again a second later.
			later := dueDay.Add(time.Hour + time.Second)
			s, err := Open(path, Options{Now: func() time.Time { return later }, Outbox: OutboxOptions{Manual: true}})
			if err != nil {
				t.Fatalf("the reopen: %v", err)
			}
			defer s.Close()
			ctx := context.Background()
			n, err := s.Drain(ctx, asSetup)
			if err != nil {
				t.Fatal(err)
			}
			// Stopped after the wake-up's transaction, the reminder is
			// made and only the Log it asked for is left.
			if want := map[string]int{"queued": 2, "performed": 2, "marked": 2, "committed": 1}[step]; n != want {
				t.Errorf("the drain after the reopen attempted %d rows, want %d", n, want)
			}
			doc, err := s.Get(ctx, "Ticket", "T-1")
			if err != nil || doc.Values["pings"] != float64(1) || doc.Version != 2 {
				t.Errorf("the ticket: %+v %v", doc, err)
			}
			if got := kinds(t, s, "Ticket", "T-1"); got != "created action_run:create transition:ping action_run:create" {
				t.Errorf("the events: %s", got)
			}
			if n, _ := s.Count(ctx, Filter{Class: "Log"}); n != 2 {
				t.Errorf("%d logs", n)
			}
			// The next reminder counts from when this one was made: before
			// the stop, or after the reopen.
			next := map[bool]int{true: 7200, false: 7201}[step == "committed"]
			if got := wakes(t, s, "Ticket", "T-1", dueDay); !reflect.DeepEqual(got, []string{"/transitions/0 done ann +3600", fmt.Sprintf("/transitions/0 pending ann +%d", next)}) {
				t.Errorf("the wake-ups: %q", got)
			}
		})
	}
}

// TestWakeCrashHelper is the process TestWake_SurvivesACrashAtEveryStep
// stops: it creates a ticket, lets its hour pass and exits without
// closing the store at the step the environment names.
func TestWakeCrashHelper(t *testing.T) {
	path, step := os.Getenv("FADODB_CRASH_DB"), os.Getenv("FADODB_CRASH_AT")
	if path == "" {
		t.Skip("run by TestWake_SurvivesACrashAtEveryStep")
	}
	cl := &still{now: dueDay}
	s, err := Open(path, Options{Now: cl.Now, Outbox: OutboxOptions{Manual: true}, Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, def := range []string{wakeLog, wakeTicket} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(ctx, "Ticket", nil, WithID("T-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("the first drain: %d %v", n, err)
	}
	cl.add(time.Hour)
	if step == "queued" {
		os.Exit(crashExit)
	}
	s.testStep = func(at string, row *outboxRow) *Error {
		if at == step && row.action == ActionTimer {
			os.Exit(crashExit)
		}
		return nil
	}
	_, err = s.Drain(ctx, asSetup)
	t.Fatalf("the helper reached the end of the drain: %v", err)
}
