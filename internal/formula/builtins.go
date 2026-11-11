package formula

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// builtin is one function the VM can call. The table order is part of the
// bytecode: OpCall carries the index, so entries are appended, never
// reordered or removed.
type builtin struct {
	name  string
	class Class
	// args gives the kind accepted at each position; the last one repeats
	// for variadic functions. Arity itself is the catalog's.
	args []argKind
	// result is the static result kind, unless typ is set.
	result Kind
	// typ names the rule that decides the result type from the argument
	// types for a function whose result depends on them; the binder's
	// resultType applies it and may make literal arguments exact.
	typ typRule
	// check runs extra definition-time checks on the call, if any.
	check func(*Call) error
	fn    func(vm *VM, args []Value) (Value, *EvalError)
	// family: on the exact family the binder passes the static alignment
	// scale as a trailing exact argument.
	family bool
}

var builtins = []*builtin{
	// Logic
	{name: "ISBLANK", class: ClassPure, args: []argKind{akScalar}, result: KindBool, fn: fnIsBlank},
	{name: "ISNOTBLANK", class: ClassPure, args: []argKind{akScalar}, result: KindBool, fn: fnIsNotBlank},
	// Numeric, over either number family
	{name: "ROUND", class: ClassPure, args: []argKind{akNumeric, akNumber, akText}, typ: typRoundMethod, fn: fnRound},
	{name: "FLOOR", class: ClassPure, args: []argKind{akNumeric, akNumber}, typ: typRoundPlain, fn: stepFn(math.Floor, rmFloor)},
	{name: "CEILING", class: ClassPure, args: []argKind{akNumeric, akNumber}, typ: typRoundPlain, fn: stepFn(math.Ceil, rmCeiling)},
	{name: "TRUNC", class: ClassPure, args: []argKind{akNumeric, akNumber}, typ: typRoundPlain, fn: stepFn(math.Trunc, rmDown)},
	{name: "ABS", class: ClassPure, args: []argKind{akNumeric}, typ: typFirst, fn: fnAbs},
	{name: "SIGN", class: ClassPure, args: []argKind{akNumeric}, typ: typSign, fn: fnSign},
	{name: "MIN", class: ClassPure, args: []argKind{akNumeric}, typ: typFamilyRule, fn: fnMin, family: true},
	{name: "MAX", class: ClassPure, args: []argKind{akNumeric}, typ: typFamilyRule, fn: fnMax, family: true},
	{name: "SUM", class: ClassPure, args: []argKind{akNumeric}, typ: typFamilyRule, fn: fnSum, family: true},
	{name: "AVERAGE", class: ClassPure, args: []argKind{akNumeric}, typ: typFamilyRule, fn: fnAverage, family: true},
	{name: "COUNT", class: ClassPure, args: []argKind{akScalar}, result: KindNumber, fn: fnCount},
	{name: "POWER", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: fnPower},
	{name: "SQRT", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnSqrt},
	{name: "EXP", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnExp},
	{name: "LN", class: ClassPure, args: []argKind{akNumber}, result: KindNumber, fn: fnLn},
	{name: "LOG", class: ClassPure, args: []argKind{akNumber, akNumber}, result: KindNumber, fn: fnLog},
	{name: "MOD", class: ClassPure, args: []argKind{akNumeric, akNumeric}, typ: typFamilyRule, fn: fnMod, family: true},
	// Dates: a date is a calendar day, a datetime an instant (ADR-0023)
	{name: "DATE", class: ClassPure, args: []argKind{akNumber, akNumber, akNumber}, result: KindDate, fn: fnDate},
	{name: "DATEVALUE", class: ClassPure, args: []argKind{akText}, result: KindDate, fn: fnDateValue},
	{name: "YEAR", class: ClassPure, args: []argKind{akDateLike}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64(t.Year()) })},
	{name: "MONTH", class: ClassPure, args: []argKind{akDateLike}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64(t.Month()) })},
	{name: "DAY", class: ClassPure, args: []argKind{akDateLike}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64(t.Day()) })},
	{name: "QUARTER", class: ClassPure, args: []argKind{akDateLike}, result: KindNumber, fn: datePart(func(t time.Time) float64 { return float64((int(t.Month())-1)/3 + 1) })},
	{name: "DAYS", class: ClassPure, args: []argKind{akDateLike, akDateLike}, typ: typDatePairRule, fn: fnDays},
	{name: "DAYSBETWEEN", class: ClassPure, args: []argKind{akDateLike, akDateLike}, typ: typDatePairRule, fn: fnDaysBetween},
	{name: "MONTHSBETWEEN", class: ClassPure, args: []argKind{akDateLike, akDateLike}, typ: typDatePairRule, fn: fnMonthsBetween},
	{name: "YEARSBETWEEN", class: ClassPure, args: []argKind{akDateLike, akDateLike}, typ: typDatePairRule, fn: fnYearsBetween},
	{name: "ADDDAYS", class: ClassPure, args: []argKind{akDateLike, akNumber}, typ: typSameDate, fn: fnAddDays},
	{name: "ADDMONTHS", class: ClassPure, args: []argKind{akDateLike, akNumber}, typ: typSameDate, fn: fnAddMonths},
	{name: "ADDYEARS", class: ClassPure, args: []argKind{akDateLike, akNumber}, typ: typSameDate, fn: fnAddYears},
	// Context
	{name: "NOW", class: ClassContext, args: []argKind{}, result: KindDateTime, fn: fnNow},
	{name: "TODAY", class: ClassContext, args: []argKind{}, result: KindDate, fn: fnToday},
	{name: "USER", class: ClassContext, args: []argKind{}, result: KindText, fn: fnUser},
	{name: "INGROUP", class: ClassContext, args: []argKind{akText}, result: KindBool, fn: fnInGroup},
	{name: "STATE", class: ClassContext, args: []argKind{}, result: KindText, fn: fnState},
	{name: "ID", class: ClassContext, args: []argKind{}, result: KindText, fn: fnID},
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
	// Exact numbers and money (ADR-0022)
	{name: "DECIMAL", class: ClassPure, args: []argKind{akNumeric, akNumber, akText}, typ: typDecimal, fn: fnDecimal},
	{name: "NUMBER", class: ClassPure, args: []argKind{akNumeric}, result: KindNumber, fn: fnNumber},
	{name: "MONEY", class: ClassPure, args: []argKind{akExact, akText}, typ: typMoney, fn: fnMoney},
	{name: "MONEYVALUE", class: ClassPure, args: []argKind{akText, akNumber}, typ: typPlacesMoney, fn: fnMoneyValue},
	{name: "DECIMALVALUE", class: ClassPure, args: []argKind{akText, akNumber}, typ: typPlacesDecimal, fn: fnDecimalValue},
	{name: "AMOUNT", class: ClassPure, args: []argKind{akMoney}, typ: typAmount, fn: fnAmount},
	{name: "CURRENCY", class: ClassPure, args: []argKind{akMoney}, result: KindText, fn: fnCurrency},
	// Calendar days and instants (ADR-0023)
	{name: "DATETIMEVALUE", class: ClassPure, args: []argKind{akText}, result: KindDateTime, fn: fnDateTimeValue},
	{name: "DATEOF", class: ClassPure, args: []argKind{akDateTime}, result: KindDate, fn: fnDateOf},
	{name: "DATETIME", class: ClassPure, args: []argKind{akDate}, result: KindDateTime, fn: fnDateTime},
	// Text made safe for a URL (ADR-0045)
	{name: "URLENCODE", class: ClassPure, args: []argKind{akText}, result: KindText, fn: fnURLEncode},
	// The life of the document, from its row (ADR-0047, item 5)
	{name: "CREATEDAT", class: ClassContext, args: []argKind{}, result: KindDateTime, fn: lifeMoment(func(d *Life) int64 { return d.CreatedAt })},
	{name: "CREATEDBY", class: ClassContext, args: []argKind{}, result: KindText, fn: lifeText(func(d *Life) string { return d.CreatedBy })},
	{name: "UPDATEDAT", class: ClassContext, args: []argKind{}, result: KindDateTime, fn: lifeMoment(func(d *Life) int64 { return d.UpdatedAt })},
	{name: "UPDATEDBY", class: ClassContext, args: []argKind{}, result: KindText, fn: lifeText(func(d *Life) string { return d.UpdatedBy })},
	{name: "ENTEREDAT", class: ClassContext, args: []argKind{}, result: KindDateTime, fn: lifeMoment(func(d *Life) int64 { return d.EnteredAt })},
	{name: "ENTEREDBY", class: ClassContext, args: []argKind{}, result: KindText, fn: lifeText(func(d *Life) string { return d.EnteredBy })},
	{name: "ENTEREDFROM", class: ClassContext, args: []argKind{}, result: KindText, fn: lifeText(func(d *Life) string { return d.EnteredFrom })},
	{name: "VERSION", class: ClassContext, args: []argKind{}, result: KindNumber, fn: fnVersion},
}

