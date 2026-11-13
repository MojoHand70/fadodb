package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Class is a compiled class definition: every formula bound, every
// reference checked, the computed variables in evaluation order, and the
// variables laid out as the columns and child tables of ADR-0014.
type Class struct {
	Name        string
	Version     int
	Label       string
	Plural      string
	Description string
	Icon        string
	Color       string
	// Title is the compiled title formula, nil when TITLE is ID.
	Title *formula.Program
	// Variables in declaration order.
	Variables []*Variable
	byName    map[string]*Variable
	// Order lists the auto computed variables, and "title" when there is
	// a title formula, in an order where every one comes after what it
	// reads: the full recalculation order of ADR-0015.
	Order []string
	// DefaultOrder lists the inputs with defaults in dependency order.
	DefaultOrder []string
	// Columns are the typed columns of the current and history tables
	// after the system columns, in variable order; Children the child
	// tables of composite variables.
	Columns  []Column
	Children []ChildTable
	// Schemas are the schemas the class uses, resolved at definition.
	Schemas     []*Schema
	States      *States
	Transitions []*Transition
	Rules       []*Rule
	Access      *Access
	// Rights is the rights table by state, then variable, "*" included
	// (ADR-0028); nil when the class declares none.
	Rights map[string]map[string]*Right
	// Reads names the classes whose documents a formula of the class
	// reads (ADR-0046), itself among them when it reads its own.
	Reads []string
	Def   *Definition
}

// systemFields are the system columns of a class as a source reads them.
var systemFields = []formula.Field{
	{Name: "id", Type: formula.Scalar(formula.KindText)},
	{Name: "title", Type: formula.Scalar(formula.KindText)},
	{Name: "state", Type: formula.Scalar(formula.KindText)},
	{Name: "version", Type: formula.Scalar(formula.KindNumber)},
	{Name: "created_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "updated_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "state_entered_at", Type: formula.Scalar(formula.KindDateTime)},
	{Name: "created_by", Type: formula.Scalar(formula.KindText)},
	{Name: "updated_by", Type: formula.Scalar(formula.KindText)},
	{Name: "state_entered_by", Type: formula.Scalar(formula.KindText)},
	{Name: "state_entered_from", Type: formula.Scalar(formula.KindText)},
}

// Source is the documents of the class as a LOOKUP reads them
// (ADR-0046): one row per document, the system columns and every scalar
// variable whose type is known, inputs and computed alike, ordered by id.
// The id and the indexed variables lead an index. Objects, rows and the
// history are not read yet.
func (c *Class) Source() *formula.Source {
	src := &formula.Source{Name: c.Name, Class: true, Key: []string{"id"}, Indexed: []string{"id"}}
	src.Columns = append(src.Columns, systemFields...)
	for _, v := range c.Variables {
		if v.Type.Kind == formula.KindNull || !v.Type.IsScalar() {
			continue
		}
		src.Columns = append(src.Columns, formula.Field{Name: v.Name, Type: v.Type})
		if v.Index {
			src.Indexed = append(src.Indexed, v.Name)
		}
	}
	return src
}

// Variable is one compiled variable.
type Variable struct {
	Name string
	// Computed is true for a variable with a formula, false for an input.
	Computed bool
	Type     formula.Type
	// RefClass is the class a ref variable points at.
	RefClass string
	Required bool
	// Default is the compiled default of an input, nil when none.
	Default *formula.Program
	// Formula and Mode belong to a computed variable; Triggers to a
	// triggered one.
	Formula  *formula.Program
	Mode     ComputeMode
	Triggers []Trigger
	// Values is the enum list of a text variable (ADR-0023).
	Values []string
	// Min and Max bound a numeric variable (ADR-0034); HasMin and HasMax
	// say which are set.
	Min, Max       formula.Value
	HasMin, HasMax bool
	Index          bool
	// Deps are the variables the formula or default reads, by declared
	// name, an object by its variable name whichever leaf was read.
	Deps []string
	// Sources are the explicit tables the formula or default reads
	// through LOOKUP, by name.
	Sources []string
	Def     *VariableDef
}

// ComputeMode is how a computed variable recalculates (ADR-0015).
type ComputeMode string

// The modes.
const (
	ModeAuto      ComputeMode = "auto"
	ModeTriggered ComputeMode = "triggered"
)

// Trigger is one trigger of a triggered variable: refresh, enter:<state>,
// exit:<state> or transition:<name>. table:<name> and schedule:<spec>
// are refused until the phase that runs them (ADR-0043).
type Trigger struct {
	Kind string
	Arg  string
}

// Column is one storage column of the current and history tables.
type Column struct {
	// Name is the column name: the variable, a flattened field path, or a
	// money column's currency twin.
	Name string
	Type formula.Type
	// Variable is the variable the column belongs to; Path the dotted
	// field path for an object leaf, the variable name for a scalar.
	Variable string
	Path     string
	Index    bool
	// Moment marks one of the two system columns of a triggered variable
	// (ADR-0043): MomentAsOf, the version of the document whose commit
	// last computed the value, or MomentError, the code of a failed
	// recomputation since then. Its Path is its own name. "" otherwise.
	Moment string
}

// The moment columns of a triggered variable are <name>__asof and
// <name>__error; __ is reserved in every name, so none collides.
const (
	MomentAsOf  = "asof"
	MomentError = "error"
)

// MomentColumn names a moment column of a triggered variable.
func MomentColumn(variable, moment string) string { return variable + PathSeparator + moment }

// Triggered reports whether the variable is computed on triggers.
func (v *Variable) Triggered() bool { return v.Computed && v.Mode == ModeTriggered }

// On reports whether the variable lists the trigger: On("refresh", ""),
// On("enter", "approved").
func (v *Variable) On(kind, arg string) bool {
	for _, t := range v.Triggers {
		if t.Kind == kind && t.Arg == arg {
			return true
		}
	}
	return false
}

// ChildTable is the per-version table of a composite variable
// (ADR-0014): the schema's columns, or value for an array, after doc_id,
// version and idx.
type ChildTable struct {
	Variable string
	Columns  []Column
}

// States is the compiled states block.
type States struct {
	Initial string
	List    []string
	Final   []string
}

// Has reports whether the state is one of the class's.
func (s *States) Has(state string) bool {
	if s == nil {
		return false
	}
	for _, st := range s.List {
		if st == state {
			return true
		}
	}
	return false
}

// Transition is one compiled transition; Guard is nil when there is none.
type Transition struct {
	Name  string
	From  []string
	To    string
	Guard *formula.Program
	// GuardSrc is the guard as written, for the hint of a refusal.
	GuardSrc string
	// After is the length of time in the state after which a timed
	// transition fires, in seconds, 0 for none (ADR-0047, item 9).
	After int64
	// At gives the moment a timed transition fires: a date, which means
	// the first instant of that day in UTC, or a datetime; a blank never
	// comes. nil for none. AtSrc is the formula as written.
	At    *formula.Program
	AtSrc string
}

