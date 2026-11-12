package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Timed transitions (ADR-0047, items 9 to 14, the part without the
// wake-up): what fires in the chain of a commit, what Due lists and what
// Transitions shows.

// timedJob has a timed transition of each kind, mixed with a guard: an
// at over a datetime with a guard beside it, an after, an eventless one
// on the clock, and an at over a date the class computes, with a name;
// and out of its second state an after with a guard and an at over the
// date itself. tail is one more transition, which a test adds at the
// end.
func timedJob(name, tail string) string {
	return `{"name": "` + name + `", "variables": {
  "due": {"type": "date", "index": true}, "ring": {"type": "datetime"}, "ok": {"type": "bool"}, "n": {"type": "number"}},
 "states": {"initial": "a", "list": ["a", "b", "c", "d", "gone"], "final": ["gone"]},
 "transitions": [
   {"from": "a", "to": "b", "at": "ring", "guard": "ok"},
   {"from": "a", "to": "c", "after": "2d"},
   {"from": "a", "to": "gone", "guard": "n > 100 AND TODAY() > due"},
   {"name": "tick", "from": "a", "to": "d", "at": "ADDDAYS(due, 1)"},
   {"name": "go", "from": "a", "to": "b"},
   {"from": "b", "to": "c", "after": "36h", "guard": "n > 0"},
   {"from": "b", "to": "gone", "at": "due"}` + tail + `
 ]}`
}

// job is what a test knows of a document of timedJob: the values it
// gave it, and the state and the entry the store reports.
type job struct {
	id, state string
	entered   int64
	due, ring *int64 // days, seconds; nil for blank
	ok        bool
	n         float64
}

// next is the class as the test reads it: where the first transition
// that fires by itself at now leads, and the moment of its timer, 0 for
// the eventless one; "" when none fires.
func (j *job) next(now int64) (string, int64) {
	const day = 86400
	switch j.state {
	case "a":
		if j.ring != nil && *j.ring <= now && j.ok {
			return "b", *j.ring
		}
		if at := j.entered + 2*day; at <= now {
			return "c", at
		}
		// A blank date compares as the first day there is.
		if due := j.due; j.n > 100 && (due == nil || now >= (*due+1)*day) {
			return "gone", 0
		}
		if j.due != nil && (*j.due+1)*day <= now {
			return "d", (*j.due + 1) * day
		}
	case "b":
		if at := j.entered + 36*3600; at <= now && j.n > 0 {
			return "c", at
		}
		if j.due != nil && *j.due*day <= now {
			return "gone", *j.due * day
		}
	}
	return "", 0
}

// timedJobs makes the documents of a class of timedJob: every
// combination of a date, an instant, the flag and the number, created
// over three days, and every third one of those still in a moved on to
// b a little later. It returns them as the store has them.
func timedJobs(t *testing.T, s *Store, cl *still, class string) []*job {
	t.Helper()
	ctx := context.Background()
	base := cl.Now().Unix()
	baseDay := base / 86400
	days := func(n int64) *int64 { d := baseDay + n; return &d }
	secs := func(n int64) *int64 { v := base + n; return &v }
	old := int64(-3653) // 1960-01-01
	dues := []*int64{nil, days(1), days(2), days(4), days(6), days(-3), &old}
	rings := []*int64{nil, secs(3600), secs(3 * 86400), secs(-50 * 365 * 86400)}
	var jobs []*job
	i := 0
	for _, due := range dues {
		for _, ring := range rings {
			for _, ok := range []any{true, false, nil} {
				for _, n := range []float64{0, 5, 200} {
					vals := Values{"n": n}
					j := &job{id: fmt.Sprintf("J-%03d", i), n: n, due: due, ring: ring}
					if due != nil {
						vals["due"] = time.Unix(*due*86400, 0).UTC().Format("2006-01-02")
					}
					if ring != nil {
						vals["ring"] = *ring
					}
					if ok != nil {
						vals["ok"] = ok
						j.ok = ok.(bool)
					}
					if _, err := s.Create(ctx, class, vals, WithID(j.id)); err != nil {
						t.Fatalf("create %s: %v", j.id, err)
					}
					jobs = append(jobs, j)
					// The documents are created over three days, at odd
					// seconds, so that their afters come one by one.
					if i++; i%40 == 0 {
						cl.mu.Lock()
						cl.now = cl.now.Add(17*time.Hour + 13*time.Second)
						cl.mu.Unlock()
					}
				}
			}
		}
	}
	cl.mu.Lock()
	cl.now = cl.now.Add(5 * time.Hour)
	cl.mu.Unlock()
	for k, j := range jobs {
		doc, err := s.Get(ctx, class, j.id)
		if err != nil {
			t.Fatal(err)
		}
		if doc.State == "a" && k%3 == 0 {
			if doc, err = s.Transition(ctx, class, j.id, "go", nil); err != nil {
				t.Fatalf("go %s: %v", j.id, err)
			}
		}
		j.state, j.entered = doc.State, doc.StateEnteredAt
	}
	return jobs
}

// wantDue is what Due must list at now, by the test's reading of the
// class: in the order of the states and then of the ids.
func wantDue(jobs []*job, now int64) []DueDocument {
	order := map[string]int{"a": 0, "b": 1, "c": 2, "d": 3, "gone": 4}
	out := []DueDocument{}
	for _, j := range jobs {
		if to, since := j.next(now); to != "" {
			out = append(out, DueDocument{ID: j.id, State: j.state, To: to, Since: since})
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].State != out[k].State {
			return order[out[i].State] < order[out[k].State]
		}
		return out[i].ID < out[k].ID
	})
	return out
}

