package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/query"
	"github.com/MojoHand70/fadodb/internal/state"
)

// DueDocument is one document a commit would move now (ADR-0047, item
// 8): the state it is in and the state the first transition that fires
// by itself leads to, an eventless one whose guard holds or a timed one
// whose time has come. Since is the moment since which that has been so,
// in seconds since the epoch, where the engine knows it: the moment of a
// timed transition. A guard does not say when it began to hold, so for
// an eventless transition it is 0. What follows that move, its rules and
// any further transition of the chain, is the commit's to work out;
// Preview answers it for one document.
type DueDocument struct {
	ID    string `json:"id"`
	State string `json:"state"`
	To    string `json:"to"`
	Since int64  `json:"since,omitempty"`
}

// MaxDue is the most documents one call of Due lists.
const MaxDue = query.MaxLimit

// dueBatch is how many ids the document-by-document path reads at a
// time; the cursor is closed before the documents are looked at.
const dueBatch = 1000

// duePlan is how Due reads a class: its live states that a transition
// of the chain leaves, eventless or timed, in the order of the class's
// states.
type duePlan struct {
	states []*dueState
}

// dueState is one state of the plan: the eventless and the timed
// transitions leaving it in definition order and, when the condition of
// every one translates to SQL, one filter for each.
type dueState struct {
	name    string
	leaving []*model.Transition
	filters []dueFilter // nil: a condition does not translate
	// noOlder says the state holds no document an older version of the
	// class computed. A document enters a state by a commit, and a commit
	// writes the current version, so once none is there none comes.
	noOlder atomic.Bool
}

// dueFilter is the condition of one transition as a statement that
// selects the ids of the documents of the current class version in the
// state for which it holds, and the parameters of its literals. The
// statement of a timed transition selects the moment of the timer
// beside each id.
type dueFilter struct {
	text  string
	args  []any
	timed bool
}

// dueHit is where a due document would move, and since when.
type dueHit struct {
	to    string
	since int64
}

// duePlanned holds a class's plan, compiled at the first call of Due.
type duePlanned struct {
	once sync.Once
	plan *duePlan
}

