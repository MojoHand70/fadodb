package formula

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// The DuckDB dialect (ADR-0017, ADR-0020, ADR-0022). Numbers are DOUBLE
// throughout: DuckDB reads a bare literal such as 2.5 as DECIMAL and
// 1706659200 as a 32-bit integer, so every literal is cast. A date is
// DOUBLE days and a datetime DOUBLE seconds, turned into naive TIMESTAMPs
// through make_timestamp, never to_timestamp, whose result would follow
// the session time zone. Booleans are BOOLEAN; blank is NULL. An exact
// value is its BIGINT mantissa; exact arithmetic runs in HUGEINT, which
// cannot overflow on 64-bit operands, and TRY_CAST narrows the result,
// NULL where it does not fit.
//
// Where DuckDB's own function differs from the rule, the dialect calls a
// macro named like the SQLite dialect's registered function, fado_<name>,
// with fado_<name>_err for the error code. The macros are plain SQL
// expressions that DuckDB inlines at bind time, so set-wise recomputes
// stay vectorised and nothing calls back into Go (ADR-0019). Setup returns
// the statements that create them.

// DuckDB is the dialect for DuckDB through github.com/duckdb/duckdb-go.
type DuckDB struct {
	coercions
	lim Limits
}

// NewDuckDB makes the DuckDB dialect under the default limits.
func NewDuckDB() DuckDB {
	return DuckDB{coercions: coercions{zeroNumber: "0.0::DOUBLE", zeroBool: "FALSE", zeroText: "''", zeroMant: "0::HUGEINT",
		castNumber: func(x string) string { return "CAST(" + x + " AS DOUBLE)" },
		castMant:   func(x string) string { return "CAST(" + x + " AS HUGEINT)" }}, lim: DefaultLimits()}
}

// Limits implements Dialect.
func (d DuckDB) Limits() Limits { return d.lim }

// WithLimits implements Dialect.
func (d DuckDB) WithLimits(l Limits) Dialect {
	d.lim = l
	return d
}

// Name implements Dialect.
func (DuckDB) Name() string { return "duckdb" }

// NumberLit implements Dialect. Every literal is a DOUBLE.
func (DuckDB) NumberLit(f float64) string {
	return "CAST(" + strconv.FormatFloat(f, 'g', -1, 64) + " AS DOUBLE)"
}

// IntLit implements Dialect.
func (DuckDB) IntLit(m int64) string { return "CAST(" + strconv.FormatInt(m, 10) + " AS BIGINT)" }

// TextLit implements Dialect.
func (DuckDB) TextLit(s string) string { return textLiteral(s) }

// True implements Dialect.
func (DuckDB) True() string { return "TRUE" }

// False implements Dialect.
func (DuckDB) False() string { return "FALSE" }

// Load implements Dialect.
func (DuckDB) Load(column string, k Kind) string {
	switch k {
	case KindNumber, KindDate, KindDateTime:
		return "CAST(" + column + " AS DOUBLE)"
	case KindDecimal, KindMoney:
		return "CAST(" + column + " AS BIGINT)"
	case KindBool:
		return "CAST(" + column + " AS BOOLEAN)"
	}
	return column
}

// NotFinite implements Dialect: DuckDB keeps NaN and the infinities.
func (DuckDB) NotFinite(val string) string { return "NOT isfinite(" + val + ")" }

// Narrow implements Dialect.
func (DuckDB) Narrow(val string) string { return "TRY_CAST(" + val + " AS BIGINT)" }

// OverflowErr implements Dialect: a NULL is a blank, not an overflow.
func (DuckDB) OverflowErr(val string) string {
	return "CASE WHEN (" + val + ") IS NOT NULL AND TRY_CAST(" + val + " AS BIGINT) IS NULL THEN 'overflow' END"
}

// ExactRowSum implements Dialect: the mantissas are HUGEINT, so their
// sum is exact, and it fails when it does not narrow.
func (d DuckDB) ExactRowSum(mant string) (string, string) {
	total := "SUM(" + mant + ")"
	return d.Narrow(total), d.OverflowErr(total)
}

// NumberRowSum implements Dialect. DuckDB's sum adds in no fixed order,
// so the rows are listed in key order and folded from zero with the plain
// addition the VM uses.
func (d DuckDB) NumberRowSum(num, order string) (string, string) {
	rows := "COALESCE(list(" + num + order + ") FILTER (WHERE " + num + " IS NOT NULL), CAST([] AS DOUBLE[]))"
	val := "list_reduce(list_prepend(" + d.NumberLit(0) + ", " + rows + "), lambda a, b: a + b)"
	return val, notFiniteErr(d, val)
}

// Pow implements Dialect: DuckDB's pow is the C library's, which the VM's
// double-double power matches.
func (DuckDB) Pow(x, y string) string { return "pow(" + x + ", " + y + ")" }

// Param implements Dialect.
func (DuckDB) Param(name string) string { return "$" + name }

// ColumnType implements Dialect: DOUBLE for a number and for the days
// and seconds of dates, BIGINT for a mantissa, BOOLEAN, VARCHAR.
func (DuckDB) ColumnType(k Kind) string {
	switch k {
	case KindNumber, KindDate, KindDateTime:
		return "DOUBLE"
	case KindDecimal, KindMoney:
		return "BIGINT"
	case KindBool:
		return "BOOLEAN"
	}
	return "VARCHAR"
}

// ContextArgs implements Dialect: groups bind as a list of text.
func (DuckDB) ContextArgs(ctx *Context) []any {
	now, user, state, id, groups := contextValues(ctx)
	var g any
	if groups != nil {
		g = groups
	}
	return lifeArgs(ctx, []any{sql.Named("now", now), sql.Named("user", user), sql.Named("groups", g), sql.Named("state", state), sql.Named("id", id),
		sql.Named("userkind", textOrNil(ctx, func(c *Context) string { return c.Kind })), sql.Named("actingfor", textOrNil(ctx, func(c *Context) string { return c.For }))})
}

// stateCall translates a function of a state (ADR-0047, item 6) for a
// statement that runs over no document row, as the SQLite dialect does:
// the life's states are bound as a JSON array, which the macro
// fado_stay reads the named state's facts from as a struct.
func (d DuckDB) stateCall(name string, state SQLExpr, params *Params) SQLExpr {
	stays := params.Use("stays")
	get := func(key string) string { return "(fado_stay(" + stays + ", " + d.Text(state) + "))." + key }
	switch name {
	case "ENTRIES":
		return SQLExpr{Val: "(CASE WHEN " + stays + " IS NOT NULL THEN CAST(COALESCE(" + get("n") + ", 0) AS DOUBLE) END)", Kind: KindNumber}
	case "DAYSIN":
		secs := get("secs") + " + CASE WHEN " + get("here") + " THEN CAST(" + params.Use("now") + " AS BIGINT) - COALESCE(" + get("le") + ", 0) ELSE 0 END"
		return SQLExpr{Val: "(CASE WHEN " + stays + " IS NOT NULL THEN COALESCE(CAST(" + secs + " AS DOUBLE), " + d.NumberLit(0) + ") / " + d.NumberLit(secondsPerDay) + " END)", Kind: KindNumber}
	}
	kind := builtins[builtinByName[name]].result
	return SQLExpr{Val: d.Load(get(stateFacts[name]), kind), Kind: kind}
}

