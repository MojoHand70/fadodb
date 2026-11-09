package formula

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The SQLite dialect. Numbers are REAL throughout, so integer division
// never happens on them; a date is REAL days and a datetime REAL seconds;
// booleans are INTEGER 0 and 1; blank is NULL. An exact value is its
// INTEGER mantissa, and an integer operation that overflows turns REAL in
// SQLite, which typeof detects (ADR-0022). Where SQLite's own function
// differs from the rule, the dialect calls the engine's Go implementation
// registered as fado_<name> (ADR-0019), with fado_<name>_err returning the
// error code: SQLite rounds 0.49999999999999994 up, ignores negative digit
// counts, upper-cases ASCII only, keeps inner spaces in trim, takes the
// dividend's sign in mod, does not clamp month arithmetic, normalises
// February 30 and never fails a cast; its pow, ln and log are the
// platform's, which differ from the VM in the last bit between Linux,
// macOS and Windows; and its printf rounds the fifteenth significant digit
// its own way. Exact division, rounding, parsing and rendering are
// registered too, so that the mantissas agree with the VM by construction.
// The sqlitefn package does the registration.

// SQLite is the dialect for SQLite through modernc.org/sqlite.
type SQLite struct {
	coercions
	lim Limits
}

// NewSQLite makes the SQLite dialect under the default limits.
func NewSQLite() SQLite {
	return SQLite{coercions: coercions{zeroNumber: "0.0", zeroBool: "0", zeroText: "''", zeroMant: "0",
		castNumber: func(x string) string { return "CAST(" + x + " AS REAL)" },
		castMant:   func(x string) string { return "CAST(" + x + " AS INTEGER)" }}, lim: DefaultLimits()}
}

// Limits implements Dialect.
func (d SQLite) Limits() Limits { return d.lim }

// WithLimits implements Dialect.
func (d SQLite) WithLimits(l Limits) Dialect {
	d.lim = l
	return d
}

// Name implements Dialect.
func (SQLite) Name() string { return "sqlite" }

// True implements Dialect: SQLite has no bool type, so 1 and 0.
func (SQLite) True() string { return "1" }

// False implements Dialect.
func (SQLite) False() string { return "0" }

// NotFinite implements Dialect. SQLite yields NULL for NaN and keeps
// infinities, so both are checked.
func (SQLite) NotFinite(val string) string {
	return "(" + val + " IS NULL OR abs(" + val + ") > " + maxFloat + ")"
}

// Narrow implements Dialect: an integer result is already 64-bit.
func (SQLite) Narrow(val string) string { return val }

// OverflowErr implements Dialect: an integer operation that overflows
// turns REAL in SQLite; a NULL is a blank, not an overflow.
func (SQLite) OverflowErr(val string) string {
	return "CASE WHEN typeof(" + val + ") = 'real' THEN 'overflow' END"
}

// Pow implements Dialect: the engine's own power function, not SQLite's.
func (SQLite) Pow(x, y string) string { return "fado_power(" + x + ", " + y + ")" }

// Param implements Dialect.
func (SQLite) Param(name string) string { return ":" + name }

// ColumnType implements Dialect: SQLite's storage classes, a date as
// INTEGER days and a datetime as INTEGER seconds (ADR-0023), an exact
// value as its INTEGER mantissa (ADR-0022), a bool as 0 or 1.
func (SQLite) ColumnType(k Kind) string {
	switch k {
	case KindNumber:
		return "REAL"
	case KindBool, KindDecimal, KindMoney, KindDate, KindDateTime:
		return "INTEGER"
	}
	return "TEXT"
}

// ContextArgs implements Dialect: now is always bound, user is text or
// NULL, groups is a JSON array or NULL.
func (SQLite) ContextArgs(ctx *Context) []any {
	now, user, state, id, groups := contextValues(ctx)
	var g any
	if groups != nil {
		b, _ := json.Marshal(groups)
		g = string(b)
	}
	return []any{sql.Named("now", now), sql.Named("user", user), sql.Named("groups", g), sql.Named("state", state), sql.Named("id", id)}
}

// Setup implements Dialect: the engine functions come from the sqlitefn
// package's registration, not from statements.
func (SQLite) Setup() []string { return nil }

// contextValues is what every dialect binds: the clock in seconds, always
// present; the user id, the document's state and id, each or nil; the
// groups or nil.
func contextValues(ctx *Context) (now int64, user, state, id any, groups []string) {
	now = time.Now().Unix()
	if ctx != nil {
		if ctx.Now != 0 {
			now = ctx.Now
		}
		if ctx.User != "" {
			user = ctx.User
		}
		if ctx.State != "" {
			state = ctx.State
		}
		if ctx.ID != "" {
			id = ctx.ID
		}
		groups = ctx.Groups
	}
	return now, user, state, id, groups
}