// Timed reports whether the transition fires by time: it carries an
// after or an at.
func (t *Transition) Timed() bool { return t.After > 0 || t.At != nil }

// MaxAfter is the longest time an after may name, in seconds: a thousand
// years of 365 days. It keeps the moment of a timer inside what a
// datetime holds.
const MaxAfter = 1000 * 365 * 86400

// ParseAfter reads a length of time as a transition's after writes it
// (ADR-0047, item 9): a whole number above zero and d, h, m or s. A day
// is 24 hours. It answers the seconds, and false for anything else.
func ParseAfter(s string) (int64, bool) {
	if len(s) < 2 {
		return 0, false
	}
	var unit int64
	switch s[len(s)-1] {
	case 'd':
		unit = 86400
	case 'h':
		unit = 3600
	case 'm':
		unit = 60
	case 's':
		unit = 1
	default:
		return 0, false
	}
	var n int64
	for _, ch := range s[:len(s)-1] {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		if n = n*10 + int64(ch-'0'); n*unit > MaxAfter {
			return 0, false
		}
	}
	if n == 0 {
		return 0, false
	}
	return n * unit, true
}

// LeftOutAllows is what a class-level access rule answers that a class
// leaves out, question A of ADR-0049. The owner ruled on 2026-11-13 that
// it lets nobody through (ADR-0052, item 5). Until the step that builds
// that, with rules for every sample and test, the engine keeps the
// answer it has given since Phase 2: a class that says nothing lets
// every caller through.
const LeftOutAllows = true

// Access holds the compiled class-level access rules.
type Access struct {
	Read, Create, Update, Delete *formula.Program
	Transition                   map[string]*formula.Program
}

// Right is one cell of the rights table: a formula, or a list of groups
// meaning INGROUP of any of them. A nil Right in a cell means the rule
// was not declared and the class-level rules alone apply.
type Right struct {
	Read, Write *RightRule
}

// RightRule is a formula or a group list; Src is the rule as written,
// for the hint of a refusal.
type RightRule struct {
	Formula *formula.Program
	Groups  []string
	Src     string
}

// ReadRule resolves the read right that governs a variable in a state
// (ADR-0028, ADR-0041): the cell of the named state and variable, then
// the state's "*", then the "*" state's cell of the variable, then its
// "*"; the first cell declaring a read rule decides. nil means the class
// rules alone apply and the variable is readable. A state of "" reads the
// "*" row alone.
func (c *Class) ReadRule(state, variable string) *RightRule {
	return c.rule(state, variable, func(r *Right) *RightRule { return r.Read })
}

// WriteRule resolves the write right of a variable in a state the way
// ReadRule resolves the read right.
func (c *Class) WriteRule(state, variable string) *RightRule {
	return c.rule(state, variable, func(r *Right) *RightRule { return r.Write })
}

func (c *Class) rule(state, variable string, pick func(*Right) *RightRule) *RightRule {
	if c.Rights == nil {
		return nil
	}
	for _, st := range [2]string{state, "*"} {
		if st == "" {
			continue
		}
		row, ok := c.Rights[st]
		if !ok {
			continue
		}
		for _, name := range [2]string{variable, "*"} {
			if r, ok := row[name]; ok {
				if rule := pick(r); rule != nil {
					return rule
				}
			}
		}
	}
	return nil
}

// Variable finds a variable by name, case-insensitively.
func (c *Class) Variable(name string) (*Variable, bool) {
	v, ok := c.byName[upper(name)]
	return v, ok
}

// Env is what a definition compiles against: the schemas its objects
// name and the explicit tables its formulas may read (ADR-0016). A nil
// Schemas is the vocabulary alone; nil Sources are none.
type Env struct {
	Schemas *SchemaSet
	Sources formula.Sources
	// Classes finds a defined class by name, for an action that names
	// another class (ADR-0044); nil when there are none.
	Classes func(name string) (*Class, bool)
	// Actions finds an action definition by name, for a rule that names
	// one (ADR-0045); nil when there are none.
	Actions func(name string) (*ActionSpec, bool)
	// Lenient compiles a stored definition, which was checked when it was
	// defined: what an action says about another class, or about the
	// definition of an action, is taken as it is, typed against it where
	// the environment has it and left untyped where it does not, and is
	// never a finding. A class may name one defined after it, and loading
	// must not depend on the order.
	Lenient bool
	// Shallow compiles a stored definition for its variables, their types
	// and what does not read other documents: a formula that reads the
	// documents of a class and does not bind is left unbound and is no
	// finding (ADR-0046). It is how a class is compiled before the classes
	// it reads are, and how an earlier version of a class is, whose
	// formulas no longer run and may read what a class has since dropped.
	Shallow bool
}

// Define parses and compiles a class definition in one step, against the
// schemas and no tables.
func Define(data []byte, schemas *SchemaSet) (*Class, Diagnostics) {
	return DefineIn(data, Env{Schemas: schemas})
}

// DefineIn parses and compiles a class definition against an environment.
func DefineIn(data []byte, env Env) (*Class, Diagnostics) {
	def, diags := Parse(data)
	if def == nil {
		return nil, diags
	}
	cls, more := CompileIn(def, env)
	return cls, append(diags, more...)
}

// Compile validates and compiles a definition against the schemas and no
// tables.
func Compile(def *Definition, schemas *SchemaSet) (*Class, Diagnostics) {
	return CompileIn(def, Env{Schemas: schemas})
}

// reservedClassNames are the tables and views the database itself owns,
// which a class view may not shadow.
var reservedClassNames = map[string]bool{
	"events": true, "event_docs": true, "classes": true, "class_versions": true, "schemas": true,
	"layouts": true, "outbox": true, "blobs": true, "tables_registry": true, "schema_version": true, "metadata": true,
	"actions": true, "action_versions": true,
}

