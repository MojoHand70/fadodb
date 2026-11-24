package formula

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Aggregate folds the values of the slot's expression over the matching
// rows, given in the source's key order, into the aggregate's answer
// (ADR-0046). It is what the same function gives over the same values
// inside one document: blanks are skipped, COUNT of nothing is 0, SUM of
// nothing is 0, or blank for money, and MIN and MAX of nothing are blank.
// AVERAGE, MEDIAN and COUNTUNIQUE are ADR-0081's: the mean and the middle
// value of nothing are blank; the mean of exact values is their exact sum
// divided as the / operator divides, the middle of an even number of them
// the mean of the two in the middle, exact at one place more; and
// COUNTUNIQUE counts the values that differ, as = compares them, money of
// two currencies being two values.
// Two things belong to rows: numbers are added in the order given, which
// is why the order is fixed, and an exact sum fails with overflow only
// when the total does not fit, whatever the order. It may reuse vals.
func (q *QuerySlot) Aggregate(vals []Value) (Value, *EvalError) {
	in := q.Value.Result
	switch q.Agg {
	case "COUNT":
		return fnCount(nil, vals)
	case "MIN", "MAX":
		fn := fnMin
		if q.Agg == "MAX" {
			fn = fnMax
		}
		if !in.IsExact() {
			return fn(nil, vals)
		}
		v, err := fn(nil, append(vals, Decimal(int64(in.Scale), 0)))
		if err != nil {
			return Value{}, err
		}
		return decUnaryValue(OpDecScale, v, decArg(q.Type))
	case "SUM":
		if !in.IsExact() {
			return SumRows(vals)
		}
		m, cur, err := sumExactRows(vals, int(in.Scale))
		if err != nil {
			return Value{}, err
		}
		return decUnaryValue(OpDecScale, exactResultOf(m, int(in.Scale), cur), decArg(q.Type))
	case "AVERAGE":
		if !in.IsExact() {
			return fnAverage(nil, vals)
		}
		m, cur, err := sumExactRows(vals, int(in.Scale))
		if err != nil {
			return Value{}, err
		}
		n := int64(0)
		for _, a := range vals {
			if a.kind != KindNull {
				n++
			}
		}
		if n == 0 {
			return Blank(), nil
		}
		mean, scale, err := decDiv(m, int(in.Scale), n, 0)
		if err != nil {
			return Value{}, err
		}
		return decUnaryValue(OpDecScale, exactResultOf(mean, scale, cur), decArg(q.Type))
	case "MEDIAN":
		if !in.IsExact() {
			xs := make([]float64, 0, len(vals))
			for _, a := range vals {
				if a.kind != KindNull {
					xs = append(xs, a.num)
				}
			}
			if len(xs) == 0 {
				return Blank(), nil
			}
			sort.Float64s(xs)
			// Each half is taken before they are added, so that the middle
			// of two large numbers is a number, as each of them is.
			return Number(xs[(len(xs)-1)/2]/2 + xs[len(xs)/2]/2), nil
		}
		ms, cur, err := exactRows(vals, int(in.Scale))
		if err != nil {
			return Value{}, err
		}
		if len(ms) == 0 {
			return Blank(), nil
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		// The two in the middle, which are one for an odd number of rows,
		// added and halved at one place more: their sum times five.
		lo, hi := ms[(len(ms)-1)/2], ms[len(ms)/2]
		if hi > 0 && lo > math.MaxInt64-hi || hi < 0 && lo < math.MinInt64-hi {
			return Value{}, overflow()
		}
		sum := lo + hi
		if sum > math.MaxInt64/5 || sum < math.MinInt64/5 {
			return Value{}, overflow()
		}
		return decUnaryValue(OpDecScale, exactResultOf(sum*5, int(in.Scale)+1, cur), decArg(q.Type))
	case "COUNTUNIQUE":
		seen := map[distinctKey]struct{}{}
		for _, a := range vals {
			if a.kind != KindNull {
				seen[keyOf(a)] = struct{}{}
			}
		}
		return Number(float64(len(seen))), nil
	}
	return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown aggregate " + q.Agg}
}

// AggregateWith is Aggregate for a slot that may be one of the
// aggregates of a list alone (ADR-0081, step 3). The values are in the
// order of the list. FIRST and LAST give the first and the last of them,
// blank for none; INDEX the one at the place extra says, counted from 1,
// and the error out_of_range for a place that is no whole number or that
// the list does not have; JOIN the values written as TEXT writes them,
// blanks skipped, with extra between them, blank for none, and
// out_of_range when the text would pass maxText; EXISTS whether there is
// a value at all, which answers CONTAINS.
func (q *QuerySlot) AggregateWith(vals []Value, extra Value, maxText int) (Value, *EvalError) {
	switch q.Agg {
	case "FIRST":
		if len(vals) == 0 {
			return Blank(), nil
		}
		return vals[0], nil
	case "LAST":
		if len(vals) == 0 {
			return Blank(), nil
		}
		return vals[len(vals)-1], nil
	case "INDEX":
		at := extra.num
		if extra.kind != KindNumber || at != math.Trunc(at) || at < 1 || at > float64(len(vals)) {
			return Value{}, outOfRange("the list has no such place")
		}
		return vals[int(at)-1], nil
	case "JOIN":
		sep := ""
		if extra.kind != KindNull {
			sep = render(extra)
		}
		var b strings.Builder
		n, runes := 0, 0
		for _, a := range vals {
			if a.kind == KindNull {
				continue
			}
			t := render(a)
			if t == "" {
				continue
			}
			if n > 0 {
				b.WriteString(sep)
				runes += utf8.RuneCountInString(sep)
			}
			if runes += utf8.RuneCountInString(t); runes > maxText {
				return Value{}, tooLong()
			}
			b.WriteString(t)
			n++
		}
		if n == 0 {
			return Blank(), nil
		}
		return Text(b.String()), nil
	case "EXISTS":
		// the values are the comparison of each row: whether any holds
		for _, a := range vals {
			if a.Truth() {
				return Bool(true), nil
			}
		}
		return Bool(false), nil
	}
	return q.Aggregate(vals)
}

// distinctKey is what makes two values of one column the same value, as
// = compares them: a text by its text, an exact number by its mantissa,
// money by its amount and its currency, and the others by their number.
type distinctKey struct {
	num  float64
	mant int64
	str  string
}

func keyOf(a Value) distinctKey {
	switch a.kind {
	case KindText, KindRef, KindJSON:
		return distinctKey{str: a.str}
	case KindDecimal:
		return distinctKey{mant: a.Mant()}
	case KindMoney:
		return distinctKey{mant: a.Mant(), str: a.str}
	}
	return distinctKey{num: a.num + 0} // -0 and 0 are one value
}

// exactRows are the mantissas of the exact values at one scale, blanks
// left out, and their currency. The currencies must agree.
func exactRows(vals []Value, scale int) ([]int64, string, *EvalError) {
	cur := ""
	for _, a := range vals {
		if a.kind == KindMoney {
			if cur != "" && a.str != cur {
				return nil, "", currencyMismatch()
			}
			cur = a.str
		}
	}
	ms := make([]int64, 0, len(vals))
	for _, a := range vals {
		if a.kind == KindNull {
			continue
		}
		m, s := exactOperand(a)
		m, ok := scaleBy(m, scale-s)
		if !ok {
			return nil, "", overflow()
		}
		ms = append(ms, m)
	}
	return ms, cur, nil
}

// SumRows adds numbers one by one in the order given, blanks skipped, as
// SUM adds its arguments: the sum over rows of the Go VM, and of the
// aggregate function the SQLite dialect registers.
func SumRows(vals []Value) (Value, *EvalError) {
	s, _ := sum(vals)
	return finite(s)
}

// sumExactRows adds exact values at one scale. The currencies must
// agree, and the sum overflows only when the total does not fit: the
// mantissas are added as two halves, which a SQL engine can do with its
// own aggregate, and joined at the end.
func sumExactRows(vals []Value, scale int) (int64, string, *EvalError) {
	var (
		hi, lo int64
		cur    string
	)
	for _, a := range vals {
		if a.kind == KindMoney {
			if cur != "" && a.str != cur {
				return 0, "", currencyMismatch()
			}
			cur = a.str
		}
	}
	for _, a := range vals {
		if a.kind == KindNull {
			continue
		}
		m, s := exactOperand(a)
		m, ok := scaleBy(m, scale-s)
		if !ok {
			return 0, "", overflow()
		}
		hi += m >> 32
		lo += m & 0xffffffff
	}
	m, ok := wideTotal(hi, lo)
	if !ok {
		return 0, "", overflow()
	}
	return m, cur, nil
}

// wideTotal joins the two halves of a sum of mantissas, the sum of their
// upper parts, m >> 32, and the sum of their lower 32 bits, into the
// total, when it fits in 64 bits. The halves themselves cannot overflow
// below two thousand million rows.
func wideTotal(hi, lo int64) (int64, bool) {
	h := hi + lo>>32
	if h < math.MinInt32 || h > math.MaxInt32 {
		return 0, false
	}
	return h<<32 | lo&0xffffffff, true
}