// maxFloat is the largest finite float64, the bound of the overflow checks.
const maxFloat = "1.7976931348623157e308"

// NumberLit implements Dialect. The literal always reads as REAL.
func (SQLite) NumberLit(f float64) string { return numberLiteral(f) }

func numberLiteral(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	if f < 0 {
		return "(" + s + ")"
	}
	return s
}

// IntLit implements Dialect.
func (SQLite) IntLit(m int64) string {
	if m < 0 {
		return "(" + strconv.FormatInt(m, 10) + ")"
	}
	return strconv.FormatInt(m, 10)
}

// TextLit implements Dialect.
func (SQLite) TextLit(s string) string { return textLiteral(s) }

func textLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Load implements Dialect.
func (SQLite) Load(column string, k Kind) string {
	switch k {
	case KindNumber, KindDate, KindDateTime:
		return "CAST(" + column + " AS REAL)"
	case KindDecimal, KindMoney:
		return "CAST(" + column + " AS INTEGER)"
	}
	return column
}

// Render implements Dialect: the TEXT rule of ADR-0019 and ADR-0022.
func (d SQLite) Render(e SQLExpr) string {
	switch e.Kind {
	case KindNull:
		return "''"
	case KindNumber:
		// The engine's own rendering: SQLite's printf('%.15g') is not
		// correctly rounded, and a NULL renders as the empty text.
		return "fado_text(" + e.Val + ")"
	case KindBool:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' WHEN " + e.Val + " THEN 'TRUE' ELSE 'FALSE' END)"
	case KindDecimal:
		return "COALESCE(fado_dec_text(" + e.Val + ", " + strconv.Itoa(int(e.Scale)) + "), '')"
	case KindMoney:
		return "(CASE WHEN " + curOrNull(e) + " IS NULL THEN '' ELSE fado_dec_text(" + e.Val + ", " + strconv.Itoa(int(e.Scale)) + ") || ' ' || " + e.Cur + " END)"
	case KindDate:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' ELSE strftime('%Y-%m-%d', " + e.Val + " * 86400.0, 'unixepoch') END)"
	case KindDateTime:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' ELSE strftime('%Y-%m-%d %H:%M:%S', " + e.Val + ", 'unixepoch') END)"
	}
	return d.Text(e)
}

// SQLiteFunctions lists the engine functions the SQLite dialect expects to
// be registered, as fado_<name> and fado_<name>_err: the builtins named in
// sqliteRegistered under their own arguments, then the exact-arithmetic
// helpers. The sqlitefn package registers them from this list.
func SQLiteFunctions() []RegisteredFunc {
	var out []RegisteredFunc
	for _, name := range sqliteRegistered {
		b := builtins[builtinByName[name]]
		rf := RegisteredFunc{Name: "fado_" + strings.ToLower(name), NArgs: len(b.args), Fn: b.fn, Limited: limitedFunctions[name]}
		if len(b.args) > 0 && LookupFunction(name).MaxArgs() < 0 {
			rf.NArgs = -1
		} else if rf.Limited {
			rf.NArgs++
		}
		rf.Kinds = make([]Kind, len(b.args))
		for i, a := range b.args {
			rf.Kinds[i] = argKindKind(a)
		}
		out = append(out, rf)
	}
	return append(out, sqliteExact...)
}

// sqliteRegistered names the builtins the SQLite dialect calls through
// registered Go functions.
var sqliteRegistered = []string{
	"ROUND", "FLOOR", "CEILING", "TRUNC", "MIN", "MAX", "SUM", "AVERAGE", "COUNT", "POWER", "LN", "LOG",
	"DATE", "DATEVALUE", "ADDMONTHS", "ADDYEARS",
	"TRIM", "UPPER", "LOWER", "PROPER", "REPLACE", "SUBSTITUTE", "REPEAT", "FINDLAST", "PAD", "REGEXREPLACE",
	"TEXT", "VALUE", "JSONVALUE", "DATETIMEVALUE",
}

// limitedFunctions names the builtins whose result can be longer than
// their inputs. The dialects pass them the text limit of ADR-0021 as one
// extra trailing argument, an integer literal, since a registered function
// and a macro have no VM to read it from.
var limitedFunctions = map[string]bool{
	"REPLACE": true, "SUBSTITUTE": true, "REPEAT": true, "PAD": true, "REGEXREPLACE": true,
}

