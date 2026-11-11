// Package fado is the Go API of FadoDB: open a database file; define
// schemas, classes and explicit tables; create, change, read and delete
// documents as commits with recalculation, typed history and an event
// log; attach files; filter documents with a formula and run read-only
// SQL over the views; redefine a class as a new version that touches no
// document, and migrate live documents on purpose (docs/architecture.md,
// "Go API").
//
// The embedder supplies the principal on every call (ADR-0027), may give
// every mutating call an idempotency key and a business date (ADR-0026),
// and can run a change, or a draft class, through the commit pipeline
// without writing anything (ADR-0025). A document moves through its
// class's statechart with Transition, and Transitions says which moves
// are open (ADR-0040); a preview, a listing or a read may be asked at a
// moment the caller chooses with At, and a preview reports the moment it
// ran under (ADR-0042). The principal decides what a call may set and
// sees: the rights table and the per-transition rules are enforced on
// every call, a refusal being forbidden and a hidden variable absent
// (ADR-0041). The rules of a class run inside the commit (ADR-0043), and
// what they do to other documents and tables is queued and run by the
// outbox (ADR-0044). An action may also be the host program's own: a
// definition, stored with DefineAction, and a Go function given to Open
// in Options.Actions, which the outbox calls with the event that fired
// the rule (ADR-0045). A definition may instead carry a request, which
// the engine sends as HTTP through Options.Outbound, with the secrets
// Options.Secrets supplies; package fado/outbound makes the sender, and a
// database opened without one sends nothing. An action may carry into,
// which writes what it returned back to the document when it has ended,
// so a process goes on from the answer. Not here yet: QUERY and time
// (Phase 4),
// the class-level access rules compiled to row predicates (Phase 5) and
// the server (Phase 6).
package fado

