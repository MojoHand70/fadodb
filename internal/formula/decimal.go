package formula

import (
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// Exact arithmetic for decimal, int and money (ADR-0022). A value is an
// int64 mantissa at a scale, the number of digits after the point, 0 to
// MaxScale. Addition, subtraction and multiplication are exact or stop
// with overflow; division and rounding land on a defined scale with a
// named method. The SQL dialects spell the same steps, so every evaluator
// gets the same mantissa.

// MaxScale is the most digits after the point an exact value may carry.
// Eighteen leaves one digit before the point at the top of the range and
// keeps every power of ten in an int64.
const MaxScale = 18

// DivScaleStep is how many digits a division adds to the dividend's scale,
// the rule MySQL uses: 1.00 / 3 is 0.333333 at scale six.
const DivScaleStep = 4

var pow10i = [MaxScale + 1]int64{
	1, 10, 100, 1000, 10000, 100000, 1000000, 10000000, 100000000, 1000000000,
	10000000000, 100000000000, 1000000000000, 10000000000000, 100000000000000,
	1000000000000000, 10000000000000000, 100000000000000000, 1000000000000000000,
}

// absU is the magnitude of an int64 as a uint64, right for MinInt64 too.
func absU(a int64) uint64 {
	if a < 0 {
		return uint64(-a)
	}
	return uint64(a)
}

func sign64(a int64) int64 {
	switch {
	case a > 0:
		return 1
	case a < 0:
		return -1
	}
	return 0
}

// addInt adds with an overflow check.
func addInt(a, b int64) (int64, bool) {
	r := a + b
	if (a >= 0) == (b >= 0) && (r >= 0) != (a >= 0) {
		return 0, false
	}
	return r, true
}

// mulInt multiplies with an overflow check.
func mulInt(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	neg := (a < 0) != (b < 0)
	hi, lo := bits.Mul64(absU(a), absU(b))
	if hi != 0 {
		return 0, false
	}
	if neg {
		if lo > 1<<63 {
			return 0, false
		}
		return -int64(lo), true
	}
	if lo > math.MaxInt64 {
		return 0, false
	}
	return int64(lo), true
}

// scaleBy multiplies a mantissa by 10^k, k from 0 to MaxScale.
func scaleBy(m int64, k int) (int64, bool) {
	if k <= 0 {
		return m, true
	}
	if k > MaxScale {
		return 0, m == 0
	}
	return mulInt(m, pow10i[k])
}

// alignScales brings two mantissas to the larger of their scales.
func alignScales(a int64, sa int, b int64, sb int) (int64, int64, int, bool) {
	switch {
	case sa < sb:
		a, ok := scaleBy(a, sb-sa)
		return a, b, sb, ok
	case sb < sa:
		b, ok := scaleBy(b, sa-sb)
		return a, b, sa, ok
	}
	return a, b, sa, true
}

// divRound divides x by y, which is not zero, rounding half away from zero.
// It reports false when the quotient does not fit, which only MinInt64 / -1
// can cause.
func divRound(x, y int64) (int64, bool) {
	if y == -1 {
		if x == math.MinInt64 {
			return 0, false
		}
		return -x, true
	}
	q := x / y
	r := x % y
	if r != 0 && 2*absU(r) >= absU(y) {
		if (x < 0) == (y < 0) {
			q++
		} else {
			q--
		}
	}
	return q, true
}

// divScale is the scale of a quotient: the dividend's scale plus
// DivScaleStep, at most MaxScale.
func divScale(sa int) int {
	if sa+DivScaleStep > MaxScale {
		return MaxScale
	}
	return sa + DivScaleStep
}

// decDiv divides a at scale sa by b at scale sb and lands on divScale(sa).
// The dividend scaled up to the result must fit an int64, as the SQL
// dialects compute it; otherwise the result is overflow.
func decDiv(a int64, sa int, b int64, sb int) (int64, int, *EvalError) {
	if b == 0 {
		return 0, 0, errDivByZero
	}
	r := divScale(sa)
	x, ok := scaleBy(a, r-sa+sb)
	if !ok {
		return 0, 0, overflow()
	}
	q, ok := divRound(x, b)
	if !ok {
		return 0, 0, overflow()
	}
	return q, r, nil
}

// decMod is a mod b on aligned mantissas, with the sign of the divisor as
// in Excel: a - b * floor(a / b).
func decMod(a, b int64) (int64, *EvalError) {
	if b == 0 {
		return 0, errModByZero
	}
	if b == -1 {
		return 0, nil
	}
	r := a % b
	if r != 0 && (r < 0) != (b < 0) {
		r += b
	}
	return r, nil
}

func overflow() *EvalError { return errOverflow }

// roundMethod is how a rounding step treats the digits it drops.
type roundMethod uint8

const (
	rmHalfUp   roundMethod = iota // to the nearest, a half away from zero
	rmHalfEven                    // to the nearest, a half to the even neighbour
	rmDown                        // toward zero
	rmUp                          // away from zero
	rmFloor                       // toward minus infinity
	rmCeiling                     // toward plus infinity
)

var roundMethods = map[string]roundMethod{
	"half_up": rmHalfUp, "half_even": rmHalfEven, "down": rmDown, "up": rmUp, "floor": rmFloor, "ceiling": rmCeiling,
}

// RoundMethods lists the names ROUND and DECIMAL accept, in catalog order.
var RoundMethods = []string{"half_up", "half_even", "down", "up", "floor", "ceiling"}

// roundMant rounds the mantissa m at scale s to the given places, which may
// be negative to round to tens and hundreds, and returns the mantissa at
// scale max(places, 0). Moving to more places is exact; fewer places drop
// digits by the method.
func roundMant(m int64, s int, places int, rm roundMethod) (int64, int, bool) {
	if places >= s {
		r, ok := scaleBy(m, places-s)
		return r, places, ok
	}
	drop := s - places
	var q, r int64
	var p uint64
	if drop > MaxScale {
		// Every digit goes: the magnitude is below the step, so the
		// quotient is zero and the whole value is the remainder.
		q, r, p = 0, m, math.MaxUint64
	} else {
		pp := pow10i[drop]
		q, r, p = m/pp, m%pp, uint64(pp)
	}
	sign := sign64(m)
	switch rm {
	case rmHalfUp:
		if r != 0 && 2*absU(r) >= p {
			q += sign
		}
	case rmHalfEven:
		if twice := 2 * absU(r); r != 0 && (twice > p || twice == p && q&1 != 0) {
			q += sign
		}
	case rmDown:
	case rmUp:
		if r != 0 {
			q += sign
		}
	case rmFloor:
		if r != 0 && m < 0 {
			q--
		}
	case rmCeiling:
		if r != 0 && m > 0 {
			q++
		}
	}
	if places < 0 {
		q, ok := scaleBy(q, -places)
		return q, 0, ok
	}
	return q, places, true
}

// rescaleExact moves a mantissa to another scale without rounding: up is
// exact, down is allowed only when the dropped digits are zero.
func rescaleExact(m int64, s, to int) (int64, *EvalError) {
	if to >= s {
		r, ok := scaleBy(m, to-s)
		if !ok {
			return 0, overflow()
		}
		return r, nil
	}
	p := pow10i[s-to]
	if m%p != 0 {
		return 0, &EvalError{Code: ErrInexact, Msg: "the value has more decimal places than " + strconv.Itoa(to)}
	}
	return m / p, nil
}

// formatDecimal renders a mantissa at a scale: every digit after the point,
// trailing zeros included, so 0.30 at scale two is "0.30".
func formatDecimal(m int64, s int) string {
	digits := strconv.FormatUint(absU(m), 10)
	if s > 0 {
		if len(digits) <= s {
			digits = strings.Repeat("0", s-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-s] + "." + digits[len(digits)-s:]
	}
	if m < 0 {
		return "-" + digits
	}
	return digits
}

// parseDecimal reads a number written exactly: an optional sign, digits
// with an optional fraction, an optional exponent. The scale is the number
// of digits written after the point, moved by the exponent, trailing zeros
// kept: "0.30" is 30 at scale two and "1e3" is 1000 at scale zero. A zero
// is zero at scale zero whatever its exponent. It reports bad_text for
// anything else and overflow for more than MaxScale places or a mantissa
// beyond int64.
func parseDecimal(s string) (int64, int, *EvalError) {
	bad := func() (int64, int, *EvalError) {
		return 0, 0, &EvalError{Code: ErrBadText, Msg: "not a decimal: " + s}
	}
	text := s
	neg := false
	if text != "" && (text[0] == '+' || text[0] == '-') {
		neg = text[0] == '-'
		text = text[1:]
	}
	expText := ""
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		expText = text[i+1:]
		text = text[:i]
		if expText == "" || (expText[0] == '+' || expText[0] == '-') && len(expText) == 1 {
			return bad()
		}
		for _, c := range expText[1:] {
			if c < '0' || c > '9' {
				return bad()
			}
		}
		if c := expText[0]; c != '+' && c != '-' && (c < '0' || c > '9') {
			return bad()
		}
	}
	intPart, fracPart, _ := strings.Cut(text, ".")
	if intPart == "" && fracPart == "" {
		return bad()
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return bad()
		}
	}
	digits := strings.TrimLeft(intPart+fracPart, "0")
	if digits == "" {
		return 0, 0, nil
	}
	exp := 0
	if expText != "" {
		e, err := strconv.Atoi(expText)
		if err != nil || e > 1000000 || e < -1000000 {
			// Beyond any mantissa or scale the type can hold.
			return 0, 0, overflow()
		}
		exp = e
	}
	scale := len(fracPart) - exp
	if scale < 0 {
		if -scale > 19 {
			return 0, 0, overflow()
		}
		digits += strings.Repeat("0", -scale)
		scale = 0
	}
	if scale > MaxScale || len(digits) > 19 {
		return 0, 0, overflow()
	}
	u, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || u > math.MaxInt64+1 || u == math.MaxInt64+1 && !neg {
		return 0, 0, overflow()
	}
	m := int64(u)
	if neg {
		m = -m
	}
	return m, scale, nil
}

