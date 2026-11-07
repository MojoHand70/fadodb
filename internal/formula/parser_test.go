package formula

import (
	"strings"
	"testing"
)

func TestParse_Shapes(t *testing.T) {
	cases := []struct{ src, want string }{
		// Precedence and associativity (ADR-0019).
		{"1 + 2 * 3", "(+ 1 (* 2 3))"},
		{"(1 + 2) * 3", "(* (+ 1 2) 3)"},
		{"10 - 2 - 3", "(- (- 10 2) 3)"},
		{"100 / 10 / 2", "(/ (/ 100 10) 2)"},
		{`7 \ 2 * 2`, `(* (\ 7 2) 2)`},
		{"2 ^ 3 ^ 2", "(^ 2 (^ 3 2))"},
		{"-x ^ 2", "(^ (neg x) 2)"},
		{"2 ^ -1", "(^ 2 (neg 1))"},
		{"-2 ^ -2", "(^ (neg 2) (neg 2))"},
		{"--1", "(neg (neg 1))"},
		{"5%", "(% 5)"},
		{"-5%", "(neg (% 5))"},
		{"50%%", "(% (% 50))"},
		{"x% ^ 2", "(^ (% x) 2)"},
		{"a + b & c", "(& (+ a b) c)"},
		{"a & b = c", "(= (& a b) c)"},
		{"a < b < c", "(< (< a b) c)"},
		{"a <> b", "(<> a b)"},
		{"a<=b", "(<= a b)"},
		{"a>=b", "(>= a b)"},
		{"NOT a = b", "(not (= a b))"},
		{"NOT a AND b", "(AND (not a) b)"},
		{"a OR b AND c", "(OR a (AND b c))"},
		{"a AND b OR c", "(OR (AND a b) c)"},
		{"NOT NOT a", "(not (not a))"},
		{"a < 0 OR b > 0 AND a > 0", "(OR (< a 0) (AND (> b 0) (> a 0)))"},
		// IF in both forms.
		{"IF(a, 1, 2)", "(IF a 1 2)"},
		{"if(a, 1, 2)", "(IF a 1 2)"},
		{"IF a THEN 1 ELSE 2", "(IF a 1 2)"},
		{"IF (a) THEN 1 ELSE 2", "(IF a 1 2)"},
		{"IF(a) THEN 1 ELSE 2", "(IF a 1 2)"},
		{"IF(a > 0) THEN IF(b > 2) THEN 1 ELSE 2 ELSE 3", "(IF (> a 0) (IF (> b 2) 1 2) 3)"},
		{"IF a > 0 THEN IF b > 2 THEN 1 ELSE 2 ELSE 3", "(IF (> a 0) (IF (> b 2) 1 2) 3)"},
		{"1 + IF a THEN 2 ELSE 3 + 4", "(+ 1 (IF a 2 (+ 3 4)))"},
		{"1 + IF(a, 2, 3) + 4", "(+ (+ 1 (IF a 2 3)) 4)"},
		{"IF(IF(a, 1, 0) = 1, a, b)", "(IF (= (IF a 1 0) 1) a b)"},
		// Keyword functions in function form.
		{"AND(a, b, c)", "(AND (AND a b) c)"},
		{"OR(a, b)", "(OR a b)"},
		{"and(a)", "a"},
		{"NOT(a)", "(not a)"},
		{"NOT (a AND b)", "(not (AND a b))"},
		{"NOT(a > 0) AND b = 0", "(AND (not (> a 0)) (= b 0))"},
		{"NOT(a) <= b", "(<= (not a) b)"},
		{"NOT (a) <= b", "(<= (not a) b)"},
		{"NOT a <= b", "(not (<= a b))"},
		{"NOT(a) & NOT(b)", "(& (not a) (not b))"},
		// Calls, aliases, names.
		{"ROUND(x, 2)", "(ROUND x 2)"},
		{"round(x)", "(ROUND x)"},
		{"ceil(x)", "(CEILING x)"},
		{`find("a", b)`, `(FINDFIRST "a" b)`},
		{"NOW()", "(NOW)"},
		{"MIN(a, b, 1)", "(MIN a b 1)"},
		{"Year + YEAR(d)", "(+ Year (YEAR d))"},
		{"lines.qty", "(. lines qty)"},
		{"SUM(lines.qty * lines.price)", "(SUM (* (. lines qty) (. lines price)))"},
		{"THIS.code", "THIS.code"},
		{"this.Code", "THIS.Code"},
		{`LOOKUP(Rates, rate, code = THIS.code)`, `(LOOKUP Rates rate (= code THIS.code))`},
		{`QUERY(Invoice, total, state = "paid")`, `(QUERY Invoice total (= state "paid"))`},
		// Literals.
		{`"a" & "b"`, `(& "a" "b")`},
		{`"it""s"`, `"it\"s"`},
		{"1e3", "1000"},
		{".5", "0.5"},
		{"TRUE AND False", "(AND TRUE FALSE)"},
		{"x = BLANK", "(= x BLANK)"},
		{"blank", "BLANK"},
		{"żółw + 1", "(+ żółw 1)"},
		{"_x1 * 2", "(* _x1 2)"},
		{"1+2*3^4%", "(+ 1 (* 2 (^ 3 (% 4))))"},
	}
	for _, c := range cases {
		n, err := Parse(c.src)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.src, err)
			continue
		}
		if got := Sexpr(n); got != c.want {
			t.Errorf("Parse(%q) = %s, want %s", c.src, got, c.want)
		}
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		src     string
		wantMsg string
		wantCol int
	}{
		{"", "empty formula", 1},
		{"   ", "empty formula", 1},
		{"1 +", "unexpected end of formula", 4},
		{"+ 1", "unexpected '+'", 1},
		{"(1 + 2", "expected ')', got end of formula", 7},
		{"1 + 2)", "unexpected ')'", 6},
		{"1 2", "unexpected number 2", 3},
		{"a b", `unexpected name "b"`, 3},
		{"a.", "expected a column or field name after a.", 3},
		{"a.b.", "expected a column or field name after a.", 5},
		{"THIS", "THIS must be followed by .name", 1},
		{"THIS.", "THIS must be followed by .name", 6},
		{"THIS.1", "THIS must be followed by .name", 1}, // .1 lexes as a number
		{"1 == 2", "unexpected '='", 4},
		{"1 != 2", "unexpected character '!'", 3},
		{"a && b", "unexpected '&'", 4},
		{"FOO(1)", "unknown function FOO", 1},
		{"NOW(1)", "NOW takes no arguments, got 1", 1},
		{"ROUND()", "ROUND takes 1 to 3 arguments, got 0", 1},
		{"ROUND(x, 2, 3, 4)", "ROUND takes 1 to 3 arguments, got 4", 1},
		{"LEFT(Code)", "LEFT takes 2 arguments, got 1", 1},
		{"find(Code)", "FIND takes 2 arguments, got 1", 1},
		{"MIN()", "MIN takes at least 1 argument, got 0", 1},
		{"IF(a > 0, 1)", "IF takes 3 arguments, got 2", 1},
		{"IF(a > 0, 1, 2, 3)", "IF takes 3 arguments, got 4", 1},
		{"IF(a > 0 OR, 1, 0)", "unexpected ','", 12},
		{"IF a > 0 THEN 1", "expected ELSE, got end of formula", 16},
		{"IF a > 0 ELSE 1", "expected THEN, got ELSE", 10},
		{"IF(a > 0) THEN 1", "expected ELSE, got end of formula", 17},
		{"IF(a > 0) 1 ELSE 2", "IF needs three arguments or THEN ... ELSE, got number 1", 11},
		{"IF", "unexpected end of formula", 3},
		{"THEN", "unexpected THEN", 1},
		{"AND a", "unexpected AND", 1},
		{"a AND", "unexpected end of formula", 6},
		{"NOT", "unexpected end of formula", 4},
		{"TRUE()", "unexpected '('", 5},
		{"BLANK()", "unexpected '('", 6},
		{"()", "unexpected ')'", 2},
		{",", "unexpected ','", 1},
		{"ROUND(x,)", "unexpected ')'", 9},
		{"ROUND(,x)", "unexpected ','", 7},
		{"x ^", "unexpected end of formula", 4},
		{"^ x", "unexpected '^'", 1},
		{`"abc`, "unterminated text literal", 1},
		{"'abc'", "text literals use double quotes", 1},
	}
	for _, c := range cases {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("Parse(%q): no error, want %q", c.src, c.wantMsg)
			continue
		}
		e, ok := err.(*Error)
		if !ok {
			t.Errorf("Parse(%q): error %T, want *Error", c.src, err)
			continue
		}
		if !strings.Contains(e.Msg, c.wantMsg) || e.Col != c.wantCol {
			t.Errorf("Parse(%q) = %q at column %d, want %q at column %d", c.src, e.Msg, e.Col, c.wantMsg, c.wantCol)
		}
	}
}

func TestParse_Positions(t *testing.T) {
	n, err := Parse("ab + ROUND(cd, 2)")
	if err != nil {
		t.Fatal(err)
	}
	bin := n.(*Binary)
	if bin.Pos() != 3 {
		t.Errorf("operator position = %d, want 3", bin.Pos())
	}
	if bin.L.Pos() != 0 || bin.R.Pos() != 5 {
		t.Errorf("operand positions = %d, %d; want 0, 5", bin.L.Pos(), bin.R.Pos())
	}
	call := bin.R.(*Call)
	if call.Args[0].Pos() != 11 || call.Args[1].Pos() != 15 {
		t.Errorf("argument positions = %d, %d; want 11, 15", call.Args[0].Pos(), call.Args[1].Pos())
	}
}

func BenchmarkParse(b *testing.B) {
	src := `IF(qty > 0 AND NOT ISBLANK(price), ROUND(qty * price * (1 - discount%), 2), 0)`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(src); err != nil {
			b.Fatal(err)
		}
	}
}
