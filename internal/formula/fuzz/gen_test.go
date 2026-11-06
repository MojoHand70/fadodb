package fuzz

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Functions the binder refuses as not implemented yet, which the generator
// must therefore never produce; the keyword functions have no builtin.
var notGenerated = map[string]bool{
	"INDEX": true, "FILTER": true, "FIRST": true, "LAST": true, "CONTAINS": true, "JOIN": true,
	"LOOKUP": true, "QUERY": true, "IF": true, "AND": true, "OR": true, "NOT": true,
}

func TestGenerator_FormulasBindAndRun(t *testing.T) {
	const n = 3000
	vm := formula.NewVM()
	seen := map[string]bool{}
	kinds := map[formula.Kind]int{}
	errors := 0
	for i := 0; i < n; i++ {
		f := New(uint64(i)).Formula()
		prog, err := formula.Bind(f.Src, Scope, formula.BindOptions{})
		if err != nil {
			t.Fatalf("seed %d: %q does not bind: %v", i, f.Src, err)
		}
		if len(f.Src) > 2000 {
			t.Fatalf("seed %d: formula is %d bytes long", i, len(f.Src))
		}
		back, err := formula.Decode(prog.Encode())
		if err != nil {
			t.Fatalf("seed %d: %q: decode: %v", i, f.Src, err)
		}
		vars := make([]formula.Value, len(prog.Slots))
		for si, s := range prog.Slots {
			vars[si] = f.Inputs[slotIndex[s.Name]]
		}
		ctx := f.Ctx
		got, err1 := vm.Run(prog, vars, &ctx)
		again, err2 := vm.Run(back, vars, &ctx)
		if (err1 == nil) != (err2 == nil) || (err1 == nil && !formula.Equal(got, again)) {
			t.Fatalf("seed %d: %q: decoded program differs: %v %v / %v %v", i, f.Src, got, err1, again, err2)
		}
		if err1 != nil {
			errors++
		}
		kinds[prog.Result.Kind]++
		for _, fn := range formula.Functions() {
			if prog.Calls(fn.Name) {
				seen[fn.Name] = true
			}
		}
	}
	var missing []string
	for _, fn := range formula.Functions() {
		if !notGenerated[fn.Name] && !seen[fn.Name] {
			missing = append(missing, fn.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("functions never generated in %d formulas: %s", n, strings.Join(missing, ", "))
	}
	for _, k := range []formula.Kind{formula.KindNumber, formula.KindBool, formula.KindText, formula.KindDate} {
		if kinds[k] < n/20 {
			t.Errorf("only %d of %d formulas have type %s", kinds[k], n, k)
		}
	}
	if errors > n/2 {
		t.Errorf("%d of %d formulas end in an error on the VM; the generator should mostly produce values", errors, n)
	}
	t.Logf("%d formulas: %d end in an error; result kinds %v", n, errors, kinds)
}

func TestGenerator_IsReproducible(t *testing.T) {
	a, b := New(7).Formula(), New(7).Formula()
	if a.Src != b.Src || a.DescribeInputs() != b.DescribeInputs() || a.DescribeContext() != b.DescribeContext() {
		t.Fatalf("two generators with the same seed differ:\n%s\n%s", a.Src, b.Src)
	}
	if c := New(8).Formula(); c.Src == a.Src {
		t.Fatalf("seeds 7 and 8 generate the same formula %q", a.Src)
	}
}

func TestCaseJSON_LoadsAsSuite(t *testing.T) {
	f := New(3).Formula()
	prog, err := formula.Bind(f.Src, Scope, formula.BindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c := f.CaseJSON(formula.Number(1), "", prog.Result.Kind)
	if !strings.HasPrefix(c, `{"formula": `) || !strings.Contains(c, `"expected": 1}`) {
		t.Errorf("case = %s", c)
	}
	e := f.CaseJSON(formula.Blank(), "div_by_zero", formula.KindDate)
	if !strings.Contains(e, `"type": "date", "error": "div_by_zero"}`) {
		t.Errorf("case = %s", e)
	}
}

func TestTextNearlyEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"1.5 EUR", "1.5 EUR", true},
		{"982568862195717121927-06-22", "982568862195718121927-06-22", true},
		{"-6.57872320542217e+3030", "-6.5787232054222e+3030", true},
		{"-6.57872320542217e+30", "6.57872320542217e+30", false},
		{"x1.0000000001y", "x1y", false},
		{"2024-01-31", "2024-01-30", false},
		{"TRUE1e+15", "TRUE999999999999999", true},
	}
	for _, c := range cases {
		if got := textNearlyEqual(c.a, c.b, Tolerance); got != c.want {
			t.Errorf("textNearlyEqual(%q, %q) = %v, want %v (runs %q / %q)", c.a, c.b, got, c.want, numberRuns(c.a), numberRuns(c.b))
		}
	}
}
