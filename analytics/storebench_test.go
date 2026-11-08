package analytics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/bench"
)

// The DuckDB runs of the Phase 2 benchmarks, mirroring bench/store_test.go:
// a small scenario always, the campaign under FADODB_BENCH.

func TestStore_DuckDB(t *testing.T) {
	for _, o := range []bench.Options{
		{Commits: 300, Documents: 100, Lines: 3, Batch: 1, Seed: 1},
		{Commits: 300, Documents: 300, Lines: 2, Batch: 50, Seed: 2},
	} {
		path := filepath.Join(t.TempDir(), "store.duckdb")
		target := DuckDBTarget{}
		db, err := target.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := bench.RunStore(context.Background(), target, db, path, o)
		if err != nil {
			t.Fatal(err)
		}
		if err := bench.Verify(context.Background(), db, o); err != nil {
			t.Error(err)
		}
		db.Close()
		if r.Commits != o.Commits || r.PerSecond <= 0 || r.P50 < 0 || r.P99 < r.P50 || r.Max < r.P99 || r.FileBytes <= 0 {
			t.Errorf("report %+v is not plausible", r)
		}
		t.Log(r)
	}
}

func TestLookup_DuckDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lookup.duckdb")
	target := DuckDBTarget{}
	db, err := target.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reports, err := bench.RunLookup(context.Background(), target, db, []int{100, 1000}, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		if r.Map < 0 || r.Stmt <= 0 || r.Stmt < r.Map {
			t.Errorf("report %+v is not plausible", r)
		}
		t.Log(r)
	}
}

// TestStore_DuckDBConcurrentReader asks whether a second process can read
// a DuckDB file while this one holds it for writing. The outcome is
// recorded, not asserted: it is DuckDB's process model, which the ADR-0017
// follow-up weighs.
func TestStore_DuckDBConcurrentReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.duckdb")
	out, err := bench.HoldAndProbe(context.Background(), DuckDBTarget{}, path, "TestHelperReadProbe")
	switch {
	case err != nil:
		t.Logf("a second process could not read the live DuckDB file while this one wrote:\n%s", strings.TrimSpace(out))
	case strings.Contains(out, "rows="):
		t.Logf("a second process read the live DuckDB file while this one wrote:\n%s", strings.TrimSpace(out))
	default:
		t.Fatalf("the reader neither failed nor reported a count:\n%s", out)
	}
}

func TestHelperReadProbe(t *testing.T) {
	path := os.Getenv("FADODB_BENCH_PROBE")
	if path == "" {
		t.Skip("not the probe process")
	}
	n, err := bench.Probe(DuckDBTarget{}, path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("rows=%d\n", n)
}

// TestCampaign_DuckDB runs the campaign with the client's defaults and
// the store scenario alone on one thread, in case the pool's coordination
// is what a single writer pays for.
func TestCampaign_DuckDB(t *testing.T) {
	which := os.Getenv("FADODB_BENCH")
	if which == "" {
		t.Skip("set FADODB_BENCH=store,lookup to run the campaign")
	}
	dir := bench.CampaignDir()
	storeOnly := ""
	if strings.Contains(which, "store") {
		storeOnly = "store"
	}
	for _, tc := range []struct {
		target bench.Target
		which  string
	}{{DuckDBTarget{}, which}, {DuckDBTarget{Threads: 1}, storeOnly}} {
		if _, err := bench.Campaign(context.Background(), tc.target, dir, tc.which, os.Stdout); err != nil {
			t.Fatal(err)
		}
	}
}
