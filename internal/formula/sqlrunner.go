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
// as named parameters over a one-row subselect, a money slot as its amount
// and its currency column, binds the context, and reads the value, the
// error and the currency columns. It is the shared half of every SQL
// conformance and property runner; comparing with the VM is the runner's
// job.
func RunSQL(db *sql.DB, d Dialect, prog *Program, vars []Value, ctx *Context) (SQLResult, error) {
	if len(vars) < len(prog.Slots) {
		return SQLResult{}, fmt.Errorf("program reads %d variables, got %d", len(prog.Slots), len(vars))
	}
	stmt, err := GenerateSQL(prog, d, func(s Slot) string { return "v." + quoteIdent(s.Name) })
	if err != nil {
		return SQLResult{}, err
	}
	cols := make([]string, 0, len(prog.Slots)+1)
	args := make([]any, 0, len(prog.Slots)+4)
	for i, s := range prog.Slots {
		name := fmt.Sprintf("v%d", i)
		cols = append(cols, d.Param(name)+" AS "+quoteIdent(s.Name))
		args = append(args, sql.Named(name, vars[i].DriverValue()))
		if s.Type.Kind == KindMoney {
			var cur any
			if vars[i].kind == KindMoney {
				cur = vars[i].str
			}
			cols = append(cols, d.Param(name+"c")+" AS "+quoteIdent(s.Name+CurrencySuffix))
			args = append(args, sql.Named(name+"c", cur))
		}
	}
	from := "(SELECT 1) AS v"
	if len(cols) > 0 {
		from = "(SELECT " + strings.Join(cols, ", ") + ") AS v"
	}
	args = append(args, d.ContextArgs(ctx)...)
	q := "SELECT (" + stmt.Value + ") AS v, (" + stmt.ErrorOrNull() + ") AS e, (" + stmt.CurrencyOrNull() + ") AS c FROM " + from
	var v, e, c any
	if err := db.QueryRow(q, args...).Scan(&v, &e, &c); err != nil {
		return SQLResult{Query: q}, fmt.Errorf("%s: %w", d.Name(), err)
	}
	res := SQLResult{Query: q, Value: ValueOfType(v, prog.Result, c)}
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

// SetupSources creates the tables of the sources in a database, named as
// the sources are, and fills them: the conformance and property runners'
// half of LOOKUP on the SQL dialects. Rows are row-major in column order;
// a money column is its amount and its currency column.
func SetupSources(db *sql.DB, d Dialect, rows map[*Source][][]Value) error {
	for src, data := range rows {
		cols := make([]string, 0, len(src.Columns))
		names := make([]string, 0, len(src.Columns))
		for _, f := range src.Columns {
			cols = append(cols, quoteIdent(f.Name)+" "+d.ColumnType(f.Type.Kind))
			names = append(names, quoteIdent(f.Name))
			if f.Type.Kind == KindMoney {
				cols = append(cols, quoteIdent(f.Name+CurrencySuffix)+" "+d.ColumnType(KindText))
				names = append(names, quoteIdent(f.Name+CurrencySuffix))
			}
		}
		if _, err := db.Exec("DROP TABLE IF EXISTS " + quoteIdent(src.Name)); err != nil {
			return fmt.Errorf("%s: %w", src.Name, err)
		}
		if _, err := db.Exec("CREATE TABLE " + quoteIdent(src.Name) + " (" + strings.Join(cols, ", ") + ")"); err != nil {
			return fmt.Errorf("%s: %w", src.Name, err)
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")
		insert := "INSERT INTO " + quoteIdent(src.Name) + " (" + strings.Join(names, ", ") + ") VALUES (" + marks + ")"
		for _, row := range data {
			args := make([]any, 0, len(names))
			for i, f := range src.Columns {
				v := row[i]
				args = append(args, v.DriverValue())
				if f.Type.Kind == KindMoney {
					var cur any
					if v.kind == KindMoney {
						cur = v.str
					}
					args = append(args, cur)
				}
			}
			if _, err := db.Exec(insert, args...); err != nil {
				return fmt.Errorf("%s: %w", src.Name, err)
			}
		}
	}
	return nil
}
