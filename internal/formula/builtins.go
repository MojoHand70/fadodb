package formula

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// builtin is one function the VM can call. The table order is part of
// bytecode v1: OpCall carries the index, so entries are appended, never
// reordered or removed.
type builtin struct {
	name  string
	class Class
	// args gives the kind accepted at each position; the last one repeats
	// for variadic functions. Arity itself is the catalog's.
	args   []argKind
	result Kind
	// check runs extra definition-time checks on the call, if any.
	check func(*Call) error
	fn    func(vm *VM, args []Value) (Value, *EvalError)
}

var builtins = []*builtin{
	// Logic
	{name: "ISBLANK", class: ClassPure, args: []argKind{akScalar}, result: KindBool, fn: fnIsBlank},
	{name: "ISNOTBLANK", class: ClassPure, args: []argKind{akScalar}, result: KindBool, fn: fnIsNotBlank},
	// Numeric
	{name: "ROUND", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: fnRound},
	{name: "FLOOR", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: stepFn(math.Floor)},
	{name: "CEILING", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: stepFn(math.Ceil)},
	{name: "TRUNC", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: stepFn(math.Trunc)},
	{name: "ABS", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnAbs},
	{name: "SIGN", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnSign},
	{name: "MIN", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnMin},
	{name: "MAX", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnMax},
	{name: "SUM", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnSum},
	{name: "AVERAGE", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnAverage},
	{name: "COUNT", class: ClassPure, args: []argKind{akScalar}, result: KindNumber, fn: fnCount},
	{name: "POWER", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: fnPower},
	{name: "SQRT", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnSqrt},
	{name: "EXP", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnExp},
	{name: "LN", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnLn},
	{name: "LOG", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: fnLog},
	{name: "MOD", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: fnMod},
	// Dates
	{name: "DATE", class: ClassPure, args: []argKind{akNumber, akNumber, akNumber}, result: KindDate, fn: fnDate},
	{name: "DATEVALUE", class: ClassPure, args: []argKind{akText}, result: KindDate, fn: fnDateValue},
	{name: "YEAR", class: ClassPure, args: []argKind{akDate}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64(t.Year()) })},
	{name: "MONTH", class: ClassPure, args: []argKind{akDate}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64(t.Month()) })},
	{name: "DAY", class: ClassPure, args: []argKind{akDate}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64(t.Day()) })},
	{name: "QUARTER", class: ClassPure, args: []argKind{akDate}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64((int(t.Month())-1)/3 + 1) })},
	{name: "DAYS", class: ClassPure, args: []argKind{akDate, akDate}, result: KindNumber, fn: fnDays},
	{name: "DAYSBETWEEN", class: ClassPure, args: []argKind{akDate, akDate}, result: KindNumber, fn: fnDaysBetween},
	{name: "MONTHSBETWEEN", class: ClassPure, args: []argKind{akDate, akDate}, result: KindNumber, fn: fnMonthsBetween},
	{name: "YEARSBETWEEN", class: ClassPure, args: []argKind{akDate, akDate}, result: KindNumber, fn: fnYearsBetween},
	{name: "ADDDAYS", class: ClassPure, args: []argKind{akDate, akNumber}, result: KindDate, fn: fnAddDays},
	{name: "ADDMONTHS", class: ClassPure, args: []argKind{akDate, akNumber}, result: KindDate, fn: fnAddMonths},
	{name: "ADDYEARS", class: ClassPure, args: []argKind{akDate, akNumber}, result: KindDate, fn: fnAddYears},
	// Context
	{name: "NOW", class: ClassContext, args: []argKind{}, result: KindDate, fn: fnNow},
	{name: "TODAY", class: ClassContext, args: []argKind{}, result: KindDate, fn: fnToday},
	{name: "USER", class: ClassContext, args: []argKind{}, result: KindText, fn: fnUser},
	{name: "INGROUP", class: ClassContext, args: []argKind{akText}, result: KindBool, fn: fnInGroup},
	// Text
	{name: "LEFT", class: ClassPure, args: []argKind{akText, akNumber}, result: KindText, fn: fnLeft},
	{name: "RIGHT", class: ClassPure, args: []argKind{akText, akNumber}, result: KindText, fn: fnRight},
	{name: "MID", class: ClassPure, args: []argKind{akText, akNumber, akNumber}, result: KindText, fn: fnMid},
	{name: "LEN", class: ClassPure, args: []argKind{akText}, result: KindNumber, fn: fnLen},
	{name: "TRIM", class: ClassPure, args: []argKind{akText}, result: KindText, fn: fnTrim},
	{name: "UPPER", class: ClassPure, args: []argKind{akText}, result: KindText, fn: textFn(strings.ToUpper)},
	{name: "LOWER", class: ClassPure, args: []argKind{akText}, result: KindText, fn: textFn(strings.ToLower)},
	{name: "PROPER", class: ClassPure, args: []argKind{akText}, result: KindText, fn: textFn(proper)},
	{name: "REPLACE", class: ClassPure, args: []argKind{akText, akNumber, akNumber, akText}, result: KindText, fn: fnReplace},
	{name: "SUBSTITUTE", class: ClassPure, args: []argKind{akText, akText, akText}, result: KindText, fn: fnSubstitute},
	{name: "CONCAT", class: ClassPure, args: []argKind{akScalar}, result: KindText, fn: fnConcat},
	{name: "REPEAT", class: ClassPure, args: []argKind{akText, akNumber}, result: KindText, fn: fnRepeat},
	{name: "FINDFIRST", class: ClassPure, args: []argKind{akText, akText}, result: KindNumber, fn: fnFindFirst},
	{name: "FINDLAST", class: ClassPure, args: []argKind{akText, akText}, result: KindNumber, fn: fnFindLast},
	{name: "PAD", class: ClassPure, args: []argKind{akText, akNumber, akText}, result: KindText, fn: fnPad},
	{name: "REGEXREPLACE", class: ClassPure, args: []argKind{akText, akText, akText}, result: KindText, check: checkRegexLiteral, fn: fnRegexReplace},
	{name: "TEXT", class: ClassPure, args: []argKind{akScalar}, result: KindText, fn: fnText},
	{name: "VALUE", class: ClassPure, args: []argKind{akText}, result: KindNumber, fn: fnValue},
	{name: "JSONVALUE", class: ClassPure, args: []argKind{akJSON, akText}, result: KindText, fn: fnJSONValue},
}

