package formula

import (
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"sync"
)

// The Postgres dialect (ADR-0019, ADR-0022, ADR-0035), for Postgres 17
// and later. A number is float8, and so are the days of a date and the
// seconds of a datetime; a bool is boolean; blank is NULL. An exact
// value is its bigint mantissa; exact arithmetic runs in numeric, which
// no operation on 64-bit operands overflows, and fado_narrow brings the
// result back, NULL where it does not fit.
//
// Postgres fails a statement where SQLite and DuckDB give an infinity, a
// NULL or a NaN: a float that leaves its range, a division by zero, a
// logarithm of nothing, a text that is no number. The VM gives an error
// code there, and a statement that fails gives none. So every such
// operation is written through a function that cannot fail: the value
// is an infinity, a NaN or NULL, and the error column carries the code,
// as in the other dialects. The functions are named as theirs are,
// fado_<name> and fado_<name>_err, and Setup returns the statements
// that create them in the first schema of the search path. They are SQL
// functions of one expression wherever that can be, which the planner
// writes into the statement that calls them.
//
// Text is ordered by code point, as the VM orders it, whatever the
// database was created with: a comparison and a sort say COLLATE "C",
// and a table's text columns are declared so. The case of a letter is
// changed by pg_c_utf8, the rule of Unicode that Go follows.

// Postgres is the dialect for Postgres through database/sql.
type Postgres struct {
	lim Limits
}

// NewPostgres makes the Postgres dialect under the default limits.
func NewPostgres() Postgres { return Postgres{lim: DefaultLimits()} }

// Limits implements Dialect.
func (d Postgres) Limits() Limits { return d.lim }

// WithLimits implements Dialect.
func (d Postgres) WithLimits(l Limits) Dialect {
	d.lim = l
	return d
}

// Name implements Dialect.
func (Postgres) Name() string { return "postgres" }

// pgFloat spells a float as a constant of the type: its shortest
// spelling that reads back as itself, which Postgres reads exactly.
func pgFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "'Infinity'::float8"
	case math.IsInf(f, -1):
		return "'-Infinity'::float8"
	case math.IsNaN(f):
		return "'NaN'::float8"
	}
	return "'" + strconv.FormatFloat(f, 'g', -1, 64) + "'::float8"
}

// NumberLit implements Dialect. Every literal is a float8.
func (Postgres) NumberLit(f float64) string { return pgFloat(f) }

// IntLit implements Dialect. The quoted spelling reads the least int64
// too, which a minus before its digits would not.
func (Postgres) IntLit(m int64) string { return "'" + strconv.FormatInt(m, 10) + "'::bigint" }

// TextLit implements Dialect.
func (Postgres) TextLit(s string) string { return textLiteral(s) + "::text" }

// True implements Dialect.
func (Postgres) True() string { return "TRUE" }

// False implements Dialect.
func (Postgres) False() string { return "FALSE" }

// Load implements Dialect. Every reference is cast, since it may be a
// parameter, which has no type until it is given one.
func (Postgres) Load(column string, k Kind) string {
	switch k {
	case KindNumber, KindDate, KindDateTime:
		return "CAST(" + column + " AS float8)"
	case KindDecimal, KindMoney:
		return "CAST(" + column + " AS bigint)"
	case KindBool:
		return "CAST(" + column + " AS boolean)"
	}
	return "CAST(" + column + " AS text)"
}

// Num implements Dialect.
func (d Postgres) Num(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return pgZero
	case e.Kind == KindBool:
		return "CAST(CAST(" + d.Cond(e) + " AS integer) AS float8)"
	case e.Sure:
		return e.Val
	}
	return "COALESCE(" + e.Val + ", " + pgZero + ")"
}

const pgZero = "'0'::float8"

// Cond implements Dialect.
func (Postgres) Cond(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return "FALSE"
	case e.Sure:
		return e.Val
	}
	return "COALESCE(" + e.Val + ", FALSE)"
}

// Text implements Dialect.
func (Postgres) Text(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return "''::text"
	case e.Sure:
		return e.Val
	}
	return "COALESCE(" + e.Val + ", ''::text)"
}

// Mant implements Dialect: the mantissa as a numeric, a blank 0.
func (d Postgres) Mant(e SQLExpr) string {
	switch {
	case e.Kind == KindNull:
		return "0::numeric"
	case e.Kind == KindBool:
		return "CAST(CAST(" + d.Cond(e) + " AS integer) AS numeric)"
	case e.Sure:
		return "CAST(" + e.Val + " AS numeric)"
	}
	return "CAST(COALESCE(" + e.Val + ", 0) AS numeric)"
}

// RawMant implements Dialect: the mantissa as a numeric, a blank NULL.
func (d Postgres) RawMant(e SQLExpr) string {
	switch e.Kind {
	case KindNull:
		return "NULL::numeric"
	case KindBool:
		return "CAST(CAST(" + d.Cond(e) + " AS integer) AS numeric)"
	}
	return "CAST(" + e.Val + " AS numeric)"
}

// NotFinite implements Dialect. Postgres holds a NaN greater than every
// number, the infinities too, so one comparison finds all three.
func (Postgres) NotFinite(val string) string {
	return "abs(" + val + ") > " + pgFloat(math.MaxFloat64)
}

// Narrow implements Dialect.
func (Postgres) Narrow(val string) string { return "fado_narrow(" + val + ")" }

// OverflowErr implements Dialect: a NULL is a blank, not an overflow.
func (Postgres) OverflowErr(val string) string {
	return "CASE WHEN (" + val + ") IS NOT NULL AND fado_narrow(" + val + ") IS NULL THEN 'overflow' END"
}

// ExactRowSum implements Dialect: the mantissas are numeric, so their
// sum is exact, and it fails when it does not narrow.
func (d Postgres) ExactRowSum(mant string) (string, string) {
	total := "SUM(" + mant + ")"
	return d.Narrow(total), d.OverflowErr(total)
}

// NumberRowSum implements Dialect. Postgres's sum adds in the order it
// meets the rows and fails where a partial sum leaves the range, so the
// rows go in key order to fado_sum_rows, which folds them from zero
// with the addition that cannot fail.
func (d Postgres) NumberRowSum(num, order string) (string, string) {
	val := "fado_sum_rows(" + num + order + ")"
	return val, notFiniteErr(d, val)
}

// JoinRows implements Dialect: Postgres's own string_agg, which takes
// its rows in an order and any expression for the separator.
func (Postgres) JoinRows(text, sep, order string) string {
	return "string_agg(" + text + ", " + sep + order + ")"
}

// Pow implements Dialect: the C library's pow, behind the checks that
// keep it from failing.
func (Postgres) Pow(x, y string) string { return "fado_power(" + x + ", " + y + ")" }

// Param implements Dialect. A parameter is written by its name; the
// driver of the postgres module turns the names into $1, $2 and so on.
func (Postgres) Param(name string) string { return "@" + name }

// ColumnType implements Dialect: float8 for a number and for the days
// and seconds of dates, bigint for a mantissa, boolean, text.
func (Postgres) ColumnType(k Kind) string {
	switch k {
	case KindNumber, KindDate, KindDateTime:
		return "float8"
	case KindDecimal, KindMoney:
		return "bigint"
	case KindBool:
		return "boolean"
	}
	return "text"
}

// SourceColumn is what a table declares for a column of the kind: its
// type, and for a text that it is ordered by code point, so that a
// sort and an index of the column are what the VM's order is, whatever
// the database was created with.
func (d Postgres) SourceColumn(k Kind) string {
	if t := d.ColumnType(k); t != "text" {
		return t
	}
	return `text COLLATE "C"`
}

// TypedBlank spells the blank of a kind: a NULL of the type the kind is
// computed in.
func (d Postgres) TypedBlank(k Kind) string { return "NULL::" + d.ColumnType(k) }

// OneRow spells the value of the one row a statement reads: Postgres
// has no MIN of a boolean, and of one row bool_and is the row's own.
func (Postgres) OneRow(v string, k Kind) string {
	if k == KindBool {
		return "bool_and(" + v + ")"
	}
	return "MIN(" + v + ")"
}

// OrderText spells a text so that it is ordered by code point.
func (Postgres) OrderText(x string) string { return "(" + x + ` COLLATE "C")` }

// TypedParam spells a parameter bound to what Value.DriverValue gives
// for the kind: a bool arrives as 0 or 1.
func (d Postgres) TypedParam(ref string, k Kind) string {
	if k == KindBool {
		return "(CAST(" + ref + " AS bigint) <> 0)"
	}
	return "CAST(" + ref + " AS " + d.ColumnType(k) + ")"
}

// FloatOp spells x op y so that no operands fail the statement.
func (Postgres) FloatOp(op Op, x, y string) string {
	switch op {
	case OpSub:
		return "fado_add(" + x + ", -(" + y + "))"
	case OpMul:
		return "fado_mul(" + x + ", " + y + ")"
	case OpDiv, OpDivStrict:
		return "fado_div(" + x + ", " + y + ")"
	}
	return "fado_add(" + x + ", " + y + ")"
}

// ContextArgs implements Dialect: groups bind as an array of text.
func (Postgres) ContextArgs(ctx *Context) []any {
	now, user, state, id, groups := contextValues(ctx)
	var g any
	if groups != nil {
		g = groups
	}
	return lifeArgs(ctx, []any{sql.Named("now", now), sql.Named("user", user), sql.Named("groups", g), sql.Named("state", state), sql.Named("id", id),
		sql.Named("userkind", textOrNil(ctx, func(c *Context) string { return c.Kind })), sql.Named("actingfor", textOrNil(ctx, func(c *Context) string { return c.For }))})
}

