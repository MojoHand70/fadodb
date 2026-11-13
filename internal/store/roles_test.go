package store

import (
	"context"
	"strings"
	"testing"
)

// The three kinds of work that are not about one document (ADR-0051):
// defining, operating the queue and running SQL, each for an
// administrator and for the groups the host program names.
func TestRoles_TheHostNamesTheGroups(t *testing.T) {
	s, _ := openStore(t, Options{Designers: []string{"designers"}, Operators: []string{"ops"}, Analysts: []string{"analysts"}})
	ctx := context.Background()
	designer := As(Principal{ID: "dee", Groups: []string{"designers"}, Kind: ActorAgent})
	operator := As(Principal{ID: "olaf", Groups: []string{"ops"}})
	analyst := As(Principal{ID: "ana", Groups: []string{"analysts"}})
	admin := As(root)
	const memo = `{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	const memo2 = `{"name": "Memo", "variables": {"t": {"type": "text"}, "u": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	if _, err := s.DefineClass(ctx, []byte(memo), designer); err != nil {
		t.Fatalf("a designer defines a class: %v", err)
	}
	doc, err := s.Create(ctx, "Memo", Values{"t": "x"}, operator)
	if err != nil {
		t.Fatalf("documents are not the designers' alone: %v", err)
	}
	type call func(o Option) error
	design := map[string]call{
		"DefineClass":   func(o Option) error { _, err := s.DefineClass(ctx, []byte(memo2), o); return err },
		"DefineClasses": func(o Option) error { _, err := s.DefineClasses(ctx, [][]byte{[]byte(memo2)}, o); return err },
		"DefineSchema": func(o Option) error {
			_, err := s.DefineSchema(ctx, []byte(`{"name": "Place", "fields": {"city": "text"}}`), o)
			return err
		},
		"DefineTable": func(o Option) error {
			_, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"]}`), o)
			return err
		},
		"DefineAction": func(o Option) error { _, err := s.DefineAction(ctx, []byte(`{"name": "audit.note"}`), o); return err },
		"PreviewDraft": func(o Option) error { _, err := s.PreviewDraft(ctx, []byte(memo2), doc.ID, nil, "", o); return err },
		"Migrate":      func(o Option) error { _, err := s.Migrate(ctx, "Memo", o); return err },
	}
	operate := map[string]call{
		"Outbox":      func(o Option) error { _, err := s.Outbox(ctx, OutboxFilter{}, o); return err },
		"Drain":       func(o Option) error { _, err := s.Drain(ctx, o); return err },
		"CheckBlobs":  func(o Option) error { _, _, err := s.CheckBlobs(ctx, o); return err },
		"RetryAction": func(o Option) error { return s.RetryAction(ctx, "none:0", o) },
	}
	analyse := map[string]call{
		"SQL": func(o Option) error { _, err := s.SQL(ctx, `SELECT count(*) FROM Memo`, nil, o); return err },
	}
	refused := func(what string, err error, option string) {
		t.Helper()
		e, ok := err.(*Error)
		if !ok || e.Code != CodeForbidden || e.Path != "/options/"+option || !strings.Contains(e.Hint, "Options."+option) {
			t.Errorf("%s: want forbidden naming Options.%s, got %v", what, option, err)
		}
	}
	for _, kind := range []struct {
		calls  map[string]call
		option string
		may    Option
		others []Option
	}{
		{design, "Designers", designer, []Option{operator, analyst, As(guest), As(Principal{})}},
		{operate, "Operators", operator, []Option{designer, analyst, As(guest)}},
		{analyse, "Analysts", analyst, []Option{designer, operator, As(guest)}},
	} {
		for what, c := range kind.calls {
			for _, o := range kind.others {
				refused(what, c(o), kind.option)
			}
			// The one the host named, and an administrator, get past the
			// check: whatever the call then answers is not about who asks.
			for _, o := range []Option{kind.may, admin} {
				if err := c(o); code(err) == CodeForbidden {
					t.Errorf("%s for one who may: %v", what, err)
				}
			}
		}
	}
	// Reading a definition is open to every caller, and so is checking one.
	if _, ok := s.Class("Memo"); !ok || len(s.Classes()) != 1 {
		t.Error("the class is not readable")
	}
	if diags := s.Validate([]byte(memo)); len(diags) != 0 {
		t.Errorf("validating: %v", diags)
	}
}

// A list that is empty leaves its work to administrators; a host that
// names no list at all is asked nothing, as before.
func TestRoles_EmptyAndUnnamed(t *testing.T) {
	ctx := context.Background()
	const memo = `{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	closed, _ := openStore(t, Options{Designers: []string{}, Operators: []string{}, Analysts: []string{}})
	if _, err := closed.DefineClass(ctx, []byte(memo), As(Principal{ID: "dee", Groups: []string{"designers"}})); code(err) != CodeForbidden {
		t.Errorf("defining where the host named nobody: %v", err)
	}
	if _, err := closed.DefineClass(ctx, []byte(memo), As(root)); err != nil {
		t.Errorf("an administrator defines where the host named nobody: %v", err)
	}
	if _, err := closed.SQL(ctx, `SELECT 1`, nil); code(err) != CodeForbidden {
		t.Errorf("SQL where the host named nobody: %v", err)
	}
	if _, err := closed.Drain(ctx); code(err) != CodeForbidden {
		t.Errorf("draining where the host named nobody: %v", err)
	}
	open, _ := openStore(t, Options{})
	if _, err := open.DefineClass(ctx, []byte(memo)); err != nil {
		t.Errorf("defining where the host named no list: %v", err)
	}
	if _, err := open.SQL(ctx, `SELECT count(*) FROM Memo`, nil); err != nil {
		t.Errorf("SQL where the host named no list: %v", err)
	}
	if _, err := open.Drain(ctx); err != nil {
		t.Errorf("draining where the host named no list: %v", err)
	}
}

// A table has two rules (ADR-0051, item 7): who may read its rows and who
// may write them, each about the table as a whole.
func TestRoles_TableRules(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	treasury := As(Principal{ID: "tess", Groups: []string{"treasury"}})
	clerk := As(Principal{ID: "carl", Groups: []string{"staff"}})
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Rates", "columns": {"code": "text", "rate": "number"}, "key": ["code"],
 "access": {"read": "INGROUP(\"treasury\") OR INGROUP(\"staff\")", "write": "INGROUP(\"treasury\")"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Rates", []Values{{"code": "EUR", "rate": 4.3}}, treasury); err != nil {
		t.Fatalf("treasury writes the rates: %v", err)
	}
	_, err := s.ImportRows(ctx, "Rates", []Values{{"code": "USD", "rate": 1}}, clerk)
	if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/access/write" || !strings.Contains(e.Message, "carl may not write the rows of Rates") || !strings.Contains(e.Hint, "treasury") {
		t.Errorf("a clerk writes the rates: %v", err)
	}
	if rows, err := s.TableRows(ctx, "Rates", clerk); err != nil || len(rows) != 1 {
		t.Errorf("a clerk reads the rates: %v %d", err, len(rows))
	}
	_, err = s.TableRows(ctx, "Rates", As(guest))
	if e, ok := err.(*Error); !ok || e.Code != CodeForbidden || e.Path != "/access/read" {
		t.Errorf("a stranger reads the rates: %v", err)
	}
	if rows, err := s.TableRows(ctx, "Rates", As(root)); err != nil || len(rows) != 1 {
		t.Errorf("an administrator reads the rates: %v %d", err, len(rows))
	}
	// A formula reads the table with the engine's eyes (ADR-0046): the
	// stranger's document gets its rate. A rule's table action asks the
	// write rule of whoever set it off.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Quote",
 "variables": {"code": {"type": "text"}, "rate": {"formula": "LOOKUP(Rates, rate, code = THIS.code)", "type": "number"}},
 "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "fix", "from": "a", "to": "b"}],
 "rules": [{"on": "transition", "name": "fix", "do": [{"action": "table.update", "table": "Rates", "key": {"code": "code"}, "set": {"rate": "rate + 1"}}]}]}`)); err != nil {
		t.Fatal(err)
	}
	q, err := s.Create(ctx, "Quote", Values{"code": "EUR"}, As(guest))
	if err != nil || q.Values["rate"] != 4.3 {
		t.Fatalf("a stranger's formula reads the table: %v %s", err, dump(q))
	}
	if _, err := s.Transition(ctx, "Quote", q.ID, "fix", nil, clerk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	dead, _ := s.Outbox(ctx, OutboxFilter{Status: ActionDead})
	if len(dead) != 1 || dead[0].ErrorCode != CodeForbidden || !strings.Contains(dead[0].ErrorMessage, "carl may not write the rows of Rates") {
		t.Errorf("the table action a clerk caused: %s", dump(dead))
	}
	q2, _ := s.Create(ctx, "Quote", Values{"code": "EUR"}, treasury)
	if _, err := s.Transition(ctx, "Quote", q2.ID, "fix", nil, treasury); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.TableRows(ctx, "Rates", treasury); len(rows) != 1 || rows[0]["rate"] != 5.3 {
		t.Errorf("the rates after treasury's rule: %s", dump(rows))
	}
	// A rule of a table is about the table as a whole.
	for what, def := range map[string]string{
		"a rule that reads a column":  `{"name": "A", "columns": {"code": "text"}, "key": ["code"], "access": {"read": "code = \"EUR\""}}`,
		"a rule that is no condition": `{"name": "B", "columns": {"code": "text"}, "key": ["code"], "access": {"write": "USER()"}}`,
		"a rule that reads a table":   `{"name": "C", "columns": {"code": "text"}, "key": ["code"], "access": {"read": "LOOKUP(Rates, rate, TRUE) > 0"}}`,
	} {
		if _, err := s.DefineTable(ctx, []byte(def)); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	// A table without rules is as it was.
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Plain", "columns": {"code": "text"}, "key": ["code"]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Plain", []Values{{"code": "x"}}); err != nil {
		t.Errorf("writing a table without rules: %v", err)
	}
	if rows, err := s.TableRows(ctx, "Plain"); err != nil || len(rows) != 1 {
		t.Errorf("reading a table without rules: %v", err)
	}
}
