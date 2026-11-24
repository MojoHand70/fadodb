package server

import (
	"net/http"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The codes a door answers with for what happens before a command runs
// or beside it, and to no Go program (ADR-0055, item 7; ADR-0059,
// item 2). A caller branches on them as on the codes of the engine.
const (
	// CodeUnauthenticated: the request does not say who sends it, or says
	// it with a credential the server does not know. The words are the
	// same whatever was wrong, so that a guess learns nothing.
	CodeUnauthenticated = "unauthenticated"
	// CodeTooLarge: the request is larger than the server reads, 16 MiB
	// unless the server was told otherwise. Nothing of it was run.
	CodeTooLarge = "too_large"
	// CodeTimeout: the command ran longer than the server lets one run,
	// 30 seconds unless the server was told otherwise, and was stopped. A
	// command that is one commit wrote nothing; migrate and drain, which
	// commit as they go, keep what they had done.
	CodeTimeout = "timeout"
	// CodeBusy: the server already runs as many commands as it takes on
	// at once, and none ended while the request waited. Nothing was run;
	// the same request can be sent again.
	CodeBusy = "busy"
	// CodeCallerLeft: the caller closed the request before the command
	// ended, and the command was stopped. Nobody reads this refusal: it
	// is what the log and the counts of the server say of such a request.
	// A command that is one commit wrote nothing; migrate and drain, which
	// commit as they go, keep what they had done.
	CodeCallerLeft = "caller_left"
)

// StatusCallerLeft is the status of a request whose caller left before
// it was answered. HTTP has no status for it, since nobody is there to
// read one; 499 is what proxies write in their logs for such a request,
// so the tools that read the log of a server know it.
const StatusCallerLeft = 499

// Status is the HTTP status a code is answered with (ADR-0055, item 8),
// for the proxies and the tools between a caller and the door. A caller
// reads the code. The empty code is a command that ran.
func Status(code string) int {
	switch code {
	case "":
		return http.StatusOK
	case command.CodeBadJSON:
		return http.StatusBadRequest
	case CodeUnauthenticated, store.CodeSignInRefused:
		return http.StatusUnauthorized
	case store.CodeTooManyAttempts:
		return http.StatusTooManyRequests
	case store.CodeForbidden, store.CodeReasonRequired:
		return http.StatusForbidden
	case command.CodeUnknownCommand, store.CodeUnknownClass, store.CodeUnknownDocument,
		store.CodeUnknownTable, store.CodeUnknownSchema, store.CodeUnknownAction,
		store.CodeUnknownBlob, store.CodeUnknownRow, store.CodeUnknownUser, store.CodeUnknownGroup, store.CodeUnknownKey, store.CodeUnknownInlet, store.CodeUnknownSession,
		store.CodeUnknownClient, store.CodeUnknownGrant, store.CodeUnknownApp, store.CodeUnknownInvitation, store.CodeUnknownPasskey:
		return http.StatusNotFound
	case store.CodeKeyReused, store.CodeDuplicate, store.CodeWrongState,
		store.CodeTerminal, store.CodeGuardFalse:
		return http.StatusConflict
	case CodeTooLarge, store.CodeBlobTooLarge:
		return http.StatusRequestEntityTooLarge
	case command.CodeInternal, store.CodeStorage:
		return http.StatusInternalServerError
	case store.CodeClosed, CodeBusy:
		return http.StatusServiceUnavailable
	case CodeTimeout:
		return http.StatusGatewayTimeout
	case CodeCallerLeft:
		return StatusCallerLeft
	}
	// Every other refusal: the request was understood and the engine
	// does not carry it out.
	return http.StatusUnprocessableEntity
}