// stateCall translates a function of a state (ADR-0047, item 6) for a
// statement that runs over no document row, as the other dialects do:
// the life's states are bound as a JSON array, from which fado_stay
// gives the object of the named state.
func (d Postgres) stateCall(name string, state SQLExpr, params *Params) SQLExpr {
	stays := "CAST(" + params.Use("stays") + " AS jsonb)"
	stay := "fado_stay(" + stays + ", " + d.Text(state) + ")"
	whole := func(key string) string { return "CAST(" + stay + " ->> '" + key + "' AS bigint)" }
	switch name {
	case "ENTRIES":
		return SQLExpr{Val: "(CASE WHEN " + stays + " IS NOT NULL THEN CAST(COALESCE(" + whole("n") + ", 0) AS float8) END)", Kind: KindNumber}
	case "DAYSIN":
		secs := whole("secs") + " + CASE WHEN CAST(" + stay + " ->> 'here' AS boolean) THEN CAST(" + params.Use("now") + " AS bigint) - COALESCE(" + whole("le") + ", 0) ELSE 0 END"
		return SQLExpr{Val: "(CASE WHEN " + stays + " IS NOT NULL THEN COALESCE(CAST(" + secs + " AS float8), " + pgZero + ") / " + pgFloat(secondsPerDay) + " END)", Kind: KindNumber}
	}
	kind := builtins[builtinByName[name]].result
	return SQLExpr{Val: d.Load("("+stay+" ->> '"+stateFacts[name]+"')", kind), Kind: kind}
}

// Setup implements Dialect: the statements that create the functions,
// in the order that creates each after the ones it calls. They are
// written out the first time they are asked for, and by nothing else,
// so that a program that never opens a Postgres database neither
// builds them nor carries them.
func (Postgres) Setup() []string {
	postgresOnce.Do(func() { postgresFunctions = makePostgresFunctions() })
	return postgresFunctions
}

var (
	postgresOnce      sync.Once
	postgresFunctions []string
)

// Render implements Dialect: the TEXT rule of ADR-0019 and ADR-0022.
func (d Postgres) Render(e SQLExpr) string {
	switch e.Kind {
	case KindNull:
		return "''::text"
	case KindNumber:
		if e.Sure {
			return "fado_num_text(" + e.Val + ")"
		}
		return "COALESCE(fado_num_text(" + e.Val + "), ''::text)"
	case KindBool:
		return "(CASE WHEN " + e.Val + " IS NULL THEN ''::text WHEN " + e.Val + " THEN 'TRUE'::text ELSE 'FALSE'::text END)"
	case KindDecimal:
		return "COALESCE(fado_dec_text(" + e.Val + ", " + strconv.Itoa(int(e.Scale)) + "), ''::text)"
	case KindMoney:
		return "(CASE WHEN " + curOrNull(e) + " IS NULL THEN ''::text ELSE fado_dec_text(" + e.Val + ", " + strconv.Itoa(int(e.Scale)) + ") || ' ' || " + e.Cur + " END)"
	case KindDate:
		return "COALESCE(fado_date_text(" + e.Val + "), ''::text)"
	case KindDateTime:
		return "COALESCE(fado_datetime_text(" + e.Val + "), ''::text)"
	}
	return d.Text(e)
}

