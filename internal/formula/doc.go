// Package formula holds the formula language: lexer, parser, function
// catalog, binder, bytecode and the Go virtual machine today; the SQL
// generator as Phase 1 continues. One compiler, three targets; the browser
// VM in web/vm executes the same bytecode.
//
// Parse turns text into a tree, Bind turns the tree into a Program by the
// static typing rules of ADR-0019, and VM.Run executes a Program over the
// values of its slots. The grammar and semantics are fixed by ADR-0019,
// the query functions by ADR-0020, and the suite under conformance/ is the
// specification. Composite values and the query functions are not
// implemented yet.
package formula
