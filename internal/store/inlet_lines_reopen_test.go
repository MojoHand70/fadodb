package store

import (
	"context"
	"strings"
	"testing"
)

// A database with an inlet that makes lines opens again, and a message
// makes them as before. It opens too after the class changed under the
// inlet: a kept inlet is taken as it was written, and what no longer
// fits refuses the message when one comes, and makes nothing.
func TestInletLines_AfterTheDatabaseIsOpenedAgain(t *testing.T) {
	ctx := context.Background()
	secrets := func(_ context.Context, connection, name string) (string, error) {
		return "the key of commercetools", nil
	}
	const lines = `"lines":  {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}},`
	for _, tc := range []struct {
		name, old, with string
		// first says a document is made before the class changes, so
		// that the class has a history and its tables are kept.
		first bool
		code  string
	}{
		{name: "the class as it was"},
		{name: "the class as it was, with a document", first: true},
		{name: "lines that are gone, with a document", old: lines, first: true, code: CodeUnknownVariable},
		{name: "lines that are gone", old: lines, code: CodeUnknownVariable},
		{name: "lines that became a text", old: lines, with: `"lines":  {"type": "text"},`, code: CodeBadValue},
		{name: "lines that are worked out", old: lines, with: `"lines":  {"formula": "LEN(number)"},`, code: CodeReadOnly},
		{name: "a column that is gone", old: `"qty": "int", `, code: CodeUnknownVariable},
		{name: "a column that is gone, with a document", old: `"qty": "int", `, first: true, code: CodeUnknownVariable},
		{name: "a column of another type", old: `"qty": "int"`, with: `"qty": "date"`, code: CodeBadValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, path := openStore(t, Options{Users: true, Inbound: true, Secrets: secrets})
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
			if _, err := s.DefineInlet(ctx, []byte(ctInlet), olgaBoth); err != nil {
				t.Fatal(err)
			}
			if tc.first {
				// An order without lines: a list that holds lines is not
				// taken out of its class.
				if _, err := s.Receive(ctx, "ct.order", fromCT(`{"id": "evt-0", "type": "OrderCreated", "order": {"id": "ct-1000", "orderNumber": "1000"}}`)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.old != "" {
				// The sum over the lines goes with them; its type stays.
				def := strings.Replace(strings.Replace(ctOrder, tc.old, tc.with, 1), `"total":  {"formula": "SUM(lines.qty)"}`, `"total":  {"formula": "DECIMAL(LEN(number), 0)"}`, 1)
				def = strings.Replace(def, `"name": "Order",`, `"name": "Order", "version": 2,`, 1)
				if _, err := s.DefineClass(ctx, []byte(def), olgaBoth); err != nil {
					t.Fatalf("the class as it changes: %v", err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			again, err := Open(path, Options{Now: (&clock{now: start}).Now, Users: true, Inbound: true, Secrets: secrets, Outbox: OutboxOptions{Manual: true}})
			if err != nil {
				t.Fatalf("the database does not open: %v", err)
			}
			defer again.Close()
			message := strings.Replace(strings.Replace(ctMessage, "evt-1", "evt-2", 1), "ct-1001", "ct-1002", 1)
			receipt, err := again.Receive(ctx, "ct.order", fromCT(message))
			if tc.code == "" {
				if err != nil || len(receipt.Document.Values["lines"].([]any)) != 2 {
					t.Errorf("the message after the database was opened again: %v, %s", err, dump(receipt))
				}
				return
			}
			if code(err) != tc.code {
				t.Errorf("the message after the class changed: %v; want it refused with %s", err, tc.code)
			}
			if _, err := again.Get(ctx, "Order", "ct-1002", machine); code(err) != CodeUnknownDocument {
				t.Errorf("the refused message made a document: %v", err)
			}
		})
	}
}
