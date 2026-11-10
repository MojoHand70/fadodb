package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// flow is a class with states for the rule tests. open and held are
// live; done, filed and parked are terminal. hold and close leave live
// states, file leaves a terminal one, and park leaves one of each.
func flow(rules string) string {
	return `{"name": "T",
 "variables": {"a": {"type": "int", "default": "1"}, "note": {"type": "text"}, "kind": {"type": "text", "values": ["x", "y"]},
               "sum": {"formula": "a + 1"}, "lines": {"type": "rows", "schema": {"sku": "text"}}, "tags": {"type": "array<text>"},
               "who": {"type": "object", "schema": "Person"},
               "lazy": {"formula": "a * 2", "mode": "triggered", "on": ["enter:held"]},
               "snap": {"formula": "a * 3", "mode": "triggered", "on": ["refresh", "transition:hold"]},
               "stamp": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "open", "list": ["open", "held", "done", "filed", "parked"], "final": ["done", "filed", "parked"]},
 "transitions": [
   {"name": "hold", "from": "open", "to": "held"},
   {"name": "close", "from": ["open", "held"], "to": "done"},
   {"name": "file", "from": "done", "to": "filed"},
   {"name": "park", "from": ["held", "done"], "to": "parked"}
 ],
 "rules": [` + rules + `]}`
}

func TestRules_Compile(t *testing.T) {
	cls, diags := Define([]byte(flow(`
	  {"on": "transition", "name": "hold", "if": "a > 0",
	   "do": [{"action": "set", "values": {"note": "\"held\"", "a": "a + 1"}},
	          {"action": "reset", "variables": ["note", "lines", "who"]},
	          {"action": "emit", "event": "ticket.held"},
	          {"action": "http.call", "url": "note"},
	          {"action": "refresh", "variables": ["stamp", "snap"]},
	          {"action": "refresh"},
	          {"action": "refresh", "id": "note", "variables": ["STAMP"]}]},
	  {"on": "values_change", "variables": ["a", "sum"], "do": [{"action": "emit", "event": "changed"}]}`)), nil)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if len(cls.Rules) != 2 {
		t.Fatalf("rules %+v", cls.Rules)
	}
	r := cls.Rules[0]
	if r.Path() != "/rules/0" || r.If == nil || len(r.Actions) != 7 {
		t.Fatalf("rule %+v", r)
	}
	// A refresh recomputes in declaration order, whatever order it names
	// them in; with none named, every variable that lists refresh; and
	// one that names another document belongs to the outbox.
	names := func(a *Action) string {
		var out []string
		for _, v := range a.Variables {
			out = append(out, v.Name)
		}
		return strings.Join(out, " ")
	}
	if a := r.Actions[4]; !a.Sync || a.Name != ActionRefresh || names(a) != "snap stamp" {
		t.Errorf("refresh of two: sync %v, variables %q", a.Sync, names(a))
	}
	if a := r.Actions[5]; !a.Sync || names(a) != "snap stamp" {
		t.Errorf("refresh of all: sync %v, variables %q", a.Sync, names(a))
	}
	if a := r.Actions[6]; a.Sync || !a.Queued || a.Name != ActionRefresh || a.Target != "T" || a.ID == nil || len(a.Variables) != 0 || strings.Join(a.Names, " ") != "stamp" {
		t.Errorf("a refresh of another document is an action of the outbox: %+v", a)
	}
	set, reset, emit, post := r.Actions[0], r.Actions[1], r.Actions[2], r.Actions[3]
	if !set.Sync || len(set.Sets) != 2 || set.Sets[0].Variable.Name != "note" || set.Sets[1].Variable.Name != "a" || set.Sets[1].Formula == nil || set.Path() != "/rules/0/do/0" {
		t.Errorf("set %+v", set)
	}
	if !reset.Sync || len(reset.Variables) != 3 || reset.Variables[1].Name != "lines" {
		t.Errorf("reset %+v", reset)
	}
	if !emit.Sync || emit.Event != "ticket.held" {
		t.Errorf("emit %+v", emit)
	}
	// http.call is the engine's and is not run yet: kept as written.
	if post.Sync || post.Queued || post.Name != ActionHTTPCall || post.Path() != "/rules/0/do/3" || !strings.Contains(string(post.Raw), "url") {
		t.Errorf("http.call is kept as written: %+v", post)
	}
	if cls.Rules[1].Index != 1 || len(cls.Rules[1].Variables) != 2 {
		t.Errorf("rule %+v", cls.Rules[1])
	}
}

