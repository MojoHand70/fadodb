package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// engine stands in for a database: it runs a command as the test says.
type engine func(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error)

func (e engine) Exec(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error) {
	return e(ctx, p, name, object)
}

// An engine of these tests keeps no file; the tests of the files have
// one that does.
func (engine) PutBlob(context.Context, string, string, io.Reader, ...store.Option) (store.Values, error) {
	return nil, errors.New("the engine of this test keeps no file")
}

func (engine) OpenFile(context.Context, string, string, string, ...store.Option) (io.ReadCloser, *store.FileInfo, error) {
	return nil, nil, errors.New("the engine of this test keeps no file")
}

func (engine) OpenBlob(context.Context, string, ...store.Option) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("the engine of this test keeps no file")
}

// And it has no log to read forward; the tests of the stream have one.
func (engine) Subscribe(context.Context, store.Principal, store.ChangeFilter) (Stream, error) {
	return nil, errors.New("the engine of this test has no log")
}

// ann is who sends every request of a test that does not say otherwise.
var ann = store.Principal{ID: "ann", Groups: []string{"finance"}, Kind: store.ActorPerson}

func asAnn(*http.Request) (store.Principal, error) { return ann, nil }

// echo answers what it was sent.
func echo(_ context.Context, p store.Principal, name string, object []byte) ([]byte, error) {
	return []byte(fmt.Sprintf(`{"ran":%q,"as":%q,"object":%s}`, name, p.ID, object)), nil
}

