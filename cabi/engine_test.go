package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// libfadodb-core has no analytical engine and refuses its options
// (ADR-0102, item 6); engine_duckdb_test.go tries libfadodb's.
func TestTheSmallLibraryHasNoEngine(t *testing.T) {
	if engine != nil {
		t.Skip("this is libfadodb, built with the analytical engine")
	}
	got, refusal := openDatabase(filepath.Join(t.TempDir(), "x.fado"), `{"analytics": {"threads": 2}}`, nil)
	if got != 0 {
		closeDatabase(got)
		t.Fatal("libfadodb-core took the options of an analytical engine")
	}
	e := refused(t, refusal)
	if e["code"] != "bad_value" || e["path"] != "/analytics" || !strings.Contains(e["message"].(string), "libfadodb-core") {
		t.Fatalf("refused %v", e)
	}
}
