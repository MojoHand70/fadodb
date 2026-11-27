package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // the pure-Go driver, the core's one dependency (ADR-0002, ADR-0035)

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/formula/sqlitefn"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
)

// DefaultWALLimit is the size past which the write-ahead log is emptied
// as soon as the writers leave an instant for it (ADR-0035). Under
// writers that never pause the log grows to eight times it, and is
// then emptied in the writer's turn.
const DefaultWALLimit int64 = 64 << 20

// DefaultCheckpointEvery is how often the checkpointer runs a passive
// checkpoint.
const DefaultCheckpointEvery = 100 * time.Millisecond

// DefaultMaxIndexedRows is the row count up to which a table is held in
// memory for the lookups of auto formulas (ADR-0036).
const DefaultMaxIndexedRows = 100000

// DefaultMaxQuestion is how long a free question that no index serves
// may run before it is stopped (ADR-0075, item 6).
const DefaultMaxQuestion = 5 * time.Second

// The connections that read. readersKept is how many are kept ready
// between two reads, and readerIdle how long one stays ready after its
// last read. More callers than that may read at once: each beyond the
// eight has its connection opened for its read. Measured with sixteen
// callers reading at once, a read takes 11.9 µs with two kept ready,
// 6.7 µs with four, 4.3 µs with eight and 3.6 µs with sixteen, and each
// connection kept takes about 3.5 MB of the machine's memory in a
// database of fifty classes, and 0.5 MB in one of four.
const readersKept = 8

// readerIdle is how long a connection that reads stays ready after its
// last read. One connection is kept ready for good: the checkpointer
// asks after it more often than that, so that a caller who reads once
// a minute does not pay for a connection each time, which takes
// milliseconds where a read takes microseconds. A test shortens it.
var readerIdle = 15 * time.Second

// lastOfCommandSQL finds the last version a command made of its
// document: the versions of the moves its own commit caused, when it
// started a chain (ADR-0040). It is asked when a command is sent again
// with its key, and reads the events of that one document, through the
// index by document: a database's whole log is not searched for the
// answer of a repeated command, however long it has grown.
const lastOfCommandSQL = "SELECT max(after_version) FROM events WHERE class = ? AND doc_id = ? AND cause_event_id = ?"

// engineSchema is the version of the store's own tables. A database of
// a newer schema is refused. One of an earlier schema is brought to this
// one when it is opened, in the transaction that opens it, from
// oldestUpgraded on; a database older than that is refused and made
// again, as every pre-release file was before v0.7.0.
const engineSchema = 15

// oldestUpgraded is the earliest engine schema Open brings to the
// current one in place: 13, the schema of v0.7.0, the first release
// whose databases hold what people keep.
const oldestUpgraded = 13

// Options configure Open. The ones that are properties of the database,
// Synchronous and MaxText, are read at creation and kept in the metadata
// (ADR-0021, ADR-0035); opening an existing database with a different
// value is an error, since a database must behave the same wherever it is
// opened.
type Options struct {
	// Synchronous is NORMAL, the default, where a commit survives a crash
	// of the process and the last commits may be lost on power failure,
	// or FULL, where every commit is fsynced before it is acknowledged.
	Synchronous string
	// MaxText is the text limit of a new database in code points; 0 is
	// formula.DefaultMaxText.
	MaxText int
	// WALLimit is the size past which the log is emptied as soon as the
	// writers leave an instant for it; under writers that never pause it
	// grows to eight times that, and they then wait a moment for it to
	// be emptied. 0 is DefaultWALLimit.
	WALLimit int64
	// CheckpointEvery is the checkpointer's period; 0 is
	// DefaultCheckpointEvery.
	CheckpointEvery time.Duration
	// MaxIndexedRows is the row count up to which an explicit table is
	// held in memory for LOOKUP (ADR-0036). It is kept in the metadata;
	// a value given on open replaces the stored one, since it changes
	// speed and memory and never a result. 0 keeps what is stored, or
	// DefaultMaxIndexedRows for a new database.
	MaxIndexedRows int
	// MaxQuestion is how long a free question that no index serves may
	// run, the where and the order of a query or a count, before it is
	// stopped and refused with too_wide (ADR-0075, item 6). It is kept
	// in the metadata; a value given on open replaces the stored one,
	// since it changes what is waited for and never a result. 0 keeps
	// what is stored, or DefaultMaxQuestion for a new database.
	MaxQuestion time.Duration
	// TimeZone is the time zone of the database, by which its schedules
	// go (ADR-0093): the name the database of time zones gives it, as
	// Europe/Warsaw. It is kept in the metadata; a value given on open
	// replaces the stored one, and the schedules that wait are then
	// timed again. "" keeps what is stored, or UTC for a new database.
	// NOW() and TODAY() in a formula stay as they are, in UTC.
	TimeZone string
	// BlobStore is the kind of blob store a new database records
	// (ADR-0033): "sqlite", the default, a table in the database file;
	// "dir", a directory beside it; or "external", the embedder's store
	// given in Blobs. An existing database keeps its kind.
	BlobStore string
	// Blobs is the embedder's blob store, required when the database's
	// kind is external.
	Blobs BlobStore
	// MaxBlob is the blob size limit of a new database in bytes; 0 is
	// DefaultMaxBlob. It may be raised on open and never lowered.
	MaxBlob int64
	// Outbox configures the worker that runs the actions commits queue
	// (ADR-0044).
	Outbox OutboxOptions
	// Actions are the Go functions of this process that implement actions,
	// each under the name of the action its definition gives (ADR-0027,
	// ADR-0045). The definition is data and travels with the database;
	// the function is the host program's and is given at every Open, so
	// that the worker has it before it runs the first row. A function
	// under a name no definition has yet waits for DefineAction. A
	// function that calls the database captures the variable the result
	// of Open is assigned to.
	Actions map[string]ActionFunc
	// Outbound sends the HTTP requests of declared actions (ADR-0045, item
	// 7). nil, the default, sends nothing: a definition with a request
	// cannot be defined, and an action that needs one gets no answer and
	// dies saying so. A host program that allows outbound calls gives the
	// result of outbound.Allow, of package fado/outbound, with the hosts
	// that may be called.
	Outbound Outbound
	// Secrets gives the value of a secret of a connection at the moment a
	// request that reads it is sent (ADR-0045, item 6). It is asked at
	// every attempt, so a provider that renews a token serves it as it
	// serves a fixed one, and an error or an empty value is no answer. The
	// engine keeps what it returns for the one request and stores it
	// nowhere. nil supplies none: only definitions that read no secret can
	// be defined.
	Secrets func(ctx context.Context, connection, name string) (string, error)
	// HostAnswersFor says the host program keeps who may act for whom
	// itself and answers for Principal.For (ADR-0053, item 8): the engine
	// records the "for" it is told, as ADR-0050 built, and asks no
	// substitution. Without it the engine checks every call that names
	// somebody against the substitutions of the database. It is the host
	// program's, given at every Open, like its actions.
	HostAnswersFor bool
	// Alone, when it is not empty, says that this program holds the
	// database alone until it closes it (ADR-0059, item 7), and is the
	// name the program gives itself, such as "fadodb serve": another
	// program that opens the database meanwhile is refused with in_use
	// and told this name. A server opens its database so, since it
	// keeps in memory what it has read of it. Without it a program
	// holds nothing, and is refused only a database held alone.
	Alone string
	// Users says that the database keeps its own users, groups and keys
	// (ADR-0057): the calls that keep them work, their commands are
	// offered, and Authenticate answers who a key belongs to. It is how
	// a server opens a database for callers of its own, as fadodb serve
	// does. A host program that says itself who is calling leaves it
	// off: the database then keeps none, and those calls answer
	// not_supported.
	Users bool
	// Inbound says that this program takes messages from outside at the
	// inlets of the database (ADR-0065, item 8): an inlet can be
	// defined, and Receive acts on a message. Without it, the default,
	// a definition of an inlet is refused with not_supported and every
	// message is answered as one for a name that is no inlet. It is the
	// host program's, given at every Open, as whether it sends requests
	// is; fadodb serve gives it when its settings have inbound.
	Inbound bool
	// Analytics is the analytical engine of the program (ADR-0017,
	// ADR-0089): with one, the store keeps a copy of its documents in
	// it and feeds the copy itself; without one, the default, every
	// question is answered by the store. It is the host program's,
	// given at every Open. CopyEvery is the period at which the copy is
	// fed without being asked; 0 is DefaultCopyEvery.
	Analytics Analytics
	CopyEvery time.Duration
	// Now is the clock of the database: it stamps every commit and is
	// what NOW() and TODAY() read in every formula, unless a call that
	// reads or previews chooses a moment with At (ADR-0042). nil is
	// time.Now. A test sets it, and so does an embedder that replays a
	// scenario into a copy of the file.
	Now func() time.Time
}

