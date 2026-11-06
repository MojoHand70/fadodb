package formula

import (
	"fmt"
	"strings"
)

// SQL generation (ADR-0003, ADR-0019). A Program becomes two SQL
// expressions over the columns that hold its variables: a value expression,
// and an error expression that yields the error code where the VM would
// stop and NULL elsewhere. The value is NULL or meaningless where the error
// is set, so a reader checks the error first. Both respect the short-
// circuit rules: an error in an untaken IF branch, or on the right of an
// AND or OR that was decided on the left, is not an error.
//
// The generator reads bytecode, not syntax, so it works on stored programs
// and on anything else that produces the format. It accepts the structured
// shapes the binder emits for IF, AND and OR and refuses other control
// flow. The core handles operators, control flow and the blank rules; a
// Dialect spells literals, casts, rendering, function calls and parameters
// for one engine.

// SQL is a generated statement fragment.
type SQL struct {
	// Value is the value expression.
	Value string
	// Error is the error expression, or "" when the formula cannot fail.
	Error string
	// Kind is the kind of the value expression.
	Kind Kind
	// Params lists the context parameters the expressions reference, by
	// name: now (seconds since the epoch, always bound, never NULL), user
	// (the user id or NULL) and groups (the user's groups or NULL, in the
	// form the dialect's ContextArgs binds).
	Params []string
}

// ErrorOrNull returns the error expression, or NULL when there is none.
func (s SQL) ErrorOrNull() string {
	if s.Error == "" {
		return "NULL"
	}
	return s.Error
}

// SQLExpr is one translated expression with its static kind.
type SQLExpr struct {
	// Val is the value expression.
	Val string
	// Err is the error expression, or "" when the expression cannot fail.
	Err string
	// Kind is the static kind of Val.
	Kind Kind
	// Sure reports that Val is never NULL, so no COALESCE is needed.
	Sure bool
}

// Dialect spells one SQL engine.
type Dialect interface {
	Name() string
	NumberLit(f float64) string
	TextLit(s string) string
	True() string
	False() string
	// Load references a column holding a variable of the kind, cast to the
	// SQL type the dialect computes with.
	Load(column string, k Kind) string
	// Num, Cond and Text read an expression as a number, a condition or
	// text with the blank rules of ADR-0005: a blank is 0, FALSE or "".
	Num(e SQLExpr) string
	Cond(e SQLExpr) string
	Text(e SQLExpr) string
	// NotFinite is the condition that a numeric value is not a finite
	// number, the overflow check of ADR-0019.
	NotFinite(val string) string
	// Render applies the TEXT rule of ADR-0019 to a scalar.
	Render(e SQLExpr) string
	// Call translates a builtin by canonical name. Args are translated
	// already; the dialect reads their kinds.
	Call(name string, args []SQLExpr, params *Params) (SQLExpr, error)
	// Param references a context parameter by name.
	Param(name string) string
	// ContextArgs binds the context parameters for database/sql, in the
	// form the dialect's SQL expects.
	ContextArgs(ctx *Context) []any
	// Setup lists statements to run on a connection before the generated
	// SQL can be used: the DuckDB macros, nothing for SQLite.
	Setup() []string
}

// Params records the context parameters a statement uses.
type Params struct {
	names []string
	ref   func(string) string
}

// Use records a parameter and returns its reference in the dialect.
func (p *Params) Use(name string) string {
	seen := false
	for _, n := range p.names {
		if n == name {
			seen = true
			break
		}
	}
	if !seen {
		p.names = append(p.names, name)
	}
	return p.ref(name)
}

// GenerateSQL translates a program for a dialect. column gives the column
// expression that holds each slot's value.
func GenerateSQL(p *Program, d Dialect, column func(Slot) string) (SQL, error) {
	if len(p.Code) == 0 || p.Code[len(p.Code)-1].Op != OpReturn {
		return SQL{}, fmt.Errorf("sqlgen: the program does not end with return")
	}
	t := &sqlTranslator{p: p, d: d, column: column, params: Params{ref: d.Param}}
	e, err := t.block(0, len(p.Code)-1)
	if err != nil {
		return SQL{}, err
	}
	return SQL{Value: e.Val, Error: e.Err, Kind: e.Kind, Params: t.params.names}, nil
}

