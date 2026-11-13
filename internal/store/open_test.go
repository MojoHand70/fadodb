package store

import (
	"context"

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