// builtinByName indexes the table by canonical name.
var builtinByName = func() map[string]int {
	m := make(map[string]int, len(builtins))
	for i, b := range builtins {
		m[b.name] = i
	}
	return m
}()

// specialForms are catalog functions the parser or the binder turns into
// control flow, or for SECRET into the read of a slot, rather than calls.
var specialForms = map[string]bool{"IF": true, "AND": true, "OR": true, "NOT": true, "IFERROR": true, "SECRET": true}

// notYetImplemented lists the catalog functions the binder refuses for now:
// the composite functions and the query functions of ADR-0020.
var notYetImplemented = map[string]bool{
	"INDEX": true, "FILTER": true, "FIRST": true, "LAST": true, "CONTAINS": true, "JOIN": true,
	"LOOKUP": true, "QUERY": true,
}

func notANumber() *EvalError { return errNotANumber }

func finite(f float64) (Value, *EvalError) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return Value{}, notANumber()
	}
	return Number(f), nil
}

// ---- Result typing for the functions whose type depends on their arguments

// typRule names how a function's result type follows from its arguments.
// The binder applies the rule in resultType; a rule is not a function
// value, so that the binder itself never escapes to the heap.
type typRule uint8

const (
	typNone          typRule = iota
	typFirst                 // the first argument's type when exact, else number: ABS
	typSign                  // an int for an exact argument, else number: SIGN
	typRoundMethod           // ROUND: places written in the formula on an exact value, a literal method
	typRoundPlain            // FLOOR, CEILING, TRUNC: places written in the formula on an exact value
	typFamilyRule            // MIN, MAX, SUM, AVERAGE, MOD: one family, exact at the largest scale
	typDecimal               // DECIMAL(value, places[, method])
	typMoney                 // MONEY(amount, currency): money at the amount's scale
	typPlacesMoney           // MONEYVALUE(text, places)
	typPlacesDecimal         // DECIMALVALUE(text, places)
	typAmount                // AMOUNT(money): a decimal at the money's scale
	typSameDate              // ADDDAYS, ADDMONTHS, ADDYEARS: the first argument's kind
	typDatePairRule          // DAYS and the BETWEEN family: two of one kind
)

