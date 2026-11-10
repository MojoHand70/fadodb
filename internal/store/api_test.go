package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

// The schemas under api/ are public contracts (ADR-0026, ADR-0045, item
// 4). The tests here hold them to what the engine does: the definitions
// the engine takes are the ones action.schema.json takes, every call the
// worker makes is a call.schema.json with an event.schema.json in it, and
// the properties of the schemas are the fields of the Go types.

// schema is a JSON Schema read for the keywords the published schemas
// use: type, required, properties, additionalProperties, items, enum,
// pattern, minimum, minLength, maxLength, anyOf and $ref, to a definition
// of the same file or to another file of api/.
type schema map[string]any

func loadSchema(t testing.TB, name string) schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", name))
	if err != nil {
		t.Fatal(err)
	}
	var s schema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

// check returns what is wrong with the value under the schema, each
// finding with the path of the value it is about.
func (s schema) check(t testing.TB, root schema, path string, v any) []string {
	if ref, ok := s["$ref"].(string); ok {
		if def, local := strings.CutPrefix(ref, "#/$defs/"); local {
			target, ok := root["$defs"].(map[string]any)[def].(map[string]any)
			if !ok {
				t.Fatalf("the schema refers to %s, which it does not define", ref)
			}
			return schema(target).check(t, root, path, v)
		}
		other := loadSchema(t, ref)
		return other.check(t, other, path, v)
	}
	var out []string
	bad := func(format string, args ...any) { out = append(out, path+": "+fmt.Sprintf(format, args...)) }
	if alts, ok := s["anyOf"].([]any); ok {
		fits := false
		for _, alt := range alts {
			fits = fits || len(schema(alt.(map[string]any)).check(t, root, path, v)) == 0
		}
		if !fits {
			bad("fits none of the alternatives")
		}
	}
	if want, ok := s["type"].(string); ok {
		got := "null"
		switch x := v.(type) {
		case bool:
			got = "boolean"
		case string:
			got = "string"
		case float64:
			got = "number"
			if x == float64(int64(x)) {
				got = "integer"
			}
		case []any:
			got = "array"
		case map[string]any:
			got = "object"
		}
		if got != want && !(want == "number" && got == "integer") {
			bad("is %s, want %s", got, want)
			return out
		}
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || reflect.DeepEqual(e, v)
		}
		if !found {
			bad("%v is none of %v", v, enum)
		}
	}
	switch x := v.(type) {
	case string:
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			bad("%q does not match %s", x, p)
		}
		if n, ok := s["minLength"].(float64); ok && len(x) < int(n) {
			bad("%q is shorter than %v", x, n)
		}
		if n, ok := s["maxLength"].(float64); ok && len(x) > int(n) {
			bad("is longer than %v", n)
		}
	case float64:
		if n, ok := s["minimum"].(float64); ok && x < n {
			bad("%v is below %v", x, n)
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, item := range x {
				out = append(out, schema(items).check(t, root, fmt.Sprintf("%s/%d", path, i), item)...)
			}
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, name := range req {
				if _, has := x[name.(string)]; !has {
					bad("has no %s", name)
				}
			}
		}
		for name, value := range x {
			switch prop, known := props[name].(map[string]any); {
			case known:
				out = append(out, schema(prop).check(t, root, path+"/"+name, value)...)
			case s["additionalProperties"] == false:
				bad("has %s, which the schema does not", name)
			default:
				if more, ok := s["additionalProperties"].(map[string]any); ok {
					out = append(out, schema(more).check(t, root, path+"/"+name, value)...)
				}
			}
		}
	}
	return out
}

func (s schema) validate(t testing.TB, raw []byte) []string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []string{err.Error()}
	}
	return s.check(t, s, "", v)
}

