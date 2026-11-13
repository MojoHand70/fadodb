package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A Reset as a command empties a rows value and an object, re-evaluates a
// default and blanks a scalar without one, in one commit with an event of
// kind reset; one that leaves a required input blank, or whose default
// errors, is refused and writes nothing.
func TestReset_Command(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Form",
 "variables": {
   "name":    {"type": "text", "required": true},
   "parts":   {"type": "number", "default": "4"},
   "share":   {"type": "number", "default": "100 / parts"},
   "lines":   {"type": "rows", "schema": {"sku": "text", "qty": "int"}},
   "tags":    {"type": "array<text>"},
   "contact": {"type": "object", "schema": "Person"},
   "count":   {"formula": "parts + 1"}
 },
 "states": {"initial": "open", "list": ["open"]}}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Form", Values{"name": "n", "parts": 5, "share": 7,
		"lines": []any{Values{"sku": "A", "qty": 1}}, "tags": []string{"x"}, "contact": Values{"id": "anna", "name": "Anna"}}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	doc, err = s.Reset(ctx, "Form", doc.ID, []string{"lines", "contact", "share", "parts"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != 2 || len(doc.Values["lines"].([]any)) != 0 || doc.Values["contact"] != nil || doc.Values["parts"] != 4.0 || doc.Values["share"] != 25.0 || doc.Values["count"] != 5.0 {
		t.Errorf("after the reset: %s", dump(doc))
	}
	if tags := doc.Values["tags"].([]any); len(tags) != 1 {
		t.Errorf("tags, which the reset did not name: %v", tags)
	}
	// The same reset again changes nothing, an emptied composite included.
	again, err := s.Reset(ctx, "Form", doc.ID, []string{"lines", "contact", "share", "parts"}, As(alice))
	if err != nil || again.Version != 2 {
		t.Errorf("a reset that changes nothing: version %d, %v", again.Version, err)
	}
	// A composite alone is a change.
	doc, err = s.Reset(ctx, "Form", doc.ID, []string{"tags"}, As(alice))
	if err != nil || doc.Version != 3 || len(doc.Values["tags"].([]any)) != 0 {
		t.Errorf("the reset of tags: %v, %s", err, dump(doc))
	}
	// Refusals: a required input without a default, a default that errors.
	if _, err := s.Reset(ctx, "Form", doc.ID, []string{"name"}, As(alice)); code(err) != CodeRequired {
		t.Errorf("the reset of a required input: %v", err)
	}
	if _, err := s.Set(ctx, "Form", doc.ID, Values{"parts": 0}, As(alice)); err != nil {
		t.Fatal(err)
	}
	_, err = s.Reset(ctx, "Form", doc.ID, []string{"share"}, As(alice))
	if se, _ := err.(*Error); se == nil || se.Code != "div_by_zero" || se.Variable != "share" || se.Path != "" {
		t.Errorf("the reset whose default errors: %v", err)
	}
	if _, err := s.Reset(ctx, "Form", doc.ID, []string{"tags"}, As(alice), At(start)); code(err) != CodeNotAllowed {
		t.Errorf("a reset at a chosen moment: %v", err)
	}
	events, err := s.Events(ctx, "Form", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.ActorID != "alice" {
			t.Errorf("the %s event has the actor %q", e.Kind, e.ActorID)
		}
	}
	if strings.Join(kinds, " ") != "created reset reset set" {
		t.Errorf("events %v", kinds)
	}
}

// A Refresh on a class with nothing to recompute is still a commit, and
// the eventless transitions are evaluated after it: a caller makes a
// document look at the clock with it (ADR-0043, item 10).
func TestRefresh_MakesADocumentLookAtTheClock(t *testing.T) {
	day := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	now := day
	s, _ := openStore(t, Options{Now: func() time.Time { return now }})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(`{"name": "Bill", "variables": {"due": {"type": "date"}},
 "states": {"initial": "sent", "list": ["sent", "late"]},
 "transitions": [{"from": "sent", "to": "late", "guard": "TODAY() > due"}]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Bill", Values{"due": "2026-03-12"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err = s.Refresh(ctx, "Bill", doc.ID, nil, As(alice), Key("check-0310"))
	if err != nil || doc.State != "sent" || doc.Version != 2 {
		t.Fatalf("a refresh before the due date: %v, %s at %d", err, doc.State, doc.Version)
	}
	now = day.AddDate(0, 0, 3)
	doc, err = s.Refresh(ctx, "Bill", doc.ID, nil, As(alice), Key("check-0313"))
	if err != nil || doc.State != "late" || doc.Version != 4 {
		t.Fatalf("a refresh after the due date: %v, %s at %d", err, doc.State, doc.Version)
	}
	// The repeat of a refresh that started a chain answers its last version.
	again, err := s.Refresh(ctx, "Bill", doc.ID, nil, As(alice), Key("check-0313"))
	if err != nil || again.Version != 4 || again.State != "late" {
		t.Errorf("the repeat: %v, %s at %d", err, again.State, again.Version)
	}
	events, _ := s.Events(ctx, "Bill", doc.ID)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, " ") != "created refresh refresh transition" {
		t.Errorf("events %v", kinds)
	}
	if _, err := s.Refresh(ctx, "Bill", doc.ID, nil, At(day)); code(err) != CodeNotAllowed {
		t.Errorf("a refresh at a chosen moment: %v", err)
	}
	var qualifier string
	if err := s.db.QueryRow(`SELECT d.qualifier FROM event_docs d JOIN events e ON e.event_id = d.event_id WHERE e.kind = 'refresh' LIMIT 1`).Scan(&qualifier); err != nil || qualifier != "refreshed" {
		t.Errorf("the qualifier of a refresh: %q, %v", qualifier, err)
	}
}

// A document computed by an older version of the class adopts the
// current one with a refresh, as with any commit (ADR-0039).
func TestRefresh_AdoptsTheClassVersion(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	v1 := `{"name": "Meter", "variables": {"n": {"type": "number"}, "twice": {"formula": "n * 2"}}, "states": {"initial": "on", "list": ["on"]}}`
	v2 := `{"name": "Meter", "variables": {"n": {"type": "number"}, "twice": {"formula": "n * 3"}, "seen": {"formula": "n", "mode": "triggered", "on": ["refresh"]}},
 "states": {"initial": "on", "list": ["on"]}}`
	if _, err := s.defineOpen(ctx, []byte(v1), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Meter", Values{"n": 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.defineOpen(ctx, []byte(v2), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err = s.Refresh(ctx, "Meter", doc.ID, []string{"seen"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ClassVersion != 2 || doc.Version != 2 || doc.Values["twice"] != 15.0 || doc.Values["seen"] != 5.0 || doc.Computed["seen"].Version != 2 {
		t.Errorf("the refresh under the new version: %s", dump(doc))
	}
}