// parseCurrency reads a currency code: three upper-case ASCII letters, the
// ISO 4217 shape.
func parseCurrency(s string) (string, bool) {
	if len(s) != 3 {
		return "", false
	}
	for i := 0; i < 3; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return "", false
		}
	}
	return s, true
}

// parseMoney reads "12.30 PLN": an exact amount, one space, a currency.
func parseMoney(s string) (int64, int, string, *EvalError) {
	amount, cur, ok := strings.Cut(s, " ")
	code, okCur := parseCurrency(cur)
	if !ok || !okCur {
		return 0, 0, "", &EvalError{Code: ErrBadText, Msg: "not an amount with a currency: " + s}
	}
	m, scale, err := parseDecimal(amount)
	if err != nil {
		return 0, 0, "", err
	}
	return m, scale, code, nil
}

// decToFloat is the float64 nearest to a mantissa at a scale, as the SQL
// dialects compute it: the mantissa as a double divided by the power of
// ten as a double.
func decToFloat(m int64, s int) float64 { return float64(m) / pow10(s) }

// decFromFloat turns a float64 into a mantissa at the given places by the
// method, the way DECIMAL(number, places, method) converts: the scaled
// value is rounded by the float rules of ADR-0004 first, so that 0.29 at
// two places is 29 under every method, then the step is applied.
func decFromFloat(f float64, places int, rm roundMethod) (int64, *EvalError) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, notANumber()
	}
	scaled := f * pow10(places)
	var stepped float64
	switch rm {
	case rmHalfUp:
		stepped = math.Round(scaled)
	case rmHalfEven:
		stepped = math.RoundToEven(scaled)
	default:
		guarded := roundTo(scaled, 9)
		switch rm {
		case rmDown:
			stepped = math.Trunc(guarded)
		case rmUp:
			if guarded < 0 {
				stepped = math.Floor(guarded)
			} else {
				stepped = math.Ceil(guarded)
			}
		case rmFloor:
			stepped = math.Floor(guarded)
		default:
			stepped = math.Ceil(guarded)
		}
	}
	if math.Abs(stepped) >= 1<<63 {
		return 0, overflow()
	}
	return int64(stepped), nil
}

// roundFloat rounds a float64 at the given digits by the method, the
// float rules of ADR-0004 extended to the methods of ADR-0022.
func roundFloat(v, digits float64, rm roundMethod) float64 {
	switch rm {
	case rmHalfUp:
		return roundTo(v, digits)
	case rmHalfEven:
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
		return math.RoundToEven(scaled) / p
	case rmDown:
		return scaledStep(v, digits, math.Trunc)
	case rmUp:
		return scaledStep(v, digits, func(x float64) float64 {
			if x < 0 {
				return math.Floor(x)
			}
			return math.Ceil(x)
		})
	case rmFloor:
		return scaledStep(v, digits, math.Floor)
	}
	return scaledStep(v, digits, math.Ceil)
}
