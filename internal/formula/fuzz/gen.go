package fuzz

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Variables is the scope every generated formula reads: three numbers,
// two texts, two bools, two dates and a json value. The names are short so
// a report stays readable.
var Variables = []formula.Variable{
	{Name: "n1", Type: formula.Scalar(formula.KindNumber)},
	{Name: "n2", Type: formula.Scalar(formula.KindNumber)},
	{Name: "n3", Type: formula.Scalar(formula.KindNumber)},
	{Name: "t1", Type: formula.Scalar(formula.KindText)},
	{Name: "t2", Type: formula.Scalar(formula.KindText)},
	{Name: "b1", Type: formula.Scalar(formula.KindBool)},
	{Name: "b2", Type: formula.Scalar(formula.KindBool)},
	{Name: "d1", Type: formula.Scalar(formula.KindDate)},
	{Name: "d2", Type: formula.Scalar(formula.KindDate)},
	{Name: "j1", Type: formula.Scalar(formula.KindJSON)},
}

// Scope is Variables as a scope.
var Scope = func() *formula.Scope {
	s, err := formula.NewScope(Variables...)
	if err != nil {
		panic(err)
	}
	return s
}()

// Formula is one generated formula with the inputs and the context it
// runs under. Inputs has one value per entry of Variables.
type Formula struct {
	// Seed reproduces the formula: New(Seed).Formula() generates it again.
	Seed   uint64
	Src    string
	Inputs []formula.Value
	Ctx    formula.Context
}

// Generator produces formulas from a seed. It is not safe for concurrent
// use; make one per goroutine.
//
// Noise and quiet: a power, an exponential or a logarithm carries
// last-bit noise between the targets, which the runner tolerates in a
// number and in its rendering (Tolerance). It
// cannot tolerate it once a discontinuous operation has amplified it: a
// comparison, MOD, SIGN, rounding, a date part, a text position, a count.
// So those operations ask for quiet arguments, and inside a quiet subtree
// the generator never produces a noisy function. The conformance suite
// covers the shapes this leaves out, such as ROUND of a power, with
// values away from any rounding boundary.
type Generator struct {
	rng    *rand.Rand
	seed   uint64
	sb     strings.Builder
	budget int
	quiet  int
}

// MaxDepth bounds the nesting of a generated expression; nodeBudget
// bounds its size, so a wide call cannot explode.
const (
	MaxDepth   = 4
	nodeBudget = 28
)

// New makes a generator for a seed.
func New(seed uint64) *Generator {
	return &Generator{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), seed: seed}
}

// Formula generates the next formula. The first formula of a generator is
// a pure function of its seed; later ones continue the stream.
func (g *Generator) Formula() *Formula {
	f := &Formula{Seed: g.seed}
	g.sb.Reset()
	g.budget = nodeBudget
	g.quiet = 0
	g.expr(g.resultKind(), 0)
	f.Src = g.sb.String()
	f.Inputs = make([]formula.Value, len(Variables))
	for i, v := range Variables {
		f.Inputs[i] = g.value(v.Type.Kind)
	}
	f.Ctx = g.context()
	return f
}

func (g *Generator) n(n int) int { return g.rng.IntN(n) }

// chance is true with probability pct/100.
func (g *Generator) chance(pct int) bool { return g.rng.IntN(100) < pct }

func (g *Generator) pick(opts ...string) string { return opts[g.rng.IntN(len(opts))] }

func (g *Generator) w(parts ...string) {
	for _, p := range parts {
		g.sb.WriteString(p)
	}
}

func (g *Generator) resultKind() formula.Kind {
	switch r := g.n(100); {
	case r < 40:
		return formula.KindNumber
	case r < 60:
		return formula.KindBool
	case r < 85:
		return formula.KindText
	}
	return formula.KindDate
}

// anyKind picks a kind for an argument that accepts any scalar.
func (g *Generator) anyKind() formula.Kind {
	switch g.n(5) {
	case 0:
		return formula.KindBool
	case 1:
		return formula.KindText
	case 2:
		return formula.KindDate
	case 3:
		return formula.KindNull
	}
	return formula.KindNumber
}

