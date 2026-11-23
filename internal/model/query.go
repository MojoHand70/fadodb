package model

import (
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Query is the list a live variable answers (ADR-0076, item 9): a QUERY
// that is the whole formula of the variable. It is one question to the
// documents of a class: which of them, with which columns, in what
// order. Nothing of it is kept; the store asks it when somebody reads
// the variable, for that reader, through what answers a filter
// (ADR-0038), so a reader gets the documents and the values they may
// read.
type Query struct {
	// Source is the class whose documents are listed, as the formula
	// writes it.
	Source string
	// Columns are the columns of a row in the order written: variables
	// of the source and its system columns. A row says the id of its
	// document whether it is listed or not (ADR-0076, item 8).
	Columns []string
	// Cond is the condition, parsed, nil when every document is listed,
	// and Src the formula it was parsed from, which its positions point
	// into. Inside it a bare name is a column of the source, THIS.name a
	// variable of the document the list belongs to, and REF() the
	// pointer to that document.
	Cond formula.Node
	Src  string
	// Order is what the list is sorted by when its reader names no
	// order, as a command writes it: a leading "-" for descending.
	Order []string
	// This are the variables of the document the condition reads as
	// THIS.name, by declared name.
	This []string
}

// FilterColumns are the system columns a filter over a class may name
// beside the class's variables (ADR-0038), and a QUERY may list.
var FilterColumns = []formula.Variable{
	{Name: "id", Type: formula.Scalar(formula.KindText)},
	{Name: "title", Type: formula.Scalar(formula.KindText)},
	{Name: "state", Type: formula.Scalar(formula.KindText)},
	{Name: "version", Type: formula.Scalar(formula.KindNumber)},
	{Name: "class_version", Type: formula.Scalar(formula.KindNumber)},
	{Name: "created_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "updated_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "state_entered_at", Type: formula.Scalar(formula.KindDateTime)},
}

// FilterColumn finds a system column of a filter by name, in any case.
func FilterColumn(name string) (formula.Variable, bool) {
	for _, sc := range FilterColumns {
		if strings.EqualFold(sc.Name, name) {
			return sc, true
		}
	}
	return formula.Variable{}, false
}

// FilterScope is what the where of a filter over the class binds
// against: its system columns and every variable of the class.
func FilterScope(cls *Class) (*formula.Scope, error) {
	vars := make([]formula.Variable, 0, len(FilterColumns)+len(cls.Variables))
	vars = append(vars, FilterColumns...)
	for _, v := range cls.Variables {
		vars = append(vars, formula.Variable{Name: v.Name, Type: v.Type})
	}
	return formula.NewScope(vars...)
}

// Scope is the variables of the class as a formula reads them: what the
// condition of a list reads as THIS.name (ADR-0076, item 9).
func (c *Class) Scope() (*formula.Scope, error) {
	return formula.NewScope(scopeVariables(c.Variables)...)
}

// queryOf reads the formula of a live variable as a list: a QUERY that
// is the whole formula. It answers nil for any other formula, and for
// one that does not parse, which the binder reports in its turn.
func queryOf(src string) *formula.Call {
	node, err := formula.Parse(src)
	if err != nil {
		return nil
	}
	if call, ok := node.(*formula.Call); ok && call.Name == "QUERY" {
		return call
	}
	return nil
}

// at records a finding about a place in the formula at path.
func (c *collector) at(code, path string, n formula.Node, message, hint string) {
	c.list = append(c.list, Diagnostic{Code: code, Path: path, Pos: n.Pos(), Message: message, Hint: hint})
}

// compileQuery checks the QUERY of a live variable and keeps it as the
// question the store asks (ADR-0076, item 9). What it names in the
// source is checked against the source as it is defined now; a
// redefinition of the source that breaks the list is refused as one that
// breaks a LOOKUP is, for the class is noted as a reader of its source.
func compileQuery(c *collector, cls *Class, v *Variable, call *formula.Call, scope *formula.Scope) {
	path := "/variables/" + v.Name + "/formula"
	q := &Query{Src: v.Def.Formula}
	if len(call.Args) > 4 {
		c.at(CodeNotSupported, path, call.Args[4], "grouping and the limit of a QUERY are not built yet (ADR-0076, item 9)",
			"leave them out: whoever reads the list says how many rows they want")
		return
	}
	srcName, ok := call.Args[0].(*formula.Ident)
	if !ok {
		c.at(CodeBadFormula, path, call.Args[0], "QUERY: the source is the name of a class", "")
		return
	}
	q.Source = srcName.Name
	// The columns: one name, or several in braces.
	cols := []formula.Node{call.Args[1]}
	if list, ok := call.Args[1].(*formula.List); ok {
		cols = list.Items
	}
	for _, n := range cols {
		id, ok := n.(*formula.Ident)
		if !ok {
			if _, isCall := n.(*formula.Call); isCall {
				c.at(CodeNotSupported, path, n, "a count or a sum among the columns of a QUERY is not built yet (ADR-0076, item 9)",
					"a single count or sum is a LOOKUP in a triggered variable")
			} else {
				c.at(CodeBadFormula, path, n, "QUERY: a column is the name of a variable of "+q.Source+" or of a system column", "write several in braces: {number, net}")
			}
			return
		}
		q.Columns = append(q.Columns, id.Name)
	}
	if len(call.Args) > 2 {
		q.Cond = call.Args[2]
	}
	// What it sorts by: one name, or several in braces, each with a
	// leading - for descending.
	if len(call.Args) > 3 {
		items := []formula.Node{call.Args[3]}
		if list, ok := call.Args[3].(*formula.List); ok {
			items = list.Items
		}
		for _, n := range items {
			desc := ""
			if u, ok := n.(*formula.Unary); ok && u.Op == formula.TokMinus {
				desc, n = "-", u.X
			}
			id, ok := n.(*formula.Ident)
			if !ok {
				c.at(CodeBadFormula, path, n, "QUERY: what it sorts by is the name of a column, with a leading - for descending", "write several in braces: {-net, number}")
				return
			}
			q.Order = append(q.Order, desc+id.Name)
		}
	}
	for _, name := range thisNames(q.Cond) {
		if c.live[upper(name)] {
			c.formulaErr(path, &liveRead{name: name})
			return
		}
		if hv, ok := cls.Variable(name); ok {
			name = hv.Name
			// What the list reads of its own document, as a page is told
			// of any live variable (ADR-0070).
			v.Deps = append(v.Deps, hv.Name)
		}
		q.This = append(q.This, name)
	}
	v.Query = q
	if c.env.Shallow {
		// A stored definition, compiled alone or as an earlier version: the
		// source is checked when the list is read.
		return
	}
	// The source: a class, this one among them.
	var src *Class
	switch {
	case upper(q.Source) == upper(cls.Name):
		src = cls
	case c.env.Classes != nil:
		src, _ = c.env.Classes(q.Source)
	}
	if src == nil {
		if c.env.Sources != nil {
			if _, table := c.env.Sources.Source(q.Source); table {
				c.at(CodeNotSupported, path, srcName, "QUERY over a table is not built yet; "+q.Source+" is a table (ADR-0076, item 9)", "the source of a QUERY is a class today; LOOKUP reads a table")
				v.Query = nil
				return
			}
		}
		c.at(CodeBadFormula, path, srcName, "unknown source "+q.Source+": QUERY lists the documents of a class", "define the class "+q.Source+" first")
		v.Query = nil
		return
	}
	q.Source = src.Name
	c.read(src.Name)
	bad := false
	for i, name := range q.Columns {
		n := cols[i]
		if sc, ok := FilterColumn(name); ok {
			q.Columns[i] = sc.Name
		} else if sv, ok := src.Variable(name); !ok {
			c.at(CodeBadFormula, path, n, "unknown column "+name+" of "+src.Name+"; its columns are "+strings.Join(queryColumns(src), ", "), "")
			bad = true
		} else if sv.Live() {
			c.at(CodeNotAllowed, path, n, sv.Name+" is a live variable of "+src.Name+": it is worked out when it is read and kept nowhere, so a list cannot show it", "")
			bad = true
		} else {
			q.Columns[i] = sv.Name
		}
		for _, earlier := range q.Columns[:i] {
			if upper(earlier) == upper(name) {
				c.at(CodeDuplicate, path, n, "the column "+name+" is listed twice", "")
				bad = true
			}
		}
	}
	for i, o := range q.Order {
		name := strings.TrimPrefix(o, "-")
		n := call.Args[3]
		if list, ok := n.(*formula.List); ok {
			n = list.Items[i]
		}
		sv, isVar := src.Variable(name)
		_, isSys := FilterColumn(name)
		switch {
		case isVar && sv.Live():
			c.at(CodeNotAllowed, path, n, sv.Name+" is a live variable of "+src.Name+", which is kept nowhere, so a list cannot be sorted by it", "")
			bad = true
		case !isSys && (!isVar || !sv.Type.IsScalar()):
			c.at(CodeBadFormula, path, n, "QUERY cannot sort by "+name+": it is no scalar variable and no system column of "+src.Name, "")
			bad = true
		}
		for _, earlier := range q.Order[:i] {
			if upper(strings.TrimPrefix(earlier, "-")) == upper(name) {
				c.at(CodeDuplicate, path, n, name+" is sorted by twice", "")
				bad = true
			}
		}
	}
	if q.Cond != nil && !checkQueryCond(c, cls, src, q, path, scope) {
		bad = true
	}
	if bad {
		v.Query = nil
	}
}

// checkQueryCond binds the condition of a QUERY over the columns of its
// source, with the variables of the document the list belongs to as
// THIS.name, and holds it to what a filter may be (ADR-0038): a
// condition that looks nothing up and reads nothing of a state's life.
func checkQueryCond(c *collector, cls, src *Class, q *Query, path string, scope *formula.Scope) bool {
	srcScope, err := FilterScope(src)
	if err != nil {
		c.at(CodeBadFormula, path, q.Cond, err.Error(), "")
		return false
	}
	prog, err := formula.BindNode(q.Src, q.Cond, srcScope, formula.BindOptions{Sources: &envSources{c: c}, This: scope})
	if err != nil {
		c.formulaErr(path, err)
		return false
	}
	ok := true
	for _, s := range prog.Slots {
		name := s.Name
		if strings.HasPrefix(name, formula.ThisSlot) || name == formula.RefSlot {
			continue
		}
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if sv, is := src.Variable(name); is && sv.Live() {
			c.at(CodeNotAllowed, path, q.Cond, sv.Name+" is a live variable of "+src.Name+", which is kept nowhere, so the condition of a list cannot read it",
				`keep the value so that it can be read: "mode": "triggered" on `+sv.Name)
			ok = false
		}
	}
	for _, class := range prog.Classes {
		if class == formula.ClassLookup || class == formula.ClassQuery {
			c.at(CodeNotAllowed, path, q.Cond, "the condition of a QUERY looks nothing up: it runs inside the one question the list is", "keep what is looked up in a variable of "+src.Name+", and compare that variable")
			ok = false
		}
	}
	if len(prog.Folds) > 0 {
		c.at(CodeNotAllowed, path, q.Cond, "the condition of a QUERY may not work a figure out of the lines or tags of a document yet: it runs inside the one question the list is (ADR-0081)", "keep the figure in a computed variable of "+src.Name+", and compare that variable")
		ok = false
	}
	if prog.ReadsStates {
		c.at(CodeNotAllowed, path, q.Cond, "the condition of a QUERY may not call a function of a state, which reads the events of one document", "keep the answer in a triggered variable of "+src.Name+", and compare that variable")
		ok = false
	}
	if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
		c.at(CodeTypeMismatch, path, q.Cond, "the condition of a QUERY must be a condition, got "+prog.Result.String(), `compare it, for example <> 0 or <> ""`)
		ok = false
	}
	// A pointer compares with the pointer to this document only when it
	// points at this class: a list of the wrong documents is caught
	// before a document exists (ADR-0076, item 10).
	walkNodes(q.Cond, func(n formula.Node) {
		b, isBinary := n.(*formula.Binary)
		if !isBinary {
			return
		}
		for _, side := range [][2]formula.Node{{b.L, b.R}, {b.R, b.L}} {
			id, isName := side[0].(*formula.Ident)
			ref, isCall := side[1].(*formula.Call)
			if !isName || !isCall || ref.Name != "REF" {
				continue
			}
			if sv, is := src.Variable(id.Name); is && sv.Type.Kind == formula.KindRef && upper(sv.RefClass) != upper(cls.Name) {
				c.at(CodeTypeMismatch, path, b, sv.Name+" points at "+sv.RefClass+", and this document is "+aClass(cls.Name),
					"REF() is the pointer to the document the list belongs to; a list of what points at it compares a ref whose class is "+cls.Name)
				ok = false
			}
		}
	})
	return ok
}