// Store is one open database.
type Store struct {
	path        string
	db          *sql.DB
	writer      *sql.Conn
	mu          sync.Mutex // the single writer: commits, definitions, close
	limits      formula.Limits
	synchronous string
	maxIndexed  int
	maxQuestion time.Duration
	// zone is the database's time zone (ADR-0093), and rezoned says the
	// program that opened the database gave it another than it had.
	zone     *time.Location
	rezoned  bool
	maxBlob  int64
	blobKind string
	blobs    BlobStore
	now      func() time.Time
	vm       *formula.VM
	qvm      *formula.VM // runs the key expressions of indexed lookups while vm runs the formula
	wev      evaluator   // the writer's evaluator over vm, with its scratch
	// curArgs, histArgs and eventArgs are the write step's argument
	// lists, kept between commits; dayArg is the business day of dayOf,
	// boxed.
	curArgs, updArgs, histArgs, eventArgs []any
	// acting is what the principal of the command in progress is and whom
	// it acts for, boxed for the events the command writes (ADR-0050);
	// forID is the id acts boxes.
	acting struct {
		kind, acts any
		forID      string
		// override and reason are the rules the command got past as a
		// content administrator and why (ADR-0052, item 4), for the
		// command's own event; nil for a command the rules let through.
		override, reason any
	}
	// stateArgs are the names of states boxed once for the statements of
	// the write step: every commit writes its document's state, and an
	// entry the state left and the state entered beside it.
	stateArgs map[string]any
	dayOf     int64
	dayArg    any
	readers   sync.Pool // evaluators for the read paths' projections (ADR-0041)
	dirty     []bool
	given     []bool   // the variables one command sets, for the write check
	visits    []string // the states one command visits, reused under the writer's lock
	// The scratch of the rules of one command (ADR-0043), reused under the
	// writer's lock: the eventless commits, the custom events to append,
	// the values of a set before they are assigned, the composites a reset
	// emptied, and the variables and the composites that changed.
	hops    []hop
	emits   []emitted
	queued  []queued // the actions for the outbox one command asked for
	lists   []list   // the composites the creates with each of one command read
	setVals []formula.Value
	emptied []*child
	// listEmptied says the reset that just ran emptied a list.
	listEmptied bool
	// keptNow are the lists that were brought in the step of the commit
	// being computed (ADR-0078, item 5), until that step takes them for
	// its write.
	keptNow     []keptList
	changed     []bool
	changedKids []*child
	// kids are the lists the command of the commit in progress assigns,
	// and the ones read for it: one map for every commit, emptied at each,
	// so that the writer's evaluator can hold it for the folds of the
	// commit's formulas (ADR-0081, step 2) without a map made per commit.
	kids    map[*child][]childRow
	ids     idGen
	stmts   engineStmts
	plans   map[*formula.QuerySlot]*lookupPlan
	filters filterStmts // the prepared statements of Query and Count, by text

	cmu        sync.RWMutex
	schemas    *model.SchemaSet
	schemaJSON map[string][]byte
	classes    map[string]*class
	versions   map[string]map[int]*class // every compiled version of a class, by upper-cased name
	// writesBack names the classes of which some version has an action
	// with an into (ADR-0045, item 8), by declared name as an outbox row
	// carries it: the worker looks no further for a row of any other.
	writesBack map[string]bool
	order      []string
	tables     map[string]*table
	tableOrder []string
	// sourceSet is the tables as the binder sees them, made again whole
	// whenever a table is registered or removed, and never changed once
	// made: a formula compiled while another table is defined reads the
	// set it was given. Every database has the four system tables
	// (ADR-0092), so a set made for each compilation would cost their
	// allocations every time.
	sourceSet *formula.SourceSet
	// actions are the action definitions by upper-cased name (ADR-0045),
	// and funcs the functions Open was given, which never change.
	actions     map[string]*action
	actionOrder []string
	funcs       map[string]ActionFunc
	// outbound sends the requests of declared actions and secretFn
	// supplies their secrets (ADR-0045); both are the host program's, and
	// nil when it gave none.
	outbound Outbound
	secretFn func(ctx context.Context, connection, name string) (string, error)
	// inlets are the inlets by upper-cased name (ADR-0065), and inbound
	// says that this process takes their messages, which is the host
	// program's to say.
	inlets     map[string]*inlet
	inletOrder []string
	inbound    bool
	// apps are the apps by upper-cased name (ADR-0077), and appOrder
	// their names in the order they were defined.
	apps     map[string]*app
	appOrder []string
	// holders are who holds each role of an app on this database
	// (ADR-0100), made whole again after each change.
	holders atomic.Pointer[holderSet]
	// words are the words in other languages by kind, upper-cased name
	// and language (ADR-0068, item 6), in the order first defined.
	words      map[string]*wordsDef
	wordsOrder []string
	// defined counts the definitions of classes, schemas and words made
	// since the store opened, under cmu; a bundle built before the last
	// of them is built again (ADR-0070). bundles holds the bundles
	// built, by class, version and language, under bmu.
	defined uint64
	bmu     sync.Mutex
	bundles map[string]*keptBundle

	// sqlConn is the read-only SQL endpoint's connection, with its
	// authorizer, made on first use; sqlMu serialises its statements.
	sqlMu   sync.Mutex
	sqlConn *sql.Conn

	stop   chan struct{}
	done   chan struct{}
	closed bool
	// feeder keeps the copy of the documents in the analytical engine
	// (ADR-0089), nil when the program gave none; copyErr is why a copy
	// could not be opened.
	feeder  *feeder
	copyErr error
	// turns counts the times the checkpointer took the writer's turn and
	// emptied a log far past its limit, which nothing else does; the
	// tests read it.
	turns atomic.Int64
	// listeners are the streams of changes that are open (ADR-0058): a
	// commit that ends wakes each. The list is replaced whole when a
	// stream opens or closes, so that a commit reads it without a lock.
	listeners  atomic.Pointer[[]*Changes]
	listenerMu sync.Mutex
	// tail is the end of the log as the streams share it: read from the
	// database once for all of them.
	tail logTail

	// The outbox (ADR-0044): its options with the defaults filled in, the
	// lock that makes one drain by hand run at a time, the documents that
	// have an action in flight, and the worker's wake-up, stop and end.
	outbox   OutboxOptions
	drainMu  sync.Mutex
	flightMu sync.Mutex
	inFlight map[flight]struct{}
	// followRuns is where each follow that is under way stands between
	// two of its transactions (ADR-0091), by the key of its row; under mu.
	followRuns map[string]*followRun
	// followEach is how many documents one transaction of a follow
	// brings when a test sets it; 0 is followMany.
	followEach int
	wake       chan struct{}
	// sleepUntil is the second of the database's clock at which the worker
	// looks at the outbox again when nothing wakes it, 0 while it is
	// awake; wakeAt, under the writer's lock, the earliest moment of the
	// wake-ups the transaction in progress queued, 0 for none (ADR-0047).
	sleepUntil atomic.Int64
	wakeAt     int64
	// looks counts the worker's looks at the outbox, for the tests.
	looks      atomic.Int64
	workCancel context.CancelFunc
	workDone   chan struct{}
	workStop   sync.Once
	// life is the writer's scratch for reading a document's events, and
	// lifeReads counts the reads, for the functions of a state (ADR-0047,
	// item 6) and the tests of "once per command".
	life      lifeReader
	lifeReads int
	// dueLists counts the lists of documents that Due read to look at
	// them one by one (ADR-0047, item 8), for the tests of when it does.
	dueLists atomic.Int64
	// held is the hold of this program on the database, nil when it
	// holds it with others (ADR-0059, item 7).
	held *hold
	// people is what a database that keeps its own users holds of them
	// in memory (ADR-0057).
	people people
	// sign is what the store holds of signing in, and auth of the passes
	// it gives clients (ADR-0071).
	sign signing
	auth passing
	// subs is what the check of "for" keeps (ADR-0053), and bringing says
	// Open is defining a class of the engine's own, which may carry a
	// name the engine keeps.
	subs     substitutions
	bringing bool
	// testStep is called at the steps of an action when a test sets it.
	testStep func(step string, row *outboxRow) *Error
	// testMoved, when a test sets it, may refuse a document of a batch of
	// moves after its commit is written, so that the test sees the batch
	// take it back.
	testMoved func(id string) *Error
}

