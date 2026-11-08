package store

import (
	"fmt"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// Error is a refusal of the store: a stable code from the list below or
// an evaluation code of the formula engine, the variable the finding is
// about when there is one, a message for a person and a hint when the
// engine knows the fix. A caller that drives the engine without a person
// watching branches on Code and Variable.
type Error struct {
	Code     string `json:"code"`
	Variable string `json:"variable,omitempty"`
	// Pos is the byte offset inside a formula when the finding is about a
	// place in one, such as a filter's where, and -1 or 0 otherwise.
	Pos     int    `json:"pos,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string {
	s := e.Message
	if e.Variable != "" {
		s = e.Variable + ": " + s
	}
	if e.Pos > 0 {
		s = fmt.Sprintf("%s (at offset %d)", s, e.Pos)
	}
	s += " [" + e.Code + "]"
	if e.Hint != "" {
		s += "; " + e.Hint
	}
	return s
}

// The codes of the store's own refusals. An evaluation error inside a
// commit keeps the code of ADR-0019 (div_by_zero, overflow, ...) and names
// the variable whose formula failed.
const (
	// CodeUnknownClass: no class of that name.
	CodeUnknownClass = "unknown_class"
	// CodeUnknownDocument: no current document with that id, or no such
	// version in its history.
	CodeUnknownDocument = "unknown_document"
	// CodeUnknownTable: no explicit table of that name.
	CodeUnknownTable = "unknown_table"
	// CodeUnknownVariable: a value names a variable or a field the class
	// does not have.
	CodeUnknownVariable = "unknown_variable"
	// CodeReadOnly: a value is given for a computed variable.
	CodeReadOnly = "read_only"
	// CodeRequired: a required input is blank.
	CodeRequired = "required"
	// CodeBadValue: a value is not of the variable's type, or an argument
	// is malformed.
	CodeBadValue = "bad_value"
	// CodeNotInValues: a text is outside the variable's enum list
	// (ADR-0023).
	CodeNotInValues = "not_in_values"
	// CodeOutOfRange: a number is outside the variable's min and max
	// (ADR-0034), or a text is longer than the database's text limit
	// (ADR-0021).
	CodeOutOfRange = "out_of_range"
	// CodeTooManyElements: a composite value has more than the hard cap of
	// ADR-0013.
	CodeTooManyElements = "too_many_elements"
	// CodeKeyReused: the idempotency key was used with a different command
	// (ADR-0026).
	CodeKeyReused = "key_reused"
	// CodeDuplicate: a document with that id exists already, or a table or
	// view name is taken.
	CodeDuplicate = "duplicate"
	// CodeNotSupported: a construct the design has that this build does
	// not implement yet.
	CodeNotSupported = "not_supported"
	// CodeClosed: the store is closed.
	CodeClosed = "closed"
	// CodeStorage: the database refused a statement; Message carries its
	// words.
	CodeStorage = "storage"
	// CodeBadFormula: a filter's where does not parse or bind; Pos says
	// where.
	CodeBadFormula = "bad_formula"
	// CodeNotAllowed: a function class a filter may not use, or a SQL
	// statement the endpoint refuses.
	CodeNotAllowed = "not_allowed"
	// CodeTooManyRows: a SQL statement returns more than MaxSQLRows.
	CodeTooManyRows = "too_many_rows"
	// CodeUnknownBlob: a File names a hash the blob store does not hold,
	// or a size that differs from the stored one (ADR-0033).
	CodeUnknownBlob = "unknown_blob"
	// CodeBlobTooLarge: a file exceeds the database's blob limit.
	CodeBlobTooLarge = "blob_too_large"
	// CodeTerminal: the document is in a terminal state and cannot change
	// (ADR-0039).
	CodeTerminal = "terminal"
)

func errf(code, variable, format string, args ...any) *Error {
	return &Error{Code: code, Variable: variable, Message: fmt.Sprintf(format, args...)}
}

func hintf(code, variable, message, hint string) *Error {
	return &Error{Code: code, Variable: variable, Message: message, Hint: hint}
}

// evalErr turns an evaluation error of a formula into the store's error,
// naming the variable.
func evalErr(variable string, err error) *Error {
	if ee, ok := err.(*formula.EvalError); ok {
		return &Error{Code: ee.Code, Variable: variable, Message: ee.Msg}
	}
	return &Error{Code: formula.ErrQueryFailed, Variable: variable, Message: err.Error()}
}

// storageErr wraps a database error.
func storageErr(what string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: CodeStorage, Message: what + ": " + err.Error()}
}
