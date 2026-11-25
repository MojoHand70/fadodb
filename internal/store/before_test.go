package store

import (
	"strings"
	"testing"
	"time"
)

// The purchase of ADR-0078 as the record has it: the limit is brought
// when the purchase is filed and again before approve is asked, the
// guard of approve reads the document alone, and the rights say who may
// bring the limit again.
const beforePurchase = `{
 "name": "Purchase",
 "title": "\"Purchase \" & number",
 "variables": {
   "number": {"type": "text", "required": true},
   "amount": {"type": "decimal", "scale": 2, "required": true},
   "grade":  {"type": "text", "required": true},
   "limit":  {"type": "decimal", "scale": 2,
              "formula": "LOOKUP(Limits, limit, grade = THIS.grade)",
              "mode": "triggered", "on": ["enter:review", "before:approve", "refresh"]},
   "room":   {"formula": "limit - amount"},
   "seen_in": {"type": "text", "formula": "STATE()", "mode": "triggered", "on": ["before:approve"]}
 },
 "states": {"initial": "draft", "list": ["draft", "review", "approved"], "final": ["approved"]},
 "transitions": [
   {"name": "file",    "from": "draft",  "to": "review"},
   {"name": "return",  "from": "review", "to": "draft"},
   {"name": "approve", "from": "review", "to": "approved",
    "guard": "NOT ISBLANK(limit) AND amount <= limit"}
 ],
 "access": {
   "read":   "INGROUP(\"everyone\")",
   "create": "INGROUP(\"everyone\")",
   "update": "CREATEDBY() = USER()",
   "delete": "FALSE",
   "transition": {
     "file":    "CREATEDBY() = USER()",
     "return":  "INGROUP(\"managers\")",
     "approve": "INGROUP(\"managers\")"
   }
 },
 "rights": {
   "*":      {"limit": {"write": "FALSE"}},
   "review": {"limit": {"write": ["managers"]}}
 }
}`