type sqlTranslator struct {
	p      *Program
	d      Dialect
	column func(Slot) string
	params Params
}

// block translates code[start:end), which must leave exactly one value on
// the stack. Control flow is recovered from the binder's shapes:
//
//	IF:  cond; jump.if.false L1; then; jump L2; L1: else; L2:
//	AND: left; and.jump L; right; to.bool; L:
//	OR:  left; or.jump L; right; to.bool; L:
func (t *sqlTranslator) block(start, end int) (SQLExpr, error) {
	code := t.p.Code
	d := t.d
	var stack []SQLExpr
	push := func(e SQLExpr) { stack = append(stack, e) }
	pop := func() SQLExpr {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return e
	}
	need := func(n int, pc int) error {
		if len(stack) < n {
			return fmt.Errorf("sqlgen: instruction %d needs %d values, the stack has %d", pc, n, len(stack))
		}
		return nil
	}
	pc := start
	for pc < end {
		in := code[pc]
		switch in.Op {
		case OpNop:
		case OpConstNum:
			push(SQLExpr{Val: d.NumberLit(t.p.Nums[in.Arg]), Kind: KindNumber, Sure: true})
		case OpConstText:
			push(SQLExpr{Val: d.TextLit(t.p.Texts[in.Arg]), Kind: KindText, Sure: true})
		case OpTrue:
			push(SQLExpr{Val: d.True(), Kind: KindBool, Sure: true})
		case OpFalse:
			push(SQLExpr{Val: d.False(), Kind: KindBool, Sure: true})
		case OpBlank:
			push(SQLExpr{Val: "NULL", Kind: KindNull})
		case OpLoad:
			slot := t.p.Slots[in.Arg]
			if !slot.Type.IsScalar() {
				return SQLExpr{}, fmt.Errorf("sqlgen: %s is %s; composite values are not supported yet", slot.Name, slot.Type)
			}
			push(SQLExpr{Val: d.Load(t.column(slot), slot.Type.Kind), Kind: slot.Type.Kind})
		case OpNeg, OpPercent, OpNot:
			if err := need(1, pc); err != nil {
				return SQLExpr{}, err
			}
			x := pop()
			switch in.Op {
			case OpNeg:
				push(SQLExpr{Val: "(-" + d.Num(x) + ")", Err: x.Err, Kind: KindNumber, Sure: true})
			case OpPercent:
				push(SQLExpr{Val: "(" + d.Num(x) + " / " + d.NumberLit(100) + ")", Err: x.Err, Kind: KindNumber, Sure: true})
			default:
				push(SQLExpr{Val: "(NOT " + d.Cond(x) + ")", Err: x.Err, Kind: KindBool, Sure: true})
			}
		case OpAdd, OpSub, OpMul, OpDiv, OpDivStrict, OpPow:
			if err := need(2, pc); err != nil {
				return SQLExpr{}, err
			}
			b := pop()
			a := pop()
			push(arith(d, in.Op, a, b))
		case OpConcat:
			if err := need(2, pc); err != nil {
				return SQLExpr{}, err
			}
			b := pop()
			a := pop()
			push(SQLExpr{Val: "(" + d.Render(a) + " || " + d.Render(b) + ")", Err: coalesceErr(a.Err, b.Err), Kind: KindText, Sure: true})
		case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
			if err := need(2, pc); err != nil {
				return SQLExpr{}, err
			}
			b := pop()
			a := pop()
			push(comparison(d, in.Op, a, b))
		case OpJumpIfFalse:
			if err := need(1, pc); err != nil {
				return SQLExpr{}, err
			}
			c := pop()
			elseStart := int(in.Arg)
			jumpPC := elseStart - 1
			if jumpPC <= pc || jumpPC >= end || code[jumpPC].Op != OpJump {
				return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: IF without the binder's shape", pc)
			}
			endPC := int(code[jumpPC].Arg)
			if endPC < elseStart || endPC > end {
				return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: IF without the binder's shape", pc)
			}
			then, err := t.block(pc+1, jumpPC)
			if err != nil {
				return SQLExpr{}, err
			}
			els, err := t.block(elseStart, endPC)
			if err != nil {
				return SQLExpr{}, err
			}
			push(ifExpr(d, c, then, els))
			pc = endPC
			continue
		case OpAndJump, OpOrJump:
			if err := need(1, pc); err != nil {
				return SQLExpr{}, err
			}
			left := pop()
			after := int(in.Arg)
			toBool := after - 1
			if toBool <= pc || after > end || code[toBool].Op != OpToBool {
				return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: AND/OR without the binder's shape", pc)
			}
			right, err := t.block(pc+1, toBool)
			if err != nil {
				return SQLExpr{}, err
			}
			push(logical(d, in.Op == OpAndJump, left, right))
			pc = after
			continue
		case OpJump, OpToBool:
			return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: %s outside the binder's shapes", pc, in.Op)
		case OpCall:
			argc := int(in.Arg & 0xff)
			if err := need(argc, pc); err != nil {
				return SQLExpr{}, err
			}
			args := make([]SQLExpr, argc)
			copy(args, stack[len(stack)-argc:])
			stack = stack[:len(stack)-argc]
			name := builtins[in.Arg>>8].name
			var e SQLExpr
			var err error
			if name == "ISBLANK" || name == "ISNOTBLANK" {
				e = blankTest(d, name == "ISBLANK", args[0])
			} else {
				e, err = d.Call(name, args, &t.params)
				if err != nil {
					return SQLExpr{}, fmt.Errorf("sqlgen: %s: %w", name, err)
				}
			}
			errs := make([]string, 0, argc+1)
			for _, a := range args {
				errs = append(errs, a.Err)
			}
			errs = append(errs, e.Err)
			e.Err = coalesceErr(errs...)
			push(e)
		case OpLookup, OpQuery:
			return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: query functions are not implemented yet", pc)
		case OpReturn:
			return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: return inside a block", pc)
		default:
			return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: unknown opcode %d", pc, in.Op)
		}
		pc++
	}
	if len(stack) != 1 {
		return SQLExpr{}, fmt.Errorf("sqlgen: block %d..%d leaves %d values, want 1", start, end, len(stack))
	}
	return stack[0], nil
}

