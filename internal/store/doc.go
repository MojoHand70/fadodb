// Package store is the SQLite storage layer and the document engine of
// Phase 2: one file holds the classes, the schemas, the typed document
// tables of ADR-0014, the event log of ADR-0006 and ADR-0026, and the
// database's own metadata.
//
// A Store opens a database file as ADR-0035 configures it: WAL mode, one
// pinned writer connection on which every statement is prepared once, a
// checkpointer on its own connection so that a commit never pays a
// checkpoint, and pooled connections for reads. DefineClass compiles a
// definition with the model package, creates the current, history and
// child tables and the views a user sees, and keeps the class in memory
// with its storage layout. Create, Set and Delete are commits in the sense
// of ADR-0015: compute first, write last, in one transaction, with the
// recalculation of the auto variables, the history row, the child rows and
// the event. Preview runs the same pipeline and rolls back (ADR-0025).
// Every refusal is an *Error with a stable code, the variable it is about,
// a message and, where there is one, a hint.
//
// Not here yet: the statechart (Phase 3), rules (Phase 4), access rules and
// rights (Phase 5), LOOKUP against explicit tables, the filter DSL and the
// read-only SQL endpoint, the blob store, and a class migration under live
// documents. A class with documents cannot be redefined until that is
// decided.
package store