type engineStmts struct {
	lookupKey, lastOfCommand, insertEvent, insertEventDoc, listEvents *sql.Stmt
	// selectLife reads the events of one document that its life by state
	// is told from (ADR-0047, item 6), and selectMoves its creation and
	// its moves alone.
	selectLife, selectMoves                                  *sql.Stmt
	insertOutbox, dueOutbox, dueEngine, dueCalls, callOutbox *sql.Stmt
	// mark, unmark and toMark are the savepoint a batch of moves sets
	// before each document, releases when the document moved and returns
	// to when it was refused (ADR-0048, item 3).
	mark, unmark, toMark *sql.Stmt
}

func (e *engineStmts) close() {
	for _, st := range []*sql.Stmt{e.lookupKey, e.lastOfCommand, e.insertEvent, e.insertEventDoc, e.listEvents, e.selectLife, e.selectMoves, e.insertOutbox, e.dueOutbox, e.dueEngine, e.dueCalls, e.callOutbox, e.mark, e.unmark, e.toMark} {
		if st != nil {
			st.Close()
		}
	}
}

// Open opens the database at path, creating it when it does not exist,
// and loads its schemas and classes. A database that another program
// holds alone is refused with in_use (ADR-0059, item 7).
func Open(path string, o Options) (*Store, error) {
	if path == "" {
		return nil, errf(CodeBadValue, "", "a database path is required")
	}
	var held *hold
	if o.Alone != "" {
		now := time.Now
		if o.Now != nil {
			now = o.Now
		}
		h, err := holdAlone(path, o.Alone, now())
		if err != nil {
			return nil, err
		}
		held = h
	} else if err := heldAlone(path); err != nil {
		return nil, err
	}
	s, err := open(path, o)
	if err != nil {
		held.release()
		return nil, err
	}
	s.held = held
	return s, nil
}

