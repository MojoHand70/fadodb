package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// A commit (ADR-0015) is computed first and written last, in one
// transaction on the writer connection, so that Preview is the same
// pipeline with the write step replaced by a rollback (ADR-0025):
//
//  1. the idempotency key, when there is one, is looked up (ADR-0026);
//  2. the current row is read for a set or a delete;
//  3. the assignments are applied, or the defaults for a new document;
//  4. the auto variables whose dependencies changed recalculate in the
//     class's order, and the title with them;
//  5. the current row is upserted, the previous history row closed, the
//     new history row and the child rows written, the event appended.

type op uint8

const (
	opCreate op = iota
	opSet
	opDelete
)

func (o op) String() string { return [...]string{"create", "set", "delete"}[o] }

type command struct {
	op      op
	c       *class
	id      string
	values  Values
	o       options
	preview bool
}

type result struct {
	doc     *Document
	eventID string
	noop    bool
	errs    []*Error
}

// Create makes a document: the values given, the defaults of the inputs
// not given, every auto variable computed, version 1.
func (s *Store) Create(ctx context.Context, class string, values Values, opts ...Option) (*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	res, err := s.commit(ctx, command{op: opCreate, c: c, id: o.id, values: values, o: o})
	if err != nil {
		return nil, err
	}
	return res.doc, nil
}

// Set assigns values to a document and recalculates what depends on
// them, as a new version. A set that changes nothing commits nothing and
// returns the document as it is.
func (s *Store) Set(ctx context.Context, class, id string, values Values, opts ...Option) (*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	res, err := s.commit(ctx, command{op: opSet, c: c, id: id, values: values, o: apply(opts)})
	if err != nil {
		return nil, err
	}
	return res.doc, nil
}

// Delete removes a document's current row and appends the event; its
// history stays (ADR-0014).
func (s *Store) Delete(ctx context.Context, class, id string, opts ...Option) error {
	c, e := s.class(class)
	if e != nil {
		return e
	}
	_, err := s.commit(ctx, command{op: opDelete, c: c, id: id, o: apply(opts)})
	return err
}

// Preview runs a create (id empty) or a set through the commit pipeline
// and rolls back (ADR-0025). Transitions arrive with Phase 3.
func (s *Store) Preview(ctx context.Context, class, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	if transition != "" {
		return nil, errf(CodeNotSupported, "", "previewing a transition arrives with the statechart (Phase 3)")
	}
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	cmd := command{op: opSet, c: c, id: id, values: values, o: o, preview: true}
	if id == "" {
		cmd.op, cmd.id = opCreate, o.id
	}
	res, err := s.commit(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &Preview{Document: res.doc, Errors: res.errs}, nil
}

// commit runs a command in a transaction on the writer.
func (s *Store) commit(ctx context.Context, cmd command) (*result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	begin := "BEGIN IMMEDIATE"
	if cmd.preview {
		begin = "BEGIN"
	}
	if _, err := s.writer.ExecContext(ctx, begin); err != nil {
		return nil, storageErr("begin", err)
	}
	res, err := s.run(ctx, cmd, now)
	if err != nil || cmd.preview || res.noop {
		if _, rerr := s.writer.ExecContext(ctx, "ROLLBACK"); rerr != nil && err == nil {
			return nil, storageErr("rollback", rerr)
		}
		return res, err
	}
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		return nil, storageErr("commit", err)
	}
	return res, nil
}