// CompileIn validates a definition against the environment and compiles
// it. It reports every finding it can, not the first: a class with three
// wrong formulas earns three diagnostics.
func CompileIn(def *Definition, env Env) (*Class, Diagnostics) {
	schemas := env.Schemas
	if schemas == nil {
		schemas = NewSchemaSet()
	}
	c := &collector{env: env}
	cls := &Class{Name: def.Name, Version: def.Version, Label: def.Label, Plural: def.Plural, Description: def.Description,
		Icon: def.Icon, Color: def.Color, Def: def, byName: map[string]*Variable{}}
	c.self = cls
	if c.checkName("/name", def.Name, reservedClassNames) && strings.HasPrefix(strings.ToLower(def.Name), "doc_") {
		c.add(CodeReservedName, "/name", "a class name may not start with doc_, the prefix of the generated tables")
	}
	if cls.Version == 0 {
		cls.Version = 1
	} else if cls.Version < 0 {
		c.add(CodeBadValue, "/version", "the version is a positive number")
	}

	// Variables: names, types and properties.
	usedSchemas := map[string]*Schema{}
	for _, name := range def.VariableOrder {
		path := "/variables/" + name
		vd := def.Variables[name]
		if vd == nil {
			c.add(CodeMissing, path, "a variable is an object with a type")
			continue
		}
		if !c.checkName(path, name, reservedVariableNames) {
			continue
		}
		if _, dup := cls.byName[upper(name)]; dup {
			c.addf(CodeDuplicate, path, "variable %s is declared twice; names differ in case only", quote(name))
			continue
		}
		v := compileVariable(c, path, name, vd, schemas, usedSchemas)
		if v == nil {
			continue
		}
		cls.Variables = append(cls.Variables, v)
		cls.byName[upper(name)] = v
	}
	for _, name := range schemas.Names() {
		if s, ok := usedSchemas[upper(name)]; ok {
			cls.Schemas = append(cls.Schemas, s)
		}
	}

	// States first, since triggers, transitions and rules refer to them.
	cls.States = compileStates(c, def.States)

	// Computed variables without a declared type take their formula's
	// type, so they are bound first, each after the ones it reads.
	inferTypes(c, cls, schemas)

	// Formulas, against every variable.
	scope := scopeOf(cls.Variables)
	for _, v := range cls.Variables {
		path := "/variables/" + v.Name
		if v.Computed {
			if v.Formula == nil && v.Type.Kind != formula.KindNull {
				mode := formula.ModeAuto
				if v.Mode == ModeTriggered {
					mode = formula.ModeAny
				}
				if prog := bindAs(c, path+"/formula", v.Def.Formula, scope, mode, v.Type); prog != nil {
					v.Formula = prog
					v.Deps = depsOf(prog)
					v.Sources = sourcesOf(prog)
				}
			}
			continue
		}
		if v.Def.Default != "" {
			if prog := bindAs(c, path+"/default", v.Def.Default, scope, formula.ModeAny, v.Type); prog != nil {
				v.Default = prog
				v.Deps = depsOf(prog)
				v.Sources = sourcesOf(prog)
				for _, d := range v.Deps {
					if dv, ok := cls.Variable(d); ok && dv.Computed {
						c.hint(CodeNotAllowed, path+"/default", "a default may read input variables only; "+dv.Name+" is computed",
							"defaults run when the document is created, before anything is computed")
					}
				}
			}
		}
	}
	if def.Title != "" {
		prog, err := c.bind(def.Title, scope, formula.ModePure)
		switch {
		case err != nil:
			c.formulaErr("/title", err)
		case prog.Result.Kind != formula.KindText && prog.Result.Kind != formula.KindNull:
			c.hint(CodeTypeMismatch, "/title", "the title must be text, got "+prog.Result.String(), "wrap it in TEXT(...)")
		default:
			cls.Title = prog
		}
	}

	// The dependency graphs: auto variables and the title; defaults.
	compileOrder(c, cls)

	// Storage layout.
	compileColumns(c, cls)

	// The statechart, rules, access rules and rights: structure and
	// formulas now, behaviour in their phases.
	compileTransitions(c, cls, def.Transitions, scope)
	// The triggers of the triggered variables name states and transitions
	// and are checked against the ones a frozen document passes through,
	// so they wait for the transitions.
	for _, v := range cls.Variables {
		if v.Computed {
			for i, t := range v.Def.On {
				checkTrigger(c, fmt.Sprintf("/variables/%s/on/%d", v.Name, i), t, cls, v)
			}
		}
	}
	compileRules(c, cls, def.Rules, scope)
	compileAccess(c, cls, def.Access, scope)
	compileRights(c, cls, def, scope)

	if c.failed() {
		return nil, c.list
	}
	cls.Reads = c.reads
	return cls, nil
}

