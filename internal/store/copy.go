package store

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Analytics is the analytical engine a program gives the database it
// opens (ADR-0017, ADR-0089). The core has none of its own: it is pure
// Go, and the engine that answers a question that reads everything is
// DuckDB, which the module analytics brings. A program built without
// one answers every question by the store.
type Analytics interface {
	// OpenCopy opens the copy that stands beside the database of the
	// path, and makes an empty one when none is there or the one there
	// does not open. The copy is never the truth: the store feeds an
	// empty one from the beginning.
	OpenCopy(ctx context.Context, database string) (Copy, error)
}

// Copy is the copy of the documents an analytical engine holds
// (ADR-0089, items 1 to 4). The store that opened it is the only one
// that writes to it.
type Copy interface {
	// Marks answers what the copy keeps of how far it was fed, by name;
	// none for a copy that was never fed.
	Marks(ctx context.Context) (map[string]string, error)
	// Lay makes each table the copy lacks, adds each column a table
	// lacks, and makes again a column whose type changed: the engine
	// lets a variable change its type only while no document holds a
	// value of it.
	Lay(ctx context.Context, tables []CopyTable) error
	// Feed runs give, and keeps what give handed over together with the
	// marks, in one transaction: all of it or nothing. A mark with an
	// empty value is taken away. The marks are read after give has
	// returned, so give may fill them.
	Feed(ctx context.Context, give func(CopyWriter) error, marks map[string]string) error
	// Ask runs one statement in the engine's own dialect and answers
	// its columns and its rows.
	Ask(ctx context.Context, statement string, args ...any) ([]string, [][]any, error)
	// Close ends the use of the copy.
	Close() error
}

// CopyTable is one table of the copy, and CopyColumn one column of it.
type CopyTable struct {
	Name    string
	Columns []CopyColumn
}

// CopyColumn is a column by its name and what it holds: CopyText,
// CopyWhole, CopyNumber, CopyBool or CopyDay.
type CopyColumn struct {
	Name string
	Type string
}

// What a column of the copy holds: a text; a whole number, which a
// moment is as its seconds and an exact value as its mantissa (ADR-0022,
// ADR-0023); a number with a fraction; a true or a false; and a day,
// the whole number of the days of a date. They are the columns the
// formula compiler writes its statements for in the engine's dialect
// (ADR-0016).
const (
	CopyText   = "text"
	CopyWhole  = "whole"
	CopyNumber = "number"
	CopyBool   = "bool"
	CopyDay    = "day"
)

// CopyWriter is what a feed hands rows to.
type CopyWriter interface {
	// Append adds a row to a table: a value for each of its columns, in
	// the order Lay was given them. A value is nil, or a string for a
	// text, an int64 for a whole number and for a day, a float64 for a
	// number, a bool for a true or a false. The row is the writer's
	// only until Append returns.
	Append(table string, row []any) error
	// Remove takes out of a table the rows whose "id" is one of ids.
	Remove(table string, ids []string) error
	// Empty takes every row out of a table.
	Empty(table string) error
}

// The names of the copy's tables and marks. A class has two tables: the
// documents as they are now, under the name of the class, and every
// version of them. A mark is the last row the copy holds of a table of
// the store: the place of the row, a space, and the id of its event.
// The place says where the next feed begins; the id tells a database
// that was put back from a backup, or a class that was made again, from
// the one the copy was fed. A class that had no version when it was
// last fed has the place 0 and copyNoVersion, and the mark of a class in
// the middle of a long load ends in copyPartial.
const (
	copyEvents    = "events"
	copyNoVersion = "-"
	copyPartial   = "+"
	copyVersions  = "_versions"
	copyMoves     = "_moves"
	copyMarkEvent = "event"
	copyMarkClass = "class:"
	// copyMarkFormat marks what a copy holds beside the rows of the
	// store: a copy fed before it kept the moves of the documents has
	// another mark, or none, and is given them.
	copyMarkFormat = "format"
	copyFormat     = "moves"
	copyEventsSQL  = `"event_id", "class", "doc_id", "kind", "name", "from_state", "to_state", "actor_id", "actor_kind", "actor_for", "before_version", "after_version", "cause_event_id", "command_key", "command_hash", "business_date", "ts", "origin", "override", "reason", "seen_version"`
)