func TestRules_Diagnostics(t *testing.T) {
	onHold := func(action string) string {
		return flow(`{"on": "transition", "name": "hold", "do": [` + action + `]}`)
	}
	cases := []struct{ name, src, code, path string }{
		{"rules in a class without states", `{"name": "T", "variables": {"a": {"type": "int"}}, "rules": [{"on": "create", "do": [{"action": "emit", "event": "made"}]}]}`, CodeNotAllowed, "/rules"},
		{"a rule on a schedule", flow(`{"on": "schedule", "do": [{"action": "emit", "event": "tick"}]}`), CodeNotSupported, "/rules/0/on"},

		{"refresh of an unknown variable", onHold(`{"action": "refresh", "variables": ["z"]}`), CodeUnknownVariable, "/rules/0/do/0/variables/0"},
		{"refresh of an auto variable", onHold(`{"action": "refresh", "variables": ["sum"]}`), CodeNotAllowed, "/rules/0/do/0/variables/0"},
		{"refresh of an input", onHold(`{"action": "refresh", "variables": ["snap", "a"]}`), CodeNotAllowed, "/rules/0/do/0/variables/1"},
		{"refresh of a variable that does not list refresh", onHold(`{"action": "refresh", "variables": ["lazy"]}`), CodeNotAllowed, "/rules/0/do/0/variables/0"},
		{"refresh of one variable twice", onHold(`{"action": "refresh", "variables": ["snap", "SNAP"]}`), CodeDuplicate, "/rules/0/do/0/variables/1"},
		{"refresh with a misspelt property", onHold(`{"action": "refresh", "variable": ["snap"]}`), CodeBadJSON, "/rules/0/do/0"},
		{"refresh of another class without a document", onHold(`{"action": "refresh", "class": "Other"}`), CodeMissing, "/rules/0/do/0/id"},
		{"refresh in a class where nothing lists it", `{"name": "T", "variables": {"a": {"type": "int"}}, "states": {"initial": "s", "list": ["s", "u"]},
		  "transitions": [{"name": "go", "from": "s", "to": "u"}], "rules": [{"on": "transition", "name": "go", "do": [{"action": "refresh"}]}]}`, CodeNotAllowed, "/rules/0/do/0"},
		{"refresh on the exit of a terminal state", flow(`{"on": "exit", "state": "done", "do": [{"action": "refresh", "variables": ["snap"]}]}`), CodeNotAllowed, "/rules/0/do/0"},

		{"set without values", onHold(`{"action": "set"}`), CodeMissing, "/rules/0/do/0/values"},
		{"set with no values", onHold(`{"action": "set", "values": {}}`), CodeMissing, "/rules/0/do/0/values"},
		{"set with a value that is not a formula", onHold(`{"action": "set", "values": {"a": 2}}`), CodeBadValue, "/rules/0/do/0/values"},
		{"set with a misspelt property", onHold(`{"action": "set", "value": {"a": "2"}}`), CodeBadJSON, "/rules/0/do/0"},
		{"set of an unknown variable", onHold(`{"action": "set", "values": {"z": "1"}}`), CodeUnknownVariable, "/rules/0/do/0/values/z"},
		{"set of a computed variable", onHold(`{"action": "set", "values": {"sum": "1"}}`), CodeNotAllowed, "/rules/0/do/0/values/sum"},
		{"set of a rows variable", onHold(`{"action": "set", "values": {"lines": "1"}}`), CodeNotSupported, "/rules/0/do/0/values/lines"},
		{"set of an array", onHold(`{"action": "set", "values": {"tags": "1"}}`), CodeNotSupported, "/rules/0/do/0/values/tags"},
		{"set of an object", onHold(`{"action": "set", "values": {"who": "1"}}`), CodeNotSupported, "/rules/0/do/0/values/who"},
		{"set of the wrong type", onHold(`{"action": "set", "values": {"a": "\"one\""}}`), CodeTypeMismatch, "/rules/0/do/0/values/a"},
		{"set with a formula that does not bind", onHold(`{"action": "set", "values": {"a": "missing + 1"}}`), CodeBadFormula, "/rules/0/do/0/values/a"},
		{"set of one variable twice", onHold(`{"action": "set", "values": {"a": "1", "A": "2"}}`), CodeDuplicate, "/rules/0/do/0/values/A"},

		{"reset without variables", onHold(`{"action": "reset"}`), CodeMissing, "/rules/0/do/0/variables"},
		{"reset of an unknown variable", onHold(`{"action": "reset", "variables": ["z"]}`), CodeUnknownVariable, "/rules/0/do/0/variables/0"},
		{"reset of a computed variable", onHold(`{"action": "reset", "variables": ["a", "sum"]}`), CodeNotAllowed, "/rules/0/do/0/variables/1"},
		{"reset of one variable twice", onHold(`{"action": "reset", "variables": ["a", "A"]}`), CodeDuplicate, "/rules/0/do/0/variables/1"},
		{"reset with a misspelt property", onHold(`{"action": "reset", "variable": ["a"]}`), CodeBadJSON, "/rules/0/do/0"},

		{"emit without an event", onHold(`{"action": "emit"}`), CodeMissing, "/rules/0/do/0/event"},
		{"emit with the old property", onHold(`{"action": "emit", "name": "held"}`), CodeBadJSON, "/rules/0/do/0"},
		{"emit of a name with a space", onHold(`{"action": "emit", "event": "ticket held"}`), CodeBadName, "/rules/0/do/0/event"},
		{"emit of a name with an empty part", onHold(`{"action": "emit", "event": "ticket..held"}`), CodeBadName, "/rules/0/do/0/event"},
		{"emit of a name ending in a dot", onHold(`{"action": "emit", "event": "ticket."}`), CodeBadName, "/rules/0/do/0/event"},
		{"emit of a part starting with a digit", onHold(`{"action": "emit", "event": "ticket.1st"}`), CodeBadName, "/rules/0/do/0/event"},
		{"emit of a name too long", onHold(`{"action": "emit", "event": "` + strings.Repeat("e", MaxEventName+1) + `"}`), CodeBadName, "/rules/0/do/0/event"},

		// A frozen document computes nothing (ADR-0043, item 8).
		{"set on the exit of a terminal state", flow(`{"on": "exit", "state": "done", "do": [{"action": "set", "values": {"note": "\"x\""}}]}`), CodeNotAllowed, "/rules/0/do/0"},
		{"reset on a transition that leaves a terminal state", flow(`{"on": "transition", "name": "file", "do": [{"action": "reset", "variables": ["note"]}]}`), CodeNotAllowed, "/rules/0/do/0"},
		{"set on a transition one of whose sources is terminal", flow(`{"on": "transition", "name": "park", "do": [{"action": "set", "values": {"note": "\"x\""}}]}`), CodeNotAllowed, "/rules/0/do/0"},
		{"set on the entry of a state a terminal state leads to", flow(`{"on": "enter", "state": "filed", "do": [{"action": "set", "values": {"note": "\"x\""}}]}`), CodeNotAllowed, "/rules/0/do/0"},
		{"set on the entry of a state a live state leads to as well", flow(`{"on": "enter", "state": "parked", "do": [{"action": "emit", "event": "parked"}, {"action": "set", "values": {"note": "\"x\""}}]}`), CodeNotAllowed, "/rules/0/do/1"},
		{"set on delete", flow(`{"on": "delete", "do": [{"action": "set", "values": {"note": "\"x\""}}]}`), CodeNotAllowed, "/rules/0/do/0"},
	}
	for _, tc := range cases {
		expectDiag(t, tc.name, tc.src, tc.code, tc.path)
	}
}

