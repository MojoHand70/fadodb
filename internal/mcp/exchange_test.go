package mcp

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"
)

// atServer is the door at the server itself: made with no Send, and
// given that of each request.
func atServer(t *testing.T) *Door {
	t.Helper()
	d, err := New(nil, Options{Version: "0.7.0", AtServer: true})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// post is one request of an agent's program, as its headers and body
// came.
type post struct {
	version, method, name string
	body                  string
	caller                string
}

func (p post) to(d *Door, db *database) Answered {
	caller := p.caller
	if caller == "" {
		caller = "ann"
	}
	return d.Exchange(context.Background(), db.send, Posted{Body: []byte(p.body), Version: p.version, Method: p.method, Name: p.name, Caller: caller})
}

// readAnswer reads the one message an answer carries, by the id of its
// request.
func readAnswer(t *testing.T, a Answered) answered {
	t.Helper()
	if strings.Contains(strings.TrimSpace(string(a.Body)), "\n") {
		t.Fatalf("the answer is more than one message:\n%s", a.Body)
	}
	return heard(t, string(a.Body))
}

// The modern form over HTTP: each request says its version in its
// params and in its headers, which also name its method and the tool it
// calls; the answers are those of the door an agent's program starts.
func TestTheModernFormOverHTTP(t *testing.T) {
	db, d := acme(), atServer(t)
	discovered := post{version: Modern, method: "server/discover", body: `{"jsonrpc":"2.0","id":"discover-1","method":"server/discover","params":{` + modern + `}}`}.to(d, db)
	if discovered.Status != http.StatusOK {
		t.Fatalf("server/discover answered %d: %s", discovered.Status, discovered.Body)
	}
	says := readAnswer(t, discovered).result(t, `"discover-1"`)["instructions"].(string)
	if !strings.Contains(says, "sent as whoever each request proves to be: the agent a person connected acts for that person, with that person's groups") ||
		strings.Contains(says, "whose key this door was given") {
		t.Errorf("the door at the server says of itself: %s", says)
	}

	listed := post{version: Modern, method: "tools/list", body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + modern + `}}`}.to(d, db)
	if r := readAnswer(t, listed).result(t, "1"); listed.Status != http.StatusOK || r["resultType"] != "complete" || len(r["tools"].([]any)) == 0 || listed.Tool != "" {
		t.Errorf("tools/list answered %d, tool %q: %s", listed.Status, listed.Tool, listed.Body)
	}

	got := post{version: Modern, method: "tools/call", name: "get",
		body: `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get","arguments":{"class":"Invoice","id":"FV/2026/11/0042"},` + modern + `}}`}.to(d, db)
	if text, failed := readAnswer(t, got).text(t, "2"); got.Status != http.StatusOK || failed || text != db.answers["get"] || got.Tool != "get" {
		t.Errorf("get answered %d, tool %q, %q, error %v", got.Status, got.Tool, text, failed)
	}

	// A refusal is the answer of the tool, as at the door a program
	// starts, and the request is answered 200.
	moved := post{version: Modern, method: "tools/call", name: "transition",
		body: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"transition","arguments":{"class":"Invoice","id":"FV/2026/11/0042","transition":"approve"},` + modern + `}}`}.to(d, db)
	if text, failed := readAnswer(t, moved).text(t, "3"); moved.Status != http.StatusOK || !failed || text != db.answers["transition"] {
		t.Errorf("a refused transition answered %d, %q, error %v", moved.Status, text, failed)
	}

	// A name that is no plain ASCII travels in its header as base64.
	odd := "zapłać"
	named := post{version: Modern, method: "tools/call", name: "=?base64?" + base64.StdEncoding.EncodeToString([]byte(odd)) + "?=",
		body: `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"` + odd + `",` + modern + `}}`}.to(d, db)
	if code, message, _ := readAnswer(t, named).failure(t, "4"); named.Status != http.StatusOK || code != codeInvalidParams || message != "Unknown tool: "+odd {
		t.Errorf("a tool of a name in base64 answered %d: %d %s", named.Status, code, message)
	}

	// A method the door does not have is 404 in this revision.
	none := post{version: Modern, method: "resources/list", body: `{"jsonrpc":"2.0","id":5,"method":"resources/list","params":{` + modern + `}}`}.to(d, db)
	if code, _, _ := readAnswer(t, none).failure(t, "5"); none.Status != http.StatusNotFound || code != codeNoMethod {
		t.Errorf("resources/list answered %d, %d", none.Status, code)
	}
}

// A request of the modern form whose headers are missing or say
// something else than its body is refused 400, -32020; one of a
// revision the door does not speak, 400, -32022.
func TestTheHeadersOfTheModernForm(t *testing.T) {
	db, d := acme(), atServer(t)
	call := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"whoami",` + modern + `}}`
	for _, c := range []struct {
		what string
		p    post
		code int
		says string
	}{
		{"no version header", post{method: "tools/call", name: "whoami", body: call}, codeHeaderMismatch, "the request has no MCP-Protocol-Version header"},
		{"another version in the header", post{version: "2025-11-25", method: "tools/call", name: "whoami", body: call}, codeHeaderMismatch, `MCP-Protocol-Version header value "2025-11-25" does not match body value "2026-07-28"`},
		{"no method header", post{version: Modern, name: "whoami", body: call}, codeHeaderMismatch, "the request has no Mcp-Method header"},
		{"another method in the header", post{version: Modern, method: "tools/list", name: "whoami", body: call}, codeHeaderMismatch, `Mcp-Method header value "tools/list"`},
		{"no name header", post{version: Modern, method: "tools/call", body: call}, codeHeaderMismatch, "the request has no Mcp-Name header"},
		{"another name in the header", post{version: Modern, method: "tools/call", name: "get", body: call}, codeHeaderMismatch, `Mcp-Name header value "get" does not match body value "whoami"`},
		{"a revision the door does not speak", post{version: "2027-01-01", method: "tools/call", name: "whoami",
			body: strings.Replace(call, `"2026-07-28"`, `"2027-01-01"`, 1)}, codeVersion, "Unsupported protocol version"},
	} {
		a := c.p.to(d, db)
		code, message, _ := readAnswer(t, a).failure(t, "7")
		if a.Status != http.StatusBadRequest || code != c.code || !strings.Contains(message, c.says) {
			t.Errorf("%s: answered %d, %d %s", c.what, a.Status, code, message)
		}
	}
	if db.was("whoami {}") {
		t.Error("a refused request reached the database")
	}
}

