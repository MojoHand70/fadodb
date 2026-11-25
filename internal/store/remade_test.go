package store

import (
	"context"
	"strings"
	"testing"
)

// A class changes, and the database opens again. An earlier version of
// a class only reads the documents it computed, and the tables may have
// moved on without it: a class that had no document yet has its tables
// made again for the new version, and a list that holds no value
// anywhere may be taken out or given other columns. v0.7.0 prepared
// every earlier version against the tables as they are now, and a
// database whose tables had moved on did not open.
const remadeOrder = `{
  "name": "Order",
  "title": "number",
  "variables": {
    "number": {"type": "text", "required": true},
    "note":   {"type": "text"},
    "lines":  {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}},
    "tags":   {"type": "array<text>"},
    "total":  {"formula": "LEN(number)"}
  },
  "states": {"initial": "received", "list": ["received", "booking"]},
  "transitions": [{"name": "book", "from": "received", "to": "booking"}]
}`

const remadeLines = `"lines":  {"type": "rows", "schema": {"sku": "text", "qty": "int", "price": "money(2)"}},`

// remadeChanges are the ways the second version differs from the first:
// what is written in the first, and what stands there in the second.
var remadeChanges = map[string][2]string{
	"lines that became a text":    {remadeLines, `"lines":  {"type": "text"},`},
	"lines that are gone":         {remadeLines, ``},
	"a column of another type":    {`"qty": "int"`, `"qty": "text"`},
	"a column that is gone":       {`"qty": "int", `, ``},
	"a column more":               {`"qty": "int", `, `"qty": "int", "colour": "text", `},
	"a text that is gone":         {`"note":   {"type": "text"},`, ``},
	"a text that became a number": {`"note":   {"type": "text"},`, `"note":   {"type": "number"},`},
	"tags that are gone":          {`"tags":   {"type": "array<text>"},`, ``},
	"tags that became numbers":    {`"tags":   {"type": "array<text>"},`, `"tags":   {"type": "array<number>"},`},
}

func remadeVersion(t *testing.T, change [2]string, version string) string {
	t.Helper()
	def := strings.Replace(remadeOrder, change[0], change[1], 1)
	if def == remadeOrder {
		t.Fatalf("the class of the test is not written as the change reads it: %s", change[0])
	}
	return strings.Replace(def, `"name": "Order",`, `"name": "Order", "version": `+version+`,`, 1)
}