func (s *Store) run(ctx context.Context, cmd command, now time.Time) (*result, error) {
	c := cmd.c
	secs := now.Unix()
	res := &result{}

	// The idempotency key (ADR-0026).
	var hash string
	if cmd.o.key != "" {
		hash = commandHash(cmd)
		var kind, class, docID string
		var after sql.NullInt64
		var stored sql.NullString
		err := s.stmts.lookupKey.QueryRowContext(ctx, cmd.o.key).Scan(&kind, &class, &docID, &after, &stored)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return nil, storageErr("look up the command key", err)
		case stored.String != hash:
			return nil, hintf(CodeKeyReused, "", "the key "+cmd.o.key+" was used by a different command (ADR-0026)", "a key names one command; a new command takes a new key")
		default:
			res.noop = true
			if kind != EventDeleted {
				row, err := s.Version(ctx, class, docID, after.Int64)
				if err != nil {
					return nil, err
				}
				res.doc = &row.Document
			}
			return res, nil
		}
	}

	var before *docRow
	if cmd.op != opCreate {
		var err error
		if before, err = c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, cmd.id), false); err != nil {
			if err == sql.ErrNoRows {
				return nil, errf(CodeUnknownDocument, "", "no document %s of class %s", cmd.id, c.cls.Name)
			}
			return nil, storageErr("read the document", err)
		}
	}
	eventID := s.ids.next(now)
	actor := cmd.o.principal.ID
	day := businessDay(cmd.o, now)
	if cmd.op == opDelete {
		if err := s.writeDelete(ctx, c, before, secs, eventID, actor, cmd.o.key, hash, day); err != nil {
			return nil, err
		}
		res.eventID = eventID
		return res, nil
	}

	kids := map[*child][]childRow{}
	dirty := s.dirtyFor(c)
	evalCtx := &formula.Context{User: actor, Groups: cmd.o.principal.Groups, Now: secs}
	var after *docRow
	if cmd.op == opCreate {
		id := cmd.id
		if id == "" {
			id = s.ids.next(now)
		} else {
			var n int64
			if err := s.writer.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(c.history)+` WHERE "id" = ?`, id).Scan(&n); err != nil {
				return nil, storageErr("check the id", err)
			}
			if n > 0 {
				return nil, errf(CodeDuplicate, "", "a document %s of class %s exists, or existed", id, c.cls.Name)
			}
		}
		after = &docRow{id: id, version: 1, createdAt: secs, updatedAt: secs, vals: make([]formula.Value, len(c.leaves))}
		if c.cls.States != nil {
			after.state, after.stateEntered = c.cls.States.Initial, secs
		}
		if _, err := c.assign(after, kids, cmd.values, dirty, s.limits); err != nil {
			return nil, err
		}
		if err := s.checkFiles(ctx, c, after, dirty, kids); err != nil {
			return nil, err
		}
		for _, cv := range c.defaults {
			if dirty[cv.idx] {
				continue
			}
			val, err := s.eval(c, cv.v.Default, after, evalCtx)
			if err == nil {
				val, err = fit(cv.v, val)
			}
			if err != nil {
				err.Variable = cv.v.Name
				if !cmd.preview {
					return nil, err
				}
				res.errs = append(res.errs, err)
				continue
			}
			after.vals[cv.leaf] = val
			dirty[cv.idx] = true
		}
		for _, cv := range c.vars {
			if v := cv.v; !v.Computed && v.Required && cv.leaf >= 0 && after.vals[cv.leaf].IsBlank() {
				err := errf(CodeRequired, v.Name, "%s is required", v.Name)
				if !cmd.preview {
					return nil, err
				}
				res.errs = append(res.errs, err)
			}
		}
		if err := s.recalc(c, after, dirty, true, evalCtx, cmd.preview, &res.errs); err != nil {
			return nil, err
		}
	} else {
		after = before.clone()
		changed, err := c.assign(after, kids, cmd.values, dirty, s.limits)
		if err != nil {
			return nil, err
		}
		if err := s.checkFiles(ctx, c, after, dirty, kids); err != nil {
			return nil, err
		}
		for ch, rows := range kids {
			prev, err := ch.load(ctx, ch.selectRows, before.id, before.version)
			if err != nil {
				return nil, err
			}
			if !sameRows(prev, rows) {
				changed = true
			}
		}
		if !changed {
			all, err := c.loadChildren(ctx, before.id, before.version, kids, true)
			if err != nil {
				return nil, err
			}
			res.noop = true
			res.doc = c.document(before, all)
			return res, nil
		}
		after.version++
		after.updatedAt = secs
		if err := s.recalc(c, after, dirty, false, evalCtx, cmd.preview, &res.errs); err != nil {
			return nil, err
		}
	}

	all := kids
	if before != nil {
		var err error
		if all, err = c.loadChildren(ctx, before.id, before.version, kids, true); err != nil {
			return nil, err
		}
	}
	if cmd.preview {
		res.doc = c.document(after, all)
		return res, nil
	}
	if err := s.write(ctx, cmd, before, after, kids, secs, eventID, actor, hash, day); err != nil {
		return nil, err
	}
	res.doc = c.document(after, all)
	res.eventID = eventID
	return res, nil
}

