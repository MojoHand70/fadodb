package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The order of the manual's chapter on the outbox: sending it mails the
// customer and posts it to the books, whose entry comes back into it.
const (
	order = `{
  "name": "Order",
  "variables": {
    "customer": {"type": "text", "required": true},
    "amount":   {"type": "number", "required": true},
    "entry":    {"type": "text"}
  },
  "states": {"initial": "new", "list": ["new", "sent"]},
  "transitions": [{"name": "send", "from": "new", "to": "sent"}],
  "rules": [
    {"on": "transition", "name": "send", "do": [
      {"action": "mail.send",  "args": {"to": "customer"}},
      {"action": "books.post", "args": {"amount": "amount"}, "into": {"entry": "entry"}}
    ]}
  ],
  "access": {"read": "INGROUP(\"everyone\")", "create": "INGROUP(\"everyone\")", "update": "CREATEDBY() = USER()", "delete": "FALSE"}
}`
	mailSend  = `{"name": "mail.send", "params": {"to": "text"}}`
	booksPost = `{"name": "books.post", "params": {"amount": "number"}, "returns": {"entry": "text"}}`
)

// heard is what the program's function was called with.
type heard struct {
	mu    sync.Mutex
	calls []map[string]any
}

func (h *heard) add(object string) map[string]any {
	var call map[string]any
	_ = json.Unmarshal([]byte(object), &call)
	h.mu.Lock()
	h.calls = append(h.calls, call)
	h.mu.Unlock()
	return call
}

// withActions opens a database whose actions mail.send and books.post the
// program carries out with answer, the queue run by hand.
func withActions(t *testing.T, answer func(call uint64, action string, object map[string]any)) uint64 {
	t.Helper()
	n, refusal := openDatabase(filepath.Join(t.TempDir(), "shop.fado"),
		`{"actions": ["mail.send", "books.post"], "outbox": {"manual": true}}`,
		func(call uint64, action, object string) {
			var o map[string]any
			if err := json.Unmarshal([]byte(object), &o); err != nil {
				t.Errorf("the call is no JSON: %v", err)
			}
			answer(call, action, o)
		})
	if n == 0 {
		t.Fatalf("not opened: %s", refusal)
	}
	t.Cleanup(func() { closeDatabase(n) })
	done(t, execCommand(n, designer, "define_action", `{"definition": `+mailSend+`}`))
	done(t, execCommand(n, designer, "define_action", `{"definition": `+booksPost+`}`))
	done(t, execCommand(n, designer, "define_class", `{"definition": `+order+`}`))
	return n
}

// sendOrder makes an order as Ann, sends it and runs the queue.
func sendOrder(t *testing.T, n uint64, id string) {
	t.Helper()
	done(t, execCommand(n, ann, "create", `{"class": "Order", "id": "`+id+`", "values": {"customer": "jan@example.com", "amount": 120}}`))
	done(t, execCommand(n, ann, "transition", `{"class": "Order", "id": "`+id+`", "transition": "send"}`))
	done(t, execCommand(n, ada, "drain", `{}`))
}

func TestTheProgramCarriesOutItsActions(t *testing.T) {
	var h heard
	n := withActions(t, func(call uint64, action string, object map[string]any) {
		h.add(jsonOf(object))
		result := `{"result": {}}`
		if action == "books.post" {
			result = `{"result": {"entry": "JE-2026-0007"}}`
		}
		if answerCall(call, result) != 0 {
			t.Errorf("the answer of %s was not taken", action)
		}
		if answerCall(call, result) != -1 {
			t.Errorf("a second answer of %s was taken", action)
		}
	})
	sendOrder(t, n, "O-1")

	if len(h.calls) != 2 {
		t.Fatalf("the program was called %d times: %v", len(h.calls), h.calls)
	}
	mail := h.calls[0]
	if mail["action"] != "mail.send" || mail["attempt"] != float64(1) || !strings.HasSuffix(mail["key"].(string), ":0") {
		t.Fatalf("the call of mail.send: %v", mail)
	}
	if to := mail["args"].(map[string]any)["to"]; to != "jan@example.com" {
		t.Fatalf("mail.send was called for %v", to)
	}
	event := mail["event"].(map[string]any)
	if event["kind"] != "transition" || event["doc_id"] != "O-1" || event["document"].(map[string]any)["state"] != "sent" {
		t.Fatalf("the event of the call: %v", event)
	}
	// What books.post returned is in the order.
	got := done(t, execCommand(n, ann, "get", `{"class": "Order", "id": "O-1"}`))
	if entry := got["document"].(map[string]any)["values"].(map[string]any)["entry"]; entry != "JE-2026-0007" {
		t.Fatalf("the entry of the order is %v", entry)
	}
	// After it returned, the call takes no answer.
	if answerCall(1, `{"result": {}}`) != -1 {
		t.Fatal("a call that returned took an answer")
	}
}

