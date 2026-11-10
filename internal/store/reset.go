package store

import (
	"context"
	"strings"
)

// Reset puts the named inputs of a document back to their defaults, as a
// new version (ADR-0015, ADR-0043): the defaults are re-evaluated in
// dependency order and an input without one becomes blank, an object in
// every field and a composite empty. It is the caller's write: it names
// at least one variable, each an input, and the write right of each is
// checked as for a set. What depends on what changed recalculates, the
// rules on values_change of what changed run, and the eventless
// transitions follow. A reset that changes nothing commits nothing and
// returns the document as it is. The event is of kind reset.
func (s *Store) Reset(ctx context.Context, class, id string, variables []string, opts ...Option) (*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	res, err := s.commit(ctx, command{op: opReset, c: c, id: id, variables: variables, o: apply(opts)})
	if err != nil {
		return nil, err
	}
	return res.doc, nil
}

// Refresh recomputes the named triggered variables of a document, as a
// new version (ADR-0015, ADR-0043): each must list refresh among its
// triggers, and with none named every variable that does recomputes, in
// declaration order. Each takes the moment of this commit; one whose
// formula errors keeps its value and records the code. A refresh always
// commits, since the moment is what it changes, and the eventless
// transitions are evaluated after it: it is how a caller makes a document
// look at the clock, or at what its formulas read outside it. It asks no
// write right. The event is of kind refresh.
func (s *Store) Refresh(ctx context.Context, class, id string, variables []string, opts ...Option) (*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	res, err := s.commit(ctx, command{op: opRefresh, c: c, id: id, variables: variables, o: apply(opts)})
	if err != nil {
		return nil, err
	}
	return res.doc, nil
}

// resetTargets resolves the inputs a Reset names, in the order given.
func (c *class) resetTargets(names []string) ([]*cvar, *Error) {
	if len(names) == 0 {
		return nil, hintf(CodeBadValue, "", "a reset names at least one variable", "name the inputs to put back to their defaults")
	}
	out := make([]*cvar, 0, len(names))
	for _, name := range names {
		cv, ok := c.byName[strings.ToUpper(name)]
		if !ok {
			return nil, errf(CodeUnknownVariable, name, "the class %s has no variable %s", c.cls.Name, name)
		}
		if cv.v.Computed {
			hint := "a reset re-evaluates the defaults of input variables"
			if cv.v.Triggered() {
				hint = "Refresh recomputes a triggered variable"
			}
			return nil, hintf(CodeReadOnly, cv.v.Name, "a computed variable takes its value from its formula", hint)
		}
		for _, seen := range out {
			if seen == cv {
				return nil, errf(CodeDuplicate, cv.v.Name, "the variable is named twice")
			}
		}
		out = append(out, cv)
	}
	return out, nil
}

// defaultsAmong lists the named inputs that have a default, in the
// class's dependency order.
func (c *class) defaultsAmong(named []*cvar) []*cvar {
	var out []*cvar
	for _, cv := range c.defaults {
		for _, n := range named {
			if n == cv {
				out = append(out, cv)
				break
			}
		}
	}
	return out
}

// refreshTargets resolves the triggered variables a Refresh recomputes,
// in declaration order: the ones named, each of which must list refresh,
// or every one that does when none is named.
func (c *class) refreshTargets(names []string) ([]*cvar, *Error) {
	named := make(map[*cvar]bool, len(names))
	for _, name := range names {
		cv, ok := c.byName[strings.ToUpper(name)]
		switch {
		case !ok:
			return nil, errf(CodeUnknownVariable, name, "the class %s has no variable %s", c.cls.Name, name)
		case !cv.v.Triggered():
			return nil, hintf(CodeNotAllowed, cv.v.Name, "a refresh recomputes triggered variables; "+cv.v.Name+" is not one", `a triggered variable has "mode": "triggered" and lists its triggers under "on"`)
		case !cv.v.On("refresh", ""):
			return nil, hintf(CodeNotAllowed, cv.v.Name, cv.v.Name+" does not list refresh among its triggers", `add "refresh" to its "on" in the class`)
		case named[cv]:
			return nil, errf(CodeDuplicate, cv.v.Name, "the variable is named twice")
		}
		named[cv] = true
	}
	var out []*cvar
	for _, cv := range c.triggered {
		if cv.v.On("refresh", "") && (len(names) == 0 || named[cv]) {
			out = append(out, cv)
		}
	}
	return out, nil
}
