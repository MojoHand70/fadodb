package query

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Filter is one query over a class (ADR-0038).
type Filter struct {
	Class string `json:"class"`
	// Where is a formula over the class's variables and the system
	// columns id, title, state, version, class_version, created_at,
	// updated_at and state_entered_at; empty matches every document.
	Where string `json:"where,omitempty"`
	// Order lists variables or system columns to sort by, a leading "-"
	// for descending; empty is by id.
	Order []string `json:"order,omitempty"`
	// Limit is at most MaxLimit; 0 is DefaultLimit. Offset skips rows.
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
	// Fields names the variables to return; empty returns every variable.
	Fields []string `json:"fields,omitempty"`
}

// The row limits of a filter (ADR-0013, ADR-0020).
const (
	DefaultLimit = 1000
	MaxLimit     = 10000
)

// Plan is a compiled filter: the fragments the store puts around its
// select list, the parameters, and the fields to return.
type Plan struct {
	// Where is the WHERE clause without the keyword, "" for none.
	Where string
	// Order is the ORDER BY list without the keywords.
	Order string
	// Args are the named parameters Where references, the prefilter's
	// literals and the context parameters of the dialect.
	Args          []any
	Limit, Offset int
	// Fields are the declared names of the variables to return, nil for
	// all.
	Fields []string
	// Prog is the bound where formula, nil when there is none.
	Prog *formula.Program
}

// Error is a refusal of a filter: a code of the store's list, the element
// of the filter it is about, the byte offset inside where when the
// finding is in the formula, a message and a hint.
type Error struct {
	Code    string
	Element string
	Pos     int
	Message string
	Hint    string
}

func (e *Error) Error() string {
	s := e.Element + ": " + e.Message
	if e.Pos >= 0 {
		s = fmt.Sprintf("%s at offset %d: %s", e.Element, e.Pos, e.Message)
	}
	s += " [" + e.Code + "]"
	if e.Hint != "" {
		s += "; " + e.Hint
	}
	return s
}

// The codes a filter is refused with.
const (
	CodeBadFormula      = "bad_formula"
	CodeNotAllowed      = "not_allowed"
	CodeUnknownVariable = "unknown_variable"
	CodeBadValue        = "bad_value"
)

// systemColumns are the columns every document has, as the filter scope
// names them (ADR-0013, ADR-0014).
var systemColumns = []formula.Variable{
	{Name: "id", Type: formula.Scalar(formula.KindText)},
	{Name: "title", Type: formula.Scalar(formula.KindText)},
	{Name: "state", Type: formula.Scalar(formula.KindText)},
	{Name: "version", Type: formula.Scalar(formula.KindNumber)},
	{Name: "class_version", Type: formula.Scalar(formula.KindNumber)},
	{Name: "created_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "updated_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "state_entered_at", Type: formula.Scalar(formula.KindDateTime)},
}

// column is one stored column the where may reference.
type column struct {
	name string // the SQL column
	typ  formula.Type
}