// builtinByName indexes the table by canonical name.
var builtinByName = func() map[string]int {
	m := make(map[string]int, len(builtins))
	for i, b := range builtins {
		m[b.name] = i
	}
	return m
}()

// notYetImplemented lists the catalog functions the binder refuses for now:
// the composite functions and the query functions of ADR-0020.
var notYetImplemented = map[string]bool{
	"INDEX": true, "FILTER": true, "FIRST": true, "LAST": true, "CONTAINS": true, "JOIN": true,
	"LOOKUP": true, "QUERY": true,
}

func notANumber() *EvalError {
	return &EvalError{Code: ErrNotANumber, Msg: "the result is not a finite number"}
}

func finite(f float64) (Value, *EvalError) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return Value{}, notANumber()
	}
	return Number(f), nil
}

// ---- Logic

func fnIsBlank(_ *VM, args []Value) (Value, *EvalError) { return Bool(args[0].kind == KindNull), nil }
func fnIsNotBlank(_ *VM, args []Value) (Value, *EvalError) {
	return Bool(args[0].kind != KindNull), nil
}

// ---- Numeric. A blank argument reads as 0: Value.num is 0 for a blank.

// roundTo rounds half away from zero at a decimal scale. The clamps keep
// 10^digits a finite float64; beyond that scale rounding is a no-op. Once
// the scaled value is 2^52 or more it is an integer already, so the value
// is returned as it is rather than scaled back, which could move it by an
// ulp. The SQL dialects emit the same shape so that every target agrees.
func roundTo(v, digits float64) float64 {
	d := math.Trunc(digits)
	if d > 300 {
		return v
	}
	if d < -300 {
		return 0
	}
	p := pow10(int(d))
	scaled := v * p
	if math.Abs(scaled) >= 1<<52 {
		return v
	}
	return math.Round(scaled) / p
}

// scaledStep applies FLOOR, CEILING or TRUNC at a decimal scale. The scaled
// value is rounded at nine places first: 0.29 * 100 is 28.999999999999996
// in binary, and without the guard FLOOR(0.29, 2) would be 0.28. The SQL
// dialects emit the same shape so that every target agrees.
func scaledStep(v, digits float64, step func(float64) float64) float64 {
	d := math.Max(-300, math.Min(300, math.Trunc(digits)))
	p := pow10(int(d))
	scaled := v * p
	if math.Abs(scaled) >= 1<<52 {
		return v
	}
	return step(roundTo(scaled, 9)) / p
}

