package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/mcp"
)

// doorChapter reads the chapter of the manual on the door for agents.
func doorChapter(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "the-door-for-agents.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A checkout on Windows may carry CRLF line endings.
	return strings.ReplaceAll(string(text), "\r\n", "\n")
}

// modernMeta is what a request of the modern revision of the protocol
// carries.
const modernMeta = `{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

// door starts "fadodb mcp" with the flags, gives it the lines as an
// agent's program would and its input's end, and answers how it ended,
// the lines it wrote and what it said on standard error.
func door(flags []string, lines ...string) (code int, wrote []string, said string) {
	var stdout, stderr bytes.Buffer
	code = runMCP(context.Background(), flags, strings.NewReader(strings.Join(lines, "\n")+"\n"), &stdout, &stderr)
	if text := strings.TrimSuffix(stdout.String(), "\n"); text != "" {
		wrote = strings.Split(text, "\n")
	}
	return code, wrote, stderr.String()
}

// called is what an agent has of the call of a tool: the text it
// reads and whether it is marked as an error, or, when the door did
// not take the call, why.
type called struct {
	text    string
	isError bool
	refused string
}

// callTool calls a tool through a door started with the flags. params
// is the name of the tool and its arguments, as the chapter shows a
// call.
func callTool(t *testing.T, flags []string, params string) called {
	t.Helper()
	var p map[string]json.RawMessage
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		t.Fatalf("the chapter shows a call that is not JSON: %v\n%s", err, params)
	}
	p["_meta"] = json.RawMessage(modernMeta)
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": p})
	if err != nil {
		t.Fatal(err)
	}
	code, wrote, said := door(flags, string(line))
	if code != exitDone || said != "" || len(wrote) != 1 {
		t.Fatalf("the door ended with %d, wrote %d lines and said %q", code, len(wrote), said)
	}
	var reply struct {
		Result *struct {
			Content []struct{ Type, Text string }
			IsError bool
		}
		Error *struct{ Message string }
	}
	if err := json.Unmarshal([]byte(wrote[0]), &reply); err != nil {
		t.Fatalf("the door wrote %s: %v", wrote[0], err)
	}
	switch {
	case reply.Error != nil:
		return called{refused: reply.Error.Message}
	case reply.Result == nil || len(reply.Result.Content) != 1 || reply.Result.Content[0].Type != "text":
		t.Fatalf("the door wrote %s", wrote[0])
	}
	return called{text: reply.Result.Content[0].Text, isError: reply.Result.IsError}
}

// asTheAgentReads is the text an agent reads laid out as the chapter shows it,
// with the moments of the chapter.
func asTheAgentReads(t *testing.T, text string) string {
	t.Helper()
	var laid bytes.Buffer
	if err := json.Indent(&laid, []byte(text), "", "  "); err != nil {
		t.Fatalf("a tool answered a text that is not JSON: %v\n%s", err, text)
	}
	return asTheTerminalChapterShows(laid.String())
}

// The chapter of the manual on the door for agents says what the door
// does: every call of it is made through "fadodb mcp" against a server,
// with the key of the user who makes it in the chapter, and what the
// agent reads is compared (ADR-0064).
func TestTheChapterOnTheDoor(t *testing.T) {
	chapter, terminal := doorChapter(t), terminalChapter(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--label", "laptop", "--db", path)
	keys := map[string]string{"olga": strings.TrimSpace(olga)}
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0")
	t.Setenv("FADODB_URL", "http://"+addr)
	if err := os.WriteFile(filepath.Join(dir, "invoice.json"), []byte(blockOf(t, terminal, "invoice.json")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	// types types the lines of a block of a chapter at a terminal, as
	// a user, and answers what each printed.
	types := func(user, from, mark string) (out []string) {
		t.Helper()
		t.Setenv("FADODB_KEY", keys[user])
		for _, line := range strings.Split(blockOf(t, from, mark), "\n") {
			args, _ := typed(t, line)
			code, o, said := tool(args...)
			if code != exitDone {
				t.Fatalf("%s: exit %d: %s", line, code, said)
			}
			out = append(out, o)
		}
		return out
	}
	secret := func(of, printed string) {
		t.Helper()
		var answer struct {
			Key struct{ Secret string }
		}
		if err := json.Unmarshal([]byte(printed), &answer); err != nil || !strings.HasPrefix(answer.Key.Secret, "fado_") {
			t.Fatalf("the key of %s: %v in %s", of, err, printed)
		}
		keys[of] = answer.Key.Secret
	}
	// The story so far is the one of the chapter on the terminal: Olga
	// defines the class and makes Bob and Ann, and Bob files three
	// invoices and submits them.
	types("olga", terminal, "olga: the class")
	made := types("olga", terminal, "olga: the users")
	secret("bob", made[2])
	secret("ann", made[3])
	types("bob", terminal, "bob: three invoices")
	// Olga makes the agent and its key.
	made = types("olga", chapter, "olga: the agent")
	secret("ap-agent", made[1])

	// as is a door started for a user of the chapter: the key is in the
	// environment the agent's program gives it.
	as := func(user string) { t.Helper(); t.Setenv("FADODB_KEY", keys[user]) }
	// reads calls the tool of a block as a user and compares what the
	// agent reads with the block that follows.
	reads := func(user, mark, answer string, isError bool) string {
		t.Helper()
		as(user)
		got := callTool(t, nil, blockOf(t, chapter, mark))
		if got.refused != "" || got.isError != isError {
			t.Fatalf("%s: the door did not take it (%q), or error is %v", mark, got.refused, got.isError)
		}
		if laid, want := asTheAgentReads(t, got.text), blockOf(t, chapter, answer); laid != want {
			t.Errorf("%s: the agent reads\n%s\nand the chapter shows\n%s", mark, laid, want)
		}
		return got.text
	}

	// What the agent's program is told, and what the door is told.
	var told struct {
		Servers map[string]struct {
			Command string
			Args    []string
			Env     map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(blockOf(t, chapter, "the agent's program is told")), &told); err != nil {
		t.Fatal(err)
	}
	acme := told.Servers["acme"]
	if acme.Command != "fadodb" || !reflect.DeepEqual(acme.Args, []string{"mcp"}) || acme.Env["FADODB_URL"] == "" || acme.Env["FADODB_KEY"] == "" || len(acme.Env) != 2 {
		t.Errorf("the chapter tells an agent's program %+v", acme)
	}
	flag := regexp.MustCompile("`(--[a-z-]+)`")
	for _, row := range rowsOf(t, chapter, "what the door is told") {
		for _, cell := range row[1:3] {
			for _, name := range append(flag.FindAllStringSubmatch(cell, -1), regexp.MustCompile("`(FADODB_[A-Z]+)`").FindAllStringSubmatch(cell, -1)...) {
				if !strings.Contains(mcpUsage, name[1]) {
					t.Errorf("the chapter names %s, which the help of the door does not", name[1])
				}
			}
		}
	}

	// The tools: one whole, as the door lists it, and the marks of all.
	as("ap-agent")
	code, wrote, said := door(nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":`+modernMeta+`}}`)
	if code != exitDone || said != "" || len(wrote) != 1 {
		t.Fatalf("the list of the tools: exit %d, %d lines, said %q", code, len(wrote), said)
	}
	if n := len(wrote[0]); n < 69_000 || n > 79_000 {
		t.Errorf("the list of the tools is %d characters, and the chapter says about 74,000", n)
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name        string
				Annotations struct{ ReadOnlyHint, DestructiveHint, OpenWorldHint bool }
				InputSchema struct {
					Properties map[string]struct{ Description string }
				}
			}
		}
	}
	var raw struct {
		Result struct{ Tools []json.RawMessage }
	}
	if err := json.Unmarshal([]byte(wrote[0]), &list); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(wrote[0]), &raw); err != nil {
		t.Fatal(err)
	}
	if len(list.Result.Tools) != len(fado.Commands())-len(rowsOf(t, chapter, "no tool")) {
		t.Errorf("%d tools for %d commands", len(list.Result.Tools), len(fado.Commands()))
	}
	kinds := map[[3]bool][]string{}
	for i, tl := range list.Result.Tools {
		marks := [3]bool{tl.Annotations.ReadOnlyHint, tl.Annotations.DestructiveHint, tl.Annotations.OpenWorldHint}
		kinds[marks] = append(kinds[marks], tl.Name)
		if tl.Name != "transition" {
			continue
		}
		var got, want any
		if err := json.Unmarshal(raw.Result.Tools[i], &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(blockOf(t, chapter, "a tool")), &want); err != nil {
			t.Fatalf("the tool the chapter shows is not JSON: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the door lists transition as\n%s\nand the chapter shows another", raw.Result.Tools[i])
		}
	}
	rows := rowsOf(t, chapter, "the marks")
	if len(rows) != len(kinds) {
		t.Errorf("the chapter shows %d kinds of marks, and the tools have %d: %v", len(rows), len(kinds), kinds)
	}
	named := regexp.MustCompile("`([a-z_]+)`")
	for _, row := range rows {
		marks := [3]bool{row[2] == "true", row[3] == "true", row[4] == "true"}
		if strconv.Itoa(len(kinds[marks])) != row[1] {
			t.Errorf("the chapter counts %s tools that %s, and the door lists %d: %v", row[1], row[0], len(kinds[marks]), kinds[marks])
		}
		if names := named.FindAllStringSubmatch(row[0], -1); len(names) > 0 {
			var want []string
			for _, n := range names {
				want = append(want, n[1])
			}
			if !reflect.DeepEqual(kinds[marks], want) {
				t.Errorf("the chapter names %v where the door lists %v", want, kinds[marks])
			}
		}
	}

	// The tools the chapter says take a key are the ones that list one,
	// and the door says the same of itself.
	var keyed, shown []string
	for _, tl := range list.Result.Tools {
		if strings.HasPrefix(tl.InputSchema.Properties["key"].Description, "the idempotency key") {
			keyed = append(keyed, tl.Name)
		}
	}
	after := rowsOf(t, chapter, "after no answer")
	if len(after) != 3 {
		t.Fatalf("the chapter says what follows no answer in %d rows", len(after))
	}
	for _, n := range named.FindAllStringSubmatch(after[1][0], -1) {
		if n[1] != "key" {
			shown = append(shown, n[1])
		}
	}
	if !reflect.DeepEqual(keyed, shown) {
		t.Errorf("the chapter says %v take a key, and the tools that list one are %v", shown, keyed)
	}
	_, discovered, _ := door(nil, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":`+modernMeta+`}}`)
	if len(discovered) != 1 || !strings.Contains(discovered[0], "makes no second commit: "+strings.Join(keyed, ", ")+". No other tool takes one.") {
		t.Fatalf("the door says of the keys: %v", discovered)
	}

	// The sign above the door is the one the chapter shows, word for
	// word, with the chapter's server where this test's is; and the
	// tools the chapter says tell an agent to look first are the ones
	// that do.
	var itself struct {
		Result struct{ Instructions string }
	}
	if err := json.Unmarshal([]byte(discovered[0]), &itself); err != nil {
		t.Fatal(err)
	}
	sign := strings.ReplaceAll(strings.TrimSpace(blockOf(t, chapter, "the sign")), "\n", " ")
	if got := strings.ReplaceAll(itself.Result.Instructions, os.Getenv("FADODB_URL"), "https://fado.acme.example"); got != sign {
		t.Errorf("the door says of itself\n%s\nand the chapter shows\n%s", got, sign)
	}
	var look, lookShown []string
	for i, tl := range list.Result.Tools {
		var described struct{ Description string }
		if err := json.Unmarshal(raw.Result.Tools[i], &described); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(described.Description, "Look before you build") {
			look = append(look, tl.Name)
		}
	}
	_, after4, _ := strings.Cut(chapter, "- **The five tools that build say it again**")
	item, _, _ := strings.Cut(after4, "\n- **")
	for _, n := range named.FindAllStringSubmatch(item, -1) {
		lookShown = append(lookShown, n[1])
	}
	sort.Strings(look)
	sort.Strings(lookShown)
	if len(look) != 5 || !reflect.DeepEqual(look, lookShown) {
		t.Errorf("the chapter says %v tell an agent to look first, and the tools that do are %v", lookShown, look)
	}

	// What an agent reads: who it is, the invoices that wait, and the
	// approval, which sent again with its key is answered as the first
	// time and approves nothing twice.
	reads("ap-agent", "ap-agent: whoami", "ap-agent: whoami, the answer", false)
	reads("ap-agent", "ap-agent: query", "ap-agent: query, the answer", false)
	first := reads("ap-agent", "ap-agent: transition", "ap-agent: transition, the answer", false)
	if again := reads("ap-agent", "ap-agent: transition", "ap-agent: transition, the answer", false); again != first {
		t.Errorf("the approval sent again with its key is answered\n%s\nand the first time\n%s", again, first)
	}

	// Bob's assistant is refused, in the database's words.
	reads("bob", "bob: transition", "bob: transition, the refusal", true)

	// No answer: a door told of a server that is not there.
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nowhere := gone.Addr().String()
	gone.Close()
	type refusal struct {
		Error struct{ Code, Message, Hint, Path string }
	}
	var got, want refusal
	none := callTool(t, []string{"--url", "http://" + nowhere}, blockOf(t, chapter, "bob: transition"))
	if err := json.Unmarshal([]byte(none.text), &got); err != nil || !none.isError {
		t.Fatalf("no answer reached the agent as %q, error %v: %v", none.text, none.isError, err)
	}
	if err := json.Unmarshal([]byte(blockOf(t, chapter, "no answer")), &want); err != nil {
		t.Fatal(err)
	}
	// What the machine says of a server that is not there is the
	// machine's; the door's words end before it.
	upTo := func(message string) string {
		before, _, _ := strings.Cut(strings.ReplaceAll(message, nowhere, "127.0.0.1:8080"), "was not reached: ")
		return before
	}
	if got.Error.Code != mcp.CodeNoAnswer || got.Error.Code != want.Error.Code || got.Error.Hint != want.Error.Hint || upTo(got.Error.Message) != upTo(want.Error.Message) || !strings.Contains(got.Error.Message, "was not reached: ") {
		t.Errorf("no answer reached the agent as\n%+v\nand the chapter shows\n%+v", got.Error, want.Error)
	}

	// Tools kept from an agent: Ann's assistant only reads.
	as("ann")
	kept := callTool(t, []string{"--read-only"}, blockOf(t, chapter, "ann: read only"))
	if want := blockOf(t, chapter, "ann: read only, the answer"); kept.refused != want {
		t.Errorf("a door that only reads answers %q, and the chapter shows %q", kept.refused, want)
	}
	code, wrote, _ = door([]string{"--tools", "get,query,transitions,transition"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":`+modernMeta+`}}`)
	if err := json.Unmarshal([]byte(strings.Join(wrote, "")), &list); err != nil || code != exitDone || len(list.Result.Tools) != 4 {
		t.Errorf("a door of four tools: exit %d, %d tools, %v", code, len(list.Result.Tools), err)
	}

	// What is no tool, and why.
	for _, row := range rowsOf(t, chapter, "no tool") {
		name := strings.Trim(row[0], "`")
		if why := mcp.Withheld(name); why == "" || why != row[1] {
			t.Errorf("the chapter says %s is no tool because %q, and the door %q", name, row[1], why)
		}
	}

	// The door by hand: a line in, a line out.
	typedLine := blockOf(t, chapter, "by hand")
	message, found := strings.CutPrefix(typedLine, "echo '")
	message, piped := strings.CutSuffix(message, "' | fadodb mcp")
	if !found || !piped {
		t.Fatalf("the chapter has a line the test cannot type: %s", typedLine)
	}
	code, wrote, said = door(nil, message)
	if want := blockOf(t, chapter, "by hand, the answer"); code != exitDone || said != "" || len(wrote) != 1 || wrote[0] != want {
		t.Errorf("by hand the door ended with %d, said %q and wrote\n%s\nand the chapter shows\n%s", code, said, strings.Join(wrote, "\n"), want)
	}

	// The revisions of the protocol.
	var revisions []string
	for _, row := range rowsOf(t, chapter, "the revisions") {
		revisions = append(revisions, row[0])
	}
	if !reflect.DeepEqual(revisions, mcp.Versions()) {
		t.Errorf("the chapter lists the revisions %v, and the door speaks %v", revisions, mcp.Versions())
	}
}

// broken is where a door writes when the agent's program is gone.
type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, errors.New("the pipe is closed") }

// How the door ends: with 2 when it could not start, having written
// nothing to standard output, which is the agent's program's; and with
// 3 when an answer could not be written.
func TestHowTheDoorEnds(t *testing.T) {
	t.Setenv("FADODB_URL", "http://127.0.0.1:8080")
	for _, c := range []struct {
		name  string
		key   string
		flags []string
		says  string
	}{
		{"no key", "", nil, "say whose key the command carries: set FADODB_KEY, or give --key-file FILE"},
		{"a tool that is no command", "fado_0123456789", []string{"--tools", "get,approve"}, `no command is named "approve"`},
		{"a tool the door withholds", "fado_0123456789", []string{"--tools", "create_key"}, "create_key is offered as no tool"},
		{"a tool that writes at a door that reads", "fado_0123456789", []string{"--read-only", "--tools", "delete"}, "delete does not only read"},
		{"an argument", "fado_0123456789", []string{"serve"}, "the door takes flags and no other argument"},
		{"a key that would travel in the clear", "fado_0123456789", []string{"--url", "http://fado.acme.example"}, "http://"},
	} {
		t.Setenv("FADODB_KEY", c.key)
		code, wrote, said := door(c.flags, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if code != exitUsage || len(wrote) != 0 || !strings.HasPrefix(said, "fadodb mcp: ") || !strings.Contains(said, c.says) {
			t.Errorf("%s: exit %d, wrote %v, said %q", c.name, code, wrote, said)
		}
	}

	t.Setenv("FADODB_KEY", "fado_0123456789")
	var stderr bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"_meta":` + modernMeta + `}}` + "\n")
	if code := runMCP(context.Background(), nil, in, broken{}, &stderr); code != exitNoAnswer || !strings.Contains(stderr.String(), "an answer could not be written to the agent's program: the pipe is closed") {
		t.Errorf("a door that cannot write: exit %d, said %q", code, stderr.String())
	}

	// The help is for a person, and names the three commands that are no
	// tool.
	code, out, said := tool("mcp", "--help")
	if code != 0 || out != "" || !strings.Contains(said, "fadodb mcp - the door for agents") || !strings.Contains(said, "subscribe") || !strings.Contains(said, "create_key") || !strings.Contains(said, "create_password_link") {
		t.Errorf("the help of the door: exit %d, printed %q, said %q", code, out, said)
	}
}

// The door at the server itself, played against a server: Olga's key
// calls a tool at /mcp, and every row of the chapter's table of what is
// refused there, a pass for the commands among them.
func TestTheChapterOnTheDoorAtTheServer(t *testing.T) {
	chapter := doorChapter(t)
	path := filepath.Join(t.TempDir(), "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, key, _ := tool("key", "new", "olga", "--db", path)
	key = strings.TrimSpace(key)
	addr, said, _ := serving(t, "--listen", "127.0.0.1:0", path)
	base := "http://" + addr
	web := &http.Client{Timeout: 10 * time.Second}
	if !strings.Contains(said.String(), "agents connect at "+base+"/mcp") {
		t.Errorf("the server does not say where agents connect:\n%s", said)
	}

	// toDoor posts one message to the door, with Olga's key unless the
	// headers say otherwise, and answers the reply.
	toDoor := func(method, body string, headers ...string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, base+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		for i := 0; i+1 < len(headers); i += 2 {
			if headers[i+1] == "" {
				req.Header.Del(headers[i])
			} else {
				req.Header.Set(headers[i], headers[i+1])
			}
		}
		resp, err := web.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		text, _ := io.ReadAll(resp.Body)
		return resp, string(text)
	}

	// The modern form, with its headers, and the greeted one, with its
	// revision in the header alone.
	resp, body := toDoor("POST", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"whoami","_meta":`+modernMeta+`}}`,
		"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", "whoami")
	if resp.StatusCode != 200 || !strings.Contains(body, `\"id\":\"olga\"`) || !strings.Contains(body, `"resultType":"complete"`) {
		t.Errorf("whoami in the modern form: %d %s", resp.StatusCode, body)
	}
	resp, body = toDoor("POST", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "MCP-Protocol-Version", "2025-11-25")
	if resp.StatusCode != 200 || !strings.Contains(body, `"name":"whoami"`) || strings.Contains(body, `"name":"create_key"`) {
		t.Errorf("tools/list in the greeted form: %d %.300s", resp.StatusCode, body)
	}

	// A pass for the commands, which FadoFace is given for Ann.
	if code, body := ask(t, web, "POST", base+"/v1/create_user", key, `{"id": "ann", "kind": "person", "groups": ["finance"]}`); code != 200 {
		t.Fatalf("create_user: %d %s", code, body)
	}
	_, linked := ask(t, web, "POST", base+"/v1/create_password_link", key, `{"user": "ann"}`)
	var made struct {
		Link fado.PasswordLink `json:"link"`
	}
	json.Unmarshal([]byte(linked), &made)
	ann := newBrowser(t, base)
	ann.post("/auth/password", url.Values{"link": {made.Link.Token}, "password": {"ann's long password"}, "again": {"ann's long password"}})
	ann.post("/auth/signin", url.Values{"user": {"ann"}, "password": {"ann's long password"}})
	if code, body := ask(t, web, "POST", base+"/v1/add_client", key,
		`{"id": "fadoface", "name": "FadoFace", "redirect_uris": ["https://face.acme.example/callback"], "address": "/v1"}`); code != 200 {
		t.Fatalf("add_client: %d %s", code, body)
	}
	passes, _ := connected(t, ann, client{id: "fadoface", redirect: "https://face.acme.example/callback", resource: base + "/v1"})
	facePass := passes["access_token"].(string)
	// And Ann's session, which her browser keeps.
	at, _ := url.Parse(base)
	var cookies []string
	for _, c := range ann.client.Jar.Cookies(at) {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	if len(cookies) == 0 {
		t.Fatal("Ann's browser keeps no session")
	}
	session := strings.Join(cookies, "; ")

	list := `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	for _, row := range tableAfter(t, chapter, "<!-- refused at the door for agents -->") {
		var got string
		status := func(resp *http.Response) string { return fmt.Sprint(resp.StatusCode) }
		switch row[0] {
		case "proves nobody: no pass or key, one the server does not know, or the session of a browser alone":
			want := `Bearer resource_metadata="` + base + `/.well-known/oauth-protected-resource/mcp"`
			none, _ := toDoor("POST", list, "Authorization", "")
			unknown, _ := toDoor("POST", list, "Authorization", "Bearer fado_nobody")
			browser, _ := toDoor("POST", list, "Authorization", "", "Cookie", session)
			if none.StatusCode == 401 && unknown.StatusCode == 401 && browser.StatusCode == 401 &&
				none.Header.Get("WWW-Authenticate") == want && unknown.Header.Get("WWW-Authenticate") == want {
				got = "401, with WWW-Authenticate naming where passes for /mcp come from"
			}
		case "carries a pass for the commands":
			if resp, _ := toDoor("POST", list, "Authorization", "Bearer "+facePass); resp.StatusCode == 401 {
				got = "401: a pass is for one address"
			}
		case "comes from a page of another site":
			resp, _ := toDoor("POST", list, "Origin", "https://evil.example")
			got = status(resp)
		case "is no POST":
			resp, _ := toDoor("GET", "")
			got = status(resp)
		case "does not say its body is JSON":
			resp, _ := toDoor("POST", list, "Content-Type", "text/plain")
			got = status(resp)
		case "is larger than the server reads":
			resp, _ := toDoor("POST", `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sql","arguments":{"sql":"`+strings.Repeat("x", 17<<20)+`"}}}`)
			got = status(resp)
		case "has headers that do not say what its body says":
			resp, _ := toDoor("POST", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"whoami","_meta":`+modernMeta+`}}`,
				"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "tools/call", "Mcp-Name", "get")
			got = status(resp)
		case "names a method the door does not have":
			modern, _ := toDoor("POST", `{"jsonrpc":"2.0","id":6,"method":"resources/list","params":{"_meta":`+modernMeta+`}}`,
				"MCP-Protocol-Version", "2026-07-28", "Mcp-Method", "resources/list")
			greeted, body := toDoor("POST", `{"jsonrpc":"2.0","id":7,"method":"resources/list"}`, "MCP-Protocol-Version", "2025-11-25")
			if modern.StatusCode == 404 && greeted.StatusCode == 200 && strings.Contains(body, `"code":-32601`) {
				got = "404 in the revision 2026-07-28, and 200 with the error in the revisions before"
			}
		default:
			t.Fatalf("the test does not know the row %q", row[0])
		}
		if got != row[1] {
			t.Errorf("%s: %s, the chapter says %s", row[0], got, row[1])
		}
	}
}
