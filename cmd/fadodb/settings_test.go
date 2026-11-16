package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

// The settings as ADR-0059, item 4, writes them, whole.
const settingsOfTheRecord = `{
  "outbound": {
    "hosts": ["hooks.acme.example"],
    "connections": {
      "slack": ["slack.com"],
      "books": ["books.acme.internal:8443"]
    }
  },
  "limits": {
    "request_bytes": 16777216,
    "command_seconds": 30,
    "commands": 64,
    "streams": 256
  }
}`

// The file of settings is read whole, and refused for anything it
// should not have (ADR-0059, item 4).
func TestTheSettings(t *testing.T) {
	s, err := parseSettings([]byte(settingsOfTheRecord))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.hosts, " ") != "hooks.acme.example" || len(s.connections) != 2 ||
		strings.Join(s.connections["slack"], " ") != "slack.com" || strings.Join(s.connections["books"], " ") != "books.acme.internal:8443" {
		t.Errorf("what may be called: %v, %v", s.hosts, s.connections)
	}
	if want := (httpdoor.Limits{RequestBytes: 16 << 20, CommandTime: 30 * time.Second, Commands: 64, Streams: 256}); s.limits != want {
		t.Errorf("the limits: %+v", s.limits)
	}
	if got := s.mayCall(); got != "its actions may call hooks.acme.example; over the connection books, books.acme.internal:8443; over the connection slack, slack.com" {
		t.Errorf("what the server says it may call: %s", got)
	}

	// A file that says nothing leaves everything as the server is when
	// it is told nothing: no host, and the limits of the door.
	for _, empty := range []string{`{}`, `{"outbound": {}, "limits": {}}`, `{"outbound": {"hosts": [], "connections": {}}}`} {
		s, err := parseSettings([]byte(empty))
		if err != nil || s.calls() || s.limits != (httpdoor.Limits{}) {
			t.Errorf("%s: %v, %+v", empty, err, s)
		}
		if err == nil && !strings.Contains(s.mayCall(), "may call no host") {
			t.Errorf("%s: the server says %s", empty, s.mayCall())
		}
	}
	// A time may have a fraction; a connection may have no host yet.
	if s, err := parseSettings([]byte(`{"limits": {"command_seconds": 0.5}, "outbound": {"connections": {"slack": []}}}`)); err != nil ||
		s.limits.CommandTime != 500*time.Millisecond || !s.calls() || !strings.Contains(s.mayCall(), "over the connection slack, no host") {
		t.Errorf("half a second, and a connection without hosts: %v, %+v", err, s)
	}

	for name, c := range map[string]struct{ file, words string }{
		"what is not JSON":                        {"{\n  \"limits\": {\n    \"commands\": 64,\n  }\n}", "it is not JSON, at line 4"},
		"what is no object":                       {`["slack.com"]`, "the file is an object, written in braces"},
		"a member that is no setting":             {`{"listen": ":8443"}`, `the file has "listen", which is no setting: it may have outbound, limits, inbound`},
		"a member in other letters":               {`{"Outbound": {}}`, `the file has "Outbound", which is no setting`},
		"a member twice":                          {`{"outbound": {"hosts": ["a.example"]}, "outbound": {}}`, `the file has "outbound" twice`},
		"something after the settings":            {`{} {}`, "it is not JSON"},
		"outbound that is no object":              {`{"outbound": ["slack.com"]}`, "outbound is an object"},
		"a member outbound does not have":         {`{"outbound": {"timeout": 5}}`, `outbound has "timeout", which is no setting: it may have hosts, connections`},
		"hosts that are no list":                  {`{"outbound": {"hosts": "slack.com"}}`, "outbound.hosts is a list of hosts"},
		"a host that is no text":                  {`{"outbound": {"hosts": [443]}}`, "outbound.hosts is a list of hosts"},
		"hosts that are null":                     {`{"outbound": {"hosts": null}}`, "outbound.hosts is a list of hosts"},
		"connections that are a list":             {`{"outbound": {"connections": ["slack"]}}`, "outbound.connections is an object"},
		"a connection whose hosts are a text":     {`{"outbound": {"connections": {"slack": "slack.com"}}}`, "outbound.connections.slack is a list of hosts"},
		"a connection twice":                      {`{"outbound": {"connections": {"slack": [], "slack": []}}}`, `outbound.connections has "slack" twice`},
		"connections that differ in case":         {`{"outbound": {"connections": {"slack": ["slack.com"], "Slack": ["evil.example"]}}}`, "outbound.connections has Slack and slack, which differ in case only"},
		"a connection no definition can name":     {`{"outbound": {"connections": {"../books": []}}}`, `outbound.connections has "../books", which no definition can name`},
		"a connection named with two underscores": {`{"outbound": {"connections": {"books__eu": []}}}`, "which no definition can name"},
		"a limit the file does not have":          {`{"limits": {"users": 5}}`, `limits has "users", which is no setting: it may have request_bytes, command_seconds, commands, streams`},
		"a limit of zero":                         {`{"limits": {"commands": 0}}`, "limits.commands is a whole number above zero, and here it is 0; leave it out for the usual"},
		"a limit below zero":                      {`{"limits": {"request_bytes": -1}}`, "limits.request_bytes is a whole number above zero"},
		"a limit with a fraction":                 {`{"limits": {"commands": 6.5}}`, "limits.commands is a whole number above zero, and here it is 6.5"},
		"a limit written as a text":               {`{"limits": {"streams": "256"}}`, `limits.streams is a whole number above zero, and here it is "256"`},
		"a time of zero":                          {`{"limits": {"command_seconds": 0}}`, "limits.command_seconds is a number of seconds above zero and at most 86400"},
		"a time beyond a day":                     {`{"limits": {"command_seconds": 86401}}`, "limits.command_seconds is a number of seconds above zero and at most 86400"},
		"a time written as a text":                {`{"limits": {"command_seconds": "30s"}}`, "limits.command_seconds is a number of seconds"},
		"a time too short to keep":                {`{"limits": {"command_seconds": 0.0000001}}`, "limits.command_seconds is at least a millisecond"},
	} {
		if _, err := parseSettings([]byte(c.file)); err == nil || !strings.Contains(err.Error(), c.words) {
			t.Errorf("%s: want a refusal with %q, got %v", name, c.words, err)
		}
	}

	// The server refuses to start on settings it cannot read, before it
	// opens anything, and says which file and what is wrong with it.
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	write := func(name, text string) string {
		t.Helper()
		file := filepath.Join(dir, name)
		if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	for name, c := range map[string]struct {
		args  []string
		words string
	}{
		"a file of settings that is not there": {[]string{"--settings", filepath.Join(dir, "none.json")}, "the file cannot be read"},
		"a setting the file should not have":   {[]string{"--settings", write("a.json", `{"listen": ":8443"}`)}, `a.json: the file has "listen", which is no setting`},
		"a host that is none":                  {[]string{"--settings", write("b.json", `{"outbound": {"hosts": ["https://slack.com/api"]}}`)}, "is not a host: write a name, .suffix, name:port or http://name:port"},
		"a host of a connection that is none":  {[]string{"--settings", write("c.json", `{"outbound": {"connections": {"slack": ["slack.com:https"]}}}`)}, "connection slack:"},
		"a directory of secrets that is none":  {[]string{"--secrets-dir", filepath.Join(dir, "none")}, "there is no directory of secrets at"},
		"a file given as the directory":        {[]string{"--secrets-dir", path}, "there is no directory of secrets at"},
	} {
		code, _, said := tool(append([]string{"serve", path, "--listen", "127.0.0.1:0"}, c.args...)...)
		if code != 2 || !strings.Contains(said, c.words) {
			t.Errorf("%s: exit %d, want 2 and %q:\n%s", name, code, c.words, said)
		}
	}
	// What was refused held nothing: the database opens.
	if code, _, said := tool("user", "add", "olga", "--kind", "person", "--db", path); code != 0 {
		t.Errorf("the tool, after servers that did not start: exit %d\n%s", code, said)
	}
}

