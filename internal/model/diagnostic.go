package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Diagnostic is one definition-time finding.
type Diagnostic struct {
	// Code is a stable identifier of the kind of finding, one of the
	// Code constants. A caller branches on it.
	Code string `json:"code"`
	// Path is the JSON pointer of the element the finding is about,
	// such as /variables/total/formula or /transitions/1/guard.
	Path string `json:"path"`
	// Pos is the byte offset inside the formula at Path when the finding
	// is about a place in a formula, and -1 otherwise.
	Pos int `json:"pos"`
	// Message says what is wrong, for a person.
	Message string `json:"message"`
	// Hint says what to write instead, when the engine knows.
	Hint string `json:"hint,omitempty"`
	// Note says that the finding refuses nothing: the definition is
	// accepted with it. A rule a class leaves out is one (ADR-0052, item
	// 5).
	Note bool `json:"note,omitempty"`
}

func (d Diagnostic) String() string {
	s := d.Path
	if d.Pos >= 0 {
		s += fmt.Sprintf(" at offset %d", d.Pos)
	}
	s += ": " + d.Message + " [" + d.Code + "]"
	if d.Hint != "" {
		s += "; " + d.Hint
	}
	return s
}

// Diagnostics is the list of findings of one definition. It is an error
// when it holds a finding that is not a note.
type Diagnostics []Diagnostic

// Refuses reports whether the list holds a finding that refuses the
// definition: any finding that is not a note.
func (ds Diagnostics) Refuses() bool {
	for _, d := range ds {
		if !d.Note {
			return true
		}
	}
	return false
}

func (ds Diagnostics) Error() string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.String()
	}
	return strings.Join(parts, "\n")
}

// Err returns the diagnostics as an error, or nil when none of them
// refuses the definition.
func (ds Diagnostics) Err() error {
	if !ds.Refuses() {
		return nil
	}
	return ds
}