// Due lists the documents of a class that a commit would move if it
// touched them now, or at the moment given with At (ADR-0047, item 8):
// each with its state and the state it would move to, the target of the
// first transition leaving its state, in definition order, that fires by
// itself: an eventless one whose guard is TRUE, or a timed one whose time
// has come and whose guard, if it has one, is TRUE. A document a timed
// transition would move carries the moment of its timer, since which it
// has been due. USER() and INGROUP in a guard are the principal's of the
// call. The list is in the order of the class's states and, in a state,
// of the ids, and holds at most MaxDue documents: a caller that touches
// what it gets asks again until the list is empty.
//
// A condition that translates to SQL is one filter over the documents of
// its state (ADR-0038). One that does not, a guard or an at that looks
// another document up or calls a function of a state, is evaluated
// document by document, and so is every document that an older version
// of the class computed, as the commit that adopted it would evaluate it
// (ADR-0039). A document in a terminal state is frozen and is never due.
// Due writes nothing.
func (s *Store) Due(ctx context.Context, class string, opts ...Option) ([]DueDocument, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	plan := s.duePlan(c)
	out := []DueDocument{}
	for _, st := range plan.states {
		room := MaxDue - len(out)
		if room <= 0 {
			break
		}
		found, err := s.dueIn(ctx, c, st, o, room)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	// A document the caller may not see is not listed (ADR-0049, item 3).
	// The host program that touches what is due asks as a content administrator,
	// for whom this costs nothing; for anybody else it is a read of each
	// document found.
	if !o.principal.content() {
		seen := out[:0]
		for _, d := range out {
			ok, err := s.sees(ctx, c, d.ID, o)
			if err != nil {
				return nil, err
			}
			if ok {
				seen = append(seen, d)
			}
		}
		out = seen
	}
	return out, nil
}

// duePlan compiles the class's plan at the first call. A guard or an at
// that the filter compiler refuses, for any reason, leaves its state to
// the document-by-document path, which evaluates it as a commit does.
func (s *Store) duePlan(c *class) *duePlan {
	c.due.once.Do(func() {
		plan := &duePlan{}
		c.due.plan = plan
		if c.cls.States == nil {
			return
		}
		sources := s.sources()
		for _, name := range c.cls.States.List {
			if c.cls.States.IsFinal(name) {
				continue
			}
			st := &dueState{name: name}
			for _, t := range c.cls.Transitions {
				if state.Unasked(t) && state.From(t, name) {
					st.leaving = append(st.leaving, t)
				}
			}
			if len(st.leaving) == 0 {
				continue
			}
			for _, t := range st.leaving {
				f, ok := dueFilterFor(c, t, sources)
				if !ok {
					st.filters = nil
					break
				}
				st.filters = append(st.filters, f)
			}
			plan.states = append(plan.states, st)
		}
	})
	return c.due.plan
}

// dueFilterFor translates the condition of a transition into the
// statement of its filter: the guard of an eventless one, and for a
// timed one that its time has come, with its guard when it has one, and
// the moment as a column. The condition is the engine's own, so it is
// compiled as a content administrator's filter: no read right narrows what it
// compares.
func dueFilterFor(c *class, t *model.Transition, sources formula.Sources) (dueFilter, bool) {
	where, since := t.GuardSrc, ""
	var came string
	switch {
	case t.After > 0:
		// The time in the state: the entry is a column of the row.
		came = `"state_entered_at" <= :now - ` + fmt.Sprint(t.After)
		since = `, "state_entered_at" + ` + fmt.Sprint(t.After)
	case t.At != nil:
		// The moment the document holds, as a formula of the filter: a
		// blank never comes, and a date is the first instant of its day in
		// UTC, which has come when today is that day or a later one.
		value, kind, err := query.Value(t.AtSrc, c.cls, sources)
		if err != nil {
			return dueFilter{}, false
		}
		clock := "NOW()"
		since = ", CAST(" + value + " AS INTEGER)"
		if kind == formula.KindDate {
			clock, since = "TODAY()", since+" * 86400"
		}
		where = "ISNOTBLANK(" + t.AtSrc + ") AND " + clock + " >= (" + t.AtSrc + ")"
		if t.GuardSrc != "" {
			where += " AND (" + t.GuardSrc + ")"
		}
	}
	var args []any
	if where != "" {
		plan, err := query.Compile(query.Filter{Class: c.cls.Name, Where: where}, c.cls, sources, query.Principal{Content: true})
		if err != nil || plan.Where == "" {
			return dueFilter{}, false
		}
		where, args = plan.Where, plan.Args
	}
	switch {
	case came != "" && where != "":
		where = came + " AND (" + where + ")"
	case came != "":
		where = came
	case where == "":
		return dueFilter{}, false
	}
	// The order is by id, written so that SQLite does not walk the whole
	// class in the order of its ids to get it: few documents of many are
	// due, and an index on what the condition compares finds them.
	text := `SELECT "id"` + since + ` FROM ` + quote(c.table) + ` WHERE "state" = :due_state AND "class_version" = :due_version AND (` + where +
		`) ORDER BY +"id" LIMIT :due_limit`
	return dueFilter{text: text, args: args, timed: since != ""}, true
}

// dueIn lists the documents in one state that are due, in the order of
// their ids, at most room of them.
func (s *Store) dueIn(ctx context.Context, c *class, st *dueState, o options, room int) ([]DueDocument, error) {
	to := map[string]dueHit{}
	if st.filters == nil {
		// Every document in the state, one by one.
		if _, err := s.dueOneByOne(ctx, c, st, o, room, false, to); err != nil {
			return nil, err
		}
	} else {
		for i, f := range st.filters {
			// The first transition in definition order that fires is the
			// one a commit takes.
			if err := s.dueFiltered(ctx, c, st, f, o, room, st.leaving[i].To, to); err != nil {
				return nil, err
			}
		}
		// The documents an older version computed, which the filters
		// leave out. A class at its first version has none.
		if c.cls.Version > 1 && !st.noOlder.Load() {
			older, err := s.dueOneByOne(ctx, c, st, o, room, true, to)
			if err != nil {
				return nil, err
			}
			if !older {
				st.noOlder.Store(true)
			}
		}
	}
	out := make([]DueDocument, 0, len(to))
	for id, hit := range to {
		out = append(out, DueDocument{ID: id, State: st.name, To: hit.to, Since: hit.since})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > room {
		out = out[:room]
	}
	return out, nil
}

// dueFiltered runs one transition's filter, over the documents of the
// current class version in the state, and records in to every document
// it finds that an earlier transition has not claimed: where it would
// move, target, and the moment of its timer.
func (s *Store) dueFiltered(ctx context.Context, c *class, st *dueState, f dueFilter, o options, room int, target string, to map[string]dueHit) error {
	stmt, err := s.filters.acquire(ctx, s.db, f.text)
	if err != nil {
		return storageErr("prepare the filter of what is due in "+c.cls.Name, err)
	}
	defer s.filters.release(stmt)
	args := make([]any, 0, len(f.args)+8)
	args = append(args, f.args...)
	args = append(args, s.contextArgs(o)...)
	args = append(args, sql.Named("due_state", st.name), sql.Named("due_version", c.cls.Version), sql.Named("due_limit", room))
	rows, err := stmt.stmt.QueryContext(ctx, args...)
	if err != nil {
		return storageErr("list what is due in "+c.cls.Name, err)
	}
	defer rows.Close()
	var id string
	var since any
	dest := []any{&id}
	if f.timed {
		dest = append(dest, &since)
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return storageErr("list what is due in "+c.cls.Name, err)
		}
		if _, ok := to[id]; !ok {
			to[id] = dueHit{to: target, since: toInt64(since)}
		}
	}
	return storageErr("list what is due in "+c.cls.Name, rows.Err())
}

// dueOneByOne looks at the documents in the state one at a time, every
// one of them or, with older, the ones an older version of the class
// computed, in the order of their ids, and records in to where each that
// is due would move, until room of them are. It reports whether it saw
// a document at all. The ids are read a batch at a time and the cursor
// closed before the documents are looked at, each under the writer's
// lock for the time it takes: the guards run on the writer's VM, which
// looks tables up and reads a document's life through the writer's
// statements.
func (s *Store) dueOneByOne(ctx context.Context, c *class, st *dueState, o options, room int, older bool, to map[string]dueHit) (bool, error) {
	text := `SELECT "id" FROM ` + quote(c.table) + ` WHERE "state" = ? AND "id" > ?`
	if older {
		text += ` AND "class_version" <> ` + fmt.Sprint(c.cls.Version)
	}
	text += ` ORDER BY "id" LIMIT ` + fmt.Sprint(dueBatch)
	stmt, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return false, storageErr("prepare the list of "+c.cls.Name, err)
	}
	defer s.filters.release(stmt)
	ctxOf := contextOf(o.principal, s.clock(o))
	evalCtx := &ctxOf
	seen, found, after := false, 0, ""
	ids := make([]string, 0, dueBatch)
	for {
		ids = ids[:0]
		s.dueLists.Add(1)
		rows, err := stmt.stmt.QueryContext(ctx, st.name, after)
		if err != nil {
			return seen, storageErr("list the documents of "+c.cls.Name, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return seen, storageErr("list the documents of "+c.cls.Name, err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return seen, storageErr("list the documents of "+c.cls.Name, err)
		}
		if len(ids) == 0 {
			return seen, nil
		}
		seen = true
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return seen, err
			}
			hit, err := s.dueOne(ctx, c, id, st.name, evalCtx)
			if err != nil {
				return seen, err
			}
			if hit.to == "" {
				continue
			}
			to[id] = hit
			if found++; found >= room {
				return seen, nil
			}
		}
		if len(ids) < dueBatch {
			return seen, nil
		}
		after = ids[len(ids)-1]
	}
}

