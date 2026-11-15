package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every commit of the store ends in one place, endTransaction, which
// tells the streams of changes that the log grew (ADR-0058, item 9). A
// commit written beside it would leave the streams asleep until the
// next one. So the statement stands once in the package.
func TestEveryCommitEndsInOnePlace(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var places []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(text), "\n") {
			if strings.Contains(line, `"COMMIT"`) {
				places = append(places, file+":"+itoa(i+1))
			}
		}
	}
	if len(places) != 1 || !strings.HasPrefix(places[0], "changes.go:") {
		t.Errorf("a transaction is committed at %v; it is committed in endTransaction, in changes.go, and nowhere else", places)
	}
}
