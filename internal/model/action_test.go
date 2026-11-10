package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// An action definition (ADR-0045, item 1): a name of a namespace and a
// verb, parameters and returned values by scalar type, each written as
// its type alone or in full.
func TestAction_Define(t *testing.T) {
	a, diags := DefineAction([]byte(`{"name": "books.post", "label": "Post to the books", "description": "Books an invoice.",
 "params": {"invoice": {"type": "text", "required": true, "description": "The invoice's id"}, "amount": "money(2)", "day": {"type": "date"}, "payload": "json"},
 "returns": {"entry": "text", "posted_at": {"type": "datetime", "description": "When the books took it"}}}`))
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if a.Name != "books.post" || a.Version != 1 || a.Label != "Post to the books" || len(a.Params) != 4 || len(a.Returns) != 2 {
		t.Fatalf("the definition: %+v", a)
	}
	// The order is the order written.
	if p := a.Params[0]; p.Name != "invoice" || !p.Required || p.Type.Kind != formula.KindText || p.Description == "" {
		t.Errorf("the first parameter: %+v", p)
	}
	if p := a.Params[1]; p.Name != "amount" || p.Required || p.Type.String() != "money(2)" {
		t.Errorf("a parameter written as its type alone: %+v", p)
	}
	if p, ok := a.Param("PAYLOAD"); !ok || p.Type.Kind != formula.KindJSON {
		t.Errorf("a parameter is found whatever its case: %+v", p)
	}
	if r, ok := a.Return("posted_at"); !ok || r.Type.Kind != formula.KindDateTime {
		t.Errorf("a returned value: %+v", r)
	}
	if _, ok := a.Param("entry"); ok {
		t.Error("a returned value is no parameter")
	}
	// An action that takes nothing and returns nothing is one too.
	if a, diags := DefineAction([]byte(`{"name": "audit.ping", "version": 3}`)); len(diags) > 0 || a.Version != 3 || len(a.Params) != 0 {
		t.Errorf("the smallest definition: %+v, %v", a, diags)
	}
}

func TestAction_Diagnostics(t *testing.T) {
	for _, tc := range []struct{ name, src, code, path string }{
		{"no name", `{"params": {"a": "text"}}`, CodeMissing, "/name"},
		{"a name without a namespace", `{"name": "post"}`, CodeReservedName, "/name"},
		{"an engine action's name", `{"name": "create"}`, CodeReservedName, "/name"},
		{"the table namespace", `{"name": "table.upsert"}`, CodeReservedName, "/name"},
		{"the http namespace", `{"name": "HTTP.get"}`, CodeReservedName, "/name"},
		{"a name that is no identifier", `{"name": "slack post.now"}`, CodeBadName, "/name"},
		{"a part that starts with a digit", `{"name": "slack.2post"}`, CodeBadName, "/name"},
		{"an empty part", `{"name": "slack..post"}`, CodeBadName, "/name"},
		{"a name too long", `{"name": "a.` + strings.Repeat("x", MaxActionName) + `"}`, CodeBadName, "/name"},
		{"a negative version", `{"name": "a.b", "version": -1}`, CodeBadValue, "/version"},
		{"a misspelt property", `{"name": "a.b", "parameters": {}}`, CodeBadJSON, ""},
		{"params that are no object", `{"name": "a.b", "params": ["x"]}`, CodeBadJSON, ""},
		{"a parameter that is no name", `{"name": "a.b", "params": {"my-arg": "text"}}`, CodeBadName, "/params/my-arg"},
		{"a parameter declared twice", `{"name": "a.b", "params": {"x": "text", "X": "text"}}`, CodeDuplicate, "/params/X"},
		{"a parameter without a type", `{"name": "a.b", "params": {"x": {"required": true}}}`, CodeMissing, "/params/x/type"},
		{"an unknown type", `{"name": "a.b", "params": {"x": "string"}}`, CodeUnknownType, "/params/x"},
		{"a blank type", `{"name": "a.b", "params": {"x": "blank"}}`, CodeUnknownType, "/params/x"},
		{"a composite parameter", `{"name": "a.b", "params": {"x": "array<text>"}}`, CodeNotAllowed, "/params/x"},
		{"a rows parameter", `{"name": "a.b", "params": {"x": "rows"}}`, CodeNotAllowed, "/params/x"},
		{"a misspelt property of a parameter", `{"name": "a.b", "params": {"x": {"type": "text", "require": true}}}`, CodeBadJSON, "/params/x"},
		{"a parameter that is a number", `{"name": "a.b", "params": {"x": 1}}`, CodeBadJSON, "/params/x"},
		{"a required returned value", `{"name": "a.b", "returns": {"x": {"type": "text", "required": true}}}`, CodeNotAllowed, "/returns/x/required"},
		{"a returned value of an unknown type", `{"name": "a.b", "returns": {"x": "object"}}`, CodeUnknownType, "/returns/x"},
		// A declared action is read and refused: this build sends nothing.
		{"a request", `{"name": "a.b", "request": {"method": "POST", "url": "\"https://example.test\""}}`, CodeNotSupported, "/request"},
		{"a connection", `{"name": "a.b", "connection": {"name": "slack", "secrets": ["token"]}}`, CodeNotSupported, "/connection"},
		{"a result", `{"name": "a.b", "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}`, CodeNotSupported, "/result"},
	} {
		a, diags := DefineAction([]byte(tc.src))
		if a != nil || !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %q, got %+v:\n%s", tc.name, tc.code, tc.path, a, diags.Error())
		}
	}
}

