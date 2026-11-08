package model

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// ChangeKind is what a redefinition does to one element of a class
// (ADR-0039).
type ChangeKind string

// The kinds of change. The store decides which are allowed against the
// documents it holds; the model only names them.
const (
	// ChangeAddVariable: a variable the old version lacked.
	ChangeAddVariable ChangeKind = "add_variable"
	// ChangeRemoveVariable: a variable the new version lacks.
	ChangeRemoveVariable ChangeKind = "remove_variable"
	// ChangeRetypeVariable: the same variable with another type, scale or
	// shape.
	ChangeRetypeVariable ChangeKind = "retype_variable"
	// ChangeFormula: another formula, default, mode or triggers, or the
	// class title.
	ChangeFormula ChangeKind = "change_formula"
	// ChangeConstraints: another required, values, min, max, index or ref
	// class.
	ChangeConstraints ChangeKind = "change_constraints"
	// ChangeRemoveState: a state the new version lacks.
	ChangeRemoveState ChangeKind = "remove_state"
	// ChangeUnfinalState: a state that was terminal and is not.
	ChangeUnfinalState ChangeKind = "unfinal_state"
	// ChangeBehaviour: states added or made terminal, transitions, rules,
	// access, rights, or the header.
	ChangeBehaviour ChangeKind = "change_behaviour"
)

// Change is one difference between two versions of a class.
type Change struct {
	Kind ChangeKind
	// Name is the variable or the state the change is about, "" for the
	// class as a whole.
	Name string
	// Path is the JSON pointer of the element in the new definition, or
	// in the old one for a removal.
	Path string
}

// Diff lists what a new version of a class changes against the old one,
// variables first in the new definition's order, then states, then the
// rest. Two identical definitions give no change.
func Diff(old, new *Class) []Change {
	var out []Change
	oldVars := map[string]*Variable{}
	for _, v := range old.Variables {
		oldVars[upper(v.Name)] = v
	}
	seen := map[string]bool{}
	for _, nv := range new.Variables {
		key := upper(nv.Name)
		seen[key] = true
		path := "/variables/" + nv.Name
		ov, ok := oldVars[key]
		if !ok {
			out = append(out, Change{Kind: ChangeAddVariable, Name: nv.Name, Path: path})
			continue
		}
		switch {
		case ov.Computed != nv.Computed || !ov.Type.Equal(nv.Type) || ov.RefClass != nv.RefClass && (ov.RefClass == "" || nv.RefClass == ""):
			out = append(out, Change{Kind: ChangeRetypeVariable, Name: nv.Name, Path: path + "/type"})
		case ov.Def.Formula != nv.Def.Formula || ov.Def.Default != nv.Def.Default || ov.Mode != nv.Mode || !reflect.DeepEqual(ov.Def.On, nv.Def.On):
			out = append(out, Change{Kind: ChangeFormula, Name: nv.Name, Path: path})
		case ov.Required != nv.Required || !reflect.DeepEqual(ov.Values, nv.Values) || ov.HasMin != nv.HasMin || ov.HasMax != nv.HasMax ||
			ov.HasMin && !sameValue(ov.Min, nv.Min) || ov.HasMax && !sameValue(ov.Max, nv.Max) || ov.Index != nv.Index || ov.RefClass != nv.RefClass:
			out = append(out, Change{Kind: ChangeConstraints, Name: nv.Name, Path: path})
		}
	}
	for _, ov := range old.Variables {
		if !seen[upper(ov.Name)] {
			out = append(out, Change{Kind: ChangeRemoveVariable, Name: ov.Name, Path: "/variables/" + ov.Name})
		}
	}
	if old.Title != nil || new.Title != nil {
		if old.Def.Title != new.Def.Title {
			out = append(out, Change{Kind: ChangeFormula, Name: "title", Path: "/title"})
		}
	}
	if old.States != nil {
		for _, st := range old.States.List {
			if new.States == nil || !new.States.Has(st) {
				out = append(out, Change{Kind: ChangeRemoveState, Name: st, Path: "/states/list"})
			}
		}
		for _, st := range old.States.Final {
			if new.States != nil && new.States.Has(st) && !new.States.IsFinal(st) {
				out = append(out, Change{Kind: ChangeUnfinalState, Name: st, Path: "/states/final"})
			}
		}
	}
	if !sameJSON(old.Def.States, new.Def.States) || !sameJSON(old.Def.Transitions, new.Def.Transitions) || !sameJSON(old.Def.Rules, new.Def.Rules) ||
		!sameJSON(old.Def.Access, new.Def.Access) || !sameJSON(old.Def.Rights, new.Def.Rights) ||
		old.Label != new.Label || old.Plural != new.Plural || old.Description != new.Description || old.Icon != new.Icon || old.Color != new.Color {
		out = append(out, Change{Kind: ChangeBehaviour, Path: ""})
	}
	return out
}

// IsFinal reports whether the state is terminal (ADR-0039).
func (s *States) IsFinal(state string) bool {
	if s == nil {
		return false
	}
	for _, st := range s.Final {
		if strings.EqualFold(st, state) {
			return true
		}
	}
	return false
}

func sameJSON(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// sameValue compares two bounds as values.
func sameValue(a, b formula.Value) bool { return formula.Equal(a, b) }
