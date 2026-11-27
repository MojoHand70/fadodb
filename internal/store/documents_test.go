package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// drillStore is the store of the tests of the documents behind a
// number: the invoices of the flow's tests, one sent back and submitted
// again, one the engine cancels at once, one made just after midnight
// in Warsaw, where the database keeps its days, and one deleted.
func drillStore(t *testing.T) (*Store, *still) {
	t.Helper()
	cl := &still{now: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: cl.Now, TimeZone: "Europe/Warsaw"})
	ctx := context.Background()
	back := strings.Replace(flowInvoice, `{"name": "submit", "from": "draft", "to": "review"},`, `{"name": "submit", "from": "draft", "to": "review"}, {"name": "send_back", "from": "review", "to": "draft"},`, 1)
	if _, err := s.DefineClass(ctx, []byte(back), asSetup); err != nil {
		t.Fatal(err)
	}
	must := func(_ *Document, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// Monday 9:00 UTC.
	for _, id := range []string{"B-1", "B-2", "B-3", "B-4", "B-9"} {
		must(s.Create(ctx, "Invoice", Values{"total": "100.00"}, flowBob, WithID(id)))
	}
	must(s.Create(ctx, "Invoice", Values{"total": "0.00"}, flowDan, WithID("D-1")))
	// 10:00: three submitted; Dan's, of nothing, cancelled by the
	// engine in the same commit; B-4 sent back and submitted again.
	cl.add(time.Hour)
	for _, id := range []string{"B-1", "B-2", "B-4", "B-9"} {
		must(s.Transition(ctx, "Invoice", id, "submit", nil, flowBob))
	}
	must(s.Transition(ctx, "Invoice", "D-1", "submit", nil, flowDan))
	must(s.Transition(ctx, "Invoice", "B-4", "send_back", nil, flowAnn))
	must(s.Transition(ctx, "Invoice", "B-4", "submit", nil, flowBob))
	// 13:00: two approved, three hours after they were submitted.
	cl.add(3 * time.Hour)
	must(s.Transition(ctx, "Invoice", "B-1", "approve", nil, flowAnn))
	must(s.Transition(ctx, "Invoice", "B-4", "approve", nil, flowAnn))
	// Tuesday 13:00: B-3 cancelled. 23:30 UTC, half past midnight on
	// Wednesday in Warsaw: B-6 made, and B-9 approved.
	cl.add(24 * time.Hour)
	must(s.Transition(ctx, "Invoice", "B-3", "cancel", nil, flowBob))
	cl.add(10*time.Hour + 30*time.Minute)
	must(s.Create(ctx, "Invoice", Values{"total": "5.00"}, flowBob, WithID("B-6")))
	must(s.Transition(ctx, "Invoice", "B-9", "approve", nil, flowAnn))
	// Wednesday 13:30 UTC: one more, the usual way.
	cl.add(14 * time.Hour)
	must(s.Create(ctx, "Invoice", Values{"total": "9.00"}, flowBob, WithID("B-5")))
	must(s.Transition(ctx, "Invoice", "B-5", "submit", nil, flowBob))
	must(s.Transition(ctx, "Invoice", "B-5", "approve", nil, flowAnn))
	// B-2 is deleted: it is behind no number.
	if err := s.Delete(ctx, "Invoice", "B-2", flowBob); err != nil {
		t.Fatal(err)
	}
	return s, cl
}

// drillRowsText writes the rows of documents as id@version by who, for
// a test to compare.
func drillRowsText(d *Documents) string {
	out := make([]string, len(d.Rows))
	for i, r := range d.Rows {
		out[i] = r.ID
		if r.Version != 0 {
			out[i] += fmt.Sprintf("@%d", r.Version)
		}
		if r.Seconds != nil {
			out[i] += fmt.Sprintf(" %ds", *r.Seconds)
		}
		if r.By != "" {
			out[i] += " by " + r.By
		}
	}
	return strings.Join(out, ", ")
}

// Every number of a flow, a throughput and the paths opens to as many
// documents as it says, for every caller and every period (ADR-0099,
// part 1, item 3).
func TestEveryNumberOpensToItsDocuments(t *testing.T) {
	s, _ := drillStore(t)
	testEveryNumberOpens(t, s)
}

