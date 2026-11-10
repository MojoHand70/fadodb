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
// the commit's own document before it is written (ADR-0043, item 4); an
// action of the outbox the engine runs itself is queued by the commit
// with its evaluated arguments (ADR-0044, item 7); any other is kept as
// written until the step of Phase 4 that runs it.
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
	// (ADR-0044): create, transition, a refresh of another document and
	// the table actions. The commit evaluates its arguments and writes a
	// row. An action that is neither Sync nor Queued is kept as written.
	Queued bool
	// Target is what a queued action acts on, by its declared name: the
	// class a create makes a document of, the class of the document a
	// transition moves or a refresh recomputes, the table a table action
	// writes.
	Target string
	// ID is the formula of the document's id: of the new document of a
	// create, nil when the engine gives one, and of the document a
	// transition or a refresh of another document acts on.
	ID *formula.Program
	// Transition is the named transition a transition action takes.
	Transition string
	// Sets are the assignments of a set, the values of a create or a
	// transition, the row of a table.insert or the columns a table.update
	// changes, in the order written.
	Sets []Assignment
	// Keys are the key columns of the row a table.update or a
	// table.delete addresses, in the order written.
	Keys []Assignment
	// Variables are the inputs a reset names, in the order written, or the
	// triggered variables a refresh of the rule's own document recomputes,
	// in declaration order.
	Variables []*Variable
	// Names are the triggered variables a refresh of another document
	// names, as that class declares them; none means every one that lists
	// refresh.
	Names []string
	// Each is the array or rows variable a create makes one document for
	// each element of, nil for one document, and As the name its formulas
	// read the element by (ADR-0044, item 12).
	Each *Variable
	As   string
	// Event is the name the custom event of an emit carries.
	Event string
	// Raw is the action as written.
	Raw json.RawMessage
}

// Path is the action's place in the definition, "/rules/2/do/0".
func (a *Action) Path() string { return a.Rule.Path() + "/do/" + strconv.Itoa(a.Index) }

// Assignment is one variable of a set, of the document a create makes or
// a transition moves, or one column of a table action, with the formula
// that gives its value. Name is the variable or the column as its class
// or table declares it; Variable is nil for a column, and when a stored
// definition names a variable the target class no longer has.
type Assignment struct {
	Name     string
	Variable *Variable
	Formula  *formula.Program
	Src      string
}