func door(t testing.TB, db Database, o Options) *Door {
	t.Helper()
	if o.Who == nil {
		o.Who = asAnn
	}
	d, err := New(db, o)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// post sends a command to a door and answers the reply.
func post(d http.Handler, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// refusal reads the refusal a reply carries.
func refusal(t testing.TB, w *httptest.ResponseRecorder) store.Error {
	t.Helper()
	var got struct {
		Error *store.Error `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Error == nil {
		t.Fatalf("the reply carries no refusal: %d %s", w.Code, w.Body)
	}
	return *got.Error
}

func TestACommandRunsAndIsAnswered(t *testing.T) {
	d := door(t, engine(echo), Options{})
	w := post(d, "/v1/transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	want := `{"ran":"transition","as":"ann","object":{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}}` + "\n"
	if got := w.Body.String(); got != want {
		t.Errorf("the answer is\n%s\nwant\n%s", got, want)
	}
	for header, want := range map[string]string{
		"Content-Type":           "application/json",
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"Content-Length":         fmt.Sprint(len(want)),
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s is %q, want %q", header, got, want)
		}
	}
}

// A refusal of the engine is answered as the engine wrote it, under the
// status of its code.
func TestARefusalOfTheEngineKeepsItsWords(t *testing.T) {
	forbidden := &store.Error{Code: store.CodeForbidden, Message: "bob may not take approve from review",
		Hint: `the rule of approve: INGROUP("finance")`, Path: "/access/transition/approve"}
	d := door(t, engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		text, _ := command.Marshal(map[string]any{"error": forbidden})
		return text, forbidden
	}), Options{})
	w := post(d, "/v1/transition", `{}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
	if got := refusal(t, w); got != *forbidden {
		t.Errorf("the refusal is %+v, want %+v", got, *forbidden)
	}
}

func TestWhatIsNotACommand(t *testing.T) {
	ran := false
	d := door(t, engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		ran = true
		return []byte(`{}`), nil
	}), Options{})
	send := func(method, path, contentType, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		return w
	}
	for _, c := range []struct {
		name, method, path, contentType, body string
		status                                int
		code, says                            string
	}{
		{"an address outside /v1", "POST", "/transition", "application/json", `{}`, 404, command.CodeUnknownCommand, "POST /v1/<command>"},
		{"the root", "GET", "/", "", "", 404, command.CodeUnknownCommand, "POST /v1/<command>"},
		{"a read sent as GET", "GET", "/v1/get", "", "", 405, command.CodeBadJSON, "a command that reads is a POST too"},
		{"a delete sent as DELETE", "DELETE", "/v1/delete", "application/json", `{}`, 405, command.CodeBadJSON, "POST /v1/delete"},
		{"a body that is not said to be JSON", "POST", "/v1/get", "", `{}`, 415, command.CodeBadJSON, "Content-Type: application/json"},
		{"a form", "POST", "/v1/get", "application/x-www-form-urlencoded", `class=Invoice`, 415, command.CodeBadJSON, "Content-Type: application/json"},
		{"a type that only begins like JSON", "POST", "/v1/get", "application/jsonp", `{}`, 415, command.CodeBadJSON, "Content-Type: application/json"},
		{"members in the address", "POST", "/v1/query?class=Invoice&limit=10", "application/json", `{}`, 400, command.CodeBadJSON, "?class=Invoice&limit=10"},
		{"no body", "POST", "/v1/list_classes", "application/json", ``, 400, command.CodeBadJSON, "sent as {}"},
		{"a body of spaces", "POST", "/v1/list_classes", "application/json", "  \n", 400, command.CodeBadJSON, "sent as {}"},
	} {
		ran = false
		w := send(c.method, c.path, c.contentType, c.body)
		got := refusal(t, w)
		if w.Code != c.status || got.Code != c.code {
			t.Errorf("%s: status %d and code %q, want %d and %q", c.name, w.Code, got.Code, c.status, c.code)
		}
		if !strings.Contains(got.Message+" "+got.Hint, c.says) {
			t.Errorf("%s: the refusal does not say %q: %+v", c.name, c.says, got)
		}
		if ran {
			t.Errorf("%s: a command ran", c.name)
		}
		if c.status == 405 && w.Header().Get("Allow") != "POST" {
			t.Errorf("%s: Allow is %q", c.name, w.Header().Get("Allow"))
		}
	}

	// A type with a parameter, and one in capitals, are JSON.
	for _, contentType := range []string{"application/json; charset=utf-8", "Application/JSON", "application/json;charset=utf-8"} {
		if w := send("POST", "/v1/get", contentType, `{}`); w.Code != http.StatusOK {
			t.Errorf("Content-Type %q is answered %d: %s", contentType, w.Code, w.Body)
		}
	}
}

// The name of a command is whatever follows /v1/, and the command set
// says whether there is one.
func TestTheNameIsTheRestOfTheAddress(t *testing.T) {
	var names []string
	d := door(t, engine(func(_ context.Context, _ store.Principal, name string, _ []byte) ([]byte, error) {
		names = append(names, name)
		return []byte(`{}`), nil
	}), Options{})
	for _, path := range []string{"/v1/get", "/v1/", "/v1/docs/Invoice/7", "/v1/Transition"} {
		post(d, path, `{}`)
	}
	if got, want := strings.Join(names, " | "), "get |  | docs/Invoice/7 | Transition"; got != want {
		t.Errorf("the names are %q, want %q", got, want)
	}
}

// Who is asked before anything of the request is read, and whatever it
// says is wrong, the caller is told the same.
func TestWhoSendsTheRequest(t *testing.T) {
	ran := false
	db := engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		ran = true
		return []byte(`{}`), nil
	})
	var replies []string
	for _, wrong := range []string{"no credential", "the key 7GQ2M4XK0P was revoked", "the user olga is disabled"} {
		d := door(t, db, Options{Who: func(*http.Request) (store.Principal, error) {
			return store.Principal{}, errors.New(wrong)
		}})
		r := httptest.NewRequest(http.MethodPost, "/v1/get", unread{t})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized || refusal(t, w).Code != CodeUnauthenticated {
			t.Errorf("%s: status %d: %s", wrong, w.Code, w.Body)
		}
		if w.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: the reply names no way to say who is calling", wrong)
		}
		replies = append(replies, w.Body.String())
	}
	if ran {
		t.Error("a command ran for a caller the door could not tell")
	}
	if replies[0] != replies[1] || replies[1] != replies[2] {
		t.Errorf("the refusals differ, so a guess learns something:\n%s", strings.Join(replies, ""))
	}
	if strings.Contains(replies[1], "7GQ2M4XK0P") || strings.Contains(replies[2], "olga") {
		t.Errorf("the refusal passes on the words of Who: %s", strings.Join(replies, ""))
	}

	// The principal Who answers is the one the command runs as.
	var as store.Principal
	d := door(t, engine(func(_ context.Context, p store.Principal, _ string, _ []byte) ([]byte, error) {
		as = p
		return []byte(`{}`), nil
	}), Options{Who: func(r *http.Request) (store.Principal, error) {
		if r.Header.Get("Authorization") != "Bearer fado_test" {
			return store.Principal{}, errors.New("unknown")
		}
		return store.Principal{ID: "ap-agent", Kind: store.ActorAgent, Groups: []string{"finance"}}, nil
	}})
	r := httptest.NewRequest(http.MethodPost, "/v1/get", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer fado_test")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != http.StatusOK || as.ID != "ap-agent" || as.Kind != store.ActorAgent || len(as.Groups) != 1 {
		t.Errorf("the command ran as %+v, status %d", as, w.Code)
	}
}

