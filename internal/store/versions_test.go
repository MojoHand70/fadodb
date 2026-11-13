package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

// ticketV1 and ticketV2 are two versions of a class: the second adds a
// variable, changes a formula and adds a terminal state.
const ticketV1 = `{"name": "Ticket", "variables": {
  "title_text": {"type": "text", "required": true},
  "hours": {"type": "number"},
  "rate": {"type": "decimal(2)", "default": "100"},
  "cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate, 2)"},
  "tags": {"type": "array<text>"}
}, "states": {"initial": "open", "list": ["open", "done"], "final": ["done"]}}`

const ticketV2 = `{"name": "Ticket", "variables": {
  "title_text": {"type": "text", "required": true},
  "hours": {"type": "number"},
  "rate": {"type": "decimal(2)", "default": "100"},
  "discount": {"type": "decimal(2)", "default": "0"},
  "cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate * (1 - discount), 2)"},
  "tags": {"type": "array<text>"},
  "notes": {"type": "rows", "schema": {"text": "text"}}
}, "states": {"initial": "open", "list": ["open", "done", "archived"], "final": ["done", "archived"]}}`

// frozen is a class whose documents are terminal from the start, since
// transitions arrive with Phase 3.
const frozen = `{"name": "Frozen", "variables": {"n": {"type": "number"}, "twice": {"formula": "n * 2"}},
  "states": {"initial": "done", "list": ["done"], "final": ["done"]}}`

