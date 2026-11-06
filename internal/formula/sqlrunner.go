package formula

import (
	"database/sql"
	"fmt"
	"strings"
)

// SQLCaseResult is what RunSQLCase got back from the engine.
type SQLCaseResult struct {
	// Query is the statement that ran, for messages.
	Query string
	// Value is the value column as a Value of the program's result kind.
	Value Value
	// Code is the error column, or "" when NULL.
	Code string
}

// RunSQLCase executes one evaluation case through database/sql on a
// dialect: it generates the SQL for the program, binds the case inputs as
// named parameters over a one-row subselect, binds the context, and reads
// the value and the error columns. It is the shared half of every SQL
// conformance runner; comparing with the case is the runner's job.
func RunSQLCase(db *sql.DB, d Dialect, prog *Program, c *Case) (SQLCaseResult, error) {
	stmt, err := GenerateSQL(prog, d, func(s Slot) string { return quoteIdent(s.Name) })
	if err != nil {
		return SQLCaseResult{}, err
	}
	cols := make([]string, len(prog.Slots))
	args := make([]any, 0, len(prog.Slots)+3)
	for i, s := range prog.Slots {
		var v Value
		if raw, ok := c.Inputs[s.Name]; ok {
			v, err = ParseCaseValue(raw, s.Type)
			if err != nil {
				return SQLCaseResult{}, fmt.Errorf("input %s: %w", s.Name, err)
			}
		}
		name := fmt.Sprintf("v%d", i)
		cols[i] = d.Param(name) + " AS " + quoteIdent(s.Name)
		args = append(args, sql.Named(name, v.DriverValue()))
	}
	from := "(SELECT 1)"
	if len(cols) > 0 {
		from = "(SELECT " + strings.Join(cols, ", ") + ")"
	}
	ctx, err := c.Context.Context()
	if err != nil {
		return SQLCaseResult{}, err
	}
	args = append(args, d.ContextArgs(ctx)...)
	q := "SELECT (" + stmt.Value + ") AS v, (" + stmt.ErrorOrNull() + ") AS e FROM " + from
	var v, e any
	if err := db.QueryRow(q, args...).Scan(&v, &e); err != nil {
		return SQLCaseResult{Query: q}, fmt.Errorf("%s: %w", d.Name(), err)
	}
	res := SQLCaseResult{Query: q, Value: ValueFromDriver(v, prog.Result.Kind)}
	if e != nil {
		res.Code = fmt.Sprint(e)
	}
	return res, nil
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
