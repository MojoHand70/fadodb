package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/state"
)

// Transition moves a document along the named transition, with the
// assignments in values applied in the same commit (ADR-0040): the
// assignments, the recalculation, the guard over the recalculated
// document, the eventless transitions, the writes. The commit is version
// N+1 with one event of kind transition; every eventless transition that
// fires after it is a further commit in the same transaction, a version
// of its own with the same values and the new state. A transition that
// cannot happen is refused before anything is written, with
// unknown_transition, wrong_state, guard_false, too_many_transitions or
// terminal.
func (s *Store) Transition(ctx context.Context, class, id, name string, values Values, opts ...Option) (*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	res, err := s.commit(ctx, command{op: opTransition, c: c, id: id, transition: name, values: values, o: apply(opts)})
	if err != nil {
		return nil, err
	}
	return res.doc, nil
}

// Transitions lists the named transitions leaving the document's state,
// in definition order, each with whether the principal may take it now
// and, when not, why (ADR-0040, ADR-0041): forbidden when the
// transition's access rule is not TRUE for the principal, else
// guard_false, with the rule's or the guard's error when it errored. A
// timed transition is listed with the moment it fires (ADR-0047, item
// 8), one without a name too, which nobody takes. It evaluates them over
// the stored document and writes nothing. Eventless transitions are not
// listed. The rules and guards run on the writer's VM, since they may
// look a table up through the writer's statements, so the listing takes
// the writer's turn; it is short.
func (s *Store) Transitions(ctx context.Context, class, id string, opts ...Option) ([]TransitionInfo, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	// The listing returns no document, so it reads who made it only when
	// a formula of the class asks.
	read := c.w.selectLean
	if c.who {
		read = c.w.selectCurrent
	}
	row, err := c.scanW(read.QueryRowContext(ctx, id), !c.who)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errf(CodeUnknownDocument, "", "no document %s of class %s", id, c.cls.Name)
		}
		return nil, storageErr("read the document", err)
	}
	if c.cls.States == nil {
		return []TransitionInfo{}, nil
	}
	evalCtx := &formula.Context{User: o.principal.ID, Groups: o.principal.Groups, Now: s.clock(o), State: row.state, ID: row.id}
	return s.listTransitions(c, row, evalCtx, o.principal, false), nil
}

// listTransitions is the listing of Transitions and Preview over a row in
// its state, which evalCtx.State names, for the principal: the access
// rule first, then the guard, and for a timed transition the moment it
// fires. fresh says the command being previewed began the row's stay.
func (s *Store) listTransitions(c *class, row *docRow, evalCtx *formula.Context, p Principal, fresh bool) []TransitionInfo {
	out := make([]TransitionInfo, 0, len(c.cls.Transitions))
	for _, t := range c.cls.Transitions {
		if !state.From(t, row.state) || t.Name == "" && !t.Timed() {
			continue
		}
		info := TransitionInfo{Name: t.Name, To: t.To}
		if t.Timed() {
			at, ok := s.timerOf(c, t, row, evalCtx, fresh)
			if info.FiresAt = at; t.Name == "" {
				// Nobody takes it: it is listed for its moment alone, and
				// not at all when that never comes.
				if ok {
					out = append(out, info)
				}
				continue
			}
		}
		if ok, err := s.permitted(&s.wev, c, t, row, evalCtx, p); !ok {
			info.Code = CodeForbidden
			if err != nil {
				info.Message = "the rule failed: " + guardFailure(err)
			}
			out = append(out, info)
			continue
		}
		held, err := s.holds(c, t, row, evalCtx)
		switch {
		case held:
			info.Enabled = true
		case err != nil:
			info.Code, info.Message = CodeGuardFalse, "the guard failed: "+guardFailure(err)
		default:
			info.Code = CodeGuardFalse
		}
		out = append(out, info)
	}
	return out
}

// holds evaluates a transition's guard over the row: TRUE holds; FALSE, a
// blank, another type and an error do not (ADR-0019), the error returned
// for the message. A transition without a guard holds.
func (s *Store) holds(c *class, t *model.Transition, row *docRow, evalCtx *formula.Context) (bool, error) {
	if t.Guard == nil {
		return true, nil
	}
	val, err := s.evalRaw(c, t.Guard, row, evalCtx)
	if err != nil {
		return false, err
	}
	return val.Truth(), nil
}

