package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// ActionDef is an action definition as written (ADR-0045, item 1): the
// contract of an action a rule may name. It has a name of a namespace and
// a verb, its parameters and what it returns, each by scalar type, and
// what implements it: nothing for a Go function the host program
// registers under the name, or a request the engine sends.
type ActionDef struct {
	Name        string `json:"name"`
	Version     int    `json:"version,omitempty"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	// Params and Returns map a name to its type as text, or to an object
	// of type, required and description.
	Params  map[string]json.RawMessage `json:"params,omitempty"`
	Returns map[string]json.RawMessage `json:"returns,omitempty"`
	// Connection, Request, OK and Result make a declared action, which the
	// engine sends as an HTTP request. They are read so that a definition
	// that has them is told why it is refused; this build sends nothing.
	Connection json.RawMessage `json:"connection,omitempty"`
	Request    json.RawMessage `json:"request,omitempty"`
	OK         json.RawMessage `json:"ok,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	// ParamOrder and ReturnOrder list the names in the order written.
	ParamOrder  []string `json:"-"`
	ReturnOrder []string `json:"-"`
}

// ParamDef is one parameter, or one returned value, written in full.
type ParamDef struct {
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
}

// ActionSpec is a compiled action definition.
type ActionSpec struct {
	Name        string
	Version     int
	Label       string
	Description string
	// Params and Returns are in the order written.
	Params  []Param
	Returns []Param
	Def     *ActionDef
}

// Param is one parameter of an action, or one value it returns: a name, a
// scalar type, whether a call must give it, and what it is for.
type Param struct {
	Name        string
	Type        formula.Type
	Required    bool
	Description string
}

// Param finds a parameter by name, case-insensitively.
func (a *ActionSpec) Param(name string) (*Param, bool) { return findParam(a.Params, name) }

// Return finds a returned value by name, case-insensitively.
func (a *ActionSpec) Return(name string) (*Param, bool) { return findParam(a.Returns, name) }

func findParam(list []Param, name string) (*Param, bool) {
	for i := range list {
		if strings.EqualFold(list[i].Name, name) {
			return &list[i], true
		}
	}
	return nil, false
}

// MaxActionName is the longest name an action may have.
const MaxActionName = 100

// engineActions are the actions the engine implements, by name, with
// whether each runs inside the commit.
var engineActions = map[string]bool{
	ActionSet: true, ActionReset: true, ActionRefresh: true, ActionEmit: true,
	ActionCreate: false, ActionTransition: false,
	ActionTableInsert: false, ActionTableUpdate: false, ActionTableDelete: false,
}

// EngineAction reports whether the engine implements the action of that
// name itself. Every other action a rule names has a definition.
func EngineAction(name string) bool {
	_, ok := engineActions[name]
	return ok
}

// ActionHTTPCall is the declared action the engine defines for a call
// nobody named (ADR-0045, item 3). It arrives with the declared actions;
// until then a rule that names it is kept as written and not run.
const ActionHTTPCall = "http.call"

// ParseAction decodes an action definition. Unknown properties are
// findings.
func ParseAction(data []byte) (*ActionDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def ActionDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	var err error
	if def.Params != nil {
		if def.ParamOrder, err = orderedKeys(mustField(data, "params")); err != nil {
			c.add(CodeBadJSON, "/params", err.Error())
		}
	}
	if def.Returns != nil {
		if def.ReturnOrder, err = orderedKeys(mustField(data, "returns")); err != nil {
			c.add(CodeBadJSON, "/returns", err.Error())
		}
	}
	return &def, c.list
}

// mustField is one property of a JSON object that has decoded already.
func mustField(data []byte, name string) json.RawMessage {
	var top map[string]json.RawMessage
	_ = json.Unmarshal(data, &top)
	return top[name]
}