// copyEventColumns are the columns of the events in the copy, in the
// order copyEventsSQL reads them.
var copyEventColumns = func() []CopyColumn {
	whole := map[string]bool{"before_version": true, "after_version": true, "business_date": true, "ts": true, "origin": true, "seen_version": true}
	var cols []CopyColumn
	for _, name := range strings.Split(strings.ReplaceAll(copyEventsSQL, `"`, ""), ", ") {
		t := CopyText
		if whole[name] {
			t = CopyWhole
		}
		cols = append(cols, CopyColumn{Name: name, Type: t})
	}
	return cols
}()

// copyMoveColumns are the columns of the moves of a class in the copy
// (ADR-0089, item 2): one row for each time a document that exists was
// made or moved along a transition, with the moment it happened and,
// for a move, the moment the state it left was entered. The question of
// how documents moved is one sum over these rows, and reads neither the
// events nor the versions.
var copyMoveColumns = []CopyColumn{{"id", CopyText}, {"kind", CopyText}, {"name", CopyText}, {"from_state", CopyText}, {"to_state", CopyText}, {"ts", CopyWhole}, {"left_since", CopyWhole}}

// The places of what a move takes among the columns copyEventsSQL
// reads, and leftSinceSQL is what the feed reads after them: for a
// transition, the moment the state it left was entered, which the
// version the event left holds. The store finds that version by its
// key, inside the one statement that reads the events.
const (
	eventClass, eventDoc, eventKind, eventName, eventFrom, eventTo, eventTS = 1, 2, 3, 4, 5, 6, 16
)

func leftSinceSQL(layouts []*copyLayout) string {
	if len(layouts) == 0 {
		return "NULL"
	}
	var b strings.Builder
	b.WriteString(`CASE WHEN "kind" = '` + EventTransition + `' THEN CASE "class"`)
	for _, l := range layouts {
		b.WriteString(` WHEN '` + strings.ReplaceAll(l.name, "'", "''") + `' THEN (SELECT p."state_entered_at" FROM ` + quote(l.history) +
			` p WHERE p."id" = "events"."doc_id" AND p."version" = "events"."before_version")`)
	}
	b.WriteString(` END END`)
	return b.String()
}

// Defaults of the feed. A class whose feed holds more new versions than
// copyRemakeNow has its documents as they are now read whole, which is
// cheaper than taking each changed one out and putting it back; at its
// closing a store feeds the copy for at most copyCloseFeed.
const (
	DefaultCopyEvery = 5 * time.Second
	copyCloseFeed    = 5 * time.Second
	copyChunk        = 500
)

// copyRemakeNow and copySlice are variables so that a test reaches them
// with few documents. A feed hands over at most copySlice versions of a
// class and as many events, and is followed at once by another when
// more are left: a long load is then copied in pieces, each kept when
// it is done, and no reading of the database is held for the whole of
// it, which would keep the store from folding its latest commits into
// the database meanwhile.
var (
	copyRemakeNow int64 = 100000
	copySlice     int64 = 1000000
)

// feeder keeps the copy of an open store (ADR-0089): the copy, how far
// it was fed, and what was laid in it for each class.
type feeder struct {
	copy Copy
	// mu lets one piece of a feed run at a time.
	mu sync.Mutex
	// marks are the marks as the copy holds them: read when the store
	// opened, and kept with every feed.
	marks map[string]string
	// laid is the version of each class whose tables the copy has, and
	// laidTables the version of each table of the database whose table
	// the copy has, by its name in the copy (ADR-0094).
	laid, laidTables map[string]int
	// tables is what the last feed found of each table of the database,
	// by its name in upper case: a feed puts a new map there, and a
	// question reads the one it finds. tableAt and tableTook say when a
	// table was last copied and how long that took.
	tables    atomic.Pointer[map[string]copiedTable]
	tableAt   map[string]time.Time
	tableTook map[string]time.Duration
	// wake asks the feed that runs by itself to run now: a question
	// found a large table changed, and does not copy it itself.
	wake chan struct{}
	// every is the period of the feed that runs by itself.
	every   time.Duration
	cancel  context.CancelFunc
	stopped sync.Once
	// first is closed when the feed that runs at the opening has ended,
	// stop asks the feed that runs by itself to end, and done says it
	// has.
	first, stop, done chan struct{}
	// stands says where the copy stands to the database: near, a
	// moment's writing behind at most, so that a question that brings
	// it up to date, or waits for a feed that is doing so, waits for
	// little; behind, being made or a long load behind, when the store
	// answers and no question waits (ADR-0089, item 5); or not known,
	// before the first feed has looked and after one that failed, when
	// a question looks for itself. A feed says which before it copies
	// anything, and again when it ends.
	stands       atomic.Int32
	fed, failed  int64
	lastErr      error
	answers      int64
	askErr       error
	lastErrMutex sync.Mutex
}