// Call implements Dialect.
func (d Postgres) Call(name string, args []SQLExpr, params *Params) (SQLExpr, error) {
	fn := func(kind Kind, withErr bool, sqlArgs ...string) SQLExpr {
		f := "fado_" + strings.ToLower(name)
		if limitedFunctions[name] {
			sqlArgs = append(sqlArgs, strconv.Itoa(d.lim.MaxText))
		}
		list := strings.Join(sqlArgs, ", ")
		e := SQLExpr{Val: f + "(" + list + ")", Kind: kind}
		if withErr {
			e.Err = f + "_err(" + list + ")"
		}
		return e
	}
	add := func(x, y string) string { return "fado_add(" + x + ", " + y + ")" }
	sub := func(x, y string) string { return "fado_add(" + x + ", -(" + y + "))" }
	mul := func(x, y string) string { return "fado_mul(" + x + ", " + y + ")" }
	div := func(x, y string) string { return "fado_div(" + x + ", " + y + ")" }
	day := pgFloat(secondsPerDay)
	numOr := func(i int, def string) string {
		if i < len(args) {
			return d.Num(args[i])
		}
		return def
	}
	rawNum := func(e SQLExpr) string {
		switch e.Kind {
		case KindNull:
			return "NULL::float8"
		case KindBool:
			return "CAST(CAST(" + e.Val + " AS integer) AS float8)"
		}
		return e.Val
	}
	rawMant := d.RawMant
	count := func(e SQLExpr) string {
		if e.Kind == KindNull {
			return "0"
		}
		return "CAST((" + e.Val + ") IS NOT NULL AS integer)"
	}
	exact := false
	for _, a := range args {
		exact = exact || a.Kind.IsExact()
	}
	// intArg truncates a position or count argument to an integer, clamped
	// to -1 below and to one past maxCount above, so that a negative
	// argument is still seen and the cast cannot fail. A NaN is the
	// greatest of numbers to Postgres, and so becomes the upper bound.
	intArg := func(x string) string {
		return "CAST(LEAST(GREATEST(trunc(" + x + "), -1), " + maxCount + " + 1) AS integer)"
	}
	secs := func(e SQLExpr) string {
		if e.Kind == KindDate {
			return mul(e.Val, day)
		}
		return e.Val
	}
	part := func(field, v string) string { return "CAST(extract(" + field + " FROM fado_ts(" + v + ")) AS float8)" }
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
		// A number rounded at a far place may leave the range: the VM
		// reports it, and so does the error column here.
		if name == "ROUND" {
			e := SQLExpr{Val: "fado_round_m(" + d.Num(args[0]) + ", " + numOr(1, pgZero) + ", " + methodConst(d, args, 2) + ")", Kind: KindNumber, Sure: true}
			e.Err = notFiniteErr(d, e.Val)
			return e, nil
		}
		e := fn(KindNumber, false, d.Num(args[0]), numOr(1, pgZero))
		e.Err = notFiniteErr(d, e.Val)
		e.Sure = true
		return e, nil
	case "ABS":
		if exact {
			m := d.Mant(args[0])
			return SQLExpr{Val: "fado_narrow(abs(" + m + "))", Err: "CASE WHEN fado_narrow(abs(" + m + ")) IS NULL THEN 'overflow' END",
				Kind: args[0].Kind, Scale: args[0].Scale, Cur: args[0].Cur, Sure: true}, nil
		}
		return SQLExpr{Val: "abs(" + d.Num(args[0]) + ")", Kind: KindNumber, Sure: true}, nil
	case "SIGN":
		if exact {
			x := d.Mant(args[0])
			return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN CAST(1 AS bigint) WHEN " + x + " < 0 THEN CAST(-1 AS bigint) ELSE CAST(0 AS bigint) END)", Kind: KindDecimal, Sure: true}, nil
		}
		x := d.Num(args[0])
		return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN " + d.NumberLit(1) + " WHEN " + x + " < 0 THEN " + d.NumberLit(-1) + " ELSE " + pgZero + " END)", Kind: KindNumber, Sure: true}, nil
	case "MIN", "MAX":
		if exact {
			return d.exactAggregate(name, args[:len(args)-1]), nil
		}
		list := make([]string, len(args))
		for i, a := range args {
			list[i] = rawNum(a)
		}
		f := "least"
		if name == "MAX" {
			f = "greatest"
		}
		if len(list) == 1 {
			return SQLExpr{Val: list[0], Kind: KindNumber}, nil
		}
		return SQLExpr{Val: f + "(" + strings.Join(list, ", ") + ")", Kind: KindNumber}, nil
	case "SUM", "AVERAGE", "COUNT":
		if exact && name != "COUNT" {
			return d.exactAggregate(name, args[:len(args)-1]), nil
		}
		counts := make([]string, len(args))
		sum := ""
		for i, a := range args {
			counts[i] = count(a)
			if i == 0 {
				sum = d.Num(a)
			} else {
				sum = add(sum, d.Num(a))
			}
		}
		n := "(" + strings.Join(counts, " + ") + ")"
		switch name {
		case "COUNT":
			return SQLExpr{Val: "CAST(" + n + " AS float8)", Kind: KindNumber, Sure: true}, nil
		case "SUM":
			val := "(" + sum + ")"
			return SQLExpr{Val: val, Err: notFiniteErr(d, val), Kind: KindNumber, Sure: true}, nil
		}
		val := "(CASE WHEN " + n + " = 0 THEN NULL ELSE " + div(sum, "CAST("+n+" AS float8)") + " END)"
		return SQLExpr{Val: val, Err: notFiniteErr(d, "("+sum+")"), Kind: KindNumber}, nil
	case "POWER":
		return arith(d, OpPow, args[0], args[1]), nil
	case "SQRT":
		x := d.Num(args[0])
		return SQLExpr{Val: "(CASE WHEN " + x + " >= 0 THEN sqrt(" + x + ") END)", Err: "CASE WHEN " + x + " < 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "EXP":
		val := "fado_exp(" + d.Num(args[0]) + ")"
		return SQLExpr{Val: val, Err: notFiniteErr(d, val), Kind: KindNumber, Sure: true}, nil
	case "LN":
		x := d.Num(args[0])
		return SQLExpr{Val: "fado_ln(" + x + ")", Err: "CASE WHEN " + x + " <= 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "LOG":
		x := d.Num(args[0])
		if len(args) == 1 {
			return SQLExpr{Val: "fado_log10(" + x + ")", Err: "CASE WHEN " + x + " <= 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
		}
		b := d.Num(args[1])
		val := "(CASE WHEN " + b + " = " + d.NumberLit(10) + " THEN fado_log10(" + x + ") ELSE " + div("fado_ln("+x+")", "fado_ln("+b+")") + " END)"
		return SQLExpr{Val: val, Err: "CASE WHEN " + x + " <= 0 OR " + b + " <= 0 OR " + b + " = 1 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "MOD":
		if exact {
			mants, scale, kind, cur, errs := exactArgs(d, args[:len(args)-1])
			list := mants[0] + ", " + mants[1]
			return SQLExpr{Val: "fado_dec_mod(" + list + ")", Err: coalesceErr(append(errs, "fado_dec_mod_err("+list+")")...), Kind: kind, Scale: int8(scale), Cur: cur}, nil
		}
		a, b := d.Num(args[0]), d.Num(args[1])
		val := "(" + sub(a, mul(b, "floor("+div(a, b)+")")) + ")"
		return SQLExpr{Val: val, Err: coalesceErr("CASE WHEN "+b+" = "+pgZero+" THEN 'div_by_zero' END", notFiniteErr(d, val)), Kind: KindNumber}, nil
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
			return SQLExpr{Val: "(CAST(" + x.Val + " AS float8) / " + d.NumberLit(pow10(int(x.Scale))) + ")", Kind: KindNumber, Sure: x.Sure}, nil
		}
		return SQLExpr{Val: rawNum(x), Kind: KindNumber, Sure: x.Sure}, nil
	case "MONEY":
		amount, cur := args[0], d.Text(args[1])
		m := "fado_narrow(" + rawMant(amount) + ")"
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
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::bigint", Kind: KindDecimal}, nil
		}
		return SQLExpr{Val: args[0].Val, Kind: KindDecimal, Scale: args[0].Scale, Sure: args[0].Sure}, nil
	case "CURRENCY":
		return SQLExpr{Val: curOrNull(args[0]), Kind: KindText}, nil
	case "DATE":
		return fn(KindDate, true, d.Num(args[0]), d.Num(args[1]), d.Num(args[2])), nil
	case "DATEVALUE":
		return fn(KindDate, true, d.Text(args[0])), nil
	case "DATETIMEVALUE":
		return fn(KindDateTime, true, d.Text(args[0])), nil
	case "DATEOF":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::float8", Kind: KindDate}, nil
		}
		return SQLExpr{Val: "floor(" + div(args[0].Val, day) + ")", Kind: KindDate, Sure: args[0].Sure}, nil
	case "DATETIME":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::float8", Kind: KindDateTime}, nil
		}
		return SQLExpr{Val: "(" + mul(args[0].Val, day) + ")", Kind: KindDateTime, Sure: args[0].Sure}, nil
	case "YEAR", "MONTH", "DAY", "QUARTER":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::float8", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: part(strings.ToLower(name), secs(args[0])), Kind: KindNumber}, nil
	case "DAYS", "DAYSBETWEEN":
		a, b := args[0], args[1]
		if name == "DAYSBETWEEN" {
			a, b = b, a
		}
		if a.Kind == KindNull || b.Kind == KindNull {
			return SQLExpr{Val: "NULL::float8", Kind: KindNumber}, nil
		}
		if a.Kind == KindDate && b.Kind == KindDate {
			return SQLExpr{Val: "(" + sub(a.Val, b.Val) + ")", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "(" + sub("floor("+div(a.Val, day)+")", "floor("+div(b.Val, day)+")") + ")", Kind: KindNumber}, nil
	case "MONTHSBETWEEN", "YEARSBETWEEN":
		if args[0].Kind == KindNull || args[1].Kind == KindNull {
			return SQLExpr{Val: "NULL::float8", Kind: KindNumber}, nil
		}
		s, e := secs(args[0]), secs(args[1])
		if name == "YEARSBETWEEN" {
			return SQLExpr{Val: "(" + part("year", e) + " - " + part("year", s) + ")", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "((" + part("year", e) + " * 12 + " + part("month", e) + ") - (" + part("year", s) + " * 12 + " + part("month", s) + "))", Kind: KindNumber}, nil
	case "ADDDAYS":
		x := args[0]
		if x.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNull}, nil
		}
		if x.Kind == KindDate {
			val := "(" + add(x.Val, "trunc("+d.Num(args[1])+")") + ")"
			return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateDays) + " OR " + val + " > " + strconv.Itoa(maxDateDays) + " THEN 'out_of_range' END", Kind: KindDate}, nil
		}
		val := "floor(" + add(x.Val, mul(d.Num(args[1]), day)) + ")"
		return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateSecs) + " OR " + val + " > " + strconv.Itoa(maxDateSecs) + " THEN 'out_of_range' END", Kind: KindDateTime}, nil
	case "ADDMONTHS", "ADDYEARS":
		x := args[0]
		if x.Kind == KindDate {
			e := fn(KindDate, true, secs(x), d.Num(args[1]))
			e.Val = "(" + e.Val + " / " + day + ")"
			return e, nil
		}
		if x.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNull}, nil
		}
		return fn(KindDateTime, true, x.Val, d.Num(args[1])), nil
	case "NOW":
		return SQLExpr{Val: "CAST(" + params.Use("now") + " AS float8)", Kind: KindDateTime, Sure: true}, nil
	case "TODAY":
		return SQLExpr{Val: "floor(CAST(" + params.Use("now") + " AS float8) / " + day + ")", Kind: KindDate, Sure: true}, nil
	case "USER":
		return SQLExpr{Val: "CAST(" + params.Use("user") + " AS text)", Kind: KindText}, nil
	case "USERKIND":
		return SQLExpr{Val: "CAST(" + params.Use("userkind") + " AS text)", Kind: KindText}, nil
	case "ACTINGFOR":
		return SQLExpr{Val: "CAST(" + params.Use("actingfor") + " AS text)", Kind: KindText}, nil
	case "STATE":
		return SQLExpr{Val: "CAST(" + params.Use("state") + " AS text)", Kind: KindText}, nil
	case "ID":
		return SQLExpr{Val: "CAST(" + params.Use("id") + " AS text)", Kind: KindText}, nil
	case "FIRSTENTERED", "FIRSTENTEREDBY", "LASTENTERED", "LASTENTEREDBY", "FIRSTLEFT", "FIRSTLEFTBY", "LASTLEFT", "LASTLEFTBY",
		"ENTRIES", "DAYSIN", "FIRSTEDITOR", "LASTEDITOR":
		return d.stateCall(name, args[0], params), nil
	case "INGROUP":
		return SQLExpr{Val: "COALESCE(" + d.Text(args[0]) + " = ANY (CAST(" + params.Use("groups") + " AS text[])), FALSE)", Kind: KindBool, Sure: true}, nil
	case "LEFT", "RIGHT":
		n := intArg(d.Num(args[1]))
		return SQLExpr{Val: strings.ToLower(name) + "(" + d.Text(args[0]) + ", GREATEST(" + n + ", 0))", Err: countErr(n), Kind: KindText, Sure: true}, nil
	case "MID":
		t, p := d.Text(args[0]), intArg(d.Num(args[1]))
		if len(args) == 2 {
			return SQLExpr{Val: "substr(" + t + ", GREATEST(" + p + ", 1))", Err: positionErr(p, ""), Kind: KindText, Sure: true}, nil
		}
		n := intArg(d.Num(args[2]))
		return SQLExpr{Val: "substr(" + t + ", GREATEST(" + p + ", 1), GREATEST(" + n + ", 0))", Err: positionErr(p, n), Kind: KindText, Sure: true}, nil
	case "LEN":
		return SQLExpr{Val: "CAST(length(" + d.Text(args[0]) + ") AS float8)", Kind: KindNumber, Sure: true}, nil
	case "UPPER", "LOWER":
		return SQLExpr{Val: strings.ToLower(name) + "(" + d.Text(args[0]) + ` COLLATE "pg_c_utf8")`, Kind: KindText, Sure: true}, nil
	case "REF":
		// The pointer of a class and an id: Class:id, blank for a blank id.
		id := d.Text(args[1])
		return SQLExpr{Val: "CASE WHEN " + id + " = '' THEN NULL ELSE " + d.Text(args[0]) + " || ':' || " + id + " END", Kind: KindRef}, nil
	case "TRIM", "PROPER":
		e := fn(KindText, false, d.Text(args[0]))
		e.Sure = true
		return e, nil
	case "REPLACE":
		return fn(KindText, true, d.Text(args[0]), d.Num(args[1]), d.Num(args[2]), d.Text(args[3])), nil
	case "SUBSTITUTE":
		return fn(KindText, true, d.Text(args[0]), d.Text(args[1]), d.Text(args[2])), nil
	case "CONCAT":
		return concatExpr(d, args, false), nil
	case "REPEAT":
		return fn(KindText, true, d.Text(args[0]), d.Num(args[1])), nil
	case "URLENCODE":
		return fn(KindText, true, d.Text(args[0])), nil
	case "FINDFIRST":
		return SQLExpr{Val: "CAST(strpos(" + d.Text(args[1]) + ", " + d.Text(args[0]) + ") AS float8)", Kind: KindNumber, Sure: true}, nil
	case "FINDLAST":
		e := fn(KindNumber, false, d.Text(args[0]), d.Text(args[1]))
		e.Sure = true
		return e, nil
	case "PAD":
		fill := "NULL::text"
		if len(args) > 2 {
			fill = d.Text(args[2])
		}
		return fn(KindText, true, d.Text(args[0]), d.Num(args[1]), fill), nil
	case "REGEXREPLACE":
		e := fn(KindText, true, d.Text(args[0]), d.Text(args[1]), d.Text(args[2]))
		e.Sure = true
		return e, nil
	case "TEXT":
		return SQLExpr{Val: d.Render(args[0]), Kind: KindText, Sure: true}, nil
	case "VALUE":
		return fn(KindNumber, true, d.Text(args[0])), nil
	case "JSONVALUE":
		j := "NULL::text"
		if args[0].Kind != KindNull {
			j = args[0].Val
		}
		return fn(KindText, true, j, d.Text(args[1])), nil
	}
	return SQLExpr{}, fmt.Errorf("no Postgres translation")
}

