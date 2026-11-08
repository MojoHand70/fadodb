package formula

import (
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
	vm     *VM
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

// Lookup implements QueryExecutor.
func (m *MemoryExecutor) Lookup(q *QuerySlot, args []Value, ctx *Context) (Value, *EvalError) {
	t, ok := m.tables[strings.ToUpper(q.Source)]
	if !ok {
		return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown source " + q.Source}
	}
	ci, ok := t.cols[strings.ToUpper(q.Column)]
	if !ok {
		return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown column " + q.Column + " of " + q.Source}
	}
	// Each slot of the condition is a column of the row or one of the
	// arguments.
	from := make([]int, len(q.Cond.Slots))
	for i, s := range q.Cond.Slots {
		if j := q.ArgIndex(s.Name); j >= 0 {
			from[i] = -1 - j
			continue
		}
		cj, ok := t.cols[strings.ToUpper(s.Name)]
		if !ok {
			return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown column " + s.Name + " of " + q.Source}
		}
		from[i] = cj
	}
	vars := make([]Value, len(from))
	for _, row := range t.rows {
		for i, f := range from {
			if f < 0 {
				vars[i] = args[-1-f]
			} else {
				vars[i] = row[f]
			}
		}
		v, err := m.vm.Run(q.Cond, vars, ctx)
		if err != nil || !v.Truth() {
			continue
		}
		return row[ci], nil
	}
	return Blank(), nil
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
