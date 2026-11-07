package sqlitefn

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The SQLite runner: every evaluation case is bound, translated to SQL for
// the SQLite dialect and executed by the pure-Go driver, and the value or
// the error code must match the case. With the VM runner this is the first
// VM = SQL differential test (ADR-0003).

const conformanceDir = "../../../conformance"

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestConformance_SQLite(t *testing.T) {
	suites, err := formula.LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	d := formula.NewSQLite()
	total, failed := 0, 0
	for _, s := range suites {
		scope, err := formula.ScopeOf(s.Variables)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		ds := d.WithLimits(s.EvalLimits())
		for i := range s.Cases {
			c := &s.Cases[i]
			total++
			label := fmt.Sprintf("%s[%d] %s", s.Suite, i, c.Formula)
			if c.Name != "" {
				label = fmt.Sprintf("%s[%d] %q: %s", s.Suite, i, c.Name, c.Formula)
			}
			fail := func(format string, args ...any) {
				failed++
				t.Errorf("%s\n    %s", label, fmt.Sprintf(format, args...))
			}
			prog, err := formula.Bind(c.Formula, scope, formula.BindOptions{})
			if err != nil {
				fail("bind: %v", err)
				continue
			}
			res, err := formula.RunSQLCase(db, ds, prog, c)
			if err != nil {
				fail("%v\n    %s", err, res.Query)
				continue
			}
			if c.Error != "" {
				if res.Code != c.Error {
					fail("got error %q (value %s), want %s\n    %s", res.Code, res.Value, c.Error, res.Query)
				}
				continue
			}
			if res.Code != "" {
				fail("got error %s, want %s\n    %s", res.Code, string(c.Expected), res.Query)
				continue
			}
			want, err := formula.ParseCaseValue(c.Expected, prog.Result)
			if err != nil {
				fail("expected: %v", err)
				continue
			}
			if !formula.Equal(res.Value, want) {
				fail("got %s, want %s\n    %s", res.Value, want, res.Query)
			}
		}
	}
	t.Logf("%d evaluation cases executed on SQLite, %d failed", total, failed)
}

func TestRegister_IsIdempotent(t *testing.T) {
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	if err := Register(); err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	var got float64
	if err := db.QueryRow("SELECT fado_round(2.675, 2, 'half_up')").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 2.68 {
		t.Errorf("fado_round(2.675, 2) = %v, want 2.68", got)
	}
	var code any
	if err := db.QueryRow("SELECT fado_value_err('abc')").Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "bad_text" {
		t.Errorf("fado_value_err('abc') = %v, want bad_text", code)
	}
}