// The greeted form over HTTP: the greeting is answered and nothing of
// it is kept; each request after it says its revision in its header,
// and one that says none is taken for 2025-03-26.
func TestTheGreetedFormOverHTTP(t *testing.T) {
	db, d := acme(), atServer(t)
	greeting := post{body: `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"claude-ai","version":"0.1.0"}}}`}.to(d, db)
	if r := readAnswer(t, greeting).result(t, "0"); greeting.Status != http.StatusOK || r["protocolVersion"] != "2025-11-25" || r["resultType"] != nil {
		t.Errorf("the greeting answered %d: %s", greeting.Status, greeting.Body)
	}
	if a := (post{body: `{"jsonrpc":"2.0","method":"notifications/initialized"}`}).to(d, db); a.Status != http.StatusAccepted || len(a.Body) != 0 {
		t.Errorf("a notice answered %d: %q", a.Status, a.Body)
	}
	for _, version := range []string{"2025-11-25", "2025-06-18", ""} {
		a := post{version: version, body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami"}}`}.to(d, db)
		if text, failed := readAnswer(t, a).text(t, "1"); a.Status != http.StatusOK || failed || text != db.answers["whoami"] {
			t.Errorf("with the header %q whoami answered %d: %s", version, a.Status, a.Body)
		}
		if r := readAnswer(t, a).result(t, "1"); r["resultType"] != nil || r["_meta"] != nil {
			t.Errorf("an answer of the greeted form carries %v", keysOf(r))
		}
	}
	// A method the door does not have is 200 here: to this form a 404
	// says that its session ended.
	none := post{version: "2025-11-25", body: `{"jsonrpc":"2.0","id":2,"method":"resources/list"}`}.to(d, db)
	if code, _, _ := readAnswer(t, none).failure(t, "2"); none.Status != http.StatusOK || code != codeNoMethod {
		t.Errorf("resources/list answered %d, %d", none.Status, code)
	}
	unknown := post{version: "1999-01-01", body: `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`}.to(d, db)
	if code, _, data := readAnswer(t, unknown).failure(t, "3"); unknown.Status != http.StatusBadRequest || code != codeVersion || data["requested"] != "1999-01-01" {
		t.Errorf("a revision the door does not speak answered %d, %d, %v", unknown.Status, code, data)
	}
	halfModern := post{version: Modern, method: "tools/list", body: `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`}.to(d, db)
	if code, message, _ := readAnswer(t, halfModern).failure(t, "4"); halfModern.Status != http.StatusBadRequest || code != codeInvalidParams || !strings.Contains(message, "in its header and not in params") {
		t.Errorf("a request that says the modern revision in its header alone answered %d, %d %s", halfModern.Status, code, message)
	}
}

// What is no single message of the protocol is refused 400 with no id;
// a notice and an answer to a request are taken with 202 and no body.
func TestWhatTheDoorTakesOverHTTP(t *testing.T) {
	db, d := acme(), atServer(t)
	for body, code := range map[string]int{
		"": codeInvalidRequest,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`:  codeInvalidRequest,
		`{"jsonrpc":"2.0","id":1,`:                    codeParse,
		`{"jsonrpc":"2.0","id":true,"method":"ping"}`: codeInvalidRequest,
	} {
		a := post{version: "2025-11-25", body: body}.to(d, db)
		got, _, _ := readAnswer(t, a).failure(t, "null")
		if a.Status != http.StatusBadRequest || got != code {
			t.Errorf("%q answered %d, %d", body, a.Status, got)
		}
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}`,
		`{"jsonrpc":"2.0","id":5,"result":{}}`,
	} {
		if a := (post{version: "2025-11-25", body: body}).to(d, db); a.Status != http.StatusAccepted || len(a.Body) != 0 {
			t.Errorf("%s answered %d: %q", body, a.Status, a.Body)
		}
	}
}

// A notice that stops a call stops the call of the same caller with
// that id, and no call of anybody else's.
func TestANoticeStopsACallOverHTTP(t *testing.T) {
	db, d := acme(), atServer(t)
	db.waits["query"] = make(chan struct{})
	call := `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"query","arguments":{"class":"Invoice"}}}`
	done := make(chan Answered, 1)
	go func() { done <- post{version: "2025-11-25", body: call}.to(d, db) }()
	// Bob names the same id; his notice stops nothing of Ann's.
	waitFor(t, func() bool { return db.was(`query {"class":"Invoice"}`) })
	post{version: "2025-11-25", caller: "bob", body: `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":42}}`}.to(d, db)
	select {
	case a := <-done:
		t.Fatalf("Bob's notice stopped Ann's call: %s", a.Body)
	case <-time.After(50 * time.Millisecond):
	}
	post{version: "2025-11-25", body: `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":42}}`}.to(d, db)
	select {
	case a := <-done:
		// The request is answered all the same: an HTTP request has an
		// answer, and the agent's program passes over one it stopped.
		if text, failed := readAnswer(t, a).text(t, "42"); !failed || !strings.Contains(text, CodeNoAnswer) {
			t.Errorf("a stopped call answered %q, error %v", text, failed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ann's notice did not stop her call")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.running) != 0 {
		t.Errorf("%d calls are still held as under way", len(d.running))
	}
}

// waitFor waits until a condition holds, for at most five seconds.
func waitFor(t *testing.T, holds func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !holds(); {
		if time.Now().After(end) {
			t.Fatal("waited five seconds in vain")
		}
		time.Sleep(time.Millisecond)
	}
}

// A door made for HTTP alone holds no conversation over a line.
func TestADoorForHTTPAlone(t *testing.T) {
	var out strings.Builder
	if err := atServer(t).Serve(context.Background(), strings.NewReader("{}\n"), &out); err == nil || out.Len() != 0 {
		t.Errorf("Serve of a door with no Send answered %v and wrote %q", err, out.String())
	}
}