// copyColumnType is what a column of a class holds in the copy.
func copyColumnType(col model.Column) string {
	switch col.Type.Kind {
	case formula.KindBool:
		return CopyBool
	case formula.KindDate:
		return CopyDay
	case formula.KindNumber:
		return CopyNumber
	case formula.KindDecimal, formula.KindMoney, formula.KindDateTime:
		return CopyWhole
	}
	return CopyText
}

// copyLayout is one class as the copy holds it: its two tables, and the
// statements that read their rows out of the store.
type copyLayout struct {
	name           string
	version        int
	history, table string
	now, versions  CopyTable
	// moves is the table of the moves of the class's documents.
	moves CopyTable
	// nowSQL and versionsSQL are the column lists of the two reads; the
	// place of the event among the columns of a version is eventAt.
	nowSQL, versionsSQL string
	eventAt             int
	// nowBools and versionsBools are the places of the columns that
	// hold a true or a false: the store keeps one as 1 or 0, and the
	// copy is handed a bool.
	nowBools, versionsBools []int
}

// bools turns the values at the places given from the store's 1 and 0
// into true and false; a blank stays blank.
func bools(row []any, at []int) {
	for _, i := range at {
		if v, ok := row[i].(int64); ok {
			row[i] = v != 0
		}
	}
}

func layoutOf(c *class) *copyLayout {
	l := &copyLayout{name: c.cls.Name, version: c.cls.Version, history: c.history, table: c.table}
	system := []CopyColumn{{"id", CopyText}, {"title", CopyText}, {"state", CopyText}, {"version", CopyWhole}, {"class_version", CopyWhole},
		{"created_at", CopyWhole}, {"updated_at", CopyWhole}, {"state_entered_at", CopyWhole},
		{"created_by", CopyText}, {"updated_by", CopyText}, {"state_entered_by", CopyText}, {"state_entered_from", CopyText}}
	// A version adds when it began, who made it and the event that
	// wrote it. When it ended is not copied: the store writes that into
	// the row later, and the copy only ever takes rows; the next
	// version of the document says it.
	versioned := append(append([]CopyColumn{}, system...), CopyColumn{"valid_from", CopyWhole}, CopyColumn{"actor_id", CopyText}, CopyColumn{"event_id", CopyText})
	l.eventAt = len(versioned) - 1
	var vars []CopyColumn
	for _, col := range c.cls.Columns {
		vars = append(vars, CopyColumn{Name: col.Name, Type: copyColumnType(col)})
	}
	l.now = CopyTable{Name: l.name, Columns: append(append([]CopyColumn{}, system...), vars...)}
	l.versions = CopyTable{Name: l.name + copyVersions, Columns: append(versioned, vars...)}
	l.moves = CopyTable{Name: l.name + copyMoves, Columns: copyMoveColumns}
	names := func(cols []CopyColumn) string {
		quoted := make([]string, len(cols))
		for i, col := range cols {
			quoted[i] = quote(col.Name)
		}
		return strings.Join(quoted, ", ")
	}
	l.nowSQL, l.versionsSQL = names(l.now.Columns), names(l.versions.Columns)
	for i, col := range l.now.Columns {
		if col.Type == CopyBool {
			l.nowBools = append(l.nowBools, i)
		}
	}
	for i, col := range l.versions.Columns {
		if col.Type == CopyBool {
			l.versionsBools = append(l.versionsBools, i)
		}
	}
	return l
}

