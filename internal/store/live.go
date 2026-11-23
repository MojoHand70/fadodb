package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
)

// LiveValue is what a live variable is for one reader at one moment
// (ADR-0076): a single value in the wire shape of its type, or, for a
// variable whose formula is a QUERY, a list.
type LiveValue struct {
	Value any
	// List says the variable is a list: Columns, Rows and More then hold
	// the answer, and Value nothing.
	List bool
	// Columns are the columns of the list in the order its formula
	// writes them, each with the type and the label its source gives it,
	// so that a list with no rows still has its headings.
	Columns []LiveColumn
	// Rows are the documents the reader may read, in the order asked
	// for, at most the limit, and More says that rows follow the last.
	Rows []LiveRow
	More bool
}

// LiveColumn is one column of a live list.
type LiveColumn struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
}

// LiveRow is one row of a live list: a document of the source, by its
// id, with the columns of the list that the reader may read in it
// (ADR-0076, items 2 and 8). A column the reader may not read is absent.
type LiveRow struct {
	ID     string
	Values Values
	// columns are the columns in the order of the list, for the wire.
	columns []string
}

// MarshalJSON writes the row as one object: the id, then the columns in
// the order of the list.
func (r LiveRow) MarshalJSON() ([]byte, error) {
	out := []byte(`{"id":`)
	var err error
	if out, err = appendPlain(out, r.ID); err != nil {
		return nil, err
	}
	for _, name := range r.columns {
		v, ok := r.Values[name]
		if !ok {
			continue
		}
		out = append(out, ',')
		if out, err = appendPlain(out, name); err != nil {
			return nil, err
		}
		out = append(out, ':')
		if out, err = appendPlain(out, v); err != nil {
			return nil, err
		}
	}
	return append(out, '}'), nil
}

// MarshalJSON writes what the command live answers (ADR-0076, item 7):
// the value of a single value, and of a list its columns, its rows and
// whether rows follow.
func (v LiveValue) MarshalJSON() ([]byte, error) {
	if !v.List {
		return appendPlain(nil, struct {
			Value any `json:"value"`
		}{v.Value})
	}
	rows := v.Rows
	if rows == nil {
		rows = []LiveRow{}
	}
	return appendPlain(nil, struct {
		Columns []LiveColumn `json:"columns"`
		Rows    []LiveRow    `json:"rows"`
		More    bool         `json:"more"`
	}{v.Columns, rows, v.More})
}

// appendPlain appends a value as JSON, with the characters of a text as
// they are and no line end.
func appendPlain(out []byte, v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return append(out, bytes.TrimRight(buf.Bytes(), "\n")...), nil
}

// LiveRead names a live variable of a document and, when the variable
// is a list, the page of it to read (ADR-0076, item 7): the formula
// says which documents, and the reader which of them, in what order.
type LiveRead struct {
	Class    string `json:"class"`
	ID       string `json:"id"`
	Variable string `json:"variable"`
	// Order lists the columns to sort by, a leading "-" for descending;
	// empty is the order the formula gives.
	Order []string `json:"order,omitempty"`
	// Limit is the most rows to answer, 1,000 when 0 and 10,000 at most,
	// and Offset how many to skip.
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
	// Fields names the columns to answer; empty is every column.
	Fields []string `json:"fields,omitempty"`
}

// Live works a live variable of a document out for its caller, and
// keeps nothing (ADR-0076, items 1, 3 and 7). A live variable says
// "mode": "live" in its class: it has no column, no history and no
// event, so no other call answers it and no commit recalculates it. It
// is read by whoever may read the document, and the variable by its
// read right in the document's state; a content administrator reads
// past either only with a reason, which the log of reads keeps
// (ADR-0067). The formula runs over the stored document on the writer's
// VM, which reads what the document's life says of its states, so the
// read takes the writer's turn; it is short. A list is read as LivePage
// reads its first page.
func (s *Store) Live(ctx context.Context, class, id, variable string, opts ...Option) (*LiveValue, error) {
	return s.LivePage(ctx, LiveRead{Class: class, ID: id, Variable: variable}, opts...)
}

