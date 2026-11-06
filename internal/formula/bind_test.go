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
	scope := mustScope(t)
	p := mustBind(t, `1 + 1 + 2 & "a" & "a" & "b"`, scope)
	if len(p.Nums) != 2 || len(p.Texts) != 2 {
		t.Errorf("pools = %v %q, want [1 2] [a b]", p.Nums, p.Texts)
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
		{"ROUND + 1", "ROUND is a function: write ROUND(number[, digits])", 1},
		{"qty + name", "+ needs numbers, got text; VALUE converts text to a number", 5},
		{"due + 1", "+ needs numbers, got date; dates move with ADDDAYS", 5},
		{"TRUE = 1", "= compares bool with number", 6},
		{"IF(qty, 1, 2)", "IF needs a condition, got number; compare it, for example <> 0", 4},
		{"IF(paid, 1, \"x\")", "IF branches must have the same type, got number and text", 1},
		{"NOT qty", "NOT needs a condition, got number", 1},
		{"qty AND paid", "AND needs conditions, got number", 5},
		{"UPPER(qty)", "UPPER: argument 1 must be text, got number; TEXT converts a number to text", 7},
		{"YEAR(name)", "YEAR: argument 1 must be a date, got text; DATEVALUE converts text to a date", 6},
		{"THIS.qty", "THIS.qty is only valid inside LOOKUP and QUERY", 1},
		{`REGEXREPLACE(name, "(", "")`, `REGEXREPLACE: invalid pattern "("`, 1},
		{"LOOKUP(t, c, x = 1)", "LOOKUP is not implemented yet", 1},
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
		if fn.Name == "IF" || fn.Name == "AND" || fn.Name == "OR" || fn.Name == "NOT" {
			continue // the parser turns these into nodes
		}
		if !seen[fn.Name] && !notYetImplemented[fn.Name] {
			t.Errorf("catalog function %s has no builtin and is not listed as not yet implemented", fn.Name)
		}
		if seen[fn.Name] && notYetImplemented[fn.Name] {
			t.Errorf("catalog function %s is implemented but listed as not yet implemented", fn.Name)
		}
	}
}
