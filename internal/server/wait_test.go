package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/store"
)

// waiting is an engine whose wait for changes waits until it is
// stopped, as a quiet log does, and then answers no event and its place;
// it says on started when a wait has begun. Every other command is
// echoed.
func waiting(started chan<- string) engine {
	return func(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error) {
		if name != addressWait {
			return echo(ctx, p, name, object)
		}
		started <- p.ID
		<-ctx.Done()
		return []byte(`{"events":[],"after":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE","more":false}`), nil
	}
}

// begun waits for a wait to begin.
func begun(t *testing.T, started <-chan string) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not begin")
	}
}

// A wait holds the place of a stream and not the slot of a command, so
// that agents that wait keep nobody's command waiting; the streams the
// server holds bound how many wait at once (ADR-0064, item 28). When the
// server stops, a wait answers its place.
func TestAWaitHoldsThePlaceOfAStream(t *testing.T) {
	started := make(chan string, 4)
	d := door(t, waiting(started), Options{Limits: Limits{Commands: 1, Streams: 2}})
	var replies sync.WaitGroup
	answers := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		replies.Add(1)
		go func() {
			defer replies.Done()
			answers <- post(d, "/v1/wait_for_changes", `{"seconds": 60}`)
		}()
		begun(t, started)
	}
	if n := d.Streams(); n != 2 {
		t.Errorf("two waits hold %d places of streams", n)
	}
	// The one slot of commands is free.
	if w := post(d, "/v1/get", `{"class": "Invoice", "id": "FV/2026/11/0042"}`); w.Code != http.StatusOK {
		t.Errorf("a command while two agents wait: %d %s", w.Code, w.Body)
	}
	// A third wait finds no place.
	if w := post(d, "/v1/wait_for_changes", `{}`); w.Code != Status(CodeBusy) || refusal(t, w).Code != CodeBusy || !strings.Contains(refusal(t, w).Message, "streams") {
		t.Errorf("a third wait: %d %s", w.Code, w.Body)
	}
	// The server stops: each wait answers what it has, with its place.
	d.EndStreams()
	replies.Wait()
	close(answers)
	for w := range answers {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"after":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE"`) {
			t.Errorf("a wait the server stopped: %d %s", w.Code, w.Body)
		}
	}
	if n := d.Streams(); n != 0 {
		t.Errorf("%d places are held after the waits ended", n)
	}
}

// At the door for agents the tool that waits gives the request's slot
// back for the place of a stream, as at /v1/.
func TestTheToolThatWaitsHoldsThePlaceOfAStream(t *testing.T) {
	started := make(chan string, 2)
	var told []Request
	var mu sync.Mutex
	d := agentsDoor(t, waiting(started), Options{Limits: Limits{Commands: 1, Streams: 1},
		Report: func(r Request) { mu.Lock(); told = append(told, r); mu.Unlock() }})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- toAgents(d, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_for_changes","arguments":{"seconds":60}}}`)
	}()
	begun(t, started)
	if w := toAgents(d, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get","arguments":{"class":"Invoice","id":"FV/2026/11/0042"}}}`); w.Code != http.StatusOK {
		t.Errorf("a tool while the agent waits: %d %s", w.Code, w.Body)
	}
	// The one place of a stream is held: a second wait is refused,
	// as the answer of its tool.
	if text, failed := called(t, toAgents(d, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wait_for_changes","arguments":{}}}`)); !failed || !strings.Contains(text, `"busy"`) {
		t.Errorf("a second wait: %q, error %v", text, failed)
	}
	d.EndStreams()
	w := <-done
	if text, failed := called(t, w); failed || !strings.Contains(text, `"after":"01KA2F8Q7ZJ4M0X3T5V9B1C6DE"`) {
		t.Errorf("the wait the server stopped answered %q, error %v", text, failed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 3 || told[2].Command != "wait_for_changes" || told[2].Status != http.StatusOK {
		t.Errorf("the door told of the requests: %+v", told)
	}
	if n := d.Streams(); n != 0 {
		t.Errorf("%d places are held after the wait ended", n)
	}
}
