package query

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"

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

// Principal is who a filter runs for (ADR-0041): the id and the groups
// its read rights are compiled with, and whether it is an administrator,
// who reads everything.
type Principal struct {
	ID     string
	Groups []string
	Admin  bool
}

// Error is a refusal of a filter: a code of the store's list, the element
// of the filter it is about, the variable when the finding is about one,
// the byte offset inside where when the finding is in the formula, a
// message and a hint.
type Error struct {
	Code     string
	Element  string
	Variable string
	Pos      int
	Message  string
	Hint     string
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
	// CodeForbidden: the where or the order names a variable the
	// principal may read in no state at all (ADR-0041).
	CodeForbidden = "forbidden"
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
// The read rights of the variables the where and the order name are
// compiled into the predicate for the principal (ADR-0041). The context
// parameters of the dialect are bound by the caller.
func Compile(f Filter, cls *model.Class, sources formula.Sources, p Principal) (*Plan, error) {
	scope, cols, err := scopeOf(cls)
	if err != nil {
		return nil, err
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
	quoted := quoter(cols)
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
		if prog.ReadsStates {
			// What a life says of a state is read from the events of one
			// document (ADR-0047, item 6); the view of stays has it for all.
			return nil, &Error{Code: CodeNotAllowed, Element: "where", Pos: -1,
				Message: "a filter may not call a function of a state; it runs inside the query as a predicate, and those functions read the events of one document",
				Hint:    "query the " + cls.Name + "_states view, which has every stay of every document, or keep the answer in a triggered variable and filter on that"}
		}
		if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1,
				Message: "the filter must be a condition, got " + prog.Result.String(), Hint: "compare it, for example <> 0 or <> \"\""}
		}
		// STATE() and ID() are the row's columns here (ADR-0040): the
		// function and the bare column spell the same predicate.
		stmt, err := formula.GenerateSQLBound(prog, d, formula.SQLBinding{Column: quoted, Row: quoteIdent})
		if err != nil {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1, Message: err.Error()}
		}
		where := d.Cond(formula.SQLExpr{Val: stmt.Value, Kind: formula.KindBool})
		if stmt.Error != "" {
			where = "(" + stmt.Error + ") IS NULL AND " + where
		}
		// An operand that reads no document, the clock or the principal
		// alone, is a constant of the statement: its SQL, for the
		// prefilter to compare an indexed column with.
		constant := func(n formula.Node) (string, formula.Kind, bool) {
			p, err := formula.BindNode(f.Where, n, scope, formula.BindOptions{Sources: sources})
			if err != nil || len(p.Slots) != 0 || p.ReadsLife || p.Calls("STATE") || p.Calls("ID") {
				return "", 0, false
			}
			for _, c := range p.Classes {
				if c != formula.ClassPure && c != formula.ClassContext {
					return "", 0, false
				}
			}
			stmt, err := formula.GenerateSQLBound(p, d, formula.SQLBinding{Column: quoted, Row: quoteIdent})
			if err != nil {
				return "", 0, false
			}
			return stmt.Value, stmt.Kind, true
		}
		pre, args := prefilter(node, f.Where, cols, d, constant)
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
	// The read rights (ADR-0041): a variable the where or the order names
	// matches and sorts only in the rows whose state lets the principal
	// read it, so the engine never compares a value the caller may not
	// see. The right is one predicate per variable, conjoined with the
	// rest; a variable readable in no state at all refuses the filter.
	if cls.Rights != nil && !p.Admin && cls.States != nil {
		rights := rightsPlanFor(cls, d, quoted)
		var named []*varRights
		var preds []string
		consider := func(name string, order int) *Error {
			if j := strings.IndexByte(name, '.'); j >= 0 {
				name = name[:j]
			}
			for _, vr := range rights.vars {
				if !strings.EqualFold(vr.v.Name, name) {
					continue
				}
				for _, seen := range named {
					if seen == vr {
						return nil
					}
				}
				named = append(named, vr)
				pred, err := vr.predicate(p, d)
				if err != nil {
					err.Element = "where"
					if order >= 0 {
						err.Element = fmt.Sprintf("order/%d", order)
					}
					return err
				}
				if pred != "" {
					preds = append(preds, pred)
				}
			}
			return nil
		}
		if plan.Prog != nil {
			for _, sl := range plan.Prog.Slots {
				if err := consider(sl.Name, -1); err != nil {
					return nil, err
				}
			}
		}
		for i, o := range f.Order {
			if err := consider(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(o), "-+")), i); err != nil {
				return nil, err
			}
		}
		if len(preds) > 0 {
			conj := strings.Join(preds, " AND ")
			if plan.Where == "" {
				plan.Where = conj
			} else {
				plan.Where += " AND " + conj
			}
		}
	}
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