func TestOpen_AfterAClassChanged(t *testing.T) {
	ctx := context.Background()
	for _, with := range []bool{false, true} {
		for name, change := range remadeChanges {
			label := name
			if with {
				label += ", with a document"
			}
			t.Run(label, func(t *testing.T) {
				s, path := openStore(t, Options{})
				if _, err := s.defineOpen(ctx, []byte(remadeOrder), asSetup); err != nil {
					t.Fatal(err)
				}
				if with {
					// An order that holds no line, no tag and no note: what
					// holds a value is not taken out of its class.
					if _, err := s.Create(ctx, "Order", Values{"number": "1000"}, WithID("o-1")); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.defineOpen(ctx, []byte(remadeVersion(t, change, "2")), asSetup); err != nil {
					t.Fatalf("the second version of the class: %v", err)
				}
				check := func(s *Store, when string) {
					t.Helper()
					if !with {
						return
					}
					// The order is still read, as the first version of its
					// class computed it, with nothing in its lists.
					d, err := s.Get(ctx, "Order", "o-1")
					if err != nil || d.Values["number"] != "1000" || d.ClassVersion != 1 {
						t.Fatalf("the order %s: %v, %s", when, err, dump(d))
					}
					for _, list := range []string{"lines", "tags"} {
						if got, is := d.Values[list].([]any); d.Values[list] != nil && (!is || len(got) != 0) {
							t.Errorf("%s of the order %s: %s", list, when, dump(d.Values[list]))
						}
					}
					if v, err := s.Version(ctx, "Order", "o-1", 1); err != nil || v.ClassVersion != 1 {
						t.Errorf("the first version of the order %s: %v", when, err)
					}
				}
				check(s, "after the class changed")
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
				if err != nil {
					t.Fatalf("the database does not open: %v", err)
				}
				defer again.Close()
				check(again, "after the database opened again")
				// Both definitions are still read, by their versions.
				for version, want := range map[int]string{1: remadeOrder, 2: remadeVersion(t, change, "2")} {
					def, err := again.Definition(ctx, DefinitionClass, "Order", version)
					if err != nil || def.Version != version {
						t.Errorf("the definition at version %d: %v", version, err)
						continue
					}
					if strings.Contains(want, `"note"`) != strings.Contains(string(def.Definition), `"note"`) || strings.Contains(want, `"tags"`) != strings.Contains(string(def.Definition), `"tags"`) {
						t.Errorf("the definition at version %d is not the one that was given: %s", version, def.Definition)
					}
				}
				// The class works on: a new order under the second version,
				// and the old one moves on under it.
				if _, err := again.Create(ctx, "Order", Values{"number": "1001"}, WithID("o-2")); err != nil {
					t.Errorf("a new order after the database opened again: %v", err)
				}
				if with {
					if d, err := again.Transition(ctx, "Order", "o-1", "book", nil); err != nil || d.State != "booking" {
						t.Errorf("the old order moved on: %v, %s", err, dump(d))
					}
				}
			})
		}
	}
}

// A third version changes the list again, and an order the first
// version computed is still read: every earlier version is brought to
// the tables as they are, not only the one before the last.
func TestOpen_AfterAClassChangedTwice(t *testing.T) {
	ctx := context.Background()
	s, path := openStore(t, Options{})
	if _, err := s.defineOpen(ctx, []byte(remadeOrder), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "1000"}, WithID("o-1")); err != nil {
		t.Fatal(err)
	}
	second := strings.Replace(remadeOrder, `"name": "Order",`, `"name": "Order", "version": 2, "label": "Order",`, 1)
	if _, err := s.defineOpen(ctx, []byte(second), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Order", Values{"number": "2000"}, WithID("o-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(remadeVersion(t, remadeChanges["a column that is gone"], "3")), asSetup); err != nil {
		t.Fatalf("the third version of the class: %v", err)
	}
	read := func(s *Store, when string) {
		t.Helper()
		for id, version := range map[string]int{"o-1": 1, "o-2": 2} {
			if d, err := s.Get(ctx, "Order", id); err != nil || d.ClassVersion != version {
				t.Errorf("%s %s: %v, %s", id, when, err, dump(d))
			}
		}
	}
	read(s, "after the third version")
	// An order of the first version takes lines in the columns the class
	// has now.
	if d, err := s.Set(ctx, "Order", "o-1", Values{"lines": []any{map[string]any{"sku": "SOFA-3", "price": "2400.00 PLN"}}}); err != nil || d.ClassVersion != 3 {
		t.Fatalf("lines for the first order: %v, %s", err, dump(d))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("the database does not open: %v", err)
	}
	defer again.Close()
	if d, err := again.Get(ctx, "Order", "o-2"); err != nil || d.ClassVersion != 2 {
		t.Errorf("o-2 after the database opened again: %v, %s", err, dump(d))
	}
	if d, err := again.Get(ctx, "Order", "o-1"); err != nil || flat(d.Values["lines"]) != `[{"price":"2400.00 PLN","sku":"SOFA-3"}]` {
		t.Errorf("o-1 after the database opened again: %v, %s", err, dump(d))
	}
	// As the first version computed it, the order had no lines.
	if v, err := again.Version(ctx, "Order", "o-1", 1); err != nil || v.ClassVersion != 1 || v.Values["lines"] != nil && len(v.Values["lines"].([]any)) != 0 {
		t.Errorf("the first version of o-1 after the database opened again: %v, %s", err, dump(v))
	}
}
