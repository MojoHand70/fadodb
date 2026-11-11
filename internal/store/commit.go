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
	"github.com/MojoHand70/fadodb/internal/state"
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
//  5. the guard of a named transition is evaluated and the state changes
//     (ADR-0040);
//  6. the triggered variables whose trigger the commit raised recompute
//     in declaration order (ADR-0043);
//  7. the rules whose triggers the commit raised run in order, and their
//     synchronous actions change the document;
//  8. the eventless transitions are followed from the resulting state,
//     one at a time, each a commit of its own that repeats 6 and 7;
//  9. the current row is upserted, the previous history row closed, the
//     new history row and the child rows written, the event appended and
//     the custom events after it; and once more for every eventless
//     transition that fired.

type op uint8

const (
	opCreate op = iota
	opSet
	opDelete
	opMigrate
	opTransition
	opReset
	opRefresh
)

func (o op) String() string {
	return [...]string{"create", "set", "delete", "migrate", "transition", "reset", "refresh"}[o]
}

type command struct {
	op     op
	c      *class
	id     string
	values Values
	// transition names the transition of an opTransition.
	transition string
	// variables are the inputs an opReset resets, or the triggered
	// variables an opRefresh recomputes, none meaning every one that
	// lists refresh.
	variables []string
	o         options
	preview   bool
	// stored is the class the document is read from when c is a draft
	// compiled for one preview (ADR-0025); nil otherwise.
	stored *class
	// The worker's commands (ADR-0044): internal marks one, whose key may
	// carry the outbox prefix; cause is the event of the commit that
	// queued the action, which the command's own event names; depth is
	// the depth the actions this command queues will have; and day, when
	// hasDay, the business date of the causing event, which the action's
	// commit takes.
	internal bool
	cause    string
	depth    int
	day      int64
	hasDay   bool
	// engine marks a write of the engine's own: the result an action
	// writes back to the document that caused it (ADR-0045, item 8). Like
	// a rule's set it consults no write right; the principal is the one
	// whose command caused the action, for the record and for the actions
	// the write queues in its turn.
	engine bool
}

type result struct {
	doc     *Document
	eventID string
	noop    bool
	errs    []*Error
	// queued is how many actions the command wrote to the outbox.
	queued int
	// path and transitions are a preview's: the states the command
	// visited and the named transitions leaving the last one; now the
	// seconds its formulas saw (ADR-0042); rules the rules whose triggers
	// it raised (ADR-0043).
	path        []string
	transitions []TransitionInfo
	rules       []RuleRun
	now         int64
}

// eventInfo is what the event of a commit says beyond its versions.
type eventInfo struct {
	kind, name, from, to, cause string
	// origin says how an entry into a state came about (ADR-0047, item
	// 3); empty for an event that is no entry.
	origin string
}

// How an entry into a state came about (ADR-0047, item 3): somebody asked
// for it, by creating the document or taking a named transition; a rule
// of a document asked for it through the outbox; an eventless transition
// fired in the chain of a commit; or a timed transition did.
const (
	OriginCommand = "command"
	OriginAction  = "action"
	OriginGuard   = "guard"
	OriginTimer   = "timer"
)

// origins lists the origins by the number the events table keeps each
// under; 0 is none.
var origins = []string{"", OriginCommand, OriginAction, OriginGuard, OriginTimer}

// originCode is the number an origin is stored as, nil for none.
func originCode(origin string) any {
	for i, o := range origins {
		if o == origin && i > 0 {
			return i
		}
	}
	return nil
}

// originOf is the origin a stored number stands for.
func originOf(code int64) string {
	if code > 0 && int(code) < len(origins) {
		return origins[code]
	}
	return ""
}