// eval runs a program over the row.
func (s *Store) eval(c *class, prog *formula.Program, row *docRow, evalCtx *formula.Context) (formula.Value, *Error) {
	idx := c.slots[prog]
	if cap(s.vars) < len(idx) {
		s.vars = make([]formula.Value, c.maxSlots)
	}
	vars := s.vars[:len(idx)]
	for i, li := range idx {
		vars[i] = row.vals[li]
	}
	val, err := s.vm.Run(prog, vars, evalCtx)
	if err != nil {
		return formula.Value{}, evalErr("", err)
	}
	return val, nil
}

// fit brings a computed or defaulted value to the variable's scale and
// applies the commit-time checks.
func fit(v *model.Variable, val formula.Value) (formula.Value, *Error) {
	if val.Kind().IsExact() {
		var e *formula.EvalError
		if val, e = formula.Rescale(val, int(v.Type.Scale)); e != nil {
			return formula.Value{}, &Error{Code: e.Code, Variable: v.Name, Message: e.Msg}
		}
	}
	if err := checkStored(v, val); err != nil {
		return formula.Value{}, err
	}
	return val, nil
}

// recalc runs the auto variables and the title in the class's order,
// all of them or the ones whose dependencies are dirty, marking each one
// dirty in turn. In a preview an error is recorded and the variable left
// blank; in a commit it is the commit's error.
func (s *Store) recalc(c *class, row *docRow, dirty []bool, all bool, evalCtx *formula.Context, preview bool, errs *[]*Error) *Error {
	for _, cv := range c.order {
		prog, deps, name := c.cls.Title, c.titleDeps, "title"
		if cv != nil {
			prog, deps, name = cv.v.Formula, cv.deps, cv.v.Name
		}
		if !all && !anyDirty(deps, dirty) {
			continue
		}
		val, err := s.eval(c, prog, row, evalCtx)
		if err != nil {
			err.Variable = name
			if !preview {
				return err
			}
			*errs = append(*errs, err)
			val = formula.Blank()
		}
		if cv == nil {
			row.title = formula.Render(val)
			continue
		}
		fitted, err := fit(cv.v, val)
		if err != nil {
			if !preview {
				return err
			}
			*errs = append(*errs, err)
			fitted = val
		}
		row.vals[cv.leaf] = fitted
		dirty[cv.idx] = true
	}
	return nil
}

// dirtyFor returns the store's dirty flags, one per variable of the
// class, all false. The slice is reused under the writer's lock.
func (s *Store) dirtyFor(c *class) []bool {
	if cap(s.dirty) < len(c.vars) {
		s.dirty = make([]bool, len(c.vars))
	}
	d := s.dirty[:len(c.vars)]
	for i := range d {
		d[i] = false
	}
	return d
}

func anyDirty(deps []int, dirty []bool) bool {
	for _, d := range deps {
		if dirty[d] {
			return true
		}
	}
	return false
}

func sameRows(a, b []childRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		for j := range a[i] {
			if !formula.Equal(a[i][j], b[i][j]) {
				return false
			}
		}
	}
	return true
}

