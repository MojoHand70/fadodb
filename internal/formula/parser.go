package formula

import "strings"

// Parse turns a formula into a syntax tree. It checks the grammar of
// ADR-0019 and every call against the catalog: the function must exist
// and the number of arguments must fit. Names, types and function classes
// are the binder's job.
//
// Precedence from highest to lowest: postfix %, unary -, ^ (right-
// associative), * / \, + -, &, comparisons, NOT, AND, OR. IF c THEN a
// ELSE b is an expression whose ELSE branch extends as far right as it
// can.
func Parse(src string) (Node, error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{src: src, toks: toks}
	if p.peek().Kind == TokEOF {
		return nil, newError(src, 0, "empty formula")
	}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.Kind != TokEOF {
		return nil, p.errAt(t.Pos, "unexpected %s", describe(t))
	}
	return n, nil
}

type parser struct {
	src  string
	toks []Token
	i    int
}

func (p *parser) peek() Token { return p.toks[p.i] }

func (p *parser) next() Token {
	t := p.toks[p.i]
	if t.Kind != TokEOF {
		p.i++
	}
	return t
}

func (p *parser) accept(k TokenKind) (Token, bool) {
	if p.peek().Kind == k {
		return p.next(), true
	}
	return Token{}, false
}

func (p *parser) expect(k TokenKind) (Token, error) {
	t := p.peek()
	if t.Kind != k {
		return t, p.errAt(t.Pos, "expected %s, got %s", k, describe(t))
	}
	return p.next(), nil
}

func (p *parser) errAt(pos int, format string, args ...any) error {
	return newError(p.src, pos, format, args...)
}

// expr := or
func (p *parser) expr() (Node, error) { return p.or() }

// or := and (OR and)*
func (p *parser) or() (Node, error) {
	return p.leftAssoc(p.and, TokOr)
}

// and := not (AND not)*
func (p *parser) and() (Node, error) {
	return p.leftAssoc(p.not, TokAnd)
}

// not := NOT not | cmp
//
// NOT directly followed by a parenthesis is the function form, a primary:
// NOT(a) = b compares NOT a with b, as a function call does, while
// NOT a = b negates the comparison, as the operator does.
func (p *parser) not() (Node, error) {
	if p.peek().Kind == TokNot && p.toks[p.i+1].Kind != TokLParen {
		t := p.next()
		x, err := p.not()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: TokNot, X: x, P: t.Pos}, nil
	}
	return p.cmp()
}

// cmp := concat ((= | <> | < | <= | > | >=) concat)*
func (p *parser) cmp() (Node, error) {
	return p.leftAssoc(p.concat, TokEq, TokNe, TokLt, TokLe, TokGt, TokGe)
}

// concat := add (& add)*
func (p *parser) concat() (Node, error) {
	return p.leftAssoc(p.add, TokAmp)
}

// add := mul ((+ | -) mul)*
func (p *parser) add() (Node, error) {
	return p.leftAssoc(p.mul, TokPlus, TokMinus)
}

// mul := power ((* | / | \) power)*
func (p *parser) mul() (Node, error) {
	return p.leftAssoc(p.power, TokStar, TokSlash, TokBackslash)
}

// power := unary (^ power)?
func (p *parser) power() (Node, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	if t, ok := p.accept(TokCaret); ok {
		r, err := p.power()
		if err != nil {
			return nil, err
		}
		return &Binary{Op: TokCaret, L: l, R: r, P: t.Pos}, nil
	}
	return l, nil
}

// unary := - unary | postfix
func (p *parser) unary() (Node, error) {
	if t, ok := p.accept(TokMinus); ok {
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &Unary{Op: TokMinus, X: x, P: t.Pos}, nil
	}
	return p.postfix()
}

// postfix := primary (%)*
func (p *parser) postfix() (Node, error) {
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.accept(TokPercent)
		if !ok {
			return x, nil
		}
		x = &Unary{Op: TokPercent, X: x, P: t.Pos}
	}
}

func (p *parser) leftAssoc(operand func() (Node, error), ops ...TokenKind) (Node, error) {
	l, err := operand()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		matched := false
		for _, op := range ops {
			if t.Kind == op {
				matched = true
				break
			}
		}
		if !matched {
			return l, nil
		}
		p.next()
		r, err := operand()
		if err != nil {
			return nil, err
		}
		l = &Binary{Op: t.Kind, L: l, R: r, P: t.Pos}
	}
}

