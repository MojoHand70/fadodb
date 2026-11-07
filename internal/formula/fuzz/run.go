package fuzz

import (
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Outcome is one formula run on the VM and on a dialect.
type Outcome struct {
	Prog *formula.Program
	// BindErr is set when the formula did not bind: a generator bug.
	BindErr error
	// VM is the VM's value, and VMCode its error code when it failed.
	VM     formula.Value
	VMCode string
	// SQL is the dialect's result; SQLErr is set when the statement
	// itself failed, which is a divergence too.
	SQL    formula.SQLResult
	SQLErr error
	// Reason says how the targets disagree; "" when they agree.
	Reason string
}

// Agree reports whether the two targets produced the same result.
func (o *Outcome) Agree() bool { return o.BindErr == nil && o.Reason == "" }

var slotIndex = func() map[string]int {
	m := make(map[string]int, len(Variables))
	for i, v := range Variables {
		m[v.Name] = i
	}
	return m
}()

// Run binds f, runs it on the VM and through db on the dialect, and
// compares the two by the rules of the conformance suite: error codes
// must match and values must be equal, except that a Noisy program's
// number, or its rendering inside a text, may differ by Tolerance.
func Run(f *Formula, vm *formula.VM, db *sql.DB, d formula.Dialect) *Outcome {
	o := &Outcome{}
	prog, err := formula.Bind(f.Src, Scope, formula.BindOptions{})
	if err != nil {
		o.BindErr = err
		return o
	}
	o.Prog = prog
	vars := make([]formula.Value, len(prog.Slots))
	for i, s := range prog.Slots {
		vars[i] = f.Inputs[slotIndex[s.Name]]
	}
	ctx := f.Ctx
	v, err := vm.Run(prog, vars, &ctx)
	if err != nil {
		var ee *formula.EvalError
		if !errors.As(err, &ee) {
			o.Reason = "VM failed: " + err.Error()
			return o
		}
		o.VMCode = ee.Code
	} else {
		o.VM = v
	}
	o.SQL, o.SQLErr = formula.RunSQL(db, d, prog, vars, &ctx)
	if o.SQLErr != nil {
		o.Reason = "statement failed: " + o.SQLErr.Error()
		return o
	}
	o.Reason = disagreement(prog, o)
	return o
}

func describe(v formula.Value, code string) string {
	if code != "" {
		return "error " + code
	}
	return v.Kind().String() + " " + v.String()
}

func disagreement(prog *formula.Program, o *Outcome) string {
	if o.VMCode != "" || o.SQL.Code != "" {
		if o.VMCode != o.SQL.Code {
			return "VM " + describe(o.VM, o.VMCode) + ", SQL " + describe(o.SQL.Value, o.SQL.Code)
		}
		return ""
	}
	if formula.Equal(o.VM, o.SQL.Value) {
		return ""
	}
	if Noisy(prog) {
		switch o.VM.Kind() {
		case formula.KindNumber:
			if formula.NearlyEqual(o.SQL.Value, o.VM, Tolerance) {
				return ""
			}
		case formula.KindText:
			if textNearlyEqual(o.SQL.Value.Str(), o.VM.Str(), Tolerance) {
				return ""
			}
		}
	}
	return "VM " + describe(o.VM, "") + ", SQL " + describe(o.SQL.Value, "")
}

// Tolerance is the relative difference allowed between the targets for a
// number that went through an aggregate or a math-library function
// (ADR-0019).
const Tolerance = 1e-12

// Noisy reports whether the program's result may carry last-bit noise
// between the targets: a power, an exponential or a logarithm, which each
// engine's math library rounds its own way (ADR-0019). A number or a
// rendering of such a result compares to Tolerance; any other kind, a
// comparison or a date built from it, must still agree exactly, and a
// disagreement there is reported. SUM and AVERAGE add left to right on
// every evaluator and are exact.
func Noisy(p *formula.Program) bool {
	return p.HasOp(formula.OpPow) || p.Calls("POWER") || p.Calls("EXP") || p.Calls("LN") || p.Calls("LOG")
}

// textNearlyEqual compares two texts run by run: every run of number
// characters must parse to numbers within the tolerance, and everything
// between the runs must match exactly. It is how a rendered noisy number
// inside a text result is compared. A concatenation may glue two numbers
// into one run, so a run that is not one number is tried as two at every
// split; runs are parsed as big floats, which no exponent overflows.
func textNearlyEqual(a, b string, rel float64) bool {
	ra, rb := numberRuns(a), numberRuns(b)
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if !runsNearlyEqual(ra[i], rb[i], rel) {
			return false
		}
	}
	return true
}

