package formula

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// The binder turns a syntax tree into a Program: it resolves names to
// slots, gives every expression a static type by the rules of ADR-0019 and
// ADR-0022, checks function classes against the mode (ADR-0015), tracks
// the stack depth, and emits bytecode. Everything it rejects is rejected at
// definition time; the VM never sees a type error.

// Mode says where a formula will run, which decides the function classes
// it may use (ADR-0015).
type Mode uint8

// The modes.
const (
	// ModeAny allows every class: defaults, triggered formulas, guards,
	// rules, access rules and layouts.
	ModeAny Mode = iota
	// ModeAuto allows pure functions and LOOKUP only: the stored value of
	// an auto variable must be a function of the document itself.
	ModeAuto
	// ModePure allows pure functions only: a class title (ADR-0018) and a
	// schema's presentation formulas (ADR-0032) are computed from the
	// document's own values and nothing else.
	ModePure
)

func (m Mode) String() string {
	switch m {
	case ModeAuto:
		return "auto"
	case ModePure:
		return "pure"
	}
	return "any"
}

// allows reports whether a formula in the mode may call a function of the
// class.
func (m Mode) allows(c Class) bool {
	switch m {
	case ModeAuto:
		return c == ClassPure || c == ClassLookup
	case ModePure:
		return c == ClassPure
	}
	return true
}

// where names the kind of formula the mode stands for, in a message.
func (m Mode) where() string {
	switch m {
	case ModeAuto:
		return "an auto formula"
	case ModePure:
		return "a title or presentation formula, which allows pure functions only"
	}
	return "this formula"
}

// Variable is one name a formula may read.
type Variable struct {
	Name string
	Type Type
}

// Scope is the set of variables a formula may read. Names resolve
// case-insensitively (ADR-0013).
type Scope struct {
	vars  []Variable
	index map[string]int
}

// NewScope makes a scope. A name declared twice, in any case, is an error.
func NewScope(vars ...Variable) (*Scope, error) {
	s := &Scope{index: make(map[string]int, len(vars))}
	for _, v := range vars {
		key := strings.ToUpper(v.Name)
		if _, dup := s.index[key]; dup {
			return nil, fmt.Errorf("variable %s is declared twice", v.Name)
		}
		if _, kw := keywords[key]; kw {
			return nil, fmt.Errorf("variable %s: %s is a reserved word", v.Name, key)
		}
		if err := checkFields(v.Name, v.Type); err != nil {
			return nil, err
		}
		s.index[key] = len(s.vars)
		s.vars = append(s.vars, v)
	}
	return s, nil
}

// checkFields rejects an object type whose field names collide or are
// reserved words, which the lexer would never read after a dot.
func checkFields(path string, t Type) error {
	if t.Kind != KindObject {
		return nil
	}
	seen := map[string]bool{}
	for _, f := range t.Fields() {
		key := strings.ToUpper(f.Name)
		if seen[key] {
			return fmt.Errorf("%s: field %s is declared twice", path, f.Name)
		}
		seen[key] = true
		if _, kw := keywords[key]; kw {
			return fmt.Errorf("%s.%s: %s is a reserved word", path, f.Name, key)
		}
		if err := checkFields(path+"."+f.Name, f.Type); err != nil {
			return err
		}
	}
	return nil
}

// Lookup finds a variable by name, case-insensitively.
func (s *Scope) Lookup(name string) (Variable, bool) {
	if s == nil {
		return Variable{}, false
	}
	i, ok := s.index[strings.ToUpper(name)]
	if !ok {
		return Variable{}, false
	}
	return s.vars[i], true
}

// BindOptions configures a bind.
type BindOptions struct {
	Mode Mode
	// Sources are the explicit tables LOOKUP may read (ADR-0016); nil
	// means none, and every LOOKUP is an unknown source.
	Sources Sources
	// Secrets are the secrets SECRET may name, as their connection
	// declares them (ADR-0045). nil refuses SECRET, which is so everywhere
	// but in the request of an action definition; an empty list allows it
	// and knows no name.
	Secrets []string
}

// SecretSlot starts the name of the slot a SECRET call reads. The binder
// turns SECRET("token") into the read of a text variable SECRET.token,
// which whoever runs the program fills in when it runs: a secret is never
// a constant of a program, and the VM needs no way to fetch one.
const SecretSlot = "SECRET."

// Bind parses and binds a formula against a scope.
func Bind(src string, scope *Scope, opts BindOptions) (*Program, error) {
	n, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return BindNode(src, n, scope, opts)
}

// BindNode binds a parsed formula. src is the source the tree came from,
// for positions in messages.
func BindNode(src string, n Node, scope *Scope, opts BindOptions) (*Program, error) {
	b := &binder{src: src, scope: scope, opts: opts, prog: &Program{},
		slotOf: map[string]int32{}, numIndex: map[float64]int32{},
		textIndex: map[string]int32{}, classes: map[Class]bool{}}
	t, err := b.expr(n)
	if err != nil {
		return nil, err
	}
	b.emit(OpReturn, 0, n.Pos())
	b.prog.Result = t
	for c := range b.classes {
		b.prog.Classes = append(b.prog.Classes, c)
	}
	sort.Slice(b.prog.Classes, func(i, j int) bool { return classOrder[b.prog.Classes[i]] < classOrder[b.prog.Classes[j]] })
	b.prog.MaxStack = b.maxDepth
	return b.prog, nil
}

var classOrder = map[Class]int{ClassPure: 0, ClassContext: 1, ClassLookup: 2, ClassQuery: 3, ClassSecret: 4}

// maxTries bounds the nesting of IFERROR, which is the VM's handler stack.
const maxTries = 16

type binder struct {
	src   string
	scope *Scope
	opts  BindOptions
	prog  *Program

	slotOf    map[string]int32
	numIndex  map[float64]int32
	decIndex  map[DecConst]int32
	textIndex map[string]int32
	classes   map[Class]bool

	depth, maxDepth int
	tries           int

	// Inside a LOOKUP condition: the source whose columns bare names
	// resolve to, the slot being built, and the argument positions by
	// variable. The scope is the enclosing formula's, which THIS.name
	// reads. No pointer back to the enclosing binder: that would move it
	// to the heap on every bind.
	source *Source
	q      *QuerySlot
	argOf  map[string]int

	// lit describes the last constant expr emitted, so that an operation
	// can make its operand exact when the other operand is (ADR-0022);
	// operand checks that it is the operand's own.
	lit litInfo
}

