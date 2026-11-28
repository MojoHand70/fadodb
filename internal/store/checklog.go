package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// LogCheck is what CheckLog found: how much of the log it checked, from
// when the log is sealed, and every place where the log does not match
// its seals (ADR-0103).
type LogCheck struct {
	// Events is how many events the log holds, and Sealed how many of
	// them carry a seal.
	Events int `json:"events"`
	Sealed int `json:"sealed"`
	// SealedFrom is the moment of the first sealed event, in seconds
	// since 1970; 0 when no event is sealed. A database made before the
	// seal is sealed from the first event an engine with the seal wrote.
	SealedFrom int64 `json:"sealed_from,omitempty"`
	// Faults are the places where the log does not match its seals, in
	// the order of the log. None is a log that nobody altered unseen.
	Faults []LogFault `json:"faults"`
}

// The kinds of fault the check finds.
const (
	// FaultChanged is an event whose seal does not match what the log
	// holds: the event or a row it wrote was changed, or an event before
	// it was taken out or put in.
	FaultChanged = "changed"
	// FaultUnsealed is an event without a seal where the log is sealed.
	FaultUnsealed = "unsealed"
	// FaultCarried is a list of a version that carried the rows of the
	// version before forward and holds other rows.
	FaultCarried = "carried"
	// FaultStray is a row of a history, a list or event_docs that no
	// event wrote.
	FaultStray = "stray"
	// FaultCurrent is a document whose current row is not its last
	// version.
	FaultCurrent = "current"
)

