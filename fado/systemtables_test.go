package fado_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/fado"
)

// TestSystemTables plays the chapter on the system tables (ADR-0092):
// the four tables and their columns against the definitions the engine
// answers, the story of Acme step by step, and what is refused.
func TestSystemTables(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "manual", "system-tables.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := fado.Open(filepath.Join(t.TempDir(), "acme.fado"), fado.Options{Users: true, Outbox: fado.OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The tables, read back as the engine defined them.
	type definition struct {
		App     string                       `json:"app"`
		Label   string                       `json:"label"`
		Key     []string                     `json:"key"`
		Columns map[string]map[string]string `json:"columns"`
		Access  map[string]string            `json:"access"`
	}
	defs := map[string]definition{}
	tables := rows(marked(t, page, "the tables"))
	if len(tables) != 4 {
		t.Fatalf("the chapter shows %d tables", len(tables))
	}
	for _, row := range tables {
		stored, err := db.Definition(ctx, fado.DefinitionTable, row[0], 0)
		if err != nil {
			t.Fatalf("%s: %v", row[0], err)
		}
		var d definition
		if err := json.Unmarshal(stored.Definition, &d); err != nil {
			t.Fatal(err)
		}
		if d.App != "System" || d.Label != row[1] || strings.Join(d.Key, ",") != row[3] || d.Access["read"] != "TRUE" {
			t.Errorf("%s is defined %+v; the chapter says %v", row[0], d, row)
		}
		if tbl, ok := db.Table(row[0]); !ok || tbl.Defined.By != "fadodb" {
			t.Errorf("%s was defined by %+v", row[0], tbl)
		}
		if got, _ := db.Exec(ctx, fado.Principal{ID: "ann"}, "get_table", []byte(`{"name": "`+row[0]+`"}`)); !strings.Contains(string(got), `"defined":{"by":"fadodb"`) {
			t.Errorf("get_table of %s: %s", row[0], got)
		}
		defs[row[0]] = d
	}
	listed, _ := db.Exec(ctx, fado.Principal{ID: "ann"}, "list_tables", []byte(`{"app": "System"}`))
	if !strings.Contains(string(listed), `"tables":["SystemApps","SystemClasses","SystemUsers","SystemGroups"]`) {
		t.Errorf("the tables of System: %s", listed)
	}
	shown := map[string]int{}
	for _, row := range rows(marked(t, page, "the columns")) {
		shown[row[0]]++
		if got := defs[row[0]].Columns[row[1]]["type"]; got != row[2] {
			t.Errorf("%s.%s is %q; the chapter says %q", row[0], row[1], got, row[2])
		}
	}
	for name, d := range defs {
		if shown[name] != len(d.Columns) {
			t.Errorf("%s has %d columns; the chapter shows %d", name, len(d.Columns), shown[name])
		}
	}

	// The story.
	if _, err := db.DefineClass(ctx, []byte(marked(t, page, "the expense")), setup); err != nil {
		t.Fatalf("the expense: %v", err)
	}
	if _, err := db.DefineClass(ctx, []byte(marked(t, page, "the start page")), setup); err != nil {
		t.Fatalf("the start page: %v", err)
	}
	olga := fado.As(fado.Principal{ID: "olga", Kind: fado.ActorPerson, System: []string{fado.GroupAdministrators}})
	ann := fado.As(fado.Principal{ID: "ann", Kind: fado.ActorPerson, Groups: []string{"clerks"}})
	dan := fado.As(fado.Principal{ID: "dan", Kind: fado.ActorPerson, System: []string{fado.GroupDesigners}})
	steps := rows(marked(t, page, "what happens"))
	if len(steps) != 5 {
		t.Fatalf("the chapter tells %d steps", len(steps))
	}
	cell := func(table, column string) []string {
		got, err := db.TableRows(ctx, table, ann)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range got {
			out = append(out, fmt.Sprint(r[column]))
		}
		return out
	}
	live := func(id, variable string) any {
		v, err := db.Live(ctx, "Expense", id, variable, ann)
		if err != nil {
			t.Fatalf("%s of the expense: %v", variable, err)
		}
		return v.Value
	}
	// 1. Olga makes Ann and Marek.
	for _, u := range []fado.User{{ID: "ann", Kind: fado.ActorPerson, Name: "Ann", Groups: []string{"clerks"}}, {ID: "marek", Kind: fado.ActorPerson, Name: "Marek Nowak", Groups: []string{"approvers"}}} {
		if _, err := db.CreateUser(ctx, u, fado.As(fado.Principal{System: []string{fado.GroupAdministrators}})); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(cell("SystemUsers", "id"), ","); got != "ann,marek" || strings.Join(cell("SystemGroups", "members"), ",") != "1,1" || strings.Join(cell("SystemGroups", "name"), ",") != "approvers,clerks" {
		t.Errorf("step 1: the users %s, the groups %v %v", got, cell("SystemGroups", "name"), cell("SystemGroups", "members"))
	}
	// 2. Ann files an expense.
	doc, err := db.Create(ctx, "Expense", fado.Values{"amount": "120.00 PLN", "approver": "marek"}, ann)
	if err != nil {
		t.Fatal(err)
	}
	if name, n := live(doc.ID, "approver_name"), live(doc.ID, "approvers"); name != "Marek Nowak" || fmt.Sprint(n) != "1" || !strings.Contains(steps[1][3], "Marek Nowak") {
		t.Errorf("step 2: %v, %v", name, n)
	}
	// 3. Olga changes Marek's name.
	name := "Marek Nowak-Lis"
	if _, err := db.SetUser(ctx, "marek", fado.UserEdit{Name: &name}, olga); err != nil {
		t.Fatal(err)
	}
	if got := live(doc.ID, "approver_name"); got != name || !strings.Contains(steps[2][3], name) {
		t.Errorf("step 3: %v", got)
	}
	// 4. Ann is an approver as well.
	both := []string{"clerks", "approvers"}
	if _, err := db.SetUser(ctx, "ann", fado.UserEdit{Groups: &both}, olga); err != nil {
		t.Fatal(err)
	}
	if got := live(doc.ID, "approvers"); fmt.Sprint(got) != "2" || !strings.Contains(steps[3][3], "2") {
		t.Errorf("step 4: %v", got)
	}
	// 5. Dan defines two apps, and the start page lists them.
	for _, app := range []string{`{"name": "Helpdesk", "label": "Help desk", "icon": "support"}`, `{"name": "Expenses", "label": "Expenses", "icon": "receipt"}`} {
		if _, err := db.DefineApp(ctx, []byte(app), dan); err != nil {
			t.Fatal(err)
		}
	}
	start, err := db.Create(ctx, "Start", nil, ann)
	if err != nil {
		t.Fatal(err)
	}
	apps, err := db.Live(ctx, "Start", start.ID, "apps", ann)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, r := range apps.Rows {
		labels = append(labels, fmt.Sprint(r.Values["name"], "/", r.Values["label"], "/", r.Values["icon"]))
	}
	if got := strings.Join(labels, ","); got != "Expenses/Expenses/receipt,Helpdesk/Help desk/support" || !strings.Contains(steps[4][3], "Expenses and Helpdesk") {
		t.Errorf("step 5: the start page lists %s", got)
	}

	// What is refused.
	refused := map[string]string{}
	for _, row := range rows(marked(t, page, "what is refused")) {
		code, _, _ := strings.Cut(row[1], ",")
		refused[row[0]] = strings.TrimSpace(code)
	}
	root := fado.As(fado.Principal{ID: "root", System: []string{fado.GroupContentAdministrators}})
	codeOfErr := func(err error) string {
		var refusal *fado.Error
		if errors.As(err, &refusal) {
			return refusal.Code
		}
		return codeOf(err)
	}
	_, importErr := db.ImportRows(ctx, "SystemUsers", []fado.Values{{"id": "eve"}}, root, fado.Reason("a test"))
	removeErr := db.RemoveTable(ctx, "SystemGroups", dan)
	tried := map[string]string{
		"import_rows or delete_rows on a system table, a content administrator's too": codeOfErr(importErr),
		"remove_table of a system table":                                              codeOfErr(removeErr),
		"a rule whose table.insert, table.update or table.delete names a system table": codeOf(func() error {
			_, err := db.DefineClass(ctx, []byte(`{"name": "Note", "variables": {"who": {"type": "text"}}, "states": {"initial": "a", "list": ["a", "b"]},
			  "transitions": [{"name": "go", "from": "a", "to": "b"}],
			  "rules": [{"on": "transition", "name": "go", "do": [{"action": "table.insert", "table": "SystemUsers", "row": {"id": "who"}}]}]}`), setup)
			return err
		}()),
		"a triggered variable that lists table:SystemUsers, or any system table": codeOf(func() error {
			_, err := db.DefineClass(ctx, []byte(`{"name": "Note", "variables": {"who": {"type": "text"},
			  "who_name": {"mode": "triggered", "formula": "LOOKUP(SystemUsers, name, id = THIS.who)", "on": ["table:SystemUsers"]}}}`), setup)
			return err
		}()),
		"a table or a class named SystemApps, SystemClasses, SystemUsers or SystemGroups": tableFinding(db, `{"name": "SystemClasses", "columns": {"n": "text"}, "key": ["n"]}`),
	}
	if len(refused) != len(tried) {
		t.Errorf("the chapter refuses %d things; the test tries %d", len(refused), len(tried))
	}
	for what, code := range tried {
		if refused[what] != code {
			t.Errorf("%s: the engine answers %s, the chapter says %q", what, code, refused[what])
		}
	}
}
