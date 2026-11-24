package command

import (
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/store"
)

// The wait for changes is a command answered with one object (ADR-0064,
// ruling 3): the events after a place, the place to ask from next, and
// whether there is more. Bob files invoices; Ann, of finance, asks what
// changed since her place, a page at a time.
func TestTheWaitIsACommand(t *testing.T) {
	s := open(t)
	define(t, s)
	cmd, _ := Lookup("wait_for_changes")
	if cmd.Stream || cmd.Mark != Reads || cmd.Call != "WaitForChanges" || len(cmd.Answer) != 3 {
		t.Errorf("the row of wait_for_changes: %+v", cmd)
	}

	start := run(t, s, ann, "wait_for_changes", `{}`)
	if at(t, start, "after") != store.LogStart || at(t, start, "more") != false || len(at(t, start, "events").([]any)) != 0 {
		t.Fatalf("a wait on an empty log answered %v", start)
	}
	for _, number := range []string{"FV/1", "FV/2", "FV/3"} {
		run(t, s, bob, "create", `{"class": "Invoice", "id": "`+number+`", "values": {"number": "`+number+`"}}`)
	}
	first := run(t, s, ann, "wait_for_changes", `{"classes": ["Invoice"], "kinds": ["created"], "after": "start", "limit": 2}`)
	events := at(t, first, "events").([]any)
	if len(events) != 2 || at(t, first, "more") != true {
		t.Fatalf("the first page: %v", first)
	}
	last := events[1].(map[string]any)
	if last["doc_id"] != "FV/2" || last["kind"] != "created" || last["class"] != "Invoice" || at(t, first, "after") != last["event_id"] {
		t.Errorf("the events of the first page: %v", events)
	}
	rest := run(t, s, ann, "wait_for_changes", `{"after": "`+at(t, first, "after").(string)+`", "seconds": 0}`)
	if events := at(t, rest, "events").([]any); len(events) != 1 || events[0].(map[string]any)["doc_id"] != "FV/3" || at(t, rest, "more") != false {
		t.Errorf("the rest: %v", rest)
	}

	// What is not the object of the wait.
	for _, c := range []struct{ object, path, says string }{
		{`{"seconds": 61}`, "/seconds", "from 0 to 60 seconds"},
		{`{"seconds": "ten"}`, "/seconds", "whole number"},
		{`{"seconds": 99999999999999}`, "/seconds", "from 0 to 60"},
		{`{"limit": 1001}`, "/limit", "from 1 to 1000"},
		{`{"after": "FV/1"}`, "", "no event of the log"},
		{`{"at": "2026-11-13"}`, "/at", `takes no "at"`},
		{`{"key": "k-1"}`, "/key", `takes no "key"`},
	} {
		e := refused(t, s, ann, "wait_for_changes", c.object)
		if e.Path != c.path || !strings.Contains(e.Message, c.says) {
			t.Errorf("%s refused with %+v", c.object, e)
		}
	}
}