// exactAggregate spells MIN, MAX, SUM and AVERAGE over exact values in
// numeric: the mantissas aligned to the largest scale, blanks skipped,
// and the currencies checked.
func (d Postgres) exactAggregate(name string, args []SQLExpr) SQLExpr {
	mants, scale, kind, cur, errs := exactArgs(d, args)
	e := SQLExpr{Kind: kind, Scale: int8(scale), Cur: cur}
	switch name {
	case "MIN", "MAX":
		raws := make([]string, len(args))
		for i, a := range args {
			if a.Kind == KindNull {
				raws[i] = "NULL::numeric"
			} else {
				var re string
				raws[i], re = rescaled(d, d.RawMant(a), int(a.Scale), scale)
				if re != "" {
					errs = append(errs, re)
				}
			}
		}
		f := "least"
		if name == "MAX" {
			f = "greatest"
		}
		val := raws[0]
		if len(raws) > 1 {
			val = f + "(" + strings.Join(raws, ", ") + ")"
		}
		e.Val = d.Narrow(val)
		errs = append(errs, "CASE WHEN "+val+" IS NOT NULL AND "+e.Val+" IS NULL THEN 'overflow' END")
	case "SUM":
		val := "(" + strings.Join(mants, " + ") + ")"
		e.Val, e.Sure = d.Narrow(val), true
		errs = append(errs, d.OverflowErr(val))
	default:
		counts := make([]string, len(args))
		for i, a := range args {
			if a.Kind == KindNull {
				counts[i] = "0"
			} else {
				counts[i] = "CAST((" + a.Val + ") IS NOT NULL AS integer)"
			}
		}
		n := "(" + strings.Join(counts, " + ") + ")"
		sum := "(" + strings.Join(mants, " + ") + ")"
		list := sum + ", CAST(" + n + " AS numeric), " + strconv.Itoa(divScale(scale)-scale)
		e.Val = "(CASE WHEN " + n + " = 0 THEN NULL ELSE fado_dec_div(" + list + ") END)"
		e.Scale = int8(divScale(scale))
		errs = append(errs, d.OverflowErr(sum), "CASE WHEN "+n+" > 0 THEN fado_dec_div_err("+list+") END")
	}
	e.Err = coalesceErr(errs...)
	return e
}

// pgSpaces are the characters strings.TrimSpace takes from the ends of
// a text, which VALUE, DATEVALUE and the other readers of a text trim.
const pgSpaces = "\t\n\v\f\r \u0085\u00A0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200A\u2028\u2029\u202F\u205F\u3000"

// pgSQL is a function of one expression, which the planner writes into
// the statement that calls it where it can.
func pgSQL(name, args, returns, body string) string {
	return "CREATE OR REPLACE FUNCTION " + name + "(" + args + ") RETURNS " + returns + " LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$ SELECT " + body + " $fado$"
}