// testEveryNumberOpens is the test of the numbers, for the store alone
// here and for the program with the copy in the analytical module.
func testEveryNumberOpens(t *testing.T, s *Store) {
	ctx := context.Background()
	opened := func(what string, d *Documents, err error, want int64) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if d.Count != want || int64(len(d.Rows)) != min(want, DefaultDocuments) || d.More != (want > DefaultDocuments) {
			t.Errorf("%s opens to %d documents, %d rows, more %v; the number is %d", what, d.Count, len(d.Rows), d.More, want)
		}
	}
	tuesday := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	periods := []FlowPeriod{{}, {Since: tuesday}, {Until: tuesday}, {Since: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Until: time.Date(2026, 11, 2, 13, 0, 0, 0, time.UTC)}}
	for _, who := range []struct {
		name string
		as   Option
	}{{"Ann", flowAnn}, {"Bob", flowBob}, {"Eve", flowEve}} {
		for _, period := range periods {
			f, err := s.Flow(ctx, "Invoice", period, who.as)
			if err != nil {
				t.Fatal(err)
			}
			at := fmt.Sprintf("%s over %v to %v", who.name, period.Since, period.Until)
			d, err := s.FlowDocuments(ctx, "Invoice", period, DocumentsAsk{Created: true}, who.as)
			opened(at+": created", d, err, f.Created)
			for _, st := range f.States {
				d, err := s.FlowDocuments(ctx, "Invoice", period, DocumentsAsk{State: st.Name}, who.as)
				opened(at+": in "+st.Name, d, err, st.Documents)
				d, err = s.FlowDocuments(ctx, "Invoice", period, DocumentsAsk{Stays: st.Name}, who.as)
				opened(at+": stays in "+st.Name, d, err, st.Stays)
			}
			for _, tr := range f.Transitions {
				ask := DocumentsAsk{Transition: tr.Name, From: tr.From, To: tr.To}
				d, err := s.FlowDocuments(ctx, "Invoice", period, ask, who.as)
				opened(fmt.Sprintf("%s: along %q from %s to %s", at, tr.Name, tr.From, tr.To), d, err, tr.Moves)
			}
			// The stays at least the middle and the slow tenth are
			// those of all the stays that lasted as long.
			spread, err := s.Flow(ctx, "Invoice", FlowPeriod{Since: period.Since, Until: period.Until, Spread: true}, who.as)
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range spread.States {
				all, err := s.FlowDocuments(ctx, "Invoice", period, DocumentsAsk{Stays: st.Name}, who.as)
				if err != nil {
					t.Fatal(err)
				}
				for least, from := range map[string]int64{AtLeastMiddle: st.Spread.MiddleSeconds, AtLeastSlowTenth: st.Spread.SlowTenthSeconds} {
					want := int64(0)
					for _, r := range all.Rows {
						if r.Seconds != nil && *r.Seconds >= from {
							want++
						}
					}
					d, err := s.FlowDocuments(ctx, "Invoice", period, DocumentsAsk{Stays: st.Name, AtLeast: least}, who.as)
					opened(fmt.Sprintf("%s: stays in %s at least the %s, %d s", at, st.Name, least, from), d, err, want)
				}
			}
		}
		for _, by := range []string{ThroughputDay, ThroughputWeek, ThroughputMonth} {
			for _, ask := range []ThroughputAsk{{By: by}, {By: by, Since: tuesday}} {
				tp, err := s.Throughput(ctx, "Invoice", ask, who.as)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range tp.Periods {
					at := fmt.Sprintf("%s by %s since %v, %s", who.name, by, ask.Since, p.From)
					d, err := s.ThroughputDocuments(ctx, "Invoice", ask, DocumentsAsk{Made: p.From}, who.as)
					opened(at+": made", d, err, p.Created)
					d, err = s.ThroughputDocuments(ctx, "Invoice", ask, DocumentsAsk{Finished: p.From}, who.as)
					opened(at+": finished", d, err, p.Finished)
				}
			}
		}
		for _, ask := range []PathsAsk{{Limit: MaxPaths}, {Limit: MaxPaths, Since: tuesday}} {
			ps, err := s.Paths(ctx, "Invoice", ask, who.as)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range ps.Paths {
				d, err := s.PathsDocuments(ctx, "Invoice", ask, DocumentsAsk{Path: p.States}, who.as)
				opened(fmt.Sprintf("%s since %v: path %v", who.name, ask.Since, p.States), d, err, p.Documents)
			}
		}
	}
}

