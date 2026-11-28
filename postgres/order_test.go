package postgres

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Text is ordered by its code points, as the VM orders it, whatever
// rule the Postgres database was created with: a capital comes before
// every small letter, and a letter with a mark after them all. The
// cases below give another answer under a rule of a language, as
// en_US, where "a" comes before "B"; the suite of the conformance
// folder has no such pair. They run on the VM too, so that what is
// expected here is the VM's order and not this file's opinion.
const orderSuite = `{
  "suite": "order",
  "description": "Text is ordered by code point on every database.",
  "variables": {"Alfa": "text", "Beta": "text"},
  "sources": {"Names": {"columns": {"name": "text", "rank": "number"}, "key": ["name"],
    "rows": [{"name": "b", "rank": 1}, {"name": "B", "rank": 2}, {"name": "a", "rank": 3}, {"name": "Z", "rank": 4}, {"name": "é", "rank": 5}]}},
  "cases": [
    {"formula": "\"a\" < \"B\"", "expected": false},
    {"formula": "\"Z\" < \"a\"", "expected": true},
    {"formula": "Alfa < Beta", "inputs": {"Alfa": "a", "Beta": "B"}, "expected": false},
    {"formula": "Alfa >= Beta", "inputs": {"Alfa": "é", "Beta": "z"}, "expected": true},
    {"formula": "Alfa <= UPPER(Alfa)", "inputs": {"Alfa": "abc"}, "expected": false},
    {"formula": "TEXT(10) < Alfa", "inputs": {"Alfa": "-1"}, "expected": false},
    {"formula": "LOOKUP(Names, name, rank > 0)", "expected": "B"},
    {"formula": "LOOKUP(Names, rank, name > \"Z\")", "expected": 3},
    {"formula": "LOOKUP(Names, COUNT(name), name < \"a\")", "expected": 2},
    {"formula": "LOOKUP(Names, COUNT(name), name < THIS.Alfa)", "inputs": {"Alfa": "b"}, "expected": 3},
    {"formula": "UPPER(Alfa)", "inputs": {"Alfa": "zażółć gęślą jaźń ß"}, "expected": "ZAŻÓŁĆ GĘŚLĄ JAŹŃ ß"},
    {"formula": "LOWER(Alfa)", "inputs": {"Alfa": "ZAŻÓŁĆ GĘŚLĄ JAŹŃ İ"}, "expected": "zażółć gęślą jaźń i"},
    {"formula": "PROPER(Alfa)", "inputs": {"Alfa": "łÓDŹ i żYRARDÓW"}, "expected": "Łódź I Żyrardów"}
  ]
}`

func TestText_OrderedByCodePointWhateverTheDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "order.json"), []byte(orderSuite), 0o600); err != nil {
		t.Fatal(err)
	}
	suites, err := formula.LoadSuites(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := suites[0]
	scope, err := s.Scope()
	if err != nil {
		t.Fatal(err)
	}
	sources, err := s.SourceSet()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.SourceRows()
	if err != nil {
		t.Fatal(err)
	}
	exec, err := s.Executor()
	if err != nil {
		t.Fatal(err)
	}
	vm := formula.NewVM()
	vm.Executor, vm.Lists = exec, exec
	db := openPostgres(t)
	d := formula.NewPostgres()
	var rule string
	if err := db.QueryRow("SELECT CASE datlocprovider WHEN 'c' THEN datcollate ELSE datlocale END FROM pg_database WHERE datname = current_database()").Scan(&rule); err != nil {
		t.Fatal(err)
	}
	t.Logf("the database's own rule for text: %s", rule)
	if err := formula.SetupSources(db, d, rows); err != nil {
		t.Fatal(err)
	}
	for i := range s.Cases {
		c := &s.Cases[i]
		prog, err := formula.Bind(c.Formula, scope, s.BindOptions(sources))
		if err != nil {
			t.Errorf("%s: bind: %v", c.Formula, err)
			continue
		}
		want, err := formula.ParseCaseValue(c.Expected, prog.Result)
		if err != nil {
			t.Fatal(err)
		}
		vars := make([]formula.Value, len(prog.Slots))
		for si, slot := range prog.Slots {
			if raw, ok := c.Inputs[slot.Name]; ok {
				if vars[si], err = formula.ParseCaseValue(raw, slot.Type); err != nil {
					t.Fatal(err)
				}
			}
		}
		got, err := vm.Run(prog, vars, &formula.Context{})
		if err != nil || !formula.Equal(got, want) {
			t.Errorf("%s: the VM gives %s, %v; the case says %s", c.Formula, got, err, want)
			continue
		}
		res, err := formula.RunSQLCase(db, d, prog, c)
		if err != nil {
			t.Errorf("%s: %v\n    %s", c.Formula, err, res.Query)
			continue
		}
		if res.Code != "" || !formula.Equal(res.Value, want) {
			t.Errorf("%s: Postgres gives %s (error %q), want %s\n    %s", c.Formula, res.Value, res.Code, want, res.Query)
		}
	}
}
