package formula

import (
	"strings"
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
		"IFERROR(a / 0, b)",
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
	// The exact path allocates nothing either (ADR-0022).
	exact := mustScope(t, Variable{Name: "x", Type: DecimalType(2)}, Variable{Name: "m", Type: MoneyType(2)}, Variable{Name: "i", Type: DecimalType(0)})
	evars := []Value{Decimal(150, 2), Money(999, 2, "PLN"), Decimal(3, 0)}
	for _, src := range []string{
		"x * i + x / 3 - 1",
		"ROUND(m * i * (1 - 10%), 2)",
		"SUM(x, i, 2.5) > m",
		"IF(x > 1, m, m * 2) + 1",
	} {
		p := mustBind(t, src, exact)
		if _, err := vm.Run(p, evars, nil); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		allocs := testing.AllocsPerRun(200, func() {
			if _, err := vm.Run(p, evars, nil); err != nil {
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
		"IFERROR(a / 0, -1)":  Number(-1),
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
	if ee.Error() != `div_by_zero: / by zero at offset 6` {
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

// BenchmarkVM_Exact5 is the five-operation formula on exact values
// (ADR-0022), beside the float one.
func BenchmarkVM_Exact5(b *testing.B) {
	scope := mustScope(b, Variable{Name: "a", Type: DecimalType(2)}, Variable{Name: "b", Type: DecimalType(2)},
		Variable{Name: "c", Type: DecimalType(2)}, Variable{Name: "d", Type: DecimalType(2)}, Variable{Name: "e", Type: DecimalType(2)})
	p := mustBind(b, "a * b + c / d - e", scope)
	vm := NewVM()
	vars := []Value{Decimal(200, 2), Decimal(300, 2), Decimal(1000, 2), Decimal(400, 2), Decimal(100, 2)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.Run(p, vars, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVM_MoneyLine is an invoice line on money: a product, a
// discount and a rounding (ADR-0022).
func BenchmarkVM_MoneyLine(b *testing.B) {
	scope := mustScope(b, Variable{Name: "qty", Type: DecimalType(0)}, Variable{Name: "price", Type: MoneyType(2)}, Variable{Name: "discount", Type: DecimalType(2)})
	p := mustBind(b, "ROUND(qty * price * (1 - discount%), 2)", scope)
	vm := NewVM()
	vars := []Value{Decimal(3, 0), Money(999, 2, "PLN"), Decimal(1000, 2)}
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

// The VM remembers the json text it parsed last and the one it was told
// last (ADR-0085): JSONVALUE reads either without parsing again, and
// JSONList answers the elements of a list a path leads to.
func TestVM_RemembersTheJSONItParsed(t *testing.T) {
	scope, err := NewScope(Variable{Name: "BODY", Type: Scalar(KindJSON)}, Variable{Name: "item", Type: Scalar(KindJSON)})
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Bind(`JSONVALUE(item, "sku") & " of " & JSONVALUE(BODY, "order.number")`, scope, BindOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// given hands the program the body and one element, each in the
	// slot the program reads it from.
	given := func(body, item Value) []Value {
		vars := make([]Value, len(prog.Slots))
		for i, slot := range prog.Slots {
			if slot.Name == "BODY" {
				vars[i] = body
			} else {
				vars[i] = item
			}
		}
		return vars
	}
	vm := NewVM()
	body := `{"order": {"number": "1001", "lines": [{"sku": "SOFA-3"}, {"sku": "CHAIR-1"}], "none": null, "text": "x"}}`
	elements, found, err := vm.JSONList(body, "order.lines")
	if err != nil || !found || len(elements) != 2 {
		t.Fatalf("the lines of the order: %v, %v, %v", elements, found, err)
	}
	for i, want := range []string{"SOFA-3 of 1001", "CHAIR-1 of 1001"} {
		// The element is told to the VM under a text that is no json at
		// all: a formula that read it by parsing would fail.
		told := "element " + string(rune('a'+i))
		vm.SeedJSON(told, elements[i])
		got, err := vm.Run(prog, given(JSON(body), JSON(told)), nil)
		if err != nil || got.Str() != want {
			t.Errorf("element %d: %v, %v; want %s", i+1, got, err, want)
		}
	}
	for path, want := range map[string]string{"order.none": "nothing", "order.nowhere": "nothing", "order.lines.5": "nothing", "order.text": "no list", "order": "no list", "": "no list"} {
		_, found, err := vm.JSONList(body, path)
		switch {
		case want == "nothing" && (found || err != nil):
			t.Errorf("%q: found %v, %v; want nothing there", path, found, err)
		case want == "no list" && (err == nil || !strings.Contains(err.Error(), "no list")):
			t.Errorf("%q: %v; want what is there to be no list", path, err)
		}
	}
	if _, _, err := vm.JSONList(`{"order": `, "order"); err == nil {
		t.Error("a text that is no json has a list")
	}
	// Once it has let go, a text that is no json is one again.
	vm.ForgetJSON()
	if _, err := vm.Run(prog, given(JSON(body), JSON("element a")), nil); err == nil {
		t.Error("the VM still reads an element it was told to forget")
	}
}
