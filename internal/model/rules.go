package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Rule is one compiled event-condition-action rule (ADR-0009, ADR-0043):
// the trigger, the condition bound, and the actions compiled.
type Rule struct {
	// Index is the rule's position in the definition.
	Index int
	// On is the trigger: create, values_change, exit, enter, transition or
	// delete. State belongs to enter and exit, Name to transition and
	// Variables to values_change.
	On        string
	State     string
	Name      string
	Variables []string
	// If is the condition, nil when the rule always runs.
	If      *formula.Program
	Actions []*Action
}

// Path is the rule's place in the definition, "/rules/2".
func (r *Rule) Path() string { return "/rules/" + strconv.Itoa(r.Index) }

// Action is one compiled action of a rule. A synchronous action changes
// the commit's own document before it is written (ADR-0043, item 4); any
// other belongs to the outbox and is kept as written until the step of
// Phase 4 that queues it.
type Action struct {
	// Rule and Index place the action: "/rules/2/do/0".
	Rule  *Rule
	Index int
	// Name is the action as written: set, reset, refresh, emit, or an
	// action of the outbox.
	Name string
	// Sync is true for set, reset, emit and a refresh of the rule's own
	// document; a refresh that names another document belongs to the
	// outbox (ADR-0044).
	Sync bool
	// Queued is true for an action of the outbox the engine runs itself
	// (ADR-0044): the commit evaluates its arguments and writes a row.
	// An action that is neither Sync nor Queued is kept as written.
	Queued bool
	// Target is the class a create makes a document of, by its declared
	// name, and ID the formula of the new document's id, nil when the
	// engine gives one.
	Target string
	ID     *formula.Program
	// Sets are the assignments of a set, or the values of a create, in
	// the order written.
	Sets []Assignment
	// Variables are the inputs a reset names, in the order written, or the
	// triggered variables a refresh recomputes, in declaration order.
	Variables []*Variable
	// Event is the name the custom event of an emit carries.
	Event string
	// Raw is the action as written.
	Raw json.RawMessage
}

// Path is the action's place in the definition, "/rules/2/do/0".
func (a *Action) Path() string { return a.Rule.Path() + "/do/" + strconv.Itoa(a.Index) }

// Assignment is one variable of a set, or of the document a create
// makes, with the formula that gives its value. Name is the variable as
// its class declares it; Variable is nil when a stored definition names
// one the target class no longer has.
type Assignment struct {
	Name     string
	Variable *Variable
	Formula  *formula.Program
	Src      string
}

// The names of the synchronous actions, and of the outbox actions the
// engine runs itself.
const (
	ActionSet     = "set"
	ActionReset   = "reset"
	ActionRefresh = "refresh"
	ActionEmit    = "emit"
	ActionCreate  = "create"
)

var ruleTriggers = map[string]bool{"create": true, "values_change": true, "exit": true, "enter": true, "transition": true, "delete": true, "schedule": true}

// MaxEventName is the longest name a custom event may carry.
const MaxEventName = 100

