//go:build duckdb

package main

import (
	"github.com/MojoHand70/fadodb/analytics"
	"github.com/MojoHand70/fadodb/fado"
)

// libfadodb: the library with DuckDB, which keeps a copy of a
// database's documents beside its file and answers the questions that
// read everything from it, as fadodb serve does (ADR-0089, ADR-0102).
// It is linked to DuckDB's own shared library, which travels beside it,
// with the tag duckdb_use_lib of the Go driver.
func init() {
	engine = func(memory string, threads int) fado.Analytics {
		return analytics.Engine{Memory: memory, Threads: threads}
	}
}
