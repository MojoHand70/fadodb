package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Figures over a list (ADR-0081). A live variable may count a list of
// its document, or add up a column of it: COUNT(invoices),
// SUM(invoices.net). The list is a live variable whose formula is a
// QUERY, so a figure over it is one more question to the documents of
// the listed class: the rows the list has for whoever reads the
// variable, and of them one aggregate. The engine's own aggregate
// answers it in one statement, and no row is read to be counted
// (ADR-0020). The statement runs beside the writer, as a list does.

// liveHeld is what a live value with figures needs of its document once
// the writer's turn is over: the row as it was read, the context its
// formula runs in, and for each list what the list's condition reads of
// the document.
type liveHeld struct {
	row *docRow
	ctx formula.Context
	on  map[string]*query.On
}

// figureLists finds the lists a live value works figures out of, and
// asks for each, as for a list that is read, whether the caller may read
// the listed class for whom they act (ADR-0053). It answers none for a
// value that has no figure.
func (s *Store) figureLists(ctx context.Context, c *class, cv *cvar, opts []Option) ([]*cvar, *Error) {
	prog := cv.v.Formula
	if prog == nil || len(prog.Queries) == 0 {
		return nil, nil
	}
	var lists []*cvar
	for i := range prog.Queries {
		q := &prog.Queries[i]
		if q.List == "" {
			// A LOOKUP of the formula's own, over the documents of a
			// class: whoever acts for somebody needs the substitution for
			// that class too (ADR-0053). A table asks its own read line.
			if src, e := s.class(q.Source); e == nil {
				if _, e := s.asked(ctx, opts, src.cls.Name); e != nil {
					return nil, e
				}
			}
			continue
		}
		lv, ok := c.byName[strings.ToUpper(q.List)]
		if !ok || lv.v.Query == nil {
			return nil, errf(CodeUnknownVariable, q.List, "%s counts the list %s, which the class %s no longer has", cv.v.Name, q.List, c.cls.Name)
		}
		known := false
		for _, have := range lists {
			known = known || have == lv
		}
		if known {
			continue
		}
		src, e := s.class(lv.v.Query.Source)
		if e != nil {
			return nil, e
		}
		if _, e := s.asked(ctx, opts, src.cls.Name); e != nil {
			return nil, e
		}
		lists = append(lists, lv)
	}
	return lists, nil
}

// liveFigures works out a live value that has figures: its formula runs
// on a reader's evaluator, and each figure in it is asked of the store
// beside the writer.
func (s *Store) liveFigures(ctx context.Context, c *class, cv *cvar, held *liveHeld, o options) (any, error) {
	ev := s.reader()
	x := &figures{s: s, ctx: ctx, o: o, held: held}
	ev.vm.Executor = x
	defer func() {
		ev.vm.Executor = nil
		s.release(ev)
	}()
	val, err := ev.run(c, cv.v.Formula, held.row, &held.ctx)
	if x.err != nil {
		// A refusal or a failure of the store met while a figure was
		// asked is the answer, in the store's own words.
		return nil, x.err
	}
	if err != nil {
		return nil, evalErr(cv.v.Name, err)
	}
	return valueOut(val), nil
}

// figures is the store as the VM's QueryExecutor for one read of a live
// value: it answers the figures of the formula for the caller of that
// read.
type figures struct {
	s    *Store
	ctx  context.Context
	o    options
	held *liveHeld
	err  error
}

// Lookup implements formula.QueryExecutor.
func (x *figures) Lookup(q *formula.QuerySlot, args []formula.Value, fctx *formula.Context) (formula.Value, *formula.EvalError) {
	var v formula.Value
	var err error
	if q.List == "" {
		// A LOOKUP of the formula's own (ADR-0076, item 5): a table, or
		// the documents of a class, for the reader of the value.
		v, err = x.s.liveLookup(x.ctx, q, args, x.o)
	} else if on := x.held.on[strings.ToUpper(q.List)]; on != nil {
		v, err = x.s.figure(x.ctx, q, args, on, x.o)
	} else {
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: "the list " + q.List + " is none of the document's"}
	}
	if err != nil {
		var ee *formula.EvalError
		if errors.As(err, &ee) {
			return formula.Value{}, ee
		}
		if x.err == nil {
			x.err = err
		}
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	return v, nil
}