func beforeStore(t *testing.T) *Store {
	t.Helper()
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	if _, err := s.DefineTable(ctx, []byte(sawLimits), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DefineClass(ctx, []byte(beforePurchase), asSetup); err != nil {
		t.Fatal(err)
	}
	return s
}

func setLimit(t *testing.T, s *Store, grade, limit string) {
	t.Helper()
	if _, err := s.ImportRows(t.Context(), "Limits", []Values{{"grade": grade, "limit": limit}}, sawTess); err != nil {
		t.Fatal(err)
	}
}

// The thirteen steps of the record, as its table has them.
func TestBefore_TheCaseOfTheRecord(t *testing.T) {
	s := beforeStore(t)
	ctx := t.Context()
	setLimit(t, s, "B", "10000.00")
	step := func(n int, what string, d *Document, err error, version int64) *Document {
		t.Helper()
		if err != nil || d.Version != version {
			t.Fatalf("step %d, %s: %v, %v; want version %d", n, what, d, err, version)
		}
		return d
	}
	d, err := s.Create(ctx, "Purchase", Values{"number": "P-7", "amount": "9000.00", "grade": "B"}, sawOla, WithID("P-7"))
	step(1, "Ola writes P-7", d, err, 1)
	if d.Values["limit"] != nil {
		t.Errorf("step 1: the limit is %v before the purchase is filed", d.Values["limit"])
	}
	d, err = s.Transition(ctx, "Purchase", "P-7", "file", nil, sawOla)
	step(2, "Ola files it", d, err, 2)
	d, err = s.Get(ctx, "Purchase", "P-7", sawTomek)
	step(3, "Tomek reads P-7", d, err, 2)
	if d.Values["amount"] != "9000.00" || d.Values["limit"] != "10000.00" {
		t.Errorf("step 3: %v against %v", d.Values["amount"], d.Values["limit"])
	}
	d, err = s.Set(ctx, "Purchase", "P-7", Values{"amount": "9900.00"}, sawOla)
	step(4, "Ola corrects the amount", d, err, 3)
	_, err = s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(2))
	stale(t, s, "step 5, Tomek approves what he saw", err, "P-7", 3, 2)
	_, err = s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, sawPiotr)
	forbiddenAt(t, "step 6, Piotr refreshes the limit", err, "limit", "piotr may not bring limit again while the document is in review")
	setLimit(t, s, "B", "5000.00")
	// Step 8: the limit is brought before the guard is asked, the guard
	// is not TRUE, nothing is written, and the refusal says what the
	// guard saw. The purchase still holds the limit of version 2.
	_, err = s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(3))
	e, _ := err.(*Error)
	if e == nil || e.Code != CodeGuardFalse || e.Brought == nil || (*e.Brought)["limit"] != "5000.00" || (*e.Brought)["seen_in"] != "review" {
		t.Fatalf("step 8, Tomek approves version 3: %v, brought %v", err, flat(e))
	}
	d, err = s.Get(ctx, "Purchase", "P-7", sawTomek)
	step(8, "the refused approval wrote nothing", d, err, 3)
	if d.Values["limit"] != "10000.00" || d.Computed["limit"].Version != 2 {
		t.Errorf("step 8: the purchase holds the limit %v of version %d", d.Values["limit"], d.Computed["limit"].Version)
	}
	// The listing brings it too, and offers approve by the number the
	// move would see.
	open, err := s.Transitions(ctx, "Purchase", "P-7", sawTomek)
	if err != nil || len(open) != 2 || open[1].Name != "approve" || open[1].Enabled || open[1].Code != CodeGuardFalse {
		t.Errorf("the listing over a limit of 5,000.00: %s, %v", flat(open), err)
	}
	d, err = s.Refresh(ctx, "Purchase", "P-7", []string{"limit"}, sawTomek)
	step(9, "Tomek refreshes the limit", d, err, 4)
	if d.Values["limit"] != "5000.00" || d.Values["room"] != "-4900.00" {
		t.Errorf("step 9: limit %v, room %v", d.Values["limit"], d.Values["room"])
	}
	d, err = s.Set(ctx, "Purchase", "P-7", Values{"amount": "4900.00"}, sawOla)
	step(10, "Ola corrects the amount", d, err, 5)
	// A preview brings the limit and writes nothing.
	p, err := s.Preview(ctx, "Purchase", "P-7", nil, "approve", sawTomek, Saw(5))
	if err != nil || p.Document.State != "approved" || p.Document.Computed["limit"].Version != 6 {
		t.Errorf("the preview of step 11: %s, %v", flat(p), err)
	}
	if open, _ := s.Transitions(ctx, "Purchase", "P-7", sawTomek); len(open) != 2 || !open[1].Enabled {
		t.Errorf("the listing before step 11: %s", flat(open))
	}
	d, err = s.Transition(ctx, "Purchase", "P-7", "approve", nil, sawTomek, Saw(5))
	step(11, "Tomek approves version 5", d, err, 6)
	// Version 6 holds the limit the guard saw, and says it was worked
	// out at version 6; the state its formula saw is the one left.
	if d.State != "approved" || d.Values["limit"] != "5000.00" || d.Computed["limit"].Version != 6 || d.Values["seen_in"] != "review" || d.Values["room"] != "100.00" {
		t.Errorf("step 11: %s", flat(d))
	}
	setLimit(t, s, "B", "20000.00")
	d, err = s.Get(ctx, "Purchase", "P-7", sawPiotr)
	step(13, "Piotr reads P-7", d, err, 6)
	if d.Values["amount"] != "4900.00" || d.Values["limit"] != "5000.00" {
		t.Errorf("step 13: %v against %v; want 4,900.00 against 5,000.00", d.Values["amount"], d.Values["limit"])
	}
	// The table of the record: when the limit was worked out, by version.
	for version, want := range map[int64]string{1: "<nil> at 0", 2: "10000.00 at 2", 3: "10000.00 at 2", 4: "5000.00 at 4", 5: "5000.00 at 4", 6: "5000.00 at 6"} {
		h, err := s.Version(ctx, "Purchase", "P-7", version, sawTomek)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(flatValue(h.Values["limit"]) + " at " + itoa(int(h.Computed["limit"].Version))); got != want {
			t.Errorf("version %d: the limit is %s; the record says %s", version, got, want)
		}
	}
	events, err := s.Events(ctx, "Purchase", "P-7", sawTomek)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Name != "approve" || last.SeenVersion != 5 || last.AfterVersion != 6 {
		t.Errorf("the event of the approval: %s", flat(last))
	}
}

