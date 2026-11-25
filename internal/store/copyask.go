package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/query"
)

// The questions a class already asks, answered by the copy (ADR-0089,
// item 6). A count, or a total that a LOOKUP or a figure over a list
// works out, reads every document it counts. Where an index narrows it
// to the documents it names, the invoices of one order, the store
// answers in a moment, as ever. Where none does, the store reads the
// class, and the analytical engine is built for that: a program that
// keeps the copy asks the copy. Nothing is written differently for it,
// and the answer is the store's: the same statement, written by the
// same compiler in the copy's dialect, with the reader's rule and
// rights in it (item 7), over a copy brought up to the database first
// (item 5). When the copy does not answer, the store does.

// countFromCopy counts the documents of a class that a filter takes,
// for the caller, in the copy.
func (s *Store) countFromCopy(ctx context.Context, c *class, f Filter, o options) (int64, bool) {
	if s.feeder == nil {
		return 0, false
	}
	d := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(d, f, nil, c.cls, s.sources(), caller(o, c))
	if err != nil {
		// The store says what is wrong with the question.
		return 0, false
	}
	text := "SELECT count(*) FROM " + quote(c.cls.Name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	evalCtx := contextOf(o.principal, s.clock(o))
	fd, ready := s.copyReady(ctx)
	if !ready {
		return 0, false
	}
	_, rows, err := fd.copy.Ask(ctx, text, append(plan.Args[:len(plan.Args):len(plan.Args)], d.ContextArgs(&evalCtx)...)...)
	if err == nil && (len(rows) != 1 || len(rows[0]) != 1) {
		err = errors.New("the copy answered a count with a row of another shape")
	}
	if err != nil {
		fd.notAnswered(err)
		return 0, false
	}
	n, ok := copyCell(rows[0][0]).(int64)
	if !ok {
		fd.notAnswered(fmt.Errorf("the copy answered a count with %T", rows[0][0]))
		return 0, false
	}
	fd.answered()
	return n, true
}

// figureFromCopy asks the copy for what figure asks the store: the
// aggregate of a slot, or the column of its first row, over the
// documents the reader may read. It answers the value, the currency
// and the error code of the one row, as the store's statement does.
func (s *Store) figureFromCopy(ctx context.Context, src *class, q *formula.QuerySlot, args []formula.Value, read *query.On, o options, of *formula.Context) (v, c any, code sql.NullString, ok bool) {
	if s.feeder == nil {
		return nil, nil, code, false
	}
	d := formula.NewDuckDB().WithLimits(s.limits)
	plan, err := query.CompileFor(d, Filter{Class: src.cls.Name}, read, src.cls, s.sources(), caller(o, src))
	if err != nil {
		return nil, nil, code, false
	}
	params := formula.NewParams(d)
	arg := func(i int, currency bool) string {
		if currency {
			return d.Param(fmt.Sprintf("fa%dc", i))
		}
		return d.Param(fmt.Sprintf("fa%d", i))
	}
	table := func(string) string {
		if plan.Where == "" {
			return quote(src.cls.Name)
		}
		return "(SELECT * FROM " + quote(src.cls.Name) + " WHERE " + plan.Where + ")"
	}
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
		return nil, nil, code, false
	}
	// The values of the statement, every one by its name: the reader's
	// rule's, the caller and the clock, and what the condition reads of
	// the document as THIS.name, a money one with its currency.
	evalCtx := contextOf(o.principal, s.clock(o))
	if of != nil {
		evalCtx = *of
	}
	named := append(plan.Args[:len(plan.Args):len(plan.Args)], d.ContextArgs(&evalCtx)...)
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
	fd, ready := s.copyReady(ctx)
	if !ready {
		return nil, nil, code, false
	}
	_, rows, err := fd.copy.Ask(ctx, text, named...)
	if err == nil && (len(rows) != 1 || len(rows[0]) != 3) {
		err = errors.New("the copy answered a figure with a row of another shape")
	}
	if err != nil {
		fd.notAnswered(err)
		return nil, nil, code, false
	}
	if e := rows[0][2]; e != nil {
		code = sql.NullString{String: fmt.Sprint(e), Valid: true}
	}
	fd.answered()
	return copyCell(rows[0][0]), copyCell(rows[0][1]), code, true
}
