package analytics

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/formula/fuzz"
)

// The property test on DuckDB: generated formulas must evaluate the same
// on the VM and on DuckDB. FADODB_FUZZ_N sizes the run, FADODB_FUZZ_SEED
// reproduces one. Each worker gets its own single-threaded in-memory
// database so a campaign does not start a thread pool per worker.

func openForFuzz() (*sql.DB, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range append([]string{"SET threads = 1"}, formula.NewDuckDB().Setup()...) {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func TestProperty_VMEqualsDuckDB(t *testing.T) {
	cfg, err := fuzz.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	stats, err := fuzz.Campaign(cfg, formula.NewDuckDB(), openForFuzz)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(stats.Summary("DuckDB"))
	for _, r := range stats.Reports {
		t.Error(r)
	}
	if len(stats.Brief) > 0 {
		t.Errorf("divergences:\n%s", strings.Join(stats.Brief, "\n"))
	}
}
