// Package rules is the place of the outbox worker and of the actions that
// leave the commit (ADR-0044, ADR-0045).
//
// Not implemented yet. The rules that run inside the commit, with the
// synchronous actions set, reset and emit (ADR-0043), are compiled by the
// model package and run by the store, which holds the document and the
// VM.
package rules