// open is Open once the database is known to be free.
func open(path string, o Options) (*Store, error) {
	if path == "" {
		return nil, errf(CodeBadValue, "", "a database path is required")
	}
	sync := strings.ToUpper(o.Synchronous)
	if sync != "" && sync != "NORMAL" && sync != "FULL" {
		return nil, errf(CodeBadValue, "", "synchronous is NORMAL or FULL, got %q", o.Synchronous)
	}
	if o.MaxText != 0 && o.MaxText < formula.MinMaxText {
		return nil, errf(CodeBadValue, "", "the text limit is at least %d code points, got %d", formula.MinMaxText, o.MaxText)
	}
	switch o.BlobStore {
	case "", "sqlite", "dir", "external":
	default:
		return nil, errf(CodeBadValue, "", "the blob store is sqlite, dir or external, got %q", o.BlobStore)
	}
	if o.BlobStore == "external" && o.Blobs == nil {
		return nil, errf(CodeBadValue, "", "an external blob store needs Blobs")
	}
	if o.MaxBlob < 0 {
		return nil, errf(CodeBadValue, "", "the blob limit is not negative")
	}
	funcs, err := checkFuncs(o.Actions)
	if err != nil {
		return nil, err
	}
	// The SQL the filter and the views run calls the engine's functions,
	// fado_upper and the rest; they are registered with the driver once
	// per process, before the first connection opens.
	if err := sqlitefn.Register(); err != nil {
		return nil, storageErr("register the engine functions", err)
	}
	// A database is its owner's: a new one is made readable and writable
	// by the user that creates it and by nobody else, and SQLite gives
	// the files it keeps beside it, the log and its index, the
	// permissions of the database. One that exists keeps what it has.
	if err := createPrivate(path); err != nil {
		return nil, storageErr("create the database", err)
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(OFF)")
	q.Add("_pragma", "wal_autocheckpoint(0)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, storageErr("open", err)
	}
	// Every reader reads on a connection of its own, and a connection
	// costs far more to open than a read costs. The library keeps two
	// ready unless it is told otherwise, so that sixteen callers at once
	// had their connections opened and closed over and over. Eight are
	// kept ready, and one that nobody has used for a quarter of a
	// minute is closed, so that a quiet database holds what it held: a
	// connection that reads takes two open files, and about 3.5 MB
	// where the database has fifty classes.
	db.SetMaxIdleConns(readersKept)
	db.SetConnMaxIdleTime(readerIdle)
	s := &Store{
		path: path, db: db, now: o.Now, vm: formula.NewVM(), qvm: formula.NewVM(),
		schemas: model.NewSchemaSet(), schemaJSON: map[string][]byte{}, classes: map[string]*class{}, versions: map[string]map[int]*class{},
		tables: map[string]*table{}, plans: map[*formula.QuerySlot]*lookupPlan{},
		actions: map[string]*action{}, funcs: funcs, inFlight: map[flight]struct{}{}, writesBack: map[string]bool{},
		outbound: o.Outbound, secretFn: o.Secrets, inlets: map[string]*inlet{}, inbound: o.Inbound, words: map[string]*wordsDef{}, apps: map[string]*app{},
		stop: make(chan struct{}), done: make(chan struct{}),
		outbox: o.Outbox, wake: make(chan struct{}, 1), workDone: make(chan struct{}),
	}
	s.subs.host = o.HostAnswersFor
	s.people.on = o.Users
	if s.now == nil {
		s.now = time.Now
	}
	if s.outbox.Workers <= 0 {
		s.outbox.Workers = DefaultOutboxWorkers
	}
	if s.outbox.MaxAttempts <= 0 {
		s.outbox.MaxAttempts = DefaultMaxAttempts
	}
	if s.outbox.Backoff <= 0 {
		s.outbox.Backoff = DefaultBackoff
	}
	ctx := context.Background()
	if s.writer, err = db.Conn(ctx); err != nil {
		db.Close()
		return nil, storageErr("open the writer connection", err)
	}
	fail := func(err error) (*Store, error) {
		// What was prepared before the failure is released, so that the
		// file is free at once: the classes and tables loaded before the
		// one that failed hold statements, and SQLite keeps a file open
		// while a statement on it lives.
		for _, vs := range s.versions {
			for _, c := range vs {
				c.close()
				query.Forget(c.cls)
			}
		}
		for _, t := range s.tables {
			t.close()
		}
		s.closePlans()
		s.stmts.close()
		s.writer.Close()
		db.Close()
		return nil, err
	}
	if err := s.init(ctx, sync, o.MaxText, o.MaxIndexedRows, o.MaxQuestion, o.BlobStore, o.MaxBlob, o.TimeZone); err != nil {
		return fail(err)
	}
	if s.people.on {
		if err := s.loadPeople(ctx); err != nil {
			return fail(err)
		}
		if err := s.loadSessions(ctx); err != nil {
			return fail(err)
		}
		if err := s.loadPasses(ctx); err != nil {
			return fail(err)
		}
	}
	switch s.blobKind {
	case "sqlite":
		s.blobs = sqliteBlobs{s: s}
	case "dir":
		s.blobs = dirBlobs{dir: path + ".blobs"}
	case "external":
		if o.Blobs == nil {
			return fail(hintf(CodeBadValue, "", "the database records an external blob store", "open it with Options.Blobs set to the embedder's store"))
		}
		s.blobs = o.Blobs
	}
	if _, err := s.writer.ExecContext(ctx, "PRAGMA synchronous="+s.synchronous); err != nil {
		return fail(storageErr("set synchronous", err))
	}
	s.vm.Limits, s.qvm.Limits = s.limits, s.limits
	s.vm.Executor = (*executor)(s)
	s.wev.vm = s.vm
	s.wev.life = s.tellLife
	s.wev.writer = true
	if err := s.prepareEngine(ctx); err != nil {
		return fail(err)
	}
	if err := s.load(ctx); err != nil {
		return fail(err)
	}
	// The indexes of the classes (ADR-0075, item 2): a database of an
	// earlier schema gets them here, in the time its size asks for.
	s.reask()
	if err := s.keepIndexes(ctx); err != nil {
		return fail(err)
	}
	// The classes the engine brings (ADR-0053, item 7), once what is
	// stored is loaded: a new database gets them here.
	if err := s.bring(ctx); err != nil {
		return fail(err)
	}
	// The schedules of the classes (ADR-0093): each has a row that waits
	// for its next moment, by the clock of the database's time zone.
	if err := s.openSchedules(ctx, s.rezoned); err != nil {
		return fail(err)
	}
	ckpt, err := db.Conn(ctx)
	if err != nil {
		return fail(storageErr("open the checkpointer connection", err))
	}
	every, limit := o.CheckpointEvery, o.WALLimit
	if every <= 0 {
		every = DefaultCheckpointEvery
	}
	if limit <= 0 {
		limit = DefaultWALLimit
	}
	go s.checkpoint(ckpt, every, limit)
	// The worker of the outbox, unless the caller drains it by hand.
	if s.outbox.Manual {
		close(s.workDone)
	} else {
		var wctx context.Context
		wctx, s.workCancel = context.WithCancel(context.Background())
		go s.work(wctx)
	}
	// The copy in the analytical engine, when the program gave one.
	s.startCopy(ctx, o.Analytics, o.CopyEvery)
	return s, nil
}

// createPrivate makes the file of a new database, empty and closed to
// everybody but its owner, for SQLite to open. A file that exists is
// left as it is.
func createPrivate(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}

// upgradeSchema brings the engine's own tables from an earlier schema,
// oldestUpgraded or later, to the current one. It runs inside the
// transaction that opens the database, so a database is at one schema or
// at the other and never between. An engine of the earlier schema no
// longer opens the file afterwards: whoever may go back keeps a copy.
func upgradeSchema(ctx context.Context, w *sql.Conn, from int64) error {
	if from < 14 {
		// Schema 14 (ADR-0078, item 2): an event keeps the version its
		// command saw, and the _events view of every class shows it.
		if _, err := w.ExecContext(ctx, "ALTER TABLE events ADD COLUMN seen_version INTEGER"); err != nil {
			return storageErr("add seen_version to the events", err)
		}
		rows, err := w.QueryContext(ctx, "SELECT name FROM classes ORDER BY rowid")
		if err != nil {
			return storageErr("read the classes", err)
		}
		var classes []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return storageErr("read the classes", err)
			}
			classes = append(classes, name)
		}
		if err := rows.Close(); err != nil {
			return storageErr("read the classes", err)
		}
		for _, name := range classes {
			for _, ddl := range []string{"DROP VIEW IF EXISTS " + quote(name+"_events"), eventsViewDDL(name)} {
				if _, err := w.ExecContext(ctx, ddl); err != nil {
					return storageErr("make the events view of "+name+" again", err)
				}
			}
		}
	}
	// Schema 15 (ADR-0075, item 2): the engine keeps its own indexes on
	// every class table. They are made once the classes are loaded
	// (keepIndexes), since only a compiled class says which it has.
	if _, err := w.ExecContext(ctx, "UPDATE schema_version SET version = ?", engineSchema); err != nil {
		return storageErr("write the schema version", err)
	}
	return nil
}