// LivePage is Live with the page of a list chosen (ADR-0076, item 7). A
// live variable whose formula is a QUERY is a list of the documents of
// another class. It is one question to that class's documents, the one
// the command query answers, with the values of this document in its
// condition: so the reader gets the documents the source's read rule
// gives them, and in each the columns its rights let them see
// (ADR-0076, item 2), and the order, the limit and the offset go into
// the one statement. The document itself is read on the writer's turn,
// as for a single value; the list is read beside the writer.
func (s *Store) LivePage(ctx context.Context, r LiveRead, opts ...Option) (*LiveValue, error) {
	id, variable := r.ID, r.Variable
	c, e := s.class(r.Class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	cv, ok := c.exact[variable]
	if !ok {
		cv, ok = c.byName[strings.ToUpper(variable)]
	}
	if !ok {
		return nil, errf(CodeUnknownVariable, variable, "the class %s has no variable %s", c.cls.Name, variable)
	}
	if !cv.v.Live() {
		return nil, hintf(CodeNotAllowed, cv.v.Name, cv.v.Name+" is no live variable: its value is kept with the document",
			`read the document with get; a live variable says "mode": "live" in its class`)
	}
	q := cv.v.Query
	var src *class
	if q == nil {
		if len(r.Order) > 0 || r.Limit != 0 || r.Offset != 0 || len(r.Fields) > 0 {
			return nil, hintf(CodeNotAllowed, cv.v.Name, cv.v.Name+" is one value and no list, so it has no order, limit, offset or fields", "leave them out")
		}
	} else {
		if src, e = s.class(q.Source); e != nil {
			return nil, e
		}
		// Whoever acts for somebody needs the substitution for the class
		// whose documents they read, too (ADR-0053).
		if _, e = s.asked(ctx, opts, src.cls.Name); e != nil {
			return nil, e
		}
	}
	val, on, err := s.liveOn(ctx, c, cv, id, o)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return &LiveValue{Value: val}, nil
	}
	return s.liveList(ctx, src, cv.v, on, r, o)
}

// liveOn reads the document a live variable is asked of, for the caller,
// on the writer's turn. It answers the value of a variable that is one
// value, and for a list what its condition reads of the document.
func (s *Store) liveOn(ctx context.Context, c *class, cv *cvar, id string, o options) (any, *query.On, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, errf(CodeClosed, "", "the store is closed")
	}
	row, err := c.scanW(c.w.selectCurrent.QueryRowContext(ctx, id), false)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, noDocument(c, id)
		}
		return nil, nil, storageErr("read the document", err)
	}
	ctxOf := contextOf(o.principal, s.clock(o))
	evalCtx := &ctxOf
	evalCtx.State, evalCtx.ID = row.state, row.id
	// A document the caller may not see does not exist for them
	// (ADR-0049, item 3).
	var r *readLog
	past := func(what, path string) *Error {
		if !o.principal.content() || c.brought {
			return nil
		}
		if o.reason == "" {
			return reasonToRead(o.principal, what, path)
		}
		if r == nil {
			r = newRead("live", c)
		}
		r.saw(id, path)
		return nil
	}
	if ok, _ := s.wev.may(c, ruleRead, row, evalCtx); !ok && !(c.brought && o.principal.content()) {
		if !o.principal.content() {
			return nil, nil, noDocument(c, id)
		}
		if e := past("the document "+id+" of "+c.cls.Name, "/access/read"); e != nil {
			return nil, nil, e
		}
	}
	// The read right of the variable, in the document's state (ADR-0041).
	if c.rights != nil {
		if rule := c.cells(row.state)[cv.idx].read; rule != nil {
			if held, rerr := s.wev.allowed(c, rule, row, evalCtx); !held {
				if !o.principal.content() || c.brought {
					msg := fmt.Sprintf("%s may not read %s", who(o.principal), cv.v.Name)
					if row.state != "" {
						msg += " while the document is in " + row.state
					}
					if rerr != nil {
						msg += ": the read right failed, " + guardFailure(rerr)
					}
					return nil, nil, hintf(CodeForbidden, cv.v.Name, msg, "the read right of "+cv.v.Name+", from the cell "+rule.Cell()+": "+ruleText(rule))
				}
				if e := past(cv.v.Name+" of the document "+id+" of "+c.cls.Name, rightRead(row.state, cv.v.Name)); e != nil {
					return nil, nil, e
				}
			}
		}
	}
	if q := cv.v.Query; q != nil {
		// What the condition reads of the document: the pointer to it, and
		// the variables it names as THIS.name, taken while the row is the
		// writer's to read.
		scope, err := c.cls.Scope()
		if err != nil {
			return nil, nil, storageErr("read the variables of "+c.cls.Name, err)
		}
		held := make(map[string]formula.Value, len(q.This))
		for _, name := range q.This {
			if hv, ok := c.byName[strings.ToUpper(name)]; ok && hv.leaf >= 0 {
				held[strings.ToUpper(name)] = row.vals[hv.leaf]
			}
		}
		on := &query.On{Cond: q.Cond, Src: q.Src, Scope: scope, Ref: c.cls.Name + ":" + row.id,
			Value: func(name string) (formula.Value, bool) {
				v, ok := held[strings.ToUpper(name)]
				return v, ok
			}}
		if err := s.logReadHeld(ctx, o, r); err != nil {
			return nil, nil, err
		}
		return nil, on, nil
	}
	val, err := s.wev.run(c, cv.v.Formula, row, evalCtx)
	if err != nil {
		return nil, nil, evalErr(cv.v.Name, err)
	}
	if err := s.logReadHeld(ctx, o, r); err != nil {
		return nil, nil, err
	}
	return valueOut(val), nil, nil
}

