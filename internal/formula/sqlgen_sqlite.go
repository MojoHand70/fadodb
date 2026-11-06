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
// never happens; dates are REAL seconds; booleans are INTEGER 0 and 1;
// blank is NULL. Where SQLite's own function differs from the rule, the
// dialect calls the engine's Go implementation registered as fado_<name>
// (ADR-0019), with fado_<name>_err returning the error code: SQLite rounds
// 0.49999999999999994 up, ignores negative digit counts, upper-cases ASCII
// only, keeps inner spaces in trim, takes the dividend's sign in mod, does
// not clamp month arithmetic, normalises February 30 and never fails a
// cast; its pow, ln and log are the platform's, which differ from the VM
// in the last bit between Linux, macOS and Windows; and its printf rounds
// the fifteenth significant digit its own way. The sqlitefn package does
// the registration.

// SQLite is the dialect for SQLite through modernc.org/sqlite.
type SQLite struct {
	coercions
	lim Limits
}

// NewSQLite makes the SQLite dialect under the default limits.
func NewSQLite() SQLite {
	return SQLite{coercions: coercions{zeroNumber: "0.0", zeroBool: "0", zeroText: "''",
		castNumber: func(x string) string { return "CAST(" + x + " AS REAL)" }}, lim: DefaultLimits()}
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

// Pow implements Dialect: the engine's own power function, not SQLite's.
func (SQLite) Pow(x, y string) string { return "fado_power(" + x + ", " + y + ")" }

// Param implements Dialect.
func (SQLite) Param(name string) string { return ":" + name }

// ContextArgs implements Dialect: now is always bound, user is text or
// NULL, groups is a JSON array or NULL.
func (SQLite) ContextArgs(ctx *Context) []any {
	now, user, groups := contextValues(ctx)
	var g any
	if groups != nil {
		b, _ := json.Marshal(groups)
		g = string(b)
	}
	return []any{sql.Named("now", now), sql.Named("user", user), sql.Named("groups", g)}
}

// Setup implements Dialect: the engine functions come from the sqlitefn
// package's registration, not from statements.
func (SQLite) Setup() []string { return nil }

// contextValues is what every dialect binds: the clock in seconds, always
// present; the user id or nil; the groups or nil.
func contextValues(ctx *Context) (now int64, user any, groups []string) {
	now = time.Now().Unix()
	if ctx != nil {
		if ctx.Now != 0 {
			now = ctx.Now
		}
		if ctx.User != "" {
			user = ctx.User
		}
		groups = ctx.Groups
	}
	return now, user, groups
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

// TextLit implements Dialect.
func (SQLite) TextLit(s string) string { return textLiteral(s) }

func textLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Load implements Dialect.
func (SQLite) Load(column string, k Kind) string {
	switch k {
	case KindNumber, KindDate:
		return "CAST(" + column + " AS REAL)"
	}
	return column
}

// Render implements Dialect: the TEXT rule of ADR-0019.
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
	case KindDate:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' WHEN " + e.Val + " % 86400 = 0 THEN strftime('%Y-%m-%d', " + e.Val +
			", 'unixepoch') ELSE strftime('%Y-%m-%d %H:%M:%S', " + e.Val + ", 'unixepoch') END)"
	}
	return d.Text(e)
}

// SQLiteFunctions lists the engine functions the SQLite dialect expects to
// be registered, as fado_<name> and fado_<name>_err. The sqlitefn package
// registers them from this list.
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
	return out
}

// sqliteRegistered names the builtins the SQLite dialect calls through
// registered Go functions.
var sqliteRegistered = []string{
	"ROUND", "FLOOR", "CEILING", "TRUNC", "MIN", "MAX", "SUM", "AVERAGE", "COUNT", "POWER", "LN", "LOG",
	"DATE", "DATEVALUE", "ADDMONTHS", "ADDYEARS",
	"TRIM", "UPPER", "LOWER", "PROPER", "REPLACE", "SUBSTITUTE", "REPEAT", "FINDLAST", "PAD", "REGEXREPLACE",
	"TEXT", "VALUE", "JSONVALUE",
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
	// Kinds gives the kind expected at each position; the last repeats.
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
	case akNumber:
		return KindNumber
	case akText:
		return KindText
	case akDate:
		return KindDate
	case akCondition:
		return KindBool
	case akJSON:
		return KindJSON
	}
	return KindNull
}

// ValueFromDriver converts a value as a database driver delivers it (nil,
// int64, float64, string or []byte) to a Value of the expected kind.
// KindNull means "whatever it is": numbers become numbers and text text.
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
	case KindNumber, KindDate, KindBool, KindNull:
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

