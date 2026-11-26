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
// (ADR-0041). The four rules of a class say who may see, create, change
// and delete a document (ADR-0049): a document its caller may not see is
// answered as if it did not exist, by a read, a command, a filter and a
// count alike, and a caller who sees it and may not do what they ask is
// refused with forbidden and the rule, which Error.Path names. A
// principal may say what it is, a person, an agent or a service, and for
// whom it acts: every event records both beside the actor's id, and
// USERKIND() and ACTINGFOR() answer them to a formula, so that a class
// can keep a step for a person (ADR-0050). The rules
// of a class run inside the commit (ADR-0043), and
// what they do to other documents and tables is queued and run by the
// outbox (ADR-0044). An action may also be the host program's own: a
// definition, stored with DefineAction, and a Go function given to Open
// in Options.Actions, which the outbox calls with the event that fired
// the rule (ADR-0045). A definition may instead carry a request, which
// the engine sends as HTTP through Options.Outbound, with the secrets
// Options.Secrets supplies; package fado/outbound makes the sender, and a
// database opened without one sends nothing. An action may carry into,
// which writes what it returned back to the document when it has ended,
// so a process goes on from the answer. A formula reads the life of its
// document. A transition may carry its time, an after or an at: the
// engine moves the document by itself when that time comes, as the
// person whose commit set the timer; Transitions shows the moment, and
// Due lists the documents a guard on time would move, for the host
// program to touch (ADR-0047). Every call that reads or changes the
// database is also a command, a name and one JSON object answered by one
// JSON object, which Exec runs (ADR-0055): it is what the server
// carries over the network (fadodb serve, and the door a host program
// mounts, fado/httpdoor). Not here yet: QUERY.
package fado