// liveList asks the question a live list is, on the document read, and
// answers the page of it the caller asked for.
func (s *Store) liveList(ctx context.Context, src *class, v *model.Variable, on *query.On, r LiveRead, o options) (*LiveValue, error) {
	q := v.Query
	f := Filter{Class: src.cls.Name, Order: r.Order, Limit: r.Limit, Offset: r.Offset}
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
	// Each by the name the source gives it, whatever the case the formula
	// or the caller wrote it in.
	out := &LiveValue{List: true, Columns: make([]LiveColumn, 0, len(asked))}
	cols := make([]string, 0, len(asked))
	for _, name := range asked {
		if sc, ok := model.FilterColumn(name); ok {
			cols = append(cols, sc.Name)
			out.Columns = append(out.Columns, LiveColumn{Name: sc.Name, Type: sc.Type.String()})
			continue
		}
		sv, ok := src.cls.Variable(name)
		if !ok {
			// The source was defined anew since the list was, without it.
			return nil, errf(CodeUnknownVariable, name, "the list %s names the column %s, which the class %s no longer has", v.Name, name, src.cls.Name)
		}
		cols = append(cols, sv.Name)
		f.Fields = append(f.Fields, sv.Name)
		out.Columns = append(out.Columns, LiveColumn{Name: sv.Name, Type: sv.Type.String(), Label: sv.Def.Label})
	}
	plan, err := query.CompileOn(f, on, src.cls, s.sources(), caller(o, src))
	if err != nil {
		return nil, filterErr(err)
	}
	if plan.Fields == nil {
		plan.Fields = []string{} // system columns alone: no variable is read
	}
	docs, more, err := s.answer(ctx, src, o, plan, "live", true)
	if err != nil {
		return nil, err
	}
	out.More, out.Rows = more, make([]LiveRow, len(docs))
	for i, d := range docs {
		row := LiveRow{ID: d.ID, Values: make(Values, len(cols)), columns: cols}
		for _, name := range cols {
			switch name {
			case "id":
				// Every row says it (ADR-0076, item 8).
			case "title":
				row.Values[name] = d.Title
			case "state":
				row.Values[name] = d.State
			case "version":
				row.Values[name] = d.Version
			case "class_version":
				row.Values[name] = d.ClassVersion
			case "created_at":
				row.Values[name] = d.CreatedAt
			case "updated_at":
				row.Values[name] = d.UpdatedAt
			case "state_entered_at":
				row.Values[name] = d.StateEnteredAt
			default:
				if val, ok := d.Values[name]; ok {
					row.Values[name] = val
				}
			}
		}
		out.Rows[i] = row
	}
	return out, nil
}