// LogFault is one place where the log does not match its seals.
type LogFault struct {
	// What is the kind of fault: changed, unsealed, carried, stray or
	// current.
	What string `json:"what"`
	// Event is the event concerned, with its kind, its name and its
	// moment, when there is one.
	Event string `json:"event,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Name  string `json:"name,omitempty"`
	At    int64  `json:"at,omitempty"`
	// Class, ID and Version are the document concerned.
	Class   string `json:"class,omitempty"`
	ID      string `json:"id,omitempty"`
	Version int64  `json:"version,omitempty"`
	// Message says what does not match, in words.
	Message string `json:"message"`
}

// maxLogFaults bounds the faults a check lists of each kind: past it a
// log was rewritten wholesale, and the first ones say where.
const maxLogFaults = 100

// CheckLog checks the seal of the log (ADR-0103): it works the seal of
// every event out again from the rows the event wrote, and the seal of
// the event before, and names every event whose seal does not match;
// and it names the rows of the histories, the lists and event_docs that
// no event wrote, and the documents whose current row is not their last
// version. It reads one snapshot of the database and stops no commit.
// It is the work of a database administrator.
func (s *Store) CheckLog(ctx context.Context, opts ...Option) (*LogCheck, error) {
	if _, e := s.worker(ctx, workOperate, opts); e != nil {
		return nil, e
	}
	return s.checkLog(ctx)
}

// logChecker holds what a check reads with.
type logChecker struct {
	s     *Store
	tx    *sql.Tx
	check *LogCheck
	count map[string]int
	buf   []byte
}

func (s *Store) checkLog(ctx context.Context) (*LogCheck, error) {
	// One read transaction is one snapshot of the file: the commits
	// that land meanwhile are not in it, and none waits for the check.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, storageErr("begin the check", err)
	}
	defer func() { _ = tx.Rollback() }()
	k := &logChecker{s: s, tx: tx, check: &LogCheck{Faults: []LogFault{}}, count: map[string]int{}}
	if err := k.events(ctx); err != nil {
		return nil, err
	}
	if err := k.strays(ctx); err != nil {
		return nil, err
	}
	if err := k.currents(ctx); err != nil {
		return nil, err
	}
	return k.check, nil
}

// fault records a fault, up to the bound for its kind.
func (k *logChecker) fault(f LogFault) {
	k.count[f.What]++
	if k.count[f.What] <= maxLogFaults {
		k.check.Faults = append(k.check.Faults, f)
	}
}

// classOf is the compiled class by its name, and the version given; nil
// when the database has no such class.
func (k *logChecker) classOf(name string, version int) *class {
	k.s.cmu.RLock()
	defer k.s.cmu.RUnlock()
	vs := k.s.versions[strings.ToUpper(name)]
	if version > 0 {
		if c := vs[version]; c != nil {
			return c
		}
	}
	return k.s.classes[strings.ToUpper(name)]
}

// logRow is a row read for the check: its columns by name.
type logRow struct {
	names []string
	vals  []any
}

func (r logRow) get(name string) any {
	for i, n := range r.names {
		if n == name {
			return r.vals[i]
		}
	}
	return nil
}

func (r logRow) text(name string) string { return toString(r.get(name)) }

func (r logRow) int(name string) (int64, bool) {
	v := r.get(name)
	if v == nil {
		return 0, false
	}
	return toInt64(v), true
}

// query reads every row a statement answers.
func (k *logChecker) query(ctx context.Context, text string, args ...any) ([]logRow, error) {
	rows, err := k.tx.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []logRow
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, logRow{names: names, vals: vals})
	}
	return out, rows.Err()
}

// rowBytes appends a row as the writer sealed it: each value that is not
// NULL with its column's name, in the order of the names, but the
// columns left out.
func rowBytes(b []byte, table string, r logRow, leave map[string]bool) []byte {
	order := make([]int, len(r.names))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, c int) bool { return r.names[order[a]] < r.names[order[c]] })
	b = append(b, 'R')
	b = sealText(b, table)
	for _, i := range order {
		if r.vals[i] == nil || leave[r.names[i]] {
			continue
		}
		b = sealText(b, r.names[i])
		b = sealValue(b, r.vals[i])
	}
	return append(b, 'E')
}

var (
	leaveOfEvent   = map[string]bool{"rowid": true, "seal": true}
	leaveOfHistory = map[string]bool{"valid_to": true}
	leaveOfVersion = map[string]bool{"version": true}
)

// events walks the log in its order and works out each seal again.
func (k *logChecker) events(ctx context.Context) error {
	var after int64
	if err := k.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT CAST(value AS INTEGER) FROM metadata WHERE key = 'sealed_after'), 0)").Scan(&after); err != nil {
		return storageErr("read the metadata", err)
	}
	var prev [sealSize]byte
	var place int64
	for {
		// The log in pieces, so that each event's rows are read while no
		// other statement is open.
		batch, err := k.query(ctx, "SELECT rowid, * FROM events WHERE rowid > ? ORDER BY rowid LIMIT 500", place)
		if err != nil {
			return storageErr("read the log", err)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, ev := range batch {
			row, _ := ev.int("rowid")
			place = row
			k.check.Events++
			seal, _ := ev.get("seal").([]byte)
			if row <= after {
				// Written before the engine sealed the log.
				prev = [sealSize]byte{}
				continue
			}
			if len(seal) < sealSize {
				k.fault(k.faultOf(FaultUnsealed, ev, "the event carries no seal, where the log is sealed"))
				prev = [sealSize]byte{}
				continue
			}
			k.check.Sealed++
			if k.check.SealedFrom == 0 {
				k.check.SealedFrom, _ = ev.int("ts")
			}
			want, err := k.sealOf(ctx, ev, seal[sealSize:], &prev)
			if err != nil {
				return err
			}
			if !bytes.Equal(want[:], seal[:sealSize]) {
				k.fault(k.faultOf(FaultChanged, ev, "the seal does not match what the log holds: the event, or a row it wrote, was changed, or an event before it was taken out or put in"))
			}
			copy(prev[:], seal[:sealSize])
		}
	}
}

// faultOf is a fault of the event.
func (k *logChecker) faultOf(what string, ev logRow, message string) LogFault {
	at, _ := ev.int("ts")
	version, _ := ev.int("after_version")
	return LogFault{What: what, Event: ev.text("event_id"), Kind: ev.text("kind"), Name: ev.text("name"), At: at,
		Class: ev.text("class"), ID: ev.text("doc_id"), Version: version, Message: message}
}

// sealOf works out the seal of an event from the rows it wrote, in the
// order the writer sealed them, after the seal of the event before.
func (k *logChecker) sealOf(ctx context.Context, ev logRow, carried []byte, prev *[sealSize]byte) ([sealSize]byte, error) {
	b := append(k.buf[:0], sealForm...)
	eventID, className, docID, kind := ev.text("event_id"), ev.text("class"), ev.text("doc_id"), ev.text("kind")
	before, hasBefore := ev.int("before_version")
	after, hasAfter := ev.int("after_version")
	c := k.classOf(className, 0)
	var hist []logRow
	if c != nil && hasAfter {
		var err error
		if hist, err = k.query(ctx, "SELECT * FROM "+quote(c.history)+` WHERE "id" = ? AND "version" = ? AND "event_id" = ?`, docID, after, eventID); err != nil {
			return [sealSize]byte{}, storageErr("read the history of "+className, err)
		}
	}
	if c != nil && hasBefore && (len(hist) > 0 || kind == EventDeleted) {
		var end any
		if err := k.tx.QueryRowContext(ctx, "SELECT "+quote("valid_to")+" FROM "+quote(c.history)+` WHERE "id" = ? AND "version" = ?`, docID, before).Scan(&end); err != nil && err != sql.ErrNoRows {
			return [sealSize]byte{}, storageErr("read the history of "+className, err)
		}
		b = append(b, 'C')
		b = sealText(b, c.history)
		b = sealValue(b, docID)
		b = sealValue(b, before)
		b = sealValue(b, end)
	}
	for _, h := range hist {
		b = rowBytes(b, c.history, h, leaveOfHistory)
	}
	if len(hist) > 0 {
		cv, _ := hist[0].int("class_version")
		written := k.classOf(className, int(cv))
		for n, ch := range written.children {
			carry := n < maxSealLists && n/8 < len(carried) && carried[n/8]&(1<<(n%8)) != 0
			b = append(b, 'L')
			b = sealText(b, ch.table)
			if carry {
				b = append(b, 1)
				b = sealValue(b, docID)
				b = sealValue(b, before)
				b = sealValue(b, after)
				if err := k.carried(ctx, ev, ch, docID, before, after); err != nil {
					return [sealSize]byte{}, err
				}
				continue
			}
			b = append(b, 0)
			rows, err := k.listRows(ctx, ch, docID, after)
			if err != nil {
				return [sealSize]byte{}, err
			}
			for _, r := range rows {
				b = rowBytes(b, ch.table, r, nil)
			}
		}
	}
	b = rowBytes(b, "events", ev, leaveOfEvent)
	docs, err := k.query(ctx, `SELECT * FROM event_docs WHERE event_id = ? ORDER BY class, doc_id`, eventID)
	if err != nil {
		return [sealSize]byte{}, storageErr("read the documents of the log", err)
	}
	for _, d := range docs {
		b = rowBytes(b, "event_docs", d, nil)
	}
	k.buf = b
	h := sha256.New()
	h.Write(prev[:])
	h.Write(b)
	var out [sealSize]byte
	h.Sum(out[:0])
	return out, nil
}

// listRows reads the rows a version holds of a list; a list whose table
// no longer holds its columns has none.
func (k *logChecker) listRows(ctx context.Context, ch *child, id string, version int64) ([]logRow, error) {
	if ch.gone {
		return nil, nil
	}
	rows, err := k.query(ctx, "SELECT * FROM "+quote(ch.table)+` WHERE "doc_id" = ? AND "version" = ? ORDER BY "idx"`, id, version)
	if err != nil {
		return nil, storageErr("read "+ch.table, err)
	}
	return rows, nil
}

// carried checks that a version that carried a list forward holds the
// rows of the version before.
func (k *logChecker) carried(ctx context.Context, ev logRow, ch *child, id string, before, after int64) error {
	was, err := k.listRows(ctx, ch, id, before)
	if err != nil {
		return err
	}
	is, err := k.listRows(ctx, ch, id, after)
	if err != nil {
		return err
	}
	same := len(was) == len(is)
	for i := 0; same && i < len(is); i++ {
		same = bytes.Equal(rowBytes(nil, ch.table, was[i], leaveOfVersion), rowBytes(nil, ch.table, is[i], leaveOfVersion))
	}
	if !same {
		k.fault(k.faultOf(FaultCarried, ev, fmt.Sprintf("the rows of %s in version %d are not those of version %d, which it carried forward", ch.v.Name, after, before)))
	}
	return nil
}

// strays names the rows that no event wrote: a version of a document
// whose event is not in the log, or not of that version; a row of a list
// of a version the history does not hold; a row of event_docs without
// its event.
func (k *logChecker) strays(ctx context.Context) error {
	for _, c := range k.classesToCheck() {
		rows, err := k.query(ctx, `SELECT h."id", h."version", h."event_id" FROM `+quote(c.history)+` h LEFT JOIN events e ON e.event_id = h."event_id"`+
			` WHERE e.event_id IS NULL OR upper(e.class) <> upper(?) OR e.doc_id IS NOT h."id" OR e.after_version IS NOT h."version" LIMIT ?`, c.cls.Name, maxLogFaults+1)
		if err != nil {
			return storageErr("read the history of "+c.cls.Name, err)
		}
		for _, r := range rows {
			version, _ := r.int("version")
			k.fault(LogFault{What: FaultStray, Event: r.text("event_id"), Class: c.cls.Name, ID: r.text("id"), Version: version,
				Message: "a version of the document that no event of the log wrote"})
		}
		for _, table := range k.listTables(c) {
			rows, err := k.query(ctx, `SELECT DISTINCT k."doc_id", k."version" FROM `+quote(table)+` k LEFT JOIN `+quote(c.history)+` h ON h."id" = k."doc_id" AND h."version" = k."version"`+
				` WHERE h."id" IS NULL LIMIT ?`, maxLogFaults+1)
			if err != nil {
				return storageErr("read "+table, err)
			}
			for _, r := range rows {
				version, _ := r.int("version")
				k.fault(LogFault{What: FaultStray, Class: c.cls.Name, ID: r.text("doc_id"), Version: version,
					Message: "rows of " + table + " of a version the history does not hold"})
			}
		}
	}
	rows, err := k.query(ctx, `SELECT d.event_id, d.class, d.doc_id FROM event_docs d LEFT JOIN events e ON e.event_id = d.event_id WHERE e.event_id IS NULL LIMIT ?`, maxLogFaults+1)
	if err != nil {
		return storageErr("read the documents of the log", err)
	}
	for _, r := range rows {
		k.fault(LogFault{What: FaultStray, Event: r.text("event_id"), Class: r.text("class"), ID: r.text("doc_id"),
			Message: "a row of event_docs whose event is not in the log"})
	}
	return nil
}

// classesToCheck are the current versions of the classes, in the order
// they were defined.
func (k *logChecker) classesToCheck() []*class {
	k.s.cmu.RLock()
	defer k.s.cmu.RUnlock()
	out := make([]*class, 0, len(k.s.order))
	for _, name := range k.s.order {
		if c := k.s.classes[strings.ToUpper(name)]; c != nil && c.history != "" {
			out = append(out, c)
		}
	}
	return out
}

// listTables are the tables of the lists of every version of a class
// that still hold their rows.
func (k *logChecker) listTables(c *class) []string {
	k.s.cmu.RLock()
	defer k.s.cmu.RUnlock()
	seen := map[string]bool{}
	var out []string
	versions := k.s.versions[strings.ToUpper(c.cls.Name)]
	numbers := make([]int, 0, len(versions))
	for n := range versions {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		for _, ch := range versions[n].children {
			if !ch.gone && !seen[ch.table] {
				seen[ch.table] = true
				out = append(out, ch.table)
			}
		}
	}
	return out
}

// currents names the documents whose current row does not hold what
// their last version holds: a value changed in place, where no seal
// reaches, shows here. A document holds the columns of its class
// version; a column another version keeps may hold what the document
// had under an earlier one, and is not compared.
func (k *logChecker) currents(ctx context.Context) error {
	for _, c := range k.classesToCheck() {
		shared, err := k.sharedColumns(ctx, c)
		if err != nil {
			return err
		}
		for _, v := range k.versionsOf(c) {
			cols := make([]string, 0, len(systemColumns)+len(v.cls.Columns))
			for _, col := range append(append([]string{}, systemColumns...), columnNames(v.cls.Columns)...) {
				if shared[col] {
					cols = append(cols, col)
				}
			}
			differs := make([]string, 0, len(cols))
			for _, col := range cols {
				differs = append(differs, "c."+quote(col)+" IS NOT h."+quote(col))
			}
			where := `h."id" IS NULL`
			if len(differs) > 0 {
				where += " OR " + strings.Join(differs, " OR ")
			}
			rows, err := k.query(ctx, `SELECT c."id", c."version" FROM `+quote(c.table)+` c LEFT JOIN `+quote(c.history)+` h ON h."id" = c."id" AND h."version" = c."version"`+
				` WHERE c."class_version" = ? AND (`+where+`) LIMIT ?`, v.cls.Version, maxLogFaults+1)
			if err != nil {
				return storageErr("read the documents of "+c.cls.Name, err)
			}
			for _, r := range rows {
				version, _ := r.int("version")
				which, err := k.differing(ctx, c, cols, r.text("id"), version)
				if err != nil {
					return err
				}
				k.fault(LogFault{What: FaultCurrent, Class: c.cls.Name, ID: r.text("id"), Version: version,
					Message: "the document holds values that are not those of its last version " + strconv.FormatInt(version, 10) + which})
			}
		}
	}
	return nil
}

// differing says which columns of a document's current row differ from
// its version, or that the history holds no such version.
func (k *logChecker) differing(ctx context.Context, c *class, cols []string, id string, version int64) (string, error) {
	list := strings.Join(quoteAll(cols), ", ")
	cur, err := k.query(ctx, "SELECT "+list+" FROM "+quote(c.table)+` WHERE "id" = ?`, id)
	if err != nil {
		return "", storageErr("read the documents of "+c.cls.Name, err)
	}
	hist, err := k.query(ctx, "SELECT "+list+" FROM "+quote(c.history)+` WHERE "id" = ? AND "version" = ?`, id, version)
	if err != nil {
		return "", storageErr("read the history of "+c.cls.Name, err)
	}
	if len(cur) == 0 || len(hist) == 0 {
		return ", which the history does not hold", nil
	}
	var names []string
	for i, col := range cols {
		if !bytes.Equal(sealValue(nil, cur[0].vals[i]), sealValue(nil, hist[0].vals[i])) {
			names = append(names, col)
		}
	}
	return ": " + strings.Join(names, ", "), nil
}

// versionsOf are the compiled versions of a class, in order.
func (k *logChecker) versionsOf(c *class) []*class {
	k.s.cmu.RLock()
	defer k.s.cmu.RUnlock()
	versions := k.s.versions[strings.ToUpper(c.cls.Name)]
	numbers := make([]int, 0, len(versions))
	for n := range versions {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	out := make([]*class, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, versions[n])
	}
	return out
}

// sharedColumns are the columns the current table of a class and its
// history both hold.
func (k *logChecker) sharedColumns(ctx context.Context, c *class) (map[string]bool, error) {
	cols := func(table string) ([]string, error) {
		rows, err := k.query(ctx, "SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return nil, storageErr("read the columns of "+table, err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.text("name"))
		}
		return out, nil
	}
	current, err := cols(c.table)
	if err != nil {
		return nil, err
	}
	history, err := cols(c.history)
	if err != nil {
		return nil, err
	}
	in := map[string]bool{}
	for _, h := range history {
		in[h] = true
	}
	out := map[string]bool{}
	for _, col := range current {
		if in[col] {
			out[col] = true
		}
	}
	return out, nil
}