func digitsArg(args []Value) float64 {
	if len(args) > 1 {
		return args[1].num
	}
	return 0
}

func fnRound(_ *VM, args []Value) (Value, *EvalError) {
	return finite(roundTo(args[0].num, digitsArg(args)))
}

func stepFn(step func(float64) float64) func(*VM, []Value) (Value, *EvalError) {
	return func(_ *VM, args []Value) (Value, *EvalError) {
		return finite(scaledStep(args[0].num, digitsArg(args), step))
	}
}

func fnAbs(_ *VM, args []Value) (Value, *EvalError) { return Number(math.Abs(args[0].num)), nil }

func fnSign(_ *VM, args []Value) (Value, *EvalError) {
	switch {
	case args[0].num > 0:
		return Number(1), nil
	case args[0].num < 0:
		return Number(-1), nil
	}
	return Number(0), nil
}

func fnMin(_ *VM, args []Value) (Value, *EvalError) {
	m, any := 0.0, false
	for _, a := range args {
		if a.kind == KindNull {
			continue
		}
		if !any || a.num < m {
			m = a.num
		}
		any = true
	}
	if !any {
		return Blank(), nil
	}
	return Number(m), nil
}

func fnMax(_ *VM, args []Value) (Value, *EvalError) {
	m, any := 0.0, false
	for _, a := range args {
		if a.kind == KindNull {
			continue
		}
		if !any || a.num > m {
			m = a.num
		}
		any = true
	}
	if !any {
		return Blank(), nil
	}
	return Number(m), nil
}

// neumaier sums with compensation, as SQLite does, so that the VM and the
// SQL targets agree as far as a sum of floats can (ADR-0019).
func neumaier(args []Value) (sum float64, n int) {
	var c float64
	for _, a := range args {
		if a.kind == KindNull {
			continue
		}
		n++
		t := sum + a.num
		if math.Abs(sum) >= math.Abs(a.num) {
			c += (sum - t) + a.num
		} else {
			c += (a.num - t) + sum
		}
		sum = t
	}
	return sum + c, n
}

func fnSum(_ *VM, args []Value) (Value, *EvalError) {
	s, _ := neumaier(args)
	return finite(s)
}

func fnAverage(_ *VM, args []Value) (Value, *EvalError) {
	s, n := neumaier(args)
	if n == 0 {
		return Blank(), nil
	}
	return finite(s / float64(n))
}

func fnCount(_ *VM, args []Value) (Value, *EvalError) {
	n := 0
	for _, a := range args {
		if a.kind != KindNull {
			n++
		}
	}
	return Number(float64(n)), nil
}

func fnPower(_ *VM, args []Value) (Value, *EvalError) {
	return finite(pow(args[0].num, args[1].num))
}

// pow is x to the y. Go's math.Pow raises x to the integer part of y by
// repeated squaring in plain doubles and loses about a bit per squaring:
// 1.005 ^ 1064 comes out hundreds of ulps from the correctly rounded
// result the C math libraries behind SQLite and DuckDB give. The integer
// part is raised here in double-double arithmetic instead, so that the
// targets agree to the last bit in all but rare cases; the fractional part
// goes through Exp and Log, or Sqrt for a half, which is within a few ulps
// of the C libraries and inside the tolerance of ADR-0019. The
// special cases, zero, infinities, NaN and a negative base with a
// fractional exponent, are math.Pow's.
func pow(x, y float64) float64 {
	if y == 0 || x == 1 {
		return 1
	}
	yi, yf := math.Modf(y)
	if x <= 0 || math.IsInf(x, 0) || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(y, 0) || math.Abs(yi) > 1<<62 {
		return math.Pow(x, y)
	}
	frac := 1.0
	switch yf {
	case 0:
	case 0.5:
		frac = math.Sqrt(x)
	default:
		frac = math.Exp(yf * ln(x))
	}
	n := int64(yi)
	neg := n < 0
	if neg {
		n = -n
	}
	hi, lo := 1.0, 0.0
	bh, bl := x, 0.0
	for n > 0 {
		if n&1 == 1 {
			hi, lo = ddMul(hi, lo, bh, bl)
		}
		n >>= 1
		if n > 0 {
			bh, bl = ddMul(bh, bl, bh, bl)
		}
		if math.IsInf(hi, 0) || math.IsInf(bh, 0) || hi == 0 {
			// Overflow or underflow on the way: the result is infinite,
			// zero or subnormal, and math.Pow's scaling gets those right.
			return math.Pow(x, y)
		}
	}
	r := hi + lo
	if neg {
		r = 1 / r
	}
	return r * frac
}

