package formula

import (
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

// The Go VM executes a Program over a fixed value stack with a call-free
// instruction loop. The scalar path never allocates: a five-operation
// arithmetic formula runs under 100 ns with zero allocations (ADR-0012).
// Errors are the closed list of ADR-0019, ADR-0022 and ADR-0024 and are
// returned, never panicked.

// Context is what the context functions see (ADR-0015).
// GroupEveryone is the system group of every caller that has an id
// (ADR-0052): the engine gives it, no host does.
const GroupEveryone = "everyone"

// InGroup reports whether the user is in the group: one of its groups,
// one of its system groups, or everyone, for a user that has an id.
func (c *Context) InGroup(name string) bool {
	if c == nil {
		return false
	}
	for _, g := range c.Groups {
		if g == name {
			return true
		}
	}
	for _, g := range c.System {
		if g == name {
			return true
		}
	}
	return c.User != "" && name == GroupEveryone
}

// AllGroups lists every group InGroup answers TRUE for: the groups as
// they are for a user with no id and no system group, nil included, and
// a new list otherwise.
func (c *Context) AllGroups() []string {
	if c == nil {
		return nil
	}
	if c.User == "" && len(c.System) == 0 {
		return c.Groups
	}
	all := make([]string, 0, len(c.Groups)+len(c.System)+1)
	all = append(append(all, c.Groups...), c.System...)
	if c.User != "" {
		all = append(all, GroupEveryone)
	}
	return all
}

type Context struct {
	// User is the id of the user the formula runs for; empty for a system
	// recompute.
	User string
	// Groups are the groups the user belongs to.
	Groups []string
	// System are the system groups the user is in (ADR-0052). INGROUP
	// answers them beside Groups, and answers GroupEveryone for a user
	// that has an id.
	System []string
	// Kind is what the user is, for USERKIND(): person, agent or service,
	// or empty when the host program does not say (ADR-0050). For is the
	// id of whoever the user acts for, for ACTINGFOR(); empty when it acts
	// for itself.
	Kind string
	For  string
	// Now is the current time in seconds since the epoch, UTC. Zero means
	// the wall clock.
	Now int64
	// State is the state of the document the formula runs over, for
	// STATE(); empty for a class without states and in a preview of a
	// stateless application (ADR-0040).
	State string
	// ID is the id of the document the formula runs over, for ID(); empty
	// when there is no document.
	ID string
	// Doc is what the document the formula runs over keeps of its own
	// life (ADR-0047); nil when there is no document.
	Doc *Life
}

// Life is what a document keeps of its own life (ADR-0047, items 4 and
// 5): its version, the moments it was created, its current version was
// made and it entered the state it is in, in seconds since the epoch, who
// did each, and the state it came from. A zero moment and an empty text
// are blank.
type Life struct {
	Version                         int64
	CreatedAt, UpdatedAt, EnteredAt int64
	CreatedBy, UpdatedBy, EnteredBy string
	EnteredFrom                     string
	// States is what the life says of each state the document has been
	// in (item 6), in the order it first entered them. Whoever runs a
	// program that reads it, Program.ReadsStates, tells it first.
	States []StateLife
}

// LifeColumns names, for each function of the row, the system column
// that holds its answer, which is also the name its parameter is bound
// under when a statement runs over no document row.
var LifeColumns = map[string]string{
	"CREATEDAT": "created_at", "CREATEDBY": "created_by",
	"UPDATEDAT": "updated_at", "UPDATEDBY": "updated_by",
	"ENTEREDAT": "state_entered_at", "ENTEREDBY": "state_entered_by",
	"ENTEREDFROM": "state_entered_from", "VERSION": "version",
}

// lifeValue reads one fact of the document's life by its column, as a
// database driver takes it: nil for a blank.
func lifeValue(ctx *Context, column string) any {
	if ctx == nil || ctx.Doc == nil {
		return nil
	}
	moment := func(secs int64) any {
		if secs == 0 {
			return nil
		}
		return secs
	}
	text := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	d := ctx.Doc
	switch column {
	case "created_at":
		return moment(d.CreatedAt)
	case "updated_at":
		return moment(d.UpdatedAt)
	case "state_entered_at":
		return moment(d.EnteredAt)
	case "version":
		return moment(d.Version)
	case "created_by":
		return text(d.CreatedBy)
	case "updated_by":
		return text(d.UpdatedBy)
	case "state_entered_by":
		return text(d.EnteredBy)
	case "state_entered_from":
		return text(d.EnteredFrom)
	}
	return nil
}

// EvalError is an evaluation error: a code from the closed list, the byte
// offset of the failing operation in the source when known, and a message.
type EvalError struct {
	Code string
	Pos  int
	Msg  string
}

func (e *EvalError) Error() string {
	if e.Pos >= 0 {
		return fmt.Sprintf("%s: %s at offset %d", e.Code, e.Msg, e.Pos)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// Error codes (ADR-0019, ADR-0020, ADR-0022, ADR-0024).
const (
	ErrDivByZero        = "div_by_zero"
	ErrNotANumber       = "not_a_number"
	ErrBadText          = "bad_text"
	ErrOutOfRange       = "out_of_range"
	ErrLengthMismatch   = "length_mismatch"
	ErrTooManyElements  = "too_many_elements"
	ErrQueryFailed      = "query_failed"
	ErrOverflow         = "overflow"
	ErrInexact          = "inexact"
	ErrCurrencyMismatch = "currency_mismatch"
)

// tryFrame is one open IFERROR: where to continue and how deep the stack
// was when it opened.
type tryFrame struct{ pc, sp int }

// VM executes programs. A VM is not safe for concurrent use; make one per
// goroutine. Its stack is reused across runs.
type VM struct {
	// Limits are the size limits of the database the programs belong to
	// (ADR-0021). NewVM sets the defaults; set others before Run.
	Limits Limits
	// Executor answers LOOKUP (ADR-0020). A program with a lookup and no
	// executor stops with query_failed.
	Executor QueryExecutor

	stack []Value
	qargs []Value
	ctx   *Context
	// tries is the handler stack of IFERROR and nt its depth, kept out
	// of the instruction loop's registers since only try, end.try and an
	// error touch them.
	tries [maxTries]tryFrame
	nt    int
}

// NewVM makes a VM with the default limits.
func NewVM() *VM { return &VM{Limits: DefaultLimits(), stack: make([]Value, 0, 32)} }

// Run executes the program. vars holds one value per program slot, in slot
// order. ctx may be nil when the program uses no context function.
func (vm *VM) Run(p *Program, vars []Value, ctx *Context) (Value, error) {
	if len(vars) < len(p.Slots) {
		return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: fmt.Sprintf("program reads %d variables, got %d", len(p.Slots), len(vars))}
	}
	if cap(vm.stack) < p.MaxStack {
		vm.stack = make([]Value, 0, p.MaxStack)
	}
	stack := vm.stack[:p.MaxStack]
	vm.ctx = ctx
	vm.nt = 0
	code := p.Code
	sp := 0
	pc := 0
	var err *EvalError
	for {
		in := code[pc]
		pc++
		switch in.Op {
		case OpNop:
		case OpConstNum:
			stack[sp] = Value{kind: KindNumber, num: p.Nums[in.Arg]}
			sp++
		case OpConstText:
			stack[sp] = Value{kind: KindText, str: p.Texts[in.Arg]}
			sp++
		case OpTrue:
			stack[sp] = Value{kind: KindBool, num: 1}
			sp++
		case OpFalse:
			stack[sp] = Value{kind: KindBool}
			sp++
		case OpBlank:
			stack[sp] = Value{}
			sp++
		case OpLoad:
			stack[sp] = vars[in.Arg]
			sp++
		case OpNeg:
			stack[sp-1] = Value{kind: KindNumber, num: -stack[sp-1].num}
		case OpPercent:
			stack[sp-1] = Value{kind: KindNumber, num: stack[sp-1].num / 100}
		case OpNot:
			stack[sp-1] = Bool(!stack[sp-1].Truth())
		case OpAdd, OpSub, OpMul, OpPow:
			a, b := stack[sp-2].num, stack[sp-1].num
			var r float64
			switch in.Op {
			case OpAdd:
				r = a + b
			case OpSub:
				r = a - b
			case OpMul:
				r = a * b
			default:
				r = pow(a, b)
			}
			if notFinite(r) {
				err = errNotANumber
				break
			}
			sp--
			stack[sp-1] = Value{kind: KindNumber, num: r}
		case OpDiv, OpDivStrict:
			a, b := stack[sp-2].num, stack[sp-1].num
			if b == 0 {
				err = errDivByZero
				break
			}
			r := a / b
			if notFinite(r) {
				err = errNotANumber
				break
			}
			sp--
			stack[sp-1] = Value{kind: KindNumber, num: r}
		case OpConstDec, OpDecNeg, OpDecPercent, OpDecScale, OpDecAdd, OpDecSub, OpDecMul, OpDecDiv:
			// The exact instructions run out of line, so that the float
			// path's registers stay in the loop (ADR-0022).
			sp, err = exactOp(p, stack, sp, in)
		case OpConcat:
			a, b := render(stack[sp-2]), render(stack[sp-1])
			if n := vm.maxText(); len(a)+len(b) > n && utf8.RuneCountInString(a)+utf8.RuneCountInString(b) > n {
				err = tooLong()
				break
			}
			sp--
			stack[sp-1] = Value{kind: KindText, str: a + b}
		case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
			a, b := &stack[sp-2], &stack[sp-1]
			var c int
			if a.kind.IsExact() || b.kind.IsExact() {
				var e *EvalError
				if c, e = compareExact(a, b, int(in.Arg)); e != nil {
					err = e
					break
				}
			} else {
				c = compare(*a, *b)
			}
			sp--
			var r bool
			switch in.Op {
			case OpEq:
				r = c == 0
			case OpNe:
				r = c != 0
			case OpLt:
				r = c < 0
			case OpLe:
				r = c <= 0
			case OpGt:
				r = c > 0
			default:
				r = c >= 0
			}
			stack[sp-1] = Bool(r)
		case OpJump:
			pc = int(in.Arg)
		case OpJumpIfFalse:
			sp--
			if !stack[sp].Truth() {
				pc = int(in.Arg)
			}
		case OpAndJump:
			if !stack[sp-1].Truth() {
				stack[sp-1] = Value{kind: KindBool}
				pc = int(in.Arg)
			} else {
				sp--
			}
		case OpOrJump:
			if stack[sp-1].Truth() {
				pc = int(in.Arg)
			} else {
				sp--
			}
		case OpToBool:
			if stack[sp-1].kind == KindNull {
				stack[sp-1] = Value{kind: KindBool}
			}
		case OpTry:
			vm.tries[vm.nt] = tryFrame{pc: int(in.Arg), sp: sp}
			vm.nt++
		case OpEndTry:
			vm.nt--
		case OpCall:
			argc := int(in.Arg & 0xff)
			fn := builtins[in.Arg>>8]
			v, e := fn.fn(vm, stack[sp-argc:sp])
			if e != nil {
				err = e
				break
			}
			sp -= argc
			stack[sp] = v
			sp++
		case OpLookup:
			q := &p.Queries[in.Arg]
			if vm.Executor == nil {
				err = errNoExecutor
				break
			}
			if cap(vm.qargs) < len(q.Args) {
				vm.qargs = make([]Value, len(q.Args))
			}
			args := vm.qargs[:len(q.Args)]
			for i, a := range q.Args {
				args[i] = vars[a]
			}
			v, e := vm.Executor.Lookup(q, args, vm.ctx)
			if e != nil {
				err = e
				break
			}
			stack[sp] = v
			sp++
		case OpQuery:
			err = &EvalError{Code: ErrQueryFailed, Msg: "QUERY is not implemented yet"}
		case OpReturn:
			return stack[sp-1], nil
		default:
			err = &EvalError{Code: ErrQueryFailed, Msg: fmt.Sprintf("unknown opcode %d", in.Op)}
		}
		if err != nil {
			if vm.nt > 0 {
				// IFERROR: drop what the failed value left and run the
				// fallback.
				vm.nt--
				sp = vm.tries[vm.nt].sp
				pc = vm.tries[vm.nt].pc
				err = nil
				continue
			}
			// The errors the operators raise are shared values; the one
			// that leaves carries its position in a copy of its own.
			out := &EvalError{Code: err.Code, Msg: err.Msg, Pos: -1}
			if p.Pos != nil {
				out.Pos = int(p.Pos[pc-1])
			}
			return Value{}, out
		}
	}
}

// The errors the instruction loop and the exact arithmetic raise without
// a detail to carry, shared so that an error IFERROR catches costs no
// allocation.
var (
	errDivByZero   = &EvalError{Code: ErrDivByZero, Msg: "/ by zero"}
	errNotANumber  = &EvalError{Code: ErrNotANumber, Msg: "the result is not a finite number"}
	errOverflow    = &EvalError{Code: ErrOverflow, Msg: "the exact result does not fit 18 digits"}
	errModByZero   = &EvalError{Code: ErrDivByZero, Msg: "MOD by zero"}
	errDateRange   = &EvalError{Code: ErrOutOfRange, Msg: "the date is outside year 1 to year 9999"}
	errCurrencyMix = &EvalError{Code: ErrCurrencyMismatch, Msg: "the currencies of one operation must agree"}
	errNoExecutor  = &EvalError{Code: ErrQueryFailed, Msg: "no query executor: the formula reads a table and the VM has nothing to read it with"}
)

// notFinite reports an infinite or NaN result with one comparison: the
// magnitude of a finite number is at most MaxFloat64, an infinity's is
// larger, and a NaN compares false with everything.
func notFinite(r float64) bool { return !(math.Abs(r) <= math.MaxFloat64) }

func currencyMismatch() *EvalError { return errCurrencyMix }

// exactOp executes one exact instruction over the stack and returns the
// new stack pointer.
//
//go:noinline
func exactOp(p *Program, stack []Value, sp int, in Instr) (int, *EvalError) {
	switch in.Op {
	case OpConstDec:
		c := p.Decs[in.Arg]
		stack[sp] = Value{kind: KindDecimal, scale: c.Scale, num: mantBits(c.Mant)}
		return sp + 1, nil
	case OpDecAdd, OpDecSub, OpDecMul, OpDecDiv:
		r, e := decBinary(in.Op, stack[sp-2], stack[sp-1], int(in.Arg&0xff), in.Arg&decMoneyFlag != 0)
		if e != nil {
			return sp, e
		}
		stack[sp-2] = r
		return sp - 1, nil
	}
	v, e := decUnaryValue(in.Op, stack[sp-1], in.Arg)
	if e != nil {
		return sp, e
	}
	stack[sp-1] = v
	return sp, nil
}

// decUnaryValue is dec.neg, dec.percent and dec.scale.
func decUnaryValue(op Op, v Value, arg int32) (Value, *EvalError) {
	scale, money := int(arg&0xff), arg&decMoneyFlag != 0
	if op == OpDecScale {
		if money && (v.kind == KindDecimal || v.kind == KindNumber) {
			// A money result with no currency: every money operand was
			// blank, and so is the result.
			return Blank(), nil
		}
		if v.kind == KindNumber {
			// A function ran on the float family because every exact
			// argument was blank; its result is 0 or blank-derived.
			m, e := decFromFloat(v.num, scale, rmHalfUp)
			if e != nil {
				return Value{}, e
			}
			return Decimal(m, scale), nil
		}
		if v.kind.IsExact() && int(v.scale) != scale {
			m, e := rescaleExact(v.Mant(), int(v.scale), scale)
			if e != nil {
				return Value{}, e
			}
			return Value{kind: v.kind, scale: int8(scale), num: mantBits(m), str: v.str}, nil
		}
		return v, nil
	}
	if v.kind == KindNull || v.kind == KindBool {
		if money {
			return Blank(), nil
		}
		m := int64(v.num)
		if op == OpDecNeg {
			m = -m
		}
		return Decimal(m, scale), nil
	}
	m := v.Mant()
	if op == OpDecNeg {
		if m == math.MinInt64 {
			return Value{}, overflow()
		}
		m = -m
	}
	return Value{kind: v.kind, scale: int8(scale), num: mantBits(m), str: v.str}, nil
}

// compareExact orders two operands of which one is exact, both brought to
// the static scale the instruction carries, as the SQL dialects align
// them; money operands must share a currency.
//
//go:noinline
func compareExact(a, b *Value, scale int) (int, *EvalError) {
	if a.kind == KindMoney && b.kind == KindMoney && a.str != b.str {
		return 0, currencyMismatch()
	}
	ma, sa := exactOperand(*a)
	mb, sb := exactOperand(*b)
	x, ok := scaleBy(ma, scale-sa)
	y, ok2 := scaleBy(mb, scale-sb)
	if !ok || !ok2 {
		return 0, overflow()
	}
	switch {
	case x < y:
		return -1, nil
	case x > y:
		return 1, nil
	}
	return 0, nil
}

// exactOperand reads an operand of an exact operation: a decimal or money
// as it is, a bool as 1 or 0, a blank as 0 (ADR-0005).
func exactOperand(v Value) (int64, int) {
	switch v.kind {
	case KindDecimal, KindMoney:
		return v.Mant(), int(v.scale)
	case KindBool:
		return int64(v.num), 0
	}
	return 0, 0
}

// decBinary computes an exact +, -, * or / at the static result scale
// (ADR-0022). The money operands must share a currency, which the result
// takes; a money result with no currency, every money operand having been
// blank, is blank.
func decBinary(op Op, a, b Value, scale int, money bool) (Value, *EvalError) {
	cur := ""
	if a.kind == KindMoney {
		cur = a.str
	}
	if b.kind == KindMoney {
		if cur != "" && b.str != cur {
			return Value{}, currencyMismatch()
		}
		cur = b.str
	}
	ma, sa := exactOperand(a)
	mb, sb := exactOperand(b)
	var (
		m  int64
		s  int
		ok bool
	)
	switch op {
	case OpDecAdd, OpDecSub:
		x, y, s2, aligned := alignScales(ma, sa, mb, sb)
		if !aligned {
			return Value{}, overflow()
		}
		if op == OpDecSub {
			if y == math.MinInt64 {
				return Value{}, overflow()
			}
			y = -y
		}
		m, ok = addInt(x, y)
		s = s2
	case OpDecMul:
		m, ok = mulInt(ma, mb)
		s = sa + sb
	default:
		var e *EvalError
		m, s, e = decDiv(ma, sa, mb, sb)
		if e != nil {
			return Value{}, e
		}
		ok = true
	}
	if !ok {
		return Value{}, overflow()
	}
	if s != scale {
		m, ok = scaleBy(m, scale-s)
		if !ok {
			return Value{}, overflow()
		}
	}
	if money {
		if cur == "" {
			return Blank(), nil
		}
		return Money(m, scale, cur), nil
	}
	return Decimal(m, scale), nil
}

// compare orders two scalars for the comparison operators, exact values
// aside. The binder guarantees the same static kind or a blank on one
// side; a blank reads as the zero of the other side's kind (ADR-0019).
func compare(a, b Value) int {
	ka, kb := a.kind, b.kind
	if ka == KindNull && kb == KindNull {
		return 0
	}
	k := ka
	if k == KindNull {
		k = kb
	}
	switch k {
	case KindText, KindRef, KindJSON:
		switch {
		case a.str < b.str:
			return -1
		case a.str > b.str:
			return 1
		}
		return 0
	}
	switch {
	case a.num < b.num:
		return -1
	case a.num > b.num:
		return 1
	}
	return 0
}

// now is the context clock in seconds, the wall clock when unset.
func (vm *VM) now() int64 {
	if vm.ctx != nil && vm.ctx.Now != 0 {
		return vm.ctx.Now
	}
	return time.Now().Unix()
}
