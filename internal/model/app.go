package model

import (
	"bytes"
	"encoding/json"
	"strings"
)

// AppDef is an app as written in JSON (ADR-0077): a name for the classes
// that belong together, such as those of Expenses or of a help desk,
// with what the app says of itself to people. A class names its app; the
// app does not list its classes.
type AppDef struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Icon and Color are as in the header of a class: a face's to read,
	// and nothing to the engine.
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
	// Roles are the parts people play in the app, by name (ADR-0100): a
	// formula of a class of the app asks for one with INROLE, and each
	// server says who holds it. RoleOrder lists them as written.
	Roles     map[string]*RoleDef `json:"roles,omitempty"`
	RoleOrder []string            `json:"-"`
}

// RoleDef is one role of an app as written: what people read in its
// place and what it is for. Who holds it is not the app's to say.
type RoleDef struct {
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// RoleNames lists the roles of the app in the order they were written;
// an empty list for an app that names none.
func (a *AppDef) RoleNames() []string {
	if a == nil {
		return []string{}
	}
	return append([]string{}, a.RoleOrder...)
}

// HasRole says whether the app names the role, whatever the case of the
// name (ADR-0013).
func (a *AppDef) HasRole(name string) bool {
	if a == nil {
		return false
	}
	for _, r := range a.RoleOrder {
		if strings.EqualFold(r, name) {
			return true
		}
	}
	return false
}

// ParseApp decodes the definition of an app and checks it. Unknown
// properties are findings, so that a misspelt property never passes as
// an absent one.
func ParseApp(data []byte) (*AppDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def AppDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	if !c.checkName("/name", def.Name, nil) {
		return nil, c.list
	}
	if len(def.Roles) > 0 {
		var top map[string]json.RawMessage
		if err := json.Unmarshal(data, &top); err == nil {
			def.RoleOrder, _ = orderedKeys(top["roles"])
		}
		for _, name := range def.RoleOrder {
			path := "/roles/" + name
			if def.Roles[name] == nil {
				c.add(CodeMissing, path, "a role is an object, with a label and a description, such as {\"label\": \"Approver\"}")
				continue
			}
			c.checkName(path, name, nil)
		}
		if c.failed() {
			return nil, c.list
		}
	}
	return &def, nil
}
