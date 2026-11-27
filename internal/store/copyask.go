package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	evalCtx := s.contextOf(o.principal, s.clock(o))
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
	evalCtx := s.contextOf(o.principal, s.clock(o))
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

// copyStoreFirst is how long the store is given to find a page, in a
// program that keeps the copy, before the copy is asked for it. A page
// the store can hand over at once, the invoices of one order or the
// first fifty that meet a condition most documents meet, is found
// there in less; a page it would read much of the class for is not,
// and is then found in the copy in milliseconds. With none, zero, the
// copy is asked at once.
var copyStoreFirst = 5 * time.Millisecond

// pageWithCopy answers a page of a list in a program that keeps the
// copy (ADR-0089, item 6). A page the store cannot hand over without
// reading and sorting every document it takes, one whose condition no
// index serves and whose order no index holds, is found in the copy at
// once. Any other page is the store's for a moment, copyStoreFirst,
// and the copy's when the moment has passed. Whichever finds the page,
// the answer is the same, and its documents are read from the store.
func (s *Store) pageWithCopy(ctx context.Context, c *class, o options, plan *query.Plan, f Filter, on *query.On, command string, probe bool) ([]*Document, bool, error) {
	sorts := false
	if !plan.Served && len(f.Order) > 0 {
		by := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(f.Order[0]), "-+"))
		sorts = !query.Indexed(c.cls, by)
	}
	if !sorts && copyStoreFirst != 0 {
		moment, done := context.WithTimeout(ctx, copyStoreFirst)
		found, more, err := s.find(moment, c, o, plan, command, probe)
		passed := err != nil && moment.Err() != nil && ctx.Err() == nil
		done()
		if !passed {
			if err != nil {
				return nil, false, err
			}
			return s.shown(ctx, c, o, plan, command, found, more)
		}
	}
	if page := s.pageFromCopy(ctx, c, o, plan, f, on, probe); page != nil {
		return s.shown(ctx, c, o, plan, command, page.found, page.more)
	}
	// The copy did not answer: the store does, for as long as it takes.
	return s.answer(ctx, c, o, plan, command, probe)
}

// copyPage is a page of a list that the copy found: its documents, read
// from the store in the order the copy gave, and whether documents
// follow the last.
type copyPage struct {
	found []*docRow
	more  bool
}

// pageFromCopy finds a page of a list in the copy (ADR-0089, item 6):
// which documents the question takes, in its order, for the caller.
// That is the part that reads the class when no index serves the
// question, and the copy is asked for the ids alone. The documents
// themselves are then read from the store by their ids, as many as the
// page has, and still under the question's own condition, so that a
// document changed since the copy looked is never shown where it no
// longer belongs. It answers nil when the store has to answer the whole
// question: CopyState keeps why.
func (s *Store) pageFromCopy(ctx context.Context, c *class, o options, plan *query.Plan, f Filter, on *query.On, probe bool) *copyPage {
	d := formula.NewDuckDB().WithLimits(s.limits)
	asked, err := query.CompileFor(d, f, on, c.cls, s.sources(), caller(o, c))
	if err != nil {
		// The store says what is wrong with the question.
		return nil
	}
	limit := plan.Limit
	if probe {
		limit++
	}
	text := `SELECT "id" FROM ` + quote(c.cls.Name)
	if asked.Where != "" {
		text += " WHERE " + asked.Where
	}
	text += " ORDER BY " + asked.Order + " LIMIT " + fmt.Sprint(limit) + " OFFSET " + fmt.Sprint(plan.Offset)
	evalCtx := s.contextOf(o.principal, s.clock(o))
	fd, ready := s.copyReady(ctx)
	if !ready {
		return nil
	}
	_, rows, err := fd.copy.Ask(ctx, text, append(asked.Args[:len(asked.Args):len(asked.Args)], d.ContextArgs(&evalCtx)...)...)
	if err != nil {
		fd.notAnswered(err)
		return nil
	}
	page := &copyPage{}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		var id string
		switch x := row[0].(type) {
		case string:
			id = x
		case []byte:
			id = string(x)
		default:
			fd.notAnswered(fmt.Errorf("the copy answered an id with %T", row[0]))
			return nil
		}
		ids = append(ids, id)
	}
	if probe && len(ids) > plan.Limit {
		ids, page.more = ids[:plan.Limit], true
	}
	if len(ids) == 0 {
		fd.answered()
		return page
	}
	// The documents, from the store.
	listed, err := json.Marshal(ids)
	if err != nil {
		fd.notAnswered(err)
		return nil
	}
	cols := append(quoteAll(systemColumns), quoteAll(columnNames(c.cls.Columns))...)
	cols = append(cols, quoteAll(c.atNames())...)
	// The ids lead: each is looked up by the key of the class, and the
	// question's condition is asked of the document found. The join is
	// written so that the store keeps to that order, whatever index the
	// condition could be answered through: a list an index narrows to a
	// million documents is not read again to hand over fifty.
	read := "SELECT " + strings.Join(cols, ", ") + ` FROM (SELECT "value" AS "fado_page_id" FROM json_each(:page_ids)) AS p CROSS JOIN ` +
		quote(c.cls.Name) + ` ON ` + quote(c.cls.Name) + `."id" = p."fado_page_id"`
	if plan.Where != "" {
		read += " WHERE " + plan.Where
	}
	args := append(plan.Args[:len(plan.Args):len(plan.Args)], s.contextArgs(o)...)
	args = append(args, sql.Named("page_ids", string(listed)))
	st, err := s.filters.acquire(ctx, s.db, read)
	if err != nil {
		fd.notAnswered(err)
		return nil
	}
	defer s.filters.release(st)
	docs, err := st.stmt.QueryContext(ctx, args...)
	if err != nil {
		fd.notAnswered(err)
		return nil
	}
	defer docs.Close()
	for docs.Next() {
		row, err := c.scanRow(docs, false)
		if err != nil {
			fd.notAnswered(err)
			return nil
		}
		page.found = append(page.found, row)
	}
	if err := docs.Err(); err != nil {
		fd.notAnswered(err)
		return nil
	}
	// In the order the copy gave.
	place := make(map[string]int, len(ids))
	for i, id := range ids {
		place[id] = i
	}
	sort.Slice(page.found, func(i, j int) bool { return place[page.found[i].id] < place[page.found[j].id] })
	fd.answered()
	return page
}
