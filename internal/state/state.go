// Package state is the statechart engine of ADR-0008 and ADR-0040: which
// named transition a command means, which transitions leave a state, and
// the chain of eventless transitions a commit follows after its
// recalculation, with its bound. It evaluates no guard itself: the store,
// which holds the document and the VM, reports whether each guard holds.
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

// Chain follows the eventless transitions from a state: at every step the
// first one in definition order that leaves the current state and whose
// guard holds, as fires reports it, until none does. The states entered
// are appended to path, which the caller gives with the states visited so
// far. ok is false when more than MaxEventless fired; path then ends at
// the bound. The loop allocates nothing beyond the growth of path.
func Chain(cls *model.Class, from string, path []string, fires func(t *model.Transition, from string) bool) (_ []string, ok bool) {
	for n := 0; ; n++ {
		var next *model.Transition
		for _, t := range cls.Transitions {
			if t.Name == "" && From(t, from) && fires(t, from) {
				next = t
				break
			}
		}
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
