package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// atStart is a clock that stands still, for the tests that read NOW().
func atStart() time.Time { return start }

// ruledTicket is a class whose rules use every synchronous action: an entry
// that stamps the document, a reset that empties a rows value, an object
// and a scalar, and emits on the way.
const ruledTicket = `{
 "name": "Ticket",
 "variables": {
   "title_text": {"type": "text", "required": true},
   "points":     {"type": "int", "default": "1"},
   "weight":     {"formula": "points * 2"},
   "owner":      {"type": "text"},
   "taken_at":   {"type": "datetime"},
   "lines":      {"type": "rows", "schema": {"sku": "text", "qty": "int"}},
   "tags":       {"type": "array<text>"},
   "contact":    {"type": "object", "schema": "Person"}
 },
 "states": {"initial": "new", "list": ["new", "taken", "heavy", "dropped"]},
 "transitions": [
   {"name": "take", "from": "new", "to": "taken"},
   {"from": "taken", "to": "heavy", "guard": "weight >= 20"},
   {"name": "drop", "from": ["taken", "heavy"], "to": "dropped"}
 ],
 "rules": [
   {"on": "create", "do": [{"action": "emit", "event": "ticket.created"}]},
   {"on": "transition", "name": "take", "do": [{"action": "set", "values": {"owner": "USER()", "taken_at": "NOW()", "points": "points * 10"}}]},
   {"on": "enter", "state": "heavy", "do": [{"action": "reset", "variables": ["tags"]}, {"action": "emit", "event": "ticket.heavy"}]},
   {"on": "transition", "name": "drop", "do": [{"action": "reset", "variables": ["lines", "contact", "owner", "points"]}]},
   {"on": "delete", "do": [{"action": "emit", "event": "ticket.deleted"}]}
 ]
}`

func defineTicket(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DefineClass(context.Background(), []byte(ruledTicket), asSetup); err != nil {
		t.Fatal(err)
	}
}

func ticketValues(points int) Values {
	return Values{"title_text": "t", "points": points,
		"lines":   []any{Values{"sku": "A", "qty": 1}, Values{"sku": "B", "qty": 2}},
		"tags":    []string{"x", "y"},
		"contact": Values{"id": "anna", "name": "Anna"}}
}