// Where a secret is (ADR-0059, item 5): a file in the directory of
// secrets, or the environment; read again each time it is asked.
func TestWhereASecretIs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	env := map[string]string{}
	kept := &secrets{dir: dir, env: func(name string) (string, bool) { v, ok := env[name]; return v, ok }}
	put := func(file, value string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	find := func(connection, name string) string {
		t.Helper()
		value, err := kept.find(ctx, connection, name)
		if err != nil {
			return "no: " + err.Error()
		}
		return value
	}

	// Neither place has it, and the words say both, by the names the
	// operator needs and without the directory of the machine.
	got := find("slack", "token")
	if got != "no: the server's secrets directory has no file slack/token, and its environment no FADODB_SECRET_SLACK__TOKEN" || strings.Contains(got, dir) {
		t.Errorf("a secret that is nowhere: %s", got)
	}
	// The environment.
	env["FADODB_SECRET_SLACK__TOKEN"] = "from-the-environment"
	if got := find("slack", "token"); got != "from-the-environment" {
		t.Errorf("a secret of the environment: %s", got)
	}
	// The file is looked for first, and read without the end of its
	// line.
	put("slack/token", "from-the-file\n")
	if got := find("slack", "token"); got != "from-the-file" {
		t.Errorf("a secret of the directory: %q", got)
	}
	put("slack/token", "written on Windows\r\n")
	if got := find("slack", "token"); got != "written on Windows" {
		t.Errorf("a secret with the end of a line of Windows: %q", got)
	}
	// It is read again each time: a secret is changed without a restart.
	put("slack/token", "renewed")
	if got := find("slack", "token"); got != "renewed" {
		t.Errorf("a secret that was changed: %q", got)
	}
	// Names differ in case nowhere: the file and the variable are found
	// however the definition writes the names.
	if got := find("Slack", "Token"); got != "renewed" {
		t.Errorf("a secret asked for in other letters: %q", got)
	}
	env["FADODB_SECRET_BOOKS__SIGNING_KEY"] = "k"
	if got := find("Books", "signing_key"); got != "k" {
		t.Errorf("a secret of the environment asked for in other letters: %q", got)
	}
	// A file that is there and empty is no secret, and the environment
	// is not asked instead: the operator meant the file.
	put("slack/token", "\n")
	if got := find("slack", "token"); got != "no: the file slack/token of the server's secrets directory is empty" {
		t.Errorf("an empty file: %s", got)
	}
	// A file that cannot be a secret.
	put("slack/token", strings.Repeat("x", maxSecret+1))
	if got := find("slack", "token"); !strings.Contains(got, "cannot be read: it is longer than 65536 bytes") || strings.Contains(got, dir) {
		t.Errorf("a file that is too long: %s", got)
	}
	put("slack/long/x", "")
	if got := find("slack", "long"); !strings.HasPrefix(got, "no: the file slack/long of the server's secrets directory cannot be read") || strings.Contains(got, dir) {
		t.Errorf("a directory where the file should be: %s", got)
	}

	// One connection never reads the secret of another. With one
	// underscore between the names, the secret c of the connection a_b
	// and the secret b_c of the connection a would be one variable.
	env["FADODB_SECRET_A_B__C"] = "of a_b"
	if got := find("a_b", "c"); got != "of a_b" {
		t.Errorf("the secret c of a_b: %s", got)
	}
	if got := find("a", "b_c"); !strings.HasPrefix(got, "no: ") || !strings.Contains(got, "FADODB_SECRET_A__B_C") {
		t.Errorf("the secret b_c of a: %s", got)
	}
	// What is no name is never made a path.
	put("outside", "not a secret")
	for _, c := range [][2]string{{"..", "outside"}, {"slack", "../outside"}, {"", "token"}, {"slack", ""}, {"slack/x", "token"}, {"a__b", "c"}} {
		if got := find(c[0], c[1]); got != "no: it has no name a secret is kept under" {
			t.Errorf("the secret %q of %q: %s", c[1], c[0], got)
		}
	}

	// Without a directory, the environment alone.
	bare := &secrets{env: kept.env}
	if got, err := bare.find(ctx, "slack", "token"); err != nil || got != "from-the-environment" {
		t.Errorf("without a directory: %q, %v", got, err)
	}
	if _, err := bare.find(ctx, "mail", "password"); err == nil ||
		err.Error() != "the server's environment has no FADODB_SECRET_MAIL__PASSWORD, and the server was given no secrets directory, where it would be the file mail/password" {
		t.Errorf("without a directory, a secret that is nowhere: %v", err)
	}
}

