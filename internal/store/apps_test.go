package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

// TestApps_KeptAcrossAnOpen follows Acme (ADR-0077): the app Expenses and
// the class that belongs to it are there when the database is opened
// again, in the order they were defined, and the class still says where
// it belongs.
func TestApps_KeptAcrossAnOpen(t *testing.T) {
	s, path := openStore(t, Options{})
	ctx := context.Background()
	if got := s.Apps(); len(got) != 0 {
		t.Fatalf("a new database has the apps %v", got)
	}
	first, err := s.DefineApp(ctx, []byte(`{"name": "Expenses", "label": "Expenses", "description": "What employees paid for the company."}`), asSetup)
	if err != nil || first.Name != "Expenses" || first.Version != 1 || first.Def.Label != "Expenses" {
		t.Fatalf("define: %+v, %v", first, err)
	}
	if _, err := s.DefineApp(ctx, []byte(`{"name": "Helpdesk"}`), asSetup); err != nil {
		t.Fatal(err)
	}
	// The same text again is no new version, however it is spaced.
	if again, err := s.DefineApp(ctx, []byte(`{ "name":"Expenses", "label":"Expenses", "description":"What employees paid for the company." }`), asSetup); err != nil || again.Version != 1 {
		t.Fatalf("the same app again: %+v, %v", again, err)
	}
	if next, err := s.DefineApp(ctx, []byte(`{"name": "Expenses", "label": "Expenses and travel"}`), asSetup); err != nil || next.Version != 2 {
		t.Fatalf("the next version: %+v, %v", next, err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Expense", "app": "Expenses", "variables": {"what": {"type": "text"}}, "states": {"initial": "draft", "list": ["draft"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Memo", "variables": {"what": {"type": "text"}}, "states": {"initial": "draft", "list": ["draft"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(path, Options{Now: (&clock{now: start}).Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got := again.Apps(); !reflect.DeepEqual(got, []string{"Expenses", "Helpdesk"}) {
		t.Fatalf("the apps after an open: %v", got)
	}
	app, ok := again.App("EXPENSES")
	if !ok || app.Name != "Expenses" || app.Version != 2 || app.Def.Label != "Expenses and travel" {
		t.Fatalf("the app after an open: %+v", app)
	}
	if got, err := again.ClassesOf("expenses"); err != nil || !reflect.DeepEqual(got, []string{"Expense"}) {
		t.Fatalf("the classes of Expenses after an open: %v, %v", got, err)
	}
	if got, err := again.ClassesOf("Helpdesk"); err != nil || len(got) != 0 {
		t.Fatalf("the classes of an app that has none: %v, %v", got, err)
	}
	var refusal *Error
	if _, err := again.ClassesOf("Travel"); !errors.As(err, &refusal) || refusal.Code != CodeUnknownApp {
		t.Fatalf("the classes of an app that does not exist: %v", err)
	}
	stored, err := again.Definition(ctx, DefinitionApp, "Expenses", 0)
	if err != nil || stored.Version != 2 || string(stored.Definition) != `{"name":"Expenses","label":"Expenses and travel"}` || stored.Defined == nil {
		t.Fatalf("the app read back: %+v, %v", stored, err)
	}
	if _, err := again.Definition(ctx, DefinitionApp, "Expenses", 1); !errors.As(err, &refusal) || refusal.Code != CodeNotSupported {
		t.Fatalf("an earlier version of an app: %v", err)
	}
	if versions, err := again.Definitions(ctx, DefinitionApp, "Expenses"); err != nil || len(versions) != 2 {
		t.Fatalf("who defined each version: %v, %v", versions, err)
	}
}

// TestApps_AClassNamesAnAppTheDatabaseHas holds the one check an app
// adds to a class: the app it names is there. A class may name none.
func TestApps_AClassNamesAnAppTheDatabaseHas(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	trip := []byte(`{"name": "Trip", "app": "Travel", "variables": {"where": {"type": "text"}}, "states": {"initial": "draft", "list": ["draft"]}}`)
	var diags model.Diagnostics
	if _, err := s.defineOpen(ctx, trip, asSetup); !errors.As(err, &diags) || len(diags) != 1 || diags[0].Code != model.CodeUnknownApp || diags[0].Path != "/app" || diags[0].Hint == "" {
		t.Fatalf("a class of an app that does not exist: %v", err)
	}
	if got := s.Classes(); len(got) != 1 {
		t.Fatalf("a refused class was stored: %v", got)
	}
	if _, err := s.DefineApp(ctx, []byte(`{"name": "Travel"}`), asSetup); err != nil {
		t.Fatal(err)
	}
	// The name of an app is read without regard to capitals, as every
	// name is.
	cls, err := s.defineOpen(ctx, []byte(`{"name": "Trip", "app": "travel", "variables": {"where": {"type": "text"}}, "states": {"initial": "draft", "list": ["draft"]}}`), asSetup)
	if err != nil || cls.App != "travel" {
		t.Fatalf("a class of an app the database has: %+v, %v", cls, err)
	}
	if got, err := s.ClassesOf("Travel"); err != nil || !reflect.DeepEqual(got, []string{"Trip"}) {
		t.Fatalf("the classes of Travel: %v, %v", got, err)
	}
	// An app under a name that differs in capitals only is no second app.
	if _, err := s.DefineApp(ctx, []byte(`{"name": "TRAVEL"}`), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeDuplicate {
		t.Fatalf("an app whose name differs in capitals only: %v", err)
	}
	// No class may take the name of the table that keeps the apps.
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Apps", "variables": {"a": {"type": "text"}}}`), asSetup); !errors.As(err, &diags) || diags[0].Code != model.CodeReservedName {
		t.Fatalf("a class named as the table of the apps: %v", err)
	}
}

// TestApps_ADesignerMakesThem holds who may define an app: whoever may
// define a class.
func TestApps_ADesignerMakesThem(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	var refusal *Error
	if _, err := s.DefineApp(ctx, []byte(`{"name": "Expenses"}`), As(Principal{ID: "ann", Kind: ActorPerson})); !errors.As(err, &refusal) || refusal.Code != CodeForbidden {
		t.Fatalf("somebody who is no designer defined an app: %v", err)
	}
	if _, err := s.DefineApp(ctx, []byte(`{"name": "Expenses"}`), As(Principal{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}})); err != nil {
		t.Fatalf("a designer: %v", err)
	}
	if got := s.Apps(); !reflect.DeepEqual(got, []string{"Expenses"}) {
		t.Fatalf("the apps: %v", got)
	}
}