// figure asks one figure: the aggregate of the slot over the rows its
// list has for the caller, on the document the list was read on. The
// rows are those of the list's own condition, of the read rule of the
// listed class and of the read rights of what the figure reads, which
// the filter compiler writes as it does for the list (ADR-0041,
// ADR-0049); the aggregate is the one a LOOKUP has (ADR-0046), over
// those rows. A column the caller may read in no state gives a blank.
func (s *Store) figure(ctx context.Context, q *formula.QuerySlot, args []formula.Value, on *query.On, o options) (formula.Value, error) {
	src, e := s.class(q.Source)
	if e != nil {
		return formula.Value{}, e
	}
	// A LOOKUP of a live formula has no list: its rows are every
	// document the reader may read, and its own condition picks among
	// them.
	var read query.On
	if on != nil {
		read = *on
	}
	for _, prog := range []*formula.Program{q.Value, q.Cond} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if !strings.HasPrefix(sl.Name, "THIS.") {
				read.Reads = append(read.Reads, sl.Name)
			}
		}
	}
	rows := func(p query.Principal) (string, []any, error) {
		plan, err := query.CompileOn(Filter{Class: src.cls.Name}, &read, src.cls, s.sources(), p)
		if err != nil {
			return "", nil, err
		}
		return plan.Where, plan.Args, nil
	}
	where, given, err := rows(caller(o, src))
	if err != nil {
		var qe *query.Error
		if errors.As(err, &qe) && qe.Code == query.CodeForbidden && qe.Variable != "" {
			return formula.Blank(), nil
		}
		return formula.Value{}, filterErr(err)
	}
	d := formula.NewSQLite().WithLimits(s.limits)
	params := formula.NewParams(d)
	arg := func(i int, currency bool) string {
		if currency {
			return d.Param(fmt.Sprintf("fa%dc", i))
		}
		return d.Param(fmt.Sprintf("fa%d", i))
	}
	table := func(string) string {
		if where == "" {
			return quote(src.cls.Name)
		}
		return "(SELECT * FROM " + quote(src.cls.Name) + " WHERE " + where + ")"
	}
	// An aggregate is one statement over the matching rows, with its
	// error beside it; a column is the first matching row's, in the
	// order of the ids.
	var text string
	if q.Agg != "" {
		text, err = formula.AggregateSQL(q, d, arg, table, params)
	} else {
		var e formula.SQLExpr
		if e, err = formula.LookupSQL(q, d, arg, table, params); err == nil {
			text = lookupSelect(e)
		}
	}
	if err != nil {
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	// The values of the document the figure's own condition names as
	// THIS.name, a money one with its currency beside it.
	money := make([]bool, len(args))
	for _, prog := range []*formula.Program{q.Value, q.Cond} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if i := q.ArgIndex(sl.Name); i >= 0 && sl.Type.Kind == formula.KindMoney {
				money[i] = true
			}
		}
	}
	named := append(given[:len(given):len(given)], s.contextArgs(o)...)
	for i, a := range args {
		named = append(named, sql.Named(fmt.Sprintf("fa%d", i), a.DriverValue()))
		if money[i] {
			var cur any
			if a.Kind() == formula.KindMoney {
				cur = a.Currency()
			}
			named = append(named, sql.Named(fmt.Sprintf("fa%dc", i), cur))
		}
	}
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return formula.Value{}, storageErr("prepare a figure over "+src.cls.Name, err)
	}
	defer s.filters.release(st)
	var v, c any
	var code sql.NullString
	if err := st.stmt.QueryRowContext(ctx, named...).Scan(&v, &c, &code); err != nil {
		return formula.Value{}, storageErr("look in "+src.cls.Name, err)
	}
	if code.Valid {
		over := "the list " + q.List
		if q.List == "" {
			over = src.cls.Name
		}
		return formula.Value{}, &formula.EvalError{Code: code.String, Pos: -1, Msg: q.Agg + " over " + over + ": " + code.String}
	}
	// A content administrator who passes the rules is written down with
	// how many of the documents only the override let them count
	// (ADR-0067, item 3), as for a count.
	if o.records(src) {
		text := "SELECT count(*) FROM " + quote(src.cls.Name)
		if where != "" {
			text += " WHERE " + where
		}
		cst, err := s.filters.acquire(ctx, s.db, text)
		if err != nil {
			return formula.Value{}, storageErr("prepare the count of "+src.cls.Name, err)
		}
		var all int64
		err = cst.stmt.QueryRowContext(ctx, append(given[:len(given):len(given)], s.contextArgs(o)...)...).Scan(&all)
		s.filters.release(cst)
		if err != nil {
			return formula.Value{}, storageErr("count "+src.cls.Name, err)
		}
		past, err := s.pastCount(ctx, src, o, all, func(p query.Principal) (string, []any, error) {
			where, given, err := rows(p)
			var qe *query.Error
			if errors.As(err, &qe) && qe.Code == query.CodeForbidden && qe.Variable != "" {
				return "0", nil, nil // nobody else may read the column: every row was counted past the rules
			}
			if err != nil {
				return "", nil, filterErr(err)
			}
			return where, given, nil
		})
		if err != nil {
			return formula.Value{}, err
		}
		r := newRead("live", src)
		r.counted, r.hasCounted, r.past = past, true, past > 0
		if err := s.logRead(ctx, o, r); err != nil {
			return formula.Value{}, err
		}
	}
	return formula.ValueOfType(v, q.Type, c), nil
}