// timerOf answers the moment a timed transition fires for the row, in
// seconds (ADR-0047, item 9): the entry into its state and the after, or
// what the at gives, a date as the first instant of its day in UTC. ok is
// false when it never comes: the at is blank, or failed.
//
// fresh says the command being computed began the row's stay. An after
// then counts from the command's moment. In a commit that is the time of
// the entry itself. A preview at a chosen moment stamps the entry with
// the database's clock (ADR-0042) and asks what a commit at its moment
// would do: for that commit the stay begins at that moment, and the
// after has not come.
func (s *Store) timerOf(c *class, t *model.Transition, row *docRow, evalCtx *formula.Context, fresh bool) (at int64, ok bool) {
	if t.After > 0 {
		entered := row.life.EnteredAt
		if fresh {
			entered = evalCtx.Now
		}
		return entered + t.After, true
	}
	val, err := s.evalRaw(c, t.At, row, evalCtx)
	if err != nil {
		return 0, false
	}
	switch val.Kind() {
	case formula.KindDate:
		return val.Days() * 86400, true
	case formula.KindDateTime:
		return val.Secs(), true
	}
	return 0, false
}

// fires reports whether a transition of the chain fires for the row at
// the context's moment (ADR-0047, item 10): an eventless one when its
// guard holds, a timed one when its time has come and its guard, if it
// has one, holds too.
func (s *Store) fires(c *class, t *model.Transition, row *docRow, evalCtx *formula.Context, fresh bool) bool {
	if t.Timed() {
		if at, ok := s.timerOf(c, t, row, evalCtx, fresh); !ok || at > evalCtx.Now {
			return false
		}
	}
	held, _ := s.holds(c, t, row, evalCtx)
	return held
}

// guard refuses the command when the named transition's guard does not
// hold over the recalculated document; evalCtx.State is the state the
// transition leaves.
func (s *Store) guard(c *class, t *model.Transition, row *docRow, evalCtx *formula.Context) *Error {
	held, err := s.holds(c, t, row, evalCtx)
	if held {
		return nil
	}
	msg := fmt.Sprintf("the guard of %s is not TRUE for the document in %s", t.Name, evalCtx.State)
	if err != nil {
		msg = fmt.Sprintf("the guard of %s failed for the document in %s: %s", t.Name, evalCtx.State, guardFailure(err))
	}
	return hintf(CodeGuardFalse, "", msg, "the guard: "+t.GuardSrc)
}

// guardFailure words a guard's evaluation error: its code and offset.
func guardFailure(err error) string {
	if ee, ok := err.(*formula.EvalError); ok {
		if ee.Pos >= 0 {
			return fmt.Sprintf("%s at offset %d", ee.Code, ee.Pos)
		}
		return ee.Code
	}
	return err.Error()
}

// resolveTransition finds the named transition a command means and
// checks that it leaves the document's state (ADR-0040).
func resolveTransition(c *class, name, from string) (*model.Transition, *Error) {
	cls := c.cls
	if cls.States == nil || len(state.Names(cls)) == 0 {
		return nil, hintf(CodeUnknownTransition, "", cls.Name+" has no named transitions", "declare states and transitions in the class")
	}
	t := state.Named(cls, name)
	if t == nil {
		return nil, hintf(CodeUnknownTransition, "", fmt.Sprintf("%s has no transition %q", cls.Name, name), "the transitions are: "+strings.Join(state.Names(cls), ", "))
	}
	if !state.From(t, from) {
		hint := "no transition leaves " + from
		if leaving := state.Leaving(cls, from); len(leaving) > 0 {
			names := make([]string, len(leaving))
			for i, l := range leaving {
				names[i] = l.Name
			}
			hint = "the transitions leaving " + from + ": " + strings.Join(names, ", ")
		}
		return nil, hintf(CodeWrongState, "", fmt.Sprintf("%s leaves %s; the document is in %s", t.Name, strings.Join(t.From, " or "), from), hint)
	}
	return t, nil
}
