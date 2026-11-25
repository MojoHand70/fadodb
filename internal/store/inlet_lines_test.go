package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A list in a message becomes the lines of a document (ADR-0085, item
// 1). The case of the record: commercetools says that Globex's order
// 1001 was placed, with two lines; `ct-in` is its service user, in the
// group commerce.
const ctOrder = `{
  "name": "Order",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "email":  {"type": "text"},
    "lines":  {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}},
    "tags":   {"type": "array<text>"},
    "total":  {"formula": "SUM(lines.qty)"}
  },
  "states": {"initial": "received", "list": ["received", "booking"]},
  "transitions": [{"name": "book", "from": "received", "to": "booking"}],
  "access": {
    "read":   "INGROUP(\"orders\") OR INGROUP(\"commerce\")",
    "create": "INGROUP(\"commerce\")",
    "update": "INGROUP(\"commerce\")",
    "delete": "FALSE",
    "transition": {"book": "INGROUP(\"orders\") OR INGROUP(\"commerce\")"}
  }
}`

const ctInlet = `{
  "name": "ct.order",
  "label": "commercetools says that an order was placed",
  "as": "ct-in",
  "connection": {"name": "ct", "secrets": ["api_key"]},
  "proof": {"form": "shown", "header": "x-api-key", "secret": "api_key"},
  "when": "JSONVALUE(BODY, \"type\") = \"OrderCreated\"",
  "key": "JSONVALUE(BODY, \"id\")",
  "command": "create",
  "object": {
    "class": "Order",
    "id": "JSONVALUE(BODY, \"order.id\")",
    "values": {
      "number": "JSONVALUE(BODY, \"order.orderNumber\")",
      "email":  "JSONVALUE(BODY, \"order.customerEmail\")",
      "lines": {
        "each": "order.lineItems",
        "as": "item",
        "row": {
          "sku":   "JSONVALUE(item, \"variant.sku\")",
          "qty":   "DECIMAL(VALUE(JSONVALUE(item, \"quantity\")), 0)",
          "price": "MONEY(ROUND(DECIMALVALUE(JSONVALUE(item, \"price.value.centAmount\"), 0) / 100, 2), JSONVALUE(BODY, \"order.currency\"))"
        }
      }
    }
  }
}`

const ctMessage = `{"id": "evt-1", "type": "OrderCreated", "order": {"id": "ct-1001", "orderNumber": "1001", "customerEmail": "buyer@globex.example", "currency": "PLN",
  "lineItems": [
    {"variant": {"sku": "SOFA-3"}, "quantity": 1, "price": {"value": {"centAmount": 240000}}},
    {"variant": {"sku": "CHAIR-1"}, "quantity": 4, "price": {"value": {"centAmount": 30000}}}
  ]}}`

func ctDatabase(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, _ := openStore(t, Options{Users: true, Inbound: true, Secrets: func(_ context.Context, connection, name string) (string, error) {
		return "the key of commercetools", nil
	}})
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupDesigners}, Groups: []string{"orders"}},
		{ID: "ct-in", Kind: ActorService, Groups: []string{"commerce"}},
	} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DefineClass(ctx, []byte(ctOrder), olgaBoth); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func fromCT(body string) *Message {
	return &Message{Headers: map[string]string{"x-api-key": "the key of commercetools", "Content-Type": "application/json"}, Body: []byte(body)}
}

