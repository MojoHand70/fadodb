package store

import (
	"context"
	"database/sql"
	"strings"
	"sync"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The stream of changes (ADR-0058): the log, read forward for one
// caller. A stream is told every event from a place in the log onward,
// in the order of the commits, as each is written, and the engine keeps
// one thing for it: the place it has reached. It keeps no queue, so a
// subscriber that reads slowly is behind in the log and loses nothing.

// ChangeFilter says which events a stream is told of (ADR-0058, item
// 2). What is left out lets everything through.
type ChangeFilter struct {
	// Classes are the classes to hear of.
	Classes []string `json:"classes,omitempty"`
	// Kinds are the kinds of event: created, set, transition and the
	// others of the log.
	Kinds []string `json:"kinds,omitempty"`
	// ToStates are, for a transition, the states entered.
	ToStates []string `json:"to_states,omitempty"`
	// After is the id of an event: the stream begins after it. Left
	// out, the stream begins now.
	After string `json:"after,omitempty"`
}

// eventKinds are the kinds of event the log holds.
var eventKinds = []string{EventCreated, EventSet, EventTransition, EventDeleted, EventReset, EventRefresh, EventMigrated, EventActionRun, EventActionFailed, EventCustom}

// Changes is a stream of changes for one caller. Next answers its
// events one by one; Close ends it. It is used by one goroutine.
type Changes struct {
	s *Store
	o options
	// classes, kinds and states are what the filter lets through, by
	// name in capitals; nil lets everything through.
	classes, kinds, states map[string]bool
	// place is the row of the log the stream has read up to, and read
	// what it has read and not yet answered.
	place int64
	read  []*Event
	// wake is told that the log grew; ended that the stream was closed.
	wake  chan struct{}
	ended chan struct{}
	once  sync.Once
}

// endTransaction commits the transaction of the writer, and tells the
// streams of changes that the log grew (ADR-0058, item 9). Every commit
// of the store goes through here. With no stream open it costs one read of a
// pointer; with streams it wakes each without waiting for any, and it
// never allocates.
func (s *Store) endTransaction(ctx context.Context) error {
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	if open := s.listeners.Load(); open != nil {
		for _, c := range *open {
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
	}
	return nil
}

// Subscribe opens a stream of changes for the caller (ADR-0058): the
// events of the log from now on, or from after the event the filter
// names, that the filter lets through and that the caller may hear of.
// A caller hears of an event when it may read the document as the
// event left it, and of a delete when it could read the document just
// before; a content administrator hears of every document. The caller
// closes the stream when it is done with it.
func (s *Store) Subscribe(ctx context.Context, f ChangeFilter, opts ...Option) (*Changes, error) {
	select {
	case <-s.stop:
		return nil, errf(CodeClosed, "", "the store is closed")
	default:
	}
	// A stream is heard as oneself. Who may act for whom is asked class
	// by class when a command is sent (ADR-0053), and a stream is of
	// every class and of no command.
	if len(opts) > 0 && apply(opts).principal.For != "" {
		return nil, hintf(CodeNotSupported, "", "a stream of changes is heard as oneself, and this caller names somebody it acts for", "open the stream without for; the commands the caller then sends name who they are for")
	}
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, e
	}
	c := &Changes{s: s, o: o, wake: make(chan struct{}, 1), ended: make(chan struct{})}
	if len(f.Classes) > 0 {
		c.classes = map[string]bool{}
		for _, name := range f.Classes {
			cls, e := s.class(name)
			if e != nil {
				return nil, e
			}
			c.classes[strings.ToUpper(cls.cls.Name)] = true
		}
	}
	if len(f.Kinds) > 0 {
		c.kinds = map[string]bool{}
		for _, kind := range f.Kinds {
			known := false
			for _, k := range eventKinds {
				known = known || k == kind
			}
			if !known {
				return nil, hintf(CodeBadValue, "", "kinds names "+quoted(kind)+", which is no kind of event", "the kinds are "+strings.Join(eventKinds, ", "))
			}
			c.kinds[kind] = true
		}
	}
	if len(f.ToStates) > 0 {
		c.states = map[string]bool{}
		for _, state := range f.ToStates {
			if !s.hasState(c.classes, state) {
				return nil, hintf(CodeBadValue, "", "to_states names "+quoted(state)+", which is a state of no class the stream hears of", "name a state of one of the classes; a state that no document enters would keep the stream silent")
			}
			c.states[strings.ToUpper(state)] = true
		}
	}
	// The place the stream begins at. It is read before the stream is
	// told of commits, and a commit that ends in between is found by the
	// first reading of the log.
	if f.After != "" {
		err := s.db.QueryRowContext(ctx, "SELECT rowid FROM events WHERE event_id = ?", f.After).Scan(&c.place)
		if err == sql.ErrNoRows {
			return nil, hintf(CodeBadValue, "", "after names "+quoted(f.After)+", which is no event of the log", "give the id of the last event the stream handled, or leave after out to begin now")
		}
		if err != nil {
			return nil, storageErr("read the log", err)
		}
	} else if err := s.db.QueryRowContext(ctx, "SELECT coalesce(max(rowid), 0) FROM events").Scan(&c.place); err != nil {
		return nil, storageErr("read the log", err)
	}
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	select {
	case <-s.stop:
		return nil, errf(CodeClosed, "", "the store is closed")
	default:
	}
	open := []*Changes{c}
	if was := s.listeners.Load(); was != nil {
		open = append(append(make([]*Changes, 0, len(*was)+1), *was...), c)
	}
	s.listeners.Store(&open)
	return c, nil
}

// hasState reports whether a state of that name is a state of one of
// the classes, or of any class when none is named.
func (s *Store) hasState(classes map[string]bool, state string) bool {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	for key, c := range s.classes {
		if classes != nil && !classes[key] {
			continue
		}
		for _, name := range c.cls.States.List {
			if strings.EqualFold(name, state) {
				return true
			}
		}
	}
	return false
}

// As says who the caller of the stream is from now on: the events read
// after it are told to that caller. A door that knows its callers by a
// key asks whose the key is before each event, so that a stream follows
// the groups of its user (ADR-0058, item 4).
func (c *Changes) As(p Principal) error {
	if e := checkPrincipal(p); e != nil {
		return e
	}
	if p.For != "" {
		return hintf(CodeNotSupported, "", "a stream of changes is heard as oneself, and this caller names somebody it acts for", "name the caller without for")
	}
	c.o.principal = p
	return nil
}

// Next answers the next event of the stream, waiting for one when the
// log has none. Its error is the context's when the context ends, and
// closed when the stream or the store was closed.
func (c *Changes) Next(ctx context.Context) (*Event, error) {
	for {
		if len(c.read) > 0 {
			ev := c.read[0]
			c.read = c.read[1:]
			return ev, nil
		}
		select {
		case <-c.ended:
			return nil, errf(CodeClosed, "", "the stream is closed")
		case <-c.s.stop:
			return nil, errf(CodeClosed, "", "the store is closed")
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		more, err := c.fill(ctx)
		if err != nil {
			// A caller that gave up while the log was read gave up: that
			// is what it is told, and not what the reading made of it.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if more {
			// The log had events, of which the caller may have heard none.
			continue
		}
		select {
		case <-c.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ended:
			return nil, errf(CodeClosed, "", "the stream is closed")
		case <-c.s.stop:
			return nil, errf(CodeClosed, "", "the store is closed")
		}
	}
}

// Close ends the stream. It may be called from another goroutine than
// the one that reads, and more than once.
func (c *Changes) Close() {
	c.once.Do(func() {
		close(c.ended)
		s := c.s
		s.listenerMu.Lock()
		defer s.listenerMu.Unlock()
		was := s.listeners.Load()
		if was == nil {
			return
		}
		open := make([]*Changes, 0, len(*was))
		for _, other := range *was {
			if other != c {
				open = append(open, other)
			}
		}
		s.listeners.Store(&open)
	})
}

// logBatch is how many events of the log a stream reads at once.
const logBatch = 256

// fill reads the events that follow the place of the stream, and keeps
// the ones the filter lets through and the caller may hear of. It
// reports whether the log had any event to read.
func (c *Changes) fill(ctx context.Context) (bool, error) {
	s := c.s
	rows, err := s.db.QueryContext(ctx, "SELECT e.rowid, e.event_id, e.class, e.doc_id, e.kind, e.name, e.from_state, e.to_state, e.actor_id, e.before_version, e.after_version, e.cause_event_id, e.command_key, e.business_date, e.ts,"+
		" (SELECT o.error_code FROM outbox o WHERE o.outcome_event = e.event_id), e.origin, e.actor_kind, e.actor_for, e.override, e.reason"+
		" FROM events e WHERE e.rowid > ? ORDER BY e.rowid LIMIT ?", c.place, logBatch)
	if err != nil {
		return false, storageErr("read the log", err)
	}
	var batch []*Event
	place := c.place
	for rows.Next() {
		var ev Event
		var name, from, to, actor, cause, key, code, kind, acts, override, reason sql.NullString
		var before, after, origin sql.NullInt64
		var day int64
		if err := rows.Scan(&place, &ev.ID, &ev.Class, &ev.DocID, &ev.Kind, &name, &from, &to, &actor, &before, &after, &cause, &key, &day, &ev.TS, &code, &origin, &kind, &acts, &override, &reason); err != nil {
			rows.Close()
			return false, storageErr("read the log", err)
		}
		ev.ActorKind, ev.ActorFor = kind.String, acts.String
		ev.Override, ev.Reason = override.String, reason.String
		ev.Name, ev.FromState, ev.ToState, ev.ActorID, ev.CauseEventID, ev.CommandKey = name.String, from.String, to.String, actor.String, cause.String, key.String
		ev.Origin = originOf(origin.Int64)
		if ev.Kind == EventActionFailed {
			ev.Code = code.String
		}
		ev.BeforeVersion, ev.AfterVersion = before.Int64, after.Int64
		ev.BusinessDate = formula.Render(formula.Date(day))
		batch = append(batch, &ev)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, storageErr("read the log", err)
	}
	for _, ev := range batch {
		if !c.lets(ev) {
			continue
		}
		heard, err := c.hears(ctx, ev)
		if err != nil {
			return false, err
		}
		if heard {
			c.read = append(c.read, ev)
		}
	}
	more := place != c.place
	c.place = place
	return more, nil
}

// lets reports whether the filter of the stream lets an event through.
func (c *Changes) lets(ev *Event) bool {
	if c.classes != nil && !c.classes[strings.ToUpper(ev.Class)] {
		return false
	}
	if c.kinds != nil && !c.kinds[ev.Kind] {
		return false
	}
	if c.states != nil && (ev.Kind != EventTransition || !c.states[strings.ToUpper(ev.ToState)]) {
		return false
	}
	return true
}

// hears reports whether the caller of the stream may hear of an event:
// whether it may read the document as the event left it, and for a
// delete as it was just before (ADR-0058, item 4). A document the
// caller may not read is absent, so nothing is said of it.
func (c *Changes) hears(ctx context.Context, ev *Event) (bool, error) {
	if c.o.principal.content() {
		return true, nil
	}
	s := c.s
	cls, e := s.class(ev.Class)
	if e != nil {
		return false, nil
	}
	version := ev.AfterVersion
	if ev.Kind == EventDeleted || version == 0 {
		version = ev.BeforeVersion
	}
	if version == 0 {
		// An event that names no version: the document as it is now.
		return s.sees(ctx, cls, ev.DocID, c.o)
	}
	row, err := cls.scanRow(cls.r.getVersion.QueryRowContext(ctx, ev.DocID, version), true)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, storageErr("read the document", err)
	}
	vc, row, err := s.underVersion(ctx, cls, row, true)
	if err != nil {
		return false, err
	}
	return s.seesRow(cls, vc, row, c.o), nil
}
