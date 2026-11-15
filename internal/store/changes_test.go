package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// An expense: its owner and its approver may read it, and finance.
const expense = `{"name": "Expense",
 "variables": {"owner": {"type": "text", "default": "USER()"}, "approver": {"type": "text"}, "amount": {"type": "number"}},
 "states": {"initial": "draft", "list": ["draft", "review", "paid"]},
 "transitions": [{"name": "submit", "from": "draft", "to": "review"}, {"name": "pay", "from": "review", "to": "paid"}],
 "access": {"read": "owner = USER() OR approver = USER() OR INGROUP(\"finance\")", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`

// A memo anybody reads.
const openMemo = `{"name": "Memo", "variables": {"text": {"type": "text"}}, "states": {"initial": "new", "list": ["new", "filed"]},
 "transitions": [{"name": "file", "from": "new", "to": "filed"}],
 "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`

// The people of the tests of the stream.
var (
	asOla    = As(Principal{ID: "ola"})
	asAnn    = As(Principal{ID: "ann"})
	asCelina = As(Principal{ID: "celina"})
	asFin    = As(Principal{ID: "fin", Groups: []string{"finance"}})
	asEve    = As(Principal{ID: "eve"})
	asRoot   = As(Principal{ID: "root", System: []string{GroupContentAdministrators}})
)

