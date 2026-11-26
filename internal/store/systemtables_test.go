package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

// rowsOf reads a table's rows as text, one line a row, its columns in
// the order given.
func rowsOf(t *testing.T, s *Store, table string, columns ...string) []string {
	t.Helper()
	rows, err := s.TableRows(context.Background(), table, annUser)
	if err != nil {
		t.Fatalf("the rows of %s: %v", table, err)
	}
	var out []string
	for _, row := range rows {
		var cells []string
		for _, c := range columns {
			switch v := row[c].(type) {
			case json.RawMessage:
				cells = append(cells, string(v))
			default:
				cells = append(cells, fmt.Sprint(v))
			}
		}
		out = append(out, strings.Join(cells, "|"))
	}
	return out
}

// TestSystemTables_WhatTheDatabaseHolds follows Acme (ADR-0092). Olga
// administers the server, Dan designs the app Expenses, Ann files an
// expense and Marek approves it. Every database has the app System and
// its four tables; their rows are what the database holds at the moment
// they are read; anybody reads them and nobody writes them.
func TestSystemTables_WhatTheDatabaseHolds(t *testing.T) {
	s, path := openStore(t, Options{Users: true})
	ctx := context.Background()
	check := func(s *Store, when string) {
		t.Helper()
		if got := s.Apps(); len(got) == 0 || got[0] != model.SystemApp {
			t.Fatalf("%s: the apps are %v", when, got)
		}
		if got, err := s.TablesOf("system"); err != nil || strings.Join(got, ",") != systemTables {
			t.Fatalf("%s: the tables of System: %v, %v", when, got, err)
		}
	}
	check(s, "new")

	// What the database holds is made by the commands as before.
	if _, err := s.DefineApp(ctx, []byte(`{"name": "Expenses", "label": "Expenses", "icon": "receipt"}`), asSetup); err != nil {
		t.Fatal(err)
	}
	expense := `{"name": "Expense", "app": "Expenses", "label": "Expense", "variables": {
	  "approver":      {"type": "text"},
	  "note":          {"type": "text"},
	  "approver_name": {"formula": "LOOKUP(SystemUsers, name, id = THIS.approver)"},
	  "approvers":     {"formula": "LOOKUP(SystemGroups, members, name = \"approvers\")"},
	  "approver_now":  {"type": "text", "mode": "live", "formula": "LOOKUP(SystemUsers, name, id = THIS.approver)"}
	}, "states": {"initial": "open", "list": ["open"]}}`
	if _, err := s.DefineClass(ctx, []byte(expense), asSetup); err != nil {
		t.Fatalf("a class that reads the system tables: %v", err)
	}
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, Name: "Olga Lis", System: []string{GroupAdministrators}},
		{ID: "ann", Kind: ActorPerson, Name: "Ann Kowalska", Groups: []string{"clerks"}},
		{ID: "marek", Kind: ActorPerson, Name: "Marek Nowak", Email: "marek@acme.example", Groups: []string{"approvers", "clerks"}},
	} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	label := "Approvers"
	if _, err := s.SetGroup(ctx, "approvers", GroupEdit{Label: &label}, olga); err != nil {
		t.Fatal(err)
	}

	if got := rowsOf(t, s, model.SystemApps, "name", "label", "icon", "version"); !reflect.DeepEqual(got, []string{"Expenses|Expenses|receipt|1", "System|System|<nil>|1"}) {
		t.Errorf("SystemApps: %q", got)
	}
	if got := rowsOf(t, s, model.SystemClasses, "name", "app", "label"); !reflect.DeepEqual(got, []string{"Expense|Expenses|Expense", "Substitution|<nil>|Substitution"}) {
		t.Errorf("SystemClasses: %q", got)
	}
	if got := rowsOf(t, s, model.SystemUsers, "id", "kind", "name", "email", "groups", "system_groups", "disabled"); !reflect.DeepEqual(got, []string{
		`ann|person|Ann Kowalska|<nil>|["clerks"]|[]|false`,
		`marek|person|Marek Nowak|marek@acme.example|["approvers","clerks"]|[]|false`,
		`olga|person|Olga Lis|<nil>|[]|["administrators"]|false`,
	}) {
		t.Errorf("SystemUsers: %q", got)
	}
	if got := rowsOf(t, s, model.SystemGroups, "name", "label", "members"); !reflect.DeepEqual(got, []string{"approvers|Approvers|1", "clerks||2"}) {
		t.Errorf("SystemGroups: %q", got)
	}

	// A formula reads a system table as any table, as it is at that
	// moment: no row is kept from before. A value worked out in a commit
	// stays as it was worked out, as one from any table does; a live one
	// is worked out each time the document is read.
	doc, err := s.Create(ctx, "Expense", Values{"approver": "marek"}, As(root), past)
	if err != nil || doc.Values["approver_name"] != "Marek Nowak" || fmt.Sprint(doc.Values["approvers"]) != "1" {
		t.Fatalf("an expense names its approver: %+v, %v", doc, err)
	}
	name := "Marek Nowak-Lis"
	if _, err := s.SetUser(ctx, "marek", UserEdit{Name: &name}, olga); err != nil {
		t.Fatal(err)
	}
	ann := []string{"clerks", "approvers"}
	if _, err := s.SetUser(ctx, "ann", UserEdit{Groups: &ann}, olga); err != nil {
		t.Fatal(err)
	}
	if doc, err = s.Set(ctx, "Expense", doc.ID, Values{"note": "taxi"}, As(root), past); err != nil || doc.Values["approver_name"] != "Marek Nowak" {
		t.Fatalf("the expense after Marek's new name: %+v, %v", doc, err)
	}
	if live, err := s.Live(ctx, "Expense", doc.ID, "approver_now", As(root), past); err != nil || live.Value != "Marek Nowak-Lis" {
		t.Fatalf("the live name after Marek's new name: %+v, %v", live, err)
	}
	second, err := s.Create(ctx, "Expense", Values{"approver": "marek"}, As(root), past)
	if err != nil || second.Values["approver_name"] != "Marek Nowak-Lis" || fmt.Sprint(second.Values["approvers"]) != "2" {
		t.Fatalf("an expense after Marek's new name: %+v, %v", second, err)
	}

	// Nobody writes a system table, a content administrator neither.
	var refusal *Error
	if _, err := s.ImportRows(ctx, "systemusers", []Values{{"id": "eve"}}, As(root), past); !errors.As(err, &refusal) || refusal.Code != CodeReadOnly || refusal.Hint == "" {
		t.Errorf("an import into a system table: %v", err)
	}
	if _, err := s.DeleteRows(ctx, model.SystemUsers, []Values{{"id": "ann"}}, As(root), past); !errors.As(err, &refusal) || refusal.Code != CodeReadOnly {
		t.Errorf("a delete from a system table: %v", err)
	}
	if err := s.RemoveTable(ctx, model.SystemGroups, asSetup); !errors.As(err, &refusal) || refusal.Code != CodeReadOnly {
		t.Errorf("a system table removed: %v", err)
	}
	var diags model.Diagnostics
	if _, err := s.DefineTable(ctx, []byte(`{"name": "SystemUsers", "columns": {"id": "text"}, "key": ["id"]}`), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeReservedName {
		t.Errorf("a table under a system table's name: %v", err)
	}
	if _, err := s.DefineClass(ctx, []byte(`{"name": "SystemGroups", "variables": {"n": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeReservedName {
		t.Errorf("a class under a system table's name: %v", err)
	}
	follows := `{"name": "Note", "variables": {"who": {"type": "text"},
	  "who_name": {"mode": "triggered", "formula": "LOOKUP(SystemUsers, name, id = THIS.who)", "on": ["table:SystemUsers"]}
	}, "states": {"initial": "a", "list": ["a"]}}`
	if _, err := s.DefineClass(ctx, []byte(follows), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotSupported || diags[0].Hint == "" {
		t.Errorf("a value that follows a system table: %v", err)
	}
	writes := `{"name": "Note", "variables": {"who": {"type": "text"}}, "states": {"initial": "a", "list": ["a", "b"]},
	  "transitions": [{"name": "go", "from": "a", "to": "b"}],
	  "rules": [{"on": "transition", "name": "go", "do": [{"action": "table.insert", "table": "SystemUsers", "row": {"id": "who"}}]}]}`
	if _, err := s.DefineClass(ctx, []byte(writes), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeNotAllowed {
		t.Errorf("a rule that writes a system table: %v", err)
	}

	// The definition reads back as any table's, the engine's own.
	stored, err := s.Definition(ctx, DefinitionTable, "SystemUsers", 0)
	if err != nil || !strings.Contains(string(stored.Definition), `"app":"System"`) || !strings.Contains(string(stored.Definition), `"read":"TRUE"`) {
		t.Errorf("the definition of SystemUsers: %+v, %v", stored, err)
	}
	if tbl, ok := s.Table("SystemUsers"); !ok || tbl.Defined.By != engineActor.ID || tbl.App != model.SystemApp {
		t.Errorf("SystemUsers is %+v", tbl)
	}

	// Opened again, the database has them still, the class that reads
	// them compiles, and the views are not written again.
	var before string
	if err := s.db.QueryRowContext(ctx, "SELECT group_concat(sql, ';') FROM sqlite_master WHERE type = 'view' AND name LIKE 'System%'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path, Options{Users: true, Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("opened again: %v", err)
	}
	defer again.Close()
	check(again, "opened again")
	if got := strings.Join(again.Tables(), ","); got != systemTables {
		t.Errorf("the tables opened again: %s", got)
	}
	if got := again.Apps(); !reflect.DeepEqual(got, []string{"System", "Expenses"}) {
		t.Errorf("the apps opened again: %v", got)
	}
	var after string
	if err := again.db.QueryRowContext(ctx, "SELECT group_concat(sql, ';') FROM sqlite_master WHERE type = 'view' AND name LIKE 'System%'").Scan(&after); err != nil || after != before {
		t.Errorf("the views opened again: %v\n%s\n%s", err, before, after)
	}
	if doc, err = again.Set(ctx, "Expense", doc.ID, Values{"approver": "ann"}, As(root), past); err != nil || doc.Values["approver_name"] != "Ann Kowalska" {
		t.Fatalf("the expense after the opening: %+v, %v", doc, err)
	}
}

// TestSystemTables_WhatWasThereBefore opens databases made before the
// engine had system tables (ADR-0092): one where a designer made the app
// System, as a face did on Acme's servers, keeps it with its words; one
// whose own table took a system table's name keeps that table, and the
// engine leaves that system table out.
func TestSystemTables_WhatWasThereBefore(t *testing.T) {
	ctx := context.Background()
	s, path := openStore(t, Options{Users: true})
	if _, err := s.DefineApp(ctx, []byte(`{"name": "System", "label": "System", "description": "The face's own pages."}`), asSetup); err != nil {
		t.Fatal(err)
	}
	// What a database of the release before holds: a table of its own
	// named SystemGroups, and no view of the engine's under that name.
	if _, err := s.writer.ExecContext(ctx, `DROP VIEW "SystemGroups"`); err != nil {
		t.Fatal(err)
	}
	mine := `{"name":"SystemGroups","columns":{"code":"text"},"key":["code"]}`
	def, diags := model.DefineTable([]byte(mine))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	for _, ddl := range newTable(def, []byte(mine)).createDDL() {
		if _, err := s.writer.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.writer.ExecContext(ctx, "INSERT INTO tables_registry (name, version, binding, json, created_at, updated_at) VALUES ('SystemGroups', 1, 'sqlite', ?, 0, 0)", mine); err != nil {
		t.Fatal(err)
	}
	s.Close()

	again, err := Open(path, Options{Users: true, Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatalf("opened: %v", err)
	}
	defer again.Close()
	app, ok := again.App("System")
	if !ok || app.Version != 2 || app.Def.Description != "The face's own pages." {
		t.Errorf("the app System of the designer: %+v", app)
	}
	if got := strings.Join(again.Tables(), ","); got != "SystemGroups,SystemApps,SystemClasses,SystemUsers" {
		t.Errorf("the tables: %s", got)
	}
	if _, err := again.DefineTable(ctx, []byte(`{"name":"SystemGroups","label":"Mine","columns":{"code":"text"},"key":["code"]}`), asSetup); err != nil {
		t.Errorf("the database's own SystemGroups is changed as before: %v", err)
	}
	if n, err := again.ImportRows(ctx, "SystemGroups", []Values{{"code": "A"}}, As(root), past); err != nil || n != 1 {
		t.Errorf("the database's own SystemGroups takes rows: %d, %v", n, err)
	}
}