// The names of the synchronous actions, and of the outbox actions the
// engine runs itself.
const (
	ActionSet         = "set"
	ActionReset       = "reset"
	ActionRefresh     = "refresh"
	ActionEmit        = "emit"
	ActionCreate      = "create"
	ActionTransition  = "transition"
	ActionTableInsert = "table.insert"
	ActionTableUpdate = "table.update"
	ActionTableDelete = "table.delete"
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

// compileAction reads one action: the synchronous ones and the outbox
// actions the engine runs itself in full, any other by its name alone.
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
	case ActionTransition:
		return compileMove(c, path, raw, cls, scope, a)
	case ActionTableInsert, ActionTableUpdate, ActionTableDelete:
		return compileTableAction(c, path, raw, scope, a)
	case ActionRefresh:
		// A refresh that carries an id names another document and belongs
		// to the outbox (ADR-0044, item 13).
		var other struct {
			Class json.RawMessage `json:"class"`
			ID    json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &other)
		if len(other.ID) > 0 {
			return compileRefreshOther(c, path, raw, cls, scope, a)
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

// The outbox actions the engine runs itself (ADR-0044, item 7). The
// class, the table and the transition are names; the id and everything
// under values, row, key and set are formulas over the document that
// fires the rule, typed against what they fill. What an action names is
// checked now, when the rule's class is defined.

// compileCreate reads a create: one document of the class, or one for
// each element of a list when it carries each (ADR-0044, item 12).
func compileCreate(c *collector, path string, raw json.RawMessage, cls *Class, scope *formula.Scope, a *Action) *Action {
	var def struct {
		Action string          `json:"action"`
		Class  string          `json:"class"`
		ID     string          `json:"id"`
		Values json.RawMessage `json:"values"`
		Each   *string         `json:"each"`
		As     *string         `json:"as"`
	}
	if !decodeAction(c, path, raw, &def) {
		return nil
	}
	a.Queued = true
	if def.Each != nil || def.As != nil {
		if scope = eachScope(c, path, cls, def.Each, def.As, a); scope == nil {
			return nil
		}
	}
	if def.Class == "" {
		c.hint(CodeMissing, path+"/class", "a create names the class of the document it makes", `"class": "Receipt"`)
		return nil
	}
	target, ok := targetClass(c, path, def.Class, cls, "define it before the class whose rule creates its documents")
	if !ok && !c.env.Lenient {
		return nil
	}
	a.Target = targetName(def.Class, target)
	sets, given, read := fillValues(c, path+"/values", def.Values, target, scope, "create")
	if !read {
		return nil
	}
	a.Sets = sets
	// A required input the create does not give and the class does not
	// default would refuse every document: say so now.
	if ok && !c.env.Lenient {
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
		a.ID = bindFill(c, path+"/id", def.ID, scope, &textType)
	}
	return a
}

// eachScope reads the each and the as of a create and returns the scope
// its formulas bind in: the variables of the class, and the element of
// the list under the name as gives it. An element of an array is a value
// of the array's type; an element of a rows variable is an object of its
// schema, read by field (ADR-0031).
func eachScope(c *collector, path string, cls *Class, each, as *string, a *Action) *formula.Scope {
	if each == nil || *each == "" {
		c.hint(CodeMissing, path+"/each", "as names the element of a list, and the create names no list", `"each": "approvers", "as": "approver"`)
		return nil
	}
	v, ok := cls.Variable(*each)
	switch {
	case !ok:
		c.addf(CodeUnknownVariable, path+"/each", "unknown variable %s", quote(*each))
		return nil
	case v.Type.Kind != formula.KindArray && v.Type.Kind != formula.KindRows:
		c.hint(CodeTypeMismatch, path+"/each", "each names a list, and "+v.Name+" is "+article(v.Type.String()), "name an array or a rows variable of the class")
		return nil
	}
	if as == nil || *as == "" {
		c.hint(CodeMissing, path+"/as", "a create with each names the element, for the formulas under values and id", `"as": "approver"`)
		return nil
	}
	if !c.checkName(path+"/as", *as, nil) {
		return nil
	}
	if own, dup := cls.Variable(*as); dup {
		c.hint(CodeDuplicate, path+"/as", quote(*as)+" is the variable "+own.Name+" of the class", "give the element a name no variable has")
		return nil
	}
	elem := formula.Type{Kind: v.Type.Elem, Scale: v.Type.Scale}
	if v.Type.Kind == formula.KindRows {
		elem = formula.ObjectType(v.Type.Schema(), v.Type.Fields())
	}
	vars := scopeVariables(cls.Variables)
	scope, err := formula.NewScope(append(vars, formula.Variable{Name: *as, Type: elem})...)
	if err != nil {
		c.add(CodeBadName, path+"/as", err.Error())
		return nil
	}
	a.Each, a.As = v, *as
	return scope
}

// compileMove reads a transition action: a named transition of a
// document, of the rule's own class unless it names another.
func compileMove(c *collector, path string, raw json.RawMessage, cls *Class, scope *formula.Scope, a *Action) *Action {
	var def struct {
		Action string          `json:"action"`
		Class  string          `json:"class"`
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		Values json.RawMessage `json:"values"`
	}
	if !decodeAction(c, path, raw, &def) {
		return nil
	}
	a.Queued, a.Transition = true, def.Name
	target, ok := targetClass(c, path, def.Class, cls, "define it before the class whose rule moves its documents")
	if !ok && !c.env.Lenient {
		return nil
	}
	a.Target = targetName(def.Class, target)
	switch {
	case def.Name == "":
		c.hint(CodeMissing, path+"/name", "a transition action names the transition it takes", `"name": "approve"`)
	case ok && !c.env.Lenient && !hasTransition(target, def.Name):
		c.addf(CodeUnknownTransition, path+"/name", "the class %s has no transition %s", target.Name, quote(def.Name))
	}
	a.ID = documentID(c, path, def.ID, scope, "a transition action names the document it moves")
	sets, _, read := fillValues(c, path+"/values", def.Values, target, scope, "transition action")
	if !read {
		return nil
	}
	a.Sets = sets
	return a
}

// compileRefreshOther reads a refresh that carries an id (ADR-0044,
// item 13): the Refresh command, sent to another document. The variables
// it names are triggered variables of that document's class that list
// refresh; with none named, every one that does recomputes.
func compileRefreshOther(c *collector, path string, raw json.RawMessage, cls *Class, scope *formula.Scope, a *Action) *Action {
	var def struct {
		Action    string   `json:"action"`
		Class     string   `json:"class"`
		ID        string   `json:"id"`
		Variables []string `json:"variables"`
	}
	if !decodeAction(c, path, raw, &def) {
		return nil
	}
	a.Queued = true
	target, ok := targetClass(c, path, def.Class, cls, "define it before the class whose rule refreshes its documents")
	if !ok && !c.env.Lenient {
		return nil
	}
	a.Target = targetName(def.Class, target)
	named := map[string]bool{}
	for k, name := range def.Variables {
		vpath := fmt.Sprintf("%s/variables/%d", path, k)
		if named[upper(name)] {
			c.addf(CodeDuplicate, vpath, "%s is named twice", quote(name))
			continue
		}
		named[upper(name)] = true
		var v *Variable
		if ok {
			v, _ = target.Variable(name)
		}
		switch {
		case c.env.Lenient:
		case v == nil:
			c.addf(CodeUnknownVariable, vpath, "the class %s has no variable %s", target.Name, quote(name))
			continue
		case !v.Triggered():
			c.hint(CodeNotAllowed, vpath, "a refresh recomputes triggered variables; "+v.Name+" of "+target.Name+" is not one", `a triggered variable has "mode": "triggered" and lists its triggers under "on"`)
			continue
		case !v.On("refresh", ""):
			c.hint(CodeNotAllowed, vpath, v.Name+" of "+target.Name+" does not list refresh among its triggers", `add "refresh" to its "on"`)
			continue
		}
		if v != nil {
			name = v.Name
		}
		a.Names = append(a.Names, name)
	}
	a.ID = documentID(c, path, def.ID, scope, "a refresh of another document names the document")
	return a
}

// compileTableAction reads table.insert, table.update and table.delete:
// one row of an explicit table, addressed by its key (ADR-0044, item 7).
func compileTableAction(c *collector, path string, raw json.RawMessage, scope *formula.Scope, a *Action) *Action {
	var def struct {
		Action string          `json:"action"`
		Table  string          `json:"table"`
		Row    json.RawMessage `json:"row"`
		Key    json.RawMessage `json:"key"`
		Set    json.RawMessage `json:"set"`
	}
	if !decodeAction(c, path, raw, &def) {
		return nil
	}
	a.Queued = true
	if def.Table == "" {
		c.hint(CodeMissing, path+"/table", "a table action names the table it writes", `"table": "Rates"`)
		return nil
	}
	var src *formula.Source
	if c.env.Sources != nil {
		src, _ = c.env.Sources.Source(def.Table)
	}
	a.Target = def.Table
	switch {
	case src != nil:
		a.Target = src.Name
		if src.Binding != "" && src.Binding != "sqlite" && !c.env.Lenient {
			c.hint(CodeNotSupported, path+"/table", "the table "+src.Name+" has the "+src.Binding+" binding; a rule writes the rows of a table the database file holds", "a table with the sqlite binding")
		}
	case !c.env.Lenient:
		c.hint(CodeUnknownTable, path+"/table", "unknown table "+quote(def.Table), "define the table before the class whose rule writes it")
		return nil
	}
	// Which of row, key and set the action takes.
	takes := map[string]bool{"row": a.Name == ActionTableInsert, "key": a.Name != ActionTableInsert, "set": a.Name == ActionTableUpdate}
	for _, part := range []struct {
		name string
		raw  json.RawMessage
	}{{"row", def.Row}, {"key", def.Key}, {"set", def.Set}} {
		switch {
		case takes[part.name] && len(part.raw) == 0:
			c.hint(CodeMissing, path+"/"+part.name, "a "+a.Name+" has "+article(part.name), tableActionShape[a.Name])
		case !takes[part.name] && len(part.raw) > 0:
			c.hint(CodeNotAllowed, path+"/"+part.name, "a "+a.Name+" takes no "+part.name, tableActionShape[a.Name])
		}
	}
	read := true
	fill := func(name string, raw json.RawMessage, key bool) []Assignment {
		if !takes[name] || len(raw) == 0 {
			return nil
		}
		sets, ok := fillColumns(c, path+"/"+name, raw, src, scope, a.Name, name, key)
		read = read && ok
		return sets
	}
	a.Keys = fill("key", def.Key, true)
	if a.Name == ActionTableInsert {
		a.Sets = fill("row", def.Row, true)
	} else {
		a.Sets = fill("set", def.Set, false)
	}
	if !read {
		return nil
	}
	return a
}

// tableActionShape is how each table action is written, for the hints.
var tableActionShape = map[string]string{
	ActionTableInsert: `{"action": "table.insert", "table": "Rates", "row": {"code": "currency", "rate": "rate"}}`,
	ActionTableUpdate: `{"action": "table.update", "table": "Rates", "key": {"code": "currency"}, "set": {"rate": "rate"}}`,
	ActionTableDelete: `{"action": "table.delete", "table": "Rates", "key": {"code": "currency"}}`,
}

// fillColumns compiles the formulas a table action gives the columns of
// a table, each typed against its column, in the order written. With key
// the part must name every key column: it is a row, or the key of one;
// without, it is the set of an update, which changes no key column. src
// is nil when a stored definition names a table the database no longer
// has.
func fillColumns(c *collector, path string, raw json.RawMessage, src *formula.Source, scope *formula.Scope, action, part string, key bool) ([]Assignment, bool) {
	names, err := orderedKeys(raw)
	if err != nil {
		c.add(CodeBadValue, path, "the "+part+" of a "+action+" is an object of formulas by column")
		return nil, false
	}
	var formulas map[string]string
	if err := json.Unmarshal(raw, &formulas); err != nil {
		c.hint(CodeBadValue, path, "every value of the "+part+" of a "+action+" is a formula, written as text", `a literal text is "\"EUR\"", a number "0"`)
		return nil, false
	}
	if len(names) == 0 {
		c.hint(CodeMissing, path, "the "+part+" of a "+action+" names at least one column", tableActionShape[action])
		return nil, true
	}
	lenient := c.env.Lenient
	isKey := func(name string) bool {
		for _, k := range src.Key {
			if upper(k) == upper(name) {
				return true
			}
		}
		return false
	}
	var sets []Assignment
	given := map[string]bool{}
	for _, name := range names {
		vpath := path + "/" + name
		if given[upper(name)] {
			c.addf(CodeDuplicate, vpath, "%s is given twice; names differ in case only", quote(name))
			continue
		}
		given[upper(name)] = true
		var col *formula.Field
		if src != nil {
			for i := range src.Columns {
				if upper(src.Columns[i].Name) == upper(name) {
					col = &src.Columns[i]
				}
			}
		}
		if col == nil {
			if !lenient {
				c.addf(CodeUnknownVariable, vpath, "the table %s has no column %s", src.Name, quote(name))
				continue
			}
			if prog := bindFill(c, vpath, formulas[name], scope, nil); prog != nil {
				sets = append(sets, Assignment{Name: name, Formula: prog, Src: formulas[name]})
			}
			continue
		}
		if !lenient {
			switch {
			case part == "key" && !isKey(col.Name):
				c.hint(CodeNotAllowed, vpath, col.Name+" is no key column of "+src.Name+"; the key is "+strings.Join(src.Key, ", "), "a key names the key columns and nothing else")
				continue
			case part == "set" && isKey(col.Name):
				c.hint(CodeNotAllowed, vpath, col.Name+" is a key column of "+src.Name+", and an update changes no key", "delete the row and insert another")
				continue
			}
		}
		if prog := bindFill(c, vpath, formulas[name], scope, &col.Type); prog != nil {
			sets = append(sets, Assignment{Name: col.Name, Formula: prog, Src: formulas[name]})
		}
	}
	if key && src != nil && !lenient {
		var missing []string
		for _, k := range src.Key {
			if !given[upper(k)] {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			c.hint(CodeMissing, path, "the "+part+" gives no value for "+strings.Join(missing, ", ")+", of the key of "+src.Name, "a row is addressed by every column of its key")
		}
	}
	return sets, true
}

// textType is the type of a document's id.
var textType = formula.Scalar(formula.KindText)

// targetClass resolves the class an action names: the class under
// definition when it names none or itself, else one the environment has.
// It reports false, with a finding unless the definition is a stored one,
// when there is no such class; and it refuses a class without states,
// which stores no documents (ADR-0040).
func targetClass(c *collector, path, name string, cls *Class, hint string) (*Class, bool) {
	target, ok := cls, name == "" || upper(name) == upper(cls.Name)
	if !ok && c.env.Classes != nil {
		target, ok = c.env.Classes(name)
	}
	switch {
	case !ok:
		if !c.env.Lenient {
			c.hint(CodeUnknownClass, path+"/class", "unknown class "+quote(name), hint)
		}
		return nil, false
	case target.States == nil && !c.env.Lenient:
		c.hint(CodeNotAllowed, path+"/class", target.Name+" declares no states: it is a stateless application and stores no documents (ADR-0040)", "name a class that has a states block")
	}
	return target, true
}

// targetName is the class as it declares its name, or as the action
// wrote it when a stored definition names one the database does not have.
func targetName(written string, target *Class) string {
	if target != nil {
		return target.Name
	}
	return written
}

// documentID binds the id of the document a transition or a refresh of
// another document acts on, which it must name.
func documentID(c *collector, path, src string, scope *formula.Scope, what string) *formula.Program {
	if src == "" {
		c.hint(CodeMissing, path+"/id", what, `"id" is a formula that gives the document's id, such as "invoice"; ID() is the rule's own document`)
		return nil
	}
	return bindFill(c, path+"/id", src, scope, &textType)
}

// fillValues compiles the values an action gives the variables of a
// class: each a formula typed against the scalar input it fills, in the
// order written. It returns them with the names given, upper-cased, and
// false when the values do not read. target is nil when a stored
// definition names a class the database does not have.
func fillValues(c *collector, path string, raw json.RawMessage, target *Class, scope *formula.Scope, verb string) ([]Assignment, map[string]bool, bool) {
	given := map[string]bool{}
	if len(raw) == 0 {
		return nil, given, true
	}
	names, err := orderedKeys(raw)
	if err != nil {
		c.add(CodeBadValue, path, "the values of a "+verb+" are an object of formulas by variable")
		return nil, given, false
	}
	var formulas map[string]string
	if err := json.Unmarshal(raw, &formulas); err != nil {
		c.hint(CodeBadValue, path, "every value of a "+verb+" is a formula, written as text", `a literal text is "\"new\"", a number "0"`)
		return nil, given, false
	}
	lenient := c.env.Lenient
	var sets []Assignment
	for _, name := range names {
		vpath := path + "/" + name
		if given[upper(name)] {
			c.addf(CodeDuplicate, vpath, "%s is given twice; names differ in case only", quote(name))
			continue
		}
		given[upper(name)] = true
		var v *Variable
		if target != nil {
			v, _ = target.Variable(name)
		}
		composite := v != nil && (v.Type.Kind == formula.KindRows || v.Type.Kind == formula.KindArray || v.Type.Kind == formula.KindObject)
		if lenient && (v == nil || v.Computed || composite) {
			// What a stored definition gives is taken as it is; the action
			// is refused for it when it runs.
			if prog := bindFill(c, vpath, formulas[name], scope, nil); prog != nil {
				sets = append(sets, Assignment{Name: name, Formula: prog, Src: formulas[name]})
			}
			continue
		}
		switch {
		case v == nil:
			c.addf(CodeUnknownVariable, vpath, "the class %s has no variable %s", target.Name, quote(name))
			continue
		case v.Computed:
			c.hint(CodeNotAllowed, vpath, "a "+verb+" gives input variables; "+v.Name+" of "+target.Name+" is computed", "its formula gives its value")
			continue
		case composite:
			c.hint(CodeNotSupported, vpath, "a rule cannot give "+v.Name+": no formula gives "+article(v.Type.String())+" in this build", "give scalar variables")
			continue
		}
		if prog := bindFill(c, vpath, formulas[name], scope, &v.Type); prog != nil {
			sets = append(sets, Assignment{Name: v.Name, Variable: v, Formula: prog, Src: formulas[name]})
		}
	}
	return sets, given, true
}

// bindFill binds a formula that fills something of another class or of
// a table. want is its type, nil when a stored definition names what the
// database no longer has. A stored definition is never a finding for
// what it says of another class: a type that no longer fits is found
// when the action runs.
func bindFill(c *collector, path, src string, scope *formula.Scope, want *formula.Type) *formula.Program {
	if want != nil && !c.env.Lenient {
		return bindAs(c, path, src, scope, formula.ModeAny, *want)
	}
	if want != nil {
		probe := collector{env: c.env}
		if prog := bindAs(&probe, path, src, scope, formula.ModeAny, *want); prog != nil {
			return prog
		}
	}
	prog, err := c.bind(src, scope, formula.ModeAny)
	if err != nil {
		c.formulaErr(path, err)
		return nil
	}
	return prog
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