// compileRules checks the rules, binds their conditions and compiles
// their actions (ADR-0043).
func compileRules(c *collector, cls *Class, defs []RuleDef, scope *formula.Scope) {
	if len(defs) > 0 && cls.States == nil {
		c.hint(CodeNotAllowed, "/rules", "a class without states stores no documents, so no rule of it would ever run (ADR-0040)", "add a states block, or drop the rules")
	}
	for i, d := range defs {
		path := fmt.Sprintf("/rules/%d", i)
		r := &Rule{Index: i, On: d.On, State: d.State, Name: d.Name, Variables: d.Variables}
		if !ruleTriggers[d.On] {
			c.hint(CodeBadValue, path+"/on", "unknown trigger "+quote(d.On), "one of create, values_change, exit, enter, transition, delete")
		}
		switch d.On {
		case "enter", "exit":
			if d.State == "" {
				c.addf(CodeMissing, path+"/state", "a rule on %s names the state", d.On)
			} else if !cls.States.Has(d.State) {
				c.addf(CodeUnknownState, path+"/state", "unknown state %s", quote(d.State))
			}
		case "transition":
			if d.Name == "" {
				c.add(CodeMissing, path+"/name", "a rule on transition names the transition")
			} else if !hasTransition(cls, d.Name) {
				c.addf(CodeUnknownTransition, path+"/name", "unknown transition %s", quote(d.Name))
			}
		case "values_change":
			if len(d.Variables) == 0 {
				c.add(CodeMissing, path+"/variables", "a rule on values_change names the variables")
			}
			for j, name := range d.Variables {
				if _, ok := cls.Variable(name); !ok {
					c.addf(CodeUnknownVariable, fmt.Sprintf("%s/variables/%d", path, j), "unknown variable %s", quote(name))
				}
			}
		case "schedule":
			// Accepted and never fired until ADR-0043; refused until the
			// phase that runs it, as a binding a build lacks is.
			c.hint(CodeNotSupported, path+"/on", "this build does not run rules on a schedule", "send the command when the time comes; schedules arrive with the set-wise work of a later phase")
		}
		r.If = bindCondition(c, path+"/if", d.If, scope, "a rule condition")
		if len(d.Do) == 0 {
			c.add(CodeMissing, path+"/do", "a rule has at least one action")
		}
		frozen := frozenTrigger(cls, d)
		for j, raw := range d.Do {
			apath := fmt.Sprintf("%s/do/%d", path, j)
			a := compileAction(c, apath, raw, cls, scope)
			if a == nil {
				continue
			}
			a.Rule, a.Index = r, j
			if frozen != "" && a.Sync && a.Name != ActionEmit {
				c.hint(CodeNotAllowed, apath, "a "+a.Name+" cannot run here: "+frozen+", and a frozen document computes nothing (ADR-0039)",
					"hang the rule on the transition that arrives from a live state; emit and the actions of the outbox may stay")
			}
			r.Actions = append(r.Actions, a)
		}
		cls.Rules = append(cls.Rules, r)
	}
}

// frozenTrigger says why a rule's trigger may fire over a document that
// computes nothing, or "" when it cannot (ADR-0043, item 8): a move out of
// a terminal state raises the exit of that state, the transition taken
// and the entry of its target; a delete leaves no document to change.
func frozenTrigger(cls *Class, d RuleDef) string {
	switch d.On {
	case "delete":
		return "a rule on delete sees the document as it was"
	case "exit":
		if cls.States.IsFinal(d.State) {
			return quote(d.State) + " is a terminal state"
		}
	case "transition":
		for _, t := range cls.Transitions {
			if t.Name != d.Name {
				continue
			}
			for _, from := range t.From {
				if cls.States.IsFinal(from) {
					return "the transition leaves the terminal state " + quote(from)
				}
			}
		}
	case "enter":
		for _, t := range cls.Transitions {
			if t.To != d.State {
				continue
			}
			for _, from := range t.From {
				if cls.States.IsFinal(from) {
					return "the terminal state " + quote(from) + " leads to " + quote(d.State)
				}
			}
		}
	}
	return ""
}

