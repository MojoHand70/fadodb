package formula

import (
	"errors"
	"sort"
	"strings"
)

// QueryExecutor answers the query functions of a program at run time
// (ADR-0020). The VM hands it the compiled slot and the values of the
// document variables the condition reads; nothing crosses the boundary
// per row.
type QueryExecutor interface {
	// Lookup returns the slot's column of the first row of its source, in
	// the source's key order, on which the condition holds for the
	// arguments, or blank when no row does. A row on which the condition
	// stops with an error does not match. ctx is the context the formula
	// runs in, for a condition that reads it.
	Lookup(q *QuerySlot, args []Value, ctx *Context) (Value, *EvalError)
}

// MemoryExecutor is a QueryExecutor over rows held in memory: the
// in-memory executor of ADR-0020 that the conformance suite runs against,
// which tests and the formula tool use. It evaluates the condition on
// every row with a VM of its own, so it is exact and slow; the store has
// its index and its statements.
type MemoryExecutor struct {
	tables map[string]*memTable
	// lists are the lists the document of a suite holds (ADR-0081, step
	// 2), which a fold reads.
	lists map[string]*memTable
	vm    *VM
	fold  *RowFold
}

type memTable struct {
	src  *Source
	cols map[string]int
	rows [][]Value
}

// NewMemoryExecutor makes an executor with no tables.
func NewMemoryExecutor() *MemoryExecutor {
	return &MemoryExecutor{tables: map[string]*memTable{}, vm: NewVM()}
}

// AddTable adds or replaces a table: rows are row-major in the source's
// column order and are sorted into the source's key order.
func (m *MemoryExecutor) AddTable(src *Source, rows [][]Value) {
	t := &memTable{src: src, cols: make(map[string]int, len(src.Columns)), rows: rows}
	for i, f := range src.Columns {
		t.cols[strings.ToUpper(f.Name)] = i
	}
	var keys []int
	for _, k := range src.Key {
		if i, ok := t.cols[strings.ToUpper(k)]; ok {
			keys = append(keys, i)
		}
	}
	sort.SliceStable(t.rows, func(a, b int) bool {
		for _, k := range keys {
			if c := compareValues(t.rows[a][k], t.rows[b][k]); c != 0 {
				return c < 0
			}
		}
		return false
	})
	m.tables[strings.ToUpper(src.Name)] = t
}

// Lookup implements QueryExecutor: the column of the first matching row,
// or the slot's aggregate over the matching rows in key order.
func (m *MemoryExecutor) Lookup(q *QuerySlot, args []Value, ctx *Context) (Value, *EvalError) {
	t, ok := m.tables[strings.ToUpper(q.Source)]
	if !ok {
		return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown source " + q.Source}
	}
	return m.over(t, q, args, ctx)
}

// AddList adds or replaces a list the document holds (ADR-0081, step 2):
// rows are row-major in the source's column order, and stay in the order
// given, which is the order of the list.
func (m *MemoryExecutor) AddList(src *Source, rows [][]Value) {
	t := &memTable{src: src, cols: make(map[string]int, len(src.Columns)), rows: rows}
	for i, f := range src.Columns {
		t.cols[strings.ToUpper(f.Name)] = i
	}
	if m.lists == nil {
		m.lists = map[string]*memTable{}
	}
	m.lists[strings.ToUpper(src.Name)] = t
}

// Fold implements ListFolder over the lists added.
func (m *MemoryExecutor) Fold(q *QuerySlot, args []Value, ctx *Context) (Value, *EvalError) {
	t, ok := m.lists[strings.ToUpper(q.Source)]
	if !ok {
		return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "the document has no list " + q.Source}
	}
	return m.over(t, q, args, ctx)
}

// over answers a slot over the rows of one table or list.
func (m *MemoryExecutor) over(t *memTable, q *QuerySlot, args []Value, ctx *Context) (Value, *EvalError) {
	ci := -1
	if q.Agg == "" {
		var ok bool
		if ci, ok = t.cols[strings.ToUpper(q.Column)]; !ok {
			return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown column " + q.Column + " of " + q.Source}
		}
	}
	colOf := func(name string) (int, bool) {
		i, ok := t.cols[strings.ToUpper(name)]
		return i, ok
	}
	condFrom, perr := q.Places(q.Cond, colOf)
	if perr != nil {
		return Value{}, perr
	}
	var valFrom []int
	if q.Agg != "" {
		if valFrom, perr = q.Places(q.Value, colOf); perr != nil {
			return Value{}, perr
		}
	}
	if m.fold == nil {
		m.fold = &RowFold{vm: m.vm}
	}
	return m.fold.Run(q, t.rows, condFrom, valFrom, ci, args, ctx)
}

