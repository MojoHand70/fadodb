package formula

import (
	"math"
	"strconv"
	"time"
)

// Value is a run-time value. Scalars live in the struct and never allocate:
// bool, number, date and datetime in num, text, ref and json in str, and an
// exact number's mantissa in the bits of num with its scale beside it, so
// that the struct stays four words wide (ADR-0022). Composites (ADR-0013)
// will hang off pointers added with the composites change; the scalar
// layout does not move. The zero Value is blank.
type Value struct {
	kind  Kind
	scale int8    // decimal and money: digits after the point
	num   float64 // number; the 1 or 0 of a bool; the days of a date; the seconds of a datetime; the mantissa bits of a decimal or money
	str   string  // text, ref, json; the currency of a money
}

// Array is an array<scalar>, stored column-wise: numbers, dates and
// booleans in Nums, text, refs and json in Strs, with Nulls marking blank
// elements. Composite operations arrive with a later change.
type Array struct {
	Elem  Kind
	Nums  []float64
	Strs  []string
	Nulls []bool
}

// Rows is a rows<schema> value: one Array per column.
type Rows struct {
	Cols []Column
	Data []Array
	N    int
}

// Blank is the blank value.
func Blank() Value { return Value{} }

// Bool makes a bool value.
func Bool(b bool) Value {
	if b {
		return Value{kind: KindBool, num: 1}
	}
	return Value{kind: KindBool}
}

// Number makes a number value.
func Number(f float64) Value { return Value{kind: KindNumber, num: f} }

// Date makes a date value from days since 1970-01-01.
func Date(days int64) Value { return Value{kind: KindDate, num: float64(days)} }

// DateTime makes a datetime value from seconds since the epoch, UTC.
func DateTime(secs int64) Value { return Value{kind: KindDateTime, num: float64(secs)} }

// Text makes a text value.
func Text(s string) Value { return Value{kind: KindText, str: s} }

// Ref makes a ref value from its Class:id form.
func Ref(s string) Value { return Value{kind: KindRef, str: s} }

// JSON makes a json value from its text.
func JSON(s string) Value { return Value{kind: KindJSON, str: s} }

// Decimal makes an exact value from a mantissa at a scale: Decimal(1230, 2)
// is 12.30.
func Decimal(mant int64, scale int) Value {
	return Value{kind: KindDecimal, scale: int8(scale), num: mantBits(mant)}
}

// Money makes a money value from a mantissa, a scale and a currency code.
func Money(mant int64, scale int, currency string) Value {
	return Value{kind: KindMoney, scale: int8(scale), num: mantBits(mant), str: currency}
}

// mantBits stores a mantissa in the bits of a float64. The float path never
// reads it: every operation on an exact value switches on the kind first.
func mantBits(m int64) float64 { return math.Float64frombits(uint64(m)) }

// Kind is the run-time kind of the value.
func (v Value) Kind() Kind { return v.kind }

// IsBlank reports whether the value is blank.
func (v Value) IsBlank() bool { return v.kind == KindNull }

// Num returns the number of a number, the 1 or 0 of a bool, the days of a
// date, the seconds of a datetime, and 0 for a blank: the zero of
// ADR-0005. It is meaningless for a decimal or a money; use Mant.
func (v Value) Num() float64 { return v.num }

// Str returns the text of a text, ref or json value, the currency of a
// money, and "" for a blank.
func (v Value) Str() string { return v.str }

// Truth returns the value as a condition: TRUE is true, FALSE and blank
// are false.
func (v Value) Truth() bool { return v.kind == KindBool && v.num != 0 }

// Secs returns the seconds of a datetime value.
func (v Value) Secs() int64 { return int64(v.num) }

// Days returns the days of a date value.
func (v Value) Days() int64 { return int64(v.num) }

// Mant returns the mantissa of a decimal or money value, and 0 for a blank.
func (v Value) Mant() int64 {
	if !v.kind.IsExact() {
		return 0
	}
	return int64(math.Float64bits(v.num))
}

// Scale returns the scale of a decimal or money value.
func (v Value) Scale() int { return int(v.scale) }

// Currency returns the currency code of a money value, "" otherwise.
func (v Value) Currency() string {
	if v.kind != KindMoney {
		return ""
	}
	return v.str
}

// Equal reports whether two values are the same value of the same kind. An
// exact value compares by mantissa and scale, so 0.3 and 0.30 differ. It
// is the identity used by tests, not the = operator of the language.
func Equal(a, b Value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KindNull:
		return true
	case KindBool, KindNumber, KindDate, KindDateTime:
		return a.num == b.num
	case KindDecimal:
		return a.scale == b.scale && a.Mant() == b.Mant()
	case KindMoney:
		return a.scale == b.scale && a.Mant() == b.Mant() && a.str == b.str
	case KindText, KindRef, KindJSON:
		return a.str == b.str
	}
	return false
}

// String renders the value for messages and tests, as the TEXT function
// does, with blank shown as BLANK and text quoted.
func (v Value) String() string {
	switch v.kind {
	case KindNull:
		return "BLANK"
	case KindText:
		return strconv.Quote(v.str)
	}
	return render(v)
}

// render is the TEXT rule of ADR-0019 and ADR-0022: the single place a
// scalar becomes text.
func render(v Value) string {
	if v.kind == KindText {
		return v.str
	}
	switch v.kind {
	case KindNull:
		return ""
	case KindBool:
		if v.num != 0 {
			return "TRUE"
		}
		return "FALSE"
	case KindNumber:
		return renderNumber(v.num)
	case KindDecimal:
		return formatDecimal(v.Mant(), int(v.scale))
	case KindMoney:
		return formatDecimal(v.Mant(), int(v.scale)) + " " + v.str
	case KindDate:
		return renderDate(int64(v.num))
	case KindDateTime:
		return renderDateTime(int64(v.num))
	case KindText, KindRef, KindJSON:
		return v.str
	}
	return ""
}

// renderNumber renders with 15 significant digits by the C %.15g rule,
// trailing zeros removed, so an integral value below 1e15 is plain digits.
func renderNumber(f float64) string {
	if f == 0 {
		return "0"
	}
	return strconv.FormatFloat(f, 'g', 15, 64)
}

func renderDate(days int64) string {
	return time.Unix(days*secondsPerDay, 0).UTC().Format(dateLayout)
}

func renderDateTime(secs int64) string {
	return time.Unix(secs, 0).UTC().Format(dateTimeLayout)
}

// The ranges of ADR-0019 and ADR-0023: year 1 to year 9999, as seconds for
// a datetime and as days for a date.
const (
	minDateSecs   = -62135596800 // 0001-01-01 00:00:00
	maxDateSecs   = 253402300799 // 9999-12-31 23:59:59
	minDateDays   = -719162      // 0001-01-01
	maxDateDays   = 2932896      // 9999-12-31
	secondsPerDay = 86400
)

func dateTimeInRange(secs float64) bool {
	return secs >= minDateSecs && secs <= maxDateSecs && !math.IsNaN(secs)
}

func dateInRange(days float64) bool {
	return days >= minDateDays && days <= maxDateDays && !math.IsNaN(days)
}
