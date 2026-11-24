package store

import (
	"context"
	"errors"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Functions over a list the document holds (ADR-0081, step 2): its
// lines, a rows variable, and its tags, an array. A formula that has one
// compiles to a fold, and whoever runs the formula over a document gives
// the fold the elements of that variable. Here that is the evaluator.
//
// In a commit the lists are the document's as the command leaves them:
// the ones the command assigns, and for the others the rows the store
// has at the version the commit began from. Anywhere else they are the
// rows of the version the row is at.

// commitLists is the document a commit works on, for the folds of the
// formulas the commit runs over it.
type commitLists struct {
	ctx context.Context
	c   *class
	// stored is the class as the store has it when c is a draft, which
	// has no statements of its own; nil otherwise.
	stored *class
	// before is the row the commit began from, nil for a create, and id
	// the document's id.
	before *docRow
	id     string
	// kids are the lists the command assigned, the commit's own map.
	// over are the ones a later step of the commit emptied, and loaded
	// the ones read from the store, once each.
	kids, over, loaded map[*child][]childRow
}

// foldPlaces is where the programs of a fold read their slots from in a
// row of the list, and the columns its SORT orders by.
type foldPlaces struct {
	ch *child
	at *formula.FoldPlaces
}

// beginLists says which document the commit in progress works on, so
// that the folds of its formulas read the lists the command gives.
func (s *Store) beginLists(ctx context.Context, c, stored *class, before *docRow, kids map[*child][]childRow) {
	s.wev.commit = commitLists{ctx: ctx, c: c, stored: stored, before: before, kids: kids}
	if before != nil {
		s.wev.commit.id = before.id
	}
}

// emptyList records that a step of the commit emptied a list after the
// command's own assignments, for the folds of the steps that follow.
func (cl *commitLists) emptyList(ch *child) {
	if cl.c == nil {
		return
	}
	if cl.over == nil {
		cl.over = map[*child][]childRow{}
	}
	cl.over[ch] = nil
}

// rows are the elements of a list of the document the commit works on.
func (cl *commitLists) rows(ch *child) ([]childRow, error) {
	if rows, ok := cl.over[ch]; ok {
		return rows, nil
	}
	if rows, ok := cl.kids[ch]; ok {
		return rows, nil
	}
	if rows, ok := cl.loaded[ch]; ok {
		return rows, nil
	}
	var rows []childRow
	if cl.before != nil {
		from := ch
		if cl.stored != nil {
			// A draft has no statements: the stored class reads the rows,
			// when it has the list in the same shape.
			prev, ok := cl.stored.childOfName(ch.v.Name)
			if from = prev; !ok || !sameLeaves(prev.leaves, ch.leaves) {
				from = nil
			}
		}
		if from != nil && from.selectRows != nil {
			var err error
			if rows, err = from.load(cl.ctx, from.selectRows, cl.before.id, cl.before.version); err != nil {
				return nil, err
			}
		}
	}
	if cl.loaded == nil {
		cl.loaded = map[*child][]childRow{}
	}
	cl.loaded[ch] = rows
	return rows, nil
}

// Fold implements formula.ListFolder: the fold over the list of the
// document the evaluator is running a program for.
func (e *evaluator) Fold(q *formula.QuerySlot, args []formula.Value, ctx *formula.Context) (formula.Value, *formula.EvalError) {
	fail := func(msg string) (formula.Value, *formula.EvalError) {
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: msg}
	}
	c, row := e.foldC, e.foldRow
	if c == nil || row == nil {
		return fail("the formula reads the list " + q.Source + " of its document, and runs over no document")
	}
	p, ok := e.places[q]
	if !ok {
		ch, has := c.childOfName(q.Source)
		if !has {
			return fail("the class " + c.cls.Name + " has no list " + q.Source)
		}
		colOf := func(name string) (int, bool) {
			i, ok := ch.leafIndex[strings.ToUpper(name)]
			return i, ok
		}
		at, err := q.FoldPlaces(colOf)
		if err != nil {
			return formula.Value{}, err
		}
		p = &foldPlaces{ch: ch, at: at}
		if e.places == nil {
			e.places = map[*formula.QuerySlot]*foldPlaces{}
		}
		e.places[q] = p
	}
	rows, err := e.listRows(c, p.ch, row)
	if err != nil {
		return fail(err.Error())
	}
	if e.fold == nil {
		e.fold = formula.NewRowFold(e.vm.Limits)
	}
	return e.fold.Run(q, rows, p.at, -1, args, ctx)
}

// listRows are the elements of a list of the document a program runs
// over: the commit's when the document is the one a commit works on,
// the stored rows of the row's version otherwise.
func (e *evaluator) listRows(c *class, ch *child, row *docRow) ([]childRow, error) {
	if cl := &e.commit; cl.c == c && cl.id == row.id && cl.c != nil {
		return cl.rows(ch)
	}
	st := ch.readRows
	if e.writer {
		st = ch.selectRows
	}
	if st == nil {
		return nil, errors.New("the list " + ch.v.Name + " of a document is not read while its class is being changed")
	}
	if row.version == 0 {
		return nil, nil
	}
	return ch.load(context.Background(), st, row.id, row.version)
}
