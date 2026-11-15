package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// journal stands in for a database with a log: the streams it opens
// tell what the test sends them.
type journal struct {
	engine
	// fail, when set, is what Subscribe answers.
	fail error
	// events are what a stream tells next, and failures what its Next
	// fails with.
	events   chan *store.Event
	failures chan error
	// What Subscribe was asked last, and how many streams were opened
	// and closed.
	mu     sync.Mutex
	filter store.ChangeFilter
	as     store.Principal
	opened atomic.Int32
	closed atomic.Int32
}

func newJournal() *journal {
	return &journal{events: make(chan *store.Event), failures: make(chan error, 1)}
}

func (j *journal) Subscribe(_ context.Context, p store.Principal, f store.ChangeFilter) (Stream, error) {
	if j.fail != nil {
		return nil, j.fail
	}
	j.mu.Lock()
	j.filter, j.as = f, p
	j.mu.Unlock()
	j.opened.Add(1)
	return &told{j: j}, nil
}

// asked is what the journal was asked for last.
func (j *journal) asked() (store.ChangeFilter, store.Principal) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.filter, j.as
}

// told is one stream of a journal.
type told struct {
	j   *journal
	who func(context.Context) (store.Principal, error)
}

func (s *told) Follow(who func(context.Context) (store.Principal, error)) { s.who = who }

func (s *told) Next(ctx context.Context) (*store.Event, error) {
	select {
	case ev := <-s.j.events:
		// As the engine does: who the caller is now is asked once there
		// is an event to judge.
		if _, err := s.who(ctx); err != nil {
			return nil, err
		}
		return ev, nil
	case err := <-s.j.failures:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *told) Close() { s.j.closed.Add(1) }

// listener is a caller that reads a stream over HTTP: the reply, and
// its entries one by one.
type listener struct {
	t      testing.TB
	resp   *http.Response
	blocks chan string
	stop   context.CancelFunc
}

// listen opens a stream at a server and reads it. The entries of the
// reply, each the lines before an empty line, are answered by next.
func listen(t testing.TB, url, object string, header ...string) *listener {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/subscribe", strings.NewReader(object))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	l := &listener{t: t, resp: resp, blocks: make(chan string, 64), stop: stop}
	go func() {
		defer close(l.blocks)
		defer resp.Body.Close()
		in := bufio.NewReader(resp.Body)
		var block strings.Builder
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				return
			}
			if line != "\n" {
				block.WriteString(line)
				continue
			}
			l.blocks <- block.String()
			block.Reset()
		}
	}()
	return l
}

// long is how long a test waits for what must come. Nothing waits that
// long unless it is broken.
const long = 20 * time.Second

// next answers the next entry of the stream, and whether there was one:
// none when the reply ended.
func (l *listener) next() (string, bool) {
	l.t.Helper()
	select {
	case block, more := <-l.blocks:
		return block, more
	case <-time.After(long):
		l.t.Fatal("the stream said nothing and did not end")
		return "", false
	}
}

// entry answers the next entry that is no comment.
func (l *listener) entry() (string, bool) {
	l.t.Helper()
	for {
		block, more := l.next()
		if !more || !strings.HasPrefix(block, ":") {
			return block, more
		}
	}
}

// ends holds the stream to ending now, with nothing more said.
func (l *listener) ends() {
	l.t.Helper()
	if block, more := l.entry(); more {
		l.t.Errorf("the stream went on after its end: %q", block)
	}
}

// ended reads the last entry of a stream the server ended, and holds
// the stream to ending after it.
func (l *listener) ended() store.Error {
	l.t.Helper()
	block, more := l.entry()
	if !more {
		l.t.Fatal("the stream ended without a last entry")
	}
	const head = "event: end\ndata: "
	if !strings.HasPrefix(block, head) || strings.Count(block, "\n") != 2 {
		l.t.Fatalf("the last entry of the stream is %q", block)
	}
	var got struct {
		Error store.Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(block, head)), &got); err != nil {
		l.t.Fatalf("the last entry of the stream carries no refusal: %q", block)
	}
	l.ends()
	return got.Error
}

