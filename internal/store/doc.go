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
// The rights table and the per-transition rules are enforced on every
// call (ADR-0041): a transition's rule and the write rights over the
// stored document before the assignments, every document returned
// projected for the principal, and the read rights of the variables a
// filter names compiled into its predicate. A call that reads or
// previews may run at a moment the caller chooses with At, and a preview
// reports it; a commit runs at the database's clock (ADR-0042).
//
// The rules of a class run inside the commit (ADR-0043): every commit
// raises a fixed list of triggers, the rules of each run in declaration
// order over the document as computed so far, and the synchronous
// actions set, reset and emit change it before it is written. An action
// that fails refuses the command with its path; a preview reports every
// rule the command would run. A triggered variable recomputes in the
// commit that raises one of its triggers, before the rules, and when the
// refresh action names it; its moment is stored beside it and a document
// carries it under Computed.
//
// Reset and Refresh are commands too, each a commit with its own event:
// a Reset is the caller's write, a Refresh always commits and is
// followed by the eventless transitions.
//
// The outbox (ADR-0044): what a rule does beyond its own document is
// evaluated by the commit and written as a row in its transaction; the
// worker, or Drain, does it, marks the row and appends the outcome's
// event in one transaction, as the person whose command queued it. The
// actions are create, for one document or for each element of a list,
// transition and refresh of another document, and table.insert,
// table.update and table.delete. Outbox lists the rows and RetryAction
// retries a dead one.
//
// Not here yet: the actions that leave the database (Phase 4), the
// class-level access rules (Phase 5), and the server (Phase 6).
package store
