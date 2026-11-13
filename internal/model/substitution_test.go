package model

import (
	"strings"
	"testing"
)

// The engine's definition compiles as the engine's, and with no note: it
// writes every rule.
func TestSubstitution_TheEnginesDefinitionCompiles(t *testing.T) {
	cls, diags := DefineIn(SubstitutionDefinition(), Env{Schemas: NewSchemaSet(), Engine: true})
	if len(diags) > 0 {
		t.Fatalf("the definition does not compile: %v", diags)
	}
	if cls.Name != SubstitutionClass {
		t.Fatalf("name %q", cls.Name)
	}
	if len(cls.Notes) > 0 {
		t.Fatalf("the definition leaves a rule out: %v", cls.Notes)
	}
	for _, st := range []string{SubstitutionPlanned, SubstitutionActive, SubstitutionEnded, SubstitutionWithdrawn} {
		if !cls.States.Has(st) {
			t.Fatalf("no state %s", st)
		}
	}
	if !cls.States.IsFinal(SubstitutionEnded) || !cls.States.IsFinal(SubstitutionWithdrawn) || cls.States.IsFinal(SubstitutionActive) {
		t.Fatalf("the terminal states are ended and withdrawn")
	}
}

// A designer's definition may not take the name, in any case, and the
// refusal says whose the class is.
func TestSubstitution_TheNameIsTheEngines(t *testing.T) {
	for _, name := range []string{"Substitution", "substitution", "SUBSTITUTION"} {
		def := `{"name": "` + name + `", "variables": {"a": {"type": "text"}}, "states": {"initial": "s", "list": ["s"]}}`
		_, diags := Define([]byte(def), NewSchemaSet())
		found := false
		for _, d := range diags {
			if d.Code == CodeReservedName && d.Path == "/name" && strings.Contains(d.Message, "the engine brings") && d.Hint != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: want reserved_name at /name, got %v", name, diags)
		}
	}
	// A stored definition, which the engine wrote, loads.
	if _, diags := DefineIn(SubstitutionDefinition(), Env{Schemas: NewSchemaSet(), Lenient: true}); len(diags) > 0 {
		t.Fatalf("the stored definition does not load: %v", diags)
	}
}
