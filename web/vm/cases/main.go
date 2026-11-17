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
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(out)
}
