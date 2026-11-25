package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
)

// A list that is kept (ADR-0078, item 5) is a triggered variable whose
// whole formula is a QUERY. At the variable's triggers the store asks
// the question the QUERY is, over the documents of its source, and keeps
// the answer with the document and with each of its versions, as it
// keeps a list somebody entered: rows of the variable's child table. A
// decision that stood on other documents then stands on rows the
// document holds, and what reads an entered list reads this one, a guard
// included.
//
// It is read with the engine's eyes (ADR-0046, item 2): every document
// of the source that the condition takes, whoever caused the list to be
// brought, with every column the QUERY names. Who sees the list is the
// read right of the variable. It is asked on the writer, inside the
// transaction of the commit, so it sees what a LOOKUP of the same commit
// sees: the documents as stored, the moves of a batch before this one
// among them.

// keptList is a list brought in the step of a commit that is being
// computed, until the step takes it.
type keptList struct {
	ch   *child
	rows []childRow
}

// keptRows asks the QUERY of a kept list for the document the row is,
// and answers its rows in the order of the QUERY, each in the layout of
// the variable's child table. A list longer than a list may be,
// MaxElements, is refused with too_many_elements.
func (s *Store) keptRows(c *class, cv *cvar, row *docRow, evalCtx *formula.Context) ([]childRow, *Error) {
	q := cv.v.Kept
	fail := func(code, msg string) ([]childRow, *Error) {
		return nil, &Error{Code: code, Variable: cv.v.Name, Message: msg}
	}
	src, e := s.class(q.Source)
	if e != nil {
		return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+" reads the class "+q.Source+", which the database no longer has")
	}
	on, err := listOn(c, q, row)
	if err != nil {
		return fail(formula.ErrQueryFailed, err.Error())
	}
	// The engine's eyes: no read rule, and every column whatever its
	// rights say.
	plan, err := query.CompileOn(Filter{Class: src.cls.Name, Order: q.Order, Limit: MaxElements}, on, src.cls, s.sources(), query.Principal{Content: true})
	if err != nil {
		fe, _ := filterErr(err).(*Error)
		if fe == nil {
			return fail(formula.ErrQueryFailed, err.Error())
		}
		return fail(fe.Code, fe.Message)
	}
	cols := append(quoteAll(systemColumns), quoteAll(columnNames(src.cls.Columns))...)
	cols = append(cols, quoteAll(src.atNames())...)
	text := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(src.cls.Name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	text += " ORDER BY " + plan.Order + " LIMIT " + fmt.Sprint(MaxElements+1)
	args := append(plan.Args, formula.NewSQLite().ContextArgs(evalCtx)...)
	ctx := s.wev.commit.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	found, qerr := s.writer.QueryContext(ctx, text, args...)
	if qerr != nil {
		return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+": "+qerr.Error())
	}
	defer found.Close()
	// Where each column of the list comes from in a row of the source:
	// a system column, or the leaf of a variable.
	ch := cv.child
	from := make([]func(*docRow) formula.Value, len(ch.leaves))
	for i, l := range ch.leaves {
		name := l.path
		if sv, ok := src.byName[strings.ToUpper(name)]; ok && sv.leaf >= 0 {
			leaf := sv.leaf
			from[i] = func(r *docRow) formula.Value { return r.vals[leaf] }
			continue
		}
		get, ok := systemValue(name)
		if !ok {
			return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+" keeps the column "+name+", which the class "+src.cls.Name+" no longer has")
		}
		from[i] = get
	}
	var out []childRow
	for found.Next() {
		srow, err := src.scanRow(found, false)
		if err != nil {
			return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+": "+err.Error())
		}
		if len(out) == MaxElements {
			return fail(CodeTooManyElements, fmt.Sprintf("the list %s would keep more than %d rows", cv.v.Name, MaxElements))
		}
		kept := make(childRow, len(from))
		for i, get := range from {
			kept[i] = get(srow)
		}
		out = append(out, kept)
	}
	if err := found.Err(); err != nil {
		return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+": "+err.Error())
	}
	return out, nil
}

// systemValue reads a system column a QUERY may list (model.FilterColumns)
// out of a row, as a value of the column's type.
func systemValue(name string) (func(*docRow) formula.Value, bool) {
	text := func(s string) formula.Value {
		if s == "" {
			return formula.Blank()
		}
		return formula.Text(s)
	}
	moment := func(secs int64) formula.Value {
		if secs == 0 {
			return formula.Blank()
		}
		return formula.DateTime(secs)
	}
	switch strings.ToLower(name) {
	case "id":
		return func(r *docRow) formula.Value { return formula.Text(r.id) }, true
	case "title":
		return func(r *docRow) formula.Value { return text(r.title) }, true
	case "state":
		return func(r *docRow) formula.Value { return text(r.state) }, true
	case "version":
		return func(r *docRow) formula.Value { return formula.Number(float64(r.version)) }, true
	case "class_version":
		return func(r *docRow) formula.Value { return formula.Number(float64(r.classVersion)) }, true
	case "created_at":
		return func(r *docRow) formula.Value { return moment(r.life.CreatedAt) }, true
	case "updated_at":
		return func(r *docRow) formula.Value { return moment(r.life.UpdatedAt) }, true
	case "state_entered_at":
		return func(r *docRow) formula.Value { return moment(r.life.EnteredAt) }, true
	}
	return nil, false
}

// keepList stores the rows a kept list was brought as, with its moment:
// the row's version and secs as its time. The step of the commit that is
// being computed takes the rows for its write; the functions over the
// list that run from here on read them; and what is worked out from the
// list by itself is worked out again.
func (s *Store) keepList(c *class, cv *cvar, row *docRow, rows []childRow, evalCtx *formula.Context, secs int64, preview bool, errs *[]*Error) *Error {
	row.vals[cv.asof] = formula.Decimal(row.version, 0)
	row.vals[cv.errLeaf] = formula.Blank()
	row.ats[cv.tidx] = secs
	if cl := &s.wev.commit; cl.c == c && cl.c != nil && (cl.id == row.id || cl.id == "") {
		if cl.over == nil {
			cl.over = map[*child][]childRow{}
		}
		cl.over[cv.child] = rows
		s.keptNow = append(s.keptNow, keptList{cv.child, rows})
	} else {
		// No commit: the listing of transitions, over a copy of the row
		// it throws away. The guard asked next reads the rows from here.
		if s.wev.shown == nil {
			s.wev.shown = map[*child][]childRow{}
		}
		s.wev.shown[cv.child], s.wev.shownRow = rows, row
	}
	dirty := s.dirtyFor(c)
	dirty[cv.idx] = true
	return s.recalc(c, row, dirty, false, evalCtx, preview, errs)
}

// takeKept gives the lists brought since the last call to the step of
// the commit they were brought in: into, the lists that step writes.
func (s *Store) takeKept(into map[*child][]childRow) {
	for _, k := range s.keptNow {
		into[k.ch] = k.rows
	}
	s.keptNow = s.keptNow[:0]
}

var _ = model.MomentAsOf
