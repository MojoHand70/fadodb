package formula

import (
	"strconv"
	"strings"
)

// Node is a node of a parsed formula. The parser produces it; the binder
// turns it into bytecode. Function names in a Call are canonical upper-case
// catalog names; variable names are kept as written and resolved
// case-insensitively by the binder.
type Node interface {
	// Pos is the byte offset of the node in the formula: the operator for
	// operators, the start otherwise.
	Pos() int
}

// NumberLit is a number literal.
type NumberLit struct {
	Value float64
	P     int
}

// TextLit is a text literal.
type TextLit struct {
	Value string
	P     int
}

// BoolLit is TRUE or FALSE.
type BoolLit struct {
	Value bool
	P     int
}

// BlankLit is the BLANK literal.
type BlankLit struct {
	P int
}

// Ident is a variable, or inside LOOKUP and QUERY a source column.
type Ident struct {
	Name string
	P    int
}

// Member is base.name: one column of a rows variable as an array.
type Member struct {
	Base string
	Name string
	P    int
}

// ThisRef is THIS.name: the document's variable inside LOOKUP and QUERY.
type ThisRef struct {
	Name string
	P    int
}

// Unary is -x, NOT x, or the postfix x%. Op is TokMinus, TokNot or
// TokPercent.
type Unary struct {
	Op TokenKind
	X  Node
	P  int
}

// Binary is an infix operation. Op is one of the arithmetic, text,
// comparison and logical operator kinds.
type Binary struct {
	Op   TokenKind
	L, R Node
	P    int
}

// Call is a function call by canonical name. IF, AND, OR and NOT never
// appear here: the parser turns them into If, Binary and Unary nodes.
type Call struct {
	Name string
	Args []Node
	P    int
}

// If is IF(c, a, b) or IF c THEN a ELSE b.
type If struct {
	Cond, Then, Else Node
	P                int
}

// Pos implements Node.
func (n *NumberLit) Pos() int { return n.P }

// Pos implements Node.
func (n *TextLit) Pos() int { return n.P }

// Pos implements Node.
func (n *BoolLit) Pos() int { return n.P }

// Pos implements Node.
func (n *BlankLit) Pos() int { return n.P }

// Pos implements Node.
func (n *Ident) Pos() int { return n.P }

// Pos implements Node.
func (n *Member) Pos() int { return n.P }

// Pos implements Node.
func (n *ThisRef) Pos() int { return n.P }

// Pos implements Node.
func (n *Unary) Pos() int { return n.P }

// Pos implements Node.
func (n *Binary) Pos() int { return n.P }

// Pos implements Node.
func (n *Call) Pos() int { return n.P }

// Pos implements Node.
func (n *If) Pos() int { return n.P }

var opText = map[TokenKind]string{
	TokPlus: "+", TokMinus: "-", TokStar: "*", TokSlash: "/", TokBackslash: `\`,
	TokCaret: "^", TokAmp: "&", TokEq: "=", TokNe: "<>", TokLt: "<", TokLe: "<=",
	TokGt: ">", TokGe: ">=", TokAnd: "AND", TokOr: "OR",
}

// Sexpr renders a node as an S-expression with every operation
// parenthesised, which makes precedence and associativity visible:
// -x ^ 2 is (^ (neg x) 2). It is the form the parser tests and the
// conformance tooling use; it is not the formula language.
func Sexpr(n Node) string {
	var b strings.Builder
	writeSexpr(&b, n)
	return b.String()
}

func writeSexpr(b *strings.Builder, n Node) {
	switch n := n.(type) {
	case *NumberLit:
		b.WriteString(strconv.FormatFloat(n.Value, 'g', -1, 64))
	case *TextLit:
		b.WriteString(strconv.Quote(n.Value))
	case *BoolLit:
		if n.Value {
			b.WriteString("TRUE")
		} else {
			b.WriteString("FALSE")
		}
	case *BlankLit:
		b.WriteString("BLANK")
	case *Ident:
		b.WriteString(n.Name)
	case *Member:
		b.WriteString("(. ")
		b.WriteString(n.Base)
		b.WriteByte(' ')
		b.WriteString(n.Name)
		b.WriteByte(')')
	case *ThisRef:
		b.WriteString("THIS.")
		b.WriteString(n.Name)
	case *Unary:
		switch n.Op {
		case TokMinus:
			b.WriteString("(neg ")
		case TokNot:
			b.WriteString("(not ")
		default:
			b.WriteString("(% ")
		}
		writeSexpr(b, n.X)
		b.WriteByte(')')
	case *Binary:
		b.WriteByte('(')
		b.WriteString(opText[n.Op])
		b.WriteByte(' ')
		writeSexpr(b, n.L)
		b.WriteByte(' ')
		writeSexpr(b, n.R)
		b.WriteByte(')')
	case *Call:
		b.WriteByte('(')
		b.WriteString(n.Name)
		for _, a := range n.Args {
			b.WriteByte(' ')
			writeSexpr(b, a)
		}
		b.WriteByte(')')
	case *If:
		b.WriteString("(IF ")
		writeSexpr(b, n.Cond)
		b.WriteByte(' ')
		writeSexpr(b, n.Then)
		b.WriteByte(' ')
		writeSexpr(b, n.Else)
		b.WriteByte(')')
	default:
		b.WriteString("?")
	}
}