// coercions implements Num, Cond and Text for a dialect from its zero
// literals and its bool-to-number cast.
type coercions struct {
	zeroNumber, zeroBool, zeroText string
	castNumber                     func(string) string
}

// Num reads an expression as a number: a blank is 0, a bool is 1 or 0.
func (c coercions) Num(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return c.zeroNumber
	case e.Kind == KindBool:
		return c.castNumber(c.Cond(e))
	case e.Sure:
		return e.Val
	}
	return "COALESCE(" + e.Val + ", " + c.zeroNumber + ")"
}

// Cond reads an expression as a condition: a blank is FALSE.
func (c coercions) Cond(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return c.zeroBool
	case e.Sure:
		return e.Val
	}
	return "COALESCE(" + e.Val + ", " + c.zeroBool + ")"
}

// Text reads an expression as text: a blank is the empty text.
func (c coercions) Text(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return c.zeroText
	case e.Sure:
		return e.Val
	}
	return "COALESCE(" + e.Val + ", " + c.zeroText + ")"
}

// coalesceErr joins error expressions in evaluation order: the first error
// wins, as in the VM.
func coalesceErr(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	switch len(kept) {
	case 0:
		return ""
	case 1:
		return kept[0]
	}
	return "COALESCE(" + strings.Join(kept, ", ") + ")"
}

func notFiniteErr(d Dialect, val string) string {
	return "CASE WHEN " + d.NotFinite(val) + " THEN 'not_a_number' END"
}

func arith(d Dialect, op Op, a, b SQLExpr) SQLExpr {
	x, y := d.Num(a), d.Num(b)
	zero := d.NumberLit(0)
	var val, own string
	switch op {
	case OpAdd:
		val = "(" + x + " + " + y + ")"
		own = notFiniteErr(d, val)
	case OpSub:
		val = "(" + x + " - " + y + ")"
		own = notFiniteErr(d, val)
	case OpMul:
		val = "(" + x + " * " + y + ")"
		own = notFiniteErr(d, val)
	case OpDiv:
		val = "(CASE WHEN " + y + " = " + zero + " THEN " + zero + " ELSE " + x + " / " + y + " END)"
	case OpDivStrict:
		val = "(CASE WHEN " + y + " = " + zero + " THEN NULL ELSE " + x + " / " + y + " END)"
		own = "CASE WHEN " + y + " = " + zero + " THEN 'div_by_zero' END"
	case OpPow:
		val = "pow(" + x + ", " + y + ")"
		own = notFiniteErr(d, val)
	}
	return SQLExpr{Val: val, Err: coalesceErr(a.Err, b.Err, own), Kind: KindNumber, Sure: op != OpDivStrict}
}

