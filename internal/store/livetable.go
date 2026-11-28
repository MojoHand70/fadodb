package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
)

// A list over a table (ADR-0076, item 9): a QUERY whose source is an
// explicit table. Its rows are rows of the table: they are no documents,
// so they carry no id, and nothing is read of them by a rule of their
// own. Who may read them is the one read line of the table, which is
// asked of whoever reads a live list, as the command table_rows asks it;
// a list that is kept is asked with the engine's eyes, as a LOOKUP is.

// tableSelect is the statement that reads the rows a plan takes of a
// table, every stored column in the order scanRows reads them, limit
// rows from the offset of the plan.
func tableSelect(t *table, plan *query.Plan, limit int) string {
	text := "SELECT " + strings.Join(quoteAll(columnNames(t.def.Columns)), ", ") + " FROM " + quote(t.name)
	if plan.Where != "" {
		text += " WHERE " + plan.Where
	}
	if plan.Order != "" {
		text += " ORDER BY " + plan.Order
	}
	return text + " LIMIT " + fmt.Sprint(limit) + " OFFSET " + fmt.Sprint(plan.Offset)
}

// tableRows runs a statement of tableSelect and answers the rows, each
// in the order of the table's leaves.
func (t *table) tableRows(ctx context.Context, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, text string, args []any) ([][]formula.Value, error) {
	rows, err := db.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, storageErr("read "+t.def.Name, err)
	}
	defer rows.Close()
	n := len(t.def.Columns)
	raw := make([]any, n)
	dest := make([]any, n)
	for i := range raw {
		dest[i] = &raw[i]
	}
	var out [][]formula.Value
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, storageErr("read "+t.def.Name, err)
		}
		row := make([]formula.Value, len(t.leaves))
		rest := raw
		for i, l := range t.leaves {
			row[i], rest = decode(l, rest)
		}
		out = append(out, row)
	}
	return out, storageErr("read "+t.def.Name, rows.Err())
}

// liveTable asks the question a live list over a table is, on the
// document read, and answers the page of it the caller asked for. The
// read line of the table is asked of the caller: one it does not let
// through is refused, as by table_rows, and a content administrator
// passes it with a reason, which the log of reads keeps.
func (s *Store) liveTable(ctx context.Context, t *table, v *model.Variable, on *query.On, r LiveRead, o options) (*LiveValue, error) {
	q := v.Query
	past, refusal := s.tableRefusal(t, false, o, s.clock(o))
	if refusal != nil {
		return nil, refusal
	}
	f := query.TableFilter{Order: r.Order, Limit: r.Limit, Offset: r.Offset}
	if len(f.Order) == 0 {
		f.Order = q.Order
	}
	// The columns: the list's, or the ones of them the caller names.
	asked := q.Columns
	if len(r.Fields) > 0 {
		asked = make([]string, 0, len(r.Fields))
	fields:
		for i, name := range r.Fields {
			for _, have := range asked {
				if strings.EqualFold(have, name) {
					continue fields
				}
			}
			for _, col := range q.Columns {
				if strings.EqualFold(col, name) {
					asked = append(asked, col)
					continue fields
				}
			}
			return nil, errf(CodeUnknownVariable, fmt.Sprintf("fields/%d", i), "the list %s has no column %s; its columns are %s", v.Name, name, strings.Join(q.Columns, ", "))
		}
	}
	out := &LiveValue{List: true, Columns: make([]LiveColumn, 0, len(asked))}
	leaves := make([]int, 0, len(asked))
	cols := make([]string, 0, len(asked))
	for _, name := range asked {
		li, ok := t.leafIndex[strings.ToUpper(name)]
		if !ok {
			// The table was defined anew since the list was, without it.
			return nil, errf(CodeUnknownVariable, name, "the list %s names the column %s, which the table %s no longer has", v.Name, name, t.def.Name)
		}
		l := t.leaves[li]
		label := ""
		if t.def.Def != nil {
			label = t.def.Def.Columns[l.path].Label
		}
		out.Columns = append(out.Columns, LiveColumn{Name: l.path, Type: l.typ.String(), Label: label})
		leaves, cols = append(leaves, li), append(cols, l.path)
	}
	plan, err := query.CompileTable(f, on, t.src, s.sources())
	if err != nil {
		return nil, filterErr(err)
	}
	args := append(plan.Args, s.contextArgs(o)...)
	// In a program that keeps the copy, a page that reads a large table
	// to find its rows is found there (ADR-0094, item 6); its rows are
	// the store's all the same.
	var rows [][]formula.Value
	if text := tableSelect(t, plan, plan.Limit+1); s.feeder == nil || t.system {
		rows, err = t.tableRows(ctx, s.db, text, args)
	} else {
		rows, err = s.tablePageWithCopy(ctx, t, plan, f, on, o, text, args)
	}
	if err != nil {
		return nil, err
	}
	if len(rows) > plan.Limit {
		rows, out.More = rows[:plan.Limit], true
	}
	out.Rows = make([]LiveRow, len(rows))
	for i, row := range rows {
		lr := LiveRow{Values: make(Values, len(cols)), columns: cols}
		for j, li := range leaves {
			lr.Values[cols[j]] = valueOut(row[li])
		}
		out.Rows[i] = lr
	}
	if past {
		rl := &readLog{command: "live", table: t.def.Name, rules: []string{"/access/read"}, rows: int64(len(rows)), hasRows: true, past: true}
		if err := s.logRead(ctx, o, rl); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// keptTableRows asks the QUERY of a list that is kept, over a table, for
// the document the row is, on the writer, with the engine's eyes.
func (s *Store) keptTableRows(ctx context.Context, c *class, cv *cvar, t *table, row *docRow, evalCtx *formula.Context) ([]childRow, *Error) {
	q := cv.v.Kept
	fail := func(code, msg string) ([]childRow, *Error) {
		return nil, &Error{Code: code, Variable: cv.v.Name, Message: msg}
	}
	on, err := listOn(c, q, row)
	if err != nil {
		return fail(formula.ErrQueryFailed, err.Error())
	}
	plan, err := query.CompileTable(query.TableFilter{Order: q.Order, Limit: MaxElements}, on, t.src, s.sources())
	if err != nil {
		if fe, _ := filterErr(err).(*Error); fe != nil {
			return fail(fe.Code, fe.Message)
		}
		return fail(formula.ErrQueryFailed, err.Error())
	}
	args := append(plan.Args, formula.NewSQLite().ContextArgs(evalCtx)...)
	found, rerr := t.tableRows(ctx, s.writer, tableSelect(t, plan, MaxElements+1), args)
	if rerr != nil {
		return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+": "+rerr.Error())
	}
	if len(found) > MaxElements {
		return fail(CodeTooManyElements, fmt.Sprintf("the list %s would keep more than %d rows", cv.v.Name, MaxElements))
	}
	ch := cv.child
	from := make([]int, len(ch.leaves))
	for i, l := range ch.leaves {
		li, ok := t.leafIndex[strings.ToUpper(l.path)]
		if !ok {
			return fail(formula.ErrQueryFailed, "the list "+cv.v.Name+" keeps the column "+l.path+", which the table "+t.def.Name+" no longer has")
		}
		from[i] = li
	}
	out := make([]childRow, len(found))
	for i, r := range found {
		kept := make(childRow, len(from))
		for j, li := range from {
			kept[j] = r[li]
		}
		out[i] = kept
	}
	return out, nil
}
