package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A list in an answer becomes the lines of a document (ADR-0085, item
// 3). The case of the record: the warehouse changed a line of the sales
// order in Business Central, and a rule pulls its lines back into the
// order.
const bcOrderLines = `{
  "name": "bc.sales_order_lines",
  "label": "Read the lines of a sales order in Business Central",
  "params": {"bc_id": {"type": "text", "required": true}},
  "returns": {"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}, "count": "number"},
  "connection": {"name": "bc", "secrets": ["token"]},
  "request": {
    "method": "GET",
    "url": "\"https://bc.example/api/v2.0/salesOrders(\" & bc_id & \")/salesOrderLines\"",
    "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"}
  },
  "result": {
    "lines": {
      "each": "value",
      "as": "l",
      "row": {
        "sku": "JSONVALUE(l, \"lineObjectNumber\")",
        "qty": "DECIMAL(VALUE(JSONVALUE(l, \"quantity\")), 0)"
      }
    },
    "count": "VALUE(JSONVALUE(BODY, \"count\"))"
  }
}`

const bcPulled = `{
  "name": "Order",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "bc_id":    {"type": "text"},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "note": "text"}},
    "narrow":   {"type": "rows", "schema": {"sku": "text"}},
    "wrong":    {"type": "rows", "schema": {"sku": "text", "qty": "number"}},
    "seen":     {"type": "number"},
    "bc_error": {"type": "text"}
  },
  "states": {"initial": "booked", "list": ["booked", "checking"]},
  "transitions": [{"name": "check", "from": "booked", "to": "checking"}, {"name": "back", "from": "checking", "to": "booked"}],
  "rules": [
    {"on": "enter", "state": "checking",
     "do": [{"action": "bc.sales_order_lines",
             "args": {"bc_id": "bc_id"},
             "into": {"lines": "lines", "seen": "count", "bc_error": "ERROR"}}]}
  ]
}`

func pulledDatabase(t *testing.T, o Options) (*Store, *fakeOutbound, string, context.Context) {
	t.Helper()
	net := &fakeOutbound{connections: map[string][]string{"bc": {"bc.example"}}}
	o.Outbound, o.Secrets = net, fixedSecrets(map[string]string{"bc/token": "bc-s3cr3t"})
	s, path := openStore(t, o)
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(bcOrderLines), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(bcPulled), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, net, path, ctx
}

// pull moves the order to checking with the reply scripted, lets the
// outbox run, and brings the order back so that it can be pulled again.
func pull(t *testing.T, s *Store, net *fakeOutbound, id string, reply *fakeReply) *Document {
	t.Helper()
	ctx := context.Background()
	net.script(reply)
	if _, err := s.Transition(ctx, "Order", id, "check", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	net.take()
	d, err := s.Transition(ctx, "Order", id, "back", nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// The order of the record takes the lines Business Central answers
// with, whole.
func TestReplyLines_TheCaseOfTheRecord(t *testing.T) {
	s, net, _, ctx := pulledDatabase(t, Options{})
	order, err := s.Create(ctx, "Order", Values{"number": "1001", "bc_id": "SO-77", "lines": []any{
		map[string]any{"sku": "SOFA-3", "qty": 1, "note": "grey"},
		map[string]any{"sku": "CHAIR-1", "qty": 4},
	}}, WithID("ct-1001"))
	if err != nil {
		t.Fatal(err)
	}
	// The warehouse had three chairs, and added a lamp.
	d := pull(t, s, net, order.ID, &fakeReply{Status: 200, Body: json.RawMessage(`{"count": 3, "value": [
  {"lineObjectNumber": "SOFA-3", "quantity": 1},
  {"lineObjectNumber": "CHAIR-1", "quantity": 3},
  {"lineObjectNumber": "LAMP-1", "quantity": 2}]}`)})
	want := `[{"note":null,"qty":"1","sku":"SOFA-3"},{"note":null,"qty":"3","sku":"CHAIR-1"},{"note":null,"qty":"2","sku":"LAMP-1"}]`
	if got := flat(d.Values["lines"]); got != want || flat(d.Values["seen"]) != `3` || d.Values["bc_error"] != nil {
		t.Errorf("the order after the pull: lines %s, seen %s, error %v; want %s", got, flat(d.Values["seen"]), d.Values["bc_error"], want)
	}
	// An answer without the list, and one with an empty list, leave the
	// order without lines.
	for name, body := range map[string]string{"no list": `{"count": 0}`, "an empty list": `{"count": 0, "value": []}`, "a null": `{"count": 0, "value": null}`} {
		if _, err := s.Set(ctx, "Order", order.ID, Values{"lines": []any{map[string]any{"sku": "SOFA-3", "qty": 1}}}); err != nil {
			t.Fatal(err)
		}
		d := pull(t, s, net, order.ID, &fakeReply{Status: 200, Body: json.RawMessage(body)})
		if got, _ := d.Values["lines"].([]any); len(got) != 0 {
			t.Errorf("%s: the order keeps %s", name, flat(d.Values["lines"]))
		}
	}
}

// A pull that did not come through leaves the lines of the order as
// they are, and says why.
func TestReplyLines_APullThatFails(t *testing.T) {
	s, net, _, ctx := pulledDatabase(t, Options{})
	order, err := s.Create(ctx, "Order", Values{"number": "1001", "bc_id": "SO-77", "lines": []any{
		map[string]any{"sku": "SOFA-3", "qty": 1},
	}}, WithID("ct-1001"))
	if err != nil {
		t.Fatal(err)
	}
	kept := `[{"note":null,"qty":"1","sku":"SOFA-3"}]`
	var many []string
	for i := 0; i <= MaxElements; i++ {
		many = append(many, `{"lineObjectNumber": "A", "quantity": 1}`)
	}
	for _, tc := range []struct {
		name  string
		reply *fakeReply
		code  string
		says  string
	}{
		{"the receiver refuses", &fakeReply{Status: 404, Text: "no such order"}, CodeRefused, ""},
		{"the path leads to something that is no list", &fakeReply{Status: 200, Body: json.RawMessage(`{"value": {"lineObjectNumber": "SOFA-3"}}`)}, CodeBadResult, "value leads to something that is no list"},
		{"a formula fails on one element", &fakeReply{Status: 200, Body: json.RawMessage(`{"value": [{"lineObjectNumber": "SOFA-3", "quantity": 1}, {"lineObjectNumber": "CHAIR-1", "quantity": "many"}]}`)}, CodeBadResult, "the element 2 of value"},
		{"too many rows", &fakeReply{Status: 200, Body: json.RawMessage(`{"value": [` + strings.Join(many, ",") + `]}`)}, CodeBadResult, "a list holds at most"},
	} {
		d := pull(t, s, net, order.ID, tc.reply)
		if got := flat(d.Values["lines"]); got != kept {
			t.Errorf("%s: the lines of the order are %s; want them kept, %s", tc.name, got, kept)
		}
		if d.Values["bc_error"] != tc.code {
			t.Errorf("%s: the order says %v; want %s", tc.name, d.Values["bc_error"], tc.code)
		}
		rows := mustRows(t, s, order.ID)
		if last := rows[len(rows)-1]; tc.says != "" && !strings.Contains(last.IntoMessage+last.ErrorMessage, tc.says) {
			t.Errorf("%s: the action says %q and %q; want %q", tc.name, last.ErrorMessage, last.IntoMessage, tc.says)
		}
	}
}

// A trial shows the rows the action returns, and a function of the host
// program returns them as a list of objects.
func TestReplyLines_ATrialAndAFunction(t *testing.T) {
	s, net, _, ctx := pulledDatabase(t, Options{Actions: map[string]ActionFunc{
		"stock.lines": func(_ context.Context, call *ActionCall) (Values, error) {
			if call.Args["what"] == "wrong" {
				return Values{"lines": []any{map[string]any{"sku": "SOFA-3", "qty": "many"}}}, nil
			}
			return Values{"lines": []any{map[string]any{"sku": "SOFA-3", "qty": 7}}}, nil
		},
	}})
	net.script(&fakeReply{Status: 200, Body: json.RawMessage(`{"count": 1, "value": [{"lineObjectNumber": "SOFA-3", "quantity": 1}]}`)})
	out, err := s.TryAction(ctx, "bc.sales_order_lines", Values{"bc_id": "SO-77"}, asSetup)
	if err != nil || !out.OK || flat(out.Result["lines"]) != `[{"qty":"1","sku":"SOFA-3"}]` {
		t.Errorf("the trial: %s, %v", flat(out), err)
	}
	if _, err := s.DefineAction(ctx, []byte(`{"name": "stock.lines", "params": {"what": "text"}, "returns": {"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	out, err = s.TryAction(ctx, "stock.lines", Values{"what": "right"}, asSetup)
	if err != nil || !out.OK || flat(out.Result["lines"]) != `[{"qty":"7","sku":"SOFA-3"}]` {
		t.Errorf("the function: %s, %v", flat(out), err)
	}
	out, err = s.TryAction(ctx, "stock.lines", Values{"what": "wrong"}, asSetup)
	if err != nil || out.Code != CodeBadResult || !strings.Contains(out.Message, "lines[0].qty") {
		t.Errorf("a function that returns a row of the wrong type: %s, %v", flat(out), err)
	}
	// The lines a function returns go into the order as a reply's do.
	stocked := strings.Replace(strings.Replace(bcPulled, `"name": "Order"`, `"name": "Stocked"`, 1),
		`{"action": "bc.sales_order_lines",
             "args": {"bc_id": "bc_id"},
             "into": {"lines": "lines", "seen": "count", "bc_error": "ERROR"}}`,
		`{"action": "stock.lines", "args": {"what": "\"right\""}, "into": {"lines": "lines"}}`, 1)
	if !strings.Contains(stocked, "stock.lines") {
		t.Fatal("the class of the case is not written as the test reads it")
	}
	if _, err := s.defineOpen(ctx, []byte(stocked), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Stocked", Values{"number": "S-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Stocked", doc.ID, "check", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if d, err := s.Get(ctx, "Stocked", doc.ID); err != nil || flat(d.Values["lines"]) != `[{"note":null,"qty":"7","sku":"SOFA-3"}]` {
		t.Errorf("the lines the function returned: %v, %s", err, flat(d.Values["lines"]))
	}
}

// What is checked when the action is defined, and when a class writes
// its list into a variable.
func TestReplyLines_CheckedWhenDefined(t *testing.T) {
	s, _, _, ctx := pulledDatabase(t, Options{})
	repeat := `"lines": {
      "each": "value",
      "as": "l",
      "row": {
        "sku": "JSONVALUE(l, \"lineObjectNumber\")",
        "qty": "DECIMAL(VALUE(JSONVALUE(l, \"quantity\")), 0)"
      }
    }`
	returns := `"returns": {"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}, "count": "number"}`
	if !strings.Contains(bcOrderLines, repeat) || !strings.Contains(bcOrderLines, returns) {
		t.Fatal("the action of the case is not written as the test reads it")
	}
	for _, tc := range []struct {
		name, old, with, code, path, says string
	}{
		{"a formula where the repeat goes", repeat, `"lines": "JSONVALUE(BODY, \"value\")"`, "bad_value", "/result/lines", "takes a repeat over the reply"},
		{"a repeat for a number", `"count": "VALUE(JSONVALUE(BODY, \"count\"))"`, `"count": {"each": "value", "as": "l", "row": {"sku": "JSONVALUE(l, \"a\")"}}`, "not_allowed", "/result/count", "a repeat gives the rows of a returned list"},
		{"a column the list has not", repeat, `"lines": {"each": "value", "as": "l", "row": {"colour": "JSONVALUE(l, \"c\")"}}`, "unknown_variable", "/result/lines/row/colour", "has no column"},
		{"a column of another type", repeat, `"lines": {"each": "value", "as": "l", "row": {"qty": "JSONVALUE(l, \"quantity\")"}}`, "type_mismatch", "/result/lines/row/qty", ""},
		{"an element named as the reply is read", repeat, `"lines": {"each": "value", "as": "BODY", "row": {"sku": "JSONVALUE(BODY, \"a\")"}}`, "not_allowed", "/result/lines/as", ""},
		{"a path with an empty step", repeat, `"lines": {"each": "value..lines", "as": "l", "row": {"sku": "JSONVALUE(l, \"a\")"}}`, "bad_value", "/result/lines/each", "empty step"},
		{"a returned list with no schema", returns, `"returns": {"lines": {"type": "rows"}, "count": "number"}`, "missing", "/returns/lines/schema", "says its columns"},
		{"a returned list that is required", returns, `"returns": {"lines": {"type": "rows", "required": true, "schema": {"sku": "text", "qty": "int"}}, "count": "number"}`, "not_allowed", "/returns/lines/required", ""},
	} {
		def := strings.Replace(strings.Replace(bcOrderLines, tc.old, tc.with, 1), `"bc.sales_order_lines"`, `"bc.other"`, 1)
		_, err := s.DefineAction(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the action was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.path) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s at %s that says %q", tc.name, err, tc.code, tc.path, tc.says)
		}
	}
	// What a class writes a returned list into: a rows variable with
	// every column the list has, of the same type.
	for _, tc := range []struct {
		name, into, at, says string
	}{
		{"a variable that lacks a column", `"narrow": "lines"`, "/into/narrow", "has the column qty, and narrow has none of that name"},
		{"a column of another type", `"wrong": "lines"`, "/into/wrong", "the column qty of the returned list lines is an int, and in wrong it is a number"},
		{"a text", `"bc_error": "lines"`, "/into/bc_error", "goes into a rows variable"},
		{"a formula over the list", `"seen": "COUNT(lines)"`, "/into/seen", ""},
		{"a formula into a rows variable", `"lines": "count"`, "/into/lines", "takes a list the action returns, by its name"},
	} {
		def := strings.Replace(strings.Replace(bcPulled, `"into": {"lines": "lines", "seen": "count", "bc_error": "ERROR"}`, `"into": {`+tc.into+`}`, 1), `"name": "Order"`, `"name": "Other"`, 1)
		_, err := s.defineOpen(ctx, []byte(def), asSetup)
		if err == nil || !strings.Contains(err.Error(), "/rules/0/do/0"+tc.at) || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v; want it refused at %s, saying %q", tc.name, err, tc.at, tc.says)
		}
	}
}

// A database opened again pulls the lines as before; and it opens after
// the class changed, with the first version kept as it was written.
func TestReplyLines_AfterTheDatabaseIsOpenedAgain(t *testing.T) {
	s, net, path, ctx := pulledDatabase(t, Options{})
	order, err := s.Create(ctx, "Order", Values{"number": "1001", "bc_id": "SO-77"}, WithID("ct-1001"))
	if err != nil {
		t.Fatal(err)
	}
	reopen := func(s *Store) *Store {
		t.Helper()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbound: net, Secrets: fixedSecrets(map[string]string{"bc/token": "bc-s3cr3t"}), Outbox: OutboxOptions{Manual: true}})
		if err != nil {
			t.Fatalf("the database does not open: %v", err)
		}
		t.Cleanup(func() { next.Close() })
		return next
	}
	s = reopen(s)
	d := pull(t, s, net, order.ID, &fakeReply{Status: 200, Body: json.RawMessage(`{"count": 1, "value": [{"lineObjectNumber": "SOFA-3", "quantity": 1}]}`)})
	if got := flat(d.Values["lines"]); got != `[{"note":null,"qty":"1","sku":"SOFA-3"}]` {
		t.Errorf("the lines after the database was opened again: %s", got)
	}
	// An action may not change so that a rule of a class no longer fits:
	// a returned list with a column the lines of the order lack is
	// refused.
	wider := strings.Replace(bcOrderLines, `"schema": {"sku": "text", "qty": "int"}}`, `"schema": {"sku": "text", "qty": "int", "colour": "text"}}`, 1)
	wider = strings.Replace(wider, `"name": "bc.sales_order_lines",`, `"name": "bc.sales_order_lines", "version": 2,`, 1)
	if _, err := s.DefineAction(ctx, []byte(wider), asSetup); err == nil || !strings.Contains(err.Error(), "it would break a rule of the class Order") || !strings.Contains(err.Error(), "has the column colour, and lines has none of that name") {
		t.Errorf("an action that returns a column the lines lack: %v; want it refused for the rule it breaks", err)
	}
	// The second version of the class no longer pulls; then the action
	// returns no list any more. The first version of the class is kept
	// as it was written, with its into.
	second := strings.Replace(bcPulled, `"name": "Order",`, `"name": "Order", "version": 2,`, 1)
	second = strings.Replace(second, second[strings.Index(second, `"rules": [`):strings.LastIndex(second, `]`)+1], `"rules": []`, 1)
	if _, err := s.defineOpen(ctx, []byte(second), asSetup); err != nil {
		t.Fatalf("the class without its rule: %v", err)
	}
	plain := strings.Replace(bcOrderLines, `"returns": {"lines": {"type": "rows", "schema": {"sku": "text", "qty": "int"}}, "count": "number"}`, `"returns": {"count": "number"}`, 1)
	plain = strings.Replace(plain, plain[strings.Index(plain, `"lines": {
      "each"`):strings.Index(plain, `"count": "VALUE`)], ``, 1)
	plain = strings.Replace(plain, `"name": "bc.sales_order_lines",`, `"name": "bc.sales_order_lines", "version": 2,`, 1)
	if _, err := s.DefineAction(ctx, []byte(plain), asSetup); err != nil {
		t.Fatalf("the action that returns no list: %v", err)
	}
	s = reopen(s)
	if v, err := s.Version(ctx, "Order", order.ID, 1); err != nil || v.ClassVersion != 1 {
		t.Errorf("the order as the first version of its class made it: %v %s", err, dump(v))
	}
}
