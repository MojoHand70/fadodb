package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The four rules of a class, read, create, update and delete (ADR-0010),
// are enforced here (ADR-0049): each is a condition over a document for
// the principal of the call. A caller the read rule does not let through
// is answered as if the document did not exist, on every path; a caller
// who sees a document and may not do what they ask is refused with
// forbidden and the rule. An administrator passes every rule and never a
// guard. What the engine does by itself asks nothing: an eventless or a
// timed transition, the wake-up of a timer, the result an action writes
// back, a migration.

// accessRule names one of the four rules of a class.
type accessRule uint8

const (
	ruleRead accessRule = iota
	ruleCreate
	ruleUpdate
	ruleDelete
)

func (k accessRule) String() string {
	return [...]string{"read", "create", "update", "delete"}[k]
}

// leftOutAllows is what a rule answers that a class leaves out: the
// open question A of ADR-0049, kept in one place for the store and the
// filter compiler.
const leftOutAllows = model.LeftOutAllows

// ruled says whether the class has an answer of its own for the rule: the
// rule, or the refusal of a rule left out.
func (c *class) ruled(k accessRule) bool {
	return c.access[k] != nil || !leftOutAllows
}

// may reports whether a rule of the class lets the principal through
// over the row: TRUE does; FALSE, a blank, a value of another type and an
// error do not (ADR-0019), the error returned for the message.
func (e *evaluator) may(c *class, k accessRule, row *docRow, ctx *formula.Context, p Principal) (bool, error) {
	if p.Admin {
		return true, nil
	}
	prog := c.access[k]
	if prog == nil {
		return leftOutAllows, nil
	}
	val, err := e.run(c, prog, row, ctx)
	if err != nil {
		return false, err
	}
	return val.Truth(), nil
}

// asksRules says whether a command asks the rules of the class at all.
// An administrator passes them. The engine's own commands ask nothing
// (ADR-0049, item 8): the result an action writes back, the wake-up of a
// timer and a migration.
func asksRules(cmd *command) bool {
	return !cmd.o.principal.Admin && !cmd.engine && cmd.op != opWake && cmd.op != opMigrate
}

// noDocument is the answer for a document that does not exist, and for
// one the caller may not see (ADR-0049, item 3): the same words.
func noDocument(c *class, id string) *Error {
	return errf(CodeUnknownDocument, "", "no document %s of class %s", id, c.cls.Name)
}

// checkRead answers a command on a stored document whose caller the read
// rule does not let through: the document does not exist for them. The
// worker's command is told the true reason, forbidden, since its row is
// read by whoever operates the queue (ADR-0049, item 9). The rule runs
// over the stored document under the current layout.
func (s *Store) checkRead(c, beforeClass *class, before *docRow, cmd *command, evalCtx *formula.Context) *Error {
	if !c.ruled(ruleRead) || !asksRules(cmd) {
		return nil
	}
	row := before
	if beforeClass != c {
		row = c.adopted(beforeClass, before)
	}
	evalCtx.ID, evalCtx.State = before.id, before.state
	ok, err := s.wev.may(c, ruleRead, row, evalCtx, cmd.o.principal)
	if ok {
		return nil
	}
	if cmd.internal {
		return forbiddenBy(c, ruleRead, cmd.o.principal, before.id, err)
	}
	return noDocument(c, cmd.id)
}

// checkRule refuses a command the rule does not let through, with
// forbidden and the rule (ADR-0049, item 4). A preview reports it under
// its errors and goes on, as it does a write right.
func (s *Store) checkRule(c *class, k accessRule, row *docRow, cmd *command, evalCtx *formula.Context, res *result) *Error {
	if !c.ruled(k) || !asksRules(cmd) {
		return nil
	}
	ok, err := s.wev.may(c, k, row, evalCtx, cmd.o.principal)
	if ok {
		return nil
	}
	e := forbiddenBy(c, k, cmd.o.principal, row.id, err)
	if !cmd.preview {
		return e
	}
	res.errs = append(res.errs, e)
	return nil
}