// What the rows say and their order: the moves newest first, each with
// the version it made and who made it; the stays longest first, with
// how long; the documents in a state the one there longest first; the
// days of Warsaw (ADR-0099, part 1, items 2 and 4).
func TestTheRowsBehindANumber(t *testing.T) {
	s, _ := drillStore(t)
	ctx := context.Background()
	rows := func(what string, d *Documents, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return drillRowsText(d)
	}
	d, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "submit"}, flowAnn)
	if got, want := rows("submit", d, err), "B-5@2 by bob, B-1@2 by bob, B-4@2 by bob, B-4@4 by bob, B-9@2 by bob, D-1@2 by dan"; got != want {
		t.Errorf("the moves along submit are %s, want %s", got, want)
	}
	if r := d.Rows[0]; r.Title != "" && r.Title != "B-5" || r.State != "approved" || r.At != time.Date(2026, 11, 4, 13, 30, 0, 0, time.UTC).Unix() {
		t.Errorf("the first row is %+v", r)
	}
	d, err = s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Stays: "review"}, flowAnn)
	if got, want := rows("stays", d, err), "B-9@3 135000s by ann, B-1@3 10800s by ann, B-4@5 10800s by ann, B-5@3 0s by ann, B-4@3 0s by ann, D-1@3 0s by dan"; got != want {
		t.Errorf("the stays in review are %s, want %s", got, want)
	}
	d, err = s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{From: "review", To: "cancelled"}, flowAnn)
	if got := rows("the move with no name", d, err); got != "D-1@3 by dan" {
		t.Errorf("the moves along the transition with no name are %s", got)
	}
	d, err = s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{State: "approved"}, flowAnn)
	if got := rows("approved now", d, err); got != "B-1@3 by ann, B-4@5 by ann, B-9@3 by ann, B-5@3 by ann" {
		t.Errorf("the invoices approved now are %s", got)
	}
	// Half past midnight in Warsaw is Wednesday: B-6 was made then, and
	// B-9 finished.
	d, err = s.ThroughputDocuments(ctx, "Invoice", ThroughputAsk{}, DocumentsAsk{Made: "2026-11-04"}, flowAnn)
	if got := rows("made on Wednesday", d, err); got != "B-5@1 by bob, B-6@1 by bob" {
		t.Errorf("the invoices made on Wednesday in Warsaw are %s", got)
	}
	d, err = s.ThroughputDocuments(ctx, "Invoice", ThroughputAsk{}, DocumentsAsk{Finished: "2026-11-03"}, flowAnn)
	if got := rows("finished on Tuesday", d, err); got != "B-3@2 by bob" {
		t.Errorf("the invoices finished on Tuesday in Warsaw are %s", got)
	}
	d, err = s.PathsDocuments(ctx, "Invoice", PathsAsk{}, DocumentsAsk{Path: []string{"draft", "review", "draft", "review", "approved"}}, flowAnn)
	if got := rows("the way round", d, err); got != "B-4 by bob" {
		t.Errorf("the invoices that went round are %s", got)
	}
	// Bob reads his own: Dan's is behind no number of his.
	d, err = s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "submit"}, flowBob)
	if got := rows("Bob's submit", d, err); strings.Contains(got, "D-1") || d.Count != 5 {
		t.Errorf("Bob's moves along submit are %d: %s", d.Count, got)
	}
	// A page at a time.
	d, err = s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "submit", Limit: 2, Offset: 1}, flowAnn)
	if got := rows("a page", d, err); got != "B-1@2 by bob, B-4@2 by bob" || d.Count != 6 || !d.More {
		t.Errorf("the second page of two is %s, count %d, more %v", got, d.Count, d.More)
	}
	d, err = s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "submit", Offset: 10}, flowAnn)
	if got := rows("past the end", d, err); got != "" || d.Count != 6 || d.More {
		t.Errorf("a page past the end is %q, count %d, more %v", got, d.Count, d.More)
	}
}

