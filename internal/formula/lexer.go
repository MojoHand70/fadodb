package formula

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lex splits a formula into tokens. The last token is always TokEOF. The
// grammar is ADR-0019's: numbers, double-quoted text with "" for a quote,
// names, case-insensitive keywords, and the operator set + - * / \ ^ % &
// = <> < <= > >= with parentheses, commas and dots.
func Lex(src string) ([]Token, error) {
	lx := lexer{src: src}
	toks := make([]Token, 0, 16)
	for {
		t, err := lx.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, t)
		if t.Kind == TokEOF {
			return toks, nil
		}
	}
}

type lexer struct {
	src string
	pos int
}

func (lx *lexer) next() (Token, error) {
	for lx.pos < len(lx.src) {
		r, w := utf8.DecodeRuneInString(lx.src[lx.pos:])
		if !unicode.IsSpace(r) {
			break
		}
		lx.pos += w
	}
	if lx.pos >= len(lx.src) {
		return Token{Kind: TokEOF, Pos: lx.pos}, nil
	}
	start := lx.pos
	r, w := utf8.DecodeRuneInString(lx.src[lx.pos:])
	switch {
	case r == '"':
		return lx.text(start)
	case isDigit(r) || (r == '.' && lx.pos+1 < len(lx.src) && isDigit(rune(lx.src[lx.pos+1]))):
		return lx.number(start)
	case isIdentStart(r):
		return lx.ident(start)
	}
	if lx.pos+2 <= len(lx.src) {
		two := lx.src[lx.pos : lx.pos+2]
		var k TokenKind
		switch two {
		case "<>":
			k = TokNe
		case "<=":
			k = TokLe
		case ">=":
			k = TokGe
		}
		if k != TokEOF {
			lx.pos += 2
			return Token{Kind: k, Text: two, Pos: start}, nil
		}
	}
	var k TokenKind
	switch r {
	case '(':
		k = TokLParen
	case ')':
		k = TokRParen
	case ',':
		k = TokComma
	case '.':
		k = TokDot
	case '+':
		k = TokPlus
	case '-':
		k = TokMinus
	case '*':
		k = TokStar
	case '/':
		k = TokSlash
	case '\\':
		k = TokBackslash
	case '^':
		k = TokCaret
	case '%':
		k = TokPercent
	case '&':
		k = TokAmp
	case '=':
		k = TokEq
	case '<':
		k = TokLt
	case '>':
		k = TokGt
	case '\'':
		return Token{}, newError(lx.src, start, "unexpected '; text literals use double quotes")
	case utf8.RuneError:
		return Token{}, newError(lx.src, start, "invalid UTF-8")
	default:
		return Token{}, newError(lx.src, start, "unexpected character %q", r)
	}
	lx.pos += w
	return Token{Kind: k, Text: lx.src[start:lx.pos], Pos: start}, nil
}

func (lx *lexer) text(start int) (Token, error) {
	var b strings.Builder
	lx.pos++ // the opening quote
	for {
		if lx.pos >= len(lx.src) {
			return Token{}, newError(lx.src, start, "unterminated text literal")
		}
		if lx.src[lx.pos] == '"' {
			if lx.pos+1 < len(lx.src) && lx.src[lx.pos+1] == '"' {
				b.WriteByte('"')
				lx.pos += 2
				continue
			}
			lx.pos++
			return Token{Kind: TokText, Text: b.String(), Pos: start}, nil
		}
		r, w := utf8.DecodeRuneInString(lx.src[lx.pos:])
		if r == utf8.RuneError && w == 1 {
			return Token{}, newError(lx.src, lx.pos, "invalid UTF-8")
		}
		b.WriteString(lx.src[lx.pos : lx.pos+w])
		lx.pos += w
	}
}

func (lx *lexer) number(start int) (Token, error) {
	for lx.pos < len(lx.src) && isDigit(rune(lx.src[lx.pos])) {
		lx.pos++
	}
	if lx.pos < len(lx.src) && lx.src[lx.pos] == '.' {
		lx.pos++
		for lx.pos < len(lx.src) && isDigit(rune(lx.src[lx.pos])) {
			lx.pos++
		}
	}
	if lx.pos < len(lx.src) && (lx.src[lx.pos] == 'e' || lx.src[lx.pos] == 'E') {
		p := lx.pos + 1
		if p < len(lx.src) && (lx.src[p] == '+' || lx.src[p] == '-') {
			p++
		}
		if p >= len(lx.src) || !isDigit(rune(lx.src[p])) {
			return Token{}, newError(lx.src, start, "malformed number %q", lx.src[start:p])
		}
		for p < len(lx.src) && isDigit(rune(lx.src[p])) {
			p++
		}
		lx.pos = p
	}
	if lx.pos < len(lx.src) {
		if r, _ := utf8.DecodeRuneInString(lx.src[lx.pos:]); isIdentPart(r) {
			return Token{}, newError(lx.src, start, "malformed number %q", lx.src[start:lx.pos+1])
		}
	}
	text := lx.src[start:lx.pos]
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange) {
			return Token{}, newError(lx.src, start, "number %s is out of range", text)
		}
		return Token{}, newError(lx.src, start, "malformed number %q", text)
	}
	return Token{Kind: TokNumber, Text: text, Num: v, Pos: start}, nil
}

func (lx *lexer) ident(start int) (Token, error) {
	for lx.pos < len(lx.src) {
		r, w := utf8.DecodeRuneInString(lx.src[lx.pos:])
		if !isIdentPart(r) {
			break
		}
		lx.pos += w
	}
	text := lx.src[start:lx.pos]
	if k, ok := keywords[strings.ToUpper(text)]; ok {
		return Token{Kind: k, Text: text, Pos: start}, nil
	}
	return Token{Kind: TokIdent, Text: text, Pos: start}, nil
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