// scopeOf is what the formulas of a filter over a class bind against: the
// system columns and the class's variables, and the stored column of
// each name a formula may compare.
func scopeOf(cls *model.Class) (*formula.Scope, map[string]column, error) {
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
		return nil, nil, &Error{Code: CodeBadValue, Element: "class", Pos: -1, Message: err.Error()}
	}
	return scope, cols, nil
}

// quoter gives the SQL of the column a slot reads.
func quoter(cols map[string]column) func(formula.Slot) string {
	return func(s formula.Slot) string {
		c, ok := cols[strings.ToUpper(s.Name)]
		if !ok {
			return quoteIdent(strings.ReplaceAll(s.Name, ".", model.PathSeparator))
		}
		return quoteIdent(c.name)
	}
}

// Value translates a formula over a class's variables and system columns
// into the SQL of its value over the rows of the class's documents, with
// the kind of the value: for a statement of the store's own, such as the
// moment of a timed transition in the list of what is due (ADR-0047,
// item 8). The expression is NULL in a row where the formula is blank or
// fails. It applies no read right, since the statement is the engine's,
// and binds no parameter of its own: the context parameters of the
// dialect are the caller's to bind. A formula that looks something up or
// calls a function of a state is refused, as in a filter.
func Value(src string, cls *model.Class, sources formula.Sources) (string, formula.Kind, error) {
	scope, cols, err := scopeOf(cls)
	if err != nil {
		return "", 0, err
	}
	prog, err := formula.Bind(src, scope, formula.BindOptions{Sources: sources})
	if err != nil {
		return "", 0, formulaError(err)
	}
	for _, c := range prog.Classes {
		if c != formula.ClassPure && c != formula.ClassContext {
			return "", 0, &Error{Code: CodeNotAllowed, Element: "value", Pos: -1, Message: "the formula looks something up; it runs over every row of the class"}
		}
	}
	stmt, err := formula.GenerateSQLBound(prog, formula.NewSQLite(), formula.SQLBinding{Column: quoter(cols), Row: quoteIdent})
	if err != nil {
		return "", 0, &Error{Code: CodeBadFormula, Element: "value", Pos: -1, Message: err.Error()}
	}
	if stmt.Error != "" {
		return "CASE WHEN (" + stmt.Error + ") IS NULL THEN " + stmt.Value + " END", stmt.Kind, nil
	}
	return stmt.Value, stmt.Kind, nil
}

// rightsPlans caches, per class, the read rights as the compiler needs
// them (ADR-0041): for every variable with a read rule in some state, per
// state the group list or the SQL of the formula, and the predicate text
// ready when every rule is a formula, since it then depends on the
// principal through parameters alone. A class is compiled once and lives
// as long as the store holds it; Forget drops a class the store let go.
var rightsPlans sync.Map // *model.Class → *rightsPlan

type rightsPlan struct {
	vars []*varRights // the variables with a read rule, in declaration order
}

// varRights is the read right of one variable over the class's states.
type varRights struct {
	v      *model.Variable
	states []stateRight
	// decided is set when the predicate needs no principal: text is then
	// the whole of it, or "" when every state lets everyone read.
	decided bool
	text    string
	err     error
}

// stateRight is the rule in one state: a group list, a formula's
// condition, or neither for no rule.
type stateRight struct {
	state  string
	groups []string
	sql    string
}

func rightsPlanFor(cls *model.Class, d formula.Dialect, column func(formula.Slot) string) *rightsPlan {
	if p, ok := rightsPlans.Load(cls); ok {
		return p.(*rightsPlan)
	}
	plan := &rightsPlan{}
	for _, v := range cls.Variables {
		vr := &varRights{v: v}
		any, formulas := false, true
		for _, st := range cls.States.List {
			sr := stateRight{state: st}
			switch rule := cls.ReadRule(st, v.Name); {
			case rule == nil:
			case rule.Groups != nil:
				sr.groups, any, formulas = rule.Groups, true, false
			default:
				any = true
				stmt, err := formula.GenerateSQLBound(rule.Formula, d, formula.SQLBinding{Column: column, Row: quoteIdent})
				if err != nil {
					vr.err = fmt.Errorf("the read right of %s in %s: %w", v.Name, st, err)
					break
				}
				sr.sql = d.Cond(formula.SQLExpr{Val: stmt.Value, Kind: formula.KindBool})
				if stmt.Error != "" {
					sr.sql = "(" + stmt.Error + ") IS NULL AND " + sr.sql
				}
			}
			vr.states = append(vr.states, sr)
		}
		if !any {
			continue
		}
		if formulas && vr.err == nil {
			vr.text, _ = vr.compose(Principal{}, d)
			vr.decided = true
		}
		plan.vars = append(plan.vars, vr)
	}
	rightsPlans.Store(cls, plan)
	return plan
}

