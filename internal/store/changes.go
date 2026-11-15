package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

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
	// who, when set, says who the caller of the stream is now (Follow).
	who func(context.Context) (Principal, error)
	// wake is told that the log grew; ended that the stream was closed.
	wake  chan struct{}
	ended chan struct{}
	once  sync.Once
}

// endTransaction commits the transaction of the writer, and tells the
// streams of changes that the log grew (ADR-0058, item 9). Every commit
// of the store goes through here. With no stream open it costs a count
// and one read of a pointer; with streams it wakes each without waiting
// for any, and it never allocates.
func (s *Store) endTransaction(ctx context.Context) error {
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	// The mark that the log grew: a stream that has read to the end of
	// the log asks the database again only after it moved.
	s.tail.grown.Add(1)
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

// Follow has the stream follow its caller (ADR-0058, item 4): who says
// who the caller is now, and the stream asks it each time the log has
// events its filter lets through, after it has read them and before it
// judges which of them the caller may hear of. So the caller is judged
// as it is no earlier than the last of those events: a caller that was
// given a group hears of what was written after, and one that lost a
// group hears of nothing that was.
//
// A door that knows its callers by a key gives the function that asks
// whose the key is; a program that names its callers itself gives the
// one that reads its session. An error of who ends the stream: Next
// answers it, and the events that were read are told to nobody. Follow
// is called before the first Next.
func (c *Changes) Follow(who func(context.Context) (Principal, error)) { c.who = who }

// follow asks who the caller of the stream is now.
func (c *Changes) follow(ctx context.Context) error {
	p, err := c.who(ctx)
	if err != nil {
		return err
	}
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
		if len(open) == 0 {
			// Nobody listens: what the tail holds is kept for nobody.
			s.tail.forget()
		}
	})
}

// logBatch is how many events of the log a stream reads at once, and
// tailSize how many of the last events of the log the tail holds.
const (
	logBatch = 256
	tailSize = 1024
)

// logEntry is one event of the log as the streams read it: the event,
// and the version of its document the read rule is asked over. The
// version is read from the database once, when a caller that is no
// content administrator first asks, and then serves every stream.
type logEntry struct {
	rowid int64
	ev    Event

	mu     sync.Mutex
	loaded bool
	// vc is the class version that computed the row; a nil row is a
	// document no version of which can be read, of which nobody hears.
	vc  *class
	row *docRow
}

// logTail is the end of the log, read once for all the streams
// (ADR-0058, item 9). A commit wakes every stream, and each then asks
// for the events after its place. The streams that keep up are all at
// the same place, the end of the log: the first of them to ask reads
// the new events from the database into the tail, and the others take
// them from there. So what the streams read from the database after a
// commit does not grow with their number, and a hundred of them do not
// stand in the way of the next commit. A stream that is behind what
// the tail holds reads the log itself until it has caught up.
type logTail struct {
	mu sync.Mutex
	// held are the events of the log above the row after and up to the
	// row last, in order; set says the tail has a place at all.
	set         bool
	after, last int64
	held        []*logEntry
	// grown counts the commits of the store. ended is its count from
	// before the read that found the end of the log, and atEnd says
	// there was such a read: while the count has not moved since, the
	// log has no event the tail does not hold, and nobody asks the
	// database.
	grown atomic.Int64
	ended int64
	atEnd bool
	// reads counts the times the log was read from the database for a
	// stream, and loads the versions of documents that were; a test
	// holds both to the number of events and not of streams.
	reads, loads atomic.Int64
}

// forget empties the tail.
func (t *logTail) forget() {
	t.mu.Lock()
	t.set, t.held, t.atEnd = false, nil, false
	t.mu.Unlock()
}

// logSince answers the events of the log after a place, at most
// logBatch of them and in order. It answers them from the tail when the
// place is where the tail is or within it, and from the database when
// the place is behind it.
func (s *Store) logSince(ctx context.Context, place int64) ([]*logEntry, error) {
	t := &s.tail
	t.mu.Lock()
	if !t.set || place > t.last {
		// The tail holds nothing yet, or stopped short of this stream
		// because nobody read it: it begins where the stream is.
		t.set, t.after, t.last, t.held, t.atEnd = true, place, place, t.held[:0], false
	}
	if place < t.after {
		t.mu.Unlock()
		return s.readLog(ctx, place)
	}
	defer t.mu.Unlock()
	if place == t.last && !(t.atEnd && t.grown.Load() == t.ended) {
		// The stream is at the end of what the tail holds, and is the
		// first to ask since a commit: it reads on, for all of them.
		grown := t.grown.Load()
		more, err := s.readLog(ctx, t.last)
		if err != nil {
			return nil, err
		}
		// A read that was not full reached the end of the log, as it
		// was after the count was taken.
		t.atEnd, t.ended = len(more) < logBatch, grown
		if len(more) > 0 {
			t.held = append(t.held, more...)
			t.last = more[len(more)-1].rowid
			if over := len(t.held) - tailSize; over > 0 {
				t.after = t.held[over-1].rowid
				t.held = append(t.held[:0], t.held[over:]...)
			}
		}
	}
	from := sort.Search(len(t.held), func(i int) bool { return t.held[i].rowid > place })
	// A list of its own: the tail moves on under the one it holds.
	return append([]*logEntry(nil), t.held[from:min(len(t.held), from+logBatch)]...), nil
}