// forbiddenBy is the refusal of a rule of the class: who, what and which
// document, the place of the rule in the class as the path, and the rule
// as the hint, or that the class has none and what to add.
func forbiddenBy(c *class, k accessRule, p Principal, id string, err error) *Error {
	name := c.cls.Name
	var what string
	switch k {
	case ruleCreate:
		what = "create this document of " + name
	case ruleUpdate:
		what = "change the document " + id + " of " + name
	case ruleDelete:
		what = "delete the document " + id + " of " + name
	default:
		what = "see the document " + id + " of " + name
	}
	msg := who(p) + " may not " + what
	if err != nil {
		msg += ": the rule failed, " + guardFailure(err)
	}
	hint := "the " + k.String() + " rule of " + name + ": " + c.accessSrc[k]
	if c.access[k] == nil {
		hint = name + " has no " + k.String() + " rule, so only an administrator may; add access." + k.String() + " to the class"
	}
	e := hintf(CodeForbidden, "", msg, hint)
	e.Path = "/access/" + k.String()
	return e
}

// veil empties the document a command answers with when its caller may
// not see it as the command left it (ADR-0049, item 7): the id, the
// version and the state stay, the values and the title go. The worker's
// commands answer the engine, not the person, and are left whole.
func (s *Store) veil(c *class, row *docRow, d *Document, cmd *command, evalCtx *formula.Context) {
	if !c.ruled(ruleRead) || !asksRules(cmd) || cmd.internal {
		return
	}
	evalCtx.ID, evalCtx.State = row.id, row.state
	if ok, _ := s.wev.may(c, ruleRead, row, evalCtx, cmd.o.principal); !ok {
		bare(d)
	}
}

// bare leaves of a document what a caller who may not see it is told.
func bare(d *Document) {
	d.Title, d.Values, d.Computed = "", Values{}, nil
}

// seesRow reports whether the principal of a read may see a document,
// given its row as the class version vc computed it. A read rule uses no
// lookup and no function of a state (ADR-0010, ADR-0047), so a borrowed
// evaluator runs it.
func (s *Store) seesRow(c, vc *class, row *docRow, o options) bool {
	if !c.ruled(ruleRead) || o.principal.Admin {
		return true
	}
	if vc != c {
		row = c.adopted(vc, row)
	}
	ev := s.reader()
	defer s.release(ev)
	ev.ctx = contextOf(o.principal, s.clock(o))
	ev.ctx.State, ev.ctx.ID = row.state, row.id
	ok, _ := ev.may(c, ruleRead, row, &ev.ctx, o.principal)
	return ok
}

// sees reports whether the principal of a read may see the document with
// the id: the read rule over the document as it is stored now, and over
// its last version when it was deleted (ADR-0049, item 6). It is false
// for an id no document has, which the caller answers in its own words,
// as it does a document that is hidden. A class with no answer of its own
// for the rule, and an administrator, cost no read.
func (s *Store) sees(ctx context.Context, c *class, id string, o options) (bool, error) {
	if !c.ruled(ruleRead) || o.principal.Admin {
		return true, nil
	}
	history := false
	row, err := c.scanRow(c.r.getCurrent.QueryRowContext(ctx, id), false)
	if err == sql.ErrNoRows {
		history = true
		row, err = c.scanRow(c.r.getLast.QueryRowContext(ctx, id), true)
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, storageErr("read the document", err)
	}
	vc, row, err := s.underVersion(ctx, c, row, history)
	if err != nil {
		return false, err
	}
	return s.seesRow(c, vc, row, o), nil
}

// hiddenFor names, upper-cased, the variables of a row the principal may
// not read: the rights table of the current class c applies by name to
// the row as the class version vc computed it, in the row's state, as a
// projection applies it (ADR-0041, item 4). nil when nothing is hidden.
func (s *Store) hiddenFor(c, vc *class, row *docRow, o options) map[string]bool {
	if c.rights == nil || o.principal.Admin {
		return nil
	}
	if vc != c {
		row = c.adopted(vc, row)
	}
	ev := s.reader()
	defer s.release(ev)
	ev.ctx = contextOf(o.principal, s.clock(o))
	ev.ctx.State, ev.ctx.ID = row.state, row.id
	var hidden map[string]bool
	for i, cell := range c.cells(row.state) {
		if cell.read == nil {
			continue
		}
		if ok, _ := ev.allowed(c, cell.read, row, &ev.ctx); !ok {
			if hidden == nil {
				hidden = map[string]bool{}
			}
			hidden[strings.ToUpper(c.vars[i].v.Name)] = true
		}
	}
	return hidden
}
