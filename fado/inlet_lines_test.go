package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// The chapter on a way in for messages says a list in a message becomes
// the lines of a document (ADR-0085, item 1). Its class, its inlet and
// its message are played here, with the table of the lines they make
// and the table of what a message is refused for.
func TestWayIn_AListInAMessage(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "a-way-in-for-messages.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := fado.Open(filepath.Join(t.TempDir(), "lines.fado"), fado.Options{Users: true, Inbound: true,
		Secrets: func(_ context.Context, connection, name string) (string, error) {
			return "the key of commercetools", nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	machine := fado.As(fado.Principal{System: []string{fado.GroupAdministrators}})
	for _, u := range []fado.User{
		{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators, fado.GroupDesigners}, Groups: []string{"orders"}},
		{ID: "ct-in", Kind: fado.ActorService, Groups: []string{"commerce"}},
	} {
		if _, err := db.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	olga := fado.As(fado.Principal{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators, fado.GroupDesigners}, Groups: []string{"orders"}})
	if _, err := db.DefineClass(ctx, []byte(marked(t, page, "a list: the class")), olga); err != nil {
		t.Fatal(err)
	}
	inlet := fenced(t, page, "a list: the inlet")
	if _, err := db.DefineInlet(ctx, []byte(inlet), olga); err != nil {
		t.Fatal(err)
	}
	// The parts of a repeat the chapter lists are the parts the inlet
	// has, and no other is taken.
	for _, row := range rows(marked(t, page, "a list: the parts of a repeat")) {
		if !strings.Contains(inlet, `"`+row[0]+`": `) {
			t.Errorf("the chapter lists the part %s, and its inlet has none", row[0])
		}
	}
	from := func(body string) *fado.Message {
		return &fado.Message{Headers: map[string]string{"x-api-key": "the key of commercetools", "Content-Type": "application/json"}, Body: []byte(body)}
	}
	message := fenced(t, page, "a list: the message")
	receipt, err := db.Receive(ctx, "ct.order", from(message))
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	if text, err := json.Marshal(receipt.Document.Values["lines"]); err != nil || json.Unmarshal(text, &lines) != nil {
		t.Fatalf("the lines of the order do not read: %v", receipt.Document.Values["lines"])
	}
	said := rows(marked(t, page, "a list: the lines"))
	if receipt.Document.ID != "ct-1001" || len(lines) != len(said) {
		t.Fatalf("the message makes %s with %d lines (%v); the chapter says ct-1001 with %d", receipt.Document.ID, len(lines), receipt.Document.Values["lines"], len(said))
	}
	for i, row := range said {
		if lines[i]["sku"] != row[0] || lines[i]["qty"] != row[1] || lines[i]["price"] != row[2] {
			t.Errorf("line %d is %v; the chapter says %v", i+1, lines[i], row)
		}
	}
	// What a message is refused for, each at the place the chapter says.
	refusals := map[string]string{
		"has something at the path that is no list":                               strings.Replace(message, `"lineItems": [`, `"lineItems": "none", "x": [`, 1),
		"has an element a formula fails on, such as a quantity that is no number": strings.Replace(message, `"quantity": 4`, `"quantity": "four"`, 1),
		"has more than 10,000 elements":                                           strings.Replace(message, `{"variant": {"sku": "SOFA-3"},  "quantity": 1, "price": {"value": {"centAmount": 240000, "currencyCode": "PLN"}}},`, strings.Repeat(`{"variant": {"sku": "SOFA-3"},  "quantity": 1, "price": {"value": {"centAmount": 240000, "currencyCode": "PLN"}}},`, 10000), 1),
	}
	table := rows(marked(t, page, "a list: what is refused"))
	if len(table) != len(refusals) {
		t.Fatalf("the chapter refuses %d messages and the test plays %d", len(table), len(refusals))
	}
	for i, row := range table {
		body, has := refusals[row[0]]
		if !has {
			t.Errorf("the chapter refuses a message that %s, and the test has none", row[0])
			continue
		}
		body = strings.Replace(strings.Replace(body, "evt-7781", "evt-"+string(rune('a'+i)), 1), "ct-1001", "ct-200"+string(rune('1'+i)), 1)
		_, err := db.Receive(ctx, "ct.order", from(body))
		var refused *fado.Error
		if !errors.As(err, &refused) || !strings.HasPrefix(row[1], refused.Code) || !strings.HasSuffix(row[2], refused.Path) {
			t.Errorf("a message that %s: %v; the chapter says %s at %s", row[0], err, row[1], row[2])
			continue
		}
		if i == 1 && !strings.Contains(refused.Message, "the element 2 of order.lineItems") {
			t.Errorf("the refusal does not name the element: %s", refused.Message)
		}
	}
	if n, err := db.Count(ctx, fado.Filter{Class: "Order"}, olga); err != nil || n != 1 {
		t.Errorf("the database holds %d orders, %v; the refused messages make nothing", n, err)
	}
}