func TestClassVersions(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(ticketV1), asSetup); err != nil {
		t.Fatal(err)
	}
	a, err := s.Create(ctx, "Ticket", Values{"title_text": "A", "hours": 2, "tags": []any{"x"}}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Create(ctx, "Ticket", Values{"title_text": "B", "hours": 3})
	if a.ClassVersion != 1 || a.Values["cost"] != "200.00" || len(a.Values) != 5 {
		t.Fatalf("v1 document: %s", dump(a))
	}
	eventsBefore, _ := s.Events(ctx, "Ticket", a.ID)
	historyBefore, _ := s.History(ctx, "Ticket", a.ID)

	// The redefinition is version 2 and touches nothing.
	cls2, err := s.DefineClass(ctx, []byte(ticketV2), asSetup)
	if err != nil {
		t.Fatal(err)
	}
	if cls2.Version != 2 {
		t.Errorf("version %d", cls2.Version)
	}
	got, _ := s.Get(ctx, "Ticket", a.ID)
	if got.ClassVersion != 1 || got.Version != 1 || got.Values["cost"] != "200.00" || len(got.Values) != 5 {
		t.Errorf("after the redefinition, a is read under version 1: %s", dump(got))
	}
	if _, has := got.Values["discount"]; has {
		t.Error("a variable of version 2 shows on a version 1 document")
	}
	// A new document is version 2.
	c, err := s.Create(ctx, "Ticket", Values{"title_text": "C", "hours": 4, "discount": "0.50", "notes": []any{Values{"text": "n1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.ClassVersion != 2 || c.Values["cost"] != "200.00" || len(c.Values) != 7 || len(c.Values["notes"].([]any)) != 1 {
		t.Errorf("v2 document: %s", dump(c))
	}
	// The views follow the current version.
	rows, err := s.SQL(ctx, `SELECT title_text, discount, class_version FROM Ticket ORDER BY title_text`, nil, asSetup)
	if err != nil || len(rows.Rows) != 3 || rows.Rows[0][1] != nil || rows.Rows[0][2] != int64(1) || rows.Rows[2][1] != int64(50) || rows.Rows[2][2] != int64(2) {
		t.Errorf("view: %v %s", err, dump(rows))
	}
	// A filter sees every document, each under its version.
	docs, err := s.Query(ctx, Filter{Class: "Ticket", Order: []string{"title_text"}})
	if err != nil || len(docs) != 3 || docs[0].ClassVersion != 1 || len(docs[0].Values) != 5 || docs[2].ClassVersion != 2 || len(docs[2].Values) != 7 {
		t.Errorf("query: %v %s", err, dump(docs))
	}
	if n, _ := s.Count(ctx, Filter{Class: "Ticket", Where: "class_version = 1"}); n != 2 {
		t.Errorf("%d documents at version 1", n)
	}

	// An edit of a live version 1 document adopts version 2: all formulas
	// recomputed, the new variables blank, the row says so; the old
	// history row stays version 1 and is read under it.
	a2, err := s.Set(ctx, "Ticket", a.ID, Values{"hours": 5}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if a2.ClassVersion != 2 || a2.Version != 2 || a2.Values["cost"] != "500.00" || a2.Values["discount"] != nil || len(a2.Values) != 7 || len(a2.Values["tags"].([]any)) != 1 {
		t.Errorf("adopted: %s", dump(a2))
	}
	v1, err := s.Version(ctx, "Ticket", a.ID, 1)
	if err != nil || v1.ClassVersion != 1 || v1.Values["cost"] != "200.00" || len(v1.Values) != 5 {
		t.Errorf("version 1 of a: %v %s", err, dump(v1))
	}
	// An edit that changes nothing on a document already at the current
	// version is still no commit; on an older version it is the adoption.
	if same, err := s.Set(ctx, "Ticket", a.ID, Values{"hours": 5}); err != nil || same.Version != 2 {
		t.Errorf("no-op on the current version: %v %d", err, same.Version)
	}
	b2, err := s.Set(ctx, "Ticket", b.ID, Values{"hours": 3})
	if err != nil || b2.Version != 2 || b2.ClassVersion != 2 {
		t.Errorf("an unchanged edit of an old document adopts: %v %s", err, dump(b2))
	}

	// Migrate moves live documents on older versions, and only those.
	d, _ := s.Create(ctx, "Ticket", Values{"title_text": "D"})
	_ = d
	ticketV3 := strings.Replace(ticketV2, `"discount": {"type": "decimal(2)", "default": "0"},`, `"discount": {"type": "decimal(2)", "default": "0"}, "urgent": {"type": "bool"},`, 1)
	if _, err := s.DefineClass(ctx, []byte(ticketV3), asSetup); err != nil {
		t.Fatal(err)
	}
	eventsAll := func() int {
		var n int
		s.db.QueryRow("SELECT count(*) FROM events").Scan(&n)
		return n
	}
	historyAll := func() int {
		var n int
		s.db.QueryRow("SELECT count(*) FROM doc_Ticket_history").Scan(&n)
		return n
	}
	e0, h0 := eventsAll(), historyAll()
	moved, err := s.Migrate(ctx, "Ticket", designing(Principal{ID: "ops"}))
	if err != nil || moved != 4 {
		t.Fatalf("migrate: %d %v", moved, err)
	}
	if again, err := s.Migrate(ctx, "Ticket", asSetup); err != nil || again != 0 {
		t.Errorf("a second migration moves %d", again)
	}
	if eventsAll() != e0+4 || historyAll() != h0+4 {
		t.Errorf("migration wrote %d events and %d history rows, want 4 and 4", eventsAll()-e0, historyAll()-h0)
	}
	a3, _ := s.Get(ctx, "Ticket", a.ID)
	if a3.ClassVersion != 3 || a3.Version != 3 || a3.Values["cost"] != "500.00" || a3.Values["urgent"] != nil {
		t.Errorf("after migration: %s", dump(a3))
	}
	ev, _ := s.Events(ctx, "Ticket", a.ID)
	if ev[len(ev)-1].Kind != EventMigrated || ev[len(ev)-1].ActorID != "ops" || ev[len(ev)-1].BeforeVersion != 2 || ev[len(ev)-1].AfterVersion != 3 {
		t.Errorf("migration event: %s", dump(ev[len(ev)-1]))
	}
	// The round trip: every event and every history row from before is
	// still there, unchanged, and reads under its version.
	eventsAfter, _ := s.Events(ctx, "Ticket", a.ID)
	historyAfter, _ := s.History(ctx, "Ticket", a.ID)
	if len(eventsAfter) < len(eventsBefore) || dump(eventsAfter[:len(eventsBefore)]) != dump(eventsBefore) {
		t.Errorf("events changed:\n%s\n%s", dump(eventsBefore), dump(eventsAfter[:len(eventsBefore)]))
	}
	historyBefore[0].ValidTo = historyAfter[0].ValidTo // the first row was closed by the later versions
	if len(historyAfter) != 3 || dump(historyAfter[0]) != dump(historyBefore[0]) {
		t.Errorf("history changed:\n%s\n%s", dump(historyBefore[0]), dump(historyAfter[0]))
	}
	for i, h := range historyAfter {
		if h.ClassVersion != i+1 {
			t.Errorf("history row %d computed by class version %d", h.Version, h.ClassVersion)
		}
	}

	// Reopening compiles every version.
	s.Close()
	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if v1, err := s2.Version(ctx, "Ticket", a.ID, 1); err != nil || v1.ClassVersion != 1 || len(v1.Values) != 5 {
		t.Errorf("version 1 after reopen: %v %s", err, dump(v1))
	}
	if got, err := s2.Get(ctx, "Ticket", a.ID); err != nil || got.ClassVersion != 3 {
		t.Errorf("current after reopen: %v %s", err, dump(got))
	}
}

func TestRedefinitionRules(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(ticketV2), asSetup); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Create(ctx, "Ticket", Values{"title_text": "A", "hours": 2, "notes": []any{Values{"text": "n"}}})
	b, _ := s.Create(ctx, "Ticket", Values{"title_text": "B"})
	expect := func(name, def, code, path string) {
		t.Helper()
		_, err := s.DefineClass(ctx, []byte(def), asSetup)
		var diags model.Diagnostics
		found := false
		if errors.As(err, &diags) {
			for _, d := range diags {
				if d.Code == code && d.Path == path {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s: want %s at %s, got %v", name, code, path, err)
		}
		if vd := s.Validate([]byte(def)); !vd.Has(code) {
			t.Errorf("%s: Validate agrees: %v", name, vd)
		}
	}
	// Values stand in the way of a removal and of a type change.
	noCost := strings.Replace(ticketV2, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate * (1 - discount), 2)"},`, `"cost": {"formula": "rate"},`, 1)
	expect("remove a variable with values", strings.Replace(noCost, `  "hours": {"type": "number"},`, "", 1), model.CodeNotAllowed, "/variables/hours")
	expect("retype a variable with values", strings.Replace(noCost, `"hours": {"type": "number"}`, `"hours": {"type": "text"}`, 1), model.CodeNotAllowed, "/variables/hours/type")
	expect("remove a composite with rows", strings.Replace(ticketV2, `,
  "notes": {"type": "rows", "schema": {"text": "text"}}`, "", 1), model.CodeNotAllowed, "/variables/notes")
	expect("remove the state documents are in", strings.Replace(ticketV2, `"initial": "open", "list": ["open", "done", "archived"]`, `"initial": "done", "list": ["done", "archived"]`, 1), model.CodeNotAllowed, "/states/list")
	// A transition out of a terminal state must lead to a terminal state.
	expect("thaw by transition", strings.Replace(ticketV2, `"final": ["done", "archived"]}`, `"final": ["done", "archived"]}, "transitions": [{"name": "reopen", "from": "done", "to": "open"}]`, 1), model.CodeNotAllowed, "/transitions/0/to")
	// A variable no document holds a value for may go, or change type;
	// the history keeps the old columns.
	v3 := strings.Replace(ticketV2, `"discount": {"type": "decimal(2)", "default": "0"},`, `"discount": {"type": "text"},`, 1)
	v3 = strings.Replace(v3, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate * (1 - discount), 2)"},`, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate, 2)"},`, 1)
	// discount holds 0.00 on both documents through its default: refused.
	expect("retype a defaulted variable", v3, model.CodeNotAllowed, "/variables/discount/type")
	if _, err := s.Set(ctx, "Ticket", a.ID, Values{"discount": nil}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(ctx, "Ticket", b.ID, Values{"discount": nil}); err != nil {
		t.Fatal(err)
	}
	// Blank now, but the history rows still hold 0.00: still refused.
	expect("retype with values in history", v3, model.CodeNotAllowed, "/variables/discount/type")
	// Removal only looks at the current rows.
	v4 := strings.Replace(ticketV2, `  "discount": {"type": "decimal(2)", "default": "0"},`, "", 1)
	v4 = strings.Replace(v4, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate * (1 - discount), 2)"},`, `"cost": {"formula": "ROUND(DECIMAL(hours, 0) * rate, 2)"},`, 1)
	cls, err := s.DefineClass(ctx, []byte(v4), asSetup)
	if err != nil || cls.Version != 2 {
		t.Fatalf("remove a blank variable: %v", err)
	}
	if got, _ := s.Get(ctx, "Ticket", a.ID); got.ClassVersion != 1 || got.Values["discount"] != nil {
		t.Errorf("a is still read under version 1: %s", dump(got))
	}
	// A variable that never held a value changes type: a fresh column.
	v5 := strings.Replace(v4, `"tags": {"type": "array<text>"}`, `"tags": {"type": "array<text>"}, "urgent": {"type": "bool"}`, 1)
	if _, err := s.DefineClass(ctx, []byte(v5), asSetup); err != nil {
		t.Fatal(err)
	}
	v6 := strings.Replace(v5, `"urgent": {"type": "bool"}`, `"urgent": {"type": "text", "index": true}`, 1)
	cls6, err := s.DefineClass(ctx, []byte(v6), asSetup)
	if err != nil || cls6.Version != 4 {
		t.Fatalf("retype a never-used variable: %v", err)
	}
	if doc, err := s.Set(ctx, "Ticket", a.ID, Values{"urgent": "yes"}); err != nil || doc.Values["urgent"] != "yes" || doc.ClassVersion != 4 {
		t.Errorf("after the type change: %v %s", err, dump(doc))
	}
	var typ string
	s.db.QueryRow("SELECT type FROM pragma_table_info('doc_Ticket') WHERE name = 'urgent'").Scan(&typ)
	if typ != "TEXT" {
		t.Errorf("the column is %s", typ)
	}
}

func TestTerminalDocuments(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(frozen), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Frozen", Values{"n": 21})
	if err != nil || doc.State != "done" || doc.Values["twice"] != 42.0 {
		t.Fatalf("create: %v %s", err, dump(doc))
	}
	if _, err := s.Set(ctx, "Frozen", doc.ID, Values{"n": 1}); code(err) != CodeTerminal {
		t.Errorf("set on a terminal document: %v", err)
	}
	if err := s.Delete(ctx, "Frozen", doc.ID); code(err) != CodeTerminal {
		t.Errorf("delete of a terminal document: %v", err)
	}
	if _, err := s.Preview(ctx, "Frozen", doc.ID, Values{"n": 1}, ""); code(err) != CodeTerminal {
		t.Errorf("preview of a terminal document: %v", err)
	}
	// A redefinition leaves it under its version; a migration skips it.
	v2 := strings.Replace(frozen, `"twice": {"formula": "n * 2"}`, `"twice": {"formula": "n * 3"}, "extra": {"type": "text"}`, 1)
	if _, err := s.DefineClass(ctx, []byte(v2), asSetup); err != nil {
		t.Fatal(err)
	}
	if moved, err := s.Migrate(ctx, "Frozen", asSetup); err != nil || moved != 0 {
		t.Errorf("migrating terminal documents: %d %v", moved, err)
	}
	got, _ := s.Get(ctx, "Frozen", doc.ID)
	if got.ClassVersion != 1 || got.Values["twice"] != 42.0 || len(got.Values) != 2 {
		t.Errorf("a terminal document after a redefinition: %s", dump(got))
	}
	// A terminal state stays terminal while documents are in it.
	_, err = s.DefineClass(ctx, []byte(strings.Replace(v2, `"final": ["done"]`, `"final": []`, 1)), asSetup)
	var diags model.Diagnostics
	if !errors.As(err, &diags) || !diags.Has(model.CodeNotAllowed) {
		t.Errorf("thawing a state with documents: %v", err)
	}
}
