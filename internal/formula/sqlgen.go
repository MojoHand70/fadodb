package formula

import (
	"fmt"
	"strconv"
	"strings"
)

// SQL generation (ADR-0003, ADR-0019, ADR-0022). A Program becomes two SQL
// expressions over the columns that hold its variables: a value expression,
// and an error expression that yields the error code where the VM would
// stop and NULL elsewhere. The value is NULL or meaningless where the error
// is set, so a reader checks the error first. Both respect the short-
// circuit rules: an error in an untaken IF branch, or on the right of an
// AND or OR that was decided on the left, is not an error, and an error
// under IFERROR selects the fallback.
//
// The generator reads bytecode, not syntax, so it works on stored programs
// and on anything else that produces the format. It accepts the structured
// shapes the binder emits for IF, AND, OR and IFERROR and refuses other
// control flow. The core handles operators, control flow and the blank
// rules; a Dialect spells literals, casts, rendering, function calls and
// parameters for one engine.
//
// An exact value (decimal, int, money) is its mantissa as an integer
// expression at the static scale; a money value carries a second
// expression for its currency, NULL when the money is blank. A money
// variable is two columns: the amount and, under the name with the
// CurrencySuffix, the currency.

// CurrencySuffix is appended to a money variable's name for the column
// that holds its currency.
const CurrencySuffix = "_currency"

