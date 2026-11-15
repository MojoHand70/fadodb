package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
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

}

// A stream follows its caller (ADR-0058, item 4): it asks who the caller
// is now once it has read events its filter lets through, and judges
// them for the caller as it is then.
func TestChanges_AStreamFollowsItsCaller(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{expense, openMemo} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	// Eve's stream hears of expenses only, and asks who Eve is now.
	eve, asked := Principal{ID: "eve"}, 0
	var gone error
	stream := listening(t, s, ChangeFilter{Classes: []string{"Expense"}}, asEve)
	stream.Follow(func(context.Context) (Principal, error) {
		asked++
		return eve, gone
	})
	expenseOf := func(id string) {
		t.Helper()
		if _, err := s.Create(ctx, "Expense", Values{"amount": 7}, asOla, WithID(id)); err != nil {
			t.Fatal(err)
		}
	}
	told := func(what, want string, times int) {
		t.Helper()
		if got := strings.Join(heard(t, stream), "; "); got != want {
			t.Errorf("%s: Eve hears %q, want %q", what, got, want)
		}
		if asked != times {
			t.Errorf("%s: who Eve is was asked %d times, want %d", what, asked, times)
		}
	}

	// Nothing in the log, and a memo, which the filter passes over:
	// nobody is asked who Eve is.
	told("before anything happens", "", 0)
	if _, err := s.Create(ctx, "Memo", Values{"text": "for all"}, asOla, WithID("m-1")); err != nil {
		t.Fatal(err)
	}
	told("a memo", "", 0)

	// An expense while Eve is in no group: she is asked for and hears
	// nothing.
	expenseOf("e-1")
	told("an expense, Eve in no group", "", 1)

	// Eve joins finance, and an expense is written after: she hears of
	// it, and of nothing that passed.
	eve.Groups = []string{"finance"}
	expenseOf("e-2")
	told("an expense after Eve joined finance", "created e-2", 2)

	// An expense is written, Eve leaves finance, another is written, and
	// only then does her stream read the log. She is judged as she is
	// after the last of the two, so she hears of neither.
	expenseOf("e-3")
	eve.Groups = nil
	expenseOf("e-4")
	told("two expenses, Eve left finance between them", "", 3)

	// Her key is revoked: the stream ends with what the function said,
	// and what it had read is told to nobody. It kept its place, so a
	// stream whose function answers again loses nothing.
	eve.Groups = []string{"finance"}
	gone = ErrUnknownKey
	expenseOf("e-5")
	if _, err := stream.Next(ctx); err != ErrUnknownKey {
		t.Errorf("a stream whose caller is gone: %v, want the error of the function", err)
	}
	if len(stream.read) != 0 {
		t.Errorf("a stream whose caller is gone kept %d events to tell", len(stream.read))
	}
	gone = nil
	told("the function answers again", "created e-5", 5)

	// A caller the engine would refuse on a command is refused here.
	eve = Principal{ID: "eve", Kind: "robot"}
	expenseOf("e-6")
	if _, err := stream.Next(ctx); code(err) != CodeBadValue {
		t.Errorf("a caller of a kind that is none: %v", err)
	}
	eve = Principal{ID: "eve", For: "ann"}
	if _, err := stream.Next(ctx); code(err) != CodeNotSupported {
		t.Errorf("a caller that acts for somebody: %v", err)
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
	// A stream is heard as oneself.
	if _, err := s.Subscribe(ctx, ChangeFilter{}, As(Principal{ID: "ola", For: "ann"})); code(err) != CodeNotSupported {
		t.Errorf("a stream for somebody else: %v", err)
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

// The log is read from the database once for all the streams that keep
// up, and so is the version of a document the read rule is asked over
// (ADR-0058, item 9): what the streams read after a commit does not
// grow with their number. A hundred streams that each read the log for
// themselves slowed the commits a hundred times.
func TestChanges_TheLogIsReadOnceForAllStreams(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(expense), asSetup); err != nil {
		t.Fatal(err)
	}
	// Fifty streams, of callers who may read an expense, who may not,
	// and of an administrator, who needs no version to be read.
	var streams []*Changes
	var hears []bool
	for i := 0; i < 50; i++ {
		as, may := asFin, true
		switch i % 3 {
		case 1:
			as, may = asEve, false
		case 2:
			as = asRoot
		}
		streams = append(streams, listening(t, s, ChangeFilter{}, as))
		hears = append(hears, may)
	}
	told := func(what string) {
		t.Helper()
		for i, c := range streams {
			want := ""
			if hears[i] {
				want = what
			}
			if got := strings.Join(heard(t, c), "; "); got != want {
				t.Errorf("stream %d hears %q, want %q", i, got, want)
			}
		}
	}
	// Before anything is written every stream has asked the log once
	// and found nothing. They are asked again, and nobody reads: no
	// commit ended since.
	told("")
	reads, loads := s.tail.reads.Load(), s.tail.loads.Load()
	told("")
	if got := s.tail.reads.Load() - reads; got != 0 {
		t.Errorf("fifty streams read the log from the database %d times with no commit since", got)
	}
	if _, err := s.Create(ctx, "Expense", Values{"amount": 7}, asOla, WithID("e-1")); err != nil {
		t.Fatal(err)
	}
	told("created e-1")
	if _, err := s.Set(ctx, "Expense", "e-1", Values{"amount": 8}, asOla); err != nil {
		t.Fatal(err)
	}
	told("set e-1")
	// Each commit was read once, and the version of the document the
	// read rule is asked over was read once for each event.
	if got := s.tail.reads.Load() - reads; got != 2 {
		t.Errorf("fifty streams read the log from the database %d times after two commits, want 2", got)
	}
	if got := s.tail.loads.Load() - loads; got != 2 {
		t.Errorf("fifty streams read %d versions of the document for two events, want 2", got)
	}
	// An event of the tail is every stream's: what a caller is given is
	// its own, and changing it changes nothing for the others.
	if _, err := s.Set(ctx, "Expense", "e-1", Values{"amount": 9}, asOla); err != nil {
		t.Fatal(err)
	}
	first, err := streams[0].Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.DocID, first.Kind = "changed", "changed"
	if got := strings.Join(heard(t, streams[3]), "; "); got != "set e-1" {
		t.Errorf("after a caller changed the event it was given, another hears %q", got)
	}
}

// A stream that is behind what the tail holds reads the log itself and
// loses nothing; one that is ahead of it moves the tail; and the tail
// lets go of its oldest events (ADR-0058, items 1 and 5).
func TestChanges_AStreamBehindTheOthers(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	if _, err := s.DefineClass(ctx, []byte(openMemo), asSetup); err != nil {
		t.Fatal(err)
	}
	memo := func(i int) {
		t.Helper()
		if _, err := s.Create(ctx, "Memo", Values{"text": "x"}, asOla, WithID("m-"+strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	// inOrder holds what a stream was told to the memos from to to.
	inOrder := func(who string, got []string, from, to int) {
		t.Helper()
		if len(got) != to-from+1 {
			t.Fatalf("%s was told of %d memos, want %d to %d", who, len(got), from, to)
		}
		for i, line := range got {
			if line != "created m-"+strconv.Itoa(from+i) {
				t.Fatalf("%s was told %q at place %d, want m-%d", who, line, i, from+i)
			}
		}
	}
	memo(0)
	first, err := s.Events(ctx, "Memo", "m-0", asOla)
	if err != nil {
		t.Fatal(err)
	}
	// Ola keeps up while more events are written than the tail holds.
	ola := listening(t, s, ChangeFilter{}, asOla)
	const many = 2*tailSize + 300
	var olaHeard []string
	for i := 1; i <= many; i++ {
		memo(i)
		if i%100 == 0 {
			olaHeard = append(olaHeard, heard(t, ola)...)
		}
	}
	olaHeard = append(olaHeard, heard(t, ola)...)
	inOrder("Ola", olaHeard, 1, many)
	if held := len(s.tail.held); held > tailSize {
		t.Errorf("the tail holds %d events, more than its %d", held, tailSize)
	}
	// Ann comes back after the first event: she is far behind the tail,
	// reads the log herself, and is told every event, each once.
	ann := listening(t, s, ChangeFilter{After: first[0].ID}, asAnn)
	inOrder("Ann", heard(t, ann), 1, many)
	// Both are told of what follows.
	memo(many + 1)
	inOrder("Ola", heard(t, ola), many+1, many+1)
	inOrder("Ann", heard(t, ann), many+1, many+1)

	// Nobody reads while ten memos are written, and Celina begins now:
	// she is ahead of the tail, which moves to where she is. Ola, left
	// behind it, still hears of the ten.
	for i := many + 2; i <= many+11; i++ {
		memo(i)
	}
	celina := listening(t, s, ChangeFilter{}, asCelina)
	if got := heard(t, celina); len(got) != 0 {
		t.Errorf("a stream that begins now is told of what passed: %q", got)
	}
	memo(many + 12)
	inOrder("Celina", heard(t, celina), many+12, many+12)
	inOrder("Ola", heard(t, ola), many+2, many+12)
	inOrder("Ann", heard(t, ann), many+2, many+12)

	// With the last stream closed the tail holds nothing.
	ola.Close()
	ann.Close()
	celina.Close()
	if s.tail.set || len(s.tail.held) != 0 {
		t.Errorf("with no stream open the tail holds %d events", len(s.tail.held))
	}
}

// Many streams read while a writer writes: each is told of every event
// it may read of, once and in order, whoever read the log for it.
func TestChanges_ManyStreamsAtOnce(t *testing.T) {
	s, _ := openStore(t, Options{})
	ctx := context.Background()
	for _, def := range []string{expense, openMemo} {
		if _, err := s.DefineClass(ctx, []byte(def), asSetup); err != nil {
			t.Fatal(err)
		}
	}
	const commits, readers = 300, 24
	// Every third commit is a memo, which everybody reads; the others
	// are expenses, which finance reads and Eve does not.
	wants := func(finance bool) []string {
		var out []string
		for i := 0; i < commits; i++ {
			switch {
			case i%3 == 0:
				out = append(out, "m-"+strconv.Itoa(i))
			case finance:
				out = append(out, "e-"+strconv.Itoa(i))
			}
		}
		return out
	}
	var wg sync.WaitGroup
	failed := make(chan error, readers)
	for r := 0; r < readers; r++ {
		finance := r%2 == 0
		as := asEve
		if finance {
			as = asFin
		}
		c := listening(t, s, ChangeFilter{}, as)
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			want := wants(finance)
			within, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			for i, id := range want {
				ev, err := c.Next(within)
				if err != nil {
					failed <- fmt.Errorf("stream %d, after %d of %d events: %v", r, i, len(want), err)
					return
				}
				if ev.DocID != id {
					failed <- fmt.Errorf("stream %d was told of %s at place %d, want %s", r, ev.DocID, i, id)
					return
				}
			}
		}(r)
	}
	for i := 0; i < commits; i++ {
		var err error
		if i%3 == 0 {
			_, err = s.Create(ctx, "Memo", Values{"text": "x"}, asOla, WithID("m-"+strconv.Itoa(i)))
		} else {
			_, err = s.Create(ctx, "Expense", Values{"amount": i}, asOla, WithID("e-"+strconv.Itoa(i)))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(failed)
	for err := range failed {
		t.Error(err)
	}
}