// litInfo marks a constant expression that compiled to one const.num
// instruction: where it is. Its exact value is read from the expression
// on demand.
type litInfo struct {
	at int32
	ok bool
}

// foldLiteral recognises a constant expression, number literals combined
// with unary minus, percent, +, - and *, and folds it to the float the VM
// would compute, operation for operation. A fold whose float is not finite
// is left to the VM, which reports it.
func foldLiteral(n Node) (float64, bool) {
	switch n := n.(type) {
	case *NumberLit:
		return n.Value, true
	case *Unary:
		if !n.Const {
			return 0, false
		}
		x, ok := foldLiteral(n.X)
		if !ok {
			return 0, false
		}
		switch n.Op {
		case TokMinus:
			return -x, true
		case TokPercent:
			return x / 100, true
		}
	case *Binary:
		if !n.Const {
			return 0, false
		}
		l, ok := foldLiteral(n.L)
		if !ok {
			return 0, false
		}
		r, ok := foldLiteral(n.R)
		if !ok {
			return 0, false
		}
		var f float64
		switch n.Op {
		case TokPlus:
			f = l + r
		case TokMinus:
			f = l - r
		default:
			f = l * r
		}
		if notFinite(f) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// exactConst reads a constant expression exactly from its spellings in
// src (ADR-0022): a mantissa and a scale, or false when it does not fit 18
// digits.
func exactConst(src string, n Node) (int64, int, bool) {
	switch n := n.(type) {
	case *NumberLit:
		m, s, e := parseDecimal(n.Text(src))
		return m, s, e == nil
	case *Unary:
		if !n.Const {
			return 0, 0, false
		}
		m, s, ok := exactConst(src, n.X)
		if !ok {
			return 0, 0, false
		}
		switch n.Op {
		case TokMinus:
			return -m, s, m != math.MinInt64
		case TokPercent:
			return m, s + 2, s+2 <= MaxScale
		}
	case *Binary:
		if !n.Const {
			return 0, 0, false
		}
		l, sl, ok := exactConst(src, n.L)
		if !ok {
			return 0, 0, false
		}
		r, sr, ok := exactConst(src, n.R)
		if !ok {
			return 0, 0, false
		}
		switch n.Op {
		case TokPlus, TokMinus:
			x, y, s, ok := alignScales(l, sl, r, sr)
			if n.Op == TokMinus {
				ok = ok && y != math.MinInt64
				y = -y
			}
			if !ok {
				return 0, 0, false
			}
			m, ok := addInt(x, y)
			return m, s, ok
		case TokStar:
			m, ok := mulInt(l, r)
			return m, sl + sr, ok && sl+sr <= MaxScale
		}
	}
	return 0, 0, false
}

func (b *binder) errAt(pos int, format string, args ...any) error {
	return newError(b.src, pos, format, args...)
}

func (b *binder) emit(op Op, arg int32, pos int) int {
	b.prog.Code = append(b.prog.Code, Instr{Op: op, Arg: arg})
	b.prog.Pos = append(b.prog.Pos, int32(pos))
	return len(b.prog.Code) - 1
}

func (b *binder) patch(at int, target int) {
	b.prog.Code[at].Arg = int32(target)
}

func (b *binder) here() int { return len(b.prog.Code) }

func (b *binder) push(n int) {
	b.depth += n
	if b.depth > b.maxDepth {
		b.maxDepth = b.depth
	}
}

func (b *binder) pop(n int) { b.depth -= n }

func (b *binder) num(f float64) int32 {
	if i, ok := b.numIndex[f]; ok {
		return i
	}
	i := int32(len(b.prog.Nums))
	b.prog.Nums = append(b.prog.Nums, f)
	b.numIndex[f] = i
	return i
}

func (b *binder) dec(c DecConst) int32 {
	if b.decIndex == nil {
		b.decIndex = map[DecConst]int32{}
	}
	if i, ok := b.decIndex[c]; ok {
		return i
	}
	i := int32(len(b.prog.Decs))
	b.prog.Decs = append(b.prog.Decs, c)
	b.decIndex[c] = i
	return i
}

func (b *binder) text(s string) int32 {
	if i, ok := b.textIndex[s]; ok {
		return i
	}
	i := int32(len(b.prog.Texts))
	b.prog.Texts = append(b.prog.Texts, s)
	b.textIndex[s] = i
	return i
}

func (b *binder) slot(v Variable) int32 {
	key := strings.ToUpper(v.Name)
	if i, ok := b.slotOf[key]; ok {
		return i
	}
	i := int32(len(b.prog.Slots))
	b.prog.Slots = append(b.prog.Slots, Slot(v))
	b.slotOf[key] = i
	return i
}

// Type predicates of ADR-0019 and ADR-0022. Blank fits everywhere; a bool
// reads as 1 or 0 in either number family.

func isNumeric(t Type) bool      { return t.Kind == KindNumber || t.Kind == KindBool || t.Kind == KindNull }
func isExactOperand(t Type) bool { return t.Kind.IsExact() || t.Kind == KindBool || t.Kind == KindNull }
func isAnyNumeric(t Type) bool   { return isNumeric(t) || t.Kind.IsExact() }
func isCondition(t Type) bool    { return t.Kind == KindBool || t.Kind == KindNull }
func isTextual(t Type) bool      { return t.Kind == KindText || t.Kind == KindNull }
func isDateT(t Type) bool        { return t.Kind == KindDate || t.Kind == KindNull }
func isDateTimeT(t Type) bool    { return t.Kind == KindDateTime || t.Kind == KindNull }
func isDateLike(t Type) bool     { return t.Kind.IsDateLike() || t.Kind == KindNull }
func isMoneyT(t Type) bool       { return t.Kind == KindMoney || t.Kind == KindNull }
func isJSONT(t Type) bool        { return t.Kind == KindJSON || t.Kind == KindNull }

// comparable kinds: everything scalar of one kind, json as text too
// (ADR-0013); exact values of any scale compare with each other, money
// with a decimal by its amount.
func comparable(a, b Type) bool {
	if !a.IsScalar() || !b.IsScalar() {
		return false
	}
	return a.Kind == b.Kind || a.Kind == KindNull || b.Kind == KindNull || a.Kind.IsExact() && b.Kind.IsExact()
}

// join is the type of two branches that must agree: the same type, the
// non-blank one when one side is blank, or two exact values of one kind at
// the larger scale.
func join(a, b Type) (Type, bool) {
	switch {
	case a.Kind == KindNull:
		return b, true
	case b.Kind == KindNull:
		return a, true
	case a.Kind == b.Kind && a.Kind.IsExact():
		if b.Scale > a.Scale {
			return b, true
		}
		return a, true
	case a.Equal(b):
		return a, true
	}
	return Type{}, false
}

func (b *binder) expr(n Node) (Type, error) {
	if f, ok := foldLiteral(n); ok {
		at := b.emit(OpConstNum, b.num(f), n.Pos())
		b.push(1)
		b.lit = litInfo{at: int32(at), ok: true}
		return Scalar(KindNumber), nil
	}
	switch n := n.(type) {
	case *TextLit:
		b.emit(OpConstText, b.text(n.Value), n.P)
		b.push(1)
		return Scalar(KindText), nil
	case *BoolLit:
		if n.Value {
			b.emit(OpTrue, 0, n.P)
		} else {
			b.emit(OpFalse, 0, n.P)
		}
		b.push(1)
		return Scalar(KindBool), nil
	case *BlankLit:
		b.emit(OpBlank, 0, n.P)
		b.push(1)
		return Scalar(KindNull), nil
	case *Ident:
		if b.source != nil {
			return b.column(n)
		}
		v, ok := b.scope.Lookup(n.Name)
		if !ok {
			if fn := LookupFunction(n.Name); fn != nil {
				return Type{}, b.errAt(n.P, "unknown variable %s; %s is a function: write %s", n.Name, fn.Name, fn.Signature(strings.ToUpper(n.Name)))
			}
			return Type{}, b.errAt(n.P, "unknown variable %s", n.Name)
		}
		if v.Type.Kind == KindObject {
			return Type{}, b.errAt(n.P, "%s is an %s; read one of its fields, %s.%s; whole objects are not supported yet", v.Name, v.Type, v.Name, strings.Join(v.Type.FieldNames(), ", "+v.Name+"."))
		}
		if !v.Type.IsScalar() {
			return Type{}, b.errAt(n.P, "%s is %s; composite values are not supported yet", v.Name, v.Type)
		}
		b.emit(OpLoad, b.slot(v), n.P)
		b.push(1)
		return v.Type, nil
	case *Member:
		if b.source != nil {
			return Type{}, b.errAt(n.P, "inside LOOKUP a bare name is a column of %s; the document's variables are THIS.name, and their fields are not reachable yet", b.source.Name)
		}
		return b.member(n)
	case *ThisRef:
		if b.source != nil {
			return b.thisRef(n)
		}
		return Type{}, b.errAt(n.P, "THIS.%s is only valid inside LOOKUP and QUERY", n.Name)
	case *Unary:
		return b.unary(n)
	case *Binary:
		return b.binary(n)
	case *If:
		return b.ifExpr(n)
	case *Call:
		return b.call(n)
	}
	return Type{}, b.errAt(n.Pos(), "unexpected node %T", n)
}

// member binds base.path: a field of an object variable, through object
// fields to a scalar leaf, which is one slot named by the declared path
// (ADR-0031); or a column of a rows variable, which composites will bring.
func (b *binder) member(n *Member) (Type, error) {
	v, ok := b.scope.Lookup(n.Base)
	if !ok {
		return Type{}, b.errAt(n.P, "unknown variable %s", n.Base)
	}
	switch v.Type.Kind {
	case KindObject:
		t, name := v.Type, v.Name
		for i, step := range n.Path {
			if t.Kind != KindObject {
				return Type{}, b.errAt(n.P, "%s is %s and has no field %s", name, t, step)
			}
			f, ok := t.Field(step)
			if !ok {
				return Type{}, b.errAt(n.P, "%s has no field %s; its fields are %s", name, step, strings.Join(t.FieldNames(), ", "))
			}
			name += "." + f.Name
			t = f.Type
			if i == len(n.Path)-1 && t.Kind == KindObject {
				return Type{}, b.errAt(n.P, "%s is an %s; read one of its fields, %s.%s; whole objects are not supported yet", name, t, name, strings.Join(t.FieldNames(), ", "+name+"."))
			}
		}
		b.emit(OpLoad, b.slot(Variable{Name: name, Type: t}), n.P)
		b.push(1)
		return t, nil
	case KindRows:
		return Type{}, b.errAt(n.P, "%s.%s: composite values are not supported yet", n.Base, strings.Join(n.Path, "."))
	}
	return Type{}, b.errAt(n.P, "%s is %s; only an object has fields and only a rows variable has columns", v.Name, v.Type)
}

// operand binds an expression and reports the constant it was, if any: a
// constant compiles to exactly one instruction, the one b.lit names, so a
// constant left behind by a sub-expression is told apart by its place.
func (b *binder) operand(n Node) (Type, litInfo, error) {
	start := b.here()
	t, err := b.expr(n)
	lit := b.lit
	if !lit.ok || int(lit.at) != start || b.here() != start+1 {
		lit = litInfo{}
	}
	return t, lit, err
}

// exactLit rewrites the constant's instruction into an exact constant read
// from its spelling, and returns its exact type.
func (b *binder) exactLit(lit litInfo, node Node) (Type, error) {
	m, s, ok := exactConst(b.src, node)
	if !ok {
		return Type{}, b.errAt(node.Pos(), "the constant %s cannot be exact: at most %d digits after the point and %d digits in all", renderNumber(b.prog.Nums[b.prog.Code[lit.at].Arg]), MaxScale, MaxScale)
	}
	b.prog.Code[lit.at] = Instr{Op: OpConstDec, Arg: b.dec(DecConst{Mant: m, Scale: int8(s)})}
	return DecimalType(s), nil
}

// claim makes a number literal exact when the other operand is exact, and
// leaves every other type as it is.
func (b *binder) claim(t Type, lit litInfo, node Node, other Type) (Type, error) {
	if other.IsExact() && !t.IsExact() && lit.ok {
		return b.exactLit(lit, node)
	}
	return t, nil
}

func (b *binder) unary(n *Unary) (Type, error) {
	t, err := b.expr(n.X)
	if err != nil {
		return Type{}, err
	}
	switch n.Op {
	case TokMinus, TokPercent:
		if t.IsExact() {
			s := int(t.Scale)
			op := OpDecNeg
			if n.Op == TokPercent {
				op = OpDecPercent
				s += 2
				if s > MaxScale {
					return Type{}, b.errAt(n.P, "%% would need scale %d, at most %d; round first", s, MaxScale)
				}
			}
			rt := Type{Kind: t.Kind, Scale: int8(s)}
			b.emit(op, decArg(rt), n.P)
			return rt, nil
		}
		if !isNumeric(t) {
			return Type{}, b.errAt(n.P, "%s needs a number, got %s", opText[n.Op], t)
		}
		if n.Op == TokMinus {
			b.emit(OpNeg, 0, n.P)
		} else {
			b.emit(OpPercent, 0, n.P)
		}
		return Scalar(KindNumber), nil
	case TokNot:
		if !isCondition(t) {
			return Type{}, b.errAt(n.P, "NOT needs a condition, got %s%s", t, conditionHint(t))
		}
		b.emit(OpNot, 0, n.P)
		return Scalar(KindBool), nil
	}
	return Type{}, b.errAt(n.P, "unexpected unary operator")
}

func conditionHint(t Type) string {
	if t.Kind == KindNumber || t.Kind.IsExact() {
		return "; compare it, for example <> 0"
	}
	return ""
}

var arithmeticOps = map[TokenKind]Op{
	TokPlus: OpAdd, TokMinus: OpSub, TokStar: OpMul, TokSlash: OpDiv, TokBackslash: OpDiv, TokCaret: OpPow,
}

var exactOps = map[TokenKind]Op{
	TokPlus: OpDecAdd, TokMinus: OpDecSub, TokStar: OpDecMul, TokSlash: OpDecDiv, TokBackslash: OpDecDiv,
}

var comparisonOps = map[TokenKind]Op{
	TokEq: OpEq, TokNe: OpNe, TokLt: OpLt, TokLe: OpLe, TokGt: OpGt, TokGe: OpGe,
}

func (b *binder) binary(n *Binary) (Type, error) {
	switch n.Op {
	case TokAnd, TokOr:
		return b.logical(n)
	}
	lt, llit, err := b.operand(n.L)
	if err != nil {
		return Type{}, err
	}
	rt, rlit, err := b.operand(n.R)
	if err != nil {
		return Type{}, err
	}
	b.pop(1)
	if op, ok := arithmeticOps[n.Op]; ok {
		if lt.IsExact() || rt.IsExact() {
			return b.exactArith(n, lt, llit, rt, rlit)
		}
		if !isNumeric(lt) || !isNumeric(rt) {
			bad := rt
			if !isNumeric(lt) {
				bad = lt
			}
			return Type{}, b.errAt(n.P, "%s needs numbers, got %s%s", opText[n.Op], bad, arithmeticHint(bad))
		}
		b.emit(op, 0, n.P)
		return Scalar(KindNumber), nil
	}
	if n.Op == TokAmp {
		if !lt.IsScalar() || !rt.IsScalar() {
			return Type{}, b.errAt(n.P, "& needs scalar values")
		}
		b.emit(OpConcat, 0, n.P)
		return Scalar(KindText), nil
	}
	if op, ok := comparisonOps[n.Op]; ok {
		if lt, err = b.claim(lt, llit, n.L, rt); err != nil {
			return Type{}, err
		}
		if rt, err = b.claim(rt, rlit, n.R, lt); err != nil {
			return Type{}, err
		}
		if !comparable(lt, rt) {
			return Type{}, b.errAt(n.P, "%s compares %s with %s; convert one side first%s", opText[n.Op], lt, rt, compareHint(lt, rt))
		}
		// Exact operands compare at the larger static scale, which the
		// instruction carries so that a blank operand aligns like a value.
		var arg int32
		if lt.IsExact() || rt.IsExact() {
			arg = int32(max(lt.Scale, rt.Scale))
		}
		b.emit(op, arg, n.P)
		return Scalar(KindBool), nil
	}
	return Type{}, b.errAt(n.P, "unexpected operator")
}

// exactArith types and emits an arithmetic operation with an exact
// operand (ADR-0022): the other side's literal becomes exact, a number
// variable is refused, money follows the currency rules, and the result
// scale is the operation's.
func (b *binder) exactArith(n *Binary, lt Type, llit litInfo, rt Type, rlit litInfo) (Type, error) {
	var err error
	if lt, err = b.claim(lt, llit, n.L, rt); err != nil {
		return Type{}, err
	}
	if rt, err = b.claim(rt, rlit, n.R, lt); err != nil {
		return Type{}, err
	}
	for _, t := range []Type{lt, rt} {
		if !isExactOperand(t) {
			return Type{}, b.errAt(n.P, "%s needs numbers of one kind, got %s and %s%s", opText[n.Op], lt, rt, arithmeticHint(t))
		}
	}
	if n.Op == TokCaret {
		return Type{}, b.errAt(n.P, "^ needs numbers, got %s; NUMBER converts an exact value", lt)
	}
	t, msg := exactResult(n.Op, lt, rt)
	if msg != "" {
		return Type{}, b.errAt(n.P, "%s", msg)
	}
	b.emit(exactOps[n.Op], decArg(t), n.P)
	return t, nil
}

// exactResult is the type of an exact operation: + and - at the larger
// scale, * at the sum of the scales, / at the dividend's scale plus
// DivScaleStep. Money plus or minus money or a decimal is money; money
// times a decimal is money; money divided by money is a decimal and by a
// decimal is money; money times money and a decimal divided by money are
// refused.
func exactResult(op TokenKind, lt, rt Type) (Type, string) {
	sa, sb := int(lt.Scale), int(rt.Scale)
	ma, mb := lt.Kind == KindMoney, rt.Kind == KindMoney
	kind := KindDecimal
	if ma || mb {
		kind = KindMoney
	}
	switch op {
	case TokPlus, TokMinus:
		return Type{Kind: kind, Scale: int8(max(sa, sb))}, ""
	case TokStar:
		if ma && mb {
			return Type{}, "* cannot multiply money by money"
		}
		if sa+sb > MaxScale {
			return Type{}, fmt.Sprintf("* would need scale %d, at most %d; round an operand first", sa+sb, MaxScale)
		}
		return Type{Kind: kind, Scale: int8(sa + sb)}, ""
	}
	if mb && !ma {
		return Type{}, "/ cannot divide a decimal by money"
	}
	if ma && mb {
		kind = KindDecimal
	}
	return Type{Kind: kind, Scale: int8(divScale(sa))}, ""
}

func arithmeticHint(t Type) string {
	switch t.Kind {
	case KindText:
		return "; VALUE converts text to a number"
	case KindDate, KindDateTime:
		return "; dates move with ADDDAYS, ADDMONTHS and ADDYEARS, and DAYS measures them"
	case KindNumber:
		return "; DECIMAL(x, places) makes a number exact"
	case KindDecimal, KindMoney:
		return "; NUMBER converts an exact value"
	}
	return ""
}

func compareHint(a, b Type) string {
	switch {
	case a.Kind == KindDate && b.Kind == KindDateTime || a.Kind == KindDateTime && b.Kind == KindDate:
		return "; DATEOF turns a datetime into a date"
	case a.Kind == KindMoney && b.Kind == KindDecimal || a.Kind == KindDecimal && b.Kind == KindMoney:
		return "; AMOUNT reads the amount of a money"
	case a.Kind.IsExact() && b.Kind == KindNumber || a.Kind == KindNumber && b.Kind.IsExact():
		return "; DECIMAL(x, places) makes a number exact"
	}
	return ""
}

// logical emits AND and OR with short-circuit evaluation: the right
// operand runs only when the left one has not decided the result.
func (b *binder) logical(n *Binary) (Type, error) {
	lt, err := b.expr(n.L)
	if err != nil {
		return Type{}, err
	}
	if !isCondition(lt) {
		return Type{}, b.errAt(n.P, "%s needs conditions, got %s%s", opText[n.Op], lt, conditionHint(lt))
	}
	op := OpAndJump
	if n.Op == TokOr {
		op = OpOrJump
	}
	jump := b.emit(op, 0, n.P)
	b.pop(1)
	rt, err := b.expr(n.R)
	if err != nil {
		return Type{}, err
	}
	if !isCondition(rt) {
		return Type{}, b.errAt(n.P, "%s needs conditions, got %s%s", opText[n.Op], rt, conditionHint(rt))
	}
	b.emit(OpToBool, 0, n.P)
	b.patch(jump, b.here())
	return Scalar(KindBool), nil
}

func (b *binder) ifExpr(n *If) (Type, error) {
	ct, err := b.expr(n.Cond)
	if err != nil {
		return Type{}, err
	}
	if !isCondition(ct) {
		return Type{}, b.errAt(n.Cond.Pos(), "IF needs a condition, got %s%s", ct, conditionHint(ct))
	}
	toElse := b.emit(OpJumpIfFalse, 0, n.P)
	b.pop(1)
	base := b.depth
	tt, tlit, err := b.operand(n.Then)
	if err != nil {
		return Type{}, err
	}
	toEnd := b.emit(OpJump, 0, n.P)
	b.depth = base
	b.patch(toElse, b.here())
	et, elit, err := b.operand(n.Else)
	if err != nil {
		return Type{}, err
	}
	b.patch(toEnd, b.here())
	return b.branches("IF", n.P, tt, tlit, n.Then, et, elit, n.Else)
}

// settle brings an exact result of two alternatives to the joined scale and
// kind at run time, since the branch taken carries its own.
func (b *binder) settle(t Type, pos int) Type {
	if t.IsExact() {
		b.emit(OpDecScale, decArg(t), pos)
	}
	return t
}

// branches joins the types of two alternatives, making a literal exact
// when the other alternative is.
func (b *binder) branches(what string, pos int, tt Type, tlit litInfo, tn Node, et Type, elit litInfo, en Node) (Type, error) {
	var err error
	if tt, err = b.claim(tt, tlit, tn, et); err != nil {
		return Type{}, err
	}
	if et, err = b.claim(et, elit, en, tt); err != nil {
		return Type{}, err
	}
	t, ok := join(tt, et)
	if !ok {
		return Type{}, b.errAt(pos, "%s branches must have the same type, got %s and %s", what, tt, et)
	}
	if !t.IsScalar() {
		return Type{}, b.errAt(pos, "composite values are not supported yet")
	}
	return b.settle(t, pos), nil
}

// ifError emits IFERROR(value, fallback): the value runs under a handler,
// and an error in it discards what it left on the stack and evaluates the
// fallback instead (ADR-0024).
func (b *binder) ifError(n *Call) (Type, error) {
	if b.tries >= maxTries {
		return Type{}, b.errAt(n.P, "IFERROR nested more than %d deep", maxTries)
	}
	b.tries++
	try := b.emit(OpTry, 0, n.P)
	base := b.depth
	vt, vlit, err := b.operand(n.Args[0])
	if err != nil {
		return Type{}, err
	}
	b.emit(OpEndTry, 0, n.P)
	toEnd := b.emit(OpJump, 0, n.P)
	b.depth = base
	b.patch(try, b.here())
	ft, flit, err := b.operand(n.Args[1])
	if err != nil {
		return Type{}, err
	}
	b.patch(toEnd, b.here())
	b.tries--
	return b.branches("IFERROR", n.P, vt, vlit, n.Args[0], ft, flit, n.Args[1])
}

// secret binds SECRET("name") (ADR-0045): the read of a slot the caller
// fills with the secret's value. The name is a literal, so that which
// secrets a request reads is known when its action is defined, and it is
// one the connection declares.
func (b *binder) secret(n *Call) (Type, error) {
	if b.opts.Secrets == nil {
		return Type{}, b.errAt(n.P, "SECRET may be used only in the request of an action definition (ADR-0045)")
	}
	if err := LookupFunction("SECRET").checkArity("SECRET", len(n.Args)); err != nil {
		return Type{}, b.errAt(n.P, "%v", err)
	}
	lit, ok := n.Args[0].(*TextLit)
	if !ok {
		return Type{}, b.errAt(n.Args[0].Pos(), "SECRET: the name of the secret is a text literal, such as SECRET(\"token\")")
	}
	for _, name := range b.opts.Secrets {
		if strings.EqualFold(name, lit.Value) {
			b.classes[ClassSecret] = true
			b.emit(OpLoad, b.slot(Variable{Name: SecretSlot + name, Type: Scalar(KindText)}), n.P)
			b.push(1)
			return Scalar(KindText), nil
		}
	}
	if len(b.opts.Secrets) == 0 {
		return Type{}, b.errAt(n.Args[0].Pos(), "SECRET: the definition names no connection that declares the secret %q", lit.Value)
	}
	return Type{}, b.errAt(n.Args[0].Pos(), "SECRET: the connection declares no secret %q; it declares %s", lit.Value, strings.Join(b.opts.Secrets, ", "))
}

func (b *binder) call(n *Call) (Type, error) {
	if n.Name == "IFERROR" {
		return b.ifError(n)
	}
	if n.Name == "LOOKUP" {
		return b.lookup(n)
	}
	if n.Name == "SECRET" {
		return b.secret(n)
	}
	id, ok := builtinByName[n.Name]
	if !ok {
		return Type{}, b.errAt(n.P, "%s is not implemented yet", n.Name)
	}
	spec := builtins[id]
	if !b.opts.Mode.allows(spec.class) {
		return Type{}, b.errAt(n.P, "%s is a %s function and may not be used in %s", n.Name, spec.class, b.opts.Mode.where())
	}
	if len(n.Args) > 255 {
		return Type{}, b.errAt(n.P, "%s: at most 255 arguments", n.Name)
	}
	// The argument types and literals live on the stack for the usual
	// call; a wider one grows past the arrays.
	var tsBuf [4]Type
	var litsBuf [4]litInfo
	ts, lits := tsBuf[:0], litsBuf[:0]
	for i, a := range n.Args {
		t, lit, err := b.operand(a)
		if err != nil {
			return Type{}, err
		}
		k := spec.args[len(spec.args)-1]
		if i < len(spec.args) {
			k = spec.args[i]
		}
		if k == akExact && !t.IsExact() && lit.ok {
			if t, err = b.exactLit(lit, a); err != nil {
				return Type{}, err
			}
		}
		if !k.accepts(t) {
			return Type{}, b.errAt(a.Pos(), "%s: argument %d must be %s, got %s%s", n.Name, i+1, k, t, k.hint(t))
		}
		ts = append(ts, t)
		lits = append(lits, lit)
	}
	if spec.check != nil {
		if err := spec.check(n); err != nil {
			return Type{}, b.errAt(n.P, "%s: %s", n.Name, err)
		}
	}
	rt, err := b.resultType(spec, n, ts, lits)
	if err != nil {
		return Type{}, err
	}
	argc := len(n.Args)
	if spec.family && rt.IsExact() {
		// The static scale the arguments align on travels as one more
		// exact argument, so that a blank argument counts like a value and
		// the SQL dialects and the VM round and overflow on the same step.
		scale := 0
		for _, t := range ts {
			scale = max(scale, int(t.Scale))
		}
		b.emit(OpConstDec, b.dec(DecConst{Mant: int64(scale)}), n.P)
		b.push(1)
		argc++
	}
	b.pop(argc)
	b.emit(OpCall, int32(id)<<8|int32(argc), n.P)
	b.push(1)
	if rt.IsExact() {
		// The function cannot know the static scale and kind when every
		// exact argument was blank at run time; the result is brought to
		// them.
		b.emit(OpDecScale, decArg(rt), n.P)
	}
	b.classes[spec.class] = true
	return rt, nil
}

// intLiteral reads an argument that must be a whole constant, such as a
// number of places, and reports its value.
func intLiteral(n Node) (int, bool) {
	f, ok := foldLiteral(n)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1e6 {
		return 0, false
	}
	return int(f), true
}

// placesArg reads a places argument of a function on an exact value: an
// integer literal from -MaxScale to MaxScale, or def when absent.
func (b *binder) placesArg(n *Call, i int, def int) (int, error) {
	if i >= len(n.Args) {
		return def, nil
	}
	p, ok := intLiteral(n.Args[i])
	if !ok || p < -MaxScale || p > MaxScale {
		return 0, b.errAt(n.Args[i].Pos(), "%s: the places must be a whole number from %d to %d written in the formula, so that the result's scale is known when the class is defined", n.Name, -MaxScale, MaxScale)
	}
	return p, nil
}

// methodArg checks a rounding method argument: a text literal naming one
// of RoundMethods, or nothing.
func (b *binder) methodArg(n *Call, i int) error {
	if i >= len(n.Args) {
		return nil
	}
	lit, ok := n.Args[i].(*TextLit)
	if !ok {
		return b.errAt(n.Args[i].Pos(), "%s: the method must be written in the formula: one of %s", n.Name, strings.Join(RoundMethods, ", "))
	}
	if _, known := roundMethods[lit.Value]; !known {
		return b.errAt(n.Args[i].Pos(), "%s: unknown method %q; one of %s", n.Name, lit.Value, strings.Join(RoundMethods, ", "))
	}
	return nil
}

// argKind is what a builtin accepts in one argument position.
type argKind uint8

const (
	akNumber    argKind = iota + 1 // number, bool or blank
	akExact                        // decimal, money, bool or blank; a number literal becomes exact
	akNumeric                      // either family
	akMoney                        // money or blank
	akText                         // text or blank
	akDate                         // a calendar day or blank
	akDateTime                     // an instant or blank
	akDateLike                     // a date, a datetime or blank
	akScalar                       // any scalar
	akCondition                    // bool or blank
	akJSON                         // json or blank
)

func (k argKind) String() string {
	switch k {
	case akNumber:
		return "a number"
	case akExact:
		return "an exact number"
	case akNumeric:
		return "a number"
	case akMoney:
		return "money"
	case akText:
		return "text"
	case akDate:
		return "a date"
	case akDateTime:
		return "a datetime"
	case akDateLike:
		return "a date or a datetime"
	case akScalar:
		return "a scalar value"
	case akCondition:
		return "a condition"
	case akJSON:
		return "a json value"
	}
	return "?"
}

func (k argKind) accepts(t Type) bool {
	switch k {
	case akNumber:
		return isNumeric(t)
	case akExact:
		return isExactOperand(t)
	case akNumeric:
		return isAnyNumeric(t)
	case akMoney:
		return isMoneyT(t)
	case akText:
		return isTextual(t)
	case akDate:
		return isDateT(t)
	case akDateTime:
		return isDateTimeT(t)
	case akDateLike:
		return isDateLike(t)
	case akScalar:
		return t.IsScalar()
	case akCondition:
		return isCondition(t)
	case akJSON:
		return isJSONT(t)
	}
	return false
}

func (k argKind) hint(t Type) string {
	switch {
	case k == akNumber || k == akNumeric || k == akExact:
		return arithmeticHint(t)
	case k == akMoney && t.Kind == KindDecimal:
		return "; MONEY(amount, currency) makes a decimal money"
	case k == akCondition:
		return conditionHint(t)
	case k == akText && (t.Kind == KindNumber || t.Kind.IsExact()):
		return "; TEXT converts a number to text"
	case (k == akDate || k == akDateLike) && t.Kind == KindText:
		return "; DATEVALUE converts text to a date"
	case k == akDate && t.Kind == KindDateTime:
		return "; DATEOF turns a datetime into a date"
	case k == akDateTime && t.Kind == KindText:
		return "; DATETIMEVALUE converts text to a datetime"
	case k == akDateTime && t.Kind == KindDate:
		return "; DATETIME turns a date into a datetime"
	}
	return ""
}

// checkRegexLiteral rejects an invalid literal pattern at definition time.
func checkRegexLiteral(n *Call) error {
	if lit, ok := n.Args[1].(*TextLit); ok {
		if _, err := regexp.Compile(lit.Value); err != nil {
			return fmt.Errorf("invalid pattern %q: %v", lit.Value, err)
		}
	}
	return nil
}

// lookup binds LOOKUP(source, column, condition) (ADR-0016, ADR-0020): the
// source is a table of the options, the column one of its columns or an
// aggregate over them (ADR-0046), and the condition a program of its own
// over the source's columns and the document variables it names as
// THIS.name. The call becomes one lookup instruction over a query slot;
// the VM hands the slot and the values of the THIS variables to the
// executor, and the SQL generator writes a correlated subquery. A
// condition that is, or holds as a term of a conjunction, an equality of
// a column with an expression of the document alone also records that
// expression, so that an index on the column can serve the lookup
// (ADR-0036, ADR-0046).
func (b *binder) lookup(n *Call) (Type, error) {
	if b.source != nil {
		return Type{}, b.errAt(n.P, "a query function may not be used inside another's arguments (ADR-0020)")
	}
	if !b.opts.Mode.allows(ClassLookup) {
		return Type{}, b.errAt(n.P, "LOOKUP is a lookup function and may not be used in %s", b.opts.Mode.where())
	}
	if err := LookupFunction("LOOKUP").checkArity("LOOKUP", len(n.Args)); err != nil {
		return Type{}, b.errAt(n.P, "%v", err)
	}
	srcName, ok := n.Args[0].(*Ident)
	if !ok {
		return Type{}, b.errAt(n.Args[0].Pos(), "LOOKUP: the source is the name of a table")
	}
	var src *Source
	if b.opts.Sources != nil {
		src, ok = b.opts.Sources.Source(srcName.Name)
	} else {
		ok = false
	}
	if !ok {
		return Type{}, b.errAt(srcName.P, "unknown source %s", srcName.Name)
	}
	if src.Class && b.opts.Mode == ModeAuto {
		return Type{}, b.errAt(srcName.P, "%s is a class, and an auto formula may not read other documents: its stored value is a function of its own document; make the variable triggered, with \"mode\": \"triggered\" and its triggers under \"on\" (ADR-0046)", src.Name)
	}
	q := &QuerySlot{Source: src.Name, Order: append([]string(nil), src.Key...)}
	argOf := map[string]int{}
	switch c := n.Args[1].(type) {
	case *Ident:
		col, ok := src.Column(c.Name)
		if !ok {
			return Type{}, b.errAt(c.P, "unknown column %s of %s; its columns are %s", c.Name, src.Name, strings.Join(src.ColumnNames(), ", "))
		}
		q.Column, q.Type = col.Name, col.Type
	case *Call:
		if err := b.aggregate(c, src, q, argOf); err != nil {
			return Type{}, err
		}
	default:
		return Type{}, b.errAt(n.Args[1].Pos(), "LOOKUP: the column is the name of a column of %s, or COUNT, SUM, MIN or MAX over its columns", src.Name)
	}
	cond, ct, err := b.subProgram(n.Args[2], src, q, argOf)
	if err != nil {
		return Type{}, err
	}
	if !isCondition(ct) {
		return Type{}, b.errAt(n.Args[2].Pos(), "LOOKUP: the condition must be a condition, got %s%s", ct, conditionHint(ct))
	}
	q.Cond = cond
	// The THIS variables become slots of this program, in argument order.
	for _, name := range q.ArgNames {
		v, _ := b.scope.Lookup(name)
		q.Args = append(q.Args, b.slot(v))
	}
	// The key: the condition when it is one equality, or else such a term
	// of a conjunction, which narrows the rows and leaves the decision to
	// the condition (ADR-0037, ADR-0046). Among several the first on a
	// column that leads an index of the source is taken, and the first of
	// all when there is none.
	terms := conjuncts(n.Args[2], nil)
	for _, term := range terms {
		key, col, err := b.lookupKey(term, src, q, argOf)
		if err != nil {
			return Type{}, err
		}
		if key == nil || q.Key != nil && !src.indexed(col) {
			continue
		}
		q.Key, q.IndexColumn, q.KeyPart = key, col, len(terms) > 1
		if src.indexed(col) {
			break
		}
	}
	idx := int32(len(b.prog.Queries))
	b.prog.Queries = append(b.prog.Queries, *q)
	b.classes[ClassLookup] = true
	b.emit(OpLookup, idx, n.P)
	b.push(1)
	return q.Type, nil
}

// rowAggregates are the functions that may stand in the column position
// of a LOOKUP (ADR-0046).
var rowAggregates = map[string]bool{"COUNT": true, "SUM": true, "MIN": true, "MAX": true}

// aggregate binds COUNT, SUM, MIN or MAX in the column position of a
// LOOKUP: the expression it runs over is a program of its own, like the
// condition, and its type is what the same function gives one value of
// that type.
func (b *binder) aggregate(n *Call, src *Source, q *QuerySlot, argOf map[string]int) error {
	if !rowAggregates[n.Name] {
		if n.Name == "AVERAGE" {
			return b.errAt(n.P, "LOOKUP: AVERAGE over a source is not built yet; divide a SUM by a COUNT")
		}
		return b.errAt(n.P, "LOOKUP: %s cannot stand where the column stands; a column of %s can, and COUNT, SUM, MIN or MAX over its columns", n.Name, src.Name)
	}
	if len(n.Args) != 1 {
		return b.errAt(n.P, "LOOKUP: %s over a source takes one expression, such as %s(%s)", n.Name, n.Name, src.Columns[0].Name)
	}
	val, vt, err := b.subProgram(n.Args[0], src, q, argOf)
	if err != nil {
		return err
	}
	spec := builtins[builtinByName[n.Name]]
	if k := spec.args[0]; !k.accepts(vt) {
		return b.errAt(n.Args[0].Pos(), "%s: argument 1 must be %s, got %s%s", n.Name, k, vt, k.hint(vt))
	}
	rt, err := b.resultType(spec, n, []Type{vt}, []litInfo{{}})
	if err != nil {
		return err
	}
	q.Agg, q.Value, q.Type = n.Name, val, rt
	return nil
}

// conjuncts appends the terms of a top-level AND to out: the expression
// itself when it is not one.
func conjuncts(n Node, out []Node) []Node {
	if and, ok := n.(*Binary); ok && and.Op == TokAnd {
		return conjuncts(and.R, conjuncts(and.L, out))
	}
	return append(out, n)
}

// lookupKey reads a term as a key of the slot: the compared expression
// and the column, when the term is an equality of a column with an
// expression of the document alone, of the column's kind; nil otherwise.
func (b *binder) lookupKey(term Node, src *Source, q *QuerySlot, argOf map[string]int) (*Program, string, error) {
	eq, ok := term.(*Binary)
	if !ok || eq.Op != TokEq {
		return nil, "", nil
	}
	for _, side := range [][2]Node{{eq.L, eq.R}, {eq.R, eq.L}} {
		id, ok := side[0].(*Ident)
		if !ok || mentionsColumn(side[1]) {
			continue
		}
		kcol, ok := src.Column(id.Name)
		if !ok {
			continue
		}
		if kcol.Type.Kind == KindMoney {
			// Money equals a decimal by its amount and money of another
			// currency not at all, so an amount is no key (ADR-0037).
			return nil, "", nil
		}
		key, kt, err := b.subProgram(side[1], src, q, argOf)
		if err != nil {
			return nil, "", err
		}
		if kt.Kind == KindNumber && kcol.Type.Kind == KindDecimal && key.ClaimExact(kcol.Type) {
			kt = key.Result
		}
		if kt.Kind == kcol.Type.Kind {
			return key, kcol.Name, nil
		}
		return nil, "", nil
	}
	return nil, "", nil
}

// subProgram binds an expression of a LOOKUP as a program of its own,
// whose slots are the source's columns and the THIS arguments of q.
func (b *binder) subProgram(n Node, src *Source, q *QuerySlot, argOf map[string]int) (*Program, Type, error) {
	sub := &binder{src: b.src, scope: b.scope, opts: b.opts, prog: &Program{},
		slotOf: map[string]int32{}, numIndex: map[float64]int32{}, textIndex: map[string]int32{}, classes: map[Class]bool{},
		source: src, q: q, argOf: argOf}
	t, err := sub.expr(n)
	if err != nil {
		return nil, Type{}, err
	}
	sub.emit(OpReturn, 0, n.Pos())
	sub.prog.Result = t
	for c := range sub.classes {
		b.classes[c] = true
		sub.prog.Classes = append(sub.prog.Classes, c)
	}
	sort.Slice(sub.prog.Classes, func(i, j int) bool { return classOrder[sub.prog.Classes[i]] < classOrder[sub.prog.Classes[j]] })
	sub.prog.MaxStack = sub.maxDepth
	return sub.prog, t, nil
}

// mentionsColumn reports whether an expression inside a LOOKUP names a
// column: any bare name, since variables are THIS.name there.
func mentionsColumn(n Node) bool {
	switch n := n.(type) {
	case *Ident, *Member:
		return true
	case *Unary:
		return mentionsColumn(n.X)
	case *Binary:
		return mentionsColumn(n.L) || mentionsColumn(n.R)
	case *If:
		return mentionsColumn(n.Cond) || mentionsColumn(n.Then) || mentionsColumn(n.Else)
	case *Call:
		for _, a := range n.Args {
			if mentionsColumn(a) {
				return true
			}
		}
	}
	return false
}

// column binds a bare name inside a LOOKUP: a column of the source.
func (b *binder) column(n *Ident) (Type, error) {
	col, ok := b.source.Column(n.Name)
	if !ok {
		if v, isVar := b.scope.Lookup(n.Name); isVar {
			return Type{}, b.errAt(n.P, "unknown column %s of %s; the document's %s is THIS.%s", n.Name, b.source.Name, v.Name, v.Name)
		}
		return Type{}, b.errAt(n.P, "unknown column %s of %s; its columns are %s", n.Name, b.source.Name, strings.Join(b.source.ColumnNames(), ", "))
	}
	b.emit(OpLoad, b.slot(Variable(col)), n.P)
	b.push(1)
	return col.Type, nil
}

// thisRef binds THIS.name inside a LOOKUP: a scalar variable of the
// document, passed to the executor as an argument. The enclosing binder
// gives the argument its slot once the condition is bound.
func (b *binder) thisRef(n *ThisRef) (Type, error) {
	v, ok := b.scope.Lookup(n.Name)
	if !ok {
		return Type{}, b.errAt(n.P, "THIS.%s: the document has no variable %s", n.Name, n.Name)
	}
	if !v.Type.IsScalar() {
		return Type{}, b.errAt(n.P, "THIS.%s is %s; only a scalar variable can be read inside LOOKUP yet", v.Name, v.Type)
	}
	key := strings.ToUpper(v.Name)
	if _, seen := b.argOf[key]; !seen {
		b.argOf[key] = len(b.q.ArgNames)
		b.q.ArgNames = append(b.q.ArgNames, v.Name)
	}
	b.emit(OpLoad, b.slot(Variable{Name: "THIS." + v.Name, Type: v.Type}), n.P)
	b.push(1)
	return v.Type, nil
}