// dueOne answers where a commit that touched the document now would
// move it first, and since when: the target of the first transition
// leaving its state that fires by itself over the document as stored, an
// eventless one whose guard holds or a timed one whose time has come,
// and the moment of a timed one; no target when none does. A document that is gone, or that left the state since it was
// listed, is not due here. A document an older version of the class
// computed is evaluated as the commit that adopted it would: its values
// moved to the current layout and every formula recomputed (ADR-0039);
// when that recalculation fails the commit would be refused, and the
// document is not due.
func (s *Store) dueOne(ctx context.Context, c *class, id, in string, evalCtx *formula.Context) (dueHit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return dueHit{}, errf(CodeClosed, "", "the store is closed")
	}
	read := c.w.selectLean
	if c.who {
		read = c.w.selectCurrent
	}
	row, err := c.scanW(read.QueryRowContext(ctx, id), !c.who)
	if err != nil {
		if err == sql.ErrNoRows {
			return dueHit{}, nil
		}
		return dueHit{}, storageErr("read the document", err)
	}
	if row.state != in {
		return dueHit{}, nil
	}
	evalCtx.ID, evalCtx.State = row.id, row.state
	if row.classVersion != c.cls.Version && row.classVersion != 0 {
		old := s.classAt(c.cls.Name, row.classVersion)
		if old == nil {
			return dueHit{}, errf(CodeStorage, "", "the document %s was computed by version %d of %s, which the store does not have", row.id, row.classVersion, c.cls.Name)
		}
		before, err := old.scanW(old.w.selectCurrent.QueryRowContext(ctx, id), false)
		if err != nil {
			return dueHit{}, storageErr("read the document under its class version", err)
		}
		row = c.adopted(old, before)
		row.classVersion = c.cls.Version
		var errs []*Error
		if err := s.recalc(c, row, s.dirtyFor(c), true, evalCtx, false, &errs); err != nil {
			return dueHit{}, nil
		}
	}
	t := state.Next(c.cls, row.state, func(t *model.Transition) bool {
		return s.fires(c, t, row, evalCtx, false)
	})
	if t == nil {
		return dueHit{}, nil
	}
	hit := dueHit{to: t.To}
	if t.Timed() {
		hit.since, _ = s.timerOf(c, t, row, evalCtx, false)
	}
	return hit, nil
}
