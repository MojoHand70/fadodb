package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// TestLookup_FormulaSuiteOnTheStore runs the lookup suite of the formula
// language, which the Go VM, the SQL generator and DuckDB answer from
// rows in memory or in their own tables, against the store's executor:
// the tables are defined and filled through the store, and every case is
// bound against them and answered by the index or by a prepared
// statement. It runs twice, with the index and with the index turned
// off, so that both paths give the suite's answers (ADR-0037, ADR-0046).
func TestLookup_FormulaSuiteOnTheStore(t *testing.T) {
	file := filepath.Join("..", "..", "conformance", "formula", "lookup.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Sources map[string]struct {
			Key     json.RawMessage `json:"key"`
			Columns json.RawMessage `json:"columns"`
			Rows    []Values        `json:"rows"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	suites, err := formula.LoadSuites(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	var suite *formula.Suite
	for _, s := range suites {
		if s.Suite == "lookup" {
			suite = s
		}
	}
	if suite == nil || len(raw.Sources) == 0 {
		t.Fatal("no lookup suite with sources")
	}
	scope, err := suite.Scope()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, mode := range []struct {
		name string
		opts Options
	}{{"index", Options{}}, {"statements", Options{MaxIndexedRows: 1}}} {
		t.Run(mode.name, func(t *testing.T) {
			s, _ := openStore(t, mode.opts)
			for name, src := range raw.Sources {
				def := fmt.Sprintf(`{"name": %q, "columns": %s, "key": %s}`, name, src.Columns, src.Key)
				if _, err := s.DefineTable(ctx, []byte(def)); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if _, err := s.ImportRows(ctx, name, src.Rows); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
			vm := formula.NewVM()
			vm.Executor = (*executor)(s)
			aggregates, narrowed := 0, 0
			for i := range suite.Cases {
				c := &suite.Cases[i]
				label := fmt.Sprintf("lookup[%d] %q: %s", i, c.Name, c.Formula)
				prog, err := formula.Bind(c.Formula, scope, formula.BindOptions{Sources: s.sources()})
				if err != nil {
					t.Errorf("%s\n    bind: %v", label, err)
					continue
				}
				for _, q := range prog.Queries {
					if q.Agg != "" {
						aggregates++
					}
					if q.KeyPart {
						narrowed++
					}
				}
				if c.Type != "" && prog.Result.String() != c.Type {
					t.Errorf("%s\n    static type %s, want %s", label, prog.Result, c.Type)
					continue
				}
				vars := make([]formula.Value, len(prog.Slots))
				for si, slot := range prog.Slots {
					if in, ok := c.Inputs[slot.Name]; ok {
						if vars[si], err = formula.ParseCaseValue(in, slot.Type); err != nil {
							t.Fatalf("%s\n    input %s: %v", label, slot.Name, err)
						}
					}
				}
				cctx, err := c.Context.Context()
				if err != nil {
					t.Fatalf("%s\n    %v", label, err)
				}
				got, err := vm.Run(prog, vars, cctx)
				if c.Error != "" {
					if ee, ok := err.(*formula.EvalError); !ok || ee.Code != c.Error {
						t.Errorf("%s\n    got %v %v, want error %s", label, got, err, c.Error)
					}
					continue
				}
				if err != nil {
					t.Errorf("%s\n    got error %v, want %s", label, err, c.Expected)
					continue
				}
				want, err := formula.ParseCaseValue(c.Expected, prog.Result)
				if err != nil {
					t.Fatalf("%s\n    expected: %v", label, err)
				}
				if !formula.Equal(got, want) {
					t.Errorf("%s\n    got %s, want %s", label, got, want)
				}
			}
			if aggregates < 30 || narrowed < 5 {
				t.Errorf("the suite ran %d aggregates and %d lookups narrowed by a term of a conjunction; it should have plenty of both", aggregates, narrowed)
			}
		})
	}
}