// compileAction reads one action: the three synchronous ones in full, any
// other by its name alone.
func compileAction(c *collector, path string, raw json.RawMessage, cls *Class, scope *formula.Scope) *Action {
	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Action == "" {
		c.add(CodeMissing, path+"/action", "an action names what it does")
		return nil
	}
	a := &Action{Name: head.Action, Raw: raw}
	switch head.Action {
	case ActionSet:
		a.Sync = true
		var def struct {
			Action string          `json:"action"`
			Values json.RawMessage `json:"values"`
		}
		if !decodeAction(c, path, raw, &def) {
			return nil
		}
		names, err := orderedKeys(def.Values)
		if len(def.Values) == 0 || err != nil || len(names) == 0 {
			c.hint(CodeMissing, path+"/values", "a set names the variables it assigns, each with its formula", `"values": {"approved_at": "NOW()"}`)
			return nil
		}
		var formulas map[string]string
		if err := json.Unmarshal(def.Values, &formulas); err != nil {
			c.hint(CodeBadValue, path+"/values", "every value of a set is a formula, written as text", `a literal text is "\"paid\"", a number "0"`)
			return nil
		}
		seen := map[string]bool{}
		for _, name := range names {
			vpath := path + "/values/" + name
			v, ok := cls.Variable(name)
			switch {
			case !ok:
				c.addf(CodeUnknownVariable, vpath, "unknown variable %s", quote(name))
				continue
			case seen[upper(name)]:
				c.addf(CodeDuplicate, vpath, "%s is assigned twice; names differ in case only", quote(name))
				continue
			case v.Computed:
				c.hint(CodeNotAllowed, vpath, "a rule sets input variables; "+v.Name+" is computed", "its formula gives its value")
				continue
			case v.Type.Kind == formula.KindRows || v.Type.Kind == formula.KindArray || v.Type.Kind == formula.KindObject:
				c.hint(CodeNotSupported, vpath, "a rule cannot set "+v.Name+": no formula gives "+article(v.Type.String())+" in this build", "set scalar variables")
				continue
			}
			seen[upper(name)] = true
			if prog := bindAs(c, vpath, formulas[name], scope, formula.ModeAny, v.Type); prog != nil {
				a.Sets = append(a.Sets, Assignment{Name: v.Name, Variable: v, Formula: prog, Src: formulas[name]})
			}
		}
	case ActionReset:
		a.Sync = true
		var def struct {
			Action    string   `json:"action"`
			Variables []string `json:"variables"`
		}
		if !decodeAction(c, path, raw, &def) {
			return nil
		}
		if len(def.Variables) == 0 {
			c.hint(CodeMissing, path+"/variables", "a reset names the input variables it resets", `"variables": ["discount"]`)
		}
		seen := map[string]bool{}
		for k, name := range def.Variables {
			vpath := fmt.Sprintf("%s/variables/%d", path, k)
			v, ok := cls.Variable(name)
			switch {
			case !ok:
				c.addf(CodeUnknownVariable, vpath, "unknown variable %s", quote(name))
				continue
			case seen[upper(name)]:
				c.addf(CodeDuplicate, vpath, "%s is named twice", quote(name))
				continue
			case v.Computed:
				c.hint(CodeNotAllowed, vpath, "a reset re-evaluates the defaults of input variables; "+v.Name+" is computed", "its formula gives its value")
				continue
			}
			seen[upper(name)] = true
			a.Variables = append(a.Variables, v)
		}
	case ActionEmit:
		a.Sync = true
		var def struct {
			Action string `json:"action"`
			Event  string `json:"event"`
		}
		if !decodeAction(c, path, raw, &def) {
			return nil
		}
		if def.Event == "" {
			c.hint(CodeMissing, path+"/event", "an emit names its event", `"event": "invoice.flagged"`)
		} else if reason := eventNameReason(def.Event); reason != "" {
			c.add(CodeBadName, path+"/event", reason+": "+quote(def.Event))
		}
		a.Event = def.Event
	case ActionCreate:
		return compileCreate(c, path, raw, cls, scope, a)
	case ActionRefresh:
		// A refresh that carries an id names another document and belongs
		// to the outbox (ADR-0044, item 13); it is kept as written.
		var other struct {
			Class json.RawMessage `json:"class"`
			ID    json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &other)
		if len(other.ID) > 0 {
			return a
		}
		if len(other.Class) > 0 {
			c.hint(CodeMissing, path+"/id", "a refresh that names a class names the document too", `"id" is a formula that gives the document's id; without class and id a refresh recomputes the rule's own document`)
			return nil
		}
		a.Sync = true
		var def struct {
			Action    string   `json:"action"`
			Variables []string `json:"variables"`
		}
		if !decodeAction(c, path, raw, &def) {
			return nil
		}
		named := map[string]bool{}
		for k, name := range def.Variables {
			vpath := fmt.Sprintf("%s/variables/%d", path, k)
			v, ok := cls.Variable(name)
			switch {
			case !ok:
				c.addf(CodeUnknownVariable, vpath, "unknown variable %s", quote(name))
			case named[upper(name)]:
				c.addf(CodeDuplicate, vpath, "%s is named twice", quote(name))
			case !v.Triggered():
				c.hint(CodeNotAllowed, vpath, "a refresh recomputes triggered variables; "+v.Name+" is not one", `a triggered variable has "mode": "triggered" and lists its triggers under "on"`)
			case !v.On("refresh", ""):
				c.hint(CodeNotAllowed, vpath, v.Name+" does not list refresh among its triggers", `add "refresh" to its "on"`)
			default:
				named[upper(name)] = true
			}
		}
		// In declaration order, which is the order they recompute in; with
		// no variable named, every one that lists refresh.
		for _, v := range cls.Variables {
			if v.Triggered() && v.On("refresh", "") && (len(def.Variables) == 0 || named[upper(v.Name)]) {
				a.Variables = append(a.Variables, v)
			}
		}
		if len(def.Variables) == 0 && len(a.Variables) == 0 {
			c.hint(CodeNotAllowed, path, "no variable of the class lists refresh among its triggers, so the action would recompute nothing", `give a triggered variable "on": ["refresh"], or drop the action`)
		}
	}
	return a
}