// The message of the record makes the order with its two lines.
func TestInletLines_TheCaseOfTheRecord(t *testing.T) {
	s, ctx := ctDatabase(t)
	if _, err := s.DefineInlet(ctx, []byte(ctInlet), olgaBoth); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.Receive(ctx, "ct.order", fromCT(ctMessage))
	if err != nil {
		t.Fatal(err)
	}
	doc := receipt.Document
	want := `[{"price":"2400.00 PLN","qty":"1","sku":"SOFA-3"},{"price":"300.00 PLN","qty":"4","sku":"CHAIR-1"}]`
	if doc.ID != "ct-1001" || doc.CreatedBy != "ct-in" || doc.Values["number"] != "1001" || flat(doc.Values["lines"]) != want {
		t.Errorf("the order: %s, by %s, lines %s; want ct-1001 with %s", doc.ID, doc.CreatedBy, flat(doc.Values["lines"]), want)
	}
	// What reads lines somebody entered reads these.
	if got := flat(doc.Values["total"]); got != `"5"` && got != `5` {
		t.Errorf("the sum over the lines: %s; want 5", got)
	}
	// A trial shows the lines the message makes, row by row, and makes
	// nothing.
	trial, err := s.TryInlet(ctx, "ct.order", nil, fromCT(strings.Replace(ctMessage, "ct-1001", "ct-1999", 1)), olgaBoth)
	if err != nil || !trial.Acts || !strings.Contains(flat(trial.Object), `"lines":`+want) {
		t.Errorf("the trial of the message: %s, %v; want its lines %s", flat(trial), err, want)
	}
	// The same message again makes nothing more.
	if again, err := s.Receive(ctx, "ct.order", fromCT(ctMessage)); err != nil || again.Document.Version != 1 {
		t.Errorf("the same message again: %+v, %v", again, err)
	}
	// A message without the list, and one with an empty list, make a
	// document with no lines.
	for id, body := range map[string]string{
		"ct-1002": `{"id": "evt-2", "type": "OrderCreated", "order": {"id": "ct-1002", "orderNumber": "1002"}}`,
		"ct-1003": `{"id": "evt-3", "type": "OrderCreated", "order": {"id": "ct-1003", "orderNumber": "1003", "lineItems": []}}`,
		"ct-1004": `{"id": "evt-4", "type": "OrderCreated", "order": {"id": "ct-1004", "orderNumber": "1004", "lineItems": null}}`,
	} {
		receipt, err := s.Receive(ctx, "ct.order", fromCT(body))
		if err != nil || receipt.Document.ID != id || flat(receipt.Document.Values["lines"]) != `[]` && receipt.Document.Values["lines"] != nil {
			t.Errorf("%s: %+v, %v; want a document with no lines", id, receipt, err)
		}
	}
}

// What a message is refused for: a path that leads to something that is
// no list, a formula that fails on one element, and too many elements.
// The refusal names the place in the inlet, and nothing is made.
func TestInletLines_AMessageThatIsRefused(t *testing.T) {
	s, ctx := ctDatabase(t)
	if _, err := s.DefineInlet(ctx, []byte(ctInlet), olgaBoth); err != nil {
		t.Fatal(err)
	}
	var many []string
	for i := 0; i <= MaxElements; i++ {
		many = append(many, `{"variant": {"sku": "S"}, "quantity": 1, "price": {"value": {"centAmount": 100}}}`)
	}
	for _, tc := range []struct {
		name, body, path, says string
	}{
		{"the list is a text", `{"id": "e-1", "type": "OrderCreated", "order": {"id": "x-1", "orderNumber": "1", "currency": "PLN", "lineItems": "none"}}`,
			"/object/values/lines/each", "order.lineItems leads to something that is no list"},
		{"the list is an object", `{"id": "e-2", "type": "OrderCreated", "order": {"id": "x-2", "orderNumber": "2", "currency": "PLN", "lineItems": {"0": {}}}}`,
			"/object/values/lines/each", "order.lineItems leads to something that is no list"},
		{"a quantity that is no number, in the second element", `{"id": "e-3", "type": "OrderCreated", "order": {"id": "x-3", "orderNumber": "3", "currency": "PLN", "lineItems": [
			{"variant": {"sku": "A"}, "quantity": 1, "price": {"value": {"centAmount": 100}}}, {"variant": {"sku": "B"}, "quantity": "two", "price": {"value": {"centAmount": 100}}}]}}`,
			"/object/values/lines/row/qty", "the message does not give qty of the element 2 of order.lineItems"},
		{"more lines than a document holds", `{"id": "e-4", "type": "OrderCreated", "order": {"id": "x-4", "orderNumber": "4", "currency": "PLN", "lineItems": [` + strings.Join(many, ",") + `]}}`,
			"/object/values/lines/each", fmt.Sprintf("the message gives %d lines of lines at order.lineItems; a document holds at most %d", MaxElements+1, MaxElements)},
	} {
		_, err := s.Receive(ctx, "ct.order", fromCT(tc.body))
		e, ok := err.(*Error)
		if !ok || e.Code != CodeBadMessage || e.Path != tc.path || !strings.Contains(e.Message, tc.says) {
			t.Errorf("%s: %v; want bad_message at %s that says %q", tc.name, err, tc.path, tc.says)
		}
	}
	if n, err := s.Count(ctx, Filter{Class: "Order"}, olgaBoth); err != nil || n != 0 {
		t.Errorf("the refused messages made %d orders, %v", n, err)
	}
}