// heard reads what a stream has to tell now, without waiting, as the
// kind and the document of each event, with the name of a transition.
func heard(t *testing.T, c *Changes) []string {
	t.Helper()
	var out []string
	for {
		more, err := c.fill(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range c.read {
			line := ev.Kind + " " + ev.DocID
			if ev.Kind == EventTransition {
				line += " " + ev.Name + " to " + ev.ToState
			}
			out = append(out, line)
		}
		c.read = nil
		if !more {
			return out
		}
	}
}

func listening(t *testing.T, s *Store, f ChangeFilter, as Option) *Changes {
	t.Helper()
	c, err := s.Subscribe(context.Background(), f, as)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// The stream is the log read forward, and the read rule decides who
// hears of an event (ADR-0058, items 1 and 4).
func TestChanges_WhoHearsOfAnEvent(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{expense, openMemo} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	// Before anybody listens: an expense that no stream is told of,
	// since a stream begins now.
	if _, err := s.Create(ctx, "Expense", Values{"amount": 1}, asOla, WithID("e-0")); err != nil {
		t.Fatal(err)
	}
	streams := map[string]*Changes{
		"ola": listening(t, s, ChangeFilter{}, asOla), "ann": listening(t, s, ChangeFilter{}, asAnn), "celina": listening(t, s, ChangeFilter{}, asCelina),
		"fin": listening(t, s, ChangeFilter{}, asFin), "eve": listening(t, s, ChangeFilter{}, asEve), "root": listening(t, s, ChangeFilter{}, asRoot),
	}
	told := func(what string, want map[string]string) {
		t.Helper()
		for who, c := range streams {
			if got := strings.Join(heard(t, c), "; "); got != want[who] {
				t.Errorf("%s: %s hears %q, want %q", what, who, got, want[who])
			}
		}
	}
	told("before anything happens", map[string]string{})

	// Ola makes an expense that Ann approves. Whoever may read it hears.
	if _, err := s.Create(ctx, "Expense", Values{"amount": 120, "approver": "ann"}, asOla, WithID("e-1")); err != nil {
		t.Fatal(err)
	}
	told("an expense is created", map[string]string{"ola": "created e-1", "ann": "created e-1", "fin": "created e-1", "root": "created e-1"})

	// The approver is changed to Celina before Ann touched it. The
	// expense leaves Ann's sight, and Ann is told nothing: for her it is
	// as if she had never had a right to it (the owner's ruling 3).
	// Celina hears of the change that brought it into her sight.
	if _, err := s.Set(ctx, "Expense", "e-1", Values{"approver": "celina"}, asOla); err != nil {
		t.Fatal(err)
	}
	told("the approver is changed", map[string]string{"ola": "set e-1", "celina": "set e-1", "fin": "set e-1", "root": "set e-1"})

	// What follows is told to who may read the expense as each event
	// left it, in the order of the commits.
	if _, err := s.Transition(ctx, "Expense", "e-1", "submit", nil, asOla); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "for all"}, asEve, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Expense", "e-1", "pay", nil, asFin); err != nil {
		t.Fatal(err)
	}
	both := "transition e-1 submit to review; created m-1; transition e-1 pay to paid"
	told("it is submitted, a memo is written, it is paid", map[string]string{"ola": both, "celina": both, "fin": both, "root": both, "ann": "created m-1", "eve": "created m-1"})

	// A delete is told to whoever could read the document just before.
	if err := s.Delete(ctx, "Expense", "e-1", asOla); err != nil {
		t.Fatal(err)
	}
	told("it is deleted", map[string]string{"ola": "deleted e-1", "celina": "deleted e-1", "fin": "deleted e-1", "root": "deleted e-1"})

	// Eve joins finance: she hears of what happens from then on, and of
	// nothing that passed.
	if err := streams["eve"].As(Principal{ID: "eve", Groups: []string{"finance"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Expense", Values{"amount": 7}, asOla, WithID("e-2")); err != nil {
		t.Fatal(err)
	}
	told("a second expense, after Eve joined finance", map[string]string{"ola": "created e-2", "fin": "created e-2", "root": "created e-2", "eve": "created e-2"})
	if err := streams["eve"].As(Principal{ID: "eve", Kind: "robot"}); code(err) != CodeBadValue {
		t.Errorf("a caller of a kind that is none: %v", err)
	}
}

// An entry is the event as the log holds it, and a stream resumes after
// the event its caller handled last (ADR-0058, items 3 and 5).
func TestChanges_TheEventsAndThePlace(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{expense, openMemo} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	all := listening(t, s, ChangeFilter{}, asRoot)
	review := listening(t, s, ChangeFilter{Classes: []string{"expense"}, Kinds: []string{"transition"}, ToStates: []string{"Review"}}, asRoot)
	memos := listening(t, s, ChangeFilter{Classes: []string{"Memo"}}, asRoot)
	created := listening(t, s, ChangeFilter{Kinds: []string{"created", "deleted"}}, asRoot)

	if _, err := s.Create(ctx, "Expense", Values{"amount": 5}, asOla, WithID("e-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "x"}, asOla, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Expense", "e-1", "submit", nil, asOla); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Memo", "m-1", "file", nil, asOla); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(ctx, "Expense", "e-1", "pay", nil, asOla); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "Memo", "m-1", asOla); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		stream *Changes
		want   string
	}{
		"every event":                {all, "created e-1; created m-1; transition e-1 submit to review; transition m-1 file to filed; transition e-1 pay to paid; deleted m-1"},
		"expenses that enter review": {review, "transition e-1 submit to review"},
		"the memos":                  {memos, "created m-1; transition m-1 file to filed; deleted m-1"},
		"what is made and deleted":   {created, "created e-1; created m-1; deleted m-1"},
	} {
		if got := strings.Join(heard(t, c.stream), "; "); got != c.want {
			t.Errorf("%s: heard %q, want %q", name, got, c.want)
		}
	}

	// The events are those of the log, whole.
	log, err := s.Events(ctx, "Expense", "e-1", asRoot)
	if err != nil || len(log) != 3 {
		t.Fatalf("the events of e-1: %v, %d", err, len(log))
	}
	again := listening(t, s, ChangeFilter{Classes: []string{"Expense"}, After: log[0].ID}, asRoot)
	first, err := again.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dump(first) != dump(log[1]) || first.Kind != EventTransition || first.ToState != "review" || first.FromState != "draft" || first.ActorID != "ola" || first.AfterVersion != 2 || first.ID == "" {
		t.Errorf("the stream tells\n%s\nand the log holds\n%s", dump(first), dump(log[1]))
	}
	// A stream that begins after an event is told what followed it, the
	// events made before the stream was opened among them.
	if got := strings.Join(heard(t, again), "; "); got != "transition e-1 pay to paid" {
		t.Errorf("the rest after the first event: %q", got)
	}

	// What a stream cannot be opened with.
	for name, c := range map[string]struct {
		f    ChangeFilter
		code string
	}{
		"a class the database does not have": {ChangeFilter{Classes: []string{"Invoice"}}, CodeUnknownClass},
		"a kind that is none":                {ChangeFilter{Kinds: []string{"approved"}}, CodeBadValue},
		"a state no class has":               {ChangeFilter{ToStates: []string{"shipped"}}, CodeBadValue},
		"a state of another class":           {ChangeFilter{Classes: []string{"Memo"}, ToStates: []string{"review"}}, CodeBadValue},
		"an event the log does not have":     {ChangeFilter{After: "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"}, CodeBadValue},
	} {
		if _, err := s.Subscribe(ctx, c.f, asRoot); code(err) != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
	if _, err := s.Subscribe(ctx, ChangeFilter{}, As(Principal{ID: "x", Kind: "robot"})); code(err) != CodeBadValue {
		t.Errorf("a caller of a kind that is none: %v", err)
	}
}

// Next waits for the log to grow, and a commit wakes it (ADR-0058, item
// 9). A stream ends when it is closed, when its caller gives up, and
// when the store is closed.
func TestChanges_WaitingAndEnding(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(openMemo), asSetup); err != nil {
		t.Fatal(err)
	}
	c := listening(t, s, ChangeFilter{}, asOla)
	type answer struct {
		ev  *Event
		err error
	}
	next := func(ctx context.Context, c *Changes) chan answer {
		got := make(chan answer, 1)
		go func() {
			ev, err := c.Next(ctx)
			got <- answer{ev, err}
		}()
		return got
	}
	// Nothing to tell: Next waits.
	waiting := next(ctx, c)
	select {
	case a := <-waiting:
		t.Fatalf("a stream with nothing to tell answered %v, %v", a.ev, a.err)
	case <-time.After(50 * time.Millisecond):
	}
	// A commit ends, and the stream is told.
	if _, err := s.Create(ctx, "Memo", Values{"text": "x"}, asOla, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-waiting:
		if a.err != nil || a.ev.Kind != EventCreated || a.ev.DocID != "m-1" {
			t.Fatalf("after the commit: %v, %v", a.ev, a.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was not told of the commit")
	}
	// The caller gives up.
	giving, cancel := context.WithCancel(ctx)
	waiting = next(giving, c)
	cancel()
	if a := <-waiting; !errors.Is(a.err, context.Canceled) {
		t.Errorf("a caller that gave up is answered %v", a.err)
	}
	// The stream is closed while its caller waits, from another place.
	waiting = next(ctx, c)
	time.Sleep(10 * time.Millisecond)
	c.Close()
	c.Close()
	if a := <-waiting; code(a.err) != CodeClosed {
		t.Errorf("a stream that was closed answers %v", a.err)
	}
	if _, err := c.Next(ctx); code(err) != CodeClosed {
		t.Errorf("a closed stream, asked again: %v", err)
	}
	if open := s.listeners.Load(); open == nil || len(*open) != 0 {
		t.Errorf("a closed stream is still told of commits")
	}
	// The store is closed while a caller waits.
	other, err := s.Subscribe(ctx, ChangeFilter{}, asOla)
	if err != nil {
		t.Fatal(err)
	}
	waiting = next(ctx, other)
	time.Sleep(10 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-waiting:
		if code(a.err) != CodeClosed {
			t.Errorf("a stream of a store that was closed answers %v", a.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end with its store")
	}
	if _, err := s.Subscribe(ctx, ChangeFilter{}, asOla); code(err) != CodeClosed {
		t.Errorf("a stream of a closed store: %v", err)
	}
}

// A commit pays nothing for the stream (ADR-0058, item 9): it allocates
// the same with streams open as with none.
func TestChanges_ACommitPaysNothing(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(openMemo), asSetup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "Memo", Values{"text": "x"}, asOla, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	set := func() {
		if _, err := s.Set(ctx, "Memo", "m-1", Values{"text": "y"}, asOla); err != nil {
			t.Fatal(err)
		}
	}
	set()
	alone := testing.AllocsPerRun(200, set)
	for i := 0; i < 8; i++ {
		listening(t, s, ChangeFilter{}, asOla)
	}
	if watched := testing.AllocsPerRun(200, set); watched > alone {
		t.Errorf("a commit allocates %v times with eight streams open, and %v with none", watched, alone)
	}
}
