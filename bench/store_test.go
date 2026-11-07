package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The SQLite runs. Every test here runs a small scenario so that the
// harness stays correct in CI; FADODB_BENCH=store,lookup runs the
// campaigns of ADR-0017 and ADR-0020 and prints the tables the records
// quote. See Campaign for the variables that size them.

func TestStore_SQLite(t *testing.T) {
	for _, tc := range []struct {
		target SQLite
		o      Options
	}{
		{SQLite{}, Options{Commits: 300, Documents: 100, Lines: 3, Batch: 1, Seed: 1}},
		{SQLite{}, Options{Commits: 300, Documents: 300, Lines: 2, Batch: 50, Seed: 2}},
		{SQLite{BackgroundCheckpoints: true}, Options{Commits: 300, Documents: 100, Lines: 3, Batch: 1, Seed: 3}},
		{SQLite{Synchronous: "FULL"}, Options{Commits: 100, Documents: 50, Lines: 1, Batch: 1, Seed: 4}},
	} {
		target, o := tc.target, tc.o
		path := filepath.Join(t.TempDir(), "store.db")
		db, err := target.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := RunStore(context.Background(), target, db, path, o)
		if err != nil {
			t.Fatal(err)
		}
		if err := Verify(context.Background(), db, o); err != nil {
			t.Error(err)
		}
		db.Close()
		// The timing fields are informative here, and a coarse clock, as
		// on the Windows runners, may round a short commit to zero.
		if r.Commits != o.Commits || r.PerSecond <= 0 || r.P50 < 0 || r.P99 < r.P50 || r.Max < r.P99 || r.FileBytes <= 0 {
			t.Errorf("report %+v is not plausible", r)
		}
		t.Log(r)
	}
}

func TestLookup_SQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lookup.db")
	target := SQLite{}
	db, err := target.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reports, err := RunLookup(context.Background(), target, db, []int{100, 1000}, 500)
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

// TestStore_SQLiteConcurrentReader holds the database open and writing in
// this process while a second process opens the same file read-only and
// counts the documents: the non-performance criterion of ADR-0017. SQLite
// in WAL mode must allow it.
func TestStore_SQLiteConcurrentReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.db")
	out, err := HoldAndProbe(context.Background(), SQLite{}, path, "TestHelperReadProbe")
	if err != nil {
		t.Fatalf("a second process could not read the live SQLite file: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rows=") {
		t.Fatalf("the reader did not report a count:\n%s", out)
	}
	t.Logf("a second process read the live file while this one wrote:\n%s", strings.TrimSpace(out))
}

// TestHelperReadProbe is the second process of the test above: it opens
// the database named by FADODB_BENCH_PROBE read-only, counts the
// documents and prints rows=N. It skips when it is not that process.
func TestHelperReadProbe(t *testing.T) {
	path := os.Getenv("FADODB_BENCH_PROBE")
	if path == "" {
		t.Skip("not the probe process")
	}
	n, err := Probe(SQLite{}, path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("rows=%d\n", n)
}

// TestCampaign_SQLite runs the campaign on the architecture page's
// configuration and the store scenario alone on its variants: checkpoints
// off the commit path, and every commit fsynced. The lookup does not
// depend on either.
func TestCampaign_SQLite(t *testing.T) {
	which := os.Getenv("FADODB_BENCH")
	if which == "" {
		t.Skip("set FADODB_BENCH=store,lookup to run the campaign")
	}
	dir := CampaignDir()
	storeOnly := ""
	if strings.Contains(which, "store") {
		storeOnly = "store"
	}
	for _, tc := range []struct {
		target Target
		which  string
	}{
		{SQLite{}, which},
		{SQLite{BackgroundCheckpoints: true}, storeOnly},
		{SQLite{Synchronous: "FULL"}, storeOnly},
		{SQLite{Synchronous: "FULL", BackgroundCheckpoints: true}, storeOnly},
	} {
		if _, err := Campaign(context.Background(), tc.target, dir, tc.which, os.Stdout); err != nil {
			t.Fatal(err)
		}
	}
}