// What is refused, each with its place and a hint (ADR-0099, part 1,
// item 6).
func TestTheDocumentsBehindANumberRefused(t *testing.T) {
	s, _ := drillStore(t)
	ctx := context.Background()
	for _, c := range []struct {
		what string
		ask  func() error
		path string
	}{
		{"two numbers", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{State: "review", Transition: "submit"}, flowAnn)
			return err
		}, "/documents"},
		{"no number", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{}, flowAnn)
			return err
		}, "/documents"},
		{"a throughput's number of a flow", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Made: "2026-11-02"}, flowAnn)
			return err
		}, "/documents/made"},
		{"a flow's number of the paths", func() error {
			_, err := s.PathsDocuments(ctx, "Invoice", PathsAsk{}, DocumentsAsk{State: "review"}, flowAnn)
			return err
		}, "/documents/state"},
		{"a state nobody was in", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{State: "revew"}, flowAnn)
			return err
		}, "/documents/state"},
		{"a transition nobody moved along", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "aprove"}, flowAnn)
			return err
		}, "/documents/transition"},
		{"at_least without stays", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{State: "review", AtLeast: AtLeastMiddle}, flowAnn)
			return err
		}, "/documents/at_least"},
		{"an at_least of another word", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Stays: "review", AtLeast: "slowest"}, flowAnn)
			return err
		}, "/documents/at_least"},
		{"a day that begins no week", func() error {
			_, err := s.ThroughputDocuments(ctx, "Invoice", ThroughputAsk{By: ThroughputWeek}, DocumentsAsk{Finished: "2026-11-04"}, flowAnn)
			return err
		}, "/documents/finished"},
		{"a day that is no day", func() error {
			_, err := s.ThroughputDocuments(ctx, "Invoice", ThroughputAsk{}, DocumentsAsk{Made: "Wednesday"}, flowAnn)
			return err
		}, "/documents/made"},
		{"made and finished", func() error {
			_, err := s.ThroughputDocuments(ctx, "Invoice", ThroughputAsk{}, DocumentsAsk{Made: "2026-11-02", Finished: "2026-11-02"}, flowAnn)
			return err
		}, "/documents"},
		{"no path", func() error {
			_, err := s.PathsDocuments(ctx, "Invoice", PathsAsk{}, DocumentsAsk{}, flowAnn)
			return err
		}, "/documents/path"},
		{"too long a page", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Created: true, Limit: MaxDocuments + 1}, flowAnn)
			return err
		}, "/documents/limit"},
		{"an offset below nothing", func() error {
			_, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Created: true, Offset: -1}, flowAnn)
			return err
		}, "/documents/offset"},
	} {
		err := c.ask()
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeBadValue || e.Path != c.path || e.Hint == "" {
			t.Errorf("%s: %v, want bad_value at %s with a hint", c.what, err, c.path)
		}
	}
	// A state the class has, in which no document is, is answered.
	d, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{State: "draft"}, flowAnn)
	if err != nil || d.Count != 1 {
		t.Errorf("the invoices in draft: %+v, %v", d, err)
	}
}

// A content administrator who opens a number past the rule gives a
// reason, and the log of reads names the documents only the override
// showed (ADR-0099, part 1, item 5; ADR-0067).
func TestTheDocumentsBehindANumberPastTheRule(t *testing.T) {
	s, _ := drillStore(t)
	ctx := context.Background()
	cora := As(Principal{ID: "cora", System: []string{GroupContentAdministrators}})
	d, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "submit"}, cora, Reason("the audit of November"))
	if err != nil || d.Count != 6 {
		t.Fatalf("cora's moves along submit: %+v, %v", d, err)
	}
	reads, err := s.Reads(ctx, ReadsFilter{}, As(Principal{ID: "root", System: []string{GroupAdministrators}}))
	if err != nil {
		t.Fatal(err)
	}
	var entry *Read
	for i := range reads {
		if reads[i].Command == "flow" && reads[i].By == "cora" {
			entry = &reads[i]
		}
	}
	if entry == nil || entry.Reason != "the audit of November" || strings.Join(entry.Documents, ",") != "B-5,B-1,B-4,B-9,D-1" {
		t.Errorf("the log of reads keeps %+v", entry)
	}
	// Without a reason she is held by the rule as anybody, and the log
	// keeps nothing.
	if d, err := s.FlowDocuments(ctx, "Invoice", FlowPeriod{}, DocumentsAsk{Transition: "submit"}, cora); err == nil && d.Count != 0 {
		t.Errorf("cora without a reason opens %d moves", d.Count)
	}
}