// compileCreate reads a create (ADR-0044, item 7): the class is a name,
// the values and the id are formulas over the document that fires the
// rule, typed against the variables of the class they fill. The class and
// its variables are checked now, when the rule's class is defined.
func compileCreate(c *collector, path string, raw json.RawMessage, cls *Class, scope *formula.Scope, a *Action) *Action {
	var def struct {
		Action string          `json:"action"`
		Class  string          `json:"class"`
		ID     string          `json:"id"`
		Values json.RawMessage `json:"values"`
		Each   json.RawMessage `json:"each"`
		As     json.RawMessage `json:"as"`
	}
	if !decodeAction(c, path, raw, &def) {
		return nil
	}
	a.Queued = true
	if len(def.Each) > 0 || len(def.As) > 0 {
		c.hint(CodeNotSupported, path+"/each", "this build does not run each on a create yet", "it arrives with the next step of Phase 4; until then a rule creates one document")
	}
	if def.Class == "" {
		c.hint(CodeMissing, path+"/class", "a create names the class of the document it makes", `"class": "Receipt"`)
		return nil
	}
	// The class under definition may create documents of itself.
	target, ok := cls, upper(def.Class) == upper(cls.Name)
	if !ok && c.env.Classes != nil {
		target, ok = c.env.Classes(def.Class)
	}
	lenient := c.env.Lenient
	a.Target = def.Class
	switch {
	case ok:
		a.Target = target.Name
		if target.States == nil && !lenient {
			c.hint(CodeNotAllowed, path+"/class", target.Name+" declares no states: it is a stateless application and stores no documents (ADR-0040)", "create documents of a class that has a states block")
		}
	case !lenient:
		c.hint(CodeUnknownClass, path+"/class", "unknown class "+quote(def.Class), "define it before the class whose rule creates its documents")
		return nil
	}
	var names []string
	formulas := map[string]string{}
	if len(def.Values) > 0 {
		var err error
		if names, err = orderedKeys(def.Values); err != nil {
			c.add(CodeBadValue, path+"/values", "the values of a create are an object of formulas by variable")
			return nil
		}
		if err := json.Unmarshal(def.Values, &formulas); err != nil {
			c.hint(CodeBadValue, path+"/values", "every value of a create is a formula, written as text", `a literal text is "\"new\"", a number "0"`)
			return nil
		}
	}
	text := formula.Scalar(formula.KindText)
	// untyped binds a formula whose variable the environment does not
	// have, for a stored definition.
	untyped := func(vpath, src string) *formula.Program {
		prog, err := c.bind(src, scope, formula.ModeAny)
		if err != nil {
			c.formulaErr(vpath, err)
			return nil
		}
		return prog
	}
	given := map[string]bool{}
	for _, name := range names {
		vpath := path + "/values/" + name
		if given[upper(name)] {
			c.addf(CodeDuplicate, vpath, "%s is given twice; names differ in case only", quote(name))
			continue
		}
		given[upper(name)] = true
		var v *Variable
		if ok {
			v, _ = target.Variable(name)
		}
		composite := v != nil && (v.Type.Kind == formula.KindRows || v.Type.Kind == formula.KindArray || v.Type.Kind == formula.KindObject)
		if lenient && (v == nil || v.Computed || composite) {
			if prog := untyped(vpath, formulas[name]); prog != nil {
				a.Sets = append(a.Sets, Assignment{Name: name, Formula: prog, Src: formulas[name]})
			}
			continue
		}
		switch {
		case v == nil:
			c.addf(CodeUnknownVariable, vpath, "the class %s has no variable %s", target.Name, quote(name))
			continue
		case v.Computed:
			c.hint(CodeNotAllowed, vpath, "a create gives input variables; "+v.Name+" of "+target.Name+" is computed", "its formula gives its value")
			continue
		case composite:
			c.hint(CodeNotSupported, vpath, "a rule cannot give "+v.Name+": no formula gives "+article(v.Type.String())+" in this build", "give scalar variables")
			continue
		}
		var prog *formula.Program
		if lenient {
			// A type that no longer fits is found when the action runs.
			var probe collector
			probe.env = c.env
			if prog = bindAs(&probe, vpath, formulas[name], scope, formula.ModeAny, v.Type); prog == nil {
				prog = untyped(vpath, formulas[name])
			}
		} else {
			prog = bindAs(c, vpath, formulas[name], scope, formula.ModeAny, v.Type)
		}
		if prog != nil {
			a.Sets = append(a.Sets, Assignment{Name: v.Name, Variable: v, Formula: prog, Src: formulas[name]})
		}
	}
	// A required input the create does not give and the class does not
	// default would refuse every document: say so now.
	if ok && !lenient {
		var missing []string
		for _, v := range target.Variables {
			if !v.Computed && v.Required && v.Default == nil && !given[upper(v.Name)] && v.Type.IsScalar() {
				missing = append(missing, v.Name)
			}
		}
		if len(missing) > 0 {
			c.hint(CodeMissing, path+"/values", "the create gives no value for "+strings.Join(missing, ", ")+", which "+target.Name+" requires and has no default for", "give each a formula under values")
		}
	}
	if def.ID != "" {
		if lenient {
			a.ID = untyped(path+"/id", def.ID)
		} else {
			a.ID = bindAs(c, path+"/id", def.ID, scope, formula.ModeAny, text)
		}
	}
	return a
}