// write is the write step: the current row, the history rows, the child
// rows and the event.
func (s *Store) write(ctx context.Context, cmd command, before, after *docRow, kids map[*child][]childRow, secs int64, eventID, actor, hash string, day int64) error {
	c := cmd.c
	args := make([]any, 0, len(systemColumns)+len(historyColumns)+len(c.cls.Columns))
	args = append(args, after.id, nullText(after.title), nullText(after.state), after.version, after.createdAt, after.updatedAt, nullInt(after.stateEntered))
	args = c.encodeVals(args, after)
	if _, err := c.w.upsert.ExecContext(ctx, args...); err != nil {
		return storageErr("write the current row", err)
	}
	if before != nil {
		if _, err := c.w.closeHistory.ExecContext(ctx, secs, before.id, before.version); err != nil {
			return storageErr("close the history row", err)
		}
	}
	args = args[:0]
	args = append(args, after.id, nullText(after.title), nullText(after.state), after.version, after.createdAt, after.updatedAt, nullInt(after.stateEntered),
		secs, nil, nullText(actor), eventID)
	args = c.encodeVals(args, after)
	if _, err := c.w.insertHistory.ExecContext(ctx, args...); err != nil {
		return storageErr("write the history row", err)
	}
	for _, ch := range c.children {
		if rows, ok := kids[ch]; ok {
			for i, row := range rows {
				cargs := make([]any, 0, len(childKey)+len(ch.def.Columns))
				cargs = append(cargs, after.id, after.version, int64(i))
				for j, l := range ch.leaves {
					cargs = encode(cargs, l, row[j])
				}
				if _, err := ch.insert.ExecContext(ctx, cargs...); err != nil {
					return storageErr("write "+ch.v.Name, err)
				}
			}
		} else if before != nil {
			if _, err := ch.copyRows.ExecContext(ctx, after.version, after.id, before.version); err != nil {
				return storageErr("carry "+ch.v.Name+" forward", err)
			}
		}
	}
	kind, qualifier := EventSet, "changed"
	var beforeVersion any
	if before != nil {
		beforeVersion = before.version
	}
	if cmd.op == opCreate {
		kind, qualifier = EventCreated, "created"
	}
	return s.writeEvent(ctx, eventID, c.cls.Name, after.id, kind, qualifier, actor, beforeVersion, after.version, cmd.o.key, hash, day, secs)
}

func (s *Store) writeDelete(ctx context.Context, c *class, before *docRow, secs int64, eventID, actor, key, hash string, day int64) error {
	if _, err := c.w.closeHistory.ExecContext(ctx, secs, before.id, before.version); err != nil {
		return storageErr("close the history row", err)
	}
	if _, err := c.w.deleteCurrent.ExecContext(ctx, before.id); err != nil {
		return storageErr("delete the current row", err)
	}
	return s.writeEvent(ctx, eventID, c.cls.Name, before.id, EventDeleted, "deleted", actor, before.version, nil, key, hash, day, secs)
}

func (s *Store) writeEvent(ctx context.Context, eventID, class, docID, kind, qualifier, actor string, before, after any, key, hash string, day, secs int64) error {
	if _, err := s.stmts.insertEvent.ExecContext(ctx, eventID, class, docID, kind, nil, nil, nil, nullText(actor), before, after, nil, nullText(key), nullText(hash), day, secs); err != nil {
		return storageErr("write the event", err)
	}
	if _, err := s.stmts.insertEventDoc.ExecContext(ctx, eventID, class, docID, qualifier); err != nil {
		return storageErr("write the event's documents", err)
	}
	return nil
}

func (c *class) encodeVals(args []any, row *docRow) []any {
	for i, l := range c.leaves {
		args = encode(args, l, row.vals[i])
	}
	return args
}

func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(i int64) any {
	if i == 0 {
		return nil
	}
	return i
}

// businessDay is the event's business date (ADR-0026): the one given,
// else the principal's day, else today in UTC.
func businessDay(o options, now time.Time) int64 {
	day := now.UTC()
	switch {
	case o.hasBusinessDate:
		day = o.businessDate
	case !o.principal.Today.IsZero():
		day = o.principal.Today
	}
	y, m, d := day.Date()
	return formula.Day(y, int(m), d).Days()
}

// commandHash identifies a command's body for the key check of
// ADR-0026: the operation, the class, the document and the values.
func commandHash(cmd command) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", cmd.op, strings.ToUpper(cmd.c.cls.Name), cmd.id)
	if b, err := json.Marshal(cmd.values); err == nil {
		h.Write(b)
	} else {
		fmt.Fprintf(h, "%v", cmd.values)
	}
	return hex.EncodeToString(h.Sum(nil))
}