// The books of a company, as a served database calls them: a receiver
// that keeps what it was sent.
type receiver struct {
	mu   sync.Mutex
	got  []string
	srv  *httptest.Server
	host string
}

func receiving(t *testing.T) *receiver {
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, req.Header.Get("Authorization")+" "+string(body))
		n := len(r.got)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"entry": "E-%d"}`, n)
	}))
	t.Cleanup(r.srv.Close)
	r.host = strings.TrimPrefix(r.srv.URL, "http://")
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *receiver) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.got) == 0 {
		return ""
	}
	return r.got[len(r.got)-1]
}

// blockOfTheChapter is a block of the chapter on serving, by the mark
// that stands before it.
func blockOfTheChapter(t *testing.T, mark string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual", "serving-a-database.md"))
	if err != nil {
		t.Fatal(err)
	}
	chapter := strings.ReplaceAll(string(text), "\r\n", "\n")
	_, after, found := strings.Cut(chapter, "<!-- "+mark+" -->\n```")
	if !found {
		t.Fatalf("the chapter has no block marked %q", mark)
	}
	_, after, _ = strings.Cut(after, "\n")
	block, _, found := strings.Cut(after, "\n```")
	if !found {
		t.Fatalf("the block marked %q does not end", mark)
	}
	return block
}

