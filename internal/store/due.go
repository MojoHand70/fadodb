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
// 8): the state it is in and the state the first eventless transition
// whose guard holds leads to. What follows that move, its rules and any
// further eventless transition, is the commit's to work out; Preview
// answers it for one document.
type DueDocument struct {
	ID    string `json:"id"`
	State string `json:"state"`
	To    string `json:"to"`
}

// MaxDue is the most documents one call of Due lists.
const MaxDue = query.MaxLimit

// dueBatch is how many ids the document-by-document path reads at a
// time; the cursor is closed before the documents are looked at.
const dueBatch = 1000

// duePlan is how Due reads a class: its live states that an eventless
// transition leaves, in the order of the class's states.
type duePlan struct {
	states []*dueState
}

// dueState is one state of the plan: the eventless transitions leaving
// it in definition order and, when every guard translates to SQL, one
// filter for each.
type dueState struct {
	name    string
	leaving []*model.Transition
	filters []dueFilter // nil: a guard does not translate
	// noOlder says the state holds no document an older version of the
	// class computed. A document enters a state by a commit, and a commit
	// writes the current version, so once none is there none comes.
	noOlder atomic.Bool
}

// dueFilter is the guard of one transition as a statement that selects
// the ids of the documents of the current class version in the state,
// and the parameters of its literals.
type dueFilter struct {
	text string
	args []any
}

// duePlanned holds a class's plan, compiled at the first call of Due.
type duePlanned struct {
	once sync.Once
	plan *duePlan
}

// Due lists the documents of a class that a commit would move if it
// touched them now, or at the moment given with At (ADR-0047, item 8):
// each with its state and the state it would move to, the target of the
// first eventless transition leaving its state, in definition order,
// whose guard is TRUE. USER() and INGROUP in a guard are the principal's
// of the call. The list is in the order of the class's states and, in a
// state, of the ids, and holds at most MaxDue documents: a caller that
// touches what it gets asks again until the list is empty.
//
// A guard that translates to SQL is one filter over the documents of
// its state (ADR-0038). A guard that does not, one that looks another
// document up or calls a function of a state, is evaluated document by
// document, and so is every document that an older version of the class
// computed, as the commit that adopted it would evaluate it (ADR-0039).
// A document in a terminal state is frozen and is never due. Due writes
// nothing.
func (s *Store) Due(ctx context.Context, class string, opts ...Option) ([]DueDocument, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
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
	return out, nil
}

