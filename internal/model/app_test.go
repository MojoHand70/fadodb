package model

import "testing"

func TestParseApp(t *testing.T) {
	def, diags := ParseApp([]byte(`{"name": "Expenses", "label": "Expenses", "description": "What employees paid for the company.", "icon": "receipt", "color": "blue"}`))
	if len(diags) != 0 || def.Name != "Expenses" || def.Label != "Expenses" || def.Icon != "receipt" || def.Color != "blue" {
		t.Fatalf("an app: %+v, %v", def, diags)
	}
	for wrong, want := range map[string]struct{ code, path string }{
		`{"label": "No name"}`:                {CodeBadName, "/name"},
		`{"name": "two words"}`:               {CodeBadName, "/name"},
		`{"name": "IF"}`:                      {CodeReservedName, "/name"},
		`{"name": "Trips", "colour": "blue"}`: {CodeBadJSON, ""},
		`{"name": "Trips", "classes": []}`:    {CodeBadJSON, ""},
		`not json`:                            {CodeBadJSON, ""},
	} {
		if _, diags := ParseApp([]byte(wrong)); len(diags) != 1 || diags[0].Code != want.code || diags[0].Path != want.path {
			t.Errorf("%s: %v", wrong, diags)
		}
	}
}

// A class names the app it belongs to; whether the app is there is asked
// of the environment, and a stored class is taken as it is.
func TestAClassNamesItsApp(t *testing.T) {
	def, diags := Parse([]byte(`{"name": "Expense", "app": "Expenses", "variables": {"what": {"type": "text"}}}`))
	if len(diags) != 0 || def.App != "Expenses" {
		t.Fatalf("parse: %+v, %v", def, diags)
	}
	has := func(name string) bool { return name == "Expenses" }
	if cls, diags := CompileIn(def, Env{Apps: has}); diags.Refuses() || cls.App != "Expenses" {
		t.Fatalf("an app the database has: %v", diags)
	}
	for _, env := range []Env{{}, {Apps: func(string) bool { return false }}} {
		_, diags := CompileIn(def, env)
		if !diags.Has(CodeUnknownApp) {
			t.Fatalf("an app the database does not have: %v", diags)
		}
	}
	if _, diags := CompileIn(def, Env{Lenient: true}); diags.Has(CodeUnknownApp) {
		t.Fatalf("a stored class is not asked again: %v", diags)
	}
}
