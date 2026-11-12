package formula

import "encoding/json"

// What a document's life says of its states (ADR-0047, item 6). The
// facts are folded from the document's events, which the store reads
// from the log and the conformance suite gives inline, so that the fold
// is one piece of code and part of the specification.

// StateLife is what a document's life says of one state: when and by
// whom it first and last entered it and left it, how often it entered
// and left it, how long the stays that ended lasted, whether it is there
// now, and who first and last changed it there. Moments are seconds
// since the epoch; a zero moment is none and an empty text nobody. The
// JSON names are the ones a statement that runs over no document row
// reads the facts by (staysArg).
type StateLife struct {
	State          string `json:"state"`
	FirstEntered   int64  `json:"fe,omitempty"`
	FirstEnteredBy string `json:"feb,omitempty"`
	LastEntered    int64  `json:"le,omitempty"`
	LastEnteredBy  string `json:"leb,omitempty"`
	// FirstLeft and LastLeft are zero while Exits is.
	FirstLeft   int64  `json:"fl,omitempty"`
	FirstLeftBy string `json:"flb,omitempty"`
	LastLeft    int64  `json:"ll,omitempty"`
	LastLeftBy  string `json:"llb,omitempty"`
	Entries     int64  `json:"n"`
	Exits       int64  `json:"-"`
	// Seconds is the length of the stays that ended. The stay the
	// document is in, when In is set, began at LastEntered.
	Seconds     int64  `json:"secs"`
	In          bool   `json:"here"`
	FirstEditor string `json:"fed,omitempty"`
	LastEditor  string `json:"led,omitempty"`
}

// LifeEvent is one event of a document's life as the functions of a
// state read it: the document was created in a state, it was moved from
// one state into another, the same one perhaps, or it was changed where
// it is.
type LifeEvent struct {
	Kind     LifeEventKind
	From, To string
	// At is the moment of the event and By who made it, empty for
	// nobody.
	At int64
	By string
}

// LifeEventKind tells the events of a life apart.
type LifeEventKind uint8

// The kinds of LifeEvent.
const (
	// LifeCreated: the document was created in To.
	LifeCreated LifeEventKind = iota + 1
	// LifeMoved: a transition took it from From into To.
	LifeMoved
	// LifeChanged: a version was made that left it where it was.
	LifeChanged
)

// TellLife adds an event to what a life says of its states and returns
// the list, which it changes in place and may grow. The events are told
// in the order they happened.
//
// An entry is the creation and every move, one back into the same state
// too, which leaves the state and enters it at one moment. An editor of
// a state is who created the document there or changed it there. Who
// moved it in or out is not: the entries and the exits say that.
func TellLife(states []StateLife, ev LifeEvent) []StateLife {
	find := func(name string) *StateLife {
		for i := range states {
			if states[i].State == name {
				return &states[i]
			}
		}
		return nil
	}
	enter := func(name string) *StateLife {
		st := find(name)
		if st == nil {
			states = append(states, StateLife{State: name})
			st = &states[len(states)-1]
		}
		if st.Entries++; st.Entries == 1 {
			st.FirstEntered, st.FirstEnteredBy = ev.At, ev.By
		}
		st.LastEntered, st.LastEnteredBy, st.In = ev.At, ev.By, true
		return st
	}
	edit := func(st *StateLife) {
		if ev.By == "" {
			return
		}
		if st.FirstEditor == "" {
			st.FirstEditor = ev.By
		}
		st.LastEditor = ev.By
	}
	switch ev.Kind {
	case LifeCreated:
		edit(enter(ev.To))
	case LifeMoved:
		if st := find(ev.From); st != nil && st.In {
			if st.Exits++; st.Exits == 1 {
				st.FirstLeft, st.FirstLeftBy = ev.At, ev.By
			}
			st.LastLeft, st.LastLeftBy, st.In = ev.At, ev.By, false
			st.Seconds += ev.At - st.LastEntered
		}
		enter(ev.To)
	case LifeChanged:
		for i := range states {
			if states[i].In {
				edit(&states[i])
			}
		}
	}
	return states
}

// Tell adds an event to what the life says of its states, on a copy of
// the list, so that another life that shares it keeps its own.
func (l *Life) Tell(ev LifeEvent) {
	states := make([]StateLife, len(l.States), len(l.States)+1)
	copy(states, l.States)
	l.States = TellLife(states, ev)
}

// state finds what the life says of a state, nil when the document was
// never in it.
func (l *Life) state(name string) *StateLife {
	for i := range l.States {
		if l.States[i].State == name {
			return &l.States[i]
		}
	}
	return nil
}

// stateFacts names, for each function of a state that reads one fact,
// the fact by its name in the JSON a statement is bound. ENTRIES and
// DAYSIN compute theirs.
var stateFacts = map[string]string{
	"FIRSTENTERED": "fe", "FIRSTENTEREDBY": "feb",
	"LASTENTERED": "le", "LASTENTEREDBY": "leb",
	"FIRSTLEFT": "fl", "FIRSTLEFTBY": "flb",
	"LASTLEFT": "ll", "LASTLEFTBY": "llb",
	"FIRSTEDITOR": "fed", "LASTEDITOR": "led",
}

// StateFunction reports whether name is a function of a state.
func StateFunction(name string) bool {
	return stateFacts[name] != "" || name == "ENTRIES" || name == "DAYSIN"
}

// staysArg binds what the life says of its states for a statement that
// runs over no document row: a JSON array of the states, and NULL where
// there is no document.
func staysArg(ctx *Context) any {
	if ctx == nil || ctx.Doc == nil {
		return nil
	}
	if len(ctx.Doc.States) == 0 {
		return "[]"
	}
	text, err := json.Marshal(ctx.Doc.States)
	if err != nil {
		return nil
	}
	return string(text)
}
