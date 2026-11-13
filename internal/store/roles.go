package store

import (
	"github.com/MojoHand70/fadodb/internal/formula"
)

// Three kinds of work are not about one document (ADR-0051), and each is
// for the members of one system group (ADR-0052): designers define,
// database administrators operate the queue and the blobs, and analysts
// run SQL over every row. The host program says who is in a system group
// when it names the caller, in Principal.System: who may rewrite the
// rules cannot be decided by a rule they may rewrite, so no formula,
// class or table gives one. Work nobody was given is done by nobody, and
// a call without a principal is nobody.

// work names one of the three.
type work uint8

const (
	workDesign work = iota
	workOperate
	workAnalyse
)

// permits refuses a principal who is not in the system group the work is
// for.
func (s *Store) permits(w work, p Principal) *Error {
	var group, what string
	switch w {
	case workDesign:
		group, what = GroupDesigners, "define, preview a draft or migrate"
	case workOperate:
		group, what = GroupAdministrators, "operate the outbox, try an action or check the blobs"
	default:
		group, what = GroupAnalysts, "run SQL, which reads every row and every variable"
	}
	if p.in(group) {
		return nil
	}
	e := hintf(CodeForbidden, "", who(p)+" may not "+what,
		"this is for the members of the system group "+group+"; the host program names them in Principal.System")
	e.Path = "/system/" + group
	return e
}

// worker is the check of a call that does one of the three kinds of
// work: its options applied, its principal sound, and the work
// permitted.
func (s *Store) worker(w work, opts []Option) (options, *Error) {
	o, e := asked(opts)
	if e != nil {
		return o, e
	}
	return o, s.permits(w, o.principal)
}

// tableRefusal refuses a principal the rule of a table does not let
// through (ADR-0051, item 7): read for its rows, write for an import and
// for a rule's table action, which asks as the person who set the rule
// off (ADR-0044, item 11). A rule is a condition over the caller and the
// clock; one that errors is FALSE. A content administrator passes
// (ADR-0052), and a rule the definition leaves out answers as a class's
// does.
func (s *Store) tableRefusal(t *table, write bool, p Principal, now int64) *Error {
	if p.content() {
		return nil
	}
	prog, kind, what := t.def.Read, "read", "read the rows of "
	if write {
		prog, kind, what = t.def.Write, "write", "write the rows of "
	}
	held := leftOutAllows
	var failure error
	if prog != nil {
		ev := s.reader()
		ev.ctx = contextOf(p, now)
		var val formula.Value
		if val, failure = ev.vm.Run(prog, nil, &ev.ctx); failure == nil {
			held = val.Truth()
		} else {
			held = false
		}
		s.release(ev)
	}
	if held {
		return nil
	}
	msg := who(p) + " may not " + what + t.def.Name
	if failure != nil {
		msg += ": the rule failed, " + guardFailure(failure)
	}
	hint := t.def.Name + " has no " + kind + " rule, so only a content administrator may; add access." + kind + " to the table"
	if prog != nil {
		src := t.def.Def.Access.Read
		if write {
			src = t.def.Def.Access.Write
		}
		hint = "the " + kind + " rule of " + t.def.Name + ": " + src
	}
	e := hintf(CodeForbidden, "", msg, hint)
	e.Path = "/access/" + kind
	return e
}
