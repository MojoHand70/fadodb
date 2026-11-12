package store

import (
	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// A triggered variable is computed at the moments its class names and
// kept in between (ADR-0015, ADR-0043): in the commit that raises one of
// its triggers, after the state change and before the rules, and when a
// refresh names it. The ones a commit recomputes run one at a time in
// declaration order, each over the document as it stands, and after each
// the auto variables that depend on it recalculate. The value carries its
// moment in two leaves beside it: the version of the document whose
// commit computed it, and the code of a recomputation that failed since.

// recomputeFor recomputes the triggered variables whose trigger a commit
// of the kind raised: enter of the initial state for a create; exit,
// transition and enter for a transition; exit and enter for an eventless
// one, and the transition too for a timed one that has a name. from is
// the state left and name the transition's, "" for one without. It
// leaves the context's state at the document's.
func (s *Store) recomputeFor(c *class, row *docRow, kind commitKind, from, name string, evalCtx *formula.Context, secs int64, preview bool, errs *[]*Error) *Error {
	for _, cv := range c.triggered {
		state, leaving, ok := triggerState(cv.v, kind, from, name, row.state)
		if !ok {
			continue
		}
		evalCtx.State = state
		// A variable of an exit or of a transition speaks of the state
		// left, and of the stay there (ADR-0047, item 5).
		leaving = leaving && c.life
		if leaving {
			row.swapStay()
		}
		err := s.recompute(c, cv, row, evalCtx, secs, preview, errs)
		if leaving {
			row.swapStay()
		}
		if err != nil {
			evalCtx.State = row.state
			return err
		}
	}
	evalCtx.State = row.state
	return nil
}

// triggerState says whether a commit raises one of the variable's
// triggers and what STATE() is while it recomputes: the first of the
// commit's triggers the variable lists, in the commit's order exit,
// transition, enter, decides, and STATE() is the state left for an exit
// and a transition and the state the document is in for an enter, as for
// a rule (ADR-0043, item 3). leaving says it is the state left.
func triggerState(v *model.Variable, kind commitKind, from, name, state string) (st string, leaving, ok bool) {
	switch kind {
	case commitCreate:
		if v.On("enter", state) {
			return state, false, true
		}
	case commitTransition, commitHop:
		if v.On("exit", from) {
			return from, true, true
		}
		if name != "" && v.On("transition", name) {
			return from, true, true
		}
		if v.On("enter", state) {
			return state, false, true
		}
	}
	return "", false, false
}

// recompute evaluates one triggered variable over the row and stores the
// value with its moment: the row's version, and secs as its time. A
// recomputation that errors, in the formula or in what the variable
// accepts, leaves the value and its moment as they were, records the code
// beside them and never refuses the command (ADR-0019). The auto
// variables that depend on a value that changed recalculate; one of them
// failing is the commit's error, as after any change.
func (s *Store) recompute(c *class, cv *cvar, row *docRow, evalCtx *formula.Context, secs int64, preview bool, errs *[]*Error) *Error {
	val, err := s.eval(c, cv.v.Formula, row, evalCtx)
	if err == nil {
		val, err = fit(cv.v, val)
	}
	if err != nil {
		row.vals[cv.errLeaf] = formula.Text(err.Code)
		return nil
	}
	changed := !formula.Equal(row.vals[cv.leaf], val)
	row.vals[cv.leaf] = val
	row.vals[cv.asof] = formula.Decimal(row.version, 0)
	row.vals[cv.errLeaf] = formula.Blank()
	row.ats[cv.tidx] = secs
	if !changed {
		return nil
	}
	dirty := s.dirtyFor(c)
	dirty[cv.idx] = true
	return s.recalc(c, row, dirty, false, evalCtx, preview, errs)
}