// What a frozen document allows: a rule that sets on the way into a
// terminal state from live ones, and emit and the actions of the outbox
// on every trigger.
func TestRules_FrozenAllows(t *testing.T) {
	for name, rules := range map[string]string{
		"set on the entry of a terminal state only live states lead to": `{"on": "enter", "state": "done", "do": [{"action": "set", "values": {"note": "\"closed\""}}]}`,
		"set on the transition into a terminal state":                   `{"on": "transition", "name": "close", "do": [{"action": "reset", "variables": ["note"]}]}`,
		"emit on the exit of a terminal state":                          `{"on": "exit", "state": "done", "do": [{"action": "emit", "event": "left.done"}]}`,
		"an outbox action on a move between terminal states":            `{"on": "transition", "name": "file", "do": [{"action": "http.call", "url": "\"https://example.test\""}]}`,
		"emit on delete": `{"on": "delete", "do": [{"action": "emit", "event": "gone"}]}`,
	} {
		if _, diags := Define([]byte(flow(rules)), nil); len(diags) > 0 {
			t.Errorf("%s: %s", name, diags.Error())
		}
	}
}

func TestEventNameReason(t *testing.T) {
	for _, name := range []string{"paid", "invoice.flagged", "a.b.c", "Invoice_2.flagged_twice", "_x"} {
		if reason := eventNameReason(name); reason != "" {
			t.Errorf("%q: %s", name, reason)
		}
	}
	for _, name := range []string{"", ".", ".a", "a.", "a..b", "a b", "a-b", "1a", "a.1", "é"} {
		if eventNameReason(name) == "" {
			t.Errorf("%q passes as an event name", name)
		}
	}
}

// The triggers of a triggered variable (ADR-0043, items 8 and 9): table:
// and schedule: wait for the phase that runs them, and a trigger that can
// fire on a move out of a terminal state is refused, since a frozen
// document computes nothing.
func TestTriggers_Diagnostics(t *testing.T) {
	on := func(triggers string) string {
		return `{"name": "T", "variables": {"a": {"type": "int"}, "b": {"formula": "a", "mode": "triggered", "on": [` + triggers + `]}},
 "states": {"initial": "open", "list": ["open", "held", "done", "filed", "parked"], "final": ["done", "filed", "parked"]},
 "transitions": [
   {"name": "hold", "from": "open", "to": "held"},
   {"name": "close", "from": ["open", "held"], "to": "done"},
   {"name": "file", "from": "done", "to": "filed"},
   {"name": "park", "from": ["held", "done"], "to": "parked"}
 ]}`
	}
	for _, tc := range []struct{ name, triggers, code string }{
		{"a schedule", `"schedule:daily"`, CodeNotSupported},
		{"a table", `"table:Rates"`, CodeNotSupported},
		{"the exit of a terminal state", `"exit:done"`, CodeNotAllowed},
		{"a transition that leaves a terminal state", `"transition:file"`, CodeNotAllowed},
		{"a transition one of whose sources is terminal", `"transition:park"`, CodeNotAllowed},
		{"the entry of a state a terminal state leads to", `"enter:filed"`, CodeNotAllowed},
		{"the entry of a state a live state leads to as well", `"enter:parked"`, CodeNotAllowed},
		{"refresh with an argument", `"refresh:now"`, CodeBadTrigger},
	} {
		expectDiag(t, tc.name, on(tc.triggers), tc.code, "/variables/b/on/0")
	}
	for _, triggers := range []string{`"refresh"`, `"enter:held"`, `"exit:open"`, `"transition:hold"`, `"transition:close"`, `"enter:done"`, `"enter:open", "exit:held", "refresh"`} {
		cls, diags := Define([]byte(on(triggers)), nil)
		if len(diags) > 0 {
			t.Errorf("%s: %s", triggers, diags.Error())
			continue
		}
		v, _ := cls.Variable("b")
		if !v.Triggered() || len(v.Triggers) != strings.Count(triggers, ",")+1 {
			t.Errorf("%s: triggers %+v", triggers, v.Triggers)
		}
	}
	// A second finding on one variable does not hide the first.
	if diags := expectDiag(t, "two triggers", on(`"schedule:daily", "exit:done"`), CodeNotAllowed, "/variables/b/on/1"); len(diags) != 2 {
		t.Errorf("want two findings, got: %s", diags.Error())
	}
}