// compileVariable reads one variable's type and properties.
func compileVariable(c *collector, path, name string, vd *VariableDef, schemas *SchemaSet, used map[string]*Schema) *Variable {
	v := &Variable{Name: name, Def: vd, Required: vd.Required, RefClass: vd.Class, Values: vd.Values, Index: vd.Index}
	if vd.Formula != "" {
		v.Computed = true
	}
	if vd.Type == "" && !v.Computed {
		c.hint(CodeMissing, path+"/type", "an input variable has a type", "a computed variable may leave it out and take its formula's type")
		return nil
	}
	// Input or computed: the properties of the kind, whatever the type.
	if v.Computed {
		if vd.Default != "" {
			c.hint(CodeNotAllowed, path+"/default", "a computed variable has no default", "its formula gives its value")
		}
		if vd.Required {
			c.add(CodeNotAllowed, path+"/required", "a computed variable is never required; its formula gives its value")
		}
		switch vd.Mode {
		case "", "auto":
			v.Mode = ModeAuto
			if len(vd.On) > 0 {
				c.hint(CodeNotAllowed, path+"/on", "an auto variable has no triggers", `set "mode": "triggered" to recalculate on triggers`)
			}
		case "triggered":
			v.Mode = ModeTriggered
			if len(vd.On) == 0 {
				c.hint(CodeMissing, path+"/on", "a triggered variable names its triggers", `"on": ["refresh"] recalculates on the refresh command`)
			}
		default:
			c.addf(CodeBadValue, path+"/mode", "the mode is auto or triggered, got %s", quote(vd.Mode))
		}
	} else {
		if vd.Mode != "" {
			c.add(CodeNotAllowed, path+"/mode", "only a computed variable, one with a formula, has a mode")
		}
		if len(vd.On) > 0 {
			c.add(CodeNotAllowed, path+"/on", "only a triggered computed variable has triggers")
		}
	}

	// The type. A computed variable without one takes its formula's type
	// once the formula is bound; its properties are checked then.
	var composite bool
	switch vd.Type {
	case "":
		return v
	case "object":
		sch := schemaOf(c, path, vd, schemas)
		if sch == nil {
			return nil
		}
		used[upper(sch.Name)] = sch
		v.Type = sch.Type
	case "rows":
		cols, schema, ok := rowsColumns(c, path, vd, schemas, used)
		if !ok {
			return nil
		}
		v.Type = formula.RowsType(schema, cols)
		composite = true
	default:
		t, err := formula.ParseType(vd.Type)
		if err != nil {
			c.add(CodeUnknownType, path+"/type", err.Error())
			return nil
		}
		if t.Kind == formula.KindNull {
			c.add(CodeUnknownType, path+"/type", "a variable is never blank by type")
			return nil
		}
		if t.Kind == formula.KindRows {
			c.hint(CodeUnknownType, path+"/type", "rows take their columns from schema", `write "type": "rows" with "schema"`)
			return nil
		}
		composite = !t.IsScalar()
		v.Type = t
	}
	if vd.Scale != nil {
		switch {
		case !v.Type.IsExact() || vd.Type == "int":
			c.hint(CodeNotAllowed, path+"/scale", "only decimal and money have a scale", "write the type as decimal(s) or money(s), or remove scale")
		case *vd.Scale < 0 || *vd.Scale > formula.MaxScale:
			c.addf(CodeBadValue, path+"/scale", "the scale is 0 to %d", formula.MaxScale)
		default:
			v.Type.Scale = int8(*vd.Scale)
		}
	}
	if v.Type.Kind == formula.KindRef && vd.Class == "" {
		c.hint(CodeMissing, path+"/class", "a ref names the class it points at", `add "class": "<ClassName>"`)
	}
	if vd.Class != "" && v.Type.Kind != formula.KindRef {
		c.add(CodeNotAllowed, path+"/class", "only a ref has a class")
	}
	if vd.Schema != nil && vd.Type != "object" && vd.Type != "rows" {
		c.add(CodeNotAllowed, path+"/schema", "only an object or rows variable has a schema")
	}

	// What a formula can produce.
	if v.Computed {
		if composite || v.Type.Kind == formula.KindObject {
			c.add(CodeNotSupported, path+"/formula", "a computed "+v.Type.String()+" is not supported yet; a formula computes a scalar")
		}
		if v.Type.Kind == formula.KindJSON {
			c.add(CodeNotAllowed, path+"/formula", "no formula produces a json value (ADR-0013)")
		}
	} else if vd.Default != "" && (composite || v.Type.Kind == formula.KindObject || v.Type.Kind == formula.KindJSON) {
		c.add(CodeNotSupported, path+"/default", "a default for "+v.Type.String()+" is not supported yet; a formula computes a scalar")
	}

	// Enum values, ranges, indexes.
	if len(vd.Values) > 0 {
		if v.Type.Kind != formula.KindText {
			c.hint(CodeNotAllowed, path+"/values", "only a text variable has values (ADR-0023)", "declare the variable as text")
		} else {
			seen := map[string]bool{}
			for i, val := range vd.Values {
				switch {
				case val == "":
					c.addf(CodeBadValue, fmt.Sprintf("%s/values/%d", path, i), "an enum value is not empty")
				case seen[val]:
					c.addf(CodeDuplicate, fmt.Sprintf("%s/values/%d", path, i), "value %s is listed twice", quote(val))
				}
				seen[val] = true
			}
		}
	}
	numeric := v.Type.Kind == formula.KindNumber || v.Type.IsExact()
	for _, b := range []struct {
		raw  json.RawMessage
		name string
		dst  *formula.Value
		has  *bool
	}{{vd.Min, "min", &v.Min, &v.HasMin}, {vd.Max, "max", &v.Max, &v.HasMax}} {
		if len(b.raw) == 0 {
			continue
		}
		if !numeric {
			c.add(CodeNotAllowed, path+"/"+b.name, "only number, int, decimal and money variables have a range (ADR-0034)")
			continue
		}
		lt := v.Type
		if lt.Kind == formula.KindMoney {
			lt = formula.DecimalType(int(lt.Scale))
		}
		val, err := formula.ParseCaseValue(b.raw, lt)
		if err != nil || val.IsBlank() {
			c.addf(CodeBadValue, path+"/"+b.name, "%s must be a literal of type %s", b.name, lt)
			continue
		}
		*b.dst, *b.has = val, true
	}
	if v.HasMin && v.HasMax && compareBounds(v.Min, v.Max) > 0 {
		c.add(CodeBadValue, path+"/max", "max is below min")
	}
	if vd.Index && (composite || v.Type.Kind == formula.KindObject || v.Type.Kind == formula.KindJSON) {
		c.add(CodeNotSupported, path+"/index", "an index on "+v.Type.String()+" is not supported; index a scalar variable")
	}
	return v
}

// compareBounds compares two bounds of one numeric type.
func compareBounds(a, b formula.Value) int {
	if a.Kind().IsExact() {
		switch {
		case a.Mant() < b.Mant():
			return -1
		case a.Mant() > b.Mant():
			return 1
		}
		return 0
	}
	switch {
	case a.Num() < b.Num():
		return -1
	case a.Num() > b.Num():
		return 1
	}
	return 0
}

// schemaOf resolves the named schema of an object or rows variable.
func schemaOf(c *collector, path string, vd *VariableDef, schemas *SchemaSet) *Schema {
	if len(vd.Schema) == 0 {
		c.hint(CodeMissing, path+"/schema", "an "+vd.Type+" variable names its schema", `"schema": "Person", or one of `+strings.Join(VocabularyNames(), ", "))
		return nil
	}
	var name string
	if err := json.Unmarshal(vd.Schema, &name); err != nil {
		c.add(CodeBadJSON, path+"/schema", "an object's schema is a schema name")
		return nil
	}
	sch, ok := schemas.Lookup(name)
	if !ok {
		c.hint(CodeUnknownSchema, path+"/schema", "unknown schema "+quote(name), "define it first, or use one of "+strings.Join(schemas.Names(), ", "))
		return nil
	}
	return sch
}

// rowsColumns reads the schema of a rows variable: a schema name, or an
// inline object of column name to type in written order. It returns the
// columns and the schema name, "" for an inline schema.
func rowsColumns(c *collector, path string, vd *VariableDef, schemas *SchemaSet, used map[string]*Schema) ([]formula.Field, string, bool) {
	if len(vd.Schema) == 0 {
		c.hint(CodeMissing, path+"/schema", "a rows variable has a schema", `"schema": {"sku": "text", "qty": "int"} or a schema name`)
		return nil, "", false
	}
	var name string
	if json.Unmarshal(vd.Schema, &name) == nil {
		sch, ok := schemas.Lookup(name)
		if !ok {
			c.hint(CodeUnknownSchema, path+"/schema", "unknown schema "+quote(name), "define it first, or use one of "+strings.Join(schemas.Names(), ", "))
			return nil, "", false
		}
		used[upper(sch.Name)] = sch
		return sch.Type.Fields(), sch.Name, true
	}
	var cols map[string]string
	if err := json.Unmarshal(vd.Schema, &cols); err != nil {
		c.add(CodeBadJSON, path+"/schema", "a rows schema is a schema name or an object of column name to type")
		return nil, "", false
	}
	order := vd.schemaOrder
	if order == nil {
		for k := range cols {
			order = append(order, k)
		}
	}
	if len(order) == 0 {
		c.add(CodeMissing, path+"/schema", "a rows schema has at least one column")
		return nil, "", false
	}
	var fields []formula.Field
	seen := map[string]bool{}
	ok := true
	for _, col := range order {
		cpath := path + "/schema/" + col
		if !c.checkName(cpath, col, reservedFieldNames) {
			ok = false
			continue
		}
		if seen[upper(col)] {
			c.addf(CodeDuplicate, cpath, "column %s is declared twice", quote(col))
			ok = false
			continue
		}
		seen[upper(col)] = true
		ft, _, good := schemas.fieldType(c, cpath, cols[col], "")
		if !good {
			ok = false
			continue
		}
		if ft.Kind == formula.KindObject {
			if sch, found := schemas.Lookup(ft.Schema()); found {
				used[upper(sch.Name)] = sch
			}
		}
		fields = append(fields, formula.Field{Name: col, Type: ft})
	}
	return fields, "", ok
}

