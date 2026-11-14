package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerated keeps the three generated chapters of the manual equal
// to what the engine has. It fails when a function, a code or a command
// was added,
// changed or removed and the chapters were not written again; run
//
//	make docs
//
// which is this test with FADODB_UPDATE_DOCS set, and commit the result.
func TestGenerated(t *testing.T) {
	refusals, err := Refusals("..")
	if err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join("..", "..", "docs", "manual")
	for name, want := range map[string]string{"functions.md": Functions(), "refusals.md": refusals, "commands.md": Commands()} {
		path := filepath.Join(manual, name)
		if os.Getenv("FADODB_UPDATE_DOCS") != "" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s is missing: run `make docs`: %v", name, err)
		}
		// A checkout on Windows may carry CRLF line endings.
		if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("docs/manual/%s is not what the engine has now: run `make docs` and commit the result", name)
		}
	}
}

// Every code has a meaning of its own words, and no list is empty.
func TestCodesHaveMeanings(t *testing.T) {
	for _, src := range sources {
		codes, err := codesOf(filepath.Join("..", filepath.FromSlash(src.file)), src.prefix)
		if err != nil {
			t.Fatal(err)
		}
		if len(codes) < 4 {
			t.Errorf("%s: %d codes found", src.file, len(codes))
		}
		for _, c := range codes {
			if len(c.meaning) < 10 || strings.Contains(c.meaning, c.value+":") {
				t.Errorf("%s: the meaning of %s is %q", src.file, c.value, c.meaning)
			}
		}
	}
}
