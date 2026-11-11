package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestLookup_FormulaSuiteOnClasses runs the same suite against documents
// (ADR-0046): every source becomes a class whose variables are the
// source's columns and whose documents are its rows, with ids that sort
// as the source's key does, so that the first row is the same row. The
// store answers every case with a statement over the class's document
// table, and the answers are the suite's.
func TestLookup_FormulaSuiteOnClasses(t *testing.T) {
	file := filepath.Join("..", "..", "conformance", "formula", "lookup.json")
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
	if suite == nil {
		t.Fatal("no lookup suite")
	}
	rows, err := suite.SourceRows()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := suite.Scope()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	sources := formula.NewSourceSet()
	for src, data := range rows {
		// A column named id is the document's id; the others are variables.
		idCol := -1
		vars := ""
		for i, f := range src.Columns {
			if strings.EqualFold(f.Name, "id") {
				idCol = i
				continue
			}
			if vars != "" {
				vars += ", "
			}
			vars += fmt.Sprintf(`%q: {"type": %q, "index": %v}`, f.Name, f.Type.String(), strings.EqualFold(f.Name, src.Key[0]) && f.Type.Kind != formula.KindMoney)
		}
		def := fmt.Sprintf(`{"name": %q, "variables": {%s}, "states": {"initial": "open", "list": ["open"]}}`, src.Name, vars)
		if _, err := s.DefineClass(ctx, []byte(def)); err != nil {
			t.Fatalf("%s: %v", src.Name, err)
		}
		// SourceRows gives the rows as declared; the executor in memory
		// sorts them by the key, and so does this.
		mem := formula.NewMemoryExecutor()
		mem.AddTable(src, data)
		for n, row := range data {
			id := fmt.Sprintf("row-%04d", n)
			vals := Values{}
			for i, f := range src.Columns {
				if i == idCol {
					id = row[i].Str()
					continue
				}
				if !row[i].IsBlank() {
					vals[f.Name] = valueOut(row[i])
				}
			}
			if _, err := s.Create(ctx, src.Name, vals, WithID(id)); err != nil {
				t.Fatalf("%s row %d: %v", src.Name, n, err)
			}
		}
		c, _ := s.class(src.Name)
		sources.Add(c.cls.Source())
	}
	vm := formula.NewVM()
	vm.Executor = (*executor)(s)
	for i := range suite.Cases {
		c := &suite.Cases[i]
		label := fmt.Sprintf("lookup[%d] %q: %s", i, c.Name, c.Formula)
		prog, err := formula.Bind(c.Formula, scope, formula.BindOptions{Sources: sources})
		if err != nil {
			t.Errorf("%s\n    bind: %v", label, err)
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
}
