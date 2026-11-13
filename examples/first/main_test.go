package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The chapter the program belongs to.
const chapter = "../../docs/manual/first-application.md"

// unix reads a file with the line endings of every platform made one.
func unix(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// The chapter shows the class and the program's output. Both are checked
// against the real ones, so the chapter cannot drift from the engine.
func TestTheChapterShowsWhatTheProgramDoes(t *testing.T) {
	page := unix(t, chapter)
	var out bytes.Buffer
	if err := run(&out, "invoice.json"); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)<!-- output of examples/first -->\n```text\n(.*?)\n```").FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the chapter has no block for the program's output")
	}
	if got, want := strings.TrimRight(out.String(), "\n"), m[1]; got != want {
		t.Errorf("the program prints\n%s\n\nand the chapter shows\n%s", got, want)
	}
	c := regexp.MustCompile("(?s)```json\n(.*?)\n```").FindStringSubmatch(page)
	if c == nil {
		t.Fatal("the chapter has no class")
	}
	if got := strings.TrimRight(unix(t, "invoice.json"), "\n"); got != c[1] {
		t.Errorf("invoice.json is not the class the chapter shows")
	}
}

// changed runs the program on the class with one piece of its text
// replaced, as the chapter's "Try a change" asks the reader to.
func changed(t *testing.T, old, new string) (string, error) {
	t.Helper()
	class := unix(t, "invoice.json")
	if !strings.Contains(class, old) {
		t.Fatalf("the class has no %s", old)
	}
	file := filepath.Join(t.TempDir(), "invoice.json")
	if err := os.WriteFile(file, []byte(strings.Replace(class, old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run(&out, file)
	return out.String(), err
}

// The three changes the chapter invites do what it says they do.
func TestTheChangesTheChapterInvites(t *testing.T) {
	// A named variable wins over * in its state.
	out, err := changed(t, `"INAPPROVAL":   {"*": {"write": "USER() = \"krzysztof\""}},`,
		`"INAPPROVAL":   {"*": {"write": "USER() = \"krzysztof\""}, "note": {"write": ["ACCOUNTING"]}},`)
	if err != nil || !strings.Contains(out, "2. anna writes a note:                 allowed") {
		t.Errorf("accounting writes notes during approval: %v\n%s", err, out)
	}
	// A group in the place of a person.
	out, err = changed(t, `"approve": "USER() = \"krzysztof\""`, `"approve": "INGROUP(\"MANAGERS\")"`)
	if err == nil || !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), "/access/transition/approve") || !strings.Contains(out, "4. anna approves") {
		t.Errorf("a group approves: %v\n%s", err, out)
	}
	// A mistake in a formula is found when the class is defined.
	out, err = changed(t, `"guard": "gross > 0"`, `"guard": "gros > 0"`)
	if err == nil || !strings.Contains(err.Error(), "/transitions/0/guard") || out != "" {
		t.Errorf("a misspelt variable: %v\n%s", err, out)
	}
}
