package formula

import "math"

// Aggregate folds the values of the slot's expression over the matching
// rows, given in the source's key order, into the aggregate's answer
// (ADR-0046). It is what the same function gives over the same values
// inside one document: blanks are skipped, COUNT of nothing is 0, SUM of
// nothing is 0, or blank for money, and MIN and MAX of nothing are blank.
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
	}
	return Value{}, &EvalError{Code: ErrQueryFailed, Pos: -1, Msg: "unknown aggregate " + q.Agg}
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