// scopeOf makes the binding scope of the class: every variable by its
// declared name and type, leaving out the ones whose type is not known
// yet. Names were checked already, so a failure here is a programming
// error.
func scopeOf(vars []*Variable) *formula.Scope {
	scope, err := formula.NewScope(scopeVariables(vars)...)
	if err != nil {
		panic("model: scope of checked variables: " + err.Error())
	}
	return scope
}

// scopeVariables are the variables of a class as the binder takes them,
// leaving out the ones whose type is not known yet.
func scopeVariables(vars []*Variable) []formula.Variable {
	fv := make([]formula.Variable, 0, len(vars)+1)
	for _, v := range vars {
		if v.Type.Kind == formula.KindNull {
			continue
		}
		fv = append(fv, formula.Variable{Name: v.Name, Type: v.Type})
	}
	return fv
}

// inferTypes binds the computed variables that declared no type, each
// after the ones it reads, and gives each its formula's type. A variable
// the formula reads before it has a type is still unknown to the binder,
// which is what the order prevents; a cycle among them is reported once.
func inferTypes(c *collector, cls *Class, schemas *SchemaSet) {
	var pending []string
	deps := map[string][]string{}
	for _, v := range cls.Variables {
		if v.Computed && v.Type.Kind == formula.KindNull {
			pending = append(pending, v.Name)
			node, err := formula.Parse(v.Def.Formula)
			if err != nil {
				c.formulaErr("/variables/"+v.Name+"/formula", err)
				continue
			}
			for _, name := range namesIn(node) {
				if dv, ok := cls.Variable(name); ok && dv.Computed && dv.Type.Kind == formula.KindNull {
					deps[v.Name] = append(deps[v.Name], dv.Name)
				}
			}
		}
	}
	order, cycle := topoOrder(pending, deps)
	if cycle != nil {
		c.hint(CodeCycle, "/variables/"+cycle[0]+"/formula", "these formulas depend on each other: "+cycleText(cycle), "one of them must stop reading the other, or become triggered")
	}
	for _, name := range order {
		v, _ := cls.Variable(name)
		path := "/variables/" + name
		mode := formula.ModeAuto
		if v.Mode == ModeTriggered {
			mode = formula.ModeAny
		}
		prog, err := c.bind(v.Def.Formula, scopeOf(cls.Variables), mode)
		if err != nil {
			c.formulaErr(path+"/formula", err)
			continue
		}
		if prog.Result.Kind == formula.KindNull {
			c.hint(CodeMissing, path+"/type", "the formula is always blank, so its type cannot be inferred", `declare "type"`)
			continue
		}
		if c.readsClass(prog) {
			// The type of a stored value may not follow another class: it
			// is the layout of this class's documents and of their history.
			c.hint(CodeMissing, path+"/type", "a variable computed from the documents of a class declares its type, which must stay what it is when that class changes (ADR-0046)",
				fmt.Sprintf(`"type": %q`, prog.Result.String()))
		}
		v.Type = prog.Result
		v.Formula = prog
		v.Deps = depsOf(prog)
		v.Sources = sourcesOf(prog)
		checkInferred(c, path, v)
	}
}

// checkInferred applies the property checks a declared type would have
// had to a variable that took its formula's type.
func checkInferred(c *collector, path string, v *Variable) {
	vd := v.Def
	if vd.Scale != nil {
		c.hint(CodeNotAllowed, path+"/scale", "a computed variable without a type takes its formula's scale", "declare the type as decimal(s) or money(s) to fix the scale")
	}
	if vd.Class != "" && v.Type.Kind != formula.KindRef {
		c.add(CodeNotAllowed, path+"/class", "only a ref has a class")
	}
	if len(vd.Values) > 0 && v.Type.Kind != formula.KindText {
		c.add(CodeNotAllowed, path+"/values", "only a text variable has values (ADR-0023)")
	}
	numeric := v.Type.Kind == formula.KindNumber || v.Type.IsExact()
	if (len(vd.Min) > 0 || len(vd.Max) > 0) && !numeric {
		c.add(CodeNotAllowed, path+"/min", "only number, int, decimal and money variables have a range (ADR-0034)")
	}
}

// namesIn lists the variable names a parsed formula mentions, the base of
// every path included. Inside LOOKUP and QUERY a bare name is a column of
// the source and the document's variables are THIS.name (ADR-0020).
func namesIn(n formula.Node) []string {
	var names []string
	var walk func(formula.Node, bool)
	walk = func(n formula.Node, inQuery bool) {
		switch n := n.(type) {
		case *formula.Ident:
			if !inQuery {
				names = append(names, n.Name)
			}
		case *formula.Member:
			if !inQuery {
				names = append(names, n.Base)
			}
		case *formula.ThisRef:
			names = append(names, n.Name)
		case *formula.Unary:
			walk(n.X, inQuery)
		case *formula.Binary:
			walk(n.L, inQuery)
			walk(n.R, inQuery)
		case *formula.If:
			walk(n.Cond, inQuery)
			walk(n.Then, inQuery)
			walk(n.Else, inQuery)
		case *formula.Call:
			query := n.Name == "LOOKUP" || n.Name == "QUERY"
			for i, a := range n.Args {
				if query && i < 2 {
					continue
				}
				walk(a, inQuery || query)
			}
		}
	}
	walk(n, false)
	return names
}

// bindAs binds a formula that must produce a value of the declared type.
func bindAs(c *collector, path, src string, scope *formula.Scope, mode formula.Mode, want formula.Type) *formula.Program {
	prog, err := c.bind(src, scope, mode)
	if err != nil {
		c.formulaErr(path, err)
		return nil
	}
	prog.ClaimExact(want)
	if msg, hint := typeFits(want, prog.Result); msg != "" {
		c.hint(CodeTypeMismatch, path, msg, hint)
		return nil
	}
	return prog
}

// bindCondition binds a guard, a rule condition or an access rule, which
// must be bool. classes restricts the function classes beyond the mode,
// for a read rule (ADR-0010).
func bindCondition(c *collector, path, src string, scope *formula.Scope, what string) *formula.Program {
	if src == "" {
		return nil
	}
	prog, err := c.bind(src, scope, formula.ModeAny)
	if err != nil {
		c.formulaErr(path, err)
		return nil
	}
	if prog.Result.Kind != formula.KindBool && prog.Result.Kind != formula.KindNull {
		c.hint(CodeNotACondition, path, what+" must be a condition, got "+prog.Result.String(), "compare it, for instance <> 0 or <> \"\"")
		return nil
	}
	return prog
}

