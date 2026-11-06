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
// Errors are the closed list of ADR-0019 and are returned, never panicked.

// Context is what the context functions see (ADR-0015).
type Context struct {
	// User is the id of the user the formula runs for; empty for a system
	// recompute.
	User string
	// Groups are the groups the user belongs to.
	Groups []string
	// Now is the current time in seconds since the epoch, UTC. Zero means
	// the wall clock.
	Now int64
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

// Error codes (ADR-0019, ADR-0020).
const (
	ErrDivByZero       = "div_by_zero"
	ErrNotANumber      = "not_a_number"
	ErrBadText         = "bad_text"
	ErrOutOfRange      = "out_of_range"
	ErrLengthMismatch  = "length_mismatch"
	ErrTooManyElements = "too_many_elements"
	ErrQueryFailed     = "query_failed"
)

// VM executes programs. A VM is not safe for concurrent use; make one per
// goroutine. Its stack is reused across runs.
type VM struct {
	// Limits are the size limits of the database the programs belong to
	// (ADR-0021). NewVM sets the defaults; set others before Run.
	Limits Limits

	stack []Value
	ctx   *Context
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
	maxText := vm.maxText()
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
			if math.IsInf(r, 0) || math.IsNaN(r) {
				err = &EvalError{Code: ErrNotANumber, Msg: "the result is not a finite number"}
				break
			}
			sp--
			stack[sp-1] = Value{kind: KindNumber, num: r}
		case OpDiv, OpDivStrict:
			a, b := stack[sp-2].num, stack[sp-1].num
			var r float64
			if b == 0 {
				if in.Op == OpDivStrict {
					err = &EvalError{Code: ErrDivByZero, Msg: `\ by zero`}
					break
				}
			} else {
				r = a / b
				if math.IsInf(r, 0) || math.IsNaN(r) {
					err = &EvalError{Code: ErrNotANumber, Msg: "the result is not a finite number"}
					break
				}
			}
			sp--
			stack[sp-1] = Value{kind: KindNumber, num: r}
		case OpConcat:
			a, b := render(stack[sp-2]), render(stack[sp-1])
			if len(a)+len(b) > maxText && utf8.RuneCountInString(a)+utf8.RuneCountInString(b) > maxText {
				err = tooLong()
				break
			}
			sp--
			stack[sp-1] = Value{kind: KindText, str: a + b}
		case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
			c := compare(stack[sp-2], stack[sp-1])
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
		case OpLookup, OpQuery:
			err = &EvalError{Code: ErrQueryFailed, Msg: "query functions are not implemented yet"}
		case OpReturn:
			return stack[sp-1], nil
		default:
			err = &EvalError{Code: ErrQueryFailed, Msg: fmt.Sprintf("unknown opcode %d", in.Op)}
		}
		if err != nil {
			err.Pos = -1
			if p.Pos != nil {
				err.Pos = int(p.Pos[pc-1])
			}
			return Value{}, err
		}
	}
}

// compare orders two scalars for the comparison operators. The binder
// guarantees the same static type or a blank on one side; a blank reads as
// the zero of the other side's kind (ADR-0019).
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
