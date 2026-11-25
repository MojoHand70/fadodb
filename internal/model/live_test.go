package model

import (
	"fmt"
	"testing"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// A live variable is worked out when it is read and kept nowhere
// (ADR-0076): the class takes it, gives it its formula's type, and lays
// out no column for it.
func TestLive_Definition(t *testing.T) {
	cls := mustDefine(t, `{
  "name": "Invoice",
  "variables": {
    "number":       {"type": "text"},
    "owner":        {"type": "text"},
    "net":          {"type": "money(2)"},
    "waiting_days": {"label": "Days waiting", "mode": "live", "formula": "DAYS(TODAY(), DATEOF(ENTEREDAT()))"},
    "mine":         {"type": "bool", "mode": "live", "formula": "owner = USER()", "description": "Whether the reader wrote it."},
    "gross":        {"formula": "ROUND(net * 1.23, 2)"}
  },
  "states": {"initial": "review", "list": ["review", "paid"], "final": ["paid"]},
  "transitions": [{"name": "pay", "from": "review", "to": "paid"}]
}`)
	days, ok := cls.Variable("waiting_days")
	if !ok || !days.Live() || days.Mode != ModeLive || days.Triggered() {
		t.Fatalf("waiting_days = %+v, want a live variable", days)
	}
	if days.Formula == nil || days.Type.Kind == formula.KindNull {
		t.Errorf("waiting_days has formula %v and type %s, want its formula bound and its type taken from it", days.Formula, days.Type)
	}
	mine, _ := cls.Variable("mine")
	if !mine.Live() || mine.Type.Kind != formula.KindBool || mine.Formula == nil {
		t.Errorf("mine = %+v, want a live bool with its formula bound", mine)
	}
	// Kept nowhere: no column, and no place in the order a commit
	// recalculates in.
	for _, col := range cls.Columns {
		if col.Variable == "waiting_days" || col.Variable == "mine" {
			t.Errorf("column %s belongs to a live variable; a live value is kept nowhere", col.Name)
		}
	}
	for _, name := range cls.Order {
		if name == "waiting_days" || name == "mine" {
			t.Errorf("the order of recalculation holds %s; nothing recalculates a live value", name)
		}
	}
	if gross, _ := cls.Variable("gross"); gross.Live() || gross.Mode != ModeAuto {
		t.Errorf("gross = %+v, want an auto variable beside the live ones", gross)
	}
	// In the bundle a live variable is the server's (item 13): a page gets
	// no formula for it, and asks the command live.
	served := map[string]BundleFormula{}
	for _, f := range Compiled(cls).Computed {
		served[f.Variable] = f
	}
	for _, name := range []string{"waiting_days", "mine"} {
		if f, ok := served[name]; !ok || !f.Server || f.Formula != "" || f.Type == "" {
			t.Errorf("the bundle has %s as %+v (%v), want it as the server's, with its type and no formula", name, f, ok)
		}
	}
	if f := served["gross"]; f.Server || f.Formula == "" {
		t.Errorf("the bundle has gross as %+v, want its formula for the page", f)
	}
}

// What speaks of a kept value has no place on a live one, and nothing
// that is kept or decided may read one (ADR-0076, items 4 and 6).
func TestLive_Refusals(t *testing.T) {
	const states = `, "states": {"initial": "s", "list": ["s", "e"], "final": ["e"]}, "transitions": [{"name": "end", "from": "s", "to": "e"%s}]`
	class := func(vars, more string) string { return `{"name": "T", "variables": {` + vars + `}` + more + `}` }
	const live = `"a": {"type": "int"}, "l": {"mode": "live", "formula": "a + 1"}`
	cases := []struct{ name, src, code, path string }{
		{"triggers", variables(`"a": {"type": "int"}, "l": {"mode": "live", "formula": "a", "on": ["refresh"]}`), CodeNotAllowed, "/variables/l/on"},
		{"an index", variables(`"a": {"type": "int"}, "l": {"mode": "live", "formula": "a", "index": true}`), CodeNotAllowed, "/variables/l/index"},
		{"a place in The List", variables(`"a": {"type": "int"}, "l": {"mode": "live", "formula": "a", "list": true}`), CodeNotAllowed, "/variables/l/list"},
		{"values", variables(`"a": {"type": "text"}, "l": {"mode": "live", "formula": "a", "values": ["x"]}`), CodeNotAllowed, "/variables/l/values"},
		{"a range", variables(`"a": {"type": "int"}, "l": {"mode": "live", "formula": "a", "min": 0}`), CodeNotAllowed, "/variables/l/min"},
		{"an upper bound", variables(`"a": {"type": "int"}, "l": {"type": "int", "mode": "live", "formula": "a", "max": 9}`), CodeNotAllowed, "/variables/l/max"},
		{"a default", variables(`"a": {"type": "int"}, "l": {"mode": "live", "formula": "a", "default": "1"}`), CodeNotAllowed, "/variables/l/default"},
		{"required", variables(`"a": {"type": "int"}, "l": {"mode": "live", "formula": "a", "required": true}`), CodeNotAllowed, "/variables/l/required"},
		{"read by an auto variable", variables(live + `, "c": {"formula": "l * 2"}`), CodeNotAllowed, "/variables/c/formula"},
		{"read by an auto variable with a type", variables(live + `, "c": {"type": "int", "formula": "l * 2"}`), CodeNotAllowed, "/variables/c/formula"},
		{"read by a triggered variable", variables(live + `, "c": {"type": "int", "formula": "l", "mode": "triggered", "on": ["refresh"]}`), CodeNotAllowed, "/variables/c/formula"},
		{"read by a default", variables(live + `, "c": {"type": "int", "default": "l"}`), CodeNotAllowed, "/variables/c/default"},
		{"read by another live variable", variables(live + `, "m": {"mode": "live", "formula": "l + 1"}`), CodeNotAllowed, "/variables/m/formula"},
		{"read by the title", `{"name": "T", "title": "TEXT(l)", "variables": {` + live + `}}`, CodeNotAllowed, "/title"},
		{"read by a guard", class(live, fmt.Sprintf(states, `, "guard": "l > 0"`)), CodeNotAllowed, "/transitions/0/guard"},
		{"read by an access rule", class(live, fmt.Sprintf(states, ``)+`, "access": {"read": "l > 0", "create": "TRUE", "update": "TRUE", "delete": "FALSE"}`), CodeNotAllowed, "/access/read"},
	}
	for _, tc := range cases {
		expectDiag(t, tc.name, tc.src, tc.code, tc.path)
	}
}
