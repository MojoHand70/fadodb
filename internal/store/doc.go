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
// Transition moves a document along a named transition of its class's
// statechart, with the eventless transitions that follow as further
// commits in the same transaction, and Transitions lists what may be
// taken (ADR-0040); a class without states stores no documents.
//
// Not here yet: rules (Phase 4), access rules and rights (Phase 5), and
// the server (Phase 6).
package store
