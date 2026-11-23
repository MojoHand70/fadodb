package formula

import "strings"

// Figures over a list (ADR-0081). A list of a document is a live
// variable whose formula is a QUERY. A function over such a list gives
// one value: how many rows it has, the sum of a column, whether every
// row meets a condition. It compiles to one query slot, as a LOOKUP with
// COUNT, SUM, MIN or MAX in its column position does (ADR-0046), so that
// the engine of the listed class answers it with one statement and no
// row is read into the VM (ADR-0020). The slot names the list; whoever
// runs the program gives the slot the rows the list has for its reader.

// figureForm says which aggregate a function over a list is, and
// whether it takes a condition and a column.
type figureForm struct {
	agg       string
	cond, col bool
}

var figureForms = map[string]figureForm{
	"COUNT": {agg: "COUNT"}, "SUM": {agg: "SUM", col: true}, "MIN": {agg: "MIN", col: true}, "MAX": {agg: "MAX", col: true},
	"COUNTIF": {agg: "COUNT", cond: true}, "SUMIF": {agg: "SUM", cond: true, col: true},
	"MINIF": {agg: "MIN", cond: true, col: true}, "MAXIF": {agg: "MAX", cond: true, col: true},
}

// IsFigureFunction reports whether name is a function that works over a
// list alone, and is no function of single values.
func IsFigureFunction(name string) bool {
	switch name = strings.ToUpper(name); name {
	case "ANY", "ALL":
		return true
	}
	f, ok := figureForms[name]
	return ok && f.cond
}

// listOf is the name of the list an argument names, as its class
// declares it: a bare name, or the base of list.column. It is "" when
// the argument names no list, and when the formula may read none.
func (b *binder) listOf(n Node) (name string, src *Source) {
	if b.opts.Lists == nil {
		return "", nil
	}
	switch n := n.(type) {
	case *Ident:
		name = n.Name
	case *Member:
		name = n.Base
	default:
		return "", nil
	}
	src, ok := b.opts.Lists(name)
	if !ok {
		return "", nil
	}
	if v, is := b.scope.Lookup(name); is {
		name = v.Name
	}
	return name, src
}

// figure binds a function over a list of the document. COUNT, SUM, MIN
// and MAX are functions of single values too: a call of one is a figure
// when its one argument is a list or a column of one, and handled is
// false otherwise, so that the function itself takes the call.
func (b *binder) figure(n *Call) (t Type, handled bool, err error) {
	if n.Name == "ANY" || n.Name == "ALL" {
		t, err = b.anyAll(n)
		return t, true, err
	}
	form, ok := figureForms[n.Name]
	if !ok {
		return Type{}, false, nil
	}
	if !form.cond {
		if len(n.Args) != 1 {
			return Type{}, false, nil
		}
		if name, _ := b.listOf(n.Args[0]); name == "" {
			return Type{}, false, nil
		}
	}
	t, err = b.figureSlot(n, form)
	return t, true, err
}