// jsonFields are the JSON names of a struct's fields, sorted.
func jsonFields(v any) []string {
	var out []string
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func propertyNames(s map[string]any) []string {
	var out []string
	for name := range s["properties"].(map[string]any) {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestAPISchemas(t *testing.T) {
	actionSchema, callSchema, eventSchema := loadSchema(t, "action.schema.json"), loadSchema(t, "call.schema.json"), loadSchema(t, "event.schema.json")

	// The properties of the schemas are the fields of the Go types.
	for name, pair := range map[string][2][]string{
		"the call":        {propertyNames(callSchema), jsonFields(ActionCall{})},
		"the event":       {propertyNames(eventSchema), jsonFields(DeliveredEvent{})},
		"the document":    {propertyNames(eventSchema["$defs"].(map[string]any)["document"].(map[string]any)), jsonFields(Document{})},
		"a moment":        {propertyNames(eventSchema["$defs"].(map[string]any)["document"].(map[string]any)["properties"].(map[string]any)["computed"].(map[string]any)["additionalProperties"].(map[string]any)), jsonFields(Moment{})},
		"the definition":  {propertyNames(actionSchema), []string{"description", "label", "name", "params", "returns", "version"}},
		"the event kinds": {kindsOf(eventSchema), {EventCreated, EventDeleted, EventRefresh, EventReset, EventSet, EventTransition}},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s: the schema has %v, the engine %v", name, pair[0], pair[1])
		}
	}

	// A definition the engine takes is one the schema takes, and the other
	// way round, for the definitions of the tests and of every suite.
	defs := map[string]string{"books.post": booksPost, "audit.ping": auditPing,
		"every type": `{"name": "a.b_c.d9", "version": 2, "label": "l", "description": "d",
 "params": {"a": "bool", "b": "number", "c": "int", "d": "text", "e": "date", "f": "datetime", "g": "ref", "h": "json", "i": "decimal", "j": "decimal(0)", "k": "money(18)", "l": {"type": "money", "required": true, "description": "x"}},
 "returns": {"r": {"type": "decimal(4)", "description": "y"}}}`}
	suites, _ := filepath.Glob(filepath.Join("..", "..", "conformance", "rules", "*.json"))
	for _, file := range suites {
		raw, _ := os.ReadFile(file)
		var suite statechartSuite
		if err := json.Unmarshal(raw, &suite); err != nil {
			t.Fatal(err)
		}
		for i, def := range suite.Actions {
			defs[fmt.Sprintf("%s, action %d", suite.Suite, i)] = string(def)
		}
	}
	if len(defs) < 6 {
		t.Fatalf("the suites define %d actions: the test found too few to mean anything", len(defs))
	}
	for name, def := range defs {
		if _, diags := model.DefineAction([]byte(def)); len(diags) > 0 {
			t.Errorf("%s: the engine refuses it: %v", name, diags)
		}
		if findings := actionSchema.validate(t, []byte(def)); len(findings) > 0 {
			t.Errorf("%s: the schema refuses it: %v", name, findings)
		}
	}
	for name, def := range map[string]string{
		"no name":                          `{"params": {"a": "text"}}`,
		"a name without a namespace":       `{"name": "post"}`,
		"a name that is no identifier":     `{"name": "books.2post"}`,
		"a name too long":                  `{"name": "a.` + strings.Repeat("x", 100) + `"}`,
		"version zero":                     `{"name": "a.b", "version": -1}`,
		"an unknown property":              `{"name": "a.b", "parameters": {}}`,
		"an unknown type":                  `{"name": "a.b", "params": {"x": "string"}}`,
		"a composite type":                 `{"name": "a.b", "params": {"x": "array<text>"}}`,
		"a scale too large":                `{"name": "a.b", "params": {"x": "decimal(19)"}}`,
		"a parameter without a type":       `{"name": "a.b", "params": {"x": {"required": true}}}`,
		"an unknown property of a param":   `{"name": "a.b", "params": {"x": {"type": "text", "default": "1"}}}`,
		"a required returned value":        `{"name": "a.b", "returns": {"x": {"type": "text", "required": true}}}`,
		"a request, which is not sent yet": `{"name": "a.b", "request": {"method": "POST", "url": "\"https://example.test\""}}`,
	} {
		if a, _ := model.DefineAction([]byte(def)); a != nil {
			t.Errorf("%s: the engine takes it", name)
		}
		if findings := actionSchema.validate(t, []byte(def)); len(findings) == 0 {
			t.Errorf("%s: the schema takes it", name)
		}
	}

	// Every call the worker makes fits the schema of the call, with the
	// event of every kind a rule can be fired by.
	rec := &recorder{}
	s, _ := openStore(t, Options{Actions: map[string]ActionFunc{"audit.note": rec.fn}})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(`{"name": "audit.note", "params": {"what": "text", "at": "datetime", "n": "number", "ok": "bool", "sum": "money(2)", "raw": "json", "day": "date"}}`)); err != nil {
		t.Fatal(err)
	}
	note := func(what string) string {
		return `{"action": "audit.note", "args": {"what": "\"` + what + `\"", "at": "NOW()", "n": "qty", "ok": "qty > 1", "sum": "sum", "raw": {"lines": "qty", "tags": ["memo"]}, "day": "TODAY()"}}`
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Case", "title": "\"Case \" & memo",
 "variables": {"memo": {"type": "text", "default": "\"new\""}, "qty": {"type": "number"}, "sum": {"type": "money", "scale": 2},
   "lines": {"type": "rows", "schema": {"sku": "text", "qty": "number"}}, "tags": {"type": "array<text>"}, "who": {"type": "object", "schema": "Person"},
   "stamp": {"formula": "NOW()", "mode": "triggered", "on": ["refresh"]}, "broken": {"formula": "1 / qty", "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "open", "list": ["open", "busy", "done"]},
 "transitions": [{"name": "start", "from": "open", "to": "busy"}, {"from": "busy", "to": "done", "guard": "qty > 5"}],
 "rules": [
   {"on": "create", "do": [`+note("create")+`]},
   {"on": "values_change", "variables": ["memo", "stamp"], "do": [`+note("change")+`]},
   {"on": "transition", "name": "start", "do": [`+note("transition")+`]},
   {"on": "enter", "state": "done", "do": [`+note("enter")+`]},
   {"on": "delete", "do": [`+note("delete")+`]}
 ]}`)); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Case", Values{"qty": 0, "sum": "12.5 EUR", "lines": []any{Values{"sku": "A", "qty": 1}}, "tags": []any{"x"}, "who": Values{"id": "ann", "name": "Ann"}}, As(alice), Key("k-create"))
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { _, err := s.Set(ctx, "Case", doc.ID, Values{"memo": "changed"}); return err },
		func() error { _, err := s.Reset(ctx, "Case", doc.ID, []string{"memo"}, As(alice)); return err },
		func() error { _, err := s.Refresh(ctx, "Case", doc.ID, nil); return err },
		func() error {
			_, err := s.Transition(ctx, "Case", doc.ID, "start", Values{"qty": 9}, Key("k-start"))
			return err
		},
		func() error { return s.Delete(ctx, "Case", doc.ID, As(alice)) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	calls := rec.take()
	var kinds []string
	for _, call := range calls {
		raw, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		if findings := callSchema.validate(t, raw); len(findings) > 0 {
			t.Errorf("the call of %v does not fit the schema: %v\n%s", call.Args["what"], findings, raw)
		}
		kinds = append(kinds, fmt.Sprintf("%v:%s", call.Args["what"], call.Event.Kind))
	}
	if got := strings.Join(kinds, " "); got != "create:created change:set change:reset change:refresh transition:transition enter:transition delete:deleted" {
		t.Errorf("the calls and the kinds of their events: %s", got)
	}
	// A document that has a moment and a failed recomputation says so in
	// the event, and the schema has both.
	if last := calls[len(calls)-1].Event.Document; last.Computed["stamp"].Version == 0 || len(last.Computed) != 2 {
		t.Errorf("the moments in the delivered document: %s", dump(last.Computed))
	}
	// The call of a trial has no event and fits too; a call with a
	// property the schema lacks does not.
	if _, err := s.TryAction(ctx, "audit.note", Values{"what": "trial"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rec.take()[0])
	if findings := callSchema.validate(t, raw); len(findings) > 0 {
		t.Errorf("the call of a trial: %v", findings)
	}
	for name, call := range map[string]string{
		"a call without a key":          `{"attempt": 1, "action": "a.b", "args": {}}`,
		"attempt zero":                  `{"key": "k", "attempt": 0, "action": "a.b", "args": {}}`,
		"an unknown property":           `{"key": "k", "attempt": 1, "action": "a.b", "args": {}, "secret": "x"}`,
		"an event of another kind":      strings.Replace(string(mustJSON(calls[0])), `"kind":"created"`, `"kind":"exploded"`, 1),
		"an event without its document": strings.Replace(string(mustJSON(calls[0])), `"document":`, `"doc":`, 1),
	} {
		if findings := callSchema.validate(t, []byte(call)); len(findings) == 0 {
			t.Errorf("%s: the schema takes it", name)
		}
	}
}

// kindsOf are the event kinds the schema of the event lists, sorted.
func kindsOf(s schema) []string {
	var out []string
	for _, k := range s["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any) {
		out = append(out, k.(string))
	}
	sort.Strings(out)
	return out
}
