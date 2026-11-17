// Command cases writes the evaluation cases of the conformance suite
// (conformance/formula) as the browser VM runs them: each formula bound
// and encoded in the bytecode of the Go engine, its inputs in slot order
// and its expected value in a typed form, its context, and the limits of
// its suite (ADR-0003, ADR-0029). The browser VM's runner reads this
// file and runs every case through the built JavaScript file, so the
// browser never parses a formula, as in production. Nothing it writes is
// kept in the repository: make vmtest makes it each time.
//
// A case whose program reads a table, LOOKUP and its aggregates, is
// marked server: those functions run at the server and the browser is
// given their last value (ADR-0003).
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// value is a run-time value in a typed form that loses nothing: the kind,
// and the number, the text, or the mantissa as decimal digits with its
// scale and currency.
type value struct {
	Kind     string   `json:"k"`
	Num      *float64 `json:"n,omitempty"`
	Str      *string  `json:"s,omitempty"`
	Mant     string   `json:"m,omitempty"`
	Scale    *int     `json:"sc,omitempty"`
	Currency string   `json:"c,omitempty"`
}

func typed(v formula.Value) value {
	out := value{Kind: v.Kind().String()}
	switch v.Kind() {
	case formula.KindNull:
	case formula.KindBool, formula.KindNumber, formula.KindDate, formula.KindDateTime:
		n := v.Num()
		out.Num = &n
	case formula.KindDecimal, formula.KindMoney:
		sc := v.Scale()
		out.Mant, out.Scale, out.Currency = strconv.FormatInt(v.Mant(), 10), &sc, v.Currency()
	default:
		s := v.Str()
		out.Str = &s
	}
	return out
}

// life is the document's life as the browser VM's context carries it.
type life struct {
	Version     int64               `json:"version,omitempty"`
	CreatedAt   int64               `json:"created_at,omitempty"`
	CreatedBy   string              `json:"created_by,omitempty"`
	UpdatedAt   int64               `json:"updated_at,omitempty"`
	UpdatedBy   string              `json:"updated_by,omitempty"`
	EnteredAt   int64               `json:"entered_at,omitempty"`
	EnteredBy   string              `json:"entered_by,omitempty"`
	EnteredFrom string              `json:"entered_from,omitempty"`
	States      []formula.StateLife `json:"states,omitempty"`
}

