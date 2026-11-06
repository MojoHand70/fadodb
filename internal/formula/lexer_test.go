package formula

import (
	"strings"
	"testing"
)

func kinds(toks []Token) []TokenKind {
	out := make([]TokenKind, len(toks))
	for i, t := range toks {
		out[i] = t.Kind
	}
	return out
}

func TestLex_Tokens(t *testing.T) {
	cases := []struct {
		src  string
		want []TokenKind
	}{
		{"1 + 2", []TokenKind{TokNumber, TokPlus, TokNumber, TokEOF}},
		{"a<>b<=c>=d<e>f=g", []TokenKind{TokIdent, TokNe, TokIdent, TokLe, TokIdent, TokGe, TokIdent, TokLt, TokIdent, TokGt, TokIdent, TokEq, TokIdent, TokEOF}},
		{`ROUND(x, 2) * 10% & "s" \ 2 ^ 3`, []TokenKind{TokIdent, TokLParen, TokIdent, TokComma, TokNumber, TokRParen, TokStar, TokNumber, TokPercent, TokAmp, TokText, TokBackslash, TokNumber, TokCaret, TokNumber, TokEOF}},
		{"if a then b else c", []TokenKind{TokIf, TokIdent, TokThen, TokIdent, TokElse, TokIdent, TokEOF}},
		{"NOT a AND b OR True or FALSE blank This.x", []TokenKind{TokNot, TokIdent, TokAnd, TokIdent, TokOr, TokTrue, TokOr, TokFalse, TokBlank, TokThis, TokDot, TokIdent, TokEOF}},
		{"lines.qty", []TokenKind{TokIdent, TokDot, TokIdent, TokEOF}},
		{"", []TokenKind{TokEOF}},
		{"   \t\n ", []TokenKind{TokEOF}},
	}
	for _, c := range cases {
		toks, err := Lex(c.src)
		if err != nil {
			t.Errorf("Lex(%q): %v", c.src, err)
			continue
		}
		got := kinds(toks)
		if len(got) != len(c.want) {
			t.Errorf("Lex(%q) = %v, want %v", c.src, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Lex(%q)[%d] = %v, want %v", c.src, i, got[i], c.want[i])
			}
		}
	}
}

func TestLex_Numbers(t *testing.T) {
	cases := map[string]float64{
		"42": 42, "3.14": 3.14, "1.5e3": 1500, "2.5E-2": 0.025, ".5": 0.5, "0": 0,
		"1.": 1, "1e0": 1, "007": 7, "1.e2": 100,
	}
	for src, want := range cases {
		toks, err := Lex(src)
		if err != nil {
			t.Errorf("Lex(%q): %v", src, err)
			continue
		}
		if toks[0].Kind != TokNumber || toks[0].Num != want || len(toks) != 2 {
			t.Errorf("Lex(%q) = %+v, want one number %v", src, toks, want)
		}
	}
}

func TestLex_Text(t *testing.T) {
	cases := map[string]string{
		`"abc"`:      "abc",
		`""`:         "",
		`""""`:       `"`,
		`""""""`:     `""`,
		`"it""s"`:    `it"s`,
		`"say 'hi'"`: "say 'hi'",
		`"żółw"`:     "żółw",
		`"a b  c"`:   "a b  c",
	}
	for src, want := range cases {
		toks, err := Lex(src)
		if err != nil {
			t.Errorf("Lex(%s): %v", src, err)
			continue
		}
		if toks[0].Kind != TokText || toks[0].Text != want || len(toks) != 2 {
			t.Errorf("Lex(%s) = %+v, want one text %q", src, toks, want)
		}
	}
}

func TestLex_NamesAndKeywords(t *testing.T) {
	toks, err := Lex("żółw _x x1 Year AND and And")
	if err != nil {
		t.Fatal(err)
	}
	want := []Token{
		{Kind: TokIdent, Text: "żółw"}, {Kind: TokIdent, Text: "_x"}, {Kind: TokIdent, Text: "x1"},
		{Kind: TokIdent, Text: "Year"}, {Kind: TokAnd, Text: "AND"}, {Kind: TokAnd, Text: "and"}, {Kind: TokAnd, Text: "And"},
	}
	for i, w := range want {
		if toks[i].Kind != w.Kind || toks[i].Text != w.Text {
			t.Errorf("token %d = %+v, want %+v", i, toks[i], w)
		}
	}
}

func TestLex_Positions(t *testing.T) {
	toks, err := Lex(`żółw + "x"`)
	if err != nil {
		t.Fatal(err)
	}
	// żółw is 7 bytes: positions are byte offsets.
	if toks[1].Pos != 8 || toks[2].Pos != 10 {
		t.Errorf("positions = %d, %d; want 8, 10", toks[1].Pos, toks[2].Pos)
	}
}

func TestLex_Errors(t *testing.T) {
	cases := []struct {
		src     string
		wantMsg string
		wantCol int
	}{
		{`"abc`, "unterminated text literal", 1},
		{`"it""s`, "unterminated text literal", 1},
		{`'abc'`, "text literals use double quotes", 1},
		{`1 + #`, `unexpected character '#'`, 5},
		{`żółw!`, `unexpected character '!'`, 5},
		{`1e`, `malformed number "1e"`, 1},
		{`1e+`, `malformed number "1e+"`, 1},
		{`12abc`, `malformed number "12a"`, 1},
		{`1e999`, `number 1e999 is out of range`, 1},
		{"x + \"a\xffb\"", "invalid UTF-8", 7},
	}
	for _, c := range cases {
		_, err := Lex(c.src)
		if err == nil {
			t.Errorf("Lex(%q): no error, want %q", c.src, c.wantMsg)
			continue
		}
		e, ok := err.(*Error)
		if !ok {
			t.Errorf("Lex(%q): error %T, want *Error", c.src, err)
			continue
		}
		if !strings.Contains(e.Msg, c.wantMsg) || e.Col != c.wantCol {
			t.Errorf("Lex(%q) = %q at column %d, want %q at column %d", c.src, e.Msg, e.Col, c.wantMsg, c.wantCol)
		}
		if !strings.HasSuffix(err.Error(), "at column "+itoa(c.wantCol)) {
			t.Errorf("Lex(%q).Error() = %q, want the column suffix", c.src, err.Error())
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func BenchmarkLex(b *testing.B) {
	src := `IF(qty > 0 AND NOT ISBLANK(price), ROUND(qty * price * (1 - discount%), 2), 0)`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Lex(src); err != nil {
			b.Fatal(err)
		}
	}
}
