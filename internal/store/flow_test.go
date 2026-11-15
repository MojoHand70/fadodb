package store

import (
	"context"
	"encoding/json"
	"errors"
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
