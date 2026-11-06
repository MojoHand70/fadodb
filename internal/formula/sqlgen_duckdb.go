package formula

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// The DuckDB dialect (ADR-0017, ADR-0020). Numbers are DOUBLE throughout:
// DuckDB reads a bare literal such as 2.5 as DECIMAL and 1706659200 as a
// 32-bit integer, so every literal is cast. Dates are DOUBLE seconds turned
// into naive TIMESTAMPs through make_timestamp, never to_timestamp, whose
// result would follow the session time zone. Booleans are BOOLEAN; blank is
// NULL.
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
	return DuckDB{coercions: coercions{zeroNumber: "0.0::DOUBLE", zeroBool: "FALSE", zeroText: "''",
		castNumber: func(x string) string { return "CAST(" + x + " AS DOUBLE)" }}, lim: DefaultLimits()}
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

// TextLit implements Dialect.
func (DuckDB) TextLit(s string) string { return textLiteral(s) }

// True implements Dialect.
func (DuckDB) True() string { return "TRUE" }

// False implements Dialect.
func (DuckDB) False() string { return "FALSE" }

// Load implements Dialect.
func (DuckDB) Load(column string, k Kind) string {
	switch k {
	case KindNumber, KindDate:
		return "CAST(" + column + " AS DOUBLE)"
	case KindBool:
		return "CAST(" + column + " AS BOOLEAN)"
	}
	return column
}

// NotFinite implements Dialect: DuckDB keeps NaN and the infinities.
func (DuckDB) NotFinite(val string) string { return "NOT isfinite(" + val + ")" }

// Pow implements Dialect: DuckDB's pow is the C library's, which the VM's
// double-double power matches.
func (DuckDB) Pow(x, y string) string { return "pow(" + x + ", " + y + ")" }

// Param implements Dialect.
func (DuckDB) Param(name string) string { return "$" + name }

// ContextArgs implements Dialect: groups bind as a list of text.
func (DuckDB) ContextArgs(ctx *Context) []any {
	now, user, groups := contextValues(ctx)
	var g any
	if groups != nil {
		g = groups
	}
	return []any{sql.Named("now", now), sql.Named("user", user), sql.Named("groups", g)}
}

// Setup implements Dialect: the macro definitions.
func (DuckDB) Setup() []string { return duckdbMacros }