// Setup implements Dialect: the macro definitions.
func (DuckDB) Setup() []string { return duckdbMacros }

// Render implements Dialect: the TEXT rule of ADR-0019 and ADR-0022.
func (d DuckDB) Render(e SQLExpr) string {
	switch e.Kind {
	case KindNull:
		return "''"
	case KindNumber:
		// Adding zero turns a negative zero into zero, which printf would
		// otherwise render as -0.
		if e.Sure {
			return "printf('%.15g', " + e.Val + " + 0.0::DOUBLE)"
		}
		return "COALESCE(printf('%.15g', " + e.Val + " + 0.0::DOUBLE), '')"
	case KindBool:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' WHEN " + e.Val + " THEN 'TRUE' ELSE 'FALSE' END)"
	case KindDecimal:
		return "COALESCE(fado_dec_text(" + e.Val + ", " + strconv.Itoa(int(e.Scale)) + "), '')"
	case KindMoney:
		return "(CASE WHEN " + curOrNull(e) + " IS NULL THEN '' ELSE fado_dec_text(" + e.Val + ", " + strconv.Itoa(int(e.Scale)) + ") || ' ' || " + e.Cur + " END)"
	case KindDate:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' ELSE strftime(fado_ts(" + e.Val + " * " + d.NumberLit(secondsPerDay) + "), '%Y-%m-%d') END)"
	case KindDateTime:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' ELSE strftime(fado_ts(" + e.Val + "), '%Y-%m-%d %H:%M:%S') END)"
	}
	return d.Text(e)
}

