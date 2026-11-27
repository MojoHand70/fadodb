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
	// Fits are the comparisons of the question that an index of the
	// class answers by itself, when there are two or more of them. The
	// store has no numbers on the data to choose between two indexes
	// (ADR-0075, item 10), so whoever runs the plan looks into each and
	// asks the question through the one that ends first: its Where and
	// its Args, in place of the plan's. Where two looks end alike the
	// earlier fit is taken: the state stands last among them. A question
	// that one index fits, or none, has no Fits.
	Fits []Fit
	// Led says the condition compares an indexed column on the bare
	// column, so that it leads the store to an index by itself.
	Led bool
	// Served says an index serves the question: the caller's condition,
	// or the read rule, offers the store a comparison of an indexed
	// column, or the question has no condition and no order of its own
	// and is read in the order of the ids. Lacks is then "". When no
	// index serves it, Lacks is the
	// first column the caller's condition compares with a value, which
	// an index would be needed on, and "" when it compares none.
	Served bool
	Lacks  string
	// Narrowed says an index narrows the question to the documents it
	// names: the caller's condition, or the read rule, compares an
	// indexed column. A count or a total that no index narrows reads
	// every document of the class to be worked out, and is the
	// analytical engine's to answer when the program keeps a copy
	// (ADR-0089, item 6).
	Narrowed bool
}

// Fit is one comparison of a question that an index of the class
// answers by itself.
type Fit struct {
	// Column is the column the index is on. Look is the comparison, on
	// the bare column, which Args bind beside the context of the caller.
	Column, Look string
	// Where and Args ask the question so that the store is offered this
	// index and no other.
	Where string
	Args  []any
}

// Principal is who a filter runs for (ADR-0041): the id and the groups
// its read rights are compiled with, the system groups it is in, and
// whether it is a content administrator, who reads everything
// (ADR-0052).
type Principal struct {
	ID      string
	Groups  []string
	System  []string
	Content bool
}

// in reports whether the principal is in one of the groups listed: its
// own, its system groups, or everyone, which a principal that has an id
// is in (ADR-0052). It answers what INGROUP answers on the Go VM.
func (p Principal) in(list []string) bool {
	for _, g := range list {
		for _, h := range p.Groups {
			if g == h {
				return true
			}
		}
		for _, h := range p.System {
			if g == h {
				return true
			}
		}
		if p.ID != "" && g == formula.GroupEveryone {
			return true
		}
	}
	return false
}

