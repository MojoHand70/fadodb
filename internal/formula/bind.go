package formula

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The binder turns a syntax tree into a Program: it resolves names to
// slots, gives every expression a static type by the rules of ADR-0019,
// checks function classes against the mode (ADR-0015), tracks the stack
// depth, and emits bytecode. Everything it rejects is rejected at
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
)

func (m Mode) String() string {
	if m == ModeAuto {
		return "auto"
	}
	return "any"
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
		s.index[key] = len(s.vars)
		s.vars = append(s.vars, v)
	}
	return s, nil
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
}

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
		slotOf: map[string]int32{}, numIndex: map[float64]int32{}, textIndex: map[string]int32{},
		classes: map[Class]bool{}}
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

var classOrder = map[Class]int{ClassPure: 0, ClassContext: 1, ClassLookup: 2, ClassQuery: 3}

type binder struct {
	src   string
	scope *Scope
	opts  BindOptions
	prog  *Program

	slotOf    map[string]int32
	numIndex  map[float64]int32
	textIndex map[string]int32
	classes   map[Class]bool

	depth, maxDepth int
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

// Type predicates of ADR-0019. Blank fits everywhere.

func isNumeric(t Type) bool   { return t.Kind == KindNumber || t.Kind == KindBool || t.Kind == KindNull }
func isCondition(t Type) bool { return t.Kind == KindBool || t.Kind == KindNull }
func isTextual(t Type) bool   { return t.Kind == KindText || t.Kind == KindNull }
func isDateT(t Type) bool     { return t.Kind == KindDate || t.Kind == KindNull }
func isJSONT(t Type) bool     { return t.Kind == KindJSON || t.Kind == KindNull }

// comparable kinds: everything scalar but json compares as text too (ADR-0013).
func comparable(a, b Type) bool {
	if !a.IsScalar() || !b.IsScalar() {
		return false
	}
	return a.Kind == b.Kind || a.Kind == KindNull || b.Kind == KindNull
}

// join is the type of two branches that must agree: the same type, or the
// non-blank one when one side is blank.
func join(a, b Type) (Type, bool) {
	switch {
	case a.Kind == KindNull:
		return b, true
	case b.Kind == KindNull:
		return a, true
	case a.Equal(b):
		return a, true
	}
	return Type{}, false
}

func (b *binder) expr(n Node) (Type, error) {
	switch n := n.(type) {
	case *NumberLit:
		b.emit(OpConstNum, b.num(n.Value), n.P)
		b.push(1)
		return Scalar(KindNumber), nil
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
		v, ok := b.scope.Lookup(n.Name)
		if !ok {
			if fn := LookupFunction(n.Name); fn != nil {
				return Type{}, b.errAt(n.P, "unknown variable %s; %s is a function: write %s", n.Name, fn.Name, fn.Signature(strings.ToUpper(n.Name)))
			}
			return Type{}, b.errAt(n.P, "unknown variable %s", n.Name)
		}
		if !v.Type.IsScalar() {
			return Type{}, b.errAt(n.P, "%s is %s; composite values are not supported yet", v.Name, v.Type)
		}
		b.emit(OpLoad, b.slot(v), n.P)
		b.push(1)
		return v.Type, nil
	case *Member:
		v, ok := b.scope.Lookup(n.Base)
		if !ok {
			return Type{}, b.errAt(n.P, "unknown variable %s", n.Base)
		}
		if v.Type.Kind != KindRows {
			return Type{}, b.errAt(n.P, "%s is %s, not rows; only a rows variable has columns", v.Name, v.Type)
		}
		return Type{}, b.errAt(n.P, "%s.%s: composite values are not supported yet", n.Base, n.Name)
	case *ThisRef:
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

func (b *binder) unary(n *Unary) (Type, error) {
	t, err := b.expr(n.X)
	if err != nil {
		return Type{}, err
	}
	switch n.Op {
	case TokMinus, TokPercent:
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
	if t.Kind == KindNumber {
		return "; compare it, for example <> 0"
	}
	return ""
}

var arithmeticOps = map[TokenKind]Op{
	TokPlus: OpAdd, TokMinus: OpSub, TokStar: OpMul, TokSlash: OpDiv, TokBackslash: OpDivStrict, TokCaret: OpPow,
}

var comparisonOps = map[TokenKind]Op{
	TokEq: OpEq, TokNe: OpNe, TokLt: OpLt, TokLe: OpLe, TokGt: OpGt, TokGe: OpGe,
}

func (b *binder) binary(n *Binary) (Type, error) {
	switch n.Op {
	case TokAnd, TokOr:
		return b.logical(n)
	}
	lt, err := b.expr(n.L)
	if err != nil {
		return Type{}, err
	}
	rt, err := b.expr(n.R)
	if err != nil {
		return Type{}, err
	}
	b.pop(1)
	if op, ok := arithmeticOps[n.Op]; ok {
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
		if !comparable(lt, rt) {
			return Type{}, b.errAt(n.P, "%s compares %s with %s; convert one side first", opText[n.Op], lt, rt)
		}
		b.emit(op, 0, n.P)
		return Scalar(KindBool), nil
	}
	return Type{}, b.errAt(n.P, "unexpected operator")
}

func arithmeticHint(t Type) string {
	switch t.Kind {
	case KindText:
		return "; VALUE converts text to a number"
	case KindDate:
		return "; dates move with ADDDAYS, ADDMONTHS and ADDYEARS, and DAYS measures them"
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
	tt, err := b.expr(n.Then)
	if err != nil {
		return Type{}, err
	}
	toEnd := b.emit(OpJump, 0, n.P)
	b.depth = base
	b.patch(toElse, b.here())
	et, err := b.expr(n.Else)
	if err != nil {
		return Type{}, err
	}
	b.patch(toEnd, b.here())
	t, ok := join(tt, et)
	if !ok {
		return Type{}, b.errAt(n.P, "IF branches must have the same type, got %s and %s", tt, et)
	}
	if !t.IsScalar() {
		return Type{}, b.errAt(n.P, "composite values are not supported yet")
	}
	return t, nil
}

func (b *binder) call(n *Call) (Type, error) {
	id, ok := builtinByName[n.Name]
	if !ok {
		fn := LookupFunction(n.Name)
		if fn != nil && (fn.Class == ClassLookup || fn.Class == ClassQuery) {
			return Type{}, b.errAt(n.P, "%s is not implemented yet", n.Name)
		}
		return Type{}, b.errAt(n.P, "%s is not implemented yet", n.Name)
	}
	spec := builtins[id]
	if b.opts.Mode == ModeAuto && (spec.class == ClassContext || spec.class == ClassQuery) {
		return Type{}, b.errAt(n.P, "%s is a %s function and may not be used in an auto formula", n.Name, spec.class)
	}
	if len(n.Args) > 255 {
		return Type{}, b.errAt(n.P, "%s: at most 255 arguments", n.Name)
	}
	for i, a := range n.Args {
		t, err := b.expr(a)
		if err != nil {
			return Type{}, err
		}
		k := spec.args[len(spec.args)-1]
		if i < len(spec.args) {
			k = spec.args[i]
		}
		if !k.accepts(t) {
			return Type{}, b.errAt(a.Pos(), "%s: argument %d must be %s, got %s%s", n.Name, i+1, k, t, k.hint(t))
		}
	}
	if spec.check != nil {
		if err := spec.check(n); err != nil {
			return Type{}, b.errAt(n.P, "%s: %s", n.Name, err)
		}
	}
	b.pop(len(n.Args))
	b.emit(OpCall, int32(id)<<8|int32(len(n.Args)), n.P)
	b.push(1)
	b.classes[spec.class] = true
	return Scalar(spec.result), nil
}

// argKind is what a builtin accepts in one argument position.
type argKind uint8

const (
	akNumber argKind = iota + 1
	akText
	akDate
	akScalar
	akCondition
	akJSON
)

func (k argKind) String() string {
	switch k {
	case akNumber:
		return "a number"
	case akText:
		return "text"
	case akDate:
		return "a date"
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
	case akText:
		return isTextual(t)
	case akDate:
		return isDateT(t)
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
	case k == akNumber:
		return arithmeticHint(t)
	case k == akCondition:
		return conditionHint(t)
	case k == akText && t.Kind == KindNumber:
		return "; TEXT converts a number to text"
	case k == akDate && t.Kind == KindText:
		return "; DATEVALUE converts text to a date"
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