// primary := number | text | TRUE | FALSE | BLANK | ( expr ) | IF ...
//
//	| (AND | OR | NOT) ( args ) | THIS . name | name ( args ) | name . name | name
func (p *parser) primary() (Node, error) {
	t := p.next()
	switch t.Kind {
	case TokNumber:
		return &NumberLit{Value: t.Num, P: t.Pos}, nil
	case TokText:
		return &TextLit{Value: t.Text, P: t.Pos}, nil
	case TokTrue:
		return &BoolLit{Value: true, P: t.Pos}, nil
	case TokFalse:
		return &BoolLit{Value: false, P: t.Pos}, nil
	case TokBlank:
		return &BlankLit{P: t.Pos}, nil
	case TokLParen:
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokRParen); err != nil {
			return nil, err
		}
		return x, nil
	case TokIf:
		return p.ifExpr(t)
	case TokAnd, TokOr, TokNot:
		if p.peek().Kind != TokLParen {
			return nil, p.errAt(t.Pos, "unexpected %s", t.Kind)
		}
		return p.callForm(t)
	case TokThis:
		if _, err := p.expect(TokDot); err != nil {
			return nil, p.errAt(t.Pos, "THIS must be followed by .name")
		}
		name, err := p.expect(TokIdent)
		if err != nil {
			return nil, p.errAt(name.Pos, "THIS must be followed by .name")
		}
		return &ThisRef{Name: name.Text, P: t.Pos}, nil
	case TokIdent:
		if p.peek().Kind == TokLParen {
			p.next()
			args, err := p.args()
			if err != nil {
				return nil, err
			}
			return p.call(t, args)
		}
		if _, ok := p.accept(TokDot); ok {
			name, err := p.expect(TokIdent)
			if err != nil {
				return nil, p.errAt(name.Pos, "expected a column name after %s.", t.Text)
			}
			if p.peek().Kind == TokDot {
				return nil, p.errAt(p.peek().Pos, "names nest only one level: %s.%s", t.Text, name.Text)
			}
			return &Member{Base: t.Text, Name: name.Text, P: t.Pos}, nil
		}
		return &Ident{Name: t.Text, P: t.Pos}, nil
	case TokEOF:
		return nil, p.errAt(t.Pos, "unexpected end of formula")
	}
	return nil, p.errAt(t.Pos, "unexpected %s", describe(t))
}

// callForm parses the function form of a keyword: AND(a, b), OR(a, b),
// NOT(a). The opening parenthesis is the next token.
func (p *parser) callForm(kw Token) (Node, error) {
	if _, err := p.expect(TokLParen); err != nil {
		return nil, err
	}
	args, err := p.args()
	if err != nil {
		return nil, err
	}
	return p.call(kw, args)
}

// args parses a comma-separated argument list up to and including the
// closing parenthesis. The opening parenthesis has been consumed.
func (p *parser) args() ([]Node, error) {
	var args []Node
	if _, ok := p.accept(TokRParen); ok {
		return args, nil
	}
	for {
		a, err := p.expr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if _, ok := p.accept(TokComma); ok {
			continue
		}
		if _, err := p.expect(TokRParen); err != nil {
			return nil, err
		}
		return args, nil
	}
}

// ifExpr parses what follows the IF keyword, in either form:
//
//	IF(c, a, b)            the call form
//	IF c THEN a ELSE b     the keyword form, c possibly parenthesised
func (p *parser) ifExpr(kw Token) (Node, error) {
	var cond Node
	if _, ok := p.accept(TokLParen); ok {
		first, err := p.expr()
		if err != nil {
			return nil, err
		}
		if _, ok := p.accept(TokComma); ok {
			args := []Node{first}
			for {
				a, err := p.expr()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if _, ok := p.accept(TokComma); ok {
					continue
				}
				if _, err := p.expect(TokRParen); err != nil {
					return nil, err
				}
				break
			}
			return p.call(kw, args)
		}
		if _, err := p.expect(TokRParen); err != nil {
			return nil, err
		}
		if p.peek().Kind != TokThen {
			return nil, p.errAt(p.peek().Pos, "IF needs three arguments or THEN ... ELSE, got %s", describe(p.peek()))
		}
		cond = first
	} else {
		c, err := p.expr()
		if err != nil {
			return nil, err
		}
		cond = c
	}
	if _, err := p.expect(TokThen); err != nil {
		return nil, err
	}
	then, err := p.expr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokElse); err != nil {
		return nil, err
	}
	els, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &If{Cond: cond, Then: then, Else: els, P: kw.Pos}, nil
}

// call checks a call against the catalog and builds its node. The keyword
// functions become the nodes their operator forms produce, so a formula has
// one shape whichever way it was written.
func (p *parser) call(name Token, args []Node) (Node, error) {
	fn := LookupFunction(name.Text)
	if fn == nil {
		return nil, p.errAt(name.Pos, "unknown function %s", strings.ToUpper(name.Text))
	}
	if err := fn.checkArity(name.Text, len(args)); err != nil {
		return nil, p.errAt(name.Pos, "%s", err.Error())
	}
	switch fn.Name {
	case "IF":
		return &If{Cond: args[0], Then: args[1], Else: args[2], P: name.Pos}, nil
	case "NOT":
		return &Unary{Op: TokNot, X: args[0], P: name.Pos}, nil
	case "AND", "OR":
		op := TokAnd
		if fn.Name == "OR" {
			op = TokOr
		}
		n := args[0]
		for _, a := range args[1:] {
			n = &Binary{Op: op, L: n, R: a, P: name.Pos}
		}
		return n, nil
	}
	return &Call{Name: fn.Name, Args: args, P: name.Pos}, nil
}