// ListFolder answers a function over a list the document holds
// (ADR-0081, step 2): the slot's aggregate over the elements of the
// variable the slot names as its source, in their order. The VM hands it
// the compiled slot and the values of the document's variables the
// slot's programs read.
type ListFolder interface {
	Fold(q *QuerySlot, args []Value, ctx *Context) (Value, *EvalError)
}

// Places says where each slot of a program of the query slot is read
// from: a column of the row, by its place in the row, or the i-th
// argument, as -1-i. colOf gives the place of a column by its name.
func (q *QuerySlot) Places(p *Program, colOf func(name string) (int, bool)) ([]int, *EvalError) {
	from := make([]int, len(p.Slots))
	for i, s := range p.Slots {
		if j := q.ArgIndex(s.Name); j >= 0 {
			from[i] = -1 - j
			continue
		}
		cj, ok := colOf(s.Name)
		if !ok {
			return nil, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown column " + s.Name + " of " + q.Source}
		}
		from[i] = cj
	}
	return from, nil
}

// RowFold answers a query slot over rows held in memory: it runs the
// slot's condition on every row with a VM of its own, and folds the
// slot's expression over the rows that match. It keeps its buffers, so
// one RowFold serves many runs, one at a time.
type RowFold struct {
	vm                  *VM
	vars, valVars, vals []Value
}

// NewRowFold makes a RowFold whose VM evaluates under the limits given.
func NewRowFold(limits Limits) *RowFold {
	vm := NewVM()
	vm.Limits = limits
	return &RowFold{vm: vm}
}

// Run answers the slot over rows, in the order given: the column at
// place ci of the first row on which the condition holds, or the slot's
// aggregate over those rows. condFrom and valFrom are the Places of the
// slot's condition and of its expression.
func (f *RowFold) Run(q *QuerySlot, rows [][]Value, condFrom, valFrom []int, ci int, args []Value, ctx *Context) (Value, *EvalError) {
	fill := func(vars []Value, from []int, row []Value) {
		for i, at := range from {
			if at < 0 {
				vars[i] = args[-1-at]
			} else {
				vars[i] = row[at]
			}
		}
	}
	if cap(f.vars) < len(condFrom) {
		f.vars = make([]Value, len(condFrom))
	}
	if cap(f.valVars) < len(valFrom) {
		f.valVars = make([]Value, len(valFrom))
	}
	vars, valVars, vals := f.vars[:len(condFrom)], f.valVars[:len(valFrom)], f.vals[:0]
	for _, row := range rows {
		fill(vars, condFrom, row)
		v, err := f.vm.Run(q.Cond, vars, ctx)
		if err != nil || !v.Truth() {
			continue
		}
		if q.Agg == "" {
			return row[ci], nil
		}
		// The expression of an aggregate failing on a matching row fails
		// the aggregate, with the error of the first such row.
		fill(valVars, valFrom, row)
		x, err := f.vm.Run(q.Value, valVars, ctx)
		if err != nil {
			var ee *EvalError
			if !errors.As(err, &ee) {
				ee = &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: err.Error()}
			}
			return Value{}, ee
		}
		vals = append(vals, x)
	}
	f.vals = vals
	if q.Agg == "" {
		return Blank(), nil
	}
	// the one argument that is the same for every row: the place of
	// INDEX, the separator of JOIN. It reads the THIS arguments alone.
	extra := Blank()
	if q.Extra != nil {
		from, perr := q.Places(q.Extra, func(string) (int, bool) { return 0, false })
		if perr != nil {
			return Value{}, perr
		}
		ex := make([]Value, len(from))
		fill(ex, from, nil)
		v, err := f.vm.Run(q.Extra, ex, ctx)
		if err != nil {
			var ee *EvalError
			if !errors.As(err, &ee) {
				ee = &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: err.Error()}
			}
			return Value{}, ee
		}
		extra = v
	}
	return q.AggregateWith(vals, extra, f.vm.maxText())
}

// compareValues orders two values of one kind as the comparison
// operators do, an exact value by its amount; a currency mismatch, which
// the operators report, orders by amount here.
func compareValues(a, b Value) int {
	if a.kind.IsExact() || b.kind.IsExact() {
		c, _ := compareExact(&a, &b, int(max(a.scale, b.scale)))
		return c
	}
	return compare(a, b)
}
