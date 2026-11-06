package analytics

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The DuckDB runner: every evaluation case is bound, translated for the
// DuckDB dialect and executed by DuckDB, and the value or the error code
// must match the case. Aggregates may differ from the VM by the relative
// tolerance of ADR-0019, because DuckDB sums left to right without
// compensation.

const conformanceDir = "../conformance"

func openDuckDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range formula.NewDuckDB().Setup() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v\n%s", err, stmt)
		}
	}
	return db
}

func TestConformance_DuckDB(t *testing.T) {
	suites, err := formula.LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	db := openDuckDB(t)
	d := formula.NewDuckDB()
	total, failed := 0, 0
	for _, s := range suites {
		scope, err := formula.ScopeOf(s.Variables)
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
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
			res, err := formula.RunSQLCase(db, d, prog, c)
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
			ok := formula.Equal(res.Value, want)
			if !ok && (prog.Calls("SUM") || prog.Calls("AVERAGE")) {
				ok = formula.NearlyEqual(res.Value, want, 1e-12)
			}
			if !ok {
				fail("got %s, want %s\n    %s", res.Value, want, res.Query)
			}
		}
	}
	t.Logf("%d evaluation cases executed on DuckDB, %d failed", total, failed)
}

func TestDuckDB_MacrosAreInlineExpressions(t *testing.T) {
	db := openDuckDB(t)
	rows, err := db.Query("SELECT fado_round(x, 2) FROM (VALUES (2.675::DOUBLE), (1.005::DOUBLE), (NULL::DOUBLE)) AS t(x)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if len(got) != 3 || got[0] != 2.68 || got[1] != 1.0 || got[2] != nil {
		t.Errorf("fado_round over three rows = %v, want [2.68 1 <nil>]", got)
	}
}
