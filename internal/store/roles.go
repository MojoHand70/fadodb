package store

import (
	"github.com/MojoHand70/fadodb/internal/formula"
)

// Three kinds of work are not about one document (ADR-0051): defining,
// operating the queue, and running SQL over every row. Each is for an
// administrator and for the members of the groups the host program names
// when it opens the database, in Options.Designers, Options.Operators and
// Options.Analysts. Who may rewrite the rules cannot be decided by a rule
// they may rewrite, so the lists come from outside the file, as the hosts
// a database may call and its secrets do.

// work names one of the three.
type work uint8

const (
	workDesign work = iota
	workOperate
	workAnalyse
)

// unnamedWorkIsOpen is what a kind of work answers when the host program
// names no list for it at all. ADR-0051 proposes that it is then left to
// administrators. Until the owner accepts that, the engine keeps what it
// has always done and asks nothing: a host that names a list, an empty
// one too, is held to it.
const unnamedWorkIsOpen = true

// permits refuses a principal who may not do the work: neither an
// administrator nor in a group the host program named for it.
func (s *Store) permits(w work, p Principal) *Error {
	var list []string
	var option, what string
	switch w {
	case workDesign:
		list, option, what = s.designers, "Designers", "define, preview a draft or migrate"
	case workOperate:
		list, option, what = s.operators, "Operators", "operate the outbox or try an action"
	default:
		list, option, what = s.analysts, "Analysts", "run SQL, which reads every row and every variable"
	}
	if p.Admin || list == nil && unnamedWorkIsOpen {
		return nil
	}
	for _, g := range list {
		for _, h := range p.Groups {
			if g == h {
				return nil
			}
		}
	}
	e := hintf(CodeForbidden, "", who(p)+" may not "+what,
		"this is for an administrator and for the members of the groups the host program names in Options."+option)
	e.Path = "/options/" + option
	return e
}

// worker is the check of a call that does one of the three kinds of
// work: its options applied, its principal's kind known, and the work
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
// clock; one that errors is FALSE. An administrator passes, and a rule
// the definition leaves out answers as a class's does (ADR-0049, the
// open question).
func (s *Store) tableRefusal(t *table, write bool, p Principal, now int64) *Error {
	if p.Admin {
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
	hint := t.def.Name + " has no " + kind + " rule, so only an administrator may; add access." + kind + " to the table"
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
