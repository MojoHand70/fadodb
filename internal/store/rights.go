package store

import (
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The rights table of ADR-0028 and the per-transition rules of ADR-0010
// are enforced here (ADR-0041): a transition's rule and the write rights
// are checked over the stored document before the assignments, and every
// document the API returns is projected for the principal, a variable
// whose read right is not TRUE in the row's state absent from its values.
// An administrator bypasses the rules and the rights, never a guard; a
// call without a principal is nobody. Formulas inside the class compute
// over every variable regardless: rights govern what a principal sees and
// may set, not what the engine computes.

// rightCell is the rights of one variable in one state, resolved once
// when the class is laid out (model.Class.ReadRule): nil means no rule.
type rightCell struct {
	read, write *model.RightRule
}

// evaluator runs programs over rows: a VM with the scratch of its slot
// values and a context it reuses. The writer owns one under its lock;
// a read path borrows one from the store's pool for its projection.
type evaluator struct {
	vm   *formula.VM
	vars []formula.Value
	ctx  formula.Context
}

// run evaluates a program over the row's values in the class's layout.
func (e *evaluator) run(c *class, prog *formula.Program, row *docRow, ctx *formula.Context) (formula.Value, error) {
	idx := c.slots[prog]
	if cap(e.vars) < len(idx) {
		e.vars = make([]formula.Value, max(len(idx), c.maxSlots))
	}
	vars := e.vars[:len(idx)]
	for i, li := range idx {
		vars[i] = row.vals[li]
	}
	return e.vm.Run(prog, vars, ctx)
}

// allowed evaluates a right over the row for the principal of ctx: a
// group list holds when the principal is in any of its groups, a formula
// when it is TRUE; a formula that errors is FALSE (ADR-0019), its error
// returned for the message.
func (e *evaluator) allowed(c *class, rule *model.RightRule, row *docRow, ctx *formula.Context) (bool, error) {
	if rule.Groups != nil {
		for _, g := range rule.Groups {
			for _, h := range ctx.Groups {
				if g == h {
					return true, nil
				}
			}
		}
		return false, nil
	}
	val, err := e.run(c, rule.Formula, row, ctx)
	if err != nil {
		return false, err
	}
	return val.Truth(), nil
}

// reader borrows an evaluator for a read path. Read rights use no lookups
// (ADR-0041), so its VM has no executor.
func (s *Store) reader() *evaluator {
	if e, ok := s.readers.Get().(*evaluator); ok {
		return e
	}
	vm := formula.NewVM()
	vm.Limits = s.limits
	return &evaluator{vm: vm}
}

func (s *Store) release(e *evaluator) { s.readers.Put(e) }

// cells is the resolved rights of the class's variables in a state; a
// state the table does not know reads the "*" row alone.
func (c *class) cells(state string) []rightCell {
	if cells, ok := c.rights[state]; ok {
		return cells
	}
	return c.rights[""]
}

// project removes from the document the variables the principal may not
// read in the row's state (ADR-0041). The rights table of the current
// class c applies by variable name to every row; a row computed by an
// older version vc is read into the current layout by path first. The
// system variables stay.
func (s *Store) project(e *evaluator, c, vc *class, row *docRow, d *Document, p Principal, now int64) {
	if c.rights == nil || p.Admin {
		return
	}
	cells := c.cells(row.state)
	e.ctx = formula.Context{User: p.ID, Groups: p.Groups, Now: now, State: row.state, ID: row.id}
	if vc != c {
		moved := &docRow{vals: make([]formula.Value, len(c.leaves))}
		for i, l := range c.leaves {
			if j, ok := vc.leafIndex[strings.ToUpper(l.path)]; ok {
				moved.vals[i] = row.vals[j]
			}
		}
		row = moved
	}
	for i, cell := range cells {
		if cell.read == nil {
			continue
		}
		if ok, _ := e.allowed(c, cell.read, row, &e.ctx); !ok {
			delete(d.Values, c.vars[i].v.Name)
		}
	}
}

// checkWrites refuses, with forbidden naming the variable, a value for a
// variable whose write right in the document's state is not TRUE for the
// principal (ADR-0041): over the stored document before the assignments,
// or over the new document after its defaults. A commit reports the first
// such variable in declaration order, a preview every one under its
// errors. A value for a name the class does not have is left to assign,
// which refuses it.
func (s *Store) checkWrites(c *class, cmd *command, row *docRow, evalCtx *formula.Context, res *result) *Error {
	if c.rights == nil || cmd.o.principal.Admin || len(cmd.values) == 0 {
		return nil
	}
	cells := c.cells(row.state)
	given := s.givenFor(c)
	for name := range cmd.values {
		cv, ok := c.exact[name]
		if !ok {
			cv, ok = c.byName[strings.ToUpper(name)]
		}
		if ok {
			given[cv.idx] = true
		}
	}
	for i, cv := range c.vars {
		if !given[i] || cells[i].write == nil {
			continue
		}
		held, err := s.wev.allowed(c, cells[i].write, row, evalCtx)
		if held {
			continue
		}
		e := forbiddenWrite(cmd.o.principal, cv.v.Name, row.state, cells[i].write, err)
		if !cmd.preview {
			return e
		}
		res.errs = append(res.errs, e)
	}
	return nil
}

// givenFor returns the store's flags of the variables a command sets, one
// per variable of the class, all false. The slice is reused under the
// writer's lock.
func (s *Store) givenFor(c *class) []bool {
	if cap(s.given) < len(c.vars) {
		s.given = make([]bool, len(c.vars))
	}
	g := s.given[:len(c.vars)]
	for i := range g {
		g[i] = false
	}
	return g
}

// transitionRule is the access rule of a named transition (ADR-0010),
// nil when the class declares none.
func transitionRule(c *class, t *model.Transition) *formula.Program {
	if c.cls.Access == nil {
		return nil
	}
	return c.cls.Access.Transition[t.Name]
}

// permitted evaluates a transition's access rule over the row for the
// principal: TRUE permits; FALSE, a blank, another type and an error do
// not, the error returned for the message. A transition without a rule,
// and every transition for an administrator, is permitted.
func (s *Store) permitted(e *evaluator, c *class, t *model.Transition, row *docRow, evalCtx *formula.Context, p Principal) (bool, error) {
	rule := transitionRule(c, t)
	if rule == nil || p.Admin {
		return true, nil
	}
	val, err := e.run(c, rule, row, evalCtx)
	if err != nil {
		return false, err
	}
	return val.Truth(), nil
}

// checkTransition refuses the command with forbidden when the named
// transition's access rule is not TRUE for the principal over the stored
// document, in the state it leaves (ADR-0041).
func (s *Store) checkTransition(c *class, t *model.Transition, row *docRow, evalCtx *formula.Context, p Principal) *Error {
	ok, err := s.permitted(&s.wev, c, t, row, evalCtx, p)
	if ok {
		return nil
	}
	return forbiddenTransition(p, t, row.state, c.cls.Def.Access.Transition[t.Name], err)
}

// who names the principal in a message.
func who(p Principal) string {
	if p.ID == "" {
		return "a call without a principal"
	}
	return p.ID
}

func forbiddenWrite(p Principal, variable, state string, rule *model.RightRule, err error) *Error {
	msg := fmt.Sprintf("%s may not set %s", who(p), variable)
	if state != "" {
		msg += " while the document is in " + state
	}
	if err != nil {
		msg += ": the write right failed, " + guardFailure(err)
	}
	return hintf(CodeForbidden, variable, msg, "the write right of "+variable+": "+ruleText(rule))
}

func forbiddenTransition(p Principal, t *model.Transition, state, src string, err error) *Error {
	msg := fmt.Sprintf("%s may not take %s from %s", who(p), t.Name, state)
	if err != nil {
		msg += ": the rule failed, " + guardFailure(err)
	}
	return hintf(CodeForbidden, "", msg, "the rule of "+t.Name+": "+src)
}

// ruleText words a right for a hint: the formula, or the groups.
func ruleText(rule *model.RightRule) string {
	if rule.Groups != nil {
		return "the groups " + rule.Src
	}
	return rule.Src
}
