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
	// Table says the source is an explicit table and not a class
	// (ADR-0076, item 9): its rows are rows of the table, which are no
	// documents, so they carry no id, and who may read them is the one
	// read line of the table.
	Table bool
	// Order is what the list is sorted by when its reader names no
	// order, as a command writes it: a leading "-" for descending.
	Order []string
	// This are the variables of the document the condition reads as
	// THIS.name, by declared name.
	This []string
	// Totals says the list is one of totals by group (ADR-0090): for
	// each of Columns the total it is, and nil for a plain column, which
	// is then what the lines are grouped by. A line of such a list is no
	// document. It is nil for a list of documents.
	Totals []*Total
}

// Total is a column of a QUERY that is a total over the documents of a
// group (ADR-0090): a function of formula.GroupTotals over one variable
// of the source, or one of its system columns.
type Total struct {
	// Name is what the total stands under in a line: its function and
	// its variable, in lower case with _ between, as sum_net.
	Name string
	// Agg is the function, and Column what it runs over, by the name
	// the source declares.
	Agg, Column string
	// Type is the type of the total, which is the one the function has
	// in a LOOKUP. It is the zero type in a stored definition compiled
	// alone, whose source is not at hand.
	Type formula.Type
}

// totalsIn says whether a QUERY, as it is written, has a function among
// its columns, which makes it a list of totals.
func totalsIn(call *formula.Call) bool {
	if call == nil || len(call.Args) < 2 {
		return false
	}
	cols := []formula.Node{call.Args[1]}
	if list, ok := call.Args[1].(*formula.List); ok {
		cols = list.Items
	}
	for _, n := range cols {
		if _, ok := n.(*formula.Call); ok {
			return true
		}
	}
	return false
}