// The list of what is due holds against the test's own reading of the
// class and against the preview of every document, at moments before,
// between and after the timers, on the path that filters and on the path
// that looks document by document.
func TestTimed_Due(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	// The second class has one more transition, last in each state, whose
	// guard calls a function of a state and never holds: its states are
	// looked at document by document.
	never := `,
   {"from": ["a", "b"], "to": "gone", "guard": "ENTRIES(\"gone\") > 0"}`
	for _, def := range []string{timedJob("Fast", ""), timedJob("Slow", never)} {
		if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		class   string
		filters bool
	}{{"Fast", true}, {"Slow", false}} {
		c, _ := s.class(tc.class)
		plan := s.duePlan(c)
		if len(plan.states) != 2 || plan.states[0].name != "a" || plan.states[1].name != "b" || len(plan.states[0].leaving) < 4 {
			t.Fatalf("%s: the plan is %+v", tc.class, plan.states)
		}
		for _, st := range plan.states {
			if (st.filters != nil) != tc.filters {
				t.Fatalf("%s: the state %s has filters %v", tc.class, st.name, st.filters != nil)
			}
		}
	}
	begin := cl.Now()
	fast := timedJobs(t, s, cl, "Fast")
	cl.mu.Lock()
	cl.now = begin
	cl.mu.Unlock()
	slow := timedJobs(t, s, cl, "Slow")
	// The two classes got the same documents at the same moments.
	states := map[string]int{}
	for i := range fast {
		if fast[i].state != slow[i].state || fast[i].entered != slow[i].entered {
			t.Fatalf("%s: %s since %d and %s since %d", fast[i].id, fast[i].state, fast[i].entered, slow[i].state, slow[i].entered)
		}
		states[fast[i].state]++
	}
	// The creates fired what had come already, and many documents wait
	// in each of the two states that timers leave.
	if states["a"] < 40 || states["b"] < 40 || states["d"] < 10 || states["gone"] < 10 {
		t.Fatalf("the documents are in %v", states)
	}
	// The moments: the clock, steps of seven hours over three weeks, and
	// around every timer the second before, its second and the one after.
	now := cl.Now().Unix()
	moments := map[int64]bool{now: true, now - 400*86400: true, now + 400*86400: true}
	for k := int64(0); k < 72; k++ {
		moments[now+k*7*3600] = true
	}
	for _, j := range fast {
		for _, at := range []int64{j.entered + 2*86400, j.entered + 36*3600} {
			moments[at-1], moments[at], moments[at+1] = true, true, true
		}
		if j.due != nil && *j.due > 0 {
			for _, at := range []int64{*j.due * 86400, (*j.due + 1) * 86400} {
				moments[at-1], moments[at], moments[at+1] = true, true, true
			}
		}
		if j.ring != nil && *j.ring > 0 {
			moments[*j.ring-1], moments[*j.ring], moments[*j.ring+1] = true, true, true
		}
	}
	some, timers := 0, map[string]int{}
	for at := range moments {
		opt := At(time.Unix(at, 0))
		want := wantDue(fast, at)
		for _, class := range []string{"Fast", "Slow"} {
			got, err := s.Due(ctx, class, opt)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s at %d: due\n  %v\nwant\n  %v", class, at, got, want)
			}
		}
		for _, d := range want {
			timers[d.State+">"+d.To]++
		}
		some += len(want)
	}
	// Every transition that fires by itself was the first to fire for
	// some document at some moment.
	for _, move := range []string{"a>b", "a>c", "a>d", "a>gone", "b>c", "b>gone"} {
		if timers[move] == 0 {
			t.Errorf("no document was due to move %s: %v", move, timers)
		}
	}
	if some < 1000 {
		t.Errorf("only %d documents were due over %d moments", some, len(moments))
	}
	// And the preview of each document says the same, at a few moments:
	// the list is what a commit that changes nothing would do.
	for _, at := range []int64{now, now + 30*3600, now + 3*86400, now + 9*86400} {
		opt := At(time.Unix(at, 0))
		want := wantDue(fast, at)
		for i := range want {
			want[i].Since = 0
		}
		for _, class := range []string{"Fast", "Slow"} {
			sameDue(t, fmt.Sprintf("the previews of %s at %d", class, at), previewed(t, s, class, opt), want)
		}
	}
	// The listing of one document shows the same moments: for each timed
	// transition leaving its state, in the order of the class.
	for _, j := range fast {
		infos, err := s.Transitions(ctx, "Fast", j.id)
		if err != nil {
			t.Fatal(err)
		}
		var got, want []string
		for _, info := range infos {
			got = append(got, fmt.Sprintf("%s>%s@%d", info.Name, info.To, info.FiresAt))
		}
		switch j.state {
		case "a":
			if j.ring != nil {
				want = append(want, fmt.Sprintf(">b@%d", *j.ring))
			}
			want = append(want, fmt.Sprintf(">c@%d", j.entered+2*86400))
			tick := int64(0)
			if j.due != nil {
				tick = (*j.due + 1) * 86400
			}
			want = append(want, fmt.Sprintf("tick>d@%d", tick), "go>b@0")
		case "b":
			want = append(want, fmt.Sprintf(">c@%d", j.entered+36*3600))
			if j.due != nil {
				want = append(want, fmt.Sprintf(">gone@%d", *j.due*86400))
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("the listing of %s in %s: %v, want %v", j.id, j.state, got, want)
		}
	}
}