import (
	"context"
	"io"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
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
	// Analytics is the analytical engine a program gives the database
	// it opens, in Options.Analytics (ADR-0017, ADR-0089); the module
	// analytics has one, made with DuckDB. Copy is the copy of the
	// documents such an engine holds, which the database feeds itself;
	// CopyTable, CopyColumn and CopyWriter are what a feed speaks in,
	// and CopyState says how the copy of an open database stands.
	Analytics  = store.Analytics
	Copy       = store.Copy
	CopyTable  = store.CopyTable
	CopyColumn = store.CopyColumn
	CopyWriter = store.CopyWriter
	CopyState  = store.CopyState
	// OutboxCounts is how much of the outbox is still to do: due,
	// waiting for its moment, and dead.
	OutboxCounts = store.OutboxCounts
	// ChangeFilter says which events a stream of changes is told of,
	// and Changes is the stream: Next answers its events one by one
	// and Close ends it. Follow gives the stream a function that says
	// who its caller is now, which it asks when there is an event to
	// tell, so that a stream follows the groups of its caller
	// (ADR-0058).
	ChangeFilter = store.ChangeFilter
	Changes      = store.Changes
	// Wait says how WaitForChanges waits, and Waited is what it
	// answers: the events, and the place to ask from next (ADR-0064,
	// ruling 3).
	Wait   = store.Wait
	Waited = store.Waited
	// Flow is the flow of a class as one caller sees it, FlowState and
	// FlowTransition a state and a transition in it, and FlowPeriod the
	// period its moves are counted in (ADR-0063).
	Flow           = store.Flow
	FlowState      = store.FlowState
	FlowTransition = store.FlowTransition
	FlowPeriod     = store.FlowPeriod
	// ActionFunc is a Go function that implements an action, given to Open
	// in Options.Actions under the action's name (ADR-0027, ADR-0045);
	// ActionCall is what it is called with, and DeliveredEvent the event
	// that fired the rule, with the document at that version.
	ActionFunc     = store.ActionFunc
	ActionCall     = store.ActionCall
	DeliveredEvent = store.DeliveredEvent
	// ActionInfo is one action a rule may name, as Actions lists it, with
	// its parameters and what it returns, each an ActionParam.
	ActionInfo = store.ActionInfo
	// FunctionCatalogue is the engine's list of the functions a formula
	// can call, as Functions answers it (ADR-0074): each function a
	// FunctionInfo with its arguments, each a FunctionParam, and each
	// kind of function a FunctionKind.
	FunctionCatalogue = store.FunctionCatalogue
	FunctionInfo      = store.FunctionInfo
	FunctionKind      = store.FunctionKind
	FunctionParam     = store.FunctionParam
	// Defined says who defined a version of a class, a schema, a table
	// or an action, and when (ADR-0052); Definition is one version in the
	// listing Definitions answers.
	Defined    = store.Defined
	Definition = store.Definition
	// StoredDefinition is one version of a class, a schema, a table or an
	// action as Definition reads it back: the JSON as it was given, with
	// its version, who defined it and a class's notes beside it.
	StoredDefinition = store.StoredDefinition
	// Pointer is one ref variable that points at a class, as the
	// definition of that class lists them under PointedAtBy (ADR-0076).
	Pointer     = store.Pointer
	ActionParam = store.ActionParam
	// ActionPreview is what an action would be called with, and
	// ActionOutcome what became of one trial of it.
	ActionPreview = store.ActionPreview
	ActionOutcome = store.ActionOutcome
	// InletInfo is one inlet of the database (ADR-0065), Message a
	// message from outside as an inlet reads it, Receipt what became of
	// one, and InletTrial what an inlet would do with one.
	InletInfo  = store.InletInfo
	InletProof = store.InletProof
	Message    = store.Message
	Receipt    = store.Receipt
	InletTrial = store.InletTrial
	// App is an app of the database (ADR-0077): a name for the classes
	// that belong together, with its definition and its version.
	App = store.App
	// WordsInfo is one definition of the words of a class, a table or a
	// schema in another language (ADR-0068, item 6), and WordsOf names
	// the words of one thing in one language.
	WordsInfo = store.WordsInfo
	WordsOf   = store.WordsOf
	// Bundle is a class in a language with its compiled formulas, as one
	// JSON object a page reads (ADR-0070), and BundleOf names one.
	Bundle   = store.Bundle
	BundleOf = store.BundleOf
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
	// and whether it may be taken now (ADR-0040), and for a timed
	// transition the moment it fires (ADR-0047).
	TransitionInfo = store.TransitionInfo
	// LiveValue is what a live variable is for one caller at one moment
	// (ADR-0076): a value in the wire shape of its type, or, for a
	// variable whose formula is a QUERY, a list: its columns, its rows
	// and whether rows follow. LiveColumn is one column of such a list,
	// LiveRow one row, a document of the source with its id, and LiveRead
	// names the variable and the page of the list to read.
	LiveValue  = store.LiveValue
	LiveColumn = store.LiveColumn
	LiveRow    = store.LiveRow
	LiveRead   = store.LiveRead
	// DueDocument is one document a commit would move now, as Due lists
	// it: its state, the state it would move to and, when a timed
	// transition would move it, the moment since which it has been due
	// (ADR-0047).
	DueDocument = store.DueDocument
	// MoveOutcome is what became of one document of a TransitionMany or
	// a PreviewMany: the document after its move, or the refusal
	// (ADR-0048).
	MoveOutcome = store.MoveOutcome
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
	// BlobCheck is what CheckBlobs found: how many blobs it read, the
	// ones whose bytes do not match, and the files that documents hold
	// and the store has lost.
	BlobCheck = store.BlobCheck
	// MissingFile is a file a document holds whose bytes the store does
	// not have: its hash, and the class, the id, the first version and
	// the variable of the document.
	MissingFile = store.MissingFile
	// User is a person, an agent or a service that a database which
	// keeps its own users knows by its id (ADR-0057); UserEdit is what
	// SetUser changes, UserFilter narrows a list of users, UserKey is a
	// key of a user and UserChange one change to a user or its keys.
	User       = store.User
	UserEdit   = store.UserEdit
	UserFilter = store.UserFilter
	UserKey    = store.UserKey
	UserChange = store.UserChange
	// Group is a group of the database, a record of its own (ADR-0086):
	// its name, its label, what it is for, and how many users are in it;
	// GroupEdit is what SetGroup changes, GroupChange one change to a
	// group.
	Group       = store.Group
	GroupEdit   = store.GroupEdit
	GroupChange = store.GroupChange
	// Signing in (ADR-0071): SignIn is a sign-in under way after its
	// password, SecondStep the secret a person enters in an
	// authenticator app, Session a session and SessionLengths how long
	// one holds, PasswordLink a link that sets a password, and
	// SignInEntry and SignInFilter the log of sign-ins.
	SignIn         = store.SignIn
	SecondStep     = store.SecondStep
	Session        = store.Session
	SessionLengths = store.SessionLengths
	PasswordLink   = store.PasswordLink
	SignInEntry    = store.SignInEntry
	SignInFilter   = store.SignInFilter
	// Passkeys (ADR-0088): Invitation is an invitation that lets a
	// person add their first passkey, Invited whom one greets, and
	// Passkey a passkey as the database lists it.
	Invitation = store.Invitation
	Invited    = store.Invited
	Passkey    = store.Passkey
	// PhoneCode is the code for a phone a page shows (ADR-0088, item
	// 3), and PhoneFor whom its link is for.
	PhoneCode = store.PhoneCode
	PhoneFor  = store.PhoneFor
	// SignedOn is who a company's provider says a person is, as the
	// server checked it, and SignOnProvider a company's sign-on as the
	// page of signing in offers it.
	SignedOn       = store.SignedOn
	SignOnProvider = store.SignOnProvider
	// The passes the server gives clients (ADR-0071, items 6 and 7):
	// Client is a client the server gives passes to, Asked its request
	// for a code, CodeExchange what it sends for its passes, Passes what
	// it is given, and Grant a client a person allowed.
	Client       = store.Client
	Asked        = store.Asked
	CodeExchange = store.CodeExchange
	Passes       = store.Passes
	Grant        = store.Grant
	// Read is one entry of the log of reads, and ReadsFilter narrows the
	// log (ADR-0067).
	Read        = store.Read
	ReadsFilter = store.ReadsFilter
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

// What a caller is, as Principal.Kind says it (ADR-0050): a person who
// sent the command, an agent that decided to send it, or a service, a
// program that decides nothing, such as an import or a schedule. A host
// program that does not know leaves the kind empty.
// What a column of the copy in an analytical engine holds (ADR-0089): a
// text, a whole number, a number with a fraction, a true or a false,
// and a day, the whole number of the days of a date.
const (
	CopyText   = store.CopyText
	CopyWhole  = store.CopyWhole
	CopyNumber = store.CopyNumber
	CopyBool   = store.CopyBool
	CopyDay    = store.CopyDay
)

const (
	ActorPerson  = store.ActorPerson
	ActorAgent   = store.ActorAgent
	ActorService = store.ActorService
)

// The system groups (ADR-0052), given to a caller in Principal.System,
// apart from its other groups. A database administrator operates the
// outbox and the blobs, a designer defines classes, tables and actions
// and migrates, a content administrator sees every document and acts on
// it past the rules of its class, and an analyst runs SQL, which reads
// everything. Nobody is in one unless the host program says so, and a
// call without a principal is nobody. GroupEveryone names every caller
// that has an id: the engine gives it by itself, and a class opens to
// them with INGROUP("everyone").
const (
	GroupAdministrators        = store.GroupAdministrators
	GroupDesigners             = store.GroupDesigners
	GroupContentAdministrators = store.GroupContentAdministrators
	GroupAnalysts              = store.GroupAnalysts
	GroupEveryone              = store.GroupEveryone
)

// LogStart is the place before the first event of the log, for
// ChangeFilter.After: a stream or a wait given it begins with the first
// event there is, and WaitForChanges answers it as its place while the
// log holds none.
const LogStart = store.LogStart

// The bounds of WaitForChanges: it waits up to MaxWaitSeconds, and an
// answer holds DefaultWaitLimit events unless Wait.Limit says otherwise,
// MaxWaitLimit at most.
const (
	MaxWaitSeconds   = store.MaxWaitSeconds
	DefaultWaitLimit = store.DefaultWaitLimit
	MaxWaitLimit     = store.MaxWaitLimit
)

// SubstitutionClass is the class the engine brings to every database
// (ADR-0053): a document of it says who may act for whom, from when,
// until when and on which classes, and a caller that names somebody in
// Principal.For is let through only while one says so.
//
//	// Ann lets Bob act for her until the fifteenth.
//	db.Create(ctx, fado.SubstitutionClass, fado.Values{"by": "bob", "until": "2026-12-15 00:00:00"}, fado.As(ann))
//	// Bob approves for Ann. USER() is bob and ACTINGFOR() is ann.
//	db.Transition(ctx, "Expense", id, "approve", nil, fado.As(fado.Principal{ID: "bob", For: "ann"}))
//	// Ann takes it back.
//	db.Transition(ctx, fado.SubstitutionClass, sub.ID, fado.SubstitutionWithdraw, nil, fado.As(ann))
//
// The person gives one for themselves; a database administrator gives
// one for somebody who cannot, with a reason and never to themselves.
// The document is planned, active, ended or withdrawn, and moves by
// itself at its from and its until. A host program that keeps its own
// substitutions opens the database with Options.HostAnswersFor.
const (
	SubstitutionClass     = model.SubstitutionClass
	SubstitutionPlanned   = model.SubstitutionPlanned
	SubstitutionActive    = model.SubstitutionActive
	SubstitutionEnded     = model.SubstitutionEnded
	SubstitutionWithdrawn = model.SubstitutionWithdrawn
	SubstitutionWithdraw  = model.SubstitutionWithdraw
)

// As runs the call as the principal.
func As(p Principal) Option { return store.As(p) }

// Key gives a mutating call its idempotency key (ADR-0026).
func Key(k string) Option { return store.Key(k) }

// Reason says why the caller acts or reads, for a call that only the
// content administrators' group lets through (ADR-0052, ADR-0067): an act
// or a read past a rule needs one, and is refused with reason_required
// without it. A change keeps the reason on its event beside the rule it
// lifted, and a read in the log of reads beside what it showed. Without a
// reason a content administrator reads as anybody. A SQL statement keeps
// a reason when it is given. A call the rules let through ignores it.
func Reason(text string) Option { return store.Reason(text) }

// BusinessDate gives the commit's event its business date (ADR-0026), a
// calendar day.
func BusinessDate(day time.Time) Option { return store.BusinessDate(day) }

// WithID gives a new document the caller's id.
func WithID(id string) Option { return store.WithID(id) }

// Replace makes ImportRows empty the table before writing the rows.
func Replace() Option { return store.Replace() }

// Saw says which version of the document the sender of the command read
// before it decided (ADR-0078, item 2). Set, Reset, Refresh, Transition
// and Delete take it, and the previews of them. When the document is at
// another version the command is refused with stale_version, which names
// the version the document is at, and nothing is written: read the
// document again and decide on what it holds now. The event of a command
// that was taken keeps the version, as Event.SeenVersion. A command
// without it acts on the document as it is; a command sent again with
// its Key is answered as the first was.
func Saw(version int64) Option { return store.Saw(version) }

// SawEach is Saw for TransitionMany and PreviewMany: the version of each
// document, as many as the ids and in their order, 0 for a document
// whose version is not named. A document at another version is refused
// with stale_version among the outcomes, and the others move.
func SawEach(versions []int64) Option { return store.SawEach(versions) }

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
// exist. A database it creates is readable and writable by the user of
// the machine that created it and by nobody else, and so is the
// directory of attachments beside it; one that exists keeps the
// permissions it has.
func Open(path string, o Options) (*DB, error) {
	s, err := store.Open(path, o)
	if err != nil {
		return nil, err
	}
	return &DB{s: s}, nil
}

// MaxQuestion is how long a query or a count that no index serves may
// run before it is stopped and refused with too_wide (ADR-0075, item
// 6): the database's limit, five seconds when none was given.
func (db *DB) MaxQuestion() time.Duration { return db.s.MaxQuestion() }

// TimeZone is the database's time zone, by which the schedules of its
// classes go (ADR-0093): the one Options.TimeZone last gave it, UTC
// when none ever did.
func (db *DB) TimeZone() string { return db.s.TimeZone() }

// CopyState says how the copy of the database in the analytical engine
// stands (ADR-0089): whether the program gave an engine, how many feeds
// wrote to the copy, and why the last one failed. Without an engine the
// database keeps no copy and answers every question by the store.
func (db *DB) CopyState() CopyState { return db.s.CopyState() }

// FeedCopy brings the copy in the analytical engine up to the database
// now, without waiting for the feed that runs by itself every
// Options.CopyEvery (ADR-0089). It answers nil for a database that
// keeps no copy.
func (db *DB) FeedCopy(ctx context.Context) error { return db.s.FeedCopy(ctx) }

// Close closes the database.
func (db *DB) Close() error { return db.s.Close() }

// The command set (ADR-0055). Command is one row of its table: the name,
// what the command does, whether it reads, writes or destroys, the call
// of this package it is, the members of its object and the members of
// its answer. CommandField and CommandMember are one of each.
type (
	Command       = command.Command
	CommandField  = command.Field
	CommandMember = command.Member
)

// Commands lists the command set, in the order of its table. A door, a
// description for another language and the help of a tool are made from
// it. The rows are read and not changed.
func Commands() []*Command { return command.Commands() }

// Exec runs one command of the command set (ADR-0055): the name of a
// call of this package in lower case with underscores, and one JSON
// object with what the call takes. It answers one JSON object.
//
//	answer, err := db.Exec(ctx, ann, "transition",
//		[]byte(`{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}`))
//	// {"document": {"class": "Invoice", "id": "FV/2026/11/0042", "state": "approved", …}}
//
// p is the caller, which the object never names; the object may say for
// whom the caller acts, under "for", and the engine checks it as it
// checks Principal.For. A member the command does not know is refused
// with bad_json and its place, and so is one of the wrong JSON type.
//
// A command that was refused answers {"error": {"code": …, "message": …}}
// and returns the refusal as its error too: the *Error a call of this
// package would have returned, or Diagnostics for a definition that was
// refused, whose findings are in the answer under "diagnostics". Any
// other error is a failure of the engine, and its words are not in the
// answer. ErrorCode gives the code of each.
func (db *DB) Exec(ctx context.Context, p Principal, name string, object []byte) ([]byte, error) {
	return command.Exec(ctx, db.s, p, name, object)
}

// ErrorCode is the code of an error Exec, or any call of this package,
// returned: the code of a refusal, "bad_definition" for Diagnostics,
// "internal" for an error that is no refusal and "" for none.
func ErrorCode(err error) string { return command.Code(err) }

// DefineClass compiles a class definition in JSON, creates its tables
// and views and stores it. The error is Diagnostics when the definition
// is refused.
func (db *DB) DefineClass(ctx context.Context, definition []byte, opts ...Option) (*Class, error) {
	return db.s.DefineClass(ctx, definition, opts...)
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
func (db *DB) DefineClasses(ctx context.Context, definitions [][]byte, opts ...Option) ([]*Class, error) {
	return db.s.DefineClasses(ctx, definitions, opts...)
}

// ValidateClasses reports what DefineClasses would refuse, storing
// nothing.
func (db *DB) ValidateClasses(definitions [][]byte) Diagnostics {
	return db.s.ValidateClasses(definitions)
}

// DefineSchema compiles a named schema in JSON and stores it.
func (db *DB) DefineSchema(ctx context.Context, definition []byte, opts ...Option) (*Schema, error) {
	return db.s.DefineSchema(ctx, definition, opts...)
}

// DefineApp checks the definition of an app in JSON and stores it
// (ADR-0077): a name for the classes and tables that belong together,
// with what the app says of itself to people. A class or a table names
// the app it belongs to with "app" in its own definition (ADR-0079). The error is Diagnostics when the
// definition is refused.
func (db *DB) DefineApp(ctx context.Context, definition []byte, opts ...Option) (*App, error) {
	return db.s.DefineApp(ctx, definition, opts...)
}

// Apps lists the names of the apps in definition order.
func (db *DB) Apps() []string { return db.s.Apps() }

// App returns an app by name.
func (db *DB) App(name string) (*App, bool) { return db.s.App(name) }

// ClassesOf lists the classes that belong to an app, in definition
// order. A name that is no app is refused with unknown_app.
func (db *DB) ClassesOf(app string) ([]string, error) { return db.s.ClassesOf(app) }

// Classes lists the class names in definition order.
func (db *DB) Classes() []string { return db.s.Classes() }

// Schemas lists the schema names in definition order, the engine's
// vocabulary first.
func (db *DB) Schemas() []string { return db.s.SchemaNames() }

// Schema returns a resolved schema by name.
func (db *DB) Schema(name string) (*Schema, bool) { return db.s.Schema(name) }

// Definition reads one class, schema, table or action back as it was
// given: the JSON a designer wrote, with the version the database gave
// it, who defined that version and, for a class, its notes. what is
// DefinitionClass, DefinitionSchema, DefinitionTable or DefinitionAction,
// and version 0 the current one; the database keeps every version of a
// class and of an action. Class, Table, Schema and Action answer the
// engine's compiled form instead. It is open to every caller.
func (db *DB) Definition(ctx context.Context, what, name string, version int) (*StoredDefinition, error) {
	return db.s.Definition(ctx, what, name, version)
}

// What a definition is, as Definitions takes it.
const (
	DefinitionClass  = store.DefinitionClass
	DefinitionSchema = store.DefinitionSchema
	DefinitionTable  = store.DefinitionTable
	DefinitionAction = store.DefinitionAction
	DefinitionInlet  = store.DefinitionInlet
	DefinitionApp    = store.DefinitionApp
	DefinitionWords  = store.DefinitionWords
)

// Definitions lists the versions of one class, schema, table or action,
// oldest first, each with who defined it, what they were, for whom they
// acted and when (ADR-0052). It is open to every caller, as reading a
// definition is: what a designer changed is in sight. Class, Table and
// Action answer the same of the current version, under Defined. The
// version a table or words had when they were removed says who removed
// them and when, under Removed (ADR-0083), to a caller in the system
// group administrators or designers who is no agent, and to an agent
// that works for a user the database keeps in administrators; anyone
// else, and a call that names nobody, gets the versions without it.
func (db *DB) Definitions(ctx context.Context, what, name string, opts ...Option) ([]Definition, error) {
	return db.s.Definitions(ctx, what, name, opts...)
}

// DefineTable compiles an explicit table definition in JSON, creates the
// table and its view and stores it; formulas read it through LOOKUP.
func (db *DB) DefineTable(ctx context.Context, definition []byte, opts ...Option) (*Table, error) {
	return db.s.DefineTable(ctx, definition, opts...)
}

// ImportRows writes rows into a table, each a map of column to value,
// replacing a row whose key exists; with Replace, the table is emptied
// first. It returns the number of rows written.
func (db *DB) ImportRows(ctx context.Context, table string, rows []Values, opts ...Option) (int, error) {
	return db.s.ImportRows(ctx, table, rows, opts...)
}

// DeleteRows removes the rows of a table that have the keys given, in one
// transaction (ADR-0082): each key a value for every column of the
// table's key. A key the table does not have removes nothing. It asks the
// table's write rule, as an import does, and answers how many rows it
// removed.
func (db *DB) DeleteRows(ctx context.Context, table string, keys []Values, opts ...Option) (int, error) {
	return db.s.DeleteRows(ctx, table, keys, opts...)
}

// RemoveTable takes a table away with its rows, its definition and its
// words (ADR-0082). It is for a designer. A table a class reads, or a rule
// of a class writes, is refused with not_allowed. Its versions stay in the
// log of definitions, which records who removed it and when (ADR-0083),
// and a table defined again under its name takes the next version.
func (db *DB) RemoveTable(ctx context.Context, name string, opts ...Option) error {
	return db.s.RemoveTable(ctx, name, opts...)
}

// TableRows reads every row of a table in key order, for a principal the
// table's read rule lets through (ADR-0051).
func (db *DB) TableRows(ctx context.Context, table string, opts ...Option) ([]Values, error) {
	return db.s.TableRows(ctx, table, opts...)
}

// Tables lists the table names in definition order.
func (db *DB) Tables() []string { return db.s.Tables() }

// TablesOf lists the tables that belong to an app, in definition order
// (ADR-0079). A name that is no app is refused with unknown_app.
func (db *DB) TablesOf(app string) ([]string, error) { return db.s.TablesOf(app) }

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
func (db *DB) Drain(ctx context.Context, opts ...Option) (int, error) {
	return db.s.Drain(ctx, opts...)
}

// Outbox lists the actions commits queued, in the order they were
// queued: what waits, what ran and what is dead, each with its key, its
// arguments, its attempts and its last error (ADR-0044).
func (db *DB) Outbox(ctx context.Context, f OutboxFilter, opts ...Option) ([]QueuedAction, error) {
	return db.s.Outbox(ctx, f, opts...)
}

// OutboxCounts counts what of the outbox is still to do: the actions
// that are due and have not run, the ones that wait for their moment,
// and the dead ones. It is an administrator's, like Outbox, and is what
// a server shows whoever watches it (ADR-0059, item 9).
func (db *DB) OutboxCounts(ctx context.Context, opts ...Option) (OutboxCounts, error) {
	return db.s.OutboxCounts(ctx, opts...)
}

// Subscribe opens a stream of changes for the caller (ADR-0058): the
// events of the log from now on, or from after the event the filter
// names, in the order of the commits. The caller hears of an event when
// it may read the document as the event left it, so a stream tells
// nothing a query would hide. Next waits for the next event; a caller
// that was away gives the id of the last event it handled as After and
// misses nothing. As a command it is subscribe, which a server answers
// with a reply that stays open; Exec, which answers one object, refuses
// it.
//
//	changes, err := db.Subscribe(ctx, fado.ChangeFilter{Classes: []string{"Invoice"}, ToStates: []string{"review"}}, fado.As(agent))
//	defer changes.Close()
//	for {
//	    ev, err := changes.Next(ctx)
//	    ...
//	}
func (db *DB) Subscribe(ctx context.Context, f ChangeFilter, opts ...Option) (*Changes, error) {
	return db.s.Subscribe(ctx, f, opts...)
}

// WaitForChanges answers the events of the log after a place that the
// filter lets through and the caller may hear of, as a stream of changes
// tells them, with the place to ask from next; when there are none yet,
// it waits for the first, up to w.Seconds (ADR-0064, ruling 3). It is
// how a caller waits that cannot hold a stream open, such as an agent
// through the door for agents, and a caller who acts for somebody: it
// hears of a class it may act for them on. The filter's After is the
// After of the last answer, LogStart for the first event of the log, or
// empty for now. As a command it is wait_for_changes.
//
//	w, err := db.WaitForChanges(ctx, fado.ChangeFilter{Classes: []string{"Invoice"}, ToStates: []string{"review"}, After: place},
//	    fado.Wait{Seconds: 30}, fado.As(agent))
//	// handle w.Events, then ask again with After: w.After
func (db *DB) WaitForChanges(ctx context.Context, f ChangeFilter, w Wait, opts ...Option) (*Waited, error) {
	return db.s.WaitForChanges(ctx, f, w, opts...)
}

// RetryAction makes a dead action pending again; the outcome it then
// reaches is a further event. A dead action is never retried by itself.
func (db *DB) RetryAction(ctx context.Context, key string, opts ...Option) error {
	return db.s.RetryAction(ctx, key, opts...)
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
func (db *DB) DefineAction(ctx context.Context, definition []byte, opts ...Option) (*ActionInfo, error) {
	return db.s.DefineAction(ctx, definition, opts...)
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

// Functions answers the engine's list of the functions a formula can
// call (ADR-0074): each with its other names, its kind, which decides
// where it may stand, its arguments, what it returns, the call as the
// manual writes it and a sentence on what it answers, and whether this
// version of the engine has built it. The list is the engine's own and
// the same for every database; a formula editor offers names from it,
// and an agent reads it before it writes a formula. The check of a
// formula stays ValidateClass.
func (db *DB) Functions() *FunctionCatalogue { return db.s.Functions() }

// PreviewAction answers what a defined action would be called with for
// the arguments, given as values, and for a declared action the request
// it would send, with every secret masked; it calls and sends nothing
// (ADR-0045).
func (db *DB) PreviewAction(ctx context.Context, name string, args Values, opts ...Option) (*ActionPreview, error) {
	return db.s.PreviewAction(ctx, name, args, opts...)
}

// TryAction runs a defined action once outside any document, for the
// arguments given as values: it queues nothing, writes no event and
// answers what the action returned or how it failed (ADR-0045). The
// request of a declared action is sent for real.
func (db *DB) TryAction(ctx context.Context, name string, args Values, opts ...Option) (*ActionOutcome, error) {
	return db.s.TryAction(ctx, name, args, opts...)
}

// DefineInlet stores an inlet (ADR-0065): a way in for messages from
// outside. An inlet is a definition, the mirror of a declared action:
// how a message proves who sent it, with a secret of the inlet's
// connection, which messages the inlet acts on, what makes two messages
// the same, and the one command a message becomes, create, set or
// transition, carried out as the service user the inlet names. Its
// formulas read the message and nothing else. It is for a database
// administrator, since an inlet says whom the database takes a stranger
// for, in a database that keeps its users (Options.Users), opened by a
// program that takes messages (Options.Inbound) and supplies secrets
// (Options.Secrets). The same definition again is a no-op, and a
// redefinition is the next version. The error is Diagnostics when the
// definition is refused.
func (db *DB) DefineInlet(ctx context.Context, definition []byte, opts ...Option) (*InletInfo, error) {
	return db.s.DefineInlet(ctx, definition, opts...)
}

// ValidateInlet reports what DefineInlet would refuse, storing nothing.
func (db *DB) ValidateInlet(definition []byte) Diagnostics { return db.s.ValidateInlet(definition) }

// Inlets lists the inlets of the database, each with its address, the
// user it acts as, the command its messages become and whether this
// process acts on them.
func (db *DB) Inlets() []InletInfo { return db.s.Inlets() }

// Inlet returns one inlet by name.
func (db *DB) Inlet(name string) (*InletInfo, bool) { return db.s.Inlet(name) }

// RemoveInlet closes an inlet: its messages are answered as those of a
// name that is no inlet. Its versions stay, and defining it again opens
// it as its next version. It is for a database administrator.
func (db *DB) RemoveInlet(ctx context.Context, name string, opts ...Option) error {
	return db.s.RemoveInlet(ctx, name, opts...)
}

// DefineWords stores the words of a class, a table or a schema in
// another language (ADR-0068, item 6): the label and the description of
// each thing that has one, named by its path, checked against the
// current version of what they translate. A path it does not have is
// refused with bad_value. It makes no new version of what it translates
// and touches no document; the same words again are a no-op, and other
// words are their next version. It is for the system group designers.
func (db *DB) DefineWords(ctx context.Context, definition []byte, opts ...Option) (*WordsInfo, error) {
	return db.s.DefineWords(ctx, definition, opts...)
}

// ListWords lists the words of the database, each with its version, who
// defined it, and a note for each path that what it translates no longer
// has. A field of of narrows the list; an empty one lists every one. It
// is open to every caller. Definition reads the words themselves, under
// DefinitionWords and the name a listing gives, class/Invoice/pl.
func (db *DB) ListWords(of WordsOf) []WordsInfo { return db.s.ListWords(of) }

// GetBundle answers the bundle of a class in a language (ADR-0070): the
// class as it was defined with the words of the language put in place,
// the schemas it uses, and every formula a page runs to show a document
// and check it before it is sent, compiled for the browser VM, with the
// tag a page sends back to learn whether it changed. Reading it is open
// to every caller.
func (db *DB) GetBundle(ctx context.Context, of BundleOf) (*Bundle, error) {
	return db.s.Bundle(ctx, of)
}

// RemoveWords takes away the words of one thing in one language: a page
// shows it in its own words from then on. Their versions stay, and the
// log of definitions records who took them away and when (ADR-0083). It
// is for the system group designers.
func (db *DB) RemoveWords(ctx context.Context, of WordsOf, opts ...Option) error {
	return db.s.RemoveWords(ctx, of, opts...)
}

// TryInlet answers what an inlet would do with a message, and does
// nothing: whether it acts on it, and the command with its object as it
// would be sent. The inlet is one of the database, by name, or a
// definition that is not stored. The proof is not asked for. It is for
// a designer.
func (db *DB) TryInlet(ctx context.Context, name string, definition []byte, m *Message, opts ...Option) (*InletTrial, error) {
	return db.s.TryInlet(ctx, name, definition, m, opts...)
}

// Receive takes a message from outside for an inlet (ADR-0065): it
// checks the proof of the message, and then carries out the one command
// the message becomes, as the user the inlet names, with the key of the
// message, so that a message sent twice makes one commit and is answered
// the second time as the first. It asks for no caller: the proof is the
// message's own.
//
// A message whose proof does not hold is answered ErrNotProven, and
// nothing of it is read. A name that is no inlet, and any name in a
// program that takes no messages, is refused with unknown_inlet; a
// message the inlet cannot read with bad_message; and a command the
// database refuses with the database's own error. Once the inlet is
// known the receipt names it and its user, whatever the error.
func (db *DB) Receive(ctx context.Context, inlet string, m *Message) (*Receipt, error) {
	return db.s.Receive(ctx, inlet, m)
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

// MaxMany is the most documents one TransitionMany or PreviewMany takes.
const MaxMany = store.MaxMany

// TransitionMany moves the documents of one class named by ids along the
// named transition, in the order given, in one transaction (ADR-0048).
// Each move is the commit Transition makes. A document that cannot move
// is refused with the code Transition would give it and left as it was,
// and the others move: the outcomes say which, one for each id in the
// order given. The error is the call's own, after which nothing has
// moved: unknown_class, unknown_transition, too_many_elements for more
// than MaxMany ids, bad_value for an id named twice, not_allowed for At,
// or a failure of the database. With Key the batch sent again moves no
// document twice.
func (db *DB) TransitionMany(ctx context.Context, class, name string, ids []string, values Values, opts ...Option) ([]MoveOutcome, error) {
	return db.s.TransitionMany(ctx, class, name, ids, values, opts...)
}

// PreviewMany runs the batch TransitionMany would run and writes
// nothing (ADR-0048): the outcomes the call would have now, or at the
// moment given with At. It holds the writer as long as the batch would.
func (db *DB) PreviewMany(ctx context.Context, class, name string, ids []string, values Values, opts ...Option) ([]MoveOutcome, error) {
	return db.s.PreviewMany(ctx, class, name, ids, values, opts...)
}

// Transitions lists the named transitions leaving the document's state,
// each with whether the principal may take it now (ADR-0040). A timed
// transition is listed with the moment it fires, one without a name too
// (ADR-0047). It writes nothing.
func (db *DB) Transitions(ctx context.Context, class, id string, opts ...Option) ([]TransitionInfo, error) {
	return db.s.Transitions(ctx, class, id, opts...)
}

// Live works a live variable of a document out for its caller, now
// (ADR-0076). A live variable says "mode": "live" in its class: it is
// kept nowhere, so no other call answers it, and it may differ from
// caller to caller and from one moment to the next. Whoever may read the
// document asks, and the variable by its read right. It writes nothing.
//
// A live variable whose formula is a QUERY is a list of the documents of
// another class: the answer then has Columns, Rows and More, each row a
// document the caller may read with the columns the caller may read in
// it. Live reads its first page; LivePage chooses the page.
func (db *DB) Live(ctx context.Context, class, id, variable string, opts ...Option) (*LiveValue, error) {
	return db.s.Live(ctx, class, id, variable, opts...)
}

// LivePage is Live with the page of a list chosen (ADR-0076, item 7):
// what to sort by, how many rows at most, how many to skip and which
// columns. The formula says which documents the list has; the caller
// says which of them to read, and the engine reads no more than that.
func (db *DB) LivePage(ctx context.Context, read LiveRead, opts ...Option) (*LiveValue, error) {
	return db.s.LivePage(ctx, read, opts...)
}

// Due lists the documents of a class that a commit would move if it
// touched them now, or at the moment given with At (ADR-0047): each with
// its state and the state it would move to, by an eventless transition
// whose guard holds or a timed one whose time has come, and for a timed
// one the moment since which the document has been due. It writes
// nothing: Refresh with no variables is the touch that moves a due
// document.
func (db *DB) Due(ctx context.Context, class string, opts ...Option) ([]DueDocument, error) {
	return db.s.Due(ctx, class, opts...)
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

// Flow answers the flow of a class for its caller (ADR-0063): its
// states, each with the documents that are in it now, and its
// transitions, each with the moves along it in the period, with how
// long the stays that ended in the period lasted. It counts the
// documents the caller may read, by the read rule of the class, and no
// other. The zero period is since the class began.
//
//	flow, err := db.Flow(ctx, "Invoice", fado.FlowPeriod{Since: firstOfNovember}, fado.As(ann))
func (db *DB) Flow(ctx context.Context, class string, period FlowPeriod, opts ...Option) (*Flow, error) {
	return db.s.Flow(ctx, class, period, opts...)
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

// OpenBlob opens a blob by hash alone, for a content administrator.
func (db *DB) OpenBlob(ctx context.Context, hash string, opts ...Option) (io.ReadCloser, int64, error) {
	return db.s.OpenBlob(ctx, hash, opts...)
}

// CheckBlobs checks the files of the database both ways: it recomputes
// the hash of every blob the store holds and names the ones whose bytes
// do not match, and it names every file a document holds, in any
// version, whose bytes the store does not have, with its document.
func (db *DB) CheckBlobs(ctx context.Context, opts ...Option) (*BlobCheck, error) {
	return db.s.CheckBlobs(ctx, opts...)
}

// Ping reads from the database, to say that it answers. A server asks
// it for its health.
func (db *DB) Ping(ctx context.Context) error { return db.s.Ping(ctx) }

// ErrUnknownKey is what Authenticate answers for every key it does not
// let through, whatever is wrong with it.
var ErrUnknownKey = store.ErrUnknownKey

// ErrNoGrant is what Exchange and Renew answer for every code and pass
// they do not let through, whatever is wrong with it.
var ErrNoGrant = store.ErrNoGrant

// ErrNotProven is what Receive answers for every message whose proof
// does not hold, whatever is wrong with it, so that a guess learns
// nothing (ADR-0065, item 3).
var ErrNotProven = store.ErrNotProven

// The calls below keep the users, groups and keys of a database
// (ADR-0057). They are for a database opened with Options.Users, which
// a server opens for callers of its own, as fadodb serve does. A
// database whose program says itself who is calling keeps none: the
// calls answer not_supported and their commands are not offered.

// CreateUser makes a user: a person, an agent or a service the database
// knows by its id. It is a database administrator's.
func (db *DB) CreateUser(ctx context.Context, u User, opts ...Option) (*User, error) {
	return db.s.CreateUser(ctx, u, opts...)
}

// SetUser changes a user: the members of the edit that are set. It is a
// database administrator's, who may change their own groups too; a
// change that would leave the database without an administrator who is
// not disabled is refused.
func (db *DB) SetUser(ctx context.Context, id string, edit UserEdit, opts ...Option) (*User, error) {
	return db.s.SetUser(ctx, id, edit, opts...)
}

// GetUser reads one user. Every user of the database may ask.
func (db *DB) GetUser(ctx context.Context, id string, opts ...Option) (*User, error) {
	return db.s.GetUser(ctx, id, opts...)
}

// Users lists the users in the order of their ids, the disabled ones
// among them. Every user of the database may ask.
func (db *DB) Users(ctx context.Context, f UserFilter, opts ...Option) ([]*User, error) {
	return db.s.Users(ctx, f, opts...)
}

// CreateGroup makes a group, with nobody in it yet (ADR-0086): its name,
// which never changes and which INGROUP names, its label and what it is
// for. A group a user is put in gets a record by itself; this makes one
// before. It is a database administrator's.
func (db *DB) CreateGroup(ctx context.Context, g Group, opts ...Option) (*Group, error) {
	return db.s.CreateGroup(ctx, g, opts...)
}

// SetGroup changes a group's label and what it is for: the members of the
// edit that are set. Its name never changes. It is a database
// administrator's.
func (db *DB) SetGroup(ctx context.Context, name string, edit GroupEdit, opts ...Option) (*Group, error) {
	return db.s.SetGroup(ctx, name, edit, opts...)
}

// GetGroup reads one group, with how many users are in it. Every user of
// the database may ask.
func (db *DB) GetGroup(ctx context.Context, name string, opts ...Option) (*Group, error) {
	return db.s.GetGroup(ctx, name, opts...)
}

// Groups lists the groups in the order of their names, each with how
// many users are in it; not the system groups, which are the engine's.
// Every user of the database may ask.
func (db *DB) Groups(ctx context.Context, opts ...Option) ([]*Group, error) {
	return db.s.Groups(ctx, opts...)
}

// RemoveGroup takes a group away; it is refused while somebody is in it.
// It is a database administrator's.
func (db *DB) RemoveGroup(ctx context.Context, name string, opts ...Option) error {
	return db.s.RemoveGroup(ctx, name, opts...)
}

// GroupChanges lists every change to a group, oldest first, a removed
// group's too. It is a database administrator's.
func (db *DB) GroupChanges(ctx context.Context, name string, opts ...Option) ([]GroupChange, error) {
	return db.s.GroupChanges(ctx, name, opts...)
}

// Reads lists the log of reads, oldest first (ADR-0067): every document,
// row and file a content administrator saw past a rule, with the rules
// and the reason, and every SQL statement an analyst ran. It is for
// database administrators.
func (db *DB) Reads(ctx context.Context, f ReadsFilter, opts ...Option) ([]Read, error) {
	return db.s.Reads(ctx, f, opts...)
}

// UserChanges lists every change to a user and to its keys, oldest
// first. A database administrator may ask of any user, and a user of
// itself.
func (db *DB) UserChanges(ctx context.Context, id string, opts ...Option) ([]UserChange, error) {
	return db.s.UserChanges(ctx, id, opts...)
}

// CreateKey makes a key for a user and answers it with its secret, this
// once: the database keeps a hash of it, and a lost key is replaced.
// expires, when given, is the last day the key holds, as YYYY-MM-DD in
// UTC. It is a database administrator's.
func (db *DB) CreateKey(ctx context.Context, user, label, expires string, opts ...Option) (*UserKey, error) {
	return db.s.CreateKey(ctx, user, label, expires, opts...)
}

// Keys lists the keys of a user, never a secret; user left empty is the
// caller. A database administrator may ask of any user, and a user of
// itself.
func (db *DB) Keys(ctx context.Context, user string, opts ...Option) ([]*UserKey, error) {
	return db.s.Keys(ctx, user, opts...)
}

// RevokeKey ends a key, by its id: the next command sent with it is
// refused. A database administrator may revoke any key, and a user its
// own.
func (db *DB) RevokeKey(ctx context.Context, key string, opts ...Option) error {
	return db.s.RevokeKey(ctx, key, opts...)
}

// BeginSignIn takes the password of a person and answers what comes
// next: the session, the code of the second step, or setting the second
// step up (ADR-0071). Every refusal is in the same words. address is
// where the request came from. It is the server's sign-in page's: a
// password never travels in a command.
func (db *DB) BeginSignIn(ctx context.Context, user, password, address string) (*SignIn, error) {
	return db.s.BeginSignIn(ctx, user, password, address)
}

// SetUpSecondStep gives the secret of the second step of a sign-in that
// must set one up.
func (db *DB) SetUpSecondStep(ctx context.Context, signIn string) (*SecondStep, error) {
	return db.s.SetUpSecondStep(ctx, signIn)
}

// FinishSignIn takes the code of the second step, when one comes next,
// and gives the session, with its token this once.
func (db *DB) FinishSignIn(ctx context.Context, signIn, code, address string, lengths SessionLengths) (*Session, error) {
	return db.s.FinishSignIn(ctx, signIn, code, address, lengths)
}

// SignOn gives the session of a person a company's provider vouched for
// (ADR-0071, item 4): the provider's id for them finds the user it was
// tied to, and the first time the e-mail the provider vouches for finds
// the user an administrator gave it. Everybody else is refused as a
// wrong password is.
func (db *DB) SignOn(ctx context.Context, who SignedOn, address string, lengths SessionLengths) (*Session, error) {
	return db.s.SignOn(ctx, who, address, lengths)
}

// SessionCaller answers who a session's token belongs to, as Authenticate
// answers for a key.
func (db *DB) SessionCaller(ctx context.Context, token string) (Principal, *Session, error) {
	return db.s.SessionCaller(ctx, token)
}

// SignOut ends the session of a token.
func (db *DB) SignOut(ctx context.Context, token string) error { return db.s.SignOut(ctx, token) }

// ChangePassword changes the caller's own password, given the one it has.
func (db *DB) ChangePassword(ctx context.Context, old, password, address string, opts ...Option) error {
	return db.s.ChangePassword(ctx, old, password, address, opts...)
}

// CreatePasswordLink makes a link that lets a person set their password,
// good once and for a day. It is a database administrator's.
func (db *DB) CreatePasswordLink(ctx context.Context, user string, opts ...Option) (*PasswordLink, error) {
	return db.s.CreatePasswordLink(ctx, user, opts...)
}

// PasswordLinkUser answers whose a link to set a password is, while it
// holds.
func (db *DB) PasswordLinkUser(ctx context.Context, token string) (string, error) {
	return db.s.PasswordLinkUser(ctx, token)
}

// UsePasswordLink sets the password of the person a link is for; the
// link then holds no more, and the person's sessions end.
func (db *DB) UsePasswordLink(ctx context.Context, token, password, address string) error {
	return db.s.UsePasswordLink(ctx, token, password, address)
}

// CreateInvitation makes an invitation for a person, good once and for 7
// days, with which they add their first passkey, and answers its secret,
// this once. It is a database administrator's.
func (db *DB) CreateInvitation(ctx context.Context, user string, opts ...Option) (*Invitation, error) {
	return db.s.CreateInvitation(ctx, user, opts...)
}

// Invitations lists the invitations that still hold, of one person or
// of everybody. It is a database administrator's.
func (db *DB) Invitations(ctx context.Context, user string, opts ...Option) ([]*Invitation, error) {
	return db.s.Invitations(ctx, user, opts...)
}

// WithdrawInvitation ends the invitation of a person that still holds.
func (db *DB) WithdrawInvitation(ctx context.Context, user string, opts ...Option) error {
	return db.s.WithdrawInvitation(ctx, user, opts...)
}

// Invited answers whom an invitation is for and who made it, while it
// holds.
func (db *DB) Invited(ctx context.Context, token string) (*Invited, error) {
	return db.s.Invited(ctx, token)
}

// InvitationQuestion is the question the page of an invitation gives the
// browser to make the person's passkey, in WebAuthn's JSON.
func (db *DB) InvitationQuestion(ctx context.Context, token, origin string) (string, error) {
	return db.s.InvitationQuestion(ctx, token, origin)
}

// UseInvitation takes the passkey a person made with their invitation,
// on the device they named, and gives their session.
func (db *DB) UseInvitation(ctx context.Context, token string, answer []byte, device, origin, address string, lengths SessionLengths) (*Session, error) {
	return db.s.UseInvitation(ctx, token, answer, device, origin, address, lengths)
}

// SignInQuestion is the question the sign-in page gives the browser: to
// use any passkey it holds for the server's host.
func (db *DB) SignInQuestion(origin string) (string, error) { return db.s.SignInQuestion(origin) }

// SignInWithPasskey takes a browser's answer to the sign-in page's
// question and gives the person's session.
func (db *DB) SignInWithPasskey(ctx context.Context, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	return db.s.SignInWithPasskey(ctx, answer, origin, address, lengths)
}

// AddPasskeyQuestion is the question the account page gives the browser,
// to make another passkey of the caller's.
func (db *DB) AddPasskeyQuestion(ctx context.Context, origin string, opts ...Option) (string, error) {
	return db.s.AddPasskeyQuestion(ctx, origin, opts...)
}

// AddPasskey takes the passkey the caller made on the device it named.
func (db *DB) AddPasskey(ctx context.Context, answer []byte, device, origin string, opts ...Option) (*Passkey, error) {
	return db.s.AddPasskey(ctx, answer, device, origin, opts...)
}

// InvitationPhoneCode makes the code the page of an invitation shows:
// its link adds the person's passkey on a phone.
func (db *DB) InvitationPhoneCode(ctx context.Context, token string) (*PhoneCode, error) {
	return db.s.InvitationPhoneCode(ctx, token)
}

// AccountPhoneCode makes the code the account page shows: its link adds
// a passkey of the caller's on a phone.
func (db *DB) AccountPhoneCode(ctx context.Context, opts ...Option) (*PhoneCode, error) {
	return db.s.AccountPhoneCode(ctx, opts...)
}

// PhoneLink answers whom the link of a code is for, while it holds.
func (db *DB) PhoneLink(ctx context.Context, link string) (*PhoneFor, error) {
	return db.s.PhoneLink(ctx, link)
}

// PhoneQuestion is the question the phone's page gives the browser, to
// make the person's passkey on the phone.
func (db *DB) PhoneQuestion(ctx context.Context, link, origin string) (string, error) {
	return db.s.PhoneQuestion(ctx, link, origin)
}

// InvitationSignInQuestion is the question the page of an invitation
// gives a device that holds a passkey of the person's already: to use
// it, with the invitation.
func (db *DB) InvitationSignInQuestion(ctx context.Context, token, origin string) (string, error) {
	return db.s.InvitationSignInQuestion(ctx, token, origin)
}

// SignInWithInvitation takes the passkey the device holds already and
// gives the person's session; the invitation is used.
func (db *DB) SignInWithInvitation(ctx context.Context, token string, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	return db.s.SignInWithInvitation(ctx, token, answer, origin, address, lengths)
}

// PhoneSignInQuestion is the question the phone's page gives a phone that
// holds a passkey of the person's already: to use it, for this code.
func (db *DB) PhoneSignInQuestion(ctx context.Context, link, origin string) (string, error) {
	return db.s.PhoneSignInQuestion(ctx, link, origin)
}

// SignInByPhoneLink takes the passkey the phone holds already and gives
// the phone's session; the browser that showed the code may enter.
func (db *DB) SignInByPhoneLink(ctx context.Context, link string, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	return db.s.SignInByPhoneLink(ctx, link, answer, origin, address, lengths)
}

// UsePhoneLink takes the passkey the phone made and gives the phone's
// session; the browser that showed the code may then enter.
func (db *DB) UsePhoneLink(ctx context.Context, link string, answer []byte, device, origin, address string, lengths SessionLengths) (*Session, error) {
	return db.s.UsePhoneLink(ctx, link, answer, device, origin, address, lengths)
}

// PhoneCodeState answers how the code a browser showed stands: waiting,
// done or ended.
func (db *DB) PhoneCodeState(computer string) string { return db.s.PhoneCodeState(computer) }

// EnterByPhone gives the session of the browser that showed the code,
// once the phone has added its passkey.
func (db *DB) EnterByPhone(ctx context.Context, computer, address string, lengths SessionLengths) (*Session, error) {
	return db.s.EnterByPhone(ctx, computer, address, lengths)
}

// Passkeys lists the passkeys of a user, never a key.
func (db *DB) Passkeys(ctx context.Context, user string, opts ...Option) ([]*Passkey, error) {
	return db.s.Passkeys(ctx, user, opts...)
}

// RemovePasskey removes a passkey by its id; the sessions it began end.
func (db *DB) RemovePasskey(ctx context.Context, id string, opts ...Option) error {
	return db.s.RemovePasskey(ctx, id, opts...)
}

// SetUpMySecondStep gives the caller a secret for a second step of its
// own, which ConfirmMySecondStep confirms with its first code.
func (db *DB) SetUpMySecondStep(ctx context.Context, opts ...Option) (*SecondStep, error) {
	return db.s.SetUpMySecondStep(ctx, opts...)
}

// ConfirmMySecondStep keeps the caller's second step and answers the
// codes for when the phone is lost, this once.
func (db *DB) ConfirmMySecondStep(ctx context.Context, code, address string, opts ...Option) ([]string, error) {
	return db.s.ConfirmMySecondStep(ctx, code, address, opts...)
}

// HasSecondStep reports whether a user has a second step, and whether
// its system groups require one.
func (db *DB) HasSecondStep(ctx context.Context, user string, opts ...Option) (has, required bool, err error) {
	return db.s.HasSecondStep(ctx, user, opts...)
}

// ClearSecondStep takes away the second step of a user.
func (db *DB) ClearSecondStep(ctx context.Context, user string, opts ...Option) error {
	return db.s.ClearSecondStep(ctx, user, opts...)
}

// Sessions lists the sessions of a user that still hold.
func (db *DB) Sessions(ctx context.Context, user string, opts ...Option) ([]*Session, error) {
	return db.s.Sessions(ctx, user, opts...)
}

// EndSession ends a session by its id.
func (db *DB) EndSession(ctx context.Context, id string, opts ...Option) error {
	return db.s.EndSession(ctx, id, opts...)
}

// SignIns lists the log of sign-ins, refused ones among them. It is a
// database administrator's.
func (db *DB) SignIns(ctx context.Context, f SignInFilter, opts ...Option) ([]SignInEntry, error) {
	return db.s.SignIns(ctx, f, opts...)
}

// AddClient adds a client the server gives passes to: a face or an app
// of the company, for the commands, or an agent, for the door for agents
// (ADR-0071, item 6). A client known by the document it publishes, as
// Claude is, is never added. It is a database administrator's.
func (db *DB) AddClient(ctx context.Context, c Client, opts ...Option) (*Client, error) {
	return db.s.AddClient(ctx, c, opts...)
}

// Clients lists the clients an administrator added, the removed ones
// among them. It is a database administrator's.
func (db *DB) Clients(ctx context.Context, opts ...Option) ([]*Client, error) {
	return db.s.Clients(ctx, opts...)
}

// RemoveClient removes a client an administrator added: every grant of
// it ends. It is a database administrator's.
func (db *DB) RemoveClient(ctx context.Context, id string, opts ...Option) error {
	return db.s.RemoveClient(ctx, id, opts...)
}

// KnownClient answers a client an administrator added and did not
// remove, for the server's page that takes a client's request.
func (db *DB) KnownClient(ctx context.Context, id string) (*Client, error) {
	return db.s.KnownClient(ctx, id)
}

// AskConsent keeps a client's request, which the server checked, while
// the person signs in and is asked, and answers its id.
func (db *DB) AskConsent(ctx context.Context, a Asked) (string, error) {
	return db.s.AskConsent(ctx, a)
}

// Consent answers a client's request that waits for the person, for the
// page that asks them.
func (db *DB) Consent(ctx context.Context, id string, opts ...Option) (*Asked, error) {
	return db.s.Consent(ctx, id, opts...)
}

// Allow gives the client of a request the person allowed its code, good
// once and for five minutes.
func (db *DB) Allow(ctx context.Context, id string, opts ...Option) (string, *Asked, error) {
	return db.s.Allow(ctx, id, opts...)
}

// Deny answers a request the person did not allow.
func (db *DB) Deny(ctx context.Context, id string, opts ...Option) (*Asked, error) {
	return db.s.Deny(ctx, id, opts...)
}

// Exchange gives a client its passes for its code, proven with PKCE.
// For the door for agents, the agent the person connects is made the
// first time, with a substitution the person gives (ADR-0071, item 7).
func (db *DB) Exchange(ctx context.Context, x CodeExchange) (*Passes, error) {
	return db.s.Exchange(ctx, x)
}

// Renew gives a client new passes for its renewing pass, which holds no
// more after it; shown twice, it ends the grant.
func (db *DB) Renew(ctx context.Context, client, token, resource string) (*Passes, error) {
	return db.s.Renew(ctx, client, token, resource)
}

// Revoke ends the grant of a pass its client gives back.
func (db *DB) Revoke(ctx context.Context, client, token string) error {
	return db.s.Revoke(ctx, client, token)
}

// PassCaller answers who a pass belongs to at an address: the person at
// the commands, the person's agent at the door for agents. It writes
// nothing.
func (db *DB) PassCaller(ctx context.Context, token, address string) (Principal, error) {
	return db.s.PassCaller(ctx, token, address)
}

// Grants lists the clients a person allowed that still hold.
func (db *DB) Grants(ctx context.Context, user string, opts ...Option) ([]*Grant, error) {
	return db.s.Grants(ctx, user, opts...)
}

// EndGrant ends a grant by its id.
func (db *DB) EndGrant(ctx context.Context, id string, opts ...Option) error {
	return db.s.EndGrant(ctx, id, opts...)
}

// StepCode is the code an authenticator app shows for the secret of a
// second step at a moment, for tests and for a program that signs a
// person in on their behalf.
func StepCode(secret string, at time.Time) string { return store.StepCode(secret, at) }

// Whoami answers who the database takes the caller for, and whom the
// caller acts for when the call names somebody.
func (db *DB) Whoami(ctx context.Context, opts ...Option) (*User, string, error) {
	return db.s.Whoami(ctx, opts...)
}

// Authenticate answers who a key belongs to: the principal a command
// sent with the key runs as. A key the database does not know, one that
// was revoked or has ended, and the key of a disabled user all answer
// ErrUnknownKey. It reads nothing from the database and allocates
// nothing, so a server asks it for every request. The groups of the
// principal are the database's own lists: do not change them.
func (db *DB) Authenticate(key string) (Principal, error) {
	return db.s.Authenticate(key)
}

// Preview runs a create (empty id), a set or a transition through the
// commit pipeline and writes nothing (ADR-0025): the document as it would
// be, the states the command would visit and the transitions open from
// the last of them.
func (db *DB) Preview(ctx context.Context, class, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	return db.s.Preview(ctx, class, id, values, transition, opts...)
}

// PreviewDelete answers what a delete of the document would do and writes
// nothing (ADR-0049): the document as it is, the refusal of the class's
// delete rule under the preview's errors when the principal may not
// delete it, and the rules the delete would run.
func (db *DB) PreviewDelete(ctx context.Context, class, id string, opts ...Option) (*Preview, error) {
	return db.s.PreviewDelete(ctx, class, id, opts...)
}

// PreviewDraft compiles a class definition for this call alone and runs
// a create (empty id) or a set of an existing document of the stored
// class of the same name through the commit pipeline under it, writing
// and storing nothing (ADR-0025): a designer tries a draft on real
// documents, terminal ones included.
func (db *DB) PreviewDraft(ctx context.Context, definition []byte, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	return db.s.PreviewDraft(ctx, definition, id, values, transition, opts...)
}