// resultType applies a function's typing rule to its argument types,
// making literal arguments exact where the rule asks for it.
func (b *binder) resultType(spec *builtin, n *Call, args []Type, lits []litInfo) (Type, error) {
	switch spec.typ {
	case typFirst:
		if args[0].IsExact() {
			return args[0], nil
		}
		return Scalar(KindNumber), nil
	case typSign:
		if args[0].IsExact() {
			return DecimalType(0), nil
		}
		return Scalar(KindNumber), nil
	case typRoundMethod, typRoundPlain:
		if spec.typ == typRoundMethod {
			if err := b.methodArg(n, 2); err != nil {
				return Type{}, err
			}
		}
		if !args[0].IsExact() {
			return Scalar(KindNumber), nil
		}
		p, err := b.placesArg(n, 1, 0)
		if err != nil {
			return Type{}, err
		}
		return Type{Kind: args[0].Kind, Scale: int8(max(p, 0))}, nil
	case typFamilyRule:
		exact := false
		for _, t := range args {
			exact = exact || t.IsExact()
		}
		if !exact {
			return Scalar(KindNumber), nil
		}
		kind, scale := KindDecimal, 0
		for i, t := range args {
			if !t.IsExact() && lits[i].ok {
				var err error
				if t, err = b.exactLit(lits[i], n.Args[i]); err != nil {
					return Type{}, err
				}
				args[i] = t
			}
			if !isExactOperand(t) {
				return Type{}, b.errAt(n.Args[i].Pos(), "%s: argument %d is %s among exact values%s", spec.name, i+1, t, arithmeticHint(t))
			}
			if t.Kind == KindMoney {
				kind = KindMoney
			}
			scale = max(scale, int(t.Scale))
		}
		if spec.name == "AVERAGE" {
			scale = divScale(scale)
		}
		return Type{Kind: kind, Scale: int8(scale)}, nil
	case typDecimal:
		p, err := b.placesArg(n, 1, 0)
		if err != nil {
			return Type{}, err
		}
		if p < 0 {
			return Type{}, b.errAt(n.Args[1].Pos(), "DECIMAL: the places must be 0 to %d", MaxScale)
		}
		if err := b.methodArg(n, 2); err != nil {
			return Type{}, err
		}
		return DecimalType(p), nil
	case typMoney:
		if args[0].Kind == KindMoney {
			return Type{}, b.errAt(n.Args[0].Pos(), "MONEY takes a decimal amount, got %s; AMOUNT reads the amount of a money", args[0])
		}
		return MoneyType(int(args[0].Scale)), nil
	case typPlacesMoney, typPlacesDecimal:
		p, err := b.placesArg(n, 1, 0)
		if err != nil {
			return Type{}, err
		}
		if p < 0 {
			return Type{}, b.errAt(n.Args[1].Pos(), "%s: the places must be 0 to %d", n.Name, MaxScale)
		}
		if spec.typ == typPlacesMoney {
			return MoneyType(p), nil
		}
		return DecimalType(p), nil
	case typAmount:
		return DecimalType(int(args[0].Scale)), nil
	case typSameDate:
		// A date or a datetime like the first argument, and blank for the
		// BLANK literal, which fits either.
		return Scalar(args[0].Kind), nil
	case typDatePairRule:
		a, c := args[0], args[1]
		if a.Kind != KindNull && c.Kind != KindNull && a.Kind != c.Kind {
			return Type{}, b.errAt(n.Args[1].Pos(), "%s needs two dates or two datetimes, got %s and %s; DATEOF turns a datetime into a date", spec.name, a, c)
		}
		return Scalar(KindNumber), nil
	}
	return Scalar(spec.result), nil
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

// placesOf reads the places of a rounding function on an exact value: the
// binder made it an integer literal.
func placesOf(args []Value) int {
	if len(args) > 1 {
		return int(math.Trunc(args[1].num))
	}
	return 0
}

// methodOf reads a rounding method argument; half_up when absent or, from
// a SQL caller, unknown.
func methodOf(args []Value, i int) roundMethod {
	if i < len(args) {
		if rm, ok := roundMethods[args[i].str]; ok {
			return rm
		}
	}
	return rmHalfUp
}

// sameKind makes an exact result of the argument's kind: money keeps its
// currency.
func sameKind(x Value, m int64, s int) Value {
	if x.kind == KindMoney {
		return Money(m, s, x.str)
	}
	return Decimal(m, s)
}

func roundExact(x Value, places int, rm roundMethod) (Value, *EvalError) {
	m, s := exactOperand(x)
	r, rs, ok := roundMant(m, s, places, rm)
	if !ok {
		return Value{}, overflow()
	}
	return sameKind(x, r, rs), nil
}

func fnRound(_ *VM, args []Value) (Value, *EvalError) {
	if !args[0].kind.IsExact() && len(args) < 3 {
		// The float rounding of ADR-0004, as before the methods.
		return finite(roundTo(args[0].num, digitsArg(args)))
	}
	rm := methodOf(args, 2)
	if args[0].kind.IsExact() {
		return roundExact(args[0], placesOf(args), rm)
	}
	return finite(roundFloat(args[0].num, digitsArg(args), rm))
}

func stepFn(step func(float64) float64, rm roundMethod) func(*VM, []Value) (Value, *EvalError) {
	return func(_ *VM, args []Value) (Value, *EvalError) {
		if !args[0].kind.IsExact() {
			return finite(scaledStep(args[0].num, digitsArg(args), step))
		}
		return roundExact(args[0], placesOf(args), rm)
	}
}

func fnAbs(_ *VM, args []Value) (Value, *EvalError) {
	if x := args[0]; x.kind.IsExact() {
		m := x.Mant()
		if m < 0 {
			if m == math.MinInt64 {
				return Value{}, overflow()
			}
			m = -m
		}
		return sameKind(x, m, int(x.scale)), nil
	}
	return Number(math.Abs(args[0].num)), nil
}

func fnSign(_ *VM, args []Value) (Value, *EvalError) {
	if x := args[0]; x.kind.IsExact() {
		return Decimal(sign64(x.Mant()), 0), nil
	}
	switch {
	case args[0].num > 0:
		return Number(1), nil
	case args[0].num < 0:
		return Number(-1), nil
	}
	return Number(0), nil
}

// anyExact reports whether an argument list runs on the exact family.
func anyExact(args []Value) bool {
	for _, a := range args {
		if a.kind.IsExact() {
			return true
		}
	}
	return false
}

// exactArgsOf splits the trailing static scale the binder passes to a
// family function off its arguments.
func exactArgsOf(args []Value) ([]Value, int) {
	last := args[len(args)-1]
	return args[:len(args)-1], int(last.Mant())
}

// exactFold folds the non-blank exact arguments left to right with step,
// every argument first brought to the static scale, as the SQL dialects
// do, so that an overflow happens on the same step on every evaluator; and
// checks that the currencies agree. It returns the fold, the currency, and
// how many arguments took part.
func exactFold(args []Value, scale int, step func(acc, x int64) (int64, bool)) (int64, string, int, *EvalError) {
	var (
		acc int64
		cur string
		n   int
	)
	for _, a := range args {
		if a.kind == KindMoney {
			if cur != "" && a.str != cur {
				return 0, "", 0, currencyMismatch()
			}
			cur = a.str
		}
	}
	for _, a := range args {
		if a.kind == KindNull {
			continue
		}
		m, s := exactOperand(a)
		m, ok := scaleBy(m, scale-s)
		if !ok {
			return 0, "", 0, overflow()
		}
		if n == 0 {
			acc = m
			n++
			continue
		}
		r, ok := step(acc, m)
		if !ok {
			return 0, "", 0, overflow()
		}
		acc = r
		n++
	}
	return acc, cur, n, nil
}

func exactResultOf(m int64, s int, cur string) Value {
	if cur != "" {
		return Money(m, s, cur)
	}
	return Decimal(m, s)
}

func fnMin(_ *VM, args []Value) (Value, *EvalError) {
	if anyExact(args) {
		args, s := exactArgsOf(args)
		m, cur, n, err := exactFold(args, s, func(acc, x int64) (int64, bool) { return min(acc, x), true })
		if err != nil || n == 0 {
			return Blank(), err
		}
		return exactResultOf(m, s, cur), nil
	}
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
	if anyExact(args) {
		args, s := exactArgsOf(args)
		m, cur, n, err := exactFold(args, s, func(acc, x int64) (int64, bool) { return max(acc, x), true })
		if err != nil || n == 0 {
			return Blank(), err
		}
		return exactResultOf(m, s, cur), nil
	}
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

// sum adds the non-blank arguments left to right in plain double
// arithmetic, exactly as the SQL dialects spell it, so that every
// evaluator gets the same bits. A compensated sum would be more accurate,
// but it differed from DuckDB's inline additions by far more than any
// tolerance when large terms cancel.
func sum(args []Value) (s float64, n int) {
	for _, a := range args {
		if a.kind == KindNull {
			continue
		}
		n++
		s += a.num
	}
	return s, n
}

func fnSum(_ *VM, args []Value) (Value, *EvalError) {
	if anyExact(args) {
		args, s := exactArgsOf(args)
		m, cur, _, err := exactFold(args, s, addInt)
		if err != nil {
			return Value{}, err
		}
		return exactResultOf(m, s, cur), nil
	}
	s, _ := sum(args)
	return finite(s)
}

func fnAverage(_ *VM, args []Value) (Value, *EvalError) {
	if anyExact(args) {
		args, s := exactArgsOf(args)
		m, cur, n, err := exactFold(args, s, addInt)
		if err != nil || n == 0 {
			return Blank(), err
		}
		q, qs, err := decDiv(m, s, int64(n), 0)
		if err != nil {
			return Value{}, err
		}
		return exactResultOf(q, qs, cur), nil
	}
	s, n := sum(args)
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
// every product where it stands: on arm64 the compiler would otherwise
// fuse a product into a sum that follows it, even one statements later,
// skip a rounding, and give a result that differs from amd64's in the
// last bits.
func ddMul(ah, al, bh, bl float64) (float64, float64) {
	p := float64(ah * bh)
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
	if anyExact(args) {
		args, s := exactArgsOf(args)
		a, b := args[0], args[1]
		if a.kind == KindMoney && b.kind == KindMoney && a.str != b.str {
			return Value{}, currencyMismatch()
		}
		ma, sa := exactOperand(a)
		mb, sb := exactOperand(b)
		x, ok := scaleBy(ma, s-sa)
		y, ok2 := scaleBy(mb, s-sb)
		if !ok || !ok2 {
			return Value{}, overflow()
		}
		r, err := decMod(x, y)
		if err != nil {
			return Value{}, err
		}
		cur := a.Currency()
		if cur == "" {
			cur = b.Currency()
		}
		return exactResultOf(r, s, cur), nil
	}
	a, b := args[0].num, args[1].num
	if b == 0 {
		return Value{}, errModByZero
	}
	// The product rounds before the subtraction, as the SQL engines round
	// it, so that arm64 cannot fuse the two into one differently rounded step.
	return finite(a - float64(b*math.Floor(a/b)))
}

// ---- Exact numbers and money (ADR-0022)

func fnDecimal(_ *VM, args []Value) (Value, *EvalError) {
	x := args[0]
	places := placesOf(args)
	rm := methodOf(args, 2)
	switch x.kind {
	case KindNull:
		return Blank(), nil
	case KindDecimal, KindMoney:
		m, s, ok := roundMant(x.Mant(), int(x.scale), places, rm)
		if !ok {
			return Value{}, overflow()
		}
		return Decimal(m, s), nil
	}
	m, err := decFromFloat(x.num, places, rm)
	if err != nil {
		return Value{}, err
	}
	return Decimal(m, places), nil
}

func fnNumber(_ *VM, args []Value) (Value, *EvalError) {
	x := args[0]
	switch x.kind {
	case KindNull:
		return Blank(), nil
	case KindDecimal, KindMoney:
		return Number(decToFloat(x.Mant(), int(x.scale))), nil
	}
	return Number(x.num), nil
}

func badCurrency(s string) *EvalError {
	return &EvalError{Code: ErrBadText, Msg: "a currency is three upper-case letters, got " + strconv.Quote(s)}
}

func fnMoney(_ *VM, args []Value) (Value, *EvalError) {
	x := args[0]
	if x.kind == KindNull {
		return Blank(), nil
	}
	cur, ok := parseCurrency(args[1].str)
	if !ok {
		return Value{}, badCurrency(args[1].str)
	}
	m, s := exactOperand(x)
	return Money(m, s, cur), nil
}

func fnMoneyValue(_ *VM, args []Value) (Value, *EvalError) {
	s := strings.TrimSpace(args[0].str)
	if s == "" {
		return Blank(), nil
	}
	m, scale, cur, err := parseMoney(s)
	if err != nil {
		return Value{}, err
	}
	places := placesOf(args)
	m, err = rescaleExact(m, scale, places)
	if err != nil {
		return Value{}, err
	}
	return Money(m, places, cur), nil
}

func fnDecimalValue(_ *VM, args []Value) (Value, *EvalError) {
	s := strings.TrimSpace(args[0].str)
	if s == "" {
		return Blank(), nil
	}
	m, scale, err := parseDecimal(s)
	if err != nil {
		return Value{}, err
	}
	places := placesOf(args)
	m, err = rescaleExact(m, scale, places)
	if err != nil {
		return Value{}, err
	}
	return Decimal(m, places), nil
}

func fnAmount(_ *VM, args []Value) (Value, *EvalError) {
	x := args[0]
	if x.kind == KindNull {
		return Blank(), nil
	}
	return Decimal(x.Mant(), int(x.scale)), nil
}

func fnCurrency(_ *VM, args []Value) (Value, *EvalError) {
	x := args[0]
	if x.kind == KindNull {
		return Blank(), nil
	}
	return Text(x.str), nil
}

// ---- Dates. A date is a day count, a datetime a second count, both from
// the epoch in UTC; a blank argument gives a blank result.

func outOfRange(what string) *EvalError {
	return &EvalError{Code: ErrOutOfRange, Msg: what}
}

func dateOutOfRange() *EvalError { return errDateRange }

// floorDiv divides rounding toward minus infinity, so that a negative
// second count lands on the day it belongs to.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func dateValue(days float64) (Value, *EvalError) {
	days = math.Floor(days)
	if !dateInRange(days) {
		return Value{}, dateOutOfRange()
	}
	return Date(int64(days)), nil
}

func dateTimeValue(secs float64) (Value, *EvalError) {
	secs = math.Floor(secs)
	if !dateTimeInRange(secs) {
		return Value{}, dateOutOfRange()
	}
	return DateTime(int64(secs)), nil
}

// timeOf is the instant of a date or a datetime value.
func timeOf(v Value) time.Time {
	if v.kind == KindDate {
		return time.Unix(int64(v.num)*secondsPerDay, 0).UTC()
	}
	return time.Unix(int64(v.num), 0).UTC()
}

// dateLike makes a value of v's kind from an instant.
func dateLike(v Value, t time.Time) (Value, *EvalError) {
	if v.kind == KindDate {
		return dateValue(float64(floorDiv(t.Unix(), secondsPerDay)))
	}
	return dateTimeValue(float64(t.Unix()))
}

func fnDate(_ *VM, args []Value) (Value, *EvalError) {
	y, m, d := math.Trunc(args[0].num), math.Trunc(args[1].num), math.Trunc(args[2].num)
	if math.Abs(y) > 1e6 || math.Abs(m) > 1e7 || math.Abs(d) > 1e9 {
		return Value{}, dateOutOfRange()
	}
	// Excel carry: January 1 of the year, plus months, plus days.
	t := time.Date(int(y), time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, int(m)-1, 0).AddDate(0, 0, int(d)-1)
	return dateValue(float64(floorDiv(t.Unix(), secondsPerDay)))
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
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Value{}, &EvalError{Code: ErrBadText, Msg: "not a date: " + s}
	}
	return dateValue(float64(floorDiv(t.Unix(), secondsPerDay)))
}

func fnDateTimeValue(_ *VM, args []Value) (Value, *EvalError) {
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
		return Value{}, &EvalError{Code: ErrBadText, Msg: "not a datetime: " + s}
	}
	return dateTimeValue(float64(t.Unix()))
}