// figureSlot makes the query slot of one figure and the instruction that
// asks for it.
func (b *binder) figureSlot(n *Call, form figureForm) (Type, error) {
	shape := n.Name + "(list"
	if form.cond {
		shape += ", condition"
	}
	if form.col {
		if form.cond {
			shape += ", column)"
		} else {
			shape += ".column)"
		}
	} else {
		shape += ")"
	}
	want := 1
	if form.cond {
		want++
		if form.col {
			want++
		}
	}
	if len(n.Args) != want {
		return Type{}, b.errAt(n.P, "%s over a list is written %s", n.Name, shape)
	}
	if b.opts.Lists == nil {
		return Type{}, b.errAt(n.P, "%s works over a list of the document, and a list is read only by a live variable: one that says \"mode\": \"live\" (ADR-0081)", n.Name)
	}
	list, src := b.listOf(n.Args[0])
	if list == "" {
		return Type{}, b.errAt(n.Args[0].Pos(), "%s: the first argument is a list of the document: a live variable of this class whose formula is a QUERY", n.Name)
	}
	if !b.opts.Live {
		return Type{}, b.errAt(n.P, "%s over the list %s counts what its reader may read, so it stands only in a live variable: one that says \"mode\": \"live\". A value that is kept, or that a decision reads, may not depend on who looks (ADR-0081)", n.Name, list)
	}
	if b.source != nil {
		return Type{}, b.errAt(n.P, "a function over a list may not be used inside another's arguments (ADR-0020)")
	}
	// The column: after the dot for COUNT, SUM, MIN and MAX, the last
	// argument for an -IF form; COUNT of a list counts its rows.
	col, colAt := "", n.P
	switch {
	case form.cond && form.col:
		switch c := n.Args[2].(type) {
		case *Ident:
			col, colAt = c.Name, c.P
		case *Member:
			if base, _ := b.listOf(c); base != list || len(c.Path) != 1 {
				return Type{}, b.errAt(c.P, "%s: the column is a column of %s, such as %s", n.Name, list, firstColumn(src))
			}
			col, colAt = c.Path[0], c.P
		default:
			return Type{}, b.errAt(n.Args[2].Pos(), "%s: the column is the name of a column of %s, such as %s", n.Name, list, firstColumn(src))
		}
	case form.cond:
		if _, ok := n.Args[0].(*Ident); !ok {
			return Type{}, b.errAt(n.Args[0].Pos(), "%s counts the rows of a list: %s", n.Name, shape)
		}
	default:
		if m, ok := n.Args[0].(*Member); ok {
			if len(m.Path) != 1 {
				return Type{}, b.errAt(m.P, "%s: %s.%s is no column of %s; a column is written %s.%s", n.Name, m.Base, strings.Join(m.Path, "."), list, list, firstColumn(src))
			}
			col, colAt = m.Path[0], m.P
		} else if form.col {
			return Type{}, b.errAt(n.Args[0].Pos(), "%s takes a column of the list: %s(%s.%s)", n.Name, n.Name, list, firstColumn(src))
		}
	}
	var val Node
	if col == "" {
		val = &Ident{Name: src.Key[0], P: n.P}
	} else {
		f, ok := src.Column(col)
		if !ok {
			return Type{}, b.errAt(colAt, "the list %s has no column %s; the columns of %s are %s", list, col, src.Name, strings.Join(src.ColumnNames(), ", "))
		}
		val = &Ident{Name: f.Name, P: colAt}
	}
	q := &QuerySlot{Source: src.Name, List: list, Order: append([]string(nil), src.Key...)}
	argOf := map[string]int{}
	if err := b.aggregate(&Call{Name: form.agg, Args: []Node{val}, P: n.P}, src, q, argOf); err != nil {
		return Type{}, err
	}
	var condNode Node = &BoolLit{Value: true, P: n.P}
	if form.cond {
		condNode = n.Args[1]
		if at, fn := ofTheDocument(condNode); fn != "" {
			return Type{}, b.errAt(at, "%s: the condition is asked of each row of %s; %s() is a function of the document the formula belongs to, and is not taken here yet. A column of the row is written by its name, as state and id are", n.Name, list, fn)
		}
	}
	cond, ct, err := b.subProgram(condNode, src, q, argOf)
	if err != nil {
		return Type{}, err
	}
	if !isCondition(ct) {
		return Type{}, b.errAt(condNode.Pos(), "%s: the condition must be a condition, got %s%s", n.Name, ct, conditionHint(ct))
	}
	if cond.ReadsLife || cond.ReadsStates {
		return Type{}, b.errAt(condNode.Pos(), "%s: the condition may not call a function of a document's life yet; compare the columns of the rows of %s", n.Name, list)
	}
	q.Cond = cond
	// The THIS variables become slots of this program, in argument order.
	for _, name := range q.ArgNames {
		v, _ := b.scope.Lookup(name)
		q.Args = append(q.Args, b.slot(v))
	}
	idx := int32(len(b.prog.Queries))
	b.prog.Queries = append(b.prog.Queries, *q)
	b.classes[ClassQuery] = true
	b.emit(OpLookup, idx, n.P)
	b.push(1)
	return q.Type, nil
}

// anyAll binds ANY and ALL as what they are of counts: some row meets
// the condition when COUNTIF is above zero, and every row does when
// COUNTIF counts every row of the list. A row on which the condition is
// blank does not meet it; of a list with no rows ANY is FALSE and ALL
// TRUE.
func (b *binder) anyAll(n *Call) (Type, error) {
	if len(n.Args) != 2 {
		return Type{}, b.errAt(n.P, "%s over a list is written %s(list, condition)", n.Name, n.Name)
	}
	met := &Call{Name: "COUNTIF", Args: n.Args, P: n.P}
	if n.Name == "ANY" {
		return b.expr(&Binary{Op: TokGt, L: met, R: &NumberLit{Value: 0, P: int32(n.P)}, P: n.P})
	}
	return b.expr(&Binary{Op: TokEq, L: met, R: &Call{Name: "COUNT", Args: n.Args[:1], P: n.P}, P: n.P})
}

// ofTheDocument finds, in the condition of a figure, a call of a
// function that reads the document a formula belongs to: its state or
// its id. Inside a figure that would be the document the list stands
// on, where the condition of the list's own QUERY reads the listed
// document; so the call is refused until one meaning is settled.
func ofTheDocument(n Node) (at int, name string) {
	switch n := n.(type) {
	case *Call:
		if (n.Name == "STATE" || n.Name == "ID" || n.Name == "REF") && len(n.Args) == 0 {
			return n.P, n.Name
		}
		for _, a := range n.Args {
			if at, name := ofTheDocument(a); name != "" {
				return at, name
			}
		}
	case *Unary:
		return ofTheDocument(n.X)
	case *Binary:
		if at, name := ofTheDocument(n.L); name != "" {
			return at, name
		}
		return ofTheDocument(n.R)
	case *If:
		for _, x := range []Node{n.Cond, n.Then, n.Else} {
			if at, name := ofTheDocument(x); name != "" {
				return at, name
			}
		}
	}
	return 0, ""
}

// firstColumn is a column of the source to show in an example: the
// first that is no column the engine keeps for every document.
func firstColumn(src *Source) string {
	for _, c := range src.Columns {
		switch c.Name {
		case "id", "title", "state", "version", "class_version", "created_at", "updated_at", "state_entered_at", "created_by", "updated_by", "state_entered_by", "state_entered_from":
			continue
		}
		return c.Name
	}
	if len(src.Columns) > 0 {
		return src.Columns[0].Name
	}
	return "column"
}
