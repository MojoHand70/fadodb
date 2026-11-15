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
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Note", "variables": {"text": {"type": "text"}}, "states": {"initial": "open", "list": ["open"]}}`), store.As(store.Principal{ID: "setup", System: []string{store.GroupDesigners}})); err != nil {
		t.Fatal(err)
	}
	fact, err := s.PutBlob(ctx, "a.txt", "", strings.NewReader("attached"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	var out, errOut bytes.Buffer
	// The class of the test and the one the engine brings (ADR-0053).
	if code := run([]string{"check", path}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "2 classes, 0 tables, 1 blobs checked, all match") {
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

	// A note holds a file, and the file is lost behind the store's back:
	// the check names the note, and fails.
	s, err = store.Open(path, store.Options{Outbox: leavesTheQueue})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Letter", "variables": {"scan": {"type": "object", "schema": "File"}}, "states": {"initial": "open", "list": ["open"]},
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), store.As(store.Principal{ID: "setup", System: []string{store.GroupDesigners}})); err != nil {
		t.Fatal(err)
	}
	scan, err := s.PutBlob(ctx, "letter.pdf", "", strings.NewReader("a letter"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Letter", store.Values{"scan": scan}, store.WithID("l-1"), store.As(store.Principal{ID: "ola"})); err != nil {
		t.Fatal(err)
	}
	s.Close()
	db = openRaw(t, path)
	if _, err := db.Exec("DELETE FROM blobs WHERE hash = ?", scan["hash"]); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out.Reset()
	errOut.Reset()
	code := run([]string{"check", path}, &out, &errOut)
	if code != 1 || !strings.Contains(out.String(), "1 files of documents are missing") ||
		!strings.Contains(out.String(), "  Letter l-1, scan, since version 1: "+scan["hash"].(string)) {
		t.Errorf("check after a file was lost: exit %d\n%s%s", code, out.String(), errOut.String())
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

// The tool works on a database and is not its program: it has none of
// the program's Go functions and may call no host. So it leaves the
// queue of the database alone. A check that ran the queue would ask for
// an action it cannot run, and use up one of the action's attempts.
func TestTheToolLeavesTheQueueAlone(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	setup := store.As(store.Principal{ID: "olga", System: []string{store.GroupAdministrators, store.GroupDesigners}})
	// The program of the company: it has the function behind mail.send,
	// and here it runs its queue by hand, so that what it queued is
	// still due when it closes.
	program := func() *store.Store {
		t.Helper()
		s, err := store.Open(path, store.Options{Users: true, Outbox: store.OutboxOptions{Manual: true},
			Actions: map[string]store.ActionFunc{"mail.send": func(context.Context, *store.ActionCall) (store.Values, error) { return nil, nil }}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := program()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "mail.send", "params": {"to": "text"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Order", "variables": {"customer": {"type": "text"}},
 "states": {"initial": "new", "list": ["new", "sent"]}, "transitions": [{"name": "send", "from": "new", "to": "sent"}],
 "rules": [{"on": "transition", "name": "send", "do": [{"action": "mail.send", "args": {"to": "customer"}}]}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", store.Values{"customer": "ann@acme.example"}, setup, store.WithID("o-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Order", "o-1", "send", nil, setup); err != nil {
		t.Fatal(err)
	}
	// Files for the check to read, so that it works for a while.
	file := make([]byte, 1<<20)
	for i := 0; i < 64; i++ {
		file[0] = byte(i)
		if _, err := s.PutBlob(ctx, "f.bin", "", bytes.NewReader(file), setup); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	queued := func(after string) {
		t.Helper()
		s := program()
		defer s.Close()
		rows, err := s.Outbox(ctx, store.OutboxFilter{}, setup)
		if err != nil || len(rows) != 1 {
			t.Fatalf("the queue %s: %v, %d actions", after, err, len(rows))
		}
		if r := rows[0]; r.Status != store.ActionPending || r.Attempts != 0 || r.ErrorCode != "" {
			t.Errorf("%s the action is %s after %d attempts, with %q: the tool ran the queue", after, r.Status, r.Attempts, r.ErrorCode)
		}
	}
	queued("before the tool,")
	if code, out, said := tool("check", path); code != 0 || !strings.Contains(out, "64 blobs checked, all match") {
		t.Fatalf("check: exit %d\n%s%s", code, out, said)
	}
	queued("after fadodb check,")
	if code, _, said := tool("user", "add", "eve", "--kind", "person", "--db", path); code != 0 {
		t.Fatal(said)
	}
	queued("after fadodb user add,")
	if code, _, said := tool("key", "new", "eve", "--db", path); code != 0 {
		t.Fatal(said)
	}
	queued("after fadodb key new,")

	// A database is made by init and by nothing else (ADR-0059, item 1):
	// the check of a name under which there is none makes none.
	typo := filepath.Join(t.TempDir(), "acm.fado")
	if code, _, said := tool("check", typo); code != 1 || !strings.Contains(said, "there is no database at "+typo) {
		t.Errorf("the check of a database that is not there: exit %d\n%s", code, said)
	}
	if exists(typo) {
		t.Error("the check made a database")
	}
}
