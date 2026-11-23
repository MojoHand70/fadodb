package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// acmeOrigin is the address Acme's people and agents reach the server
// at.
const acmeOrigin = "https://fadodb.acme.example"

func atAcme(*http.Request) (string, bool) { return acmeOrigin, true }

// agentsDoor is a door that opens the door for agents.
func agentsDoor(t testing.TB, db Database, o Options) *Door {
	t.Helper()
	o.Agents, o.Version = true, "0.7.0"
	if o.Origin == nil {
		o.Origin = atAcme
	}
	return door(t, db, o)
}

// toAgents posts one message of the protocol to /mcp, with a key in the
// header and the headers of the greeted form, and with the headers it
// is given on top.
func toAgents(d http.Handler, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Authorization", "Bearer fado_ann")
	r.Header.Set("MCP-Protocol-Version", "2025-11-25")
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			r.Header.Del(headers[i])
		} else {
			r.Header.Set(headers[i], headers[i+1])
		}
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// called is the text a tool answered, and whether it is an error.
func called(t *testing.T, w *httptest.ResponseRecorder) (string, bool) {
	t.Helper()
	var m struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || len(m.Result.Content) != 1 {
		t.Fatalf("the answer carries no answer of a tool: %d %s", w.Code, w.Body)
	}
	return m.Result.Content[0].Text, m.Result.IsError
}

// A tool runs its command as whoever the request proves to be, with
// the door's limits and in its log, and is answered in the reply.
func TestAToolRunsAsTheCaller(t *testing.T) {
	var told []Request
	d := agentsDoor(t, engine(echo), Options{Report: func(r Request) { told = append(told, r) }})
	w := toAgents(d, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get","arguments":{"class":"Invoice","id":"FV/2026/11/0042"}}}`)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, %s: %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	text, failed := called(t, w)
	if want := `{"ran":"get","as":"ann","object":{"class":"Invoice","id":"FV/2026/11/0042"}}`; failed || text != want {
		t.Errorf("get answered %q, error %v", text, failed)
	}
	if len(told) != 1 || !told[0].Agents || told[0].Command != "get" || told[0].User != "ann" || told[0].Status != http.StatusOK {
		t.Errorf("the door told of the request: %+v", told)
	}

	// A request that calls no tool is told as mcp.
	told = nil
	toAgents(d, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if len(told) != 1 || !told[0].Agents || told[0].Command != "mcp" {
		t.Errorf("the door told of tools/list: %+v", told)
	}
}

// A command the database refuses is the answer of its tool, and a
// command that runs longer than the door lets one is stopped as at /v1/.
func TestAToolIsHeldAsACommand(t *testing.T) {
	var told []Request
	d := agentsDoor(t, engine(func(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error) {
		if name == "sql" {
			<-ctx.Done()
			return nil, &store.Error{Code: store.CodeStorage, Message: "interrupted"}
		}
		return []byte(`{"error":{"code":"forbidden","message":"ann may not delete an invoice"}}`), &store.Error{Code: store.CodeForbidden}
	}), Options{Limits: Limits{CommandTime: 20 * time.Millisecond}, Report: func(r Request) { told = append(told, r) }})
	w := toAgents(d, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete","arguments":{"class":"Invoice","id":"FV/2026/11/0042"}}}`)
	if text, failed := called(t, w); w.Code != http.StatusOK || !failed || !strings.Contains(text, `"forbidden"`) {
		t.Errorf("a refused delete answered %d %q, error %v", w.Code, text, failed)
	}
	if told[0].Code != store.CodeForbidden {
		t.Errorf("the door told the code %q", told[0].Code)
	}
	w = toAgents(d, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sql","arguments":{"sql":"SELECT 1"}}}`)
	if text, failed := called(t, w); !failed || !strings.Contains(text, `"timeout"`) || told[1].Code != CodeTimeout {
		t.Errorf("a slow sql answered %q, error %v, told %q", text, failed, told[1].Code)
	}
}

// A request that proves nobody, with no key or pass or with a session
// of a browser alone, is refused 401, and told where passes come from
// (ADR-0071, item 8).
func TestTheDoorForAgentsAsksWhoCalls(t *testing.T) {
	d := agentsDoor(t, engine(echo), Options{Who: func(r *http.Request) (store.Principal, error) {
		if r.Header.Get("Authorization") == "Bearer fado_ann" {
			return ann, nil
		}
		if _, err := r.Cookie(SessionCookie); err == nil {
			// The Who of a server whose pages sign people in takes a
			// session; the door for agents never asks it of one.
			return ann, nil
		}
		return store.Principal{}, store.ErrUnknownKey
	}})
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	want := `Bearer resource_metadata="https://fadodb.acme.example/.well-known/oauth-protected-resource/mcp"`
	for what, w := range map[string]*httptest.ResponseRecorder{
		"no credential":  toAgents(d, list, "Authorization", ""),
		"an unknown key": toAgents(d, list, "Authorization", "Bearer fado_nobody"),
		"a session alone": func() *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(list))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: "s"})
			w := httptest.NewRecorder()
			d.ServeHTTP(w, r)
			return w
		}(),
	} {
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != want || refusal(t, w).Code != CodeUnauthenticated {
			t.Errorf("%s: answered %d, WWW-Authenticate %q: %s", what, w.Code, w.Header().Get("WWW-Authenticate"), w.Body)
		}
	}
	// A server that does not know its address names none.
	d = agentsDoor(t, engine(echo), Options{Who: func(*http.Request) (store.Principal, error) { return store.Principal{}, store.ErrUnknownKey },
		Origin: func(*http.Request) (string, bool) { return "", false }})
	if w := toAgents(d, list); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("with no address known: %d, %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
}

// What the door for agents does not take: another method than POST, a
// page of another site, a body that is not said to be JSON, or one
// larger than the door reads.
func TestWhatTheDoorForAgentsDoesNotTake(t *testing.T) {
	d := agentsDoor(t, engine(echo), Options{Limits: Limits{RequestBytes: 1 << 10}})
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		r := httptest.NewRequest(method, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer fado_ann")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
			t.Errorf("%s answered %d, Allow %q", method, w.Code, w.Header().Get("Allow"))
		}
	}
	if w := toAgents(d, list, "Origin", "https://evil.example"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"id":null`) {
		t.Errorf("a page of another site: %d %s", w.Code, w.Body)
	}
	if w := toAgents(d, list, "Origin", acmeOrigin); w.Code != http.StatusOK {
		t.Errorf("a page of the server itself: %d %s", w.Code, w.Body)
	}
	if w := toAgents(d, list, "Content-Type", "text/plain"); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a body not said to be JSON: %d %s", w.Code, w.Body)
	}
	if w := toAgents(d, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sql","arguments":{"sql":"`+strings.Repeat("x", 2<<10)+`"}}}`); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body too large: %d %s", w.Code, w.Body)
	}
	// A notice is taken with 202 and no body.
	if w := toAgents(d, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Errorf("a notice: %d %q", w.Code, w.Body)
	}
}

// A door that was not told to open the door for agents has nothing at
// /mcp, as before.
func TestNoDoorForAgentsUnlessToldTo(t *testing.T) {
	d := door(t, engine(echo), Options{})
	w := toAgents(d, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if w.Code != Status(command.CodeUnknownCommand) || refusal(t, w).Code != command.CodeUnknownCommand {
		t.Errorf("/mcp of a door with none answered %d: %s", w.Code, w.Body)
	}
}