// expr writes an expression of the kind. Blank asks for the BLANK literal.
func (g *Generator) expr(k formula.Kind, d int) {
	g.budget--
	if k == formula.KindNull {
		g.w("BLANK")
		return
	}
	if d >= MaxDepth || g.budget <= 0 || g.chance(12+12*d) {
		g.leaf(k)
		return
	}
	switch k {
	case formula.KindNumber:
		g.number(d)
	case formula.KindBool:
		g.condition(d)
	case formula.KindText:
		g.text(d)
	case formula.KindDate:
		g.date(d)
	default:
		g.leaf(k)
	}
}

// quietly writes an expression that carries no last-bit noise.
func (g *Generator) quietly(k formula.Kind, d int) {
	g.quiet++
	g.expr(k, d)
	g.quiet--
}

// arg returns a writer for a plain argument of the kind, quiet returns
// one for a quiet argument.
func (g *Generator) arg(k formula.Kind, d int) func()      { return func() { g.expr(k, d+1) } }
func (g *Generator) quietArg(k formula.Kind, d int) func() { return func() { g.quietly(k, d+1) } }

func (g *Generator) leaf(k formula.Kind) {
	switch k {
	case formula.KindNumber:
		switch g.n(10) {
		case 0, 1, 2, 3:
			g.w(g.pick("n1", "n2", "n3"))
		case 4:
			g.w("BLANK")
		default:
			g.numberLit()
		}
	case formula.KindBool:
		switch g.n(10) {
		case 0, 1, 2, 3:
			g.w(g.pick("b1", "b2"))
		case 4:
			g.w("BLANK")
		default:
			g.w(g.pick("TRUE", "FALSE", "true", "False"))
		}
	case formula.KindText:
		switch g.n(10) {
		case 0, 1, 2, 3:
			g.w(g.pick("t1", "t2"))
		case 4:
			g.w("BLANK")
		case 5:
			g.w("USER()")
		default:
			g.textLit(g.someText())
		}
	case formula.KindDate:
		switch g.n(10) {
		case 0, 1, 2, 3, 4:
			g.w(g.pick("d1", "d2"))
		case 5:
			g.w("BLANK")
		case 6:
			g.w(g.pick("NOW()", "TODAY()"))
		default:
			g.w("DATEVALUE(")
			g.textLit(g.dateText())
			g.w(")")
		}
	default:
		g.w("BLANK")
	}
}

// ---- Literals and pools

var numberPool = []float64{
	0, 1, 2, 3, 7, 10, 12, 100, 1000, 0.5, 0.1, 0.2, 0.29, 1.005, 2.675, 2.5, 0.125,
	1e-7, 1e15, 1e16, 123456789, 999999999999999, 1234.5678, 86400, 365, 1e100, 1e300, 0.3, 9.995,
}

func (g *Generator) someNumber() float64 {
	switch g.n(6) {
	case 0, 1, 2:
		return numberPool[g.n(len(numberPool))]
	case 3:
		return float64(g.n(200) - 100)
	case 4:
		return math.Round(g.rng.Float64()*2e4-1e4) / 100
	}
	return g.rng.NormFloat64() * 1e3
}

// numberLit writes a number literal; a negative one as a negated literal.
func (g *Generator) numberLit() {
	f := g.someNumber()
	if f < 0 {
		g.w("(-", strconv.FormatFloat(-f, 'g', -1, 64), ")")
		return
	}
	g.w(strconv.FormatFloat(f, 'g', -1, 64))
}

// smallInt writes a small non-negative integer literal, now and then a
// count that reaches the text limit of a campaign or runs past the end of
// any text, or any quiet number expression: the shape of a count, a width
// or a position.
func (g *Generator) smallInt(d int, max int) {
	switch {
	case g.chance(25):
		g.quietly(formula.KindNumber, d+1)
	case g.chance(12):
		g.w(g.pick("100", "300", "1000", "1024", "1025", "3000", "65536", "65537", "1e9", "1e300"))
	default:
		g.w(strconv.Itoa(g.n(max + 1)))
	}
}

var textPool = []string{
	"", "a", "abc", "Hello World", "hello world", "  padded  ", "x y  z", "é", "Ünïcode", "日本語", "it's",
	`say "hi"`, "1.5", "-2", "1e3", " 12 ", "abc1", "2024-02-29", "2024-13-01", "2024-02-30 10:00:00",
	"TRUE", "%", "a.b", ".", "a\\b", "aaa", "AbC dEf", "x", "0", "007", "+3.25", "1,5", "€100", "tab\there",
}

