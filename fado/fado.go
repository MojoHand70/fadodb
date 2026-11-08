// Package fado is the Go API of FadoDB: open a database file, define
// schemas and classes, and create, change, read and delete documents as
// commits with history and an event log (docs/architecture.md, "Go API").
//
// The embedder supplies the principal on every call (ADR-0027), may give
// every mutating call an idempotency key and a business date (ADR-0026),
// and can run a change through the commit pipeline without writing it
// (ADR-0025). What this build does not do yet is listed on the store
// package: the statechart, rules, access rules and rights, LOOKUP against
// explicit tables, the filter DSL and the SQL endpoint, the blob store,
// and class migrations under live documents.
package fado

import (
	"context"
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
	// Option configures one call: As, Key, BusinessDate, WithID.
	Option = store.Option
	// Error is a refusal with a stable code, the variable it is about, a
	// message and a hint.
	Error = store.Error
	// Class is a compiled class definition.
	Class = model.Class
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

// Class returns a compiled class by name.
func (db *DB) Class(name string) (*Class, bool) { return db.s.Class(name) }

// Create makes a document with the values given, the defaults of the
// inputs not given and every auto variable computed, as version 1.
func (db *DB) Create(ctx context.Context, class string, values Values, opts ...Option) (*Document, error) {
	return db.s.Create(ctx, class, values, opts...)
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

// Preview runs a create (empty id) or a set through the commit pipeline
// and writes nothing (ADR-0025).
func (db *DB) Preview(ctx context.Context, class, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	return db.s.Preview(ctx, class, id, values, transition, opts...)
}
