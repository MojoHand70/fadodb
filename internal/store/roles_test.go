package store

import (
	"context"
	"strings"
	"testing"
)

// The three kinds of work that are not about one document (ADR-0051):
// defining, operating the queue and running SQL, each for the members of
// one system group (ADR-0052).
func TestRoles_SystemGroups(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	designer := As(Principal{ID: "dee", System: []string{GroupDesigners}, Kind: ActorAgent})
	operator := As(Principal{ID: "olaf", System: []string{GroupAdministrators}})
	analyst := As(Principal{ID: "ana", System: []string{GroupAnalysts}})
	content := As(Principal{ID: "cora", System: []string{GroupContentAdministrators}})
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
	refused := func(what string, err error, group string) {
		t.Helper()
		e, ok := err.(*Error)
		if !ok || e.Code != CodeForbidden || e.Path != "/system/"+group || !strings.Contains(e.Hint, "the system group "+group) {
			t.Errorf("%s: want forbidden naming the system group %s, got %v", what, group, err)
		}
	}
	// A content administrator acts on documents and does none of this
	// work; nor does a caller with an id and no system group, or nobody.
	for _, kind := range []struct {
		calls  map[string]call
		group  string
		may    Option
		others []Option
	}{
		{design, GroupDesigners, designer, []Option{operator, analyst, content, As(guest), As(Principal{})}},
		{operate, GroupAdministrators, operator, []Option{designer, analyst, content, As(guest), As(Principal{})}},
		{analyse, GroupAnalysts, analyst, []Option{designer, operator, content, As(guest), As(Principal{})}},
	} {
		for what, c := range kind.calls {
			for _, o := range kind.others {
				refused(what, c(o), kind.group)
			}
			// The member of the group, and a caller in all four, get past
			// the check: whatever the call then answers is not about who
			// asks.
			for _, o := range []Option{kind.may, As(root)} {
				if err := c(o); code(err) == CodeForbidden {
					t.Errorf("%s for one who may: %v", what, err)
				}
			}
		}
	}
	// A call that names no caller at all is nobody's, and is refused.
	if _, err := s.DefineClass(ctx, []byte(memo2)); code(err) != CodeForbidden {
		t.Errorf("defining without a principal: %v", err)
	}
	if _, err := s.Drain(ctx); code(err) != CodeForbidden {
		t.Errorf("draining without a principal: %v", err)
	}
	if _, err := s.SQL(ctx, `SELECT 1`, nil); code(err) != CodeForbidden {
		t.Errorf("SQL without a principal: %v", err)
	}
	// Reading a definition is open to every caller, and so is checking one.
	if _, ok := s.Class("Memo"); !ok || len(s.Classes()) != 1 {
		t.Error("the class is not readable")
	}
	if diags := s.Validate([]byte(memo)); len(diags) != 0 {
		t.Errorf("validating: %v", diags)
	}
}