// errorsOf are the words of the queued actions of a document, by
// action, and their status.
func errorsOf(t *testing.T, n uint64, id string) (status, words map[string]string) {
	t.Helper()
	status, words = map[string]string{}, map[string]string{}
	for _, a := range done(t, execCommand(n, ada, "outbox", `{"id": "`+id+`"}`))["actions"].([]any) {
		row := a.(map[string]any)
		name := row["action"].(string)
		status[name] = row["status"].(string)
		if m, ok := row["error_message"].(string); ok {
			words[name] = m
		}
	}
	return status, words
}

// The actions of one event run in their order, so a failure of the
// first holds the second back, and each case fails one of them.
func TestWhenTheProgramsActionFails(t *testing.T) {
	// A refusal is dead at once, with its words.
	n := withActions(t, func(call uint64, action string, _ map[string]any) {
		if action == "books.post" {
			answerCall(call, `{"error": {"message": "the period 2026-11 is closed", "permanent": true}}`)
			return
		}
		answerCall(call, `{"result": {}}`)
	})
	sendOrder(t, n, "O-2")
	status, words := errorsOf(t, n, "O-2")
	if status["mail.send"] != "done" || status["books.post"] != "dead" || words["books.post"] != "the period 2026-11 is closed" {
		t.Errorf("a refusal: %v %v", status, words)
	}

	// A failure is tried again, and holds the next action back.
	n = withActions(t, func(call uint64, action string, _ map[string]any) {
		answerCall(call, `{"error": {"message": "the mail server is down"}}`)
	})
	sendOrder(t, n, "O-3")
	status, words = errorsOf(t, n, "O-3")
	if status["mail.send"] != "pending" || words["mail.send"] != "the mail server is down" || status["books.post"] != "pending" {
		t.Errorf("a failure: %v %v", status, words)
	}
}

func TestAnAnswerThatIsNone(t *testing.T) {
	for _, c := range []struct{ answer, words string }{
		{"", "returned without answering"},
		{`{"result": {}, "error": {"message": "x"}}`, "both a result and an error"},
		{`{"entry": "JE-1"}`, `which is no {"result": …} and no {"error": …}`},
		{`not json`, `which is no {"result": …}`},
	} {
		n := withActions(t, func(call uint64, _ string, _ map[string]any) {
			if c.answer != "" {
				answerCall(call, c.answer)
			}
		})
		sendOrder(t, n, "O-4")
		status, words := errorsOf(t, n, "O-4")
		if status["mail.send"] != "pending" || !strings.Contains(words["mail.send"], c.words) {
			t.Errorf("answered %q: %v %v", c.answer, status, words)
		}
	}
}

func TestActionsWithoutAFunction(t *testing.T) {
	got, refusal := openDatabase(filepath.Join(t.TempDir(), "x.fado"), `{"actions": ["mail.send"]}`, nil)
	if got != 0 {
		closeDatabase(got)
		t.Fatal("a database opened with actions and no function")
	}
	if e := refused(t, refusal); e["code"] != "bad_value" || e["path"] != "/actions" {
		t.Fatalf("refused %v", e)
	}
	got, refusal = openDatabase(filepath.Join(t.TempDir(), "y.fado"), `{"actions": "mail.send"}`, nil)
	if got != 0 || refused(t, refusal)["path"] != "/actions" {
		t.Fatalf("actions as text: %d %s", got, refusal)
	}
}
