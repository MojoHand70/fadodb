package formula

import (
	"strings"
	"testing"
)

func TestBind_SlotsDependenciesAndClasses(t *testing.T) {
	scope := mustScope(t, Variable{Name: "Qty", Type: Scalar(KindNumber)}, Variable{Name: "Price", Type: Scalar(KindNumber)},
		Variable{Name: "Due", Type: Scalar(KindDate)})
	p := mustBind(t, "qty * PRICE + Qty + DAYS(Due, TODAY())", scope)
	if len(p.Slots) != 3 || p.Slots[0].Name != "Qty" || p.Slots[1].Name != "Price" || p.Slots[2].Name != "Due" {
		t.Errorf("slots = %+v, want Qty, Price, Due once each in first-use order", p.Slots)
	}
	if p.Result.Kind != KindNumber {
		t.Errorf("result type %s, want number", p.Result)
	}
	if len(p.Classes) != 2 || p.Classes[0] != ClassPure || p.Classes[1] != ClassContext {
		t.Errorf("classes = %v, want [pure context]", p.Classes)
	}
	if p.MaxStack < 2 || p.MaxStack > 4 {
		t.Errorf("MaxStack = %d, want a small number", p.MaxStack)
	}
	if len(p.Pos) != len(p.Code) {
		t.Errorf("positions: %d for %d instructions", len(p.Pos), len(p.Code))
	}
}

func TestBind_ConstantPoolDeduplicates(t *testing.T) {
	scope := mustScope(t, numVars("a")...)
	p := mustBind(t, `1 + a + 1 + 2 & "a" & "a" & "b"`, scope)
	if len(p.Nums) != 2 || len(p.Texts) != 2 {
		t.Errorf("pools = %v %q, want [1 2] [a b]", p.Nums, p.Texts)
	}
}

func TestBind_FoldsConstantExpressions(t *testing.T) {
	scope := mustScope(t, Variable{Name: "m", Type: MoneyType(2)}, Variable{Name: "n", Type: Scalar(KindNumber)})
	p := mustBind(t, `1 - 10%`, scope)
	if len(p.Code) != 2 || p.Code[0].Op != OpConstNum || p.Nums[0] != 1-0.1 {
		t.Errorf("1 - 10%% should fold to one constant, got %v %v", p.Code, p.Nums)
	}
	p = mustBind(t, `m * (1 - 10%)`, scope)
	if len(p.Decs) != 1 || p.Decs[0] != (DecConst{Mant: 90, Scale: 2}) || !p.Result.Equal(MoneyType(4)) {
		t.Errorf("m * (1 - 10%%) should read the constant exactly as 0.90, got %v, result %s", p.Decs, p.Result)
	}
	if _, err := Bind(`m * (1 / 3)`, scope, BindOptions{}); err == nil {
		t.Error("a division of constants is a number, not an exact constant")
	}
	if _, err := Bind(`n * (1 - 10%)`, scope, BindOptions{}); err != nil {
		t.Errorf("a folded constant still works with numbers: %v", err)
	}
}

func TestBind_Errors(t *testing.T) {
	scope := mustScope(t, Variable{Name: "qty", Type: Scalar(KindNumber)}, Variable{Name: "name", Type: Scalar(KindText)},
		Variable{Name: "due", Type: Scalar(KindDate)}, Variable{Name: "paid", Type: Scalar(KindBool)})
	cases := []struct {
		src     string
		wantMsg string
		wantCol int
	}{
		{"nope + 1", "unknown variable nope", 1},
		{"ROUND + 1", "ROUND is a function: write ROUND(number[, digits[, method]])", 1},
		{"qty + name", "+ needs numbers, got text; VALUE converts text to a number", 5},
		{"due + 1", "+ needs numbers, got date; dates move with ADDDAYS", 5},
		{"TRUE = 1", "= compares bool with number", 6},
		{"IF(qty, 1, 2)", "IF needs a condition, got number; compare it, for example <> 0", 4},
		{"IF(paid, 1, \"x\")", "IF branches must have the same type, got number and text", 1},
		{"NOT qty", "NOT needs a condition, got number", 1},
		{"qty AND paid", "AND needs conditions, got number", 5},
		{"UPPER(qty)", "UPPER: argument 1 must be text, got number; TEXT converts a number to text", 7},
		{"YEAR(name)", "YEAR: argument 1 must be a date or a datetime, got text; DATEVALUE converts text to a date", 6},
		{"THIS.qty", "THIS.qty is only valid inside LOOKUP and QUERY", 1},
		{`REGEXREPLACE(name, "(", "")`, `REGEXREPLACE: invalid pattern "("`, 1},
		{"LOOKUP(t, c, x = 1)", "unknown source t", 8},
	}
	for _, c := range cases {
		_, err := Bind(c.src, scope, BindOptions{})
		if err == nil {
			t.Errorf("Bind(%q): no error, want %q", c.src, c.wantMsg)
			continue
		}
		e, ok := err.(*Error)
		if !ok {
			t.Errorf("Bind(%q): error %T, want *Error", c.src, err)
			continue
		}
		if !strings.Contains(e.Msg, c.wantMsg) || e.Col != c.wantCol {
			t.Errorf("Bind(%q) = %q at column %d, want %q at column %d", c.src, e.Msg, e.Col, c.wantMsg, c.wantCol)
		}
	}
}

