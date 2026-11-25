package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A list goes out in a request (ADR-0085, item 2). The case of the
// record: when Olga books an order, its lines go to Business Central as
// the lines of a sales order, one element for each.
const bcSalesOrder = `{
  "name": "bc.sales_order",
  "label": "Create a sales order in Business Central",
  "params": {
    "customer": {"type": "text", "required": true},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}, "required": true}
  },
  "returns": {"bc_id": "text"},
  "connection": {"name": "bc", "secrets": ["token"]},
  "request": {
    "method": "POST",
    "url": "\"https://bc.example/api/v2.0/salesOrders\"",
    "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"},
    "body": {
      "customerNumber": "customer",
      "salesOrderLines": {
        "each": "lines",
        "as": "line",
        "item": {
          "lineType": "\"Item\"",
          "lineObjectNumber": "line.sku",
          "quantity": "VALUE(TEXT(line.qty))",
          "unitPrice": "VALUE(TEXT(AMOUNT(line.price)))",
          "for": "customer"
        }
      }
    }
  },
  "ok": "STATUS = 201",
  "result": {"bc_id": "JSONVALUE(BODY, \"id\")"}
}`

const bcOrder = `{
  "name": "Order",
  "title": "number",
  "variables": {
    "number":   {"type": "text", "required": true},
    "lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)", "note": "text"}},
    "other":    {"type": "rows", "schema": {"sku": "text", "qty": "int"}},
    "wrong":    {"type": "rows", "schema": {"sku": "text", "qty": "number", "price": "money(2)"}},
    "bc_id":    {"type": "text"},
    "bc_error": {"type": "text"}
  },
  "states": {"initial": "received", "list": ["received", "booking"]},
  "transitions": [{"name": "book", "from": "received", "to": "booking"}],
  "rules": [
    {"on": "enter", "state": "booking",
     "do": [{"action": "bc.sales_order",
             "args": {"customer": "\"WEB-PL\"", "lines": "lines"},
             "into": {"bc_id": "bc_id", "bc_error": "ERROR"}}]}
  ]
}`

func bcDatabase(t *testing.T) (*Store, *fakeOutbound, context.Context) {
	t.Helper()
	net := &fakeOutbound{connections: map[string][]string{"bc": {"bc.example"}}}
	s, _ := openStore(t, Options{Outbound: net, Secrets: fixedSecrets(map[string]string{"bc/token": "bc-s3cr3t"})})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(bcSalesOrder), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(bcOrder), asSetup); err != nil {
		t.Fatal(err)
	}
	return s, net, ctx
}