// Compile binds a filter against a class and translates it for SQLite.
// sources are the explicit tables the store has, so that a lookup in the
// where is refused as a lookup and not as an unknown name; nil is none.
// The context parameters of the dialect are bound by the caller.
func Compile(f Filter, cls *model.Class, sources formula.Sources) (*Plan, error) {
	cols := map[string]column{}
	vars := make([]formula.Variable, 0, len(systemColumns)+len(cls.Variables))
	for _, sc := range systemColumns {
		vars = append(vars, sc)
		cols[strings.ToUpper(sc.Name)] = column{name: sc.Name, typ: sc.Type}
	}
	for _, v := range cls.Variables {
		vars = append(vars, formula.Variable{Name: v.Name, Type: v.Type})
	}
	for _, c := range cls.Columns {
		if strings.HasSuffix(c.Name, formula.CurrencySuffix) && c.Type.Kind == formula.KindText {
			if prev, ok := cols[strings.ToUpper(c.Path)]; ok && prev.typ.Kind == formula.KindMoney {
				continue
			}
		}
		cols[strings.ToUpper(c.Path)] = column{name: c.Name, typ: c.Type}
	}
	scope, err := formula.NewScope(vars...)
	if err != nil {
		return nil, &Error{Code: CodeBadValue, Element: "class", Pos: -1, Message: err.Error()}
	}
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
	quoted := func(s formula.Slot) string {
		c, ok := cols[strings.ToUpper(s.Name)]
		if !ok {
			return quoteIdent(strings.ReplaceAll(s.Name, ".", model.PathSeparator))
		}
		return quoteIdent(c.name)
	}
	if strings.TrimSpace(f.Where) != "" {
		node, err := formula.Parse(f.Where)
		if err != nil {
			return nil, formulaError(err)
		}
		prog, err := formula.BindNode(f.Where, node, scope, formula.BindOptions{Sources: sources})
		if err != nil {
			return nil, formulaError(err)
		}
		for _, c := range prog.Classes {
			if c == formula.ClassLookup || c == formula.ClassQuery {
				return nil, &Error{Code: CodeNotAllowed, Element: "where", Pos: -1,
					Message: "a filter may use pure and context functions only; it runs inside the query as a predicate",
					Hint:    "move the lookup into a computed variable and filter on that variable"}
			}
		}
		if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1,
				Message: "the filter must be a condition, got " + prog.Result.String(), Hint: "compare it, for example <> 0 or <> \"\""}
		}
		stmt, err := formula.GenerateSQL(prog, d, quoted)
		if err != nil {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1, Message: err.Error()}
		}
		where := d.Cond(formula.SQLExpr{Val: stmt.Value, Kind: formula.KindBool})
		if stmt.Error != "" {
			where = "(" + stmt.Error + ") IS NULL AND " + where
		}
		pre, args := prefilter(node, f.Where, cols, d)
		if pre != "" {
			where = pre + " AND (" + where + ")"
		}
		plan.Where = where
		plan.Args = args
		plan.Prog = prog
	}
	// Order: by id unless told otherwise.
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
				Message: "cannot order by " + strconv.Quote(name) + ": not a scalar variable, a field path or a system column of " + cls.Name}
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
	if !seen["ID"] {
		order = append(order, `"id"`)
	}
	plan.Order = strings.Join(order, ", ")
	// Fields: variables by declared name.
	if len(f.Fields) > 0 {
		plan.Fields = make([]string, 0, len(f.Fields))
		fseen := map[string]bool{}
		for i, name := range f.Fields {
			v, ok := cls.Variable(name)
			if !ok {
				return nil, &Error{Code: CodeUnknownVariable, Element: fmt.Sprintf("fields/%d", i), Pos: -1,
					Message: "the class " + cls.Name + " has no variable " + strconv.Quote(name)}
			}
			if !fseen[strings.ToUpper(v.Name)] {
				fseen[strings.ToUpper(v.Name)] = true
				plan.Fields = append(plan.Fields, v.Name)
			}
		}
	}
	return plan, nil
}

func formulaError(err error) *Error {
	if fe, ok := err.(*formula.Error); ok {
		return &Error{Code: CodeBadFormula, Element: "where", Pos: fe.Pos, Message: fe.Msg}
	}
	return &Error{Code: CodeBadFormula, Element: "where", Pos: -1, Message: err.Error()}
}

// prefilter adds, for each comparison of a column with a literal in the
// top-level AND of the formula, the comparison on the bare column, which
// an index answers. When a blank would satisfy the comparison, the blank
// rows are kept with "OR column IS NULL". Money columns, and literals that
// do not fit the column's scale, are left to the exact predicate.
func prefilter(n formula.Node, src string, cols map[string]column, d formula.Dialect) (string, []any) {
	var terms []string
	var args []any
	var walk func(formula.Node)
	walk = func(n formula.Node) {
		b, ok := n.(*formula.Binary)
		if !ok {
			return
		}
		if b.Op == formula.TokAnd {
			walk(b.L)
			walk(b.R)
			return
		}
		op, ok := sargable[b.Op]
		if !ok {
			return
		}
		col, lit, flipped := columnAndLiteral(b, cols)
		if col == nil {
			return
		}
		if flipped {
			op = flip[op]
		}
		val, c, ok := literalFor(lit, src, col.typ)
		if !ok {
			return
		}
		name := fmt.Sprintf("f%d", len(args))
		term := quoteIdent(col.name) + " " + op + " " + d.Param(name)
		if zeroSatisfies(op, c) {
			term = "(" + term + " OR " + quoteIdent(col.name) + " IS NULL)"
		}
		terms = append(terms, term)
		args = append(args, sql.Named(name, val))
	}
	walk(n)
	return strings.Join(terms, " AND "), args
}

