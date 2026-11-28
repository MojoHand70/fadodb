// Package postgres is FadoDB's way to Postgres (ADR-0035, Phase 10): the
// driver the engine's statements reach a Postgres database through. It
// is a module of its own, so that the driver, pgx, and what it brings
// stay out of the core, which is the standard library and SQLite
// (ADR-0002).
//
// The engine writes a parameter as ? or as @name, as it does for its
// other databases. Postgres takes $1, $2 and so on, and wants every
// one it is given used. Open gives a database/sql handle whose
// statements are spelled Postgres's way on their way out: each ? and
// each @name becomes the next $n, a name written twice is one
// parameter, the arguments are put in that order, and a named argument
// the statement does not use is left out.
//
// The formulas' dialect for Postgres is formula.Postgres, in the core,
// which imports no driver; this module runs the conformance suite and
// the property test of the formula language against a server of
// Postgres 17 or later. They need one: FADODB_POSTGRES gives its address, as
// postgres://user:password@host:port/database. Without it the tests
// that need a server are skipped; with the value "must" they fail,
// which is how GitHub's checks make sure they ran.
package postgres