func fnDateOf(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull {
		return Blank(), nil
	}
	return Date(floorDiv(int64(args[0].num), secondsPerDay)), nil
}

func fnDateTime(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull {
		return Blank(), nil
	}
	return DateTime(int64(args[0].num) * secondsPerDay), nil
}

func datePart(part func(time.Time) float64) func(*VM, []Value) (Value, *EvalError) {
	return func(_ *VM, args []Value) (Value, *EvalError) {
		if args[0].kind == KindNull {
			return Blank(), nil
		}
		return Number(part(timeOf(args[0]))), nil
	}
}

// dayNumber is the calendar day of a date or a datetime.
func dayNumber(v Value) float64 {
	if v.kind == KindDate {
		return v.num
	}
	return math.Floor(v.num / secondsPerDay)
}

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
	s, e := timeOf(args[0]), timeOf(args[1])
	return Number(float64((e.Year()*12 + int(e.Month())) - (s.Year()*12 + int(s.Month())))), nil
}

func fnYearsBetween(_ *VM, args []Value) (Value, *EvalError) {
	if args[0].kind == KindNull || args[1].kind == KindNull {
		return Blank(), nil
	}
	return Number(float64(timeOf(args[1]).Year() - timeOf(args[0]).Year())), nil
}

func fnAddDays(_ *VM, args []Value) (Value, *EvalError) {
	x := args[0]
	switch x.kind {
	case KindNull:
		return Blank(), nil
	case KindDate:
		return dateValue(x.num + math.Trunc(args[1].num))
	}
	return dateTimeValue(x.num + float64(args[1].num*secondsPerDay))
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
		return Value{}, dateOutOfRange()
	}
	return dateLike(v, addMonthsClamped(timeOf(v), int(n)))
}