// RegisteredFunc describes an engine function a SQL dialect calls back into
// Go (ADR-0019).
type RegisteredFunc struct {
	// Name is the SQL function name.
	Name string
	// NArgs is the argument count, the limit included when Limited, or -1
	// for variadic.
	NArgs int
	// Kinds gives the kind expected at each position; the last repeats. A
	// KindDecimal position receives a raw mantissa, scale 0.
	Kinds []Kind
	// Fn is the implementation, the VM's own.
	Fn func(vm *VM, args []Value) (Value, *EvalError)
	// Limited: the last argument is the text limit, and Fn reads it from
	// the VM it is called with.
	Limited bool
}

// Kind returns the kind expected at argument position i.
func (f RegisteredFunc) Kind(i int) Kind {
	if len(f.Kinds) == 0 {
		return KindNull
	}
	if i >= len(f.Kinds) {
		return f.Kinds[len(f.Kinds)-1]
	}
	return f.Kinds[i]
}

func argKindKind(a argKind) Kind {
	switch a {
	case akNumber, akNumeric:
		return KindNumber
	case akExact, akMoney:
		return KindDecimal
	case akText:
		return KindText
	case akDate:
		return KindDate
	case akDateTime, akDateLike:
		return KindDateTime
	case akCondition:
		return KindBool
	case akJSON:
		return KindJSON
	}
	return KindNull
}

// sqliteExact are the exact-arithmetic helpers (ADR-0022), on raw
// mantissas: the same Go steps the VM takes, so that SQLite agrees with it
// by construction.
var sqliteExact = []RegisteredFunc{
	// fado_dec_div(x, y, k): round(x * 10^k / y) half away from zero.
	{Name: "fado_dec_div", NArgs: 3, Kinds: []Kind{KindDecimal, KindDecimal, KindNumber},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			k := int(args[2].num)
			if args[1].Mant() == 0 {
				return Value{}, &EvalError{Code: ErrDivByZero, Msg: "/ by zero"}
			}
			x, ok := scaleBy(args[0].Mant(), k)
			if !ok {
				return Value{}, overflow()
			}
			q, ok := divRound(x, args[1].Mant())
			if !ok {
				return Value{}, overflow()
			}
			return Decimal(q, 0), nil
		}},
	// fado_dec_round(m, s, places, method): the mantissa at scale max(places, 0).
	{Name: "fado_dec_round", NArgs: 4, Kinds: []Kind{KindDecimal, KindNumber, KindNumber, KindText},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			if args[0].kind == KindNull {
				return Blank(), nil
			}
			m, _, ok := roundMant(args[0].Mant(), int(args[1].num), int(args[2].num), methodOf(args, 3))
			if !ok {
				return Value{}, overflow()
			}
			return Decimal(m, 0), nil
		}},
	// fado_dec_text(m, s): the TEXT rendering.
	{Name: "fado_dec_text", NArgs: 2, Kinds: []Kind{KindDecimal, KindNumber},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			if args[0].kind == KindNull {
				return Blank(), nil
			}
			return Text(formatDecimal(args[0].Mant(), int(args[1].num))), nil
		}},
	// fado_dec_parse(t, places): DECIMALVALUE.
	{Name: "fado_dec_parse", NArgs: 2, Kinds: []Kind{KindText, KindNumber},
		Fn: func(vm *VM, args []Value) (Value, *EvalError) {
			v, err := fnDecimalValue(vm, args)
			if err != nil || v.kind == KindNull {
				return v, err
			}
			return Decimal(v.Mant(), 0), nil
		}},
	// fado_money_amount(t, places) and fado_money_cur(t): MONEYVALUE.
	{Name: "fado_money_amount", NArgs: 2, Kinds: []Kind{KindText, KindNumber},
		Fn: func(vm *VM, args []Value) (Value, *EvalError) {
			v, err := fnMoneyValue(vm, args)
			if err != nil || v.kind == KindNull {
				return v, err
			}
			return Decimal(v.Mant(), 0), nil
		}},
	{Name: "fado_money_cur", NArgs: 1, Kinds: []Kind{KindText},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			s := strings.TrimSpace(args[0].str)
			if s == "" {
				return Blank(), nil
			}
			_, _, cur, err := parseMoney(s)
			if err != nil {
				return Blank(), nil
			}
			return Text(cur), nil
		}},
	// fado_currency(t): a currency code or NULL, bad_text otherwise.
	{Name: "fado_currency", NArgs: 1, Kinds: []Kind{KindText},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			cur, ok := parseCurrency(args[0].str)
			if !ok {
				return Value{}, badCurrency(args[0].str)
			}
			return Text(cur), nil
		}},
	// fado_dec_from(x, places, method): DECIMAL of a number.
	{Name: "fado_dec_from", NArgs: 3, Kinds: []Kind{KindNumber, KindNumber, KindText},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			if args[0].kind == KindNull {
				return Blank(), nil
			}
			m, err := decFromFloat(args[0].num, int(args[1].num), methodOf(args, 2))
			if err != nil {
				return Value{}, err
			}
			return Decimal(m, 0), nil
		}},
	// fado_dec_mod(a, b) on aligned mantissas.
	{Name: "fado_dec_mod", NArgs: 2, Kinds: []Kind{KindDecimal, KindDecimal},
		Fn: func(_ *VM, args []Value) (Value, *EvalError) {
			r, err := decMod(args[0].Mant(), args[1].Mant())
			if err != nil {
				return Value{}, err
			}
			return Decimal(r, 0), nil
		}},
	// fado_dec_min and fado_dec_max over aligned mantissas, blanks skipped;
	// the mantissas are at one scale already, which the VM's functions
	// take as their trailing scale argument.
	{Name: "fado_dec_min", NArgs: -1, Kinds: []Kind{KindDecimal}, Fn: aligned(fnMin)},
	{Name: "fado_dec_max", NArgs: -1, Kinds: []Kind{KindDecimal}, Fn: aligned(fnMax)},
}