// SQL is a generated statement fragment.
type SQL struct {
	// Value is the value expression.
	Value string
	// Error is the error expression, or "" when the formula cannot fail.
	Error string
	// Currency is the currency expression of a money result, "" otherwise.
	Currency string
	// Kind is the kind of the value expression.
	Kind Kind
	// Scale is the scale of an exact value expression.
	Scale int8
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

// CurrencyOrNull returns the currency expression, or NULL when there is none.
func (s SQL) CurrencyOrNull() string {
	if s.Currency == "" {
		return "NULL"
	}
	return s.Currency
}

// SQLExpr is one translated expression with its static kind.
type SQLExpr struct {
	// Val is the value expression: the mantissa for an exact kind.
	Val string
	// Err is the error expression, or "" when the expression cannot fail.
	Err string
	// Cur is the currency expression of a money value, NULL for a blank.
	Cur string
	// Kind is the static kind of Val.
	Kind Kind
	// Scale is the scale of an exact Val.
	Scale int8
	// Sure reports that Val is never NULL, so no COALESCE is needed.
	Sure bool
	// IsConst and Const carry a number literal, which a dialect reads for
	// an argument that must be written in the formula, such as places.
	IsConst bool
	Const   float64
}

// Dialect spells one SQL engine.
type Dialect interface {
	Name() string
	NumberLit(f float64) string
	// IntLit spells an integer literal as the engine's 64-bit integer.
	IntLit(m int64) string
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
	// Mant reads an exact expression's mantissa as a non-null integer in
	// the type the dialect does exact arithmetic in: a blank is 0, a bool
	// 1 or 0. RawMant reads it with a blank kept NULL, for the functions
	// that skip blanks.
	Mant(e SQLExpr) string
	RawMant(e SQLExpr) string
	// Narrow brings an exact arithmetic result back to a 64-bit integer,
	// NULL when it does not fit, and OverflowErr is the matching check.
	Narrow(val string) string
	OverflowErr(val string) string
	// NotFinite is the condition that a numeric value is not a finite
	// number, the overflow check of ADR-0019.
	NotFinite(val string) string
	// ExactRowSum and NumberRowSum spell SUM over the rows of an aggregate
	// query (ADR-0046), each as the value and its error check. mant is
	// the raw mantissa of an exact expression, NULL for a blank: the value
	// is the total, NULL when no row has one, and the sum fails only when
	// the total does not fit. num is a number, NULL for a blank, and order
	// the ORDER BY clause of the source's key, or "": the rows are added
	// one by one in that order from zero, as the VM adds them.
	ExactRowSum(mant string) (val, err string)
	NumberRowSum(num, order string) (val, err string)
	// Pow spells x to the power y as the VM computes it.
	Pow(x, y string) string
	// Render applies the TEXT rule of ADR-0019 to a scalar.
	Render(e SQLExpr) string
	// Call translates a builtin by canonical name. Args are translated
	// already; the dialect reads their kinds.
	Call(name string, args []SQLExpr, params *Params) (SQLExpr, error)
	// Param references a context parameter by name.
	Param(name string) string
	// ColumnType is the SQL type of a column that stores values of the
	// kind, as the engine's tables declare it.
	ColumnType(k Kind) string
	// ContextArgs binds the context parameters for database/sql, in the
	// form the dialect's SQL expects.
	ContextArgs(ctx *Context) []any
	// Setup lists statements to run on a connection before the generated
	// SQL can be used: the DuckDB macros, nothing for SQLite.
	Setup() []string
	// Limits are the size limits the generated SQL enforces (ADR-0021).
	Limits() Limits
	// WithLimits returns the dialect under other limits.
	WithLimits(l Limits) Dialect
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

// NewParams makes an empty parameter list for a dialect, for callers that
// generate fragments with LookupSQL.
func NewParams(d Dialect) *Params { return &Params{ref: d.Param} }

// Names lists the parameters recorded so far.
func (p *Params) Names() []string { return append([]string(nil), p.names...) }

// SQLBinding says how a program's references are spelled in a statement.
type SQLBinding struct {
	// Column gives the column expression that holds each slot's value; for
	// a money slot it is asked again for the currency column, a text slot
	// named with CurrencySuffix. Column expressions must be qualified when
	// the program has a lookup, since they are referenced inside its
	// subquery.
	Column func(Slot) string
	// Table gives the table expression of each source a LOOKUP reads; nil
	// refers to the source by its quoted name.
	Table func(source string) string
	// Row gives the column that holds a system value of the document row
	// the statement runs over, asked for "state" and "id", which STATE()
	// and ID() read (ADR-0040); nil binds them to parameters, as USER().
	Row func(name string) string
}

// GenerateSQL translates a program for a dialect. column gives the column
// expression that holds each slot's value; for a money slot it is asked
// again for the currency column, a text slot named with CurrencySuffix.
// A program with a lookup refers to the source by its quoted name; a
// caller whose tables are named otherwise uses GenerateSQLWith. Column
// expressions must be qualified when the program has a lookup, since
// they are referenced inside its subquery.
func GenerateSQL(p *Program, d Dialect, column func(Slot) string) (SQL, error) {
	return GenerateSQLBound(p, d, SQLBinding{Column: column})
}

// GenerateSQLWith is GenerateSQL with the table expression of each source
// a LOOKUP reads given by table.
func GenerateSQLWith(p *Program, d Dialect, column func(Slot) string, table func(source string) string) (SQL, error) {
	return GenerateSQLBound(p, d, SQLBinding{Column: column, Table: table})
}

// GenerateSQLBound is GenerateSQL with every binding given.
func GenerateSQLBound(p *Program, d Dialect, b SQLBinding) (SQL, error) {
	if len(p.Code) == 0 || p.Code[len(p.Code)-1].Op != OpReturn {
		return SQL{}, fmt.Errorf("sqlgen: the program does not end with return")
	}
	if b.Table == nil {
		b.Table = quoteIdent
	}
	params := NewParams(d)
	t := &sqlTranslator{p: p, d: d, column: b.Column, table: b.Table, row: b.Row, params: params}
	e, err := t.block(0, len(p.Code)-1)
	if err != nil {
		return SQL{}, err
	}
	out := SQL{Value: e.Val, Error: e.Err, Kind: e.Kind, Scale: e.Scale, Params: params.names}
	if e.Kind == KindMoney {
		out.Currency = e.Cur
	}
	return out, nil
}

type sqlTranslator struct {
	p      *Program
	d      Dialect
	column func(Slot) string
	table  func(string) string
	row    func(string) string
	params *Params
}

// block translates code[start:end), which must leave exactly one value on
// the stack. Control flow is recovered from the binder's shapes:
//
//	IF:      cond; jump.if.false L1; then; jump L2; L1: else; L2:
//	AND:     left; and.jump L; right; to.bool; L:
//	OR:      left; or.jump L; right; to.bool; L:
//	IFERROR: try L1; value; end.try; jump L2; L1: fallback; L2:
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
			push(SQLExpr{Val: d.NumberLit(t.p.Nums[in.Arg]), Kind: KindNumber, Sure: true, IsConst: true, Const: t.p.Nums[in.Arg]})
		case OpConstDec:
			c := t.p.Decs[in.Arg]
			push(SQLExpr{Val: d.IntLit(c.Mant), Kind: KindDecimal, Scale: c.Scale, Sure: true})
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
			e := SQLExpr{Val: d.Load(t.column(slot), slot.Type.Kind), Kind: slot.Type.Kind, Scale: slot.Type.Scale}
			if slot.Type.Kind == KindMoney {
				e.Cur = d.Load(t.column(Slot{Name: slot.Name + CurrencySuffix, Type: Scalar(KindText)}), KindText)
			}
			push(e)
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
		case OpDecNeg, OpDecPercent, OpDecScale:
			if err := need(1, pc); err != nil {
				return SQLExpr{}, err
			}
			push(decUnary(d, in.Op, pop(), in.Arg))
		case OpAdd, OpSub, OpMul, OpDiv, OpDivStrict, OpPow:
			if err := need(2, pc); err != nil {
				return SQLExpr{}, err
			}
			b := pop()
			a := pop()
			push(arith(d, in.Op, a, b))
		case OpDecAdd, OpDecSub, OpDecMul, OpDecDiv:
			if err := need(2, pc); err != nil {
				return SQLExpr{}, err
			}
			b := pop()
			a := pop()
			push(decArith(d, in.Op, a, b, in.Arg))
		case OpConcat:
			if err := need(2, pc); err != nil {
				return SQLExpr{}, err
			}
			b := pop()
			a := pop()
			push(concatExpr(d, []SQLExpr{a, b}, true))
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
		case OpTry:
			handler := int(in.Arg)
			endTry, jumpPC := handler-2, handler-1
			if endTry <= pc || handler > end || code[endTry].Op != OpEndTry || code[jumpPC].Op != OpJump {
				return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: IFERROR without the binder's shape", pc)
			}
			endPC := int(code[jumpPC].Arg)
			if endPC < handler || endPC > end {
				return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: IFERROR without the binder's shape", pc)
			}
			val, err := t.block(pc+1, endTry)
			if err != nil {
				return SQLExpr{}, err
			}
			fb, err := t.block(handler, endPC)
			if err != nil {
				return SQLExpr{}, err
			}
			push(ifErrorExpr(d, val, fb))
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
		case OpJump, OpToBool, OpEndTry:
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
			switch {
			case name == "ISBLANK" || name == "ISNOTBLANK":
				e = blankTest(d, name == "ISBLANK", args[0])
			case (name == "STATE" || name == "ID") && t.row != nil:
				// Over document rows the state and the id are columns of
				// the row (ADR-0040); the dialect binds a parameter otherwise.
				e = SQLExpr{Val: d.Load(t.row(strings.ToLower(name)), KindText), Kind: KindText}
			case LifeColumns[name] != "":
				// A fact of the document's life is a system column of its
				// row (ADR-0047), and a parameter of that name where the
				// statement runs over no row.
				column, kind := LifeColumns[name], builtins[in.Arg>>8].result
				if t.row != nil {
					e = SQLExpr{Val: d.Load(t.row(column), kind), Kind: kind}
				} else {
					e = SQLExpr{Val: d.Load(t.params.Use(column), kind), Kind: kind}
				}
			default:
				e, err = d.Call(name, args, t.params)
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
			push(moneyBlank(e))
		case OpLookup:
			q := &t.p.Queries[in.Arg]
			arg := func(i int, currency bool) string {
				slot := t.p.Slots[q.Args[i]]
				if currency {
					return t.column(Slot{Name: slot.Name + CurrencySuffix, Type: Scalar(KindText)})
				}
				return t.column(slot)
			}
			e, err := LookupSQL(q, d, arg, t.table, t.params)
			if err != nil {
				return SQLExpr{}, err
			}
			push(e)
		case OpQuery:
			return SQLExpr{}, fmt.Errorf("sqlgen: instruction %d: QUERY is not implemented yet", pc)
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

// coercions implements Num, Cond, Text and Mant for a dialect from its
// zero literals and its casts.
type coercions struct {
	zeroNumber, zeroBool, zeroText, zeroMant string
	castNumber                               func(string) string
	castMant                                 func(string) string
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

// Mant reads an exact expression's mantissa as a non-null integer in the
// arithmetic type: a blank is 0, a bool 1 or 0.
func (c coercions) Mant(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return c.zeroMant
	case e.Kind == KindBool:
		return c.castMant(c.Cond(e))
	case e.Sure:
		return c.castMant(e.Val)
	}
	return c.castMant("COALESCE(" + e.Val + ", 0)")
}

// RawMant reads an exact expression's mantissa in the arithmetic type with
// a blank kept NULL.
func (c coercions) RawMant(e SQLExpr) string {
	switch e.Kind {
	case KindNull:
		return "NULL"
	case KindBool:
		return c.castMant(c.Cond(e))
	}
	return c.castMant(e.Val)
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
	case OpDiv, OpDivStrict:
		val = "(CASE WHEN " + y + " = " + zero + " THEN NULL ELSE " + x + " / " + y + " END)"
		own = coalesceErr("CASE WHEN "+y+" = "+zero+" THEN 'div_by_zero' END", notFiniteErr(d, val))
	case OpPow:
		val = d.Pow(x, y)
		own = notFiniteErr(d, val)
	}
	return SQLExpr{Val: val, Err: coalesceErr(a.Err, b.Err, own), Kind: KindNumber, Sure: op != OpDiv && op != OpDivStrict}
}

// rescaled spells a mantissa moved from one scale up to another, with the
// overflow check of the move, "" when there is no move: the VM checks
// every step, so the SQL must too.
func rescaled(d Dialect, mant string, from, to int) (string, string) {
	if to <= from {
		return mant, ""
	}
	val := "(" + mant + " * " + d.IntLit(pow10i[to-from]) + ")"
	return val, d.OverflowErr(val)
}

// decKind is the result kind of an exact operation from its operand.
func decResult(arg int32) (Kind, int8) {
	if arg&decMoneyFlag != 0 {
		return KindMoney, int8(arg & 0xff)
	}
	return KindDecimal, int8(arg & 0xff)
}

// currencyOf is the currency expression of a money result built from the
// operands: the one that is not NULL, so that a blank operand defers to
// the other; and the mismatch check when both are money.
func currencyOf(a, b SQLExpr) (cur, err string) {
	switch {
	case a.Kind == KindMoney && b.Kind == KindMoney:
		return "COALESCE(" + a.Cur + ", " + b.Cur + ")",
			"CASE WHEN " + a.Cur + " IS NOT NULL AND " + b.Cur + " IS NOT NULL AND " + a.Cur + " <> " + b.Cur + " THEN 'currency_mismatch' END"
	case a.Kind == KindMoney:
		return a.Cur, ""
	case b.Kind == KindMoney:
		return b.Cur, ""
	}
	return "", ""
}

// decUnary spells dec.neg, dec.percent and dec.scale.
func decUnary(d Dialect, op Op, x SQLExpr, arg int32) SQLExpr {
	kind, scale := decResult(arg)
	e := SQLExpr{Kind: kind, Scale: scale, Sure: true}
	if x.Kind == KindMoney {
		e.Cur = x.Cur
	} else if kind == KindMoney {
		e.Cur = "NULL"
	}
	m := d.Mant(x)
	switch op {
	case OpDecNeg:
		val := "(-" + m + ")"
		e.Val, e.Err = d.Narrow(val), coalesceErr(x.Err, d.OverflowErr(val))
	case OpDecPercent:
		e.Val, e.Err = d.Narrow(m), x.Err
	default:
		// dec.scale: the value as it is at the static scale, a blank kept
		// blank; brought up otherwise.
		if x.Scale == scale {
			e.Val, e.Err, e.Sure = x.Val, x.Err, x.Sure
			return e
		}
		val, re := rescaled(d, d.RawMant(x), int(x.Scale), int(scale))
		e.Val, e.Err, e.Sure = d.Narrow(val), coalesceErr(x.Err, re), x.Sure
		return e
	}
	return moneyBlank(e)
}

// moneyBlank makes a money result blank, NULL, where it has no currency,
// as the VM does, so that ISBLANK and COUNT agree.
func moneyBlank(e SQLExpr) SQLExpr {
	if e.Kind != KindMoney {
		return e
	}
	e.Val = "(CASE WHEN " + curOrNull(e) + " IS NULL THEN NULL ELSE " + e.Val + " END)"
	e.Sure = false
	return e
}

// decArith spells an exact +, -, * or / at the static result scale
// (ADR-0022), with the overflow and division checks the VM makes.
func decArith(d Dialect, op Op, a, b SQLExpr, arg int32) SQLExpr {
	kind, scale := decResult(arg)
	s := int(scale)
	x, y := d.Mant(a), d.Mant(b)
	cur, curErr := currencyOf(a, b)
	e := SQLExpr{Kind: kind, Scale: scale, Sure: true}
	if kind == KindMoney {
		e.Cur = cur
		if e.Cur == "" {
			e.Cur = "NULL"
		}
	}
	var val, own string
	switch op {
	case OpDecAdd, OpDecSub:
		sign := " + "
		if op == OpDecSub {
			sign = " - "
		}
		xv, xe := rescaled(d, x, int(a.Scale), s)
		yv, ye := rescaled(d, y, int(b.Scale), s)
		val = "(" + xv + sign + yv + ")"
		own = coalesceErr(xe, ye, d.OverflowErr(val))
		e.Val = d.Narrow(val)
	case OpDecMul:
		val = "(" + x + " * " + y + ")"
		own = d.OverflowErr(val)
		e.Val = d.Narrow(val)
	default:
		k := s - int(a.Scale) + int(b.Scale)
		list := x + ", " + y + ", " + strconv.Itoa(k)
		e.Val = "fado_dec_div(" + list + ")"
		own = "fado_dec_div_err(" + list + ")"
		e.Sure = false
	}
	e.Err = coalesceErr(a.Err, b.Err, curErr, own)
	return moneyBlank(e)
}

var sqlComparison = map[Op]string{OpEq: "=", OpNe: "<>", OpLt: "<", OpLe: "<=", OpGt: ">", OpGe: ">="}

// comparison compares two values of the same kind, or one blank: a blank
// reads as the zero of the other side's kind.
func comparison(d Dialect, op Op, a, b SQLExpr) SQLExpr {
	k := a.Kind
	if k == KindNull {
		k = b.Kind
	}
	var x, y, own string
	switch k {
	case KindText, KindRef, KindJSON:
		x, y = d.Text(a), d.Text(b)
	case KindBool:
		x, y = d.Cond(a), d.Cond(b)
	case KindDecimal, KindMoney:
		s := max(int(a.Scale), int(b.Scale))
		var xe, ye string
		x, xe = rescaled(d, d.Mant(a), int(a.Scale), s)
		y, ye = rescaled(d, d.Mant(b), int(b.Scale), s)
		// The currencies are checked before the operands align, as in the VM.
		_, curErr := currencyOf(a, b)
		own = coalesceErr(curErr, xe, ye)
	default:
		x, y = d.Num(a), d.Num(b)
	}
	return SQLExpr{Val: "(" + x + " " + sqlComparison[op] + " " + y + ")", Err: coalesceErr(a.Err, b.Err, own), Kind: KindBool, Sure: true}
}

// joined is the kind and scale two alternatives share, both at that scale,
// and the overflow checks of the moves, kept apart from the alternatives'
// own errors: the VM moves the chosen value after it has chosen.
func joined(d Dialect, then, els SQLExpr) (SQLExpr, SQLExpr, Kind, int8, string, string) {
	kind := then.Kind
	if kind == KindNull {
		kind = els.Kind
	}
	scale := max(then.Scale, els.Scale)
	var res [2]string
	if kind.IsExact() {
		for i, e := range []*SQLExpr{&then, &els} {
			if e.Kind.IsExact() && e.Scale < scale {
				val, re := rescaled(d, d.RawMant(*e), int(e.Scale), int(scale))
				e.Val, e.Scale, res[i] = d.Narrow(val), scale, re
			}
		}
	}
	return then, els, kind, scale, res[0], res[1]
}

// pick is the error expression of one alternative when it is taken,
// "" when it cannot fail.
func pick(when string, errs ...string) string {
	all := coalesceErr(errs...)
	if all == "" {
		return ""
	}
	return "CASE WHEN " + when + " THEN " + all + " END"
}

func ifExpr(d Dialect, c, then, els SQLExpr) SQLExpr {
	then, els, kind, scale, thenRe, elsRe := joined(d, then, els)
	cond := d.Cond(c)
	val := "(CASE WHEN " + cond + " THEN " + then.Val + " ELSE " + els.Val + " END)"
	branches := coalesceErr(pick(cond, then.Err, thenRe), pick("NOT "+cond, els.Err, elsRe))
	e := SQLExpr{Val: val, Err: coalesceErr(c.Err, branches), Kind: kind, Scale: scale, Sure: then.Sure && els.Sure && then.Kind != KindNull && els.Kind != KindNull}
	if kind == KindMoney {
		e.Cur = "(CASE WHEN " + cond + " THEN " + curOrNull(then) + " ELSE " + curOrNull(els) + " END)"
	}
	return e
}

func curOrNull(e SQLExpr) string {
	if e.Cur == "" {
		return "NULL"
	}
	return e.Cur
}

// ifErrorExpr is IFERROR: the fallback where the value's error expression
// is set, the value elsewhere, and the value's own errors swallowed.
func ifErrorExpr(d Dialect, val, fb SQLExpr) SQLExpr {
	if val.Err == "" {
		// The value cannot fail, so the fallback is never taken; the
		// value still moves to the joined scale, outside any catch.
		val, _, kind, scale, valRe, _ := joined(d, val, fb)
		val.Kind, val.Scale, val.Err = kind, scale, valRe
		return val
	}
	failed := "(" + val.Err + ") IS NOT NULL"
	val, fb, kind, scale, valRe, fbRe := joined(d, val, fb)
	e := SQLExpr{Val: "(CASE WHEN " + failed + " THEN " + fb.Val + " ELSE " + val.Val + " END)", Kind: kind, Scale: scale,
		Sure: val.Sure && fb.Sure && val.Kind != KindNull && fb.Kind != KindNull}
	e.Err = coalesceErr(pick(failed, fb.Err, fbRe), pick("NOT "+failed, valRe))
	if kind == KindMoney {
		e.Cur = "(CASE WHEN " + failed + " THEN " + curOrNull(fb) + " ELSE " + curOrNull(val) + " END)"
	}
	return e
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

// blankTest is ISBLANK and ISNOTBLANK, the same in every dialect. A money
// value is blank when it has no currency.
func blankTest(d Dialect, blank bool, x SQLExpr) SQLExpr {
	if x.Kind == KindNull {
		if blank {
			return SQLExpr{Val: d.True(), Kind: KindBool, Sure: true}
		}
		return SQLExpr{Val: d.False(), Kind: KindBool, Sure: true}
	}
	subject := x.Val
	if x.Kind == KindMoney {
		subject = curOrNull(x)
	}
	if blank {
		return SQLExpr{Val: "(" + subject + " IS NULL)", Kind: KindBool, Sure: true}
	}
	return SQLExpr{Val: "(" + subject + " IS NOT NULL)", Kind: KindBool, Sure: true}
}

// maxCount is where the dialects clamp a count, length or width before
// casting it to an integer, so that a huge number never overflows the
// cast. A count that large reads to the end of any text, since a text is
// at most the limit of ADR-0021.
const maxCount = "1000000000"

// countErr is the out_of_range check of a count, length or width argument:
// it must not be negative.
func countErr(n string) string {
	return "CASE WHEN " + n + " < 0 THEN 'out_of_range' END"
}

// positionErr is the out_of_range check of a 1-based position and, when n
// is not empty, a count after it.
func positionErr(p, n string) string {
	if n == "" {
		return "CASE WHEN " + p + " < 1 THEN 'out_of_range' END"
	}
	return "CASE WHEN " + p + " < 1 OR " + n + " < 0 THEN 'out_of_range' END"
}

// concatExpr joins scalars rendered as text under the text limit
// (ADR-0021). The error expression adds the lengths of the parts up, and
// the value is cut one past the limit, so that a chain of concatenations
// stays bounded whatever the error column says; each part is at most the
// limit by the same rule, so the join itself is bounded. withArgErrs
// includes the parts' own errors, which the call path adds itself.
func concatExpr(d Dialect, parts []SQLExpr, withArgErrs bool) SQLExpr {
	texts := make([]string, len(parts))
	var errs []string
	for i, p := range parts {
		texts[i] = d.Render(p)
		if withArgErrs {
			errs = append(errs, p.Err)
		}
	}
	e := SQLExpr{Kind: KindText, Sure: true}
	if len(texts) == 1 {
		e.Val = texts[0]
		e.Err = coalesceErr(errs...)
		return e
	}
	lens := make([]string, len(texts))
	for i, t := range texts {
		lens[i] = "length(" + t + ")"
	}
	max := d.Limits().MaxText
	e.Val = "substr(" + strings.Join(texts, " || ") + ", 1, " + strconv.Itoa(max+1) + ")"
	own := "CASE WHEN " + strings.Join(lens, " + ") + " > " + strconv.Itoa(max) + " THEN 'out_of_range' END"
	e.Err = coalesceErr(append(errs, own)...)
	return e
}

// placesConst reads a places argument the binder required to be a literal.
func placesConst(args []SQLExpr, i int) int {
	if i < len(args) && args[i].IsConst {
		return int(args[i].Const)
	}
	return 0
}

// methodConst reads a rounding method argument the binder required to be
// a literal, as a quoted SQL text; 'half_up' when absent.
func methodConst(d Dialect, args []SQLExpr, i int) string {
	if i < len(args) {
		return args[i].Val
	}
	return d.TextLit("half_up")
}

// exactArgs is the shared part of the exact-family functions in a dialect:
// the largest scale of the arguments, every mantissa brought to it, the
// currency of the result and the mismatch checks, and the kind.
func exactArgs(d Dialect, args []SQLExpr) (mants []string, scale int, kind Kind, cur string, errs []string) {
	kind = KindDecimal
	for _, a := range args {
		scale = max(scale, int(a.Scale))
		if a.Kind == KindMoney {
			kind = KindMoney
		}
	}
	var curs []string
	for _, a := range args {
		if a.Kind == KindMoney {
			curs = append(curs, a.Cur)
		}
	}
	// Every pair must agree, since a blank among them has no currency to
	// compare with; the VM checks the currencies before it aligns.
	for i := 0; i < len(curs); i++ {
		for j := i + 1; j < len(curs); j++ {
			errs = append(errs, "CASE WHEN "+curs[i]+" IS NOT NULL AND "+curs[j]+" IS NOT NULL AND "+curs[i]+" <> "+curs[j]+" THEN 'currency_mismatch' END")
		}
	}
	for _, a := range args {
		m, re := rescaled(d, d.Mant(a), int(a.Scale), scale)
		mants = append(mants, m)
		if re != "" {
			errs = append(errs, re)
		}
	}
	switch len(curs) {
	case 0:
	case 1:
		cur = curs[0]
	default:
		cur = "COALESCE(" + strings.Join(curs, ", ") + ")"
	}
	return mants, scale, kind, cur, errs
}

// HasOp reports whether the program contains the opcode.
func (p *Program) HasOp(op Op) bool {
	for _, in := range p.Code {
		if in.Op == op {
			return true
		}
	}
	return false
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

// LookupSQL translates one lookup slot into a correlated scalar subquery
// (ADR-0020): the column of the first row of the source, in key order, on
// which the condition holds, NULL when none does, or the slot's aggregate
// over those rows (ADR-0046). A row on which the condition would stop
// with an error does not match. arg gives the expression of the i-th THIS
// argument, and of its currency column when currency is set; table the
// expression of the source. The store's statement path and the
// generator's lookup instruction share it.
func LookupSQL(q *QuerySlot, d Dialect, arg func(i int, currency bool) string, table func(string) string, params *Params) (SQLExpr, error) {
	r, err := lookupRows(q, d, arg, table, params)
	if err != nil {
		return SQLExpr{}, err
	}
	if q.Agg != "" {
		a, err := r.aggregate()
		if err != nil {
			return SQLExpr{}, err
		}
		e := SQLExpr{Val: "(SELECT " + a.val + r.from + ")", Kind: q.Type.Kind, Scale: q.Type.Scale}
		if a.cur != "" {
			e.Cur = "(SELECT " + a.cur + r.from + ")"
		}
		errs := []string{a.rowErr}
		for _, x := range a.errs {
			errs = append(errs, "(SELECT "+x+r.from+")")
		}
		e.Err = coalesceErr(errs...)
		return e, nil
	}
	from := r.from + r.order + " LIMIT 1)"
	e := SQLExpr{Val: "(SELECT " + d.Load(lookupAlias+"."+quoteIdent(q.Column), q.Type.Kind) + from, Kind: q.Type.Kind, Scale: q.Type.Scale}
	if q.Type.Kind == KindMoney {
		e.Cur = "(SELECT " + lookupAlias + "." + quoteIdent(q.Column+CurrencySuffix) + from
	}
	return e, nil
}

// AggregateSQL translates an aggregate slot into one statement over the
// matching rows, for an executor that runs the lookup by itself: one row
// with the value as v, the currency of a money value as c and the error
// code as e, each NULL when there is none. The arguments are those of
// LookupSQL.
func AggregateSQL(q *QuerySlot, d Dialect, arg func(i int, currency bool) string, table func(string) string, params *Params) (string, error) {
	r, err := lookupRows(q, d, arg, table, params)
	if err != nil {
		return "", err
	}
	a, err := r.aggregate()
	if err != nil {
		return "", err
	}
	orNull := func(x string) string {
		if x == "" {
			return "NULL"
		}
		return x
	}
	return "SELECT " + a.val + " AS v, " + orNull(a.cur) + " AS c, " + orNull(coalesceErr(append([]string{a.rowErr}, a.errs...)...)) + " AS e" + r.from, nil
}

// lookupAlias names the source inside a lookup's subquery.
const lookupAlias = "q"

// rowsSQL is the matching rows of a lookup: the FROM and WHERE of its
// subquery, the order of the source's key, and what translates a program
// of the slot.
type rowsSQL struct {
	q           *QuerySlot
	d           Dialect
	from, order string
	sub         func(p *Program) (SQLExpr, error)
}

func lookupRows(q *QuerySlot, d Dialect, arg func(i int, currency bool) string, table func(string) string, params *Params) (*rowsSQL, error) {
	inner := func(s Slot) string {
		name, ok := strings.CutPrefix(s.Name, "THIS.")
		if !ok {
			return lookupAlias + "." + quoteIdent(s.Name)
		}
		if i := q.ArgIndex(s.Name); i >= 0 {
			return arg(i, false)
		}
		base := strings.TrimSuffix(name, CurrencySuffix)
		if i := q.ArgIndex("THIS." + base); i >= 0 && base != name {
			return arg(i, true)
		}
		return lookupAlias + "." + quoteIdent(name)
	}
	r := &rowsSQL{q: q, d: d}
	r.sub = func(p *Program) (SQLExpr, error) {
		t := &sqlTranslator{p: p, d: d, column: inner, table: table, params: params}
		return t.block(0, len(p.Code)-1)
	}
	cond, err := r.sub(q.Cond)
	if err != nil {
		return nil, fmt.Errorf("sqlgen: LOOKUP condition: %w", err)
	}
	where := d.Cond(cond)
	if cond.Err != "" {
		where = "(" + cond.Err + ") IS NULL AND " + where
	}
	if len(q.Order) > 0 {
		keys := make([]string, len(q.Order))
		for i, k := range q.Order {
			keys[i] = lookupAlias + "." + quoteIdent(k)
		}
		r.order = " ORDER BY " + strings.Join(keys, ", ")
	}
	r.from = " FROM " + table(q.Source) + " AS " + lookupAlias + " WHERE " + where
	return r, nil
}

// aggregateSQL is an aggregate over the matching rows: val, cur and errs
// are expressions of one SELECT over them, and rowErr a subquery of its
// own, the error of the first row in key order on which the aggregated
// expression fails.
type aggregateSQL struct {
	val, cur, rowErr string
	errs             []string
}

// aggregate spells the slot's aggregate with the engine's own aggregate
// functions, so that nothing crosses to the engine per row, and so that
// the answer is the one QuerySlot.Aggregate gives (ADR-0046).
func (r *rowsSQL) aggregate() (aggregateSQL, error) {
	q, d := r.q, r.d
	x, err := r.sub(q.Value)
	if err != nil {
		return aggregateSQL{}, fmt.Errorf("sqlgen: LOOKUP %s: %w", q.Agg, err)
	}
	var a aggregateSQL
	if x.Err != "" {
		a.rowErr = "(SELECT " + x.Err + r.from + " AND (" + x.Err + ") IS NOT NULL" + r.order + " LIMIT 1)"
	}
	exact := x.Kind.IsExact()
	// num is the value as a number and mant as a mantissa, each NULL for
	// a blank, which the engine's aggregates skip.
	num := x.Val
	switch x.Kind {
	case KindNull:
		num = "NULL"
	case KindBool:
		num = "(CASE WHEN " + x.Val + " IS NULL THEN NULL ELSE " + d.Num(x) + " END)"
	}
	mant := d.RawMant(x)
	if exact && x.Kind == KindMoney && q.Agg != "COUNT" {
		a.cur = "MIN(" + x.Cur + ")"
		a.errs = append(a.errs, "CASE WHEN COUNT(DISTINCT "+x.Cur+") > 1 THEN 'currency_mismatch' END")
	}
	switch q.Agg {
	case "COUNT":
		seen := x.Val
		if x.Kind == KindNull {
			seen = "NULL"
		}
		a.val = "CAST(COUNT(" + seen + ") AS " + d.ColumnType(KindNumber) + ")"
	case "MIN", "MAX":
		if exact {
			a.val = d.Narrow(q.Agg + "(" + mant + ")")
		} else {
			a.val = q.Agg + "(" + num + ")"
		}
	case "SUM":
		var e string
		if exact {
			a.val, e = d.ExactRowSum(mant)
			if x.Kind != KindMoney {
				a.val = "COALESCE(" + a.val + ", " + d.IntLit(0) + ")"
			}
		} else {
			a.val, e = d.NumberRowSum(num, r.order)
		}
		a.errs = append(a.errs, e)
	default:
		return aggregateSQL{}, fmt.Errorf("sqlgen: LOOKUP: unknown aggregate %s", q.Agg)
	}
	return a, nil
}
