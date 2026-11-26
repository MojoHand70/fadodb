package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// An invoice of the tests of the flow: sales reads its own, finance
// reads every one; an invoice of nothing is cancelled by itself.
const flowInvoice = `{"name": "Invoice",
 "variables": {"owner": {"type": "text", "default": "USER()"}, "total": {"type": "decimal", "scale": 2}},
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "cancelled"], "final": ["approved", "cancelled"]},
 "transitions": [
   {"name": "submit", "from": "draft", "to": "review"},
   {"from": "review", "to": "cancelled", "guard": "total = 0"},
   {"name": "approve", "from": "review", "to": "approved"},
   {"name": "cancel", "from": ["draft", "review"], "to": "cancelled"},
   {"name": "expire", "from": "draft", "to": "cancelled", "after": "30d"}
 ],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "TRUE", "delete": "INGROUP(\"sales\")"}}`

var (
	flowBob = As(Principal{ID: "bob", Groups: []string{"sales"}})
	flowDan = As(Principal{ID: "dan", Groups: []string{"sales"}})
	flowAnn = As(Principal{ID: "ann", Groups: []string{"finance"}})
	flowEve = As(Principal{ID: "eve"})
)

// flowOf asks for a flow that must be answered, and writes it as its
// JSON.
func flowOf(t *testing.T, s *Store, class string, period FlowPeriod, as Option) string {
	t.Helper()
	f, err := s.Flow(context.Background(), class, period, as)
	return shown(t, f, err)
}