// Render implements Dialect: the TEXT rule of ADR-0019.
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
	case KindDate:
		return "(CASE WHEN " + e.Val + " IS NULL THEN '' WHEN " + e.Val + " % 86400 = 0 THEN strftime(fado_ts(" + e.Val +
			"), '%Y-%m-%d') ELSE strftime(fado_ts(" + e.Val + "), '%Y-%m-%d %H:%M:%S') END)"
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
	count := func(e SQLExpr) string {
		if e.Kind == KindNull {
			return "0"
		}
		return "CAST(" + e.Val + " IS NOT NULL AS INTEGER)"
	}
	// intArg truncates a position or count argument to an integer, clamped
	// to -1 below and to one past maxCount above, so that a negative
	// argument is still seen and the cast cannot overflow.
	intArg := func(x string) string {
		return "CAST(LEAST(GREATEST(trunc(" + x + "), -1), " + maxCount + " + 1) AS BIGINT)"
	}
	year := func(v string) string { return "CAST(year(fado_ts(" + v + ")) AS DOUBLE)" }
	month := func(v string) string { return "CAST(month(fado_ts(" + v + ")) AS DOUBLE)" }
	switch name {
	case "ROUND", "FLOOR", "CEILING", "TRUNC":
		e := macro(KindNumber, false, d.Num(args[0]), numOr(1, zero))
		e.Sure = true
		return e, nil
	case "ABS":
		return SQLExpr{Val: "abs(" + d.Num(args[0]) + ")", Kind: KindNumber, Sure: true}, nil
	case "SIGN":
		x := d.Num(args[0])
		return SQLExpr{Val: "(CASE WHEN " + x + " > 0 THEN " + d.NumberLit(1) + " WHEN " + x + " < 0 THEN " + d.NumberLit(-1) + " ELSE " + zero + " END)", Kind: KindNumber, Sure: true}, nil
	case "MIN", "MAX":
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
		a, b := d.Num(args[0]), d.Num(args[1])
		val := "(" + a + " - " + b + " * floor(" + a + " / " + b + "))"
		return SQLExpr{Val: val, Err: coalesceErr("CASE WHEN "+b+" = "+zero+" THEN 'div_by_zero' END", notFiniteErr(d, val)), Kind: KindNumber}, nil
	case "DATE":
		return macro(KindDate, true, d.Num(args[0]), d.Num(args[1]), d.Num(args[2])), nil
	case "DATEVALUE":
		return macro(KindDate, true, d.Text(args[0])), nil
	case "YEAR", "MONTH", "DAY", "QUARTER":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "CAST(" + strings.ToLower(name) + "(fado_ts(" + args[0].Val + ")) AS DOUBLE)", Kind: KindNumber}, nil
	case "DAYS", "DAYSBETWEEN":
		a, b := args[0], args[1]
		if name == "DAYSBETWEEN" {
			a, b = b, a
		}
		if a.Kind == KindNull || b.Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		day := d.NumberLit(secondsPerDay)
		return SQLExpr{Val: "(floor(" + a.Val + " / " + day + ") - floor(" + b.Val + " / " + day + "))", Kind: KindNumber}, nil
	case "MONTHSBETWEEN", "YEARSBETWEEN":
		if args[0].Kind == KindNull || args[1].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindNumber}, nil
		}
		s, e := args[0].Val, args[1].Val
		if name == "YEARSBETWEEN" {
			return SQLExpr{Val: "(" + year(e) + " - " + year(s) + ")", Kind: KindNumber}, nil
		}
		return SQLExpr{Val: "((" + year(e) + " * 12 + " + month(e) + ") - (" + year(s) + " * 12 + " + month(s) + "))", Kind: KindNumber}, nil
	case "ADDDAYS":
		if args[0].Kind == KindNull {
			return SQLExpr{Val: "NULL", Kind: KindDate}, nil
		}
		val := "floor(" + args[0].Val + " + " + d.Num(args[1]) + " * " + d.NumberLit(secondsPerDay) + ")"
		return SQLExpr{Val: val, Err: "CASE WHEN " + val + " < " + strconv.Itoa(minDateSecs) + " OR " + val + " > " + strconv.Itoa(maxDateSecs) + " THEN 'out_of_range' END", Kind: KindDate}, nil
	case "ADDMONTHS", "ADDYEARS":
		return macro(KindDate, true, args[0].Val, d.Num(args[1])), nil
	case "NOW":
		return SQLExpr{Val: "CAST(" + params.Use("now") + " AS DOUBLE)", Kind: KindDate, Sure: true}, nil
	case "TODAY":
		day := d.NumberLit(secondsPerDay)
		return SQLExpr{Val: "(floor(CAST(" + params.Use("now") + " AS DOUBLE) / " + day + ") * " + day + ")", Kind: KindDate, Sure: true}, nil
	case "USER":
		return SQLExpr{Val: "CAST(" + params.Use("user") + " AS VARCHAR)", Kind: KindText}, nil
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

const (
	duckdbMinDate = "-62135596800"
	duckdbMaxDate = "253402300799"
)

