package notices

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// THIRD_PARTY_NOTICES.md is the notices of the modules each program and
// library is built from, as they are: a module added, moved or dropped
// fails here until the file is written again. It lists the modules with
// go list for every system a release is built for, which fetches them,
// so it runs when FADODB_NOTICES is set, as it is must on GitHub, and
// FADODB_UPDATE_NOTICES=1 writes the file.
func TestTheNoticesAreCurrent(t *testing.T) {
	update := os.Getenv("FADODB_UPDATE_NOTICES") != ""
	if !update && os.Getenv("FADODB_NOTICES") == "" {
		t.Skip("FADODB_NOTICES=must lists the modules of every program for every system and holds the file to them")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "THIRD_PARTY_NOTICES.md")
	if update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatal("THIRD_PARTY_NOTICES.md is not the notices of the modules as they are: write it again with FADODB_UPDATE_NOTICES=1 go test ./internal/notices/")
	}
}
