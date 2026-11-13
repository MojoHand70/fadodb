package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Every version of a class, a schema, a table and an action records who
// defined it, what they were, for whom they acted and when (ADR-0052,
// item 7), and the record is read back with the definition and as a
// list.
func TestDefined_WhoDefinedEachVersion(t *testing.T) {
	now := &clock{now: start}
	funcs := map[string]ActionFunc{"audit.note": func(context.Context, *ActionCall) (Values, error) { return nil, nil }}
	s, path := openStore(t, Options{Now: now.Now, Actions: funcs})
	ctx := context.Background()
	dee := Principal{ID: "dee", Kind: ActorAgent, For: "lead", System: []string{GroupDesigners}}
	dan := Principal{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}}
	const memo1 = `{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	const memo2 = `{"name": "Memo", "variables": {"t": {"type": "text"}, "u": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	cls, err := s.DefineClass(ctx, []byte(memo1), As(dee))
	if err != nil || cls.Defined.By != "dee" || cls.Defined.Kind != ActorAgent || cls.Defined.For != "lead" || hours(cls.Defined.At) != 0 {
		t.Fatalf("the class an agent defined: %v %+v", err, cls)
	}
	// The same definition again, by somebody else, is no new version and
	// changes no record.
	now.mu.Lock()
	now.now = now.now.Add(time.Hour)
	now.mu.Unlock()
	if cls, err = s.DefineClass(ctx, []byte(memo1), As(dan)); err != nil || cls.Version != 1 || cls.Defined.By != "dee" {
		t.Errorf("the stored definition again: %v %+v", err, cls.Defined)
	}
	if cls, err = s.DefineClass(ctx, []byte(memo2), As(dan)); err != nil || cls.Version != 2 || cls.Defined.By != "dan" || hours(cls.Defined.At) != 1 {
		t.Fatalf("the second version: %v %+v", err, cls)
	}
	if _, err := s.DefineSchema(ctx, []byte(`{"name": "Depot", "fields": {"city": "text"}}`), As(dee)); err != nil {
		t.Fatal(err)
	}
	table, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"]}`), As(dan))
	if err != nil || table.Defined.By != "dan" || table.Defined.Kind != ActorPerson {
		t.Fatalf("the table: %v %+v", err, table)
	}
	action, err := s.DefineAction(ctx, []byte(`{"name": "audit.note"}`), As(dee))
	if err != nil || action.Defined == nil || action.Defined.By != "dee" || action.Defined.For != "lead" {
		t.Fatalf("the action: %v %s", err, dump(action))
	}
	listed := func(s *Store, what, name string) string {
		t.Helper()
		defs, err := s.Definitions(ctx, what, name)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, d := range defs {
			out = append(out, strings.TrimSpace(fmt.Sprintf("%s %s v%d by %s %s for %s at +%dh", d.What, d.Name, d.Version, d.By, d.Kind, d.For, hours(d.At))))
		}
		return strings.Join(out, "; ")
	}
	check := func(s *Store) {
		t.Helper()
		for key, want := range map[string]string{
			"class Memo":        "class Memo v1 by dee agent for lead at +0h; class Memo v2 by dan person for  at +1h",
			"schema Depot":      "schema Depot v1 by dee agent for lead at +1h",
			"table Rates":       "table Rates v1 by dan person for  at +1h",
			"action audit.note": "action audit.note v1 by dee agent for lead at +1h",
			"class Nothing":     "",
		} {
			what, name, _ := strings.Cut(key, " ")
			if got := listed(s, what, name); got != want {
				t.Errorf("who defined the %s: %s, want %s", key, got, want)
			}
		}
		if cls, ok := s.Class("Memo"); !ok || cls.Defined.By != "dan" || cls.Defined.Kind != ActorPerson || hours(cls.Defined.At) != 1 {
			t.Errorf("the class says who defined its current version: %+v", cls.Defined)
		}
		if table, ok := s.Table("Rates"); !ok || table.Defined.By != "dan" {
			t.Errorf("the table says who defined it: %+v", table.Defined)
		}
		if action, ok := s.Action("audit.note"); !ok || action.Defined == nil || action.Defined.By != "dee" || action.Defined.Kind != ActorAgent {
			t.Errorf("the action says who defined it: %s", dump(action))
		}
		// An action of the engine's own was defined by nobody.
		if own, ok := s.Action("set"); !ok || own.Defined != nil {
			t.Errorf("an action of the engine's own: %s", dump(own))
		}
	}
	check(s)
	// The list is open to every caller, and a kind nobody knows is
	// refused.
	if _, err := s.Definitions(ctx, "view", "Memo"); code(err) != CodeBadValue {
		t.Errorf("a definition of an unknown kind: %v", err)
	}
	// The record is in the database: another process reads the same.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Outbox: OutboxOptions{Manual: true}, Actions: funcs})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	check(again)
}

// hours is how many whole hours after the start of the test clock a
// moment is.
func hours(at int64) int64 { return (at - start.Unix()) / 3600 }
