package formula

import (
	"database/sql"
	"fmt"
	"strings"
)

// SQLResult is what RunSQL got back from the engine.
type SQLResult struct {
	// Query is the statement that ran, for messages.
	Query string
	// Value is the value column as a Value of the program's result kind.
	Value Value
	// Code is the error column, or "" when NULL.
	Code string
}

// RunSQL executes one program through database/sql on a dialect: it
// generates the SQL, binds vars, one value per program slot in slot order,
// as named parameters over a one-row subselect, binds the context, and
// reads the value and the error columns. It is the shared half of every
// SQL conformance and property runner; comparing with the VM is the
// runner's job.
func RunSQL(db *sql.DB, d Dialect, prog *Program, vars []Value, ctx *Context) (SQLResult, error) {
	if len(vars) < len(prog.Slots) {
		return SQLResult{}, fmt.Errorf("program reads %d variables, got %d", len(prog.Slots), len(vars))
	}
	stmt, err := GenerateSQL(prog, d, func(s Slot) string { return quoteIdent(s.Name) })
	if err != nil {
		return SQLResult{}, err
	}
	cols := make([]string, len(prog.Slots))
	args := make([]any, 0, len(prog.Slots)+3)
	for i, s := range prog.Slots {
		name := fmt.Sprintf("v%d", i)
		cols[i] = d.Param(name) + " AS " + quoteIdent(s.Name)
		args = append(args, sql.Named(name, vars[i].DriverValue()))
	}
	from := "(SELECT 1)"
	if len(cols) > 0 {
		from = "(SELECT " + strings.Join(cols, ", ") + ")"
	}
	args = append(args, d.ContextArgs(ctx)...)
	q := "SELECT (" + stmt.Value + ") AS v, (" + stmt.ErrorOrNull() + ") AS e FROM " + from
	var v, e any
	if err := db.QueryRow(q, args...).Scan(&v, &e); err != nil {
		return SQLResult{Query: q}, fmt.Errorf("%s: %w", d.Name(), err)
	}
	res := SQLResult{Query: q, Value: ValueFromDriver(v, prog.Result.Kind)}
	if e != nil {
		res.Code = fmt.Sprint(e)
	}
	return res, nil
}

// RunSQLCase executes one evaluation case: the case inputs become the
// program's variables, an absent input is blank, and the case context is
// the context.
func RunSQLCase(db *sql.DB, d Dialect, prog *Program, c *Case) (SQLResult, error) {
	vars := make([]Value, len(prog.Slots))
	for i, s := range prog.Slots {
		raw, ok := c.Inputs[s.Name]
		if !ok {
			continue
		}
		v, err := ParseCaseValue(raw, s.Type)
		if err != nil {
			return SQLResult{}, fmt.Errorf("input %s: %w", s.Name, err)
		}
		vars[i] = v
	}
	ctx, err := c.Context.Context()
	if err != nil {
		return SQLResult{}, err
	}
	return RunSQL(db, d, prog, vars, ctx)
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// NearlyEqual compares two values exactly, except numbers, which may
// differ by the relative tolerance: the allowance of ADR-0019 for
// floating-point aggregates across targets.
func NearlyEqual(a, b Value, rel float64) bool {
	if a.kind != KindNumber || b.kind != KindNumber {
		return Equal(a, b)
	}
	diff := a.num - b.num
	if diff < 0 {
		diff = -diff
	}
	scale := b.num
	if scale < 0 {
		scale = -scale
	}
	if scale < 1 {
		scale = 1
	}
	return diff <= rel*scale
}
