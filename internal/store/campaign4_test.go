package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRulesCampaign is the campaign the records of Phase 4 ask for, on
// disk and beside TestCommitCampaign, whose rows it does not repeat:
//
//   - a transition that fires three rules with two sets, a custom event
//     and a triggered variable, and a refresh, against the 2 ms of a
//     commit (ADR-0043);
//   - a transition that queues one action, one that queues ten with
//     each, and the outbox drained: an engine action and a registered
//     action that does nothing, against 2,000 a second (ADR-0044);
//   - a refresh that counts over the documents of another class through
//     an index and moves by its guard, and a count that no index
//     narrows (ADR-0046);
//   - a refresh whose formula reads the life of a document with a long
//     history, by its moves and by its editors (ADR-0047, item 6);
//   - a transition that queues a wake-up, wake-ups that find nothing to
//     do, and timers that fire (ADR-0047, items 10 and 11).
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real disk;
// FADODB_BENCH_COMMITS sizes it, 100,000 by default. It prints a
// Markdown table with -v.
func TestRulesCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the campaign of rules and actions")
	}
	n := 100000
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_COMMITS")); err == nil && v > 0 {
		n = v
	}
	path := filepath.Join(dir, fmt.Sprintf("rules-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	// The clock is the machine's, moved on by the test where timers are
	// to come.
	var ahead atomic.Int64
	calls := 0
	s, err := Open(path, Options{
		Now:     func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) },
		Outbox:  OutboxOptions{Manual: true},
		Actions: map[string]ActionFunc{"books.post": func(context.Context, *ActionCall) (Values, error) { calls++; return nil, nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "books.post", "params": {"job": {"type": "text", "required": true}, "amount": "number"}}`)); err != nil {
		t.Fatal(err)
	}
	for _, def := range []string{
		// Three rules on the way from draft to review, two sets among
		// them, a custom event, and a triggered variable an auto formula
		// reads.
		`{"name": "Deal", "variables": {
   "amount": {"type": "number"}, "qty": {"type": "number"}, "rate": {"type": "number"}, "note": {"type": "text"}, "owner": {"type": "text"},
   "total": {"formula": "amount * qty"},
   "locked": {"formula": "rate", "mode": "triggered", "on": ["transition:submit", "refresh"]},
   "price": {"formula": "amount * locked"}},
 "states": {"initial": "draft", "list": ["draft", "review"]},
 "transitions": [{"name": "submit", "from": "draft", "to": "review", "guard": "amount > 0"}, {"name": "back", "from": "review", "to": "draft"}],
 "rules": [
   {"on": "exit", "state": "draft", "do": [{"action": "set", "values": {"owner": "USER()", "qty": "qty + 1"}}]},
   {"on": "transition", "name": "submit", "if": "total > 0", "do": [{"action": "emit", "event": "deal.submitted"}]},
   {"on": "enter", "state": "review", "do": [{"action": "set", "values": {"note": "\"in review\""}}]}]}`,
		`{"name": "Entry", "variables": {"job": {"type": "text", "required": true}, "amount": {"type": "number"}, "who": {"type": "text"}}, "states": {"initial": "booked", "list": ["booked"]}}`,
		// One transition for each kind of action a commit queues.
		`{"name": "Job", "variables": {"amount": {"type": "number"}, "crew": {"type": "array<text>"}},
 "states": {"initial": "open", "list": ["open"]},
 "transitions": [{"name": "book", "from": "open", "to": "open"}, {"name": "post", "from": "open", "to": "open"}, {"name": "staff", "from": "open", "to": "open"}],
 "rules": [
   {"on": "transition", "name": "book", "do": [{"action": "create", "class": "Entry", "values": {"job": "ID()", "amount": "amount"}}]},
   {"on": "transition", "name": "post", "do": [{"action": "books.post", "args": {"job": "ID()", "amount": "amount"}}]},
   {"on": "transition", "name": "staff", "do": [{"action": "create", "class": "Entry", "each": "crew", "as": "member", "values": {"job": "ID()", "who": "member"}}]}]}`,
		// The pattern of parallel approvals: a purchase counts its
		// approvals through the index of what they name, and moves when
		// half are granted. A scan counts with no index to narrow it.
		`{"name": "Approval", "variables": {"purchase": {"type": "text", "index": true}, "amount": {"type": "number"}, "ok": {"type": "bool"}},
  "states": {"initial": "asked", "list": ["asked"]}}`,
		`{"name": "Purchase", "variables": {"granted": {"type": "number", "formula": "LOOKUP(Approval, COUNT(id), purchase = ID() AND ok)", "mode": "triggered", "on": ["refresh"]}},
  "states": {"initial": "waiting", "list": ["waiting", "approved"]},
  "transitions": [{"from": "waiting", "to": "approved", "guard": "granted >= 50"}]}`,
		`{"name": "Scan", "variables": {"large": {"type": "number", "formula": "LOOKUP(Approval, COUNT(id), amount > 5)", "mode": "triggered", "on": ["refresh"]}},
  "states": {"initial": "idle", "list": ["idle"]}}`,
		// A formula that reads the life of its document: by its moves,
		// and by who changed it.
		`{"name": "Moved", "variables": {"n": {"type": "number"}, "rounds": {"type": "number", "formula": "ENTRIES(\"review\")", "mode": "triggered", "on": ["refresh"]}},
  "states": {"initial": "draft", "list": ["draft", "review"]},
  "transitions": [{"name": "submit", "from": "draft", "to": "review"}, {"name": "back", "from": "review", "to": "draft"}]}`,
		`{"name": "Edited", "variables": {"n": {"type": "number"}, "writer": {"type": "text", "formula": "LASTEDITOR(\"draft\")", "mode": "triggered", "on": ["refresh"]}},
  "states": {"initial": "draft", "list": ["draft", "review"]},
  "transitions": [{"name": "submit", "from": "draft", "to": "review"}, {"name": "back", "from": "review", "to": "draft"}]}`,
		// A state a timer leaves, entered and left by hand.
		`{"name": "Shift", "variables": {"n": {"type": "number"}},
 "states": {"initial": "busy", "list": ["busy", "idle", "over"]},
 "transitions": [{"name": "rest", "from": "busy", "to": "idle"}, {"name": "work", "from": "idle", "to": "busy"}, {"from": "idle", "to": "over", "after": "1h"}]}`,
	} {
		if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
			t.Fatal(err)
		}
	}
	var table []string
	report := func(work string, commits int, lat []time.Duration, elapsed time.Duration) {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		pct := func(p float64) time.Duration { return lat[min(len(lat)-1, int(float64(len(lat))*p))] }
		table = append(table, fmt.Sprintf("| %s | %s | %s/s | %s | %s | %s | %s |", work, thousands(commits), thousands(int(float64(commits)/elapsed.Seconds())),
			ms(pct(0.5)), ms(pct(0.9)), ms(pct(0.99)), ms(lat[len(lat)-1])))
	}
	// rate reports work that is not timed one by one: a drain.
	rate := func(work string, count int, elapsed time.Duration) {
		table = append(table, fmt.Sprintf("| %s | %s | %s/s | | | | |", work, thousands(count), thousands(int(float64(count)/elapsed.Seconds()))))
	}
	// drain runs the outbox and reports how fast count rows ran.
	drain := func(work string, want int) {
		t.Helper()
		start := time.Now()
		got, err := s.Drain(ctx)
		if err != nil || got != want {
			t.Fatalf("%s: drained %d rows, %v; want %d", work, got, err, want)
		}
		rate(work, got, time.Since(start))
	}
	const batch = 1000
	many := func(class string, count int, values func(i int) Values) []string {
		t.Helper()
		ids := make([]string, 0, count)
		for i := 0; i < count; i += batch {
			vals := make([]Values, min(batch, count-i))
			for j := range vals {
				vals[j] = values(i + j)
			}
			made, err := s.CreateMany(ctx, class, vals, As(alice))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range made {
				ids = append(ids, d.ID)
			}
		}
		return ids
	}
	docs := max(1, n/5)
	lat := make([]time.Duration, 0, n)

	// 1. Rules inside the commit: every submit fires three rules; the
	// back between two is not measured.
	deals := many("Deal", docs, func(i int) Values {
		return Values{"amount": float64(100 + i%900), "qty": float64(i%9 + 1), "rate": 0.5, "note": "deal"}
	})
	var spent time.Duration
	for i := 0; i < n; i++ {
		id := deals[i%len(deals)]
		t0 := time.Now()
		if _, err := s.Transition(ctx, "Deal", id, "submit", nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		spent += lat[len(lat)-1]
		if _, err := s.Transition(ctx, "Deal", id, "back", nil, As(alice)); err != nil {
			t.Fatal(err)
		}
	}
	report("transition with three rules, two sets, a custom event and a triggered variable", n, lat, spent)

	// 2. A refresh: the triggered variable computed again, the auto
	// formula that reads it recalculated, a version written.
	lat = lat[:0]
	start := time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if _, err := s.Refresh(ctx, "Deal", deals[i%len(deals)], nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("refresh of a triggered variable", n, lat, time.Since(start))

	// 3. The outbox. A transition that queues one action of the engine,
	// then the drain that runs them all: the document each creates, the
	// mark and the event of the outcome, one transaction for each.
	jobs := many("Job", docs, func(i int) Values {
		return Values{"amount": float64(i % 1000), "crew": []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}}
	})
	queue := func(work, transition string, count int) {
		t.Helper()
		lat = lat[:0]
		start := time.Now()
		for i := 0; i < count; i++ {
			t0 := time.Now()
			if _, err := s.Transition(ctx, "Job", jobs[i%len(jobs)], transition, nil, As(alice)); err != nil {
				t.Fatal(err)
			}
			lat = append(lat, time.Since(t0))
		}
		report(work, count, lat, time.Since(start))
	}
	queue("transition that queues one action", "book", n)
	drain("drain: an engine action, create", n)
	queue("transition that queues one registered action", "post", n)
	drain("drain: a registered action that does nothing", n)
	if calls != n {
		t.Fatalf("the registered action was called %d times for %d rows", calls, n)
	}
	tens := max(1, n/10)
	queue("transition that queues ten creates with each", "staff", tens)
	drain("drain: the ten creates of each", tens*10)

	// 4. Formulas that read other documents. Every purchase has a
	// hundred approvals among all of them, half granted; its refresh
	// counts them through the index and its guard moves it.
	purchases := max(1, n/100)
	many("Approval", purchases*100, func(i int) Values {
		return Values{"purchase": fmt.Sprintf("P-%07d", i/100), "amount": float64(i % 10), "ok": i%2 == 0}
	})
	asking := make([]string, purchases)
	for i := range asking {
		asking[i] = fmt.Sprintf("P-%07d", i)
		if _, err := s.Create(ctx, "Purchase", nil, WithID(asking[i]), As(alice)); err != nil {
			t.Fatal(err)
		}
	}
	lat = lat[:0]
	start = time.Now()
	for _, id := range asking {
		t0 := time.Now()
		doc, err := s.Refresh(ctx, "Purchase", id, nil, As(alice))
		if err != nil || doc.State != "approved" {
			t.Fatalf("the purchase %s: %+v %v", id, doc, err)
		}
		lat = append(lat, time.Since(t0))
	}
	report(fmt.Sprintf("refresh that counts 50 of %s approvals through an index and moves by its guard", thousands(purchases*100)), purchases, lat, time.Since(start))
	scan, err := s.Create(ctx, "Scan", nil, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < 20; i++ {
		t0 := time.Now()
		if _, err := s.Refresh(ctx, "Scan", scan.ID, nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report(fmt.Sprintf("refresh that counts over %s approvals with no index", thousands(purchases*100)), 20, lat, time.Since(start))

	// 5. The life of a document with a long history: a refresh whose
	// formula counts its entries into a state reads its moves, and one
	// that asks for an editor reads every change.
	for _, class := range []string{"Moved", "Edited"} {
		doc, err := s.Create(ctx, class, Values{"n": 0}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		versions := int64(1)
		for _, history := range []int64{100, 1000, 10000} {
			if history > int64(n) {
				break
			}
			// A set, a submit and a back in turn, so that a third of the
			// versions are changes and two thirds moves.
			for i := 0; versions < history; i++ {
				switch i % 3 {
				case 0:
					_, err = s.Set(ctx, class, doc.ID, Values{"n": float64(versions)}, As(alice))
				case 1:
					_, err = s.Transition(ctx, class, doc.ID, "submit", nil, As(alice))
				default:
					_, err = s.Transition(ctx, class, doc.ID, "back", nil, As(alice))
				}
				if err != nil {
					t.Fatal(err)
				}
				versions++
			}
			const reads = 200
			lat = lat[:0]
			start = time.Now()
			for i := 0; i < reads; i++ {
				t0 := time.Now()
				if _, err := s.Refresh(ctx, class, doc.ID, nil, As(alice)); err != nil {
					t.Fatal(err)
				}
				lat = append(lat, time.Since(t0))
			}
			versions += reads
			what := "its moves"
			if class == "Edited" {
				what = "its editors"
			}
			report(fmt.Sprintf("refresh that reads %s, a document of %s versions", what, thousands(int(history))), reads, lat, time.Since(start))
		}
	}

	// 6. Timers. A transition into a state a timer leaves queues a
	// wake-up; the move out again ends the stay, so an hour later those
	// wake-ups find nothing to do.
	shifts := many("Shift", docs, func(i int) Values { return Values{"n": float64(i)} })
	lat = lat[:0]
	spent = 0
	for i := 0; i < n; i++ {
		id := shifts[i%len(shifts)]
		t0 := time.Now()
		if _, err := s.Transition(ctx, "Shift", id, "rest", nil, As(alice)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		spent += lat[len(lat)-1]
		if _, err := s.Transition(ctx, "Shift", id, "work", nil, As(alice)); err != nil {
			t.Fatal(err)
		}
	}
	report("transition that queues a wake-up", n, lat, spent)
	ahead.Add(int64(time.Hour))
	drain("drain: wake-ups that find their document in another state", n)
	// Every shift rests, and an hour later every timer fires.
	for _, id := range shifts {
		if _, err := s.Transition(ctx, "Shift", id, "rest", nil, As(alice)); err != nil {
			t.Fatal(err)
		}
	}
	ahead.Add(int64(time.Hour))
	drain("drain: timers that fire, a transition each", len(shifts))
	if over, err := s.Count(ctx, Filter{Class: "Shift", Where: `state = "over"`}); err != nil || int(over) != len(shifts) {
		t.Fatalf("%d of %d shifts are over, %v", over, len(shifts), err)
	}

	size := int64(0)
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(path + suffix); err == nil {
			size += fi.Size()
		}
	}
	t.Logf("\n| Work | Count | Throughput | p50 | p90 | p99 | max |\n|---|---|---|---|---|---|---|\n%s\n\nThe file and its log are %.0f MB.", strings.Join(table, "\n"), float64(size)/1e6)
}
