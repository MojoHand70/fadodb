package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The webhook of examples/actions: its address and its signing key are
// secrets of the connection webhook, which the program keeps.
// ada keeps the database: trying an action is an administrator's.
const ada = `{"id": "ada", "kind": "person", "system_groups": ["administrators"]}`

const webhook = `{
  "name": "webhook.send",
  "params": {"topic": {"type": "text"}},
  "returns": {"status": {"type": "number"}},
  "connection": {"name": "webhook", "secrets": ["url", "signing_key"]},
  "request": {"method": "POST", "url": "SECRET(\"url\")", "sign": "signing_key"},
  "result": {"status": "STATUS"}
}`

// A database sends requests only to the hosts its options list, with the
// secrets of the program's directory of secrets and its environment, as
// fadodb serve does (ADR-0101, step B).
func TestTheRequestsOfActions(t *testing.T) {
	heard := make(chan *http.Request, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		heard <- r
		w.WriteHeader(http.StatusAccepted)
	}))
	defer receiver.Close()

	// Without hosts, nothing is sent, and a definition that sends is
	// refused, as in a Go program that gives no Outbound.
	quiet := opened(t, "")
	answer := execCommand(quiet, designer, "define_action", `{"definition": `+webhook+`}`)
	if e := refused(t, answer); e["code"] != "bad_definition" || !strings.Contains(string(answer), `"code":"not_supported","path":"/request"`) {
		t.Fatalf("a request was defined with no host allowed: %s", answer)
	}

	secrets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(secrets, "webhook"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "webhook", "url"), []byte(receiver.URL+"/hook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FADODB_SECRET_WEBHOOK__SIGNING_KEY", "the key of the receiver")

	n := opened(t, `{"outbound": {"connections": {"webhook": ["`+receiver.URL+`"]}}, "secrets_dir": `+quote(secrets)+`}`)
	done(t, execCommand(n, designer, "define_action", `{"definition": `+webhook+`}`))
	got := done(t, execCommand(n, ada, "try_action", `{"action": "webhook.send", "args": {"topic": "invoice.booked"}}`))
	if !strings.Contains(jsonOf(got), "202") {
		t.Fatalf("the action answered %v", got)
	}
	r := <-heard
	if r.URL.Path != "/hook" || r.Header.Get("FadoDB-Signature") == "" {
		t.Fatalf("the receiver heard %s with the signature %q", r.URL.Path, r.Header.Get("FadoDB-Signature"))
	}

	// A secret that is in neither place says so, in the program's words.
	t.Setenv("FADODB_SECRET_WEBHOOK__SIGNING_KEY", "")
	got = answered(t, execCommand(n, ada, "try_action", `{"action": "webhook.send", "args": {"topic": "invoice.booked"}}`))
	if text := jsonOf(got); !strings.Contains(text, "the program's secrets directory has no file webhook/signing_key") {
		t.Fatalf("a missing secret answered %s", text)
	}

	for _, c := range []struct{ options, path string }{
		{`{"outbound": {"hosts": ["https://"]}}`, "/outbound"},
		{`{"outbound": {"hosts": "slack.com"}}`, "/outbound/hosts"},
		{`{"outbound": {"host": ["slack.com"]}}`, "/outbound/host"},
		{`{"secrets_dir": 7}`, "/secrets_dir"},
	} {
		got, refusal := openDatabase(filepath.Join(t.TempDir(), "x.fado"), c.options)
		if got != 0 {
			closeDatabase(got)
			t.Errorf("%s opened a database", c.options)
			continue
		}
		if e := refused(t, refusal); e["path"] != c.path {
			t.Errorf("%s: refused %v, want the place %s", c.options, e, c.path)
		}
	}
}