// ddMul multiplies two double-double numbers; the error of each product
// is recovered with a fused multiply-add. The explicit conversions round
// the other products before they join the sum: on arm64 the compiler
// would otherwise fuse them into the addition, skip a rounding, and give
// a result that differs from amd64's in the last bits.
func ddMul(ah, al, bh, bl float64) (float64, float64) {
	p := ah * bh
	if math.IsInf(p, 0) {
		return p, 0 // the error term would be NaN; pow stops on the infinity
	}
	e := math.FMA(ah, bh, -p) + float64(ah*bl) + float64(al*bh)
	hi := p + e
	return hi, e - (hi - p)
}

// minNormal is the smallest normal float64; below it a number is subnormal.
const minNormal = 0x1p-1022

// ln is the natural logarithm, right for a subnormal argument too. Go's
// math.Log flattens every subnormal to the smallest normal number, so that
// LN(1e-310) came out as -708.4 where the C libraries behind SQLite and
// DuckDB give -713.8; a subnormal is scaled up by 2^64 first and the
// scaling taken back out of the result.
func ln(x float64) float64 {
	if x > 0 && x < minNormal {
		return math.Log(x*(1<<64)) - 64*math.Ln2
	}
	return math.Log(x)
}

// log10 is the base-10 logarithm, exact at the powers of ten. Go's
// math.Log10 is Log2 scaled by a constant and gives 14.999999999999998
// for 1e15 where the C libraries give 15; and like math.Log it flattens a
// subnormal, which goes through ln instead.
func log10(x float64) float64 {
	r := math.Log10(x)
	if x < minNormal {
		r = ln(x) / math.Ln10
	}
	if n := math.Round(r); n != r && math.Abs(n) <= 323 && pow10(int(n)) == x {
		return n
	}
	return r
}

// pow10 is 10 to an integer power, correctly rounded, so that every
// evaluator scales by the same double: math.Pow raises 10 by repeated
// squaring and is off by an ulp for most exponents beyond 22, where the
// power is no longer exactly representable, while the DuckDB dialect
// reads the same power from its decimal spelling. Outside the table the
// power is zero or infinite.
func pow10(d int) float64 {
	if d < -323 {
		return 0
	}
	if d > 308 {
		return math.Inf(1)
	}
	return pow10Table[d+323]
}

var pow10Table = func() [632]float64 {
	var t [632]float64
	for i := range t {
		f, err := strconv.ParseFloat("1e"+strconv.Itoa(i-323), 64)
		if err != nil {
			panic(err)
		}
		t[i] = f
	}
	return t
}()

func fnSqrt(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].num < 0 {
		return Value{}, notANumber()
	}
	return Number(math.Sqrt(args[0].num)), nil
}

func fnExp(_ *VM, args []Value) (Value, *EvalError) { return finite(math.Exp(args[0].num)) }

func fnLn(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].num <= 0 {
		return Value{}, notANumber()
	}
	return Number(ln(args[0].num)), nil
}

func fnLog(_ *VM, args []Value) (Value, *EvalError) {
	x, base := args[0].num, 10.0
	if len(args) > 1 {
		base = args[1].num
	}
	if x <= 0 || base <= 0 || base == 1 {
		return Value{}, notANumber()
	}
	if base == 10 {
		return finite(log10(x))
	}
	return finite(ln(x) / ln(base))
}

func fnMod(_ *VM, args []Value) (Value, *EvalError) {
	a, b := args[0].num, args[1].num
	if b == 0 {
		return Value{}, &EvalError{Code: ErrDivByZero, Msg: "MOD by zero"}
	}
	// The product rounds before the subtraction, as the SQL engines round
	// it, so that arm64 cannot fuse the two into one differently rounded step.
	return finite(a - float64(b*math.Floor(a/b)))
}

// ---- Dates. Seconds since the epoch, UTC; a blank date argument gives a
// blank result.

func outOfRange(what string) *EvalError {
	return &EvalError{Code: ErrOutOfRange, Msg: what}
}

