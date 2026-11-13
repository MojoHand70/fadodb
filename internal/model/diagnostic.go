package model

import (
	"errors"
	"fmt"
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
	// CodeTooDeep: objects nest more than MaxObjectDepth deep.
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
	// CodeUnknownState, CodeUnknownVariable, CodeUnknownTransition: a
	// reference to a state, variable or transition the class lacks.
	CodeUnknownState      = "unknown_state"
	CodeUnknownVariable   = "unknown_variable"
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
	// CodeRuleLeftOut is the note of an access rule a class leaves out:
	// the class is accepted, and nobody but a content administrator
	// passes that rule (ADR-0052, item 5).
	CodeRuleLeftOut = "rule_left_out"
	// CodeTooManyColumns: a generated table would exceed MaxColumns.
	CodeTooManyColumns = "too_many_columns"
	// CodeNotSupported: a construct the format allows that the engine
	// does not implement yet.
	CodeNotSupported = "not_supported"
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
}

// errUnbound says a formula that reads the documents of a class was left
// unbound in a shallow compilation. It is never a finding.
var errUnbound = errors.New("the formula reads a class and was not bound")

// bind binds a formula in the mode against the environment's sources:
// its explicit tables and the documents of its classes.
func (c *collector) bind(src string, scope *formula.Scope, mode formula.Mode) (*formula.Program, error) {
	r := &envSources{c: c}
	prog, err := formula.Bind(src, scope, formula.BindOptions{Mode: mode, Sources: r, States: c.states()})
	if err != nil && r.class && c.env.Shallow {
		return nil, errUnbound
	}
	return prog, err
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
	d := Diagnostic{Code: CodeBadFormula, Path: path, Pos: -1, Message: err.Error()}
	var fe *formula.Error
	if errors.As(err, &fe) {
		d.Pos = fe.Pos
		d.Message = fe.Msg
	}
	c.list = append(c.list, d)
}

func (c *collector) failed() bool { return len(c.list) > 0 }