// serve listens with a door on this machine.
func serve(t testing.TB, d http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	return srv
}

// until waits for what a test says will hold.
func until(t testing.TB, what string, holds func() bool) {
	t.Helper()
	for end := time.Now().Add(long); !holds(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("%s: not within %v", what, long)
		}
	}
}

var submitted = &store.Event{ID: "01KA2F8Q7ZJ4M0X3T5V9B1C6DE", Class: "Invoice", DocID: "FV/2026/11/0042", Kind: store.EventTransition,
	Name: "submit", FromState: "draft", ToState: "review", ActorID: "bob", ActorKind: store.ActorPerson,
	BeforeVersion: 2, AfterVersion: 3, BusinessDate: "2026-11-14", TS: 1763107200, Reason: "a line\nand <another>"}

// A stream is opened as a command is sent, and its reply stays open: an
// entry for each event, in the form of server-sent events (ADR-0058,
// items 2 and 3).
func TestAStreamOfChanges(t *testing.T) {
	j := newJournal()
	var reports []Request
	var mu sync.Mutex
	d := door(t, j, Options{Report: func(r Request) {
		mu.Lock()
		reports = append(reports, r)
		mu.Unlock()
	}})
	srv := serve(t, d)
	l := listen(t, srv.URL, `{"classes": ["Invoice"], "kinds": ["transition"], "to_states": ["review"]}`)

	if l.resp.StatusCode != http.StatusOK {
		t.Fatalf("the stream was answered with the status %d", l.resp.StatusCode)
	}
	for name, want := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Accel-Buffering": "no"} {
		if got := l.resp.Header.Get(name); got != want {
			t.Errorf("the header %s of a stream is %q, want %q", name, got, want)
		}
	}
	if l.resp.ContentLength >= 0 {
		t.Errorf("a stream says how long it is: %d", l.resp.ContentLength)
	}
	// The first line says the stream is open, before anything happened.
	if block, _ := l.next(); block != ": the stream is open\n" {
		t.Errorf("the stream begins with %q", block)
	}
	f, as := j.asked()
	if want := (store.ChangeFilter{Classes: []string{"Invoice"}, Kinds: []string{"transition"}, ToStates: []string{"review"}}); !reflect.DeepEqual(f, want) {
		t.Errorf("the stream was opened with the filter %+v, want %+v", f, want)
	}
	if !reflect.DeepEqual(as, ann) {
		t.Errorf("the stream was opened as %+v, want %+v", as, ann)
	}
	if n := d.Streams(); n != 1 {
		t.Errorf("the door holds %d streams, want 1", n)
	}

	// An event: its id, its kind of entry, and the event on one line, as
	// the command events answers it.
	j.events <- submitted
	text, _ := command.Marshal(submitted)
	if block, _ := l.next(); block != "id: 01KA2F8Q7ZJ4M0X3T5V9B1C6DE\nevent: change\ndata: "+string(text)+"\n" {
		t.Errorf("the entry of an event is %q", block)
	}
	// The line is written as an answer is: a < is a <.
	if !strings.Contains(string(text), `and <another>`) {
		t.Errorf("an event is written as %s", text)
	}
	second := *submitted
	second.ID, second.Name, second.Reason = "01KA2F8Q7ZJ4M0X3T5V9B1C6DF", "approve", ""
	j.events <- &second
	if block, _ := l.next(); !strings.HasPrefix(block, "id: 01KA2F8Q7ZJ4M0X3T5V9B1C6DF\nevent: change\ndata: {") {
		t.Errorf("the second entry is %q", block)
	}

	// The caller closes the stream: the door closes it in the database,
	// gives its place back and tells of it, once.
	l.stop()
	until(t, "the stream is closed", func() bool { return j.closed.Load() == 1 && d.Streams() == 0 })
	until(t, "the stream is told of", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reports) == 1
	})
	got := reports[0]
	if got.Took <= 0 || got.Lasted < 0 || got.At.IsZero() {
		t.Errorf("the times of a stream: at %v, took %v, lasted %v", got.At, got.Took, got.Lasted)
	}
	got.At, got.Took, got.Lasted = time.Time{}, 0, 0
	if want := (Request{Command: "subscribe", User: "ann", Status: 200, Stream: true, Entries: 2}); got != want {
		t.Errorf("the stream is reported as %+v, want %+v", got, want)
	}
}