// init creates the engine's tables when the database is new and reads
// the metadata.
func (s *Store) init(ctx context.Context, wantSync string, wantMaxText int, wantIndexed int, wantQuestion time.Duration, wantBlobs string, wantMaxBlob int64, wantZone string) error {
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	rollback := func(err error) error {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return err
	}
	for _, ddl := range append(append(append(append(append(append(append(append(append(append([]string(nil), engineDDL...), usersDDL...), groupsDDL...), signinDDL...), passkeysDDL...), signonDDL...), oauthDDL...), readsDDL...), commentsDDL...), rolesDDL...) {
		if _, err := w.ExecContext(ctx, ddl); err != nil {
			return rollback(storageErr("create the engine tables", err))
		}
	}
	if err := recordGroupsOfUsers(ctx, w, s.now().Unix()); err != nil {
		return rollback(err)
	}
	var version int64
	err := w.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version)
	switch {
	case err == sql.ErrNoRows:
		sync := wantSync
		if sync == "" {
			sync = "NORMAL"
		}
		maxText := wantMaxText
		if maxText == 0 {
			maxText = formula.DefaultMaxText
		}
		now := s.now().Unix()
		if _, err := w.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", engineSchema); err != nil {
			return rollback(storageErr("write the schema version", err))
		}
		indexed := wantIndexed
		if indexed <= 0 {
			indexed = DefaultMaxIndexedRows
		}
		blobs := wantBlobs
		if blobs == "" {
			blobs = "sqlite"
		}
		maxBlob := wantMaxBlob
		if maxBlob == 0 {
			maxBlob = DefaultMaxBlob
		}
		for _, kv := range [][2]string{{"synchronous", sync}, {"max_text", strconv.Itoa(maxText)}, {"max_indexed_rows", strconv.Itoa(indexed)},
			{"blob_store", blobs}, {"max_blob", strconv.FormatInt(maxBlob, 10)}, {"created_at", strconv.FormatInt(now, 10)}} {
			if _, err := w.ExecContext(ctx, "INSERT INTO metadata (key, value) VALUES (?, ?)", kv[0], kv[1]); err != nil {
				return rollback(storageErr("write the metadata", err))
			}
		}
	case err != nil:
		return rollback(storageErr("read the schema version", err))
	case version > engineSchema:
		return rollback(errf(CodeNotSupported, "", "the database was created by a newer engine (schema %d, this one reads %d)", version, engineSchema))
	case version < oldestUpgraded:
		return rollback(hintf(CodeNotSupported, "", fmt.Sprintf("the database was created by an earlier pre-release engine (schema %d, this one reads %d)", version, engineSchema),
			fmt.Sprintf("a database is brought to this schema from schema %d on, the one of v0.7.0; an earlier file is recreated", oldestUpgraded)))
	case version < engineSchema:
		if err := upgradeSchema(ctx, w, version); err != nil {
			return rollback(err)
		}
	}
	if wantIndexed > 0 {
		if _, err := w.ExecContext(ctx, "INSERT INTO metadata (key, value) VALUES ('max_indexed_rows', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", strconv.Itoa(wantIndexed)); err != nil {
			return rollback(storageErr("write the metadata", err))
		}
	}
	if wantZone != "" {
		// The time zone the program gives (ADR-0093): one this machine
		// knows, kept in place of the one the database had.
		if _, e := zoneOf(wantZone); e != nil {
			return rollback(e)
		}
		had := DefaultTimeZone
		if err := w.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key = 'timezone'").Scan(&had); err != nil && err != sql.ErrNoRows {
			return rollback(storageErr("read the metadata", err))
		}
		s.rezoned = had != wantZone
		if _, err := w.ExecContext(ctx, "INSERT INTO metadata (key, value) VALUES ('timezone', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", wantZone); err != nil {
			return rollback(storageErr("write the metadata", err))
		}
	}
	if wantQuestion > 0 {
		// In microseconds: a test stops a question after very little.
		if _, err := w.ExecContext(ctx, "INSERT INTO metadata (key, value) VALUES ('max_question_us', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", strconv.FormatInt(wantQuestion.Microseconds(), 10)); err != nil {
			return rollback(storageErr("write the metadata", err))
		}
	}
	if wantMaxBlob > 0 {
		var current string
		if err := w.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key = 'max_blob'").Scan(&current); err == nil {
			if stored, _ := strconv.ParseInt(current, 10, 64); wantMaxBlob < stored {
				return rollback(hintf(CodeBadValue, "", "the database's blob limit is "+current+" bytes; it may be raised, never lowered (ADR-0033)", "open it without MaxBlob, or with a larger one"))
			}
		}
		if _, err := w.ExecContext(ctx, "INSERT INTO metadata (key, value) VALUES ('max_blob', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", strconv.FormatInt(wantMaxBlob, 10)); err != nil {
			return rollback(storageErr("write the metadata", err))
		}
	}
	rows, err := w.QueryContext(ctx, "SELECT key, value FROM metadata")
	if err != nil {
		return rollback(storageErr("read the metadata", err))
	}
	meta := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return rollback(storageErr("read the metadata", err))
		}
		meta[k] = v
	}
	rows.Close()
	if err := s.endTransaction(ctx); err != nil {
		return storageErr("commit", err)
	}
	s.synchronous = meta["synchronous"]
	if s.synchronous != "NORMAL" && s.synchronous != "FULL" {
		return errf(CodeStorage, "", "the metadata has synchronous %q", s.synchronous)
	}
	maxText, _ := strconv.Atoi(meta["max_text"])
	if maxText < formula.MinMaxText {
		return errf(CodeStorage, "", "the metadata has max_text %q", meta["max_text"])
	}
	s.limits = formula.Limits{MaxText: maxText}
	if s.maxIndexed, _ = strconv.Atoi(meta["max_indexed_rows"]); s.maxIndexed <= 0 {
		s.maxIndexed = DefaultMaxIndexedRows
	}
	if us, _ := strconv.ParseInt(meta["max_question_us"], 10, 64); us > 0 {
		s.maxQuestion = time.Duration(us) * time.Microsecond
	} else {
		s.maxQuestion = DefaultMaxQuestion
	}
	if s.maxBlob, _ = strconv.ParseInt(meta["max_blob"], 10, 64); s.maxBlob <= 0 {
		s.maxBlob = DefaultMaxBlob
	}
	zoneName := meta["timezone"]
	if zoneName == "" {
		zoneName = DefaultTimeZone
	}
	zone, zerr := zoneOf(zoneName)
	if zerr != nil {
		zerr.Message = "the database's time zone: " + zerr.Message
		return zerr
	}
	s.zone = zone
	s.blobKind = meta["blob_store"]
	if s.blobKind == "" {
		s.blobKind = "sqlite"
	}
	if s.blobKind != "sqlite" && s.blobKind != "dir" && s.blobKind != "external" {
		return errf(CodeStorage, "", "the metadata has blob_store %q", s.blobKind)
	}
	if wantBlobs != "" && wantBlobs != s.blobKind {
		return hintf(CodeBadValue, "", "the database was created with the "+s.blobKind+" blob store, not "+wantBlobs,
			"the store is the database's (ADR-0033); open it without one")
	}
	if wantSync != "" && wantSync != s.synchronous {
		return hintf(CodeBadValue, "", "the database was created with synchronous "+s.synchronous+", not "+wantSync,
			"the setting is the database's (ADR-0035); open it without one")
	}
	if wantMaxText != 0 && wantMaxText != maxText {
		return hintf(CodeBadValue, "", "the database was created with a text limit of "+strconv.Itoa(maxText)+" code points, not "+strconv.Itoa(wantMaxText),
			"the limit is the database's (ADR-0021); open it without one")
	}
	return nil
}

