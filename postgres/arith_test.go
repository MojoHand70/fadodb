package postgres

import (
	"database/sql"
	"math"
	"math/rand/v2"
	"testing"
)

// Postgres fails a statement where a float leaves its range, and the
// dialect's own addition, multiplication and division may not: they
// give what the VM's arithmetic gives, which is Go's, an infinity past
// the range and zero under it. The test holds them to Go, to the bit,
// over the pairs of the numbers at the edges of the range and over
// pairs of any bits at all.
func TestArithmetic_AsGoOverTheWholeRange(t *testing.T) {
	db := openPostgres(t)
	edges := []float64{0, 1, 2, 0.5, 3, 10, 1e-7, 1e15, 1e100, 1e150, 1e-150, 1e290, 8e307, 1e300, 1e-300, 1e308, 1e-308,
		math.MaxFloat64, math.MaxFloat64 / 2, math.SmallestNonzeroFloat64, 2 * math.SmallestNonzeroFloat64, 2.2250738585072014e-308,
		math.Ldexp(1, -537), math.Ldexp(1, -538), math.Ldexp(1, 537), math.Ldexp(1, 512), math.Ldexp(1, -512), math.Ldexp(1, 1023),
		math.Ldexp(1, -1022), math.Ldexp(1, -60), math.Ldexp(1, -40), math.Ldexp(1, -51), math.Ldexp(1, -50), math.Nextafter(1, 2), math.Nextafter(1, 0),
		math.Ldexp(math.Nextafter(1, 2), -537), math.Ldexp(math.Nextafter(1, 0), 512), math.Inf(1), math.NaN()}
	var xs, ys []float64
	for _, a := range edges {
		for _, b := range edges {
			for _, s := range [][2]float64{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
				xs, ys = append(xs, s[0]*a), append(ys, s[1]*b)
			}
		}
	}
	rng := rand.New(rand.NewPCG(20261128, 10))
	for i := 0; i < 40000; i++ {
		xs, ys = append(xs, math.Float64frombits(rng.Uint64())), append(ys, math.Float64frombits(rng.Uint64()))
	}
	// Pairs whose product or quotient stands near either end of the
	// range, where the far paths decide.
	for i := 0; i < 40000; i++ {
		x := math.Float64frombits(rng.Uint64())
		if math.IsNaN(x) || math.IsInf(x, 0) || x == 0 {
			continue
		}
		end := []float64{math.MaxFloat64, math.SmallestNonzeroFloat64, math.Ldexp(1, -1075), math.Ldexp(1, -1022)}[i%4]
		near := end * (1 + (rng.Float64()-0.5)*math.Ldexp(1, -40+int(rng.IntN(40))))
		xs, ys = append(xs, x, x), append(ys, near/x, x/near)
	}
	rows, err := db.Query(`SELECT fado_add(x, y), fado_add(x, -(y)), fado_mul(x, y), fado_div(x, y)
		FROM unnest(CAST(? AS float8[]), CAST(? AS float8[])) WITH ORDINALITY AS t(x, y, i) ORDER BY i`, xs, ys)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	same := func(got sql.NullFloat64, want float64) bool {
		return got.Valid && (got.Float64 == want || math.IsNaN(got.Float64) && math.IsNaN(want))
	}
	i, bad := 0, 0
	for rows.Next() {
		var sum, diff, prod, quot sql.NullFloat64
		if err := rows.Scan(&sum, &diff, &prod, &quot); err != nil {
			t.Fatal(err)
		}
		x, y := xs[i], ys[i]
		i++
		fail := func(op string, got sql.NullFloat64, want float64) {
			if bad++; bad <= 20 {
				t.Errorf("%v %s %v: got %v, want %v", x, op, y, got, want)
			}
		}
		if !same(sum, x+y) {
			fail("+", sum, x+y)
		}
		if !same(diff, x-y) {
			fail("-", diff, x-y)
		}
		if !same(prod, x*y) && !lastStep(x*y) {
			fail("*", prod, x*y)
		}
		switch {
		case y == 0:
			if quot.Valid {
				fail("/", quot, math.NaN())
			}
		case !same(quot, x/y) && !lastStep(x/y):
			fail("/", quot, x/y)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(xs) {
		t.Fatalf("%d rows for %d pairs", i, len(xs))
	}
	t.Logf("%d pairs, %d differences", i, bad)
}

// lastStep says a result is the least number a float holds: where the
// true product or quotient is within one part in 2^53 above half of
// it, Go rounds it up to that number and the dialect answers zero. It
// is the one place the two may differ, and the test lets it by.
func lastStep(want float64) bool { return math.Abs(want) == math.SmallestNonzeroFloat64 }

// The exponential and the power, which each engine's library rounds
// its own way (ADR-0019): finite where Go's is finite, and within the
// tolerance of it; an infinity or a NaN where Go's is, and never a
// failed statement.
func TestPowerAndExp_NeverFail(t *testing.T) {
	db := openPostgres(t)
	vals := []float64{0, 1, -1, 2, -2, 0.5, -0.5, 10, -10, 1e-7, 3, -3, 1.5, 709, 709.78, 709.79, 710, -708, -744, -745, -745.2, -746, 1023, 1024, -1074, -1075, -1076, 1e15, 1e100, 1e300, -1e300,
		308, 309, -323, -324, -325, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN(), 0.3333333333333333, 2.5, -2.5, 1e-300, 1 + 1e-9, 1 - 1e-9}
	var xs, ys []float64
	for _, a := range vals {
		for _, b := range vals {
			xs, ys = append(xs, a), append(ys, b)
		}
	}
	rng := rand.New(rand.NewPCG(20261128, 11))
	for i := 0; i < 20000; i++ {
		xs, ys = append(xs, math.Float64frombits(rng.Uint64())), append(ys, (rng.Float64()-0.5)*4000)
	}
	rows, err := db.Query(`SELECT fado_power(x, y), fado_exp(y), fado_ln(x), fado_log10(x)
		FROM unnest(CAST(? AS float8[]), CAST(? AS float8[])) WITH ORDINALITY AS t(x, y, i) ORDER BY i`, xs, ys)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	finite := func(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
	// near says two results agree: both no number, or within the
	// tolerance, or both under the least a float holds times a thousand,
	// where the last digits of each library show.
	near := func(got, want float64) bool {
		if !finite(want) || !finite(got) {
			return !finite(want) && !finite(got)
		}
		if math.Abs(want) < 1e-320 && math.Abs(got) < 1e-320 {
			return true
		}
		return math.Abs(got-want) <= 1e-12*math.Max(math.Abs(want), math.SmallestNonzeroFloat64)
	}
	i, bad := 0, 0
	for rows.Next() {
		var pow, exp, ln, log sql.NullFloat64
		if err := rows.Scan(&pow, &exp, &ln, &log); err != nil {
			t.Fatal(err)
		}
		x, y := xs[i], ys[i]
		i++
		// At the very end of the range one library's infinity is the
		// other's greatest number: those are let by.
		edge := func(want float64) bool { return math.Abs(want) > 1e308 }
		// Go's own logarithm takes a number under the least normal one
		// for that one, 2^-1022, where the C library reads it rightly;
		// what Go answers of such a number is no measure, here or in a
		// power of it.
		tiny := x != 0 && math.Abs(x) < 2.2250738585072014e-308
		if !pow.Valid {
			t.Fatalf("%v ^ %v: no answer", x, y)
		}
		if want := math.Pow(x, y); !tiny && !near(pow.Float64, want) && !edge(want) && !edge(pow.Float64) {
			if bad++; bad <= 20 {
				t.Errorf("%v ^ %v: got %v, want %v", x, y, pow, want)
			}
		}
		if want := math.Exp(y); !exp.Valid || !near(exp.Float64, want) && !edge(want) && !edge(exp.Float64) {
			if bad++; bad <= 20 {
				t.Errorf("exp(%v): got %v, want %v", y, exp, want)
			}
		}
		if x > 0 {
			if !ln.Valid || !log.Valid || !tiny && (!near(ln.Float64, math.Log(x)) || !near(log.Float64, math.Log10(x))) {
				if bad++; bad <= 20 {
					t.Errorf("ln(%v), log10: got %v and %v, want %v and %v", x, ln, log, math.Log(x), math.Log10(x))
				}
			}
		} else if !math.IsNaN(x) && (ln.Valid || log.Valid) {
			if bad++; bad <= 20 {
				t.Errorf("ln(%v), log10: got %v and %v, want NULL", x, ln, log)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(xs) {
		t.Fatalf("%d rows for %d pairs", i, len(xs))
	}
	t.Logf("%d pairs, %d differences", i, bad)
}
