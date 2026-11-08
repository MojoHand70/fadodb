package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

const rates = `{"name": "Rates", "label": "Exchange rates", "columns": {"code": "text", "rate": "decimal(6)", "fee": "money(2)", "since": "date"}, "key": ["code"], "indexes": [["since"]]}`

func TestDefineTable(t *testing.T) {
	tbl, diags := DefineTable([]byte(rates))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if tbl.Name != "Rates" || tbl.Version != 1 || tbl.Binding != "sqlite" || len(tbl.Fields) != 4 || len(tbl.Columns) != 5 || tbl.Columns[3].Name != "fee_currency" {
		t.Errorf("table %+v", tbl)
	}
	if strings.Join(tbl.Key, ",") != "code" || len(tbl.Indexes) != 1 || tbl.Indexes[0][0] != "since" {
		t.Errorf("key %v indexes %v", tbl.Key, tbl.Indexes)
	}
	src := tbl.Source()
	if src.Name != "Rates" || len(src.Columns) != 4 || src.Key[0] != "code" || src.Binding != "sqlite" {
		t.Errorf("source %+v", src)
	}
	if f, ok := tbl.Field("RATE"); !ok || f.Type.String() != "decimal(6)" {
		t.Errorf("field lookup %v %v", f, ok)
	}
}

func TestDefineTable_Diagnostics(t *testing.T) {
	cases := []struct{ name, src, code, path string }{
		{"no columns", `{"name": "T", "columns": {}, "key": []}`, CodeMissing, "/columns"},
		{"no key", `{"name": "T", "columns": {"a": "text"}}`, CodeMissing, "/key"},
		{"unknown key column", `{"name": "T", "columns": {"a": "text"}, "key": ["b"]}`, CodeUnknownVariable, "/key/0"},
		{"key twice", `{"name": "T", "columns": {"a": "text"}, "key": ["a", "A"]}`, CodeDuplicate, "/key/1"},
		{"json key", `{"name": "T", "columns": {"a": "json"}, "key": ["a"]}`, CodeNotAllowed, "/key/0"},
		{"unknown type", `{"name": "T", "columns": {"a": "integer"}, "key": ["a"]}`, CodeUnknownType, "/columns/a"},
		{"object column", `{"name": "T", "columns": {"a": "object"}, "key": ["a"]}`, CodeNotSupported, "/columns/a"},
		{"composite column", `{"name": "T", "columns": {"a": "array<text>"}, "key": ["a"]}`, CodeNotAllowed, "/columns/a"},
		{"column twice", `{"name": "T", "columns": {"a": "text", "A": "text"}, "key": ["a"]}`, CodeDuplicate, "/columns/A"},
		{"bad column name", `{"name": "T", "columns": {"a__b": "text"}, "key": ["a__b"]}`, CodeBadName, "/columns/a__b"},
		{"reserved name", `{"name": "events", "columns": {"a": "text"}, "key": ["a"]}`, CodeReservedName, "/name"},
		{"generated prefix", `{"name": "tbl_x", "columns": {"a": "text"}, "key": ["a"]}`, CodeReservedName, "/name"},
		{"unknown binding", `{"name": "T", "columns": {"a": "text"}, "key": ["a"], "binding": "postgres"}`, CodeBadValue, "/binding"},
		{"duckdb binding", `{"name": "T", "columns": {"a": "text"}, "key": ["a"], "binding": "duckdb"}`, CodeNotSupported, "/binding"},
		{"index column", `{"name": "T", "columns": {"a": "text"}, "key": ["a"], "indexes": [["z"]]}`, CodeUnknownVariable, "/indexes/0/0"},
		{"unknown property", `{"name": "T", "columns": {"a": "text"}, "key": ["a"], "rows": []}`, CodeBadJSON, ""},
	}
	for _, tc := range cases {
		_, diags := DefineTable([]byte(tc.src))
		found := false
		for _, d := range diags {
			if d.Code == tc.code && d.Path == tc.path {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s at %q, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
}

func TestClass_Lookup(t *testing.T) {
	tbl, diags := DefineTable([]byte(rates))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Sources: formula.NewSourceSet(tbl.Source())}
	cls, diags := DefineIn([]byte(`{"name": "Invoice", "variables": {
		"currency": {"type": "text"}, "net": {"type": "decimal(2)"},
		"rate": {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"},
		"net_pln": {"formula": "ROUND(net * rate, 2)"},
		"fee": {"type": "money(2)", "formula": "LOOKUP(Rates, fee, code = THIS.currency)"},
		"owner": {"type": "text", "default": "LOOKUP(Rates, code, since > TODAY())"}}}`), env)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	rate, _ := cls.Variable("rate")
	if rate.Type.String() != "decimal(6)" || strings.Join(rate.Sources, ",") != "Rates" || strings.Join(rate.Deps, ",") != "currency" {
		t.Errorf("rate: type %s sources %v deps %v", rate.Type, rate.Sources, rate.Deps)
	}
	if q := rate.Formula.Queries[0]; q.IndexColumn != "code" || q.Key == nil {
		t.Errorf("rate takes the statement path: %+v", q)
	}
	owner, _ := cls.Variable("owner")
	if q := owner.Default.Queries[0]; q.Key != nil || len(owner.Sources) != 1 {
		t.Errorf("owner's default: %+v %v", q, owner.Sources)
	}
	if strings.Join(cls.Order, " ") != "rate fee net_pln" && strings.Join(cls.Order, " ") != "rate net_pln fee" {
		t.Errorf("order %v", cls.Order)
	}
	// Without the table in the environment, the lookup is an unknown source.
	_, diags = Define([]byte(`{"name": "Invoice", "variables": {"currency": {"type": "text"}, "rate": {"formula": "LOOKUP(Rates, rate, code = THIS.currency)"}}}`), nil)
	if !diags.Has(CodeBadFormula) || !strings.Contains(diags.Error(), "unknown source") {
		t.Errorf("without sources: %v", diags)
	}
	// A title may not read a table; a read rule may not either.
	_, diags = DefineIn([]byte(`{"name": "Invoice", "title": "LOOKUP(Rates, code, TRUE)", "variables": {"currency": {"type": "text"}},
		"access": {"read": "LOOKUP(Rates, code, TRUE) = currency"}}`), env)
	if !diags.Has(CodeBadFormula) || !diags.Has(CodeNotAllowed) {
		t.Errorf("title and read rule: %v", diags)
	}
}
