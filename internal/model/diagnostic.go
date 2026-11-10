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
// when it is not empty.
type Diagnostics []Diagnostic

func (ds Diagnostics) Error() string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.String()
	}
	return strings.Join(parts, "\n")
}

// Err returns the diagnostics as an error, or nil when there are none.
func (ds Diagnostics) Err() error {
	if len(ds) == 0 {
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
	// CodeBadTrigger: a trigger of a triggered variable is malformed.
	CodeBadTrigger = "bad_trigger"
	// CodeBadValue: an enum value, a range bound or a literal is wrong.
	CodeBadValue = "bad_value"
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
}

// bind binds a formula in the mode against the environment's sources.
func (c *collector) bind(src string, scope *formula.Scope, mode formula.Mode) (*formula.Program, error) {
	return formula.Bind(src, scope, formula.BindOptions{Mode: mode, Sources: c.env.Sources})
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
	d := Diagnostic{Code: CodeBadFormula, Path: path, Pos: -1, Message: err.Error()}
	var fe *formula.Error
	if errors.As(err, &fe) {
		d.Pos = fe.Pos
		d.Message = fe.Msg
	}
	c.list = append(c.list, d)
}

func (c *collector) failed() bool { return len(c.list) > 0 }
