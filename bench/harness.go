package bench

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The pieces the module tests of both engines share: the read probe, the
// writer that holds a file while the probe runs, and the campaign.

// Probe opens the database at path read-only, as a second process does,
// and counts the documents.
func Probe(target Target, path string) (int64, error) {
	db, err := target.OpenReadOnly(path)
	if err != nil {
		return 0, fmt.Errorf("open: %w", err)
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow("SELECT count(*) FROM doc_Invoice").Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// HoldAndProbe writes a small scenario into path, keeps writing on the
// single connection while the probe command runs, and returns the probe's
// combined output and its error. The probe command is the test binary
// re-run on its probe test with FADODB_BENCH_PROBE naming the file, so
// that the reader is another process, which is the question of ADR-0017.
func HoldAndProbe(ctx context.Context, target Target, path string, probeTest string) (string, error) {
	db, err := target.Open(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	o := Options{Commits: 200, Documents: 50, Lines: 2, Batch: 1, Seed: 3}
	if _, err := RunStore(ctx, target, db, path, o); err != nil {
		return "", err
	}
	var stop atomic.Bool
	done := make(chan error, 1)
	go func() { done <- keepWriting(ctx, db, &stop) }()
	cmd := exec.Command(os.Args[0], "-test.run=^"+probeTest+"$", "-test.v")
	cmd.Env = append(os.Environ(), "FADODB_BENCH_PROBE="+path)
	out, probeErr := cmd.CombinedOutput()
	stop.Store(true)
	if err := <-done; err != nil {
		return string(out), fmt.Errorf("the writer failed while the probe ran: %w", err)
	}
	return string(out), probeErr
}

// keepWriting appends event rows in small transactions until stop is set.
func keepWriting(ctx context.Context, db *sql.DB, stop *atomic.Bool) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for i := 0; !stop.Load(); i++ {
		if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
			return err
		}
		id := fmt.Sprintf("probe_%06d", i)
		if _, err := conn.ExecContext(ctx, "INSERT INTO events (event_id, class, doc_id, kind, command_key, business_date, ts) VALUES (?, 'Invoice', 'inv_00000000', 'set', ?, 20700, 0)", id, id); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

// Campaign runs the store scenario, the lookup scenario or both for a
// target, as which says ("store", "lookup" or "store,lookup"), writing
// its databases under dir and the Markdown tables to w. It returns false
// without running when which is empty, or when FADODB_BENCH_TARGET is set
// and the target's name does not contain it. The tests pass FADODB_BENCH
// as which; FADODB_BENCH_COMMITS sizes the store campaign (default
// 100,000) and FADODB_BENCH_ITERS the lookups per table size (default
// 50,000).
//
// dir must be on the disk the numbers are meant for. The system's
// temporary directory is the wrong place: on many Linux machines /tmp is
// a tmpfs in memory, where an fsync costs nothing and every row of the
// store table is wrong by an order of magnitude.
func Campaign(ctx context.Context, target Target, dir, which string, w io.Writer) (bool, error) {
	if which == "" {
		return false, nil
	}
	if only := os.Getenv("FADODB_BENCH_TARGET"); only != "" && !strings.Contains(target.Name(), only) {
		return false, nil
	}
	fmt.Fprintf(w, "%s campaign in %s\n", target.Name(), dir)
	if strings.Contains(which, "store") {
		commits := envInt("FADODB_BENCH_COMMITS", 100_000)
		fmt.Fprintln(w, Header)
		for i, o := range []Options{
			{Commits: commits, Documents: commits / 5, Lines: 5, Batch: 1, Seed: 11},
			{Commits: commits, Documents: commits / 5, Lines: 5, Batch: 1000, Seed: 12},
			{Commits: commits, Documents: commits, Lines: 5, Batch: 1000, Seed: 13},
		} {
			path := filepath.Join(dir, fmt.Sprintf("store-%d.db", i))
			if err := storeRun(ctx, target, path, o, w); err != nil {
				return true, err
			}
			removeDatabase(path)
		}
	}
	if strings.Contains(which, "lookup") {
		iters := envInt("FADODB_BENCH_ITERS", 50_000)
		path := filepath.Join(dir, "lookup.db")
		db, err := target.Open(path)
		if err != nil {
			return true, err
		}
		fmt.Fprintln(w, LookupHeader)
		reports, err := RunLookup(ctx, target, db, []int{100, 1_000, 10_000, 100_000, 1_000_000}, iters)
		for _, r := range reports {
			fmt.Fprintln(w, r)
		}
		db.Close()
		removeDatabase(path)
		if err != nil {
			return true, err
		}
	}
	return true, nil
}

// removeDatabase removes a database file and the logs and shared memory
// either engine keeps beside it.
func removeDatabase(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", ".wal"} {
		os.Remove(path + suffix)
	}
}

// CampaignDir is the directory the campaign writes to: FADODB_BENCH_DIR,
// or the working directory, which under go test is the package's, on the
// disk the checkout is on. Never the temporary directory; see Campaign.
func CampaignDir() string {
	if dir := os.Getenv("FADODB_BENCH_DIR"); dir != "" {
		return dir
	}
	return "."
}

// storeRun runs one store scenario, verifies it, prints its row and the
// time to reopen the database and read one row.
func storeRun(ctx context.Context, target Target, path string, o Options, w io.Writer) error {
	db, err := target.Open(path)
	if err != nil {
		return err
	}
	r, err := RunStore(ctx, target, db, path, o)
	if err != nil {
		db.Close()
		return err
	}
	if err := Verify(ctx, db, o); err != nil {
		db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	fmt.Fprintln(w, r)
	reopen := time.Now()
	db, err = target.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var version int64
	if err := db.QueryRowContext(ctx, "SELECT version FROM doc_Invoice WHERE id = 'inv_00000000'").Scan(&version); err != nil {
		return err
	}
	fmt.Fprintf(w, "    %s: reopen and read one row in %s\n", target.Name(), ms(time.Since(reopen)))
	return nil
}

func envInt(name string, def int) int {
	if s := os.Getenv(name); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return def
}