// CompileAction checks an action definition and compiles it.
func CompileAction(def *ActionDef) (*ActionSpec, Diagnostics) {
	c := &collector{}
	a := &ActionSpec{Name: def.Name, Version: def.Version, Label: def.Label, Description: def.Description, Def: def}
	if code, reason := actionNameReason(def.Name); reason != "" {
		c.hint(code, "/name", reason+": "+quote(def.Name), `a namespace and a verb joined by a dot, such as "slack.post"`)
	}
	if a.Version == 0 {
		a.Version = 1
	} else if a.Version < 0 {
		c.add(CodeBadValue, "/version", "the version is a positive number")
	}
	a.Params = compileParams(c, "/params", def.Params, def.ParamOrder, true)
	a.Returns = compileParams(c, "/returns", def.Returns, def.ReturnOrder, false)
	// A declared action is sent by the engine; this build sends nothing.
	for _, part := range []struct {
		name string
		raw  json.RawMessage
	}{{"request", def.Request}, {"connection", def.Connection}, {"ok", def.OK}, {"result", def.Result}} {
		if len(part.raw) > 0 {
			c.hint(CodeNotSupported, "/"+part.name, "this build sends no requests: an action declared with a request arrives with a later step of Phase 4 (ADR-0045)",
				"without request, connection, ok and result a definition is the contract of a Go function the host program registers under its name")
		}
	}
	if c.failed() {
		return nil, c.list
	}
	return a, nil
}

// DefineAction parses and compiles an action definition in one step.
func DefineAction(data []byte) (*ActionSpec, Diagnostics) {
	def, diags := ParseAction(data)
	if def == nil {
		return nil, diags
	}
	a, more := CompileAction(def)
	return a, append(diags, more...)
}

// compileParams reads the parameters of an action, or with params false
// what it returns: each a name and a scalar type.
func compileParams(c *collector, path string, defs map[string]json.RawMessage, order []string, params bool) []Param {
	what := "a parameter"
	if !params {
		what = "a returned value"
	}
	var out []Param
	seen := map[string]bool{}
	for _, name := range order {
		ppath := path + "/" + name
		if !c.checkName(ppath, name, nil) {
			continue
		}
		if seen[upper(name)] {
			c.addf(CodeDuplicate, ppath, "%s is declared twice; names differ in case only", quote(name))
			continue
		}
		seen[upper(name)] = true
		raw := bytes.TrimSpace(defs[name])
		var pd ParamDef
		switch {
		case len(raw) > 0 && raw[0] == '"':
			_ = json.Unmarshal(raw, &pd.Type)
		default:
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&pd); err != nil {
				c.hint(CodeBadJSON, ppath, jsonMessage(err), what+` is its type as text, or an object: {"type": "text", "required": true, "description": "..."}`)
				continue
			}
		}
		if pd.Type == "" {
			c.hint(CodeMissing, ppath+"/type", what+" has a type", "one of bool, number, int, decimal(s), money(s), text, date, datetime, ref, json")
			continue
		}
		t, err := formula.ParseType(pd.Type)
		switch {
		case err != nil:
			c.add(CodeUnknownType, ppath, err.Error())
			continue
		case t.Kind == formula.KindNull:
			c.add(CodeUnknownType, ppath, what+" is never blank by type")
			continue
		case !t.IsScalar():
			c.hint(CodeNotAllowed, ppath, what+" is a scalar; "+pd.Type+" is a composite", "a json parameter carries a structure")
			continue
		}
		if pd.Required && !params {
			c.hint(CodeNotAllowed, ppath+"/required", "required belongs to a parameter; what an action returns it may leave blank", "drop it")
			continue
		}
		out = append(out, Param{Name: name, Type: t, Required: pd.Required, Description: pd.Description})
	}
	return out
}

// actionNameReason says why a text is not the name of a defined action,
// with the code of the finding, or "" when it is: identifiers joined by
// dots, a namespace before a verb. A name without a dot, and the
// namespaces table and http, are the engine's (ADR-0045, item 1).
func actionNameReason(name string) (code, reason string) {
	if name == "" {
		return CodeMissing, "an action has a name"
	}
	if len(name) > MaxActionName {
		return CodeBadName, fmt.Sprintf("an action name has at most %d characters", MaxActionName)
	}
	if r := strings.Replace(eventNameReason(name), "an event name", "an action name", 1); r != "" {
		return CodeBadName, r
	}
	namespace, _, dotted := strings.Cut(name, ".")
	switch {
	case !dotted:
		return CodeReservedName, "a name without a dot is the engine's, as set and create are"
	case strings.EqualFold(namespace, "table") || strings.EqualFold(namespace, "http"):
		return CodeReservedName, "the namespace " + strings.ToLower(namespace) + " is the engine's"
	}
	return "", ""
}
