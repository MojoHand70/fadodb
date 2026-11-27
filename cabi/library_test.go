package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests of the library in Go: what each function answers, without a
// C compiler. library_c_test.go loads the library built from this code
// into a program of C and holds it to the same.

// invoice is the class of the manual's chapter on naming the caller:
// sales makes an invoice, accounting books it, and only a person may.
const invoice = `{
  "name": "Invoice",
  "variables": {
    "number":  {"type": "text", "required": true},
    "account": {"type": "text"}
  },
  "states": {"initial": "open", "list": ["open", "booked"], "final": ["booked"]},
  "transitions": [
    {"name": "book", "from": "open", "to": "booked", "guard": "NOT ISBLANK(account)"}
  ],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"accounting\")",
    "create": "INGROUP(\"sales\")",
    "update": "INGROUP(\"accounting\")",
    "delete": "FALSE",
    "transition": {"book": "INGROUP(\"accounting\") AND USERKIND() = \"person\""}
  }
}`

// The callers of the tests.
const (
	designer = `{"id": "dan", "kind": "person", "system_groups": ["designers"]}`
	ann      = `{"id": "ann", "kind": "person", "groups": ["sales"]}`
	bob      = `{"id": "bob", "kind": "person", "groups": ["accounting"]}`
	robot    = `{"id": "ledger-bot", "kind": "agent", "groups": ["accounting"]}`
)

// opened opens a database in a directory of the test, and closes it when
// the test ends unless the test closed it.
func opened(t *testing.T, options string) uint64 {
	t.Helper()
	n, refusal := openDatabase(filepath.Join(t.TempDir(), "books.fado"), options)
	if n == 0 {
		t.Fatalf("not opened: %s", refusal)
	}
	t.Cleanup(func() { closeDatabase(n) })
	return n
}

// answered decodes an answer, and fails the test when it is no JSON
// object.
func answered(t *testing.T, answer []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(answer, &m); err != nil {
		t.Fatalf("the answer is no JSON object: %v\n%s", err, answer)
	}
	return m
}

// refused is the refusal of an answer, or fails the test when it holds
// none.
func refused(t *testing.T, answer []byte) map[string]any {
	t.Helper()
	e, ok := answered(t, answer)["error"].(map[string]any)
	if !ok {
		t.Fatalf("not refused: %s", answer)
	}
	return e
}

// done fails the test when the answer is a refusal.
func done(t *testing.T, answer []byte) map[string]any {
	t.Helper()
	m := answered(t, answer)
	if e, ok := m["error"]; ok {
		t.Fatalf("refused: %v", e)
	}
	return m
}

func define(t *testing.T, n uint64) {
	t.Helper()
	done(t, execCommand(n, designer, "define_class", `{"definition": `+invoice+`}`))
}

// Ann makes an invoice, the agent may not book it, Bob does: the story
// of the chapter, told through the library.
func TestTheStoryThroughTheLibrary(t *testing.T) {
	n := opened(t, "")
	define(t, n)

	made := done(t, execCommand(n, ann, "create", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"number": "FV/2026/11/0042"}}`))
	doc := made["document"].(map[string]any)
	if doc["id"] != "FV/2026/11/0042" || doc["state"] != "open" {
		t.Fatalf("made %v", doc)
	}
	done(t, execCommand(n, bob, "set", `{"class": "Invoice", "id": "FV/2026/11/0042", "values": {"account": "400-01"}}`))

	e := refused(t, execCommand(n, robot, "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"}`))
	if e["code"] != "forbidden" {
		t.Fatalf("the agent booked, or was refused for another reason: %v", e)
	}
	booked := done(t, execCommand(n, bob, "transition", `{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "book"}`))
	if s := booked["document"].(map[string]any)["state"]; s != "booked" {
		t.Fatalf("Bob's book left the invoice %v", s)
	}

	// The log keeps who did what: the caller the program named.
	events := done(t, execCommand(n, bob, "events", `{"class": "Invoice", "id": "FV/2026/11/0042"}`))
	var actors []string
	for _, ev := range events["events"].([]any) {
		actors = append(actors, fmt.Sprint(ev.(map[string]any)["actor_id"]))
	}
	if got := strings.Join(actors, " "); got != "ann bob bob" {
		t.Fatalf("the log has %q", got)
	}
}