// named says of an operand of a comparison that it is the caller's id
// and that the caller has one: USER() is then a value that is not blank,
// and a column compared with it needs no care for blanks.
func (p Principal) named(n formula.Node) bool {
	call, ok := n.(*formula.Call)
	return ok && call.Name == "USER" && len(call.Args) == 0 && p.ID != ""
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
var systemColumns = model.FilterColumns

// column is one stored column the where may reference.
type column struct {
	name string // the SQL column
	typ  formula.Type
	// indexed says the column has an index: the id, the state, the two
	// moments, and a variable the class keeps one for.
	indexed bool
}

// Compile binds a filter against a class and translates it for SQLite.
// sources are the explicit tables the store has, so that a lookup in the
// where is refused as a lookup and not as an unknown name; nil is none.
// The read rights of the variables the where and the order name are
// compiled into the predicate for the principal (ADR-0041). The context
// parameters of the dialect are bound by the caller.
func Compile(f Filter, cls *model.Class, sources formula.Sources, p Principal) (*Plan, error) {
	return CompileOn(f, nil, cls, sources, p)
}

// On is the document a list is read on (ADR-0076, item 9): the where of
// the filter is then the condition of the QUERY a live variable of that
// document holds. Inside it THIS.name is a variable of the document and
// REF() the pointer to it; each is one value handed to the statement, so
// the list of one order is the query a person would write with the
// order's id in its where.
type On struct {
	// Cond is the condition, parsed, nil for none, and Src the formula
	// its positions point into. The filter's own Where is not read.
	Cond formula.Node
	Src  string
	// Scope are the variables of the document, and Value the value of
	// one of them by its declared name.
	Scope *formula.Scope
	Value func(name string) (formula.Value, bool)
	// Ref is the pointer to the document, in its Class:id form.
	Ref string
	// Reads are variables of the listed class that the statement reads
	// beside the condition: the column a figure over the list adds up
	// (ADR-0081). A row counts only where its state lets the principal
	// read each of them, as for a variable the condition names
	// (ADR-0041).
	Reads []string
}

// CompileOn is Compile for a list read on a document; on is nil for a
// filter that stands alone.
func CompileOn(f Filter, on *On, cls *model.Class, sources formula.Sources, p Principal) (*Plan, error) {
	return CompileFor(formula.NewSQLite(), f, on, cls, sources, p)
}

// CompileFor is CompileOn in the dialect of an engine: the store's, or
// the analytical engine's, whose copy holds the documents of a class in
// a table of the class's name with the columns of its view (ADR-0089,
// item 7). The reader's rule and rights are written into the statement
// for either.
func CompileFor(d formula.Dialect, f Filter, on *On, cls *model.Class, sources formula.Sources, p Principal) (*Plan, error) {
	scope, cols, err := scopeOf(cls)
	if err != nil {
		return nil, err
	}
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
	// REF() in a filter is the pointer to the document of the row, as
	// ID() is its id, and a REF that names a class writes it as the
	// class of the filter does (ADR-0076, item 10).
	src, opts, notBlank := f.Where, formula.BindOptions{Sources: sources, Class: cls.Name, Classes: cls, App: cls.App}, func(n formula.Node) bool { return isNamed(n) || p.named(n) }
	// bound gives the SQL of a slot of the caller's condition: a column
	// of the class, or a value of the document a list is read on.
	bound := quoted
	var node formula.Node
	// given are the values of the document a list is read on, each a
	// parameter of the statement.
	var given []any
	if on != nil {
		src, node, opts.This = on.Src, on.Cond, on.Scope
		// The condition is a class's own, held to its rules when the
		// class was defined: REF() in it is the document the list is
		// read on, of whatever class that is.
		opts.Class, opts.Loose = "", true
		bound, notBlank = on.bound(quoted, d, &given), func(n formula.Node) bool { return isRef(n) || isNamed(n) || p.named(n) || on.holds(n) }
	} else if strings.TrimSpace(f.Where) != "" {
		if node, err = formula.Parse(f.Where); err != nil {
			return nil, formulaError(err)
		}
	}
	// led says the caller's own condition compares an indexed column, so
	// that it leads the statement to an index by itself.
	led := false
	// own is what the caller's condition offers the store, rule what
	// the read rule does, and readable the predicate of the read
	// rights: the three parts of the condition, kept apart for a
	// question that is asked through one index of several (ADR-0075,
	// item 10).
	var own, rule offer
	var readable string
	if node != nil {
		prog, err := formula.BindNode(src, node, scope, opts)
		if err != nil {
			return nil, formulaError(err)
		}
		if e := noLive(cls, prog, "where"); e != nil {
			return nil, e
		}
		if len(prog.Folds) > 0 {
			return nil, &Error{Code: CodeNotAllowed, Element: "where", Pos: -1,
				Message: "a filter may not work a figure out of the lines or tags of a document yet; it runs inside the query as a predicate (ADR-0081)",
				Hint:    "keep the figure in a computed variable and filter on that variable"}
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
		stmt, err := formula.GenerateSQLBound(prog, d, formula.SQLBinding{Column: bound, Row: quoteIdent})
		if err != nil {
			return nil, &Error{Code: CodeBadFormula, Element: "where", Pos: -1, Message: err.Error()}
		}
		where := d.Cond(formula.SQLExpr{Val: stmt.Value, Kind: formula.KindBool})
		if stmt.Error != "" {
			where = "(" + stmt.Error + ") IS NULL AND " + where
		}
		terms, args := prefilter(node, src, cols, d, constantOf(src, scope, opts, d, bound), "f", notBlank)
		own = offer{terms: terms, args: args, exact: where}
		if pre := own.bare(everyTerm); pre != "" {
			where = pre + " AND (" + where + ")"
		}
		for i := range terms {
			led = led || terms[i].indexed
		}
		plan.Led = led
		plan.Where = where
		plan.Args = args
		plan.Prog = prog
	}
	// The read rule of the class (ADR-0049, item 5): one more predicate,
	// from the formula the Go VM runs for a read by id, so that a filter
	// and a count leave out what the caller may not see.
	if !p.Content {
		rp, err := readPlanFor(cls, scope, cols, sources, d, quoted, p)
		if err != nil {
			return nil, err
		}
		// The rule offers its own comparisons to an index only where the
		// caller's condition offers none: with both, SQLite, which knows
		// nothing of how many rows each finds, may take the rule's index
		// and read every document of the caller to find one by its
		// number.
		rule = offer{terms: rp.terms, args: rp.args, exact: rp.plain, whole: true}
		where, args := rp.where, rp.args
		if led {
			where, args = rp.plain, nil
		}
		if where != "" {
			if plan.Where == "" {
				plan.Where = where
			} else {
				plan.Where = "(" + plan.Where + ") AND " + where
			}
			plan.Args = append(plan.Args[:len(plan.Args):len(plan.Args)], args...)
		}
	}
	// Order: by id unless told otherwise.
	var order []string
	ofStore := d.Name() == (formula.SQLite{}).Name()
	seen := map[string]bool{}
	for i, o := range f.Order {
		desc := false
		name := strings.TrimSpace(o)
		if strings.HasPrefix(name, "-") {
			desc, name = true, strings.TrimSpace(name[1:])
		} else if strings.HasPrefix(name, "+") {
			name = strings.TrimSpace(name[1:])
		}
		if v, is := cls.Variable(name); is && v.Live() {
			return nil, liveIn(v.Name, fmt.Sprintf("order/%d", i))
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
		// In the store a blank sorts before every value: first going
		// up, last going down. Another engine is told so, since its own
		// way may be another (ADR-0089, item 7).
		if !ofStore {
			if desc {
				term += " NULLS LAST"
			} else {
				term += " NULLS FIRST"
			}
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
	if cls.Rights != nil && !p.Content && cls.States != nil {
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
		if on != nil {
			for _, name := range on.Reads {
				if err := consider(name, -1); err != nil {
					return nil, err
				}
			}
		}
		if len(preds) > 0 {
			conj := strings.Join(preds, " AND ")
			readable = conj
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
			if v.Live() {
				return nil, liveIn(v.Name, fmt.Sprintf("fields/%d", i))
			}
			if !fseen[strings.ToUpper(v.Name)] {
				fseen[strings.ToUpper(v.Name)] = true
				plan.Fields = append(plan.Fields, v.Name)
			}
		}
	}
	plan.Args = append(plan.Args[:len(plan.Args):len(plan.Args)], given...)
	through(plan, on != nil, &own, &rule, readable, given)
	// Whether an index serves the question (ADR-0075, item 6), and the
	// column one would be needed on when none does. A question with no
	// condition and no order of its own is read in the order of the
	// ids, a page at a time, which the key of the table serves.
	plan.Narrowed = led
	for i := range rule.terms {
		plan.Narrowed = plan.Narrowed || rule.terms[i].indexed
	}
	plan.Served = plan.Narrowed || node == nil && len(f.Order) == 0
	if !plan.Served && len(own.terms) > 0 {
		plan.Lacks = own.terms[0].col
	}
	return plan, nil
}

// offer is one part of a condition as the store is given it: the
// comparisons on bare columns, which an index may answer, beside the
// exact predicate they were read from. whole says the exact predicate
// stands in its own brackets already, as the read rule's does.
type offer struct {
	terms []bare
	args  []any
	exact string
	whole bool
}

// everyTerm keeps every comparison of an offer.
func everyTerm(*bare) bool { return true }

// bare joins the comparisons of the offer that keep lets through.
func (o *offer) bare(keep func(*bare) bool) string {
	n, size := 0, 0
	var one *bare
	for i := range o.terms {
		if t := &o.terms[i]; keep(t) {
			one = t
			n++
			size += len(t.sql) + len(" AND ")
		}
	}
	switch n {
	case 0:
		return ""
	case 1:
		return one.sql
	}
	var b strings.Builder
	b.Grow(size)
	for i := range o.terms {
		if t := &o.terms[i]; keep(t) {
			if b.Len() > 0 {
				b.WriteString(" AND ")
			}
			b.WriteString(t.sql)
		}
	}
	return b.String()
}

// with is the offer with the comparisons keep lets through, and their
// parameters added to args.
func (o *offer) with(keep func(*bare) bool, args *[]any) string {
	pre := o.bare(keep)
	for i := range o.terms {
		if t := &o.terms[i]; keep(t) {
			*args = append(*args, o.args[t.lo:t.hi]...)
		}
	}
	switch {
	case o.exact == "" || o.exact == "0":
		return o.exact
	case pre == "":
		return o.exact
	case o.whole:
		return "(" + pre + " AND " + o.exact + ")"
	}
	return pre + " AND (" + o.exact + ")"
}

// through settles which index a question is asked through when its
// condition and the read rule compare more than one indexed column
// (ADR-0075, item 10).
//
// A declared question, the condition of a list read on a document,
// names its index: the first comparison, for equality, of an indexed
// column with something of that document, its pointer or one of its
// variables (item 2). The store is offered that comparison and no other
// on an indexed column, so it has no choice to make.
//
// Any other question that two or more indexes fit is left to whoever
// runs the plan, who looks into each: the plan gains its Fits, the
// state last among them, which is the rule for two looks that end
// alike.
func through(plan *Plan, declared bool, own, rule *offer, rights string, given []any) {
	indexed := 0
	for _, o := range []*offer{own, rule} {
		for i := range o.terms {
			if o.terms[i].indexed {
				indexed++
			}
		}
	}
	if indexed < 2 {
		return
	}
	// asked writes the question so that only one indexed comparison is
	// offered, with every comparison no index answers beside it.
	asked := func(one *bare) (string, []any) {
		keep := func(t *bare) bool { return !t.indexed || t == one }
		var args []any
		where := own.with(keep, &args)
		if r := rule.with(keep, &args); r != "" {
			if where == "" {
				where = r
			} else {
				where = "(" + where + ") AND " + r
			}
		}
		if rights != "" {
			if where == "" {
				where = rights
			} else {
				where += " AND " + rights
			}
		}
		return where, append(args, given...)
	}
	if declared {
		for i := range own.terms {
			if t := &own.terms[i]; t.fits && t.ofDoc {
				plan.Where, plan.Args = asked(t)
				return
			}
		}
	}
	var fits, state []*bare
	for _, o := range []*offer{own, rule} {
		for i := range o.terms {
			switch t := &o.terms[i]; {
			case !t.fits:
			case t.col == "state":
				state = append(state, t)
			default:
				fits = append(fits, t)
			}
		}
	}
	fits = append(fits, state...)
	if len(fits) < 2 {
		return
	}
	plan.Fits = make([]Fit, len(fits))
	for i, t := range fits {
		where, args := asked(t)
		plan.Fits[i] = Fit{Column: t.col, Look: t.sql, Where: where, Args: args}
	}
}

// bound gives the SQL of a slot of a condition read on a document: a
// parameter for a variable of the document and for the pointer to it,
// whose value joins given the first time it is asked for, and what
// column gives for a column of the source.
func (on *On) bound(column func(formula.Slot) string, d formula.Dialect, given *[]any) func(formula.Slot) string {
	params := map[string]string{}
	return func(s formula.Slot) string {
		name, held := strings.CutPrefix(s.Name, formula.ThisSlot)
		if !held && s.Name != formula.RefSlot {
			return column(s)
		}
		key := strings.ToUpper(s.Name)
		if text, ok := params[key]; ok {
			return text
		}
		var val any
		if !held {
			val = on.Ref
		} else if v, ok := on.Value(name); ok {
			val = v.DriverValue()
		} else if base, isCurrency := strings.CutSuffix(name, formula.CurrencySuffix); isCurrency {
			// The currency of a money variable, which the generator asks
			// for beside its amount.
			if v, ok := on.Value(base); ok && v.Kind() == formula.KindMoney {
				val = v.Currency()
			}
		}
		param := fmt.Sprintf("this%d", len(params))
		params[key] = d.Param(param)
		*given = append(*given, sql.Named(param, val))
		return params[key]
	}
}

// holds says a node is THIS.name, a variable of the document a list is
// read on, and that the document holds a value for it: the value is
// handed to the statement, so whether it is blank is known when the
// list is read.
func (on *On) holds(n formula.Node) bool {
	this, ok := n.(*formula.ThisRef)
	if !ok || on.Value == nil {
		return false
	}
	v, ok := on.Value(this.Name)
	return ok && !v.IsBlank() && v.Str() != ""
}

// isRef says a node is REF(), the pointer to the document a list is read
// on, which is never blank.
func isRef(n formula.Node) bool {
	call, ok := n.(*formula.Call)
	return ok && call.Name == "REF" && len(call.Args) == 0
}

// isNamed says a node is the pointer to a named document, as
// REF("Order", "ORD-17"): its id is written out and is not empty, so
// the pointer is never blank.
func isNamed(n formula.Node) bool {
	call, ok := n.(*formula.Call)
	if !ok || call.Name != "REF" || len(call.Args) != 2 {
		return false
	}
	id, ok := call.Args[1].(*formula.TextLit)
	return ok && id.Value != ""
}

// constantOf answers, for an operand of a condition that reads no
// document, the clock or the principal alone, its SQL: a constant of the
// statement, for the prefilter to compare an indexed column with.
func constantOf(src string, scope *formula.Scope, opts formula.BindOptions, d formula.Dialect, quoted func(formula.Slot) string) func(formula.Node) (string, formula.Kind, bool) {
	return func(n formula.Node) (string, formula.Kind, bool) {
		p, err := formula.BindNode(src, n, scope, opts)
		if err != nil || p.ReadsLife || p.Calls("STATE") || p.Calls("ID") {
			return "", 0, false
		}
		for _, s := range p.Slots {
			// A value of the document a list is read on is one value for
			// the whole statement; a column of the source is not.
			if !strings.HasPrefix(s.Name, formula.ThisSlot) && s.Name != formula.RefSlot {
				return "", 0, false
			}
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
}

// readRules keeps the read rule of each class, parsed once, with the
// predicates made of it (ADR-0049, item 5); Forget drops it with the
// class.
var readRules sync.Map // *model.Class → *readRule

// readRule is the read rule of a class: its tree, the INGROUP calls in
// it that the caller alone decides, and the predicate for each outcome of
// those calls.
//
// The rule is not written into the statement as it stands. A call such
// as INGROUP("finance") has one answer for the whole statement, so it is
// answered first and the rule folded around it. For a member of finance,
// owner = USER() OR INGROUP("finance") is TRUE and adds nothing; for
// anybody else it is owner = USER(), a comparison of a column with one
// value, which an index on the column answers. A statement is kept for
// each outcome, not for each caller.
type readRule struct {
	src  string
	node formula.Node
	// groups are the INGROUP calls of a text literal that stand on the
	// logical skeleton of the rule, under AND, OR and NOT alone: at most
	// 64, one bit of an outcome each.
	groups []*formula.Call
	mu     sync.Mutex
	plans  map[readOutcome]*readPlan
}

// readOutcome is what a caller decides of a read rule before any row is
// read: the answers of its INGROUP calls, one bit each, and whether the
// caller has an id, which lets a comparison with USER() be written as
// one with a value that is not blank.
type readOutcome struct {
	groups uint64
	named  bool
	// dialect names the engine the predicate is written for: the store
	// and the analytical engine ask one class in one program, each in
	// its own dialect (ADR-0089, item 7).
	dialect string
}

type readPlan struct {
	// where is the predicate: "" when the rule lets the caller see every
	// document, "0" when it lets them see none. Before the rule itself it
	// carries the rule's comparisons on bare columns, which an index
	// answers, and args are the named parameters of their literals.
	where string
	args  []any
	// plain is the rule without them, for a filter whose own condition
	// leads to an index.
	plain string
	// terms are those comparisons one by one, for a question asked
	// through one index of several (ADR-0075, item 10).
	terms []bare
}

// maxReadPlans bounds the predicates kept for one class.
const maxReadPlans = 256

// readNone is the predicate of a class that has no read rule: nobody but
// a content administrator sees its documents (ADR-0052, item 5).
var readNone = &readPlan{where: "0", plain: "0"}

func readPlanFor(cls *model.Class, scope *formula.Scope, cols map[string]column, sources formula.Sources, d formula.Dialect, quoted func(formula.Slot) string, p Principal) (*readPlan, error) {
	if cls.Access == nil || cls.Access.Read == nil {
		return readNone, nil
	}
	var rr *readRule
	if cached, ok := readRules.Load(cls); ok {
		rr = cached.(*readRule)
	} else {
		src := cls.Def.Access.Read
		node, err := formula.Parse(src)
		if err != nil {
			return nil, &Error{Code: CodeBadFormula, Element: "access/read", Pos: -1, Message: "the read rule of " + cls.Name + " does not read: " + err.Error()}
		}
		rr = &readRule{src: src, node: node, plans: map[readOutcome]*readPlan{}}
		rr.collect(node)
		if len(rr.groups) > 64 {
			rr.groups = nil
		}
		cached, _ := readRules.LoadOrStore(cls, rr)
		rr = cached.(*readRule)
	}
	// The outcome of the caller's part: one bit for each INGROUP call.
	var mask uint64
	for i, call := range rr.groups {
		if p.in([]string{call.Args[0].(*formula.TextLit).Value}) {
			mask |= 1 << i
		}
	}
	key := readOutcome{groups: mask, named: p.ID != "", dialect: d.Name()}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if rp, ok := rr.plans[key]; ok {
		return rp, nil
	}
	f := folder{rr: rr, mask: mask, scope: scope, sources: sources, class: cls.Name, app: cls.App, d: d, quoted: quoted}
	node := f.fold(rr.node)
	rp := &readPlan{}
	if lit, ok := node.(*formula.BoolLit); ok {
		if !lit.Value {
			rp.where, rp.plain = "0", "0"
		}
	} else {
		refused := func(msg string) error {
			return &Error{Code: CodeBadFormula, Element: "access/read", Pos: -1, Message: "the read rule of " + cls.Name + " does not translate to SQL: " + msg}
		}
		// A read rule is the class's own formula: REF() in it is the
		// pointer to the document of the row, and what a database holds
		// runs as it was written.
		opts := formula.BindOptions{Sources: sources, Class: cls.Name, Loose: true, App: cls.App}
		prog, err := formula.BindNode(rr.src, node, scope, opts)
		if err != nil {
			return nil, refused(err.Error())
		}
		stmt, err := formula.GenerateSQLBound(prog, d, formula.SQLBinding{Column: quoted, Row: quoteIdent})
		if err != nil {
			return nil, refused(err.Error())
		}
		// A rule that stops with an error on a row lets nobody see the
		// row, as it is FALSE for a read by id (ADR-0019).
		where := d.Cond(formula.SQLExpr{Val: stmt.Value, Kind: formula.KindBool})
		if stmt.Error != "" {
			where = "(" + stmt.Error + ") IS NULL AND " + where
		}
		rp.plain = "(" + where + ")"
		terms, args := prefilter(node, rr.src, cols, d, constantOf(rr.src, scope, opts, d, quoted), "r", p.named)
		rp.terms = terms
		if pre := (&offer{terms: terms}).bare(everyTerm); pre != "" {
			where = pre + " AND (" + where + ")"
		}
		rp.where, rp.args = "("+where+")", args
	}
	if len(rr.plans) < maxReadPlans {
		rr.plans[key] = rp
	}
	return rp, nil
}

// collect finds the INGROUP calls of a text literal on the logical
// skeleton of the rule: the operands of AND, OR and NOT, from the root.
func (rr *readRule) collect(n formula.Node) {
	switch n := n.(type) {
	case *formula.Binary:
		if n.Op == formula.TokAnd || n.Op == formula.TokOr {
			rr.collect(n.L)
			rr.collect(n.R)
		}
	case *formula.Unary:
		if n.Op == formula.TokNot {
			rr.collect(n.X)
		}
	case *formula.Call:
		if n.Name == "INGROUP" && len(n.Args) == 1 {
			if _, ok := n.Args[0].(*formula.TextLit); ok {
				rr.groups = append(rr.groups, n)
			}
		}
	}
}

// folder folds a read rule around the answers of its INGROUP calls for
// one outcome. It makes new nodes and leaves the rule's tree as parsed.
type folder struct {
	rr      *readRule
	mask    uint64
	scope   *formula.Scope
	sources formula.Sources
	class   string
	app     string
	d       formula.Dialect
	quoted  func(formula.Slot) string
}

// fold answers the INGROUP calls and simplifies AND, OR and NOT around
// them, by the rules the evaluators follow (ADR-0019): AND and OR stop at
// the operand that decides them, left to right, so an operand left of a
// deciding constant is dropped only when it cannot stop with an error,
// and an operand is put in place of an AND or an OR only when it is a
// condition itself.
func (f *folder) fold(n formula.Node) formula.Node {
	switch n := n.(type) {
	case *formula.Call:
		for i, call := range f.rr.groups {
			if call == n {
				return &formula.BoolLit{Value: f.mask&(1<<i) != 0, P: n.P}
			}
		}
	case *formula.Unary:
		if n.Op != formula.TokNot {
			break
		}
		x := f.fold(n.X)
		if lit, ok := x.(*formula.BoolLit); ok {
			return &formula.BoolLit{Value: !lit.Value, P: n.P}
		}
		if x != n.X {
			return &formula.Unary{Op: n.Op, X: x, P: n.P}
		}
	case *formula.Binary:
		if n.Op != formula.TokAnd && n.Op != formula.TokOr {
			break
		}
		l, r := f.fold(n.L), f.fold(n.R)
		and := n.Op == formula.TokAnd
		if lit, ok := l.(*formula.BoolLit); ok {
			if lit.Value != and {
				// FALSE AND x, TRUE OR x: the right side is never asked.
				return &formula.BoolLit{Value: lit.Value, P: n.P}
			}
			// TRUE AND x, FALSE OR x: x decides.
			if isBool, _ := f.probe(r); isBool {
				return r
			}
		} else if lit, ok := r.(*formula.BoolLit); ok {
			isBool, fails := f.probe(l)
			if lit.Value != and && !fails {
				// x AND FALSE, x OR TRUE, where x cannot stop with an error.
				return &formula.BoolLit{Value: lit.Value, P: n.P}
			}
			if lit.Value == and && isBool {
				// x AND TRUE, x OR FALSE: x decides.
				return l
			}
		}
		if l != n.L || r != n.R {
			return &formula.Binary{Op: n.Op, L: l, R: r, P: n.P}
		}
	}
	return n
}

// probe says of an operand whether it is a condition, and whether it may
// stop with an error on some row, which the SQL generator knows: it
// writes an error expression for exactly those.
func (f *folder) probe(n formula.Node) (isBool, fails bool) {
	if _, ok := n.(*formula.BoolLit); ok {
		return true, false
	}
	prog, err := formula.BindNode(f.rr.src, n, f.scope, formula.BindOptions{Sources: f.sources, Class: f.class, Loose: true, App: f.app})
	if err != nil {
		return false, true
	}
	stmt, err := formula.GenerateSQLBound(prog, f.d, formula.SQLBinding{Column: f.quoted, Row: quoteIdent})
	if err != nil {
		return false, true
	}
	return prog.Result.Kind == formula.KindBool, stmt.Error != ""
}

// liveIn refuses a live variable in a filter. A live value is worked out
// when it is read, for one reader, and kept nowhere (ADR-0076), so no
// statement over the stored documents can compare, sort by or answer it.
func liveIn(name, element string) *Error {
	return &Error{Code: CodeNotAllowed, Element: element, Variable: name, Pos: -1,
		Message: name + " is a live variable: it is worked out when it is read and kept nowhere, so a filter cannot read it",
		Hint:    "read it with the command live, for one document; to filter on it, keep it: \"mode\": \"triggered\""}
}

// noLive refuses a formula of a filter that reads a live variable.
func noLive(cls *model.Class, prog *formula.Program, element string) *Error {
	for _, s := range prog.Slots {
		name := s.Name
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if v, ok := cls.Variable(name); ok && v.Live() {
			return liveIn(v.Name, element)
		}
	}
	return nil
}

// indexedColumn says a column of the class has an index: one the class
// keeps by itself, or one a question of another class asks for
// (ADR-0075, item 2).
func indexedColumn(cls *model.Class, index bool, path, variable string) bool {
	return index || path == variable && cls.Asked(variable)
}

// Indexed says what a question compares, a variable of the class or a
// system column, by its name in any case, has an index: the id, the
// state and the two moments of every class, and the columns scopeOf
// says so of.
func Indexed(cls *model.Class, name string) bool {
	switch strings.ToLower(name) {
	case "id", "state", "created_at", "updated_at":
		return true
	}
	for i := range cls.Columns {
		if c := &cls.Columns[i]; strings.EqualFold(c.Path, name) {
			return indexedColumn(cls, c.Index, c.Path, c.Variable)
		}
	}
	return false
}

// scopeOf is what the formulas of a filter over a class bind against: the
// system columns and the class's variables, and the stored column of
// each name a formula may compare.
func scopeOf(cls *model.Class) (*formula.Scope, map[string]column, error) {
	cols := map[string]column{}
	vars := make([]formula.Variable, 0, len(systemColumns)+len(cls.Variables))
	for _, sc := range systemColumns {
		vars = append(vars, sc)
		// The id, and what every class has an index on (ADR-0075, item
		// 2): the state and the two moments.
		cols[strings.ToUpper(sc.Name)] = column{name: sc.Name, typ: sc.Type,
			indexed: sc.Name == "id" || sc.Name == "state" || sc.Name == "created_at" || sc.Name == "updated_at"}
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
		// An index the class keeps by itself, or one a question of
		// another class asks for (ADR-0075, item 2).
		cols[strings.ToUpper(c.Path)] = column{name: c.Name, typ: c.Type, indexed: indexedColumn(cls, c.Index, c.Path, c.Variable)}
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
	prog, err := formula.Bind(src, scope, formula.BindOptions{Sources: sources, Class: cls.Name, Loose: true, App: cls.App})
	if err != nil {
		return "", 0, formulaError(err)
	}
	if e := noLive(cls, prog, "value"); e != nil {
		return "", 0, e
	}
	if len(prog.Folds) > 0 {
		return "", 0, &Error{Code: CodeNotAllowed, Element: "value", Pos: -1, Message: "the formula works a figure out of the lines or tags of a document, which a formula over every row of the class does not do yet (ADR-0081)",
			Hint: "keep the figure in a computed variable and name that variable"}
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

// Kept translates the formula of a variable a document keeps into the
// SQL of its value over the rows of the class's documents, read under
// the alias doc: for the statement that finds the documents whose kept
// value is no longer what the formula gives (ADR-0091, item 6). Unlike
// Value it takes a formula that looks into tables, each a correlated
// subquery over the table that table names. It applies no read right,
// since the statement is the engine's, and binds no parameter of its
// own: the context parameters it lists are the caller's to bind.
//
// It refuses what one statement over a class cannot say: a formula that
// reads the documents of a class or a list the document holds, and one
// that reads the document's life, which is bound for one document at a
// time. Whoever asks then takes every document, and works the value out
// in each.
func Kept(src string, cls *model.Class, sources formula.Sources, doc string, table func(string) string) (formula.SQL, error) {
	scope, cols, err := scopeOf(cls)
	if err != nil {
		return formula.SQL{}, err
	}
	prog, err := formula.Bind(src, scope, formula.BindOptions{Sources: sources, Class: cls.Name, Loose: true, App: cls.App})
	if err != nil {
		return formula.SQL{}, formulaError(err)
	}
	if e := noLive(cls, prog, "value"); e != nil {
		return formula.SQL{}, e
	}
	if len(prog.Folds) > 0 || prog.ReadsLife || prog.HasOp(formula.OpQuery) {
		return formula.SQL{}, &Error{Code: CodeNotAllowed, Element: "value", Pos: -1, Message: "the formula reads a list or the life of one document; it runs over every row of the class"}
	}
	column := quoter(cols)
	stmt, err := formula.GenerateSQLBound(prog, formula.NewSQLite(), formula.SQLBinding{
		Column: func(s formula.Slot) string { return doc + "." + column(s) },
		Table:  table,
		Row:    func(name string) string { return doc + "." + quoteIdent(name) },
	})
	if err != nil {
		return formula.SQL{}, &Error{Code: CodeBadFormula, Element: "value", Pos: -1, Message: err.Error()}
	}
	return stmt, nil
}

// rightsPlans caches, per class, the read rights as the compiler needs
// them (ADR-0041): for every variable with a read rule in some state, per
// state the group list or the SQL of the formula, and the predicate text
// ready when every rule is a formula, since it then depends on the
// principal through parameters alone. A class is compiled once for each
// dialect that asks it, the store's and the analytical engine's
// (ADR-0089, item 7), and lives as long as the store holds it; Forget
// drops a class the store let go.
var rightsPlans sync.Map // *model.Class → *rightsPlan, the first of its dialects

type rightsPlan struct {
	vars []*varRights // the variables with a read rule, in declaration order
	// dialect names the engine the predicates are written for, and next
	// is the plan of the same class for another. Neither changes once
	// the plan is kept.
	dialect string
	next    *rightsPlan
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
	name := d.Name()
	kept, _ := rightsPlans.Load(cls)
	first, _ := kept.(*rightsPlan)
	for p := first; p != nil; p = p.next {
		if p.dialect == name {
			return p
		}
	}
	// Two that compile a class for two dialects at one moment each keep
	// their own, and the later one is kept: the other is made again when
	// it is next asked for.
	plan := &rightsPlan{dialect: name, next: first}
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

// Forget drops the cached rights and read rule of a class the store no
// longer holds.
func Forget(cls *model.Class) {
	rightsPlans.Delete(cls)
	readRules.Delete(cls)
}

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
			if !p.in(sr.groups) {
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

func formulaError(err error) *Error {
	if fe, ok := err.(*formula.Error); ok {
		return &Error{Code: CodeBadFormula, Element: "where", Pos: fe.Pos, Message: fe.Msg}
	}
	return &Error{Code: CodeBadFormula, Element: "where", Pos: -1, Message: err.Error()}
}

// bare is one comparison of a column with a value, which the prefilter
// offers the store beside the exact condition.
type bare struct {
	sql string
	// col is the column compared, as the table names it.
	col string
	// indexed says the column has an index; fits, that the comparison is
	// an equality the index answers by itself, with no blank beside it.
	indexed, fits bool
	// ofDoc says the value is of the document a list is read on: the
	// pointer to it or one of its variables (ADR-0075, item 2).
	ofDoc bool
	// lo and hi bound the parameters of the comparison among the args
	// the prefilter answers.
	lo, hi int
}

// ofDocument says an operand is of the document a list is read on: the
// pointer to it, REF(), or one of its variables, THIS.name.
func ofDocument(n formula.Node) bool {
	if _, this := n.(*formula.ThisRef); this {
		return true
	}
	return isRef(n)
}

// prefilter adds, for each comparison of a column with a literal in the
// top-level AND of the formula, the comparison on the bare column, which
// an index answers. When a blank would satisfy the comparison, the blank
// rows are kept with "OR column IS NULL". Money columns, and literals that
// do not fit the column's scale, are left to the exact predicate.
func prefilter(n formula.Node, src string, cols map[string]column, d formula.Dialect, constant func(formula.Node) (string, formula.Kind, bool), prefix string, notBlank func(formula.Node) bool) (terms []bare, args []any) {
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
			zero := "0"
			switch c.typ.Kind {
			case formula.KindDate, formula.KindDateTime, formula.KindNumber:
			case formula.KindText, formula.KindRef:
				// A text against the caller, owner = USER(), or a pointer
				// against the document a list is read on, order = REF():
				// equality alone, which is exact on the bare column as it
				// is against a literal. A blank on either side reads "".
				if op != "=" {
					return
				}
				zero = "''"
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
			if (c.typ.Kind == formula.KindText || c.typ.Kind == formula.KindRef) && notBlank(expr) {
				// The value is not blank, so no blank column equals it:
				// the bare comparison is exact, and seeks the index as
				// one with a literal does.
				terms = append(terms, bare{sql: quoteIdent(c.name) + " = " + val, col: c.name, indexed: c.indexed, fits: c.indexed, ofDoc: ofDocument(expr), lo: len(args), hi: len(args)})
				return
			}
			val = "COALESCE(" + val + ", " + zero + ")"
			terms = append(terms, bare{sql: "(" + quoteIdent(c.name) + " " + op + " " + val + " OR (" + quoteIdent(c.name) + " IS NULL AND " + zero + " " + op + " " + val + "))",
				col: c.name, indexed: c.indexed, lo: len(args), hi: len(args)})
			return
		}
		if flipped {
			op = flip[op]
		}
		val, c, ok := literalFor(lit, src, col.typ)
		if !ok {
			return
		}
		name := fmt.Sprintf("%s%d", prefix, len(args))
		term := quoteIdent(col.name) + " " + op + " " + d.Param(name)
		blank := zeroSatisfies(op, c)
		if blank {
			term = "(" + term + " OR " + quoteIdent(col.name) + " IS NULL)"
		}
		terms = append(terms, bare{sql: term, col: col.name, indexed: col.indexed, fits: col.indexed && op == "=" && !blank, lo: len(args), hi: len(args) + 1})
		args = append(args, sql.Named(name, val))
	}
	walk(n)
	return terms, args
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