// readLog reads the events of the log after a place from the database,
// at most logBatch of them.
func (s *Store) readLog(ctx context.Context, place int64) ([]*logEntry, error) {
	s.tail.reads.Add(1)
	rows, err := s.db.QueryContext(ctx, "SELECT e.rowid, e.event_id, e.class, e.doc_id, e.kind, e.name, e.from_state, e.to_state, e.actor_id, e.before_version, e.after_version, e.cause_event_id, e.command_key, e.business_date, e.ts,"+
		" (SELECT o.error_code FROM outbox o WHERE o.outcome_event = e.event_id), e.origin, e.actor_kind, e.actor_for, e.override, e.reason"+
		" FROM events e WHERE e.rowid > ? ORDER BY e.rowid LIMIT ?", place, logBatch)
	if err != nil {
		return nil, storageErr("read the log", err)
	}
	defer rows.Close()
	var batch []*logEntry
	for rows.Next() {
		e := &logEntry{}
		ev := &e.ev
		var name, from, to, actor, cause, key, code, kind, acts, override, reason sql.NullString
		var before, after, origin sql.NullInt64
		var day int64
		if err := rows.Scan(&e.rowid, &ev.ID, &ev.Class, &ev.DocID, &ev.Kind, &name, &from, &to, &actor, &before, &after, &cause, &key, &day, &ev.TS, &code, &origin, &kind, &acts, &override, &reason); err != nil {
			return nil, storageErr("read the log", err)
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
		batch = append(batch, e)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the log", err)
	}
	return batch, nil
}

// version answers the version of the document the read rule is asked
// over for the event, with the class version that computed it: the
// version the event left; for a delete, the version before it; and for
// an event that names no version, the document as it is now, or its
// last version when it was deleted (ADR-0058, item 4). The row is nil
// when there is none to read.
func (e *logEntry) version(ctx context.Context, s *Store, cls *class) (*class, *docRow, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return e.vc, e.row, nil
	}
	version := e.ev.AfterVersion
	if e.ev.Kind == EventDeleted || version == 0 {
		version = e.ev.BeforeVersion
	}
	s.tail.loads.Add(1)
	history := true
	var row *docRow
	var err error
	if version != 0 {
		row, err = cls.scanRow(cls.r.getVersion.QueryRowContext(ctx, e.ev.DocID, version), true)
	} else {
		history = false
		row, err = cls.scanRow(cls.r.getCurrent.QueryRowContext(ctx, e.ev.DocID), false)
		if err == sql.ErrNoRows {
			history = true
			row, err = cls.scanRow(cls.r.getLast.QueryRowContext(ctx, e.ev.DocID), true)
		}
	}
	if err == sql.ErrNoRows {
		e.loaded = true
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, storageErr("read the document", err)
	}
	vc, row, err := s.underVersion(ctx, cls, row, history)
	if err != nil {
		return nil, nil, err
	}
	e.vc, e.row, e.loaded = vc, row, true
	return vc, row, nil
}

// fill reads the events that follow the place of the stream, and keeps
// the ones the filter lets through and the caller may hear of. It
// reports whether the log had any event to read. When it fails it has
// kept nothing and stays at its place, so that it can be asked again.
func (c *Changes) fill(ctx context.Context) (bool, error) {
	batch, err := c.s.logSince(ctx, c.place)
	if err != nil || len(batch) == 0 {
		return false, err
	}
	place := batch[len(batch)-1].rowid
	// let are the events of the batch the filter lets through.
	let := batch[:0]
	for _, e := range batch {
		if c.lets(&e.ev) {
			let = append(let, e)
		}
	}
	// Who the caller is now is asked after the events were read, so that
	// no event is judged for a caller as it was before the event.
	if len(let) > 0 && c.who != nil {
		if err := c.follow(ctx); err != nil {
			return false, err
		}
	}
	var heard []*Event
	for _, e := range let {
		hears, err := c.hears(ctx, e)
		if err != nil {
			return false, err
		}
		if hears {
			// The caller's own: the event of the tail is every stream's.
			ev := e.ev
			heard = append(heard, &ev)
		}
	}
	c.read = append(c.read, heard...)
	c.place = place
	return true, nil
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
// caller may not read is absent, so nothing is said of it. The rule is
// the class's as it is now.
func (c *Changes) hears(ctx context.Context, e *logEntry) (bool, error) {
	if c.o.principal.content() {
		return true, nil
	}
	s := c.s
	cls, er := s.class(e.ev.Class)
	if er != nil {
		return false, nil
	}
	vc, row, err := e.version(ctx, s, cls)
	if err != nil || row == nil {
		return false, err
	}
	return s.seesRow(cls, vc, row, c.o), nil
}