// typeFits says whether a formula of type got may be stored in a variable
// of type want, with the message and the hint when it may not. A blank
// fits every type; an exact result fits a variable of the same kind at
// the same or a larger scale (ADR-0022).
func typeFits(want, got formula.Type) (msg, hint string) {
	if got.Kind == formula.KindNull || want.Kind == got.Kind && !want.IsExact() {
		return "", ""
	}
	if want.IsExact() && got.IsExact() {
		if want.Kind != got.Kind {
			if want.Kind == formula.KindMoney {
				return fmt.Sprintf("the variable is %s but the formula is %s", want, got), "MONEY(amount, currency) makes a decimal money"
			}
			return fmt.Sprintf("the variable is %s but the formula is %s", want, got), "AMOUNT(money) takes the amount of a money"
		}
		if got.Scale > want.Scale {
			return fmt.Sprintf("the formula has scale %d but the variable %s keeps %d places", got.Scale, want, want.Scale),
				fmt.Sprintf("round it: ROUND(..., %d) or ROUND(..., %d, \"half_even\")", want.Scale, want.Scale)
		}
		return "", ""
	}
	msg = fmt.Sprintf("the variable is %s but the formula is %s", want, got)
	switch {
	case want.Kind == formula.KindMoney && (got.Kind == formula.KindNumber || got.Kind == formula.KindDecimal):
		hint = "MONEY(amount, currency) makes a money value"
	case want.IsExact() && got.Kind == formula.KindNumber:
		hint = fmt.Sprintf("DECIMAL(x, %d) makes a number exact", want.Scale)
	case want.Kind == formula.KindNumber && got.IsExact():
		hint = "NUMBER(x) makes an exact value a number"
	case want.Kind == formula.KindText:
		hint = "TEXT(x) renders a value as text"
	case got.Kind == formula.KindText && (want.Kind == formula.KindNumber || want.IsExact()):
		hint = "VALUE, DECIMALVALUE or MONEYVALUE read a number from text"
	case want.Kind == formula.KindDate && got.Kind == formula.KindDateTime:
		hint = "DATEOF(x) takes the day of an instant"
	case want.Kind == formula.KindDateTime && got.Kind == formula.KindDate:
		hint = "DATETIME(x) is the day's midnight"
	case want.Kind == formula.KindDate && got.Kind == formula.KindText:
		hint = "DATEVALUE(x) reads a day from text"
	}
	return msg, hint
}

// depsOf lists the variables a program reads, by declared name: a leaf
// path counts as its variable.
func depsOf(prog *formula.Program) []string {
	seen := map[string]bool{}
	var deps []string
	for _, s := range prog.Slots {
		name := s.Name
		if i := strings.IndexByte(name, '.'); i >= 0 {
			name = name[:i]
		}
		if !seen[upper(name)] {
			seen[upper(name)] = true
			deps = append(deps, name)
		}
	}
	return deps
}

// sourcesOf lists the explicit tables a program reads, by name, once each.
func sourcesOf(prog *formula.Program) []string {
	var out []string
	for _, q := range prog.Queries {
		found := false
		for _, s := range out {
			if upper(s) == upper(q.Source) {
				found = true
			}
		}
		if !found {
			out = append(out, q.Source)
		}
	}
	return out
}

// checkTrigger reads one trigger of a triggered variable.
func checkTrigger(c *collector, path, text string, cls *Class, v *Variable) {
	kind, arg, _ := strings.Cut(text, ":")
	t := Trigger{Kind: kind, Arg: arg}
	switch kind {
	case "refresh":
		if arg != "" {
			c.add(CodeBadTrigger, path, "refresh takes no argument")
			return
		}
	case "enter", "exit":
		if arg == "" {
			c.addf(CodeBadTrigger, path, "%s names a state: %s:<state>", kind, kind)
			return
		}
		if !cls.States.Has(arg) {
			c.addf(CodeUnknownState, path, "unknown state %s", quote(arg))
			return
		}
	case "transition":
		if arg == "" {
			c.add(CodeBadTrigger, path, "transition names a transition: transition:<name>")
			return
		}
		found := false
		for _, t := range cls.Def.Transitions {
			if t.Name == arg {
				found = true
			}
		}
		if !found {
			c.addf(CodeUnknownTransition, path, "unknown transition %s", quote(arg))
			return
		}
	case "table", "schedule":
		// Accepted and never fired until ADR-0043; refused until the
		// phase that runs them, as a binding a build lacks is.
		c.hint(CodeNotSupported, path, "this build does not recompute a variable on "+kind+":", "use refresh, and send the command when the "+map[string]string{"table": "table changes", "schedule": "time comes"}[kind]+"; "+kind+": arrives with the set-wise work of a later phase")
		return
	default:
		c.hint(CodeBadTrigger, path, "unknown trigger "+quote(text), "one of refresh, enter:<state>, exit:<state>, transition:<name>")
		return
	}
	// A frozen document computes nothing (ADR-0043, item 8): a trigger
	// that can fire on a move out of a terminal state is refused.
	if why := frozenTrigger(cls, RuleDef{On: kind, State: arg, Name: arg}); why != "" {
		c.hint(CodeNotAllowed, path, "a variable cannot recompute on "+text+": "+why+", and a frozen document computes nothing (ADR-0039)",
			"name the transition that arrives from a live state, or the entry of a state only live states lead to")
		return
	}
	v.Triggers = append(v.Triggers, t)
}

// compileStates checks the states block.
func compileStates(c *collector, def *StatesDef) *States {
	if def == nil {
		return nil
	}
	s := &States{Initial: def.Initial, List: def.List, Final: def.Final}
	if len(def.List) == 0 {
		c.add(CodeMissing, "/states/list", "the states block lists the states")
		return s
	}
	seen := map[string]bool{}
	for i, st := range def.List {
		path := fmt.Sprintf("/states/list/%d", i)
		if !c.checkName(path, st, nil) {
			continue
		}
		if seen[upper(st)] {
			c.addf(CodeDuplicate, path, "state %s is listed twice", quote(st))
		}
		seen[upper(st)] = true
	}
	if def.Initial == "" {
		c.add(CodeMissing, "/states/initial", "the states block names the initial state")
	} else if !s.Has(def.Initial) {
		c.addf(CodeUnknownState, "/states/initial", "unknown state %s", quote(def.Initial))
	}
	for i, st := range def.Final {
		if !s.Has(st) {
			c.addf(CodeUnknownState, fmt.Sprintf("/states/final/%d", i), "unknown state %s", quote(st))
		}
	}
	return s
}

