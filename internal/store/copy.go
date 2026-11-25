package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
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
	copyMarkEvent = "event"
	copyMarkClass = "class:"
	copyEventsSQL = `"event_id", "class", "doc_id", "kind", "name", "from_state", "to_state", "actor_id", "actor_kind", "actor_for", "before_version", "after_version", "cause_event_id", "command_key", "command_hash", "business_date", "ts", "origin", "override", "reason", "seen_version"`
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
	// mu lets one feed run at a time.
	mu sync.Mutex
	// marks are the marks as the copy holds them: read when the store
	// opened, and kept with every feed.
	marks map[string]string
	// laid is the version of each class whose tables the copy has.
	laid map[string]int
	// every is the period of the feed that runs by itself.
	every   time.Duration
	cancel  context.CancelFunc
	stopped sync.Once
	// first is closed when the feed that runs at the opening has ended,
	// stop asks the feed that runs by itself to end, and done says it
	// has.
	first, stop, done chan struct{}
	fed, failed       int64
	lastErr           error
	lastErrMutex      sync.Mutex
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
	f := &feeder{copy: c, marks: marks, laid: map[string]int{}, every: every, first: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	var fctx context.Context
	fctx, f.cancel = context.WithCancel(context.Background())
	s.feeder = f
	go func() {
		defer close(f.done)
		// At once: the copy is laid, checked against the database and
		// brought up to it.
		_ = s.feed(fctx)
		close(f.first)
		t := time.NewTicker(f.every)
		defer t.Stop()
		for {
			select {
			case <-f.stop:
				return
			case <-t.C:
				_ = s.feed(fctx)
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
		_ = s.feed(ctx)
		cancel()
		_ = f.copy.Close()
	})
}

// CopyState says how the copy of an open database stands: whether the
// program gave an analytical engine, how many feeds wrote to the copy,
// and why the last one failed, nil when it did not.
type CopyState struct {
	Kept  bool
	Feeds int64
	Err   error
}

// CopyState answers how the copy of the database stands.
func (s *Store) CopyState() CopyState {
	f := s.feeder
	if f == nil {
		return CopyState{Err: s.copyErr}
	}
	f.lastErrMutex.Lock()
	defer f.lastErrMutex.Unlock()
	return CopyState{Kept: true, Feeds: f.fed, Err: f.lastErr}
}

// FeedCopy brings the copy up to the database now, without waiting for
// the feed that runs by itself. It answers nil for a database that keeps
// no copy.
func (s *Store) FeedCopy(ctx context.Context) error { return s.feed(ctx) }

// feed brings the copy up to the database (ADR-0089, item 4): one read
// of the database, which sees one moment of it, and one transaction of
// the copy, which takes the rows and the new marks together or nothing.
// A feed that finds nothing new writes nothing.
func (s *Store) feed(ctx context.Context) error {
	f := s.feeder
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		more, err := s.feedOnce(ctx, f)
		f.lastErrMutex.Lock()
		f.lastErr = err
		if err != nil {
			f.failed++
		}
		f.lastErrMutex.Unlock()
		if err != nil || !more {
			return err
		}
	}
}

// feedOnce is one piece of a feed: it reports whether the database
// holds more than it handed over.
func (s *Store) feedOnce(ctx context.Context, f *feeder) (bool, error) {
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

	// What the copy has to be given tables for.
	var lay []CopyTable
	for _, l := range layouts {
		if f.laid[l.name] != l.version {
			lay = append(lay, l.now, l.versions)
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
		f.laid[copyEvents] = 1
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
	if whole {
		for name := range next {
			next[name] = ""
		}
	}
	// Nothing new, and nothing to make again: the copy is as the
	// database is.
	if !whole && last.Int64 == from && len(lay) == 0 {
		return false, nil
	}

	wrote, more := false, false
	give := func(w CopyWriter) error {
		if whole {
			if err := w.Empty(copyEvents); err != nil {
				return err
			}
		}
		// The events since the mark, and the documents they removed: a
		// removal writes no version, so only its event tells of it.
		removed := map[string][]string{}
		if whole {
			from = 0
		}
		rows, err := conn.QueryContext(ctx, `SELECT rowid, `+copyEventsSQL+` FROM "events" WHERE rowid > ? ORDER BY rowid LIMIT ?`, from, copySlice)
		if err != nil {
			return storageErr("read the events for the copy", err)
		}
		row, dest := scanInto(len(copyEventColumns) + 1)
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
			if kind, _ := row[4].(string); kind == EventDeleted {
				class, _ := row[2].(string)
				doc, _ := row[3].(string)
				removed[strings.ToUpper(class)] = append(removed[strings.ToUpper(class)], doc)
			}
			if err := w.Append(copyEvents, row[1:]); err != nil {
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
			did, left, err := feedClass(ctx, conn, w, l, next, removed[strings.ToUpper(l.name)])
			if err != nil {
				return err
			}
			wrote, more = wrote || did, more || left
		}
		return nil
	}
	if err := f.copy.Feed(ctx, give, next); err != nil {
		return false, err
	}
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
