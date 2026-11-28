//go:build duckdb

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// libfadodb keeps a copy of every database it opens in DuckDB beside the
// file, and the questions that read everything are answered from it
// (ADR-0102, item 6). Run with the tags duckdb and duckdb_use_lib and
// DuckDB's library where the system looks (scripts/library.sh try).
func TestTheEngineKeepsACopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "books.fado")
	n, refusal := openDatabase(path, `{"analytics": {"memory": "512MB", "threads": 2}}`, nil)
	if n == 0 {
		t.Fatalf("not opened: %s", refusal)
	}
	define(t, n)
	for _, id := range []string{"FV/1", "FV/2", "FV/3"} {
		done(t, execCommand(n, ann, "create", `{"class": "Invoice", "id": "`+id+`", "values": {"number": "`+id+`"}}`))
	}
	flow := done(t, execCommand(n, bob, "flow", `{"class": "Invoice"}`))["flow"].(map[string]any)
	if flow["class"] != "Invoice" {
		t.Fatalf("flow answered %v", flow)
	}
	if r := closeDatabase(n); r != nil {
		t.Fatalf("close: %s", r)
	}
	if _, err := os.Stat(path + ".duckdb"); err != nil {
		t.Fatalf("no copy beside the database: %v", err)
	}
}

func TestTheEnginesOptions(t *testing.T) {
	for _, c := range []struct{ options, path string }{
		{`{"analytics": {"memory": 2}}`, "/analytics/memory"},
		{`{"analytics": {"threads": "eight"}}`, "/analytics/threads"},
		{`{"analytics": {"cores": 8}}`, "/analytics/cores"},
	} {
		got, refusal := openDatabase(filepath.Join(t.TempDir(), "x.fado"), c.options, nil)
		if got != 0 {
			closeDatabase(got)
			t.Errorf("%s opened a database", c.options)
			continue
		}
		if e := refused(t, refusal); e["path"] != c.path {
			t.Errorf("%s: refused %v, want the place %s", c.options, e, c.path)
		}
	}
}