// The host of the books in the chapter, and the address the chapter's
// action calls.
const (
	booksOfTheChapter   = "books.acme.internal:8443"
	addressOfTheChapter = "https://" + booksOfTheChapter + "/entries"
)

// A served database calls the hosts its settings list, with the secrets
// the operator keeps beside it, and nothing else (ADR-0059, items 2, 4,
// 5 and 6).
func TestWhatAServedDatabaseCalls(t *testing.T) {
	books := receiving(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	if code, _, said := tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--system", "designers", "--db", path); code != 0 {
		t.Fatal(said)
	}
	_, key, _ := tool("key", "new", "olga", "--db", path)
	key = strings.TrimSpace(key)
	client := &http.Client{Timeout: 10 * time.Second}
	// The action, the class and the settings are the chapter's own, with
	// the books of the test where the chapter has Acme's.
	definition := blockOfTheChapter(t, "the action")
	if !strings.Contains(definition, addressOfTheChapter) {
		t.Fatalf("the action of the chapter does not call %s:\n%s", addressOfTheChapter, definition)
	}
	definition = strings.Replace(definition, addressOfTheChapter, "http://"+books.host+"/entries", 1)
	orderOfTheBooks := blockOfTheChapter(t, "the class")
	settingsOfTheCase := blockOfTheChapter(t, "the settings of the case")
	if !strings.Contains(settingsOfTheCase, `"`+booksOfTheChapter+`"`) {
		t.Fatalf("the settings of the chapter do not list %s:\n%s", booksOfTheChapter, settingsOfTheCase)
	}
	settingsOfTheCase = strings.Replace(settingsOfTheCase, `"`+booksOfTheChapter+`"`, `"http://`+books.host+`"`, 1)
	// A request may be 4 KiB, beside what the chapter's settings say.
	settingsOfTheCase = strings.Replace(settingsOfTheCase, `"outbound":`, `"limits": {"request_bytes": 4096}, "outbound":`, 1)
	define := func(base, definition string) (int, string) {
		t.Helper()
		return ask(t, client, "POST", base+"/v1/define_action", key, `{"definition": `+definition+`}`)
	}

	// Told nothing, the server calls no host: an action that sends a
	// request cannot be defined (item 2), and the refusal says where the
	// hosts are listed.
	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	if !strings.Contains(log.String(), "its actions may call no host") {
		t.Errorf("a server told nothing says:\n%s", log)
	}
	if status, body := define("http://"+addr, definition); status != 422 || !strings.Contains(body, `"code":"not_supported"`) || !strings.Contains(body, "its settings list the hosts it may call") {
		t.Errorf("a request defined on a server that calls no host: %d %s", status, body)
	}
	stop()

	// The operator lists the host of the books for the connection books,
	// keeps its token in a file, and lets a request be 4 KiB.
	settingsFile := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(settingsOfTheCase), 0o600); err != nil {
		t.Fatal(err)
	}
	secretsDir := filepath.Join(dir, "secrets")
	token := filepath.Join(secretsDir, "books", "token")
	if err := os.MkdirAll(filepath.Dir(token), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("tok-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, log, stop = serving(t, path, "--listen", "127.0.0.1:0", "--settings", settingsFile, "--secrets-dir", secretsDir)
	base := "http://" + addr
	if !strings.Contains(log.String(), "its actions may call over the connection books, http://"+books.host) {
		t.Errorf("the server says:\n%s", log)
	}

	// The action is defined, and the server can run it.
	if status, body := define(base, definition); status != 200 || !strings.Contains(body, `"name":"books.post","version":1`) {
		t.Fatalf("define_action: %d %s", status, body)
	}
	_, catalogue := ask(t, client, "POST", base+"/v1/list_actions", key, "{}")
	if at := strings.Index(catalogue, `"name":"books.post","kind":"declared"`); at < 0 || !strings.Contains(catalogue[at:], `"available":true`) {
		t.Errorf("the action in the catalogue of the server: %s", catalogue)
	}
	// What the settings do not list is refused when it is defined.
	for name, c := range map[string]struct{ definition, words string }{
		"a host the connection does not have": {strings.Replace(definition, books.host, "evil.example", 1), `"code":"not_allowed","path":"/request/url"`},
		"a connection the settings do not have": {strings.Replace(strings.Replace(definition, `"name": "books", "secrets"`, `"name": "slack", "secrets"`, 1), "books.post", "slack.post", 1),
			`"code":"unknown_connection","path":"/connection/name"`},
		"a request without a connection, to a host on no list": {`{"name": "hook.send", "request": {"url": "\"http://` + books.host + `/in\""}}`, `"code":"not_allowed","path":"/request/url"`},
	} {
		if status, body := define(base, c.definition); status != 422 || !strings.Contains(body, c.words) {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	// An action that a Go function runs is refused when it is defined,
	// with the code its rows would wait with, and told the way that
	// works on a server (item 6).
	status, body := define(base, `{"name": "mail.send", "params": {"to": "text"}}`)
	if status != 422 || !strings.Contains(body, `"code":"not_registered","path":"/name"`) || !strings.Contains(body, "give the definition a request") || !strings.Contains(body, "fadodb serve takes none") {
		t.Errorf("an action of a Go function, defined on a server: %d %s", status, body)
	}

	// Run once, the action reaches the books with the token of the file.
	try := func() string {
		t.Helper()
		_, body := ask(t, client, "POST", base+"/v1/try_action", key, `{"action": "books.post", "args": {"amount": 120}}`)
		return body
	}
	if body := try(); !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"entry":"E-1"`) || books.last() != `Bearer tok-one {"amount":120}` {
		t.Fatalf("the action, tried: %s\nthe books got: %s", body, books.last())
	}
	// The operator changes the token; the next request carries the new
	// one, and the server was not restarted (item 5).
	if err := os.WriteFile(token, []byte("tok-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body := try(); !strings.Contains(body, `"ok":true`) || books.last() != `Bearer tok-two {"amount":120}` {
		t.Errorf("after the token was changed: %s\nthe books got: %s", body, books.last())
	}
	// Without the file, the environment.
	if err := os.Remove(token); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FADODB_SECRET_BOOKS__TOKEN", "tok-three")
	if body := try(); !strings.Contains(body, `"ok":true`) || books.last() != `Bearer tok-three {"amount":120}` {
		t.Errorf("with the token in the environment: %s\nthe books got: %s", body, books.last())
	}
	// In neither place: no answer, said by the names the operator needs.
	// Nothing was sent, and the words hold no directory of the machine.
	t.Setenv("FADODB_SECRET_BOOKS__TOKEN", "")
	sent := books.count()
	if body := try(); !strings.Contains(body, `"code":"secret_unavailable"`) || !strings.Contains(body, "no file books/token") || !strings.Contains(body, "FADODB_SECRET_BOOKS__TOKEN") ||
		strings.Contains(body, dir) || books.count() != sent {
		t.Errorf("without the token: %s", body)
	}

	// A commit queues the action, the server's worker sends it, and what
	// the books answer is written into the order.
	if err := os.WriteFile(token, []byte("tok-four"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, body := ask(t, client, "POST", base+"/v1/define_class", key, `{"definition": `+orderOfTheBooks+`}`); status != 200 {
		t.Fatalf("define_class: %d %s", status, body)
	}
	// The agent of sales, made by Olga, makes the order and sends it.
	if status, body := ask(t, client, "POST", base+"/v1/create_user", key, `{"id": "sales-agent", "kind": "agent", "groups": ["sales"]}`); status != 200 {
		t.Fatalf("create_user: %d %s", status, body)
	}
	_, body = ask(t, client, "POST", base+"/v1/create_key", key, `{"user": "sales-agent"}`)
	_, agent, found := strings.Cut(body, `"secret":"`)
	agent, _, _ = strings.Cut(agent, `"`)
	if !found || !strings.HasPrefix(agent, "fado_") {
		t.Fatalf("create_key: %s", body)
	}
	if status, body := ask(t, client, "POST", base+"/v1/create", agent, `{"class": "Order", "id": "o-1", "values": {"amount": 250}}`); status != 200 {
		t.Fatalf("create: %d %s", status, body)
	}
	if status, body := ask(t, client, "POST", base+"/v1/transition", agent, `{"class": "Order", "id": "o-1", "transition": "send"}`); status != 200 {
		t.Fatalf("transition: %d %s", status, body)
	}
	entry := fmt.Sprintf(`"entry":"E-%d"`, sent+1)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		_, body := ask(t, client, "POST", base+"/v1/get", agent, `{"class": "Order", "id": "o-1"}`)
		if strings.Contains(body, entry) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the order did not get its entry: %s\nthe books got: %s\n%s", body, books.last(), log)
		}
	}
	if books.last() != `Bearer tok-four {"amount":250}` {
		t.Errorf("the books got: %s", books.last())
	}

	// The limits of the settings are the door's: a request over 4 KiB is
	// refused.
	if status, body := ask(t, client, "POST", base+"/v1/create", agent, `{"class": "Order", "values": {"amount": 1}, "id": "`+strings.Repeat("x", 5000)+`"}`); status != 413 || !strings.Contains(body, "too_large") {
		t.Errorf("a request beyond the limit of the settings: %d %s", status, body)
	}
	if code := stop(); code != 0 {
		t.Errorf("the server ended with %d:\n%s", code, log)
	}
	// No secret is in what the server wrote.
	for _, secret := range []string{"tok-one", "tok-two", "tok-three", "tok-four", agent} {
		if strings.Contains(log.String(), secret) {
			t.Errorf("the server wrote the secret %q:\n%s", secret, log)
		}
	}

	// Served again without the settings, the action is still defined and
	// cannot run: the server says so when it starts.
	_, log, stop = serving(t, path, "--listen", "127.0.0.1:0")
	if !strings.Contains(log.String(), "the action books.post sends its request over the connection books, which the settings do not have: it does not run") {
		t.Errorf("a server without the settings of its actions says:\n%s", log)
	}
	stop()
	// And with the settings and no token, which secret is missing.
	_, log, stop = serving(t, path, "--listen", "127.0.0.1:0", "--settings", settingsFile)
	if !strings.Contains(log.String(), "the action books.post reads the secret token of the connection books, and the server's environment has no FADODB_SECRET_BOOKS__TOKEN") {
		t.Errorf("a server without the secret of its action says:\n%s", log)
	}
	stop()
}

// A database that a program of the company's own kept may hold actions
// that Go functions run. The server runs none (ADR-0059, item 6): it
// says which they are when it starts, and they do not run on it.
func TestAServedDatabaseWithActionsOfGoFunctions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acme.fado")
	if code, _, said := tool("init", path); code != 0 {
		t.Fatal(said)
	}
	db, err := fado.Open(path, fado.Options{Users: true, Actions: map[string]fado.ActionFunc{
		"mail.send": func(context.Context, *fado.ActionCall) (fado.Values, error) { return nil, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DefineAction(context.Background(), []byte(`{"name": "mail.send", "params": {"to": "text"}}`), fado.As(fado.Principal{System: []string{fado.GroupDesigners}})); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if code, _, said := tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path); code != 0 {
		t.Fatal(said)
	}
	_, key, _ := tool("key", "new", "olga", "--db", path)

	addr, log, stop := serving(t, path, "--listen", "127.0.0.1:0")
	defer stop()
	if !strings.Contains(log.String(), "the database has actions that a Go function runs, and this server runs none: mail.send") {
		t.Errorf("the server says:\n%s", log)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	// The catalogue says the same to a caller.
	status, body := ask(t, client, "POST", "http://"+addr+"/v1/list_actions", strings.TrimSpace(key), "{}")
	if status != 200 || !strings.Contains(body, `"name":"mail.send","kind":"registered"`) {
		t.Fatalf("list_actions: %d %s", status, body)
	}
	if at := strings.Index(body, `"name":"mail.send"`); !strings.Contains(body[at:], `"available":false`) {
		t.Errorf("the action of a Go function, in the catalogue of a server: %s", body[at:])
	}
	status, body = ask(t, client, "POST", "http://"+addr+"/v1/try_action", strings.TrimSpace(key), `{"action": "mail.send", "args": {"to": "olga"}}`)
	if status != 422 || !strings.Contains(body, `"code":"not_registered"`) {
		t.Errorf("the action of a Go function, tried on a server: %d %s", status, body)
	}
}