// aligned adapts a family function to mantissas already at one scale: it
// appends the scale 0 the function reads as its trailing argument.
func aligned(fn func(*VM, []Value) (Value, *EvalError)) func(*VM, []Value) (Value, *EvalError) {
	return func(vm *VM, args []Value) (Value, *EvalError) {
		all := make([]Value, 0, len(args)+1)
		all = append(all, args...)
		return fn(vm, append(all, Decimal(0, 0)))
	}
}

// ValueFromDriver converts a value as a database driver delivers it (nil,
// int64, float64, string or []byte) to a Value of the expected kind.
// KindNull means "whatever it is": numbers become numbers and text text.
// KindDecimal reads a raw mantissa at scale 0; ValueOfType reads a typed
// value with its scale and currency.
func ValueFromDriver(v any, k Kind) Value {
	if v == nil {
		return Blank()
	}
	switch x := v.(type) {
	case []byte:
		v = string(x)
	case bool:
		if x {
			v = int64(1)
		} else {
			v = int64(0)
		}
	}
	switch k {
	case KindDecimal, KindMoney:
		switch x := v.(type) {
		case int64:
			return Decimal(x, 0)
		case float64:
			return Decimal(int64(x), 0)
		case string:
			m, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				return Blank()
			}
			return Decimal(m, 0)
		}
		return Blank()
	case KindNumber, KindDate, KindDateTime, KindBool, KindNull:
		var f float64
		switch x := v.(type) {
		case int64:
			f = float64(x)
		case float64:
			f = x
		case string:
			if k == KindNull {
				return Text(x)
			}
			g, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return Blank()
			}
			f = g
		default:
			return Blank()
		}
		switch k {
		case KindDate:
			return Date(int64(f))
		case KindDateTime:
			return DateTime(int64(f))
		case KindBool:
			return Bool(f != 0)
		}
		return Number(f)
	case KindText, KindRef, KindJSON:
		var s string
		switch x := v.(type) {
		case string:
			s = x
		case int64:
			s = strconv.FormatInt(x, 10)
		case float64:
			s = renderNumber(x)
		default:
			return Blank()
		}
		switch k {
		case KindRef:
			return Ref(s)
		case KindJSON:
			return JSON(s)
		}
		return Text(s)
	}
	return Blank()
}

// ValueOfType converts a driver value to a Value of a static type: an
// exact value gets the type's scale, a money value its currency from cur,
// and is blank when the currency is.
func ValueOfType(v any, t Type, cur any) Value {
	switch t.Kind {
	case KindDecimal:
		m := ValueFromDriver(v, KindDecimal)
		if m.kind == KindNull {
			return Blank()
		}
		return Decimal(m.Mant(), int(t.Scale))
	case KindMoney:
		m := ValueFromDriver(v, KindDecimal)
		c := ValueFromDriver(cur, KindText)
		if m.kind == KindNull || c.kind == KindNull {
			return Blank()
		}
		return Money(m.Mant(), int(t.Scale), c.str)
	}
	return ValueFromDriver(v, t.Kind)
}