func (g *Generator) someText() string {
	if g.chance(75) {
		return textPool[g.n(len(textPool))]
	}
	const alphabet = "abcXYZ019 .-_é日"
	n := g.n(8)
	var b strings.Builder
	for i := 0; i < n; i++ {
		r := []rune(alphabet)
		b.WriteRune(r[g.n(len(r))])
	}
	return b.String()
}

func (g *Generator) textLit(s string) {
	g.w(`"`, strings.ReplaceAll(s, `"`, `""`), `"`)
}

var datePool = []int64{
	0,            // 1970-01-01
	-62135596800, // 0001-01-01
	253402300799, // 9999-12-31 23:59:59
	253402214400, // 9999-12-31
	1709164800,   // 2024-02-29
	1706659200,   // 2024-01-31
	1704067199,   // 2023-12-31 23:59:59
	951868800,    // 2000-03-01
	946684799,    // 1999-12-31 23:59:59
	-2208988800,  // 1900-01-01
	4102444800,   // 2100-01-01
	1735689600,   // 2025-01-01
	1740787200,   // 2025-03-01
	1762387200,   // 2025-11-06
	1740700800,   // 2025-02-28
}

func (g *Generator) someDate() int64 {
	if g.chance(50) {
		return datePool[g.n(len(datePool))]
	}
	// A day between 1900 and 2100, with a time of day on half of them.
	day := int64(g.n(73049)) - 25567
	secs := day * 86400
	if g.chance(50) {
		secs += int64(g.n(86400))
	}
	return secs
}

func (g *Generator) dateText() string {
	if g.chance(80) {
		return formula.Date(g.someDate()).String()
	}
	return g.pick("", " 2024-02-29 ", "2024-2-9", "2024-02-30", "29/02/2024", "2024-02-29T10:00:00", "abc", "2024-02-29 24:00:00", "0000-01-01")
}

var jsonPool = []string{
	`{"a":1,"b":"x","c":[1,2,{"d":true}],"e":null,"f":1.5,"g":false,"h":{"i":"deep"}}`,
	`[1,2,3]`, `"str"`, `42`, `{bad`, `null`, `{"a":{"b":{"c":"deep"}},"n":-0.5,"big":1e21}`, `[]`, `{}`, `{"a":[]}`,
}

var jsonPaths = []string{"", "a", "b", "c", "c.2.d", "c.0", "e", "f", "g", "h.i", "zz", "a.b.c", "0", "1", "c.9", "n", "big", "h"}

var patternPool = []string{"a", "[a-z]+", `\d`, "^", "$", ".", "x*", "(ab)+", `\s`, "[aeiou]", "", "o", "l+", "(?i)hello", `\.`, "[^a-z]", "^a|b$"}

// ---- Values and context

func (g *Generator) value(k formula.Kind) formula.Value {
	if g.chance(20) {
		return formula.Blank()
	}
	switch k {
	case formula.KindNumber:
		return formula.Number(g.someNumber())
	case formula.KindBool:
		return formula.Bool(g.chance(50))
	case formula.KindText:
		return formula.Text(g.someText())
	case formula.KindDate:
		return formula.Date(g.someDate())
	case formula.KindJSON:
		return formula.JSON(jsonPool[g.n(len(jsonPool))])
	}
	return formula.Blank()
}

func (g *Generator) context() formula.Context {
	ctx := formula.Context{Now: g.someDate(), User: g.pick("", "alice", "bob", "Alice")}
	switch g.n(4) {
	case 1:
		ctx.Groups = []string{"finance"}
	case 2:
		ctx.Groups = []string{"finance", "admin"}
	case 3:
		ctx.Groups = []string{}
	}
	return ctx
}

// ---- Productions

