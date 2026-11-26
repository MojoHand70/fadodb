package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Removing rows and removing a table (ADR-0082). The owner, 2026-11-23,
// with test rows and two test tables on his demo that no call could take
// away: "Removing rows and tables."

// DeleteRows removes the rows of a table that have the keys given, in one
// transaction (ADR-0082, item 1). Each key is an object with a value for
// every column of the table's key and for no other; a key the table does
// not have removes nothing. It asks the table's write rule, as an import
// does, and a content administrator's delete past it is written down in
// the log of reads, in the same transaction. It returns how many rows it
// removed, and drops the table's rows in memory, as an import does.
func (s *Store) DeleteRows(ctx context.Context, name string, keys []Values, opts ...Option) (int, error) {
	t, e := s.table(name)
	if e != nil {
		return 0, e
	}
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return 0, e
	}
	if err := refuseAt(o, "row delete"); err != nil {
		return 0, err
	}
	past, e := s.tableRefusal(t, true, o, s.clock(o))
	if e != nil {
		return 0, e
	}
	// Every key is read before anything is removed: one that does not
	// name the key whole, or names another column, refuses them all.
	fields := t.keyFields(true)
	vals := make([]formula.Value, len(t.leaves))
	where := make([]string, len(t.def.Key))
	for i, k := range t.def.Key {
		where[i] = quote(t.leaves[t.leafIndex[strings.ToUpper(k)]].column) + " = ?"
	}
	args := make([][]any, len(keys))
	for i, key := range keys {
		for j := range vals {
			vals[j] = formula.Blank()
		}
		if err := setFields(vals, t.leafIndex, "", fields, key, "", s.limits); err != nil {
			err.Variable = fmt.Sprintf("%s[%d].%s", t.def.Name, i, err.Variable)
			if err.Code == CodeUnknownVariable {
				err.Message += "; a key names the columns of the table's key, and no other"
			}
			return 0, err
		}
		for _, k := range t.def.Key {
			li := t.leafIndex[strings.ToUpper(k)]
			if vals[li].IsBlank() {
				return 0, errf(CodeRequired, fmt.Sprintf("%s[%d].%s", t.def.Name, i, k), "a key names every column of the table's key")
			}
			args[i] = append(args[i], encode(nil, t.leaves[li], vals[li])[0])
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errf(CodeClosed, "", "the store is closed")
	}
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, storageErr("begin", err)
	}
	rollback := func(err error) (int, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return 0, err
	}
	stmt := "DELETE FROM " + quote(t.name) + " WHERE " + strings.Join(where, " AND ")
	removed := 0
	for i := range keys {
		res, err := w.ExecContext(ctx, stmt, args[i]...)
		if err != nil {
			return rollback(storageErr("delete a row of "+t.def.Name, err))
		}
		n, _ := res.RowsAffected()
		removed += int(n)
	}
	if past {
		r := &readLog{command: "delete_rows", table: t.def.Name, rules: []string{"/access/write"}, rows: int64(removed), hasRows: true, past: true}
		if err := s.insertRead(ctx, w, o, r); err != nil {
			return rollback(err)
		}
	}
	// The values that follow the table (ADR-0091), queued with the
	// removal.
	followed := 0
	if removed > 0 {
		var err error
		if followed, err = s.queueFollows(ctx, t, o.principal, o.reason, 0, s.now()); err != nil {
			return rollback(err)
		}
	}
	if err := s.endTransaction(ctx); err != nil {
		return rollback(storageErr("commit", err))
	}
	t.cache, t.tooBig = nil, false
	if followed > 0 {
		s.wakeWorker()
	}
	return removed, nil
}

// RemoveTable takes a table away with its rows (ADR-0082, item 2): its
// storage, its view, its definition and its words. It is for a designer,
// as defining one is. A table that a class reads, or that a rule of a
// class writes, is not removed: the class names it first. The versions the
// table had stay in the log of definitions, and a table defined again
// under its name takes the next version after them. The log records who
// removed it and when, on the version it had, and the same for each of
// its words in another language, in the same transaction (ADR-0083).
func (s *Store) RemoveTable(ctx context.Context, name string, opts ...Option) error {
	o, e := s.worker(ctx, workDesign, opts)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	key := strings.ToUpper(name)
	t, ok := s.tables[key]
	if !ok {
		return errf(CodeUnknownTable, "", "unknown table %q", name)
	}
	for _, cname := range s.order {
		c := s.classes[strings.ToUpper(cname)]
		if c.sources[key] {
			return hintf(CodeNotAllowed, "", "the class "+cname+" reads the table "+t.def.Name, "define a version of "+cname+" that no longer reads it, then remove the table")
		}
		if actsOn(c.cls, t.def.Name, true) {
			return hintf(CodeNotAllowed, "", "a rule of the class "+cname+" writes the table "+t.def.Name, "define a version of "+cname+" whose rules no longer write it, then remove the table")
		}
	}
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	rollback := func(err error) error {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return err
	}
	for _, ddl := range t.dropDDL() {
		if _, err := w.ExecContext(ctx, ddl); err != nil {
			return rollback(storageErr("drop the table "+t.def.Name, err))
		}
	}
	if _, err := w.ExecContext(ctx, "DELETE FROM tables_registry WHERE name = ? COLLATE NOCASE", t.def.Name); err != nil {
		return rollback(storageErr("remove the table "+t.def.Name, err))
	}
	now := s.now().Unix()
	if err := recordRemoval(ctx, w, DefinitionTable, t.def.Name, t.def.Version, o.principal, now); err != nil {
		return rollback(err)
	}
	// Its words go with it, as remove_words takes them away: their
	// versions stay, and each that was still in use records its removal
	// as remove_words does.
	var words []string
	for _, k := range s.wordsOrder {
		if wd := s.words[k]; wd.kind == model.WordsOfTable && strings.EqualFold(wd.target, t.def.Name) && !wd.removed {
			words = append(words, k)
			if err := recordRemoval(ctx, w, DefinitionWords, wd.name(), wd.version, o.principal, now); err != nil {
				return rollback(err)
			}
		}
	}
	if _, err := w.ExecContext(ctx, "UPDATE words SET removed = 1, updated_at = ? WHERE kind = ? AND target = ? COLLATE NOCASE", now, model.WordsOfTable, t.def.Name); err != nil {
		return rollback(storageErr("remove the words of "+t.def.Name, err))
	}
	if err := s.endTransaction(ctx); err != nil {
		return rollback(storageErr("commit", err))
	}
	for _, k := range words {
		gone := *s.words[k]
		gone.removed = true
		s.words[k] = &gone
	}
	s.defined++
	t.close()
	s.closePlans()
	delete(s.tables, key)
	for i, n := range s.tableOrder {
		if strings.EqualFold(n, t.def.Name) {
			s.tableOrder = append(s.tableOrder[:i:i], s.tableOrder[i+1:]...)
			break
		}
	}
	return nil
}
