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
		// What reads a request or its reply belongs to a definition that has
		// a request.
		{"a connection without a request", `{"name": "a.b", "connection": {"name": "slack", "secrets": ["token"]}}`, CodeNotAllowed, "/connection"},
		{"ok without a request", `{"name": "a.b", "ok": "STATUS = 200"}`, CodeNotAllowed, "/ok"},
		{"a result without a request", `{"name": "a.b", "returns": {"ts": "text"}, "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}`, CodeNotAllowed, "/result"},
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

// A declared action (ADR-0045, items 3, 4 and 6): a definition with a
// request the engine sends. The url, the headers and every string of the
// body are pure formulas over the parameters and the secrets of the
// connection; ok and result are pure formulas over the reply.
func TestAction_Declared(t *testing.T) {
	a, diags := DefineAction([]byte(`{"name": "slack.post", "version": 1, "label": "Post a message to Slack",
 "params": {"channel": {"type": "text", "required": true}, "text": {"type": "text", "required": true}, "urgent": "bool"},
 "returns": {"ts": "text", "code": "number"},
 "connection": {"name": "slack", "secrets": ["token", "signing_key"]},
 "request": {"method": "post",
             "url": "\"https://slack.com/api/chat.postMessage\"",
             "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")", "X-Channel": "channel"},
             "body": {"channel": "channel", "text": "IF(urgent, \"! \", \"\") & text", "blocks": [{"type": "\"section\"", "n": 1}]},
             "sign": "SIGNING_KEY"},
 "ok": "JSONVALUE(BODY, \"ok\") = \"TRUE\"",
 "result": {"ts": "JSONVALUE(BODY, \"ts\")", "code": "STATUS"}}`))
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	req := a.Request
	if !a.Declared() || a.Connection != "slack" || strings.Join(a.Secrets, " ") != "token signing_key" || req == nil {
		t.Fatalf("the definition: %+v", a)
	}
	if req.Method != "POST" || req.ConstURL != "https://slack.com/api/chat.postMessage" || req.Sign != "signing_key" || req.Dynamic {
		t.Errorf("the request: %+v", req)
	}
	if len(req.Headers) != 2 || req.Headers[0].Name != "Authorization" || len(req.Headers[0].Value.Slots) != 1 || req.Headers[0].Value.Slots[0].Name != formula.SecretSlot+"token" || req.Headers[1].Name != "X-Channel" {
		t.Errorf("the headers, in the order written: %+v", req.Headers)
	}
	if req.Body == nil || !req.Body.Object || len(req.Body.Fields) != 3 || req.Body.Fields[1].Value.Formula == nil || !req.Body.Fields[2].Value.Array {
		t.Errorf("the body: %+v", req.Body)
	}
	if a.OK == nil || len(a.Result) != 2 || a.Result[0].Name != "ts" || a.Result[1].Formula.Result.Kind != formula.KindNumber {
		t.Errorf("the reply: ok %v, result %+v", a.OK, a.Result)
	}
	// The smallest declared action: an address. POST, no connection, the
	// call itself as the body, any 2xx a success.
	a, diags = DefineAction([]byte(`{"name": "hook.send", "params": {"path": "text"}, "request": {"url": "\"https://hooks.example/\" & URLENCODE(path)"}}`))
	if len(diags) > 0 || a.Request.Method != "POST" || a.Request.Body != nil || a.Request.ConstURL != "" || a.Connection != "" || len(a.Secrets) != 0 || a.OK != nil {
		t.Errorf("the smallest declared action: %+v, %v", a, diags)
	}
	// A GET says everything in its address.
	if a, diags := DefineAction([]byte(`{"name": "rates.get", "params": {"code": "text"}, "returns": {"rate": "decimal(4)"},
 "request": {"method": "GET", "url": "\"https://rates.example/v1?code=\" & URLENCODE(code)"}, "result": {"rate": "DECIMALVALUE(JSONVALUE(BODY, \"rate\"), 4)"}}`)); len(diags) > 0 || a.Request.Method != "GET" {
		t.Errorf("a GET: %v", diags)
	}

	slack := func(change string) string {
		def := `{"name": "slack.post", "params": {"channel": "text", "n": "number"}, "returns": {"ts": "text"},
 "connection": {"name": "slack", "secrets": ["token"]},
 "request": {"method": "POST", "url": "\"https://slack.com/api\"", "headers": {"Authorization": "SECRET(\"token\")"}, "body": {"channel": "channel"}},
 "ok": "STATUS = 200", "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}`
		old, new, _ := strings.Cut(change, " => ")
		if !strings.Contains(def, old) {
			t.Fatalf("the definition has no %q", old)
		}
		return strings.Replace(def, old, new, 1)
	}
	for _, tc := range []struct{ name, change, code, path string }{
		{"no url", `"url": "\"https://slack.com/api\"", => `, CodeMissing, "/request/url"},
		{"an unknown method", `"method": "POST" => "method": "FETCH"`, CodeBadValue, "/request/method"},
		{"a url that is no text", `"\"https://slack.com/api\"" => "n"`, CodeTypeMismatch, "/request/url"},
		{"a constant url that is no address", `"\"https://slack.com/api\"" => "\"slack.com/api\""`, CodeBadValue, "/request/url"},
		{"a constant url of another scheme", `"\"https://slack.com/api\"" => "\"ftp://slack.com/api\""`, CodeBadValue, "/request/url"},
		{"a constant url with a password", `"\"https://slack.com/api\"" => "\"https://bot:pw@slack.com/api\""`, CodeBadValue, "/request/url"},
		{"a url that reads the document's context", `"\"https://slack.com/api\"" => "\"https://slack.com/\" & USER()"`, CodeBadFormula, "/request/url"},
		{"a url that reads the clock", `"\"https://slack.com/api\"" => "\"https://slack.com/\" & TEXT(NOW())"`, CodeBadFormula, "/request/url"},
		{"a url that reads an unknown parameter", `"\"https://slack.com/api\"" => "\"https://slack.com/\" & team"`, CodeBadFormula, "/request/url"},
		{"a header that is no text", `"SECRET(\"token\")" => "n"`, CodeTypeMismatch, "/request/headers/Authorization"},
		{"a header the engine sets", `"Authorization": "SECRET(\"token\")" => "Idempotency-Key": "channel"`, CodeReservedName, "/request/headers/Idempotency-Key"},
		{"the signature header", `"Authorization": "SECRET(\"token\")" => "fadodb-signature": "channel"`, CodeReservedName, "/request/headers/fadodb-signature"},
		{"a header name that is no token", `"Authorization": => "Auth orization":`, CodeBadName, "/request/headers/Auth orization"},
		{"a header given twice", `"Authorization": "SECRET(\"token\")" => "X-A": "channel", "x-a": "channel"`, CodeDuplicate, "/request/headers/x-a"},
		{"a secret the connection does not declare", `SECRET(\"token\") => SECRET(\"password\")`, CodeBadFormula, "/request/headers/Authorization"},
		{"a secret named by a formula", `SECRET(\"token\") => SECRET(channel)`, CodeBadFormula, "/request/headers/Authorization"},
		{"a secret without a connection", `"connection": {"name": "slack", "secrets": ["token"]}, => `, CodeBadFormula, "/request/headers/Authorization"},
		{"a body formula that does not bind", `{"channel": "channel"} => {"channel": ["chanel"]}`, CodeBadFormula, "/request/body/channel/0"},
		{"a body on a GET", `"method": "POST" => "method": "GET"`, CodeNotAllowed, "/request/body"},
		{"a secret in the body of another connection's name", `{"channel": "channel"} => {"key": "SECRET(\"other\")"}`, CodeBadFormula, "/request/body/key"},
		{"sign with a secret the connection lacks", `"method": "POST", => "method": "POST", "sign": "signing_key",`, CodeUnknownSecret, "/request/sign"},
		{"a connection without a name", `{"name": "slack", "secrets": ["token"]} => {"secrets": ["token"]}`, CodeBadName, "/connection/name"},
		{"a secret that is no name", `"secrets": ["token"] => "secrets": ["token", "my key"]`, CodeBadName, "/connection/secrets/1"},
		{"a secret declared twice", `"secrets": ["token"] => "secrets": ["token", "Token"]`, CodeDuplicate, "/connection/secrets/1"},
		{"a misspelt property of the connection", `"secrets": ["token"] => "secrets": ["token"], "hosts": ["slack.com"]`, CodeBadJSON, ""},
		{"a misspelt property of the request", `"method": "POST", => "verb": "POST",`, CodeBadJSON, ""},
		{"ok that is no condition", `"STATUS = 200" => "STATUS"`, CodeNotACondition, "/ok"},
		{"ok that reads a parameter", `"STATUS = 200" => "channel = \"x\""`, CodeBadFormula, "/ok"},
		{"ok that reads a secret", `"STATUS = 200" => "SECRET(\"token\") = \"x\""`, CodeBadFormula, "/ok"},
		{"a result the action does not return", `{"ts": "JSONVALUE(BODY, \"ts\")"} => {"id": "JSONVALUE(BODY, \"ts\")"}`, CodeUnknownParameter, "/result/id"},
		{"a result of another type", `"JSONVALUE(BODY, \"ts\")" => "STATUS"`, CodeTypeMismatch, "/result/ts"},
		{"a result given twice", `{"ts": "JSONVALUE(BODY, \"ts\")"} => {"ts": "\"a\"", "TS": "\"b\""}`, CodeDuplicate, "/result/TS"},
		{"a result that is not formulas", `{"ts": "JSONVALUE(BODY, \"ts\")"} => {"ts": 7}`, CodeBadValue, "/result"},
	} {
		a, diags := DefineAction([]byte(slack(tc.change)))
		if a != nil || !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %q, got %+v:\n%s", tc.name, tc.code, tc.path, a, diags.Error())
		}
	}
	// The message of a secret used where it may not be says where it may.
	_, diags = DefineAction([]byte(slack(`"STATUS = 200" => "SECRET(\"token\") = \"x\""`)))
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "only in the request") {
		t.Errorf("a secret outside the request: %v", diags)
	}
}