// DriverValue converts a Value to what a database driver takes: nil,
// float64, int64 or string. A date is REAL days, a datetime REAL seconds,
// a bool 0 or 1, an exact value its mantissa; a money's currency is
// bound apart, from Currency.
func (v Value) DriverValue() any {
	switch v.kind {
	case KindNull:
		return nil
	case KindBool:
		return int64(v.num)
	case KindNumber, KindDate, KindDateTime:
		return v.num
	case KindDecimal, KindMoney:
		return v.Mant()
	}
	return v.str
}

// intArg truncates a number to an integer for a position or a count,
// clamped to -1 below and to one past maxCount above, so that a negative
// argument is still seen and the cast cannot overflow.
func intArg(x string) string {
	return "CAST(min(max(trunc(" + x + "), -1), " + maxCount + " + 1) AS INTEGER)"
}

// secsOf spells a date or datetime expression in seconds.
func secsOf(e SQLExpr) string {
	if e.Kind == KindDate {
		return "(" + e.Val + " * 86400.0)"
	}
	return e.Val
}

// Call implements Dialect.
func (d SQLite) Call(name string, args []SQLExpr, params *Params) (SQLExpr, error) {
	reg := func(kind Kind, withErr bool, sqlArgs ...string) SQLExpr {
		fn := "fado_" + strings.ToLower(name)
		if limitedFunctions[name] {
			sqlArgs = append(sqlArgs, strconv.Itoa(d.lim.MaxText))
		}
		list := strings.Join(sqlArgs, ", ")
		e := SQLExpr{Val: fn + "(" + list + ")", Kind: kind}
		if withErr {
			e.Err = fn + "_err(" + list + ")"
		}
		return e
	}
	numOr := func(i int, def string) string {
		if i < len(args) {
			return d.Num(args[i])
		}
		return def
	}
	raw := func(e SQLExpr) string { return e.Val }
	rawNum := func(e SQLExpr) string {
		if e.Kind == KindBool {
			return "CAST(" + e.Val + " AS REAL)"
		}
		return e.Val
	}
	rawMant := d.RawMant
	exact := len(args) > 0 && args[0].Kind.IsExact()
	for _, a := range args {
		exact = exact || a.Kind.IsExact()
	}
	year := func(v string) string { return "CAST(strftime('%Y', " + v + ", 'unixepoch') AS REAL)" }
	month := func(v string) string { return "CAST(strftime('%m', " + v + ", 'unixepoch') AS REAL)" }
	switch name {
	case "ROUND", "FLOOR", "CEILING", "TRUNC":
		if args[0].Kind.IsExact() {
			method := d.TextLit(map[string]string{"ROUND": "half_up", "FLOOR": "floor", "CEILING": "ceiling", "TRUNC": "down"}[name])
			if name == "ROUND" {
				method = methodConst(d, args, 2)
			}
			places := placesConst(args, 1)
			list := d.Mant(args[0]) + ", " + strconv.Itoa(int(args[0].Scale)) + ", " + strconv.Itoa(places) + ", " + method
			return SQLExpr{Val: "fado_dec_round(" + list + ")", Err: "fado_dec_round_err(" + list + ")", Kind: args[0].Kind, Scale: int8(max(places, 0)), Cur: args[0].Cur, Sure: args[0].Sure}, nil
		}
		list := []string{d.Num(args[0]), numOr(1, "0.0")}
		if name == "ROUND" {
			list = append(list, methodConst(d, args, 2))
		}
		e := reg(KindNumber, false, list...)
		e.Sure = true
		return e, nil
	case "ABS":
		if exact {
			m := d.Mant(args[0])
			return SQLExpr{Val: "(CASE WHEN " + m + " = -9223372036854775808 THEN NULL ELSE abs(" + m + ") END)",
				Err: "CASE WHEN " + m + " = -9223372036854775808 THEN 'overflow' END", Kind: args[0].Kind, Scale: args[0].Scale, Cur: args[0].Cur}, nil
		}
		return SQLExpr{Val: "abs(" + d.Num(args[0]) + ")", Kind: KindNumber, Sure: true}, nil
	case "SIGN":
		if exact {
			x := d.Mant(args[0])
			return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN 1 WHEN " + x + " < 0 THEN -1 ELSE 0 END)", Kind: KindDecimal, Sure: true}, nil
		}
		x := d.Num(args[0])
		return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN 1.0 WHEN " + x + " < 0 THEN -1.0 ELSE 0.0 END)", Kind: KindNumber, Sure: true}, nil
	case "MIN", "MAX", "SUM", "AVERAGE":
		if exact {
			return d.exactAggregate(name, args[:len(args)-1]), nil
		}
		fallthrough
	case "COUNT":
		list := make([]string, len(args))
		for i, a := range args {
			if name == "COUNT" {
				list[i] = raw(a)
			} else {
				list[i] = rawNum(a)
			}
		}
		e := reg(KindNumber, name == "SUM" || name == "AVERAGE", list...)
		e.Sure = name == "COUNT" || name == "SUM"
		return e, nil
	case "POWER":
		return arith(d, OpPow, args[0], args[1]), nil
	case "SQRT":
		x := d.Num(args[0])
		return SQLExpr{Val: "sqrt(" + x + ")", Err: "CASE WHEN " + x + " < 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "EXP":
		val := "exp(" + d.Num(args[0]) + ")"
		return SQLExpr{Val: val, Err: notFiniteErr(d, val), Kind: KindNumber, Sure: true}, nil
	case "LN":
		return reg(KindNumber, true, d.Num(args[0])), nil
	case "LOG":
		return reg(KindNumber, true, d.Num(args[0]), numOr(1, "10.0")), nil
	case "MOD":
		if exact {
			mants, scale, kind, cur, errs := exactArgs(d, args[:len(args)-1])
			list := mants[0] + ", " + mants[1]
			e := SQLExpr{Val: "fado_dec_mod(" + list + ")", Err: coalesceErr(append(errs, "fado_dec_mod_err("+list+")")...), Kind: kind, Scale: int8(scale), Cur: cur}
			return e, nil
		}
		a, b := d.Num(args[0]), d.Num(args[1])
		val := "(" + a + " - " + b + " * floor(" + a + " / " + b + "))"
		return SQLExpr{Val: val, Err: coalesceErr("CASE WHEN "+b+" = 0 THEN 'div_by_zero' END", notFiniteErr(d, val)), Kind: KindNumber}, nil
	case "DECIMAL":
		places := placesConst(args, 1)
		method := methodConst(d, args, 2)
		x := args[0]
		if x.Kind.IsExact() {
			list := rawMant(x) + ", " + strconv.Itoa(int(x.Scale)) + ", " + strconv.Itoa(places) + ", " + method
			return SQLExpr{Val: "fado_dec_round(" + list + ")", Err: "fado_dec_round_err(" + list + ")", Kind: KindDecimal, Scale: int8(places), Sure: x.Sure}, nil
		}
		list := rawNum(x) + ", " + strconv.Itoa(places) + ", " + method
		return SQLExpr{Val: "fado_dec_from(" + list + ")", Err: "fado_dec_from_err(" + list + ")", Kind: KindDecimal, Scale: int8(places)}, nil
	case "NUMBER":
		x := args[0]
		if x.Kind.IsExact() {
			return SQLExpr{Val: "(CAST(" + x.Val + " AS REAL) / " + d.NumberLit(pow10(int(x.Scale))) + ")", Kind: KindNumber, Sure: x.Sure}, nil
		}
		return SQLExpr{Val: rawNum(x), Kind: KindNumber, Sure: x.Sure}, nil
	case "MONEY":
		amount, cur := args[0], d.Text(args[1])
		m := rawMant(amount)
		return SQLExpr{Val: m, Err: "CASE WHEN " + m + " IS NOT NULL THEN fado_currency_err(" + cur + ") END",
			Cur: "(CASE WHEN " + m + " IS NULL THEN NULL ELSE fado_currency(" + cur + ") END)", Kind: KindMoney, Scale: amount.Scale}, nil
	case "MONEYVALUE":
		t, places := d.Text(args[0]), strconv.Itoa(placesConst(args, 1))
		return SQLExpr{Val: "fado_money_amount(" + t + ", " + places + ")", Err: "fado_money_amount_err(" + t + ", " + places + ")",
			Cur: "fado_money_cur(" + t + ")", Kind: KindMoney, Scale: int8(placesConst(args, 1))}, nil
	case "DECIMALVALUE":
		t, places := d.Text(args[0]), strconv.Itoa(placesConst(args, 1))
		return SQLExpr{Val: "fado_dec_parse(" + t + ", " + places + ")", Err: "fado_dec_parse_err(" + t + ", " + places + ")", Kind: KindDecimal, Scale: int8(placesConst(args, 1))}, nil
	case "AMOUNT":
		return SQLExpr{Val: rawMant(args[0]), Kind: KindDecimal, Scale: args[0].Scale, Sure: args[0].Sure && args[0].Kind != KindNull}, nil
	case "CURRENCY":
		return SQLExpr{Val: curOrNull(args[0]), Kind: KindText}, nil
	case "DATE":
		return reg(KindDate, true, d.Num(args[0]), d.Num(args[1]), d.Num(args[2])), nil
	case "DATEVALUE":
		return reg(KindDate, true, d.Text(args[0])), nil
	case "DATETIMEVALUE":
		return reg(KindDateTime, true, d.Text(args[0])), nil
	case "DATEOF":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindDate}, nil
		}
		return SQLExpr{Val: "floor(" + args[0].Val + " / 86400.0)", Kind: KindDate, Sure: args[0].Sure}, nil
	case "DATETIME":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindDateTime}, nil
		}
		return SQLExpr{Val: "(" + args[0].Val + " * 86400.0)", Kind: KindDateTime, Sure: args[0].Sure}, nil
	case "YEAR", "MONTH", "DAY":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		part := map[string]string{"YEAR": "%Y", "MONTH": "%m", "DAY": "%d"}[name]
		return SQLExpr{Val: "CAST(strftime('" + part + "', " + secsOf(args[0]) + ", 'unixepoch') AS REAL)", Kind: KindNumber}, nil
	case "QUARTER":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "(floor((CAST(strftime('%m', " + secsOf(args[0]) + ", 'unixepoch') AS REAL) - 1) / 3) + 1)", Kind: KindNumber}, nil
	case "DAYS", "DAYSBETWEEN":
		a, b := args[0], args[1]
		if name == "DAYSBETWEEN" {
			a, b = b, a
		}
		if a.Kind == KindNull || b.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		if a.Kind == KindDate && b.Kind == KindDate {
			return SQLExpr{Val: "(" + a.Val + " - " + b.Val + ")", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "(floor(" + a.Val + " / 86400.0) - floor(" + b.Val + " / 86400.0))", Kind: KindNumber}, nil
	case "MONTHSBETWEEN", "YEARSBETWEEN":
		if args[0].Kind == KindNull || args[1].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		s, e := secsOf(args[0]), secsOf(args[1])
		if name == "YEARSBETWEEN" {
			return SQLExpr{Val: "(" + year(e) + " - " + year(s) + ")", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "((" + year(e) + " * 12 + " + month(e) + ") - (" + year(s) + " * 12 + " + month(s) + "))", Kind: KindNumber}, nil
	case "ADDDAYS":
		x := args[0]
		if x.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNull}, nil
		}
		if x.Kind == KindDate {
			val := "(" + x.Val + " + trunc(" + d.Num(args[1]) + "))"
			return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateDays) + " OR " + val + " > " + strconv.Itoa(maxDateDays) + " THEN 'out_of_range' END", Kind: KindDate}, nil
		}
		val := "floor(" + x.Val + " + " + d.Num(args[1]) + " * 86400.0)"
		return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateSecs) + " OR " + val + " > " + strconv.Itoa(maxDateSecs) + " THEN 'out_of_range' END", Kind: KindDateTime}, nil
	case "ADDMONTHS", "ADDYEARS":
		x := args[0]
		if x.Kind == KindDate {
			e := reg(KindDate, true, secsOf(x), d.Num(args[1]))
			e.Val = "(" + e.Val + " / 86400.0)"
			return e, nil
		}
		if x.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNull}, nil
		}
		return reg(KindDateTime, true, raw(x), d.Num(args[1])), nil
	case "NOW":
		return SQLExpr{Val: "CAST(" + params.Use("now") + " AS REAL)", Kind: KindDateTime, Sure: true}, nil
	case "TODAY":
		return SQLExpr{Val: "floor(CAST(" + params.Use("now") + " AS REAL) / 86400.0)", Kind: KindDate, Sure: true}, nil
	case "USER":
		return SQLExpr{Val: params.Use("user"), Kind: KindText}, nil
	case "STATE":
		return SQLExpr{Val: params.Use("state"), Kind: KindText}, nil
	case "ID":
		return SQLExpr{Val: params.Use("id"), Kind: KindText}, nil
	case "INGROUP":
		return SQLExpr{Val: "EXISTS (SELECT 1 FROM json_each(" + params.Use("groups") + ") WHERE json_each.value = " + d.Text(args[0]) + ")", Kind: KindBool, Sure: true}, nil
	case "LEFT":
		n := intArg(d.Num(args[1]))
		return SQLExpr{Val: "substr(" + d.Text(args[0]) + ", 1, " + n + ")", Err: countErr(n), Kind: KindText, Sure: true}, nil
	case "RIGHT":
		t, n := d.Text(args[0]), intArg(d.Num(args[1]))
		val := "(CASE WHEN " + n + " <= 0 THEN '' WHEN " + n + " >= length(" + t + ") THEN " + t + " ELSE substr(" + t + ", -" + n + ") END)"
		return SQLExpr{Val: val, Err: countErr(n), Kind: KindText, Sure: true}, nil
	case "MID":
		t, p := d.Text(args[0]), intArg(d.Num(args[1]))
		if len(args) == 2 {
			return SQLExpr{Val: "substr(" + t + ", " + p + ")", Err: positionErr(p, ""), Kind: KindText, Sure: true}, nil
		}
		n := intArg(d.Num(args[2]))
		return SQLExpr{Val: "substr(" + t + ", " + p + ", " + n + ")", Err: positionErr(p, n), Kind: KindText, Sure: true}, nil
	case "LEN":
		return SQLExpr{Val: "CAST(length(" + d.Text(args[0]) + ") AS REAL)", Kind: KindNumber, Sure: true}, nil
	case "TRIM", "UPPER", "LOWER", "PROPER":
		e := reg(KindText, false, d.Text(args[0]))
		e.Sure = true
		return e, nil
	case "REPLACE":
		return reg(KindText, true, d.Text(args[0]), d.Num(args[1]), d.Num(args[2]), d.Text(args[3])), nil
	case "SUBSTITUTE":
		return reg(KindText, true, d.Text(args[0]), d.Text(args[1]), d.Text(args[2])), nil
	case "CONCAT":
		return concatExpr(d, args, false), nil
	case "REPEAT":
		return reg(KindText, true, d.Text(args[0]), d.Num(args[1])), nil
	case "FINDFIRST":
		return SQLExpr{Val: "CAST(instr(" + d.Text(args[1]) + ", " + d.Text(args[0]) + ") AS REAL)", Kind: KindNumber, Sure: true}, nil
	case "FINDLAST":
		e := reg(KindNumber, false, d.Text(args[0]), d.Text(args[1]))
		e.Sure = true
		return e, nil
	case "PAD":
		fill := "NULL"
		if len(args) > 2 {
			fill = d.Text(args[2])
		}
		return reg(KindText, true, d.Text(args[0]), d.Num(args[1]), fill), nil
	case "REGEXREPLACE":
		return reg(KindText, true, d.Text(args[0]), d.Text(args[1]), d.Text(args[2])), nil
	case "TEXT":
		return SQLExpr{Val: d.Render(args[0]), Kind: KindText, Sure: true}, nil
	case "VALUE":
		return reg(KindNumber, true, d.Text(args[0])), nil
	case "JSONVALUE":
		return reg(KindText, true, raw(args[0]), d.Text(args[1])), nil
	}
	return SQLExpr{}, fmt.Errorf("no SQLite translation")
}

