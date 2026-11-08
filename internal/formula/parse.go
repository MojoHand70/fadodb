package formula

import "time"

// ParseExact reads an exact number written as text, "12.30" for a decimal
// or "12.30 PLN" for a money (ADR-0022), as a value of the type, brought to
// the type's scale. A digit the scale would drop is the error inexact, a
// mantissa past int64 is overflow, and anything else is bad_text.
func ParseExact(s string, t Type) (Value, *EvalError) {
	switch t.Kind {
	case KindDecimal:
		m, scale, err := parseDecimal(s)
		if err != nil {
			return Value{}, err
		}
		if m, err = rescaleExact(m, scale, int(t.Scale)); err != nil {
			return Value{}, err
		}
		return Decimal(m, int(t.Scale)), nil
	case KindMoney:
		m, scale, cur, err := parseMoney(s)
		if err != nil {
			return Value{}, err
		}
		if m, err = rescaleExact(m, scale, int(t.Scale)); err != nil {
			return Value{}, err
		}
		return Money(m, int(t.Scale), cur), nil
	}
	return Value{}, &EvalError{Code: ErrBadText, Pos: -1, Msg: t.String() + " is not an exact type"}
}

// ExactFromFloat makes a decimal of the type from a float64 that is
// exactly a value at the type's scale, as a bare literal meeting an exact
// variable is read (ADR-0022): 0 and 12.3 fit a decimal(2), 0.125 does not.
// It reports whether the float fit.
func ExactFromFloat(f float64, t Type) (Value, bool) {
	if t.Kind != KindDecimal {
		return Value{}, false
	}
	m, err := decFromFloat(f, int(t.Scale), rmHalfUp)
	if err != nil || decToFloat(m, int(t.Scale)) != f {
		return Value{}, false
	}
	return Decimal(m, int(t.Scale)), true
}

// Rescale brings an exact value to another scale: up is exact, down is
// the error inexact when a dropped digit is not zero. A blank stays blank;
// a value that is not exact is returned as it is.
func Rescale(v Value, scale int) (Value, *EvalError) {
	if !v.kind.IsExact() || int(v.scale) == scale {
		return v, nil
	}
	m, err := rescaleExact(v.Mant(), int(v.scale), scale)
	if err != nil {
		return Value{}, err
	}
	return Value{kind: v.kind, scale: int8(scale), num: mantBits(m), str: v.str}, nil
}

// Render renders a value as the TEXT function does: a decimal with every
// place, a money with its currency, a date as YYYY-MM-DD, a datetime as
// YYYY-MM-DD HH:MM:SS, a blank as the empty text (ADR-0019, ADR-0022).
func Render(v Value) string { return render(v) }

// Day makes a date value from a calendar day of the proleptic Gregorian
// calendar, as DATE(year, month, day) does; the month and the day
// normalise as time.Date normalises them.
func Day(year int, month int, day int) Value {
	return Date(floorDiv(time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Unix(), secondsPerDay))
}