func (s *Store) prepareEngine(ctx context.Context) error {
	var err error
	prep := func(dst **sql.Stmt, text string) {
		if err != nil {
			return
		}
		*dst, err = s.writer.PrepareContext(ctx, text)
	}
	prep(&s.stmts.lookupKey, "SELECT kind, class, doc_id, after_version, command_hash, event_id FROM events WHERE command_key = ?")
	prep(&s.stmts.lastOfCommand, lastOfCommandSQL)
	prep(&s.stmts.insertEvent, "INSERT INTO events (event_id, class, doc_id, kind, name, from_state, to_state, actor_id, before_version, after_version, cause_event_id, command_key, command_hash, business_date, ts, origin, actor_kind, actor_for, override, reason, seen_version)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	prep(&s.stmts.insertEventDoc, "INSERT INTO event_docs (event_id, class, doc_id, qualifier) VALUES (?, ?, ?, ?)")
	prep(&s.stmts.selectLife, lifeSQL)
	prep(&s.stmts.selectMoves, movesSQL)
	prep(&s.stmts.mark, "SAVEPOINT many")
	prep(&s.stmts.unmark, "RELEASE many")
	prep(&s.stmts.toMark, "ROLLBACK TO many")
	prep(&s.stmts.insertOutbox, "INSERT INTO outbox (event_id, idx, class, doc_id, version, class_version, rule, action, args, principal_id, principal_groups, depth, status, attempts, due_at, error_code, error_message, created_at, finished_at, outcome_event)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return storageErr("prepare", err)
	}
	// An event of kind action_failed is read with the code its action died
	// with, which the outbox row holds (ADR-0044).
	if s.stmts.listEvents, err = s.db.PrepareContext(ctx, "SELECT e.event_id, e.class, e.doc_id, e.kind, e.name, e.from_state, e.to_state, e.actor_id, e.before_version, e.after_version, e.cause_event_id, e.command_key, e.business_date, e.ts,"+
		" (SELECT o.error_code FROM outbox o WHERE o.outcome_event = e.event_id), e.origin, e.actor_kind, e.actor_for, e.override, e.reason, e.seen_version"+
		" FROM events e JOIN event_docs d ON d.event_id = e.event_id WHERE d.class = ? AND d.doc_id = ? ORDER BY e.ts, e.event_id"); err != nil {
		return storageErr("prepare", err)
	}
	// The next actions that may run: pending, due, and not behind an
	// earlier pending action of the same document, so at most one per
	// document; the index on (status, due_at, event_id, idx) gives them in
	// order without a sort. A drain by hand takes them all; the worker
	// takes the engine's own actions and the ones that leave the database
	// apart, so that the second, which may take long, never stand in the
	// way of the first. A wake-up stands outside that order (ADR-0047):
	// it waits for its moment, which may be far off, and holds back no
	// action of its document meanwhile; and it is the document's own
	// move, which no action of the document holds back. A follow stands
	// outside it too (ADR-0091, ADR-0093): it names no document, and the
	// one of a schedule waits for a moment that may be a month off.
	engine := "'" + strings.Join([]string{model.ActionCreate, model.ActionTransition, model.ActionRefresh, model.ActionTableInsert, model.ActionTableUpdate, model.ActionTableDelete, ActionTimer, ActionFollow}, "', '") + "'"
	for _, due := range []struct {
		dst  **sql.Stmt
		only string
	}{{&s.stmts.dueOutbox, ""}, {&s.stmts.dueEngine, " AND o.action IN (" + engine + ")"}, {&s.stmts.dueCalls, " AND o.action NOT IN (" + engine + ")"}} {
		*due.dst, err = s.db.PrepareContext(ctx, "SELECT o.event_id, o.idx, o.class, o.doc_id, o.action, o.args, o.principal_id, o.principal_groups, o.depth FROM outbox o"+
			" WHERE o.status = 'pending' AND o.due_at <= ?"+due.only+
			" AND (o.action IN ('"+ActionTimer+"', '"+ActionFollow+"') OR NOT EXISTS (SELECT 1 FROM outbox p WHERE p.class = o.class AND p.doc_id = o.doc_id AND p.status = 'pending' AND p.action NOT IN ('"+ActionTimer+"', '"+ActionFollow+"')"+
			" AND (p.event_id < o.event_id OR (p.event_id = o.event_id AND p.idx < o.idx))))"+
			" ORDER BY o.due_at, o.event_id, o.idx LIMIT 64")
		if err != nil {
			return storageErr("prepare", err)
		}
	}
	// What the call of an action carries beside its arguments: the row's
	// state and the event that fired the rule (ADR-0045, item 4). The
	// event of an eventless transition has no key of its own; the command
	// that caused it has.
	s.stmts.callOutbox, err = s.db.PrepareContext(ctx, "SELECT o.status, o.attempts, o.version, o.class_version, e.kind, e.name, e.from_state, e.to_state, e.actor_id,"+
		" coalesce(e.command_key, (SELECT c.command_key FROM events c WHERE c.event_id = e.cause_event_id)), e.business_date, e.ts, e.origin"+
		" FROM outbox o JOIN events e ON e.event_id = o.event_id WHERE o.event_id = ? AND o.idx = ?")
	return storageErr("prepare", err)
}

// load reads the stored schemas and classes, in the order they were
// defined, and compiles them.
func (s *Store) load(ctx context.Context) error {
	// The apps, which the classes name (ADR-0077).
	if err := s.loadApps(ctx); err != nil {
		return err
	}
	if err := s.loadRoleHolders(ctx); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name, version, json FROM schemas ORDER BY rowid")
	if err != nil {
		return storageErr("read the schemas", err)
	}
	type stored struct {
		name    string
		version int
		json    []byte
	}
	var schemas, classes []stored
	for rows.Next() {
		var st stored
		if err := rows.Scan(&st.name, &st.version, &st.json); err != nil {
			rows.Close()
			return storageErr("read the schemas", err)
		}
		schemas = append(schemas, st)
	}
	rows.Close()
	for _, st := range schemas {
		def, diags := model.ParseSchema(st.json)
		if len(diags) == 0 {
			var sch *model.Schema
			if sch, diags = s.schemas.Define(def); len(diags) == 0 {
				sch.Version = st.version
			}
		}
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored schema " + st.name + " does not compile: " + diags.Error()}
		}
		s.schemaJSON[strings.ToUpper(st.name)] = st.json
	}
	if rows, err = s.db.QueryContext(ctx, "SELECT name, version, json FROM tables_registry ORDER BY rowid"); err != nil {
		return storageErr("read the tables", err)
	}
	var tables []stored
	for rows.Next() {
		var st stored
		if err := rows.Scan(&st.name, &st.version, &st.json); err != nil {
			rows.Close()
			return storageErr("read the tables", err)
		}
		tables = append(tables, st)
	}
	rows.Close()
	for _, st := range tables {
		def, diags := model.DefineTable(st.json)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored table " + st.name + " does not compile: " + diags.Error()}
		}
		def.Version = st.version
		t := newTable(def, st.json)
		if err := t.prepare(ctx, s.writer, s.db); err != nil {
			return err
		}
		s.registerTable(t)
	}
	// The app System and its tables (ADR-0092), before the classes,
	// which may read them.
	if err := s.keepSystem(ctx); err != nil {
		return err
	}
	if rows, err = s.db.QueryContext(ctx, "SELECT name, version, json FROM classes ORDER BY rowid"); err != nil {
		return storageErr("read the classes", err)
	}
	for rows.Next() {
		var st stored
		if err := rows.Scan(&st.name, &st.version, &st.json); err != nil {
			rows.Close()
			return storageErr("read the classes", err)
		}
		classes = append(classes, st)
	}
	rows.Close()
	// Every version of every class is compiled: a document is read under
	// the version that computed it (ADR-0039).
	if rows, err = s.db.QueryContext(ctx, "SELECT class, version, json FROM class_versions ORDER BY class, version"); err != nil {
		return storageErr("read the class versions", err)
	}
	var olds []stored
	for rows.Next() {
		var st stored
		if err := rows.Scan(&st.name, &st.version, &st.json); err != nil {
			rows.Close()
			return storageErr("read the class versions", err)
		}
		olds = append(olds, st)
	}
	rows.Close()
	// The action definitions, which the rules of the classes name
	// (ADR-0045): the engine's own http.call, then the stored ones.
	s.actions[strings.ToUpper(model.ActionHTTPCall)] = &action{spec: model.HTTPCall()}
	if err := s.loadActions(ctx); err != nil {
		return err
	}
	defer s.loadDefined(ctx)
	// A rule may name a class defined after its own (ADR-0044), and a
	// formula may read one (ADR-0046), so the classes compile in two
	// passes: every one alone, for its variables and their types, then
	// every one against all of them. A stored definition was checked when
	// it was defined, and loads leniently. Alone, a formula that reads
	// another class is left unbound: a variable computed from one declares
	// its type, so the variables are whole without it.
	env := s.env()
	env.Lenient = true
	env.Shallow = true
	alone := map[string]*model.Class{}
	env.Classes = nil
	for _, st := range classes {
		cls, diags := model.DefineIn(st.json, env)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored class " + st.name + " does not compile: " + diags.Error()}
		}
		alone[strings.ToUpper(cls.Name)] = cls
	}
	env.Classes = func(name string) (*model.Class, bool) {
		cls, ok := alone[strings.ToUpper(name)]
		return cls, ok
	}
	// The current version of a class binds every formula: a redefinition
	// of a class it reads was refused if it broke one. An earlier version
	// no longer computes anything, and may read what has since gone.
	env.Shallow = false
	for _, st := range classes {
		c, err := s.compileStored(ctx, st.name, st.version, st.json, env, false)
		if err != nil {
			return err
		}
		s.register(c)
	}
	env.Shallow = true
	for _, st := range olds {
		if cur, ok := s.classes[strings.ToUpper(st.name)]; !ok || cur.cls.Version == st.version {
			continue
		}
		if _, err := s.compileStored(ctx, st.name, st.version, st.json, env, true); err != nil {
			return err
		}
	}
	// The inlets, which name the classes (ADR-0065).
	if err := s.loadInlets(ctx); err != nil {
		return err
	}
	// The words, which translate the classes, the tables and the schemas
	// (ADR-0068, item 6).
	return s.loadWords(ctx)
}

