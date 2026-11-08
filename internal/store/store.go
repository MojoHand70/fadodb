package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the pure-Go driver, the core's one dependency (ADR-0002, ADR-0035)

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
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
// by a newer engine is refused.
const engineSchema = 1

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
	// Now is the clock the store stamps commits with; nil is time.Now.
	// Tests set it.
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
	vars        []formula.Value
	dirty       []bool
	ids         idGen
	stmts       engineStmts
	plans       map[*formula.QuerySlot]*lookupPlan

	cmu        sync.RWMutex
	schemas    *model.SchemaSet
	schemaJSON map[string][]byte
	classes    map[string]*class
	order      []string
	tables     map[string]*table
	tableOrder []string

	// sqlConn is the read-only SQL endpoint's connection, with its
	// authorizer, made on first use; sqlMu serialises its statements.
	sqlMu   sync.Mutex
	sqlConn *sql.Conn

	stop   chan struct{}
	done   chan struct{}
	closed bool
}

type engineStmts struct {
	lookupKey, insertEvent, insertEventDoc, listEvents *sql.Stmt
}

func (e *engineStmts) close() {
	for _, st := range []*sql.Stmt{e.lookupKey, e.insertEvent, e.insertEventDoc, e.listEvents} {
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
		schemas: model.NewSchemaSet(), schemaJSON: map[string][]byte{}, classes: map[string]*class{},
		tables: map[string]*table{}, plans: map[*formula.QuerySlot]*lookupPlan{},
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	if s.now == nil {
		s.now = time.Now
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
	prep(&s.stmts.lookupKey, "SELECT kind, class, doc_id, after_version, command_hash FROM events WHERE command_key = ?")
	prep(&s.stmts.insertEvent, "INSERT INTO events (event_id, class, doc_id, kind, name, from_state, to_state, actor_id, before_version, after_version, cause_event_id, command_key, command_hash, business_date, ts)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	prep(&s.stmts.insertEventDoc, "INSERT INTO event_docs (event_id, class, doc_id, qualifier) VALUES (?, ?, ?, ?)")
	if err != nil {
		return storageErr("prepare", err)
	}
	s.stmts.listEvents, err = s.db.PrepareContext(ctx, "SELECT e.event_id, e.class, e.doc_id, e.kind, e.name, e.from_state, e.to_state, e.actor_id, e.before_version, e.after_version, e.cause_event_id, e.command_key, e.business_date, e.ts"+
		" FROM events e JOIN event_docs d ON d.event_id = e.event_id WHERE d.class = ? AND d.doc_id = ? ORDER BY e.ts, e.event_id")
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
	for _, st := range classes {
		cls, diags := model.DefineIn(st.json, s.env())
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored class " + st.name + " does not compile: " + diags.Error()}
		}
		cls.Version = st.version
		c, err := newClass(cls, st.json)
		if err != nil {
			return err
		}
		if err := c.prepare(ctx, s.writer, s.db); err != nil {
			return err
		}
		s.register(c)
	}
	return nil
}

// env is what definitions compile against: the schemas and the tables.
func (s *Store) env() model.Env {
	set := formula.NewSourceSet()
	for _, name := range s.tableOrder {
		set.Add(s.tables[strings.ToUpper(name)].src)
	}
	return model.Env{Schemas: s.schemas, Sources: set}
}

func (s *Store) register(c *class) {
	key := strings.ToUpper(c.cls.Name)
	if _, exists := s.classes[key]; !exists {
		s.order = append(s.order, c.cls.Name)
	}
	s.classes[key] = c
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.stop)
	<-s.done
	s.cmu.Lock()
	for _, c := range s.classes {
		c.close()
	}
	for _, t := range s.tables {
		t.close()
	}
	s.cmu.Unlock()
	s.closePlans()
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
