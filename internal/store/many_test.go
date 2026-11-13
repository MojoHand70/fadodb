package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// outcomes renders the outcomes of a batch for a comparison: the state
// and version of a moved document, the code of a refused one.
func outcomes(out []MoveOutcome) string {
	parts := make([]string, len(out))
	for i, m := range out {
		switch {
		case m.Refusal != nil && m.Document != nil:
			parts[i] = "both"
		case m.Refusal != nil:
			parts[i] = m.Refusal.Code
		case m.Document != nil:
			parts[i] = fmt.Sprintf("%s:%d", m.Document.State, m.Document.Version)
		default:
			parts[i] = "neither"
		}
	}
	return strings.Join(parts, " ")
}

func eventCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func outboxCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM outbox").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// approvals creates one Approval for each amount and returns the ids.
func approvals(t *testing.T, s *Store, amounts ...float64) []string {
	t.Helper()
	ids := make([]string, len(amounts))
	for i, a := range amounts {
		doc, err := s.Create(context.Background(), "Approval", Values{"amount": a, "note": "new"}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = doc.ID
	}
	return ids
}

// The batch of ADR-0048: the documents that can move do, each with the
// commit Transition makes, and a refused one comes back with its reason
// and is left as it was.
func TestTransitionMany(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	ids := approvals(t, s, 500, 0, 700, 2000)
	before := eventCount(t, s)

	batch := []string{ids[0], ids[1], ids[2], "nope", ids[3]}
	out, err := s.TransitionMany(ctx, "Approval", "submit", batch, Values{"note": "batch"}, As(alice))
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes(out); got != "review:2 guard_false review:2 unknown_document review:2" {
		t.Fatalf("outcomes: %s\n%s", got, dump(out))
	}
	for i, m := range out {
		if m.ID != batch[i] || m.Moved() != (m.Document != nil) {
			t.Errorf("outcome %d: %s", i, dump(m))
		}
	}
	if out[0].Document.Values["note"] != "batch" || out[0].Document.Values["amount"] != 500.0 {
		t.Errorf("the values of the batch: %s", dump(out[0].Document))
	}
	if !strings.Contains(out[1].Refusal.Hint, "the guard: amount > 0") {
		t.Errorf("the refusal's hint: %s", dump(out[1].Refusal))
	}
	// The refused document is as it was, and the log says nothing of it.
	if got, _ := s.Get(ctx, "Approval", ids[1]); got.Version != 1 || got.State != "draft" || got.Values["note"] != "new" {
		t.Errorf("the refused document changed: %s", dump(got))
	}
	if n := eventCount(t, s); n != before+3 {
		t.Errorf("%d events for three moves", n-before)
	}
	// Each move has the event Transition writes, and the events stand in
	// the order of the ids.
	var last string
	for _, id := range []string{ids[0], ids[2], ids[3]} {
		events, _ := s.Events(ctx, "Approval", id)
		e := events[len(events)-1]
		if len(events) != 2 || e.Kind != EventTransition || e.Name != "submit" || e.FromState != "draft" || e.ToState != "review" ||
			e.BeforeVersion != 1 || e.AfterVersion != 2 || e.ActorID != "alice" || e.Origin != OriginCommand || e.CommandKey != "" {
			t.Errorf("the event of %s: %s", id, dump(events))
		}
		if e.ID <= last {
			t.Errorf("the events are not in the order of the ids: %s after %s", e.ID, last)
		}
		last = e.ID
	}
	if got := visits(t, s, "Approval", ids[0]); !strings.HasPrefix(got, "draft:1-1/") || !strings.HasSuffix(got, " review:2") {
		t.Errorf("the stays of a moved document: %s", got)
	}

	// The chain of each document follows its own move: 500 is booked by
	// the eventless transition, 2000 is big and stays approved, and the
	// one still in draft is in the wrong state.
	out, err = s.TransitionMany(ctx, "Approval", "approve", []string{ids[0], ids[1], ids[3]}, nil, As(finance))
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes(out); got != "booked:4 wrong_state approved:3" {
		t.Errorf("approve: %s", got)
	}
	// A guard over the principal refuses every document: nothing is
	// written, and the call still answers for each.
	before = eventCount(t, s)
	out, err = s.TransitionMany(ctx, "Approval", "approve", []string{ids[2]}, nil, As(alice))
	if err != nil || outcomes(out) != "guard_false" || eventCount(t, s) != before {
		t.Errorf("approve as sales: %v %s", err, outcomes(out))
	}
	// A terminal document only moves, without values.
	if out, err = s.TransitionMany(ctx, "Approval", "reject", []string{ids[1], ids[2]}, nil, As(alice)); err != nil || outcomes(out) != "rejected:2 rejected:3" {
		t.Fatalf("reject: %v %s", err, outcomes(out))
	}
	out, err = s.TransitionMany(ctx, "Approval", "archive", []string{ids[1], ids[2]}, Values{"note": "kept"}, As(alice))
	if err != nil || outcomes(out) != "terminal terminal" {
		t.Errorf("archive with values: %v %s", err, outcomes(out))
	}
	if out, err = s.TransitionMany(ctx, "Approval", "archive", []string{ids[1], ids[2]}, nil, As(alice)); err != nil || outcomes(out) != "archived:3 archived:4" {
		t.Errorf("archive: %v %s", err, outcomes(out))
	}
}

// What refuses the whole call, before anything is written (ADR-0048,
// item 5).
func TestTransitionMany_TheCallRefused(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineApproval(t, s)
	ids := approvals(t, s, 500, 600)
	before := eventCount(t, s)
	long := make([]string, MaxMany+1)
	for i := range long {
		long[i] = fmt.Sprintf("d%d", i)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for name, tc := range map[string]struct {
		ctx        context.Context
		class      string
		transition string
		ids        []string
		opts       []Option
		code       string
		says       string
	}{
		"unknown class":      {ctx, "Nope", "submit", ids, nil, CodeUnknownClass, ""},
		"unknown transition": {ctx, "Approval", "fly", ids, nil, CodeUnknownTransition, "submit, approve, reject, archive, fail"},
		"too many":           {ctx, "Approval", "submit", long, nil, CodeTooManyElements, "at most 10000"},
		"named twice":        {ctx, "Approval", "submit", []string{ids[0], ids[1], ids[0]}, nil, CodeBadValue, "at [0] and [2]"},
		"a chosen moment":    {ctx, "Approval", "submit", ids, []Option{At(start.Add(time.Hour))}, CodeNotAllowed, ""},
		"the outbox's key":   {ctx, "Approval", "submit", ids, []Option{Key(outboxKeyPrefix + "x")}, CodeBadValue, ""},
		"a context ended":    {cancelled, "Approval", "submit", ids, nil, CodeStorage, ""},
	} {
		out, err := s.TransitionMany(tc.ctx, tc.class, tc.transition, tc.ids, nil, append([]Option{As(alice)}, tc.opts...)...)
		e, _ := err.(*Error)
		if out != nil || e == nil || e.Code != tc.code || !strings.Contains(e.Message+" "+e.Hint, tc.says) {
			t.Errorf("%s: %v %v", name, out, err)
		}
	}
	if n := eventCount(t, s); n != before {
		t.Errorf("a refused call wrote %d events", n-before)
	}
	// No documents is no work, and no error.
	if out, err := s.TransitionMany(ctx, "Approval", "submit", nil, nil); err != nil || out == nil || len(out) != 0 {
		t.Errorf("an empty batch: %v %v", out, err)
	}
	// The bound itself is taken: every document of it is unknown.
	out, err := s.TransitionMany(ctx, "Approval", "submit", long[:MaxMany], nil)
	if err != nil || len(out) != MaxMany || out[MaxMany-1].Refusal.Code != CodeUnknownDocument {
		t.Errorf("the bound: %v, %d outcomes", err, len(out))
	}
}

// notice has a transition that returns to its own state, which a batch
// sent twice would take twice, and the rule that makes it matter.
const notice = `{"name": "Notice",
 "variables": {"amount": {"type": "number"}, "sent": {"type": "number", "default": "0"}},
 "states": {"initial": "open", "list": ["open", "closed"]},
 "transitions": [
   {"name": "remind", "from": "open", "to": "open", "guard": "amount > 0"},
   {"name": "close",  "from": "open", "to": "closed"}],
 "rules": [{"on": "transition", "name": "remind", "do": [{"action": "set", "values": {"sent": "sent + 1"}}]}]}`

// The key of a batch is held for each document (ADR-0048, item 9): sent
// again, the batch moves no document twice, answers for a moved document
// as the first call did, and tries a refused one again.
func TestTransitionMany_Key(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(notice), asSetup); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range []float64{10, 0, 30} {
		doc, err := s.Create(ctx, "Notice", Values{"amount": a}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	out, err := s.TransitionMany(ctx, "Notice", "remind", ids, nil, As(alice), Key("november"))
	if err != nil || outcomes(out) != "open:2 guard_false open:2" || out[0].Document.Values["sent"] != 1.0 {
		t.Fatalf("the first call: %v %s", err, dump(out))
	}
	events, _ := s.Events(ctx, "Notice", ids[0])
	if e := events[len(events)-1]; e.CommandKey != "november#"+ids[0] {
		t.Errorf("the key on the event: %q", e.CommandKey)
	}
	// Sent again: the two that moved are answered from the log, and the
	// third, whose amount is now given, moves for the first time.
	if _, err := s.Set(ctx, "Notice", ids[1], Values{"amount": 20}, As(alice)); err != nil {
		t.Fatal(err)
	}
	before := eventCount(t, s)
	out, err = s.TransitionMany(ctx, "Notice", "remind", ids, nil, As(alice), Key("november"))
	if err != nil || outcomes(out) != "open:2 open:3 open:2" || out[0].Document.Values["sent"] != 1.0 || out[1].Document.Values["sent"] != 1.0 {
		t.Fatalf("the second call: %v %s", err, dump(out))
	}
	if n := eventCount(t, s); n != before+1 {
		t.Errorf("the second call wrote %d events, want the one of the document refused before", n-before)
	}
	// A third time moves nothing at all.
	out, err = s.TransitionMany(ctx, "Notice", "remind", ids, nil, As(alice), Key("november"))
	if err != nil || outcomes(out) != "open:2 open:3 open:2" || eventCount(t, s) != before+1 {
		t.Errorf("the third call: %v %s", err, outcomes(out))
	}
	// The same key with another command is refused for each document.
	out, err = s.TransitionMany(ctx, "Notice", "close", ids[:2], nil, As(alice), Key("november"))
	if err != nil || outcomes(out) != "key_reused key_reused" {
		t.Errorf("the key with another transition: %v %s", err, outcomes(out))
	}
	// Without a key the reminder is taken again, which is what the key
	// is for.
	out, err = s.TransitionMany(ctx, "Notice", "remind", ids[:1], nil, As(alice))
	if err != nil || outcomes(out) != "open:3" || out[0].Document.Values["sent"] != 2.0 {
		t.Errorf("without a key: %v %s", err, dump(out))
	}
}

// seats has a guard that counts the documents of its own class: at most
// two seats are taken.
const seats = `{"name": "Seat",
 "variables": {"who": {"type": "text"}},
 "states": {"initial": "asked", "list": ["asked", "taken"]},
 "transitions": [{"name": "take", "from": "asked", "to": "taken",
                  "guard": "LOOKUP(Seat, COUNT(id), state = \"taken\") < 2"}]}`

// A document sees the ones moved before it in the batch (ADR-0048, item
// 8), in a preview as in the call.
func TestTransitionMany_SeesTheMovesBeforeIt(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(seats), asSetup); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, who := range []string{"ann", "bob", "cy", "di"} {
		doc, err := s.Create(ctx, "Seat", Values{"who": who}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	const want = "taken:2 taken:2 guard_false guard_false"
	before := eventCount(t, s)
	out, err := s.PreviewMany(ctx, "Seat", "take", ids, nil, As(alice))
	if err != nil || outcomes(out) != want {
		t.Fatalf("the preview: %v %s", err, outcomes(out))
	}
	if n, _ := s.Count(ctx, Filter{Class: "Seat", Where: `state = "taken"`}); n != 0 || eventCount(t, s) != before {
		t.Fatalf("the preview wrote: %d seats taken, %d events", n, eventCount(t, s)-before)
	}
	// The order of the ids decides who gets a seat.
	back := []string{ids[3], ids[2], ids[1], ids[0]}
	out, err = s.TransitionMany(ctx, "Seat", "take", back, nil, As(alice))
	if err != nil || outcomes(out) != want {
		t.Fatalf("the call: %v %s", err, outcomes(out))
	}
	for i, state := range []string{"asked", "asked", "taken", "taken"} {
		if got, _ := s.Get(ctx, "Seat", ids[i]); got.State != state {
			t.Errorf("seat %d is %s, want %s", i, got.State, state)
		}
	}
}

// A document refused after its commit is written leaves nothing behind:
// the batch returns to the mark it set before the document (ADR-0048,
// item 3), and the documents after it move.
func TestTransitionMany_ARefusedDocumentIsTakenBack(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineOrders(t, s)
	var ids []string
	for i := 0; i < 5; i++ {
		doc, err := s.Create(ctx, "Order", Values{"item": fmt.Sprintf("desk %d", i)}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	events, queued := eventCount(t, s), outboxCount(t, s)
	// The second and the third are refused once everything of theirs is
	// written: the version, the history row, the event and the action.
	s.testMoved = func(id string) *Error {
		if id == ids[1] || id == ids[2] {
			return errf(CodeBadValue, "", "refused late")
		}
		return nil
	}
	out, err := s.TransitionMany(ctx, "Order", "place", ids, nil, As(alice))
	s.testMoved = nil
	if err != nil || outcomes(out) != "placed:2 bad_value bad_value placed:2 placed:2" {
		t.Fatalf("outcomes: %v %s", err, outcomes(out))
	}
	if n := eventCount(t, s); n != events+3 {
		t.Errorf("%d events for three moves", n-events)
	}
	if n := outboxCount(t, s); n != queued+3 {
		t.Errorf("%d actions queued for three moves", n-queued)
	}
	for _, id := range ids[1:3] {
		got, _ := s.Get(ctx, "Order", id)
		history, _ := s.History(ctx, "Order", id)
		evs, _ := s.Events(ctx, "Order", id)
		if got.State != "new" || got.Version != 1 || len(history) != 1 || len(evs) != 1 {
			t.Errorf("a refused document kept something: %s, %d versions, %d events", dump(got), len(history), len(evs))
		}
	}
	// The actions of the three run when the outbox is drained, one note
	// each, and the refused two can still be placed.
	if n, err := s.Drain(ctx, asSetup); err != nil || n != 3 {
		t.Errorf("drain: %d %v", n, err)
	}
	if n, _ := s.Count(ctx, Filter{Class: "Note"}); n != 3 {
		t.Errorf("%d notes", n)
	}
	if out, err = s.TransitionMany(ctx, "Order", "place", ids[1:3], nil, As(alice)); err != nil || outcomes(out) != "placed:2 placed:2" {
		t.Errorf("the two placed afterwards: %v %s", err, outcomes(out))
	}
}

// A preview is the batch rolled back (ADR-0048, item 11): the outcomes
// of the call, and nothing written, no action queued, no key held.
func TestPreviewMany(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	defineOrders(t, s)
	var ids []string
	for i := 0; i < 3; i++ {
		doc, err := s.Create(ctx, "Order", Values{"item": "desk"}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	if _, err := s.Transition(ctx, "Order", ids[1], "place", nil, As(alice)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drain(ctx, asSetup); err != nil {
		t.Fatal(err)
	}
	events, queued := eventCount(t, s), outboxCount(t, s)
	const want = "placed:2 wrong_state placed:2"
	for _, opts := range [][]Option{{As(alice)}, {As(alice), Key("try")}, {As(alice), At(start.Add(48 * time.Hour))}} {
		out, err := s.PreviewMany(ctx, "Order", "place", ids, nil, opts...)
		if err != nil || outcomes(out) != want {
			t.Fatalf("the preview: %v %s", err, outcomes(out))
		}
		if eventCount(t, s) != events || outboxCount(t, s) != queued {
			t.Fatalf("the preview wrote: %d events, %d actions", eventCount(t, s)-events, outboxCount(t, s)-queued)
		}
		if got, _ := s.Get(ctx, "Order", ids[0]); got.State != "new" || got.Version != 1 {
			t.Fatalf("the preview moved a document: %s", dump(got))
		}
	}
	// The call then does what the preview said, under the key the
	// preview tried and did not hold.
	out, err := s.TransitionMany(ctx, "Order", "place", ids, nil, As(alice), Key("try"))
	if err != nil || outcomes(out) != want {
		t.Fatalf("the call: %v %s", err, outcomes(out))
	}
	if eventCount(t, s) != events+2 || outboxCount(t, s) != queued+2 {
		t.Errorf("the call wrote %d events and %d actions", eventCount(t, s)-events, outboxCount(t, s)-queued)
	}
	// The whole call is refused in a preview as in the call.
	if _, err := s.PreviewMany(ctx, "Order", "fly", ids, nil); code(err) != CodeUnknownTransition {
		t.Errorf("an unknown transition: %v", err)
	}
	if _, err := s.PreviewMany(ctx, "Order", "place", []string{ids[0], ids[0]}, nil); code(err) != CodeBadValue {
		t.Errorf("a document named twice: %v", err)
	}
}

// The rights are each document's own (ADR-0041): a transition a
// principal may not take is forbidden for that document, and the others
// move.
func TestTransitionMany_Rights(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const desk = `{"name": "Desk",
 "variables": {"owner": {"type": "text", "default": "USER()"}},
 "states": {"initial": "free", "list": ["free", "held"]},
 "transitions": [{"name": "hold", "from": "free", "to": "held"}],
 "access": {"transition": {"hold": "owner = USER()"}}}`
	if _, err := s.defineOpen(ctx, []byte(desk), asSetup); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range []Principal{alice, finance, alice} {
		doc, err := s.Create(ctx, "Desk", nil, As(p))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	out, err := s.TransitionMany(ctx, "Desk", "hold", ids, nil, As(alice))
	if err != nil || outcomes(out) != "held:2 forbidden held:2" {
		t.Fatalf("outcomes: %v %s", err, outcomes(out))
	}
	if got, _ := s.Get(ctx, "Desk", ids[0]); got.StateEnteredBy != "alice" || got.StateEnteredFrom != "free" {
		t.Errorf("who moved it: %s", dump(got))
	}
}

// The commits of a batch queue their wake-ups with them (ADR-0047), and
// a batch of moves takes its turn as one transaction: a reader sees none
// of them before it ends.
func TestTransitionMany_Timers(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	const fuse = `{"name": "Fuse",
 "variables": {"n": {"type": "number"}},
 "states": {"initial": "idle", "list": ["idle", "set", "blown"]},
 "transitions": [{"name": "arm", "from": "idle", "to": "set"},
                 {"name": "blow", "from": "set", "to": "blown", "after": "1m"}]}`
	if _, err := s.defineOpen(ctx, []byte(fuse), asSetup); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 4; i++ {
		doc, err := s.Create(ctx, "Fuse", Values{"n": i}, As(alice))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc.ID)
	}
	if n := outboxCount(t, s); n != 0 {
		t.Fatalf("%d rows before", n)
	}
	// A preview queues no wake-up and leaves no moment behind for the
	// worker.
	if out, err := s.PreviewMany(ctx, "Fuse", "arm", ids, nil, As(alice)); err != nil || outcomes(out) != "set:2 set:2 set:2 set:2" {
		t.Fatalf("the preview: %v %s", err, outcomes(out))
	}
	if n := outboxCount(t, s); n != 0 || s.wakeAt != 0 {
		t.Fatalf("the preview left %d wake-ups, and the moment %d", n, s.wakeAt)
	}
	out, err := s.TransitionMany(ctx, "Fuse", "arm", ids, nil, As(alice))
	if err != nil || outcomes(out) != "set:2 set:2 set:2 set:2" {
		t.Fatalf("the call: %v %s", err, outcomes(out))
	}
	if n := outboxCount(t, s); n != 4 {
		t.Errorf("%d wake-ups for four documents", n)
	}
}
