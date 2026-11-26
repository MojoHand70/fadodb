package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// The flow of a class is answered from the copy (ADR-0089, step 2), and
// the answer is the store's: the same documents for the same reader, in
// the same period, and never older than the question.

const flowInvoice = `{
  "name": "Invoice",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "owner":    {"type": "text"},
    "customer": {"type": "text"},
    "net":      {"type": "money", "scale": 2},
    "urgent":   {"type": "bool"},
    "due":      {"type": "date"}
  },
  "states": {"initial": "draft", "list": ["draft", "review", "paid"], "final": ["paid"]},
  "transitions": [
    {"name": "file", "from": "draft", "to": "review"},
    {"name": "send_back", "from": "review", "to": "draft"},
    {"name": "approve", "from": "review", "to": "paid"}
  ],
  "access": {
    "read":   "owner = USER() OR INGROUP(\"finance\") OR (urgent AND INGROUP(\"duty\") AND due >= DATE(2026, 1, 1))",
    "create": "TRUE", "update": "TRUE", "delete": "TRUE"
  }
}`

func person(id string, groups ...string) fado.Option {
	return fado.As(fado.Principal{ID: id, Kind: fado.ActorPerson, Groups: groups})
}

func TestFlow_FromTheCopyIsTheStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	// The clock of the database: every commit moves it on, so that the
	// stays have lengths.
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { now = now.Add(37 * time.Minute); return now }
	withCopy, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer withCopy.Close()
	if _, err := withCopy.DefineClass(ctx, []byte(flowInvoice), olga); err != nil {
		t.Fatal(err)
	}
	ewa, jan, ola, dan, nobody := person("ewa", "finance"), person("jan"), person("ola"), person("dan", "duty"), person("zed")
	var ids []string
	for i := 0; i < 40; i++ {
		owner := []string{"jan", "ola", "ewa"}[i%3]
		d, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": fmt.Sprintf("FV-%d", i), "owner": owner, "customer": "Globex",
			"net": "100.00 PLN", "urgent": i%5 == 0, "due": []string{"2025-12-31", "2026-06-30"}[i%2]}, ewa)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	move := func(name string, which []string) {
		t.Helper()
		out, err := withCopy.TransitionMany(ctx, "Invoice", name, which, nil, ewa)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range out {
			if o.Refusal != nil {
				t.Fatalf("%s: %v", name, o.Refusal)
			}
		}
	}
	move("file", ids[:30])
	middle := now
	move("send_back", ids[:6])
	move("approve", ids[10:22])
	move("file", ids[:3])
	if err := withCopy.Delete(ctx, "Invoice", ids[39], ewa); err != nil {
		t.Fatal(err)
	}
	// A second opening of the same database, with no engine: the store
	// answers there.
	plain, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()

	same := func(when string) {
		t.Helper()
		for name, who := range map[string]fado.Option{"ewa, who reads all": ewa, "jan": jan, "ola": ola, "dan, on duty": dan, "somebody who reads none": nobody} {
			for label, period := range map[string]fado.FlowPeriod{
				"since the class began": {},
				"since the middle":      {Since: middle},
				"until the middle":      {Until: middle},
				"an hour of the middle": {Since: middle.Add(-30 * time.Minute), Until: middle.Add(30 * time.Minute)},
				// How the stays spread, the middle one and the slow tenth
				// (ADR-0095): the store orders the stays and takes the one
				// at each place, the copy asks DuckDB's quantile, and the
				// two are the same.
				"since the class began, with the spread": {Spread: true},
				"since the middle, with the spread":      {Since: middle, Spread: true},
				"until the middle, with the spread":      {Until: middle, Spread: true},
				"an hour of the middle, with the spread": {Since: middle.Add(-30 * time.Minute), Until: middle.Add(30 * time.Minute), Spread: true},
			} {
				before := withCopy.CopyState().Answers
				fromCopy, err := withCopy.Flow(ctx, "Invoice", period, who)
				if err != nil {
					t.Fatalf("%s, %s, %s: the flow from the copy: %v", when, name, label, err)
				}
				if st := withCopy.CopyState(); st.Answers != before+1 || st.AskErr != nil {
					t.Fatalf("%s, %s, %s: the copy did not answer: %+v", when, name, label, st)
				}
				fromStore, err := plain.Flow(ctx, "Invoice", period, who)
				if err != nil {
					t.Fatalf("%s, %s, %s: the flow from the store: %v", when, name, label, err)
				}
				a, _ := json.Marshal(fromCopy)
				b, _ := json.Marshal(fromStore)
				if string(a) != string(b) {
					t.Errorf("%s, %s, %s:\nthe copy  %s\nthe store %s", when, name, label, a, b)
				}
				// The paths the documents took (ADR-0097): the copy
				// reads its moves in the order of their versions, the
				// store its events.
				for _, limit := range []int{0, 2, 1000} {
					ask := fado.PathsAsk{Since: period.Since, Until: period.Until, Limit: limit}
					before := withCopy.CopyState().Answers
					fromCopy, err := withCopy.Paths(ctx, "Invoice", ask, who)
					if err != nil {
						t.Fatalf("%s, %s, %s, limit %d: the paths from the copy: %v", when, name, label, limit, err)
					}
					if st := withCopy.CopyState(); st.Answers != before+1 || st.AskErr != nil {
						t.Fatalf("%s, %s, %s, limit %d: the copy did not answer: %+v", when, name, label, limit, st)
					}
					fromStore, err := plain.Paths(ctx, "Invoice", ask, who)
					if err != nil {
						t.Fatalf("%s, %s, %s, limit %d: the paths from the store: %v", when, name, label, limit, err)
					}
					a, _ := json.Marshal(fromCopy)
					b, _ := json.Marshal(fromStore)
					if string(a) != string(b) {
						t.Errorf("%s, %s, %s, limit %d:\nthe copy  %s\nthe store %s", when, name, label, limit, a, b)
					}
				}
				// How many were made and how many finished in each
				// period (ADR-0096), over the same time: the copy
				// counts its moves, the store its events.
				for _, by := range []string{"", fado.ThroughputDay, fado.ThroughputWeek, fado.ThroughputMonth} {
					ask := fado.ThroughputAsk{Since: period.Since, Until: period.Until, By: by}
					before := withCopy.CopyState().Answers
					fromCopy, err := withCopy.Throughput(ctx, "Invoice", ask, who)
					if err != nil {
						t.Fatalf("%s, %s, %s by %q: the throughput from the copy: %v", when, name, label, by, err)
					}
					if st := withCopy.CopyState(); st.Answers != before+1 || st.AskErr != nil {
						t.Fatalf("%s, %s, %s by %q: the copy did not answer: %+v", when, name, label, by, st)
					}
					fromStore, err := plain.Throughput(ctx, "Invoice", ask, who)
					if err != nil {
						t.Fatalf("%s, %s, %s by %q: the throughput from the store: %v", when, name, label, by, err)
					}
					a, _ := json.Marshal(fromCopy)
					b, _ := json.Marshal(fromStore)
					if string(a) != string(b) {
						t.Errorf("%s, %s, %s by %q:\nthe copy  %s\nthe store %s", when, name, label, by, a, b)
					}
				}
			}
		}
	}
	same("after the moves")
	// What the flow says, so that two wrong answers that agree are not
	// taken for a right one.
	all, err := withCopy.Flow(ctx, "Invoice", fado.FlowPeriod{}, ewa)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]int64{}
	for _, st := range all.States {
		in[st.Name] = st.Documents
	}
	if in["draft"] != 12 || in["review"] != 15 || in["paid"] != 12 || all.Created != 39 {
		t.Errorf("the flow: %d drafts, %d in review, %d paid, %d created; want 12, 15, 12 and 39", in["draft"], in["review"], in["paid"], all.Created)
	}
	// And how many were made and finished: 39 invoices that exist, 12
	// of them paid, in the days the clock of the test went through.
	made, err := withCopy.Throughput(ctx, "Invoice", fado.ThroughputAsk{}, ewa)
	if err != nil {
		t.Fatal(err)
	}
	if made.Created != 39 || made.Finished != 12 || made.By != fado.ThroughputDay || len(made.Periods) < 2 {
		t.Errorf("the throughput: %d made and %d finished in %d days by %s; want 39 and 12 in several days", made.Created, made.Finished, len(made.Periods), made.By)
	}
	// And which ways they went: 39 invoices on five paths, three of
	// them filed, sent back and filed again.
	ways, err := withCopy.Paths(ctx, "Invoice", fado.PathsAsk{}, ewa)
	if err != nil {
		t.Fatal(err)
	}
	took := map[string]int64{}
	for _, p := range ways.Paths {
		took[strings.Join(p.States, ">")] = p.Documents
	}
	if ways.Documents != 39 || ways.Others != 0 || took["draft>review>paid"] != 12 || took["draft>review>draft>review"] != 3 || took["draft>review>draft"] != 3 || took["draft"] != 9 || took["draft>review"] != 12 {
		t.Errorf("the paths: %d documents, %d others, %v", ways.Documents, ways.Others, took)
	}
	mine, err := withCopy.Flow(ctx, "Invoice", fado.FlowPeriod{}, nobody)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range mine.States {
		if st.Documents != 0 || st.Stays != 0 {
			t.Errorf("somebody who reads no invoice sees %d in %s and %d stays", st.Documents, st.Name, st.Stays)
		}
	}

	// An answer is never older than its question: the copy is fed every
	// hour here, and a move is in the flow that is asked right after it.
	move("approve", ids[22:25])
	fresh, err := withCopy.Flow(ctx, "Invoice", fado.FlowPeriod{}, ewa)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range fresh.States {
		if st.Name == "paid" && st.Documents != 15 {
			t.Errorf("right after three more were paid the flow has %d paid, want 15", st.Documents)
		}
	}
	same("after three more were paid")
}