// makePostgresFunctions writes the engine functions as SQL.
func makePostgresFunctions() []string {
	// The bounds of a float8, and the powers of two the far ranges of
	// the arithmetic are scaled by, each spelled exactly.
	p2 := func(e int) string { return pgFloat(math.Ldexp(1, e)) }
	maxF := pgFloat(math.MaxFloat64)
	inf, negInf := "'Infinity'::float8", "'-Infinity'::float8"
	// The numerics a float8 rounds to an infinity from, 2^1024 - 2^970,
	// and to zero at or under, 2^-1075, to the last digit.
	over := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1024), new(big.Int).Lsh(big.NewInt(1), 970)).String()
	under := new(big.Float).SetMantExp(big.NewFloat(1), -1075).Text('f', 1075)
	two52 := "4503599627370496"
	minDate, maxDate := strconv.Itoa(minDateSecs), strconv.Itoa(maxDateSecs)
	minDay, maxDay := strconv.Itoa(minDateDays), strconv.Itoa(maxDateDays)
	number := `'^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$'`
	r := strings.NewReplacer(
		"{MAX}", maxF, "{MAXHALF}", pgFloat(math.MaxFloat64/2), "{INF}", inf, "{NINF}", negInf,
		"{BIG}", pgFloat(8e307), "{FAR}", pgFloat(1e290), "{LO}", pgFloat(1e-150), "{HI}", pgFloat(1e150),
		"{P-537}", p2(-537), "{P537}", p2(537), "{P-50}", p2(-50), "{P-60}", p2(-60), "{P60}", p2(60),
		"{P-1000}", p2(-1000), "{P-512}", p2(-512), "{P512}", p2(512), "{P-40}", p2(-40), "{P-51}", p2(-51),
		"{OVER}", over, "{UNDER}", under, "{2^52}", two52,
		"{MINDATE}", minDate, "{MAXDATE}", maxDate, "{MINDAY}", minDay, "{MAXDAY}", maxDay,
		"{NUMBER}", number, "{SPACES}", "'"+pgSpaces+"'", "{DAY}", pgFloat(secondsPerDay),
	)
	defs := []string{
		// Float arithmetic that cannot fail. Postgres raises an error
		// where a result leaves the range of a float8 and neither operand
		// had left it, and where a result is zero and no operand was. The
		// common range is tested first and goes to the operator. Past
		// it the operands are scaled by a power of two, which is exact,
		// and the scaled result says whether the true one would have
		// been an infinity or zero.
		pgSQL("fado_add", "x float8, y float8", "float8", `CASE WHEN abs(x) < {BIG} AND abs(y) < {BIG} THEN x + y
			WHEN NOT (abs(x) <= {MAX} AND abs(y) <= {MAX}) THEN x + y
			WHEN abs(x) < {FAR} THEN y
			WHEN abs(y) < {FAR} THEN x
			WHEN abs(x / 2 + y / 2) > {MAXHALF} THEN (CASE WHEN x > 0 THEN {INF} ELSE {NINF} END)
			ELSE x + y END`),
		pgSQL("fado_mul", "x float8, y float8", "float8", `CASE WHEN (x = 0 OR (abs(x) >= {LO} AND abs(x) <= {HI})) AND (y = 0 OR (abs(y) >= {LO} AND abs(y) <= {HI})) THEN x * y
			WHEN NOT (abs(x) <= {MAX} AND abs(y) <= {MAX}) OR x = 0 OR y = 0 THEN x * y
			WHEN abs(x) >= 1 AND abs(y) >= 1 THEN (CASE WHEN abs((x * {P-537}) * (y * {P-537})) >= {P-50} THEN (CASE WHEN (x > 0) = (y > 0) THEN {INF} ELSE {NINF} END) ELSE x * y END)
			WHEN abs(x) < 1 AND abs(y) < 1 THEN (CASE WHEN abs((x * {P537}) * (y * {P537})) <= 0.5 THEN 0::float8 ELSE x * y END)
			ELSE x * y END`),
		pgSQL("fado_div", "x float8, y float8", "float8", `CASE WHEN y = 0 THEN NULL
			WHEN abs(y) >= {LO} AND abs(y) <= {HI} AND (x = 0 OR (abs(x) >= {LO} AND abs(x) <= {HI})) THEN x / y
			WHEN NOT (abs(x) <= {MAX} AND abs(y) <= {MAX}) OR x = 0 THEN x / y
			WHEN abs(x) >= abs(y) THEN (CASE WHEN abs(y) >= 1 OR abs(x) < {P-60} THEN x / y
				WHEN abs(x) * {P-1000} > abs(y) * {P60} OR abs((x * {P-512}) / (y * {P512})) >= 1 THEN (CASE WHEN (x > 0) = (y > 0) THEN {INF} ELSE {NINF} END)
				ELSE x / y END)
			ELSE (CASE WHEN abs(x) >= {P-40} OR abs(y) < 1 THEN x / y
				WHEN abs((x * {P512}) / (y * {P-512})) <= {P-51} THEN 0::float8
				ELSE x / y END) END`),
		// The exponential and the logarithms, by the C library, behind
		// the bounds Go's own give: past them an infinity or zero.
		pgSQL("fado_exp", "x float8", "float8", `CASE WHEN x > '709.782712893384'::float8 THEN {INF} WHEN x < '-745.14'::float8 THEN 0::float8
			WHEN x < -708 THEN fado_mul(exp(x + 100), exp('-100'::float8)) ELSE exp(x) END`),
		pgSQL("fado_ln", "x float8", "float8", `CASE WHEN x > 0 THEN ln(x) END`),
		pgSQL("fado_log10", "x float8", "float8", `CASE WHEN x > 0 THEN log(x) END`),
		// The power. Postgres refuses zero to a negative power and a
		// negative number to a power with a fraction, where the VM has
		// an infinity and a NaN; and a result past the range. Where the
		// result is near either end of the range it is the square of the
		// power of half the exponent, which the multiplication above
		// brings to an infinity or zero as the power itself would be.
		pgSQL("fado_power", "x float8, y float8", "float8", `CASE WHEN x = 0 AND y < 0 THEN {INF}
			WHEN x = {NINF} AND floor(y) <> y THEN (CASE WHEN y < 0 THEN 0::float8 ELSE {INF} END)
			WHEN x < 0 AND floor(y) <> y THEN 'NaN'::float8
			WHEN NOT (abs(x) <= {MAX} AND abs(y) <= {MAX}) OR x = 0 THEN power(x, y)
			WHEN fado_mul(y, ln(abs(x))) > 709.9 THEN (CASE WHEN x < 0 AND floor(y / 2) * 2 <> y THEN {NINF} ELSE {INF} END)
			WHEN fado_mul(y, ln(abs(x))) < -745.2 THEN 0::float8
			WHEN fado_mul(y, ln(abs(x))) BETWEEN -708 AND 709.7 THEN power(x, y)
			ELSE fado_mul(fado_mul(power(abs(x), y / 2), power(abs(x), y / 2)), CASE WHEN x < 0 AND floor(y / 2) * 2 <> y THEN -1 ELSE 1 END) END`),
		// The rows of a sum, added one by one from zero in the order
		// they are given, blanks skipped.
		`CREATE OR REPLACE FUNCTION fado_sum_step(float8, float8) RETURNS float8 LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $fado$ SELECT fado_add($1, $2) $fado$`,
		`CREATE OR REPLACE AGGREGATE fado_sum_rows(float8) (SFUNC = fado_sum_step, STYPE = float8, INITCOND = '0')`,
		// TEXT of a number: C's %.15g, which is what Postgres writes a
		// float8 with when it adds no digits of its own.
		`CREATE OR REPLACE FUNCTION fado_num_text(x float8) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE SET extra_float_digits = 0 AS $fado$ SELECT CASE WHEN x = 0 THEN '0' ELSE x::text END $fado$`,
		// Rounding, as the VM's roundTo and scaledStep, operation for
		// operation. A power of ten comes from its decimal spelling,
		// which Postgres reads to the nearest float8, as the VM's table
		// holds it. A scaled value of 2^52 or more is an integer already
		// and the value is returned as it is.
		pgSQL("fado_pow10", "d float8", "float8", `CAST('1e' || CAST(CAST(LEAST(GREATEST(trunc(d), -323), 308) AS integer) AS text) AS float8)`),
		pgSQL("fado_scale", "d float8", "float8", `fado_pow10(LEAST(GREATEST(trunc(d), -300), 300))`),
		pgSQL("fado_rnd", "v float8", "float8", `CASE WHEN NOT (abs(v) < {2^52}) THEN v ELSE trunc(v) + CASE WHEN abs(v - trunc(v)) >= 0.5 THEN sign(v) ELSE 0 END END`),
		pgSQL("fado_rnd_even", "v float8", "float8", `CASE WHEN NOT (abs(v) < {2^52}) THEN v
			WHEN abs(v - trunc(v)) > 0.5 OR (abs(v - trunc(v)) = 0.5 AND trunc(v) - 2 * floor(trunc(v) / 2) <> 0) THEN trunc(v) + sign(v)
			ELSE trunc(v) END`),
		pgSQL("fado_round", "x float8, d float8", "float8", `CASE WHEN trunc(d) > 300 THEN x WHEN trunc(d) < -300 THEN 0::float8
			WHEN NOT (abs(fado_mul(x, fado_pow10(d))) < {2^52}) THEN x
			ELSE fado_div(fado_rnd(fado_mul(x, fado_pow10(d))), fado_pow10(d)) END`),
		pgSQL("fado_round_even", "x float8, d float8", "float8", `CASE WHEN trunc(d) > 300 THEN x WHEN trunc(d) < -300 THEN 0::float8
			WHEN NOT (abs(fado_mul(x, fado_pow10(d))) < {2^52}) THEN x
			ELSE fado_div(fado_rnd_even(fado_mul(x, fado_pow10(d))), fado_pow10(d)) END`),
		pgSQL("fado_floor", "x float8, d float8", "float8", `CASE WHEN NOT (abs(fado_mul(x, fado_scale(d))) < {2^52}) THEN x
			ELSE fado_div(floor(fado_round(fado_mul(x, fado_scale(d)), 9)), fado_scale(d)) END`),
		pgSQL("fado_ceiling", "x float8, d float8", "float8", `CASE WHEN NOT (abs(fado_mul(x, fado_scale(d))) < {2^52}) THEN x
			ELSE fado_div(ceil(fado_round(fado_mul(x, fado_scale(d)), 9)), fado_scale(d)) END`),
		pgSQL("fado_trunc", "x float8, d float8", "float8", `CASE WHEN NOT (abs(fado_mul(x, fado_scale(d))) < {2^52}) THEN x
			ELSE fado_div(trunc(fado_round(fado_mul(x, fado_scale(d)), 9)), fado_scale(d)) END`),
		// ROUND with a method (ADR-0022): up is away from zero.
		pgSQL("fado_round_m", "x float8, d float8, m text", "float8", `CASE m WHEN 'half_even' THEN fado_round_even(x, d) WHEN 'down' THEN fado_trunc(x, d)
			WHEN 'up' THEN (CASE WHEN x < 0 THEN fado_floor(x, d) ELSE fado_ceiling(x, d) END)
			WHEN 'floor' THEN fado_floor(x, d) WHEN 'ceiling' THEN fado_ceiling(x, d) ELSE fado_round(x, d) END`),
		// Exact arithmetic (ADR-0022) in numeric. A power of ten is built
		// from its digits.
		pgSQL("fado_narrow", "v numeric", "bigint", `CASE WHEN v >= -9223372036854775808 AND v <= 9223372036854775807 THEN CAST(v AS bigint) END`),
		pgSQL("fado_hpow10", "k integer", "numeric", `CAST('1' || repeat('0', GREATEST(k, 0)) AS numeric)`),
		// The dividend scaled by at most 10^19, as in the other dialects:
		// the magnitude check below reports overflow for any dividend
		// past an int64 at that power.
		pgSQL("fado_dec_x", "x numeric, k integer", "numeric", `x * fado_hpow10(LEAST(k, 19))`),
		// Division rounds half away from zero: the quotient truncated
		// toward zero, plus one in the quotient's direction when twice
		// the remainder reaches the divisor.
		pgSQL("fado_dec_div_raw", "x numeric, y numeric, k integer", "numeric", `div(fado_dec_x(x, k), NULLIF(y, 0))
			+ CASE WHEN abs(mod(fado_dec_x(x, k), NULLIF(y, 0))) * 2 >= abs(y) THEN (CASE WHEN (fado_dec_x(x, k) < 0) = (y < 0) THEN 1 ELSE -1 END) ELSE 0 END`),
		pgSQL("fado_dec_div", "x numeric, y numeric, k integer", "bigint", `CASE WHEN y = 0 OR abs(fado_dec_x(x, k)) > 9223372036854775807 THEN NULL ELSE fado_narrow(fado_dec_div_raw(x, y, k)) END`),
		pgSQL("fado_dec_div_err", "x numeric, y numeric, k integer", "text", `CASE WHEN y = 0 THEN 'div_by_zero' WHEN abs(fado_dec_x(x, k)) > 9223372036854775807 OR fado_narrow(fado_dec_div_raw(x, y, k)) IS NULL THEN 'overflow' END`),
		// Rounding a mantissa m at scale s to p places by method meth, as
		// the VM's roundMant: the dropped digits decide by the method,
		// and negative places scale the result back up.
		pgSQL("fado_dec_rounded", "m numeric, s integer, p integer, meth text", "numeric", `div(m, fado_hpow10(s - p)) + CASE WHEN mod(m, fado_hpow10(s - p)) = 0 THEN 0
			WHEN meth = 'half_even' THEN (CASE WHEN abs(mod(m, fado_hpow10(s - p))) * 2 > fado_hpow10(s - p) OR (abs(mod(m, fado_hpow10(s - p))) * 2 = fado_hpow10(s - p) AND mod(div(m, fado_hpow10(s - p)), 2) <> 0) THEN sign(m) ELSE 0 END)
			WHEN meth = 'down' THEN 0 WHEN meth = 'up' THEN sign(m)
			WHEN meth = 'floor' THEN (CASE WHEN m < 0 THEN -1 ELSE 0 END) WHEN meth = 'ceiling' THEN (CASE WHEN m > 0 THEN 1 ELSE 0 END)
			ELSE (CASE WHEN abs(mod(m, fado_hpow10(s - p))) * 2 >= fado_hpow10(s - p) THEN sign(m) ELSE 0 END) END`),
		pgSQL("fado_dec_round_raw", "m numeric, s integer, p integer, meth text", "numeric", `CASE WHEN p >= s THEN m * fado_hpow10(p - s) WHEN p >= 0 THEN fado_dec_rounded(m, s, p, meth) ELSE fado_dec_rounded(m, s, p, meth) * fado_hpow10(-p) END`),
		pgSQL("fado_dec_round", "m numeric, s integer, p integer, meth text", "bigint", `fado_narrow(fado_dec_round_raw(m, s, p, meth))`),
		pgSQL("fado_dec_round_err", "m numeric, s integer, p integer, meth text", "text", `CASE WHEN m IS NOT NULL AND fado_narrow(fado_dec_round_raw(m, s, p, meth)) IS NULL THEN 'overflow' END`),
		// Rendering: every digit after the point.
		pgSQL("fado_dec_digits", "m numeric, s integer", "text", `CASE WHEN length(CAST(abs(m) AS text)) <= s THEN repeat('0', s + 1 - length(CAST(abs(m) AS text))) || CAST(abs(m) AS text) ELSE CAST(abs(m) AS text) END`),
		pgSQL("fado_dec_text", "m numeric, s integer", "text", `CASE WHEN m IS NULL THEN NULL ELSE (CASE WHEN m < 0 THEN '-' ELSE '' END)
			|| (CASE WHEN s = 0 THEN fado_dec_digits(m, s) ELSE substr(fado_dec_digits(m, s), 1, length(fado_dec_digits(m, s)) - s) || '.' || substr(fado_dec_digits(m, s), length(fado_dec_digits(m, s)) - s + 1) END) END`),
		// Parsing, as the VM's parseDecimal. fado_dec_scan reads a text
		// that is trimmed already: it answers the error code, or = and
		// the number as its digits and its scale, which is read as a
		// numeric then. A zero is zero whatever its exponent. More than
		// nineteen digits, more than eighteen places or an exponent past
		// a million is an overflow, as written and not as valued.
		pgSQL("fado_ws", "t text", "text", `btrim(t, {SPACES})`),
		`CREATE OR REPLACE FUNCTION fado_dec_scan(w text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN NOT a.ok THEN 'bad_text'
				WHEN b.digits = '' THEN '=0'
				WHEN length(b.es) > 7 OR abs(c.e) > 1000000 THEN 'overflow'
				WHEN length(b.frac) - c.e < -19 OR length(b.frac) - c.e > 18 OR length(b.digits) + GREATEST(c.e - length(b.frac), 0) > 19 THEN 'overflow'
				WHEN CAST(b.digits || repeat('0', GREATEST(c.e - length(b.frac), 0)) AS numeric) > (CASE WHEN left(w, 1) = '-' THEN 9223372036854775808 ELSE 9223372036854775807 END) THEN 'overflow'
				ELSE '=' || (CASE WHEN left(w, 1) = '-' THEN '-' ELSE '' END) || b.digits || repeat('0', GREATEST(c.e - length(b.frac), 0)) || 'e' || CAST(-GREATEST(length(b.frac) - c.e, 0) AS text) END
			FROM (SELECT w ~ {NUMBER} AS ok, ltrim(w, '+-') AS body) a,
			LATERAL (SELECT CASE WHEN a.ok THEN ltrim(replace(substring(a.body from '^[^eE]*'), '.', ''), '0') ELSE '' END AS digits,
				CASE WHEN a.ok THEN split_part(substring(a.body from '^[^eE]*'), '.', 2) ELSE '' END AS frac,
				CASE WHEN a.ok THEN COALESCE(substring(a.body from '[eE](.*)$'), '') ELSE '' END AS ex,
				CASE WHEN a.ok THEN ltrim(ltrim(COALESCE(substring(a.body from '[eE](.*)$'), ''), '+-'), '0') ELSE '' END AS es) b,
			LATERAL (SELECT CASE WHEN b.es ~ '^[0-9]{1,7}$' THEN (CASE WHEN left(b.ex, 1) = '-' THEN -1 ELSE 1 END) * CAST(b.es AS integer) ELSE 0 END AS e) c
		$fado$`,
		`CREATE OR REPLACE FUNCTION fado_dec_parse_w(w text, p integer) RETURNS bigint LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN q.n = trunc(q.n) THEN fado_narrow(q.n) END
			FROM (SELECT fado_dec_scan(w) AS v) r, LATERAL (SELECT CASE WHEN left(r.v, 1) = '=' THEN CAST(substr(r.v, 2) AS numeric) * fado_hpow10(p) END AS n) q
		$fado$`,
		`CREATE OR REPLACE FUNCTION fado_dec_parse_w_err(w text, p integer) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN left(r.v, 1) <> '=' THEN r.v WHEN q.n <> trunc(q.n) THEN 'inexact' WHEN fado_narrow(q.n) IS NULL THEN 'overflow' END
			FROM (SELECT fado_dec_scan(w) AS v) r, LATERAL (SELECT CASE WHEN left(r.v, 1) = '=' THEN CAST(substr(r.v, 2) AS numeric) * fado_hpow10(p) END AS n) q
		$fado$`,
		pgSQL("fado_dec_parse", "t text, p integer", "bigint", `CASE WHEN fado_ws(t) = '' THEN NULL ELSE fado_dec_parse_w(fado_ws(t), p) END`),
		pgSQL("fado_dec_parse_err", "t text, p integer", "text", `CASE WHEN fado_ws(t) = '' THEN NULL ELSE fado_dec_parse_w_err(fado_ws(t), p) END`),
		// MONEYVALUE, as the VM's parseMoney: an exact amount, the first
		// space, a currency of three capital letters.
		pgSQL("fado_money_ok", "t text", "boolean", `strpos(fado_ws(t), ' ') > 0 AND substr(fado_ws(t), strpos(fado_ws(t), ' ') + 1) ~ '^[A-Z]{3}$'`),
		pgSQL("fado_money_part", "t text", "text", `substr(fado_ws(t), 1, GREATEST(strpos(fado_ws(t), ' ') - 1, 0))`),
		pgSQL("fado_money_cur", "t text", "text", `CASE WHEN fado_money_ok(t) AND left(fado_dec_scan(fado_money_part(t)), 1) = '=' THEN substr(fado_ws(t), strpos(fado_ws(t), ' ') + 1) END`),
		pgSQL("fado_money_amount", "t text, p integer", "bigint", `CASE WHEN fado_money_ok(t) THEN fado_dec_parse_w(fado_money_part(t), p) END`),
		pgSQL("fado_money_amount_err", "t text, p integer", "text", `CASE WHEN fado_ws(t) = '' THEN NULL WHEN NOT fado_money_ok(t) THEN 'bad_text' ELSE fado_dec_parse_w_err(fado_money_part(t), p) END`),
		pgSQL("fado_currency", "t text", "text", `CASE WHEN COALESCE(t, '') ~ '^[A-Z]{3}$' THEN t END`),
		pgSQL("fado_currency_err", "t text", "text", `CASE WHEN COALESCE(t, '') !~ '^[A-Z]{3}$' THEN 'bad_text' END`),
		// DECIMAL of a number: the scaled value stepped by the method on
		// the float rules, then narrowed.
		pgSQL("fado_dec_fstep", "x float8, p integer, meth text", "float8", `CASE meth WHEN 'half_even' THEN fado_rnd_even(fado_mul(x, fado_pow10(p)))
			WHEN 'down' THEN trunc(fado_round(fado_mul(x, fado_pow10(p)), 9))
			WHEN 'up' THEN (CASE WHEN fado_round(fado_mul(x, fado_pow10(p)), 9) < 0 THEN floor(fado_round(fado_mul(x, fado_pow10(p)), 9)) ELSE ceil(fado_round(fado_mul(x, fado_pow10(p)), 9)) END)
			WHEN 'floor' THEN floor(fado_round(fado_mul(x, fado_pow10(p)), 9)) WHEN 'ceiling' THEN ceil(fado_round(fado_mul(x, fado_pow10(p)), 9))
			ELSE fado_rnd(fado_mul(x, fado_pow10(p))) END`),
		pgSQL("fado_dec_from", "x float8, p integer, meth text", "bigint", `CASE WHEN x IS NULL OR NOT (abs(x) <= {MAX}) OR NOT (abs(fado_dec_fstep(x, p, meth)) < 9223372036854775808) THEN NULL ELSE CAST(fado_dec_fstep(x, p, meth) AS bigint) END`),
		pgSQL("fado_dec_from_err", "x float8, p integer, meth text", "text", `CASE WHEN x IS NULL THEN NULL WHEN NOT (abs(x) <= {MAX}) THEN 'not_a_number' WHEN NOT (abs(fado_dec_fstep(x, p, meth)) < 9223372036854775808) THEN 'overflow' END`),
		// MOD on aligned mantissas, with the divisor's sign.
		pgSQL("fado_dec_mod", "a numeric, b numeric", "bigint", `CASE WHEN b = 0 THEN NULL WHEN b = -1 THEN CAST(0 AS bigint)
			ELSE fado_narrow(CASE WHEN mod(a, NULLIF(b, 0)) <> 0 AND ((mod(a, NULLIF(b, 0)) < 0) <> (b < 0)) THEN mod(a, NULLIF(b, 0)) + b ELSE mod(a, NULLIF(b, 0)) END) END`),
		pgSQL("fado_dec_mod_err", "a numeric, b numeric", "text", `CASE WHEN b = 0 THEN 'div_by_zero' END`),
		// Dates: seconds to a timestamp. A moment past the years a date
		// may have is still a moment the VM reads the year and the month
		// of, so the seconds are bounded only where Postgres's own
		// timestamp ends, near the year 290000, and at the year 1 below:
		// a value past those never fails. A NaN, the greatest of numbers
		// here, becomes the last second.
		pgSQL("fado_ts", "x float8", "timestamp", `CASE WHEN x IS NULL THEN NULL ELSE timestamp '1970-01-01 00:00:00' + CAST(LEAST(GREATEST(x, {MINDATE}), 9100000000000) AS bigint) * interval '1 second' END`),
		pgSQL("fado_year_text", "ts timestamp", "text", `lpad(CAST(CAST(extract(year FROM ts) AS integer) AS text), GREATEST(length(CAST(CAST(extract(year FROM ts) AS integer) AS text)), 4), '0')`),
		`CREATE OR REPLACE FUNCTION fado_date_text(days float8) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT fado_year_text(s.ts) || '-' || lpad(CAST(CAST(extract(month FROM s.ts) AS integer) AS text), 2, '0') || '-' || lpad(CAST(CAST(extract(day FROM s.ts) AS integer) AS text), 2, '0')
			FROM (SELECT fado_ts(fado_mul(days, {DAY})) AS ts) s
		$fado$`,
		`CREATE OR REPLACE FUNCTION fado_datetime_text(secs float8) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT fado_year_text(s.ts) || '-' || lpad(CAST(CAST(extract(month FROM s.ts) AS integer) AS text), 2, '0') || '-' || lpad(CAST(CAST(extract(day FROM s.ts) AS integer) AS text), 2, '0')
				|| ' ' || lpad(CAST(CAST(extract(hour FROM s.ts) AS integer) AS text), 2, '0') || ':' || lpad(CAST(CAST(extract(minute FROM s.ts) AS integer) AS text), 2, '0') || ':' || lpad(CAST(CAST(floor(extract(second FROM s.ts)) AS integer) AS text), 2, '0')
			FROM (SELECT fado_ts(secs) AS ts) s
		$fado$`,
		// The days since the epoch of the first of a month, by the civil
		// arithmetic of the proleptic Gregorian calendar, in plain floats,
		// exact at these magnitudes: the callers bound the year first.
		`CREATE OR REPLACE FUNCTION fado_civil_days(yy float8, mm float8) RETURNS float8 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT e.era * 146097 + f.yoe * 365 + floor(f.yoe / 4) - floor(f.yoe / 100) + floor((153 * a.mp + 2) / 5) - 719468
			FROM (SELECT yy - CASE WHEN mm <= 2 THEN 1 ELSE 0 END AS yr, (mm + 9) - 12 * floor((mm + 9) / 12) AS mp) a,
			LATERAL (SELECT floor(a.yr / 400) AS era) e, LATERAL (SELECT a.yr - e.era * 400 AS yoe) f
		$fado$`,
		pgSQL("fado_dim", "yy float8, mm float8", "float8", `(CASE WHEN mm >= 12 THEN fado_civil_days(yy + 1, 1) ELSE fado_civil_days(yy, mm + 1) END) - fado_civil_days(yy, mm)`),
		// A date of a year, a month and a day as written: NULL where the
		// month or the day is none of that month's.
		pgSQL("fado_ymd", "yy float8, mm float8, dd float8", "float8", `CASE WHEN mm >= 1 AND mm <= 12 AND dd >= 1 AND dd <= fado_dim(yy, mm) THEN fado_civil_days(yy, mm) + dd - 1 END`),
		// DATE: the month carries into the year, the day is added to the
		// first of that month, as the VM's time.Date does. The arguments
		// are bounded first, past what the error reports, so that the
		// arithmetic cannot leave a float's range.
		`CREATE OR REPLACE FUNCTION fado_date(y float8, m float8, d float8) RETURNS float8 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT fado_civil_days(floor(b.months / 12), b.months - floor(b.months / 12) * 12 + 1) + c.dd - 1
			FROM (SELECT LEAST(GREATEST(trunc(y), -10000000), 10000000) AS yy, LEAST(GREATEST(trunc(m), -100000000), 100000000) AS mm, LEAST(GREATEST(trunc(d), -10000000000), 10000000000) AS dd) c,
			LATERAL (SELECT c.yy * 12 + c.mm - 1 AS months) b
		$fado$`,
		pgSQL("fado_date_err", "y float8, m float8, d float8", "text", `CASE WHEN abs(trunc(y)) > 1e6 OR abs(trunc(m)) > 1e7 OR abs(trunc(d)) > 1e9 OR fado_date(y, m, d) < {MINDAY} OR fado_date(y, m, d) > {MAXDAY} THEN 'out_of_range' END`),
		`CREATE OR REPLACE FUNCTION fado_datevalue(t text) RETURNS float8 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN s.w ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' THEN fado_ymd(CAST(substr(s.w, 1, 4) AS float8), CAST(substr(s.w, 6, 2) AS float8), CAST(substr(s.w, 9, 2) AS float8)) END
			FROM (SELECT fado_ws(t) AS w) s
		$fado$`,
		pgSQL("fado_datevalue_err", "t text", "text", `CASE WHEN fado_ws(t) = '' THEN NULL WHEN fado_datevalue(t) IS NULL THEN 'bad_text' WHEN fado_datevalue(t) < {MINDAY} OR fado_datevalue(t) > {MAXDAY} THEN 'out_of_range' END`),
		// DATETIMEVALUE, as Go reads the two layouts: a date alone, or a
		// date, one space or more and a time, whose hour may be one digit
		// and whose seconds may have a fraction, which is dropped.
		`CREATE OR REPLACE FUNCTION fado_datetimevalue(t text) RETURNS float8 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN s.w ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' THEN fado_ymd(CAST(substr(s.w, 1, 4) AS float8), CAST(substr(s.w, 6, 2) AS float8), CAST(substr(s.w, 9, 2) AS float8)) * 86400
				WHEN s.w ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2} +[0-9]{1,2}:[0-9]{2}:[0-9]{2}([.,][0-9]+)?$' THEN
					(CASE WHEN CAST(split_part(ltrim(substr(s.w, 11), ' '), ':', 1) AS integer) < 24 AND CAST(split_part(ltrim(substr(s.w, 11), ' '), ':', 2) AS integer) < 60 AND CAST(substr(split_part(ltrim(substr(s.w, 11), ' '), ':', 3), 1, 2) AS integer) < 60
					THEN fado_ymd(CAST(substr(s.w, 1, 4) AS float8), CAST(substr(s.w, 6, 2) AS float8), CAST(substr(s.w, 9, 2) AS float8)) * 86400
						+ CAST(split_part(ltrim(substr(s.w, 11), ' '), ':', 1) AS integer) * 3600 + CAST(split_part(ltrim(substr(s.w, 11), ' '), ':', 2) AS integer) * 60 + CAST(substr(split_part(ltrim(substr(s.w, 11), ' '), ':', 3), 1, 2) AS integer) END) END
			FROM (SELECT fado_ws(t) AS w) s
		$fado$`,
		pgSQL("fado_datetimevalue_err", "t text", "text", `CASE WHEN fado_ws(t) = '' THEN NULL WHEN fado_datetimevalue(t) IS NULL THEN 'bad_text' WHEN fado_datetimevalue(t) < {MINDATE} OR fado_datetimevalue(t) > {MAXDATE} THEN 'out_of_range' END`),
		// ADDMONTHS: the months are added to the year and the month, and
		// the day is kept, or is the last of the month where the month
		// has no such day. The time of the day stays.
		`CREATE OR REPLACE FUNCTION fado_addmonths(x float8, n float8) RETURNS float8 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN x IS NULL THEN NULL ELSE (fado_civil_days(e.ny, e.nm) + LEAST(b.dd, fado_dim(e.ny, e.nm)) - 1) * 86400 + b.sod END
			FROM (SELECT fado_ts(x) AS ts, LEAST(GREATEST(trunc(n), -20000000), 20000000) AS k) a,
			LATERAL (SELECT CAST(extract(year FROM a.ts) AS float8) AS yy, CAST(extract(month FROM a.ts) AS float8) AS mm, CAST(extract(day FROM a.ts) AS float8) AS dd,
				CAST(extract(hour FROM a.ts) * 3600 + extract(minute FROM a.ts) * 60 + floor(extract(second FROM a.ts)) AS float8) AS sod) b,
			LATERAL (SELECT b.yy * 12 + (b.mm - 1) + a.k AS tm) c,
			LATERAL (SELECT floor(c.tm / 12) AS ny, c.tm - floor(c.tm / 12) * 12 + 1 AS nm) e
		$fado$`,
		pgSQL("fado_addmonths_err", "x float8, n float8", "text", `CASE WHEN x IS NULL THEN NULL WHEN abs(trunc(n)) > 1e7 OR fado_addmonths(x, n) < {MINDATE} OR fado_addmonths(x, n) > {MAXDATE} THEN 'out_of_range' END`),
		pgSQL("fado_addyears", "x float8, n float8", "float8", `fado_addmonths(x, fado_mul(12, trunc(n)))`),
		pgSQL("fado_addyears_err", "x float8, n float8", "text", `fado_addmonths_err(x, fado_mul(12, trunc(n)))`),
		// Text.
		pgSQL("fado_trim", "t text", "text", `regexp_replace(btrim(t, ' '), ' +', ' ', 'g')`),
		// PROPER: a letter that follows no letter is made capital, the
		// others small, one character at a time as the VM does.
		`CREATE OR REPLACE FUNCTION fado_proper(t text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT COALESCE(string_agg(CASE WHEN NOT c.letter THEN c.ch WHEN c.after THEN lower(c.ch COLLATE "pg_c_utf8") ELSE upper(c.ch COLLATE "pg_c_utf8") END, '' ORDER BY c.i), '')
			FROM (SELECT i, substr(t, i, 1) AS ch, (substr(t, i, 1) COLLATE "pg_c_utf8") ~ '^[[:alpha:]]$' AS letter,
				i > 1 AND (substr(t, i - 1, 1) COLLATE "pg_c_utf8") ~ '^[[:alpha:]]$' AS after
				FROM generate_series(1, length(t)) AS i) c
		$fado$`,
		// Text builders under the text limit m (ADR-0021), as in the
		// DuckDB dialect: each keeps the value it builds bounded by
		// clamping the argument that makes it grow, and reports
		// out_of_range from a length worked out without building it.
		pgSQL("fado_replace_start", "t text, p float8", "float8", `LEAST(GREATEST(trunc(p), 1), length(t) + 1)`),
		pgSQL("fado_replace_cut", "t text, p float8, n float8", "float8", `LEAST(GREATEST(trunc(n), 0), length(t) - fado_replace_start(t, p) + 1)`),
		pgSQL("fado_replace_len", "t text, p float8, n float8, s text", "float8", `length(t) - fado_replace_cut(t, p, n) + length(s)`),
		pgSQL("fado_replace", "t text, p float8, n float8, s text, m integer", "text", `substr(t, 1, CAST(fado_replace_start(t, p) AS integer) - 1) || (CASE WHEN fado_replace_len(t, p, n, s) > m THEN '' ELSE s END) || substr(t, CAST(fado_replace_start(t, p) + fado_replace_cut(t, p, n) AS integer))`),
		pgSQL("fado_replace_err", "t text, p float8, n float8, s text, m integer", "text", `CASE WHEN trunc(p) < 1 OR trunc(n) < 0 OR fado_replace_len(t, p, n, s) > m THEN 'out_of_range' END`),
		pgSQL("fado_substitute_len", "t text, p text, n text", "bigint", `length(t) + CASE WHEN length(p) = 0 THEN 0 ELSE CAST((length(t) - length(replace(t, p, ''))) / length(p) AS bigint) * (length(n) - length(p)) END`),
		pgSQL("fado_substitute", "t text, p text, n text, m integer", "text", `CASE WHEN p = '' THEN t ELSE replace(t, p, CASE WHEN fado_substitute_len(t, p, n) > m THEN p ELSE n END) END`),
		pgSQL("fado_substitute_err", "t text, p text, n text, m integer", "text", `CASE WHEN p <> '' AND fado_substitute_len(t, p, n) > m THEN 'out_of_range' END`),
		pgSQL("fado_repeat", "t text, n float8, m integer", "text", `repeat(t, CAST(CASE WHEN trunc(n) < 0 OR length(t) = 0 OR fado_mul(trunc(n), length(t)) > m THEN 0 ELSE trunc(n) END AS integer))`),
		pgSQL("fado_repeat_err", "t text, n float8, m integer", "text", `CASE WHEN trunc(n) < 0 OR fado_mul(trunc(n), length(t)) > m THEN 'out_of_range' END`),
		// URLENCODE: every byte that is not unreserved becomes three
		// characters, so the length is known from the bytes and the
		// unreserved characters among them.
		pgSQL("fado_urlencode_len", "t text", "bigint", `3 * CAST(octet_length(t) AS bigint) - 2 * length(regexp_replace(t, '[^A-Za-z0-9._~-]', '', 'g'))`),
		`CREATE OR REPLACE FUNCTION fado_urlencode(t text, m integer) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN fado_urlencode_len(t) > m THEN '' ELSE
				(SELECT COALESCE(string_agg(CASE WHEN chr(b.c) ~ '^[A-Za-z0-9._~-]$' THEN chr(b.c) ELSE '%' || upper(lpad(to_hex(b.c), 2, '0')) END, '' ORDER BY b.i), '')
				FROM (SELECT i, get_byte(convert_to(t, 'UTF8'), i) AS c FROM generate_series(0, octet_length(t) - 1) AS i) b) END
		$fado$`,
		pgSQL("fado_urlencode_err", "t text, m integer", "text", `CASE WHEN fado_urlencode_len(t) > m THEN 'out_of_range' END`),
		pgSQL("fado_pad_width", "w float8, m integer", "float8", `CASE WHEN trunc(w) < 0 OR trunc(w) > m THEN 0 ELSE trunc(w) END`),
		pgSQL("fado_pad", "t text, w float8, f text, m integer", "text", `CASE WHEN length(t) >= fado_pad_width(w, m) THEN t ELSE lpad(t, CAST(fado_pad_width(w, m) AS integer), COALESCE(NULLIF(substr(f, 1, 1), ''), ' ')) END`),
		pgSQL("fado_pad_err", "t text, w float8, f text, m integer", "text", `CASE WHEN trunc(w) < 0 OR trunc(w) > m THEN 'out_of_range' END`),
		// REGEXREPLACE. A pattern Postgres cannot read fails the
		// statement, which only a handler of the error can stop:
		// fado_regex is the dialect's one function written in PL/pgSQL,
		// and a statement that calls it runs in no parallel worker. It
		// answers = and the text, or the error code. Every match is
		// first put between two marks, characters the text does not
		// have. Go takes an empty match right after a match as none,
		// where Postgres replaces it too, so such a pair of marks is
		// taken out. The marks then say how many matches there are and
		// how long the text will be, before it is built, and the
		// replacement is taken as it is written. The classes of a
		// pattern are those of the C rule, ASCII, as Go's are, and Go's
		// \b, \B and \z are written as Postgres writes them, \y, \Y
		// and \Z.
		pgSQL("fado_regex_pattern", "p text", "text", `regexp_replace(regexp_replace(regexp_replace(p, '(?<!\\)((?:\\\\)*)\\b', '\1\\y', 'g'), '(?<!\\)((?:\\\\)*)\\B', '\1\\Y', 'g'), '(?<!\\)((?:\\\\)*)\\z', '\1\\Z', 'g')`),
		`CREATE OR REPLACE FUNCTION fado_regex(t text, p text, r text, m integer) RETURNS text LANGUAGE plpgsql IMMUTABLE PARALLEL UNSAFE AS $fado$
		DECLARE
			a text := chr(57344);
			z text := chr(57345);
			marked text;
			matches bigint;
		BEGIN
			WHILE strpos(t, a) > 0 OR strpos(t, z) > 0 LOOP
				a := chr(ascii(a) + 2);
				z := chr(ascii(z) + 2);
			END LOOP;
			marked := replace(regexp_replace(t COLLATE "C", fado_regex_pattern(p), a || '\&' || z, 'g'), z || a || z, z);
			matches := (length(marked) - length(t)) / 2;
			IF matches = 0 THEN
				RETURN '=' || t;
			END IF;
			IF length(regexp_replace(marked, a || '[^' || z || ']*' || z, '', 'g')) + matches * length(r) > m THEN
				RETURN 'out_of_range';
			END IF;
			RETURN '=' || regexp_replace(marked, a || '[^' || z || ']*' || z, replace(r, '\', '\\'), 'g');
		EXCEPTION WHEN invalid_regular_expression THEN
			RETURN 'bad_text';
		END $fado$`,
		`CREATE OR REPLACE FUNCTION fado_regexreplace(t text, p text, r text, m integer) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL UNSAFE AS $fado$ SELECT CASE WHEN left(fado_regex(t, p, r, m), 1) = '=' THEN substr(fado_regex(t, p, r, m), 2) ELSE t END $fado$`,
		`CREATE OR REPLACE FUNCTION fado_regexreplace_err(t text, p text, r text, m integer) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL UNSAFE AS $fado$ SELECT CASE WHEN left(fado_regex(t, p, r, m), 1) <> '=' THEN fado_regex(t, p, r, m) END $fado$`,
		pgSQL("fado_findlast", "s text, t text", "float8", `CASE WHEN strpos(reverse(t), reverse(s)) = 0 THEN 0::float8 ELSE CAST(length(t) - strpos(reverse(t), reverse(s)) - length(s) + 2 AS float8) END`),
		// VALUE. A number as written is a numeric first, which holds any
		// spelling exactly: 0.d1d2... times a power of ten, of its first
		// 800 digits. It is a float8 where it rounds to one, an infinity
		// past the range and zero under it, and so never fails.
		pgSQL("fado_num_float", "v numeric", "float8", `CASE WHEN abs(v) >= {OVER} THEN (CASE WHEN v > 0 THEN {INF} ELSE {NINF} END) WHEN abs(v) <= {UNDER} THEN 0::float8 ELSE CAST(v AS float8) END`),
		`CREATE OR REPLACE FUNCTION fado_value_w(w text) RETURNS float8 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT CASE WHEN b.sig = '' THEN 0::float8
				WHEN c.mag > 400 THEN (CASE WHEN left(w, 1) = '-' THEN {NINF} ELSE {INF} END)
				WHEN c.mag < -400 THEN 0::float8
				ELSE fado_num_float(CAST((CASE WHEN left(w, 1) = '-' THEN '-' ELSE '' END) || '0.' || left(b.sig, 800) || (CASE WHEN length(b.sig) > 800 THEN '1' ELSE '' END) || 'e' || CAST(c.mag AS text) AS numeric)) END
			FROM (SELECT ltrim(w, '+-') AS body) a,
			LATERAL (SELECT ltrim(replace(substring(a.body from '^[^eE]*'), '.', ''), '0') AS sig,
				length(split_part(substring(a.body from '^[^eE]*'), '.', 1)) - (length(replace(substring(a.body from '^[^eE]*'), '.', '')) - length(ltrim(replace(substring(a.body from '^[^eE]*'), '.', ''), '0'))) AS lead,
				COALESCE(substring(a.body from '[eE](.*)$'), '') AS ex,
				ltrim(ltrim(COALESCE(substring(a.body from '[eE](.*)$'), ''), '+-'), '0') AS es) b,
			LATERAL (SELECT b.lead + CASE WHEN b.es = '' THEN 0 WHEN length(b.es) > 7 THEN (CASE WHEN left(b.ex, 1) = '-' THEN -10000000 ELSE 10000000 END)
				ELSE (CASE WHEN left(b.ex, 1) = '-' THEN -1 ELSE 1 END) * CAST(b.es AS integer) END AS mag) c
		$fado$`,
		pgSQL("fado_value", "t text", "float8", `CASE WHEN fado_ws(t) = '' THEN NULL WHEN fado_ws(t) !~ {NUMBER} THEN NULL
			WHEN fado_ws(t) ~ '^[+-]?[0-9]{0,18}(\.[0-9]{0,18})?$' THEN CAST(fado_ws(t) AS float8) ELSE fado_value_w(fado_ws(t)) END`),
		pgSQL("fado_value_err", "t text", "text", `CASE WHEN fado_ws(t) = '' THEN NULL WHEN fado_ws(t) !~ {NUMBER} THEN 'bad_text' WHEN NOT (abs(fado_value(t)) <= {MAX}) THEN 'not_a_number' END`),
		// JSON: the dotted path is followed a name into an object and a
		// whole number into a list, as the VM's jsonAt follows it;
		// numbers render as TEXT does. A text that is no JSON is asked
		// after first, so that reading it cannot fail. A number anywhere
		// in it that no float holds makes the whole text none, as Go
		// reads it.
		// The text is read by a function that catches what reading it
		// raises, and answers NULL: a guard beside the reading is not
		// enough, since the planner reads a constant text as it plans,
		// before any guard is asked, and from Postgres 18 on it plans a
		// function's statement with its arguments as constants. A JSON
		// null is read as JSON's null, which is no NULL.
		`CREATE OR REPLACE FUNCTION fado_jsonb(j text) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE PARALLEL UNSAFE AS $fado$
			BEGIN RETURN CAST(j AS jsonb); EXCEPTION WHEN OTHERS THEN RETURN NULL; END
		$fado$`,
		`CREATE OR REPLACE FUNCTION fado_jsonat(j text, p text) RETURNS jsonb LANGUAGE sql IMMUTABLE PARALLEL UNSAFE AS $fado$
			SELECT CASE WHEN j IS NULL OR fado_jsonb(j) IS NULL THEN NULL ELSE (
				WITH RECURSIVE path AS (SELECT string_to_array(p, '.') AS seg),
				walk(i, cur) AS (
					SELECT 0, fado_jsonb(j)
					UNION ALL
					SELECT walk.i + 1, CASE jsonb_typeof(walk.cur) WHEN 'object' THEN walk.cur -> path.seg[walk.i + 1]
						WHEN 'array' THEN (CASE WHEN path.seg[walk.i + 1] ~ '^(0|[1-9][0-9]{0,8})$' THEN walk.cur -> CAST(path.seg[walk.i + 1] AS integer) END) END
					FROM walk, path WHERE walk.i < COALESCE(cardinality(path.seg), 0) AND walk.cur IS NOT NULL)
				SELECT walk.cur FROM walk, path WHERE walk.i = COALESCE(cardinality(path.seg), 0)) END
		$fado$`,
		`CREATE OR REPLACE FUNCTION fado_jsonvalue(j text, p text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL UNSAFE AS $fado$
			SELECT CASE jsonb_typeof(a.at) WHEN 'string' THEN a.at #>> '{}' WHEN 'boolean' THEN upper(CAST(a.at AS text))
				WHEN 'number' THEN fado_num_text(fado_num_float(CAST(a.at AS numeric))) END
			FROM (SELECT fado_jsonat(j, p) AS at) a
		$fado$`,
		`CREATE OR REPLACE FUNCTION fado_jsonvalue_err(j text, p text) RETURNS text LANGUAGE sql IMMUTABLE PARALLEL UNSAFE AS $fado$
			SELECT CASE WHEN j IS NULL THEN NULL WHEN fado_jsonb(j) IS NULL THEN 'bad_text'
				WHEN jsonb_path_exists(fado_jsonb(j), '$.** ? (@.type() == "number" && (@ >= {OVER} || @ <= -{OVER}))') THEN 'bad_text'
				WHEN jsonb_typeof(fado_jsonat(j, p)) IN ('object', 'array') THEN 'bad_text' END
		$fado$`,
		// What a document's life says of one state (ADR-0047, item 6):
		// the states are bound as a JSON array of objects, as StateLife
		// names their facts, and the function gives the object of the
		// named state.
		`CREATE OR REPLACE FUNCTION fado_stay(stays jsonb, name text) RETURNS jsonb LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $fado$
			SELECT e.stay FROM jsonb_array_elements(stays) AS e(stay) WHERE e.stay ->> 'state' = name LIMIT 1
		$fado$`,
	}
	for i := range defs {
		defs[i] = r.Replace(defs[i])
	}
	return defs
}
