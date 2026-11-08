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
// without writing anything (ADR-0025). Not here yet: the statechart
// (Phase 3), rules and QUERY (Phase 4), access rules and rights (Phase 5)
// and the server (Phase 6); the principal is recorded as the actor and
// enforces nothing until Phase 5.
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
	// HistoryRow is one committed version with its validity and actor.
	HistoryRow = store.HistoryRow
	// Event is one row of the event log.
	Event = store.Event
	// Preview is what a commit would do (ADR-0025).
	Preview = store.Preview
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
	// Diagnostics are the findings of a class or schema definition; the
	// error DefineClass, DefineSchema and ValidateClass return.
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

// Preview runs a create (empty id) or a set through the commit pipeline
// and writes nothing (ADR-0025).
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
