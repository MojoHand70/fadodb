package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/MojoHand70/fadodb/internal/store"
)

func TestCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.fado")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Note", "variables": {"text": {"type": "text"}}}`)); err != nil {
		t.Fatal(err)
	}
	fact, err := s.PutBlob(ctx, "a.txt", "", strings.NewReader("attached"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	var out, errOut bytes.Buffer
	if code := run([]string{"check", path}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "1 classes, 0 tables, 1 blobs checked, all match") {
		t.Errorf("check: exit %d\n%s%s", code, out.String(), errOut.String())
	}

	// Alter the blob behind the store's back.
	db := openRaw(t, path)
	if _, err := db.Exec("UPDATE blobs SET bytes = ? WHERE hash = ?", []byte("tampered"), fact["hash"]); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out.Reset()
	errOut.Reset()
	if code := run([]string{"check", path}, &out, &errOut); code != 1 || !strings.Contains(out.String(), "1 do not match") || !strings.Contains(out.String(), fact["hash"].(string)) {
		t.Errorf("check after tampering: exit %d\n%s%s", code, out.String(), errOut.String())
	}

	if code := run([]string{"check"}, &out, &errOut); code != 2 {
		t.Errorf("no file: exit %d", code)
	}
	if code := run([]string{"check", filepath.Join(t.TempDir(), "missing", "x.fado")}, &out, &errOut); code != 1 {
		t.Errorf("unopenable file: exit %d", code)
	}
}

// openRaw opens the database file with the driver alone, as a tool that
// knows nothing of the engine would.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