// A repeat is checked when the inlet is defined: its three parts, the
// name of the element, each column against the schema of the variable,
// and that it fills a rows variable somebody enters.
func TestInletLines_CheckedWhenDefined(t *testing.T) {
	s, ctx := ctDatabase(t)
	lines := `"lines": {
        "each": "order.lineItems",
        "as": "item",
        "row": {
          "sku":   "JSONVALUE(item, \"variant.sku\")",
          "qty":   "DECIMAL(VALUE(JSONVALUE(item, \"quantity\")), 0)",
          "price": "MONEY(ROUND(DECIMALVALUE(JSONVALUE(item, \"price.value.centAmount\"), 0) / 100, 2), JSONVALUE(BODY, \"order.currency\"))"
        }
      }`
	if !strings.Contains(ctInlet, lines) {
		t.Fatal("the inlet of the case is not written as the test reads it")
	}
	for _, tc := range []struct {
		name, with, code, path, says string
	}{
		{"no path", `"lines": {"as": "item", "row": {"sku": "JSONVALUE(item, \"sku\")"}}`, "missing", "/object/values/lines/each", "names the list it repeats over"},
		{"a path with an empty step", `"lines": {"each": "order..lineItems", "as": "item", "row": {"sku": "JSONVALUE(item, \"sku\")"}}`, "bad_value", "/object/values/lines/each", "has an empty step"},
		{"no name for the element", `"lines": {"each": "order.lineItems", "row": {"sku": "JSONVALUE(BODY, \"sku\")"}}`, "missing", "/object/values/lines/as", "names one element of its list"},
		{"the element named as the message is", `"lines": {"each": "order.lineItems", "as": "body", "row": {"sku": "JSONVALUE(body, \"sku\")"}}`, "not_allowed", "/object/values/lines/as", "BODY is what the message itself is read by"},
		{"no row", `"lines": {"each": "order.lineItems", "as": "item"}`, "missing", "/object/values/lines/row", "gives a formula for each column it fills"},
		{"a column the variable has not", `"lines": {"each": "order.lineItems", "as": "item", "row": {"sku": "JSONVALUE(item, \"sku\")", "colour": "JSONVALUE(item, \"colour\")"}}`,
			"unknown_variable", "/object/values/lines/row/colour", "lines has no column"},
		{"a formula of another type than its column", `"lines": {"each": "order.lineItems", "as": "item", "row": {"qty": "JSONVALUE(item, \"quantity\")"}}`, "type_mismatch", "/object/values/lines/row/qty", ""},
		{"a formula that names what is not there", `"lines": {"each": "order.lineItems", "as": "item", "row": {"sku": "JSONVALUE(line, \"sku\")"}}`, "bad_formula", "/object/values/lines/row/sku", "unknown variable line"},
		{"a part a repeat has not", `"lines": {"each": "order.lineItems", "as": "item", "row": {"sku": "JSONVALUE(item, \"sku\")"}, "limit": 5}`, "bad_json", "/object/values/lines", ""},
		{"a repeat for a variable that is no rows", `"email": {"each": "order.lineItems", "as": "item", "row": {"sku": "JSONVALUE(item, \"sku\")"}}`, "not_allowed", "/object/values/email", "a repeat gives the lines of a rows variable"},
		{"a repeat for a variable the class has not", `"things": {"each": "order.lineItems", "as": "item", "row": {"sku": "JSONVALUE(item, \"sku\")"}}`, "unknown_variable", "/object/values/things", "has no variable"},
		{"a value that is neither a formula nor a repeat", `"lines": ["a"]`, "bad_value", "/object/values", "every value of a create is a formula"},
	} {
		def := strings.Replace(strings.Replace(ctInlet, lines, tc.with, 1), `"ct.order"`, `"ct.other"`, 1)
		if tc.with[:7] != `"lines"` {
			def = strings.Replace(strings.Replace(ctInlet, lines, lines+",\n      "+tc.with, 1), `"ct.order"`, `"ct.other"`, 1)
			if strings.HasPrefix(tc.with, `"email"`) {
				def = strings.Replace(def, `"email":  "JSONVALUE(BODY, \"order.customerEmail\")",`, ``, 1)
			}
		}
		_, err := s.DefineInlet(ctx, []byte(def), olgaBoth)
		if err == nil {
			t.Errorf("%s: the inlet was taken", tc.name)
			continue
		}
		text := err.Error()
		if !strings.Contains(text, tc.code) || !strings.Contains(text, tc.path) || !strings.Contains(text, tc.says) {
			t.Errorf("%s: %v; want %s at %s that says %q", tc.name, err, tc.code, tc.path, tc.says)
		}
	}
	// A rule has no list to repeat over: what it gives is a formula.
	withRule := strings.Replace(ctOrder, `"access": {`, `"rules": [{"on": "enter", "state": "booking", "do": [{"action": "create", "class": "Order",
	  "values": {"number": "number", "lines": {"each": "x", "as": "item", "row": {"sku": "\"a\""}}}}]}],
  "access": {`, 1)
	if _, err := s.DefineClass(ctx, []byte(withRule), olgaBoth); err == nil || !strings.Contains(err.Error(), "every value of a create is a formula") {
		t.Errorf("a repeat in the values of a rule: %v; want it refused", err)
	}
}

