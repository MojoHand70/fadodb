// Package rules is the place of the actions that leave the database
// (ADR-0045).
//
// Not implemented yet. The rules that run inside the commit, with the
// synchronous actions set, reset, refresh and emit (ADR-0043), are
// compiled by the model package and run by the store, which holds the
// document and the VM; the outbox and the engine's own actions
// (ADR-0044) are the store's too, since they write to its database.
package rules
