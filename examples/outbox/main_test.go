package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The chapter the program belongs to.
const chapter = "../../docs/manual/the-outbox-and-the-worker.md"

// unix reads a file with the line endings of every platform made one.
func unix(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// The chapter shows the program and what it prints. Both are checked
// against the real ones, so the chapter cannot drift from the engine.
func TestTheChapterShowsTheProgram(t *testing.T) {
	page := unix(t, chapter)
	shown := regexp.MustCompile("(?s)<!-- the program -->\n```go\n(.*?)\n```\n").FindStringSubmatch(page)
	if shown == nil {
		t.Fatal("the chapter has no block for the program")
	}
	if got := strings.TrimRight(unix(t, "main.go"), "\n"); got != shown[1] {
		t.Error("main.go is not the program the chapter shows")
	}
	var out bytes.Buffer
	if err := run(&out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	printed := regexp.MustCompile("(?s)<!-- output of examples/outbox -->\n```text\n(.*?)\n```").FindStringSubmatch(page)
	if printed == nil {
		t.Fatal("the chapter has no block for what the program prints")
	}
	if got := strings.TrimRight(out.String(), "\n"); got != printed[1] {
		t.Errorf("the program prints\n%s\n\nand the chapter shows\n%s", got, printed[1])
	}
}
