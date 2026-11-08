package model

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

func TestVocabulary(t *testing.T) {
	s := NewSchemaSet()
	if got := strings.Join(s.Names(), " "); got != "Person Address Organization GeoPoint Place Period Event Quantity File" {
		t.Errorf("vocabulary = %q", got)
	}
	// The fields of ADR-0032, pinned.
	for name, want := range map[string]string{
		"Person":       "id text, name text, email text, phone text, avatar text",
		"Organization": "id text, name text, tax_id text, email text, phone text, address object<Address>",
		"Address":      "line1 text, line2 text, city text, postal_code text, region text, country text",
		"GeoPoint":     "lat number, lon number",
		"Place":        "name text, address object<Address>, geo object<GeoPoint>",
		"Period":       "start datetime, end datetime",
		"Event":        "title text, start datetime, end datetime, all_day bool, place object<Place>, organizer object<Person>",
		"Quantity":     "value decimal(6), unit text",
		"File":         "hash text, name text, media_type text, size int",
	} {
		sch, ok := s.Lookup(name)
		if !ok {
			t.Fatalf("no schema %s", name)
		}
		var parts []string
		for _, f := range sch.Type.Fields() {
			parts = append(parts, f.Name+" "+f.Type.String())
		}
		if got := strings.Join(parts, ", "); got != want {
			t.Errorf("%s fields = %s, want %s", name, got, want)
		}
		if !sch.Predefined || sch.Presentation == nil || sch.Presentation.Title == nil {
			t.Errorf("%s: predefined %v, presentation %+v", name, sch.Predefined, sch.Presentation)
		}
	}
	if ev, _ := s.Lookup("Event"); ev.Depth != 3 {
		t.Errorf("Event nests %d deep, want 3: Event, Place, Address (ADR-0032)", ev.Depth)
	}
	if _, ok := s.Lookup("person"); !ok {
		t.Error("schema names resolve case-insensitively")
	}
}

