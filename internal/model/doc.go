// Package model defines document classes: the class definition as JSON,
// its validation into a compiled Class, the named schemas and the
// predefined vocabulary, and the diagnostics a definition earns.
//
// A Definition is the class as written (docs/architecture.md, ADR-0013,
// ADR-0018, ADR-0022, ADR-0023, ADR-0031 to ADR-0034). Compile checks
// every name, type and property, binds every formula against the class's
// variables with the function classes its mode allows (ADR-0015), builds
// the dependency graph of the computed variables and refuses a cycle, and
// lays the variables out as the typed columns and child tables of
// ADR-0014. Everything it refuses, it refuses at definition time, as a
// list of Diagnostics: a stable code, the JSON pointer of the element, the
// position inside a formula, a message and, where there is one, a fix. A
// caller that drives the engine without a person watching reads the code
// and the path; a person reads the message.
//
// The statechart, the rules with their actions, and the rights are
// compiled here and run by the store. An action a rule names is the
// engine's own or has a definition, an ActionSpec compiled from its own
// JSON (ADR-0045), against which the rule's arguments are typed. The
// class-level access rules (Phase 5) are part of the class format
// already: Compile checks their structure and binds their formulas so
// that a definition is valid as a whole, and the engine enforces them
// when their phase arrives.
package model