// totalOf reads a column of a QUERY that is written as a function: a
// total over one variable of the source (ADR-0090, items 1 and 7). It
// answers nil when it has said why the column is none.
func totalOf(c *collector, path string, call *formula.Call, source string) *Total {
	name := strings.ToUpper(call.Name)
	switch {
	case name == "MEDIAN":
		c.at(CodeNotSupported, path, call, "MEDIAN among the columns of a QUERY, the middle value of each group, is not built (ADR-0090)",
			"the middle of all the documents, one number, is a LOOKUP with MEDIAN where its column stands")
		return nil
	case !formula.IsGroupTotal(name):
		c.at(CodeNotAllowed, path, call, call.Name+" cannot stand among the columns of a QUERY: a column is a variable of "+source+", or a total over one, which is "+strings.Join(formula.GroupTotals, ", ")+" (ADR-0090)", "")
		return nil
	case len(call.Args) != 1:
		c.at(CodeBadFormula, path, call, name+" among the columns of a QUERY takes one variable of "+source+", as "+name+"(id)", "")
		return nil
	}
	id, ok := call.Args[0].(*formula.Ident)
	if !ok {
		c.at(CodeNotSupported, path, call.Args[0], "a total among the columns of a QUERY runs over one variable of "+source+", and this is worked out (ADR-0090)",
			"keep the value in a computed variable of "+source+", and total that variable")
		return nil
	}
	return &Total{Name: strings.ToLower(name + "_" + id.Name), Agg: name, Column: id.Name}
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
		c.at(CodeNotSupported, path, call.Args[4], "grouping and the limit of a QUERY are not written as arguments of their own (ADR-0076, item 9; ADR-0090)",
			"leave them out: a total among the columns, as SUM(net), groups the lines by the plain columns, and whoever reads the list says how many rows they want")
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
	// A column that is a total makes the list one of totals by group
	// (ADR-0090): the plain columns then say what its lines are grouped
	// by.
	totals, grouped := make([]*Total, 0, len(cols)), false
	for _, n := range cols {
		switch col := n.(type) {
		case *formula.Ident:
			q.Columns, totals = append(q.Columns, col.Name), append(totals, nil)
		case *formula.Call:
			t := totalOf(c, path, col, q.Source)
			if t == nil {
				return
			}
			q.Columns, totals, grouped = append(q.Columns, t.Name), append(totals, t), true
		default:
			c.at(CodeBadFormula, path, n, "QUERY: a column is the name of a variable of "+q.Source+" or of a system column, or a total over one", "write several in braces: {number, net}")
			return
		}
	}
	if grouped {
		q.Totals = totals
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
			switch by := n.(type) {
			case *formula.Ident:
				q.Order = append(q.Order, desc+by.Name)
			case *formula.Call:
				// A total, written as among the columns, where it must
				// stand too (ADR-0090, item 2).
				t := totalOf(c, path, by, q.Source)
				if t == nil {
					return
				}
				listed := false
				for _, have := range q.Totals {
					listed = listed || have != nil && have.Name == t.Name
				}
				if !listed {
					c.at(CodeNotAllowed, path, by, "the list is sorted by "+t.Agg+"("+t.Column+"), which is none of its columns", "list the total among the columns too")
					return
				}
				q.Order = append(q.Order, desc+t.Name)
			default:
				c.at(CodeBadFormula, path, n, "QUERY: what it sorts by is the name of a column, with a leading - for descending", "write several in braces: {-net, number}")
				return
			}
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
			if tsrc, table := c.env.Sources.Source(q.Source); table && !tsrc.Class {
				if q.Totals != nil {
					c.at(CodeNotSupported, path, srcName, "a total over the rows of a table is not built (ADR-0090): "+q.Source+" is a table", "a single total over a table is a LOOKUP")
					v.Query = nil
					return
				}
				if !compileTableQuery(c, cls, v, q, call, cols, tsrc, path, scope) {
					v.Query = nil
				}
				return
			}
		}
		c.at(CodeBadFormula, path, srcName, "unknown source "+q.Source+": QUERY lists the documents of a class or the rows of a table", "define the class or the table "+q.Source+" first")
		v.Query = nil
		return
	}
	q.Source = src.Name
	c.read(src.Name)
	bad := false
	// rows is the source as a total reads it, for a list of totals.
	var rows *formula.Source
	if q.Totals != nil {
		rows = src.Source()
	}
	for i, name := range q.Columns {
		n := cols[i]
		if q.Totals != nil && q.Totals[i] != nil {
			// A total (ADR-0090): what it runs over is a single value
			// the source keeps, and its type is the one the function has
			// in a LOOKUP.
			t, over := q.Totals[i], n.(*formula.Call).Args[0]
			column := ""
			if sc, ok := FilterColumn(t.Column); ok {
				column = sc.Name
			} else if sv, ok := src.Variable(t.Column); !ok {
				c.at(CodeBadFormula, path, over, "unknown column "+t.Column+" of "+src.Name+"; its columns are "+strings.Join(queryColumns(src), ", "), "")
				bad = true
			} else if sv.Live() {
				c.at(CodeNotAllowed, path, over, sv.Name+" is a live variable of "+src.Name+": it is worked out when it is read and kept nowhere, so a list cannot total it", "")
				bad = true
			} else if !sv.Type.IsScalar() {
				c.at(CodeNotAllowed, path, over, "a total runs over single values, and "+sv.Name+" of "+src.Name+" is "+sv.Type.String(), "")
				bad = true
			} else {
				column = sv.Name
			}
			if column != "" {
				if slot, err := formula.BindTotal(t.Agg, column, rows); err != nil {
					c.at(CodeBadFormula, path, n, err.Error(), "")
					bad = true
				} else {
					t.Column, t.Type = column, slot.Type
				}
			}
			for _, earlier := range q.Columns[:i] {
				if upper(earlier) == upper(name) {
					c.at(CodeDuplicate, path, n, "the total "+t.Agg+"("+t.Column+") stands under "+name+", which is listed twice", "")
					bad = true
				}
			}
			continue
		}
		if sc, ok := FilterColumn(name); ok {
			q.Columns[i] = sc.Name
		} else if sv, ok := src.Variable(name); !ok {
			c.at(CodeBadFormula, path, n, "unknown column "+name+" of "+src.Name+"; its columns are "+strings.Join(queryColumns(src), ", "), "")
			bad = true
		} else if sv.Live() {
			c.at(CodeNotAllowed, path, n, sv.Name+" is a live variable of "+src.Name+": it is worked out when it is read and kept nowhere, so a list cannot show it", "")
			bad = true
		} else if q.Totals != nil && !sv.Type.IsScalar() {
			c.at(CodeNotAllowed, path, n, "a list of totals is grouped by single values, and "+sv.Name+" of "+src.Name+" is "+sv.Type.String(), "")
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
		if q.Totals != nil {
			// A list of totals is sorted by what its lines hold: a group
			// or a total (ADR-0090, item 2).
			held := false
			for _, col := range q.Columns {
				if upper(col) == upper(name) {
					held, q.Order[i] = true, o[:len(o)-len(name)]+col
				}
			}
			if !held {
				c.at(CodeNotAllowed, path, n, "a list of totals is sorted by a group or a total of its own, and "+name+" is neither", "list "+name+" among the columns, or sort by one of "+strings.Join(q.Columns, ", "))
				bad = true
			}
			for _, earlier := range q.Order[:i] {
				if upper(strings.TrimPrefix(earlier, "-")) == upper(name) {
					c.at(CodeDuplicate, path, n, name+" is sorted by twice", "")
					bad = true
				}
			}
			continue
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
		return
	}
	c.asksList(path, src, q.Cond, q.Totals != nil)
}

// compileKept holds the QUERY of a triggered variable to the list the
// variable keeps (ADR-0078, item 5): every column of the QUERY is a
// column of the variable's schema with the type it has in the source,
// and the schema has no other. The store then writes each row of the
// answer into the child table the schema laid out. The question moves
// from Query, which is a live list's, to Kept.
func compileKept(c *collector, cls *Class, v *Variable) {
	q := v.Query
	v.Query = nil
	if q == nil {
		return
	}
	path := "/variables/" + v.Name
	if q.Totals != nil {
		c.hint(CodeNotSupported, path+"/formula", "a list of totals that a triggered variable keeps is not built yet (ADR-0090)",
			`make the variable live, with "mode": "live": the totals are then worked out when they are read`)
		return
	}
	fields := v.Type.Fields()
	if c.env.Shallow {
		// A stored definition compiled alone: the source is not at hand,
		// and the columns were checked when the class was defined.
		v.Kept = q
		return
	}
	// The type of each column in the source: a class, or a table.
	var typeOf func(name string) (formula.Type, bool)
	srcName := q.Source
	if q.Table {
		tsrc, ok := c.env.Sources.Source(q.Source)
		if !ok {
			return // compileQuery has said so
		}
		typeOf = func(name string) (formula.Type, bool) {
			for _, f := range tsrc.Columns {
				if upper(f.Name) == upper(name) {
					return f.Type, true
				}
			}
			return formula.Type{}, false
		}
	} else {
		var cs *Class
		switch {
		case upper(q.Source) == upper(cls.Name):
			cs = cls
		case c.env.Classes != nil:
			cs, _ = c.env.Classes(q.Source)
		}
		if cs == nil {
			return // compileQuery has said so
		}
		typeOf = func(name string) (formula.Type, bool) {
			if sc, ok := FilterColumn(name); ok {
				return sc.Type, true
			}
			if sv, ok := cs.Variable(name); ok {
				return sv.Type, true
			}
			return formula.Type{}, false
		}
	}
	src := struct{ Name string }{srcName}
	bad := false
	listed := map[string]bool{}
	for _, name := range q.Columns {
		listed[upper(name)] = true
		want, known := typeOf(name)
		if !known {
			continue // compileQuery has said so
		}
		if !want.IsScalar() {
			c.hint(CodeNotSupported, path+"/formula", "a list that is kept holds single values, and "+name+" of "+src.Name+" is "+want.String(),
				"list the single values it needs; a list of lists is not kept")
			bad = true
			continue
		}
		if reservedFieldNames[strings.ToLower(name)] {
			c.hint(CodeNotSupported, path+"/formula", "a list that is kept cannot hold the column "+name+" of "+src.Name+": the table of a list has a column of that name for its own use",
				"leave "+name+" out of the QUERY")
			bad = true
			continue
		}
		var field *formula.Field
		for i := range fields {
			if upper(fields[i].Name) == upper(name) {
				field = &fields[i]
			}
		}
		switch {
		case field == nil:
			c.hint(CodeMissing, path+"/schema", "the QUERY lists the column "+name+", and the schema of "+v.Name+" has no such column",
				`add "`+name+`": "`+want.String()+`" to the schema`)
			bad = true
		case field.Type.String() != want.String():
			c.hint(CodeTypeMismatch, path+"/schema", "the column "+field.Name+" is "+field.Type.String()+" in the schema of "+v.Name+", and "+want.String()+" in "+src.Name,
				`write "`+field.Name+`": "`+want.String()+`"`)
			bad = true
		}
	}
	for _, f := range fields {
		if !listed[upper(f.Name)] {
			c.hint(CodeNotAllowed, path+"/schema", "the schema of "+v.Name+" has the column "+f.Name+", which its QUERY does not list",
				"a list that is kept has the columns of its QUERY and no other: list "+f.Name+" in the QUERY, or remove it from the schema")
			bad = true
		}
	}
	if !bad {
		v.Kept = q
	}
}

// compileTableQuery checks a QUERY whose source is an explicit table
// (ADR-0076, item 9): its columns and what it sorts by are columns of
// the table, and its condition reads the columns of the table, the
// variables of the document as THIS.name and the pointer to it as
// REF(). The class is noted as a reader of the table, as for a LOOKUP,
// so that a table a list reads is not taken away under it.
func compileTableQuery(c *collector, cls *Class, v *Variable, q *Query, call *formula.Call, cols []formula.Node, src *formula.Source, path string, scope *formula.Scope) bool {
	q.Source, q.Table = src.Name, true
	column := func(name string) (formula.Field, bool) {
		for _, f := range src.Columns {
			if upper(f.Name) == upper(name) {
				return f, true
			}
		}
		return formula.Field{}, false
	}
	var names []string
	for _, f := range src.Columns {
		names = append(names, f.Name)
	}
	ok := true
	for i, name := range q.Columns {
		n := cols[i]
		if f, has := column(name); !has {
			c.at(CodeBadFormula, path, n, "unknown column "+name+" of the table "+src.Name+"; its columns are "+strings.Join(names, ", "), "")
			ok = false
		} else {
			q.Columns[i] = f.Name
		}
		for _, earlier := range q.Columns[:i] {
			if upper(earlier) == upper(name) {
				c.at(CodeDuplicate, path, n, "the column "+name+" is listed twice", "")
				ok = false
			}
		}
	}
	for i, o := range q.Order {
		name := strings.TrimPrefix(o, "-")
		n := call.Args[3]
		if list, isList := n.(*formula.List); isList {
			n = list.Items[i]
		}
		if f, has := column(name); !has || !f.Type.IsScalar() {
			c.at(CodeBadFormula, path, n, "QUERY cannot sort by "+name+": it is no column of the table "+src.Name, "")
			ok = false
		}
		for _, earlier := range q.Order[:i] {
			if upper(strings.TrimPrefix(earlier, "-")) == upper(name) {
				c.at(CodeDuplicate, path, n, name+" is sorted by twice", "")
				ok = false
			}
		}
	}
	if q.Cond != nil {
		vars := make([]formula.Variable, 0, len(src.Columns))
		for _, f := range src.Columns {
			vars = append(vars, formula.Variable(f))
		}
		srcScope, err := formula.NewScope(vars...)
		if err != nil {
			c.at(CodeBadFormula, path, q.Cond, err.Error(), "")
			return false
		}
		prog, err := formula.BindNode(q.Src, q.Cond, srcScope, formula.BindOptions{Sources: &envSources{c: c}, This: scope, Class: cls.Name, Classes: classNames{c}, Loose: c.env.Lenient})
		if err != nil {
			c.formulaErr(path, err)
			return false
		}
		for _, class := range prog.Classes {
			if class == formula.ClassLookup || class == formula.ClassQuery {
				c.at(CodeNotAllowed, path, q.Cond, "the condition of a QUERY looks nothing up: it runs inside the one question the list is", "keep what is looked up in a variable of the document, and compare with THIS.<variable>")
				ok = false
			}
		}
		if len(prog.Folds) > 0 || prog.ReadsStates {
			c.at(CodeNotAllowed, path, q.Cond, "the condition of a QUERY over a table reads the columns of the table and the values of the document, and nothing of a state's life or of a list", "")
			ok = false
		}
		if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
			c.at(CodeTypeMismatch, path, q.Cond, "the condition of a QUERY must be a condition, got "+prog.Result.String(), `compare it, for example <> 0 or <> ""`)
			ok = false
		}
	}
	if ok {
		// The class reads the table: it is one of its sources.
		found := false
		for _, s := range v.Sources {
			if upper(s) == upper(src.Name) {
				found = true
			}
		}
		if !found {
			v.Sources = append(v.Sources, src.Name)
		}
	}
	return ok
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
	prog, err := formula.BindNode(q.Src, q.Cond, srcScope, formula.BindOptions{Sources: &envSources{c: c}, This: scope, Class: cls.Name, Classes: classNames{c}, Loose: c.env.Lenient})
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
