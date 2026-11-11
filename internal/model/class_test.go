package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// invoice is a class using most of the format: scalars of every kind,
// enum values, ranges, objects of the vocabulary, an inline rows schema,
// an array, computed variables with and without declared types, a
// triggered variable, a title, states, transitions, rules, access rules
// and rights. LOOKUP, QUERY and formulas over composites are not bound
// yet and so are not in it.
const invoice = `{
  "name": "Invoice", "label": "Invoice", "plural": "Invoices", "icon": "receipt",
  "title": "\"INV-\" & number",
  "variables": {
    "number":   {"type": "text", "required": true, "index": true},
    "customer": {"type": "ref", "class": "Customer", "required": true},
    "issued":   {"type": "date", "default": "TODAY()"},
    "due":      {"type": "date", "default": "ADDDAYS(issued, 30)"},
    "owner":    {"type": "text", "default": "USER()"},
    "currency": {"type": "text", "default": "\"PLN\"", "values": ["PLN", "EUR"]},
    "discount": {"type": "decimal", "scale": 2, "default": "0", "min": 0, "max": 1},
    "net":      {"type": "money", "scale": 2},
    "shipping": {"type": "object", "schema": "Address"},
    "contact":  {"type": "object", "schema": "Person"},
    "scan":     {"type": "object", "schema": "File"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money"}},
    "tags":     {"type": "array<text>"},
    "rating":   {"type": "int", "min": 1, "max": 5},
    "payload":  {"type": "json"},
    "paid":     {"type": "bool", "default": "FALSE"},
    "total":    {"formula": "ROUND(net * (1 - discount), 2)"},
    "vat":      {"type": "money(2)", "formula": "ROUND(total * 0.23, 2)"},
    "gross":    {"formula": "total + vat"},
    "ship_city": {"formula": "shipping.city"},
    "overdue":  {"formula": "TODAY() > due AND NOT paid", "mode": "triggered", "on": ["transition:submit", "enter:approved", "refresh"]}
  },
  "states": {"initial": "draft", "list": ["draft", "submitted", "approved", "paid", "cancelled"], "final": ["paid", "cancelled"]},
  "transitions": [
    {"name": "submit",  "from": "draft",     "to": "submitted", "guard": "gross > 0"},
    {"name": "approve", "from": "submitted", "to": "approved",  "guard": "INGROUP(\"finance\") AND NOT ISBLANK(scan.hash)"},
    {"name": "pay",     "from": "approved",  "to": "paid"},
    {"name": "cancel",  "from": ["draft", "submitted"], "to": "cancelled"},
    {"from": "submitted", "to": "approved", "guard": "gross = 0"}
  ],
  "rules": [
    {"on": "enter", "state": "approved", "if": "gross > 10000", "do": [{"action": "http.call", "args": {"url": "\"https://hooks.example/approved\""}}]},
    {"on": "transition", "name": "cancel", "do": [{"action": "reset", "variables": ["discount"]}]},
    {"on": "values_change", "variables": ["discount"], "do": [{"action": "emit", "event": "discount_changed"}]}
  ],
  "access": {
    "read": "INGROUP(\"finance\") OR owner = USER()",
    "create": "INGROUP(\"sales\")",
    "update": "owner = USER()",
    "transition": {"approve": "INGROUP(\"finance\") AND owner <> USER()"}
  },
  "rights": {
    "*": {"net": {"read": ["finance"], "write": "INGROUP(\"finance\")"}},
    "approved": {"discount": {"write": "FALSE"}}
  }
}`

func mustDefine(t *testing.T, src string) *Class {
	t.Helper()
	cls, diags := Define([]byte(src), nil)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics:\n%s", diags.Error())
	}
	return cls
}

