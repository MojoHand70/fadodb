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
	Def    *Definition
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
// exit:<state>, transition:<name>, table:<name> or schedule:<spec>.
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
}

// Rule is one compiled rule; its actions are kept as written until Phase
// 4 gives them their meaning.
type Rule struct {
	On        string
	State     string
	Name      string
	Variables []string
	If        *formula.Program
	Actions   []json.RawMessage
}

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

// RightRule is a formula or a group list.
type RightRule struct {
	Formula *formula.Program
	Groups  []string
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
			for i, t := range v.Def.On {
				checkTrigger(c, fmt.Sprintf("%s/on/%d", path, i), t, cls, v)
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
	compileRules(c, cls, def.Rules, scope)
	compileAccess(c, cls, def.Access, scope)
	compileRights(c, cls, def, scope)

	if c.failed() {
		return nil, c.list
	}
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
	fv := make([]formula.Variable, 0, len(vars))
	for _, v := range vars {
		if v.Type.Kind == formula.KindNull {
			continue
		}
		fv = append(fv, formula.Variable{Name: v.Name, Type: v.Type})
	}
	scope, err := formula.NewScope(fv...)
	if err != nil {
		panic("model: scope of checked variables: " + err.Error())
	}
	return scope
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
		if arg == "" {
			c.addf(CodeBadTrigger, path, "%s takes an argument: %s:<%s>", kind, kind, map[string]string{"table": "name", "schedule": "spec"}[kind])
			return
		}
	default:
		c.hint(CodeBadTrigger, path, "unknown trigger "+quote(text), "one of refresh, enter:<state>, exit:<state>, transition:<name>, table:<name>, schedule:<spec>")
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
		}
	}
	const systemColumns = 7
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
		} else if d.Guard == "" {
			c.hint(CodeMissing, path+"/guard", "an eventless transition, one without a name, needs a guard", "name the transition, or give it a guard")
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
		cls.Transitions = append(cls.Transitions, t)
	}
}

var ruleTriggers = map[string]bool{"create": true, "values_change": true, "exit": true, "enter": true, "transition": true, "delete": true, "schedule": true}

// compileRules checks the rules' structure and binds their conditions.
func compileRules(c *collector, cls *Class, defs []RuleDef, scope *formula.Scope) {
	for i, d := range defs {
		path := fmt.Sprintf("/rules/%d", i)
		r := &Rule{On: d.On, State: d.State, Name: d.Name, Variables: d.Variables, Actions: d.Do}
		if !ruleTriggers[d.On] {
			c.hint(CodeBadValue, path+"/on", "unknown trigger "+quote(d.On), "one of create, values_change, exit, enter, transition, delete, schedule")
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
		}
		r.If = bindCondition(c, path+"/if", d.If, scope, "a rule condition")
		if len(d.Do) == 0 {
			c.add(CodeMissing, path+"/do", "a rule has at least one action")
		}
		for j, raw := range d.Do {
			var action struct {
				Action string `json:"action"`
			}
			if err := json.Unmarshal(raw, &action); err != nil || action.Action == "" {
				c.add(CodeMissing, fmt.Sprintf("%s/do/%d/action", path, j), "an action names what it does")
			}
		}
		cls.Rules = append(cls.Rules, r)
	}
}

func hasTransition(cls *Class, name string) bool {
	for _, t := range cls.Def.Transitions {
		if t.Name == name {
			return true
		}
	}
	return false
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
			if name != "*" {
				if _, ok := cls.Variable(name); !ok {
					c.addf(CodeUnknownVariable, vpath, "unknown variable %s", quote(name))
					continue
				}
			}
			rd := def.Rights[st][name]
			if rd == nil {
				c.add(CodeMissing, vpath, "a right has read, write or both")
				continue
			}
			r := &Right{}
			r.Read = rightRule(c, vpath+"/read", rd.Read, scope)
			r.Write = rightRule(c, vpath+"/write", rd.Write, scope)
			if r.Read == nil && r.Write == nil && !c.failed() {
				c.add(CodeMissing, vpath, "a right has read, write or both")
			}
			row[name] = r
		}
		cls.Rights[st] = row
	}
}

// rightRule reads one cell: a formula as text or a list of groups.
func rightRule(c *collector, path string, raw json.RawMessage, scope *formula.Scope) *RightRule {
	if len(raw) == 0 {
		return nil
	}
	var src string
	if json.Unmarshal(raw, &src) == nil {
		if prog := bindCondition(c, path, src, scope, "a right"); prog != nil {
			return &RightRule{Formula: prog}
		}
		return nil
	}
	var groups []string
	if json.Unmarshal(raw, &groups) == nil {
		if len(groups) == 0 {
			c.add(CodeBadValue, path, "a group list names at least one group")
			return nil
		}
		return &RightRule{Groups: groups}
	}
	c.add(CodeBadValue, path, "a right is a formula as text or a list of group names")
	return nil
}