// compileOrder builds the evaluation orders and refuses cycles.
func compileOrder(c *collector, cls *Class) {
	var autos []string
	deps := map[string][]string{}
	for _, v := range cls.Variables {
		if v.Computed && v.Mode == ModeAuto {
			autos = append(autos, v.Name)
			deps[v.Name] = v.Deps
		}
	}
	if cls.Title != nil {
		autos = append(autos, "title")
		deps["title"] = depsOf(cls.Title)
	}
	order, cycle := topoOrder(autos, deps)
	if cycle != nil {
		path := "/variables/" + cycle[0] + "/formula"
		if cycle[0] == "title" {
			path = "/title"
		}
		c.hint(CodeCycle, path, "these formulas depend on each other: "+cycleText(cycle), "one of them must stop reading the other, or become triggered")
	}
	cls.Order = order
	var defaults []string
	ddeps := map[string][]string{}
	for _, v := range cls.Variables {
		if v.Default != nil {
			defaults = append(defaults, v.Name)
			ddeps[v.Name] = v.Deps
		}
	}
	order, cycle = topoOrder(defaults, ddeps)
	if cycle != nil {
		c.hint(CodeCycle, "/variables/"+cycle[0]+"/default", "these defaults depend on each other: "+cycleText(cycle), "one of them must not read the other")
	}
	cls.DefaultOrder = order
}

// compileColumns lays the variables out as columns and child tables.
func compileColumns(c *collector, cls *Class) {
	textType := formula.Scalar(formula.KindText)
	columnsOf := func(prefix string, t formula.Type, variable string, index bool) []Column {
		var cols []Column
		for _, leaf := range leavesOf(prefix, t) {
			cols = append(cols, Column{Name: leaf.Column, Type: leaf.Type, Variable: variable, Path: leaf.Path, Index: index})
			if leaf.Type.Kind == formula.KindMoney {
				cols = append(cols, Column{Name: leaf.Column + formula.CurrencySuffix, Type: textType, Variable: variable, Path: leaf.Path})
			}
		}
		return cols
	}
	for _, v := range cls.Variables {
		switch v.Type.Kind {
		case formula.KindRows:
			child := ChildTable{Variable: v.Name}
			for _, f := range v.Type.Fields() {
				child.Columns = append(child.Columns, columnsOf(f.Name, f.Type, v.Name, false)...)
			}
			if len(child.Columns)+3 > MaxColumns {
				c.addf(CodeTooManyColumns, "/variables/"+v.Name, "the child table would have %d columns; at most %d", len(child.Columns)+3, MaxColumns)
			}
			cls.Children = append(cls.Children, child)
		case formula.KindArray:
			elem := formula.Type{Kind: v.Type.Elem, Scale: v.Type.Scale}
			cls.Children = append(cls.Children, ChildTable{Variable: v.Name, Columns: columnsOf("value", elem, v.Name, false)})
		default:
			cls.Columns = append(cls.Columns, columnsOf(v.Name, v.Type, v.Name, v.Index)...)
			if v.Triggered() {
				// The moment of a triggered value, beside it (ADR-0043).
				asof, errc := MomentColumn(v.Name, MomentAsOf), MomentColumn(v.Name, MomentError)
				cls.Columns = append(cls.Columns,
					Column{Name: asof, Type: formula.DecimalType(0), Variable: v.Name, Path: asof, Moment: MomentAsOf},
					Column{Name: errc, Type: textType, Variable: v.Name, Path: errc, Moment: MomentError})
			}
		}
	}
	const systemColumns = 11
	if len(cls.Columns)+systemColumns > MaxColumns {
		c.addf(CodeTooManyColumns, "/variables", "the document table would have %d columns; at most %d", len(cls.Columns)+systemColumns, MaxColumns)
	}
}

// compileTransitions checks the transitions and binds their guards.
func compileTransitions(c *collector, cls *Class, defs []TransitionDef, scope *formula.Scope) {
	if len(defs) > 0 && cls.States == nil {
		c.add(CodeMissing, "/states", "transitions need a states block")
		return
	}
	names := map[string]bool{}
	for i, d := range defs {
		path := fmt.Sprintf("/transitions/%d", i)
		t := &Transition{Name: d.Name, To: d.To}
		if d.Name != "" {
			if c.checkName(path+"/name", d.Name, nil) {
				if names[upper(d.Name)] {
					c.addf(CodeDuplicate, path+"/name", "transition %s is declared twice", quote(d.Name))
				}
				names[upper(d.Name)] = true
			}
		} else if d.Guard == "" && d.After == "" && d.At == "" {
			c.hint(CodeMissing, path+"/guard", "an eventless transition, one without a name, needs a guard", "name the transition, or give it a guard, an after or an at")
		}
		from, err := fromStates(d.From)
		if err != nil || len(from) == 0 {
			c.add(CodeMissing, path+"/from", "from is a state or a list of states")
		}
		for j, st := range from {
			if !cls.States.Has(st) {
				p := path + "/from"
				if len(from) > 1 {
					p = fmt.Sprintf("%s/%d", p, j)
				}
				c.addf(CodeUnknownState, p, "unknown state %s", quote(st))
			}
		}
		t.From = from
		if d.To == "" {
			c.add(CodeMissing, path+"/to", "a transition has a target state")
		} else if !cls.States.Has(d.To) {
			c.addf(CodeUnknownState, path+"/to", "unknown state %s", quote(d.To))
		} else if !cls.States.IsFinal(d.To) {
			// A document in a terminal state is frozen; it may move to
			// another terminal state and never back to a live one
			// (ADR-0039).
			for _, st := range from {
				if cls.States.IsFinal(st) {
					c.hint(CodeNotAllowed, path+"/to", "a transition out of the terminal state "+quote(st)+" must lead to a terminal state; "+quote(d.To)+" is not one",
						"list "+quote(d.To)+" under states.final, or drop the transition")
					break
				}
			}
		}
		t.Guard = bindCondition(c, path+"/guard", d.Guard, scope, "a guard")
		t.GuardSrc = d.Guard
		compileTimer(c, cls, t, d, path, scope)
		cls.Transitions = append(cls.Transitions, t)
	}
}

