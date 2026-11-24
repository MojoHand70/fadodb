package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// shownWait finds what changes from one run to the next in an answer
// laid out as the chapter shows it: the id of an event, the place, and
// the day; the chapter shows those of its own.
var (
	shownWaitIDs  = regexp.MustCompile(`"(event_id|after)": "[0-9A-Z]{26}"`)
	shownWaitDays = regexp.MustCompile(`"business_date": "\d{4}-\d{2}-\d{2}"`)
)

// The chapter's section on waiting for a change, played against a real
// server through "fadodb mcp": ap-agent waits for an invoice entering
// review, Bob submits one while it waits, the agent reads the event and
// asks again from its place (ADR-0064, ruling 3).
func TestTheChapterOnWaiting(t *testing.T) {
	chapter := doorChapter(t)
	path := filepath.Join(t.TempDir(), "acme.fado")
	for _, args := range [][]string{
		{"init", path},
		{"user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path},
	} {
		if code, _, said := tool(args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, said)
		}
	}
	_, olga, _ := tool("key", "new", "olga", "--db", path)
	olga = strings.TrimSpace(olga)
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0")
	base := "http://" + addr
	web := &http.Client{Timeout: 10 * time.Second}
	send := func(key, name, object string) string {
		t.Helper()
		status, body := ask(t, web, "POST", base+"/v1/"+name, key, object)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", name, status, body)
		}
		return body
	}
	send(olga, "define_class", `{"definition": {"name": "Invoice", "title": "number",
	  "variables": {"number": {"type": "text", "required": true}},
	  "states": {"initial": "draft", "list": ["draft", "review", "approved"]},
	  "transitions": [{"name": "submit", "from": "draft", "to": "review"}, {"name": "approve", "from": "review", "to": "approved"}],
	  "access": {"read": "TRUE", "create": "INGROUP(\"sales\")", "update": "INGROUP(\"sales\")", "delete": "FALSE",
	             "transition": {"submit": "INGROUP(\"sales\")", "approve": "INGROUP(\"finance\")"}}}}`)
	keys := map[string]string{}
	for _, u := range []string{`{"id": "bob", "kind": "person", "groups": ["sales"]}`, `{"id": "ap-agent", "kind": "agent", "groups": ["finance"]}`} {
		var made struct {
			User struct{ ID string } `json:"user"`
		}
		json.Unmarshal([]byte(send(olga, "create_user", u)), &made)
		var key struct {
			Key struct{ Secret string } `json:"key"`
		}
		json.Unmarshal([]byte(send(olga, "create_key", `{"user": "`+made.User.ID+`", "label": "chapter"}`)), &key)
		keys[made.User.ID] = key.Key.Secret
	}
	send(keys["bob"], "create", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042"}}`)

	// The agent's first call, as the chapter writes it, through the door
	// its program starts; it waits.
	t.Setenv("FADODB_URL", base)
	t.Setenv("FADODB_KEY", keys["ap-agent"])
	line := func(params string) string {
		var p map[string]json.RawMessage
		if err := json.Unmarshal([]byte(params), &p); err != nil {
			t.Fatalf("the chapter shows a call that is not JSON: %v\n%s", err, params)
		}
		p["_meta"] = json.RawMessage(modernMeta)
		text, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": p})
		return string(text)
	}
	// The place the agent's wait takes. The server counts a wait among
	// its streams before the wait takes its place in the log, so Bob's
	// submit, made as soon as the count shows the wait, can commit first,
	// and a wait that names no place would begin after it and never hear
	// of it (seen once on a loaded runner). So the test gives the
	// chapter's call the place the log is at now; the chapter's agent
	// names none, and the server takes the place then.
	var at struct {
		After string `json:"after"`
	}
	if err := json.Unmarshal([]byte(send(keys["ap-agent"], "wait_for_changes", `{"classes": ["Invoice"]}`)), &at); err != nil || at.After == "" {
		t.Fatalf("the place of the log: %v", err)
	}
	call := blockOf(t, chapter, "waiting: the call")
	placed := strings.Replace(call, `"seconds": 30}`, `"seconds": 30, "after": "`+at.After+`"}`, 1)
	if placed == call {
		t.Fatalf("the chapter's call names no seconds to put the place beside: %s", call)
	}
	first := line(placed)
	type ended struct {
		code  int
		wrote []string
		said  string
	}
	answered := make(chan ended, 1)
	began := time.Now()
	go func() {
		code, wrote, said := door(nil, first)
		answered <- ended{code, wrote, said}
	}()
	// Bob submits once the agent waits: the server counts the wait among
	// its streams.
	for deadline := time.Now().Add(10 * time.Second); ; {
		_, counts := ask(t, web, "GET", base+"/v1/metrics", olga, "")
		if strings.Contains(counts, "\nfadodb_streams 1\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent does not wait:\n%s", counts)
		}
		time.Sleep(20 * time.Millisecond)
	}
	send(keys["bob"], "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "submit"}`)
	var got ended
	select {
	case got = <-answered:
	case <-time.After(20 * time.Second):
		t.Fatal("the wait did not answer the submit")
	}
	if took := time.Since(began); took > 15*time.Second {
		t.Errorf("the wait answered after %s", took)
	}
	text := toolText(t, got.code, got.wrote, got.said)
	var reply struct {
		Events []struct {
			ID string `json:"event_id"`
		} `json:"events"`
		After string `json:"after"`
	}
	if err := json.Unmarshal([]byte(text), &reply); err != nil || len(reply.Events) != 1 || reply.After != reply.Events[0].ID {
		t.Fatalf("the answer's place is not the event it answered: %s", text)
	}
	laid := shownWaitIDs.ReplaceAllString(asTheAgentReads(t, text), `"$1": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE"`)
	laid = shownWaitDays.ReplaceAllString(laid, `"business_date": "2026-11-15"`)
	if want := blockOf(t, chapter, "waiting: the answer"); laid != want {
		t.Errorf("the agent reads\n%s\nand the chapter shows\n%s", laid, want)
	}

	// Again from its place, the chapter's call with the place the agent
	// was given, and nothing to wait for but a second.
	again := strings.Replace(blockOf(t, chapter, "waiting: again"), "01KA2F8Q7ZJ4M0X3T5V9B1C6DE", reply.After, 1)
	again = strings.Replace(again, `"seconds": 30`, `"seconds": 1`, 1)
	code, wrote, said := door(nil, line(again))
	var quiet struct {
		Events []json.RawMessage `json:"events"`
		After  string            `json:"after"`
		More   bool              `json:"more"`
	}
	if text := toolText(t, code, wrote, said); json.Unmarshal([]byte(text), &quiet) != nil || len(quiet.Events) != 0 || quiet.After != reply.After || quiet.More {
		t.Errorf("asked again from its place, the agent reads %s", text)
	}
}

// toolText is the text a tool answered through the door, failing the
// test when the door did not end well or the tool answered an error.
func toolText(t *testing.T, code int, wrote []string, said string) string {
	t.Helper()
	if code != exitDone || said != "" || len(wrote) != 1 {
		t.Fatalf("the door ended with %d, wrote %d lines and said %q", code, len(wrote), said)
	}
	var reply struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.Unmarshal([]byte(wrote[0]), &reply); err != nil || len(reply.Result.Content) != 1 || reply.Result.IsError {
		t.Fatalf("the door wrote %s", wrote[0])
	}
	return reply.Result.Content[0].Text
}