// A caller that was away says the last event it handled, in the object
// or in the header a reader of server-sent events sends when it
// connects again; the header counts (ADR-0058, item 5).
func TestAStreamSaysWhereItWas(t *testing.T) {
	j := newJournal()
	srv := serve(t, door(t, j, Options{}))
	for name, c := range map[string]struct {
		object string
		header []string
		after  string
	}{
		"nothing":                 {`{}`, nil, ""},
		"after":                   {`{"after": "01A"}`, nil, "01A"},
		"the header":              {`{}`, []string{"Last-Event-ID", "01B"}, "01B"},
		"both, the header counts": {`{"after": "01A", "classes": ["Invoice"]}`, []string{"Last-Event-ID", "01B"}, "01B"},
	} {
		l := listen(t, srv.URL, c.object, c.header...)
		l.next()
		if f, _ := j.asked(); f.After != c.after {
			t.Errorf("%s: the stream begins after %q, want %q", name, f.After, c.after)
		}
		l.stop()
	}
}

// A stream that says nothing shows it is alive, and goes on telling
// (ADR-0058, item 6).
func TestAStreamThatSaysNothingShowsItIsAlive(t *testing.T) {
	j := newJournal()
	d := door(t, j, Options{})
	d.alive = 5 * time.Millisecond
	l := listen(t, serve(t, d).URL, `{}`)
	l.next()
	for i := 0; i < 3; i++ {
		if block, _ := l.next(); block != ": nothing new\n" {
			t.Fatalf("a silent stream says %q", block)
		}
	}
	j.events <- submitted
	if block, more := l.entry(); !more || !strings.HasPrefix(block, "id: "+submitted.ID+"\n") {
		t.Errorf("after its signs of life the stream says %q", block)
	}
}