// Call implements Dialect.
func (d DuckDB) Call(name string, args []SQLExpr, params *Params) (SQLExpr, error) {
	macro := func(kind Kind, withErr bool, sqlArgs ...string) SQLExpr {
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
	zero := d.NumberLit(0)
	numOr := func(i int, def string) string {
		if i < len(args) {
			return d.Num(args[i])
		}
		return def
	}
	rawNum := func(e SQLExpr) string {
		switch e.Kind {
		case KindNull:
			return "NULL::DOUBLE"
		case KindBool:
			return "CAST(" + e.Val + " AS DOUBLE)"
		}
		return e.Val
	}
	rawMant := d.RawMant
	count := func(e SQLExpr) string {
		if e.Kind == KindNull {
			return "0"
		}
		return "CAST(" + e.Val + " IS NOT NULL AS INTEGER)"
	}
	exact := false
	for _, a := range args {
		exact = exact || a.Kind.IsExact()
	}
	// intArg truncates a position or count argument to an integer, clamped
	// to -1 below and to one past maxCount above, so that a negative
	// argument is still seen and the cast cannot overflow.
	intArg := func(x string) string {
		return "CAST(LEAST(GREATEST(trunc(" + x + "), -1), " + maxCount + " + 1) AS BIGINT)"
	}
	secs := func(e SQLExpr) string {
		if e.Kind == KindDate {
			return "(" + e.Val + " * " + d.NumberLit(secondsPerDay) + ")"
		}
		return e.Val
	}
	year := func(v string) string { return "CAST(year(fado_ts(" + v + ")) AS DOUBLE)" }
	month := func(v string) string { return "CAST(month(fado_ts(" + v + ")) AS DOUBLE)" }
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
		if name == "ROUND" {
			e := SQLExpr{Val: "fado_round_m(" + d.Num(args[0]) + ", " + numOr(1, zero) + ", " + methodConst(d, args, 2) + ")", Kind: KindNumber, Sure: true}
			return e, nil
		}
		e := macro(KindNumber, false, d.Num(args[0]), numOr(1, zero))
		e.Sure = true
		return e, nil
	case "ABS":
		if exact {
			m := d.Mant(args[0])
			return SQLExpr{Val: "TRY_CAST(abs(" + m + ") AS BIGINT)", Err: "CASE WHEN TRY_CAST(abs(" + m + ") AS BIGINT) IS NULL THEN 'overflow' END",
				Kind: args[0].Kind, Scale: args[0].Scale, Cur: args[0].Cur, Sure: true}, nil
		}
		return SQLExpr{Val: "abs(" + d.Num(args[0]) + ")", Kind: KindNumber, Sure: true}, nil
	case "SIGN":
		if exact {
			x := d.Mant(args[0])
			return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN CAST(1 AS BIGINT) WHEN " + x + " < 0 THEN CAST(-1 AS BIGINT) ELSE CAST(0 AS BIGINT) END)", Kind: KindDecimal, Sure: true}, nil
		}
		x := d.Num(args[0])
		return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN " + d.NumberLit(1) + " WHEN " + x + " < 0 THEN " + d.NumberLit(-1) + " ELSE " + zero + " END)", Kind: KindNumber, Sure: true}, nil
	case "MIN", "MAX":
		if exact {
			return d.exactAggregate(name, args[:len(args)-1]), nil
		}
		list := make([]string, len(args))
		for i, a := range args {
			list[i] = rawNum(a)
		}
		fn := "least"
		if name == "MAX" {
			fn = "greatest"
		}
		if len(list) == 1 {
			return SQLExpr{Val: list[0], Kind: KindNumber}, nil
		}
		return SQLExpr{Val: fn + "(" + strings.Join(list, ", ") + ")", Kind: KindNumber}, nil
	case "SUM", "AVERAGE", "COUNT":
		if exact && name != "COUNT" {
			return d.exactAggregate(name, args[:len(args)-1]), nil
		}
		sums := make([]string, len(args))
		counts := make([]string, len(args))
		for i, a := range args {
			sums[i] = d.Num(a)
			counts[i] = count(a)
		}
		n := "(" + strings.Join(counts, " + ") + ")"
		switch name {
		case "COUNT":
			return SQLExpr{Val: "CAST(" + n + " AS DOUBLE)", Kind: KindNumber, Sure: true}, nil
		case "SUM":
			val := "(" + strings.Join(sums, " + ") + ")"
			return SQLExpr{Val: val, Err: notFiniteErr(d, val), Kind: KindNumber, Sure: true}, nil
		}
		val := "(CASE WHEN " + n + " = 0 THEN NULL ELSE (" + strings.Join(sums, " + ") + ") / " + n + " END)"
		return SQLExpr{Val: val, Err: notFiniteErr(d, "("+strings.Join(sums, " + ")+")"), Kind: KindNumber}, nil
	case "POWER":
		return arith(d, OpPow, args[0], args[1]), nil
	case "SQRT":
		x := d.Num(args[0])
		return SQLExpr{Val: "sqrt(" + x + ")", Err: "CASE WHEN " + x + " < 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "EXP":
		val := "exp(" + d.Num(args[0]) + ")"
		return SQLExpr{Val: val, Err: notFiniteErr(d, val), Kind: KindNumber, Sure: true}, nil
	case "LN":
		x := d.Num(args[0])
		return SQLExpr{Val: "ln(" + x + ")", Err: "CASE WHEN " + x + " <= 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "LOG":
		x := d.Num(args[0])
		if len(args) == 1 {
			return SQLExpr{Val: "log10(" + x + ")", Err: "CASE WHEN " + x + " <= 0 THEN 'not_a_number' END", Kind: KindNumber}, nil
		}
		b := d.Num(args[1])
		val := "(CASE WHEN " + b + " = " + d.NumberLit(10) + " THEN log10(" + x + ") ELSE ln(" + x + ") / ln(" + b + ") END)"
		return SQLExpr{Val: val, Err: "CASE WHEN " + x + " <= 0 OR " + b + " <= 0 OR " + b + " = 1 THEN 'not_a_number' END", Kind: KindNumber}, nil
	case "MOD":
		if exact {
			mants, scale, kind, cur, errs := exactArgs(d, args[:len(args)-1])
			list := mants[0] + ", " + mants[1]
			return SQLExpr{Val: "fado_dec_mod(" + list + ")", Err: coalesceErr(append(errs, "fado_dec_mod_err("+list+")")...), Kind: kind, Scale: int8(scale), Cur: cur}, nil
		}
		a, b := d.Num(args[0]), d.Num(args[1])
		val := "(" + a + " - " + b + " * floor(" + a + " / " + b + "))"
		return SQLExpr{Val: val, Err: coalesceErr("CASE WHEN "+b+" = "+zero+" THEN 'div_by_zero' END", notFiniteErr(d, val)), Kind: KindNumber}, nil
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
			return SQLExpr{Val: "(CAST(" + x.Val + " AS DOUBLE) / " + d.NumberLit(pow10(int(x.Scale))) + ")", Kind: KindNumber, Sure: x.Sure}, nil
		}
		return SQLExpr{Val: rawNum(x), Kind: KindNumber, Sure: x.Sure}, nil
	case "MONEY":
		amount, cur := args[0], d.Text(args[1])
		m := "TRY_CAST(" + rawMant(amount) + " AS BIGINT)"
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
			return SQLExpr{Val: "NULL::BIGINT", Kind: KindDecimal}, nil
		}
		return SQLExpr{Val: args[0].Val, Kind: KindDecimal, Scale: args[0].Scale, Sure: args[0].Sure}, nil
	case "CURRENCY":
		return SQLExpr{Val: curOrNull(args[0]), Kind: KindText}, nil
	case "DATE":
		return macro(KindDate, true, d.Num(args[0]), d.Num(args[1]), d.Num(args[2])), nil
	case "DATEVALUE":
		return macro(KindDate, true, d.Text(args[0])), nil
	case "DATETIMEVALUE":
		return macro(KindDateTime, true, d.Text(args[0])), nil
	case "DATEOF":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::DOUBLE", Kind: KindDate}, nil
		}
		return SQLExpr{Val: "floor(" + args[0].Val + " / " + d.NumberLit(secondsPerDay) + ")", Kind: KindDate, Sure: args[0].Sure}, nil
	case "DATETIME":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::DOUBLE", Kind: KindDateTime}, nil
		}
		return SQLExpr{Val: "(" + args[0].Val + " * " + d.NumberLit(secondsPerDay) + ")", Kind: KindDateTime, Sure: args[0].Sure}, nil
	case "YEAR", "MONTH", "DAY", "QUARTER":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL::DOUBLE", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "CAST(" + strings.ToLower(name) + "(fado_ts(" + secs(args[0]) + ")) AS DOUBLE)", Kind: KindNumber}, nil
	case "DAYS", "DAYSBETWEEN":
		a, b := args[0], args[1]
		if name == "DAYSBETWEEN" {
			a, b = b, a
		}
		if a.Kind == KindNull || b.Kind == KindNull {
			return SQLExpr{Val: "NULL::DOUBLE", Kind: KindNumber}, nil
		}
		if a.Kind == KindDate && b.Kind == KindDate {
			return SQLExpr{Val: "(" + a.Val + " - " + b.Val + ")", Kind: KindNumber}, nil
		}
		day := d.NumberLit(secondsPerDay)
		return SQLExpr{Val: "(floor(" + a.Val + " / " + day + ") - floor(" + b.Val + " / " + day + "))", Kind: KindNumber}, nil
	case "MONTHSBETWEEN", "YEARSBETWEEN":
		if args[0].Kind == KindNull || args[1].Kind == KindNull {
			return SQLExpr{Val: "NULL::DOUBLE", Kind: KindNumber}, nil
		}
		s, e := secs(args[0]), secs(args[1])
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
		val := "floor(" + x.Val + " + " + d.Num(args[1]) + " * " + d.NumberLit(secondsPerDay) + ")"
		return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateSecs) + " OR " + val + " > " + strconv.Itoa(maxDateSecs) + " THEN 'out_of_range' END", Kind: KindDateTime}, nil
	case "ADDMONTHS", "ADDYEARS":
		x := args[0]
		if x.Kind == KindDate {
			e := macro(KindDate, true, secs(x), d.Num(args[1]))
			e.Val = "(" + e.Val + " / " + d.NumberLit(secondsPerDay) + ")"
			return e, nil
		}
		if x.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNull}, nil
		}
		return macro(KindDateTime, true, x.Val, d.Num(args[1])), nil
	case "NOW":
		return SQLExpr{Val: "CAST(" + params.Use("now") + " AS DOUBLE)", Kind: KindDateTime, Sure: true}, nil
	case "TODAY":
		return SQLExpr{Val: "floor(CAST(" + params.Use("now") + " AS DOUBLE) / " + d.NumberLit(secondsPerDay) + ")", Kind: KindDate, Sure: true}, nil
	case "USER":
		return SQLExpr{Val: "CAST(" + params.Use("user") + " AS VARCHAR)", Kind: KindText}, nil
	case "USERKIND":
		return SQLExpr{Val: "CAST(" + params.Use("userkind") + " AS VARCHAR)", Kind: KindText}, nil
	case "ACTINGFOR":
		return SQLExpr{Val: "CAST(" + params.Use("actingfor") + " AS VARCHAR)", Kind: KindText}, nil
	case "STATE":
		return SQLExpr{Val: "CAST(" + params.Use("state") + " AS VARCHAR)", Kind: KindText}, nil
	case "ID":
		return SQLExpr{Val: "CAST(" + params.Use("id") + " AS VARCHAR)", Kind: KindText}, nil
	case "FIRSTENTERED", "FIRSTENTEREDBY", "LASTENTERED", "LASTENTEREDBY", "FIRSTLEFT", "FIRSTLEFTBY", "LASTLEFT", "LASTLEFTBY",
		"ENTRIES", "DAYSIN", "FIRSTEDITOR", "LASTEDITOR":
		return d.stateCall(name, args[0], params), nil
	case "INGROUP":
		return SQLExpr{Val: "COALESCE(list_contains(" + params.Use("groups") + ", " + d.Text(args[0]) + "), FALSE)", Kind: KindBool, Sure: true}, nil
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
		return SQLExpr{Val: "CAST(length(" + d.Text(args[0]) + ") AS DOUBLE)", Kind: KindNumber, Sure: true}, nil
	case "UPPER", "LOWER":
		return SQLExpr{Val: strings.ToLower(name) + "(" + d.Text(args[0]) + ")", Kind: KindText, Sure: true}, nil
	case "TRIM", "PROPER":
		e := macro(KindText, false, d.Text(args[0]))
		e.Sure = true
		return e, nil
	case "REPLACE":
		return macro(KindText, true, d.Text(args[0]), d.Num(args[1]), d.Num(args[2]), d.Text(args[3])), nil
	case "SUBSTITUTE":
		return macro(KindText, true, d.Text(args[0]), d.Text(args[1]), d.Text(args[2])), nil
	case "CONCAT":
		return concatExpr(d, args, false), nil
	case "REPEAT":
		return macro(KindText, true, d.Text(args[0]), d.Num(args[1])), nil
	case "URLENCODE":
		return macro(KindText, true, d.Text(args[0])), nil
	case "FINDFIRST":
		return SQLExpr{Val: "CAST(strpos(" + d.Text(args[1]) + ", " + d.Text(args[0]) + ") AS DOUBLE)", Kind: KindNumber, Sure: true}, nil
	case "FINDLAST":
		e := macro(KindNumber, false, d.Text(args[0]), d.Text(args[1]))
		e.Sure = true
		return e, nil
	case "PAD":
		fill := "NULL"
		if len(args) > 2 {
			fill = d.Text(args[2])
		}
		return macro(KindText, true, d.Text(args[0]), d.Num(args[1]), fill), nil
	case "REGEXREPLACE":
		e := macro(KindText, true, d.Text(args[0]), d.Text(args[1]), d.Text(args[2]))
		e.Sure = true
		return e, nil
	case "TEXT":
		return SQLExpr{Val: d.Render(args[0]), Kind: KindText, Sure: true}, nil
	case "VALUE":
		return macro(KindNumber, true, d.Text(args[0])), nil
	case "JSONVALUE":
		return macro(KindText, true, args[0].Val, d.Text(args[1])), nil
	}
	return SQLExpr{}, fmt.Errorf("no DuckDB translation")
}