// compileTimer checks the after or the at of a timed transition
// (ADR-0047, items 9 and 14). The moment of an at is the document's own:
// the engine works it out when the document enters the state and again
// when a commit changes what it reads, so it may read neither the clock,
// nor who asks, nor anything outside the document.
func compileTimer(c *collector, cls *Class, t *Transition, d TransitionDef, path string, scope *formula.Scope) {
	if d.After == "" && d.At == "" {
		return
	}
	if d.After != "" && d.At != "" {
		c.hint(CodeNotAllowed, path+"/at", "a transition has an after or an at, not both",
			"after counts from the entry into the state; at is a moment the document holds")
		return
	}
	for _, st := range t.From {
		if cls.States.IsFinal(st) {
			c.hint(CodeNotAllowed, path+"/from", "a timed transition may not leave the terminal state "+quote(st)+": a document in a terminal state is frozen, and nothing moves it by itself",
				"drop the after or the at, and take the transition by its name")
			break
		}
	}
	if d.After != "" {
		secs, ok := ParseAfter(d.After)
		if !ok {
			c.hint(CodeBadValue, path+"/after", "after is a length of time: a whole number above zero and d, h, m or s; got "+quote(d.After),
				`"30d" is thirty days of 24 hours, "12h" twelve hours, "90m" ninety minutes and "45s" forty-five seconds; at most a thousand years`)
			return
		}
		t.After = secs
		return
	}
	for _, st := range t.From {
		if st == t.To {
			c.hint(CodeNotAllowed, path+"/at", "an at on a transition into its own state would fire again at once: the moment has come, and the document is in "+quote(st)+" again",
				"after is how a stay repeats: it counts from every entry")
			return
		}
	}
	prog, err := c.bind(d.At, scope, formula.ModeAny)
	if err != nil {
		c.formulaErr(path+"/at", err)
		return
	}
	if k := prog.Result.Kind; k != formula.KindDate && k != formula.KindDateTime {
		c.hint(CodeTypeMismatch, path+"/at", "an at gives a date or a datetime, got "+prog.Result.String(),
			"name a date the document holds, or compute one: ADDDAYS(due, 1)")
		return
	}
	switch {
	case prog.Calls("NOW") || prog.Calls("TODAY"):
		c.hint(CodeNotAllowed, path+"/at", "an at may not read the clock: its moment is worked out when the document enters the state, and stands until a value it reads changes",
			`a time counted from the entry is an after: "after": "30d"; a time counted from a date is that date moved: ADDDAYS(due, 30)`)
	case prog.Calls("USER") || prog.Calls("INGROUP") || prog.Calls("USERKIND") || prog.Calls("ACTINGFOR"):
		c.hint(CodeNotAllowed, path+"/at", "an at may not read who asks: its moment is the document's, the same for everyone",
			"compute the moment from the document's own values")
	case len(prog.Queries) > 0:
		c.hint(CodeNotAllowed, path+"/at", "an at may not read another document or a table: its moment follows from the document's own values, so that a commit of the document is what moves it",
			"keep the date in a triggered variable of the document, and name that variable")
	default:
		t.At, t.AtSrc = prog, d.At
	}
}

// compileAccess binds the class-level access rules. A read rule compiles
// to a SQL predicate (ADR-0010) and so may use pure and context functions
// only.
func compileAccess(c *collector, cls *Class, def *AccessDef, scope *formula.Scope) {
	if def == nil {
		return
	}
	a := &Access{Transition: map[string]*formula.Program{}}
	a.Read = bindCondition(c, "/access/read", def.Read, scope, "a read rule")
	if a.Read != nil {
		for _, cl := range a.Read.Classes {
			if cl == formula.ClassLookup || cl == formula.ClassQuery {
				c.hint(CodeNotAllowed, "/access/read", "a read rule may use pure and context functions only: it runs inside every query as a predicate",
					"move the lookup into a computed variable and test that variable")
				a.Read = nil
				break
			}
		}
	}
	if a.Read != nil && a.Read.ReadsStates {
		c.hint(CodeNotAllowed, "/access/read", "a read rule may not call a function of a state: it runs inside every query as a predicate, and those functions read the events of one document",
			"keep the answer in a triggered variable and test that variable")
		a.Read = nil
	}
	a.Create = bindCondition(c, "/access/create", def.Create, scope, "a create rule")
	a.Update = bindCondition(c, "/access/update", def.Update, scope, "an update rule")
	a.Delete = bindCondition(c, "/access/delete", def.Delete, scope, "a delete rule")
	for name, src := range def.Transition {
		path := "/access/transition/" + name
		if !hasTransition(cls, name) {
			c.addf(CodeUnknownTransition, path, "unknown transition %s", quote(name))
			continue
		}
		if prog := bindCondition(c, path, src, scope, "a transition rule"); prog != nil {
			a.Transition[name] = prog
		}
	}
	cls.Access = a
}

// compileRights checks the rights table (ADR-0028).
func compileRights(c *collector, cls *Class, def *Definition, scope *formula.Scope) {
	if len(def.Rights) == 0 {
		return
	}
	cls.Rights = map[string]map[string]*Right{}
	states := def.rightsStates
	if states == nil {
		for st := range def.Rights {
			states = append(states, st)
		}
	}
	for _, st := range states {
		spath := "/rights/" + st
		if st != "*" && !cls.States.Has(st) {
			c.addf(CodeUnknownState, spath, "unknown state %s", quote(st))
			continue
		}
		row := map[string]*Right{}
		vars := def.RightsOrder[st]
		if vars == nil {
			for name := range def.Rights[st] {
				vars = append(vars, name)
			}
		}
		for _, name := range vars {
			vpath := spath + "/" + name
			key := name
			if name != "*" {
				v, ok := cls.Variable(name)
				if !ok {
					c.addf(CodeUnknownVariable, vpath, "unknown variable %s", quote(name))
					continue
				}
				key = v.Name
			}
			if _, dup := row[key]; dup {
				c.add(CodeDuplicate, vpath, "the variable is listed twice; names differ in case only")
				continue
			}
			rd := def.Rights[st][name]
			if rd == nil {
				c.add(CodeMissing, vpath, "a right has read, write or both")
				continue
			}
			r := &Right{}
			r.Read = rightRule(c, vpath+"/read", rd.Read, scope, true)
			r.Write = rightRule(c, vpath+"/write", rd.Write, scope, false)
			if r.Read == nil && r.Write == nil && !c.failed() {
				c.add(CodeMissing, vpath, "a right has read, write or both")
			}
			row[key] = r
		}
		cls.Rights[st] = row
	}
}

// rightRule reads one cell: a formula as text or a list of groups. A
// read right is compiled into every filter as a predicate (ADR-0041), so
// like a read rule it may use pure and context functions only; a write
// right keeps every function.
func rightRule(c *collector, path string, raw json.RawMessage, scope *formula.Scope, read bool) *RightRule {
	if len(raw) == 0 {
		return nil
	}
	var src string
	if json.Unmarshal(raw, &src) == nil {
		prog := bindCondition(c, path, src, scope, "a right")
		if prog == nil {
			return nil
		}
		if read {
			for _, cl := range prog.Classes {
				if cl == formula.ClassLookup || cl == formula.ClassQuery {
					c.hint(CodeNotAllowed, path, "a read right may use pure and context functions over the document's own variables only: it is compiled into every filter as a predicate",
						"move the lookup into a computed variable and test that variable")
					return nil
				}
			}
			if prog.ReadsStates {
				c.hint(CodeNotAllowed, path, "a read right may not call a function of a state: it is compiled into every filter as a predicate, and those functions read the events of one document",
					"keep the answer in a triggered variable and test that variable")
				return nil
			}
		}
		return &RightRule{Formula: prog, Src: src}
	}
	var groups []string
	if json.Unmarshal(raw, &groups) == nil {
		if len(groups) == 0 {
			c.add(CodeBadValue, path, "a group list names at least one group")
			return nil
		}
		return &RightRule{Groups: groups, Src: strings.Join(groups, ", ")}
	}
	c.add(CodeBadValue, path, "a right is a formula as text or a list of group names")
	return nil
}