// What the filter of each kind of timer is, so that an index can serve
// it: an after compares the entry, an at over a date the bare column.
func TestTimed_Filters(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Bill", "variables": {"due": {"type": "date", "index": true}, "n": {"type": "number"}},
	  "states": {"initial": "open", "list": ["open", "late", "old", "void"]},
	  "transitions": [
	    {"from": "open", "to": "late", "at": "due"},
	    {"from": "late", "to": "old", "after": "30d"},
	    {"from": "old", "to": "void", "after": "30d", "guard": "n > 0"}]}`)); err != nil {
		t.Fatal(err)
	}
	c, _ := s.class("Bill")
	plan := s.duePlan(c)
	if len(plan.states) != 3 {
		t.Fatalf("the plan has %d states", len(plan.states))
	}
	for i, want := range [][]string{
		// The date as a bare column, for the index; the exact condition
		// beside it; and the moment as a column of the answer.
		{`"due" <= COALESCE(`, `SELECT "id", CAST(`, `* 86400 FROM`},
		{`("state_entered_at" <= :now - 2592000)`, `SELECT "id", "state_entered_at" + 2592000 FROM`},
		{`("state_entered_at" <= :now - 2592000 AND (`, `SELECT "id", "state_entered_at" + 2592000 FROM`},
	} {
		st := plan.states[i]
		if len(st.filters) != 1 {
			t.Fatalf("%s has %d filters", st.name, len(st.filters))
		}
		for _, part := range want {
			if !strings.Contains(st.filters[0].text, part) {
				t.Errorf("the filter of %s lacks %s:\n%s", st.name, part, st.filters[0].text)
			}
		}
	}
	// SQLite finds the documents past their date through the index.
	var detail string
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+plan.states[0].filters[0].text,
		append(append([]any{}, plan.states[0].filters[0].args...), append(s.contextArgs(options{}), sql.Named("due_state", "open"), sql.Named("due_version", 1), sql.Named("due_limit", 10))...)...)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		detail += d + "\n"
	}
	rows.Close()
	if !strings.Contains(detail, "doc_Bill_due") {
		t.Errorf("the filter of an at over an indexed date does not use the index:\n%s", detail)
	}
}

// A class that gains a timer: the documents an older version computed
// are looked at one by one, as the commit that adopts them would see
// them, and are due by the entry they have.
func TestTimed_OlderVersions(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	def := `{"name": "Lead", "variables": {"n": {"type": "number"}},
	  "states": {"initial": "open", "list": ["open", "stale"]},
	  "transitions": [{"name": "drop", "from": "open", "to": "stale"}%s]}`
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, ""))); err != nil {
		t.Fatal(err)
	}
	entered := map[string]int64{}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("L-%d", i)
		doc, err := s.Create(ctx, "Lead", Values{"n": i}, WithID(id))
		if err != nil {
			t.Fatal(err)
		}
		entered[id] = doc.StateEnteredAt
		cl.days(1)
	}
	if got, err := s.Due(ctx, "Lead"); err != nil || len(got) != 0 {
		t.Fatalf("a class without a timer has %v due, %v", got, err)
	}
	// Four days on the class says a lead goes stale after three days.
	if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(def, `, {"from": "open", "to": "stale", "after": "3d"}`))); err != nil {
		t.Fatal(err)
	}
	want := []DueDocument{
		{ID: "L-0", State: "open", To: "stale", Since: entered["L-0"] + 3*86400},
		{ID: "L-1", State: "open", To: "stale", Since: entered["L-1"] + 3*86400},
	}
	got, err := s.Due(ctx, "Lead")
	if err != nil {
		t.Fatal(err)
	}
	sameDue(t, "under the second version", got, want)
	for i := range want {
		want[i].Since = 0
	}
	sameDue(t, "the previews", previewed(t, s, "Lead"), want)
	// A new document is of the second version and comes through the
	// filter; the two paths give one list.
	fresh, err := s.Create(ctx, "Lead", Values{"n": 9}, WithID("L-9"))
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Due(ctx, "Lead", At(cl.Now().AddDate(0, 0, 3)))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range got {
		ids = append(ids, d.ID)
		at := entered[d.ID]
		if d.ID == "L-9" {
			at = fresh.StateEnteredAt
		}
		if d.Since != at+3*86400 || d.To != "stale" {
			t.Errorf("%s is due since %d to %s; it entered at %d", d.ID, d.Since, d.To, at)
		}
	}
	if !reflect.DeepEqual(ids, []string{"L-0", "L-1", "L-2", "L-3", "L-9"}) {
		t.Errorf("three days on: %v", ids)
	}
	// The touch adopts the document and fires the timer.
	doc, err := s.Refresh(ctx, "Lead", "L-0", nil)
	if err != nil || doc.State != "stale" {
		t.Fatalf("the touch: %+v %v", doc, err)
	}
	events, err := s.Events(ctx, "Lead", "L-0")
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Kind != EventTransition || last.Name != "" || last.Origin != OriginTimer || last.FromState != "open" || last.ToState != "stale" {
		t.Errorf("the event of the timer: %+v", last)
	}
}

// What a timed transition does in the chain of a commit: its event, the
// rules and the triggered variables of its name and of the states, who
// it is made as, and that no right is asked of it.
func TestTimed_Chain(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Claim", "variables": {
	    "note": {"type": "text"}, "log": {"type": "text", "default": "\"\""}, "limit": {"type": "date"},
	    "pinged": {"type": "datetime", "formula": "NOW()", "mode": "triggered", "on": ["transition:ping"]},
	    "left": {"type": "text", "formula": "STATE() & \" by \" & USER()", "mode": "triggered", "on": ["exit:waiting"]}},
	  "states": {"initial": "waiting", "list": ["waiting", "review", "lapsed"], "final": ["lapsed"]},
	  "transitions": [
	    {"name": "ping", "from": "waiting", "to": "waiting", "after": "1d"},
	    {"name": "lapse", "from": ["waiting", "review"], "to": "lapsed", "at": "limit"},
	    {"from": "waiting", "to": "review", "guard": "note = \"look\""}],
	  "rules": [
	    {"on": "transition", "name": "ping", "do": [{"action": "set", "values": {"log": "log & \"ping:\" & STATE() & \":\" & USER() & \" \""}}]},
	    {"on": "exit", "state": "waiting", "do": [{"action": "set", "values": {"log": "log & \"exit \""}}]},
	    {"on": "enter", "state": "waiting", "do": [{"action": "set", "values": {"log": "log & \"enter \""}}]},
	    {"on": "transition", "name": "lapse", "do": [{"action": "emit", "event": "claim.lapsed"}]}],
	  "access": {"transition": {"ping": "INGROUP(\"sales\")", "lapse": "FALSE"}}}`)); err != nil {
		t.Fatal(err)
	}
	limit := dueDay.AddDate(0, 0, 5).Format("2006-01-02")
	if _, err := s.Create(ctx, "Claim", Values{"limit": limit}, WithID("C-1"), As(ann)); err != nil {
		t.Fatal(err)
	}
	// Bob is not in sales: the transition is not his to take.
	if _, err := s.Transition(ctx, "Claim", "C-1", "ping", nil, As(bob)); code(err) != CodeForbidden {
		t.Fatalf("bob takes ping: %v", err)
	}
	// Before its time nothing fires.
	doc, err := s.Refresh(ctx, "Claim", "C-1", nil, As(bob))
	if err != nil || doc.Version != 2 || doc.Values["log"] != "enter " {
		t.Fatalf("a touch before the time: %+v %v", doc, err)
	}
	cl.days(1)
	// Its time has come, and bob's commit fires it: the move is the
	// document's own, so his right is not asked. The rules of the exit,
	// of the name and of the entry run, in that order, and the triggered
	// variables of the exit and of the name are computed again.
	doc, err = s.Set(ctx, "Claim", "C-1", Values{"note": "called"}, As(bob))
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != "waiting" || doc.Version != 4 || doc.StateEnteredBy != "bob" || doc.StateEnteredFrom != "waiting" || doc.StateEnteredAt != cl.Now().Unix() {
		t.Fatalf("after the timer: %+v", doc)
	}
	if got := doc.Values["log"]; got != "enter exit ping:waiting:bob enter " {
		t.Errorf("the rules ran as %q", got)
	}
	if doc.Values["pinged"] != cl.Now().Unix() || doc.Values["left"] != "waiting by bob" {
		t.Errorf("the triggered variables: pinged %v, left %v", doc.Values["pinged"], doc.Values["left"])
	}
	events, err := s.Events(ctx, "Claim", "C-1")
	if err != nil {
		t.Fatal(err)
	}
	set, ping := events[len(events)-2], events[len(events)-1]
	if set.Kind != EventSet || ping.Kind != EventTransition || ping.Name != "ping" || ping.Origin != OriginTimer || ping.CauseEventID != set.ID ||
		ping.ActorID != "bob" || ping.FromState != "waiting" || ping.ToState != "waiting" || ping.BeforeVersion != 3 || ping.AfterVersion != 4 || ping.CommandKey != "" {
		t.Errorf("the events of the commit: %+v, %+v", set, ping)
	}
	// The same second again: the new stay has just begun, so the timer
	// does not fire twice.
	doc, err = s.Refresh(ctx, "Claim", "C-1", nil, As(bob))
	if err != nil || doc.Version != 5 || doc.State != "waiting" {
		t.Fatalf("a second touch: %+v %v", doc, err)
	}
	// An eventless transition stands after the timers in the class: when
	// the timer has not come it is the one that fires, and its event has
	// no name and the origin guard.
	doc, err = s.Set(ctx, "Claim", "C-1", Values{"note": "look"}, As(ann))
	if err != nil || doc.State != "review" {
		t.Fatalf("the guard: %+v %v", doc, err)
	}
	events, _ = s.Events(ctx, "Claim", "C-1")
	if last := events[len(events)-1]; last.Name != "" || last.Origin != OriginGuard {
		t.Errorf("the event of the eventless transition: %+v", last)
	}
	// The at: nobody may take lapse by its name, and it fires by itself
	// when its day begins, with the rule of its name.
	if _, err := s.Transition(ctx, "Claim", "C-1", "lapse", nil, As(ann)); code(err) != CodeForbidden {
		t.Fatalf("ann takes lapse: %v", err)
	}
	cl.days(3)
	if doc, err = s.Refresh(ctx, "Claim", "C-1", nil, As(ann)); err != nil || doc.State != "review" {
		t.Fatalf("the day before: %+v %v", doc, err)
	}
	cl.days(1)
	if got, err := s.Due(ctx, "Claim"); err != nil || len(got) != 1 || got[0].To != "lapsed" || got[0].Since != dueDay.AddDate(0, 0, 5).Truncate(24*time.Hour).Unix() {
		t.Fatalf("due on the day: %+v %v", got, err)
	}
	if doc, err = s.Refresh(ctx, "Claim", "C-1", nil, As(ann)); err != nil || doc.State != "lapsed" {
		t.Fatalf("on the day: %+v %v", doc, err)
	}
	events, _ = s.Events(ctx, "Claim", "C-1")
	var kinds []string
	for _, e := range events[len(events)-3:] {
		kinds = append(kinds, e.Kind+":"+e.Name+":"+e.Origin)
	}
	if !reflect.DeepEqual(kinds, []string{"refresh::", "transition:lapse:timer", "custom:claim.lapsed:"}) {
		t.Errorf("the events of the lapse: %v", kinds)
	}
}

// A preview at a chosen moment asks what a commit at that moment would
// do: a stay the previewed command begins counts from that moment, so
// an after out of it has not come, a reminder fires once, and the
// listing shows the moments counted from there.
func TestTimed_PreviewAtAMoment(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Nudge", "variables": {"n": {"type": "number"}},
	  "states": {"initial": "idle", "list": ["idle", "waiting", "done"]},
	  "transitions": [
	    {"name": "start", "from": "idle", "to": "waiting"},
	    {"name": "remind", "from": "waiting", "to": "waiting", "after": "3d"},
	    {"from": "idle", "to": "done", "after": "10d"}]}`)); err != nil {
		t.Fatal(err)
	}
	now := cl.Now().Unix()
	far := time.Unix(now+40*86400, 0)
	fires := func(infos []TransitionInfo, name string) int64 {
		for _, info := range infos {
			if info.Name == name {
				return info.FiresAt
			}
		}
		return -1
	}
	// A create far ahead: the document would be idle from then.
	pv, err := s.Preview(ctx, "Nudge", "", Values{"n": 1}, "", At(far))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pv.Path, []string{"idle"}) || fires(pv.Transitions, "") != far.Unix()+10*86400 {
		t.Errorf("a create at a moment: path %v, listing %+v", pv.Path, pv.Transitions)
	}
	if _, err := s.Create(ctx, "Nudge", Values{"n": 1}, WithID("N-1")); err != nil {
		t.Fatal(err)
	}
	// The stored document has been idle since today: far ahead its ten
	// days are over.
	pv, err = s.Preview(ctx, "Nudge", "N-1", nil, "", At(far))
	if err != nil || !reflect.DeepEqual(pv.Path, []string{"idle", "done"}) {
		t.Errorf("the stored document far ahead: %+v %v", pv, err)
	}
	// A start far ahead begins the wait then: no reminder is due in the
	// same commit, and the first is three days after the moment.
	pv, err = s.Preview(ctx, "Nudge", "N-1", nil, "start", At(far))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pv.Path, []string{"idle", "waiting"}) || fires(pv.Transitions, "remind") != far.Unix()+3*86400 {
		t.Errorf("a start at a moment: path %v, listing %+v", pv.Path, pv.Transitions)
	}
	if _, err := s.Transition(ctx, "Nudge", "N-1", "start", nil); err != nil {
		t.Fatal(err)
	}
	// The stored wait began today. Far ahead the reminder fires, once:
	// the stay it begins counts from the moment of the preview.
	pv, err = s.Preview(ctx, "Nudge", "N-1", nil, "", At(far))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pv.Path, []string{"waiting", "waiting"}) || fires(pv.Transitions, "remind") != far.Unix()+3*86400 {
		t.Errorf("the reminder far ahead: path %v, listing %+v", pv.Path, pv.Transitions)
	}
	// At the clock it has not come, and the listing counts from the entry.
	infos, err := s.Transitions(ctx, "Nudge", "N-1")
	if err != nil || fires(infos, "remind") != now+3*86400 {
		t.Errorf("the listing at the clock: %+v %v", infos, err)
	}
	// A commit does the same as its preview said.
	cl.days(40)
	doc, err := s.Refresh(ctx, "Nudge", "N-1", nil)
	if err != nil || doc.Version != 4 || doc.State != "waiting" || doc.StateEnteredAt != cl.Now().Unix() {
		t.Errorf("the commit far ahead: %+v %v", doc, err)
	}
}

// A moment that cannot be worked out never comes: an at that is blank or
// whose formula fails. And timers that send a document round in a circle
// are stopped at the bound of the chain, like guards that hold in turn.
func TestTimed_NeverAndForever(t *testing.T) {
	cl := &still{now: dueDay}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	never := `,
	    {"from": "open", "to": "void", "guard": "ENTRIES(\"void\") > 0"}`
	for _, tc := range []struct{ name, tail string }{{"Odd", ""}, {"OddSlow", never}} {
		if _, err := s.DefineClass(ctx, []byte(`{"name": "`+tc.name+`", "variables": {"due": {"type": "date"}, "n": {"type": "number"}},
	  "states": {"initial": "open", "list": ["open", "late", "void"]},
	  "transitions": [
	    {"name": "expire", "from": "open", "to": "late", "at": "ADDDAYS(due, 10 / n)"}`+tc.tail+`]}`)); err != nil {
			t.Fatal(err)
		}
		past := dueDay.AddDate(0, 0, -30).Format("2006-01-02")
		for id, vals := range map[string]Values{
			"blank":  {"n": 1},              // no date: the at is blank
			"fails":  {"due": past, "n": 0}, // a division by zero
			"comes":  {"due": past, "n": 2}, // five days after a date long past
			"waits":  {"due": day(20), "n": 2},
			"nobody": {},
		} {
			// The one whose moment has come moves in the create.
			doc, err := s.Create(ctx, tc.name, vals, WithID(id))
			if err != nil {
				t.Fatal(err)
			}
			if want := map[bool]string{true: "late", false: "open"}[id == "comes"]; doc.State != want {
				t.Errorf("%s: %s is created %s", tc.name, id, doc.State)
			}
		}
		for _, at := range []time.Time{cl.Now(), cl.Now().AddDate(1, 0, 0)} {
			got, err := s.Due(ctx, tc.name, At(at))
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, d := range got {
				ids = append(ids, d.ID)
			}
			want := []string(nil)
			if at != cl.Now() {
				want = []string{"waits"}
			}
			if !reflect.DeepEqual(ids, want) {
				t.Errorf("%s at %v: due %v, want %v", tc.name, at, ids, want)
			}
		}
		// The listing has the transition, since it has a name, and no
		// moment for it.
		for _, id := range []string{"blank", "fails", "nobody"} {
			infos, err := s.Transitions(ctx, tc.name, id)
			if err != nil || len(infos) != 1 || infos[0].Name != "expire" || infos[0].FiresAt != 0 || !infos[0].Enabled {
				t.Errorf("%s: the listing of %s: %+v %v", tc.name, id, infos, err)
			}
			if doc, err := s.Refresh(ctx, tc.name, id, nil); err != nil || doc.State != "open" {
				t.Errorf("%s: a touch of %s: %+v %v", tc.name, id, doc, err)
			}
		}
	}
	// Two timers whose moments have both come, each into the other's
	// state.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Loop", "variables": {"x": {"type": "date"}, "y": {"type": "date"}},
	  "states": {"initial": "p", "list": ["p", "q"]},
	  "transitions": [{"from": "p", "to": "q", "at": "x"}, {"from": "q", "to": "p", "at": "y"}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Loop", Values{"x": day(-1), "y": day(-2)}, WithID("L")); code(err) != CodeTooManyTransitions {
		t.Errorf("a circle of timers: %v", err)
	}
	if doc, err := s.Create(ctx, "Loop", Values{"x": day(-1), "y": day(2)}, WithID("L")); err != nil || doc.State != "q" || doc.Version != 2 {
		t.Errorf("half a circle: %+v %v", doc, err)
	}
}

// Moments before 1970 are negative seconds: the timers count them like
// any others, in a commit, in the filter and document by document.
func TestTimed_BeforeTheEpoch(t *testing.T) {
	begin := time.Date(1965, 3, 1, 8, 30, 0, 0, time.UTC)
	cl := &still{now: begin}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	never := `,
	    {"from": "open", "to": "void", "guard": "ENTRIES(\"void\") > 0"}`
	classes := []string{"Old", "OldSlow"}
	for i, tail := range []string{"", never} {
		if _, err := s.DefineClass(ctx, []byte(`{"name": "`+classes[i]+`", "variables": {"due": {"type": "date"}, "ring": {"type": "datetime"}},
	  "states": {"initial": "open", "list": ["open", "late", "rung", "stale", "void"]},
	  "transitions": [
	    {"from": "open", "to": "late", "at": "due"},
	    {"from": "open", "to": "rung", "at": "ring"},
	    {"from": "open", "to": "stale", "after": "36h"}`+tail+`]}`)); err != nil {
			t.Fatal(err)
		}
		for id, vals := range map[string]Values{
			"d": {"due": "1965-03-02"},
			"r": {"ring": "1965-03-02 20:29:59"},
			"s": {},
		} {
			if doc, err := s.Create(ctx, classes[i], vals, WithID(id)); err != nil || doc.State != "open" {
				t.Fatalf("%s: create %s: %+v %v", classes[i], id, doc, err)
			}
		}
	}
	day := time.Date(1965, 3, 2, 0, 0, 0, 0, time.UTC).Unix()
	ring := time.Date(1965, 3, 2, 20, 29, 59, 0, time.UTC).Unix()
	stale := begin.Unix() + 36*3600
	if day >= 0 || ring >= 0 || stale >= 0 || stale != ring+1 {
		t.Fatalf("the moments are %d, %d and %d", day, ring, stale)
	}
	for _, tc := range []struct {
		at   int64
		want []DueDocument
	}{
		{day - 1, []DueDocument{}},
		{day, []DueDocument{{"d", "open", "late", day}}},
		{ring - 1, []DueDocument{{"d", "open", "late", day}}},
		{ring, []DueDocument{{"d", "open", "late", day}, {"r", "open", "rung", ring}}},
		// The after has come for all three; for two an at stands before it.
		{stale, []DueDocument{{"d", "open", "late", day}, {"r", "open", "rung", ring}, {"s", "open", "stale", stale}}},
	} {
		for _, class := range classes {
			got, err := s.Due(ctx, class, At(time.Unix(tc.at, 0)))
			if err != nil {
				t.Fatal(err)
			}
			sameDue(t, fmt.Sprintf("%s at %d", class, tc.at), got, tc.want)
		}
	}
	// The listing gives the same moments, and a commit fires by them.
	infos, err := s.Transitions(ctx, "Old", "s")
	if err != nil || len(infos) != 1 || infos[0].FiresAt != stale {
		t.Errorf("the listing: %+v %v", infos, err)
	}
	cl.mu.Lock()
	cl.now = time.Unix(ring, 0).UTC()
	cl.mu.Unlock()
	for id, want := range map[string]string{"d": "late", "r": "rung", "s": "open"} {
		if doc, err := s.Refresh(ctx, "Old", id, nil); err != nil || doc.State != want {
			t.Errorf("a touch of %s: %+v %v, want %s", id, doc, err, want)
		}
	}
}