import (
	"context"
	"io"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The types of the API. They are the store's, named here so that an
// embedder never imports an internal package.
type (
	// Options configure Open.
	Options = store.Options
	// Principal is who a call runs for (ADR-0027).
	Principal = store.Principal
	// Values are variable values by name in the wire shape (ADR-0018).
	Values = store.Values
	// Document is a document at one version.
	Document = store.Document
	// Moment is when a triggered variable's value was computed
	// (ADR-0043): Document.Computed has one per such variable.
	Moment = store.Moment
	// HistoryRow is one committed version with its validity and actor.
	HistoryRow = store.HistoryRow
	// Event is one row of the event log.
	Event = store.Event
	// Preview is what a commit would do (ADR-0025): the document, the
	// errors per variable, the states visited, the transitions open and
	// the rules the command would run.
	Preview = store.Preview
	// RuleRun is one rule whose trigger a previewed command raised, and
	// ActionRun one action of a rule that ran (ADR-0043).
	RuleRun   = store.RuleRun
	ActionRun = store.ActionRun
	// OutboxOptions configure the outbox of an open database, in
	// Options.Outbox; QueuedAction is one action a commit queued, with
	// what became of it; OutboxFilter selects them (ADR-0044).
	OutboxOptions = store.OutboxOptions
	QueuedAction  = store.QueuedAction
	OutboxFilter  = store.OutboxFilter
	// ActionFunc is a Go function that implements an action, given to Open
	// in Options.Actions under the action's name (ADR-0027, ADR-0045);
	// ActionCall is what it is called with, and DeliveredEvent the event
	// that fired the rule, with the document at that version.
	ActionFunc     = store.ActionFunc
	ActionCall     = store.ActionCall
	DeliveredEvent = store.DeliveredEvent
	// ActionInfo is one action a rule may name, as Actions lists it, with
	// its parameters and what it returns, each an ActionParam.
	ActionInfo  = store.ActionInfo
	ActionParam = store.ActionParam
	// ActionPreview is what an action would be called with, and
	// ActionOutcome what became of one trial of it.
	ActionPreview = store.ActionPreview
	ActionOutcome = store.ActionOutcome
	// Outbound sends the HTTP requests of declared actions, in
	// Options.Outbound (ADR-0045, item 7); package fado/outbound makes one
	// that calls only the hosts a program lists. OutboundRequest and
	// OutboundReply are what it is handed and what it answers, Header one
	// header of a request, and RequestPreview the request PreviewAction
	// shows, with secrets masked.
	Outbound        = store.Outbound
	OutboundRequest = store.OutboundRequest
	OutboundReply   = store.OutboundReply
	Header          = store.Header
	RequestPreview  = store.RequestPreview
	// TransitionInfo is one named transition leaving a document's state
	// and whether it may be taken now (ADR-0040).
	TransitionInfo = store.TransitionInfo
	// Filter is a query over a class: a where formula, an order, a limit,
	// an offset and the fields to return (ADR-0038).
	Filter = store.Filter
	// Rows is the result of a SQL statement: columns and rows.
	Rows = store.Rows
	// BlobStore holds the bytes of attachments by content hash
	// (ADR-0033); an embedder may supply its own in Options.Blobs.
	BlobStore = store.BlobStore
	// FileInfo is the fact of a file as a document holds it.
	FileInfo = store.FileInfo
	// Option configures one call: As, Key, BusinessDate, WithID.
	Option = store.Option
	// Error is a refusal with a stable code, the variable it is about, a
	// message and a hint.
	Error = store.Error
	// Class is a compiled class definition.
	Class = model.Class
	// Table is a compiled explicit table definition (ADR-0016).
	Table = model.Table
	// Schema is a resolved named schema (ADR-0031).
	Schema = model.Schema
	// Diagnostics are the findings of a definition: the error DefineClass,
	// DefineSchema, DefineTable and DefineAction return, and what
	// ValidateClass and ValidateAction answer.
	Diagnostics = model.Diagnostics
	// Diagnostic is one finding: a code, the JSON pointer of the element,
	// the position in a formula, a message and a hint.
	Diagnostic = model.Diagnostic
)

// As runs the call as the principal.
func As(p Principal) Option { return store.As(p) }

// Key gives a mutating call its idempotency key (ADR-0026).
func Key(k string) Option { return store.Key(k) }

// BusinessDate gives the commit's event its business date (ADR-0026), a
// calendar day.
func BusinessDate(day time.Time) Option { return store.BusinessDate(day) }

// WithID gives a new document the caller's id.
func WithID(id string) Option { return store.WithID(id) }

// Replace makes ImportRows empty the table before writing the rows.
func Replace() Option { return store.Replace() }

// AtVersion makes OpenFile read a committed version instead of the
// current one.
func AtVersion(version int64) Option { return store.AtVersion(version) }

// At runs a call that reads or previews at the moment (ADR-0042): NOW()
// is t and TODAY() its UTC day in every formula the call evaluates, and a
// preview reports it as Now. A zero t is the database's clock. A command
// that writes refuses it with not_allowed: a change is previewed at a
// moment, or made in a copy of the database opened with Options.Now set.
func At(t time.Time) Option { return store.At(t) }

// Permanent marks an error a Go action returns as a refusal: the action
// is dead at once with it, where any other error is tried again later
// (ADR-0045).
func Permanent(err error) error { return store.Permanent(err) }

// Sign is the signature the engine puts on a request whose definition
// names a sign secret (ADR-0045, item 4): the HMAC-SHA256, in hex, of the
// timestamp, a dot and the body, keyed with the secret. The request
// carries it as "FadoDB-Signature: t=<timestamp>,v1=<signature>".
func Sign(secret, timestamp string, body []byte) string { return store.Sign(secret, timestamp, body) }

// VerifySignature is what a receiver written in Go calls on a request it
// was sent: it checks the FadoDB-Signature header against the body as
// received and the shared secret, and that the timestamp is within the
// tolerance of now.
func VerifySignature(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	return store.VerifySignature(secret, header, body, now, tolerance)
}

// DB is an open database.
type DB struct {
	s *store.Store
}

// Open opens the database file at path, creating it when it does not
// exist.
func Open(path string, o Options) (*DB, error) {
	s, err := store.Open(path, o)
	if err != nil {
		return nil, err
	}
	return &DB{s: s}, nil
}

// Close closes the database.
func (db *DB) Close() error { return db.s.Close() }

// DefineClass compiles a class definition in JSON, creates its tables
// and views and stores it. The error is Diagnostics when the definition
// is refused.
func (db *DB) DefineClass(ctx context.Context, definition []byte) (*Class, error) {
	return db.s.DefineClass(ctx, definition)
}

// ValidateClass reports what DefineClass would refuse, storing nothing.
func (db *DB) ValidateClass(definition []byte) Diagnostics { return db.s.Validate(definition) }

// DefineClasses defines several classes in one call: each definition is
// checked against the others and against what is stored, and all are
// stored or none is. It is how classes that name each other are defined,
// such as an approval whose rule refreshes its purchase and a purchase
// whose formula counts its approvals, since neither could be defined
// first. The classes come back in the order of the definitions. The
// error is Diagnostics when a definition is refused; the path of each
// finding starts with the position of its definition, /1/variables/total
// for the second.
func (db *DB) DefineClasses(ctx context.Context, definitions [][]byte) ([]*Class, error) {
	return db.s.DefineClasses(ctx, definitions)
}

// ValidateClasses reports what DefineClasses would refuse, storing
// nothing.
func (db *DB) ValidateClasses(definitions [][]byte) Diagnostics {
	return db.s.ValidateClasses(definitions)
}

// DefineSchema compiles a named schema in JSON and stores it.
func (db *DB) DefineSchema(ctx context.Context, definition []byte) (*Schema, error) {
	return db.s.DefineSchema(ctx, definition)
}

// Classes lists the class names in definition order.
func (db *DB) Classes() []string { return db.s.Classes() }

// DefineTable compiles an explicit table definition in JSON, creates the
// table and its view and stores it; formulas read it through LOOKUP.
func (db *DB) DefineTable(ctx context.Context, definition []byte) (*Table, error) {
	return db.s.DefineTable(ctx, definition)
}

// ImportRows writes rows into a table, each a map of column to value,
// replacing a row whose key exists; with Replace, the table is emptied
// first. It returns the number of rows written.
func (db *DB) ImportRows(ctx context.Context, table string, rows []Values, opts ...Option) (int, error) {
	return db.s.ImportRows(ctx, table, rows, opts...)
}

// TableRows reads every row of a table in key order.
func (db *DB) TableRows(ctx context.Context, table string) ([]Values, error) {
	return db.s.TableRows(ctx, table)
}

// Tables lists the table names in definition order.
func (db *DB) Tables() []string { return db.s.Tables() }

// Table returns a compiled table definition by name.
func (db *DB) Table(name string) (*Table, bool) { return db.s.Table(name) }

// Class returns a compiled class by name.
func (db *DB) Class(name string) (*Class, bool) { return db.s.Class(name) }

// Create makes a document with the values given, the defaults of the
// inputs not given and every auto variable computed, as version 1.
func (db *DB) Create(ctx context.Context, class string, values Values, opts ...Option) (*Document, error) {
	return db.s.Create(ctx, class, values, opts...)
}

// CreateMany makes documents in one transaction, one commit each, all or
// none: the bulk load path (ADR-0035). A batch takes no key and no id.
func (db *DB) CreateMany(ctx context.Context, class string, docs []Values, opts ...Option) ([]*Document, error) {
	return db.s.CreateMany(ctx, class, docs, opts...)
}

// Migrate brings every live document of a class that an older version
// computed to the current version, as commits in batches (ADR-0039). It
// returns how many documents it moved; terminal documents stay.
func (db *DB) Migrate(ctx context.Context, class string, opts ...Option) (int, error) {
	return db.s.Migrate(ctx, class, opts...)
}

// Set assigns values to a document and recalculates what depends on
// them, as a new version; a set that changes nothing commits nothing.
func (db *DB) Set(ctx context.Context, class, id string, values Values, opts ...Option) (*Document, error) {
	return db.s.Set(ctx, class, id, values, opts...)
}

// Reset puts the named input variables of a document back to their
// defaults, as a new version (ADR-0043): a default is re-evaluated and an
// input without one becomes blank. It is the caller's write, checked
// against the write rights as a set is; a reset that changes nothing
// commits nothing.
func (db *DB) Reset(ctx context.Context, class, id string, variables []string, opts ...Option) (*Document, error) {
	return db.s.Reset(ctx, class, id, variables, opts...)
}

// Refresh recomputes the named triggered variables of a document, as a
// new version (ADR-0043); each must list refresh among its triggers, and
// with none named every variable that does recomputes. It always commits
// and the eventless transitions are evaluated after it, so it is how a
// caller makes a document look at the clock: an invoice whose guard reads
// TODAY() moves when it is refreshed on the day.
func (db *DB) Refresh(ctx context.Context, class, id string, variables []string, opts ...Option) (*Document, error) {
	return db.s.Refresh(ctx, class, id, variables, opts...)
}

// Drain runs every action of the outbox that is due, on the caller's
// goroutine, and returns how many it attempted (ADR-0044). The worker
// does this by itself unless Options.Outbox.Manual is set; a test and a
// simulation set it and call Drain at the moment they choose.
func (db *DB) Drain(ctx context.Context) (int, error) { return db.s.Drain(ctx) }

// Outbox lists the actions commits queued, in the order they were
// queued: what waits, what ran and what is dead, each with its key, its
// arguments, its attempts and its last error (ADR-0044).
func (db *DB) Outbox(ctx context.Context, f OutboxFilter) ([]QueuedAction, error) {
	return db.s.Outbox(ctx, f)
}

// RetryAction makes a dead action pending again; the outcome it then
// reaches is a further event. A dead action is never retried by itself.
func (db *DB) RetryAction(ctx context.Context, key string) error {
	return db.s.RetryAction(ctx, key)
}

// DefineAction compiles an action definition in JSON and stores it
// (ADR-0045): a name of a namespace and a verb, such as "books.post", its
// parameters and what it returns, each by scalar type. A rule may name
// the action from then on and gives its arguments under args. The
// definition without a request is the contract of a Go function, which
// must have been given to Open, in Options.Actions, under the same name.
// A definition with a request is sent by the engine: the database must
// have been opened with Options.Outbound, which must know the
// definition's connection, and with Options.Secrets if the request reads
// a secret. The same definition again is a no-op; a redefinition is the
// next version and may not break a rule that calls the action. The error
// is Diagnostics when the definition is refused.
func (db *DB) DefineAction(ctx context.Context, definition []byte) (*ActionInfo, error) {
	return db.s.DefineAction(ctx, definition)
}

// ValidateAction reports what DefineAction would refuse, storing nothing.
func (db *DB) ValidateAction(definition []byte) Diagnostics { return db.s.ValidateAction(definition) }

// Actions lists every action a rule may name in this database and this
// process: the engine's own, then the defined ones, each with its
// parameters, their types and descriptions, what it returns and whether
// this process can run it (ADR-0045).
func (db *DB) Actions() []ActionInfo { return db.s.Actions() }

// Action returns one action of the catalogue by name.
func (db *DB) Action(name string) (*ActionInfo, bool) { return db.s.Action(name) }

// PreviewAction answers what a defined action would be called with for
// the arguments, given as values, and for a declared action the request
// it would send, with every secret masked; it calls and sends nothing
// (ADR-0045).
func (db *DB) PreviewAction(ctx context.Context, name string, args Values) (*ActionPreview, error) {
	return db.s.PreviewAction(ctx, name, args)
}

// TryAction runs a defined action once outside any document, for the
// arguments given as values: it queues nothing, writes no event and
// answers what the action returned or how it failed (ADR-0045). The
// request of a declared action is sent for real.
func (db *DB) TryAction(ctx context.Context, name string, args Values) (*ActionOutcome, error) {
	return db.s.TryAction(ctx, name, args)
}

// Transition moves a document along the named transition of its class,
// with the assignments in values applied in the same commit (ADR-0040);
// values may be nil. Every eventless transition that then fires is a
// further commit in the same transaction. A transition that cannot happen
// is refused with unknown_transition, wrong_state, guard_false,
// too_many_transitions or terminal, before anything is written.
func (db *DB) Transition(ctx context.Context, class, id, name string, values Values, opts ...Option) (*Document, error) {
	return db.s.Transition(ctx, class, id, name, values, opts...)
}

// Transitions lists the named transitions leaving the document's state,
// each with whether the principal may take it now (ADR-0040). It writes
// nothing.
func (db *DB) Transitions(ctx context.Context, class, id string, opts ...Option) ([]TransitionInfo, error) {
	return db.s.Transitions(ctx, class, id, opts...)
}

// Delete removes a document's current row and appends the event; its
// history stays.
func (db *DB) Delete(ctx context.Context, class, id string, opts ...Option) error {
	return db.s.Delete(ctx, class, id, opts...)
}

// Get reads the current version of a document.
func (db *DB) Get(ctx context.Context, class, id string, opts ...Option) (*Document, error) {
	return db.s.Get(ctx, class, id, opts...)
}

// Version reads one committed version of a document.
func (db *DB) Version(ctx context.Context, class, id string, version int64, opts ...Option) (*HistoryRow, error) {
	return db.s.Version(ctx, class, id, version, opts...)
}

// History reads every committed version of a document, oldest first.
func (db *DB) History(ctx context.Context, class, id string, opts ...Option) ([]*HistoryRow, error) {
	return db.s.History(ctx, class, id, opts...)
}

// Events reads the events that touched a document, oldest first.
func (db *DB) Events(ctx context.Context, class, id string, opts ...Option) ([]*Event, error) {
	return db.s.Events(ctx, class, id, opts...)
}

// Query reads the documents of a class that match a filter, in its
// order, at most its limit, with the fields it names.
func (db *DB) Query(ctx context.Context, f Filter, opts ...Option) ([]*Document, error) {
	return db.s.Query(ctx, f, opts...)
}

// Count counts the documents a filter's where matches.
func (db *DB) Count(ctx context.Context, f Filter, opts ...Option) (int64, error) {
	return db.s.Count(ctx, f, opts...)
}

// SQL runs one read-only SELECT over the views with positional
// parameters; at most 10,000 rows come back.
func (db *DB) SQL(ctx context.Context, statement string, args []any, opts ...Option) (*Rows, error) {
	return db.s.SQL(ctx, statement, args, opts...)
}

// PutBlob stores the bytes of a file and returns the File fact to commit
// in an object<File> variable or a rows<File> element (ADR-0033).
func (db *DB) PutBlob(ctx context.Context, name, mediaType string, r io.Reader, opts ...Option) (Values, error) {
	return db.s.PutBlob(ctx, name, mediaType, r, opts...)
}

// OpenFile opens a file a document holds, by the document and the hash,
// at the current version or the one AtVersion names.
func (db *DB) OpenFile(ctx context.Context, class, id, hash string, opts ...Option) (io.ReadCloser, *FileInfo, error) {
	return db.s.OpenFile(ctx, class, id, hash, opts...)
}

// OpenBlob opens a blob by hash alone, for an administrator.
func (db *DB) OpenBlob(ctx context.Context, hash string, opts ...Option) (io.ReadCloser, int64, error) {
	return db.s.OpenBlob(ctx, hash, opts...)
}

// CheckBlobs recomputes the hash of every held blob and returns the ones
// whose bytes do not match, and how many it checked.
func (db *DB) CheckBlobs(ctx context.Context) (mismatched []string, checked int, err error) {
	return db.s.CheckBlobs(ctx)
}

// Preview runs a create (empty id), a set or a transition through the
// commit pipeline and writes nothing (ADR-0025): the document as it would
// be, the states the command would visit and the transitions open from
// the last of them.
func (db *DB) Preview(ctx context.Context, class, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	return db.s.Preview(ctx, class, id, values, transition, opts...)
}

// PreviewDraft compiles a class definition for this call alone and runs
// a create (empty id) or a set of an existing document of the stored
// class of the same name through the commit pipeline under it, writing
// and storing nothing (ADR-0025): a designer tries a draft on real
// documents, terminal ones included.
func (db *DB) PreviewDraft(ctx context.Context, definition []byte, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	return db.s.PreviewDraft(ctx, definition, id, values, transition, opts...)
}
