package formula

import "strings"

// Figures over a list (ADR-0081). A list of a document is a live
// variable whose formula is a QUERY. A function over such a list gives
// one value: how many rows it has, the sum of a column, whether every
// row meets a condition. It compiles to one query slot, as a LOOKUP with
// an aggregate in its column position does (ADR-0046), so that
// the engine of the listed class answers it with one statement and no
// row is read into the VM (ADR-0020). The slot names the list; whoever
// runs the program gives the slot the rows the list has for its reader.
//
// A list is also what the document itself holds (step 2 of the record):
// its lines, a variable of type rows, and its tags, an array. The same
// functions work over it, and compile to a fold slot: the same slot,
// over the elements the document has, which whoever runs the program
// folds where the document is. Such a figure reads its own document
// alone, so it is the same for every reader and stands in any formula.

// figureForm says which aggregate a function over a list is, whether it
// takes a condition and a column, and whether it works over rows alone:
// MEDIAN and COUNTUNIQUE are no functions of single values.
type figureForm struct {
	agg       string
	cond, col bool
	rows      bool
	// extra names the one further argument of a function of step 3: the
	// place of INDEX, the separator of JOIN, the value CONTAINS looks
	// for. It is a value of the document, the same for every row.
	extra string
}

var figureForms = map[string]figureForm{
	"COUNT": {agg: "COUNT"}, "SUM": {agg: "SUM", col: true}, "MIN": {agg: "MIN", col: true}, "MAX": {agg: "MAX", col: true},
	"AVERAGE": {agg: "AVERAGE", col: true},
	"MEDIAN":  {agg: "MEDIAN", col: true, rows: true}, "COUNTUNIQUE": {agg: "COUNTUNIQUE", col: true, rows: true},
	"COUNTIF": {agg: "COUNT", cond: true}, "SUMIF": {agg: "SUM", cond: true, col: true},
	"MINIF": {agg: "MIN", cond: true, col: true}, "MAXIF": {agg: "MAX", cond: true, col: true},
	"AVERAGEIF": {agg: "AVERAGE", cond: true, col: true},
	// One value out of a list (step 3). They work over a list alone.
	"FIRST": {agg: "FIRST", col: true, rows: true}, "LAST": {agg: "LAST", col: true, rows: true},
	"INDEX":    {agg: "INDEX", col: true, rows: true, extra: "position"},
	"JOIN":     {agg: "JOIN", col: true, rows: true, extra: "separator"},
	"CONTAINS": {agg: "EXISTS", col: true, rows: true, extra: "value"},
}

// IsFigureFunction reports whether name is a function that works over
// rows alone, and is no function of single values.
func IsFigureFunction(name string) bool {
	switch name = strings.ToUpper(name); name {
	case "ANY", "ALL":
		return true
	}
	f, ok := figureForms[name]
	return ok && (f.cond || f.rows)
}

// listOf is the name of the list an argument names, as its class
// declares it: a bare name, or the base of list.column. It is "" when
// the argument names no list, and when the formula may read none.
func (b *binder) listOf(n Node) (name string, src *Source) {
	switch n := n.(type) {
	case *Ident:
		name = n.Name
	case *Member:
		name = n.Base
	default:
		return "", nil
	}
	v, declared := b.scope.Lookup(name)
	if b.opts.Lists != nil {
		if src, ok := b.opts.Lists(name); ok {
			if declared {
				name = v.Name
			}
			return name, src
		}
	}
	if declared {
		if src := heldSource(v); src != nil {
			return v.Name, src
		}
	}
	return "", nil
}