// queryColumns names what a QUERY over the class may list.
func queryColumns(cls *Class) []string {
	var names []string
	for _, sc := range FilterColumns {
		names = append(names, sc.Name)
	}
	for _, v := range cls.Variables {
		if !v.Live() {
			names = append(names, v.Name)
		}
	}
	return names
}

// thisNames lists the variables a condition reads as THIS.name, each
// once, in the order met.
func thisNames(n formula.Node) []string {
	var names []string
	walkNodes(n, func(n formula.Node) {
		t, ok := n.(*formula.ThisRef)
		if !ok {
			return
		}
		for _, have := range names {
			if upper(have) == upper(t.Name) {
				return
			}
		}
		names = append(names, t.Name)
	})
	return names
}

// walkNodes calls visit for a node and every node under it.
func walkNodes(n formula.Node, visit func(formula.Node)) {
	if n == nil {
		return
	}
	visit(n)
	switch n := n.(type) {
	case *formula.Unary:
		walkNodes(n.X, visit)
	case *formula.Binary:
		walkNodes(n.L, visit)
		walkNodes(n.R, visit)
	case *formula.If:
		walkNodes(n.Cond, visit)
		walkNodes(n.Then, visit)
		walkNodes(n.Else, visit)
	case *formula.Call:
		for _, a := range n.Args {
			walkNodes(a, visit)
		}
	case *formula.List:
		for _, item := range n.Items {
			walkNodes(item, visit)
		}
	}
}

// aClass puts "a" or "an" before the name of a class.
func aClass(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}