// exactAggregate spells MIN, MAX, SUM and AVERAGE over exact values in
// HUGEINT: the mantissas aligned to the largest scale, blanks skipped, and
// the currencies checked.
func (d DuckDB) exactAggregate(name string, args []SQLExpr) SQLExpr {
	mants, scale, kind, cur, errs := exactArgs(d, args)
	e := SQLExpr{Kind: kind, Scale: int8(scale), Cur: cur}
	switch name {
	case "MIN", "MAX":
		raws := make([]string, len(args))
		for i, a := range args {
			if a.Kind == KindNull {
				raws[i] = "NULL::HUGEINT"
			} else {
				var re string
				raws[i], re = rescaled(d, d.RawMant(a), int(a.Scale), scale)
				if re != "" {
					errs = append(errs, re)
				}
			}
		}
		fn := "least"
		if name == "MAX" {
			fn = "greatest"
		}
		val := raws[0]
		if len(raws) > 1 {
			val = fn + "(" + strings.Join(raws, ", ") + ")"
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
				counts[i] = "CAST(" + a.Val + " IS NOT NULL AS INTEGER)"
			}
		}
		n := "(" + strings.Join(counts, " + ") + ")"
		sum := "(" + strings.Join(mants, " + ") + ")"
		list := sum + ", CAST(" + n + " AS HUGEINT), " + strconv.Itoa(divScale(scale)-scale)
		e.Val = "(CASE WHEN " + n + " = 0 THEN NULL ELSE fado_dec_div(" + list + ") END)"
		e.Scale = int8(divScale(scale))
		errs = append(errs, d.OverflowErr(sum), "CASE WHEN "+n+" > 0 THEN fado_dec_div_err("+list+") END")
	}
	e.Err = coalesceErr(errs...)
	return e
}

const (
	duckdbMinDate = "-62135596800"
	duckdbMaxDate = "253402300799"
	duckdbMinDay  = "-719162"
	duckdbMaxDay  = "2932896"
	duckdbMaxInt  = "9223372036854775807::HUGEINT"
)

