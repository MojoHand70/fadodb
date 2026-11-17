package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const claim = `{"name": "Claim", "variables": {
  "number":   {"type": "text", "required": true},
  "scan":     {"type": "object", "schema": "File"},
  "receipts": {"type": "rows", "schema": "File"},
  "scanned":  {"formula": "NOT ISBLANK(scan.hash)"}
}, "states": {"initial": "open", "list": ["open"]}}`

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBlobStores(t *testing.T) {
	for _, kind := range []string{"sqlite", "dir"} {
		t.Run(kind, func(t *testing.T) {
			s, path := openStore(t, Options{BlobStore: kind, MaxBlob: 64})
			ctx := context.Background()
			if s.BlobStoreKind() != kind || s.MaxBlob() != 64 {
				t.Fatalf("kind %s limit %d", s.BlobStoreKind(), s.MaxBlob())
			}
			content := []byte("hello, world")
			fact, err := s.PutBlob(ctx, "hello.txt", "", bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			if fact["hash"] != sha(content) || fact["name"] != "hello.txt" || fact["media_type"] != "text/plain; charset=utf-8" || fact["size"] != int64(12) {
				t.Errorf("fact %s", dump(fact))
			}
			// The same bytes again are the same blob; a given media type is kept.
			again, err := s.PutBlob(ctx, "copy.txt", "text/x-custom", bytes.NewReader(content))
			if err != nil || again["hash"] != fact["hash"] || again["media_type"] != "text/x-custom" {
				t.Errorf("second put: %v %s", err, dump(again))
			}
			if size, ok, err := s.Blobs().Stat(ctx, fact["hash"].(string)); err != nil || !ok || size != 12 {
				t.Errorf("stat: %d %v %v", size, ok, err)
			}
			if _, ok, _ := s.Blobs().Stat(ctx, strings.Repeat("0", 64)); ok {
				t.Error("an unknown hash is held")
			}
			// A content administrator opens by hash; anyone else does not.
			rc, size, err := s.OpenBlob(ctx, fact["hash"].(string), readAs(root)...)
			if err != nil || size != 12 || readAll(t, rc) != "hello, world" {
				t.Errorf("admin open: %v %d", err, size)
			}
			if _, _, err := s.OpenBlob(ctx, fact["hash"].(string), As(alice)); code(err) != CodeNotAllowed {
				t.Errorf("open by hash as a user: %v", err)
			}
			if _, _, err := s.OpenBlob(ctx, strings.Repeat("0", 64), readAs(root)...); code(err) != CodeUnknownBlob {
				t.Errorf("open an unknown hash: %v", err)
			}
			// Past the limit nothing is stored.
			big := bytes.Repeat([]byte("x"), 65)
			if _, err := s.PutBlob(ctx, "big.bin", "", bytes.NewReader(big)); code(err) != CodeBlobTooLarge {
				t.Errorf("too large: %v", err)
			}
			if _, ok, _ := s.Blobs().Stat(ctx, sha(big)); ok {
				t.Error("a refused blob was stored")
			}
			if kind == "dir" {
				h := fact["hash"].(string)
				if _, err := os.Stat(filepath.Join(path+".blobs", h[:2], h)); err != nil {
					t.Errorf("dir layout: %v", err)
				}
				if leftovers, _ := filepath.Glob(filepath.Join(path+".blobs", "*.tmp")); len(leftovers) > 0 {
					t.Errorf("temporary files left: %v", leftovers)
				}
			}
			if err := s.Blobs().Delete(ctx, fact["hash"].(string)); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := s.Blobs().Stat(ctx, fact["hash"].(string)); ok {
				t.Error("deleted blob is held")
			}
			if err := s.Blobs().Delete(ctx, fact["hash"].(string)); err != nil {
				t.Errorf("deleting twice: %v", err)
			}
			// The kind and the limit are the database's.
			s.Close()
			if _, err := Open(path, Options{BlobStore: map[string]string{"sqlite": "dir", "dir": "sqlite"}[kind]}); code(err) != CodeBadValue {
				t.Errorf("reopen with another kind: %v", err)
			}
			if _, err := Open(path, Options{MaxBlob: 32}); code(err) != CodeBadValue {
				t.Errorf("lowering the limit: %v", err)
			}
			s2, err := Open(path, Options{MaxBlob: 128})
			if err != nil {
				t.Fatal(err)
			}
			defer s2.Close()
			if s2.BlobStoreKind() != kind || s2.MaxBlob() != 128 {
				t.Errorf("after reopen: kind %s limit %d", s2.BlobStoreKind(), s2.MaxBlob())
			}
			if _, err := s2.PutBlob(ctx, "big.bin", "", bytes.NewReader(big)); err != nil {
				t.Errorf("after raising the limit: %v", err)
			}
		})
	}
	if _, err := Open(filepath.Join(t.TempDir(), "x.fado"), Options{BlobStore: "s3"}); code(err) != CodeBadValue {
		t.Errorf("unknown kind: %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "x.fado"), Options{BlobStore: "external"}); code(err) != CodeBadValue {
		t.Errorf("external without a store: %v", err)
	}
}

func TestCommit_Files(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(claim), asSetup); err != nil {
		t.Fatal(err)
	}
	scan, _ := s.PutBlob(ctx, "scan.pdf", "application/pdf", strings.NewReader("%PDF-1.4 scan"))
	r1, _ := s.PutBlob(ctx, "r1.jpg", "image/jpeg", strings.NewReader("jpeg one"))
	r2, _ := s.PutBlob(ctx, "r2.jpg", "image/jpeg", strings.NewReader("jpeg two"))
	doc, err := s.Create(ctx, "Claim", Values{"number": "C-1", "scan": scan, "receipts": []any{r1, r2}}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Values["scanned"] != true || doc.Values["scan"].(map[string]any)["size"] != "13" || len(doc.Values["receipts"].([]any)) != 2 {
		t.Errorf("created: %s", dump(doc.Values))
	}
	// A blank File is checked for nothing.
	blank, err := s.Create(ctx, "Claim", Values{"number": "C-2"})
	if err != nil || blank.Values["scanned"] != false {
		t.Errorf("blank file: %v %s", err, dump(blank))
	}
	// Unknown hashes and wrong sizes are refused, naming the variable.
	for _, tc := range []struct {
		values Values
		vari   string
	}{
		{Values{"number": "x", "scan": Values{"hash": strings.Repeat("a", 64), "size": 13}}, "scan"},
		{Values{"number": "x", "scan": Values{"hash": scan["hash"], "size": 99}}, "scan"},
		{Values{"number": "x", "scan": Values{"hash": scan["hash"]}}, "scan"},
		{Values{"number": "x", "receipts": []any{r1, Values{"hash": strings.Repeat("b", 64), "size": 1}}}, "receipts[1].hash"},
	} {
		_, err := s.Create(ctx, "Claim", tc.values)
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeUnknownBlob || !strings.HasPrefix(e.Variable, strings.TrimSuffix(tc.vari, ".hash")) {
			t.Errorf("%s: want unknown_blob on %s, got %v", dump(tc.values), tc.vari, err)
		}
	}
	// A file is opened through its document; a receipt too.
	rc, info, err := s.OpenFile(ctx, "Claim", doc.ID, scan["hash"].(string), As(alice))
	if err != nil || info.Name != "scan.pdf" || info.MediaType != "application/pdf" || info.Size != 13 || readAll(t, rc) != "%PDF-1.4 scan" {
		t.Errorf("open file: %v %+v", err, info)
	}
	rc, info, err = s.OpenFile(ctx, "Claim", doc.ID, r2["hash"].(string))
	if err != nil || info.Name != "r2.jpg" || readAll(t, rc) != "jpeg two" {
		t.Errorf("open a receipt: %v %+v", err, info)
	}
	if _, _, err := s.OpenFile(ctx, "Claim", doc.ID, strings.Repeat("c", 64)); code(err) != CodeUnknownBlob {
		t.Errorf("a hash the document does not hold: %v", err)
	}
	if _, _, err := s.OpenFile(ctx, "Claim", "nope", scan["hash"].(string)); code(err) != CodeUnknownDocument {
		t.Errorf("unknown document: %v", err)
	}
	// Replacing the scan is a version; the old one opens at its version.
	scan2, _ := s.PutBlob(ctx, "scan2.pdf", "application/pdf", strings.NewReader("%PDF-1.4 second"))
	if _, err := s.Set(ctx, "Claim", doc.ID, Values{"scan": scan2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenFile(ctx, "Claim", doc.ID, scan["hash"].(string)); code(err) != CodeUnknownBlob {
		t.Errorf("the old scan on the current version: %v", err)
	}
	rc, info, err = s.OpenFile(ctx, "Claim", doc.ID, scan["hash"].(string), AtVersion(1))
	if err != nil || info.Name != "scan.pdf" || readAll(t, rc) != "%PDF-1.4 scan" {
		t.Errorf("the old scan at version 1: %v %+v", err, info)
	}
	if _, _, err := s.OpenFile(ctx, "Claim", doc.ID, scan["hash"].(string), AtVersion(9)); code(err) != CodeUnknownDocument {
		t.Errorf("a version that does not exist: %v", err)
	}
	// A preview checks the blob too and writes nothing.
	if _, err := s.Preview(ctx, "Claim", doc.ID, Values{"scan": Values{"hash": strings.Repeat("d", 64), "size": 1}}, ""); code(err) != CodeUnknownBlob {
		t.Errorf("preview with an unknown blob: %v", err)
	}
	// A set that leaves the file alone does not check it again, even if
	// the blob is gone.
	if err := s.Blobs().Delete(ctx, scan2["hash"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Claim", doc.ID, Values{"number": "C-1b"}); err != nil {
		t.Errorf("a set beside a missing blob: %v", err)
	}
}

// The check finds a file both ways (ADR-0033, item 7): bytes that are
// not the bytes that were put, and a file that a document holds, in any
// version, whose bytes the store has lost.
func TestCheckBlobs(t *testing.T) {
	for _, kind := range []string{"sqlite", "dir"} {
		t.Run(kind, func(t *testing.T) {
			s, path := openStore(t, Options{BlobStore: kind})
			ctx := context.Background()
			if _, err := s.defineOpen(ctx, []byte(claim), asSetup); err != nil {
				t.Fatal(err)
			}
			put := func(text string) Values {
				t.Helper()
				fact, err := s.PutBlob(ctx, text+".txt", "", strings.NewReader(text))
				if err != nil {
					t.Fatal(err)
				}
				return fact
			}
			hash := func(fact Values) string { return fact["hash"].(string) }
			// What happens to bytes behind the store's back.
			onDisk := func(h string) string { return filepath.Join(path+".blobs", h[:2], h) }
			alter := func(h string) {
				t.Helper()
				var err error
				if kind == "sqlite" {
					_, err = s.writer.ExecContext(ctx, "UPDATE blobs SET bytes = ? WHERE hash = ?", []byte("altered"), h)
				} else {
					err = os.WriteFile(onDisk(h), []byte("altered"), 0o644)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			lose := func(h string) {
				t.Helper()
				var err error
				if kind == "sqlite" {
					_, err = s.writer.ExecContext(ctx, "DELETE FROM blobs WHERE hash = ?", h)
				} else {
					err = os.Remove(onDisk(h))
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			check := func() *BlobCheck {
				t.Helper()
				found, err := s.CheckBlobs(ctx, asSetup)
				if err != nil {
					t.Fatal(err)
				}
				return found
			}

			good, bad := put("good bytes"), put("bad bytes")
			scan, was := put("the scan"), put("the scan as it was")
			first, second := put("the first receipt"), put("the second receipt")
			// C-1 holds a scan and two receipts; in its first version the
			// scan was another file.
			if _, err := s.Create(ctx, "Claim", Values{"number": "C-1", "scan": was, "receipts": []any{first, second}}, WithID("c-1")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Set(ctx, "Claim", "c-1", Values{"scan": scan}); err != nil {
				t.Fatal(err)
			}
			// C-2 holds the first receipt too, and was deleted since: its
			// versions are kept, and so is what they hold.
			if _, err := s.Create(ctx, "Claim", Values{"number": "C-2", "receipts": []any{first}}, WithID("c-2")); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, "Claim", "c-2"); err != nil {
				t.Fatal(err)
			}
			// C-3 holds no file.
			if _, err := s.Create(ctx, "Claim", Values{"number": "C-3"}, WithID("c-3")); err != nil {
				t.Fatal(err)
			}
			if found := check(); found.Checked != 6 || len(found.Mismatched) != 0 || len(found.Missing) != 0 {
				t.Fatalf("a sound database: %s", dump(found))
			}

			// Bytes that were changed are named by their hash, as before.
			alter(hash(bad))
			found := check()
			if found.Checked != 6 || len(found.Mismatched) != 1 || found.Mismatched[0] != hash(bad) || len(found.Missing) != 0 {
				t.Errorf("after bytes were changed: %s", dump(found))
			}

			// Files that are gone are named with the documents that hold
			// them: the scan of the first version of C-1, and the first
			// receipt, which two documents hold. A file no document holds
			// is not missed.
			lose(hash(was))
			lose(hash(first))
			lose(hash(good))
			found = check()
			want := []MissingFile{
				{Hash: hash(was), Class: "Claim", ID: "c-1", Version: 1, Variable: "scan"},
				{Hash: hash(first), Class: "Claim", ID: "c-1", Version: 1, Variable: "receipts"},
				{Hash: hash(first), Class: "Claim", ID: "c-2", Version: 1, Variable: "receipts"},
			}
			if found.Checked != 3 || len(found.Mismatched) != 1 || !reflect.DeepEqual(found.Missing, want) {
				t.Errorf("after files were lost:\n%s\nwant missing\n%s", dump(found), dump(want))
			}
			// What is still there is still read.
			if rc, _, err := s.OpenFile(ctx, "Claim", "c-1", hash(second)); err != nil || readAll(t, rc) != "the second receipt" {
				t.Errorf("a file that was not lost: %v", err)
			}
		})
	}
}

// The check names the variable that holds a missing file, and does not
// look where an earlier version of a class kept files and the class of
// today keeps none.
func TestCheckBlobsAcrossClassVersions(t *testing.T) {
	s, path := openStore(t, Options{BlobStore: "dir"})
	ctx := context.Background()
	const parcel = `{"name": "Parcel", "variables": {
  "number": {"type": "text"},
  "label":  {"type": "object", "schema": "File"},
  "photos": {"type": "rows", "schema": "File"}
}, "states": {"initial": "open", "list": ["open"]}}`
	if _, err := s.defineOpen(ctx, []byte(parcel), asSetup); err != nil {
		t.Fatal(err)
	}
	label, _ := s.PutBlob(ctx, "label.pdf", "", strings.NewReader("the label"))
	photo, _ := s.PutBlob(ctx, "photo.jpg", "", strings.NewReader("the photo"))
	if _, err := s.Create(ctx, "Parcel", Values{"number": "P-1", "label": label, "photos": []any{photo}}, WithID("p-1")); err != nil {
		t.Fatal(err)
	}
	for _, fact := range []Values{label, photo} {
		h := fact["hash"].(string)
		if err := os.Remove(filepath.Join(path+".blobs", h[:2], h)); err != nil {
			t.Fatal(err)
		}
	}
	found, err := s.CheckBlobs(ctx, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	want := []MissingFile{
		{Hash: label["hash"].(string), Class: "Parcel", ID: "p-1", Version: 1, Variable: "label"},
		{Hash: photo["hash"].(string), Class: "Parcel", ID: "p-1", Version: 1, Variable: "photos"},
	}
	if found.Checked != 0 || !reflect.DeepEqual(found.Missing, want) {
		t.Errorf("both files lost:\n%s", dump(found))
	}

	// A slip could hold a scan and no slip ever did, so the next version
	// of the class drops the variable, and its column with it. The check
	// still knows the first version, and does not ask for the column.
	const slip = `{"name": "Slip", "variables": {"number": {"type": "text"}%s}, "states": {"initial": "open", "list": ["open"]}}`
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(slip, `, "scan": {"type": "object", "schema": "File"}`)), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Slip", Values{"number": "S-1"}, WithID("s-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(fmt.Sprintf(slip, "")), asSetup); err != nil {
		t.Fatal(err)
	}
	if again, err := s.CheckBlobs(ctx, asSetup); err != nil || !reflect.DeepEqual(again.Missing, want) {
		t.Errorf("after a class dropped a variable of files: %v\n%s", err, dump(again))
	}
}

// memBlobs is an embedder's blob store: a map, no listing.
type memBlobs struct{ m map[string][]byte }

func (b *memBlobs) Put(ctx context.Context, r io.Reader, max int64) (string, int64, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return "", 0, err
	}
	if int64(len(data)) > max {
		return "", 0, ErrBlobTooLarge
	}
	h := sha(data)
	b.m[h] = data
	return h, int64(len(data)), nil
}

func (b *memBlobs) Open(ctx context.Context, hash string) (io.ReadCloser, int64, error) {
	data, ok := b.m[hash]
	if !ok {
		return nil, 0, ErrNoBlob
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (b *memBlobs) Stat(ctx context.Context, hash string) (int64, bool, error) {
	data, ok := b.m[hash]
	return int64(len(data)), ok, nil
}

func (b *memBlobs) Delete(ctx context.Context, hash string) error {
	delete(b.m, hash)
	return nil
}

func TestExternalBlobStore(t *testing.T) {
	mem := &memBlobs{m: map[string][]byte{}}
	s, path := openStore(t, Options{BlobStore: "external", Blobs: mem})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(claim), asSetup); err != nil {
		t.Fatal(err)
	}
	fact, err := s.PutBlob(ctx, "x.bin", "application/octet-stream", bytes.NewReader([]byte{1, 2, 3}))
	if err != nil || len(mem.m) != 1 {
		t.Fatalf("put: %v %d", err, len(mem.m))
	}
	doc, err := s.Create(ctx, "Claim", Values{"number": "E-1", "scan": fact})
	if err != nil {
		t.Fatal(err)
	}
	if rc, _, err := s.OpenFile(ctx, "Claim", doc.ID, fact["hash"].(string)); err != nil || readAll(t, rc) != "\x01\x02\x03" {
		t.Errorf("open: %v", err)
	}
	if _, err := s.CheckBlobs(ctx, asSetup); code(err) != CodeNotSupported {
		t.Errorf("check without a lister: %v", err)
	}
	s.Close()
	if _, err := Open(path, Options{}); code(err) != CodeBadValue {
		t.Errorf("reopen without the external store: %v", err)
	}
	s2, err := Open(path, Options{Blobs: mem})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.BlobStoreKind() != "external" {
		t.Errorf("kind %s", s2.BlobStoreKind())
	}
}
