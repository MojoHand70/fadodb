package store

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A new database is its owner's alone: the file, the log and the index
// SQLite keeps beside it, and the directory of attachments with what is
// in it. Nobody else on the machine reads a document by reading the
// file.
func TestNewDatabaseIsClosedToOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps permissions in the access list of the directory")
	}
	s, path := openStore(t, Options{BlobStore: "dir"})
	ctx := context.Background()
	defineInvoice(t, s)
	if _, err := s.Create(ctx, "Invoice", sampleValues()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutBlob(ctx, "hello.txt", "", bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	// While the database is open the log and its index exist.
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(name), err)
		}
		if open := info.Mode().Perm() & 0o077; open != 0 {
			t.Errorf("%s is %v: others can reach it", filepath.Base(name), info.Mode().Perm())
		}
	}
	seen := 0
	err := filepath.WalkDir(path+".blobs", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		seen++
		if open := info.Mode().Perm() & 0o077; open != 0 {
			t.Errorf("%s is %v: others can reach it", name, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 3 {
		t.Errorf("walked %d entries of the attachments, want the directory, a shard and the blob", seen)
	}
}

// A database that exists keeps the permissions it has: opening it
// changes nothing its operator set.
func TestExistingDatabaseKeepsItsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps permissions in the access list of the directory")
	}
	path := filepath.Join(t.TempDir(), "shared.fado")
	if err := os.WriteFile(path, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	// WriteFile is subject to the umask; say exactly what is meant.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defineInvoice(t, s)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("the database is %v after opening, want it left at -rw-r-----", got)
	}
}