func TestBind_AutoModeRejectsContextFunctions(t *testing.T) {
	scope := mustScope(t, Variable{Name: "due", Type: Scalar(KindDate)})
	if _, err := Bind("DAYS(due, TODAY())", scope, BindOptions{Mode: ModeAuto}); err == nil || !strings.Contains(err.Error(), "TODAY is a context function and may not be used in an auto formula") {
		t.Errorf("auto mode: %v", err)
	}
	if _, err := Bind("DAYS(due, TODAY())", scope, BindOptions{}); err != nil {
		t.Errorf("any mode: %v", err)
	}
}

func TestBind_ObjectFieldPaths(t *testing.T) {
	address := ObjectType("Address", []Field{{"city", Scalar(KindText)}, {"country", Scalar(KindText)}})
	place := ObjectType("Place", []Field{{"name", Scalar(KindText)}, {"address", address}, {"lat", Scalar(KindNumber)}})
	scope := mustScope(t, Variable{Name: "Shipping", Type: address}, Variable{Name: "venue", Type: place}, Variable{Name: "n", Type: Scalar(KindNumber)})
	p := mustBind(t, `shipping.CITY & venue.Address.City & TEXT(VENUE.lat + n)`, scope)
	want := []Slot{{"Shipping.city", Scalar(KindText)}, {"venue.address.city", Scalar(KindText)}, {"venue.lat", Scalar(KindNumber)}, {"n", Scalar(KindNumber)}}
	if len(p.Slots) != len(want) {
		t.Fatalf("slots = %+v, want %+v", p.Slots, want)
	}
	for i := range want {
		if p.Slots[i].Name != want[i].Name || !p.Slots[i].Type.Equal(want[i].Type) {
			t.Errorf("slot %d = %+v, want %+v: a leaf is one slot named by its declared path", i, p.Slots[i], want[i])
		}
	}
	for _, tc := range []struct{ src, want string }{
		{"shipping", "whole objects are not supported yet"},
		{"venue.address", "whole objects are not supported yet"},
		{"shipping.street", "has no field street; its fields are city, country"},
		{"shipping.city.x", "is text and has no field x"},
		{"n.x", "only an object has fields"},
	} {
		_, err := Bind(tc.src, scope, BindOptions{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one containing %q", tc.src, err, tc.want)
		}
	}
	if _, err := NewScope(Variable{Name: "a", Type: ObjectType("S", []Field{{"if", Scalar(KindText)}})}); err == nil {
		t.Error("a field named by a keyword must be refused")
	}
	if _, err := NewScope(Variable{Name: "a", Type: ObjectType("S", []Field{{"x", Scalar(KindText)}, {"X", Scalar(KindText)}})}); err == nil {
		t.Error("two fields of one name in any case must be refused")
	}
}

func TestBind_PureModeAllowsPureFunctionsOnly(t *testing.T) {
	scope := mustScope(t, Variable{Name: "number", Type: Scalar(KindText)}, Variable{Name: "due", Type: Scalar(KindDate)})
	if _, err := Bind(`"INV-" & number & " " & TEXT(due)`, scope, BindOptions{Mode: ModePure}); err != nil {
		t.Errorf("a title over pure functions binds: %v", err)
	}
	for _, src := range []string{`"INV-" & USER()`, `DAYS(due, TODAY())`, `LOOKUP(Rates, rate, code = "EUR")`} {
		if _, err := Bind(src, scope, BindOptions{Mode: ModePure}); err == nil {
			t.Errorf("%s binds in pure mode, want a refusal", src)
		}
	}
}

func TestScope_RejectsDuplicatesAndKeywords(t *testing.T) {
	if _, err := NewScope(Variable{Name: "qty"}, Variable{Name: "QTY"}); err == nil {
		t.Error("duplicate names in different case should be rejected")
	}
	if _, err := NewScope(Variable{Name: "blank"}); err == nil {
		t.Error("a keyword as a variable name should be rejected")
	}
}

func TestBuiltins_MatchTheCatalog(t *testing.T) {
	seen := map[string]bool{}
	for i, b := range builtins {
		seen[b.name] = true
		fn := LookupFunction(b.name)
		if fn == nil {
			t.Errorf("builtin %s is not in the catalog", b.name)
			continue
		}
		if fn.Name != b.name {
			t.Errorf("builtin %s is the alias of %s; builtins use canonical names", b.name, fn.Name)
		}
		if fn.Class != b.class {
			t.Errorf("builtin %s has class %s, the catalog says %s", b.name, b.class, fn.Class)
		}
		if fn.MaxArgs() != 0 && len(b.args) == 0 {
			t.Errorf("builtin %s accepts arguments but types none", b.name)
		}
		if builtinByName[b.name] != i {
			t.Errorf("builtin %s indexed at %d, listed at %d", b.name, builtinByName[b.name], i)
		}
	}
	for _, fn := range Functions() {
		if specialForms[fn.Name] {
			continue // the parser or the binder turns these into control flow
		}
		if !seen[fn.Name] && !notYetImplemented[fn.Name] {
			t.Errorf("catalog function %s has no builtin and is not listed as not yet implemented", fn.Name)
		}
		if seen[fn.Name] && notYetImplemented[fn.Name] {
			t.Errorf("catalog function %s is implemented but listed as not yet implemented", fn.Name)
		}
	}
}

// SECRET is the read of a slot, never a constant (ADR-0045): the program
// of a request names the secrets it reads, whoever runs it fills them in,
// and nothing of a secret is in the bytecode.
func TestBind_SecretIsASlot(t *testing.T) {
	scope, err := NewScope(Variable{Name: "channel", Type: Scalar(KindText)})
	if err != nil {
		t.Fatal(err)
	}
	opts := BindOptions{Mode: ModePure, Secrets: []string{"token", "key"}}
	prog, err := Bind(`"Bearer " & SECRET("TOKEN") & "/" & channel & SECRET("token")`, scope, opts)
	if err != nil {
		t.Fatal(err)
	}
	// One slot for the secret however often and however cased it is read,
	// named as the connection declares it.
	if len(prog.Slots) != 2 || prog.Slots[0].Name != SecretSlot+"token" || prog.Slots[0].Type.Kind != KindText || prog.Slots[1].Name != "channel" {
		t.Fatalf("slots %+v", prog.Slots)
	}
	if len(prog.Classes) != 1 || prog.Classes[0] != ClassSecret {
		t.Errorf("classes %v", prog.Classes)
	}
	back, err := Decode(prog.Encode())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Program{prog, back} {
		got, err := NewVM().Run(p, []Value{Text("s3cr3t"), Text("#ops")}, &Context{})
		if err != nil || got.Str() != "Bearer s3cr3t/#opss3cr3t" {
			t.Errorf("the request formula: %v, %v", got, err)
		}
	}
	if strings.Contains(string(prog.Encode()), "s3cr3t") {
		t.Error("a secret is in the bytecode")
	}
	// A formula that reads no secret says so.
	plain, _ := Bind(`"x" & channel`, scope, opts)
	for _, c := range plain.Classes {
		if c == ClassSecret {
			t.Error("a formula without SECRET has the secret class")
		}
	}
	// Outside a request, in every mode, SECRET is refused; and inside one
	// the functions that are not pure still are.
	for _, mode := range []Mode{ModeAny, ModeAuto, ModePure} {
		if _, err := Bind(`SECRET("token")`, scope, BindOptions{Mode: mode}); err == nil || !strings.Contains(err.Error(), "only in the request of an action definition") {
			t.Errorf("mode %s: %v", mode, err)
		}
	}
	if _, err := Bind(`SECRET("token") & USER()`, scope, opts); err == nil {
		t.Error("a request formula took a context function")
	}
	// An empty list allows the form and knows no name.
	if _, err := Bind(`SECRET("token")`, scope, BindOptions{Mode: ModePure, Secrets: []string{}}); err == nil || !strings.Contains(err.Error(), "names no connection") {
		t.Errorf("a definition without a connection: %v", err)
	}
}