func TestDefine_Invoice(t *testing.T) {
	cls := mustDefine(t, invoice)
	if cls.Name != "Invoice" || cls.Version != 1 || cls.Title == nil {
		t.Errorf("header: name %q version %d title %v", cls.Name, cls.Version, cls.Title != nil)
	}
	if got := len(cls.Variables); got != 21 {
		t.Errorf("%d variables, want 21", got)
	}
	// Inferred types.
	for name, want := range map[string]string{"total": "money(2)", "gross": "money(2)", "ship_city": "text", "overdue": "bool", "vat": "money(2)", "discount": "decimal(2)", "lines": "rows<sku text, qty int, price money(2)>"} {
		v, ok := cls.Variable(name)
		if !ok {
			t.Fatalf("no variable %s", name)
		}
		if v.Type.String() != want {
			t.Errorf("%s: type %s, want %s", name, v.Type, want)
		}
	}
	// Evaluation order: total before vat before gross; the title anywhere
	// after number, which is an input.
	order := strings.Join(cls.Order, " ")
	if !strings.Contains(order, "total vat gross") || !strings.Contains(order, "title") || strings.Contains(order, "overdue") {
		t.Errorf("order = %v, want total vat gross ... title and no triggered variable", cls.Order)
	}
	if got := strings.Join(cls.DefaultOrder, " "); got != "issued due owner currency discount paid" {
		t.Errorf("default order = %q, want issued before due", got)
	}
	// Triggers.
	if v, _ := cls.Variable("overdue"); v.Mode != ModeTriggered || len(v.Triggers) != 3 || v.Triggers[1] != (Trigger{"enter", "approved"}) {
		t.Errorf("overdue: mode %s triggers %+v", v.Mode, v.Triggers)
	}
	// Ranges and enums.
	if v, _ := cls.Variable("rating"); !v.HasMin || !v.HasMax || v.Min.Mant() != 1 || v.Max.Mant() != 5 {
		t.Errorf("rating bounds %v %v", v.Min, v.Max)
	}
	if v, _ := cls.Variable("discount"); !v.HasMax || v.Max.Mant() != 100 || v.Max.Scale() != 2 {
		t.Errorf("discount max = %v, want 1.00 at scale 2", v.Max)
	}
	if v, _ := cls.Variable("currency"); len(v.Values) != 2 {
		t.Errorf("currency values %v", v.Values)
	}
	// Dependencies name variables, an object by its name.
	if v, _ := cls.Variable("ship_city"); strings.Join(v.Deps, ",") != "shipping" {
		t.Errorf("ship_city deps %v, want shipping", v.Deps)
	}
	// Storage layout: objects flatten, money doubles, composites leave.
	cols := map[string]Column{}
	for _, col := range cls.Columns {
		cols[col.Name] = col
	}
	for _, want := range []string{"number", "net", "net_currency", "shipping__city", "shipping__country", "contact__email", "scan__hash", "scan__size", "total", "total_currency", "ship_city", "overdue", "overdue__asof", "overdue__error", "rating", "payload"} {
		if _, ok := cols[want]; !ok {
			t.Errorf("no column %s", want)
		}
	}
	// A triggered variable has its moment beside it (ADR-0043); an auto
	// one has none.
	if asof, errc := cols["overdue__asof"], cols["overdue__error"]; asof.Moment != MomentAsOf || asof.Type.String() != "int" || asof.Variable != "overdue" ||
		errc.Moment != MomentError || errc.Type.Kind != formula.KindText {
		t.Errorf("the moment columns of overdue: %+v %+v", asof, errc)
	}
	if _, ok := cols["total__asof"]; ok {
		t.Error("an auto variable has a moment column")
	}
	for _, absent := range []string{"lines", "tags", "shipping"} {
		if _, ok := cols[absent]; ok {
			t.Errorf("column %s should not be in the document table", absent)
		}
	}
	if cols["number"].Index != true || cols["scan__size"].Type.String() != "int" || cols["shipping__city"].Path != "shipping.city" {
		t.Errorf("column details: %+v %+v %+v", cols["number"], cols["scan__size"], cols["shipping__city"])
	}
	if len(cls.Children) != 2 {
		t.Fatalf("children = %+v, want lines and tags", cls.Children)
	}
	lines := cls.Children[0]
	if lines.Variable != "lines" || len(lines.Columns) != 4 || lines.Columns[3].Name != "price_currency" {
		t.Errorf("lines child table = %+v", lines)
	}
	if tags := cls.Children[1]; tags.Variable != "tags" || len(tags.Columns) != 1 || tags.Columns[0].Name != "value" {
		t.Errorf("tags child table = %+v", tags)
	}
	// Schemas used, in vocabulary order.
	var used []string
	for _, s := range cls.Schemas {
		used = append(used, s.Name)
	}
	if strings.Join(used, " ") != "Person Address File" {
		t.Errorf("schemas used = %v", used)
	}
	// Statechart, rules, access, rights.
	if cls.States.Initial != "draft" || len(cls.Transitions) != 5 || cls.Transitions[3].From[1] != "submitted" || cls.Transitions[4].Name != "" || cls.Transitions[4].Guard == nil {
		t.Errorf("transitions %+v", cls.Transitions)
	}
	if len(cls.Rules) != 3 || cls.Rules[0].If == nil || len(cls.Rules[1].Actions) != 1 {
		t.Errorf("rules %+v", cls.Rules)
	}
	if cls.Access == nil || cls.Access.Read == nil || cls.Access.Transition["approve"] == nil {
		t.Errorf("access %+v", cls.Access)
	}
	if r := cls.Rights["*"]["net"]; r == nil || r.Read == nil || len(r.Read.Groups) != 1 || r.Write == nil || r.Write.Formula == nil {
		t.Errorf("rights %+v", cls.Rights)
	}
}

