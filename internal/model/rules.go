package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

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
	// Name is the action as written: set, reset, emit, or an action of the
	// outbox.
	Name string
	// Sync is true for set, reset and emit.
	Sync bool
	// Sets are the assignments of a set, in the order written.
	Sets []Assignment
	// Variables are the inputs a reset names, in the order written.
	Variables []*Variable
	// Event is the name the custom event of an emit carries.
	Event string
	// Raw is the action as written.
	Raw json.RawMessage
}

// Path is the action's place in the definition, "/rules/2/do/0".
func (a *Action) Path() string { return a.Rule.Path() + "/do/" + strconv.Itoa(a.Index) }

// Assignment is one variable of a set with the formula that gives its
// value.
type Assignment struct {
	Variable *Variable
	Formula  *formula.Program
	Src      string
}

// The names of the synchronous actions.
const (
	ActionSet     = "set"
	ActionReset   = "reset"
	ActionRefresh = "refresh"
	ActionEmit    = "emit"
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
				a.Sets = append(a.Sets, Assignment{Variable: v, Formula: prog, Src: formulas[name]})
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
	case ActionRefresh:
		c.hint(CodeNotSupported, path+"/action", "this build does not run the refresh action yet", "it arrives with the triggered variables, the next step of Phase 4")
		return nil
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
