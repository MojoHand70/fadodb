package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
)

// Totals by group (ADR-0090). A live variable whose QUERY has a total
// among its columns is a list of totals: one line for each group, and
// in it the group and its totals. It is one more question to the
// documents of the listed class, asked as a figure over a list is
// (ADR-0081): the rows are the ones the condition of the list, the read
// rule of the class and the read rights of what the list names leave to
// whoever reads it, and the engine's own aggregates count them, group
// by group, in one statement. No document is read to be counted
// (ADR-0020), and a line is no document: it has no id.

// totalsOf lays a list of totals out against its source as the source
// is now: the columns it groups by, and its totals bound as a LOOKUP
// binds them. at says for each column of the list where it stands among
// the groups, or among the totals.
type totalsOf struct {
	groups []formula.GroupColumn
	slots  []*formula.QuerySlot
	at     []formula.TotalsOrder
	out    []LiveColumn
	// reads are the variables of the source the list names: a document
	// counts only where its state lets the reader read each of them.
	reads []string
}

func layTotals(src *class, v *model.Variable) (*totalsOf, error) {
	q := v.Query
	lay := &totalsOf{at: make([]formula.TotalsOrder, len(q.Columns)), out: make([]LiveColumn, 0, len(q.Columns))}
	// gone is what a list says of a column its source no longer has: the
	// source was defined anew since the list was.
	gone := func(name string) error {
		return errf(CodeUnknownVariable, name, "the list %s names the column %s, which the class %s no longer has", v.Name, name, src.cls.Name)
	}
	var rows *formula.Source
	for i, name := range q.Columns {
		if t := q.Totals[i]; t != nil {
			if rows == nil {
				rows = src.cls.Source()
			}
			slot, err := formula.BindTotal(t.Agg, t.Column, rows)
			if err != nil {
				if _, ok := rows.Column(t.Column); !ok {
					return nil, gone(t.Column)
				}
				return nil, errf(CodeBadFormula, v.Name, "the list %s cannot work out %s(%s) over %s as it is defined now: %s", v.Name, t.Agg, t.Column, src.cls.Name, err.Error())
			}
			lay.at[i] = formula.TotalsOrder{Column: len(lay.slots), Total: true}
			lay.slots = append(lay.slots, slot)
			lay.out = append(lay.out, LiveColumn{Name: t.Name, Type: slot.Type.String()})
			lay.reads = append(lay.reads, t.Column)
			continue
		}
		lay.at[i] = formula.TotalsOrder{Column: len(lay.groups)}
		if sc, ok := model.FilterColumn(name); ok {
			lay.groups = append(lay.groups, formula.GroupColumn(sc))
			lay.out = append(lay.out, LiveColumn{Name: sc.Name, Type: sc.Type.String()})
			continue
		}
		sv, ok := src.cls.Variable(name)
		if !ok || sv.Live() || !sv.Type.IsScalar() {
			return nil, gone(name)
		}
		lay.groups = append(lay.groups, formula.GroupColumn{Name: sv.Name, Type: sv.Type})
		lay.out = append(lay.out, LiveColumn{Name: sv.Name, Type: sv.Type.String(), Label: sv.Def.Label})
		lay.reads = append(lay.reads, sv.Name)
	}
	return lay, nil
}

// sorted reads what a list of totals is sorted by, as a command or the
// formula writes it: columns of the list, a leading "-" for descending.
func (lay *totalsOf) sorted(v *model.Variable, order []string) ([]formula.TotalsOrder, error) {
	terms := make([]formula.TotalsOrder, 0, len(order))
	seen := map[int]bool{}
	for i, o := range order {
		name, desc := strings.TrimSpace(o), false
		if strings.HasPrefix(name, "-") {
			desc, name = true, strings.TrimSpace(name[1:])
		} else if strings.HasPrefix(name, "+") {
			name = strings.TrimSpace(name[1:])
		}
		at := -1
		for j, col := range v.Query.Columns {
			if strings.EqualFold(col, name) {
				at = j
			}
		}
		if at < 0 {
			return nil, errf(CodeUnknownVariable, fmt.Sprintf("order/%d", i), "the list %s has no column %s to be sorted by; its columns are %s", v.Name, name, strings.Join(v.Query.Columns, ", "))
		}
		if seen[at] {
			return nil, errf(CodeBadValue, fmt.Sprintf("order/%d", i), "%s is listed twice", name)
		}
		seen[at] = true
		term := lay.at[at]
		term.Desc = desc
		terms = append(terms, term)
	}
	return terms, nil
}

