package formula

import (
	"math"
	"strconv"
	"time"
)

// Value is a run-time value. Scalars live in the struct and never allocate:
// bool, number and date in num, text, ref and json in str. Composites
// (ADR-0013) will hang off pointers added with the composites change; the
// scalar layout does not move. The zero Value is blank.
type Value struct {
	kind Kind
	num  float64
	str  string
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

// Date makes a date value from seconds since the epoch, UTC.
func Date(secs int64) Value { return Value{kind: KindDate, num: float64(secs)} }

// Text makes a text value.
func Text(s string) Value { return Value{kind: KindText, str: s} }

// Ref makes a ref value from its Class:id form.
func Ref(s string) Value { return Value{kind: KindRef, str: s} }

// JSON makes a json value from its text.
func JSON(s string) Value { return Value{kind: KindJSON, str: s} }

// Kind is the run-time kind of the value.
func (v Value) Kind() Kind { return v.kind }

// IsBlank reports whether the value is blank.
func (v Value) IsBlank() bool { return v.kind == KindNull }

// Num returns the number of a number, the 1 or 0 of a bool, the seconds of
// a date, and 0 for a blank: the zero of ADR-0005.
func (v Value) Num() float64 { return v.num }

// Str returns the text of a text, ref or json value and "" for a blank.
func (v Value) Str() string { return v.str }

// Truth returns the value as a condition: TRUE is true, FALSE and blank
// are false.
func (v Value) Truth() bool { return v.kind == KindBool && v.num != 0 }

// Secs returns the seconds of a date value.
func (v Value) Secs() int64 { return int64(v.num) }

// Equal reports whether two values are the same value of the same kind. It
// is the identity used by tests, not the = operator of the language.
func Equal(a, b Value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case KindNull:
		return true
	case KindBool, KindNumber, KindDate:
		return a.num == b.num
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

// render is the TEXT rule of ADR-0019: the single place a scalar becomes
// text.
func render(v Value) string {
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
	case KindDate:
		return renderDate(int64(v.num))
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

func renderDate(secs int64) string {
	t := time.Unix(secs, 0).UTC()
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04:05")
}

// The date range of ADR-0019: year 1 to year 9999.
const (
	minDateSecs   = -62135596800 // 0001-01-01 00:00:00
	maxDateSecs   = 253402300799 // 9999-12-31 23:59:59
	secondsPerDay = 86400
)

func dateInRange(secs float64) bool {
	return secs >= minDateSecs && secs <= maxDateSecs && !math.IsNaN(secs)
}
