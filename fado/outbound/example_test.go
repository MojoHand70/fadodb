package outbound_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/outbound"
)

// setup is the caller of a host program's own setup code (ADR-0052): it
// is in the system groups that define, operate the outbox and run SQL,
// and in no other, so the rules of a class hold for it as for anybody.
var setup = fado.As(fado.Principal{ID: "setup", Kind: fado.ActorService,
	System: []string{fado.GroupDesigners, fado.GroupAdministrators, fado.GroupAnalysts}})

// An action the engine sends as an HTTP request (ADR-0045): the
// definition is JSON and nothing else, and the receiver is a service in
// any language. The host program says where the database may send, here
// one connection with one host, and supplies the connection's secrets
// when a request is sent. The request carries the call, the arguments
// and the event that fired the rule, signed with a secret the two sides
// share. A definition that would send the connection's secret anywhere
// else is refused.
func Example() {
	// The receiver: it checks that the request came from the database,
	// takes the call and answers.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := fado.VerifySignature("shared-secret", r.Header.Get("FadoDB-Signature"), body, time.Now(), time.Minute); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var call fado.ActionCall
		_ = json.Unmarshal(body, &call)
		why := "in a trial"
		if call.Event != nil {
			why = "after " + call.Event.Kind + " " + call.Event.Name
		}
		fmt.Println("received", call.Action, "for", call.Args["number"], why)
		_, _ = w.Write([]byte(`{"entry": "E-` + strings.TrimPrefix(call.Args["number"].(string), "INV-") + `"}`))
	}))
	defer receiver.Close()

	tmp, err := os.MkdirTemp("", "fado-example")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	// The host program's decision: the connection "books" reaches this one
	// host, and nothing else may be called.
	out, err := outbound.Allow(outbound.Options{
		Connections: map[string][]string{"books": {"http://" + receiver.Listener.Addr().String()}},
	})
	if err != nil {
		panic(err)
	}
	db, err := fado.Open(filepath.Join(tmp, "requests.fado"), fado.Options{
		Outbox:   fado.OutboxOptions{Manual: true},
		Outbound: out,
		Secrets: func(_ context.Context, connection, name string) (string, error) {
			return "shared-secret", nil // a real program asks its vault
		},
	})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.DefineAction(ctx, []byte(`{
	  "name": "books.post",
	  "params": {"number": {"type": "text", "required": true}},
	  "returns": {"entry": "text"},
	  "connection": {"name": "books", "secrets": ["signing_key"]},
	  "request": {"url": "\"`+receiver.URL+`/hooks/fadodb\"", "sign": "signing_key"},
	  "result": {"entry": "JSONVALUE(BODY, \"entry\")"}
	}`), setup); err != nil {
		panic(err)
	}
	// The same secret, pointed at another host: refused when it is defined.
	_, err = db.DefineAction(ctx, []byte(`{
	  "name": "books.leak",
	  "connection": {"name": "books", "secrets": ["signing_key"]},
	  "request": {"url": "\"https://elsewhere.example/collect\"", "sign": "signing_key"}
	}`), setup)

	for _, d := range err.(fado.Diagnostics) {
		fmt.Println(d.Code, d.Path)
	}
	if _, err := db.DefineClass(ctx, []byte(`{
	  "name": "Invoice",
	  "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"},
	  "variables": {"number": {"type": "text"}},
	  "states": {"initial": "draft", "list": ["draft", "approved"]},
	  "transitions": [{"name": "approve", "from": "draft", "to": "approved"}],
	  "rules": [{"on": "enter", "state": "approved",
	             "do": [{"action": "books.post", "args": {"number": "number"}}]}]
	}`), setup); err != nil {
		panic(err)
	}

	// A preview shows the request and sends nothing; the secret is masked.
	pv, err := db.PreviewAction(ctx, "books.post", fado.Values{"number": "INV-1"}, setup)
	if err != nil {
		panic(err)
	}
	for _, h := range pv.Request.Headers {
		if h.Name == "FadoDB-Signature" {
			fmt.Println("preview:", pv.Request.Method, "signed", h.Value[strings.Index(h.Value, "v1="):])
		}
	}

	doc, err := db.Create(ctx, "Invoice", fado.Values{"number": "INV-7"})
	if err != nil {
		panic(err)
	}
	if _, err := db.Transition(ctx, "Invoice", doc.ID, "approve", nil); err != nil {
		panic(err)
	}
	if _, err := db.Drain(ctx, setup); err != nil {
		panic(err)
	}
	events, _ := db.Events(ctx, "Invoice", doc.ID)
	last := events[len(events)-1]
	fmt.Println(last.Kind, last.Name)

	// A trial sends the request for real, outside any document.
	trial, err := db.TryAction(ctx, "books.post", fado.Values{"number": "INV-9"}, setup)
	if err != nil {
		panic(err)
	}
	fmt.Println("trial:", trial.OK, trial.Result["entry"])
	// Output:
	// not_allowed /request/url
	// preview: POST signed v1=***
	// received books.post for INV-7 after transition approve
	// action_run books.post
	// received books.post for INV-9 in a trial
	// trial: true E-9
}

