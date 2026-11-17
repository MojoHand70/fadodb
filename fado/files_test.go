package fado_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on files is a reference: how a file is attached to a
// document, who may open it, and where its bytes are kept. TestFiles
// takes the class out of the chapter, plays its story step by step, and
// holds every table of the chapter to what the engine answers.

// memoryStore is a store of the host program, for the row of the
// chapter on the store called external: it keeps bytes in memory and
// cannot list what it keeps.
type memoryStore struct{ kept map[string][]byte }

func (m *memoryStore) Put(_ context.Context, r io.Reader, _ int64) (string, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	m.kept[hash] = data
	return hash, int64(len(data)), nil
}

func (m *memoryStore) Open(_ context.Context, hash string) (io.ReadCloser, int64, error) {
	data, ok := m.kept[hash]
	if !ok {
		return nil, 0, errors.New("not kept")
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (m *memoryStore) Stat(_ context.Context, hash string) (int64, bool, error) {
	data, ok := m.kept[hash]
	return int64(len(data)), ok, nil
}

func (m *memoryStore) Delete(_ context.Context, hash string) error {
	delete(m.kept, hash)
	return nil
}

// zeros is a stream of zero bytes without end.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestFiles(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "files.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	class := notionClass(t, page)
	scan := []byte("%PDF-1.4 a small receipt")
	better := []byte("%PDF-1.4 the same receipt, scanned straight")
	paper := []byte("checked against GL")

	ola, anna, piotr := person("ola"), person("anna", "ACCOUNTING"), person("piotr")
	celina := fado.As(fado.Principal{ID: "celina", Kind: fado.ActorPerson, System: []string{fado.GroupContentAdministrators}})
	why := fado.Reason("the auditor asked for the first scan")
	adam := fado.As(fado.Principal{ID: "adam", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}})

	db := notionDB(t, fado.Options{})
	if _, err := db.DefineClass(ctx, class, setup); err != nil {
		t.Fatal(err)
	}
	// put keeps bytes and answers with the facts of the file.
	put := func(name string, data []byte, as ...fado.Option) fado.Values {
		t.Helper()
		fact, err := db.PutBlob(ctx, name, "", bytes.NewReader(data), as...)
		if err != nil {
			t.Fatal(err)
		}
		return fact
	}
	// opened says what a caller who opens a file is answered: the
	// number of bytes when they are the ones expected, or the refusal.
	opened := func(want []byte) func(rc io.ReadCloser, err error) string {
		return func(rc io.ReadCloser, err error) string {
			if err != nil {
				return told(err)
			}
			defer rc.Close()
			got, err := io.ReadAll(rc)
			if err != nil || !bytes.Equal(got, want) {
				return fmt.Sprintf("other bytes: %q, %v", got, err)
			}
			return fmt.Sprintf("the %d bytes", len(got))
		}
	}
	file := func(id, hash string, opts ...fado.Option) (io.ReadCloser, error) {
		rc, _, err := db.OpenFile(ctx, "Receipt", id, hash, opts...)
		return rc, err
	}
	blob := func(hash string, as ...fado.Option) (io.ReadCloser, error) {
		rc, _, err := db.OpenBlob(ctx, hash, as...)
		return rc, err
	}
	versioned := func(doc *fado.Document, err error) string {
		if err != nil {
			return told(err)
		}
		return fmt.Sprintf("allowed, version %d", doc.Version)
	}

	// The story.
	story := taleOf(t, page)
	step := story.step
	fact := put("taxi.pdf", scan, ola)
	hash := fmt.Sprint(fact["hash"])
	if answered, _ := json.MarshalIndent(fact, "", "  "); string(answered) == fenced(t, page, "the facts of the file") {
		step(1, "the facts above")
	} else {
		step(1, string(answered))
	}
	receipt, err := db.Create(ctx, "Receipt", fado.Values{"what": "taxi", "scan": fact}, ola)
	step(2, versioned(receipt, err))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Create(ctx, "Receipt", fado.Values{"what": "ghost", "scan": map[string]any{"hash": strings.Repeat("0", 64), "name": "ghost.pdf", "size": 5}}, ola)
	step(3, told(err))
	_, err = db.Create(ctx, "Receipt", fado.Values{"what": "taxi", "scan": map[string]any{"hash": hash, "name": "taxi.pdf", "size": 3}}, ola)
	step(4, told(err))
	step(5, opened(scan)(file(receipt.ID, hash, ola)))
	step(6, opened(scan)(file(receipt.ID, hash, piotr)))
	step(7, opened(scan)(blob(hash, piotr)))
	working := put("working-paper.txt", paper, anna)
	step(8, versioned(db.Set(ctx, "Receipt", receipt.ID, fado.Values{"audit": working}, anna)))
	step(9, opened(paper)(file(receipt.ID, fmt.Sprint(working["hash"]), ola)))
	step(10, opened(paper)(file(receipt.ID, fmt.Sprint(working["hash"]), anna)))
	second := put("taxi-better.pdf", better, ola)
	step(11, versioned(db.Set(ctx, "Receipt", receipt.ID, fado.Values{"scan": second}, ola)))
	step(12, opened(scan)(file(receipt.ID, hash, ola)))
	step(13, opened(scan)(file(receipt.ID, hash, ola, fado.AtVersion(1))))
	step(14, versioned(db.Set(ctx, "Receipt", receipt.ID, fado.Values{"scan": nil}, ola)))
	_, err = db.Transition(ctx, "Receipt", receipt.ID, "file", nil, ola)
	step(15, told(err))
	step(16, opened(scan)(blob(hash, celina)))
	step(17, opened(scan)(blob(hash, celina, why)))
	check, err := db.CheckBlobs(ctx, adam)
	if err != nil {
		t.Fatal(err)
	}
	checked := check.Checked
	step(18, fmt.Sprintf("%d checked, %d wrong, %d missing", checked, len(check.Mismatched), len(check.Missing)))
	story.done()

	// What the history keeps.
	history, err := db.History(ctx, "Receipt", receipt.ID, ola)
	if err != nil {
		t.Fatal(err)
	}
	var kept [][]string
	for _, version := range history {
		name := "blank"
		if held, ok := version.Values["scan"].(map[string]any); ok {
			name = fmt.Sprint(held["name"])
		}
		kept = append(kept, []string{fmt.Sprint(version.Version), version.ActorID, name})
	}
	if want := rows(marked(t, page, "the history")); !reflect.DeepEqual(kept, want) {
		t.Errorf("the history keeps %v, and the chapter says %v", kept, want)
	}

	// The facts: the four the engine answers, and who says each.
	var facts, answered []string
	for _, row := range rows(marked(t, page, "the facts")) {
		facts = append(facts, row[0])
	}
	for name := range fact {
		answered = append(answered, name)
	}
	sort.Strings(facts)
	sort.Strings(answered)
	if !reflect.DeepEqual(facts, answered) {
		t.Errorf("the chapter lists the facts %v, and the engine answers %v", facts, answered)
	}
	renamed, err := db.PutBlob(ctx, "copy.txt", "text/plain", bytes.NewReader(scan), ola)
	if err != nil || renamed["hash"] != fact["hash"] || renamed["media_type"] != "text/plain" || renamed["name"] != "copy.txt" {
		t.Errorf("the same bytes under another name and kind: %v, %v", renamed, err)
	}
	if again, err := db.CheckBlobs(ctx, adam); err != nil || again.Checked != checked {
		t.Errorf("the same bytes put twice are kept more than once: %v, %v", again, err)
	}

	// Attaching.
	nameless, err := db.Create(ctx, "Receipt", fado.Values{"what": "parking", "scan": map[string]any{"name": "parking.pdf"}}, ola)
	if err != nil {
		t.Errorf("a file with a name and no hash: %v", err)
	} else if _, err := db.Transition(ctx, "Receipt", nameless.ID, "file", nil, ola); answer(err) != "guard_false" {
		t.Errorf("a receipt whose file has no hash is filed: %v", err)
	}
	_, err = db.Set(ctx, "Receipt", receipt.ID, fado.Values{"what": "bus"}, anna)
	want(t, "Anna writes another value than the working paper", err, "forbidden, variable what")
	if after, _ := db.Get(ctx, "Receipt", receipt.ID, ola); after == nil || after.Values["large"] != false {
		t.Errorf("large is %v", after)
	} else if _, sees := after.Values["audit"]; sees {
		t.Error("Ola is shown the working paper")
	}
	if got := finding(db, scratch("Counts", `"pages":{"type":"rows","schema":"File"},"n":{"formula":"COUNT(pages.hash)"}`, oneState)); got != "bad_formula" {
		t.Errorf("a formula over the files of rows is found %s", got)
	}

	// Opening. Piotr holds the same bytes on a receipt of his own.
	own, err := db.Create(ctx, "Receipt", fado.Values{"what": "the same taxi", "scan": fact, "pages": []any{second}}, piotr)
	if err != nil {
		t.Fatal(err)
	}
	// says writes what a caller who opens a file is answered as the
	// table of the chapter does: the bytes, or the code.
	says := func(rc io.ReadCloser, info *fado.FileInfo, err error) string {
		if err != nil {
			return codeOf(err)
		}
		rc.Close()
		if info == nil || info.Hash == "" {
			return "the bytes"
		}
		return "the bytes, and the facts as the document holds them"
	}
	is := func(what string, got string) func(row []string) {
		return func(row []string) {
			t.Helper()
			if !strings.HasPrefix(got, row[1]) {
				t.Errorf("a caller who %s is answered %s, and the chapter says %s", what, got, row[1])
			}
		}
	}
	checks(t, page, "opening", map[string]func(row []string){
		"sees the document, and may read the variable that holds the file": func(row []string) {
			rc, info, err := db.OpenFile(ctx, "Receipt", own.ID, hash, piotr)
			if got := says(rc, info, err); got != row[1] {
				t.Errorf("a caller who may read the file is answered %s, and the chapter says %s", got, row[1])
			}
			if info == nil || info.Name != "taxi.pdf" || info.MediaType != "application/pdf" || info.Size != 24 {
				t.Errorf("the facts as the document holds them: %v", info)
			}
		},
		"may not see the document":                                is("may not see the document", says(db.OpenFile(ctx, "Receipt", receipt.ID, hash, piotr, fado.AtVersion(1)))),
		"sees the document, and may not read the variable":        is("may not read the variable", says(db.OpenFile(ctx, "Receipt", receipt.ID, fmt.Sprint(working["hash"]), ola))),
		"names a hash the document does not hold in this version": is("names a hash the document does not hold", says(db.OpenFile(ctx, "Receipt", receipt.ID, hash, ola))),
		"names a version, and the document held the hash then": func(row []string) {
			rc, _, err := db.OpenFile(ctx, "Receipt", receipt.ID, hash, ola, fado.AtVersion(2))
			if got := says(rc, nil, err); got != row[1] {
				t.Errorf("a caller who names a version is answered %s, and the chapter says %s", got, row[1])
			}
		},
	})
	// A file in rows is opened the same way.
	if got := opened(better)(file(own.ID, fmt.Sprint(second["hash"]), piotr)); got != fmt.Sprintf("the %d bytes", len(better)) {
		t.Errorf("a file of the pages: %s", got)
	}
	// A deleted document keeps its files.
	if err := db.Delete(ctx, "Receipt", receipt.ID, ola); err != nil {
		t.Fatal(err)
	}
	if got := opened(scan)(file(receipt.ID, hash, ola, fado.AtVersion(1))); got != "the 24 bytes" {
		t.Errorf("a file of a deleted document, as of version 1: %s", got)
	}
	if after, err := db.CheckBlobs(ctx, adam); err != nil || after.Checked != checked || len(after.Missing) != 0 {
		t.Errorf("after the delete the database keeps other files than the %d it kept: %v, %v", checked, after, err)
	}

	// Who may.
	lacks := func(row []string, err error) {
		t.Helper()
		var got *fado.Error
		if !errors.As(err, &got) || got.Code != "forbidden" || !strings.Contains(row[1], "the system group "+got.Path[strings.LastIndex(got.Path, "/")+1:]) {
			t.Errorf("to %s the chapter asks for %q, and the engine answers %v", row[0], row[1], err)
		}
	}
	checks(t, page, "who may", map[string]func(row []string){
		"put bytes": func(row []string) {
			if _, err := db.PutBlob(ctx, "anonymous.txt", "", strings.NewReader("from nobody")); err != nil || row[1] != "nothing" {
				t.Errorf("a call that names nobody puts bytes: %v; the chapter asks for %q", err, row[1])
			}
		},
		"attach, replace or take off a file": func(row []string) {
			_, err := db.Set(ctx, "Receipt", own.ID, fado.Values{"audit": working}, piotr)
			var got *fado.Error
			if !errors.As(err, &got) || got.Code != "forbidden" || got.Variable != "audit" || !strings.Contains(row[1], "write the variable") {
				t.Errorf("Piotr attaches a working paper: %v; the chapter asks for %q", err, row[1])
			}
		},
		"open a file": func(row []string) {
			// Both halves are rows of the table of opening, above.
			if !strings.Contains(row[1], "see the document") || !strings.Contains(row[1], "read the variable") {
				t.Errorf("the chapter asks for %q to open a file", row[1])
			}
		},
		"open bytes by their hash alone": func(row []string) {
			for who, as := range map[string]fado.Option{"Ola": ola, "an administrator": adam, "a designer": setup} {
				if got := opened(scan)(blob(hash, as)); got != "refused, not_allowed" {
					t.Errorf("%s opens bytes by their hash: %s", who, got)
				}
			}
			if got := opened(scan)(blob(hash, celina, why)); got != "the 24 bytes" || !strings.Contains(row[1], "the system group content-administrators, and a reason") {
				t.Errorf("a content administrator opens bytes by their hash: %s; the chapter asks for %q", got, row[1])
			}
		},
		"check the files": func(row []string) {
			_, err := db.CheckBlobs(ctx, ola)
			lacks(row, err)
			_, err = db.CheckBlobs(ctx, celina)
			lacks(row, err)
		},
	})

	// The command.
	got, err := db.Exec(ctx, fado.Principal{ID: "adam", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}}, "check_blobs", []byte(`{}`))
	if err != nil || !strings.Contains(string(got), `"checked"`) || !strings.Contains(string(got), `"mismatched":[],"missing":[]`) {
		t.Errorf("the command check_blobs: %s, %v", got, err)
	}

	// Where the bytes are kept: each store in a database of its own.
	dir := t.TempDir()
	create := func(name string, o fado.Options) (*fado.DB, string) {
		t.Helper()
		path := filepath.Join(dir, name)
		made, err := fado.Open(path, o)
		if err != nil {
			t.Fatalf("a database with the store %q: %v", o.BlobStore, err)
		}
		t.Cleanup(func() { made.Close() })
		return made, path
	}
	host := &memoryStore{kept: map[string][]byte{}}
	checks(t, page, "the stores", map[string]func(row []string){
		"sqlite": func([]string) {
			made, path := create("in-the-file.fado", fado.Options{})
			held, err := made.PutBlob(ctx, "a.txt", "", bytes.NewReader(scan), ola)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path + ".blobs"); err == nil {
				t.Error("the store of a database that names none keeps bytes beside the file")
			}
			rc, _, err := made.OpenBlob(ctx, fmt.Sprint(held["hash"]), celina, why)
			if got := opened(scan)(rc, err); got != "the 24 bytes" {
				t.Errorf("the bytes of the sqlite store: %s", got)
			}
			made.Close()
			if _, err := fado.Open(path, fado.Options{BlobStore: "dir"}); codeOf(err) != "bad_value" {
				t.Errorf("a database opened with another store than it was created with: %v", err)
			}
			if again, err := fado.Open(path, fado.Options{BlobStore: "sqlite"}); err != nil {
				t.Errorf("a database opened with the store it has: %v", err)
			} else {
				again.Close()
			}
		},
		"dir": func([]string) {
			made, path := create("beside.fado", fado.Options{BlobStore: "dir"})
			held, err := made.PutBlob(ctx, "a.txt", "", bytes.NewReader(scan), ola)
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprint(held["hash"])
			onDisk := filepath.Join(path+".blobs", name[:2], name)
			if data, err := os.ReadFile(onDisk); err != nil || !bytes.Equal(data, scan) {
				t.Fatalf("the bytes are not in <database>.blobs/<two letters>/<hash>: %v", err)
			}
			if found, err := made.CheckBlobs(ctx, adam); err != nil || found.Checked != 1 || len(found.Mismatched) != 0 || len(found.Missing) != 0 {
				t.Errorf("a check of sound bytes: %v, %v", found, err)
			}
			// The bytes are changed on disk, and the check names them.
			if err := os.Chmod(onDisk, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(onDisk, []byte("%PDF-1.4 a forged receipt"), 0o644); err != nil {
				t.Fatal(err)
			}
			if found, err := made.CheckBlobs(ctx, adam); err != nil || found.Checked != 1 || !reflect.DeepEqual(found.Mismatched, []string{name}) {
				t.Errorf("a check of bytes that were changed on disk: %v, %v", found, err)
			}
			// A receipt holds a second file, and the file is lost: a
			// backup that left it out. The check names the receipt.
			second, err := made.PutBlob(ctx, "b.txt", "", strings.NewReader("a second file"), ola)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := made.DefineClass(ctx, class, setup); err != nil {
				t.Fatal(err)
			}
			doc, err := made.Create(ctx, "Receipt", fado.Values{"what": "taxi", "scan": second}, ola)
			if err != nil {
				t.Fatal(err)
			}
			lost := fmt.Sprint(second["hash"])
			if err := os.Remove(filepath.Join(path+".blobs", lost[:2], lost)); err != nil {
				t.Fatal(err)
			}
			want := []fado.MissingFile{{Hash: lost, Class: "Receipt", ID: doc.ID, Version: 1, Variable: "scan"}}
			if found, err := made.CheckBlobs(ctx, adam); err != nil || found.Checked != 1 || !reflect.DeepEqual(found.Missing, want) {
				t.Errorf("a check after a file was lost: %v, %v", found, err)
			}
		},
		"external": func([]string) {
			made, _ := create("with-the-host.fado", fado.Options{BlobStore: "external", Blobs: host})
			held, err := made.PutBlob(ctx, "a.txt", "", bytes.NewReader(scan), ola)
			if err != nil || !bytes.Equal(host.kept[fmt.Sprint(held["hash"])], scan) {
				t.Fatalf("the bytes are not with the host program: %v", err)
			}
			if _, err := made.DefineClass(ctx, class, setup); err != nil {
				t.Fatal(err)
			}
			doc, err := made.Create(ctx, "Receipt", fado.Values{"what": "taxi", "scan": held}, ola)
			if err != nil {
				t.Fatal(err)
			}
			rc, _, err := made.OpenFile(ctx, "Receipt", doc.ID, fmt.Sprint(held["hash"]), ola)
			if got := opened(scan)(rc, err); got != "the 24 bytes" {
				t.Errorf("a file whose bytes the host program keeps: %s", got)
			}
			if _, err := made.CheckBlobs(ctx, adam); codeOf(err) != "not_supported" {
				t.Errorf("a check of a store that cannot list what it keeps: %v", err)
			}
		},
	})
	if _, err := fado.Open(filepath.Join(dir, "none.fado"), fado.Options{BlobStore: "external"}); codeOf(err) != "bad_value" {
		t.Errorf("the store of the host program, and no store given: %v", err)
	}

	// How large a file may be.
	small, path := create("small.fado", fado.Options{MaxBlob: 1000})
	_, tooLarge := small.PutBlob(ctx, "big.bin", "", strings.NewReader(strings.Repeat("x", 1001)), ola)
	if found, err := small.CheckBlobs(ctx, adam); err != nil || found.Checked != 0 {
		t.Errorf("bytes past the limit were kept: %v, %v", found, err)
	}
	if _, err := small.PutBlob(ctx, "fits.bin", "", strings.NewReader(strings.Repeat("x", 1000)), ola); err != nil {
		t.Errorf("bytes at the limit: %v", err)
	}
	small.Close()
	_, lowered := fado.Open(path, fado.Options{MaxBlob: 500})
	raised, err := fado.Open(path, fado.Options{MaxBlob: 5000})
	if err != nil {
		t.Fatalf("a database opened with a higher limit: %v", err)
	}
	if _, err := raised.PutBlob(ctx, "bigger.bin", "", strings.NewReader(strings.Repeat("x", 3000)), ola); err != nil {
		t.Errorf("bytes under the raised limit: %v", err)
	}
	raised.Close()
	plain, plainPath := create("plain.fado", fado.Options{})
	// The limit of a database that names none: 32 MiB go in, and one
	// byte more does not.
	const limit = 32 << 20
	if _, err := plain.PutBlob(ctx, "at-the-limit.bin", "", io.LimitReader(zeros{}, limit), ola); err != nil {
		t.Errorf("32 MiB into a database with no limit of its own: %v", err)
	}
	if _, err := plain.PutBlob(ctx, "past-the-limit.bin", "", io.LimitReader(zeros{}, limit+1), ola); codeOf(err) != "blob_too_large" {
		t.Errorf("32 MiB and a byte into a database with no limit of its own: %v", err)
	}
	plain.Close()
	_, otherStore := fado.Open(plainPath, fado.Options{BlobStore: "dir"})

	// What is refused, each row by its own call.
	third, err := db.Create(ctx, "Receipt", fado.Values{"what": "tram", "scan": fact}, ola)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]error{
		"puts bytes past the limit of the database": tooLarge,
		"names a hash the engine does not keep, in a document": func() error {
			_, err := db.Set(ctx, "Receipt", third.ID, fado.Values{"scan": map[string]any{"hash": strings.Repeat("a", 64), "size": 1}}, ola)
			return err
		}(),
		"names a hash with another size than the bytes have": func() error {
			_, err := db.Set(ctx, "Receipt", third.ID, fado.Values{"scan": map[string]any{"hash": hash, "size": 25}}, ola)
			return err
		}(),
		"names a hash the engine does not keep, in a row of files": func() error {
			_, err := db.Set(ctx, "Receipt", third.ID, fado.Values{"pages": []any{map[string]any{"hash": strings.Repeat("b", 64), "size": 1}}}, ola)
			return err
		}(),
		"opens a file of a document they may not see": func() error {
			_, _, err := db.OpenFile(ctx, "Receipt", third.ID, hash, piotr)
			return err
		}(),
		"opens a file the document does not hold, or holds in a variable they may not read": func() error {
			_, _, err := db.OpenFile(ctx, "Receipt", third.ID, fmt.Sprint(second["hash"]), ola)
			return err
		}(),
		"opens bytes by their hash alone, and is no content administrator": func() error {
			_, _, err := db.OpenBlob(ctx, hash, anna)
			return err
		}(),
		"opens bytes by their hash alone as a content administrator, and gives no reason": func() error {
			_, _, err := db.OpenBlob(ctx, hash, celina)
			return err
		}(),
		"checks the files, and is no administrator": func() error {
			_, err := db.CheckBlobs(ctx, anna)
			return err
		}(),
		"opens a database with another store than it was created with": otherStore,
		"opens a database with a lower limit than it has":              lowered,
	}
	by := map[string]func(row []string){}
	for what, err := range calls {
		by[what] = func(row []string) {
			var got *fado.Error
			if !errors.As(err, &got) {
				t.Errorf("a caller who %s is answered %v", what, err)
				return
			}
			if got.Code != row[1] {
				t.Errorf("a caller who %s: the engine answers %s, and the chapter says %s", what, got.Code, row[1])
			}
			names := row[2]
			if _, example, ok := strings.Cut(names, ", as "); ok {
				names = example
			}
			if place := got.Variable + got.Path; place != names {
				t.Errorf("a caller who %s: the refusal names %q, and the chapter says %q", what, place, names)
			}
		}
	}
	checks(t, page, "what is refused", by)
}