// flatValue is a value of a document as the table of a test writes it.
func flatValue(v any) string {
	if v == nil {
		return "<nil>"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return flat(v)
}

// A value that cannot be brought refuses the transition, with the code
// and the variable: a condition is not asked over a number that could
// not be worked out. Another trigger would leave the old value and go on.
func TestBefore_AValueThatCannotBeBrought(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	const class = `{"name": "Split",
 "variables": {
   "total": {"type": "number"}, "parts": {"type": "number"},
   "each":  {"type": "number", "formula": "total / parts", "mode": "triggered", "on": ["before:go"], "max": 100}
 },
 "states": {"initial": "a", "list": ["a", "b"]},
 "transitions": [{"name": "go", "from": "a", "to": "b", "guard": "each > 0"}, {"name": "back", "from": "b", "to": "a"}]}`
	if _, err := s.defineOpen(ctx, []byte(class), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Split", Values{"total": 10, "parts": 0}, WithID("S-1")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Transition(ctx, "Split", "S-1", "go", nil)
	e, _ := err.(*Error)
	if e == nil || e.Code != "div_by_zero" || e.Variable != "each" || !strings.HasPrefix(e.Message, "each could not be brought before go: ") {
		t.Fatalf("a formula that fails before the guard: %v", err)
	}
	open, err := s.Transitions(ctx, "Split", "S-1")
	if err != nil || len(open) != 1 || open[0].Enabled || open[0].Code != "div_by_zero" {
		t.Errorf("the listing of a transition whose value cannot be brought: %s, %v", flat(open), err)
	}
	// A value the variable does not accept refuses it too.
	_, err = s.Transition(ctx, "Split", "S-1", "go", Values{"total": 1000, "parts": 2})
	if e, _ := err.(*Error); e == nil || e.Code != "out_of_range" || e.Variable != "each" {
		t.Fatalf("a value out of range before the guard: %v", err)
	}
	if d, _ := s.Get(ctx, "Split", "S-1"); d.Version != 1 || d.Values["each"] != nil || d.Computed != nil {
		t.Errorf("a refused transition left %s", flat(d))
	}
	// The values the command sets are in place before the value is
	// brought, and the guard reads what was brought.
	d, err := s.Transition(ctx, "Split", "S-1", "go", Values{"parts": 4})
	if err != nil || d.State != "b" || d.Values["each"] != 2.5 || d.Computed["each"].Version != 2 {
		t.Fatalf("the transition with the parts given: %s, %v", flat(d), err)
	}
}

// A variable is worked out once in a commit: one brought before the
// transition is not worked out again by the exit, the transition or the
// entry of the same move. A transition that fires by itself has nobody
// asking, and brings nothing.
func TestBefore_OnceInACommitAndNotByItself(t *testing.T) {
	clock := &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	s, _ := openStore(t, Options{Now: clock.Now})
	ctx := t.Context()
	const class = `{"name": "Step",
 "variables": {
   "n":     {"type": "number", "default": "0"},
   "saw":   {"type": "text", "formula": "STATE() & \":\" & TEXT(n)", "mode": "triggered", "on": ["before:go", "exit:a", "transition:go", "enter:b"]},
   "timed": {"type": "text", "formula": "\"brought in \" & STATE()", "mode": "triggered", "on": ["before:late"]}
 },
 "states": {"initial": "a", "list": ["a", "b", "c"]},
 "transitions": [
   {"name": "go", "from": "a", "to": "b", "guard": "saw = \"a:1\""},
   {"name": "late", "from": "b", "to": "c", "after": "1d"}
 ]}`
	if _, err := s.defineOpen(ctx, []byte(class), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Step", nil, WithID("X")); err != nil {
		t.Fatal(err)
	}
	// Brought before the guard with STATE() the state left; the exit,
	// the transition and the entry would each say otherwise, and none of
	// them works it out again.
	d, err := s.Transition(ctx, "Step", "X", "go", Values{"n": 1})
	if err != nil || d.State != "b" || d.Values["saw"] != "a:1" || d.Computed["saw"].Version != 2 {
		t.Fatalf("go: %s, %v", flat(d), err)
	}
	// The time comes and the document moves by itself: nobody asked for
	// late, and nothing was brought before it.
	clock.days(2)
	d, err = s.Refresh(ctx, "Step", "X", nil)
	if err != nil || d.State != "c" || d.Values["timed"] != nil {
		t.Fatalf("a timed transition by itself: %s, %v", flat(d), err)
	}
}

// What was brought is told on the refusal to a caller who may read it,
// and to nobody else.
func TestBefore_TheRefusalTellsWhoMayRead(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := t.Context()
	const class = `{"name": "Bid",
 "variables": {
   "price": {"type": "number"},
   "floor": {"type": "number", "formula": "50", "mode": "triggered", "on": ["before:take"]}
 },
 "states": {"initial": "open", "list": ["open", "taken"]},
 "transitions": [{"name": "take", "from": "open", "to": "taken", "guard": "price >= floor"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "FALSE"},
 "rights": {"*": {"floor": {"read": ["managers"]}}}}`
	if _, err := s.DefineClass(ctx, []byte(class), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Bid", Values{"price": 10}, sawOla, WithID("B-1")); err != nil {
		t.Fatal(err)
	}
	_, err := s.Transition(ctx, "Bid", "B-1", "take", nil, sawOla)
	if e, _ := err.(*Error); e == nil || e.Code != CodeGuardFalse || e.Brought != nil {
		t.Errorf("Ola, who may not read the floor, is told %s", flat(err))
	}
	_, err = s.Transition(ctx, "Bid", "B-1", "take", nil, sawTomek)
	if e, _ := err.(*Error); e == nil || e.Code != CodeGuardFalse || e.Brought == nil || (*e.Brought)["floor"] != 50.0 {
		t.Errorf("Tomek, a manager, is told %s", flat(err))
	}
}
