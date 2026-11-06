// Package formula holds the formula language: lexer, parser, function
// catalog and the loader of the conformance suite today; binder, bytecode,
// the Go virtual machine and the SQL generator as Phase 1 continues. One
// compiler, three targets; the browser VM in web/vm executes the same
// bytecode.
//
// The grammar and semantics are fixed by ADR-0019, the query functions by
// ADR-0020, and the suite under conformance/ is the specification.
package formula
