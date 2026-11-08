package formula

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TokenKind is the kind of a lexical token.
type TokenKind uint8

// Token kinds. Keywords are case-insensitive and reserved (ADR-0019).
const (
	TokEOF TokenKind = iota
	TokNumber
	TokText
	TokIdent

	TokAnd
	TokOr
	TokNot
	TokIf
	TokThen
	TokElse
	TokTrue
	TokFalse
	TokBlank
	TokThis

	TokLParen
	TokRParen
	TokComma
	TokDot
	TokPlus
	TokMinus
	TokStar
	TokSlash
	TokBackslash
	TokCaret
	TokPercent
	TokAmp
	TokEq
	TokNe
	TokLt
	TokLe
	TokGt
	TokGe
)

// IsKeyword reports whether a name, in any case, is a keyword of the
// language and so can never name a variable, a field, a column or a table
// (ADR-0019).
func IsKeyword(name string) bool {
	_, ok := keywords[strings.ToUpper(name)]
	return ok
}

var keywords = map[string]TokenKind{
	"AND": TokAnd, "OR": TokOr, "NOT": TokNot, "IF": TokIf, "THEN": TokThen,
	"ELSE": TokElse, "TRUE": TokTrue, "FALSE": TokFalse, "BLANK": TokBlank,
	"THIS": TokThis,
}

var tokenNames = [...]string{
	TokEOF: "end of formula", TokNumber: "number", TokText: "text", TokIdent: "name",
	TokAnd: "AND", TokOr: "OR", TokNot: "NOT", TokIf: "IF", TokThen: "THEN",
	TokElse: "ELSE", TokTrue: "TRUE", TokFalse: "FALSE", TokBlank: "BLANK", TokThis: "THIS",
	TokLParen: "'('", TokRParen: "')'", TokComma: "','", TokDot: "'.'",
	TokPlus: "'+'", TokMinus: "'-'", TokStar: "'*'", TokSlash: "'/'", TokBackslash: "'\\'",
	TokCaret: "'^'", TokPercent: "'%'", TokAmp: "'&'", TokEq: "'='", TokNe: "'<>'",
	TokLt: "'<'", TokLe: "'<='", TokGt: "'>'", TokGe: "'>='",
}

func (k TokenKind) String() string {
	if int(k) < len(tokenNames) && tokenNames[k] != "" {
		return tokenNames[k]
	}
	return fmt.Sprintf("token(%d)", k)
}

// Token is one lexical token with its position in the formula.
type Token struct {
	Kind TokenKind
	// Text is the name as written for a name, the unescaped value for a
	// text literal, and the source text otherwise.
	Text string
	// Num is the value of a number literal.
	Num float64
	// Pos is the byte offset of the token in the formula.
	Pos int
}

// describe names a token for an error message.
func describe(t Token) string {
	switch t.Kind {
	case TokIdent:
		return fmt.Sprintf("name %q", t.Text)
	case TokText:
		return fmt.Sprintf("text %q", t.Text)
	case TokNumber:
		return "number " + t.Text
	}
	return t.Kind.String()
}

// Error is a lexical or syntax error at a position in a formula.
type Error struct {
	// Pos is the byte offset of the error in the formula.
	Pos int
	// Col is the 1-based column in characters, for people.
	Col int
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at column %d", e.Msg, e.Col)
}

func newError(src string, pos int, format string, args ...any) *Error {
	if pos > len(src) {
		pos = len(src)
	}
	return &Error{Pos: pos, Col: utf8.RuneCountInString(src[:pos]) + 1, Msg: fmt.Sprintf(format, args...)}
}
