package model

import (
	"bytes"
	"encoding/json"
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
	return &def, nil
}
