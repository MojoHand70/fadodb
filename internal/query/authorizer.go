package query

import (
	"strings"

	"modernc.org/sqlite"
)

// Authorizer makes the authorizer of the read-only SQL endpoint
// (docs/architecture.md, ADR-0038). It allows SELECT, reads of the views
// in the set and of whatever a view reads on the user's behalf, and
// function calls other than the ones that reach the file system; it
// refuses reads of the physical tables' columns and of the schema, every
// write, every schema change, pragmas, attachments and transactions.
// SQLite reports a refusal as an error of the statement, before it runs.
//
// When a statement reads no column of a table, count(*) or SELECT 1 over
// a view, SQLite reports one read of the underlying table with an empty
// column name and no view context. That read is allowed for the tables
// behind the views, doc_ and tbl_, since it reveals no column; it is
// refused for the engine's own tables and the schema.
func Authorizer(views map[string]bool) sqlite.AuthorizerFn {
	return func(action sqlite.AuthorizerActionCode, arg1, arg2, database, triggerOrView string) sqlite.AuthorizerReturnCode {
		switch action {
		case sqlite.AuthSelect, sqlite.AuthRecursive:
			return sqlite.AuthorizerOK
		case sqlite.AuthRead:
			if triggerOrView != "" || views[strings.ToUpper(arg1)] {
				return sqlite.AuthorizerOK
			}
			if arg2 == "" && backsAView(arg1) {
				return sqlite.AuthorizerOK
			}
			return sqlite.AuthorizerDeny
		case sqlite.AuthFunction:
			if deniedFunctions[strings.ToLower(arg2)] {
				return sqlite.AuthorizerDeny
			}
			return sqlite.AuthorizerOK
		}
		return sqlite.AuthorizerDeny
	}
}

// deniedFunctions reach outside the database.
var deniedFunctions = map[string]bool{"load_extension": true, "readfile": true, "writefile": true, "edit": true, "fsdir": true}

// The prefix every physical table carries, so that a view set can be
// derived: a name without one of these prefixes that is not an engine
// table is a view.
var physicalPrefixes = []string{"doc_", "tbl_"}

// EngineTables are the tables the engine keeps for itself; none is
// readable through the endpoint.
var EngineTables = map[string]bool{
	"SCHEMA_VERSION": true, "METADATA": true, "SCHEMAS": true, "CLASSES": true, "CLASS_VERSIONS": true,
	"TABLES_REGISTRY": true, "EVENTS": true, "EVENT_DOCS": true,
}

// backsAView reports whether a physical table is one a view reads: a
// document table or an explicit table.
func backsAView(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range physicalPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// IsPhysical reports whether a name is a generated table rather than a
// view a user may read.
func IsPhysical(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range physicalPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return EngineTables[strings.ToUpper(name)] || strings.HasPrefix(lower, "sqlite_")
}