// decodeAction reads an action whose properties the engine knows, so that
// a misspelt one is a finding and never passes as an absent one.
func decodeAction(c *collector, path string, raw json.RawMessage, into any) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		c.add(CodeBadJSON, path, jsonMessage(err))
		return false
	}
	return true
}

// eventNameReason says why a text is not the name of a custom event, or
// "" when it is: identifiers joined by dots, such as invoice.flagged. It
// is a name and never a formula, so that a subscription and a mining
// query can match it (ADR-0043).
func eventNameReason(name string) string {
	if len(name) > MaxEventName {
		return fmt.Sprintf("an event name has at most %d characters", MaxEventName)
	}
	start := true
	for _, r := range name {
		switch {
		case r == '.':
			if start {
				return "an event name is identifiers joined by single dots"
			}
			start = true
			continue
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if start {
				return "a part of an event name may not start with a digit"
			}
		default:
			return "an event name is letters, digits, underscores and dots"
		}
		start = false
	}
	if start {
		return "an event name is identifiers joined by single dots"
	}
	return ""
}

// article puts "a" or "an" before a type name.
func article(noun string) string {
	if noun != "" && bytes.IndexByte([]byte("aeiou"), noun[0]) >= 0 {
		return "an " + noun
	}
	return "a " + noun
}

func hasTransition(cls *Class, name string) bool {
	for _, t := range cls.Def.Transitions {
		if t.Name == name {
			return true
		}
	}
	return false
}
