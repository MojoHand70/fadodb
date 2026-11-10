package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the pure-Go driver, the core's one dependency (ADR-0002, ADR-0035)

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/formula/sqlitefn"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
)

// DefaultWALLimit is the write-ahead log size past which the checkpointer
// truncates the log (ADR-0035).
const DefaultWALLimit int64 = 64 << 20

// DefaultCheckpointEvery is how often the checkpointer runs a passive
// checkpoint.
const DefaultCheckpointEvery = 100 * time.Millisecond

// DefaultMaxIndexedRows is the row count up to which a table is held in
// memory for the lookups of auto formulas (ADR-0036).
const DefaultMaxIndexedRows = 100000

// engineSchema is the version of the store's own tables. A database made
// by another engine schema is refused: before 1.0 there is no upgrade
// path, and a pre-release file is recreated.
const engineSchema = 3

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
	// WALLimit is the log size that triggers a truncating checkpoint; 0
	// is DefaultWALLimit.
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
	maxBlob     int64
	blobKind    string
	blobs       BlobStore
	now         func() time.Time
	vm          *formula.VM
	qvm         *formula.VM // runs the key expressions of indexed lookups while vm runs the formula
	wev         evaluator   // the writer's evaluator over vm, with its scratch
	readers     sync.Pool   // evaluators for the read paths' projections (ADR-0041)
	dirty       []bool
	given       []bool   // the variables one command sets, for the write check
	visits      []string // the states one command visits, reused under the writer's lock
	// The scratch of the rules of one command (ADR-0043), reused under the
	// writer's lock: the eventless commits, the custom events to append,
	// the values of a set before they are assigned, the composites a reset
	// emptied, and the variables and the composites that changed.
	hops        []hop
	emits       []emitted
	queued      []queued // the actions for the outbox one command asked for
	lists       []list   // the composites the creates with each of one command read
	setVals     []formula.Value
	emptied     []*child
	changed     []bool
	changedKids []*child
	ids         idGen
	stmts       engineStmts
	plans       map[*formula.QuerySlot]*lookupPlan
	filters     filterStmts // the prepared statements of Query and Count, by text

	cmu        sync.RWMutex
	schemas    *model.SchemaSet
	schemaJSON map[string][]byte
	classes    map[string]*class
	versions   map[string]map[int]*class // every compiled version of a class, by upper-cased name
	order      []string
	tables     map[string]*table
	tableOrder []string
	// actions are the action definitions by upper-cased name (ADR-0045),
	// and funcs the functions Open was given, which never change.
	actions     map[string]*action
	actionOrder []string
	funcs       map[string]ActionFunc

	// sqlConn is the read-only SQL endpoint's connection, with its
	// authorizer, made on first use; sqlMu serialises its statements.
	sqlMu   sync.Mutex
	sqlConn *sql.Conn

	stop   chan struct{}
	done   chan struct{}
	closed bool

	// The outbox (ADR-0044): its options with the defaults filled in, the
	// lock that makes one drain by hand run at a time, the documents that
	// have an action in flight, and the worker's wake-up, stop and end.
	outbox     OutboxOptions
	drainMu    sync.Mutex
	flightMu   sync.Mutex
	inFlight   map[flight]struct{}
	wake       chan struct{}
	workCancel context.CancelFunc
	workDone   chan struct{}
	workStop   sync.Once
	// testStep is called at the steps of an action when a test sets it.
	testStep func(step string, row *outboxRow) *Error
}

type engineStmts struct {
	lookupKey, lastOfCommand, insertEvent, insertEventDoc, listEvents *sql.Stmt
	insertOutbox, dueOutbox, dueEngine, dueCalls, callOutbox          *sql.Stmt
}

func (e *engineStmts) close() {
	for _, st := range []*sql.Stmt{e.lookupKey, e.lastOfCommand, e.insertEvent, e.insertEventDoc, e.listEvents, e.insertOutbox, e.dueOutbox, e.dueEngine, e.dueCalls, e.callOutbox} {
		if st != nil {
			st.Close()
		}
	}
}