func runsNearlyEqual(a, b string, rel float64) bool {
	if a == b {
		return true
	}
	if numbersNearlyEqual(a, b, rel) {
		return true
	}
	if len(a) > 120 || len(b) > 120 {
		return false
	}
	for i := 1; i < len(a); i++ {
		for j := 1; j < len(b); j++ {
			if numbersNearlyEqual(a[:i], b[:j], rel) && runsNearlyEqual(a[i:], b[j:], rel) {
				return true
			}
		}
	}
	return false
}

func numbersNearlyEqual(a, b string, rel float64) bool {
	x, _, errA := big.ParseFloat(a, 10, 80, big.ToNearestEven)
	y, _, errB := big.ParseFloat(b, 10, 80, big.ToNearestEven)
	if errA != nil || errB != nil {
		return false
	}
	diff := new(big.Float).Sub(x, y)
	diff.Abs(diff)
	scale := new(big.Float).Abs(y)
	if scale.Cmp(big.NewFloat(1)) < 0 {
		scale = big.NewFloat(1)
	}
	return diff.Cmp(scale.Mul(scale, big.NewFloat(rel))) <= 0
}

// numberRuns splits a text into alternating runs, outside a number and
// inside one: a number run is digits and dots, an exponent mark after a
// digit, and a sign right after the exponent mark. A sign elsewhere, as
// in a negative number or a date, stays outside, where it must match
// exactly.
func numberRuns(s string) []string {
	var runs []string
	start, inNum := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		prev := byte(0)
		if i > 0 {
			prev = s[i-1]
		}
		isNum := c >= '0' && c <= '9' || c == '.' ||
			(c == 'e' || c == 'E') && inNum && prev >= '0' && prev <= '9' ||
			(c == '+' || c == '-') && inNum && (prev == 'e' || prev == 'E')
		if isNum != inNum {
			runs = append(runs, s[start:i])
			start, inNum = i, isNum
		}
	}
	return append(runs, s[start:])
}

// Report renders the outcome for a person: the formula, how to reproduce
// it, both results, the statement, and the formula as a suite case with
// the VM's result as the expectation.
func (o *Outcome) Report(f *Formula, d formula.Dialect) string {
	var b strings.Builder
	fmt.Fprintf(&b, "formula: %s\n", f.Src)
	fmt.Fprintf(&b, "seed:    %d  (FADODB_FUZZ_SEED=%d FADODB_FUZZ_N=1 FADODB_FUZZ_MAXTEXT=%d)\n", f.Seed, f.Seed, d.Limits().MaxText)
	fmt.Fprintf(&b, "inputs:  %s\n", f.DescribeInputs())
	fmt.Fprintf(&b, "context: %s\n", f.DescribeContext())
	if o.BindErr != nil {
		fmt.Fprintf(&b, "bind:    %v\n", o.BindErr)
		return b.String()
	}
	fmt.Fprintf(&b, "VM:      %s\n", describe(o.VM, o.VMCode))
	if o.SQLErr != nil {
		fmt.Fprintf(&b, "%-8s %v\n", d.Name()+":", o.SQLErr)
	} else {
		fmt.Fprintf(&b, "%-8s %s\n", d.Name()+":", describe(o.SQL.Value, o.SQL.Code))
	}
	fmt.Fprintf(&b, "reason:  %s\n", o.Reason)
	if o.SQL.Query != "" {
		fmt.Fprintf(&b, "query:   %s\n", o.SQL.Query)
	}
	fmt.Fprintf(&b, "case:    %s\n", f.CaseJSON(o.VM, o.VMCode, o.Prog.Result))
	return b.String()
}

// Config sizes a campaign.
type Config struct {
	// Seed is the seed of the first formula; formula i uses Seed + i.
	Seed uint64
	// N is the number of formulas.
	N int
	// Workers is the number of goroutines, each with its own VM and
	// database connection.
	Workers int
	// MaxText is the text limit the formulas evaluate under (ADR-0021).
	// It is small by default, so that short inputs reach the boundary
	// often and a campaign never builds a long text.
	MaxText int
}

// Defaults.
const (
	DefaultSeed    = 20261106
	DefaultN       = 2000
	DefaultMaxText = 1024
	envSeed        = "FADODB_FUZZ_SEED"
	envN           = "FADODB_FUZZ_N"
	envWorkers     = "FADODB_FUZZ_WORKERS"
	envMaxText     = "FADODB_FUZZ_MAXTEXT"
	maxReports     = 5
	maxBrief       = 200
)

