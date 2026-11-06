package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

const formulaUsage = `fadodb formula eval - run a formula through the engine

Usage:
  fadodb formula eval [flags] FORMULA

Evaluates the formula on the Go VM and prints the result in the notation
of the conformance suite: a number plain, text quoted, a date as
YYYY-MM-DD or YYYY-MM-DD HH:MM:SS, TRUE or FALSE, and BLANK. A formula
that does not parse or bind, or stops with an error code, prints the
reason and exits 1; a usage mistake exits 2.

Flags:
  -var name:type[=value]   declare a variable, blank without a value; may
                           repeat. Types: number, text, bool, date, ref,
                           json. A date is YYYY-MM-DD or
                           YYYY-MM-DD HH:MM:SS, UTC.
  -user id                 the user the formula runs for (USER)
  -group name              a group of the user (INGROUP); may repeat
  -now date                the clock (NOW, TODAY); default the wall clock
  -max-text n              the text limit in code points (ADR-0021);
                           default 65536, at least 64
  -ast                     print the parsed formula as an s-expression
                           instead of evaluating
  -bytecode                print the bytecode in its JSON debug form
                           instead of evaluating
  -sql sqlite|duckdb       print the SQL generated for the dialect, the
                           value and the error expression over the
                           variables as columns, instead of evaluating

Examples:
  fadodb formula eval -var qty:number=3 -var price:number=9.99 'ROUND(qty * price, 2)'
  fadodb formula eval -var name:text=invoice 'UPPER(LEFT(name, 3)) & "-" & TEXT(42)'
  fadodb formula eval -user alice -group finance 'INGROUP("finance") AND USER() = "alice"'
  fadodb formula eval -sql duckdb -var due:date 'DAYS(due, TODAY())'
`

// multiFlag collects a repeatable flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// runFormula is the formula command: for now only eval.
func runFormula(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "eval":
			return runFormulaEval(args[1:], stdout, stderr)
		case "help", "-h", "--help":
			fmt.Fprint(stdout, formulaUsage)
			return 0
		}
		fmt.Fprintf(stderr, "fadodb formula: unknown command %q\n\n", args[0])
	}
	fmt.Fprint(stderr, formulaUsage)
	return 2
}

func runFormulaEval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fadodb formula eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {}
	var vars, groups multiFlag
	fs.Var(&vars, "var", "declare a variable, name:type[=value]")
	fs.Var(&groups, "group", "a group of the user")
	user := fs.String("user", "", "the user the formula runs for")
	now := fs.String("now", "", "the clock")
	maxText := fs.Int("max-text", formula.DefaultMaxText, "the text limit in code points")
	dialect := fs.String("sql", "", "print the SQL for a dialect")
	ast := fs.Bool("ast", false, "print the parsed formula")
	bytecode := fs.Bool("bytecode", false, "print the bytecode")
	usageErr := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "fadodb formula eval: "+format+"\n\n", a...)
		fmt.Fprint(stderr, formulaUsage)
		return 2
	}
	// Flags may come before or after the formula.
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprint(stdout, formulaUsage)
				return 0
			}
			fmt.Fprintln(stderr)
			fmt.Fprint(stderr, formulaUsage)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 {
		return usageErr("want one formula, got %d", len(positional))
	}
	src := positional[0]
	if *maxText < formula.MinMaxText {
		return usageErr("-max-text must be at least %d", formula.MinMaxText)
	}
	var d formula.Dialect
	switch *dialect {
	case "":
	case "sqlite":
		d = formula.NewSQLite()
	case "duckdb":
		d = formula.NewDuckDB()
	default:
		return usageErr("-sql must be sqlite or duckdb, got %q", *dialect)
	}
	var decls []formula.Variable
	values := map[string]formula.Value{}
	for _, spec := range vars {
		v, val, err := parseVar(spec)
		if err != nil {
			return usageErr("-var %s: %v", spec, err)
		}
		decls = append(decls, v)
		values[v.Name] = val
	}
	scope, err := formula.NewScope(decls...)
	if err != nil {
		return usageErr("%v", err)
	}
	ctx := &formula.Context{User: *user, Groups: groups}
	if *now != "" {
		secs, err := formula.ParseCaseDate(*now)
		if err != nil {
			return usageErr("-now: want YYYY-MM-DD or YYYY-MM-DD HH:MM:SS, got %q", *now)
		}
		ctx.Now = secs
	}

	if *ast {
		node, err := formula.Parse(src)
		if err != nil {
			fmt.Fprintf(stderr, "fadodb formula eval: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, formula.Sexpr(node))
		return 0
	}
	prog, err := formula.Bind(src, scope, formula.BindOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "fadodb formula eval: %v\n", err)
		return 1
	}
	if *bytecode {
		b, err := json.MarshalIndent(prog, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "fadodb formula eval: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if d != nil {
		d = d.WithLimits(formula.Limits{MaxText: *maxText})
		stmt, err := formula.GenerateSQL(prog, d, func(s formula.Slot) string {
			return `"` + strings.ReplaceAll(s.Name, `"`, `""`) + `"`
		})
		if err != nil {
			fmt.Fprintf(stderr, "fadodb formula eval: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "value  %s\nerror  %s\n", stmt.Value, stmt.ErrorOrNull())
		if len(stmt.Params) > 0 {
			fmt.Fprintf(stdout, "params %s\n", strings.Join(stmt.Params, ", "))
		}
		return 0
	}

	vals := make([]formula.Value, len(prog.Slots))
	for i, s := range prog.Slots {
		vals[i] = values[s.Name]
	}
	vm := formula.NewVM()
	vm.Limits.MaxText = *maxText
	v, err := vm.Run(prog, vals, ctx)
	if err != nil {
		fmt.Fprintf(stderr, "fadodb formula eval: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, v.String())
	return 0
}

// parseVar reads a -var argument, name:type[=value].
func parseVar(spec string) (formula.Variable, formula.Value, error) {
	name, rest, ok := strings.Cut(spec, ":")
	if !ok || name == "" {
		return formula.Variable{}, formula.Value{}, errors.New("want name:type[=value]")
	}
	typ, raw, hasValue := strings.Cut(rest, "=")
	t, err := formula.ParseType(typ)
	if err != nil || !t.IsScalar() || t.Kind == formula.KindNull {
		return formula.Variable{}, formula.Value{}, fmt.Errorf("type %q is not number, text, bool, date, ref or json", typ)
	}
	v := formula.Variable{Name: name, Type: t}
	if !hasValue {
		return v, formula.Blank(), nil
	}
	switch t.Kind {
	case formula.KindNumber:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return v, formula.Value{}, fmt.Errorf("%q is not a number", raw)
		}
		return v, formula.Number(f), nil
	case formula.KindBool:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true":
			return v, formula.Bool(true), nil
		case "false":
			return v, formula.Bool(false), nil
		}
		return v, formula.Value{}, fmt.Errorf("%q is not true or false", raw)
	case formula.KindDate:
		secs, err := formula.ParseCaseDate(strings.TrimSpace(raw))
		if err != nil {
			return v, formula.Value{}, fmt.Errorf("%q is not YYYY-MM-DD or YYYY-MM-DD HH:MM:SS", raw)
		}
		return v, formula.Date(secs), nil
	case formula.KindRef:
		return v, formula.Ref(raw), nil
	case formula.KindJSON:
		return v, formula.JSON(raw), nil
	}
	return v, formula.Text(raw), nil
}
