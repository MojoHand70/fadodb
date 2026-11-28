package postgres

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/formula/fuzz"
)

// The functions that read a text or a number are held to the VM over
// the inputs a careless or a hostile caller gives: texts that are
// nearly numbers, dates and JSON, patterns that are no patterns,
// numbers at the ends of the range. On each, Postgres must answer what
// the VM answers, value or error code, and no statement may fail.
func TestHostileInputs_AsTheVM(t *testing.T) {
	db := openPostgres(t)
	d := formula.NewPostgres().WithLimits(formula.Limits{MaxText: 4096})
	vm := formula.NewVM()
	vm.Limits.MaxText = 4096

	texts := []string{"", " ", "0", "-0", "+0", "1", "-1", "1.5", "+1.5", ".5", "5.", ".", "+", "-", "e5", "1e", "1e+", "1e5", "1E5", "1e-5", "1e+05", "--1", "1 2", "0x10",
		"Infinity", "NaN", "inf", "1_000", "１２３", " 12 ", "\t12\n", "\u00a012\u3000", "12\u200b", "1e308", "1.7976931348623157e308", "1.7976931348623159e308", "1.8e308", "1e309", "1e400", "1e999999999", "1e-999999999",
		"1e-323", "1e-324", "2.4703282292062327e-324", "2.4703282292062328e-324", "4.9e-324", "1e-400", "0.1234567890123456789", "123456789012345678901234567890", strings.Repeat("9", 400), "0." + strings.Repeat("0", 400) + "1",
		strings.Repeat("9", 400) + "e-400", "0." + strings.Repeat("0", 400) + "1e401", "1e00000000000000000000005", "1e-00000000000000000000005", "0e999999999999999999999", "0.000e-999999999999",
		"9223372036854775807", "9223372036854775808", "-9223372036854775808", "-9223372036854775809", "92233720368547758.07", "92233720368547758.08", "0.000000000000000001", "0.0000000000000000001", "1e-18", "1e-19", "1e18", "1e19", "1e20",
		"12.30", "12.3", "12.345", "0.30", "1e3", "1.50e1", "12.30 PLN", "12.30  PLN", " 12.30 PLN ", "12.30 pln", "12.30 PLNX", "PLN", " PLN", "12.30\tPLN", "1e2 EUR", "abc USD", "-0.005 USD", "12.30 ŻŁO",
		"2024-02-29", "2023-02-29", "2024-02-30", "2024-13-01", "2024-00-10", "2024-01-00", "0000-01-01", "0001-01-01", "9999-12-31", "10000-01-01", "2024-1-1", "24-01-01", " 2024-02-29 ", "2024-02-29T10:00:00",
		"2024-02-29 10:00:00", "2024-02-29 24:00:00", "2024-02-29 23:59:60", "2024-02-29 9:05:07", "2024-02-29 10:00", "2024-02-29 10:00:00.5", "2024-02-29 10:00:00,25", "2024-02-29  10:00:00", "2024-02-29 10:00:00Z",
		"abc", "ABC", "aBc DeF", "żółć gęślą jaźń", "ŻÓŁĆ", "straße", "İstanbul", "ǆ", "ﬁn", "o'neil mc-donald", "1st 2nd", "日本語 text", "a\u0301b", "ΑΒΓ αβγ ς", "  two  spaces  ", "tab\there", "a%b c&d=é/ü?", "~._-",
		"{", "[", "(", ")", "*", "a{2,1}", "a**", "(?i)é", `\`, `\d+`, `\w+`, `\s`, `\b`, `[[:alpha:]]+`, "ab|a", "(a)|b", "^", "$", "^$", ".", ".*", "x*", "(?:a)", "[a-z", "a+?", "[^a]",
		`{"a":1}`, `{"a":"x","b":true,"c":null,"d":[1,2,{"e":-0.5}],"f":{"g":1e21}}`, `[10,20,30]`, `"text"`, `12`, `-0`, `1e400`, `{"a":1e400}`, `null`, `true`, `nul`, `{"a":1,}`, `{'a':1}`, `{"a":1} x`, ` {"a" : 1 } `,
		`{"a":"\u0041\u00e9"}`, `{"a":"\ud83d\ude00 😀"}`, `{"a":"\n\t\"\\\/"}`, `{"a":1,"a":2}`, `{"":7}`, `{"a.b":1,"a":{"b":2}}`, `[[1,2],[3,4]]`, `{"a":1.0}`, `{"a":100000000000000000000}`, `{"a":0.1}`, `{"a":1E2}`,
	}
	numbers := []float64{0, 1, -1, 0.5, -0.5, 2.5, -2.5, 3.5, 0.1, 1e-7, 0.49999999999999994, 1.005, 2.675, 9.995, 12.5, 123456.789, 1e9, 2147483647, 2147483648, 4294967296, 1e15, 1e16, 4503599627370496, 9007199254740993,
		9.223372036854775e18, 9.223372036854776e18, -9.223372036854776e18, 1e19, 1e22, 1e23, 1e100, 1e150, 1e300, -1e300, math.MaxFloat64, -math.MaxFloat64, 1e-150, 1e-300, 1e-310, 5e-324, 86400, 2932896, -719162, 253402300799, -62135596800, 1e6, 1e7, 1e8, 12, 13, 31, 32, 365, 400, 9999, 10000, 18, 19, 300, 301, 323, 324}
	paths := []string{"", "a", "b", "c", "d", "d.0", "d.2", "d.2.e", "d.3", "d.-1", "d.01", "d.+1", "d. 1", "f.g", "f", "a.b", "0", "1", "3", "x", "a.", ".a", "..", "0.1"}

	textFormulas := []string{`VALUE(t1)`, `TEXT(VALUE(t1))`, `DECIMALVALUE(t1, 0)`, `DECIMALVALUE(t1, 2)`, `DECIMALVALUE(t1, 18)`, `MONEYVALUE(t1, 2)`, `MONEYVALUE(t1, 0)`, `CURRENCY(MONEYVALUE(t1, 2))`, `MONEY(1.5, t1)`,
		`DATEVALUE(t1)`, `DATETIMEVALUE(t1)`, `TEXT(DATETIMEVALUE(t1))`, `UPPER(t1)`, `LOWER(t1)`, `PROPER(t1)`, `TRIM(t1)`, `LEN(t1)`, `URLENCODE(t1)`, `REGEXREPLACE("ab ab Ab é1 x_y", t1, "<>")`, `REGEXREPLACE(t1, "a", "\1&")`,
		`REGEXREPLACE(t1, "[0-9]*", "#")`, `FINDFIRST("a", t1)`, `FINDLAST("a", t1)`, `FINDLAST("", t1)`, `LEFT(t1, 2)`, `RIGHT(t1, 2)`, `MID(t1, 2, 3)`, `REPEAT(t1, 3)`, `SUBSTITUTE(t1, "a", "bb")`, `SUBSTITUTE(t1, "", "x")`,
		`REPLACE(t1, 2, 1, "é")`, `PAD(t1, 6, "é")`, `PAD(t1, 6)`, `t1 & "|" & t1`, `ISBLANK(VALUE(t1))`, `IFERROR(VALUE(t1), -1)`, `IFERROR(DECIMALVALUE(t1, 4), DECIMAL(-1, 4))`}
	twoTexts := []string{`t1 < t2`, `t1 <= t2`, `t1 = t2`, `t1 > t2`, `IF(t1 < t2, t1, t2)`, `FINDFIRST(t1, t2)`, `SUBSTITUTE(t2, t1, "-")`}
	numberFormulas := []string{`n1 + n2`, `n1 - n2`, `n1 * n2`, `n1 / n2`, `n1 \ n2`, `n1 ^ n2`, `n1%`, `-n1`, `ROUND(n1)`, `ROUND(n1, 2)`, `ROUND(n1, -2)`, `ROUND(n1, 2, "half_even")`, `ROUND(n1, 0, "up")`, `ROUND(n1, n2)`, `FLOOR(n1, 1)`,
		`FLOOR(n1, n2)`, `CEILING(n1)`, `CEILING(n1, n2)`, `TRUNC(n1, 3)`, `TRUNC(n1, n2)`, `MOD(n1, n2)`, `SQRT(n1)`, `EXP(n1)`, `LN(n1)`, `LOG(n1)`, `LOG(n1, n2)`, `ABS(n1)`, `SIGN(n1)`, `POWER(n1, 0.5)`, `DECIMAL(n1, 2)`, `DECIMAL(n1, 0, "half_even")`,
		`DECIMAL(n1, 18)`, `DECIMAL(n1, 4, "up")`, `DECIMAL(n1, 4, "down")`, `DECIMAL(n1, 4, "floor")`, `DECIMAL(n1, 4, "ceiling")`, `DATE(n1, n2, 1)`, `DATE(2024, n1, n2)`, `DATE(n1, 1, n2)`, `ADDDAYS(d1, n1)`, `ADDDAYS(s1, n1)`, `ADDMONTHS(d1, n1)`,
		`ADDMONTHS(s1, n1)`, `ADDYEARS(d1, n1)`, `ADDYEARS(s1, n1)`, `TEXT(n1)`, `TEXT(n1 * n2)`, `LEFT("abcdef", n1)`, `RIGHT("abcdef", n1)`, `MID("abcdef", n1, n2)`, `MID("abcdef", n1)`, `REPEAT("ab", n1)`, `PAD("ab", n1)`, `REPLACE("abcdef", n1, n2, "X")`,
		`YEAR(ADDDAYS(d1, n1))`, `MONTH(ADDDAYS(s1, n1))`, `TEXT(ADDDAYS(s1, n1))`, `TEXT(ADDDAYS(d1, n1))`, `DAYS(ADDDAYS(d1, n1), d1)`, `MIN(n1, n2)`, `MAX(n1, n2)`, `SUM(n1, n2, n1)`, `AVERAGE(n1, n2)`, `n1 < n2`, `IF(n1 > n2, n1, n2) * 2`,
		`DATEOF(ADDDAYS(s1, n1))`, `DATETIME(ADDDAYS(d1, n1))`, `MONTHSBETWEEN(d1, ADDDAYS(d1, n1))`, `YEARSBETWEEN(s1, ADDDAYS(s1, n1))`, `NUMBER(DECIMAL(n1, 2)) + n2`, `DECIMAL(n1, 2) * DECIMAL(n2, 2)`, `DECIMAL(n1, 2) / DECIMAL(n2, 2)`,
		`ROUND(DECIMAL(n1, 4), 2)`, `ROUND(DECIMAL(n1, 4), -2)`, `MOD(DECIMAL(n1, 2), DECIMAL(n2, 2))`, `TEXT(DECIMAL(n1, 3))`, `MONEY(DECIMAL(n1, 2), "PLN") + MONEY(DECIMAL(n2, 2), "PLN")`}

	index := map[string]int{}
	for i, v := range fuzz.Variables {
		index[v.Name] = i
	}
	base := make([]formula.Value, len(fuzz.Variables))
	base[index["d1"]], base[index["s1"]] = formula.Date(19782), formula.DateTime(1709200800)
	total, bad := 0, 0
	run := func(src string, set map[string]formula.Value, skip func(*formula.Program) bool) {
		t.Helper()
		in := append([]formula.Value(nil), base...)
		for name, v := range set {
			in[index[name]] = v
		}
		f := &fuzz.Formula{Src: src, Inputs: in, Ctx: formula.Context{Now: 1764288000}}
		o := fuzz.Run(f, vm, db, d)
		if o.BindErr != nil {
			t.Fatalf("%s: %v", src, o.BindErr)
		}
		if skip != nil && skip(o.Prog) {
			return
		}
		total++
		if !o.Agree() {
			if bad++; bad <= 60 {
				t.Errorf("%s with %s\n    %s", src, describeInputs(set), o.Reason)
			}
		}
	}
	for _, src := range textFormulas {
		for _, s := range texts {
			// A text of JSON with a character written by its number is,
			// as a pattern, one Postgres reads and Go does not:
			// TestKnownDifferences has that.
			if strings.Contains(src, `, t1, "<>")`) && strings.Contains(s, `\u`) {
				continue
			}
			run(src, map[string]formula.Value{"t1": formula.Text(s)}, nil)
		}
	}
	// As JSON, every text but the numbers whose exponent Postgres's
	// numeric cannot hold, which TestKnownDifferences has.
	for _, src := range []string{`JSONVALUE(j1, "a")`, `JSONVALUE(j1, "")`, `JSONVALUE(j1, "d.2.e")`} {
		for _, s := range texts {
			if strings.Contains(s, "999999999") {
				continue
			}
			run(src, map[string]formula.Value{"j1": formula.JSON(s)}, nil)
		}
	}
	for _, src := range twoTexts {
		for i, a := range texts {
			run(src, map[string]formula.Value{"t1": formula.Text(a), "t2": formula.Text(texts[(i*7+3)%len(texts)])}, nil)
			run(src, map[string]formula.Value{"t1": formula.Text(a), "t2": formula.Text(a)}, nil)
		}
	}
	for _, p := range paths {
		for _, s := range texts[len(texts)-27:] {
			run(`JSONVALUE(j1, t1)`, map[string]formula.Value{"t1": formula.Text(p), "j1": formula.JSON(s)}, nil)
		}
	}
	// Go's logarithm misreads a number under the least normal one, so
	// what the VM answers of a logarithm or a power of one is no measure.
	tiny := func(f float64) bool { return f != 0 && math.Abs(f) < 2.2250738585072014e-308 }
	for _, src := range numberFormulas {
		for i, a := range numbers {
			for _, b := range []float64{numbers[(i*5+2)%len(numbers)], numbers[(i*11+7)%len(numbers)], a, 2, -3, 0} {
				a, b := a, b
				run(src, map[string]formula.Value{"n1": formula.Number(a), "n2": formula.Number(b)}, func(p *formula.Program) bool { return fuzz.Noisy(p) && (tiny(a) || tiny(b)) })
			}
		}
	}
	t.Logf("%d formulas with hostile inputs, %d differences", total, bad)
}

func describeInputs(set map[string]formula.Value) string {
	var parts []string
	for _, name := range []string{"t1", "t2", "j1", "n1", "n2"} {
		if v, ok := set[name]; ok {
			s := v.String()
			if len(s) > 80 {
				s = s[:80] + "..."
			}
			parts = append(parts, fmt.Sprintf("%s=%q", name, s))
		}
	}
	return strings.Join(parts, " ")
}

// What Postgres answers otherwise than the VM, and cannot be made to
// answer as it does: each is written down here with both answers, so
// that a change of either is seen. conformance/README.md says them in
// words.
func TestKnownDifferences(t *testing.T) {
	db := openPostgres(t)
	d := formula.NewPostgres()
	vm := formula.NewVM()
	index := map[string]int{}
	for i, v := range fuzz.Variables {
		index[v.Name] = i
	}
	for _, tc := range []struct {
		why, src string
		set      map[string]formula.Value
		reason   string
	}{
		{"a pattern's alternatives: Go takes the first that matches, Postgres the longest",
			`REGEXREPLACE("ab ab", "a|ab", "<>")`, nil, `VM text "<>b <>b", SQL text "<> <>"`},
		{"a named group is Go's way of writing; Postgres has none",
			`REGEXREPLACE("ab", "(?P<n>a)", "<>")`, nil, `VM text "<>b", SQL error bad_text`},
		{"a class of Unicode is Go's way of writing",
			`REGEXREPLACE("ab1", t1, "<>")`, map[string]formula.Value{"t1": formula.Text(`\pL`)}, `VM text "<><>1", SQL error bad_text`},
		{"a character by its number in braces is Go's way of writing",
			`REGEXREPLACE("Ab", t1, "<>")`, map[string]formula.Value{"t1": formula.Text(`\x{41}`)}, `VM text "<>b", SQL error bad_text`},
		{"a back reference is Postgres's and not Go's",
			`REGEXREPLACE("aab", t1, "<>")`, map[string]formula.Value{"t1": formula.Text(`(a)\1`)}, `VM error bad_text, SQL text "<>b"`},
		{"a look ahead is Postgres's and not Go's",
			`REGEXREPLACE("ab", t1, "<>")`, map[string]formula.Value{"t1": formula.Text("a(?=b)")}, `VM error bad_text, SQL text "<>b"`},
		{"a character by its number without braces is Postgres's and not Go's",
			`REGEXREPLACE("Ab", t1, "<>")`, map[string]formula.Value{"t1": formula.Text(`\u0041`)}, `VM error bad_text, SQL text "<>b"`},
		{"a dot is any character to Postgres, a line's end too",
			`REGEXREPLACE(t1, "a.b", "<>")`, map[string]formula.Value{"t1": formula.Text("a\nb")}, `VM text "a\nb", SQL text "<>"`},
		{"a letter, to PROPER: Postgres takes the character of a Roman numeral for one, Go does not",
			`PROPER(t1)`, map[string]formula.Value{"t1": formula.Text("Ⅳa")}, `VM text "ⅣA", SQL text "Ⅳa"`},
		{"a text of Postgres cannot hold the character of number zero, so JSON that writes one is none",
			`JSONVALUE(j1, "a")`, map[string]formula.Value{"j1": formula.JSON(`{"a":"\u0000","b":1}`)}, `VM text "\x00", SQL error bad_text`},
		{"a number in JSON with an exponent past what numeric holds is no JSON to Postgres",
			`JSONVALUE(j1, "")`, map[string]formula.Value{"j1": formula.JSON(`1e-999999999`)}, `VM text "0", SQL error bad_text`},
	} {
		in := make([]formula.Value, len(fuzz.Variables))
		for name, v := range tc.set {
			in[index[name]] = v
		}
		o := fuzz.Run(&fuzz.Formula{Src: tc.src, Inputs: in, Ctx: formula.Context{Now: 1764288000}}, vm, db, d)
		if o.BindErr != nil {
			t.Fatalf("%s: %v", tc.src, o.BindErr)
		}
		if o.Reason != tc.reason {
			t.Errorf("%s: %s\n    got  %s\n    want %s", tc.why, tc.src, o.Reason, tc.reason)
		}
	}
}