// ConfigFromEnv reads FADODB_FUZZ_SEED, FADODB_FUZZ_N, FADODB_FUZZ_WORKERS
// and FADODB_FUZZ_MAXTEXT, with the defaults for what is unset.
func ConfigFromEnv() (Config, error) {
	cfg := Config{Seed: DefaultSeed, N: DefaultN, Workers: runtime.GOMAXPROCS(0), MaxText: DefaultMaxText}
	if s := os.Getenv(envSeed); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", envSeed, err)
		}
		cfg.Seed = v
	}
	if s := os.Getenv(envN); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			return cfg, fmt.Errorf("%s: want a positive count, got %q", envN, s)
		}
		cfg.N = v
	}
	if s := os.Getenv(envWorkers); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			return cfg, fmt.Errorf("%s: want a positive count, got %q", envWorkers, s)
		}
		cfg.Workers = v
	}
	if s := os.Getenv(envMaxText); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < formula.MinMaxText {
			return cfg, fmt.Errorf("%s: want a limit of at least %d, got %q", envMaxText, formula.MinMaxText, s)
		}
		cfg.MaxText = v
	}
	return cfg, nil
}

// Stats is what a campaign found.
type Stats struct {
	Formulas int
	// VMErrors counts formulas the VM ended with an error code; the
	// rest produced a value.
	VMErrors int
	// Divergences counts formulas the targets disagreed on; Reports holds
	// the first few, rendered.
	Divergences int
	Reports     []string
	// Brief has one line per divergence, seed, reason and formula, for
	// the first maxBrief of them, to see the classes at a glance.
	Brief   []string
	Elapsed time.Duration
}

// Campaign generates cfg.N formulas and runs each on the VM and on the
// dialect under cfg.MaxText, over cfg.Workers goroutines; open gives each
// worker its own database. A formula that does not bind stops the
// campaign with an error, since the generator promises to bind.
func Campaign(cfg Config, d formula.Dialect, open func() (*sql.DB, error)) (Stats, error) {
	start := time.Now()
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Workers > cfg.N {
		cfg.Workers = cfg.N
	}
	if cfg.MaxText < formula.MinMaxText {
		cfg.MaxText = DefaultMaxText
	}
	d = d.WithLimits(formula.Limits{MaxText: cfg.MaxText})
	var (
		mu    sync.Mutex
		stats Stats
		fatal error
		wg    sync.WaitGroup
	)
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			db, err := open()
			if err != nil {
				mu.Lock()
				if fatal == nil {
					fatal = err
				}
				mu.Unlock()
				return
			}
			defer db.Close()
			vm := formula.NewVM()
			vm.Limits.MaxText = cfg.MaxText
			local := Stats{}
			var reports, brief []string
			for i := w; i < cfg.N; i += cfg.Workers {
				mu.Lock()
				stop := fatal != nil
				mu.Unlock()
				if stop {
					break
				}
				f := New(cfg.Seed + uint64(i)).Formula()
				o := Run(f, vm, db, d)
				if o.BindErr != nil {
					mu.Lock()
					if fatal == nil {
						fatal = fmt.Errorf("generated formula does not bind:\n%s", o.Report(f, d))
					}
					mu.Unlock()
					break
				}
				local.Formulas++
				if o.VMCode != "" {
					local.VMErrors++
				}
				if !o.Agree() {
					local.Divergences++
					if len(reports) < maxReports {
						reports = append(reports, o.Report(f, d))
					}
					if len(brief) < maxBrief {
						brief = append(brief, fmt.Sprintf("seed %d: %s: %s", f.Seed, o.Reason, f.Src))
					}
				}
			}
			mu.Lock()
			stats.Formulas += local.Formulas
			stats.VMErrors += local.VMErrors
			stats.Divergences += local.Divergences
			for _, r := range reports {
				if len(stats.Reports) < maxReports {
					stats.Reports = append(stats.Reports, r)
				}
			}
			for _, b := range brief {
				if len(stats.Brief) < maxBrief {
					stats.Brief = append(stats.Brief, b)
				}
			}
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	stats.Elapsed = time.Since(start)
	return stats, fatal
}

// Summary renders the counts in one line.
func (s Stats) Summary(target string) string {
	return fmt.Sprintf("%d formulas on %s in %s: %d ended in an error code on the VM, %d divergences",
		s.Formulas, target, s.Elapsed.Round(time.Millisecond), s.VMErrors, s.Divergences)
}