// exactAggregate spells MIN, MAX, SUM and AVERAGE over exact values: the
// mantissas aligned to the largest scale, blanks skipped, and the
// currencies checked.
func (d SQLite) exactAggregate(name string, args []SQLExpr) SQLExpr {
	mants, scale, kind, cur, errs := exactArgs(d, args)
	e := SQLExpr{Kind: kind, Scale: int8(scale), Cur: cur}
	switch name {
	case "MIN", "MAX":
		raws := make([]string, len(args))
		for i, a := range args {
			if a.Kind == KindNull {
				raws[i] = "NULL"
			} else {
				var re string
				raws[i], re = rescaled(d, d.RawMant(a), int(a.Scale), scale)
				if re != "" {
					errs = append(errs, re)
				}
			}
		}
		e.Val = "fado_dec_" + strings.ToLower(name) + "(" + strings.Join(raws, ", ") + ")"
		errs = append(errs, "fado_dec_"+strings.ToLower(name)+"_err("+strings.Join(raws, ", ")+")")
	case "SUM":
		val := "(" + strings.Join(mants, " + ") + ")"
		e.Val, e.Sure = val, true
		errs = append(errs, d.OverflowErr(val))
	default:
		counts := make([]string, len(args))
		for i, a := range args {
			if a.Kind == KindNull {
				counts[i] = "0"
			} else {
				counts[i] = "(CASE WHEN " + a.Val + " IS NULL THEN 0 ELSE 1 END)"
			}
		}
		n := "(" + strings.Join(counts, " + ") + ")"
		sum := "(" + strings.Join(mants, " + ") + ")"
		list := sum + ", " + n + ", " + strconv.Itoa(divScale(scale)-scale)
		e.Val = "(CASE WHEN " + n + " = 0 THEN NULL ELSE fado_dec_div(" + list + ") END)"
		e.Scale = int8(divScale(scale))
		errs = append(errs, d.OverflowErr(sum), "CASE WHEN "+n+" > 0 THEN fado_dec_div_err("+list+") END")
	}
	e.Err = coalesceErr(errs...)
	return e
}