// duckdbMacros are the engine functions as inlined SQL. A macro body is an
// expression, never a subquery: DuckDB cannot run a subquery macro over
// more than one row.
var duckdbMacros = []string{
	// Rounding, as the VM's roundTo and scaledStep, operation for operation.
	// The power of ten comes from its decimal spelling beyond 22, where it is
	// no longer exactly representable: DuckDB's text-to-double conversion is
	// correctly rounded, as is the VM's table, while pow() may be off by an
	// ulp. A scaled value of 2^52 or more is an integer already and the value
	// is returned as it is.
	`CREATE OR REPLACE MACRO fado_pow10(d) AS CASE WHEN abs(trunc(d::DOUBLE)) <= 22 THEN pow(10::DOUBLE, trunc(d::DOUBLE)) ELSE CAST('1e' || CAST(CAST(LEAST(GREATEST(trunc(d::DOUBLE), -323), 308) AS INTEGER) AS VARCHAR) AS DOUBLE) END`,
	`CREATE OR REPLACE MACRO fado_scale(d) AS fado_pow10(LEAST(GREATEST(trunc(d::DOUBLE), -300), 300))`,
	`CREATE OR REPLACE MACRO fado_round(x, d) AS CASE WHEN trunc(d::DOUBLE) > 300 THEN x::DOUBLE WHEN trunc(d::DOUBLE) < -300 THEN 0.0::DOUBLE WHEN abs(x::DOUBLE * fado_pow10(d)) >= 4503599627370496 THEN x::DOUBLE ELSE round(x::DOUBLE * fado_pow10(d)) / fado_pow10(d) END`,
	`CREATE OR REPLACE MACRO fado_floor(x, d) AS CASE WHEN abs(x::DOUBLE * fado_scale(d)) >= 4503599627370496 THEN x::DOUBLE ELSE floor(fado_round(x::DOUBLE * fado_scale(d), 9)) / fado_scale(d) END`,
	`CREATE OR REPLACE MACRO fado_ceiling(x, d) AS CASE WHEN abs(x::DOUBLE * fado_scale(d)) >= 4503599627370496 THEN x::DOUBLE ELSE ceil(fado_round(x::DOUBLE * fado_scale(d), 9)) / fado_scale(d) END`,
	`CREATE OR REPLACE MACRO fado_trunc(x, d) AS CASE WHEN abs(x::DOUBLE * fado_scale(d)) >= 4503599627370496 THEN x::DOUBLE ELSE trunc(fado_round(x::DOUBLE * fado_scale(d), 9)) / fado_scale(d) END`,
	// Dates: seconds to a naive timestamp, clamped so that garbage never
	// raises. The NULL test comes first because LEAST and GREATEST ignore
	// NULLs, which would turn a blank into the first day of year 1.
	`CREATE OR REPLACE MACRO fado_ts(x) AS CASE WHEN x IS NULL THEN NULL ELSE make_timestamp(CAST(LEAST(GREATEST(x::DOUBLE, ` + duckdbMinDate + `), ` + duckdbMaxDate + `) AS BIGINT) * 1000000) END`,
	`CREATE OR REPLACE MACRO fado_date(y, m, d) AS epoch(make_timestamp(CAST(LEAST(GREATEST(trunc(y::DOUBLE), -50000), 50000) AS BIGINT), 1, 1, 0, 0, 0) + to_months(CAST(LEAST(GREATEST(trunc(m::DOUBLE), -1000000), 1000000) AS INTEGER) - 1) + to_days(CAST(LEAST(GREATEST(trunc(d::DOUBLE), -30000000), 30000000) AS INTEGER) - 1))`,
	`CREATE OR REPLACE MACRO fado_date_err(y, m, d) AS CASE WHEN abs(y::DOUBLE) > 1e6 OR abs(m::DOUBLE) > 1e7 OR abs(d::DOUBLE) > 1e9 OR fado_date(y, m, d) < ` + duckdbMinDate + ` OR fado_date(y, m, d) > ` + duckdbMaxDate + ` THEN 'out_of_range' END`,
	`CREATE OR REPLACE MACRO fado_datevalue(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN NOT regexp_full_match(trim(t::VARCHAR), '[0-9]{4}-[0-9]{2}-[0-9]{2}( [0-9]{2}:[0-9]{2}:[0-9]{2})?') THEN NULL ELSE COALESCE(epoch(try_strptime(trim(t::VARCHAR), '%Y-%m-%d')), epoch(try_strptime(trim(t::VARCHAR), '%Y-%m-%d %H:%M:%S'))) END`,
	`CREATE OR REPLACE MACRO fado_datevalue_err(t) AS CASE WHEN trim(t::VARCHAR) = '' THEN NULL WHEN fado_datevalue(t) IS NULL THEN 'bad_text' WHEN fado_datevalue(t) < ` + duckdbMinDate + ` OR fado_datevalue(t) > ` + duckdbMaxDate + ` THEN 'out_of_range' END`,
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