func (g *Generator) number(d int) {
	noisy := g.quiet == 0
	r := g.n(100)
	if !noisy && (r >= 56 && r < 62 || r >= 70 && r < 77) {
		r = g.n(30) // the noisy functions become arithmetic
	}
	switch {
	case r < 30:
		ops := []string{"+", "-", "*", "/", `\`, "+", "*"}
		if noisy {
			ops = append(ops, "^")
		}
		g.w("(")
		g.expr(formula.KindNumber, d+1)
		g.w(" ", ops[g.n(len(ops))], " ")
		g.expr(formula.KindNumber, d+1)
		g.w(")")
	case r < 36:
		g.w("(-")
		g.expr(formula.KindNumber, d+1)
		g.w(")")
	case r < 40:
		g.w("(")
		g.expr(formula.KindNumber, d+1)
		g.w("%)")
	case r < 48:
		g.ifExpr(formula.KindNumber, d)
	case r < 54:
		g.call(g.pick("ROUND", "FLOOR", "CEILING", "TRUNC"), g.quietArg(formula.KindNumber, d), g.optional(func() { g.digits(d) }))
	case r < 56:
		g.call(g.pick("ABS", "SQRT"), g.arg(formula.KindNumber, d))
	case r < 58:
		g.call("SIGN", g.quietArg(formula.KindNumber, d))
	case r < 62:
		g.call(g.pick("EXP", "LN"), g.arg(formula.KindNumber, d))
	case r < 70:
		g.aggregate(d, noisy)
	case r < 74:
		g.call("POWER", g.arg(formula.KindNumber, d), func() { g.smallInt(d, 4) })
	case r < 77:
		g.call("LOG", g.arg(formula.KindNumber, d), g.optional(func() { g.w(g.pick("10", "2", "2.718281828459045", "0.5", "1", "100", "0")) }))
	case r < 81:
		g.call("MOD", g.quietArg(formula.KindNumber, d), g.quietArg(formula.KindNumber, d))
	case r < 86:
		g.call(g.pick("YEAR", "MONTH", "DAY", "QUARTER"), g.arg(formula.KindDate, d))
	case r < 90:
		g.call(g.pick("DAYS", "DAYSBETWEEN", "MONTHSBETWEEN", "YEARSBETWEEN"), g.arg(formula.KindDate, d), g.arg(formula.KindDate, d))
	case r < 94:
		g.call("LEN", g.quietArg(formula.KindText, d))
	case r < 97:
		g.call(g.pick("FINDFIRST", "FINDLAST"), g.quietArg(formula.KindText, d), g.quietArg(formula.KindText, d))
	default:
		g.call("VALUE", g.arg(formula.KindText, d))
	}
}

// digits writes a digits argument: usually a small literal, negative now
// and then, sometimes any quiet number.
func (g *Generator) digits(d int) {
	switch g.n(8) {
	case 0:
		g.w("(-", strconv.Itoa(1+g.n(3)), ")")
	case 1:
		g.quietly(formula.KindNumber, d+1)
	default:
		g.w(strconv.Itoa(g.n(7)))
	}
}

func (g *Generator) aggregate(d int, noisy bool) {
	names := []string{"MIN", "MAX", "COUNT"}
	if noisy {
		names = append(names, "SUM", "AVERAGE")
	}
	name := names[g.n(len(names))]
	argc := 1 + g.n(4)
	args := make([]func(), argc)
	for i := range args {
		if name == "COUNT" {
			args[i] = g.arg(g.anyKind(), d)
		} else {
			args[i] = g.arg(formula.KindNumber, d)
		}
	}
	g.call(name, args...)
}

func (g *Generator) condition(d int) {
	switch r := g.n(100); {
	case r < 40:
		g.comparison(d)
	case r < 55:
		if g.chance(30) {
			name := g.pick("AND", "OR")
			argc := 2 + g.n(2)
			args := make([]func(), argc)
			for i := range args {
				args[i] = g.arg(formula.KindBool, d)
			}
			g.call(name, args...)
			return
		}
		g.w("(")
		g.expr(formula.KindBool, d+1)
		g.w(" ", g.pick("AND", "OR", "and", "or"), " ")
		g.expr(formula.KindBool, d+1)
		g.w(")")
	case r < 65:
		if g.chance(50) {
			g.call("NOT", g.arg(formula.KindBool, d))
			return
		}
		g.w("(NOT ")
		g.expr(formula.KindBool, d+1)
		g.w(")")
	case r < 80:
		g.call(g.pick("ISBLANK", "ISNOTBLANK"), g.arg(g.anyKind(), d))
	case r < 88:
		g.call("INGROUP", func() {
			if g.chance(60) {
				g.textLit(g.pick("finance", "admin", "Finance", ""))
				return
			}
			g.quietly(formula.KindText, d+1)
		})
	default:
		g.ifExpr(formula.KindBool, d)
	}
}

// comparison compares two quiet operands of one kind, or one with BLANK.
func (g *Generator) comparison(d int) {
	k := []formula.Kind{formula.KindNumber, formula.KindNumber, formula.KindText, formula.KindDate, formula.KindBool}[g.n(5)]
	g.w("(")
	g.quietly(k, d+1)
	g.w(" ", g.pick("=", "<>", "<", "<=", ">", ">="), " ")
	if g.chance(10) {
		g.w("BLANK")
	} else {
		g.quietly(k, d+1)
	}
	g.w(")")
}

func (g *Generator) text(d int) {
	switch r := g.n(100); {
	case r < 15:
		g.w("(")
		g.expr(g.anyKind(), d+1)
		g.w(" & ")
		g.expr(g.anyKind(), d+1)
		g.w(")")
	case r < 25:
		g.call(g.pick("LEFT", "RIGHT"), g.quietArg(formula.KindText, d), func() { g.smallInt(d, 6) })
	case r < 32:
		g.call("MID", g.quietArg(formula.KindText, d), func() { g.smallInt(d, 6) }, g.optional(func() { g.smallInt(d, 6) }))
	case r < 44:
		g.call(g.pick("TRIM", "UPPER", "LOWER", "PROPER"), g.arg(formula.KindText, d))
	case r < 49:
		g.call("REPLACE", g.quietArg(formula.KindText, d), func() { g.smallInt(d, 6) }, func() { g.smallInt(d, 6) }, g.quietArg(formula.KindText, d))
	case r < 55:
		g.call("SUBSTITUTE", g.quietArg(formula.KindText, d), g.quietArg(formula.KindText, d), g.quietArg(formula.KindText, d))
	case r < 61:
		argc := 1 + g.n(4)
		args := make([]func(), argc)
		for i := range args {
			args[i] = g.arg(g.anyKind(), d)
		}
		g.call("CONCAT", args...)
	case r < 65:
		g.call("REPEAT", g.arg(formula.KindText, d), func() { g.smallInt(d, 5) })
	case r < 71:
		g.call("PAD", g.quietArg(formula.KindText, d), func() { g.smallInt(d, 12) }, g.optional(func() { g.textLit(g.pick("0", "*", "ab", "", " ", "é")) }))
	case r < 77:
		g.call("REGEXREPLACE", g.quietArg(formula.KindText, d), func() { g.textLit(patternPool[g.n(len(patternPool))]) }, g.quietArg(formula.KindText, d))
	case r < 87:
		g.call("TEXT", g.arg(g.anyKind(), d))
	case r < 93:
		g.call("JSONVALUE", func() { g.w("j1") }, func() {
			if g.chance(80) {
				g.textLit(jsonPaths[g.n(len(jsonPaths))])
				return
			}
			g.quietly(formula.KindText, d+1)
		})
	default:
		g.ifExpr(formula.KindText, d)
	}
}

func (g *Generator) date(d int) {
	switch r := g.n(100); {
	case r < 30:
		g.call("DATE", func() {
			if g.chance(60) {
				g.w(strconv.Itoa(1900 + g.n(200)))
				return
			}
			g.quietly(formula.KindNumber, d+1)
		}, func() {
			if g.chance(70) {
				g.w(strconv.Itoa(g.n(15) - 1))
				return
			}
			g.quietly(formula.KindNumber, d+1)
		}, func() {
			if g.chance(70) {
				g.w(strconv.Itoa(g.n(34) - 1))
				return
			}
			g.quietly(formula.KindNumber, d+1)
		})
	case r < 45:
		g.call("DATEVALUE", func() {
			if g.chance(50) {
				g.textLit(g.dateText())
				return
			}
			g.quietly(formula.KindText, d+1)
		})
	case r < 80:
		g.call(g.pick("ADDDAYS", "ADDMONTHS", "ADDYEARS"), g.arg(formula.KindDate, d), func() {
			switch g.n(4) {
			case 0:
				g.quietly(formula.KindNumber, d+1)
			case 1:
				g.w("(-", strconv.Itoa(g.n(40)), ")")
			default:
				g.w(strconv.Itoa(g.n(40)))
			}
		})
	case r < 88:
		g.w(g.pick("NOW()", "TODAY()"))
	default:
		g.ifExpr(formula.KindDate, d)
	}
}

// ifExpr writes an IF in the call form or the keyword form. One branch is
// blank now and then, which is the join rule of ADR-0019.
func (g *Generator) ifExpr(k formula.Kind, d int) {
	branch := func() {
		if g.chance(10) {
			g.w("BLANK")
			return
		}
		g.expr(k, d+1)
	}
	if g.chance(50) {
		g.call("IF", g.arg(formula.KindBool, d), branch, branch)
		return
	}
	g.w("(IF ")
	g.expr(formula.KindBool, d+1)
	g.w(" THEN ")
	branch()
	g.w(" ELSE ")
	branch()
	g.w(")")
}

// optional returns the argument writer half of the time, nil otherwise;
// call drops a nil argument and everything after it.
func (g *Generator) optional(arg func()) func() {
	if g.chance(50) {
		return arg
	}
	return nil
}

// call writes name(args...), spelling the name in a random case now and
// then, since names are case-insensitive.
func (g *Generator) call(name string, args ...func()) {
	if g.chance(10) {
		name = strings.ToLower(name)
	}
	g.w(name, "(")
	for i, a := range args {
		if a == nil {
			break
		}
		if i > 0 {
			g.w(", ")
		}
		a()
	}
	g.w(")")
}

// ---- Rendering a formula for people and for the suite

// DescribeInputs renders the inputs as name=value pairs, blanks left out.
func (f *Formula) DescribeInputs() string {
	parts := make([]string, 0, len(Variables))
	for i, v := range Variables {
		if f.Inputs[i].IsBlank() {
			continue
		}
		parts = append(parts, v.Name+"="+f.Inputs[i].String())
	}
	if len(parts) == 0 {
		return "(all blank)"
	}
	return strings.Join(parts, " ")
}

// DescribeContext renders the context.
func (f *Formula) DescribeContext() string {
	return "user=" + strconv.Quote(f.Ctx.User) + " groups=" + strings.Join(f.Ctx.Groups, ",") +
		" now=" + formula.Date(f.Ctx.Now).String()
}

// CaseJSON renders the formula with its inputs and context as a suite case
// whose expectation is the given value or error code, ready to paste into
// conformance/formula. The suite variables are those of Variables.
func (f *Formula) CaseJSON(result formula.Value, code string, kind formula.Kind) string {
	var b strings.Builder
	b.WriteString(`{"formula": ` + strconv.Quote(f.Src))
	first := true
	for i, v := range Variables {
		if f.Inputs[i].IsBlank() {
			continue
		}
		if first {
			b.WriteString(`, "inputs": {`)
			first = false
		} else {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(v.Name) + ": " + suiteValue(f.Inputs[i]))
	}
	if !first {
		b.WriteString("}")
	}
	b.WriteString(`, "context": {"user": ` + strconv.Quote(f.Ctx.User))
	if f.Ctx.Groups != nil {
		b.WriteString(`, "groups": [`)
		for i, g := range f.Ctx.Groups {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(g))
		}
		b.WriteString("]")
	}
	b.WriteString(`, "now": ` + strconv.Quote(formula.Date(f.Ctx.Now).String()) + "}")
	if kind == formula.KindDate {
		b.WriteString(`, "type": "date"`)
	}
	if code != "" {
		b.WriteString(`, "error": ` + strconv.Quote(code))
	} else {
		b.WriteString(`, "expected": ` + suiteValue(result))
	}
	b.WriteString("}")
	return b.String()
}

// suiteValue writes a value in the notation of the conformance suite.
func suiteValue(v formula.Value) string {
	switch v.Kind() {
	case formula.KindNull:
		return "null"
	case formula.KindBool:
		if v.Truth() {
			return "true"
		}
		return "false"
	case formula.KindNumber:
		return strconv.FormatFloat(v.Num(), 'g', -1, 64)
	case formula.KindDate:
		return strconv.Quote(v.String())
	}
	return strconv.Quote(v.Str())
}