// unread is a body that fails the test when it is read.
type unread struct{ t testing.TB }

func (u unread) Read([]byte) (int, error) {
	u.t.Error("the body was read before the door knew who sent it")
	return 0, io.EOF
}

func TestARequestThatIsTooLarge(t *testing.T) {
	ran := false
	d := door(t, engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		ran = true
		return []byte(`{}`), nil
	}), Options{Limits: Limits{RequestBytes: 64}})
	long := `{"class": "Invoice", "values": {"note": "` + strings.Repeat("x", 80) + `"}}`

	// A length the request states.
	w := post(d, "/v1/create", long)
	if got := refusal(t, w); w.Code != http.StatusRequestEntityTooLarge || got.Code != CodeTooLarge || !strings.Contains(got.Message, "64 bytes") {
		t.Errorf("a stated length: %d %+v", w.Code, got)
	}
	// A length it does not state.
	r := httptest.NewRequest(http.MethodPost, "/v1/create", io.MultiReader(strings.NewReader(long)))
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if got := refusal(t, w); w.Code != http.StatusRequestEntityTooLarge || got.Code != CodeTooLarge {
		t.Errorf("an unstated length: %d %+v", w.Code, got)
	}
	if ran {
		t.Error("a command ran from a request that was too large")
	}
	// At the limit it runs, with its length stated or not.
	exact := `{"note": "` + strings.Repeat("x", 64-12) + `"}`
	if len(exact) != 64 {
		t.Fatalf("the body of the test is %d bytes", len(exact))
	}
	if w := post(d, "/v1/create", exact); w.Code != http.StatusOK {
		t.Errorf("a request of the largest size: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest(http.MethodPost, "/v1/create", io.MultiReader(strings.NewReader(exact)))
	r.ContentLength = -1
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("a request of the largest size, its length unstated: %d %s", w.Code, w.Body)
	}
}

// A body longer than what is read in one piece arrives whole.
func TestALongBodyArrivesWhole(t *testing.T) {
	var got int
	d := door(t, engine(func(_ context.Context, _ store.Principal, _ string, object []byte) ([]byte, error) {
		got = len(object)
		return []byte(`{}`), nil
	}), Options{})
	long := `{"note": "` + strings.Repeat("x", 3*small) + `"}`
	if w := post(d, "/v1/create", long); w.Code != http.StatusOK || got != len(long) {
		t.Errorf("%d bytes arrived of %d, status %d", got, len(long), w.Code)
	}
}

func TestACommandThatRunsTooLong(t *testing.T) {
	stopped := make(chan struct{})
	d := door(t, engine(func(ctx context.Context, _ store.Principal, name string, _ []byte) ([]byte, error) {
		switch name {
		case "sql":
			// A statement that does not end is stopped through its
			// context, and the engine reports what its storage said.
			<-ctx.Done()
			close(stopped)
			return []byte(`{"error":{"code":"storage","message":"interrupted"}}`), &store.Error{Code: store.CodeStorage, Message: "interrupted"}
		case "transition":
			// A refusal that comes after the time is up is still the
			// refusal: the command was not stopped, it was refused.
			<-ctx.Done()
			e := &store.Error{Code: store.CodeGuardFalse, Message: "the guard of approve is false"}
			text, _ := command.Marshal(map[string]any{"error": e})
			return text, e
		}
		<-ctx.Done()
		return []byte(`{"document":{}}`), nil
	}), Options{Limits: Limits{CommandTime: 20 * time.Millisecond}})

	w := post(d, "/v1/sql", `{}`)
	got := refusal(t, w)
	if w.Code != http.StatusGatewayTimeout || got.Code != CodeTimeout || !strings.Contains(got.Message, "20ms") {
		t.Errorf("a command stopped by the clock: %d %+v", w.Code, got)
	}
	select {
	case <-stopped:
	default:
		t.Error("the command was not told to stop")
	}
	if w := post(d, "/v1/transition", `{}`); w.Code != http.StatusConflict || refusal(t, w).Code != store.CodeGuardFalse {
		t.Errorf("a refusal after the time: %d %s", w.Code, w.Body)
	}
	// A command that ended, however late, ran.
	if w := post(d, "/v1/get", `{}`); w.Code != http.StatusOK {
		t.Errorf("a command that ended late: %d %s", w.Code, w.Body)
	}
}

