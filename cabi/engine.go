package main

import "github.com/MojoHand70/fadodb/fado"

// engine makes the analytical engine of a database the library opens
// (ADR-0102, item 6): nil in libfadodb-core, which has none, and
// DuckDB's in libfadodb, built with the tag duckdb (engine_duckdb.go).
var engine func(memory string, threads int) fado.Analytics

// whichLibrary is the name of the library this build is.
func whichLibrary() string {
	if engine != nil {
		return "libfadodb"
	}
	return "libfadodb-core"
}