type context struct {
	User   string   `json:"user,omitempty"`
	Groups []string `json:"groups,omitempty"`
	System []string `json:"system,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	For    string   `json:"for,omitempty"`
	Now    int64    `json:"now,omitempty"`
	State  string   `json:"state,omitempty"`
	ID     string   `json:"id,omitempty"`
	Doc    *life    `json:"doc,omitempty"`
}

type exported struct {
	Label    string   `json:"label"`
	Program  string   `json:"program,omitempty"`
	Result   string   `json:"result,omitempty"`
	Inputs   []value  `json:"inputs,omitempty"`
	Context  *context `json:"context,omitempty"`
	MaxText  int      `json:"max_text"`
	Expected *value   `json:"expected,omitempty"`
	Error    string   `json:"error,omitempty"`
	Server   bool     `json:"server,omitempty"`
}

type suite struct {
	Suite string     `json:"suite"`
	Cases []exported `json:"cases"`
}

// rendering is a number and the text the engine renders it as, by the
// %.15g rule of TEXT (ADR-0019), for the runner to hold the browser's
// rendering to.
type rendering struct {
	Num  float64 `json:"n"`
	Text string  `json:"s"`
}

type file struct {
	Builtins []string    `json:"builtins"`
	Suites   []suite     `json:"suites"`
	Renders  []rendering `json:"renders"`
}

// renders are numbers the cases do not reach: the edges of float64, ties
// at the fifteenth digit, which JavaScript and Go round differently, and
// numbers drawn at random over every exponent, the same ones each run.
func renders() []rendering {
	nums := []float64{0.1 + 0.2, 1e15, 1e14, 999999999999999, 1e-4, 1e-5, 123456789012345.5, 123456789012344.5, 0.5, 2.5e-7,
		math.MaxFloat64, math.SmallestNonzeroFloat64, 2.2250738585072014e-308, 1 << 53, 9007199254740993, -1.5, 1 / 3.0, 2 / 3.0}
	r := rand.New(rand.NewPCG(67, 2026))
	for len(nums) < 3000 {
		var f float64
		switch r.IntN(3) {
		case 0:
			f = math.Float64frombits(r.Uint64())
		case 1:
			f = float64(r.Int64N(1e17)) / math.Pow(10, float64(r.IntN(20)))
		default:
			// Halfway at the fifteenth digit when exact: an integer of 15
			// digits plus a half.
			f = float64(r.Int64N(9e14)+1e14) + 0.5
		}
		if !math.IsNaN(f) && !math.IsInf(f, 0) {
			nums = append(nums, f)
		}
	}
	out := make([]rendering, len(nums))
	for i, f := range nums {
		out[i] = rendering{Num: f, Text: formula.Number(f).String()}
	}
	return out
}

func main() {
	dir := filepath.Join("conformance", "formula")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := run(dir); err != nil {
		fmt.Fprintln(os.Stderr, "cases:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	suites, err := formula.LoadSuites(dir)
	if err != nil {
		return err
	}
	out := file{Builtins: formula.BuiltinNames(), Renders: renders()}
	for _, s := range suites {
		scope, err := s.Scope()
		if err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		sources, err := s.SourceSet()
		if err != nil {
			return fmt.Errorf("%s: %w", s.File, err)
		}
		limit := s.EvalLimits().MaxText
		if limit <= 0 {
			limit = formula.DefaultMaxText
		}
		got := suite{Suite: s.Suite}
		for i, c := range s.Cases {
			label := fmt.Sprintf("%s[%d] %s", s.Suite, i, c.Formula)
			if c.Name != "" {
				label = fmt.Sprintf("%s[%d] %q: %s", s.Suite, i, c.Name, c.Formula)
			}
			e := exported{Label: label, MaxText: limit, Error: c.Error}
			prog, err := formula.Bind(c.Formula, scope, formula.BindOptions{Sources: sources})
			if err != nil {
				return fmt.Errorf("%s: bind: %w", label, err)
			}
			e.Program, e.Result = base64.StdEncoding.EncodeToString(prog.Encode()), prog.Result.String()
			e.Server = len(prog.Queries) > 0
			for _, slot := range prog.Slots {
				raw, ok := c.Inputs[slot.Name]
				if !ok {
					e.Inputs = append(e.Inputs, typed(formula.Blank()))
					continue
				}
				v, err := formula.ParseCaseValue(raw, slot.Type)
				if err != nil {
					return fmt.Errorf("%s: input %s: %w", label, slot.Name, err)
				}
				e.Inputs = append(e.Inputs, typed(v))
			}
			ctx, err := c.Context.Context()
			if err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
			if ctx != nil {
				e.Context = &context{User: ctx.User, Groups: ctx.Groups, System: ctx.System, Kind: ctx.Kind, For: ctx.For, Now: ctx.Now, State: ctx.State, ID: ctx.ID}
				if d := ctx.Doc; d != nil {
					e.Context.Doc = &life{Version: d.Version, CreatedAt: d.CreatedAt, CreatedBy: d.CreatedBy, UpdatedAt: d.UpdatedAt, UpdatedBy: d.UpdatedBy,
						EnteredAt: d.EnteredAt, EnteredBy: d.EnteredBy, EnteredFrom: d.EnteredFrom, States: d.States}
				}
			}
			if c.Error == "" {
				want, err := formula.ParseCaseValue(c.Expected, prog.Result)
				if err != nil {
					return fmt.Errorf("%s: expected: %w", label, err)
				}
				t := typed(want)
				e.Expected = &t
			}
			got.Cases = append(got.Cases, e)
		}
		out.Suites = append(out.Suites, got)
	}
	drawnSuite, err := drawn()
	if err != nil {
		return err
	}
	out.Suites = append(out.Suites, drawnSuite)
	exactSuite, err := drawnExact()
	if err != nil {
		return err
	}
	out.Suites = append(out.Suites, exactSuite)
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(out)
}

// drawn is a suite the conformance files do not hold: calls of the
// functions of dates drawn at random, the same ones each run, worked out
// by the Go VM. The edges are what it is for: a month or a day that runs
// over into another year, year 1 and year 9999, the instants before
// 1970, and texts that are almost a date.
func drawn() (suite, error) {
	scope, err := formula.ScopeOf(map[string]string{"Y": "number", "M": "number", "D": "number", "N": "number",
		"T": "date", "U": "date", "S": "datetime", "R": "datetime", "X": "text"}, nil)
	if err != nil {
		return suite{}, err
	}
	r := rand.New(rand.NewPCG(2026, 11))
	day := func() formula.Value {
		if r.IntN(10) == 0 {
			return formula.Date(-719162 + r.Int64N(30)) // year 1
		}
		if r.IntN(10) == 0 {
			return formula.Date(2932896 - r.Int64N(30)) // year 9999
		}
		return formula.Date(r.Int64N(2932896+719162) - 719162)
	}
	instant := func() formula.Value {
		return formula.DateTime(r.Int64N(253402300799+62135596800) - 62135596800)
	}
	num := func(lo, hi int64) formula.Value {
		if r.IntN(8) == 0 {
			return formula.Number(float64(r.Int64N(hi-lo)+lo) + r.Float64())
		}
		return formula.Number(float64(r.Int64N(hi-lo) + lo))
	}
	texts := func() formula.Value {
		y, m, d := r.IntN(10001), r.IntN(14), r.IntN(33)
		forms := []string{
			fmt.Sprintf("%04d-%02d-%02d", y, m, d),
			fmt.Sprintf("%04d-%d-%02d", y, m, d),
			fmt.Sprintf("  %04d-%02d-%02d ", y, m, d),
			fmt.Sprintf("%04d-%02d-%02d %d:%02d:%02d", y, m, d, r.IntN(26), r.IntN(61), r.IntN(61)),
			fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%d", y, m, d, r.IntN(24), r.IntN(60), r.IntN(60), r.IntN(1000)),
			fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d,5", y, m, d, r.IntN(24), r.IntN(60), r.IntN(60)),
			fmt.Sprintf("%+04d-%02d-%02d", y%1000, m, d),
			fmt.Sprintf("%04d/%02d/%02d", y, m, d),
			fmt.Sprintf("%04d-%02d-%02dT10:00:00", y, m, d),
		}
		return formula.Text(forms[r.IntN(len(forms))])
	}
	calls := []call{
		{"DATE(Y, M, D)", func() map[string]formula.Value {
			return map[string]formula.Value{"Y": num(-50, 10050), "M": num(-40, 40), "D": num(-800, 800)}
		}},
		{"ADDMONTHS(T, N)", func() map[string]formula.Value { return map[string]formula.Value{"T": day(), "N": num(-400, 400)} }},
		{"ADDMONTHS(S, N)", func() map[string]formula.Value { return map[string]formula.Value{"S": instant(), "N": num(-400, 400)} }},
		{"ADDYEARS(T, N)", func() map[string]formula.Value { return map[string]formula.Value{"T": day(), "N": num(-30, 30)} }},
		{"ADDDAYS(T, N)", func() map[string]formula.Value { return map[string]formula.Value{"T": day(), "N": num(-1000, 1000)} }},
		{"ADDDAYS(S, N)", func() map[string]formula.Value {
			return map[string]formula.Value{"S": instant(), "N": num(-1000, 1000)}
		}},
		{"MONTHSBETWEEN(T, U)", func() map[string]formula.Value { return map[string]formula.Value{"T": day(), "U": day()} }},
		{"YEARSBETWEEN(S, R)", func() map[string]formula.Value { return map[string]formula.Value{"S": instant(), "R": instant()} }},
		{"DAYS(S, R)", func() map[string]formula.Value { return map[string]formula.Value{"S": instant(), "R": instant()} }},
		{"YEAR(S) * 10000 + MONTH(S) * 100 + DAY(S) + QUARTER(S) / 10", func() map[string]formula.Value { return map[string]formula.Value{"S": instant()} }},
		{"TEXT(T) & \" \" & TEXT(S)", func() map[string]formula.Value { return map[string]formula.Value{"T": day(), "S": instant()} }},
		{"DATEVALUE(X)", func() map[string]formula.Value { return map[string]formula.Value{"X": texts()} }},
		{"DATETIMEVALUE(X)", func() map[string]formula.Value { return map[string]formula.Value{"X": texts()} }},
		{"DAYSBETWEEN(DATEOF(S), T) + DAYSBETWEEN(DATETIME(T), R)", func() map[string]formula.Value {
			return map[string]formula.Value{"S": instant(), "R": instant(), "T": day()}
		}},
	}
	return runDrawn("dates drawn", scope, calls, 300)
}

// call is a formula and how to draw the values of its variables.
type call struct {
	formula string
	draw    func() map[string]formula.Value
}

// runDrawn binds each formula, draws its variables per times and has the
// Go VM work each case out: its value or the code it stops with.
func runDrawn(name string, scope *formula.Scope, calls []call, per int) (suite, error) {
	vm := formula.NewVM()
	got := suite{Suite: name}
	for _, c := range calls {
		prog, err := formula.Bind(c.formula, scope, formula.BindOptions{})
		if err != nil {
			return suite{}, fmt.Errorf("%s: %w", c.formula, err)
		}
		code := base64.StdEncoding.EncodeToString(prog.Encode())
		for i := 0; i < per; i++ {
			given := c.draw()
			e := exported{Label: fmt.Sprintf("%s: %s with", name, c.formula), Program: code, Result: prog.Result.String(), MaxText: formula.DefaultMaxText}
			vars := make([]formula.Value, len(prog.Slots))
			for si, slot := range prog.Slots {
				vars[si] = given[slot.Name]
				e.Inputs = append(e.Inputs, typed(vars[si]))
				e.Label += " " + slot.Name + "=" + vars[si].String()
			}
			v, err := vm.Run(prog, vars, nil)
			if err != nil {
				ee, ok := err.(*formula.EvalError)
				if !ok {
					return suite{}, err
				}
				e.Error = ee.Code
			} else {
				t := typed(v)
				e.Expected = &t
			}
			got.Cases = append(got.Cases, e)
		}
	}
	return got, nil
}

// drawnExact is the same for exact numbers and money (ADR-0022): the
// operators at every pair of scales, the six methods of rounding on
// halves, a division's four more places, mantissas next to the limit of
// 64 bits, two currencies in one operation, and texts that are almost an
// amount.
func drawnExact() (suite, error) {
	scope, err := formula.ScopeOf(map[string]string{"P": "decimal(2)", "Q": "decimal(5)", "R": "int", "K": "money(2)", "L": "money(2)",
		"F": "number", "X": "text"}, nil)
	if err != nil {
		return suite{}, err
	}
	r := rand.New(rand.NewPCG(22, 2026))
	mant := func() int64 {
		switch r.IntN(10) {
		case 0:
			return math.MaxInt64 - r.Int64N(1000)
		case 1:
			return math.MinInt64 + r.Int64N(1000)
		case 2:
			return r.Int64N(1e16) - 5e15
		case 3:
			return 0
		default:
			return r.Int64N(2e8) - 1e8
		}
	}
	cur := func() string {
		if r.IntN(12) == 0 {
			return "EUR"
		}
		return "PLN"
	}
	vals := func() map[string]formula.Value {
		f := float64(r.Int64N(2e7)-1e7) / 1000
		if r.IntN(3) == 0 {
			f += 0.0005 * float64(1-2*r.IntN(2)) // a half at the third place
		}
		texts := []string{
			fmt.Sprintf("%d.%02d", r.Int64N(1e6)-5e5, r.IntN(100)),
			fmt.Sprintf("%d.%05d", r.Int64N(1e4), r.IntN(1e5)),
			fmt.Sprintf("%de%d", r.Int64N(1e4), r.IntN(40)-20),
			fmt.Sprintf("  -%d.%d ", r.Int64N(1e6), r.IntN(1000)),
			fmt.Sprintf("%d.%02d %s", r.Int64N(1e6), r.IntN(100), cur()),
			fmt.Sprintf("%d,%02d PLN", r.Int64N(1e6), r.IntN(100)),
			fmt.Sprintf("%d.%02d pln", r.Int64N(1e6), r.IntN(100)),
			fmt.Sprintf("%d%d", r.Int64N(1e10), r.Int64N(1e10)),
			".5", "1.", "-.25e1", "e3", "0.000", "+",
		}
		return map[string]formula.Value{
			"P": formula.Decimal(mant(), 2), "Q": formula.Decimal(mant(), 5), "R": formula.Decimal(mant()/1000, 0),
			"K": formula.Money(mant(), 2, cur()), "L": formula.Money(mant(), 2, cur()),
			"F": formula.Number(f), "X": formula.Text(texts[r.IntN(len(texts))]),
		}
	}
	var calls []call
	for _, f := range []string{
		"P + Q", "P - Q", "P * Q", "P / Q", "Q / R", "-P", "P%", "K + L", "K - L", "K * R", "K / R", "P < Q", "K = L",
		"ROUND(Q, 0)", "ROUND(Q, 2, \"half_up\")", "ROUND(Q, 2, \"half_even\")", "ROUND(Q, 2, \"down\")", "ROUND(Q, 2, \"up\")",
		"ROUND(Q, 2, \"floor\")", "ROUND(Q, 2, \"ceiling\")", "ROUND(K, 1, \"half_even\")", "ROUND(F, 2, \"half_even\")", "ROUND(F, 2, \"up\")",
		"DECIMAL(F, 2)", "DECIMAL(F, 2, \"half_even\")", "DECIMAL(F, 2, \"floor\")", "DECIMAL(Q, 3, \"ceiling\")",
		"FLOOR(Q, 1)", "CEILING(Q, 1)", "TRUNC(Q, 3)", "ABS(Q)", "SIGN(Q)", "NUMBER(Q)",
		"SUM(P, Q, R)", "MIN(P, Q)", "MAX(P, Q, R)", "AVERAGE(P, Q, R)", "MOD(P, Q)", "SUM(K, L)", "MOD(K, L)",
		"DECIMALVALUE(X, 2)", "DECIMALVALUE(X, 5)", "MONEYVALUE(X, 2)", "AMOUNT(K)", "CURRENCY(K)", "MONEY(P, \"EUR\")",
		"TEXT(K) & \" \" & TEXT(Q)", "IFERROR(P / R, -1)",
	} {
		calls = append(calls, call{f, vals})
	}
	return runDrawn("exact drawn", scope, calls, 100)
}