// shown writes a flow as its JSON, for a test to compare.
func shown(t *testing.T, f *Flow, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	text, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// The flow of a class: the documents in each state now, the moves
// along each transition and how long the stays lasted, for the
// documents the caller may read (ADR-0063).
func TestTheFlowOfAClass(t *testing.T) {
	cl := &still{now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(flowInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	must := func(_ *Document, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Monday 9:00: Bob files three invoices and Dan one.
	for _, id := range []string{"B-1", "B-2", "B-3"} {
		must(s.Create(ctx, "Invoice", Values{"total": "100.00"}, flowBob, WithID(id)))
	}
	must(s.Create(ctx, "Invoice", Values{"total": "0.00"}, flowDan, WithID("D-1")))
	// 10:00: Bob submits two, an hour after he filed them. Dan submits
	// his, which is of nothing, and the engine cancels it at once.
	cl.add(time.Hour)
	must(s.Transition(ctx, "Invoice", "B-1", "submit", nil, flowBob))
	must(s.Transition(ctx, "Invoice", "B-2", "submit", nil, flowBob))
	must(s.Transition(ctx, "Invoice", "D-1", "submit", nil, flowDan))
	// 13:00: Ann approves one, three hours after it was submitted.
	cl.add(3 * time.Hour)
	must(s.Transition(ctx, "Invoice", "B-1", "approve", nil, flowAnn))
	// Tuesday 13:00: Bob cancels the third, which stayed a draft for 28
	// hours.
	cl.add(24 * time.Hour)
	must(s.Transition(ctx, "Invoice", "B-3", "cancel", nil, flowBob))

	monday := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC).Unix()
	// Ann, in finance, reads every invoice.
	want := `{"class":"Invoice","class_version":1,"created":4,"states":[` +
		`{"name":"draft","initial":true,"documents":0,"stays":4,"average_seconds":27900,"longest_seconds":100800},` +
		`{"name":"review","documents":1,"oldest_since":` + secondsText(monday+3600) + `,"stays":2,"average_seconds":5400,"longest_seconds":10800},` +
		`{"name":"approved","final":true,"documents":1,"oldest_since":` + secondsText(monday+4*3600) + `,"stays":0,"average_seconds":0,"longest_seconds":0},` +
		`{"name":"cancelled","final":true,"documents":2,"oldest_since":` + secondsText(monday+3600) + `,"stays":0,"average_seconds":0,"longest_seconds":0}],` +
		`"transitions":[` +
		`{"name":"submit","from":"draft","to":"review","moves":3},` +
		`{"from":"review","to":"cancelled","automatic":true,"moves":1},` +
		`{"name":"approve","from":"review","to":"approved","moves":1},` +
		`{"name":"cancel","from":"draft","to":"cancelled","moves":1},` +
		`{"name":"cancel","from":"review","to":"cancelled","moves":0},` +
		`{"name":"expire","from":"draft","to":"cancelled","timed":true,"moves":0}]}`
	if got := flowOf(t, s, "Invoice", FlowPeriod{}, flowAnn); got != want {
		t.Errorf("the flow Ann sees is\n%s\nwant\n%s", got, want)
	}
	// Bob, in sales, reads his own three: Dan's invoice is in no count
	// of his, neither where it is nor how it moved.
	want = `{"class":"Invoice","class_version":1,"created":3,"states":[` +
		`{"name":"draft","initial":true,"documents":0,"stays":3,"average_seconds":36000,"longest_seconds":100800},` +
		`{"name":"review","documents":1,"oldest_since":` + secondsText(monday+3600) + `,"stays":1,"average_seconds":10800,"longest_seconds":10800},` +
		`{"name":"approved","final":true,"documents":1,"oldest_since":` + secondsText(monday+4*3600) + `,"stays":0,"average_seconds":0,"longest_seconds":0},` +
		`{"name":"cancelled","final":true,"documents":1,"oldest_since":` + secondsText(monday+28*3600) + `,"stays":0,"average_seconds":0,"longest_seconds":0}],` +
		`"transitions":[` +
		`{"name":"submit","from":"draft","to":"review","moves":2},` +
		`{"from":"review","to":"cancelled","automatic":true,"moves":0},` +
		`{"name":"approve","from":"review","to":"approved","moves":1},` +
		`{"name":"cancel","from":"draft","to":"cancelled","moves":1},` +
		`{"name":"cancel","from":"review","to":"cancelled","moves":0},` +
		`{"name":"expire","from":"draft","to":"cancelled","timed":true,"moves":0}]}`
	if got := flowOf(t, s, "Invoice", FlowPeriod{}, flowBob); got != want {
		t.Errorf("the flow Bob sees is\n%s\nwant\n%s", got, want)
	}
	// How the stays spread, for a caller who asks (ADR-0095). Three
	// invoices left draft after an hour and one after 28: the average,
	// 7 hours 45 minutes, is a time none took; the middle stay is an
	// hour, and the slow tenth the 28. Of the two stays in review one
	// ended at once and one after three hours.
	spread := func(as Option, period FlowPeriod) string {
		t.Helper()
		period.Spread = true
		f, err := s.Flow(ctx, "Invoice", period, as)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, st := range f.States {
			if st.Spread == nil {
				t.Fatalf("the state %s says nothing of how its stays spread", st.Name)
			}
			out = append(out, fmt.Sprintf("%s %d/%d", st.Name, st.Spread.MiddleSeconds, st.Spread.SlowTenthSeconds))
		}
		return strings.Join(out, ", ")
	}
	if got := spread(flowAnn, FlowPeriod{}); got != "draft 3600/100800, review 0/10800, approved 0/0, cancelled 0/0" {
		t.Errorf("how the stays spread for Ann: %s", got)
	}
	// Bob's own: three stays in draft, one in review.
	if got := spread(flowBob, FlowPeriod{}); got != "draft 3600/100800, review 10800/10800, approved 0/0, cancelled 0/0" {
		t.Errorf("how the stays spread for Bob: %s", got)
	}
	// Eve reads none, and a period takes the stays that ended in it.
	if got := spread(flowEve, FlowPeriod{}); got != "draft 0/0, review 0/0, approved 0/0, cancelled 0/0" {
		t.Errorf("how the stays spread for Eve: %s", got)
	}
	if got := spread(flowAnn, FlowPeriod{Until: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}); got != "draft 3600/3600, review 0/10800, approved 0/0, cancelled 0/0" {
		t.Errorf("how the stays spread before Tuesday: %s", got)
	}
	// A caller who does not ask is told nothing of it, and the answer
	// is the one above, with the same numbers beside the spread.
	asked, err := s.Flow(ctx, "Invoice", FlowPeriod{Spread: true}, flowAnn)
	if err != nil {
		t.Fatal(err)
	}
	for i := range asked.States {
		asked.States[i].Spread = nil
	}
	if got := shown(t, asked, nil); got != flowOf(t, s, "Invoice", FlowPeriod{}, flowAnn) {
		t.Errorf("the flow that was asked how the stays spread differs in the rest: %s", got)
	}

	// Eve reads no invoice: her flow has the chart and no document.
	eve, err := s.Flow(ctx, "Invoice", FlowPeriod{}, flowEve)
	if err != nil {
		t.Fatal(err)
	}
	if eve.Created != 0 || len(eve.States) != 4 || len(eve.Transitions) != 6 {
		t.Errorf("the flow Eve sees is %+v", eve)
	}
	for _, st := range eve.States {
		if st.Documents != 0 || st.Stays != 0 || st.OldestSince != 0 {
			t.Errorf("Eve is told of documents in %s: %+v", st.Name, st)
		}
	}
	for _, tr := range eve.Transitions {
		if tr.Moves != 0 {
			t.Errorf("Eve is told of moves along %s: %+v", tr.Name, tr)
		}
	}

	// A period: the moves and the stays that ended in it, from its
	// first moment and before its last. Where the documents are is
	// always now.
	tuesday := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	later, err := s.Flow(ctx, "Invoice", FlowPeriod{Since: tuesday}, flowAnn)
	if err != nil {
		t.Fatal(err)
	}
	if later.Since != tuesday.Unix() || later.Until != 0 || later.Created != 0 || later.States[0].Stays != 1 || later.States[0].AverageSeconds != 100800 ||
		later.States[1].Stays != 0 || later.States[1].Documents != 1 || later.Transitions[3].Moves != 1 || later.Transitions[0].Moves != 0 {
		t.Errorf("the flow since Tuesday is %s", shown(t, later, nil))
	}
	earlier, err := s.Flow(ctx, "Invoice", FlowPeriod{Until: tuesday}, flowAnn)
	if err != nil {
		t.Fatal(err)
	}
	if earlier.Since != 0 || earlier.Until != tuesday.Unix() || earlier.Created != 4 || earlier.States[0].Stays != 3 || earlier.States[0].AverageSeconds != 3600 ||
		earlier.Transitions[0].Moves != 3 || earlier.Transitions[3].Moves != 0 || earlier.States[3].Documents != 2 {
		t.Errorf("the flow before Tuesday is %s", shown(t, earlier, nil))
	}
	// The first moment of a period is in it, and its last is not.
	at10 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if f, _ := s.Flow(ctx, "Invoice", FlowPeriod{Since: at10, Until: at10.Add(time.Second)}, flowAnn); f == nil || f.Transitions[0].Moves != 3 {
		t.Errorf("a period that begins with the moves: %+v", f)
	}
	if f, _ := s.Flow(ctx, "Invoice", FlowPeriod{Since: at10.Add(-time.Hour), Until: at10}, flowAnn); f == nil || f.Transitions[0].Moves != 0 || f.Created != 4 {
		t.Errorf("a period that ends with the moves: %+v", f)
	}

	// A deleted document is not counted, neither where it was nor how
	// it moved.
	if err := s.Delete(ctx, "Invoice", "B-2", flowBob); err != nil {
		t.Fatal(err)
	}
	after, err := s.Flow(ctx, "Invoice", FlowPeriod{}, flowAnn)
	if err != nil {
		t.Fatal(err)
	}
	if after.Created != 3 || after.States[1].Documents != 0 || after.Transitions[0].Moves != 2 {
		t.Errorf("after a delete the flow is %s", shown(t, after, nil))
	}

	// What is refused.
	for what, c := range map[string]struct {
		class  string
		period FlowPeriod
		as     Option
		code   string
	}{
		"a class there is none of":            {"Receipt", FlowPeriod{}, flowAnn, CodeUnknownClass},
		"a period that ends before it begins": {"Invoice", FlowPeriod{Since: tuesday, Until: tuesday.Add(-time.Hour)}, flowAnn, CodeBadValue},
		"a period of no length":               {"Invoice", FlowPeriod{Since: tuesday, Until: tuesday}, flowAnn, CodeBadValue},
	} {
		var refused *Error
		if _, err := s.Flow(ctx, c.class, c.period, c.as); !errors.As(err, &refused) || refused.Code != c.code {
			t.Errorf("%s: %v, want %s", what, err, c.code)
		}
	}
}

// A class that is defined again touches no document, and keeps what
// happened: a state and a transition it no longer has stay in the flow
// while documents left the one or moved along the other in the period,
// marked retired. (A state that documents are in cannot be taken out of
// a class.)
func TestTheFlowOfAClassThatChanged(t *testing.T) {
	cl := &still{now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	const first = `{"name": "Ticket", "variables": {"what": {"type": "text"}},
 "states": {"initial": "new", "list": ["new", "triage", "done"]},
 "transitions": [{"name": "sort", "from": "new", "to": "triage"}, {"name": "close", "from": "triage", "to": "done"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	const second = `{"name": "Ticket", "variables": {"what": {"type": "text"}},
 "states": {"initial": "new", "list": ["new", "done"]},
 "transitions": [{"name": "close", "from": "new", "to": "done"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	if _, err := s.DefineClass(ctx, []byte(first), asSetup); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"T-1", "T-2"} {
		if _, err := s.Create(ctx, "Ticket", Values{"what": id}, flowBob, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	cl.add(time.Hour)
	if _, err := s.Transition(ctx, "Ticket", "T-1", "sort", nil, flowBob); err != nil {
		t.Fatal(err)
	}
	cl.add(2 * time.Hour)
	if _, err := s.Transition(ctx, "Ticket", "T-1", "close", nil, flowBob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(second), asSetup); err != nil {
		t.Fatal(err)
	}
	monday := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC).Unix()
	want := `{"class":"Ticket","class_version":2,"created":2,"states":[` +
		`{"name":"new","initial":true,"documents":1,"oldest_since":` + secondsText(monday) + `,"stays":1,"average_seconds":3600,"longest_seconds":3600},` +
		`{"name":"done","documents":1,"oldest_since":` + secondsText(monday+3*3600) + `,"stays":0,"average_seconds":0,"longest_seconds":0},` +
		`{"name":"triage","retired":true,"documents":0,"stays":1,"average_seconds":7200,"longest_seconds":7200}],` +
		`"transitions":[` +
		`{"name":"close","from":"new","to":"done","moves":0},` +
		`{"name":"sort","from":"new","to":"triage","retired":true,"moves":1},` +
		`{"name":"close","from":"triage","to":"done","retired":true,"moves":1}]}`
	if got := flowOf(t, s, "Ticket", FlowPeriod{}, flowBob); got != want {
		t.Errorf("the flow of a class that changed is\n%s\nwant\n%s", got, want)
	}
}

func secondsText(n int64) string {
	text, _ := json.Marshal(n)
	return string(text)
}

// The throughput of a class (ADR-0096): how many documents were made
// and how many finished in each day, week or month, for the caller, by
// the clock of the database's time zone. The story is that of the flow.
func TestTheThroughputOfAClass(t *testing.T) {
	cl := &still{now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: cl.Now})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(flowInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	must := func(_ *Document, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Monday 2 November: four invoices made; D-1 is cancelled at 10:00
	// and B-1 approved at 13:00. Tuesday: B-3 cancelled. Friday 6: one
	// more made. Monday 9: it is approved. Tuesday 1 December: one made.
	for _, id := range []string{"B-1", "B-2", "B-3"} {
		must(s.Create(ctx, "Invoice", Values{"total": "100.00"}, flowBob, WithID(id)))
	}
	must(s.Create(ctx, "Invoice", Values{"total": "0.00"}, flowDan, WithID("D-1")))
	cl.add(time.Hour)
	must(s.Transition(ctx, "Invoice", "B-1", "submit", nil, flowBob))
	must(s.Transition(ctx, "Invoice", "B-2", "submit", nil, flowBob))
	must(s.Transition(ctx, "Invoice", "D-1", "submit", nil, flowDan))
	cl.add(3 * time.Hour)
	must(s.Transition(ctx, "Invoice", "B-1", "approve", nil, flowAnn))
	cl.add(24 * time.Hour)
	must(s.Transition(ctx, "Invoice", "B-3", "cancel", nil, flowBob))
	cl.now = time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	must(s.Create(ctx, "Invoice", Values{"total": "5.00"}, flowBob, WithID("B-4")))
	must(s.Transition(ctx, "Invoice", "B-4", "submit", nil, flowBob))
	cl.now = time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	must(s.Transition(ctx, "Invoice", "B-4", "approve", nil, flowAnn))
	cl.now = time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
	must(s.Create(ctx, "Invoice", Values{"total": "7.00"}, flowBob, WithID("B-5")))

	told := func(as Option, ask ThroughputAsk) string {
		t.Helper()
		th, err := s.Throughput(ctx, "Invoice", ask, as)
		if err != nil {
			t.Fatal(err)
		}
		var sumC, sumF int64
		out := make([]string, len(th.Periods))
		for i, p := range th.Periods {
			out[i] = fmt.Sprintf("%s %d/%d", p.From, p.Created, p.Finished)
			sumC, sumF = sumC+p.Created, sumF+p.Finished
		}
		if sumC != th.Created || sumF != th.Finished || th.TimeZone != "UTC" || th.Class != "Invoice" {
			t.Errorf("the sums of a throughput: %+v", th)
		}
		return fmt.Sprintf("%s: %s", th.By, strings.Join(out, ", "))
	}
	// Ann reads every invoice. By week: a week begins on a Monday, and
	// the weeks in which nothing was made or finished are there too.
	if got := told(flowAnn, ThroughputAsk{By: ThroughputWeek}); got != "week: 2026-11-02 5/3, 2026-11-09 0/1, 2026-11-16 0/0, 2026-11-23 0/0, 2026-11-30 1/0" {
		t.Errorf("Ann's weeks: %s", got)
	}
	if got := told(flowAnn, ThroughputAsk{By: ThroughputMonth}); got != "month: 2026-11-01 5/4, 2026-12-01 1/0" {
		t.Errorf("Ann's months: %s", got)
	}
	// By day when nothing is said, and within a period.
	first := ThroughputAsk{Until: time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)}
	if got := told(flowAnn, first); got != "day: 2026-11-02 4/2, 2026-11-03 0/1, 2026-11-04 0/0, 2026-11-05 0/0, 2026-11-06 1/0" {
		t.Errorf("Ann's days of the first week: %s", got)
	}
	// Bob reads his own: Dan's invoice is in no number of his.
	if got := told(flowBob, first); got != "day: 2026-11-02 3/1, 2026-11-03 0/1, 2026-11-04 0/0, 2026-11-05 0/0, 2026-11-06 1/0" {
		t.Errorf("Bob's days of the first week: %s", got)
	}
	// Eve reads none, and a period in which nothing happened is empty.
	if got := told(flowEve, ThroughputAsk{}); got != "day: " {
		t.Errorf("Eve's days: %s", got)
	}
	if got := told(flowAnn, ThroughputAsk{Since: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}); got != "day: " {
		t.Errorf("days in which nothing happened: %s", got)
	}
	// A deleted document is in no number.
	if err := s.Delete(ctx, "Invoice", "B-5", flowBob); err != nil {
		t.Fatal(err)
	}
	if got := told(flowAnn, ThroughputAsk{By: ThroughputMonth}); got != "month: 2026-11-01 5/4" {
		t.Errorf("Ann's months after a delete: %s", got)
	}
	// What is refused.
	tuesday := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for what, c := range map[string]struct {
		class string
		ask   ThroughputAsk
		code  string
	}{
		"a class there is none of":            {"Receipt", ThroughputAsk{}, CodeUnknownClass},
		"a period that ends before it begins": {"Invoice", ThroughputAsk{Since: tuesday, Until: tuesday.Add(-time.Hour)}, CodeBadValue},
		"a length of a period there is none":  {"Invoice", ThroughputAsk{By: "year"}, CodeBadValue},
	} {
		var refused *Error
		if _, err := s.Throughput(ctx, c.class, c.ask, flowAnn); !errors.As(err, &refused) || refused.Code != c.code {
			t.Errorf("%s: %v, want %s", what, err, c.code)
		}
	}
}

// A day begins at midnight by the clock of the database's time zone,
// and a week on its Monday there: an invoice made on Sunday at 23:30 in
// UTC is Monday's in Warsaw, and one finished on the night the clocks
// go back is the day's it was finished on there.
func TestTheThroughputGoesByTheDatabasesZone(t *testing.T) {
	cl := &still{now: time.Date(2026, 11, 1, 23, 30, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: cl.Now, TimeZone: "Europe/Warsaw"})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(flowInvoice), asSetup); err != nil {
		t.Fatal(err)
	}
	// Sunday 1 November 23:30 UTC is Monday 2 November 00:30 in Warsaw.
	if _, err := s.Create(ctx, "Invoice", Values{"total": "1.00"}, flowBob, WithID("B-1")); err != nil {
		t.Fatal(err)
	}
	// Saturday 24 October 2026, 21:59 UTC, is 23:59 in Warsaw, in summer
	// time; the clocks go back that night.
	cl.now = time.Date(2026, 10, 24, 21, 59, 0, 0, time.UTC)
	if _, err := s.Create(ctx, "Invoice", Values{"total": "1.00"}, flowBob, WithID("B-0")); err != nil {
		t.Fatal(err)
	}
	// Sunday 25 October, 23:30 in Warsaw, now in winter time: 22:30 UTC.
	cl.now = time.Date(2026, 10, 25, 22, 30, 0, 0, time.UTC)
	if _, err := s.Transition(ctx, "Invoice", "B-0", "cancel", nil, flowBob); err != nil {
		t.Fatal(err)
	}
	th, err := s.Throughput(ctx, "Invoice", ThroughputAsk{}, flowAnn)
	if err != nil {
		t.Fatal(err)
	}
	var days []string
	for _, p := range th.Periods {
		if p.Created != 0 || p.Finished != 0 {
			days = append(days, fmt.Sprintf("%s %d/%d", p.From, p.Created, p.Finished))
		}
	}
	if got := strings.Join(days, ", "); got != "2026-10-24 1/0, 2026-10-25 0/1, 2026-11-02 1/0" || th.TimeZone != "Europe/Warsaw" || len(th.Periods) != 10 {
		t.Errorf("the days in Warsaw: %s, in %s, %d periods", got, th.TimeZone, len(th.Periods))
	}
	weeks, err := s.Throughput(ctx, "Invoice", ThroughputAsk{By: ThroughputWeek}, flowAnn)
	if err != nil {
		t.Fatal(err)
	}
	if len(weeks.Periods) != 3 || weeks.Periods[0].From != "2026-10-19" || weeks.Periods[0].Created != 1 || weeks.Periods[0].Finished != 1 ||
		weeks.Periods[2].From != "2026-11-02" || weeks.Periods[2].Created != 1 {
		t.Errorf("the weeks in Warsaw: %+v", weeks.Periods)
	}
}