// figure binds a function over a list of the document. COUNT, SUM, MIN,
// MAX and AVERAGE are functions of single values too: a call of one is a
// figure when its one argument is a list or a column of one, and handled
// is false otherwise, so that the function itself takes the call.
func (b *binder) figure(n *Call) (t Type, handled bool, err error) {
	if n.Name == "ANY" || n.Name == "ALL" {
		t, err = b.anyAll(n)
		return t, true, err
	}
	form, ok := figureForms[n.Name]
	if !ok {
		return Type{}, false, nil
	}
	if !form.cond && !form.rows {
		if len(n.Args) != 1 {
			return Type{}, false, nil
		}
		if name, _ := b.listOf(n.Args[0]); name == "" && b.listIn(n.Args[0]) == "" && !isShape(n.Args[0]) {
			return Type{}, false, nil
		}
	}
	if form.rows && b.source != nil {
		// The column position of a LOOKUP is bound by the LOOKUP itself;
		// what comes here stands among the arguments of a function over
		// rows, where no other may stand.
		if !rowAggregates[form.agg] {
			return Type{}, true, b.errAt(n.P, "%s works over a list of the document, and may not be used inside the arguments of another function over rows (ADR-0020)", n.Name)
		}
		return Type{}, true, b.errAt(n.P, "%s works over rows, and may not be used inside the arguments of another function over rows (ADR-0020); over the rows of %s it stands in the column position of a LOOKUP alone: LOOKUP(%s, %s(column), condition)", n.Name, b.source.Name, b.source.Name, n.Name)
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
	if form.extra != "" {
		shape = n.Name + "(list.column, " + form.extra + ")"
		want++
	}
	if len(n.Args) != want {
		return Type{}, b.errAt(n.P, "%s over a list is written %s", n.Name, shape)
	}
	// What the functions that give a list made of the list: the figure
	// is then bound over the list itself, and the shape put on its slot.
	sh, err := b.shapeOf(n.Args[0])
	if err != nil {
		return Type{}, err
	}
	if sh.any() {
		shaped := *n
		shaped.Args = append([]Node(nil), n.Args...)
		shaped.Args[0] = sh.base
		n = &shaped
	}
	also := ""
	if form.rows && rowAggregates[form.agg] {
		also = "; over the rows of another class it stands in the column position of a LOOKUP"
	}
	list, src := b.listOf(n.Args[0])
	lifted := false
	if list == "" && !form.cond {
		// The argument written in the words of the document (ADR-0019):
		// an expression of the columns of one list, each list.column.
		if list = b.listIn(n.Args[0]); list != "" {
			list, src = b.listOf(&Ident{Name: list})
			lifted = true
		}
	}
	if list == "" {
		what := "the first argument"
		if !form.cond {
			what = "the argument"
		}
		return Type{}, b.errAt(n.Args[0].Pos(), "%s: %s is a list of the document: its lines or its tags, or a live variable of this class whose formula is a QUERY%s", n.Name, what, also)
	}
	if src.Held && b.opts.Mode == ModePure {
		return Type{}, b.errAt(n.P, "%s over the list %s is not built yet in %s; keep the figure in a computed variable and name that variable", n.Name, list, b.opts.Mode.where())
	}
	if !src.Held && !b.opts.Live {
		return Type{}, b.errAt(n.P, "%s over the list %s counts what its reader may read, so it stands only in a live variable: one that says \"mode\": \"live\". A value that is kept, or that a decision reads, may not depend on who looks (ADR-0081)", n.Name, list)
	}
	if b.source != nil {
		return Type{}, b.errAt(n.P, "a function over a list may not be used inside another's arguments (ADR-0020)")
	}
	// The column: after the dot for a function of one argument, the last
	// argument for an -IF form; COUNT of a list counts its rows.
	// An array has one column, its elements: every function of one
	// argument runs over it, and COUNT counts the elements that have a
	// value, as it does over single values.
	array := src.Held && len(src.Key) == 0
	col, colAt := "", n.P
	var val Node
	switch {
	case lifted:
		val = n.Args[0]
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
			if !src.Held {
				return Type{}, b.errAt(n.Args[2].Pos(), "%s: the column is the name of a column of %s, such as %s", n.Name, list, firstColumn(src))
			}
			// Over a list the document holds, an expression of the row's
			// columns, written as the condition is: qty * price.
			val = n.Args[2]
		}
	case form.cond:
		if _, ok := n.Args[0].(*Ident); !ok {
			return Type{}, b.errAt(n.Args[0].Pos(), "%s counts the rows of a list: %s", n.Name, shape)
		}
	default:
		if m, ok := n.Args[0].(*Member); ok {
			if len(m.Path) != 1 || array {
				return Type{}, b.errAt(m.P, "%s: %s.%s is no column of %s; a column is written %s.%s", n.Name, m.Base, strings.Join(m.Path, "."), list, list, firstColumn(src))
			}
			col, colAt = m.Path[0], m.P
		} else if array {
			col = HeldValue
		} else if form.col {
			return Type{}, b.errAt(n.Args[0].Pos(), "%s takes a column of the list: %s(%s.%s)", n.Name, n.Name, list, firstColumn(src))
		}
	}
	switch {
	case val != nil:
	case col == "" && src.Held:
		// The rows of a list the document holds have no column that
		// every one of them fills: each row counts as one.
		val = &NumberLit{Value: 1, P: int32(n.P)}
	case col == "":
		val = &Ident{Name: src.Key[0], P: n.P}
	default:
		f, ok := src.Column(col)
		if !ok {
			return Type{}, b.errAt(colAt, "the list %s has no column %s; the columns of %s are %s", list, col, src.Name, strings.Join(src.ColumnNames(), ", "))
		}
		val = &Ident{Name: f.Name, P: colAt}
	}
	q := &QuerySlot{Source: src.Name, Order: append([]string(nil), src.Key...)}
	if src.Held {
		q.Order = []string{HeldOrder}
	} else {
		q.List = list
	}
	argOf := map[string]int{}
	var condNode Node = &BoolLit{Value: true, P: n.P}
	if form.agg == "EXISTS" {
		// CONTAINS asks whether some row has the value in the column:
		// whether the column equals it on any row, compared as = compares.
		// The value is the document's, so the comparison is written in the
		// words of the document, and it is what the aggregate runs over.
		var left Node = n.Args[0]
		if !lifted {
			name := HeldValue
			if id, ok := val.(*Ident); ok {
				name = id.Name
			}
			left = &Member{Base: list, Path: []string{name}, P: n.Args[0].Pos()}
		}
		val, lifted = &Binary{Op: TokEq, L: left, R: n.Args[1], P: n.P}, true
	}
	if lifted {
		b.liftNext = list
	}
	err = b.aggregateOver(&Call{Name: form.agg, Args: []Node{val}, P: n.P}, src, q, argOf, true)
	b.liftNext = ""
	if err != nil {
		return Type{}, err
	}
	if err := b.shapeSlot(n, form, sh, list, src, q, argOf); err != nil {
		return Type{}, err
	}
	if form.extra != "" && form.agg != "EXISTS" {
		// The place, or the separator: a value of the document, the same
		// for every row.
		b.liftNext = list
		extra, et, err := b.subProgram(n.Args[1], src, q, argOf)
		b.liftNext = ""
		if err != nil {
			return Type{}, err
		}
		for _, sl := range extra.Slots {
			if !strings.HasPrefix(sl.Name, "THIS.") {
				return Type{}, b.errAt(n.Args[1].Pos(), "%s: the %s is a value of the document, the same for every row; the column %s of %s cannot stand there", n.Name, form.extra, sl.Name, list)
			}
		}
		switch {
		case form.agg == "INDEX" && et.Kind != KindNumber && et.Kind != KindNull:
			return Type{}, b.errAt(n.Args[1].Pos(), "INDEX: the position must be a number, got %s", et)
		case form.agg == "JOIN" && et.Kind != KindText && et.Kind != KindNull:
			return Type{}, b.errAt(n.Args[1].Pos(), "JOIN: the separator must be a text, got %s", et)
		}
		q.Extra = extra
	}
	if form.cond {
		condNode = n.Args[1]
	}
	// FILTER narrows the rows before the function's own condition: a row
	// counts when it meets every one of them.
	for i := len(sh.conds) - 1; i >= 0; i-- {
		if _, plain := condNode.(*BoolLit); plain && !form.cond && i == len(sh.conds)-1 {
			condNode = sh.conds[i]
			continue
		}
		condNode = &Binary{Op: TokAnd, L: sh.conds[i], R: condNode, P: sh.conds[i].Pos()}
	}
	if form.cond || len(sh.conds) > 0 {
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
	if src.Held {
		// A fold reads the document it belongs to and nothing else, as a
		// pure function does.
		b.prog.Folds = append(b.prog.Folds, *q)
		b.emit(OpFold, int32(len(b.prog.Folds)-1), n.P)
		b.push(1)
		return q.Type, nil
	}
	idx := int32(len(b.prog.Queries))
	b.prog.Queries = append(b.prog.Queries, *q)
	b.classes[ClassQuery] = true
	b.emit(OpLookup, idx, n.P)
	b.push(1)
	return q.Type, nil
}

// heldSource is a variable of the document as the source of a function
// over it, when the variable is a list the document holds: the scalar
// fields of rows as its columns, or the elements of an array as the one
// column "value". It is nil for any other variable.
func heldSource(v Variable) *Source {
	switch v.Type.Kind {
	case KindRows:
		// A list that is a QUERY is rows too, and holds nothing: its type
		// names no column.
		if len(v.Type.Fields()) == 0 {
			return nil
		}
		src := &Source{Name: v.Name, Held: true, Key: []string{HeldOrder}}
		for _, f := range v.Type.Fields() {
			if f.Type.IsScalar() {
				src.Columns = append(src.Columns, f)
			}
		}
		return src
	case KindArray:
		return &Source{Name: v.Name, Held: true, Columns: []Field{{Name: HeldValue, Type: Type{Kind: v.Type.Elem, Scale: v.Type.Scale}}}}
	}
	return nil
}

// listIn finds, in an expression, the list whose column it names as
// list.column: the first such list, by the name its class declares. It
// is "" when the expression names the column of no list.
func (b *binder) listIn(n Node) string {
	switch n := n.(type) {
	case *Member:
		if name, src := b.listOf(n); name != "" && len(src.Key) > 0 {
			return name
		}
	case *Unary:
		return b.listIn(n.X)
	case *Binary:
		if name := b.listIn(n.L); name != "" {
			return name
		}
		return b.listIn(n.R)
	case *If:
		for _, x := range []Node{n.Cond, n.Then, n.Else} {
			if name := b.listIn(x); name != "" {
				return name
			}
		}
	case *Call:
		// A function over a list among the arguments is a figure of its
		// own: SUM(lines.cost) + 1 is no expression of the columns.
		if _, figure := figureForms[n.Name]; figure || n.Name == "ANY" || n.Name == "ALL" || n.Name == "LOOKUP" {
			return ""
		}
		for _, a := range n.Args {
			if name := b.listIn(a); name != "" {
				return name
			}
		}
	}
	return ""
}

// liftedName binds a bare name inside an expression of a list's columns
// that is written in the words of the document: a variable of the
// document, the same for every row.
func (b *binder) liftedName(n *Ident) (Type, error) {
	v, ok := b.scope.Lookup(n.Name)
	if !ok {
		if _, isCol := b.source.Column(n.Name); isCol {
			return Type{}, b.errAt(n.P, "unknown variable %s; the column of the list is written %s.%s", n.Name, b.lift, n.Name)
		}
		return Type{}, b.errAt(n.P, "unknown variable %s", n.Name)
	}
	if strings.EqualFold(v.Name, b.lift) {
		return Type{}, b.errAt(n.P, "%s is the whole list; a column of it is written %s.%s", v.Name, v.Name, firstColumn(b.source))
	}
	if !v.Type.IsScalar() {
		return Type{}, b.errAt(n.P, "%s is %s; beside the columns of %s only a single value of the document can be read yet", v.Name, v.Type, b.lift)
	}
	return b.thisRef(&ThisRef{Name: v.Name, P: n.P})
}

// liftedMember binds list.column inside such an expression: a column of
// the row.
func (b *binder) liftedMember(n *Member) (Type, error) {
	if !strings.EqualFold(n.Base, b.lift) {
		if name, _ := b.listOf(n); name != "" {
			return Type{}, b.errAt(n.P, "%s.%s: one function runs over one list, and this one runs over %s", n.Base, strings.Join(n.Path, "."), b.lift)
		}
		return Type{}, b.errAt(n.P, "%s.%s: beside the columns of %s only a single value of the document can be read yet, and no field of an object", n.Base, strings.Join(n.Path, "."), b.lift)
	}
	if len(n.Path) != 1 {
		return Type{}, b.errAt(n.P, "%s.%s is no column of %s; a column is written %s.%s", n.Base, strings.Join(n.Path, "."), b.lift, b.lift, firstColumn(b.source))
	}
	if _, ok := b.source.Column(n.Path[0]); !ok {
		return Type{}, b.errAt(n.P, "the list %s has no column %s; its columns are %s", b.lift, n.Path[0], strings.Join(b.source.ColumnNames(), ", "))
	}
	return b.column(&Ident{Name: n.Path[0], P: n.P})
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

// The functions that give a list again (ADR-0081, step 3): FILTER, SORT,
// TOP and UNIQUE. Such a list stands only as what a function over a
// list works on, and each of them becomes a part of that function's one
// question: FILTER its condition, SORT its order, TOP its limit, UNIQUE
// the different values of its column. They are written from the inside
// out in that order, FILTER nearest the list.
var shapeRank = map[string]int{"FILTER": 1, "SORT": 2, "TOP": 3}

// listShape is what those functions made of a list.
type listShape struct {
	// base is the list itself, or list.column when a column was named
	// after the shaping call or inside UNIQUE.
	base   Node
	conds  []Node // the conditions of FILTER, the outer first
	sort   *List  // the columns of SORT
	top    Node   // the count of TOP
	unique bool
	at     int // where the outermost shaping call stands
}

func (s *listShape) any() bool {
	return len(s.conds) > 0 || s.sort != nil || s.top != nil || s.unique
}

// isShape reports whether a node is written as a list a function gives.
func isShape(n Node) bool {
	switch n := n.(type) {
	case *CallMember:
		return true
	case *Call:
		return shapeRank[n.Name] > 0 || n.Name == "UNIQUE"
	}
	return false
}

// shapeOf reads the first argument of a function over a list: the list,
// or what FILTER, SORT, TOP and UNIQUE made of it.
func (b *binder) shapeOf(n Node) (listShape, error) {
	sh := listShape{base: n, at: n.Pos()}
	col, colAt := "", 0
	if u, ok := n.(*Call); ok && u.Name == "UNIQUE" {
		if len(u.Args) != 1 {
			return sh, b.errAt(u.P, "UNIQUE is written UNIQUE(list.column)")
		}
		if inner, again := u.Args[0].(*Call); again && inner.Name == "UNIQUE" {
			return sh, b.errAt(inner.P, "UNIQUE of the different values is the different values; write it once")
		}
		sh.unique, n = true, u.Args[0]
		sh.base = n
	}
	if cm, ok := n.(*CallMember); ok {
		if shapeRank[cm.Call.Name] == 0 {
			return sh, b.errAt(cm.P, "%s gives no list, and only a list has columns: a column is written after FILTER, SORT or TOP, as FILTER(list, condition).column", cm.Call.Name)
		}
		col, colAt, n = cm.Name, cm.P, cm.Call
	}
	rank := 4
	for {
		c, ok := n.(*Call)
		if !ok || shapeRank[c.Name] == 0 {
			break
		}
		r := shapeRank[c.Name]
		if r > rank || (r == rank && r != 1) {
			return sh, b.errAt(c.P, "%s stands in the wrong place: a list is narrowed, then ordered, then cut, each once but FILTER: TOP(SORT(FILTER(list, condition), {columns}), count)", c.Name)
		}
		rank = r
		if len(c.Args) != 2 {
			return sh, b.errAt(c.P, "%s takes a list and one more argument", c.Name)
		}
		switch c.Name {
		case "FILTER":
			sh.conds = append(sh.conds, c.Args[1])
		case "SORT":
			l, isList := c.Args[1].(*List)
			if !isList {
				return sh, b.errAt(c.Args[1].Pos(), "SORT: the columns stand in braces, a minus before one for descending: SORT(list, {-price, what})")
			}
			sh.sort = l
		case "TOP":
			sh.top = c.Args[1]
		}
		n = c.Args[0]
	}
	if c, ok := n.(*Call); ok && c.Name == "UNIQUE" {
		return sh, b.errAt(c.P, "UNIQUE gives single values, which FILTER, SORT and TOP do not take; write UNIQUE outside them: UNIQUE(TOP(list, 3).column)")
	}
	if !sh.any() {
		return sh, nil
	}
	switch base := n.(type) {
	case *Ident:
		if col != "" {
			n = &Member{Base: base.Name, Path: []string{col}, P: colAt}
		}
	case *Member:
		if col != "" {
			return sh, b.errAt(colAt, "the column is named once: after the list, or after the function that shapes it")
		}
		if !sh.unique {
			return sh, b.errAt(base.P, "FILTER, SORT and TOP take the list itself, %s; its column is written after them, as FILTER(%s, condition).%s", base.Base, base.Base, strings.Join(base.Path, "."))
		}
	default:
		return sh, b.errAt(n.Pos(), "FILTER, SORT, TOP and UNIQUE work on a list of the document: its lines or its tags, or a live variable whose formula is a QUERY")
	}
	if name, _ := b.listOf(n); name == "" {
		return sh, b.errAt(n.Pos(), "FILTER, SORT, TOP and UNIQUE work on a list of the document: its lines or its tags, or a live variable whose formula is a QUERY")
	}
	sh.base = n
	return sh, nil
}

// shapeSlot puts the shape on the slot of the figure: the order of
// SORT, the count of TOP and the different values of UNIQUE. FILTER's
// conditions join the figure's own, where that is bound.
func (b *binder) shapeSlot(n *Call, form figureForm, sh listShape, list string, src *Source, q *QuerySlot, argOf map[string]int) error {
	if sh.unique {
		switch q.Agg {
		case "SUM", "AVERAGE", "MEDIAN":
			return b.errAt(sh.at, "%s of the different values of a column is not built; UNIQUE stands inside COUNT, JOIN, FIRST, LAST, INDEX and CONTAINS", n.Name)
		case "COUNT":
			// how many different values: the aggregate that counts them
			q.Agg = "COUNTUNIQUE"
		case "FIRST", "LAST", "INDEX", "JOIN":
			q.Distinct = true
		}
		if len(q.Value.Slots) == 0 {
			// the figure counts rows and names no column: nothing to tell apart
			return b.errAt(sh.at, "UNIQUE takes a column of the list: UNIQUE(%s.%s)", list, firstColumn(src))
		}
	}
	if sh.sort != nil {
		keys := make([]string, 0, len(sh.sort.Items)+len(q.Order))
		for _, item := range sh.sort.Items {
			desc := false
			if u, ok := item.(*Unary); ok && u.Op == TokMinus {
				desc, item = true, u.X
			}
			id, ok := item.(*Ident)
			if !ok {
				return b.errAt(item.Pos(), "SORT orders by columns of %s, each by its name, a minus before one for descending", list)
			}
			f, has := src.Column(id.Name)
			if !has {
				return b.errAt(id.P, "the list %s has no column %s; the columns of %s are %s", list, id.Name, src.Name, strings.Join(src.ColumnNames(), ", "))
			}
			if desc {
				keys = append(keys, "-"+f.Name)
			} else {
				keys = append(keys, f.Name)
			}
		}
		q.Sorted = len(keys)
		q.Order = append(keys, q.Order...)
	}
	if sh.top != nil {
		b.liftNext = list
		top, tt, err := b.subProgram(sh.top, src, q, argOf)
		b.liftNext = ""
		if err != nil {
			return err
		}
		for _, sl := range top.Slots {
			if !strings.HasPrefix(sl.Name, "THIS.") {
				return b.errAt(sh.top.Pos(), "TOP: the count is a value of the document, the same for every row; the column %s of %s cannot stand there", sl.Name, list)
			}
		}
		if tt.Kind != KindNumber && tt.Kind != KindNull {
			return b.errAt(sh.top.Pos(), "TOP: the count must be a number, got %s", tt)
		}
		q.Limit = top
	}
	return nil
}
