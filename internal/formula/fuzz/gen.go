package fuzz

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Variables is the scope every generated formula reads: three numbers,
// two texts, two bools, two dates, two datetimes, three exact numbers, two
// money values and a json value. The names are short so a report stays
// readable.
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
	{Name: "s1", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "s2", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "x1", Type: formula.DecimalType(2)},
	{Name: "x2", Type: formula.DecimalType(4)},
	{Name: "i1", Type: formula.DecimalType(0)},
	{Name: "m1", Type: formula.MoneyType(2)},
	{Name: "m2", Type: formula.MoneyType(2)},
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
// comparison, MOD, SIGN, rounding, a date part, a text position, a count,
// a conversion to an exact number. So those operations ask for quiet
// arguments, and inside a quiet subtree the generator never produces a
// noisy function. The conformance suite covers the shapes this leaves
// out, such as ROUND of a power, with values away from any rounding
// boundary. Exact arithmetic is never noisy.
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
		f.Inputs[i] = g.value(v.Type)
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
	case r < 28:
		return formula.KindNumber
	case r < 42:
		return formula.KindBool
	case r < 60:
		return formula.KindText
	case r < 68:
		return formula.KindDate
	case r < 76:
		return formula.KindDateTime
	case r < 90:
		return formula.KindDecimal
	}
	return formula.KindMoney
}

// anyKind picks a kind for an argument that accepts any scalar.
func (g *Generator) anyKind() formula.Kind {
	switch g.n(8) {
	case 0:
		return formula.KindBool
	case 1:
		return formula.KindText
	case 2:
		return formula.KindDate
	case 3:
		return formula.KindNull
	case 4:
		return formula.KindDateTime
	case 5:
		return formula.KindDecimal
	case 6:
		return formula.KindMoney
	}
	return formula.KindNumber
}

