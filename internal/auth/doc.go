// Package auth will hold the optional package of users, groups, API keys
// and sessions that turns a credential into a principal for the server
// (ADR-0027), which comes with Phase 6.
//
// Not implemented yet. The access rules of a class are not here: the
// store enforces them and package query compiles the read rule into a
// filter (ADR-0049).
package auth
