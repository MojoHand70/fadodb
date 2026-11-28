package postgres

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/formula/fuzz"
)

// The property test on Postgres: generated formulas must evaluate the
// same on the VM and on Postgres, and no statement may fail.
// FADODB_FUZZ_N sizes the run, FADODB_FUZZ_SEED reproduces one, and
// FADODB_FUZZ_WORKERS says how many connections run it. Each worker
// has a connection of its own to the schema of the test.
func TestProperty_VMEqualsPostgres(t *testing.T) {
	cfg, err := fuzz.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	addr, schema := schemaOf(t)
	open := func() (*sql.DB, error) {
		db, err := Open(addr, schema)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		return db, nil
	}
	stats, err := fuzz.Campaign(cfg, formula.NewPostgres(), open)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(stats.Summary("Postgres"))
	for _, r := range stats.Reports {
		t.Error(r)
	}
	if len(stats.Brief) > 0 {
		t.Errorf("divergences:\n%s", strings.Join(stats.Brief, "\n"))
	}
}