// startCopy opens the copy of the store with the engine the program
// gave, and starts the feed that runs by itself. A copy that cannot be
// opened does not stop the database: the copy is never the truth, and
// every question is then answered by the store.
func (s *Store) startCopy(ctx context.Context, engine Analytics, every time.Duration) {
	if engine == nil {
		return
	}
	c, err := engine.OpenCopy(ctx, s.path)
	if err != nil {
		s.copyErr = err
		return
	}
	marks, err := c.Marks(ctx)
	if err != nil {
		s.copyErr = err
		_ = c.Close()
		return
	}
	if marks == nil {
		marks = map[string]string{}
	}
	if every <= 0 {
		every = DefaultCopyEvery
	}
	f := &feeder{copy: c, marks: marks, laid: map[string]int{}, laidTables: map[string]int{}, tableAt: map[string]time.Time{}, tableTook: map[string]time.Duration{},
		wake: make(chan struct{}, 1), every: every, first: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	var fctx context.Context
	fctx, f.cancel = context.WithCancel(context.Background())
	s.feeder = f
	go func() {
		defer close(f.done)
		// At once: the copy is laid, checked against the database and
		// brought up to it.
		_ = s.feedAs(fctx, 0, feedSelf)
		close(f.first)
		t := time.NewTicker(f.every)
		defer t.Stop()
		for {
			select {
			case <-f.stop:
				return
			case <-t.C:
				_ = s.feedAs(fctx, 0, feedSelf)
			case <-f.wake:
				_ = s.feedAs(fctx, 0, feedSelf)
			}
		}
	}()
}

// stopCopy ends the feed that runs by itself, brings the copy up to the
// database once more, for no longer than copyCloseFeed, and closes it.
// It runs when the store closes, while the database is still open.
func (s *Store) stopCopy() {
	f := s.feeder
	if f == nil {
		return
	}
	f.stopped.Do(func() {
		close(f.stop)
		f.cancel()
		<-f.done
		ctx, cancel := context.WithTimeout(context.Background(), copyCloseFeed)
		_ = s.feedAs(ctx, 0, feedClosing)
		cancel()
		_ = f.copy.Close()
	})
}

// CopyState says how the copy of an open database stands: whether the
// program gave an analytical engine and its copy is kept; how many
// feeds wrote to the copy and why the last one failed, nil when it did
// not; and how many questions the copy answered and why it last did
// not answer one, which the store then answered.
type CopyState struct {
	Kept    bool
	Feeds   int64
	Err     error
	Answers int64
	AskErr  error
}

// CopyState answers how the copy of the database stands.
func (s *Store) CopyState() CopyState {
	f := s.feeder
	if f == nil {
		return CopyState{Err: s.copyErr}
	}
	f.lastErrMutex.Lock()
	defer f.lastErrMutex.Unlock()
	return CopyState{Kept: true, Feeds: f.fed, Err: f.lastErr, Answers: f.answers, AskErr: f.askErr}
}

// answered counts a question the copy answered, and notAnswered keeps
// why it did not answer one.
func (f *feeder) answered() {
	f.lastErrMutex.Lock()
	f.answers++
	f.askErr = nil
	f.lastErrMutex.Unlock()
}

func (f *feeder) notAnswered(err error) {
	f.lastErrMutex.Lock()
	f.askErr = err
	f.lastErrMutex.Unlock()
}

// FeedCopy brings the copy up to the database now, without waiting for
// the feed that runs by itself. It answers nil for a database that keeps
// no copy.
func (s *Store) FeedCopy(ctx context.Context) error { return s.feed(ctx) }

// feed brings the copy up to the database (ADR-0089, item 4): one read
// of the database, which sees one moment of it, and one transaction of
// the copy, which takes the rows and the new marks together or nothing.
// A feed that finds nothing new writes nothing.
func (s *Store) feed(ctx context.Context) error { return s.feedAs(ctx, 0, feedAsked) }

// copyCatchUp is how many events a question brings the copy up by
// itself before it is answered. A copy that is further behind, after a
// long load or while it is being made, is brought up by the feed that
// runs by itself, and the store answers meanwhile: a question waits for
// a moment's writing and never for a load.
var copyCatchUp = int64(10000)

// feedAtMost is feed for a question: with most set, a copy that is
// more than most events behind is left as it is, and errCopyBehind
// says so. One piece is fed at a time, and a question that finds a feed
// running waits for it only while the copy is near the database: a
// feed that has much to bring says at once that it is not.
func (s *Store) feedAtMost(ctx context.Context, most int64) error {
	return s.feedAs(ctx, most, feedAsked)
}

// feedAs is the feed of whoever runs it: kind says what it does with a
// large table that changed (ADR-0094, item 4).
func (s *Store) feedAs(ctx context.Context, most int64, kind feedKind) error {
	f := s.feeder
	if f == nil {
		return nil
	}
	for {
		if most == 0 {
			f.mu.Lock()
		} else {
			for !f.mu.TryLock() {
				if f.stands.Load() == copyBehind {
					return errCopyBehind
				}
				time.Sleep(200 * time.Microsecond)
			}
		}
		more, err := s.feedOnce(ctx, f, most, kind)
		if err == errCopyBehind {
			// No failure of the feed: the question does not wait.
			f.mu.Unlock()
			return err
		}
		f.lastErrMutex.Lock()
		f.lastErr = err
		if err != nil {
			f.failed++
		}
		f.lastErrMutex.Unlock()
		switch {
		case err != nil:
			f.stands.Store(copyUnknown)
		case more:
			f.stands.Store(copyBehind)
		default:
			f.stands.Store(copyNear)
		}
		f.mu.Unlock()
		if err != nil || !more {
			return err
		}
		if most > 0 {
			// More than one piece: a load, which the feed that runs by
			// itself goes on with.
			return errCopyBehind
		}
	}
}

// Where a copy stands to its database.
const (
	copyUnknown int32 = iota
	copyNear
	copyBehind
)

// errCopyBehind is why the store answered a question the copy could
// have: the copy was still being made, or a long load behind.
var errCopyBehind = errors.New("the copy is still being brought up to the database")

// copyReady brings the copy up to the database for one question
// (ADR-0089, item 5) and answers the feeder to ask. It answers false
// when the store has to answer instead: the program keeps no copy, the
// copy is being made or is more than copyCatchUp events behind, so
// that the question would wait for it, or it cannot be fed. CopyState
// keeps why.
func (s *Store) copyReady(ctx context.Context) (*feeder, bool) {
	f := s.feeder
	if f == nil {
		return nil, false
	}
	if f.stands.Load() == copyBehind {
		f.notAnswered(errCopyBehind)
		return nil, false
	}
	if err := s.feedAtMost(ctx, copyCatchUp); err != nil {
		if err == errCopyBehind {
			f.notAnswered(err)
		}
		return nil, false
	}
	return f, true
}

// copyCell is a value the copy answered as the store's driver would
// have given it: a whole number of any width as an int64, since a value
// is read from either engine by the same code.
func copyCell(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case float32:
		return float64(x)
	case *big.Int:
		if x != nil && x.IsInt64() {
			return x.Int64()
		}
	}
	return v
}

