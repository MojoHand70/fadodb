package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"hash"
	"math"
	"sort"
)

// The seal of the log (ADR-0103). Every event carries, in its own row,
// a seal: a SHA-256 of the seal of the event before it and of what the
// event wrote, its history row, the closing of the version before, the
// rows of its lists, its own columns and its event_docs row. Change one
// of those rows, take one out or put one in, and the seal of that event
// no longer matches, nor, once it is worked out again, does any after
// it. The check, CheckLog, works every seal out again from the rows.
//
// The seal rides in a row every event writes anyway: it costs the
// commit no statement of its own. The writer keeps the seal of the last
// event in memory, and the events of a transaction that is not yet
// committed apart, so that a rollback, whole or to a savepoint, takes
// them back with the rows.

// sealSize is the length of a seal.
const sealSize = sha256.Size

// sealForm begins the bytes of every event that are sealed: the form of
// those bytes, so that a later form is told apart.
const sealForm = "fado seal 1\x00"

// maxSealLists bounds the lists of a class whose being carried forward
// the seal notes, one bit each after the 32 bytes (ADR-0103, item 7).
const maxSealLists = 256

// sealCols are the columns a statement writes into a row of the log,
// as the seal reads them: each by its name, in the order of the names,
// so that the seal does not depend on the order of a statement or of a
// table's columns.
type sealCols struct {
	table string
	names []string
	order []int
}

// newSealCols names the columns of a statement that writes into the
// table, in the order of its arguments.
func newSealCols(table string, names []string) sealCols {
	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return names[order[a]] < names[order[b]] })
	return sealCols{table: table, names: names, order: order}
}

// The columns of the two rows every event writes, in the order of the
// statements that write them.
var (
	eventSealCols = newSealCols("events", []string{"event_id", "class", "doc_id", "kind", "name", "from_state", "to_state", "actor_id", "before_version", "after_version",
		"cause_event_id", "command_key", "command_hash", "business_date", "ts", "origin", "actor_kind", "actor_for", "override", "reason", "seen_version"})
	eventDocSealCols = newSealCols("event_docs", []string{"event_id", "class", "doc_id", "qualifier"})
)

// sealPoint is an event the writer wrote: its row and its seal.
type sealPoint struct {
	row  int64
	seal [sealSize]byte
}

// sealMark is a savepoint, and the last event written before it.
type sealMark struct {
	name string
	at   sealPoint
}

// sealer is the writer's part of the seal. It is used under the
// writer's lock.
type sealer struct {
	h hash.Hash
	// buf holds the bytes of the event being written, from its first row
	// to its event_docs row.
	buf  []byte
	open bool
	// out is the seal the event row carries, and after it one bit for
	// each list of the class that the version carried forward. boxes
	// holds out boxed once for each length it takes, so that binding it
	// allocates nothing.
	out   [sealSize + maxSealLists/8]byte
	lists int
	boxes [maxSealLists/8 + 1]any
	// docArgs are the values of the event_docs row, for the seal.
	docArgs [4]any
	// committed is the last event of a committed transaction; last the
	// last event written, of the transaction under way or committed;
	// marks the savepoints of the transaction.
	committed, last sealPoint
	marks           []sealMark
	// update writes a seal again when an event was written after the
	// writer lost count (wrote).
	update *sql.Stmt
}

// ready makes what the writer seals with, once, as the database opens,
// so that no commit pays for it.
func (z *sealer) ready() {
	z.h = sha256.New()
	z.buf = make([]byte, 0, 4096)
	z.boxes[0] = any(z.out[:sealSize])
	z.boxes[1] = any(z.out[:sealSize+1])
}

// begin is told that the writer began a transaction: no event of it is
// written yet, and what a transaction taken back wrote is gone.
func (z *sealer) begin() {
	z.last = z.committed
	z.marks = z.marks[:0]
	z.open = false
}

// commit is told that the writer's transaction is committed: its last
// event is the last of the log.
func (z *sealer) commit() {
	z.committed = z.last
	z.marks = z.marks[:0]
	z.open = false
}

