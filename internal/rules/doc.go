// Package rules is the place of the actions the engine sends as HTTP
// requests (ADR-0045): the client, which only a program that allows
// outbound calls links.
//
// Not implemented yet. The rules that run inside the commit, with the
// synchronous actions set, reset, refresh and emit (ADR-0043), are
// compiled by the model package and run by the store, which holds the
// document and the VM; the outbox, the engine's own actions (ADR-0044)
// and the calls of the Go functions a host registers (ADR-0045) are the
// store's too, since they are marked in its database.
package rules
