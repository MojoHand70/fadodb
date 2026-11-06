package sqlitefn

import (
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/formula/fuzz"
)

// The property test: generated formulas must evaluate the same on the VM
// and on SQLite. FADODB_FUZZ_N sizes the run (2000 by default, one
// million for the Phase 1 acceptance), FADODB_FUZZ_SEED reproduces one.

func openForFuzz() (*sql.DB, error) {
	if err := Register(); err != nil {
		return nil, err
	}
	return sql.Open(DriverName, ":memory:")
}

func TestProperty_VMEqualsSQLite(t *testing.T) {
	cfg, err := fuzz.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	stats, err := fuzz.Campaign(cfg, formula.NewSQLite(), openForFuzz)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(stats.Summary("SQLite"))
	for _, r := range stats.Reports {
		t.Error(r)
	}
	if len(stats.Brief) > 0 {
		t.Errorf("divergences:\n%s", strings.Join(stats.Brief, "\n"))
	}
}

var (
	fuzzOnce sync.Once
	fuzzDB   *sql.DB
	fuzzErr  error
)

// FuzzVMEqualsSQLite is the same property under the Go fuzzing engine:
// go test -fuzz=FuzzVMEqualsSQLite ./internal/formula/sqlitefn explores
// seeds and keeps any that diverge under testdata/fuzz.
func FuzzVMEqualsSQLite(f *testing.F) {
	f.Add(uint64(1))
	f.Add(uint64(fuzz.DefaultSeed))
	d := formula.NewSQLite()
	f.Fuzz(func(t *testing.T, seed uint64) {
		fuzzOnce.Do(func() { fuzzDB, fuzzErr = openForFuzz() })
		if fuzzErr != nil {
			t.Fatal(fuzzErr)
		}
		vm := formula.NewVM()
		g := fuzz.New(seed)
		for i := 0; i < 4; i++ {
			fm := g.Formula()
			if o := fuzz.Run(fm, vm, fuzzDB, d); !o.Agree() {
				t.Fatal(o.Report(fm, d))
			}
		}
	})
}