// env is what definitions compile against: the schemas, the tables, the
// classes an action may name (ADR-0044), the action definitions a rule
// may name (ADR-0045) and the apps a class may belong to (ADR-0077). The
// caller holds cmu.
func (s *Store) env() model.Env {
	set := s.sourceSet
	if set == nil {
		set = formula.NewSourceSet()
	}
	return model.Env{Schemas: s.schemas, Sources: set, Engine: s.bringing, Classes: func(name string) (*model.Class, bool) {
		if c, ok := s.classes[strings.ToUpper(name)]; ok {
			return c.cls, true
		}
		return nil, false
	}, Actions: func(name string) (*model.ActionSpec, bool) {
		if a, ok := s.actions[strings.ToUpper(name)]; ok {
			return a.spec, true
		}
		return nil, false
	}, Apps: func(name string) bool {
		_, ok := s.apps[strings.ToUpper(name)]
		return ok
	}, Roles: func(app string) []string {
		if a, ok := s.apps[strings.ToUpper(app)]; ok {
			return a.Def.RoleNames()
		}
		return nil
	}}
}

// compileStored compiles one stored version of a class, prepares it and
// keeps it among the class's versions. An earlier version that the
// tables were made again without is not kept, and the answer is nil:
// nothing was ever written under it.
func (s *Store) compileStored(ctx context.Context, name string, version int, src []byte, env model.Env, earlier bool) (*class, error) {
	cls, diags := model.DefineIn(src, env)
	if len(diags) > 0 {
		return nil, &Error{Code: CodeStorage, Message: "the stored class " + name + " version " + itoa(version) + " does not compile: " + diags.Error()}
	}
	cls.Version = version
	c, err := newClass(cls, src)
	if err != nil {
		return nil, err
	}
	if earlier {
		kept, err := c.prepareEarlier(ctx, s.writer, s.db, s.db)
		if err != nil || !kept {
			return nil, err
		}
	} else if err := c.prepare(ctx, s.writer, s.db); err != nil {
		return nil, err
	}
	s.keepVersion(c)
	return c, nil
}

// register makes a class the current one of its name.
func (s *Store) register(c *class) {
	key := strings.ToUpper(c.cls.Name)
	if _, exists := s.classes[key]; !exists {
		s.order = append(s.order, c.cls.Name)
	}
	s.classes[key] = c
	s.keepVersion(c)
}

// keepVersion remembers a compiled version of a class.
func (s *Store) keepVersion(c *class) {
	key := strings.ToUpper(c.cls.Name)
	if s.versions[key] == nil {
		s.versions[key] = map[int]*class{}
	}
	s.versions[key][c.cls.Version] = c
	for _, r := range c.cls.Rules {
		for _, a := range r.Actions {
			if len(a.Into) > 0 {
				s.writesBack[c.cls.Name] = true
			}
		}
	}
}

// classAt returns a compiled version of a class, nil when the store does
// not have it.
func (s *Store) classAt(name string, version int) *class {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.versions[strings.ToUpper(name)][version]
}

// farPast is how many times its limit the log may grow to before the
// checkpointer takes the writer's turn to empty it, and fewPages how
// many pages of the log may be left to copy for it to be emptied at no
// cost: a megabyte of the usual pages.
const (
	farPast  = 8
	fewPages = 256
)

