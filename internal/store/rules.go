package store

import (
	"context"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The rules of a class run inside the commit (ADR-0043). Every commit
// raises a fixed list of triggers, decided by what the command did before
// any rule runs; the rules of each trigger run in the order the class
// declares them, each over the document as the commit has computed it so
// far; and a synchronous action changes that document before it is
// written. A rule's action raises no trigger: what follows a commit is
// the statechart's, one eventless transition at a time, each a commit of
// its own with its own rules. So one command runs at most nine commits
// and each rule at most once in each.

// ruleSet is the rules of a class by trigger, built when the class is
// laid out, so that a commit looks up the triggers it raised and touches
// nothing else.
type ruleSet struct {
	create, delete, change  []*crule
	enter, exit, transition map[string][]*crule
}

// crule is one rule laid out for the commit.
type crule struct {
	r *model.Rule
	// vars are the variables of a values_change rule, by index.
	vars []int
	acts []caction
}

// caction is one action of a rule laid out for the commit.
type caction struct {
	a    *model.Action
	sets []cset
	// resets are the inputs a reset names, in the order written; defaults
	// the ones among them that have a default, in dependency order.
	resets, defaults []*cvar
	// refresh are the triggered variables a refresh recomputes, in
	// declaration order.
	refresh []*cvar
}

// cset is one assignment of a set.
type cset struct {
	cv   *cvar
	prog *formula.Program
}

// commitKind is what a commit did, which decides the triggers it raises
// (ADR-0043, item 1).
type commitKind uint8

const (
	commitCreate     commitKind = iota // create, then enter of the initial state
	commitSet                          // values_change
	commitTransition                   // values_change, exit, transition, enter
	commitHop                          // an eventless transition: exit, enter
	commitDelete                       // delete
)

// emitted is a custom event a rule's emit asked for, with the commit of
// the command it belongs to: 0 the command's own, n its n-th eventless
// transition.
type emitted struct {
	commit int
	name   string
}

// hop is one eventless transition of a command, a commit of its own: the
// row it writes, and the composites a reset of its rules emptied, nil
// when none did and every composite is carried forward.
type hop struct {
	row  *docRow
	kids map[*child][]childRow
}

// ruleRun is what the rules of one commit run with, beside the row. The
// row is passed apart and never kept here: the context is held by the VM
// while a formula runs, and what shares a struct with it would be taken
// to the heap with it.
type ruleRun struct {
	c       *class
	preview bool
	res     *result
	commit  int
	evalCtx *formula.Context
	// secs is the commit's time, the time of the moment of a variable a
	// refresh recomputes.
	secs int64
}

// layoutRules binds the slots of the rules' formulas and indexes the
// rules by trigger.
func (c *class) layoutRules() error {
	if len(c.cls.Rules) == 0 {
		return nil
	}
	rs := &ruleSet{enter: map[string][]*crule{}, exit: map[string][]*crule{}, transition: map[string][]*crule{}}
	for _, r := range c.cls.Rules {
		cr := &crule{r: r}
		if r.If != nil {
			if err := c.bindSlots(r.If, r.Path()+"/if"); err != nil {
				return err
			}
		}
		for _, name := range r.Variables {
			cr.vars = append(cr.vars, c.byName[strings.ToUpper(name)].idx)
		}
		for _, a := range r.Actions {
			ca := caction{a: a}
			for _, set := range a.Sets {
				if err := c.bindSlots(set.Formula, a.Path()+"/values/"+set.Variable.Name); err != nil {
					return err
				}
				ca.sets = append(ca.sets, cset{cv: c.byName[strings.ToUpper(set.Variable.Name)], prog: set.Formula})
			}
			named := map[*cvar]bool{}
			for _, v := range a.Variables {
				cv := c.byName[strings.ToUpper(v.Name)]
				if a.Name == model.ActionRefresh {
					ca.refresh = append(ca.refresh, cv)
					continue
				}
				ca.resets = append(ca.resets, cv)
				named[cv] = true
			}
			for _, cv := range c.defaults {
				if named[cv] {
					ca.defaults = append(ca.defaults, cv)
				}
			}
			cr.acts = append(cr.acts, ca)
		}
		switch r.On {
		case "create":
			rs.create = append(rs.create, cr)
		case "delete":
			rs.delete = append(rs.delete, cr)
		case "values_change":
			rs.change = append(rs.change, cr)
		case "enter":
			rs.enter[r.State] = append(rs.enter[r.State], cr)
		case "exit":
			rs.exit[r.State] = append(rs.exit[r.State], cr)
		case "transition":
			rs.transition[r.Name] = append(rs.transition[r.Name], cr)
		}
	}
	c.rules = rs
	return nil
}

// changes reports, per variable of the class, whether its value differs
// between the row before and the document as the commit computed it
// (ADR-0043, item 1): any stored leaf of a scalar or an object, and for a
// composite what the commit found when it compared the rows. The row
// before may have been computed by an older version of the class; a
// variable it did not have was blank. The slice is reused under the
// writer's lock.
func (s *Store) changes(c, beforeClass *class, before, after *docRow, kids []*child) []bool {
	if cap(s.changed) < len(c.vars) {
		s.changed = make([]bool, len(c.vars))
	}
	changed := s.changed[:len(c.vars)]
	for i, cv := range c.vars {
		changed[i] = false
		for _, li := range cv.leaves {
			prev := formula.Blank()
			if beforeClass == c {
				prev = before.vals[li]
			} else if j, ok := beforeClass.leafIndex[strings.ToUpper(c.leaves[li].path)]; ok {
				prev = before.vals[j]
			}
			if !formula.Equal(prev, after.vals[li]) {
				changed[i] = true
				break
			}
		}
	}
	for _, ch := range kids {
		changed[ch.idx] = true
	}
	return changed
}

// fireCommit runs the rules of one commit over its row: the triggers of
// its kind in the order of ADR-0043, the rules of each in declaration
// order. The composites a reset emptied are left in s.emptied. from is
// the state a transition or a hop left and name the named transition
// taken; changed are the variables that changed, for a set and a
// transition. STATE() is the state left in an exit and a transition rule
// and the state the document is in everywhere else. It leaves the
// context's state at the document's.
func (s *Store) fireCommit(run *ruleRun, row *docRow, kind commitKind, from, name string, changed []bool) *Error {
	rs, state := run.c.rules, row.state
	run.evalCtx.State = state
	s.emptied = s.emptied[:0]
	switch kind {
	case commitCreate:
		if err := s.fire(run, row, rs.create); err != nil {
			return err
		}
		return s.fire(run, row, rs.enter[state])
	case commitDelete:
		return s.fire(run, row, rs.delete)
	}
	if kind != commitHop {
		for _, cr := range rs.change {
			for _, vi := range cr.vars {
				if changed[vi] {
					if err := s.runRule(run, row, cr); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	if kind == commitSet {
		return nil
	}
	run.evalCtx.State = from
	if err := s.fire(run, row, rs.exit[from]); err != nil {
		return err
	}
	if kind == commitTransition {
		if err := s.fire(run, row, rs.transition[name]); err != nil {
			return err
		}
	}
	run.evalCtx.State = state
	return s.fire(run, row, rs.enter[state])
}

// fire runs the rules of one trigger in declaration order.
func (s *Store) fire(run *ruleRun, row *docRow, rules []*crule) *Error {
	for _, cr := range rules {
		if err := s.runRule(run, row, cr); err != nil {
			return err
		}
	}
	return nil
}

// runRule evaluates a rule's condition and, when it is TRUE or absent,
// runs its actions in order. A condition that errors is FALSE (ADR-0019)
// and leaves no trace in a commit; a preview reports its code.
func (s *Store) runRule(run *ruleRun, row *docRow, cr *crule) *Error {
	preview := run.preview
	at := -1
	if preview {
		run.res.rules = append(run.res.rules, RuleRun{Commit: run.commit, Path: cr.r.Path()})
		at = len(run.res.rules) - 1
	}
	if cr.r.If != nil {
		val, err := s.evalRaw(run.c, cr.r.If, row, run.evalCtx)
		if err != nil {
			if preview {
				run.res.rules[at].Code = evalErr("", err).Code
			}
			return nil
		}
		if !val.Truth() {
			return nil
		}
	}
	if preview {
		run.res.rules[at].Ran = true
	}
	for i := range cr.acts {
		act := &cr.acts[i]
		var args Values
		var err *Error
		switch {
		case !act.a.Sync:
			// An action of the outbox: the commit queues it once the outbox
			// is built; until then a preview names it and nothing runs.
		case act.a.Name == model.ActionSet:
			args, err = s.runSet(run, row, act)
		case act.a.Name == model.ActionReset:
			args, err = s.runReset(run, row, act)
		case act.a.Name == model.ActionRefresh:
			args, err = s.runRefresh(run, row, act)
		case act.a.Name == model.ActionEmit:
			s.emits = append(s.emits, emitted{commit: run.commit, name: act.a.Event})
			if preview {
				args = Values{"event": act.a.Event}
			}
		}
		if err != nil {
			return err
		}
		if preview {
			run.res.rules[at].Actions = append(run.res.rules[at].Actions, ActionRun{Action: act.a.Name, Queued: !act.a.Sync, Args: args})
		}
	}
	return nil
}

// fail is what an action's error does (ADR-0043, item 6): it refuses the
// command, with the action's path; a preview records it and goes on.
func (run *ruleRun) fail(act *caction, err *Error) *Error {
	err.Path = act.a.Path()
	if !run.preview {
		return err
	}
	run.res.errs = append(run.res.errs, err)
	return nil
}

// runSet evaluates every formula of a set over the document as it
// stands, then assigns them all, then recalculates what depends on what
// changed. A value is checked like any other: type and scale, the enum
// list, the range, required. It is the engine's write, so no write right
// is consulted (ADR-0028).
func (s *Store) runSet(run *ruleRun, row *docRow, act *caction) (Values, *Error) {
	c := run.c
	vals := s.setVals[:0]
	for _, set := range act.sets {
		v := set.cv.v
		val, err := s.eval(c, set.prog, row, run.evalCtx)
		if err == nil {
			val, err = fit(v, val)
		}
		if err == nil && v.Required && val.IsBlank() {
			err = errf(CodeRequired, v.Name, "%s is required", v.Name)
		}
		if err != nil {
			err.Variable = v.Name
			if err := run.fail(act, err); err != nil {
				return nil, err
			}
			val = row.vals[set.cv.leaf]
		}
		vals = append(vals, val)
	}
	s.setVals = vals
	dirty := s.dirtyFor(c)
	touched := false
	for i, set := range act.sets {
		if !formula.Equal(row.vals[set.cv.leaf], vals[i]) {
			row.vals[set.cv.leaf] = vals[i]
			dirty[set.cv.idx] = true
			touched = true
		}
	}
	var args Values
	if run.preview {
		set := make(Values, len(act.sets))
		for i, cs := range act.sets {
			set[cs.cv.v.Name] = valueOut(vals[i])
		}
		args = Values{"values": set}
	}
	if touched {
		if err := s.recalcAfter(run, row, act, dirty); err != nil {
			return nil, err
		}
	}
	return args, nil
}

// runReset re-evaluates the defaults of the named inputs in dependency
// order; an input without a default becomes blank, an object in every
// field and a composite empty. Then it recalculates what depends on what
// changed. A required input left blank refuses the command.
func (s *Store) runReset(run *ruleRun, row *docRow, act *caction) (Values, *Error) {
	c := run.c
	dirty := s.dirtyFor(c)
	touched := false
	for _, cv := range act.resets {
		if cv.v.Default != nil {
			continue
		}
		if cv.child != nil {
			s.emptied = append(s.emptied, cv.child)
			continue
		}
		for _, li := range cv.leaves {
			if !row.vals[li].IsBlank() {
				row.vals[li] = formula.Blank()
				dirty[cv.idx] = true
				touched = true
			}
		}
	}
	for _, cv := range act.defaults {
		val, err := s.eval(c, cv.v.Default, row, run.evalCtx)
		if err == nil {
			val, err = fit(cv.v, val)
		}
		if err != nil {
			err.Variable = cv.v.Name
			if err := run.fail(act, err); err != nil {
				return nil, err
			}
			continue
		}
		if !formula.Equal(row.vals[cv.leaf], val) {
			row.vals[cv.leaf] = val
			dirty[cv.idx] = true
			touched = true
		}
	}
	for _, cv := range act.resets {
		if v := cv.v; v.Required && cv.leaf >= 0 && row.vals[cv.leaf].IsBlank() {
			if err := run.fail(act, errf(CodeRequired, v.Name, "%s is required and the reset leaves it blank", v.Name)); err != nil {
				return nil, err
			}
		}
	}
	var args Values
	if run.preview {
		names := make([]any, len(act.resets))
		for i, cv := range act.resets {
			names[i] = cv.v.Name
		}
		args = Values{"variables": names}
	}
	if touched {
		if err := s.recalcAfter(run, row, act, dirty); err != nil {
			return nil, err
		}
	}
	return args, nil
}

// runRefresh recomputes the triggered variables the action names, in
// declaration order, each with the moment of this commit. A recomputation
// that errors leaves its value as it was and is recorded beside it; it is
// no failure of the action (ADR-0043, item 9).
func (s *Store) runRefresh(run *ruleRun, row *docRow, act *caction) (Values, *Error) {
	had := len(run.res.errs)
	for _, cv := range act.refresh {
		if err := s.recompute(run.c, cv, row, run.evalCtx, run.secs, run.preview, &run.res.errs); err != nil {
			err.Path = act.a.Path()
			return nil, err
		}
	}
	for _, e := range run.res.errs[had:] {
		e.Path = act.a.Path()
	}
	if !run.preview {
		return nil, nil
	}
	names := make([]any, len(act.refresh))
	for i, cv := range act.refresh {
		names[i] = cv.v.Name
	}
	return Values{"variables": names}, nil
}

// recalcAfter recalculates the auto variables that depend on what an
// action changed. A formula that fails is the action's failure.
func (s *Store) recalcAfter(run *ruleRun, row *docRow, act *caction, dirty []bool) *Error {
	had := len(run.res.errs)
	if err := s.recalc(run.c, row, dirty, false, run.evalCtx, run.preview, &run.res.errs); err != nil {
		err.Path = act.a.Path()
		return err
	}
	for _, e := range run.res.errs[had:] {
		e.Path = act.a.Path()
	}
	return nil
}

// writeEmits appends the custom events the rules of one commit emitted
// (ADR-0043): each a row of the log of kind custom with the name the rule
// gave, caused by the commit's event. It carries no payload: a reader
// takes the document at the commit's version. After a delete there is no
// version made, and the event names the last one as its before_version,
// as the delete's own event does.
func (s *Store) writeEmits(ctx context.Context, c *class, commit int, docID string, before, after any, cause, actor string, day, secs int64, now time.Time) error {
	for _, e := range s.emits {
		if e.commit != commit {
			continue
		}
		ev := eventInfo{kind: EventCustom, qualifier: "emitted", name: e.name, cause: cause}
		if err := s.writeEvent(ctx, s.ids.next(now), c.cls.Name, docID, ev, actor, before, after, "", "", day, secs); err != nil {
			return err
		}
	}
	return nil
}
