package query

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// TableFilter is a question to the rows of an explicit table (ADR-0076,
// item 9): which of them, in what order, and which page. It is what a
// QUERY whose source is a table asks, on a document: Cond, Src, Scope,
// Value and Ref of on are the condition and what it reads of that
// document, as for a list over a class.
type TableFilter struct {
	// Order lists columns to sort by, a leading "-" for descending;
	// empty is by the key of the table.
	Order []string
	// Limit is at most MaxLimit; 0 is DefaultLimit. Offset skips rows.
	Limit, Offset int
}

// CompileTable binds the condition of a list over a table against the
// table's columns and translates it for SQLite. A table has no read
// rule for each row and no rights for each column: who may read it is
// one line of its definition, which the caller asks before it reads.
// The context parameters of the dialect are bound by the caller.
func CompileTable(f TableFilter, on *On, src *formula.Source, sources formula.Sources) (*Plan, error) {
	d := formula.NewSQLite()
	plan := &Plan{Limit: f.Limit, Offset: f.Offset}
	switch {
	case plan.Limit == 0:
		plan.Limit = DefaultLimit
	case plan.Limit < 0 || plan.Limit > MaxLimit:
		return nil, &Error{Code: CodeBadValue, Element: "limit", Pos: -1, Message: fmt.Sprintf("the limit is 1 to %d, got %d", MaxLimit, f.Limit)}
	}
	if plan.Offset < 0 {
		return nil, &Error{Code: CodeBadValue, Element: "offset", Pos: -1, Message: fmt.Sprintf("the offset is not negative, got %d", f.Offset)}
	}
	vars := make([]formula.Variable, 0, len(src.Columns))
	cols := map[string]column{}
	for _, c := range src.Columns {
		vars = append(vars, formula.Variable{Name: c.Name, Type: c.Type})
		cols[strings.ToUpper(c.Name)] = column{name: c.Name, typ: c.Type}
	}
	scope, err := formula.NewScope(vars...)
	if err != nil {
		return nil, &Error{Code: CodeBadValue, Element: "table", Pos: -1, Message: err.Error()}
	}
	quoted := quoter(cols)
	var given []any
	if on != nil && on.Cond != nil {
		opts := formula.BindOptions{Sources: sources, This: on.Scope}
		prog, err := formula.BindNode(on.Src, on.Cond, scope, opts)
		if err != nil {
			return nil, formulaError(err)
		}
		for _, c := range prog.Classes {
			if c == formula.ClassLookup || c == formula.ClassQuery {
				return nil, &Error{Code: CodeNotAllowed, Element: "where", Pos: -1,
					Message: "the condition of a list looks nothing up; it runs inside the query as a predicate",
					Hint:    "move the lookup into a computed variable of the document, and compare with THIS.<variable>"}
			}
		}
		if prog.ReadsStates || len(prog.Folds) > 0 {
			return nil, &Error{Code: CodeNotAllowed, Element: "where", Pos: -1,
				Message: "the condition of a list over a table reads the columns of the table and the values of the document, and nothing of a state's life or of a list"}
		}
		if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1,
				Message: "the condition must be a condition, got " + prog.Result.String(), Hint: "compare it, for example <> 0 or <> \"\""}
		}
		stmt, err := formula.GenerateSQLBound(prog, d, formula.SQLBinding{Column: on.bound(quoted, d, &given), Row: quoteIdent})
		if err != nil {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1, Message: err.Error()}
		}
		where := d.Cond(formula.SQLExpr{Val: stmt.Value, Kind: formula.KindBool})
		if stmt.Error != "" {
			where = "(" + stmt.Error + ") IS NULL AND " + where
		}
		plan.Where = where
		plan.Prog = prog
	}
	// Order: what is asked, then the key of the table, so that a page
	// is the same page each time it is read.
	var order []string
	seen := map[string]bool{}
	for i, o := range f.Order {
		desc := false
		name := strings.TrimSpace(o)
		if strings.HasPrefix(name, "-") {
			desc, name = true, strings.TrimSpace(name[1:])
		} else if strings.HasPrefix(name, "+") {
			name = strings.TrimSpace(name[1:])
		}
		c, ok := cols[strings.ToUpper(name)]
		if !ok || !c.typ.IsScalar() {
			return nil, &Error{Code: CodeUnknownVariable, Element: fmt.Sprintf("order/%d", i), Pos: -1,
				Message: "cannot order by " + strconv.Quote(name) + ": not a column of " + src.Name}
		}
		if seen[strings.ToUpper(name)] {
			return nil, &Error{Code: CodeBadValue, Element: fmt.Sprintf("order/%d", i), Pos: -1, Message: name + " is listed twice"}
		}
		seen[strings.ToUpper(name)] = true
		term := quoteIdent(c.name)
		if desc {
			term += " DESC"
		}
		order = append(order, term)
	}
	for _, k := range src.Key {
		if !seen[strings.ToUpper(k)] {
			seen[strings.ToUpper(k)] = true
			order = append(order, quoteIdent(k))
		}
	}
	plan.Order = strings.Join(order, ", ")
	plan.Args = append(plan.Args, given...)
	return plan, nil
}
