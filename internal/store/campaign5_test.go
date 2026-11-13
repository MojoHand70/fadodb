package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// TestAccessCampaign is the campaign the records of Phase 5 ask for, on
// disk and beside TestCommitCampaign and TestRulesCampaign, whose rows
// it does not repeat:
//
//   - a transition on a class with its four rules, by the owner of the
//     document, against the 2 ms of a commit, and the same on a class
//     whose rules are TRUE, to compare (ADR-0049);
//   - the transition by an assistant that acts for the owner, an agent
//     with a "for" the engine checks against a substitution (ADR-0050,
//     ADR-0053);
//   - the transition by a content administrator, past the rule and with
//     a reason, which the event keeps (ADR-0052);
//   - a read by id by the owner, and by a caller the read rule hides the
//     document from (ADR-0049);
//   - substitutions given, one commit each, and withdrawn; a read by a
//     substitute whose pair the engine remembers; and the first read of
//     a pair after a commit wrote a substitution, which reads the pair
//     from the database among all the substitutions there are
//     (ADR-0053).
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real disk;
// FADODB_BENCH_COMMITS sizes it, 100,000 by default. It prints a
// Markdown table with -v.
func TestAccessCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on disk to run the campaign of access")
	}
	n := 100000
	if v, err := strconv.Atoi(os.Getenv("FADODB_BENCH_COMMITS")); err == nil && v > 0 {
		n = v
	}
	path := filepath.Join(dir, fmt.Sprintf("campaign5-%d.fado", time.Now().UnixNano()))
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
	}()
	// The worker stays out of the way: the campaign measures the calls,
	// and the wake-ups of the substitutions wait in the outbox.
	s, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	const flow = `{"name": "%s",
 "variables": {
   "owner":  {"type": "text", "default": "IF(ACTINGFOR() <> \"\", ACTINGFOR(), USER())", "index": true},
   "amount": {"type": "number"},
   "note":   {"type": "text"}
 },
 "states": {"initial": "draft", "list": ["draft", "submitted"]},
 "transitions": [
   {"name": "submit", "from": "draft", "to": "submitted", "guard": "amount > 0"},
   {"name": "back", "from": "submitted", "to": "draft"}
 ],
 "access": %s}`
	const open = `{"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}`
	const ruled = `{"read": "owner = USER() OR owner = ACTINGFOR() OR INGROUP(\"finance\")", "create": "INGROUP(\"everyone\")",
  "update": "(owner = USER() OR owner = ACTINGFOR()) AND amount < 100000", "delete": "FALSE"}`
	for name, access := range map[string]string{"Open": open, "Ruled": ruled} {
		if _, err := s.DefineClass(ctx, []byte(fmt.Sprintf(flow, name, access)), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	var table []string
	report := func(work string, calls int, lat []time.Duration, elapsed time.Duration) {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		pct := func(p float64) time.Duration { return lat[min(len(lat)-1, int(float64(len(lat))*p))] }
		table = append(table, fmt.Sprintf("| %s | %s | %s/s | %s | %s | %s | %s |", work, thousands(calls), thousands(int(float64(calls)/elapsed.Seconds())),
			ms(pct(0.5)), ms(pct(0.9)), ms(pct(0.99)), ms(lat[len(lat)-1])))
	}
	owner := Principal{ID: "ann", Kind: ActorPerson, Groups: []string{"sales"}}
	assistant := Principal{ID: "agent-7", Kind: ActorAgent, For: "ann"}
	stranger := Principal{ID: "zed", Kind: ActorPerson}
	cora := Principal{ID: "cora", Kind: ActorPerson, System: []string{GroupContentAdministrators}}
	olaf := Principal{ID: "olaf", Kind: ActorPerson, System: []string{GroupAdministrators}}

	// A fifth of the calls' worth of documents in each class, the owner's.
	docs, batch := max(n/5, 1), 1000
	made := map[string][]string{}
	for _, class := range []string{"Open", "Ruled"} {
		for i := 0; i < docs; i += batch {
			many := make([]Values, min(batch, docs-i))
			for j := range many {
				many[j] = Values{"amount": float64(100 + j%900), "note": "flow"}
			}
			got, err := s.CreateMany(ctx, class, many, As(owner))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range got {
				made[class] = append(made[class], d.ID)
			}
		}
	}
	// Ann lets her assistant act for her, on every class and with no end.
	if _, err := s.Create(ctx, model.SubstitutionClass, Values{"by": assistant.ID}, As(owner)); err != nil {
		t.Fatal(err)
	}
	moved := map[string][]bool{"Open": make([]bool, docs), "Ruled": make([]bool, docs)}
	transitions := func(work, class string, opts ...Option) {
		ids, in := made[class], moved[class]
		lat := make([]time.Duration, 0, n)
		start := time.Now()
		for i := 0; i < n; i++ {
			k := i % len(ids)
			in[k] = !in[k]
			name := "back"
			if in[k] {
				name = "submit"
			}
			t0 := time.Now()
			if _, err := s.Transition(ctx, class, ids[k], name, nil, opts...); err != nil {
				t.Fatalf("%s: %v", work, err)
			}
			lat = append(lat, time.Since(t0))
		}
		report(work, n, lat, time.Since(start))
	}

	// 1 to 4. The transition, by who makes it.
	transitions("transition by the owner, the rules of the class TRUE", "Open", As(owner))
	transitions("transition by the owner, a class with its four rules", "Ruled", As(owner))
	transitions("transition by an assistant that acts for the owner", "Ruled", As(assistant))
	transitions("transition by a content administrator, past the rule, with a reason", "Ruled", As(cora), Reason("the owner is away and the customer waits"))

	// 5 and 6. A read by id: by the owner, and by a caller the read rule
	// hides the document from, who is told it does not exist.
	reads := func(work string, want string, p Principal) {
		ids := made["Ruled"]
		lat := make([]time.Duration, 0, n)
		start := time.Now()
		for i := 0; i < n; i++ {
			t0 := time.Now()
			_, err := s.Get(ctx, "Ruled", ids[i%len(ids)], As(p))
			lat = append(lat, time.Since(t0))
			if got := code(err); want == "" && err != nil || want != "" && got != want {
				t.Fatalf("%s: %v", work, err)
			}
		}
		report(work, n, lat, time.Since(start))
	}
	reads("read by id, by the owner", "", owner)
	reads("read by id, by a caller who may not see it", CodeUnknownDocument, stranger)
	reads("read by id, by the assistant, its pair remembered", "", assistant)

	// 7. Substitutions given, one commit each: every person lets a
	// colleague act for them, from now. The commit creates the document
	// and moves it to active, two versions.
	subs := make([]string, 0, n)
	lat := make([]time.Duration, 0, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		person := Principal{ID: fmt.Sprintf("p%07d", i), Kind: ActorPerson}
		t0 := time.Now()
		d, err := s.Create(ctx, model.SubstitutionClass, Values{"by": fmt.Sprintf("p%07d", (i+1)%n), "classes": []string{"Ruled"}}, As(person))
		if err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
		subs = append(subs, d.ID)
	}
	report("substitution given, one commit each", n, lat, time.Since(start))

	// 8. The first read of a pair after a commit wrote a substitution:
	// the engine reads the pair from the database, among all of them. A
	// database administrator gives one more each round, which is the
	// commit.
	rounds := min(n, 2000)
	lat = lat[:0]
	start = time.Now()
	var between time.Duration
	for i := 0; i < rounds; i++ {
		t1 := time.Now()
		if _, err := s.Create(ctx, model.SubstitutionClass, Values{"by": fmt.Sprintf("q%07d", i), "for": "zed", "reason": "campaign"}, As(olaf)); err != nil {
			t.Fatal(err)
		}
		between += time.Since(t1)
		t0 := time.Now()
		if _, err := s.Get(ctx, "Ruled", made["Ruled"][i%docs], As(assistant)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report(fmt.Sprintf("read by id, by the assistant, its pair read again among %s substitutions", thousands(n+rounds+1)), rounds, lat, time.Since(start)-between)

	// 9. A call refused for a "for" that no substitution covers: a pair
	// the engine remembers as having none.
	lat = lat[:0]
	start = time.Now()
	for i := 0; i < n; i++ {
		t0 := time.Now()
		_, err := s.Get(ctx, "Ruled", made["Ruled"][i%docs], As(Principal{ID: "zed", For: "ann"}))
		lat = append(lat, time.Since(t0))
		if !refusedFor(err) {
			t.Fatalf("a for without a substitution: %v", err)
		}
	}
	report("read by id refused: a for that no substitution covers", n, lat, time.Since(start))

	// 10. Substitutions withdrawn, each by the person who gave it.
	lat = lat[:0]
	start = time.Now()
	for i, id := range subs {
		person := Principal{ID: fmt.Sprintf("p%07d", i), Kind: ActorPerson}
		t0 := time.Now()
		if _, err := s.Transition(ctx, model.SubstitutionClass, id, model.SubstitutionWithdraw, nil, As(person)); err != nil {
			t.Fatal(err)
		}
		lat = append(lat, time.Since(t0))
	}
	report("substitution withdrawn, one commit each", len(subs), lat, time.Since(start))

	// What the log kept of the overrides.
	var overrides int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE override IS NOT NULL AND reason IS NOT NULL`).Scan(&overrides); err != nil {
		t.Fatal(err)
	}
	size := int64(0)
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(path + suffix); err == nil {
			size += fi.Size()
		}
	}
	t.Logf("\n| Work | Calls | Throughput | p50 | p90 | p99 | max |\n|---|---|---|---|---|---|---|\n%s\n\n%s events keep an override and its reason; the engine read the substitutions of a pair from the database %s times; the file is %.0f MB.",
		strings.Join(table, "\n"), thousands(int(overrides)), thousands(int(s.subs.reads.Load())), float64(size)/1e6)
}
