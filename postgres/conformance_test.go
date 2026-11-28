package postgres

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The Postgres runner: every evaluation case is bound, translated for
// the Postgres dialect and executed by Postgres, and the value or the
// error code must match the case, exactly, as on every evaluator.

const conformanceDir = "../conformance"

func TestConformance_Postgres(t *testing.T) {
	suites, err := formula.LoadSuites(filepath.Join(conformanceDir, "formula"))
	if err != nil {
		t.Fatal(err)
	}
	db := openPostgres(t)
	d := formula.NewPostgres()
	total, failed := 0, 0
	for _, s := range suites {
		scope, err := s.Scope()
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		sources, err := s.SourceSet()
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		rows, err := s.SourceRows()
		if err != nil {
			t.Fatalf("%s: %v", s.File, err)
		}
		ds := d.WithLimits(s.EvalLimits())
		if err := formula.SetupSources(db, ds, rows); err != nil {
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
			prog, err := formula.Bind(c.Formula, scope, s.BindOptions(sources))
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
	t.Logf("%d evaluation cases executed on Postgres, %d failed", total, failed)
}