// BenchmarkFlow is the flow of a class of 30,000 tickets that moved
// four times each, asked of a program that keeps a copy and of one
// that keeps none (ADR-0089, step 2). Each asking of the copy brings
// the copy up to the database first, which here finds nothing new. The
// budget of ADR-0017 is for ten million visits and is measured by hand
// on a database of that size; this one tells a change that makes the
// copy's answer slower.
func BenchmarkFlow(b *testing.B) {
	ticket := `{"name": "Ticket", "title": "number",
 "variables": {"number": {"type": "text", "required": true}, "owner": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "held"]},
 "transitions": [{"name": "hold", "from": "open", "to": "held"}, {"name": "release", "from": "held", "to": "open"}],
 "access": {"read": "owner = USER() OR INGROUP(\"support\")", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "acme.fado")
	lead := person("lea", "support")
	load, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := load.DefineClass(ctx, []byte(ticket), olga); err != nil {
		b.Fatal(err)
	}
	const tickets, batch = 30000, 1000
	for have := 0; have < tickets; have += batch {
		docs := make([]fado.Values, batch)
		for i := range docs {
			docs[i] = fado.Values{"number": fmt.Sprintf("T-%d", have+i), "owner": []string{"jan", "ola", "lea"}[i%3]}
		}
		made, err := load.CreateMany(ctx, "Ticket", docs, lead)
		if err != nil {
			b.Fatal(err)
		}
		ids := make([]string, len(made))
		for i, d := range made {
			ids[i] = d.ID
		}
		for _, move := range []string{"hold", "release", "hold", "release"} {
			if _, err := load.TransitionMany(ctx, "Ticket", move, ids, nil, lead); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := load.Close(); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		o    fado.Options
	}{
		{"from the copy", fado.Options{Analytics: Engine{Threads: 4}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}}},
		{"from the store", fado.Options{Outbox: fado.OutboxOptions{Manual: true}}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			db, err := fado.Open(path, tc.o)
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			if err := db.FeedCopy(ctx); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				f, err := db.Flow(ctx, "Ticket", fado.FlowPeriod{}, lead)
				if err != nil || f.Created != tickets {
					b.Fatalf("the flow of the benchmark: %v, %+v", err, f)
				}
			}
			b.StopTimer()
			if st := db.CopyState(); st.Kept && (st.AskErr != nil || st.Answers < int64(b.N)) {
				b.Fatalf("the copy did not answer: %+v", st)
			}
		})
	}
}

// The middle stay and the slow tenth are the lengths of the stays at
// two places among the stays in the order of their lengths (ADR-0095):
// of n stays, at place (n+1)/2 and at place (9n+9)/10, rounded down.
// The store takes them by a count, DuckDB by its quantile, and the test
// by sorting: at every number of stays from one to forty-five, tens and
// odd numbers among them, the three agree.
func TestFlow_TheSpreadAtEveryNumberOfStays(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	withCopy, err := fado.Open(path, fado.Options{Analytics: Engine{Threads: 2}, CopyEvery: time.Hour, Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer withCopy.Close()
	if _, err := withCopy.DefineClass(ctx, []byte(flowInvoice), olga); err != nil {
		t.Fatal(err)
	}
	plain, err := fado.Open(path, fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	ewa := person("ewa", "finance")
	var lengths []int64
	seed := uint32(7)
	for n := 1; n <= 45; n++ {
		// A stay in draft of a length that repeats now and then, so
		// that equal lengths stand side by side too.
		seed = seed*1664525 + 1013904223
		length := int64(seed>>8) % 50 * 60
		d, err := withCopy.Create(ctx, "Invoice", fado.Values{"number": fmt.Sprintf("FV-%d", n), "owner": "ewa", "customer": "Globex", "net": "1.00 PLN"}, ewa)
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Duration(length) * time.Second)
		if _, err := withCopy.Transition(ctx, "Invoice", d.ID, "file", nil, ewa); err != nil {
			t.Fatal(err)
		}
		lengths = append(lengths, length)
		sorted := append([]int64(nil), lengths...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		want := fmt.Sprintf("%d/%d", sorted[(n+1)/2-1], sorted[(9*n+9)/10-1])
		for program, db := range map[string]*fado.DB{"the program with the copy": withCopy, "the program without it": plain} {
			before := withCopy.CopyState().Answers
			f, err := db.Flow(ctx, "Invoice", fado.FlowPeriod{Spread: true}, ewa)
			if err != nil {
				t.Fatalf("%d stays, %s: %v", n, program, err)
			}
			if db == withCopy && withCopy.CopyState().Answers != before+1 {
				t.Fatalf("%d stays: the copy did not answer: %+v", n, withCopy.CopyState())
			}
			got := ""
			for _, st := range f.States {
				if st.Name == "draft" && st.Spread != nil {
					got = fmt.Sprintf("%d/%d", st.Spread.MiddleSeconds, st.Spread.SlowTenthSeconds)
				}
			}
			if got != want {
				t.Errorf("%d stays, %s: the middle stay and the slow tenth are %s, want %s", n, program, got, want)
			}
		}
	}
}
