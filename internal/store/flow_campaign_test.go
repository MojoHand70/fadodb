package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/store"
)

// flowCampaignInvoice is the class of the campaign of the flow: sales
// reads its own invoices and finance every one, so that the read rule
// is asked of every document.
const flowCampaignInvoice = `{"name": "Invoice",
 "variables": {"owner": {"type": "text", "default": "USER()"}, "total": {"type": "decimal", "scale": 2}},
 "states": {"initial": "draft", "list": ["draft", "review", "approved", "paid"], "final": ["paid"]},
 "transitions": [{"name": "submit", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "approved"}, {"name": "pay", "from": "approved", "to": "paid"}],
 "access": {"read": "owner = USER() OR INGROUP(\"finance\")", "create": "INGROUP(\"sales\")", "update": "TRUE", "delete": "FALSE"}}`

// TestFlowCampaign measures the flow of a class (ADR-0063,
// Performance) on a database on a real disk: a class with
// FADODB_BENCH_DOCS documents, 100,000 by default, written by a hundred
// people of sales, of which three in four were submitted, half
// approved and a quarter paid. It asks for the flow as finance, who
// reads every document; as one of sales, who reads a hundredth; and as
// finance for the last day, in which a hundredth of the moves fell.
//
// It runs only with FADODB_BENCH_DIR set to a directory on a real disk,
// and prints a Markdown table with -v.
func TestFlowCampaign(t *testing.T) {
	dir := os.Getenv("FADODB_BENCH_DIR")
	if dir == "" {
		t.Skip("set FADODB_BENCH_DIR to a directory on a real disk to run the campaign of the flow")
	}
	docs := 100000
	if n, err := strconv.Atoi(os.Getenv("FADODB_BENCH_DOCS")); err == nil && n > 0 {
		docs = n
	}
	path := filepath.Join(dir, fmt.Sprintf("flow-campaign-%d.fado", os.Getpid()))
	t.Cleanup(func() {
		for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
			os.Remove(path + suffix)
		}
	})
	// The clock of the database: every batch of the work is a minute
	// after the one before, and the last hundredth of it is a day later.
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	o := store.Options{Now: func() time.Time { return now }}
	o.Outbox.Manual = true
	s, err := store.Open(path, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	setup := store.As(store.Principal{ID: "setup", System: []string{store.GroupAdministrators, store.GroupDesigners}})
	if _, err := s.DefineClass(ctx, []byte(flowCampaignInvoice), setup); err != nil {
		t.Fatal(err)
	}
	const people, batch = 100, 1000
	finance := store.As(store.Principal{ID: "ann", Groups: []string{"finance"}})
	began := time.Now()
	lastDay := time.Time{}
	for start := 0; start < docs; start += batch {
		if start >= docs-docs/100 && lastDay.IsZero() {
			now = now.Add(24 * time.Hour)
			lastDay = now
		}
		n := min(batch, docs-start)
		owner := store.As(store.Principal{ID: "sales-" + strconv.Itoa((start/batch)%people), Groups: []string{"sales"}})
		values := make([]store.Values, n)
		for i := range values {
			values[i] = store.Values{"total": "100.00"}
		}
		made, err := s.CreateMany(ctx, "Invoice", values, owner)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, len(made))
		for i, d := range made {
			ids[i] = d.ID
		}
		for step, name := range []string{"submit", "approve", "pay"} {
			now = now.Add(time.Minute)
			ids = ids[:len(ids)*(3-step)/(4-step)]
			if _, err := s.TransitionMany(ctx, "Invoice", name, ids, nil, finance); err != nil {
				t.Fatal(err)
			}
		}
	}
	wrote := time.Since(began)
	if lastDay.IsZero() {
		lastDay = now
	}

	type row struct {
		who    string
		as     store.Option
		period store.FlowPeriod
	}
	rows := []row{
		{"finance, who reads every document, since the class began", finance, store.FlowPeriod{}},
		{"one of sales, who reads a hundredth of them", store.As(store.Principal{ID: "sales-7", Groups: []string{"sales"}}), store.FlowPeriod{}},
		{"finance, for the last day", finance, store.FlowPeriod{Since: lastDay}},
	}
	t.Logf("%d documents and %d moves written in %s", docs, docs*3/4+docs/2+docs/4, wrote.Round(time.Second))
	t.Log("| Who asks | Documents counted | Moves counted | p50 | p99 |")
	t.Log("|---|---|---|---|---|")
	for _, r := range rows {
		const runs = 40
		took := make([]time.Duration, runs)
		var flow *store.Flow
		for i := range took {
			start := time.Now()
			if flow, err = s.Flow(ctx, "Invoice", r.period, r.as); err != nil {
				t.Fatal(err)
			}
			took[i] = time.Since(start)
		}
		sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
		var counted, moves int64
		for _, st := range flow.States {
			counted += st.Documents
		}
		for _, tr := range flow.Transitions {
			moves += tr.Moves
		}
		t.Logf("| %s | %d | %d | %s | %s |", r.who, counted, moves, took[runs/2].Round(10*time.Microsecond), took[runs-1].Round(10*time.Microsecond))
	}
}