// DriverValue converts a Value to what a database driver takes: nil,
// float64, int64 or string. Dates are REAL seconds, booleans 0 and 1.
func (v Value) DriverValue() any {
	switch v.kind {
	case KindNull:
		return nil
	case KindBool:
		return int64(v.num)
	case KindNumber, KindDate:
		return v.num
	}
	return v.str
}

// intArg truncates a number to an integer for a position or a count,
// clamped to -1 below and to one past maxCount above, so that a negative
// argument is still seen and the cast cannot overflow.
func intArg(x string) string {
	return "CAST(min(max(trunc(" + x + "), -1), " + maxCount + " + 1) AS INTEGER)"
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
	switch name {
	case "ROUND", "FLOOR", "CEILING", "TRUNC":
		e := reg(KindNumber, false, d.Num(args[0]), numOr(1, "0.0"))
		e.Sure = true
		return e, nil
	case "ABS":
		return SQLExpr{Val: "abs(" + d.Num(args[0]) + ")", Kind: KindNumber, Sure: true}, nil
	case "SIGN":
		x := d.Num(args[0])
		return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN 1.0 WHEN " + x + " < 0 THEN -1.0 ELSE 0.0 END)", Kind: KindNumber, Sure: true}, nil
	case "MIN", "MAX", "SUM", "AVERAGE", "COUNT":
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
		a, b := d.Num(args[0]), d.Num(args[1])
		val := "(" + a + " - " + b + " * floor(" + a + " / " + b + "))"
		return SQLExpr{Val: val, Err: coalesceErr("CASE WHEN "+b+" = 0 THEN 'div_by_zero' END", notFiniteErr(d, val)), Kind: KindNumber}, nil
	case "DATE":
		return reg(KindDate, true, d.Num(args[0]), d.Num(args[1]), d.Num(args[2])), nil
	case "DATEVALUE":
		return reg(KindDate, true, d.Text(args[0])), nil
	case "YEAR", "MONTH", "DAY":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		part := map[string]string{"YEAR": "%Y", "MONTH": "%m", "DAY": "%d"}[name]
		return SQLExpr{Val: "CAST(strftime('" + part + "', " + args[0].Val + ", 'unixepoch') AS REAL)", Kind: KindNumber}, nil
	case "QUARTER":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "(floor((CAST(strftime('%m', " + args[0].Val + ", 'unixepoch') AS REAL) - 1) / 3) + 1)", Kind: KindNumber}, nil
	case "DAYS", "DAYSBETWEEN":
		a, b := args[0], args[1]
		if name == "DAYSBETWEEN" {
			a, b = b, a
		}
		if a.Kind == KindNull || b.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "(floor(" + a.Val + " / 86400.0) - floor(" + b.Val + " / 86400.0))", Kind: KindNumber}, nil
	case "MONTHSBETWEEN", "YEARSBETWEEN":
		if args[0].Kind == KindNull || args[1].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		year := func(v string) string { return "CAST(strftime('%Y', " + v + ", 'unixepoch') AS REAL)" }
		month := func(v string) string { return "CAST(strftime('%m', " + v + ", 'unixepoch') AS REAL)" }
		s, e := args[0].Val, args[1].Val
		if name == "YEARSBETWEEN" {
			return SQLExpr{Val: "(" + year(e) + " - " + year(s) + ")", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "((" + year(e) + " * 12 + " + month(e) + ") - (" + year(s) + " * 12 + " + month(s) + "))", Kind: KindNumber}, nil
	case "ADDDAYS":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindDate}, nil
		}
		val := "floor(" + args[0].Val + " + " + d.Num(args[1]) + " * 86400.0)"
		return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateSecs) + " OR " + val + " > " + strconv.Itoa(maxDateSecs) + " THEN 'out_of_range' END", Kind: KindDate}, nil
	case "ADDMONTHS", "ADDYEARS":
		return reg(KindDate, true, raw(args[0]), d.Num(args[1])), nil
	case "NOW":
		return SQLExpr{Val: "CAST(" + params.Use("now") + " AS REAL)", Kind: KindDate, Sure: true}, nil
	case "TODAY":
		return SQLExpr{Val: "(floor(CAST(" + params.Use("now") + " AS REAL) / 86400.0) * 86400.0)", Kind: KindDate, Sure: true}, nil
	case "USER":
		return SQLExpr{Val: params.Use("user"), Kind: KindText}, nil
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