func dateValue(secs float64) (Value, *EvalError) {
	secs = math.Floor(secs)
	if !dateInRange(secs) {
		return Value{}, outOfRange("the date is outside year 1 to year 9999")
	}
	return Date(int64(secs)), nil
}

func epochTime(v Value) time.Time { return time.Unix(int64(v.num), 0).UTC() }

func fnDate(_ *VM, args []Value) (Value, *EvalError) {
	y, m, d := math.Trunc(args[0].num), math.Trunc(args[1].num), math.Trunc(args[2].num)
	if math.Abs(y) > 1e6 || math.Abs(m) > 1e7 || math.Abs(d) > 1e9 {
		return Value{}, outOfRange("the date is outside year 1 to year 9999")
	}
	// Excel carry: January 1 of the year, plus months, plus days.
	t := time.Date(int(y), time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, int(m)-1, 0).AddDate(0, 0, int(d)-1)
	return dateValue(float64(t.Unix()))
}

const (
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02 15:04:05"
)

func fnDateValue(_ *VM, args []Value) (Value, *EvalError) {
	s := strings.TrimSpace(args[0].str)
	if s == "" {
		return Blank(), nil
	}
	layout := dateLayout
	if len(s) > len(dateLayout) {
		layout = dateTimeLayout
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return Value{}, &EvalError{Code: ErrBadText, Msg: "not a date: " + s}
	}
	return dateValue(float64(t.Unix()))
}

func datePart(part func(time.Time) float64) func(*VM, []Value) (Value, *EvalError) {
	return func(_ *VM, args []Value) (Value, *EvalError) {
		if args[0].kind == KindNull {
			return Blank(), nil
		}
		return Number(part(epochTime(args[0]))), nil
	}
}

func dayNumber(v Value) float64 { return math.Floor(v.num / secondsPerDay) }

func fnDays(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull || args[1].kind == KindNull {
		return Blank(), nil
	}
	return Number(dayNumber(args[0]) - dayNumber(args[1])), nil
}

func fnDaysBetween(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull || args[1].kind == KindNull {
		return Blank(), nil
	}
	return Number(dayNumber(args[1]) - dayNumber(args[0])), nil
}

func fnMonthsBetween(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull || args[1].kind == KindNull {
		return Blank(), nil
	}
	s, e := epochTime(args[0]), epochTime(args[1])
	return Number(float64((e.Year()*12 + int(e.Month())) - (s.Year()*12 + int(s.Month())))), nil
}

func fnYearsBetween(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull || args[1].kind == KindNull {
		return Blank(), nil
	}
	return Number(float64(epochTime(args[1]).Year() - epochTime(args[0]).Year())), nil
}

func fnAddDays(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull {
		return Blank(), nil
	}
	return dateValue(args[0].num + float64(args[1].num*secondsPerDay))
}

// addMonthsClamped keeps the day of month and the time of day and clamps to
// the last day of a shorter month.
func addMonthsClamped(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	d := t.Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

func addMonths(v Value, months float64) (Value, *EvalError) {
	if v.kind == KindNull {
		return Blank(), nil
	}
	n := math.Trunc(months)
	if math.Abs(n) > 1e7 {
		return Value{}, outOfRange("the date is outside year 1 to year 9999")
	}
	return dateValue(float64(addMonthsClamped(epochTime(v), int(n)).Unix()))
}

func fnAddMonths(_ *VM, args []Value) (Value, *EvalError) { return addMonths(args[0], args[1].num) }

func fnAddYears(_ *VM, args []Value) (Value, *EvalError) {
	return addMonths(args[0], 12*math.Trunc(args[1].num))
}

// ---- Context

func fnNow(vm *VM, _ []Value) (Value, *EvalError) { return Date(vm.now()), nil }

func fnToday(vm *VM, _ []Value) (Value, *EvalError) {
	now := float64(vm.now())
	return Date(int64(math.Floor(now/secondsPerDay) * secondsPerDay)), nil
}

func fnUser(vm *VM, _ []Value) (Value, *EvalError) {
	if vm.ctx == nil || vm.ctx.User == "" {
		return Blank(), nil
	}
	return Text(vm.ctx.User), nil
}

func fnInGroup(vm *VM, args []Value) (Value, *EvalError) {
	if vm.ctx != nil {
		for _, g := range vm.ctx.Groups {
			if g == args[0].str {
				return Bool(true), nil
			}
		}
	}
	return Bool(false), nil
}