// expectDiag defines a class built from a variables block and the rest of
// the invoice, and expects one finding with the code at the path.
func expectDiag(t *testing.T, name, src, code, path string) Diagnostics {
	t.Helper()
	_, diags := Define([]byte(src), nil)
	if len(diags) == 0 {
		t.Errorf("%s: compiled without findings, want %s at %s", name, code, path)
		return nil
	}
	for _, d := range diags {
		if d.Code == code && d.Path == path {
			return diags
		}
	}
	t.Errorf("%s: want %s at %s, got:\n%s", name, code, path, diags.Error())
	return diags
}

func variables(block string) string {
	return `{"name": "T", "variables": {` + block + `}}`
}

func TestDefine_Diagnostics(t *testing.T) {
	cases := []struct{ name, src, code, path string }{
		{"not json", `{"name": `, CodeBadJSON, ""},
		{"unknown property", `{"name": "T", "variabels": {}}`, CodeBadJSON, ""},
		{"missing type", variables(`"a": {"required": true}`), CodeMissing, "/variables/a/type"},
		{"unknown type", variables(`"a": {"type": "integer"}`), CodeUnknownType, "/variables/a/type"},
		{"reserved name", variables(`"version": {"type": "int"}`), CodeReservedName, "/variables/version"},
		{"keyword name", variables(`"if": {"type": "int"}`), CodeReservedName, "/variables/if"},
		{"path separator", variables(`"a__b": {"type": "int"}`), CodeBadName, "/variables/a__b"},
		{"currency suffix", variables(`"x_currency": {"type": "text"}`), CodeBadName, "/variables/x_currency"},
		{"duplicate in case", variables(`"a": {"type": "int"}, "A": {"type": "text"}`), CodeDuplicate, "/variables/A"},
		{"scale on int", variables(`"a": {"type": "int", "scale": 2}`), CodeNotAllowed, "/variables/a/scale"},
		{"scale too big", variables(`"a": {"type": "decimal", "scale": 19}`), CodeBadValue, "/variables/a/scale"},
		{"ref without class", variables(`"a": {"type": "ref"}`), CodeMissing, "/variables/a/class"},
		{"class on text", variables(`"a": {"type": "text", "class": "X"}`), CodeNotAllowed, "/variables/a/class"},
		{"unknown schema", variables(`"a": {"type": "object", "schema": "Nobody"}`), CodeUnknownSchema, "/variables/a/schema"},
		{"object without schema", variables(`"a": {"type": "object"}`), CodeMissing, "/variables/a/schema"},
		{"values on number", variables(`"a": {"type": "number", "values": ["x"]}`), CodeNotAllowed, "/variables/a/values"},
		{"duplicate value", variables(`"a": {"type": "text", "values": ["x", "x"]}`), CodeDuplicate, "/variables/a/values/1"},
		{"range on text", variables(`"a": {"type": "text", "min": 1}`), CodeNotAllowed, "/variables/a/min"},
		{"range literal of wrong type", variables(`"a": {"type": "int", "min": "low"}`), CodeBadValue, "/variables/a/min"},
		{"max below min", variables(`"a": {"type": "number", "min": 5, "max": 1}`), CodeBadValue, "/variables/a/max"},
		{"default on computed", variables(`"a": {"type": "int"}, "b": {"formula": "a", "default": "1"}`), CodeNotAllowed, "/variables/b/default"},
		{"mode on input", variables(`"a": {"type": "int", "mode": "auto"}`), CodeNotAllowed, "/variables/a/mode"},
		{"bad mode", variables(`"a": {"type": "int"}, "b": {"formula": "a", "mode": "sometimes"}`), CodeBadValue, "/variables/b/mode"},
		{"triggered without triggers", variables(`"a": {"type": "int"}, "b": {"formula": "a", "mode": "triggered"}`), CodeMissing, "/variables/b/on"},
		{"triggers on auto", variables(`"a": {"type": "int"}, "b": {"formula": "a", "on": ["refresh"]}`), CodeNotAllowed, "/variables/b/on"},
		{"bad trigger", variables(`"a": {"type": "int"}, "b": {"formula": "a", "mode": "triggered", "on": ["sometimes"]}`), CodeBadTrigger, "/variables/b/on/0"},
		{"trigger on unknown state", `{"name": "T", "variables": {"a": {"type": "int"}, "b": {"formula": "a", "mode": "triggered", "on": ["enter:gone"]}}, "states": {"initial": "s", "list": ["s"]}}`, CodeUnknownState, "/variables/b/on/0"},
		{"formula does not bind", variables(`"a": {"type": "int"}, "b": {"formula": "a +"}`), CodeBadFormula, "/variables/b/formula"},
		{"formula of wrong type", variables(`"a": {"type": "int"}, "b": {"type": "text", "formula": "a * 2"}`), CodeTypeMismatch, "/variables/b/formula"},
		{"formula scale too large", variables(`"a": {"type": "decimal(2)"}, "b": {"type": "decimal(2)", "formula": "a * a"}`), CodeTypeMismatch, "/variables/b/formula"},
		{"context function in auto", variables(`"a": {"type": "date"}, "b": {"formula": "DAYS(a, TODAY())"}`), CodeBadFormula, "/variables/b/formula"},
		{"blank formula without type", variables(`"b": {"formula": "BLANK"}`), CodeMissing, "/variables/b/type"},
		{"default reads computed", variables(`"a": {"type": "number", "default": "b"}, "b": {"formula": "1"}`), CodeNotAllowed, "/variables/a/default"},
		{"cycle", variables(`"a": {"type": "int", "formula": "b + 1"}, "b": {"type": "int", "formula": "a + 1"}`), CodeCycle, "/variables/a/formula"},
		{"inferred cycle", variables(`"a": {"formula": "b + 1"}, "b": {"formula": "a + 1"}`), CodeCycle, "/variables/a/formula"},
		{"default cycle", variables(`"a": {"type": "int", "default": "b"}, "b": {"type": "int", "default": "a"}`), CodeCycle, "/variables/a/default"},
		{"title not text", `{"name": "T", "title": "a", "variables": {"a": {"type": "int"}}}`, CodeTypeMismatch, "/title"},
		{"title with context", `{"name": "T", "title": "USER()", "variables": {"a": {"type": "int"}}}`, CodeBadFormula, "/title"},
		{"computed object", variables(`"a": {"type": "object", "schema": "Person"}, "b": {"type": "object", "schema": "Person", "formula": "a"}`), CodeNotSupported, "/variables/b/formula"},
		{"json formula", variables(`"a": {"type": "json", "formula": "\"{}\""}`), CodeNotAllowed, "/variables/a/formula"},
		{"inline column reserved", variables(`"a": {"type": "rows", "schema": {"idx": "int"}}`), CodeReservedName, "/variables/a/schema/idx"},
		{"inline column json", variables(`"a": {"type": "rows", "schema": {"p": "json"}}`), CodeNotAllowed, "/variables/a/schema/p"},
		{"inline column collection", variables(`"a": {"type": "rows", "schema": {"p": "array<text>"}}`), CodeNotAllowed, "/variables/a/schema/p"},
		{"class name reserved", `{"name": "events", "variables": {"a": {"type": "int"}}}`, CodeReservedName, "/name"},
		{"class name prefix", `{"name": "doc_x", "variables": {"a": {"type": "int"}}}`, CodeReservedName, "/name"},
		{"states without initial", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"list": ["s"]}}`, CodeMissing, "/states/initial"},
		{"initial unknown", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "x", "list": ["s"]}}`, CodeUnknownState, "/states/initial"},
		{"transitions without states", `{"name": "T", "variables": {"a": {"type": "int"}}, "transitions": [{"name": "t", "from": "a", "to": "b"}]}`, CodeMissing, "/states"},
		{"transition to unknown", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "s", "list": ["s"]}, "transitions": [{"name": "t", "from": "s", "to": "gone"}]}`, CodeUnknownState, "/transitions/0/to"},
		{"eventless without guard", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "s", "list": ["s", "u"]}, "transitions": [{"from": "s", "to": "u"}]}`, CodeMissing, "/transitions/0/guard"},
		{"guard not a condition", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "s", "list": ["s", "u"]}, "transitions": [{"name": "t", "from": "s", "to": "u", "guard": "a"}]}`, CodeNotACondition, "/transitions/0/guard"},
		{"rule on unknown trigger", `{"name": "T", "variables": {"a": {"type": "int"}}, "rules": [{"on": "sometimes", "do": [{"action": "emit"}]}]}`, CodeBadValue, "/rules/0/on"},
		{"rule without actions", `{"name": "T", "variables": {"a": {"type": "int"}}, "rules": [{"on": "create", "do": []}]}`, CodeMissing, "/rules/0/do"},
		{"rule action unnamed", `{"name": "T", "variables": {"a": {"type": "int"}}, "rules": [{"on": "create", "do": [{"url": "x"}]}]}`, CodeMissing, "/rules/0/do/0/action"},
		{"rule on unknown variable", `{"name": "T", "variables": {"a": {"type": "int"}}, "rules": [{"on": "values_change", "variables": ["z"], "do": [{"action": "emit"}]}]}`, CodeUnknownVariable, "/rules/0/variables/0"},
		{"read rule with lookup class", `{"name": "T", "variables": {"a": {"type": "int"}}, "access": {"read": "LOOKUP(Rates, rate, code = \"EUR\") > 0"}}`, CodeBadFormula, "/access/read"},
		{"access for unknown transition", `{"name": "T", "variables": {"a": {"type": "int"}}, "access": {"transition": {"t": "TRUE"}}}`, CodeUnknownTransition, "/access/transition/t"},
		{"rights for unknown state", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "s", "list": ["s"]}, "rights": {"x": {"a": {"read": "TRUE"}}}}`, CodeUnknownState, "/rights/x"},
		{"rights for unknown variable", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "s", "list": ["s"]}, "rights": {"s": {"z": {"read": "TRUE"}}}}`, CodeUnknownVariable, "/rights/s/z"},
		{"right of wrong shape", `{"name": "T", "variables": {"a": {"type": "int"}}, "rights": {"*": {"a": {"read": 1}}}}`, CodeBadValue, "/rights/*/a/read"},
	}
	for _, tc := range cases {
		expectDiag(t, tc.name, tc.src, tc.code, tc.path)
	}
}

func TestDefine_FormulaPositionAndSeveralFindings(t *testing.T) {
	_, diags := Define([]byte(variables(`"a": {"type": "int"}, "b": {"formula": "a + \"x\""}, "c": {"type": "text", "formula": "a"}, "d": {"type": "bool", "formula": "a"}`)), nil)
	if len(diags) != 3 {
		t.Fatalf("want every finding, got %d:\n%s", len(diags), diags.Error())
	}
	if diags[0].Code != CodeBadFormula || diags[0].Pos != 2 {
		t.Errorf("first finding %+v, want bad_formula at the + (offset 2)", diags[0])
	}
	if diags[1].Code != CodeTypeMismatch || !strings.Contains(diags[1].Hint, "TEXT(") {
		t.Errorf("second finding %+v, want a type mismatch with a TEXT hint", diags[1])
	}
	if diags[2].Code != CodeTypeMismatch || diags[2].Path != "/variables/d/formula" {
		t.Errorf("third finding %+v, want the mismatch of d", diags[2])
	}
}

func TestDefine_PreservesDeclarationOrder(t *testing.T) {
	cls := mustDefine(t, variables(`"z": {"type": "int"}, "m": {"type": "text"}, "a": {"type": "bool"}`))
	if cls.Variables[0].Name != "z" || cls.Variables[1].Name != "m" || cls.Variables[2].Name != "a" {
		t.Errorf("variables in %v, want written order z m a", []string{cls.Variables[0].Name, cls.Variables[1].Name, cls.Variables[2].Name})
	}
	if cls.Columns[0].Name != "z" || cls.Columns[2].Name != "a" {
		t.Errorf("columns in %+v, want written order", cls.Columns)
	}
}

func TestDefine_ExactDefaultFromALiteral(t *testing.T) {
	cls := mustDefine(t, variables(`"a": {"type": "decimal(2)", "default": "0"}, "b": {"type": "int", "default": "42"}, "c": {"type": "decimal(2)", "default": "0.1"}, "d": {"type": "decimal(2)", "formula": "0.25"}`))
	for name, want := range map[string]string{"a": "decimal(2)", "b": "int", "c": "decimal(2)", "d": "decimal(2)"} {
		v, _ := cls.Variable(name)
		prog := v.Default
		if prog == nil {
			prog = v.Formula
		}
		if prog.Result.String() != want || len(prog.Decs) != 1 {
			t.Errorf("%s: a literal for an exact variable binds as %s with %d exact constants, want %s and one", name, prog.Result, len(prog.Decs), want)
		}
	}
	_, diags := Define([]byte(variables(`"a": {"type": "decimal(2)", "default": "0.125"}`)), nil)
	if !diags.Has(CodeTypeMismatch) {
		t.Errorf("a literal with more places than the scale is refused, got %v", diags)
	}
	_, diags = Define([]byte(variables(`"m": {"type": "money(2)", "default": "0"}`)), nil)
	if !diags.Has(CodeTypeMismatch) || !strings.Contains(diags.Error(), "MONEY(") {
		t.Errorf("a bare number for money needs a currency, got %v", diags)
	}
}

func TestDefine_TitleIsIDWithoutAFormula(t *testing.T) {
	cls := mustDefine(t, variables(`"a": {"type": "int"}`))
	if cls.Title != nil || len(cls.Order) != 0 || cls.States != nil {
		t.Errorf("a minimal class: title %v order %v states %v", cls.Title, cls.Order, cls.States)
	}
}

func TestTypeFits(t *testing.T) {
	num, text := formula.Scalar(formula.KindNumber), formula.Scalar(formula.KindText)
	for _, tc := range []struct {
		want, got formula.Type
		fits      bool
	}{
		{num, num, true}, {num, formula.Scalar(formula.KindNull), true},
		{formula.DecimalType(2), formula.DecimalType(2), true}, {formula.DecimalType(4), formula.DecimalType(2), true},
		{formula.DecimalType(2), formula.DecimalType(4), false}, {formula.MoneyType(2), formula.DecimalType(2), false},
		{num, formula.DecimalType(2), false}, {text, num, false}, {formula.Scalar(formula.KindDate), formula.Scalar(formula.KindDateTime), false},
	} {
		msg, _ := typeFits(tc.want, tc.got)
		if (msg == "") != tc.fits {
			t.Errorf("typeFits(%s, %s) = %q, want fits=%v", tc.want, tc.got, msg, tc.fits)
		}
	}
}

// The rights table resolves by the named state's cell for the variable,
// then that state's "*", then the "*" state's cell, then its "*"; the
// first cell declaring the rule decides (ADR-0028, ADR-0041). Keys are
// the variables' canonical names, and a rule keeps its source.
func TestRights_Resolution(t *testing.T) {
	cls := mustDefine(t, `{"name": "T", "variables": {"a": {"type": "int"}, "b": {"type": "int"}, "c": {"type": "int"}},
	  "states": {"initial": "s", "list": ["s", "u"]},
	  "rights": {
	    "*": {"*": {"read": ["everyone"]}, "A": {"write": "a > 0"}},
	    "s": {"*": {"write": ["clerks"]}, "b": {"read": "FALSE"}}
	  }}`)
	if r := cls.Rights["*"]["a"]; r == nil || r.Write == nil || r.Write.Src != "a > 0" {
		t.Fatalf("the cell is keyed by the canonical name and keeps its source: %+v", cls.Rights["*"])
	}
	cases := []struct {
		state, variable, kind, want string
	}{
		{"s", "b", "read", "FALSE"},    // the named cell
		{"s", "a", "read", "everyone"}, // the named state has no read for a: the * state's *
		{"s", "a", "write", "clerks"},  // the named state's * comes before the * state's cell of the variable
		{"s", "c", "write", "clerks"},  // the state's *
		{"u", "c", "write", ""},        // nothing declares it
		{"u", "a", "write", "a > 0"},   // the * state's cell of the variable
		{"u", "b", "read", "everyone"}, // the * state's *
		{"", "a", "write", "a > 0"},    // no state: the * row alone
		{"", "b", "read", "everyone"},
	}
	for _, tc := range cases {
		var rule *RightRule
		if tc.kind == "read" {
			rule = cls.ReadRule(tc.state, tc.variable)
		} else {
			rule = cls.WriteRule(tc.state, tc.variable)
		}
		got := ""
		if rule != nil {
			got = rule.Src
		}
		if got != tc.want {
			t.Errorf("%s right of %s in %q = %q, want %q", tc.kind, tc.variable, tc.state, got, tc.want)
		}
	}
	// A read right is compiled into every filter and may not look up.
	src := &formula.Source{Name: "Fx", Columns: []formula.Field{{Name: "code", Type: formula.Scalar(formula.KindText)}, {Name: "rate", Type: formula.Scalar(formula.KindNumber)}}, Key: []string{"code"}}
	env := Env{Sources: formula.NewSourceSet(src)}
	_, diags := DefineIn([]byte(`{"name": "T", "variables": {"a": {"type": "text"}}, "states": {"initial": "s", "list": ["s"]},
	  "rights": {"*": {"a": {"read": "LOOKUP(Fx, rate, code = THIS.a) > 1"}}}}`), env)
	if len(diags) != 1 || diags[0].Code != CodeNotAllowed || diags[0].Path != "/rights/*/a/read" {
		t.Errorf("read right with a lookup: %v", diags)
	}
	if _, diags := DefineIn([]byte(`{"name": "T", "variables": {"a": {"type": "text"}}, "states": {"initial": "s", "list": ["s"]},
	  "rights": {"*": {"a": {"write": "LOOKUP(Fx, rate, code = THIS.a) > 1"}}}}`), env); len(diags) != 0 {
		t.Errorf("write right with a lookup: %v", diags)
	}
	if _, diags := Define([]byte(`{"name": "T", "variables": {"a": {"type": "int"}}, "rights": {"*": {"a": {"read": "TRUE"}, "A": {"write": "TRUE"}}}}`), nil); len(diags) != 1 || diags[0].Code != CodeDuplicate {
		t.Errorf("a variable listed twice in different case: %v", diags)
	}
}

// TestShallow is the compilation of a stored class before the classes it
// reads are compiled, and of an earlier version of a class (ADR-0046): a
// formula that reads a class and does not bind is left unbound, with the
// declared type of its variable; every other finding is still one.
func TestShallow(t *testing.T) {
	const reader = `{"name": "Kit", "variables": {
  "top":   {"type": "decimal(2)", "formula": "LOOKUP(Part, MAX(z), TRUE)", "mode": "triggered", "on": ["refresh"]},
  "twice": {"formula": "top + top"}},
 "states": {"initial": "open", "list": ["open", "done"]},
 "transitions": [{"name": "finish", "from": "open", "to": "done", "guard": "LOOKUP(Part, COUNT(id), TRUE) > 0"}]}`
	if _, diags := DefineIn([]byte(reader), Env{}); !diags.Has(CodeBadFormula) {
		t.Errorf("a class nobody defined is an unknown source: %v", diags)
	}
	cls, diags := DefineIn([]byte(reader), Env{Shallow: true})
	if len(diags) > 0 {
		t.Fatalf("shallow: %v", diags)
	}
	top, _ := cls.Variable("top")
	twice, _ := cls.Variable("twice")
	if top.Formula != nil || top.Type.String() != "decimal(2)" || twice.Formula == nil || twice.Type.String() != "decimal(2)" || cls.Transitions[0].Guard != nil {
		t.Errorf("top %v %s, twice %v %s, guard %v", top.Formula, top.Type, twice.Formula, twice.Type, cls.Transitions[0].Guard)
	}
	src := cls.Source()
	if !src.Class || len(src.Columns) != 9 || src.Columns[7].Name != "top" || src.Key[0] != "id" {
		t.Errorf("the source of a class compiled alone: %+v", src)
	}
	// What does not read a class is judged as ever.
	for _, bad := range []string{
		strings.Replace(reader, `"top + top"`, `"top + nope"`, 1),
		strings.Replace(reader, `"top + top"`, `"top +"`, 1),
		strings.Replace(reader, `"LOOKUP(Part, COUNT(id), TRUE) > 0"`, `"missing > 0"`, 1),
	} {
		if _, diags := DefineIn([]byte(bad), Env{Shallow: true}); !diags.Has(CodeBadFormula) {
			t.Errorf("a shallow compilation passed %s", bad)
		}
	}
}
