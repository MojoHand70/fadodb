package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/testdef"
)

// storedBefore defines a class as a database made by an earlier engine
// holds it: with a definition this engine refuses, which it compiles as
// it was written when it finds it stored (model.Env.Lenient). The class
// is first defined as now, the definition as the earlier engine took it
// with every part this engine refuses replaced by what it takes, and
// then the earlier words are put in its place in the file and the
// database is opened again. The two definitions lay out the same tables.
// o are the options the database was opened with; the store given is
// closed, and the one answered takes its place.
func storedBefore(t *testing.T, s *Store, path string, o Options, earlier string, replace ...string) *Store {
	t.Helper()
	ctx := context.Background()
	cls, err := s.defineOpen(ctx, []byte(strings.NewReplacer(replace...).Replace(earlier)), asSetup)
	if err != nil {
		t.Fatalf("define the class as it is taken now: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stored := string(opened([]byte(earlier)))
	for _, stmt := range []string{`UPDATE classes SET json = ? WHERE name = ?`, `UPDATE class_versions SET json = ? WHERE class = ?`} {
		res, err := raw.Exec(stmt, stored, cls.Name)
		if n, _ := res.RowsAffected(); err != nil || n != 1 {
			t.Fatalf("%s: %d rows, %v", stmt, n, err)
		}
	}
	raw.Close()
	o.Outbox.Manual = true
	again, err := Open(path, o)
	if err != nil {
		t.Fatalf("open a database that holds a definition of an earlier engine: %v", err)
	}
	t.Cleanup(func() { again.Close() })
	return again
}

// The tests that are not about who may see or change a document define
// their classes and tables open, with TRUE for every access rule a
// definition leaves out (package testdef): a rule left out lets nobody
// through (ADR-0052, item 5), and these tests were written to run as
// anybody.

func opened(def []byte) []byte         { return testdef.Open(def) }
func openedTable(def []byte) []byte    { return testdef.OpenTable(def) }
func openedAll(defs [][]byte) [][]byte { return testdef.OpenAll(defs) }

func (s *Store) defineOpen(ctx context.Context, def []byte, opts ...Option) (*model.Class, error) {
	return s.DefineClass(ctx, opened(def), opts...)
}

func (s *Store) defineOpenAll(ctx context.Context, defs [][]byte, opts ...Option) ([]*model.Class, error) {
	return s.DefineClasses(ctx, openedAll(defs), opts...)
}

func (s *Store) defineOpenTable(ctx context.Context, def []byte, opts ...Option) (*model.Table, error) {
	return s.DefineTable(ctx, openedTable(def), opts...)
}

// gives records that acts lets by act for them on every class, from now
// until it is withdrawn (ADR-0053): the substitution a test needs before
// a caller of it names somebody.
func gives(t *testing.T, s *Store, by, acts string) *Document {
	t.Helper()
	d, err := s.Create(context.Background(), model.SubstitutionClass, Values{"by": by}, As(Principal{ID: acts}))
	if err != nil {
		t.Fatalf("%s lets %s act for them: %v", acts, by, err)
	}
	return d
}