// How a stream ends when the server ends it: with a last entry that
// says why, as a refusal (ADR-0058, item 7).
func TestHowAStreamEnds(t *testing.T) {
	// revoked is whether the key of the caller was revoked.
	var revoked atomic.Bool
	who := func(*http.Request) (store.Principal, error) {
		if revoked.Load() {
			return store.Principal{}, store.ErrUnknownKey
		}
		return ann, nil
	}
	open := func(t *testing.T, alive time.Duration) (*journal, *Door, *listener, func() Request) {
		revoked.Store(false)
		j := newJournal()
		reports := make(chan Request, 4)
		d := door(t, j, Options{Who: who, Report: func(r Request) { reports <- r }})
		if alive > 0 {
			d.alive = alive
		}
		srv := serve(t, d)
		l := listen(t, srv.URL, `{}`)
		l.next()
		return j, d, l, func() Request {
			select {
			case r := <-reports:
				return r
			case <-time.After(long):
				t.Fatal("the stream was not told of")
				return Request{}
			}
		}
	}

	t.Run("the key is revoked while the stream is silent", func(t *testing.T) {
		_, d, l, report := open(t, 5*time.Millisecond)
		revoked.Store(true)
		if e := l.ended(); e.Code != CodeUnauthenticated || e.Message != unauthenticated().Message {
			t.Errorf("the stream ended with %+v", e)
		}
		if r := report(); r.Code != CodeUnauthenticated || r.Status != 200 || !r.Stream || r.Entries != 0 {
			t.Errorf("the stream is reported as %+v", r)
		}
		until(t, "the place is given back", func() bool { return d.Streams() == 0 })
	})

	t.Run("the key is revoked, and an event comes", func(t *testing.T) {
		j, _, l, report := open(t, 0)
		revoked.Store(true)
		j.events <- submitted
		// The event is not told: the stream ends before it.
		if e := l.ended(); e.Code != CodeUnauthenticated {
			t.Errorf("the stream ended with %+v", e)
		}
		if r := report(); r.Code != CodeUnauthenticated || r.Entries != 0 {
			t.Errorf("the stream is reported as %+v", r)
		}
	})

	t.Run("the server stops", func(t *testing.T) {
		j, d, l, report := open(t, 0)
		other := listen(t, l.resp.Request.URL.Scheme+"://"+l.resp.Request.URL.Host, `{}`)
		other.next()
		d.EndStreams()
		for _, l := range []*listener{l, other} {
			if e := l.ended(); e.Code != store.CodeClosed || !strings.Contains(e.Message, "the server stops") || !strings.Contains(e.Hint, "after") {
				t.Errorf("the stream ended with %+v", e)
			}
		}
		if r := report(); r.Code != store.CodeClosed || r.Status != 200 {
			t.Errorf("the stream is reported as %+v", r)
		}
		until(t, "both streams are closed", func() bool { return j.closed.Load() == 2 && d.Streams() == 0 })
		// And no stream is opened after it, while a command is answered.
		w := post(d, "/v1/subscribe", `{}`)
		if e := refusal(t, w); w.Code != http.StatusServiceUnavailable || e.Code != store.CodeClosed {
			t.Errorf("a stream asked of a door that ended its streams: %d %+v", w.Code, e)
		}
		if w := post(door(t, engine(echo), Options{}), "/v1/get", `{}`); w.Code != http.StatusOK {
			t.Errorf("a command: %d", w.Code)
		}
		d.EndStreams()
	})

	t.Run("the database is closed", func(t *testing.T) {
		j, _, l, report := open(t, 0)
		j.failures <- &store.Error{Code: store.CodeClosed, Message: "the store is closed"}
		if e := l.ended(); e.Code != store.CodeClosed {
			t.Errorf("the stream ended with %+v", e)
		}
		if r := report(); r.Code != store.CodeClosed || r.Err != nil {
			t.Errorf("the stream is reported as %+v", r)
		}
	})

	t.Run("the engine fails", func(t *testing.T) {
		j, _, l, report := open(t, 0)
		j.events <- submitted
		l.entry()
		j.failures <- errors.New("disk I/O error at /var/lib/acme.fado")
		e := l.ended()
		if e.Code != command.CodeInternal || strings.Contains(e.Message+e.Hint, "acme") {
			t.Errorf("the stream ended with %+v", e)
		}
		// The words are for whoever runs the server.
		if r := report(); r.Code != command.CodeInternal || r.Err == nil || !strings.Contains(r.Err.Error(), "acme.fado") || r.Entries != 1 {
			t.Errorf("the stream is reported as %+v", r)
		}
	})
}

// A door holds so many streams and no more; one beyond them is refused
// at once, and a stream that is closed makes room (ADR-0059, item 2).
func TestMoreStreamsThanTheDoorHolds(t *testing.T) {
	j := newJournal()
	d := door(t, j, Options{Limits: Limits{Streams: 2}})
	if got := d.Limits().Streams; got != 2 {
		t.Fatalf("the door holds %d streams, want 2", got)
	}
	if got := door(t, j, Options{}).Limits().Streams; got != 256 {
		t.Errorf("a door that was told nothing holds %d streams", got)
	}
	srv := serve(t, d)
	first, second := listen(t, srv.URL, `{}`), listen(t, srv.URL, `{}`)
	first.next()
	second.next()

	before := time.Now()
	w := post(d, "/v1/subscribe", `{}`)
	e := refusal(t, w)
	if w.Code != http.StatusServiceUnavailable || e.Code != CodeBusy || !strings.Contains(e.Message, "2 streams of changes") {
		t.Errorf("a third stream: %d %+v", w.Code, e)
	}
	if waited := time.Since(before); waited >= busyWait {
		t.Errorf("a stream beyond the limit was refused after %v: it waits for nothing", waited)
	}
	if n := j.opened.Load(); n != 2 {
		t.Errorf("%d streams were opened in the database, want 2", n)
	}
	// A command does not take the place of a stream, nor a stream that
	// of a command.
	if w := post(door(t, engine(echo), Options{Limits: Limits{Streams: 1}}), "/v1/get", `{}`); w.Code != http.StatusOK {
		t.Errorf("a command: %d", w.Code)
	}

	first.stop()
	until(t, "the place of the first is given back", func() bool { return d.Streams() == 1 })
	third := listen(t, srv.URL, `{}`)
	if block, _ := third.next(); third.resp.StatusCode != http.StatusOK || block != ": the stream is open\n" {
		t.Errorf("a stream after one was closed: %d %q", third.resp.StatusCode, block)
	}
}