// The order of the record goes out with its two lines, as the commit
// that booked it held them.
func TestRequestLines_TheCaseOfTheRecord(t *testing.T) {
	s, net, ctx := bcDatabase(t)
	order, err := s.Create(ctx, "Order", Values{"number": "1001", "lines": []any{
		map[string]any{"sku": "SOFA-3", "qty": 1, "price": "2400.00 PLN", "note": "grey"},
		map[string]any{"sku": "CHAIR-1", "qty": 4, "price": "300.00 PLN"},
	}}, WithID("ct-1001"))
	if err != nil {
		t.Fatal(err)
	}
	net.script(&fakeReply{Status: 201, Body: json.RawMessage(`{"id": "SO-77"}`)})
	if _, err := s.Transition(ctx, "Order", order.ID, "book", nil); err != nil {
		t.Fatal(err)
	}
	// A line changed after the commit changes nothing of what is sent.
	if _, err := s.Set(ctx, "Order", order.ID, Values{"lines": []any{map[string]any{"sku": "LAMP-1", "qty": 9, "price": "1.00 PLN"}}}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	sent := net.take()
	if len(sent) != 1 {
		t.Fatalf("%d requests were sent", len(sent))
	}
	var body any
	if err := json.Unmarshal(sent[0].Body, &body); err != nil {
		t.Fatalf("the body is no JSON: %s", sent[0].Body)
	}
	want := `{"customerNumber":"WEB-PL","salesOrderLines":[` +
		`{"for":"WEB-PL","lineObjectNumber":"SOFA-3","lineType":"Item","quantity":1,"unitPrice":2400},` +
		`{"for":"WEB-PL","lineObjectNumber":"CHAIR-1","lineType":"Item","quantity":4,"unitPrice":300}]}`
	if got := flat(body); got != want {
		t.Errorf("the body sent is\n%s\nwant\n%s", got, want)
	}
	// The answer came back into the order.
	if d, err := s.Get(ctx, "Order", order.ID); err != nil || d.Values["bc_id"] != "SO-77" {
		t.Errorf("the order after the answer: %v, %v", d, err)
	}
	// An order with no lines sends an empty list.
	empty, err := s.Create(ctx, "Order", Values{"number": "1002"}, WithID("ct-1002"))
	if err != nil {
		t.Fatal(err)
	}
	net.script(&fakeReply{Status: 201, Body: json.RawMessage(`{"id": "SO-78"}`)})
	if _, err := s.Transition(ctx, "Order", empty.ID, "book", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if sent := net.take(); len(sent) != 1 || !strings.Contains(string(sent[0].Body), `"salesOrderLines":[]`) {
		t.Errorf("an order with no lines sends %s", dump(sent))
	}
}

// PreviewAction shows the whole body, every line, with the secrets
// masked, and sends nothing.
func TestRequestLines_ThePreviewShowsEveryLine(t *testing.T) {
	s, net, ctx := bcDatabase(t)
	preview, err := s.PreviewAction(ctx, "bc.sales_order", Values{"customer": "WEB-PL", "lines": []any{
		map[string]any{"sku": "SOFA-3", "qty": 1, "price": "2400.00 PLN"},
		map[string]any{"sku": "CHAIR-1", "qty": 4, "price": "300.00 PLN"},
	}}, asSetup)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Request == nil {
		t.Fatalf("the preview has no request: %s", flat(preview))
	}
	var body any
	if err := json.Unmarshal([]byte(preview.Request.Body), &body); err != nil {
		t.Fatalf("the body of the preview is no JSON: %s", preview.Request.Body)
	}
	text := flat(body)
	for _, part := range []string{`"lineObjectNumber":"SOFA-3"`, `"lineObjectNumber":"CHAIR-1"`, `"quantity":4`, `"unitPrice":2400`} {
		if !strings.Contains(text, part) {
			t.Errorf("the preview lacks %s:\n%s", part, text)
		}
	}
	if strings.Contains(flat(preview), "bc-s3cr3t") {
		t.Errorf("the preview shows the secret:\n%s", flat(preview))
	}
	if sent := net.take(); len(sent) != 0 {
		t.Errorf("a preview sent %d requests", len(sent))
	}
	// What is given for the lines is typed column by column.
	for _, tc := range []struct {
		name  string
		lines any
		code  string
	}{
		{"a quantity that is no whole number", []any{map[string]any{"sku": "A", "qty": "two"}}, CodeBadValue},
		{"lines that are no list", "SOFA-3", CodeBadValue},
		{"a line that is no object", []any{"SOFA-3"}, CodeBadValue},
		{"a line with a column the parameter has not", []any{map[string]any{"sku": "A", "colour": "grey"}}, CodeBadValue},
	} {
		if _, err := s.PreviewAction(ctx, "bc.sales_order", Values{"customer": "WEB-PL", "lines": tc.lines}, asSetup); code(err) != tc.code {
			t.Errorf("%s: %v; want %s", tc.name, err, tc.code)
		}
	}
	if _, err := s.PreviewAction(ctx, "bc.sales_order", Values{"customer": "WEB-PL"}, asSetup); code(err) != CodeRequired {
		t.Errorf("no lines given for a required parameter: %v; want required", err)
	}
}

// What is checked when the action is defined, and when a class calls
// it.
func TestRequestLines_CheckedWhenDefined(t *testing.T) {
	s, _, ctx := bcDatabase(t)
	body := `"salesOrderLines": {
        "each": "lines",
        "as": "line",
        "item": {
          "lineType": "\"Item\"",
          "lineObjectNumber": "line.sku",
          "quantity": "VALUE(TEXT(line.qty))",
          "unitPrice": "VALUE(TEXT(AMOUNT(line.price)))",
          "for": "customer"
        }
      }`
	params := `"lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}, "required": true}`
	if !strings.Contains(bcSalesOrder, body) || !strings.Contains(bcSalesOrder, params) {
		t.Fatal("the action of the case is not written as the test reads it")
	}
	for _, tc := range []struct {
		name, old, with, code, path, says string
	}{
		{"a repeat over what is no parameter", body, `"salesOrderLines": {"each": "items", "as": "line", "item": "line.sku"}`, "unknown_parameter", "/request/body/salesOrderLines/each", "no rows parameter"},
		{"a repeat over a parameter that is no rows", body, `"salesOrderLines": {"each": "customer", "as": "line", "item": "customer"}`, "unknown_parameter", "/request/body/salesOrderLines/each", "no rows parameter"},
		{"a row named as a parameter is", body, `"salesOrderLines": {"each": "lines", "as": "customer", "item": "customer"}`, "not_allowed", "/request/body/salesOrderLines/as", "is a parameter of the action"},
		{"a column the parameter has not", body, `"salesOrderLines": {"each": "lines", "as": "line", "item": {"x": "line.colour"}}`, "bad_formula", "/request/body/salesOrderLines/item/x", ""},
		{"a row read outside its repeat", body, `"salesOrderLines": {"each": "lines", "as": "line", "item": "line.sku"}, "first": "line.sku"`, "bad_formula", "/request/body/first", ""},
		{"the list itself where a value goes", body, `"salesOrderLines": "lines"`, "bad_formula", "/request/body/salesOrderLines", ""},
		{"a rows parameter with no schema", params, `"lines":    {"type": "rows", "required": true}`, "missing", "/params/lines/schema", "says its columns"},
		{"a column that is no scalar", params, `"lines":    {"type": "rows", "schema": {"sku": "text", "more": "rows"}}`, "not_allowed", "/params/lines/schema/more", "is a scalar"},
		{"a schema on a parameter that is no rows", params, params + `, "note": {"type": "text", "schema": {"a": "text"}}`, "not_allowed", "/params/note/schema", "only a rows parameter has a schema"},
	} {
		def := strings.Replace(strings.Replace(bcSalesOrder, tc.old, tc.with, 1), `"bc.sales_order"`, `"bc.other"`, 1)
		_, err := s.DefineAction(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the action was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.path) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s at %s that says %q", tc.name, err, tc.code, tc.path, tc.says)
		}
	}
	// A repeat inside a repeat, each over a parameter of its own.
	nested := strings.Replace(strings.Replace(bcSalesOrder, body, `"groups": {"each": "lines", "as": "outer", "item": {"sku": "outer.sku", "again": {"each": "lines", "as": "inner", "item": "outer.sku & \"/\" & inner.sku"}}}`, 1), `"bc.sales_order"`, `"bc.nested"`, 1)
	if _, err := s.DefineAction(ctx, []byte(nested), asSetup); err != nil {
		t.Errorf("a repeat inside a repeat: %v", err)
	}
	// What a class gives a rows parameter: the name of a rows variable
	// with every column the parameter has, of the same type.
	for _, tc := range []struct {
		name, arg, says string
	}{
		{"a variable the class has not", `"lines": "items"`, "the class has no variable"},
		{"a variable that is no rows", `"lines": "number"`, "takes the lines of a rows variable the document holds"},
		{"rows that lack a column", `"lines": "other"`, "has the column price, and other has none of that name"},
		{"a column of another type", `"lines": "wrong"`, "the column qty of the parameter lines is an int, and in wrong it is a number"},
		{"a formula where the name of a variable goes", `"lines": "IF(TRUE, lines, lines)"`, "the class has no variable"},
		{"a template where the name of a variable goes", `"lines": [{"sku": "number"}]`, "the argument of a rows parameter is the name of a rows variable"},
	} {
		def := strings.Replace(strings.Replace(bcOrder, `"lines": "lines"`, tc.arg, 1), `"name": "Order"`, `"name": "Other"`, 1)
		_, err := s.defineOpen(ctx, []byte(def), asSetup)
		if err == nil || !strings.Contains(err.Error(), "/rules/0/do/0/args/lines") || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v; want it refused at the argument, saying %q", tc.name, err, tc.says)
		}
	}
}

