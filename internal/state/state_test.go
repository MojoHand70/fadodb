package state

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/model"
)

const flow = `{"name": "Flow", "variables": {"n": {"type": "number"}},
  "states": {"initial": "a", "list": ["a", "b", "c", "d"]},
  "transitions": [
    {"name": "go",   "from": "a", "to": "b"},
    {"name": "Back", "from": ["b", "c"], "to": "a", "guard": "n > 0"},
    {"from": "b", "to": "c", "guard": "n > 1"},
    {"from": "c", "to": "d", "guard": "n > 2"},
    {"from": "d", "to": "c", "guard": "n > 3"},
    {"name": "end",  "from": "d", "to": "a"}
  ]}`

func compile(t *testing.T) *model.Class {
	t.Helper()
	cls, diags := model.Define([]byte(flow), model.NewSchemaSet())
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	return cls
}

func names(ts []*model.Transition) string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return strings.Join(out, " ")
}

func TestNamedAndLeaving(t *testing.T) {
	cls := compile(t)
	if Named(cls, "GO") == nil || Named(cls, "back") == nil || Named(cls, "nope") != nil || Named(cls, "") != nil {
		t.Error("Named")
	}
	if got := strings.Join(Names(cls), " "); got != "go Back end" {
		t.Errorf("Names: %s", got)
	}
	if got := names(Leaving(cls, "b")); got != "Back" {
		t.Errorf("Leaving b: %q", got)
	}
	if got := names(Leaving(cls, "a")); got != "go" {
		t.Errorf("Leaving a: %q", got)
	}
	if got := Leaving(cls, "x"); len(got) != 0 {
		t.Errorf("Leaving x: %d", len(got))
	}
	if !From(Named(cls, "Back"), "c") || From(Named(cls, "Back"), "a") {
		t.Error("From")
	}
}

func TestChain(t *testing.T) {
	cls := compile(t)
	// The guards hold up to a value of n: the chain stops where they stop.
	for _, tc := range []struct {
		n    float64
		path string
		ok   bool
	}{
		{0, "b", true},
		{1, "b", true},
		{2, "b c", true},
		{3, "b c d", true},
		{4, "b c d c d c d c d", false},
	} {
		path, ok := Chain(cls, "b", []string{"b"}, func(tr *model.Transition, from string) bool {
			// The guards are n > 1, n > 2, n > 3 in definition order.
			switch tr.To {
			case "c":
				return tc.n > 1 && (from == "b" || tc.n > 3)
			case "d":
				return tc.n > 2
			}
			return false
		})
		if got := strings.Join(path, " "); got != tc.path || ok != tc.ok {
			t.Errorf("n=%v: path %q ok %v, want %q %v", tc.n, got, ok, tc.path, tc.ok)
		}
	}
	// Exactly the bound fires and is allowed; one more is not.
	fired := 0
	_, ok := Chain(cls, "c", nil, func(tr *model.Transition, from string) bool {
		fired++
		return fired <= MaxEventless
	})
	if !ok || fired != MaxEventless+1 {
		t.Errorf("at the bound: ok %v after %d evaluations", ok, fired)
	}
	fired = 0
	path, ok := Chain(cls, "c", nil, func(tr *model.Transition, from string) bool {
		fired++
		return fired <= MaxEventless+1
	})
	if ok || len(path) != MaxEventless {
		t.Errorf("past the bound: ok %v, %d states", ok, len(path))
	}
}

// A timed transition fires in the chain with a name or without one
// (ADR-0047, item 10); a named transition that is not timed never does.
func TestNext_Timed(t *testing.T) {
	cls, diags := model.Define([]byte(`{"name": "Wait", "variables": {"due": {"type": "date"}, "n": {"type": "number"}},
	  "states": {"initial": "a", "list": ["a", "b", "c"]},
	  "transitions": [
	    {"name": "go", "from": "a", "to": "b"},
	    {"name": "remind", "from": "a", "to": "a", "after": "3d"},
	    {"from": "a", "to": "c", "at": "due"},
	    {"from": "a", "to": "b", "guard": "n > 0"},
	    {"name": "back", "from": ["b", "c"], "to": "a", "guard": "n > 5"}
	  ]}`), model.NewSchemaSet())
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	var asked []string
	all := func(tr *model.Transition) bool {
		asked = append(asked, tr.Name+">"+tr.To)
		return false
	}
	if next := Next(cls, "a", all); next != nil {
		t.Errorf("nothing fires, and Next is %+v", next)
	}
	// The named one that is not timed is never asked; the others are, in
	// the order of the class.
	if got := strings.Join(asked, " "); got != "remind>a >c >b" {
		t.Errorf("asked %q", got)
	}
	for _, tr := range cls.Transitions {
		if want := tr.Name != "go" && tr.Name != "back"; Unasked(tr) != want {
			t.Errorf("Unasked(%s>%s) = %v", tr.Name, tr.To, !want)
		}
	}
	// The first that fires is taken, a named timed one too.
	if next := Next(cls, "a", func(tr *model.Transition) bool { return tr.To != "a" }); next == nil || next.To != "c" {
		t.Errorf("the at: %+v", next)
	}
	if next := Next(cls, "a", func(*model.Transition) bool { return true }); next == nil || next.Name != "remind" {
		t.Errorf("the reminder: %+v", next)
	}
	// From b only a named transition leaves: nothing fires by itself.
	if next := Next(cls, "b", func(*model.Transition) bool { return true }); next != nil {
		t.Errorf("out of b: %+v", next)
	}
	// The named transitions a command may take include the timed one.
	if got := names(Leaving(cls, "a")); got != "go remind" {
		t.Errorf("Leaving a: %q", got)
	}
}