// eventFor is the event of a commit that moves no state.
func eventFor(o op) eventInfo {
	switch o {
	case opCreate:
		return eventInfo{kind: EventCreated}
	case opMigrate:
		return eventInfo{kind: EventMigrated}
	case opReset:
		return eventInfo{kind: EventReset}
	case opRefresh:
		return eventInfo{kind: EventRefresh}
	}
	return eventInfo{kind: EventSet}
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

// Preview runs a create (id empty), a set or a transition through the
// commit pipeline and rolls back (ADR-0025): the document as it would be,
// the states visited and the transitions leaving the resulting state.
func (s *Store) Preview(ctx context.Context, class, id string, values Values, transition string, opts ...Option) (*Preview, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	cmd := command{op: opSet, c: c, id: id, values: values, o: o, preview: true}
	switch {
	case transition != "":
		if id == "" {
			return nil, hintf(CodeBadValue, "", "a transition moves an existing document; no id was given", "create the document first, or preview the create alone")
		}
		cmd.op, cmd.transition = opTransition, transition
	case id == "":
		cmd.op, cmd.id = opCreate, o.id
	}
	res, err := s.commit(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &Preview{Document: res.doc, Errors: res.errs, Path: res.path, Transitions: res.transitions, Rules: res.rules, Now: res.now}, nil
}

// commit runs a command in a transaction on the writer.
func (s *Store) commit(ctx context.Context, cmd command) (*result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	if !cmd.preview {
		if err := refuseAt(cmd.o, cmd.op.String()); err != nil {
			return nil, err
		}
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
	if res.queued > 0 {
		s.wakeWorker()
	}
	return res, nil
}

func (s *Store) run(ctx context.Context, cmd command, now time.Time) (*result, error) {
	c := cmd.c
	secs := now.Unix()
	// The formulas' clock is the moment a preview chose, else the
	// commit's; the timestamps a commit writes are always the commit's
	// (ADR-0042).
	clock := secs
	if !cmd.o.at.IsZero() {
		clock = cmd.o.at.Unix()
	}
	res := &result{now: clock}

	// A class without states is a stateless application and stores no
	// documents (ADR-0040); the previews evaluate it.
	if cmd.op == opCreate && c.cls.States == nil && !cmd.preview {
		return nil, hintf(CodeStateless, "", c.cls.Name+" declares no states: it is a stateless application and stores no documents", "add a states block with an initial state, or evaluate it with Preview")
	}

	// The idempotency key (ADR-0026). The keys of the worker's own commits
	// have a prefix a caller may not use (ADR-0044).
	var hash string
	if !cmd.internal && strings.HasPrefix(cmd.o.key, outboxKeyPrefix) {
		return nil, hintf(CodeBadValue, "", "a key may not start with "+outboxKeyPrefix+", the prefix of the commits the outbox makes", "choose another key")
	}
	if cmd.o.key != "" {
		hash = commandHash(cmd)
		var kind, class, docID, keyed string
		var after sql.NullInt64
		var stored sql.NullString
		err := s.stmts.lookupKey.QueryRowContext(ctx, cmd.o.key).Scan(&kind, &class, &docID, &after, &stored, &keyed)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return nil, storageErr("look up the command key", err)
		case stored.String != hash:
			return nil, hintf(CodeKeyReused, "", "the key "+cmd.o.key+" was used by a different command (ADR-0026)", "a key names one command; a new command takes a new key")
		default:
			res.noop = true
			if kind != EventDeleted {
				// A command whose chain made further versions is answered
				// with the last of them (ADR-0040).
				var last sql.NullInt64
				if err := s.stmts.lastOfCommand.QueryRowContext(ctx, keyed).Scan(&last); err != nil {
					return nil, storageErr("look up the command's versions", err)
				}
				if last.Valid {
					after = last
				}
				vc, vrow, err := s.version(ctx, c, docID, after.Int64)
				if err != nil {
					return nil, err
				}
				h, err := vc.historyRow(ctx, vrow)
				if err != nil {
					return nil, err
				}
				s.project(&s.wev, c, vc, vrow, &h.Document, cmd.o.principal, clock)
				res.doc = &h.Document
			}
			return res, nil
		}
	}

	var before *docRow
	var beforeClass *class // the version that computed the current row
	var trans *model.Transition
	if cmd.op != opCreate {
		reader := c
		if cmd.stored != nil {
			reader = cmd.stored
		}
		var err error
		if before, err = reader.scanW(reader.w.selectCurrent.QueryRowContext(ctx, cmd.id), false); err != nil {
			if err == sql.ErrNoRows {
				return nil, errf(CodeUnknownDocument, "", "no document %s of class %s", cmd.id, reader.cls.Name)
			}
			return nil, storageErr("read the document", err)
		}
		if cmd.op == opTransition {
			var e *Error
			if trans, e = resolveTransition(c, cmd.transition, before.state); e != nil {
				return nil, e
			}
		}
		// A document in a terminal state is frozen (ADR-0039): it may only
		// move to another terminal state, with no values. A draft preview
		// computes what a definition would make of it and writes nothing,
		// so it may look.
		if cmd.stored == nil && c.cls.States.IsFinal(before.state) {
			if cmd.op != opTransition {
				return nil, hintf(CodeTerminal, "", "the document is in the terminal state "+before.state+" and cannot change", "a terminal document keeps its values and its class version; it may only move to another terminal state")
			}
			if len(cmd.values) > 0 {
				return nil, hintf(CodeTerminal, "", "the document is in the terminal state "+before.state+" and its values cannot change", "a terminal document only moves: take the transition without values")
			}
		}
		beforeClass = reader
		if before.classVersion != reader.cls.Version && before.classVersion != 0 {
			old := s.classAt(reader.cls.Name, before.classVersion)
			if old == nil {
				return nil, errf(CodeStorage, "", "the document %s was computed by version %d of %s, which the store does not have", before.id, before.classVersion, c.cls.Name)
			}
			if before, err = old.scanW(old.w.selectCurrent.QueryRowContext(ctx, cmd.id), false); err != nil {
				return nil, storageErr("read the document under its class version", err)
			}
			beforeClass = old
		}
	}
	eventID := s.ids.next(now)
	actor := cmd.o.principal.ID
	day := businessDay(cmd.o, now)
	if cmd.hasDay {
		day = cmd.day
	}
	evalCtx := &formula.Context{User: actor, Groups: cmd.o.principal.Groups, Now: clock}
	s.emits = s.emits[:0]
	s.queued = s.queued[:0]
	rules := ruleRun{c: c, preview: cmd.preview, res: res, evalCtx: evalCtx, secs: secs}
	if cmd.op == opDelete {
		// A delete rule sees the document as it was (ADR-0043), under the
		// current definition's layout; it may emit and queue, nothing else.
		if c.rules != nil && len(c.rules.delete) > 0 {
			row := before
			if beforeClass != c {
				row = c.adopted(beforeClass, before)
			}
			evalCtx.ID = before.id
			if len(c.rules.lists) > 0 {
				if err := s.loadLists(ctx, c, nil, before, nil); err != nil {
					return nil, err
				}
			}
			if err := s.fireCommit(&rules, row, commitDelete, "", "", nil); err != nil {
				return nil, err
			}
		}
		if err := s.writeDelete(ctx, c, before, secs, eventID, actor, cmd.o.key, hash, day); err != nil {
			return nil, err
		}
		if len(s.emits) > 0 {
			if err := s.writeEmits(ctx, c, 0, before.id, before.version, nil, eventID, actor, day, secs, now); err != nil {
				return nil, err
			}
		}
		if len(s.queued) > 0 {
			n, err := s.writeQueued(ctx, c, &cmd, 0, eventID, before.id, before.version, day, secs, now)
			if err != nil {
				return nil, err
			}
			res.queued += n
		}
		res.eventID = eventID
		return res, nil
	}

	kids := map[*child][]childRow{}
	dirty := s.dirtyFor(c)
	changedKids := s.changedKids[:0]
	unchanged := false
	var after *docRow
	if cmd.op == opCreate {
		id := cmd.id
		if id == "" {
			id = s.ids.next(now)
		} else if cmd.stored == nil && c.w.selectCurrent != nil {
			var n int64
			if err := s.writer.QueryRowContext(ctx, "SELECT count(*) FROM "+quote(c.history)+` WHERE "id" = ?`, id).Scan(&n); err != nil {
				return nil, storageErr("check the id", err)
			}
			if n > 0 {
				return nil, errf(CodeDuplicate, "", "a document %s of class %s exists, or existed", id, c.cls.Name)
			}
		}
		after = &docRow{id: id, version: 1, classVersion: c.cls.Version, vals: make([]formula.Value, len(c.leaves)),
			life: formula.Life{Version: 1, CreatedAt: secs, UpdatedAt: secs, CreatedBy: actor, UpdatedBy: actor}}
		if len(c.triggered) > 0 {
			after.ats = make([]int64, len(c.triggered))
		}
		if c.cls.States != nil {
			after.state, after.life.EnteredAt, after.life.EnteredBy = c.cls.States.Initial, secs, actor
		}
		evalCtx.ID, evalCtx.State = after.id, after.state
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
		// The write rights, over the new document after its defaults, in
		// the initial state (ADR-0041).
		if err := s.checkWrites(c, &cmd, after, evalCtx, res); err != nil {
			return nil, err
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
		// A live document computed by an older version adopts the current
		// one with this commit: its values move to the current layout by
		// name, every formula is recomputed, and the row says so
		// (ADR-0039). A migration is that and nothing else.
		adopt := beforeClass != c || cmd.op == opMigrate
		evalCtx.ID, evalCtx.State = before.id, before.state
		if beforeClass != c {
			after = c.adopted(beforeClass, before)
		} else {
			after = before.clone()
		}
		after.classVersion = c.cls.Version
		// The transition's access rule, then the write rights, over the
		// stored document before the assignments, in the state it is in
		// (ADR-0041): the rules say who may change this document.
		if trans != nil {
			if err := s.checkTransition(c, trans, after, evalCtx, cmd.o.principal); err != nil {
				return nil, err
			}
		}
		// The variables a Reset or a Refresh names (ADR-0043, item 10).
		var resets, refreshes []*cvar
		switch cmd.op {
		case opReset:
			var e *Error
			if resets, e = c.resetTargets(cmd.variables); e != nil {
				return nil, e
			}
		case opRefresh:
			var e *Error
			if refreshes, e = c.refreshTargets(cmd.variables); e != nil {
				return nil, e
			}
		}
		if err := s.checkWrites(c, &cmd, after, evalCtx, res); err != nil {
			return nil, err
		}
		changed, err := c.assign(after, kids, cmd.values, dirty, s.limits)
		if err != nil {
			return nil, err
		}
		if cmd.op == opReset {
			// The caller's write, like the assignments of a set: the
			// defaults re-evaluated, the rest blank, a composite empty.
			s.emptied = s.emptied[:0]
			touched, err := s.resetVars(c, after, resets, c.defaultsAmong(resets), evalCtx, dirty, nil, nil)
			if err != nil {
				return nil, err
			}
			changed = changed || touched
			for _, ch := range s.emptied {
				kids[ch] = nil
			}
		}
		if err := s.checkFiles(ctx, c, after, dirty, kids); err != nil {
			return nil, err
		}
		for ch, rows := range kids {
			if ch.selectRows == nil {
				// A draft has no statements; it adopts anyway, and a
				// composite it was given counts as changed.
				changedKids = append(changedKids, ch)
				continue
			}
			prev, err := ch.load(ctx, ch.selectRows, before.id, before.version)
			if err != nil {
				return nil, err
			}
			if !sameRows(prev, rows) {
				changed = true
				changedKids = append(changedKids, ch)
			}
		}
		s.changedKids = changedKids
		// A refresh always commits: the moment of the value is what it
		// changes.
		unchanged = !changed && !adopt && cmd.op != opTransition && cmd.op != opRefresh
		if unchanged && !cmd.preview {
			all, err := c.loadChildren(ctx, before.id, before.version, kids, true)
			if err != nil {
				return nil, err
			}
			res.noop = true
			res.doc = c.document(before, all)
			s.project(&s.wev, c, c, before, res.doc, cmd.o.principal, clock)
			return res, nil
		}
		// A preview of a set that changes nothing answers what a commit
		// at this moment would do (ADR-0042): the eventless transitions
		// from the document's state and the listing, the version as it
		// is.
		if !unchanged {
			after.version++
			after.life.Version, after.life.UpdatedAt, after.life.UpdatedBy = after.version, secs, actor
		}
		if err := s.recalc(c, after, dirty, adopt, evalCtx, cmd.preview, &res.errs); err != nil {
			return nil, err
		}
		// The triggered variables a Refresh names, in declaration order,
		// each with the moment of this commit.
		for _, cv := range refreshes {
			if err := s.recompute(c, cv, after, evalCtx, secs, cmd.preview, &res.errs); err != nil {
				return nil, err
			}
		}
	}

	// The statechart (ADR-0040): the named transition's guard over the
	// recalculated document, with STATE() the state it leaves. A migration
	// moves nothing.
	path := s.visits[:0]
	if c.cls.States != nil {
		path = append(path, after.state)
	}
	ev := eventFor(cmd.op)
	ev.cause = cmd.cause
	from := after.state
	if trans != nil {
		evalCtx.State = from
		if err := s.guard(c, trans, after, evalCtx); err != nil {
			return nil, err
		}
		ev = eventInfo{kind: EventTransition, name: trans.Name, from: from, to: trans.To, cause: cmd.cause}
		// Any transition is an entry, one back into the same state too
		// (ADR-0047, item 3). The stay it ends is kept for the rules of
		// the exit and of the transition.
		after.ended = stay{after.life.EnteredAt, after.life.EnteredBy, after.life.EnteredFrom}
		after.state, after.life.EnteredAt, after.life.EnteredBy, after.life.EnteredFrom = trans.To, secs, actor, from
		path = append(path, after.state)
	}

	// The triggered variables whose trigger the command's own commit
	// raised (ADR-0043), after the state change and before the rules: the
	// entry of the initial state for a create, and exit, transition and
	// entry for a transition. A set raises none of their triggers.
	if c.triggered != nil && (cmd.op == opCreate || cmd.op == opTransition) {
		kind, name := commitCreate, ""
		if trans != nil {
			kind, name = commitTransition, trans.Name
		}
		if err := s.recomputeFor(c, after, kind, from, name, evalCtx, secs, cmd.preview, &res.errs); err != nil {
			return nil, err
		}
	}

	// The lists the class's creates with each go over (ADR-0044), as the
	// command leaves them, for the rules of every commit that follows.
	if c.rules != nil && len(c.rules.lists) > 0 && cmd.op != opMigrate {
		if err := s.loadLists(ctx, c, cmd.stored, before, kids); err != nil {
			return nil, err
		}
	}

	// The rules of the command's own commit (ADR-0043), over the document
	// as computed so far. A preview of a set that changes nothing is no
	// commit and raises nothing; a migration raises nothing either.
	if c.rules != nil && !unchanged && cmd.op != opMigrate {
		var err *Error
		switch cmd.op {
		case opCreate:
			err = s.fireCommit(&rules, after, commitCreate, "", "", nil)
		case opTransition:
			err = s.fireCommit(&rules, after, commitTransition, from, trans.Name, s.changesFor(c, beforeClass, before, after, changedKids))
		default:
			err = s.fireCommit(&rules, after, commitSet, "", "", s.changesFor(c, beforeClass, before, after, changedKids))
		}
		if err != nil {
			return nil, err
		}
		for _, ch := range s.emptied {
			kids[ch] = nil
		}
	}

	// The eventless chain, from the state the commit leaves the document
	// in: every transition that fires is a further commit, the values the
	// rules of the commit before left, the new state, and its own rules.
	hops := s.hops[:0]
	if c.cls.States != nil && cmd.op != opMigrate {
		cur := after
		for n := 0; ; n++ {
			evalCtx.State = cur.state
			t := state.Next(c.cls, cur.state, func(t *model.Transition) bool {
				held, _ := s.holds(c, t, cur, evalCtx)
				return held
			})
			if t == nil {
				break
			}
			if n == state.MaxEventless {
				s.visits, s.hops = path, hops
				return nil, hintf(CodeTooManyTransitions, "", fmt.Sprintf("more than %d eventless transitions fired after the %s", state.MaxEventless, cmd.op),
					"the states visited: "+strings.Join(path, " > ")+"; a guard keeps holding, or guards hold in turn")
			}
			next := cur.clone()
			next.version++
			next.life.Version, next.life.UpdatedAt, next.life.UpdatedBy = next.version, secs, actor
			next.ended = stay{cur.life.EnteredAt, cur.life.EnteredBy, cur.life.EnteredFrom}
			next.life.EnteredFrom = cur.state
			next.state, next.life.EnteredAt, next.life.EnteredBy = t.To, secs, actor
			path = append(path, next.state)
			h := hop{row: next}
			if c.triggered != nil {
				if err := s.recomputeFor(c, next, commitHop, cur.state, "", evalCtx, secs, cmd.preview, &res.errs); err != nil {
					s.visits, s.hops = path, hops
					return nil, err
				}
			}
			if c.rules != nil {
				rules.commit = n + 1
				if err := s.fireCommit(&rules, next, commitHop, cur.state, "", nil); err != nil {
					s.visits, s.hops = path, hops
					return nil, err
				}
				if len(s.emptied) > 0 {
					h.kids = make(map[*child][]childRow, len(s.emptied))
					for _, ch := range s.emptied {
						h.kids[ch] = nil
					}
				}
			}
			hops = append(hops, h)
			cur = next
		}
		s.visits, s.hops = path, hops
	}

	all := kids
	if before != nil {
		var err error
		if cmd.stored != nil {
			all, err = s.draftChildren(ctx, c, beforeClass, before, kids)
		} else {
			all, err = c.loadChildren(ctx, before.id, before.version, kids, true)
		}
		if err != nil {
			return nil, err
		}
	}
	if cmd.preview {
		final := after
		for _, h := range hops {
			final = h.row
			for ch := range h.kids {
				all[ch] = nil
			}
		}
		res.doc = c.document(final, all)
		if c.cls.States != nil {
			res.path = append([]string(nil), path...)
			evalCtx.State = final.state
			res.transitions = s.listTransitions(c, final, evalCtx, cmd.o.principal)
		}
		s.project(&s.wev, c, c, final, res.doc, cmd.o.principal, clock)
		return res, nil
	}
	if ev.kind == EventCreated || ev.kind == EventTransition {
		// The log says how the entry came about (ADR-0047, item 3).
		ev.origin = OriginCommand
		if cmd.internal {
			ev.origin = OriginAction
		}
	}
	if err := s.write(ctx, c, before, after, kids, secs, eventID, actor, ev, cmd.o.key, hash, day); err != nil {
		return nil, err
	}
	if len(s.emits) > 0 {
		if err := s.writeEmits(ctx, c, 0, after.id, nil, after.version, eventID, actor, day, secs, now); err != nil {
			return nil, err
		}
	}
	if len(s.queued) > 0 {
		n, err := s.writeQueued(ctx, c, &cmd, 0, eventID, after.id, after.version, day, secs, now)
		if err != nil {
			return nil, err
		}
		res.queued += n
	}
	// Every eventless transition that fired is a further commit: a version
	// and a history row of its own, and a transition event with no name
	// caused by the command's event.
	prev := after
	for i, h := range hops {
		hopEvent := s.ids.next(now)
		if err := s.write(ctx, c, prev, h.row, h.kids, secs, hopEvent, actor, eventInfo{kind: EventTransition, from: prev.state, to: h.row.state, cause: eventID, origin: OriginGuard}, "", "", day); err != nil {
			return nil, err
		}
		if len(s.emits) > 0 {
			if err := s.writeEmits(ctx, c, i+1, h.row.id, nil, h.row.version, hopEvent, actor, day, secs, now); err != nil {
				return nil, err
			}
		}
		if len(s.queued) > 0 {
			n, err := s.writeQueued(ctx, c, &cmd, i+1, hopEvent, h.row.id, h.row.version, day, secs, now)
			if err != nil {
				return nil, err
			}
			res.queued += n
		}
		for ch := range h.kids {
			all[ch] = nil
		}
		prev = h.row
	}
	res.doc = c.document(prev, all)
	s.project(&s.wev, c, c, prev, res.doc, cmd.o.principal, clock)
	res.eventID = eventID
	return res, nil
}

// adopted is a row computed by another version of the class, moved to
// this version's layout by name (ADR-0039): a variable the other version
// did not have is blank.
func (c *class) adopted(from *class, row *docRow) *docRow {
	out := &docRow{id: row.id, title: row.title, state: row.state, version: row.version, life: row.life,
		vals: make([]formula.Value, len(c.leaves))}
	for i, l := range c.leaves {
		if j, ok := from.leafIndex[strings.ToUpper(l.path)]; ok {
			out.vals[i] = row.vals[j]
		}
	}
	// The times of the moments follow their variables by name.
	if len(c.triggered) > 0 {
		out.ats = make([]int64, len(c.triggered))
		for i, cv := range c.triggered {
			if ocv, ok := from.byName[strings.ToUpper(cv.v.Name)]; ok && ocv.tidx >= 0 && ocv.tidx < len(row.ats) {
				out.ats[i] = row.ats[ocv.tidx]
			}
		}
	}
	return out
}

// changesFor is changes when the class has a rule on values_change, and
// nil otherwise: no rule would read the answer.
func (s *Store) changesFor(c, beforeClass *class, before, after *docRow, kids []*child) []bool {
	if len(c.rules.change) == 0 {
		return nil
	}
	return s.changes(c, beforeClass, before, after, kids)
}

// eval runs a program over the row.
func (s *Store) eval(c *class, prog *formula.Program, row *docRow, evalCtx *formula.Context) (formula.Value, *Error) {
	val, err := s.evalRaw(c, prog, row, evalCtx)
	if err != nil {
		return formula.Value{}, evalErr("", err)
	}
	return val, nil
}

// evalRaw runs a program over the row on the writer's VM and returns the
// engine's error as it is, for the callers that word it themselves.
func (s *Store) evalRaw(c *class, prog *formula.Program, row *docRow, evalCtx *formula.Context) (formula.Value, error) {
	return s.wev.run(c, prog, row, evalCtx)
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
// rows and the event. With kids nil and a before row, every composite is
// carried forward from the previous version.
func (s *Store) write(ctx context.Context, c *class, before, after *docRow, kids map[*child][]childRow, secs int64, eventID, actor string, ev eventInfo, key, hash string, day int64) error {
	// Every value is boxed once and bound wherever it goes: to the current
	// row, to the history row and to the event. The buffers are the
	// writer's, which writes one row at a time.
	life := &after.life
	idArg, actorArg, eventArg, secsArg := any(after.id), nullText(actor), any(eventID), any(secs)
	args := append(s.curArgs[:0], idArg, nullText(after.title), nullText(after.state), after.version, after.classVersion, life.CreatedAt, life.UpdatedAt, nullInt(life.EnteredAt),
		// Who, and where from (ADR-0047, item 4).
		sameText(life.CreatedBy, actor, actorArg), sameText(life.UpdatedBy, actor, actorArg), sameText(life.EnteredBy, actor, actorArg), nullText(life.EnteredFrom))
	args = c.encodeVals(args, after)
	s.curArgs = args
	sys := len(systemColumns)
	if before == nil {
		if _, err := c.w.upsert.ExecContext(ctx, args...); err != nil {
			return storageErr("write the current row", err)
		}
	} else {
		// An existing row: its system columns, and its values unless the
		// commit left every one as it was, then the id that finds it.
		upd, stmt := s.updArgs[:0], c.w.updateSystem
		for _, i := range updatedSystem {
			upd = append(upd, args[i])
		}
		if !sameValues(before, after) {
			upd, stmt = append(upd, args[sys:]...), c.w.update
		}
		upd = append(upd, idArg)
		s.updArgs = upd
		if _, err := stmt.ExecContext(ctx, upd...); err != nil {
			return storageErr("write the current row", err)
		}
		if _, err := c.w.closeHistory.ExecContext(ctx, secsArg, idArg, before.version); err != nil {
			return storageErr("close the history row", err)
		}
	}
	// The history row holds the same values, with its own after the
	// system columns: when it begins, who made it and its event. It is
	// written open.
	hist := append(s.histArgs[:0], args[:sys]...)
	hist = append(hist, secsArg, actorArg, eventArg)
	hist = append(hist, args[sys:]...)
	s.histArgs = hist
	if _, err := c.w.insertHistory.ExecContext(ctx, hist...); err != nil {
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
	var beforeVersion any
	if before != nil {
		beforeVersion = before.version
	}
	return s.writeEventArgs(ctx, eventArg, c.nameArg, idArg, ev, actorArg, beforeVersion, after.version, key, hash, day, secsArg)
}

// sameValues reports whether a commit left every stored value of the
// document as it was: the two rows are of one version of the class and
// hold the same value in every column. The same is what formula.Equal
// says, as for a set that changes nothing: an exact value keeps its
// mantissa in the bits of a number, where the smallest one reads as a
// negative zero, so the numbers are not what is compared.
func sameValues(before, after *docRow) bool {
	if before.classVersion != after.classVersion || len(before.vals) != len(after.vals) {
		return false
	}
	for i := range after.vals {
		if !formula.Equal(before.vals[i], after.vals[i]) {
			return false
		}
	}
	return true
}

// sameText boxes a text for a statement, nil when it is empty, and takes
// the box of an equal text that is boxed already.
func sameText(s, boxed string, box any) any {
	if s == boxed {
		return box
	}
	return nullText(s)
}

func (s *Store) writeDelete(ctx context.Context, c *class, before *docRow, secs int64, eventID, actor, key, hash string, day int64) error {
	if _, err := c.w.closeHistory.ExecContext(ctx, secs, before.id, before.version); err != nil {
		return storageErr("close the history row", err)
	}
	if _, err := c.w.deleteCurrent.ExecContext(ctx, before.id); err != nil {
		return storageErr("delete the current row", err)
	}
	return s.writeEvent(ctx, eventID, c.cls.Name, before.id, eventInfo{kind: EventDeleted}, actor, before.version, nil, key, hash, day, secs)
}

func (s *Store) writeEvent(ctx context.Context, eventID, class, docID string, ev eventInfo, actor string, before, after any, key, hash string, day, secs int64) error {
	return s.writeEventArgs(ctx, eventID, class, docID, ev, nullText(actor), before, after, key, hash, day, secs)
}

// writeEventArgs writes an event whose id, class, document, actor and
// time are boxed already, as the write step has them. The kind and the
// origin are of a closed list and boxed once for all, and so is the
// qualifier of its event_docs row; the business day changes once a day.
func (s *Store) writeEventArgs(ctx context.Context, eventID, class, docID any, ev eventInfo, actor, before, after any, key, hash string, day int64, secs any) error {
	if s.dayArg == nil || s.dayOf != day {
		s.dayOf, s.dayArg = day, day
	}
	args := append(s.eventArgs[:0], eventID, class, docID, word(ev.kind), nullText(ev.name), word(ev.from), word(ev.to), actor, before, after, nullText(ev.cause), nullText(key), nullText(hash), s.dayArg, secs, originCode(ev.origin))
	s.eventArgs = args
	if _, err := s.stmts.insertEvent.ExecContext(ctx, args...); err != nil {
		return storageErr("write the event", err)
	}
	args = append(s.eventArgs[:0], eventID, class, docID, word(qualifierOf(ev.kind)))
	s.eventArgs = args
	if _, err := s.stmts.insertEventDoc.ExecContext(ctx, args...); err != nil {
		return storageErr("write the event's documents", err)
	}
	return nil
}

// words holds the texts of the closed lists the log is written in, each
// boxed once, so that binding one allocates nothing.
var words = func() map[string]any {
	m := map[string]any{}
	for _, w := range []string{EventCreated, EventSet, EventDeleted, EventMigrated, EventTransition, EventReset, EventRefresh, EventActionRun, EventActionFailed, EventCustom,
		"caused", "changed", "created", "deleted", "emitted", "migrated", "refreshed", "transitioned"} {
		m[w] = w
	}
	return m
}()

// word boxes a text for a statement: nil when it is empty, the shared box
// when it is one of words, and a box of its own otherwise, as a state's
// name is.
func word(s string) any {
	if s == "" {
		return nil
	}
	if w, ok := words[s]; ok {
		return w
	}
	return s
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
// ADR-0026: the operation, the class, the document, the transition or
// the variables named, and the values.
func commandHash(cmd command) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", cmd.op, strings.ToUpper(cmd.c.cls.Name), cmd.id)
	if cmd.transition != "" {
		fmt.Fprintf(h, "%s\x00", cmd.transition)
	}
	for _, name := range cmd.variables {
		fmt.Fprintf(h, "%s\x00", strings.ToUpper(name))
	}
	if b, err := json.Marshal(cmd.values); err == nil {
		h.Write(b)
	} else {
		fmt.Fprintf(h, "%v", cmd.values)
	}
	return hex.EncodeToString(h.Sum(nil))
}