// A create is checked against the class it names when the rule's class
// is defined (ADR-0044, item 7): the class, its variables, the types of
// the formulas, and the inputs it requires.
func TestRules_Create(t *testing.T) {
	receipt, diags := Define([]byte(`{"name": "Receipt",
 "variables": {"invoice": {"type": "text", "required": true}, "amount": {"type": "money", "scale": 2}, "count": {"type": "int", "default": "1"},
               "twice": {"formula": "count * 2"}, "lines": {"type": "rows", "schema": {"sku": "text"}}},
 "states": {"initial": "issued", "list": ["issued"]}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	sheet, diags := Define([]byte(`{"name": "Sheet", "variables": {"a": {"type": "int"}}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	env := Env{Classes: func(name string) (*Class, bool) {
		switch strings.ToUpper(name) {
		case "RECEIPT":
			return receipt, true
		case "SHEET":
			return sheet, true
		}
		return nil, false
	}}
	invoice := func(action string) string {
		return `{"name": "Invoice", "variables": {"total": {"type": "money", "scale": 2}, "n": {"type": "int"}, "note": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "paid"]}, "transitions": [{"name": "pay", "from": "open", "to": "paid"}],
 "rules": [{"on": "enter", "state": "paid", "do": [` + action + `]}]}`
	}
	cls, diags := DefineIn([]byte(invoice(`{"action": "create", "class": "receipt", "id": "\"R-\" & ID()", "values": {"invoice": "ID()", "amount": "total", "count": "n + 1"}}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	a := cls.Rules[0].Actions[0]
	if a.Sync || !a.Queued || a.Name != ActionCreate || a.Target != "Receipt" || a.ID == nil || len(a.Sets) != 3 ||
		a.Sets[1].Name != "amount" || a.Sets[1].Variable != receipt.Variables[1] || a.Sets[2].Formula.Result.String() != "int" {
		t.Errorf("the compiled create: %+v", a)
	}
	// A class may create documents of itself.
	if _, diags := DefineIn([]byte(`{"name": "Echo", "variables": {"n": {"type": "int"}}, "states": {"initial": "s", "list": ["s"]},
 "rules": [{"on": "create", "do": [{"action": "create", "class": "Echo", "values": {"n": "n + 1"}}]}]}`), env); len(diags) > 0 {
		t.Errorf("a class that creates itself: %s", diags.Error())
	}
	for _, tc := range []struct{ name, action, code, path string }{
		{"no class", `{"action": "create", "values": {"invoice": "ID()"}}`, CodeMissing, "/rules/0/do/0/class"},
		{"an unknown class", `{"action": "create", "class": "Voucher", "values": {}}`, CodeUnknownClass, "/rules/0/do/0/class"},
		{"a class that stores no documents", `{"action": "create", "class": "Sheet", "values": {"a": "1"}}`, CodeNotAllowed, "/rules/0/do/0/class"},
		{"an unknown variable", `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "price": "total"}}`, CodeUnknownVariable, "/rules/0/do/0/values/price"},
		{"a computed variable", `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "twice": "2"}}`, CodeNotAllowed, "/rules/0/do/0/values/twice"},
		{"a rows variable", `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "lines": "1"}}`, CodeNotSupported, "/rules/0/do/0/values/lines"},
		{"a formula of the wrong type", `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "amount": "note"}}`, CodeTypeMismatch, "/rules/0/do/0/values/amount"},
		{"a formula that does not bind", `{"action": "create", "class": "Receipt", "values": {"invoice": "missing"}}`, CodeBadFormula, "/rules/0/do/0/values/invoice"},
		{"a variable given twice", `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "INVOICE": "ID()"}}`, CodeDuplicate, "/rules/0/do/0/values/INVOICE"},
		{"a required input not given", `{"action": "create", "class": "Receipt", "values": {"amount": "total"}}`, CodeMissing, "/rules/0/do/0/values"},
		{"no values where one is required", `{"action": "create", "class": "Receipt"}`, CodeMissing, "/rules/0/do/0/values"},
		{"a value that is not a formula", `{"action": "create", "class": "Receipt", "values": {"invoice": 7}}`, CodeBadValue, "/rules/0/do/0/values"},
		{"an id that is not text", `{"action": "create", "class": "Receipt", "id": "n", "values": {"invoice": "ID()"}}`, CodeTypeMismatch, "/rules/0/do/0/id"},
		{"a misspelt property", `{"action": "create", "class": "Receipt", "value": {"invoice": "ID()"}}`, CodeBadJSON, "/rules/0/do/0"},
	} {
		_, diags := DefineIn([]byte(invoice(tc.action)), env)
		found := false
		for _, d := range diags {
			if d.Code == tc.code && d.Path == tc.path {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
	// A stored definition loads whatever became of the class it names: a
	// class the environment does not have yet, a variable it lost, a type
	// that no longer fits. What it cannot check it leaves to the action.
	lenient := env
	lenient.Lenient = true
	for name, action := range map[string]string{
		"an unknown class":    `{"action": "create", "class": "Voucher", "values": {"x": "n"}}`,
		"an unknown variable": `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "price": "total"}}`,
		"a type that changed": `{"action": "create", "class": "Receipt", "values": {"invoice": "ID()", "amount": "note"}}`,
		"a required input":    `{"action": "create", "class": "Receipt", "values": {"amount": "total"}}`,
	} {
		cls, diags := DefineIn([]byte(invoice(action)), lenient)
		if len(diags) > 0 {
			t.Errorf("%s, loaded leniently: %s", name, diags.Error())
			continue
		}
		if a := cls.Rules[0].Actions[0]; !a.Queued || len(a.Sets) == 0 || a.Sets[len(a.Sets)-1].Formula == nil {
			t.Errorf("%s, loaded leniently: %+v", name, a)
		}
	}
	// A formula that does not bind is a finding even so: the definition
	// itself is broken.
	if _, diags := DefineIn([]byte(invoice(`{"action": "create", "class": "Voucher", "values": {"x": "missing"}}`)), lenient); len(diags) == 0 {
		t.Error("a formula that does not bind loaded leniently")
	}
}

// A create with each (ADR-0044, item 12): each names an array or a rows
// variable of the rule's class, as names the element, and the formulas
// under values and id read the element beside the document.
func TestRules_Each(t *testing.T) {
	approval, diags := Define([]byte(`{"name": "Approval",
 "variables": {"invoice": {"type": "text"}, "approver": {"type": "text"}, "qty": {"type": "int"}, "price": {"type": "money", "scale": 2}},
 "states": {"initial": "asked", "list": ["asked"]}}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	env := Env{Classes: func(name string) (*Class, bool) { return approval, strings.EqualFold(name, "Approval") }}
	invoice := func(action string) string {
		return `{"name": "Invoice",
 "variables": {"note": {"type": "text"}, "approvers": {"type": "array<text>"}, "counts": {"type": "array<int>"},
               "lines": {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money"}}},
 "states": {"initial": "open", "list": ["open", "sent"]}, "transitions": [{"name": "send", "from": "open", "to": "sent"}],
 "rules": [{"on": "enter", "state": "sent", "do": [` + action + `]}]}`
	}
	cls, diags := DefineIn([]byte(invoice(`{"action": "create", "class": "Approval", "each": "APPROVERS", "as": "who", "id": "ID() & \"-\" & who",
 "values": {"invoice": "ID()", "approver": "who & note"}}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	a := cls.Rules[0].Actions[0]
	if !a.Queued || a.Each == nil || a.Each.Name != "approvers" || a.As != "who" || a.ID == nil || len(a.Sets) != 2 {
		t.Fatalf("the compiled create: %+v", a)
	}
	// The element is a slot of its formulas, under the name as gives it.
	slots := func(a Assignment) string {
		var names []string
		for _, s := range a.Formula.Slots {
			names = append(names, s.Name)
		}
		return strings.Join(names, " ")
	}
	if got := slots(a.Sets[1]); got != "who note" {
		t.Errorf("the slots of a formula over an element: %q", got)
	}
	// An element of a rows variable is read by field, and typed by it.
	cls, diags = DefineIn([]byte(invoice(`{"action": "create", "class": "Approval", "each": "lines", "as": "line",
 "values": {"approver": "line.sku", "qty": "line.qty + 1", "price": "line.price"}}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if a := cls.Rules[0].Actions[0]; a.Each.Name != "lines" || slots(a.Sets[0]) != "line.sku" || a.Sets[1].Formula.Result.String() != "int" {
		t.Errorf("the compiled create over rows: %+v", a)
	}
	for _, tc := range []struct{ name, action, code, path string }{
		{"as without each", `{"action": "create", "class": "Approval", "as": "x", "values": {}}`, CodeMissing, "/rules/0/do/0/each"},
		{"each without as", `{"action": "create", "class": "Approval", "each": "approvers", "values": {}}`, CodeMissing, "/rules/0/do/0/as"},
		{"each of an unknown variable", `{"action": "create", "class": "Approval", "each": "people", "as": "x", "values": {}}`, CodeUnknownVariable, "/rules/0/do/0/each"},
		{"each of a scalar", `{"action": "create", "class": "Approval", "each": "note", "as": "x", "values": {}}`, CodeTypeMismatch, "/rules/0/do/0/each"},
		{"as that is a variable of the class", `{"action": "create", "class": "Approval", "each": "approvers", "as": "Note", "values": {}}`, CodeDuplicate, "/rules/0/do/0/as"},
		{"as that is no name", `{"action": "create", "class": "Approval", "each": "approvers", "as": "the one", "values": {}}`, CodeBadName, "/rules/0/do/0/as"},
		{"as that is a keyword", `{"action": "create", "class": "Approval", "each": "approvers", "as": "and", "values": {}}`, CodeReservedName, "/rules/0/do/0/as"},
		{"each that is no name", `{"action": "create", "class": "Approval", "each": 3, "as": "x", "values": {}}`, CodeBadJSON, "/rules/0/do/0"},
		{"an element of the wrong type", `{"action": "create", "class": "Approval", "each": "counts", "as": "n", "values": {"approver": "n"}}`, CodeTypeMismatch, "/rules/0/do/0/values/approver"},
		{"a whole element of a rows variable", `{"action": "create", "class": "Approval", "each": "lines", "as": "line", "values": {"approver": "line"}}`, CodeBadFormula, "/rules/0/do/0/values/approver"},
		{"a field the element lacks", `{"action": "create", "class": "Approval", "each": "lines", "as": "line", "values": {"approver": "line.name"}}`, CodeBadFormula, "/rules/0/do/0/values/approver"},
		{"the element outside a create with each", `{"action": "create", "class": "Approval", "values": {"approver": "who"}}`, CodeBadFormula, "/rules/0/do/0/values/approver"},
	} {
		_, diags := DefineIn([]byte(invoice(tc.action)), env)
		if !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
}

func hasDiag(diags Diagnostics, code, path string) bool {
	for _, d := range diags {
		if d.Code == code && d.Path == path {
			return true
		}
	}
	return false
}

// A transition action and a refresh of another document (ADR-0044, items
// 7 and 13): the class is the rule's own unless named, the transition and
// the variables are the target's, and the id is a formula over the
// document that fires the rule.
func TestRules_OtherDocuments(t *testing.T) {
	parent, diags := Define([]byte(`{"name": "Parent",
 "variables": {"n": {"type": "int"}, "label": {"type": "text"}, "twice": {"formula": "n * 2"},
               "count": {"formula": "n + 1", "mode": "triggered", "on": ["refresh"]},
               "entered": {"formula": "NOW()", "mode": "triggered", "on": ["enter:busy"]}},
 "states": {"initial": "idle", "list": ["idle", "busy"]},
 "transitions": [{"name": "start", "from": "idle", "to": "busy"}, {"from": "busy", "to": "idle", "guard": "n > 9"}]}`), nil)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	sheet, _ := Define([]byte(`{"name": "Sheet", "variables": {"a": {"type": "int"}}}`), nil)
	env := Env{Classes: func(name string) (*Class, bool) {
		switch strings.ToUpper(name) {
		case "PARENT":
			return parent, true
		case "SHEET":
			return sheet, true
		}
		return nil, false
	}}
	child := func(action string) string {
		return `{"name": "Child", "variables": {"parent": {"type": "text"}, "n": {"type": "int"}, "note": {"type": "text"},
               "seen": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "open", "list": ["open", "done"]}, "transitions": [{"name": "finish", "from": "open", "to": "done"}],
 "rules": [{"on": "enter", "state": "done", "do": [` + action + `]}]}`
	}
	cls, diags := DefineIn([]byte(child(`{"action": "transition", "class": "parent", "id": "parent", "name": "start", "values": {"n": "n + 1", "label": "note"}},
 {"action": "transition", "id": "ID()", "name": "finish"},
 {"action": "refresh", "class": "Parent", "id": "parent", "variables": ["COUNT"]},
 {"action": "refresh", "class": "Parent", "id": "parent"},
 {"action": "refresh", "id": "parent", "variables": ["seen"]}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	acts := cls.Rules[0].Actions
	if a := acts[0]; a.Sync || !a.Queued || a.Target != "Parent" || a.Transition != "start" || a.ID == nil || len(a.Sets) != 2 || a.Sets[0].Variable != parent.Variables[0] {
		t.Errorf("a transition of another document: %+v", a)
	}
	if a := acts[1]; !a.Queued || a.Target != "Child" || a.Transition != "finish" || len(a.Sets) != 0 {
		t.Errorf("a transition of a document of the rule's own class: %+v", a)
	}
	if a := acts[2]; a.Sync || !a.Queued || a.Target != "Parent" || strings.Join(a.Names, " ") != "count" || a.ID == nil {
		t.Errorf("a refresh of another document: %+v", a)
	}
	if a := acts[3]; !a.Queued || len(a.Names) != 0 {
		t.Errorf("a refresh of another document that names no variable: %+v", a)
	}
	if a := acts[4]; !a.Queued || a.Target != "Child" || strings.Join(a.Names, " ") != "seen" {
		t.Errorf("a refresh of a document of the rule's own class: %+v", a)
	}
	for _, tc := range []struct{ name, action, code, path string }{
		{"a transition of an unknown class", `{"action": "transition", "class": "Voucher", "id": "parent", "name": "start"}`, CodeUnknownClass, "/rules/0/do/0/class"},
		{"a transition of a class that stores no documents", `{"action": "transition", "class": "Sheet", "id": "parent", "name": "start"}`, CodeNotAllowed, "/rules/0/do/0/class"},
		{"a transition without a name", `{"action": "transition", "class": "Parent", "id": "parent"}`, CodeMissing, "/rules/0/do/0/name"},
		{"a transition the class lacks", `{"action": "transition", "class": "Parent", "id": "parent", "name": "finish"}`, CodeUnknownTransition, "/rules/0/do/0/name"},
		{"a transition without an id", `{"action": "transition", "class": "Parent", "name": "start"}`, CodeMissing, "/rules/0/do/0/id"},
		{"an id that is not text", `{"action": "transition", "class": "Parent", "id": "n", "name": "start"}`, CodeTypeMismatch, "/rules/0/do/0/id"},
		{"a value of an unknown variable", `{"action": "transition", "class": "Parent", "id": "parent", "name": "start", "values": {"note": "note"}}`, CodeUnknownVariable, "/rules/0/do/0/values/note"},
		{"a value of a computed variable", `{"action": "transition", "class": "Parent", "id": "parent", "name": "start", "values": {"twice": "n"}}`, CodeNotAllowed, "/rules/0/do/0/values/twice"},
		{"a value of the wrong type", `{"action": "transition", "class": "Parent", "id": "parent", "name": "start", "values": {"n": "note"}}`, CodeTypeMismatch, "/rules/0/do/0/values/n"},
		{"a transition with a misspelt property", `{"action": "transition", "class": "Parent", "id": "parent", "transition": "start"}`, CodeBadJSON, "/rules/0/do/0"},
		{"a refresh of an unknown class", `{"action": "refresh", "class": "Voucher", "id": "parent"}`, CodeUnknownClass, "/rules/0/do/0/class"},
		{"a refresh that names a class and no document", `{"action": "refresh", "class": "Parent"}`, CodeMissing, "/rules/0/do/0/id"},
		{"a refresh with a blank id", `{"action": "refresh", "class": "Parent", "id": ""}`, CodeMissing, "/rules/0/do/0/id"},
		{"a refresh of a variable the class lacks", `{"action": "refresh", "class": "Parent", "id": "parent", "variables": ["seen"]}`, CodeUnknownVariable, "/rules/0/do/0/variables/0"},
		{"a refresh of an auto variable", `{"action": "refresh", "class": "Parent", "id": "parent", "variables": ["twice"]}`, CodeNotAllowed, "/rules/0/do/0/variables/0"},
		{"a refresh of a variable that does not list refresh", `{"action": "refresh", "class": "Parent", "id": "parent", "variables": ["count", "entered"]}`, CodeNotAllowed, "/rules/0/do/0/variables/1"},
		{"a refresh of one variable twice", `{"action": "refresh", "class": "Parent", "id": "parent", "variables": ["count", "Count"]}`, CodeDuplicate, "/rules/0/do/0/variables/1"},
	} {
		_, diags := DefineIn([]byte(child(tc.action)), env)
		if !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
	// A stored definition loads whatever became of what it names.
	lenient := env
	lenient.Lenient = true
	for name, action := range map[string]string{
		"an unknown class":      `{"action": "transition", "class": "Voucher", "id": "parent", "name": "start", "values": {"x": "n"}}`,
		"a transition it lost":  `{"action": "transition", "class": "Parent", "id": "parent", "name": "stop"}`,
		"a variable it lost":    `{"action": "transition", "class": "Parent", "id": "parent", "name": "start", "values": {"gone": "n"}}`,
		"a type that changed":   `{"action": "transition", "class": "Parent", "id": "parent", "name": "start", "values": {"n": "note"}}`,
		"a refresh it lost":     `{"action": "refresh", "class": "Parent", "id": "parent", "variables": ["gone", "entered"]}`,
		"a refresh of no class": `{"action": "refresh", "class": "Voucher", "id": "parent"}`,
	} {
		cls, diags := DefineIn([]byte(child(action)), lenient)
		if len(diags) > 0 {
			t.Errorf("%s, loaded leniently: %s", name, diags.Error())
			continue
		}
		if a := cls.Rules[0].Actions[0]; !a.Queued || a.ID == nil || a.Target == "" {
			t.Errorf("%s, loaded leniently: %+v", name, a)
		}
	}
}

// The table actions (ADR-0044, item 7): one row of an explicit table,
// addressed by its key, each formula typed against its column.
func TestRules_TableActions(t *testing.T) {
	tbl, diags := DefineTable([]byte(`{"name": "Prices", "columns": {"sku": "text", "min_qty": "int", "price": "money(2)", "note": "text"}, "key": ["sku", "min_qty"]}`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	env := Env{Sources: formula.NewSourceSet(tbl.Source())}
	item := func(action string) string {
		return `{"name": "Item", "variables": {"sku": {"type": "text"}, "qty": {"type": "int"}, "price": {"type": "money", "scale": 2}, "note": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "listed"]}, "transitions": [{"name": "list", "from": "open", "to": "listed"}],
 "rules": [{"on": "enter", "state": "listed", "do": [` + action + `]}]}`
	}
	cls, diags := DefineIn([]byte(item(`{"action": "table.insert", "table": "prices", "row": {"SKU": "sku", "min_qty": "qty", "price": "price"}},
 {"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {"price": "price", "note": "note"}},
 {"action": "table.delete", "table": "Prices", "key": {"min_qty": "qty", "sku": "sku"}}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	acts := cls.Rules[0].Actions
	if a := acts[0]; a.Sync || !a.Queued || a.Target != "Prices" || len(a.Sets) != 3 || a.Sets[0].Name != "sku" || len(a.Keys) != 0 {
		t.Errorf("a table.insert: %+v", a)
	}
	if a := acts[1]; !a.Queued || len(a.Keys) != 2 || len(a.Sets) != 2 || a.Sets[1].Name != "note" || a.Keys[1].Formula.Result.String() != "int" {
		t.Errorf("a table.update: %+v", a)
	}
	if a := acts[2]; !a.Queued || len(a.Keys) != 2 || a.Keys[0].Name != "min_qty" || len(a.Sets) != 0 {
		t.Errorf("a table.delete: %+v", a)
	}
	for _, tc := range []struct{ name, action, code, path string }{
		{"no table", `{"action": "table.insert", "row": {"sku": "sku"}}`, CodeMissing, "/rules/0/do/0/table"},
		{"an unknown table", `{"action": "table.insert", "table": "Costs", "row": {"sku": "sku"}}`, CodeUnknownTable, "/rules/0/do/0/table"},
		{"an insert without a row", `{"action": "table.insert", "table": "Prices"}`, CodeMissing, "/rules/0/do/0/row"},
		{"an insert with a key", `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": "1"}, "key": {"sku": "sku"}}`, CodeNotAllowed, "/rules/0/do/0/key"},
		{"an insert that leaves a key column out", `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "price": "price"}}`, CodeMissing, "/rules/0/do/0/row"},
		{"an insert of an unknown column", `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": "1", "cost": "price"}}`, CodeUnknownVariable, "/rules/0/do/0/row/cost"},
		{"an insert of the wrong type", `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": "note"}}`, CodeTypeMismatch, "/rules/0/do/0/row/min_qty"},
		{"a column given twice", `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": "1", "SKU": "sku"}}`, CodeDuplicate, "/rules/0/do/0/row/SKU"},
		{"a row that is not formulas", `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": 1}}`, CodeBadValue, "/rules/0/do/0/row"},
		{"an empty row", `{"action": "table.insert", "table": "Prices", "row": {}}`, CodeMissing, "/rules/0/do/0/row"},
		{"an update without a key", `{"action": "table.update", "table": "Prices", "set": {"price": "price"}}`, CodeMissing, "/rules/0/do/0/key"},
		{"an update without a set", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}}`, CodeMissing, "/rules/0/do/0/set"},
		{"an update with an empty set", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {}}`, CodeMissing, "/rules/0/do/0/set"},
		{"an update with a row", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {"price": "price"}, "row": {"sku": "sku"}}`, CodeNotAllowed, "/rules/0/do/0/row"},
		{"an update with half a key", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku"}, "set": {"price": "price"}}`, CodeMissing, "/rules/0/do/0/key"},
		{"an update whose key names another column", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1", "note": "note"}, "set": {"price": "price"}}`, CodeNotAllowed, "/rules/0/do/0/key/note"},
		{"an update that changes a key column", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {"min_qty": "2"}}`, CodeNotAllowed, "/rules/0/do/0/set/min_qty"},
		{"an update of the wrong type", `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {"price": "qty"}}`, CodeTypeMismatch, "/rules/0/do/0/set/price"},
		{"a delete with a set", `{"action": "table.delete", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {"price": "price"}}`, CodeNotAllowed, "/rules/0/do/0/set"},
		{"a delete without a key", `{"action": "table.delete", "table": "Prices"}`, CodeMissing, "/rules/0/do/0/key"},
		{"a misspelt property", `{"action": "table.delete", "table": "Prices", "keys": {"sku": "sku"}}`, CodeBadJSON, "/rules/0/do/0"},
	} {
		_, diags := DefineIn([]byte(item(tc.action)), env)
		if !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
	// A stored definition loads whatever became of the table.
	lenient := env
	lenient.Lenient = true
	for name, action := range map[string]string{
		"an unknown table":    `{"action": "table.insert", "table": "Costs", "row": {"sku": "sku"}}`,
		"a column it lost":    `{"action": "table.update", "table": "Prices", "key": {"sku": "sku", "min_qty": "1"}, "set": {"cost": "price"}}`,
		"a key that changed":  `{"action": "table.delete", "table": "Prices", "key": {"sku": "sku"}}`,
		"a type that changed": `{"action": "table.insert", "table": "Prices", "row": {"sku": "sku", "min_qty": "note"}}`,
	} {
		cls, diags := DefineIn([]byte(item(action)), lenient)
		if len(diags) > 0 {
			t.Errorf("%s, loaded leniently: %s", name, diags.Error())
			continue
		}
		if a := cls.Rules[0].Actions[0]; !a.Queued || a.Target == "" || len(a.Sets)+len(a.Keys) == 0 {
			t.Errorf("%s, loaded leniently: %+v", name, a)
		}
	}
}