var sqlComparison = map[Op]string{OpEq: "=", OpNe: "<>", OpLt: "<", OpLe: "<=", OpGt: ">", OpGe: ">="}

// comparison compares two values of the same kind, or one blank: a blank
// reads as the zero of the other side's kind.
func comparison(d Dialect, op Op, a, b SQLExpr) SQLExpr {
	k := a.Kind
	if k == KindNull {
		k = b.Kind
	}
	var x, y string
	switch k {
	case KindText, KindRef, KindJSON:
		x, y = d.Text(a), d.Text(b)
	case KindBool:
		x, y = d.Cond(a), d.Cond(b)
	default:
		x, y = d.Num(a), d.Num(b)
	}
	return SQLExpr{Val: "(" + x + " " + sqlComparison[op] + " " + y + ")", Err: coalesceErr(a.Err, b.Err), Kind: KindBool, Sure: true}
}

func ifExpr(d Dialect, c, then, els SQLExpr) SQLExpr {
	kind := then.Kind
	if kind == KindNull {
		kind = els.Kind
	}
	cond := d.Cond(c)
	val := "(CASE WHEN " + cond + " THEN " + then.Val + " ELSE " + els.Val + " END)"
	var branches string
	switch {
	case then.Err != "" && els.Err != "":
		branches = "CASE WHEN " + cond + " THEN " + then.Err + " ELSE " + els.Err + " END"
	case then.Err != "":
		branches = "CASE WHEN " + cond + " THEN " + then.Err + " END"
	case els.Err != "":
		branches = "CASE WHEN NOT " + cond + " THEN " + els.Err + " END"
	}
	return SQLExpr{Val: val, Err: coalesceErr(c.Err, branches), Kind: kind, Sure: then.Sure && els.Sure && then.Kind != KindNull && els.Kind != KindNull}
}

// logical is AND or OR with the right side evaluated only when the left
// has not decided the result.
func logical(d Dialect, and bool, left, right SQLExpr) SQLExpr {
	l, r := d.Cond(left), d.Cond(right)
	var val, when string
	if and {
		val = "(" + l + " AND " + r + ")"
		when = l
	} else {
		val = "(" + l + " OR " + r + ")"
		when = "NOT " + l
	}
	var rightErr string
	if right.Err != "" {
		rightErr = "CASE WHEN " + when + " THEN " + right.Err + " END"
	}
	return SQLExpr{Val: val, Err: coalesceErr(left.Err, rightErr), Kind: KindBool, Sure: true}
}

// blankTest is ISBLANK and ISNOTBLANK, the same in every dialect.
func blankTest(d Dialect, blank bool, x SQLExpr) SQLExpr {
	if x.Kind == KindNull {
		if blank {
			return SQLExpr{Val: d.True(), Kind: KindBool, Sure: true}
		}
		return SQLExpr{Val: d.False(), Kind: KindBool, Sure: true}
	}
	if blank {
		return SQLExpr{Val: "(" + x.Val + " IS NULL)", Kind: KindBool, Sure: true}
	}
	return SQLExpr{Val: "(" + x.Val + " IS NOT NULL)", Kind: KindBool, Sure: true}
}

// nonNegativeErr is the out_of_range check of a count or width argument.
func nonNegativeErr(x string) string {
	return "CASE WHEN " + x + " < 0 THEN 'out_of_range' END"
}

// Calls reports whether the program calls the named builtin.
func (p *Program) Calls(name string) bool {
	id, ok := builtinByName[name]
	if !ok {
		return false
	}
	for _, in := range p.Code {
		if in.Op == OpCall && int(in.Arg>>8) == id {
			return true
		}
	}
	return false
}
