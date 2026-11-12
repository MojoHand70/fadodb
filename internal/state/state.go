// Package state is the statechart engine of ADR-0008 and ADR-0040: which
// named transition a command means, which transitions leave a state, and
// the chain of transitions a commit follows by itself after its
// recalculation, with its bound: the eventless ones, and the timed ones
// of ADR-0047, with a name or without. It evaluates no guard and reads no
// clock itself: the store, which holds the document and the VM, reports
// whether each fires.
package state

import (
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// MaxEventless is the bound of eventless transitions one command may fire
// (ADR-0008); past it the commit is refused with too_many_transitions.
const MaxEventless = 8

// Named returns the class's named transition, matched without regard to
// case, or nil when there is none.
func Named(cls *model.Class, name string) *model.Transition {
	for _, t := range cls.Transitions {
		if t.Name != "" && strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return nil
}

// Names lists the names of the class's named transitions in definition
// order, for messages.
func Names(cls *model.Class) []string {
	var out []string
	for _, t := range cls.Transitions {
		if t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}

// From reports whether the transition leaves the state.
func From(t *model.Transition, state string) bool {
	for _, s := range t.From {
		if s == state {
			return true
		}
	}
	return false
}

// Leaving lists the named transitions whose sources include the state,
// in definition order: what Transitions and Preview list (ADR-0040).
func Leaving(cls *model.Class, state string) []*model.Transition {
	var out []*model.Transition
	for _, t := range cls.Transitions {
		if t.Name != "" && From(t, state) {
			out = append(out, t)
		}
	}
	return out
}

// Unasked reports whether the transition may fire in the chain of a
// commit, with nobody asking for it: it has no name, or it is timed
// (ADR-0047, item 10). A timed transition with a name is both: it fires
// by itself when its time has come, and a command may take it before.
func Unasked(t *model.Transition) bool { return t.Name == "" || t.Timed() }

// Next is one step of the chain: the first transition in definition
// order that leaves the state, may fire unasked and does, as fires
// reports it, or nil when none does: an eventless one when its guard
// holds, a timed one when its time has come and its guard holds. A commit
// takes the steps one at a time, since each one that fires is a commit of
// its own whose rules may change what the next guard reads (ADR-0043).
func Next(cls *model.Class, from string, fires func(t *model.Transition) bool) *model.Transition {
	for _, t := range cls.Transitions {
		if Unasked(t) && From(t, from) && fires(t) {
			return t
		}
	}
	return nil
}

// Chain follows the transitions that fire unasked from a state: at every
// step the first one in definition order that leaves the current state
// and fires, as fires reports it, until none does. The states entered
// are appended to path, which the caller gives with the states visited so
// far. ok is false when more than MaxEventless fired; path then ends at
// the bound. The loop allocates nothing beyond the growth of path.
func Chain(cls *model.Class, from string, path []string, fires func(t *model.Transition, from string) bool) (_ []string, ok bool) {
	for n := 0; ; n++ {
		next := Next(cls, from, func(t *model.Transition) bool { return fires(t, from) })
		if next == nil {
			return path, true
		}
		if n == MaxEventless {
			return path, false
		}
		from = next.To
		path = append(path, from)
	}
}