// mark is told of a savepoint.
func (z *sealer) mark(name string) {
	z.marks = append(z.marks, sealMark{name: name, at: z.last})
}

// toMark is told that the transaction went back to the savepoint, which
// stays: the events written after it are gone.
func (z *sealer) toMark(name string) {
	for i := len(z.marks) - 1; i >= 0; i-- {
		if z.marks[i].name == name {
			z.last = z.marks[i].at
			z.marks = z.marks[:i+1]
			break
		}
	}
	z.open = false
}

// release is told that the savepoint is released: its events stay with
// the transaction.
func (z *sealer) release(name string) {
	for i := len(z.marks) - 1; i >= 0; i-- {
		if z.marks[i].name == name {
			z.marks = z.marks[:i]
			return
		}
	}
}

// start begins the bytes of an event whose document's class has the
// lists given.
func (z *sealer) start(lists int) {
	if z.h == nil {
		z.ready()
	}
	z.buf = append(z.buf[:0], sealForm...)
	z.open = true
	if lists > maxSealLists {
		lists = maxSealLists
	}
	z.lists = (lists + 7) / 8
	clear(z.out[sealSize : sealSize+z.lists])
}

// starting begins the bytes of an event that wrote nothing before its
// own row, unless they are begun.
func (z *sealer) starting() {
	if !z.open {
		z.start(0)
	}
}

// closing adds the closing of a document's version: the moment its
// history row was given as its end. The values are in the form
// sealValue gives them, written without a box.
func (z *sealer) closing(table, id string, version, secs int64) {
	b := append(z.buf, 'C')
	b = sealText(b, table)
	b = sealText(append(b, 's'), id)
	b = binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(version))
	z.buf = binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(secs))
}

// row adds a row a statement wrote: the values it was given, each with
// its column's name, in the order of the names; a blank, which the
// table holds as NULL, is left out, so that a column added later to the
// table, NULL in the rows before, changes nothing.
func (z *sealer) row(cols *sealCols, args []any) {
	b := append(z.buf, 'R')
	b = sealText(b, cols.table)
	for _, i := range cols.order {
		if i >= len(args) || args[i] == nil {
			continue
		}
		b = sealText(b, cols.names[i])
		b = sealValue(b, args[i])
	}
	z.buf = append(b, 'E')
}

// list adds what a version did with one of its lists: rows of its own,
// which follow, or the rows of the version before carried forward.
func (z *sealer) list(i int, table string, carried bool, id string, from, to int64) {
	b := append(z.buf, 'L')
	b = sealText(b, table)
	if carried {
		b = append(b, 1)
		b = sealText(append(b, 's'), id)
		b = binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(from))
		b = binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(to))
		if i < maxSealLists {
			z.out[sealSize+i/8] |= 1 << (i % 8)
		}
	} else {
		b = append(b, 0)
	}
	z.buf = b
}

// seal works out the seal of the event, chained to the event before,
// and answers it boxed for the event's row.
func (z *sealer) seal() any {
	z.sum(&z.last.seal)
	n := z.lists
	if z.boxes[n] == nil {
		z.boxes[n] = any(z.out[:sealSize+n])
	}
	return z.boxes[n]
}

// sum writes into out the seal of the bytes held, after the seal given.
func (z *sealer) sum(prev *[sealSize]byte) {
	z.h.Reset()
	z.h.Write(prev[:])
	z.h.Write(z.buf)
	z.h.Sum(z.out[:0])
}

// wrote is told the row the event was written at. The writer counts on
// the next row after the last event it wrote; another row means that it
// lost count, which a transaction taken back without its knowing, or a
// row the file was given behind the engine's back, does. Then the seal
// is worked out again after the event the row follows, when the writer
// knows it, the last committed or the one before a savepoint, and
// written again; else after the last event it wrote, so that the check
// finds the gap.
func (z *sealer) wrote(ctx context.Context, row int64) error {
	if row != z.last.row+1 {
		prev := z.last
		if z.committed.row == row-1 {
			prev = z.committed
		}
		for _, m := range z.marks {
			if m.at.row == row-1 {
				prev = m.at
			}
		}
		z.sum(&prev.seal)
		if z.update != nil {
			if _, err := z.update.ExecContext(ctx, z.boxes[z.lists], row); err != nil {
				return storageErr("seal the event", err)
			}
		}
	}
	z.last.row = row
	copy(z.last.seal[:], z.out[:sealSize])
	z.open = false
	return nil
}