func TestMoreCommandsThanTheDoorRuns(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	d := door(t, engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		entered <- struct{}{}
		<-release
		return []byte(`{}`), nil
	}), Options{Limits: Limits{Commands: 2}})
	d.wait = 30 * time.Millisecond

	var running sync.WaitGroup
	for range 2 {
		running.Add(1)
		go func() {
			defer running.Done()
			if w := post(d, "/v1/get", `{}`); w.Code != http.StatusOK {
				t.Errorf("a command within the limit: %d %s", w.Code, w.Body)
			}
		}()
	}
	<-entered
	<-entered

	// The third waits its short time and is refused; nothing of it ran.
	start := time.Now()
	w := post(d, "/v1/get", `{}`)
	got := refusal(t, w)
	if w.Code != http.StatusServiceUnavailable || got.Code != CodeBusy || !strings.Contains(got.Message, "2 commands") {
		t.Errorf("a command beyond the limit: %d %+v", w.Code, got)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("the refusal does not say when to come back")
	}
	// Half the wait, and not the whole of it: a clock that ticks
	// coarsely may measure a wait a tick short.
	if waited := time.Since(start); waited < d.wait/2 {
		t.Errorf("it was refused after %v, before the %v it waits", waited, d.wait)
	}
	if len(entered) != 0 {
		t.Error("the command beyond the limit ran")
	}

	// One that comes while a command ends is let in.
	d.wait = 5 * time.Second
	late := make(chan int, 1)
	go func() { late <- post(d, "/v1/get", `{}`).Code }()
	time.Sleep(10 * time.Millisecond)
	close(release)
	if code := <-late; code != http.StatusOK {
		t.Errorf("a command that waited for a slot: %d", code)
	}
	running.Wait()
	if len(d.slots) != 0 {
		t.Errorf("%d slots are held after every command ended", len(d.slots))
	}
}

// A failure that is no refusal is answered without its words, which go
// to whoever runs the server.
func TestAFailureKeepsItsWordsFromTheCaller(t *testing.T) {
	var told []Request
	report := func(r Request) { told = append(told, r) }
	d := door(t, engine(func(_ context.Context, _ store.Principal, name string, _ []byte) ([]byte, error) {
		if name == "get" {
			panic("index out of range [7]")
		}
		return []byte(`{"error":{"code":"internal","message":"the command failed in the engine; the program that runs it has the words"}}`),
			errors.New("disk /var/lib/acme is on fire")
	}), Options{Report: report})

	for i, c := range []struct{ path, words string }{
		{"/v1/transition", "on fire"},
		{"/v1/get", "index out of range"},
	} {
		w := post(d, c.path, `{}`)
		if got := refusal(t, w); w.Code != http.StatusInternalServerError || got.Code != command.CodeInternal {
			t.Errorf("%s: %d %+v", c.path, w.Code, got)
		}
		if strings.Contains(w.Body.String(), c.words) {
			t.Errorf("%s: the caller is told the words of the failure: %s", c.path, w.Body)
		}
		if len(told) != i+1 || told[i].Err == nil || !strings.Contains(told[i].Err.Error(), c.words) || told[i].Code != command.CodeInternal || told[i].Status != 500 {
			t.Errorf("%s: the report is %+v", c.path, told)
		}
	}
}

// slow is how long the command of a test runs when the test reads how
// long the door says it took. The test asks for half of it, since a
// coarse clock may measure a tick short.
const slow = 20 * time.Millisecond

