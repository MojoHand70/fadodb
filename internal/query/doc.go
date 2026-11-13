// Package query compiles the JSON filter of the Go API and the command set
// into SQL over a class's view, and guards the read-only SQL endpoint
// (docs/architecture.md, "Querying"; ADR-0038).
//
// A Filter names a class and gives a where formula over the class's
// variables and the system columns, an order, a limit, an offset and the
// fields to return. The formula is bound by the formula engine and
// translated by its SQL generator, so a filter means exactly what the same
// expression means in a formula: blanks read as 0 and "", comparisons
// follow ADR-0019, and a row on which the formula stops with an error does
// not match. Beside that exact predicate the compiler adds, for every
// comparison of a column with a literal in the top-level AND of the
// formula, the same comparison on the bare column, which SQLite can answer
// from an index; it is exact because a literal that a blank would not
// satisfy never matches a blank row, and when a blank would satisfy it,
// the blank rows are added back.
//
// Authorizer is the function the read-only SQL endpoint installs on its
// connection: SELECT, reads through the views and function calls are
// allowed; reads of the physical tables, writes, schema changes, pragmas
// and attachments are refused.
//
// The read rights of ADR-0028 are compiled into a filter for its
// principal (ADR-0041): a variable the where or the order names matches
// and sorts only in the rows whose state lets the principal read it, and
// a variable the principal may read in no state refuses the filter with
// forbidden. The read rule of the class, the row predicate of ADR-0010,
// joins the where of every filter for a caller who is not a content
// administrator (ADR-0049, ADR-0052). The caller's part of the rule, its INGROUP
// calls, is answered before the statement is written and the rule
// folded around the answers, so that what is left can seek an index; a
// predicate is kept for each outcome. A SQL statement sees every row and
// every column, as ADR-0028 decided for the views.
package query
