package store

import (
	"context"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/testdef"
)

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