// A system group is given apart from a caller's other groups (ADR-0052,
// item 2): a group of the host's own that has such a name is refused, and
// so is a system group that is none of the four a host gives.
func TestRoles_TheNamesAreTheEngines(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const memo = `{"name": "Memo", "variables": {"t": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`
	if _, err := s.DefineClass(ctx, []byte(memo), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Memo", Values{"t": "x"}, As(guest))
	if err != nil {
		t.Fatal(err)
	}
	for what, p := range map[string]Principal{
		"a directory group called designers":      {ID: "dee", Groups: []string{"staff", GroupDesigners}},
		"a directory group called administrators": {ID: "al", Groups: []string{GroupAdministrators}},
		"a directory group called everyone":       {ID: "eve", Groups: []string{GroupEveryone}},
		"a system group nobody knows":             {ID: "oz", System: []string{"owners"}},
		"everyone given by the host":              {ID: "eve", System: []string{GroupEveryone}},
	} {
		for call, err := range map[string]error{
			"DefineClass": func() error { _, err := s.DefineClass(ctx, []byte(memo), As(p)); return err }(),
			"Get":         func() error { _, err := s.Get(ctx, "Memo", doc.ID, As(p)); return err }(),
			"Set":         func() error { _, err := s.Set(ctx, "Memo", doc.ID, Values{"t": "y"}, As(p)); return err }(),
			"Query":       func() error { _, err := s.Query(ctx, Filter{Class: "Memo"}, As(p)); return err }(),
			"Drain":       func() error { _, err := s.Drain(ctx, As(p)); return err }(),
		} {
			if e, ok := err.(*Error); !ok || e.Code != CodeBadValue || e.Hint == "" {
				t.Errorf("%s, %s: want bad_value with a hint, got %v", what, call, err)
			}
		}
	}
	if got, err := s.Get(ctx, "Memo", doc.ID, As(guest)); err != nil || got.Values["t"] != "x" {
		t.Errorf("the memo after the refusals: %v %s", err, dump(got))
	}
}

// INGROUP answers the system groups as it answers any group, and answers
// everyone for every caller that has an id (ADR-0052, item 2): on the Go
// VM for a read by id and a command, in the statement of a filter where
// the name is written in the rule, and through the bound groups where it
// is computed.
func TestRoles_AClassSpeaksOfSystemGroups(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Note",
 "variables": {"t": {"type": "text"}, "circle": {"type": "text", "index": true}},
 "states": {"initial": "a", "list": ["a"]},
 "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"everyone\")", "update": "INGROUP(\"designers\") OR INGROUP(circle)", "delete": "FALSE"}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	designer := As(Principal{ID: "dee", System: []string{GroupDesigners}})
	analyst := As(Principal{ID: "ana", System: []string{GroupAnalysts}})
	doc, err := s.Create(ctx, "Note", Values{"t": "x", "circle": GroupAnalysts}, As(guest))
	if err != nil {
		t.Fatalf("everyone creates: %v", err)
	}
	if _, err := s.Create(ctx, "Note", Values{"t": "y"}); code(err) != CodeForbidden {
		t.Errorf("a call without a principal is not in everyone: %v", err)
	}
	if _, err := s.Get(ctx, "Note", doc.ID, As(guest)); err != nil {
		t.Errorf("everyone reads: %v", err)
	}
	if _, err := s.Get(ctx, "Note", doc.ID); code(err) != CodeUnknownDocument {
		t.Errorf("nobody reads: %v", err)
	}
	for who, want := range map[string]struct {
		as   []Option
		docs int64
	}{"a guest": {[]Option{As(guest)}, 1}, "an analyst": {[]Option{analyst}, 1}, "nobody": {nil, 0}} {
		if n, err := s.Count(ctx, Filter{Class: "Note"}, want.as...); err != nil || n != want.docs {
			t.Errorf("the notes %s counts: %v %d, want %d", who, err, n, want.docs)
		}
	}
	// The group a filter computes is found among the system groups too.
	for who, want := range map[string]struct {
		as   Option
		docs int64
	}{"an analyst": {analyst, 1}, "a designer": {designer, 0}, "a guest": {As(guest), 0}} {
		if n, err := s.Count(ctx, Filter{Class: "Note", Where: `INGROUP(circle)`}, want.as); err != nil || n != want.docs {
			t.Errorf("the notes of the circle of %s: %v %d, want %d", who, err, n, want.docs)
		}
	}
	if n, err := s.Count(ctx, Filter{Class: "Note", Where: `INGROUP("everyone")`}, As(guest)); err != nil || n != 1 {
		t.Errorf("everyone in a where: %v %d", err, n)
	}
	if _, err := s.Set(ctx, "Note", doc.ID, Values{"t": "z"}, designer); err != nil {
		t.Errorf("a designer updates by the rule that names the group: %v", err)
	}
	if _, err := s.Set(ctx, "Note", doc.ID, Values{"t": "z2"}, analyst); err != nil {
		t.Errorf("an analyst updates by the circle the note names: %v", err)
	}
	if _, err := s.Set(ctx, "Note", doc.ID, Values{"t": "no"}, As(guest)); code(err) != CodeForbidden {
		t.Errorf("a guest updates: %v", err)
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
 "access": {"read": "INGROUP(\"treasury\") OR INGROUP(\"staff\")", "write": "INGROUP(\"treasury\")"}}`), asSetup); err != nil {
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
		t.Errorf("a content administrator reads the rates: %v %d", err, len(rows))
	}
	// A formula reads the table with the engine's eyes (ADR-0046): the
	// stranger's document gets its rate. A rule's table action asks the
	// write rule of whoever set it off.
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Quote",
 "variables": {"code": {"type": "text"}, "rate": {"formula": "LOOKUP(Rates, rate, code = THIS.code)", "type": "number"}},
 "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "fix", "from": "a", "to": "b"}],
 "rules": [{"on": "transition", "name": "fix", "do": [{"action": "table.update", "table": "Rates", "key": {"code": "code"}, "set": {"rate": "rate + 1"}}]}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	q, err := s.Create(ctx, "Quote", Values{"code": "EUR"}, As(guest))
	if err != nil || q.Values["rate"] != 4.3 {
		t.Fatalf("a stranger's formula reads the table: %v %s", err, dump(q))
	}
	if _, err := s.Transition(ctx, "Quote", q.ID, "fix", nil, clerk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	dead, _ := s.Outbox(ctx, OutboxFilter{Status: ActionDead}, asSetup)
	if len(dead) != 1 || dead[0].ErrorCode != CodeForbidden || !strings.Contains(dead[0].ErrorMessage, "carl may not write the rows of Rates") {
		t.Errorf("the table action a clerk caused: %s", dump(dead))
	}
	q2, _ := s.Create(ctx, "Quote", Values{"code": "EUR"}, treasury)
	if _, err := s.Transition(ctx, "Quote", q2.ID, "fix", nil, treasury); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
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
		if _, err := s.DefineTable(ctx, []byte(def), asSetup); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	// A table without rules is as it was.
	if _, err := s.DefineTable(ctx, []byte(`{"name": "Plain", "columns": {"code": "text"}, "key": ["code"]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportRows(ctx, "Plain", []Values{{"code": "x"}}); err != nil {
		t.Errorf("writing a table without rules: %v", err)
	}
	if rows, err := s.TableRows(ctx, "Plain"); err != nil || len(rows) != 1 {
		t.Errorf("reading a table without rules: %v", err)
	}
}