// duePlan compiles the class's plan at the first call. A guard that the
// filter compiler refuses, for any reason, leaves its state to the
// document-by-document path, which evaluates it as a commit does.
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
				if t.Name == "" && state.From(t, name) {
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

// dueFilterFor translates a transition's guard into the statement of its
// filter. The guard is the engine's own condition, so it is compiled as
// an administrator's filter: no read right narrows what it compares.
func dueFilterFor(c *class, t *model.Transition, sources formula.Sources) (dueFilter, bool) {
	if t.Guard == nil {
		return dueFilter{}, false
	}
	plan, err := query.Compile(query.Filter{Class: c.cls.Name, Where: t.GuardSrc}, c.cls, sources, query.Principal{Admin: true})
	if err != nil || plan.Where == "" {
		return dueFilter{}, false
	}
	// The order is by id, written so that SQLite does not walk the whole
	// class in the order of its ids to get it: few documents of many are
	// due, and an index on what the guard compares finds them.
	text := `SELECT "id" FROM ` + quote(c.table) + ` WHERE "state" = :due_state AND "class_version" = :due_version AND (` + plan.Where +
		`) ORDER BY +"id" LIMIT :due_limit`
	return dueFilter{text: text, args: plan.Args}, true
}

// dueIn lists the documents in one state that are due, in the order of
// their ids, at most room of them.
func (s *Store) dueIn(ctx context.Context, c *class, st *dueState, o options, room int) ([]DueDocument, error) {
	to := map[string]string{}
	if st.filters == nil {
		// Every document in the state, one by one.
		if _, err := s.dueOneByOne(ctx, c, st, o, room, false, to); err != nil {
			return nil, err
		}
	} else {
		for i, f := range st.filters {
			ids, err := s.dueFiltered(ctx, c, st, f, o, room)
			if err != nil {
				return nil, err
			}
			// The first transition in definition order whose guard holds
			// is the one a commit takes.
			for _, id := range ids {
				if _, ok := to[id]; !ok {
					to[id] = st.leaving[i].To
				}
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
	for id, target := range to {
		out = append(out, DueDocument{ID: id, State: st.name, To: target})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > room {
		out = out[:room]
	}
	return out, nil
}

// dueFiltered runs one transition's filter: the ids of the documents of
// the current class version in the state whose guard holds.
func (s *Store) dueFiltered(ctx context.Context, c *class, st *dueState, f dueFilter, o options, room int) ([]string, error) {
	stmt, err := s.filters.acquire(ctx, s.db, f.text)
	if err != nil {
		return nil, storageErr("prepare the filter of what is due in "+c.cls.Name, err)
	}
	defer s.filters.release(stmt)
	args := make([]any, 0, len(f.args)+8)
	args = append(args, f.args...)
	args = append(args, s.contextArgs(o)...)
	args = append(args, sql.Named("due_state", st.name), sql.Named("due_version", c.cls.Version), sql.Named("due_limit", room))
	rows, err := stmt.stmt.QueryContext(ctx, args...)
	if err != nil {
		return nil, storageErr("list what is due in "+c.cls.Name, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, storageErr("list what is due in "+c.cls.Name, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("list what is due in "+c.cls.Name, err)
	}
	return ids, nil
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
func (s *Store) dueOneByOne(ctx context.Context, c *class, st *dueState, o options, room int, older bool, to map[string]string) (bool, error) {
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
	evalCtx := &formula.Context{User: o.principal.ID, Groups: o.principal.Groups, Now: s.clock(o)}
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
			target, err := s.dueOne(ctx, c, id, st.name, evalCtx)
			if err != nil {
				return seen, err
			}
			if target == "" {
				continue
			}
			to[id] = target
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
// move it first: the target of the first eventless transition leaving
// its state whose guard holds over the document as stored, "" when none
// does. A document that is gone, or that left the state since it was
// listed, is not due here. A document an older version of the class
// computed is evaluated as the commit that adopted it would: its values
// moved to the current layout and every formula recomputed (ADR-0039);
// when that recalculation fails the commit would be refused, and the
// document is not due.
func (s *Store) dueOne(ctx context.Context, c *class, id, in string, evalCtx *formula.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errf(CodeClosed, "", "the store is closed")
	}
	read := c.w.selectLean
	if c.who {
		read = c.w.selectCurrent
	}
	row, err := c.scanW(read.QueryRowContext(ctx, id), !c.who)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", storageErr("read the document", err)
	}
	if row.state != in {
		return "", nil
	}
	evalCtx.ID, evalCtx.State = row.id, row.state
	if row.classVersion != c.cls.Version && row.classVersion != 0 {
		old := s.classAt(c.cls.Name, row.classVersion)
		if old == nil {
			return "", errf(CodeStorage, "", "the document %s was computed by version %d of %s, which the store does not have", row.id, row.classVersion, c.cls.Name)
		}
		before, err := old.scanW(old.w.selectCurrent.QueryRowContext(ctx, id), false)
		if err != nil {
			return "", storageErr("read the document under its class version", err)
		}
		row = c.adopted(old, before)
		row.classVersion = c.cls.Version
		var errs []*Error
		if err := s.recalc(c, row, s.dirtyFor(c), true, evalCtx, false, &errs); err != nil {
			return "", nil
		}
	}
	t := state.Next(c.cls, row.state, func(t *model.Transition) bool {
		held, _ := s.holds(c, t, row, evalCtx)
		return held
	})
	if t == nil {
		return "", nil
	}
	return t.To, nil
}