// A rule sends the lines itself, with http.call and no definition: a
// repeat in the JSON it gives the json parameter goes over a rows
// variable of its class, and its formulas read the document and one
// line.
const factoryOrder = `{
  "name": "FactoryOrder",
  "title": "number",
  "variables": {
    "number":  {"type": "text", "required": true},
    "factory": {"type": "text"},
    "lines":   {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}},
    "others":  {"mode": "live", "formula": "QUERY(FactoryOrder, {number}, factory = THIS.factory)"}
  },
  "states": {"initial": "received", "list": ["received", "sent"]},
  "transitions": [{"name": "send", "from": "received", "to": "sent"}],
  "rules": [
    {"on": "enter", "state": "sent",
     "do": [{"action": "http.call",
             "args": {"url": "\"https://hooks.example/factory\"",
                      "body": {"order": "number",
                               "items": {"each": "lines", "as": "line",
                                         "item": {"sku": "line.sku", "pieces": "VALUE(TEXT(line.qty))", "for": "number & \"/\" & factory"}}}}}]}
  ]
}`

func TestRequestLines_ARuleSendsItsLinesItself(t *testing.T) {
	net := network()
	s, path := openStore(t, Options{Outbound: net})
	ctx := context.Background()
	def := strings.Replace(factoryOrder, `,
    "others":  {"mode": "live", "formula": "QUERY(FactoryOrder, {number}, factory = THIS.factory)"}`, "", 1)
	if def == factoryOrder {
		t.Fatal("the class of the case is not written as the test reads it")
	}
	if _, err := s.defineOpen(ctx, []byte(def), asSetup); err != nil {
		t.Fatal(err)
	}
	want := `{"items":[{"for":"F-1/Kalisz","pieces":4,"sku":"CHAIR-1"},{"for":"F-1/Kalisz","pieces":2,"sku":"TABLE-2"}],"order":"F-1"}`
	send := func(s *Store, id string) string {
		t.Helper()
		order, err := s.Create(ctx, "FactoryOrder", Values{"number": id, "factory": "Kalisz", "lines": []any{
			map[string]any{"sku": "CHAIR-1", "qty": 4, "price": "300.00 PLN"},
			map[string]any{"sku": "TABLE-2", "qty": 2, "price": "900.00 PLN"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		net.script(&fakeReply{Status: 200})
		if _, err := s.Transition(ctx, "FactoryOrder", order.ID, "send", nil); err != nil {
			t.Fatal(err)
		}
		// A line changed after the commit changes nothing of what is sent.
		if _, err := s.Set(ctx, "FactoryOrder", order.ID, Values{"lines": []any{}}); err != nil {
			t.Fatal(err)
		}
		if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
			t.Fatalf("drain: %d, %v", n, err)
		}
		sent := net.take()
		if len(sent) != 1 {
			t.Fatalf("%d requests were sent", len(sent))
		}
		var body any
		if err := json.Unmarshal(sent[0].Body, &body); err != nil {
			t.Fatalf("the body is no JSON: %s", sent[0].Body)
		}
		return flat(body)
	}
	if got := send(s, "F-1"); got != want {
		t.Errorf("the body sent is\n%s\nwant\n%s", got, want)
	}
	// An order without lines sends an empty list.
	bare, err := s.Create(ctx, "FactoryOrder", Values{"number": "F-2"})
	if err != nil {
		t.Fatal(err)
	}
	net.script(&fakeReply{Status: 200})
	if _, err := s.Transition(ctx, "FactoryOrder", bare.ID, "send", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if sent := net.take(); len(sent) != 1 || !strings.Contains(string(sent[0].Body), `"items":[]`) {
		t.Errorf("an order with no lines sends %s", dump(sent))
	}
	// The database closed and opened again sends as before: the class it
	// kept is laid out with its repeat.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbound: net, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := send(again, "F-1"); got != want {
		t.Errorf("after the database was opened again the body sent is\n%s\nwant\n%s", got, want)
	}

	// What is refused when the class is defined.
	body := `{"each": "lines", "as": "line",
                                         "item": {"sku": "line.sku", "pieces": "VALUE(TEXT(line.qty))", "for": "number & \"/\" & factory"}}`
	if !strings.Contains(factoryOrder, body) {
		t.Fatal("the repeat of the case is not written as the test reads it")
	}
	for _, tc := range []struct {
		name, with, code, path, says string
	}{
		{"a repeat over what is no variable", `{"each": "items", "as": "line", "item": "line.sku"}`, "unknown_variable", "/args/body/items/each", "no rows variable"},
		{"a repeat over a text", `{"each": "number", "as": "line", "item": "number"}`, "unknown_variable", "/args/body/items/each", "no rows variable"},
		{"a repeat over a list the document does not hold", `{"each": "others", "as": "line", "item": "line.number"}`, "unknown_variable", "/args/body/items/each", "no rows variable"},
		{"a line named as a variable is", `{"each": "lines", "as": "factory", "item": "number"}`, "not_allowed", "/args/body/items/as", "is a variable of the class"},
		{"a column the list has not", `{"each": "lines", "as": "line", "item": "line.colour"}`, "bad_formula", "/args/body/items/item", ""},
		{"a repeat inside a repeat", `{"each": "lines", "as": "line", "item": {"again": {"each": "lines", "as": "inner", "item": "inner.sku"}}}`, "not_allowed", "/args/body/items/item/again", "does not stand in another"},
	} {
		def := strings.Replace(strings.Replace(factoryOrder, body, tc.with, 1), `"name": "FactoryOrder"`, `"name": "Other"`, 1)
		def = strings.Replace(def, `QUERY(FactoryOrder,`, `QUERY(Other,`, 1)
		_, err := again.defineOpen(ctx, []byte(def), asSetup)
		if err == nil {
			t.Errorf("%s: the class was taken", tc.name)
			continue
		}
		if text := err.Error(); !strings.Contains(text, tc.code) || !strings.Contains(text, tc.path) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s at %s that says %q", tc.name, err, tc.code, tc.path, tc.says)
		}
	}
}

// A database opened again still sends the lines a rows parameter is
// given.
func TestRequestLines_AfterTheDatabaseIsOpenedAgain(t *testing.T) {
	net := &fakeOutbound{connections: map[string][]string{"bc": {"bc.example"}}}
	secrets := fixedSecrets(map[string]string{"bc/token": "bc-s3cr3t"})
	s, path := openStore(t, Options{Outbound: net, Secrets: secrets})
	ctx := context.Background()
	if _, err := s.DefineAction(ctx, []byte(bcSalesOrder), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(bcOrder), asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbound: net, Secrets: secrets, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	order, err := again.Create(ctx, "Order", Values{"number": "1001", "lines": []any{
		map[string]any{"sku": "SOFA-3", "qty": 1, "price": "2400.00 PLN"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	net.script(&fakeReply{Status: 201, Body: json.RawMessage(`{"id": "SO-77"}`)})
	if _, err := again.Transition(ctx, "Order", order.ID, "book", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := again.Drain(ctx, asSetup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	if sent := net.take(); len(sent) != 1 || !strings.Contains(string(sent[0].Body), `"lineObjectNumber":"SOFA-3"`) {
		t.Errorf("after the database was opened again: %s", dump(sent))
	}

	// An action may not change so that a rule of a class no longer fits:
	// a column more than the lines of the order have is refused.
	wider := strings.Replace(bcSalesOrder, `"price": "money(2)"}`, `"price": "money(2)", "colour": "text"}`, 1)
	wider = strings.Replace(wider, `"name": "bc.sales_order",`, `"name": "bc.sales_order", "version": 2,`, 1)
	if _, err := again.DefineAction(ctx, []byte(wider), asSetup); err == nil || !strings.Contains(err.Error(), "it would break a rule of the class Order") || !strings.Contains(err.Error(), "has the column colour, and lines has none of that name") {
		t.Errorf("an action that takes a column the lines lack: %v; want it refused for the rule it breaks", err)
	}
	// The class drops its rule, and then the action makes a text of the
	// parameter. The first version of the class is kept as it was
	// written, giving the lines: the database opens all the same.
	bare := strings.Replace(bcOrder, `"name": "Order",`, `"name": "Order", "version": 2,`, 1)
	rule := bare[strings.Index(bare, `"rules": [`) : strings.LastIndex(bare, `]`)+1]
	if _, err := again.defineOpen(ctx, []byte(strings.Replace(bare, rule, `"rules": []`, 1)), asSetup); err != nil {
		t.Fatalf("the class without its rule: %v", err)
	}
	text := strings.Replace(bcSalesOrder, `"lines":    {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}, "required": true}`, `"lines":    {"type": "text"}`, 1)
	text = strings.Replace(text, text[strings.Index(text, `"salesOrderLines": {`):strings.Index(text, `"ok":`)], `"salesOrderLines": "lines"}}, `, 1)
	text = strings.Replace(text, `"name": "bc.sales_order",`, `"name": "bc.sales_order", "version": 2,`, 1)
	if _, err := again.DefineAction(ctx, []byte(text), asSetup); err != nil {
		t.Fatalf("the action with a text where the lines were: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbound: net, Secrets: secrets, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("the database does not open with the first version of the class kept: %v", err)
	}
	defer third.Close()
	if v1, err := third.Version(ctx, "Order", order.ID, 1); err != nil || v1.ClassVersion != 1 {
		t.Errorf("the order as the first version of its class made it: %v %s", err, dump(v1))
	}
}
