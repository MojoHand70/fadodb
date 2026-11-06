// Package formula holds the formula language: lexer, parser, function
// catalog, binder, bytecode, the Go virtual machine and the SQL generator
// with its SQLite dialect; the DuckDB dialect follows as Phase 1
// continues. One compiler, three targets; the browser VM in web/vm
// executes the same bytecode.
//
// Parse turns text into a tree, Bind turns the tree into a Program by the
// static typing rules of ADR-0019, VM.Run executes a Program over the
// values of its slots, and GenerateSQL turns a Program into a value and an
// error expression for a Dialect. The grammar and semantics are fixed by
// ADR-0019, the query functions by ADR-0020, and the suite under
// conformance/ is the specification. Composite values and the query
// functions are not implemented yet.
package formula
