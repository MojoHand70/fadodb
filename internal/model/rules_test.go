package model

import (
	"strings"
	"testing"
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
	          {"action": "slack.post", "args": {"text": "note"}},
	          {"action": "refresh", "variables": ["stamp", "snap"]},
	          {"action": "refresh"},
	          {"action": "refresh", "class": "Other", "id": "note", "variables": ["count"]}]},
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
	if a := r.Actions[6]; a.Sync || a.Name != ActionRefresh || len(a.Variables) != 0 {
		t.Errorf("a refresh of another document is kept as written for the outbox: %+v", a)
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
	if post.Sync || post.Name != "slack.post" || post.Path() != "/rules/0/do/3" || !strings.Contains(string(post.Raw), "args") {
		t.Errorf("an action of the outbox is kept as written: %+v", post)
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
		{"each, which waits", `{"action": "create", "class": "Receipt", "each": "note", "as": "x", "values": {"invoice": "ID()"}}`, CodeNotSupported, "/rules/0/do/0/each"},
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