// expr writes an expression of the kind. Blank asks for the BLANK literal.
func (g *Generator) expr(k formula.Kind, d int) {
	switch k {
	case formula.KindDecimal:
		g.exact(d, false, formula.MaxScale)
		return
	case formula.KindMoney:
		g.exact(d, true, formula.MaxScale)
		return
	}
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
	case formula.KindDateTime:
		g.datetime(d)
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
		case 5:
			g.w(g.pick("VERSION()", "n1"))
		case 6:
			g.ofState("ENTRIES", "DAYSIN")
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
			g.w(g.pick("USER()", "USER()", "USERKIND()", "ACTINGFOR()"))
		case 6:
			g.w(g.pick("STATE()", "ID()", "CREATEDBY()", "UPDATEDBY()", "ENTEREDBY()", "ENTEREDFROM()"))
		case 7:
			g.ofState("FIRSTENTEREDBY", "LASTENTEREDBY", "FIRSTLEFTBY", "LASTLEFTBY", "FIRSTEDITOR", "LASTEDITOR")
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
			g.w("TODAY()")
		default:
			g.w("DATEVALUE(")
			g.textLit(g.dayText())
			g.w(")")
		}
	case formula.KindDateTime:
		switch g.n(10) {
		case 0, 1, 2, 3, 4:
			g.w(g.pick("s1", "s2"))
		case 5:
			g.w("BLANK")
		case 6:
			g.w(g.pick("NOW()", "CREATEDAT()", "UPDATEDAT()", "ENTEREDAT()"))
		case 7:
			g.ofState("FIRSTENTERED", "LASTENTERED", "FIRSTLEFT", "LASTLEFT")
		default:
			g.w("DATETIMEVALUE(")
			g.textLit(g.dateTimeText())
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
	"12.30", "12.30 PLN", "-0.5 EUR", "1e2 USD", "12.345", "0.1234567890123456789", "9223372036854775808", "PLN", "pln", "12.30PLN",
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

var secondsPool = []int64{
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

// someSecs is an instant: from the pool, or a day between 1900 and 2100
// with a time of day on half of them.
func (g *Generator) someSecs() int64 {
	if g.chance(50) {
		return secondsPool[g.n(len(secondsPool))]
	}
	day := int64(g.n(73049)) - 25567
	secs := day * 86400
	if g.chance(50) {
		secs += int64(g.n(86400))
	}
	return secs
}

// someDay is a calendar day: the day of an instant from the pool, or one
// between 1900 and 2100.
func (g *Generator) someDay() int64 {
	if g.chance(50) {
		secs := secondsPool[g.n(len(secondsPool))]
		return int64(math.Floor(float64(secs) / 86400))
	}
	return int64(g.n(73049)) - 25567
}

func (g *Generator) dayText() string {
	if g.chance(80) {
		return formula.Date(g.someDay()).String()
	}
	return g.pick("", " 2024-02-29 ", "2024-2-9", "2024-02-30", "29/02/2024", "2024-02-29T10:00:00", "abc", "2024-02-29 10:00:00", "0000-01-01")
}

func (g *Generator) dateTimeText() string {
	if g.chance(80) {
		if g.chance(30) {
			return formula.Date(g.someDay()).String()
		}
		return formula.DateTime(g.someSecs()).String()
	}
	return g.pick("", " 2024-02-29 ", "2024-2-9", "2024-02-30", "29/02/2024", "2024-02-29T10:00:00", "abc", "2024-02-29 24:00:00", "0000-01-01", "2024-02-29 10:00")
}

// exactPool holds mantissas for exact values at scale 2; other scales are
// rescaled from them.
var exactPool = []int64{0, 1, -1, 100, 150, 1000, 999, -50, 12345, 1234567, 2675, 1005, 29, 50, 25, 33, 9999999999999999, -9999999999999999, 922337203685477580}

func (g *Generator) someMant() int64 {
	switch g.n(5) {
	case 0, 1:
		return exactPool[g.n(len(exactPool))]
	case 2:
		return int64(g.n(20001) - 10000)
	case 3:
		return int64(g.n(2000001)) - 1000000
	}
	return g.rng.Int64N(2000000000000000000) - 1000000000000000000
}

var currencyPool = []string{"PLN", "PLN", "PLN", "EUR", "USD"}

var jsonPool = []string{
	`{"a":1,"b":"x","c":[1,2,{"d":true}],"e":null,"f":1.5,"g":false,"h":{"i":"deep"}}`,
	`[1,2,3]`, `"str"`, `42`, `{bad`, `null`, `{"a":{"b":{"c":"deep"}},"n":-0.5,"big":1e21}`, `[]`, `{}`, `{"a":[]}`,
}

var jsonPaths = []string{"", "a", "b", "c", "c.2.d", "c.0", "e", "f", "g", "h.i", "zz", "a.b.c", "0", "1", "c.9", "n", "big", "h"}

var patternPool = []string{"a", "[a-z]+", `\d`, "^", "$", ".", "x*", "(ab)+", `\s`, "[aeiou]", "", "o", "l+", "(?i)hello", `\.`, "[^a-z]", "^a|b$"}

// ---- Values and context

func (g *Generator) value(t formula.Type) formula.Value {
	if g.chance(20) {
		return formula.Blank()
	}
	switch t.Kind {
	case formula.KindNumber:
		return formula.Number(g.someNumber())
	case formula.KindBool:
		return formula.Bool(g.chance(50))
	case formula.KindText:
		return formula.Text(g.someText())
	case formula.KindDate:
		return formula.Date(g.someDay())
	case formula.KindDateTime:
		return formula.DateTime(g.someSecs())
	case formula.KindDecimal:
		return formula.Decimal(g.someMant(), int(t.Scale))
	case formula.KindMoney:
		return formula.Money(g.someMant(), int(t.Scale), currencyPool[g.n(len(currencyPool))])
	case formula.KindJSON:
		return formula.JSON(jsonPool[g.n(len(jsonPool))])
	}
	return formula.Blank()
}

func (g *Generator) context() formula.Context {
	ctx := formula.Context{Now: g.someSecs(), User: g.pick("", "alice", "bob", "Alice"),
		State: g.pick("", "draft", "paid"), ID: g.pick("", "inv-1", "01J0"),
		Kind: g.pick("", "person", "agent", "service"), For: g.pick("", "", "alice", "carol")}
	switch g.n(4) {
	case 1:
		ctx.Groups = []string{"finance"}
	case 2:
		ctx.Groups = []string{"finance", "admin"}
	case 3:
		ctx.Groups = []string{}
	}
	// The life of the document (ADR-0047): none, or one with some of its
	// facts blank.
	if g.n(4) > 0 {
		ctx.Doc = &formula.Life{Version: int64(g.n(5)), CreatedAt: g.someSecs(), UpdatedAt: g.someSecs(),
			CreatedBy: g.pick("", "alice", "bob"), UpdatedBy: g.pick("", "alice", "carol"),
			EnteredBy: g.pick("", "bob"), EnteredFrom: g.pick("", "draft", "paid")}
		if g.n(3) > 0 {
			ctx.Doc.EnteredAt = g.someSecs()
		}
		// What the life says of its states (item 6): nothing, or a
		// creation and a few moves and changes, by somebody or by nobody,
		// at moments in any order.
		if g.n(4) > 0 {
			at, state := g.someSecs(), g.pick("draft", "paid")
			ctx.Doc.Tell(formula.LifeEvent{Kind: formula.LifeCreated, To: state, At: at, By: g.pick("", "alice")})
			for n := g.n(6); n > 0; n-- {
				if g.chance(80) {
					at += int64(g.n(200000))
				} else {
					at = g.someSecs()
				}
				ev := formula.LifeEvent{Kind: formula.LifeChanged, At: at, By: g.pick("", "alice", "bob", "carol")}
				if g.chance(60) {
					ev.Kind, ev.From, ev.To = formula.LifeMoved, state, g.pick("draft", "paid", "review")
					state = ev.To
				}
				ctx.Doc.Tell(ev)
			}
		}
	}
	return ctx
}

// ofState writes a call of one of the functions of a state (ADR-0047,
// item 6), the state written as text, known or not, or computed.
func (g *Generator) ofState(names ...string) {
	g.w(g.pick(names...))
	g.w("(")
	g.w(g.pick(`"draft"`, `"paid"`, `"review"`, `"void"`, `""`, "STATE()", "ENTEREDFROM()", "t1", "BLANK"))
	g.w(")")
}

// ---- Productions

func (g *Generator) number(d int) {
	noisy := g.quiet == 0
	r := g.n(100)
	if !noisy && (r >= 56 && r < 62 || r >= 70 && r < 77) {
		r = g.n(30) // the noisy functions become arithmetic
	}
	switch {
	case r < 28:
		ops := []string{"+", "-", "*", "/", `\`, "+", "*"}
		if noisy {
			ops = append(ops, "^")
		}
		g.w("(")
		g.expr(formula.KindNumber, d+1)
		g.w(" ", ops[g.n(len(ops))], " ")
		g.expr(formula.KindNumber, d+1)
		g.w(")")
	case r < 33:
		g.w("(-")
		g.expr(formula.KindNumber, d+1)
		g.w(")")
	case r < 36:
		g.w("(")
		g.expr(formula.KindNumber, d+1)
		g.w("%)")
	case r < 42:
		g.ifExpr(formula.KindNumber, d)
	case r < 48:
		g.call("ROUND", g.quietArg(formula.KindNumber, d), g.optional(func() { g.digits(d) }), g.optional(func() { g.textLit(g.method()) }))
	case r < 52:
		g.call(g.pick("FLOOR", "CEILING", "TRUNC"), g.quietArg(formula.KindNumber, d), g.optional(func() { g.digits(d) }))
	case r < 56:
		g.call(g.pick("ABS", "SQRT"), g.arg(formula.KindNumber, d))
	case r < 58:
		g.call("SIGN", g.quietArg(formula.KindNumber, d))
	case r < 62:
		g.call(g.pick("EXP", "LN"), g.arg(formula.KindNumber, d))
	case r < 68:
		g.aggregate(d, noisy)
	case r < 70:
		g.call("NUMBER", func() { g.exact(d+1, g.chance(30), formula.MaxScale) })
	case r < 74:
		g.call("POWER", g.arg(formula.KindNumber, d), func() { g.smallInt(d, 4) })
	case r < 77:
		g.call("LOG", g.arg(formula.KindNumber, d), g.optional(func() { g.w(g.pick("10", "2", "2.718281828459045", "0.5", "1", "100", "0")) }))
	case r < 81:
		g.call("MOD", g.quietArg(formula.KindNumber, d), g.quietArg(formula.KindNumber, d))
	case r < 86:
		g.call(g.pick("YEAR", "MONTH", "DAY", "QUARTER"), g.arg(g.dateLike(), d))
	case r < 90:
		k := g.dateLike()
		g.call(g.pick("DAYS", "DAYSBETWEEN", "MONTHSBETWEEN", "YEARSBETWEEN"), g.arg(k, d), g.arg(k, d))
	case r < 93:
		g.call("LEN", g.quietArg(formula.KindText, d))
	case r < 96:
		g.call(g.pick("FINDFIRST", "FINDLAST"), g.quietArg(formula.KindText, d), g.quietArg(formula.KindText, d))
	case r < 98:
		g.call("IFERROR", g.arg(formula.KindNumber, d), g.arg(formula.KindNumber, d))
	default:
		g.call("VALUE", g.arg(formula.KindText, d))
	}
}

// dateLike picks a date or a datetime for an argument that takes either.
func (g *Generator) dateLike() formula.Kind {
	if g.chance(50) {
		return formula.KindDate
	}
	return formula.KindDateTime
}

// method picks a rounding method.
func (g *Generator) method() string {
	return g.pick("half_up", "half_even", "down", "up", "floor", "ceiling")
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
	case r < 93:
		g.call("IFERROR", g.arg(formula.KindBool, d), g.arg(formula.KindBool, d))
	default:
		g.ifExpr(formula.KindBool, d)
	}
}

// comparison compares two quiet operands of one kind, or one with BLANK;
// exact values compare with each other, money with a decimal too.
func (g *Generator) comparison(d int) {
	k := []formula.Kind{formula.KindNumber, formula.KindNumber, formula.KindText, formula.KindDate, formula.KindDateTime, formula.KindBool, formula.KindDecimal, formula.KindMoney}[g.n(8)]
	g.w("(")
	g.quietly(k, d+1)
	g.w(" ", g.pick("=", "<>", "<", "<=", ">", ">="), " ")
	switch {
	case g.chance(10):
		g.w("BLANK")
	case k.IsExact() && g.chance(30):
		g.exactLit(formula.MaxScale)
	case k.IsExact() && g.chance(30):
		g.quietly(formula.KindDecimal, d+1)
	default:
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
	case r < 42:
		g.call(g.pick("TRIM", "UPPER", "LOWER", "PROPER", "URLENCODE"), g.arg(formula.KindText, d))
	case r < 47:
		g.call("REPLACE", g.quietArg(formula.KindText, d), func() { g.smallInt(d, 6) }, func() { g.smallInt(d, 6) }, g.quietArg(formula.KindText, d))
	case r < 53:
		g.call("SUBSTITUTE", g.quietArg(formula.KindText, d), g.quietArg(formula.KindText, d), g.quietArg(formula.KindText, d))
	case r < 59:
		argc := 1 + g.n(4)
		args := make([]func(), argc)
		for i := range args {
			args[i] = g.arg(g.anyKind(), d)
		}
		g.call("CONCAT", args...)
	case r < 63:
		g.call("REPEAT", g.arg(formula.KindText, d), func() { g.smallInt(d, 5) })
	case r < 69:
		g.call("PAD", g.quietArg(formula.KindText, d), func() { g.smallInt(d, 12) }, g.optional(func() { g.textLit(g.pick("0", "*", "ab", "", " ", "é")) }))
	case r < 75:
		g.call("REGEXREPLACE", g.quietArg(formula.KindText, d), func() { g.textLit(patternPool[g.n(len(patternPool))]) }, g.quietArg(formula.KindText, d))
	case r < 85:
		g.call("TEXT", g.arg(g.anyKind(), d))
	case r < 89:
		g.call("CURRENCY", func() { g.exact(d+1, true, formula.MaxScale) })
	case r < 93:
		g.call("JSONVALUE", func() { g.w("j1") }, func() {
			if g.chance(80) {
				g.textLit(jsonPaths[g.n(len(jsonPaths))])
				return
			}
			g.quietly(formula.KindText, d+1)
		})
	case r < 96:
		g.call("IFERROR", g.arg(formula.KindText, d), g.arg(formula.KindText, d))
	default:
		g.ifExpr(formula.KindText, d)
	}
}

func (g *Generator) date(d int) {
	switch r := g.n(100); {
	case r < 25:
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
	case r < 38:
		g.call("DATEVALUE", func() {
			if g.chance(50) {
				g.textLit(g.dayText())
				return
			}
			g.quietly(formula.KindText, d+1)
		})
	case r < 50:
		g.call("DATEOF", g.arg(formula.KindDateTime, d))
	case r < 78:
		g.call(g.pick("ADDDAYS", "ADDMONTHS", "ADDYEARS"), g.arg(formula.KindDate, d), g.moveBy(d))
	case r < 84:
		g.w("TODAY()")
	case r < 90:
		g.call("IFERROR", g.arg(formula.KindDate, d), g.arg(formula.KindDate, d))
	default:
		g.ifExpr(formula.KindDate, d)
	}
}

func (g *Generator) datetime(d int) {
	switch r := g.n(100); {
	case r < 30:
		g.call("DATETIMEVALUE", func() {
			if g.chance(50) {
				g.textLit(g.dateTimeText())
				return
			}
			g.quietly(formula.KindText, d+1)
		})
	case r < 45:
		g.call("DATETIME", g.arg(formula.KindDate, d))
	case r < 78:
		g.call(g.pick("ADDDAYS", "ADDMONTHS", "ADDYEARS"), g.arg(formula.KindDateTime, d), g.moveBy(d))
	case r < 84:
		g.w("NOW()")
	case r < 90:
		g.call("IFERROR", g.arg(formula.KindDateTime, d), g.arg(formula.KindDateTime, d))
	default:
		g.ifExpr(formula.KindDateTime, d)
	}
}

// moveBy writes the count a date function moves by.
func (g *Generator) moveBy(d int) func() {
	return func() {
		switch g.n(5) {
		case 0:
			g.quietly(formula.KindNumber, d+1)
		case 1:
			g.w("(-", strconv.Itoa(g.n(40)), ")")
		case 2:
			g.w(g.pick("0.5", "1.5", "(-0.25)", "2.9"))
		default:
			g.w(strconv.Itoa(g.n(40)))
		}
	}
}

// exact writes an exact expression, money or decimal, whose scale is at
// most limit, and returns the scale. The productions keep the scale
// within MaxScale by construction, since the binder refuses a product
// that would need more.
func (g *Generator) exact(d int, money bool, limit int) int {
	g.budget--
	if d >= MaxDepth || g.budget <= 0 || g.chance(12+12*d) {
		return g.exactLeaf(money, limit)
	}
	r := g.n(100)
	switch {
	case r < 22:
		// + and -: both sides within the limit.
		g.w("(")
		sl, lit := g.exactOperand(d, money, limit, false)
		g.w(" ", g.pick("+", "-"), " ")
		var sr int
		if money && g.chance(40) || g.chance(30) {
			sr, _ = g.exactOperand(d, false, limit, lit)
		} else {
			sr, _ = g.exactOperand(d, money, limit, lit)
		}
		g.w(")")
		return max(sl, sr)
	case r < 34:
		// *: money times a decimal, or decimals; the right side fits the
		// room the left leaves.
		g.w("(")
		sl, lit := g.exactOperand(d, money, limit, false)
		g.w(" * ")
		sr, _ := g.exactOperand(d, false, limit-sl, lit)
		g.w(")")
		return sl + sr
	case r < 44:
		// /: the result is the dividend's scale plus four, so the dividend
		// has four to spare unless the limit is the maximum.
		room := limit - formula.DivScaleStep
		if limit == formula.MaxScale {
			room = formula.MaxScale
		}
		if room < 0 {
			return g.exactLeaf(money, limit)
		}
		g.w("(")
		sl, lit := g.exactOperand(d, money, room, false)
		g.w(" ", g.pick("/", "/", `\`), " ")
		g.exactOperand(d, false, formula.MaxScale, lit)
		g.w(")")
		return min(sl+formula.DivScaleStep, formula.MaxScale)
	case r < 48:
		if money {
			// money / money is a decimal, so a money expression cannot
			// end in it; a decimal one can.
			return g.exactLeaf(money, limit)
		}
		room := limit - formula.DivScaleStep
		if limit == formula.MaxScale {
			room = formula.MaxScale
		}
		if room < 0 {
			return g.exactLeaf(money, limit)
		}
		g.w("(")
		sl := g.exact(d+1, true, room)
		g.w(" / ")
		g.exact(d+1, true, formula.MaxScale)
		g.w(")")
		return min(sl+formula.DivScaleStep, formula.MaxScale)
	case r < 52:
		g.w("(-")
		s := g.exact(d+1, money, limit)
		g.w(")")
		return s
	case r < 55:
		if limit < 2 {
			return g.exactLeaf(money, limit)
		}
		g.w("(")
		s := g.exact(d+1, money, limit-2)
		g.w("%)")
		return s + 2
	case r < 63:
		// Rounding: the places are written, the result lands on them.
		name := g.pick("ROUND", "ROUND", "FLOOR", "CEILING", "TRUNC")
		places := g.n(limit+3) - 2
		g.w(name, "(")
		g.exact(d+1, money, formula.MaxScale)
		g.w(", ", strconv.Itoa(places))
		if name == "ROUND" && g.chance(60) {
			g.w(", ")
			g.textLit(g.method())
		}
		g.w(")")
		return max(places, 0)
	case r < 67:
		g.w("ABS(")
		s := g.exact(d+1, money, limit)
		g.w(")")
		return s
	case r < 77:
		name := g.pick("MIN", "MAX", "SUM", "SUM", "AVERAGE", "MOD")
		room := limit
		if name == "AVERAGE" {
			room = limit - formula.DivScaleStep
			if limit == formula.MaxScale {
				room = formula.MaxScale
			}
			if room < 0 {
				return g.exactLeaf(money, limit)
			}
		}
		argc := 2 + g.n(3)
		if name == "MOD" {
			argc = 2
		}
		g.w(name, "(")
		s := 0
		for i := 0; i < argc; i++ {
			if i > 0 {
				g.w(", ")
			}
			// The first argument is exact itself, so that the function
			// runs on the exact family whatever the rest are.
			si, _ := g.exactOperand(d, money && (i == 0 || g.chance(70)), room, i == 0)
			s = max(s, si)
		}
		g.w(")")
		if name == "AVERAGE" {
			return min(s+formula.DivScaleStep, formula.MaxScale)
		}
		return s
	case r < 83:
		if money {
			g.w("MONEY(")
			s := g.exact(d+1, false, limit)
			g.w(", ")
			if g.chance(80) {
				g.textLit(g.pick("PLN", "EUR", "USD", "pln", "", "PLNX"))
			} else {
				g.quietly(formula.KindText, d+1)
			}
			g.w(")")
			return s
		}
		places := g.n(limit + 1)
		g.w("DECIMAL(")
		g.quietly(formula.KindNumber, d+1)
		g.w(", ", strconv.Itoa(places))
		if g.chance(50) {
			g.w(", ")
			g.textLit(g.method())
		}
		g.w(")")
		return places
	case r < 87:
		if money {
			places := g.n(limit + 1)
			g.w("MONEYVALUE(")
			if g.chance(60) {
				g.textLit(g.pick("12.30 PLN", "12.3 EUR", "-5 USD", "1e2 PLN", "12.345 PLN", "abc PLN", "12.30", "", "12.30 pln", "0.50 CHF"))
			} else {
				g.quietly(formula.KindText, d+1)
			}
			g.w(", ", strconv.Itoa(places), ")")
			return places
		}
		if g.chance(50) {
			g.w("AMOUNT(")
			s := g.exact(d+1, true, limit)
			g.w(")")
			return s
		}
		places := g.n(limit + 1)
		g.w("DECIMALVALUE(")
		if g.chance(60) {
			g.textLit(g.pick("12.30", "12.3", "-0.5", "1e3", "12.345", "abc", "", " 7 ", "0.1234567890123456789", "9223372036854775808", ".5", "5."))
		} else {
			g.quietly(formula.KindText, d+1)
		}
		g.w(", ", strconv.Itoa(places), ")")
		return places
	case r < 93:
		g.w("IFERROR(")
		s := g.exact(d+1, money, limit)
		g.w(", ")
		s2 := g.exact(d+1, money, limit)
		g.w(")")
		return max(s, s2)
	default:
		g.w("(IF ")
		g.expr(formula.KindBool, d+1)
		g.w(" THEN ")
		s := g.exact(d+1, money, limit)
		g.w(" ELSE ")
		s2 := g.exact(d+1, money, limit)
		g.w(")")
		return max(s, s2)
	}
}

// exactOperand writes an operand of an exact operation: an exact
// expression, or, unless the other operand is one already (noLit), a
// number constant the binder reads exactly; the scale is within the
// limit. It reports whether it wrote a constant, since an operation on
// constants alone is a number, not an exact value.
func (g *Generator) exactOperand(d int, money bool, limit int, noLit bool) (int, bool) {
	if !money && !noLit && g.chance(30) {
		return g.exactLit(limit), true
	}
	return g.exact(d+1, money, limit), false
}

// exactLit writes a number constant that reads exactly with at most limit
// places, and returns its scale.
func (g *Generator) exactLit(limit int) int {
	pool := []struct {
		text  string
		scale int
	}{
		{"0", 0}, {"1", 0}, {"2", 0}, {"3", 0}, {"10", 0}, {"100", 0}, {"(-1)", 0}, {"7", 0},
		{"0.5", 1}, {"1.5", 1}, {"0.1", 1}, {"2.5", 1}, {"(-0.5)", 1},
		{"0.25", 2}, {"0.01", 2}, {"1.23", 2}, {"2.675", 3}, {"1.005", 3}, {"0.125", 3},
		{"10%", 2}, {"23%", 2}, {"(1 - 10%)", 2}, {"(2 + 0.5)", 1}, {"1e3", 0}, {"1e-2", 2}, {"0.0001", 4},
	}
	for i := 0; i < 50; i++ {
		p := pool[g.n(len(pool))]
		if p.scale <= limit {
			g.w(p.text)
			return p.scale
		}
	}
	g.w("1")
	return 0
}

// exactLeaf writes a variable, a blank or a small constant expression of
// the family within the limit, and returns its scale.
func (g *Generator) exactLeaf(money bool, limit int) int {
	g.budget--
	if money {
		switch g.n(10) {
		case 0, 1, 2, 3, 4, 5:
			if limit >= 2 {
				g.w(g.pick("m1", "m2"))
				return 2
			}
			g.w("ROUND(", g.pick("m1", "m2"), ", ", strconv.Itoa(limit), ")")
			return limit
		case 6, 7:
			places := min(2, limit)
			g.w("MONEYVALUE(\"1.50 PLN\", ", strconv.Itoa(places), ")")
			return places
		default:
			g.w("MONEY(")
			s := g.exactLit(limit)
			g.w(", ", `"`, g.pick("PLN", "EUR", "USD"), `")`)
			return s
		}
	}
	for i := 0; i < 20; i++ {
		switch g.n(10) {
		case 0, 1, 2:
			if limit >= 2 {
				g.w("x1")
				return 2
			}
		case 3, 4:
			if limit >= 4 {
				g.w("x2")
				return 4
			}
		case 5, 6, 7:
			g.w("i1")
			return 0
		case 8:
			places := g.n(limit + 1)
			g.w("DECIMAL(", strconv.FormatFloat(g.someNumber(), 'g', -1, 64), ", ", strconv.Itoa(places), ")")
			return places
		default:
			places := g.n(limit + 1)
			g.w("DECIMALVALUE(\"", g.pick("12.30", "1.5", "0", "7", "-2.25"), "\", ", strconv.Itoa(places), ")")
			return places
		}
	}
	g.w("i1")
	return 0
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
	return "user=" + strconv.Quote(f.Ctx.User) + " kind=" + strconv.Quote(f.Ctx.Kind) + " for=" + strconv.Quote(f.Ctx.For) +
		" groups=" + strings.Join(f.Ctx.Groups, ",") + " now=" + formula.DateTime(f.Ctx.Now).String()
}

// CaseJSON renders the formula with its inputs and context as a suite case
// whose expectation is the given value or error code, ready to paste into
// conformance/formula. The suite variables are those of Variables.
func (f *Formula) CaseJSON(result formula.Value, code string, t formula.Type) string {
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
	if f.Ctx.Kind != "" {
		b.WriteString(`, "kind": ` + strconv.Quote(f.Ctx.Kind))
	}
	if f.Ctx.For != "" {
		b.WriteString(`, "for": ` + strconv.Quote(f.Ctx.For))
	}
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
	b.WriteString(`, "now": ` + strconv.Quote(formula.DateTime(f.Ctx.Now).String()) + "}")
	if t.Kind.IsDateLike() || t.Kind.IsExact() {
		b.WriteString(`, "type": ` + strconv.Quote(t.String()))
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
	case formula.KindDate, formula.KindDateTime, formula.KindDecimal, formula.KindMoney:
		return strconv.Quote(v.String())
	}
	return strconv.Quote(v.Str())
}