// BenchmarkDeclaredAction measures an action the engine sends as a
// request, end to end against a receiver in the same process (ADR-0045):
// a transition whose rule queues the call, and the drain that builds the
// request from its templates, sends it over the loopback, reads the JSON
// reply and marks the row. The round trip itself is most of it.
func BenchmarkDeclaredAction(b *testing.B) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"ok": true, "ts": "1700000000.000100"}`))
	}))
	defer receiver.Close()
	out, err := outbound.Allow(outbound.Options{Connections: map[string][]string{"slack": {"http://" + receiver.Listener.Addr().String()}}})
	if err != nil {
		b.Fatal(err)
	}
	db, err := fado.Open(filepath.Join(b.TempDir(), "bench.fado"), fado.Options{Outbox: fado.OutboxOptions{Manual: true}, Outbound: out,
		Secrets: func(context.Context, string, string) (string, error) { return "xoxb-bench", nil }})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.DefineAction(ctx, []byte(`{"name": "slack.post", "params": {"channel": {"type": "text", "required": true}, "text": "text"}, "returns": {"ts": "text"},
 "connection": {"name": "slack", "secrets": ["token"]},
 "request": {"url": "\"`+receiver.URL+`/api/chat.postMessage\"", "headers": {"Authorization": "\"Bearer \" & SECRET(\"token\")"}, "body": {"channel": "channel", "text": "text"}},
 "ok": "JSONVALUE(BODY, \"ok\") = \"TRUE\"", "result": {"ts": "JSONVALUE(BODY, \"ts\")"}}`), setup); err != nil {
		b.Fatal(err)
	}
	if _, err := db.DefineClass(ctx, []byte(`{"name": "Job", "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}, "variables": {"note": {"type": "text"}},
 "states": {"initial": "open", "list": ["open"]}, "transitions": [{"name": "tell", "from": "open", "to": "open"}],
 "rules": [{"on": "transition", "name": "tell", "do": [{"action": "slack.post", "args": {"channel": "\"#ops\"", "text": "note"}}]}]}`), setup); err != nil {
		b.Fatal(err)
	}
	doc, err := db.Create(ctx, "Job", fado.Values{"note": "done"})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := db.Transition(ctx, "Job", doc.ID, "tell", nil); err != nil {
			b.Fatal(err)
		}
		if n, err := db.Drain(ctx, setup); err != nil || n != 1 {
			b.Fatalf("drained %d, %v", n, err)
		}
	}
	b.StopTimer()
	rows, err := db.Outbox(ctx, fado.OutboxFilter{Status: "dead"}, setup)
	if err != nil || len(rows) != 0 {
		b.Fatalf("%d actions died: %v", len(rows), err)
	}
}