// duckdbMacros are the engine functions as inlined SQL. A macro body is an
// expression, never a subquery: DuckDB cannot run a subquery macro over
// more than one row.
var duckdbMacros = []string{
	// What a document's life says of one state (ADR-0047, item 6): the
	// states are bound as a JSON array of objects, as StateLife names
	// their facts, and the macro gives the object of the named state.
	// The cast gives the answer its type where the states are a NULL the
	// binder folds away, which is so when there is no document.
	`CREATE OR REPLACE MACRO fado_stay(stays, name) AS CAST(list_filter(from_json(CAST(stays AS JSON), '[{"state":"VARCHAR","fe":"BIGINT","feb":"VARCHAR","le":"BIGINT","leb":"VARCHAR","fl":"BIGINT","flb":"VARCHAR","ll":"BIGINT","llb":"VARCHAR","n":"BIGINT","secs":"BIGINT","here":"BOOLEAN","fed":"VARCHAR","led":"VARCHAR"}]'), lambda fado_s: fado_s.state = name)[1]` +
		` AS STRUCT(state VARCHAR, fe BIGINT, feb VARCHAR, le BIGINT, leb VARCHAR, fl BIGINT, flb VARCHAR, ll BIGINT, llb VARCHAR, n BIGINT, secs BIGINT, here BOOLEAN, fed VARCHAR, led VARCHAR))`,
	// Rounding, as the VM's roundTo and scaledStep, operation for operation.
	// The power of ten comes from its decimal spelling beyond 22, where it is
	// no longer exactly representable: DuckDB's text-to-double conversion is
	// correctly rounded, as is the VM's table, while pow() may be off by an
	// ulp. A scaled value of 2^52 or more is an integer already and the value
	// is returned as it is.
	`CREATE OR REPLACE MACRO fado_pow10(d) AS CASE WHEN abs(trunc(d::DOUBLE)) <= 22 THEN pow(10::DOUBLE, trunc(d::DOUBLE)) ELSE CAST('1e' || CAST(CAST(LEAST(GREATEST(trunc(d::DOUBLE), -323), 308) AS INTEGER) AS VARCHAR) AS DOUBLE) END`,
	`CREATE OR REPLACE MACRO fado_scale(d) AS fado_pow10(LEAST(GREATEST(trunc(d::DOUBLE), -300), 300))`,
	`CREATE OR REPLACE MACRO fado_round(x, d) AS CASE WHEN trunc(d::DOUBLE) > 300 THEN x::DOUBLE WHEN trunc(d::DOUBLE) < -300 THEN 0.0::DOUBLE WHEN abs(x::DOUBLE * fado_pow10(d)) >= 4503599627370496 THEN x::DOUBLE ELSE round(x::DOUBLE * fado_pow10(d)) / fado_pow10(d) END`,
	`CREATE OR REPLACE MACRO fado_round_even(x, d) AS CASE WHEN trunc(d::DOUBLE) > 300 THEN x::DOUBLE WHEN trunc(d::DOUBLE) < -300 THEN 0.0::DOUBLE WHEN abs(x::DOUBLE * fado_pow10(d)) >= 4503599627370496 THEN x::DOUBLE ELSE round_even(x::DOUBLE * fado_pow10(d), 0) / fado_pow10(d) END`,
	`CREATE OR REPLACE MACRO fado_floor(x, d) AS CASE WHEN abs(x::DOUBLE * fado_scale(d)) >= 4503599627370496 THEN x::DOUBLE ELSE floor(fado_round(x::DOUBLE * fado_scale(d), 9)) / fado_scale(d) END`,
	`CREATE OR REPLACE MACRO fado_ceiling(x, d) AS CASE WHEN abs(x::DOUBLE * fado_scale(d)) >= 4503599627370496 THEN x::DOUBLE ELSE ceil(fado_round(x::DOUBLE * fado_scale(d), 9)) / fado_scale(d) END`,
	`CREATE OR REPLACE MACRO fado_trunc(x, d) AS CASE WHEN abs(x::DOUBLE * fado_scale(d)) >= 4503599627370496 THEN x::DOUBLE ELSE trunc(fado_round(x::DOUBLE * fado_scale(d), 9)) / fado_scale(d) END`,
	// ROUND with a method (ADR-0022): up is away from zero.
	`CREATE OR REPLACE MACRO fado_round_m(x, d, m) AS CASE m WHEN 'half_even' THEN fado_round_even(x, d) WHEN 'down' THEN fado_trunc(x, d) WHEN 'up' THEN (CASE WHEN x::DOUBLE < 0 THEN fado_floor(x, d) ELSE fado_ceiling(x, d) END) WHEN 'floor' THEN fado_floor(x, d) WHEN 'ceiling' THEN fado_ceiling(x, d) ELSE fado_round(x, d) END`,
	// Exact arithmetic (ADR-0022) in HUGEINT. A power of ten is built from
	// its digits, which is exact where pow() is not.
	`CREATE OR REPLACE MACRO fado_hpow10(k) AS CAST('1' || repeat('0', CAST(GREATEST(k, 0) AS INTEGER)) AS HUGEINT)`,
	// The dividend scaled by at most 10^19 stays within HUGEINT; a larger
	// power would overflow the engine, and the magnitude check below reports
	// overflow for any non-zero dividend at that power anyway.
	`CREATE OR REPLACE MACRO fado_dec_x(x, k) AS x::HUGEINT * fado_hpow10(LEAST(k, 19))`,
	`CREATE OR REPLACE MACRO fado_dec_q(x, y, k) AS fado_dec_x(x, k) // NULLIF(y::HUGEINT, 0)`,
	`CREATE OR REPLACE MACRO fado_dec_r(x, y, k) AS fado_dec_x(x, k) % NULLIF(y::HUGEINT, 0)`,
	// Division rounds half away from zero: the quotient truncated toward
	// zero, plus one in the quotient's direction when twice the remainder
	// reaches the divisor.
	`CREATE OR REPLACE MACRO fado_dec_div_raw(x, y, k) AS fado_dec_q(x, y, k) + CASE WHEN abs(fado_dec_r(x, y, k)) * 2 >= abs(y::HUGEINT) THEN (CASE WHEN (fado_dec_x(x, k) < 0) = (y::HUGEINT < 0) THEN 1 ELSE -1 END) ELSE 0 END`,
	`CREATE OR REPLACE MACRO fado_dec_div(x, y, k) AS CASE WHEN y::HUGEINT = 0 OR abs(fado_dec_x(x, k)) > ` + duckdbMaxInt + ` THEN NULL ELSE TRY_CAST(fado_dec_div_raw(x, y, k) AS BIGINT) END`,
	`CREATE OR REPLACE MACRO fado_dec_div_err(x, y, k) AS CASE WHEN y::HUGEINT = 0 THEN 'div_by_zero' WHEN abs(fado_dec_x(x, k)) > ` + duckdbMaxInt + ` OR TRY_CAST(fado_dec_div_raw(x, y, k) AS BIGINT) IS NULL THEN 'overflow' END`,
	// Rounding a mantissa m at scale s to p places by method meth, as the
	// VM's roundMant: the dropped digits decide by the method, and negative
	// places scale the result back up.
	`CREATE OR REPLACE MACRO fado_dec_pw(s, p) AS fado_hpow10(s - p)`,
	`CREATE OR REPLACE MACRO fado_dec_rq(m, s, p) AS m::HUGEINT // fado_dec_pw(s, p)`,
	`CREATE OR REPLACE MACRO fado_dec_rr(m, s, p) AS m::HUGEINT % fado_dec_pw(s, p)`,
	`CREATE OR REPLACE MACRO fado_dec_sign(m) AS CASE WHEN m::HUGEINT > 0 THEN 1 WHEN m::HUGEINT < 0 THEN -1 ELSE 0 END`,
	`CREATE OR REPLACE MACRO fado_dec_rounded(m, s, p, meth) AS fado_dec_rq(m, s, p) + CASE WHEN fado_dec_rr(m, s, p) = 0 THEN 0 WHEN meth = 'half_even' THEN (CASE WHEN abs(fado_dec_rr(m, s, p)) * 2 > fado_dec_pw(s, p) OR (abs(fado_dec_rr(m, s, p)) * 2 = fado_dec_pw(s, p) AND fado_dec_rq(m, s, p) % 2 <> 0) THEN fado_dec_sign(m) ELSE 0 END) WHEN meth = 'down' THEN 0 WHEN meth = 'up' THEN fado_dec_sign(m) WHEN meth = 'floor' THEN (CASE WHEN m::HUGEINT < 0 THEN -1 ELSE 0 END) WHEN meth = 'ceiling' THEN (CASE WHEN m::HUGEINT > 0 THEN 1 ELSE 0 END) ELSE (CASE WHEN abs(fado_dec_rr(m, s, p)) * 2 >= fado_dec_pw(s, p) THEN fado_dec_sign(m) ELSE 0 END) END`,
	`CREATE OR REPLACE MACRO fado_dec_round_raw(m, s, p, meth) AS CASE WHEN p >= s THEN m::HUGEINT * fado_hpow10(p - s) WHEN p >= 0 THEN fado_dec_rounded(m, s, p, meth) ELSE fado_dec_rounded(m, s, p, meth) * fado_hpow10(-p) END`,
	`CREATE OR REPLACE MACRO fado_dec_round(m, s, p, meth) AS TRY_CAST(fado_dec_round_raw(m, s, p, meth) AS BIGINT)`,
	`CREATE OR REPLACE MACRO fado_dec_round_err(m, s, p, meth) AS CASE WHEN m IS NOT NULL AND TRY_CAST(fado_dec_round_raw(m, s, p, meth) AS BIGINT) IS NULL THEN 'overflow' END`,
	// Rendering: every digit after the point.
	`CREATE OR REPLACE MACRO fado_dec_mag(m) AS CAST(abs(m::HUGEINT) AS VARCHAR)`,
	`CREATE OR REPLACE MACRO fado_dec_digits(m, s) AS CASE WHEN length(fado_dec_mag(m)) <= CAST(s AS INTEGER) THEN repeat('0', CAST(s AS INTEGER) + 1 - length(fado_dec_mag(m))) || fado_dec_mag(m) ELSE fado_dec_mag(m) END`,
	`CREATE OR REPLACE MACRO fado_dec_text(m, s) AS CASE WHEN m IS NULL THEN NULL ELSE (CASE WHEN m::HUGEINT < 0 THEN '-' ELSE '' END) || (CASE WHEN s = 0 THEN fado_dec_digits(m, s) ELSE substr(fado_dec_digits(m, s), 1, length(fado_dec_digits(m, s)) - CAST(s AS INTEGER)) || '.' || substr(fado_dec_digits(m, s), length(fado_dec_digits(m, s)) - CAST(s AS INTEGER) + 1) END) END`,
	// Parsing: the text's own scale is counted from its spelling, the
	// mantissa read through DECIMAL(38, 18) and its digits, then brought to
	// the places asked for, which must drop zeros only.
	`CREATE OR REPLACE MACRO fado_dec_syntax(t) AS regexp_full_match(trim(t::VARCHAR), '[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?')`,
	`CREATE OR REPLACE MACRO fado_dec_exp(t) AS regexp_extract(trim(t::VARCHAR), '[eE]([+-]?[0-9]+)', 1)`,
	`CREATE OR REPLACE MACRO fado_dec_scale(t) AS length(regexp_extract(trim(t::VARCHAR), '\.([0-9]*)', 1)) - CASE WHEN fado_dec_exp(t) = '' THEN 0 ELSE COALESCE(TRY_CAST(fado_dec_exp(t) AS BIGINT), 999) END`,
	`CREATE OR REPLACE MACRO fado_dec_zero(t) AS regexp_full_match(trim(t::VARCHAR), '[+-]?(0+\.?0*|\.0+)([eE][+-]?[0-9]+)?')`,
	`CREATE OR REPLACE MACRO fado_dec_m18(t) AS TRY_CAST(replace(CAST(TRY_CAST(trim(t::VARCHAR) AS DECIMAL(38, 18)) AS VARCHAR), '.', '') AS HUGEINT)`,
	`CREATE OR REPLACE MACRO fado_dec_parse_raw(t, p) AS CASE WHEN fado_dec_m18(t) % fado_hpow10(18 - p) = 0 THEN fado_dec_m18(t) // fado_hpow10(18 - p) END`,
	`CREATE OR REPLACE MACRO fado_dec_parse(t, p) AS CASE WHEN trim(t::VARCHAR) = '' OR NOT fado_dec_syntax(t) THEN NULL WHEN fado_dec_zero(t) THEN CAST(0 AS BIGINT) WHEN fado_dec_scale(t) > 18 THEN NULL ELSE TRY_CAST(fado_dec_parse_raw(t, p) AS BIGINT) END`,
	`CREATE OR REPLACE MACRO fado_dec_parse_err(t, p) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN NOT fado_dec_syntax(t) THEN 'bad_text' WHEN fado_dec_zero(t) THEN NULL WHEN fado_dec_scale(t) > 18 OR fado_dec_m18(t) IS NULL THEN 'overflow' WHEN fado_dec_m18(t) % fado_hpow10(18 - p) <> 0 THEN 'inexact' WHEN TRY_CAST(fado_dec_parse_raw(t, p) AS BIGINT) IS NULL THEN 'overflow' END`,
	`CREATE OR REPLACE MACRO fado_money_ok(t) AS regexp_full_match(trim(t::VARCHAR), '[^ ]+ [A-Z]{3}')`,
	`CREATE OR REPLACE MACRO fado_money_cur(t) AS CASE WHEN fado_money_ok(t) AND fado_dec_syntax(split_part(trim(t::VARCHAR), ' ', 1)) THEN split_part(trim(t::VARCHAR), ' ', 2) END`,
	`CREATE OR REPLACE MACRO fado_money_amount(t, p) AS CASE WHEN fado_money_ok(t) THEN fado_dec_parse(split_part(trim(t::VARCHAR), ' ', 1), p) END`,
	`CREATE OR REPLACE MACRO fado_money_amount_err(t, p) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN NOT fado_money_ok(t) THEN 'bad_text' ELSE fado_dec_parse_err(split_part(trim(t::VARCHAR), ' ', 1), p) END`,
	`CREATE OR REPLACE MACRO fado_currency(t) AS CASE WHEN regexp_full_match(COALESCE(t::VARCHAR, ''), '[A-Z]{3}') THEN t::VARCHAR END`,
	`CREATE OR REPLACE MACRO fado_currency_err(t) AS CASE WHEN NOT regexp_full_match(COALESCE(t::VARCHAR, ''), '[A-Z]{3}') THEN 'bad_text' END`,
	// DECIMAL of a number: the scaled value stepped by the method on the
	// float rules, then narrowed.
	`CREATE OR REPLACE MACRO fado_dec_fscaled(x, p) AS x::DOUBLE * fado_pow10(p)`,
	`CREATE OR REPLACE MACRO fado_dec_fstep(x, p, meth) AS CASE meth WHEN 'half_even' THEN round_even(fado_dec_fscaled(x, p), 0) WHEN 'down' THEN trunc(fado_round(fado_dec_fscaled(x, p), 9)) WHEN 'up' THEN (CASE WHEN fado_round(fado_dec_fscaled(x, p), 9) < 0 THEN floor(fado_round(fado_dec_fscaled(x, p), 9)) ELSE ceil(fado_round(fado_dec_fscaled(x, p), 9)) END) WHEN 'floor' THEN floor(fado_round(fado_dec_fscaled(x, p), 9)) WHEN 'ceiling' THEN ceil(fado_round(fado_dec_fscaled(x, p), 9)) ELSE round(fado_dec_fscaled(x, p)) END`,
	`CREATE OR REPLACE MACRO fado_dec_from(x, p, meth) AS CASE WHEN x IS NULL OR NOT isfinite(x::DOUBLE) OR abs(fado_dec_fstep(x, p, meth)) >= 9223372036854775808 THEN NULL ELSE CAST(fado_dec_fstep(x, p, meth) AS BIGINT) END`,
	`CREATE OR REPLACE MACRO fado_dec_from_err(x, p, meth) AS CASE WHEN x IS NULL THEN NULL WHEN NOT isfinite(x::DOUBLE) THEN 'not_a_number' WHEN abs(fado_dec_fstep(x, p, meth)) >= 9223372036854775808 THEN 'overflow' END`,
	// MOD on aligned mantissas, with the divisor's sign.
	`CREATE OR REPLACE MACRO fado_dec_rem(a, b) AS a::HUGEINT % NULLIF(b::HUGEINT, 0)`,
	`CREATE OR REPLACE MACRO fado_dec_mod(a, b) AS CASE WHEN b::HUGEINT = 0 THEN NULL WHEN b::HUGEINT = -1 THEN CAST(0 AS BIGINT) ELSE TRY_CAST(CASE WHEN fado_dec_rem(a, b) <> 0 AND ((fado_dec_rem(a, b) < 0) <> (b::HUGEINT < 0)) THEN fado_dec_rem(a, b) + b::HUGEINT ELSE fado_dec_rem(a, b) END AS BIGINT) END`,
	`CREATE OR REPLACE MACRO fado_dec_mod_err(a, b) AS CASE WHEN b::HUGEINT = 0 THEN 'div_by_zero' END`,
	// Dates: seconds to a naive timestamp, clamped so that garbage never
	// raises. The NULL test comes first because LEAST and GREATEST ignore
	// NULLs, which would turn a blank into the first day of year 1.
	`CREATE OR REPLACE MACRO fado_ts(x) AS CASE WHEN x IS NULL THEN NULL ELSE make_timestamp(CAST(LEAST(GREATEST(x::DOUBLE, ` + duckdbMinDate + `), ` + duckdbMaxDate + `) AS BIGINT) * 1000000) END`,
	// DATE by the civil-calendar arithmetic of the proleptic Gregorian
	// calendar: the month carries into the year, the day is added to the
	// first of that month, and the day count since the epoch is range
	// checked. Plain arithmetic on doubles, exact at these magnitudes, so no
	// clamp is needed and a far-off month and day cannot cancel into a date
	// that looks valid. The same carry as the VM's time.Date normalisation.
	`CREATE OR REPLACE MACRO fado_civil_y(yy, mm) AS yy - CASE WHEN mm <= 2 THEN 1 ELSE 0 END`,
	`CREATE OR REPLACE MACRO fado_civil_era(yy, mm) AS floor(fado_civil_y(yy, mm) / 400)`,
	`CREATE OR REPLACE MACRO fado_civil_yoe(yy, mm) AS fado_civil_y(yy, mm) - fado_civil_era(yy, mm) * 400`,
	`CREATE OR REPLACE MACRO fado_civil_days(yy, mm) AS fado_civil_era(yy, mm) * 146097 + fado_civil_yoe(yy, mm) * 365 + floor(fado_civil_yoe(yy, mm) / 4) - floor(fado_civil_yoe(yy, mm) / 100) + floor((153 * ((mm + 9) % 12) + 2) / 5) - 719468`,
	`CREATE OR REPLACE MACRO fado_date_months(y, m) AS trunc(y::DOUBLE) * 12 + trunc(m::DOUBLE) - 1`,
	`CREATE OR REPLACE MACRO fado_date_year(y, m) AS floor(fado_date_months(y, m) / 12)`,
	`CREATE OR REPLACE MACRO fado_date_month(y, m) AS fado_date_months(y, m) - fado_date_year(y, m) * 12 + 1`,
	`CREATE OR REPLACE MACRO fado_date_day(y, m, d) AS fado_civil_days(fado_date_year(y, m), fado_date_month(y, m)) + trunc(d::DOUBLE) - 1`,
	`CREATE OR REPLACE MACRO fado_date(y, m, d) AS fado_date_day(y, m, d)`,
	`CREATE OR REPLACE MACRO fado_date_err(y, m, d) AS CASE WHEN abs(trunc(y::DOUBLE)) > 1e6 OR abs(trunc(m::DOUBLE)) > 1e7 OR abs(trunc(d::DOUBLE)) > 1e9 OR fado_date_day(y, m, d) < ` + duckdbMinDay + ` OR fado_date_day(y, m, d) > ` + duckdbMaxDay + ` THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_datevalue(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN NOT regexp_full_match(trim(t::VARCHAR), '[0-9]{4}-[0-9]{2}-[0-9]{2}') THEN NULL ELSE epoch(try_strptime(trim(t::VARCHAR), '%Y-%m-%d')) / 86400 END`,
	`CREATE OR REPLACE MACRO fado_datevalue_err(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN fado_datevalue(t) IS NULL THEN 'bad_text' WHEN fado_datevalue(t) < ` + duckdbMinDay + ` OR fado_datevalue(t) > ` + duckdbMaxDay + ` THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_datetimevalue(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN NOT regexp_full_match(trim(t::VARCHAR), '[0-9]{4}-[0-9]{2}-[0-9]{2}( [0-9]{2}:[0-9]{2}:[0-9]{2})?') THEN NULL ELSE COALESCE(epoch(try_strptime(trim(t::VARCHAR), '%Y-%m-%d')), epoch(try_strptime(trim(t::VARCHAR), '%Y-%m-%d %H:%M:%S'))) END`,
	`CREATE OR REPLACE MACRO fado_datetimevalue_err(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN fado_datetimevalue(t) IS NULL THEN 'bad_text' WHEN fado_datetimevalue(t) < ` + duckdbMinDate + ` OR fado_datetimevalue(t) > ` + duckdbMaxDate + ` THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_addmonths(x, n) AS epoch(fado_ts(x) + to_months(CAST(LEAST(GREATEST(trunc(n::DOUBLE), -1000000), 1000000) AS INTEGER)))`,
	`CREATE OR REPLACE MACRO fado_addmonths_err(x, n) AS CASE WHEN x IS NULL THEN NULL WHEN abs(trunc(n::DOUBLE)) > 1e7 OR fado_addmonths(x, n) < ` + duckdbMinDate + ` OR fado_addmonths(x, n) > ` + duckdbMaxDate + ` THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_addyears(x, n) AS fado_addmonths(x, 12 * trunc(n::DOUBLE))`,
	`CREATE OR REPLACE MACRO fado_addyears_err(x, n) AS fado_addmonths_err(x, 12 * trunc(n::DOUBLE))`,
	// Text.
	`CREATE OR REPLACE MACRO fado_trim(t) AS regexp_replace(trim(t::VARCHAR, ' '), ' +', ' ', 'g')`,
	`CREATE OR REPLACE MACRO fado_proper(t) AS COALESCE(list_aggregate(list_transform(str_split(t::VARCHAR, ''), lambda c, i: CASE WHEN regexp_matches(c, '\p{L}') AND (i = 1 OR NOT regexp_matches(str_split(t::VARCHAR, '')[i - 1], '\p{L}')) THEN upper(c) WHEN regexp_matches(c, '\p{L}') THEN lower(c) ELSE c END), 'string_agg', ''), '')`,
	// Text builders under the text limit m (ADR-0021). Each keeps the value
	// it builds bounded by clamping the argument that makes it grow, since
	// DuckDB folds a constant branch before a CASE could skip it, and
	// reports out_of_range from a length worked out without building the
	// result. REPLACE reads the position and the length clamped to the text;
	// SUBSTITUTE counts the occurrences from a bounded deletion; REGEXREPLACE
	// counts the matches as the difference of two bounded replacements,
	// which agree with the replacement itself on empty matches.
	`CREATE OR REPLACE MACRO fado_replace_start(t, p) AS LEAST(GREATEST(trunc(p::DOUBLE), 1), length(t::VARCHAR) + 1)`,
	`CREATE OR REPLACE MACRO fado_replace_cut(t, p, n) AS LEAST(GREATEST(trunc(n::DOUBLE), 0), length(t::VARCHAR) - fado_replace_start(t, p) + 1)`,
	`CREATE OR REPLACE MACRO fado_replace_len(t, p, n, s) AS length(t::VARCHAR) - fado_replace_cut(t, p, n) + length(s::VARCHAR)`,
	`CREATE OR REPLACE MACRO fado_replace(t, p, n, s, m) AS substr(t::VARCHAR, 1, CAST(fado_replace_start(t, p) AS BIGINT) - 1) || (CASE WHEN fado_replace_len(t, p, n, s) > m THEN '' ELSE s::VARCHAR END) || substr(t::VARCHAR, CAST(fado_replace_start(t, p) + fado_replace_cut(t, p, n) AS BIGINT))`,
	`CREATE OR REPLACE MACRO fado_replace_err(t, p, n, s, m) AS CASE WHEN trunc(p::DOUBLE) < 1 OR trunc(n::DOUBLE) < 0 OR fado_replace_len(t, p, n, s) > m THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_substitute_len(t, p, n) AS length(t::VARCHAR) + CASE WHEN length(p::VARCHAR) = 0 THEN 0 ELSE (length(t::VARCHAR) - length(replace(t::VARCHAR, p::VARCHAR, ''))) // length(p::VARCHAR) * (length(n::VARCHAR) - length(p::VARCHAR)) END`,
	`CREATE OR REPLACE MACRO fado_substitute(t, p, n, m) AS CASE WHEN p::VARCHAR = '' THEN t::VARCHAR ELSE replace(t::VARCHAR, NULLIF(p::VARCHAR, ''), CASE WHEN fado_substitute_len(t, p, n) > m THEN p::VARCHAR ELSE n::VARCHAR END) END`,
	`CREATE OR REPLACE MACRO fado_substitute_err(t, p, n, m) AS CASE WHEN p::VARCHAR <> '' AND fado_substitute_len(t, p, n) > m THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_repeat(t, n, m) AS repeat(t::VARCHAR, CAST(CASE WHEN trunc(n::DOUBLE) < 0 OR length(t::VARCHAR) = 0 OR trunc(n::DOUBLE) * length(t::VARCHAR) > m THEN 0 ELSE trunc(n::DOUBLE) END AS INTEGER))`,
	`CREATE OR REPLACE MACRO fado_repeat_err(t, n, m) AS CASE WHEN trunc(n::DOUBLE) < 0 OR trunc(n::DOUBLE) * length(t::VARCHAR) > m THEN 'out_of_range' END`,
	// URLENCODE: every byte that is not unreserved becomes three
	// characters, so the length is known from the bytes and the unreserved
	// characters among them.
	`CREATE OR REPLACE MACRO fado_urlencode_len(t) AS 3 * strlen(t::VARCHAR) - 2 * length(regexp_replace(t::VARCHAR, '[^A-Za-z0-9._~-]', '', 'g'))`,
	`CREATE OR REPLACE MACRO fado_urlencode(t, m) AS CASE WHEN fado_urlencode_len(t) > m THEN '' ELSE url_encode(t::VARCHAR) END`,
	`CREATE OR REPLACE MACRO fado_urlencode_err(t, m) AS CASE WHEN fado_urlencode_len(t) > m THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_pad_width(w, m) AS CASE WHEN trunc(w::DOUBLE) < 0 OR trunc(w::DOUBLE) > m THEN 0 ELSE trunc(w::DOUBLE) END`,
	`CREATE OR REPLACE MACRO fado_pad(t, w, f, m) AS CASE WHEN length(t::VARCHAR) >= fado_pad_width(w, m) THEN t::VARCHAR ELSE lpad(t::VARCHAR, CAST(fado_pad_width(w, m) AS INTEGER), COALESCE(NULLIF(substr(f::VARCHAR, 1, 1), ''), ' ')) END`,
	`CREATE OR REPLACE MACRO fado_pad_err(t, w, f, m) AS CASE WHEN trunc(w::DOUBLE) < 0 OR trunc(w::DOUBLE) > m THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_regexreplace_len(t, p, r) AS length(regexp_replace(t::VARCHAR, p::VARCHAR, '', 'g')) + (length(regexp_replace(t::VARCHAR, p::VARCHAR, 'X', 'g')) - length(regexp_replace(t::VARCHAR, p::VARCHAR, '', 'g'))) * length(r::VARCHAR)`,
	`CREATE OR REPLACE MACRO fado_regexreplace(t, p, r, m) AS regexp_replace(t::VARCHAR, p::VARCHAR, CASE WHEN fado_regexreplace_len(t, p, r) > m THEN '' ELSE replace(r::VARCHAR, '\', '\\') END, 'g')`,
	`CREATE OR REPLACE MACRO fado_regexreplace_err(t, p, r, m) AS CASE WHEN fado_regexreplace_len(t, p, r) > m THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_findlast(s, t) AS CASE WHEN strpos(reverse(t::VARCHAR), reverse(s::VARCHAR)) = 0 THEN 0.0::DOUBLE ELSE CAST(length(t::VARCHAR) - strpos(reverse(t::VARCHAR), reverse(s::VARCHAR)) - length(s::VARCHAR) + 2 AS DOUBLE) END`,
	`CREATE OR REPLACE MACRO fado_value(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN regexp_full_match(trim(t::VARCHAR), '[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?') THEN TRY_CAST(trim(t::VARCHAR) AS DOUBLE) END`,
	`CREATE OR REPLACE MACRO fado_value_err(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN NOT regexp_full_match(trim(t::VARCHAR), '[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?') THEN 'bad_text' WHEN NOT COALESCE(isfinite(TRY_CAST(trim(t::VARCHAR) AS DOUBLE)), FALSE) THEN 'not_a_number' END`,
	// JSON: the dotted path becomes a JSON pointer; numbers render as TEXT does.
	`CREATE OR REPLACE MACRO fado_jsonpath(p) AS CASE WHEN p::VARCHAR = '' THEN '$' ELSE '/' || replace(p::VARCHAR, '.', '/') END`,
	`CREATE OR REPLACE MACRO fado_jsonvalue(j, p) AS CASE WHEN j IS NULL OR NOT json_valid(j::VARCHAR) THEN NULL ELSE CASE json_type(j::VARCHAR, fado_jsonpath(p)) WHEN 'VARCHAR' THEN json_extract_string(j::VARCHAR, fado_jsonpath(p)) WHEN 'BOOLEAN' THEN upper(json_extract_string(j::VARCHAR, fado_jsonpath(p))) WHEN 'DOUBLE' THEN printf('%.15g', CAST(json_extract(j::VARCHAR, fado_jsonpath(p)) AS DOUBLE) + 0.0::DOUBLE) WHEN 'UBIGINT' THEN printf('%.15g', CAST(json_extract(j::VARCHAR, fado_jsonpath(p)) AS DOUBLE) + 0.0::DOUBLE) WHEN 'BIGINT' THEN printf('%.15g', CAST(json_extract(j::VARCHAR, fado_jsonpath(p)) AS DOUBLE) + 0.0::DOUBLE) ELSE NULL END END`,
	`CREATE OR REPLACE MACRO fado_jsonvalue_err(j, p) AS CASE WHEN j IS NULL THEN NULL WHEN NOT json_valid(j::VARCHAR) THEN 'bad_text' WHEN json_type(j::VARCHAR, fado_jsonpath(p)) IN ('OBJECT', 'ARRAY') THEN 'bad_text' END`,
}