// Forget drops the cached rights of a class the store no longer holds.
func Forget(cls *model.Class) { rightsPlans.Delete(cls) }

// predicate is the condition over the row for the principal: "" when
// every state lets the principal read the variable, forbidden when none
// does, which is decided without a row.
func (vr *varRights) predicate(p Principal, d formula.Dialect) (string, *Error) {
	if vr.err != nil {
		return "", &Error{Code: CodeBadFormula, Variable: vr.v.Name, Pos: -1, Message: vr.err.Error()}
	}
	if vr.decided {
		return vr.text, nil
	}
	text, readable := vr.compose(p, d)
	if !readable {
		who := p.ID
		if who == "" {
			who = "a call without a principal"
		}
		return "", &Error{Code: CodeForbidden, Variable: vr.v.Name, Pos: -1, Message: who + " may read " + vr.v.Name + " in no state",
			Hint: "leave the variable out of where and order, or run the filter as a principal who may read it"}
	}
	return text, nil
}

// compose writes the predicate as a CASE over the state column, the rule
// of each state resolved for the principal: a group list the literal
// test of the principal's groups, a formula its condition. It returns ""
// when every state reads TRUE, and readable false when none can.
func (vr *varRights) compose(p Principal, d formula.Dialect) (text string, readable bool) {
	var b strings.Builder
	allTrue := true
	for _, sr := range vr.states {
		cond := d.True()
		switch {
		case sr.groups != nil:
			if !inGroups(sr.groups, p.Groups) {
				cond = d.False()
			}
		case sr.sql != "":
			cond = sr.sql
		}
		allTrue = allTrue && cond == d.True()
		readable = readable || cond != d.False()
		b.WriteString(" WHEN " + d.TextLit(sr.state) + " THEN (" + cond + ")")
	}
	if allTrue {
		return "", true
	}
	if !readable {
		return "", false
	}
	return `(CASE "state"` + b.String() + " ELSE " + d.False() + " END)", true
}

func inGroups(list, groups []string) bool {
	for _, g := range list {
		for _, h := range groups {
			if g == h {
				return true
			}
		}
	}
	return false
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
func prefilter(n formula.Node, src string, cols map[string]column, d formula.Dialect, constant func(formula.Node) (string, formula.Kind, bool)) (string, []any) {
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
			// A date, a datetime or a number column against an operand
			// that reads no document, such as due < TODAY(): the operand
			// is one value for the whole statement, so the comparison can
			// seek an index as one with a literal does. Both are stored
			// and computed as plain numbers, and a blank on either side
			// compares as zero.
			c, expr, fl := columnAndOperand(b, cols)
			if c == nil {
				return
			}
			switch c.typ.Kind {
			case formula.KindDate, formula.KindDateTime, formula.KindNumber:
			default:
				return
			}
			val, kind, ok := constant(expr)
			if !ok || kind != c.typ.Kind {
				return
			}
			if fl {
				op = flip[op]
			}
			val = "COALESCE(" + val + ", 0)"
			terms = append(terms, "("+quoteIdent(c.name)+" "+op+" "+val+" OR ("+quoteIdent(c.name)+" IS NULL AND 0 "+op+" "+val+"))")
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

// columnAndOperand is the column of a comparison and its other operand,
// whatever it is, and whether the column stands on the right.
func columnAndOperand(b *formula.Binary, cols map[string]column) (*column, formula.Node, bool) {
	if c := columnOf(b.L, cols); c != nil {
		return c, b.R, false
	}
	if c := columnOf(b.R, cols); c != nil {
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
	case *formula.Call:
		// STATE() and ID() are the state and id columns (ADR-0040).
		if len(n.Args) != 0 {
			return nil
		}
		switch strings.ToUpper(n.Name) {
		case "STATE":
			path = "state"
		case "ID":
			path = "id"
		default:
			return nil
		}
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