// The answer of a command is the command set's, byte for byte: the
// library adds nothing and takes nothing away.
func TestTheAnswerIsTheCommandSets(t *testing.T) {
	n := opened(t, "")
	define(t, n)
	got := execCommand(n, ann, "get", `{"class": "Invoice", "id": "none"}`)
	e := refused(t, got)
	if e["code"] != "unknown_document" {
		t.Fatalf("refused with %v", e)
	}
	if !strings.HasPrefix(string(got), `{"error":`) {
		t.Fatalf("a refusal begins otherwise: %s", got)
	}
	if e := refused(t, execCommand(n, ann, "Create", `{}`)); e["code"] != "unknown_command" || e["hint"] != "the command is create" {
		t.Fatalf("an unknown command: %v", e)
	}
	if e := refused(t, execCommand(n, ann, "create", `{"class": "Invoice", "colour": "red"}`)); e["code"] != "bad_json" || e["path"] != "/colour" {
		t.Fatalf("a member no command takes: %v", e)
	}
	// Nothing is the empty object.
	if e := refused(t, execCommand(n, ann, "create", "")); e["code"] != "bad_json" {
		t.Fatalf("create of nothing: %v", e)
	}
	// subscribe answers a stream, which one call cannot.
	if e := refused(t, execCommand(n, bob, "subscribe", `{}`)); e["code"] != "not_supported" {
		t.Fatalf("subscribe: %v", e)
	}
}

// Nobody is a caller as a Principal left empty is in Go: the rules of a
// class decide what nobody may do.
func TestNobodyCalls(t *testing.T) {
	n := opened(t, "")
	define(t, n)
	for _, nobody := range []string{"", "{}", "  "} {
		e := refused(t, execCommand(n, nobody, "create", `{"class": "Invoice", "values": {"number": "1"}}`))
		if e["code"] != "forbidden" {
			t.Fatalf("nobody (%q) was refused with %v", nobody, e)
		}
	}
}

func TestWhatTheCallerMaySay(t *testing.T) {
	n := opened(t, "")
	define(t, n)
	for _, c := range []struct{ caller, code, path string }{
		{`{"id": "ann", "name": "Ann"}`, "bad_json", "/name"},
		{`{"id": 7}`, "bad_json", "/id"},
		{`{"groups": "sales"}`, "bad_json", "/groups"},
		{`{"today": "27.11.2026"}`, "bad_value", "/today"},
		{`{"id": "ann", "id": "bob"}`, "bad_json", "/id"},
		{`{"for": "ann"}`, "bad_json", "/for"},
		{`["ann"]`, "bad_json", ""},
		{`{"id": "ann"`, "bad_json", ""},
		{`{"id": "ann"} {}`, "bad_json", ""},
	} {
		e := refused(t, execCommand(n, c.caller, "get", `{"class": "Invoice", "id": "x"}`))
		if e["code"] != c.code || (c.path != "" && e["path"] != c.path) {
			t.Errorf("caller %s: refused with %v, want %s at %q", c.caller, e, c.code, c.path)
		}
		if e["hint"] == nil || e["hint"] == "" {
			t.Errorf("caller %s: no hint in %v", c.caller, e)
		}
	}
	// The caller's day is the commit's business date when the command
	// gives none (ADR-0026).
	made := done(t, execCommand(n, `{"id": "ann", "groups": ["sales"], "today": "2026-11-02"}`, "create", `{"class": "Invoice", "values": {"number": "2"}}`))
	id := made["document"].(map[string]any)["id"].(string)
	got := done(t, execCommand(n, bob, "events", `{"class": "Invoice", "id": `+quote(id)+`}`))
	if day := got["events"].([]any)[0].(map[string]any)["business_date"]; day != "2026-11-02" {
		t.Fatalf("the business date is %v, not the caller's day", day)
	}
}

func TestWhatTheOptionsMaySay(t *testing.T) {
	n := opened(t, `{"timezone": "Europe/Warsaw", "synchronous": "FULL", "max_question_seconds": 2.5,
	  "max_text": 1000, "max_indexed_rows": 50, "max_blob_bytes": 1048576, "blob_store": "dir",
	  "host_answers_for": true, "outbox": {"manual": true, "workers": 2, "max_attempts": 3, "backoff_seconds": 0.5}}`)
	define(t, n)

	dir := t.TempDir()
	for i, c := range []struct{ options, code, path string }{
		{`{"time_zone": "UTC"}`, "bad_json", "/time_zone"},
		{`{"max_text": "a lot"}`, "bad_json", "/max_text"},
		{`{"max_text": 1.5}`, "bad_json", "/max_text"},
		{`{"outbox": {"workers": 2, "retries": 3}}`, "bad_json", "/outbox/retries"},
		{`{"outbox": {"manual": "yes"}}`, "bad_json", "/outbox/manual"},
		{`{"outbox": []}`, "bad_json", "/outbox"},
		{`{"blob_store": "external"}`, "bad_value", "/blob_store"},
		{`{"timezone": "Mars/Olympus"}`, "", ""},
		{`[]`, "bad_json", ""},
	} {
		path := filepath.Join(dir, fmt.Sprintf("%d.fado", i))
		got, refusal := openDatabase(path, c.options)
		if got != 0 {
			closeDatabase(got)
			t.Errorf("options %s opened a database", c.options)
			continue
		}
		e := refused(t, refusal)
		if (c.code != "" && e["code"] != c.code) || (c.path != "" && e["path"] != c.path) {
			t.Errorf("options %s: refused with %v, want %s at %q", c.options, e, c.code, c.path)
		}
	}
	if got, refusal := openDatabase("", ""); got != 0 || refused(t, refusal)["code"] != "bad_value" {
		t.Errorf("a database with no file: %d %s", got, refusal)
	}
}

