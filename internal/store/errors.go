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
	// Path is the place in the class definition of the rule's action that
	// failed, such as /rules/2/do/0 (ADR-0043); empty otherwise.
	Path string `json:"path,omitempty"`
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
	if e.Path != "" {
		s += " in " + e.Path
	}
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
	// CodeUnknownDocument: no current document with that id, no such
	// version in its history, or a document the caller may not see, who
	// is answered in the same words (ADR-0049).
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
	// CodeNotAllowed: a function class a filter may not use, a SQL
	// statement the endpoint refuses, or At on a command that writes
	// (ADR-0042).
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
	// CodeStateless: Create or CreateMany on a class without states, a
	// stateless application that stores no documents (ADR-0040).
	CodeStateless = "stateless"
	// CodeUnknownTransition: the class has no named transition of that
	// name (ADR-0040).
	CodeUnknownTransition = "unknown_transition"
	// CodeWrongState: the transition exists and does not leave the
	// document's state.
	CodeWrongState = "wrong_state"
	// CodeGuardFalse: the transition's guard did not evaluate TRUE; an
	// erroring guard is FALSE.
	CodeGuardFalse = "guard_false"
	// CodeTooManyTransitions: more than eight transitions fired by
	// themselves after one command, eventless or timed (ADR-0008,
	// ADR-0047).
	CodeTooManyTransitions = "too_many_transitions"
	// CodeForbidden: the caller may not do this. A rule of the class
	// refused it (ADR-0049), or the right of a variable in the document's
	// state (ADR-0041), or the work is for a system group the caller is
	// not in (ADR-0052), or the caller acts for somebody and no
	// substitution says it may (ADR-0053). The path, or the variable,
	// names which.
	CodeForbidden = "forbidden"
	// CodeReasonRequired: a content administrator's command gets past a
	// rule and gives no reason (ADR-0052, item 4). The path is the place
	// of the rule.
	CodeReasonRequired = "reason_required"
	// CodeTooDeep: an action was queued by a chain of actions deeper than
	// MaxActionDepth (ADR-0044); its row is written dead.
	CodeTooDeep = "too_deep"
	// CodeUnknownAction: no action of the outbox has that key, or the
	// database has no action of that name.
	CodeUnknownAction = "unknown_action"
	// CodeUnknownParameter: an argument names a parameter the action's
	// definition does not have (ADR-0045).
	CodeUnknownParameter = "unknown_parameter"
	// CodeNotRegistered: the action is defined and nothing in this process
	// implements it: its Go function was not given to Open (ADR-0027).
	CodeNotRegistered = "not_registered"
	// CodeActionError: the Go function of an action returned an error, or
	// panicked; Message carries its words. A function that returns an
	// error of the store keeps that error's code.
	CodeActionError = "action_error"
	// CodeBadResult: what an action returned does not fit what its
	// definition declares under returns.
	CodeBadResult = "bad_result"
	// CodeOutboundOff: the action is sent as an HTTP request and this
	// process sends none: it was opened without Options.Outbound
	// (ADR-0045, item 7).
	CodeOutboundOff = "outbound_off"
	// CodeUnknownConnection: the host program gave this process no
	// connection of that name.
	CodeUnknownConnection = "unknown_connection"
	// CodeHostNotAllowed: the outbound rules of this process do not let a
	// request go to that host, over that scheme or port, or to the address
	// it resolves to; or the host is not one of the request's connection.
	CodeHostNotAllowed = "host_not_allowed"
	// CodeSecretUnavailable: the host program did not supply a secret a
	// request reads. No answer: the request is tried again.
	CodeSecretUnavailable = "secret_unavailable"
	// CodeNoReply: a request got no reply: the receiver was not reached, or
	// did not answer in time. Tried again.
	CodeNoReply = "no_reply"
	// CodeTryLater: the receiver answered 408, 429 or 5xx. Tried again, no
	// sooner than the reply's Retry-After asks.
	CodeTryLater = "try_later"
	// CodeRefused: the receiver answered with a status that is neither a
	// success nor a request to try again. A refusal.
	CodeRefused = "refused"
	// CodeNotOK: the receiver answered 2xx and the definition's ok is not
	// TRUE for the reply. A refusal.
	CodeNotOK = "not_ok"
	// CodeReplyTooLarge: the reply is larger than the outbound rules read.
	CodeReplyTooLarge = "reply_too_large"
	// CodeUnknownRow: a table.update addresses a row the table does not
	// have (ADR-0044).
	CodeUnknownRow = "unknown_row"
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
