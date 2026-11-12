package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Definition is a class as written in JSON (docs/architecture.md). Maps
// keep JSON's lack of order, so the order of declaration, which decides
// column order and messages, is recorded apart in VariableOrder and
// RightsOrder by Parse.
type Definition struct {
	Name        string `json:"name"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Plural      string `json:"plural,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Color       string `json:"color,omitempty"`
	// Title is a formula over pure functions and the document's own
	// variables (ADR-0018); empty means TITLE equals ID.
	Title       string                          `json:"title,omitempty"`
	Variables   map[string]*VariableDef         `json:"variables"`
	States      *StatesDef                      `json:"states,omitempty"`
	Transitions []TransitionDef                 `json:"transitions,omitempty"`
	Rules       []RuleDef                       `json:"rules,omitempty"`
	Access      *AccessDef                      `json:"access,omitempty"`
	Rights      map[string]map[string]*RightDef `json:"rights,omitempty"`
	// VariableOrder lists the variables in the order they were written.
	VariableOrder []string `json:"-"`
	// RightsOrder lists the states of the rights table, then per state its
	// variables, in the order they were written.
	RightsOrder  map[string][]string `json:"-"`
	rightsStates []string
}

// VariableDef is one variable as written.
type VariableDef struct {
	// Type is the type as written: a scalar name, int, decimal(s),
	// money(s), array<scalar>, object or rows. decimal and money without
	// a scale take Scale, or the default of two places.
	Type  string `json:"type"`
	Scale *int   `json:"scale,omitempty"`
	// Class names the class a ref points at.
	Class string `json:"class,omitempty"`
	// Schema is the schema of an object or rows variable: a schema name
	// as text, or for rows an inline object of column name to type.
	Schema json.RawMessage `json:"schema,omitempty"`
	// Input variables.
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
	// Computed variables (ADR-0015).
	Formula string   `json:"formula,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	On      []string `json:"on,omitempty"`
	// Values makes a text variable an enum (ADR-0023).
	Values []string `json:"values,omitempty"`
	// Min and Max bound a numeric variable (ADR-0034), as literals of its
	// type: a number, or text for an exact value.
	Min json.RawMessage `json:"min,omitempty"`
	Max json.RawMessage `json:"max,omitempty"`
	// Index asks for a B-tree index on the column.
	Index bool `json:"index,omitempty"`
	// schemaOrder is the column order of an inline rows schema.
	schemaOrder []string
}

// StatesDef is the statechart's states (ADR-0008).
type StatesDef struct {
	Initial string   `json:"initial"`
	List    []string `json:"list"`
	Final   []string `json:"final,omitempty"`
}

// TransitionDef is one transition. From is one state as text or a list
// of states. A transition without a name is eventless and needs a guard,
// unless it is timed (ADR-0047, item 9): After is a length of time in the
// state, a whole number and d, h, m or s, and At a formula that gives the
// moment, a date or a datetime. A transition carries one of them at most.
type TransitionDef struct {
	Name  string          `json:"name,omitempty"`
	From  json.RawMessage `json:"from"`
	To    string          `json:"to"`
	Guard string          `json:"guard,omitempty"`
	After string          `json:"after,omitempty"`
	At    string          `json:"at,omitempty"`
}

// RuleDef is one event-condition-action rule (ADR-0009). The actions are
// kept as written; Phase 4 gives them their meaning.
type RuleDef struct {
	On        string            `json:"on"`
	State     string            `json:"state,omitempty"`
	Name      string            `json:"name,omitempty"`
	Variables []string          `json:"variables,omitempty"`
	If        string            `json:"if,omitempty"`
	Do        []json.RawMessage `json:"do"`
}

// AccessDef holds the class-level access rules (ADR-0010).
type AccessDef struct {
	Read       string            `json:"read,omitempty"`
	Create     string            `json:"create,omitempty"`
	Update     string            `json:"update,omitempty"`
	Delete     string            `json:"delete,omitempty"`
	Transition map[string]string `json:"transition,omitempty"`
}

// RightDef is one cell of the rights table (ADR-0028): a formula as text
// or a list of group names, for reading and for writing.
type RightDef struct {
	Read  json.RawMessage `json:"read,omitempty"`
	Write json.RawMessage `json:"write,omitempty"`
}

// Parse decodes a class definition. Unknown properties are findings, so
// that a misspelt property never passes as an absent one.
func Parse(data []byte) (*Definition, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def Definition
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	var err error
	if raw, ok := top["variables"]; ok {
		if def.VariableOrder, err = orderedKeys(raw); err != nil {
			c.add(CodeBadJSON, "/variables", err.Error())
		}
	}
	for _, name := range def.VariableOrder {
		v := def.Variables[name]
		if v != nil && len(v.Schema) > 0 && bytes.HasPrefix(bytes.TrimSpace(v.Schema), []byte("{")) {
			if v.schemaOrder, err = orderedKeys(v.Schema); err != nil {
				c.add(CodeBadJSON, "/variables/"+name+"/schema", err.Error())
			}
		}
	}
	if raw, ok := top["rights"]; ok && len(def.Rights) > 0 {
		def.RightsOrder = map[string][]string{}
		if def.rightsStates, err = orderedKeys(raw); err != nil {
			c.add(CodeBadJSON, "/rights", err.Error())
		}
		var perState map[string]json.RawMessage
		if json.Unmarshal(raw, &perState) == nil {
			for _, st := range def.rightsStates {
				if def.RightsOrder[st], err = orderedKeys(perState[st]); err != nil {
					c.add(CodeBadJSON, "/rights/"+st, err.Error())
				}
			}
		}
	}
	return &def, c.list
}

// orderedKeys reads the keys of a JSON object in the order written.
func orderedKeys(raw json.RawMessage) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if t != json.Delim('{') {
		return nil, fmt.Errorf("want an object, got %v", t)
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// jsonMessage turns the decoder's error into a message without the
// package prefix.
func jsonMessage(err error) string {
	return strings.TrimPrefix(err.Error(), "json: ")
}

// fromStates reads a transition's from: one state or a list.
func fromStates(raw json.RawMessage) ([]string, error) {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("from must be a state or a list of states")
	}
	return many, nil
}