// A number reaches its database until it is closed, and none after; no
// number is given twice.
func TestTheNumberOfADatabase(t *testing.T) {
	dir := t.TempDir()
	first, _ := openDatabase(filepath.Join(dir, "a.fado"), "")
	second, _ := openDatabase(filepath.Join(dir, "b.fado"), "")
	if first == 0 || second == 0 || first == second {
		t.Fatalf("numbers %d and %d", first, second)
	}
	if r := closeDatabase(first); r != nil {
		t.Fatalf("close: %s", r)
	}
	if e := refused(t, execCommand(first, designer, "list_classes", `{}`)); e["code"] != "closed" {
		t.Fatalf("a command after close: %v", e)
	}
	if e := refused(t, closeDatabase(first)); e["code"] != "closed" {
		t.Fatalf("a second close: %v", e)
	}
	if e := refused(t, execCommand(0, designer, "list_classes", `{}`)); e["code"] != "closed" {
		t.Fatalf("a command to 0: %v", e)
	}
	again, _ := openDatabase(filepath.Join(dir, "a.fado"), "")
	if again == first || again == second {
		t.Fatalf("the number %d was given again", again)
	}
	done(t, execCommand(second, designer, "list_classes", `{}`))
	closeDatabase(second)
	closeDatabase(again)
}

// Commands from many threads at once, on one database, while it is
// closed under them: each is answered, the ones after the close with
// closed, and nothing is lost of what was answered done.
func TestManyThreadsAndAClose(t *testing.T) {
	n := opened(t, "")
	define(t, n)
	const threads, each = 16, 25
	var wg sync.WaitGroup
	var made sync.Map
	for th := range threads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				id := fmt.Sprintf("T%d-%d", th, i)
				m := answered(t, execCommand(n, ann, "create", `{"class": "Invoice", "id": "`+id+`", "values": {"number": "`+id+`"}}`))
				if _, refused := m["error"]; !refused {
					made.Store(id, true)
				}
			}
		}()
	}
	wg.Wait()
	count := 0
	made.Range(func(any, any) bool { count++; return true })
	if count != threads*each {
		t.Fatalf("%d of %d made", count, threads*each)
	}
	got := done(t, execCommand(n, bob, "count", `{"class": "Invoice"}`))
	if fmt.Sprint(got["count"]) != fmt.Sprint(threads*each) {
		t.Fatalf("counted %v", got)
	}
}

// A command that waits is answered at once when its database is closed,
// and the close does not wait out its seconds.
func TestACloseEndsTheWaiting(t *testing.T) {
	n, refusal := openDatabase(filepath.Join(t.TempDir(), "books.fado"), "")
	if n == 0 {
		t.Fatal(string(refusal))
	}
	define(t, n)
	answers := make(chan []byte, 1)
	go func() {
		answers <- execCommand(n, bob, "wait_for_changes", `{"classes": ["Invoice"], "seconds": 60}`)
	}()
	time.Sleep(200 * time.Millisecond)
	began := time.Now()
	if r := closeDatabase(n); r != nil {
		t.Fatalf("close: %s", r)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("the close waited %v", took)
	}
	select {
	case a := <-answers:
		answered(t, a)
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting command was not answered")
	}
}

// A failure of the engine, which is no refusal, keeps its words: the
// program that loaded the library has no other log.
func TestAFailureKeepsItsWords(t *testing.T) {
	got := refused(t, failed(fmt.Errorf("the disk is full")))
	if got["code"] != "internal" || !strings.Contains(fmt.Sprint(got["message"]), "the disk is full") {
		t.Fatalf("%v", got)
	}
	var answer []byte
	func() {
		defer caught("create", &answer)
		panic("a nil map")
	}()
	if e := refused(t, answer); e["code"] != "internal" || !strings.Contains(fmt.Sprint(e["message"]), "a nil map") {
		t.Fatalf("a panic was answered %v", e)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