// feedOnce is one piece of a feed: it reports whether the database
// holds more than it handed over.
func (s *Store) feedOnce(ctx context.Context, f *feeder, most int64, kind feedKind) (bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, storageErr("open a connection for the copy", err)
	}
	defer conn.Close()
	// The classes as they are, and the moment of the database the feed
	// reads, are taken together: a class that is being redefined has
	// written its tables before it is the class of the store, and no
	// document of its new version exists until then, so the columns
	// read here are the columns of every row this moment holds.
	s.cmu.RLock()
	layouts := make([]*copyLayout, 0, len(s.order))
	for _, name := range s.order {
		if c := s.classes[strings.ToUpper(name)]; c != nil {
			layouts = append(layouts, layoutOf(c))
		}
	}
	// The tables of the database, but the system tables, whose rows are
	// the engine's own records and are read where they are.
	tabs := make([]*tableLayout, 0, len(s.tableOrder))
	for _, name := range s.tableOrder {
		if t := s.tables[strings.ToUpper(name)]; t != nil && !t.system {
			tabs = append(tabs, tableLayoutOf(t))
		}
	}
	// How far the database has got: the place of its last event. The
	// feed goes by the place of a row and not by the id of its event:
	// an id begins with its moment, and after a clock was set back a
	// new event sorts before an older one.
	var last sql.NullInt64
	if _, err = conn.ExecContext(ctx, "BEGIN"); err == nil {
		err = conn.QueryRowContext(ctx, `SELECT max(rowid) FROM "events"`).Scan(&last)
	}
	s.cmu.RUnlock()
	// The read ends without having written: nothing to keep.
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
	if err != nil {
		return false, storageErr("read the database for the copy", err)
	}

	// A copy fed from another database, or from a later moment of this
	// one than a backup brought back, names an event the database does
	// not have: it is made again whole.
	next := make(map[string]string, len(f.marks))
	for k, v := range f.marks {
		next[k] = v
	}
	whole, from := false, int64(0)
	if mark := next[copyMarkEvent]; mark != "" {
		place, event, _ := strings.Cut(mark, " ")
		at, err := strconv.ParseInt(place, 10, 64)
		var have sql.NullString
		if err == nil {
			err = conn.QueryRowContext(ctx, `SELECT "event_id" FROM "events" WHERE rowid = ?`, at).Scan(&have)
		}
		switch {
		case err == nil && have.String == event:
			from = at
		case err == nil || err == sql.ErrNoRows || errors.Is(err, strconv.ErrSyntax):
			whole = true
		default:
			return false, storageErr("read the database for the copy", err)
		}
	}
	// A copy that was fed before it kept the moves has the events and
	// none of the moves: the events are read again from the first, and
	// the moves are made with them. The versions and the documents stay.
	again := !whole && next[copyMarkFormat] != copyFormat
	if again {
		from = 0
	}
	// Where the copy stands is known from here, before a table is laid
	// or a row copied: it is behind when it is made again whole, when
	// it is more than copyCatchUp events behind, and when a class is in
	// the middle of a long load. The questions that come while it is
	// brought up go to the store, and so does the one that found it so;
	// a copy that is near is waited for, since that is a moment.
	behind := whole || last.Int64-from > copyCatchUp
	for _, mark := range next {
		behind = behind || strings.HasSuffix(mark, " "+copyPartial)
	}
	if behind {
		f.stands.Store(copyBehind)
		if most > 0 {
			return false, errCopyBehind
		}
	} else {
		f.stands.Store(copyNear)
	}
	// The tables of the database (ADR-0094): which of them changed
	// since the copy was made of them, and which of those this feed
	// copies.
	copyNow, gone, tablesAre, kick, err := f.tablesToCopy(ctx, conn, tabs, next, whole, most, kind)
	if err != nil {
		return false, err
	}
	if kick {
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
	// What the copy has to be given tables for.
	var lay []CopyTable
	for _, l := range layouts {
		if f.laid[l.name] != l.version {
			lay = append(lay, l.now, l.versions, l.moves)
		}
	}
	for _, l := range copyNow {
		if f.laidTables[l.copy] != l.version {
			lay = append(lay, l.laid)
		}
	}
	if _, ok := f.laid[copyEvents]; !ok {
		lay = append(lay, CopyTable{Name: copyEvents, Columns: copyEventColumns})
	}
	if len(lay) > 0 {
		if err := f.copy.Lay(ctx, lay); err != nil {
			return false, err
		}
		for _, l := range layouts {
			f.laid[l.name] = l.version
		}
		for _, l := range copyNow {
			f.laidTables[l.copy] = l.version
		}
		f.laid[copyEvents] = 1
	}

	if whole {
		for name := range next {
			next[name] = ""
		}
	}
	// Nothing new, and nothing to make again: the copy is as the
	// database is.
	if !whole && !again && last.Int64 == from && len(lay) == 0 && len(copyNow) == 0 && len(gone) == 0 {
		f.tables.Store(&tablesAre)
		return false, nil
	}
	byClass := make(map[string]*copyLayout, len(layouts))
	for _, l := range layouts {
		byClass[strings.ToUpper(l.name)] = l
	}

	wrote, more := false, false
	copiedNow, took := map[string]copiedTable{}, map[string]time.Duration{}
	give := func(w CopyWriter) error {
		if whole || again {
			if err := w.Empty(copyEvents); err != nil {
				return err
			}
			for _, l := range layouts {
				if err := w.Empty(l.moves.Name); err != nil {
					return err
				}
			}
		}
		next[copyMarkFormat] = copyFormat
		// The events since the mark, and the documents they removed: a
		// removal writes no version, so only its event tells of it. With
		// each event comes, for a transition, the moment the state it
		// left was entered: what a move is timed by.
		removed := map[string][]string{}
		if whole {
			from = 0
		}
		rows, err := conn.QueryContext(ctx, `SELECT rowid, `+copyEventsSQL+`, `+leftSinceSQL(layouts)+` FROM "events" WHERE rowid > ? ORDER BY rowid LIMIT ?`, from, copySlice)
		if err != nil {
			return storageErr("read the events for the copy", err)
		}
		row, dest := scanInto(len(copyEventColumns) + 2)
		event, leftAt := row[1:len(copyEventColumns)+1], len(copyEventColumns)+1
		move := make([]any, len(copyMoveColumns))
		events := int64(0)
		for rows.Next() {
			events++
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return storageErr("read the events for the copy", err)
			}
			place, _ := row[0].(int64)
			id, _ := row[1].(string)
			next[copyMarkEvent] = strconv.FormatInt(place, 10) + " " + id
			class, _ := event[eventClass].(string)
			switch kind, _ := event[eventKind].(string); kind {
			case EventDeleted:
				doc, _ := event[eventDoc].(string)
				removed[strings.ToUpper(class)] = append(removed[strings.ToUpper(class)], doc)
			case EventCreated, EventTransition:
				// A move of a document of a class the database has.
				if l := byClass[strings.ToUpper(class)]; l != nil {
					move[0], move[1], move[2], move[3], move[4], move[5], move[6] = event[eventDoc], kind, event[eventName], event[eventFrom], event[eventTo], event[eventTS], row[leftAt]
					if err := w.Append(l.moves.Name, move); err != nil {
						rows.Close()
						return err
					}
				}
			}
			if err := w.Append(copyEvents, event); err != nil {
				rows.Close()
				return err
			}
			wrote = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return storageErr("read the events for the copy", err)
		}
		rows.Close()
		more = events == copySlice

		for _, l := range layouts {
			// A document that was deleted has no moves any more: the
			// moves are of the documents that exist, as the flow is. The
			// id of a deleted document is never given again.
			if err := w.Remove(l.moves.Name, removed[strings.ToUpper(l.name)]); err != nil {
				return err
			}
			did, left, err := feedClass(ctx, conn, w, l, next, removed[strings.ToUpper(l.name)])
			if err != nil {
				return err
			}
			wrote, more = wrote || did, more || left
		}
		// The tables that changed, each whole: a table has no log of
		// its changes. The mark is the token the rows were read under.
		for _, l := range copyNow {
			began := time.Now()
			if err := w.Empty(l.copy); err != nil {
				return err
			}
			n, err := feedTable(ctx, conn, w, l)
			if err != nil {
				return err
			}
			next[copyMarkTable+l.upper] = l.want
			copiedNow[l.upper] = copiedTable{current: true, rows: n}
			took[l.upper] = time.Since(began)
			wrote = true
		}
		// A table the database no longer has: its rows go, and its mark.
		for _, name := range gone {
			if err := w.Empty("tbl_" + strings.ToLower(strings.TrimPrefix(name, copyMarkTable))); err != nil {
				return err
			}
			next[name] = ""
			wrote = true
		}
		return nil
	}
	if err := f.copy.Feed(ctx, give, next); err != nil {
		return false, err
	}
	for name, is := range copiedNow {
		tablesAre[name] = is
		f.tableAt[name], f.tableTook[name] = time.Now(), took[name]
	}
	f.tables.Store(&tablesAre)
	for name, value := range next {
		if value == "" {
			delete(next, name)
		}
	}
	f.marks = next
	if wrote || whole {
		f.lastErrMutex.Lock()
		f.fed++
		f.lastErrMutex.Unlock()
	}
	return more, nil
}