// liveTotals asks the question a list of totals is, on the document
// read, and answers the page of its lines the caller asked for.
func (s *Store) liveTotals(ctx context.Context, src *class, v *model.Variable, on *query.On, r LiveRead, o options) (*LiveValue, error) {
	q := v.Query
	if len(r.Fields) > 0 {
		return nil, hintf(CodeNotAllowed, "fields", v.Name+" is a list of totals: its lines are grouped by the columns its formula names, so a reader cannot take some of them",
			"leave fields out; the columns are "+strings.Join(q.Columns, ", "))
	}
	lay, err := layTotals(src, v)
	if err != nil {
		return nil, err
	}
	by := r.Order
	if len(by) == 0 {
		by = q.Order
	}
	order, err := lay.sorted(v, by)
	if err != nil {
		return nil, err
	}
	// The rows that are counted: the list's own condition, the read rule
	// of the class and the read rights of what the list names, written
	// by the filter compiler as for a list of documents (ADR-0041,
	// ADR-0049).
	read := *on
	read.Reads = append(read.Reads[:len(read.Reads):len(read.Reads)], lay.reads...)
	rows := func(p query.Principal) (*query.Plan, error) {
		return query.CompileOn(Filter{Class: src.cls.Name, Limit: r.Limit, Offset: r.Offset}, &read, src.cls, s.sources(), p)
	}
	plan, err := rows(caller(o, src))
	if err != nil {
		return nil, filterErr(err)
	}
	// A row of the statement: each group, a money one with its currency
	// after it; then each total with its currency and its error.
	width := 3 * len(lay.slots)
	for _, g := range lay.groups {
		width++
		if g.Type.Kind == formula.KindMoney {
			width++
		}
	}
	given := append(plan.Args[:len(plan.Args):len(plan.Args)], s.contextArgs(o)...)
	// inStore asks the store for the lines, one past the page.
	inStore := func(ctx context.Context) ([][]any, error) {
		d := formula.NewSQLite().WithLimits(s.limits)
		table := quote(src.cls.Name)
		if plan.Where != "" {
			table = "(SELECT * FROM " + table + " WHERE " + plan.Where + ")"
		}
		text, err := formula.TotalsSQL(lay.groups, lay.slots, order, d, table, formula.NewParams(d))
		if err != nil {
			return nil, errf(CodeBadFormula, v.Name, "the list %s: %s", v.Name, err.Error())
		}
		named := append(given[:len(given):len(given)], sql.Named(formula.TotalsLimit, plan.Limit+1), sql.Named(formula.TotalsOffset, plan.Offset))
		st, err := s.filters.acquire(ctx, s.db, text)
		if err != nil {
			return nil, storageErr("prepare the totals of "+src.cls.Name, err)
		}
		defer s.filters.release(st)
		found, err := st.stmt.QueryContext(ctx, named...)
		if err != nil {
			return nil, storageErr("total "+src.cls.Name, err)
		}
		defer found.Close()
		var lines [][]any
		for found.Next() {
			cells, into := make([]any, width), make([]any, width)
			for i := range cells {
				into[i] = &cells[i]
			}
			if err := found.Scan(into...); err != nil {
				return nil, storageErr("total "+src.cls.Name, err)
			}
			lines = append(lines, cells)
		}
		if err := found.Err(); err != nil {
			return nil, storageErr("total "+src.cls.Name, err)
		}
		return lines, nil
	}
	// In a program that keeps the copy, the lines come as a count and a
	// total of a LOOKUP do (ADR-0089, item 6): from the copy at once
	// when no index narrows what the list counts; from the store for a
	// moment when one does, and from the copy when the moment has
	// passed. Otherwise, and when the copy does not answer, from the
	// store. The statement is the same in both.
	var lines [][]any
	have := false
	switch {
	case s.feeder == nil:
	case !plan.Narrowed, copyStoreFirst == 0:
		lines, have = s.totalsFromCopy(ctx, src, lay, order, &read, plan, o, width)
	default:
		moment, done := context.WithTimeout(ctx, copyStoreFirst)
		got, merr := inStore(moment)
		passed := merr != nil && moment.Err() != nil && ctx.Err() == nil
		done()
		switch {
		case merr == nil:
			lines, have = got, true
		case passed:
			lines, have = s.totalsFromCopy(ctx, src, lay, order, &read, plan, o, width)
		default:
			return nil, merr
		}
	}
	if !have {
		if lines, err = inStore(ctx); err != nil {
			return nil, err
		}
	}
	out := &LiveValue{List: true, Columns: lay.out}
	names := make([]string, len(lay.out))
	for i, col := range lay.out {
		names[i] = col.Name
	}
	if len(lines) > plan.Limit {
		lines, out.More = lines[:plan.Limit], true
	}
	for _, cells := range lines {
		line := LiveRow{Values: make(Values, len(names)), columns: names}
		next := 0
		groups := make([]any, len(lay.groups))
		for i, g := range lay.groups {
			var cur any
			val := cells[next]
			next++
			if g.Type.Kind == formula.KindMoney {
				cur = cells[next]
				next++
			}
			groups[i] = valueOut(formula.ValueOfType(val, g.Type, cur))
		}
		totals := make([]any, len(lay.slots))
		for i, slot := range lay.slots {
			val, cur, code := cells[next], cells[next+1], cells[next+2]
			next += 3
			if code != nil {
				// What a LOOKUP stops with, a sum of money in two
				// currencies, stops the list (ADR-0090, item 8).
				t := q.Totals[totalAt(lay, i)]
				return nil, evalErr(v.Name, &formula.EvalError{Code: fmt.Sprint(code), Pos: -1,
					Msg: fmt.Sprintf("%s(%s) over a group of %s: %v", t.Agg, t.Column, src.cls.Name, code)})
			}
			totals[i] = valueOut(formula.ValueOfType(val, slot.Type, cur))
		}
		for i, at := range lay.at {
			if at.Total {
				line.Values[names[i]] = totals[at.Column]
			} else {
				line.Values[names[i]] = groups[at.Column]
			}
		}
		out.Rows = append(out.Rows, line)
	}
	// A content administrator who passes the rules is written down with
	// how many of the documents only the override let them count
	// (ADR-0067, item 3), as for a figure.
	if o.records(src) {
		err := s.logCounted(ctx, src, o, plan.Where, given, func(p query.Principal) (string, []any, error) {
			plan, err := rows(p)
			var qe *query.Error
			if errors.As(err, &qe) && qe.Code == query.CodeForbidden && qe.Variable != "" {
				return "0", nil, nil // nobody else may read the column: every row was counted past the rules
			}
			if err != nil {
				return "", nil, filterErr(err)
			}
			return plan.Where, plan.Args, nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// totalAt finds the column of the list that the i-th total is.
func totalAt(lay *totalsOf, i int) int {
	for at, o := range lay.at {
		if o.Total && o.Column == i {
			return at
		}
	}
	return 0
}

// logCounted writes down a content administrator who passed the rules
// to count documents: how many of the ones counted only the override
// let them count (ADR-0067, item 3). where and args are the condition
// the count ran under and its values; rows gives both for another
// reader, the same administrator without the override.
func (s *Store) logCounted(ctx context.Context, src *class, o options, where string, args []any, rows func(query.Principal) (string, []any, error)) error {
	text := "SELECT count(*) FROM " + quote(src.cls.Name)
	if where != "" {
		text += " WHERE " + where
	}
	cst, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return storageErr("prepare the count of "+src.cls.Name, err)
	}
	var all int64
	err = cst.stmt.QueryRowContext(ctx, args...).Scan(&all)
	s.filters.release(cst)
	if err != nil {
		return storageErr("count "+src.cls.Name, err)
	}
	past, err := s.pastCount(ctx, src, o, all, rows)
	if err != nil {
		return err
	}
	r := newRead("live", src)
	r.counted, r.hasCounted, r.past = past, true, past > 0
	return s.logRead(ctx, o, r)
}

// totalsFromCopy asks the copy for the lines of a list of totals: the
// statement the store is asked, written for the analytical engine, over
// the documents the reader may read there (ADR-0089, items 6 and 7). It
// answers the rows as the store's statement gives them, width cells
// each, or false when the store has to answer: CopyState keeps why.
func (s *Store) totalsFromCopy(ctx context.Context, src *class, lay *totalsOf, order []formula.TotalsOrder, read *query.On, plan *query.Plan, o options, width int) ([][]any, bool) {
	d := formula.NewDuckDB().WithLimits(s.limits)
	asked, err := query.CompileFor(d, Filter{Class: src.cls.Name}, read, src.cls, s.sources(), caller(o, src))
	if err != nil {
		// The store says what is wrong with the question.
		return nil, false
	}
	table := quote(src.cls.Name)
	if asked.Where != "" {
		table = "(SELECT * FROM " + table + " WHERE " + asked.Where + ")"
	}
	text, err := formula.TotalsSQL(lay.groups, lay.slots, order, d, table, formula.NewParams(d))
	if err != nil {
		return nil, false
	}
	evalCtx := contextOf(o.principal, s.clock(o))
	args := append(asked.Args[:len(asked.Args):len(asked.Args)], d.ContextArgs(&evalCtx)...)
	args = append(args, sql.Named(formula.TotalsLimit, plan.Limit+1), sql.Named(formula.TotalsOffset, plan.Offset))
	fd, ready := s.copyReady(ctx)
	if !ready {
		return nil, false
	}
	_, rows, err := fd.copy.Ask(ctx, text, args...)
	if err != nil {
		fd.notAnswered(err)
		return nil, false
	}
	for _, row := range rows {
		if len(row) != width {
			fd.notAnswered(errors.New("the copy answered a list of totals with a row of another shape"))
			return nil, false
		}
		for i := range row {
			row[i] = copyCell(row[i])
		}
	}
	fd.answered()
	return rows, true
}