// A rule names a defined action and gives its arguments under args
// (ADR-0045, item 1): each a formula typed against its parameter, or for
// a json parameter a template whose every string is a formula.
func TestRules_Calls(t *testing.T) {
	post, diags := DefineAction([]byte(`{"name": "books.post",
 "params": {"invoice": {"type": "text", "required": true}, "amount": "decimal(2)", "day": "date", "payload": "json", "urgent": "bool"},
 "returns": {"entry": "text"}}`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	ping, _ := DefineAction([]byte(`{"name": "audit.ping"}`))
	env := Env{Actions: func(name string) (*ActionSpec, bool) {
		for _, a := range []*ActionSpec{post, ping} {
			if strings.EqualFold(a.Name, name) {
				return a, true
			}
		}
		return nil, false
	}}
	inv := func(action string) string {
		return `{"name": "Inv", "variables": {"number": {"type": "text"}, "total": {"type": "decimal", "scale": 2}, "net": {"type": "decimal", "scale": 4},
   "due": {"type": "date"}, "meta": {"type": "json"}, "qty": {"type": "number"}, "tags": {"type": "array<text>"}},
 "states": {"initial": "open", "list": ["open", "sent"]}, "transitions": [{"name": "send", "from": "open", "to": "sent"}],
 "rules": [{"on": "enter", "state": "sent", "do": [` + action + `]}]}`
	}
	cls, diags := DefineIn([]byte(inv(`{"action": "Books.Post", "args": {"INVOICE": "number", "amount": "total", "day": "ADDDAYS(due, 14)", "urgent": "qty > 10",
    "payload": {"number": "number", "lines": ["qty", "qty * 2", 3, true, null], "meta": "meta", "nested": {"total": "total"}, "none": {}, "empty": []}}},
 {"action": "audit.ping"},
 {"action": "books.post", "args": {"invoice": "\"fixed\"", "payload": "meta"}}`)), env)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	acts := cls.Rules[0].Actions
	a := acts[0]
	if a.Sync || !a.Queued || !a.Call || a.Name != "books.post" || a.Spec != post || len(a.Args) != 5 || a.Path() != "/rules/0/do/0" {
		t.Fatalf("a call: %+v", a)
	}
	// The argument is named as the definition declares its parameter.
	if arg := a.Args[0]; arg.Name != "invoice" || arg.Param == nil || arg.Formula == nil || arg.Template != nil || arg.Src != "number" {
		t.Errorf("an argument: %+v", arg)
	}
	if arg := a.Args[1]; arg.Formula.Result.String() != "decimal(2)" {
		t.Errorf("an exact argument has the parameter's type: %s", arg.Formula.Result)
	}
	tmpl := a.Args[4].Template
	if a.Args[4].Formula != nil || tmpl == nil || !tmpl.Object || len(tmpl.Fields) != 6 {
		t.Fatalf("a template: %+v", tmpl)
	}
	if f := tmpl.Fields[0]; f.Name != "number" || f.Value.Formula == nil || f.Value.Src != "number" {
		t.Errorf("a string of a template is a formula: %+v", f)
	}
	lines := tmpl.Fields[1].Value
	if !lines.Array || len(lines.Items) != 5 || lines.Items[1].Formula == nil || string(lines.Items[2].Literal) != "3" || string(lines.Items[3].Literal) != "true" || string(lines.Items[4].Literal) != "null" {
		t.Errorf("an array of a template: %+v", lines)
	}
	if none, empty := tmpl.Fields[4].Value, tmpl.Fields[5].Value; !none.Object || len(none.Fields) != 0 || !empty.Array || len(empty.Items) != 0 {
		t.Errorf("an empty object and an empty array stay what they are: %+v, %+v", none, empty)
	}
	if a := acts[1]; !a.Call || a.Name != "audit.ping" || len(a.Args) != 0 {
		t.Errorf("a call without arguments: %+v", a)
	}
	// A json parameter also takes a formula that gives a json value.
	if arg := acts[2].Args[1]; arg.Formula == nil || arg.Template != nil {
		t.Errorf("a json argument given by a formula: %+v", arg)
	}

	deep := strings.Repeat(`{"a": `, MaxTemplateDepth+1) + `"number"` + strings.Repeat("}", MaxTemplateDepth+1)
	for _, tc := range []struct{ name, action, code, path string }{
		{"an action nobody defined", `{"action": "slack.post", "args": {"text": "number"}}`, CodeUnknownAction, "/rules/0/do/0/action"},
		{"an engine action in another case", `{"action": "Set", "values": {"number": "\"x\""}}`, CodeBadJSON, "/rules/0/do/0"},
		{"an unknown parameter", `{"action": "books.post", "args": {"invoice": "number", "memo": "number"}}`, CodeUnknownParameter, "/rules/0/do/0/args/memo"},
		{"a required parameter left out", `{"action": "books.post", "args": {"amount": "total"}}`, CodeMissing, "/rules/0/do/0/args"},
		{"no arguments where one is required", `{"action": "books.post"}`, CodeMissing, "/rules/0/do/0/args"},
		{"an argument of the wrong type", `{"action": "books.post", "args": {"invoice": "total"}}`, CodeTypeMismatch, "/rules/0/do/0/args/invoice"},
		{"an exact argument at a finer scale", `{"action": "books.post", "args": {"invoice": "number", "amount": "net"}}`, CodeTypeMismatch, "/rules/0/do/0/args/amount"},
		{"a parameter given twice", `{"action": "books.post", "args": {"invoice": "number", "Invoice": "number"}}`, CodeDuplicate, "/rules/0/do/0/args/Invoice"},
		{"an argument that is no formula", `{"action": "books.post", "args": {"invoice": 7}}`, CodeBadValue, "/rules/0/do/0/args/invoice"},
		{"a template for a parameter that is not json", `{"action": "books.post", "args": {"invoice": {"id": "number"}}}`, CodeBadValue, "/rules/0/do/0/args/invoice"},
		{"a formula that does not bind", `{"action": "books.post", "args": {"invoice": "nmber"}}`, CodeBadFormula, "/rules/0/do/0/args/invoice"},
		{"a formula of a template that does not bind", `{"action": "books.post", "args": {"invoice": "number", "payload": {"a": ["nmber"]}}}`, CodeBadFormula, "/rules/0/do/0/args/payload/a/0"},
		{"a text formula for a json parameter", `{"action": "books.post", "args": {"invoice": "number", "payload": "number"}}`, CodeTypeMismatch, "/rules/0/do/0/args/payload"},
		{"a template too deep", `{"action": "books.post", "args": {"invoice": "number", "payload": ` + deep + `}}`, CodeTooDeep, "/rules/0/do/0/args/payload" + strings.Repeat("/a", MaxTemplateDepth)},
		{"args that are no object", `{"action": "books.post", "args": ["number"]}`, CodeBadValue, "/rules/0/do/0/args"},
		{"into, which waits", `{"action": "books.post", "args": {"invoice": "number"}, "into": {"number": "entry"}}`, CodeNotSupported, "/rules/0/do/0/into"},
		{"a misspelt property", `{"action": "books.post", "arguments": {"invoice": "number"}}`, CodeBadJSON, "/rules/0/do/0"},
	} {
		_, diags := DefineIn([]byte(inv(tc.action)), env)
		if !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
	// The message of a mismatch names the parameter, not a variable.
	_, diags = DefineIn([]byte(inv(`{"action": "books.post", "args": {"invoice": "total"}}`)), env)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "the parameter invoice is text but the formula is decimal(2)") {
		t.Errorf("the message of a mismatch: %v", diags)
	}

	// A stored class loads whatever became of the action: one the database
	// no longer has, a parameter it lost, a type that no longer fits, a
	// parameter it now requires. Each is found when the action runs.
	lenient := env
	lenient.Lenient = true
	for name, action := range map[string]string{
		"an action the database lost":   `{"action": "slack.post", "args": {"text": "number", "blocks": {"a": "qty"}}}`,
		"a parameter it lost":           `{"action": "books.post", "args": {"invoice": "number", "memo": "number"}}`,
		"a type that no longer fits":    `{"action": "books.post", "args": {"invoice": "total"}}`,
		"a parameter it now requires":   `{"action": "books.post", "args": {"amount": "total"}}`,
		"a template where a text was":   `{"action": "books.post", "args": {"invoice": {"id": "number"}}}`,
		"into, written by a later step": `{"action": "books.post", "args": {"invoice": "number"}, "into": {"number": "entry"}}`,
	} {
		cls, diags := DefineIn([]byte(inv(action)), lenient)
		if len(diags) > 0 {
			t.Errorf("%s: a stored class loads: %s", name, diags.Error())
			continue
		}
		if a := cls.Rules[0].Actions[0]; !a.Queued || !a.Call || len(a.Args) == 0 {
			t.Errorf("%s: the call is kept: %+v", name, a)
		}
	}
	// What does not parse is a finding even of a stored class.
	if _, diags := DefineIn([]byte(inv(`{"action": "books.post", "args": {"invoice": "nmber"}}`)), lenient); !hasDiag(diags, CodeBadFormula, "/rules/0/do/0/args/invoice") {
		t.Errorf("a formula that does not bind: %v", diags)
	}
}