func fnAddMonths(_ *VM, args []Value) (Value, *EvalError) { return addMonths(args[0], args[1].num) }

func fnAddYears(_ *VM, args []Value) (Value, *EvalError) {
	return addMonths(args[0], 12*math.Trunc(args[1].num))
}

// ---- Context

func fnNow(vm *VM, _ []Value) (Value, *EvalError) { return DateTime(vm.now()), nil }

func fnToday(vm *VM, _ []Value) (Value, *EvalError) {
	return Date(floorDiv(vm.now(), secondsPerDay)), nil
}

func fnUser(vm *VM, _ []Value) (Value, *EvalError) {
	if vm.ctx == nil || vm.ctx.User == "" {
		return Blank(), nil
	}
	return Text(vm.ctx.User), nil
}

func fnState(vm *VM, _ []Value) (Value, *EvalError) {
	if vm.ctx == nil || vm.ctx.State == "" {
		return Blank(), nil
	}
	return Text(vm.ctx.State), nil
}

func fnID(vm *VM, _ []Value) (Value, *EvalError) {
	if vm.ctx == nil || vm.ctx.ID == "" {
		return Blank(), nil
	}
	return Text(vm.ctx.ID), nil
}

// lifeMoment makes the function of one moment of the document's life,
// blank when there is no document or the moment is not set.
func lifeMoment(get func(*Life) int64) func(*VM, []Value) (Value, *EvalError) {
	return func(vm *VM, _ []Value) (Value, *EvalError) {
		if vm.ctx == nil || vm.ctx.Doc == nil {
			return Blank(), nil
		}
		if secs := get(vm.ctx.Doc); secs != 0 {
			return DateTime(secs), nil
		}
		return Blank(), nil
	}
}

// lifeText makes the function of one text of the document's life.
func lifeText(get func(*Life) string) func(*VM, []Value) (Value, *EvalError) {
	return func(vm *VM, _ []Value) (Value, *EvalError) {
		if vm.ctx == nil || vm.ctx.Doc == nil {
			return Blank(), nil
		}
		if s := get(vm.ctx.Doc); s != "" {
			return Text(s), nil
		}
		return Blank(), nil
	}
}

func fnVersion(vm *VM, _ []Value) (Value, *EvalError) {
	if vm.ctx == nil || vm.ctx.Doc == nil || vm.ctx.Doc.Version == 0 {
		return Blank(), nil
	}
	return Number(float64(vm.ctx.Doc.Version)), nil
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
