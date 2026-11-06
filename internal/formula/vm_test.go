package formula

import (
	"testing"
)

func mustScope(t testing.TB, vars ...Variable) *Scope {
	t.Helper()
	s, err := NewScope(vars...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func numVars(names ...string) []Variable {
	vs := make([]Variable, len(names))
	for i, n := range names {
		vs[i] = Variable{Name: n, Type: Scalar(KindNumber)}
	}
	return vs
}

func mustBind(t testing.TB, src string, scope *Scope) *Program {
	t.Helper()
	p, err := Bind(src, scope, BindOptions{})
	if err != nil {
		t.Fatalf("Bind(%q): %v", src, err)
	}
	return p
}

// TestVM_ScalarPathDoesNotAllocate is the budget of ADR-0012: zero
// allocations on the scalar path.
func TestVM_ScalarPathDoesNotAllocate(t *testing.T) {
	scope := mustScope(t, numVars("a", "b", "c", "d", "e")...)
	vm := NewVM()
	vars := []Value{Number(2), Number(3), Number(10), Number(4), Number(1)}
	for _, src := range []string{
		"a * b + c / d - e",
		"IF(a > b, ROUND(c / d, 1), -e) ^ 2",
		"a > 0 AND b > 0 OR NOT c = 0",
		"ISBLANK(a) OR MOD(c, b) = 1",
		"(a + b) % * 100",
	} {
		p := mustBind(t, src, scope)
		if _, err := vm.Run(p, vars, nil); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		allocs := testing.AllocsPerRun(200, func() {
			if _, err := vm.Run(p, vars, nil); err != nil {
				t.Fatal(err)
			}
		})
		if allocs != 0 {
			t.Errorf("%s: %v allocations per run, want 0", src, allocs)
		}
	}
}

func TestVM_Results(t *testing.T) {
	scope := mustScope(t, numVars("a", "b")...)
	vm := NewVM()
	vars := []Value{Number(7), Number(2)}
	cases := map[string]Value{
		"a * b + 1":           Number(15),
		"a / 0":               Number(0),
		"a > b":               Bool(true),
		"a & \"-\" & b":       Text("7-2"),
		"IF(a > b, a, b)":     Number(7),
		"MOD(a, b)":           Number(1),
		"BLANK":               Blank(),
		"IF(a < b, 1, BLANK)": Blank(),
	}
	for src, want := range cases {
		p := mustBind(t, src, scope)
		got, err := vm.Run(p, vars, nil)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if !Equal(got, want) {
			t.Errorf("%s = %s, want %s", src, got, want)
		}
	}
}

func TestVM_ErrorsCarryCodeAndPosition(t *testing.T) {
	scope := mustScope(t, numVars("a", "b")...)
	vm := NewVM()
	p := mustBind(t, `a + b \ 0`, scope)
	_, err := vm.Run(p, []Value{Number(1), Number(2)}, nil)
	ee, ok := err.(*EvalError)
	if !ok {
		t.Fatalf("error %T, want *EvalError", err)
	}
	if ee.Code != ErrDivByZero || ee.Pos != 6 {
		t.Errorf("error = %+v, want div_by_zero at offset 6", ee)
	}
	if ee.Error() != `div_by_zero: \ by zero at offset 6` {
		t.Errorf("Error() = %q", ee.Error())
	}
}

func TestVM_TooFewVariables(t *testing.T) {
	scope := mustScope(t, numVars("a", "b")...)
	p := mustBind(t, "a + b", scope)
	if _, err := NewVM().Run(p, []Value{Number(1)}, nil); err == nil {
		t.Error("running with one variable for two slots should fail")
	}
}

func TestVM_DecodedProgramRuns(t *testing.T) {
	scope := mustScope(t, numVars("qty", "price")...)
	p := mustBind(t, `IF(qty > 0, ROUND(qty * price, 2), 0) & " EUR"`, scope)
	q, err := Decode(p.Encode())
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewVM().Run(q, []Value{Number(3), Number(1.25)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(got, Text("3.75 EUR")) {
		t.Errorf("got %s", got)
	}
}

func BenchmarkVM_Arithmetic5(b *testing.B) {
	scope := mustScope(b, numVars("a", "b", "c", "d", "e")...)
	p := mustBind(b, "a * b + c / d - e", scope)
	vm := NewVM()
	vars := []Value{Number(2), Number(3), Number(10), Number(4), Number(1)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Run(p, vars, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVM_IfRoundCompare(b *testing.B) {
	scope := mustScope(b, numVars("qty", "price", "discount")...)
	p := mustBind(b, "IF(qty > 0 AND NOT ISBLANK(price), ROUND(qty * price * (1 - discount%), 2), 0)", scope)
	vm := NewVM()
	vars := []Value{Number(3), Number(9.99), Number(10)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Run(p, vars, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVM_Text(b *testing.B) {
	scope := mustScope(b, Variable{Name: "name", Type: Scalar(KindText)}, Variable{Name: "n", Type: Scalar(KindNumber)})
	p := mustBind(b, `UPPER(LEFT(name, 3)) & "-" & TEXT(n)`, scope)
	vm := NewVM()
	vars := []Value{Text("invoice"), Number(42)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Run(p, vars, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBind(b *testing.B) {
	scope := mustScope(b, numVars("qty", "price", "discount")...)
	src := "IF(qty > 0 AND NOT ISBLANK(price), ROUND(qty * price * (1 - discount%), 2), 0)"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Bind(src, scope, BindOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