// The rules of a transition write into its own version, and the rules of
// the eventless transition after it into theirs: a reset there empties a
// composite in that version and not in the one before.
func TestRules_SetResetAndTheVersionsTheyWrite(t *testing.T) {
	s, _ := openStore(t, Options{Now: atStart})
	defineTicket(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Ticket", ticketValues(2), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	doc, err = s.Transition(ctx, "Ticket", doc.ID, "take", nil, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != "heavy" || doc.Version != 3 {
		t.Fatalf("state %s version %d, want heavy at 3", doc.State, doc.Version)
	}
	if doc.Values["owner"] != "alice" || doc.Values["taken_at"] != start.Unix() || doc.Values["points"] != "20" || doc.Values["weight"] != "40" {
		t.Errorf("the values the rule set: %s", dump(doc.Values))
	}
	if tags := doc.Values["tags"].([]any); len(tags) != 0 {
		t.Errorf("tags %v, want them emptied by the reset of the eventless commit", tags)
	}
	if lines := doc.Values["lines"].([]any); len(lines) != 2 {
		t.Errorf("lines %v, want them carried forward", lines)
	}
	// Version 2 is the transition's: the set is in it, the tags still are.
	v2, err := s.Version(ctx, "Ticket", doc.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.State != "taken" || v2.Values["owner"] != "alice" || len(v2.Values["tags"].([]any)) != 2 {
		t.Errorf("version 2: state %s, values %s", v2.State, dump(v2.Values))
	}
	got, err := s.Get(ctx, "Ticket", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Values, doc.Values) {
		t.Errorf("the stored document %s differs from the one returned %s", dump(got.Values), dump(doc.Values))
	}

	// A reset in the command's own commit: a rows value empty, an object
	// blank in every field, a scalar without a default blank, one with a
	// default back to it.
	doc, err = s.Transition(ctx, "Ticket", doc.ID, "drop", nil, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Values["lines"].([]any)) != 0 || doc.Values["contact"] != nil || doc.Values["owner"] != nil || doc.Values["points"] != "1" || doc.Values["weight"] != "2" {
		t.Errorf("after the reset: %s", dump(doc.Values))
	}
	v3, err := s.Version(ctx, "Ticket", doc.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(v3.Values["lines"].([]any)) != 2 || v3.Values["contact"] == nil {
		t.Errorf("version 3 keeps what it had: %s", dump(v3.Values))
	}
}

// A custom event is caused by the event of its commit and names that
// commit's version; after a delete it names the last version as the one
// before, as the delete's own event does (ADR-0043).
func TestRules_CustomEvents(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineTicket(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Ticket", ticketValues(2), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Ticket", doc.ID, "take", nil, As(alice), Key("take-1")); err != nil {
		t.Fatal(err)
	}
	// The same key again: no second commit, no second event, the document
	// of the command's last version.
	again, err := s.Transition(ctx, "Ticket", doc.ID, "take", nil, As(alice), Key("take-1"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != 3 || again.State != "heavy" {
		t.Errorf("the repeat answers version %d in %s, want 3 in heavy", again.Version, again.State)
	}
	if err := s.Delete(ctx, "Ticket", doc.ID, As(alice)); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, "Ticket", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		kind, name    string
		before, after int64
		causedBy      int // the index of the event that caused it, -1 for none
	}
	index := map[string]int{}
	var got []row
	for i, e := range events {
		index[e.ID] = i
		cause := -1
		if e.CauseEventID != "" {
			cause = index[e.CauseEventID]
		}
		got = append(got, row{e.Kind, e.Name, e.BeforeVersion, e.AfterVersion, cause})
		if e.ActorID != "alice" {
			t.Errorf("event %d: actor %q, want the command's principal", i, e.ActorID)
		}
	}
	want := []row{
		{EventCreated, "", 0, 1, -1},
		{EventCustom, "ticket.created", 0, 1, 0},
		{EventTransition, "take", 1, 2, -1},
		{EventTransition, "", 2, 3, 2},
		{EventCustom, "ticket.heavy", 0, 3, 3},
		{EventDeleted, "", 3, 0, -1},
		{EventCustom, "ticket.deleted", 3, 0, 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events\n got %+v\nwant %+v", got, want)
	}
	var qualifiers []string
	rows, err := s.db.Query(`SELECT d.qualifier FROM event_docs d JOIN events e ON e.event_id = d.event_id WHERE e.kind = 'custom' AND d.doc_id = ?`, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			t.Fatal(err)
		}
		qualifiers = append(qualifiers, q)
	}
	if strings.Join(qualifiers, " ") != "emitted emitted emitted" {
		t.Errorf("qualifiers %v", qualifiers)
	}
}

// A batch runs the rules of every document it creates.
func TestRules_CreateMany(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineTicket(t, s)
	ctx := context.Background()
	docs, err := s.CreateMany(ctx, "Ticket", []Values{{"title_text": "a"}, {"title_text": "b"}}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		events, err := s.Events(ctx, "Ticket", d.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 2 || events[1].Kind != EventCustom || events[1].Name != "ticket.created" || events[1].CauseEventID != events[0].ID {
			t.Errorf("%s: events %s", d.ID, dump(events))
		}
	}
}

// A preview reports every rule whose trigger was raised, with the commit
// it belongs to, whether it ran, and the evaluated arguments of its
// actions; it writes nothing.
func TestRules_PreviewReport(t *testing.T) {
	s, _ := openStore(t, Options{Now: atStart})
	defineTicket(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Ticket", ticketValues(2), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	pv, err := s.Preview(ctx, "Ticket", doc.ID, nil, "take", As(alice))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pv.Rules)
	want := `[{"commit":0,"path":"/rules/1","ran":true,"actions":[{"action":"set","args":{"values":{"owner":"alice","points":"20","taken_at":` + itoa(int(start.Unix())) + `}}}]},` +
		`{"commit":1,"path":"/rules/2","ran":true,"actions":[{"action":"reset","args":{"variables":["tags"]}},{"action":"emit","args":{"event":"ticket.heavy"}}]}]`
	if string(raw) != want {
		t.Errorf("rules\n got %s\nwant %s", raw, want)
	}
	if pv.Document.State != "heavy" || pv.Document.Version != 3 || len(pv.Document.Values["tags"].([]any)) != 0 {
		t.Errorf("the previewed document: %s", dump(pv.Document))
	}
	after, err := s.Get(ctx, "Ticket", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.Events(ctx, "Ticket", doc.ID)
	if after.Version != 1 || len(events) != 2 {
		t.Errorf("the preview wrote: version %d, %d events", after.Version, len(events))
	}

	// A create previewed: the rule on create, in commit 0.
	pv, err = s.Preview(ctx, "Ticket", "", Values{"title_text": "x"}, "", As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Rules) != 1 || pv.Rules[0].Path != "/rules/0" || !pv.Rules[0].Ran || pv.Rules[0].Actions[0].Args["event"] != "ticket.created" {
		t.Errorf("rules of a previewed create: %s", dump(pv.Rules))
	}

	// A draft's rules are reported the same way.
	draft := strings.Replace(ruledTicket, `"ticket.heavy"`, `"ticket.very_heavy"`, 1)
	pv, err = s.PreviewDraft(ctx, []byte(draft), doc.ID, nil, "take", designing(alice))
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Rules) != 2 || pv.Rules[1].Actions[1].Args["event"] != "ticket.very_heavy" {
		t.Errorf("rules of a draft: %s", dump(pv.Rules))
	}
}

// A condition that errors is FALSE: the rule does not run, a commit
// leaves no trace of it and a preview reports its code (ADR-0019,
// ADR-0043). An action that fails refuses the command with its path and
// names the variable.
func TestRules_ConditionAndActionErrors(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(`{"name": "Part",
 "variables": {"n": {"type": "number"}, "share": {"type": "number"}, "half": {"formula": "100 / share"}, "label": {"type": "text"}},
 "states": {"initial": "a", "list": ["a", "b", "c"]},
 "transitions": [{"name": "go", "from": "a", "to": "b"}, {"name": "split", "from": "a", "to": "c"}],
 "rules": [
   {"on": "transition", "name": "go", "if": "1 / n > 0", "do": [{"action": "set", "values": {"label": "\"ran\""}}]},
   {"on": "transition", "name": "split", "do": [{"action": "set", "values": {"share": "n"}}]}
 ]}`), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Part", Values{"n": 0, "share": 4})
	if err != nil {
		t.Fatal(err)
	}
	pv, err := s.Preview(ctx, "Part", doc.ID, nil, "go")
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Rules) != 1 || pv.Rules[0].Ran || pv.Rules[0].Code != "div_by_zero" || len(pv.Rules[0].Actions) != 0 {
		t.Errorf("a rule whose condition errors: %s", dump(pv.Rules))
	}
	// The set makes an auto formula fail: it is the action's failure.
	_, err = s.Transition(ctx, "Part", doc.ID, "split", nil)
	se, ok := err.(*Error)
	if !ok || se.Code != "div_by_zero" || se.Variable != "half" || se.Path != "/rules/1/do/0" {
		t.Fatalf("want div_by_zero of half in /rules/1/do/0, got %v", err)
	}
	if !strings.Contains(se.Error(), "in /rules/1/do/0") {
		t.Errorf("the message does not name the action: %s", se.Error())
	}
	pv, err = s.Preview(ctx, "Part", doc.ID, nil, "split")
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Errors) != 1 || pv.Errors[0].Code != "div_by_zero" || pv.Errors[0].Variable != "half" || pv.Errors[0].Path != "/rules/1/do/0" {
		t.Errorf("the preview's errors: %s", dump(pv.Errors))
	}
	if doc, err = s.Transition(ctx, "Part", doc.ID, "go", nil); err != nil || doc.Values["label"] != nil {
		t.Errorf("the commit with the erroring condition: %v, label %v", err, doc.Values["label"])
	}
}

// A document computed by an older version of the class adopts the current
// one with its next commit, and the rules of the current version see what
// changed against the row as it was (ADR-0039, ADR-0043).
func TestRules_ValuesChangeAcrossAClassVersion(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	v1 := `{"name": "Meter", "variables": {"n": {"type": "number"}, "twice": {"formula": "n * 2"}}, "states": {"initial": "on", "list": ["on"]}}`
	v2 := `{"name": "Meter", "variables": {"n": {"type": "number"}, "twice": {"formula": "n * 3"}, "note": {"type": "text"}},
 "states": {"initial": "on", "list": ["on"]},
 "rules": [{"on": "values_change", "variables": ["twice"], "do": [{"action": "set", "values": {"note": "\"twice changed\""}}, {"action": "emit", "event": "meter.recomputed"}]},
           {"on": "values_change", "variables": ["note"], "do": [{"action": "emit", "event": "meter.noted"}]}]}`
	if _, err := s.DefineClass(ctx, []byte(v1), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Meter", Values{"n": 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(v2), asSetup); err != nil {
		t.Fatal(err)
	}
	// n is given the value it has: the commit adopts the new version, and
	// twice differs because its formula does.
	doc, err = s.Set(ctx, "Meter", doc.ID, Values{"n": 5})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != 2 || doc.ClassVersion != 2 || doc.Values["twice"] != 15.0 || doc.Values["note"] != "twice changed" {
		t.Errorf("the adopting commit: %s", dump(doc))
	}
	events, _ := s.Events(ctx, "Meter", doc.ID)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind+":"+e.Name)
	}
	if strings.Join(kinds, " ") != "created: set: custom:meter.recomputed" {
		t.Errorf("events %v", kinds)
	}
}

// The rules of a commit whose triggers none of them names evaluate
// nothing and allocate nothing, and neither does a rule that runs a set
// and an emit outside a preview (ADR-0043, Performance).
func TestRules_AllocateNothing(t *testing.T) {
	s, _ := openStore(t, Options{})
	defineTicket(t, s)
	ctx := context.Background()
	doc, err := s.Create(ctx, "Ticket", ticketValues(2), As(alice))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.class("Ticket")
	row, err := c.scanRow(c.w.selectCurrent.QueryRowContext(ctx, doc.ID), false)
	if err != nil {
		t.Fatal(err)
	}
	run := benchRules(c, row)
	points := c.byName["POINTS"].leaf
	two := row.vals[points]
	for name, fire := range map[string]func() *Error{
		"a set that changes a variable no rule names": func() *Error {
			return s.fireCommit(run, row, commitSet, "", "", nil)
		},
		"an eventless transition into a state with no rule": func() *Error {
			return s.fireCommit(run, row, commitHop, "new", "", nil)
		},
		"a transition whose rule sets three values and recalculates": func() *Error {
			s.emits = s.emits[:0]
			row.vals[points] = two
			return s.fireCommit(run, row, commitTransition, "new", "take", nil)
		},
	} {
		var failed *Error
		allocs := testing.AllocsPerRun(200, func() {
			if err := fire(); err != nil {
				failed = err
			}
		})
		if failed != nil {
			t.Fatalf("%s: %v", name, failed)
		}
		if allocs != 0 {
			t.Errorf("%s: %v allocations per run, want none", name, allocs)
		}
	}
}