var sargable = map[formula.TokenKind]string{
	formula.TokEq: "=", formula.TokLt: "<", formula.TokLe: "<=", formula.TokGt: ">", formula.TokGe: ">=",
}

var flip = map[string]string{"=": "=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}

// columnAndLiteral splits a comparison into its column and its literal
// side; flipped says the literal was on the left.
func columnAndLiteral(b *formula.Binary, cols map[string]column) (*column, formula.Node, bool) {
	if c := columnOf(b.L, cols); c != nil && isLiteral(b.R) {
		return c, b.R, false
	}
	if c := columnOf(b.R, cols); c != nil && isLiteral(b.L) {
		return c, b.L, true
	}
	return nil, nil, false
}

func columnOf(n formula.Node, cols map[string]column) *column {
	var path string
	switch n := n.(type) {
	case *formula.Ident:
		path = n.Name
	case *formula.Member:
		path = n.Base + "." + strings.Join(n.Path, ".")
	default:
		return nil
	}
	c, ok := cols[strings.ToUpper(path)]
	if !ok || !c.typ.IsScalar() || c.typ.Kind == formula.KindMoney || c.typ.Kind == formula.KindJSON {
		return nil
	}
	return &c
}

func isLiteral(n formula.Node) bool {
	switch n := n.(type) {
	case *formula.NumberLit, *formula.TextLit, *formula.BoolLit:
		return true
	case *formula.Unary:
		_, num := n.X.(*formula.NumberLit)
		return n.Op == formula.TokMinus && num
	}
	return false
}

// zeroSatisfies says whether the zero a blank reads as satisfies the
// comparison with the literal, given the sign of zero against the
// literal: -1 when zero is below it, 0 when equal, 1 when above.
func zeroSatisfies(op string, c int) bool {
	switch op {
	case "=":
		return c == 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	}
	return c >= 0
}

// literalFor turns a literal into the stored value of the column's type
// and returns the sign of the zero a blank reads as against the literal.
func literalFor(n formula.Node, src string, t formula.Type) (val any, zeroCmp int, ok bool) {
	switch lit := n.(type) {
	case *formula.TextLit:
		if t.Kind != formula.KindText && t.Kind != formula.KindRef {
			return nil, 0, false
		}
		return lit.Value, strings.Compare("", lit.Value), true
	case *formula.BoolLit:
		if t.Kind != formula.KindBool {
			return nil, 0, false
		}
		if lit.Value {
			return int64(1), -1, true
		}
		return int64(0), 0, true
	}
	text, f, ok := numberText(n, src)
	if !ok {
		return nil, 0, false
	}
	switch t.Kind {
	case formula.KindNumber:
		return f, sign(-f), true
	case formula.KindDecimal:
		v, err := formula.ParseExact(text, t)
		if err != nil {
			return nil, 0, false
		}
		return v.Mant(), sign(float64(-v.Mant())), true
	}
	return nil, 0, false
}

func sign(f float64) int {
	switch {
	case f < 0:
		return -1
	case f > 0:
		return 1
	}
	return 0
}

func numberText(n formula.Node, src string) (string, float64, bool) {
	switch lit := n.(type) {
	case *formula.NumberLit:
		return lit.Text(src), lit.Value, true
	case *formula.Unary:
		if num, ok := lit.X.(*formula.NumberLit); ok && lit.Op == formula.TokMinus {
			return "-" + num.Text(src), -num.Value, true
		}
	}
	return "", 0, false
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