// Has reports whether a finding with the code is in the list.
func (ds Diagnostics) Has(code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// The diagnostic codes. The list is a contract once the validate command
// ships (ADR-0025): a new code is added, never renamed.
const (
	// CodeBadJSON: the definition is not the JSON the class format expects.
	CodeBadJSON = "bad_json"
	// CodeMissing: a required element is absent or empty.
	CodeMissing = "missing"
	// CodeBadName: a name is not an identifier, or uses the reserved
	// separator or suffix.
	CodeBadName = "bad_name"
	// CodeReservedName: a name the generated tables use, or a keyword.
	CodeReservedName = "reserved_name"
	// CodeDuplicate: a name declared twice, in any case.
	CodeDuplicate = "duplicate"
	// CodeUnknownType: a type the type system does not have.
	CodeUnknownType = "unknown_type"
	// CodeUnknownSchema: an object or rows type names a schema that is not
	// defined.
	CodeUnknownSchema = "unknown_schema"
	// CodeSchemaCycle: a schema contains itself.
	CodeSchemaCycle = "schema_cycle"
	// CodeTooDeep: something nests past its limit: objects below a
	// variable or sections in an outline more than four deep, or the
	// template of an action too deep.
	CodeTooDeep = "too_deep"
	// CodeNotAllowed: a property that this kind of variable, field or
	// element may not have.
	CodeNotAllowed = "not_allowed"
	// CodeBadFormula: a formula does not parse or bind; Pos says where.
	CodeBadFormula = "bad_formula"
	// CodeTypeMismatch: a formula's type is not the variable's.
	CodeTypeMismatch = "type_mismatch"
	// CodeNotACondition: a guard, rule condition or access rule is not
	// bool.
	CodeNotACondition = "not_a_condition"
	// CodeCycle: computed variables depend on each other in a loop.
	CodeCycle = "cycle"
	// CodeUnknownState: a reference to a state the class lacks.
	CodeUnknownState = "unknown_state"
	// CodeUnknownVariable: a reference to a variable the class lacks.
	CodeUnknownVariable = "unknown_variable"
	// CodeUnknownTransition: a reference to a transition the class
	// lacks.
	CodeUnknownTransition = "unknown_transition"
	// CodeUnknownClass: an action names a class the database does not
	// have (ADR-0044).
	CodeUnknownClass = "unknown_class"
	// CodeUnknownTable: an action names a table the database does not
	// have (ADR-0044).
	CodeUnknownTable = "unknown_table"
	// CodeUnknownAction: a rule names an action that has no definition
	// (ADR-0045).
	CodeUnknownAction = "unknown_action"
	// CodeUnknownParameter: a rule gives an action a parameter its
	// definition does not have (ADR-0045).
	CodeUnknownParameter = "unknown_parameter"
	// CodeUnknownSecret: a request names a secret its connection does not
	// declare (ADR-0045).
	CodeUnknownSecret = "unknown_secret"
	// CodeUnknownConnection: a definition names a connection the host
	// program did not give this process (ADR-0045).
	CodeUnknownConnection = "unknown_connection"
	// CodeBadTrigger: a trigger of a triggered variable is malformed.
	CodeBadTrigger = "bad_trigger"
	// CodeBadValue: an enum value, a range bound or a literal is wrong.
	CodeBadValue = "bad_value"
	// CodeRuleLeftOut: a note and no refusal. The class leaves an access
	// rule out; it is accepted, and nobody but a content administrator
	// passes that rule (ADR-0052, item 5).
	CodeRuleLeftOut = "rule_left_out"
	// CodeTooManyColumns: a generated table would exceed MaxColumns.
	CodeTooManyColumns = "too_many_columns"
	// CodeNotSupported: a construct the format allows that the engine
	// does not implement yet.
	CodeNotSupported = "not_supported"
	// CodeNoDescription: a note and no refusal. A variable, a section, a
	// state or a transition has no description, which a person reads as
	// help and an agent as what the thing is for (ADR-0068, item 3).
	CodeNoDescription = "no_description"
	// CodeReadsAll: a note and no refusal. A question a class declares,
	// a LOOKUP or a list over the documents of a class, compares no
	// variable an index answers, so it reads every document of that
	// class each time it is worked out (ADR-0075, item 5). On a small
	// class that is harmless; the note says the mend.
	CodeReadsAll = "reads_all"
	// CodeBuildsIndex: a note and no refusal, of a check before anything
	// is written. Taking the definition would make an index on a class
	// that already holds many documents, and the commits of the
	// database wait while it is built (ADR-0075, item 7).
	CodeBuildsIndex = "builds_index"
	// CodeUnknownApp: a class names an app the database does not have
	// (ADR-0077).
	CodeUnknownApp = "unknown_app"
	// CodeUnknownRole: a formula asks for a role its app does not name,
	// or asks for one where it belongs to no app (ADR-0100).
	CodeUnknownRole = "unknown_role"
	// CodeUnusedWords: a note and no refusal. The words in another
	// language name something the current version of the class, the
	// table or the schema no longer has (ADR-0068, item 6).
	CodeUnusedWords = "unused_words"
)

// collector gathers diagnostics while compiling, and carries the
// environment the formulas bind against.
type collector struct {
	list Diagnostics
	env  Env
	// self is the class being compiled, whose documents its own formulas
	// may read, and reads the classes its formulas read (ADR-0046).
	self  *Class
	reads []string
	// pure binds what fills a document as a pure formula: the values an
	// inlet gives are read from a message, and from nothing else
	// (ADR-0065, item 4).
	pure bool
	// live are the live variables of the class being compiled, by upper
	// case name: no formula of the class may read one (ADR-0076, item 4).
	live map[string]bool
	// listsOf and listsFor are set while the formula of a computed
	// variable is bound: its class, whose lists a live variable may work
	// figures out of (ADR-0081), and the variable itself.
	listsOf  *Class
	listsFor *Variable
	// wide are the notes of the questions the class declares that no
	// index serves (ADR-0075, item 5), and keys the indexes its
	// questions ask of the classes they read (item 2).
	wide Diagnostics
	keys []Key
	// roles are the roles of its app the class's formulas ask for with
	// INROLE (ADR-0100), in the order they were first met.
	roles []string
}

// liveRead says a formula reads a live variable. A live value is worked
// out when it is read, for the reader, and kept nowhere, so nothing that
// is kept or decided may stand on it: what a guard saw must be there to
// read afterwards.
type liveRead struct{ name string }

func (e *liveRead) Error() string {
	return e.name + " is a live variable: it is worked out when it is read and kept nowhere, so no formula of the class may read it"
}

// errUnbound says a formula that reads the documents of a class was left
// unbound in a shallow compilation. It is never a finding.
var errUnbound = errors.New("the formula reads a class and was not bound")

// bind binds a formula in the mode against the environment's sources:
// its explicit tables and the documents of its classes.
func (c *collector) bind(src string, scope *formula.Scope, mode formula.Mode) (*formula.Program, error) {
	r := &envSources{c: c}
	opts := formula.BindOptions{Mode: mode, Sources: r, States: c.states(), Loose: c.env.Lenient}
	// INROLE asks for a role of the class's app, one the app names; a
	// stored definition was checked when it was defined (ADR-0100).
	if c.self != nil && c.self.App != "" {
		opts.App = c.self.App
		if !c.env.Lenient {
			opts.Roles = []string{}
			if c.env.Roles != nil {
				if roles := c.env.Roles(c.self.App); roles != nil {
					opts.Roles = roles
				}
			}
		}
	}
	// REF() is the pointer to a document of the class being compiled,
	// and a REF that names a class names one the database has, written
	// as its definition writes it (ADR-0076, item 10). A definition
	// compiled alone, before the classes it names are at hand, is taken
	// at its word.
	if c.self != nil {
		opts.Class = c.self.Name
	}
	opts.Classes = classNames{c}
	if c.listsOf != nil {
		opts.Lists, opts.Live = listsOf(c.listsOf, c.listsFor, r), c.listsFor.Mode == ModeLive
	}
	prog, err := formula.Bind(src, scope, opts)
	if err != nil && r.class && c.env.Shallow {
		return nil, errUnbound
	}
	if err == nil {
		for _, role := range prog.Roles {
			if !slices.Contains(c.roles, role) {
				c.roles = append(c.roles, role)
			}
		}
	}
	if err == nil && len(c.live) > 0 {
		for _, s := range prog.Slots {
			name := s.Name
			if i := strings.IndexByte(name, '.'); i >= 0 {
				name = name[:i]
			}
			if c.live[upper(name)] {
				return nil, &liveRead{name: name}
			}
		}
	}
	return prog, err
}

// classNames says of a name whether it is a class, and how its
// definition writes it, for the class a REF names: the class being
// compiled and the classes of the environment. A definition compiled
// alone, before the classes it names are at hand, is taken at its word.
type classNames struct{ c *collector }

// ClassName implements formula.ClassNames.
func (n classNames) ClassName(name string) (string, bool) {
	c := n.c
	if c.self != nil && upper(name) == upper(c.self.Name) {
		return c.self.Name, true
	}
	if c.env.Classes != nil {
		if cls, ok := c.env.Classes(name); ok {
			return cls.Name, true
		}
	}
	return name, c.env.Shallow
}

// classOf finds a class a formula reads, the one being compiled among
// them; nil for a table, and for a class that is not at hand.
func (c *collector) classOf(name string) *Class {
	if c.env.Sources != nil {
		if _, table := c.env.Sources.Source(name); table {
			return nil
		}
	}
	if c.self != nil && upper(name) == upper(c.self.Name) {
		return c.self
	}
	if c.env.Classes != nil {
		if cls, ok := c.env.Classes(name); ok {
			return cls
		}
	}
	return nil
}

// indexedIn says a column of a class has an index: the id, the state
// and the two moments, which every class has one on, and a variable the
// class keeps one for (ADR-0075, item 2).
func indexedIn(cls *Class, column string) bool {
	switch upper(column) {
	case "ID", "STATE", "CREATED_AT", "UPDATED_AT":
		return true
	}
	v, ok := cls.Variable(column)
	return ok && cls.Keyed(v)
}

// ask notes that a question of the class being compiled is keyed by a
// variable of a class it reads, so that the engine keeps an index on it
// (ADR-0075, item 2). It answers false for what no index can be kept on.
func (c *collector) ask(src *Class, column, path string) bool {
	v, ok := src.Variable(column)
	if !ok || !keyable(v) {
		return false
	}
	for _, k := range c.keys {
		if k.Class == src.Name && k.Column == v.Name {
			return true
		}
	}
	c.keys = append(c.keys, Key{Class: src.Name, Column: v.Name, At: path})
	return true
}

// asks notes each LOOKUP of a formula over the documents of a class
// that no index of that class serves (ADR-0075, item 5): its condition
// compares no indexed variable, for equality, with a value of the
// document the formula belongs to. Such a lookup reads every document
// of the class each time it is worked out. A total over a whole class
// is one by its nature, until the analytical engine answers it.
func (c *collector) asks(path string, prog *formula.Program) {
	if prog == nil {
		return
	}
	for i := range prog.Queries {
		q := &prog.Queries[i]
		src := c.classOf(q.Source)
		if q.List != "" || src == nil {
			continue
		}
		switch {
		case q.Key != nil && indexedIn(src, q.IndexColumn):
			continue
		case q.Key != nil && c.ask(src, q.IndexColumn, path):
			// the engine makes the index for it
			continue
		case q.Key != nil:
			c.wide = append(c.wide, Diagnostic{Code: CodeReadsAll, Path: path, Pos: -1, Note: true,
				Message: "this LOOKUP reads every document of " + src.Name + " each time it is worked out: " + q.IndexColumn + ", which its condition compares, is a value no index can be kept on",
				Hint:    "compare a variable that is one plain value, a text, a number, a date or a pointer"})
		default:
			c.wide = append(c.wide, Diagnostic{Code: CodeReadsAll, Path: path, Pos: -1, Note: true,
				Message: "this LOOKUP reads every document of " + src.Name + " each time it is worked out: its condition compares no variable of " + src.Name + " for equality with a value of this document",
				Hint:    "begin the condition with such a comparison, as order = REF(): the engine keeps an index on every pointer; a total over a whole class has no index to be served by"})
		}
	}
}

// asksList is asks for a list of the documents of a class (ADR-0076):
// its condition, when it has one, compares an indexed variable for
// equality with a value that is no column of a listed document, or the
// list reads every document until its page is full. A list without a
// condition is read in the order of the ids, a page at a time, and is
// served by that.
func (c *collector) asksList(path string, src *Class, cond formula.Node, totals bool) {
	if cond == nil {
		if totals {
			// Totals over a whole class: every document is read to be
			// counted (ADR-0090).
			c.wide = append(c.wide, Diagnostic{Code: CodeReadsAll, Path: path, Pos: -1, Note: true,
				Message: "this list of totals reads every document of " + src.Name + " each time it is read: it has no condition",
				Hint:    "a program with the analytical engine answers it from its copy of the documents; without one, a condition that compares an indexed variable narrows what is read"})
		}
		return
	}
	served := false
	var walk func(formula.Node)
	walk = func(n formula.Node) {
		b, ok := n.(*formula.Binary)
		if !ok {
			return
		}
		switch b.Op {
		case formula.TokAnd:
			walk(b.L)
			walk(b.R)
		case formula.TokEq:
			for _, side := range [][2]formula.Node{{b.L, b.R}, {b.R, b.L}} {
				column := ""
				switch col := side[0].(type) {
				case *formula.Ident:
					column = col.Name
				case *formula.Call:
					if len(col.Args) == 0 && (col.Name == "STATE" || col.Name == "ID") {
						column = col.Name
					}
				}
				switch {
				case column == "" || namesColumn(side[1]):
				case indexedIn(src, column):
					served = true
				case ofDocument(side[1]) && c.ask(src, column, path):
					// A list keyed by a value of its document: the
					// engine makes the index for it (item 2).
					served = true
				}
			}
		}
	}
	walk(cond)
	if !served && totals {
		c.wide = append(c.wide, Diagnostic{Code: CodeReadsAll, Path: path, Pos: -1, Note: true,
			Message: "this list of totals reads every document of " + src.Name + " each time it is read: its condition compares no indexed variable of " + src.Name + " for equality with a value",
			Hint:    "a program with the analytical engine answers it from its copy of the documents; without one, begin the condition with such a comparison, as customer = THIS.customer"})
		return
	}
	if !served {
		c.wide = append(c.wide, Diagnostic{Code: CodeReadsAll, Path: path, Pos: -1, Note: true,
			Message: "this list reads every document of " + src.Name + " until its page is full: its condition compares no indexed variable of " + src.Name + " for equality with a value",
			Hint:    "begin the condition with such a comparison, as order = REF(): the engine keeps an index on every pointer, and on a variable that says \"index\": true"})
	}
}

// ofDocument says an operand of the condition of a list is a value of
// the document the list belongs to: the pointer to it, REF(), or one of
// its variables, THIS.name.
func ofDocument(n formula.Node) bool {
	switch n := n.(type) {
	case *formula.ThisRef:
		return true
	case *formula.Call:
		return n.Name == "REF" && len(n.Args) == 0
	}
	return false
}

// namesColumn says an operand of the condition of a list reads the
// listed document: a bare name in it is a column, and so are STATE()
// and ID().
func namesColumn(n formula.Node) bool {
	found := false
	walkNodes(n, func(n formula.Node) {
		switch n := n.(type) {
		case *formula.Ident, *formula.Member:
			found = true
		case *formula.Call:
			if len(n.Args) == 0 && (n.Name == "STATE" || n.Name == "ID") {
				found = true
			}
		}
	})
	return found
}

// states are the states of the class being compiled, which a function
// of a state may name as text (ADR-0047, item 6): an empty list for a
// class without states, and nil, which checks no name, where no class is
// being compiled.
func (c *collector) states() []string {
	if c.self == nil {
		return nil
	}
	if c.self.States == nil || c.self.States.List == nil {
		return []string{}
	}
	return c.self.States.List
}

// envSources resolves the sources a formula names: an explicit table of
// the environment, or else the documents of a class (ADR-0046), the
// class being compiled among them. class records that a name was not a
// table, whether a class of that name was found or not.
type envSources struct {
	c     *collector
	class bool
}

// Source implements formula.Sources.
func (r *envSources) Source(name string) (*formula.Source, bool) {
	c := r.c
	if c.env.Sources != nil {
		if src, ok := c.env.Sources.Source(name); ok {
			return src, true
		}
	}
	r.class = true
	if c.self != nil && upper(name) == upper(c.self.Name) {
		c.read(c.self.Name)
		return c.self.Source(), true
	}
	if c.env.Classes != nil {
		if cls, ok := c.env.Classes(name); ok {
			c.read(cls.Name)
			return cls.Source(), true
		}
	}
	return nil, false
}

// read notes a class the compiled class reads.
func (c *collector) read(name string) {
	for _, r := range c.reads {
		if upper(r) == upper(name) {
			return
		}
	}
	c.reads = append(c.reads, name)
}

// readsClass reports whether a program reads the documents of a class:
// a source of its lookups is no explicit table.
func (c *collector) readsClass(prog *formula.Program) bool {
	for _, q := range prog.Queries {
		if c.env.Sources == nil {
			return true
		}
		if _, table := c.env.Sources.Source(q.Source); !table {
			return true
		}
	}
	return false
}

func (c *collector) add(code, path, message string) {
	c.list = append(c.list, Diagnostic{Code: code, Path: path, Pos: -1, Message: message})
}

func (c *collector) addf(code, path, format string, args ...any) {
	c.add(code, path, fmt.Sprintf(format, args...))
}

func (c *collector) hint(code, path, message, hint string) {
	c.list = append(c.list, Diagnostic{Code: code, Path: path, Pos: -1, Message: message, Hint: hint})
}

// formulaErr records a parse or bind error of the formula at path, with
// the error's position when it has one.
func (c *collector) formulaErr(path string, err error) {
	if err == errUnbound {
		return
	}
	var lr *liveRead
	if errors.As(err, &lr) {
		c.hint(CodeNotAllowed, path, lr.Error(), `keep the value so that it can be read: "mode": "triggered" on `+lr.name)
		return
	}
	d := Diagnostic{Code: CodeBadFormula, Path: path, Pos: -1, Message: err.Error()}
	var fe *formula.Error
	if errors.As(err, &fe) {
		d.Pos = fe.Pos
		d.Message = fe.Msg
		if fe.Role {
			d.Code = CodeUnknownRole
			d.Hint = "name the role among the roles of the app, in its definition, and give it to people with set_role; a class of no app has no roles"
		}
		if fe.Mismatch {
			// a comparison of pointers to two classes (ADR-0076, item 10)
			d.Code = CodeTypeMismatch
			d.Hint = "a pointer compares with a pointer to the same class; REF() is the pointer to the document the formula belongs to"
		}
	}
	c.list = append(c.list, d)
}

func (c *collector) failed() bool { return len(c.list) > 0 }