// What is not a stream is refused as a command is, with one JSON
// object, and opens nothing.
func TestWhatIsNotAStream(t *testing.T) {
	j := newJournal()
	var reports []Request
	d := door(t, j, Options{
		Limits: Limits{RequestBytes: 64},
		Report: func(r Request) { reports = append(reports, r) },
		Who: func(r *http.Request) (store.Principal, error) {
			if r.Header.Get("Authorization") == "" {
				return store.Principal{}, errors.New("no key")
			}
			return ann, nil
		},
	})
	send := func(method, address, body, contentType, key string) (*httptest.ResponseRecorder, store.Error) {
		t.Helper()
		r := httptest.NewRequest(method, address, strings.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if got := w.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s %s: the refusal says its body is %q", method, address, got)
		}
		return w, refusal(t, w)
	}
	const jsonBody, key = "application/json", "fado_K"
	for name, c := range map[string]struct {
		method, address, body, contentType, key string
		status                                  int
		code, path                              string
	}{
		"a GET":                              {http.MethodGet, "/v1/subscribe", ``, "", key, 405, command.CodeBadJSON, ""},
		"members in an address":              {http.MethodPost, "/v1/subscribe?classes=Invoice", `{}`, jsonBody, key, 400, command.CodeBadJSON, ""},
		"no key":                             {http.MethodPost, "/v1/subscribe", `{}`, jsonBody, "", 401, CodeUnauthenticated, ""},
		"a body that is not said to be JSON": {http.MethodPost, "/v1/subscribe", `{}`, "text/plain", key, 415, command.CodeBadJSON, ""},
		"no body":                            {http.MethodPost, "/v1/subscribe", ``, jsonBody, key, 400, command.CodeBadJSON, ""},
		"a member it does not take":          {http.MethodPost, "/v1/subscribe", `{"class": "Invoice"}`, jsonBody, key, 400, command.CodeBadJSON, "/class"},
		"a member of the wrong type":         {http.MethodPost, "/v1/subscribe", `{"kinds": "set"}`, jsonBody, key, 400, command.CodeBadJSON, "/kinds"},
		"a body that is too large":           {http.MethodPost, "/v1/subscribe", `{"classes": ["` + strings.Repeat("x", 80) + `"]}`, jsonBody, key, 413, CodeTooLarge, ""},
	} {
		w, e := send(c.method, c.address, c.body, c.contentType, c.key)
		if w.Code != c.status || e.Code != c.code || e.Path != c.path {
			t.Errorf("%s: %d %+v, want %d %s at %q", name, w.Code, e, c.status, c.code, c.path)
		}
	}
	if n := j.opened.Load(); n != 0 {
		t.Errorf("%d streams were opened by requests that were refused", n)
	}

	// A refusal of the engine keeps its words, and a failure does not.
	j.fail = &store.Error{Code: store.CodeUnknownClass, Message: `no class is named "Invoice"`}
	if w, e := send(http.MethodPost, "/v1/subscribe", `{"classes": ["Invoice"]}`, jsonBody, key); w.Code != http.StatusNotFound || e != *j.fail.(*store.Error) {
		t.Errorf("a class the database does not have: %d %+v", w.Code, e)
	}
	j.fail = errors.New("disk I/O error at /var/lib/acme.fado")
	if w, e := send(http.MethodPost, "/v1/subscribe", `{}`, jsonBody, key); w.Code != http.StatusInternalServerError || e.Code != command.CodeInternal || strings.Contains(e.Message, "acme") {
		t.Errorf("a failure of the engine: %d %+v", w.Code, e)
	}
	last := reports[len(reports)-1]
	if last.Err == nil || last.Code != command.CodeInternal || last.Stream || last.Command != "subscribe" {
		t.Errorf("the failure is reported as %+v", last)
	}
	if d.Streams() != 0 {
		t.Errorf("the door holds %d streams after refusals alone", d.Streams())
	}
	for i, r := range reports {
		if r.Stream || r.Status == 200 {
			t.Errorf("report %d of a refused stream: %+v", i, r)
		}
	}
}