// With set the repeat gives the lines whole, as a set of a rows variable
// always does; a repeat stands with transition too.
func TestInletLines_WithSetAndTransition(t *testing.T) {
	s, ctx := ctDatabase(t)
	if _, err := s.DefineInlet(ctx, []byte(ctInlet), olgaBoth); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(ctx, "ct.order", fromCT(ctMessage)); err != nil {
		t.Fatal(err)
	}
	changed := `{"name": "ct.changed", "as": "ct-in", "connection": {"name": "ct", "secrets": ["api_key"]},
	  "proof": {"form": "shown", "header": "x-api-key", "secret": "api_key"}, "key": "JSONVALUE(BODY, \"id\")", "command": "COMMAND",
	  "object": {"class": "Order", "id": "JSONVALUE(BODY, \"order\")"TRANSITION, "values": {
	    "lines": {"each": "lines", "as": "l", "row": {"sku": "JSONVALUE(l, \"sku\")", "qty": "DECIMAL(VALUE(JSONVALUE(l, \"n\")), 0)"}}}}}`
	for _, def := range []string{
		strings.NewReplacer("COMMAND", "set", "TRANSITION", "").Replace(changed),
		strings.NewReplacer("COMMAND", "transition", "TRANSITION", `, "transition": "book"`, "ct.changed", "ct.booked").Replace(changed),
	} {
		if _, err := s.DefineInlet(ctx, []byte(def), olgaBoth); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := s.Receive(ctx, "ct.changed", fromCT(`{"id": "c-1", "order": "ct-1001", "lines": [{"sku": "TABLE-2", "n": 2}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flat(receipt.Document.Values["lines"]), `[{"price":null,"qty":"2","sku":"TABLE-2"}]`; got != want && got != `[{"qty":"2","sku":"TABLE-2"}]` {
		t.Errorf("after a set the lines are %s; want the one line of the message, whole", got)
	}
	receipt, err = s.Receive(ctx, "ct.booked", fromCT(`{"id": "b-1", "order": "ct-1001", "lines": [{"sku": "LAMP-1", "n": 3}, {"sku": "RUG-9", "n": 1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc := receipt.Document; doc.State != "booking" || !strings.Contains(flat(doc.Values["lines"]), `"sku":"LAMP-1"`) || !strings.Contains(flat(doc.Values["lines"]), `"sku":"RUG-9"`) {
		t.Errorf("after the transition the order is in %s with the lines %s", doc.State, flat(doc.Values["lines"]))
	}
}