// Open opens the database at path, creating it when it does not exist,
// and loads its schemas and classes.
func Open(path string, o Options) (*Store, error) {
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
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(OFF)")
	q.Add("_pragma", "wal_autocheckpoint(0)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, storageErr("open", err)
	}
	s := &Store{
		path: path, db: db, now: o.Now, vm: formula.NewVM(), qvm: formula.NewVM(),
		schemas: model.NewSchemaSet(), schemaJSON: map[string][]byte{}, classes: map[string]*class{}, versions: map[string]map[int]*class{},
		tables: map[string]*table{}, plans: map[*formula.QuerySlot]*lookupPlan{},
		actions: map[string]*action{}, funcs: funcs, inFlight: map[flight]struct{}{},
		stop: make(chan struct{}), done: make(chan struct{}),
		outbox: o.Outbox, wake: make(chan struct{}, 1), workDone: make(chan struct{}),
	}
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
		s.stmts.close()
		s.writer.Close()
		db.Close()
		return nil, err
	}
	if err := s.init(ctx, sync, o.MaxText, o.MaxIndexedRows, o.BlobStore, o.MaxBlob); err != nil {
		return fail(err)
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
	if err := s.prepareEngine(ctx); err != nil {
		return fail(err)
	}
	if err := s.load(ctx); err != nil {
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
	return s, nil
}

// init creates the engine's tables when the database is new and reads
// the metadata.
func (s *Store) init(ctx context.Context, wantSync string, wantMaxText int, wantIndexed int, wantBlobs string, wantMaxBlob int64) error {
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	rollback := func(err error) error {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return err
	}
	for _, ddl := range engineDDL {
		if _, err := w.ExecContext(ctx, ddl); err != nil {
			return rollback(storageErr("create the engine tables", err))
		}
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
	case version < engineSchema:
		return rollback(hintf(CodeNotSupported, "", fmt.Sprintf("the database was created by an earlier pre-release engine (schema %d, this one reads %d)", version, engineSchema),
			"there is no upgrade path before 1.0; recreate the file"))
	}
	if wantIndexed > 0 {
		if _, err := w.ExecContext(ctx, "INSERT INTO metadata (key, value) VALUES ('max_indexed_rows', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", strconv.Itoa(wantIndexed)); err != nil {
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
	if _, err := w.ExecContext(ctx, "COMMIT"); err != nil {
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
	if s.maxBlob, _ = strconv.ParseInt(meta["max_blob"], 10, 64); s.maxBlob <= 0 {
		s.maxBlob = DefaultMaxBlob
	}
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
	prep(&s.stmts.lastOfCommand, "SELECT max(after_version) FROM events WHERE cause_event_id = ?")
	prep(&s.stmts.insertEvent, "INSERT INTO events (event_id, class, doc_id, kind, name, from_state, to_state, actor_id, before_version, after_version, cause_event_id, command_key, command_hash, business_date, ts)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	prep(&s.stmts.insertEventDoc, "INSERT INTO event_docs (event_id, class, doc_id, qualifier) VALUES (?, ?, ?, ?)")
	prep(&s.stmts.insertOutbox, "INSERT INTO outbox (event_id, idx, class, doc_id, version, class_version, rule, action, args, principal_id, principal_groups, principal_admin, depth, status, attempts, due_at, error_code, error_message, created_at, finished_at, outcome_event)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return storageErr("prepare", err)
	}
	// An event of kind action_failed is read with the code its action died
	// with, which the outbox row holds (ADR-0044).
	if s.stmts.listEvents, err = s.db.PrepareContext(ctx, "SELECT e.event_id, e.class, e.doc_id, e.kind, e.name, e.from_state, e.to_state, e.actor_id, e.before_version, e.after_version, e.cause_event_id, e.command_key, e.business_date, e.ts,"+
		" (SELECT o.error_code FROM outbox o WHERE o.outcome_event = e.event_id)"+
		" FROM events e JOIN event_docs d ON d.event_id = e.event_id WHERE d.class = ? AND d.doc_id = ? ORDER BY e.ts, e.event_id"); err != nil {
		return storageErr("prepare", err)
	}
	// The next actions that may run: pending, due, and not behind an
	// earlier pending action of the same document, so at most one per
	// document; the index on (status, due_at, event_id, idx) gives them in
	// order without a sort. A drain by hand takes them all; the worker
	// takes the engine's own actions and the ones that leave the database
	// apart, so that the second, which may take long, never stand in the
	// way of the first.
	engine := "'" + strings.Join([]string{model.ActionCreate, model.ActionTransition, model.ActionRefresh, model.ActionTableInsert, model.ActionTableUpdate, model.ActionTableDelete}, "', '") + "'"
	for _, due := range []struct {
		dst  **sql.Stmt
		only string
	}{{&s.stmts.dueOutbox, ""}, {&s.stmts.dueEngine, " AND o.action IN (" + engine + ")"}, {&s.stmts.dueCalls, " AND o.action NOT IN (" + engine + ")"}} {
		*due.dst, err = s.db.PrepareContext(ctx, "SELECT o.event_id, o.idx, o.class, o.doc_id, o.action, o.args, o.principal_id, o.principal_groups, o.principal_admin, o.depth FROM outbox o"+
			" WHERE o.status = 'pending' AND o.due_at <= ?"+due.only+
			" AND NOT EXISTS (SELECT 1 FROM outbox p WHERE p.class = o.class AND p.doc_id = o.doc_id AND p.status = 'pending' AND (p.event_id < o.event_id OR (p.event_id = o.event_id AND p.idx < o.idx)))"+
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
		" coalesce(e.command_key, (SELECT c.command_key FROM events c WHERE c.event_id = e.cause_event_id)), e.business_date, e.ts"+
		" FROM outbox o JOIN events e ON e.event_id = o.event_id WHERE o.event_id = ? AND o.idx = ?")
	return storageErr("prepare", err)
}

// load reads the stored schemas and classes, in the order they were
// defined, and compiles them.
func (s *Store) load(ctx context.Context) error {
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
	// (ADR-0045).
	if err := s.loadActions(ctx); err != nil {
		return err
	}
	// A rule may name a class defined after its own (ADR-0044), so the
	// classes compile in two passes: every one alone, for its variables
	// and their types, then every one against all of them. A stored
	// definition was checked when it was defined, and loads leniently.
	env := s.env()
	env.Lenient = true
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
	for _, st := range classes {
		c, err := s.compileStored(ctx, st.name, st.version, st.json, env)
		if err != nil {
			return err
		}
		s.register(c)
	}
	for _, st := range olds {
		if cur, ok := s.classes[strings.ToUpper(st.name)]; !ok || cur.cls.Version == st.version {
			continue
		}
		if _, err := s.compileStored(ctx, st.name, st.version, st.json, env); err != nil {
			return err
		}
	}
	return nil
}

// env is what definitions compile against: the schemas, the tables, the
// classes an action may name (ADR-0044) and the action definitions a rule
// may name (ADR-0045). The caller holds cmu.
func (s *Store) env() model.Env {
	set := formula.NewSourceSet()
	for _, name := range s.tableOrder {
		set.Add(s.tables[strings.ToUpper(name)].src)
	}
	return model.Env{Schemas: s.schemas, Sources: set, Classes: func(name string) (*model.Class, bool) {
		if c, ok := s.classes[strings.ToUpper(name)]; ok {
			return c.cls, true
		}
		return nil, false
	}, Actions: func(name string) (*model.ActionSpec, bool) {
		if a, ok := s.actions[strings.ToUpper(name)]; ok {
			return a.spec, true
		}
		return nil, false
	}}
}

// compileStored compiles one stored version of a class, prepares it and
// keeps it among the class's versions.
func (s *Store) compileStored(ctx context.Context, name string, version int, src []byte, env model.Env) (*class, error) {
	cls, diags := model.DefineIn(src, env)
	if len(diags) > 0 {
		return nil, &Error{Code: CodeStorage, Message: "the stored class " + name + " version " + itoa(version) + " does not compile: " + diags.Error()}
	}
	cls.Version = version
	c, err := newClass(cls, src)
	if err != nil {
		return nil, err
	}
	if err := c.prepare(ctx, s.writer, s.db); err != nil {
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
}

// classAt returns a compiled version of a class, nil when the store does
// not have it.
func (s *Store) classAt(name string, version int) *class {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.versions[strings.ToUpper(name)][version]
}

// checkpoint copies the write-ahead log into the database file every
// period without waiting for the writer, and truncates the log once it
// passes limit bytes, until the store is closed (ADR-0035).
func (s *Store) checkpoint(conn *sql.Conn, every time.Duration, limit int64) {
	defer close(s.done)
	defer conn.Close()
	t := time.NewTicker(every)
	defer t.Stop()
	ctx := context.Background()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			mode := "PASSIVE"
			if fi, err := os.Stat(s.path + "-wal"); err == nil && fi.Size() > limit {
				mode = "TRUNCATE"
			}
			_, _ = conn.ExecContext(ctx, "PRAGMA wal_checkpoint("+mode+")")
		}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.stop)
	<-s.done
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
	return storageErr("close", err)
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