// checkpoint keeps the write-ahead log small until the store is closed
// (ADR-0035, item 7). Every period it copies what it can of the log into
// the database file, on a connection of its own and without waiting for
// anybody, so that no commit pays for it. A log can only be emptied
// when nobody writes to it and it was copied whole.
//
//   - Past its limit, the log is emptied as soon as that costs nothing:
//     when little is left to copy and no commit runs at that instant.
//     Callers that pause between commits leave such instants all the
//     time.
//   - A writer that never pauses leaves none, and its log grows. A
//     burst may: it is copied in the background and costs the writer
//     nothing.
//   - Once the log is farPast times its limit, the checkpointer takes
//     the writer's turn, between two commits, copies what is left and
//     empties the log. The writers wait for that, and it is kept short
//     by copying once more before the turn is taken.
func (s *Store) checkpoint(conn *sql.Conn, every time.Duration, limit int64) {
	defer close(s.done)
	defer conn.Close()
	t := time.NewTicker(every)
	defer t.Stop()
	ctx := context.Background()
	// The checkpointer never waits inside SQLite. In the writer's turn it
	// would hold every commit for as long as a reader keeps an old view
	// of the database, or another program writes; what it cannot do now
	// it does a period later.
	_, _ = conn.ExecContext(ctx, "PRAGMA busy_timeout(0)")
	// The two statements are prepared once: the checkpointer runs for as
	// long as the database is open, and what it allocates is counted
	// with the commits it runs beside.
	copyLog := prepared(ctx, conn, "PRAGMA wal_checkpoint(PASSIVE)")
	emptyLog := prepared(ctx, conn, "PRAGMA wal_checkpoint(TRUNCATE)")
	defer copyLog.close()
	defer emptyLog.close()
	log := s.path + "-wal"
	// One connection that reads is kept ready for good: asking after
	// the database takes the one that was used last and gives it back,
	// which starts its quarter of a minute again. The others go when
	// nobody has used them for that long.
	//
	// The asking is one reader more for the instant it takes. A read
	// that falls in that instant on a quiet database opens a connection
	// of its own, and beside eight callers reading at once a ninth
	// connection is opened and one is closed when the nine are back:
	// one opening in five seconds at the most.
	asked, often := time.Now(), readerIdle/3
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if now := time.Now(); now.Sub(asked) >= often {
				asked = now
				_ = s.db.PingContext(ctx)
			}
			fi, err := os.Stat(log)
			if err != nil || fi.Size() <= limit {
				copyLog.run(ctx)
				continue
			}
			// Past the limit: the log is copied, and how much is left of
			// it is asked.
			left := copyLog.left(ctx)
			switch {
			case fi.Size() > farPast*limit:
				// Far past it. What the writers wrote while the log was
				// copied is copied too, outside their turn, and what they
				// write during that is what the turn has to copy. Then
				// the writer's turn: the lock every commit of this
				// program takes, so that no commit runs meanwhile.
				copyLog.run(ctx)
				s.mu.Lock()
				if !s.closed && emptyLog.emptied(ctx) {
					s.turns.Add(1)
				}
				s.mu.Unlock()
			case left <= fewPages && s.mu.TryLock():
				// Little is left and no commit runs just now: emptying
				// the log costs nobody anything.
				if !s.closed {
					emptyLog.run(ctx)
				}
				s.mu.Unlock()
			}
		}
	}
}

// statement is a statement of the checkpointer on its connection,
// prepared when it can be.
type statement struct {
	conn *sql.Conn
	stmt *sql.Stmt
	text string
}

func prepared(ctx context.Context, conn *sql.Conn, text string) *statement {
	st := &statement{conn: conn, text: text}
	if stmt, err := conn.PrepareContext(ctx, text); err == nil {
		st.stmt = stmt
	}
	return st
}

// run runs the statement and leaves its outcome: a checkpoint that could
// not be made now is made a period later.
func (st *statement) run(ctx context.Context) {
	if st.stmt != nil {
		_, _ = st.stmt.ExecContext(ctx)
		return
	}
	_, _ = st.conn.ExecContext(ctx, st.text)
}

// left runs a statement that copies the log, and answers how many pages
// of the log are left to copy; all of them, as far as the caller is to
// know, when the statement could not be run.
func (st *statement) left(ctx context.Context) int {
	var busy, pages, copied int
	if err := st.conn.QueryRowContext(ctx, st.text).Scan(&busy, &pages, &copied); err != nil {
		return fewPages + 1
	}
	return pages - copied
}

// emptied runs a statement that empties the log, and answers whether it
// did: SQLite says busy when somebody kept it from it.
func (st *statement) emptied(ctx context.Context) bool {
	var busy, pages, copied int
	var row *sql.Row
	if st.stmt != nil {
		row = st.stmt.QueryRowContext(ctx)
	} else {
		row = st.conn.QueryRowContext(ctx, st.text)
	}
	return row.Scan(&busy, &pages, &copied) == nil && busy == 0
}

func (st *statement) close() {
	if st.stmt != nil {
		st.stmt.Close()
	}
}

// Close stops the checkpointer, closes every statement before its
// connection, and closes the database.
func (s *Store) Close() error {
	// The worker first, and outside the writer's lock, which an action in
	// flight holds: Close waits for it (ADR-0044).
	s.workStop.Do(func() {
		if s.workCancel != nil {
			s.workCancel()
		}
	})
	<-s.workDone
	// The copy is brought up to the database a last time, while the
	// database is open, and closed.
	s.stopCopy()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	// The checkpointer may be waiting for the writer's turn: it gets it
	// here, sees the store closed and ends.
	s.mu.Unlock()
	close(s.stop)
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmu.Lock()
	for _, vs := range s.versions {
		for _, c := range vs {
			c.close()
			query.Forget(c.cls)
		}
	}
	for _, t := range s.tables {
		t.close()
	}
	s.cmu.Unlock()
	s.closePlans()
	s.filters.clear()
	s.stmts.close()
	s.sqlMu.Lock()
	if s.sqlConn != nil {
		s.sqlConn.Close()
		s.sqlConn = nil
	}
	s.sqlMu.Unlock()
	err := s.writer.Close()
	if err2 := s.db.Close(); err == nil {
		err = err2
	}
	s.held.release()
	return storageErr("close", err)
}

// Ping reads from the database, to say that it answers. A server asks
// it for its health (ADR-0059, item 9).
func (s *Store) Ping(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version); err != nil {
		return storageErr("read the database", err)
	}
	return nil
}

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Limits are the database's size limits (ADR-0021).
func (s *Store) Limits() formula.Limits { return s.limits }

// Synchronous is the database's synchronous setting, NORMAL or FULL.
func (s *Store) Synchronous() string { return s.synchronous }

// MaxIndexedRows is the row count up to which a table is held in memory
// for lookups (ADR-0036).
func (s *Store) MaxIndexedRows() int { return s.maxIndexed }

// MaxQuestion is how long a free question that no index serves may run
// before it is stopped (ADR-0075, item 6).
func (s *Store) MaxQuestion() time.Duration { return s.maxQuestion }

// Classes lists the class names in the order they were defined.
func (s *Store) Classes() []string {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return append([]string(nil), s.order...)
}

// Class returns the compiled class of that name.
func (s *Store) Class(name string) (*model.Class, bool) {
	c, err := s.class(name)
	if err != nil {
		return nil, false
	}
	return c.cls, true
}

// Schemas are the database's schemas, the vocabulary included.
func (s *Store) Schemas() *model.SchemaSet {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.schemas
}

func (s *Store) class(name string) (*class, *Error) {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	c, ok := s.classes[strings.ToUpper(name)]
	if !ok {
		return nil, errf(CodeUnknownClass, "", "unknown class %q", name)
	}
	return c, nil
}