// http.call is the declared action the engine defines (ADR-0045, item
// 3): the address, the method, the headers and the body are arguments.
// It is defined everywhere, and nobody defines an action in its
// namespace.
func TestAction_HTTPCall(t *testing.T) {
	a := HTTPCall()
	if a != HTTPCall() || a.Name != "http.call" || !a.Declared() || !a.Request.Dynamic || a.Connection != "" || len(a.Params) != 4 || !a.Params[0].Required || len(a.Returns) != 2 {
		t.Fatalf("http.call: %+v", a)
	}
	inv := func(action string) string {
		return `{"name": "Inv", "variables": {"number": {"type": "text"}, "total": {"type": "number"}, "hook": {"type": "text"}},
 "states": {"initial": "open", "list": ["open", "sent"]}, "transitions": [{"name": "send", "from": "open", "to": "sent"}],
 "rules": [{"on": "enter", "state": "sent", "do": [` + action + `]}]}`
	}
	cls, diags := Define([]byte(inv(`{"action": "HTTP.Call", "args": {"url": "hook & \"/\" & URLENCODE(number)", "method": "\"PUT\"",
   "headers": {"X-Number": "number"}, "body": {"number": "number", "total": "total", "by": "USER()"}}}`)), nil)
	if len(diags) > 0 {
		t.Fatal(diags.Error())
	}
	if c := cls.Rules[0].Actions[0]; !c.Call || c.Name != "http.call" || c.Spec != a || len(c.Args) != 4 || c.Args[2].Template == nil {
		t.Errorf("a call of http.call: %+v", c)
	}
	for _, tc := range []struct{ name, action, code, path string }{
		{"no address", `{"action": "http.call", "args": {"method": "\"GET\""}}`, CodeMissing, "/rules/0/do/0/args"},
		{"an address that is no text", `{"action": "http.call", "args": {"url": "total"}}`, CodeTypeMismatch, "/rules/0/do/0/args/url"},
		{"an unknown argument", `{"action": "http.call", "args": {"url": "hook", "timeout": "5"}}`, CodeUnknownParameter, "/rules/0/do/0/args/timeout"},
		{"the shape of before the definitions", `{"action": "http.call", "url": "hook"}`, CodeBadJSON, "/rules/0/do/0"},
		{"a secret, which no rule reads", `{"action": "http.call", "args": {"url": "hook", "headers": {"Authorization": "SECRET(\"token\")"}}}`, CodeBadFormula, "/rules/0/do/0/args/headers/Authorization"},
	} {
		if _, diags := Define([]byte(inv(tc.action)), nil); !hasDiag(diags, tc.code, tc.path) {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
	for _, name := range []string{"http.call", "http.get", "HTTP.post"} {
		if _, diags := DefineAction([]byte(`{"name": "` + name + `", "request": {"url": "\"https://example.test\""}}`)); !hasDiag(diags, CodeReservedName, "/name") {
			t.Errorf("a definition named %s: %v", name, diags)
		}
	}
}