// lookupSelect is the statement of a LOOKUP that takes one column of the
// first matching row: the value as v, the currency of a money value as
// c, and no error, since a row whose condition would stop with one does
// not match. It has the three columns an aggregate's statement has.
func lookupSelect(e formula.SQLExpr) string {
	cur := "NULL"
	if e.Cur != "" {
		cur = e.Cur
	}
	return "SELECT (" + e.Val + ") AS v, (" + cur + ") AS c, NULL AS e"
}

// liveLookup asks a LOOKUP of a live formula for the reader of the value
// (ADR-0076, items 2 and 5). Over the documents of a class it is asked
// as a figure is: among the documents the read rule lets the reader
// see, and a value the reader may read in no state is blank. Over a
// table the read line of the table is asked of the reader, as the
// command table_rows asks it: a table has no rule for each row, so a
// reader it does not let through is refused, and a content
// administrator passes it with a reason, which the log of reads keeps.
func (s *Store) liveLookup(ctx context.Context, q *formula.QuerySlot, args []formula.Value, o options) (formula.Value, error) {
	if _, e := s.class(q.Source); e == nil {
		return s.figure(ctx, q, args, nil, o)
	}
	t, e := s.table(q.Source)
	if e != nil {
		return formula.Value{}, e
	}
	past, refusal := s.tableRefusal(t, false, o, s.clock(o))
	if refusal != nil {
		return formula.Value{}, refusal
	}
	d := formula.NewSQLite().WithLimits(s.limits)
	params := formula.NewParams(d)
	arg := func(i int, currency bool) string {
		if currency {
			return d.Param(fmt.Sprintf("fa%dc", i))
		}
		return d.Param(fmt.Sprintf("fa%d", i))
	}
	table := func(string) string { return quote(t.name) }
	var text string
	var err error
	if q.Agg != "" {
		text, err = formula.AggregateSQL(q, d, arg, table, params)
	} else {
		var e formula.SQLExpr
		if e, err = formula.LookupSQL(q, d, arg, table, params); err == nil {
			text = lookupSelect(e)
		}
	}
	if err != nil {
		return formula.Value{}, &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: err.Error()}
	}
	money := make([]bool, len(args))
	for _, prog := range []*formula.Program{q.Value, q.Cond, q.Key} {
		if prog == nil {
			continue
		}
		for _, sl := range prog.Slots {
			if i := q.ArgIndex(sl.Name); i >= 0 && sl.Type.Kind == formula.KindMoney {
				money[i] = true
			}
		}
	}
	named := s.contextArgs(o)
	for i, a := range args {
		named = append(named, sql.Named(fmt.Sprintf("fa%d", i), a.DriverValue()))
		if money[i] {
			var cur any
			if a.Kind() == formula.KindMoney {
				cur = a.Currency()
			}
			named = append(named, sql.Named(fmt.Sprintf("fa%dc", i), cur))
		}
	}
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return formula.Value{}, storageErr("prepare a lookup in "+t.def.Name, err)
	}
	defer s.filters.release(st)
	var v, c any
	var code sql.NullString
	if err := st.stmt.QueryRowContext(ctx, named...).Scan(&v, &c, &code); err != nil {
		return formula.Value{}, storageErr("look in "+t.def.Name, err)
	}
	if code.Valid {
		return formula.Value{}, &formula.EvalError{Code: code.String, Pos: -1, Msg: q.Agg + " over " + t.def.Name + ": " + code.String}
	}
	if past {
		rl := &readLog{command: "live", table: t.def.Name, rules: []string{"/access/read"}, rows: 1, hasRows: true, past: true}
		if err := s.logRead(ctx, o, rl); err != nil {
			return formula.Value{}, err
		}
	}
	return formula.ValueOfType(v, q.Type, c), nil
}