// sealText appends a text with its length.
func sealText(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// sealValue appends a value as the table keeps it: a whole number, and
// a number with no fraction, which SQLite may keep either way, as an
// integer; another number by its bits; a text, or bytes, with its
// length. The check reads each back in the same form.
func sealValue(b []byte, v any) []byte {
	switch x := v.(type) {
	case int64:
		return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(x))
	case int:
		return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(x))
	case int32:
		return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(x))
	case bool:
		var n uint64
		if x {
			n = 1
		}
		return binary.LittleEndian.AppendUint64(append(b, 'i'), n)
	case float64:
		if x == math.Trunc(x) && x >= -(1<<63) && x < 1<<63 {
			return binary.LittleEndian.AppendUint64(append(b, 'i'), uint64(int64(x)))
		}
		return binary.LittleEndian.AppendUint64(append(b, 'f'), math.Float64bits(x))
	case string:
		return sealText(append(b, 's'), x)
	case []byte:
		b = binary.LittleEndian.AppendUint32(append(b, 'b'), uint32(len(x)))
		return append(b, x...)
	case nil:
		return append(b, 'n')
	}
	// A kind of value the engine does not write: it cannot match what
	// the check reads back, which says so.
	return append(b, '?')
}

// loadSeal reads the last event of the log and its seal, from which the
// writer goes on.
func (s *Store) loadSeal(ctx context.Context) error {
	s.seal.ready()
	var row int64
	var seal []byte
	err := s.writer.QueryRowContext(ctx, `SELECT rowid, seal FROM events ORDER BY rowid DESC LIMIT 1`).Scan(&row, &seal)
	switch {
	case err == sql.ErrNoRows:
		return nil
	case err != nil:
		return storageErr("read the seal of the log", err)
	}
	s.seal.committed.row = row
	if len(seal) >= sealSize {
		copy(s.seal.committed.seal[:], seal[:sealSize])
	}
	s.seal.last = s.seal.committed
	return nil
}

// beginWriter begins a transaction on the writer: every write of the
// store begins here, so that the seal knows that no event of the
// transaction is written yet.
func (s *Store) beginWriter(ctx context.Context, stmt string) error {
	if _, err := s.writer.ExecContext(ctx, stmt); err != nil {
		return err
	}
	s.seal.begin()
	return nil
}

// savepoint sets a savepoint on the writer, back to which the writer
// may go (backTo) or which it releases (releasePoint).
func (s *Store) savepoint(ctx context.Context, name string) error {
	if _, err := s.writer.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return err
	}
	s.seal.mark(name)
	return nil
}

// backTo takes the transaction back to the savepoint, which stays.
func (s *Store) backTo(ctx context.Context, name string) error {
	if _, err := s.writer.ExecContext(ctx, "ROLLBACK TO "+name); err != nil {
		return err
	}
	s.seal.toMark(name)
	return nil
}

// releasePoint releases the savepoint.
func (s *Store) releasePoint(ctx context.Context, name string) error {
	if _, err := s.writer.ExecContext(ctx, "RELEASE "+name); err != nil {
		return err
	}
	s.seal.release(name)
	return nil
}

// markMany, backToMany and releaseMany are the savepoint before each
// document of a batch, with statements prepared once.
func (s *Store) markMany(ctx context.Context) error {
	if _, err := s.stmts.mark.ExecContext(ctx); err != nil {
		return err
	}
	s.seal.mark("many")
	return nil
}

func (s *Store) backToMany(ctx context.Context) error {
	if _, err := s.stmts.toMark.ExecContext(ctx); err != nil {
		return err
	}
	s.seal.toMark("many")
	return nil
}

func (s *Store) releaseMany(ctx context.Context) error {
	if _, err := s.stmts.unmark.ExecContext(ctx); err != nil {
		return err
	}
	s.seal.release("many")
	return nil
}
