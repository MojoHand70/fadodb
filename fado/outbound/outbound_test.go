package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
)

// setup is the caller of a host program's own setup code (ADR-0052): it
// is in the system groups that define, operate the outbox and run SQL,
// and in no other, so the rules of a class hold for it as for anybody.
var setup = fado.As(fado.Principal{ID: "setup", Kind: fado.ActorService,
	System: []string{fado.GroupDesigners, fado.GroupAdministrators, fado.GroupAnalysts}})

func refusal(err error) string {
	var e *fado.Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return "not a refusal: " + err.Error()
}

func TestEntries(t *testing.T) {
	for in, want := range map[string]entry{
		"slack.com":             {scheme: "https", host: "slack.com"},
		" Slack.COM. ":          {scheme: "https", host: "slack.com"},
		".slack.com":            {scheme: "https", host: ".slack.com", suffix: true},
		"books.internal:8443":   {scheme: "https", host: "books.internal", port: "8443"},
		".internal:8443":        {scheme: "https", host: ".internal", port: "8443", suffix: true},
		"http://localhost:8080": {scheme: "http", host: "localhost", port: "8080"},
		"https://api.test":      {scheme: "https", host: "api.test"},
		"10.1.2.3":              {scheme: "https", host: "10.1.2.3"},
		"[::1]:9000":            {scheme: "https", host: "::1", port: "9000"},
		"http://[::1]":          {scheme: "http", host: "::1"},
	} {
		if got, err := parseEntry(in); err != nil || got != want {
			t.Errorf("%q: %+v, %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", " ", "*", "*.slack.com", "slack.com/api", "https://slack.com/", "user@slack.com", "slack.com:0", "slack.com:http", "slack.com:99999", ".", "http://", "ftp://x.test", "a b"} {
		if e, err := parseEntry(in); err == nil {
			t.Errorf("%q is taken as %+v", in, e)
		}
	}
	if _, err := Allow(Options{Hosts: []string{"ok.test", "not/ok"}}); err == nil {
		t.Error("Allow took a host that is none")
	}
	if _, err := Allow(Options{Connections: map[string][]string{"slack": {"*"}}}); err == nil || !strings.Contains(err.Error(), "connection slack") {
		t.Errorf("Allow took a connection whose host is none: %v", err)
	}
	if _, err := Allow(Options{Connections: map[string][]string{"": {"x.test"}}}); err == nil {
		t.Error("Allow took a connection without a name")
	}
}

// Where a request may go (ADR-0045, item 7, and the owner's ruling of
// 2026-11-10): to a listed host, over the scheme and the port of its
// entry; with a connection to the hosts of that connection and no other,
// without one to the open list and to no connection's.
func TestCheck(t *testing.T) {
	out, err := Allow(Options{
		Hosts: []string{"hooks.partner.example", ".open.example", "http://localhost:8080", "books.internal:8443"},
		Connections: map[string][]string{
			"slack":  {"slack.com", ".slack.com"},
			"sheets": {"sheets.googleapis.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ connection, url, want string }{
		{"", "https://hooks.partner.example/in?x=1", ""},
		{"", "https://HOOKS.partner.example./in", ""},
		{"", "https://hooks.partner.example:443/in", ""},
		{"", "https://a.b.open.example/x", ""},
		{"", "http://localhost:8080/x", ""},
		{"", "https://books.internal:8443/x", ""},
		{"slack", "https://slack.com/api/chat.postMessage", ""},
		{"slack", "https://files.slack.com/upload", ""},
		{"sheets", "https://sheets.googleapis.com/v4/spreadsheets/1/values/A1:append", ""},
		// The connection alone.
		{"slack", "", ""},
		{"", "", ""},
		{"mail", "", "unknown_connection"},
		{"mail", "https://slack.com/api", "unknown_connection"},
		// A host that is not listed, or is listed for somebody else.
		{"", "https://evil.example/collect", "host_not_allowed"},
		{"", "https://open.example/x", "host_not_allowed"},
		{"", "https://notopen.example/x", "host_not_allowed"},
		{"", "https://hooks.partner.example.evil.test/x", "host_not_allowed"},
		{"", "https://slack.com/api/chat.postMessage", "host_not_allowed"},
		{"slack", "https://hooks.partner.example/in", "host_not_allowed"},
		{"slack", "https://sheets.googleapis.com/v4", "host_not_allowed"},
		{"sheets", "https://slack.com/api", "host_not_allowed"},
		{"slack", "https://evilslack.com/api", "host_not_allowed"},
		// The scheme and the port are part of what is listed.
		{"", "http://hooks.partner.example/in", "host_not_allowed"},
		{"", "https://hooks.partner.example:8443/in", "host_not_allowed"},
		{"", "https://localhost:8080/x", "host_not_allowed"},
		{"", "http://localhost/x", "host_not_allowed"},
		{"", "https://books.internal/x", "host_not_allowed"},
		// What is no address a request goes to.
		{"", "ftp://hooks.partner.example/in", "host_not_allowed"},
		{"", "hooks.partner.example/in", "host_not_allowed"},
		{"", "https://user:pw@hooks.partner.example/in", "host_not_allowed"},
		{"", "https:///in", "host_not_allowed"},
		{"", "://", "host_not_allowed"},
	} {
		if got := refusal(out.Check(tc.connection, tc.url)); got != tc.want {
			t.Errorf("connection %q, %s: %q, want %q", tc.connection, tc.url, got, tc.want)
		}
	}
	// The refusal says what is listed when only the scheme or the port
	// differs, and never more of the address than its host.
	err = out.Check("", "http://hooks.partner.example/in?token=s3cr3t")
	if e := err.(*fado.Error); !strings.Contains(e.Message, "over https on port 443, not over http on port 80") || strings.Contains(e.Message, "s3cr3t") {
		t.Errorf("the message: %s", e.Message)
	}
	if e := out.Check("slack", "https://evil.example/x?token=s3cr3t").(*fado.Error); !strings.Contains(e.Message, "evil.example is not one of the connection slack") || strings.Contains(e.Message, "s3cr3t") {
		t.Errorf("the message: %s", e.Message)
	}
	// Nothing allowed, nothing sent.
	none, _ := Allow(Options{})
	if got := refusal(none.Check("", "https://hooks.partner.example/in")); got != "host_not_allowed" {
		t.Errorf("a sender with no host listed: %q", got)
	}
}

// local is a test server and the entry that lists it by its own address.
func local(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, "http://" + srv.Listener.Addr().String()
}

func TestSend(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	var bodies []string
	srv, entry := local(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen, bodies = append(seen, r), append(bodies, string(raw))
		mu.Unlock()
		switch r.URL.Path {
		case "/busy":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/later":
			w.Header().Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/moved":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/big":
			w.Write([]byte(strings.Repeat("x", 2000)))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case "/drip":
			// The headers at once, and the body never.
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"ok": true}`))
		}
	})
	out, err := Allow(Options{Hosts: []string{entry}, Timeout: 300 * time.Millisecond, MaxReply: 1000})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reply, err := out.Send(ctx, &fado.OutboundRequest{Method: "PUT", URL: srv.URL + "/things/7?x=a%20b", Body: []byte(`{"n": 1}`), Headers: []fado.Header{
		{Name: "Content-Type", Value: "application/json"}, {Name: "x-kind", Value: "invoice"}, {Name: "User-Agent", Value: "FadoDB"},
		{Name: "Idempotency-Key", Value: "E1:0"}, {Name: "FadoDB-Event", Value: "E1"}, {Name: "FadoDB-Attempt", Value: "2"}}})
	if err != nil || reply.Status != 201 || string(reply.Body) != `{"ok": true}` || reply.RetryAfter != 0 {
		t.Fatalf("the reply: %+v, %v", reply, err)
	}
	r := seen[0]
	if r.Method != "PUT" || r.URL.Path != "/things/7" || r.URL.RawQuery != "x=a%20b" || bodies[0] != `{"n": 1}` || r.Header.Get("Content-Type") != "application/json" ||
		r.Header.Get("X-Kind") != "invoice" || r.Header.Get("User-Agent") != "FadoDB" || r.Header.Get("Idempotency-Key") != "E1:0" || r.Header.Get("FadoDB-Event") != "E1" || r.Header.Get("FadoDB-Attempt") != "2" {
		t.Errorf("the request as the receiver saw it: %s %s %v %q", r.Method, r.URL, r.Header, bodies[0])
	}
	// A request without a body sends none.
	if reply, err := out.Send(ctx, &fado.OutboundRequest{Method: "GET", URL: srv.URL + "/things"}); err != nil || reply.Status != 201 || bodies[1] != "" || seen[1].ContentLength != 0 {
		t.Errorf("a GET: %+v, %v", reply, err)
	}
	// Retry-After, in seconds and as a date.
	if reply, err := out.Send(ctx, &fado.OutboundRequest{Method: "POST", URL: srv.URL + "/busy"}); err != nil || reply.Status != 429 || reply.RetryAfter != 7*time.Second {
		t.Errorf("a reply that says when: %+v, %v", reply, err)
	}
	if reply, err := out.Send(ctx, &fado.OutboundRequest{Method: "POST", URL: srv.URL + "/later"}); err != nil || reply.Status != 503 || reply.RetryAfter < 59*time.Minute || reply.RetryAfter > time.Hour {
		t.Errorf("a reply that says when, as a date: %+v, %v", reply, err)
	}
	// A redirect is the reply: nothing follows it, wherever it points.
	if reply, err := out.Send(ctx, &fado.OutboundRequest{Method: "POST", URL: srv.URL + "/moved"}); err != nil || reply.Status != 302 {
		t.Errorf("a redirect: %+v, %v", reply, err)
	}
	// A reply longer than the program reads is refused.
	if _, err := out.Send(ctx, &fado.OutboundRequest{Method: "GET", URL: srv.URL + "/big"}); refusal(err) != "reply_too_large" {
		t.Errorf("a long reply: %v", err)
	}
	// No reply in time is an error that is no refusal, and says nothing
	// of the address.
	before := time.Now()
	_, err = out.Send(ctx, &fado.OutboundRequest{Method: "GET", URL: srv.URL + "/slow?token=s3cr3t"})
	if err == nil || refusal(err) != "not a refusal: "+err.Error() || strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "/slow") || time.Since(before) > 3*time.Second {
		t.Errorf("a receiver that does not answer: %v after %s", err, time.Since(before))
	}
	// The limit is on the whole call: a receiver that starts to answer
	// and does not finish got no reply through either.
	before = time.Now()
	if _, err := out.Send(ctx, &fado.OutboundRequest{Method: "GET", URL: srv.URL + "/drip"}); err == nil || strings.HasPrefix(refusal(err), "reply") || time.Since(before) > 3*time.Second {
		t.Errorf("a receiver that does not finish its reply: %v after %s", err, time.Since(before))
	}
	// Nobody listening: no reply either, and the address stays out of it.
	gone := srv.Listener.Addr().String()
	srv.Close()
	_, err = out.Send(ctx, &fado.OutboundRequest{Method: "GET", URL: "http://" + gone + "/x?token=s3cr3t"})
	if err == nil || strings.HasPrefix(refusal(err), "host") || strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("a receiver that is gone: %v", err)
	}
	// A host that is not listed is not called at all.
	if _, err := out.Send(ctx, &fado.OutboundRequest{Method: "GET", URL: "http://127.0.0.1:1/x"}); refusal(err) != "host_not_allowed" {
		t.Errorf("a host that is not listed: %v", err)
	}
	// A caller that gives up ends the call.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := out.Send(cctx, &fado.OutboundRequest{Method: "GET", URL: "http://" + gone + "/x"}); err == nil {
		t.Error("a call whose caller gave up returned a reply")
	}
}

// A host that only a suffix admits must be public: a name under an
// allowed domain that points inside the network is not called. A host
// listed by its own name may be inside.
func TestSend_ASuffixAdmitsPublicAddressesOnly(t *testing.T) {
	var arrived atomic.Int32
	srv, _ := local(t, func(w http.ResponseWriter, r *http.Request) { arrived.Add(1) })
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	c, err := newClient(Options{
		Hosts:       []string{"http://.partner.test:" + port, "http://named.partner.test:" + port},
		Connections: map[string][]string{"inside": {"http://metadata.partner.test:" + port}, "wide": {"http://.partner.test:" + port}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Every name of the test resolves to the test server, which is on the
	// loopback: an address inside the network.
	c.lookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch {
		case host == "nowhere.partner.test":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		case strings.HasSuffix(host, ".partner.test"):
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return nil, fmt.Errorf("the test resolves no %s", host)
	}
	ctx := context.Background()
	send := func(connection, host string) error {
		_, err := c.Send(ctx, &fado.OutboundRequest{Method: "POST", URL: "http://" + host + ":" + port + "/x?token=s3cr3t", Connection: connection})
		return err
	}
	if err := send("", "api.partner.test"); refusal(err) != "host_not_allowed" || !strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "s3cr3t") || arrived.Load() != 0 {
		t.Errorf("a host under an allowed suffix that points inside: %v, %d requests arrived", err, arrived.Load())
	}
	if err := send("wide", "metadata.partner.test"); refusal(err) != "host_not_allowed" || arrived.Load() != 0 {
		t.Errorf("the same over a connection: %v", err)
	}
	// Listed by its own name, it is called.
	if err := send("", "named.partner.test"); err != nil || arrived.Load() != 1 {
		t.Errorf("a host listed by its own name: %v, %d requests arrived", err, arrived.Load())
	}
	if err := send("inside", "metadata.partner.test"); err != nil || arrived.Load() != 2 {
		t.Errorf("a host of a connection, by its own name: %v", err)
	}
	// A name that does not resolve is no reply, not a refusal.
	if err := send("", "nowhere.partner.test"); err == nil || strings.HasPrefix(refusal(err), "host") {
		t.Errorf("a host that does not resolve: %v", err)
	}
	for ip, public := range map[string]bool{
		"127.0.0.1": false, "10.0.0.8": false, "172.16.3.4": false, "192.168.1.1": false, "169.254.169.254": false, "100.64.0.1": false, "100.127.255.255": false,
		"0.0.0.0": false, "224.0.0.1": false, "::1": false, "fe80::1": false, "fc00::1": false, "::ffff:10.0.0.1": false, "::": false,
		"8.8.8.8": true, "100.63.255.255": true, "100.128.0.1": true, "172.32.0.1": true, "2606:4700::1111": true, "::ffff:8.8.8.8": true,
	} {
		if isPublic(net.ParseIP(ip)) != public {
			t.Errorf("%s public: want %v", ip, public)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{
		"": 0, "0": 0, "5": 5 * time.Second, " 120 ": 2 * time.Minute, "-3": 0, "soon": 0, "1.5": 0,
		"Sun, 01 Mar 2026 12:00:30 GMT": 30 * time.Second, "Sun, 01 Mar 2026 11:00:00 GMT": 0,
		"99999999999": 366 * 24 * time.Hour,
	} {
		if got := retryAfter(in, now); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

// A database that is allowed to send (ADR-0045, items 3 to 7), end to
// end, with the worker running: a rule's action reaches a receiver in
// another program over HTTP, signed; the receiver checks the signature,
// makes twice once by the key and answers; a busy receiver is asked again
// and a secret is asked of the host at every attempt.
func TestDatabaseSendsARequest(t *testing.T) {
	const key = "whsec-test-shared"
	var mu sync.Mutex
	seen := map[string]int{}
	var calls []fado.ActionCall
	var failures []string
	srv, entry := local(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if err := fado.VerifySignature(key, r.Header.Get("FadoDB-Signature"), body, time.Now(), time.Minute); err != nil {
			failures = append(failures, err.Error())
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var call fado.ActionCall
		event := ""
		if err := json.Unmarshal(body, &call); err == nil && call.Event != nil {
			event = call.Event.ID
		}
		// The headers say what the body says; a trial has no event.
		if err := json.Unmarshal(body, &call); err != nil || call.Key != r.Header.Get("Idempotency-Key") || event != r.Header.Get("FadoDB-Event") || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") {
			failures = append(failures, fmt.Sprintf("the call %s with the headers %v: %v", body, r.Header, err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// The first call of every key finds the receiver busy.
		if seen[call.Key]++; seen[call.Key] == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		calls = append(calls, call)
		w.Write([]byte(`{"entry": "E-` + call.Args["number"].(string) + `"}`))
	})
	out, err := Allow(Options{Connections: map[string][]string{"books": {entry}}})
	if err != nil {
		t.Fatal(err)
	}
	asked := 0
	db, err := fado.Open(filepath.Join(t.TempDir(), "send.fado"), fado.Options{
		Outbound: out,
		Secrets: func(_ context.Context, connection, name string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			switch connection + "/" + name {
			case "books/signing_key":
				return key, nil
			case "books/token":
				// A token that is renewed: another one at every attempt.
				asked++
				return fmt.Sprint("tok-", asked), nil
			}
			return "", errors.New("no such secret")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineAction(ctx, []byte(`{"name": "books.post", "params": {"number": {"type": "text", "required": true}}, "returns": {"entry": "text"},
 "connection": {"name": "books", "secrets": ["token", "signing_key"]},
 "request": {"url": "\"`+srv.URL+`/hooks/fadodb\"", "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"}, "sign": "signing_key"},
 "result": {"entry": "JSONVALUE(BODY, \"entry\")"}}`), setup); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, []byte(`{"name": "Invoice", "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}, "variables": {"number": {"type": "text"}},
 "states": {"initial": "draft", "list": ["draft", "approved"]}, "transitions": [{"name": "approve", "from": "draft", "to": "approved"}],
 "rules": [{"on": "enter", "state": "approved", "do": [{"action": "books.post", "args": {"number": "number"}}]}]}`), setup); err != nil {
		t.Fatal(err)
	}
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "INV-7"}, fado.As(fado.Principal{ID: "ann"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Invoice", doc.ID, "approve", nil, fado.As(fado.Principal{ID: "ann"})); err != nil {
		t.Fatal(err)
	}
	// The worker sends, is told to come back, and a second later does.
	deadline := time.Now().Add(15 * time.Second)
	var row fado.QueuedAction
	for {
		rows, err := db.Outbox(ctx, fado.OutboxFilter{DocID: doc.ID}, setup)
		if err != nil || len(rows) != 1 {
			t.Fatalf("the outbox: %v, %v", rows, err)
		}
		if row = rows[0]; row.Status != "pending" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker left the action at %+v; the receiver said %v", row, failures)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if row.Status != "done" || row.Attempts != 2 || len(failures) != 0 || len(calls) != 1 || asked != 2 || !strings.HasPrefix(calls[0].Key, calls[0].Event.ID) {
		t.Fatalf("the row %+v, the receiver's complaints %v, %d calls taken, %d tokens asked", row, failures, len(calls), asked)
	}
	if call := calls[0]; call.Attempt != 2 || call.Action != "books.post" || call.Event.Kind != "transition" || call.Event.ActorID != "ann" || call.Event.Document.State != "approved" || call.Event.Document.Values["number"] != "INV-7" {
		t.Errorf("the call the receiver took: %+v", call)
	}
	// A trial reaches the receiver too, and answers what result reads.
	seen = map[string]int{}
	mu.Unlock()
	first, err1 := db.TryAction(ctx, "books.post", fado.Values{"number": "INV-8"}, setup)
	mu.Lock()
	if err1 != nil || first.OK || first.Code != "try_later" || !first.Retry || len(failures) != 0 || asked != 3 {
		t.Errorf("a trial the receiver was busy for: %+v, %v; the receiver said %v", first, err1, failures)
	}
}

// TestExampleActions keeps examples/actions honest: every sample
// definition is defined against the engine as it is and sent to a local
// receiver, so a change that breaks a sample fails here before a reader
// meets it.
func TestExampleActions(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "actions", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sample actions under examples/actions: %v", err)
	}
	tested := map[string]func(*testing.T, []byte){"webhook.json": exampleWebhook}
	for _, file := range files {
		run, ok := tested[filepath.Base(file)]
		if !ok {
			t.Errorf("%s has no test: add one to TestExampleActions", filepath.Base(file))
			continue
		}
		def, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.Base(file), func(t *testing.T) { run(t, def) })
	}
}

// The generic webhook: one definition for any receiver. The address and
// the signing key are the host program's, the request carries the signed
// call, and the rule keeps what it wants of the answer.
func exampleWebhook(t *testing.T, def []byte) {
	const key = "whsec-example"
	var mu sync.Mutex
	var got []fado.ActionCall
	srv, entry := local(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := fado.VerifySignature(key, r.Header.Get("FadoDB-Signature"), body, time.Now(), time.Minute); err != nil || r.URL.Path != "/hooks/in/s3cr3t-path" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var call fado.ActionCall
		_ = json.Unmarshal(body, &call)
		mu.Lock()
		got = append(got, call)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status": "success", "id": "evt_1"}`))
	})
	out, err := Allow(Options{Connections: map[string][]string{"webhook": {entry}}})
	if err != nil {
		t.Fatal(err)
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "webhook.fado"), fado.Options{
		Outbox: fado.OutboxOptions{Manual: true}, Outbound: out,
		Secrets: func(_ context.Context, connection, name string) (string, error) {
			return map[string]string{"webhook/url": srv.URL + "/hooks/in/s3cr3t-path", "webhook/signing_key": key}[connection+"/"+name], nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	info, err := db.DefineAction(ctx, def, setup)
	if err != nil {
		t.Fatalf("the sample does not define: %v", err)
	}
	if info.Name != "webhook.send" || info.Kind != "declared" || info.Connection != "webhook" || !info.Available || len(info.Secrets) != 2 {
		t.Errorf("the sample as the catalogue lists it: %+v", info)
	}
	if _, err := db.DefineClass(ctx, []byte(`{"name": "Invoice","access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"},
 "variables": {"number": {"type": "text"}, "sent": {"type": "bool"}, "send_error": {"type": "text"}, "receipt": {"type": "text"}},
 "states": {"initial": "draft", "list": ["draft", "approved", "announced"]},
 "transitions": [{"name": "approve", "from": "draft", "to": "approved"}, {"from": "approved", "to": "announced", "guard": "sent"}],
 "rules": [{"on": "enter", "state": "approved",
            "do": [{"action": "webhook.send", "args": {"topic": "\"invoice.approved\"", "data": {"number": "number"}},
                    "into": {"sent": "OK", "send_error": "ERROR", "receipt": "JSONVALUE(reply, \"id\")"}}]}]}`), setup); err != nil {
		t.Fatal(err)
	}
	// A preview shows the request and keeps the address to itself.
	pv, err := db.PreviewAction(ctx, "webhook.send", fado.Values{"topic": "invoice.approved"}, setup)
	if err != nil || pv.Request.URL != "***" || pv.Request.Method != "POST" || strings.Contains(fmt.Sprint(pv), "s3cr3t") {
		t.Errorf("a preview of the sample: %+v, %v", pv, err)
	}
	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "INV-7"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Transition(ctx, "Invoice", doc.ID, "approve", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Drain(ctx, setup); err != nil || n != 1 {
		t.Fatalf("drain: %d, %v", n, err)
	}
	// The receiver took the signed call, and the invoice went on from its
	// answer.
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Action != "webhook.send" || got[0].Args["topic"] != "invoice.approved" || got[0].Event.Document.Values["number"] != "INV-7" {
		t.Fatalf("what the receiver took: %+v", got)
	}
	after, _ := db.Get(ctx, "Invoice", doc.ID)
	if after.State != "announced" || after.Values["sent"] != true || after.Values["send_error"] != nil || after.Values["receipt"] != "evt_1" {
		t.Errorf("the invoice after the answer: %+v", after)
	}
	rows, _ := db.Outbox(ctx, fado.OutboxFilter{}, setup)
	if len(rows) != 1 || rows[0].Status != "done" || strings.Contains(fmt.Sprint(rows), "s3cr3t") {
		t.Errorf("the row: %+v", rows)
	}
}

// TestActionsGuide keeps docs/actions.md honest: every definition, class
// and rule the guide shows is taken by the engine as it is, under the
// hosts the guide's own program allows.
func TestActionsGuide(t *testing.T) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "docs", "actions.md"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Allow(Options{
		Hosts:       []string{"hooks.partner.example"},
		Connections: map[string][]string{"tickets": {"api.example.com"}, "rates": {"rates.example.com"}, "sms": {"sms.example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := fado.Open(filepath.Join(t.TempDir(), "guide.fado"), fado.Options{
		Outbox: fado.OutboxOptions{Manual: true}, Outbound: out,
		Secrets: func(context.Context, string, string) (string, error) { return "a-secret", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var definitions, classes, fragments int
	blocks := strings.Split(strings.ReplaceAll(string(guide), "\r\n", "\n"), "```json\n")[1:]
	for i, block := range blocks {
		src, _, _ := strings.Cut(block, "\n```")
		var head map[string]json.RawMessage
		if err := json.Unmarshal([]byte(src), &head); err != nil {
			t.Errorf("block %d of the guide is not JSON: %v\n%s", i, err, src)
			continue
		}
		switch {
		case head["variables"] != nil:
			classes++
			if _, err := db.DefineClass(ctx, []byte(src), setup); err != nil {
				t.Errorf("the class of block %d does not define: %v", i, err)
			}
		case head["action"] != nil:
			// One action of a rule: it stands in a class of its own.
			fragments++
			class := `{"name": "Fragment", "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}, "variables": {"subject": {"type": "text"}, "ticket_error": {"type": "text"}},
 "states": {"initial": "a", "list": ["a", "b"]}, "transitions": [{"name": "go", "from": "a", "to": "b"}],
 "rules": [{"on": "enter", "state": "b", "do": [` + src + `]}]}`
			if diags := db.ValidateClass([]byte(class)); len(diags) > 0 {
				t.Errorf("the action of block %d is refused: %v", i, diags)
			}
		default:
			definitions++
			if _, err := db.DefineAction(ctx, []byte(src), setup); err != nil {
				t.Errorf("the definition of block %d does not define: %v", i, err)
			}
		}
	}
	if definitions != 3 || classes != 1 || fragments != 1 {
		t.Errorf("the guide has %d definitions, %d classes and %d actions; the test expects 3, 1 and 1", definitions, classes, fragments)
	}
	// The request of the guide's first definition is the one it describes.
	pv, err := db.PreviewAction(ctx, "tickets.create", fado.Values{"subject": "printer"}, setup)
	if err != nil || pv.Request.Method != "POST" || pv.Request.URL != "https://api.example.com/v1/tickets" || pv.Request.Body != `{"priority":"normal","subject":"printer"}` {
		t.Errorf("the request of tickets.create: %+v, %v", pv, err)
	}
}
