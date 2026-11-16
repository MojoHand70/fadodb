package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The way in for messages from outside (ADR-0065): POST /v1/in/<inlet>.

// inbox stands in for a database with inlets: it takes a message as the
// test says, and runs commands as an engine does.
type inbox struct {
	engine
	receive func(ctx context.Context, inlet string, m *store.Message) (*store.Receipt, error)
}

func (b inbox) Receive(ctx context.Context, inlet string, m *store.Message) (*store.Receipt, error) {
	return b.receive(ctx, inlet, m)
}

// nobody is the Who of a door whose messages must not be asked for a
// key: a message that reaches it fails the test.
func nobody(t testing.TB) Who {
	return func(r *http.Request) (store.Principal, error) {
		if strings.HasPrefix(r.URL.Path, "/v1/in/") {
			t.Errorf("the door asked a message for a key: %s", r.URL.Path)
		}
		return ann, nil
	}
}

// bank is the receipt of a message for the inlet of the tests.
func bank() *store.Receipt { return &store.Receipt{Inlet: "bank.paid", As: "bank-in"} }

// send sends a message to a door as a sender does: no key, the body as
// it is, and the headers given.
func send(d http.Handler, path, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Add(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// What a sender is answered (ADR-0065, item 7): the status says what
// happened, and the body says no more than the code. The words are in
// what the door tells of the request, for whoever runs the server.
func TestWhatASenderIsAnswered(t *testing.T) {
	refused := &store.Error{Code: store.CodeGuardFalse, Message: "pay leaves sent; the document FV/2026/11/0043 of Globex is in paid", Hint: "the rule of pay: INGROUP(\"payments\")", Path: "/access/transition/pay"}
	cases := []struct {
		what    string
		receipt *store.Receipt
		err     error
		status  int
		body    string
		told    Request
	}{
		{"the command was carried out", bank(), nil, 200, `{"ok":true}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", User: "bank-in", Status: 200}},
		{"the inlet does not act on it", &store.Receipt{Inlet: "bank.paid", As: "bank-in", Ignored: true}, nil, 200, `{"ok":true,"ignored":true}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", User: "bank-in", Status: 200, Ignored: true}},
		// Nobody is named for a message that has not shown who sent it.
		{"the proof does not hold", bank(), store.ErrNotProven, 401, `{"error":{"code":"unauthenticated"}}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", Status: 401, Code: CodeUnauthenticated}},
		{"no inlet of that name", nil, &store.Error{Code: store.CodeUnknownInlet, Message: "the database has no inlet \"bank.paid\""}, 404, `{"error":{"code":"unknown_inlet"}}`,
			Request{Command: "in/bank.paid", Status: 404, Code: store.CodeUnknownInlet}},
		{"the message is not what the inlet reads", bank(), &store.Error{Code: store.CodeBadMessage, Path: "/key", Message: "the message does not give its key"}, 422, `{"error":{"code":"bad_message"}}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", User: "bank-in", Status: 422, Code: store.CodeBadMessage}},
		{"the database refused the command", bank(), refused, 409, `{"error":{"code":"guard_false"}}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", User: "bank-in", Status: 409, Code: store.CodeGuardFalse}},
		{"the user of the inlet may not", bank(), &store.Error{Code: store.CodeForbidden, Message: "bank-in may not create this document of Invoice"}, 403, `{"error":{"code":"forbidden"}}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", User: "bank-in", Status: 403, Code: store.CodeForbidden}},
		// The server cannot check the proof now: the sender comes back.
		{"the secret is not there", bank(), &store.Error{Code: store.CodeSecretUnavailable, Message: "the secret signing_key of the connection bank: it is in neither place"}, 503, `{"error":{"code":"secret_unavailable"}}`,
			Request{Command: "in/bank.paid", Inlet: "bank.paid", Status: 503, Code: store.CodeSecretUnavailable}},
	}
	for _, tc := range cases {
		var told []Request
		d := door(t, inbox{receive: func(_ context.Context, inlet string, _ *store.Message) (*store.Receipt, error) {
			if inlet != "bank.paid" {
				t.Errorf("%s: the door asked for the inlet %q", tc.what, inlet)
			}
			return tc.receipt, tc.err
		}}, Options{Inbound: true, Who: nobody(t), Report: func(r Request) { told = append(told, r) }})
		w := send(d, "/v1/in/bank.paid", `{"id": "ntc_8841"}`)
		if w.Code != tc.status || w.Body.String() != tc.body+"\n" || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: the sender is answered %d %s, want %d %s", tc.what, w.Code, w.Body, tc.status, tc.body)
		}
		// No word of the database reaches the sender.
		for _, word := range []string{"Globex", "INGROUP", "FV/2026", `"message"`, `"hint"`, `"path"`} {
			if strings.Contains(w.Body.String(), word) {
				t.Errorf("%s: the answer to the sender says %q: %s", tc.what, word, w.Body)
			}
		}
		if len(told) != 1 {
			t.Fatalf("%s: the door told of %d requests", tc.what, len(told))
		}
		got := told[0]
		// What the database refused is told to whoever runs the server,
		// in its own words.
		refusal := tc.err != nil && !errors.Is(tc.err, store.ErrNotProven)
		if (got.Err != nil) != refusal || (refusal && got.Err.Error() != tc.err.Error()) {
			t.Errorf("%s: the door told the error %v", tc.what, got.Err)
		}
		got.At, got.Took, got.Err = time.Time{}, 0, nil
		if got != tc.told {
			t.Errorf("%s: the door told %+v, want %+v", tc.what, got, tc.told)
		}
	}
}

// A message reaches the database as its sender wrote it: the headers,
// the parameters of the address, the body, and whether it is a form.
func TestAMessageArrivesAsItWasSent(t *testing.T) {
	var got *store.Message
	d := door(t, inbox{receive: func(_ context.Context, _ string, m *store.Message) (*store.Receipt, error) {
		got = m
		return bank(), nil
	}}, Options{Inbound: true, Who: nobody(t)})
	send(d, "/v1/in/bank.paid?source=web&source=other&empty=", `{"id": "ntc_8841"} `,
		"Bank-Signature", "t=1,v1=abc", "Content-Type", "application/json; charset=utf-8", "X-Tag", "a", "X-Tag", "b")
	if got == nil || string(got.Body) != `{"id": "ntc_8841"} ` || got.Form ||
		got.Headers["Bank-Signature"] != "t=1,v1=abc" || got.Headers["X-Tag"] != "a, b" || got.Params["source"] != "web" || len(got.Params) != 2 {
		t.Errorf("the message as the database got it: %+v", got)
	}
	send(d, "/v1/in/shop.order", "number=ORD-7&item=a+lamp", "Content-Type", "application/x-www-form-urlencoded")
	if got == nil || !got.Form || string(got.Body) != "number=ORD-7&item=a+lamp" || got.Params != nil {
		t.Errorf("a form as the database got it: %+v", got)
	}
	// A message with no body is one too: an inlet may read the address.
	if w := send(d, "/v1/in/bank.paid?id=7", ""); w.Code != http.StatusOK || len(got.Body) != 0 || got.Params["id"] != "7" {
		t.Errorf("a message with no body: %d %+v", w.Code, got)
	}
}

// A door takes no message until it is told to (ADR-0065, item 8): every
// address of an inlet answers as a name that is none, and the database
// is not asked.
func TestADoorThatTakesNoMessages(t *testing.T) {
	var told []Request
	d := door(t, inbox{receive: func(context.Context, string, *store.Message) (*store.Receipt, error) {
		t.Error("a door that takes no messages asked its database")
		return bank(), nil
	}}, Options{Who: nobody(t), Report: func(r Request) { told = append(told, r) }})
	w := send(d, "/v1/in/bank.paid", `{"id": "ntc_8841"}`)
	if w.Code != http.StatusNotFound || w.Body.String() != `{"error":{"code":"unknown_inlet"}}`+"\n" {
		t.Errorf("a message to a door that takes none: %d %s", w.Code, w.Body)
	}
	if len(told) != 1 || told[0].Command != "in/bank.paid" || told[0].Code != store.CodeUnknownInlet || told[0].Inlet != "" {
		t.Errorf("the door told %+v", told)
	}
}

// A message is sent with POST, is at most as large as the door reads,
// and has a place of its own or none.
func TestWhatADoorTakesOnOfMessages(t *testing.T) {
	d := door(t, inbox{receive: func(context.Context, string, *store.Message) (*store.Receipt, error) { return bank(), nil }},
		Options{Inbound: true, Who: nobody(t), Limits: Limits{MessageBytes: 64}})
	if got := d.Limits(); got.MessageBytes != 64 || got.Messages != DefaultMessages || got.RequestBytes != DefaultRequestBytes {
		t.Errorf("the limits of the door: %+v", got)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/in/bank.paid", nil)
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Errorf("a message sent with GET: %d %s", w.Code, w.Body)
	}
	// Too large, whether the message says so or not.
	large := strings.Repeat("x", 65)
	if w := send(d, "/v1/in/bank.paid", large); w.Code != http.StatusRequestEntityTooLarge || w.Body.String() != `{"error":{"code":"too_large"}}`+"\n" {
		t.Errorf("a message that is too large: %d %s", w.Code, w.Body)
	}
	unsaid := httptest.NewRequest(http.MethodPost, "/v1/in/bank.paid", strings.NewReader(large))
	unsaid.ContentLength = -1
	w = httptest.NewRecorder()
	d.ServeHTTP(w, unsaid)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a message that is too large and does not say so: %d %s", w.Code, w.Body)
	}
	if w := send(d, "/v1/in/bank.paid", strings.Repeat("x", 64)); w.Code != http.StatusOK {
		t.Errorf("a message as large as the door reads: %d %s", w.Code, w.Body)
	}
	if _, err := New(inbox{}, Options{Who: asAnn, Limits: Limits{Messages: -1}}); err == nil {
		t.Error("a door was made with a limit below zero")
	}

	// One place: while a message is worked, the next is refused at
	// once, and a command still runs, since it has a place of its own.
	entered, release := make(chan struct{}), make(chan struct{})
	one := door(t, inbox{engine: echo, receive: func(context.Context, string, *store.Message) (*store.Receipt, error) {
		entered <- struct{}{}
		<-release
		return bank(), nil
	}}, Options{Inbound: true, Limits: Limits{Messages: 1}})
	done := make(chan int)
	go func() { done <- send(one, "/v1/in/bank.paid", `{}`).Code }()
	<-entered
	began := time.Now()
	w = send(one, "/v1/in/bank.paid", `{}`)
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != `{"error":{"code":"busy"}}`+"\n" || w.Header().Get("Retry-After") != "1" || time.Since(began) > busyWait/2 {
		t.Errorf("a message beyond the places of the door: %d %s after %s", w.Code, w.Body, time.Since(began))
	}
	if w := post(one, "/v1/get", `{}`); w.Code != http.StatusOK {
		t.Errorf("a command while the messages are busy: %d %s", w.Code, w.Body)
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Errorf("the message that had the place: %d", code)
	}
}

// A message whose command runs too long is stopped as a command is,
// and the sender is told so by the code.
func TestAMessageThatRunsTooLong(t *testing.T) {
	var told Request
	d := door(t, inbox{receive: func(ctx context.Context, _ string, _ *store.Message) (*store.Receipt, error) {
		<-ctx.Done()
		return bank(), &store.Error{Code: store.CodeStorage, Message: "commit: " + ctx.Err().Error()}
	}}, Options{Inbound: true, Limits: Limits{CommandTime: 20 * time.Millisecond}, Report: func(r Request) { told = r }})
	w := send(d, "/v1/in/bank.paid", `{}`)
	if w.Code != http.StatusGatewayTimeout || w.Body.String() != `{"error":{"code":"timeout"}}`+"\n" || told.Code != CodeTimeout || told.User != "bank-in" {
		t.Errorf("a message that runs too long: %d %s, told %+v", w.Code, w.Body, told)
	}
	// A failure of the engine is said to nobody outside.
	d = door(t, inbox{receive: func(context.Context, string, *store.Message) (*store.Receipt, error) {
		return bank(), errors.New("the disk of Acme is full")
	}}, Options{Inbound: true, Report: func(r Request) { told = r }})
	w = send(d, "/v1/in/bank.paid", `{}`)
	if w.Code != http.StatusInternalServerError || w.Body.String() != `{"error":{"code":"internal"}}`+"\n" || told.Code != command.CodeInternal || told.Err == nil {
		t.Errorf("a message that fails in the engine: %d %s, told %+v", w.Code, w.Body, told)
	}
}

// The body of a message arrives in a short time or not at all: a
// sender that stops in the middle of it is answered, and gives its
// place back.
func TestAMessageThatDoesNotArrive(t *testing.T) {
	d := door(t, inbox{receive: func(context.Context, string, *store.Message) (*store.Receipt, error) { return bank(), nil }},
		Options{Inbound: true, Limits: Limits{Messages: 1}})
	d.arrive = 50 * time.Millisecond
	srv := httptest.NewServer(d)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A hundred bytes are announced, and three are sent.
	if _, err := conn.Write([]byte("POST /v1/in/bank.paid HTTP/1.1\r\nHost: acme\r\nContent-Length: 100\r\n\r\nabc")); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_ = conn.SetReadDeadline(began.Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("a message that stops in the middle is not answered: %v", err)
	}
	answer, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity || strings.TrimSpace(string(answer)) != `{"error":{"code":"bad_message"}}` || time.Since(began) > 2*time.Second {
		t.Errorf("a message that stops in the middle: %d %s after %s", resp.StatusCode, answer, time.Since(began))
	}
	// Its place is free: the next message is taken.
	next, err := http.Post(srv.URL+"/v1/in/bank.paid", "application/json", strings.NewReader(`{"id": "ntc_8841"}`))
	if err != nil {
		t.Fatal(err)
	}
	next.Body.Close()
	if next.StatusCode != http.StatusOK {
		t.Errorf("the message after one that did not arrive: %d", next.StatusCode)
	}
}