func TestDefineSchema(t *testing.T) {
	s := NewSchemaSet()
	def, diags := ParseSchema([]byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "text", "salary": "money(2)", "home": "object<Address>"},
		"presentation": {"title": "name & \" (\" & badge & \")\"", "subtitle": "home.city", "avatar": "avatar"}}`))
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if strings.Join(def.FieldOrder, ",") != "badge,salary,home" {
		t.Errorf("field order %v", def.FieldOrder)
	}
	sch, diags := s.Define(def)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if sch.Version != 1 || sch.Depth != 2 || len(sch.Type.Fields()) != 8 || sch.Type.Fields()[0].Name != "id" || sch.Type.Fields()[5].Name != "badge" {
		t.Errorf("Employee = version %d depth %d fields %v", sch.Version, sch.Depth, sch.Type.FieldNames())
	}
	if sch.Presentation == nil || sch.Presentation.Title == nil || sch.Presentation.Subtitle == nil || sch.Presentation.Avatar != "avatar" {
		t.Errorf("presentation %+v", sch.Presentation)
	}
	// A second definition is a new version; a class that resolved the
	// first keeps its shape, which is the caller's to hold.
	def.Fields["badge"] = "int"
	sch2, diags := s.Define(def)
	if len(diags) > 0 || sch2.Version != 2 || sch2.Type.Fields()[5].Type.String() != "int" || sch.Type.Fields()[5].Type.String() != "text" {
		t.Errorf("redefinition: %v, version %d, new badge %s, old badge %s", diags, sch2.Version, sch2.Type.Fields()[5].Type, sch.Type.Fields()[5].Type)
	}
	// Leaves of a variable of the schema.
	leaves := leavesOf("boss", sch2.Type)
	var paths []string
	for _, l := range leaves {
		paths = append(paths, l.Column)
	}
	if got := strings.Join(paths, " "); !strings.Contains(got, "boss__home__city") || !strings.Contains(got, "boss__salary") || len(leaves) != 13 {
		t.Errorf("leaves = %v", paths)
	}
}

func TestDefineSchema_Diagnostics(t *testing.T) {
	cases := []struct{ name, src, code, path string }{
		{"predefined name", `{"name": "Person", "fields": {"x": "text"}}`, CodeReservedName, "/name"},
		{"no fields", `{"name": "Empty", "fields": {}}`, CodeMissing, "/fields"},
		{"reserved field", `{"name": "S", "fields": {"idx": "int"}}`, CodeReservedName, "/fields/idx"},
		{"currency suffix", `{"name": "S", "fields": {"price_currency": "text"}}`, CodeBadName, "/fields/price_currency"},
		{"json field", `{"name": "S", "fields": {"p": "json"}}`, CodeNotAllowed, "/fields/p"},
		{"collection field", `{"name": "S", "fields": {"p": "rows"}}`, CodeNotAllowed, "/fields/p"},
		{"unknown field type", `{"name": "S", "fields": {"p": "integer"}}`, CodeUnknownType, "/fields/p"},
		{"unknown object", `{"name": "S", "fields": {"p": "object<Nope>"}}`, CodeUnknownSchema, "/fields/p"},
		{"contains itself", `{"name": "S", "fields": {"p": "object<S>"}}`, CodeSchemaCycle, "/fields/p"},
		{"extends unknown", `{"name": "S", "extends": "Nope", "fields": {"p": "text"}}`, CodeUnknownSchema, "/extends"},
		{"redeclares a base field", `{"name": "S", "extends": "Person", "fields": {"Name": "text"}}`, CodeDuplicate, "/fields/Name"},
		{"presentation not text", `{"name": "S", "fields": {"n": "number"}, "presentation": {"title": "n"}}`, CodeTypeMismatch, "/presentation/title"},
		{"presentation with context", `{"name": "S", "fields": {"n": "text"}, "presentation": {"title": "USER()"}}`, CodeBadFormula, "/presentation/title"},
		{"avatar not a field", `{"name": "S", "fields": {"n": "text"}, "presentation": {"avatar": "pic"}}`, CodeUnknownVariable, "/presentation/avatar"},
	}
	for _, tc := range cases {
		s := NewSchemaSet()
		def, diags := ParseSchema([]byte(tc.src))
		if len(diags) == 0 {
			_, diags = s.Define(def)
		}
		found := false
		for _, d := range diags {
			if d.Code == tc.code && d.Path == tc.path {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %s at %s, got:\n%s", tc.name, tc.code, tc.path, diags.Error())
		}
	}
}

func TestDefineSchema_TooDeep(t *testing.T) {
	s := NewSchemaSet()
	four, _ := ParseSchema([]byte(`{"name": "Meeting", "fields": {"event": "object<Event>"}}`))
	if sch, diags := s.Define(four); len(diags) > 0 || sch.Depth != 4 {
		t.Fatalf("a schema holding an Event nests four deep and is allowed: %v, depth %d", diags, sch.Depth)
	}
	five, _ := ParseSchema([]byte(`{"name": "Agenda", "fields": {"meeting": "object<Meeting>"}}`))
	if _, diags := s.Define(five); !diags.Has(CodeTooDeep) {
		t.Errorf("five deep is refused, got %v", diags)
	}
}

func TestClass_UsesUserSchema(t *testing.T) {
	s := NewSchemaSet()
	def, _ := ParseSchema([]byte(`{"name": "Employee", "extends": "Person", "fields": {"badge": "text"}}`))
	if _, diags := s.Define(def); len(diags) > 0 {
		t.Fatal(diags)
	}
	cls, diags := Define([]byte(`{"name": "Task", "variables": {"assignee": {"type": "object", "schema": "Employee"}, "team": {"type": "rows", "schema": "Employee"}, "who": {"formula": "assignee.name & \" #\" & assignee.badge"}}}`), s)
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	if v, _ := cls.Variable("team"); v.Type.String() != "rows<Employee>" || len(v.Type.Fields()) != 6 {
		t.Errorf("team type %s with %d columns", v.Type, len(v.Type.Fields()))
	}
	if v, _ := cls.Variable("who"); v.Type.Kind != formula.KindText || strings.Join(v.Deps, ",") != "assignee" {
		t.Errorf("who: type %s deps %v", v.Type, v.Deps)
	}
	if len(cls.Children) != 1 || len(cls.Children[0].Columns) != 6 || cls.Children[0].Columns[5].Name != "badge" {
		t.Errorf("team child table %+v", cls.Children)
	}
}

func TestTopoOrder(t *testing.T) {
	order, cycle := topoOrder([]string{"c", "b", "a", "d"}, map[string][]string{"c": {"b"}, "b": {"a"}, "d": {"x"}})
	if cycle != nil || strings.Join(order, " ") != "a b c d" {
		t.Errorf("order %v cycle %v; want a b c d: the earliest declared ready node first", order, cycle)
	}
	_, cycle = topoOrder([]string{"a", "b", "c"}, map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"b"}})
	if strings.Join(cycle, " ") != "b c b" && strings.Join(cycle, " ") != "c b c" {
		t.Errorf("cycle %v, want b c b", cycle)
	}
	_, cycle = topoOrder([]string{"a"}, map[string][]string{"a": {"a"}})
	if strings.Join(cycle, " ") != "a a" {
		t.Errorf("self cycle %v, want a a", cycle)
	}
}