// A caller that takes nothing of what it is sent is cut, so that one
// that went away without a word holds no stream.
func TestACallerThatDoesNotReadIsCut(t *testing.T) {
	j := newJournal()
	d := door(t, j, Options{})
	d.take = 50 * time.Millisecond
	srv := serve(t, d)
	// The caller opens the stream and reads nothing of it.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/subscribe", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Events as large as an event can be, until nothing more fits on the
	// line and the door gives up.
	large := *submitted
	large.Reason = strings.Repeat("a reason of some length. ", 40<<10)
	sent := 0
	for end := time.Now().Add(long); j.closed.Load() == 0; {
		select {
		case j.events <- &large:
			sent++
		case <-time.After(time.Millisecond):
		}
		if time.Now().After(end) {
			t.Fatalf("the door went on writing to a caller that reads nothing: %d events of 1 MiB", sent)
		}
	}
	until(t, "the place is given back", func() bool { return d.Streams() == 0 })
}

// The time a server gives a request to arrive and a reply to be written
// does not end a stream.
func TestAStreamOutlivesTheTimesOfTheServer(t *testing.T) {
	j := newJournal()
	d := door(t, j, Options{})
	srv := httptest.NewUnstartedServer(d)
	const short = 100 * time.Millisecond
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = short, short
	srv.Start()
	t.Cleanup(srv.Close)
	l := listen(t, srv.URL, `{}`)
	l.next()
	time.Sleep(3 * short)
	j.events <- submitted
	if block, more := l.entry(); !more || !strings.HasPrefix(block, "id: "+submitted.ID+"\n") {
		t.Errorf("after the times of the server passed, the stream says %q, open: %v", block, more)
	}
	// And a command sent on after it has the server's times again.
	resp, err := http.Post(srv.URL+"/v1/get", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// The chapter of the manual on the stream says what the door does: how
// often a silent stream shows it is alive, how long a caller is given
// to take what it was sent, and the two lines that are no event.
func TestTheChapterOnTheStreamAndTheDoor(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "the-stream-of-changes.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	chapter := strings.ReplaceAll(string(text), "\r\n", "\n")
	for what, c := range map[string]struct {
		says  string
		holds bool
	}{
		"how often a silent stream shows it is alive": {"Every fifteen seconds without an event", aliveEvery == 15*time.Second},
		"when a caller that takes nothing is cut":     {"is cut** after\n  thirty seconds", takeWithin == 30*time.Second},
		"the line of a silent stream":                 {"`" + strings.TrimSpace(lineAlive) + "`", true},
		"the first line of a stream":                  {"```\n" + lineOpen, true},
		"the header a proxy reads":                    {"`X-Accel-Buffering: no`", true},
		"the header of a caller that comes back":      {"`" + lastEventID + "`", true},
	} {
		if !strings.Contains(chapter, c.says) || !c.holds {
			t.Errorf("the chapter and the door differ on %s: the chapter is to say %q", what, c.says)
		}
	}
}