// scanInto makes a row of n values and what a scan writes them through.
func scanInto(n int) ([]any, []any) {
	row, dest := make([]any, n), make([]any, n)
	for i := range row {
		dest[i] = &row[i]
	}
	return row, dest
}

// feedClass gives the copy what one class gained since its mark: the
// versions written since, at most copySlice of them, and the documents
// they changed as they are now. It reports whether it handed anything
// over and whether the class holds more versions than it handed over,
// and leaves the new mark of the class in next.
//
// A class whose documents as they are now are read whole, because the
// copy holds none of them yet or most of them changed, gets them with
// the last piece. Until then its mark ends in copyPartial: its versions
// are in the copy up to the mark, and its documents as they are now are
// not to be asked.
func feedClass(ctx context.Context, conn *sql.Conn, w CopyWriter, l *copyLayout, next map[string]string, removed []string) (bool, bool, error) {
	fail := func(err error) (bool, bool, error) {
		return false, false, storageErr("read "+l.name+" for the copy", err)
	}
	// The mark of the class: the place of the last version the copy
	// holds, and the event that wrote it. A class that was made again
	// has another row there, or none, and its copy is made again.
	from, whole, midway := int64(0), true, false
	if mark := strings.Fields(next[copyMarkClass+l.name]); len(mark) >= 2 {
		at, err := strconv.ParseInt(mark[0], 10, 64)
		var have sql.NullString
		if err == nil && at > 0 {
			err = conn.QueryRowContext(ctx, `SELECT "event_id" FROM `+quote(l.history)+` WHERE rowid = ?`, at).Scan(&have)
		}
		switch {
		case err == sql.ErrNoRows || errors.Is(err, strconv.ErrSyntax):
		case err != nil:
			return fail(err)
		case at == 0 && mark[1] == copyNoVersion:
			// The class had no version when it was last fed, and the
			// copy holds nothing of it.
			whole = false
		case at > 0 && have.String == mark[1]:
			from, whole = at, false
			midway = len(mark) > 2 && mark[2] == copyPartial
		}
	}
	// How many versions were written since the mark. A version is never
	// taken out of a history, so the places after the mark are those
	// versions, and the last place says how many there are: asking the
	// store to count them would read every one, at every piece of a
	// long load.
	var last sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT max(rowid) FROM `+quote(l.history)).Scan(&last); err != nil {
		return fail(err)
	}
	news := max(last.Int64-from, 0)
	if !whole && !midway && news == 0 && len(removed) == 0 {
		return false, false, nil
	}
	if whole {
		if err := w.Empty(l.versions.Name); err != nil {
			return false, false, err
		}
	}

	// The versions since the mark, in the order they were written, and
	// how the documents as they are now follow them. Usually the
	// documents these versions changed are taken out of the copy and
	// put back as they are, piece by piece. They are read whole, with
	// the last piece, when the copy holds none of the class yet, and
	// when the versions are so many that most documents changed: more
	// than copyRemakeNow, and more than a quarter of the documents the
	// class holds.
	left := news > copySlice
	var docs sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT max(rowid) FROM `+quote(l.table)).Scan(&docs); err != nil {
		return fail(err)
	}
	nowWhole := whole || midway || news > max(copyRemakeNow, docs.Int64/4)
	changed := map[string]bool{}
	for _, id := range removed {
		changed[id] = true
	}
	rows, err := conn.QueryContext(ctx, `SELECT rowid, `+l.versionsSQL+` FROM `+quote(l.history)+` WHERE rowid > ? ORDER BY rowid LIMIT ?`, from, copySlice)
	if err != nil {
		return fail(err)
	}
	n := len(l.versions.Columns)
	row, dest := scanInto(n + 1)
	lastPlace, lastEvent := from, ""
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return fail(err)
		}
		lastPlace, _ = row[0].(int64)
		lastEvent, _ = row[1+l.eventAt].(string)
		if !nowWhole {
			if id, ok := row[1].(string); ok {
				changed[id] = true
			}
		}
		bools(row[1:], l.versionsBools)
		if err := w.Append(l.versions.Name, row[1:]); err != nil {
			rows.Close()
			return false, false, err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fail(err)
	}
	rows.Close()
	switch {
	case lastEvent != "" && left && nowWhole:
		// More versions follow, and the documents as they are now are
		// read whole with the last piece: until then the mark says the
		// class is in the middle of its load.
		next[copyMarkClass+l.name] = strconv.FormatInt(lastPlace, 10) + " " + lastEvent + " " + copyPartial
		return true, true, nil
	case lastEvent != "":
		next[copyMarkClass+l.name] = strconv.FormatInt(lastPlace, 10) + " " + lastEvent
	case whole:
		next[copyMarkClass+l.name] = "0 " + copyNoVersion
	case midway:
		// The last piece of a long load found nothing more: the mark
		// stays where it is, and is whole now.
		mark := strings.Fields(next[copyMarkClass+l.name])
		next[copyMarkClass+l.name] = mark[0] + " " + mark[1]
	}

	// The documents as they are now.
	appendNow := func(query string, args ...any) error {
		rows, err := conn.QueryContext(ctx, query, args...)
		if err != nil {
			return storageErr("read "+l.name+" for the copy", err)
		}
		defer rows.Close()
		row, dest := scanInto(len(l.now.Columns))
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				return storageErr("read "+l.name+" for the copy", err)
			}
			bools(row, l.nowBools)
			if err := w.Append(l.now.Name, row); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return storageErr("read "+l.name+" for the copy", err)
		}
		return nil
	}
	if nowWhole {
		if err := w.Empty(l.now.Name); err != nil {
			return false, false, err
		}
		return true, false, appendNow(`SELECT ` + l.nowSQL + ` FROM ` + quote(l.table))
	}
	// The documents this piece changed, as they are at the moment the
	// feed reads: a later piece changes them again if they moved on.
	ids := make([]string, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	if err := w.Remove(l.now.Name, ids); err != nil {
		return false, false, err
	}
	for start := 0; start < len(ids); start += copyChunk {
		part := ids[start:min(start+copyChunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		if err := appendNow(`SELECT `+l.nowSQL+` FROM `+quote(l.table)+` WHERE "id" IN (`+marks(len(part))+`)`, args...); err != nil {
			return false, false, err
		}
	}
	return true, left, nil
}
