package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
		v, err = x.s.liveLookup(x.ctx, q, args, x.o, fctx)
	} else if on := x.held.on[strings.ToUpper(q.List)]; on != nil {
		v, err = x.s.figure(x.ctx, q, args, on, x.o, fctx)
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

// liveArgs are the context parameters of a statement asked for a live
// value: the reader and the clock, and, from the context the formula
// runs under, the state and the id of the document the value is read
// on. STATE() and ID() in the condition of a LOOKUP or of a figure are
// that document's, as they are for a value that is kept (ADR-0040), and
// REF() is the pointer to it (ADR-0076, item 10).
func (s *Store) liveArgs(o options, of *formula.Context) []any {
	if of == nil {
		return s.contextArgs(o)
	}
	return formula.NewSQLite().ContextArgs(of)
}

// figure asks one figure: the aggregate of the slot over the rows its
// list has for the caller, on the document the list was read on. The
// rows are those of the list's own condition, of the read rule of the
// listed class and of the read rights of what the figure reads, which
// the filter compiler writes as it does for the list (ADR-0041,
// ADR-0049); the aggregate is the one a LOOKUP has (ADR-0046), over
// those rows. A column the caller may read in no state gives a blank.
func (s *Store) figure(ctx context.Context, q *formula.QuerySlot, args []formula.Value, on *query.On, o options, of *formula.Context) (formula.Value, error) {
	src, e := s.class(q.Source)
	if e != nil {
		return formula.Value{}, e
	}
	// A LOOKUP of a live formula has no list: its rows are every
	// document the reader may read, and its own condition picks among
	// them. A figure over a list takes no function of the document in
	// its condition (ADR-0081), so its statement is handed the reader
	// and the clock alone.
	var read query.On
	if on != nil {
		read, of = *on, nil
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
	plan, err := query.CompileOn(Filter{Class: src.cls.Name}, &read, src.cls, s.sources(), caller(o, src))
	if err != nil {
		var qe *query.Error
		if errors.As(err, &qe) && qe.Code == query.CodeForbidden && qe.Variable != "" {
			return formula.Blank(), nil
		}
		return formula.Value{}, filterErr(err)
	}
	where, given := plan.Where, plan.Args
	// A figure that an index narrows to the documents it names, the
	// invoices of one order, is the store's. One that no index narrows
	// reads the class, and is the copy's to answer when the program
	// keeps one (ADR-0089, item 6). The statement is the same in both,
	// and so is its one row: the value, its currency and its error.
	var v, c any
	var code sql.NullString
	// tried says the copy was asked already and did not answer.
	answered, tried := false, false
	if s.feeder != nil && !plan.Narrowed && !(q.Key != nil && query.Indexed(src.cls, q.IndexColumn)) {
		v, c, code, answered = s.figureFromCopy(ctx, src, q, args, &read, o, of)
		tried = true
	}
	if !answered {
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
		named := append(given[:len(given):len(given)], s.liveArgs(o, of)...)
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
		// In a program that keeps the copy, a figure that an index
		// narrows is the store's for a moment, and the copy's when the
		// moment has passed: the invoices of one order are a few, the
		// invoices in one state may be millions. When the copy does not
		// answer, the store does, for as long as it takes.
		asked, done := ctx, context.CancelFunc(nil)
		if s.feeder != nil && !tried {
			// With no moment at all, zero, the copy is asked at once:
			// a moment that lies behind has passed before the store
			// begins.
			moment := copyStoreFirst
			if moment == 0 {
				moment = -time.Second
			}
			asked, done = context.WithTimeout(ctx, moment)
		}
		err = st.stmt.QueryRowContext(asked, named...).Scan(&v, &c, &code)
		if done != nil {
			passed := err != nil && asked.Err() != nil && ctx.Err() == nil
			done()
			if passed {
				if v, c, code, answered = s.figureFromCopy(ctx, src, q, args, &read, o, of); answered {
					err = nil
				} else {
					err = st.stmt.QueryRowContext(ctx, named...).Scan(&v, &c, &code)
				}
			}
		}
		s.filters.release(st)
		if err != nil {
			return formula.Value{}, storageErr("look in "+src.cls.Name, err)
		}
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
		rows := func(p query.Principal) (string, []any, error) {
			plan, err := query.CompileOn(Filter{Class: src.cls.Name}, &read, src.cls, s.sources(), p)
			if err != nil {
				return "", nil, err
			}
			return plan.Where, plan.Args, nil
		}
		err := s.logCounted(ctx, src, o, where, append(given[:len(given):len(given)], s.liveArgs(o, of)...), func(p query.Principal) (string, []any, error) {
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
func (s *Store) liveLookup(ctx context.Context, q *formula.QuerySlot, args []formula.Value, o options, of *formula.Context) (formula.Value, error) {
	if _, e := s.class(q.Source); e == nil {
		return s.figure(ctx, q, args, nil, o, of)
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
	// A lookup keyed by a column that leads the key of the table or an
	// index of it is first narrowed to the rows of that key, as the
	// lookup of a commit is (ADR-0036): the store then finds the row by
	// the index, whatever the size of the table. The condition itself
	// still decides.
	keyArg, keyed := s.liveKey(t, q, args, o, of)
	table := func(string) string { return quote(t.name) }
	if keyed {
		table = func(string) string {
			return "(SELECT * FROM " + quote(t.name) + " WHERE " + quote(q.IndexColumn) + " = " + d.Param("fak") + ")"
		}
	}
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
	named := s.liveArgs(o, of)
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
	if keyed {
		named = append(named, sql.Named("fak", keyArg))
	}
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return formula.Value{}, storageErr("prepare a lookup in "+t.def.Name, err)
	}
	defer s.filters.release(st)
	var v, c any
	var code sql.NullString
	// In a program that keeps the copy (ADR-0094, item 6), a lookup
	// that reads a large table whole, a sum over it or a row that no
	// index finds, is the copy's to answer. Any other is the store's
	// for a moment, and the copy's when the moment has passed: a row
	// found by its key is there in less. When the copy does not answer,
	// the store does, for as long as it takes.
	// tried says the copy was asked already and did not answer.
	answered, tried := false, false
	if s.feeder != nil && !t.system && !keyed && s.largeInCopy(t) {
		v, c, code, answered = s.tableFigureFromCopy(ctx, t, q, args, o, of)
		tried = true
	}
	if !answered {
		asked, done := ctx, context.CancelFunc(nil)
		if s.feeder != nil && !t.system && !tried {
			// With no moment at all, zero, the copy is asked at once.
			moment := copyStoreFirst
			if moment == 0 {
				moment = -time.Second
			}
			asked, done = context.WithTimeout(ctx, moment)
		}
		err = st.stmt.QueryRowContext(asked, named...).Scan(&v, &c, &code)
		if done != nil {
			passed := err != nil && asked.Err() != nil && ctx.Err() == nil
			done()
			if passed {
				if v, c, code, answered = s.tableFigureFromCopy(ctx, t, q, args, o, of); answered {
					err = nil
				} else {
					err = st.stmt.QueryRowContext(ctx, named...).Scan(&v, &c, &code)
				}
			}
		}
		if err != nil {
			return formula.Value{}, storageErr("look in "+t.def.Name, err)
		}
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

// liveVMs are the machines that work out the key of a live lookup: a
// live value is read beside the writer, whose own machines it may not
// use.
var liveVMs = sync.Pool{New: func() any { return formula.NewVM() }}

// liveKey works out the key of a LOOKUP of a live formula over a table,
// and reports whether the lookup can be narrowed to the rows of that
// key: the key column leads the key of the table or an index of it, and
// the key is neither blank nor the zero of its kind, which a blank row
// would equal too. It answers the value as the store compares it with
// the column.
func (s *Store) liveKey(t *table, q *formula.QuerySlot, args []formula.Value, o options, of *formula.Context) (any, bool) {
	if q.Key == nil || !t.leads(q.IndexColumn) {
		return nil, false
	}
	li, ok := t.leafIndex[strings.ToUpper(q.IndexColumn)]
	if !ok {
		return nil, false
	}
	vars := make([]formula.Value, len(q.Key.Slots))
	for i, sl := range q.Key.Slots {
		a := q.ArgIndex(sl.Name)
		if a < 0 || a >= len(args) {
			return nil, false
		}
		vars[i] = args[a]
	}
	evalCtx := contextOf(o.principal, s.clock(o))
	if of != nil {
		evalCtx = *of
	}
	vm := liveVMs.Get().(*formula.VM)
	vm.Limits = s.limits
	key, err := vm.Run(q.Key, vars, &evalCtx)
	liveVMs.Put(vm)
	if err != nil || key.IsBlank() {
		return nil, false
	}
	switch typ := t.leaves[li].typ; typ.Kind {
	case formula.KindDecimal:
		k, err := formula.Rescale(key, int(typ.Scale))
		if err != nil || k.Mant() == 0 {
			return nil, false
		}
		return k.Mant(), true
	case formula.KindText, formula.KindRef, formula.KindJSON:
		return key.Str(), key.Str() != ""
	case formula.KindMoney:
		// A money compares by its amount and its currency: not by one
		// column.
		return nil, false
	default:
		return key.DriverValue(), key.Num() != 0
	}
}