// The report of a request says what was asked and how it ended, and
// nothing of what was sent.
func TestWhatADoorTellsOfARequest(t *testing.T) {
	var told []Request
	guard := &store.Error{Code: store.CodeGuardFalse, Message: "the guard of approve is false"}
	d := door(t, engine(func(_ context.Context, _ store.Principal, name string, _ []byte) ([]byte, error) {
		if name == "transition" {
			return []byte(`{"error":{"code":"guard_false"}}`), guard
		}
		// The set takes a time that any clock can tell.
		time.Sleep(slow)
		return []byte(`{"document":{"values":{"salary":"9000.00 PLN"}}}`), nil
	}), Options{
		Report: func(r Request) { told = append(told, r) },
		Who: func(r *http.Request) (store.Principal, error) {
			if r.Header.Get("Authorization") == "" {
				return store.Principal{}, errors.New("no key")
			}
			return ann, nil
		},
	})
	send := func(path, key string) {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"values": {"salary": "9000.00 PLN"}}`))
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		d.ServeHTTP(httptest.NewRecorder(), r)
	}
	before := time.Now()
	send("/v1/set", "fado_SECRET")
	send("/v1/transition", "fado_SECRET")
	send("/v1/get", "")
	send("/elsewhere", "fado_SECRET")

	want := []Request{
		{Command: "set", User: "ann", Status: 200},
		{Command: "transition", User: "ann", Status: 409, Code: store.CodeGuardFalse},
		{Command: "get", Status: 401, Code: CodeUnauthenticated},
		{Status: 404, Code: command.CodeUnknownCommand},
	}
	if len(told) != len(want) {
		t.Fatalf("%d requests were reported, want %d", len(told), len(want))
	}
	for i, got := range told {
		// A clock that ticks coarsely, as the one of Windows does, may
		// say that a request that did nothing took no time at all.
		if got.At.Before(before) || got.Took < 0 {
			t.Errorf("request %d: at %v, took %v", i, got.At, got.Took)
		}
		if i == 0 && got.Took < slow/2 {
			t.Errorf("the set ran for %v and is reported to have taken %v", slow, got.Took)
		}
		got.At, got.Took = time.Time{}, 0
		if got != want[i] {
			t.Errorf("request %d is reported as %+v, want %+v", i, got, want[i])
		}
	}
	if all := fmt.Sprintf("%+v", told); strings.Contains(all, "SECRET") || strings.Contains(all, "9000") {
		t.Errorf("a report holds a key or a value: %s", all)
	}
}

func TestADoorIsNotMadeWithoutWhatItNeeds(t *testing.T) {
	for name, make := range map[string]func() (*Door, error){
		"no database":      func() (*Door, error) { return New(nil, Options{Who: asAnn}) },
		"no Who":           func() (*Door, error) { return New(engine(echo), Options{}) },
		"a negative limit": func() (*Door, error) { return New(engine(echo), Options{Who: asAnn, Limits: Limits{Commands: -1}}) },
		"a negative time": func() (*Door, error) {
			return New(engine(echo), Options{Who: asAnn, Limits: Limits{CommandTime: -time.Second}})
		},
		"a negative size": func() (*Door, error) { return New(engine(echo), Options{Who: asAnn, Limits: Limits{RequestBytes: -1}}) },
		"a negative number of streams": func() (*Door, error) {
			return New(engine(echo), Options{Who: asAnn, Limits: Limits{Streams: -1}})
		},
	} {
		if d, err := make(); err == nil || d != nil {
			t.Errorf("%s: a door was made", name)
		}
	}
	d := door(t, engine(echo), Options{})
	if got, want := d.Limits(), (Limits{RequestBytes: 16 << 20, CommandTime: 30 * time.Second, Commands: 64, Streams: 256}); got != want {
		t.Errorf("the limits of a door told nothing are %+v, want %+v (ADR-0059, item 2)", got, want)
	}
}

// Two doors in one program share nothing (ADR-0056, item 4): the limit
// of one is not the refusal of the other.
func TestTwoDoorsShareNothing(t *testing.T) {
	hold := make(chan struct{})
	entered := make(chan struct{})
	acme := door(t, engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		entered <- struct{}{}
		<-hold
		return []byte(`{"of":"acme"}`), nil
	}), Options{Limits: Limits{Commands: 1}})
	acme.wait = 10 * time.Millisecond
	globex := door(t, engine(func(context.Context, store.Principal, string, []byte) ([]byte, error) {
		return []byte(`{"of":"globex"}`), nil
	}), Options{Limits: Limits{Commands: 1}})
	globex.wait = 10 * time.Millisecond

	done := make(chan struct{})
	go func() { post(acme, "/v1/get", `{}`); close(done) }()
	<-entered
	if w := post(acme, "/v1/get", `{}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("Acme, full, answers %d", w.Code)
	}
	if w := post(globex, "/v1/get", `{}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "globex") {
		t.Errorf("Globex is answered %d %s while Acme is full", w.Code, w.Body)
	}
	close(hold)
	<-done
}

// The package has no variable: nothing a door keeps is outside it, so
// nothing can pass from one database to another (ADR-0056, item 4). A
// cache, a count or a pool added at package level fails here; it
// belongs in Door.
func TestThePackageKeepsNothing(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, decl := range parsed.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					for _, spec := range d.Specs {
						for _, name := range spec.(*ast.ValueSpec).Names {
							t.Errorf("%s declares the package variable %s", file, name.Name)
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" {
					t.Errorf("%s has an init function", file)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no file of the package was read")
	}
}
